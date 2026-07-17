package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/security"
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
		result = append(result, canonical.Message{Role: stringValue(mapped["role"]), Content: mapped["content"], Name: stringValue(mapped["name"]), ToolCallID: stringValue(mapped["tool_call_id"]), ToolCalls: mapSlice(mapped["tool_calls"])})
	}
	return result
}

func renderOpenAI(response *canonical.Response, requestID string) map[string]any {
	message := map[string]any{"role": "assistant", "content": response.Content}
	if response.Reasoning != "" {
		message["reasoning_content"] = response.Reasoning
	}
	if len(response.ToolCalls) > 0 {
		message["tool_calls"] = response.ToolCalls
	}
	return map[string]any{"id": nonEmpty(response.ID, requestID), "object": "chat.completion", "created": time.Now().Unix(), "model": response.Model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": nonEmpty(response.FinishReason, "stop")}}, "usage": map[string]any{"prompt_tokens": response.Usage.InputTokens, "completion_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.InputTokens + response.Usage.OutputTokens}}
}

func renderResponses(response *canonical.Response, requestID string) map[string]any {
	content := []any{map[string]any{"type": "output_text", "text": stringValue(response.Content)}}
	output := []any{map[string]any{"id": "msg_" + requestID, "type": "message", "role": "assistant", "content": content}}
	return map[string]any{"id": nonEmpty(response.ID, "resp_"+requestID), "object": "response", "created_at": time.Now().Unix(), "model": response.Model, "status": "completed", "output": output, "usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.InputTokens + response.Usage.OutputTokens}}
}

func renderClaude(response *canonical.Response, requestID string) map[string]any {
	blocks := []any{}
	if text := stringValue(response.Content); text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	if response.Reasoning != "" {
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": response.Reasoning})
	}
	for _, call := range response.ToolCalls {
		fn, _ := call["function"].(map[string]any)
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": stringValue(call["id"]), "name": stringValue(fn["name"]), "input": parseJSONAny(stringValue(fn["arguments"]))})
	}
	return map[string]any{"id": nonEmpty(response.ID, "msg_"+requestID), "type": "message", "role": "assistant", "model": response.Model, "content": blocks, "stop_reason": nonEmpty(response.FinishReason, "end_turn"), "usage": map[string]any{"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens}}
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

func writeOpenAIStream(w http.ResponseWriter, r *http.Request, events <-chan canonical.Event, requestID, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	started := false
	for event := range events {
		if !started {
			started = true
		}
		payload := map[string]any{"id": nonEmpty(event.ID, "chatcmpl_"+requestID), "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": nonEmpty(event.Model, model), "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": nil}}}
		choice := payload["choices"].([]any)[0].(map[string]any)
		delta := choice["delta"].(map[string]any)
		switch event.Type {
		case canonical.EventMessageStart:
			delta["role"] = "assistant"
		case canonical.EventTextDelta:
			delta["content"] = event.Text
		case canonical.EventReasoningDelta:
			delta["reasoning_content"] = event.Reasoning
		case canonical.EventToolCallDelta:
			delta["tool_calls"] = []any{event.ToolCall}
		case canonical.EventImageDelta:
			delta["image"] = event.Media
		case canonical.EventAudioDelta:
			delta["audio"] = event.Media
		case canonical.EventUsage:
			if event.Usage != nil {
				payload["usage"] = map[string]any{"prompt_tokens": event.Usage.InputTokens, "completion_tokens": event.Usage.OutputTokens, "total_tokens": event.Usage.InputTokens + event.Usage.OutputTokens}
			}
		case canonical.EventMessageEnd:
			choice["finish_reason"] = nonEmpty(event.FinishReason, "stop")
		}
		if event.Type == canonical.EventError {
			writeError(w, 502, "stream_error", event.Err.Error(), requestID)
			return
		}
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
		if event.Type == canonical.EventMessageEnd {
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
	}
}

func writeResponsesStream(w http.ResponseWriter, r *http.Request, events <-chan canonical.Event, requestID, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	var usage *canonical.Usage
	send := func(eventType string, payload any) {
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	for event := range events {
		switch event.Type {
		case canonical.EventMessageStart:
			send("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_" + requestID, "object": "response", "model": nonEmpty(event.Model, model), "status": "in_progress"}})
		case canonical.EventTextDelta:
			send("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "response_id": "resp_" + requestID, "delta": event.Text})
		case canonical.EventReasoningDelta:
			send("response.reasoning_summary_text.delta", map[string]any{"type": "response.reasoning_summary_text.delta", "response_id": "resp_" + requestID, "delta": event.Reasoning})
		case canonical.EventImageDelta:
			send("response.output_image.delta", map[string]any{"type": "response.output_image.delta", "response_id": "resp_" + requestID, "delta": event.Media})
		case canonical.EventAudioDelta:
			send("response.output_audio.delta", map[string]any{"type": "response.output_audio.delta", "response_id": "resp_" + requestID, "delta": event.Media})
		case canonical.EventUsage:
			if event.Usage != nil {
				copyUsage := *event.Usage
				usage = &copyUsage
			}
		case canonical.EventMessageEnd:
			response := map[string]any{"id": "resp_" + requestID, "model": model, "status": "completed"}
			if usage != nil {
				response["usage"] = map[string]any{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "total_tokens": usage.InputTokens + usage.OutputTokens}
			}
			send("response.completed", map[string]any{"type": "response.completed", "response": response})
			return
		case canonical.EventError:
			send("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": event.Err.Error()}})
			return
		}
	}
}

func writeClaudeStream(w http.ResponseWriter, r *http.Request, events <-chan canonical.Event, requestID, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	send := func(eventType string, payload any) {
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	for event := range events {
		switch event.Type {
		case canonical.EventMessageStart:
			send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_" + requestID, "type": "message", "role": "assistant", "model": nonEmpty(event.Model, model), "content": []any{}, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}})
		case canonical.EventTextDelta:
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": event.Text}})
		case canonical.EventReasoningDelta:
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": event.Reasoning}})
		case canonical.EventToolCallDelta:
			function, _ := event.ToolCall["function"].(map[string]any)
			if name := stringValue(function["name"]); name != "" {
				send("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": stringValue(event.ToolCall["id"]), "name": name, "input": map[string]any{}}})
			}
			if arguments := stringValue(function["arguments"]); arguments != "" {
				send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": arguments}})
			}
		case canonical.EventUsage:
			if event.Usage != nil {
				send("message_delta", map[string]any{"type": "message_delta", "usage": map[string]any{"output_tokens": event.Usage.OutputTokens}})
			}
		case canonical.EventMessageEnd:
			send("message_stop", map[string]any{"type": "message_stop"})
			return
		case canonical.EventError:
			send("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": event.Err.Error()}})
			return
		}
	}
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
