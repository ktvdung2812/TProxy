package claudeopenai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
)

const claudeOAuthToolPrefix = "proxy_"

type claudeStreamState struct {
	messageID            string
	model                string
	messageStartSent     bool
	nextBlockIndex       int
	textBlockIndex       int
	textBlockStarted     bool
	textBlockClosed      bool
	thinkingBlockIndex   int
	thinkingBlockStarted bool
	toolCalls            map[int]toolCallState
	toolArgBuffers       map[int]string
	finishReason         string
	usage                *canonical.Usage
}

type toolCallState struct {
	id         string
	name       string
	blockIndex int
}

// WriteClaudeStream renders canonical events as Anthropic SSE.
// Ported from 9router open-sse/translator/response/openai-to-claude.js.
func WriteClaudeStream(w http.ResponseWriter, events <-chan canonical.Event, requestID, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	state := &claudeStreamState{toolCalls: map[int]toolCallState{}, toolArgBuffers: map[int]string{}}
	send := func(eventType string, payload any) {
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	stopThinking := func() {
		if !state.thinkingBlockStarted {
			return
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": state.thinkingBlockIndex})
		state.thinkingBlockStarted = false
	}
	stopText := func() {
		if !state.textBlockStarted || state.textBlockClosed {
			return
		}
		state.textBlockClosed = true
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": state.textBlockIndex})
		state.textBlockStarted = false
	}
	ensureMessageStart := func(event canonical.Event) {
		if state.messageStartSent {
			return
		}
		state.messageStartSent = true
		state.messageID = strings.TrimPrefix(nonEmpty(event.ID, "msg_"+requestID), "chatcmpl-")
		state.model = nonEmpty(model, event.Model)
		send("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            state.messageID,
				"type":          "message",
				"role":          "assistant",
				"model":         state.model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		})
	}

	for event := range events {
		switch event.Type {
		case canonical.EventMessageStart:
			ensureMessageStart(event)
		case canonical.EventTextDelta:
			ensureMessageStart(event)
			stopThinking()
			if !state.textBlockStarted {
				state.textBlockIndex = state.nextBlockIndex
				state.nextBlockIndex++
				state.textBlockStarted = true
				state.textBlockClosed = false
				send("content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": state.textBlockIndex,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				})
			}
			send("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.textBlockIndex,
				"delta": map[string]any{"type": "text_delta", "text": event.Text},
			})
		case canonical.EventReasoningDelta:
			ensureMessageStart(event)
			stopText()
			if !state.thinkingBlockStarted {
				state.thinkingBlockIndex = state.nextBlockIndex
				state.nextBlockIndex++
				state.thinkingBlockStarted = true
				send("content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": state.thinkingBlockIndex,
					"content_block": map[string]any{
						"type":     "thinking",
						"thinking": "",
					},
				})
			}
			send("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.thinkingBlockIndex,
				"delta": map[string]any{"type": "thinking_delta", "thinking": event.Reasoning},
			})
		case canonical.EventToolCallDelta:
			ensureMessageStart(event)
			stopThinking()
			stopText()
			idx := toolCallIndex(event.ToolCall)
			toolInfo, exists := state.toolCalls[idx]
			if !exists {
				fn := mapValue(event.ToolCall["function"])
				name := stripClaudeOAuthToolPrefix(stringValue(fn["name"]))
				id := stringValue(event.ToolCall["id"])
				if id == "" {
					id = fmt.Sprintf("toolu_%d", idx)
				}
				toolInfo = toolCallState{id: id, name: name, blockIndex: state.nextBlockIndex}
				state.nextBlockIndex++
				state.toolCalls[idx] = toolInfo
				send("content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": toolInfo.blockIndex,
					"content_block": map[string]any{
						"type":  "tool_use",
						"id":    toolInfo.id,
						"name":  toolInfo.name,
						"input": map[string]any{},
					},
				})
			}
			fn := mapValue(event.ToolCall["function"])
			if args := stringValue(fn["arguments"]); args != "" {
				state.toolArgBuffers[idx] += args
			}
		case canonical.EventUsage:
			if event.Usage != nil {
				copyUsage := *event.Usage
				state.usage = &copyUsage
			}
		case canonical.EventMessageEnd:
			ensureMessageStart(event)
			stopThinking()
			stopText()
			for idx, toolInfo := range state.toolCalls {
				if buffered := state.toolArgBuffers[idx]; buffered != "" {
					sanitized := sanitizeClaudeToolArgs(toolInfo.name, buffered)
					send("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": toolInfo.blockIndex,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": sanitized},
					})
				}
				send("content_block_stop", map[string]any{"type": "content_block_stop", "index": toolInfo.blockIndex})
			}
			finish := openAIFinishToClaude(nonEmpty(event.FinishReason, "stop"), len(state.toolCalls) > 0)
			usage := map[string]any{"output_tokens": 0}
			if state.usage != nil {
				usage["input_tokens"] = state.usage.InputTokens
				usage["output_tokens"] = state.usage.OutputTokens
			}
			send("message_delta", map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": finish},
				"usage": usage,
			})
			send("message_stop", map[string]any{"type": "message_stop"})
			return
		case canonical.EventError:
			send("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": event.Err.Error()}})
			return
		}
	}
}

