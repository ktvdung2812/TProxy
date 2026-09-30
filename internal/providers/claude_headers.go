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
	// Upstream Codex CLI session/turn contract (codex-rs core client +
	// codex-api session headers). Forwarded verbatim so chatgpt.com sees the
	// same turn identity and metadata it would on a direct connection.
	"session-id":                             true,
	"thread-id":                              true,
	"x-client-request-id":                    true,
	"openai-beta":                            true,
	"openai-organization":                    true,
	"openai-project":                         true,
	"x-codex-installation-id":                true,
	"x-codex-routing-hint":                   true,
	"x-codex-turn-state":                     true,
	"x-codex-turn-metadata":                  true,
	"x-codex-parent-thread-id":               true,
	"x-codex-window-id":                      true,
	"x-codex-beta-features":                  true,
	"x-openai-subagent":                      true,
	"x-openai-memgen-request":                true,
	"x-openai-internal-codex-residency":      true,
	"x-openai-internal-codex-responses-lite": true,
	"x-responsesapi-include-timing-metrics":  true,
	"x-oai-attestation":                      true,
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

// mergeAnthropicBeta unions two beta lists while preserving order: the existing
// list first, then any betas the client added. Order is deliberate — upstream
// sees the raw header, and a set that reshuffles between otherwise identical
// requests is a fingerprint of its own.
func mergeAnthropicBeta(existing, incoming string) string {
	seen := map[string]struct{}{}
	values := make([]string, 0, 8)
	for _, beta := range append(splitHeaderList(existing), splitHeaderList(incoming)...) {
		if _, duplicate := seen[beta]; duplicate {
			continue
		}
		seen[beta] = struct{}{}
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
