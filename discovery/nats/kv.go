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

// KVDiscoverer is a Rafty discoverer which leverages the use of NATS JetStream
// key values. This discoverer will register itself upon calling Start() and
// deregister itself when terminated.
type KVDiscoverer struct {
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

var _ discovery.Discoverer = (*KVDiscoverer)(nil)

type KVOption func(*KVDiscoverer) error

func JSContext(ctx nats.JetStreamContext) KVOption {
	return func(disco *KVDiscoverer) error {
		disco.jsContext = ctx
		return nil
	}
}

func JSBucket(bucket string) KVOption {
	return func(disco *KVDiscoverer) error {
		disco.jsBucketName = bucket
		return nil
	}
}

func Logger(logger rafty.Logger) KVOption {
	return func(disco *KVDiscoverer) error {
		disco.logger = logger
		return nil
	}
}

func NewKVDiscoverer(advertisedAddr string, natsConn *nats.Conn, options ...KVOption) (*KVDiscoverer, error) {
	var err error

	d := &KVDiscoverer{
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

func (d *KVDiscoverer) Start(ctx context.Context) {
	go d.watcher(ctx)
	go d.ticker(ctx)
}

func (d *KVDiscoverer) Done() chan struct{} {
	return d.done
}

func (d *KVDiscoverer) ticker(ctx context.Context) {
	defer func() {
		d.done <- struct{}{}
	}()

	updater := func() {
		key := strings.ReplaceAll(d.advertisedAddr, ":", "__")
		key = strings.ReplaceAll(key, ".", "_")

		_, err := d.jsKeyValue.Put(key, []byte(time.Now().Format(time.RFC3339Nano)))
		if err != nil {
			d.logger.Errorf("disco-nats-kv: ticker put key: %s", err)
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

			d.logger.Debugf("disco-nats-kv: deleting key: %s", key)

			err := d.jsKeyValue.Delete(key)
			if err != nil {
				d.logger.Errorf("disco-nats-kv: failed to delete key: %s", err)
			}

			d.logger.Debugf("disco-nats-kv: key deleted: %s", key)

			return
		}
	}
}

func (d *KVDiscoverer) watcher(ctx context.Context) {
WATCH:
	watcher, err := d.jsKeyValue.WatchAll()
	if err != nil {
		d.logger.Errorf("disco-nats-kv:: %s", err)
		time.Sleep(time.Second * 2)
		goto WATCH
	}

	defer watcher.Stop()

	for {
		select {
		case update := <-watcher.Updates():
			if update != nil {
				newServers := d.getServers()

				if discovery.ServersDiffer(d.currentServers, newServers) {
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

func (d *KVDiscoverer) NewServers() chan struct{} {
	return d.ch
}

func (d *KVDiscoverer) GetServers() []raft.Server {
	d.mux.Lock()
	defer d.mux.Unlock()

	if d.currentServers == nil {
		d.currentServers = d.getServers()
	}

	return d.currentServers
}

func (d *KVDiscoverer) getServers() []raft.Server {
	keys, err := d.jsKeyValue.Keys()
	if err != nil {
		d.logger.Errorf("disco-nats-kv: get keys: %s", err)
		return nil
	}

	servers := make([]raft.Server, 0, len(keys))
	for _, key := range keys {
		rev, err := d.jsKeyValue.Get(key)
		if err != nil {
			d.logger.Errorf("disco-nats-kv: fetch key: %s", err)
			return nil
		}

		lastUpdate, err := time.Parse(time.RFC3339Nano, string(rev.Value()))
		if err != nil {
			d.logger.Errorf("disco-nats-kv: parsing key value: %s", err)
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
			d.logger.Debugf("disco-nats-kv: discarding stale server: %s", advertisedAddr)
		}
	}

	return servers
}
