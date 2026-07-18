package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/bridge"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

func bridgeConfigFromYAML(cfg config.ClaudeAliasConfig) bridge.Config {
	return bridge.Config{
		Opus:                 cfg.Opus,
		Sonnet:               cfg.Sonnet,
		Haiku:                cfg.Haiku,
		Fable:                cfg.Fable,
		DefaultCodexProvider: cfg.DefaultCodexProvider,
	}
}

func (s *Server) loadClaudeAliasResolver() {
	resolver := bridge.NewResolver(bridgeConfigFromYAML(s.cfg.ClaudeAliases))
	resolver.SetEnvOverrides(bridge.EnvOverrides())
	if overrides, err := s.store.ClaudeAliasSettings(context.Background()); err == nil {
		resolver.SetOverrides(overrides.Models)
		resolver.SetReasoningEffortOverrides(overrides.ReasoningEffort)
	}
	s.claudeAliases = resolver
}

func (s *Server) adminClaudeMapping(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.adminGetClaudeMapping(w, r)
	case http.MethodPut:
		s.adminPutClaudeMapping(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or PUT required", useClientRequestID(r))
	}
}

func (s *Server) adminGetClaudeMapping(w http.ResponseWriter, r *http.Request) {
	resolver := s.claudeAliasResolver()
	defaults := resolver.Defaults()
	overrides := resolver.CurrentOverrides()
	reasoningEffort := resolver.CurrentReasoningEffortOverrides()
	effective := resolver.EffectiveMapping()
	effectiveResolved := resolver.EffectiveResolvedMapping()
	placeholders := make([]map[string]any, 0, len(bridge.PlaceholderNames()))
	for _, name := range bridge.PlaceholderNames() {
		role, ok := bridge.PlaceholderRole(name)
		if !ok {
			continue
		}
		resolved := effectiveResolved[string(role)]["resolved"]
		if resolved == "" {
			resolved = bridge.FormatTarget(effective[string(role)], defaults.DefaultCodexProvider)
		}
		placeholders = append(placeholders, map[string]any{
			"name":     name,
			"role":     role,
			"resolves": resolved,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"defaults": map[string]string{
			string(bridge.RoleFable):  defaults.Fable,
			string(bridge.RoleOpus):   defaults.Opus,
			string(bridge.RoleSonnet): defaults.Sonnet,
			string(bridge.RoleHaiku):  defaults.Haiku,
		},
		"env_defaults": envOverridesMap(resolver.EnvOverrides()),
		"overrides": map[string]string{
			string(bridge.RoleFable):  overrides[bridge.RoleFable],
			string(bridge.RoleOpus):   overrides[bridge.RoleOpus],
			string(bridge.RoleSonnet): overrides[bridge.RoleSonnet],
			string(bridge.RoleHaiku):  overrides[bridge.RoleHaiku],
		},
		"reasoning_effort_overrides": reasoningEffortMap(reasoningEffort),
		"effective_reasoning_effort": resolver.EffectiveReasoningEffortMapping(),
		"effective":              effective,
		"effective_resolved":       resolver.EffectiveResolvedMapping(),
		"default_codex_provider":   defaults.DefaultCodexProvider,
		"placeholders":             placeholders,
		"content_mapping":          contentMappingSummary(),
	})
}

func (s *Server) adminPutClaudeMapping(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Overrides                map[string]string `json:"overrides"`
		ReasoningEffortOverrides map[string]string `json:"reasoning_effort_overrides"`
		DefaultCodexProvider     string            `json:"default_codex_provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	overrides := bridge.Overrides{}
	for _, role := range bridge.Roles {
		if value, ok := payload.Overrides[string(role)]; ok {
			overrides[role] = strings.TrimSpace(value)
		}
	}
	reasoningEffort := bridge.ReasoningEffortOverrides{}
	for _, role := range bridge.Roles {
		if value, ok := payload.ReasoningEffortOverrides[string(role)]; ok {
			if normalized := bridge.NormalizeReasoningEffort(value); normalized != "" {
				reasoningEffort[role] = normalized
			}
		}
	}
	if err := s.store.SaveClaudeAliasSettings(r.Context(), store.ClaudeAliasSettings{
		Models:          overrides,
		ReasoningEffort: reasoningEffort,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	resolver := s.claudeAliasResolver()
	resolver.SetOverrides(overrides)
	resolver.SetReasoningEffortOverrides(reasoningEffort)
	if strings.TrimSpace(payload.DefaultCodexProvider) != "" {
		cfg := resolver.Defaults()
		cfg.DefaultCodexProvider = strings.TrimSpace(payload.DefaultCodexProvider)
		resolver.SetConfig(cfg)
	}
	s.adminGetClaudeMapping(w, r)
}

func reasoningEffortMap(layer bridge.ReasoningEffortOverrides) map[string]string {
	out := map[string]string{}
	for _, role := range bridge.Roles {
		if value := bridge.NormalizeReasoningEffort(layer[role]); value != "" {
			out[string(role)] = value
		}
	}
	return out
}

func envOverridesMap(layer bridge.Overrides) map[string]string {
	out := map[string]string{}
	for _, role := range bridge.Roles {
		if value := strings.TrimSpace(layer[role]); value != "" {
			out[string(role)] = value
		}
	}
	return out
}

func (s *Server) claudeAliasResolver() *bridge.Resolver {
	if s.claudeAliases == nil {
		s.loadClaudeAliasResolver()
	}
	return s.claudeAliases
}

func (s *Server) resolveClaudeIngressModel(r *http.Request, model string) string {
	resolved := resolveIngressModel(r, model)
	return s.claudeAliasResolver().ResolveModel(resolved)
}

func contentMappingSummary() map[string]any {
	return map[string]any{
		"codex": map[string]any{
			"text":        "text -> input_text / output_text",
			"tools":       "OpenAI tools -> Codex function schema (64-char names)",
			"responses":   "Responses SSE emits tool call lifecycle events",
		},
		"claude": map[string]any{
			"tools":       "tool_use <-> tool_calls",
			"reasoning":   "thinking <-> reasoning_content",
			"stop_reason": "finish_reason mapped on stream and JSON responses",
		},
	}
}
