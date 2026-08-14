package importcliproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

type AuthFile struct {
	Type                    string         `json:"type"`
	AccessToken             string         `json:"access_token"`
	RefreshToken            string         `json:"refresh_token"`
	IDToken                 string         `json:"id_token"`
	Email                   string         `json:"email"`
	AccountID               string         `json:"account_id"`
	Disabled                bool           `json:"disabled"`
	Expired                 string         `json:"expired"`
	LastRefresh             string         `json:"last_refresh"`
	APIKey                  string         `json:"api_key"`
	ProjectID               any            `json:"project_id"`
	ProjectIDCamel          any            `json:"projectId"`
	Project                 any            `json:"project"`
	CloudAICompanionProject any            `json:"cloudaicompanionProject"`
	CloudAICompanionSnake   any            `json:"cloudaicompanion_project"`
	Extra                   map[string]any `json:"-"`
}

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
}

type providerSpec struct {
	Type string
	Name string
}

var providerSpecs = map[string]providerSpec{
	"codex":                {Type: "codex", Name: "OpenAI Codex"},
	"claude":               {Type: "claude", Name: "Anthropic Claude"},
	"xai":                  {Type: "xai", Name: "xAI (Grok)"},
	"antigravity":          {Type: "antigravity", Name: "Google Antigravity"},
	"gemini-cli":           {Type: "antigravity", Name: "Google Gemini CLI"},
	"kimi":                 {Type: "kimi", Name: "Kimi Code"},
	"gemini":               {Type: "gemini", Name: "Google Gemini"},
	"copilot":              {Type: "copilot", Name: "GitHub Copilot"},
	"openai-compatibility": {Type: "openai-compatible", Name: "OpenAI Compatible"},
	"openai-compatible":    {Type: "openai-compatible", Name: "OpenAI Compatible"},
}

func ParseAuthFiles(data []byte) ([]AuthFile, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty JSON payload")
	}
	if trimmed[0] == '[' {
		var list []AuthFile
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("invalid CLIProxyAPI auth array: %w", err)
		}
		return normalizeAuthFiles(list)
	}
	var single AuthFile
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, fmt.Errorf("invalid CLIProxyAPI auth JSON: %w", err)
	}
	return normalizeAuthFiles([]AuthFile{single})
}

func normalizeAuthFiles(items []AuthFile) ([]AuthFile, error) {
	out := make([]AuthFile, 0, len(items))
	for _, item := range items {
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		if item.Type == "" {
			continue
		}
		if strings.TrimSpace(item.AccessToken) == "" && strings.TrimSpace(item.APIKey) == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("not a CLIProxyAPI auth export: missing type and access_token/api_key")
	}
	return out, nil
}

func Import(ctx context.Context, dataStore *store.Store, data []byte, opts Options) (*Result, error) {
	files, err := ParseAuthFiles(data)
	if err != nil {
		return nil, err
	}
	result := &Result{DryRun: opts.DryRun, OK: true}
	importer := &importer{store: dataStore, result: result, dryRun: opts.DryRun, providers: map[string]struct{}{}}

	for _, file := range files {
		importer.importAuthFile(ctx, file)
	}
	if len(result.Errors) > 0 {
		result.OK = false
	}
	return result, nil
}

type importer struct {
	store     *store.Store
	result    *Result
	dryRun    bool
	providers map[string]struct{}
	warnSet   map[string]struct{}
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
	if _, ok := i.providers[providerID]; ok {
		return
	}
	spec, ok := providerSpecs[providerID]
	if !ok {
		i.warn(fmt.Sprintf("unsupported CLIProxyAPI type %q", providerID))
		return
	}
	if i.dryRun {
		i.providers[providerID] = struct{}{}
		i.result.Counts.Providers++
		return
	}
	providerConfig := config.ProviderConfig{
		ID:      providerID,
		Type:    spec.Type,
		Name:    spec.Name,
		Enabled: true,
	}
	// A Gemini CLI export does not carry a Cloud Code base URL. Apply just the
	// Antigravity defaults needed to make that imported credential usable; keep
	// other CLIProxy provider imports on their established bootstrap path.
	if spec.Type == "antigravity" {
		config.ApplyProviderDefaults(&providerConfig)
	}
	if err := i.store.SaveProvider(ctx, providerConfig); err != nil {
		i.fail(fmt.Sprintf("provider %q: %v", providerID, err))
		return
	}
	i.providers[providerID] = struct{}{}
	i.result.Counts.Providers++
}

