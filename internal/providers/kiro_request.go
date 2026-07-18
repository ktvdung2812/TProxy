package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	kiroAgenticSuffix   = "-agentic"
	kiroThinkingSuffix  = "-thinking"
	kiroThinkingBudget  = 16000
	kiroAgenticPrompt   = "You MUST follow chunked write protocol: never write more than 300 lines per file operation."
	kiroDefaultBuilder  = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
	kiroDefaultSocial   = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
)

type kiroResolvedModel struct {
	upstream string
	agentic  bool
	thinking bool
}

func resolveKiroModel(model string) kiroResolvedModel {
	result := kiroResolvedModel{upstream: model}
	if strings.HasSuffix(result.upstream, kiroAgenticSuffix) {
		result.agentic = true
		result.upstream = strings.TrimSuffix(result.upstream, kiroAgenticSuffix)
	}
	if strings.HasSuffix(result.upstream, kiroThinkingSuffix) {
		result.thinking = true
		result.upstream = strings.TrimSuffix(result.upstream, kiroThinkingSuffix)
	}
	return result
}

func buildKiroPayload(request canonical.Request, credential store.Credential) (map[string]any, error) {
	resolved := resolveKiroModel(request.UpstreamModel)
	body := buildOpenAIBody(request)
	messages, _ := body["messages"].([]any)
	tools, _ := body["tools"].([]any)
	history, current := convertKiroMessages(messages, tools, resolved.upstream)
	authMethod := credentialExtraString(credential, "auth_method", "authMethod")
	profileARN := credentialExtraString(credential, "profile_arn", "profileArn")
	if authMethod == "api_key" || authMethod == "idc" || authMethod == "external_idp" {
		if profileARN == "" {
			profileARN = ""
		}
	} else if profileARN == "" {
		profileARN = resolveKiroDefaultProfile(authMethod)
	}
	systemParts := []string{}
	if resolved.thinking || kiroThinkingRequested(body) {
		systemParts = append(systemParts, fmt.Sprintf("<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>%d</max_thinking_length>", kiroThinkingBudget))
	}
	if resolved.agentic {
		systemParts = append(systemParts, kiroAgenticPrompt)
	}
	systemPrompt := strings.Join(systemParts, "\n\n")
	timestamp := time.Now().UTC().Format(time.RFC3339)
	timeContext := "[Context: Current time is " + timestamp + "]"
	contentPrefix := strings.TrimSpace(strings.Join([]string{systemPrompt, timeContext}, "\n\n"))
	if contentPrefix != "" {
		prefixUserMessage(current, contentPrefix, resolved.upstream)
	}
	conversationID := strings.TrimSpace(request.SessionID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(request.RequestID)
	}
	if conversationID == "" {
		conversationID = uuid.NewString()
	}
	payload := map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType":     "MANUAL",
			"conversationId":      conversationID,
			"agentContinuationId": conversationID,
			"agentTaskType":       "vibe",
			"currentMessage":      current,
			"history":             history,
		},
		"agentMode": "vibe",
	}
	if profileARN != "" {
		payload["profileArn"] = profileARN
	}
	if systemPrompt != "" {
		payload["systemPrompt"] = systemPrompt
	}
	payload["inferenceConfig"] = map[string]any{"maxTokens": 32000}
	return payload, nil
}

func prefixUserMessage(current map[string]any, prefix, modelID string) {
	uim, _ := current["userInputMessage"].(map[string]any)
	if uim == nil {
		uim = map[string]any{"content": "", "modelId": modelID}
		current["userInputMessage"] = uim
	}
	content := stringValue(uim["content"])
	if content != "" {
		uim["content"] = prefix + "\n\n" + content
	} else {
		uim["content"] = prefix
	}
	if stringValue(uim["modelId"]) == "" {
		uim["modelId"] = modelID
	}
}

func resolveKiroDefaultProfile(authMethod string) string {
	if authMethod == "google" || authMethod == "github" {
		return kiroDefaultSocial
	}
	return kiroDefaultBuilder
}

