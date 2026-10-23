package importsub2api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ParseTokenExport converts standalone ChatGPT OAuth exports to the account
// format used by Import. JWT claims supply metadata only; upstream validates
// the bearer token when it is used.
func ParseTokenExport(data []byte) (*Export, error) {
	type tokenExport struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		IDToken      string `json:"idToken"`
		ExpiresAt    string `json:"expiresAt"`
		ProviderData struct {
			AccountID string `json:"chatgptAccountId"`
			Plan      string `json:"chatgptPlanType"`
		} `json:"providerSpecificData"`
	}
	var items []tokenExport
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("invalid ChatGPT token export: %w", err)
		}
	} else {
		var item tokenExport
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("invalid ChatGPT token export: %w", err)
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("ChatGPT token export contains no accounts")
	}
	export := &Export{}
	for index, item := range items {
		if strings.TrimSpace(item.AccessToken) == "" {
			return nil, fmt.Errorf("ChatGPT account %d: missing accessToken", index+1)
		}
		access, identity := tokenClaims(item.AccessToken), tokenClaims(item.IDToken)
		accessAuth, _ := access["https://api.openai.com/auth"].(map[string]any)
		identityAuth, _ := identity["https://api.openai.com/auth"].(map[string]any)
		accountID := firstNonEmpty(item.ProviderData.AccountID, stringFromAny(accessAuth["chatgpt_account_id"]), stringFromAny(identityAuth["chatgpt_account_id"]))
		if accountID == "" {
			return nil, fmt.Errorf("ChatGPT account %d: missing chatgptAccountId", index+1)
		}
		for _, claims := range []map[string]any{accessAuth, identityAuth} {
			if id := stringFromAny(claims["chatgpt_account_id"]); id != "" && id != accountID {
				return nil, fmt.Errorf("ChatGPT account %d: account IDs do not match", index+1)
			}
		}
		var expiry time.Time
		if item.ExpiresAt != "" {
			var err error
			expiry, err = time.Parse(time.RFC3339Nano, item.ExpiresAt)
			if err != nil {
				return nil, fmt.Errorf("ChatGPT account %d: invalid expiresAt", index+1)
			}
		} else if seconds, ok := access["exp"].(float64); ok && seconds > 0 {
			expiry = time.Unix(int64(seconds), 0).UTC()
		} else {
			// expiresIn is relative to issuance, not the time this file is imported.
			return nil, fmt.Errorf("ChatGPT account %d: missing expiresAt or access token exp", index+1)
		}
		profile, _ := access["https://api.openai.com/profile"].(map[string]any)
		email := firstNonEmpty(stringFromAny(identity["email"]), stringFromAny(profile["email"]))
		plan := firstNonEmpty(item.ProviderData.Plan, stringFromAny(accessAuth["chatgpt_plan_type"]), stringFromAny(identityAuth["chatgpt_plan_type"]))
		export.Accounts = append(export.Accounts, Account{
			Name: firstNonEmpty(email, accountID), Platform: "openai", Type: "oauth",
			Credentials: map[string]any{
				"access_token": item.AccessToken, "refresh_token": item.RefreshToken,
				"id_token": item.IDToken, "expires_at": expiry.Format(time.RFC3339Nano),
				"chatgpt_account_id": accountID, "chatgpt_plan_type": plan, "email": email,
				"chatgpt_user_id": firstNonEmpty(stringFromAny(accessAuth["chatgpt_user_id"]), stringFromAny(identityAuth["chatgpt_user_id"])),
			},
		})
	}
	return export, nil
}

func tokenClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	_ = json.Unmarshal(data, &claims)
	return claims
}
