package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

const gitlabDefaultBaseURL = "https://gitlab.com"

type gitlabOAuthConfig struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
}

func gitlabOAuthFromProvider(provider store.Provider, oauthClientID, oauthClientSecret string) gitlabOAuthConfig {
	cfg := gitlabOAuthConfig{
		BaseURL:      gitlabDefaultBaseURL,
		ClientID:     strings.TrimSpace(oauthClientID),
		ClientSecret: strings.TrimSpace(oauthClientSecret),
	}
	if provider.Config != nil {
		if base := strings.TrimSpace(stringValue(provider.Config["gitlab_base_url"])); base != "" {
			cfg.BaseURL = strings.TrimRight(base, "/")
		}
		if clientID := strings.TrimSpace(stringValue(provider.Config["gitlab_client_id"])); clientID != "" {
			cfg.ClientID = clientID
		}
		if secret := strings.TrimSpace(stringValue(provider.Config["gitlab_client_secret"])); secret != "" {
			cfg.ClientSecret = secret
		}
	}
	if cfg.ClientID == "" {
		cfg.ClientID = strings.TrimSpace(oauthClientID)
	}
	if cfg.ClientSecret == "" {
		cfg.ClientSecret = strings.TrimSpace(oauthClientSecret)
	}
	return cfg
}

func gitlabAuthorizationURL(cfg gitlabOAuthConfig, redirectURI, state, codeChallenge string) string {
	params := url.Values{
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"state":                 {state},
		"scope":                 {"api read_user"},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return cfg.BaseURL + "/oauth/authorize?" + params.Encode()
}

func (m *Manager) exchangeGitlabCode(ctx context.Context, cfg gitlabOAuthConfig, code, redirectURI, verifier string) (store.OAuthToken, error) {
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"grant_type":    {"authorization_code"},
		"code":          {strings.TrimSpace(code)},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/oauth/token", strings.NewReader(form.Encode()))
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
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(body, resp.StatusCode, false)
	}
	token, err := parseToken(body, m.now())
	if err != nil {
		return store.OAuthToken{}, err
	}
	if token.Extra == nil {
		token.Extra = map[string]any{}
	}
	token.Extra["base_url"] = cfg.BaseURL
	token.Extra["client_id"] = cfg.ClientID
	token.Extra["auth_kind"] = "oauth"
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BaseURL+"/api/v4/user", nil)
	if err == nil {
		userReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
		userReq.Header.Set("Accept", "application/json")
		if userResp, userErr := m.client.Do(userReq); userErr == nil {
			defer userResp.Body.Close()
			if userResp.StatusCode >= 200 && userResp.StatusCode < 300 {
				var user map[string]any
				if json.NewDecoder(io.LimitReader(userResp.Body, 1<<20)).Decode(&user) == nil {
					if username := strings.TrimSpace(stringValue(user["username"])); username != "" {
						token.Extra["username"] = username
					}
					if email := strings.TrimSpace(firstNonEmpty(stringValue(user["email"]), stringValue(user["public_email"]))); email != "" {
						token.Extra["email"] = email
					}
					if name := strings.TrimSpace(stringValue(user["name"])); name != "" {
						token.Extra["name"] = name
					}
				}
			}
		}
	}
	return token, nil
}

func (m *Manager) refreshGitlabToken(ctx context.Context, refreshToken string, cfg gitlabOAuthConfig) (store.OAuthToken, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return store.OAuthToken{}, &Error{code: "authorization_required", permanent: true}
	}
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/oauth/token", strings.NewReader(form.Encode()))
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
	if token.Extra == nil {
		token.Extra = map[string]any{}
	}
	token.Extra["base_url"] = cfg.BaseURL
	token.Extra["client_id"] = cfg.ClientID
	token.Extra["auth_kind"] = "oauth"
	return token, nil
}
