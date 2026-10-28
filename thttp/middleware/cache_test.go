package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersPreserveApplicationCSPPolicy(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	cases := []struct {
		name     string
		url      string
		wantHSTS string
	}{
		{name: "plain HTTP", url: "http://example.test/"},
		{name: "TLS", url: "https://example.test/", wantHSTS: "max-age=31536000; includeSubDomains"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.url, http.NoBody)
			SecurityHeaders(next).ServeHTTP(recorder, request)
			for header, want := range map[string]string{
				"X-Content-Type-Options":     "nosniff",
				"X-Frame-Options":            "DENY",
				"Referrer-Policy":            "strict-origin-when-cross-origin",
				"Cross-Origin-Opener-Policy": "same-origin",
				"Strict-Transport-Security":  tc.wantHSTS,
			} {
				if got := recorder.Header().Get(header); got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}
			if got := recorder.Header().Get("Content-Security-Policy"); got != "" {
				t.Errorf("SecurityHeaders set application-owned CSP to %q", got)
			}
		})
	}
}

func TestNoStoreAPIResponsesIsScopedToAPIPaths(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/api/health", want: "no-store"},
		{path: "/health", want: ""},
		{path: "/api", want: ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, http.NoBody)
			NoStoreAPIResponses(next).ServeHTTP(recorder, request)
			if got := recorder.Header().Get("Cache-Control"); got != tc.want {
				t.Errorf("Cache-Control = %q, want %q", got, tc.want)
			}
		})
	}
}
