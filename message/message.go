// Package message provides a protocol-neutral envelope and router.
package message

import (
	"context"
	"errors"

	"github.com/nexssp/kernel/xerr"

	"github.com/nexssp/transport/bus"
)

var (
	ErrNilEnvelope   = errors.New("message: nil envelope")
	ErrTopicRequired = errors.New("message: topic is required")
)

// Envelope carries delivery identity and an opaque payload. Publish does not copy it.
type Envelope struct {
	Topic       string
	ID          string
	RequestID   string
	ExecutionID string
	TraceID     string
	SpanID      string
	Attempt     uint16
	Payload     any
}

// Message is a domain-friendly alias for Envelope.
type Message = Envelope

// Handler receives the caller-owned envelope.
type Handler func(context.Context, *Envelope) error

// Router is a concurrent in-process router. Publish is allocation-free for a ready envelope.
type Router struct {
	bus *bus.Bus[*Envelope]
}

func NewRouter() *Router {
	return &Router{bus: bus.New[*Envelope]()}
}

func (r *Router) Subscribe(topic string, h Handler) {
	r.bus.Subscribe(topic, bus.Handler[*Envelope](h))
}

func (r *Router) Publish(ctx context.Context, env *Envelope) error {
	if env == nil {
		return xerr.BadRequest(ErrNilEnvelope.Error(), ErrNilEnvelope)
	}
	if env.Topic == "" {
		return xerr.BadRequest(ErrTopicRequired.Error(), ErrTopicRequired)
	}
	return r.bus.Publish(ctx, env.Topic, env)
}
