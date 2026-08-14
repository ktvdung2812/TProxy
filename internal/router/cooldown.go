package router

import (
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
)

const defaultMaxCooldown = 30 * time.Minute

type CooldownSettings struct {
	Fallback      time.Duration
	Max           time.Duration
	BackoffBase   time.Duration
	PermanentAuth time.Duration
	Status401     time.Duration
	Status402     time.Duration
	Status403     time.Duration
	Status404     time.Duration
	Status429     time.Duration
	Status5xx     time.Duration
	// Disabled suppresses every cooldown. Auth and quota backoffs go with it, so
	// this is a deliberate "never bench an account" switch.
	Disabled bool
	// TransientDisabled drops the backoff for retryable upstream failures only,
	// keeping auth (401/402/403) and quota (429) cooldowns intact.
	TransientDisabled bool
}

func CooldownSettingsFromConfig(cfg config.CooldownConfig) CooldownSettings {
	transient, transientDisabled := transientCooldown(cfg.Transient, 15*time.Second)
	settings := CooldownSettings{
		Fallback:          durationOr(cfg.Default, time.Minute),
		Max:               durationOr(cfg.Max, defaultMaxCooldown),
		BackoffBase:       durationOr(cfg.BackoffBase, time.Minute),
		PermanentAuth:     durationOr(cfg.PermanentAuth, time.Minute),
		Status401:         durationOr(cfg.Status401, 5*time.Minute),
		Status402:         durationOr(cfg.Status402, 5*time.Minute),
		Status403:         durationOr(cfg.Status403, 5*time.Minute),
		Status404:         durationOr(cfg.Status404, 2*time.Minute),
		Status429:         durationOr(cfg.Status429, time.Minute),
		Status5xx:         durationOr(cfg.Status5xx, transient),
		Disabled:          cfg.Disabled,
		TransientDisabled: transientDisabled,
	}
	// An explicit status-5xx entry still wins over the transient shorthand.
	if strings.TrimSpace(cfg.Status5xx) != "" {
		settings.TransientDisabled = false
		settings.Status5xx = durationOr(cfg.Status5xx, transient)
	}
	return settings
}

// transientCooldown reads the transient shorthand: "off"/"none"/"0"/a negative
// duration disables the backoff, anything else parses as a duration.
func transientCooldown(raw string, fallback time.Duration) (time.Duration, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return fallback, false
	}
	switch trimmed {
	case "off", "none", "disabled", "false", "0":
		return fallback, true
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return fallback, false
	}
	if parsed <= 0 {
		return fallback, true
	}
	return parsed, false
}

// SkipCooldown reports whether a failure with this status should be recorded at
// all.
func (s CooldownSettings) SkipCooldown(status int) bool {
	if s.Disabled {
		return true
	}
	return s.TransientDisabled && isTransientStatus(status)
}

func isTransientStatus(status int) bool {
	return status == http.StatusRequestTimeout || status >= 500
}

