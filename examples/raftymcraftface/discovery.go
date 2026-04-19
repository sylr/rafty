package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"sylr.dev/rafty"
)

const discoveryPortOffset = 1000

type LocalDiscoverer struct {
	id             rafty.ServerID
	bindAddr       string
	advertisedAddr string
	port           int
	startPort      int
	clusterSize    int
	ch             chan struct{}
	recoveryCh     chan struct{}
	interval       time.Duration
	server         *http.Server
	client         *http.Client
	done           chan struct{}

	mu    sync.RWMutex
	epoch uint64
	seed  rafty.ServerID
}

type discoveryEntry struct {
	ID      rafty.ServerID      `json:"id"`
	Address rafty.ServerAddress `json:"address"`
	Epoch   uint64              `json:"epoch"`
	Seed    rafty.ServerID      `json:"seed"`
}

var _ rafty.Discoverer = (*LocalDiscoverer)(nil)
var _ rafty.ClusterSizer = (*LocalDiscoverer)(nil)
var _ rafty.Recoverer = (*LocalDiscoverer)(nil)
var _ rafty.Finalizer = (*LocalDiscoverer)(nil)

func (ld *LocalDiscoverer) Start(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/id", func(w http.ResponseWriter, r *http.Request) {
		epoch, seed := ld.recoveryInfo()
		_ = json.NewEncoder(w).Encode(discoveryEntry{
			ID:      ld.id,
			Address: rafty.ServerAddress(fmt.Sprintf("%s:%d", ld.advertisedAddr, ld.port)),
			Epoch:   epoch,
			Seed:    seed,
		})
	})

	ld.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", ld.bindAddr, ld.port+discoveryPortOffset),
		Handler: mux,
	}

	go func() {
		ln, err := net.Listen("tcp", ld.server.Addr)
		if err == nil {
			_ = ld.server.Serve(ln)
		}
	}()

	ld.signal()

	ticker := time.NewTicker(ld.interval)
	defer ticker.Stop()

	<-time.After(200 * time.Millisecond)
	ld.signal()

	for {
		select {
		case <-ticker.C:
			if ld.refreshState() {
				ld.signalRecovery()
			}
			ld.signal()
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = ld.server.Shutdown(shutdownCtx)
			close(ld.done)
			close(ld.ch)
			return
		}
	}
}

func (ld *LocalDiscoverer) NewServers() chan struct{} {
	return ld.ch
}

func (ld *LocalDiscoverer) ExpectedServers() int {
	return ld.clusterSize
}

func (ld *LocalDiscoverer) RecoveryCh() chan struct{} {
	return ld.recoveryCh
}

func (ld *LocalDiscoverer) Done() chan struct{} {
	return ld.done
}

func (ld *LocalDiscoverer) GetServers() []rafty.Server {
	entries := ld.fetchEntries()
	maxEpoch := ld.maxEpoch(entries)
	if maxEpoch > 0 {
		filtered := entries[:0]
		for _, entry := range entries {
			if entry.Epoch == maxEpoch {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Address < entries[j].Address })

	servers := make([]rafty.Server, 0, len(entries))
	for i, liveEntry := range entries {
		suffrage := rafty.Voter
		if i > 8 {
			suffrage = rafty.Nonvoter
		} else if len(entries) > 3 && i == len(entries)-1 && i%2 == 1 {
			suffrage = rafty.Nonvoter
		}
		servers = append(servers, rafty.Server{
			ID:       liveEntry.ID,
			Address:  liveEntry.Address,
			Suffrage: suffrage,
		})
	}

	return servers
}

func (ld *LocalDiscoverer) GetRecoveryState() rafty.RecoveryState {
	ld.refreshState()

	ld.mu.RLock()
	state := rafty.RecoveryState{
		Epoch:       ld.epoch,
		Seed:        ld.seed,
		MinRequired: ld.minRecoveryMembers(),
	}
	ld.mu.RUnlock()
	state.Members = ld.GetServers()
	return state
}

func (ld *LocalDiscoverer) StartRecovery(self rafty.Server, live []rafty.Server) error {
	if len(live) == 0 {
		live = []rafty.Server{self}
	}
	if len(live) < ld.minRecoveryMembers() {
		return nil
	}

	sorted := append([]rafty.Server(nil), live...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Address < sorted[j].Address })
	if len(sorted) == 0 || sorted[0].ID != self.ID {
		return nil
	}

	entries := ld.fetchEntries()
	nextEpoch := ld.maxEpoch(entries) + 1

	ld.mu.Lock()
	changed := nextEpoch > ld.epoch || ld.seed != self.ID
	if changed {
		ld.epoch = nextEpoch
		ld.seed = self.ID
	}
	ld.mu.Unlock()

	if changed {
		ld.signalRecovery()
		ld.signal()
	}

	return nil
}

func (ld *LocalDiscoverer) signal() {
	select {
	case ld.ch <- struct{}{}:
	default:
	}
}

func (ld *LocalDiscoverer) signalRecovery() {
	select {
	case ld.recoveryCh <- struct{}{}:
	default:
	}
}

func (ld *LocalDiscoverer) recoveryInfo() (uint64, rafty.ServerID) {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	return ld.epoch, ld.seed
}

func (ld *LocalDiscoverer) refreshState() bool {
	entries := ld.fetchEntries()
	maxEpoch := ld.maxEpoch(entries)
	if maxEpoch == 0 {
		return false
	}

	var seed rafty.ServerID
	for _, entry := range entries {
		if entry.Epoch == maxEpoch && entry.Seed != "" {
			seed = entry.Seed
			break
		}
	}

	ld.mu.Lock()
	defer ld.mu.Unlock()
	if maxEpoch < ld.epoch || (maxEpoch == ld.epoch && seed == ld.seed) {
		return false
	}
	if seed == "" {
		seed = ld.seed
	}
	changed := maxEpoch != ld.epoch || seed != ld.seed
	ld.epoch = maxEpoch
	ld.seed = seed
	return changed
}

func (ld *LocalDiscoverer) fetchEntries() []discoveryEntry {
	entries := make([]discoveryEntry, 0, ld.clusterSize)
	for i := 0; i < ld.clusterSize; i++ {
		port := ld.startPort + i
		url := fmt.Sprintf("http://%s:%d/id", ld.advertisedAddr, port+discoveryPortOffset)
		resp, err := ld.client.Get(url)
		if err != nil {
			continue
		}
		var discovered discoveryEntry
		err = json.NewDecoder(resp.Body).Decode(&discovered)
		resp.Body.Close()
		if err != nil || discovered.ID == "" || discovered.Address == "" {
			continue
		}
		entries = append(entries, discovered)
	}
	return entries
}

func (ld *LocalDiscoverer) maxEpoch(entries []discoveryEntry) uint64 {
	ld.mu.RLock()
	maxEpoch := ld.epoch
	ld.mu.RUnlock()
	for _, entry := range entries {
		if entry.Epoch > maxEpoch {
			maxEpoch = entry.Epoch
		}
	}
	return maxEpoch
}

func (ld *LocalDiscoverer) minRecoveryMembers() int {
	if ld.clusterSize <= 0 {
		return 1
	}
	return (ld.clusterSize / 2) + 1
}
