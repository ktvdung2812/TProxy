package import9router

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/ninerouter"
	"github.com/tproxy/tproxy/internal/store"
)

type Options struct {
	DryRun bool
}

type Result struct {
	OK       bool     `json:"ok"`
	DryRun   bool     `json:"dry_run"`
	Counts   Counts   `json:"counts"`
	Warnings []string `json:"warnings"`
	Errors   []string `json:"errors"`
}

func (r *Result) GetOK() bool {
	if r == nil {
		return false
	}
	return r.OK
}

type Counts struct {
	Providers   int `json:"providers"`
	Credentials int `json:"credentials"`
	APIKeys     int `json:"api_keys"`
	Models      int `json:"models"`
	Combos      int `json:"combos"`
	ProxyPools  int `json:"proxy_pools"`
}

var slugPattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func ParseBackup(data []byte) (*Backup, error) {
	var backup Backup
	if err := json.Unmarshal(data, &backup); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if len(backup.ProviderConnections) == 0 && len(backup.APIKeys) == 0 && len(backup.Combos) == 0 {
		return nil, fmt.Errorf("not a 9router backup: missing providerConnections/apiKeys/combos")
	}
	return &backup, nil
}

func Import(ctx context.Context, dataStore *store.Store, data []byte, opts Options) (*Result, error) {
	backup, err := ParseBackup(data)
	if err != nil {
		return nil, err
	}
	result := &Result{DryRun: opts.DryRun, OK: true}
	importer := &importer{store: dataStore, result: result, dryRun: opts.DryRun, models: map[string]struct{}{}}

	for _, pool := range backup.ProxyPools {
		importer.importProxyPool(ctx, pool)
	}
	for _, conn := range backup.ProviderConnections {
		importer.importConnection(ctx, conn)
	}
	for _, key := range backup.APIKeys {
		importer.importAPIKey(ctx, key)
	}
	for _, combo := range backup.Combos {
		importer.importCombo(ctx, backup, combo)
	}
	if !opts.DryRun {
		if err := importRotationSettings(ctx, dataStore, backup.Settings); err != nil {
			return nil, err
		}
	}

	if len(result.Errors) > 0 {
		result.OK = false
	}
	return result, nil
}

type importer struct {
	store   *store.Store
	result  *Result
	dryRun  bool
	models  map[string]struct{}
	warnSet map[string]struct{}
}

func (i *importer) warn(message string) {
	if i.warnSet == nil {
		i.warnSet = map[string]struct{}{}
	}
	if _, exists := i.warnSet[message]; exists {
		return
	}
	i.warnSet[message] = struct{}{}
	i.result.Warnings = append(i.result.Warnings, message)
}

func (i *importer) fail(message string) {
	i.result.Errors = append(i.result.Errors, message)
}

func (i *importer) ensureProvider(ctx context.Context, providerID string) {
	providerID = ninerouter.ResolveProviderID(providerID)
	preset, ok := ninerouter.Lookup(providerID)
	if !ok {
		i.warn(fmt.Sprintf("unsupported 9router provider %q — skipped provider bootstrap", providerID))
		return
	}
	cfg := config.ProviderConfig{
		ID:      providerID,
		Type:    preset.Type,
		Name:    preset.Name,
		BaseURL: preset.BaseURL,
		Enabled: true,
	}
	config.ApplyProviderDefaults(&cfg)
	if i.dryRun {
		i.result.Counts.Providers++
		return
	}
	if err := i.store.SaveProvider(ctx, cfg); err != nil {
		i.fail(fmt.Sprintf("provider %q: %v", providerID, err))
		return
	}
	i.result.Counts.Providers++
}

func (i *importer) importProxyPool(ctx context.Context, pool ProxyPool) {
	if strings.TrimSpace(pool.ID) == "" {
		return
	}
	cfg := config.ProxyPoolConfig{
		ID:      pool.ID,
		Name:    firstNonEmpty(pool.Name, pool.ID),
		URL:     pool.URL,
		Enabled: boolPtr(pool.IsActive),
	}
	if i.dryRun {
		i.result.Counts.ProxyPools++
		return
	}
	if err := i.store.SaveProxyPool(ctx, cfg); err != nil {
		i.fail(fmt.Sprintf("proxy pool %q: %v", pool.ID, err))
		return
	}
	i.result.Counts.ProxyPools++
}

