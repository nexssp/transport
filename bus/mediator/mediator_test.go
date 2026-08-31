package mediator_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/nexssp/transport/bus/mediator"
)

type (
	CreateUserCmd  struct{ Email string }
	CreateUserRes  struct{ ID string }
	UserCreatedEvt struct{ ID string }
)

func TestMediator_CommandRouting(t *testing.T) {
	t.Parallel()
	m := mediator.New()

	mediator.Register(m, func(ctx context.Context, cmd CreateUserCmd) (CreateUserRes, error) {
		if cmd.Email == "" {
			return CreateUserRes{}, errors.New("empty email")
		}
		return CreateUserRes{ID: "usr_123"}, nil
	})

	res, err := mediator.Send[CreateUserCmd, CreateUserRes](m, context.Background(), CreateUserCmd{Email: "test@domain.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ID != "usr_123" {
		t.Fatalf("expected usr_123, got %v", res.ID)
	}

	_, err = mediator.Send[CreateUserCmd, CreateUserRes](m, context.Background(), CreateUserCmd{Email: ""})
	if err == nil {
		t.Fatal("expected error for empty email")
	}
}

func TestMediator_EventPublishing(t *testing.T) {
	t.Parallel()
	m := mediator.New()
	var evtCount atomic.Int32

	mediator.Subscribe(m, func(ctx context.Context, evt UserCreatedEvt) error {
		if evt.ID == "usr_123" {
			evtCount.Add(1)
		}
		return nil
	})
	mediator.Subscribe(m, func(ctx context.Context, evt UserCreatedEvt) error {
		evtCount.Add(1)
		return nil
	})

	err := mediator.Publish(m, context.Background(), UserCreatedEvt{ID: "usr_123"})
	if err != nil {
		t.Fatalf("unexpected error publishing: %v", err)
	}

	if evtCount.Load() != 2 {
		t.Fatalf("expected 2 subscribers to receive event, got %d", evtCount.Load())
	}
}
