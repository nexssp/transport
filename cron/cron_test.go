package cron_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/cron"
)

func TestCronSignalsReadyAfterSchedulerStarts(t *testing.T) {
	signal := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), signal))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := cron.New().Do(ctx, nil)
		done <- err
	}()
	select {
	case <-signal.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cron scheduler did not signal readiness")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cron scheduler did not stop after cancellation")
	}
}

func TestCronTransport_ScheduleExecution(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	act := action.New("cron.job", func(_ context.Context, _ struct{}) (string, error) {
		callCount.Add(1)
		return "ok", nil
	}).Route(cron.Every(1 * time.Second)).Build()

	tr := cron.New()
	tr.Mount([]action.AnyAction{act})

	ctx, cancel := context.WithCancel(context.Background())

	go func() { _, _ = tr.Do(ctx, nil) }()

	time.Sleep(1100 * time.Millisecond) // Allow 1s tick to fire
	cancel()

	if count := callCount.Load(); count < 1 {
		t.Fatalf("expected cron job to run at least 1 time, ran %d times", count)
	}
}
