package nexssflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexssp/transport/thttp"
)

func TestListenerIngressLimitHelpers(t *testing.T) {
	cfg := Config{MaxBodyBytes: 4, RateLimitRPS: 1, RateLimitBurst: 1}
	server := thttp.New(":0", thttp.WithMaxRequestBodyBytes(cfg.MaxBodyBytes))
	middleware, err := configuredRateLimit(cfg.RateLimitRPS, cfg.RateLimitBurst)
	if err != nil {
		t.Fatal(err)
	}
	server.Use(middleware)
	server.Mux().Handle("POST /limits", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tooLarge := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/limits", strings.NewReader("12345"))
	tooLarge.RemoteAddr = "192.0.2.10:1000"
	tooLargeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(tooLargeResponse, tooLarge)
	if tooLargeResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", tooLargeResponse.Code)
	}

	for i := range 2 {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/limits", strings.NewReader("{}"))
		request.RemoteAddr = "192.0.2.10:1000"
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		want := http.StatusNoContent
		if i == 1 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("request %d status = %d, want %d", i+1, response.Code, want)
		}
	}
}

func TestBundleAcceptsIngressProtectionOptions(t *testing.T) {
	bundle := Bundle(map[string]string{
		"max_body_bytes":          "4096",
		"rate_limit_rps":          "20",
		"rate_limit_burst":        "40",
		"loopback_only":           "true",
		"max_concurrent_requests": "3",
	})
	if bundle.ID != "thttp" || len(bundle.Libraries) == 0 {
		t.Fatalf("Bundle() = %#v, want thttp bundle with listener actions", bundle)
	}
}

func TestLoopbackOnlyRequiresAnExplicitLocalBind(t *testing.T) {
	if _, err := newListenerServer(Config{LoopbackOnly: true}, RuntimeConfig{}, ":8080"); err == nil {
		t.Fatal("loopback_only accepted a wildcard bind")
	}
	for _, address := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
		if _, err := newListenerServer(Config{LoopbackOnly: true}, RuntimeConfig{}, address); err != nil {
			t.Errorf("loopback_only rejected %q: %v", address, err)
		}
	}
}

func TestConfiguredMaxConcurrentRequestsRejectsExcessWork(t *testing.T) {
	server, err := newListenerServer(Config{MaxConcurrentRequests: 1}, RuntimeConfig{}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	server.Mux().HandleFunc("GET /busy", func(writer http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		writer.WriteHeader(http.StatusNoContent)
	})

	firstDone := make(chan int, 1)
	go func() {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/busy", http.NoBody)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		firstDone <- response.Code
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not enter route")
	}

	second := httptest.NewRecorder()
	server.Handler().ServeHTTP(second, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/busy", http.NoBody))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request status = %d, want 503", second.Code)
	}
	close(release)
	select {
	case status := <-firstDone:
		if status != http.StatusNoContent {
			t.Fatalf("first request status = %d, want 204", status)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
}
