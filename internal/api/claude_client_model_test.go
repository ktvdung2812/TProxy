package api

import (
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestPreserveClaudeClientModel(t *testing.T) {
	request := canonical.Request{PublicModelID: "sonnet"}
	preserveClaudeClientModel(&request)
	if request.Metadata[claudeClientModelMetadataKey] != "sonnet" {
		t.Fatalf("metadata = %#v", request.Metadata)
	}

	real := canonical.Request{PublicModelID: "claude-opus-4-7"}
	preserveClaudeClientModel(&real)
	if real.Metadata != nil {
		t.Fatalf("expected no metadata for real model, got %#v", real.Metadata)
	}
}

func TestClientFacingModel(t *testing.T) {
	request := canonical.Request{
		Metadata: map[string]any{claudeClientModelMetadataKey: "fable"},
	}
	if got := clientFacingModel(request, "codex:gpt-5.6-luna"); got != "fable" {
		t.Fatalf("client model = %q", got)
	}
	if got := clientFacingModel(canonical.Request{}, "codex:gpt-5.4"); got != "codex:gpt-5.4" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestRenderClaudeUsesClientModel(t *testing.T) {
	payload := renderClaude(&canonical.Response{Model: "codex:gpt-5.4", Content: "ok"}, "req-1", "sonnet")
	if payload["model"] != "sonnet" {
		t.Fatalf("model = %v", payload["model"])
	}
}
