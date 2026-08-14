package auth

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterAcceptsSecondsAndDates(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	seconds := http.Header{"Retry-After": []string{"45"}}
	if got := parseRetryAfter(seconds, now); got != 45*time.Second {
		t.Fatalf("seconds form = %v", got)
	}

	date := http.Header{"Retry-After": []string{now.Add(90 * time.Second).Format(http.TimeFormat)}}
	if got := parseRetryAfter(date, now); got != 90*time.Second {
		t.Fatalf("date form = %v", got)
	}

	// Anthropic's millisecond variant takes precedence when both are present.
	both := http.Header{"Retry-After": []string{"45"}, "Retry-After-Ms": []string{"2500"}}
	if got := parseRetryAfter(both, now); got != 2500*time.Millisecond {
		t.Fatalf("millisecond form = %v", got)
	}
}

func TestParseRetryAfterIgnoresUnusableValues(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	for name, header := range map[string]http.Header{
		"absent":   {},
		"garbage":  {"Retry-After": []string{"soon"}},
		"zero":     {"Retry-After": []string{"0"}},
		"negative": {"Retry-After": []string{"-5"}},
		"past":     {"Retry-After": []string{now.Add(-time.Hour).Format(http.TimeFormat)}},
	} {
		if got := parseRetryAfter(header, now); got != 0 {
			t.Fatalf("%s: parsed %v, want 0", name, got)
		}
	}
	if got := parseRetryAfter(nil, now); got != 0 {
		t.Fatalf("nil header parsed %v", got)
	}
}

// The provider's own backoff wins, but only within bounds: a tiny value must
// not turn the retry into a hot loop, and an outsized one must not park a
// credential for hours.
func TestRefreshCooldownHonorsProviderBackoffWithinBounds(t *testing.T) {
	if got := refreshCooldown(&Error{code: "oauth_provider_unavailable", retryAfter: time.Minute}); got != time.Minute {
		t.Fatalf("in-range backoff = %v", got)
	}
	if got := refreshCooldown(&Error{code: "oauth_provider_unavailable", retryAfter: time.Millisecond}); got != minRefreshCooldown {
		t.Fatalf("clamped low = %v", got)
	}
	if got := refreshCooldown(&Error{code: "oauth_provider_unavailable", retryAfter: time.Hour}); got != maxRefreshCooldown {
		t.Fatalf("clamped high = %v", got)
	}
	if got := refreshCooldown(&Error{code: "oauth_provider_unavailable"}); got != defaultRefreshCooldown {
		t.Fatalf("no hint = %v", got)
	}
	if got := refreshCooldown(nil); got != defaultRefreshCooldown {
		t.Fatalf("nil error = %v", got)
	}
}
