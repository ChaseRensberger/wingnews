package main

import (
	"context"
	"sync"
	"time"
)

type cacheEntry struct {
	value     any
	expiresAt time.Time
}

type inflightCall struct {
	done  chan struct{}
	value any
	err   error
}

type memoryCache struct {
	mu       sync.Mutex
	entries  map[string]cacheEntry
	inflight map[string]*inflightCall
}

func newMemoryCache() *memoryCache {
	return &memoryCache{
		entries:  map[string]cacheEntry{},
		inflight: map[string]*inflightCall{},
	}
}

func (c *memoryCache) getOrLoad(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (any, error)) (any, error) {
	now := time.Now()

	c.mu.Lock()
	entry, hasEntry := c.entries[key]
	if hasEntry && now.Before(entry.expiresAt) {
		value := entry.value
		c.mu.Unlock()
		return value, nil
	}

	stale, hasStale := entry.value, hasEntry

	if call, exists := c.inflight[key]; exists {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			return call.value, call.err
		}
	}

	call := &inflightCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	loaded, err := loader(ctx)
	if err != nil && hasStale {
		loaded = stale
		err = nil
	}

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil {
		c.entries[key] = cacheEntry{value: loaded, expiresAt: time.Now().Add(ttl)}
	}
	call.value = loaded
	call.err = err
	close(call.done)
	c.mu.Unlock()

	return loaded, err
}
