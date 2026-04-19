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
	discovered     []Server
	currentServers []Server
	recoveryEpoch  uint64
	formationPhase bool
	clusterReady   bool
	expectedNodes  int

	listeningAddress  string
	listeningPort     int
	advertisedAddress string

	ch          chan RaftLog[T, T2]
	currentWork RaftLog[T, T2]
	workCancel  map[string]context.CancelFunc
	foremanOn   bool

	startFunc func(context.Context, T2)
	doneCh    chan struct{}
}

func New[T any, T2 Work[T]](disco Discoverer, foreman Foreman[T, T2], start func(context.Context, T2), opts ...Option[T, T2]) (*Rafty[T, T2], error) {
	r := &Rafty[T, T2]{
		logger:         &StdLogger{},
		startFunc:      start,
		discoverer:     disco,
		foreman:        foreman,
		discovered:     make([]Server, 0),
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
	selfAddress := ServerAddress(fmt.Sprintf("%s:%d", r.advertisedAddress, r.listeningPort))
	for _, server := range servers {
		if server.Address == selfAddress {
			r.raftID = server.ID
			found = true
			break
		}
	}

	if !found {
		r.logger.Warnf("Did not find myself in first servers list, retrying ...")
		goto NEWSERVERS
	}

	r.discovered = append([]Server(nil), servers...)
	bootstrap := shouldBootstrap(servers, r.advertisedAddress, r.listeningPort)
	if recoverer, ok := r.discoverer.(Recoverer); ok {
		recovery := recoverer.GetRecoveryState()
		if recovery.Epoch > 0 && len(recovery.Members) >= recovery.MinRequired {
			r.recoveryEpoch = recovery.Epoch
			r.formationPhase = true
			servers = append([]Server(nil), recovery.Members...)
			r.discovered = append([]Server(nil), recovery.Members...)
			bootstrap = r.raftID == recovery.Seed
		}
	}
	if sizer, ok := r.discoverer.(ClusterSizer); ok {
		r.expectedNodes = sizer.ExpectedServers()
	}
	if bootstrap {
		r.currentServers = []Server{{ID: r.raftID, Address: selfAddress, Suffrage: Voter}}
	} else {
		r.currentServers = nil
	}

	config := RaftConfig{
		ListeningAddress:  r.listeningAddress,
		ListeningPort:     r.listeningPort,
		AdvertisedAddress: r.advertisedAddress,
		Logger:            r.logger,
		ServerID:          r.raftID,
	}

	var err error
	r.raft, err = NewRaft(config, r.ch, servers, bootstrap)
	if err != nil {
		return nil, err
	}

	limboctx, limbocancel := context.WithCancel(context.Background())

	go func(ctx context.Context) {
		var recoverySince time.Time
		var recoveryAttempted bool
		recoverer, hasRecovery := r.discoverer.(Recoverer)
		self := Server{ID: r.raftID, Address: selfAddress, Suffrage: Voter}
		for {
			select {
			case <-time.After(5 * time.Second):
				liveCount := len(r.discovered)
				required := (len(r.currentServers) / 2) + 1
				minRecoveryMembers := required
				leaderAddr, _ := r.raft.LeaderWithID()
				noLeader := leaderAddr == ""
				membershipChanged := ServersDiffer(r.currentServers, r.discovered)
				if hasRecovery {
					if state := recoverer.GetRecoveryState(); state.MinRequired > minRecoveryMembers {
						minRecoveryMembers = state.MinRequired
					}
				}
				needsRecovery := len(r.currentServers) > 0 &&
					noLeader &&
					liveCount >= minRecoveryMembers &&
					membershipChanged
				if needsRecovery {
					if recoverySince.IsZero() {
						recoverySince = time.Now()
					} else if time.Since(recoverySince) > 10*time.Second {
						if hasRecovery {
							if !recoveryAttempted {
								r.logger.Warnf("Attempting discovery-led recovery with %d/%d live members", liveCount, len(r.currentServers))
								if err := recoverer.StartRecovery(self, r.discovered); err != nil {
									r.logger.Errorf("Failed to start recovery: %v", err)
								}
								recoveryAttempted = true
							}
						} else {
							limbocancel()
						}
					}
				} else {
					recoverySince = time.Time{}
					recoveryAttempted = false
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
		recoverer, hasRecovery := r.discoverer.(Recoverer)
		reconcileTicker := time.NewTicker(time.Second)
		defer reconcileTicker.Stop()
		for {
			select {
			case leader := <-r.raft.LeaderCh():
				if leader {
					if r.formationPhase {
						r.logger.Infof("This node is now the leader, forming the recovery cluster")
					} else if r.shouldActivateWork(recoverer, hasRecovery) {
						r.logger.Infof("This node is now the leader, starting the foreman")
						r.startForeman()
					} else {
						r.logger.Infof("This node is now the leader, waiting for cluster readiness")
					}
					r.currentServers = r.raft.CurrentServers()
					r.updateServers(r.discovered)
					if r.shouldStartForeman(recoverer, hasRecovery) {
						r.finishFormation()
					} else if r.shouldActivateWork(recoverer, hasRecovery) {
						r.leader(r.foreman.GetWorks())
					}
				} else {
					r.logger.Infof("This node is no longer the leader, stoping the foreman")
					r.stopForeman()
				}

			case <-r.raft.MembershipCh():
				r.currentServers = r.raft.CurrentServers()
				if r.raft.State() != etcdraft.StateLeader {
					continue
				}

				if ServersDiffer(r.currentServers, r.discovered) {
					r.updateServers(r.discovered)
				}

				r.logger.Infof("Applied new raft membership: %v", r.currentServers)
				if r.shouldStartForeman(recoverer, hasRecovery) {
					r.finishFormation()
				} else if r.shouldActivateWork(recoverer, hasRecovery) {
					r.leader(r.foreman.GetWorks())
				}

			case <-r.discoverer.NewServers():
				servers := r.discoverer.GetServers()

				if ServersDiffer(r.discovered, servers) {
					r.discovered = append(r.discovered[:0], servers...)
					r.logger.Infof("Received new servers from discovery: %v", servers)

					if r.raft.State() != etcdraft.StateLeader {
						continue
					}

					r.logger.Infof("Apply new servers from discovery: %v", servers)

					r.updateServers(servers)
					if r.shouldStartForeman(recoverer, hasRecovery) {
						r.finishFormation()
					} else if r.shouldActivateWork(recoverer, hasRecovery) && !r.foremanOn {
						r.logger.Infof("Cluster is now ready, starting the foreman")
						r.startForeman()
						r.leader(r.foreman.GetWorks())
					}
				}

			case <-reconcileTicker.C:
				if r.raft.State() != etcdraft.StateLeader {
					continue
				}
				r.currentServers = r.raft.CurrentServers()
				if ServersDiffer(r.currentServers, r.discovered) {
					r.updateServers(r.discovered)
				}
				if r.shouldStartForeman(recoverer, hasRecovery) {
					r.finishFormation()
				}

			case <-func() chan struct{} {
				if !hasRecovery {
					return nil
				}
				return recoverer.RecoveryCh()
			}():
				state := recoverer.GetRecoveryState()
				if state.Epoch <= r.recoveryEpoch {
					continue
				}

				r.logger.Warnf("Detected recovery epoch change from %d to %d", r.recoveryEpoch, state.Epoch)
				r.recoveryEpoch = state.Epoch
				limbocancel()

			case <-r.foreman.NewWorks():
				if r.raft.State() != etcdraft.StateLeader || r.formationPhase {
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

func (r *Rafty[T, T2]) startForeman() {
	if r.foremanOn {
		return
	}
	r.foreman.Start()
	r.foremanOn = true
}

func (r *Rafty[T, T2]) stopForeman() {
	if r.foremanOn {
		r.foreman.Stop()
		r.foremanOn = false
	}
	r.cancelAllWork()
}

func (r *Rafty[T, T2]) fullClusterReady() bool {
	if r.expectedNodes <= 0 {
		return len(r.currentServers) > 0 && !ServersDiffer(r.currentServers, r.discovered)
	}
	return len(r.discovered) >= r.expectedNodes &&
		len(r.currentServers) >= r.expectedNodes &&
		!ServersDiffer(r.currentServers, r.discovered)
}

func (r *Rafty[T, T2]) shouldActivateWork(recoverer Recoverer, hasRecovery bool) bool {
	if r.raft.State() != etcdraft.StateLeader || r.formationPhase {
		return false
	}
	if r.clusterReady {
		return true
	}
	if !r.fullClusterReady() {
		return false
	}
	r.clusterReady = true
	return true
}

func (r *Rafty[T, T2]) shouldStartForeman(recoverer Recoverer, hasRecovery bool) bool {
	if !r.formationPhase || r.raft.State() != etcdraft.StateLeader {
		return false
	}
	if !r.fullClusterReady() {
		return false
	}
	if !hasRecovery {
		return true
	}
	state := recoverer.GetRecoveryState()
	return len(r.currentServers) >= state.MinRequired
}

func (r *Rafty[T, T2]) finishFormation() {
	if !r.formationPhase {
		return
	}
	r.formationPhase = false
	r.clusterReady = true
	r.logger.Infof("Recovery cluster formed, starting the foreman")
	r.startForeman()
	r.leader(r.foreman.GetWorks())
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
	promotions := make([]Server, 0)

	for _, server := range servers {
		found := false
		updated := false
		currentSuffrage := server.Suffrage
		for _, currentServer := range r.currentServers {
			if server.ID == currentServer.ID {
				found = true
				currentSuffrage = currentServer.Suffrage
				if server != currentServer {
					updated = true
				}
				break
			}
		}

		if !found || updated {
			candidate := server
			if !found && candidate.Suffrage == Voter {
				candidate.Suffrage = Nonvoter
			}
			if found && currentSuffrage == Nonvoter && server.Suffrage == Voter {
				promotions = append(promotions, server)
				continue
			}

			r.logger.Debugf("Adding server to cluster: %s found=%v updated=%v suffrage=%v", candidate.ID, found, updated, candidate.Suffrage)
			if err := r.raft.AddServer(candidate); err != nil {
				r.logger.Errorf("Failed to add server to cluster: %s", err)
			}
			return
		}
	}

	for _, server := range promotions {
		if !r.raft.CanPromote(server.ID) {
			continue
		}
		r.logger.Debugf("Promoting server to voter: %s", server.ID)
		if err := r.raft.AddServer(server); err != nil {
			r.logger.Errorf("Failed to promote server to voter: %s", err)
		}
		return
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
			return
		}
	}

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

func shouldBootstrap(servers []Server, advertisedAddress string, port int) bool {
	selfAddress := ServerAddress(fmt.Sprintf("%s:%d", advertisedAddress, port))
	voters := make([]Server, 0, len(servers))
	for _, server := range servers {
		if server.Suffrage == Voter {
			voters = append(voters, server)
		}
	}
	if len(voters) == 0 {
		return false
	}
	sort.Slice(voters, func(i, j int) bool {
		return voters[i].Address < voters[j].Address
	})
	return voters[0].Address == selfAddress
}
