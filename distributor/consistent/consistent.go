// Package distribconsistent implements a Rafty Distributor which uses the
// github.com/buraksezer/consistent library to distribute work amongst the Rafty
// members.
package distribconsistent

import (
	"encoding/json"

	"github.com/buraksezer/consistent"

	"sylr.dev/rafty"
)

type distributor[T any, T2 rafty.Work[T]] struct {
	config consistent.Config
}

var _ rafty.Distributor[string, rafty.Work[string]] = (*distributor[string, rafty.Work[string]])(nil)

type Option[T any, T2 rafty.Work[T]] func(*distributor[T, T2]) error

func PartitionCountOption[T any, T2 rafty.Work[T]](count int) Option[T, T2] {
	return func(d *distributor[T, T2]) error {
		d.config.PartitionCount = count
		return nil
	}
}

func ReplicationFactor[T any, T2 rafty.Work[T]](factor int) Option[T, T2] {
	return func(d *distributor[T, T2]) error {
		d.config.ReplicationFactor = factor
		return nil
	}
}

func Load[T any, T2 rafty.Work[T]](load float64) Option[T, T2] {
	return func(d *distributor[T, T2]) error {
		d.config.Load = load
		return nil
	}
}

func Hasher[T any, T2 rafty.Work[T]](hasher consistent.Hasher) Option[T, T2] {
	return func(d *distributor[T, T2]) error {
		d.config.Hasher = hasher
		return nil
	}
}

func New[T any, T2 rafty.Work[T]](options ...Option[T, T2]) (*distributor[T, T2], error) {
	d := &distributor[T, T2]{
		config: consistent.Config{
			Hasher:            hasher{},
			PartitionCount:    271,
			ReplicationFactor: 20,
			Load:              1.25,
		},
	}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}

	return d, nil
}

func (d *distributor[T, T2]) Distribute(servers []rafty.Server, works []T2) map[rafty.ServerID][]T2 {
	members := make([]consistent.Member, 0, len(servers))

	for _, server := range servers {
		members = append(members, stringer(server.ID))
	}

	c := consistent.New(members, d.config)

	output := make(map[rafty.ServerID][]T2, 0)

	for _, w := range works {
		b, _ := json.Marshal(w)
		i := c.FindPartitionID(b)
		owner := c.GetPartitionOwner(i).String()

		if _, ok := output[rafty.ServerID(owner)]; !ok {
			output[rafty.ServerID(owner)] = make([]T2, 0)
		}

		output[rafty.ServerID(owner)] = append(output[rafty.ServerID(owner)], w)
	}

	return output
}
