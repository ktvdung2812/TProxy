package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	cursorpkg "github.com/tproxy/tproxy/internal/providers/cursor"
	"github.com/tproxy/tproxy/internal/store"
)

type cursorAdapter struct{ client *http.Client }

func (a *cursorAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	events, err := a.ExecuteStream(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	result := &canonical.Response{Model: request.UpstreamModel, Role: "assistant"}
	var text strings.Builder
	var thinking strings.Builder
	toolCalls := map[int]map[string]any{}
	for event := range events {
		switch event.Type {
		case canonical.EventTextDelta:
			text.WriteString(event.Text)
		case canonical.EventReasoningDelta:
			thinking.WriteString(event.Text)
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
	content := text.String()
	if content == "" && cursorpkg.IsComposerModel(request.UpstreamModel) {
		content = cursorpkg.VisibleComposerContentFromThinking(thinking.String())
	}
	result.Content = content
	if len(toolCalls) > 0 {
		result.ToolCalls = orderedToolCalls(toolCalls)
	}
	return result, nil
}

func (a *cursorAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	response, err := a.postCursor(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, upstreamError(response)
	}
	out := make(chan canonical.Event, 32)
	go func() {
		defer close(out)
		defer response.Body.Close()
		buffer, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<20))
		if readErr != nil {
			out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Code: "upstream_network", Err: readErr}}
			return
		}
		streamCursorBuffer(buffer, request, out)
	}()
	return out, nil
}

func (a *cursorAdapter) postCursor(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*http.Response, error) {
	accessToken, machineID, err := cursorCredentials(credential)
	if err != nil {
		return nil, err
	}
	body := buildOpenAIBody(request)
	messages, tools, reasoningEffort := cursorMessagesFromBody(body)
	forceAgent := cursorForceAgentMode(request)
	protobufBody := cursorpkg.GenerateCursorBody(messages, request.UpstreamModel, tools, reasoningEffort, forceAgent)

	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = cursorpkg.BaseURL
	}
	target := baseURL + cursorpkg.ChatPath

	ghostMode := credentialExtraString(credential, "ghost_mode", "ghostMode") != "false"
	headers := cursorpkg.BuildCursorHeaders(accessToken, &machineID, ghostMode)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(protobufBody))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Accept", "application/connect+proto")
	return a.client.Do(req)
}

func cursorCredentials(credential store.Credential) (accessToken, machineID string, err error) {
	accessToken = credential.Secret
	if credential.OAuthToken != nil && credential.OAuthToken.AccessToken != "" {
		accessToken = credential.OAuthToken.AccessToken
	}
	machineID = credentialExtraString(credential, "machine_id", "machineId")
	if accessToken == "" {
		return "", "", &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: "cursor credential is missing access token"}
	}
	if machineID == "" {
		return "", "", &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: "cursor credential is missing machine ID; re-import from Cursor IDE"}
	}
	return accessToken, machineID, nil
}

func cursorForceAgentMode(request canonical.Request) bool {
	if request.Metadata == nil {
		return false
	}
	ua := strings.ToLower(fmt.Sprint(request.Metadata["user_agent"]))
	return strings.Contains(ua, "claude-cli") || strings.Contains(ua, "claude-code") || strings.Contains(ua, "claude code")
}

