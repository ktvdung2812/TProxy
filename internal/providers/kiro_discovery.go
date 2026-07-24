package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/store"
)

var kiroStaticModels = []struct {
	id, name string
}{
	{id: "claude-opus-4.8", name: "Claude Opus 4.8"},
	{id: "claude-opus-4.7", name: "Claude Opus 4.7"},
	{id: "claude-opus-4.5", name: "Claude Opus 4.5"},
	{id: "claude-sonnet-5", name: "Claude Sonnet 5"},
	{id: "claude-sonnet-4.5", name: "Claude Sonnet 4.5"},
	{id: "claude-haiku-4.5", name: "Claude Haiku 4.5"},
	{id: "deepseek-3.2", name: "DeepSeek 3.2"},
	{id: "qwen3-coder-next", name: "Qwen3 Coder Next"},
	{id: "glm-5", name: "GLM 5"},
	{id: "MiniMax-M2.5", name: "MiniMax M2.5"},
	{id: "gpt-5.6-sol", name: "GPT 5.6 Sol"},
	{id: "gpt-5.6-terra", name: "GPT 5.6 Terra"},
	{id: "gpt-5.6-luna", name: "GPT 5.6 Luna"},
	{id: "auto", name: "Auto"},
}

func discoverKiroModels(ctx context.Context, registry *Registry, provider store.Provider, credential store.Credential) ([]DiscoveredModel, error) {
	accessToken := kiroAccessToken(credential)
	if accessToken == "" {
		if items := kiroStaticModelEntries(registry, provider); len(items) > 0 {
			return items, nil
		}
		return nil, &ProviderError{Code: "model_discovery_failed", Message: "Kiro credential has no access token"}
	}
	profileARN := credentialExtraString(credential, "profile_arn", "profileArn")
	region := kiroRegionFromProfileARN(profileARN)
	if region == "" {
		region = credentialExtraString(credential, "region")
	}
	if region == "" {
		region = "us-east-1"
	}
	ctx = withCredentialProxy(ctx, credential)
	raw, err := fetchKiroModelCatalog(ctx, registry.client, accessToken, profileARN, region, credential)
	if err != nil {
		if items := kiroStaticModelEntries(registry, provider); len(items) > 0 {
			return items, nil
		}
		return nil, &ProviderError{Code: "model_discovery_failed", Message: err.Error(), Err: err}
	}
	if len(raw) == 0 {
		if items := kiroStaticModelEntries(registry, provider); len(items) > 0 {
			return items, nil
		}
		return nil, &ProviderError{Code: "model_discovery_failed", Message: "Kiro returned no models"}
	}
	items := make([]DiscoveredModel, 0, len(raw)*3)
	for _, model := range raw {
		upstreamID := strings.TrimSpace(firstStringValue(stringValue(model["modelId"]), stringValue(model["id"])))
		if upstreamID == "" {
			continue
		}
		display := kiroModelDisplayName(model, upstreamID)
		for _, variant := range expandKiroModelVariants(upstreamID, display) {
			items = append(items, DiscoveredModel{
				ID:           variant.id,
				Name:         variant.name,
				OwnedBy:      "kiro",
				Capabilities: kiroDiscoveryCapabilities(registry, provider, variant),
			})
		}
	}
	if len(items) == 0 {
		if fallback := kiroStaticModelEntries(registry, provider); len(fallback) > 0 {
			return fallback, nil
		}
		return nil, &ProviderError{Code: "model_discovery_failed", Message: "Kiro returned no usable models"}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func kiroAccessToken(credential store.Credential) string {
	authMethod := credentialExtraString(credential, "auth_method", "authMethod")
	token := strings.TrimSpace(credential.Secret)
	if credential.OAuthToken != nil && strings.TrimSpace(credential.OAuthToken.AccessToken) != "" {
		token = strings.TrimSpace(credential.OAuthToken.AccessToken)
	}
	if authMethod == "api_key" {
		if apiKey := credentialExtraString(credential, "api_key", "apiKey"); apiKey != "" {
			token = apiKey
		}
	}
	return token
}

func kiroRegionFromProfileARN(profileARN string) string {
	profileARN = strings.TrimSpace(profileARN)
	if profileARN == "" {
		return ""
	}
	parts := strings.Split(profileARN, ":")
	if len(parts) >= 4 {
		return strings.TrimSpace(parts[3])
	}
	return ""
}

func fetchKiroModelCatalog(ctx context.Context, client *http.Client, accessToken, profileARN, region string, credential store.Credential) ([]map[string]any, error) {
	if client == nil {
		client = http.DefaultClient
	}
	params := url.Values{}
	params.Set("origin", "AI_EDITOR")
	if profileARN != "" {
		params.Set("profileArn", profileARN)
	}
	target := fmt.Sprintf("https://q.%s.amazonaws.com/ListAvailableModels?%s", region, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	for key, values := range kiroDiscoveryHeaders(accessToken, credential) {
		req.Header[key] = values
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if models, postErr := fetchKiroModelCatalogPOST(ctx, client, accessToken, profileARN, region, credential); postErr == nil {
			return models, nil
		}
		return nil, fmt.Errorf("Kiro ListAvailableModels HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return kiroModelsFromPayload(payload), nil
}

func fetchKiroModelCatalogPOST(ctx context.Context, client *http.Client, accessToken, profileARN, region string, credential store.Credential) ([]map[string]any, error) {
	endpoint := fmt.Sprintf("https://codewhisperer.%s.amazonaws.com", region)
	body := map[string]any{"origin": "AI_EDITOR"}
	if profileARN != "" {
		body["profileArn"] = profileARN
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	for key, values := range kiroDiscoveryHeaders(accessToken, credential) {
		req.Header[key] = values
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonCodeWhispererService.ListAvailableModels")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Kiro ListAvailableModels POST HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return kiroModelsFromPayload(payload), nil
}

func kiroModelsFromPayload(payload map[string]any) []map[string]any {
	raw, _ := payload["models"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if model, ok := item.(map[string]any); ok {
			out = append(out, model)
		}
	}
	return out
}

func kiroDiscoveryHeaders(accessToken string, credential store.Credential) http.Header {
	seed := firstStringValue(
		credentialExtraString(credential, "client_id", "clientId"),
		credentialExtraString(credential, "profile_arn", "profileArn"),
		credential.ID,
		accessToken,
		"kiro-anonymous",
	)
	sum := sha256.Sum256([]byte(seed))
	machineID := hex.EncodeToString(sum[:])
	userAgent := fmt.Sprintf(
		"aws-sdk-js/1.0.0 ua/2.1 os/windows#10.0.26200 lang/js md/nodejs#22.21.1 api/codewhispererruntime#1.0.0 m/N,E KiroIDE-0.10.32-%s",
		machineID,
	)
	headers := http.Header{
		"Authorization":           {"Bearer " + accessToken},
		"Accept":                    {"application/json"},
		"User-Agent":                {userAgent},
		"X-Amz-User-Agent":          {fmt.Sprintf("aws-sdk-js/1.0.0 KiroIDE-0.10.32-%s", machineID)},
		"X-Amzn-Kiro-Agent-Mode":    {"vibe"},
		"X-Amzn-Codewhisperer-Optout": {"true"},
		"Amz-Sdk-Request":           {"attempt=1; max=1"},
		"Amz-Sdk-Invocation-Id":     {uuid.NewString()},
	}
	authMethod := credentialExtraString(credential, "auth_method", "authMethod")
	if authMethod == "api_key" {
		headers.Set("tokentype", "API_KEY")
	} else if authMethod == "external_idp" {
		headers.Set("TokenType", "EXTERNAL_IDP")
	}
	return headers
}

type kiroModelVariant struct {
	id, name  string
	thinking  bool
	agentic   bool
}

func expandKiroModelVariants(upstreamID, display string) []kiroModelVariant {
	isAuto := upstreamID == "auto"
	variants := []kiroModelVariant{{id: upstreamID, name: display}}
	variants = append(variants, kiroModelVariant{id: upstreamID + "-thinking", name: display + " (Thinking)", thinking: true})
	if !isAuto {
		variants = append(variants,
			kiroModelVariant{id: upstreamID + "-agentic", name: display + " (Agentic)", agentic: true},
			kiroModelVariant{id: upstreamID + "-thinking-agentic", name: display + " (Thinking + Agentic)", thinking: true, agentic: true},
		)
	}
	return variants
}

func kiroModelDisplayName(model map[string]any, upstreamID string) string {
	name := strings.TrimSpace(firstStringValue(stringValue(model["modelName"]), stringValue(model["name"])))
	if name == "" {
		name = upstreamID
	}
	rate := numberValue(model["rateMultiplier"])
	if rate > 0 && rate != 1 {
		return fmt.Sprintf("Kiro %s (%.1fx credit)", name, float64(rate))
	}
	return "Kiro " + name
}

func kiroDiscoveryCapabilities(registry *Registry, provider store.Provider, variant kiroModelVariant) []string {
	caps := discoveryCapabilities(registry, provider, provider.Type, variant.id)
	seen := make(map[string]struct{}, len(caps))
	for _, cap := range caps {
		seen[cap] = struct{}{}
	}
	if variant.thinking {
		seen["reasoning"] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for cap := range seen {
		out = append(out, cap)
	}
	sort.Strings(out)
	return out
}

func kiroStaticModelEntries(registry *Registry, provider store.Provider) []DiscoveredModel {
	items := make([]DiscoveredModel, 0, len(kiroStaticModels)*3)
	for _, model := range kiroStaticModels {
		display := "Kiro " + model.name
		for _, variant := range expandKiroModelVariants(model.id, display) {
			items = append(items, DiscoveredModel{
				ID:           variant.id,
				Name:         variant.name,
				OwnedBy:      "kiro",
				Capabilities: kiroDiscoveryCapabilities(registry, provider, variant),
			})
		}
	}
	return items
}

func firstStringValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
