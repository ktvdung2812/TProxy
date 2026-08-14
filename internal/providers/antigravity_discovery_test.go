package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

// Mirrors the fetchAvailableModels payload: a map keyed by model ID, carrying
// display names, internal flags and quota state.
const antigravityCatalogue = `{"models":{
	"gemini-3-pro-agent":{"displayName":"Gemini 3 Pro","quotaInfo":{"remainingFraction":1}},
	"gemini-3-flash":{"displayName":"Gemini 3 Flash","quotaInfo":{"remainingFraction":0.5}},
	"gemini-3-pro-image":{"displayName":"Gemini 3 Pro Image"},
	"claude-sonnet-4-6":{"displayName":"Claude Sonnet 4.6 (Thinking)"},
	"internal-scratch":{"displayName":"Internal","isInternal":true},
	"tab_flash_lite_preview":{"displayName":"Tab Completion"},
	"gemini-2.5-pro":{"displayName":"Legacy Pro"},
	"no-display-name":{}
}}`

func antigravityDiscoveryRegistry(t *testing.T, handler http.HandlerFunc) (*Registry, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	registry := NewRegistry()
	registry.client = server.Client()
	return registry, server
}

func antigravityOAuthCredential() store.Credential {
	return store.Credential{
		AuthType:   "oauth",
		OAuthToken: &store.OAuthToken{AccessToken: "token-value", Extra: map[string]any{"project_id": "cloud-project"}},
	}
}

// The provider page reported "0 upstream models" while the quota badges for
// those same models rendered beside it, because discovery refused to call the
// catalogue endpoint the quota tracker was already using successfully.
func TestAntigravityDiscoveryReturnsCatalogue(t *testing.T) {
	var authorization, userAgent string
	var requestBody map[string]any
	registry, server := antigravityDiscoveryRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		userAgent = r.Header.Get("User-Agent")
		payload, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(payload, &requestBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(antigravityCatalogue))
	})
	original := antigravityModelsURLForTest(server.URL)
	defer original()

	models, err := registry.DiscoverModels(context.Background(), store.Provider{ID: "antigravity", Type: "antigravity"}, antigravityOAuthCredential())
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}

	if authorization != "Bearer token-value" {
		t.Errorf("Authorization = %q", authorization)
	}
	// Cloud Code is called by the IDE, not the Hub launcher.
	if !strings.HasPrefix(userAgent, "antigravity/ide/") {
		t.Errorf("User-Agent = %q, want the Antigravity IDE agent", userAgent)
	}
	// The catalogue is per project, so the scope has to be sent.
	if requestBody["project"] != "cloud-project" {
		t.Errorf("request project = %#v, want cloud-project", requestBody["project"])
	}

	got := map[string]DiscoveredModel{}
	for _, model := range models {
		got[model.ID] = model
	}
	for _, want := range []string{"gemini-3-pro-agent", "gemini-3-flash", "gemini-3-pro-image", "claude-sonnet-4-6", "no-display-name"} {
		if _, ok := got[want]; !ok {
			t.Errorf("model %q missing from discovery", want)
		}
	}
	// Editor scaffolding and models flagged internal are not routable.
	for _, unwanted := range []string{"internal-scratch", "tab_flash_lite_preview", "gemini-2.5-pro"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("internal model %q leaked into discovery", unwanted)
		}
	}
	if got["gemini-3-flash"].Name != "Gemini 3 Flash" {
		t.Errorf("display name = %q", got["gemini-3-flash"].Name)
	}
	// A missing display name must not produce a blank row in the dashboard.
	if got["no-display-name"].Name != "no-display-name" {
		t.Errorf("fallback name = %q, want the model ID", got["no-display-name"].Name)
	}
	if got["gemini-3-pro-agent"].OwnedBy != "antigravity" {
		t.Errorf("owned_by = %q", got["gemini-3-pro-agent"].OwnedBy)
	}
}

// Image models take the image_gen request path and cannot serve chat, so they
// must not be advertised as text models.
func TestAntigravityDiscoveryMarksImageModels(t *testing.T) {
	registry, server := antigravityDiscoveryRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(antigravityCatalogue))
	})
	defer antigravityModelsURLForTest(server.URL)()

	models, err := registry.DiscoverModels(context.Background(), store.Provider{ID: "antigravity", Type: "antigravity"}, antigravityOAuthCredential())
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if model.ID == "gemini-3-pro-image" {
			if len(model.Capabilities) != 1 || model.Capabilities[0] != "image" {
				t.Fatalf("image model capabilities = %v, want [image]", model.Capabilities)
			}
			return
		}
	}
	t.Fatal("image model not returned")
}

func TestAntigravityDiscoveryOrdersResultsStably(t *testing.T) {
	registry, server := antigravityDiscoveryRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(antigravityCatalogue))
	})
	defer antigravityModelsURLForTest(server.URL)()

	provider := store.Provider{ID: "antigravity", Type: "antigravity"}
	first, err := registry.DiscoverModels(context.Background(), provider, antigravityOAuthCredential())
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		next, err := registry.DiscoverModels(context.Background(), provider, antigravityOAuthCredential())
		if err != nil {
			t.Fatal(err)
		}
		for i := range first {
			if first[i].ID != next[i].ID {
				t.Fatalf("model order changed between calls: %q vs %q", first[i].ID, next[i].ID)
			}
		}
	}
}

func TestAntigravityDiscoveryReportsAuthFailure(t *testing.T) {
	registry, server := antigravityDiscoveryRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer antigravityModelsURLForTest(server.URL)()

	_, err := registry.DiscoverModels(context.Background(), store.Provider{ID: "antigravity", Type: "antigravity"}, antigravityOAuthCredential())
	if Code(err) != "authorization_required" {
		t.Fatalf("error code = %q, want authorization_required", Code(err))
	}
}

func TestAntigravityDiscoveryRequiresAccessToken(t *testing.T) {
	registry := NewRegistry()
	_, err := registry.DiscoverModels(context.Background(), store.Provider{ID: "antigravity", Type: "antigravity"}, store.Credential{AuthType: "oauth"})
	if Code(err) != "authorization_required" {
		t.Fatalf("error code = %q, want authorization_required", Code(err))
	}
}

func TestAntigravityAdapterAdvertisesModelDiscovery(t *testing.T) {
	descriptor, err := NewRegistry().Describe("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if !descriptor.ModelDiscovery {
		t.Fatal("antigravity descriptor still reports model discovery as unsupported")
	}
}

// antigravityModelsURLForTest redirects discovery at a local server and returns
// a function that restores the real endpoint.
func antigravityModelsURLForTest(baseURL string) func() {
	original := antigravityModelsURL
	antigravityModelsURL = baseURL + "/v1internal:fetchAvailableModels"
	return func() { antigravityModelsURL = original }
}
