package router

import (
	"context"
	"net/http"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
)

const defaultRetryBackoff = 500 * time.Millisecond

// RetrySettings bounds the work one client request may do. Before this the
// router walked every candidate credential with no same-credential retry, so a
// blip on the upstream immediately burned a credential and a broad provider
// outage could take a very long time to surface as an error.
type RetrySettings struct {
	// Attempts is the number of extra tries against the same credential for a
	// transient failure. 0 means "no same-credential retry".
	Attempts int
	// MaxCredentials caps how many distinct credentials one request may try.
	// 0 means unlimited, the historical behaviour.
	MaxCredentials int
	// MaxWait is how long to wait for a credential whose cooldown expires soon
	// rather than skipping it. 0 disables waiting.
	MaxWait time.Duration
	// Backoff is the pause between same-credential attempts.
	Backoff time.Duration
}

func RetrySettingsFromConfig(cfg config.RetryConfig) RetrySettings {
	settings := RetrySettings{
		Attempts:       cfg.Attempts,
		MaxCredentials: cfg.MaxCredentials,
		Backoff:        durationOr(cfg.Backoff, defaultRetryBackoff),
		MaxWait:        durationOrZero(cfg.MaxWait),
	}
	if settings.Attempts < 0 {
		settings.Attempts = 0
	}
	if settings.MaxCredentials < 0 {
		settings.MaxCredentials = 0
	}
	return settings
}

func durationOrZero(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

// retryableStatus reports whether re-sending the same request to the same
// credential is worth trying. Auth and quota failures are excluded: they need a
// different credential, not another attempt.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusForbidden,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// allowCredentialAttempt reports whether the request may try another credential.
func (s RetrySettings) allowCredentialAttempt(used int) bool {
	if s.MaxCredentials <= 0 {
		return true
	}
	return used < s.MaxCredentials
}

// waitForCooldown sleeps until a credential's cooldown expires when that is
// within the configured budget, so a brief 429 does not force a fallback to a
// worse credential. It reports whether the caller should retry the credential.
func (s RetrySettings) waitForCooldown(ctx context.Context, until time.Time, now time.Time) bool {
	if s.MaxWait <= 0 || until.IsZero() {
		return false
	}
	remaining := until.Sub(now)
	if remaining <= 0 {
		return true
	}
	if remaining > s.MaxWait {
		return false
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// sleepBackoff pauses between same-credential attempts, aborting on cancel.
func (s RetrySettings) sleepBackoff(ctx context.Context) bool {
	if s.Backoff <= 0 {
		return true
	}
	timer := time.NewTimer(s.Backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// shouldRetrySameCredential decides whether the error justifies another attempt
// against the credential that just failed.
func (s RetrySettings) shouldRetrySameCredential(attempt int, err error) bool {
	if s.Attempts <= 0 || attempt >= s.Attempts {
		return false
	}
	return retryableStatus(providers.Status(err))
}
