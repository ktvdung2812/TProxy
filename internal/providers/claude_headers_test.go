package providers

import (
	"net/http"
	"strings"
	"testing"
)

func TestApplyClaudeCodeCompatibilityHeadersMergesBeta(t *testing.T) {
	headers := http.Header{}
	headers.Set("Anthropic-Beta", "oauth-2025-04-20")
	applyClaudeCodeCompatibilityHeaders(headers, map[string]string{
		"anthropic-beta": "claude-code-20250219,interleaved-thinking-2025-05-14",
		"user-agent":     "claude-cli/2.1.63 (external, cli)",
	})
	beta := headers.Get("Anthropic-Beta")
	if !strings.Contains(beta, "oauth-2025-04-20") || !strings.Contains(beta, "claude-code-20250219") {
		t.Fatalf("merged beta = %q", beta)
	}
	if headers.Get("User-Agent") != "claude-cli/2.1.63 (external, cli)" {
		t.Fatalf("user-agent = %q", headers.Get("User-Agent"))
	}
}

func TestApplyClaudeCodeCompatibilityHeadersIgnoresAuth(t *testing.T) {
	headers := http.Header{}
	applyClaudeCodeCompatibilityHeaders(headers, map[string]string{
		"authorization": "Bearer secret",
		"x-api-key":     "secret",
	})
	if headers.Get("Authorization") != "" || headers.Get("X-Api-Key") != "" {
		t.Fatal("expected auth headers to be ignored")
	}
}
