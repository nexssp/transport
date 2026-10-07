package identity_test

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/nexssp/kernel/xctx"

	"github.com/nexssp/transport/identity"
)

type headers map[string]string

func (h headers) Header(name string) string    { return h[name] }
func (h headers) SetHeader(name, value string) { h[name] = value }

func TestPopulateAndPropagateRoundTrip(t *testing.T) {
	input := headers{
		identity.HeaderRequestID:   "req-1",
		identity.HeaderExecutionID: "exec-1",
		identity.HeaderTraceID:     "trace-1",
		identity.HeaderSpanID:      "span-1",
		identity.HeaderTenantID:    "tenant-1",
		identity.HeaderUserID:      "user-1",
		identity.HeaderClientIP:    "192.0.2.10",
		identity.HeaderApproval:    "approval-1",
	}
	ctx := identity.Populate(context.Background(), input)
	output := headers{}
	identity.Propagate(ctx, output)
	for name, want := range input {
		if got := output[name]; got != want {
			t.Errorf("header %s: got %q, want %q", name, got, want)
		}
	}
}

func TestPopulateGeneratesCorrelationIDsOnce(t *testing.T) {
	ctx := identity.Populate(context.Background(), headers{})
	requestID := xctx.RequestIDFrom(ctx)
	traceID := xctx.TraceIDFrom(ctx)
	if len(requestID) != 32 || len(traceID) != 32 {
		t.Fatalf("generated ID lengths: request=%d trace=%d, want 32", len(requestID), len(traceID))
	}
	for name, value := range map[string]string{"request": requestID, "trace": traceID} {
		if _, err := hex.DecodeString(value); err != nil {
			t.Errorf("%s ID is not hex: %v", name, err)
		}
	}
	if requestID == traceID {
		t.Fatal("request and trace IDs unexpectedly match")
	}
	if xctx.TenantIDFrom(ctx) != "" || xctx.UserIDFrom(ctx) != "" || xctx.ApprovalTokenFrom(ctx) != "" {
		t.Fatal("identity fields were fabricated for an empty source")
	}
}

func TestPopulatePreservesExistingCorrelationIDs(t *testing.T) {
	ctx := xctx.WithRequestID(context.Background(), "request-from-parent")
	ctx = xctx.WithTraceID(ctx, "trace-from-parent")
	ctx = identity.Populate(ctx, headers{})
	if got := xctx.RequestIDFrom(ctx); got != "request-from-parent" {
		t.Fatalf("request ID: got %q", got)
	}
	if got := xctx.TraceIDFrom(ctx); got != "trace-from-parent" {
		t.Fatalf("trace ID: got %q", got)
	}
}

func TestNewIDIsUniqueAndEncoded(t *testing.T) {
	first, second := identity.NewID(), identity.NewID()
	if first == second {
		t.Fatal("generated IDs are not unique")
	}
	if len(first) != 32 {
		t.Fatalf("ID length: got %d, want 32", len(first))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("ID is not hex: %v", err)
	}
}

func TestNilAdaptersAreSafe(t *testing.T) {
	ctx := identity.Populate(context.Background(), nil)
	if ctx == nil {
		t.Fatal("Populate returned a nil context")
	}
	identity.Propagate(ctx, nil)
	identity.Propagate(context.Background(), headers{})
	identity.PropagateCorrelation(context.Background(), nil)
}

func TestPropagateCorrelationOmitsSensitiveValues(t *testing.T) {
	ctx := xctx.WithRequestID(context.Background(), "req-1")
	ctx = xctx.WithTenantID(ctx, "tenant-secret")
	ctx = xctx.WithUserID(ctx, "user-secret")
	output := headers{}
	identity.PropagateCorrelation(ctx, output)
	if output[identity.HeaderRequestID] != "req-1" {
		t.Fatalf("request ID not propagated: %#v", output)
	}
	if _, ok := output[identity.HeaderTenantID]; ok {
		t.Fatal("tenant ID propagated by correlation-only helper")
	}
	if _, ok := output[identity.HeaderUserID]; ok {
		t.Fatal("user ID propagated by correlation-only helper")
	}
}
