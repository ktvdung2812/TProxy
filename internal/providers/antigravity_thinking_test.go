package providers

import (
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func antigravityThinkingCredential() store.Credential {
	return store.Credential{AuthType: "oauth", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "p"}}}
}

func antigravityInnerRequest(t *testing.T, request canonical.Request) map[string]any {
	t.Helper()
	body, err := antigravityBody(request, antigravityThinkingCredential())
	if err != nil {
		t.Fatal(err)
	}
	inner, _ := body["request"].(map[string]any)
	if inner == nil {
		t.Fatal("request envelope missing its inner request")
	}
	return inner
}

// Gemini 3+ rejects a functionCall replayed without its thoughtSignature, and
// no client persists that signature across turns. Without a backfill the second
// request of every tool conversation fails.
func TestAntigravityBackfillsThoughtSignatureOnToolHistory(t *testing.T) {
	request := canonical.Request{
		RequestID:     "r",
		UpstreamModel: "gemini-3-pro",
		Messages: []canonical.Message{
			{Role: "user", Content: "list the files"},
			{Role: "assistant", ToolCalls: []map[string]any{
				{"id": "call-1", "type": "function", "function": map[string]any{"name": "list_dir", "arguments": `{"path":"."}`}},
			}},
			{Role: "tool", ToolCallID: "call-1", Name: "list_dir", Content: "a.go b.go"},
		},
	}
	inner := antigravityInnerRequest(t, request)

	var checked bool
	for _, content := range antigravityMapSlice(inner["contents"]) {
		for _, part := range antigravityMapSlice(content["parts"]) {
			if _, ok := part["functionCall"].(map[string]any); !ok {
				continue
			}
			checked = true
			if signature := stringValue(part["thoughtSignature"]); signature == "" {
				t.Fatal("functionCall part was sent without a thoughtSignature")
			}
		}
	}
	if !checked {
		t.Fatal("no functionCall part reached the upstream body")
	}
}

// A signature the caller supplied is theirs to keep.
func TestAntigravityKeepsCallerThoughtSignature(t *testing.T) {
	inner := map[string]any{
		"contents": []any{
			map[string]any{"role": "model", "parts": []any{
				map[string]any{"functionCall": map[string]any{"name": "run", "args": map[string]any{}}, "thoughtSignature": "caller-signature"},
			}},
		},
	}
	antigravityRestoreThoughtSignatures(inner)

	part := antigravityMapSlice(antigravityMapSlice(inner["contents"])[0]["parts"])[0]
	if got := stringValue(part["thoughtSignature"]); got != "caller-signature" {
		t.Fatalf("thoughtSignature = %q, want the caller's value preserved", got)
	}
}

// Reasoning replayed from an earlier turn means nothing to Cloud Code, and a
// signature with no call attached is rejected outright.
func TestAntigravityDropsThoughtOnlyParts(t *testing.T) {
	inner := map[string]any{
		"contents": []any{
			map[string]any{"role": "model", "parts": []any{
				map[string]any{"text": "let me think", "thought": true},
				map[string]any{"thoughtSignature": "orphan"},
				map[string]any{"text": "the answer"},
			}},
		},
	}
	antigravityRestoreThoughtSignatures(inner)

	parts := antigravityMapSlice(antigravityMapSlice(inner["contents"])[0]["parts"])
	if len(parts) != 1 {
		t.Fatalf("kept %d parts, want only the answer text", len(parts))
	}
	if stringValue(parts[0]["text"]) != "the answer" {
		t.Fatalf("surviving part = %#v", parts[0])
	}
}

// A thought part that also carries the call must survive: dropping it would
// lose the call itself.
func TestAntigravityKeepsThoughtPartCarryingACall(t *testing.T) {
	inner := map[string]any{
		"contents": []any{
			map[string]any{"role": "model", "parts": []any{
				map[string]any{"thought": true, "functionCall": map[string]any{"name": "run", "args": map[string]any{}}},
			}},
		},
	}
	antigravityRestoreThoughtSignatures(inner)

	parts := antigravityMapSlice(antigravityMapSlice(inner["contents"])[0]["parts"])
	if len(parts) != 1 {
		t.Fatalf("kept %d parts, want the call to survive", len(parts))
	}
	if stringValue(parts[0]["thoughtSignature"]) == "" {
		t.Fatal("surviving call was not given a signature")
	}
}

