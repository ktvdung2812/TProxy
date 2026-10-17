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

	"github.com/tproxy/tproxy/internal/store"
)

// ExportAccounts builds a sub2api-compatible JSON export for the given credentials.
func ExportAccounts(ctx context.Context, dataStore *store.Store, credentialIDs []string) ([]byte, *Result, error) {
	ids := uniqueNonEmpty(credentialIDs)
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("credential_ids is required")
	}

	result := &Result{OK: true}
	exporter := &exporter{store: dataStore, result: result, proxies: map[string]Proxy{}, proxyKeys: map[string]string{}}
	accounts := make([]Account, 0, len(ids))

	for _, credentialID := range ids {
		credential, err := dataStore.CredentialByID(ctx, credentialID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				result.Errors = append(result.Errors, fmt.Sprintf("credential %q not found", credentialID))
				continue
			}
			return nil, nil, err
		}
		provider, err := dataStore.Provider(ctx, credential.ProviderID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				result.Errors = append(result.Errors, fmt.Sprintf("credential %q: provider %q not found", credentialID, credential.ProviderID))
				continue
			}
			return nil, nil, err
		}
		account, err := exporter.accountFromCredential(ctx, *provider, credential)
		if err != nil {
			result.Warnings = append(result.Warnings, err.Error())
			continue
		}
		accounts = append(accounts, account)
		result.Counts.Credentials++
	}

	if len(result.Errors) > 0 {
		result.OK = false
		return nil, result, errors.New(strings.Join(result.Errors, "; "))
	}
	if len(accounts) == 0 {
		result.OK = false
		return nil, result, fmt.Errorf("no exportable accounts in selection")
	}

	payload := Export{
		Type:       "sub2api-data",
		Version:    1,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Proxies:    exporter.proxyList(),
		Accounts:   accounts,
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return encoded, result, nil
}

type exporter struct {
	store     *store.Store
	result    *Result
	proxies   map[string]Proxy
	proxyKeys map[string]string
}

func (e *exporter) accountFromCredential(ctx context.Context, provider store.Provider, credential store.Credential) (Account, error) {
	platform, accountType, err := sub2apiPlatformType(provider, credential)
	if err != nil {
		return Account{}, err
	}

	account := Account{
		Name:        firstNonEmpty(credential.Email, credential.Label, credential.ID),
		Platform:    platform,
		Type:        accountType,
		Credentials: map[string]any{},
	}
	if notes := strings.TrimSpace(stringFromAny(credential.Metadata["notes"])); notes != "" {
		account.Notes = notes
	}
	if account.Concurrency = metadataInt(credential.Metadata, "sub2api_concurrency"); account.Concurrency == 0 {
		account.Concurrency = metadataInt(credential.Metadata, "max_concurrent_requests")
	}
	if _, ok := credential.Metadata["sub2api_priority"]; ok {
		account.Priority = metadataInt(credential.Metadata, "sub2api_priority")
	} else if credential.Priority < 0 {
		account.Priority = -credential.Priority
	}
	if multiplier, ok := metadataFloat(credential.Metadata, "sub2api_rate_multiplier"); ok {
		account.RateMultiplier = &multiplier
	}
	if expiresAt, ok := metadataInt64(credential.Metadata, "sub2api_expires_at"); ok && expiresAt > 0 {
		account.ExpiresAt = &expiresAt
	}
	if extra, ok := credential.Metadata["sub2api_extra"].(map[string]any); ok && len(extra) > 0 {
		account.Extra = cloneMap(extra)
	}

	switch accountType {
	case "oauth", "oauth2", "oauth_token", "setup_token":
		if err := fillOAuthCredentials(&account, platform, credential); err != nil {
			return Account{}, err
		}
	case "apikey", "api_key":
		fillAPIKeyCredentials(&account, provider, credential)
	default:
		return Account{}, fmt.Errorf("credential %q has unsupported auth type %q", credential.ID, credential.AuthType)
	}

	if proxyKey := e.attachProxy(ctx, credential); proxyKey != "" {
		account.ProxyKey = &proxyKey
	}
	return account, nil
}

func sub2apiPlatformType(provider store.Provider, credential store.Credential) (platform, accountType string, err error) {
	providerID := strings.ToLower(strings.TrimSpace(provider.ID))
	providerType := strings.ToLower(strings.TrimSpace(provider.Type))
	authType := strings.ToLower(strings.TrimSpace(credential.AuthType))

	switch authType {
	case "oauth":
		accountType = "oauth"
	case "api_key":
		accountType = "apikey"
	default:
		return "", "", fmt.Errorf("credential %q auth type %q cannot be exported as sub2api", credential.ID, credential.AuthType)
	}

	if platform = stringFromAny(credential.Metadata["sub2api_platform"]); platform != "" {
		return platform, accountType, nil
	}

	switch {
	case providerID == "openai-api" || (providerType == "openai-compatible" && providerID != "codex"):
		return "openai", accountType, nil
	case providerType == "codex" || providerID == "codex":
		return "openai", accountType, nil
	case providerType == "claude" || providerID == "claude":
		return "anthropic", accountType, nil
	case providerType == "antigravity" || providerID == "antigravity":
		return "gemini", accountType, nil
	case providerType == "xai" || providerID == "xai":
		return "grok", accountType, nil
	default:
		return "", "", fmt.Errorf("credential %q provider %q is not supported for sub2api export", credential.ID, provider.ID)
	}
}

