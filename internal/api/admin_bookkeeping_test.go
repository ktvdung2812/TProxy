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
)

// The quota tracker refreshes every credential once a minute. Recording each of
// those polls as an operator action produced 341,745 audit events and 335,952
// config versions on a real install — ~112MB of a 186MB database.
func TestQuotaRefreshDoesNotGrowBookkeepingTables(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{
		ID: "openai-main", Type: "openai-compatible", Name: "OpenAI", Enabled: true, BaseURL: "http://127.0.0.1:1",
		Credentials: []config.CredentialConfig{{ID: "cred-a", AuthType: "api_key", Secret: "secret-value"}},
	}}}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()
	handler := server.Handler()
	ctx := context.Background()

	auditBefore, err := dataStore.RecentAuditEvents(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	versionsBefore, err := dataStore.RecentConfigVersions(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		request := httptest.NewRequest(http.MethodPost, "/api/admin/credentials/cred-a/quota", nil)
		request.RemoteAddr = "127.0.0.1:1234"
		withDefaultManagementAuth(request)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}

	auditAfter, err := dataStore.RecentAuditEvents(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	versionsAfter, err := dataStore.RecentConfigVersions(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(auditAfter) != len(auditBefore) {
		t.Fatalf("quota refresh wrote %d audit events", len(auditAfter)-len(auditBefore))
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("quota refresh wrote %d config versions", len(versionsAfter)-len(versionsBefore))
	}
}

// A real configuration change must still be recorded.
func TestConfigChangeIsStillAudited(t *testing.T) {
	cfg := &config.Config{}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()
	handler := server.Handler()
	ctx := context.Background()

	before, err := dataStore.RecentConfigVersions(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"id":"audited-model","display_name":"Audited","enabled":true,"routes":[]}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/models", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code >= http.StatusBadRequest {
		t.Fatalf("model create status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	audit, err := dataStore.RecentAuditEvents(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	var audited bool
	for _, event := range audit {
		if strings.Contains(event.Action, "/api/admin/models") {
			audited = true
			break
		}
	}
	if !audited {
		t.Fatal("model creation was not audited")
	}

	after, err := dataStore.RecentConfigVersions(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("config versions went from %d to %d, want exactly one new row", len(before), len(after))
	}
}
