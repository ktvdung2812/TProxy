package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/translator/claudeopenai"
)

func decodeBody(r *http.Request) (map[string]any, error) {
	defer r.Body.Close()
	var body map[string]any
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	return body, nil
}

func parseOpenAIChat(body map[string]any, requestID string) canonical.Request {
	request := canonical.Request{RequestID: requestID, Source: canonical.ProtocolOpenAI, PublicModelID: stringValue(body["model"]), Raw: body, Stream: boolValue(body["stream"])}
	request.Messages = parseMessages(body["messages"])
	request.Tools = mapSlice(body["tools"])
	request.ToolChoice = body["tool_choice"]
	request.MaxTokens = intValue(firstValue(body, "max_tokens", "max_completion_tokens"))
	request.Temperature = floatPointer(body["temperature"])
	request.Reasoning = mapValue(body["reasoning"])
	return request
}

func parseResponses(body map[string]any, requestID string) canonical.Request {
	request := canonical.Request{RequestID: requestID, Source: canonical.ProtocolResponses, PublicModelID: stringValue(body["model"]), Raw: body, Stream: boolValue(body["stream"]), MaxTokens: intValue(body["max_output_tokens"]), Temperature: floatPointer(body["temperature"])}
	if instructions := body["instructions"]; instructions != nil {
		request.System = instructions
	}
	input := body["input"]
	switch value := input.(type) {
	case string:
		request.Messages = []canonical.Message{{Role: "user", Content: value}}
	case []any:
		request.Messages = parseMessages(value)
	default:
		request.Messages = parseMessages(body["messages"])
	}
	request.Tools = mapSlice(body["tools"])
	request.ToolChoice = body["tool_choice"]
	return request
}

func parseClaude(body map[string]any, requestID string) canonical.Request {
	request := canonical.Request{RequestID: requestID, Source: canonical.ProtocolClaude, PublicModelID: stringValue(body["model"]), Raw: body, Stream: boolValue(body["stream"]), MaxTokens: intValue(body["max_tokens"]), System: body["system"], Tools: mapSlice(body["tools"]), ToolChoice: body["tool_choice"], Temperature: floatPointer(body["temperature"])}
	request.Messages = parseMessages(body["messages"])
	return request
}

func parseGemini(body map[string]any, requestID, model string) canonical.Request {
	request := canonical.Request{RequestID: requestID, Source: canonical.ProtocolGemini, PublicModelID: model, UpstreamModel: model, Raw: body, Stream: false}
	if config, ok := body["generationConfig"].(map[string]any); ok {
		request.MaxTokens = intValue(config["maxOutputTokens"])
		request.Temperature = floatPointer(config["temperature"])
	}
	if instruction, ok := body["systemInstruction"].(map[string]any); ok {
		request.System = geminiPartsText(instruction["parts"])
	}
	contents, _ := body["contents"].([]any)
	for _, item := range contents {
		mapped, _ := item.(map[string]any)
		role := stringValue(mapped["role"])
		if role == "model" {
			role = "assistant"
		}
		request.Messages = append(request.Messages, canonical.Message{Role: role, Content: geminiPartsText(mapped["parts"])})
	}
	return request
}

func parseMessages(value any) []canonical.Message {
	items, _ := value.([]any)
	result := make([]canonical.Message, 0, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itemType := stringValue(mapped["type"])
		role := stringValue(mapped["role"])
		// Normalize developer role to system for providers that don't support it.
		// The Codex adapter will re-convert system→developer when needed.
		if role == "developer" {
			role = "system"
		}
		// Codex /responses function_call items: {type: "function_call", call_id, name, arguments}
		// Map to OpenAI assistant message with tool_calls.
		if role == "" && itemType == "function_call" && stringValue(mapped["call_id"]) != "" {
			toolCall := map[string]any{
				"id":   mapped["call_id"],
				"type": "function",
				"function": map[string]any{
					"name":      mapped["name"],
					"arguments": stringValue(mapped["arguments"]),
				},
			}
			// /responses emits one item per parallel call, chat completions expects a
			// single assistant message carrying all of them. Splitting them leaves an
			// assistant tool_calls message followed by another assistant message, which
			// upstreams reject ("insufficient tool messages following tool_calls").
			if last := len(result) - 1; last >= 0 && result[last].Role == "assistant" {
				result[last].ToolCalls = append(result[last].ToolCalls, toolCall)
				continue
			}
			result = append(result, canonical.Message{
				Role:      "assistant",
				Content:   nil,
				ToolCalls: []map[string]any{toolCall},
			})
			continue
		}
		// Codex /responses function_call_output items: {type: "function_call_output", call_id, output}
		// Map to OpenAI tool message.
		if role == "" && itemType == "function_call_output" && stringValue(mapped["call_id"]) != "" {
			result = append(result, canonical.Message{
				Role:       "tool",
				Content:    stringValue(mapped["output"]),
				ToolCallID: stringValue(mapped["call_id"]),
			})
			continue
		}
		// Skip Codex-specific items that have no OpenAI equivalent.
		if role == "" && itemType != "" && itemType != "message" {
			continue
		}
		result = append(result, canonical.Message{Role: role, Content: mapped["content"], Name: stringValue(mapped["name"]), ToolCallID: stringValue(mapped["tool_call_id"]), ToolCalls: mapSlice(mapped["tool_calls"])})
	}
	return result
}

