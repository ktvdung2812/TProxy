package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

type clusterPeerCall struct {
	method  string
	path    string
	escaped string
	query   string
	header  http.Header
	body    map[string]any
	raw     []byte
}

// clusterTestPeer is an httptest server standing in for the peer node, reached
// through a ClusterTransport that rewrites the relative request onto it.
type clusterTestPeer struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []clusterPeerCall
	nodes  []string
	err    error
}

func newClusterTestPeer(t *testing.T, respond http.HandlerFunc) *clusterTestPeer {
	t.Helper()
	peer := &clusterTestPeer{}
	peer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		call := clusterPeerCall{method: r.Method, path: r.URL.Path, escaped: r.URL.EscapedPath(), query: r.URL.RawQuery, header: r.Header.Clone(), raw: raw}
		_ = json.Unmarshal(raw, &call.body)
		peer.mu.Lock()
		peer.calls = append(peer.calls, call)
		peer.mu.Unlock()
		respond(w, r)
	}))
	t.Cleanup(peer.server.Close)
	return peer
}

func (p *clusterTestPeer) Do(ctx context.Context, nodeID string, req *http.Request) (*http.Response, error) {
	p.mu.Lock()
	p.nodes = append(p.nodes, nodeID)
	err := p.err
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	target, _ := url.Parse(p.server.URL)
	forwarded := req.Clone(ctx)
	forwarded.URL.Scheme, forwarded.URL.Host, forwarded.Host = target.Scheme, target.Host, target.Host
	return http.DefaultTransport.RoundTrip(forwarded)
}

func (p *clusterTestPeer) lastCall(t *testing.T) clusterPeerCall {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		t.Fatal("peer received no request")
	}
	return p.calls[len(p.calls)-1]
}

func clusterTestTarget() (store.Provider, store.Credential) {
	return store.Provider{ID: ClusterProviderType, Type: ClusterProviderType, Enabled: true},
		store.Credential{ID: "cluster-node-node-b", AuthType: "none", Enabled: true, Metadata: map[string]any{"cluster_node_id": "node-b", "serves_models": []any{"peer-model"}}}
}

func clusterTestAdapter(t *testing.T, transport ClusterTransport) Adapter {
	t.Helper()
	registry := NewRegistry()
	registry.SetClusterTransport(transport)
	adapter, err := registry.Adapter(ClusterProviderType)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func clusterCollect(t *testing.T, events <-chan canonical.Event) []canonical.Event {
	t.Helper()
	var collected []canonical.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return collected
			}
			collected = append(collected, event)
		case <-timeout:
			t.Fatalf("stream did not finish; got %#v", collected)
		}
	}
}

func writeSSE(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, body)
}

func writePeerJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func assertProviderError(t *testing.T, err error, status int, code, retryAfter string) {
	t.Helper()
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %#v, want *ProviderError", err)
	}
	if providerErr.Status != status || providerErr.Code != code || providerErr.RetryAfter != retryAfter {
		t.Fatalf("error = status %d code %q retry-after %q (%s), want %d %q %q", providerErr.Status, providerErr.Code, providerErr.RetryAfter, providerErr.Message, status, code, retryAfter)
	}
}

