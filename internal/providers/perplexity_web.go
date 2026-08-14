package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	pplxWebDefaultURL = "https://www.perplexity.ai/rest/sse/perplexity_ask"
	pplxWebAPIVersion = "2.18"
	pplxWebUserAgent  = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	pplxSessionMaxAge = time.Hour
	pplxSessionMaxN   = 200
)

// mode, model_preference
var pplxWebModelMap = map[string][2]string{
	"pplx-auto":     {"concise", "pplx_pro"},
	"pplx-sonar":    {"copilot", "experimental"},
	"pplx-gpt":      {"copilot", "gpt54"},
	"pplx-gemini":   {"copilot", "gemini31pro_high"},
	"pplx-sonnet":   {"copilot", "claude46sonnet"},
	"pplx-opus":     {"copilot", "claude46opus"},
	"pplx-nemotron": {"copilot", "nv_nemotron_3_super"},
}

var pplxWebThinkingMap = map[string]string{
	"pplx-gpt":    "gpt54_thinking",
	"pplx-sonnet": "claude46sonnetthinking",
	"pplx-opus":   "claude46opusthinking",
}

var (
	pplxCitationRE   = regexp.MustCompile(`\[\d+\]`)
	pplxGrokTagRE    = regexp.MustCompile(`(?s)<grok:[^>]*>.*?</grok:[^>]*>`)
	pplxGrokSelfRE   = regexp.MustCompile(`<grok:[^>]*/>`)
	pplxXMLDeclRE    = regexp.MustCompile(`<\?xml[^?]*\?>`)
	pplxResponseRE   = regexp.MustCompile(`(?i)</?response\b[^>]*>`)
	pplxMultiSpace   = regexp.MustCompile(` {2,}`)
	pplxMultiNL      = regexp.MustCompile(`\n{3,}`)
	pplxSessionMu    sync.Mutex
	pplxSessionCache = map[string]pplxSessionEntry{}
)

type pplxSessionEntry struct {
	UUID string
	TS   time.Time
}

type perplexityWebAdapter struct{ client *http.Client }

func (a *perplexityWebAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
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
	result.Content = cleanPplxResponse(text.String(), true)
	result.Reasoning = reasoning.String()
	return result, nil
}

