// Command worker is a demo SQS consumer. It long-polls the queue, "processes"
// each message by sleeping, then deletes it. A small HTTP server exposes
// /healthz so the chart's default TCP probe has something to connect to.
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
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"demo/internal/db"
)

// queue is the part of the SQS client the worker needs; tests replace it.
type queue interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// screener completes a queued screening in the database. It reports whether a queued screening
// was found for the application.
type screener interface {
	Screen(ctx context.Context, applicationID int64) (bool, error)
}

// screenJob is the message the API queues for POST /applications/{id}/screen.
type screenJob struct {
	Type          string `json:"type"`
	ApplicationID int64  `json:"applicationId"`
}

func main() {
	// "-version" prints and exits at once. The image pre-puller runs every image with it as an
	// init container, which pulls the image onto the node without starting the service.
	if len(os.Args) > 1 && os.Args[1] == "-version" {
		fmt.Println("demo-worker dev")
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	queueURL := os.Getenv("QUEUE_URL")
	if queueURL == "" {
		log.Error("QUEUE_URL is required")
		os.Exit(1)
	}
	// No artificial delay by default; PROCESS_TIME (e.g. "2s") adds one for experiments.
	var processTime time.Duration
	if d, err := time.ParseDuration(os.Getenv("PROCESS_TIME")); err == nil && d >= 0 {
		processTime = d
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Error("load aws config", "err", err)
		os.Exit(1)
	}

	// SIGTERM stops polling; in-flight messages are finished before exit so a
	// spot interruption or scale-down does not drop work.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// ScaledJob mode: take one batch, process it and exit. No health server, the Job's pod just runs to completion.
	if os.Getenv("WORKER_MODE") == "job" {
		var sc screener
		if d := db.New(); d != nil {
			sc = d
		}
		batch := intFromEnv("JOB_BATCH", 5, 1, 10)
		wait := intFromEnv("JOB_WAIT_SECONDS", 2, 0, 20)
		log.Info("job started", "queue", queueURL, "batch", batch, "processTime", processTime.String(), "screening", sc != nil)
		n := runOnce(ctx, sqs.NewFromConfig(cfg), queueURL, batch, wait, processTime, sc)
		log.Info("job finished", "messages", n)
		return
	}

	health := startHealth(":" + firstNonEmpty(os.Getenv("PORT"), "8080"))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = health.Shutdown(shutdownCtx)
	}()

	// Screening messages need the database; without it only the demo messages can be processed.
	var sc screener
	if d := db.New(); d != nil {
		sc = d
	}

	log.Info("worker started", "queue", queueURL, "processTime", processTime.String(), "screening", sc != nil)
	run(ctx, sqs.NewFromConfig(cfg), queueURL, processTime, sc)
	log.Info("worker stopped")
}

// run polls until ctx is cancelled, then waits for in-flight messages.
func run(ctx context.Context, q queue, queueURL string, processTime time.Duration, sc screener) {
	var wg sync.WaitGroup
	defer wg.Wait()

	for ctx.Err() == nil {
		out, err := q.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20, // long polling
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("receive", "err", err)
			select {
			case <-time.After(2 * time.Second): // back off before retrying
			case <-ctx.Done():
			}
			continue
		}
		for _, m := range out.Messages {
			wg.Add(1)
			go func(m types.Message) {
				defer wg.Done()
				handle(q, queueURL, m, processTime, sc)
			}(m)
		}
	}
}

// handle processes one message and deletes it. Contexts are fresh (not tied to shutdown) so a
// message that finished while the worker was stopping is still acknowledged. If the real work
// fails the message is NOT deleted: SQS hands it out again, and after maxReceiveCount tries it
// moves to the dead-letter queue.
func handle(q queue, queueURL string, m types.Message, processTime time.Duration, sc screener) {
	start := time.Now()
	if processTime > 0 {
		time.Sleep(processTime) // optional stand-in for real work
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var job screenJob
	if json.Unmarshal([]byte(aws.ToString(m.Body)), &job) == nil && job.Type == "screen" {
		if sc == nil {
			slog.Error("screening message but no database configured", "messageId", aws.ToString(m.MessageId))
			return
		}
		found, err := sc.Screen(ctx, job.ApplicationID)
		if err != nil {
			slog.Error("screening failed", "err", err, "applicationId", job.ApplicationID)
			return
		}
		if !found {
			slog.Warn("no queued screening for this application (stale or repeated message)", "applicationId", job.ApplicationID)
		}
	}

	if _, err := q.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: aws.String(queueURL), ReceiptHandle: m.ReceiptHandle}); err != nil {
		slog.Error("delete", "err", err, "messageId", aws.ToString(m.MessageId))
		return
	}
	slog.Info("processed", "messageId", aws.ToString(m.MessageId), "tookMs", time.Since(start).Milliseconds())
}

func startHealth(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("health server", "err", err)
			os.Exit(1)
		}
	}()
	return srv
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