func TestClusterPeerExecuteForwardsEachProtocolNatively(t *testing.T) {
	cases := []struct {
		name    string
		source  canonical.Protocol
		raw     map[string]any
		path    string
		answer  string
		content string
		input   int
		output  int
	}{
		{
			name: "openai", source: canonical.ProtocolOpenAI, path: "/v1/chat/completions",
			raw:     map[string]any{"model": "client-alias", "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "temperature": 0.2, "stream": true},
			answer:  `{"id":"chatcmpl-1","model":"peer-model","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
			content: "hello", input: 3, output: 2,
		},
		{
			name: "responses", source: canonical.ProtocolResponses, path: "/v1/responses",
			raw:     map[string]any{"model": "client-alias", "input": "hi", "instructions": "be brief"},
			answer:  `{"id":"resp_1","object":"response","model":"peer-model","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":5,"output_tokens":2}}`,
			content: "hello", input: 5, output: 2,
		},
		{
			name: "claude", source: canonical.ProtocolClaude, path: "/v1/messages",
			raw:     map[string]any{"model": "client-alias", "max_tokens": 64, "messages": []any{map[string]any{"role": "user", "content": "hi"}}},
			answer:  `{"id":"msg_1","type":"message","model":"peer-model","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":1}}`,
			content: "hello", input: 4, output: 1,
		},
		{
			name: "gemini", source: canonical.ProtocolGemini, path: "/v1beta/models/peer-model:generateContent",
			raw:     map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}}},
			answer:  `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}`,
			content: "hello", input: 2, output: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) { writePeerJSON(w, http.StatusOK, tc.answer) })
			provider, credential := clusterTestTarget()
			response, err := clusterTestAdapter(t, peer).Execute(context.Background(), provider, credential, canonical.Request{RequestID: "req-1", Source: tc.source, UpstreamModel: "peer-model", Raw: tc.raw})
			if err != nil {
				t.Fatal(err)
			}
			call := peer.lastCall(t)
			if call.method != http.MethodPost || call.path != tc.path || call.query != "" {
				t.Fatalf("peer request = %s %s?%s, want POST %s", call.method, call.path, call.query, tc.path)
			}
			if tc.source == canonical.ProtocolGemini {
				if _, ok := call.body["model"]; ok {
					t.Fatalf("gemini body carries model: %v", call.body)
				}
			} else if call.body["model"] != "peer-model" || call.body["stream"] != false {
				t.Fatalf("body model/stream = %v/%v", call.body["model"], call.body["stream"])
			}
			for key := range tc.raw {
				if _, ok := call.body[key]; !ok && key != "model" {
					t.Fatalf("raw field %q was not forwarded: %v", key, call.body)
				}
			}
			if peer.nodes[0] != "node-b" {
				t.Fatalf("transport node = %q", peer.nodes[0])
			}
			if response.Content != tc.content || response.Usage.InputTokens != tc.input || response.Usage.OutputTokens != tc.output {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}

func TestClusterPeerExecuteStreamParsesEachProtocol(t *testing.T) {
	cases := []struct {
		name   string
		source canonical.Protocol
		raw    map[string]any
		path   string
		query  string
		stream string
		text   string
		finish string
	}{
		{
			name: "openai", source: canonical.ProtocolOpenAI, path: "/v1/chat/completions",
			raw: map[string]any{"messages": []any{}},
			stream: "data: {\"id\":\"c1\",\"model\":\"peer-model\",\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n" +
				"data: [DONE]\n\n",
			text: "hello", finish: "stop",
		},
		{
			name: "claude", source: canonical.ProtocolClaude, path: "/v1/messages",
			raw: map[string]any{"messages": []any{}, "max_tokens": 16},
			stream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"peer-model\",\"usage\":{\"input_tokens\":3}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			text: "hello", finish: "end_turn",
		},
		{
			name: "gemini", source: canonical.ProtocolGemini, path: "/v1beta/models/peer-model:streamGenerateContent", query: "alt=sse",
			raw: map[string]any{"contents": []any{}},
			stream: "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2}}\n\n",
			text: "hello", finish: "STOP",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) { writeSSE(w, tc.stream) })
			provider, credential := clusterTestTarget()
			events, err := clusterTestAdapter(t, peer).ExecuteStream(context.Background(), provider, credential, canonical.Request{RequestID: "req-1", Source: tc.source, UpstreamModel: "peer-model", Raw: tc.raw})
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			var last canonical.Event
			var usage *canonical.Usage
			for _, event := range clusterCollect(t, events) {
				if event.Type == canonical.EventError {
					t.Fatalf("unexpected error event: %v", event.Err)
				}
				if event.Type == canonical.EventUsage {
					usage = event.Usage
				}
				text.WriteString(event.Text)
				last = event
			}
			if text.String() != tc.text || last.Type != canonical.EventMessageEnd || last.FinishReason != tc.finish {
				t.Fatalf("text = %q, last = %#v", text.String(), last)
			}
			if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 2 {
				t.Fatalf("usage = %#v", usage)
			}
			call := peer.lastCall(t)
			if call.path != tc.path || call.query != tc.query {
				t.Fatalf("peer request = %s?%s, want %s?%s", call.path, call.query, tc.path, tc.query)
			}
			if call.header.Get("Accept") != "text/event-stream" {
				t.Fatalf("Accept = %q", call.header.Get("Accept"))
			}
			if tc.source != canonical.ProtocolGemini && call.body["stream"] != true {
				t.Fatalf("body stream = %v", call.body["stream"])
			}
		})
	}
}

func TestClusterPeerResponsesStreamIsByteExact(t *testing.T) {
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1",  "model":"peer-model"}}`,
		`{"type":"response.output_text.delta","delta":"hello" ,"item_id":"msg_1"}`,
		`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":4,"output_tokens":2}}}`,
	}
	names := []string{"response.created", "response.output_text.delta", "response.completed"}
	var stream strings.Builder
	for index, frame := range frames {
		stream.WriteString("event: " + names[index] + "\ndata: " + frame + "\n\n")
	}
	stream.WriteString("data: [DONE]\n\n")
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Codex-Turn-State", "turn-1")
		writeSSE(w, stream.String())
	})
	provider, credential := clusterTestTarget()
	events, err := clusterTestAdapter(t, peer).ExecuteStream(context.Background(), provider, credential, canonical.Request{Source: canonical.ProtocolResponses, UpstreamModel: "peer-model", Raw: map[string]any{"input": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	collected := clusterCollect(t, events)
	if collected[0].Type != canonical.EventUpstreamHeaders || collected[0].UpstreamHeaders["x-codex-turn-state"] != "turn-1" {
		t.Fatalf("first event = %#v", collected[0])
	}
	var passthrough []canonical.Event
	var usage *canonical.Usage
	for _, event := range collected[1:] {
		switch event.Type {
		case canonical.EventResponsesSSE:
			passthrough = append(passthrough, event)
		case canonical.EventUsage:
			usage = event.Usage
		default:
			t.Fatalf("unexpected event %#v", event)
		}
	}
	if len(passthrough) != len(frames) {
		t.Fatalf("passthrough frames = %d, want %d", len(passthrough), len(frames))
	}
	for index, event := range passthrough {
		if event.SSEEvent != names[index] || string(event.SSEData) != frames[index] {
			t.Fatalf("frame %d = %s %q, want %s %q", index, event.SSEEvent, event.SSEData, names[index], frames[index])
		}
	}
	if usage == nil || usage.InputTokens != 4 || usage.OutputTokens != 2 {
		t.Fatalf("usage = %#v", usage)
	}
	if call := peer.lastCall(t); call.path != "/v1/responses" || call.body["stream"] != true || call.body["model"] != "peer-model" {
		t.Fatalf("peer request = %s %v", call.path, call.body)
	}
}

func TestClusterPeerMidStreamErrorFramesBecomeEventErrors(t *testing.T) {
	cases := []struct {
		name       string
		source     canonical.Protocol
		stream     string
		status     int
		code       string
		retryAfter string
		before     int
	}{
		{
			name: "openai error envelope then DONE", source: canonical.ProtocolOpenAI,
			stream: "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n" +
				"data: {\"error\":{\"type\":\"provider_error\",\"code\":\"upstream_rate_limited\",\"message\":\"slow down\",\"request_id\":\"r\"}}\n\n" +
				"data: [DONE]\n\n",
			status: 429, code: "upstream_rate_limited", retryAfter: "15", before: 2,
		},
		{
			name: "openai generic stream error", source: canonical.ProtocolOpenAI,
			stream: "data: {\"error\":{\"type\":\"provider_error\",\"code\":\"stream_error\",\"message\":\"boom\"}}\n\ndata: [DONE]\n\n",
			status: 503, code: "cluster_peer_no_capacity", retryAfter: "30",
		},
		{
			name: "claude event error", source: canonical.ProtocolClaude,
			stream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"peer-model\"}}\n\n" +
				"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n",
			status: 503, code: "cluster_peer_no_capacity", retryAfter: "30", before: 1,
		},
		{
			name: "responses response.failed", source: canonical.ProtocolResponses,
			stream: "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
				"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"Rate limit reached. Please try again in 2s.\"}}}\n\n",
			status: 429, code: "rate_limit_exceeded", retryAfter: "2", before: 1,
		},
		{
			name: "responses at-capacity does not name the provider-wide code", source: canonical.ProtocolResponses,
			stream: "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"Selected model is at capacity. Please try a different model.\"}}}\n\n",
			status: 429, code: "cluster_peer_no_capacity", retryAfter: "15",
		},
		{
			name: "responses error event", source: canonical.ProtocolResponses,
			stream: "event: error\ndata: {\"error\":{\"type\":\"api_error\",\"message\":\"stream broke\"}}\n\ndata: [DONE]\n\n",
			status: 503, code: "cluster_peer_no_capacity", retryAfter: "30",
		},
		{
			name: "gemini google error", source: canonical.ProtocolGemini,
			stream: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"he\"}]}}]}\n\n" +
				"data: {\"error\":{\"code\":429,\"message\":\"quota\",\"status\":\"RESOURCE_EXHAUSTED\"}}\n\n",
			status: 429, code: "RESOURCE_EXHAUSTED", retryAfter: "15", before: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) { writeSSE(w, tc.stream) })
			provider, credential := clusterTestTarget()
			events, err := clusterTestAdapter(t, peer).ExecuteStream(context.Background(), provider, credential, canonical.Request{Source: tc.source, UpstreamModel: "peer-model", Raw: map[string]any{"input": "hi"}})
			if err != nil {
				t.Fatal(err)
			}
			collected := clusterCollect(t, events)
			if len(collected) == 0 {
				t.Fatal("no events")
			}
			last := collected[len(collected)-1]
			if last.Type != canonical.EventError {
				t.Fatalf("last event = %#v, want error; all = %#v", last, collected)
			}
			assertProviderError(t, last.Err, tc.status, tc.code, tc.retryAfter)
			var before []canonical.Event
			for _, event := range collected[:len(collected)-1] {
				if event.Type == canonical.EventMessageEnd || event.Type == canonical.EventError {
					t.Fatalf("terminal event before the error: %#v", event)
				}
				if event.Type == canonical.EventResponsesSSE && event.SSEEvent == "response.failed" {
					t.Fatalf("response.failed was passed through: %#v", event)
				}
				before = append(before, event)
			}
			if len(before) != tc.before {
				t.Fatalf("events before the error = %#v, want %d", before, tc.before)
			}
		})
	}
}

