package main

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func TestRunOnceProcessesTheBatchAndReturns(t *testing.T) {
	h1, h2 := "h1", "h2"
	q := &fakeQueue{pending: []types.Message{{ReceiptHandle: &h1}, {ReceiptHandle: &h2}}}

	n := runOnce(context.Background(), q, "http://queue", 5, 0, 10*time.Millisecond, nil)

	if n != 2 {
		t.Fatalf("runOnce = %d, want 2", n)
	}
	if len(q.deleted) != 2 {
		t.Fatalf("deleted = %v, want both messages", q.deleted)
	}
}

func TestRunOnceOnAnEmptyQueueReturnsZero(t *testing.T) {
	q := &fakeQueue{}
	// The fake blocks like a long poll on an empty queue until the context ends.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if n := runOnce(ctx, q, "http://queue", 5, 1, time.Millisecond, nil); n != 0 {
		t.Fatalf("runOnce = %d, want 0", n)
	}
}

func TestIntFromEnvClampsAndFallsBack(t *testing.T) {
	t.Setenv("X_BATCH", "99")
	if got := intFromEnv("X_BATCH", 5, 1, 10); got != 10 {
		t.Fatalf("clamp high = %d, want 10", got)
	}
	t.Setenv("X_BATCH", "oops")
	if got := intFromEnv("X_BATCH", 5, 1, 10); got != 5 {
		t.Fatalf("bad value = %d, want default 5", got)
	}
	if got := intFromEnv("X_UNSET", 3, 1, 10); got != 3 {
		t.Fatalf("unset = %d, want 3", got)
	}
}
