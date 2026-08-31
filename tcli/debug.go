package tcli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nexssp/kernel/action"
)

// AddDebugCommands returns standard operational CLI commands for probes and network tests.
func AddDebugCommands() []action.AnyAction {
	return []action.AnyAction{
		action.New("healthcheck", func(ctx context.Context, _ struct{}) (string, error) {
			c := &http.Client{Timeout: 2 * time.Second}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8080/health", http.NoBody)
			if err != nil {
				return "", fmt.Errorf("build healthcheck request failed: %w", err)
			}
			resp, err := c.Do(req)
			if err != nil {
				return "", fmt.Errorf("healthcheck request failed: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return "", fmt.Errorf("unhealthy status: %d", resp.StatusCode)
			}
			return "ok", nil
		}).
			Description("Docker & Kubernetes container health probe").
			Route(Command("-healthcheck", "Docker health probe")).
			Build(),

		action.New("host-check", func(ctx context.Context, url string) (string, error) {
			if url == "" {
				return "", fmt.Errorf("target URL cannot be empty")
			}
			c := &http.Client{Timeout: 5 * time.Second}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
			if err != nil {
				return "", fmt.Errorf("build host-check request failed: %w", err)
			}
			resp, err := c.Do(req)
			if err != nil {
				return "", fmt.Errorf("host-check request failed: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return "", fmt.Errorf("HTTP error: %d", resp.StatusCode)
			}
			return fmt.Sprintf("OK (HTTP %d)", resp.StatusCode), nil
		}).
			Description("Debug utility: Check URL connectivity").
			Route(Command("-host-check", "Debug: Check URL connectivity")).
			Build(),
	}
}
