package taskqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var ErrChannelFull = errors.New("channel full (backpressure)")

// Priority for task lanes.
type Priority int8

const (
	High   Priority = 2
	Normal Priority = 1
	Low    Priority = 0
)

// Config controls queue behavior.
type Config struct {
	Name        string
	Workers     int           // per priority lane (Low gets half)
	MaxAttempts int           // default 5
	BaseDelay   time.Duration // exponential retry base, default 500ms
	Logger      *slog.Logger
}

func (c *Config) applyDefaults() {
	if c.Workers <= 0 {
		c.Workers = 8
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = 500 * time.Millisecond
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Handler processes one task.
type Handler func(ctx context.Context, env Envelope) error

// Envelope is the data passed to handlers.
type Envelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	Attempt    int             `json:"attempt"`
	EnqueuedAt time.Time       `json:"enqueued_at"`
}

// Queue is the in‑process task queue.
type Queue struct {
	cfg       Config
	log       *slog.Logger
	handlers  sync.Map
	high      chan Envelope
	normal    chan Envelope
	low       chan Envelope
	dlq       chan Envelope
	startOnce sync.Once
	done      chan struct{}
	doneOnce  sync.Once
	wg        sync.WaitGroup
}

// New creates a ready Queue (no external deps).
func New(cfg Config) (*Queue, error) {
	cfg.applyDefaults()
	return &Queue{
		cfg:    cfg,
		log:    cfg.Logger.With("component", "taskqueue", "queue", cfg.Name),
		high:   make(chan Envelope, 1000),
		normal: make(chan Envelope, 1000),
		low:    make(chan Envelope, 1000),
		dlq:    make(chan Envelope, 1000),
		done:   make(chan struct{}),
	}, nil
}

// Register binds a handler to a task type.
func (q *Queue) Register(taskType string, h Handler) {
	q.handlers.Store(taskType, h)
}

// Enqueue adds a task at Normal priority.
func (q *Queue) Enqueue(ctx context.Context, taskType string, payload any) error {
	return q.EnqueueAt(ctx, taskType, payload, Normal)
}

// EnqueueAt adds a task with the specified priority.
func (q *Queue) EnqueueAt(_ context.Context, taskType string, payload any, p Priority) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	env := Envelope{
		ID:         fmt.Sprintf("%s_%d", taskType, time.Now().UnixNano()),
		Type:       taskType,
		Payload:    raw,
		EnqueuedAt: time.Now().UTC(),
	}
	select {
	case q.channel(p) <- env:
		return nil
	default:
		return ErrChannelFull
	}
}

// Start begins processing workers. Blocks until ctx is canceled.
func (q *Queue) Start(ctx context.Context) error {
	q.startOnce.Do(func() {
		lanes := []struct {
			ch      chan Envelope
			workers int
			label   string
		}{
			{q.high, q.cfg.Workers, "high"},
			{q.normal, q.cfg.Workers, "normal"},
			{q.low, max(1, q.cfg.Workers/2), "low"},
		}
		for _, lane := range lanes {
			for i := range lane.workers {
				q.wg.Add(1)
				go q.worker(ctx, lane.ch, lane.label, i)
			}
		}
	})
	q.log.Info("taskqueue_started",
		"high_workers", q.cfg.Workers,
		"normal_workers", q.cfg.Workers,
		"low_workers", max(1, q.cfg.Workers/2),
	)
	<-ctx.Done()
	q.closeDone()
	q.wg.Wait()
	return ctx.Err()
}

func (q *Queue) worker(ctx context.Context, ch <-chan Envelope, label string, id int) {
	defer q.wg.Done()
	name := fmt.Sprintf("%s_%s_w%d", q.cfg.Name, label, id)
	log := q.log.With("worker", name)

	for {
		select {
		case <-ctx.Done():
			// Phase 2: Context cancelled — drain remaining messages non-blockingly
			for {
				select {
				case env := <-ch:
					// Drain tasks with an independent timeout context
					drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					q.dispatch(drainCtx, env, log)
					cancel()
				default:
					return
				}
			}
		case env := <-ch:
			q.dispatch(ctx, env, log)
		}
	}
}

func (q *Queue) dispatch(ctx context.Context, env Envelope, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("handler_panic", "panic", r)
			env.Attempt++
			if env.Attempt < q.cfg.MaxAttempts {
				go q.retry(env)
			} else {
				q.deadLetter(env, log)
				log.Warn("task_dead_lettered", "id", env.ID, "type", env.Type)
			}
		}
	}()

	h, ok := q.handlers.Load(env.Type)
	if !ok {
		log.Warn("no_handler", "type", env.Type)
		q.deadLetter(env, log)
		return
	}

	start := time.Now()
	err := h.(Handler)(ctx, env)
	latency := time.Since(start)

	if err == nil {
		log.Debug("task_ok", "id", env.ID, "type", env.Type, "ms", latency.Milliseconds())
		return
	}

	env.Attempt++
	log.Warn("task_failed", "id", env.ID, "type", env.Type,
		"attempt", env.Attempt, "error", err, "ms", latency.Milliseconds())

	if env.Attempt >= q.cfg.MaxAttempts {
		q.deadLetter(env, log)
		log.Warn("task_dead_lettered", "id", env.ID, "type", env.Type)
		return
	}

	go q.retry(env)
}

func (q *Queue) retry(env Envelope) {
	delay := min(q.cfg.BaseDelay*(1<<min(env.Attempt-1, 10)), 5*time.Minute)

	select {
	case <-time.After(delay):
	case <-q.done:
		return
	}

	select {
	case <-q.done:
		return
	default:
	}

	select {
	case q.normal <- env:
	case <-q.done:
	}
}

func (q *Queue) channel(p Priority) chan<- Envelope {
	switch p {
	case High:
		return q.high
	case Low:
		return q.low
	default:
		return q.normal
	}
}

func (q *Queue) closeDone() {
	q.doneOnce.Do(func() { close(q.done) })
}

func (q *Queue) DeadLetter() <-chan Envelope {
	return q.dlq
}

func (q *Queue) deadLetter(env Envelope, log *slog.Logger) {
	select {
	case q.dlq <- env:
	default:
		log.Warn("dlq_full_dropping_task", "id", env.ID, "type", env.Type)
	}
}
