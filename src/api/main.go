// Command api is a tiny demo HTTP service. It reports where it runs (pod, node,
// CPU architecture) so scaling behaviour is visible, can burn CPU on demand to
// trigger the HPA, and can enqueue jobs on SQS for the worker.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"demo/internal/db"
)

const (
	maxBurn     = 10 * time.Second // upper bound for /burn so a typo cannot pin a CPU forever
	maxJobs     = 100              // upper bound for one POST /jobs call
	sqsBatchMax = 10               // SendMessageBatch accepts at most 10 entries
)

// sender is the part of the SQS client the handlers need; tests replace it.
type sender interface {
	SendMessageBatch(ctx context.Context, in *sqs.SendMessageBatchInput, opts ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error)
}

// visitor counts visits in the database (GET /db). A separate small interface keeps its tests simple.
type visitor interface {
	Visit(ctx context.Context) (int64, error)
}

type server struct {
	started  time.Time
	pod      string
	node     string
	queueURL string
	sqs      sender
	db       visitor // nil when no database is configured
	st       store   // the recruitment data, nil when no database is configured
}

func main() {
	// "-version" prints and exits at once. The image pre-puller runs every image with it as an
	// init container, which pulls the image onto the node without starting the service.
	if len(os.Args) > 1 && os.Args[1] == "-version" {
		fmt.Println("demo-api dev")
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	// Simulate a slow-starting app so cold-start measurements can include it.
	if d, err := time.ParseDuration(os.Getenv("STARTUP_DELAY")); err == nil && d > 0 {
		log.Info("startup delay", "delay", d.String())
		time.Sleep(d)
	}

	s := &server{
		started:  time.Now(),
		pod:      firstNonEmpty(os.Getenv("POD_NAME"), hostname()),
		node:     os.Getenv("NODE_NAME"),
		queueURL: os.Getenv("QUEUE_URL"),
	}
	if s.queueURL != "" {
		cfg, err := config.LoadDefaultConfig(context.Background())
		if err != nil {
			log.Error("load aws config", "err", err)
			os.Exit(1)
		}
		s.sqs = sqs.NewFromConfig(cfg)
	}

	// Assign only when configured: a nil *db.DB stored in an interface would not compare equal to nil.
	if d := db.New(); d != nil {
		s.db = d
		s.st = pgStore{d: d}
	}

	srv := &http.Server{
		Addr:              ":" + firstNonEmpty(os.Getenv("PORT"), "8080"),
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown: on SIGTERM stop accepting new connections and let
	// in-flight requests finish, so spot interruptions and scale-down lose nothing.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		log.Info("listening", "addr", srv.Addr, "pod", s.pod, "node", s.node, "arch", runtime.GOARCH)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.info)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /burn", s.burn)
	mux.HandleFunc("GET /slow", s.slow)
	mux.HandleFunc("POST /jobs", s.jobs)
	mux.HandleFunc("GET /db", s.dbVisit)
	s.registerDomain(mux)
	return cors(logging(mux))
}

func (s *server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"pod":           s.pod,
		"node":          s.node,
		"arch":          runtime.GOARCH,
		"startedAt":     s.started.UTC().Format(time.RFC3339),
		"uptimeSeconds": int(time.Since(s.started).Seconds()),
	})
}

// burn spins one goroutine for ?ms= milliseconds (default 1000, capped).
func (s *server) burn(w http.ResponseWriter, r *http.Request) {
	ms, err := strconv.Atoi(r.URL.Query().Get("ms"))
	if err != nil || ms <= 0 {
		ms = 1000
	}
	d := time.Duration(ms) * time.Millisecond
	if d > maxBurn {
		d = maxBurn
	}
	deadline := time.Now().Add(d)
	n := 0
	for time.Now().Before(deadline) {
		n++ // busy loop on purpose
	}
	writeJSON(w, http.StatusOK, map[string]any{"pod": s.pod, "burnedMs": d.Milliseconds(), "iterations": n})
}

// slow answers after ?ms= milliseconds (default 1000, capped) without using CPU. It stands in for a request that takes
// a while (a report, a call to a slow dependency): many of them in flight at once is what makes the HTTP autoscaler
// add pods, which fast requests never do.
func (s *server) slow(w http.ResponseWriter, r *http.Request) {
	ms, err := strconv.Atoi(r.URL.Query().Get("ms"))
	if err != nil || ms <= 0 {
		ms = 1000
	}
	d := time.Duration(ms) * time.Millisecond
	if d > maxBurn {
		d = maxBurn
	}
	select {
	case <-time.After(d):
	case <-r.Context().Done():
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pod": s.pod, "waitedMs": d.Milliseconds()})
}

// dbVisit increments the visit counter in the database and reports how long that took.
func (s *server) dbVisit(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "DB_HOST is not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	n, err := s.db.Visit(ctx)
	if err != nil {
		slog.Error("db visit", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "database unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"visits":      n,
		"dbLatencyMs": float64(time.Since(start).Microseconds()) / 1000,
		"pod":         s.pod,
	})
}

type jobsRequest struct {
	Count   int    `json:"count"`
	Payload string `json:"payload"`
}

// jobs enqueues Count messages (default 1, capped) on the configured SQS queue.
func (s *server) jobs(w http.ResponseWriter, r *http.Request) {
	if s.sqs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "QUEUE_URL is not configured"})
		return
	}
	var req jobsRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
			return
		}
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.Count > maxJobs {
		req.Count = maxJobs
	}

	sent := 0
	for sent < req.Count {
		n := min(sqsBatchMax, req.Count-sent)
		entries := make([]types.SendMessageBatchRequestEntry, n)
		for i := range entries {
			entries[i] = types.SendMessageBatchRequestEntry{
				Id:          aws.String(strconv.Itoa(i)),
				MessageBody: aws.String(fmt.Sprintf(`{"seq":%d,"payload":%q,"from":%q}`, sent+i, req.Payload, s.pod)),
			}
		}
		out, err := s.sqs.SendMessageBatch(r.Context(), &sqs.SendMessageBatchInput{QueueUrl: aws.String(s.queueURL), Entries: entries})
		if err != nil {
			slog.Error("send batch", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "failed to send to SQS", "sent": sent})
			return
		}
		sent += len(out.Successful)
		if len(out.Failed) > 0 {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "some messages failed", "sent": sent})
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"enqueued": sent})
}

// cors allows the browser to call the API directly when the frontend is served
// from a different origin. CORS_ORIGIN defaults to "*" since this is a demo.
func cors(next http.Handler) http.Handler {
	origin := firstNonEmpty(os.Getenv("CORS_ORIGIN"), "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Skip health checks to keep the log readable.
		if r.URL.Path != "/healthz" {
			slog.Info("request", "method", r.Method, "path", r.URL.Path, "durationMs", time.Since(start).Milliseconds())
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