func (i *importer) importConnection(ctx context.Context, conn ProviderConnection) {
	if strings.TrimSpace(conn.ID) == "" || strings.TrimSpace(conn.Provider) == "" {
		return
	}
	providerID := ninerouter.ResolveProviderID(conn.Provider)
	if _, ok := ninerouter.Lookup(providerID); !ok {
		i.warn(fmt.Sprintf("credential %q uses unsupported provider %q", conn.Name, conn.Provider))
		return
	}
	i.ensureProvider(ctx, providerID)
	label := firstNonEmpty(conn.Name, conn.Email, conn.ID)
	enabled := conn.IsActive
	priority := conn.Priority
	if priority <= 0 {
		priority = 100
	}

	switch strings.ToLower(conn.AuthType) {
	case "oauth":
		if strings.TrimSpace(conn.AccessToken) == "" {
			i.warn(fmt.Sprintf("oauth credential %q has no access token", label))
			return
		}
		token := store.OAuthToken{
			AccessToken:  conn.AccessToken,
			RefreshToken: conn.RefreshToken,
			TokenType:    "Bearer",
			ExpiresAt:    parseTime(conn.ExpiresAt),
			Extra: map[string]any{
				"scope":                 conn.Scope,
				"provider_specific":     conn.ProviderSpecificData,
				"imported_from":         "9router",
				"ninerouter_connection": conn.ID,
			},
		}
		applyImportedOAuthExtras(providerID, conn, &token)
		if i.dryRun {
			i.result.Counts.Credentials++
			return
		}
		if err := i.store.SaveOAuthCredential(ctx, providerID, conn.ID, label, conn.Email, token); err != nil {
			i.fail(fmt.Sprintf("oauth credential %q: %v", label, err))
			return
		}
		if !enabled {
			_ = i.store.SetCredentialEnabled(ctx, conn.ID, false)
		}
		i.touchImportedRotationState(ctx, conn)
		i.result.Counts.Credentials++
	case "apikey", "api_key":
		if strings.TrimSpace(conn.APIKey) == "" {
			i.warn(fmt.Sprintf("api key credential %q is empty", label))
			return
		}
		if i.dryRun {
			i.result.Counts.Credentials++
			return
		}
		if err := i.store.SaveCredential(ctx, providerID, config.CredentialConfig{
			ID:       conn.ID,
			Label:    label,
			Email:    conn.Email,
			AuthType: "api_key",
			Secret:   conn.APIKey,
			Priority: priority,
			Enabled:  boolPtr(enabled),
		}); err != nil {
			i.fail(fmt.Sprintf("api key credential %q: %v", label, err))
			return
		}
		i.touchImportedRotationState(ctx, conn)
		i.result.Counts.Credentials++
	default:
		i.warn(fmt.Sprintf("credential %q has unsupported auth type %q", label, conn.AuthType))
	}
}

func (i *importer) importAPIKey(ctx context.Context, key APIKey) {
	if strings.TrimSpace(key.Key) == "" {
		return
	}
	id := firstNonEmpty(key.ID, slugify(key.Name))
	name := firstNonEmpty(key.Name, id)
	if i.dryRun {
		i.result.Counts.APIKeys++
		return
	}
	if err := i.store.ImportClientAPIKey(ctx, id, name, key.Key, []string{"*"}, key.IsActive); err != nil {
		i.fail(fmt.Sprintf("api key %q: %v", name, err))
		return
	}
	i.result.Counts.APIKeys++
}

