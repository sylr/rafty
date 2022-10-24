package rafty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"

	distribconsistent "sylr.dev/rafty/distributor/consistent"
	"sylr.dev/rafty/interfaces"
	"sylr.dev/rafty/logger"
)

// Rafty
type Rafty[T any, T2 interfaces.Work[T]] struct {
	logger   interfaces.Logger
	hcLogger hclog.Logger

	raft           *raft.Raft
	raftID         raft.ServerID
	discoverer     interfaces.Discoverer
	foreman        interfaces.Foreman[T, T2]
	distributor    interfaces.Distributor[T, T2]
	currentServers []raft.Server

	listeningAddress  string
	listeningPort     int
	advertisedAddress string

	ch          chan RaftLog[T, T2]
	currentWork RaftLog[T, T2]
	workCancel  map[string]context.CancelFunc

	startFunc func(context.Context, T2)
	doneCh    chan struct{}
}

func New[T any, T2 interfaces.Work[T]](disco interfaces.Discoverer, foreman interfaces.Foreman[T, T2], start func(context.Context, T2), opts ...Option[T, T2]) (*Rafty[T, T2], error) {
	r := &Rafty[T, T2]{
		logger:         &logger.StdLogger{},
		startFunc:      start,
		discoverer:     disco,
		foreman:        foreman,
		currentServers: make([]raft.Server, 0),
		ch:             make(chan RaftLog[T, T2]),
		currentWork: RaftLog[T, T2]{
			Disco: make(map[raft.ServerID][]T2),
		},
		workCancel: make(map[string]context.CancelFunc),
		doneCh:     make(chan struct{}),
	}

	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}

	if r.logger == nil {
		r.logger = &logger.StdLogger{}
	}
	if r.distributor == nil {
		r.distributor, _ = distribconsistent.New[T, T2]()
	}
	if len(r.listeningAddress) == 0 {
		r.listeningAddress = "0.0.0.0"
	}
	if r.listeningPort == 0 {
		r.listeningPort = 10000
	}
	if len(r.advertisedAddress) == 0 {
		r.advertisedAddress = "127.0.0.1"
	}

	r.raftID = raft.ServerID(fmt.Sprintf("%s:%d", r.advertisedAddress, r.listeningPort))

	config := RaftConfig{
		ListeningAddress:  r.listeningAddress,
		ListeningPort:     r.listeningPort,
		AdvertisedAddress: r.advertisedAddress,
		Logger:            r.logger,
		HCLogger:          r.hcLogger,
		ServerID:          r.raftID,
	}

	if r.raft == nil {
		r.logger.Debugf("No raft instance provided, creating a default one")
		var err error
		r.raft, err = NewRaft(config, r.ch)

		if err != nil {
			return nil, err
		}
	}

	return r, nil
}

type Option[T any, T2 interfaces.Work[T]] func(*Rafty[T, T2]) error

func Logger[T any, T2 interfaces.Work[T]](logger interfaces.Logger) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.logger = logger
		return nil
	}
}

func HCLogger[T any, T2 interfaces.Work[T]](logger hclog.Logger) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.hcLogger = logger
		return nil
	}
}

func RaftListeningAddressPort[T any, T2 interfaces.Work[T]](address string, port int) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.listeningAddress = address
		r.listeningPort = port
		return nil
	}
}

func RaftAdvertisedAddress[T any, T2 interfaces.Work[T]](address string) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.advertisedAddress = address
		return nil
	}
}

func Distributor[T any, T2 interfaces.Work[T]](distrib interfaces.Distributor[T, T2]) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.distributor = distrib
		return nil
	}
}

