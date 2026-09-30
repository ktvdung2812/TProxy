package providers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/canonical"
	devinpkg "github.com/tproxy/tproxy/internal/providers/devin"
	"github.com/tproxy/tproxy/internal/store"
)

type devinAdapter struct{ client *http.Client }

const devinSessionTokenPrefix = "devin-session-token$"

func (a *devinAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
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
	result.Content = text.String()
	result.Reasoning = thinking.String()
	if len(toolCalls) > 0 {
		result.ToolCalls = orderedToolCalls(toolCalls)
	}
	return result, nil
}

func (a *devinAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	apiKey, err := devinAPIKey(credential)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = devinpkg.BaseURL
	}
	chatRequest := buildDevinChatRequest(request, apiKey)
	body := devinpkg.WrapConnectFrame(devinpkg.BuildChatRequest(chatRequest.request, chatRequest.messageIDs), false)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+devinpkg.ChatPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range provider.Headers {
		req.Header.Set(key, value)
	}
	// Codeium's Connect-RPC upstream expects the session token doubled as
	// Basic auth, Sentry tracing on the chat stream, and no User-Agent —
	// matching native devin-cli wire behavior.
	req.Header.Set("Authorization", "Basic "+apiKey+"-"+apiKey)
	req.Header.Set("Content-Type", "application/connect+proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Sentry-Trace", devinSentryTrace())
	req.Header["User-Agent"] = []string{""}

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
		streamDevinBody(response.Body, out)
	}()
	return out, nil
}

func devinAPIKey(credential store.Credential) (string, error) {
	apiKey := credential.Secret
	if apiKey == "" && credential.OAuthToken != nil {
		apiKey = credential.OAuthToken.AccessToken
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", &ProviderError{Status: http.StatusUnauthorized, Code: "authorization_required", Message: "devin credential is missing the API key (windsurf_api_key from Devin CLI)"}
	}
	if !strings.HasPrefix(apiKey, devinSessionTokenPrefix) {
		apiKey = devinSessionTokenPrefix + apiKey
	}
	return apiKey, nil
}

// devinSentryTrace returns a "<32-hex>-<16-hex>-1" distributed-trace header
// like native devin-cli attaches to chat streams.
func devinSentryTrace() string {
	trace := strings.ReplaceAll(uuid.NewString(), "-", "")
	span := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	return trace + "-" + span + "-1"
}

type devinChatBuild struct {
	request    devinpkg.ChatRequest
	messageIDs []string
}

