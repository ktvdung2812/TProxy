package api

import "testing"

func TestAdminRequestIsAction(t *testing.T) {
	actions := []string{
		"/api/admin/credentials/c8b83f64-6585-482b-9fff-542d6ebfdd3a/quota",
		"/api/admin/credentials/account-a/models",
		"/api/admin/providers/openai-main/health",
		"/api/admin/providers/openai-main/models",
		"/api/admin/proxy-pools/pool-1/test",
		"/api/admin/models/test",
		"/api/admin/failover/reset",
		"/api/admin/rotation/reset",
		"/api/admin/oauth/status",
		"/api/admin/config/export",
	}
	for _, path := range actions {
		if !adminRequestIsAction(path) {
			t.Errorf("%s should be treated as a runtime action", path)
		}
	}

	// Real configuration changes must stay audited. In particular the model and
	// provider collection endpoints share a prefix with the probe endpoints
	// above, so a suffix-only rule would wrongly silence them.
	mutations := []string{
		"/api/admin/models",
		"/api/admin/providers",
		"/api/admin/credentials",
		"/api/admin/proxy-pools",
		"/api/admin/api-keys",
		"/api/admin/combos",
		"/api/admin/settings",
		"/api/admin/config/import",
		"/api/admin/oauth/start",
		"/api/admin/oauth/kiro/import",
		"/api/admin/settings/dashboard-password",
	}
	for _, path := range mutations {
		if adminRequestIsAction(path) {
			t.Errorf("%s is a configuration change and must stay audited", path)
		}
	}
}

func TestAdminPathMatchesRequiresSameSegmentCount(t *testing.T) {
	if adminPathMatches("/api/admin/models/test", "/api/admin/models") {
		t.Error("shorter path matched a longer pattern")
	}
	if adminPathMatches("/api/admin/models/test", "/api/admin/models/test/extra") {
		t.Error("longer path matched a shorter pattern")
	}
	if adminPathMatches("/api/admin/credentials/*/quota", "/api/admin/credentials//quota") {
		t.Error("wildcard matched an empty segment")
	}
}
