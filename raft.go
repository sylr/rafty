package rafty

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	etcdraft "go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

const raftTransportPath = "/__rafty/raft"

type RaftConfig struct {
	ListeningAddress  string
	ListeningPort     int
	AdvertisedAddress string
	Logger            Logger
	ServerID          ServerID
}

type etcdLogger struct {
	logger Logger
}

func (l etcdLogger) Debug(v ...interface{})                 { l.logger.Debugf("%s", fmt.Sprint(v...)) }
func (l etcdLogger) Debugf(format string, v ...interface{}) { l.logger.Debugf(format, v...) }
func (l etcdLogger) Error(v ...interface{})                 { l.logger.Errorf("%s", fmt.Sprint(v...)) }
func (l etcdLogger) Errorf(format string, v ...interface{}) { l.logger.Errorf(format, v...) }
func (l etcdLogger) Info(v ...interface{})                  { l.logger.Infof("%s", fmt.Sprint(v...)) }
func (l etcdLogger) Infof(format string, v ...interface{})  { l.logger.Infof(format, v...) }
func (l etcdLogger) Warning(v ...interface{})               { l.logger.Warnf("%s", fmt.Sprint(v...)) }
func (l etcdLogger) Warningf(format string, v ...interface{}) {
	l.logger.Warnf(format, v...)
}
func (l etcdLogger) Fatal(v ...interface{})                 { panic(fmt.Sprint(v...)) }
func (l etcdLogger) Fatalf(format string, v ...interface{}) { panic(fmt.Sprintf(format, v...)) }
func (l etcdLogger) Panic(v ...interface{})                 { panic(fmt.Sprint(v...)) }
func (l etcdLogger) Panicf(format string, v ...interface{}) { panic(fmt.Sprintf(format, v...)) }

type raftNode[T any, T2 Work[T]] struct {
	logger Logger
	node   etcdraft.Node
	store  *etcdraft.MemoryStorage

	serverID ServerID
	numeric  uint64

	transport *http.Server
	client    *http.Client

	ch chan RaftLog[T, T2]

	mu            sync.RWMutex
	servers       map[uint64]Server
	serverIDs     map[ServerID]uint64
	lastIndex     uint64
	appliedIndex  uint64
	lead          uint64
	isLeader      bool
	leaderCh      chan bool
	stopCh        chan struct{}
	doneCh        chan struct{}
	confChangeSeq uint64
}

func NewRaft[T any, T2 Work[T]](c RaftConfig, ch chan RaftLog[T, T2], initialServers []Server) (*raftNode[T, T2], error) {
	listen := fmt.Sprintf("%s:%d", c.ListeningAddress, c.ListeningPort)
	n := &raftNode[T, T2]{
		logger:   c.Logger,
		store:    etcdraft.NewMemoryStorage(),
		serverID: c.ServerID,
		numeric:  numericServerID(c.ServerID),
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
		ch:        ch,
		servers:   make(map[uint64]Server),
		serverIDs: make(map[ServerID]uint64),
		leaderCh:  make(chan bool, 8),
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}

	n.setServers(initialServers)

	cfg := &etcdraft.Config{
		ID:              n.numeric,
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         n.store,
		MaxSizePerMsg:   1024 * 1024,
		MaxInflightMsgs: 256,
		CheckQuorum:     true,
		PreVote:         true,
		Logger:          etcdLogger{logger: c.Logger},
	}

	peers := make([]etcdraft.Peer, 0, len(initialServers))
	for _, server := range initialServers {
		peers = append(peers, etcdraft.Peer{
			ID:      numericServerID(server.ID),
			Context: mustJSON(server),
		})
	}

	n.node = etcdraft.StartNode(cfg, peers)

	mux := http.NewServeMux()
	mux.HandleFunc(raftTransportPath, n.handleMessage)
	n.transport = &http.Server{
		Addr:    listen,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		n.node.Stop()
		return nil, err
	}

	c.Logger.Infof("Listen address: %v, Advertised address: %s:%d", listen, c.AdvertisedAddress, c.ListeningPort)

	go func() {
		if err := n.transport.Serve(ln); err != nil && err != http.ErrServerClosed {
			n.logger.Errorf("raft transport failed: %v", err)
		}
	}()

	go n.tickLoop()
	go n.readyLoop()

	return n, nil
}

func (n *raftNode[T, T2]) Done() <-chan struct{} {
	return n.doneCh
}

func (n *raftNode[T, T2]) LeaderCh() <-chan bool {
	return n.leaderCh
}

func (n *raftNode[T, T2]) State() etcdraft.StateType {
	return n.node.Status().RaftState
}

