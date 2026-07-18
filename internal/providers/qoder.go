package providers

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/canonical"
	qoderpkg "github.com/tproxy/tproxy/internal/providers/qoder"
	"github.com/tproxy/tproxy/internal/store"
)

type qoderAdapter struct{ client *http.Client }

func (a *qoderAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	events, err := a.ExecuteStream(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	result := &canonical.Response{Model: request.UpstreamModel, Role: "assistant"}
	var text strings.Builder
	toolCalls := map[int]map[string]any{}
	for event := range events {
		switch event.Type {
		case canonical.EventTextDelta:
			text.WriteString(event.Text)
		case canonical.EventToolCallDelta:
			mergeToolCallDelta(toolCalls, event.ToolCall)
		case canonical.EventUsage:
			if event.Usage != nil {
				result.Usage = *event.Usage
			}
		case canonical.EventMessageEnd:
			result.FinishReason = event.FinishReason
		case canonical.EventError:
			if event.Err != nil {
				return nil, event.Err
			}
		}
	}
	result.Content = text.String()
	if len(toolCalls) > 0 {
		result.ToolCalls = orderedToolCalls(toolCalls)
	}
	return result, nil
}

func (a *qoderAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	creds, err := qoderCosyCreds(credential)
	if err != nil {
		return nil, err
	}
	payload, modelKey, modelSource, err := buildQoderPayload(ctx, a.client, request, creds)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: err.Error()}
	}
	plainBody, _ := json.Marshal(payload)
	encoded := qoderpkg.EncodeBody(plainBody)
	encodedBody := []byte(encoded)
	headers, err := qoderpkg.BuildCosyHeaders(encodedBody, qoderpkg.ChatURLEncoded, creds)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qoderpkg.ChatURLEncoded, strings.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("X-Model-Key", modelKey)
	req.Header.Set("X-Model-Source", modelSource)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := a.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, upstreamError(response)
	}
	out := make(chan canonical.Event, 32)
	go func() {
		defer close(out)
		defer response.Body.Close()
		parseQoderSSE(ctx, response, request.UpstreamModel, out)
	}()
	return out, nil
}

func qoderCosyCreds(credential store.Credential) (qoderpkg.CosyCreds, error) {
	userID := credentialExtraString(credential, "user_id", "userId")
	if userID == "" {
		return qoderpkg.CosyCreds{}, &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: "qoder credential is missing userId; reconnect the account"}
	}
	token := credential.Secret
	if credential.OAuthToken != nil && credential.OAuthToken.AccessToken != "" {
		token = credential.OAuthToken.AccessToken
	}
	if token == "" {
		return qoderpkg.CosyCreds{}, &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: "qoder credential is missing accessToken; reconnect the account"}
	}
	return qoderpkg.CosyCreds{
		UserID:    userID,
		AuthToken: token,
		Name:      credential.Label,
		Email:     credential.Email,
		MachineID: credentialExtraString(credential, "machine_id", "machineId"),
	}, nil
}

func buildQoderPayload(ctx context.Context, client *http.Client, request canonical.Request, creds qoderpkg.CosyCreds) (map[string]any, string, string, error) {
	modelKey := strings.TrimPrefix(request.UpstreamModel, "qoder/")
	if modelKey == "" {
		modelKey = "auto"
	}
	modelConfig, err := qoderpkg.GetModelConfig(ctx, client, creds, modelKey)
	if err != nil {
		return nil, "", "", err
	}
	body := buildOpenAIBody(request)
	messages, systemText := normalizeQoderMessages(body["messages"])
	tools, _ := body["tools"].([]any)
	maxTokens := 32768
	if v := numberValue(modelConfig["max_output_tokens"]); v > 0 {
		maxTokens = v
	}
	if request.MaxTokens > 0 && request.MaxTokens < maxTokens {
		maxTokens = request.MaxTokens
	}
	lastUser := lastQoderUserText(messages)
	sessionID := stableQoderHash("qoder-session", creds.UserID, modelKey)
	recordID := stableQoderChatRecord(modelKey, messages, tools, maxTokens)
	isReasoning := boolValue(modelConfig["is_reasoning"])
	payload := map[string]any{
		"request_id":      uuid.NewString(),
		"request_set_id":  recordID,
		"chat_record_id":  recordID,
		"session_id":      sessionID,
		"stream":          true,
		"chat_task":       "FREE_INPUT",
		"is_reply":        true,
		"is_retry":        false,
		"source":          1,
		"version":         "3",
		"session_type":    "qodercli",
		"agent_id":        "agent_common",
		"task_id":         "common",
		"code_language":   "",
		"chat_prompt":     "",
		"image_urls":      nil,
		"aliyun_user_type": "",
		"system":          systemText,
		"messages":        messages,
		"tools":           tools,
		"parameters":      map[string]any{"max_tokens": maxTokens},
		"chat_context": map[string]any{
			"chatPrompt": "",
			"imageUrls":  nil,
			"extra": map[string]any{
				"context": []any{},
				"modelConfig": map[string]any{
					"key":          modelKey,
					"is_reasoning": isReasoning,
				},
				"originalContent": lastUser,
			},
			"features": []any{},
			"text":     lastUser,
		},
		"model_config": modelConfig,
		"business": map[string]any{
			"product":  "cli",
			"version":  "1.0.0",
			"type":     "agent",
			"stage":    "start",
			"id":       uuid.NewString(),
			"name":     truncateQoder(lastUser, 30),
			"begin_at": time.Now().UnixMilli(),
		},
	}
	modelSource := stringValue(firstValue(modelConfig, "source"))
	if modelSource == "" {
		modelSource = "system"
	}
	return payload, modelKey, modelSource, nil
}

