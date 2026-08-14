package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/antigravity"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

const antigravityMaxOutputTokens = 64_000

type antigravityAdapter struct{ client *http.Client }

func antigravityProject(credential store.Credential) string {
	if credential.OAuthToken != nil {
		if project := antigravityProjectFromValues(credential.OAuthToken.Extra); project != "" {
			return project
		}
	}
	return antigravityProjectFromValues(credential.Metadata)
}

func antigravityProjectFromValues(values map[string]any) string {
	if values == nil {
		return ""
	}
	for _, key := range []string{"project_id", "projectId", "cloudaicompanionProject", "cloudaicompanion_project", "project"} {
		if project := antigravityProjectValue(values[key]); project != "" {
			return project
		}
	}
	return ""
}

func antigravityProjectValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		for _, key := range []string{"id", "project_id", "projectId", "cloudaicompanionProject", "cloudaicompanion_project"} {
			if project := antigravityProjectValue(typed[key]); project != "" {
				return project
			}
		}
	}
	return ""
}

func antigravityBody(request canonical.Request, credential store.Credential) (map[string]any, error) {
	body, _, err := antigravityPreparedBody(request, credential)
	return body, err
}

// antigravityPreparedBody converts a canonical Gemini request into the Cloud
// Code envelope and retains the request-local tool-name mapping needed to
// restore caller-visible names in the upstream response.
func antigravityPreparedBody(request canonical.Request, credential store.Credential) (map[string]any, map[string]string, error) {
	projectID := antigravityProject(credential)
	if projectID == "" {
		return nil, nil, &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: "Antigravity credential is missing project_id; reconnect the OAuth account"}
	}
	inner := cloneAntigravityMap(geminiBody(request))
	reverseToolNames := normalizeAntigravityRequest(inner)
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(request.RequestID)
	}
	if sessionID != "" {
		inner["sessionId"] = sessionID
	}
	requestType := "agent"
	requestID := antigravityIDERequestID(sessionID, request.UpstreamModel, requestType, antigravityContentCount(inner))
	if antigravityIsImageModel(request.UpstreamModel) {
		requestType = "image_gen"
		requestID = antigravityImageRequestID()
	}
	return map[string]any{
		"model":   request.UpstreamModel,
		"project": projectID,
		// Cloud Code validates this as an IDE request identifier. Never send an
		// arbitrary caller-supplied request ID to that field.
		"requestId":   requestID,
		"requestType": requestType,
		"userAgent":   "antigravity",
		"request":     inner,
	}, reverseToolNames, nil
}

// antigravityIsImageModel reports whether a model routes through Cloud Code's
// image generation path, which uses a different request type, request ID format
// and transport than the agent path.
func antigravityIsImageModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "image")
}

func antigravityRequestID() string { return "agent-" + uuid.NewString() }

// antigravityImageRequestID mirrors the identifier the Antigravity IDE sends
// for image generation. The agent-scoped form is rejected on that path.
func antigravityImageRequestID() string {
	return fmt.Sprintf("image_gen/%d/%s/12", time.Now().UnixMilli(), uuid.NewString())
}

// antigravityHeaders mirrors what the Antigravity IDE puts on the wire.
//
// The correlation header tproxy adds everywhere else is deliberately left off:
// no real Antigravity client sends X-Request-ID, so it only serves to mark the
// request as coming from something other than the IDE. The request already
// carries its own requestId inside the Cloud Code envelope, which is where the
// upstream looks.
func antigravityHeaders(provider store.Provider, credential store.Credential, stream bool) http.Header {
	headers := authHeaders(provider, credential)
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", antigravity.UserAgent())
	}
	if stream {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "application/json")
	}
	return headers
}

// antigravityActionURL builds a Cloud Code action URL against one host.
func antigravityActionURL(baseURL string, stream bool) string {
	path := "/v1internal:generateContent"
	if stream {
		path = "/v1internal:streamGenerateContent"
	}
	target := endpoint(antigravityBaseURL(baseURL), path)
	if stream {
		target += "?alt=sse"
	}
	return target
}

// antigravityStreamable reports whether a request may use
// streamGenerateContent. Cloud Code only serves image generation over the
// unary endpoint, so a streaming image request has to be issued as a single
// call and adapted back into events for the caller.
func antigravityStreamable(request canonical.Request) bool {
	return !antigravityIsImageModel(request.UpstreamModel)
}

