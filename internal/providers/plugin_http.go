package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

// pluginHTTPAdapter is an intentionally small out-of-process protocol. The
// plugin process receives canonical requests and can be upgraded independently
// from tproxy. Plugin execution must be enabled explicitly in security config.
type pluginHTTPAdapter struct{ client *http.Client }

type pluginExecuteRequest struct {
	Request  canonical.Request `json:"request"`
	Provider map[string]any    `json:"provider_config,omitempty"`
}

func (a *pluginHTTPAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	ctx = withCredentialProxy(ctx, credential)
	response, err := executeJSON(ctx, a.client, http.MethodPost, endpoint(provider.BaseURL, "/execute"), correlationHeaders(authHeaders(provider, credential), request.RequestID), pluginExecuteRequest{Request: request, Provider: provider.Config})
	if err != nil {
		return nil, &ProviderError{Code: "plugin_unavailable", Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, upstreamError(response)
	}
	var result canonical.Response
	if err = json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&result); err != nil {
		return nil, &ProviderError{Code: "plugin_protocol_error", Message: "plugin returned an invalid canonical response", Err: err}
	}
	return &result, nil
}

func (a *pluginHTTPAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	headers := correlationHeaders(authHeaders(provider, credential), request.RequestID)
	headers.Set("Accept", "text/event-stream, application/x-ndjson")
	response, err := executeJSON(ctx, a.client, http.MethodPost, endpoint(provider.BaseURL, "/stream"), headers, pluginExecuteRequest{Request: request, Provider: provider.Config})
	if err != nil {
		return nil, &ProviderError{Code: "plugin_unavailable", Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, upstreamError(response)
	}
	out := make(chan canonical.Event, 16)
	go func() {
		defer close(out)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "event:") {
				continue
			}
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if line == "[DONE]" {
				// Canonical consumers finalize usage and release stream capacity only
				// after an explicit terminal event.
				out <- canonical.Event{Type: canonical.EventMessageEnd}
				return
			}
			var payload struct {
				Type         canonical.EventType `json:"type"`
				ID           string              `json:"id"`
				Model        string              `json:"model"`
				Text         string              `json:"text"`
				Reasoning    string              `json:"reasoning"`
				ToolCall     map[string]any      `json:"tool_call"`
				Media        any                 `json:"media"`
				Usage        *canonical.Usage    `json:"usage"`
				FinishReason string              `json:"finish_reason"`
				Error        string              `json:"error"`
			}
			if json.Unmarshal([]byte(line), &payload) != nil {
				out <- canonical.Event{Type: canonical.EventError, Err: errors.New("plugin emitted invalid stream event")}
				return
			}
			event := canonical.Event{Type: payload.Type, ID: payload.ID, Model: payload.Model, Text: payload.Text, Reasoning: payload.Reasoning, ToolCall: payload.ToolCall, Media: payload.Media, Usage: payload.Usage, FinishReason: payload.FinishReason}
			if payload.Error != "" {
				event.Type = canonical.EventError
				event.Err = errors.New(payload.Error)
			}
			out <- event
			if event.Type == canonical.EventMessageEnd || event.Type == canonical.EventError {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			out <- canonical.Event{Type: canonical.EventError, Err: err}
		}
	}()
	return out, nil
}

type pluginRawRequest struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	ContentType string            `json:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodyBase64  string            `json:"body_base64,omitempty"`
}

type pluginRawResponse struct {
	Status      int               `json:"status"`
	ContentType string            `json:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodyBase64  string            `json:"body_base64,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
}

func (a *pluginHTTPAdapter) Proxy(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	ctx = withCredentialProxy(ctx, credential)
	method := rawRequest.Method
	if method == "" {
		method = http.MethodPost
	}
	forwardHeaders := map[string]string{}
	for _, name := range []string{"Idempotency-Key", "X-Request-ID"} {
		if value := rawRequest.Headers.Get(name); value != "" {
			forwardHeaders[name] = value
		}
	}
	body := pluginRawRequest{Method: method, Path: rawRequest.Path, ContentType: rawRequest.ContentType, Headers: forwardHeaders, BodyBase64: base64.StdEncoding.EncodeToString(rawRequest.Body)}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(provider.BaseURL, "/proxy"), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header = authHeaders(provider, credential)
	response, err := a.client.Do(req)
	if err != nil {
		return nil, &ProviderError{Code: "plugin_unavailable", Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, upstreamError(response)
	}
	var pluginResponse pluginRawResponse
	if err = json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&pluginResponse); err != nil {
		return nil, &ProviderError{Code: "plugin_protocol_error", Message: "plugin returned an invalid proxy response", Err: err}
	}
	decoded := []byte(pluginResponse.Body)
	if pluginResponse.BodyBase64 != "" {
		decoded, err = base64.StdEncoding.DecodeString(pluginResponse.BodyBase64)
		if err != nil {
			return nil, &ProviderError{Code: "plugin_protocol_error", Message: "plugin returned invalid base64 content", Err: err}
		}
	}
	status := pluginResponse.Status
	if status == 0 {
		status = http.StatusOK
	}
	headersOut := make(http.Header)
	for name, value := range pluginResponse.Headers {
		headersOut.Set(name, value)
	}
	return &RawResponse{Status: status, Headers: headersOut, Body: decoded, ContentType: pluginResponse.ContentType}, nil
}
