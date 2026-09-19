package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/version"
)

// openCodePassthroughHeaders are the session-scoped headers OpenCode clients send
// to opencode-hosted models. opencode.ai Go endpoints require x-opencode-session
// for routing and prompt-cache affinity; tproxy synthesizes it when the client
// does not provide one.
var openCodePassthroughHeaders = []string{
	"x-opencode-session",
	"x-opencode-request",
	"x-opencode-project",
	"x-opencode-client",
}

func isOpenCodeUpstream(provider store.Provider) bool {
	parsed, err := url.Parse(strings.TrimSpace(provider.BaseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "opencode.ai" || strings.HasSuffix(host, ".opencode.ai")
}

func applyOpenCodeHeaders(headers http.Header, provider store.Provider, request canonical.Request) {
	if !isOpenCodeUpstream(provider) {
		return
	}
	client := clientHeadersFromRequest(request)
	for _, name := range openCodePassthroughHeaders {
		if headers.Get(name) == "" {
			if value := strings.TrimSpace(client[name]); value != "" {
				headers.Set(name, value)
			}
		}
	}
	if headers.Get("x-opencode-session") == "" {
		headers.Set("x-opencode-session", openCodeSessionID(request))
	}
	if headers.Get("x-opencode-request") == "" {
		if value := strings.TrimSpace(request.RequestID); value != "" {
			headers.Set("x-opencode-request", value)
		}
	}
	if headers.Get("User-Agent") == "" {
		userAgent := strings.TrimSpace(client["user-agent"])
		if userAgent == "" {
			ver := strings.TrimSpace(version.Current())
			if ver == "" || ver == "dev" {
				ver = "1.0"
			}
			userAgent = "tproxy/" + ver
		}
		headers.Set("User-Agent", userAgent)
	}
}

// openCodeSessionID returns a stable session identifier for upstream routing.
// A client-supplied session ID wins; otherwise a content anchor keeps all turns
// of the same conversation on one upstream session.
func openCodeSessionID(request canonical.Request) string {
	if value := strings.TrimSpace(request.SessionID); value != "" {
		return value
	}
	if derived := openCodeDerivedSessionID(request); derived != "" {
		return derived
	}
	if value := strings.TrimSpace(request.RequestID); value != "" {
		return value
	}
	return "tproxy"
}

// openCodeDerivedSessionID hashes the conversation's first user message so every
// turn of the same conversation maps to one stable session ID even when the
// client sends no session header.
func openCodeDerivedSessionID(request canonical.Request) string {
	var anchor *canonical.Message
	for index := range request.Messages {
		msg := &request.Messages[index]
		if anchor == nil {
			anchor = msg
		}
		if msg.Role == "user" {
			anchor = msg
			break
		}
	}
	if anchor == nil {
		return ""
	}
	data, err := json.Marshal(anchor)
	if err != nil || len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return "tproxy-" + hex.EncodeToString(sum[:])[:32]
}
