package thttp_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"

	"github.com/nexssp/transport/identity"
	"github.com/nexssp/transport/thttp"
)

func TestRequestIdentityGeneratesAndPropagatesOneRequestID(t *testing.T) {
	server := thttp.New("")
	act := action.New("identity.echo", func(ctx context.Context, _ struct{}) (map[string]string, error) {
		return map[string]string{
			"request_id": xctx.RequestIDFrom(ctx),
			"trace_id":   xctx.TraceIDFrom(ctx),
		}, nil
	}).Route(thttp.GET("/identity")).Build()
	server.Mount([]action.AnyAction{act})

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/identity", http.NoBody)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d, body %s", response.Code, response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	requestID := response.Header().Get(identity.HeaderRequestID)
	traceID := response.Header().Get(identity.HeaderTraceID)
	if len(requestID) != 32 || len(traceID) != 32 {
		t.Fatalf("generated IDs: request=%q trace=%q", requestID, traceID)
	}
	if _, err := hex.DecodeString(requestID); err != nil {
		t.Fatalf("request ID is not hex: %v", err)
	}
	if body["request_id"] != requestID {
		t.Fatalf("request ID mismatch: response header %q, action context %q", requestID, body["request_id"])
	}
	if body["trace_id"] != traceID {
		t.Fatalf("trace ID mismatch: response header %q, action context %q", traceID, body["trace_id"])
	}
}

func TestRequestIdentityUsesValidTraceparentAndExplicitHeadersWin(t *testing.T) {
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	server := thttp.New("")
	server.Mux().HandleFunc("GET /trace", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(xctx.TraceIDFrom(r.Context()) + ":" + xctx.SpanIDFrom(r.Context())))
	})

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/trace", http.NoBody)
	request.Header.Set("Traceparent", traceparent)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if got, want := response.Body.String(), "4bf92f3577b34da6a3ce929d0e0e4736:00f067aa0ba902b7"; got != want {
		t.Fatalf("trace context: got %q, want %q", got, want)
	}
	if response.Header().Get(identity.HeaderTraceID) != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatal("valid traceparent trace ID was not propagated")
	}

	request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/trace", http.NoBody)
	request.Header.Set("Traceparent", traceparent)
	request.Header.Set(identity.HeaderTraceID, "explicit-trace")
	request.Header.Set(identity.HeaderSpanID, "explicit-span")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if got, want := response.Body.String(), "explicit-trace:explicit-span"; got != want {
		t.Fatalf("explicit headers should win: got %q, want %q", got, want)
	}
}

func TestRequestIdentityRejectsMalformedTraceparent(t *testing.T) {
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	invalid := []string{
		"",
		valid + "00",
		"01" + valid[2:],
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"00-4bf92f3577b34da6a3ce929d0e0e473g-00f067aa0ba902b7-01",
		"00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-0g",
	}
	for _, value := range invalid {
		t.Run(strings.ReplaceAll(value, "/", "_"), func(t *testing.T) {
			server := thttp.New("")
			server.Mux().HandleFunc("GET /trace", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(xctx.TraceIDFrom(r.Context()) + ":" + xctx.SpanIDFrom(r.Context())))
			})
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/trace", http.NoBody)
			if value != "" {
				request.Header.Set("Traceparent", value)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			parts := strings.Split(response.Body.String(), ":")
			if len(parts) != 2 || parts[0] == "" || parts[1] != "" {
				t.Fatalf("invalid traceparent should not set a span ID: %q", response.Body.String())
			}
		})
	}
}

func TestRequestIdentityDoesNotTrustAuthorizationHeaders(t *testing.T) {
	server := thttp.New("")
	server.Mux().HandleFunc("GET /identity", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(xctx.TenantIDFrom(r.Context()) + ":" + xctx.UserIDFrom(r.Context()) + ":" + xctx.ApprovalTokenFrom(r.Context())))
	})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/identity", http.NoBody)
	request.Header.Set(identity.HeaderTenantID, "attacker-tenant")
	request.Header.Set(identity.HeaderUserID, "attacker-user")
	request.Header.Set(identity.HeaderApproval, "attacker-approval")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if got := response.Body.String(); got != "::" {
		t.Fatalf("untrusted identity was populated: %q", got)
	}
	for _, name := range []string{identity.HeaderTenantID, identity.HeaderUserID, identity.HeaderApproval} {
		if got := response.Header().Get(name); got != "" {
			t.Errorf("untrusted header %s was reflected: %q", name, got)
		}
	}
}

func TestHTTPErrorUsesSameRequestIDAsResponseHeader(t *testing.T) {
	server := thttp.New("")
	act := action.New("identity.fail", func(context.Context, struct{}) (any, error) {
		return nil, errors.New("expected test failure")
	}).Route(thttp.GET("/fail")).Build()
	server.Mount([]action.AnyAction{act})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/fail", http.NoBody)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var body struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if header := response.Header().Get(identity.HeaderRequestID); header == "" || header != body.RequestID {
		t.Fatalf("request ID mismatch: header %q, error body %q", header, body.RequestID)
	}
}

func TestStandaloneHTTPAdapterUsesGeneratedRequestID(t *testing.T) {
	act := action.New("adapter.fail", func(context.Context, struct{}) (map[string]string, error) {
		return nil, errors.New("expected adapter failure")
	}).Build()
	handler := thttp.HTTP[struct{}, map[string]string](act)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var body struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if requestID := response.Header().Get(identity.HeaderRequestID); requestID == "" || requestID != body.RequestID {
		t.Fatalf("request ID mismatch: header %q, error body %q", requestID, body.RequestID)
	}
}
