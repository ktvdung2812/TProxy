package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/store"
)

// Claude "limit resets" are one-shot grants that refill the plan windows. They
// are surfaced in claude.ai Settings > Usage and in Claude Code's /limit-reset.
// Status rides on the usage endpoint behind a query flag; claiming goes through
// the organization reset_rate_limits endpoint with a program name.
const (
	claudeResetStatusEndpoint = claudeUsageEndpoint + "?at_wall=1&skip_spend=1"
	claudeResetClaimTemplate  = "https://api.anthropic.com/api/organizations/%s/reset_rate_limits"

	// ClaudeResetProgramGrant is the "Full reset" program: numbered grants
	// (promotions, launches) that each clear a fixed set of windows.
	ClaudeResetProgramGrant = "cedar_ember"
	// ClaudeResetProgramSession is the "5-hour reset" program: a weekly
	// session-limit refill offered once the account hits the session wall.
	ClaudeResetProgramSession = "juniper_tide"

	// The reset programs are gated server-side on the client version and
	// answer ineligible_reason=cli_version for the inference fingerprint's
	// older version, so these calls present a current Claude Code release.
	// Only the reset calls use it; the inference tuple in claude_client.go is
	// left alone because its version also feeds the billing-header hash.
	claudeResetClientVersion = "2.1.287"
)

// ClaudeResetGrant is one claimable Claude "Full reset" grant.
type ClaudeResetGrant struct {
	ID               string   `json:"id"`
	Label            string   `json:"label,omitempty"`
	ResetsTotal      int      `json:"resets_total"`
	ResetsLeft       int      `json:"resets_left"`
	StartsAt         string   `json:"starts_at,omitempty"`
	EndsAt           string   `json:"ends_at,omitempty"`
	Clears           []string `json:"clears,omitempty"`
	Paused           bool     `json:"paused,omitempty"`
	UsableNow        bool     `json:"usable_now"`
	UseRequiresLimit bool     `json:"use_requires_limit,omitempty"`
}

// ClaudeSessionReset is the weekly "5-hour reset" offer.
type ClaudeSessionReset struct {
	Eligible         bool   `json:"eligible"`
	IneligibleReason string `json:"ineligible_reason,omitempty"`
	Available        bool   `json:"available"`
	NextAvailableAt  string `json:"next_available_at,omitempty"`
	ResetsPerWeek    int    `json:"resets_per_week,omitempty"`
}

// ClaudeLimitResets is the reset inventory for a Claude credential.
type ClaudeLimitResets struct {
	Eligible         bool                `json:"eligible"`
	IneligibleReason string              `json:"ineligible_reason,omitempty"`
	AtLimit          bool                `json:"at_limit,omitempty"`
	Grants           []ClaudeResetGrant  `json:"grants"`
	NextGrantID      string              `json:"next_grant_id,omitempty"`
	CooldownUntil    string              `json:"cooldown_until,omitempty"`
	WeeklyResetsAt   string              `json:"weekly_resets_at,omitempty"`
	Session          *ClaudeSessionReset `json:"session,omitempty"`
}

func claudeResetHeaders(credential store.Credential) http.Header {
	headers := authHeaders(store.Provider{Type: "claude"}, credential)
	headers.Set("anthropic-version", claudeAnthropicVersion)
	headers.Set("anthropic-beta", "oauth-2025-04-20")
	headers.Set("Accept", "application/json")
	headers.Set("User-Agent", fmt.Sprintf(claudeUserAgentTemplate, claudeResetClientVersion, claudeCodeEntrypoint))
	return headers
}

func claudeResetPreflight(provider store.Provider, credential store.Credential) error {
	if provider.Type != "claude" {
		return &ProviderError{Code: "invalid_request", Message: "limit resets are only available for Claude credentials", Status: http.StatusBadRequest}
	}
	if credential.Secret == "" || credential.AuthType != "oauth" {
		return &ProviderError{Code: "authorization_required", Message: "limit resets need a Claude OAuth login", Status: http.StatusUnauthorized}
	}
	return nil
}

