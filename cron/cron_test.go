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

func TestCronTransport_MountAccumulatesActions(t *testing.T) {
	t.Parallel()

	calls := make(chan string, 2)
	first := action.New("cron.first", func(_ context.Context, _ struct{}) (string, error) {
		calls <- "first"
		return "ok", nil
	}).Route(cron.Every(time.Second)).Build()
	second := action.New("cron.second", func(_ context.Context, _ struct{}) (string, error) {
		calls <- "second"
		return "ok", nil
	}).Route(cron.Every(time.Second)).Build()

	tr := cron.New()
	tr.Mount([]action.AnyAction{first})
	tr.Mount([]action.AnyAction{second})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = tr.Do(ctx, nil)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("cron scheduler did not stop after cancellation")
		}
	})

	seen := make(map[string]bool, 2)
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for len(seen) < 2 {
		select {
		case name := <-calls:
			seen[name] = true
		case <-timer.C:
			t.Fatalf("both mounted jobs should run within 3s, saw %v", seen)
		}
	}
}

func TestCronTransport_RecoversPanickingExecutable(t *testing.T) {
	t.Parallel()

	var panicCalls atomic.Int32
	base := action.New("cron.panic", func(_ context.Context, _ struct{}) (string, error) {
		return "unused", nil
	}).Route(cron.Every(time.Second)).Build()
	panicking := panickingExecutable{AnyAction: base, calls: &panicCalls}

	ran := make(chan struct{}, 1)
	healthy := action.New("cron.healthy", func(_ context.Context, _ struct{}) (string, error) {
		ran <- struct{}{}
		return "ok", nil
	}).Route(cron.Every(time.Second)).Build()

	tr := cron.New()
	tr.Mount([]action.AnyAction{panicking, healthy})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := tr.Do(ctx, nil)
		done <- err
	}()

	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("healthy cron job did not run after panicking job")
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
	if calls := panicCalls.Load(); calls == 0 {
		t.Fatal("panicking executable was not called")
	}
}

type panickingExecutable struct {
	action.AnyAction
	calls *atomic.Int32
}

func (p panickingExecutable) ExecuteDecoded(context.Context, action.DecodeFunc) (any, error) {
	p.calls.Add(1)
	panic("test panic")
}
