package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
)

func TestDevinAuthorizationURLManualFlow(t *testing.T) {
	got := devinAuthorizationURL("challenge-abc", "state-xyz")
	if !strings.HasPrefix(got, "https://app.devin.ai/auth/cli/continue?") {
		t.Fatalf("url = %q", got)
	}
	want := "state=state-xyz&prompt=select_account&code_challenge=challenge-abc&code_challenge_method=S256&cli_pkce_marker=1"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("query = %q, want suffix %q", got[strings.IndexByte(got, '?')+1:], want)
	}
	if strings.Contains(got, "redirect_uri") {
		t.Fatalf("redirect_uri must never be sent: %q", got)
	}
}

func TestDevinSessionToken(t *testing.T) {
	if got := devinSessionToken("raw"); got != "devin-session-token$raw" {
		t.Fatalf("got %q", got)
	}
	if got := devinSessionToken("devin-session-token$raw"); got != "devin-session-token$raw" {
		t.Fatalf("got %q", got)
	}
	if got := devinSessionToken("  devin-session-token$raw  "); got != "devin-session-token$raw" {
		t.Fatalf("got %q", got)
	}
}

func TestDevinBrowserOAuthStartWithoutRedirect(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "devin", Type: "devin", Enabled: true, OAuth: &config.OAuthConfig{}}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, http.DefaultClient)
	defer manager.Close()

	started, err := manager.StartAuthorization(context.Background(), StartRequest{
		ProviderID:   "devin",
		CredentialID: "devin-account",
		Mode:         "browser",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(started.AuthorizationURL, "app.devin.ai/auth/cli/continue") {
		t.Fatalf("authorization url = %q", started.AuthorizationURL)
	}
	if !strings.Contains(started.AuthorizationURL, "cli_pkce_marker=1") {
		t.Fatalf("authorization url = %q", started.AuthorizationURL)
	}
}

func TestDevinCompleteCallbackSavesSessionToken(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "devin", Type: "devin", Enabled: true, OAuth: &config.OAuthConfig{}}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case strings.HasSuffix(request.URL.Path, "/auth/cli/token"):
			body = `{"token":"raw-session"}`
		case strings.HasSuffix(request.URL.Path, "/v3/self"):
			body = `{"user_name":"dev","user_id":"u1","org_id":"o1"}`
		default:
			// GetUserStatus: empty protobuf body decodes to an empty status.
			body = ""
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})})
	defer manager.Close()

	started, err := manager.StartAuthorization(context.Background(), StartRequest{
		ProviderID:   "devin",
		CredentialID: "devin-account",
		Mode:         "browser",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.CompleteCallback(context.Background(), "", "pasted-code", started.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "complete" {
		t.Fatalf("status = %+v", status)
	}
	credentials, err := dataStore.Credentials(context.Background(), "devin")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 {
		t.Fatalf("credentials = %+v", credentials)
	}
	if credentials[0].Secret != "devin-session-token$raw-session" {
		t.Fatalf("secret = %q", credentials[0].Secret)
	}
}
