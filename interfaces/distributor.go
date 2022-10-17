package interfaces

import (
	"github.com/hashicorp/raft"
)

// Distributor is the mechanism that allows to distibute work across the members
// of the Raft cluster that constitutes Rafty.
type Distributor[T any, T2 Work[T]] interface {
	Distribute(servers []raft.Server, works []T2) map[raft.ServerID][]T2
}