func normalizeQoderMessages(raw any) ([]map[string]any, string) {
	items, _ := raw.([]any)
	systemParts := []string{}
	out := []map[string]any{}
	for _, item := range items {
		msg, _ := item.(map[string]any)
		if msg == nil {
			continue
		}
		text := extractQoderText(msg["content"])
		if stringValue(msg["role"]) == "system" {
			if text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		cloned := map[string]any{}
		for k, v := range msg {
			cloned[k] = v
		}
		cloned["content"] = text
		out = append(out, cloned)
	}
	return out, strings.Join(systemParts, "\n\n")
}

func extractQoderText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := []string{}
		for _, raw := range v {
			block, _ := raw.(map[string]any)
			if block == nil {
				continue
			}
			if text := stringValue(firstValue(block, "text")); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func lastQoderUserText(messages []map[string]any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i]["role"] == "user" {
			if text, ok := messages[i]["content"].(string); ok {
				return text
			}
		}
	}
	return ""
}

func stableQoderChatRecord(model string, messages []map[string]any, tools []any, maxTokens int) string {
	h := sha256.New()
	h.Write([]byte("qoder-record\x00"))
	h.Write([]byte(model))
	for _, msg := range messages {
		h.Write([]byte{0})
		h.Write([]byte(stringValue(msg["role"])))
		if text, ok := msg["content"].(string); ok && text != "" {
			h.Write([]byte{0})
			h.Write([]byte(text))
		}
	}
	if len(tools) > 0 {
		b, _ := json.Marshal(tools)
		h.Write([]byte{0})
		h.Write(b)
	}
	h.Write([]byte(fmt.Sprintf("\x00mt=%d", maxTokens)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func truncateQoder(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func parseQoderSSE(ctx context.Context, response *http.Response, model string, out chan<- canonical.Event) {
	out <- canonical.Event{Type: canonical.EventMessageStart, Model: model}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	finishReason := ""
	done := false
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		inner, status, errText := unwrapQoderEnvelope(data)
		if status != 200 {
			out <- canonical.Event{Type: canonical.EventTextDelta, Text: "\n[qoder error " + fmt.Sprint(status) + ": " + errText + "]"}
			out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "stop"}
			return
		}
		if inner == "" {
			continue
		}
		if inner == "[DONE]" {
			done = true
			break
		}
		emitOpenAIChunk(out, inner, &finishReason)
	}
	if err := scanner.Err(); err != nil {
		out <- canonical.Event{Type: canonical.EventError, Err: err}
		return
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	if !done || finishReason != "" {
		out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: finishReason}
	}
}

func unwrapQoderEnvelope(data string) (inner string, status int, errText string) {
	var envelope map[string]any
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		return "", 200, ""
	}
	status = int(numberValue(envelope["statusCodeValue"]))
	if status == 0 {
		status = 200
	}
	inner = stringValue(envelope["body"])
	if status != 200 {
		return inner, status, inner
	}
	return strings.ReplaceAll(strings.ReplaceAll(inner, "\r", ""), "\n", ""), status, ""
}

func emitOpenAIChunk(out chan<- canonical.Event, data string, finishReason *string) {
	var raw map[string]any
	if json.Unmarshal([]byte(data), &raw) != nil {
		return
	}
	if usage, ok := raw["usage"].(map[string]any); ok {
		u := parseOpenAIUsage(usage)
		out <- canonical.Event{Type: canonical.EventUsage, Usage: &u}
	}
	choices, _ := raw["choices"].([]any)
	if len(choices) == 0 {
		return
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	if text := stringValue(delta["content"]); text != "" {
		out <- canonical.Event{Type: canonical.EventTextDelta, Text: text}
	}
	if text := stringValue(firstValue(delta, "reasoning_content", "reasoning")); text != "" {
		out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: text}
	}
	for _, call := range mapSlice(delta["tool_calls"]) {
		out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: call}
	}
	if finish := stringValue(choice["finish_reason"]); finish != "" {
		*finishReason = finish
	}
}

var _ Adapter = (*qoderAdapter)(nil)
