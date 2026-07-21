package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

var (
	kilocodeInitiateURL = "https://api.kilo.ai/api/device-auth/codes"
	kilocodePollURLBase = "https://api.kilo.ai/api/device-auth/codes"
)

const kilocodeAPIBaseURL = "https://api.kilo.ai"

type kilocodeDeviceStart struct {
	Code            string
	VerificationURI string
	Interval        int
}

func initiateKilocodeDeviceFlow() (kilocodeDeviceStart, error) {
	req, err := http.NewRequest(http.MethodPost, kilocodeInitiateURL, nil)
	if err != nil {
		return kilocodeDeviceStart{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return kilocodeDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		return kilocodeDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kilocode: too many pending authorization requests")}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return kilocodeDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kilocode initiate HTTP %d", resp.StatusCode)}
	}
	var raw struct {
		Code             string `json:"code"`
		VerificationURL  string `json:"verificationUrl"`
		ExpiresIn        int    `json:"expiresIn"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return kilocodeDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	if raw.Code == "" || raw.VerificationURL == "" {
		return kilocodeDeviceStart{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kilocode initiate returned incomplete data")}
	}
	return kilocodeDeviceStart{
		Code:            raw.Code,
		VerificationURI: raw.VerificationURL,
		Interval:        3,
	}, nil
}

func (m *Manager) pollKilocodeDevice(ctx context.Context, code string) (store.OAuthToken, bool, error) {
	target := kilocodePollURLBase + "/" + strings.TrimSpace(code)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusAccepted:
		return store.OAuthToken{}, true, &Error{code: "authorization_pending"}
	case http.StatusForbidden:
		return store.OAuthToken{}, false, &Error{code: "oauth_authorization_rejected", permanent: true}
	case http.StatusGone:
		return store.OAuthToken{}, false, &Error{code: "invalid_state", permanent: true, err: fmt.Errorf("kilocode authorization expired")}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("kilocode poll HTTP %d", resp.StatusCode)}
	}
	var raw struct {
		Status    string `json:"status"`
		Token     string `json:"token"`
		UserEmail string `json:"userEmail"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: err}
	}
	if raw.Status != "approved" || raw.Token == "" {
		return store.OAuthToken{}, true, &Error{code: "authorization_pending"}
	}
	token := store.OAuthToken{
		AccessToken: raw.Token,
		TokenType:   "Bearer",
		Extra:       map[string]any{},
	}
	if email := strings.TrimSpace(raw.UserEmail); email != "" {
		token.Extra["email"] = email
	}
	if orgID := m.fetchKilocodeOrgID(ctx, raw.Token); orgID != "" {
		token.Extra["org_id"] = orgID
		token.Extra["orgId"] = orgID
	}
	return token, false, nil
}

func (m *Manager) fetchKilocodeOrgID(ctx context.Context, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kilocodeAPIBaseURL+"/api/profile", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	defer resp.Body.Close()
	var profile struct {
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&profile) != nil {
		return ""
	}
	if len(profile.Organizations) == 0 {
		return ""
	}
	return strings.TrimSpace(profile.Organizations[0].ID)
}
