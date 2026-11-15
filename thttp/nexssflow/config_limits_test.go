package nexssflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	tooLarge := httptest.NewRequest(http.MethodPost, "/limits", strings.NewReader("12345"))
	tooLarge.RemoteAddr = "192.0.2.10:1000"
	tooLargeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(tooLargeResponse, tooLarge)
	if tooLargeResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", tooLargeResponse.Code)
	}

	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodPost, "/limits", strings.NewReader("{}"))
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
		"max_body_bytes":   "4096",
		"rate_limit_rps":   "20",
		"rate_limit_burst": "40",
	})
	if bundle.ID != "thttp" || len(bundle.Libraries) == 0 {
		t.Fatalf("Bundle() = %#v, want thttp bundle with listener actions", bundle)
	}
}
