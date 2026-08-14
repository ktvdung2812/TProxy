package providers

import (
	"net/http"
	"strings"
)

var claudeCodeAllowedHeaders = map[string]bool{
	"anthropic-beta":           true,
	"user-agent":               true,
	"x-claude-code-session-id": true,
	"anthropic-dangerous-direct-browser-access": true,
	"x-app":                       true,
	"x-stainless-helper-method":   true,
	"x-stainless-retry-count":     true,
	"x-stainless-runtime-version": true,
	"x-stainless-package-version": true,
	"x-stainless-runtime":         true,
	"x-stainless-lang":            true,
	"x-stainless-arch":            true,
	"x-stainless-os":              true,
	"x-stainless-timeout":         true,
	"package-version":             true,
	"runtime-version":             true,
	"os":                          true,
	"arch":                        true,
}

var codexClientAllowedHeaders = map[string]bool{
	"user-agent":         true,
	"originator":         true,
	"version":            true,
	"session_id":         true,
	"chatgpt-account-id": true,
}

func splitHeaderList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func mergeAnthropicBeta(existing, incoming string) string {
	merged := map[string]struct{}{}
	for _, beta := range splitHeaderList(existing) {
		merged[beta] = struct{}{}
	}
	for _, beta := range splitHeaderList(incoming) {
		merged[beta] = struct{}{}
	}
	if len(merged) == 0 {
		return ""
	}
	values := make([]string, 0, len(merged))
	for beta := range merged {
		values = append(values, beta)
	}
	return strings.Join(values, ",")
}

func applyClaudeCodeCompatibilityHeaders(headers http.Header, client map[string]string) {
	if len(client) == 0 {
		return
	}
	for name, value := range client {
		key := strings.ToLower(strings.TrimSpace(name))
		if !claudeCodeAllowedHeaders[key] {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if key == "anthropic-beta" {
			merged := mergeAnthropicBeta(headers.Get("Anthropic-Beta"), value)
			if merged != "" {
				headers.Set("Anthropic-Beta", merged)
			}
			continue
		}
		canonicalKey := http.CanonicalHeaderKey(key)
		if headers.Get(canonicalKey) == "" {
			headers.Set(canonicalKey, value)
		}
	}
}

func applyCodexClientHeaders(headers http.Header, client map[string]string) {
	for name, value := range client {
		key := strings.ToLower(strings.TrimSpace(name))
		if !codexClientAllowedHeaders[key] {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		canonicalKey := http.CanonicalHeaderKey(key)
		if headers.Get(canonicalKey) == "" {
			headers.Set(canonicalKey, value)
		}
	}
	if headers.Get("Originator") == "" {
		headers.Set("Originator", "codex_cli_rs")
	}
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", "codex_cli_rs/0.125.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464")
	}
}
