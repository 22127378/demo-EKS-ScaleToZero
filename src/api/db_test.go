package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

type fakeVisitor struct {
	n   int64
	err error
}

func (f *fakeVisitor) Visit(context.Context) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.n++
	return f.n, nil
}

func serverWithDB(v visitor) http.Handler {
	s := &server{started: time.Now(), pod: "pod-1", node: "node-1", db: v}
	return s.routes()
}

func TestDBVisitCounts(t *testing.T) {
	h := serverWithDB(&fakeVisitor{})
	for want := int64(1); want <= 3; want++ {
		rec := do(h, http.MethodGet, "/db", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /db = %d (%s)", rec.Code, rec.Body)
		}
		var got struct {
			Visits int64   `json:"visits"`
			Pod    string  `json:"pod"`
			Ms     float64 `json:"dbLatencyMs"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Visits != want || got.Pod != "pod-1" {
			t.Fatalf("visit %d: got %+v", want, got)
		}
	}
}

func TestDBUnavailableKeepsAPIAlive(t *testing.T) {
	h := serverWithDB(&fakeVisitor{err: errors.New("connection refused")})
	if rec := do(h, http.MethodGet, "/db", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /db with failing DB = %d, want 503", rec.Code)
	}
	// The rest of the API must keep working while the database is down.
	if rec := do(h, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
}

func TestDBNotConfigured(t *testing.T) {
	if rec := do(newTestServer(nil), http.MethodGet, "/db", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /db without DB = %d, want 503", rec.Code)
	}
}
