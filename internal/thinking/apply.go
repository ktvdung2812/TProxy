package thinking

import (
	"strings"

	"github.com/tproxy/tproxy/internal/bridge"
)

// Provider identifiers Apply understands. They are the store.Provider.Type
// values, plus "openai-compatible" for everything that speaks bare Chat
// Completions.
const (
	ProviderCodex       = "codex"
	ProviderClaude      = "claude"
	ProviderGemini      = "gemini"
	ProviderAntigravity = "antigravity"
	ProviderXAI         = "xai"
)

// Apply writes cfg into body using the wire shape providerType expects and
// reports whether it changed anything. body may be nil, in which case nothing
// happens.
//
// Callers apply their own provider quirks afterwards: GLM deletes
// reasoning_effort, xAI's Chat Completions path deletes it too.
func Apply(providerType, upstreamModel string, body map[string]any, cfg Config) bool {
	if body == nil {
		return false
	}
	switch providerType {
	case ProviderCodex:
		return applyCodex(body, cfg)
	case ProviderClaude:
		return applyClaude(body, cfg)
	case ProviderGemini, ProviderAntigravity:
		return applyGemini(body, cfg)
	case ProviderXAI:
		return applyXAI(body, cfg)
	default:
		return applyEffortOnly(body, cfg)
	}
}

// applyCodex writes the Responses reasoning object. An explicit effort wins; no
// effort at all keeps the historical Codex default of "low"; "none" removes
// reasoning (and its include entry) entirely.
func applyCodex(body map[string]any, cfg Config) bool {
	delete(body, "reasoning_effort")
	effort := cfg.Effort
	if effort == "" {
		if reasoning, ok := body["reasoning"].(map[string]any); ok {
			if existing := NormalizeEffort(stringValue(reasoning["effort"])); existing != "" {
				effort = existing
			}
		}
	}
	if effort == "" {
		effort = "low"
	}
	if effort == "none" {
		delete(body, "reasoning")
		delete(body, "include")
		return true
	}
	body["reasoning"] = map[string]any{"effort": bridge.CodexWireReasoningEffort(effort), "summary": "auto"}
	body["include"] = []any{"reasoning.encrypted_content"}
	return true
}

// applyClaude writes Anthropic's thinking block. A budget is required to ask for
// thinking; an effort-only choice is left to the caller's translator, which
// already turns budgets into effort for OpenAI-shaped targets.
func applyClaude(body map[string]any, cfg Config) bool {
	if cfg.Effort == "none" {
		delete(body, "thinking")
		return true
	}
	if cfg.BudgetTokens <= 0 {
		return false
	}
	body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": cfg.BudgetTokens}
	return true
}

// applyGemini writes generationConfig.thinkingConfig only when the client asked
// for a budget. It never renames the model: in this repo a "-high" suffix is a
// real upstream model ID, not a reasoning level.
func applyGemini(body map[string]any, cfg Config) bool {
	if cfg.BudgetTokens <= 0 {
		return false
	}
	generation, ok := body["generationConfig"].(map[string]any)
	if !ok || generation == nil {
		generation = map[string]any{}
		body["generationConfig"] = generation
	}
	thinking, _ := generation["thinkingConfig"].(map[string]any)
	if thinking == nil {
		thinking = map[string]any{}
	}
	thinking["thinkingBudget"] = cfg.BudgetTokens
	generation["thinkingConfig"] = thinking
	return true
}

// applyXAI writes xAI's reasoning object, which accepts only low|medium|high.
func applyXAI(body map[string]any, cfg Config) bool {
	if cfg.Effort == "" {
		return false
	}
	if cfg.Effort == "none" {
		delete(body, "reasoning")
		return true
	}
	body["reasoning"] = map[string]any{"effort": XAIWireReasoningEffort(cfg.Effort)}
	return true
}

// applyEffortOnly is the OpenAI-compatible default: a flat reasoning_effort.
func applyEffortOnly(body map[string]any, cfg Config) bool {
	if cfg.Effort == "" {
		return false
	}
	body["reasoning_effort"] = cfg.Effort
	return true
}

// XAIWireReasoningEffort clamps an effort label to the three xAI accepts.
func XAIWireReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return "medium"
	}
}
