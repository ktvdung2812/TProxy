package providers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

const codexToolNameMaxLen = 64

// CodeUpstreamModelAtCapacity is the router-facing code for ChatGPT's
// "Selected model is at capacity" failure. Upstream reports it as an HTTP 400
// or a mid-stream response.failed — both read as permanent to the router — so
// wherever it surfaces it is reclassified as a 429 carrying this code, which
// triggers failover and a model-level cooldown instead.
const CodeUpstreamModelAtCapacity = "upstream_model_at_capacity"

// isModelAtCapacity recognises the model-capacity failure by its upstream
// error code or message text.
func isModelAtCapacity(code, message string) bool {
	code = strings.ToLower(code)
	if strings.Contains(code, "at_capacity") || strings.Contains(code, "at capacity") {
		return true
	}
	return strings.Contains(strings.ToLower(message), "at capacity")
}

// codexFailedError extracts the error code and message from a response.failed
// (or error) event, which ChatGPT nests either at the top level or under the
// response object.
func codexFailedError(raw map[string]any) (code, message string) {
	errorPayload, _ := raw["error"].(map[string]any)
	if errorPayload == nil {
		if response, _ := raw["response"].(map[string]any); response != nil {
			errorPayload, _ = response["error"].(map[string]any)
		}
	}
	if errorPayload != nil {
		code = stringValue(errorPayload["code"])
		message = stringValue(errorPayload["message"])
	}
	if code == "" {
		code = stringValue(raw["code"])
	}
	if message == "" {
		message = stringValue(raw["message"])
	}
	return code, message
}

// ModelAtCapacitySSE inspects a Responses API response.failed SSE payload and
// reports whether it carries the model-capacity error, returning its message.
// The passthrough stream forwards events verbatim, so this is the router's
// only chance to recognise the condition on that path.
func ModelAtCapacitySSE(data []byte) (message string, ok bool) {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return "", false
	}
	code, message := codexFailedError(raw)
	if !isModelAtCapacity(code, message) {
		return "", false
	}
	return message, true
}

var codexToolNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type codexStreamState struct {
	responseID                string
	createdAt                 int64
	model                     string
	functionCallIndex         int
	hasReceivedArgumentsDelta bool
	hasToolCallAnnounced      bool
	hasEmittedContent         bool
	hasCompletedEvent         bool
	hasIncompleteEvent        bool
	hasFailedEvent            bool
	eventCount                int
	reverseToolNameMap        map[string]string
}

func newCodexStreamState(reverseToolNameMap map[string]string) *codexStreamState {
	if reverseToolNameMap == nil {
		reverseToolNameMap = map[string]string{}
	}
	return &codexStreamState{functionCallIndex: -1, reverseToolNameMap: reverseToolNameMap}
}

