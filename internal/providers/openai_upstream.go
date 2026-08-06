package providers

import (
	"encoding/json"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/translator/claudeopenai"
)

// unwrapOpenAIChatCompletion flattens gateway envelopes such as Cline's
// { "success": true, "data": { "id": ..., "choices": [...] } } into a standard
// OpenAI chat.completion object. Leaves standard responses unchanged.
func unwrapOpenAIChatCompletion(raw map[string]any) map[string]any {
	if raw == nil {
		return raw
	}
	if _, hasChoices := raw["choices"]; hasChoices {
		return raw
	}
	data, ok := raw["data"].(map[string]any)
	if !ok || data == nil {
		return raw
	}
	if _, hasChoices := data["choices"]; !hasChoices {
		return raw
	}
	return data
}

// unwrapOpenAIChatCompletionBody rewrites a JSON body when it uses the Cline
// {success,data} envelope so downstream clients receive a normal OpenAI payload.
func unwrapOpenAIChatCompletionBody(body []byte) []byte {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}
	unwrapped := unwrapOpenAIChatCompletion(raw)
	if unwrapped == nil || len(unwrapped) == 0 {
		return body
	}
	// Only rewrite when we actually unwrapped (identity pointer means no change
	// is hard to detect; compare presence of top-level choices after unwrap).
	if _, had := raw["choices"]; had {
		return body
	}
	if _, has := unwrapped["choices"]; !has {
		return body
	}
	out, err := json.Marshal(unwrapped)
	if err != nil {
		return body
	}
	return out
}

func sanitizeOpenAIUpstreamBody(provider store.Provider, body map[string]any, stream bool) map[string]any {
	if body == nil {
		return map[string]any{}
	}
	if isClinePassProvider(provider) {
		if model := normalizeClinePassModelID(stringValue(body["model"])); model != "" {
			// ClinePass advertises its subscription models with the cline-pass/
			// namespace. Existing routes can contain a generic provider prefix
			// (for example deepseek/deepseek-v4-pro); normalize only the wire
			// model so those routes continue to work without a database migration.
			body["model"] = model
		}
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

func isClinePassProvider(provider store.Provider) bool {
	return strings.EqualFold(strings.TrimSpace(provider.Type), "clinepass") ||
		strings.EqualFold(strings.TrimSpace(provider.ID), "clinepass")
}

func normalizeClinePassModelID(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" || strings.HasPrefix(strings.ToLower(trimmed), "cline-pass/") {
		return trimmed
	}
	if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
		trimmed = strings.TrimSpace(trimmed[slash+1:])
	}
	if trimmed == "" {
		return ""
	}
	return "cline-pass/" + trimmed
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
	messages := openAIMessageMaps(body["messages"])
	if len(messages) == 0 {
		return
	}
	for _, msg := range messages {
		if stringValue(msg["role"]) == "developer" {
			msg["role"] = "system"
		}
		msg["content"] = normalizeOpenAIContent(msg["content"])
	}
	messages = repairOpenAIToolMessages(messages)
	items := make([]any, 0, len(messages))
	for _, msg := range messages {
		items = append(items, msg)
	}
	body["messages"] = items
}

// openAIMessageMaps accepts both []any (raw client JSON) and []canonical.Message
// (bodies built from request.Messages) and returns a mutable list of message maps.
func openAIMessageMaps(value any) []map[string]any {
	switch items := value.(type) {
	case []any:
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if msg := asMap(item); msg != nil {
				result = append(result, msg)
			}
		}
		return result
	case []map[string]any:
		return items
	case []canonical.Message:
		result := make([]map[string]any, 0, len(items))
		for _, message := range items {
			msg := map[string]any{"role": message.Role}
			if message.Content != nil {
				msg["content"] = message.Content
			}
			if message.Name != "" {
				msg["name"] = message.Name
			}
			if message.ToolCallID != "" {
				msg["tool_call_id"] = message.ToolCallID
			}
			if len(message.ToolCalls) > 0 {
				msg["tool_calls"] = message.ToolCalls
			}
			result = append(result, msg)
		}
		return result
	default:
		return nil
	}
}

// missingToolResultText stands in for a tool result the client never sent, which
// happens when a turn is aborted mid tool call and the history is replayed.
const missingToolResultText = "[No response received]"

