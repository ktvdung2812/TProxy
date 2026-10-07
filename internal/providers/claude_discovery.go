package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

// claudeModelsPath is Anthropic's public Models API. It is undocumented for
// Claude Code OAuth credentials, but dressed in the same fingerprint the
// inference path already sends (anthropic-beta, User-Agent, Stainless
// headers), Anthropic authorizes it the same way it authorizes /v1/messages.
// This lets the catalog track new model releases automatically instead of
// waiting on a manual update to claudeStaticModelEntries.
const claudeModelsPath = "/v1/models"

type claudeModelsResponse struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Type        string `json:"type"`
	} `json:"data"`
}

// discoverClaudeModels returns the live model catalog for a Claude Code
// credential, falling back to the static list on any failure. The fallback is
// silent (no error returned) on purpose: a credential that can still serve
// inference must not be cooled down just because the catalog probe failed, and
// the dashboard should never show fewer models than it did before this probe
// existed.
func discoverClaudeModels(ctx context.Context, registry *Registry, provider store.Provider, credential store.Credential) ([]DiscoveredModel, error) {
	if items, ok := fetchClaudeModelsLive(ctx, registry, provider, credential); ok {
		return items, nil
	}
	return claudeStaticModelEntries(provider), nil
}

func fetchClaudeModelsLive(ctx context.Context, registry *Registry, provider store.Provider, credential store.Credential) ([]DiscoveredModel, bool) {
	ctx = withClaudeTransport(withCredentialProxy(ctx, credential), provider, credential)
	headers := authHeaders(provider, credential)
	headers.Set("anthropic-version", claudeAnthropicVersion)
	headers.Set("anthropic-beta", strings.Join(claudeDefaultBetas, ","))
	headers.Set("User-Agent", claudeUserAgent())
	headers.Set("Accept", "application/json")

	base := strings.TrimRight(provider.BaseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}

	body, status, err := registry.quotaGET(ctx, base+claudeModelsPath+"?limit=1000", headers)
	if err != nil || status != http.StatusOK {
		return nil, false
	}

	var payload claudeModelsResponse
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Data) == 0 {
		return nil, false
	}

	items := make([]DiscoveredModel, 0, len(payload.Data))
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" || (model.Type != "" && model.Type != "model") {
			continue
		}
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = id
		}
		items = append(items, DiscoveredModel{
			ID:           id,
			Name:         name,
			OwnedBy:      "anthropic",
			Capabilities: discoveryCapabilities(registry, provider, provider.Type, id),
		})
	}
	if len(items) == 0 {
		return nil, false
	}
	return items, true
}
