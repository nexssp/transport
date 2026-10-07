package nexssflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nexssp/flow/contracts"
	"github.com/nexssp/flow/core"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/thttp"
)

type Config struct {
	Addr    string        `flow:"addr" default:":8080"`
	Timeout time.Duration `flow:"timeout" default:"30s"`
}

func init() {
	core.Register("thttp", Bundle)
}

func Bundle(opts map[string]string) core.Bundle {
	cfg, err := core.Decode[Config](opts)
	if err != nil {
		panic(err)
	}
	return core.Bundle{
		ID:        "thttp",
		Libraries: []action.Library{Library(cfg)},
		Modifiers: []core.Modifier{
			core.String("route", func(b *action.Builder[any, any], raw string) *action.Builder[any, any] {
				method := "POST"
				path := raw
				if sp := strings.IndexByte(raw, ' '); sp > 0 {
					method = strings.ToUpper(strings.TrimSpace(raw[:sp]))
					path = strings.TrimSpace(raw[sp+1:])
				}
				if path == "" {
					path = "/"
				}
				return b.Route(thttp.HTTPRoute{Method: method, Path: path})
			}),
		},
	}
}

func Library(cfg Config) action.Library {
	return action.Library{
		Name: "transport.thttp",
		Actions: []action.AnyAction{
			listenTriggerAction(cfg),
		},
	}
}

type httpWorkload struct {
	actions []action.AnyAction
	addr    string
}

// resolveHTTPWorkload decides the action set for thttp.listen:
//
//  1. action.Library or []action.AnyAction — explicit, authoritative.
//  2. map[string]any — a route table (either bare or under "routes:").
//     This is only treated as explicit when it resolves to at least one
//     action. A bare `thttp.listen` atom receives the pipeline's current
//     value as its workload; when that value is nil or an empty map, it
//     is not an explicit routing decision and must fall through to
//     auto-discovery.
//  3. resolver from context — auto-discovery of every action that
//     carries an HTTPRoute binding.
func resolveHTTPWorkload(ctx context.Context, workload any, defaultAddr string) (httpWorkload, error) {
	switch w := workload.(type) {
	case action.Library:
		addr := defaultAddr
		if w.Name != "" && strings.Contains(w.Name, ":") {
			addr = w.Name
		}
		return httpWorkload{actions: w.Actions, addr: addr}, nil
	case []action.AnyAction:
		return httpWorkload{actions: w, addr: defaultAddr}, nil
	case map[string]any:
		resolved := resolveHTTPWorkloadMap(ctx, w, defaultAddr)
		if len(resolved.actions) > 0 {
			return resolved, nil
		}
		// Empty/unresolvable route map falls through to discovery.
	}

	return autoDiscoverHTTPWorkload(ctx, defaultAddr)
}

func resolveHTTPWorkloadMap(ctx context.Context, w map[string]any, defaultAddr string) httpWorkload {
	routes := w
	if nested, found := w["routes"].(map[string]any); found {
		routes = nested
	}
	return httpWorkload{actions: resolveHTTPRouteMap(ctx, routes), addr: defaultAddr}
}

func autoDiscoverHTTPWorkload(ctx context.Context, defaultAddr string) (httpWorkload, error) {
	resolver := contracts.ActionResolverFromContext(ctx)
	if resolver == nil {
		return httpWorkload{}, errors.New("thttp.listen: action resolver is nil in execution context")
	}
	lister, ok := resolver.(interface{ Actions() []action.AnyAction })
	if !ok {
		return httpWorkload{}, fmt.Errorf("thttp.listen: resolver (%T) does not implement Actions()", resolver)
	}

	allActions := lister.Actions()
	discovered := filterHTTPRoutedActions(allActions)
	if len(discovered) == 0 {
		return httpWorkload{}, fmt.Errorf(
			"thttp.listen: auto-discovery found 0 routed actions. Available in resolver: %v",
			describeHTTPActions(allActions),
		)
	}
	return httpWorkload{actions: discovered, addr: defaultAddr}, nil
}

func filterHTTPRoutedActions(actions []action.AnyAction) []action.AnyAction {
	var out []action.AnyAction
	for _, act := range actions {
		if act == nil {
			continue
		}
		if slices.ContainsFunc(act.GetBindings(), isHTTPRouteBinding) {
			out = append(out, act)
		}
	}
	return out
}

func isHTTPRouteBinding(b action.Binding) bool {
	_, isRoute := b.(thttp.HTTPRoute)
	return isRoute
}

func describeHTTPActions(actions []action.AnyAction) []string {
	var names []string
	for _, act := range actions {
		if act == nil || act.Describe() == nil {
			continue
		}
		names = append(names, fmt.Sprintf("%s (bindings=%v)", act.Describe().Name, act.GetBindings()))
	}
	return names
}

func resolveHTTPRouteMap(ctx context.Context, routes map[string]any) []action.AnyAction {
	resolver := contracts.ActionResolverFromContext(ctx)
	if resolver == nil {
		return nil
	}
	var out []action.AnyAction
	for routeStr, target := range routes {
		targetName, ok := target.(string)
		if !ok {
			continue
		}
		method := "POST"
		path := strings.TrimSpace(routeStr)
		if sp := strings.IndexByte(path, ' '); sp > 0 {
			method = strings.ToUpper(strings.TrimSpace(path[:sp]))
			path = strings.TrimSpace(path[sp+1:])
		}
		if path == "" {
			path = "/"
		}
		if act, found := resolver.Action(targetName); found {
			act = action.Dynamic(act).Route(thttp.HTTPRoute{Method: method, Path: path}).Build()
			out = append(out, act)
		}
	}
	return out
}

func listenTriggerAction(cfg Config) action.AnyAction {
	return action.New("thttp.listen", func(ctx context.Context, workload any) (any, error) {
		resolved, err := resolveHTTPWorkload(ctx, workload, cfg.Addr)
		if err != nil {
			return nil, err
		}
		if len(resolved.actions) == 0 {
			return nil, errors.New("thttp.listen: no actions mounted to server")
		}

		server := thttp.New(resolved.addr, thttp.WithReadTimeout(cfg.Timeout))

		toMount := make([]action.AnyAction, 0, len(resolved.actions))
		for _, act := range resolved.actions {
			if act == nil {
				continue
			}
			if !slices.ContainsFunc(act.GetBindings(), server.CanHandle) {
				p := "/"
				if desc := act.Describe(); desc != nil && desc.Name != "" {
					p = "/" + strings.TrimPrefix(desc.Name, "pipeline.")
				}
				act = action.Dynamic(act).Route(thttp.POST(p)).Build()
			}
			toMount = append(toMount, act)
		}
		if len(toMount) == 0 {
			return nil, errors.New("thttp.listen: no actions mounted to server")
		}
		server.Mount(toMount)
		return server.Do(ctx, nil)
	}).
		Tag("transport", "thttp").
		Build()
}
