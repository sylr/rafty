package rafty

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/hashicorp/raft"
	"sylr.dev/rafty/interfaces"
)

var _ raft.FSM = (*fsm[string, interfaces.Work[string]])(nil)
var _ raft.FSMSnapshot = (*snapshot[string, interfaces.Work[string]])(nil)

type fsm[T any, T2 interfaces.Work[T]] struct {
	logger interfaces.Logger
	mux    sync.Mutex
	ch     chan RaftLog[T, T2]
	w      RaftLog[T, T2]
}

func (f *fsm[T, T2]) Apply(log *raft.Log) interface{} {
	f.mux.Lock()
	defer f.mux.Unlock()

	f.w = RaftLog[T, T2]{
		Disco: make(map[raft.ServerID][]T2),
		index: log.Index,
	}

	err := json.Unmarshal(log.Data, &f.w.Disco)
	if err != nil {
		f.logger.Errorf("Failed to unmarshal log data while applying snapshot: %s", err)
		return nil
	}

	f.ch <- f.w

	return nil
}

func (f *fsm[T, T2]) Snapshot() (raft.FSMSnapshot, error) {
	f.logger.Debugf("Taking a snapshot")
	return &snapshot[T, T2]{f.w}, nil
}

func (f *fsm[T, T2]) Restore(io.ReadCloser) error {
	return nil
}

type snapshot[T any, T2 interfaces.Work[T]] struct {
	w RaftLog[T, T2]
}

func (s *snapshot[T, T2]) Persist(sink raft.SnapshotSink) error {
	b, err := json.Marshal(s.w)
	if err != nil {
		sink.Cancel()
		return err
	}

	_, err = sink.Write(b)
	if err != nil {
		return fmt.Errorf("sink.Write(): %v", err)
	}

	return sink.Close()
}

func (s *snapshot[T, T2]) Release() {

}