func cursorMessagesFromBody(body map[string]any) ([]cursorpkg.Message, []cursorpkg.OpenAITool, string) {
	rawMessages, _ := body["messages"].([]any)
	messages := make([]cursorpkg.Message, 0, len(rawMessages))
	for _, raw := range rawMessages {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		msg := cursorpkg.Message{
			Role:    stringValue(item["role"]),
			Content: messageContentString(item["content"]),
		}
		if toolCalls, ok := item["tool_calls"].([]any); ok {
			msg.ToolCalls = toolCalls
		}
		if toolResults, ok := item["tool_results"].([]any); ok {
			for _, tr := range toolResults {
				m, ok := tr.(map[string]any)
				if !ok {
					continue
				}
				msg.ToolResults = append(msg.ToolResults, cursorpkg.ToolResult{
					ToolCallID:    stringValue(m["tool_call_id"]),
					ToolName:      stringValue(firstValue(m, "tool_name", "name")),
					Name:          stringValue(m["name"]),
					RawArgs:       stringValue(m["raw_args"]),
					ResultContent: stringValue(firstValue(m, "result_content", "result")),
					Result:        stringValue(m["result"]),
					ToolIndex:     int(numberValue(m["tool_index"])),
					Index:         int(numberValue(m["index"])),
				})
			}
		}
		messages = append(messages, msg)
	}

	rawTools, _ := body["tools"].([]any)
	tools := make([]cursorpkg.OpenAITool, 0, len(rawTools))
	for _, raw := range rawTools {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tool := cursorpkg.OpenAITool{Type: stringValue(item["type"])}
		if fn, ok := item["function"].(map[string]any); ok {
			tool.Function = &struct {
				Name        string         `json:"name,omitempty"`
				Description string         `json:"description,omitempty"`
				Parameters  map[string]any `json:"parameters,omitempty"`
			}{
				Name:        stringValue(fn["name"]),
				Description: stringValue(fn["description"]),
				Parameters:  mapValue(fn["parameters"]),
			}
		}
		tools = append(tools, tool)
	}

	reasoningEffort := ""
	if v, ok := body["reasoning_effort"].(string); ok {
		reasoningEffort = v
	}
	return messages, tools, reasoningEffort
}

func streamCursorBuffer(buffer []byte, request canonical.Request, out chan<- canonical.Event) {
	model := request.UpstreamModel
	offset := 0
	emittedRole := false
	totalContent := ""
	totalThinking := ""
	emittedComposerThinking := 0
	toolCalls := map[int]map[string]any{}
	toolCallsByID := map[string]int{}
	toolIndex := 0

	for offset < len(buffer) {
		frame := cursorpkg.ParseConnectRPCFrame(buffer[offset:])
		if frame == nil {
			break
		}
		offset += frame.Consumed
		payload := cursorpkg.DecompressPayload(frame.Payload, frame.Flags)

		if len(payload) > 0 && payload[0] == '{' {
			if text := string(payload); strings.Contains(text, `"error"`) {
				if totalContent != "" || len(toolCalls) > 0 {
					break
				}
				out <- canonical.Event{Type: canonical.EventError, Err: cursorJSONError(text)}
				return
			}
		}

		result := cursorpkg.ExtractTextFromResponse(payload)
		if result.Error != nil {
			if totalContent != "" || len(toolCalls) > 0 {
				break
			}
			msg := *result.Error
			out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: msg}}
			return
		}

		if result.ToolCall != nil {
			tc := result.ToolCall
			if !emittedRole {
				emittedRole = true
				out <- canonical.Event{Type: canonical.EventMessageStart}
			}
			idx, exists := toolCallsByID[tc.ID]
			if !exists {
				idx = toolIndex
				toolIndex++
				toolCallsByID[tc.ID] = idx
				toolCalls[idx] = map[string]any{
					"id":   tc.ID,
					"type": tc.Type,
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				}
			} else {
				fn, _ := toolCalls[idx]["function"].(map[string]any)
				if fn != nil {
					fn["arguments"] = stringValue(fn["arguments"]) + tc.Function.Arguments
				}
			}
			out <- canonical.Event{
				Type: canonical.EventToolCallDelta,
				ToolCall: map[string]any{
					"index": idx,
					"id":    tc.ID,
					"type":  tc.Type,
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				},
			}
		}

		if result.Text != nil && *result.Text != "" {
			if !emittedRole {
				emittedRole = true
				out <- canonical.Event{Type: canonical.EventMessageStart}
			}
			totalContent += *result.Text
			out <- canonical.Event{Type: canonical.EventTextDelta, Text: *result.Text}
		}

		if cursorpkg.IsComposerModel(model) && result.Thinking != nil && *result.Thinking != "" {
			totalThinking += *result.Thinking
			visible := cursorpkg.VisibleComposerContentFromThinking(totalThinking)
			if len(visible) > emittedComposerThinking {
				delta := visible[emittedComposerThinking:]
				emittedComposerThinking = len(visible)
				if !emittedRole {
					emittedRole = true
					out <- canonical.Event{Type: canonical.EventMessageStart}
				}
				totalContent += delta
				out <- canonical.Event{Type: canonical.EventTextDelta, Text: delta}
			}
		}
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	out <- canonical.Event{
		Type: canonical.EventUsage,
		Usage: &canonical.Usage{
			InputTokens:  estimateCursorTokens(request),
			OutputTokens: max(1, len(totalContent)/4),
		},
	}
	out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: finishReason}
}

