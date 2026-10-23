// Command worker is a demo SQS consumer. It long-polls the queue, "processes"
// each message by sleeping, then deletes it. A small HTTP server exposes
// /healthz so the chart's default TCP probe has something to connect to.
package main

import (
	"context"
	"errors"
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
)

// queue is the part of the SQS client the worker needs; tests replace it.
type queue interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	queueURL := os.Getenv("QUEUE_URL")
	if queueURL == "" {
		log.Error("QUEUE_URL is required")
		os.Exit(1)
	}
	processTime := 2 * time.Second
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

	health := startHealth(":" + firstNonEmpty(os.Getenv("PORT"), "8080"))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = health.Shutdown(shutdownCtx)
	}()

	log.Info("worker started", "queue", queueURL, "processTime", processTime.String())
	run(ctx, sqs.NewFromConfig(cfg), queueURL, processTime)
	log.Info("worker stopped")
}

// run polls until ctx is cancelled, then waits for in-flight messages.
func run(ctx context.Context, q queue, queueURL string, processTime time.Duration) {
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
				handle(q, queueURL, m, processTime)
			}(m)
		}
	}
}

// handle processes one message and deletes it. The delete uses a fresh context
// so a message that finished during shutdown is still acknowledged.
func handle(q queue, queueURL string, m types.Message, processTime time.Duration) {
	start := time.Now()
	time.Sleep(processTime) // stand-in for real work

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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