func (i *importer) importAuthFile(ctx context.Context, file AuthFile) {
	spec, ok := providerSpecs[file.Type]
	if !ok {
		i.warn(fmt.Sprintf("skip auth for unsupported type %q", file.Type))
		return
	}
	providerID := file.Type
	i.ensureProvider(ctx, providerID)

	label := firstNonEmpty(file.Email, file.AccountID, providerID+" account")
	credentialID := credentialIDFor(file)
	enabled := !file.Disabled

	if strings.TrimSpace(file.APIKey) != "" {
		if i.dryRun {
			i.result.Counts.Credentials++
			return
		}
		if err := i.store.SaveCredential(ctx, providerID, config.CredentialConfig{
			ID:       credentialID,
			Label:    label,
			Email:    file.Email,
			AuthType: "api_key",
			Secret:   file.APIKey,
			Priority: 100,
			Enabled:  boolPtr(enabled),
		}); err != nil {
			i.fail(fmt.Sprintf("api key credential %q: %v", label, err))
			return
		}
		i.result.Counts.Credentials++
		return
	}

	token := store.OAuthToken{
		AccessToken:  file.AccessToken,
		RefreshToken: file.RefreshToken,
		TokenType:    "Bearer",
		ExpiresAt:    parseTime(firstNonEmpty(file.Expired)),
		Extra: map[string]any{
			"account_id":        file.AccountID,
			"id_token":          file.IDToken,
			"last_refresh":      file.LastRefresh,
			"imported_from":     "cliproxyapi",
			"cliproxy_type":     file.Type,
			"cliproxy_provider": spec.Type,
		},
	}
	if spec.Type == "antigravity" {
		if projectID := antigravityProjectID(file); projectID != "" {
			token.Extra["project_id"] = projectID
		}
	}
	if i.dryRun {
		i.result.Counts.Credentials++
		return
	}
	if err := i.store.SaveOAuthCredential(ctx, providerID, credentialID, label, file.Email, token); err != nil {
		i.fail(fmt.Sprintf("oauth credential %q: %v", label, err))
		return
	}
	if !enabled {
		_ = i.store.SetCredentialEnabled(ctx, credentialID, false)
	}
	i.result.Counts.Credentials++
}

func antigravityProjectID(file AuthFile) string {
	return firstNonEmpty(
		projectIDFromAny(file.ProjectID),
		projectIDFromAny(file.ProjectIDCamel),
		projectIDFromAny(file.Project),
		projectIDFromAny(file.CloudAICompanionProject),
		projectIDFromAny(file.CloudAICompanionSnake),
	)
}

func projectIDFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return firstNonEmpty(
			projectIDFromAny(typed["id"]),
			projectIDFromAny(typed["project_id"]),
			projectIDFromAny(typed["projectId"]),
			projectIDFromAny(typed["project"]),
			projectIDFromAny(typed["cloudaicompanionProject"]),
			projectIDFromAny(typed["cloudaicompanion_project"]),
		)
	default:
		return ""
	}
}

func credentialIDFor(file AuthFile) string {
	if id := strings.TrimSpace(file.AccountID); id != "" {
		return slugify("cliproxy-" + file.Type + "-" + id)
	}
	if email := strings.TrimSpace(file.Email); email != "" {
		return slugify("cliproxy-" + file.Type + "-" + email)
	}
	return slugify("cliproxy-" + file.Type + "-imported")
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "cliproxy-imported"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.', r == '_', r == '-', r == '@':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "cliproxy-imported"
	}
	return out
}

func parseTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	layouts := []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolPtr(value bool) *bool {
	return &value
}