func cursorJSONError(text string) error {
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return &ProviderError{Status: http.StatusBadRequest, Code: "upstream_error", Message: text}
	}
	errObj, _ := payload["error"].(map[string]any)
	msg := stringValue(errObj["message"])
	if msg == "" {
		msg = text
	}
	status := http.StatusBadRequest
	if stringValue(errObj["code"]) == "resource_exhausted" {
		status = http.StatusTooManyRequests
	}
	return &ProviderError{Status: status, Code: "upstream_error", Message: msg}
}

func estimateCursorTokens(request canonical.Request) int {
	chars := 0
	for _, msg := range request.Messages {
		chars += len(messageContentString(msg.Content))
	}
	if chars == 0 {
		return 1
	}
	return max(1, chars/4)
}

func messageContentString(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text := stringValue(block["text"]); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		if content == nil {
			return ""
		}
		return fmt.Sprint(content)
	}
}

func mapValue(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func discoverCursorModels(ctx context.Context, registry *Registry, provider store.Provider, credential store.Credential) ([]DiscoveredModel, error) {
	accessToken, machineID, err := cursorCredentials(credential)
	if err != nil {
		if items := staticDiscoveryModels(provider); len(items) > 0 {
			return items, nil
		}
		return nil, err
	}
	ghostMode := credentialExtraString(credential, "ghost_mode", "ghostMode") != "false"
	entries, discoverErr := cursorpkg.DiscoverModels(ctx, registry.client, provider.BaseURL, cursorpkg.CatalogCredentials{
		AccessToken: accessToken,
		MachineID:   machineID,
		GhostMode:   ghostMode,
	})
	if discoverErr != nil {
		if items := staticDiscoveryModels(provider); len(items) > 0 {
			return nil, &ProviderError{Code: "model_discovery_failed", Message: discoverErr.Error(), Err: discoverErr}
		}
		return nil, &ProviderError{Code: "model_discovery_failed", Message: discoverErr.Error(), Err: discoverErr}
	}
	if len(entries) == 0 {
		return nil, &ProviderError{Code: "model_discovery_failed", Message: "cursor returned no models"}
	}
	items := make([]DiscoveredModel, 0, len(entries))
	for _, entry := range entries {
		items = append(items, DiscoveredModel{
			ID:           entry.ID,
			Name:         entry.Name,
			OwnedBy:      "cursor",
			Capabilities: cursorDiscoveryCapabilities(registry, provider, entry),
		})
	}
	return items, nil
}

func cursorDiscoveryCapabilities(registry *Registry, provider store.Provider, entry cursorpkg.DiscoveredModelEntry) []string {
	caps := discoveryCapabilities(registry, provider, provider.Type, entry.ID)
	seen := make(map[string]struct{}, len(caps))
	for _, cap := range caps {
		seen[cap] = struct{}{}
	}
	if entry.SupportsImages {
		seen["vision"] = struct{}{}
	}
	if entry.SupportsReasoning {
		seen["reasoning"] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for cap := range seen {
		out = append(out, cap)
	}
	sort.Strings(out)
	return out
}
