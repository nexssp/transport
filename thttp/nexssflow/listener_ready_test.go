package nexssflow

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/nexssp/kernel/xctx"

	"github.com/nexssp/transport/thttp"
)

type readyEventCapture struct {
	events chan *thttp.ListenerReadyEvent
}

func (capture *readyEventCapture) PublishEvent(_ context.Context, subject string, payload any) error {
	if subject != "events.listener_ready" {
		return errors.New("unexpected listener-ready subject: " + subject)
	}
	event, ok := payload.(*thttp.ListenerReadyEvent)
	if !ok {
		return errors.New("unexpected listener-ready payload type")
	}
	capture.events <- event
	return nil
}

func TestNewListenerServerPassesConfiguredListenerReadyMetadata(t *testing.T) {
	server, err := newListenerServer(Config{
		ServiceID:     "mercury3-chat-web",
		AdvertisedURL: "http://127.0.0.1:8766",
	}, RuntimeConfig{}, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("newListenerServer: %v", err)
	}
	capture := &readyEventCapture{events: make(chan *thttp.ListenerReadyEvent, 1)}
	ctx, cancel := context.WithCancel(xctx.WithEventPublisher(context.Background(), capture))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, runErr := server.Do(ctx, nil)
		done <- runErr
	}()

	var event *thttp.ListenerReadyEvent
	select {
	case event = <-capture.events:
	case runErr := <-done:
		t.Fatalf("server returned before listener-ready event: %v", runErr)
	case <-time.After(3 * time.Second):
		t.Fatal("listener-ready event was not published")
	}
	if event.ServiceID != "mercury3-chat-web" || event.AdvertisedURL != "http://127.0.0.1:8766" {
		t.Fatalf("configured metadata = (%q, %q), want Mercury3 values", event.ServiceID, event.AdvertisedURL)
	}
	host, port, err := net.SplitHostPort(event.BindAddress)
	if err != nil || host != "127.0.0.1" || port == "0" {
		t.Fatalf("bind_address = %q, want actual loopback address with nonzero port", event.BindAddress)
	}
	cancel()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("server shutdown error = %v, want context.Canceled", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}
