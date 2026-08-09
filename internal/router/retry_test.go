package router

import (
	"context"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
)

func TestRetrySettingsDefaultsPreserveLegacyBehaviour(t *testing.T) {
	settings := RetrySettingsFromConfig(config.RetryConfig{})
	if settings.Attempts != 0 {
		t.Errorf("same-credential retries must be opt-in, got %d", settings.Attempts)
	}
	if !settings.allowCredentialAttempt(1000) {
		t.Error("an unset credential cap must stay unlimited")
	}
	if settings.MaxWait != 0 {
		t.Errorf("waiting must be opt-in, got %v", settings.MaxWait)
	}
	if settings.Backoff != defaultRetryBackoff {
		t.Errorf("backoff = %v, want %v", settings.Backoff, defaultRetryBackoff)
	}
}

func TestRetrySettingsCapsCredentials(t *testing.T) {
	settings := RetrySettingsFromConfig(config.RetryConfig{MaxCredentials: 2})
	if !settings.allowCredentialAttempt(0) || !settings.allowCredentialAttempt(1) {
		t.Error("the first two credentials must be allowed")
	}
	if settings.allowCredentialAttempt(2) {
		t.Error("the third credential must be rejected")
	}
}

func TestRetryOnlyRepeatsTransientFailures(t *testing.T) {
	settings := RetrySettingsFromConfig(config.RetryConfig{Attempts: 2})
	transient := &providers.ProviderError{Status: 502}
	if !settings.shouldRetrySameCredential(0, transient) {
		t.Error("502 should be retried on the same credential")
	}
	if !settings.shouldRetrySameCredential(1, transient) {
		t.Error("the second attempt should still be allowed")
	}
	if settings.shouldRetrySameCredential(2, transient) {
		t.Error("attempts beyond the limit must stop")
	}
	// Auth and quota problems need a different credential, not another try.
	for _, status := range []int{401, 402, 429} {
		if settings.shouldRetrySameCredential(0, &providers.ProviderError{Status: status}) {
			t.Errorf("status %d must not be retried on the same credential", status)
		}
	}
	if settings.shouldRetrySameCredential(0, nil) {
		t.Error("a successful call must not be retried")
	}
}

func TestWaitForCooldownRespectsBudget(t *testing.T) {
	settings := RetrySettingsFromConfig(config.RetryConfig{MaxWait: "50ms"})
	now := time.Now()
	if !settings.waitForCooldown(context.Background(), now.Add(10*time.Millisecond), now) {
		t.Error("a cooldown inside the budget should be waited out")
	}
	if settings.waitForCooldown(context.Background(), now.Add(time.Hour), now) {
		t.Error("a cooldown beyond the budget must not block the request")
	}

	disabled := RetrySettingsFromConfig(config.RetryConfig{})
	if disabled.waitForCooldown(context.Background(), now.Add(time.Millisecond), now) {
		t.Error("waiting must not happen when the budget is unset")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if settings.waitForCooldown(ctx, now.Add(40*time.Millisecond), now) {
		t.Error("a cancelled request must not keep waiting")
	}
}

func TestCooldownDisabledSuppressesEverything(t *testing.T) {
	settings := CooldownSettingsFromConfig(config.CooldownConfig{Disabled: true})
	for _, status := range []int{401, 429, 500, 503} {
		if !settings.SkipCooldown(status) {
			t.Errorf("status %d must be skipped when cooling is disabled", status)
		}
	}
}

func TestTransientCooldownCanBeDisabledAlone(t *testing.T) {
	settings := CooldownSettingsFromConfig(config.CooldownConfig{Transient: "off"})
	for _, status := range []int{408, 500, 502, 503, 504} {
		if !settings.SkipCooldown(status) {
			t.Errorf("transient status %d must be skipped", status)
		}
	}
	// Auth and quota backoffs must survive.
	for _, status := range []int{401, 402, 403, 429} {
		if settings.SkipCooldown(status) {
			t.Errorf("status %d must still cool down", status)
		}
	}
}

func TestTransientCooldownDurationOverride(t *testing.T) {
	settings := CooldownSettingsFromConfig(config.CooldownConfig{Transient: "90s"})
	if settings.Status5xx != 90*time.Second {
		t.Errorf("transient cooldown = %v, want 90s", settings.Status5xx)
	}
	if settings.SkipCooldown(503) {
		t.Error("a positive transient duration must not disable the cooldown")
	}
	// An explicit per-status entry still wins.
	explicit := CooldownSettingsFromConfig(config.CooldownConfig{Transient: "off", Status5xx: "42s"})
	if explicit.Status5xx != 42*time.Second || explicit.SkipCooldown(503) {
		t.Errorf("explicit status-5xx must override the shorthand: %v", explicit.Status5xx)
	}
}

func TestRequestTimeoutTreatedAsTransient(t *testing.T) {
	settings := CooldownSettingsFromConfig(config.CooldownConfig{Status5xx: "7s"})
	if got := settings.fallbackForStatus(408); got != 7*time.Second {
		t.Errorf("408 cooldown = %v, want it to follow the transient bucket", got)
	}
}