// repairOpenAIToolMessages enforces the chat.completions contract that an assistant
// message with tool_calls is immediately followed by one tool message per
// tool_call_id. Histories replayed by /responses clients routinely break it: parallel
// function_call items arrive as separate assistant messages, and aborted turns leave
// calls with no output. Upstreams reject both with "An assistant message with
// 'tool_calls' must be followed by tool messages responding to each 'tool_call_id'".
func repairOpenAIToolMessages(messages []map[string]any) []map[string]any {
	messages = mergeOpenAIToolCallMessages(messages)
	result := make([]map[string]any, 0, len(messages))
	var pending []string
	answered := map[string]bool{}

	closePending := func() {
		for _, id := range pending {
			if answered[id] {
				continue
			}
			result = append(result, map[string]any{
				"role":         "tool",
				"tool_call_id": id,
				"content":      missingToolResultText,
			})
		}
		pending = nil
		answered = map[string]bool{}
	}

	for _, msg := range messages {
		if stringValue(msg["role"]) == "tool" {
			id := stringValue(msg["tool_call_id"])
			// A tool message that answers no open call makes upstreams fail the same
			// way the missing ones do, so drop it instead of forwarding it.
			if id == "" || answered[id] || !containsString(pending, id) {
				continue
			}
			answered[id] = true
			result = append(result, msg)
			continue
		}
		closePending()
		pending = openAIToolCallIDs(msg)
		if _, declared := msg["tool_calls"]; declared && len(openAIToolCalls(msg)) == 0 {
			// An empty tool_calls array is rejected as too short by strict upstreams.
			msg = withoutOpenAIKey(msg, "tool_calls")
		}
		result = append(result, msg)
	}
	closePending()
	return result
}

// mergeOpenAIToolCallMessages folds an assistant message that carries only tool_calls
// into the assistant message right before it, so parallel calls end up in one message.
func mergeOpenAIToolCallMessages(messages []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		last := len(result) - 1
		if last < 0 || stringValue(msg["role"]) != "assistant" || stringValue(result[last]["role"]) != "assistant" {
			result = append(result, msg)
			continue
		}
		calls := openAIToolCalls(msg)
		if len(calls) == 0 || !isEmptyOpenAIContent(msg["content"]) {
			result = append(result, msg)
			continue
		}
		// Copy instead of mutating: the maps can be shared with the original request
		// body, which is reused when a request is retried against another provider.
		merged := make(map[string]any, len(result[last])+1)
		for key, value := range result[last] {
			merged[key] = value
		}
		merged["tool_calls"] = append(append([]any{}, openAIToolCalls(result[last])...), calls...)
		result[last] = merged
	}
	return result
}

// withoutOpenAIKey copies a message without one key, so repairs never mutate maps
// shared with the original request body.
func withoutOpenAIKey(msg map[string]any, key string) map[string]any {
	result := make(map[string]any, len(msg))
	for name, value := range msg {
		if name != key {
			result[name] = value
		}
	}
	return result
}

func openAIToolCalls(msg map[string]any) []any {
	switch calls := msg["tool_calls"].(type) {
	case []any:
		return calls
	case []map[string]any:
		result := make([]any, 0, len(calls))
		for _, call := range calls {
			result = append(result, call)
		}
		return result
	default:
		return nil
	}
}

