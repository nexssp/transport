package thttp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/xctx"
)

const listenerReadySubject = "events.listener_ready"

type listenerReadyPublisher struct {
	mu             sync.Mutex
	subject        string
	payload        any
	err            error
	started        chan struct{}
	deadline       chan struct{}
	release        <-chan struct{}
	signal         *contracts.ReadySignal
	readyAtPublish bool
}

func (p *listenerReadyPublisher) PublishEvent(ctx context.Context, subject string, payload any) error {
	p.mu.Lock()
	p.subject = subject
	p.payload = payload
	if p.signal != nil {
		select {
		case <-p.signal.Done():
			p.readyAtPublish = true
		default:
		}
	}
	p.mu.Unlock()
	if p.started != nil {
		close(p.started)
	}
	if p.release != nil || p.deadline != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && p.deadline != nil {
				close(p.deadline)
			}
			return ctx.Err()
		}
	}
	return p.err
}

func (p *listenerReadyPublisher) event() (subject string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.subject, p.payload
}

func (p *listenerReadyPublisher) wasReadyAtPublish() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readyAtPublish
}

func TestDoPublishesListenerReadyWithActualEphemeralAddress(t *testing.T) {
	publisher := &listenerReadyPublisher{}
	signal := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(xctx.WithEventPublisher(
		contracts.WithServiceReady(context.Background(), signal), publisher,
	))
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := New("127.0.0.1:0", WithListenerReadyMetadata("test-service", "https://chat.example.test")).Do(ctx, nil)
		done <- err
	}()
	waitForReady(t, signal)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context.Canceled", err)
	}

	subject, payload := publisher.event()
	if subject != listenerReadySubject {
		t.Fatalf("subject = %q, want %q", subject, listenerReadySubject)
	}
	event, ok := payload.(*ListenerReadyEvent)
	if !ok {
		t.Fatalf("payload type = %T, want *thttp.ListenerReadyEvent", payload)
	}
	if event.ServiceID != "test-service" || event.AdvertisedURL != "https://chat.example.test" {
		t.Fatalf("listener-ready metadata = (%q, %q), want configured service and URL", event.ServiceID, event.AdvertisedURL)
	}
	if event.BindAddress == "127.0.0.1:0" || event.BindAddress == "" {
		t.Fatalf("BindAddress = %q, want actual bound address", event.BindAddress)
	}
	_, port, err := net.SplitHostPort(event.BindAddress)
	if err != nil || port == "0" {
		t.Fatalf("BindAddress = %q, want non-zero TCP port", event.BindAddress)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"service_id":"test-service","advertised_url":"https://chat.example.test","bind_address":"` + event.BindAddress + `"}`
	if string(encoded) != wantJSON {
		t.Fatalf("JSON = %s, want %s", encoded, wantJSON)
	}
}

func TestDoPublishesBeforeReadySignal(t *testing.T) {
	release := make(chan struct{})
	signal := contracts.NewReadySignal()
	publisher := &listenerReadyPublisher{started: make(chan struct{}), release: release, signal: signal}
	ctx, cancel := context.WithCancel(xctx.WithEventPublisher(
		contracts.WithServiceReady(context.Background(), signal), publisher,
	))
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := New("127.0.0.1:0").Do(ctx, nil)
		done <- err
	}()
	select {
	case <-publisher.started:
	case <-time.After(3 * time.Second):
		t.Fatal("listener-ready event was not published before readiness")
	}
	if publisher.wasReadyAtPublish() {
		t.Fatal("listener-ready event was published after the ReadySignal was marked")
	}
	select {
	case <-signal.Done():
		t.Fatal("ReadySignal closed while listener-ready publication was blocked")
	default:
	}
	close(release)
	waitForReady(t, signal)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not stop after cancellation while event publication was active")
	}
}

func TestDoDoesNotPublishListenerReadyOnBindFailure(t *testing.T) {
	publisher := &listenerReadyPublisher{}
	ctx := xctx.WithEventPublisher(context.Background(), publisher)
	if _, err := New("://invalid-address").Do(ctx, nil); err == nil {
		t.Fatal("expected bind failure")
	}
	if subject, payload := publisher.event(); subject != "" || payload != nil {
		t.Fatalf("published event on bind failure: subject=%q payload=%#v", subject, payload)
	}
}

func TestDoDoesNotPublishListenerReadyWhenCanceledBeforeBind(t *testing.T) {
	publisher := &listenerReadyPublisher{}
	ctx, cancel := context.WithCancel(xctx.WithEventPublisher(context.Background(), publisher))
	cancel()
	if _, err := New("://invalid-address").Do(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context.Canceled", err)
	}
	if subject, payload := publisher.event(); subject != "" || payload != nil {
		t.Fatalf("published event before bind: subject=%q payload=%#v", subject, payload)
	}
}

func TestDoTreatsListenerReadyPublisherErrorAsBestEffort(t *testing.T) {
	publisher := &listenerReadyPublisher{err: errors.New("publisher unavailable")}
	signal := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(xctx.WithEventPublisher(
		contracts.WithServiceReady(context.Background(), signal), publisher,
	))
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := New("127.0.0.1:0").Do(ctx, nil)
		done <- err
	}()
	waitForReady(t, signal)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context.Canceled", err)
	}
	if subject, payload := publisher.event(); subject != listenerReadySubject || payload == nil {
		t.Fatalf("publisher received subject=%q payload=%#v", subject, payload)
	}
}

func TestListenerReadyEventJSONShape(t *testing.T) {
	encoded, err := json.Marshal(ListenerReadyEvent{
		ServiceID:     "test-service",
		AdvertisedURL: "https://chat.example.test",
		BindAddress:   "127.0.0.1:12345",
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"service_id":     "test-service",
		"advertised_url": "https://chat.example.test",
		"bind_address":   "127.0.0.1:12345",
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("JSON fields = %#v, want %#v", fields, want)
	}
}

func TestDoBoundsListenerReadyPublicationWithContextDeadline(t *testing.T) {
	publisher := &listenerReadyPublisher{started: make(chan struct{}), deadline: make(chan struct{})}
	ctx, cancel := context.WithCancel(xctx.WithEventPublisher(context.Background(), publisher))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := New("127.0.0.1:0").Do(ctx, nil)
		done <- err
	}()
	select {
	case <-publisher.started:
	case <-time.After(3 * time.Second):
		t.Fatal("listener-ready publication did not start")
	}
	select {
	case <-publisher.deadline:
	case <-time.After(listenerReadyPublishTimeout + 3*time.Second):
		t.Fatal("publisher did not observe the listener-ready deadline")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled after caller cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not stop after publisher deadline and cancellation")
	}
}
