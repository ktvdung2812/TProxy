package providers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

// ClusterProviderType is the type (and id) of the provider whose credentials
// are the other nodes of a tproxy cluster. Requests are forwarded to the peer,
// which serves them through its own /v1 pipeline and accounts.
const ClusterProviderType = "tproxy-cluster"

const (
	clusterNodeMetadataKey         = "cluster_node_id"
	clusterServesModelsMetadataKey = "serves_models"
	clusterModelHeader             = "X-TProxy-Cluster-Model"
	clusterErrorCodeHeader         = "X-TProxy-Error-Code"
)

// ClusterTransport delivers req to the cluster node nodeID and returns its response unbuffered (caller closes Body).
// req.URL.Path is the target's local path (e.g. "/v1/chat/completions"), req.URL.RawQuery the query; the transport adds auth/relay headers.
// When the node is unreachable it may return a synthetic 503 JSON response with X-TProxy-Error-Code "cluster_node_offline" or "cluster_not_configured".
type ClusterTransport interface {
	Do(ctx context.Context, nodeID string, req *http.Request) (*http.Response, error)
}

// SetClusterTransport installs the transport used by the tproxy-cluster
// adapter. It is safe for concurrent use; nil disables forwarding, after which
// the adapter answers 503 cluster_not_configured.
func (r *Registry) SetClusterTransport(t ClusterTransport) {
	if r.cluster != nil {
		r.cluster.setTransport(t)
	}
}

type clusterPeerAdapter struct {
	mu        sync.RWMutex
	transport ClusterTransport
}

func (a *clusterPeerAdapter) setTransport(t ClusterTransport) {
	a.mu.Lock()
	a.transport = t
	a.mu.Unlock()
}

func (a *clusterPeerAdapter) currentTransport() ClusterTransport {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.transport
}

// clusterCall is a request in the wire protocol the peer is asked to serve.
type clusterCall struct {
	protocol canonical.Protocol
	path     string
	query    string
	body     map[string]any
}

var clusterProtocolPaths = map[canonical.Protocol]string{
	canonical.ProtocolOpenAI:    "/v1/chat/completions",
	canonical.ProtocolResponses: "/v1/responses",
	canonical.ProtocolClaude:    "/v1/messages",
}

var clusterRawResponseLimit int64 = 64 << 20

func clusterScopedValue(clientID, value string) string {
	sum := sha256.Sum256([]byte(clientID + "\x00" + value))
	return fmt.Sprintf("%x", sum)
}

// newClusterCall forwards the client's own payload to the peer endpoint of the
// same protocol, so the peer applies its own translation exactly once.
func newClusterCall(request canonical.Request) clusterCall {
	if len(request.Raw) > 0 {
		body := cloneAnyMap(request.Raw)
		switch request.Source {
		case canonical.ProtocolOpenAI, canonical.ProtocolResponses, canonical.ProtocolClaude:
			body["model"] = request.UpstreamModel
			body["stream"] = request.Stream
			return clusterCall{protocol: request.Source, path: clusterProtocolPaths[request.Source], body: body}
		case canonical.ProtocolGemini:
			delete(body, "model")
			action, query := "generateContent", ""
			if request.Stream {
				action, query = "streamGenerateContent", "alt=sse"
			}
			return clusterCall{protocol: canonical.ProtocolGemini, path: "/v1beta/models/" + request.UpstreamModel + ":" + action, query: query, body: body}
		}
	}
	body := buildOpenAIBody(request)
	body["stream"] = request.Stream
	return clusterCall{protocol: canonical.ProtocolOpenAI, path: "/v1/chat/completions", body: body}
}

func (a *clusterPeerAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	request.Stream = false
	call := newClusterCall(request)
	response, err := a.send(ctx, credential, request, call)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, clusterReadError(ctx, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "invalid_upstream_response", Message: "cluster peer returned an invalid response", Err: err}
	}
	// A peer that commits 200 early (non-stream keepalive) reports a later
	// failure as an error envelope in the body.
	if raw["error"] != nil {
		return nil, clusterFrameError(raw)
	}
	switch call.protocol {
	case canonical.ProtocolResponses:
		return clusterResponsesResponse(raw, request.UpstreamModel)
	case canonical.ProtocolClaude:
		return claudeMessageResponse(raw), nil
	case canonical.ProtocolGemini:
		return geminiContentResponse(raw, request.UpstreamModel), nil
	default:
		return openAIChatResponse(raw), nil
	}
}

