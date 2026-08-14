package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func oauthCredential() store.Credential {
	return store.Credential{ID: "cred-1", AuthType: "oauth", Secret: "sk-ant-oat01-abc", TokenType: "Bearer"}
}

func claudeProvider(baseURL string) store.Provider {
	return store.Provider{ID: "claude", Type: "claude", BaseURL: baseURL}
}

func TestClaudeCloakSuffixesCallerToolsAndAddsDecoys(t *testing.T) {
	body := map[string]any{
		"tools": []any{
			map[string]any{"name": "read_file", "description": "read"},
			map[string]any{"type": "web_search_20250305", "name": "web_search"},
		},
		"tool_choice": map[string]any{"type": "tool", "name": "read_file"},
	}
	reverse := claudeCloakRequest(body, oauthCredential(), false)

	if reverse["read_file_cc"] != "read_file" {
		t.Fatalf("reverse map = %+v", reverse)
	}
	tools, ok := body["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %T", body["tools"])
	}
	if len(tools) != 2+len(claudeDecoyTools) {
		t.Fatalf("tool count = %d", len(tools))
	}
	first, _ := tools[0].(map[string]any)
	if stringValue(first["name"]) != "read_file_cc" {
		t.Fatalf("caller tool = %+v", first)
	}
	// A server-side tool carries a reserved name upstream validates; suffixing
	// it would make the request fail outright.
	builtin, _ := tools[1].(map[string]any)
	if stringValue(builtin["name"]) != "web_search" {
		t.Fatalf("built-in tool renamed: %+v", builtin)
	}
	choice, _ := body["tool_choice"].(map[string]any)
	if stringValue(choice["name"]) != "read_file_cc" {
		t.Fatalf("tool_choice = %+v", choice)
	}
}

func TestClaudeCloakRenamesPriorToolUseBlocks(t *testing.T) {
	body := map[string]any{
		"tools": []any{map[string]any{"name": "run_command"}},
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "t1", "name": "run_command"},
			}},
		},
	}
	claudeCloakRequest(body, oauthCredential(), false)

	messages, _ := body["messages"].([]any)
	assistant, _ := messages[1].(map[string]any)
	content, _ := assistant["content"].([]any)
	block, _ := content[0].(map[string]any)
	if stringValue(block["name"]) != "run_command_cc" {
		t.Fatalf("tool_use block = %+v", block)
	}
}

// The request body is a shallow clone of the caller's raw request, which the
// router keeps for retries and for failover to another provider. Cloaking must
// therefore never write through into the caller's nested values.
func TestClaudeCloakLeavesCallerRequestUnmodified(t *testing.T) {
	raw := map[string]any{
		"model": "claude-sonnet-4-5",
		"tools": []any{map[string]any{"name": "read_file"}},
		"messages": []any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "t1", "name": "read_file"},
			}},
		},
		"tool_choice": map[string]any{"type": "tool", "name": "read_file"},
		"system":      "be brief",
	}
	before, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}

	request := canonical.Request{Source: canonical.ProtocolClaude, Raw: raw, UpstreamModel: "claude-sonnet-4-5"}
	body, reverse := anthropicRequestBody(claudeProvider("http://example.invalid"), oauthCredential(), request)
	if len(reverse) == 0 {
		t.Fatal("expected the caller tool to be renamed")
	}
	if tools, _ := body["tools"].([]any); len(tools) <= 1 {
		t.Fatalf("upstream body was not cloaked: %+v", body["tools"])
	}

	after, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("caller request mutated:\nbefore=%s\nafter =%s", before, after)
	}
}

