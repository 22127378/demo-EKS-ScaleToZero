package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakeQueue hands out its messages once, then blocks until the context ends,
// like a long poll on an empty queue.
type fakeQueue struct {
	mu      sync.Mutex
	pending []types.Message
	deleted []string
}

func (f *fakeQueue) ReceiveMessage(ctx context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	msgs := f.pending
	f.pending = nil
	f.mu.Unlock()
	if len(msgs) > 0 {
		return &sqs.ReceiveMessageOutput{Messages: msgs}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeQueue) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, *in.ReceiptHandle)
	return &sqs.DeleteMessageOutput{}, nil
}

func TestRunProcessesAndDeletesThenStops(t *testing.T) {
	h1, h2 := "h1", "h2"
	q := &fakeQueue{pending: []types.Message{{ReceiptHandle: &h1}, {ReceiptHandle: &h2}}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		run(ctx, q, "http://queue", 10*time.Millisecond)
		close(done)
	}()

	// Wait until both messages are acknowledged, then ask the worker to stop.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		n := len(q.deleted)
		q.mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	if len(q.deleted) != 2 {
		t.Fatalf("deleted = %v, want 2 messages", q.deleted)
	}
}
