package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

const kiroAuthServiceURL = "https://prod.us-east-1.auth.desktop.kiro.dev"

var kiroRefreshTokenPrefix = regexp.MustCompile(`^aorAAAAAG`)

var microsoftTokenEndpointHosts = map[string]struct{}{
	"login.microsoftonline.com": {},
	"login.microsoft.com":       {},
	"login.windows.net":         {},
}

type KiroAutoImportResult struct {
	Found        bool
	RefreshToken string
	ClientID     string
	ClientSecret string
	Region       string
	AuthMethod   string
	ProfileArn   string
	Source       string
	Err          error
}

func AutoImportKiro() KiroAutoImportResult {
	cachePath := filepath.Join(os.Getenv("HOME"), ".aws", "sso", "cache")
	entries, err := os.ReadDir(cachePath)
	if err != nil {
		return KiroAutoImportResult{Err: errors.New("AWS SSO cache not found. Please login to Kiro IDE first")}
	}

	var refreshToken, source string
	var tokenData map[string]any

	tryFile := func(name string) bool {
		data, readErr := os.ReadFile(filepath.Join(cachePath, name))
		if readErr != nil {
			return false
		}
		var parsed map[string]any
		if json.Unmarshal(data, &parsed) != nil {
			return false
		}
		token := strings.TrimSpace(stringValue(parsed["refreshToken"]))
		if token == "" || !kiroRefreshTokenPrefix.MatchString(token) {
			return false
		}
		refreshToken = token
		source = name
		tokenData = parsed
		return true
	}

	if !tryFile("kiro-auth-token.json") {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			if tryFile(entry.Name()) {
				break
			}
		}
	}
	if refreshToken == "" {
		return KiroAutoImportResult{Err: errors.New("Kiro token not found in AWS SSO cache. Please login to Kiro IDE first")}
	}

	result := KiroAutoImportResult{
		Found:        true,
		RefreshToken: refreshToken,
		Source:       source,
		Region:       strings.TrimSpace(stringValue(tokenData["region"])),
		AuthMethod:   strings.TrimSpace(stringValue(tokenData["authMethod"])),
	}
	if result.Region == "" {
		result.Region = kiroDefaultRegion
	}

	if hash := strings.TrimSpace(stringValue(tokenData["clientIdHash"])); hash != "" {
		clientData, readErr := os.ReadFile(filepath.Join(cachePath, hash+".json"))
		if readErr == nil {
			var clientInfo map[string]any
			if json.Unmarshal(clientData, &clientInfo) == nil {
				result.ClientID = strings.TrimSpace(stringValue(clientInfo["clientId"]))
				result.ClientSecret = strings.TrimSpace(stringValue(clientInfo["clientSecret"]))
			}
		}
	}

	for _, profilePath := range kiroProfilePaths() {
		data, readErr := os.ReadFile(profilePath)
		if readErr != nil {
			continue
		}
		var profile map[string]any
		if json.Unmarshal(data, &profile) != nil {
			continue
		}
		if arn := strings.TrimSpace(stringValue(profile["arn"])); arn != "" {
			result.ProfileArn = normalizeKiroProfileARNRegion(arn)
			break
		}
	}
	return result
}

func kiroProfilePaths() []string {
	home := os.Getenv("HOME")
	appData := os.Getenv("APPDATA")
	if appData == "" && home != "" {
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	paths := []string{}
	if appData != "" {
		paths = append(paths, filepath.Join(appData, "Kiro", "User", "globalStorage", "kiro.kiroagent", "profile.json"))
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".config", "Kiro", "User", "globalStorage", "kiro.kiroagent", "profile.json"))
	}
	return paths
}

func normalizeKiroProfileARNRegion(arn string) string {
	re := regexp.MustCompile(`^arn:aws:codewhisperer:[^:]+:`)
	if re.MatchString(arn) {
		return re.ReplaceAllString(arn, "arn:aws:codewhisperer:us-east-1:")
	}
	return arn
}

type KiroImportInput struct {
	RefreshToken string
	ClientID     string
	ClientSecret string
	Region       string
	AuthMethod   string
	ProfileArn   string
}

