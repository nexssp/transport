// Package transport_test contains the executable specification of the
// transport contract.
//
// Every *nexssflow adapter must satisfy the same shape: a non-empty
// bundle, an action named "<id>.listen" reachable from the bundle's
// libraries, a modifier that produces a binding the corresponding
// transport recognizes via CanHandle, and a discovery guard that
// returns a clear error rather than silently starting an empty
// transport.
package transport_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nexssp/flow/core"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport"
	"github.com/nexssp/transport/bus"
	"github.com/nexssp/transport/cron"
	cronnexssflow "github.com/nexssp/transport/cron/nexssflow"
	"github.com/nexssp/transport/tbus"
	tbusnexssflow "github.com/nexssp/transport/tbus/nexssflow"
	"github.com/nexssp/transport/tcli"
	tclinexssflow "github.com/nexssp/transport/tcli/nexssflow"
	"github.com/nexssp/transport/thttp"
	thttpnexssflow "github.com/nexssp/transport/thttp/nexssflow"
	"github.com/nexssp/transport/tworker"
	tworkernexssflow "github.com/nexssp/transport/tworker/nexssflow"
)

type adapterCase struct {
	name         string
	bundle       core.Bundle
	listenAction string
	rawModifier  string

	checkBindings func(t *testing.T, bindings []action.Binding)
	newTransport  func() transport.Transport
}

func cases() []adapterCase {
	return []adapterCase{
		{
			name:         "tcli",
			bundle:       tclinexssflow.Bundle(nil),
			listenAction: "tcli.listen",
			rawModifier:  "cli=probe:Desc",
			checkBindings: func(t *testing.T, bindings []action.Binding) {
				t.Helper()
				for _, b := range bindings {
					cli, ok := b.(tcli.CLIBinding)
					if !ok {
						continue
					}
					if cli.Command != "probe" || cli.Description != "Desc" {
						t.Fatalf("CLIBinding = %+v, want {Command:probe Description:Desc}", cli)
					}
					return
				}
				t.Fatalf("no tcli.CLIBinding in %#v", bindings)
			},
			newTransport: func() transport.Transport { return tcli.New() },
		},
		{
			name:         "thttp",
			bundle:       thttpnexssflow.Bundle(nil),
			listenAction: "thttp.listen",
			rawModifier:  "route=GET /probe",
			checkBindings: func(t *testing.T, bindings []action.Binding) {
				t.Helper()
				for _, b := range bindings {
					route, ok := b.(thttp.HTTPRoute)
					if !ok {
						continue
					}
					if route.Method != http.MethodGet || route.Path != "/probe" {
						t.Fatalf("HTTPRoute = %+v, want {Method:%s Path:/probe}", route, http.MethodGet)
					}
					return
				}
				t.Fatalf("no thttp.HTTPRoute in %#v", bindings)
			},
			newTransport: func() transport.Transport { return thttp.New(":0") },
		},
		{
			name:         "tbus",
			bundle:       tbusnexssflow.Bundle(nil),
			listenAction: "tbus.listen",
			rawModifier:  "topic=probe",
			checkBindings: func(t *testing.T, bindings []action.Binding) {
				t.Helper()
				for _, b := range bindings {
					topic, ok := b.(tbus.TopicBinding)
					if !ok {
						continue
					}
					if topic.Topic != "probe" {
						t.Fatalf("TopicBinding = %+v, want {Topic:probe}", topic)
					}
					return
				}
				t.Fatalf("no tbus.TopicBinding in %#v", bindings)
			},
			newTransport: func() transport.Transport { return tbus.New(bus.New[any]()) },
		},
		{
			name:         "cron",
			bundle:       cronnexssflow.Bundle(nil),
			listenAction: "cron.listen",
			rawModifier:  "cron=@every 1m",
			checkBindings: func(t *testing.T, bindings []action.Binding) {
				t.Helper()
				for _, b := range bindings {
					cronB, ok := b.(cron.Binding)
					if !ok {
						continue
					}
					if cronB.Schedule != "@every 1m" {
						t.Fatalf("cron.Binding = %+v, want Schedule:@every 1m", cronB)
					}
					return
				}
				t.Fatalf("no cron.Binding in %#v", bindings)
			},
			newTransport: func() transport.Transport { return cron.New() },
		},
		{
			name:         "tworker",
			bundle:       tworkernexssflow.Bundle(nil),
			listenAction: "tworker.listen",
			rawModifier:  "worker=1m",
			checkBindings: func(t *testing.T, bindings []action.Binding) {
				t.Helper()
				for _, b := range bindings {
					workerB, ok := b.(tworker.Binding)
					if !ok {
						continue
					}
					if workerB.Interval != time.Minute {
						t.Fatalf("tworker.Binding = %+v, want Interval:1m", workerB)
					}
					return
				}
				t.Fatalf("no tworker.Binding in %#v", bindings)
			},
			newTransport: func() transport.Transport { return tworker.New() },
		},
	}
}