func (n *raftNode[T, T2]) LastIndex() uint64 {
	return atomic.LoadUint64(&n.lastIndex)
}

func (n *raftNode[T, T2]) LeaderWithID() (string, ServerID) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	server, ok := n.servers[n.lead]
	if !ok {
		return "", ""
	}

	return string(server.Address), server.ID
}

func (n *raftNode[T, T2]) Propose(data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return n.node.Propose(ctx, data)
}

func (n *raftNode[T, T2]) AddServer(server Server) error {
	n.mu.Lock()
	n.servers[numericServerID(server.ID)] = server
	n.serverIDs[server.ID] = numericServerID(server.ID)
	n.mu.Unlock()

	changeType := raftpb.ConfChangeAddNode
	if server.Suffrage == Nonvoter {
		changeType = raftpb.ConfChangeAddLearnerNode
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	return n.node.ProposeConfChange(ctx, raftpb.ConfChange{
		ID:      atomic.AddUint64(&n.confChangeSeq, 1),
		Type:    changeType,
		NodeID:  numericServerID(server.ID),
		Context: mustJSON(server),
	})
}

func (n *raftNode[T, T2]) RemoveServer(id ServerID) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	return n.node.ProposeConfChange(ctx, raftpb.ConfChange{
		ID:     atomic.AddUint64(&n.confChangeSeq, 1),
		Type:   raftpb.ConfChangeRemoveNode,
		NodeID: numericServerID(id),
	})
}

func (n *raftNode[T, T2]) TransferLeadership() {
	status := n.node.Status()
	if status.RaftState != etcdraft.StateLeader {
		return
	}

	for id := range status.Progress {
		if id == n.numeric {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		n.node.TransferLeadership(ctx, n.numeric, id)
		cancel()
		return
	}
}

func (n *raftNode[T, T2]) Shutdown() error {
	close(n.stopCh)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := n.transport.Shutdown(ctx); err != nil {
		n.logger.Errorf("failed shutting down raft transport: %v", err)
	}

	n.node.Stop()
	<-n.doneCh
	return nil
}

func (n *raftNode[T, T2]) setServers(servers []Server) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.servers = make(map[uint64]Server, len(servers))
	n.serverIDs = make(map[ServerID]uint64, len(servers))
	for _, server := range servers {
		n.servers[numericServerID(server.ID)] = server
		n.serverIDs[server.ID] = numericServerID(server.ID)
	}
}

func (n *raftNode[T, T2]) handleMessage(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var msg raftpb.Message
	if err := msg.Unmarshal(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := n.node.Step(r.Context(), msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (n *raftNode[T, T2]) tickLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			n.node.Tick()
		case <-n.stopCh:
			return
		}
	}
}

func (n *raftNode[T, T2]) readyLoop() {
	defer close(n.doneCh)

	for {
		select {
		case rd := <-n.node.Ready():
			n.processSoftState(rd.SoftState)

			if !etcdraft.IsEmptyHardState(rd.HardState) {
				if err := n.store.SetHardState(rd.HardState); err != nil {
					n.logger.Errorf("failed to persist hard state: %v", err)
				}
			}

			if !etcdraft.IsEmptySnap(rd.Snapshot) {
				if err := n.store.ApplySnapshot(rd.Snapshot); err != nil {
					n.logger.Errorf("failed to apply snapshot: %v", err)
				}
			}

			if len(rd.Entries) > 0 {
				if err := n.store.Append(rd.Entries); err != nil {
					n.logger.Errorf("failed to append entries: %v", err)
				}
				atomic.StoreUint64(&n.lastIndex, rd.Entries[len(rd.Entries)-1].Index)
			}

			for _, msg := range rd.Messages {
				n.send(msg)
			}

			for _, entry := range rd.CommittedEntries {
				n.applyEntry(entry)
			}

			n.node.Advance()
		case <-n.stopCh:
			return
		}
	}
}

func (n *raftNode[T, T2]) processSoftState(soft *etcdraft.SoftState) {
	if soft == nil {
		return
	}

	n.lead = soft.Lead
	isLeader := soft.RaftState == etcdraft.StateLeader
	if isLeader != n.isLeader {
		n.isLeader = isLeader
		select {
		case n.leaderCh <- isLeader:
		default:
		}
	}
}