func kiroThinkingRequested(body map[string]any) bool {
	if effort := stringValue(firstValue(body, "reasoning_effort")); effort != "" && effort != "none" && effort != "off" {
		return true
	}
	if reasoning, ok := body["reasoning"].(map[string]any); ok {
		if effort := stringValue(reasoning["effort"]); effort != "" && effort != "none" && effort != "off" {
			return true
		}
	}
	model := strings.ToLower(stringValue(body["model"]))
	return strings.Contains(model, "thinking") || strings.Contains(model, "-reason")
}

func convertKiroMessages(messages []any, tools []any, model string) ([]map[string]any, map[string]any) {
	clientTools := len(tools) > 0
	if !clientTools {
		messages = flattenKiroToolMessages(messages)
	}
	history := []map[string]any{}
	var current map[string]any
	pendingUser := []string{}
	pendingAssistant := []string{}
	pendingToolResults := []map[string]any{}
	pendingImages := []any{}
	currentRole := ""
	toolsInjected := false
	flush := func() {
		switch currentRole {
		case "user":
			content := strings.TrimSpace(strings.Join(pendingUser, "\n\n"))
			if content == "" {
				content = "continue"
			}
			userMsg := map[string]any{
				"userInputMessage": map[string]any{
					"content": content,
					"modelId": "",
				},
			}
			uim := userMsg["userInputMessage"].(map[string]any)
			if len(pendingImages) > 0 {
				uim["images"] = pendingImages
			}
			if len(pendingToolResults) > 0 {
				uim["userInputMessageContext"] = map[string]any{"toolResults": pendingToolResults}
			}
			if clientTools && !toolsInjected {
				ctx, _ := uim["userInputMessageContext"].(map[string]any)
				if ctx == nil {
					ctx = map[string]any{}
					uim["userInputMessageContext"] = ctx
				}
				ctx["tools"] = mapKiroTools(tools)
				toolsInjected = true
			}
			history = append(history, userMsg)
			current = userMsg
			pendingUser, pendingToolResults, pendingImages = nil, nil, nil
		case "assistant":
			content := strings.TrimSpace(strings.Join(pendingAssistant, "\n\n"))
			if content == "" {
				content = "..."
			}
			history = append(history, map[string]any{"assistantResponseMessage": map[string]any{"content": content}})
			pendingAssistant = nil
		}
	}
	for _, raw := range messages {
		msg, _ := raw.(map[string]any)
		if msg == nil {
			continue
		}
		role := stringValue(msg["role"])
		wasSystem := role == "system"
		if role == "system" || role == "tool" {
			role = "user"
		}
		if currentRole != "" && role != currentRole {
			flush()
		}
		currentRole = role
		switch role {
		case "user":
			text, images, toolResults := extractKiroUserContent(msg)
			if msg["role"] == "tool" {
				toolResults = append(toolResults, map[string]any{
					"toolUseId": stringValue(msg["tool_call_id"]),
					"status":    "success",
					"content":   []map[string]any{{"text": stringValue(msg["content"])}},
				})
			} else if text != "" {
				if wasSystem {
					text = "<instructions>\n" + text + "\n</instructions>"
				}
				pendingUser = append(pendingUser, text)
			}
			pendingImages = append(pendingImages, images...)
			pendingToolResults = append(pendingToolResults, toolResults...)
		case "assistant":
			text, toolUses := extractKiroAssistantContent(msg)
			if text != "" {
				pendingAssistant = append(pendingAssistant, text)
			}
			if len(toolUses) > 0 {
				flush()
				last := history[len(history)-1]
				arm, _ := last["assistantResponseMessage"].(map[string]any)
				if arm == nil {
					last = map[string]any{"assistantResponseMessage": map[string]any{"content": "..."}}
					history = append(history, last)
					arm = last["assistantResponseMessage"].(map[string]any)
				}
				arm["toolUses"] = toolUses
				currentRole = ""
			}
		}
	}
	if currentRole != "" {
		flush()
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i]["userInputMessage"] != nil {
			current = history[i]
			history = append(history[:i], history[i+1:]...)
			break
		}
	}
	if current == nil {
		current = map[string]any{"userInputMessage": map[string]any{"content": "", "modelId": model}}
	}
	var firstTools any
	if len(history) > 0 {
		if uim, ok := history[0]["userInputMessage"].(map[string]any); ok {
			if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
				firstTools = ctx["tools"]
				delete(ctx, "tools")
				if len(ctx) == 0 {
					delete(uim, "userInputMessageContext")
				}
			}
		}
	}
	history = mergeConsecutiveKiroUsers(history, model)
	if firstTools != nil {
		uim := current["userInputMessage"].(map[string]any)
		ctx, _ := uim["userInputMessageContext"].(map[string]any)
		if ctx == nil {
			ctx = map[string]any{}
			uim["userInputMessageContext"] = ctx
		}
		if ctx["tools"] == nil {
			ctx["tools"] = firstTools
		}
	}
	ensureKiroModelIDs(history, current, model)
	return history, current
}