func translateCodexEvent(raw map[string]any, state *codexStreamState) []canonical.Event {
	typeName := stringValue(raw["type"])
	if typeName != "" {
		state.eventCount++
		switch typeName {
		case "response.completed":
			state.hasCompletedEvent = true
		case "response.incomplete":
			state.hasIncompleteEvent = true
		case "response.failed":
			state.hasFailedEvent = true
		}
	}

	switch typeName {
	case "response.created", "response.in_progress":
		responseDetails, _ := raw["response"].(map[string]any)
		if responseDetails == nil {
			responseDetails = raw
		}
		if id := stringValue(responseDetails["id"]); id != "" {
			state.responseID = id
		}
		if model := stringValue(responseDetails["model"]); model != "" {
			state.model = model
		}
		if created := numberValue(responseDetails["created_at"]); created > 0 {
			state.createdAt = int64(created)
		}
		return nil

	case "response.output_text.delta", "output_text.delta":
		text := stringValue(firstValue(raw, "delta", "text"))
		if text == "" {
			return nil
		}
		state.hasEmittedContent = true
		return []canonical.Event{{
			Type:  canonical.EventTextDelta,
			ID:    state.responseID,
			Model: state.model,
			Text:  text,
		}}

	case "response.reasoning_summary_text.delta", "reasoning_summary_text.delta":
		reasoning := stringValue(firstValue(raw, "delta", "text"))
		if reasoning == "" {
			return nil
		}
		return []canonical.Event{{
			Type:      canonical.EventReasoningDelta,
			ID:        state.responseID,
			Model:     state.model,
			Reasoning: reasoning,
		}}

	case "response.reasoning_summary_text.done", "reasoning_summary_text.done":
		return []canonical.Event{{
			Type:      canonical.EventReasoningDelta,
			ID:        state.responseID,
			Model:     state.model,
			Reasoning: "\n\n",
		}}

	case "response.output_item.added":
		item, _ := raw["item"].(map[string]any)
		if item == nil {
			return nil
		}
		switch stringValue(item["type"]) {
		case "reasoning":
			return codexReasoningEvents(state, item)
		case "function_call":
			state.functionCallIndex++
			state.hasReceivedArgumentsDelta = false
			state.hasToolCallAnnounced = true
			toolName := restoreCodexToolName(stringValue(item["name"]), state.reverseToolNameMap)
			return []canonical.Event{{
				Type:  canonical.EventToolCallDelta,
				ID:    state.responseID,
				Model: state.model,
				ToolCall: map[string]any{
					"index": state.functionCallIndex,
					"id":    stringValue(item["call_id"]),
					"type":  "function",
					"function": map[string]any{
						"name":      toolName,
						"arguments": "",
					},
				},
			}}
		}
		return nil

	case "response.output_item.done":
		item, _ := raw["item"].(map[string]any)
		if item == nil {
			return nil
		}
		switch stringValue(item["type"]) {
		case "reasoning":
			return codexReasoningEvents(state, item)
		case "message":
			if state.hasEmittedContent {
				return nil
			}
			if text := textFromCodexMessageItem(item); text != "" {
				state.hasEmittedContent = true
				return []canonical.Event{{
					Type:  canonical.EventTextDelta,
					ID:    state.responseID,
					Model: state.model,
					Text:  text,
				}}
			}
		case "function_call":
			if state.hasToolCallAnnounced {
				return nil
			}
			state.functionCallIndex++
			toolName := restoreCodexToolName(stringValue(item["name"]), state.reverseToolNameMap)
			return []canonical.Event{{
				Type:  canonical.EventToolCallDelta,
				ID:    state.responseID,
				Model: state.model,
				ToolCall: map[string]any{
					"index": state.functionCallIndex,
					"id":    stringValue(item["call_id"]),
					"type":  "function",
					"function": map[string]any{
						"name":      toolName,
						"arguments": stringValue(item["arguments"]),
					},
				},
			}}
		}
		return nil

	case "response.function_call_arguments.delta", "function_call_arguments.delta":
		arguments := stringValue(firstValue(raw, "delta", "arguments"))
		if arguments == "" {
			return nil
		}
		state.hasReceivedArgumentsDelta = true
		return []canonical.Event{{
			Type:  canonical.EventToolCallDelta,
			ID:    state.responseID,
			Model: state.model,
			ToolCall: map[string]any{
				"index": state.functionCallIndex,
				"type":  "function",
				"function": map[string]any{
					"arguments": arguments,
				},
			},
		}}

	case "response.function_call_arguments.done", "function_call_arguments.done":
		if state.hasReceivedArgumentsDelta {
			return nil
		}
		arguments := stringValue(raw["arguments"])
		if arguments == "" {
			return nil
		}
		return []canonical.Event{{
			Type:  canonical.EventToolCallDelta,
			ID:    state.responseID,
			Model: state.model,
			ToolCall: map[string]any{
				"index": state.functionCallIndex,
				"type":  "function",
				"function": map[string]any{
					"arguments": arguments,
				},
			},
		}}

	case "response.completed", "response.done":
		usage := parseResponsesUsage(firstAny(raw["usage"], nestedMapValue(raw, "response", "usage")))
		events := make([]canonical.Event, 0, 3)
		if !state.hasEmittedContent && state.functionCallIndex < 0 {
			events = append(events, canonical.Event{
				Type:  canonical.EventTextDelta,
				ID:    state.responseID,
				Model: state.model,
				Text:  "",
			})
		}
		finishReason := "stop"
		if state.functionCallIndex >= 0 {
			finishReason = "tool_calls"
		}
		if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.ReasoningTokens > 0 {
			copyUsage := usage
			events = append(events, canonical.Event{Type: canonical.EventUsage, Usage: &copyUsage})
		}
		events = append(events, canonical.Event{
			Type:         canonical.EventMessageEnd,
			ID:           state.responseID,
			Model:        state.model,
			FinishReason: finishReason,
		})
		return events

	case "response.incomplete":
		usage := parseResponsesUsage(firstAny(raw["usage"], nestedMapValue(raw, "response", "usage")))
		events := []canonical.Event{{
			Type:         canonical.EventMessageEnd,
			ID:           state.responseID,
			Model:        state.model,
			FinishReason: "length",
		}}
		if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.ReasoningTokens > 0 {
			copyUsage := usage
			events = append([]canonical.Event{{Type: canonical.EventUsage, Usage: &copyUsage}}, events...)
		}
		return events

	case "response.failed", "error":
		code, message := codexFailedError(raw)
		if message == "" {
			message = "Codex upstream response failed"
		}
		if isModelAtCapacity(code, message) {
			return []canonical.Event{{Type: canonical.EventError, Err: &ProviderError{Status: http.StatusTooManyRequests, Code: CodeUpstreamModelAtCapacity, Message: message}}}
		}
		return []canonical.Event{{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "upstream_response_failed", Message: message}}}
	}

	return nil
}

