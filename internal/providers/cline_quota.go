package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

// Cline's account API is the same host the proxy calls for chat:
//
//	GET /api/v1/users/me                     — profile + organization list
//	GET /api/v1/users/{id}/balance           — personal credit balance in cents
//	GET /api/v1/organizations/{id}/balance   — org credit balance in cents
//
// There is no percent-utilization meter: Cline bills a prepaid credit balance,
// so the windows report the dollar balance directly. A balance at or below
// zero is the exhausted state — the API rejects generation with 402s until
// credits are topped up.
const clineAPIBase = "https://api.cline.bot"

func (r *Registry) clineQuota(ctx context.Context, provider store.Provider, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
	}
	if result.ProviderType == "" {
		result.ProviderType = "cline"
	}
	headers := http.Header{"Accept": {"application/json"}}
	applyClineAuthHeaders(headers, credential)
	if headers.Get("Authorization") == "" {
		result.Message = "Cline credential has no access token."
		return result
	}

	meBody, status, err := r.quotaGET(ctx, clineAPIBase+"/api/v1/users/me", headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		result.Message = "Cline authentication expired. Re-authorize the account; API keys cannot read the balance endpoint."
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Cline account API unavailable (HTTP %d)", status)
		return result
	}
	var mePayload map[string]any
	if json.Unmarshal(meBody, &mePayload) != nil {
		result.Message = "Invalid Cline account response"
		return result
	}
	me, _ := mePayload["data"].(map[string]any)
	userID := strings.TrimSpace(stringValue(firstValue(me, "id", "userId", "user_id")))
	if userID == "" {
		result.Message = "Cline connected. Account profile did not include a user ID."
		return result
	}

	orgs := clineOrganizations(me)
	result.Plan = clinePlan(me, orgs)

	// Fetch the personal balance and every org balance in parallel; a
	// credential routes as long as ANY balance it can bill against is positive.
	type balanceResult struct {
		key, label string
		cents      float64
		ok         bool
	}
	type target struct {
		key, label, url string
	}
	targets := []target{{key: "credits", label: "Credits", url: clineAPIBase + "/api/v1/users/" + userID + "/balance"}}
	for _, org := range orgs {
		targets = append(targets, target{
			key:   "org_" + org.id,
			label: org.name + " credits",
			url:   clineAPIBase + "/api/v1/organizations/" + org.id + "/balance",
		})
	}
	results := make(chan balanceResult, len(targets))
	for _, t := range targets {
		go func(t target) {
			body, status, err := r.quotaGET(ctx, t.url, headers)
			if err != nil || status < 200 || status >= 300 {
				results <- balanceResult{key: t.key, label: t.label}
				return
			}
			var payload map[string]any
			if json.Unmarshal(body, &payload) != nil {
				results <- balanceResult{key: t.key, label: t.label}
				return
			}
			data, _ := payload["data"].(map[string]any)
			cents, ok := clineBalanceCents(data)
			results <- balanceResult{key: t.key, label: t.label, cents: cents, ok: ok}
		}(t)
	}
	for range targets {
		bal := <-results
		if !bal.ok {
			continue
		}
		result.Quotas[bal.key] = clineBalanceEntry(bal.label, bal.cents)
	}
	if len(result.Quotas) == 0 {
		result.Message = "Cline connected. Balance endpoint returned no data."
	}
	return result
}

type clineOrg struct {
	id   string
	name string
}

// clineOrganizations reads the org memberships from users/me. Entries are
// {organizationId, name, role}; a credential with orgs can bill against the
// org balance instead of (or in addition to) its personal credits.
func clineOrganizations(me map[string]any) []clineOrg {
	items, _ := me["organizations"].([]any)
	orgs := make([]clineOrg, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		id := strings.TrimSpace(stringValue(firstValue(item, "organizationId", "organization_id", "id")))
		if id == "" {
			continue
		}
		name := strings.TrimSpace(stringValue(firstValue(item, "name", "organizationName", "organization_name")))
		if name == "" {
			name = id
		}
		orgs = append(orgs, clineOrg{id: id, name: name})
	}
	return orgs
}

// clinePlan labels the account by where its usage bills: org memberships mean
// a team/workspace context; a lone user is a personal credits account.
func clinePlan(me map[string]any, orgs []clineOrg) string {
	if plan := strings.TrimSpace(stringValue(firstValue(me, "plan", "subscription", "subscription_plan", "membership"))); plan != "" {
		return plan
	}
	if len(orgs) > 0 {
		names := make([]string, 0, len(orgs))
		for _, org := range orgs {
			names = append(names, org.name)
		}
		return strings.Join(names, ", ")
	}
	return "Personal"
}

// clineBalanceCents accepts the {data:{balance: cents}} envelope; the value
// can legitimately be negative when usage outran purchased credits.
func clineBalanceCents(data map[string]any) (float64, bool) {
	raw := firstValue(data, "balance", "credit_balance", "creditBalance")
	if raw == nil {
		return 0, false
	}
	return kimiQuotaNumber(raw), true
}

// clineBalanceEntry renders a credit balance as a quota window. Positive
// balances show the dollar amount with 100% remaining; zero or negative
// balances are the exhausted state that auto-disables the credential.
func clineBalanceEntry(label string, cents float64) QuotaEntry {
	usd := cents / 100
	name := fmt.Sprintf("%s $%.2f", label, usd)
	if cents > 0 {
		return QuotaEntry{Name: name, Used: 0, Total: usd, Remaining: 100}
	}
	return QuotaEntry{Name: name, Used: 1, Total: 1, Remaining: 0}
}
