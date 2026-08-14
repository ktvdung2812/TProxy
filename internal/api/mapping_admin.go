package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/bridge"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	cursorpkg "github.com/tproxy/tproxy/internal/providers/cursor"
	"github.com/tproxy/tproxy/internal/store"
)

func bridgeConfigFromYAML(cfg config.ClaudeAliasConfig) bridge.Config {
	return bridge.Config{
		Default:              cfg.Default,
		Opus:                 cfg.Opus,
		Sonnet:               cfg.Sonnet,
		Haiku:                cfg.Haiku,
		Fable:                cfg.Fable,
		DefaultCodexProvider: cfg.DefaultCodexProvider,
	}
}

func (s *Server) loadClaudeAliasResolver() {
	resolver := bridge.NewResolver(bridgeConfigFromYAML(s.currentConfig().ClaudeAliases))
	resolver.SetEnvOverrides(bridge.EnvOverrides())
	if overrides, err := s.store.ClaudeAliasSettings(context.Background()); err == nil {
		resolver.SetOverrides(overrides.Models)
		resolver.SetReasoningEffortOverrides(overrides.ReasoningEffort)
	}
	s.claudeAliases = resolver
}

func (s *Server) loadCursorAliasResolver() {
	resolver := bridge.NewCursorResolver()
	if settings, err := s.store.CursorAliasSettings(context.Background()); err == nil {
		resolver.SetAliases(settings.Models)
	}
	s.cursorAliases = resolver
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
			string(bridge.RoleDefault): defaults.Default,
			string(bridge.RoleFable):   defaults.Fable,
			string(bridge.RoleOpus):    defaults.Opus,
			string(bridge.RoleSonnet):  defaults.Sonnet,
			string(bridge.RoleHaiku):   defaults.Haiku,
		},
		"env_defaults": envOverridesMap(resolver.EnvOverrides()),
		"overrides": map[string]string{
			string(bridge.RoleDefault): overrides[bridge.RoleDefault],
			string(bridge.RoleFable):   overrides[bridge.RoleFable],
			string(bridge.RoleOpus):    overrides[bridge.RoleOpus],
			string(bridge.RoleSonnet):  overrides[bridge.RoleSonnet],
			string(bridge.RoleHaiku):   overrides[bridge.RoleHaiku],
		},
		"reasoning_effort_overrides": reasoningEffortMap(reasoningEffort),
		"effective_reasoning_effort": resolver.EffectiveReasoningEffortMapping(),
		"effective":                  effective,
		"effective_resolved":         resolver.EffectiveResolvedMapping(),
		"placeholders":               placeholders,
		"content_mapping":            contentMappingSummary(),
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

func (s *Server) cursorAliasResolver() *bridge.CursorResolver {
	if s.cursorAliases == nil {
		s.loadCursorAliasResolver()
	}
	return s.cursorAliases
}

func (s *Server) adminCursorMapping(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.adminGetCursorMapping(w, r)
	case http.MethodPut:
		s.adminPutCursorMapping(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or PUT required", useClientRequestID(r))
	}
}

func (s *Server) adminGetCursorMapping(w http.ResponseWriter, r *http.Request) {
	refresh := strings.EqualFold(r.URL.Query().Get("refresh"), "1") ||
		strings.EqualFold(r.URL.Query().Get("refresh"), "true")
	s.writeCursorMappingResponse(w, r, refresh)
}

func (s *Server) writeCursorMappingResponse(w http.ResponseWriter, r *http.Request, refresh bool) {
	resolver := s.cursorAliasResolver()
	aliases := resolver.Aliases()
	catalog, meta := s.cursorModelCatalog(r.Context(), refresh)
	placeholders := resolver.PlaceholderRows(catalog)
	models := make([]map[string]string, 0, len(catalog))
	for _, model := range catalog {
		models = append(models, map[string]string{
			"id":   model.ID,
			"name": model.Name,
		})
	}
	payload := map[string]any{
		"cursor_models":   models,
		"overrides":       aliases,
		"effective":       resolver.EffectiveMapping(),
		"placeholders":    placeholders,
		"content_mapping": contentMappingSummary(),
		"catalog_source":  meta.Source,
		"catalog_count":   len(models),
	}
	if meta.ProviderID != "" {
		payload["provider_id"] = meta.ProviderID
	}
	if meta.Error != "" {
		payload["discovery_error"] = meta.Error
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) adminPutCursorMapping(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Overrides map[string]string `json:"overrides"`
	}
	// Limit body size; mapping payloads are small.
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if payload.Overrides == nil {
		payload.Overrides = map[string]string{}
	}
	aliases := bridge.NormalizeCursorAliases(payload.Overrides)
	if err := s.store.SaveCursorAliasSettings(r.Context(), store.CursorAliasSettings{Models: aliases}); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	resolver := s.cursorAliasResolver()
	resolver.SetAliases(aliases)
	// Respond with the saved mapping + catalog. Avoid force-refresh on save so a
	// slow Cursor discovery call cannot turn a successful write into a client error.
	s.writeCursorMappingResponse(w, r, false)
}

