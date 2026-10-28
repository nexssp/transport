package tworker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/tworker"
)

func TestTransport_WorkerExecution(t *testing.T) {
	t.Parallel()

	var counter atomic.Int32

	act := action.New("test.worker", func(_ context.Context, _ struct{}) (string, error) {
		counter.Add(1)
		return "ok", nil
	}).Route(tworker.Every(10 * time.Millisecond)).Build()

	tr := tworker.New()
	tr.Mount([]action.AnyAction{act})

	ctx, cancel := context.WithCancel(context.Background())

	// Start transport in background
	go func() { _, _ = tr.Do(ctx, nil) }()

	// Wait enough time for a few intervals to fire
	time.Sleep(50 * time.Millisecond)
	cancel() // trigger graceful shutdown

	count := counter.Load()
	if count < 2 {
		t.Fatalf("expected worker to run at least 2 times, ran %d times", count)
	}
}

func TestTransportSignalsReadyAfterWorkersAreStarted(t *testing.T) {
	act := action.New("test.long_lived_worker", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	}).Route(tworker.Every(time.Hour)).Build()
	tr := tworker.New()
	tr.Mount([]action.AnyAction{act})

	signal := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), signal))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := tr.Do(ctx, nil)
		done <- err
	}()
	select {
	case <-signal.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("worker transport did not signal readiness")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker transport did not join its workers after cancellation")
	}
}
