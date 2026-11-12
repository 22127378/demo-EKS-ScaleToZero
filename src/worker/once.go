package main

import (
	"context"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// runOnce is the KEDA ScaledJob mode (WORKER_MODE=job): receive one batch of at most batch messages,
// process them concurrently, delete each one that succeeded, and return how many were received.
// The pod then exits, so a Job ends as soon as its batch is done instead of idling on the queue.
// A message that fails is not deleted: SQS hands it out again after the visibility timeout.
//
// With linger > 0 the Job stays alive and keeps polling for that long after its last message was deleted. KEDA
// subtracts running Jobs from the Jobs it wants, and SQS keeps reporting a deleted message for about 6 seconds;
// a Job that exits right after its delete leaves a gap in which KEDA starts an empty extra Job. A linger longer
// than that lag closes the gap, and a message that arrives meanwhile is taken by the lingering Job at once.
func runOnce(ctx context.Context, q queue, queueURL string, batch, waitSeconds int32, linger, processTime time.Duration, sc screener) int {
	out, err := q.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:                    aws.String(queueURL),
		MaxNumberOfMessages:         batch,
		WaitTimeSeconds:             waitSeconds,
		MessageSystemAttributeNames: receiveAttrs,
	})
	if err != nil || len(out.Messages) == 0 {
		return 0 // nothing to do (another Job took them first) or the queue is unreachable
	}

	total := processAll(q, queueURL, out.Messages, processTime, sc)
	if linger > 0 {
		total += lingerFor(ctx, q, queueURL, batch, linger, processTime, sc)
	}
	return total
}

// processAll handles the messages concurrently and returns how many there were.
func processAll(q queue, queueURL string, msgs []types.Message, processTime time.Duration, sc screener) int {
	var wg sync.WaitGroup
	for _, m := range msgs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle(q, queueURL, m, processTime, sc)
		}()
	}
	wg.Wait()
	return len(msgs)
}

// lingerGrace is how long past the linger a receive call may run before its context cancels it. The long poll
// ends by itself at WaitTimeSeconds; cancelling a receive that is still in flight could leave messages hidden.
var lingerGrace = 1500 * time.Millisecond

// lingerMax caps how long a Job keeps lingering under a steady stream of messages, so it ends well before the
// Job's activeDeadlineSeconds.
var lingerMax = 120 * time.Second

// lingerFor polls for new messages until linger has passed since the last one was handled, and returns how many
// it handled.
func lingerFor(ctx context.Context, q queue, queueURL string, batch int32, linger, processTime time.Duration, sc screener) int {
	total := 0
	begin := time.Now()
	idleSince := time.Now()
	for ctx.Err() == nil && time.Since(begin) < lingerMax {
		left := linger - time.Since(idleSince)
		if left <= 0 {
			break
		}
		rctx, cancel := context.WithTimeout(ctx, left+lingerGrace)
		out, err := q.ReceiveMessage(rctx, &sqs.ReceiveMessageInput{
			QueueUrl:                    aws.String(queueURL),
			MaxNumberOfMessages:         batch,
			WaitTimeSeconds:             int32(max(1, int(math.Ceil(left.Seconds())))),
			MessageSystemAttributeNames: receiveAttrs,
		})
		cancel()
		if err != nil {
			break
		}
		if len(out.Messages) > 0 {
			total += processAll(q, queueURL, out.Messages, processTime, sc)
			idleSince = time.Now() // the stale count starts again from this delete
		}
	}
	return total
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