func (a *perplexityWebAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	parsed := parsePplxMessages(request)
	if strings.TrimSpace(parsed.current) == "" && len(parsed.history) == 0 {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "empty messages for Perplexity Web"}
	}

	model := strings.TrimSpace(request.UpstreamModel)
	mode, pref := "copilot", model
	thinking := requestWantsThinking(request)
	if thinking {
		if tpref, ok := pplxWebThinkingMap[model]; ok {
			mode, pref = "copilot", tpref
		} else if pair, ok := pplxWebModelMap[model]; ok {
			mode, pref = pair[0], pair[1]
		}
	} else if pair, ok := pplxWebModelMap[model]; ok {
		mode, pref = pair[0], pair[1]
	}

	followUp := pplxSessionLookup(parsed.history)
	query := buildPplxQuery(parsed, followUp, request.Tools)
	if strings.TrimSpace(query) == "" {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "empty query after processing"}
	}

	pplxBody := buildPplxRequestBody(query, mode, pref, followUp)
	target := strings.TrimSpace(provider.BaseURL)
	if target == "" {
		target = pplxWebDefaultURL
	}
	headers := pplxWebHeaders(credential)
	encoded, err := json.Marshal(pplxBody)
	if err != nil {
		return nil, &ProviderError{Code: "invalid_request", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	req.Header = headers
	response, err := a.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Message: "Perplexity Web connection failed", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		msg := fmt.Sprintf("Perplexity Web returned HTTP %d", response.StatusCode)
		if response.StatusCode == 401 || response.StatusCode == 403 {
			msg = "Perplexity auth failed — session cookie may be expired. Re-paste __Secure-next-auth.session-token."
		} else if response.StatusCode == 429 {
			msg = "Perplexity rate limited. Wait and retry."
		}
		return nil, &ProviderError{Status: response.StatusCode, Code: store.ErrorCode(response.StatusCode), Message: msg}
	}

	out := make(chan canonical.Event, 32)
	go func() {
		defer close(out)
		defer response.Body.Close()
		id := "chatcmpl-pplx-" + randomHex(6)
		out <- canonical.Event{Type: canonical.EventMessageStart, ID: id, Model: request.UpstreamModel}

		var full strings.Builder
		seenLen := 0
		seenThinking := map[string]bool{}
		var backendUUID string

		reader := bufio.NewReaderSize(response.Body, 256*1024)
		var dataLines []string
		flush := func() map[string]any {
			if len(dataLines) == 0 {
				return nil
			}
			payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
			dataLines = nil
			if payload == "" || payload == "[DONE]" {
				return map[string]any{"__done": true}
			}
			var event map[string]any
			if json.Unmarshal([]byte(payload), &event) != nil {
				return nil
			}
			return event
		}
		processEvent := func(event map[string]any) bool {
			if event == nil {
				return false
			}
			if event["__done"] == true {
				return true
			}
			if msg := stringValue(firstValue(event, "error_message", "error")); msg != "" {
				out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "upstream_error", Message: msg}}
				return true
			}
			if u := stringValue(event["backend_uuid"]); u != "" {
				backendUUID = u
			}
			blocks, _ := event["blocks"].([]any)
			for _, raw := range blocks {
				block, _ := raw.(map[string]any)
				if block == nil {
					continue
				}
				usage := stringValue(block["intended_usage"])
				if usage == "pro_search_steps" {
					if plan, ok := block["plan_block"].(map[string]any); ok {
						for _, sraw := range toAnySlice(plan["steps"]) {
							step, _ := sraw.(map[string]any)
							if step == nil {
								continue
							}
							switch stringValue(step["step_type"]) {
							case "SEARCH_WEB":
								if content, ok := step["search_web_content"].(map[string]any); ok {
									for _, qraw := range toAnySlice(content["queries"]) {
										qitem, _ := qraw.(map[string]any)
										qr := stringValue(qitem["query"])
										if qr != "" && !seenThinking[qr] {
											seenThinking[qr] = true
											out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: "Searching: " + qr + "\n"}
										}
									}
								}
							case "READ_RESULTS":
								if content, ok := step["read_results_content"].(map[string]any); ok {
									urls := toAnySlice(content["urls"])
									for i, u := range urls {
										if i >= 3 {
											break
										}
										us := stringValue(u)
										if us != "" && !seenThinking[us] {
											seenThinking[us] = true
											out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: "Reading: " + us + "\n"}
										}
									}
								}
							}
						}
					}
				}
				if usage == "plan" {
					if plan, ok := block["plan_block"].(map[string]any); ok {
						for _, graw := range toAnySlice(plan["goals"]) {
							goal, _ := graw.(map[string]any)
							desc := stringValue(goal["description"])
							if desc != "" && !seenThinking[desc] {
								seenThinking[desc] = true
								out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: desc + "\n"}
							}
						}
					}
				}
				if !strings.Contains(usage, "markdown") {
					continue
				}
				mb, _ := block["markdown_block"].(map[string]any)
				if mb == nil {
					continue
				}
				chunks := toAnySlice(mb["chunks"])
				if len(chunks) == 0 {
					continue
				}
				var joined strings.Builder
				for _, c := range chunks {
					joined.WriteString(stringValue(c))
				}
				chunkText := joined.String()
				if stringValue(mb["progress"]) == "DONE" {
					full.Reset()
					full.WriteString(chunkText)
					seenLen = full.Len()
					continue
				}
				cumulative := full.String() + chunkText
				if len(cumulative) > seenLen {
					delta := cumulative[seenLen:]
					full.Reset()
					full.WriteString(cumulative)
					seenLen = full.Len()
					if cleaned := cleanPplxResponse(delta, false); cleaned != "" {
						out <- canonical.Event{Type: canonical.EventTextDelta, Text: cleaned}
					}
				}
			}
			if len(blocks) == 0 {
				if t := strings.TrimSpace(stringValue(event["text"])); t != "" && len(t) > seenLen {
					delta := t[seenLen:]
					full.Reset()
					full.WriteString(t)
					seenLen = full.Len()
					if cleaned := cleanPplxResponse(delta, false); cleaned != "" {
						out <- canonical.Event{Type: canonical.EventTextDelta, Text: cleaned}
					}
				}
			}
			if event["final"] == true || stringValue(event["status"]) == "COMPLETED" {
				return true
			}
			return false
		}

		for {
			if ctx.Err() != nil {
				break
			}
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "upstream_read_error", Err: err}}
					return
				}
				// flush remainder
				if strings.HasPrefix(strings.TrimSpace(line), "data:") {
					dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:")))
				}
				if event := flush(); processEvent(event) {
					break
				}
				break
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if processEvent(flush()) {
					break
				}
				continue
			}
			if line == "event: end_of_stream" {
				break
			}
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(line[5:]))
			}
		}

		answer := cleanPplxResponse(full.String(), true)
		pplxSessionStore(parsed.history, parsed.current, answer, backendUUID)
		usage := canonical.Usage{
			InputTokens:  maxInt(1, len(parsed.current)/4),
			OutputTokens: maxInt(1, len(answer)/4),
		}
		out <- canonical.Event{Type: canonical.EventUsage, Usage: &usage}
		out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "stop"}
	}()
	return out, nil
}

