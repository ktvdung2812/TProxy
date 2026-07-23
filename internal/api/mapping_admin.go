package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/bridge"
	"github.com/tproxy/tproxy/internal/canonical"
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
		"effective":            effective,
		"effective_resolved":   resolver.EffectiveResolvedMapping(),
		"placeholders":         placeholders,
		"content_mapping":          contentMappingSummary(),
	})
}

func (s *Server) adminPutClaudeMapping(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Overrides                map[string]string `json:"overrides"`
		ReasoningEffortOverrides map[string]string `json:"reasoning_effort_overrides"`
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

func placeholderDisplayName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "default":
		return "Default"
	case "gpt-sol":
		return "GPT Sol"
	case "gpt-terra":
		return "GPT Terra"
	case "gpt-luna":
		return "GPT Luna"
	case "claude-fable":
		return "Claude Fable"
	case "claude-opus":
		return "Claude Opus"
	case "claude-sonnet":
		return "Claude Sonnet"
	case "claude-haiku":
		return "Claude Haiku"
	case "fable":
		return "Fable"
	case "opus":
		return "Opus"
	case "opusplan":
		return "Opus Plan"
	case "sonnet":
		return "Sonnet"
	case "haiku":
		return "Haiku"
	default:
		return name
	}
}

func (s *Server) placeholderModelCatalog() []map[string]any {
	resolver := s.claudeAliasResolver()
	defaults := resolver.Defaults()
	effective := resolver.EffectiveMapping()
	effectiveResolved := resolver.EffectiveResolvedMapping()
	entries := make([]map[string]any, 0, len(bridge.CatalogPlaceholderNames()))
	for _, name := range bridge.CatalogPlaceholderNames() {
		role, ok := bridge.PlaceholderRole(name)
		if !ok {
			continue
		}
		resolved := ""
		route := ""
		if entry, ok := effectiveResolved[string(role)]; ok {
			resolved = entry["resolved"]
			route = entry["route"]
		}
		if resolved == "" {
			resolved = bridge.FormatTarget(effective[string(role)], defaults.DefaultCodexProvider)
		}
		item := map[string]any{
			"id":          name,
			"object":      "model",
			"name":        placeholderDisplayName(name),
			"owned_by":    "tproxy",
			"placeholder": true,
			"role":        string(role),
			"resolves_to": resolved,
			"endpoint":    "placeholder",
			"created":     time.Now().Unix(),
		}
		if route != "" {
			item["route"] = route
		}
		entries = append(entries, item)
	}
	return entries
}

func (s *Server) placeholderModelInfo(id string) (map[string]any, bool) {
	normalized := bridge.NormalizeModel(id)
	if normalized == "" {
		return nil, false
	}
	if _, ok := bridge.PlaceholderRole(normalized); !ok {
		return nil, false
	}
	for _, entry := range s.placeholderModelCatalog() {
		entryID, _ := entry["id"].(string)
		if entryID != normalized {
			continue
		}
		info := make(map[string]any, len(entry)+1)
		for key, value := range entry {
			info[key] = value
		}
		info["supported_parameters"] = []string{"stream", "temperature", "max_tokens", "tools"}
		return info, true
	}
	return nil, false
}

func (s *Server) resolveClaudeIngressModel(r *http.Request, model string) string {
	resolved := resolveIngressModel(r, model)
	return s.claudeAliasResolver().ResolveModel(resolved)
}

func (s *Server) applyMappingIngress(r *http.Request, request *canonical.Request) {
	preserveClaudeClientModel(request)
	request.PublicModelID = s.resolveClaudeIngressModel(r, request.PublicModelID)
	s.applyClaudeTierReasoningEffort(request)
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
