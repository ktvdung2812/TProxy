package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

// Antigravity publishes its catalogue through the same endpoint the quota
// tracker already calls. Discovery used to refuse outright, on the grounds that
// the catalogue was only reachable through a project-scoped generation request,
// which left the provider page reporting "0 upstream models" even while the
// quota badges for those very models rendered correctly beside it.
//
// The catalogue is per host as well as per account: the hosts in
// antigravityBaseURLs do not always list the same models (see
// antigravityShouldTryNextHost), so discovery asks every host and merges the
// answers. Asking only one hid whatever the other one carried.
const (
	antigravityModelsPath       = "/v1internal:fetchAvailableModels"
	antigravityDefaultModelsURL = "https://cloudcode-pa.googleapis.com" + antigravityModelsPath
)

// The value is a variable so tests can point discovery at a single local
// server; it is never reassigned at runtime.
var antigravityModelsURL = antigravityDefaultModelsURL

// antigravityInternalModels are scaffolding entries the IDE uses for editor
// features such as tab completion. They are not routable chat models, and
// Cloud Code does not always flag them with isInternal.
var antigravityInternalModels = map[string]struct{}{
	"chat_20706":                  {},
	"chat_23310":                  {},
	"tab_flash_lite_preview":      {},
	"tab_jump_flash_lite_preview": {},
	"gemini-2.5-flash-thinking":   {},
	"gemini-2.5-pro":              {},
}

// antigravityKnownModels supplies stable display names for the current
// account-facing Antigravity catalog when fetchAvailableModels omits a
// displayName. The upstream response remains authoritative for availability;
// this map never adds models that the signed-in account cannot use.
var antigravityKnownModels = map[string]string{
	"gemini-4-argon":             "Gemini 4 Argon",
	"gemini-4-argon-high":        "Gemini 4 Argon (High)",
	"gemini-4-argon-low":         "Gemini 4 Argon (Low)",
	"gemini-3.8-flash-high":      "Gemini 3.8 Flash (High)",
	"gemini-3.8-flash-medium":    "Gemini 3.8 Flash (Medium)",
	"gemini-3.8-flash-low":       "Gemini 3.8 Flash (Low)",
	"gemini-3.8-flash":           "Gemini 3.8 Flash",
	"gemini-3.8-flash-tiered":    "Gemini 3.8 Flash (Tiered)",
	"gemini-3.7-flash-high":      "Gemini 3.7 Flash (High)",
	"gemini-3.7-flash-medium":    "Gemini 3.7 Flash (Medium)",
	"gemini-3.7-flash-low":       "Gemini 3.7 Flash (Low)",
	"gemini-3.7-flash":           "Gemini 3.7 Flash",
	"gemini-3.6-flash-high":      "Gemini 3.6 Flash (High)",
	"gemini-3.6-flash-medium":    "Gemini 3.6 Flash (Medium)",
	"gemini-3.6-flash-low":       "Gemini 3.6 Flash (Low)",
	"gemini-3.6-flash":           "Gemini 3.6 Flash",
	"gemini-3-flash-agent":       "Gemini 3.5 Flash (High)",
	"gemini-3.5-flash-low":       "Gemini 3.5 Flash (Medium)",
	"gemini-3.5-flash-extra-low": "Gemini 3.5 Flash (Low)",
	"gemini-pro-agent":           "Gemini 3.1 Pro (High)",
	"gemini-3.1-pro-low":         "Gemini 3.1 Pro (Low)",
	"claude-sonnet-4-6":          "Claude Sonnet 4.6 (Thinking)",
	"claude-opus-4-6-thinking":   "Claude Opus 4.6 (Thinking)",
	"gpt-oss-120b-medium":        "GPT-OSS 120B (Medium)",
	"gemini-3-flash":             "Gemini 3 Flash",
	"gemini-3.1-flash-image":     "Gemini 3.1 Flash (Image)",
	"gemini-3-pro-agent":         "Gemini 3 Pro",
	"gemini-3-pro-image":         "Gemini 3 Pro Image",
}

