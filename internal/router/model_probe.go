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

// CredentialChatResult is one exchange run against a single credential.
type CredentialChatResult struct {
	Content   string          `json:"content"`
	Reasoning string          `json:"reasoning,omitempty"`
	Model     string          `json:"model,omitempty"`
	LatencyMS int64           `json:"latency_ms"`
	Usage     canonical.Usage `json:"usage"`
}

// ChatWithCredential runs a completion through one specific credential and
// returns what the model actually said.
//
// It deliberately bypasses routing and failover: the point is to exercise this
// account, so a request that would normally rotate to a healthy sibling must
// fail here instead, and surface why.
func (r *Router) ChatWithCredential(ctx context.Context, providerID, credentialID, modelID string, messages []canonical.Message) (CredentialChatResult, error) {
	provider, err := r.store.Provider(ctx, providerID)
	if err != nil {
		return CredentialChatResult{}, err
	}
	credential, err := r.store.CredentialByID(ctx, credentialID)
	if err != nil {
		return CredentialChatResult{}, err
	}
	if credential.ProviderID != providerID {
		return CredentialChatResult{}, fmt.Errorf("credential %s does not belong to provider %s", credentialID, providerID)
	}
	adapter, err := r.registry.Adapter(provider.Type)
	if err != nil {
		return CredentialChatResult{}, err
	}
	prepared, prepareErr := r.prepareCredential(ctx, Selection{Provider: *provider, Credential: credential}, false)
	if prepareErr != nil {
		return CredentialChatResult{}, asCredentialError(prepareErr)
	}

	start := time.Now()
	request := canonical.Request{
		RequestID:     fmt.Sprintf("account-test-%d", start.UnixNano()),
		Source:        accountTestProtocol(provider.Type),
		UpstreamModel: modelID,
		MaxTokens:     1024,
		Messages:      messages,
	}
	response, err := adapter.Execute(ctx, *provider, prepared, request)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return CredentialChatResult{LatencyMS: latency}, err
	}
	if response == nil {
		return CredentialChatResult{LatencyMS: latency}, fmt.Errorf("provider returned no response")
	}
	return CredentialChatResult{
		Content:   fmt.Sprint(response.Content),
		Reasoning: response.Reasoning,
		Model:     response.Model,
		LatencyMS: latency,
		Usage:     response.Usage,
	}, nil
}

// accountTestProtocol picks the request shape for a pinned-credential test.
//
// A provider's own protocol is normally right: each adapter builds its native
// body directly from canonical messages. Responses is the exception — an
// adapter seeing it forwards the upstream stream verbatim for a client that
// speaks that protocol, and those raw events carry no canonical text, so the
// reply would arrive empty. The neutral OpenAI shape is used there instead.
func accountTestProtocol(providerType string) canonical.Protocol {
	if source := providers.DefaultProtocol(providerType); source != canonical.ProtocolResponses {
		return source
	}
	return canonical.ProtocolOpenAI
}
