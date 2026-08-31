// Package transport defines the universal boundary interface for Nexss applications.
// Concrete adapters (thttp, tcli, tbus, cron, tworker) translate incoming network or CLI
// invocations into typed action.Executable dispatches.
package transport

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
)

// Standard transport-level correlation headers and keys.
const (
	HeaderRequestID   = "X-Request-ID"
	HeaderExecutionID = "X-Execution-ID"
	HeaderTraceID     = "X-Trace-ID"
	HeaderSpanID      = "X-Span-ID"
)

// Transport defines the universal lifecycle and routing contract for protocol listeners.
type Transport interface {
	fmt.Stringer

	// CanHandle reports whether this transport can process the given routing binding.
	CanHandle(b action.Binding) bool

	// Mount registers actions into the transport's internal routing table.
	Mount(actions []action.AnyAction)

	// Do starts the transport listener loop and blocks until ctx is canceled.
	Do(ctx context.Context, v any) (any, error)
}

// MapToHTTPStatus converts an xerr.Kind to its corresponding standard HTTP status code.
func MapToHTTPStatus(k xerr.Kind) int {
	switch k {
	case xerr.KindBadRequest, xerr.KindValidation:
		return http.StatusBadRequest
	case xerr.KindUnauthorized:
		return http.StatusUnauthorized
	case xerr.KindForbidden:
		return http.StatusForbidden
	case xerr.KindNotFound:
		return http.StatusNotFound
	case xerr.KindConflict:
		return http.StatusConflict
	case xerr.KindMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case xerr.KindRateLimit, xerr.KindTooManyRequests:
		return http.StatusTooManyRequests
	case xerr.KindTimeout:
		return http.StatusGatewayTimeout
	case xerr.KindUnavailable, xerr.KindDatabase, xerr.KindCircuitBreaker:
		return http.StatusServiceUnavailable
	case xerr.KindCanceled:
		return 499 // Client Closed Request
	default:
		return http.StatusInternalServerError
	}
}
