package tmsg

import (
	"context"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/kernel/xtest"
	"github.com/nexssp/kernel/xtest/ktest"

	"github.com/nexssp/transport/message"
)

type event struct {
	ID    int
	Value string
}

func TestTransportRoutesEnvelopePayloadToAction(t *testing.T) {
	tr := New(nil)
	var got event
	preflight := contextFromEnvelope(context.Background(), &message.Envelope{
		RequestID: "req_1", ExecutionID: "exec_1", TraceID: "trace_1", SpanID: "span_1",
	})
	ktest.RequireEqual(t, xctx.RequestIDFrom(preflight), "req_1")
	ktest.RequireEqual(t, xctx.ExecutionIDFrom(preflight), "exec_1")
	act := action.New("events.consume", func(ctx context.Context, in event) (event, error) {
		ktest.RequireEqual(t, xctx.RequestIDFrom(ctx), "req_1")
		ktest.RequireEqual(t, xctx.ExecutionIDFrom(ctx), "exec_1")
		ktest.RequireEqual(t, xctx.TraceIDFrom(ctx), "trace_1")
		ktest.RequireEqual(t, xctx.SpanIDFrom(ctx), "span_1")
		got = in
		return in, nil
	}).Route(Topic("events.created")).Build()
	tr.Mount([]action.AnyAction{act})

	err := tr.Publish(context.Background(), &message.Envelope{
		Topic:       "events.created",
		ID:          "msg_1",
		RequestID:   "req_1",
		ExecutionID: "exec_1",
		TraceID:     "trace_1",
		SpanID:      "span_1",
		Payload:     event{ID: 7, Value: "ready"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got != (event{ID: 7, Value: "ready"}) {
		t.Fatalf("action received %#v", got)
	}
}

func TestTransportDispatchZeroAllocBudget(t *testing.T) {
	tr := New(nil)
	act := action.New("events.consume", func(_ context.Context, in *event) (*event, error) {
		return in, nil
	}).Route(Topic("events.hot")).Build()
	tr.Mount([]action.AnyAction{act})

	env := &message.Envelope{Topic: "events.hot", Payload: &event{ID: 1, Value: "hot"}}
	xtest.RequireMaxAlloc(t, 1000, 3, func() {
		if err := tr.Publish(context.Background(), env); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	})
}
