package nexssflow

import (
	"context"
	"errors"
	"time"

	"github.com/nexssp/flow/contracts"
	"github.com/nexssp/flow/core"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/tworker"
)

type Config struct{}

func init() {
	core.Register("tworker", Bundle)
}

func Bundle(_ map[string]string) core.Bundle {
	return core.Bundle{
		ID:        "tworker",
		Libraries: []action.Library{Library()},
		Modifiers: []core.Modifier{
			core.Duration("worker", func(b *action.Builder[any, any], interval time.Duration) *action.Builder[any, any] {
				return b.Route(tworker.Every(interval))
			}),
		},
	}
}

func Library() action.Library {
	tr := tworker.New()

	listenTrigger := action.New("tworker.listen", func(ctx context.Context, workload any) (any, error) {
		actionsToMount, fallbackName, ok := resolveWorkerWorkload(ctx, workload)
		if !ok {
			return nil, errors.New("tworker.listen: no explicit workload and no usable action resolver in context")
		}
		if len(actionsToMount) == 0 {
			return nil, errors.New("tworker.listen: auto-discovery found 0 worker-bound actions")
		}

		for _, act := range actionsToMount {
			hasBinding := false
			for _, b := range act.GetBindings() {
				if _, ok := b.(tworker.Binding); ok {
					hasBinding = true
					break
				}
			}
			if !hasBinding {
				d, parseErr := time.ParseDuration(fallbackName)
				if parseErr != nil || d <= 0 {
					d = time.Minute
				}
				act = action.Dynamic(act).Route(tworker.Every(d)).Build()
			}
			tr.Mount([]action.AnyAction{act})
		}
		return tr.Do(ctx, nil)
	}).
		Tag("transport", "tworker").
		Build()

	return action.Library{
		Name: "transport.tworker",
		Actions: []action.AnyAction{
			listenTrigger,
		},
	}
}

func resolveWorkerWorkload(ctx context.Context, workload any) (actions []action.AnyAction, fallbackName string, ok bool) {
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
			if _, isWorker := b.(tworker.Binding); isWorker {
				discovered = append(discovered, act)
				break
			}
		}
	}
	return discovered, "", true
}
