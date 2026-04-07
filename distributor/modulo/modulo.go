// Package distribmodulo implements a naïve Rafty Distributor which evenly
// distributes work across the servers.
package distribmodulo

import (
	"math"
	"sort"

	"sylr.dev/rafty"
)

type distributor[T any, T2 rafty.Work[T]] struct {
}

var _ rafty.Distributor[string, rafty.Work[string]] = (*distributor[string, rafty.Work[string]])(nil)

type Option[T any, T2 rafty.Work[T]] func(*distributor[T, T2]) error

func New[T any, T2 rafty.Work[T]](options ...Option[T, T2]) (*distributor[T, T2], error) {
	d := &distributor[T, T2]{}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}

	return d, nil
}

func (d *distributor[T, T2]) Distribute(servers []rafty.Server, works []T2) map[rafty.ServerID][]T2 {
	output := make(map[rafty.ServerID][]T2)

	sortFunc := func(a, b rafty.Server) bool {
		return a.ID < b.ID
	}

	sort.Slice(servers, func(i, j int) bool {
		return sortFunc(servers[i], servers[j])
	})

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