func buildDevinChatRequest(request canonical.Request, apiKey string) devinChatBuild {
	var systemParts []string
	if system := messageContentString(request.System); system != "" {
		systemParts = append(systemParts, system)
	}
	messages := make([]devinpkg.ChatMessage, 0, len(request.Messages))
	messageIDs := make([]string, 0, len(request.Messages))
	// pendingToolCalls tracks assistant tool-call IDs awaiting results so
	// orphaned tool results can be downgraded to user prompts instead of
	// sending a dangling tool_call_id the upstream rejects.
	var pendingToolCalls []string
	matchPending := func(id string) (string, bool) {
		idx := -1
		if id != "" {
			for i, pending := range pendingToolCalls {
				if pending == id {
					idx = i
					break
				}
			}
		} else if len(pendingToolCalls) > 0 {
			idx = 0
		}
		if idx < 0 {
			return "", false
		}
		matched := pendingToolCalls[idx]
		pendingToolCalls = append(pendingToolCalls[:idx], pendingToolCalls[idx+1:]...)
		if id == "" {
			id = matched
		}
		return id, true
	}
	for _, msg := range request.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "system" || role == "developer" {
			if text := messageContentString(msg.Content); text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		message := devinpkg.ChatMessage{Role: role, ToolCallID: msg.ToolCallID}
		message.Text, message.Images = devinContentParts(msg.Content)
		for _, raw := range msg.ToolCalls {
			fn, _ := raw["function"].(map[string]any)
			call := devinpkg.ToolCall{ID: stringValue(raw["id"]), Name: stringValue(fn["name"])}
			switch args := fn["arguments"].(type) {
			case string:
				call.ArgumentsJSON = args
			default:
				if encoded, err := json.Marshal(args); err == nil {
					call.ArgumentsJSON = string(encoded)
				}
			}
			if call.ID == "" {
				call.ID = "call_" + uuid.NewString()
			}
			message.ToolCalls = append(message.ToolCalls, call)
			pendingToolCalls = append(pendingToolCalls, call.ID)
		}
		if role == "tool" {
			if matchedID, ok := matchPending(msg.ToolCallID); ok {
				message.ToolCallID = matchedID
			} else {
				// No matching assistant tool call: send the result as a user
				// turn rather than a dangling source=4 prompt.
				message.Role = "user"
				message.ToolCallID = ""
			}
		}
		messages = append(messages, message)
		messageIDs = append(messageIDs, uuid.NewString())
	}
	tools := make([]devinpkg.Tool, 0, len(request.Tools))
	for _, raw := range request.Tools {
		tool := devinpkg.Tool{}
		if fn, ok := raw["function"].(map[string]any); ok {
			tool.Name = stringValue(fn["name"])
			tool.Description = stringValue(fn["description"])
			if parameters, ok := fn["parameters"]; ok {
				if encoded, err := json.Marshal(parameters); err == nil {
					tool.JSONSchema = string(encoded)
				}
			}
		} else {
			tool.Name = stringValue(raw["name"])
			tool.Description = stringValue(firstValue(raw, "description", "desc"))
			if schema, ok := firstValue(raw, "input_schema", "parameters").(map[string]any); ok {
				if encoded, err := json.Marshal(schema); err == nil {
					tool.JSONSchema = string(encoded)
				}
			}
		}
		if tool.Name != "" {
			tools = append(tools, tool)
		}
	}
	sessionID := normalizeDevinSessionID(request.SessionID)
	cascadeID := sessionID
	var topP *float64
	if value, ok := numberValueAny(request.Raw["top_p"]); ok {
		topP = &value
	}
	effort := stringValue(request.Reasoning["effort"])
	return devinChatBuild{
		request: devinpkg.ChatRequest{
			APIKey:       apiKey,
			SystemPrompt: strings.Join(systemParts, "\n\n"),
			Messages:     messages,
			ModelUID:     devinpkg.ResolveChatModelUID(request.UpstreamModel, effort),
			SessionID:    sessionID,
			CascadeID:    cascadeID,
			MaxTokens:    request.MaxTokens,
			Temperature:  request.Temperature,
			TopP:         topP,
			Tools:        tools,
		},
		messageIDs: messageIDs,
	}
}

// normalizeDevinSessionID keeps session/cascade IDs UUID-shaped (the upstream
// uses them as prompt-cache keys); arbitrary client session strings map to a
// deterministic UUIDv5.
func normalizeDevinSessionID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.NewString()
	}
	if _, err := uuid.Parse(raw); err == nil {
		return raw
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(raw)).String()
}

func devinContentParts(content any) (string, []devinpkg.Image) {
	switch typed := content.(type) {
	case string:
		return typed, nil
	case []any:
		var text strings.Builder
		var images []devinpkg.Image
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch stringValue(block["type"]) {
			case "text":
				text.WriteString(stringValue(block["text"]))
			case "image_url":
				if image := devinImageFromURL(block["image_url"]); image != nil {
					images = append(images, *image)
				}
			case "image":
				if image := devinImageFromURL(block); image != nil {
					images = append(images, *image)
				}
			}
		}
		return text.String(), images
	default:
		return messageContentString(content), nil
	}
}

func devinImageFromURL(value any) *devinpkg.Image {
	url := ""
	switch typed := value.(type) {
	case map[string]any:
		url = stringValue(typed["url"])
		if url == "" {
			url = stringValue(typed["image_url"])
		}
		if url == "" {
			if data := stringValue(typed["base64_data"]); data != "" {
				return &devinpkg.Image{Base64Data: data, MimeType: stringValue(firstValue(typed, "mime_type", "mimeType"))}
			}
		}
	case string:
		url = typed
	}
	const marker = ";base64,"
	if !strings.HasPrefix(url, "data:") {
		return nil
	}
	idx := strings.Index(url, marker)
	if idx < 0 {
		return nil
	}
	return &devinpkg.Image{Base64Data: url[idx+len(marker):], MimeType: strings.TrimPrefix(url[len("data:"):idx], "")}
}

func numberValueAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := stringValue(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return typed
	default:
		return nil
	}
}

func streamDevinBody(body io.Reader, out chan<- canonical.Event) {
	emittedRole := false
	sawToolCalls := false
	toolCallsByID := map[string]int{}
	toolIndex := 0
	lastStopReason := 0
	var usage *devinpkg.Usage
	header := make([]byte, 5)

	emit := func(event canonical.Event) { out <- event }

	for {
		if _, err := io.ReadFull(body, header); err != nil {
			break
		}
		flags := header[0]
		length := binary.BigEndian.Uint32(header[1:5])
		if length > 64<<20 {
			emit(canonical.Event{Type: canonical.EventError, Err: &ProviderError{Code: "upstream_error", Message: "devin stream frame exceeds size limit"}})
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(body, payload); err != nil {
			break
		}
		payload = devinpkg.DecodeConnectPayload(payload, flags)
		if devinpkg.IsEndStream(flags) {
			if message := devinpkg.TrailerError(payload); message != "" {
				emit(canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: http.StatusBadRequest, Code: "upstream_error", Message: message}})
				return
			}
			break
		}
		delta, err := devinpkg.ParseChatResponse(payload)
		if err != nil || delta == nil {
			continue
		}
		if delta.StopReason != 0 {
			lastStopReason = delta.StopReason
		}
		if delta.Usage != nil {
			usage = delta.Usage
		}
		if delta.Thinking != "" {
			if !emittedRole {
				emittedRole = true
				emit(canonical.Event{Type: canonical.EventMessageStart})
			}
			emit(canonical.Event{Type: canonical.EventReasoningDelta, Text: delta.Thinking})
		}
		if delta.Text != "" {
			if !emittedRole {
				emittedRole = true
				emit(canonical.Event{Type: canonical.EventMessageStart})
			}
			emit(canonical.Event{Type: canonical.EventTextDelta, Text: delta.Text})
		}
		for _, call := range delta.ToolCalls {
			callID := call.ID
			if callID == "" {
				continue
			}
			sawToolCalls = true
			if !emittedRole {
				emittedRole = true
				emit(canonical.Event{Type: canonical.EventMessageStart})
			}
			idx, exists := toolCallsByID[callID]
			if !exists {
				idx = toolIndex
				toolIndex++
				toolCallsByID[callID] = idx
			}
			emit(canonical.Event{
				Type: canonical.EventToolCallDelta,
				ToolCall: map[string]any{
					"index": idx,
					"id":    callID,
					"type":  "function",
					"function": map[string]any{
						"name":      call.Name,
						"arguments": call.ArgumentsJSON,
					},
				},
			})
		}
	}
	if usage != nil {
		emit(canonical.Event{Type: canonical.EventUsage, Usage: &canonical.Usage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
			CachedTokens: usage.CacheReadTokens,
		}})
	}
	emit(canonical.Event{Type: canonical.EventMessageEnd, FinishReason: devinpkg.FinishReason(lastStopReason, sawToolCalls)})
}

