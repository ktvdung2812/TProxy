package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestMuseUsageWindows(t *testing.T) {
	windows := museUsageWindows(map[string]any{
		"subscription_usage_snapshot": map[string]any{
			"session": map[string]any{"used_percent": 30, "window_duration_mins": 300, "resets_at": "2026-01-01T12:00:00Z"},
			"weekly":  map[string]any{"used_percent": 10, "window_duration_mins": 10080, "resets_at": "2026-01-08T00:00:00Z"},
		},
	}, 0)
	if len(windows) != 2 {
		t.Fatalf("windows = %v", windows)
	}
	if session := windows["session"]; session.Used != 30 || session.Remaining != 70 || session.Total != 100 || session.WindowMinutes != 300 {
		t.Fatalf("session window = %+v", session)
	}
	if weekly := windows["weekly"]; weekly.WindowMinutes != 10080 || weekly.ResetAt == "" {
		t.Fatalf("weekly window = %+v", weekly)
	}
}

func TestMuseQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/muse-code/key" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer dca:stored" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"api_key":        "LLM|fresh",
			"subs_tier_name": "Muse Pro",
			"is_subs_active": true,
			"usage":          map[string]any{"session": map[string]any{"used_percent": 42, "window_duration_mins": 300, "resets_at": "2026-01-01T12:00:00Z"}},
		})
	}))
	defer server.Close()
	registry := NewRegistry()
	registry.client = server.Client()
	quota, err := registry.CredentialQuota(context.Background(),
		store.Provider{ID: "p1", Type: "muse", BaseURL: server.URL + "/v1"},
		store.Credential{ID: "c1", Secret: "LLM|fresh", OAuthToken: &store.OAuthToken{Extra: map[string]any{"dca_token": "dca:stored"}}})
	if err != nil {
		t.Fatalf("CredentialQuota: %v", err)
	}
	if quota.Plan != "Muse Pro" {
		t.Fatalf("plan = %q", quota.Plan)
	}
	if session := quota.Quotas["session"]; session.Used != 42 || session.Remaining != 58 || session.WindowMinutes != 300 {
		t.Fatalf("session quota = %+v", session)
	}
}

func TestMuseQuotaAPIKeyCredentialHasNoSubscriptionSurface(t *testing.T) {
	registry := NewRegistry()
	quota, err := registry.CredentialQuota(context.Background(),
		store.Provider{ID: "p1", Type: "muse", BaseURL: "https://api.meta.ai/v1"},
		store.Credential{ID: "c1", Secret: "mk-plain-api-key"})
	if err != nil {
		t.Fatalf("CredentialQuota: %v", err)
	}
	if quota.Message == "" || len(quota.Quotas) != 0 {
		t.Fatalf("quota = %+v", quota)
	}
}
