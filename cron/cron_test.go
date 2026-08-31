package cron_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/cron"
)

func TestCronTransport_ScheduleExecution(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	act := action.New("cron.job", func(ctx context.Context, _ struct{}) (string, error) {
		callCount.Add(1)
		return "ok", nil
	}).Route(cron.Every(1 * time.Second)).Build()

	tr := cron.New()
	tr.Mount([]action.AnyAction{act})

	ctx, cancel := context.WithCancel(context.Background())

	go tr.Do(ctx, nil)

	time.Sleep(1100 * time.Millisecond) // Allow 1s tick to fire
	cancel()

	if count := callCount.Load(); count < 1 {
		t.Fatalf("expected cron job to run at least 1 time, ran %d times", count)
	}
}