// ClaudeLimitResets reads the reset grants and the 5-hour reset offer.
func (r *Registry) ClaudeLimitResets(ctx context.Context, provider store.Provider, credential store.Credential) (ClaudeLimitResets, error) {
	if err := claudeResetPreflight(provider, credential); err != nil {
		return ClaudeLimitResets{}, err
	}
	// Same endpoint as the quota probe, so it shares its rate-limit cooldown.
	if claudeUsageThrottled(credential.ID, time.Now()) {
		return ClaudeLimitResets{}, &ProviderError{Code: "rate_limited", Message: "Claude usage API is cooling down after a rate limit; try again in a few minutes", Status: http.StatusTooManyRequests}
	}
	ctx = withClaudeTransport(ctx, provider, credential)
	body, status, err := r.quotaGET(ctx, claudeResetStatusEndpoint, claudeResetHeaders(credential))
	if err != nil {
		return ClaudeLimitResets{}, err
	}
	if status == http.StatusTooManyRequests {
		setClaudeUsageThrottle(credential.ID, time.Now().Add(claudeUsageCooldown))
		return ClaudeLimitResets{}, &ProviderError{Code: "rate_limited", Message: "Claude usage API rate limited; try again in a few minutes", Status: status}
	}
	if status != http.StatusOK {
		return ClaudeLimitResets{}, &ProviderError{Code: "claude_reset_status_failed", Message: fmt.Sprintf("Claude usage API unavailable (HTTP %d)", status), Status: status}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ClaudeLimitResets{}, &ProviderError{Code: "claude_reset_status_failed", Message: "invalid Claude usage response", Err: err}
	}
	return parseClaudeLimitResets(payload), nil
}

func parseClaudeLimitResets(payload map[string]any) ClaudeLimitResets {
	result := ClaudeLimitResets{Grants: []ClaudeResetGrant{}}
	if block, ok := payload["cedar_ember"].(map[string]any); ok {
		result.Eligible = block["eligible"] == true
		result.IneligibleReason = stringValue(block["ineligible_reason"])
		result.AtLimit = block["at_limit"] == true
		result.CooldownUntil = parseResetAt(block["cooldown_until"])
		result.WeeklyResetsAt = parseResetAt(block["weekly_resets_at"])
		grants, _ := block["grants"].([]any)
		for _, raw := range grants {
			item, _ := raw.(map[string]any)
			id := stringValue(item["id"])
			if item == nil || id == "" {
				continue
			}
			grant := ClaudeResetGrant{
				ID:               id,
				Label:            stringValue(item["label"]),
				ResetsTotal:      int(numberValue(item["resets_total"])),
				ResetsLeft:       int(numberValue(item["resets_left"])),
				StartsAt:         parseResetAt(item["starts_at"]),
				EndsAt:           parseResetAt(item["ends_at"]),
				Paused:           item["paused"] == true,
				UsableNow:        item["usable_now"] == true,
				UseRequiresLimit: item["use_requires_limit"] == true,
			}
			clears, _ := item["clears"].([]any)
			for _, window := range clears {
				if name := stringValue(window); name != "" {
					grant.Clears = append(grant.Clears, name)
				}
			}
			result.Grants = append(result.Grants, grant)
		}
		// Only point at a grant that is actually in the list, as the CLI does.
		if next := stringValue(block["next_grant_id"]); next != "" {
			for _, grant := range result.Grants {
				if grant.ID == next {
					result.NextGrantID = next
					break
				}
			}
		}
	}
	if block, ok := payload["juniper_tide"].(map[string]any); ok {
		result.Session = &ClaudeSessionReset{
			Eligible:         block["eligible"] == true,
			IneligibleReason: stringValue(block["ineligible_reason"]),
			Available:        block["available"] == true,
			NextAvailableAt:  parseResetAt(block["next_available_at"]),
			ResetsPerWeek:    int(numberValue(block["resets_per_week"])),
		}
	}
	return result
}

// ClaudeResetClaimResult is the outcome of claiming one Claude limit reset.
type ClaudeResetClaimResult struct {
	OK              bool     `json:"ok"`
	Result          string   `json:"result"`
	Reason          string   `json:"reason,omitempty"`
	ResetsLeft      *int     `json:"resets_left,omitempty"`
	Cleared         []string `json:"cleared,omitempty"`
	WeeklyResetsAt  string   `json:"weekly_resets_at,omitempty"`
	CooldownUntil   string   `json:"cooldown_until,omitempty"`
	NextAvailableAt string   `json:"next_available_at,omitempty"`
	RequestID       string   `json:"request_id,omitempty"`
}

