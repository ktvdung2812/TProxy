package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestDevinLiveChat(t *testing.T) {
	if os.Getenv("DEVIN_LIVE") == "" {
		t.Skip("set DEVIN_LIVE=1")
	}
	data, err := os.ReadFile(os.Getenv("HOME") + "/.local/share/devin/credentials.toml")
	if err != nil {
		t.Skipf("no credentials.toml: %v", err)
	}
	var key string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "windsurf_api_key") {
			key = strings.Trim(strings.TrimSpace(strings.SplitN(line, "=", 2)[1]), `"`)
		}
	}
	if key == "" {
		t.Skip("no windsurf_api_key")
	}
	if !strings.HasPrefix(key, "devin-session-token$") {
		key = "devin-session-token$" + key
	}

	a := &devinAdapter{client: &http.Client{}}
	provider := store.Provider{ID: "devin-1", Type: "devin", Name: "Devin", BaseURL: "https://server.codeium.com", Enabled: true}
	cred := store.Credential{ID: "c1", ProviderID: "devin-1", AuthType: "api_key", Secret: key}

	req := canonical.Request{
		RequestID:     "req-live-1",
		UpstreamModel: "swe-1-6-fast",
		Messages:      []canonical.Message{{Role: "user", Content: "Reply with exactly the word: pong"}},
		MaxTokens:     64,
		Stream:        true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	events, err := a.ExecuteStream(ctx, provider, cred, req)
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	var text strings.Builder
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		switch ev.Type {
		case canonical.EventTextDelta:
			text.WriteString(ev.Text)
		case canonical.EventReasoningDelta:
			t.Logf("reasoning: %q", truncStr(ev.Reasoning, 80))
		case canonical.EventUsage:
			t.Logf("usage: in=%d out=%d", ev.Usage.InputTokens, ev.Usage.OutputTokens)
		case canonical.EventMessageEnd:
			t.Logf("end: finish=%s model=%s", ev.FinishReason, ev.Model)
		}
	}
	got := strings.TrimSpace(text.String())
	t.Logf("stream text: %q", got)
	if !strings.Contains(strings.ToLower(got), "pong") {
		t.Fatalf("expected 'pong' in response, got %q", got)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()
	req.Stream = false
	req.RequestID = "req-live-2"
	resp, err := a.Execute(ctx2, provider, cred, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	fmt.Printf("EXECUTE text=%q usage=%+v\n", truncStr(respText(resp), 200), resp.Usage)
	if !strings.Contains(strings.ToLower(respText(resp)), "pong") {
		t.Fatalf("expected 'pong' in Execute response, got %q", respText(resp))
	}
}

func TestDevinLiveDiscoveryAndQuota(t *testing.T) {
	if os.Getenv("DEVIN_LIVE") == "" {
		t.Skip("set DEVIN_LIVE=1")
	}
	data, err := os.ReadFile(os.Getenv("HOME") + "/.local/share/devin/credentials.toml")
	if err != nil {
		t.Skipf("no credentials.toml: %v", err)
	}
	var key string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "windsurf_api_key") {
			key = strings.Trim(strings.TrimSpace(strings.SplitN(line, "=", 2)[1]), `"`)
		}
	}
	if key == "" {
		t.Skip("no windsurf_api_key")
	}
	if !strings.HasPrefix(key, "devin-session-token$") {
		key = "devin-session-token$" + key
	}

	registry := NewRegistry()
	provider := store.Provider{ID: "devin-1", Type: "devin", Name: "Devin", BaseURL: "https://server.codeium.com", Enabled: true}
	cred := store.Credential{ID: "c1", ProviderID: "devin-1", AuthType: "api_key", Secret: key}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := registry.DiscoverModels(ctx, provider, cred)
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	t.Logf("discovered %d models", len(models))
	if len(models) == 0 {
		t.Fatal("expected live model catalog")
	}

	quota := registry.devinQuota(ctx, provider, cred)
	t.Logf("quota: plan=%q renews=%q daily=%+v weekly=%+v msg=%q", quota.Plan, quota.RenewsAt, quota.Quotas["daily"], quota.Quotas["weekly"], quota.Message)
	if quota.Message != "" {
		t.Fatalf("quota probe failed: %s", quota.Message)
	}
}

func truncStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func respText(r *canonical.Response) string {
	if r == nil {
		return ""
	}
	if s, ok := r.Content.(string); ok {
		return s
	}
	b, _ := json.Marshal(r.Content)
	return string(b)
}
