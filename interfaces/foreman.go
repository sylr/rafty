package interfaces

// Formeman is the mechanism Rafty uses to retrieve work.
type Foreman[T any, T2 Work[T]] interface {
	// Start is called on the process that has been elected as the leader by the
	// Raft Cluster.
	Start()

	// Stop is called in the event we lose leadership.
	Stop()

	// NewWorks returns a channel that is used to notify Rafty that there is new
	// works available.
	NewWorks() chan struct{}

	// GetWorks returns the current list of work.
	GetWorks() []T2
}
