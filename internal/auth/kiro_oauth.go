package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

const (
	kiroDefaultRegion  = "us-east-1"
	kiroDefaultStartURL = "https://view.awsapps.com/start"
	kiroClientName     = "kiro-oauth-client"
	kiroClientType     = "public"
	kiroIssuerURL      = "https://identitycenter.amazonaws.com/ssoins-722374e8c3c8e6c6"
)

var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d{1,2}$`)

type kiroDeviceStart struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	Interval        time.Duration
	ClientID        string
	ClientSecret    string
	Region          string
	AuthMethod      string
	StartURL        string
}

func assertValidAWSRegion(region string) error {
	region = strings.TrimSpace(region)
	if !awsRegionPattern.MatchString(region) {
		return fmt.Errorf("invalid AWS region %q", region)
	}
	return nil
}

func kiroRegionFromMeta(meta map[string]string) string {
	if meta == nil {
		return kiroDefaultRegion
	}
	region := strings.TrimSpace(meta["region"])
	if region == "" {
		return kiroDefaultRegion
	}
	if err := assertValidAWSRegion(region); err != nil {
		return kiroDefaultRegion
	}
	return region
}

func initiateKiroDeviceFlow(region, startURL, authMethod string) (kiroDeviceStart, error) {
	if err := assertValidAWSRegion(region); err != nil {
		return kiroDeviceStart{}, &Error{code: "oauth_configuration_invalid", err: err}
	}
	if strings.TrimSpace(startURL) == "" {
		startURL = kiroDefaultStartURL
	}
	if strings.TrimSpace(authMethod) == "" {
		authMethod = "builder-id"
	}
	registerURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/client/register", region)
	deviceURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/device_authorization", region)
	registerBody := map[string]any{
		"clientName": kiroClientName,
		"clientType": kiroClientType,
		"scopes": []string{
			"codewhisperer:completions",
			"codewhisperer:analysis",
			"codewhisperer:conversations",
		},
		"grantTypes": []string{
			"urn:ietf:params:oauth:grant-type:device_code",
			"refresh_token",
		},
		"issuerUrl": kiroIssuerURL,
	}
	registerPayload, err := json.Marshal(registerBody)
	if err != nil {
		return kiroDeviceStart{}, err
	}
	registerReq, err := http.NewRequest(http.MethodPost, registerURL, strings.NewReader(string(registerPayload)))
	if err != nil {
		return kiroDeviceStart{}, err
	}
	registerReq.Header.Set("Content-Type", "application/json")
	registerReq.Header.Set("Accept", "application/json")
	registerResp, err := http.DefaultClient.Do(registerReq)
	if err != nil {
		return kiroDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer registerResp.Body.Close()
	registerData, _ := io.ReadAll(io.LimitReader(registerResp.Body, 1<<20))
	if registerResp.StatusCode < 200 || registerResp.StatusCode >= 300 {
		return kiroDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kiro client registration HTTP %d", registerResp.StatusCode)}
	}
	var clientInfo struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := json.Unmarshal(registerData, &clientInfo); err != nil || clientInfo.ClientID == "" || clientInfo.ClientSecret == "" {
		return kiroDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kiro client registration returned incomplete data")}
	}
	deviceBody := map[string]any{
		"clientId":     clientInfo.ClientID,
		"clientSecret": clientInfo.ClientSecret,
		"startUrl":     startURL,
	}
	devicePayload, err := json.Marshal(deviceBody)
	if err != nil {
		return kiroDeviceStart{}, err
	}
	deviceReq, err := http.NewRequest(http.MethodPost, deviceURL, strings.NewReader(string(devicePayload)))
	if err != nil {
		return kiroDeviceStart{}, err
	}
	deviceReq.Header.Set("Content-Type", "application/json")
	deviceReq.Header.Set("Accept", "application/json")
	deviceResp, err := http.DefaultClient.Do(deviceReq)
	if err != nil {
		return kiroDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer deviceResp.Body.Close()
	deviceData, _ := io.ReadAll(io.LimitReader(deviceResp.Body, 1<<20))
	if deviceResp.StatusCode < 200 || deviceResp.StatusCode >= 300 {
		return kiroDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kiro device authorization HTTP %d", deviceResp.StatusCode)}
	}
	var device struct {
		DeviceCode              string `json:"deviceCode"`
		UserCode                string `json:"userCode"`
		VerificationURI         string `json:"verificationUri"`
		VerificationURIComplete string `json:"verificationUriComplete"`
		ExpiresIn               int    `json:"expiresIn"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(deviceData, &device); err != nil || device.DeviceCode == "" {
		return kiroDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kiro device authorization returned incomplete data")}
	}
	verificationURI := strings.TrimSpace(device.VerificationURIComplete)
	if verificationURI == "" {
		verificationURI = strings.TrimSpace(device.VerificationURI)
	}
	interval := time.Duration(device.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return kiroDeviceStart{
		DeviceCode:      device.DeviceCode,
		UserCode:        device.UserCode,
		VerificationURI: verificationURI,
		Interval:        interval,
		ClientID:        clientInfo.ClientID,
		ClientSecret:    clientInfo.ClientSecret,
		Region:          region,
		AuthMethod:      authMethod,
		StartURL:        startURL,
	}, nil
}

