package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"sync"
	"time"
)

// Verifying the dashboard password derives PBKDF2-SHA256 over 600,000
// iterations. That cost is deliberate — it is what makes an offline attack on
// the stored verifier expensive — but managementAuth runs it on *every*
// management request, and the dashboard polls continuously (the quota tracker
// alone issues one request per credential per minute). Measured on an M1 Pro a
// single verification takes 67.5ms, which both dominates admin request latency
// and hands an unauthenticated caller a cheap CPU-exhaustion lever: each request
// costs them nothing and costs the gateway 67.5ms of a core.
//
// The cache keeps the derivation but stops repeating it for a token that was
// already checked. Entries are keyed by an HMAC of the token under a
// process-random key, so the cache never holds the token itself and cannot be
// probed across restarts. Negative results are cached too — otherwise a wrong
// password would still pay full price on every retry, leaving the DoS lever in
// place.
const (
	dashboardPasswordCacheTTL     = 5 * time.Minute
	dashboardPasswordCacheMaxSize = 128
)

type dashboardPasswordDecision struct {
	matched   bool
	expiresAt time.Time
}

type dashboardPasswordCache struct {
	mu      sync.Mutex
	key     []byte
	entries map[[32]byte]dashboardPasswordDecision
	now     func() time.Time
}

func newDashboardPasswordCache() *dashboardPasswordCache {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		// Without a random key the cache cannot be keyed safely, so leave it
		// nil-keyed and fall back to verifying every request.
		key = nil
	}
	return &dashboardPasswordCache{key: key, entries: map[[32]byte]dashboardPasswordDecision{}}
}

func (c *dashboardPasswordCache) timeNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *dashboardPasswordCache) digest(token string) ([32]byte, bool) {
	if c == nil || len(c.key) == 0 {
		return [32]byte{}, false
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte(token))
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out, true
}

// lookup reports a cached decision for the token, if one is still valid.
func (c *dashboardPasswordCache) lookup(token string) (matched bool, ok bool) {
	digest, addressable := c.digest(token)
	if !addressable {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, present := c.entries[digest]
	if !present {
		return false, false
	}
	if !c.timeNow().Before(entry.expiresAt) {
		delete(c.entries, digest)
		return false, false
	}
	return entry.matched, true
}

func (c *dashboardPasswordCache) store(token string, matched bool) {
	digest, addressable := c.digest(token)
	if !addressable {
		return
	}
	now := c.timeNow()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= dashboardPasswordCacheMaxSize {
		for key, entry := range c.entries {
			if !now.Before(entry.expiresAt) {
				delete(c.entries, key)
			}
		}
		// Still full of live entries: drop everything rather than let an
		// attacker rotating tokens pin the map at its ceiling.
		if len(c.entries) >= dashboardPasswordCacheMaxSize {
			clear(c.entries)
		}
	}
	c.entries[digest] = dashboardPasswordDecision{matched: matched, expiresAt: now.Add(dashboardPasswordCacheTTL)}
}

// reset drops every cached decision. It must be called whenever the stored
// verifier changes, so a rotated password takes effect immediately.
func (c *dashboardPasswordCache) reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}

// verifyDashboardPassword answers from the cache when possible and otherwise
// falls back to the full derivation, recording the result.
func (s *Server) verifyDashboardPassword(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	if matched, ok := s.dashboardPasswordCache.lookup(token); ok {
		return matched
	}
	matched, err := s.store.VerifyDashboardPassword(ctx, token)
	if err != nil {
		// A store failure is not a verdict about the token; do not cache it.
		return false
	}
	s.dashboardPasswordCache.store(token, matched)
	return matched
}
