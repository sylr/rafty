package discodns

import (
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/raft"
	"github.com/miekg/dns"
	"golang.org/x/net/context"

	"sylr.dev/rafty/discovery"
)

var _ discovery.Discoverer = (*DNSSRVDiscoverer)(nil)

type DNSSRVDiscoverer struct {
	client     *dns.Client
	nameserver string
	record     string
	interval   time.Duration
	ch         chan []raft.Server
	maxVoters  int
}

func NewDNSSRVDiscoverer(client *dns.Client, nameserver string, interval time.Duration, SRVRecord string, ch chan []raft.Server) (*DNSSRVDiscoverer, error) {
	d := &DNSSRVDiscoverer{
		maxVoters: 10,
	}

	if client == nil {
		d.client = &dns.Client{}
	} else {
		d.client = client
	}

	if interval == 0 {
		d.interval = time.Second * 5
	} else {
		d.interval = interval
	}

	if len(nameserver) == 0 {
		config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil {
			return nil, err
		}
		d.nameserver = config.Servers[0]
	} else {
		d.nameserver = nameserver
	}

	if len(SRVRecord) == 0 {
		return nil, errors.New("DNSSRVDiscoverer: SRVRecord is empty")
	}

	d.record = SRVRecord
	d.ch = ch

	return d, nil
}

func (d *DNSSRVDiscoverer) Start(ctx context.Context) (chan []raft.Server, error) {
	go d.run(ctx)

	return d.ch, nil
}

func (d *DNSSRVDiscoverer) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			close(d.ch)
			return
		case <-time.After(d.interval):
			d.query()
		}
	}
}

func (d *DNSSRVDiscoverer) query() {
	msg := dns.Msg{}
	msg.SetQuestion(d.record, dns.TypeSRV)
	msg.SetEdns0(4096, true)
	r, _, err := d.client.Exchange(&msg, d.nameserver)
	if err != nil {
		return
	}

	servers := make([]raft.Server, 0, len(r.Answer))

	for i, a := range r.Answer {
		if srv, ok := a.(*dns.SRV); ok {
			suffrage := raft.Nonvoter
			if i < d.maxVoters {
				suffrage = raft.Voter
			}

			servers = append(servers, raft.Server{
				ID:       raft.ServerID(srv.Target),
				Suffrage: suffrage,
				Address:  raft.ServerAddress(fmt.Sprintf("%s:%d", srv.Target, srv.Port)),
			})
		}
	}

	d.ch <- servers
}