func (i *importer) importCombo(ctx context.Context, backup *Backup, combo Combo) {
	name := strings.TrimSpace(combo.Name)
	if name == "" || len(combo.Models) == 0 {
		if name != "" {
			i.warn(fmt.Sprintf("combo %q has no models and was skipped", name))
		}
		return
	}
	items := make([]config.ComboItemConfig, 0, len(combo.Models))
	for _, modelRef := range combo.Models {
		providerID, upstreamModel := parseModelRef(modelRef)
		if providerID == "" || upstreamModel == "" {
			i.warn(fmt.Sprintf("combo %q: could not parse model %q", name, modelRef))
			continue
		}
		resolvedProvider := ninerouter.ResolveProviderID(providerID)
		if _, ok := ninerouter.Lookup(resolvedProvider); !ok {
			i.warn(fmt.Sprintf("combo %q: unsupported provider in model %q", name, modelRef))
			continue
		}
		providerID = resolvedProvider
		virtualModelID, err := i.ensureVirtualModel(ctx, providerID, upstreamModel)
		if err != nil {
			i.fail(fmt.Sprintf("combo %q model %q: %v", name, modelRef, err))
			continue
		}
		items = append(items, config.ComboItemConfig{PublicModelID: virtualModelID})
	}
	if len(items) == 0 {
		i.warn(fmt.Sprintf("combo %q produced no importable steps", name))
		return
	}

	policy := map[string]any{"imported_from": "9router", "ninerouter_combo_id": combo.ID}
	if backup.Settings != nil {
		if strategies, ok := backup.Settings["comboStrategies"].(map[string]any); ok {
			if strategy, ok := strategies[name].(map[string]any); ok {
				policy["ninerouter_strategy"] = strategy
			}
		}
	}

	comboCfg := config.ComboConfig{
		ID:                   slugify(name),
		DisplayName:          name,
		Enabled:              true,
		RewriteResponseModel: true,
		Capabilities:         []string{"text", "tools"},
		Policy:               policy,
		Items:                items,
	}
	if i.dryRun {
		i.result.Counts.Combos++
		return
	}
	if err := i.store.SaveCombo(ctx, comboCfg); err != nil {
		i.fail(fmt.Sprintf("combo %q: %v", name, err))
		return
	}
	i.result.Counts.Combos++
}

func (i *importer) ensureVirtualModel(ctx context.Context, providerID, upstreamModel string) (string, error) {
	i.ensureProvider(ctx, providerID)
	modelID := slugify(fmt.Sprintf("%s-%s", providerID, upstreamModel))
	if _, exists := i.models[modelID]; exists {
		return modelID, nil
	}
	cfg := config.PublicModelConfig{
		ID:                   modelID,
		DisplayName:          fmt.Sprintf("%s / %s", providerID, upstreamModel),
		Enabled:              true,
		RewriteResponseModel: true,
		Capabilities:         []string{"text", "tools"},
		Routes: []config.RouteTargetConfig{
			{
				ID:            fmt.Sprintf("%s-route", modelID),
				Provider:      providerID,
				UpstreamModel: upstreamModel,
				Priority:      100,
				Weight:        1,
				Enabled:       boolPtr(true),
			},
		},
	}
	if i.dryRun {
		i.models[modelID] = struct{}{}
		i.result.Counts.Models++
		return modelID, nil
	}
	if err := i.store.SavePublicModel(ctx, cfg); err != nil {
		return "", err
	}
	i.models[modelID] = struct{}{}
	i.result.Counts.Models++
	return modelID, nil
}

func parseModelRef(value string) (providerID, upstreamModel string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if strings.Contains(value, "/") {
		parts := strings.SplitN(value, "/", 2)
		providerID = resolveProviderID(parts[0])
		upstreamModel = strings.TrimSpace(parts[1])
		return providerID, upstreamModel
	}
	return resolveProviderID(value), value
}

func resolveProviderID(aliasOrID string) string {
	aliasOrID = strings.TrimSpace(aliasOrID)
	if aliasOrID == "" {
		return ""
	}
	if preset, ok := ninerouter.Lookup(aliasOrID); ok {
		return preset.ID
	}
	if strings.HasPrefix(aliasOrID, "openai-compatible") {
		return ""
	}
	return aliasOrID
}

func slugify(value string) string {
	value = strings.Trim(strings.ToLower(strings.TrimSpace(value)), "-._")
	if value == "" {
		return "imported-item"
	}
	return slugPattern.ReplaceAllString(value, "-")
}

func parseTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return time.Time{}
}

