// Package tmsg routes canonical messages to Kernel actions.
package tmsg

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/kernel/xerr"

	"github.com/nexssp/transport"
	"github.com/nexssp/transport/message"
)

var _ transport.Transport = (*Transport)(nil)

var decoderPool = sync.Pool{
	New: func() any {
		d := &payloadDecoder{}
		d.decode = d.Decode
		return d
	},
}

type payloadDecoder struct {
	payload any
	decode  func(any) error
}

func (d *payloadDecoder) Decode(v any) error {
	if d.payload == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return xerr.BadRequest("tmsg: decode target must be a non-nil pointer")
	}
	pv := reflect.ValueOf(d.payload)
	dst := rv.Elem()
	if pv.Type().AssignableTo(dst.Type()) {
		dst.Set(pv)
		return nil
	}
	if pv.Kind() == reflect.Pointer && !pv.IsNil() && pv.Elem().Type().AssignableTo(dst.Type()) {
		dst.Set(pv.Elem())
		return nil
	}
	if dst.Kind() == reflect.Pointer && pv.Type().AssignableTo(dst.Type()) {
		dst.Set(pv)
		return nil
	}
	return xerr.BadRequest(fmt.Sprintf("tmsg: type mismatch, got %T want %T", d.payload, dst.Interface()))
}

// Transport routes envelopes to Topic-bound actions.
type Transport struct {
	router *message.Router
}

func New(router *message.Router) *Transport {
	if router == nil {
		router = message.NewRouter()
	}
	return &Transport{router: router}
}

func (t *Transport) CanHandle(b action.Binding) bool {
	_, ok := b.(TopicBinding)
	return ok
}

func (t *Transport) String() string { return "message" }

func (t *Transport) Mount(actions []action.AnyAction) {
	for _, act := range actions {
		ex, ok := act.(action.Executable)
		if !ok {
			continue
		}
		for _, binding := range act.GetBindings() {
			topic, ok := binding.(TopicBinding)
			if !ok || topic.Topic == "" {
				continue
			}
			t.router.Subscribe(topic.Topic, func(ctx context.Context, env *message.Envelope) error {
				d, ok := decoderPool.Get().(*payloadDecoder)
				if !ok {
					// unreachable in practice: decoderPool.New always returns *payloadDecoder
					return xerr.Internal("tmsg: unexpected decoder pool type")
				}
				d.payload = env.Payload
				_, err := ex.ExecuteDecoded(contextFromEnvelope(ctx, env), d.decode)
				d.payload = nil
				decoderPool.Put(d)
				return err
			})
		}
	}
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (t *Transport) Publish(ctx context.Context, env *message.Envelope) error {
	return t.router.Publish(ctx, env)
}

func (t *Transport) Router() *message.Router { return t.router }

func contextFromEnvelope(ctx context.Context, env *message.Envelope) context.Context {
	if env.RequestID != "" {
		ctx = xctx.WithRequestID(ctx, env.RequestID)
	}
	if env.ExecutionID != "" {
		ctx = xctx.WithExecutionID(ctx, env.ExecutionID)
	}
	if env.TraceID != "" || env.SpanID != "" {
		ctx = xctx.WithTraceContext(ctx, env.TraceID, env.SpanID)
	}
	return ctx
}
