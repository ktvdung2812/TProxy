package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/cluster"
	"github.com/tproxy/tproxy/internal/config"
)

func fidelityBlocks() []any {
	return []any{
		map[string]any{"type": "thinking", "thinking": "think", "signature": "sig"},
		map[string]any{"type": "redacted_thinking", "data": "opaque"},
		map[string]any{"type": "tool_use", "id": "t1", "name": "first", "input": map[string]any{"x": float64(1)}},
		map[string]any{"type": "tool_use", "id": "t2", "name": "second", "input": map[string]any{"y": float64(2)}},
	}
}

func fidelityUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if !request.Stream {
			writeJSON(w, 200, map[string]any{"id": "msg_fidelity", "model": "up-model", "type": "message", "role": "assistant", "content": fidelityBlocks(), "stop_reason": "max_tokens", "stop_sequence": "END", "usage": map[string]any{"input_tokens": 3, "output_tokens": 5}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(v map[string]any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v["type"], b)
		}
		start := func(i int, b map[string]any) {
			emit(map[string]any{"type": "content_block_start", "index": i, "content_block": b})
		}
		delta := func(i int, d map[string]any) {
			emit(map[string]any{"type": "content_block_delta", "index": i, "delta": d})
		}
		stop := func(i int) { emit(map[string]any{"type": "content_block_stop", "index": i}) }
		emit(map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_fidelity", "model": "up-model", "usage": map[string]any{"input_tokens": 3}}})
		start(0, map[string]any{"type": "thinking", "thinking": ""})
		delta(0, map[string]any{"type": "thinking_delta", "thinking": "think"})
		delta(0, map[string]any{"type": "signature_delta", "signature": "sig"})
		stop(0)
		start(1, map[string]any{"type": "redacted_thinking", "data": "opaque"})
		stop(1)
		start(2, map[string]any{"type": "tool_use", "id": "t1", "name": "first", "input": map[string]any{}})
		start(3, map[string]any{"type": "tool_use", "id": "t2", "name": "second", "input": map[string]any{}})
		delta(2, map[string]any{"type": "input_json_delta", "partial_json": `{"x":`})
		delta(3, map[string]any{"type": "input_json_delta", "partial_json": `{"y":`})
		delta(2, map[string]any{"type": "input_json_delta", "partial_json": `1}`})
		delta(3, map[string]any{"type": "input_json_delta", "partial_json": `2}`})
		stop(2)
		stop(3)
		emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "max_tokens", "stop_sequence": "END"}, "usage": map[string]any{"output_tokens": 5}})
		emit(map[string]any{"type": "message_stop"})
	}))
	t.Cleanup(server.Close)
	return server
}

func fidelityRequest(t *testing.T, n *clusterE2ENode, stream bool) []byte {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"model": "shared-model", "max_tokens": 100, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
	req, err := http.NewRequest("POST", n.http.URL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+n.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("status=%d err=%v body=%s", resp.StatusCode, err, data)
	}
	return data
}

func fidelitySSE(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "data: ") {
			var v map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v); err != nil {
				t.Fatal(err)
			}
			if v["type"] != "message_start" {
				out = append(out, v)
			}
		}
	}
	return out
}

