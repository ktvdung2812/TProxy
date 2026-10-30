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

	"github.com/tproxy/tproxy/internal/store"
)

const (
	museDefaultAPIBase  = "https://api.meta.ai"
	museMintPath        = "/muse-code/key"
	museCodeUserAgent   = "muse-code/1.0.2"
	museExtraDCAToken   = "dca_token"
	museExtraDCAExpires = "dca_expires_at"
)

// museMintedKey is the POST /muse-code/key response: a subscription-backed
// Meta Model API key plus the account's subscription state.
type museMintedKey struct {
	APIKey           string `json:"api_key"`
	BaseURL          string `json:"base_url"`
	UserEmail        string `json:"user_email"`
	UserFullName     string `json:"user_full_name"`
	SubsTierName     string `json:"subs_tier_name"`
	SubsTierID       string `json:"subs_tier_id"`
	IsSubsActive     bool   `json:"is_subs_active"`
	HasPaymentMethod bool   `json:"has_payment_method"`
	RequirePayment   bool   `json:"require_payment"`
	CanSubscribe     bool   `json:"can_subscribe"`
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
// subscription-backed inference key (LLM|…).
func (m *Manager) mintMuseKey(ctx context.Context, provider store.Provider, dcaToken string) (*museMintedKey, error) {
	payload, err := json.Marshal(map[string]string{"dca_token": dcaToken})
	if err != nil {
		return nil, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, museMintURL(provider), bytes.NewReader(payload))
	if err != nil {
		return nil, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+dcaToken)
	request.Header.Set("User-Agent", museCodeUserAgent)
	response, err := m.client.Do(request)
	if err != nil {
		return nil, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, &Error{code: "oauth_provider_unavailable"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return nil, &Error{code: "authorization_required", err: errors.New("muse: account token rejected; sign in again"), permanent: true}
		}
		return nil, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("muse: key mint HTTP %d", response.StatusCode)}
	}
	var minted museMintedKey
	if err := json.Unmarshal(data, &minted); err != nil || minted.APIKey == "" {
		return nil, &Error{code: "oauth_provider_unavailable", err: errors.New("muse: malformed key mint response")}
	}
	// A minted key on an inactive subscription is billed pay-as-you-go, not
	// covered by Muse. Reject it rather than silently attaching paid API
	// usage to the account.
	if minted.RequirePayment {
		return nil, &Error{code: "authorization_required", err: errors.New("muse: subscription requires an active payment method"), permanent: true}
	}
	if !minted.IsSubsActive {
		return nil, &Error{code: "authorization_required", err: errors.New("muse: no active subscription on this account"), permanent: true}
	}
	return &minted, nil
}

func museStashMintedExtra(token *store.OAuthToken, minted *museMintedKey) {
	token.Extra["subs_tier_name"] = minted.SubsTierName
	token.Extra["subs_tier_id"] = minted.SubsTierID
	token.Extra["is_subs_active"] = minted.IsSubsActive
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
	token.Extra[museExtraDCAToken] = dca
	if !token.ExpiresAt.IsZero() {
		token.Extra[museExtraDCAExpires] = strconv.FormatInt(token.ExpiresAt.Unix(), 10)
	}
	minted, err := m.mintMuseKey(ctx, provider, dca)
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
	minted, err := m.mintMuseKey(ctx, provider, dca)
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
