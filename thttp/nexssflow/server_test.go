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

	"github.com/nexssp/flow/core"
	"github.com/nexssp/flow/extensions/pipeline"
	"github.com/nexssp/flow/extensions/runtime"
	"github.com/nexssp/flow/runner"
	"github.com/nexssp/kernel/xtest/ktest"
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

	src := `
@pipeline health:route="GET /health"
  runtime.const @{ value: { status: "up" } }
@end

@pipeline echo:route="POST /echo"
  runtime.noop
@end

thttp.listen
`
	bundles := []core.Bundle{
		runtime.Bundle(nil),
		pipeline.Bundle(nil),
		nexssflow.Bundle(map[string]string{
			"addr": addr,
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

	client := &http.Client{Timeout: 500 * time.Millisecond}
	healthURL := fmt.Sprintf("http://%s/health", addr)

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

	// Test 1: GET /health
	healthReq, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, http.NoBody)
	ktest.RequireNoError(t, err)
	resp, err := client.Do(healthReq)
	ktest.RequireNoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	ktest.RequireNoError(t, err)

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
	echoBody, _ := io.ReadAll(respPost.Body)

	var echoRes map[string]any
	_ = json.Unmarshal(echoBody, &echoRes)
	ktest.RequireEqual(t, echoRes["greeting"], "hello world")

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