func TestClusterPeerErrorMapping(t *testing.T) {
	cases := []struct {
		status     int
		header     string
		body       string
		retryAfter string
		wantStatus int
		wantCode   string
		wantRetry  string
	}{
		{status: 401, header: "cluster_unauthorized", wantStatus: 401, wantCode: "cluster_unauthorized"},
		{status: 403, body: `{"error":{"type":"provider_error","code":"cluster_node_revoked","message":"revoked"}}`, wantStatus: 403, wantCode: "cluster_node_revoked"},
		{status: 401, header: "invalid_api_key", wantStatus: 401, wantCode: "invalid_api_key"},
		{status: 401, header: "missing_api_key", wantStatus: 401, wantCode: "missing_api_key"},
		{status: 403, header: "model_forbidden", wantStatus: 404, wantCode: "cluster_model_unavailable"},
		{status: 403, header: "endpoint_forbidden", wantStatus: 404, wantCode: "cluster_model_unavailable"},
		{status: 403, header: "cluster_lending_disabled", wantStatus: 404, wantCode: "cluster_model_unavailable"},
		{status: 403, header: "cluster_model_not_shared", wantStatus: 404, wantCode: "cluster_model_unavailable"},
		// A lender that does not lend the model, or whose relay principal may
		// not use it, refuses that model only; the peer stays in rotation.
		{status: 403, header: "cluster_model_not_lent", wantStatus: 404, wantCode: "cluster_model_unavailable"},
		{status: 403, header: "model_not_found", wantStatus: 404, wantCode: "cluster_model_unavailable"},
		{status: 404, header: "model_not_found", wantStatus: 404, wantCode: "cluster_model_unavailable"},
		{status: 404, body: "not here", wantStatus: 404, wantCode: "cluster_model_unavailable"},
		{status: 402, header: "budget_exceeded", wantStatus: 503, wantCode: "cluster_peer_no_capacity", wantRetry: "30"},
		{status: 429, header: "upstream_rate_limited", retryAfter: "7", wantStatus: 429, wantCode: "upstream_rate_limited", wantRetry: "7"},
		{status: 429, header: "concurrency_limit_exceeded", wantStatus: 429, wantCode: "concurrency_limit_exceeded", wantRetry: "15"},
		{status: 429, header: CodeUpstreamModelAtCapacity, wantStatus: 429, wantCode: "cluster_peer_no_capacity", wantRetry: "15"},
		{status: 502, header: "upstream_server_error", wantStatus: 503, wantCode: "cluster_peer_no_capacity", wantRetry: "30"},
		{status: 503, header: "no_available_credential", retryAfter: "99", wantStatus: 503, wantCode: "cluster_peer_no_capacity", wantRetry: "30"},
		{status: 504, header: "upstream_timeout", wantStatus: 503, wantCode: "cluster_peer_no_capacity", wantRetry: "30"},
		{status: 503, header: "cluster_node_offline", wantStatus: 503, wantCode: "cluster_node_offline", wantRetry: "15"},
		{status: 503, body: `{"error":{"code":"cluster_not_configured","message":"no hub"}}`, wantStatus: 503, wantCode: "cluster_not_configured", wantRetry: "15"},
		{status: 400, header: "invalid_request", wantStatus: 400, wantCode: "invalid_request"},
		{status: 413, header: "request_too_large", wantStatus: 413, wantCode: "request_too_large"},
		{status: 422, body: `{"error":{"message":"bad schema"}}`, wantStatus: 422, wantCode: "provider_error"},
	}
	for _, tc := range cases {
		name := tc.header
		if name == "" {
			name = tc.body
		}
		t.Run(strings.Join([]string{http.StatusText(tc.status), name}, " "), func(t *testing.T) {
			peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("X-TProxy-Error-Code", tc.header)
				}
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				body := tc.body
				if body == "" {
					body = `{"error":{"type":"provider_error","code":"` + tc.header + `","message":"peer said no","request_id":"r"}}`
				}
				writePeerJSON(w, tc.status, body)
			})
			provider, credential := clusterTestTarget()
			adapter := clusterTestAdapter(t, peer)
			request := canonical.Request{Source: canonical.ProtocolOpenAI, UpstreamModel: "peer-model", Raw: map[string]any{"messages": []any{}}}
			_, err := adapter.Execute(context.Background(), provider, credential, request)
			assertProviderError(t, err, tc.wantStatus, tc.wantCode, tc.wantRetry)
			if Status(err) != 0 && strings.Contains(tc.body, "bad schema") && !strings.Contains(err.Error(), "bad schema") {
				t.Fatalf("peer message was dropped: %v", err)
			}
			_, err = adapter.ExecuteStream(context.Background(), provider, credential, request)
			assertProviderError(t, err, tc.wantStatus, tc.wantCode, tc.wantRetry)
		})
	}
}

