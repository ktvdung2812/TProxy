package providers

import (
	"net/http"
	"runtime"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/version"
)

// applyClineHeaders mirrors 9router buildClineHeaders for Cline / ClinePass upstream.
func applyClineHeaders(headers http.Header) {
	if headers.Get("HTTP-Referer") == "" {
		headers.Set("HTTP-Referer", "https://cline.bot")
	}
	if headers.Get("X-Title") == "" {
		headers.Set("X-Title", "Cline")
	}
	ver := strings.TrimSpace(version.Current())
	if ver == "" || ver == "dev" {
		ver = "1.0"
	}
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", "tproxy/"+ver)
	}
	if headers.Get("X-PLATFORM") == "" {
		headers.Set("X-PLATFORM", runtime.GOOS)
	}
	if headers.Get("X-PLATFORM-VERSION") == "" {
		headers.Set("X-PLATFORM-VERSION", runtime.Version())
	}
	if headers.Get("X-CLIENT-TYPE") == "" {
		headers.Set("X-CLIENT-TYPE", "tproxy")
	}
	if headers.Get("X-CLIENT-VERSION") == "" {
		headers.Set("X-CLIENT-VERSION", ver)
	}
	if headers.Get("X-CORE-VERSION") == "" {
		headers.Set("X-CORE-VERSION", ver)
	}
	if headers.Get("X-IS-MULTIROOT") == "" {
		headers.Set("X-IS-MULTIROOT", "false")
	}
}

// clineAuthorizationValue builds Authorization for Cline upstream.
// OAuth WorkOS tokens require the workos: prefix; API keys are sent as plain Bearer.
func clineAuthorizationValue(secret, authType string) string {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return ""
	}
	if isClineAPIKeyAuth(authType) {
		return "Bearer " + trimmed
	}
	return "Bearer " + clineWorkOSAccessToken(trimmed)
}

func isClineAPIKeyAuth(authType string) bool {
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "api_key", "apikey", "key":
		return true
	default:
		return false
	}
}

// clineWorkOSAccessToken ensures OAuth tokens carry the WorkOS prefix.
func clineWorkOSAccessToken(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "workos:") {
		return trimmed
	}
	return "workos:" + trimmed
}

// applyClineAuthHeaders sets Cline-specific headers + Authorization for a credential.
func applyClineAuthHeaders(headers http.Header, credential store.Credential) {
	applyClineHeaders(headers)
	if credential.Secret != "" {
		headers.Set("Authorization", clineAuthorizationValue(credential.Secret, credential.AuthType))
	}
}
