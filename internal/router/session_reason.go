package router

import "net/http"

// FailureReason classifies why a request moved off the credential that a session
// was bound to. Session affinity uses it to decide whether the move is worth
// remembering: a quota or auth failure will keep failing on that account, while a
// transient 5xx is usually the provider having a bad minute.
type FailureReason int

const (
	// FailureNone means the request did not fail on the previous credential: it
	// was the first attempt, or the previous attempt was never dispatched.
	FailureNone FailureReason = iota
	// FailureTransient covers 408, 500, 502, 503, 504, an empty stream, and
	// anything else that is neither quota nor auth. One request may well succeed
	// elsewhere, but the original account is still the better default.
	FailureTransient
	// FailureQuota covers 429 and exhausted-quota responses: the account is out
	// of budget and will keep failing, so the session should follow the traffic.
	FailureQuota
	// FailureAuth covers 401 and 403: the credential itself is unusable.
	FailureAuth
)

// IsQuota reports whether the failure is a quota or auth problem, meaning the
// credential will keep failing rather than having a bad minute.
func (f FailureReason) IsQuota() bool { return f == FailureQuota || f == FailureAuth }

// failureReasonForStatus classifies an upstream status.
func failureReasonForStatus(status int) FailureReason {
	switch status {
	case http.StatusTooManyRequests, http.StatusPaymentRequired:
		return FailureQuota
	case http.StatusUnauthorized, http.StatusForbidden:
		return FailureAuth
	case 0:
		return FailureNone
	default:
		return FailureTransient
	}
}
