package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestClaudeFingerprintSendsFullBetaSetAndClientTuple(t *testing.T) {
	headers := anthropicHeaders(store.Provider{Type: "claude"}, oauthCredential(), canonical.Request{})

	betas := headers.Get("Anthropic-Beta")
	for _, required := range []string{"claude-code-20250219", "oauth-2025-04-20", "interleaved-thinking-2025-05-14", "context-management-2025-06-27"} {
		if !strings.Contains(betas, required) {
			t.Fatalf("missing beta %q in %q", required, betas)
		}
	}
	// redact-thinking makes upstream return signature-only thinking blocks,
	// which this gateway does not handle, so it must not be sent by default.
	if strings.Contains(betas, "redact-thinking") {
		t.Fatalf("redact-thinking sent by default: %q", betas)
	}
	if got := headers.Get("User-Agent"); got != "claude-cli/"+claudeCodeVersion+" (external, cli)" {
		t.Fatalf("user agent = %q", got)
	}
	for name, want := range map[string]string{
		"X-App":                       "cli",
		"X-Stainless-Lang":            "js",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Package-Version": claudeStainlessPackage,
		"X-Stainless-Retry-Count":     "0",
		"X-Stainless-Timeout":         "600",
		"anthropic-version":           claudeAnthropicVersion,
	} {
		if got := headers.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if headers.Get("X-Claude-Code-Session-Id") == "" || headers.Get("X-Client-Request-Id") == "" {
		t.Fatalf("session markers missing: %+v", headers)
	}
}

// The session ties a series of requests together upstream, so it must be stable
// per account; the request ID is per-call and must not be.
func TestClaudeSessionIsStableAndRequestIDIsNot(t *testing.T) {
	first := anthropicHeaders(store.Provider{Type: "claude"}, oauthCredential(), canonical.Request{})
	second := anthropicHeaders(store.Provider{Type: "claude"}, oauthCredential(), canonical.Request{})

	if first.Get("X-Claude-Code-Session-Id") != second.Get("X-Claude-Code-Session-Id") {
		t.Fatal("session id changed between requests")
	}
	if first.Get("X-Client-Request-Id") == second.Get("X-Client-Request-Id") {
		t.Fatal("client request id was reused")
	}

	other := oauthCredential()
	other.ID = "cred-2"
	third := anthropicHeaders(store.Provider{Type: "claude"}, other, canonical.Request{})
	if third.Get("X-Claude-Code-Session-Id") == first.Get("X-Claude-Code-Session-Id") {
		t.Fatal("two accounts share a session id")
	}
}

func TestClaudeStreamAdvertisesStreamHelper(t *testing.T) {
	streaming := anthropicHeaders(store.Provider{Type: "claude"}, oauthCredential(), canonical.Request{Stream: true})
	if streaming.Get("X-Stainless-Helper-Method") != "stream" {
		t.Fatalf("helper method = %q", streaming.Get("X-Stainless-Helper-Method"))
	}
	unary := anthropicHeaders(store.Provider{Type: "claude"}, oauthCredential(), canonical.Request{})
	if unary.Get("X-Stainless-Helper-Method") != "" {
		t.Fatalf("helper method leaked into a non-stream request: %q", unary.Get("X-Stainless-Helper-Method"))
	}
}

// A third-party caller's User-Agent must never reach upstream on a Claude Code
// OAuth token: it is the clearest possible signal that the request is not from
// the CLI the token is scoped to.
func TestClaudeFingerprintOverridesThirdPartyClientIdentity(t *testing.T) {
	request := canonical.Request{Metadata: map[string]any{"client_headers": map[string]string{
		"user-agent":     "python-httpx/0.27.0",
		"anthropic-beta": "some-client-beta",
	}}}
	headers := anthropicHeaders(store.Provider{Type: "claude"}, oauthCredential(), request)

	if got := headers.Get("User-Agent"); got != claudeUserAgent() {
		t.Fatalf("third-party user agent survived: %q", got)
	}
	// Its betas are still honored: they change behavior, not identity.
	if !strings.Contains(headers.Get("Anthropic-Beta"), "some-client-beta") {
		t.Fatalf("client beta dropped: %q", headers.Get("Anthropic-Beta"))
	}
}

// A caller that already speaks Claude Code keeps its own markers; the defaults
// only fill gaps, and its betas are added to ours rather than replacing them.
func TestClaudeFingerprintDefersToClientHeaders(t *testing.T) {
	request := canonical.Request{Metadata: map[string]any{"client_headers": map[string]string{
		"user-agent":     "claude-cli/9.9.9 (external, cli)",
		"anthropic-beta": "some-client-beta",
	}}}
	headers := anthropicHeaders(store.Provider{Type: "claude"}, oauthCredential(), request)

	if got := headers.Get("User-Agent"); got != "claude-cli/9.9.9 (external, cli)" {
		t.Fatalf("user agent = %q", got)
	}
	betas := headers.Get("Anthropic-Beta")
	if !strings.Contains(betas, "some-client-beta") || !strings.Contains(betas, "oauth-2025-04-20") {
		t.Fatalf("betas = %q", betas)
	}
}

// Upstream sees the raw header, so an identical request must produce an
// identical beta string rather than a reshuffled set.
func TestMergeAnthropicBetaIsOrderStable(t *testing.T) {
	first := mergeAnthropicBeta("a,b,c", "d,b")
	for i := 0; i < 20; i++ {
		if got := mergeAnthropicBeta("a,b,c", "d,b"); got != first {
			t.Fatalf("beta order unstable: %q vs %q", got, first)
		}
	}
	if first != "a,b,c,d" {
		t.Fatalf("merged betas = %q", first)
	}
}

func TestClaudeAPIKeyCredentialKeepsAPIKeyAuth(t *testing.T) {
	credential := store.Credential{ID: "cred-key", AuthType: "api_key", Secret: "sk-ant-api03-xyz"}
	headers := anthropicHeaders(store.Provider{Type: "claude"}, credential, canonical.Request{})

	if headers.Get("x-api-key") != "sk-ant-api03-xyz" || headers.Get("Authorization") != "" {
		t.Fatalf("headers = %+v", headers)
	}
	// The Claude Code markers are for OAuth traffic; an API key is a legitimate
	// third-party caller and must not be dressed up as the CLI.
	if headers.Get("X-Claude-Code-Session-Id") != "" || headers.Get("X-App") != "" {
		t.Fatalf("api key request carries CLI markers: %+v", headers)
	}
}

func TestIsClaudeOAuthCredentialRequiresOAuthToken(t *testing.T) {
	if !isClaudeOAuthCredential(oauthCredential()) {
		t.Fatal("expected an sk-ant-oat oauth credential to qualify")
	}
	if isClaudeOAuthCredential(store.Credential{AuthType: "api_key", Secret: "sk-ant-oat01-abc"}) {
		t.Fatal("api key auth must not qualify regardless of secret shape")
	}
	if isClaudeOAuthCredential(store.Credential{AuthType: "oauth", Secret: "some-other-token"}) {
		t.Fatal("a non-Claude-Code oauth token must not qualify")
	}
}

func TestClaudeDerivedUUIDIsWellFormed(t *testing.T) {
	identity := claudeIdentityFor(oauthCredential())
	for _, value := range []string{identity.AccountUUID, identity.SessionID} {
		parts := strings.Split(value, "-")
		if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
			t.Fatalf("malformed uuid %q", value)
		}
		if parts[2][0] != '4' {
			t.Fatalf("version nibble = %q in %q", parts[2][0], value)
		}
		if !strings.ContainsRune("89ab", rune(parts[3][0])) {
			t.Fatalf("variant nibble = %q in %q", parts[3][0], value)
		}
	}
	if identity.AccountUUID == identity.SessionID {
		t.Fatal("account and session must not collide")
	}
	if len(identity.DeviceID) != 64 {
		t.Fatalf("device id = %q", identity.DeviceID)
	}
}

