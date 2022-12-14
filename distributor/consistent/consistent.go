// Package distribconsistent implements a Rafty Distributor which uses the
// github.com/buraksezer/consistent library to distribute work amongst the Rafty
// members.
package distribconsistent

import (
	"encoding/json"

	"github.com/buraksezer/consistent"
	"github.com/hashicorp/raft"

	"sylr.dev/rafty/interfaces"
)

type distributor[T any, T2 interfaces.Work[T]] struct {
	config consistent.Config
}

var _ interfaces.Distributor[string, interfaces.Work[string]] = (*distributor[string, interfaces.Work[string]])(nil)

type Option[T any, T2 interfaces.Work[T]] func(*distributor[T, T2]) error

func PartitionCountOption[T any, T2 interfaces.Work[T]](count int) Option[T, T2] {
	return func(d *distributor[T, T2]) error {
		d.config.PartitionCount = count
		return nil
	}
}

func ReplicationFactor[T any, T2 interfaces.Work[T]](factor int) Option[T, T2] {
	return func(d *distributor[T, T2]) error {
		d.config.ReplicationFactor = factor
		return nil
	}
}

func Load[T any, T2 interfaces.Work[T]](load float64) Option[T, T2] {
	return func(d *distributor[T, T2]) error {
		d.config.Load = load
		return nil
	}
}

func Hasher[T any, T2 interfaces.Work[T]](hasher consistent.Hasher) Option[T, T2] {
	return func(d *distributor[T, T2]) error {
		d.config.Hasher = hasher
		return nil
	}
}

func New[T any, T2 interfaces.Work[T]](options ...Option[T, T2]) (*distributor[T, T2], error) {
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

func (d *distributor[T, T2]) Distribute(servers []raft.Server, works []T2) map[raft.ServerID][]T2 {
	members := make([]consistent.Member, 0, len(servers))

	for _, server := range servers {
		members = append(members, stringer(server.ID))
	}

	c := consistent.New(members, d.config)

	output := make(map[raft.ServerID][]T2, 0)

	for _, w := range works {
		b, _ := json.Marshal(w)
		i := c.FindPartitionID(b)
		owner := c.GetPartitionOwner(i).String()

		if _, ok := output[raft.ServerID(owner)]; !ok {
			output[raft.ServerID(owner)] = make([]T2, 0)
		}

		output[raft.ServerID(owner)] = append(output[raft.ServerID(owner)], w)
	}

	return output
}
