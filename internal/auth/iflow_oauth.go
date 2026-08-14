package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	iflowAuthorizeURL = "https://iflow.cn/oauth"
	iflowTokenURL     = "https://iflow.cn/oauth/token"
	iflowUserInfoURL  = "https://iflow.cn/api/oauth/getUserInfo"
	iflowClientID     = "10009311001"
	iflowClientSecret = "4Z3YjXycVsQvyGF1etiNlIBB4RsqSDtW"
)

func iflowAuthorizationURL(redirectURI, state, clientID string) string {
	params := url.Values{
		"loginMethod": {"phone"},
		"type":        {"phone"},
		"redirect":    {redirectURI},
		"state":       {state},
		"client_id":   {clientID},
	}
	return iflowAuthorizeURL + "?" + params.Encode()
}

func (m *Manager) exchangeIflowCode(ctx context.Context, code, redirectURI, clientID, clientSecret string) (store.OAuthToken, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return store.OAuthToken{}, &Error{code: "invalid_state", permanent: true}
	}
	if clientID == "" {
		clientID = iflowClientID
	}
	if clientSecret == "" {
		clientSecret = iflowClientSecret
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, iflowTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return store.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret)))
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(body, resp.StatusCode, false)
	}
	token, err := parseToken(body, m.now())
	if err != nil {
		return store.OAuthToken{}, err
	}
	return m.enrichIflowToken(ctx, token, clientID, clientSecret)
}

func (m *Manager) refreshIflowToken(ctx context.Context, refreshToken string, cfg config.OAuthConfig) (store.OAuthToken, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return store.OAuthToken{}, &Error{code: "authorization_required", permanent: true}
	}
	clientID := m.clientID(cfg)
	clientSecret := m.clientSecret(cfg)
	if clientID == "" {
		clientID = iflowClientID
	}
	if clientSecret == "" {
		clientSecret = iflowClientSecret
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, iflowTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return store.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret)))
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(body, resp.StatusCode, false)
	}
	token, err := parseToken(body, m.now())
	if err != nil {
		return store.OAuthToken{}, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return m.enrichIflowToken(ctx, token, clientID, clientSecret)
}

func (m *Manager) enrichIflowToken(ctx context.Context, token store.OAuthToken, clientID, clientSecret string) (store.OAuthToken, error) {
	if token.AccessToken == "" {
		return token, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("iflow token exchange returned no access token")}
	}
	target := iflowUserInfoURL + "?accessToken=" + url.QueryEscape(token.AccessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return token, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return token, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return token, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("iflow user info HTTP %d", resp.StatusCode)}
	}
	var raw struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			APIKey   string `json:"apiKey"`
			Email    string `json:"email"`
			Phone    string `json:"phone"`
			Nickname string `json:"nickname"`
			Name     string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return token, &Error{code: "oauth_provider_unavailable", err: err}
	}
	if !raw.Success {
		return token, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("iflow user info: %s", strings.TrimSpace(raw.Message))}
	}
	apiKey := strings.TrimSpace(raw.Data.APIKey)
	if apiKey == "" {
		return token, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("iflow returned empty API key")}
	}
	email := strings.TrimSpace(raw.Data.Email)
	if email == "" {
		email = strings.TrimSpace(raw.Data.Phone)
	}
	if token.Extra == nil {
		token.Extra = map[string]any{}
	}
	token.Extra["api_key"] = apiKey
	token.Extra["apiKey"] = apiKey
	token.Extra["oauth_access_token"] = token.AccessToken
	if email != "" {
		token.Extra["email"] = email
	}
	if name := strings.TrimSpace(firstNonEmpty(raw.Data.Nickname, raw.Data.Name)); name != "" {
		token.Extra["name"] = name
	}
	token.AccessToken = apiKey
	if token.ExpiresAt.IsZero() {
		token.ExpiresAt = m.now().Add(24 * time.Hour)
	}
	return token, nil
}