type pplxParsedMessages struct {
	system  string
	history []map[string]string
	current string
}

func parsePplxMessages(request canonical.Request) pplxParsedMessages {
	var out pplxParsedMessages
	if request.System != nil {
		if s := strings.TrimSpace(fmt.Sprint(request.System)); s != "" && s != "<nil>" {
			out.system = s + "\n"
		}
	}
	for _, msg := range request.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "developer" {
			role = "system"
		}
		content := messageTextContent(msg.Content)
		if strings.TrimSpace(content) == "" {
			continue
		}
		switch role {
		case "system":
			out.system += content + "\n"
		case "user", "assistant":
			out.history = append(out.history, map[string]string{"role": role, "content": content})
		}
	}
	if n := len(out.history); n > 0 && out.history[n-1]["role"] == "user" {
		out.current = out.history[n-1]["content"]
		out.history = out.history[:n-1]
	}
	return out
}

func requestWantsThinking(request canonical.Request) bool {
	if request.Reasoning != nil {
		if effort := strings.ToLower(stringValue(request.Reasoning["effort"])); effort != "" && effort != "none" {
			return true
		}
	}
	if request.Raw != nil {
		if request.Raw["thinking"] == true {
			return true
		}
		if effort := strings.ToLower(stringValue(request.Raw["reasoning_effort"])); effort != "" && effort != "none" {
			return true
		}
	}
	return false
}

func buildPplxQuery(parsed pplxParsedMessages, followUpUUID string, tools []map[string]any) string {
	if followUpUUID != "" {
		return parsed.current
	}
	obj := map[string]any{}
	instr := []string{}
	if strings.TrimSpace(parsed.system) != "" {
		instr = append(instr, strings.TrimSpace(parsed.system))
	}
	if hint := formatPplxToolsHint(tools); hint != "" {
		instr = append(instr, hint)
	}
	instr = append(instr, "You have built-in web search. Answer questions directly using search results.")
	obj["instructions"] = instr
	if len(parsed.history) > 0 {
		obj["history"] = parsed.history
	}
	obj["query"] = parsed.current
	raw, _ := json.Marshal(obj)
	s := string(raw)
	if len(s) > 96000 {
		return s[len(s)-96000:]
	}
	return s
}

func formatPplxToolsHint(tools []map[string]any) string {
	if len(tools) == 0 {
		return ""
	}
	lines := make([]string, 0, len(tools))
	for _, t := range tools {
		fn, _ := t["function"].(map[string]any)
		if fn == nil {
			fn = t
		}
		name := stringValue(fn["name"])
		if name == "" {
			name = "unnamed"
		}
		desc := strings.SplitN(stringValue(fn["description"]), "\n", 2)[0]
		if len(desc) > 200 {
			desc = desc[:200]
		}
		lines = append(lines, "- "+name+": "+desc)
	}
	return "Available tools (reference only, cannot invoke):\n" + strings.Join(lines, "\n")
}

