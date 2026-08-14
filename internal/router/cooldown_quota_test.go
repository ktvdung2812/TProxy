package router_test

import (
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/router"
)

// Antigravity answers 429 both for a rate limit that clears in under a second
// and for a daily quota that is gone, and the quota-exhausted response still
// carries a retryDelay. Obeying that delay put the credential back in rotation
// after ~30s, so every subsequent request rediscovered the same dead account.
func TestQuotaExhaustedIgnoresShortRetryDelay(t *testing.T) {
	settings := router.CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Now()

	until := settings.ComputeUntilWithReason(now, 429, "30s", "QUOTA_EXHAUSTED", 0)
	if got := until.Sub(now); got < 40*time.Second {
		t.Fatalf("quota-exhausted cooldown = %v, want the configured 429 window rather than the 30s retryDelay", got)
	}
}

func TestRateLimitHonoursShortRetryDelay(t *testing.T) {
	settings := router.CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Now()

	until := settings.ComputeUntilWithReason(now, 429, "0.479417207s", "RATE_LIMIT_EXCEEDED", 0)
	if got := until.Sub(now); got > 2*time.Second {
		t.Fatalf("rate-limit cooldown = %v, want it to track the sub-second retryDelay", got)
	}
}

// A drained account must not be retried at a fixed interval forever; repeat
// exhaustion has to widen the window.
func TestQuotaExhaustedBackoffGrows(t *testing.T) {
	settings := router.CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Now()

	first := settings.ComputeUntilWithReason(now, 429, "30s", "QUOTA_EXHAUSTED", 0).Sub(now)
	later := settings.ComputeUntilWithReason(now, 429, "30s", "QUOTA_EXHAUSTED", 3).Sub(now)
	if later <= first {
		t.Fatalf("cooldown did not grow with repeated exhaustion: first=%v later=%v", first, later)
	}
}

func TestQuotaExhaustedCooldownRespectsMax(t *testing.T) {
	settings := router.CooldownSettingsFromConfig(config.CooldownConfig{Max: "10m"})
	now := time.Now()

	until := settings.ComputeUntilWithReason(now, 429, "30s", "QUOTA_EXHAUSTED", 20)
	if got := until.Sub(now); got > 10*time.Minute {
		t.Fatalf("cooldown = %v, want it capped at the configured max of 10m", got)
	}
}

// An unrelated reason must not be mistaken for exhaustion.
func TestUnknownReasonKeepsRetryDelay(t *testing.T) {
	settings := router.CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Now()

	until := settings.ComputeUntilWithReason(now, 429, "2s", "SOMETHING_ELSE", 0)
	if got := until.Sub(now); got > 5*time.Second {
		t.Fatalf("cooldown = %v, want the 2s retryDelay to be honoured", got)
	}
}

// RESOURCE_EXHAUSTED is the gRPC status Google puts on both a momentary throttle
// and a spent daily allowance. Treating it as exhaustion benched a healthy
// credential for minutes over a transient rate limit.
func TestBareResourceExhaustedIsNotTreatedAsQuotaExhaustion(t *testing.T) {
	settings := router.CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Now()

	// With a short retryDelay the credential must come back quickly.
	until := settings.ComputeUntilWithReason(now, 429, "0.5s", "RESOURCE_EXHAUSTED", 0)
	if got := until.Sub(now); got > 2*time.Second {
		t.Fatalf("cooldown = %v, want the sub-second retryDelay to be honoured", got)
	}
}

// The genuine signal must still escalate.
func TestQuotaExhaustedReasonStillEscalates(t *testing.T) {
	settings := router.CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Now()

	until := settings.ComputeUntilWithReason(now, 429, "0.5s", "QUOTA_EXHAUSTED", 0)
	if got := until.Sub(now); got < 40*time.Second {
		t.Fatalf("cooldown = %v, want the retryDelay ignored for a spent quota", got)
	}
}
