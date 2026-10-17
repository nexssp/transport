package nexssflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/bus/taskqueue"
)

func TestFlowTaskQueueEnqueueRunsTaskBoundAction(t *testing.T) {
	lib := LibraryWithConfig(taskqueue.Config{Name: "flow-test", Workers: 1})
	listen := findAction(t, lib, "taskqueue.listen")
	enqueue := findAction(t, lib, "taskqueue.enqueue")

	type payload struct {
		ID string `json:"id"`
	}
	completed := make(chan string, 1)
	handler := action.New("orders.process", func(_ context.Context, req payload) (string, error) {
		select {
		case completed <- req.ID:
		default:
		}
		return req.ID, nil
	}).Route(Task("orders.process")).Build()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listenDone := make(chan error, 1)
	go func() {
		_, err := action.InvokeAny(ctx, listen, []action.AnyAction{handler})
		listenDone <- err
	}()

	if _, err := action.InvokeAny(context.Background(), enqueue, map[string]any{
		"type":     "orders.process",
		"payload":  map[string]any{"id": "order-42"},
		"priority": "high",
	}); err != nil {
		t.Fatalf("enqueue action error = %v", err)
	}
	select {
	case got := <-completed:
		if got != "order-42" {
			t.Fatalf("task payload id = %q, want order-42", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Flow task handler")
	}

	cancel()
	if err := <-listenDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("listen error = %v, want context.Canceled", err)
	}
}

func TestBundleModifierAttachesTaskBinding(t *testing.T) {
	bundle := Bundle(nil)
	if bundle.ID != "taskqueue" {
		t.Fatalf("bundle ID = %q, want taskqueue", bundle.ID)
	}
	table := compiler.NewModifierTable(bundle.Modifiers...)
	probe := action.New("probe", func(context.Context, any) (any, error) { return nil, nil }).Build()
	bound, err := table.ApplyAll(probe, []string{"task=email.send"})
	if err != nil {
		t.Fatalf("ApplyAll() error = %v", err)
	}
	if len(bound.GetBindings()) != 1 {
		t.Fatalf("bindings = %#v, want one task binding", bound.GetBindings())
	}
	route, ok := bound.GetBindings()[0].(TaskBinding)
	if !ok || route.Type != "email.send" {
		t.Fatalf("binding = %#v, want TaskBinding{Type:email.send}", bound.GetBindings()[0])
	}
}

func findAction(t *testing.T, lib action.Library, name string) action.AnyAction {
	t.Helper()
	for _, act := range lib.Actions {
		if meta := act.Describe(); meta != nil && meta.Name == name {
			return act
		}
	}
	t.Fatalf("action %q not found in %q", name, lib.Name)
	return nil
}
