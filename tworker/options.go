package tworker

import (
	"context"
	"time"

	"github.com/nexssp/kernel/xctx"
)

const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateIdle     = "idle"
	StateBackoff  = "backoff"
	StateStopped  = "stopped"
)

// WorkerStatus is a payload-free lifecycle snapshot; it intentionally contains
// no action results, claim data, provider errors, or credentials.
type WorkerStatus struct {
	Action            string
	State             string
	Interval          time.Duration
	LastCycleAt       time.Time
	ConsecutiveErrors int
}

// Options configure reusable worker scheduling semantics. A zero interval
// override keeps each NFlow :worker binding's own schedule. A nil Backoff uses
// the default Kernel exponential policy (one second base, thirty seconds max).
type Options struct {
	PollIntervalOverride time.Duration
	// Backoff receives the 1-based consecutive failure count. It must return a
	// non-negative delay; a negative result stops that worker. Nil selects the
	// default action.ExponentialBackoff(1s, 30s) policy.
	Backoff  func(attempt int) time.Duration
	OnStatus func(WorkerStatus)
}

var optionsContextKey = xctx.NewKey[Options]("transport.tworker.options")

// WithOptions supplies options to the tworker.listen NFlow action invocation.
func WithOptions(ctx context.Context, options Options) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return optionsContextKey.With(ctx, options)
}

// OptionsFromContext returns settings attached by WithOptions, or zero values.
func OptionsFromContext(ctx context.Context) Options {
	if ctx == nil {
		return Options{}
	}
	options, _ := optionsContextKey.From(ctx)
	return options
}
