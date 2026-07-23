package tunnel

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func ProbeURLAlive(ctx context.Context, baseURL string) bool {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: healthFetchTimeout * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func WaitForHealth(ctx context.Context, baseURL string, cancelled func() bool) error {
	deadline := time.Now().Add(healthCheckTimeout * time.Second)
	for time.Now().Before(deadline) {
		if cancelled != nil && cancelled() {
			return fmt.Errorf("tunnel cancelled")
		}
		if ProbeURLAlive(ctx, baseURL) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(healthCheckInterval * time.Second):
		}
	}
	return fmt.Errorf("health check timeout after %ds", healthCheckTimeout)
}
