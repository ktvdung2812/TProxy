package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/security"
)

func (s *Server) adminTranslatePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST required", "")
		return
	}
	var payload struct {
		Source string         `json:"source"`
		Target string         `json:"target"`
		Model  string         `json:"model"`
		Body   map[string]any `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil || payload.Body == nil {
		writeError(w, 400, "invalid_request", "provide a JSON object body (maximum 1 MiB)", "")
		return
	}
	var request canonical.Request
	switch payload.Source {
	case "openai":
		request = parseOpenAIChat(payload.Body, "preview")
	case "responses":
		request = parseResponses(payload.Body, "preview")
	case "claude":
		request = parseClaude(payload.Body, "preview")
	case "gemini":
		request = parseGemini(payload.Body, "preview", payload.Model)
	default:
		writeError(w, 400, "invalid_protocol", "unsupported source protocol", "")
		return
	}
	request.UpstreamModel = payload.Model
	if request.UpstreamModel == "" {
		request.UpstreamModel = request.PublicModelID
	}
	if request.UpstreamModel == "" {
		writeError(w, 400, "model_required", "provide a model", "")
		return
	}
	body, err := providers.PreviewRequest(payload.Target, request)
	if err != nil {
		writeError(w, 400, "translation_failed", err.Error(), "")
		return
	}
	// Return only sanitized copies; diagnostic inputs are never persisted.
	writeJSON(w, 200, redactDiagnosticValue(map[string]any{"canonical": request, "outbound": body, "offline": true, "notes": []string{"Protocol body preview; account-specific headers and OAuth transformations are not applied."}}))
}

func redactDiagnosticValue(value any) any {
	// Normalize structs to JSON values before recursively filtering nested input.
	encoded, _ := json.Marshal(value)
	var normalized any
	_ = json.Unmarshal(encoded, &normalized)
	var redact func(any) any
	redact = func(v any) any {
		switch value := v.(type) {
		case map[string]any:
			for key, child := range value {
				name := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
				switch name {
				case "authorization", "api_key", "apikey", "x_api_key", "secret", "access_token", "refresh_token", "password", "cookie", "set_cookie", "id_token", "token", "client_secret", "clientsecret", "accesstoken", "refreshtoken", "proxy_authorization":
					value[key] = "[REDACTED]"
				default:
					value[key] = redact(child)
				}
			}
		case []any:
			for i := range value {
				value[i] = redact(value[i])
			}
		case string:
			return security.RedactText(value)
		}
		return v
	}
	return redact(normalized)
}
