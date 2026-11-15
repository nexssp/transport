package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimitUsesClientAddressAndReturnsRetryAfter(t *testing.T) {
	mw, err := NewRateLimit(RateLimitConfig{RequestsPerSecond: 1, Burst: 1})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	first := rateLimitRequest(handler, "192.0.2.10:1000")
	if first.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d, want 204", first.Code)
	}
	second := rateLimitRequest(handler, "192.0.2.10:1001")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
	if got := second.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	otherClient := rateLimitRequest(handler, "192.0.2.11:1000")
	if otherClient.Code != http.StatusNoContent {
		t.Fatalf("other client status = %d, want 204", otherClient.Code)
	}
	if called != 2 {
		t.Fatalf("next handler called %d times, want 2", called)
	}
}

func TestRateLimitSupportsCustomKey(t *testing.T) {
	mw, err := NewRateLimit(RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		KeyFunc: func(*http.Request) string {
			return "shared-user"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if got := rateLimitRequest(handler, "192.0.2.1:1000").Code; got != http.StatusNoContent {
		t.Fatalf("first request status = %d, want 204", got)
	}
	if got := rateLimitRequest(handler, "192.0.2.2:1000").Code; got != http.StatusTooManyRequests {
		t.Fatalf("request sharing custom key status = %d, want 429", got)
	}
}

func TestNewRateLimitRejectsInvalidDefaultRate(t *testing.T) {
	for _, rps := range []float64{0, -1} {
		if _, err := NewRateLimit(RateLimitConfig{RequestsPerSecond: rps}); err == nil {
			t.Errorf("NewRateLimit(%v) succeeded, want validation error", rps)
		}
	}
}

func TestRateLimitCapsPerClientState(t *testing.T) {
	mw, err := NewRateLimit(RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		MaxKeys:           1,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, remote := range []string{"192.0.2.1:1000", "192.0.2.2:1000", "192.0.2.1:1000"} {
		if got := rateLimitRequest(handler, remote).Code; got != http.StatusNoContent {
			t.Fatalf("request from %s status = %d, want 204 after bounded-key eviction", remote, got)
		}
	}
}

func TestRateLimitFailsClosedWhenInjectedLimiterErrors(t *testing.T) {
	mw, err := NewRateLimit(RateLimitConfig{Limiter: failingRateLimiter{}})
	if err != nil {
		t.Fatal(err)
	}
	handler := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler ran after limiter error")
	}))
	response := rateLimitRequest(handler, "192.0.2.1:1000")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

type failingRateLimiter struct{}

func (failingRateLimiter) Allow(context.Context, string) (bool, error) {
	return false, context.DeadlineExceeded
}

func rateLimitRequest(handler http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = remoteAddr
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
