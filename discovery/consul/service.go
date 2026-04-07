// Package discoconsul contains Rafty Discoverers which leverage the use of
// HashiCorp's Consul.
package discoconsul

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/api/watch"

	"sylr.dev/rafty"
	"sylr.dev/rafty/discovery"
)

// ServiceDiscoverer is a Rafty discoverer which leverages the use of Consul services.
// This discoverer will register itself upon calling Start() and deregister itself
// when context given to Start() is canceled.
type ServiceDiscoverer struct {
	logger               rafty.Logger
	consulLogger         *log.Logger
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
	currentServers       []rafty.Server
	mux                  sync.Mutex
}

var _ rafty.Discoverer = (*ServiceDiscoverer)(nil)

type ServiceOption func(*ServiceDiscoverer) error

func Logger(logger rafty.Logger) ServiceOption {
	return func(disco *ServiceDiscoverer) error {
		disco.logger = logger
		return nil
	}
}

func ConsulLogger(logger *log.Logger) ServiceOption {
	return func(disco *ServiceDiscoverer) error {
		disco.consulLogger = logger
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
	if d.consulLogger == nil {
		d.consulLogger = log.New(io.Discard, "", 0)
	}

	return d, nil
}

func (d *ServiceDiscoverer) Start(ctx context.Context) error {
	var err error

	if d.consulNode, err = d.consulClient.Agent().NodeName(); err != nil {
		return err
	}

	serviceID := fmt.Sprintf("%s:%d", d.advertisedAddr, d.advertisedPort)
	reg := &api.AgentServiceRegistration{
		ID:      serviceID,
		Name:    d.consulServiceName,
		Address: d.advertisedAddr,
		Port:    d.advertisedPort,
		Tags:    d.consulServiceTags,
		Checks: api.AgentServiceChecks{
			&api.AgentServiceCheck{
				TCP:      serviceID,
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
		err := d.consulServiceWatcher.RunWithClientAndLogger(d.consulClient, d.consulLogger)
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

func (d *ServiceDiscoverer) GetServers() []rafty.Server {
	d.mux.Lock()
	defer d.mux.Unlock()

	if d.currentServers == nil {
		d.currentServers = d.getServers()
	}

	return d.currentServers
}

func (d *ServiceDiscoverer) getServers() []rafty.Server {
	opts := &api.QueryOptions{AllowStale: false, RequireConsistent: true, UseCache: true}
	members, _, err := d.consulClient.Health().Service(d.consulServiceName, "", true, opts)
	if err != nil {
		d.logger.Errorf("disco-consul-service: failed to get services: %w", err)
		return nil
	}

	raftServers := make([]rafty.Server, 0, len(members))
	for _, member := range members {
		addrPort := fmt.Sprintf("%s:%d", member.Service.Address, member.Service.Port)
		raftServers = append(raftServers, rafty.Server{
			ID:       rafty.ServerID(addrPort),
			Address:  rafty.ServerAddress(addrPort),
			Suffrage: rafty.Voter,
		})
	}

	return raftServers
}
