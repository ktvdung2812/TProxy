package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOfflineDiagnosticsUsesTranslationAndRedacts(t *testing.T) {
	// No store or router: preview cannot use credentials or make upstream calls.
	server := &Server{}
	request := httptest.NewRequest("POST", "/api/admin/diagnostics/translate", strings.NewReader(`{"source":"responses","target":"openai","model":"upstream","body":{"model":"public","instructions":"Be concise","input":"Hello","metadata":{"access_token":"do-not-show"},"api_key":"do-not-show"}}`))
	response := httptest.NewRecorder()
	server.adminTranslatePreview(response, request)
	if response.Code != 200 || strings.Contains(response.Body.String(), "do-not-show") {
		t.Fatalf("preview response: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Outbound map[string]any `json:"outbound"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	messages := body.Outbound["messages"].([]any)
	if body.Outbound["model"] != "upstream" || len(messages) != 2 || messages[0].(map[string]any)["content"] != "Be concise" {
		t.Fatalf("translation: %#v", body.Outbound)
	}
	request = httptest.NewRequest("POST", "/api/admin/diagnostics/translate", strings.NewReader(`{"source":"nope","target":"openai","body":{}}`))
	response = httptest.NewRecorder()
	server.adminTranslatePreview(response, request)
	if response.Code != 400 {
		t.Fatal("unsupported protocol accepted")
	}
	request = httptest.NewRequest("POST", "/api/admin/diagnostics/translate", strings.NewReader(`{"source":"openai","target":"codex","model":"upstream","body":{"messages":[{"role":"user","content":"Hello"}],"metadata":{"clientSecret":"hidden-value","token":"hidden-value"}}}`))
	response = httptest.NewRecorder()
	server.adminTranslatePreview(response, request)
	if response.Code != 200 || strings.Contains(response.Body.String(), "_codex_reverse_tool_names") || strings.Contains(response.Body.String(), "hidden-value") {
		t.Fatalf("preview leaked private builder fields or secrets: %d %s", response.Code, response.Body.String())
	}
}