func (n *raftNode[T, T2]) applyEntry(entry raftpb.Entry) {
	atomic.StoreUint64(&n.appliedIndex, entry.Index)

	switch entry.Type {
	case raftpb.EntryNormal:
		if len(entry.Data) == 0 {
			return
		}

		logData := RaftLog[T, T2]{
			Disco: make(map[ServerID][]T2),
			index: entry.Index,
		}
		if err := json.Unmarshal(entry.Data, &logData.Disco); err != nil {
			n.logger.Errorf("failed to unmarshal committed entry: %v", err)
			return
		}

		n.ch <- logData
	case raftpb.EntryConfChange:
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(entry.Data); err != nil {
			n.logger.Errorf("failed to unmarshal conf change: %v", err)
			return
		}
		n.node.ApplyConfChange(cc)
		n.applyConfChange(cc)
	case raftpb.EntryConfChangeV2:
		var cc raftpb.ConfChangeV2
		if err := cc.Unmarshal(entry.Data); err != nil {
			n.logger.Errorf("failed to unmarshal conf change v2: %v", err)
			return
		}
		n.node.ApplyConfChange(cc)
	}
}

func (n *raftNode[T, T2]) applyConfChange(cc raftpb.ConfChange) {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch cc.Type {
	case raftpb.ConfChangeAddNode, raftpb.ConfChangeAddLearnerNode:
		var server Server
		if len(cc.Context) > 0 {
			if err := json.Unmarshal(cc.Context, &server); err == nil {
				n.servers[cc.NodeID] = server
				n.serverIDs[server.ID] = cc.NodeID
			}
		}
	case raftpb.ConfChangeRemoveNode:
		if server, ok := n.servers[cc.NodeID]; ok {
			delete(n.serverIDs, server.ID)
		}
		delete(n.servers, cc.NodeID)
	}
}

func (n *raftNode[T, T2]) send(msg raftpb.Message) {
	n.mu.RLock()
	server, ok := n.servers[msg.To]
	n.mu.RUnlock()
	if !ok || server.Address == "" {
		n.node.ReportUnreachable(msg.To)
		return
	}

	body, err := msg.Marshal()
	if err != nil {
		n.logger.Errorf("failed to marshal raft message: %v", err)
		return
	}

	url := fmt.Sprintf("http://%s%s", server.Address, raftTransportPath)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		n.logger.Errorf("failed to create raft request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/protobuf")

	resp, err := n.client.Do(req)
	if err != nil {
		n.node.ReportUnreachable(msg.To)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		n.node.ReportUnreachable(msg.To)
	}
}

type RaftLog[T any, T2 Work[T]] struct {
	Disco map[ServerID][]T2
	index uint64
}

// Diff returns the source slice indexes that needs to be removed because they are
// not present or have been modified in the target slice. It also returns the indexes
// of the target slice that needs to be added because they are not present in the source
// slice. This function is nil safe.
func (l RaftLog[T, T2]) Diff(key ServerID, targetLog RaftLog[T, T2]) (removed []int, added []int) {
	if l.Disco == nil && targetLog.Disco == nil {
		return
	}

	if l.Disco == nil && targetLog.Disco != nil {
		added = append(added, serie(0, len(targetLog.Disco[key]), 1)...)
		return
	}

	if l.Disco != nil && targetLog.Disco == nil {
		removed = append(removed, serie(0, len(l.Disco[key]), 1)...)
		return
	}

	source, sourceKeyFound := l.Disco[key]
	target, targetKeyFound := targetLog.Disco[key]

	sourceFound := sourceKeyFound && len(source) > 0
	targetFound := targetKeyFound && len(targetLog.Disco[key]) > 0

	if !sourceFound && !targetFound {
		return
	}

	if !sourceFound && targetFound {
		added = append(added, serie(0, len(targetLog.Disco[key]), 1)...)
		return
	}

	if sourceFound && !targetFound {
		removed = append(removed, serie(0, len(l.Disco[key]), 1)...)
		return
	}

	for targetIndex, targetElem := range target {
		found := false
		sourceIndex := 0

		for i, sourceElem := range source {
			if sourceElem.ID() == targetElem.ID() {
				found = true
				sourceIndex = i
				break
			}
		}

		if !found {
			added = append(added, targetIndex)
		} else if !targetElem.Equals(source[sourceIndex].Object()) {
			removed = append(removed, sourceIndex)
			added = append(added, targetIndex)
		}
	}

	for sourceIndex, sourceElem := range source {
		found := false
		for _, targetElem := range target {
			if sourceElem.ID() == targetElem.ID() {
				found = true
				break
			}
		}

		if !found {
			removed = append(removed, sourceIndex)
		}
	}

	return
}

func numericServerID(id ServerID) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	out := h.Sum64()
	if out == 0 {
		return 1
	}
	return out
}

func mustJSON[T any](v T) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