func (m *Manager) pollKiroDevice(ctx context.Context, deviceCode string, meta map[string]string) (store.OAuthToken, bool, error) {
	region := kiroRegionFromMeta(meta)
	clientID := strings.TrimSpace(meta["client_id"])
	clientSecret := strings.TrimSpace(meta["client_secret"])
	if clientID == "" || clientSecret == "" || deviceCode == "" {
		return store.OAuthToken{}, false, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	tokenURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
	body := map[string]any{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"deviceCode":   deviceCode,
		"grantType":    "urn:ietf:params:oauth:grant-type:device_code",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: err}
	}
	if accessToken := strings.TrimSpace(stringValue(raw["accessToken"])); accessToken != "" {
		token := store.OAuthToken{
			AccessToken:  accessToken,
			RefreshToken: stringValue(raw["refreshToken"]),
			TokenType:    "Bearer",
			Extra: map[string]any{
				"client_id":     clientID,
				"clientId":      clientID,
				"client_secret": clientSecret,
				"clientSecret":  clientSecret,
				"region":        region,
				"auth_method":   strings.TrimSpace(meta["auth_method"]),
				"authMethod":    strings.TrimSpace(meta["auth_method"]),
				"start_url":     strings.TrimSpace(meta["start_url"]),
				"startUrl":      strings.TrimSpace(meta["start_url"]),
			},
		}
		if profileARN := strings.TrimSpace(stringValue(raw["profileArn"])); profileARN != "" {
			token.Extra["profile_arn"] = profileARN
			token.Extra["profileArn"] = profileARN
		}
		if expiresIn := int(numberValue(raw["expiresIn"])); expiresIn > 0 {
			token.ExpiresAt = m.now().Add(time.Duration(expiresIn) * time.Second)
		}
		if claims := parseJWTClaims(accessToken); claims != nil {
			if email := strings.TrimSpace(stringValue(claims["email"])); email != "" {
				token.Extra["email"] = email
			}
		}
		return token, false, nil
	}
	errorCode := strings.TrimSpace(stringValue(raw["error"]))
	if errorCode == "" {
		errorCode = strings.TrimSpace(stringValue(raw["message"]))
	}
	if errorCode == "authorization_pending" || errorCode == "slow_down" {
		return store.OAuthToken{}, true, &Error{code: errorCode}
	}
	return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kiro poll: %s", errorCode)}
}

func (m *Manager) refreshKiroToken(ctx context.Context, old store.OAuthToken) (store.OAuthToken, error) {
	refreshToken := strings.TrimSpace(old.RefreshToken)
	if refreshToken == "" {
		return store.OAuthToken{}, &Error{code: "authorization_required", permanent: true}
	}
	extra := old.Extra
	if extra == nil {
		extra = map[string]any{}
	}
	authMethod := strings.TrimSpace(firstNonEmpty(stringValue(extra["auth_method"]), stringValue(extra["authMethod"])))
	if authMethod == "external_idp" {
		return m.refreshKiroExternalIdpToken(ctx, refreshToken, extra)
	}
	clientID := strings.TrimSpace(firstNonEmpty(stringValue(extra["client_id"]), stringValue(extra["clientId"])))
	clientSecret := strings.TrimSpace(firstNonEmpty(stringValue(extra["client_secret"]), stringValue(extra["clientSecret"])))
	if clientID != "" && clientSecret != "" {
		return m.refreshKiroAWSOIDCToken(ctx, refreshToken, clientID, clientSecret, extra)
	}
	return m.refreshKiroSocialToken(ctx, refreshToken, extra)
}

