package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

var responsesUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	ReadBufferSize:   16 << 10,
	WriteBufferSize:  16 << 10,
	CheckOrigin:      responsesWebSocketOriginAllowed,
}

func responsesWebSocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	return security.IsLoopback(r) && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")
}

func (s *Server) responsesWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "WebSocket GET upgrade required", useClientRequestID(r))
		return
	}
	connection, err := responsesUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.Protocol = "responses-websocket"
	}
	defer connection.Close()
	connection.SetReadLimit(2 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)
	type frame struct {
		kind int
		data []byte
	}
	frames := make(chan frame, 1)
	go func() {
		defer cancel()
		for {
			kind, data, err := connection.ReadMessage()
			if err != nil {
				return
			}
			select {
			case frames <- frame{kind, data}:
			case <-ctx.Done():
				return
			default:
				_ = connection.Close()
				return
			}
		}
	}()
	sessionID := sessionIDFromRequest(r)
	if sessionID == "" {
		sessionID = security.NewID("ws_")
	}
	lastResponseID := ""
	for {
		var incoming frame
		select {
		case <-ctx.Done():
			return
		case incoming = <-frames:
		}
		messageType, data := incoming.kind, incoming.data

		if messageType != websocket.TextMessage {
			_ = writeWebSocketError(connection, "invalid_request", "Responses WebSocket accepts JSON text frames", "")
			continue
		}
		var event map[string]any
		if json.Unmarshal(data, &event) != nil {
			_ = writeWebSocketError(connection, "invalid_request", "invalid JSON frame", "")
			continue
		}
		if eventType := strings.TrimSpace(stringValue(event["type"])); eventType != "" && eventType != "response.create" {
			_ = writeWebSocketError(connection, "unsupported_event", "supported event type is response.create", stringValue(event["request_id"]))
			continue
		}
		payload := event
		if nested, ok := event["response"].(map[string]any); ok {
			payload = nested
		} else {
			payload = cloneMap(event)
			delete(payload, "type")
		}
		requestID := strings.TrimSpace(stringValue(firstValue(event, "request_id", "event_id")))
		if requestID == "" {
			requestID = security.NewID("req_")
		}
		if explicit := sessionIDFromRequest(r, payload); explicit != "" {
			if explicit != sessionID {
				lastResponseID = ""
			}
			sessionID = explicit
		} else if explicit := strings.TrimSpace(stringValue(event["session_id"])); explicit != "" {
			if explicit != sessionID {
				lastResponseID = ""
			}
			sessionID = explicit
		}
		scope := responseSessionScope(r, sessionID)
		prepared, prepareErr := s.responseSessions.prepare(scope, lastResponseID, payload)
		if prepareErr != nil {
			if writeWebSocketError(connection, "invalid_continuation", prepareErr.Error(), requestID) != nil {
				return
			}
			continue
		}
		request := parseResponses(prepared, requestID)
		request.Stream = true
		request.SessionID = sessionID
		request.PublicModelID = resolveIngressModel(r, request.PublicModelID)
		attachIngressMetadata(r, &request)
		responseID, runErr := s.runResponsesWebSocketRequest(r.Context(), connection, r, request, scope)
		if runErr != nil {
			return
		}
		if responseID != "" {
			lastResponseID = responseID
		}
	}
}

func (s *Server) runResponsesWebSocketRequest(parent context.Context, connection *websocket.Conn, r *http.Request, request canonical.Request, scope string) (string, error) {
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	attachClientPolicyMetadata(&request, key)
	if err := s.enforceClientBudget(parent, key); err != nil {
		return "", writeWebSocketError(connection, "budget_exceeded", err.Error(), request.RequestID)
	}
	if key != nil && key.Policy.Limits.MaxOutputTokens > 0 && request.MaxTokens > key.Policy.Limits.MaxOutputTokens {
		return "", writeWebSocketError(connection, "max_output_tokens_exceeded", "requested output tokens exceed API key policy", request.RequestID)
	}
	if err := s.limiter.acquireStream(key, s.limitScopes(key)...); err != nil {
		return "", writeWebSocketError(connection, "concurrency_limit_exceeded", err.Error(), request.RequestID)
	}
	defer s.limiter.releaseStream(key, s.limitScopes(key)...)
	model, err := s.router.Resolve(parent, request.PublicModelID, key)
	if err != nil {
		return "", writeWebSocketError(connection, "model_not_found", err.Error(), request.RequestID)
	}
	request.PublicModelID = model.ID
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.PublicModelID = model.ID
	}
	s.applyTokenSaver(&request, nil, r)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stream, err := s.router.ExecuteStream(ctx, *model, request)
	if err != nil {
		return "", writeWebSocketError(connection, providers.Code(err), err.Error(), request.RequestID)
	}
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.ProviderID = stream.Selection.Provider.ID
		state.CredentialID = stream.Selection.Credential.ID
		state.Attempt = stream.Selection.Attempt
	}
	responseID := "resp_" + request.RequestID
	writer := newResponsesStreamWriter(responseID, model.ID)
	outputs := map[int]any{}
	for event := range stream.Events {
		payloads, done := writer.handle(event)
		if event.Type == canonical.EventResponsesSSE {
			var payload map[string]any
			if json.Unmarshal(event.SSEData, &payload) != nil {
				return "", writeWebSocketError(connection, "invalid_upstream_event", "invalid Responses event", request.RequestID)
			}
			if stringValue(payload["type"]) == "" {
				payload["type"] = event.SSEEvent
			}
			payloads = []map[string]any{payload}
			kind := stringValue(payload["type"])
			done = kind == "response.completed" || kind == "response.incomplete" || kind == "response.failed"
		}
		for _, payload := range payloads {
			if stringValue(payload["type"]) == "error" {
				errObj, _ := payload["error"].(map[string]any)
				return "", writeWebSocketError(connection, "stream_error", stringValue(errObj["message"]), request.RequestID)
			}
			kind := stringValue(payload["type"])
			if kind == "response.output_item.done" {
				outputs[intValue(payload["output_index"])] = payload["item"]
			}
			if kind == "response.completed" || kind == "response.incomplete" {
				response, _ := payload["response"].(map[string]any)
				if response == nil {
					return "", writeWebSocketError(connection, "invalid_upstream_event", "terminal response object missing", request.RequestID)
				}
				if id := stringValue(response["id"]); id != "" {
					responseID = id
				}
				output, hasOutput := response["output"].([]any)
				if !hasOutput {
					indices := make([]int, 0, len(outputs))
					for index := range outputs {
						indices = append(indices, index)
					}
					sort.Ints(indices)
					for _, index := range indices {
						output = append(output, outputs[index])
					}
					response["output"] = output
				}
				s.responseSessions.put(scope, responseID, request.Raw, output)
			}
			_ = connection.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if err = connection.WriteJSON(payload); err != nil {
				cancel()
				return "", err
			}
		}
		if done {
			if _, ok := s.responseSessions.get(scope, responseID); !ok {
				return "", nil
			}
			return responseID, nil
		}
	}
	return "", nil
}

func writeWebSocketError(connection *websocket.Conn, code, message, requestID string) error {
	_ = connection.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return connection.WriteJSON(map[string]any{"type": "error", "error": map[string]any{"type": "provider_error", "code": code, "message": security.RedactText(message), "request_id": requestID}})
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
