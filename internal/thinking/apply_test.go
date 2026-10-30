package thinking

import "testing"

func TestApplyCodexWritesReasoningObject(t *testing.T) {
	body := map[string]any{}
	Apply(ProviderCodex, "gpt-5.6-sol", body, Config{Effort: "high", Set: true})

	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("effort = %#v", reasoning["effort"])
	}
	if reasoning["summary"] != "auto" {
		t.Fatalf("summary = %#v", reasoning["summary"])
	}
	if _, leaked := body["reasoning_effort"]; leaked {
		t.Fatalf("reasoning_effort must not reach Codex: %#v", body)
	}
	include, _ := body["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", body["include"])
	}
}

func TestApplyCodexNoneRemovesReasoning(t *testing.T) {
	body := map[string]any{"reasoning_effort": "high", "include": []any{"reasoning.encrypted_content"}}
	Apply(ProviderCodex, "gpt-5.6-sol", body, Config{Effort: "none", Set: true})

	if _, exists := body["reasoning"]; exists {
		t.Fatalf("reasoning = %#v, want removed", body["reasoning"])
	}
	if _, exists := body["include"]; exists {
		t.Fatalf("include = %#v, want removed", body["include"])
	}
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatalf("reasoning_effort must be consumed: %#v", body)
	}
}

func TestApplyCodexDefaultsToLow(t *testing.T) {
	body := map[string]any{}
	Apply(ProviderCodex, "gpt-5.6-sol", body, Config{})
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "low" {
		t.Fatalf("effort = %#v, want the historical Codex default", reasoning["effort"])
	}
}

func TestApplyClaudeWritesBudget(t *testing.T) {
	body := map[string]any{}
	Apply(ProviderClaude, "claude-sonnet-5", body, Config{Effort: "high", BudgetTokens: 12000, Set: true})

	block, _ := body["thinking"].(map[string]any)
	if block["type"] != "enabled" || block["budget_tokens"] != 12000 {
		t.Fatalf("thinking = %#v", body["thinking"])
	}
}

func TestApplyClaudeSkipsEffortOnly(t *testing.T) {
	body := map[string]any{}
	Apply(ProviderClaude, "claude-sonnet-5", body, Config{Effort: "high", Set: true})
	if _, exists := body["thinking"]; exists {
		t.Fatalf("an effort label alone is not a Claude budget: %#v", body["thinking"])
	}
}

func TestApplyGeminiWritesBudgetOnly(t *testing.T) {
	body := map[string]any{}
	Apply(ProviderGemini, "gemini-3.8-flash-high", body, Config{BudgetTokens: 4000, Set: true})

	generation, _ := body["generationConfig"].(map[string]any)
	thinkingConfig, _ := generation["thinkingConfig"].(map[string]any)
	if thinkingConfig["thinkingBudget"] != 4000 {
		t.Fatalf("thinkingBudget = %#v", thinkingConfig)
	}
}

func TestApplyGeminiSkipsWhenClientAskedNothing(t *testing.T) {
	// A model ID ending in "-high" is a real upstream ID here, so an empty config
	// must not add a thinkingConfig or touch the model name.
	body := map[string]any{"model": "gemini-3.8-flash-high"}
	Apply(ProviderGemini, "gemini-3.8-flash-high", body, Config{})
	if _, exists := body["generationConfig"]; exists {
		t.Fatalf("generationConfig should stay absent: %#v", body["generationConfig"])
	}
	if body["model"] != "gemini-3.8-flash-high" {
		t.Fatalf("model renamed: %#v", body["model"])
	}
}

func TestApplyXAIClampsEffort(t *testing.T) {
	body := map[string]any{}
	Apply(ProviderXAI, "grok-4.5", body, Config{Effort: "xhigh", Set: true})
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" {
		t.Fatalf("effort = %#v, want the clamp midpoint", reasoning["effort"])
	}
}

func TestApplyDefaultWritesFlatEffort(t *testing.T) {
	body := map[string]any{}
	Apply("openai-compatible", "gpt-5.5", body, Config{Effort: "high", Set: true})
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v", body["reasoning_effort"])
	}
	if _, exists := body["reasoning"]; exists {
		t.Fatalf("Chat Completions must not receive a reasoning object: %#v", body)
	}
}

func TestApplyDefaultSkipsEmptyEffort(t *testing.T) {
	body := map[string]any{}
	Apply("openai-compatible", "gpt-5.5", body, Config{BudgetTokens: 4000, Set: true})
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatalf("a budget alone is not an effort label: %#v", body)
	}
}

func TestApplyIgnoresNilBody(t *testing.T) {
	if Apply(ProviderCodex, "gpt-5.6-sol", nil, Config{Effort: "high", Set: true}) {
		t.Fatal("Apply must not report a change for a nil body")
	}
}