func importRotationSettings(ctx context.Context, dataStore *store.Store, settings map[string]any) error {
	if len(settings) == 0 {
		return nil
	}
	current, err := dataStore.AccountRotationSettings(ctx)
	if err != nil {
		return err
	}
	if value, ok := settings["stickyRoundRobinLimit"].(float64); ok && value > 0 {
		current.StickyRoundRobinLimit = int(value)
	}
	if value, ok := settings["fallbackStrategy"].(string); ok && strings.TrimSpace(value) != "" {
		current.Strategy = strings.TrimSpace(value)
	}
	if strategies, ok := settings["providerStrategies"].(map[string]any); ok {
		if current.ProviderStrategies == nil {
			current.ProviderStrategies = map[string]store.ProviderRotationStrategy{}
		}
		for providerName, raw := range strategies {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			providerID := ninerouter.ResolveProviderID(providerName)
			strategy := store.ProviderRotationStrategy{}
			if value, ok := entry["fallbackStrategy"].(string); ok {
				strategy.Strategy = value
			}
			if value, ok := entry["stickyRoundRobinLimit"].(float64); ok && value > 0 {
				strategy.StickyRoundRobinLimit = int(value)
			}
			current.ProviderStrategies[providerID] = strategy
		}
	}
	return dataStore.SaveAccountRotationSettings(ctx, current)
}

func (i *importer) touchImportedRotationState(ctx context.Context, conn ProviderConnection) {
	if i.dryRun {
		return
	}
	usedAt := strings.TrimSpace(conn.LastUsedAt)
	if usedAt == "" {
		return
	}
	count := conn.ConsecutiveUseCount
	if count < 0 {
		count = 0
	}
	_ = i.store.TouchCredentialRotation(ctx, conn.ID, count, usedAt)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func applyImportedOAuthExtras(providerID string, conn ProviderConnection, token *store.OAuthToken) {
	if token == nil {
		return
	}
	if token.Extra == nil {
		token.Extra = map[string]any{}
	}
	switch {
	case providerID == "cursor":
		machineID := firstNonEmpty(
			stringFromAny(conn.ProviderSpecificData["machineId"]),
			stringFromAny(conn.ProviderSpecificData["machine_id"]),
		)
		if machineID == "" {
			return
		}
		token.Extra["machine_id"] = machineID
		token.Extra["machineId"] = machineID
		token.Extra["auth_method"] = firstNonEmpty(
			stringFromAny(conn.ProviderSpecificData["authMethod"]),
			"imported",
		)
		token.Extra["client_type"] = "ide"
		token.Extra["client_version"] = "3.1.0"
	case isAntigravityProvider(providerID):
		projectID := firstNonEmpty(
			projectIDFromAny(conn.ProjectID),
			projectIDFromAny(conn.ProjectIDSnake),
			projectIDFromAny(conn.Project),
			projectIDFromAny(conn.CloudAICompanionProject),
			projectIDFromAny(conn.CloudAICompanionSnake),
			projectIDFromAny(conn.ProviderSpecificData["projectId"]),
			projectIDFromAny(conn.ProviderSpecificData["project_id"]),
			projectIDFromAny(conn.ProviderSpecificData["project"]),
			projectIDFromAny(conn.ProviderSpecificData["cloudaicompanionProject"]),
			projectIDFromAny(conn.ProviderSpecificData["cloudaicompanion_project"]),
		)
		if projectID != "" {
			token.Extra["project_id"] = projectID
		}
	}
}

func isAntigravityProvider(providerID string) bool {
	preset, ok := ninerouter.Lookup(providerID)
	return ok && preset.Type == "antigravity"
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func projectIDFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return firstNonEmpty(
			projectIDFromAny(typed["id"]),
			projectIDFromAny(typed["projectId"]),
			projectIDFromAny(typed["project_id"]),
			projectIDFromAny(typed["project"]),
			projectIDFromAny(typed["cloudaicompanionProject"]),
			projectIDFromAny(typed["cloudaicompanion_project"]),
		)
	default:
		return ""
	}
}

func boolPtr(value bool) *bool {
	return &value
}
