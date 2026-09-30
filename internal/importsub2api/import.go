// Package importsub2api imports account exports produced by sub2api's
// "Export data" admin action (DataPayload: exported_at + proxies + accounts).
package importsub2api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

type Export struct {
	Type       string    `json:"type"`
	Version    int       `json:"version"`
	ExportedAt string    `json:"exported_at"`
	Proxies    []Proxy   `json:"proxies"`
	Accounts   []Account `json:"accounts"`
}

// Proxy mirrors sub2api's DataProxy. ProxyKey is "protocol|host|port|user|pass"
// and is what accounts reference via Account.ProxyKey.
type Proxy struct {
	ProxyKey string `json:"proxy_key"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Status   string `json:"status"`
}

// Account mirrors sub2api's DataAccount: credentials stay an open map because
// the key set differs per platform/type.
type Account struct {
	Name           string         `json:"name"`
	Notes          string         `json:"notes"`
	Platform       string         `json:"platform"`
	Type           string         `json:"type"`
	Credentials    map[string]any `json:"credentials"`
	Extra          map[string]any `json:"extra"`
	ProxyKey       *string        `json:"proxy_key"`
	Concurrency    int            `json:"concurrency"`
	Priority       int            `json:"priority"`
	RateMultiplier *float64       `json:"rate_multiplier"`
	ExpiresAt      *int64         `json:"expires_at"`
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
	ProxyPools  int `json:"proxy_pools"`
}

type providerSpec struct {
	Type    string
	Name    string
	BaseURL string // optional override; empty means ApplyProviderDefaults fills it
}

// platformSpecs maps sub2api platforms to tproxy provider types. sub2api's
// "openai" platform is ChatGPT-subscription OAuth traffic against the Codex
// backend; its "gemini" platform is Gemini Code Assist OAuth, which tproxy
// serves through the antigravity provider.
var platformSpecs = map[string]providerSpec{
	"openai":      {Type: "codex", Name: "OpenAI Codex"},
	"anthropic":   {Type: "claude", Name: "Anthropic Claude"},
	"gemini":      {Type: "antigravity", Name: "Google Antigravity"},
	"antigravity": {Type: "antigravity", Name: "Google Antigravity"},
	"grok":        {Type: "xai", Name: "xAI (Grok)"},
}

// apiKeyPlatformSpecs covers platform+apikey accounts, which talk to plain API
// endpoints rather than the subscription OAuth backends.
var apiKeyPlatformSpecs = map[string]providerSpec{
	"openai":    {Type: "openai-compatible", Name: "OpenAI", BaseURL: "https://api.openai.com/v1"},
	"anthropic": {Type: "claude", Name: "Anthropic Claude"},
	"grok":      {Type: "xai", Name: "xAI (Grok)"},
}

// oauthLikeTypes carry bearer tokens: oauth and Claude setup tokens
// (sk-ant-oat…, which tproxy's claude provider recognizes as OAuth material).
var oauthLikeTypes = map[string]bool{
	"oauth":       true,
	"setup-token": true,
	"setup_token": true,
}

func ParseExport(data []byte) (*Export, error) {
	var export Export
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	usable := 0
	for _, account := range export.Accounts {
		if strings.TrimSpace(account.Platform) != "" && len(account.Credentials) > 0 {
			usable++
		}
	}
	if usable == 0 {
		return nil, fmt.Errorf("not a sub2api export: missing accounts with platform and credentials")
	}
	return &export, nil
}

func Import(ctx context.Context, dataStore *store.Store, data []byte, opts Options) (*Result, error) {
	export, err := ParseExport(data)
	if err != nil {
		return nil, err
	}
	result := &Result{DryRun: opts.DryRun, OK: true}
	importer := &importer{store: dataStore, result: result, dryRun: opts.DryRun, providers: map[string]struct{}{}, proxyPoolByKey: map[string]string{}}

	for _, proxy := range export.Proxies {
		importer.importProxy(ctx, proxy)
	}
	for _, account := range export.Accounts {
		importer.importAccount(ctx, account)
	}
	if len(result.Errors) > 0 {
		result.OK = false
	}
	return result, nil
}

type importer struct {
	store          *store.Store
	result         *Result
	dryRun         bool
	providers      map[string]struct{}
	proxyPoolByKey map[string]string
	warnSet        map[string]struct{}
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

func (i *importer) ensureProvider(ctx context.Context, providerID string, spec providerSpec) bool {
	if _, ok := i.providers[providerID]; ok {
		return true
	}
	// Importing accounts must not reset an existing provider's URL, OAuth
	// configuration, proxy bindings or enabled state.
	if existing, err := i.store.Provider(ctx, providerID); err == nil {
		if existing.Type != spec.Type {
			i.fail(fmt.Sprintf("provider %q has type %q, expected %q", providerID, existing.Type, spec.Type))
			return false
		}
		i.providers[providerID] = struct{}{}
		i.result.Counts.Providers++
		return true
	} else if !errors.Is(err, sql.ErrNoRows) {
		i.fail(fmt.Sprintf("provider %q: %v", providerID, err))
		return false
	}
	if i.dryRun {
		i.providers[providerID] = struct{}{}
		i.result.Counts.Providers++
		return true
	}
	providerConfig := config.ProviderConfig{
		ID:      providerID,
		Type:    spec.Type,
		Name:    spec.Name,
		BaseURL: spec.BaseURL,
		Enabled: true,
	}
	config.ApplyProviderDefaults(&providerConfig)
	if err := i.store.SaveProvider(ctx, providerConfig); err != nil {
		i.fail(fmt.Sprintf("provider %q: %v", providerID, err))
		return false
	}
	i.providers[providerID] = struct{}{}
	i.result.Counts.Providers++
	return true
}

func (i *importer) importProxy(ctx context.Context, proxy Proxy) {
	poolID, url := proxyPoolFor(proxy)
	if poolID == "" {
		return
	}
	if proxy.ProxyKey != "" {
		i.proxyPoolByKey[proxy.ProxyKey] = poolID
	}
	enabled := !strings.EqualFold(strings.TrimSpace(proxy.Status), "inactive") &&
		!strings.EqualFold(strings.TrimSpace(proxy.Status), "disabled")
	if i.dryRun {
		i.result.Counts.ProxyPools++
		return
	}
	if err := i.store.SaveProxyPool(ctx, config.ProxyPoolConfig{
		ID:      poolID,
		Name:    firstNonEmpty(proxy.Name, poolID),
		URL:     url,
		Enabled: boolPtr(enabled),
	}); err != nil {
		i.fail(fmt.Sprintf("proxy pool %q: %v", poolID, err))
		return
	}
	i.result.Counts.ProxyPools++
}

func (i *importer) importAccount(ctx context.Context, account Account) {
	platform := strings.ToLower(strings.TrimSpace(account.Platform))
	accountType := strings.ToLower(strings.TrimSpace(account.Type))
	label := accountLabel(account)
	if label == "" {
		label = platform + " account"
	}
	if len(account.Credentials) == 0 {
		i.warn(fmt.Sprintf("account %q has no credentials", label))
		return
	}

	if oauthLikeTypes[accountType] {
		i.importOAuthAccount(ctx, account, platform, label)
		return
	}
	if accountType == "apikey" || accountType == "api_key" {
		i.importAPIKeyAccount(ctx, account, platform, label)
		return
	}
	i.warn(fmt.Sprintf("account %q has unsupported type %q — skipped", label, account.Type))
}

func (i *importer) importOAuthAccount(ctx context.Context, account Account, platform, label string) {
	spec, ok := platformSpecs[platform]
	if !ok {
		i.warn(fmt.Sprintf("account %q uses unsupported platform %q", label, platform))
		return
	}
	providerID := spec.Type

	accessToken := credentialString(account.Credentials, "access_token", "session_key", "token")
	if accessToken == "" {
		i.warn(fmt.Sprintf("oauth account %q has no access token", label))
		return
	}
	if !i.ensureProvider(ctx, providerID, spec) {
		return
	}

	token := store.OAuthToken{
		AccessToken:  accessToken,
		RefreshToken: credentialString(account.Credentials, "refresh_token"),
		TokenType:    "Bearer",
		ExpiresAt:    credentialExpiresAt(account.Credentials),
		Extra:        map[string]any{},
	}
	for key, value := range account.Credentials {
		switch key {
		case "access_token", "refresh_token", "expires_at", "expires_in", "token_type", "session_key", "token":
		default:
			token.Extra[key] = value
		}
	}
	// The codex adapter and refresh flow key the ChatGPT account off
	// Extra["account_id"].
	if accountID := credentialString(account.Credentials, "chatgpt_account_id", "account_id"); accountID != "" {
		token.Extra["account_id"] = accountID
	}
	token.Extra["imported_from"] = "sub2api"
	token.Extra["sub2api_platform"] = platform

	credentialID := credentialIDFor(account, platform)
	if i.dryRun {
		i.result.Counts.Credentials++
		return
	}
	if err := i.store.SaveOAuthCredential(ctx, providerID, credentialID, label, accountEmail(account), token); err != nil {
		i.fail(fmt.Sprintf("oauth credential %q: %v", label, err))
		return
	}
	i.applyMetadata(ctx, credentialID, account)
	i.result.Counts.Credentials++
}

func (i *importer) importAPIKeyAccount(ctx context.Context, account Account, platform, label string) {
	spec, ok := apiKeyPlatformSpecs[platform]
	if !ok {
		i.warn(fmt.Sprintf("api key account %q uses unsupported platform %q", label, platform))
		return
	}
	apiKey := credentialString(account.Credentials, "api_key", "key", "access_token")
	if apiKey == "" {
		i.warn(fmt.Sprintf("api key account %q has no api_key", label))
		return
	}
	providerID := spec.Type
	if platform == "openai" {
		// Keep api.openai.com keys off the "codex" provider id so they cannot
		// collide with OAuth accounts on the ChatGPT backend.
		providerID = "openai-api"
	}
	spec.BaseURL = firstNonEmpty(spec.BaseURL, credentialString(account.Credentials, "base_url"))
	if !i.ensureProvider(ctx, providerID, spec) {
		return
	}

	credentialID := credentialIDFor(account, platform)
	priority := mappedPriority(account.Priority)
	if i.dryRun {
		i.result.Counts.Credentials++
		return
	}
	if err := i.store.SaveCredential(ctx, providerID, config.CredentialConfig{
		ID:         credentialID,
		Label:      label,
		Email:      accountEmail(account),
		AuthType:   "api_key",
		Secret:     apiKey,
		Priority:   priority,
		Enabled:    boolPtr(true),
		Metadata:   i.metadataFor(account),
		ProxyPools: i.poolIDsFor(account),
	}); err != nil {
		i.fail(fmt.Sprintf("api key credential %q: %v", label, err))
		return
	}
	i.result.Counts.Credentials++
}

// applyMetadata persists proxy pools and the sub2api scheduling knobs that
// SaveOAuthCredential has no slot for (it writes metadata_json='{}').
func (i *importer) applyMetadata(ctx context.Context, credentialID string, account Account) {
	metadata := i.metadataFor(account)
	if poolIDs := i.poolIDsFor(account); len(poolIDs) > 0 {
		metadata["proxy_pool_ids"] = poolIDs
	}
	if len(metadata) == 0 {
		return
	}
	if err := i.store.UpdateCredentialMetadata(ctx, credentialID, metadata); err != nil {
		i.warn(fmt.Sprintf("credential %q: metadata not saved: %v", credentialID, err))
	}
}

func (i *importer) metadataFor(account Account) map[string]any {
	metadata := map[string]any{"imported_from": "sub2api"}
	if account.Concurrency > 0 {
		metadata["sub2api_concurrency"] = account.Concurrency
	}
	if account.Priority != 0 {
		metadata["sub2api_priority"] = account.Priority
	}
	if account.RateMultiplier != nil {
		metadata["sub2api_rate_multiplier"] = *account.RateMultiplier
	}
	if account.ExpiresAt != nil && *account.ExpiresAt > 0 {
		metadata["sub2api_expires_at"] = *account.ExpiresAt
	}
	if len(account.Extra) > 0 {
		metadata["sub2api_extra"] = account.Extra
	}
	return metadata
}

func (i *importer) poolIDsFor(account Account) []string {
	if account.ProxyKey == nil {
		return nil
	}
	if poolID := i.proxyPoolByKey[strings.TrimSpace(*account.ProxyKey)]; poolID != "" {
		return []string{poolID}
	}
	return nil
}

// mappedPriority inverts sub2api priority: sub2api schedules lower numbers
// first while tproxy tries higher priority credentials first.
func mappedPriority(priority int) int {
	if priority <= 0 {
		return 0
	}
	return -priority
}

func credentialIDFor(account Account, platform string) string {
	// chatgpt_account_id is deliberately last: on Team workspaces it is the
	// shared workspace id, identical across every member account.
	unique := firstNonEmpty(
		accountEmail(account),
		credentialString(account.Credentials, "chatgpt_user_id"),
		credentialString(account.Credentials, "claude_user_id", "anthropic_user_id"),
		account.Name,
		credentialString(account.Credentials, "chatgpt_account_id", "account_id"),
	)
	return slugify("sub2api-" + platform + "-" + unique)
}

func accountLabel(account Account) string {
	return firstNonEmpty(
		accountEmail(account),
		account.Name,
		stringFromAny(account.Extra["email"]),
	)
}

func accountEmail(account Account) string {
	return firstNonEmpty(
		credentialString(account.Credentials, "email"),
		stringFromAny(account.Extra["email"]),
	)
}

func credentialString(credentials map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringFromAny(credentials[key])); value != "" {
			return value
		}
	}
	return ""
}

// credentialExpiresAt parses credentials.expires_at, which sub2api emits either
// as an RFC3339 string or as unix seconds; credentials.expires_in (seconds) is
// the fallback.
func credentialExpiresAt(credentials map[string]any) time.Time {
	switch value := credentials["expires_at"].(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
				return parsed
			}
		}
	case float64:
		if value > 0 {
			return time.Unix(int64(value), 0).UTC()
		}
	}
	if seconds, ok := credentials["expires_in"].(float64); ok && seconds > 0 {
		return time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	}
	return time.Time{}
}

func proxyPoolFor(proxy Proxy) (id, rawURL string) {
	protocol := strings.ToLower(strings.TrimSpace(proxy.Protocol))
	host := strings.TrimSpace(proxy.Host)
	if host == "" || proxy.Port <= 0 {
		return "", ""
	}
	if protocol == "" {
		protocol = "http"
	}
	u := &url.URL{Scheme: protocol, Host: fmt.Sprintf("%s:%d", host, proxy.Port)}
	if proxy.Username != "" {
		u.User = url.UserPassword(proxy.Username, proxy.Password)
	}
	id = slugify("sub2api-proxy-" + firstNonEmpty(proxy.Name, fmt.Sprintf("%s-%s-%d", protocol, host, proxy.Port)))
	return id, u.String()
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := true
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "sub2api-imported"
	}
	return out
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
