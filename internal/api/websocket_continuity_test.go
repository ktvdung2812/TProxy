package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
)

func TestWebSocketThreeTurnsReconstructToolHistory(t *testing.T) {
	var turn atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		n := turn.Add(1)
		messages, _ := body["messages"].([]any)
		if len(messages) != int(n*2-1) {
			t.Errorf("turn %d got %d messages: %#v", n, len(messages), messages)
		}
		if n == 2 {
			tool := messages[2].(map[string]any)
			if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" {
				t.Errorf("lost tool pairing: %#v", messages)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"weather\",\"arguments\":\"{}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"sunny\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer upstream.Close()
	t.Setenv("TPROXY_WS_CONTINUITY_KEY", "test-only-key")
	cfg := &config.Config{ClientAPIKeys: []config.ClientAPIKey{{ID: "a", KeyEnv: "TPROXY_WS_CONTINUITY_KEY"}}, Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "c", AuthType: "none"}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "p", UpstreamModel: "upstream"}}}}}
	data := apiTestStore(t, cfg)
	app := httptest.NewServer(NewServer(cfg, data, router.New(data, providers.NewRegistry())).Handler())
	defer app.Close()
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(app.URL, "http")+"/v1/responses/ws", http.Header{"Authorization": []string{"Bearer test-only-key"}, "X-Session-ID": []string{"conversation"}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	previous := ""
	for n := 1; n <= 3; n++ {
		payload := map[string]any{"type": "response.create", "request_id": fmt.Sprint("turn-", n)}
		if n == 1 {
			payload["model"] = "m"
			payload["input"] = "weather?"
		} else {
			payload["previous_response_id"] = previous
			if n == 2 {
				payload["input"] = []any{map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "sunny"}}
			} else {
				payload["input"] = "tomorrow?"
			}
		}
		if err = connection.WriteJSON(payload); err != nil {
			t.Fatal(err)
		}
		_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			var event map[string]any
			if err = connection.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			if event["type"] == "error" {
				t.Fatalf("turn %d: %#v", n, event)
			}
			if event["type"] == "response.completed" {
				response := event["response"].(map[string]any)
				previous = stringValue(response["id"])
				if len(response["output"].([]any)) == 0 {
					t.Fatal("completed response lost output")
				}
				break
			}
		}
	}
	if turn.Load() != 3 {
		t.Fatal(turn.Load())
	}
}

func TestWebSocketDisconnectReleasesStalledAccount(t *testing.T) {
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	t.Setenv("TPROXY_WS_CANCEL_KEY", "test-only-cancel")
	cfg := &config.Config{ClientAPIKeys: []config.ClientAPIKey{{ID: "a", KeyEnv: "TPROXY_WS_CANCEL_KEY"}}, Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "c", AuthType: "none", Metadata: map[string]any{"max_concurrent_requests": 1}}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "p", UpstreamModel: "upstream"}}}}}
	data := apiTestStore(t, cfg)
	requestRouter := router.New(data, providers.NewRegistry())
	app := httptest.NewServer(NewServer(cfg, data, requestRouter).Handler())
	defer app.Close()
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(app.URL, "http")+"/v1/responses/ws", http.Header{"Authorization": []string{"Bearer test-only-cancel"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = connection.WriteJSON(map[string]any{"type": "response.create", "model": "m", "input": "hello"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream never started")
	}
	_ = connection.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		states := requestRouter.AccountRuntime()
		if len(states) > 0 && states[0].InFlight == 0 && states[0].Waiting == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot stuck after disconnect: %+v", states)
		}
		time.Sleep(time.Millisecond)
	}
}
