package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSRequiresOriginAndRejectsWildcardCredentials(t *testing.T) {
	if _, err := CORS(); err == nil {
		t.Fatal("expected missing origin configuration to fail")
	}
	if _, err := CORS(WithOrigins("*"), WithCredentials()); err == nil {
		t.Fatal("expected wildcard plus credentials to fail")
	}
	if _, err := CORS(WithOrigins("https://good.example\r\nX-Test: bad")); err == nil {
		t.Fatal("expected CRLF origin to fail")
	}
	if _, err := CORS(WithOrigins("https://good.example"), WithMethods()); err == nil {
		t.Fatal("expected empty method override to fail")
	}
}

func TestCORSReflectsOnlyAllowedOriginAndVaries(t *testing.T) {
	middleware := MustCORS(WithOrigins("https://good.example"), WithCredentials(), WithExposedHeaders("X-Request-ID"))
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	for _, test := range []struct {
		name            string
		origin          string
		wantAllowOrigin string
		wantCredentials string
	}{
		{name: "allowed", origin: "https://good.example", wantAllowOrigin: "https://good.example", wantCredentials: "true"},
		{name: "rejected", origin: "https://evil.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusAccepted {
				t.Fatalf("status: got %d", response.Code)
			}
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != test.wantAllowOrigin {
				t.Errorf("allow-origin: got %q, want %q", got, test.wantAllowOrigin)
			}
			if got := response.Header().Get("Access-Control-Allow-Credentials"); got != test.wantCredentials {
				t.Errorf("allow-credentials: got %q, want %q", got, test.wantCredentials)
			}
			if got := response.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-ID" && test.wantAllowOrigin != "" {
				t.Errorf("expose-headers: got %q", got)
			}
			if !hasVary(response.Header(), "Origin") {
				t.Fatal("Vary: Origin missing")
			}
		})
	}
}

func TestCORSPrefightFiltersHeadersAndSetsCachePolicy(t *testing.T) {
	middleware := MustCORS(
		WithOrigins("https://app.example"),
		WithHeaders("Content-Type", "X-Request-ID"),
		WithMethods(http.MethodGet, http.MethodPost),
		WithMaxAge(600),
	)
	called := false
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/resource", http.NoBody)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type, X-Not-Allowed, x-request-id")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", response.Code)
	}
	if called {
		t.Fatal("preflight unexpectedly called the next handler")
	}
	if got, want := response.Header().Get("Access-Control-Allow-Headers"), "content-type, x-request-id"; got != want {
		t.Fatalf("allowed headers: got %q, want %q", got, want)
	}
	if got, want := response.Header().Get("Access-Control-Allow-Methods"), "GET, POST"; got != want {
		t.Fatalf("allowed methods: got %q, want %q", got, want)
	}
	if got := response.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("max age: got %q", got)
	}
	if !hasVary(response.Header(), "Origin") || !hasVary(response.Header(), "Access-Control-Request-Headers") {
		t.Fatalf("preflight Vary values missing: %v", response.Header().Values("Vary"))
	}
}

func TestCORSWildcardAndNoOrigin(t *testing.T) {
	middleware := MustCORS(WithOrigins("*"))
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	request.Header.Set("Origin", "https://any.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("wildcard allow-origin: got %q", got)
	}

	request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("origin-less request got allow-origin %q", got)
	}
}

func TestCORSWithEnv(t *testing.T) {
	t.Setenv("CORS_ORIGINS", "https://app.example")
	t.Setenv("CORS_HEADERS", "Content-Type,X-Request-ID")
	t.Setenv("CORS_METHODS", "GET,POST")
	t.Setenv("CORS_EXPOSED", "X-Trace-ID")
	t.Setenv("CORS_MAX_AGE", "120")
	t.Setenv("CORS_CREDENTIALS", "true")
	middleware := MustCORS(WithEnv())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/", http.NoBody)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "content-type")
	response := httptest.NewRecorder()
	middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(response, request)
	if response.Header().Get("Access-Control-Allow-Credentials") != "true" || response.Header().Get("Access-Control-Max-Age") != "120" {
		t.Fatalf("environment configuration was not applied: %v", response.Header())
	}
}

func hasVary(header http.Header, want string) bool {
	for _, value := range header.Values("Vary") {
		for item := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), want) {
				return true
			}
		}
	}
	return false
}

func TestCORSOrdinaryOptionsReachesNextHandler(t *testing.T) {
	middleware := MustCORS(WithOrigins("https://app.example"))
	called := false
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/resource", http.NoBody)
	request.Header.Set("Origin", "https://app.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusAccepted {
		t.Fatalf("ordinary OPTIONS was intercepted: called=%v status=%d", called, response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Fatalf("ordinary OPTIONS got preflight methods %q", got)
	}
}