func TestTransportContract(t *testing.T) {
	t.Parallel()

	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			t.Run("bundle_shape", func(t *testing.T) {
				t.Parallel()
				testBundleShape(t, tc)
			})
			t.Run("listen_action_exists", func(t *testing.T) {
				t.Parallel()
				testListenActionExists(t, tc)
			})
			t.Run("modifier_registered", func(t *testing.T) {
				t.Parallel()
				testModifierRegistered(t, tc)
			})
			t.Run("modifier_produces_binding", func(t *testing.T) {
				t.Parallel()
				testModifierProducesBinding(t, tc)
			})
			t.Run("transport_recognizes_binding", func(t *testing.T) {
				t.Parallel()
				testTransportRecognizesBinding(t, tc)
			})
			t.Run("discovery_guard", func(t *testing.T) {
				t.Parallel()
				testDiscoveryGuard(t, tc)
			})
		})
	}
}

func testBundleShape(t *testing.T, tc adapterCase) {
	t.Helper()

	if tc.bundle.ID != tc.name {
		t.Fatalf("bundle.ID = %q, want %q", tc.bundle.ID, tc.name)
	}
	if len(tc.bundle.Libraries) == 0 {
		t.Fatal("bundle has no libraries")
	}
	for i := range tc.bundle.Libraries {
		if tc.bundle.Libraries[i].Name == "" {
			t.Fatalf("library %d has empty name", i)
		}
	}
}

func testListenActionExists(t *testing.T, tc adapterCase) {
	t.Helper()

	if findListenAction(tc) == nil {
		t.Fatalf("listen action %q not present in bundle %q", tc.listenAction, tc.bundle.ID)
	}
}

func testModifierRegistered(t *testing.T, tc adapterCase) {
	t.Helper()

	name, _, _ := strings.Cut(tc.rawModifier, "=")
	for i := range tc.bundle.Modifiers {
		if tc.bundle.Modifiers[i].Name == name {
			return
		}
	}
	t.Fatalf("modifier %q is not registered in bundle %q", name, tc.bundle.ID)
}

func testModifierProducesBinding(t *testing.T, tc adapterCase) {
	t.Helper()

	result := applyModifier(t, tc)
	bindings := result.GetBindings()
	if len(bindings) == 0 {
		t.Fatalf("modifier %q attached no binding", tc.rawModifier)
	}
	tc.checkBindings(t, bindings)
}

func testTransportRecognizesBinding(t *testing.T, tc adapterCase) {
	t.Helper()

	result := applyModifier(t, tc)
	bindings := result.GetBindings()
	if len(bindings) == 0 {
		t.Fatalf("modifier %q attached no binding", tc.rawModifier)
	}

	tr := tc.newTransport()
	if tr == nil {
		t.Fatal("newTransport returned nil")
	}
	if !slices.ContainsFunc(bindings, tr.CanHandle) {
		t.Fatalf("transport %s does not recognize any binding produced by %q: %#v",
			tr.String(), tc.rawModifier, bindings)
	}
}

func testDiscoveryGuard(t *testing.T, tc adapterCase) {
	t.Helper()

	listen := findListenAction(tc)
	if listen == nil {
		t.Fatalf("listen action %q not found", tc.listenAction)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := action.InvokeAny(ctx, listen, nil)
	if err == nil {
		t.Fatalf("listen action %q with nil workload and no resolver must return an error", tc.listenAction)
	}

	msg := err.Error()
	for _, needle := range []string{"resolver", "workload", "discovery", "nil"} {
		if strings.Contains(msg, needle) {
			return
		}
	}
	t.Fatalf("error does not describe the contract violation: %v", err)
}

func findListenAction(tc adapterCase) action.AnyAction {
	for i := range tc.bundle.Libraries {
		lib := &tc.bundle.Libraries[i]
		for _, act := range lib.Actions {
			if meta := act.Describe(); meta != nil && meta.Name == tc.listenAction {
				return act
			}
		}
	}
	return nil
}

func applyModifier(t *testing.T, tc adapterCase) action.AnyAction {
	t.Helper()

	table := core.NewModifierTable(tc.bundle.Modifiers...)
	probe := action.New("contract_probe", func(context.Context, any) (any, error) {
		return nil, nil
	}).Build()

	result, err := table.ApplyAll(probe, []string{tc.rawModifier})
	if err != nil {
		t.Fatalf("ApplyAll(%q) failed: %v", tc.rawModifier, err)
	}
	return result
}
