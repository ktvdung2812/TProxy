package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
)

func TestOperationalUsageRoundTripAndPercentiles(t *testing.T) {
	data, err := OpenSQLite(filepath.Join(t.TempDir(), "metrics.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	now := time.Now()
	ctx := context.Background()
	for i := 1; i <= 20; i++ {
		ttft := int64(i)
		item := UsageEvent{ProviderID: "p", PublicModelID: "m", CredentialID: "a", Status: 200, Attempt: 1, LatencyMS: int64(i * 10), QueueMS: 5, InputTokens: 100, OutputTokens: 20, CachedTokens: 40, CacheCreationTokens: 10, CreatedAt: now, RoutingReason: "latency-aware"}
		if i <= 10 {
			item.TTFTMS = &ttft
		}
		if i == 20 {
			item.Status = 503
			item.Attempt = 2
		}
		if err = data.AddUsage(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	metrics, err := data.OperationalMetrics(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 {
		t.Fatal(metrics)
	}
	m := metrics[0]
	if m.P95MS != 190 || m.TTFTP95MS == nil || *m.TTFTP95MS != 10 || m.Requests != 20 || m.Errors != 1 || m.Fallbacks != 1 || m.CacheCreationTokens != 200 || m.InputTokens != 2000 {
		t.Fatalf("metrics: %+v", m)
	}
	items, err := data.RecentUsage(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].TTFTMS != nil || items[0].CacheCreationTokens != 10 || items[0].QueueMS != 5 || items[0].RoutingReason != "latency-aware" {
		t.Fatalf("roundtrip: %+v", items[0])
	}
	stats, err := data.UsageStats(ctx, now.Add(-time.Minute), UsageLookupMaps{})
	if err != nil || stats.TotalCacheCreationTokens != 200 || stats.ByProvider["p"].CacheCreationTokens != 200 {
		t.Fatalf("aggregation: %+v %v", stats, err)
	}
	for _, code := range []string{"provider_concurrency_limit", "account_queue_full", "account_queue_timeout", "account_unavailable"} {
		if err = data.AddUsage(ctx, UsageEvent{ProviderID: "local", Status: 429, ErrorCode: code, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	metrics, err = data.OperationalMetrics(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range metrics {
		if metric.ProviderID == "local" && (metric.Errors != 0 || metric.TTFTP95MS != nil) {
			t.Fatalf("local limits counted as upstream errors: %+v", metric)
		}
	}
}

func TestAdmissionPolicyAndStaleQuotaPreserveManualDisable(t *testing.T) {
	data, err := OpenSQLite(filepath.Join(t.TempDir(), "policy.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	ctx := context.Background()
	if err = data.SaveProvider(ctx, config.ProviderConfig{ID: "p", Type: "openai-compatible", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = data.SaveCredential(ctx, "p", config.CredentialConfig{ID: "a", AuthType: "none", Metadata: map[string]any{"note": "keep", "sub2api_concurrency": 2, "sub2api_expires_at": time.Now().Add(time.Hour).Unix()}}); err != nil {
		t.Fatal(err)
	}
	stale, _ := data.CredentialByID(ctx, "a")
	if CredentialAdmission(stale.Metadata).MaxConcurrent != 2 || CredentialAdmission(stale.Metadata).ExpiresAt == "" || AccountExpired(stale, time.Now()) {
		t.Fatal("imported admission not honored")
	}
	if err = data.SaveCredential(ctx, "p", config.CredentialConfig{ID: "a", AuthType: "none", Metadata: map[string]any{"max_concurrent_requests": 3}}); err != nil {
		t.Fatal(err)
	}
	updated, _ := data.CredentialByID(ctx, "a")
	if updated.Metadata["note"] != "keep" {
		t.Fatal("partial update clobbered metadata")
	}
	if _, err = data.SyncCredentialQuotaState(ctx, updated, true); err != nil {
		t.Fatal(err)
	}
	auto, _ := data.CredentialByID(ctx, "a")
	if err = data.SetCredentialEnabled(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	if changed, err := data.SyncCredentialQuotaState(ctx, auto, false); err != nil || changed {
		t.Fatalf("stale probe re-enabled manually disabled account: %v %v", changed, err)
	}
	if changed, err := data.SyncCredentialQuotaState(ctx, stale, true); err != nil || changed {
		t.Fatalf("stale probe took ownership: %v %v", changed, err)
	}
	updated, _ = data.CredentialByID(ctx, "a")
	if updated.Enabled || QuotaAutoDisabled(updated.Metadata) {
		t.Fatal("manual disable lost")
	}
	updated.Enabled = true
	updated.Status = "healthy"
	updated.Metadata["account_expires_at"] = time.Now().Add(-time.Hour).Format(time.RFC3339)
	if len(EligibleCredentials([]Credential{updated}, time.Now())) != 0 {
		t.Fatal("expired account eligible")
	}
	for _, value := range []any{-1, 1.5, 10001, "oops"} {
		if ValidateAdmissionMetadata(map[string]any{"max_concurrent_requests": value}) == nil {
			t.Fatalf("invalid limit accepted: %v", value)
		}
	}
}
