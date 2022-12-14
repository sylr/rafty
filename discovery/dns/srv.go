// Package discodns contains Rafty Discoverers which leverage the use of DNS.
package discodns

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	"github.com/miekg/dns"

	"sylr.dev/rafty/discovery"
	"sylr.dev/rafty/interfaces"
	"sylr.dev/rafty/logger"
)

// SRVDiscoverer is a Rafty discoverer which leverages the use of DNS and SRV records.
type SRVDiscoverer struct {
	logger         interfaces.Logger
	client         *dns.Client
	nameserver     string
	record         string
	interval       time.Duration
	ch             chan struct{}
	maxVoters      int
	currentServers []raft.Server
	mux            sync.Mutex
}

var _ interfaces.Discoverer = (*SRVDiscoverer)(nil)

type SRVOption func(*SRVDiscoverer) error

func Logger(logger interfaces.Logger) SRVOption {
	return func(disco *SRVDiscoverer) error {
		disco.logger = logger
		return nil
	}
}

func Client(client *dns.Client) SRVOption {
	return func(disco *SRVDiscoverer) error {
		disco.client = client
		return nil
	}
}

func Interval(interval time.Duration) SRVOption {
	return func(disco *SRVDiscoverer) error {
		disco.interval = interval
		return nil
	}
}

func Nameserver(nameserver string) SRVOption {
	return func(disco *SRVDiscoverer) error {
		disco.nameserver = nameserver
		return nil
	}
}

func NewSRVDiscoverer(srvRecord string, options ...SRVOption) (*SRVDiscoverer, error) {
	d := &SRVDiscoverer{
		maxVoters: 9,
		interval:  10 * time.Second,
		record:    srvRecord,
		ch:        make(chan struct{}, 1),
	}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}

	if d.client == nil {
		d.client = &dns.Client{
			Timeout:     3 * time.Second,
			DialTimeout: 3 * time.Second,
			ReadTimeout: 3 * time.Second,
		}
	}

	if d.logger == nil {
		d.logger = &logger.NullLogger{}
	}

	if len(d.nameserver) == 0 {
		config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil {
			return nil, err
		}
		d.nameserver = fmt.Sprintf("%s:%d", config.Servers[0], 53)
	}

	if len(d.record) == 0 {
		return nil, errors.New("SRVDiscoverer: srvRecord is empty")
	}

	return d, nil
}

func (d *SRVDiscoverer) Start(ctx context.Context) error {
	go d.run(ctx)

	// Immediatly signal that there are new servers
	d.ch <- struct{}{}

	return nil
}

func (d *SRVDiscoverer) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			close(d.ch)
			return
		case <-time.After(d.interval):
			newServers := d.getServers()
			if discovery.ServersDiffer(d.currentServers, newServers) {
				d.mux.Lock()
				d.currentServers = newServers
				d.mux.Unlock()
				d.ch <- struct{}{}
			}
		}
	}
}

func (d *SRVDiscoverer) NewServers() chan struct{} {
	return d.ch
}

func (d *SRVDiscoverer) GetServers() []raft.Server {
	d.mux.Lock()
	defer d.mux.Unlock()

	if d.currentServers == nil {
		d.currentServers = d.getServers()
	}

	return d.currentServers
}

func (d *SRVDiscoverer) getServers() []raft.Server {
	msgSrv := dns.Msg{}
	msgSrv.SetQuestion(d.record, dns.TypeSRV)
	msgSrv.SetEdns0(4096, true)

	respSrv, _, err := d.client.Exchange(&msgSrv, d.nameserver)
	if err != nil {
		d.logger.Errorf("disco-dns-srv: fail to query nameserver: %s", err.Error())
		return nil
	}

	servers := make([]raft.Server, 0, len(respSrv.Answer))
	for i, ansSrv := range respSrv.Answer {
		if srv, ok := ansSrv.(*dns.SRV); ok {
			msgA := dns.Msg{}
			msgA.SetQuestion(srv.Target, dns.TypeA)
			msgA.SetEdns0(4096, true)

			respA, _, err := d.client.Exchange(&msgA, d.nameserver)
			if err != nil {
				d.logger.Errorf("disco-dns-srv: fail to query nameserver: %s", err.Error())
				return nil
			}

			if len(respA.Answer) == 0 {
				d.logger.Warnf("disco-dns-srv: empty A answer for: %s", srv.Target)
				continue
			}

			ansA := respA.Answer[0]

			a, ok := ansA.(*dns.A)
			if !ok {
				d.logger.Warnf("disco-dns-srv: cannot cast A answer for: %s", srv.Target)
				continue
			}

			suffrage := raft.Voter
			if l := len(servers); l > 8 {
				suffrage = raft.Nonvoter
			} else {
				// Make the number of voters odd if cluster size greater than 3
				if l > 3 && i == l-1 && i%2 == 1 {
					suffrage = raft.Nonvoter
				}
			}

			addrPort := fmt.Sprintf("%s:%d", a.A.String(), srv.Port)
			servers = append(servers, raft.Server{
				ID:       raft.ServerID(addrPort),
				Address:  raft.ServerAddress(addrPort),
				Suffrage: suffrage,
			})
		}
	}

	return servers
}
