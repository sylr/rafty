package rafty

import (
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

type RaftConfig struct {
	ListeningAddress  string
	ListeningPort     int
	AdvertisedAddress string
	Logger            Logger
	HCLogger          hclog.Logger
	ServerID          raft.ServerID
}

func NewRaft[T any, T2 Work[T]](c RaftConfig, ch chan RaftLog[T, T2]) (*raft.Raft, error) {
	listen := fmt.Sprintf("%s:%d", c.ListeningAddress, c.ListeningPort)
	config := raft.DefaultConfig()
	config.LocalID = c.ServerID
	//config.NoSnapshotRestoreOnStart = true

	if c.HCLogger != nil {
		config.Logger = c.HCLogger.Named("raft")
	} else {
		config.Logger = hclog.New(&hclog.LoggerOptions{
			Name:  "raft",
			Level: hclog.Trace,
		})
	}

	logStore := raft.NewInmemStore()
	stableStore := raft.NewInmemStore()
	snapshotStore := raft.NewInmemSnapshotStore()

	listenIP, err := netip.ParseAddr(c.ListeningAddress)
	if err != nil {
		return nil, err
	}

	var advertisedAddr net.Addr

	if len(c.AdvertisedAddress) == 0 {
		listenAddr := netip.AddrPortFrom(listenIP, uint16(c.ListeningPort))
		advertisedAddr = net.TCPAddrFromAddrPort(listenAddr)
	} else {
		advertisedIP, err := netip.ParseAddr(c.AdvertisedAddress)
		if err != nil {
			return nil, err
		}

		advertisedAddrPort := netip.AddrPortFrom(advertisedIP, uint16(c.ListeningPort))
		advertisedAddr = net.TCPAddrFromAddrPort(advertisedAddrPort)
		if err != nil {
			return nil, err
		}
	}

	c.Logger.Infof("Listen address: %v", listen)
	c.Logger.Infof("Advertised address: %v", advertisedAddr)

	//transport, err := raft.NewTCPTransport(listen, advertisedAddr, 3, 10*time.Second, os.Stdout)
	transport, err := raft.NewTCPTransportWithLogger(listen, advertisedAddr, 3, 10*time.Second, config.Logger.Named("raft-net"))
	if err != nil {
		return nil, err
	}

	f := &fsm[T, T2]{
		logger: c.Logger,
		ch:     ch,
		w: RaftLog[T, T2]{
			Disco: make(map[raft.ServerID][]T2),
		},
	}

	return raft.NewRaft(config, f, logStore, stableStore, snapshotStore, transport)
}

type RaftLog[T any, T2 Work[T]] struct {
	Disco map[raft.ServerID][]T2
	index uint64
}

// Diff returns the source slice indexes that needs to be removed because they are
// not present or have been modified in the target slice. It also returns the indexes
// of the target slice that needs to be added because they are not present in the source
// slice. This function is nil safe.
func (l RaftLog[T, T2]) Diff(key raft.ServerID, targetLog RaftLog[T, T2]) (removed []int, added []int) {
	// Diffing nil objects
	if l.Disco == nil && targetLog.Disco == nil {
		return
	}

	// Source is nil, return all indexes of the target as added
	if l.Disco == nil && targetLog.Disco != nil {
		added = append(added, serie(0, len(targetLog.Disco[key]), 1)...)
		return
	}

	// Target is nil, return all indexes of the source as removed
	if l.Disco != nil && targetLog.Disco == nil {
		removed = append(removed, serie(0, len(l.Disco[key]), 1)...)
		return
	}

	source, sourceKeyFound := l.Disco[key]
	target, targetKeyFound := targetLog.Disco[key]

	sourceFound := sourceKeyFound && len(source) > 0
	targetFound := targetKeyFound && len(targetLog.Disco[key]) > 0

	// Source and target are empty
	if !sourceFound && !targetFound {
		return
	}

	// Source is empty, return all indexes of the target as added
	if !sourceFound && targetFound {
		added = append(added, serie(0, len(targetLog.Disco[key]), 1)...)
		return
	}

	// Source is empty, return all indexes of the target as added
	if sourceFound && !targetFound {
		removed = append(removed, serie(0, len(l.Disco[key]), 1)...)
		return
	}

	// Looking for added  or updated work
	for targetIndex, targetElem := range target {
		found := false
		sourceIndex := 0

		for i, sourceElem := range source {
			if sourceElem.ID() == targetElem.ID() {
				found = true
				sourceIndex = i
				break
			}
		}

		if !found {
			added = append(added, targetIndex)
		} else {
			if !targetElem.Equals(source[sourceIndex].Object()) {
				removed = append(removed, sourceIndex)
				added = append(added, targetIndex)
			}
		}
	}

	// Looking for removed work
	for sourceIndex, sourceElem := range source {
		found := false

		for _, targetElem := range target {
			if sourceElem.ID() == targetElem.ID() {
				found = true
				break
			}
		}

		if !found {
			removed = append(removed, sourceIndex)
		}
	}

	return
}
