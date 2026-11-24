package nexssflow_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/flow/extensions/pipeline"
	"github.com/nexssp/flow/extensions/projection"
	"github.com/nexssp/flow/extensions/runtime"
	"github.com/nexssp/flow/runner"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/kernel/xtest/ktest"

	"github.com/nexssp/transport/thttp"
	"github.com/nexssp/transport/thttp/nexssflow"
)

func TestHTTPServer_AutoDiscovery(t *testing.T) {
	var listenConfig net.ListenConfig
	ln, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	ktest.RequireNoError(t, err)
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("listener address is not *net.TCPAddr")
	}
	port := tcpAddr.Port
	_ = ln.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	rawAction := action.New("fixture.raw", func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	}).Route(thttp.RawHandler(http.MethodGet, "/raw", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "raw-handler-ok")
	}))).Build()
	rawBundle := compiler.Bundle{
		ID:        "raw-fixture",
		Libraries: []action.Library{{Name: "raw-fixture", Actions: []action.AnyAction{rawAction}}},
	}

	src := `
		@pipeline health:route="GET /api/health"
  { status: "up" }
@end

@pipeline echo:route="POST /echo"
  noop
@end

thttp.listen
`
	bundles := []compiler.Bundle{
		runtime.Bundle(nil),
		pipeline.Bundle(nil),
		projection.Bundle(nil),
		rawBundle,
		nexssflow.Bundle(map[string]string{
			"addr":             addr,
			"security_headers": "true",
		}),
	}

	cfg, err := runner.BuildConfig(bundles)
	ktest.RequireNoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverDone := make(chan error, 1)
	go func() {
		_, execErr := runner.Execute(ctx, cfg, src, "api_test.nflow", nil)
		serverDone <- execErr
	}()

	clientTransport := &http.Transport{DisableKeepAlives: true}
	defer clientTransport.CloseIdleConnections()
	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: clientTransport}
	healthURL := fmt.Sprintf("http://%s/api/health", addr)

	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	var lastStatus int
	ready := false

	for time.Now().Before(deadline) {
		select {
		case execErr := <-serverDone:
			t.Fatalf("server exited unexpectedly: %v", execErr)
		default:
		}

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, http.NoBody)
		if reqErr != nil {
			lastErr = reqErr
			time.Sleep(25 * time.Millisecond)
			continue
		}
		resp, doErr := client.Do(req)
		if doErr == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		} else {
			lastErr = doErr
		}
		time.Sleep(25 * time.Millisecond)
	}

	if !ready {
		t.Fatalf("server not ready on %s within 3s; last status: %d, last error: %v", addr, lastStatus, lastErr)
	}

	// Test 1: GET /api/health
	healthReq, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, http.NoBody)
	ktest.RequireNoError(t, err)
	resp, err := client.Do(healthReq)
	ktest.RequireNoError(t, err)
	defer resp.Body.Close()
	for header, want := range map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
		"Cache-Control":              "no-store",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("configured NFlow middleware %s = %q, want %q", header, got, want)
		}
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "" {
		t.Errorf("NFlow security_headers unexpectedly set application-owned CSP: %q", got)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("NFlow HSTS should not be set for plain HTTP: %q", got)
	}

	body, err := io.ReadAll(resp.Body)
	ktest.RequireNoError(t, err)
	ktest.RequireNoError(t, resp.Body.Close())

	var healthRes map[string]any
	err = json.Unmarshal(body, &healthRes)
	ktest.RequireNoError(t, err)
	ktest.RequireEqual(t, healthRes["status"], "up")

	// Test 2: POST /echo
	postURL := fmt.Sprintf("http://%s/echo", addr)
	postBody := bytes.NewBufferString(`{"greeting":"hello world"}`)
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, postBody)
	ktest.RequireNoError(t, err)
	postReq.Header.Set("Content-Type", "application/json")

	respPost, err := client.Do(postReq)
	ktest.RequireNoError(t, err)
	defer respPost.Body.Close()

	ktest.RequireEqual(t, respPost.StatusCode, http.StatusOK)
	echoBody, err := io.ReadAll(respPost.Body)
	ktest.RequireNoError(t, err)
	ktest.RequireNoError(t, respPost.Body.Close())

	var echoRes map[string]any
	_ = json.Unmarshal(echoBody, &echoRes)
	ktest.RequireEqual(t, echoRes["greeting"], "hello world")

	// RawHTTPHandler bindings are handled by the same NFlow auto-discovery path.
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, fmt.Sprintf("http://%s/raw", addr), http.NoBody)
	ktest.RequireNoError(t, err)
	rawResp, err := client.Do(request)
	ktest.RequireNoError(t, err)
	rawBody, err := io.ReadAll(rawResp.Body)
	ktest.RequireNoError(t, err)
	ktest.RequireNoError(t, rawResp.Body.Close())
	ktest.RequireEqual(t, rawResp.StatusCode, http.StatusOK)
	ktest.RequireEqual(t, string(rawBody), "raw-handler-ok")
	if got := rawResp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("raw handler security header = %q, want nosniff", got)
	}
	if got := rawResp.Header.Get("Cache-Control"); got != "" {
		t.Errorf("non-API raw handler Cache-Control = %q, want empty", got)
	}
	if got := rawResp.Header.Get("Content-Security-Policy"); got != "" {
		t.Errorf("raw handler unexpectedly received an application-owned CSP: %q", got)
	}

	cancel()
	select {
	case execErr := <-serverDone:
		if execErr != nil && !errors.Is(execErr, context.Canceled) {
			t.Fatalf("server exited with error: %v", execErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server failed to shutdown within timeout")
	}
}

func TestHTTPServer_NFlowStreamRouteFlushesAndCompletes(t *testing.T) {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	ktest.RequireNoError(t, err)
	addr := listener.Addr().String()
	ktest.RequireNoError(t, listener.Close())

	tokenEmitted := make(chan struct{})
	releaseStream := make(chan struct{})
	streamAction := action.New("fixture.stream", func(ctx context.Context, _ any) (map[string]any, error) {
		writer, ok := thttp.StreamEventWriterFromContext(ctx)
		if !ok {
			return nil, errors.New("request stream writer is missing")
		}
		if err := writer.WriteEvent(ctx, thttp.StreamEvent{Type: "token.delta", Delta: "Hello"}); err != nil {
			return nil, err
		}
		close(tokenEmitted)
		select {
		case <-releaseStream:
			return map[string]any{"answer": "Hello, Mercury3"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}).Build()
	errorAction := action.New("fixture.failure", func(context.Context, any) (any, error) {
		return nil, xerr.BadRequest("fixture rejected input")
	}).Build()
	fixtureBundle := compiler.Bundle{
		ID:        "stream-fixture",
		Libraries: []action.Library{{Name: "fixture", Actions: []action.AnyAction{streamAction, errorAction}}},
	}
	source := `
@pipeline health:route="GET /health"
  { status: "up" }
@end

@pipeline stream:stream_route="POST /stream"
  fixture.stream
@end

@pipeline failure:stream_route="POST /failure"
  fixture.failure
@end

thttp.listen
`
	bundles := []compiler.Bundle{
		runtime.Bundle(nil),
		pipeline.Bundle(nil),
		projection.Bundle(nil),
		fixtureBundle,
		nexssflow.Bundle(map[string]string{"addr": addr, "max_body_bytes": "4096"}),
	}
	cfg, err := runner.BuildConfig(bundles)
	ktest.RequireNoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		_, runErr := runner.Execute(ctx, cfg, source, "stream_route_test.nflow", nil)
		serverDone <- runErr
	}()
	defer func() {
		select {
		case <-releaseStream:
		default:
			close(releaseStream)
		}
		cancel()
		select {
		case runErr := <-serverDone:
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Errorf("server exited with error: %v", runErr)
			}
		case <-time.After(2 * time.Second):
			t.Error("server failed to shut down")
		}
	}()

	client := &http.Client{Timeout: 4 * time.Second}
	defer client.CloseIdleConnections()
	healthURL := "http://" + addr + "/health"
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case runErr := <-serverDone:
			t.Fatalf("server exited before readiness: %v", runErr)
		default:
		}
		request, requestErr := http.NewRequestWithContext(t.Context(), http.MethodGet, healthURL, http.NoBody)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/stream", bytes.NewBufferString(`{"message":"hello"}`))
	ktest.RequireNoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	ktest.RequireNoError(t, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("stream response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	firstLine := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, readErr := reader.ReadString('\n')
		firstLine <- struct {
			line string
			err  error
		}{line: line, err: readErr}
	}()
	select {
	case first := <-firstLine:
		ktest.RequireNoError(t, first.err)
		var event thttp.StreamEvent
		ktest.RequireNoError(t, json.Unmarshal([]byte(first.line), &event))
		ktest.RequireEqual(t, event.Type, "token.delta")
		ktest.RequireEqual(t, event.Delta, "Hello")
	case <-time.After(time.Second):
		t.Fatal("first token event was not flushed while the action was still running")
	}
	close(releaseStream)
	finalLine, err := reader.ReadString('\n')
	ktest.RequireNoError(t, err)
	var finalEvent map[string]any
	ktest.RequireNoError(t, json.Unmarshal([]byte(finalLine), &finalEvent))
	ktest.RequireEqual(t, finalEvent["type"], "done")
	result, ok := finalEvent["result"].(map[string]any)
	ktest.RequireCondition(t, ok, "final event = %#v, want result object", finalEvent)
	ktest.RequireEqual(t, result["answer"], "Hello, Mercury3")

	failureRequest, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/failure", bytes.NewBufferString(`{}`))
	ktest.RequireNoError(t, err)
	failureRequest.Header.Set("Content-Type", "application/json")
	failureResponse, err := client.Do(failureRequest)
	ktest.RequireNoError(t, err)
	failureBody, err := io.ReadAll(failureResponse.Body)
	ktest.RequireNoError(t, err)
	ktest.RequireNoError(t, failureResponse.Body.Close())
	var errorEvent map[string]any
	ktest.RequireNoError(t, json.Unmarshal(bytes.TrimSpace(failureBody), &errorEvent))
	ktest.RequireEqual(t, errorEvent["type"], "error")
	ktest.RequireCondition(t, errorEvent["error"] != nil, "error event = %#v, want a public error", errorEvent)
}
