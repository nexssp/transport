package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport/tcli"
	"github.com/nexssp/transport/thttp"
)

type BroadcastReq struct {
	Message string `json:"message" cli:"message,m" validate:"required"`
}

func main() {
	broadcaster := thttp.NewBroadcaster(100)

	// Broadcast Action mounted on both HTTP (POST /broadcast) and CLI (`broadcast` command)
	broadcastAction := action.New("dashboard.broadcast", func(_ context.Context, req BroadcastReq) (action.MessageRes, error) {
		if req.Message == "" {
			return action.MessageRes{}, xerr.BadRequest("message is required")
		}
		broadcaster.Publish("dashboard", []byte(req.Message))
		return action.MessageRes{Message: "Event broadcasted"}, nil
	}).
		Route(thttp.POST("/broadcast")).
		Route(tcli.Command("broadcast", "Broadcast a message").WithAliases("b")).
		Build()

	sseAction := action.New[any, any]("dashboard.sse", nil).
		Route(thttp.Stream("/events").WithChannel("dashboard")).
		Build()

	// Handle CLI invocations
	if len(os.Args) > 1 && os.Args[1] != "server" {
		cli := tcli.New(tcli.WithArgs(os.Args[1:]...))
		cli.Mount([]action.AnyAction{broadcastAction})
		if _, err := cli.Do(context.Background(), nil); err != nil {
			log.Fatal(err)
		}
		return
	}

	// Server mode
	httpServer := thttp.New(":8080", thttp.WithBroadcaster(broadcaster))
	httpServer.Mount([]action.AnyAction{broadcastAction, sseAction})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		if _, err := httpServer.Do(ctx, nil); err != nil {
			log.Fatal(err)
		}
	}()

	// Periodic ticker emitting SSE metrics
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				data, _ := json.Marshal(map[string]any{
					"time":  time.Now().Format(time.RFC3339),
					"value": rand.IntN(100),
				})
				broadcaster.Publish("dashboard", data)
			}
		}
	}()

	log.Println("🌐 Dashboard server running on http://localhost:8080")
	log.Println("   Open http://localhost:8080/events in your browser")
	log.Println("   Try: go run . broadcast --message 'Hello from CLI!'")
	<-ctx.Done()
}
