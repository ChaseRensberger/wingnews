package main

import (
	"container/list"
	"context"
	"sync"
	"time"
)

type cacheEntry struct {
	value     any
	expiresAt time.Time
	element   *list.Element
}

type inflightCall struct {
	done  chan struct{}
	value any
	err   error
}

type memoryCache struct {
	mu       sync.RWMutex
	entries  map[string]*cacheEntry
	inflight map[string]*inflightCall
	lru      *list.List

	maxEntries      int
	cleanupInterval time.Duration

	onHit  func()
	onMiss func()
}

func newHNMemoryCache() *memoryCache {
	c := newMemoryCache(20000, time.Minute)
	c.onHit = func() { metrics.cacheHits.Add(1) }
	c.onMiss = func() { metrics.cacheMisses.Add(1) }
	return c
}

func newCommentsMemoryCache() *memoryCache {
	return newMemoryCache(1500, 20*time.Second)
}

func newMemoryCache(maxEntries int, cleanupInterval time.Duration) *memoryCache {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	if cleanupInterval <= 0 {
		cleanupInterval = 30 * time.Second
	}

	c := &memoryCache{
		entries:         map[string]*cacheEntry{},
		inflight:        map[string]*inflightCall{},
		lru:             list.New(),
		maxEntries:      maxEntries,
		cleanupInterval: cleanupInterval,
	}

	go c.startJanitor()
	return c
}

func (c *memoryCache) getOrLoad(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (any, error)) (any, error) {
	now := time.Now()

	c.mu.RLock()
	entry, hasEntry := c.entries[key]
	if hasEntry && now.Before(entry.expiresAt) {
		value := entry.value
		c.mu.RUnlock()
		c.mu.Lock()
		if entry.element != nil {
			c.lru.MoveToFront(entry.element)
		}
		c.mu.Unlock()
		if c.onHit != nil {
			c.onHit()
		}
		return value, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()

	entry, hasEntry = c.entries[key]
	if hasEntry && now.Before(entry.expiresAt) {
		c.lru.MoveToFront(entry.element)
		value := entry.value
		c.mu.Unlock()
		if c.onHit != nil {
			c.onHit()
		}
		return value, nil
	}

	stale := any(nil)
	hasStale := false
	if hasEntry {
		stale = entry.value
		hasStale = true
	}

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

	if c.onMiss != nil {
		c.onMiss()
	}

	loaded, err := loader(ctx)
	if err != nil && hasStale {
		loaded = stale
		err = nil
	}

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil {
		c.upsert(key, loaded, time.Now().Add(ttl))
		c.enforceCapacityLocked()
	}
	call.value = loaded
	call.err = err
	close(call.done)
	c.mu.Unlock()

	return loaded, err
}

func (c *memoryCache) upsert(key string, value any, expiresAt time.Time) {
	if entry, exists := c.entries[key]; exists {
		entry.value = value
		entry.expiresAt = expiresAt
		c.lru.MoveToFront(entry.element)
		return
	}

	elem := c.lru.PushFront(key)
	c.entries[key] = &cacheEntry{
		value:     value,
		expiresAt: expiresAt,
		element:   elem,
	}
}

func (c *memoryCache) removeEntryLocked(key string) {
	entry, exists := c.entries[key]
	if !exists {
		return
	}
	if entry.element != nil {
		c.lru.Remove(entry.element)
	}
	delete(c.entries, key)
}

func (c *memoryCache) enforceCapacityLocked() {
	for len(c.entries) > c.maxEntries {
		back := c.lru.Back()
		if back == nil {
			return
		}
		key, _ := back.Value.(string)
		c.removeEntryLocked(key)
	}
}

func (c *memoryCache) collectExpiredKeys(now time.Time) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var expired []string
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			expired = append(expired, key)
		}
	}
	return expired
}

func (c *memoryCache) startJanitor() {
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()

		expired := c.collectExpiredKeys(now)
		if len(expired) > 0 {
			c.mu.Lock()
			for _, key := range expired {
				if entry, exists := c.entries[key]; exists && now.After(entry.expiresAt) {
					c.removeEntryLocked(key)
				}
			}
			c.enforceCapacityLocked()
			c.mu.Unlock()
		}
	}
}
