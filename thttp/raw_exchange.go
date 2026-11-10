package thttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// RawHTTPRoute binds a Flow action to a route that receives its original HTTP
// request through RawHTTPRequestFromContext and must return RawHTTPResponse.
// Unlike HTTPRoute, Transport does not decode the body or JSON-encode the result.
type RawHTTPRoute struct {
	Method string
	Path   string
}

func (route RawHTTPRoute) String() string {
	return route.Method + " " + route.Path + " (raw action)"
}

// RawRoute constructs a method/path binding for a raw Flow HTTP action.
func RawRoute(method, path string) RawHTTPRoute {
	return RawHTTPRoute{Method: method, Path: path}
}

// RawHTTPRequest is an opaque, redacted view of the live request supplied to a
// raw route action. Request returns the original request, including its host-
// middleware context, body, headers, cookies, and query. Never log that request.
type RawHTTPRequest struct {
	request *http.Request
}

// WithRawHTTPRequest stores a request in ctx for a raw route action.
func WithRawHTTPRequest(ctx context.Context, request *http.Request) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if request == nil {
		return ctx
	}
	return context.WithValue(ctx, rawHTTPRequestContextKey{}, RawHTTPRequest{request: request})
}

// RawHTTPRequestFromContext returns the live request wrapper from a raw route.
func RawHTTPRequestFromContext(ctx context.Context) (RawHTTPRequest, bool) {
	if ctx == nil {
		return RawHTTPRequest{}, false
	}
	request, ok := ctx.Value(rawHTTPRequestContextKey{}).(RawHTTPRequest)
	return request, ok && request.request != nil
}

// Request returns the original HTTP request for handler logic. The wrapper's
// formatting and serialization deliberately redact all request data.
func (request RawHTTPRequest) Request() *http.Request {
	return request.request
}

func (RawHTTPRequest) String() string           { return "thttp.RawHTTPRequest{redacted}" }
func (request RawHTTPRequest) GoString() string { return request.String() }
func (request RawHTTPRequest) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, request.String())
}

func (RawHTTPRequest) MarshalJSON() ([]byte, error) {
	return []byte(`{"redacted":true}`), nil
}

func (RawHTTPRequest) LogValue() slog.Value {
	return slog.StringValue("[redacted HTTP request]")
}

type rawHTTPRequestContextKey struct{}

// RawHTTPResponse carries the exact status, headers, and body to write for a raw
// route. Its formatting and serialization never disclose headers or body bytes.
type RawHTTPResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// NewRawHTTPResponse returns an owned copy of the response headers and body.
func NewRawHTTPResponse(statusCode int, header http.Header, body []byte) RawHTTPResponse {
	return RawHTTPResponse{
		StatusCode: statusCode,
		Header:     header.Clone(),
		Body:       append([]byte(nil), body...),
	}
}

func (RawHTTPResponse) String() string            { return "thttp.RawHTTPResponse{redacted}" }
func (response RawHTTPResponse) GoString() string { return response.String() }
func (response RawHTTPResponse) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, response.String())
}

func (RawHTTPResponse) MarshalJSON() ([]byte, error) {
	return []byte(`{"redacted":true}`), nil
}

func (RawHTTPResponse) LogValue() slog.Value {
	return slog.StringValue("[redacted HTTP response]")
}

// WriteTo writes the raw response without encoding or wrapping its body.
// Headers supplied by the action replace same-named transport headers. The
// transport does not calculate Content-Length; callers that provide it are
// responsible for matching the body they return.
func (response RawHTTPResponse) WriteTo(writer http.ResponseWriter) error {
	if writer == nil {
		return errors.New("thttp: raw HTTP response writer is nil")
	}
	if err := response.validate(); err != nil {
		return err
	}

	headers := writer.Header()
	for name, values := range response.Header {
		headers.Del(name)
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotModified {
		return nil
	}
	_, err := writer.Write(response.Body)
	return err
}

func validRawHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func validRawHeaderValue(value string) bool {
	for i := range len(value) {
		if value[i] == '\t' {
			continue
		}
		if value[i] < 0x20 || value[i] == 0x7f {
			return false
		}
	}
	return true
}

var (
	_ json.Marshaler = RawHTTPRequest{}
	_ slog.LogValuer = RawHTTPRequest{}
	_ json.Marshaler = RawHTTPResponse{}
	_ slog.LogValuer = RawHTTPResponse{}
)

func (response RawHTTPResponse) validate() error {
	if response.StatusCode < http.StatusOK || response.StatusCode > 599 {
		return errors.New("thttp: raw HTTP response status is invalid")
	}
	for name, values := range response.Header {
		if !validRawHeaderName(name) {
			return errors.New("thttp: raw HTTP response header name is invalid")
		}
		for _, value := range values {
			if !validRawHeaderValue(value) {
				return errors.New("thttp: raw HTTP response header value is invalid")
			}
		}
	}
	return nil
}
