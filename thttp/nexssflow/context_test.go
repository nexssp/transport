package nexssflow

import (
	"context"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
)

func TestRuntimeConfigContextRoundTrip(t *testing.T) {
	want := RuntimeConfig{
		Addr:              "127.0.0.1:9090",
		ReadHeaderTimeout: 2 * time.Second,
		ActionFilter:      func(_ action.AnyAction) bool { return true },
	}
	got := runtimeConfigFromContext(WithRuntimeConfig(context.Background(), want))
	if got.Addr != want.Addr || got.ReadHeaderTimeout != want.ReadHeaderTimeout || got.ActionFilter == nil {
		t.Fatalf("runtime config from context = %+v, want configured values", got)
	}
}
