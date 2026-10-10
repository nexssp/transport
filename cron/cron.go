package cron

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nexssp/kernel/action"
	robfig "github.com/robfig/cron/v3"

	"github.com/nexssp/transport"
)

var _ transport.Transport = (*Transport)(nil)

type Transport struct {
	actions []action.AnyAction
	cron    *robfig.Cron
}

func New() *Transport {
	return &Transport{
		cron: robfig.New(),
	}
}

func (t *Transport) CanHandle(b action.Binding) bool {
	_, ok := b.(Binding)
	return ok
}

func (t *Transport) String() string {
	return "cron"
}

func (t *Transport) Mount(actions []action.AnyAction) { t.actions = actions }

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	// Derive a cancellable context for all cron jobs.
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, act := range t.actions {
		for _, b := range act.GetBindings() {
			if c, ok := b.(Binding); ok {
				if ex, ok := act.(action.Executable); ok {
					sched := c.Schedule
					if sched == "" {
						sched = fmt.Sprintf("@every %s", c.Interval)
					}

					if _, err := t.cron.AddFunc(sched, func() {
						// Cron jobs take no payload.
						if _, err := ex.ExecuteDecoded(jobCtx, nil); err != nil {
							slog.Error("cron job failed", "error", err)
						}
					}); err != nil {
						return nil, fmt.Errorf("cron AddFunc failed: %w", err)
					}
				}
			}
		}
	}

	t.cron.Start()
	<-ctx.Done()
	<-t.cron.Stop().Done()
	return nil, ctx.Err()
}
