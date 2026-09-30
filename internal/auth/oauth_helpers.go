package auth

import (
	"strings"

	"github.com/tproxy/tproxy/internal/config"
)

func oauthAllowsMissingClientID(providerType string, oauth config.OAuthConfig) bool {
	if isClineProvider(providerType) {
		return true
	}
	switch providerType {
	case "kimchi", "kilocode", "codebuddy-cn", "kiro", "devin":
		return true
	case "gitlab":
		return strings.TrimSpace(oauth.ClientID) == ""
	}
	switch strings.ToLower(strings.TrimSpace(oauth.DeviceFlow)) {
	case "qoder", "kilocode", "codebuddy-cn", "kiro":
		return true
	default:
		return false
	}
}

func usesCustomBrowserOAuth(providerType string) bool {
	switch providerType {
	case "cline", "clinepass", "iflow", "kimchi", "gitlab", "devin":
		return true
	default:
		return false
	}
}

func isCustomOAuthProviderType(providerType string) bool {
	switch providerType {
	case "qoder", "kilocode", "codebuddy-cn", "kiro", "kimchi", "iflow", "gitlab", "devin":
		return true
	default:
		return false
	}
}
