package discodns

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	"github.com/miekg/dns"

	"sylr.dev/rafty"
	"sylr.dev/rafty/discovery"
)

var _ discovery.Discoverer = (*SRVDiscoverer)(nil)

// SRVDiscoverer is a Rafty discoverer which leverages the use of DNS and SRV records.
type SRVDiscoverer struct {
	logger         rafty.Logger
	client         *dns.Client
	nameserver     string
	record         string
	interval       time.Duration
	ch             chan struct{}
	maxVoters      int
	currentServers []raft.Server
	mux            sync.Mutex
}

type SRVOption func(*SRVDiscoverer) error

func Logger(logger rafty.Logger) SRVOption {
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
		d.client = &dns.Client{}
	}

	if d.logger == nil {
		d.logger = &rafty.NullLogger{}
	}

	if len(d.nameserver) == 0 {
		config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil {
			return nil, err
		}
		d.nameserver = config.Servers[0]
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
	msg := dns.Msg{}
	msg.SetQuestion(d.record, dns.TypeSRV)
	msg.SetEdns0(4096, true)

	r, _, err := d.client.Exchange(&msg, d.nameserver)
	if err != nil {
		d.logger.Errorf("dns-disco: fail to query nameserver: %w", err)
		return nil
	}

	servers := make([]raft.Server, 0, len(r.Answer))
	for i, a := range r.Answer {
		if srv, ok := a.(*dns.SRV); ok {
			suffrage := raft.Nonvoter
			if i < d.maxVoters {
				suffrage = raft.Voter
			}

			addrPort := fmt.Sprintf("%s:%d", srv.Target, srv.Port)
			servers = append(servers, raft.Server{
				ID:       raft.ServerID(addrPort),
				Address:  raft.ServerAddress(addrPort),
				Suffrage: suffrage,
			})
		}
	}

	return servers
}
