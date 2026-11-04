package nexssflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"
)

func TestFlowMediatorSendAndPublish(t *testing.T) {
	lib := Library()
	listen := findAction(t, lib, "mediator.listen")
	send := findAction(t, lib, "mediator.send")
	publish := findAction(t, lib, "mediator.publish")

	type request struct {
		Name string `json:"name"`
	}
	type event struct {
		ID string `json:"id"`
	}
	gotEvent := make(chan string, 1)
	command := action.New("users.create", func(_ context.Context, req request) (map[string]string, error) {
		return map[string]string{"id": "user:" + req.Name}, nil
	}).Route(Command("users.create")).Build()
	subscriber := action.New("users.created", func(_ context.Context, evt event) (string, error) {
		select {
		case gotEvent <- evt.ID:
		default:
		}
		return evt.ID, nil
	}).Route(Event("users.created")).Build()

	ready := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), ready))
	defer cancel()
	listenDone := make(chan error, 1)
	go func() {
		_, err := action.InvokeAny(ctx, listen, []action.AnyAction{command, subscriber})
		listenDone <- err
	}()
	select {
	case <-ready.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("mediator listener did not signal readiness after registering handlers")
	}

	response, err := action.InvokeAny(context.Background(), send, map[string]any{
		"name":    "users.create",
		"payload": map[string]any{"name": "Ada"},
	})
	if err != nil {
		t.Fatalf("send command: %v", err)
	}
	result, ok := response.(map[string]string)
	if !ok || result["id"] != "user:Ada" {
		t.Fatalf("command response = %#v, want id user:Ada", response)
	}

	if _, err := action.InvokeAny(context.Background(), publish, map[string]any{
		"name":    "users.created",
		"payload": map[string]any{"id": "user:Ada"},
	}); err != nil {
		t.Fatalf("publish event: %v", err)
	}
	select {
	case got := <-gotEvent:
		if got != "user:Ada" {
			t.Fatalf("event id = %q, want user:Ada", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Flow event subscriber")
	}

	cancel()
	if err := <-listenDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("listen error = %v, want context.Canceled", err)
	}
}

func TestBundleModifiersAttachBindings(t *testing.T) {
	bundle := Bundle(nil)
	if bundle.ID != "mediator" {
		t.Fatalf("bundle ID = %q, want mediator", bundle.ID)
	}
	table := compiler.NewModifierTable(bundle.Modifiers...)
	probe := action.New("probe", func(context.Context, any) (any, error) { return nil, nil }).Build()
	bound, err := table.ApplyAll(probe, []string{"command=users.create", "event=users.created"})
	if err != nil {
		t.Fatalf("ApplyAll() error = %v", err)
	}
	var gotCommand, gotEvent bool
	for _, binding := range bound.GetBindings() {
		switch route := binding.(type) {
		case CommandBinding:
			gotCommand = route.Name == "users.create"
		case EventBinding:
			gotEvent = route.Name == "users.created"
		}
	}
	if !gotCommand || !gotEvent {
		t.Fatalf("bindings = %#v, want command and event bindings", bound.GetBindings())
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

func TestFlowMediatorListenerRemovesRegistrationsOnRestart(t *testing.T) {
	lib := Library()
	listen := findAction(t, lib, "mediator.listen")
	publish := findAction(t, lib, "mediator.publish")
	send := findAction(t, lib, "mediator.send")

	type payload struct {
		Value string `json:"value"`
	}
	eventCalls := make(chan string, 4)
	subscriber := action.New("event.handler", func(_ context.Context, req payload) (string, error) {
		eventCalls <- req.Value
		return req.Value, nil
	}).Route(Event("restart-event")).Build()
	command := action.New("command.handler", func(_ context.Context, req payload) (string, error) {
		return req.Value, nil
	}).Route(Command("restart-command")).Build()
	workload := []action.AnyAction{subscriber, command}

	start := func() (context.CancelFunc, <-chan error) {
		ready := contracts.NewReadySignal()
		ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), ready))
		done := make(chan error, 1)
		go func() {
			_, err := action.InvokeAny(ctx, listen, workload)
			done <- err
		}()
		select {
		case <-ready.Done():
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("mediator listener did not become ready")
		}
		return cancel, done
	}
	publishEvent := func(value string) {
		t.Helper()
		if _, err := action.InvokeAny(context.Background(), publish, map[string]any{
			"name":    "restart-event",
			"payload": map[string]any{"value": value},
		}); err != nil {
			t.Fatalf("publish event: %v", err)
		}
	}
	callCommand := func() error {
		_, err := action.InvokeAny(context.Background(), send, map[string]any{
			"name":    "restart-command",
			"payload": map[string]any{"value": "command"},
		})
		return err
	}
	stop := func(cancel context.CancelFunc, done <-chan error) {
		t.Helper()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("listener error = %v, want context.Canceled", err)
		}
	}

	cancel, done := start()
	publishEvent("first")
	if got := <-eventCalls; got != "first" {
		t.Fatalf("first event = %q, want first", got)
	}
	if err := callCommand(); err != nil {
		t.Fatalf("first command: %v", err)
	}
	stop(cancel, done)
	if err := callCommand(); err == nil {
		t.Fatal("command remained registered after listener cancellation")
	}
	if _, err := action.InvokeAny(context.Background(), publish, map[string]any{
		"name": "restart-event", "payload": map[string]any{"value": "stale"},
	}); err == nil {
		t.Fatal("event remained registered after listener cancellation")
	}

	cancel, done = start()
	publishEvent("second")
	if got := <-eventCalls; got != "second" {
		t.Fatalf("restarted event = %q, want second", got)
	}
	if len(eventCalls) != 0 {
		t.Fatalf("stale event handlers delivered %d duplicate event(s)", len(eventCalls))
	}
	if err := callCommand(); err != nil {
		t.Fatalf("restarted command: %v", err)
	}
	stop(cancel, done)
}

