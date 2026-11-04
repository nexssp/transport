package tbus_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler/contracts"

	"github.com/nexssp/transport/bus"
	"github.com/nexssp/transport/tbus"
)

func TestDoSignalsReadyAfterMount(t *testing.T) {
	transport := tbus.New(bus.New[any]())
	signal := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), signal))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := transport.Do(ctx, nil)
		done <- err
	}()
	select {
	case <-signal.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("bus transport did not signal readiness")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bus transport did not stop after cancellation")
	}
}

func TestDoCanRestartAfterCancellation(t *testing.T) {
	transport := tbus.New(bus.New[any]())

	for run := 1; run <= 2; run++ {
		t.Run(fmt.Sprintf("run_%d", run), func(t *testing.T) {
			signal := contracts.NewReadySignal()
			ctx, cancel := context.WithCancel(contracts.WithServiceReady(context.Background(), signal))
			done := make(chan error, 1)
			go func() {
				_, err := transport.Do(ctx, nil)
				done <- err
			}()

			select {
			case <-signal.Done():
			case <-time.After(2 * time.Second):
				cancel()
				t.Fatal("bus transport did not signal readiness")
			}

			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Do error = %v, want context.Canceled", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("bus transport did not stop after cancellation")
			}
		})
	}
}
