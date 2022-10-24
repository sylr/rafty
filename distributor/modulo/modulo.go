// Package distribmodulo implements a naïve Rafty Distributor which evenly
// distributes work across the servers.
package distribmodulo

import (
	"math"

	"github.com/hashicorp/raft"
	"golang.org/x/exp/slices"

	"sylr.dev/rafty/interfaces"
)

type distributor[T any, T2 interfaces.Work[T]] struct {
}

var _ interfaces.Distributor[string, interfaces.Work[string]] = (*distributor[string, interfaces.Work[string]])(nil)

type Option[T any, T2 interfaces.Work[T]] func(*distributor[T, T2]) error

func New[T any, T2 interfaces.Work[T]](options ...Option[T, T2]) (*distributor[T, T2], error) {
	d := &distributor[T, T2]{}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}

	return d, nil
}

func (d *distributor[T, T2]) Distribute(servers []raft.Server, works []T2) map[raft.ServerID][]T2 {
	output := make(map[raft.ServerID][]T2)

	sortFunc := func(a, b raft.Server) bool {
		return a.ID < b.ID
	}

	slices.SortFunc(servers, sortFunc)

	sl, wl := len(servers), len(works)
	oc := int(math.Ceil(float64(sl) / float64(wl)))

	for i, work := range works {
		if _, ok := output[servers[i%sl].ID]; !ok {
			output[servers[i%sl].ID] = make([]T2, 0, oc)
		}

		output[servers[i%sl].ID] = append(output[servers[i%sl].ID], work)
	}

	return output
}
