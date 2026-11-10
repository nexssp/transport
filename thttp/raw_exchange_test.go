package thttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRawHTTPResponseWriteToPreservesStatusHeadersCookiesAndBody(t *testing.T) {
	originalHeader := http.Header{
		"Content-Type": {"text/plain; charset=utf-8"},
		"Location":     {"https://accounts.example.test/authorize?code=private-code&state=private-state"},
		"Set-Cookie":   {"session=private-session; Path=/; HttpOnly", "csrf=private-csrf; Path=/"},
		"X-Exact":      {"preserved"},
	}
	response := NewRawHTTPResponse(http.StatusSeeOther, originalHeader, []byte("raw-response-body"))
	originalHeader.Set("X-Exact", "mutated")

	writer := httptest.NewRecorder()
	if err := response.WriteTo(writer); err != nil {
		t.Fatal(err)
	}
	if writer.Code != http.StatusSeeOther {
		t.Fatalf("status=%d, want 303", writer.Code)
	}
	if got := writer.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type=%q", got)
	}
	if got := writer.Header().Get("Location"); got != "https://accounts.example.test/authorize?code=private-code&state=private-state" {
		t.Errorf("Location=%q", got)
	}
	if got := writer.Header().Values("Set-Cookie"); len(got) != 2 || got[0] != "session=private-session; Path=/; HttpOnly" || got[1] != "csrf=private-csrf; Path=/" {
		t.Errorf("Set-Cookie values=%q", got)
	}
	if got := writer.Header().Get("X-Exact"); got != "preserved" {
		t.Errorf("X-Exact=%q, want original copied value", got)
	}
	if got := writer.Body.String(); got != "raw-response-body" {
		t.Errorf("body=%q, want exact unwrapped bytes", got)
	}
}

func TestRawHTTPResponseNilHeaderBody(t *testing.T) {
	writer := httptest.NewRecorder()
	response := NewRawHTTPResponse(http.StatusNoContent, nil, nil)
	if err := response.WriteTo(writer); err != nil {
		t.Fatal(err)
	}
	if writer.Code != http.StatusNoContent || writer.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q, want 204 with empty body", writer.Code, writer.Body.String())
	}
	if writer.Header().Get("Content-Type") != "" || writer.Header().Get("Content-Length") != "" {
		t.Fatalf("unexpected body headers: %v", writer.Header())
	}
}

func TestRawHTTPResponseOverridesTransportHeaders(t *testing.T) {
	writer := httptest.NewRecorder()
	writer.Header().Set("X-Request-ID", "transport-request-id")
	response := NewRawHTTPResponse(http.StatusOK, http.Header{"X-Request-ID": {"action-request-id"}}, nil)
	if err := response.WriteTo(writer); err != nil {
		t.Fatal(err)
	}
	if got := writer.Header().Get("X-Request-ID"); got != "action-request-id" {
		t.Fatalf("X-Request-ID=%q, want action value to override transport value", got)
	}
}

func TestRawHTTPResponseRejectsInvalidStatusAndHeadersBeforeWriting(t *testing.T) {
	cases := []struct {
		name     string
		response RawHTTPResponse
	}{
		{name: "informational status", response: NewRawHTTPResponse(http.StatusSwitchingProtocols, nil, nil)},
		{name: "invalid header name", response: NewRawHTTPResponse(http.StatusOK, http.Header{"Bad Header": {"value"}}, nil)},
		{name: "header injection", response: NewRawHTTPResponse(http.StatusOK, http.Header{"X-Test": {"safe\r\nX-Evil: yes"}}, nil)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			if err := test.response.WriteTo(writer); err == nil {
				t.Fatal("WriteTo() succeeded for invalid response")
			}
			if writer.Code != http.StatusOK || writer.Body.Len() != 0 || len(writer.Header()) != 0 {
				t.Fatalf("invalid response wrote output: code=%d headers=%v body=%q", writer.Code, writer.Header(), writer.Body.String())
			}
		})
	}
}

func TestRawHTTPRequestAndResponseDiagnosticsRedactSecrets(t *testing.T) {
	const (
		password = "private-password-7e5d"
		cookie   = "private-cookie-58b2"
		code     = "private-oauth-code-412f"
		state    = "private-oauth-state-6a2c"
		csrf     = "private-csrf-82fd"
	)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/callback?code="+code+"&state="+state, strings.NewReader(password))
	request.Header.Set("Cookie", "session="+cookie)
	request.Header.Set("Authorization", "Bearer "+csrf)
	ctx := WithRawHTTPRequest(context.Background(), request)
	wrapped, ok := RawHTTPRequestFromContext(ctx)
	if !ok || wrapped.Request() != request {
		t.Fatal("raw request context did not return the original request")
	}
	response := NewRawHTTPResponse(http.StatusSeeOther, http.Header{
		"Location":   {"https://app.example.test/callback?code=" + code + "&state=" + state},
		"Set-Cookie": {"session=" + cookie},
	}, []byte(password))

	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logger.Info("raw exchange", "request", wrapped, "response", response)
	requestJSON, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := strings.Join([]string{
		wrapped.String(),
		fmt.Sprintf("%+v", wrapped),
		fmt.Sprintf("%#v", wrapped),
		string(requestJSON),
		response.String(),
		fmt.Sprintf("%+v", response),
		fmt.Sprintf("%#v", response),
		string(responseJSON),
		output.String(),
	}, "\n")
	for _, secret := range []string{password, cookie, code, state, csrf} {
		if strings.Contains(diagnostics, secret) {
			t.Errorf("diagnostic output disclosed %q: %s", secret, diagnostics)
		}
	}
	if !strings.Contains(diagnostics, "redacted") {
		t.Errorf("diagnostics do not identify redaction: %s", diagnostics)
	}
}
