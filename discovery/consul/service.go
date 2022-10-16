package discoconsul

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/api/watch"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"

	"sylr.dev/rafty"
	"sylr.dev/rafty/discovery"
)

// ServiceDiscoverer is a Rafty discoverer which leverages the use of Consul services.
// This discoverer will register itself upon calling Start() and deregister itself
// when terminated.
type ServiceDiscoverer struct {
	logger               rafty.Logger
	hclogger             hclog.Logger
	advertisedAddr       string
	advertisedPort       int
	consulNode           string
	consulClient         *api.Client
	consulServiceName    string
	consulServiceTags    []string
	consulServiceWatcher *watch.Plan
	consulWatcherCh      chan struct{}
	interval             time.Duration
	ch                   chan struct{}
	done                 chan struct{}
	currentServers       []raft.Server
	mux                  sync.Mutex
}

var _ discovery.Discoverer = (*ServiceDiscoverer)(nil)

type ServiceOption func(*ServiceDiscoverer) error

func Logger(logger rafty.Logger) ServiceOption {
	return func(disco *ServiceDiscoverer) error {
		disco.logger = logger
		return nil
	}
}

func HCLogger(logger hclog.Logger) ServiceOption {
	return func(disco *ServiceDiscoverer) error {
		disco.hclogger = logger
		return nil
	}
}

func Name(name string) ServiceOption {
	return func(disco *ServiceDiscoverer) error {
		disco.consulServiceName = name
		return nil
	}
}

func Tags(tags []string) ServiceOption {
	return func(disco *ServiceDiscoverer) error {
		disco.consulServiceTags = tags
		return nil
	}
}

func NewServiceDiscoverer(advertisedAddr string, advertisedPort int, consulClient *api.Client, options ...ServiceOption) (*ServiceDiscoverer, error) {
	d := &ServiceDiscoverer{
		advertisedAddr:  advertisedAddr,
		advertisedPort:  advertisedPort,
		consulClient:    consulClient,
		consulWatcherCh: make(chan struct{}),
		ch:              make(chan struct{}),
		done:            make(chan struct{}, 1),
		interval:        10 * time.Second,
	}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}

	if d.advertisedAddr == "" {
		return nil, errors.New("disco-consul-service: advertisedAddr is required")
	}

	if d.logger == nil {
		d.logger = &rafty.NullLogger{}
	}

	return d, nil
}

func (d *ServiceDiscoverer) Start(ctx context.Context) error {
	var err error

	if d.consulNode, err = d.consulClient.Agent().NodeName(); err != nil {
		return err
	}

	reg := &api.AgentServiceRegistration{
		ID:      fmt.Sprintf("%s:%d", d.advertisedAddr, d.advertisedPort),
		Name:    d.consulServiceName,
		Address: d.advertisedAddr,
		Port:    d.advertisedPort,
		Tags:    d.consulServiceTags,
		Checks: api.AgentServiceChecks{
			&api.AgentServiceCheck{
				TCP:      fmt.Sprintf("%s:%d", d.advertisedAddr, d.advertisedPort),
				Interval: (5 * time.Second).String(),
				Timeout:  (1 * time.Second).String(),
				Notes:    "Raft port",
			},
		},
	}
	if err := d.consulClient.Agent().ServiceRegister(reg); err != nil {
		return fmt.Errorf("disco-consul-service: failed to register service: %w", err)
	}

	params := map[string]interface{}{"type": "service", "service": d.consulServiceName}
	if d.consulServiceWatcher, err = watch.Parse(params); err != nil {
		return fmt.Errorf("disco-consul-service: failed to create services watcher plan: %w", err)
	}

	d.consulServiceWatcher.HybridHandler = d.makeServiceWatcherHandler(ctx)

	go func() {
		err := d.consulServiceWatcher.RunWithClientAndHclog(d.consulClient, d.hclogger)
		if err != nil {
			d.logger.Errorf("disco-consul-service: watcher failed: %w", err)
		}
	}()
	go d.watcher(ctx)

	return nil
}

func (d *ServiceDiscoverer) makeServiceWatcherHandler(ctx context.Context) func(watch.BlockingParamVal, interface{}) {
	return func(_ watch.BlockingParamVal, _ interface{}) {
		select {
		case <-ctx.Done():
		case d.consulWatcherCh <- struct{}{}:
		default:
			// Event chan is full, discard event.
		}
	}
}

func (d *ServiceDiscoverer) Done() chan struct{} {
	return d.done
}

func (d *ServiceDiscoverer) watcher(ctx context.Context) {
	defer func() {
		d.done <- struct{}{}
	}()

	for {
		select {
		case <-d.consulWatcherCh:
			newServers := d.getServers()
			if discovery.ServersDiffer(d.currentServers, newServers) {
				d.mux.Lock()
				d.currentServers = newServers
				d.mux.Unlock()
				d.ch <- struct{}{}
			}
		case <-ctx.Done():
			serviceID := fmt.Sprintf("%s:%d", d.advertisedAddr, d.advertisedPort)
			if err := d.consulClient.Agent().ServiceDeregister(serviceID); err != nil {
				d.logger.Errorf("disco-consul-service: failed to deregister service `%s`: %w", serviceID, err)
			}
			return
		}
	}
}

func (d *ServiceDiscoverer) NewServers() chan struct{} {
	return d.ch
}

func (d *ServiceDiscoverer) GetServers() []raft.Server {
	d.mux.Lock()
	defer d.mux.Unlock()

	if d.currentServers == nil {
		d.currentServers = d.getServers()
	}

	return d.currentServers
}

func (d *ServiceDiscoverer) getServers() []raft.Server {
	opts := &api.QueryOptions{AllowStale: false, RequireConsistent: true, UseCache: true}
	members, _, err := d.consulClient.Health().Service(d.consulServiceName, "", true, opts)
	if err != nil {
		d.logger.Errorf("disco-consul-service: failed to get services: %w", err)
		return nil
	}

	raftServers := make([]raft.Server, 0, len(members))
	for _, member := range members {
		addrPort := fmt.Sprintf("%s:%d", member.Service.Address, member.Service.Port)
		raftServers = append(raftServers, raft.Server{
			ID:       raft.ServerID(addrPort),
			Address:  raft.ServerAddress(addrPort),
			Suffrage: raft.Voter,
		})
	}

	return raftServers
}
