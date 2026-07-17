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
	Fallback       time.Duration
	Max            time.Duration
	BackoffBase    time.Duration
	PermanentAuth  time.Duration
	Status401      time.Duration
	Status402      time.Duration
	Status403      time.Duration
	Status404      time.Duration
	Status429      time.Duration
	Status5xx      time.Duration
}

func CooldownSettingsFromConfig(cfg config.CooldownConfig) CooldownSettings {
	settings := CooldownSettings{
		Fallback:      durationOr(cfg.Default, time.Minute),
		Max:           durationOr(cfg.Max, defaultMaxCooldown),
		BackoffBase:   durationOr(cfg.BackoffBase, time.Minute),
		PermanentAuth: durationOr(cfg.PermanentAuth, time.Minute),
		Status401:     durationOr(cfg.Status401, 5*time.Minute),
		Status402:     durationOr(cfg.Status402, 5*time.Minute),
		Status403:     durationOr(cfg.Status403, 5*time.Minute),
		Status404:     durationOr(cfg.Status404, 2*time.Minute),
		Status429:     durationOr(cfg.Status429, time.Minute),
		Status5xx:     durationOr(cfg.Status5xx, 15*time.Second),
	}
	return settings
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
	case status >= 500:
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
	return settings.ComputeUntil(now, providers.Status(err), providers.RetryAfter(err), backoffCount)
}