func openAIToolCallIDs(msg map[string]any) []string {
	calls := openAIToolCalls(msg)
	ids := make([]string, 0, len(calls))
	for _, item := range calls {
		if id := stringValue(asMap(item)["id"]); id != "" && !containsString(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func isEmptyOpenAIContent(content any) bool {
	switch value := content.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(value) == ""
	case []any:
		return len(value) == 0
	default:
		return false
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// normalizeOpenAIContent converts Codex responses content types (input_text, output_text,
// input_image) to standard OpenAI chat.completions content types (text, image_url).
// This is necessary when a /responses-protocol request falls through from the Codex
// adapter to an OpenAI-compatible provider that doesn't understand Codex types.
func normalizeOpenAIContent(content any) any {
	parts, ok := content.([]any)
	if !ok {
		return content
	}
	normalized := make([]any, 0, len(parts))
	for _, part := range parts {
		block := asMap(part)
		if block == nil {
			normalized = append(normalized, part)
			continue
		}
		switch stringValue(block["type"]) {
		case "input_text", "output_text":
			block["type"] = "text"
		case "input_image":
			block["type"] = "image_url"
			if url, ok := block["image_url"]; ok {
				block["image_url"] = map[string]any{"url": stringValue(url)}
			}
		}
		normalized = append(normalized, block)
	}
	return normalized
}

// normalizeOpenAITools converts Codex flat tool format ({type,name,description,parameters})
// to the nested OpenAI format ({type,function:{name,description,parameters}}) expected by
// chat completions providers. Tools already in OpenAI format pass through unchanged.
// Non-function tools (namespace, web_search, file_search, code_interpreter, etc.) are
// stripped because OpenAI chat completions only accepts type="function".
func normalizeOpenAITools(body map[string]any) {
	// Try []any first (from raw JSON / cloneAnyMap path).
	tools, ok := body["tools"].([]any)
	if ok {
		tools = filterAndNormalizeToolsAny(tools)
		if len(tools) == 0 {
			delete(body, "tools")
			delete(body, "tool_choice")
		} else {
			body["tools"] = tools
		}
		return
	}
	// Try []map[string]any (from request.Tools / ProtocolResponses path).
	typedTools, ok := body["tools"].([]map[string]any)
	if ok {
		typedTools = filterAndNormalizeToolsMap(typedTools)
		if len(typedTools) == 0 {
			delete(body, "tools")
			delete(body, "tool_choice")
		} else {
			body["tools"] = typedTools
		}
	}
}

func filterAndNormalizeToolsAny(tools []any) []any {
	result := make([]any, 0, len(tools))
	for _, tool := range tools {
		mapped := asMap(tool)
		if mapped == nil {
			continue
		}
		if !isOpenAICompatibleTool(mapped) {
			continue
		}
		result = append(result, normalizeOneOpenAITool(mapped))
	}
	return result
}

func filterAndNormalizeToolsMap(tools []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if !isOpenAICompatibleTool(tool) {
			continue
		}
		result = append(result, normalizeOneOpenAIToolMap(tool))
	}
	return result
}

// isOpenAICompatibleTool returns true if the tool type is supported by OpenAI chat completions.
// OpenAI only accepts type="function"; Codex-specific types (namespace, web_search,
// file_search, code_interpreter, etc.) are incompatible.
func isOpenAICompatibleTool(mapped map[string]any) bool {
	typ := stringValue(mapped["type"])
	if typ == "" {
		// No explicit type — treat as function if it has a name (Codex flat format).
		return stringValue(mapped["name"]) != ""
	}
	// Only "function" tools are OpenAI-compatible. "namespace", "web_search",
	// "file_search", "code_interpreter", etc. are Codex-only and must be stripped.
	return typ == "function"
}

// normalizeOneOpenAITool converts a single tool from []any (map[string]any) path.
func normalizeOneOpenAITool(mapped map[string]any) map[string]any {
	return normalizeOneOpenAIToolMap(mapped)
}

// normalizeOneOpenAIToolMap converts a single tool from []map[string]any path.
func normalizeOneOpenAIToolMap(mapped map[string]any) map[string]any {
	// Already in OpenAI nested format.
	if _, hasFunction := mapped["function"]; hasFunction {
		return mapped
	}
	// Codex flat format: {type: "function", name, description, parameters}
	if mapped["type"] == "function" || stringValue(mapped["name"]) != "" {
		mapped = wrapCodexToolFunction(mapped)
	}
	return mapped
}

// wrapCodexToolFunction moves Codex flat fields (name, description, parameters, strict)
// into a nested "function" map for OpenAI chat completions compatibility.
func wrapCodexToolFunction(mapped map[string]any) map[string]any {
	function := map[string]any{}
	if name := stringValue(mapped["name"]); name != "" {
		function["name"] = name
		delete(mapped, "name")
	}
	if desc := stringValue(mapped["description"]); desc != "" {
		function["description"] = desc
		delete(mapped, "description")
	}
	if params := mapped["parameters"]; params != nil {
		function["parameters"] = params
		delete(mapped, "parameters")
	}
	if strict := mapped["strict"]; strict != nil {
		function["strict"] = strict
		delete(mapped, "strict")
	}
	mapped["function"] = function
	return mapped
}

func asMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}
