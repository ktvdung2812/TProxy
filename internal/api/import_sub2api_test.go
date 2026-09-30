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

func TestAdminImportSub2api(t *testing.T) {
	cfg := &config.Config{Security: config.SecurityConfig{ManagementSecretEnv: "TPROXY_TEST_MANAGEMENT"}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	const payload = `{"exported_at":"2026-09-22T17:42:29Z","proxies":[],"accounts":[{"platform":"openai","type":"oauth","credentials":{"email":"member@example.com","access_token":"synthetic-token","chatgpt_account_id":"workspace"}}]}`
	for _, test := range []struct {
		name, query, body string
		status, count     int
	}{
		{"preview", "?dry_run=true", payload, http.StatusOK, 0},
		{"invalid", "", `{"accounts":[]}`, http.StatusBadRequest, 0},
		{"import", "", payload, http.StatusOK, 1},
		{"reimport", "", payload, http.StatusOK, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/admin/import/sub2api"+test.query, strings.NewReader(test.body))
			request.RemoteAddr = "127.0.0.1:1234"
			withDefaultManagementAuth(request)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.status == http.StatusOK {
				var result importsub2api.Result
				if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || !result.OK || result.Counts.Credentials != 1 || result.DryRun != (test.query != "") {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			}
			credentials, err := dataStore.Credentials(context.Background(), "codex")
			if err != nil || len(credentials) != test.count {
				t.Fatalf("stored count=%d err=%v", len(credentials), err)
			}
		})
	}
}
