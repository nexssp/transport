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

func TestTaskQueueStartCallbackRunsAfterWorkerLaunch(t *testing.T) {
	q, err := taskqueue.New(taskqueue.Config{Name: "ready", Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	handled := make(chan struct{})
	q.Register("ready", func(context.Context, taskqueue.Envelope) error {
		close(handled)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- q.StartWithReadyCallback(ctx, func() { close(started) })
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("queue did not report that workers started")
	}
	if err := q.Enqueue(context.Background(), "ready", nil); err != nil {
		t.Fatalf("enqueue after readiness: %v", err)
	}
	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("queue was signaled ready before workers could process tasks")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queue shutdown error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queue did not join its workers after cancellation")
	}
}

func TestTaskQueueSequentialSecondStartFailsPromptly(t *testing.T) {
	q, err := taskqueue.New(taskqueue.Config{Name: "sequential-start", Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	started := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- q.StartWithReadyCallback(firstCtx, func() { close(started) })
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first queue start did not launch its workers")
	}

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondDone := make(chan error, 1)
	go func() { secondDone <- q.StartWithReadyCallback(secondCtx, nil) }()
	select {
	case err := <-secondDone:
		if !errors.Is(err, taskqueue.ErrAlreadyStarted) {
			t.Fatalf("second start error = %v, want ErrAlreadyStarted", err)
		}
	case <-time.After(2 * time.Second):
		cancelSecond()
		t.Fatal("sequential second start did not fail promptly")
	}

	cancelFirst()
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first start shutdown error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first queue start did not join its workers after cancellation")
	}
}

func TestTaskQueueConcurrentSecondStartFailsPromptly(t *testing.T) {
	q, err := taskqueue.New(taskqueue.Config{Name: "concurrent-start", Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	type startResult struct {
		index int
		err   error
	}
	contexts := [2]context.Context{}
	cancels := [2]context.CancelFunc{}
	contexts[0], cancels[0] = context.WithCancel(context.Background())
	contexts[1], cancels[1] = context.WithCancel(context.Background())
	t.Cleanup(cancels[0])
	t.Cleanup(cancels[1])
	gate := make(chan struct{})
	readyToStart := make(chan struct{}, 2)
	results := make(chan startResult, 2)
	var callbackCount atomic.Int32
	for i := range contexts {
		go func(index int) {
			readyToStart <- struct{}{}
			<-gate
			err := q.StartWithReadyCallback(contexts[index], func() { callbackCount.Add(1) })
			results <- startResult{index: index, err: err}
		}(i)
	}
	for range 2 {
		select {
		case <-readyToStart:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent start callers did not reach the start barrier")
		}
	}
	close(gate)

	var alreadyStarted, canceled int
	for range 2 {
		select {
		case result := <-results:
			switch {
			case errors.Is(result.err, taskqueue.ErrAlreadyStarted):
				alreadyStarted++
				cancels[0]()
				cancels[1]()
			case errors.Is(result.err, context.Canceled):
				canceled++
			default:
				t.Errorf("start call %d returned %v, want ErrAlreadyStarted or context.Canceled", result.index, result.err)
				cancels[0]()
				cancels[1]()
			}
		case <-time.After(2 * time.Second):
			cancels[0]()
			cancels[1]()
			t.Fatal("concurrent second start did not return promptly")
		}
	}
	if alreadyStarted != 1 || canceled != 1 {
		t.Fatalf("concurrent start results: already-started=%d canceled=%d, want one each", alreadyStarted, canceled)
	}
	if got := callbackCount.Load(); got != 1 {
		t.Fatalf("ready callback count = %d, want 1", got)
	}
}

func TestTaskQueue_Success(t *testing.T) {
	t.Parallel()
	q, _ := taskqueue.New(taskqueue.Config{Name: "success"})
	done := make(chan string, 1)

	q.Register("send.email", func(_ context.Context, env taskqueue.Envelope) error {
		var p EmailPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return err
		}
		done <- p.To
		return nil
	})

	ctx := t.Context()
	go func() { _ = q.Start(ctx) }()

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
	q.Register("fails", func(_ context.Context, _ taskqueue.Envelope) error {
		attempts.Add(1)
		return errors.New("boom")
	})

	ctx := t.Context()

	go func() { _ = q.Start(ctx) }()
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
	q.Register("panics", func(_ context.Context, _ taskqueue.Envelope) error {
		attempts.Add(1)
		panic("sudden death")
	})

	ctx := t.Context()

	go func() { _ = q.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)

	_ = q.Enqueue(context.Background(), "panics", nil)

	time.Sleep(1 * time.Second)

	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts (panic recovery), got %d", got)
	}
}