func TestClusterClaudeFidelity(t *testing.T) {
	t.Setenv("TPROXY_SKIP_TUNNEL_AUTO", "1")
	t.Setenv("TPROXY_SKIP_CLUSTER_AUTO", "")
	t.Setenv("TPROXY_CLUSTER_JOIN_TOKEN", "")
	previous := clusterOptionsHook
	clusterOptionsHook = func(o *cluster.Options) {
		o.PollInterval = 100 * time.Millisecond
		o.PingInterval = 200 * time.Millisecond
		o.PingTimeout = time.Second
	}
	t.Cleanup(func() { clusterOptionsHook = previous })
	upstream := fidelityUpstream(t)
	hub := newClusterE2ENode(t, &config.Config{Server: config.ServerConfig{Host: "127.0.0.1", Port: 1}, Providers: []config.ProviderConfig{{ID: "anthropic", Type: "anthropic-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "cred", AuthType: "api_key", Secret: "secret"}}}}, Models: []config.PublicModelConfig{{ID: "shared-model", Enabled: true, Routes: []config.RouteTargetConfig{clusterE2ERoute("local", "anthropic", "up-model")}}}})
	member := newClusterE2ENode(t, &config.Config{Server: config.ServerConfig{Host: "127.0.0.1", Port: 1}})
	hub.mustAdmin(t, http.MethodPost, "/api/admin/cluster/create", map[string]any{"name": "fidelity", "hub_url": hub.http.URL, "allow_insecure": true})
	hub.mustAdmin(t, http.MethodPut, "/api/admin/cluster/shared", map[string]any{"models": []string{"shared-model"}, "combos": []string{}, "api_keys": []string{hub.keyID}})
	member.mustAdmin(t, http.MethodPost, "/api/admin/cluster/join", map[string]any{"token": clusterE2EToken(t, hub), "node_name": "member", "allow_insecure": true})
	hubID := hub.status(t).Node.ID
	clusterE2EWait(t, "peer route", func() bool { return member.peerServes(hubID, "shared-model") })
	direct := fidelitySSE(t, fidelityRequest(t, hub, true))
	relayed := fidelitySSE(t, fidelityRequest(t, member, true))
	if !reflect.DeepEqual(direct, relayed) {
		t.Fatalf("direct=%v\nrelay=%v", direct, relayed)
	}
	starts := map[int]map[string]any{}
	args := map[int]string{}
	stops := map[int]bool{}
	var signature string
	var end map[string]any
	for _, e := range direct {
		i := intNumberFidelity(e["index"])
		switch e["type"] {
		case "content_block_start":
			starts[i], _ = e["content_block"].(map[string]any)
		case "content_block_stop":
			stops[i] = true
		case "content_block_delta":
			d := e["delta"].(map[string]any)
			if d["type"] == "signature_delta" {
				signature = d["signature"].(string)
			}
			if d["type"] == "input_json_delta" {
				args[i] += d["partial_json"].(string)
			}
		case "message_delta":
			end = e["delta"].(map[string]any)
		}
	}
	if signature != "sig" || starts[1]["data"] != "opaque" || starts[2]["id"] != "t1" || starts[2]["name"] != "first" || starts[3]["id"] != "t2" || starts[3]["name"] != "second" || args[2] != `{"x":1}` || args[3] != `{"y":2}` || !stops[2] || !stops[3] || end["stop_reason"] != "max_tokens" || end["stop_sequence"] != "END" {
		t.Fatalf("events=%v", direct)
	}
	var directBody, relayBody map[string]any
	if err := json.Unmarshal(fidelityRequest(t, hub, false), &directBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fidelityRequest(t, member, false), &relayBody); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(directBody["content"], fidelityBlocks()) || !reflect.DeepEqual(directBody["content"], relayBody["content"]) || directBody["stop_reason"] != "max_tokens" || directBody["stop_sequence"] != "END" || relayBody["stop_reason"] != "max_tokens" || relayBody["stop_sequence"] != "END" {
		t.Fatalf("direct=%v relay=%v", directBody, relayBody)
	}
}

func intNumberFidelity(v any) int { n, _ := v.(float64); return int(n) }

func TestClaudeFidelityToOpenAI(t *testing.T) {
	upstream := fidelityUpstream(t)
	node := newClusterE2ENode(t, &config.Config{Server: config.ServerConfig{Host: "127.0.0.1", Port: 1}, Providers: []config.ProviderConfig{{ID: "anthropic", Type: "anthropic-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "cred", AuthType: "api_key", Secret: "secret"}}}}, Models: []config.PublicModelConfig{{ID: "shared-model", Enabled: true, Routes: []config.RouteTargetConfig{clusterE2ERoute("local", "anthropic", "up-model")}}}})
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"model": "shared-model", "max_tokens": 100, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hi"}}})
			req, _ := http.NewRequest("POST", node.http.URL+"/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+node.key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != 200 {
				t.Fatalf("status=%d err=%v body=%s", resp.StatusCode, err, data)
			}
			if !strings.Contains(string(data), `"finish_reason":"length"`) {
				t.Fatalf("response=%s", data)
			}
			if stream && (!strings.Contains(string(data), `"index":0`) || !strings.Contains(string(data), `"index":1`) || !strings.Contains(string(data), `"id":"t1"`) || !strings.Contains(string(data), `"id":"t2"`)) {
				t.Fatalf("response=%s", data)
			}
		})
	}
}