// Cloud Code rejects a declaration whose parameter schema is missing, taking
// the whole request down over one parameterless tool.
func TestAntigravityParameterlessToolGetsASchema(t *testing.T) {
	request := canonical.Request{
		RequestID:     "r",
		UpstreamModel: "gemini-3-pro",
		Messages:      []canonical.Message{{Role: "user", Content: "go"}},
		Tools:         []map[string]any{{"type": "function", "function": map[string]any{"name": "ping"}}},
	}
	inner := antigravityInnerRequest(t, request)

	groups := antigravityMapSlice(inner["tools"])
	if len(groups) == 0 {
		t.Fatal("tools were dropped")
	}
	declarations := antigravityMapSlice(groups[len(groups)-1]["functionDeclarations"])
	if len(declarations) != 1 {
		t.Fatalf("declarations = %d, want 1", len(declarations))
	}
	parameters, _ := declarations[0]["parameters"].(map[string]any)
	if parameters == nil {
		t.Fatal("parameterless tool was sent without a schema")
	}
	if parameters["type"] != "object" {
		t.Fatalf("schema type = %#v, want object", parameters["type"])
	}
}

// Without a mode the upstream may emit calls that do not match the declared
// schema, which the client then rejects.
func TestAntigravityDefaultsToolConfigWhenToolsPresent(t *testing.T) {
	request := canonical.Request{
		RequestID:     "r",
		UpstreamModel: "gemini-3-pro",
		Messages:      []canonical.Message{{Role: "user", Content: "go"}},
		Tools:         []map[string]any{{"type": "function", "function": map[string]any{"name": "ping"}}},
	}
	inner := antigravityInnerRequest(t, request)

	config, _ := inner["toolConfig"].(map[string]any)
	if config == nil {
		t.Fatal("toolConfig was not set for a request carrying tools")
	}
	calling, _ := config["functionCallingConfig"].(map[string]any)
	if calling == nil || calling["mode"] != "VALIDATED" {
		t.Fatalf("functionCallingConfig = %#v, want mode VALIDATED", calling)
	}
}

// An explicit tool choice from the caller must win over the default.
func TestAntigravityKeepsExplicitToolChoice(t *testing.T) {
	request := canonical.Request{
		RequestID:     "r",
		UpstreamModel: "gemini-3-pro",
		Messages:      []canonical.Message{{Role: "user", Content: "go"}},
		Tools:         []map[string]any{{"type": "function", "function": map[string]any{"name": "ping"}}},
		ToolChoice:    "required",
	}
	inner := antigravityInnerRequest(t, request)

	config, _ := inner["toolConfig"].(map[string]any)
	calling, _ := config["functionCallingConfig"].(map[string]any)
	if calling == nil || calling["mode"] != "ANY" {
		t.Fatalf("functionCallingConfig = %#v, want the caller's ANY mode preserved", calling)
	}
}

func TestAntigravityToolConfigAbsentWithoutTools(t *testing.T) {
	inner := antigravityInnerRequest(t, canonical.Request{
		RequestID: "r", UpstreamModel: "gemini-3-pro",
		Messages: []canonical.Message{{Role: "user", Content: "go"}},
	})
	if _, present := inner["toolConfig"]; present {
		t.Fatal("toolConfig was set on a request with no tools")
	}
}

func TestAntigravityDefaultSignatureLooksLikeASignature(t *testing.T) {
	if len(antigravityDefaultThoughtSignature) < 500 {
		t.Fatalf("signature is %d chars, far shorter than the upstream form", len(antigravityDefaultThoughtSignature))
	}
	if strings.ContainsAny(antigravityDefaultThoughtSignature, " \n\t") {
		t.Fatal("signature contains whitespace and would be rejected")
	}
}
