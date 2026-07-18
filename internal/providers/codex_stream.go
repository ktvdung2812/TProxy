package providers

import (
	"fmt"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

const codexToolNameMaxLen = 64

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
			Type:  canonical.EventReasoningDelta,
			ID:    state.responseID,
			Model: state.model,
			Reasoning: "\n\n",
		}}

	case "response.output_item.added":
		item, _ := raw["item"].(map[string]any)
		if stringValue(item["type"]) != "function_call" {
			return nil
		}
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

	case "response.output_item.done":
		item, _ := raw["item"].(map[string]any)
		if item == nil {
			return nil
		}
		switch stringValue(item["type"]) {
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
		message := stringValue(firstAny(raw["message"], nestedMapValue(raw, "error", "message")))
		if message == "" {
			message = "Codex upstream response failed"
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

func restoreCodexToolName(name string, reverse map[string]string) string {
	if restored, ok := reverse[name]; ok && restored != "" {
		return restored
	}
	return name
}

func buildCodexToolNameMaps(tools []map[string]any) (map[string]string, map[string]string) {
	shortMap := map[string]string{}
	reverseMap := map[string]string{}
	used := map[string]struct{}{}
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		name := stringValue(function["name"])
		if name == "" {
			name = stringValue(tool["name"])
		}
		if name == "" {
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

func shortenCodexToolName(name string) string {
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
			shortName := shortMap[name]
			if shortName == "" {
				shortName = name
			}
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
