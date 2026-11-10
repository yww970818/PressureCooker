package main

import (
	"errors"
	"sync"

	"pressurecooker/execution/objectstore"
	"pressurecooker/execution/queue"
)

// Process dependencies are shared by project-scoped Platform values. Request
// handles release their own ownership; only Platform.Close closes the pool.
type platformDependencies struct {
	mu     sync.Mutex
	queues map[queue.Config]*queue.Client
	stores map[objectstore.Config]*objectstore.Client
	closed bool
}

type queueHandle struct {
	*queue.Client
	owned bool
}

func (h *queueHandle) Close() error {
	if h.owned {
		return h.Client.Close()
	}
	return nil
}

func queueConfig(c Config) queue.Config {
	return queue.Config{Addr: c.RedisAddr, Password: c.RedisPassword, DB: c.RedisDB, Prefix: c.RedisPrefix}
}

func (p *Platform) runnerQueue() (*queueHandle, error) {
	// Small standalone adapters may construct a Platform without process-owned
	// dependencies. Their handle continues to own its connection explicitly.
	if p.dependencies == nil {
		client, err := queue.New(queueConfig(p.Config))
		if err != nil {
			return nil, err
		}
		return &queueHandle{Client: client, owned: true}, nil
	}
	d := p.dependencies
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("platform dependencies closed")
	}
	cfg := queueConfig(p.Config)
	if d.queues == nil {
		d.queues = make(map[queue.Config]*queue.Client)
	}
	client := d.queues[cfg]
	if client == nil {
		var err error
		client, err = queue.New(cfg)
		if err != nil {
			return nil, err
		}
		d.queues[cfg] = client
	}
	return &queueHandle{Client: client}, nil
}

func (d *platformDependencies) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	var failures []error
	for _, client := range d.queues {
		failures = append(failures, client.Close())
	}
	for _, client := range d.stores {
		client.CloseIdleConnections()
	}
	return errors.Join(failures...)
}

// Close is the process ownership boundary, after HTTP and background work stop.
func (p *Platform) Close() error {
	if p.borrowed {
		return nil
	}
	var dependencyError, databaseError error
	if p.dependencies != nil {
		dependencyError = p.dependencies.close()
	}
	if p.DB != nil {
		databaseError = p.DB.Close()
	}
	return errors.Join(dependencyError, databaseError)
}

// Object clients own bounded HTTP connection pools and can be reused across
// project/request values. Immutable configuration keys prevent stale endpoints
// or credentials being silently reused by a distinct dependency configuration.
func (p *Platform) objectClient(cfg objectstore.Config) (*objectstore.Client, error) {
	if p.dependencies == nil {
		return objectstore.New(cfg)
	}
	d := p.dependencies
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("platform dependencies closed")
	}
	if d.stores == nil {
		d.stores = make(map[objectstore.Config]*objectstore.Client)
	}
	client := d.stores[cfg]
	if client == nil {
		var err error
		client, err = objectstore.New(cfg)
		if err != nil {
			return nil, err
		}
		d.stores[cfg] = client
	}
	return client, nil
}
