package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

func TestResponsesSessionContinuationsAndTools(t *testing.T) {
	cache := responseSessions{}
	first := map[string]any{"model": "m", "instructions": "concise", "input": []any{map[string]any{"role": "user", "content": "hello"}}}
	call := map[string]any{"type": "function_call", "call_id": "call_1", "name": "weather", "arguments": "{}"}
	cache.put("client-a", "r1", first, []any{call})
	second, err := cache.prepare("client-a", "", map[string]any{"previous_response_id": "r1", "input": []any{map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "sunny"}}})
	if err != nil {
		t.Fatal(err)
	}
	if second["model"] != "m" || second["instructions"] != "concise" || len(responsesInput(second["input"])) != 3 {
		t.Fatalf("continuation: %#v", second)
	}
	if _, exists := second["previous_response_id"]; exists {
		t.Fatal("HTTP upstream cannot depend on stored response")
	}
	cache.put("client-a", "r2", second, []any{map[string]any{"role": "assistant", "content": "sunny"}})
	third, err := cache.prepare("client-a", "r2", map[string]any{"previous_response_id": "r2", "input": "tomorrow?"})
	if err != nil || len(responsesInput(third["input"])) != 5 {
		t.Fatalf("third turn: %#v %v", third, err)
	}
	full := append(responsesInput(third["input"]), map[string]any{"role": "user", "content": "more"})
	replaced, err := cache.prepare("client-a", "", map[string]any{"previous_response_id": "r2", "input": full})
	if err != nil || !reflect.DeepEqual(full, replaced["input"]) {
		t.Fatalf("full transcript duplicated: %#v %v", replaced, err)
	}
	compacted, err := cache.prepare("client-a", "", map[string]any{"previous_response_id": "r2", "input": []any{map[string]any{"type": "compaction", "encrypted_content": "summary"}}})
	if err != nil || len(responsesInput(compacted["input"])) != 1 {
		t.Fatalf("compaction: %#v %v", compacted, err)
	}
	if _, err = cache.prepare("client-b", "", map[string]any{"previous_response_id": "r1", "input": "stolen"}); err == nil {
		t.Fatal("cross-client history leaked")
	}
	if _, err = cache.prepare("client-a", "", map[string]any{"model": "m", "input": []any{map[string]any{"type": "function_call_output", "call_id": "missing"}}}); err == nil {
		t.Fatal("orphaned tool result accepted")
	}
	reset, err := cache.prepare("client-a", "r2", map[string]any{"reset": true, "model": "m", "input": "new"})
	if err != nil || len(responsesInput(reset["input"])) != 1 || reset["instructions"] != nil {
		t.Fatal("reset kept old state")
	}
	// Mutating a prepared request must not mutate cached immutable history.
	responsesInput(second["input"])[0].(map[string]any)["content"] = "changed"
	prior, _ := cache.get("client-a", "r1")
	if responsesInput(prior.Payload["input"])[0].(map[string]any)["content"] != "hello" {
		t.Fatal("history aliases request")
	}
}

func TestResponsesSessionBoundsAndIdentity(t *testing.T) {
	cache := responseSessions{}
	for i := 0; i < 140; i++ {
		cache.put("s", fmt.Sprint(i), map[string]any{"model": "m", "input": "x"}, nil)
	}
	if len(cache.entries) > 128 || cache.bytes > 32<<20 {
		t.Fatal("unbounded cache")
	}
	if _, ok := cache.get("s", "0"); ok {
		t.Fatal("old entry not evicted")
	}
	item := cache.entries["s:139"]
	item.ExpiresAt = time.Now().Add(-time.Second)
	cache.entries["s:139"] = item
	if _, ok := cache.get("s", "139"); ok {
		t.Fatal("expired entry survived")
	}
	if _, err := cache.prepare("s", "", map[string]any{"model": "m", "input": strings.Repeat("x", responseSessionMaxBytes)}); err == nil {
		t.Fatal("oversized context accepted")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Client-Request-Id", "request-only")
	if sessionIDFromRequest(r) != "" {
		t.Fatal("per-request ID treated as session")
	}
	if got := sessionIDFromRequest(r, map[string]any{"metadata": map[string]any{"user_id": `{"session_id":"stable"}`}}); got != "stable" {
		t.Fatal(got)
	}
	if got := sessionIDFromRequest(r, map[string]any{"metadata": map[string]any{"user_id": "user_abc_account_def_session_stable"}}); got != "stable" {
		t.Fatal(got)
	}
	a := r.WithContext(context.WithValue(r.Context(), apiKeyContext, &store.APIKey{ID: "a"}))
	b := r.WithContext(context.WithValue(r.Context(), apiKeyContext, &store.APIKey{ID: "b"}))
	if responseSessionScope(a, "same") == responseSessionScope(b, "same") {
		t.Fatal("scope collides")
	}
}