func (m *Manager) refreshKiroAWSOIDCToken(ctx context.Context, refreshToken, clientID, clientSecret string, extra map[string]any) (store.OAuthToken, error) {
	region := strings.TrimSpace(stringValue(extra["region"]))
	if region == "" {
		region = kiroDefaultRegion
	}
	tokenURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
	body := map[string]any{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return store.OAuthToken{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return store.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(data, resp.StatusCode, false)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	accessToken := strings.TrimSpace(stringValue(raw["accessToken"]))
	if accessToken == "" {
		return store.OAuthToken{}, &Error{code: "oauth_refresh_failed"}
	}
	token := store.OAuthToken{
		AccessToken:  accessToken,
		RefreshToken: firstNonEmpty(stringValue(raw["refreshToken"]), refreshToken),
		TokenType:    "Bearer",
		Extra:        map[string]any{},
	}
	for key, value := range extra {
		token.Extra[key] = value
	}
	if profileARN := strings.TrimSpace(stringValue(raw["profileArn"])); profileARN != "" {
		token.Extra["profile_arn"] = profileARN
		token.Extra["profileArn"] = profileARN
	}
	if expiresIn := int(numberValue(raw["expiresIn"])); expiresIn > 0 {
		token.ExpiresAt = m.now().Add(time.Duration(expiresIn) * time.Second)
	}
	return token, nil
}

func (m *Manager) refreshKiroSocialToken(ctx context.Context, refreshToken string, extra map[string]any) (store.OAuthToken, error) {
	body, err := json.Marshal(map[string]string{"refreshToken": refreshToken})
	if err != nil {
		return store.OAuthToken{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kiroAuthServiceURL+"/refreshToken", strings.NewReader(string(body)))
	if err != nil {
		return store.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(data, resp.StatusCode, false)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	accessToken := strings.TrimSpace(stringValue(raw["accessToken"]))
	if accessToken == "" {
		return store.OAuthToken{}, &Error{code: "oauth_refresh_failed"}
	}
	token := store.OAuthToken{
		AccessToken:  accessToken,
		RefreshToken: firstNonEmpty(stringValue(raw["refreshToken"]), refreshToken),
		TokenType:    "Bearer",
		Extra:        map[string]any{},
	}
	for key, value := range extra {
		token.Extra[key] = value
	}
	if profileARN := strings.TrimSpace(stringValue(raw["profileArn"])); profileARN != "" {
		token.Extra["profile_arn"] = profileARN
		token.Extra["profileArn"] = profileARN
	}
	expiresIn := int(numberValue(raw["expiresIn"]))
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	token.ExpiresAt = m.now().Add(time.Duration(expiresIn) * time.Second)
	return token, nil
}

func (m *Manager) refreshKiroExternalIdpToken(ctx context.Context, refreshToken string, extra map[string]any) (store.OAuthToken, error) {
	tokenEndpoint, err := validateMicrosoftTokenEndpoint(firstNonEmpty(stringValue(extra["token_endpoint"]), stringValue(extra["tokenEndpoint"])))
	if err != nil {
		return store.OAuthToken{}, &Error{code: "authorization_required", permanent: true, err: err}
	}
	clientID := strings.TrimSpace(firstNonEmpty(stringValue(extra["client_id"]), stringValue(extra["clientId"])))
	scope := normalizeKiroScope(extra["scopes"], extra["scope"])
	if clientID == "" || scope == "" {
		return store.OAuthToken{}, &Error{code: "authorization_required", permanent: true}
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
		"scope":         {scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return store.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(data, resp.StatusCode, false)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	accessToken := strings.TrimSpace(firstNonEmpty(stringValue(raw["access_token"]), stringValue(raw["accessToken"])))
	if accessToken == "" {
		return store.OAuthToken{}, &Error{code: "oauth_refresh_failed"}
	}
	token := store.OAuthToken{
		AccessToken:  accessToken,
		RefreshToken: firstNonEmpty(stringValue(raw["refresh_token"]), stringValue(raw["refreshToken"]), refreshToken),
		TokenType:    firstNonEmpty(stringValue(raw["token_type"]), "Bearer"),
		Extra:        map[string]any{},
	}
	for key, value := range extra {
		token.Extra[key] = value
	}
	expiresIn := int(numberValue(raw["expires_in"]))
	if expiresIn == 0 {
		expiresIn = int(numberValue(raw["expiresIn"]))
	}
	if expiresIn > 0 {
		token.ExpiresAt = m.now().Add(time.Duration(expiresIn) * time.Second)
	}
	return token, nil
}

func kiroDeviceMeta(start kiroDeviceStart) map[string]string {
	return map[string]string{
		"client_id":     start.ClientID,
		"client_secret": start.ClientSecret,
		"region":        start.Region,
		"auth_method":   start.AuthMethod,
		"start_url":     start.StartURL,
	}
}