func TestFlowMediatorConcurrentListenersCleanupIndependently(t *testing.T) {
	lib := Library()
	listen := findAction(t, lib, "mediator.listen")
	publish := findAction(t, lib, "mediator.publish")
	send := findAction(t, lib, "mediator.send")
	type payload struct {
		Value string `json:"value"`
	}
	calls := make(chan string, 8)
	subscriber := action.New("event.handler", func(_ context.Context, req payload) (string, error) {
		calls <- req.Value
		return req.Value, nil
	}).Route(Event("overlap-event")).Build()
	command := action.New("command.handler", func(_ context.Context, req payload) (string, error) {
		return req.Value, nil
	}).Route(Command("overlap-command")).Build()
	workload := []action.AnyAction{subscriber, command}
	start := func() (context.CancelFunc, <-chan error) {
		ready := contracts.NewReadySignal()
		ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), ready))
		done := make(chan error, 1)
		go func() {
			_, err := action.InvokeAny(ctx, listen, workload)
			done <- err
		}()
		select {
		case <-ready.Done():
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("mediator listener did not become ready")
		}
		return cancel, done
	}
	publishEvent := func(value string) {
		t.Helper()
		if _, err := action.InvokeAny(context.Background(), publish, map[string]any{
			"name": "overlap-event", "payload": map[string]any{"value": value},
		}); err != nil {
			t.Fatalf("publish event: %v", err)
		}
	}
	stop := func(cancel context.CancelFunc, done <-chan error) {
		t.Helper()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("listener error = %v, want context.Canceled", err)
		}
	}

	cancelFirst, doneFirst := start()
	cancelSecond, doneSecond := start()
	publishEvent("both")
	if len(calls) != 2 {
		t.Fatalf("active listener calls = %d, want 2", len(calls))
	}
	<-calls
	<-calls
	stop(cancelFirst, doneFirst)
	publishEvent("second")
	if len(calls) != 1 {
		t.Fatalf("remaining listener calls = %d, want 1", len(calls))
	}
	<-calls
	if _, err := action.InvokeAny(context.Background(), send, map[string]any{
		"name": "overlap-command", "payload": map[string]any{"value": "still-active"},
	}); err != nil {
		t.Fatalf("command from remaining listener: %v", err)
	}
	stop(cancelSecond, doneSecond)
	if _, err := action.InvokeAny(context.Background(), send, map[string]any{
		"name": "overlap-command", "payload": map[string]any{"value": "after-stop"},
	}); err == nil {
		t.Fatal("command remained registered after both listeners stopped")
	}
}
