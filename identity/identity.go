// Package identity provides helpers for reading and propagating transport identity.
//
// Populate and Propagate accept generic adapters so protocol packages do not need
// to depend on one another. Sources used for tenant, user, client-IP, or approval
// values must be trusted: these values can affect authorization and must not be
// copied from an unauthenticated client without validation. The HTTP integration
// intentionally handles only correlation headers by default.
package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/nexssp/kernel/xctx"
)

const (
	HeaderRequestID   = "X-Request-ID"
	HeaderExecutionID = "X-Execution-ID"
	HeaderTraceID     = "X-Trace-ID"
	HeaderSpanID      = "X-Span-ID"
	HeaderTenantID    = "X-Tenant-ID"
	HeaderUserID      = "X-User-ID"
	HeaderClientIP    = "X-Client-IP"
	HeaderApproval    = "X-Approval"
)

// Source reads a value from an incoming message.
type Source interface {
	Header(name string) string
}

// Writer writes a value to an outgoing message.
type Writer interface {
	SetHeader(name, value string)
}

// Populate reads identity values from src into ctx. Missing request and trace
// IDs are generated only when they are also absent from ctx. Other values are
// copied only when present in src; this function never fabricates them. Use a
// trusted Source for tenant, user, client-IP, and approval values.
func Populate(ctx context.Context, src Source) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if src == nil {
		return ctx
	}

	requestID := src.Header(HeaderRequestID)
	if requestID == "" {
		requestID = xctx.RequestIDFrom(ctx)
	}
	if requestID == "" {
		requestID = NewID()
	}
	ctx = xctx.WithRequestID(ctx, requestID)

	if value := src.Header(HeaderExecutionID); value != "" {
		ctx = xctx.WithExecutionID(ctx, value)
	}

	traceID := src.Header(HeaderTraceID)
	if traceID == "" {
		traceID = xctx.TraceIDFrom(ctx)
	}
	if traceID == "" {
		traceID = NewID()
	}
	ctx = xctx.WithTraceID(ctx, traceID)

	if value := src.Header(HeaderSpanID); value != "" {
		ctx = xctx.WithSpanID(ctx, value)
	}
	if value := src.Header(HeaderTenantID); value != "" {
		ctx = xctx.WithTenantID(ctx, value)
	}
	if value := src.Header(HeaderUserID); value != "" {
		ctx = xctx.WithUserID(ctx, value)
	}
	if value := src.Header(HeaderClientIP); value != "" {
		ctx = xctx.WithClientIP(ctx, value)
	}
	if value := src.Header(HeaderApproval); value != "" {
		ctx = xctx.WithApprovalToken(ctx, value)
	}
	return ctx
}

// Propagate writes every non-empty identity value in ctx to dst. Callers should
// choose an appropriate destination policy; HTTP responses use
// PropagateCorrelation to avoid reflecting authorization-sensitive values.
func Propagate(ctx context.Context, dst Writer) {
	if ctx == nil || dst == nil {
		return
	}
	propagate(ctx, dst, false)
}

// PropagateCorrelation writes only request, execution, trace, and span IDs.
// These are suitable for automatic inclusion in HTTP response headers.
func PropagateCorrelation(ctx context.Context, dst Writer) {
	if ctx == nil || dst == nil {
		return
	}
	propagate(ctx, dst, true)
}

func propagate(ctx context.Context, dst Writer, correlationOnly bool) {
	if value := xctx.RequestIDFrom(ctx); value != "" {
		dst.SetHeader(HeaderRequestID, value)
	}
	if value := xctx.ExecutionIDFrom(ctx); value != "" {
		dst.SetHeader(HeaderExecutionID, value)
	}
	if value := xctx.TraceIDFrom(ctx); value != "" {
		dst.SetHeader(HeaderTraceID, value)
	}
	if value := xctx.SpanIDFrom(ctx); value != "" {
		dst.SetHeader(HeaderSpanID, value)
	}
	if correlationOnly {
		return
	}
	if value := xctx.TenantIDFrom(ctx); value != "" {
		dst.SetHeader(HeaderTenantID, value)
	}
	if value := xctx.UserIDFrom(ctx); value != "" {
		dst.SetHeader(HeaderUserID, value)
	}
	if value := xctx.ClientIPFrom(ctx); value != "" {
		dst.SetHeader(HeaderClientIP, value)
	}
	if value := xctx.ApprovalTokenFrom(ctx); value != "" {
		dst.SetHeader(HeaderApproval, value)
	}
}

// NewID returns a 128-bit cryptographically random identifier encoded as 32
// lowercase hexadecimal characters. Failure to obtain cryptographic randomness
// is unrecoverable for this helper and panics rather than returning a weak ID.
func NewID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("transport/identity: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}
