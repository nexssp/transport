package tworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"
)

type Transport struct {
	workers []*workerInstance
	wg      sync.WaitGroup
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
}

func New() *Transport {
	return &Transport{}
}

func (t *Transport) Mount(actions []action.AnyAction) {
	for _, act := range actions {
		for _, b := range act.GetBindings() {
			if wBind, ok := b.(Binding); ok {
				if ex, ok := act.(action.Executable); ok {
					t.workers = append(t.workers, &workerInstance{
						act:      ex,
						meta:     act.Describe(),
						interval: wBind.Interval,
						restart:  wBind.RestartOnError,
					})
				}
			}
		}
	}
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	for _, w := range t.workers {
		t.wg.Add(1)
		go t.runWorker(ctx, w)
	}

	// Block until context is canceled and all workers finish gracefully
	<-ctx.Done()
	t.wg.Wait()
	return nil, nil
}

func (t *Transport) runWorker(ctx context.Context, w *workerInstance) {
	defer t.wg.Done()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Helper for safe isolated tick execution
	runIteration := func() bool {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("worker_panic_recovered",
					"action", w.meta.Name,
					"panic", fmt.Sprintf("%v", r),
				)
			}
		}()

		err := t.execute(ctx, w)
		if err != nil && !w.restart {
			return false // Stop worker if restart is disabled
		}
		return true
	}

	// Initial immediate run
	if !runIteration() {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !runIteration() {
				return
			}
		}
	}
}

func (t *Transport) execute(ctx context.Context, w *workerInstance) error {
	runCtx, scope, release := xctx.NewScope(ctx)
	defer release()

	scope.RequestID = "wrk_" + uuid.NewString()
	scope.Endpoint = w.meta.Name

	_, err := w.act.ExecuteDecoded(runCtx, nil)
	if err != nil {
		if errors.Is(err, action.ErrLocked) {
			slog.DebugContext(runCtx, "worker_skipped_not_leader", "action", w.meta.Name)
			return nil
		}
		slog.ErrorContext(runCtx, "worker_failed", "action", w.meta.Name, "error", err)
	}
	return err
}
