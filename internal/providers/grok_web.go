package providers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	grokWebDefaultURL = "https://grok.com/rest/app-chat/conversations/new"
	grokWebUserAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"
)

// grokWebModel maps public model IDs to grok.com wire fields.
type grokWebModel struct {
	GrokModel  string
	ModelMode  string
	IsThinking bool
}

var grokWebModelMap = map[string]grokWebModel{
	"grok-3":           {GrokModel: "grok-3", ModelMode: "MODEL_MODE_GROK_3", IsThinking: false},
	"grok-3-mini":      {GrokModel: "grok-3", ModelMode: "MODEL_MODE_GROK_3_MINI_THINKING", IsThinking: true},
	"grok-3-thinking":  {GrokModel: "grok-3", ModelMode: "MODEL_MODE_GROK_3_THINKING", IsThinking: true},
	"grok-4":           {GrokModel: "grok-4", ModelMode: "MODEL_MODE_GROK_4", IsThinking: false},
	"grok-4-mini":      {GrokModel: "grok-4-mini", ModelMode: "MODEL_MODE_GROK_4_MINI_THINKING", IsThinking: true},
	"grok-4-thinking":  {GrokModel: "grok-4", ModelMode: "MODEL_MODE_GROK_4_THINKING", IsThinking: true},
	"grok-4-heavy":     {GrokModel: "grok-4", ModelMode: "MODEL_MODE_HEAVY", IsThinking: true},
	"grok-4.1-mini":    {GrokModel: "grok-4-1-thinking-1129", ModelMode: "MODEL_MODE_GROK_4_1_MINI_THINKING", IsThinking: true},
	"grok-4.1-fast":    {GrokModel: "grok-4-1-thinking-1129", ModelMode: "MODEL_MODE_FAST", IsThinking: false},
	"grok-4.1-expert":  {GrokModel: "grok-4-1-thinking-1129", ModelMode: "MODEL_MODE_EXPERT", IsThinking: true},
	"grok-4.1-thinking": {GrokModel: "grok-4-1-thinking-1129", ModelMode: "MODEL_MODE_GROK_4_1_THINKING", IsThinking: true},
	"grok-4.2":         {GrokModel: "grok-420", ModelMode: "MODEL_MODE_GROK_420", IsThinking: false},
	"grok-4.20":        {GrokModel: "grok-420", ModelMode: "MODEL_MODE_GROK_420", IsThinking: false},
	"grok-4.20-beta":   {GrokModel: "grok-420", ModelMode: "MODEL_MODE_GROK_420", IsThinking: false},
}

type grokWebAdapter struct{ client *http.Client }

func (a *grokWebAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	events, err := a.ExecuteStream(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	result := &canonical.Response{Model: request.UpstreamModel, Role: "assistant", Raw: map[string]any{}}
	var text, reasoning strings.Builder
	for event := range events {
		switch event.Type {
		case canonical.EventMessageStart:
			result.ID = event.ID
			if event.Model != "" {
				result.Model = event.Model
			}
		case canonical.EventTextDelta:
			text.WriteString(event.Text)
		case canonical.EventReasoningDelta:
			reasoning.WriteString(event.Reasoning)
		case canonical.EventUsage:
			if event.Usage != nil {
				result.Usage = *event.Usage
			}
		case canonical.EventMessageEnd:
			result.FinishReason = event.FinishReason
		case canonical.EventError:
			return nil, event.Err
		}
	}
	result.Content = text.String()
	result.Reasoning = reasoning.String()
	return result, nil
}

func (a *grokWebAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	message := flattenMessagesForWeb(request)
	if strings.TrimSpace(message) == "" {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "empty messages for Grok Web"}
	}
	modelInfo, ok := grokWebModelMap[strings.TrimSpace(request.UpstreamModel)]
	if !ok {
		modelInfo = grokWebModelMap["grok-4.1-fast"]
	}
	payload := map[string]any{
		"temporary": true, "modelName": modelInfo.GrokModel, "modelMode": modelInfo.ModelMode, "message": message,
		"fileAttachments": []any{}, "imageAttachments": []any{},
		"disableSearch": false, "enableImageGeneration": false, "returnImageBytes": false,
		"returnRawGrokInXaiRequest": false, "enableImageStreaming": false, "imageGenerationCount": 0,
		"forceConcise": false, "toolOverrides": map[string]any{}, "enableSideBySide": true, "sendFinalMetadata": true,
		"isReasoning": false, "disableTextFollowUps": false, "disableMemory": true,
		"forceSideBySide": false, "isAsyncChat": false, "disableSelfHarmShortCircuit": false,
		"deviceEnvInfo": map[string]any{
			"darkModeEnabled": false, "devicePixelRatio": 2,
			"screenWidth": 2056, "screenHeight": 1329, "viewportWidth": 2056, "viewportHeight": 1083,
		},
	}
	target := strings.TrimSpace(provider.BaseURL)
	if target == "" {
		target = grokWebDefaultURL
	}
	headers := grokWebHeaders(credential)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Code: "invalid_request", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	req.Header = headers
	response, err := a.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Message: "Grok Web connection failed", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		msg := fmt.Sprintf("Grok Web returned HTTP %d", response.StatusCode)
		if response.StatusCode == 401 || response.StatusCode == 403 {
			msg = "Grok Web auth failed — SSO cookie may be expired. Re-paste sso cookie from grok.com."
		} else if response.StatusCode == 429 {
			msg = "Grok Web rate limited. Wait or rotate cookies."
		}
		return nil, &ProviderError{Status: response.StatusCode, Code: store.ErrorCode(response.StatusCode), Message: msg}
	}

	out := make(chan canonical.Event, 32)
	go func() {
		defer close(out)
		defer response.Body.Close()
		id := "chatcmpl-grok-" + randomHex(6)
		out <- canonical.Event{Type: canonical.EventMessageStart, ID: id, Model: request.UpstreamModel}
		scanner := bufio.NewScanner(response.Body)
		// Grok NDJSON lines can be large.
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 4*1024*1024)
		var full strings.Builder
		thinkOpen := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}
			if errObj, ok := event["error"].(map[string]any); ok {
				msg := stringValue(errObj["message"])
				if msg == "" {
					msg = "Grok Web error"
				}
				out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "upstream_error", Message: msg}}
				return
			}
			resp, _ := event["result"].(map[string]any)
			if resp == nil {
				continue
			}
			if nested, ok := resp["response"].(map[string]any); ok {
				resp = nested
			}
			if mr, ok := resp["modelResponse"].(map[string]any); ok {
				if msg := stringValue(mr["message"]); msg != "" {
					if thinkOpen && modelInfo.IsThinking {
						out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: msg}
						thinkOpen = false
					}
					// Full message replaces stream accumulation.
					if full.Len() == 0 {
						out <- canonical.Event{Type: canonical.EventTextDelta, Text: msg}
					} else if !strings.HasPrefix(msg, full.String()) {
						// Replacement snapshot — emit suffix only when possible.
						out <- canonical.Event{Type: canonical.EventTextDelta, Text: msg}
					}
					full.Reset()
					full.WriteString(msg)
				}
				continue
			}
			if token, ok := resp["token"].(string); ok && token != "" {
				full.WriteString(token)
				out <- canonical.Event{Type: canonical.EventTextDelta, Text: token}
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "upstream_read_error", Err: err}}
			return
		}
		usage := canonical.Usage{
			InputTokens:  maxInt(1, len(message)/4),
			OutputTokens: maxInt(1, full.Len()/4),
		}
		out <- canonical.Event{Type: canonical.EventUsage, Usage: &usage}
		out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "stop"}
		_ = thinkOpen
	}()
	return out, nil
}

