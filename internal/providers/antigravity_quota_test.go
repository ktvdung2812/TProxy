package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestAntigravityQuotaEntryUsesFractionalRemainingPercentage(t *testing.T) {
	tests := []struct {
		name     string
		fraction any
		wantUsed float64
		wantLeft float64
	}{
		{name: "json number", fraction: float64(0.5), wantUsed: 500, wantLeft: 50},
		{name: "string", fraction: "0.125", wantUsed: 875, wantLeft: 12.5},
		{name: "empty", fraction: float64(0), wantUsed: 1000, wantLeft: 0},
		{name: "full", fraction: "1", wantUsed: 0, wantLeft: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, ok := antigravityQuotaEntry("Gemini", tt.fraction, nil)
			if !ok {
				t.Fatalf("fraction %#v was rejected", tt.fraction)
			}
			if entry.Total != 1000 || entry.Used != tt.wantUsed || entry.Remaining != tt.wantLeft {
				t.Fatalf("entry = %+v, want total=1000 used=%v remaining=%v", entry, tt.wantUsed, tt.wantLeft)
			}
		})
	}
}

func TestAntigravityQuotaEntryRejectsInvalidFractions(t *testing.T) {
	for _, value := range []any{"not-a-number", -0.1, 1.1, nil} {
		if entry, ok := antigravityQuotaEntry("Gemini", value, nil); ok {
			t.Fatalf("invalid fraction %#v produced %+v", value, entry)
		}
	}
}

func TestGeminiCLIQuotaUsesStoredProjectAndStringFraction(t *testing.T) {
	var calls atomic.Int32
	registry := NewRegistry()
	registry.client = &http.Client{Transport: antigravityQuotaRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.String() != "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota" {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer cli-token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["project"] != "stored-project" {
			t.Fatalf("quota request body=%#v", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"buckets":[{"modelId":"gemini-3-flash-preview","remainingFraction":"0.125","resetTime":"2026-08-13T12:00:00Z"}]
			}`)),
		}, nil
	})}

	quota := registry.geminiCLIQuota(t.Context(), store.Credential{
		ID:         "gemini-cli-account",
		ProviderID: "gemini-cli",
		AuthType:   "oauth",
		OAuthToken: &store.OAuthToken{
			AccessToken: "cli-token",
			Extra:       map[string]any{"project_id": "stored-project"},
		},
	})
	entry, ok := quota.Quotas["gemini-3-flash-preview"]
	if !ok {
		t.Fatalf("quota=%+v", quota)
	}
	if calls.Load() != 1 || entry.Used != 875 || entry.Total != 1000 || entry.Remaining != 12.5 {
		t.Fatalf("calls=%d entry=%+v", calls.Load(), entry)
	}
}

func TestCredentialQuotaRoutesGeminiCLIThroughCredentialProxy(t *testing.T) {
	const proxyURL = "http://proxy.example:8080"
	registry := NewRegistry()
	registry.client = &http.Client{Transport: antigravityQuotaRoundTripper(func(request *http.Request) (*http.Response, error) {
		if got, _ := request.Context().Value(proxyContextKey{}).(string); got != proxyURL {
			t.Fatalf("credential proxy=%q, want %q", got, proxyURL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"buckets":[{"modelId":"gemini-3-flash-preview","remainingFraction":0.5}]}`)),
		}, nil
	})}

	quota, err := registry.CredentialQuota(t.Context(), store.Provider{
		ID:   "gemini-cli",
		Type: "gemini",
	}, store.Credential{
		ID:         "gemini-cli-account",
		ProviderID: "gemini-cli",
		Secret:     "cli-token",
		ProxyURL:   proxyURL,
		OAuthToken: &store.OAuthToken{
			AccessToken: "cli-token",
			Extra:       map[string]any{"project_id": "stored-project"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := quota.Quotas["gemini-3-flash-preview"]; !ok {
		t.Fatalf("quota=%+v", quota)
	}
}

type antigravityQuotaRoundTripper func(*http.Request) (*http.Response, error)

func (f antigravityQuotaRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
