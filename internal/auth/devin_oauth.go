package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	devinwire "github.com/tproxy/tproxy/internal/providers/devin"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	devinAppBaseURL    = "https://app.devin.ai"
	devinAPIBaseURL    = "https://api.devin.ai"
	devinServerBaseURL = "https://server.codeium.com"
)

// devinAuthorizationURL builds the Devin CLI PKCE login URL in headless
// manual-code mode (cli_pkce_marker=1, no redirect_uri), matching the official
// Devin CLI binary — app.devin.ai rejects unregistered redirect URIs.
func devinAuthorizationURL(codeChallenge, state string) string {
	parts := []string{
		"state=" + url.QueryEscape(state),
		"prompt=select_account",
		"code_challenge=" + url.QueryEscape(codeChallenge),
		"code_challenge_method=S256",
		"cli_pkce_marker=1",
	}
	return devinAppBaseURL + "/auth/cli/continue?" + strings.Join(parts, "&")
}

// exchangeDevinCode swaps the pasted authorization code for a Devin session
// token, then enriches it with profile and plan metadata.
func (m *Manager) exchangeDevinCode(ctx context.Context, code, verifier string) (store.OAuthToken, error) {
	code, verifier = strings.TrimSpace(code), strings.TrimSpace(verifier)
	if code == "" || verifier == "" {
		return store.OAuthToken{}, &Error{code: "invalid_state", permanent: true}
	}
	rawToken, err := m.devinTokenExchange(ctx, code, verifier)
	if err != nil {
		return store.OAuthToken{}, err
	}
	sessionToken := devinSessionToken(rawToken)
	token := store.OAuthToken{
		AccessToken: sessionToken,
		TokenType:   "Bearer",
		Extra:       map[string]any{"auth_method": "pkce"},
	}
	if name, userID, orgID := m.fetchDevinSelfProfile(ctx, sessionToken); name != "" || userID != "" {
		token.Extra["user_name"] = name
		token.Extra["user_id"] = userID
		token.Extra["org_id"] = orgID
	}
	if status := m.fetchDevinUserStatus(ctx, sessionToken); status != nil {
		if status.Email != "" {
			token.Extra["email"] = status.Email
		}
		if status.Plan != "" {
			token.Extra["plan"] = status.Plan
		}
	}
	return token, nil
}

func (m *Manager) devinTokenExchange(ctx context.Context, code, verifier string) (string, error) {
	body, err := json.Marshal(map[string]string{"code": code, "code_verifier": verifier})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, devinAPIBaseURL+"/auth/cli/token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return "", &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", &Error{code: "oauth_provider_unavailable", err: err}
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return "", &Error{code: "oauth_authorization_rejected", permanent: true, err: fmt.Errorf("devin token exchange rejected with status %d", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("devin token exchange failed with status %d", response.StatusCode)}
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(data, &payload); err != nil {
		return "", &Error{code: "oauth_provider_unavailable", err: errors.New("invalid devin token exchange response")}
	}
	if token := strings.TrimSpace(payload.Token); token != "" {
		return token, nil
	}
	return "", &Error{code: "oauth_provider_unavailable", err: errors.New("devin token exchange returned no token")}
}

// devinSessionToken normalizes a raw CLI token into the devin-session-token$
// form the reasoning backend expects.
func devinSessionToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.HasPrefix(raw, "devin-session-token$") {
		return "devin-session-token$" + raw
	}
	return raw
}

func (m *Manager) fetchDevinSelfProfile(ctx context.Context, sessionToken string) (userName, userID, orgID string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, devinAPIBaseURL+"/v3/self", nil)
	if err != nil {
		return "", "", ""
	}
	request.Header.Set("Authorization", "Bearer "+sessionToken)
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return "", "", ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", ""
	}
	var payload struct {
		UserName string `json:"user_name"`
		UserID   string `json:"user_id"`
		OrgID    string `json:"org_id"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", "", ""
	}
	return strings.TrimSpace(payload.UserName), strings.TrimSpace(payload.UserID), strings.TrimSpace(payload.OrgID)
}

// fetchDevinUserStatus calls the unary SeatManagementService/GetUserStatus RPC
// (raw protobuf body, application/proto — same shape as the quota probe).
func (m *Manager) fetchDevinUserStatus(ctx context.Context, sessionToken string) *devinwire.UserStatus {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, devinServerBaseURL+devinwire.UserStatusPath,
		bytes.NewReader(devinwire.BuildGetUserStatusRequest(sessionToken)))
	if err != nil {
		return nil
	}
	request.Header.Set("Authorization", "Basic "+sessionToken+"-"+sessionToken)
	request.Header.Set("Content-Type", "application/proto")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("Accept", "*/*")
	request.Header["User-Agent"] = []string{""}
	response, err := m.client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil
	}
	status, err := devinwire.ParseGetUserStatusResponse(devinwire.MaybeGunzip(body))
	if err != nil {
		return nil
	}
	return status
}
