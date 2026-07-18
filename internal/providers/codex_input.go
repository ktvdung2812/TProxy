package providers

import (
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

func codexMessagesToInput(messages []canonical.Message, shortMap map[string]string) []any {
	result := make([]any, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "tool":
			callID := strings.TrimSpace(message.ToolCallID)
			if callID == "" {
				continue
			}
			result = append(result, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  codexToolOutputContent(message.Content),
			})
		default:
			role := message.Role
			if role == "system" {
				role = "developer"
			}
			parts := codexContentParts(message.Role, message.Content)
			if message.Role == "user" && len(parts) == 0 {
				parts = []any{map[string]any{"type": "input_text", "text": "..."}}
			}
			if message.Role == "assistant" && len(parts) == 0 && len(message.ToolCalls) > 0 {
				for _, call := range message.ToolCalls {
					if item := codexFunctionCallItem(call, shortMap); item != nil {
						result = append(result, item)
					}
				}
				continue
			}
			if len(parts) == 0 {
				continue
			}
			result = append(result, map[string]any{
				"type":    "message",
				"role":    role,
				"content": parts,
			})
			if message.Role == "assistant" {
				for _, call := range message.ToolCalls {
					if item := codexFunctionCallItem(call, shortMap); item != nil {
						result = append(result, item)
					}
				}
			}
		}
	}
	return result
}

func codexNormalizeInputValue(input any, shortMap map[string]string) any {
	switch value := input.(type) {
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return value
		}
		return []any{map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": trimmed}},
		}}
	case []any:
		return codexNormalizeInputItems(value, shortMap)
	default:
		return input
	}
}

func codexNormalizeInputItems(items []any, shortMap map[string]string) []any {
	result := codexSanitizeInputItems(codexNormalizeInputItemsRaw(items, shortMap))
	return result
}

func codexNormalizeInputItemsRaw(items []any, shortMap map[string]string) []any {
	result := make([]any, 0, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			result = append(result, item)
			continue
		}
		normalized := map[string]any{}
		for key, value := range mapped {
			normalized[key] = value
		}
		role := stringValue(normalized["role"])
		if role == "system" {
			normalized["role"] = "developer"
		}
		if stringValue(normalized["type"]) == "" && role != "" {
			normalized["type"] = "message"
		}
		if content, exists := normalized["content"]; exists {
			if parts := codexContentParts(role, content); parts != nil {
				normalized["content"] = parts
			}
		}
		if output, exists := normalized["output"]; exists {
			normalized["output"] = codexToolOutputContent(output)
		}
		if stringValue(normalized["type"]) == "function_call" {
			normalized["name"] = codexWireToolName(stringValue(normalized["name"]), shortMap)
		}
		result = append(result, normalized)
	}
	return result
}

// codexSanitizeInputItems strips orphan reasoning item IDs from follow-up requests.
// Codex does not persist output items when store=false, so clients that echo
// reasoning items with only summary (no encrypted_content) must not send an id.
func codexSanitizeInputItems(items []any) []any {
	result := make([]any, 0, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			result = append(result, item)
			continue
		}
		if stringValue(mapped["type"]) == "reasoning" {
			result = append(result, codexSanitizeReasoningItem(mapped))
			continue
		}
		result = append(result, codexStripEphemeralItemID(mapped))
	}
	return result
}

func codexSanitizeReasoningItem(item map[string]any) map[string]any {
	sanitized := make(map[string]any, len(item))
	for key, value := range item {
		sanitized[key] = value
	}
	if !codexHasUsableReasoningEncryptedContent(sanitized["encrypted_content"]) {
		delete(sanitized, "encrypted_content")
		delete(sanitized, "id")
	}
	return sanitized
}

func codexHasUsableReasoningEncryptedContent(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

func codexIsEphemeralItemID(id string) bool {
	id = strings.TrimSpace(id)
	return strings.HasPrefix(id, "resp_") ||
		strings.HasPrefix(id, "msg_") ||
		strings.HasPrefix(id, "rs_") ||
		strings.HasPrefix(id, "fc_")
}

func codexStripEphemeralItemID(item map[string]any) map[string]any {
	sanitized := make(map[string]any, len(item))
	for key, value := range item {
		sanitized[key] = value
	}
	if codexHasUsableReasoningEncryptedContent(sanitized["encrypted_content"]) {
		return sanitized
	}
	if id := stringValue(sanitized["id"]); codexIsEphemeralItemID(id) {
		delete(sanitized, "id")
	}
	return sanitized
}

func codexContentParts(role string, content any) []any {
	partType := "input_text"
	if role == "assistant" {
		partType = "output_text"
	}
	switch value := content.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return []any{}
		}
		return []any{map[string]any{"type": partType, "text": value}}
	case []any:
		parts := make([]any, 0, len(value))
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if part := codexContentPart(role, block); part != nil {
				parts = append(parts, part)
			}
		}
		return parts
	default:
		if text := strings.TrimSpace(stringValue(content)); text != "" {
			return []any{map[string]any{"type": partType, "text": text}}
		}
		return []any{}
	}
}

func codexContentPart(role string, block map[string]any) map[string]any {
	partType := "input_text"
	if role == "assistant" {
		partType = "output_text"
	}
	switch stringValue(block["type"]) {
	case "text":
		return map[string]any{"type": partType, "text": stringValue(block["text"])}
	case "input_text", "output_text":
		mapped := map[string]any{"type": stringValue(block["type"]), "text": stringValue(block["text"])}
		if mapped["type"] == "" {
			mapped["type"] = partType
		}
		return mapped
	case "image_url":
		part := map[string]any{"type": "input_image"}
		if urlBlock, ok := block["image_url"].(map[string]any); ok {
			if url := stringValue(urlBlock["url"]); url != "" {
				part["image_url"] = url
			}
			if fileID := stringValue(urlBlock["file_id"]); fileID != "" {
				part["file_id"] = fileID
			}
			if detail := stringValue(urlBlock["detail"]); detail != "" {
				part["detail"] = detail
			}
		}
		return part
	case "input_image":
		return block
	case "input_file":
		return block
	case "input_audio":
		return block
	default:
		if text := strings.TrimSpace(stringValue(block["text"])); text != "" {
			return map[string]any{"type": partType, "text": text}
		}
		return nil
	}
}

func codexToolOutputContent(content any) any {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]any, 0, len(value))
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if part := codexContentPart("tool", block); part != nil {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return parts
	default:
		return stringValue(content)
	}
}

func codexFunctionCallItem(call map[string]any, shortMap map[string]string) map[string]any {
	function, _ := call["function"].(map[string]any)
	name := stringValue(function["name"])
	if name == "" {
		name = stringValue(call["name"])
	}
	name = codexWireToolName(name, shortMap)
	callID := stringValue(call["id"])
	if name == "" || callID == "" {
		return nil
	}
	item := map[string]any{
		"type":    "function_call",
		"call_id": callID,
		"name":    name,
	}
	if arguments := stringValue(function["arguments"]); arguments != "" {
		item["arguments"] = arguments
	}
	return item
}
