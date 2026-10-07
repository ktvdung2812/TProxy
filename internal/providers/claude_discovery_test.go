package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func claudeDiscoveryRegistry(t *testing.T, handler http.HandlerFunc) (*Registry, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	registry := NewRegistry()
	registry.client = server.Client()
	return registry, server
}

// The dashboard's "N models available on this account" catalogue is only as
// fresh as whatever claudeStaticModelEntries happens to hard-code. Discovery
// must prefer Anthropic's live Models API, dressed in the Claude Code
// fingerprint, so new releases show up without a code change.
func TestDiscoverClaudeModelsPrefersLiveCatalogue(t *testing.T) {
	var authorization, beta, version string
	registry, server := claudeDiscoveryRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		beta = r.Header.Get("anthropic-beta")
		version = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","type":"model"},
			{"id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5","type":"model"}
		]}`))
	})

	provider := store.Provider{ID: "claude", Type: "claude", BaseURL: server.URL}
	credential := store.Credential{ID: "cred-1", AuthType: "oauth", Secret: "sk-ant-oat01-abc"}

	models, err := registry.DiscoverModels(context.Background(), provider, credential)
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}

	if authorization != "Bearer sk-ant-oat01-abc" {
		t.Errorf("Authorization = %q", authorization)
	}
	if !strings.Contains(beta, "oauth-2025-04-20") || !strings.Contains(beta, "claude-code-20250219") {
		t.Errorf("anthropic-beta = %q, want the Claude Code fingerprint betas", beta)
	}
	if version != claudeAnthropicVersion {
		t.Errorf("anthropic-version = %q", version)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v, want 2 live entries", models)
	}
	if models[0].ID != "claude-opus-5-5" || models[0].Name != "Claude Opus 5.5" {
		t.Errorf("models[0] = %+v", models[0])
	}
}

// A credential that can still serve inference must not lose its catalogue
// just because the live probe failed (expired token, rate limit, network
// blip) — the dashboard should fall back to the static list, not show zero
// models.
func TestDiscoverClaudeModelsFallsBackOnFailure(t *testing.T) {
	registry, server := claudeDiscoveryRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	provider := store.Provider{ID: "claude", Type: "claude", BaseURL: server.URL}
	credential := store.Credential{ID: "cred-1", AuthType: "oauth", Secret: "sk-ant-oat01-abc"}

	models, err := registry.DiscoverModels(context.Background(), provider, credential)
	if err != nil {
		t.Fatalf("discovery returned an error instead of falling back: %v", err)
	}

	want := claudeStaticModelEntries(provider)
	if len(models) != len(want) {
		t.Fatalf("models = %d entries, want the static fallback (%d entries)", len(models), len(want))
	}
	if models[0].ID != want[0].ID {
		t.Errorf("models[0].ID = %q, want %q", models[0].ID, want[0].ID)
	}
}