func buildPplxRequestBody(query, mode, modelPref, followUpUUID string) map[string]any {
	return map[string]any{
		"query_str": query,
		"params": map[string]any{
			"query_str":             query,
			"search_focus":          "internet",
			"mode":                  mode,
			"model_preference":      modelPref,
			"sources":               []string{"web"},
			"attachments":           []any{},
			"frontend_uuid":         randomUUIDLike(),
			"frontend_context_uuid": randomUUIDLike(),
			"version":               pplxWebAPIVersion,
			"language":              "en-US",
			"timezone":              "UTC",
			"search_recency_filter": nil,
			"is_incognito":          true,
			"use_schematized_api":   true,
			"last_backend_uuid":     followUpUUID,
		},
	}
}

func pplxWebHeaders(credential store.Credential) http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "text/event-stream")
	headers.Set("Origin", "https://www.perplexity.ai")
	headers.Set("Referer", "https://www.perplexity.ai/")
	headers.Set("User-Agent", pplxWebUserAgent)
	headers.Set("X-App-ApiClient", "default")
	headers.Set("X-App-ApiVersion", pplxWebAPIVersion)
	if cookie := normalizePplxSessionCookie(credential.Secret); cookie != "" {
		headers.Set("Cookie", "__Secure-next-auth.session-token="+cookie)
	}
	return headers
}

func normalizePplxSessionCookie(secret string) string {
	token := strings.TrimSpace(secret)
	for _, prefix := range []string{"__Secure-next-auth.session-token=", "next-auth.session-token="} {
		if strings.HasPrefix(token, prefix) {
			token = strings.TrimSpace(strings.TrimPrefix(token, prefix))
		}
	}
	return token
}

func cleanPplxResponse(text string, strip bool) string {
	t := text
	t = pplxXMLDeclRE.ReplaceAllString(t, "")
	t = pplxCitationRE.ReplaceAllString(t, "")
	t = pplxGrokTagRE.ReplaceAllString(t, "")
	t = pplxGrokSelfRE.ReplaceAllString(t, "")
	t = pplxResponseRE.ReplaceAllString(t, "")
	if strip {
		t = pplxMultiSpace.ReplaceAllString(t, " ")
		t = pplxMultiNL.ReplaceAllString(t, "\n\n")
		t = strings.TrimSpace(t)
	}
	return t
}

func pplxSessionKey(history []map[string]string) string {
	var b strings.Builder
	for _, h := range history {
		b.WriteString(h["role"])
		b.WriteByte(':')
		b.WriteString(h["content"])
		b.WriteByte('\n')
	}
	s := b.String()
	// FNV-1a 32-bit
	var hash uint32 = 0x811c9dc5
	for i := 0; i < len(s); i++ {
		hash ^= uint32(s[i])
		hash *= 0x01000193
	}
	return fmt.Sprintf("%08x", hash)
}

func pplxSessionLookup(history []map[string]string) string {
	if len(history) == 0 {
		return ""
	}
	key := pplxSessionKey(history)
	pplxSessionMu.Lock()
	defer pplxSessionMu.Unlock()
	entry, ok := pplxSessionCache[key]
	if !ok {
		return ""
	}
	if time.Since(entry.TS) > pplxSessionMaxAge {
		delete(pplxSessionCache, key)
		return ""
	}
	return entry.UUID
}

func pplxSessionStore(history []map[string]string, current, responseText, backendUUID string) {
	if backendUUID == "" {
		return
	}
	full := append(append([]map[string]string{}, history...), map[string]string{"role": "user", "content": current}, map[string]string{"role": "assistant", "content": responseText})
	key := pplxSessionKey(full)
	pplxSessionMu.Lock()
	defer pplxSessionMu.Unlock()
	pplxSessionCache[key] = pplxSessionEntry{UUID: backendUUID, TS: time.Now()}
	if len(pplxSessionCache) > pplxSessionMaxN {
		var oldestKey string
		var oldest time.Time
		first := true
		for k, v := range pplxSessionCache {
			if first || v.TS.Before(oldest) {
				oldest = v.TS
				oldestKey = k
				first = false
			}
		}
		if oldestKey != "" {
			delete(pplxSessionCache, oldestKey)
		}
	}
}

var _ Adapter = (*perplexityWebAdapter)(nil)
