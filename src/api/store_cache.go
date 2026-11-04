package main

import (
	"context"
	"maps"
	"sync"
	"time"
)

// cachedStats serves the dashboard counters from memory for a short time. They are read by every
// page load but change only when data is written, so a few seconds of staleness saves the database
// six count queries per request. A zero ttl turns the cache off (the default, and what the tests use).
type cachedStats struct {
	store
	ttl time.Duration

	mu       sync.Mutex
	sum      summary
	sumAt    time.Time
	byStatus map[string]int
	byStatAt time.Time
}

func newCachedStats(st store, ttl time.Duration) store {
	if ttl <= 0 {
		return st
	}
	return &cachedStats{store: st, ttl: ttl}
}

func (c *cachedStats) Summary(ctx context.Context) (summary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.sumAt.IsZero() && time.Since(c.sumAt) < c.ttl {
		return c.sum, nil
	}
	m, err := c.store.Summary(ctx)
	if err != nil {
		return summary{}, err
	}
	c.sum, c.sumAt = m, time.Now()
	return m, nil
}

func (c *cachedStats) CountByStatus(ctx context.Context) (map[string]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byStatus != nil && time.Since(c.byStatAt) < c.ttl {
		return maps.Clone(c.byStatus), nil
	}
	m, err := c.store.CountByStatus(ctx)
	if err != nil {
		return nil, err
	}
	c.byStatus, c.byStatAt = m, time.Now()
	return maps.Clone(m), nil
}
