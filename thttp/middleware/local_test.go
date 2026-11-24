package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoopbackOnlyRequiresLoopbackPeerHostAndSameOrigin(t *testing.T) {
	called := 0
	handler := LoopbackOnly(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		called++
		writer.WriteHeader(http.StatusNoContent)
	}))
	for _, test := range []struct {
		name       string
		remoteAddr string
		host       string
		origin     string
		wantStatus int
	}{
		{name: "local same origin", remoteAddr: "127.0.0.1:1234", host: "127.0.0.1:8080", origin: "http://127.0.0.1:8080", wantStatus: http.StatusNoContent},
		{name: "no origin", remoteAddr: "[::1]:1234", host: "localhost:8080", wantStatus: http.StatusNoContent},
		{name: "remote peer", remoteAddr: "192.0.2.1:1234", host: "127.0.0.1:8080", wantStatus: http.StatusForbidden},
		{name: "wildcard host", remoteAddr: "127.0.0.1:1234", host: "0.0.0.0:8080", wantStatus: http.StatusForbidden},
		{name: "cross-site origin", remoteAddr: "127.0.0.1:1234", host: "127.0.0.1:8080", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
		{name: "host mismatch", remoteAddr: "127.0.0.1:1234", host: "localhost:8080", origin: "http://127.0.0.1:8080", wantStatus: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+test.host+"/", http.NoBody)
			request.RemoteAddr = test.remoteAddr
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
	if called != 2 {
		t.Fatalf("next handler called %d times, want 2", called)
	}
}

func TestMaxConcurrentRequestsRejectsExcessWithoutWaiting(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan int, 1)
	handler := MaxConcurrentRequests(1)(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		writer.WriteHeader(http.StatusNoContent)
	}))

	go func() {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		firstDone <- response.Code
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not enter handler")
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	if second.Code != http.StatusServiceUnavailable || second.Header().Get("Retry-After") != "1" {
		t.Fatalf("second response = %d, Retry-After %q; want 503, 1", second.Code, second.Header().Get("Retry-After"))
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
