// Package bus provides a lightweight, type-safe in-process message bus.
// Supports 1:N pub/sub and 1:1 typed command routing via TypedBus.
package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrNoHandler      = errors.New("bus: no handler for key")
	ErrPanicRecovered = errors.New("panic recovered")
)

// Handler is the subscriber function signature.
type Handler[T any] func(ctx context.Context, msg T) error

// Middleware wraps a Handler for cross-cutting concerns.
type Middleware[T any] func(Handler[T]) Handler[T]

// subscription is one handler registration. active lets a publish snapshot
// skip a registration that was removed after the snapshot was loaded.
type subscription[T any] struct {
	handler Handler[T]
	active  atomic.Bool
}

// handlerSet is an immutable snapshot of handlers for one subject.
type handlerSet[T any] struct {
	handlers []*subscription[T]
}

// Bus is a generic pub/sub message bus. Safe for concurrent use.
type Bus[T any] struct {
	mu       sync.RWMutex
	handlers map[string]*atomic.Pointer[handlerSet[T]]
	mws      []Middleware[T]
}

// New creates a Bus with optional middleware applied to all subscribers.
func New[T any](mws ...Middleware[T]) *Bus[T] {
	return &Bus[T]{
		handlers: make(map[string]*atomic.Pointer[handlerSet[T]]),
		mws:      mws,
	}
}

// Subscribe registers a handler. Middleware is applied at subscribe time —
// zero alloc on Publish.
func (b *Bus[T]) Subscribe(subject string, h Handler[T]) {
	b.SubscribeWithCancel(subject, h)
}

// SubscribeWithCancel registers a handler and returns a function that removes it.
func (b *Bus[T]) SubscribeWithCancel(subject string, h Handler[T]) func() {
	if h == nil {
		return func() {}
	}

	b.mu.Lock()
	p := b.handlers[subject]
	if p == nil {
		p = &atomic.Pointer[handlerSet[T]]{}
		b.handlers[subject] = p
	}

	wrapped := h
	for _, v := range slices.Backward(b.mws) {
		wrapped = v(wrapped)
	}

	var old []*subscription[T]
	if set := p.Load(); set != nil {
		old = set.handlers
	}

	registered := &subscription[T]{handler: wrapped}
	registered.active.Store(true)
	handlers := slices.Clone(old)
	handlers = append(handlers, registered)
	p.Store(&handlerSet[T]{handlers: handlers})
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			registered.active.Store(false)
			b.mu.Lock()
			defer b.mu.Unlock()

			if b.handlers[subject] != p {
				return
			}
			set := p.Load()
			if set == nil {
				return
			}
			remaining := make([]*subscription[T], 0, len(set.handlers)-1)
			for _, sub := range set.handlers {
				if sub != registered {
					remaining = append(remaining, sub)
				}
			}
			if len(remaining) == 0 {
				p.Store(nil)
				delete(b.handlers, subject)
				return
			}
			p.Store(&handlerSet[T]{handlers: remaining})
		})
	}
}

// Publish delivers msg to all handlers for subject.
// Returns first error; all handlers still run.
func (b *Bus[T]) Publish(ctx context.Context, subject string, msg T) error {
	b.mu.RLock()
	p := b.handlers[subject]
	b.mu.RUnlock()

	if p == nil {
		return nil
	}

	set := p.Load()
	if set == nil {
		return nil
	}

	var firstErr error
	for _, sub := range set.handlers {
		if sub.active.Load() {
			if err := sub.handler(ctx, msg); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ── TypedBus — 1:1 command routing ───────────────────────────────────────────

type TypedHandler[Req, Res any] func(ctx context.Context, req Req) (Res, error)

type TypedBus struct {
	mu       sync.RWMutex
	handlers map[string]any
}

func NewTyped() *TypedBus { return &TypedBus{handlers: make(map[string]any)} }

// Register adds a command handler. Last registration wins.
func Register[Req, Res any](b *TypedBus, h TypedHandler[Req, Res]) {
	var zero Req
	key := fmt.Sprintf("%T", zero)
	b.mu.Lock()
	b.handlers[key] = h
	b.mu.Unlock()
}

// Send dispatches a command to its registered handler.
func Send[Req, Res any](ctx context.Context, b *TypedBus, req Req) (Res, error) {
	var zero Req
	key := fmt.Sprintf("%T", zero)
	b.mu.RLock()
	h, ok := b.handlers[key]
	b.mu.RUnlock()
	if !ok {
		var z Res
		return z, fmt.Errorf("%w: %s", ErrNoHandler, key)
	}
	typed, ok := h.(TypedHandler[Req, Res])
	if !ok {
		var z Res
		return z, fmt.Errorf("%w: %s (stored handler has type %T)", ErrNoHandler, key, h)
	}
	return typed(ctx, req)
}

// ── Standard middlewares ──────────────────────────────────────────────────────

func Logging[T any](log *slog.Logger) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, msg T) error {
			err := next(ctx, msg)
			if err != nil {
				log.ErrorContext(ctx, "bus.handler_failed", "type", fmt.Sprintf("%T", msg), "error", err)
			}
			return err
		}
	}
}

func Recover[T any](log *slog.Logger) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, msg T) (err error) {
			defer func() {
				if r := recover(); r != nil {
					log.ErrorContext(ctx, "bus.handler_panic", "type", fmt.Sprintf("%T", msg), "panic", r)
					err = fmt.Errorf("%w: %v", ErrPanicRecovered, r)
				}
			}()
			return next(ctx, msg)
		}
	}
}

func Timeout[T any](d time.Duration) Middleware[T] {
	return func(next Handler[T]) Handler[T] {
		return func(ctx context.Context, msg T) error {
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next(ctx, msg)
		}
	}
}
