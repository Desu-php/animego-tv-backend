// Package cache contains a bounded in-memory adapter with independent caller cancellation.
package cache

import (
	"container/list"
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type entry[T any] struct {
	key       string
	value     T
	expires   time.Time
	refreshed time.Time
}

type loadPolicy struct {
	refresh         bool
	minimumInterval time.Duration
}

type Cache[T any] struct {
	mu           sync.Mutex
	items        map[string]*list.Element
	order        *list.List
	group        singleflight.Group
	capacity     int
	ttl, timeout time.Duration
	clone        func(T) T
	now          func() time.Time
}

// New requires positive bounds and a clone function that isolates mutable values.
func New[T any](capacity int, ttl, timeout time.Duration, clone func(T) T) *Cache[T] {
	if capacity <= 0 || ttl <= 0 || timeout <= 0 || clone == nil {
		panic("cache requires positive bounds and a clone function")
	}
	return &Cache[T]{items: make(map[string]*list.Element), order: list.New(), capacity: capacity, ttl: ttl, timeout: timeout, clone: clone, now: time.Now}
}

func (c *Cache[T]) lookup(key string) (T, bool) {
	return c.lookupWithPolicy(key, loadPolicy{})
}

func (c *Cache[T]) lookupWithPolicy(key string, policy loadPolicy) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if item, ok := c.items[key]; ok {
		value := item.Value.(entry[T])
		if c.now().Before(value.expires) {
			if policy.refresh && (value.refreshed.IsZero() || !c.now().Before(value.refreshed.Add(policy.minimumInterval))) {
				var zero T
				return zero, false
			}
			c.order.MoveToFront(item)
			return value.value, true
		}
		c.order.Remove(item)
		delete(c.items, key)
	}
	var zero T
	return zero, false
}

func (c *Cache[T]) store(key string, value T, refreshed time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if item, ok := c.items[key]; ok {
		c.order.Remove(item)
	}
	c.items[key] = c.order.PushFront(entry[T]{key: key, value: value, expires: c.now().Add(c.ttl), refreshed: refreshed})
	for len(c.items) > c.capacity {
		oldest := c.order.Back()
		delete(c.items, oldest.Value.(entry[T]).key)
		c.order.Remove(oldest)
	}
}

func (c *Cache[T]) GetOrLoad(ctx context.Context, key string, load func(context.Context) (T, error)) (T, error) {
	return c.getOrLoad(ctx, key, load, loadPolicy{})
}

// Refresh replaces a cached value through the same per-key flight as a miss.
// Successful refreshes are reused for minimumInterval to bound repeated probes.
// A failed refresh preserves the last cached value and may be retried.
func (c *Cache[T]) Refresh(ctx context.Context, key string, minimumInterval time.Duration, load func(context.Context) (T, error)) (T, error) {
	if minimumInterval <= 0 {
		panic("cache refresh interval must be positive")
	}
	return c.getOrLoad(ctx, key, load, loadPolicy{refresh: true, minimumInterval: minimumInterval})
}

func (c *Cache[T]) getOrLoad(ctx context.Context, key string, load func(context.Context) (T, error), policy loadPolicy) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if value, ok := c.lookupWithPolicy(key, policy); ok {
		return c.clone(value), nil
	}
	result := c.group.DoChan(key, func() (any, error) {
		if value, ok := c.lookupWithPolicy(key, policy); ok {
			return value, nil
		}
		// A departing caller must not cancel another caller's shared request.
		shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()
		value, err := load(shared)
		if err == nil {
			var refreshed time.Time
			if policy.refresh {
				refreshed = c.now()
			}
			c.store(key, value, refreshed)
		}
		return value, err
	})
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case result := <-result:
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if result.Err != nil {
			return zero, result.Err
		}
		return c.clone(result.Val.(T)), nil
	}
}