func ensureKiroModelIDs(history []map[string]any, current map[string]any, model string) {
	for _, item := range history {
		if uim, ok := item["userInputMessage"].(map[string]any); ok && stringValue(uim["modelId"]) == "" {
			uim["modelId"] = model
		}
	}
	if uim, ok := current["userInputMessage"].(map[string]any); ok {
		if stringValue(uim["modelId"]) == "" {
			uim["modelId"] = model
		}
	}
}

func mergeConsecutiveKiroUsers(history []map[string]any, model string) []map[string]any {
	out := []map[string]any{}
	for _, item := range history {
		if item["userInputMessage"] == nil || len(out) == 0 || out[len(out)-1]["userInputMessage"] == nil {
			out = append(out, item)
			continue
		}
		prev := out[len(out)-1]
		prevUIM := prev["userInputMessage"].(map[string]any)
		curUIM := item["userInputMessage"].(map[string]any)
		prevUIM["content"] = stringValue(prevUIM["content"]) + "\n\n" + stringValue(curUIM["content"])
		prevCtx, _ := prevUIM["userInputMessageContext"].(map[string]any)
		curCtx, _ := curUIM["userInputMessageContext"].(map[string]any)
		if curCtx != nil {
			if prevCtx == nil {
				prevUIM["userInputMessageContext"] = curCtx
			} else {
				prevCtx["toolResults"] = append(toMapSlice(prevCtx["toolResults"]), toMapSlice(curCtx["toolResults"])...)
			}
		}
	}
	return out
}

func toMapSlice(value any) []map[string]any {
	items, _ := value.([]any)
	out := []map[string]any{}
	for _, raw := range items {
		if item, ok := raw.(map[string]any); ok {
			out = append(out, item)
		}
	}
	return out
}

func mapKiroTools(tools []any) []map[string]any {
	out := []map[string]any{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool == nil {
			continue
		}
		fn, _ := tool["function"].(map[string]any)
		name := stringValue(firstValue(fn, "name"))
		if name == "" {
			name = stringValue(tool["name"])
		}
		desc := stringValue(firstValue(fn, "description"))
		if desc == "" {
			desc = stringValue(tool["description"])
		}
		if desc == "" {
			desc = "Tool: " + name
		}
		schema, _ := firstValue(fn, "parameters").(map[string]any)
		if schema == nil {
			if alt, ok := tool["parameters"].(map[string]any); ok {
				schema = alt
			}
		}
		if schema == nil {
			if alt, ok := tool["input_schema"].(map[string]any); ok {
				schema = alt
			}
		}
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
		} else {
			if _, ok := schema["required"]; !ok {
				schema["required"] = []any{}
			}
		}
		out = append(out, map[string]any{
			"toolSpecification": map[string]any{
				"name":        name,
				"description": desc,
				"inputSchema": map[string]any{"json": schema},
			},
		})
	}
	return out
}