func durationOr(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func (s CooldownSettings) accountLevel(status int) bool {
	return status == 401 || status == 402 || status == 403
}

func (s CooldownSettings) ComputeUntil(now time.Time, status int, retryAfter string, backoffCount int) time.Time {
	return s.computeUntil(now, status, retryAfter, "", backoffCount)
}

// ComputeUntilWithReason additionally honours google.rpc.ErrorInfo's reason.
// Google returns 429 both for a rate limit that clears in under a second and
// for a quota that is gone for the day, and a quota-exhausted response can
// still carry a short retryDelay. Obeying that delay sends the request straight
// back to an account that has nothing left, so the reason has to override it.
func (s CooldownSettings) ComputeUntilWithReason(now time.Time, status int, retryAfter, reason string, backoffCount int) time.Time {
	return s.computeUntil(now, status, retryAfter, reason, backoffCount)
}

func (s CooldownSettings) computeUntil(now time.Time, status int, retryAfter, reason string, backoffCount int) time.Time {
	if status == 429 && quotaExhaustedReason(reason) {
		// Ignore retryAfter entirely here: it describes when the rate limiter
		// would next admit a request, not when the quota returns.
		return applyJitter(s.quotaExhaustedBackoff(backoffCount), s.Max, now)
	}
	if until, ok := parseRetryAfter(now, retryAfter, s.Max); ok {
		return applyJitter(until.Sub(now), s.Max, now)
	}
	if status == 429 {
		base := float64(s.BackoffBase) * math.Pow(2, float64(backoffCount))
		if base > float64(s.Max) {
			base = float64(s.Max)
		}
		return applyJitter(time.Duration(base), s.Max, now)
	}
	duration := s.fallbackForStatus(status)
	if duration <= 0 {
		duration = s.Fallback
	}
	return applyJitter(duration, s.Max, now)
}

// quotaExhaustedReason recognises the ErrorInfo reason that means the account
// has no quota left, as opposed to being briefly throttled.
//
// Only QUOTA_EXHAUSTED qualifies. RESOURCE_EXHAUSTED is the gRPC status Google
// puts on *both* conditions — a rate limit that clears in under a second and a
// quota that is gone for the day arrive with the same status and the same
// "Resource has been exhausted (e.g. check quota)." text. Treating that status
// as exhaustion benches a healthy credential for minutes over a momentary
// throttle, so the distinction has to come from the ErrorInfo reason.
func quotaExhaustedReason(reason string) bool {
	return strings.EqualFold(strings.TrimSpace(reason), "QUOTA_EXHAUSTED")
}

// quotaExhaustedBackoff benches the credential for the configured 429 window
// and grows it while the account keeps coming back exhausted, so a drained
// account is not retried once per request for the rest of the day.
func (s CooldownSettings) quotaExhaustedBackoff(backoffCount int) time.Duration {
	base := s.Status429
	if base <= 0 {
		base = s.Fallback
	}
	if base <= 0 {
		base = time.Minute
	}
	scaled := float64(base) * math.Pow(2, float64(backoffCount))
	if scaled > float64(s.Max) {
		return s.Max
	}
	return time.Duration(scaled)
}

func (s CooldownSettings) fallbackForStatus(status int) time.Duration {
	switch {
	case status == 401:
		return s.Status401
	case status == 402:
		return s.Status402
	case status == 403:
		return s.Status403
	case status == 404:
		return s.Status404
	case status == 429:
		return s.Status429
	case isTransientStatus(status):
		return s.Status5xx
	default:
		return s.Fallback
	}
}

func applyJitter(duration time.Duration, max time.Duration, now time.Time) time.Time {
	if duration <= 0 {
		duration = time.Second
	}
	jittered := time.Duration(float64(duration) * (0.8 + rand.New(rand.NewSource(now.UnixNano())).Float64()*0.4))
	if jittered > max {
		jittered = max
	}
	return now.Add(jittered)
}

func parseRetryAfter(now time.Time, raw string, max time.Duration) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds > 0 {
		d := time.Duration(seconds * float64(time.Second))
		if d > max {
			d = max
		}
		return now.Add(d), true
	}
	// google.rpc.RetryInfo is encoded as a protobuf duration (for example
	// "0.479417207s") rather than a Retry-After delta-seconds value.
	if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
		if parsed > max {
			parsed = max
		}
		return now.Add(parsed), true
	}
	if parsed, err := http.ParseTime(raw); err == nil && parsed.After(now) {
		d := parsed.Sub(now)
		if d > max {
			d = max
		}
		return now.Add(d), true
	}
	return time.Time{}, false
}

func credentialCooldownUntil(now time.Time, settings CooldownSettings, err error, backoffCount int) time.Time {
	return settings.ComputeUntilWithReason(now, providers.Status(err), providers.RetryAfter(err), providers.Reason(err), backoffCount)
}
