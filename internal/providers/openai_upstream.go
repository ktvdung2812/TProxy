package providers

import (
	"strings"

	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/translator/claudeopenai"
)

func sanitizeOpenAIUpstreamBody(provider store.Provider, body map[string]any, stream bool) map[string]any {
	if body == nil {
		return map[string]any{}
	}
	normalizeOpenAIMessages(body)
	normalizeOpenAITools(body)
	if choice := body["tool_choice"]; choice != nil {
		body["tool_choice"] = normalizeOpenAIToolChoice(choice)
	}
	if isGLMProvider(provider) {
		model := stringValue(body["model"])
		stripGLMImageContent(body, model)
		applyGLMThinking(body)
	}
	if provider.Type == "xai" {
		applyXAICompletionsCompat(body)
	}
	if stream && !openAISupportsStreamOptions(provider) {
		delete(body, "stream_options")
	}
	return body
}

func isGLMProvider(provider store.Provider) bool {
	switch provider.ID {
	case "glm", "glm-cn":
		return true
	default:
		return strings.Contains(strings.ToLower(provider.BaseURL), "bigmodel.cn") ||
			strings.Contains(strings.ToLower(provider.BaseURL), "api.z.ai")
	}
}

func openAISupportsStreamOptions(provider store.Provider) bool {
	switch provider.ID {
	case "glm", "glm-cn", "codex", "ollama", "ollama-local", "deepseek", "perplexity", "perplexity-agent":
		return false
	default:
		return provider.Type == "openai-compatible" || provider.Type == "openai"
	}
}

func applyGLMThinking(body map[string]any) {
	effort := strings.TrimSpace(stringValue(body["reasoning_effort"]))
	delete(body, "reasoning_effort")
	delete(body, "reasoning")

	if thinking := asMap(body["thinking"]); thinking != nil {
		if disabled, _ := thinking["disabled"].(bool); disabled {
			body["enable_thinking"] = false
			delete(body, "thinking")
			return
		}
		if stringValue(thinking["type"]) == "disabled" {
			body["enable_thinking"] = false
			delete(body, "thinking")
			return
		}
		return
	}
	if enable, ok := body["enable_thinking"].(bool); ok {
		if !enable {
			delete(body, "thinking")
			return
		}
		body["thinking"] = map[string]any{"type": "enabled"}
		delete(body, "enable_thinking")
		return
	}

	switch strings.ToLower(effort) {
	case "none", "off", "disabled":
		body["enable_thinking"] = false
		delete(body, "thinking")
	default:
		// GLM reasoning models (glm-5.x) expect explicit thinking on by default.
		body["thinking"] = map[string]any{"type": "enabled"}
	}
}

func stripGLMImageContent(body map[string]any, model string) {
	lowerModel := strings.ToLower(model)
	if strings.Contains(lowerModel, "v") || strings.Contains(lowerModel, "vision") || strings.Contains(lowerModel, "4.6v") {
		return
	}
	messages, ok := body["messages"].([]any)
	if !ok {
		return
	}
	for i, item := range messages {
		msg := asMap(item)
		if msg == nil {
			continue
		}
		parts, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(parts))
		for _, part := range parts {
			block := asMap(part)
			if block != nil && stringValue(block["type"]) == "image_url" {
				continue
			}
			filtered = append(filtered, part)
		}
		if len(filtered) == len(parts) {
			continue
		}
		if len(filtered) == 0 {
			msg["content"] = " "
		} else {
			msg["content"] = filtered
		}
		messages[i] = msg
	}
	body["messages"] = messages
}

func normalizeOpenAIToolChoice(choice any) any {
	if choice == nil {
		return choice
	}
	if mapped := asMap(choice); mapped != nil {
		switch stringValue(mapped["type"]) {
		case "function":
			if fn := asMap(mapped["function"]); fn != nil && stringValue(fn["name"]) != "" {
				return choice
			}
			if name := stringValue(mapped["name"]); name != "" {
				return map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": name,
					},
				}
			}
		}
	}
	return claudeopenai.NormalizeOpenAIToolChoice(choice)
}

func normalizeOpenAIMessages(body map[string]any) {
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) == 0 {
		return
	}
	for i, item := range messages {
		msg := asMap(item)
		if msg == nil {
			continue
		}
		if stringValue(msg["role"]) == "developer" {
			msg["role"] = "system"
			messages[i] = msg
		}
	}
	body["messages"] = messages
}

func normalizeOpenAITools(body map[string]any) {
	tools, ok := body["tools"].([]any)
	if !ok {
		return
	}
	if len(tools) == 0 {
		delete(body, "tools")
		delete(body, "tool_choice")
	}
}

func asMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}
