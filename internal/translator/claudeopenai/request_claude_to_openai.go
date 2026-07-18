package claudeopenai

import (
	"encoding/json"
	"strings"
)

// ClaudeToOpenAIRequest converts an Anthropic /v1/messages body to OpenAI chat completions format.
// Ported from 9router open-sse/translator/request/claude-to-openai.js and proxy translators/anthropic.ts.
func ClaudeToOpenAIRequest(model string, body map[string]any, stream bool) map[string]any {
	result := map[string]any{
		"model":    model,
		"messages": []any{},
		"stream":   stream,
	}
	if maxTokens, ok := body["max_tokens"]; ok {
		result["max_tokens"] = maxTokens
	}
	if temp, ok := body["temperature"]; ok {
		result["temperature"] = temp
	}
	if topP, ok := body["top_p"]; ok {
		result["top_p"] = topP
	}
	if stop := body["stop_sequences"]; stop != nil {
		result["stop"] = stop
	}

	messages := []any{}
	if system := body["system"]; system != nil {
		systemText := extractClaudeSystemText(system)
		if systemText != "" {
			messages = append(messages, map[string]any{"role": "system", "content": systemText})
		}
	}
	for _, item := range sliceValue(body["messages"]) {
		for _, converted := range convertClaudeMessage(mapValue(item)) {
			messages = append(messages, converted)
		}
	}
	fixMissingToolResponsesOpenAI(messages)
	result["messages"] = messages

	if tools := sliceValue(body["tools"]); len(tools) > 0 {
		openAITools := make([]any, 0, len(tools))
		for _, item := range tools {
			tool := mapValue(item)
			if tool == nil {
				continue
			}
			if stringValue(tool["type"]) == "function" && tool["function"] != nil {
				openAITools = append(openAITools, tool)
				continue
			}
			openAITools = append(openAITools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        stringValue(tool["name"]),
					"description": stringValue(tool["description"]),
					"parameters":  firstAny(tool["input_schema"], tool["parameters"], map[string]any{"type": "object", "properties": map[string]any{}}),
				},
			})
		}
		result["tools"] = openAITools
	}
	if choice := body["tool_choice"]; choice != nil {
		result["tool_choice"] = convertClaudeToolChoice(choice)
	}
	if effort, ok := body["reasoning_effort"]; ok {
		result["reasoning_effort"] = effort
	} else if reasoning := mapValue(body["reasoning"]); reasoning != nil {
		if effort, ok := reasoning["effort"]; ok {
			result["reasoning_effort"] = effort
		}
		result["reasoning"] = reasoning
	}
	if thinking := mapValue(body["thinking"]); thinking != nil {
		if budget, ok := thinking["budget_tokens"]; ok {
			result["reasoning_effort"] = claudeThinkingBudgetToEffort(numberValue(budget))
		}
	}
	return result
}

func extractClaudeSystemText(system any) string {
	return SystemInstructionsText(system)
}

