package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	museDefaultAPIBase  = "https://api.meta.ai"
	museMintPath        = "/muse-code/key"
	museCodeUserAgent   = "muse-code/1.0.2"
	museExtraDCAToken   = "dca_token"
	museExtraDCAExpires = "dca_expires_at"
	// museLoginMintAttempts bounds the login-time mint. The endpoint is
	// aggressively rate-limited, and a failed mint fails the sign-in and forces
	// a new device login.
	museLoginMintAttempts = 3
)

// museMintRetryDelay is the wait before login mint attempt n+1 (overridden in tests).
var museMintRetryDelay = func(attempt int) time.Duration { return time.Duration(attempt+1) * 5 * time.Second }

// museMintedKey is the POST /muse-code/key response: a subscription-backed
// Meta Model API key plus the account's subscription state.
type museMintedKey struct {
	APIKey           string `json:"api_key"`
	BaseURL          string `json:"base_url"`
	UserEmail        string `json:"user_email"`
	UserFullName     string `json:"user_full_name"`
	SubsTierName     string `json:"subs_tier_name"`
	SubsTierID       string `json:"subs_tier_id"`
	IsSubsActive     *bool  `json:"is_subs_active"` // nil when omitted: not read as inactive
	HasPaymentMethod bool   `json:"has_payment_method"`
	RequirePayment   bool   `json:"require_payment"`
	CanSubscribe     bool   `json:"can_subscribe"`
	// Fix-it link Meta returns when the account needs a subscription or payment.
	ActionURL               string `json:"action_url"`
	RequirePaymentActionURL string `json:"require_payment_action_url"`
}

// museMintURL derives the mint endpoint from the provider's Model API base
// (…/v1 → …/muse-code/key), falling back to the real Meta API host.
func museMintURL(provider store.Provider) string {
	base := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/"), "/v1")
	if base == "" {
		base = museDefaultAPIBase
	}
	return base + museMintPath
}

// mintMuseKey exchanges a device-code access token (dca:…) for a
// subscription-backed inference key (LLM|…). login enrolls the account on
// first sign-in (onboard) and retries throttled or failed mints.
func (m *Manager) mintMuseKey(ctx context.Context, provider store.Provider, dcaToken string, login bool) (*museMintedKey, error) {
	body := map[string]any{"dca_token": dcaToken}
	attempts := 1
	if login {
		body["onboard"] = true
		attempts = museLoginMintAttempts
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	var status int
	var data []byte
	for attempt := 0; ; attempt++ {
		status, data, err = m.postMuseMint(ctx, provider, dcaToken, payload)
		if err != nil {
			return nil, err
		}
		transient := status == http.StatusTooManyRequests || status >= 500
		if !transient || attempt+1 >= attempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, &Error{code: "oauth_provider_unavailable", err: ctx.Err()}
		case <-time.After(museMintRetryDelay(attempt)):
		}
	}
	if status < 200 || status >= 300 {
		detail := museErrorDetail(data)
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			if detail == "" {
				detail = "Meta rejected the account token; sign in again"
			}
			return nil, &Error{code: "authorization_required", err: fmt.Errorf("muse: key mint HTTP %d: %s", status, detail), permanent: true, detail: detail}
		}
		return nil, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("muse: key mint HTTP %d: %s", status, detail), detail: detail}
	}
	var minted museMintedKey
	if err := json.Unmarshal(data, &minted); err != nil {
		return nil, &Error{code: "oauth_provider_unavailable", err: errors.New("muse: malformed key mint response")}
	}
	actionURL := strings.TrimSpace(firstNonEmpty(minted.RequirePaymentActionURL, minted.ActionURL))
	// A minted key on an inactive subscription is billed pay-as-you-go, not
	// covered by Muse. Reject it rather than silently attaching paid API
	// usage to the account.
	if minted.RequirePayment {
		return nil, museSubscriptionError("Muse Code subscription requires an active payment method", actionURL)
	}
	if minted.IsSubsActive != nil && !*minted.IsSubsActive {
		return nil, museSubscriptionError("No active Muse Code subscription on this account; activate it on muse.ai", actionURL)
	}
	if minted.APIKey == "" {
		if actionURL != "" {
			return nil, museSubscriptionError("Muse Code subscription required", actionURL)
		}
		return nil, &Error{code: "oauth_provider_unavailable", err: errors.New("muse: malformed key mint response")}
	}
	return &minted, nil
}

func (m *Manager) postMuseMint(ctx context.Context, provider store.Provider, dcaToken string, payload []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, museMintURL(provider), bytes.NewReader(payload))
	if err != nil {
		return 0, nil, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+dcaToken)
	request.Header.Set("User-Agent", museCodeUserAgent)
	request.Header.Set("x-api-version", config.MuseAPIVersion)
	response, err := m.client.Do(request)
	if err != nil {
		return 0, nil, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, &Error{code: "oauth_provider_unavailable"}
	}
	return response.StatusCode, data, nil
}

