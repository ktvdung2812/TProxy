package api

import (
	"context"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

func TestOperationalAlertsDeduplicateAndResolve(t *testing.T) {
	ctx := context.Background()
	data := apiTestStore(t, &config.Config{
		Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", Enabled: true, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none"}}}},
		Models:    []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "m"}}}},
	})
	s := &Server{store: data}
	if err := data.SetCredentialEnabled(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	metrics := []store.OperationalMetric{{ProviderID: "p", ModelID: "m", CredentialID: "a", Requests: 20, Errors: 4}}
	first, err := s.evaluateOperationalAlerts(ctx, metrics)
	if err != nil || len(first) != 2 {
		t.Fatalf("expected error-rate and unavailable-route alerts: %+v %v", first, err)
	}
	s.operations.evaluatedAt = time.Time{}
	second, err := s.evaluateOperationalAlerts(ctx, metrics)
	if err != nil || len(second) != 2 {
		t.Fatalf("deduplication: %+v %v", second, err)
	}
	for i := range first {
		if first[i].ID != second[i].ID || !first[i].FirstSeen.Equal(second[i].FirstSeen) || second[i].LastSeen.Before(first[i].LastSeen) {
			t.Fatalf("alert identity changed: %+v -> %+v", first[i], second[i])
		}
	}
	if err := data.SetCredentialEnabled(ctx, "a", true); err != nil {
		t.Fatal(err)
	}
	s.operations.evaluatedAt = time.Time{}
	resolved, err := s.evaluateOperationalAlerts(ctx, nil)
	if err != nil || len(resolved) != 0 {
		t.Fatalf("resolved alerts remain active: %+v %v", resolved, err)
	}
}
