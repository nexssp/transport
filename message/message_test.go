package message

import (
	"context"
	"testing"

	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/kernel/xtest"
	"github.com/nexssp/kernel/xtest/ktest"
)

func TestRouterPublishesCanonicalEnvelope(t *testing.T) {
	r := NewRouter()
	var got *Envelope
	r.Subscribe("orders.created", func(_ context.Context, env *Envelope) error {
		got = env
		return nil
	})

	env := &Envelope{
		Topic:       "orders.created",
		ID:          "msg_1",
		RequestID:   "req_1",
		ExecutionID: "exec_1",
		TraceID:     "trace_1",
		SpanID:      "span_1",
		Payload:     "order-payload",
	}
	if err := r.Publish(context.Background(), env); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got != env {
		t.Fatal("router must pass the caller-owned envelope without copying")
	}
	if got.ExecutionID != "exec_1" || got.Payload != "order-payload" {
		t.Fatalf("envelope changed during dispatch: %#v", got)
	}
}

func TestRouterRejectsInvalidEnvelope(t *testing.T) {
	r := NewRouter()
	nilErr := r.Publish(context.Background(), nil)
	ktest.RequireErrorIs(t, nilErr, ErrNilEnvelope)
	ktest.RequireErrorKind(t, nilErr, xerr.KindBadRequest)
	emptyTopicErr := r.Publish(context.Background(), &Envelope{})
	ktest.RequireErrorIs(t, emptyTopicErr, ErrTopicRequired)
	ktest.RequireErrorKind(t, emptyTopicErr, xerr.KindBadRequest)
}

func TestRouterPublishZeroAlloc(t *testing.T) {
	r := NewRouter()
	var count int
	r.Subscribe("hot.path", func(_ context.Context, env *Envelope) error {
		count += int(env.Attempt)
		return nil
	})
	env := &Envelope{Topic: "hot.path", Attempt: 1, Payload: &struct{ Value int }{Value: 7}}
	ctx := context.Background()

	xtest.RequireZeroAlloc(t, 1000, func() {
		if err := r.Publish(ctx, env); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	})
	if count == 0 {
		t.Fatal("handler did not receive messages")
	}
}