func (a *clusterPeerAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	request.Stream = true
	call := newClusterCall(request)
	response, err := a.send(ctx, credential, request, call)
	if err != nil {
		return nil, err
	}
	body := clusterStreamBody(ctx, response.Body, call.protocol)
	switch call.protocol {
	case canonical.ProtocolResponses:
		return clusterResponsesStream(ctx, response.Header, body), nil
	case canonical.ProtocolClaude:
		return parseAnthropicStream(ctx, body, nil, true), nil
	case canonical.ProtocolGemini:
		return parseGeminiStream(ctx, body, request.UpstreamModel), nil
	default:
		return parseOpenAIStream(ctx, &http.Response{Body: body}), nil
	}
}

func (a *clusterPeerAdapter) Proxy(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	method := rawRequest.Method
	if method == "" {
		method = http.MethodPost
	}
	target, err := url.Parse(rawRequest.Path)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Err: err}
	}
	// RawPath keeps an escaped "/" inside an id (e.g. /v1/videos/{id}) intact.
	relative := &url.URL{Path: target.Path, RawPath: target.RawPath, RawQuery: target.RawQuery}
	if rawRequest.RawQuery != "" {
		relative.RawQuery = rawRequest.RawQuery
	}
	if relative.Path == "/v1/audio/voices" && rawRequest.UpstreamModel != "" {
		query := relative.Query()
		query.Set("model", rawRequest.UpstreamModel)
		relative.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, relative.String(), bytes.NewReader(rawRequest.Body))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Err: err}
	}
	if rawRequest.ContentType != "" {
		req.Header.Set("Content-Type", rawRequest.ContentType)
	}
	for _, name := range []string{"Idempotency-Key", "X-Request-ID"} {
		if value := rawRequest.Headers.Get(name); value != "" {
			if name == "Idempotency-Key" {
				value = clusterScopedValue(rawRequest.ClientID, value)
			}
			req.Header.Set(name, value)
		}
	}
	response, err := a.do(ctx, credential, req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, clusterRawResponseLimit+1))
	if err != nil {
		return nil, clusterReadError(ctx, err)
	}
	if int64(len(data)) > clusterRawResponseLimit {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "cluster_response_too_large", Message: "cluster peer response exceeds size limit"}
	}
	raw := &RawResponse{Status: response.StatusCode, Headers: response.Header.Clone(), Body: data, ContentType: response.Header.Get("Content-Type")}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return raw, clusterResponseError(response, data)
	}
	return raw, nil
}

// send dispatches call and maps any non-2xx answer to a ProviderError.
func (a *clusterPeerAdapter) send(ctx context.Context, credential store.Credential, request canonical.Request, call clusterCall) (*http.Response, error) {
	payload, err := json.Marshal(call.body)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, (&url.URL{Path: call.path, RawQuery: call.query}).String(), bytes.NewReader(payload))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if request.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	correlationHeaders(req.Header, request.RequestID)
	clientID, _ := request.Metadata["client_api_key_id"].(string)
	if sessionID := strings.TrimSpace(request.SessionID); sessionID != "" {
		req.Header.Set("X-Session-ID", clusterScopedValue(clientID, sessionID))
	}
	if parent, _ := request.Metadata[config.ParentSessionMetadataKey].(string); strings.TrimSpace(parent) != "" {
		req.Header.Set(config.ParentSessionHeader, clusterScopedValue(clientID, strings.TrimSpace(parent)))
	}
	client := clientHeadersFromRequest(request)
	for _, name := range []string{"anthropic-version", "anthropic-beta"} {
		if value := client[name]; value != "" {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set(clusterModelHeader, request.UpstreamModel)
	response, err := a.do(ctx, credential, req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, clusterResponseError(response, data)
	}
	return response, nil
}

func (a *clusterPeerAdapter) do(ctx context.Context, credential store.Credential, req *http.Request) (*http.Response, error) {
	nodeID := clusterNodeID(credential)
	if nodeID == "" {
		return nil, &ProviderError{Status: http.StatusServiceUnavailable, Code: "cluster_peer_unconfigured", Message: fmt.Sprintf("credential %s has no cluster node id", credential.ID)}
	}
	transport := a.currentTransport()
	if transport == nil {
		return nil, &ProviderError{Status: http.StatusServiceUnavailable, Code: "cluster_not_configured", Message: "this node is not connected to a cluster", RetryAfter: "15"}
	}
	response, err := transport.Do(ctx, nodeID, req)
	if err != nil {
		if ctx.Err() != nil {
			// The caller went away; that says nothing about the peer.
			return nil, &ProviderError{Code: "upstream_network", Err: err}
		}
		return nil, &ProviderError{Status: http.StatusServiceUnavailable, Code: "cluster_peer_unreachable", Message: fmt.Sprintf("cluster node %s is unreachable: %v", nodeID, err), RetryAfter: "15", Err: err}
	}
	return response, nil
}

func clusterReadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &ProviderError{Code: "upstream_network", Err: err}
	}
	return &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Err: err}
}