// SystemInstructionsText normalizes Anthropic system prompts (string or text blocks) for Codex instructions.
func SystemInstructionsText(system any) string {
	switch value := system.(type) {
	case string:
		return stripAnthropicBillingHeader(value)
	case []any:
		var parts []string
		for _, item := range value {
			block := mapValue(item)
			if stringValue(block["type"]) == "text" {
				if text := stripAnthropicBillingHeader(stringValue(block["text"])); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func convertClaudeMessage(msg map[string]any) []map[string]any {
	if msg == nil {
		return nil
	}
	if stringValue(msg["role"]) == "system" {
		if text := systemReminderText(msg["content"]); text != "" {
			return []map[string]any{{"role": "user", "content": text}}
		}
		return nil
	}
	role := stringValue(msg["role"])
	if role == "tool" {
		role = "user"
	}
	if role != "assistant" {
		role = "user"
	}
	if role == "assistant" {
		return []map[string]any{convertClaudeAssistantMessage(msg)}
	}
	return convertClaudeUserMessage(msg)
}

func systemReminderText(content any) string {
	var parts []string
	switch value := content.(type) {
	case string:
		parts = []string{value}
	case []any:
		for _, item := range value {
			block := mapValue(item)
			if stringValue(block["type"]) == "text" {
				parts = append(parts, stringValue(block["text"]))
			}
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return ""
	}
	return "<instructions>\n" + text + "\n</instructions>"
}

func convertClaudeAssistantMessage(msg map[string]any) map[string]any {
	result := map[string]any{"role": "assistant"}
	var textParts []string
	var reasoningParts []string
	var toolCalls []any
	blocks := claudeContentBlocks(msg["content"])
	for _, block := range blocks {
		switch stringValue(block["type"]) {
		case "text":
			textParts = append(textParts, stringValue(block["text"]))
		case "thinking":
			reasoningParts = append(reasoningParts, stringValue(block["thinking"]))
		case "tool_use":
			toolCalls = append(toolCalls, map[string]any{
				"id":   stringValue(block["id"]),
				"type": "function",
				"function": map[string]any{
					"name":      stringValue(block["name"]),
					"arguments": marshalJSON(block["input"]),
				},
			})
		}
	}
	if len(textParts) > 0 {
		result["content"] = strings.Join(textParts, "")
	} else if len(toolCalls) == 0 {
		result["content"] = ""
	}
	if len(reasoningParts) > 0 {
		result["reasoning_content"] = strings.Join(reasoningParts, "\n")
	}
	if len(toolCalls) > 0 {
		result["tool_calls"] = toolCalls
	}
	return result
}

func convertClaudeUserMessage(msg map[string]any) []map[string]any {
	blocks := claudeContentBlocks(msg["content"])
	var parts []map[string]any
	var toolResults []map[string]any
	flush := func() {
		if len(parts) == 0 {
			return
		}
		toolResults = append(toolResults, map[string]any{"role": "user", "content": collapseTextParts(parts)})
		parts = nil
	}
	for _, block := range blocks {
		switch stringValue(block["type"]) {
		case "text":
			parts = append(parts, map[string]any{"type": "text", "text": stringValue(block["text"])})
		case "image":
			if image := claudeImageToOpenAI(block); image != nil {
				parts = append(parts, image)
			}
		case "tool_result":
			flush()
			toolResults = append(toolResults, map[string]any{
				"role":         "tool",
				"tool_call_id": stringValue(block["tool_use_id"]),
				"content":      stringifyClaudeContent(block["content"]),
			})
		}
	}
	flush()
	if len(toolResults) == 0 {
		return []map[string]any{{"role": "user", "content": collapseTextParts(parts)}}
	}
	return toolResults
}

func claudeContentBlocks(content any) []map[string]any {
	switch value := content.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []map[string]any{{"type": "text", "text": value}}
	case []any:
		blocks := make([]map[string]any, 0, len(value))
		for _, item := range value {
			if block := mapValue(item); block != nil {
				blocks = append(blocks, block)
			}
		}
		return blocks
	default:
		return nil
	}
}

func claudeImageToOpenAI(block map[string]any) map[string]any {
	source := mapValue(block["source"])
	if source == nil {
		return nil
	}
	switch stringValue(source["type"]) {
	case "base64":
		return map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": encodeDataURI(stringValue(source["media_type"]), stringValue(source["data"])),
			},
		}
	case "url":
		return map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": stringValue(source["url"])},
		}
	default:
		return nil
	}
}

func fixMissingToolResponsesOpenAI(messages []any) {
	for i := 0; i < len(messages); i++ {
		msg := mapValue(messages[i])
		if msg == nil || stringValue(msg["role"]) != "assistant" {
			continue
		}
		toolCalls := sliceValue(msg["tool_calls"])
		if len(toolCalls) == 0 {
			continue
		}
		ids := make([]string, 0, len(toolCalls))
		for _, call := range toolCalls {
			ids = append(ids, stringValue(mapValue(call)["id"]))
		}
		responded := map[string]bool{}
		insertAt := i + 1
		for j := i + 1; j < len(messages); j++ {
			next := mapValue(messages[j])
			if next == nil || stringValue(next["role"]) != "tool" {
				break
			}
			responded[stringValue(next["tool_call_id"])] = true
			insertAt = j + 1
		}
		missing := make([]any, 0)
		for _, id := range ids {
			if id == "" || responded[id] {
				continue
			}
			missing = append(missing, map[string]any{
				"role":         "tool",
				"tool_call_id": id,
				"content":      "[No response received]",
			})
		}
		if len(missing) == 0 {
			continue
		}
		messages = append(messages[:insertAt], append(missing, messages[insertAt:]...)...)
		i = insertAt + len(missing) - 1
	}
}

func claudeThinkingBudgetToEffort(budget int) string {
	switch {
	case budget <= 0:
		return "low"
	case budget < 8000:
		return "medium"
	case budget < 24000:
		return "high"
	default:
		return "xhigh"
	}
}

func marshalJSON(value any) string {
	if value == nil {
		return "{}"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func numberValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

func firstAny(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