func (r *Rafty[T, T2]) Start(ctx context.Context) (context.Context, error) {
	configuration := raft.Configuration{}

NEWSERVERS:
	r.logger.Tracef("Waiting for disco to send first servers list")
	select {
	case <-r.discoverer.NewServers():
	case <-ctx.Done():
		return nil, fmt.Errorf("context canceled while waiting for first servers list")
	}

	servers := r.discoverer.GetServers()
	r.logger.Tracef("Received first servers list: %v", servers)

	if len(servers) == 0 {
		r.logger.Warnf("Received an empty first servers list, retrying ...")
		goto NEWSERVERS
	}

	found := false
	isVoter := false
	for _, server := range servers {
		if server.ID == r.raftID {
			found = true
			if server.Suffrage == raft.Voter {
				isVoter = true
			}
			break
		}
	}

	if !found {
		r.logger.Warnf("Did not find myself in first servers list, retrying ...")
		goto NEWSERVERS
	}

	if isVoter {
		r.logger.Infof("Server is voter, attempting to bootstrap cluster")
		configuration.Servers = append(configuration.Servers, servers...)
		fut := r.raft.BootstrapCluster(configuration)

		if err := fut.Error(); err != nil {
			r.logger.Tracef("Bootstrapping cluster failed: %v", err)
			if !errors.Is(err, raft.ErrCantBootstrap) {
				return nil, fut.Error()
			}
		} else {
			_, leaderID := r.raft.LeaderWithID()
			if leaderID == r.raftID {
				r.updateServers(servers)
			}
		}
	}

	raftObservation := make(chan raft.Observation)
	observer := raft.NewObserver(raftObservation, false, nil)
	r.raft.RegisterObserver(observer)

	limboctx, limbocancel := context.WithCancel(context.Background())

	go func(ctx context.Context) {
		var candidate bool
		var candidateSince time.Time
		for {
			select {
			case <-time.After(5 * time.Second):
				if r.raft.State() == raft.Candidate {
					if !candidate {
						candidateSince = time.Now()
					} else if time.Since(candidateSince) > 10*time.Second {
						limbocancel()
					}
					candidate = true
				} else {
					candidate = false
				}

			case newWork := <-r.ch:
				if newWork.index < r.raft.LastIndex() {
					r.logger.Debugf("Ignoring new outdated log index: %d", newWork.index)
					continue
				}
				r.logger.Infof("Received new raft log (index=%d)", newWork.index)
				r.logger.Tracef("%v", newWork)
				r.manageWork(newWork)

			case <-limboctx.Done():
				r.logger.Warnf("We are in a Raft Limbo")
				r.logger.Tracef("Quitting Rafty worker loop")
				r.cancelAllWork()
				return

			case <-ctx.Done():
				r.logger.Tracef("Quitting Rafty worker loop")
				r.cancelAllWork()
				return
			}
		}
	}(ctx)

	go func(ctx context.Context) {
		for {
			select {
			case leader := <-r.raft.LeaderCh():
				if leader {
					r.logger.Infof("This node is now the leader, starting the foreman")
					r.foreman.Start()

					fut := r.raft.GetConfiguration()
					if err := fut.Error(); err != nil {
						r.logger.Errorf("Error getting raft configuration: %v", err)
					}

					r.updateServers(r.discoverer.GetServers())
					r.leader(r.foreman.GetWorks())
				} else {
					r.logger.Infof("This node is no longer the leader, stoping the foreman")
					r.foreman.Stop()
				}

			case <-r.discoverer.NewServers():
				servers := r.discoverer.GetServers()

				r.logger.Infof("Received new servers from discovery: %v", servers)

				if r.raft.State() != raft.Leader {
					r.currentServers = servers
					continue
				}

				r.logger.Infof("Apply new servers from discovery: %v", servers)

				r.updateServers(servers)
				r.leader(r.foreman.GetWorks())

			case <-r.foreman.NewWorks():
				if r.raft.State() != raft.Leader {
					continue
				}

				r.logger.Infof("Foreman signaled new work")

				r.leader(r.foreman.GetWorks())

			case <-limboctx.Done():
				r.logger.Warnf("We are in a Raft Limbo")
				r.logger.Tracef("Quitting Rafty leader loop")
				fut := r.raft.Shutdown()
				if err := fut.Error(); err != nil {
					r.logger.Errorf("Error while shutting down raft: %w", err)
				}
				r.doneCh <- struct{}{}
				return

			case <-ctx.Done():
				fut := r.raft.GetConfiguration()
				if err := fut.Error(); err != nil {
					r.logger.Errorf("Error getting raft configuration: %v", err)
				} else {
					if len(fut.Configuration().Servers) > 1 && r.raft.State() == raft.Leader {
						r.logger.Infof("Transfering leadership")
						fut := r.raft.LeadershipTransfer()
						if err := fut.Error(); err != nil {
							r.logger.Errorf("Failed to transfer leadership: %v", err)
						}

					OBSERVATIONS:
						for observation := range raftObservation {
							switch obs := observation.Data.(type) {
							case raft.LeaderObservation:
								if obs.Leader != "" {
									r.logger.Infof("Final leader observation: %v", obs)
									break OBSERVATIONS
								}
							}
						}
					}
				}

				if d, ok := r.discoverer.(interfaces.Finalizer); ok {
					select {
					case <-d.Done():
					case <-time.After(5 * time.Second):
						r.logger.Errorf("Timeout waiting for discoverer to finalize")
					}
				}

				sfut := r.raft.Shutdown()
				if err := sfut.Error(); err != nil {
					r.logger.Errorf("Failed shutdown: %v", err)
				}

				r.doneCh <- struct{}{}

				r.logger.Tracef("Quitting Rafty leader loop")
				return
			}
		}
	}(ctx)

	return limboctx, nil
}

