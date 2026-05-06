// Package cache provides a small TTL cache used to throttle redundant
// /proc/net/tcp scans and ss invocations under high health-check load.
package cache

import (
	"sync"
	"time"
)

type entry[T any] struct {
	value T
	until time.Time
}

type Cache[T any] struct {
	ttl  time.Duration
	mu   sync.RWMutex
	data map[string]entry[T]
	now  func() time.Time
}

func New[T any](ttl time.Duration) *Cache[T] {
	return &Cache[T]{ttl: ttl, data: make(map[string]entry[T]), now: time.Now}
}

func (c *Cache[T]) Get(key string) (T, bool) {
	var zero T
	if c.ttl <= 0 {
		return zero, false
	}
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()
	if !ok || c.now().After(e.until) {
		return zero, false
	}
	return e.value, true
}

func (c *Cache[T]) Set(key string, v T) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.data[key] = entry[T]{value: v, until: c.now().Add(c.ttl)}
	c.mu.Unlock()
}
