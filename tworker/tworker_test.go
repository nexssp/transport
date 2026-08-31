package tworker_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/tworker"
)

func TestTransport_WorkerExecution(t *testing.T) {
	t.Parallel()

	var counter atomic.Int32

	act := action.New("test.worker", func(ctx context.Context, _ struct{}) (string, error) {
		counter.Add(1)
		return "ok", nil
	}).Route(tworker.Every(10 * time.Millisecond)).Build()

	tr := tworker.New()
	tr.Mount([]action.AnyAction{act})

	ctx, cancel := context.WithCancel(context.Background())

	// Start transport in background
	go tr.Do(ctx, nil)

	// Wait enough time for a few intervals to fire
	time.Sleep(50 * time.Millisecond)
	cancel() // trigger graceful shutdown

	count := counter.Load()
	if count < 2 {
		t.Fatalf("expected worker to run at least 2 times, ran %d times", count)
	}
}
