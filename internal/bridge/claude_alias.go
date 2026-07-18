package bridge

import (
	"strings"
	"sync"
)

const AppSettingClaudeAliases = "claude.alias_models"

type Role string

const (
	RoleOpus   Role = "opus"
	RoleSonnet Role = "sonnet"
	RoleHaiku  Role = "haiku"
	RoleFable  Role = "fable"
)

var Roles = []Role{RoleFable, RoleOpus, RoleSonnet, RoleHaiku}

type Config struct {
	Opus                 string `yaml:"opus" json:"opus"`
	Sonnet               string `yaml:"sonnet" json:"sonnet"`
	Haiku                string `yaml:"haiku" json:"haiku"`
	Fable                string `yaml:"fable" json:"fable"`
	DefaultCodexProvider string `yaml:"default-codex-provider" json:"default_codex_provider"`
}

type Overrides map[Role]string

type Resolver struct {
	mu                  sync.RWMutex
	defaults            Config
	env                 Overrides
	overrides           Overrides
	placeholderToTarget map[string]string
}

var defaultRoleTargets = Config{
	Opus:   "gpt-5.5",
	Sonnet: "gpt-5.4",
	Haiku:  "gpt-5.1-codex-mini",
	Fable:  "gpt-5.6-luna",
}

var placeholderToRole = map[string]Role{
	"default":       RoleSonnet,
	"opus":          RoleOpus,
	"sonnet":        RoleSonnet,
	"haiku":         RoleHaiku,
	"fable":         RoleFable,
	"opusplan":      RoleOpus,
	"claude-opus":   RoleOpus,
	"claude-sonnet": RoleSonnet,
	"claude-haiku":  RoleHaiku,
	"claude-fable":  RoleFable,
}

func NewResolver(cfg Config) *Resolver {
	if strings.TrimSpace(cfg.Opus) == "" {
		cfg.Opus = defaultRoleTargets.Opus
	}
	if strings.TrimSpace(cfg.Sonnet) == "" {
		cfg.Sonnet = defaultRoleTargets.Sonnet
	}
	if strings.TrimSpace(cfg.Haiku) == "" {
		cfg.Haiku = defaultRoleTargets.Haiku
	}
	if strings.TrimSpace(cfg.Fable) == "" {
		cfg.Fable = defaultRoleTargets.Fable
	}
	return &Resolver{defaults: cfg, env: EnvOverrides(), overrides: Overrides{}, placeholderToTarget: buildPlaceholderIndex(cfg, EnvOverrides(), Overrides{})}
}

func (r *Resolver) SetConfig(cfg Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(cfg.Opus) != "" {
		r.defaults.Opus = strings.TrimSpace(cfg.Opus)
	}
	if strings.TrimSpace(cfg.Sonnet) != "" {
		r.defaults.Sonnet = strings.TrimSpace(cfg.Sonnet)
	}
	if strings.TrimSpace(cfg.Haiku) != "" {
		r.defaults.Haiku = strings.TrimSpace(cfg.Haiku)
	}
	if strings.TrimSpace(cfg.Fable) != "" {
		r.defaults.Fable = strings.TrimSpace(cfg.Fable)
	}
	if strings.TrimSpace(cfg.DefaultCodexProvider) != "" {
		r.defaults.DefaultCodexProvider = strings.TrimSpace(cfg.DefaultCodexProvider)
	}
	r.placeholderToTarget = buildPlaceholderIndex(r.defaults, r.env, r.overrides)
}

func (r *Resolver) SetEnvOverrides(env Overrides) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.env = cloneOverrides(env)
	r.placeholderToTarget = buildPlaceholderIndex(r.defaults, r.env, r.overrides)
}

func (r *Resolver) SetOverrides(overrides Overrides) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.overrides = cloneOverrides(overrides)
	r.placeholderToTarget = buildPlaceholderIndex(r.defaults, r.env, r.overrides)
}

func (r *Resolver) EnvOverrides() Overrides {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneOverrides(r.env)
}

func (r *Resolver) EffectiveResolvedMapping() map[string]map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]map[string]string, len(Roles))
	for _, role := range Roles {
		raw := r.roleTargetLocked(role)
		resolved := r.formatRoleTarget(raw, r.defaults.DefaultCodexProvider)
		entry := map[string]string{
			"raw":      raw,
			"resolved": resolved,
			"route":    "codex-bridge",
		}
		if classified, ok := ClassifyAliasTarget(raw); ok {
			switch classified.Kind {
			case TargetKindClaude:
				entry["route"] = "claude-native"
			case TargetKindVirtual:
				entry["route"] = "virtual-model"
			}
		}
		out[string(role)] = entry
	}
	return out
}

