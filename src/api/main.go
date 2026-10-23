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

type server struct {
	started  time.Time
	pod      string
	node     string
	queueURL string
	sqs      sender
}

func main() {
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
	mux.HandleFunc("POST /jobs", s.jobs)
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
