package taskqueue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexssp/transport/bus/taskqueue"
)

func TestTaskQueueShutdownDrainsTaskSelectedWithCancellation(t *testing.T) {
	q, err := taskqueue.New(taskqueue.Config{Name: "shutdown-drain", Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondDone := make(chan error, 1)
	q.Register("first", func(context.Context, taskqueue.Envelope) error {
		close(firstStarted)
		<-releaseFirst
		return nil
	})
	q.Register("second", func(ctx context.Context, _ taskqueue.Envelope) error {
		secondDone <- ctx.Err()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	startDone := make(chan error, 1)
	go func() {
		startDone <- q.StartWithReadyCallback(ctx, func() { close(started) })
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("queue did not start")
	}

	if err := q.Enqueue(context.Background(), "first", nil); err != nil {
		t.Fatalf("enqueue first task: %v", err)
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first task did not start")
	}
	if err := q.Enqueue(context.Background(), "second", nil); err != nil {
		t.Fatalf("enqueue second task: %v", err)
	}

	cancel()
	close(releaseFirst)

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second task got canceled context: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second task did not complete during shutdown drain")
	}
	select {
	case err := <-startDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queue shutdown error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queue did not finish shutdown")
	}
}
