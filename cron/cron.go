package cron

import (
	"context"
	"fmt"
	"log"

	"github.com/nexssp/kernel/action"
	robfig "github.com/robfig/cron/v3"
)

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
	_, ok := b.(CronBinding)
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
			if c, ok := b.(CronBinding); ok {
				if ex, ok := act.(action.Executable); ok {
					sched := c.Schedule
					if sched == "" {
						sched = fmt.Sprintf("@every %s", c.Interval)
					}

					if _, err := t.cron.AddFunc(sched, func() {
						// Cron jobs take no payload.
						if _, err := ex.ExecuteDecoded(jobCtx, nil); err != nil {
							log.Printf("cron job failed: %v", err)
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
	return nil, nil
}