func discoverDevinModels(ctx context.Context, registry *Registry, provider store.Provider, credential store.Credential) ([]DiscoveredModel, error) {
	apiKey, err := devinAPIKey(credential)
	if err != nil {
		if items := staticDiscoveryModels(provider); len(items) > 0 {
			return items, nil
		}
		return nil, err
	}
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = devinpkg.BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+devinpkg.ModelConfigsPath, bytes.NewReader(devinpkg.BuildGetCliModelConfigsRequest(apiKey)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Basic "+apiKey+"-"+apiKey)
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Accept", "*/*")
	req.Header["User-Agent"] = []string{""}
	response, err := registry.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: "health_check_failed", Err: err}
	}
	defer response.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if readErr != nil {
		return nil, &ProviderError{Code: "health_check_failed", Err: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, upstreamResponseError(response, payload)
	}
	entries, err := devinpkg.ParseCliModelConfigsResponse(devinpkg.MaybeGunzip(payload))
	if err != nil {
		return nil, &ProviderError{Code: "model_discovery_failed", Message: "devin returned an undecodable model catalog", Err: err}
	}
	if len(entries) == 0 {
		return nil, &ProviderError{Code: "model_discovery_failed", Message: "devin returned no models"}
	}
	uids := make([]string, 0, len(entries))
	for _, entry := range entries {
		uids = append(uids, entry.UID)
	}
	devinpkg.RegisterModelLevels(uids)
	items := make([]DiscoveredModel, 0, len(entries))
	for _, entry := range entries {
		name := entry.Label
		if name == "" {
			name = entry.UID
		}
		caps := discoveryCapabilities(registry, provider, provider.Type, entry.UID)
		if entry.SupportsImages && !containsString(caps, "vision") {
			caps = append(caps, "vision")
		}
		if entry.SupportsThinking && !containsString(caps, "reasoning") {
			caps = append(caps, "reasoning")
		}
		sort.Strings(caps)
		items = append(items, DiscoveredModel{ID: entry.UID, Name: name, OwnedBy: devinpkg.VendorName(entry.VendorID), Capabilities: caps})
	}
	return items, nil
}

// devinQuota probes SeatManagementService/GetUserStatus for the account's
// plan and daily/weekly quota windows (returned as remaining-percent values).
func (r *Registry) devinQuota(ctx context.Context, provider store.Provider, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
	}
	apiKey, err := devinAPIKey(credential)
	if err != nil {
		result.Message = "Devin credential has no session token"
		return result
	}
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = devinpkg.BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+devinpkg.UserStatusPath, bytes.NewReader(devinpkg.BuildGetUserStatusRequest(apiKey)))
	if err != nil {
		result.Message = err.Error()
		return result
	}
	req.Header.Set("Authorization", "Basic "+apiKey+"-"+apiKey)
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Accept", "*/*")
	req.Header["User-Agent"] = []string{""}
	response, err := r.client.Do(req)
	if err != nil {
		result.Message = truncatePingError(err.Error())
		return result
	}
	defer response.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		result.Message = readErr.Error()
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Message = fmt.Sprintf("Devin user status unavailable (HTTP %d)", response.StatusCode)
		return result
	}
	status, err := devinpkg.ParseGetUserStatusResponse(devinpkg.MaybeGunzip(payload))
	if err != nil {
		result.Message = "unable to parse Devin user status"
		return result
	}
	result.Plan = status.Plan
	if status.PlanEnd > 0 {
		result.RenewsAt = time.Unix(status.PlanEnd, 0).UTC().Format(time.RFC3339)
	}
	// A missing plan_status block leaves every quota field zeroed; only a
	// decoded block (plan name, reset times, or nonzero remaining) produces
	// real windows, so an empty response can't read as a depleted account.
	hasPlanStatus := status.Plan != "" || status.DailyQuotaResetAt > 0 || status.WeeklyQuotaResetAt > 0 ||
		status.DailyQuotaRemainingPercent > 0 || status.WeeklyQuotaRemainingPercent > 0
	if hasPlanStatus {
		result.Quotas["daily"] = devinQuotaWindow("daily", status.DailyQuotaRemainingPercent, status.DailyQuotaResetAt)
		result.Quotas["weekly"] = devinQuotaWindow("weekly", status.WeeklyQuotaRemainingPercent, status.WeeklyQuotaResetAt)
	}
	if len(result.Quotas) == 0 {
		result.Message = "Devin connected. No quota data returned."
	}
	return result
}

func devinQuotaWindow(name string, remainingPercent int, resetUnix int64) QuotaEntry {
	remaining := float64(remainingPercent)
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	entry := QuotaEntry{Name: name, Used: 100 - remaining, Total: 100, Remaining: remaining}
	if resetUnix > 0 {
		entry.ResetAt = time.Unix(resetUnix, 0).UTC().Format(time.RFC3339)
	}
	return entry
}

func devinStaticModelEntries(provider store.Provider) []DiscoveredModel {
	registry := NewRegistry()
	items := make([]DiscoveredModel, 0, len(devinpkg.StaticModels))
	for _, model := range devinpkg.StaticModels {
		items = append(items, DiscoveredModel{
			ID:           model.UID,
			Name:         model.Label,
			OwnedBy:      "devin",
			Capabilities: discoveryCapabilities(registry, provider, provider.Type, model.UID),
		})
	}
	return items
}