func (r *Resolver) EffectiveMapping() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return map[string]string{
		string(RoleFable):  r.roleTargetLocked(RoleFable),
		string(RoleOpus):   r.roleTargetLocked(RoleOpus),
		string(RoleSonnet): r.roleTargetLocked(RoleSonnet),
		string(RoleHaiku):  r.roleTargetLocked(RoleHaiku),
	}
}

func (r *Resolver) Defaults() Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaults
}

func (r *Resolver) CurrentOverrides() Overrides {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneOverrides(r.overrides)
}

func (r *Resolver) ResolveModel(requested string) string {
	normalized := NormalizeModel(requested)
	if normalized == "" {
		return requested
	}
	role, ok := PlaceholderRole(normalized)
	if !ok {
		if provider, upstream, hasPrefix := parseSlashProviderAlias(normalized); hasPrefix {
			if _, isAnthropic := anthropicProviderAliases[provider]; isAnthropic {
				role, ok = PlaceholderRole(upstream)
			}
		}
	}
	if !ok {
		return requested
	}
	r.mu.RLock()
	target := r.roleTargetLocked(role)
	defaultCodex := r.defaults.DefaultCodexProvider
	r.mu.RUnlock()
	return r.formatRoleTarget(target, defaultCodex)
}

func NormalizeModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, "["); idx > 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	return strings.ToLower(trimmed)
}

func PlaceholderRole(model string) (Role, bool) {
	normalized := NormalizeModel(model)
	if normalized == "" {
		return "", false
	}
	if role, ok := placeholderToRole[normalized]; ok {
		return role, true
	}
	if provider, upstream, ok := parseSlashProviderAlias(normalized); ok {
		if _, isAnthropic := anthropicProviderAliases[provider]; isAnthropic {
			if role, ok := placeholderToRole[upstream]; ok {
				return role, true
			}
		}
	}
	return "", false
}

func PlaceholderNames() []string {
	return []string{
		"default",
		"fable",
		"claude-fable",
		"opus",
		"opusplan",
		"claude-opus",
		"sonnet",
		"claude-sonnet",
		"haiku",
		"claude-haiku",
	}
}

func FormatTarget(target, defaultCodexProvider string) string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return trimmed
	}
	if strings.Contains(trimmed, "::") || strings.Contains(trimmed, ":") || strings.Contains(trimmed, "/") {
		return normalizeProviderSelector(trimmed)
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4") || strings.HasPrefix(lower, "chatgpt-") {
		provider := strings.TrimSpace(defaultCodexProvider)
		if provider == "" {
			provider = "codex"
		}
		return provider + ":" + trimmed
	}
	return trimmed
}

func normalizeProviderSelector(value string) string {
	if strings.Contains(value, "::") {
		parts := strings.SplitN(value, "::", 2)
		return strings.TrimSpace(parts[0]) + ":" + strings.TrimSpace(parts[1])
	}
	if strings.Contains(value, "/") {
		parts := strings.SplitN(value, "/", 2)
		return strings.TrimSpace(parts[0]) + ":" + strings.TrimSpace(parts[1])
	}
	return value
}

func (r *Resolver) roleTarget(role Role) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.roleTargetLocked(role)
}

func (r *Resolver) roleTargetLocked(role Role) string {
	if value := strings.TrimSpace(r.overrides[role]); value != "" {
		return value
	}
	if value := strings.TrimSpace(r.env[role]); value != "" {
		return value
	}
	switch role {
	case RoleOpus:
		return r.defaults.Opus
	case RoleHaiku:
		return r.defaults.Haiku
	case RoleFable:
		return r.defaults.Fable
	default:
		return r.defaults.Sonnet
	}
}

func buildPlaceholderIndex(cfg Config, env, overrides Overrides) map[string]string {
	resolver := &Resolver{defaults: cfg, env: env, overrides: overrides}
	index := make(map[string]string, len(placeholderToRole))
	for name, role := range placeholderToRole {
		index[name] = resolver.formatRoleTarget(resolver.roleTargetLocked(role), cfg.DefaultCodexProvider)
	}
	return index
}

func cloneOverrides(overrides Overrides) Overrides {
	if len(overrides) == 0 {
		return Overrides{}
	}
	out := make(Overrides, len(overrides))
	for role, value := range overrides {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out[role] = strings.TrimSpace(value)
	}
	return out
}