func TestClusterPeerTransportFailures(t *testing.T) {
	provider, credential := clusterTestTarget()
	request := canonical.Request{Source: canonical.ProtocolOpenAI, UpstreamModel: "peer-model", Raw: map[string]any{"messages": []any{}}}

	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("peer must not be reached") })
	peer.err = errors.New("websocket closed")
	_, err := clusterTestAdapter(t, peer).Execute(context.Background(), provider, credential, request)
	assertProviderError(t, err, 503, "cluster_peer_unreachable", "15")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = clusterTestAdapter(t, peer).Execute(ctx, provider, credential, request)
	if Status(err) != 0 {
		t.Fatalf("a cancelled caller must not look like a peer failure: %v", err)
	}

	for _, adapter := range []Adapter{clusterTestAdapter(t, nil), NewRegistry().items[ClusterProviderType]} {
		_, err = adapter.Execute(context.Background(), provider, credential, request)
		assertProviderError(t, err, 503, "cluster_not_configured", "15")
		_, err = adapter.ExecuteStream(context.Background(), provider, credential, request)
		assertProviderError(t, err, 503, "cluster_not_configured", "15")
		_, err = adapter.(RawProxyAdapter).Proxy(context.Background(), provider, credential, RawRequest{Path: "/v1/embeddings"})
		assertProviderError(t, err, 503, "cluster_not_configured", "15")
	}

	unconfigured := credential
	unconfigured.Metadata = map[string]any{}
	_, err = clusterTestAdapter(t, peer).Execute(context.Background(), provider, unconfigured, request)
	assertProviderError(t, err, 503, "cluster_peer_unconfigured", "")
}

