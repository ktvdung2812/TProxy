package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	// A user-owned hostname may still route to a different website or an access
	// login page. Only our health response proves that it reaches tproxy.
	var health struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health) == nil && health.Status == "ok" && health.Service == "tproxy"
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
