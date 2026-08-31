package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/cron"
	"github.com/nexssp/transport/tworker"
)

func main() {
	var jobRuns atomic.Int32
	var workerRuns atomic.Int32

	// 1. Cron Job: Runs on a precise schedule
	cronJob := action.New("cleanup.job", func(_ context.Context, _ struct{}) (string, error) {
		count := jobRuns.Add(1)
		fmt.Printf("⏰ [Cron] Executed cleanup cycle #%d at %s\n", count, time.Now().Format("15:04:05"))
		return "cleaned", nil
	}).Route(cron.Every(2 * time.Second)).Build()

	// 2. Background Worker: Continuous interval runner with auto-recovery and request scope
	bgWorker := action.New("sync.worker", func(_ context.Context, _ struct{}) (string, error) {
		count := workerRuns.Add(1)
		fmt.Printf("⚙️  [Worker] Processed telemetry batch #%d at %s\n", count, time.Now().Format("15:04:05"))
		return "synced", nil
	}).Route(tworker.Every(1 * time.Second)).Build()

	cronTransport := cron.New()
	cronTransport.Mount([]action.AnyAction{cronJob})

	workerTransport := tworker.New()
	workerTransport.Mount([]action.AnyAction{bgWorker})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		if _, err := cronTransport.Do(ctx, nil); err != nil {
			log.Printf("Cron transport error: %v", err)
		}
	}()
	go func() {
		if _, err := workerTransport.Do(ctx, nil); err != nil {
			log.Printf("Worker transport error: %v", err)
		}
	}()

	fmt.Println("🚀 Schedulers & Workers running. Press Ctrl+C to stop.")
	<-ctx.Done()
	fmt.Println("🛑 Graceful shutdown completed.")
}
