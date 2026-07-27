package auth

import (
	"net/url"
	"strings"

	"github.com/tproxy/tproxy/internal/config"
)

// claudeAuthorizationURL builds the Claude OAuth authorize URL (9router-compatible).
func claudeAuthorizationURL(state, verifier, redirectURL, clientID string, scopes []string) string {
	if len(scopes) == 0 {
		scopes = config.ClaudeOAuthScopes()
	}
	values := url.Values{}
	values.Set("code", "true")
	values.Set("client_id", clientID)
	values.Set("response_type", "code")
	values.Set("redirect_uri", redirectURL)
	values.Set("scope", strings.Join(scopes, " "))
	values.Set("code_challenge", pkceChallenge(verifier))
	values.Set("code_challenge_method", "S256")
	values.Set("state", state)
	return "https://claude.ai/oauth/authorize?" + values.Encode()
}
