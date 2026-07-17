package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

const antigravityRuntimeUserAgent = "antigravity/hub/2.2.1 darwin/arm64"

type antigravityAdapter struct{ client *http.Client }

func antigravityProject(credential store.Credential) string {
	if credential.OAuthToken != nil && credential.OAuthToken.Extra != nil {
		if project := stringValue(firstValue(credential.OAuthToken.Extra, "project_id", "projectId", "project")); project != "" {
			return project
		}
	}
	return stringValue(firstValue(credential.Metadata, "project_id", "projectId", "project"))
}

func antigravityBody(request canonical.Request, credential store.Credential) (map[string]any, error) {
	projectID := antigravityProject(credential)
	if projectID == "" {
		return nil, &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: "Antigravity credential is missing project_id; reconnect the OAuth account"}
	}
	inner := geminiBody(request)
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(request.RequestID)
	}
	if sessionID != "" {
		inner["sessionId"] = sessionID
	}
	requestID := strings.TrimSpace(request.RequestID)
	if requestID == "" {
		requestID = "tproxy-request"
	}
	return map[string]any{
		"model":       request.UpstreamModel,
		"project":     projectID,
		"requestId":   "agent-" + requestID,
		"requestType": "agent",
		"userAgent":   "antigravity",
		"request":     inner,
	}, nil
}

func antigravityHeaders(provider store.Provider, credential store.Credential, requestID string, stream bool) http.Header {
	headers := correlationHeaders(authHeaders(provider, credential), requestID)
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", antigravityRuntimeUserAgent)
	}
	if stream {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "application/json")
	}
	return headers
}

func antigravityURL(provider store.Provider, stream bool) string {
	path := "/v1internal:generateContent"
	if stream {
		path = "/v1internal:streamGenerateContent"
	}
	target := endpoint(provider.BaseURL, path)
	if stream {
		target += "?alt=sse"
	}
	return target
}

func (a *antigravityAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	ctx = withCredentialProxy(ctx, credential)
	body, err := antigravityBody(request, credential)
	if err != nil {
		return nil, err
	}
	response, err := executeJSON(ctx, a.client, http.MethodPost, antigravityURL(provider, false), antigravityHeaders(provider, credential, request.RequestID, false), body)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, upstreamError(response)
	}
	var raw map[string]any
	if err = json.NewDecoder(response.Body).Decode(&raw); err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "invalid_upstream_response", Err: err}
	}
	raw = unwrapAntigravityResponse(raw)
	return canonicalGeminiResponse(raw, request.UpstreamModel), nil
}

func (a *antigravityAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	body, err := antigravityBody(request, credential)
	if err != nil {
		return nil, err
	}
	response, err := executeJSON(ctx, a.client, http.MethodPost, antigravityURL(provider, true), antigravityHeaders(provider, credential, request.RequestID, true), body)
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
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		ended := false
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
			if data == "" || data == "[DONE]" {
				continue
			}
			var raw map[string]any
			if json.Unmarshal([]byte(data), &raw) != nil {
				continue
			}
			raw = unwrapAntigravityResponse(raw)
			if emitGeminiEvents(out, raw) {
				ended = true
				return
			}
		}
		if err := scanner.Err(); err != nil {
			out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: http.StatusBadGateway, Code: "upstream_stream_error", Err: err}}
			return
		}
		if !ended {
			out <- canonical.Event{Type: canonical.EventMessageEnd}
		}
	}()
	return out, nil
}

func unwrapAntigravityResponse(raw map[string]any) map[string]any {
	if response, ok := raw["response"].(map[string]any); ok {
		raw = response
	}
	if usage, ok := raw["cpaUsageMetadata"].(map[string]any); ok {
		if _, exists := raw["usageMetadata"]; !exists {
			raw["usageMetadata"] = usage
		}
		delete(raw, "cpaUsageMetadata")
	}
	return raw
}

func canonicalGeminiResponse(raw map[string]any, model string) *canonical.Response {
	result := &canonical.Response{Raw: raw, Model: stringValue(firstValue(raw, "modelVersion", "model")), Role: "assistant"}
	if result.Model == "" {
		result.Model = model
	}
	candidates, _ := raw["candidates"].([]any)
	if len(candidates) > 0 {
		candidate, _ := candidates[0].(map[string]any)
		result.FinishReason = stringValue(candidate["finishReason"])
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		var text, reasoning strings.Builder
		for _, item := range parts {
			part, _ := item.(map[string]any)
			partText := stringValue(part["text"])
			if thought, _ := part["thought"].(bool); thought {
				reasoning.WriteString(partText)
			} else {
				text.WriteString(partText)
			}
			if call, ok := part["functionCall"].(map[string]any); ok {
				result.ToolCalls = append(result.ToolCalls, geminiToolCall(call))
			}
		}
		result.Content = text.String()
		result.Reasoning = reasoning.String()
	}
	result.Usage = parseGeminiUsage(firstAny(raw["usageMetadata"], raw["cpaUsageMetadata"]))
	return result
}

func emitGeminiEvents(out chan<- canonical.Event, raw map[string]any) bool {
	candidates, _ := raw["candidates"].([]any)
	for _, item := range candidates {
		candidate, _ := item.(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, value := range parts {
			part, _ := value.(map[string]any)
			if text := stringValue(part["text"]); text != "" {
				if thought, _ := part["thought"].(bool); thought {
					out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: text}
				} else {
					out <- canonical.Event{Type: canonical.EventTextDelta, Text: text}
				}
			}
			if call, ok := part["functionCall"].(map[string]any); ok {
				out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: geminiToolCall(call)}
			}
		}
		if finish := stringValue(candidate["finishReason"]); finish != "" {
			if usage := geminiUsageEvent(raw); usage != nil {
				out <- canonical.Event{Type: canonical.EventUsage, Usage: usage}
			}
			out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: finish}
			return true
		}
	}
	if usage := geminiUsageEvent(raw); usage != nil {
		out <- canonical.Event{Type: canonical.EventUsage, Usage: usage}
	}
	return false
}

func geminiUsageEvent(raw map[string]any) *canonical.Usage {
	value := firstAny(raw["usageMetadata"], raw["cpaUsageMetadata"])
	usage, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	parsed := parseGeminiUsage(usage)
	return &parsed
}

func geminiToolCall(call map[string]any) map[string]any {
	arguments, err := json.Marshal(call["args"])
	if err != nil {
		arguments = []byte("{}")
	}
	return map[string]any{
		"id":   stringValue(firstValue(call, "id", "call_id")),
		"type": "function",
		"function": map[string]any{
			"name":      stringValue(call["name"]),
			"arguments": string(arguments),
		},
	}
}

var _ Adapter = (*antigravityAdapter)(nil)
