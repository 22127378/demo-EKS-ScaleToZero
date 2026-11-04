package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type fakeScreener struct {
	calls   []int64
	found   bool
	err     error
	failed  []string // reasons passed to FailScreening
	failErr error
}

func (f *fakeScreener) FailScreening(_ context.Context, _ int64, reason string) (bool, error) {
	f.failed = append(f.failed, reason)
	return true, f.failErr
}

func (f *fakeScreener) Screen(_ context.Context, id int64) (bool, error) {
	f.calls = append(f.calls, id)
	return f.found, f.err
}

func msg(body string) types.Message {
	return types.Message{Body: aws.String(body), ReceiptHandle: aws.String("rh"), MessageId: aws.String("m1")}
}

func TestScreenMessageIsScreenedThenDeleted(t *testing.T) {
	q := &fakeQueue{}
	sc := &fakeScreener{found: true}
	handle(q, "http://queue", msg(`{"type":"screen","applicationId":42}`), 0, sc)
	if len(sc.calls) != 1 || sc.calls[0] != 42 {
		t.Fatalf("screener calls = %v, want [42]", sc.calls)
	}
	if len(q.deleted) != 1 {
		t.Fatalf("message not deleted after screening: %v", q.deleted)
	}
}

func TestScreenFailureKeepsTheMessageForRetry(t *testing.T) {
	q := &fakeQueue{}
	handle(q, "http://queue", msg(`{"type":"screen","applicationId":7}`), 0, &fakeScreener{err: errors.New("db down")})
	if len(q.deleted) != 0 {
		t.Fatalf("a failed screening must not delete the message, deleted: %v", q.deleted)
	}
}

func TestStaleScreenMessageIsDropped(t *testing.T) {
	q := &fakeQueue{}
	handle(q, "http://queue", msg(`{"type":"screen","applicationId":7}`), 0, &fakeScreener{found: false})
	if len(q.deleted) != 1 {
		t.Fatalf("a stale message should be acknowledged, deleted: %v", q.deleted)
	}
}

func TestScreenMessageWithoutDatabaseIsNotDeleted(t *testing.T) {
	q := &fakeQueue{}
	handle(q, "http://queue", msg(`{"type":"screen","applicationId":7}`), 0, nil)
	if len(q.deleted) != 0 {
		t.Fatalf("without a database the message must stay on the queue, deleted: %v", q.deleted)
	}
}

func TestDemoMessageStillWorks(t *testing.T) {
	q := &fakeQueue{}
	sc := &fakeScreener{}
	handle(q, "http://queue", msg(`{"seq":1,"payload":"load","from":"api"}`), 5*time.Millisecond, sc)
	if len(sc.calls) != 0 || len(q.deleted) != 1 {
		t.Fatalf("demo message: screener calls %v, deleted %v", sc.calls, q.deleted)
	}
}

func msgAttempt(body string, attempt string) types.Message {
	m := msg(body)
	m.Attributes = map[string]string{"ApproximateReceiveCount": attempt}
	return m
}

func TestScreenFailureOnAnEarlyAttemptDoesNotMarkFailed(t *testing.T) {
	q := &fakeQueue{}
	sc := &fakeScreener{err: errors.New("db down")}
	handle(q, "http://queue", msgAttempt(`{"type":"screen","applicationId":7}`, "2"), 0, sc)
	if len(sc.failed) != 0 {
		t.Fatalf("marked failed on attempt 2 of %d: %v", maxReceive(), sc.failed)
	}
	if len(q.deleted) != 0 {
		t.Fatalf("message deleted although screening failed: %v", q.deleted)
	}
}

func TestScreenFailureOnTheLastAttemptMarksTheApplicationFailed(t *testing.T) {
	q := &fakeQueue{}
	sc := &fakeScreener{err: errors.New("db down")}
	handle(q, "http://queue", msgAttempt(`{"type":"screen","applicationId":7}`, "8"), 0, sc)
	if len(sc.failed) != 1 || sc.failed[0] != "db down" {
		t.Fatalf("failed reasons = %v, want [db down]", sc.failed)
	}
	if len(q.deleted) != 0 {
		t.Fatalf("message must stay for the dead-letter queue, but it was deleted: %v", q.deleted)
	}
}

func TestMaxReceiveFromEnvironment(t *testing.T) {
	t.Setenv("MAX_RECEIVE", "5")
	if got := maxReceive(); got != 5 {
		t.Fatalf("maxReceive = %d, want 5", got)
	}
	t.Setenv("MAX_RECEIVE", "")
	if got := maxReceive(); got != 8 {
		t.Fatalf("default maxReceive = %d, want 8", got)
	}
}
