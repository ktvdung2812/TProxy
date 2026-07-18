package claudeopenai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var anthropicBillingHeader = regexp.MustCompile(`(?i)^x-anthropic-billing-header:[^\n]*(?:\r?\n)?`)

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(value)
	}
}

func mapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func sliceValue(value any) []any {
	result, _ := value.([]any)
	return result
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(encoded, &out)
	return out
}

func safeParseJSON(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}
	}
	var parsed any
	if json.Unmarshal([]byte(trimmed), &parsed) == nil {
		return parsed
	}
	return raw
}

func stripAnthropicBillingHeader(text string) string {
	return anthropicBillingHeader.ReplaceAllString(text, "")
}

func extractTextContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, item := range value {
			block := mapValue(item)
			if stringValue(block["type"]) == "text" {
				if text := stringValue(block["text"]); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func collapseTextParts(parts []map[string]any) any {
	if len(parts) == 0 {
		return ""
	}
	allText := true
	for _, part := range parts {
		if stringValue(part["type"]) != "text" {
			allText = false
			break
		}
	}
	if allText {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(stringValue(part["text"]))
		}
		return builder.String()
	}
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		out = append(out, part)
	}
	return out
}

func parseDataURI(uri string) (mimeType, data string, ok bool) {
	if !strings.HasPrefix(uri, "data:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(uri, "data:")
	comma := strings.Index(rest, ",")
	if comma < 0 {
		return "", "", false
	}
	meta := rest[:comma]
	payload := rest[comma+1:]
	mimeType = strings.TrimSuffix(meta, ";base64")
	if strings.HasSuffix(meta, ";base64") {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", "", false
		}
		return mimeType, base64.StdEncoding.EncodeToString(decoded), true
	}
	return mimeType, payload, true
}

func encodeDataURI(mimeType, data string) string {
	return fmt.Sprintf("data:%s;base64,%s", mimeType, data)
}

func stringifyClaudeContent(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if items, ok := value.([]any); ok {
		var parts []string
		for _, item := range items {
			block := mapValue(item)
			if stringValue(block["type"]) == "text" {
				parts = append(parts, stringValue(block["text"]))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func convertClaudeToolChoice(choice any) any {
	if choice == nil {
		return "auto"
	}
	if text, ok := choice.(string); ok {
		return text
	}
	mapped := mapValue(choice)
	if mapped == nil {
		return "auto"
	}
	switch stringValue(mapped["type"]) {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": stringValue(mapped["name"]),
			},
		}
	case "none":
		return "none"
	default:
		return "auto"
	}
}

// NormalizeOpenAIToolChoice converts Claude-style or malformed OpenAI tool_choice
// values into the shape expected by OpenAI-compatible upstream APIs.
func NormalizeOpenAIToolChoice(choice any) any {
	return convertClaudeToolChoice(choice)
}

func convertOpenAIToolChoice(choice any) map[string]any {
	if choice == nil {
		return map[string]any{"type": "auto"}
	}
	if text, ok := choice.(string); ok {
		switch text {
		case "required":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "auto"}
		default:
			return map[string]any{"type": "auto"}
		}
	}
	mapped := mapValue(choice)
	if mapped == nil {
		return map[string]any{"type": "auto"}
	}
	if fn := mapValue(mapped["function"]); fn != nil && stringValue(fn["name"]) != "" {
		return map[string]any{"type": "tool", "name": stringValue(fn["name"])}
	}
	switch stringValue(mapped["type"]) {
	case "auto", "any", "tool", "none":
		return mapped
	default:
		return map[string]any{"type": "auto"}
	}
}

func openAIFinishToClaude(reason string, hasToolCalls bool) string {
	switch reason {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "stop":
		if hasToolCalls {
			return "tool_use"
		}
		return "end_turn"
	default:
		if hasToolCalls {
			return "tool_use"
		}
		return "end_turn"
	}
}

func claudeStopToOpenAI(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "end_turn", "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}
