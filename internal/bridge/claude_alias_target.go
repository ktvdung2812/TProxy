package bridge

import (
	"os"
	"strings"
)

type TargetKind string

const (
	TargetKindChatGPT  TargetKind = "chatgpt"
	TargetKindClaude   TargetKind = "claude"
	TargetKindVirtual  TargetKind = "virtual"
)

type AliasTarget struct {
	Kind  TargetKind
	Model string
}

var anthropicProviderAliases = map[string]struct{}{
	"anthropic": {},
	"claude":    {},
}

func IsOpenAIChatModel(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(lower, "gpt-") ||
		strings.HasPrefix(lower, "o1") ||
		strings.HasPrefix(lower, "o3") ||
		strings.HasPrefix(lower, "o4") ||
		strings.HasPrefix(lower, "chatgpt-")
}

func isPlaceholderName(model string) bool {
	_, ok := placeholderToRole[strings.ToLower(strings.TrimSpace(model))]
	return ok
}

func IsRealClaudeModel(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(lower, "claude-") && !isPlaceholderName(lower)
}

func isVirtualModelID(model string) bool {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" || isPlaceholderName(strings.ToLower(trimmed)) {
		return false
	}
	if IsOpenAIChatModel(trimmed) || IsRealClaudeModel(trimmed) {
		return false
	}
	if strings.Contains(trimmed, ":") || strings.Contains(trimmed, "/") {
		return false
	}
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func parseSlashProviderAlias(model string) (provider, upstream string, ok bool) {
	slashIndex := strings.Index(model, "/")
	if slashIndex <= 0 {
		return "", "", false
	}
	provider = strings.ToLower(strings.TrimSpace(model[:slashIndex]))
	upstream = strings.TrimSpace(model[slashIndex+1:])
	if provider == "" || upstream == "" {
		return "", "", false
	}
	return provider, upstream, true
}

// ClassifyAliasTarget validates a configured mapping target.
// Placeholder names cannot be targets. GPT models bridge via Codex; real Claude
// models route natively to an Anthropic-compatible provider.
func ClassifyAliasTarget(value string) (AliasTarget, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return AliasTarget{}, false
	}
	normalized := NormalizeModel(trimmed)
	if normalized == "" {
		return AliasTarget{}, false
	}

	if provider, upstream, ok := parseSlashProviderAlias(normalized); ok {
		upstream = NormalizeModel(upstream)
		if IsOpenAIChatModel(upstream) {
			return AliasTarget{Kind: TargetKindChatGPT, Model: strings.ToLower(upstream)}, true
		}
		if _, isAnthropic := anthropicProviderAliases[provider]; isAnthropic {
			if IsRealClaudeModel(upstream) {
				return AliasTarget{Kind: TargetKindClaude, Model: strings.ToLower(upstream)}, true
			}
			return AliasTarget{}, false
		}
		return AliasTarget{}, false
	}

	if strings.Contains(trimmed, ":") || strings.Contains(trimmed, "::") {
		selector := normalizeProviderSelector(trimmed)
		parts := strings.SplitN(selector, ":", 2)
		if len(parts) == 2 {
			upstream := NormalizeModel(parts[1])
			if IsOpenAIChatModel(upstream) {
				return AliasTarget{Kind: TargetKindChatGPT, Model: strings.ToLower(upstream)}, true
			}
			provider := strings.ToLower(strings.TrimSpace(parts[0]))
			if _, isAnthropic := anthropicProviderAliases[provider]; isAnthropic && IsRealClaudeModel(upstream) {
				return AliasTarget{Kind: TargetKindClaude, Model: strings.ToLower(upstream)}, true
			}
		}
	}

	lower := strings.ToLower(normalized)
	if isPlaceholderName(lower) {
		return AliasTarget{}, false
	}
	if IsOpenAIChatModel(lower) {
		return AliasTarget{Kind: TargetKindChatGPT, Model: lower}, true
	}
	if IsRealClaudeModel(lower) {
		return AliasTarget{Kind: TargetKindClaude, Model: lower}, true
	}
	if isVirtualModelID(trimmed) {
		return AliasTarget{Kind: TargetKindVirtual, Model: trimmed}, true
	}
	return AliasTarget{}, false
}

// IsClaudePlaceholder reports whether the client sent a virtual tier placeholder
// (Claude names like sonnet/fable or GPT codenames like gpt-sol/gpt-terra).
func IsClaudePlaceholder(model string) bool {
	if _, ok := PlaceholderRole(model); ok {
		return true
	}
	normalized := NormalizeModel(model)
	if provider, upstream, ok := parseSlashProviderAlias(normalized); ok {
		if _, isAnthropic := anthropicProviderAliases[provider]; isAnthropic {
			return isPlaceholderName(upstream)
		}
	}
	return false
}

func EnvOverrides() Overrides {
	layer := Overrides{}
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_DEFAULT_FABLE_MODEL")); value != "" {
		layer[RoleFable] = value
	}
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_DEFAULT_OPUS_MODEL")); value != "" {
		layer[RoleOpus] = value
	}
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_DEFAULT_SONNET_MODEL")); value != "" {
		layer[RoleSonnet] = value
	}
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_DEFAULT_HAIKU_MODEL")); value != "" {
		layer[RoleHaiku] = value
	}
	return layer
}

func EnvOverridesMap() map[string]string {
	layer := EnvOverrides()
	out := map[string]string{}
	for _, role := range Roles {
		if value := strings.TrimSpace(layer[role]); value != "" {
			out[string(role)] = value
		}
	}
	return out
}

func (r *Resolver) formatRoleTarget(target, defaultCodexProvider string) string {
	trimmed := strings.TrimSpace(target)
	if classified, ok := ClassifyAliasTarget(trimmed); ok {
		switch classified.Kind {
		case TargetKindClaude, TargetKindVirtual:
			return classified.Model
		case TargetKindChatGPT:
			if strings.Contains(trimmed, ":") || strings.Contains(trimmed, "::") || strings.Contains(trimmed, "/") {
				return normalizeProviderSelector(trimmed)
			}
			return FormatTarget(classified.Model, defaultCodexProvider)
		}
	}
	return FormatTarget(trimmed, defaultCodexProvider)
}
