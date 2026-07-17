package router

import (
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
)

func withinJitter(expected time.Duration, actual time.Duration) bool {
	min := time.Duration(float64(expected) * 0.79)
	max := time.Duration(float64(expected) * 1.21)
	return actual >= min && actual <= max
}

func TestCooldownUntilUsesRetryAfterSeconds(t *testing.T) {
	settings := CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	until := settings.ComputeUntil(now, 429, "30", 0)
	if !withinJitter(30*time.Second, until.Sub(now)) {
		t.Fatalf("expected ~30s cooldown, got %s", until.Sub(now))
	}
}

func TestCooldownUntilAuthErrors(t *testing.T) {
	settings := CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	until := settings.ComputeUntil(now, 401, "", 0)
	if !withinJitter(5*time.Minute, until.Sub(now)) {
		t.Fatalf("expected ~5m auth cooldown, got %s", until.Sub(now))
	}
}

func TestCredentialCooldownUntilReadsProviderError(t *testing.T) {
	settings := CooldownSettingsFromConfig(config.CooldownConfig{})
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	err := &providers.ProviderError{Status: 429, RetryAfter: "45"}
	until := credentialCooldownUntil(now, settings, err, 0)
	if !withinJitter(45*time.Second, until.Sub(now)) {
		t.Fatalf("expected ~45s cooldown, got %s", until.Sub(now))
	}
}
