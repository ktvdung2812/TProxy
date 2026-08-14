package api

import "strings"

// The dashboard drives a number of admin endpoints on timers: the quota tracker
// refreshes every credential once a minute, topology and failover panels poll,
// and the OAuth modal polls while a flow is open. They use POST because they
// make the gateway do work, but none of them changes configuration.
//
// Treating every non-GET /api/admin/ request as an operator action made those
// timers dominate both bookkeeping tables — in a month of real use a single
// install accumulated 341,745 audit events and 335,952 config versions, ~112MB
// of a 186MB database, with the quota refresh of individual credentials as the
// top entries. That buries genuine operator actions and, because recording a
// config version runs a full ExportConfig, it also re-read and re-decrypted
// every credential on each poll.
//
// Patterns are matched per path segment; "*" matches exactly one segment.
var adminActionPaths = []string{
	// Polled by the quota tracker, once per credential per refresh.
	"/api/admin/credentials/*/quota",
	"/api/admin/credentials/*/models",
	// Provider probes issued by the dashboard and the model picker.
	"/api/admin/providers/*/health",
	"/api/admin/providers/*/models",
	"/api/admin/proxy-pools/*/test",
	"/api/admin/models/test",
	// Runtime resets: they clear in-memory breaker and rotation state only.
	"/api/admin/failover/reset",
	"/api/admin/rotation/reset",
	// OAuth progress polling. The flow-changing endpoints (start, callback,
	// import) are deliberately absent so they stay audited.
	"/api/admin/oauth/status",
	"/api/admin/oauth/session",
	// Read-only exports and streams that happen to be POST or long-lived.
	"/api/admin/config/export",
	"/api/admin/auth/export",
	"/api/admin/logs/stream",
}

// adminRequestIsAction reports whether an admin request performs a runtime
// action rather than an operator-visible configuration change. Such requests
// are excluded from the audit trail and from config version history.
func adminRequestIsAction(path string) bool {
	for _, pattern := range adminActionPaths {
		if adminPathMatches(pattern, path) {
			return true
		}
	}
	return false
}

func adminPathMatches(pattern, path string) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i, part := range patternParts {
		if part == "*" {
			// A wildcard stands for an identifier, which is never empty.
			if pathParts[i] == "" {
				return false
			}
			continue
		}
		if part != pathParts[i] {
			return false
		}
	}
	return true
}