// antigravityBaseURL normalizes 9router's legacy Gemini CLI preset, which
// includes /v1internal even though Cloud Code action URLs already include that
// namespace as /v1internal:<action>.
func antigravityBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(strings.ToLower(baseURL), "/v1internal") {
		baseURL = strings.TrimRight(baseURL[:len(baseURL)-len("/v1internal")], "/")
	}
	return baseURL
}

func (a *antigravityAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	ctx = withCredentialProxy(ctx, credential)
	body, reverseToolNames, err := antigravityPreparedBody(request, credential)
	if err != nil {
		return nil, err
	}
	headers := antigravityHeaders(provider, credential, false)
	var lastErr error
	for _, baseURL := range antigravityBaseURLs(provider) {
		response, execErr := executeJSON(ctx, a.client, http.MethodPost, antigravityActionURL(baseURL, false), headers, body)
		if execErr != nil {
			lastErr = &ProviderError{Code: "upstream_network", Err: execErr}
			if antigravityShouldTryNextHost(lastErr) {
				continue
			}
			return nil, lastErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			lastErr = antigravityUpstreamError(response)
			response.Body.Close()
			if antigravityShouldTryNextHost(lastErr) {
				continue
			}
			return nil, lastErr
		}
		var raw map[string]any
		decodeErr := json.NewDecoder(response.Body).Decode(&raw)
		response.Body.Close()
		if decodeErr != nil {
			return nil, &ProviderError{Status: http.StatusBadGateway, Code: "invalid_upstream_response", Err: decodeErr}
		}
		raw = unwrapAntigravityResponse(raw)
		return canonicalGeminiResponse(raw, request.UpstreamModel, reverseToolNames), nil
	}
	return nil, lastErr
}

func (a *antigravityAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	if !antigravityStreamable(request) {
		return a.executeUnaryStream(ctx, provider, credential, request)
	}
	ctx = withCredentialProxy(ctx, credential)
	body, reverseToolNames, err := antigravityPreparedBody(request, credential)
	if err != nil {
		return nil, err
	}
	headers := antigravityHeaders(provider, credential, true)
	var response *http.Response
	var lastErr error
	for _, baseURL := range antigravityBaseURLs(provider) {
		candidate, execErr := executeJSON(ctx, a.client, http.MethodPost, antigravityActionURL(baseURL, true), headers, body)
		if execErr != nil {
			lastErr = &ProviderError{Code: "upstream_network", Err: execErr}
			if antigravityShouldTryNextHost(lastErr) {
				continue
			}
			return nil, lastErr
		}
		if candidate.StatusCode < 200 || candidate.StatusCode >= 300 {
			lastErr = antigravityUpstreamError(candidate)
			candidate.Body.Close()
			if antigravityShouldTryNextHost(lastErr) {
				continue
			}
			return nil, lastErr
		}
		response = candidate
		break
	}
	if response == nil {
		return nil, lastErr
	}
	out := make(chan canonical.Event, 16)
	go func() {
		defer close(out)
		defer response.Body.Close()
		out <- canonical.Event{Type: canonical.EventMessageStart, Model: request.UpstreamModel}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
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
			if data == "" || data == "[DONE]" {
				continue
			}
			var raw map[string]any
			if json.Unmarshal([]byte(data), &raw) != nil {
				continue
			}
			raw = unwrapAntigravityResponse(raw)
			if finish := emitGeminiEvents(out, raw, reverseToolNames); finish != "" {
				finishReason = finish
			}
		}
		if err := scanner.Err(); err != nil {
			out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: http.StatusBadGateway, Code: "upstream_stream_error", Err: err}}
			return
		}
		// The terminal event is held back until the stream is drained. Cloud Code
		// often reports final token counts in a chunk that follows the one
		// carrying finishReason, and the router stops accounting at
		// EventMessageEnd, so ending early recorded those requests as costing
		// zero tokens.
		out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: finishReason}
	}()
	return out, nil
}

// executeUnaryStream serves a streaming caller from a single generateContent
// call. Image generation is the only path that needs it: Cloud Code has no
// streaming image endpoint, so without this a client asking for a streamed
// image would get an upstream error instead of a picture.
func (a *antigravityAdapter) executeUnaryStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	response, err := a.Execute(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	out := make(chan canonical.Event, 8)
	go func() {
		defer close(out)
		out <- canonical.Event{Type: canonical.EventMessageStart, Model: response.Model}
		if response.Reasoning != "" {
			out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: response.Reasoning}
		}
		if text := stringValue(response.Content); text != "" {
			out <- canonical.Event{Type: canonical.EventTextDelta, Text: text}
		}
		for _, call := range response.ToolCalls {
			out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: call}
		}
		usage := response.Usage
		out <- canonical.Event{Type: canonical.EventUsage, Usage: &usage}
		out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: response.FinishReason}
	}()
	return out, nil
}

