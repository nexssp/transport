// Package nexssflow exposes the CQRS mediator to Nexss Flow.
package nexssflow

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"

	coremediator "github.com/nexssp/transport/bus/mediator"
	"github.com/nexssp/transport/internal/flowutil"
)

func init() {
	compiler.Register("mediator", Bundle)
}

// CommandBinding marks an action as the handler for a named Flow command.
type CommandBinding struct {
	Name string
}

// EventBinding marks an action as a subscriber to a named Flow event.
type EventBinding struct {
	Name string
}

// Command binds an action to a named Flow command.
func Command(name string) CommandBinding { return CommandBinding{Name: name} }

// Event binds an action to a named Flow event.
func Event(name string) EventBinding { return EventBinding{Name: name} }

// CommandRequest is the input accepted by mediator.send.
type CommandRequest struct {
	Name    string `json:"name"`
	Payload any    `json:"payload"`
}

// EventRequest is the input accepted by mediator.publish.
type EventRequest struct {
	Name    string `json:"name"`
	Payload any    `json:"payload"`
}

type commandEnvelope struct {
	Name    string
	Payload any
}

type eventEnvelope struct {
	Name    string
	Payload any
}

type flowMediator struct {
	mediator *coremediator.Mediator
	mu       sync.RWMutex
	commands map[string]action.Executable
	events   map[string][]action.Executable
}

// Bundle returns the Flow bundle for mediator command and event routing.
func Bundle(_ map[string]string) compiler.Bundle {
	return compiler.Bundle{
		ID:        "mediator",
		Libraries: []action.Library{Library()},
		Modifiers: []compiler.Modifier{
			compiler.String("command", func(b *action.Builder[any, any], name string) *action.Builder[any, any] {
				return b.Route(Command(name))
			}),
			compiler.String("event", func(b *action.Builder[any, any], name string) *action.Builder[any, any] {
				return b.Route(Event(name))
			}),
		},
	}
}

// Library creates a Flow action library backed by a fresh mediator.
func Library() action.Library { return LibraryWithMediator(nil) }

// LibraryWithMediator uses the provided mediator, or creates one when nil.
func LibraryWithMediator(m *coremediator.Mediator) action.Library {
	if m == nil {
		m = coremediator.New()
	}
	state := &flowMediator{
		mediator: m,
		commands: make(map[string]action.Executable),
		events:   make(map[string][]action.Executable),
	}

	coremediator.Register(m, func(ctx context.Context, command commandEnvelope) (any, error) {
		state.mu.RLock()
		handler := state.commands[command.Name]
		state.mu.RUnlock()
		if handler == nil {
			return nil, fmt.Errorf("mediator.send: no command handler for %q", command.Name)
		}
		return handler.ExecuteDecoded(ctx, flowutil.Decode(command.Payload))
	})

	coremediator.Subscribe(m, func(ctx context.Context, event eventEnvelope) error {
		state.mu.RLock()
		handlers := append([]action.Executable(nil), state.events[event.Name]...)
		state.mu.RUnlock()
		if len(handlers) == 0 {
			return fmt.Errorf("mediator.publish: no event subscribers for %q", event.Name)
		}
		var firstErr error
		for _, handler := range handlers {
			if _, err := handler.ExecuteDecoded(ctx, flowutil.Decode(event.Payload)); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	})

	send := action.New("mediator.send", func(ctx context.Context, in any) (any, error) {
		var req CommandRequest
		if err := flowutil.DecodeInto(in, &req); err != nil {
			return nil, xerr.BadRequest("mediator.send: invalid request: " + err.Error())
		}
		if req.Name == "" {
			return nil, xerr.BadRequest("mediator.send: 'name' is required")
		}
		result, err := coremediator.Send[commandEnvelope, any](ctx, m, commandEnvelope(req))
		if err != nil {
			return nil, xerr.Unavailable("mediator.send: "+err.Error(), err)
		}
		return result, nil
	}).
		Description("Send a named command to its registered handler and return the response").
		Tag("transport", "mediator").
		Build()

	publish := action.New("mediator.publish", func(ctx context.Context, in any) (any, error) {
		var req EventRequest
		if err := flowutil.DecodeInto(in, &req); err != nil {
			return nil, xerr.BadRequest("mediator.publish: invalid request: " + err.Error())
		}
		if req.Name == "" {
			return nil, xerr.BadRequest("mediator.publish: 'name' is required")
		}
		if err := coremediator.Publish(ctx, m, eventEnvelope(req)); err != nil {
			return nil, xerr.Unavailable("mediator.publish: "+err.Error(), err)
		}
		return in, nil
	}).
		Description("Publish a named event to all registered subscribers").
		Tag("transport", "mediator").
		Build()

	listen := action.New("mediator.listen", func(ctx context.Context, workload any) (any, error) {
		actions, ok := flowutil.ResolveActions(ctx, workload)
		if !ok {
			return nil, errors.New("mediator.listen: no explicit workload and no usable action resolver in context")
		}
		registered := 0
		for _, act := range actions {
			if act == nil {
				continue
			}
			count, err := registerBoundAction(state, act)
			if err != nil {
				return nil, err
			}
			registered += count
		}
		if registered == 0 {
			return nil, errors.New("mediator.listen: auto-discovery found 0 command- or event-bound actions")
		}

		<-ctx.Done()
		return nil, ctx.Err()
	}).
		Description("Register command and event actions, then wait for the context to be canceled").
		Tag("transport", "mediator").
		Build()

	return action.Library{
		Name:    "transport.mediator",
		Actions: []action.AnyAction{listen, send, publish},
	}
}

func registerBoundAction(state *flowMediator, act action.AnyAction) (int, error) {
	ex, executable := act.(action.Executable)
	if !executable {
		return 0, nil
	}

	registered := 0
	for _, binding := range act.GetBindings() {
		switch route := binding.(type) {
		case CommandBinding:
			if route.Name == "" {
				return registered, errors.New("mediator.listen: command binding name must not be empty")
			}
			state.mu.Lock()
			state.commands[route.Name] = ex
			state.mu.Unlock()
			registered++
		case EventBinding:
			if route.Name == "" {
				return registered, errors.New("mediator.listen: event binding name must not be empty")
			}
			state.mu.Lock()
			state.events[route.Name] = append(state.events[route.Name], ex)
			state.mu.Unlock()
			registered++
		}
	}
	return registered, nil
}
