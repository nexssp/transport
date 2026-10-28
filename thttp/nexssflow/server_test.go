package nexssflow_test

import (
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
	"github.com/nexssp/flow/extensions/runtime"
	"github.com/nexssp/flow/runner"
	"github.com/nexssp/kernel/action"
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
  runtime.const @{ value: { status: "up" } }
@end

@pipeline echo:route="POST /echo"
  runtime.noop
@end

thttp.listen
`
	bundles := []compiler.Bundle{
		runtime.Bundle(nil),
		pipeline.Bundle(nil),
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
