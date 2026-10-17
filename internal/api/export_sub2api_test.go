package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/importsub2api"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
)

func TestAdminExportSub2api(t *testing.T) {
	cfg := &config.Config{Security: config.SecurityConfig{ManagementSecretEnv: "TPROXY_TEST_MANAGEMENT"}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	ctx := context.Background()

	importRequest := httptest.NewRequest(http.MethodPost, "/api/admin/import/sub2api", strings.NewReader(`{"accounts":[{"platform":"openai","type":"oauth","credentials":{"email":"member@example.com","access_token":"synthetic-token","chatgpt_account_id":"workspace"}}]}`))
	importRequest.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(importRequest)
	importRequest.Header.Set("Content-Type", "application/json")
	importRecorder := httptest.NewRecorder()
	handler.ServeHTTP(importRecorder, importRequest)
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", importRecorder.Code, importRecorder.Body.String())
	}

	credentials, err := dataStore.Credentials(ctx, "codex")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}

	exportBody := `{"credential_ids":["` + credentials[0].ID + `"]}`
	exportRequest := httptest.NewRequest(http.MethodPost, "/api/admin/export/sub2api", strings.NewReader(exportBody))
	exportRequest.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(exportRequest)
	exportRequest.Header.Set("Content-Type", "application/json")
	exportRecorder := httptest.NewRecorder()
	handler.ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}
	if !strings.Contains(exportRecorder.Header().Get("Content-Disposition"), "sub2api-accounts-") {
		t.Fatalf("content disposition=%q", exportRecorder.Header().Get("Content-Disposition"))
	}
	var bundle importsub2api.Export
	if err = json.Unmarshal(exportRecorder.Body.Bytes(), &bundle); err != nil || len(bundle.Accounts) != 1 {
		t.Fatalf("bundle=%+v err=%v", bundle, err)
	}
}