// unwrapAntigravityResponse lifts the Gemini payload out of the Cloud Code
// envelope and normalises where token accounting is reported.
//
// Cloud Code is inconsistent about both the name and the position of that
// accounting: it may appear as usageMetadata or as cpaUsageMetadata, and either
// inside the "response" object or alongside it on the envelope. Reading only
// the inner object meant an envelope-level total was dropped on the floor and
// the request was recorded as costing zero tokens, which silently understates
// usage, quota consumption and cost.
//
// cpaUsageMetadata wins when both are present: it is the Cloud Code layer's own
// accounting, which is what the account's quota is measured against.
func unwrapAntigravityResponse(raw map[string]any) map[string]any {
	inner, wrapped := raw["response"].(map[string]any)
	if !wrapped {
		inner = raw
	}
	usage := antigravityUsageMetadata(inner)
	if usage == nil && wrapped {
		usage = antigravityUsageMetadata(raw)
	}
	delete(inner, "cpaUsageMetadata")
	if usage != nil {
		inner["usageMetadata"] = usage
	}
	return inner
}

// antigravityUsageMetadata returns the token accounting carried by one level of
// the payload, preferring the Cloud Code counters over the model-level ones.
func antigravityUsageMetadata(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	if usage, ok := values["cpaUsageMetadata"].(map[string]any); ok && len(usage) > 0 {
		return usage
	}
	if usage, ok := values["usageMetadata"].(map[string]any); ok && len(usage) > 0 {
		return usage
	}
	return nil
}

// restoreAntigravityResponseToolNames keeps the raw response internally
// consistent with the canonical response. A request-local wire name is an
// implementation detail, so it must not escape through Response.Raw either.
func restoreAntigravityResponseToolNames(raw map[string]any, reverseToolNames map[string]string) {
	if len(reverseToolNames) == 0 {
		return
	}
	for _, candidateValue := range antigravityAnySlice(raw["candidates"]) {
		candidate, _ := candidateValue.(map[string]any)
		if candidate == nil {
			continue
		}
		content, _ := candidate["content"].(map[string]any)
		for _, part := range antigravityMapSlice(content["parts"]) {
			if call, ok := part["functionCall"].(map[string]any); ok {
				call["name"] = restoreAntigravityToolName(stringValue(call["name"]), reverseToolNames)
			}
		}
	}
}

func canonicalGeminiResponse(raw map[string]any, model string, reverseToolNames map[string]string) *canonical.Response {
	restoreAntigravityResponseToolNames(raw, reverseToolNames)
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
				result.ToolCalls = append(result.ToolCalls, geminiToolCall(call, reverseToolNames))
			}
		}
		result.Content = text.String()
		result.Reasoning = reasoning.String()
	}
	result.Usage = parseGeminiUsage(firstAny(raw["usageMetadata"], raw["cpaUsageMetadata"]))
	return result
}

// emitGeminiEvents forwards one decoded chunk as canonical events and reports
// the finish reason it carried, if any. It deliberately does not emit the
// terminal event: the caller holds that back until the stream is drained so a
// trailing usage chunk is still accounted for.
func emitGeminiEvents(out chan<- canonical.Event, raw map[string]any, reverseToolNames map[string]string) string {
	finishReason := ""
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
				out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: geminiToolCall(call, reverseToolNames)}
			}
		}
		if finish := stringValue(candidate["finishReason"]); finish != "" {
			finishReason = finish
		}
	}
	if usage := geminiUsageEvent(raw); usage != nil {
		out <- canonical.Event{Type: canonical.EventUsage, Usage: usage}
	}
	return finishReason
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

func geminiToolCall(call map[string]any, reverseToolNames map[string]string) map[string]any {
	arguments, err := json.Marshal(call["args"])
	if err != nil {
		arguments = []byte("{}")
	}
	return map[string]any{
		"id":   stringValue(firstValue(call, "id", "call_id")),
		"type": "function",
		"function": map[string]any{
			"name":      restoreAntigravityToolName(stringValue(call["name"]), reverseToolNames),
			"arguments": string(arguments),
		},
	}
}

var _ Adapter = (*antigravityAdapter)(nil)
