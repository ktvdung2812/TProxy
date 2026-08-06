package claudeopenai_test

import (
	"testing"

	"github.com/tproxy/tproxy/internal/translator/claudeopenai"
)

func TestClaudeToOpenAIRequestSystemArray(t *testing.T) {
	body := map[string]any{
		"model": "sonnet",
		"system": []any{
			map[string]any{"type": "text", "text": "Rule one"},
			map[string]any{"type": "text", "text": "Rule two"},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	out := claudeopenai.ClaudeToOpenAIRequest("gpt-5.4", body, true)
	messages, _ := out["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	system := messages[0].(map[string]any)
	if system["role"] != "system" || system["content"] != "Rule one\nRule two" {
		t.Fatalf("system message = %#v", system)
	}
}

func TestSystemInstructionsText(t *testing.T) {
	text := claudeopenai.SystemInstructionsText([]any{
		map[string]any{"type": "text", "text": "alpha"},
		map[string]any{"type": "text", "text": "beta"},
	})
	if text != "alpha\nbeta" {
		t.Fatalf("text = %q", text)
	}
}

func TestClaudeToOpenAIRequestToolsAndToolResults(t *testing.T) {
	body := map[string]any{
		"max_tokens": 1024,
		"system":     "You are helpful.",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "hello"},
				},
			},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": map[string]any{"city": "NYC"}},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "sunny"},
				},
			},
		},
		"tools": []any{
			map[string]any{
				"name":        "get_weather",
				"description": "Get weather",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
				},
			},
		},
		"tool_choice": map[string]any{"type": "any"},
	}

	out := claudeopenai.ClaudeToOpenAIRequest("gpt-5.4", body, true)
	messages, _ := out["messages"].([]any)
	if len(messages) < 4 {
		t.Fatalf("messages = %#v", messages)
	}
	system := messages[0].(map[string]any)
	if system["role"] != "system" {
		t.Fatalf("system role = %#v", system)
	}
	toolMsg := messages[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "toolu_1" {
		t.Fatalf("tool message = %#v", toolMsg)
	}
	if out["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %#v", out["tool_choice"])
	}
}

func TestOpenAIToClaudeRequestMergesToolResults(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "You are helpful."},
			map[string]any{"role": "user", "content": "What's in this image?"},
			map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{
					map[string]any{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      "get_weather",
							"arguments": `{"city":"NYC"}`,
						},
					},
				},
			},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "sunny"},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "Get weather",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		"tool_choice": "required",
	}

	out := claudeopenai.OpenAIToClaudeRequest("claude-sonnet-4-5", body, true)
	messages, _ := out["messages"].([]any)
	if len(messages) < 3 {
		t.Fatalf("messages = %#v", messages)
	}
	toolResultMsg := messages[2].(map[string]any)
	content, _ := toolResultMsg["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("tool result content = %#v", content)
	}
	block := content[0].(map[string]any)
	if block["type"] != "tool_result" || block["tool_use_id"] != "call_1" {
		t.Fatalf("tool result block = %#v", block)
	}
	choice := out["tool_choice"].(map[string]any)
	if choice["type"] != "any" {
		t.Fatalf("tool_choice = %#v", choice)
	}
}

func TestOpenAIToClaudeRequestImage(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "look"},
					map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "data:image/png;base64,QUJDRA==",
						},
					},
				},
			},
		},
	}
	out := claudeopenai.OpenAIToClaudeRequest("claude-sonnet-4-5", body, false)
	messages := out["messages"].([]any)
	msg := messages[0].(map[string]any)
	content := msg["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %#v", content)
	}
	image := content[1].(map[string]any)
	source := image["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "image/png" {
		t.Fatalf("image source = %#v", source)
	}
}

// A tool_use with no matching tool_result (aborted turn) needs a stand-in tool
// message, and inserting it must not drop any of the messages that follow.
func TestClaudeToOpenAIRequestFillsMissingToolResults(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "run"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "call_1", "name": "shell", "input": map[string]any{"a": 1}},
				map[string]any{"type": "tool_use", "id": "call_2", "name": "shell", "input": map[string]any{"a": 2}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "ok1"},
			}},
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	out := claudeopenai.ClaudeToOpenAIRequest("gpt-5.4", body, false)
	messages, _ := out["messages"].([]any)
	if len(messages) != 5 {
		t.Fatalf("messages = %#v", messages)
	}
	placeholder, _ := messages[3].(map[string]any)
	if placeholder["role"] != "tool" || placeholder["tool_call_id"] != "call_2" {
		t.Fatalf("placeholder = %#v", messages[3])
	}
	last, _ := messages[4].(map[string]any)
	if last["role"] != "user" || last["content"] != "hi" {
		t.Fatalf("trailing message = %#v", messages[4])
	}
}
