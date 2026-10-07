package nexssflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/nexssp/flow/contracts"
	"github.com/nexssp/flow/core"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport/bus"
	"github.com/nexssp/transport/tbus"
)

type Config struct{}

func init() {
	core.Register("tbus", Bundle)
}

func Bundle(_ map[string]string) core.Bundle {
	return core.Bundle{
		ID:        "tbus",
		Libraries: []action.Library{Library(bus.New[any]())},
		Modifiers: []core.Modifier{
			core.String("topic", func(b *action.Builder[any, any], topic string) *action.Builder[any, any] {
				return b.Route(tbus.Topic(topic))
			}),
		},
	}
}

// Library returns the tbus action surface: tbus.listen (subscribe and
// block until ctx is done) and tbus.publish (emit an in-process event).
// The two share one bus instance so a pipeline that mounts both can
// emit an event that a sibling listener receives within the same
// process.
func Library(eventBus *bus.Bus[any]) action.Library {
	tr := tbus.New(eventBus)
	return action.Library{
		Name: "transport.tbus",
		Actions: []action.AnyAction{
			listenTriggerAction(tr),
			publishAction(tr),
		},
	}
}

// ── publish ──────────────────────────────────────────────────────────

// PublishRequest is the structured input accepted by tbus.publish. A
// bare map[string]any is also accepted: "topic" is required, and the
// remaining keys become the payload if "payload" is absent.
type PublishRequest struct {
	Topic   string `json:"topic"`
	Payload any    `json:"payload,omitempty"`
}

func publishAction(tr *tbus.Transport) action.AnyAction {
	return action.New("tbus.publish", func(ctx context.Context, in any) (any, error) {
		topic, payload, err := extractPublishInput(in)
		if err != nil {
			return nil, err
		}
		if pubErr := tr.Publish(ctx, topic, payload); pubErr != nil {
			return nil, xerr.Unavailable("tbus.publish: "+pubErr.Error(), pubErr)
		}
		return in, nil
	}).
		Description("Publish an in-process event to a topic; input passes through unchanged").
		Tag("transport", "tbus").
		Build()
}

func extractPublishInput(in any) (topic string, payload any, err error) {
	switch v := in.(type) {
	case PublishRequest:
		if v.Topic == "" {
			return "", nil, xerr.BadRequest("tbus.publish: 'topic' is required")
		}
		return v.Topic, v.Payload, nil
	case map[string]any:
		rawTopic, ok := v["topic"].(string)
		if !ok || rawTopic == "" {
			return "", nil, xerr.BadRequest("tbus.publish: 'topic' is required (use: { topic: \"...\", payload: ... })")
		}
		if p, hasPayload := v["payload"]; hasPayload {
			return rawTopic, p, nil
		}
		// No explicit payload: forward the whole map minus "topic".
		filtered := make(map[string]any, len(v)-1)
		for k, val := range v {
			if k == "topic" {
				continue
			}
			filtered[k] = val
		}
		return rawTopic, filtered, nil
	default:
		return "", nil, xerr.BadRequest(fmt.Sprintf(
			"tbus.publish: input must be an object with a 'topic' field, got %T", in))
	}
}

// ── listen ───────────────────────────────────────────────────────────

func listenTriggerAction(tr *tbus.Transport) action.AnyAction {
	return action.New("tbus.listen", func(ctx context.Context, workload any) (any, error) {
		actionsToMount, fallbackName, ok := resolveTBusWorkload(ctx, workload)
		if !ok {
			return nil, errors.New("tbus.listen: no explicit workload and no usable action resolver in context")
		}
		if len(actionsToMount) == 0 {
			return nil, errors.New("tbus.listen: auto-discovery found 0 topic-bound actions")
		}

		for _, act := range actionsToMount {
			hasBinding := false
			for _, b := range act.GetBindings() {
				if _, ok := b.(tbus.TopicBinding); ok {
					hasBinding = true
					break
				}
			}
			if !hasBinding && fallbackName != "" && fallbackName != "inline" {
				act = action.Dynamic(act).Route(tbus.Topic(fallbackName)).Build()
			}
			tr.Mount([]action.AnyAction{act})
		}
		return tr.Do(ctx, nil)
	}).
		Tag("transport", "tbus").
		Build()
}

func resolveTBusWorkload(ctx context.Context, workload any) (actions []action.AnyAction, fallbackName string, ok bool) {
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
			if _, isTopic := b.(tbus.TopicBinding); isTopic {
				discovered = append(discovered, act)
				break
			}
		}
	}
	return discovered, "", true
}
