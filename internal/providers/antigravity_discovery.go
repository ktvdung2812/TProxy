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
// The value is a variable so tests can point discovery at a local server; it is
// never reassigned at runtime.
var antigravityModelsURL = "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"

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
	payload, status, err := r.quotaPOST(ctx, antigravityModelsURL, antigravityQuotaHeaders(token), body)
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
	if len(models) == 0 {
		return nil, &ProviderError{Code: "model_discovery_unsupported", Message: "Antigravity returned no models for this account"}
	}

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
			continue
		}
		if isInternal, _ := entry["isInternal"].(bool); isInternal {
			continue
		}
		name := stringValue(firstValue(entry, "displayName", "display_name"))
		if name == "" {
			name = modelID
		}
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

// antigravityModelCapabilities marks the image generation models, which take a
// different request path and cannot be used for chat completions.
func antigravityModelCapabilities(r *Registry, modelID string) []string {
	if antigravityIsImageModel(modelID) {
		return []string{"image"}
	}
	return r.Capabilities("antigravity")
}