func clusterNodeID(credential store.Credential) string {
	return strings.TrimSpace(stringValue(credential.Metadata[clusterNodeMetadataKey]))
}

func clusterDiscoveredModels(credential store.Credential) []DiscoveredModel {
	var ids []string
	switch values := credential.Metadata[clusterServesModelsMetadataKey].(type) {
	case []string:
		ids = values
	case []any:
		for _, value := range values {
			ids = append(ids, stringValue(value))
		}
	}
	items := make([]DiscoveredModel, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			items = append(items, DiscoveredModel{ID: id, OwnedBy: ClusterProviderType})
		}
	}
	return items
}

func clusterResponsesResponse(raw map[string]any, model string) (*canonical.Response, error) {
	events := make(chan canonical.Event, 16)
	go func() {
		defer close(events)
		codexEventsFromJSON(events, raw, nil)
	}()
	result, err := collectStreamResponse(events, model)
	for range events {
	}
	if err != nil {
		return nil, err
	}
	result.Raw = raw
	return result, nil
}

// clusterResponsesStream relays Responses frames byte-exact. The passthrough
// parser wraps read errors as upstream_stream_error, which would hide the
// peer's mapped failure carried by the pipe, so it is unwrapped here.
func clusterResponsesStream(ctx context.Context, header http.Header, body io.ReadCloser) <-chan canonical.Event {
	parsed := make(chan canonical.Event, 16)
	go func() {
		defer close(parsed)
		defer body.Close()
		parseCodexResponsesPassthroughSSE(ctx, body, parsed)
	}()
	out := make(chan canonical.Event, 16)
	if headers := codexRelayableResponseHeaders(header); len(headers) > 0 {
		out <- canonical.Event{Type: canonical.EventUpstreamHeaders, UpstreamHeaders: headers}
	}
	go func() {
		defer close(out)
		for event := range parsed {
			if wrapped, ok := event.Err.(*ProviderError); ok && event.Type == canonical.EventError && wrapped.Code == "upstream_stream_error" {
				var peerErr *ProviderError
				if errors.As(wrapped.Err, &peerErr) {
					event.Err = peerErr
				}
			}
			select {
			case out <- event:
			case <-ctx.Done():
				for range parsed {
				}
				return
			}
		}
	}()
	return out
}

// clusterStreamBody copies a peer's SSE body through to the protocol parser.
// A peer that fails after answering 200 reports it as an error frame, which
// the stock parsers would read as an empty success; the frame is withheld and
// the pipe is closed with the mapped error, which the parser then emits as
// EventError.
func clusterStreamBody(ctx context.Context, body io.ReadCloser, protocol canonical.Protocol) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer body.Close()
		stop := context.AfterFunc(ctx, func() { _ = body.Close() })
		defer stop()
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		event := ""
		var line []byte
		for scanner.Scan() {
			trimmed := bytes.TrimSpace(scanner.Bytes())
			switch {
			case len(trimmed) == 0:
				event = ""
			case bytes.HasPrefix(trimmed, []byte("event:")):
				event = string(bytes.TrimSpace(trimmed[len("event:"):]))
			case bytes.HasPrefix(trimmed, []byte("data:")):
				if failure := clusterStreamFailure(protocol, event, bytes.TrimSpace(trimmed[len("data:"):])); failure != nil {
					_ = writer.CloseWithError(failure)
					return
				}
			}
			line = append(append(line[:0], scanner.Bytes()...), '\n')
			if _, err := writer.Write(line); err != nil {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = writer.CloseWithError(&ProviderError{Status: http.StatusBadGateway, Code: "upstream_stream_error", Message: "cluster peer stream failed", Err: err})
			return
		}
		_ = writer.Close()
	}()
	return reader
}

