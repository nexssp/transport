// Package mediator provides a CQRS-style mediator.
// Commands: 1:1 — one handler, returns a response.
// Events:   1:N — fire-and-forget to all subscribers.
package mediator

import (
	"context"
	"errors"
	"fmt"

	"github.com/nexssp/transport/bus"
)

var ErrTypeMismatch = errors.New("mediator: type mismatch")

type Event = any

// Mediator combines command routing and event publishing.
type Mediator struct {
	commands *bus.TypedBus
	events   *bus.Bus[Event]
}

func New() *Mediator {
	return &Mediator{
		commands: bus.NewTyped(),
		events:   bus.New[Event](),
	}
}

func NewWithBus(events *bus.Bus[Event]) *Mediator {
	return &Mediator{commands: bus.NewTyped(), events: events}
}

// Register adds a 1:1 command handler.
func Register[Req, Res any](m *Mediator, h func(context.Context, Req) (Res, error)) {
	bus.Register[Req, Res](m.commands, h)
}

// Send dispatches a command to its registered handler.
func Send[Req, Res any](m *Mediator, ctx context.Context, req Req) (Res, error) {
	return bus.Send[Req, Res](m.commands, ctx, req)
}

// Subscribe registers a 1:N event handler.
func Subscribe[E any](m *Mediator, h func(context.Context, E) error) {
	var zero E
	subject := fmt.Sprintf("%T", zero)
	m.events.Subscribe(subject, func(ctx context.Context, e Event) error {
		typed, ok := e.(E)
		if !ok {
			return fmt.Errorf("%w: got %T, want %T", ErrTypeMismatch, e, zero)
		}
		return h(ctx, typed)
	})
}

// Publish broadcasts an event to all subscribers.
func Publish[E any](m *Mediator, ctx context.Context, event E) error {
	return m.events.Publish(ctx, fmt.Sprintf("%T", event), event)
}

func (m *Mediator) Events() *bus.Bus[Event] { return m.events }
