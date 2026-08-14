package auth

import (
	"net/url"
	"strings"

	"github.com/tproxy/tproxy/internal/config"
)

// claudeAuthorizationURL builds the Claude OAuth authorize URL.
//
// The parameters are emitted in the order the Claude Code client itself sends
// them rather than sorted, which is what url.Values.Encode would do. Anthropic
// validates the authorize request strictly, and every reference implementation
// of this flow reproduces this exact order, so it is preserved here rather than
// left to chance.
func claudeAuthorizationURL(state, verifier, redirectURL, clientID string, scopes []string) string {
	if len(scopes) == 0 {
		scopes = config.ClaudeOAuthScopes()
	}
	ordered := [][2]string{
		{"code", "true"},
		{"client_id", clientID},
		{"response_type", "code"},
		{"redirect_uri", redirectURL},
		{"scope", strings.Join(scopes, " ")},
		{"code_challenge", pkceChallenge(verifier)},
		{"code_challenge_method", "S256"},
		{"state", state},
	}
	var query strings.Builder
	for index, pair := range ordered {
		if index > 0 {
			query.WriteByte('&')
		}
		query.WriteString(pair[0])
		query.WriteByte('=')
		query.WriteString(url.QueryEscape(pair[1]))
	}
	return "https://claude.ai/oauth/authorize?" + query.String()
}