func renderOpenAI(response *canonical.Response, requestID, clientModel string) map[string]any {
	content := response.Content
	if content == nil {
		content = ""
	}
	message := map[string]any{"role": "assistant", "content": content}
	if response.Reasoning != "" {
		message["reasoning_content"] = response.Reasoning
	}
	if len(response.ToolCalls) > 0 {
		message["tool_calls"] = response.ToolCalls
	}
	model := response.Model
	if clientModel != "" {
		model = clientModel
	}
	return map[string]any{"id": nonEmpty(response.ID, requestID), "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": nonEmpty(response.FinishReason, "stop")}}, "usage": map[string]any{"prompt_tokens": response.Usage.InputTokens, "completion_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.InputTokens + response.Usage.OutputTokens}}
}

func renderResponses(response *canonical.Response, requestID, clientModel string) map[string]any {
	content := []any{map[string]any{"type": "output_text", "text": stringValue(response.Content)}}
	output := []any{map[string]any{"id": "msg_" + requestID, "type": "message", "role": "assistant", "content": content}}
	model := response.Model
	if clientModel != "" {
		model = clientModel
	}
	return map[string]any{"id": nonEmpty(response.ID, "resp_"+requestID), "object": "response", "created_at": time.Now().Unix(), "model": model, "status": "completed", "output": output, "usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.InputTokens + response.Usage.OutputTokens}}
}

func renderClaude(response *canonical.Response, requestID, clientModel string) map[string]any {
	if clientModel != "" && response != nil && response.Model != clientModel {
		cloned := *response
		cloned.Model = clientModel
		response = &cloned
	}
	return claudeopenai.RenderClaudeResponse(response, requestID)
}

func renderGemini(response *canonical.Response) map[string]any {
	parts := []any{}
	if text := stringValue(response.Content); text != "" {
		parts = append(parts, map[string]any{"text": text})
	}
	return map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "finishReason": strings.ToUpper(nonEmpty(response.FinishReason, "STOP"))}}, "usageMetadata": map[string]any{"promptTokenCount": response.Usage.InputTokens, "candidatesTokenCount": response.Usage.OutputTokens, "totalTokenCount": response.Usage.InputTokens + response.Usage.OutputTokens}}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("X-TProxy-Error-Code", code)
	writeJSON(w, status, map[string]any{"error": map[string]any{"type": "provider_error", "code": code, "message": security.RedactText(message), "request_id": requestID}})
}

// writeProviderError renders a routing or upstream failure and forwards the
// Retry-After that goes with it, so a client backs off instead of treating a
// rate-limited account as a dead connection and reconnecting in a loop.
func writeProviderError(w http.ResponseWriter, err error, requestID string) {
	if retryAfter := strings.TrimSpace(providers.RetryAfter(err)); retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	writeError(w, providerErrorHTTPStatus(err), providers.Code(err), err.Error(), requestID)
}

func providerErrorHTTPStatus(err error) int {
	status := providers.Status(err)
	if status == 0 {
		return http.StatusBadGateway
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return http.StatusBadGateway
	default:
		return status
	}
}

func peekRequestBody(r *http.Request, limit int64) []byte {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return body
}

func requestWantsOpenAIStream(r *http.Request) bool {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		return false
	}
	body := peekRequestBody(r, 1<<20)
	return bytes.Contains(body, []byte(`"stream":true`)) || bytes.Contains(body, []byte(`"stream": true`))
}

func writeStreamAwareError(w http.ResponseWriter, r *http.Request, status int, code, message, requestID string) {
	if requestWantsOpenAIStream(r) {
		writeOpenAIStreamError(w, status, code, message, requestID)
		return
	}
	writeError(w, status, code, message, requestID)
}