func grokWebHeaders(credential store.Credential) http.Header {
	headers := make(http.Header)
	headers.Set("Accept", "*/*")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Content-Type", "application/json")
	headers.Set("Origin", "https://grok.com")
	headers.Set("Pragma", "no-cache")
	headers.Set("Referer", "https://grok.com/")
	headers.Set("User-Agent", grokWebUserAgent)
	headers.Set("x-statsig-id", grokWebStatsigID())
	headers.Set("x-xai-request-id", randomUUIDLike())
	traceID := randomHex(16)
	spanID := randomHex(8)
	headers.Set("traceparent", fmt.Sprintf("00-%s-%s-00", traceID, spanID))
	if cookie := normalizeGrokSSOCookie(credential.Secret); cookie != "" {
		headers.Set("Cookie", "sso="+cookie)
	}
	return headers
}

func normalizeGrokSSOCookie(secret string) string {
	token := strings.TrimSpace(secret)
	token = strings.TrimPrefix(token, "sso=")
	token = strings.TrimSpace(token)
	return token
}

func flattenMessagesForWeb(request canonical.Request) string {
	type item struct {
		role string
		text string
	}
	var extracted []item
	if request.System != nil {
		if s := strings.TrimSpace(fmt.Sprint(request.System)); s != "" && s != "<nil>" {
			extracted = append(extracted, item{role: "system", text: s})
		}
	}
	for _, msg := range request.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "developer" {
			role = "system"
		}
		text := messageTextContent(msg.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		extracted = append(extracted, item{role: role, text: text})
	}
	lastUser := -1
	for i := len(extracted) - 1; i >= 0; i-- {
		if extracted[i].role == "user" {
			lastUser = i
			break
		}
	}
	parts := make([]string, 0, len(extracted))
	for i, it := range extracted {
		if i == lastUser {
			parts = append(parts, it.text)
		} else {
			parts = append(parts, it.role+": "+it.text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func messageTextContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, raw := range v {
			item, _ := raw.(map[string]any)
			if item == nil {
				continue
			}
			if stringValue(item["type"]) == "text" || item["type"] == nil {
				b.WriteString(stringValue(item["text"]))
			}
		}
		return b.String()
	case []map[string]any:
		var b strings.Builder
		for _, item := range v {
			if stringValue(item["type"]) == "text" || item["type"] == nil {
				b.WriteString(stringValue(item["text"]))
			}
		}
		return b.String()
	default:
		if content == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(content))
	}
}

func grokWebStatsigID() string {
	// Best-effort browser-like statsig id (9router-compatible).
	msg := "e:TypeError: Cannot read properties of undefined (reading '" + randomAlpha(10) + "')"
	return base64.StdEncoding.EncodeToString([]byte(msg))
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func randomAlpha(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	for i := range buf {
		buf[i] = chars[int(buf[i])%len(chars)]
	}
	return string(buf)
}

func randomUUIDLike() string {
	// UUID v4-ish without external deps.
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ Adapter = (*grokWebAdapter)(nil)
