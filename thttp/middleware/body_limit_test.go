package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestBodyLimitRejectsOversizedDeclaredBody(t *testing.T) {
	called := false
	mw := RequestBodyLimit(4)
	handler := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/upload", strings.NewReader("12345"))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
	if called {
		t.Fatal("handler ran for a body larger than the configured limit")
	}
}

func TestRequestBodyLimitCapsUnknownLengthBody(t *testing.T) {
	mw := RequestBodyLimit(4)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != "1234" {
			t.Errorf("body = %q, want %q", body, "1234")
		}
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/upload", strings.NewReader("12345"))
	request.ContentLength = -1
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestRequestBodyLimitLeavesNonPositiveLimitUnchanged(t *testing.T) {
	called := false
	handler := RequestBodyLimit(0)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/upload", strings.NewReader("large")))

	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("called = %v, status = %d", called, response.Code)
	}
}
