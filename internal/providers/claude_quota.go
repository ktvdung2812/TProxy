package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

// claudeUsageEndpoint is the Claude Code consumer OAuth usage probe. It reports
// rolling windows as percent-utilization rather than absolute token counts.
const claudeUsageEndpoint = "https://api.anthropic.com/api/oauth/usage"

// The usage endpoint rate-limits independently of inference: a 429 here does not
// mean the credential is out of quota, only that the probe was polled too often.
// Cool the probe down per credential so a dashboard refresh loop cannot hammer it.
const claudeUsageCooldown = 3 * time.Minute

var claudeUsageThrottle = struct {
	mu    sync.Mutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

func claudeUsageThrottled(credentialID string, now time.Time) bool {
	if credentialID == "" {
		return false
	}
	claudeUsageThrottle.mu.Lock()
	defer claudeUsageThrottle.mu.Unlock()
	until, ok := claudeUsageThrottle.until[credentialID]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(claudeUsageThrottle.until, credentialID)
	return false
}

func setClaudeUsageThrottle(credentialID string, until time.Time) {
	if credentialID == "" {
		return
	}
	claudeUsageThrottle.mu.Lock()
	defer claudeUsageThrottle.mu.Unlock()
	claudeUsageThrottle.until[credentialID] = until
}

func (r *Registry) claudeQuota(ctx context.Context, provider store.Provider, credential store.Credential) (CredentialQuota, error) {
	// The probe hits the same host as inference, so it must present the same
	// client; two handshakes from one account is itself a signal.
	ctx = withClaudeTransport(ctx, provider, credential)
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: "claude",
		Quotas:       map[string]QuotaEntry{},
	}
	if claudeUsageThrottled(credential.ID, time.Now()) {
		result.Message = "Claude usage probe is cooling down after an upstream rate limit."
		return result, nil
	}
	headers := authHeaders(store.Provider{Type: "claude"}, credential)
	headers.Set("anthropic-version", claudeAnthropicVersion)
	// The usage endpoint is OAuth-scoped and rejects requests that do not carry
	// the OAuth beta flag, the same one the inference path sends.
	headers.Set("anthropic-beta", "oauth-2025-04-20")
	headers.Set("Accept", "application/json")
	// Without this the request goes out as Go's default client, which reads as
	// a bot to the edge in front of this endpoint and draws a 429 that has
	// nothing to do with the account's actual usage.
	headers.Set("User-Agent", claudeUserAgent())
	body, status, err := r.quotaGET(ctx, claudeUsageEndpoint, headers)
	if err != nil {
		return result, err
	}
	if status == http.StatusTooManyRequests {
		setClaudeUsageThrottle(credential.ID, time.Now().Add(claudeUsageCooldown))
		result.Message = "Claude usage API rate limited; retrying later."
		return result, nil
	}
	if status != http.StatusOK {
		// Report the status rather than a generic line: a 401 means the token
		// lost the usage scope, a 403 means the edge blocked the probe, and
		// each needs a different fix.
		result.Message = fmt.Sprintf("Claude usage API unavailable (HTTP %d)", status)
		return result, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Claude connected. Usage is tracked per request."
		return result, nil
	}
	appendClaudeUsageWindows(result.Quotas, payload)
	if plan := stringValue(firstValue(payload, "plan", "plan_type", "subscription_type")); plan != "" {
		result.Plan = plan
	} else if len(result.Quotas) > 0 {
		result.Plan = "Claude Code"
	}
	if len(result.Quotas) == 0 {
		result.Message = "Claude connected. Usage is tracked per request."
	}
	return result, nil
}

// appendClaudeUsageWindows normalizes the OAuth usage payload into quota
// windows. Anthropic reports each window as `utilization`, the percentage of the
// window already consumed, so the entries are expressed on a 0-100 scale rather
// than in tokens. Keys follow the Codex convention (`session`, `weekly`) so the
// dashboard and the routing gate in quotaKeyAffectsRouting treat both providers
// alike.
func appendClaudeUsageWindows(quotas map[string]QuotaEntry, payload map[string]any) {
	for key, raw := range payload {
		window, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, ok := claudeWindowName(key)
		if !ok {
			continue
		}
		entry, ok := claudeWindowEntry(name, window)
		if !ok {
			continue
		}
		quotas[name] = entry
	}
}

// claudeWindowName maps an upstream usage key to a dashboard window name.
// Anything that is not a recognized rolling window (extra_usage, account
// metadata) is skipped.
func claudeWindowName(key string) (string, bool) {
	switch {
	case key == "five_hour":
		return "session", true
	case key == "seven_day":
		return "weekly", true
	case strings.HasPrefix(key, "seven_day_"):
		model := strings.TrimPrefix(key, "seven_day_")
		if model == "" {
			return "", false
		}
		return "weekly_" + model, true
	default:
		return "", false
	}
}

func claudeWindowEntry(name string, window map[string]any) (QuotaEntry, bool) {
	raw, ok := window["utilization"]
	if !ok {
		return QuotaEntry{}, false
	}
	used, ok := claudeUtilization(raw)
	if !ok {
		return QuotaEntry{}, false
	}
	return QuotaEntry{
		Name:      name,
		Used:      used,
		Total:     100,
		Remaining: math.Max(0, 100-used),
		ResetAt:   parseResetAt(firstValue(window, "resets_at", "reset_at")),
	}, true
}

// claudeUtilization accepts only a real number: a missing or non-numeric
// utilization must not be reported as 0% used, which would look like a fully
// available window.
func claudeUtilization(raw any) (float64, bool) {
	var used float64
	switch typed := raw.(type) {
	case float64:
		used = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		used = parsed
	case int:
		used = float64(typed)
	case int64:
		used = float64(typed)
	default:
		return 0, false
	}
	return math.Max(0, math.Min(100, used)), true
}