func TestClusterPeerSetTransportCanDisableAndToleratesBareRegistry(t *testing.T) {
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		writePeerJSON(w, http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`)
	})
	registry := NewRegistry()
	registry.SetClusterTransport(peer)
	adapter, _ := registry.Adapter(ClusterProviderType)
	provider, credential := clusterTestTarget()
	request := canonical.Request{Source: canonical.ProtocolOpenAI, UpstreamModel: "peer-model", Raw: map[string]any{"messages": []any{}}}
	if _, err := adapter.Execute(context.Background(), provider, credential, request); err != nil {
		t.Fatal(err)
	}
	registry.SetClusterTransport(nil)
	_, err := adapter.Execute(context.Background(), provider, credential, request)
	assertProviderError(t, err, 503, "cluster_not_configured", "15")
	(&Registry{}).SetClusterTransport(peer)
}

func TestClusterPeerNonStreamErrorEnvelopeAfter200(t *testing.T) {
	// A peer with non-stream keepalive commits 200 before the router fails.
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		writePeerJSON(w, http.StatusOK, "\n\n{\"error\":{\"type\":\"provider_error\",\"code\":\"no_available_credential\",\"message\":\"nothing left\"}}")
	})
	provider, credential := clusterTestTarget()
	_, err := clusterTestAdapter(t, peer).Execute(context.Background(), provider, credential, canonical.Request{Source: canonical.ProtocolClaude, UpstreamModel: "peer-model", Raw: map[string]any{"messages": []any{}}})
	assertProviderError(t, err, 503, "cluster_peer_no_capacity", "30")
	if !strings.Contains(err.Error(), "nothing left") {
		t.Fatalf("message = %v", err)
	}
}

func TestClusterPeerModelNotLentFrameIsModelLevel(t *testing.T) {
	// The same refusal reported after a committed 200 must not read as a
	// link failure either.
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		writePeerJSON(w, http.StatusOK, `{"error":{"type":"cluster_error","code":"cluster_model_not_lent","message":"this node does not lend peer-model"}}`)
	})
	provider, credential := clusterTestTarget()
	_, err := clusterTestAdapter(t, peer).Execute(context.Background(), provider, credential, canonical.Request{Source: canonical.ProtocolOpenAI, UpstreamModel: "peer-model", Raw: map[string]any{"messages": []any{}}})
	assertProviderError(t, err, 404, "cluster_model_unavailable", "")
}

func TestClusterPeerHeadersForwardedAndStripped(t *testing.T) {
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		writePeerJSON(w, http.StatusOK, `{"id":"msg","content":[{"type":"text","text":"ok"}]}`)
	})
	provider, credential := clusterTestTarget()
	provider.Headers = map[string]string{"Authorization": "Bearer provider-secret"}
	credential.Secret = "credential-secret"
	request := canonical.Request{
		RequestID: "req-42", SessionID: "sess-1", Source: canonical.ProtocolClaude, UpstreamModel: "peer-model",
		Raw: map[string]any{"messages": []any{}},
		Metadata: map[string]any{
			config.ParentSessionMetadataKey: "parent-1",
			"client_headers": map[string]string{
				"anthropic-version": "2023-06-01", "anthropic-beta": "tools-2024-04-04",
				"authorization": "Bearer client-key", "x-tproxy-route": "elsewhere", "x-api-key": "client-key",
			},
		},
	}
	if _, err := clusterTestAdapter(t, peer).Execute(context.Background(), provider, credential, request); err != nil {
		t.Fatal(err)
	}
	header := peer.lastCall(t).header
	want := map[string]string{
		"Content-Type": "application/json", "Accept": "application/json", "X-Request-ID": "req-42", "X-Session-ID": clusterScopedValue("", "sess-1"),
		"X-Parent-Session-ID": clusterScopedValue("", "parent-1"), "anthropic-version": "2023-06-01", "anthropic-beta": "tools-2024-04-04", "X-TProxy-Cluster-Model": "peer-model",
	}
	for name, value := range want {
		if header.Get(name) != value {
			t.Fatalf("%s = %q, want %q (headers %v)", name, header.Get(name), value, header)
		}
	}
	for name := range header {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "x-api-key" || (strings.HasPrefix(lower, "x-tproxy-") && lower != "x-tproxy-cluster-model") {
			t.Fatalf("header %s leaked to the peer: %v", name, header)
		}
	}
}

func TestClusterPeerRawNilFallsBackToOpenAIChat(t *testing.T) {
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		writePeerJSON(w, http.StatusOK, `{"id":"c","model":"peer-model","choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`)
	})
	provider, credential := clusterTestTarget()
	temperature := 0.5
	response, err := clusterTestAdapter(t, peer).Execute(context.Background(), provider, credential, canonical.Request{
		Source: canonical.ProtocolClaude, UpstreamModel: "peer-model", MaxTokens: 16, Temperature: &temperature,
		System: "be terse", Messages: []canonical.Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "pong" {
		t.Fatalf("content = %v", response.Content)
	}
	call := peer.lastCall(t)
	messages, _ := call.body["messages"].([]any)
	if call.path != "/v1/chat/completions" || call.body["model"] != "peer-model" || call.body["stream"] != false || call.body["max_tokens"] != float64(16) || len(messages) != 2 {
		t.Fatalf("fallback request = %s %v", call.path, call.body)
	}
}

func TestClusterPeerProxyForwardsRawRequests(t *testing.T) {
	status := http.StatusOK
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.Header().Set("X-TProxy-Error-Code", "upstream_rate_limited")
			writePeerJSON(w, status, `{"error":{"code":"upstream_rate_limited","message":"later"}}`)
			return
		}
		writePeerJSON(w, http.StatusOK, `{"data":[{"url":"https://img"}]}`)
	})
	provider, credential := clusterTestTarget()
	adapter := clusterTestAdapter(t, peer).(RawProxyAdapter)
	headers := http.Header{}
	headers.Set("Idempotency-Key", "idem-1")
	headers.Set("X-Request-ID", "req-raw")
	headers.Set("Authorization", "Bearer client")
	headers.Set("X-TProxy-Route", "elsewhere")
	raw, err := adapter.Proxy(context.Background(), provider, credential, RawRequest{Method: http.MethodPost, Path: "/v1/images/generations?quality=hd", Body: []byte(`{"model":"peer-model","prompt":"cat"}`), ContentType: "application/json", Headers: headers})
	if err != nil {
		t.Fatal(err)
	}
	if raw.Status != http.StatusOK || !strings.Contains(string(raw.Body), "https://img") || !strings.Contains(raw.ContentType, "json") {
		t.Fatalf("raw response = %#v", raw)
	}
	call := peer.lastCall(t)
	if call.method != http.MethodPost || call.path != "/v1/images/generations" || call.query != "quality=hd" || string(call.raw) != `{"model":"peer-model","prompt":"cat"}` {
		t.Fatalf("peer request = %s %s?%s %s", call.method, call.path, call.query, call.raw)
	}
	if call.header.Get("Idempotency-Key") != clusterScopedValue("", "idem-1") || call.header.Get("X-Request-ID") != "req-raw" || call.header.Get("Content-Type") != "application/json" {
		t.Fatalf("forwarded headers = %v", call.header)
	}
	if call.header.Get("Authorization") != "" || call.header.Get("X-TProxy-Route") != "" {
		t.Fatalf("client control headers leaked: %v", call.header)
	}

	raw, err = adapter.Proxy(context.Background(), provider, credential, RawRequest{Method: http.MethodGet, Path: "/v1/videos/" + url.PathEscape("job/1")})
	if err != nil || peer.lastCall(t).method != http.MethodGet || peer.lastCall(t).escaped != "/v1/videos/job%2F1" {
		t.Fatalf("GET proxy = %#v, %v", raw, err)
	}

	status = http.StatusTooManyRequests
	raw, err = adapter.Proxy(context.Background(), provider, credential, RawRequest{Path: "/v1/embeddings", Body: []byte(`{}`), ContentType: "application/json"})
	assertProviderError(t, err, 429, "upstream_rate_limited", "15")
	if raw == nil || raw.Status != http.StatusTooManyRequests {
		t.Fatalf("non-2xx raw response = %#v", raw)
	}
}

