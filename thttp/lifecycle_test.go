package thttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDoReturnsStartupFailure(t *testing.T) {
	transport := New("://invalid-address")
	_, err := transport.Do(context.Background(), nil)
	if err == nil {
		t.Fatal("expected invalid listen address to fail")
	}
	if got := err.Error(); !strings.HasPrefix(got, "http server crash:") {
		t.Fatalf("unexpected startup error: %v", err)
	}
}

func TestDoReturnsWhenContextAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New("://invalid-address").Do(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got error %v, want context.Canceled", err)
	}
}

func TestDoGracefullyDrainsInFlightRequest(t *testing.T) {
	var listenConfig net.ListenConfig
	probe, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	transport := New(addr)
	transport.Mux().HandleFunc("GET /block", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doDone := make(chan error, 1)
	go func() {
		_, err := transport.Do(ctx, nil)
		doDone <- err
	}()

	requestDone := make(chan error, 1)
	go func() {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/block", http.NoBody)
		if err != nil {
			requestDone <- err
			return
		}
		response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
		if err != nil {
			requestDone <- err
			return
		}
		defer response.Body.Close()
		_, copyErr := io.Copy(io.Discard, response.Body)
		requestDone <- copyErr
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach the handler")
	}

	cancel()
	select {
	case err := <-doDone:
		t.Fatalf("Do returned before the active handler drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("in-flight request failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not finish")
	}
	select {
	case err := <-doDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error: got %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Do did not return after the request drained")
	}
}