// ClaimClaudeLimitReset spends one Claude limit reset. program is
// ClaudeResetProgramGrant (needs grantID) or ClaudeResetProgramSession.
// requestID makes the claim idempotent upstream; retries must reuse it.
func (r *Registry) ClaimClaudeLimitReset(ctx context.Context, provider store.Provider, credential store.Credential, program, grantID, requestID string) (ClaudeResetClaimResult, error) {
	if err := claudeResetPreflight(provider, credential); err != nil {
		return ClaudeResetClaimResult{}, err
	}
	body := map[string]string{"program": program}
	switch program {
	case ClaudeResetProgramGrant:
		grantID = strings.TrimSpace(grantID)
		if grantID == "" {
			return ClaudeResetClaimResult{}, &ProviderError{Code: "invalid_request", Message: "grant_id is required for a full reset", Status: http.StatusBadRequest}
		}
		if requestID == "" {
			requestID = uuid.NewString()
		}
		body["grant_id"] = grantID
		body["request_id"] = requestID
	case ClaudeResetProgramSession:
	default:
		return ClaudeResetClaimResult{}, &ProviderError{Code: "invalid_request", Message: "unknown reset program", Status: http.StatusBadRequest}
	}
	ctx = withClaudeTransport(ctx, provider, credential)
	headers := claudeResetHeaders(credential)
	orgID, err := r.claudeOrganizationID(ctx, headers)
	if err != nil {
		return ClaudeResetClaimResult{}, err
	}
	encoded, _ := json.Marshal(body)
	raw, status, err := r.quotaJSON(ctx, http.MethodPost, fmt.Sprintf(claudeResetClaimTemplate, url.PathEscape(orgID)), headers, encoded)
	if err != nil {
		return ClaudeResetClaimResult{}, err
	}
	switch {
	case status == http.StatusTooManyRequests:
		return ClaudeResetClaimResult{Result: "rate_limited", RequestID: requestID}, &ProviderError{Code: "rate_limited", Message: "Claude rate limited the reset; try again in a minute", Status: status}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ClaudeResetClaimResult{Result: "auth_error", RequestID: requestID}, &ProviderError{Code: "authorization_required", Message: "Claude rejected this login for limit resets; re-authenticate the account", Status: status}
	case status < 200 || status >= 300:
		return ClaudeResetClaimResult{Result: "error", RequestID: requestID}, &ProviderError{Code: "claude_reset_claim_failed", Message: claudeResetErrorMessage(raw, status), Status: status}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ClaudeResetClaimResult{Result: "error", RequestID: requestID}, &ProviderError{Code: "claude_reset_claim_failed", Message: "unreadable Claude reset response", Status: http.StatusBadGateway, Err: err}
	}
	result := ClaudeResetClaimResult{
		Result:          stringValue(payload["result"]),
		Reason:          stringValue(payload["reason"]),
		WeeklyResetsAt:  parseResetAt(payload["weekly_resets_at"]),
		CooldownUntil:   parseResetAt(payload["cooldown_until"]),
		NextAvailableAt: parseResetAt(payload["next_available_at"]),
		RequestID:       requestID,
	}
	if left, ok := payload["resets_left"].(float64); ok {
		count := int(left)
		result.ResetsLeft = &count
	}
	cleared, _ := payload["cleared"].([]any)
	for _, window := range cleared {
		if name := stringValue(window); name != "" {
			result.Cleared = append(result.Cleared, name)
		}
	}
	if result.Result == "" {
		result.Result = "unavailable"
	}
	result.OK = result.Result == "reset"
	return result, nil
}

// claudeOrganizationID resolves the account's organization from the OAuth
// profile; the reset endpoint is scoped to it.
func (r *Registry) claudeOrganizationID(ctx context.Context, headers http.Header) (string, error) {
	raw, status, err := r.quotaGET(ctx, claudeProfileEndpoint, headers)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", &ProviderError{Code: "claude_profile_failed", Message: fmt.Sprintf("Claude profile API unavailable (HTTP %d)", status), Status: status}
	}
	var profile struct {
		Organization struct {
			UUID string `json:"uuid"`
		} `json:"organization"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil || profile.Organization.UUID == "" {
		return "", &ProviderError{Code: "claude_profile_failed", Message: "Claude profile has no organization", Status: http.StatusBadGateway}
	}
	return profile.Organization.UUID, nil
}

func claudeResetErrorMessage(body []byte, status int) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		if nested, ok := payload["error"].(map[string]any); ok {
			if message := stringValue(nested["message"]); message != "" {
				return message
			}
		}
		if message := stringValue(firstValue(payload, "message", "detail")); message != "" {
			return message
		}
	}
	return fmt.Sprintf("Claude reset API unavailable (HTTP %d)", status)
}
