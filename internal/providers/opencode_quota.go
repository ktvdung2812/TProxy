package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

// OpenCode Go exposes two key-authenticated quota endpoints on the console host:
//
//	GET /console/api/go/status — subscription meters in micro-cents plus the
//	    access window, whose endsAt doubles as the renewal date.
//	GET /zen/go/v1/usage       — public percent-only view of the same windows.
//
// The status payload carries real dollar amounts and the renewal date, so it is
// the primary probe; the usage endpoint is the fallback for deployments where
// the console route is missing.
func (r *Registry) opencodeGoQuota(ctx context.Context, provider store.Provider, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: "opencode-go",
		Quotas:       map[string]QuotaEntry{},
		Plan:         "Go",
	}
	base := openCodeGoQuotaBase(provider)
	headers := authHeaders(provider, credential)
	body, status, err := r.quotaGET(ctx, base+"/console/api/go/status", headers)
	fallback := status == http.StatusNotFound
	if fallback {
		body, status, err = r.quotaGET(ctx, base+"/zen/go/v1/usage", headers)
	}
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		result.Message = "OpenCode Go API key invalid or expired."
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("OpenCode Go quota API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid OpenCode Go quota response"
		return result
	}
	if fallback {
		result.RenewsAt, result.Quotas = parseOpenCodeGoUsage(payload)
	} else {
		result.RenewsAt, result.Quotas = parseOpenCodeGoStatus(payload)
	}
	if len(result.Quotas) == 0 {
		result.Message = "OpenCode Go connected. No quota windows returned."
	}
	return result
}

// isOpenCodeGoQuotaProvider matches the preset by ID and custom providers
// pointing at the Go endpoint, so manually configured entries get quota too.
func isOpenCodeGoQuotaProvider(provider store.Provider) bool {
	if strings.EqualFold(strings.TrimSpace(provider.ID), "opencode-go") {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(provider.BaseURL)), "opencode.ai/zen/go")
}

// openCodeGoQuotaBase derives the console host from the provider base URL so
// custom mirrors probe their own host instead of leaking the key to opencode.ai.
func openCodeGoQuotaBase(provider store.Provider) string {
	base := strings.TrimSpace(provider.BaseURL)
	if parsed, err := url.Parse(base); err == nil && parsed.Host != "" {
		scheme := parsed.Scheme
		if scheme == "" {
			scheme = "https"
		}
		return scheme + "://" + parsed.Host
	}
	return "https://opencode.ai"
}

// parseOpenCodeGoStatus reads the console status payload. Meter amounts are
// micro-cent strings; access.endsAt is the subscription renewal date and also
// closes the month meter, which carries no resetsAt of its own.
func parseOpenCodeGoStatus(payload map[string]any) (string, map[string]QuotaEntry) {
	quotas := map[string]QuotaEntry{}
	access, _ := payload["access"].(map[string]any)
	if access == nil {
		return "", quotas
	}
	renewsAt := parseResetAt(firstValue(access, "endsAt", "ends_at"))
	meters, _ := access["meters"].(map[string]any)
	if meters == nil {
		return renewsAt, quotas
	}
	for _, window := range []struct {
		field, key, name, fallbackReset string
	}{
		{"fiveHour", "session", "Session", ""},
		{"week", "weekly", "Weekly", ""},
		{"month", "monthly", "Monthly", renewsAt},
	} {
		meter, _ := meters[window.field].(map[string]any)
		if meter == nil {
			continue
		}
		quotas[window.key] = openCodeGoMeterEntry(window.name, meter, window.fallbackReset)
	}
	return renewsAt, quotas
}

func openCodeGoMeterEntry(name string, meter map[string]any, fallbackReset string) QuotaEntry {
	total := kimiQuotaNumber(meter["limitMicroCents"]) / 1e8
	used := kimiQuotaNumber(meter["usedMicroCents"]) / 1e8
	reset := parseResetAt(firstValue(meter, "resetsAt", "resets_at"))
	if reset == "" {
		reset = fallbackReset
	}
	return QuotaEntry{
		Name:      name,
		Used:      used,
		Total:     total,
		Remaining: quotaPercentRemaining(used, total),
		ResetAt:   reset,
	}
}

// parseOpenCodeGoUsage reads the public usage endpoint, which reports each
// window as a percent used. monthly.resetsAt lands on the subscription's
// renewal date, so it doubles as RenewsAt.
func parseOpenCodeGoUsage(payload map[string]any) (string, map[string]QuotaEntry) {
	quotas := map[string]QuotaEntry{}
	usage, _ := payload["usage"].(map[string]any)
	renewsAt := ""
	for _, window := range []struct {
		field, key, name string
	}{
		{"rolling", "session", "Session"},
		{"weekly", "weekly", "Weekly"},
		{"monthly", "monthly", "Monthly"},
	} {
		meter, _ := usage[window.field].(map[string]any)
		if meter == nil {
			continue
		}
		used := kimiQuotaNumber(meter["percent"])
		if used < 0 {
			used = 0
		}
		if used > 100 {
			used = 100
		}
		reset := parseResetAt(firstValue(meter, "resetsAt", "resets_at"))
		if window.key == "monthly" {
			renewsAt = reset
		}
		quotas[window.key] = QuotaEntry{
			Name:      window.name,
			Used:      used,
			Total:     100,
			Remaining: 100 - used,
			ResetAt:   reset,
		}
	}
	return renewsAt, quotas
}