func TestClusterPeerClientScopedValuesAndVoicesQuery(t *testing.T) {
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		writePeerJSON(w, http.StatusOK, `{"id":"c","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})
	provider, credential := clusterTestTarget()
	adapter := clusterTestAdapter(t, peer)
	for _, client := range []string{"client-a", "client-b", "client-a", ""} {
		hash := func(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(client+"\x00"+value))) }
		raw := RawRequest{Method: http.MethodGet, Path: "/v1/audio/voices", RawQuery: "model=old&language=en", UpstreamModel: "peer-model", ClientID: client, Headers: http.Header{"Idempotency-Key": {"same"}}}
		if _, err := adapter.(RawProxyAdapter).Proxy(context.Background(), provider, credential, raw); err != nil {
			t.Fatal(err)
		}
		call := peer.lastCall(t)
		query, err := url.ParseQuery(call.query)
		if err != nil || query.Get("model") != "peer-model" || query.Get("language") != "en" || call.header.Get("Idempotency-Key") != hash("same") {
			t.Fatalf("client %q query=%q headers=%v error=%v", client, call.query, call.header, err)
		}
		request := canonical.Request{Source: canonical.ProtocolOpenAI, UpstreamModel: "peer-model", SessionID: "session", Raw: map[string]any{"messages": []any{}}, Metadata: map[string]any{"client_api_key_id": client, config.ParentSessionMetadataKey: "parent"}}
		if _, err := adapter.Execute(context.Background(), provider, credential, request); err != nil {
			t.Fatal(err)
		}
		header := peer.lastCall(t).header
		if header.Get("X-Session-ID") != hash("session") || header.Get("X-Parent-Session-ID") != hash("parent") {
			t.Fatalf("client %q session headers = %v", client, header)
		}
	}
}

func TestClusterPeerRawResponseLimit(t *testing.T) {
	previous := clusterRawResponseLimit
	clusterRawResponseLimit = 4
	t.Cleanup(func() { clusterRawResponseLimit = previous })
	body := "1234"
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) })
	provider, credential := clusterTestTarget()
	adapter := clusterTestAdapter(t, peer).(RawProxyAdapter)
	if raw, err := adapter.Proxy(context.Background(), provider, credential, RawRequest{Path: "/v1/audio/speech"}); err != nil || string(raw.Body) != body {
		t.Fatalf("at limit: response=%v error=%v", raw, err)
	}
	body = "12345"
	_, err := adapter.Proxy(context.Background(), provider, credential, RawRequest{Path: "/v1/audio/speech"})
	assertProviderError(t, err, 502, "cluster_response_too_large", "")
}

func TestClusterProviderRegistryEntriesMakeNoOutboundCalls(t *testing.T) {
	outbound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected outbound call to %s", r.URL)
	}))
	defer outbound.Close()
	registry := NewRegistry()
	descriptor, err := registry.Describe(ClusterProviderType)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ProtocolFamily != "cluster" || descriptor.ModelDiscovery || len(descriptor.Capabilities) != 1 || descriptor.Capabilities[0] != "*" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	provider, credential := clusterTestTarget()
	provider.BaseURL = outbound.URL
	if err := registry.HealthCheck(context.Background(), provider, credential); err != nil {
		t.Fatalf("health check = %v", err)
	}
	models, err := registry.DiscoverModels(context.Background(), provider, credential)
	if err != nil || len(models) != 1 || models[0].ID != "peer-model" {
		t.Fatalf("discovered = %#v, %v", models, err)
	}
	quota, err := registry.CredentialQuota(context.Background(), provider, credential)
	if err != nil || len(quota.Quotas) != 0 || quota.Message == "" {
		t.Fatalf("quota = %#v, %v", quota, err)
	}
	if SupportsQuota(ClusterProviderType) {
		t.Fatal("background quota refresh must skip cluster peers")
	}
}

func TestClusterPeerPingTravelsOverTheTransport(t *testing.T) {
	peer := newClusterTestPeer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/embeddings" {
			writePeerJSON(w, http.StatusOK, `{"data":[{"embedding":[0.1,0.2]}]}`)
			return
		}
		writePeerJSON(w, http.StatusOK, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`)
	})
	registry := NewRegistry()
	registry.SetClusterTransport(peer)
	provider, credential := clusterTestTarget()
	provider.BaseURL = "http://127.0.0.1:1"
	if result := registry.PingModel(context.Background(), provider, credential, "peer-embed", "embedding"); !result.OK {
		t.Fatalf("embedding ping = %#v", result)
	}
	if peer.lastCall(t).path != "/v1/embeddings" {
		t.Fatalf("embedding ping path = %s", peer.lastCall(t).path)
	}
	if result := registry.PingModel(context.Background(), provider, credential, "peer-model", "llm"); !result.OK {
		t.Fatalf("llm ping = %#v", result)
	}
	if call := peer.lastCall(t); call.path != "/v1/chat/completions" || call.body["model"] != "peer-model" {
		t.Fatalf("llm ping request = %s %v", call.path, call.body)
	}
}
