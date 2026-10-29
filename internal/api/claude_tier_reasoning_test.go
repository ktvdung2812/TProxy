package api

import (
	"testing"

	"github.com/tproxy/tproxy/internal/bridge"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/thinking"
)

func aliasServer(role bridge.Role, effort string) *Server {
	server := &Server{claudeAliases: bridge.NewResolver(bridge.Config{})}
	server.claudeAliases.SetReasoningEffortOverrides(bridge.ReasoningEffortOverrides{role: effort})
	return server
}

func TestApplyClaudeTierReasoningEffortContributesHint(t *testing.T) {
	server := aliasServer(bridge.RoleFable, "high")

	request := canonical.Request{
		PublicModelID: "codex:gpt-5.6-sol",
		Raw: map[string]any{
			"model":    "fable",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		},
		Metadata: map[string]any{claudeClientModelMetadataKey: "fable"},
	}
	server.applyClaudeTierReasoningEffort(&request)

	// The alias no longer writes into the client's pass-through body.
	if _, ok := request.Raw["reasoning_effort"]; ok {
		t.Fatalf("alias must not write reasoning_effort into Raw: %#v", request.Raw)
	}
	// It contributes a hint that Normalize resolves.
	normalized := thinking.Normalize(request)
	if got := normalized.Reasoning["effort"]; got != "high" {
		t.Fatalf("effort = %#v, want high", got)
	}
}

func TestApplyClaudeTierReasoningEffortRespectsClientThinking(t *testing.T) {
	server := aliasServer(bridge.RoleFable, "high")

	request := canonical.Request{
		PublicModelID: "codex:gpt-5.6-sol",
		Raw: map[string]any{
			"model": "fable",
			"thinking": map[string]any{
				"type":          "enabled",
				"budget_tokens": 12000,
			},
		},
		Metadata: map[string]any{claudeClientModelMetadataKey: "fable"},
	}
	server.applyClaudeTierReasoningEffort(&request)
	if _, ok := request.Metadata[thinking.MetadataAliasEffort]; ok {
		t.Fatalf("expected client thinking to suppress the alias hint")
	}

	// The client's budget survives normalization; the alias effort never replaces it.
	normalized := thinking.Normalize(request)
	if got := normalized.Reasoning["effort"]; got != "high" {
		t.Fatalf("budget 12000 should map to high, got %#v", got)
	}
	if got := normalized.Reasoning["budget_tokens"]; got != 12000 {
		t.Fatalf("client budget lost: %#v", normalized.Reasoning)
	}
}

func TestClaudeRequestHasReasoningPreference(t *testing.T) {
	if !claudeRequestHasReasoningPreference(map[string]any{"reasoning_effort": "low"}) {
		t.Fatal("expected reasoning_effort to count")
	}
	if !claudeRequestHasReasoningPreference(map[string]any{
		"thinking": map[string]any{"type": "adaptive"},
	}) {
		t.Fatal("expected adaptive thinking to count")
	}
	if claudeRequestHasReasoningPreference(map[string]any{"messages": []any{}}) {
		t.Fatal("expected empty body to have no preference")
	}
}
