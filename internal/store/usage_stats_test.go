package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/security"
)

func TestUsageStatsAggregatesByModelAndAccount(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "usage-stats.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	now := time.Date(2026, 7, 17, 15, 30, 0, 0, time.UTC)

	events := []UsageEvent{
		{RequestID: "r1", PublicModelID: "gpt-test", ProviderID: "openai", UpstreamModel: "gpt-4o", CredentialID: "cred-1", ClientAPIKeyID: "key-1", Status: 200, InputTokens: 10, OutputTokens: 5, TokensSaved: 2, EstimatedCostUSD: 0.01, CreatedAt: now},
		{RequestID: "r2", PublicModelID: "gpt-test", ProviderID: "openai", UpstreamModel: "gpt-4o", CredentialID: "cred-1", ClientAPIKeyID: "key-1", Status: 200, InputTokens: 20, OutputTokens: 8, TokensSaved: 1, EstimatedCostUSD: 0.02, CreatedAt: now.Add(time.Minute)},
	}
	for _, event := range events {
		if err := dataStore.AddUsage(ctx, event); err != nil {
			t.Fatalf("AddUsage() error = %v", err)
		}
	}

	stats, err := dataStore.UsageStats(ctx, now.Add(-time.Hour), UsageLookupMaps{
		ProviderNames:  map[string]string{"openai": "OpenAI"},
		CredentialName: map[string]string{"cred-1": "Primary account"},
		APIKeyNames:    map[string]string{"key-1": "Dev key"},
	})
	if err != nil {
		t.Fatalf("UsageStats() error = %v", err)
	}
	if stats.TotalRequests != 2 {
		t.Fatalf("TotalRequests = %d, want 2", stats.TotalRequests)
	}
	if stats.TotalPromptTokens != 30 || stats.TotalCompletionTokens != 13 || stats.TotalCachedTokens != 3 {
		t.Fatalf("token totals = %+v", stats)
	}
	if stats.TotalCost < 0.029 || stats.TotalCost > 0.031 {
		t.Fatalf("TotalCost = %v", stats.TotalCost)
	}
	if len(stats.ByModel) != 1 {
		t.Fatalf("ByModel = %+v", stats.ByModel)
	}
	if len(stats.ByAccount) != 1 {
		t.Fatalf("ByAccount = %+v", stats.ByAccount)
	}
	if len(stats.RecentRequests) != 2 {
		t.Fatalf("RecentRequests = %+v", stats.RecentRequests)
	}
}

func TestUsageChartDailyBuckets(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "usage-chart.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	day := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	if err := dataStore.AddUsage(ctx, UsageEvent{RequestID: "chart-1", PublicModelID: "m1", ProviderID: "p1", Status: 200, InputTokens: 100, OutputTokens: 50, EstimatedCostUSD: 0.5, CreatedAt: day}); err != nil {
		t.Fatalf("AddUsage() error = %v", err)
	}

	points, err := dataStore.UsageChart(ctx, "7d", day)
	if err != nil {
		t.Fatalf("UsageChart() error = %v", err)
	}
	if len(points) == 0 {
		t.Fatal("expected chart points")
	}
	found := false
	for _, point := range points {
		if point.Tokens == 150 && point.Cost >= 0.49 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("chart points = %+v", points)
	}
}

func TestUsagePeriodSince(t *testing.T) {
	now := time.Date(2026, 7, 17, 15, 30, 0, 0, time.UTC)
	since, err := UsagePeriodSince("today", now)
	if err != nil {
		t.Fatalf("UsagePeriodSince() error = %v", err)
	}
	if !since.Equal(usageStartOfDayUTC(now)) {
		t.Fatalf("today since = %v", since)
	}
	if _, err := UsagePeriodSince("bad", now); err == nil {
		t.Fatal("expected invalid period error")
	}
}
