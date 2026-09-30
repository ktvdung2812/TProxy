package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

func TestTunnelAdminConfiguration(t *testing.T) {
	t.Setenv("TPROXY_SKIP_TUNNEL_AUTO", "1")
	cfg := &config.Config{}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()

	for _, body := range []string{
		`{`, `{}`, `{"hostname":"api.example.com"}`,
		`{"hostname":"http://api.example.com","token":"private-token"}`,
		`{"hostname":"api.example.com/v1","token":"private-token"}`,
		`{"hostname":"api.example.com","token":"cloudflared tunnel run --token private-token"}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/admin/tunnel/enable", strings.NewReader(body))
		w := httptest.NewRecorder()
		server.adminTunnel(w, r)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "private-token") {
			t.Fatalf("invalid config: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	settings := store.TunnelSettings{TunnelToken: "private-token", TunnelHostname: "api.example.com", TunnelURL: "https://api.example.com"}
	if err := dataStore.SaveTunnelSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.adminTunnel(w, httptest.NewRequest(http.MethodGet, "/api/admin/tunnel/status", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"hostname":"api.example.com"`) || !strings.Contains(w.Body.String(), `"tokenConfigured":true`) || strings.Contains(w.Body.String(), settings.TunnelToken) {
		t.Fatalf("status must report saved config without its token: %d %s", w.Code, w.Body.String())
	}
	// Updating dashboard access must retain the token and hostname.
	w = httptest.NewRecorder()
	server.adminTunnel(w, httptest.NewRequest(http.MethodPatch, "/api/admin/tunnel/dashboard-access", strings.NewReader(`{"tunnel_dashboard_access":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard update: %d %s", w.Code, w.Body.String())
	}
	loaded, err := dataStore.TunnelSettings(context.Background())
	if err != nil || loaded.TunnelToken != settings.TunnelToken || loaded.TunnelHostname != settings.TunnelHostname {
		t.Fatalf("dashboard update lost tunnel configuration: %v", err)
	}
}

func TestTunnelPolicySurvivesCorruptTokenAndFailsClosedOnReadError(t *testing.T) {
	t.Setenv("TPROXY_SKIP_TUNNEL_AUTO", "1")
	ctx := context.Background()
	cfg := &config.Config{}
	dataStore := apiTestStore(t, cfg)
	settings := store.TunnelSettings{
		Enabled:               true,
		TunnelHostname:        "api.example.com",
		TunnelDashboardAccess: false,
	}
	if err := dataStore.SaveTunnelSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	raw, err := dataStore.GetAppSettingJSON(ctx, store.AppSettingTunnel)
	if err != nil {
		t.Fatal(err)
	}
	raw["tunnel_token_ciphertext"] = "corrupt-ciphertext"
	if err := dataStore.SetAppSettingJSON(ctx, store.AppSettingTunnel, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.TunnelSettings(ctx); err == nil {
		t.Fatal("TunnelSettings should preserve token decryption errors")
	}

	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()

	dashboard := httptest.NewRequest(http.MethodGet, "https://api.example.com:443/dashboard/", nil)
	dashboard.RemoteAddr = "127.0.0.1:1234"
	dashboardRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(dashboardRecorder, dashboard)
	if dashboardRecorder.Code != http.StatusForbidden || !strings.Contains(dashboardRecorder.Body.String(), "tunnel_dashboard_disabled") {
		t.Fatalf("corrupt-token tunnel dashboard status=%d body=%s", dashboardRecorder.Code, dashboardRecorder.Body.String())
	}

	admin := httptest.NewRequest(http.MethodGet, "https://api.example.com:443/api/admin/snapshot", nil)
	admin.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(admin)
	adminRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(adminRecorder, admin)
	if adminRecorder.Code != http.StatusForbidden || !strings.Contains(adminRecorder.Body.String(), "management_remote_disabled") {
		t.Fatalf("corrupt-token tunnel admin status=%d body=%s", adminRecorder.Code, adminRecorder.Body.String())
	}

	canceledContext, cancel := context.WithCancel(ctx)
	cancel()
	requestWithReadError := httptest.NewRequest(http.MethodGet, "http://unlisted.example.com/api/admin/snapshot", nil)
	requestWithReadError = requestWithReadError.WithContext(canceledContext)
	requestWithReadError.RemoteAddr = "127.0.0.1:1234"
	if server.managementClientAllowed(requestWithReadError) {
		t.Fatal("management access should fail closed when tunnel policy cannot be read")
	}
}