// clusterStreamFailure reports whether an SSE data frame is a peer error:
// `event: error` (Claude, Responses), a Responses response.failed, or a data
// object carrying a top-level "error" (OpenAI and tproxy envelopes).
func clusterStreamFailure(protocol canonical.Protocol, event string, data []byte) *ProviderError {
	named := event == "error" || (protocol == canonical.ProtocolResponses && event == "response.failed")
	if !named && !bytes.Contains(data, []byte(`"error"`)) && !bytes.Contains(data, []byte(`"response.failed"`)) {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		if named {
			return clusterPeerError(http.StatusBadGateway, "", string(data), "")
		}
		return nil
	}
	kind := stringValue(raw["type"])
	if named || kind == "error" || raw["error"] != nil || (protocol == canonical.ProtocolResponses && kind == "response.failed") {
		return clusterFrameError(raw)
	}
	return nil
}

func clusterResponseError(response *http.Response, data []byte) *ProviderError {
	code := strings.TrimSpace(response.Header.Get(clusterErrorCodeHeader))
	message := strings.TrimSpace(string(data))
	var raw map[string]any
	if json.Unmarshal(data, &raw) == nil {
		_, bodyCode, bodyMessage := clusterErrorFields(raw)
		if code == "" {
			code = bodyCode
		}
		if bodyMessage != "" {
			message = bodyMessage
		}
	}
	return clusterPeerError(response.StatusCode, code, message, strings.TrimSpace(response.Header.Get("Retry-After")))
}

// clusterFrameError maps an error a peer reported after answering 200, where
// only the code survives and the status has to be inferred from it.
func clusterFrameError(raw map[string]any) *ProviderError {
	status, code, message := clusterErrorFields(raw)
	if status == 0 {
		status = clusterFrameStatus(code, message)
	}
	return clusterPeerError(status, code, message, codexFailedRetryAfter(code, message, codexFailedErrorPayload(raw)))
}

// clusterErrorFields reads tproxy's {"error":{"code","message"}}, Claude's
// {"type":"error","error":{"type","message"}}, Responses failures and Google's
// numeric {"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}.
func clusterErrorFields(raw map[string]any) (status int, code, message string) {
	payload := codexFailedErrorPayload(raw)
	code, message = codexFailedError(raw)
	if numeric := numberValue(payload["code"]); numeric >= 400 && numeric <= 599 {
		status, code = numeric, stringValue(payload["status"])
	}
	if code == "" {
		code = stringValue(payload["type"])
	}
	if text, ok := raw["error"].(string); ok && message == "" {
		message = text
	}
	return status, strings.TrimSpace(code), strings.TrimSpace(message)
}

func clusterFrameStatus(code, message string) int {
	if isModelAtCapacity(code, message) {
		return http.StatusTooManyRequests
	}
	switch strings.ToLower(code) {
	case "cluster_unauthorized", "invalid_api_key", "missing_api_key":
		return http.StatusUnauthorized
	case "cluster_node_revoked":
		return http.StatusForbidden
	case "model_forbidden", "endpoint_forbidden", "cluster_lending_disabled", "cluster_model_not_shared", "cluster_model_not_lent", "model_not_found", "not_found_error":
		return http.StatusNotFound
	case "invalid_request", "invalid_request_error", "context_length_exceeded", "invalid_prompt", "bio_policy", "cyber_policy", "misalignment_policy_violation":
		return http.StatusBadRequest
	case "request_too_large":
		return http.StatusRequestEntityTooLarge
	case "upstream_rate_limited", "rate_limit_exceeded", "rate_limit_error", "slow_down", "provider_limit_exceeded", "concurrency_limit_exceeded", "upstream_quota_exceeded", "insufficient_quota":
		return http.StatusTooManyRequests
	}
	return http.StatusBadGateway
}