func discoverAntigravityModels(ctx context.Context, r *Registry, provider store.Provider, credential store.Credential) ([]DiscoveredModel, error) {
	token := credentialAccessToken(credential)
	if token == "" {
		return nil, &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: "Antigravity credential has no access token; reconnect the OAuth account"}
	}
	body := map[string]any{}
	if projectID := antigravityProject(credential); projectID != "" {
		body["project"] = projectID
	}
	ctx = withCredentialProxy(ctx, credential)
	merged := map[string]any{}
	var lastErr error
	for _, target := range antigravityModelCatalogURLs(provider) {
		models, err := r.antigravityCatalog(ctx, target, token, body)
		if err != nil {
			// The token travels with the request, so another host would reject it
			// just the same.
			if Code(err) == "authorization_required" {
				return nil, err
			}
			lastErr = err
			continue
		}
		for modelID, entry := range models {
			if existing, ok := merged[modelID]; !ok || existing == nil {
				merged[modelID] = entry
			}
		}
	}
	if len(merged) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, &ProviderError{Code: "model_discovery_unsupported", Message: "Antigravity returned no models for this account"}
	}
	return antigravityDiscoveredModels(r, merged)
}

// antigravityCatalog fetches the models map from one host.
func (r *Registry) antigravityCatalog(ctx context.Context, target, token string, body map[string]any) (map[string]any, error) {
	payload, status, err := r.quotaPOST(ctx, target, antigravityQuotaHeaders(token), body)
	if err != nil {
		return nil, &ProviderError{Code: "model_discovery_failed", Err: err}
	}
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return nil, &ProviderError{Status: status, Code: "authorization_required", Message: "Antigravity rejected the model catalogue request; reconnect the OAuth account"}
	case status < 200 || status >= 300:
		return nil, &ProviderError{Status: status, Code: "model_discovery_failed", Message: "Antigravity model catalogue is unavailable"}
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "invalid_upstream_response", Err: err}
	}
	models, _ := data["models"].(map[string]any)
	return models, nil
}

// antigravityModelCatalogURLs lists the catalogue endpoint on every host
// generation may use. The test hook pins discovery to one server.
func antigravityModelCatalogURLs(provider store.Provider) []string {
	if antigravityModelsURL != antigravityDefaultModelsURL {
		return []string{antigravityModelsURL}
	}
	bases := antigravityBaseURLs(provider)
	urls := make([]string, 0, len(bases))
	for _, base := range bases {
		urls = append(urls, endpoint(base, antigravityModelsPath))
	}
	return urls
}

func antigravityDiscoveredModels(r *Registry, models map[string]any) ([]DiscoveredModel, error) {
	items := make([]DiscoveredModel, 0, len(models))
	for modelID, value := range models {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if _, internal := antigravityInternalModels[modelID]; internal {
			continue
		}
		entry, _ := value.(map[string]any)
		if entry == nil {
			// New catalog entries can be null while their metadata is still being
			// rolled out. The ID is enough to make the model selectable.
			entry = map[string]any{}
		}
		if isInternal, _ := entry["isInternal"].(bool); isInternal {
			continue
		}
		// A plan that does not include a model gets a disabled placeholder under
		// a different ID ("gemini-4-argon-disabled", tagged "Unavailable") so the
		// App can show an upsell. It cannot be called.
		if disabled, _ := entry["disabled"].(bool); disabled {
			continue
		}
		name := antigravityModelDisplayName(modelID, entry)
		items = append(items, DiscoveredModel{
			ID:           modelID,
			Name:         name,
			OwnedBy:      "antigravity",
			Capabilities: antigravityModelCapabilities(r, modelID),
		})
	}
	// The upstream payload is a JSON object, so iteration order is random.
	// Sort for a stable dashboard listing.
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if len(items) == 0 {
		return nil, &ProviderError{Code: "model_discovery_unsupported", Message: "Antigravity returned only internal models for this account"}
	}
	return items, nil
}

func antigravityModelDisplayName(modelID string, entry map[string]any) string {
	if name := strings.TrimSpace(stringValue(firstValue(entry, "displayName", "display_name"))); name != "" {
		return name
	}
	if name := antigravityKnownModels[modelID]; name != "" {
		return name
	}
	return modelID
}

// antigravityModelCapabilities marks the image generation models, which take a
// different request path and cannot be used for chat completions.
func antigravityModelCapabilities(r *Registry, modelID string) []string {
	if antigravityIsImageModel(modelID) {
		return []string{"image"}
	}
	return r.Capabilities("antigravity")
}