func toolCallIndex(call map[string]any) int {
	if call == nil {
		return 0
	}
	if index, ok := call["index"].(float64); ok {
		return int(index)
	}
	if index, ok := call["index"].(int); ok {
		return index
	}
	return 0
}

func stripClaudeOAuthToolPrefix(name string) string {
	if strings.HasPrefix(name, claudeOAuthToolPrefix) {
		return strings.TrimPrefix(name, claudeOAuthToolPrefix)
	}
	return name
}

func sanitizeClaudeToolArgs(toolName, argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return argsJSON
	}
	switch stripClaudeOAuthToolPrefix(toolName) {
	case "Read":
		sanitizeClaudeReadArgs(args)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return argsJSON
	}
	return string(encoded)
}

func sanitizeClaudeReadArgs(args map[string]any) {
	if limit, ok := args["limit"].(string); ok && isDigits(limit) {
		args["limit"] = atoiDefault(limit, 0)
	}
	if offset, ok := args["offset"].(string); ok && isSignedDigits(offset) {
		args["offset"] = atoiDefault(offset, 0)
	}
	if limit, ok := toIntArg(args["limit"]); ok {
		if limit > 2000 {
			args["limit"] = 2000
		}
		if limit < 1 {
			delete(args, "limit")
		}
	}
	if offset, ok := toIntArg(args["offset"]); ok && offset < 0 {
		args["offset"] = 0
	}
	if _, hasPages := args["pages"]; hasPages {
		filePath, _ := args["file_path"].(string)
		pages, _ := args["pages"].(string)
		if !isValidClaudePDFPagesArg(filePath, pages) {
			delete(args, "pages")
		}
	}
}

func toIntArg(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}

func isValidClaudePDFPagesArg(filePath, pages string) bool {
	return strings.HasSuffix(strings.ToLower(filePath), ".pdf") &&
		pages != "" &&
		isPDFPagesRange(pages)
}

func isPDFPagesRange(pages string) bool {
	for i := 0; i < len(pages); i++ {
		ch := pages[i]
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '-' && i > 0 && i < len(pages)-1 {
			continue
		}
		return false
	}
	return true
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func isSignedDigits(value string) bool {
	if value == "" {
		return false
	}
	start := 0
	if value[0] == '-' {
		start = 1
	}
	if start >= len(value) {
		return false
	}
	for _, ch := range value[start:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func atoiDefault(value string, fallback int) int {
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return fallback
	}
	return parsed
}

// CollectClaudeStreamEvents helps tests capture Anthropic SSE payloads.
func CollectClaudeStreamEvents(events <-chan canonical.Event, requestID, model string) []map[string]any {
	recorder := httptest.NewRecorder()
	WriteClaudeStream(recorder, events, requestID, model)
	return parseClaudeSSE(recorder.Body.String())
}

func parseClaudeSSE(body string) []map[string]any {
	var payloads []map[string]any
	for _, block := range strings.Split(body, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var dataLine string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data:") {
				dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if dataLine == "" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(dataLine), &payload) == nil {
			payloads = append(payloads, payload)
		}
	}
	return payloads
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

// RenderClaudeResponse converts a canonical response to Anthropic /v1/messages JSON.
func RenderClaudeResponse(response *canonical.Response, requestID string) map[string]any {
	blocks := []any{}
	if text := stringValue(response.Content); text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	if response.Reasoning != "" {
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": response.Reasoning})
	}
	for _, call := range response.ToolCalls {
		fn := mapValue(call["function"])
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    stringValue(call["id"]),
			"name":  stripClaudeOAuthToolPrefix(stringValue(fn["name"])),
			"input": safeParseJSON(stringValue(fn["arguments"])),
		})
	}
	return map[string]any{
		"id":          nonEmpty(response.ID, "msg_"+requestID),
		"type":        "message",
		"role":        "assistant",
		"model":       response.Model,
		"content":     blocks,
		"stop_reason": openAIFinishToClaude(nonEmpty(response.FinishReason, "stop"), len(response.ToolCalls) > 0),
		"usage": map[string]any{
			"input_tokens":  response.Usage.InputTokens,
			"output_tokens": response.Usage.OutputTokens,
		},
	}
}

// RenderOpenAIResponse converts a canonical response to OpenAI chat completions JSON.
func RenderOpenAIResponse(response *canonical.Response, requestID string) map[string]any {
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
	return map[string]any{
		"id":      nonEmpty(response.ID, requestID),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   response.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": claudeStopToOpenAI(nonEmpty(response.FinishReason, "stop")),
		}},
		"usage": map[string]any{
			"prompt_tokens":     response.Usage.InputTokens,
			"completion_tokens": response.Usage.OutputTokens,
			"total_tokens":      response.Usage.InputTokens + response.Usage.OutputTokens,
		},
	}
}
