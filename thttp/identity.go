package thttp

import (
	"context"
	"net/http"
	"strings"

	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/transport/identity"
)

type httpSource struct {
	header  http.Header
	traceID string
	spanID  string
}

// Header implements identity.Source. HTTP ingress accepts only correlation
// headers by default; tenant, user, client-IP, and approval identity must be
// established by trusted authentication or proxy middleware.
func (s httpSource) Header(name string) string {
	switch name {
	case identity.HeaderRequestID, identity.HeaderExecutionID:
		return s.header.Get(name)
	case identity.HeaderTraceID:
		if value := s.header.Get(name); value != "" {
			return value
		}
		return s.traceID
	case identity.HeaderSpanID:
		if value := s.header.Get(name); value != "" {
			return value
		}
		return s.spanID
	default:
		return ""
	}
}

type httpWriter struct{ header http.Header }

func (w httpWriter) SetHeader(name, value string) { w.header.Set(name, value) }

// PopulateFromRequest reads correlation IDs from an HTTP request and returns a
// context containing them. It uses a valid W3C traceparent as a fallback for
// X-Trace-ID and X-Span-ID. Existing context IDs are retained when the request
// does not provide a value. Authorization-sensitive headers are not trusted.
func PopulateFromRequest(ctx context.Context, r *http.Request) context.Context {
	if r == nil {
		return ctx
	}
	traceID, spanID, ok := parseTraceparent(r.Header.Get("traceparent"))
	if !ok {
		traceID, spanID = "", ""
	}
	return identity.Populate(ctx, httpSource{header: r.Header, traceID: traceID, spanID: spanID})
}

// PropagateToResponse writes correlation IDs to the response. Sensitive
// identity values are intentionally not reflected to HTTP clients.
func PropagateToResponse(ctx context.Context, w http.ResponseWriter) {
	if w == nil {
		return
	}
	identity.PropagateCorrelation(ctx, httpWriter{header: w.Header()})
}

// RequestIdentity adds a pooled request scope, provisions or propagates
// correlation IDs, and records the URL path as the endpoint. It is installed
// automatically by Transport.Handler and can also be used on standalone HTTP
// handlers. It does not trust tenant/user/client-IP/approval headers.
func RequestIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		release := func() {}
		if xctx.ScopeFrom(ctx) == nil {
			ctx, _, release = xctx.NewScope(ctx)
		}
		defer release()

		ctx = PopulateFromRequest(ctx, r)
		if xctx.EndpointFrom(ctx) == "" {
			ctx = xctx.WithEndpoint(ctx, r.URL.Path)
		}
		PropagateToResponse(ctx, w)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// parseTraceparent accepts the version 00 W3C traceparent form:
// 00-<32 lowercase hex trace ID>-<16 lowercase hex parent ID>-<2 hex flags>.
// The all-zero trace and parent IDs are forbidden by the specification.
func parseTraceparent(value string) (traceID, spanID string, ok bool) {
	if len(value) != 55 || value[0:2] != "00" || value[2] != '-' || value[35] != '-' || value[52] != '-' {
		return "", "", false
	}
	traceID, spanID = value[3:35], value[36:52]
	if !lowerHex(traceID) || !lowerHex(spanID) || !hexPair(value[53:55]) {
		return "", "", false
	}
	if allZero(traceID) || allZero(spanID) {
		return "", "", false
	}
	return traceID, spanID, true
}

func lowerHex(value string) bool {
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return value != ""
}

func hexPair(value string) bool {
	if len(value) != 2 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}

func allZero(value string) bool { return strings.Trim(value, "0") == "" }
