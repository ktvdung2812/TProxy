package auth

import (
	"context"
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
	codebuddyRefreshURL = "https://copilot.tencent.com/v2/plugin/auth/token/refresh"
	codebuddyUserAgent  = "CLI/2.63.2 CodeBuddy/2.63.2"
	codebuddyPlatform   = "CLI"
)

var (
	codebuddyStateURL = "https://copilot.tencent.com/v2/plugin/auth/state"
	codebuddyTokenURL = "https://copilot.tencent.com/v2/plugin/auth/token"
)

type codebuddyDeviceStart struct {
	State           string
	VerificationURI string
	Interval        time.Duration
}

func initiateCodebuddyDeviceFlow() (codebuddyDeviceStart, error) {
	req, err := http.NewRequest(http.MethodPost, codebuddyStateURL+"?platform="+url.QueryEscape(codebuddyPlatform), strings.NewReader("{}"))
	if err != nil {
		return codebuddyDeviceStart{}, err
	}
	for key, value := range codebuddyRequestHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return codebuddyDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return codebuddyDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("codebuddy state HTTP %d", resp.StatusCode)}
	}
	var raw struct {
		Code int `json:"code"`
		Data struct {
			State   string `json:"state"`
			AuthURL string `json:"authUrl"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return codebuddyDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	if raw.Code != 0 || raw.Data.State == "" || raw.Data.AuthURL == "" {
		return codebuddyDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("codebuddy state error: %s", strings.TrimSpace(raw.Msg))}
	}
	return codebuddyDeviceStart{
		State:           raw.Data.State,
		VerificationURI: raw.Data.AuthURL,
		Interval:        5 * time.Second,
	}, nil
}

func (m *Manager) pollCodebuddyDevice(ctx context.Context, state string) (store.OAuthToken, bool, error) {
	target := codebuddyTokenURL + "?state=" + url.QueryEscape(state)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	for key, value := range codebuddyPollHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("codebuddy poll HTTP %d", resp.StatusCode)}
	}
	var raw struct {
		Code int `json:"code"`
		Data struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			TokenType    string `json:"tokenType"`
			ExpiresIn    int    `json:"expiresIn"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: err}
	}
	if raw.Code == 11217 {
		return store.OAuthToken{}, true, &Error{code: "authorization_pending"}
	}
	if raw.Code == 0 && raw.Data.AccessToken != "" {
		expiresAt := time.Time{}
		if raw.Data.ExpiresIn > 0 {
			expiresAt = m.now().Add(time.Duration(raw.Data.ExpiresIn) * time.Second)
		}
		tokenType := strings.TrimSpace(raw.Data.TokenType)
		if tokenType == "" {
			tokenType = "Bearer"
		}
		return store.OAuthToken{
			AccessToken:  raw.Data.AccessToken,
			RefreshToken: raw.Data.RefreshToken,
			TokenType:    tokenType,
			ExpiresAt:    expiresAt,
		}, false, nil
	}
	return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("codebuddy poll: %s", strings.TrimSpace(raw.Msg))}
}

func (m *Manager) refreshCodebuddyToken(ctx context.Context, refreshToken string, cfg config.OAuthConfig) (store.OAuthToken, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return store.OAuthToken{}, &Error{code: "authorization_required", permanent: true}
	}
	target := strings.TrimSpace(cfg.RefreshURL)
	if target == "" {
		target = codebuddyRefreshURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader("{}"))
	if err != nil {
		return store.OAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codebuddyUserAgent)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Domain", "copilot.tencent.com")
	req.Header.Set("X-Refresh-Token", refreshToken)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	req.Header.Set("X-Product", "SaaS")
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(body, resp.StatusCode, false)
	}
	var raw struct {
		Code int `json:"code"`
		Data struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresIn    int    `json:"expiresIn"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	if raw.Code != 0 || raw.Data.AccessToken == "" {
		return store.OAuthToken{}, &Error{code: "oauth_refresh_failed", err: fmt.Errorf("codebuddy refresh: %s", strings.TrimSpace(raw.Msg))}
	}
	expiresAt := time.Time{}
	if raw.Data.ExpiresIn > 0 {
		expiresAt = m.now().Add(time.Duration(raw.Data.ExpiresIn) * time.Second)
	}
	return store.OAuthToken{
		AccessToken:  raw.Data.AccessToken,
		RefreshToken: firstNonEmpty(raw.Data.RefreshToken, refreshToken),
		TokenType:    "Bearer",
		ExpiresAt:    expiresAt,
	}, nil
}

func codebuddyRequestHeaders() map[string]string {
	return map[string]string{
		"Content-Type":       "application/json",
		"Accept":             "application/json",
		"User-Agent":         codebuddyUserAgent,
		"X-Requested-With":   "XMLHttpRequest",
		"X-Domain":           "copilot.tencent.com",
		"X-No-Authorization": "true",
		"X-No-User-Id":       "true",
		"X-Product":          "SaaS",
	}
}

func codebuddyPollHeaders() map[string]string {
	headers := codebuddyRequestHeaders()
	headers["X-No-Enterprise-Id"] = "true"
	headers["X-No-Department-Info"] = "true"
	return headers
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
