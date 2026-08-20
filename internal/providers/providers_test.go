package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestCodexAdapterTranslatesResponsesAndParsesSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer codex-access" || r.Header.Get("Originator") != "codex_cli_rs" || r.Header.Get("ChatGPT-Account-ID") != "acct-123" {
			t.Fatalf("headers = %+v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "gpt-5.4" || body["stream"] != true || body["store"] != false {
			t.Fatalf("body = %+v", body)
		}
		if _, ok := body["max_output_tokens"]; ok {
			t.Fatalf("codex body must not include max_output_tokens: %+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"response.created","response":{"id":"resp-1","model":"gpt-5.4"}}`,
			`{"type":"response.output_text.delta","delta":"hello"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":4,"output_tokens":2}}}`,
			`[DONE]`,
		} {
			_, _ = w.Write([]byte("data: " + event + "\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("codex")
	if err != nil {
		t.Fatal(err)
	}
	credential := store.Credential{ID: "codex-account", AuthType: "oauth", Secret: "codex-access", TokenType: "Bearer", OAuthToken: &store.OAuthToken{AccessToken: "codex-access", Extra: map[string]any{"account_id": "acct-123"}}}
	response, err := adapter.Execute(context.Background(), store.Provider{ID: "codex", Type: "codex", BaseURL: upstream.URL}, credential, canonical.Request{Source: canonical.ProtocolOpenAI, UpstreamModel: "gpt-5.4", Raw: map[string]any{"model": "public", "input": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != "resp-1" || response.Model != "gpt-5.4" || response.Content != "hello" || response.Usage.InputTokens != 4 || response.Usage.OutputTokens != 2 {
		t.Fatalf("response = %+v", response)
	}
}

func TestOpenAIStreamRetainsUsageOnlyChunkAfterFinish(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"stream-usage\",\"model\":\"upstream\",\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":4,\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("openai-compatible")
	if err != nil {
		t.Fatal(err)
	}
	events, err := adapter.ExecuteStream(context.Background(), store.Provider{ID: "openai", Type: "openai-compatible", BaseURL: upstream.URL}, store.Credential{AuthType: "none"}, canonical.Request{RequestID: "openai-stream-usage", UpstreamModel: "upstream"})
	if err != nil {
		t.Fatal(err)
	}
	var usage canonical.Usage
	usageIndex, endIndex := -1, -1
	index := 0
	for event := range events {
		if event.Type == canonical.EventUsage && event.Usage != nil {
			usage = *event.Usage
			usageIndex = index
		}
		if event.Type == canonical.EventMessageEnd {
			endIndex = index
		}
		index++
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 4 || usage.ReasoningTokens != 2 {
		t.Fatalf("usage = %+v", usage)
	}
	if usageIndex < 0 || endIndex < 0 || usageIndex >= endIndex {
		t.Fatalf("usage must precede terminal event: usage_index=%d end_index=%d", usageIndex, endIndex)
	}
}

func TestGeminiStreamEmitsUsageBeforeFinalMessageEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-stream:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Fatalf("request URL = %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":13,\"candidatesTokenCount\":5,\"thoughtsTokenCount\":1}}\n\n"))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("gemini")
	if err != nil {
		t.Fatal(err)
	}
	events, err := adapter.ExecuteStream(context.Background(), store.Provider{ID: "gemini", Type: "gemini", BaseURL: upstream.URL}, store.Credential{AuthType: "none"}, canonical.Request{RequestID: "gemini-stream-usage", UpstreamModel: "gemini-stream"})
	if err != nil {
		t.Fatal(err)
	}
	var usage canonical.Usage
	usageIndex, endIndex := -1, -1
	index := 0
	for event := range events {
		if event.Type == canonical.EventUsage && event.Usage != nil {
			usage = *event.Usage
			usageIndex = index
		}
		if event.Type == canonical.EventMessageEnd {
			endIndex = index
		}
		index++
	}
	if usage.InputTokens != 13 || usage.OutputTokens != 5 || usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %+v", usage)
	}
	if usageIndex < 0 || endIndex < 0 || usageIndex >= endIndex {
		t.Fatalf("usage must precede terminal event: usage_index=%d end_index=%d", usageIndex, endIndex)
	}
}

func TestAnthropicStreamMergesMessageStartAndDeltaUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-usage\",\"model\":\"claude-stream\",\"usage\":{\"input_tokens\":17,\"cache_read_input_tokens\":6}}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":8}}\n\n"))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("claude")
	if err != nil {
		t.Fatal(err)
	}
	events, err := adapter.ExecuteStream(context.Background(), store.Provider{ID: "claude", Type: "claude", BaseURL: upstream.URL}, store.Credential{AuthType: "none"}, canonical.Request{RequestID: "claude-stream-usage", UpstreamModel: "claude-stream", Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	var usage canonical.Usage
	usageEvents, endIndex := 0, -1
	index := 0
	for event := range events {
		if event.Type == canonical.EventUsage && event.Usage != nil {
			usage = *event.Usage
			usageEvents++
		}
		if event.Type == canonical.EventMessageEnd {
			endIndex = index
		}
		index++
	}
	if usage.InputTokens != 17 || usage.OutputTokens != 8 || usage.CachedTokens != 6 {
		t.Fatalf("usage = %+v", usage)
	}
	if usageEvents < 2 || endIndex < 0 {
		t.Fatalf("expected start and delta usage before terminal: usage_events=%d end_index=%d", usageEvents, endIndex)
	}
}

func TestClaudeOAuthAdapterUsesBearerAndClaudeHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.URL.Query().Get("beta") != "true" {
			t.Fatalf("URL = %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer claude-access" || r.Header.Get("x-api-key") != "" || !strings.Contains(r.Header.Get("Anthropic-Beta"), "oauth-2025-04-20") || r.Header.Get("X-App") != "cli" {
			t.Fatalf("headers = %+v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-1","type":"message","role":"assistant","model":"claude-sonnet","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("claude")
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Execute(context.Background(), store.Provider{ID: "claude", Type: "claude", BaseURL: upstream.URL}, store.Credential{ID: "claude-account", AuthType: "oauth", Secret: "claude-access", TokenType: "Bearer"}, canonical.Request{UpstreamModel: "claude-sonnet", Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "hello" || response.Model != "claude-sonnet" || response.Usage.InputTokens != 3 {
		t.Fatalf("response = %+v", response)
	}
}

func TestAntigravityAdapterWrapsCloudCodeRequestAndNormalizesResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer antigravity-access" || !strings.HasPrefix(r.Header.Get("User-Agent"), "antigravity/ide/") {
			t.Fatalf("headers = %+v", r.Header)
		}
		// No Antigravity client sends a correlation header, so sending one only
		// marks the request as coming from something other than the IDE. The
		// envelope carries its own requestId, which is where Cloud Code looks.
		if got := r.Header.Get("X-Request-ID"); got != "" {
			t.Fatalf("X-Request-ID = %q, want it absent from the Antigravity path", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		request, _ := body["request"].(map[string]any)
		if body["model"] != "gemini-3-pro" || body["project"] != "cloud-project-123" || body["requestType"] != "agent" || request["sessionId"] != "session-1" {
			t.Fatalf("body = %+v", body)
		}
		if len(mapSlice(request["tools"])) != 1 {
			t.Fatalf("tools = %+v", request["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1internal:generateContent":
			_, _ = w.Write([]byte(`{"response":{"modelVersion":"gemini-3-pro","candidates":[{"content":{"role":"model","parts":[{"text":"thinking","thought":true},{"text":"hello"},{"functionCall":{"id":"call-1","name":"lookup","args":{"q":"x"}}}]},"finishReason":"STOP"}],"cpaUsageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":1}}}`))
		case "/v1internal:streamGenerateContent":
			if r.URL.Query().Get("alt") != "sse" {
				t.Fatalf("stream URL = %s", r.URL.String())
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"stream-thought\",\"thought\":true},{\"text\":\"stream-text\"}]}}]}}\n\n"))
			_, _ = w.Write([]byte("data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1}}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.Provider{ID: "antigravity", Type: "antigravity", BaseURL: upstream.URL + "/v1internal"}
	credential := store.Credential{ID: "antigravity-account", AuthType: "oauth", Secret: "antigravity-access", TokenType: "Bearer", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "cloud-project-123"}}}
	request := canonical.Request{
		RequestID: "req-1", SessionID: "session-1", UpstreamModel: "gemini-3-pro",
		Messages:   []canonical.Message{{Role: "user", Content: "hello"}},
		Tools:      []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup", "description": "Lookup", "parameters": map[string]any{"type": "object"}}}},
		ToolChoice: "auto",
	}
	response, err := adapter.Execute(context.Background(), provider, credential, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "gemini-3-pro" || response.Content != "hello" || response.Reasoning != "thinking" || len(response.ToolCalls) != 1 || response.Usage.InputTokens != 5 || response.Usage.ReasoningTokens != 1 {
		t.Fatalf("response = %+v", response)
	}
	events, err := adapter.ExecuteStream(context.Background(), provider, credential, request)
	if err != nil {
		t.Fatal(err)
	}
	var text, reasoning string
	var usage canonical.Usage
	for event := range events {
		switch event.Type {
		case canonical.EventTextDelta:
			text += event.Text
		case canonical.EventReasoningDelta:
			reasoning += event.Reasoning
		case canonical.EventUsage:
			usage = *event.Usage
		}
	}
	if text != "stream-text" || reasoning != "stream-thought" || usage.InputTokens != 2 || usage.OutputTokens != 1 {
		t.Fatalf("stream text=%q reasoning=%q usage=%+v", text, reasoning, usage)
	}
}

func TestAntigravityAdapterAcceptsObjectProjectMetadata(t *testing.T) {
	credential := store.Credential{
		AuthType: "oauth",
		OAuthToken: &store.OAuthToken{Extra: map[string]any{
			"cloudaicompanionProject": map[string]any{"id": "cloud-project-object"},
		}},
	}
	body, err := antigravityBody(canonical.Request{RequestID: "request", UpstreamModel: "gemini-3-pro"}, credential)
	if err != nil {
		t.Fatal(err)
	}
	if got := body["project"]; got != "cloud-project-object" {
		t.Fatalf("project=%#v, want cloud-project-object", got)
	}
}

func TestAntigravityAdapterAcceptsSnakeCaseCompanionProjectMetadata(t *testing.T) {
	credential := store.Credential{
		AuthType: "oauth",
		OAuthToken: &store.OAuthToken{Extra: map[string]any{
			"cloudaicompanion_project": map[string]any{"id": "cloud-project-snake"},
		}},
	}
	body, err := antigravityBody(canonical.Request{RequestID: "request", UpstreamModel: "gemini-3-pro"}, credential)
	if err != nil {
		t.Fatal(err)
	}
	if got := body["project"]; got != "cloud-project-snake" {
		t.Fatalf("project=%#v, want cloud-project-snake", got)
	}
}

func TestAntigravityAdapterNormalizesCloudCodeRequestShape(t *testing.T) {
	originalName := "9 invalid/tool name with spaces and an intentionally very long suffix"
	longName := strings.Repeat("very-long-tool-name-", 5)
	credential := store.Credential{AuthType: "oauth", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "cloud-project"}}}
	request := canonical.Request{
		RequestID:     "caller controlled / request id",
		UpstreamModel: "gemini-3-pro",
		MaxTokens:     90_000,
		Messages: []canonical.Message{
			{Role: "assistant", ToolCalls: []map[string]any{{"id": "call-1", "type": "function", "function": map[string]any{"name": originalName, "arguments": `{"value":"x"}`}}}},
			{Role: "tool", ToolCallID: "call-1", Name: originalName, Content: "done"},
		},
		Tools: []map[string]any{
			{"type": "function", "function": map[string]any{"name": originalName, "parameters": map[string]any{
				"type": []any{"object", "null"}, "properties": map[string]any{"value": map[string]any{"$ref": "#/$defs/Value", "pattern": ".*"}}, "required": []any{"value", "missing"}, "$defs": map[string]any{"Value": map[string]any{"type": "string"}},
			}}},
			{"type": "function", "function": map[string]any{"name": longName, "parameters": map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "object", "properties": map[string]any{"nested": map[string]any{"type": "string"}}}}}}},
		},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": originalName}},
	}

	body, reverse, err := antigravityPreparedBody(request, credential)
	if err != nil {
		t.Fatal(err)
	}
	if requestID := stringValue(body["requestId"]); !regexp.MustCompile(`^agent/[0-9a-f-]{36}/\d+/[0-9a-f-]{36}/\d+$`).MatchString(requestID) {
		t.Fatalf("requestId=%q", requestID)
	}
	inner, _ := body["request"].(map[string]any)
	generation, _ := inner["generationConfig"].(map[string]any)
	if got := numberValue(generation["maxOutputTokens"]); got != antigravityMaxOutputTokens {
		t.Fatalf("maxOutputTokens=%d, want %d", got, antigravityMaxOutputTokens)
	}
	groups := antigravityMapSlice(inner["tools"])
	if len(groups) != 1 {
		t.Fatalf("tools=%#v", inner["tools"])
	}
	declarations := antigravityMapSlice(groups[0]["functionDeclarations"])
	if len(declarations) != 2 {
		t.Fatalf("declarations=%#v", groups[0])
	}
	firstName := stringValue(declarations[0]["name"])
	if !antigravityToolNamePattern.MatchString(firstName) || firstName == originalName || len(firstName) > antigravityToolNameMaxLen {
		t.Fatalf("wire name=%q", firstName)
	}
	if got := reverse[firstName]; got != originalName {
		t.Fatalf("reverse[%q]=%q, want %q", firstName, got, originalName)
	}
	firstSchema, _ := declarations[0]["parameters"].(map[string]any)
	properties, _ := firstSchema["properties"].(map[string]any)
	valueSchema, _ := properties["value"].(map[string]any)
	if firstSchema["$defs"] != nil || valueSchema["$ref"] != nil || valueSchema["pattern"] != nil || len(antigravityStrings(firstSchema["required"])) != 1 {
		t.Fatalf("schema not normalized: %#v", firstSchema)
	}
	contents := antigravityMapSlice(inner["contents"])
	callParts := antigravityMapSlice(contents[0]["parts"])
	var call map[string]any
	for _, part := range callParts {
		if candidate, ok := part["functionCall"].(map[string]any); ok {
			call = candidate
			break
		}
	}
	if call["name"] != firstName {
		t.Fatalf("history function call=%#v want %q", call, firstName)
	}
	responseParts := antigravityMapSlice(contents[1]["parts"])
	functionResponse, _ := responseParts[0]["functionResponse"].(map[string]any)
	if functionResponse["name"] != firstName {
		t.Fatalf("history function response=%#v want %q", functionResponse, firstName)
	}
	response := canonicalGeminiResponse(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": firstName, "args": map[string]any{}}}}}}}}, "gemini-3-pro", reverse)
	function, _ := response.ToolCalls[0]["function"].(map[string]any)
	if got := function["name"]; got != originalName {
		t.Fatalf("response function name=%q, want %q", got, originalName)
	}
	rawCandidates := antigravityAnySlice(response.Raw["candidates"])
	rawContent, _ := rawCandidates[0].(map[string]any)["content"].(map[string]any)
	rawCall, _ := antigravityMapSlice(rawContent["parts"])[0]["functionCall"].(map[string]any)
	if got := rawCall["name"]; got != originalName {
		t.Fatalf("raw response function name=%q, want %q", got, originalName)
	}
}

func TestParseGeminiUsageIncludesCachedContentTokens(t *testing.T) {
	usage := parseGeminiUsage(map[string]any{"promptTokenCount": 3, "candidatesTokenCount": 2, "cachedContentTokenCount": 7})
	if usage.InputTokens != 3 || usage.OutputTokens != 2 || usage.CachedTokens != 7 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestUpstreamGoogleRetryInfoPopulatesRetryAfter(t *testing.T) {
	err := buildUpstreamBodyError(http.StatusTooManyRequests, []byte(`{"error":{"message":"quota exhausted","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.479417207s"}]}}`), "")
	if got := RetryAfter(err); got != "0.479417207s" {
		t.Fatalf("retryAfter=%q", got)
	}
	objectErr := buildUpstreamBodyError(http.StatusTooManyRequests, []byte(`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":{"seconds":"1","nanos":"500000000"}}]}}`), "")
	if got := RetryAfter(objectErr); got != "1.5s" {
		t.Fatalf("object-form retryAfter=%q", got)
	}
	transientErr := buildUpstreamBodyError(http.StatusServiceUnavailable, []byte(`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"2s"}]}}`), "")
	if got := RetryAfter(transientErr); got != "2s" {
		t.Fatalf("transient retryAfter=%q", got)
	}
	if got := RetryAfter(buildUpstreamBodyError(http.StatusTooManyRequests, []byte(`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"bad"}]}}`), "")); got != "" {
		t.Fatalf("invalid retryAfter=%q", got)
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Message != "quota exhausted" {
		t.Fatalf("error=%+v", err)
	}
}

func TestVertexAdapterUsesProjectScopedGeminiEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/project-1/locations/us-central1/publishers/google/models/gemini-vertex:generateContent" {
			t.Fatalf("vertex path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer vertex-token" {
			t.Fatalf("vertex authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"vertex ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}`))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("vertex")
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Execute(context.Background(), store.Provider{ID: "vertex", Type: "vertex", BaseURL: upstream.URL, Config: map[string]any{"project": "project-1", "location": "us-central1"}}, store.Credential{AuthType: "service_account", Secret: "vertex-token", TokenType: "Bearer"}, canonical.Request{RequestID: "vertex-request", UpstreamModel: "gemini-vertex", Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil || response.Content != "vertex ok" || response.Usage.InputTokens != 2 {
		t.Fatalf("vertex response=%+v err=%v", response, err)
	}
}

func TestHTTPPluginAdapterUsesCanonicalOutOfProcessProtocol(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/execute" || r.Header.Get("Authorization") != "Bearer plugin-secret" {
			t.Fatalf("plugin path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var payload struct {
			Request canonical.Request `json:"request"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Request.UpstreamModel != "plugin-model" {
			t.Fatalf("plugin request=%+v err=%v", payload.Request, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"plugin-response","model":"plugin-model","role":"assistant","content":"plugin ok","finish_reason":"stop","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("plugin-http")
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Execute(context.Background(), store.Provider{ID: "plugin", Type: "plugin-http", BaseURL: upstream.URL}, store.Credential{AuthType: "api_key", Secret: "plugin-secret"}, canonical.Request{RequestID: "plugin-request", UpstreamModel: "plugin-model"})
	if err != nil || response.Content != "plugin ok" || response.Usage.OutputTokens != 2 {
		t.Fatalf("plugin response=%+v err=%v", response, err)
	}
}

func TestHTTPPluginStreamSynthesizesMessageEndFromDone(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			t.Fatalf("plugin path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"text_delta\",\"text\":\"plugin stream\"}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("plugin-http")
	if err != nil {
		t.Fatal(err)
	}
	events, err := adapter.ExecuteStream(context.Background(), store.Provider{ID: "plugin", Type: "plugin-http", BaseURL: upstream.URL}, store.Credential{AuthType: "none"}, canonical.Request{RequestID: "plugin-stream", UpstreamModel: "plugin-model"})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	messageEnds := 0
	for event := range events {
		text.WriteString(event.Text)
		if event.Type == canonical.EventMessageEnd {
			messageEnds++
		}
	}
	if text.String() != "plugin stream" || messageEnds != 1 {
		t.Fatalf("plugin stream text=%q message_ends=%d", text.String(), messageEnds)
	}
}

func TestNativeOpenAIRequestPreservesOpaqueFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "upstream-model" || payload["custom_extension"] != "keep-me" || payload["response_format"] == nil {
			t.Fatalf("native payload=%+v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"native","model":"upstream-model","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("openai-compatible")
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Execute(context.Background(), store.Provider{ID: "native", Type: "openai-compatible", BaseURL: upstream.URL}, store.Credential{AuthType: "none"}, canonical.Request{Source: canonical.ProtocolOpenAI, UpstreamModel: "upstream-model", Raw: map[string]any{"model": "public", "messages": []any{}, "custom_extension": "keep-me", "response_format": map[string]any{"type": "json_object"}}})
	if err != nil || response.Content != "ok" {
		t.Fatalf("native response=%+v err=%v", response, err)
	}
}

func TestTavilyAdapterNormalizesSearchRequestAndResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.Header.Get("Authorization") != "Bearer tavily-key" || r.Header.Get("X-Request-ID") != "search-request-1" {
			t.Fatalf("request path=%s headers=%+v", r.URL.Path, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["query"] != "latest Go release" || body["model"] != nil || body["search_depth"] != "advanced" || body["include_answer"] != true || body["max_results"] != float64(3) {
			t.Fatalf("Tavily body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"latest Go release","answer":"Go 1.26","results":[{"title":"Go","url":"https://go.dev","content":"release notes","score":0.99}],"request_id":"tavily-1"}`))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("tavily")
	if err != nil {
		t.Fatal(err)
	}
	rawAdapter, ok := adapter.(RawProxyAdapter)
	if !ok {
		t.Fatal("Tavily adapter does not implement raw search proxy")
	}
	forwardHeaders := make(http.Header)
	forwardHeaders.Set("X-Request-ID", "search-request-1")
	response, err := rawAdapter.Proxy(context.Background(), store.Provider{ID: "search", Type: "tavily", BaseURL: upstream.URL}, store.Credential{AuthType: "api_key", Secret: "tavily-key"}, RawRequest{
		Path: "/v1/search", Body: []byte(`{"model":"tavily-search","q":"latest Go release","limit":3}`), ContentType: "application/json", Headers: forwardHeaders,
	})
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err = json.Unmarshal(response.Body, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized["object"] != "search.results" || normalized["model"] != "tavily-search" || normalized["answer"] != "Go 1.26" {
		t.Fatalf("normalized response = %+v", normalized)
	}
}

func TestElevenLabsAdapterTranslatesSpeechAndTranscription(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "eleven-key" {
			t.Fatalf("headers = %+v", r.Header)
		}
		switch r.URL.Path {
		case "/v1/text-to-speech/voice-1":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["text"] != "hello" || body["model_id"] != "eleven-model" || r.URL.Query().Get("output_format") != "mp3_44100_128" {
				t.Fatalf("speech request body=%+v url=%s", body, r.URL.String())
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("audio-bytes"))
		case "/v1/speech-to-text":
			if !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Fatalf("transcription content type = %q", r.Header.Get("Content-Type"))
			}
			reader := multipart.NewReader(r.Body, strings.Split(strings.Split(r.Header.Get("Content-Type"), "boundary=")[1], ";")[0])
			seenModel := false
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(part)
				if part.FormName() == "model_id" && string(data) == "stt-model" {
					seenModel = true
				}
			}
			if !seenModel {
				t.Fatal("model_id field missing")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"transcribed"}`))
		case "/v1/voices":
			if r.Method != http.MethodGet {
				t.Fatalf("voices method=%s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"voices":[{"voice_id":"voice-1","name":"Narrator"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("elevenlabs")
	if err != nil {
		t.Fatal(err)
	}
	rawAdapter, ok := adapter.(RawProxyAdapter)
	if !ok {
		t.Fatal("ElevenLabs adapter does not implement raw proxy")
	}
	headers := make(http.Header)
	headers.Set("X-Request-ID", "audio-1")
	speech, err := rawAdapter.Proxy(context.Background(), store.Provider{ID: "audio", Type: "elevenlabs", BaseURL: upstream.URL}, store.Credential{AuthType: "api_key", Secret: "eleven-key"}, RawRequest{Path: "/v1/audio/speech", Body: []byte(`{"model":"eleven-model","input":"hello","voice":"voice-1","response_format":"mp3"}`), ContentType: "application/json", Headers: headers})
	if err != nil || speech.ContentType != "audio/mpeg" || string(speech.Body) != "audio-bytes" {
		t.Fatalf("speech response=%+v err=%v", speech, err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	modelField, _ := writer.CreateFormField("model")
	_, _ = modelField.Write([]byte("stt-model"))
	fileField, _ := writer.CreateFormFile("file", "audio.wav")
	_, _ = fileField.Write([]byte("wav-bytes"))
	_ = writer.Close()
	transcription, err := rawAdapter.Proxy(context.Background(), store.Provider{ID: "audio", Type: "elevenlabs", BaseURL: upstream.URL}, store.Credential{AuthType: "api_key", Secret: "eleven-key"}, RawRequest{Path: "/v1/audio/transcriptions", Body: body.Bytes(), ContentType: writer.FormDataContentType(), Headers: headers})
	if err != nil || !strings.Contains(string(transcription.Body), "transcribed") || !strings.Contains(string(transcription.Body), "stt-model") {
		t.Fatalf("transcription response=%s err=%v", transcription.Body, err)
	}
	voices, err := rawAdapter.Proxy(context.Background(), store.Provider{ID: "audio", Type: "elevenlabs", BaseURL: upstream.URL}, store.Credential{AuthType: "api_key", Secret: "eleven-key"}, RawRequest{Method: http.MethodGet, Path: "/v1/audio/voices", Headers: headers})
	if err != nil || !strings.Contains(string(voices.Body), "voice-1") || voices.Status != http.StatusOK {
		t.Fatalf("voices response=%s err=%v", voices.Body, err)
	}
}

func TestProviderHTTPProxyTransportRoutesRequestsWithProxyAuthentication(t *testing.T) {
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "upstream.invalid" || r.Header.Get("Proxy-Authorization") == "" {
			t.Fatalf("proxy request URL=%s headers=%+v", r.URL.String(), r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"proxied","model":"upstream","choices":[{"message":{"role":"assistant","content":"through proxy"},"finish_reason":"stop"}]}`))
	}))
	defer proxyServer.Close()
	proxyURL := strings.Replace(proxyServer.URL, "http://", "http://proxy-user:proxy-pass@", 1)
	adapter, err := NewRegistry().Adapter("openai-compatible")
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Execute(context.Background(), store.Provider{ID: "provider", Type: "openai-compatible", BaseURL: "http://upstream.invalid"}, store.Credential{ID: "credential", AuthType: "none", ProxyURL: proxyURL}, canonical.Request{RequestID: "proxy-request", UpstreamModel: "upstream", Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "through proxy" {
		t.Fatalf("response = %+v", response)
	}
	if transport, err := buildProxyTransport("socks5://127.0.0.1:1080"); err != nil || transport.Proxy != nil || transport.DialContext == nil {
		t.Fatalf("SOCKS5 transport=%+v err=%v", transport, err)
	}
}

func TestCodexDiscoveryUsesCodexModelsEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" || r.URL.Query().Get("client_version") == "" {
			t.Errorf("discovery request method=%s path=%s query=%q", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer codex-token" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Originator") != "codex_cli_rs" {
			t.Errorf("originator=%q", r.Header.Get("Originator"))
		}
		if r.Header.Get("ChatGPT-Account-ID") != "acct-123" {
			t.Errorf("account id=%q", r.Header.Get("ChatGPT-Account-ID"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.4","name":"GPT-5.4"}]}`))
	}))
	defer upstream.Close()
	registry := NewRegistry()
	provider := store.Provider{ID: "codex", Type: "codex", BaseURL: upstream.URL}
	credential := store.Credential{
		ID: "codex-account", AuthType: "oauth", Secret: "codex-token", TokenType: "Bearer",
		OAuthToken: &store.OAuthToken{AccessToken: "codex-token", Extra: map[string]any{"account_id": "acct-123"}},
	}
	models, err := registry.DiscoverModels(context.Background(), provider, credential)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-5.4" {
		t.Fatalf("discovered models=%+v", models)
	}
	if err = registry.HealthCheck(context.Background(), provider, credential); err != nil {
		t.Fatal(err)
	}
}

func TestAppendCodexWindows(t *testing.T) {
	quotas := map[string]QuotaEntry{}
	appendCodexWindows(quotas, "", map[string]any{
		"primary_window":   map[string]any{"used_percent": 25, "reset_at": "2030-01-01T00:00:00Z"},
		"secondary_window": map[string]any{"used_percent": 10, "reset_at": "2030-01-07T00:00:00Z"},
	})
	if len(quotas) != 2 || quotas["session"].Used != 25 || quotas["weekly"].Used != 10 {
		t.Fatalf("quotas=%+v", quotas)
	}
}

func TestProviderDiscoveryUsesLightweightCatalogRequest(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer catalog-key" || r.Header.Get("X-Request-ID") == "" {
			t.Errorf("discovery request method=%s path=%s auth=%q request_id=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Request-ID"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-a","owned_by":"upstream"},{"id":"model-b","name":"Model B"}]}`))
	}))
	defer upstream.Close()
	registry := NewRegistry()
	provider := store.Provider{ID: "catalog", Type: "openai-compatible", BaseURL: upstream.URL}
	credential := store.Credential{ID: "catalog-key", AuthType: "api_key", Secret: "catalog-key"}
	models, err := registry.DiscoverModels(context.Background(), provider, credential)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "model-a" || len(models[0].Capabilities) == 0 {
		t.Fatalf("discovered models=%+v", models)
	}
	if err = registry.HealthCheck(context.Background(), provider, credential); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("catalog calls=%d", calls.Load())
	}
}

func TestEndpointVersionlessOpenAIPaths(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		{"https://api.z.ai/api/paas/v4", "/v1/models", "https://api.z.ai/api/paas/v4/models"},
		{"https://api.z.ai/api/paas/v4/", "/v1/chat/completions", "https://api.z.ai/api/paas/v4/chat/completions"},
		{"https://api.z.ai/api/coding/paas/v4", "/v1/models", "https://api.z.ai/api/coding/paas/v4/models"},
		{"https://open.bigmodel.cn/api/coding/paas/v4", "/v1/chat/completions", "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions"},
		{"https://api.openai.com/v1", "/v1/models", "https://api.openai.com/v1/models"},
		{"https://api.openai.com", "/v1/models", "https://api.openai.com/v1/models"},
	}
	for _, tc := range cases {
		if got := endpoint(tc.base, tc.path); got != tc.want {
			t.Fatalf("endpoint(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}

func TestDiscoveryPathGLMUsesModelsEndpoint(t *testing.T) {
	provider := store.Provider{ID: "glm", Type: "openai-compatible", BaseURL: "https://api.z.ai/api/paas/v4"}
	if got := modelsDiscoveryURL(provider); got != "https://api.z.ai/api/paas/v4/models" {
		t.Fatalf("modelsDiscoveryURL() = %q, want https://api.z.ai/api/paas/v4/models", got)
	}
}

func TestDiscoverGLMFallsBackOn404(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Fatalf("path = %s", r.URL.Path)
		}
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	defer upstream.Close()

	registry := NewRegistry()
	models, err := registry.DiscoverModels(context.Background(), store.Provider{
		ID: "glm", Type: "openai-compatible", BaseURL: upstream.URL + "/api/paas/v4",
	}, store.Credential{Secret: "test-key"})
	if err != nil {
		t.Fatalf("DiscoverModels() error: %v", err)
	}
	if len(models) == 0 || models[0].ID != "glm-5.2" {
		t.Fatalf("models = %+v", models)
	}
}

func TestDiscoverCursorStaticModels(t *testing.T) {
	registry := NewRegistry()
	models, err := registry.DiscoverModels(context.Background(), store.Provider{
		ID: "cursor", Type: "cursor", BaseURL: "https://api2.cursor.sh",
	}, store.Credential{})
	if err != nil {
		t.Fatalf("DiscoverModels() error: %v", err)
	}
	if len(models) < 10 {
		t.Fatalf("expected static cursor models, got %+v", models)
	}
	if models[0].ID != "default" || models[0].OwnedBy != "cursor" {
		t.Fatalf("first model = %+v", models[0])
	}
}

func TestCursorAdapterRequiresMachineID(t *testing.T) {
	adapter, err := NewRegistry().Adapter("cursor")
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Execute(context.Background(), store.Provider{ID: "cursor", Type: "cursor", BaseURL: "https://api2.cursor.sh"}, store.Credential{
		AuthType:   "oauth",
		OAuthToken: &store.OAuthToken{AccessToken: "cursor-token"},
	}, canonical.Request{UpstreamModel: "default", Messages: []canonical.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error for missing machine ID")
	}
	providerErr, ok := err.(*ProviderError)
	if !ok || providerErr.Code != "authorization_required" {
		t.Fatalf("err = %v", err)
	}
}

func TestParseResponsesUsageIncludesCachedTokens(t *testing.T) {
	usage := parseResponsesUsage(map[string]any{
		"input_tokens":  120,
		"output_tokens": 40,
		"input_tokens_details": map[string]any{
			"cached_tokens": 80,
		},
	})
	if usage.InputTokens != 120 || usage.OutputTokens != 40 || usage.CachedTokens != 80 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestParseOpenAIUsageIncludesCachedTokens(t *testing.T) {
	usage := parseOpenAIUsage(map[string]any{
		"prompt_tokens":     90,
		"completion_tokens": 12,
		"prompt_tokens_details": map[string]any{
			"cached_tokens": 55,
		},
	})
	if usage.InputTokens != 90 || usage.OutputTokens != 12 || usage.CachedTokens != 55 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestCodexRenewalFromAccountsPayload(t *testing.T) {
	payload := map[string]any{
		"accounts": map[string]any{
			"acc-1": map[string]any{
				"entitlement": map[string]any{
					"has_active_subscription": true,
					"renews_at":               "2026-09-14T04:43:46+00:00",
					"expires_at":              "2026-09-14T10:43:46+00:00",
				},
			},
		},
	}
	if got := codexRenewalFromAccountsPayload(payload, "acc-1"); got != "2026-09-14T04:43:46Z" {
		t.Fatalf("renews_at = %q", got)
	}
	// Without renews_at the expiry is the next best renewal signal.
	delete(payload["accounts"].(map[string]any)["acc-1"].(map[string]any)["entitlement"].(map[string]any), "renews_at")
	if got := codexRenewalFromAccountsPayload(payload, "acc-1"); got != "2026-09-14T10:43:46Z" {
		t.Fatalf("expires_at fallback = %q", got)
	}
}

func TestCodexRenewalFromAccountsPayloadSelectsWantedAccount(t *testing.T) {
	payload := map[string]any{
		"accounts": map[string]any{
			"acc-1": map[string]any{"entitlement": map[string]any{"renews_at": "2026-09-14T04:43:46+00:00"}},
			"acc-2": map[string]any{"entitlement": map[string]any{"renews_at": "2026-10-01T00:00:00+00:00"}},
		},
	}
	if got := codexRenewalFromAccountsPayload(payload, "acc-2"); got != "2026-10-01T00:00:00Z" {
		t.Fatalf("wanted account renews_at = %q", got)
	}
}

func TestCodexRenewalFromAccountsPayloadSoftFails(t *testing.T) {
	if got := codexRenewalFromAccountsPayload(map[string]any{}, ""); got != "" {
		t.Fatalf("empty payload = %q", got)
	}
	if got := codexRenewalFromAccountsPayload(map[string]any{"accounts": map[string]any{"acc-1": map[string]any{}}}, ""); got != "" {
		t.Fatalf("missing entitlement = %q", got)
	}
}
