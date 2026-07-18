package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/tokenizer"
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
	connection.SetReadLimit(16 << 20)
	for {
		messageType, data, readErr := connection.ReadMessage()
		if readErr != nil {
			if websocket.IsCloseError(readErr, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(readErr, context.Canceled) {
				return
			}
			return
		}
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
		request := parseResponses(payload, requestID)
		request.Stream = true
		request.SessionID = sessionIDFromRequest(r)
		if request.SessionID == "" {
			request.SessionID = strings.TrimSpace(stringValue(event["session_id"]))
		}
		request.PublicModelID = resolveIngressModel(r, request.PublicModelID)
		attachIngressMetadata(r, &request)
		if err = s.runResponsesWebSocketRequest(r.Context(), connection, r, request); err != nil {
			return
		}
	}
}

func (s *Server) runResponsesWebSocketRequest(parent context.Context, connection *websocket.Conn, r *http.Request, request canonical.Request) error {
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	attachClientPolicyMetadata(&request, key)
	if err := s.enforceClientBudget(parent, key); err != nil {
		return writeWebSocketError(connection, "budget_exceeded", err.Error(), request.RequestID)
	}
	if key != nil && key.Policy.Limits.MaxOutputTokens > 0 && request.MaxTokens > key.Policy.Limits.MaxOutputTokens {
		return writeWebSocketError(connection, "max_output_tokens_exceeded", "requested output tokens exceed API key policy", request.RequestID)
	}
	if err := s.limiter.acquireStream(key, s.limitScopes(key)...); err != nil {
		return writeWebSocketError(connection, "concurrency_limit_exceeded", err.Error(), request.RequestID)
	}
	defer s.limiter.releaseStream(key, s.limitScopes(key)...)
	model, err := s.router.Resolve(parent, request.PublicModelID, key)
	if err != nil {
		return writeWebSocketError(connection, "model_not_found", err.Error(), request.RequestID)
	}
	request.PublicModelID = model.ID
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.PublicModelID = model.ID
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-TProxy-Token-Saver")), "off") {
		stats := tokenizer.Compress(&request)
		if stats.TokensSaved > 0 {
			if request.Metadata == nil {
				request.Metadata = map[string]any{}
			}
			request.Metadata["tokens_saved"] = stats.TokensSaved
		}
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stream, err := s.router.ExecuteStream(ctx, *model, request)
	if err != nil {
		return writeWebSocketError(connection, providers.Code(err), err.Error(), request.RequestID)
	}
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.ProviderID = stream.Selection.Provider.ID
		state.CredentialID = stream.Selection.Credential.ID
		state.Attempt = stream.Selection.Attempt
	}
	responseID := "resp_" + request.RequestID
	writer := newResponsesStreamWriter(responseID, model.ID)
	for event := range stream.Events {
		payloads, done := writer.handle(event)
		for _, payload := range payloads {
			if stringValue(payload["type"]) == "error" {
				errObj, _ := payload["error"].(map[string]any)
				return writeWebSocketError(connection, "stream_error", stringValue(errObj["message"]), request.RequestID)
			}
			if err = connection.WriteJSON(payload); err != nil {
				cancel()
				return err
			}
		}
		if done {
			return nil
		}
	}
	return nil
}

func writeWebSocketError(connection *websocket.Conn, code, message, requestID string) error {
	return connection.WriteJSON(map[string]any{"type": "error", "error": map[string]any{"type": "provider_error", "code": code, "message": message, "request_id": requestID}})
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