func TestClaudeCloakInjectsBillingBlockOnceAndPreservesSystem(t *testing.T) {
	body := map[string]any{"system": "be brief"}
	claudeCloakRequest(body, oauthCredential(), false)

	blocks, _ := body["system"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("system blocks = %+v", blocks)
	}
	first, _ := blocks[0].(map[string]any)
	if !strings.HasPrefix(stringValue(first["text"]), claudeBillingPrefix) {
		t.Fatalf("billing block = %+v", first)
	}
	if !strings.Contains(stringValue(first["text"]), "cc_version="+claudeCodeVersion+".") {
		t.Fatalf("billing block missing version: %+v", first)
	}
	second, _ := blocks[1].(map[string]any)
	if stringValue(second["text"]) != "be brief" {
		t.Fatalf("caller system dropped: %+v", second)
	}

	// Re-entering the same body (a retry) must not stack a second block.
	claudeCloakRequest(body, oauthCredential(), false)
	if blocks, _ := body["system"].([]any); len(blocks) != 2 {
		t.Fatalf("billing block injected twice: %+v", blocks)
	}
}

func TestClaudeCloakInjectsStableIdentityMetadata(t *testing.T) {
	first := map[string]any{}
	claudeCloakRequest(first, oauthCredential(), false)
	second := map[string]any{}
	claudeCloakRequest(second, oauthCredential(), false)

	metadata, _ := first["metadata"].(map[string]any)
	userID := stringValue(metadata["user_id"])
	if userID == "" {
		t.Fatalf("metadata = %+v", metadata)
	}
	var parsed struct {
		DeviceID    string `json:"device_id"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(userID), &parsed); err != nil {
		t.Fatalf("user_id is not JSON: %v", err)
	}
	if len(parsed.DeviceID) != 64 || len(parsed.AccountUUID) != 36 || len(parsed.SessionID) != 36 {
		t.Fatalf("identity = %+v", parsed)
	}
	// The same account must present the same device across requests.
	otherMetadata, _ := second["metadata"].(map[string]any)
	if stringValue(otherMetadata["user_id"]) != userID {
		t.Fatalf("identity changed between requests")
	}
}

// The identity line is visible to the model, so it stays off unless the
// operator turns it on — and even then the caller's own system prompt survives.
func TestClaudeAgentPromptIsOptIn(t *testing.T) {
	raw := map[string]any{"system": "be brief"}
	request := canonical.Request{Source: canonical.ProtocolClaude, Raw: raw, UpstreamModel: "claude-sonnet-4-5"}

	body, _ := anthropicRequestBody(claudeProvider("http://example.invalid"), oauthCredential(), request)
	blocks, _ := body["system"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("default system blocks = %+v", blocks)
	}

	provider := claudeProvider("http://example.invalid")
	provider.Config = map[string]any{"claude_agent_prompt": "on"}
	body, _ = anthropicRequestBody(provider, oauthCredential(), request)
	blocks, _ = body["system"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("opt-in system blocks = %+v", blocks)
	}
	agent, _ := blocks[1].(map[string]any)
	if stringValue(agent["text"]) != claudeAgentPrompt {
		t.Fatalf("agent block = %+v", agent)
	}
	caller, _ := blocks[2].(map[string]any)
	if stringValue(caller["text"]) != "be brief" {
		t.Fatalf("caller system dropped: %+v", caller)
	}
}

func TestClaudeCloakKeepsCallerSuppliedUserID(t *testing.T) {
	body := map[string]any{"metadata": map[string]any{"user_id": "caller-supplied"}}
	claudeCloakRequest(body, oauthCredential(), false)
	metadata, _ := body["metadata"].(map[string]any)
	if stringValue(metadata["user_id"]) != "caller-supplied" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestClaudeCloakSkippedForAPIKeyCredentials(t *testing.T) {
	raw := map[string]any{"tools": []any{map[string]any{"name": "read_file"}}}
	request := canonical.Request{Source: canonical.ProtocolClaude, Raw: raw, UpstreamModel: "claude-sonnet-4-5"}
	apiKey := store.Credential{ID: "cred-2", AuthType: "api_key", Secret: "sk-ant-api03-xyz"}

	body, reverse := anthropicRequestBody(claudeProvider("http://example.invalid"), apiKey, request)
	if reverse != nil {
		t.Fatalf("api key request was cloaked: %+v", reverse)
	}
	if _, injected := body["system"]; injected {
		t.Fatalf("api key request got a billing block: %+v", body)
	}
	if tools, _ := body["tools"].([]any); len(tools) != 1 {
		t.Fatalf("api key tools rewritten: %+v", tools)
	}
}

// A genuine Claude Code caller already sends Claude Code tool names; suffixing
// them and appending decoys would make an already-correct request look wrong.
func TestClaudeCloakSkippedForNativeClaudeCodeClients(t *testing.T) {
	raw := map[string]any{"tools": []any{map[string]any{"name": "Read"}}}
	request := canonical.Request{
		Source:        canonical.ProtocolClaude,
		Raw:           raw,
		UpstreamModel: "claude-sonnet-4-5",
		Metadata:      map[string]any{"client_headers": map[string]string{"user-agent": "claude-cli/2.1.63 (external, cli)"}},
	}

	body, reverse := anthropicRequestBody(claudeProvider("http://example.invalid"), oauthCredential(), request)
	if reverse != nil {
		t.Fatalf("native client request was cloaked: %+v", reverse)
	}
	if tools, _ := body["tools"].([]any); len(tools) != 1 {
		t.Fatalf("native client tools rewritten: %+v", tools)
	}
	if _, injected := body["system"]; injected {
		t.Fatalf("native client got an injected billing block: %+v", body)
	}
}

func TestClaudeCloakDisabledByProviderConfig(t *testing.T) {
	raw := map[string]any{"tools": []any{map[string]any{"name": "read_file"}}}
	request := canonical.Request{Source: canonical.ProtocolClaude, Raw: raw, UpstreamModel: "claude-sonnet-4-5"}
	provider := claudeProvider("http://example.invalid")
	provider.Config = map[string]any{"claude_cloaking": "off"}

	_, reverse := anthropicRequestBody(provider, oauthCredential(), request)
	if reverse != nil {
		t.Fatalf("cloaking stayed on: %+v", reverse)
	}
}

func TestClaudeAdapterRestoresToolNamesInResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var sent map[string]any
		if err := json.Unmarshal(payload, &sent); err != nil {
			t.Fatalf("upstream body: %v", err)
		}
		if tools, _ := sent["tools"].([]any); len(tools) != 1+len(claudeDecoyTools) {
			t.Fatalf("upstream tools = %+v", sent["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-1","type":"message","role":"assistant","model":"claude-sonnet","content":[{"type":"tool_use","id":"t1","name":"read_file_cc","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	adapter, err := NewRegistry().Adapter("claude")
	if err != nil {
		t.Fatal(err)
	}
	request := canonical.Request{
		Source:        canonical.ProtocolClaude,
		UpstreamModel: "claude-sonnet",
		Raw:           map[string]any{"tools": []any{map[string]any{"name": "read_file"}}},
	}
	response, err := adapter.Execute(context.Background(), claudeProvider(upstream.URL), oauthCredential(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", response.ToolCalls)
	}
	function, _ := response.ToolCalls[0]["function"].(map[string]any)
	if stringValue(function["name"]) != "read_file" {
		t.Fatalf("tool name not restored: %+v", function)
	}
	// Claude-protocol callers are handed Raw verbatim, so it must be restored too.
	blocks, _ := response.Raw["content"].([]any)
	block, _ := blocks[0].(map[string]any)
	if stringValue(block["name"]) != "read_file" {
		t.Fatalf("raw tool name not restored: %+v", block)
	}
}

func TestClaudeAdapterRestoresToolNamesInStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"read_file_cc\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer upstream.Close()

	adapter, err := NewRegistry().Adapter("claude")
	if err != nil {
		t.Fatal(err)
	}
	request := canonical.Request{
		Source:        canonical.ProtocolClaude,
		UpstreamModel: "claude-sonnet",
		Raw:           map[string]any{"tools": []any{map[string]any{"name": "read_file"}}},
	}
	events, err := adapter.ExecuteStream(context.Background(), claudeProvider(upstream.URL), oauthCredential(), request)
	if err != nil {
		t.Fatal(err)
	}
	name := ""
	for event := range events {
		if event.Type != canonical.EventToolCallDelta {
			continue
		}
		function, _ := event.ToolCall["function"].(map[string]any)
		if value := stringValue(function["name"]); value != "" {
			name = value
		}
	}
	if name != "read_file" {
		t.Fatalf("stream tool name = %q", name)
	}
}
