// Package nexssflow exposes the in-process task queue to Nexss Flow.
package nexssflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"

	corequeue "github.com/nexssp/transport/bus/taskqueue"
	"github.com/nexssp/transport/internal/flowutil"
)

func init() {
	compiler.Register("taskqueue", Bundle)
}

// TaskBinding marks an action as a handler for a named queue task type.
type TaskBinding struct {
	Type string
}

// Task binds an action to a task type.
func Task(taskType string) TaskBinding { return TaskBinding{Type: taskType} }

// EnqueueRequest is the input accepted by taskqueue.enqueue.
type EnqueueRequest struct {
	Type     string `json:"type"`
	Payload  any    `json:"payload"`
	Priority string `json:"priority,omitempty"`
}

// Bundle returns the Flow bundle for task queue producers and workers.
func Bundle(_ map[string]string) compiler.Bundle {
	return compiler.Bundle{
		ID:        "taskqueue",
		Libraries: []action.Library{Library()},
		Modifiers: []compiler.Modifier{
			compiler.String("task", func(b *action.Builder[any, any], taskType string) *action.Builder[any, any] {
				return b.Route(Task(taskType))
			}),
		},
	}
}

// Library creates a Flow action library backed by a fresh in-memory queue.
func Library() action.Library {
	return LibraryWithConfig(corequeue.Config{Name: "flow"})
}

// LibraryWithConfig creates a Flow library with the supplied queue settings.
func LibraryWithConfig(cfg corequeue.Config) action.Library {
	q, err := corequeue.New(cfg)
	if err != nil {
		return unavailableLibrary(fmt.Errorf("taskqueue: create queue: %w", err))
	}
	return LibraryWithQueue(q)
}

// LibraryWithQueue uses the provided queue so producers and listeners share it.
func LibraryWithQueue(q *corequeue.Queue) action.Library {
	if q == nil {
		return unavailableLibrary(errors.New("taskqueue: queue must not be nil"))
	}

	enqueue := action.New("taskqueue.enqueue", func(ctx context.Context, in any) (any, error) {
		var req EnqueueRequest
		if err := flowutil.DecodeInto(in, &req); err != nil {
			return nil, xerr.BadRequest("taskqueue.enqueue: invalid request: " + err.Error())
		}
		if req.Type == "" {
			return nil, xerr.BadRequest("taskqueue.enqueue: 'type' is required")
		}
		priority, err := parsePriority(req.Priority)
		if err != nil {
			return nil, xerr.BadRequest("taskqueue.enqueue: " + err.Error())
		}
		if err := q.EnqueueAt(ctx, req.Type, req.Payload, priority); err != nil {
			return nil, xerr.Unavailable("taskqueue.enqueue: "+err.Error(), err)
		}
		return in, nil
	}).
		Description("Enqueue a payload for an in-process task handler").
		Tag("transport", "taskqueue").
		Build()

	listen := action.New("taskqueue.listen", func(ctx context.Context, workload any) (any, error) {
		actions, ok := flowutil.ResolveActions(ctx, workload)
		if !ok {
			return nil, errors.New("taskqueue.listen: no explicit workload and no usable action resolver in context")
		}
		registered := 0
		for _, act := range actions {
			if act == nil {
				continue
			}
			ex, executable := act.(action.Executable)
			if !executable {
				continue
			}
			for _, binding := range act.GetBindings() {
				task, isTask := binding.(TaskBinding)
				if !isTask {
					continue
				}
				if task.Type == "" {
					return nil, errors.New("taskqueue.listen: task binding type must not be empty")
				}
				q.Register(task.Type, func(handlerCtx context.Context, env corequeue.Envelope) error {
					_, err := ex.ExecuteDecoded(handlerCtx, flowutil.Decode(env.Payload))
					return err
				})
				registered++
			}
		}
		if registered == 0 {
			return nil, errors.New("taskqueue.listen: auto-discovery found 0 task-bound actions")
		}
		return nil, q.Start(ctx)
	}).
		Description("Register task-bound actions and run queue workers until the context is canceled").
		Tag("transport", "taskqueue").
		Build()

	return action.Library{
		Name:    "transport.taskqueue",
		Actions: []action.AnyAction{listen, enqueue},
	}
}

func parsePriority(value string) (corequeue.Priority, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "normal":
		return corequeue.Normal, nil
	case "high":
		return corequeue.High, nil
	case "low":
		return corequeue.Low, nil
	default:
		return corequeue.Normal, fmt.Errorf("unknown priority %q (want high, normal, or low)", value)
	}
}

func unavailableLibrary(err error) action.Library {
	fail := func(name string) action.AnyAction {
		return action.New(name, func(context.Context, any) (any, error) {
			return nil, err
		}).Tag("transport", "taskqueue").Build()
	}
	return action.Library{
		Name:    "transport.taskqueue",
		Actions: []action.AnyAction{fail("taskqueue.listen"), fail("taskqueue.enqueue")},
	}
}
