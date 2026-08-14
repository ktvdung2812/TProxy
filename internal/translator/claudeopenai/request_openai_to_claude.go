package claudeopenai

import (
	"strings"
)

// OpenAIToClaudeRequest converts an OpenAI chat completions body to Anthropic /v1/messages format.
// Ported from 9router open-sse/translator/request/openai-to-claude.js and proxy translators/anthropic.ts.
func OpenAIToClaudeRequest(model string, body map[string]any, stream bool) map[string]any {
	result := map[string]any{
		"model":    model,
		"messages": []any{},
		"stream":   stream,
	}
	if maxTokens := firstAny(body["max_tokens"], body["max_completion_tokens"]); maxTokens != nil {
		result["max_tokens"] = maxTokens
	}
	if temp, ok := body["temperature"]; ok {
		result["temperature"] = temp
	}
	if topP, ok := body["top_p"]; ok {
		result["top_p"] = topP
	}

	toolNameMap := map[string]string{}
	systemParts := []string{}
	messages := []any{}
	for _, item := range sliceValue(body["messages"]) {
		msg := mapValue(item)
		if msg == nil {
			continue
		}
		if stringValue(msg["role"]) == "system" {
			systemParts = append(systemParts, extractTextContent(msg["content"]))
			continue
		}
		for _, block := range openAIMessageToClaudeBlocks(msg, toolNameMap) {
			messages = append(messages, block)
		}
	}
	result["messages"] = mergeClaudeMessages(messages)

	if responseFormat := mapValue(body["response_format"]); responseFormat != nil {
		switch stringValue(responseFormat["type"]) {
		case "json_schema":
			if schema := mapValue(mapValue(responseFormat["json_schema"])["schema"]); schema != nil {
				systemParts = append(systemParts, "You must respond with valid JSON that strictly follows the provided JSON schema. Respond ONLY with the JSON object, no other text.")
			}
		case "json_object":
			systemParts = append(systemParts, "You must respond with valid JSON. Respond ONLY with a JSON object, no other text.")
		}
	}
	if len(systemParts) > 0 {
		result["system"] = strings.Join(systemParts, "\n")
	}

	if tools := sliceValue(body["tools"]); len(tools) > 0 {
		claudeTools := make([]any, 0, len(tools))
		for _, item := range tools {
			tool := mapValue(item)
			if tool == nil {
				continue
			}
			toolType := stringValue(tool["type"])
			if toolType != "" && toolType != "function" {
				claudeTools = append(claudeTools, tool)
				continue
			}
			toolData := mapValue(tool["function"])
			if toolData == nil {
				toolData = tool
			}
			name := stringValue(toolData["name"])
			claudeTools = append(claudeTools, map[string]any{
				"name":         name,
				"description":  stringValue(toolData["description"]),
				"input_schema": firstAny(toolData["parameters"], toolData["input_schema"], map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}),
			})
		}
		result["tools"] = claudeTools
	}
	if choice := body["tool_choice"]; choice != nil {
		result["tool_choice"] = convertOpenAIToolChoice(choice)
	}
	if effort, ok := body["reasoning_effort"]; ok {
		result["reasoning_effort"] = effort
	}
	return result
}

func openAIMessageToClaudeBlocks(msg map[string]any, toolNameMap map[string]string) []map[string]any {
	role := stringValue(msg["role"])
	if role == "tool" {
		role = "user"
	}
	claudeRole := "assistant"
	if role == "user" || role == "tool" {
		claudeRole = "user"
	}
	blocks := openAIContentBlocks(msg, toolNameMap)
	if len(blocks) == 0 {
		return nil
	}
	hasToolResult := false
	for _, block := range blocks {
		if stringValue(block["type"]) == "tool_result" {
			hasToolResult = true
			break
		}
	}
	if hasToolResult {
		result := make([]map[string]any, 0)
		var other []map[string]any
		for _, block := range blocks {
			if stringValue(block["type"]) == "tool_result" {
				result = append(result, map[string]any{"role": "user", "content": []any{block}})
			} else {
				other = append(other, block)
			}
		}
		if len(other) > 0 {
			result = append(result, map[string]any{"role": claudeRole, "content": blocksToAny(other)})
		}
		return result
	}
	return []map[string]any{{"role": claudeRole, "content": blocksToAny(blocks)}}
}

