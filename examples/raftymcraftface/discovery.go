package main

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/raft"

	"sylr.dev/rafty/discovery"
)

type LocalDiscoverer struct {
	advertisedAddr string
	startPort      int
	clusterSize    int
	ch             chan struct{}
	interval       time.Duration
}

var _ discovery.Discoverer = (*LocalDiscoverer)(nil)

func (ld *LocalDiscoverer) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			close(ld.ch)
			return
		case <-time.After(ld.interval):
			ld.ch <- struct{}{}
		}
	}
}

func (ld *LocalDiscoverer) NewServers() chan struct{} {
	return ld.ch
}

func (ld *LocalDiscoverer) GetServers() []raft.Server {
	servers := make([]raft.Server, ld.clusterSize)

	for i := 0; i < ld.clusterSize; i++ {
		address := raft.ServerAddress(fmt.Sprintf("%s:%d", ld.advertisedAddr, ld.startPort+i))

		suffrage := raft.Voter
		if i > 9 {
			suffrage = raft.Nonvoter
		} else {
			// Make the number of voters odd if cluster size greater than 3
			if ld.clusterSize > 3 && i == ld.clusterSize-1 && i%2 == 1 {
				suffrage = raft.Nonvoter
			}
		}

		servers[i] = raft.Server{
			ID:       raft.ServerID(address),
			Address:  address,
			Suffrage: suffrage,
		}
	}

	return servers
}
