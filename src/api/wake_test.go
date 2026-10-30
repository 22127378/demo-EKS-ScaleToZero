package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeKube answers the three calls the waker makes and records the Jobs it is asked to create.
type fakeKube struct {
	mu      sync.Mutex
	active  int
	created []map[string]any
}

func (f *fakeKube) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer token on %s", r.URL.Path)
		}
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/scaledjobs/worker"):
			_, _ = io.WriteString(w, `{"metadata":{"uid":"u-1"},"spec":{"jobTargetRef":{"backoffLimit":0,"template":{"spec":{"restartPolicy":"Never"}}}}}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/jobs"):
			if r.URL.Query().Get("labelSelector") != "scaledjob.keda.sh/name=worker" {
				t.Errorf("labelSelector = %q", r.URL.Query().Get("labelSelector"))
			}
			items := make([]string, f.active)
			for i := range items {
				items[i] = `{"status":{"active":1}}`
			}
			items = append(items, `{"status":{"completionTime":"2026-01-01T00:00:00Z"}}`) // finished Jobs are not counted
			_, _ = io.WriteString(w, `{"items":[`+strings.Join(items, ",")+`]}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/jobs"):
			var job map[string]any
			_ = json.NewDecoder(r.Body).Decode(&job)
			f.mu.Lock()
			f.created = append(f.created, job)
			f.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newTestWaker(url string, max int) *k8sWaker {
	return &k8sWaker{client: http.DefaultClient, base: url, token: func() string { return "tok" }, namespace: "demo", scaledJob: "worker", maxActive: max}
}

func TestWakeCreatesAJobFromTheScaledJobTemplate(t *testing.T) {
	f := &fakeKube{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	newTestWaker(srv.URL, 3).Wake(context.Background())

	if len(f.created) != 1 {
		t.Fatalf("created %d Jobs, want 1", len(f.created))
	}
	meta := f.created[0]["metadata"].(map[string]any)
	if meta["generateName"] != "worker-" {
		t.Errorf("generateName = %v", meta["generateName"])
	}
	// KEDA counts Jobs by this label; without it the wake-up Job would be invisible and duplicated.
	if meta["labels"].(map[string]any)["scaledjob.keda.sh/name"] != "worker" {
		t.Errorf("labels = %v", meta["labels"])
	}
	if f.created[0]["spec"].(map[string]any)["backoffLimit"] != float64(0) {
		t.Errorf("spec was not copied from jobTargetRef: %v", f.created[0]["spec"])
	}
}

func TestWakeSkipsWhenEnoughJobsAreActive(t *testing.T) {
	f := &fakeKube{active: 3}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	newTestWaker(srv.URL, 3).Wake(context.Background())

	if len(f.created) != 0 {
		t.Fatalf("created %d Jobs, want none while 3 are active", len(f.created))
	}
}

func TestWakeFailureIsHarmless(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()

	newTestWaker(srv.URL, 3).Wake(context.Background()) // must not panic or block
}

func TestWakerIsOffWithoutConfiguration(t *testing.T) {
	t.Setenv("WAKE_SCALEDJOB", "")
	if newWakerFromEnv() != nil {
		t.Fatal("waker should be nil when WAKE_SCALEDJOB is unset")
	}
	s := &server{}
	s.wakeWorker() // a nil waker must be a no-op
}
