package tworker

import (
	"context"
	"testing"
	"time"
)

func TestOptionsContextRoundTrip(t *testing.T) {
	want := Options{
		PollIntervalOverride: 250 * time.Millisecond,
		Backoff: func(attempt int) time.Duration {
			return time.Duration(attempt) * time.Second
		},
		OnStatus: func(WorkerStatus) {},
	}
	got := OptionsFromContext(WithOptions(context.Background(), want))
	if got.PollIntervalOverride != want.PollIntervalOverride || got.Backoff == nil ||
		got.Backoff(3) != 3*time.Second || got.OnStatus == nil {
		t.Fatalf("options from context = %+v, want configured values", got)
	}
}
