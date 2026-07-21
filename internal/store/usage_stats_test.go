package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/security"
)

func TestUsageDisplayModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		publicModelID string
		upstreamModel string
		providerID    string
		want          string
	}{
		{publicModelID: "openai-compatible-9rlv:gpt-5.6-sol", upstreamModel: "gpt-5.6-sol", providerID: "openai-compatible-9rlv", want: "gpt-5.6-sol"},
		{publicModelID: "openai-compatible-9rlv:gpt-5.5", upstreamModel: "", providerID: "openai-compatible-9rlv", want: "gpt-5.5"},
		{publicModelID: "codex-gpt-5.6-sol", upstreamModel: "gpt-5.6-sol", providerID: "openai-compatible-9rlv", want: "gpt-5.6-sol"},
		{publicModelID: "codex-gpt-5.6-sol", upstreamModel: "", providerID: "codex", want: "codex-gpt-5.6-sol"},
	}
	for _, test := range tests {
		if got := usageDisplayModel(test.publicModelID, test.upstreamModel, test.providerID); got != test.want {
			t.Fatalf("usageDisplayModel(%q, %q, %q)=%q want %q", test.publicModelID, test.upstreamModel, test.providerID, got, test.want)
		}
	}
}

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
		{RequestID: "r1", PublicModelID: "gpt-test", ProviderID: "openai", UpstreamModel: "gpt-4o", CredentialID: "cred-1", ClientAPIKeyID: "key-1", Status: 200, InputTokens: 10, OutputTokens: 5, CachedTokens: 2, TokensSaved: 99, EstimatedCostUSD: 0.01, CreatedAt: now},
		{RequestID: "r2", PublicModelID: "gpt-test", ProviderID: "openai", UpstreamModel: "gpt-4o", CredentialID: "cred-1", ClientAPIKeyID: "key-1", Status: 200, InputTokens: 20, OutputTokens: 8, CachedTokens: 1, TokensSaved: 50, EstimatedCostUSD: 0.02, CreatedAt: now.Add(time.Minute)},
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
	if len(stats.ByCredential) != 1 {
		t.Fatalf("ByCredential = %+v", stats.ByCredential)
	}
	credentialUsage := stats.ByCredential["cred-1"]
	if credentialUsage.Requests != 2 || credentialUsage.PromptTokens != 30 || credentialUsage.CompletionTokens != 13 {
		t.Fatalf("ByCredential[cred-1] = %+v", credentialUsage)
	}
	if len(stats.RecentRequests) != 2 {
		t.Fatalf("RecentRequests = %+v", stats.RecentRequests)
	}
}

func TestUsageStatsStripsProviderModelPrefix(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "usage-stats-prefix.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	if err := dataStore.AddUsage(ctx, UsageEvent{
		RequestID: "prefix-1", PublicModelID: "openai-compatible-9rlv:gpt-5.6-sol",
		ProviderID: "openai-compatible-9rlv", UpstreamModel: "gpt-5.6-sol", Status: 200,
		InputTokens: 10, OutputTokens: 5, EstimatedCostUSD: 0.01, CreatedAt: now,
	}); err != nil {
		t.Fatalf("AddUsage() error = %v", err)
	}

	stats, err := dataStore.UsageStats(ctx, now.Add(-time.Hour), UsageLookupMaps{
		ProviderNames: map[string]string{"openai-compatible-9rlv": "Virouter"},
	})
	if err != nil {
		t.Fatalf("UsageStats() error = %v", err)
	}
	for _, entry := range stats.ByModel {
		if entry.RawModel != "gpt-5.6-sol" {
			t.Fatalf("ByModel rawModel = %q, want gpt-5.6-sol (%+v)", entry.RawModel, stats.ByModel)
		}
	}
	if stats.RecentRequests[0].Model != "gpt-5.6-sol" {
		t.Fatalf("RecentRequests model = %q, want gpt-5.6-sol", stats.RecentRequests[0].Model)
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

func TestCredentialUsageByPeriod(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "credential-usage.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	now := time.Date(2026, 7, 17, 15, 30, 0, 0, time.UTC)
	events := []UsageEvent{
		{RequestID: "r1", ProviderID: "openai", CredentialID: "cred-1", Status: 200, InputTokens: 10, OutputTokens: 5, CreatedAt: now},
		{RequestID: "r2", ProviderID: "openai", CredentialID: "cred-2", Status: 200, InputTokens: 3, OutputTokens: 2, CreatedAt: now},
		{RequestID: "r3", ProviderID: "openai", CredentialID: "cred-1", Status: 200, InputTokens: 7, OutputTokens: 4, CreatedAt: now.Add(26 * time.Hour)},
	}
	for _, event := range events {
		if err := dataStore.AddUsage(ctx, event); err != nil {
			t.Fatalf("AddUsage() error = %v", err)
		}
	}

	allUsage, err := dataStore.CredentialUsageByPeriod(ctx, time.Time{})
	if err != nil {
		t.Fatalf("CredentialUsageByPeriod(all) error = %v", err)
	}
	if allUsage["cred-1"].Requests != 2 || allUsage["cred-1"].PromptTokens != 17 || allUsage["cred-1"].CompletionTokens != 9 {
		t.Fatalf("all usage[cred-1] = %+v", allUsage["cred-1"])
	}
	if allUsage["cred-2"].Requests != 1 {
		t.Fatalf("all usage[cred-2] = %+v", allUsage["cred-2"])
	}

	nextDay := now.Add(26 * time.Hour)
	nextDayUsage, err := dataStore.CredentialUsageByPeriod(ctx, usageStartOfDayUTC(nextDay))
	if err != nil {
		t.Fatalf("CredentialUsageByPeriod(next day) error = %v", err)
	}
	if nextDayUsage["cred-1"].Requests != 1 || nextDayUsage["cred-1"].PromptTokens != 7 {
		t.Fatalf("next day usage[cred-1] = %+v", nextDayUsage["cred-1"])
	}
	if _, ok := nextDayUsage["cred-2"]; ok {
		t.Fatalf("next day usage should not include cred-2: %+v", nextDayUsage)
	}
}
