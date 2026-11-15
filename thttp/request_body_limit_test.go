package thttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"
)

func TestServerRequestBodyLimitRejectsLargeBodyBeforeRawHandler(t *testing.T) {
	called := false
	server := New("", WithMaxRequestBodyBytes(4))
	server.Mux().Handle("POST /raw", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/raw", strings.NewReader("12345"))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
	if called {
		t.Fatal("raw handler ran for an oversized declared body")
	}
}

func TestServerRequestBodyLimitReports413ForChunkedTypedRequest(t *testing.T) {
	server := New("", WithMaxRequestBodyBytes(4))
	act := action.New("typed.body", func(context.Context, map[string]any) (struct{}, error) {
		return struct{}{}, nil
	}).Route(POST("/typed")).Build()
	server.Mount([]action.AnyAction{act})

	request := httptest.NewRequest(http.MethodPost, "/typed", strings.NewReader(`{"x":1}`))
	request.ContentLength = -1
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
}