type cursorCatalogMeta struct {
	Source     string // "discovery" | "static" | "mixed"
	ProviderID string
	Error      string
}

// cursorModelCatalog returns Cursor model IDs for the mapping dropdown.
// Uses the same discovery path as /dashboard/providers/cursor
// (router.DiscoverProviderModels / RefreshProviderModels), then merges static
// fallback + already-configured mapping sources.
//
// When refresh is false and the first discover returns only a thin static-sized
// result (or fails), we automatically retry once with refresh so the mapping UI
// still gets the full live Cursor catalog.
func (s *Server) cursorModelCatalog(ctx context.Context, refresh bool) ([]bridge.CursorModel, cursorCatalogMeta) {
	seen := map[string]struct{}{}
	out := make([]bridge.CursorModel, 0, 256)
	add := func(id, name string, overwrite bool) {
		// Preserve original casing from Cursor; only trim + dedupe case-insensitively.
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		key := strings.ToLower(id)
		if strings.TrimSpace(name) == "" {
			name = id
		}
		if _, ok := seen[key]; ok {
			if !overwrite {
				return
			}
			for i := range out {
				if strings.EqualFold(out[i].ID, id) {
					out[i].ID = id
					if name != "" {
						out[i].Name = name
					}
					return
				}
			}
			return
		}
		seen[key] = struct{}{}
		out = append(out, bridge.CursorModel{ID: id, Name: name})
	}

	// Static baseline (used when discovery is offline).
	for _, model := range cursorpkg.StaticCursorModels {
		add(model.ID, model.Name, false)
	}
	staticCount := len(out)

	meta := cursorCatalogMeta{Source: "static"}
	liveIDs := map[string]struct{}{}
	providerIDs := s.cursorProviderIDs(ctx)
	var lastErr string

	runDiscover := func(forceRefresh bool) {
		for _, providerID := range providerIDs {
			if s.router == nil {
				return
			}
			var (
				items []providers.DiscoveredModel
				err   error
			)
			if forceRefresh {
				items, err = s.router.RefreshProviderModels(ctx, providerID)
			} else {
				items, err = s.router.DiscoverProviderModels(ctx, providerID)
			}
			if err != nil {
				lastErr = err.Error()
				continue
			}
			if meta.ProviderID == "" {
				meta.ProviderID = providerID
			}
			for _, item := range items {
				id := strings.TrimSpace(item.ID)
				if id == "" {
					continue
				}
				liveIDs[strings.ToLower(id)] = struct{}{}
				add(id, item.Name, true)
			}
		}
	}

	runDiscover(refresh)
	// Auto-upgrade: if live discovery returned nothing beyond static fallback, force refresh once.
	if !refresh && len(liveIDs) <= staticCount && len(providerIDs) > 0 {
		lastErr = ""
		liveIDs = map[string]struct{}{}
		runDiscover(true)
	}

	if len(liveIDs) > 0 {
		// Prefer "discovery" whenever live data expands beyond the static fallback set.
		if len(out) > staticCount || len(liveIDs) > staticCount {
			meta.Source = "discovery"
		} else {
			meta.Source = "mixed"
		}
	} else if lastErr != "" {
		meta.Error = lastErr
	} else if len(providerIDs) == 0 {
		meta.Error = "No Cursor provider connected. Connect Cursor under Providers to load the full live model catalog; showing static fallback."
	}

	// Keep already-mapped sources visible even if discovery no longer returns them.
	for source := range s.cursorAliasResolver().EffectiveMapping() {
		add(source, source, false)
	}

	// Stable alphabetical order by name then id for the dropdown.
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if li == lj {
			return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID)
		}
		return li < lj
	})
	return out, meta
}

