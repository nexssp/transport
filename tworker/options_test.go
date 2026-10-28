package tworker

import (
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
)

func TestNewUsesKernelExponentialBackoffByDefault(t *testing.T) {
	got := New().options.Backoff
	want := action.ExponentialBackoff(time.Second, 30*time.Second)
	for _, attempt := range []int{1, 2, 3, 4, 5, 6, 10, 31} {
		if gotDelay, wantDelay := got(attempt), want(attempt); gotDelay != wantDelay {
			t.Errorf("default backoff attempt %d = %s, want Kernel policy %s", attempt, gotDelay, wantDelay)
		}
	}
}