func (r *Rafty[T, T2]) Done() chan struct{} {
	return r.doneCh
}

func (r *Rafty[T, T2]) leader(newWork []T2) {
	distributed := r.distributor.Distribute(r.currentServers, newWork)
	r.logger.Infof("Distributed work: %v", distributed)

	js, err := json.Marshal(distributed)
	if err != nil {
		r.logger.Errorf("Error while marshaling work: %s", err)
		return
	}

	fut := r.raft.Apply(js, time.Second)
	if err := fut.Error(); err != nil {
		r.logger.Errorf("Failed to apply new log: %s", err)
	}
}

func (r *Rafty[T, T2]) removeServer(id raft.ServerID) {
	r.logger.Infof("Removing server from cluster: %s", id)
	fut := r.raft.RemoveServer(id, 0, time.Second)
	if err := fut.Error(); err != nil {
		r.logger.Errorf("Failed to remove server from cluster: %s", err)
	}

	indexToRemove := -1
	for i := range r.currentServers {
		if id == r.currentServers[i].ID {
			indexToRemove = i
			break
		}
	}

	if indexToRemove >= 0 {
		r.currentServers = append(r.currentServers[:indexToRemove], r.currentServers[indexToRemove+1:]...)
	}
}

func (r *Rafty[T, T2]) updateServers(servers []raft.Server) {
	for _, server := range servers {
		found := false
		updated := false
		for _, currentServer := range r.currentServers {
			if server.ID == currentServer.ID {
				found = true
				if server != currentServer {
					updated = true
				}
				break
			}
		}

		if !found || updated {
			var fut raft.IndexFuture
			if server.Suffrage == raft.Voter {
				r.logger.Debugf("Adding new server as voter to cluster: %s found=%v updated=%v", server.ID, found, updated)
				fut = r.raft.AddVoter(server.ID, server.Address, 0, time.Second)
			} else {
				r.logger.Debugf("Adding new server as non voter to cluster: %s found=%v updated=%v", server.ID, found, updated)
				fut = r.raft.AddNonvoter(server.ID, server.Address, 0, time.Second)
			}

			if err := fut.Error(); err != nil {
				r.logger.Errorf("Failed to add server to cluster: %s", err)
			}
		}
	}

	for _, currentServer := range r.currentServers {
		found := false
		for _, server := range servers {
			if server.ID == currentServer.ID {
				found = true
			}
		}

		if !found {
			r.logger.Debugf("Removing server from the cluster: %s", currentServer.ID)
			fut := r.raft.RemoveServer(currentServer.ID, 0, time.Second)

			if err := fut.Error(); err != nil {
				r.logger.Errorf("Failed to add server to cluster: %s", err)
				return
			}
		}
	}

	r.currentServers = servers
}

func (r *Rafty[T, T2]) manageWork(newWork RaftLog[T, T2]) {
	removed, added := r.currentWork.Diff(r.raftID, newWork)

	for _, w := range removed {
		r.cancelWork(r.currentWork.Disco[r.raftID][w])
	}

	for _, w := range added {
		r.startWork(newWork.Disco[r.raftID][w])
	}

	r.currentWork.Disco[r.raftID] = newWork.Disco[r.raftID]
}

func (r *Rafty[T, T2]) startWork(w T2) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	go r.startFunc(ctx, w)

	r.workCancel[w.ID()] = cancel
}

func (r *Rafty[T, T2]) cancelAllWork() {
	for _, work := range r.currentWork.Disco[r.raftID] {
		r.cancelWork(work)
	}

	r.currentWork.Disco[r.raftID] = make([]T2, 0)
}

func (r *Rafty[T, T2]) cancelWork(w interfaces.Work[T]) {
	r.workCancel[w.ID()]()
	delete(r.workCancel, w.ID())
}