func museSubscriptionError(message, actionURL string) error {
	detail := message
	if actionURL != "" {
		detail += " — " + actionURL
	}
	return &Error{code: "authorization_required", err: errors.New("muse: " + detail), permanent: true, detail: detail}
}

// museErrorDetail reads Meta's error bodies: the Muse Code envelope
// ({title, detail, action_url}, e.g. 403 code 4705002) and the documented
// Model API one ({error: {message}}).
func museErrorDetail(data []byte) string {
	var envelope struct {
		Title     string `json:"title"`
		Detail    string `json:"detail"`
		ActionURL string `json:"action_url"`
		Error     any    `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return ""
	}
	parts := make([]string, 0, 2)
	for _, part := range []string{envelope.Title, envelope.Detail} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	message := strings.Join(parts, ": ")
	if message == "" {
		switch value := envelope.Error.(type) {
		case string:
			message = strings.TrimSpace(value)
		case map[string]any:
			message = strings.TrimSpace(stringValue(value["message"]))
		}
	}
	if envelope.ActionURL != "" {
		if message == "" {
			return envelope.ActionURL
		}
		message += " — " + envelope.ActionURL
	}
	return message
}

func museStashMintedExtra(token *store.OAuthToken, minted *museMintedKey) {
	token.Extra["subs_tier_name"] = minted.SubsTierName
	token.Extra["subs_tier_id"] = minted.SubsTierID
	token.Extra["is_subs_active"] = minted.IsSubsActive == nil || *minted.IsSubsActive
	token.Extra["has_payment_method"] = minted.HasPaymentMethod
	if minted.UserFullName != "" {
		token.Extra["user_full_name"] = minted.UserFullName
	}
}

// enrichMuseToken runs once at login: the device-flow access token is not a
// usable inference key, so it is minted into one. The dca: token is kept in
// Extra for later re-mints; it is never used as the credential secret.
func (m *Manager) enrichMuseToken(ctx context.Context, provider store.Provider, token store.OAuthToken, email string) (store.OAuthToken, string, error) {
	dca := strings.TrimSpace(token.AccessToken)
	if dca == "" {
		return token, email, &Error{code: "authorization_required", permanent: true}
	}
	// The refresh path runs prepareProviderToken after remintMuseKey, so the
	// access token is already a minted key. Minting again would send that key
	// as the account token and overwrite the stored dca: token with it.
	if stored := strings.TrimSpace(stringValue(token.Extra[museExtraDCAToken])); stored != "" && stored != dca {
		return token, email, nil
	}
	token.Extra[museExtraDCAToken] = dca
	if !token.ExpiresAt.IsZero() {
		token.Extra[museExtraDCAExpires] = strconv.FormatInt(token.ExpiresAt.Unix(), 10)
	}
	minted, err := m.mintMuseKey(ctx, provider, dca, true)
	if err != nil {
		return token, email, err
	}
	museStashMintedExtra(&token, minted)
	token.AccessToken = minted.APIKey
	token.TokenType = "Bearer"
	if minted.UserEmail != "" {
		email = minted.UserEmail
	}
	return token, email, nil
}

// remintMuseKey is the refresh path: the stored dca: token mints a fresh
// inference key. An expired dca: token cannot be extended — it marks the
// credential authorization_required so the user signs in again.
func (m *Manager) remintMuseKey(ctx context.Context, provider store.Provider, old store.OAuthToken) (store.OAuthToken, error) {
	dca := stringValue(old.Extra[museExtraDCAToken])
	if dca == "" {
		dca = strings.TrimSpace(old.RefreshToken)
	}
	if dca == "" {
		return store.OAuthToken{}, &Error{code: "authorization_required", err: errors.New("muse: no device token stored; sign in again"), permanent: true}
	}
	if raw := stringValue(old.Extra[museExtraDCAExpires]); raw != "" {
		if expires, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil && m.now().Unix() >= expires {
			return store.OAuthToken{}, &Error{code: "authorization_required", err: errors.New("muse: account token expired; sign in again"), permanent: true}
		}
	}
	minted, err := m.mintMuseKey(ctx, provider, dca, false)
	if err != nil {
		return store.OAuthToken{}, err
	}
	token := store.OAuthToken{
		AccessToken:  minted.APIKey,
		TokenType:    "Bearer",
		RefreshToken: old.RefreshToken,
		Extra:        make(map[string]any),
	}
	museStashMintedExtra(&token, minted)
	token.Extra[museExtraDCAToken] = dca
	token.Extra[museExtraDCAExpires] = old.Extra[museExtraDCAExpires]
	if minted.UserEmail != "" {
		token.Extra["email"] = minted.UserEmail
	} else {
		token.Extra["email"] = old.Extra["email"]
	}
	return token, nil
}
