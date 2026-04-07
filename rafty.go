package rafty

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	etcdraft "go.etcd.io/etcd/raft/v3"
)

// Rafty
type Rafty[T any, T2 Work[T]] struct {
	logger Logger

	raft           *raftNode[T, T2]
	raftID         ServerID
	discoverer     Discoverer
	foreman        Foreman[T, T2]
	distributor    Distributor[T, T2]
	currentServers []Server

	listeningAddress  string
	listeningPort     int
	advertisedAddress string

	ch          chan RaftLog[T, T2]
	currentWork RaftLog[T, T2]
	workCancel  map[string]context.CancelFunc

	startFunc func(context.Context, T2)
	doneCh    chan struct{}
}

func New[T any, T2 Work[T]](disco Discoverer, foreman Foreman[T, T2], start func(context.Context, T2), opts ...Option[T, T2]) (*Rafty[T, T2], error) {
	r := &Rafty[T, T2]{
		logger:         &StdLogger{},
		startFunc:      start,
		discoverer:     disco,
		foreman:        foreman,
		currentServers: make([]Server, 0),
		ch:             make(chan RaftLog[T, T2]),
		currentWork: RaftLog[T, T2]{
			Disco: make(map[ServerID][]T2),
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
		r.logger = &StdLogger{}
	}
	if r.distributor == nil {
		r.distributor = defaultDistributor[T, T2]{}
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

	r.raftID = ServerID(fmt.Sprintf("%s:%d", r.advertisedAddress, r.listeningPort))

	return r, nil
}

type Option[T any, T2 Work[T]] func(*Rafty[T, T2]) error

func WithLogger[T any, T2 Work[T]](logger Logger) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.logger = logger
		return nil
	}
}

func RaftListeningAddressPort[T any, T2 Work[T]](address string, port int) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.listeningAddress = address
		r.listeningPort = port
		return nil
	}
}

func RaftAdvertisedAddress[T any, T2 Work[T]](address string) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.advertisedAddress = address
		return nil
	}
}

func WithDistributor[T any, T2 Work[T]](distrib Distributor[T, T2]) Option[T, T2] {
	return func(r *Rafty[T, T2]) error {
		r.distributor = distrib
		return nil
	}
}

func (r *Rafty[T, T2]) Start(ctx context.Context) (context.Context, error) {
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
	for _, server := range servers {
		if server.ID == r.raftID {
			found = true
			break
		}
	}

	if !found {
		r.logger.Warnf("Did not find myself in first servers list, retrying ...")
		goto NEWSERVERS
	}

	r.currentServers = servers

	config := RaftConfig{
		ListeningAddress:  r.listeningAddress,
		ListeningPort:     r.listeningPort,
		AdvertisedAddress: r.advertisedAddress,
		Logger:            r.logger,
		ServerID:          r.raftID,
	}

	var err error
	r.raft, err = NewRaft(config, r.ch, servers)
	if err != nil {
		return nil, err
	}

	limboctx, limbocancel := context.WithCancel(context.Background())

	go func(ctx context.Context) {
		var candidate bool
		var candidateSince time.Time
		for {
			select {
			case <-time.After(5 * time.Second):
				if r.raft.State() == etcdraft.StateCandidate {
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
					r.updateServers(r.discoverer.GetServers())
					r.leader(r.foreman.GetWorks())
				} else {
					r.logger.Infof("This node is no longer the leader, stoping the foreman")
					r.foreman.Stop()
				}

			case <-r.discoverer.NewServers():
				servers := r.discoverer.GetServers()

				r.logger.Infof("Received new servers from discovery: %v", servers)

				if r.raft.State() != etcdraft.StateLeader {
					r.currentServers = servers
					continue
				}

				r.logger.Infof("Apply new servers from discovery: %v", servers)

				r.updateServers(servers)
				r.leader(r.foreman.GetWorks())

			case <-r.foreman.NewWorks():
				if r.raft.State() != etcdraft.StateLeader {
					continue
				}

				r.logger.Infof("Foreman signaled new work")

				r.leader(r.foreman.GetWorks())

			case <-limboctx.Done():
				r.logger.Warnf("We are in a Raft Limbo")
				r.logger.Tracef("Quitting Rafty leader loop")
				if err := r.raft.Shutdown(); err != nil {
					r.logger.Errorf("Error while shutting down raft: %v", err)
				}
				r.doneCh <- struct{}{}
				return

			case <-ctx.Done():
				if len(r.currentServers) > 1 && r.raft.State() == etcdraft.StateLeader {
					r.logger.Infof("Transfering leadership")
					r.raft.TransferLeadership()
				}

				if d, ok := r.discoverer.(Finalizer); ok {
					select {
					case <-d.Done():
					case <-time.After(5 * time.Second):
						r.logger.Errorf("Timeout waiting for discoverer to finalize")
					}
				}

				if err := r.raft.Shutdown(); err != nil {
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

	if err := r.raft.Propose(js); err != nil {
		r.logger.Errorf("Failed to apply new log: %s", err)
	}
}

func (r *Rafty[T, T2]) removeServer(id ServerID) {
	r.logger.Infof("Removing server from cluster: %s", id)
	if err := r.raft.RemoveServer(id); err != nil {
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

func (r *Rafty[T, T2]) updateServers(servers []Server) {
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
			r.logger.Debugf("Adding server to cluster: %s found=%v updated=%v", server.ID, found, updated)
			if err := r.raft.AddServer(server); err != nil {
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
			if err := r.raft.RemoveServer(currentServer.ID); err != nil {
				r.logger.Errorf("Failed to remove server from cluster: %s", err)
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

func (r *Rafty[T, T2]) cancelWork(w Work[T]) {
	if cancel, ok := r.workCancel[w.ID()]; ok {
		cancel()
		delete(r.workCancel, w.ID())
	}
}

type defaultDistributor[T any, T2 Work[T]] struct{}

func (defaultDistributor[T, T2]) Distribute(servers []Server, works []T2) map[ServerID][]T2 {
	output := make(map[ServerID][]T2)
	if len(servers) == 0 || len(works) == 0 {
		return output
	}

	sorted := append([]Server(nil), servers...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	serverCount := len(sorted)
	workCount := len(works)
	capacity := int(math.Ceil(float64(workCount) / float64(serverCount)))

	for i, work := range works {
		server := sorted[i%serverCount]
		if _, ok := output[server.ID]; !ok {
			output[server.ID] = make([]T2, 0, capacity)
		}
		output[server.ID] = append(output[server.ID], work)
	}

	return output
}
