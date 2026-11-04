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

	ready := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), ready))
	defer cancel()
	listenDone := make(chan error, 1)
	go func() {
		_, err := action.InvokeAny(ctx, listen, []action.AnyAction{subscriber, command})
		listenDone <- err
	}()
	select {
	case <-ready.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("bus listener did not signal readiness after registering handlers")
	}

	if _, err := action.InvokeAny(context.Background(), publish, map[string]any{
		"subject": "alerts",
		"payload": map[string]any{"text": "ready"},
	}); err != nil {
		t.Fatalf("publish event: %v", err)
	}
	select {
	case got := <-received:
		if got != "ready" {
			t.Fatalf("subscriber received %q, want %q", got, "ready")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Flow bus subscriber")
	}

	var response any
	var err error
	response, err = action.InvokeAny(context.Background(), send, map[string]any{
		"name":    "orders.lookup",
		"payload": map[string]any{"id": "order-42"},
	})
	if err != nil {
		t.Fatalf("send command: %v", err)
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

func TestFlowBusListenerRemovesRegistrationsOnRestart(t *testing.T) {
	lib := Library()
	listen := findAction(t, lib, "bus.listen")
	publish := findAction(t, lib, "bus.publish")
	send := findAction(t, lib, "bus.send")

	type notice struct {
		Text string `json:"text"`
	}
	eventCalls := make(chan string, 4)
	subscriber := action.New("notice.handler", func(_ context.Context, req notice) (string, error) {
		eventCalls <- req.Text
		return req.Text, nil
	}).Route(Subject("restart-alerts")).Build()
	command := action.New("command.handler", func(_ context.Context, req notice) (string, error) {
		return req.Text, nil
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
			t.Fatal("bus listener did not become ready")
		}
		return cancel, done
	}

	publishNotice := func(text string) {
		t.Helper()
		if _, err := action.InvokeAny(context.Background(), publish, map[string]any{
			"subject": "restart-alerts",
			"payload": map[string]any{"text": text},
		}); err != nil {
			t.Fatalf("publish event: %v", err)
		}
	}
	callCommand := func() error {
		_, err := action.InvokeAny(context.Background(), send, map[string]any{
			"name":    "restart-command",
			"payload": map[string]any{"text": "command"},
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
	publishNotice("first")
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

	cancel, done = start()
	publishNotice("second")
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

func TestFlowBusConcurrentListenersCleanupIndependently(t *testing.T) {
	lib := Library()
	listen := findAction(t, lib, "bus.listen")
	publish := findAction(t, lib, "bus.publish")
	send := findAction(t, lib, "bus.send")
	type notice struct {
		Text string `json:"text"`
	}
	calls := make(chan string, 8)
	subscriber := action.New("notice.handler", func(_ context.Context, req notice) (string, error) {
		calls <- req.Text
		return req.Text, nil
	}).Route(Subject("overlap-alerts")).Build()
	command := action.New("command.handler", func(_ context.Context, req notice) (string, error) {
		return req.Text, nil
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
			t.Fatal("bus listener did not become ready")
		}
		return cancel, done
	}
	publishNotice := func(value string) {
		t.Helper()
		if _, err := action.InvokeAny(context.Background(), publish, map[string]any{
			"subject": "overlap-alerts", "payload": map[string]any{"text": value},
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
	publishNotice("both")
	if len(calls) != 2 {
		t.Fatalf("active listener calls = %d, want 2", len(calls))
	}
	<-calls
	<-calls
	stop(cancelFirst, doneFirst)
	publishNotice("second")
	if len(calls) != 1 {
		t.Fatalf("remaining listener calls = %d, want 1", len(calls))
	}
	<-calls
	if _, err := action.InvokeAny(context.Background(), send, map[string]any{
		"name": "overlap-command", "payload": map[string]any{"text": "still-active"},
	}); err != nil {
		t.Fatalf("command from remaining listener: %v", err)
	}
	stop(cancelSecond, doneSecond)
	if _, err := action.InvokeAny(context.Background(), send, map[string]any{
		"name": "overlap-command", "payload": map[string]any{"text": "after-stop"},
	}); err == nil {
		t.Fatal("command remained registered after both listeners stopped")
	}
}
