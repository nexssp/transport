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

	"github.com/nexssp/flow/compiler/contracts"
)

func waitForReady(t *testing.T, signal *contracts.ReadySignal) {
	t.Helper()
	select {
	case <-signal.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP service did not become ready after binding")
	}
}

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

func TestDoSignalsReadyOnlyAfterSuccessfulBind(t *testing.T) {
	var listenConfig net.ListenConfig
	probe, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}

	signal := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), signal))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := New(addr).Do(ctx, nil)
		done <- err
	}()
	waitForReady(t, signal)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP service did not stop after cancellation")
	}
}

func TestDoDoesNotSignalReadyWhenBindFails(t *testing.T) {
	signal := contracts.NewReadySignal()
	_, err := New("://invalid-address").Do(contracts.WithServiceReady(context.Background(), signal), nil)
	if err == nil {
		t.Fatal("expected bind failure")
	}
	select {
	case <-signal.Done():
		t.Fatal("HTTP service signaled ready despite bind failure")
	default:
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

	ready := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), ready))
	defer cancel()
	doDone := make(chan error, 1)
	go func() {
		_, err := transport.Do(ctx, nil)
		doDone <- err
	}()

	waitForReady(t, ready)

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
	case err := <-requestDone:
		t.Fatalf("request failed before reaching the handler: %v", err)
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

func TestDoClosesIdleKeepAliveConnectionOnShutdown(t *testing.T) {
	var listenConfig net.ListenConfig
	probe, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}

	transport := New(addr)
	transport.Mux().HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	ready := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), ready))
	defer cancel()
	doDone := make(chan error, 1)
	go func() {
		_, err := transport.Do(ctx, nil)
		doDone <- err
	}()
	waitForReady(t, ready)

	clientTransport := &http.Transport{}
	defer clientTransport.CloseIdleConnections()
	client := &http.Client{Transport: clientTransport}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/ok", http.NoBody)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET /ok: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		t.Fatalf("read response body: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close response body: %v", closeErr)
	}
	if string(body) != "ok" {
		t.Fatalf("response body = %q, want %q", body, "ok")
	}

	// Leave the client's keep-alive connection idle. Server.Do must close it as
	// part of graceful shutdown; this separately guards the behavior excluded
	// from the route auto-discovery test's isolated, non-keep-alive client.
	cancel()
	select {
	case err := <-doDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error: got %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Do did not close the idle keep-alive connection during shutdown")
	}
}
