package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

const (
	kimchiWebAppURL      = "https://app.kimchi.dev"
	kimchiValidationURL  = "https://api.cast.ai/v1/llm/openai/supported-providers"
	kimchiUserInfoURL    = "https://app.kimchi.dev/api/v1/me"
)

func kimchiAuthorizationURL(redirectURI, state string) string {
	baseURL := strings.TrimRight(kimchiWebAppURL, "/")
	params := url.Values{
		"callback": {redirectURI},
		"state":    {state},
	}
	return baseURL + "/cli-auth?" + params.Encode()
}

func (m *Manager) exchangeKimchiToken(ctx context.Context, tokenValue string) (store.OAuthToken, error) {
	tokenValue = strings.TrimSpace(tokenValue)
	if tokenValue == "" {
		return store.OAuthToken{}, &Error{code: "invalid_state", permanent: true}
	}
	if err := m.validateKimchiToken(ctx, tokenValue); err != nil {
		return store.OAuthToken{}, err
	}
	userInfo := m.fetchKimchiUserInfo(ctx, tokenValue)
	token := store.OAuthToken{
		AccessToken: tokenValue,
		TokenType:   "Bearer",
		Extra: map[string]any{
			"auth_method": "browser_token",
		},
	}
	if userID := strings.TrimSpace(stringValue(userInfo["id"])); userID != "" {
		token.Extra["user_id"] = userID
		token.Extra["userId"] = userID
	}
	if username := strings.TrimSpace(stringValue(userInfo["username"])); username != "" {
		token.Extra["username"] = username
	}
	if email := kimchiEmail(userInfo); email != "" {
		token.Extra["email"] = email
	}
	if name := strings.TrimSpace(stringValue(userInfo["name"])); name != "" {
		token.Extra["name"] = name
	}
	return token, nil
}

func (m *Manager) validateKimchiToken(ctx context.Context, tokenValue string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kimchiValidationURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenValue)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return &Error{code: "oauth_authorization_rejected", permanent: true, err: fmt.Errorf("kimchi token invalid or expired")}
	case http.StatusForbidden:
		return &Error{code: "oauth_authorization_rejected", permanent: true, err: fmt.Errorf("kimchi token lacks required scope")}
	default:
		return nil
	}
}

func (m *Manager) fetchKimchiUserInfo(ctx context.Context, tokenValue string) map[string]any {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kimchiUserInfoURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenValue)
	resp, err := m.client.Do(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	defer resp.Body.Close()
	var userInfo map[string]any
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&userInfo) != nil {
		return nil
	}
	return userInfo
}

func kimchiEmail(userInfo map[string]any) string {
	if userInfo == nil {
		return ""
	}
	if email := strings.TrimSpace(stringValue(userInfo["email"])); email != "" {
		return email
	}
	userID := strings.TrimSpace(stringValue(userInfo["id"]))
	if userID != "" {
		return "kimchi-user-" + userID
	}
	return ""
}
