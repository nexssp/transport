package taskqueue_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/transport/bus/taskqueue"
)

// ── 1. Happy Path ────────────────────────────────────────────────────────────
type EmailPayload struct {
	To string `json:"to"`
}

func TestTaskQueue_Success(t *testing.T) {
	t.Parallel()
	q, _ := taskqueue.New(taskqueue.Config{Name: "success"})
	done := make(chan string, 1)

	q.Register("send.email", func(ctx context.Context, env taskqueue.Envelope) error {
		var p EmailPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return err
		}
		done <- p.To
		return nil
	})

	ctx := t.Context()
	go q.Start(ctx)

	time.Sleep(50 * time.Millisecond) // let workers start

	if err := q.Enqueue(context.Background(), "send.email", EmailPayload{To: "user@ex.com"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	select {
	case result := <-done:
		if result != "user@ex.com" {
			t.Fatalf("got %q", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// ── 2. Retries + DLQ ─────────────────────────────────────────────────────────
func TestTaskQueue_RetriesAndDLQ(t *testing.T) {
	t.Parallel()
	q, _ := taskqueue.New(taskqueue.Config{
		Name:        "dlq",
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
	})

	var attempts atomic.Int32
	q.Register("fails", func(ctx context.Context, env taskqueue.Envelope) error {
		attempts.Add(1)
		return errors.New("boom")
	})

	ctx := t.Context()

	go q.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	if err := q.Enqueue(context.Background(), "fails", nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	time.Sleep(1 * time.Second) // allow retries to complete

	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

// ── 3. Panic Recovery ────────────────────────────────────────────────────────
func TestTaskQueue_PanicRecovery(t *testing.T) {
	t.Parallel()
	q, _ := taskqueue.New(taskqueue.Config{
		Name:        "panic",
		MaxAttempts: 2,
		BaseDelay:   10 * time.Millisecond,
	})

	var attempts atomic.Int32
	q.Register("panics", func(ctx context.Context, env taskqueue.Envelope) error {
		attempts.Add(1)
		panic("sudden death")
	})

	ctx := t.Context()

	go q.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	_ = q.Enqueue(context.Background(), "panics", nil)

	time.Sleep(1 * time.Second)

	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts (panic recovery), got %d", got)
	}
}
