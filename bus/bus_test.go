package bus_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nexssp/transport/bus"
)

// ── 1. Pub/Sub Bus Tests ─────────────────────────────────────────────────────

func TestBus_PubSub(t *testing.T) {
	t.Parallel()

	b := bus.New[string]()
	var receivedCount atomic.Int32

	// Subscribe two different handlers to the same subject
	b.Subscribe("events.user_created", func(ctx context.Context, msg string) error {
		if msg == "usr_123" {
			receivedCount.Add(1)
		}
		return nil
	})

	b.Subscribe("events.user_created", func(ctx context.Context, msg string) error {
		if msg == "usr_123" {
			receivedCount.Add(1)
		}
		return nil
	})

	// Publish an event
	err := b.Publish(context.Background(), "events.user_created", "usr_123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify both handlers received the message
	if receivedCount.Load() != 2 {
		t.Fatalf("expected 2 handlers to receive message, got %d", receivedCount.Load())
	}
}

// ── 2. Typed Bus (Commands) Tests ────────────────────────────────────────────

type CreateOrderCmd struct {
	ItemID   string
	Quantity int
}

type OrderRes struct {
	OrderID string
}

func TestTypedBus_CommandRouting(t *testing.T) {
	t.Parallel()

	tb := bus.NewTyped()

	// Register 1:1 handler
	bus.Register(tb, func(ctx context.Context, cmd CreateOrderCmd) (OrderRes, error) {
		if cmd.Quantity <= 0 {
			return OrderRes{}, errors.New("quantity must be > 0")
		}
		return OrderRes{OrderID: "ord_999"}, nil
	})

	// Send valid command
	res, err := bus.Send[CreateOrderCmd, OrderRes](tb, context.Background(), CreateOrderCmd{
		ItemID:   "itm_1",
		Quantity: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OrderID != "ord_999" {
		t.Fatalf("expected ord_999, got %v", res.OrderID)
	}

	// Send invalid command
	_, err = bus.Send[CreateOrderCmd, OrderRes](tb, context.Background(), CreateOrderCmd{
		ItemID:   "itm_1",
		Quantity: 0,
	})
	if err == nil || err.Error() != "quantity must be > 0" {
		t.Fatalf("expected quantity error, got %v", err)
	}
}

func TestTypedBus_MissingHandler(t *testing.T) {
	t.Parallel()
	tb := bus.NewTyped()

	type UnregisteredCmd struct{}
	_, err := bus.Send[UnregisteredCmd, string](tb, context.Background(), UnregisteredCmd{})
	if err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Fatalf("expected no handler error, got %v", err)
	}
}
