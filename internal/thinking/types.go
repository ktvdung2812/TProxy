// Package thinking owns the single reasoning/thinking contract every provider
// adapter reads.
//
// Before this package each adapter guessed the client's intent from a different
// place: Codex merged request.Reasoning, a top-level reasoning_effort and a
// hard-coded default; xAI clamped the effort on its own; Gemini only looked at
// generationConfig; Kiro and Cursor re-read reasoning_effort from a body they
// had just built. Normalize resolves that once per request and Apply is the only
// place that turns the result into upstream JSON.
package thinking

import (
	"fmt"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

// MetadataNormalized marks a request whose reasoning has already been resolved,
// so a fallback attempt reuses the same decision instead of re-deriving it.
const MetadataNormalized = "thinking_normalized"

// MetadataSet preserves an explicit enabled/adaptive choice that has no
// effort or budget value to store in Request.Reasoning.
const MetadataSet = "thinking_set"

// MetadataAliasEffort carries the Claude-tier effort an alias contributes when
// the client itself expressed no preference. It is a hint, not a choice: every
// client source still wins over it.
const MetadataAliasEffort = "thinking_alias_effort"

// Config is the resolved thinking choice for one request.
type Config struct {
	// Effort is one of none|minimal|low|medium|high|xhigh|max. Empty means no
	// level was chosen.
	Effort string
	// BudgetTokens is an explicit thinking budget. 0 means unset.
	BudgetTokens int
	// Set reports whether the client (or a Claude alias) chose anything at all.
	Set bool
}

// EffortLevels are the labels every adapter understands. Wire-format quirks
// (Codex and xAI clamping, Gemini budget fields) stay inside Apply.
var EffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// NormalizeEffort lowercases and trims an effort label, returning "" for
// "default", "inherit" and anything unrecognised. An unknown label must not
// reach an upstream as a made-up level.
func NormalizeEffort(effort string) string {
	switch trimmed := strings.TrimSpace(strings.ToLower(effort)); trimmed {
	case "", "default", "inherit":
		return ""
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return trimmed
	default:
		return ""
	}
}

// EffortForBudget maps an Anthropic thinking budget onto an effort label,
// mirroring the Claude→OpenAI translation so a Codex target sees the level it
// saw when that translation filled reasoning_effort itself.
func EffortForBudget(budget int) string {
	switch {
	case budget <= 0:
		return ""
	case budget < 8000:
		return "medium"
	case budget < 24000:
		return "high"
	default:
		return "xhigh"
	}
}

// Resolved reports whether request already carries a normalized config.
func Resolved(request canonical.Request) bool {
	value, _ := request.Metadata[MetadataNormalized].(bool)
	return value
}

// FromSources resolves the config a request carries, reading every place a
// client can express a choice. Precedence, highest first:
//
//	request.Reasoning effort/budget
//	Raw.reasoning.effort
//	Raw.reasoning_effort
//	Raw.thinking (enabled/adaptive/disabled, or a budget)
//	Raw.generationConfig.thinkingConfig
//	Metadata alias effort
//
// An already normalized request short-circuits, so a fallback attempt inherits
// the first decision instead of re-deriving one.
func FromSources(request canonical.Request) Config {
	if Resolved(request) {
		cfg := fromReasoning(request.Reasoning)
		if set, _ := request.Metadata[MetadataSet].(bool); set {
			cfg.Set = true
		}
		return cfg
	}
	if cfg := fromReasoning(request.Reasoning); cfg.Set {
		return cfg
	}
	if cfg := fromRaw(request.Raw); cfg.Set {
		return cfg
	}
	if effort := NormalizeEffort(metadataString(request, MetadataAliasEffort)); effort != "" {
		return Config{Effort: effort, Set: true}
	}
	return Config{}
}

func fromReasoning(reasoning map[string]any) Config {
	if len(reasoning) == 0 {
		return Config{}
	}
	cfg := Config{
		Effort:       NormalizeEffort(stringValue(reasoning["effort"])),
		BudgetTokens: positiveInt(reasoning["budget_tokens"]),
	}
	cfg.Set = cfg.Effort != "" || cfg.BudgetTokens > 0
	if cfg.Effort == "" && cfg.BudgetTokens > 0 {
		cfg.Effort = EffortForBudget(cfg.BudgetTokens)
	}
	return cfg
}

// FromRaw resolves a Config from a raw request body only. Aliases are not
// consulted: callers use it to ask "did the client itself choose a level?".
func FromRaw(raw map[string]any) Config {
	return fromRaw(raw)
}

func fromRaw(raw map[string]any) Config {
	if len(raw) == 0 {
		return Config{}
	}
	if reasoning, ok := raw["reasoning"].(map[string]any); ok {
		if cfg := fromReasoning(reasoning); cfg.Set {
			return cfg
		}
	}
	if effort := NormalizeEffort(stringValue(raw["reasoning_effort"])); effort != "" {
		return Config{Effort: effort, Set: true}
	}
	if cfg := fromThinking(raw["thinking"]); cfg.Set {
		return cfg
	}
	if config, ok := raw["generationConfig"].(map[string]any); ok {
		return fromThinkingConfig(config["thinkingConfig"])
	}
	return Config{}
}

// fromThinking reads Anthropic's thinking block. "enabled" without a budget
// still counts as a choice, so an alias effort cannot silently replace it.
func fromThinking(value any) Config {
	switch typed := value.(type) {
	case bool:
		if typed {
			return Config{Set: true}
		}
	case map[string]any:
		budget := positiveInt(typed["budget_tokens"])
		switch strings.TrimSpace(strings.ToLower(stringValue(typed["type"]))) {
		case "enabled", "adaptive":
			cfg := Config{BudgetTokens: budget, Set: true}
			if budget > 0 {
				cfg.Effort = EffortForBudget(budget)
			}
			return cfg
		case "disabled":
			return Config{Effort: "none", Set: true}
		}
		if budget > 0 {
			return Config{BudgetTokens: budget, Effort: EffortForBudget(budget), Set: true}
		}
	}
	return Config{}
}

// fromThinkingConfig reads Gemini's generationConfig.thinkingConfig.
func fromThinkingConfig(value any) Config {
	config, ok := value.(map[string]any)
	if !ok {
		return Config{}
	}
	budget := positiveInt(firstValue(config, "thinkingBudget", "thinking_budget"))
	if budget <= 0 {
		return Config{}
	}
	return Config{BudgetTokens: budget, Effort: EffortForBudget(budget), Set: true}
}

func metadataString(request canonical.Request, key string) string {
	value, _ := request.Metadata[key].(string)
	return value
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, exists := values[key]; exists && value != nil {
			return value
		}
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func positiveInt(value any) int {
	switch typed := value.(type) {
	case int:
		return max(typed, 0)
	case int32:
		return max(int(typed), 0)
	case int64:
		return max(int(typed), 0)
	case float32:
		return max(int(typed), 0)
	case float64:
		return max(int(typed), 0)
	case string:
		trimmed := strings.TrimSpace(typed)
		parsed := 0
		for _, digit := range trimmed {
			if digit < '0' || digit > '9' {
				return 0
			}
			parsed = parsed*10 + int(digit-'0')
		}
		return parsed
	default:
		return 0
	}
}