func fillOAuthCredentials(account *Account, platform string, credential store.Credential) error {
	token := credential.OAuthToken
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		if strings.TrimSpace(credential.Secret) == "" {
			return fmt.Errorf("credential %q has no oauth token", credential.ID)
		}
		token = &store.OAuthToken{AccessToken: credential.Secret, TokenType: credential.TokenType}
	}
	account.Credentials["access_token"] = token.AccessToken
	if token.RefreshToken != "" {
		account.Credentials["refresh_token"] = token.RefreshToken
	}
	if !token.ExpiresAt.IsZero() {
		account.Credentials["expires_at"] = token.ExpiresAt.UTC().Format(time.RFC3339)
	}
	email := firstNonEmpty(credential.Email, credential.Label)
	if email != "" {
		account.Credentials["email"] = email
	}
	for key, value := range token.Extra {
		switch key {
		case "account_id":
			if platform == "openai" {
				account.Credentials["chatgpt_account_id"] = value
			} else {
				account.Credentials["account_id"] = value
			}
		case "imported_from", "sub2api_platform":
		default:
			account.Credentials[key] = value
		}
	}
	return nil
}

func fillAPIKeyCredentials(account *Account, provider store.Provider, credential store.Credential) {
	if credential.Secret != "" {
		account.Credentials["api_key"] = credential.Secret
	}
	email := firstNonEmpty(credential.Email, credential.Label)
	if email != "" {
		account.Credentials["email"] = email
	}
	if baseURL := strings.TrimSpace(provider.BaseURL); baseURL != "" {
		account.Credentials["base_url"] = baseURL
	}
}

func (e *exporter) attachProxy(ctx context.Context, credential store.Credential) string {
	if len(credential.ProxyPoolIDs) == 0 {
		return ""
	}
	poolID := strings.TrimSpace(credential.ProxyPoolIDs[0])
	if poolID == "" {
		return ""
	}
	if proxyKey, ok := e.proxyKeys[poolID]; ok {
		return proxyKey
	}
	pool, err := e.store.ProxyPool(ctx, poolID)
	if err != nil || pool == nil || strings.TrimSpace(pool.URL) == "" {
		if err != nil {
			e.result.Warnings = append(e.result.Warnings, fmt.Sprintf("credential %q: proxy pool %q unavailable", credential.ID, poolID))
		}
		return ""
	}
	proxy, proxyKey, err := proxyFromURL(pool.Name, pool.Status, pool.URL)
	if err != nil {
		e.result.Warnings = append(e.result.Warnings, fmt.Sprintf("credential %q: proxy pool %q: %v", credential.ID, poolID, err))
		return ""
	}
	e.proxies[proxyKey] = proxy
	e.proxyKeys[poolID] = proxyKey
	e.result.Counts.ProxyPools = len(e.proxies)
	return proxyKey
}

func (e *exporter) proxyList() []Proxy {
	items := make([]Proxy, 0, len(e.proxies))
	for _, proxy := range e.proxies {
		items = append(items, proxy)
	}
	return items
}

func proxyFromURL(name, status, rawURL string) (Proxy, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return Proxy{}, "", fmt.Errorf("invalid proxy url")
	}
	protocol := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if protocol == "" {
		protocol = "http"
	}
	host := parsed.Hostname()
	port := 0
	if portString := parsed.Port(); portString != "" {
		fmt.Sscanf(portString, "%d", &port)
	} else {
		switch protocol {
		case "https":
			port = 443
		case "socks5", "socks5h":
			port = 1080
		default:
			port = 80
		}
	}
	username := ""
	password := ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	proxyKey := fmt.Sprintf("%s|%s|%d|%s|%s", protocol, host, port, username, password)
	proxy := Proxy{
		ProxyKey: proxyKey,
		Name:     firstNonEmpty(name, proxyKey),
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
		Status:   firstNonEmpty(status, "active"),
	}
	return proxy, proxyKey, nil
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func metadataInt(metadata map[string]any, key string) int {
	value, ok := metadataInt64(metadata, key)
	if !ok {
		return 0
	}
	return int(value)
}

func metadataInt64(metadata map[string]any, key string) (int64, bool) {
	switch typed := metadata[key].(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func metadataFloat(metadata map[string]any, key string) (float64, bool) {
	switch typed := metadata[key].(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func cloneMap(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
