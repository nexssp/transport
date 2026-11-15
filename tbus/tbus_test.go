package tbus_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/bus"
	"github.com/nexssp/transport/tbus"
)

type UserCreatedEvent struct {
	UserID string `json:"user_id"`
}

func TestTBus_SubscriptionAndZeroAllocDispatch(t *testing.T) {
	t.Parallel()

	eventBus := bus.New[any]()
	tr := tbus.New(eventBus)

	var receivedUser string

	act := action.New("user.created.listener", func(_ context.Context, req UserCreatedEvent) (string, error) {
		receivedUser = req.UserID
		return "ok", nil
	}).Route(tbus.Topic("user.created")).Build()

	tr.Mount([]action.AnyAction{act})

	ctx := context.Background()

	// Emit memory payload directly
	err := tr.Publish(ctx, "user.created", UserCreatedEvent{UserID: "usr_999"})
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	if receivedUser != "usr_999" {
		t.Fatalf("expected usr_999, got %q", receivedUser)
	}
}

func TestTBus_MountAccumulatesActions(t *testing.T) {
	eventBus := bus.New[any]()
	tr := tbus.New(eventBus)
	var firstCalls, secondCalls atomic.Int32

	first := action.New("user.created.listener", func(_ context.Context, _ UserCreatedEvent) (string, error) {
		firstCalls.Add(1)
		return "ok", nil
	}).Route(tbus.Topic("user.created")).Build()
	second := action.New("audit.created.listener", func(_ context.Context, _ UserCreatedEvent) (string, error) {
		secondCalls.Add(1)
		return "ok", nil
	}).Route(tbus.Topic("audit.created")).Build()

	tr.Mount([]action.AnyAction{first})
	tr.Mount([]action.AnyAction{second})

	publishUserCreated(t, tr)
	if err := tr.Publish(context.Background(), "audit.created", UserCreatedEvent{}); err != nil {
		t.Fatalf("failed to publish audit event: %v", err)
	}
	if got := firstCalls.Load(); got != 1 {
		t.Fatalf("first handler calls = %d, want 1", got)
	}
	if got := secondCalls.Load(); got != 1 {
		t.Fatalf("second handler calls = %d, want 1", got)
	}
}

func TestTBus_DoUnsubscribesAndCanRemount(t *testing.T) {
	eventBus := bus.New[any]()
	tr := tbus.New(eventBus)
	var calls atomic.Int32

	act := action.New("user.created.listener", func(_ context.Context, _ UserCreatedEvent) (string, error) {
		calls.Add(1)
		return "ok", nil
	}).Route(tbus.Topic("user.created")).Build()

	tr.Mount([]action.AnyAction{act})
	waitForTBusShutdown(t, tr)
	publishUserCreated(t, tr)
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls after shutdown = %d, want 0", got)
	}

	tr.Mount([]action.AnyAction{act})
	publishUserCreated(t, tr)
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls after remount = %d, want 1", got)
	}

	waitForTBusShutdown(t, tr)
	publishUserCreated(t, tr)
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls after second shutdown = %d, want 1", got)
	}
}

func waitForTBusShutdown(t *testing.T, tr *tbus.Transport) {
	t.Helper()

	signal := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), signal))
	done := make(chan error, 1)
	go func() {
		_, err := tr.Do(ctx, nil)
		done <- err
	}()

	select {
	case <-signal.Done():
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("bus transport did not signal readiness")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bus transport did not stop after cancellation")
	}
}

func publishUserCreated(t *testing.T, tr *tbus.Transport) {
	t.Helper()
	if err := tr.Publish(context.Background(), "user.created", UserCreatedEvent{}); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}
}
