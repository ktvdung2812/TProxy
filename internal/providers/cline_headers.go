package providers

import "net/http"

func applyClineHeaders(headers http.Header) {
	if headers.Get("HTTP-Referer") == "" {
		headers.Set("HTTP-Referer", "https://cline.bot")
	}
	if headers.Get("X-Title") == "" {
		headers.Set("X-Title", "Cline")
	}
	if headers.Get("X-CLIENT-TYPE") == "" {
		headers.Set("X-CLIENT-TYPE", "tproxy")
	}
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", "tproxy/1.0")
	}
}

func clineAuthorizationValue(secret string) string {
	trimmed := secret
	if trimmed == "" {
		return ""
	}
	if len(trimmed) >= 7 && trimmed[:7] == "workos:" {
		return "Bearer " + trimmed
	}
	return "Bearer workos:" + trimmed
}