func blocksToAny(blocks []map[string]any) []any {
	out := make([]any, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, block)
	}
	return out
}

func openAIContentBlocks(msg map[string]any, toolNameMap map[string]string) []map[string]any {
	role := stringValue(msg["role"])
	if role == "tool" {
		return []map[string]any{{
			"type":        "tool_result",
			"tool_use_id": stringValue(msg["tool_call_id"]),
			"content":     msg["content"],
		}}
	}
	var blocks []map[string]any
	switch content := msg["content"].(type) {
	case string:
		if content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": content})
		}
	case []any:
		for _, item := range content {
			part := mapValue(item)
			if part == nil {
				continue
			}
			switch stringValue(part["type"]) {
			case "text":
				if text := stringValue(part["text"]); text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": text})
				}
			case "tool_result":
				block := map[string]any{
					"type":        "tool_result",
					"tool_use_id": stringValue(part["tool_use_id"]),
					"content":     part["content"],
				}
				if part["is_error"] == true {
					block["is_error"] = true
				}
				blocks = append(blocks, block)
			case "image_url":
				if image := openAIImageToClaude(part); image != nil {
					blocks = append(blocks, image)
				}
			case "image":
				if source := part["source"]; source != nil {
					blocks = append(blocks, map[string]any{"type": "image", "source": source})
				}
			case "file":
				if file := mapValue(part["file"]); file != nil {
					if doc := openAIFileToClaudeDocument(file); doc != nil {
						blocks = append(blocks, doc)
					}
				}
			case "thinking":
				block := cloneMap(part)
				delete(block, "cache_control")
				blocks = append(blocks, block)
			}
		}
	}
	for _, call := range sliceValue(msg["tool_calls"]) {
		tc := mapValue(call)
		if tc == nil || stringValue(tc["type"]) != "function" {
			continue
		}
		fn := mapValue(tc["function"])
		name := stringValue(fn["name"])
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    stringValue(tc["id"]),
			"name":  name,
			"input": safeParseJSON(stringValue(fn["arguments"])),
		})
	}
	return blocks
}

func openAIImageToClaude(part map[string]any) map[string]any {
	imageURL := mapValue(part["image_url"])
	if imageURL == nil {
		return nil
	}
	url := stringValue(imageURL["url"])
	if mime, data, ok := parseDataURI(url); ok {
		return map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "base64", "media_type": mime, "data": data},
		}
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "url", "url": url},
		}
	}
	return nil
}

func openAIFileToClaudeDocument(file map[string]any) map[string]any {
	fileData := stringValue(file["file_data"])
	mime, data, ok := parseDataURI(fileData)
	if !ok || mime != "application/pdf" {
		return nil
	}
	return map[string]any{
		"type":   "document",
		"source": map[string]any{"type": "base64", "media_type": mime, "data": data},
	}
}

func mergeClaudeMessages(messages []any) []any {
	if len(messages) == 0 {
		return messages
	}
	result := make([]any, 0, len(messages))
	var currentRole string
	var currentParts []any

	flush := func() {
		if currentRole == "" || len(currentParts) == 0 {
			return
		}
		result = append(result, map[string]any{"role": currentRole, "content": currentParts})
		currentParts = nil
	}

	for _, item := range messages {
		msg := mapValue(item)
		if msg == nil {
			continue
		}
		role := stringValue(msg["role"])
		parts := anySlice(msg["content"])
		hasToolUse := false
		hasToolResult := false
		for _, part := range parts {
			block := mapValue(part)
			switch stringValue(block["type"]) {
			case "tool_use":
				hasToolUse = true
			case "tool_result":
				hasToolResult = true
			}
		}
		if hasToolResult {
			flush()
			result = append(result, map[string]any{"role": "user", "content": parts})
			currentRole = role
			continue
		}
		if currentRole != role {
			flush()
			currentRole = role
		}
		currentParts = append(currentParts, parts...)
		if hasToolUse {
			flush()
			currentRole = ""
		}
	}
	flush()
	return result
}

func anySlice(value any) []any {
	switch items := value.(type) {
	case []any:
		return items
	case []map[string]any:
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}
