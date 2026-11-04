package main

import (
	"context"
	"testing"
	"time"
)

type countingStore struct {
	store
	summaries, byStatus int
}

func (c *countingStore) Summary(context.Context) (summary, error) {
	c.summaries++
	return summary{Applicants: c.summaries}, nil
}

func (c *countingStore) CountByStatus(context.Context) (map[string]int, error) {
	c.byStatus++
	return map[string]int{"applied": c.byStatus}, nil
}

func TestCachedStatsServesFromMemoryUntilTTL(t *testing.T) {
	inner := &countingStore{}
	st := newCachedStats(inner, 50*time.Millisecond)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if m, _ := st.Summary(ctx); m.Applicants != 1 {
			t.Fatalf("Summary call %d = %d, want cached 1", i, m.Applicants)
		}
		if m, _ := st.CountByStatus(ctx); m["applied"] != 1 {
			t.Fatalf("CountByStatus call %d = %d, want cached 1", i, m["applied"])
		}
	}
	if inner.summaries != 1 || inner.byStatus != 1 {
		t.Fatalf("database reads = %d/%d, want 1/1", inner.summaries, inner.byStatus)
	}

	time.Sleep(60 * time.Millisecond)
	if m, _ := st.Summary(ctx); m.Applicants != 2 {
		t.Fatalf("Summary after TTL = %d, want a fresh read (2)", m.Applicants)
	}
}

func TestCachedStatsDisabledWithZeroTTL(t *testing.T) {
	inner := &countingStore{}
	if got := newCachedStats(inner, 0); got != store(inner) {
		t.Fatal("zero TTL must return the store unchanged")
	}
}
