package tbus_test

import (
	"context"
	"testing"

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

	act := action.New("user.created.listener", func(ctx context.Context, req UserCreatedEvent) (string, error) {
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
