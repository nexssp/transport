package cron_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/cron"
)

func TestCronTransport_RestartUsesFreshScheduler(t *testing.T) {
	var calls atomic.Int32
	var canceledContextCalls atomic.Int32

	act := action.New("cron.restart", func(ctx context.Context, _ struct{}) (string, error) {
		calls.Add(1)
		if ctx.Err() != nil {
			canceledContextCalls.Add(1)
		}
		return "ok", nil
	}).Route(cron.Every(time.Second)).Build()

	tr := cron.New()
	tr.Mount([]action.AnyAction{act})

	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() {
		_, err := tr.Do(ctx1, nil)
		done1 <- err
	}()
	waitForCalls(t, &calls, 1)
	cancel1()
	waitForDo(t, done1)

	countAfterFirstRun := calls.Load()
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() {
		_, err := tr.Do(ctx2, nil)
		done2 <- err
	}()
	waitForCalls(t, &calls, countAfterFirstRun+2)
	cancel2()
	waitForDo(t, done2)

	if got := canceledContextCalls.Load(); got != 0 {
		t.Fatalf("callbacks ran with canceled run contexts: %d", got)
	}
}

func waitForCalls(t *testing.T, calls *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for calls.Load() < want {
		select {
		case <-deadline.C:
			if got := calls.Load(); got < want {
				t.Fatalf("timed out waiting for %d calls; got %d", want, got)
			}
			return
		case <-ticker.C:
		}
	}
}

func waitForDo(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cron Do error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cron Do did not stop after cancellation")
	}
}
