package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type fakeSQS struct{ batches []int }

func (f *fakeSQS) SendMessageBatch(_ context.Context, in *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
	f.batches = append(f.batches, len(in.Entries))
	ok := make([]types.SendMessageBatchResultEntry, len(in.Entries))
	return &sqs.SendMessageBatchOutput{Successful: ok}, nil
}

func newTestServer(q sender) http.Handler {
	s := &server{started: time.Now(), pod: "pod-1", node: "node-1", queueURL: "http://queue", sqs: q}
	return s.routes()
}

func do(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestInfoAndHealth(t *testing.T) {
	h := newTestServer(nil)

	rec := do(h, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["pod"] != "pod-1" || got["node"] != "node-1" || got["arch"] == "" {
		t.Fatalf("unexpected info: %v", got)
	}

	if rec := do(h, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /nope = %d, want 404", rec.Code)
	}
}

func TestBurnIsCapped(t *testing.T) {
	h := newTestServer(nil)
	start := time.Now()
	rec := do(h, http.MethodGet, "/burn?ms=50", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /burn = %d", rec.Code)
	}
	if el := time.Since(start); el < 50*time.Millisecond || el > 2*time.Second {
		t.Fatalf("burn took %s", el)
	}
}

func TestJobsBatching(t *testing.T) {
	f := &fakeSQS{}
	h := newTestServer(f)

	rec := do(h, http.MethodPost, "/jobs", `{"count":25}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /jobs = %d (%s)", rec.Code, rec.Body)
	}
	if len(f.batches) != 3 || f.batches[0] != 10 || f.batches[1] != 10 || f.batches[2] != 5 {
		t.Fatalf("batches = %v, want [10 10 5]", f.batches)
	}

	f.batches = nil
	do(h, http.MethodPost, "/jobs", `{"count":100000}`)
	total := 0
	for _, n := range f.batches {
		total += n
	}
	if total != maxJobs {
		t.Fatalf("count not capped: sent %d, want %d", total, maxJobs)
	}
}

func TestJobsWithoutQueue(t *testing.T) {
	rec := do(newTestServer(nil), http.MethodPost, "/jobs", `{"count":1}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /jobs without queue = %d, want 503", rec.Code)
	}
}

func TestJobsBadJSON(t *testing.T) {
	rec := do(newTestServer(&fakeSQS{}), http.MethodPost, "/jobs", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /jobs bad json = %d, want 400", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	rec := do(newTestServer(nil), http.MethodOptions, "/jobs", "")
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("preflight = %d headers=%v", rec.Code, rec.Header())
	}
}
