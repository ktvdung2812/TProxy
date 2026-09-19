package providers

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

type clineQuotaRoundTripper func(*http.Request) (*http.Response, error)

func (f clineQuotaRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func clineJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// Live shape: users/me resolves the account id, then users/{id}/balance
// returns prepaid credit cents — negative when usage outran purchases.
func TestClineQuotaReadsBalance(t *testing.T) {
	var calls atomic.Int32
	registry := NewRegistry()
	registry.client = &http.Client{Transport: clineQuotaRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Header.Get("Authorization") != "Bearer workos:tok" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/api/v1/users/me":
			return clineJSONResponse(`{"data":{"id":"usr-1","displayName":"D","organizations":[]},"success":true}`), nil
		case "/api/v1/users/usr-1/balance":
			return clineJSONResponse(`{"data":{"userId":"usr-1","balance":1234},"success":true}`), nil
		default:
			t.Fatalf("unexpected request %s", request.URL)
			return nil, nil
		}
	})}

	quota := registry.clineQuota(t.Context(), store.Provider{ID: "cline", Type: "cline"}, store.Credential{
		ID:         "cred-1",
		ProviderID: "cline",
		Secret:     "workos:tok",
		AuthType:   "oauth",
	})
	if quota.Plan != "Personal" {
		t.Fatalf("plan=%q", quota.Plan)
	}
	entry, ok := quota.Quotas["credits"]
	if !ok || entry.Used != 0 || entry.Total != 12.34 || entry.Remaining != 100 {
		t.Fatalf("credits=%+v quotas=%+v", entry, quota.Quotas)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

// A negative balance is the exhausted state: it must parse as a depleted
// window so QuotaAtZero pauses the credential.
func TestClineQuotaNegativeBalanceDepletes(t *testing.T) {
	registry := NewRegistry()
	registry.client = &http.Client{Transport: clineQuotaRoundTripper(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/users/me":
			return clineJSONResponse(`{"data":{"id":"usr-1","organizations":[]},"success":true}`), nil
		case "/api/v1/users/usr-1/balance":
			return clineJSONResponse(`{"data":{"userId":"usr-1","balance":-20031},"success":true}`), nil
		default:
			t.Fatalf("unexpected request %s", request.URL)
			return nil, nil
		}
	})}

	quota := registry.clineQuota(t.Context(), store.Provider{ID: "cline", Type: "cline"}, store.Credential{
		ID:       "cred-1",
		Secret:   "workos:tok",
		AuthType: "oauth",
	})
	entry := quota.Quotas["credits"]
	if entry.Remaining != 0 {
		t.Fatalf("credits=%+v", entry)
	}
	if !QuotaAtZero(quota) {
		t.Fatal("negative credit balance should deplete the credential")
	}
}

// Org memberships add per-org balance windows; any positive balance keeps the
// credential routable even when the personal balance is empty.
func TestClineQuotaOrgBalances(t *testing.T) {
	registry := NewRegistry()
	registry.client = &http.Client{Transport: clineQuotaRoundTripper(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/users/me":
			return clineJSONResponse(`{"data":{"id":"usr-1","organizations":[{"organizationId":"org-9","name":"Acme Team"}]},"success":true}`), nil
		case "/api/v1/users/usr-1/balance":
			return clineJSONResponse(`{"data":{"balance":0},"success":true}`), nil
		case "/api/v1/organizations/org-9/balance":
			return clineJSONResponse(`{"data":{"organizationId":"org-9","balance":5000},"success":true}`), nil
		default:
			t.Fatalf("unexpected request %s", request.URL)
			return nil, nil
		}
	})}

	quota := registry.clineQuota(t.Context(), store.Provider{ID: "cline", Type: "cline"}, store.Credential{
		ID:       "cred-1",
		Secret:   "workos:tok",
		AuthType: "oauth",
	})
	if quota.Plan != "Acme Team" {
		t.Fatalf("plan=%q", quota.Plan)
	}
	if quota.Quotas["credits"].Remaining != 0 {
		t.Fatalf("personal=%+v", quota.Quotas["credits"])
	}
	org := quota.Quotas["org_org-9"]
	if org.Remaining != 100 || org.Total != 50 {
		t.Fatalf("org=%+v", org)
	}
	if QuotaAtZero(quota) {
		t.Fatal("a funded org balance should keep the credential routable")
	}
}

// OAuth auth failure on users/me is reported as an auth problem, not as
// exhausted quota.
func TestClineQuotaAuthFailure(t *testing.T) {
	registry := NewRegistry()
	registry.client = &http.Client{Transport: clineQuotaRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"Unauthorized","success":false}`)),
		}, nil
	})}

	quota := registry.clineQuota(t.Context(), store.Provider{ID: "cline", Type: "cline"}, store.Credential{
		ID:       "cred-1",
		Secret:   "workos:tok",
		AuthType: "oauth",
	})
	if !strings.Contains(quota.Message, "authentication") {
		t.Fatalf("message=%q", quota.Message)
	}
	if len(quota.Quotas) != 0 || QuotaAtZero(quota) {
		t.Fatalf("auth failure must not report depleted quota: %+v", quota)
	}
}