func textFromCodexMessageItem(item map[string]any) string {
	if stringValue(item["type"]) != "message" {
		return ""
	}
	content, ok := item["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, part := range content {
		block, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if text := stringValue(block["text"]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func codexReasoningEvents(state *codexStreamState, item map[string]any) []canonical.Event {
	if stringValue(item["type"]) != "reasoning" {
		return nil
	}
	reasoning := codexReasoningSummaryText(item)
	encrypted := strings.TrimSpace(stringValue(item["encrypted_content"]))
	itemID := strings.TrimSpace(stringValue(item["id"]))
	if reasoning == "" && encrypted == "" && itemID == "" {
		return nil
	}
	return []canonical.Event{{
		Type:               canonical.EventReasoningDelta,
		ID:                 state.responseID,
		Model:              state.model,
		Reasoning:          reasoning,
		ReasoningEncrypted: encrypted,
		ReasoningItemID:    itemID,
	}}
}

func codexReasoningSummaryText(item map[string]any) string {
	if summary, ok := item["summary"].([]any); ok {
		var parts []string
		for _, entry := range summary {
			block, _ := entry.(map[string]any)
			if text := strings.TrimSpace(stringValue(block["text"])); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func restoreCodexToolName(name string, reverse map[string]string) string {
	if restored, ok := reverse[name]; ok && restored != "" {
		return restored
	}
	return name
}

func buildCodexToolNameMaps(tools []map[string]any, input any) (map[string]string, map[string]string) {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		name := stringValue(function["name"])
		if name == "" {
			name = stringValue(tool["name"])
		}
		if name != "" {
			names = append(names, name)
		}
	}
	names = append(names, collectCodexFunctionCallNames(input)...)
	return buildCodexToolNameMapsFromNames(names)
}

func buildCodexToolNameMapsFromNames(names []string) (map[string]string, map[string]string) {
	shortMap := map[string]string{}
	reverseMap := map[string]string{}
	used := map[string]struct{}{}
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, exists := shortMap[name]; exists {
			continue
		}
		candidate := shortenCodexToolName(name)
		if _, exists := used[candidate]; exists {
			for i := 1; ; i++ {
				suffix := fmt.Sprintf("_%d", i)
				allowed := codexToolNameMaxLen - len(suffix)
				if allowed < 0 {
					allowed = 0
				}
				base := candidate
				if len(base) > allowed {
					base = base[:allowed]
				}
				tmp := base + suffix
				if _, ok := used[tmp]; !ok {
					candidate = tmp
					break
				}
			}
		}
		used[candidate] = struct{}{}
		shortMap[name] = candidate
		reverseMap[candidate] = name
	}
	return shortMap, reverseMap
}

func collectCodexFunctionCallNames(input any) []string {
	switch value := input.(type) {
	case []any:
		names := make([]string, 0)
		for _, item := range value {
			mapped, ok := item.(map[string]any)
			if !ok || stringValue(mapped["type"]) != "function_call" {
				continue
			}
			if name := stringValue(mapped["name"]); name != "" {
				names = append(names, name)
			}
		}
		return names
	default:
		return nil
	}
}

func codexWireToolName(name string, shortMap map[string]string) string {
	if name == "" {
		return name
	}
	if short := shortMap[name]; short != "" {
		return short
	}
	return shortenCodexToolName(name)
}

func sanitizeCodexToolName(name string) string {
	sanitized := codexToolNameSanitizer.ReplaceAllString(name, "_")
	if sanitized == "" {
		return "tool"
	}
	return sanitized
}

func shortenCodexToolName(name string) string {
	name = sanitizeCodexToolName(name)
	if len(name) <= codexToolNameMaxLen {
		return name
	}
	if strings.HasPrefix(name, "mcp__") {
		if idx := strings.LastIndex(name, "__"); idx > 0 {
			candidate := "mcp__" + name[idx+2:]
			if len(candidate) <= codexToolNameMaxLen {
				return candidate
			}
			return candidate[:codexToolNameMaxLen]
		}
	}
	return name[:codexToolNameMaxLen]
}

func codexToolsWithShortNames(tools []map[string]any, shortMap map[string]string) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if function, ok := tool["function"].(map[string]any); ok {
			name := stringValue(function["name"])
			shortName := codexWireToolName(name, shortMap)
			mapped := map[string]any{
				"type":        "function",
				"name":        shortName,
				"description": function["description"],
				"parameters":  function["parameters"],
			}
			if function["strict"] != nil {
				mapped["strict"] = function["strict"]
			}
			result = append(result, mapped)
			continue
		}
		result = append(result, tool)
	}
	return result
}
