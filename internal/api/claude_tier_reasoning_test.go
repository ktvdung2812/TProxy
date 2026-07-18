package api

import (
	"testing"

	"github.com/tproxy/tproxy/internal/bridge"
	"github.com/tproxy/tproxy/internal/canonical"
)

func TestApplyClaudeTierReasoningEffort(t *testing.T) {
	server := &Server{
		claudeAliases: bridge.NewResolver(bridge.Config{}),
	}
	server.claudeAliases.SetReasoningEffortOverrides(bridge.ReasoningEffortOverrides{
		bridge.RoleFable: "high",
	})

	request := canonical.Request{
		PublicModelID: "codex:gpt-5.6-sol",
		Raw: map[string]any{
			"model":    "fable",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		},
		Metadata: map[string]any{claudeClientModelMetadataKey: "fable"},
	}
	server.applyClaudeTierReasoningEffort(&request)
	if got := request.Raw["reasoning_effort"]; got != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", got)
	}
}

func TestApplyClaudeTierReasoningEffortRespectsClientThinking(t *testing.T) {
	server := &Server{
		claudeAliases: bridge.NewResolver(bridge.Config{}),
	}
	server.claudeAliases.SetReasoningEffortOverrides(bridge.ReasoningEffortOverrides{
		bridge.RoleFable: "high",
	})

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
	if _, ok := request.Raw["reasoning_effort"]; ok {
		t.Fatalf("expected client thinking to win over mapping effort")
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
