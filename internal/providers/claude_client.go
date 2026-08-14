package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/google/uuid"

	"github.com/tproxy/tproxy/internal/store"
)

// Claude Code client fingerprint.
//
// Anthropic decides whether an OAuth request is billed against the plan or
// against "extra usage" by looking at the whole shape of the request: the beta
// set, the User-Agent, the Stainless SDK tuple, and the presence of the
// Claude Code session/billing markers. A partial imitation is worse than none,
// because a request that claims to be Claude Code but is missing markers only
// a real client would send stands out. So the values below are kept as one
// coherent tuple and must be updated together.
const (
	claudeCodeVersion = "2.1.92"
	// claudeCodeEntrypoint appears both in the User-Agent and in the billing
	// header's cc_entrypoint field; upstream sees them side by side, so they
	// must agree.
	claudeCodeEntrypoint     = "cli"
	claudeStainlessPackage   = "0.80.0"
	claudeStainlessRuntimeV  = "v24.14.0"
	claudeStainlessTimeout   = "600"
	claudeStainlessRetry     = "0"
	claudeAnthropicVersion   = "2023-06-01"
	claudeUserAgentTemplate  = "claude-cli/%s (external, %s)"
	claudeStreamHelperMethod = "stream"
)

// claudeDefaultBetas is the beta set a current Claude Code CLI sends.
//
// Two betas the reference implementations ship are deliberately absent:
//   - redact-thinking-2026-02-12 makes upstream return signature-only thinking
//     blocks, which this gateway has no special handling for and which would
//     silently drop reasoning text from responses. A client that wants it can
//     still send it; the merge below preserves client betas.
//
// Order is fixed rather than alphabetical: it mirrors the order a real client
// emits, and a stable order keeps the header byte-identical across requests.
var claudeDefaultBetas = []string{
	"claude-code-20250219",
	"oauth-2025-04-20",
	"interleaved-thinking-2025-05-14",
	"context-management-2025-06-27",
	"prompt-caching-scope-2026-01-05",
	"advanced-tool-use-2025-11-20",
	"effort-2025-11-24",
	"structured-outputs-2025-12-15",
	"fast-mode-2026-02-01",
	"token-efficient-tools-2026-03-28",
}

func claudeUserAgent() string {
	return fmt.Sprintf(claudeUserAgentTemplate, claudeCodeVersion, claudeCodeEntrypoint)
}

// applyClaudeCodeFingerprint fills in the client markers a real Claude Code CLI
// sends. Every value is set only when absent so an explicit client header still
// wins; the beta list is unioned rather than replaced for the same reason.
func applyClaudeCodeFingerprint(headers http.Header, credential store.Credential, stream bool) {
	headers.Set("Anthropic-Beta", mergeAnthropicBeta(strings.Join(claudeDefaultBetas, ","), headers.Get("Anthropic-Beta")))

	defaults := map[string]string{
		"X-App":                       claudeCodeEntrypoint,
		"User-Agent":                  claudeUserAgent(),
		"X-Stainless-Lang":            "js",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Runtime-Version": claudeStainlessRuntimeV,
		"X-Stainless-Package-Version": claudeStainlessPackage,
		"X-Stainless-Arch":            claudeStainlessArch(),
		"X-Stainless-Os":              claudeStainlessOS(),
		"X-Stainless-Retry-Count":     claudeStainlessRetry,
		"X-Stainless-Timeout":         claudeStainlessTimeout,
	}
	if stream {
		defaults["X-Stainless-Helper-Method"] = claudeStreamHelperMethod
	}
	for name, value := range defaults {
		if headers.Get(name) == "" {
			headers.Set(name, value)
		}
	}

	// The session ID is stable per credential: it is what ties a series of
	// requests together as one Claude Code session upstream.
	if headers.Get("X-Claude-Code-Session-Id") == "" {
		headers.Set("X-Claude-Code-Session-Id", claudeIdentityFor(credential).SessionID)
	}
	// The request ID is the opposite: a real client mints a fresh UUID per call,
	// so reusing one would be the anomaly.
	headers.Set("X-Client-Request-Id", uuid.NewString())
}

// claudeStainlessOS maps the host OS onto the label the Node SDK reports.
func claudeStainlessOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return "Unknown"
	}
}

// claudeStainlessArch maps the host architecture onto the Node SDK label.
func claudeStainlessArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "x32"
	default:
		return runtime.GOARCH
	}
}

// claudeClientIsNative reports whether the caller is itself a Claude Code CLI.
// Such a request already carries a self-consistent identity and an already
// Claude Code-shaped body, so the imitation applied to other callers would only
// make it look less genuine.
func claudeClientIsNative(client map[string]string) bool {
	for name, value := range client {
		if !strings.EqualFold(strings.TrimSpace(name), "user-agent") {
			continue
		}
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "claude-cli/")
	}
	return false
}

// isClaudeOAuthCredential reports whether the credential is a Claude Code
// consumer OAuth token rather than a first-party API key. Every imitation
// behavior in this package is gated on it: an API key is a legitimate
// third-party caller and must be left exactly as the client sent it.
func isClaudeOAuthCredential(credential store.Credential) bool {
	if credential.AuthType != "oauth" {
		return false
	}
	return strings.Contains(credential.Secret, "sk-ant-oat")
}

// claudeIdentity is the per-account identity a real Claude Code install would
// have: a device it runs on, the account signed into it, and the session it is
// currently in. Upstream sees these across requests, so they are derived
// deterministically from the credential rather than randomized per request —
// a device ID that changes every call is itself a signal.
type claudeIdentity struct {
	DeviceID    string
	AccountUUID string
	SessionID   string
}

func claudeIdentityFor(credential store.Credential) claudeIdentity {
	seed := credential.ID
	if seed == "" {
		seed = credential.Secret
	}
	return claudeIdentity{
		DeviceID:    claudeDeriveHex(seed, "device"),
		AccountUUID: claudeDeriveUUID(seed, "account"),
		SessionID:   claudeDeriveUUID(seed, "session"),
	}
}

func claudeDeriveHex(seed, purpose string) string {
	sum := sha256.Sum256([]byte(purpose + ":" + seed))
	return hex.EncodeToString(sum[:])
}

// claudeDeriveUUID shapes a deterministic digest into a v4-looking UUID, with
// the version and variant nibbles forced so the result is well-formed.
func claudeDeriveUUID(seed, purpose string) string {
	digest := claudeDeriveHex(seed, purpose)
	variant := (hexNibble(digest[16]) & 0x3) | 0x8
	return fmt.Sprintf("%s-%s-4%s-%x%s-%s",
		digest[0:8],
		digest[8:12],
		digest[13:16],
		variant,
		digest[17:20],
		digest[20:32],
	)
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0
	}
}
