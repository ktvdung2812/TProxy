package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

// TestUpstreamModel probes a provider upstream model using one enabled credential.
func (r *Router) TestUpstreamModel(ctx context.Context, providerID, modelID, kind, credentialID string, preferredCredentialIDs []string) (providers.PingResult, string, error) {
	provider, err := r.store.Provider(ctx, providerID)
	if err != nil {
		return providers.PingResult{}, "", err
	}
	credential, err := r.pickCredentialForTest(ctx, providerID, credentialID, preferredCredentialIDs)
	if err != nil {
		return providers.PingResult{}, "", err
	}
	selection := Selection{Provider: *provider, Credential: credential}
	prepared, prepareErr := r.prepareCredential(ctx, selection, false)
	if prepareErr != nil {
		return providers.PingResult{}, credential.ID, asCredentialError(prepareErr)
	}
	result := r.registry.PingModel(ctx, *provider, prepared, modelID, resolveUpstreamProbeKind(modelID, kind))
	return result, prepared.ID, nil
}

// resolveUpstreamProbeKind picks the endpoint for a direct upstream model probe.
// Providers often tag chat models with embedding capability; probe those via chat
// unless the model id clearly targets embeddings.
func resolveUpstreamProbeKind(modelID, kind string) string {
	normalized := strings.TrimSpace(strings.ToLower(kind))
	if normalized == "" {
		return "llm"
	}
	if normalized != "embedding" {
		return normalized
	}
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" || strings.Contains(id, "embed") {
		return "embedding"
	}
	return "llm"
}

// TestPublicModel probes a configured virtual model through the normal router path.
func (r *Router) TestPublicModel(ctx context.Context, publicModelID string) (providers.PingResult, error) {
	model, err := r.store.ResolveModel(ctx, publicModelID, "")
	if err != nil {
		return providers.PingResult{}, err
	}
	start := time.Now()
	request := canonical.Request{
		RequestID: fmt.Sprintf("model-test-%d", start.UnixNano()),
		MaxTokens: 16,
		Messages:  []canonical.Message{{Role: "user", Content: "hi"}},
	}
	execResult, err := r.Execute(ctx, *model, request)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return providers.PingResult{
			OK:        false,
			LatencyMS: latency,
			Error:     err.Error(),
			Status:    providers.Status(err),
		}, nil
	}
	if execResult == nil || execResult.Response == nil {
		return providers.PingResult{OK: false, LatencyMS: latency, Error: "Router returned no response"}, nil
	}
	response := execResult.Response
	if strings.TrimSpace(response.Reasoning) != "" || len(response.ToolCalls) > 0 || response.Usage.OutputTokens > 0 {
		return providers.PingResult{OK: true, LatencyMS: latency}, nil
	}
	content := fmt.Sprint(response.Content)
	if strings.TrimSpace(content) != "" {
		return providers.PingResult{OK: true, LatencyMS: latency}, nil
	}
	return providers.PingResult{OK: false, LatencyMS: latency, Error: "Provider returned no completion content for this model"}, nil
}

func (r *Router) pickCredentialForTest(ctx context.Context, providerID, credentialID string, preferredCredentialIDs []string) (store.Credential, error) {
	if credentialID != "" {
		credential, err := r.store.CredentialByID(ctx, credentialID)
		if err != nil {
			return store.Credential{}, err
		}
		if !credential.Enabled {
			return store.Credential{}, fmt.Errorf("credential %s is disabled", credentialID)
		}
		return credential, nil
	}
	credentials, err := r.store.Credentials(ctx, providerID)
	if err != nil {
		return store.Credential{}, err
	}
	byID := make(map[string]store.Credential, len(credentials))
	for _, credential := range credentials {
		byID[credential.ID] = credential
	}
	for _, id := range preferredCredentialIDs {
		if credential, ok := byID[id]; ok && credential.Enabled {
			return credential, nil
		}
	}
	now := time.Now()
	for _, credential := range store.EligibleCredentials(credentials, now) {
		return credential, nil
	}
	for _, credential := range credentials {
		if credential.Enabled {
			return credential, nil
		}
	}
	if len(credentials) > 0 {
		return credentials[0], nil
	}
	return store.Credential{}, fmt.Errorf("no credentials configured for provider %s", providerID)
}