func (m *Manager) ImportKiroRefreshToken(ctx context.Context, input KiroImportInput) (store.OAuthToken, string, error) {
	refreshToken := strings.TrimSpace(input.RefreshToken)
	if refreshToken == "" {
		return store.OAuthToken{}, "", errors.New("refresh token is required")
	}
	region := strings.TrimSpace(input.Region)
	if region == "" {
		region = kiroDefaultRegion
	}
	if err := assertValidAWSRegion(region); err != nil {
		return store.OAuthToken{}, "", err
	}

	extra := map[string]any{
		"region": region,
	}
	isIDC := strings.TrimSpace(input.ClientID) != "" && strings.TrimSpace(input.ClientSecret) != ""
	authMethod := strings.TrimSpace(input.AuthMethod)
	if isIDC {
		extra["client_id"] = strings.TrimSpace(input.ClientID)
		extra["clientId"] = strings.TrimSpace(input.ClientID)
		extra["client_secret"] = strings.TrimSpace(input.ClientSecret)
		extra["clientSecret"] = strings.TrimSpace(input.ClientSecret)
		authMethod = "idc"
	} else if authMethod == "" {
		authMethod = "imported"
	}
	extra["auth_method"] = authMethod
	extra["authMethod"] = authMethod

	old := store.OAuthToken{RefreshToken: refreshToken, Extra: extra}
	token, err := m.refreshKiroToken(ctx, old)
	if err != nil {
		return store.OAuthToken{}, "", err
	}
	email := kiroEmailFromToken(token)
	profileARN := strings.TrimSpace(input.ProfileArn)
	if profileARN == "" {
		profileARN = credentialExtraFromToken(token, "profile_arn", "profileArn")
	}
	if profileARN == "" {
		profileARN, _ = fetchKiroProfileArn(ctx, m.client, token.AccessToken, region)
	}
	if profileARN != "" {
		if token.Extra == nil {
			token.Extra = map[string]any{}
		}
		token.Extra["profile_arn"] = profileARN
		token.Extra["profileArn"] = profileARN
	}
	if isIDC {
		token.Extra["provider"] = "Enterprise"
	} else if authMethod == "imported" {
		token.Extra["provider"] = "Imported"
	}
	return token, email, nil
}

func (m *Manager) ImportKiroAPIKey(ctx context.Context, apiKey, region string) (config.CredentialConfig, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return config.CredentialConfig{}, errors.New("API key is required")
	}
	if region == "" {
		region = kiroDefaultRegion
	}
	if err := assertValidAWSRegion(region); err != nil {
		return config.CredentialConfig{}, err
	}
	profileARN, err := fetchKiroProfileArn(ctx, m.client, apiKey, region)
	if err != nil {
		return config.CredentialConfig{}, fmt.Errorf("API key validation failed: %w", err)
	}
	email := kiroEmailFromAccessToken(apiKey)
	return config.CredentialConfig{
		AuthType: "api_key",
		Secret:   apiKey,
		Email:    email,
		Metadata: map[string]any{
			"profile_arn":  profileARN,
			"profileArn":   profileARN,
			"region":       region,
			"auth_method":  "api_key",
			"authMethod":   "api_key",
			"provider":     "API Key",
			"api_key":      apiKey,
		},
	}, nil
}

func ImportKiroExternalIdpJSON(raw string) (store.OAuthToken, string, error) {
	var input map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &input); err != nil {
		return store.OAuthToken{}, "", errors.New("CLIProxyAPI auth JSON is invalid")
	}
	authMethod := strings.TrimSpace(firstNonEmpty(stringValue(input["auth_method"]), stringValue(input["authMethod"])))
	if authMethod != "" && authMethod != "external_idp" {
		return store.OAuthToken{}, "", errors.New("only external_idp Kiro auth is supported by this importer")
	}
	accessToken := strings.TrimSpace(firstNonEmpty(stringValue(input["access_token"]), stringValue(input["accessToken"])))
	refreshToken := strings.TrimSpace(firstNonEmpty(stringValue(input["refresh_token"]), stringValue(input["refreshToken"])))
	clientID := strings.TrimSpace(firstNonEmpty(stringValue(input["client_id"]), stringValue(input["clientId"])))
	tokenEndpoint, err := validateMicrosoftTokenEndpoint(firstNonEmpty(stringValue(input["token_endpoint"]), stringValue(input["tokenEndpoint"])))
	if err != nil {
		return store.OAuthToken{}, "", err
	}
	profileARN := strings.TrimSpace(firstNonEmpty(stringValue(input["profile_arn"]), stringValue(input["profileArn"])))
	region := strings.TrimSpace(stringValue(input["region"]))
	if region == "" {
		region = kiroDefaultRegion
	}
	scope := normalizeKiroScope(input["scopes"], input["scope"])
	if accessToken == "" {
		return store.OAuthToken{}, "", errors.New("access_token is required")
	}
	if refreshToken == "" {
		return store.OAuthToken{}, "", errors.New("refresh_token is required")
	}
	if clientID == "" {
		return store.OAuthToken{}, "", errors.New("client_id is required")
	}
	if scope == "" {
		return store.OAuthToken{}, "", errors.New("scopes is required")
	}
	if profileARN == "" {
		return store.OAuthToken{}, "", errors.New("profile_arn is required")
	}
	email := firstNonEmpty(
		stringValue(input["email"]),
		kiroEmailFromAccessToken(accessToken),
	)
	token := store.OAuthToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresAt:    resolveKiroExpiresAt(input),
		Extra: map[string]any{
			"profile_arn":    profileARN,
			"profileArn":     profileARN,
			"region":         region,
			"auth_method":    "external_idp",
			"authMethod":     "external_idp",
			"provider":       "CLIProxyAPI",
			"client_id":      clientID,
			"clientId":       clientID,
			"token_endpoint": tokenEndpoint,
			"tokenEndpoint":  tokenEndpoint,
			"scope":          scope,
		},
	}
	return token, email, nil
}

func validateMicrosoftTokenEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("token_endpoint is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", errors.New("token_endpoint must be a valid https URL")
	}
	if _, ok := microsoftTokenEndpointHosts[strings.ToLower(parsed.Hostname())]; !ok {
		return "", errors.New("token_endpoint must be a Microsoft login endpoint")
	}
	return parsed.String(), nil
}

func normalizeKiroScope(scopes any, scope any) string {
	switch typed := scopes.(type) {
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if value := strings.TrimSpace(stringValue(item)); value != "" {
				parts = append(parts, value)
			}
		}
		return strings.Join(parts, " ")
	case []string:
		parts := make([]string, 0, len(typed))
		for _, value := range typed {
			if value = strings.TrimSpace(value); value != "" {
				parts = append(parts, value)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(stringValue(scope))
	}
}

func resolveKiroExpiresAt(input map[string]any) time.Time {
	for _, key := range []string{"expired", "expires_at", "expiresAt"} {
		if raw := strings.TrimSpace(stringValue(input[key])); raw != "" {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				return parsed
			}
		}
	}
	expiresIn := int(numberValue(input["expires_in"]))
	if expiresIn == 0 {
		expiresIn = int(numberValue(input["expiresIn"]))
	}
	if expiresIn > 0 {
		return time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	if claims := parseJWTClaims(strings.TrimSpace(firstNonEmpty(stringValue(input["access_token"]), stringValue(input["accessToken"])))); claims != nil {
		if exp := int(numberValue(claims["exp"])); exp > 0 {
			return time.Unix(int64(exp), 0)
		}
	}
	return time.Now().Add(time.Hour)
}

func fetchKiroProfileArn(ctx context.Context, client *http.Client, accessToken, region string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if region == "" {
		region = kiroDefaultRegion
	}
	endpoint := fmt.Sprintf("https://codewhisperer.%s.amazonaws.com", region)
	body, err := json.Marshal(map[string]any{"maxResults": 10})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonCodeWhispererService.ListAvailableProfiles")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("list profiles HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Profiles []map[string]any `json:"profiles"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	var fallback string
	for _, profile := range payload.Profiles {
		arn := strings.TrimSpace(firstNonEmpty(stringValue(profile["arn"]), stringValue(profile["profileArn"])))
		if arn == "" {
			continue
		}
		if fallback == "" {
			fallback = arn
		}
		parts := strings.Split(arn, ":")
		if len(parts) > 3 && parts[3] == region {
			return arn, nil
		}
	}
	if fallback == "" {
		return "", errors.New("no CodeWhisperer profile found")
	}
	return fallback, nil
}

func kiroEmailFromToken(token store.OAuthToken) string {
	if token.Extra != nil {
		if email := strings.TrimSpace(stringValue(token.Extra["email"])); email != "" {
			return email
		}
	}
	return kiroEmailFromAccessToken(token.AccessToken)
}

func kiroEmailFromAccessToken(accessToken string) string {
	claims := parseJWTClaims(accessToken)
	if claims == nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(
		stringValue(claims["email"]),
		stringValue(claims["preferred_username"]),
		stringValue(claims["upn"]),
		stringValue(claims["sub"]),
	))
}

func credentialExtraFromToken(token store.OAuthToken, keys ...string) string {
	if token.Extra == nil {
		return ""
	}
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(token.Extra[key])); value != "" {
			return value
		}
	}
	return ""
}

func (m *Manager) enrichKiroToken(ctx context.Context, token store.OAuthToken, email string) (store.OAuthToken, string, error) {
	if token.Extra == nil {
		token.Extra = map[string]any{}
	}
	if email == "" {
		email = kiroEmailFromToken(token)
	}
	profileARN := credentialExtraFromToken(token, "profile_arn", "profileArn")
	if profileARN == "" && token.AccessToken != "" {
		region := credentialExtraFromToken(token, "region")
		if region == "" {
			region = kiroDefaultRegion
		}
		if fetched, err := fetchKiroProfileArn(ctx, m.client, token.AccessToken, region); err == nil && fetched != "" {
			profileARN = fetched
			token.Extra["profile_arn"] = profileARN
			token.Extra["profileArn"] = profileARN
		}
	}
	authMethod := credentialExtraFromToken(token, "auth_method", "authMethod")
	if authMethod != "" && credentialExtraFromToken(token, "provider") == "" {
		switch authMethod {
		case "idc":
			token.Extra["provider"] = "Enterprise"
		case "builder-id":
			token.Extra["provider"] = "Builder ID"
		case "imported":
			token.Extra["provider"] = "Imported"
		case "api_key":
			token.Extra["provider"] = "API Key"
		case "external_idp":
			token.Extra["provider"] = "CLIProxyAPI"
		}
	}
	if email != "" {
		token.Extra["email"] = email
	}
	return token, email, nil
}