func (s *Server) cursorProviderIDs(ctx context.Context) []string {
	if s.store == nil {
		return nil
	}
	providersList, err := s.store.Providers(ctx)
	if err != nil {
		return nil
	}
	ids := make([]string, 0)
	seen := map[string]struct{}{}
	for _, provider := range providersList {
		if !provider.Enabled {
			continue
		}
		if !strings.EqualFold(provider.Type, "cursor") && !strings.EqualFold(provider.ID, "cursor") {
			continue
		}
		if _, ok := seen[provider.ID]; ok {
			continue
		}
		seen[provider.ID] = struct{}{}
		ids = append(ids, provider.ID)
	}
	// Prefer canonical "cursor" id first when present.
	sort.SliceStable(ids, func(i, j int) bool {
		if ids[i] == "cursor" {
			return true
		}
		if ids[j] == "cursor" {
			return false
		}
		return ids[i] < ids[j]
	})
	return ids
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
	case "cursor-default":
		return "Cursor Default"
	case "cursor-fast":
		return "Cursor Fast"
	case "cursor-balanced":
		return "Cursor Balanced"
	case "cursor-smart":
		return "Cursor Smart"
	case "cursor-max":
		return "Cursor Max"
	default:
		return name
	}
}

func (s *Server) placeholderModelCatalog() []map[string]any {
	resolver := s.claudeAliasResolver()
	defaults := resolver.Defaults()
	effective := resolver.EffectiveMapping()
	effectiveResolved := resolver.EffectiveResolvedMapping()
	entries := make([]map[string]any, 0, len(bridge.CatalogPlaceholderNames())+8)
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
	// Expose configured Cursor client aliases in /v1/models so Cursor can pick them up.
	for source, target := range s.cursorAliasResolver().EffectiveMapping() {
		if source == "" || target == "" {
			continue
		}
		// Skip keys already covered by Claude/Codex placeholders.
		if _, ok := bridge.PlaceholderRole(source); ok {
			continue
		}
		entries = append(entries, map[string]any{
			"id":          source,
			"object":      "model",
			"name":        placeholderDisplayName(source),
			"owned_by":    "tproxy",
			"placeholder": true,
			"role":        "cursor",
			"resolves_to": target,
			"endpoint":    "placeholder",
			"created":     time.Now().Unix(),
			"route":       "cursor-alias",
		})
	}
	return entries
}

func (s *Server) placeholderModelInfo(id string) (map[string]any, bool) {
	normalized := bridge.NormalizeModel(id)
	if normalized == "" {
		return nil, false
	}
	_, isTier := bridge.PlaceholderRole(normalized)
	_, isCursor := s.cursorAliasResolver().EffectiveMapping()[normalized]
	if !isTier && !isCursor {
		return nil, false
	}
	for _, entry := range s.placeholderModelCatalog() {
		entryID, _ := entry["id"].(string)
		if bridge.NormalizeModel(entryID) != normalized {
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
	// Cursor free-form aliases first so a slot can point at a Claude/Codex placeholder (e.g. fable).
	resolved = s.cursorAliasResolver().Resolve(resolved)
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
			"text":      "text -> input_text / output_text",
			"tools":     "OpenAI tools -> Codex function schema (64-char names)",
			"responses": "Responses SSE emits tool call lifecycle events",
		},
		"claude": map[string]any{
			"tools":       "tool_use <-> tool_calls",
			"reasoning":   "thinking <-> reasoning_content",
			"stop_reason": "finish_reason mapped on stream and JSON responses",
		},
		"cursor": map[string]any{
			"client":   "Cursor OpenAI override → POST /v1/chat/completions (and /v1/responses when enabled)",
			"models":   "Custom model names in Cursor Settings map via free-form aliases, then Claude/Codex tier placeholders",
			"upstream": "Cursor IDE subscription provider uses Connect+protobuf (api2.cursor.sh) — configure under Providers",
			"public":   "Requires a publicly reachable tproxy URL (tunnel); localhost is not supported by Cursor",
		},
	}
}
