package api

import (
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func claudeRequest(system any, messages ...canonical.Message) canonical.Request {
	return canonical.Request{RequestID: "req-1", Source: canonical.ProtocolClaude, System: system, Messages: messages}
}

func TestDetectClaudeBypassHousekeeping(t *testing.T) {
	warmup := claudeRequest(nil, canonical.Message{Role: "user", Content: "Warmup"})
	if _, ok := detectClaudeBypass(warmup, false); !ok {
		t.Error("warmup should bypass")
	}

	count := claudeRequest(nil, canonical.Message{Role: "user", Content: "count"})
	if _, ok := detectClaudeBypass(count, false); !ok {
		t.Error("count probe should bypass")
	}

	title := claudeRequest(nil,
		canonical.Message{Role: "user", Content: "hello"},
		canonical.Message{Role: "assistant", Content: []any{map[string]any{"type": "text", "text": "{"}}},
	)
	if _, ok := detectClaudeBypass(title, false); !ok {
		t.Error("title primer should bypass")
	}

	normal := claudeRequest(nil, canonical.Message{Role: "user", Content: "refactor this function"})
	if _, ok := detectClaudeBypass(normal, true); ok {
		t.Error("ordinary turn must not bypass")
	}
}

func TestDetectClaudeBypassNamingRespectsToggle(t *testing.T) {
	system := []any{map[string]any{"type": "text", "text": "Return {\"isNewTopic\": bool, \"title\": string}"}}
	request := claudeRequest(system, canonical.Message{Role: "user", Content: "add retry logic to the client"})

	if _, ok := detectClaudeBypass(request, false); ok {
		t.Error("naming must not bypass while the filter is off")
	}

	text, ok := detectClaudeBypass(request, true)
	if !ok {
		t.Fatal("naming should bypass while the filter is on")
	}
	if want := `{"isNewTopic":true,"title":"add retry logic"}`; text != want {
		t.Errorf("naming reply = %s, want %s", text, want)
	}
}

func TestSystemPromptTextReadsInBandSystemMessage(t *testing.T) {
	request := claudeRequest(nil,
		canonical.Message{Role: "system", Content: "emit isNewTopic please"},
		canonical.Message{Role: "user", Content: "hi there friend"},
	)
	if _, ok := detectClaudeBypass(request, true); !ok {
		t.Error("system message form should bypass")
	}
}
