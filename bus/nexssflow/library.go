// Package nexssflow exposes the generic in-process bus to Nexss Flow.
package nexssflow

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"

	transportbus "github.com/nexssp/transport/bus"
	"github.com/nexssp/transport/internal/flowutil"
)

func init() {
	compiler.Register("bus", Bundle)
}

// SubjectBinding routes an action to a generic bus subject.
type SubjectBinding struct {
	Subject string
}

// CommandBinding routes an action to a named generic-bus command.
type CommandBinding struct {
	Name string
}

// Subject binds an action to a generic bus subject.
func Subject(subject string) SubjectBinding { return SubjectBinding{Subject: subject} }

// Command binds an action to a named generic-bus command.
func Command(name string) CommandBinding { return CommandBinding{Name: name} }

// PublishRequest is the input accepted by bus.publish.
type PublishRequest struct {
	Subject string `json:"subject"`
	Payload any    `json:"payload"`
}

// CommandRequest is the input accepted by bus.send.
type CommandRequest struct {
	Name    string `json:"name"`
	Payload any    `json:"payload"`
}

type commandEnvelope struct {
	Name    string
	Payload any
}

type flowBus struct {
	events   *transportbus.Bus[any]
	commands *transportbus.TypedBus
	mu       sync.RWMutex
	handlers map[string]action.Executable
}

// Bundle returns the Flow bundle for generic bus pub/sub and command routing.
func Bundle(_ map[string]string) compiler.Bundle {
	return compiler.Bundle{
		ID:        "bus",
		Libraries: []action.Library{Library()},
		Modifiers: []compiler.Modifier{
			compiler.String("subject", func(b *action.Builder[any, any], subject string) *action.Builder[any, any] {
				return b.Route(Subject(subject))
			}),
			compiler.String("command", func(b *action.Builder[any, any], name string) *action.Builder[any, any] {
				return b.Route(Command(name))
			}),
		},
	}
}

// Library creates a Flow action library backed by fresh generic buses.
func Library() action.Library { return LibraryWithBus(transportbus.New[any]()) }

// LibraryWithBus uses the provided pub/sub bus so listeners and publishers share state.
func LibraryWithBus(eventBus *transportbus.Bus[any]) action.Library {
	if eventBus == nil {
		eventBus = transportbus.New[any]()
	}
	state := &flowBus{
		events:   eventBus,
		commands: transportbus.NewTyped(),
		handlers: make(map[string]action.Executable),
	}
	transportbus.Register(state.commands, func(ctx context.Context, command commandEnvelope) (any, error) {
		state.mu.RLock()
		handler := state.handlers[command.Name]
		state.mu.RUnlock()
		if handler == nil {
			return nil, fmt.Errorf("bus.send: no command handler for %q", command.Name)
		}
		return handler.ExecuteDecoded(ctx, flowutil.Decode(command.Payload))
	})

	publish := action.New("bus.publish", func(ctx context.Context, in any) (any, error) {
		var req PublishRequest
		if err := flowutil.DecodeInto(in, &req); err != nil {
			return nil, xerr.BadRequest("bus.publish: invalid request: " + err.Error())
		}
		if req.Subject == "" {
			return nil, xerr.BadRequest("bus.publish: 'subject' is required")
		}
		if err := state.events.Publish(ctx, req.Subject, req.Payload); err != nil {
			return nil, xerr.Unavailable("bus.publish: "+err.Error(), err)
		}
		return in, nil
	}).
		Description("Publish a payload to a generic in-process bus subject").
		Tag("transport", "bus").
		Build()

	send := action.New("bus.send", func(ctx context.Context, in any) (any, error) {
		var req CommandRequest
		if err := flowutil.DecodeInto(in, &req); err != nil {
			return nil, xerr.BadRequest("bus.send: invalid request: " + err.Error())
		}
		if req.Name == "" {
			return nil, xerr.BadRequest("bus.send: 'name' is required")
		}
		result, err := transportbus.Send[commandEnvelope, any](ctx, state.commands, commandEnvelope(req))
		if err != nil {
			return nil, xerr.Unavailable("bus.send: "+err.Error(), err)
		}
		return result, nil
	}).
		Description("Send a named command to its registered handler and return the response").
		Tag("transport", "bus").
		Build()

	listen := action.New("bus.listen", func(ctx context.Context, workload any) (any, error) {
		actions, ok := flowutil.ResolveActions(ctx, workload)
		if !ok {
			return nil, errors.New("bus.listen: no explicit workload and no usable action resolver in context")
		}

		mounted := 0
		for _, act := range actions {
			if act == nil {
				continue
			}
			ex, executable := act.(action.Executable)
			if !executable {
				continue
			}
			for _, binding := range act.GetBindings() {
				switch route := binding.(type) {
				case SubjectBinding:
					if route.Subject == "" {
						return nil, errors.New("bus.listen: subject binding must not be empty")
					}
					state.events.Subscribe(route.Subject, func(handlerCtx context.Context, payload any) error {
						_, err := ex.ExecuteDecoded(handlerCtx, flowutil.Decode(payload))
						return err
					})
					mounted++
				case CommandBinding:
					if route.Name == "" {
						return nil, errors.New("bus.listen: command binding name must not be empty")
					}
					state.mu.Lock()
					state.handlers[route.Name] = ex
					state.mu.Unlock()
					mounted++
				}
			}
		}
		if mounted == 0 {
			return nil, errors.New("bus.listen: auto-discovery found 0 subject- or command-bound actions")
		}

		<-ctx.Done()
		return nil, ctx.Err()
	}).
		Description("Register subject subscribers and command handlers until the context is canceled").
		Tag("transport", "bus").
		Build()

	return action.Library{
		Name:    "transport.bus",
		Actions: []action.AnyAction{listen, publish, send},
	}
}
