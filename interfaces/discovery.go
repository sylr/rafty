package interfaces

import "github.com/hashicorp/raft"

// Discoverer is the mechanism Rafty uses to discover other Rafty processes.
type Discoverer interface {
	// NewServers returns a channel that is used to notify Rafty that there is a
	// new list of servers available. Although not required, it is better that
	// notifications are sent only when the list of servers has changed.
	NewServers() chan struct{}

	// GetServers returns the current list of servers.
	GetServers() []raft.Server
}

// Finalizer interface is used to finalize the discovery process. In the event of
// a Rafty process being stopped, if the Discoverer also implements Finalizer, Rafty
// will call Done() to allow the Discoverer to perform any cleanup required before
// the process exits.
type Finalizer interface {
	Done() chan struct{}
}
