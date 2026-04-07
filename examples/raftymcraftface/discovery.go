package main

import (
	"context"
	"fmt"
	"time"

	"sylr.dev/rafty"
)

type LocalDiscoverer struct {
	advertisedAddr string
	startPort      int
	clusterSize    int
	ch             chan struct{}
	interval       time.Duration
}

var _ rafty.Discoverer = (*LocalDiscoverer)(nil)

func (ld *LocalDiscoverer) Start(ctx context.Context) {
	ld.ch <- struct{}{}

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

func (ld *LocalDiscoverer) GetServers() []rafty.Server {
	servers := make([]rafty.Server, ld.clusterSize)

	for i := 0; i < ld.clusterSize; i++ {
		address := rafty.ServerAddress(fmt.Sprintf("%s:%d", ld.advertisedAddr, ld.startPort+i))

		suffrage := rafty.Voter
		if i > 8 {
			suffrage = rafty.Nonvoter
		} else {
			// Make the number of voters odd if cluster size greater than 3
			if ld.clusterSize > 3 && i == ld.clusterSize-1 && i%2 == 1 {
				suffrage = rafty.Nonvoter
			}
		}

		servers[i] = rafty.Server{
			ID:       rafty.ServerID(address),
			Address:  address,
			Suffrage: suffrage,
		}
	}

	return servers
}
