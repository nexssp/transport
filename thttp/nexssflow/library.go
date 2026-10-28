package nexssflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"

	"github.com/nexssp/transport/thttp"
	httpmiddleware "github.com/nexssp/transport/thttp/middleware"
)

type Config struct {
	Addr              string        `nflow:"addr" default:":8080"`
	ReadHeaderTimeout time.Duration `nflow:"read_header_timeout" default:"5s"`
	ReadTimeout       time.Duration `nflow:"read_timeout" default:"30s"`
	WriteTimeout      time.Duration `nflow:"write_timeout" default:"30s"`
	IdleTimeout       time.Duration `nflow:"idle_timeout" default:"120s"`
	SecurityHeaders   bool          `nflow:"security_headers" default:"false"`
}

// RuntimeConfig overrides bundle-level server settings for one listener
// invocation. It is useful when the host chooses its bind address at runtime.
type RuntimeConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ActionFilter      func(action.AnyAction) bool
}

var runtimeConfigKey = xctx.NewKey[RuntimeConfig]("transport.thttp.runtime_config")

// WithRuntimeConfig attaches per-invocation listener settings to ctx.
func WithRuntimeConfig(ctx context.Context, cfg RuntimeConfig) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return runtimeConfigKey.With(ctx, cfg)
}

func runtimeConfigFromContext(ctx context.Context) RuntimeConfig {
	if ctx == nil {
		return RuntimeConfig{}
	}
	cfg, _ := runtimeConfigKey.From(ctx)
	return cfg
}

func init() {
	compiler.Register("thttp", Bundle)
}

func Bundle(opts map[string]string) compiler.Bundle {
	cfg, err := compiler.Decode[Config](opts)
	if err != nil {
		panic(err)
	}
	return compiler.Bundle{
		ID:        "thttp",
		Libraries: []action.Library{Library(cfg)},
		Modifiers: []compiler.Modifier{
			compiler.String("route", func(b *action.Builder[any, any], raw string) *action.Builder[any, any] {
				method := http.MethodPost
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
//     action. A bare thttp.listen atom receives the pipeline's current value
//     as its workload; when empty, it falls through to auto-discovery.
//  3. resolver from context — auto-discovery of every binding supported by
//     the HTTP transport, including HTTPRoute, SSERoute, and RawHTTPHandler.
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
	var server thttp.Transport
	discovered := filterHTTPBoundActions(allActions, server.CanHandle)
	if filter := runtimeConfigFromContext(ctx).ActionFilter; filter != nil {
		filtered := discovered[:0]
		for _, act := range discovered {
			if filter(act) {
				filtered = append(filtered, act)
			}
		}
		discovered = filtered
	}
	if len(discovered) == 0 {
		return httpWorkload{}, fmt.Errorf("thttp.listen: auto-discovery found 0 supported actions. Available in resolver: %v", describeHTTPActions(allActions))
	}
	return httpWorkload{actions: discovered, addr: defaultAddr}, nil
}

func filterHTTPBoundActions(actions []action.AnyAction, canHandle func(action.Binding) bool) []action.AnyAction {
	var out []action.AnyAction
	for _, act := range actions {
		if act != nil && canHandle != nil && slices.ContainsFunc(act.GetBindings(), canHandle) {
			out = append(out, act)
		}
	}
	return out
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
		method := http.MethodPost
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

		runtime := runtimeConfigFromContext(ctx)
		addr := runtime.Addr
		if addr == "" {
			addr = resolved.addr
		}
		readHeaderTimeout := cfg.ReadHeaderTimeout
		if runtime.ReadHeaderTimeout > 0 {
			readHeaderTimeout = runtime.ReadHeaderTimeout
		}
		readTimeout := cfg.ReadTimeout
		if runtime.ReadTimeout > 0 {
			readTimeout = runtime.ReadTimeout
		}
		writeTimeout := cfg.WriteTimeout
		if runtime.WriteTimeout > 0 {
			writeTimeout = runtime.WriteTimeout
		}
		idleTimeout := cfg.IdleTimeout
		if runtime.IdleTimeout > 0 {
			idleTimeout = runtime.IdleTimeout
		}

		server := thttp.New(addr,
			thttp.WithReadHeaderTimeout(readHeaderTimeout),
			thttp.WithReadTimeout(readTimeout),
			thttp.WithWriteTimeout(writeTimeout),
			thttp.WithIdleTimeout(idleTimeout),
		)
		if cfg.SecurityHeaders {
			server.Use(httpmiddleware.SecurityHeaders, httpmiddleware.NoStoreAPIResponses)
		}

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