// clusterPeerError maps a peer failure onto what the router should do with
// the peer credential: 401/403 link failures bench the whole peer, model
// failures bench the (peer, model) pair, and capacity failures back off
// briefly so the request falls through to the next route.
func clusterPeerError(status int, code, message, retryAfter string) *ProviderError {
	code = strings.TrimSpace(code)
	message = security.RedactText(strings.TrimSpace(message))
	if message == "" {
		message = fmt.Sprintf("cluster peer answered HTTP %d", status)
	}
	switch {
	case (status == http.StatusUnauthorized || status == http.StatusForbidden) && clusterLinkCode(code):
		return &ProviderError{Status: status, Code: code, Message: message}
	case status == http.StatusNotFound || (status == http.StatusForbidden && clusterModelCode(code)):
		return &ProviderError{Status: http.StatusNotFound, Code: "cluster_model_unavailable", Message: message}
	case status == http.StatusPaymentRequired:
		return &ProviderError{Status: http.StatusServiceUnavailable, Code: "cluster_peer_no_capacity", Message: message, RetryAfter: "30"}
	case status == http.StatusTooManyRequests:
		if isModelAtCapacity(code, message) {
			// The router benches a whole provider/model pair on the capacity
			// code, which for tproxy-cluster would take every peer out at once.
			code = "cluster_peer_no_capacity"
		}
		if code == "" {
			code = store.ErrorCode(status)
		}
		if retryAfter == "" {
			retryAfter = "15"
		}
		return &ProviderError{Status: status, Code: code, Message: message, RetryAfter: retryAfter}
	case status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout:
		if code == "cluster_node_offline" || code == "cluster_not_configured" {
			return &ProviderError{Status: http.StatusServiceUnavailable, Code: code, Message: message, RetryAfter: "15"}
		}
		return &ProviderError{Status: http.StatusServiceUnavailable, Code: "cluster_peer_no_capacity", Message: message, RetryAfter: "30"}
	}
	if code == "" {
		code = store.ErrorCode(status)
	}
	return &ProviderError{Status: status, Code: code, Message: message, RetryAfter: retryAfter}
}

func clusterLinkCode(code string) bool {
	switch code {
	case "cluster_unauthorized", "cluster_node_revoked", "invalid_api_key", "missing_api_key":
		return true
	}
	return false
}

// clusterModelCode reports a refusal that concerns the requested model, not
// the peer: a lender that does not lend it (cluster_model_not_lent) or whose
// relay principal may not use it (model_not_found, which /v1 answers with 403
// for a forbidden model).
func clusterModelCode(code string) bool {
	switch code {
	case "model_forbidden", "endpoint_forbidden", "cluster_lending_disabled", "cluster_model_not_shared", "cluster_model_not_lent", "model_not_found":
		return true
	}
	return false
}

// pingClusterJSON is pingJSON for a cluster peer: the probe travels over the
// cluster transport, never to the provider's base URL.
func (r *Registry) pingClusterJSON(ctx context.Context, provider store.Provider, credential store.Credential, path string, body []byte, start time.Time, validate func(map[string]any) bool, emptyMessage string) PingResult {
	if r.cluster == nil {
		return failPing(start, http.StatusServiceUnavailable, "this node is not connected to a cluster")
	}
	raw, err := r.cluster.Proxy(ctx, provider, credential, RawRequest{Method: http.MethodPost, Path: path, Body: body, ContentType: "application/json"})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return PingResult{OK: false, LatencyMS: latency, Error: truncatePingError(err.Error()), Status: Status(err)}
	}
	var payload map[string]any
	if json.Unmarshal(raw.Body, &payload) != nil {
		return PingResult{OK: false, LatencyMS: latency, Error: "Provider returned invalid JSON", Status: raw.Status}
	}
	if !validate(payload) {
		return PingResult{OK: false, LatencyMS: latency, Error: emptyMessage, Status: raw.Status}
	}
	return PingResult{OK: true, LatencyMS: latency, Status: raw.Status}
}