func writeOpenAIStreamError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-TProxy-Error-Code", code)
	w.WriteHeader(status)
	payload, _ := json.Marshal(map[string]any{"error": map[string]any{"type": "provider_error", "code": code, "message": security.RedactText(message), "request_id": requestID}})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func writeOpenAIStream(w http.ResponseWriter, r *http.Request, events <-chan canonical.Event, requestID, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	streamID := "chatcmpl_" + requestID
	created := time.Now().Unix()
	hasRole := false
	var pendingUsage *canonical.Usage

	writeChunk := func(delta map[string]any, finishReason any, usage *canonical.Usage) {
		if !hasRole {
			delta["role"] = "assistant"
			hasRole = true
		}
		payload := map[string]any{
			"id":      streamID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finishReason}},
		}
		if usage != nil {
			payload["usage"] = map[string]any{
				"prompt_tokens":     usage.InputTokens,
				"completion_tokens": usage.OutputTokens,
				"total_tokens":      usage.InputTokens + usage.OutputTokens,
			}
		}
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	for event := range events {
		if event.ID != "" {
			streamID = event.ID
		}
		if event.Model != "" && model == "" {
			model = event.Model
		}
		switch event.Type {
		case canonical.EventMessageStart:
			continue
		case canonical.EventTextDelta:
			writeChunk(map[string]any{"content": event.Text}, nil, nil)
		case canonical.EventReasoningDelta:
			writeChunk(map[string]any{"reasoning_content": event.Reasoning}, nil, nil)
		case canonical.EventToolCallDelta:
			writeChunk(map[string]any{"tool_calls": []any{event.ToolCall}}, nil, nil)
		case canonical.EventImageDelta:
			writeChunk(map[string]any{"image": event.Media}, nil, nil)
		case canonical.EventAudioDelta:
			writeChunk(map[string]any{"audio": event.Media}, nil, nil)
		case canonical.EventUsage:
			if event.Usage != nil {
				copyUsage := *event.Usage
				pendingUsage = &copyUsage
			}
		case canonical.EventMessageEnd:
			writeChunk(map[string]any{}, nonEmpty(event.FinishReason, "stop"), pendingUsage)
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		case canonical.EventError:
			writeOpenAIStreamError(w, 502, "stream_error", event.Err.Error(), requestID)
			return
		}
	}
}

func writeClaudeStream(w http.ResponseWriter, r *http.Request, events <-chan canonical.Event, requestID, model string) {
	claudeopenai.WriteClaudeStream(w, events, requestID, model)
}

func writeGeminiStream(w http.ResponseWriter, r *http.Request, events <-chan canonical.Event) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for event := range events {
		if event.Type == canonical.EventTextDelta {
			data, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": event.Text}}}}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		if event.Type == canonical.EventMessageEnd {
			data, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"finishReason": strings.ToUpper(nonEmpty(event.FinishReason, "STOP"))}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func parseJSONAny(value string) any {
	var result any
	if json.Unmarshal([]byte(value), &result) == nil {
		return result
	}
	return value
}
func geminiPartsText(value any) string {
	parts, _ := value.([]any)
	var builder strings.Builder
	for _, item := range parts {
		if mapped, ok := item.(map[string]any); ok {
			builder.WriteString(stringValue(mapped["text"]))
		}
	}
	return builder.String()
}
func mapValue(value any) map[string]any { result, _ := value.(map[string]any); return result }
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
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
func boolValue(value any) bool { valueBool, ok := value.(bool); return ok && valueBool }
func intValue(value any) int {
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
func floatPointer(value any) *float64 {
	switch v := value.(type) {
	case float64:
		return &v
	case int:
		f := float64(v)
		return &f
	default:
		return nil
	}
}
func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, exists := values[key]; exists {
			return value
		}
	}
	return nil
}
func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
func tokenEstimate(request canonical.Request) int {
	size := 0
	for _, message := range request.Messages {
		size += len(stringValue(message.Content)) + len(message.Role)
	}
	return intValue(float64((size + 3) / 4))
}
func useClientRequestID(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-Request-ID")); id != "" {
		return id
	}
	return security.NewID("req_")
}

func sessionIDFromRequest(r *http.Request) string {
	for _, header := range []string{"X-Session-ID", "Session_id", "X-Client-Request-Id", "Conversation-ID"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return ""
}
func statusText(status int) string { return strconv.Itoa(status) }
