//go:build !race

package tbus_test

import (
	"context"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/bus"
	"github.com/nexssp/transport/tbus"
)

// EventDTO is a value type on purpose.
//
// Kernel ExecuteDecoded always calls reflect.New when Req is a pointer type,
// so a *EventDTO request can never be ≤1 alloc. Value types use:
//
//	var req Req
//	decode(&req)
//
// Residual allocs are only from escape analysis of &req into the shared
// decoder and the two reflect.ValueOf calls inside it.
type EventDTO struct {
	ID    int
	Value string
}

func TestTBusDispatch_ZeroAlloc(t *testing.T) {
	eventBus := bus.New[any]()
	tr := tbus.New(eventBus)

	act := action.New("bench.event", func(ctx context.Context, evt EventDTO) (EventDTO, error) {
		return evt, nil
	}).Route(tbus.Topic("bench.topic")).Build()

	tr.Mount([]action.AnyAction{act})

	ctx := context.Background()
	// Publish a pointer so boxing into any is free (fits in the interface data word).
	evt := &EventDTO{ID: 1, Value: "fast"}

	allocs := testing.AllocsPerRun(1000, func() {
		if err := tr.Publish(ctx, "bench.topic", evt); err != nil {
			t.Fatalf("publish: %v", err)
		}
	})

	// ≤3 accounts for: escaped req + reflect.ValueOf(v) + reflect.ValueOf(payload).
	// Pure bus.Publish (no action) is 0; action.Do is 0; the decode bridge is the cost.
	if allocs > 3 {
		t.Fatalf("expected <= 3 allocs on hotpath, got %.1f", allocs)
	}
}
