package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

type limitError struct {
	Code    string
	Message string
}

func (e *limitError) Error() string { return e.Message }

type requestWindow struct {
	Started time.Time
	Count   int
}

type limitScope struct {
	ID     string
	Limits config.LimitPolicy
}

type requestLimiter struct {
	mu      sync.Mutex
	windows map[string]requestWindow
	streams map[string]int
}

func newRequestLimiter() *requestLimiter {
	return &requestLimiter{windows: make(map[string]requestWindow), streams: make(map[string]int)}
}

func (l *requestLimiter) admitRequest(key *store.APIKey, path string, scopes ...limitScope) error {
	if key != nil && !endpointAllowed(key.Policy.Endpoints, path) {
		return &limitError{Code: "endpoint_forbidden", Message: fmt.Sprintf("endpoint %s is not allowed for this API key", path)}
	}
	if len(scopes) == 0 && key != nil {
		scopes = []limitScope{{ID: "key:" + key.ID, Limits: key.Policy.Limits}}
	}
	if len(scopes) == 0 {
		return nil
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, scope := range scopes {
		limit := scope.Limits.RequestsPerMinute
		if limit <= 0 {
			continue
		}
		window := l.windows[scope.ID]
		if window.Started.IsZero() || now.Sub(window.Started) >= time.Minute {
			window = requestWindow{Started: now}
		}
		if window.Count >= limit {
			return &limitError{Code: "rate_limit_exceeded", Message: fmt.Sprintf("requests per minute limit exceeded for %s", scope.ID)}
		}
	}
	for _, scope := range scopes {
		if scope.Limits.RequestsPerMinute <= 0 {
			continue
		}
		window := l.windows[scope.ID]
		if window.Started.IsZero() || now.Sub(window.Started) >= time.Minute {
			window = requestWindow{Started: now}
		}
		window.Count++
		l.windows[scope.ID] = window
	}
	return nil
}

func (l *requestLimiter) acquireStream(key *store.APIKey, scopes ...limitScope) error {
	if len(scopes) == 0 && key != nil {
		scopes = []limitScope{{ID: "key:" + key.ID, Limits: key.Policy.Limits}}
	}
	if len(scopes) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, scope := range scopes {
		if scope.Limits.ConcurrentStreams > 0 && l.streams[scope.ID] >= scope.Limits.ConcurrentStreams {
			return &limitError{Code: "concurrency_limit_exceeded", Message: fmt.Sprintf("concurrent stream limit exceeded for %s", scope.ID)}
		}
	}
	for _, scope := range scopes {
		if scope.Limits.ConcurrentStreams > 0 {
			l.streams[scope.ID]++
		}
	}
	return nil
}

func (l *requestLimiter) releaseStream(key *store.APIKey, scopes ...limitScope) {
	if len(scopes) == 0 && key != nil {
		scopes = []limitScope{{ID: "key:" + key.ID, Limits: key.Policy.Limits}}
	}
	if len(scopes) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, scope := range scopes {
		if scope.Limits.ConcurrentStreams <= 0 {
			continue
		}
		if l.streams[scope.ID] > 1 {
			l.streams[scope.ID]--
		} else {
			delete(l.streams, scope.ID)
		}
	}
}

func endpointAllowed(endpoints []string, path string) bool {
	if len(endpoints) == 0 {
		return true
	}
	for _, pattern := range endpoints {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || pattern == "*" || pattern == path {
			return true
		}
		if strings.HasSuffix(pattern, "/*") && strings.HasPrefix(path, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func policyInputLimit(policy config.ClientKeyPolicy) int64 { return policy.Limits.MaxInputBytes }

func isBodyRequest(r *http.Request) bool {
	return r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch
}
