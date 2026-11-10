package nexssflow_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/flow/extensions/pipeline"
	"github.com/nexssp/flow/extensions/runtime"
	"github.com/nexssp/flow/runner"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/thttp"
	thttpflow "github.com/nexssp/transport/thttp/nexssflow"
)

type rawRouteMiddlewareContextKey struct{}

func TestRawRouteNFlowPreservesHTTPExchangeAndRunsHostMiddleware(t *testing.T) {
	const bodyMarker = "raw-route-body-marker-7d2c"
	const queryMarker = "raw-route-query-marker-4a9f"
	middlewareKey := rawRouteMiddlewareContextKey{}
	var sawMiddleware atomic.Bool

	rawAction := action.New("rawroute.echo", func(ctx context.Context, input map[string]any) (thttp.RawHTTPResponse, error) {
		if len(input) != 0 {
			return thttp.RawHTTPResponse{}, errors.New("raw route decoded a request payload")
		}
		request, ok := thttp.RawHTTPRequestFromContext(ctx)
		if !ok || request.Request() == nil {
			return thttp.RawHTTPResponse{}, errors.New("raw request was not attached to action context")
		}
		rawRequest := request.Request()
		if rawRequest.Context().Value(middlewareKey) != "host-middleware-ran" {
			return thttp.RawHTTPResponse{}, errors.New("host middleware did not run before raw action")
		}
		if got := rawRequest.URL.Query().Get("code"); got != queryMarker {
			return thttp.RawHTTPResponse{}, errors.New("raw query was not preserved")
		}
		body, err := io.ReadAll(rawRequest.Body)
		if err != nil {
			return thttp.RawHTTPResponse{}, err
		}
		if string(body) != bodyMarker {
			return thttp.RawHTTPResponse{}, errors.New("raw body was changed or consumed")
		}
		sawMiddleware.Store(true)
		return thttp.NewRawHTTPResponse(http.StatusSeeOther, http.Header{
			"Content-Type": {"text/plain; charset=utf-8"},
			"Location":     {"https://accounts.example.test/callback?code=redirect-marker"},
			"Set-Cookie":   {"session=first; Path=/; HttpOnly", "csrf=second; Path=/"},
		}, body), nil
	}).Build()
	rawBundle := compiler.Bundle{
		ID:        "raw-route-fixture",
		Libraries: []action.Library{{Name: "raw-route-fixture", Actions: []action.AnyAction{rawAction}}},
	}

	address := reserveRawRouteAddress(t)
	config, err := runner.BuildConfig([]compiler.Bundle{runtime.Bundle(nil), pipeline.Bundle(nil), rawBundle, thttpflow.Bundle(map[string]string{
		"addr":             address,
		"security_headers": "true",
	})})
	if err != nil {
		t.Fatal(err)
	}
	source := `
	@pipeline raw_echo:raw_route="POST /raw"
	  rawroute.echo
	@end
	thttp.listen
`
	hostMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Host-Middleware", "ran")
			ctx := context.WithValue(r.Context(), middlewareKey, "host-middleware-ran")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	ctx, cancel := context.WithCancel(thttpflow.WithRuntimeConfig(context.Background(), thttpflow.RuntimeConfig{
		Addr:        address,
		Middlewares: []func(http.Handler) http.Handler{hostMiddleware},
	}))
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		_, runErr := runner.Execute(ctx, config, source, "raw_route_test.nflow", nil)
		serverDone <- runErr
	}()

	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	endpoint := "http://" + address + "/raw?code=" + queryMarker
	deadline := time.Now().Add(3 * time.Second)
	var response *http.Response
	for time.Now().Before(deadline) {
		select {
		case runErr := <-serverDone:
			t.Fatalf("Flow listener exited before serving route: %v", runErr)
		default:
		}
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(bodyMarker))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err = client.Do(request)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if response == nil {
		t.Fatalf("raw route did not start: %v", err)
	}
	payload, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status=%d, want 303", response.StatusCode)
	}
	if got := response.Header.Get("X-Host-Middleware"); got != "ran" || !sawMiddleware.Load() {
		t.Fatalf("runtime host middleware did not wrap the route: header=%q actionSaw=%t", got, sawMiddleware.Load())
	}
	if got := response.Header.Get("Location"); got != "https://accounts.example.test/callback?code=redirect-marker" {
		t.Errorf("Location=%q", got)
	}
	if got := response.Header.Values("Set-Cookie"); len(got) != 2 {
		t.Errorf("Set-Cookie values=%q, want two cookies", got)
	}
	if got := response.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type=%q", got)
	}
	if string(payload) != bodyMarker {
		t.Errorf("raw body was JSON-wrapped or modified: %q", payload)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("security middleware header=%q, want nosniff", got)
	}

	cancel()
	select {
	case runErr := <-serverDone:
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Fatalf("Flow listener shutdown error: %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Flow listener did not stop after context cancellation")
	}
}

func reserveRawRouteAddress(t *testing.T) string {
	t.Helper()
	listenConfig := &net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
