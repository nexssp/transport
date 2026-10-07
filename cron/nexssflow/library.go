package nexssflow

import (
	"context"
	"errors"

	"github.com/nexssp/flow/contracts"
	"github.com/nexssp/flow/core"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/cron"
)

type Config struct{}

func init() {
	core.Register("cron", Bundle)
}

func Bundle(_ map[string]string) core.Bundle {
	return core.Bundle{
		ID:        "cron",
		Libraries: []action.Library{Library()},
		Modifiers: []core.Modifier{
			core.String("cron", func(b *action.Builder[any, any], schedule string) *action.Builder[any, any] {
				return b.Route(cron.Cron(schedule))
			}),
		},
	}
}

func Library() action.Library {
	tr := cron.New()

	listenTrigger := action.New("cron.listen", func(ctx context.Context, workload any) (any, error) {
		actionsToMount, fallbackName, ok := resolveCronWorkload(ctx, workload)
		if !ok {
			return nil, errors.New("cron.listen: no explicit workload and no usable action resolver in context")
		}
		if len(actionsToMount) == 0 {
			return nil, errors.New("cron.listen: auto-discovery found 0 schedule-bound actions")
		}

		for _, act := range actionsToMount {
			hasBinding := false
			for _, b := range act.GetBindings() {
				if _, ok := b.(cron.Binding); ok {
					hasBinding = true
					break
				}
			}
			if !hasBinding && fallbackName != "" && fallbackName != "inline" {
				act = action.Dynamic(act).Route(cron.Cron(fallbackName)).Build()
			}
			tr.Mount([]action.AnyAction{act})
		}
		return tr.Do(ctx, nil)
	}).
		Tag("transport", "cron").
		Build()

	return action.Library{
		Name: "transport.cron",
		Actions: []action.AnyAction{
			listenTrigger,
		},
	}
}

func resolveCronWorkload(ctx context.Context, workload any) (actions []action.AnyAction, fallbackName string, ok bool) {
	if lib, isLib := workload.(action.Library); isLib {
		return lib.Actions, lib.Name, true
	}
	if acts, isActs := workload.([]action.AnyAction); isActs {
		return acts, "", true
	}

	resolver := contracts.ActionResolverFromContext(ctx)
	if resolver == nil {
		return nil, "", false
	}
	lister, isLister := resolver.(interface{ Actions() []action.AnyAction })
	if !isLister {
		return nil, "", false
	}

	var discovered []action.AnyAction
	for _, act := range lister.Actions() {
		if act == nil {
			continue
		}
		for _, b := range act.GetBindings() {
			if _, isSchedule := b.(cron.Binding); isSchedule {
				discovered = append(discovered, act)
				break
			}
		}
	}
	return discovered, "", true
}
