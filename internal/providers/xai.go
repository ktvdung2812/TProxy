package providers

import (
	"context"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

// xAI integration follows the dual-API model used by Pi (packages/ai):
// - grok-4.5 on api.x.ai uses OpenAI Responses (/responses)
// - other Grok models use OpenAI Chat Completions (/chat/completions)
// Grok CLI subscription traffic uses cli-chat-proxy.grok.com with first-party headers.

type xaiAdapter struct {
	client *http.Client
	openAI *openAIAdapter
	codex  *codexAdapter
}

func newXAIAdapter(client *http.Client) *xaiAdapter {
	return &xaiAdapter{
		client: client,
		openAI: &openAIAdapter{client: client},
		codex:  &codexAdapter{client: client},
	}
}

func (a *xaiAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	events, err := a.ExecuteStream(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	result := &canonical.Response{Model: request.UpstreamModel, Role: "assistant", Raw: map[string]any{}}
	var text, reasoning strings.Builder
	for event := range events {
		switch event.Type {
		case canonical.EventMessageStart:
			result.ID = event.ID
			if event.Model != "" {
				result.Model = event.Model
			}
		case canonical.EventTextDelta:
			text.WriteString(event.Text)
		case canonical.EventReasoningDelta:
			reasoning.WriteString(event.Reasoning)
		case canonical.EventToolCallDelta:
			result.ToolCalls = append(result.ToolCalls, event.ToolCall)
		case canonical.EventUsage:
			if event.Usage != nil {
				result.Usage = *event.Usage
			}
		case canonical.EventMessageEnd:
			result.FinishReason = event.FinishReason
		case canonical.EventError:
			return nil, event.Err
		}
	}
	result.Content = text.String()
	result.Reasoning = reasoning.String()
	return result, nil
}

func (a *xaiAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	if xaiUsesResponsesAPI(provider, request.UpstreamModel) {
		ctx = withCredentialProxy(ctx, credential)
		body := xaiResponsesBody(request)
		return a.codex.streamResponses(ctx, provider, credential, request, body, xaiHeaders(provider, credential, true, request))
	}
	return a.openAI.ExecuteStream(ctx, provider, credential, request)
}

func xaiUsesResponsesAPI(provider store.Provider, model string) bool {
	if !xaiUsesPublicAPI(provider.BaseURL) {
		return false
	}
	return xaiModelUsesResponsesAPI(model)
}

func xaiUsesPublicAPI(baseURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseURL))
	return strings.Contains(lower, "api.x.ai")
}

func xaiModelUsesResponsesAPI(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "grok-4.5" {
		return true
	}
	return strings.HasPrefix(model, "grok-4.5-") || strings.HasPrefix(model, "grok-4.5.")
}

func xaiResponsesBody(request canonical.Request) map[string]any {
	body := codexBody(request)
	xaiNormalizeResponsesBody(body, request)
	return body
}

func xaiNormalizeResponsesBody(body map[string]any, request canonical.Request) {
	delete(body, "safety_identifier")
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(request.RequestID)
	}
	if sessionID != "" {
		body["prompt_cache_key"] = sessionID
	}
	if reasoning, ok := body["reasoning"].(map[string]any); ok {
		delete(reasoning, "summary")
		reasoning["effort"] = xaiWireReasoningEffort(stringValue(reasoning["effort"]))
		body["reasoning"] = reasoning
	}
	body["include"] = []any{"reasoning.encrypted_content"}
}

func xaiWireReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return "medium"
	}
}

func xaiHeaders(provider store.Provider, credential store.Credential, streaming bool, request canonical.Request) http.Header {
	headers := authHeaders(provider, credential)
	applyGrokCLIHeaders(headers, provider.BaseURL)
	if sessionID := strings.TrimSpace(request.SessionID); sessionID != "" {
		headers.Set("session_id", sessionID)
	} else if sessionID := strings.TrimSpace(request.RequestID); sessionID != "" {
		headers.Set("session_id", sessionID)
	}
	if streaming {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "application/json")
	}
	return headers
}

func applyGrokCLIHeaders(headers http.Header, baseURL string) {
	if !strings.Contains(strings.ToLower(baseURL), "cli-chat-proxy.grok.com") {
		return
	}
	if headers.Get("x-xai-token-auth") == "" {
		headers.Set("x-xai-token-auth", "xai-grok-cli")
	}
	if headers.Get("x-grok-client-identifier") == "" {
		headers.Set("x-grok-client-identifier", "grok-cli")
	}
	if headers.Get("x-grok-client-version") == "" {
		headers.Set("x-grok-client-version", "0.2.99")
	}
	if headers.Get("x-grok-client-mode") == "" {
		headers.Set("x-grok-client-mode", "headless")
	}
}

func applyXAICompletionsCompat(body map[string]any) {
	delete(body, "store")
	delete(body, "reasoning_effort")
	if maxTokens, exists := body["max_tokens"]; exists {
		delete(body, "max_tokens")
		if body["max_completion_tokens"] == nil {
			body["max_completion_tokens"] = maxTokens
		}
	}
}
