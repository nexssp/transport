package tworker_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/tworker"
)

type continueCycle bool

func (c continueCycle) ContinueWorkerCycle() bool { return bool(c) }

func TestWorkerImmediatelyDrainsWhenCycleSignalsMoreWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	second := make(chan struct{})
	act := action.New("drain.cycle", func(context.Context, struct{}) (continueCycle, error) {
		if calls.Add(1) == 1 {
			return continueCycle(true), nil
		}
		close(second)
		cancel()
		return continueCycle(false), nil
	}).Route(tworker.Every(time.Hour)).Build()

	tr := tworker.New()
	tr.Mount([]action.AnyAction{act})
	done := make(chan error, 1)
	go func() { _, err := tr.Do(ctx, nil); done <- err }()
	select {
	case <-second:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker did not immediately start the next signaled cycle")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context.Canceled", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("cycle calls = %d, want 2", calls.Load())
	}
}

func TestWorkerBackoffAndStatusObserver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	var mu sync.Mutex
	var statuses []tworker.WorkerStatus
	var backoffAttempts []int
	act := action.New("retry.cycle", func(context.Context, struct{}) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("mock failure")
		}
		cancel()
		return "ok", nil
	}).Route(tworker.Every(time.Millisecond)).Build()

	tr := tworker.New(tworker.Options{
		Backoff: func(attempt int) time.Duration {
			mu.Lock()
			backoffAttempts = append(backoffAttempts, attempt)
			mu.Unlock()
			return 10 * time.Millisecond
		},
		OnStatus: func(status tworker.WorkerStatus) {
			mu.Lock()
			statuses = append(statuses, status)
			mu.Unlock()
		},
	})
	tr.Mount([]action.AnyAction{act})
	_, err := tr.Do(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context.Canceled", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("cycle calls = %d, want retry after failure", calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(backoffAttempts) != 1 || backoffAttempts[0] != 1 {
		t.Fatalf("custom backoff attempts = %v, want [1]", backoffAttempts)
	}
	foundBackoff := false
	for _, status := range statuses {
		if status.State == tworker.StateBackoff && status.ConsecutiveErrors == 1 {
			foundBackoff = true
		}
	}
	if !foundBackoff {
		t.Fatalf("status events did not report backoff: %+v", statuses)
	}
}

func TestWorkerStopsOnNegativeCustomBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	stopped := make(chan struct{})
	var stopOnce sync.Once
	act := action.New("negative.backoff", func(context.Context, struct{}) (string, error) {
		calls.Add(1)
		return "", errors.New("mock failure")
	}).Route(tworker.Every(time.Millisecond)).Build()
	tr := tworker.New(tworker.Options{
		Backoff: func(int) time.Duration { return -time.Nanosecond },
		OnStatus: func(status tworker.WorkerStatus) {
			if status.State == tworker.StateStopped {
				stopOnce.Do(func() {
					close(stopped)
					cancel()
				})
			}
		},
	})
	tr.Mount([]action.AnyAction{act})
	done := make(chan error, 1)
	go func() { _, err := tr.Do(ctx, nil); done <- err }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after the backoff policy returned a negative delay")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("worker calls = %d, want 1 after invalid backoff", got)
	}
}

func TestWorkerRejectsNonPositiveIntervals(t *testing.T) {
	act := action.New("invalid.interval", func(context.Context, struct{}) (string, error) { return "ok", nil }).
		Route(tworker.Every(0)).Build()
	tr := tworker.New()
	tr.Mount([]action.AnyAction{act})
	if _, err := tr.Do(context.Background(), nil); err == nil {
		t.Fatal("worker with a zero interval unexpectedly started")
	}
}
