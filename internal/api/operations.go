package api

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

type operationalAlert struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	ResourceID string    `json:"resource_id"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
}

type operationalState struct {
	mu          sync.Mutex
	alerts      map[string]operationalAlert
	evaluatedAt time.Time
}

func (s *Server) evaluateOperationalAlerts(ctx context.Context, metrics []store.OperationalMetric) ([]operationalAlert, error) {
	s.operations.mu.Lock()
	defer s.operations.mu.Unlock()
	now := time.Now()
	if time.Since(s.operations.evaluatedAt) >= 30*time.Second {
		active := map[string]operationalAlert{}
		add := func(kind, id string) {
			key := kind + ":" + id
			alert, ok := s.operations.alerts[key]
			if !ok {
				alert = operationalAlert{ID: key, Kind: kind, ResourceID: id, FirstSeen: now}
			}
			alert.LastSeen = now
			active[key] = alert
		}
		for _, metric := range metrics {
			if metric.Requests >= 20 && float64(metric.Errors)/float64(metric.Requests) >= .2 {
				add("high_error_rate", metric.ProviderID+" / "+metric.ModelID+" / "+metric.CredentialID)
			}
		}
		snapshot, err := s.store.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		available := map[string]bool{}
		for _, provider := range snapshot.Providers {
			credentials, err := s.store.Credentials(ctx, provider.ID)
			if err != nil {
				return nil, err
			}
			available[provider.ID] = provider.Enabled && len(store.EligibleCredentials(credentials, now)) > 0
			for _, credential := range credentials {
				if store.QuotaAutoDisabled(credential.Metadata) {
					add("quota_exhausted", credential.ID)
				}
				if credential.Status == "auth_required" || credential.LastErrorCode == "oauth_refresh_failed" {
					add("authentication_failed", credential.ID)
				}
				if credential.Enabled && store.AccountExpired(credential, now) {
					add("account_expired", credential.ID)
				}
			}
		}
		for _, model := range snapshot.Models {
			if !model.Enabled || len(model.ComboItems) > 0 {
				continue
			}
			routes, err := s.store.Routes(ctx, model.ID)
			if err != nil {
				return nil, err
			}
			usable := false
			for _, route := range routes {
				if route.Enabled && available[route.ProviderID] {
					usable = true
					break
				}
			}
			if !usable {
				add("route_unavailable", model.ID)
			}
		}
		s.operations.alerts = active
		s.operations.evaluatedAt = now
	}
	items := make([]operationalAlert, 0, len(s.operations.alerts))
	for _, alert := range s.operations.alerts {
		items = append(items, alert)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *Server) adminOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method_not_allowed", "GET required", "")
		return
	}
	since := time.Now().Add(-5 * time.Minute)
	metrics, err := s.store.OperationalMetrics(r.Context(), since)
	if err != nil {
		writeError(w, 500, "metrics_failed", err.Error(), "")
		return
	}
	alerts, err := s.evaluateOperationalAlerts(r.Context(), metrics)
	if err != nil {
		writeError(w, 500, "alerts_failed", err.Error(), "")
		return
	}
	s.operations.mu.Lock()
	evaluatedAt := s.operations.evaluatedAt
	s.operations.mu.Unlock()
	writeJSON(w, 200, map[string]any{"since": since, "metrics": metrics, "accounts": s.router.AccountRuntime(), "alerts": alerts, "evaluated_at": evaluatedAt})
}

func (s *Server) runOperationalMonitor(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			metrics, err := s.store.OperationalMetrics(ctx, time.Now().Add(-5*time.Minute))
			if err == nil {
				_, _ = s.evaluateOperationalAlerts(ctx, metrics)
			}
		}
	}
}
