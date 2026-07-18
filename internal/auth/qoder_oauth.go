package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/store"
)

const qoderDevicePollURL = "https://openapi.qoder.sh/api/v1/deviceToken/poll"
const qoderLoginURL = "https://qoder.com/device/selectAccounts"
const qoderUserInfoURL = "https://openapi.qoder.sh/api/v1/userinfo"

type qoderDeviceStart struct {
	VerificationURI string
	Nonce           string
	CodeVerifier    string
	MachineID       string
}

func initiateQoderDeviceFlow() (qoderDeviceStart, error) {
	verifier, err := pkceVerifier()
	if err != nil {
		return qoderDeviceStart{}, err
	}
	challenge := pkceChallenge(verifier)
	nonce := uuid.NewString()
	machineID := uuid.NewString()
	params := url.Values{
		"challenge":        {challenge},
		"challenge_method": {"S256"},
		"machine_id":       {machineID},
		"nonce":            {nonce},
	}
	return qoderDeviceStart{
		VerificationURI: qoderLoginURL + "?" + params.Encode(),
		Nonce:           nonce,
		CodeVerifier:    verifier,
		MachineID:       machineID,
	}, nil
}

func (m *Manager) pollQoderDevice(ctx context.Context, nonce, verifier, machineID string) (store.OAuthToken, bool, error) {
	target := fmt.Sprintf("%s?nonce=%s&verifier=%s&challenge_method=S256",
		qoderDevicePollURL,
		url.QueryEscape(nonce),
		url.QueryEscape(verifier),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Go-http-client/2.0")
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNotFound {
		return store.OAuthToken{}, true, &Error{code: "authorization_pending"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("qoder poll HTTP %d", resp.StatusCode)}
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: err}
	}
	tokenValue := stringValue(raw["token"])
	if tokenValue == "" {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("qoder poll returned no token")}
	}
	expiresAt := parseQoderExpiry(raw["expires_at"], int(numberValue(raw["expires_in"])))
	oauthToken := store.OAuthToken{
		AccessToken:  tokenValue,
		RefreshToken: stringValue(raw["refresh_token"]),
		TokenType:    "Bearer",
		ExpiresAt:    expiresAt,
		Extra: map[string]any{
			"user_id":    stringValue(raw["user_id"]),
			"userId":     stringValue(raw["user_id"]),
			"machine_id": machineID,
			"machineId":  machineID,
		},
	}
	oauthToken = m.enrichQoderToken(ctx, oauthToken)
	return oauthToken, false, nil
}

func parseQoderExpiry(expiresAt any, expiresIn int) time.Time {
	now := time.Now()
	switch v := expiresAt.(type) {
	case float64:
		if v > 0 {
			return time.UnixMilli(int64(v))
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			if isDigits(trimmed) {
				if ms, err := strconv.ParseInt(trimmed, 10, 64); err == nil && ms > 0 {
					return time.UnixMilli(ms)
				}
			}
			if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
				return t
			}
		}
	}
	if expiresIn >= 0 {
		return now.Add(time.Duration(expiresIn) * time.Second)
	}
	return now.Add(30 * 24 * time.Hour)
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func (m *Manager) enrichQoderToken(ctx context.Context, token store.OAuthToken) store.OAuthToken {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qoderUserInfoURL, nil)
	if err != nil {
		return token
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return token
	}
	defer resp.Body.Close()
	var raw map[string]any
	if json.NewDecoder(resp.Body).Decode(&raw) != nil {
		return token
	}
	if token.Extra == nil {
		token.Extra = map[string]any{}
	}
	if name := strings.TrimSpace(stringValue(firstValue(raw, "name", "username"))); name != "" {
		token.Extra["name"] = name
	}
	if email := strings.TrimSpace(stringValue(raw["email"])); email != "" {
		token.Extra["email"] = email
	}
	return token
}
