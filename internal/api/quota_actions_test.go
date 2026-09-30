package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
)

func TestQuotaResetIdempotency(t *testing.T) {
	state := quotaResetState{}
	id := uuid.NewString()
	var count atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := state.consume(context.Background(), "a", id, func(redeem string) (providers.CodexResetConsumeResult, error) {
				count.Add(1)
				if redeem != id {
					t.Error("changed retry identity")
				}
				return providers.CodexResetConsumeResult{OK: true, Reset: true}, nil
			})
			if err != nil || !result.OK {
				t.Errorf("result=%+v err=%v", result, err)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("consumed %d credits", count.Load())
	}
	if _, err := state.consume(context.Background(), "a", "bad", func(string) (providers.CodexResetConsumeResult, error) {
		t.Fatal("invalid id dispatched")
		return providers.CodexResetConsumeResult{}, nil
	}); err == nil {
		t.Fatal("invalid ID accepted")
	}
}

func TestQuotaBatchValidatesBeforeMutating(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", Enabled: true, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none"}}}}}
	data := apiTestStore(t, cfg)
	server := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/quota/actions", strings.NewReader(`{"action":"clear-cooldown","credential_ids":["a","missing"]}`))
	response := httptest.NewRecorder()
	before, _ := data.CredentialByID(context.Background(), "a")
	server.adminQuotaActions(response, request)
	after, _ := data.CredentialByID(context.Background(), "a")
	if response.Code != 404 || !before.LastValidated.Equal(after.LastValidated) {
		t.Fatalf("partial mutation on invalid scope: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/quota/actions", strings.NewReader(`{"action":"refresh","credential_ids":["a"]}`))
	response = httptest.NewRecorder()
	server.adminQuotaActions(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"ok":false`) {
		t.Fatalf("unknown quota reported success: %s", response.Body.String())
	}
}
