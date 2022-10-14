package disconats

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	"github.com/nats-io/nats.go"

	"sylr.dev/rafty"
	"sylr.dev/rafty/discovery"
)

type NatsJSDiscoverer struct {
	logger         rafty.Logger
	advertisedAddr string
	natsConn       *nats.Conn
	jsContext      nats.JetStreamContext
	jsBucketName   string
	jsKeyValue     nats.KeyValue
	interval       time.Duration
	ch             chan struct{}
	done           chan struct{}
	currentServers []raft.Server
	mux            sync.Mutex
}

var _ discovery.Discoverer = (*NatsJSDiscoverer)(nil)

type Option func(*NatsJSDiscoverer) error

func JSContext(ctx nats.JetStreamContext) Option {
	return func(disco *NatsJSDiscoverer) error {
		disco.jsContext = ctx
		return nil
	}
}

func JSBucket(bucket string) Option {
	return func(disco *NatsJSDiscoverer) error {
		disco.jsBucketName = bucket
		return nil
	}
}

func Logger(logger rafty.Logger) Option {
	return func(disco *NatsJSDiscoverer) error {
		disco.logger = logger
		return nil
	}
}

func NewNatsJSDiscoverer(advertisedAddr string, natsConn *nats.Conn, options ...Option) (*NatsJSDiscoverer, error) {
	var err error

	d := &NatsJSDiscoverer{
		advertisedAddr: advertisedAddr,
		natsConn:       natsConn,
		ch:             make(chan struct{}),
		done:           make(chan struct{}, 1),
		interval:       10 * time.Second,
	}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}

	if d.advertisedAddr == "" {
		return nil, errors.New("advertisedAddr is required")
	}

	if d.jsBucketName == "" {
		d.jsBucketName = "rafty"
	}

	if d.logger == nil {
		d.logger = &rafty.NullLogger{}
	}

	if d.jsContext == nil {
		d.jsContext, err = d.natsConn.JetStream()
		if err != nil {
			return nil, err
		}
	}

	d.jsKeyValue, err = d.jsContext.KeyValue(d.jsBucketName)
	if err != nil {
		if errors.Is(err, nats.ErrBucketNotFound) {
			replicas := 1
			servers := d.natsConn.DiscoveredServers()
			if len(servers) >= 3 {
				replicas = 3
			}

			d.jsKeyValue, err = d.jsContext.CreateKeyValue(&nats.KeyValueConfig{
				Bucket:      d.jsBucketName,
				Description: "Rafty Nats JetStream Discoverer",
				TTL:         time.Minute,
				Storage:     nats.MemoryStorage,
				Replicas:    replicas,
				History:     10,
			})

			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	return d, nil
}

func (d *NatsJSDiscoverer) Start(ctx context.Context) {
	go d.watcher(ctx)
	go d.ticker(ctx)
}

func (d *NatsJSDiscoverer) Done() chan struct{} {
	return d.done
}

func (d *NatsJSDiscoverer) ticker(ctx context.Context) {
	defer func() {
		d.done <- struct{}{}
	}()

	updater := func() {
		key := strings.ReplaceAll(d.advertisedAddr, ":", "__")
		key = strings.ReplaceAll(key, ".", "_")

		_, err := d.jsKeyValue.Put(key, []byte(time.Now().Format(time.RFC3339Nano)))
		if err != nil {
			d.logger.Errorf("nats-js-disco ticker put key: %s", err)
		}
	}

	// Initial update
	updater()

	for {
		select {
		case <-time.After(d.interval):
			updater()
		case <-ctx.Done():
			key := strings.ReplaceAll(d.advertisedAddr, ":", "__")
			key = strings.ReplaceAll(key, ".", "_")

			d.logger.Debugf("nats-js-disco ticker deleting key: %s", key)

			err := d.jsKeyValue.Delete(key)
			if err != nil {
				d.logger.Errorf("nats-js-disco ticker delete key: %s", err)
			}

			d.logger.Debugf("nats-js-disco ticker key deleted: %s", key)

			return
		}
	}
}

func (d *NatsJSDiscoverer) watcher(ctx context.Context) {
WATCH:
	watcher, err := d.jsKeyValue.WatchAll()
	if err != nil {
		d.logger.Errorf("nats-js-disco: %s", err)
		time.Sleep(time.Second * 2)
		goto WATCH
	}

	defer watcher.Stop()

	for {
		select {
		case update := <-watcher.Updates():
			if update != nil {
				newServers := d.getServers()

				if ServersDiffer(d.currentServers, newServers) {
					d.mux.Lock()
					d.currentServers = newServers
					d.mux.Unlock()
					d.ch <- struct{}{}
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (d *NatsJSDiscoverer) NewServers() chan struct{} {
	return d.ch
}

func (d *NatsJSDiscoverer) GetServers() []raft.Server {
	d.mux.Lock()
	defer d.mux.Unlock()

	if d.currentServers == nil {
		d.currentServers = d.getServers()
	}

	return d.currentServers
}

func (d *NatsJSDiscoverer) getServers() []raft.Server {
	keys, err := d.jsKeyValue.Keys()
	if err != nil {
		d.logger.Errorf("nats-js-disco get keys: %s", err)
		return nil
	}

	servers := make([]raft.Server, 0, len(keys))
	for _, key := range keys {
		rev, err := d.jsKeyValue.Get(key)
		if err != nil {
			d.logger.Errorf("nats-js-disco fetch key: %s", err)
			return nil
		}

		lastUpdate, err := time.Parse(time.RFC3339Nano, string(rev.Value()))
		if err != nil {
			d.logger.Errorf("nats-js-disco parsing key value: %s", err)
			return nil
		}

		advertisedAddr := strings.ReplaceAll(key, "__", ":")
		advertisedAddr = strings.ReplaceAll(advertisedAddr, "_", ".")

		if lastUpdate.Add(d.interval + (5 * time.Second)).After(time.Now()) {
			servers = append(servers, raft.Server{
				ID:       raft.ServerID(advertisedAddr),
				Address:  raft.ServerAddress(advertisedAddr),
				Suffrage: raft.Voter,
			})
		} else {
			d.logger.Debugf("nats-js-disco discarding stale server: %s", advertisedAddr)
		}
	}

	return servers
}

func ServersDiffer(source, target []raft.Server) bool {
	if len(source) != len(target) {
		return true
	}

	for _, s := range source {
		found := false
		for _, t := range target {
			if s.ID == t.ID && s.Address == t.Address {
				found = true
			}
		}

		if !found {
			return true
		}
	}

	return false
}
