package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/translator/claudeopenai"
)

type ProviderError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter string
	Err        error
}

func (e *ProviderError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("provider error (%d)", e.Status)
}

func (e *ProviderError) Unwrap() error { return e.Err }

type Adapter interface {
	Execute(context.Context, store.Provider, store.Credential, canonical.Request) (*canonical.Response, error)
	ExecuteStream(context.Context, store.Provider, store.Credential, canonical.Request) (<-chan canonical.Event, error)
}

type RawResponse struct {
	Status      int
	Headers     http.Header
	Body        []byte
	ContentType string
}

type RawRequest struct {
	Method      string
	Path        string
	Body        []byte
	ContentType string
	Headers     http.Header
}

type RawProxyAdapter interface {
	Proxy(context.Context, store.Provider, store.Credential, RawRequest) (*RawResponse, error)
}

// DiscoveredModel is the provider-neutral model catalog shape returned by
// lightweight provider discovery. It intentionally contains no credentials or
// provider-specific authorization material.
type DiscoveredModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	OwnedBy       string   `json:"owned_by,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	CredentialIDs []string `json:"credential_ids,omitempty"`
}

type AdapterDescriptor struct {
	ProviderType   string   `json:"provider_type"`
	ProtocolFamily string   `json:"protocol_family"`
	Capabilities   []string `json:"capabilities"`
	ModelDiscovery bool     `json:"model_discovery"`
	BootstrapRetry bool     `json:"bootstrap_retry"`
}

type Registry struct {
	client *http.Client
	items  map[string]Adapter
}

// Capabilities returns the conservative capability set advertised by a
// built-in adapter. Administrator model overrides remain authoritative.
func (r *Registry) Capabilities(providerType string) []string {
	capabilities := map[string][]string{
		"openai-compatible":    {"text", "vision", "tools", "reasoning", "embedding", "image-output", "video-output", "tts", "stt"},
		"image":                {"image-output"},
		"video":                {"video-output"},
		"ollama":               {"text", "vision", "tools"},
		"kimi":                 {"text", "tools", "reasoning"},
		"xai":                  {"text", "vision", "tools", "reasoning"},
		"anthropic-compatible": {"text", "vision", "tools", "reasoning"},
		"claude":               {"text", "vision", "tools", "reasoning"},
		"codex":                {"text", "vision", "tools", "reasoning"},
		"gemini":               {"text", "vision", "tools", "reasoning", "embedding"},
		"vertex":               {"text", "vision", "tools", "reasoning", "embedding"},
		"antigravity":          {"text", "vision", "tools", "reasoning"},
		"tavily":               {"web-search"},
		"elevenlabs":           {"tts", "stt"},
		"plugin-http":          {"*"},
		"copilot":              {"text", "vision", "tools", "reasoning"},
		"vertex-partner":       {"text", "vision", "tools", "reasoning", "embedding"},
		"qwen":                 {"text", "tools"},
		"kiro":                 {"text", "vision", "tools", "reasoning"},
		"qoder":                {"text", "tools", "reasoning"},
	}
	items := append([]string(nil), capabilities[providerType]...)
	return items
}

func (r *Registry) Describe(providerType string) (AdapterDescriptor, error) {
	if _, err := r.Adapter(providerType); err != nil {
		return AdapterDescriptor{}, err
	}
	protocols := map[string]string{
		"openai-compatible": "openai", "image": "openai", "video": "openai", "ollama": "openai", "kimi": "openai", "xai": "openai",
		"anthropic-compatible": "anthropic", "claude": "anthropic", "codex": "responses", "gemini": "gemini", "vertex": "gemini",
		"antigravity": "gemini", "tavily": "search", "elevenlabs": "audio", "plugin-http": "canonical-plugin",
		"copilot": "openai", "vertex-partner": "openai", "qwen": "openai", "kiro": "kiro", "qoder": "qoder",
	}
	return AdapterDescriptor{ProviderType: providerType, ProtocolFamily: protocols[providerType], Capabilities: r.Capabilities(providerType), ModelDiscovery: providerType != "antigravity" && providerType != "tavily" && providerType != "elevenlabs", BootstrapRetry: true}, nil
}

// HealthCheck performs a lightweight non-generation request. It is safe to
// run from the dashboard and does not consume paid generation quota.
func (r *Registry) HealthCheck(ctx context.Context, provider store.Provider, credential store.Credential) error {
	if provider.Type == "tavily" || provider.Type == "elevenlabs" {
		if strings.TrimSpace(provider.BaseURL) == "" {
			return &ProviderError{Code: "health_check_failed", Message: "provider base URL is empty"}
		}
		return nil
	}
	if shouldSkipModelDiscovery(provider) {
		if strings.TrimSpace(provider.BaseURL) == "" {
			return &ProviderError{Code: "health_check_failed", Message: "provider base URL is empty"}
		}
		if items := staticDiscoveryModels(provider); len(items) > 0 {
			return nil
		}
		return nil
	}
	_, err := r.discover(ctx, provider, credential)
	return err
}

// DiscoverModels fetches a provider's model catalog using a GET-only request.
// Results are returned to the management API; aliases and public models are
// never overwritten implicitly.
func (r *Registry) DiscoverModels(ctx context.Context, provider store.Provider, credential store.Credential) ([]DiscoveredModel, error) {
	return r.discover(ctx, provider, credential)
}

func (r *Registry) discover(ctx context.Context, provider store.Provider, credential store.Credential) ([]DiscoveredModel, error) {
	if provider.Type == "antigravity" {
		return nil, &ProviderError{Code: "model_discovery_unsupported", Message: "Antigravity model discovery requires a project-scoped generation request"}
	}
	if shouldSkipModelDiscovery(provider) {
		if items := staticDiscoveryModels(provider); len(items) > 0 {
			return items, nil
		}
		return nil, &ProviderError{Code: "model_discovery_unsupported", Message: "model discovery is not supported for this provider type"}
	}
	headers := authHeaders(provider, credential)
	if provider.Type == "codex" {
		headers = codexHeaders(provider, credential, false, canonical.Request{})
	}
	ctx = withCredentialProxy(ctx, credential)
	target := modelsDiscoveryURL(provider)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, &ProviderError{Code: "health_check_failed", Err: err}
	}
	req.Header = correlationHeaders(headers, "")
	response, err := r.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: "health_check_failed", Err: err}
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return nil, &ProviderError{Code: "health_check_failed", Err: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusNotFound {
			if items := staticDiscoveryModels(provider); len(items) > 0 {
				return items, nil
			}
		}
		return nil, upstreamResponseError(response, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &ProviderError{Code: "model_discovery_failed", Message: "provider returned invalid model catalog", Err: err}
	}
	items := make([]DiscoveredModel, 0)
	if provider.Type == "codex" {
		models, _ := payload["models"].([]any)
		for _, raw := range models {
			item, _ := raw.(map[string]any)
			id := stringValue(firstValue(item, "slug", "id"))
			if id == "" {
				continue
			}
			items = append(items, DiscoveredModel{ID: id, Name: stringValue(item["name"]), OwnedBy: "openai", Capabilities: discoveryCapabilities(r, provider, provider.Type, id)})
		}
		return items, nil
	}
	if provider.Type == "gemini" {
		models, _ := payload["models"].([]any)
		for _, raw := range models {
			item, _ := raw.(map[string]any)
			id := strings.TrimPrefix(stringValue(item["name"]), "models/")
			if id == "" {
				continue
			}
			items = append(items, DiscoveredModel{ID: id, Name: stringValue(item["displayName"]), OwnedBy: "google", Capabilities: r.Capabilities(provider.Type)})
		}
		return items, nil
	}
	models, _ := payload["data"].([]any)
	for _, raw := range models {
		item, _ := raw.(map[string]any)
		id := stringValue(item["id"])
		if id == "" {
			continue
		}
		items = append(items, DiscoveredModel{ID: id, Name: stringValue(item["name"]), OwnedBy: stringValue(item["owned_by"]), Capabilities: discoveryCapabilities(r, provider, provider.Type, id)})
	}
	return items, nil
}

func NewRegistry() *Registry {
	client := &http.Client{Transport: newProxyTransport()}
	return &Registry{client: client, items: map[string]Adapter{
		"openai-compatible":    &openAIAdapter{client: client},
		"image":                &openAIAdapter{client: client},
		"video":                &openAIAdapter{client: client},
		"ollama":               &openAIAdapter{client: client},
		"kimi":                 &openAIAdapter{client: client},
		"xai":                  &openAIAdapter{client: client},
		"anthropic-compatible": &anthropicAdapter{client: client},
		"claude":               &anthropicAdapter{client: client},
		"codex":                &codexAdapter{client: client},
		"gemini":               &geminiAdapter{client: client},
		"vertex":               &geminiAdapter{client: client},
		"antigravity":          &antigravityAdapter{client: client},
		"tavily":               &tavilyAdapter{client: client},
		"elevenlabs":           &elevenLabsAdapter{client: client},
		"plugin-http":          &pluginHTTPAdapter{client: client},
		"copilot":              &copilotAdapter{client: client, openAI: &openAIAdapter{client: client}},
		"vertex-partner":       &openAIAdapter{client: client},
		"qwen":                 &openAIAdapter{client: client},
		"kiro":                 &kiroAdapter{client: client},
		"qoder":                &qoderAdapter{client: client},
	}}
}

func (r *Registry) Adapter(providerType string) (Adapter, error) {
	adapter := r.items[providerType]
	if adapter == nil {
		return nil, fmt.Errorf("unsupported provider type %q", providerType)
	}
	return adapter, nil
}

func endpoint(baseURL, path string) string {
	if strings.HasPrefix(path, "/v1/") {
		return openAIResourceURL(baseURL, strings.TrimPrefix(path, "/v1"))
	}
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		return base + strings.TrimPrefix(path, "/v1")
	}
	return base + path
}

func authHeaders(provider store.Provider, credential store.Credential) http.Header {
	headers := make(http.Header)
	for key, value := range provider.Headers {
		headers.Set(key, value)
	}
	if credential.Secret != "" {
		tokenType := credential.TokenType
		if tokenType == "" {
			tokenType = "Bearer"
		}
		headers.Set("Authorization", tokenType+" "+credential.Secret)
	}
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	if provider.Type == "kimi" {
		if headers.Get("X-Msh-Platform") == "" {
			headers.Set("X-Msh-Platform", "tproxy")
		}
		if headers.Get("X-Msh-Version") == "" {
			headers.Set("X-Msh-Version", "0.1")
		}
		if headers.Get("X-Msh-Device-Name") == "" {
			headers.Set("X-Msh-Device-Name", "tproxy")
		}
		if headers.Get("X-Msh-Device-Model") == "" {
			headers.Set("X-Msh-Device-Model", "gateway")
		}
		if credential.OAuthToken != nil && credential.OAuthToken.Extra != nil {
			if deviceID := stringValue(credential.OAuthToken.Extra["device_id"]); deviceID != "" && headers.Get("X-Msh-Device-Id") == "" {
				headers.Set("X-Msh-Device-Id", deviceID)
			}
		}
	}
	return headers
}

func correlationHeaders(headers http.Header, requestID string) http.Header {
	if strings.TrimSpace(requestID) == "" {
		requestID = security.NewID("req_")
	}
	headers.Set("X-Request-ID", requestID)
	return headers
}

func executeJSON(ctx context.Context, client *http.Client, method, target string, headers http.Header, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header = headers.Clone()
	return client.Do(req)
}

func upstreamError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return upstreamResponseError(response, data)
}

func upstreamResponseError(response *http.Response, data []byte) error {
	return upstreamResponseErrorForProvider(response, data, "")
}

func upstreamResponseErrorForProvider(response *http.Response, data []byte, baseURL string) error {
	retryAfter := ""
	if response != nil {
		retryAfter = response.Header.Get("Retry-After")
	}
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	return upstreamBodyErrorForProvider(status, data, retryAfter, baseURL)
}

func upstreamBodyErrorForProvider(status int, data []byte, retryAfter, baseURL string) error {
	pe := buildUpstreamBodyError(status, data, retryAfter)
	if baseURL != "" {
		pe.Message = enrichGLMUpstreamMessage(baseURL, pe.Message, pe.Status)
	}
	return pe
}

func buildUpstreamBodyError(status int, data []byte, retryAfter string) *ProviderError {
	message := strings.TrimSpace(string(data))
	var parsed map[string]any
	if json.Unmarshal(data, &parsed) == nil {
		if item, ok := parsed["error"].(map[string]any); ok {
			if text, ok := item["message"].(string); ok {
				message = text
			}
		}
		if text, ok := parsed["message"].(string); ok {
			message = text
		}
	}
	if message == "" {
		message = http.StatusText(status)
	}
	message = security.RedactText(message)
	return &ProviderError{Status: status, Code: store.ErrorCode(status), Message: message, RetryAfter: retryAfter}
}

type openAIAdapter struct{ client *http.Client }

func (a *openAIAdapter) Proxy(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	ctx = withCredentialProxy(ctx, credential)
	target := endpoint(provider.BaseURL, rawRequest.Path)
	method := rawRequest.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(rawRequest.Body))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	req.Header = authHeaders(provider, credential)
	if rawRequest.ContentType != "" {
		req.Header.Set("Content-Type", rawRequest.ContentType)
	}
	for _, name := range []string{"Idempotency-Key", "X-Request-ID"} {
		if value := rawRequest.Headers.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}
	response, err := a.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if readErr != nil {
		return nil, &ProviderError{Status: 502, Code: "upstream_read_error", Err: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &RawResponse{Status: response.StatusCode, Headers: response.Header.Clone(), Body: data, ContentType: response.Header.Get("Content-Type")}, upstreamResponseError(response, data)
	}
	return &RawResponse{Status: response.StatusCode, Headers: response.Header.Clone(), Body: data, ContentType: response.Header.Get("Content-Type")}, nil
}

func openAIBody(provider store.Provider, request canonical.Request) map[string]any {
	return sanitizeOpenAIUpstreamBody(provider, buildOpenAIBody(request), request.Stream)
}

func buildOpenAIBody(request canonical.Request) map[string]any {
	if request.Source == canonical.ProtocolClaude && len(request.Raw) > 0 {
		return claudeopenai.ClaudeToOpenAIRequest(request.UpstreamModel, request.Raw, request.Stream)
	}
	if request.Source == canonical.ProtocolOpenAI && len(request.Raw) > 0 {
		body := cloneAnyMap(request.Raw)
		body["model"] = request.UpstreamModel
		body["stream"] = request.Stream
		return body
	}
	body := map[string]any{"model": request.UpstreamModel, "messages": request.Messages, "stream": request.Stream}
	if len(request.Tools) > 0 {
		body["tools"] = request.Tools
	}
	if request.ToolChoice != nil {
		body["tool_choice"] = request.ToolChoice
	}
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.MaxTokens > 0 {
		body["max_tokens"] = request.MaxTokens
	}
	for key, value := range request.Reasoning {
		body[key] = value
	}
	return body
}

func (a *openAIAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	ctx = withCredentialProxy(ctx, credential)
	request.Stream = false
	response, err := postOpenAIChat(ctx, a.client, provider, correlationHeaders(authHeaders(provider, credential), request.RequestID), openAIBody(provider, request))
	if err != nil {
		return nil, &ProviderError{Status: 0, Code: "upstream_network", Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, upstreamResponseErrorForProvider(response, data, provider.BaseURL)
	}
	var raw map[string]any
	if err = json.NewDecoder(response.Body).Decode(&raw); err != nil {
		return nil, &ProviderError{Status: 502, Code: "invalid_upstream_response", Err: err}
	}
	result := &canonical.Response{Raw: raw, Role: "assistant", Model: stringValue(raw["model"]), ID: stringValue(raw["id"])}
	choices, _ := raw["choices"].([]any)
	if len(choices) > 0 {
		choice, _ := choices[0].(map[string]any)
		result.FinishReason = stringValue(choice["finish_reason"])
		if msg, ok := choice["message"].(map[string]any); ok {
			result.Content = msg["content"]
			result.Reasoning = stringValue(firstValue(msg, "reasoning_content", "reasoning"))
			result.ToolCalls = mapSlice(msg["tool_calls"])
		}
	}
	result.Usage = parseOpenAIUsage(raw["usage"])
	return result, nil
}

func (a *openAIAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	request.Stream = true
	body := openAIBody(provider, request)
	if openAISupportsStreamOptions(provider) {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	headers := correlationHeaders(authHeaders(provider, credential), request.RequestID)
	headers.Set("Accept", "text/event-stream")
	response, err := postOpenAIChat(ctx, a.client, provider, headers, body)
	if err != nil {
		return nil, &ProviderError{Status: 0, Code: "upstream_network", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, upstreamError(response)
	}
	return parseOpenAIStream(ctx, response), nil
}

func parseOpenAIStream(ctx context.Context, response *http.Response) <-chan canonical.Event {
	out := make(chan canonical.Event, 16)
	go func() {
		defer close(out)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 2<<20)
		started := false
		// OpenAI-compatible servers may send a usage-only chunk after the first
		// finish_reason, so defer the canonical terminal event until SSE completion.
		finishReason := ""
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: finishReason}
				return
			}
			var raw map[string]any
			if json.Unmarshal([]byte(data), &raw) != nil {
				continue
			}
			if !started {
				out <- canonical.Event{Type: canonical.EventMessageStart, ID: stringValue(raw["id"]), Model: stringValue(raw["model"])}
				started = true
			}
			if usage, ok := raw["usage"].(map[string]any); ok {
				u := parseOpenAIUsage(usage)
				out <- canonical.Event{Type: canonical.EventUsage, Usage: &u}
			}
			choices, _ := raw["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if text := stringValue(delta["content"]); text != "" {
				out <- canonical.Event{Type: canonical.EventTextDelta, Text: text}
			}
			if text := stringValue(firstValue(delta, "reasoning_content", "reasoning")); text != "" {
				out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: text}
			}
			for _, call := range mapSlice(delta["tool_calls"]) {
				out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: call}
			}
			if finish := stringValue(choice["finish_reason"]); finish != "" {
				finishReason = finish
			}
		}
		if err := scanner.Err(); err != nil {
			out <- canonical.Event{Type: canonical.EventError, Err: err}
		} else if finishReason != "" {
			out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: finishReason}
		}
	}()
	return out
}

type codexAdapter struct{ client *http.Client }

func codexBody(request canonical.Request) map[string]any {
	if request.Source == canonical.ProtocolClaude && request.Raw != nil {
		openAI := claudeopenai.ClaudeToOpenAIRequest(request.UpstreamModel, request.Raw, true)
		request.Messages = canonicalMessages(openAI["messages"])
		request.Tools = mapSlice(openAI["tools"])
		if choice, ok := openAI["tool_choice"]; ok {
			request.ToolChoice = choice
		}
		if effort, ok := openAI["reasoning_effort"].(string); ok && strings.TrimSpace(effort) != "" {
			if request.Raw == nil {
				request.Raw = map[string]any{}
			}
			request.Raw["reasoning_effort"] = effort
		}
	}
	shortMap, reverseMap := buildCodexToolNameMaps(request.Tools)
	var body map[string]any
	if request.Source == canonical.ProtocolResponses && request.Raw != nil {
		encoded, _ := json.Marshal(request.Raw)
		_ = json.Unmarshal(encoded, &body)
	}
	if body == nil {
		body = map[string]any{}
	}
	instructions := codexInstructionsString(request)
	if instructions != "" {
		body["instructions"] = instructions
	} else {
		delete(body, "instructions")
	}
	if _, hasInput := body["input"]; hasInput {
		body["input"] = codexNormalizeInputValue(body["input"], shortMap)
	} else {
		body["input"] = codexMessagesToInput(codexMessagesWithoutInstructions(request.Messages), shortMap)
	}
	body["model"] = request.UpstreamModel
	body["stream"] = true
	// Codex /responses rejects token-limit and sampling fields.
	delete(body, "max_output_tokens")
	delete(body, "max_completion_tokens")
	delete(body, "max_tokens")
	delete(body, "temperature")
	delete(body, "top_p")
	if len(request.Tools) > 0 {
		body["tools"] = codexToolsWithShortNames(request.Tools, shortMap)
		body["parallel_tool_calls"] = true
	}
	if request.ToolChoice != nil {
		body["tool_choice"] = codexToolChoice(request.ToolChoice, shortMap)
	}
	codexNormalizeReasoning(body)
	for key, value := range request.Reasoning {
		body[key] = value
	}
	// Codex /responses rejects requests unless store is explicitly false.
	body["store"] = false
	delete(body, "previous_response_id")
	delete(body, "prompt_cache_retention")
	delete(body, "safety_identifier")
	body["_codex_reverse_tool_names"] = reverseMap
	return body
}

func codexNormalizeReasoning(body map[string]any) {
	effort := ""
	if reasoning, ok := body["reasoning"].(map[string]any); ok {
		effort = strings.TrimSpace(stringValue(reasoning["effort"]))
		if stringValue(reasoning["summary"]) == "" {
			reasoning["summary"] = "auto"
		}
	}
	if effort == "" {
		if value, ok := body["reasoning_effort"].(string); ok {
			effort = strings.TrimSpace(value)
		}
	}
	delete(body, "reasoning_effort")
	if effort == "" {
		effort = "low"
	}
	if effort == "none" {
		delete(body, "reasoning")
		delete(body, "include")
		return
	}
	body["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
	body["include"] = []any{"reasoning.encrypted_content"}
}

func codexToolChoice(toolChoice any, shortMap map[string]string) any {
	mapped, ok := toolChoice.(map[string]any)
	if !ok {
		return toolChoice
	}
	if stringValue(mapped["type"]) != "function" {
		return toolChoice
	}
	function, _ := mapped["function"].(map[string]any)
	name := stringValue(function["name"])
	if name == "" {
		return toolChoice
	}
	shortName := shortMap[name]
	if shortName == "" {
		shortName = name
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": shortName,
		},
	}
}

func codexReverseToolNames(body map[string]any) map[string]string {
	reverse, _ := body["_codex_reverse_tool_names"].(map[string]string)
	delete(body, "_codex_reverse_tool_names")
	if reverse == nil {
		return map[string]string{}
	}
	return reverse
}

func codexTools(tools []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if function, ok := tool["function"].(map[string]any); ok {
			mapped := map[string]any{"type": "function", "name": function["name"], "description": function["description"], "parameters": function["parameters"]}
			result = append(result, mapped)
		} else {
			result = append(result, tool)
		}
	}
	return result
}

func codexHeaders(provider store.Provider, credential store.Credential, streaming bool, request canonical.Request) http.Header {
	headers := authHeaders(provider, credential)
	applyCodexClientHeaders(headers, clientHeadersFromRequest(request))
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", "codex_cli_rs/0.125.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464")
	}
	if headers.Get("Originator") == "" && credential.AuthType == "oauth" {
		headers.Set("Originator", "codex_cli_rs")
	}
	if credential.OAuthToken != nil && credential.OAuthToken.Extra != nil {
		if accountID := stringValue(firstValue(credential.OAuthToken.Extra, "account_id", "chatgpt_account_id")); accountID != "" && headers.Get("ChatGPT-Account-ID") == "" {
			headers.Set("ChatGPT-Account-ID", accountID)
		}
	}
	if streaming {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "application/json")
	}
	headers.Set("Connection", "keep-alive")
	return headers
}

func (a *codexAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
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
			if event.ID != "" {
				result.ID = event.ID
			}
			if event.Model != "" {
				result.Model = event.Model
			}
			text.WriteString(event.Text)
		case canonical.EventReasoningDelta:
			if event.ID != "" {
				result.ID = event.ID
			}
			if event.Model != "" {
				result.Model = event.Model
			}
			reasoning.WriteString(event.Reasoning)
		case canonical.EventToolCallDelta:
			if event.ID != "" {
				result.ID = event.ID
			}
			if event.Model != "" {
				result.Model = event.Model
			}
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

func (a *codexAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	body := codexBody(request)
	reverseMap := codexReverseToolNames(body)
	response, err := executeJSON(ctx, a.client, http.MethodPost, endpoint(provider.BaseURL, "/responses"), correlationHeaders(codexHeaders(provider, credential, true, request), request.RequestID), body)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, upstreamError(response)
	}
	out := make(chan canonical.Event, 16)
	go func() {
		defer close(out)
		defer response.Body.Close()
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if strings.Contains(contentType, "json") {
			var raw map[string]any
			if decodeErr := json.NewDecoder(response.Body).Decode(&raw); decodeErr != nil {
				out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "invalid_upstream_response", Err: decodeErr}}
				return
			}
			codexEventsFromJSON(out, raw, reverseMap)
			return
		}
		if request.Source == canonical.ProtocolResponses {
			parseCodexResponsesPassthroughSSE(ctx, response.Body, out)
			return
		}
		parseCodexSSE(ctx, response.Body, out, reverseMap)
	}()
	return out, nil
}

func parseCodexSSE(ctx context.Context, body io.Reader, out chan<- canonical.Event, reverseToolNames map[string]string) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	state := newCodexStreamState(reverseToolNames)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if state.eventCount > 0 && !state.hasCompletedEvent && !state.hasIncompleteEvent && !state.hasFailedEvent {
				out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "codex_stream_missing_response_completed", Message: "Codex stream ended before response.completed"}}
			}
			out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "stop"}
			return
		}
		var raw map[string]any
		if json.Unmarshal([]byte(data), &raw) != nil {
			continue
		}
		for _, event := range translateCodexEvent(raw, state) {
			out <- event
		}
	}
	if err := scanner.Err(); err != nil {
		out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "upstream_stream_error", Err: err}}
	}
}

func codexEventsFromJSON(out chan<- canonical.Event, raw map[string]any, reverseToolNames map[string]string) {
	state := newCodexStreamState(reverseToolNames)
	if response, ok := raw["response"].(map[string]any); ok {
		raw = response
	}
	events := translateCodexEvent(map[string]any{"type": "response.created", "response": raw}, state)
	for _, event := range events {
		out <- event
	}
	if output, ok := raw["output"].([]any); ok {
		for _, item := range output {
			mapped, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch stringValue(mapped["type"]) {
			case "reasoning":
				for _, event := range translateCodexEvent(map[string]any{"type": "response.output_item.done", "item": mapped}, state) {
					out <- event
				}
			case "message":
				for _, event := range translateCodexEvent(map[string]any{"type": "response.output_item.done", "item": mapped}, state) {
					out <- event
				}
			case "function_call":
				for _, event := range translateCodexEvent(map[string]any{"type": "response.output_item.done", "item": mapped}, state) {
					out <- event
				}
			}
		}
	}
	for _, event := range translateCodexEvent(map[string]any{"type": "response.completed", "response": raw}, state) {
		out <- event
	}
}

func parseResponsesUsage(value any) canonical.Usage {
	usage, _ := value.(map[string]any)
	return canonical.Usage{InputTokens: numberValue(firstValue(usage, "input_tokens", "prompt_tokens")), OutputTokens: numberValue(firstValue(usage, "output_tokens", "completion_tokens")), ReasoningTokens: numberValue(firstValue(usage, "reasoning_tokens"))}
}

func nestedMapValue(raw map[string]any, parent, child string) any {
	item, _ := raw[parent].(map[string]any)
	return item[child]
}

func firstAny(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

type anthropicAdapter struct{ client *http.Client }

func anthropicBody(request canonical.Request) map[string]any {
	if request.Source == canonical.ProtocolClaude && len(request.Raw) > 0 {
		body := cloneAnyMap(request.Raw)
		body["model"] = request.UpstreamModel
		body["stream"] = request.Stream
		if request.MaxTokens > 0 {
			body["max_tokens"] = request.MaxTokens
		}
		return body
	}
	if request.Source == canonical.ProtocolOpenAI && len(request.Raw) > 0 {
		return claudeopenai.OpenAIToClaudeRequest(request.UpstreamModel, request.Raw, request.Stream)
	}
	if request.Source == canonical.ProtocolOpenAI || request.Source == canonical.ProtocolResponses {
		openAI := buildOpenAIBody(request)
		return claudeopenai.OpenAIToClaudeRequest(request.UpstreamModel, openAI, request.Stream)
	}
	messages := make([]canonical.Message, 0, len(request.Messages))
	var systemParts []string
	for _, message := range request.Messages {
		if message.Role == "system" {
			if text := stringValue(message.Content); text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		messages = append(messages, message)
	}
	body := map[string]any{"model": request.UpstreamModel, "messages": messages, "max_tokens": request.MaxTokens, "stream": request.Stream}
	if request.MaxTokens <= 0 {
		body["max_tokens"] = 4096
	}
	if request.System != nil {
		body["system"] = request.System
	} else if len(systemParts) > 0 {
		body["system"] = strings.Join(systemParts, "\n\n")
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			if fn, ok := tool["function"].(map[string]any); ok {
				tools = append(tools, map[string]any{"name": fn["name"], "description": fn["description"], "input_schema": fn["parameters"]})
			} else {
				tools = append(tools, tool)
			}
		}
		body["tools"] = tools
	}
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	return body
}

func anthropicHeaders(provider store.Provider, credential store.Credential, request canonical.Request) http.Header {
	headers := authHeaders(provider, credential)
	if credential.AuthType == "oauth" {
		if headers.Get("anthropic-version") == "" {
			headers.Set("anthropic-version", "2023-06-01")
		}
		if provider.Type == "claude" {
			if headers.Get("Anthropic-Beta") == "" {
				headers.Set("Anthropic-Beta", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14")
			}
			if headers.Get("X-App") == "" {
				headers.Set("X-App", "cli")
			}
			if headers.Get("User-Agent") == "" {
				headers.Set("User-Agent", "claude-cli/2.1.63 (external, cli)")
			}
			if headers.Get("X-Stainless-Runtime") == "" {
				headers.Set("X-Stainless-Runtime", "node")
			}
			if headers.Get("X-Stainless-Lang") == "" {
				headers.Set("X-Stainless-Lang", "js")
			}
		}
		applyClaudeCodeCompatibilityHeaders(headers, clientHeadersFromRequest(request))
		return headers
	}
	headers.Del("Authorization")
	if credential.Secret != "" {
		headers.Set("x-api-key", credential.Secret)
	}
	if headers.Get("anthropic-version") == "" {
		headers.Set("anthropic-version", "2023-06-01")
	}
	applyClaudeCodeCompatibilityHeaders(headers, clientHeadersFromRequest(request))
	return headers
}

func clientHeadersFromRequest(request canonical.Request) map[string]string {
	if request.Metadata == nil {
		return nil
	}
	client, _ := request.Metadata["client_headers"].(map[string]string)
	return client
}

func anthropicMessagesEndpoint(provider store.Provider) string {
	path := "/v1/messages"
	if provider.Type == "claude" {
		path += "?beta=true"
	}
	return endpoint(provider.BaseURL, path)
}

func (a *anthropicAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	ctx = withCredentialProxy(ctx, credential)
	request.Stream = false
	response, err := executeJSON(ctx, a.client, http.MethodPost, anthropicMessagesEndpoint(provider), correlationHeaders(anthropicHeaders(provider, credential, request), request.RequestID), anthropicBody(request))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, upstreamError(response)
	}
	var raw map[string]any
	if err = json.NewDecoder(response.Body).Decode(&raw); err != nil {
		return nil, err
	}
	result := &canonical.Response{Raw: raw, ID: stringValue(raw["id"]), Model: stringValue(raw["model"]), Role: "assistant", FinishReason: stringValue(raw["stop_reason"])}
	blocks, _ := raw["content"].([]any)
	var text strings.Builder
	for _, value := range blocks {
		block, _ := value.(map[string]any)
		switch stringValue(block["type"]) {
		case "text":
			text.WriteString(stringValue(block["text"]))
		case "thinking":
			result.Reasoning += stringValue(block["thinking"])
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, map[string]any{"id": block["id"], "type": "function", "function": map[string]any{"name": block["name"], "arguments": marshalString(block["input"])}})
		}
	}
	result.Content = text.String()
	result.Usage = parseClaudeUsage(raw["usage"])
	return result, nil
}

func (a *anthropicAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	request.Stream = true
	headers := correlationHeaders(anthropicHeaders(provider, credential, request), request.RequestID)
	headers.Set("Accept", "text/event-stream")
	response, err := executeJSON(ctx, a.client, http.MethodPost, anthropicMessagesEndpoint(provider), headers, anthropicBody(request))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, upstreamError(response)
	}
	out := make(chan canonical.Event, 16)
	go func() {
		defer close(out)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 2<<20)
		// Anthropic splits prompt/cache usage into message_start and output usage
		// into message_delta. Emit cumulative snapshots because the router records
		// the latest usage event as authoritative.
		var streamUsage canonical.Usage
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var raw map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &raw) != nil {
				continue
			}
			switch stringValue(raw["type"]) {
			case "message_start":
				msg, _ := raw["message"].(map[string]any)
				out <- canonical.Event{Type: canonical.EventMessageStart, ID: stringValue(msg["id"]), Model: stringValue(msg["model"])}
				if usage, ok := msg["usage"].(map[string]any); ok {
					streamUsage = mergeStreamUsage(streamUsage, parseClaudeUsage(usage))
					current := streamUsage
					out <- canonical.Event{Type: canonical.EventUsage, Usage: &current}
				}
			case "content_block_delta":
				delta, _ := raw["delta"].(map[string]any)
				if text := stringValue(delta["text"]); text != "" {
					out <- canonical.Event{Type: canonical.EventTextDelta, Text: text}
				}
				if thinking := stringValue(delta["thinking"]); thinking != "" {
					out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: thinking}
				}
				if inputJSON := stringValue(delta["partial_json"]); inputJSON != "" {
					out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: map[string]any{"type": "function", "function": map[string]any{"arguments": inputJSON}}}
				}
			case "content_block_start":
				block, _ := raw["content_block"].(map[string]any)
				if stringValue(block["type"]) == "tool_use" {
					out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: map[string]any{"id": block["id"], "type": "function", "function": map[string]any{"name": block["name"], "arguments": ""}}}
				}
			case "message_delta":
				delta, _ := raw["delta"].(map[string]any)
				if usage, ok := raw["usage"].(map[string]any); ok {
					streamUsage = mergeStreamUsage(streamUsage, parseClaudeUsage(usage))
					current := streamUsage
					out <- canonical.Event{Type: canonical.EventUsage, Usage: &current}
				}
				if stop := stringValue(delta["stop_reason"]); stop != "" {
					out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: stop}
					return
				}
			case "message_stop":
				out <- canonical.Event{Type: canonical.EventMessageEnd}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			out <- canonical.Event{Type: canonical.EventError, Err: err}
		}
	}()
	return out, nil
}

type geminiAdapter struct{ client *http.Client }

func geminiParts(content any) []map[string]any {
	switch value := content.(type) {
	case string:
		return []map[string]any{{"text": value}}
	case []any:
		var parts []map[string]any
		for _, item := range value {
			if block, ok := item.(map[string]any); ok {
				switch stringValue(block["type"]) {
				case "tool_use":
					parts = append(parts, map[string]any{"functionCall": map[string]any{"id": block["id"], "name": block["name"], "args": block["input"]}})
					continue
				case "tool_result":
					parts = append(parts, map[string]any{"functionResponse": map[string]any{"id": block["tool_use_id"], "name": block["name"], "response": map[string]any{"result": block["content"]}}})
					continue
				}
				if text := stringValue(block["text"]); text != "" {
					parts = append(parts, map[string]any{"text": text})
				}
			}
		}
		return parts
	default:
		return []map[string]any{{"text": fmt.Sprint(value)}}
	}
}

func geminiMessageParts(message canonical.Message) []map[string]any {
	parts := geminiParts(message.Content)
	for _, call := range message.ToolCalls {
		function, _ := call["function"].(map[string]any)
		arguments := any(map[string]any{})
		if raw := stringValue(function["arguments"]); raw != "" {
			_ = json.Unmarshal([]byte(raw), &arguments)
		}
		parts = append(parts, map[string]any{"functionCall": map[string]any{"id": call["id"], "name": function["name"], "args": arguments}})
	}
	if message.Role == "tool" {
		name := message.Name
		if name == "" {
			name = message.ToolCallID
		}
		parts = []map[string]any{{"functionResponse": map[string]any{"id": message.ToolCallID, "name": name, "response": map[string]any{"result": message.Content}}}}
	}
	return parts
}

func geminiTools(tools []map[string]any) []map[string]any {
	declarations := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		function := tool
		if nested, ok := tool["function"].(map[string]any); ok {
			function = nested
		}
		name := stringValue(function["name"])
		if name == "" {
			continue
		}
		declaration := map[string]any{"name": name}
		if description := stringValue(function["description"]); description != "" {
			declaration["description"] = description
		}
		if schema := firstAny(function["parameters"], function["input_schema"], function["parametersJsonSchema"]); schema != nil {
			declaration["parameters"] = schema
		}
		declarations = append(declarations, declaration)
	}
	if len(declarations) == 0 {
		return nil
	}
	return []map[string]any{{"functionDeclarations": declarations}}
}

func geminiToolConfig(choice any) map[string]any {
	if choice == nil {
		return nil
	}
	mode := "AUTO"
	var allowed []string
	switch value := choice.(type) {
	case string:
		switch strings.ToLower(value) {
		case "none":
			mode = "NONE"
		case "required", "any":
			mode = "ANY"
		}
	case map[string]any:
		typeName := strings.ToLower(stringValue(value["type"]))
		switch typeName {
		case "none":
			mode = "NONE"
		case "any", "required", "tool", "function":
			mode = "ANY"
		}
		if function, ok := value["function"].(map[string]any); ok {
			if name := stringValue(function["name"]); name != "" {
				allowed = append(allowed, name)
			}
		}
		if name := stringValue(value["name"]); name != "" {
			allowed = append(allowed, name)
		}
	}
	config := map[string]any{"mode": mode}
	if len(allowed) > 0 {
		config["allowedFunctionNames"] = allowed
	}
	return map[string]any{"functionCallingConfig": config}
}

func geminiBody(request canonical.Request) map[string]any {
	if request.Source == canonical.ProtocolGemini && len(request.Raw) > 0 {
		return cloneAnyMap(request.Raw)
	}
	contents := make([]map[string]any, 0, len(request.Messages))
	var systemParts []string
	for _, message := range request.Messages {
		role := message.Role
		if role == "assistant" {
			role = "model"
		} else if role == "tool" {
			role = "user"
		}
		if role == "system" {
			if text := stringValue(message.Content); text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		contents = append(contents, map[string]any{"role": role, "parts": geminiMessageParts(message)})
	}
	body := map[string]any{"contents": contents}
	generation := map[string]any{}
	if request.Temperature != nil {
		generation["temperature"] = *request.Temperature
	}
	if request.MaxTokens > 0 {
		generation["maxOutputTokens"] = request.MaxTokens
	}
	if len(generation) > 0 {
		body["generationConfig"] = generation
	}
	if request.System != nil {
		body["systemInstruction"] = map[string]any{"parts": geminiParts(request.System)}
	} else if len(systemParts) > 0 {
		body["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": strings.Join(systemParts, "\n\n")}}}
	}
	if tools := geminiTools(request.Tools); len(tools) > 0 {
		body["tools"] = tools
	}
	if toolConfig := geminiToolConfig(request.ToolChoice); toolConfig != nil {
		body["toolConfig"] = toolConfig
	}
	return body
}

func canonicalMessages(value any) []canonical.Message {
	items, _ := value.([]any)
	result := make([]canonical.Message, 0, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, canonical.Message{
			Role:       stringValue(mapped["role"]),
			Content:    mapped["content"],
			Name:       stringValue(mapped["name"]),
			ToolCallID: stringValue(mapped["tool_call_id"]),
			ToolCalls:  mapSlice(mapped["tool_calls"]),
		})
	}
	return result
}

func cloneAnyMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func geminiURL(provider store.Provider, credential store.Credential, model, action string) string {
	if provider.Type == "vertex" {
		base := strings.TrimRight(provider.BaseURL, "/")
		if strings.Contains(base, "/publishers/") {
			return base + "/models/" + url.PathEscape(model) + ":" + action
		}
		project := stringValue(provider.Config["project"])
		location := stringValue(provider.Config["location"])
		publisher := stringValue(provider.Config["publisher"])
		if publisher == "" {
			publisher = "google"
		}
		if location == "" {
			location = "global"
		}
		return strings.TrimRight(base, "/") + "/v1/projects/" + url.PathEscape(project) + "/locations/" + url.PathEscape(location) + "/publishers/" + url.PathEscape(publisher) + "/models/" + url.PathEscape(model) + ":" + action
	}
	target := endpoint(provider.BaseURL, "/v1beta/models/") + url.PathEscape(model) + ":" + action
	if credential.Secret != "" && credential.AuthType != "oauth" {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + "key=" + url.QueryEscape(credential.Secret)
	}
	return target
}

func (a *geminiAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	ctx = withCredentialProxy(ctx, credential)
	headers := correlationHeaders(authHeaders(provider, credential), request.RequestID)
	if provider.Type != "vertex" {
		headers.Del("Authorization")
	}
	response, err := executeJSON(ctx, a.client, http.MethodPost, geminiURL(provider, credential, request.UpstreamModel, "generateContent"), headers, geminiBody(request))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, upstreamError(response)
	}
	var raw map[string]any
	if err = json.NewDecoder(response.Body).Decode(&raw); err != nil {
		return nil, err
	}
	result := &canonical.Response{Raw: raw, Model: request.UpstreamModel, Role: "assistant"}
	candidates, _ := raw["candidates"].([]any)
	if len(candidates) > 0 {
		candidate, _ := candidates[0].(map[string]any)
		result.FinishReason = stringValue(candidate["finishReason"])
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		var text strings.Builder
		for _, item := range parts {
			part, _ := item.(map[string]any)
			if boolValue(part["thought"]) {
				result.Reasoning += stringValue(part["text"])
			} else {
				text.WriteString(stringValue(part["text"]))
			}
			if call, ok := part["functionCall"].(map[string]any); ok {
				result.ToolCalls = append(result.ToolCalls, map[string]any{"type": "function", "function": map[string]any{"name": call["name"], "arguments": marshalString(call["args"])}})
			}
		}
		result.Content = text.String()
	}
	result.Usage = parseGeminiUsage(raw["usageMetadata"])
	return result, nil
}

func (a *geminiAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	headers := correlationHeaders(authHeaders(provider, credential), request.RequestID)
	if provider.Type != "vertex" {
		headers.Del("Authorization")
	}
	headers.Set("Accept", "text/event-stream")
	target := geminiURL(provider, credential, request.UpstreamModel, "streamGenerateContent")
	if strings.Contains(target, "?") {
		target += "&alt=sse"
	} else {
		target += "?alt=sse"
	}
	response, err := executeJSON(ctx, a.client, http.MethodPost, target, headers, geminiBody(request))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, upstreamError(response)
	}
	out := make(chan canonical.Event, 16)
	go func() {
		defer close(out)
		defer response.Body.Close()
		out <- canonical.Event{Type: canonical.EventMessageStart, Model: request.UpstreamModel}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 2<<20)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var raw map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &raw) != nil {
				continue
			}
			if usage, ok := raw["usageMetadata"].(map[string]any); ok {
				// Gemini commonly attaches usage to the same chunk as finishReason.
				u := parseGeminiUsage(usage)
				out <- canonical.Event{Type: canonical.EventUsage, Usage: &u}
			}
			candidates, _ := raw["candidates"].([]any)
			if len(candidates) > 0 {
				candidate, _ := candidates[0].(map[string]any)
				content, _ := candidate["content"].(map[string]any)
				parts, _ := content["parts"].([]any)
				for _, item := range parts {
					part, _ := item.(map[string]any)
					if text := stringValue(part["text"]); text != "" {
						if boolValue(part["thought"]) {
							out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: text}
						} else {
							out <- canonical.Event{Type: canonical.EventTextDelta, Text: text}
						}
					}
					if call, ok := part["functionCall"].(map[string]any); ok {
						out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: map[string]any{"id": call["id"], "type": "function", "function": map[string]any{"name": call["name"], "arguments": marshalString(call["args"])}}}
					}
				}
				if finish := stringValue(candidate["finishReason"]); finish != "" {
					out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: finish}
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			out <- canonical.Event{Type: canonical.EventError, Err: err}
		} else {
			out <- canonical.Event{Type: canonical.EventMessageEnd}
		}
	}()
	return out, nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, exists := values[key]; exists {
			return value
		}
	}
	return nil
}
func numberValue(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}
func boolValue(value any) bool { result, _ := value.(bool); return result }
func mapSlice(value any) []map[string]any {
	items, _ := value.([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			result = append(result, mapped)
		}
	}
	return result
}
func marshalString(value any) string { data, _ := json.Marshal(value); return string(data) }
func parseOpenAIUsage(value any) canonical.Usage {
	usage, _ := value.(map[string]any)
	result := canonical.Usage{InputTokens: numberValue(firstValue(usage, "prompt_tokens", "input_tokens")), OutputTokens: numberValue(firstValue(usage, "completion_tokens", "output_tokens"))}
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		result.ReasoningTokens = numberValue(details["reasoning_tokens"])
	}
	return result
}
func parseClaudeUsage(value any) canonical.Usage {
	usage, _ := value.(map[string]any)
	return canonical.Usage{InputTokens: numberValue(usage["input_tokens"]), OutputTokens: numberValue(usage["output_tokens"]), CachedTokens: numberValue(usage["cache_read_input_tokens"])}
}

// mergeStreamUsage preserves fields omitted from partial stream usage chunks.
func mergeStreamUsage(current, update canonical.Usage) canonical.Usage {
	if update.InputTokens > 0 {
		current.InputTokens = update.InputTokens
	}
	if update.OutputTokens > 0 {
		current.OutputTokens = update.OutputTokens
	}
	if update.ReasoningTokens > 0 {
		current.ReasoningTokens = update.ReasoningTokens
	}
	if update.CachedTokens > 0 {
		current.CachedTokens = update.CachedTokens
	}
	return current
}
func parseGeminiUsage(value any) canonical.Usage {
	usage, _ := value.(map[string]any)
	return canonical.Usage{InputTokens: numberValue(usage["promptTokenCount"]), OutputTokens: numberValue(usage["candidatesTokenCount"]), ReasoningTokens: numberValue(usage["thoughtsTokenCount"])}
}

func Status(err error) int {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Status
	}
	return 0
}
func Code(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr.Code != "" {
		return providerErr.Code
	}
	return "provider_error"
}

func RetryAfter(err error) string {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.RetryAfter
	}
	return ""
}
