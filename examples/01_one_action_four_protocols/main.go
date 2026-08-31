package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/bus"
	"github.com/nexssp/transport/cron"
	"github.com/nexssp/transport/tbus"
	"github.com/nexssp/transport/tcli"
	"github.com/nexssp/transport/thttp"
)

type OrderReq struct {
	ProductID string `json:"product_id" cli:"product,p"`
	Quantity  int    `json:"quantity" cli:"quantity,q"`
}

type OrderRes struct {
	OrderID string `json:"order_id"`
	Total   int    `json:"total"`
}

func createOrder(_ context.Context, req OrderReq) (OrderRes, error) {
	if req.ProductID == "" {
		req.ProductID = "AUTO_RECURRING"
	}
	if req.Quantity <= 0 {
		req.Quantity = 1
	}

	res := OrderRes{
		OrderID: "ORD-2026-" + req.ProductID,
		Total:   req.Quantity * 199,
	}

	fmt.Printf("📦 [Action Executed] Product: %-14s | Qty: %d | Generated: %-18s | Total: $%d\n",
		req.ProductID, req.Quantity, res.OrderID, res.Total)

	return res, nil
}

func main() {
	// Graceful shutdown na Ctrl+C (kod wyjścia 0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	eventBus := bus.New[any]()

	orderAction := action.New("order.create", createOrder).
		Description("Creates a new customer order").
		Route(thttp.POST("/orders")).
		Route(tcli.Command("order:create", "Create order")).
		Route(tbus.Topic("order.create")).
		Route(cron.Every(5 * time.Second)).
		Build()

	// ─── 1. HTTP Server ──────────────────────────────────────────────────
	httpServer := thttp.New(":8080")
	httpServer.Mount([]action.AnyAction{orderAction})
	go func() {
		_, _ = httpServer.Do(ctx, nil)
	}()

	// ─── 2. CLI Runner ───────────────────────────────────────────────────
	cli := tcli.New(tcli.WithArgs("order:create", "-p=CLI_1", "-q=3"))
	cli.Mount([]action.AnyAction{orderAction})
	fmt.Println("💻 [1/4] Testing CLI Trigger:")
	_, _ = cli.Do(ctx, nil)

	// ─── 3. In-Memory Event Bus ──────────────────────────────────────────
	busTransport := tbus.New(eventBus)
	busTransport.Mount([]action.AnyAction{orderAction})
	fmt.Println("\n⚡ [2/4] Testing In-Memory Bus Trigger:")
	_ = busTransport.Publish(ctx, "order.create", OrderReq{ProductID: "BUS_1", Quantity: 5})

	// ─── 4. Cron Scheduler ───────────────────────────────────────────────
	cronTransport := cron.New()
	cronTransport.Mount([]action.AnyAction{orderAction})
	go func() {
		_, _ = cronTransport.Do(ctx, nil)
	}()
	fmt.Println("\n⏰ [3/4] Cron Scheduler active (auto-triggers every 5s)...")

	// ─── Instrukcja dla HTTP (CURL) ──────────────────────────────────────
	fmt.Println("\n🌐 [4/4] HTTP Server listening on :8080")
	fmt.Println("👉 Test HTTP in another terminal:")
	fmt.Println("   curl -X POST localhost:8080/orders -H \"Content-Type: application/json\" -d \"{\\\"product_id\\\":\\\"WEB_1\\\",\\\"quantity\\\":2}\"")
	fmt.Println("\n✨ All 4 protocols active. Press Ctrl+C to exit cleanly.")

	<-ctx.Done()
	fmt.Println("\n🛑 Application closed cleanly.")
}
