package providers

import (
	"context"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/thinking"
)

// Meta Model API (https://dev.meta.ai/docs/protocols) serves Muse Spark and
// Muse Wonder on both Chat Completions and Responses, but only Responses
// carries the model's reasoning between turns: Chat Completions redacts
// reasoning_content for external keys. Those models therefore go to
// /responses with encrypted reasoning replay; anything else stays on Chat.

type museAdapter struct {
	openAI *openAIAdapter
	codex  *codexAdapter
}

func newMuseAdapter(client *http.Client) *museAdapter {
	return &museAdapter{openAI: &openAIAdapter{client: client}, codex: &codexAdapter{client: client}}
}

func (a *museAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	if !museUsesResponsesAPI(request.UpstreamModel) {
		return a.openAI.Execute(ctx, provider, credential, request)
	}
	events, err := a.ExecuteStream(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	return collectStreamResponse(events, request.UpstreamModel)
}

func (a *museAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	if !museUsesResponsesAPI(request.UpstreamModel) {
		return a.openAI.ExecuteStream(ctx, provider, credential, request)
	}
	ctx = withCredentialProxy(ctx, credential)
	headers := authHeaders(provider, credential)
	headers.Set("Accept", "text/event-stream")
	return a.codex.streamResponses(ctx, provider, credential, request, museResponsesBody(request), headers)
}

func (a *museAdapter) Proxy(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	return a.openAI.Proxy(ctx, provider, credential, rawRequest)
}

func museUsesResponsesAPI(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "muse-spark") || strings.HasPrefix(model, "muse-wonder")
}

// applyMuseHeaders marks subscription (Muse Code sign-in) traffic the way the
// Muse Code CLI does. Pay-as-you-go Model API keys are sent unchanged.
func applyMuseHeaders(headers http.Header, credential store.Credential) {
	if credential.AuthType == "oauth" && headers.Get("x-api-version") == "" {
		headers.Set("x-api-version", config.MuseAPIVersion)
	}
}

// museResponsesBody builds the /responses request. It starts from the Codex
// translation (input items, tool names, store=false) and then undoes the
// Codex-only restrictions Meta does not share.
func museResponsesBody(request canonical.Request) map[string]any {
	body := codexBody(request)
	// Codex strips sampling and limit fields; Meta accepts them
	// (max_output_tokens has a floor of 16).
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.MaxTokens >= 16 {
		body["max_output_tokens"] = request.MaxTokens
	}
	// Documented as rejected or unimplemented on Meta's /responses.
	for _, key := range []string{"frequency_penalty", "presence_penalty", "logprobs", "top_logprobs", "stop", "n"} {
		delete(body, key)
	}
	if truncation := stringValue(body["truncation"]); truncation != "" && truncation != "disabled" {
		delete(body, "truncation")
	}
	// Muse reasons by default and rejects effort "none", so only an explicit
	// level is sent; Codex's implicit "low" default is dropped.
	if effort := museReasoningEffort(request.UpstreamModel, thinking.FromSources(request).Effort); effort != "" {
		body["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
	} else {
		delete(body, "reasoning")
	}
	// Encrypted reasoning is what lets a store=false conversation keep its
	// chain of thought across turns. Unknown include values are a 400.
	body["include"] = []any{"reasoning.encrypted_content"}
	// Meta's function tools default to strict schemas; Chat Completions
	// clients expect the non-strict default.
	tools, ok := body["tools"].([]map[string]any)
	if !ok {
		tools = mapSlice(body["tools"])
	}
	for _, tool := range tools {
		if stringValue(tool["type"]) == "function" && tool["strict"] == nil {
			tool["strict"] = false
		}
	}
	if sessionID := strings.TrimSpace(request.SessionID); sessionID != "" {
		body["prompt_cache_key"] = sessionID
	}
	return body
}

// museReasoningEffort maps an effort label onto the levels Meta accepts:
// minimal..xhigh everywhere, "max" only on muse-spark-1.3 and Muse Wonder.
func museReasoningEffort(model, effort string) string {
	switch effort = strings.ToLower(strings.TrimSpace(effort)); effort {
	case "minimal", "low", "medium", "high", "xhigh":
		return effort
	case "max":
		model = strings.ToLower(strings.TrimSpace(model))
		if model == "muse-spark-1.3" || strings.HasPrefix(model, "muse-wonder") {
			return "max"
		}
		return "xhigh"
	default:
		return ""
	}
}
