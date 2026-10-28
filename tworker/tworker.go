package tworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"

	"github.com/nexssp/transport"
)

var _ transport.Transport = (*Transport)(nil)

const workerErrQuiet = 5 * time.Second

type Transport struct {
	workers []*workerInstance
	wg      sync.WaitGroup
	options Options
}

type ImmediateCycle interface {
	ContinueWorkerCycle() bool
}

func (t *Transport) AsAction() action.AnyAction {
	return action.New("transport.worker", func(ctx context.Context, _ any) (any, error) {
		return t.Do(ctx, nil)
	}).
		Tag("infra", "transport", "worker").
		Description("Background Worker Runner").
		Build()
}

func (t *Transport) CanHandle(b action.Binding) bool {
	_, ok := b.(Binding)
	return ok
}

func (t *Transport) String() string {
	return "worker"
}

type workerInstance struct {
	act      action.Executable
	meta     *action.Meta
	interval time.Duration
	restart  bool

	errMu      sync.Mutex
	lastErrMsg string
	lastErrAt  time.Time
	suppressed uint64
}

func New(options ...Options) *Transport {
	cfg := Options{Backoff: action.ExponentialBackoff(time.Second, 30*time.Second)}
	for _, option := range options {
		if option.PollIntervalOverride != 0 {
			cfg.PollIntervalOverride = option.PollIntervalOverride
		}
		if option.Backoff != nil {
			cfg.Backoff = option.Backoff
		}
		if option.OnStatus != nil {
			cfg.OnStatus = option.OnStatus
		}
	}
	return &Transport{options: cfg}
}

func (t *Transport) Mount(actions []action.AnyAction) {
	for _, act := range actions {
		if act == nil {
			continue
		}
		for _, b := range act.GetBindings() {
			if wBind, ok := b.(Binding); ok {
				if ex, ok := act.(action.Executable); ok {
					interval := wBind.Interval
					if t.options.PollIntervalOverride > 0 {
						interval = t.options.PollIntervalOverride
					}
					t.workers = append(t.workers, &workerInstance{
						act: ex, meta: act.Describe(), interval: interval, restart: wBind.RestartOnError,
					})
				}
			}
		}
	}
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	if ctx == nil {
		return nil, errors.New("worker transport context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(t.workers) == 0 {
		return nil, errors.New("worker transport has no mounted workers")
	}
	if t.options.PollIntervalOverride < 0 {
		return nil, errors.New("worker transport poll interval cannot be negative")
	}
	for _, worker := range t.workers {
		if worker.interval <= 0 {
			return nil, fmt.Errorf("worker %q has invalid interval %s", workerName(worker.meta), worker.interval)
		}
	}

	for _, worker := range t.workers {
		t.wg.Add(1)
		go t.runWorker(ctx, worker)
	}
	if err := ctx.Err(); err != nil {
		t.wg.Wait()
		return nil, err
	}
	contracts.SignalReady(ctx)
	<-ctx.Done()
	t.wg.Wait()
	return nil, ctx.Err()
}

func (t *Transport) runWorker(ctx context.Context, w *workerInstance) {
	defer t.wg.Done()
	name := workerName(w.meta)
	failures := 0
	var lastCycle time.Time
	t.publish(WorkerStatus{Action: name, State: StateStarting, Interval: w.interval})

	for {
		if ctx.Err() != nil {
			t.publish(WorkerStatus{Action: name, State: StateStopped, Interval: w.interval, LastCycleAt: lastCycle, ConsecutiveErrors: failures})
			return
		}
		t.publish(WorkerStatus{Action: name, State: StateRunning, Interval: w.interval, LastCycleAt: lastCycle, ConsecutiveErrors: failures})
		result, err := t.safeExecute(ctx, w)
		lastCycle = time.Now().UTC()
		if ctx.Err() != nil {
			t.publish(WorkerStatus{Action: name, State: StateStopped, Interval: w.interval, LastCycleAt: lastCycle, ConsecutiveErrors: failures})
			return
		}
		if err != nil {
			failures++
			t.publish(WorkerStatus{Action: name, State: StateBackoff, Interval: w.interval, LastCycleAt: lastCycle, ConsecutiveErrors: failures})
			if !w.restart {
				t.publish(WorkerStatus{Action: name, State: StateStopped, Interval: w.interval, LastCycleAt: lastCycle, ConsecutiveErrors: failures})
				return
			}
			delay := t.options.Backoff(failures)
			if delay < 0 {
				slog.ErrorContext(ctx, "worker backoff policy returned a negative duration; stopping worker", "action", name, "backoff", delay)
				t.publish(WorkerStatus{Action: name, State: StateStopped, Interval: w.interval, LastCycleAt: lastCycle, ConsecutiveErrors: failures})
				return
			}
			slog.WarnContext(ctx, "worker cycle failed; backing off", "action", name, "error_type", fmt.Sprintf("%T", err), "backoff", delay)
			if !wait(ctx, delay) {
				t.publish(WorkerStatus{Action: name, State: StateStopped, Interval: w.interval, LastCycleAt: lastCycle, ConsecutiveErrors: failures})
				return
			}
			continue
		}

		failures = 0
		t.publish(WorkerStatus{Action: name, State: StateIdle, Interval: w.interval, LastCycleAt: lastCycle})
		if signal, ok := result.(ImmediateCycle); ok && signal.ContinueWorkerCycle() {
			continue
		}
		if !wait(ctx, w.interval) {
			t.publish(WorkerStatus{Action: name, State: StateStopped, Interval: w.interval, LastCycleAt: lastCycle})
			return
		}
	}
}

func (t *Transport) safeExecute(ctx context.Context, w *workerInstance) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("worker action panic: %v", recovered)
		}
	}()
	return t.execute(ctx, w)
}

func (t *Transport) execute(ctx context.Context, w *workerInstance) (any, error) {
	runCtx, scope, release := xctx.NewScope(ctx)
	defer release()

	scope.RequestID = "wrk_" + uuid.NewString()
	scope.Endpoint = workerName(w.meta)

	value, err := w.act.ExecuteDecoded(runCtx, nil)
	if err == nil {
		return value, nil
	}
	if errors.Is(err, action.ErrLocked) {
		slog.DebugContext(runCtx, "worker_skipped_not_leader", "action", workerName(w.meta))
		return value, nil
	}
	t.logWorkerError(runCtx, w, err)
	return nil, err
}

func (t *Transport) publish(status WorkerStatus) {
	if t.options.OnStatus == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("worker_status_observer_panicked", "panic", fmt.Sprintf("%v", recovered))
		}
	}()
	t.options.OnStatus(status)
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func workerName(meta *action.Meta) string {
	if meta == nil || meta.Name == "" {
		return "unnamed"
	}
	return meta.Name
}

func (t *Transport) logWorkerError(ctx context.Context, w *workerInstance, err error) {
	msg := fmt.Sprintf("%T", err)
	now := time.Now()

	w.errMu.Lock()
	quiet := msg == w.lastErrMsg && now.Sub(w.lastErrAt) < workerErrQuiet
	if quiet {
		w.suppressed++
		w.errMu.Unlock()
		return
	}
	suppressed := w.suppressed
	w.suppressed = 0
	w.lastErrMsg = msg
	w.lastErrAt = now
	w.errMu.Unlock()

	if suppressed > 0 {
		slog.WarnContext(ctx, "worker_failed_summary", "action", workerName(w.meta), "suppressed", suppressed, "window", workerErrQuiet)
	}
	slog.ErrorContext(ctx, "worker_failed", "action", workerName(w.meta), "error_type", msg)
}
