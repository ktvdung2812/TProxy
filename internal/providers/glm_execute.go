package providers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

func glmBaseURLCandidates(baseURL string) []string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil
	}
	alt := alternateGLMBaseURL(base)
	if alt == "" || alt == base {
		return []string{base}
	}
	return []string{base, alt}
}

func isGLMParameterError(data []byte, status int) bool {
	if status != http.StatusBadRequest {
		return false
	}
	text := strings.ToLower(string(data))
	return strings.Contains(text, `"code":"1210"`) ||
		strings.Contains(text, `"code":1210`) ||
		strings.Contains(text, "invalid api parameter")
}

func postOpenAIChat(ctx context.Context, client *http.Client, provider store.Provider, headers http.Header, body map[string]any) (*http.Response, error) {
	if !isGLMProvider(provider) {
		return executeJSON(ctx, client, http.MethodPost, endpoint(provider.BaseURL, "/v1/chat/completions"), headers, body)
	}

	var lastData []byte
	var lastStatus int
	for i, base := range glmBaseURLCandidates(provider.BaseURL) {
		candidate := provider
		candidate.BaseURL = base
		response, err := executeJSON(ctx, client, http.MethodPost, endpoint(candidate.BaseURL, "/v1/chat/completions"), headers, body)
		if err != nil {
			return nil, err
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return response, nil
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if i < len(glmBaseURLCandidates(provider.BaseURL))-1 && isGLMParameterError(data, response.StatusCode) {
			lastData = data
			lastStatus = response.StatusCode
			continue
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		return response, nil
	}
	if len(lastData) > 0 {
		return &http.Response{
			StatusCode: lastStatus,
			Body:       io.NopCloser(bytes.NewReader(lastData)),
		}, nil
	}
	return executeJSON(ctx, client, http.MethodPost, endpoint(provider.BaseURL, "/v1/chat/completions"), headers, body)
}

func glmCapabilitiesForModel(modelID string) []string {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	caps := []string{"text", "tools", "reasoning"}
	if strings.Contains(lower, "4.6v") ||
		strings.Contains(lower, "5v") ||
		strings.HasSuffix(lower, "-v") ||
		strings.Contains(lower, "vision") {
		caps = append(caps, "vision")
	}
	return caps
}

func discoveryCapabilities(registry *Registry, provider store.Provider, providerType, modelID string) []string {
	if isGLMProvider(provider) {
		return glmCapabilitiesForModel(modelID)
	}
	return registry.Capabilities(providerType)
}
