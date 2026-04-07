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

// Finalizer interface is used to finalize the discovery process.
type Finalizer interface {
	Done() chan struct{}
}

// Distributor is the mechanism that allows to distibute work across the members
// of the Raft cluster that constitutes Rafty.
type Distributor[T any, T2 Work[T]] interface {
	Distribute(servers []Server, works []T2) map[ServerID][]T2
}
