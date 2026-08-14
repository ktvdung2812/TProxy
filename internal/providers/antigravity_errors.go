package providers

import (
	"errors"
	"net/http"
	"strings"
)

// Cloud Code rejects an account that has not finished Antigravity's own sign-up
// with a bare 403 and a message that reads like it came from the gateway
// ("Verify your account to continue."). Operators reasonably read that as a
// tproxy fault and go looking for a configuration mistake, when the credential
// is valid and the account simply has not been cleared upstream yet. Naming the
// source and the remedy in the message is the difference between a five-minute
// fix and an hour of debugging the proxy.
const antigravityAccountVerificationHint = "Antigravity hint: this rejection comes from Google, not tproxy. The OAuth token is valid, but the account has not finished Antigravity's own sign-up. Open the Antigravity IDE (or antigravity.google.com), sign in with this same Google account, complete the verification/terms prompt, then retry."

func antigravityUpstreamError(response *http.Response) error {
	err := upstreamError(response)
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		return err
	}
	if hint := antigravityAccountHint(providerErr.Status, providerErr.Message); hint != "" {
		providerErr.Message = strings.TrimSpace(providerErr.Message + " " + hint)
	}
	return providerErr
}

// Cloud Code answers this 429 with no error details at all — no ErrorInfo
// reason, no RetryInfo delay — so neither tproxy nor the operator can tell from
// the response which limit was hit. What makes it genuinely confusing is that
// the quota panel keeps reading nearly full while every request fails: the
// remainingFraction the catalogue reports tracks the rolling model quota, which
// is a different budget from the request throttle that produces this. Observed
// directly against a live account: 93% remaining on every model, and every
// generateContent call rejected regardless of model, headers, client version or
// project scope, for minutes on end.
//
// The practical trap is that retrying makes it worse, and the dashboard's own
// "Test all" button fans out across every discovered model at once.
const antigravityResourceExhaustedHint = "Antigravity hint: Google sends no reason with this one, and the quota percentages shown here track a different budget — they can read nearly full while every request is still refused. It is an upstream throttle on the account, not a gateway fault, and retrying extends it. Give the account several minutes of quiet before trying again, avoid fanning out across many models at once, and check Antigravity's own Models & Usage panel for the weekly limit, which is not visible here."

// antigravityAccountHint recognises upstream rejections whose text does not say
// what actually went wrong.
func antigravityAccountHint(status int, message string) string {
	lower := strings.ToLower(message)
	switch status {
	case http.StatusForbidden:
		if strings.Contains(lower, "verify your account") ||
			strings.Contains(lower, "verify this account") ||
			strings.Contains(lower, "account verification") {
			return antigravityAccountVerificationHint
		}
	case http.StatusTooManyRequests:
		// A response that names the real cause needs no help.
		if strings.Contains(lower, "quota_exhausted") || strings.Contains(lower, "quota exhausted") {
			return ""
		}
		if strings.Contains(lower, "resource has been exhausted") || strings.Contains(lower, "resource_exhausted") {
			return antigravityResourceExhaustedHint
		}
	}
	return ""
}