func flattenKiroToolMessages(messages []any) []any {
	out := []any{}
	for _, raw := range messages {
		msg, _ := raw.(map[string]any)
		if msg == nil {
			continue
		}
		role := stringValue(msg["role"])
		if role == "tool" {
			out = append(out, map[string]any{
				"role":    "user",
				"content": "[Tool result: " + stringValue(msg["content"]) + "]",
			})
			continue
		}
		if role == "assistant" {
			parts := []string{}
			if text := stringValue(msg["content"]); text != "" {
				parts = append(parts, text)
			}
			for _, tc := range mapSlice(msg["tool_calls"]) {
				fn, _ := tc["function"].(map[string]any)
				parts = append(parts, "[Tool call: "+stringValue(fn["name"])+"("+stringValue(fn["arguments"])+")]")
			}
			out = append(out, map[string]any{"role": "assistant", "content": strings.Join(parts, "\n")})
			continue
		}
		out = append(out, msg)
	}
	return out
}

func extractKiroUserContent(msg map[string]any) (string, []any, []map[string]any) {
	content := msg["content"]
	switch v := content.(type) {
	case string:
		return v, nil, nil
	case []any:
		textParts := []string{}
		images := []any{}
		toolResults := []map[string]any{}
		for _, raw := range v {
			block, _ := raw.(map[string]any)
			if block == nil {
				continue
			}
			switch stringValue(block["type"]) {
			case "text":
				textParts = append(textParts, stringValue(block["text"]))
			case "tool_result":
				toolResults = append(toolResults, map[string]any{
					"toolUseId": stringValue(block["tool_use_id"]),
					"status":    "success",
					"content":   []map[string]any{{"text": stringifyContent(block["content"])}},
				})
			case "image_url":
				if urlMap, ok := block["image_url"].(map[string]any); ok {
					if parsed := parseDataURIImage(stringValue(urlMap["url"])); parsed != nil {
						images = append(images, parsed)
					} else {
						textParts = append(textParts, "[Image: "+stringValue(urlMap["url"])+"]")
					}
				}
			}
		}
		return strings.Join(textParts, "\n"), images, toolResults
	default:
		return stringifyContent(content), nil, nil
	}
}

func extractKiroAssistantContent(msg map[string]any) (string, []map[string]any) {
	text := ""
	toolUses := []map[string]any{}
	if content, ok := msg["content"].(string); ok {
		text = content
	}
	for _, tc := range mapSlice(msg["tool_calls"]) {
		fn, _ := tc["function"].(map[string]any)
		args := fn["arguments"]
		var input any
		if s, ok := args.(string); ok {
			_ = json.Unmarshal([]byte(s), &input)
			if input == nil {
				input = map[string]any{}
			}
		}
		toolUses = append(toolUses, map[string]any{
			"toolUseId": stringValue(firstValue(tc, "id")),
			"name":      stringValue(fn["name"]),
			"input":     input,
		})
	}
	return text, toolUses
}

func parseDataURIImage(uri string) map[string]any {
	if !strings.HasPrefix(uri, "data:") {
		return nil
	}
	parts := strings.SplitN(uri, ",", 2)
	if len(parts) != 2 {
		return nil
	}
	meta := parts[0]
	data := parts[1]
	mime := strings.TrimPrefix(meta, "data:")
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = mime[:idx]
	}
	format := mime
	if idx := strings.Index(mime, "/"); idx >= 0 {
		format = mime[idx+1:]
	}
	return map[string]any{
		"format": format,
		"source": map[string]any{"bytes": data},
	}
}

func stringifyContent(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := []string{}
		for _, raw := range v {
			if block, ok := raw.(map[string]any); ok {
				parts = append(parts, stringValue(block["text"]))
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(value)
	}
}

func credentialExtraString(credential store.Credential, keys ...string) string {
	for _, key := range keys {
		if credential.Metadata != nil {
			if value := stringValue(credential.Metadata[key]); value != "" {
				return value
			}
		}
		if credential.OAuthToken != nil && credential.OAuthToken.Extra != nil {
			if value := stringValue(credential.OAuthToken.Extra[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func stableQoderHash(prefix string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(prefix))
	for _, part := range parts {
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
