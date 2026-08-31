package tbus

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/bus"
)

var decoderPool = sync.Pool{
	New: func() any {
		d := &payloadDecoder{}
		d.decode = d.Decode
		return d
	},
}

type payloadDecoder struct {
	payload any
	decode  func(v any) error
}

// Decode fills the target provided by action.ExecuteDecoded.
//
// Kernel behaviour:
//   - value Req  → decode receives *Req
//   - pointer Req → decode receives Req (already a *T from reflect.New)
//
// Payload may be T or *T; we accept both.
func (d *payloadDecoder) Decode(v any) error {
	if d.payload == nil {
		return nil
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("tbus: decode target must be a non-nil pointer")
	}

	pv := reflect.ValueOf(d.payload)
	dst := rv.Elem()

	// payload type matches destination (value Req path, value payload)
	if pv.Type().AssignableTo(dst.Type()) {
		dst.Set(pv)
		return nil
	}

	// payload is *T and destination is T (value Req + pointer publish,
	// or pointer Req after reflect.New)
	if pv.Kind() == reflect.Pointer && !pv.IsNil() && pv.Elem().Type().AssignableTo(dst.Type()) {
		dst.Set(pv.Elem())
		return nil
	}

	// destination is *T and payload is *T (pointer Req, pointer payload)
	if dst.Kind() == reflect.Pointer && pv.Type().AssignableTo(dst.Type()) {
		dst.Set(pv)
		return nil
	}

	return fmt.Errorf("tbus: type mismatch, got %T want %T", d.payload, dst.Interface())
}

// Transport bridges the internal Nexss Bus to the Action execution engine.
type Transport struct {
	eventBus *bus.Bus[any]
}

// New creates a new internal bus transport.
func New(eventBus *bus.Bus[any]) *Transport {
	return &Transport{
		eventBus: eventBus,
	}
}

// Mount scans actions for tbus.Topic bindings and subscribes them to the internal bus.
func (t *Transport) Mount(actions []action.AnyAction) {
	for _, act := range actions {
		ex, ok := act.(action.Executable)
		if !ok {
			continue
		}

		for _, b := range act.GetBindings() {
			if tb, ok := b.(TopicBinding); ok {
				t.eventBus.Subscribe(tb.Topic, func(ctx context.Context, payload any) error {
					d := decoderPool.Get().(*payloadDecoder)
					d.payload = payload

					_, err := ex.ExecuteDecoded(ctx, d.decode)

					d.payload = nil
					decoderPool.Put(d)
					return err
				})
			}
		}
	}
}

// Do implements the Transport interface. It just blocks since the bus is in-memory.
func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	<-ctx.Done()
	return nil, nil
}

// Publish is a convenience wrapper to emit events to the internal bus.
func (t *Transport) Publish(ctx context.Context, topic string, payload any) error {
	return t.eventBus.Publish(ctx, topic, payload)
}
