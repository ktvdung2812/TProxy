package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

func chatGPTExport(t *testing.T) string {
	t.Helper()
	claims := `{"email":"member@example.com","https://api.openai.com/auth":{"chatgpt_account_id":"workspace","chatgpt_user_id":"member"}}`
	token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".synthetic-signature"
	data, err := json.Marshal(map[string]any{
		"accessToken": "synthetic-access", "refreshToken": "synthetic-refresh", "idToken": token,
		"expiresAt": "2026-11-01T04:16:57.134Z", "expiresIn": 864000,
		"providerSpecificData": map[string]any{"chatgptAccountId": "workspace", "chatgptPlanType": "plus"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func autoImportRequest(handler http.Handler, body, query string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/import"+query, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(request)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAdminAutoImportFormats(t *testing.T) {
	raw := chatGPTExport(t)
	for _, test := range []struct{ name, body, source string }{
		{"9router", `{"providerConnections":[{"id":"imported","provider":"codex","authType":"oauth","isActive":true,"accessToken":"synthetic-access"}]}`, "9router"},
		{"cliproxy", `{"type":"codex","access_token":"synthetic-access","refresh_token":"synthetic-refresh","email":"member@example.com"}`, "cliproxy"},
		{"cliproxy array", `[{"type":"codex","access_token":"synthetic-access"}]`, "cliproxy"},
		{"sub2api wrapped", `{"data":{"accounts":[{"platform":"openai","type":"oauth","credentials":{"access_token":"synthetic-access","email":"member@example.com"}}]}}`, "sub2api"},
		{"sub2api array", `[{"platform":"openai","type":"oauth","credentials":{"access_token":"synthetic-access","email":"member@example.com"}}]`, "sub2api"},
		{"chatgpt", raw, "chatgpt"},
		{"chatgpt array", "[" + raw + "]", "chatgpt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{}
			dataStore := apiTestStore(t, cfg)
			handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
			for _, dry := range []bool{true, false} {
				query := ""
				if dry {
					query = "?dry_run=true"
				}
				response := autoImportRequest(handler, test.body, query)
				if response.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				var result struct {
					Source string `json:"source"`
					Total  int    `json:"total_accounts"`
					Result struct {
						OK     bool           `json:"ok"`
						Dry    bool           `json:"dry_run"`
						Counts map[string]int `json:"counts"`
					} `json:"result"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Source != test.source || result.Total != 1 || !result.Result.OK || result.Result.Dry != dry || result.Result.Counts["credentials"] != 1 {
					t.Fatalf("unexpected result: %+v", result)
				}
				if strings.Contains(response.Body.String(), "synthetic-access") || strings.Contains(response.Body.String(), "synthetic-refresh") {
					t.Fatal("tokens exposed in import result")
				}
				credentials, err := dataStore.Credentials(context.Background(), "codex")
				if err != nil {
					t.Fatal(err)
				}
				expected := 1
				if dry {
					expected = 0
				}
				if len(credentials) != expected {
					t.Fatalf("stored credentials=%d want=%d", len(credentials), expected)
				}
				if dry {
					providers, err := dataStore.Providers(context.Background())
					if err != nil || len(providers) != 0 {
						t.Fatalf("preview wrote providers: count=%d err=%v", len(providers), err)
					}
				} else if test.source == "chatgpt" {
					credential := credentials[0]
					token := credential.OAuthToken
					expiry, _ := time.Parse(time.RFC3339Nano, "2026-11-01T04:16:57.134Z")
					if token == nil || token.AccessToken != "synthetic-access" || token.RefreshToken != "synthetic-refresh" || !token.ExpiresAt.Equal(expiry) || token.Extra["account_id"] != "workspace" || token.Extra["chatgpt_plan_type"] != "plus" || credential.Email != "member@example.com" || token.Extra["id_token"] == "" {
						t.Fatal("converted credential fields were not preserved")
					}
					duplicate := autoImportRequest(handler, test.body, "")
					if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), `"skipped":1`) {
						t.Fatalf("duplicate import: %s", duplicate.Body.String())
					}
				}
			}
		})
	}
}

func TestAdminAutoImportRejectsUnknownAndInvalidFiles(t *testing.T) {
	raw := chatGPTExport(t)
	cfg := &config.Config{}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	for _, body := range []string{
		`{`, `null`, `[]`, `{}`, `{"access_token":"synthetic-access"}`,
		`{"accessToken":"synthetic-access","expiresAt":"2026-11-01T00:00:00Z"}`,
		strings.Replace(raw, `"expiresAt":"2026-11-01T04:16:57.134Z"`, `"expiresAt":"bad"`, 1),
		strings.Replace(raw, `"expiresAt":"2026-11-01T04:16:57.134Z",`, "", 1),
		strings.Replace(raw, `"chatgptAccountId":"workspace"`, `"chatgptAccountId":"mismatch"`, 1),
		"[" + raw + `,{"type":"codex","access_token":"synthetic-access"}]`,
	} {
		response := autoImportRequest(handler, body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid input accepted: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	providers, err := dataStore.Providers(context.Background())
	if err != nil || len(providers) != 0 {
		t.Fatalf("invalid files changed store: count=%d err=%v", len(providers), err)
	}
}

func TestAdminAutoImportAuthBundlePreviewRollsBack(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "codex", Type: "codex", Enabled: true}}}
	dataStore := apiTestStore(t, cfg)
	save := func(access string) {
		t.Helper()
		if err := dataStore.SaveOAuthCredential(ctx, "codex", "member", "Member", "member@example.com", store.OAuthToken{AccessToken: access}); err != nil {
			t.Fatal(err)
		}
	}
	save("bundle-token")
	bundle, err := dataStore.ExportAuthBundle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	save("current-token")
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	for _, dry := range []bool{true, false} {
		query := ""
		expected := "bundle-token"
		if dry {
			query = "?dry_run=true"
			expected = "current-token"
		}
		response := autoImportRequest(handler, string(bundle), query)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"source":"auth-bundle"`) {
			t.Fatalf("bundle status=%d body=%s", response.Code, response.Body.String())
		}
		credential, err := dataStore.CredentialByID(ctx, "member")
		if err != nil {
			t.Fatal(err)
		}
		if credential.OAuthToken == nil || credential.OAuthToken.AccessToken != expected {
			t.Fatal("bundle preview/import did not preserve transaction semantics")
		}
	}
	otherStore := apiTestStore(t, &config.Config{})
	otherHandler := NewServer(&config.Config{}, otherStore, router.New(otherStore, providers.NewRegistry())).Handler()
	response := autoImportRequest(otherHandler, string(bundle), "?dry_run=true")
	if response.Code != http.StatusBadRequest {
		t.Fatal("preview accepted bundle encrypted with another master key")
	}
	providers, err := otherStore.Providers(ctx)
	if err != nil || len(providers) != 0 {
		t.Fatal("failed bundle preview did not roll back provider creation")
	}
}

func TestAdminAutoImportAccountLimit(t *testing.T) {
	raw := chatGPTExport(t)
	second := strings.ReplaceAll(raw, "synthetic-access", "second-access")
	// An opaque token may omit identity claims; distinguish the second account by workspace ID.
	var account map[string]any
	if err := json.Unmarshal([]byte(second), &account); err != nil {
		t.Fatal(err)
	}
	account["idToken"] = ""
	account["providerSpecificData"] = map[string]any{"chatgptAccountId": "second-workspace"}
	encoded, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	response := autoImportRequest(handler, "["+raw+","+string(encoded)+"]", "?max_accounts=1")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total_accounts":2`) {
		t.Fatalf("limit response: %s", response.Body.String())
	}
	credentials, err := dataStore.Credentials(context.Background(), "codex")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("limit not honored: count=%d err=%v", len(credentials), err)
	}
}
