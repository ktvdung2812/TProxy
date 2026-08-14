package providers

import (
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestCodexMessagesToInputConvertsTextParts(t *testing.T) {
	body := codexBody(canonical.Request{
		Source: canonical.ProtocolOpenAI,
		Messages: []canonical.Message{
			{Role: "user", Content: []any{map[string]any{"type": "text", "text": "Hello from Zoo Code"}}},
			{Role: "assistant", Content: []any{map[string]any{"type": "text", "text": "Hi there"}}},
		},
	})

	input, ok := body["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("input = %#v", body["input"])
	}
	user, _ := input[0].(map[string]any)
	if user["type"] != "message" || user["role"] != "user" {
		t.Fatalf("user message = %#v", user)
	}
	userParts, _ := user["content"].([]any)
	if len(userParts) != 1 {
		t.Fatalf("user parts = %#v", user["content"])
	}
	userPart, _ := userParts[0].(map[string]any)
	if userPart["type"] != "input_text" || userPart["text"] != "Hello from Zoo Code" {
		t.Fatalf("user part = %#v", userPart)
	}

	assistant, _ := input[1].(map[string]any)
	assistantParts, _ := assistant["content"].([]any)
	assistantPart, _ := assistantParts[0].(map[string]any)
	if assistantPart["type"] != "output_text" || assistantPart["text"] != "Hi there" {
		t.Fatalf("assistant part = %#v", assistantPart)
	}
}

func TestCodexNormalizeInputValueConvertsStringAndTextParts(t *testing.T) {
	normalized := codexNormalizeInputValue([]any{
		map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": "ping"}},
		},
	}, nil)
	items, ok := normalized.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("normalized = %#v", normalized)
	}
	item, _ := items[0].(map[string]any)
	if item["type"] != "message" {
		t.Fatalf("item = %#v", item)
	}
	parts, _ := item["content"].([]any)
	part, _ := parts[0].(map[string]any)
	if part["type"] != "input_text" || part["text"] != "ping" {
		t.Fatalf("part = %#v", part)
	}
}

func TestCodexMessagesToInputShortensHistoryToolNames(t *testing.T) {
	longName := strings.Repeat("mcp__server__", 4) + "lookup_files"
	body := codexBody(canonical.Request{
		Source: canonical.ProtocolOpenAI,
		Messages: []canonical.Message{
			{
				Role: "assistant",
				ToolCalls: []map[string]any{{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      longName,
						"arguments": `{}`,
					},
				}},
			},
		},
		Tools: []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        longName,
				"description": "lookup",
			},
		}},
	})
	input, ok := body["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v", body["input"])
	}
	item, _ := input[0].(map[string]any)
	if item["type"] != "function_call" {
		t.Fatalf("item = %#v", item)
	}
	name := item["name"].(string)
	if len(name) > 64 {
		t.Fatalf("tool name not shortened: len=%d name=%q", len(name), name)
	}
}

func TestCodexMessagesToInputMapsSystemRoleToInstructions(t *testing.T) {
	body := codexBody(canonical.Request{
		Source:   canonical.ProtocolOpenAI,
		Messages: []canonical.Message{{Role: "system", Content: "You are helpful"}},
	})
	if body["instructions"] != "You are helpful" {
		t.Fatalf("instructions = %#v", body["instructions"])
	}
	input, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("input = %#v", body["input"])
	}
	if len(input) != 0 {
		t.Fatalf("system should not be duplicated in input: %#v", input)
	}
}

func TestCodexSanitizeInputItemsStripsOrphanReasoningID(t *testing.T) {
	body := codexBody(canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "gpt-5.4",
		Raw: map[string]any{
			"model": "gpt-5.4",
			"input": []any{
				map[string]any{
					"type": "reasoning",
					"id":   "rs_resp_req_test_0",
					"summary": []any{
						map[string]any{"type": "summary_text", "text": "thinking"},
					},
				},
				map[string]any{
					"type":    "message",
					"role":    "user",
					"content": []any{map[string]any{"type": "input_text", "text": "next"}},
				},
			},
		},
	})
	input, ok := body["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("input = %#v", body["input"])
	}
	reasoning, _ := input[0].(map[string]any)
	if _, exists := reasoning["id"]; exists {
		t.Fatalf("orphan reasoning id should be stripped: %#v", reasoning)
	}
	if body["store"] != false {
		t.Fatalf("store = %#v", body["store"])
	}
}

func TestCodexSanitizeInputItemsKeepsReasoningIDWithEncryptedContent(t *testing.T) {
	body := codexBody(canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "gpt-5.4",
		Raw: map[string]any{
			"model": "gpt-5.4",
			"input": []any{
				map[string]any{
					"type":              "reasoning",
					"id":                "rs_good",
					"encrypted_content": "opaque-signature",
					"summary":           []any{},
				},
			},
		},
	})
	input, _ := body["input"].([]any)
	reasoning, _ := input[0].(map[string]any)
	if reasoning["id"] != "rs_good" {
		t.Fatalf("reasoning with encrypted_content should keep id: %#v", reasoning)
	}
}

func TestCodexSanitizeInputItemsStripsEphemeralMessageID(t *testing.T) {
	body := codexBody(canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "gpt-5.4",
		Raw: map[string]any{
			"model": "gpt-5.4",
			"input": []any{
				map[string]any{
					"type":    "message",
					"id":      "msg_resp_req_test_0",
					"role":    "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "hi"}},
				},
			},
		},
	})
	input, _ := body["input"].([]any)
	message, _ := input[0].(map[string]any)
	if _, exists := message["id"]; exists {
		t.Fatalf("ephemeral message id should be stripped: %#v", message)
	}
}

func TestCodexNormalizeInputItemsSanitizesHistoryFunctionCallNames(t *testing.T) {
	body := codexBody(canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "gpt-5.4",
		Raw: map[string]any{
			"model": "gpt-5.4",
			"input": []any{
				map[string]any{
					"type":      "function_call",
					"call_id":   "call_history",
					"name":      "mcp__plugin.context7/query-docs",
					"arguments": `{}`,
				},
			},
		},
	})
	input, ok := body["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v", body["input"])
	}
	item, _ := input[0].(map[string]any)
	name := item["name"].(string)
	if name == "" || codexToolNameSanitizer.MatchString(name) {
		t.Fatalf("function_call name not sanitized: %q", name)
	}
}

func TestCodexBodyClaudeSystemArrayUsesStringInstructions(t *testing.T) {
	body := codexBody(canonical.Request{
		Source: canonical.ProtocolClaude,
		System: []any{
			map[string]any{"type": "text", "text": "Follow project rules."},
			map[string]any{"type": "text", "text": "Be concise."},
		},
		Raw: map[string]any{
			"model":      "sonnet",
			"max_tokens": 1024,
			"system": []any{
				map[string]any{"type": "text", "text": "Follow project rules."},
				map[string]any{"type": "text", "text": "Be concise."},
			},
			"messages": []any{
				map[string]any{"role": "user", "content": "hi"},
			},
		},
		Messages: []canonical.Message{{Role: "user", Content: "hi"}},
	})
	instructions, ok := body["instructions"].(string)
	if !ok || instructions != "Follow project rules.\nBe concise." {
		t.Fatalf("instructions = %#v", body["instructions"])
	}
}
