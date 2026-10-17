package nexssflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/kernel/action"
)

func TestFlowBusPublishAndSendReachBoundActions(t *testing.T) {
	lib := Library()
	listen := findAction(t, lib, "bus.listen")
	publish := findAction(t, lib, "bus.publish")
	send := findAction(t, lib, "bus.send")

	type notice struct {
		Text string `json:"text"`
	}
	type commandRequest struct {
		ID string `json:"id"`
	}
	received := make(chan string, 1)
	subscriber := action.New("notice.handler", func(_ context.Context, req notice) (string, error) {
		select {
		case received <- req.Text:
		default:
		}
		return req.Text, nil
	}).Route(Subject("alerts")).Build()
	command := action.New("orders.lookup", func(_ context.Context, req commandRequest) (map[string]string, error) {
		return map[string]string{"order_id": req.ID}, nil
	}).Route(Command("orders.lookup")).Build()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listenDone := make(chan error, 1)
	go func() {
		_, err := action.InvokeAny(ctx, listen, []action.AnyAction{subscriber, command})
		listenDone <- err
	}()

	deadline := time.After(2 * time.Second)
	for {
		_, _ = action.InvokeAny(context.Background(), publish, map[string]any{
			"subject": "alerts",
			"payload": map[string]any{"text": "ready"},
		})
		select {
		case got := <-received:
			if got != "ready" {
				t.Fatalf("subscriber received %q, want %q", got, "ready")
			}
			goto commandTest
		case <-deadline:
			t.Fatal("timed out waiting for Flow bus subscriber")
		default:
			time.Sleep(time.Millisecond)
		}
	}

commandTest:
	var response any
	for {
		var err error
		response, err = action.InvokeAny(context.Background(), send, map[string]any{
			"name":    "orders.lookup",
			"payload": map[string]any{"id": "order-42"},
		})
		if err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("command handler did not become available: %v", err)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	result, ok := response.(map[string]string)
	if !ok || result["order_id"] != "order-42" {
		t.Fatalf("command response = %#v, want order_id order-42", response)
	}

	cancel()
	if err := <-listenDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("listen error = %v, want context.Canceled", err)
	}
}

func TestBundleModifiersAttachBindings(t *testing.T) {
	bundle := Bundle(nil)
	if bundle.ID != "bus" {
		t.Fatalf("bundle ID = %q, want bus", bundle.ID)
	}
	table := compiler.NewModifierTable(bundle.Modifiers...)
	probe := action.New("probe", func(context.Context, any) (any, error) { return nil, nil }).Build()
	bound, err := table.ApplyAll(probe, []string{"subject=alerts", "command=orders.lookup"})
	if err != nil {
		t.Fatalf("ApplyAll() error = %v", err)
	}
	var gotSubject, gotCommand bool
	for _, binding := range bound.GetBindings() {
		switch route := binding.(type) {
		case SubjectBinding:
			gotSubject = route.Subject == "alerts"
		case CommandBinding:
			gotCommand = route.Name == "orders.lookup"
		}
	}
	if !gotSubject || !gotCommand {
		t.Fatalf("bindings = %#v, want subject and command bindings", bound.GetBindings())
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