func TestClaudeHeadersAreNotAppliedToOtherAnthropicProviders(t *testing.T) {
	credential := store.Credential{ID: "cred-3", AuthType: "oauth", Secret: "sk-ant-oat01-abc"}
	headers := anthropicHeaders(store.Provider{Type: "anthropic-compatible"}, credential, canonical.Request{})

	if headers.Get("X-App") != "" || headers.Get("X-Claude-Code-Session-Id") != "" {
		t.Fatalf("CLI markers leaked to a compatible provider: %+v", headers)
	}
	if headers.Get("anthropic-version") != claudeAnthropicVersion {
		t.Fatalf("anthropic-version = %q", headers.Get("anthropic-version"))
	}
}

// The fingerprint is on by default — without it the handshake contradicts the
// Claude Code headers and the account is billed as a third-party app — but it
// never applies to an API key, which is an ordinary third-party caller.
func TestClaudeTLSFingerprintDefaultsOnAndIsOAuthOnly(t *testing.T) {
	plain := store.Provider{ID: "claude", Type: "claude"}
	if !claudeTLSFingerprintEnabled(plain) {
		t.Fatal("fingerprint must be on by default")
	}

	disabled := plain
	disabled.Config = map[string]any{"claude_tls_fingerprint": "off"}
	if claudeTLSFingerprintEnabled(disabled) {
		t.Fatal("claude_tls_fingerprint=off should disable it")
	}

	// Other Anthropic-shaped providers are not Claude Code and must never be
	// dialled as if they were, whatever their config says.
	other := store.Provider{ID: "compat", Type: "anthropic-compatible", Config: map[string]any{"claude_tls_fingerprint": "on"}}
	if claudeTLSFingerprintEnabled(other) {
		t.Fatal("the flag is Claude-only")
	}

	ctx := context.Background()
	if !tlsFingerprintRequested(withClaudeTransport(ctx, plain, oauthCredential())) {
		t.Fatal("oauth credential should be fingerprinted by default")
	}
	apiKey := store.Credential{ID: "cred-key", AuthType: "api_key", Secret: "sk-ant-api03-xyz"}
	if tlsFingerprintRequested(withClaudeTransport(ctx, plain, apiKey)) {
		t.Fatal("api key traffic must not be fingerprinted")
	}
	if tlsFingerprintRequested(withClaudeTransport(ctx, disabled, oauthCredential())) {
		t.Fatal("an explicit off must be honoured")
	}
}

// An unsupported proxy must cost the fingerprint, not the request.
func TestProxyTransportFallsBackWhenProxyCannotCarryFingerprint(t *testing.T) {
	transport := newProxyTransport()
	if got := transport.fingerprintTransport("https://proxy.example.test:8443"); got != nil {
		t.Fatalf("https proxy returned a fingerprint transport: %#v", got)
	}
	// The negative result is cached rather than rebuilt per request.
	if _, cached := transport.fingerprint["https://proxy.example.test:8443"]; !cached {
		t.Fatal("unsupported proxy was not cached")
	}
	if got := transport.fingerprintTransport(""); got == nil {
		t.Fatal("direct connections should get a fingerprint transport")
	}
}
