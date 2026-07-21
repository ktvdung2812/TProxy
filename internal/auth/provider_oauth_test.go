package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPollCodebuddyDevicePendingAndSuccess(t *testing.T) {
	state := "test-state-123"
	pollCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v2/plugin/auth/state" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"state": state, "authUrl": "https://example.com/auth"},
			})
			return
		}
		pollCount++
		if pollCount == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 11217})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"accessToken":  "cbcn-access",
				"refreshToken": "cbcn-refresh",
				"expiresIn":    3600,
			},
		})
	}))
	defer server.Close()

	origStateURL := codebuddyStateURL
	origTokenURL := codebuddyTokenURL
	codebuddyStateURL = server.URL + "/v2/plugin/auth/state"
	codebuddyTokenURL = server.URL + "/v2/plugin/auth/token"
	t.Cleanup(func() {
		codebuddyStateURL = origStateURL
		codebuddyTokenURL = origTokenURL
	})

	start, err := initiateCodebuddyDeviceFlow()
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if start.State != state {
		t.Fatalf("state = %q", start.State)
	}

	manager := NewManager(nil, server.Client())
	token, pending, err := manager.pollCodebuddyDevice(context.Background(), start.State)
	if err == nil || !pending || Code(err) != "authorization_pending" {
		t.Fatalf("first poll: pending=%v err=%v", pending, err)
	}
	token, pending, err = manager.pollCodebuddyDevice(context.Background(), start.State)
	if err != nil || pending || token.AccessToken != "cbcn-access" {
		t.Fatalf("second poll: token=%q pending=%v err=%v", token.AccessToken, pending, err)
	}
}

func TestPollKilocodeDevice(t *testing.T) {
	code := "device-code-abc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/device-auth/codes":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":            code,
				"verificationUrl": "https://kilo.ai/verify",
				"expiresIn":       300,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/device-auth/codes/"+code:
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	origInitiate := kilocodeInitiateURL
	origPollBase := kilocodePollURLBase
	kilocodeInitiateURL = server.URL + "/api/device-auth/codes"
	kilocodePollURLBase = server.URL + "/api/device-auth/codes"
	t.Cleanup(func() {
		kilocodeInitiateURL = origInitiate
		kilocodePollURLBase = origPollBase
	})

	start, err := initiateKilocodeDeviceFlow()
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	manager := NewManager(nil, server.Client())
	_, pending, err := manager.pollKilocodeDevice(context.Background(), start.Code)
	if err == nil || !pending {
		t.Fatalf("expected pending, got pending=%v err=%v", pending, err)
	}
}
