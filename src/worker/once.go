package main

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// runOnce is the KEDA ScaledJob mode (WORKER_MODE=job): receive one batch of at most batch messages,
// process them concurrently, delete each one that succeeded, and return how many were received.
// The pod then exits, so a Job ends as soon as its batch is done instead of idling on the queue.
// A message that fails is not deleted: SQS hands it out again after the visibility timeout.
func runOnce(ctx context.Context, q queue, queueURL string, batch, waitSeconds int32, processTime time.Duration, sc screener) int {
	out, err := q.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: batch,
		WaitTimeSeconds:     waitSeconds,
	})
	if err != nil || len(out.Messages) == 0 {
		return 0 // nothing to do (another Job took them first) or the queue is unreachable
	}

	var wg sync.WaitGroup
	for _, m := range out.Messages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle(q, queueURL, m, processTime, sc)
		}()
	}
	wg.Wait()
	return len(out.Messages)
}

// intFromEnv reads an integer environment variable and clamps it to [lo, hi].
func intFromEnv(name string, def, lo, hi int) int32 {
	v := def
	if s := os.Getenv(name); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			v = n
		}
	}
	return int32(min(max(v, lo), hi))
}
