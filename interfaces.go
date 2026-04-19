package rafty

type Logger interface {
	Tracef(format string, fields ...interface{})
	Debugf(format string, fields ...interface{})
	Infof(format string, fields ...interface{})
	Warnf(format string, fields ...interface{})
	Errorf(format string, fields ...interface{})
}

type Work[T any] interface {
	ID() string
	Object() T
	Equals(T) bool
}

type Works[T any, T2 Work[T]] interface {
	GetWorks() []T2
}

// Formeman is the mechanism Rafty uses to retrieve work.
type Foreman[T any, T2 Work[T]] interface {
	Start()
	Stop()
	NewWorks() chan struct{}
	GetWorks() []T2
}

// Discoverer is the mechanism Rafty uses to discover other Rafty processes.
type Discoverer interface {
	NewServers() chan struct{}
	GetServers() []Server
}

// ClusterSizer is the optional mechanism a discoverer can use to expose the
// expected full cluster size for readiness gating.
type ClusterSizer interface {
	ExpectedServers() int
}

type RecoveryState struct {
	Epoch       uint64
	Seed        ServerID
	Members     []Server
	MinRequired int
}

// Recoverer is the optional mechanism a discoverer can use to coordinate
// no-quorum recovery outside of Raft.
type Recoverer interface {
	RecoveryCh() chan struct{}
	GetRecoveryState() RecoveryState
	StartRecovery(self Server, live []Server) error
}

// Finalizer interface is used to finalize the discovery process.
type Finalizer interface {
	Done() chan struct{}
}

// Distributor is the mechanism that allows to distibute work across the members
// of the Raft cluster that constitutes Rafty.
type Distributor[T any, T2 Work[T]] interface {
	Distribute(servers []Server, works []T2) map[ServerID][]T2
}
