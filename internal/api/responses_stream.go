package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
)

// responsesStreamWriter emits Codex-compatible Responses API SSE events.
// Ported from 9router open-sse/translator/response/openai-responses.js.
type responsesStreamWriter struct {
	responseID string
	model      string
	createdAt  int64
	seq        int

	started   bool
	completed bool

	msgTextBuf      strings.Builder
	msgItemAdded    bool
	msgContentAdded bool
	msgItemDone     bool
	msgOutputIndex  int

	reasoningID        string
	reasoningIndex     int
	reasoningBuf       strings.Builder
	reasoningEncrypted string
	reasoningItemAdded bool
	reasoningPartAdded bool
	reasoningDone      bool

	funcArgsBuf  map[int]string
	funcNames    map[int]string
	funcCallIDs  map[int]string
	funcItemDone map[int]bool

	usage *canonical.Usage
}

func newResponsesStreamWriter(responseID, model string) *responsesStreamWriter {
	return &responsesStreamWriter{
		responseID:     responseID,
		model:          model,
		createdAt:      time.Now().Unix(),
		msgOutputIndex: 0,
		funcArgsBuf:    map[int]string{},
		funcNames:      map[int]string{},
		funcCallIDs:    map[int]string{},
		funcItemDone:   map[int]bool{},
	}
}

func (w *responsesStreamWriter) nextSeq() int {
	w.seq++
	return w.seq
}

func (w *responsesStreamWriter) emit(eventType string, payload map[string]any) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["type"] = eventType
	payload["sequence_number"] = w.nextSeq()
	return payload
}

func (w *responsesStreamWriter) ensureStarted() []map[string]any {
	if w.started {
		return nil
	}
	w.started = true
	return []map[string]any{
		w.emit("response.created", map[string]any{
			"response": map[string]any{
				"id":         w.responseID,
				"object":     "response",
				"model":      w.model,
				"created_at": w.createdAt,
				"status":     "in_progress",
				"background": false,
				"error":      nil,
				"output":     []any{},
			},
		}),
		w.emit("response.in_progress", map[string]any{
			"response": map[string]any{
				"id":         w.responseID,
				"object":     "response",
				"model":      w.model,
				"created_at": w.createdAt,
				"status":     "in_progress",
			},
		}),
	}
}

func (w *responsesStreamWriter) messageID() string {
	return fmt.Sprintf("msg_%s_%d", w.responseID, w.msgOutputIndex)
}

func (w *responsesStreamWriter) emitTextDelta(text string) []map[string]any {
	if text == "" {
		return nil
	}
	events := w.closeReasoning()
	events = append(events, w.ensureStarted()...)
	idx := w.msgOutputIndex
	msgID := w.messageID()

	if !w.msgItemAdded {
		w.msgItemAdded = true
		events = append(events, w.emit("response.output_item.added", map[string]any{
			"output_index": idx,
			"item": map[string]any{
				"id":      msgID,
				"type":    "message",
				"content": []any{},
				"role":    "assistant",
			},
		}))
	}
	if !w.msgContentAdded {
		w.msgContentAdded = true
		events = append(events, w.emit("response.content_part.added", map[string]any{
			"item_id":       msgID,
			"output_index":  idx,
			"content_index": 0,
			"part": map[string]any{
				"type":        "output_text",
				"annotations": []any{},
				"logprobs":    []any{},
				"text":        "",
			},
		}))
	}
	events = append(events, w.emit("response.output_text.delta", map[string]any{
		"item_id":       msgID,
		"output_index":  idx,
		"content_index": 0,
		"delta":         text,
		"logprobs":      []any{},
	}))
	w.msgTextBuf.WriteString(text)
	return events
}

func (w *responsesStreamWriter) ensureReasoningStarted(idx int) []map[string]any {
	if w.reasoningItemAdded {
		return nil
	}
	w.reasoningItemAdded = true
	if w.reasoningID == "" {
		w.reasoningID = fmt.Sprintf("rs_%s_%d", w.responseID, idx)
	}
	w.reasoningIndex = idx
	events := w.ensureStarted()
	events = append(events, w.emit("response.output_item.added", map[string]any{
		"output_index": idx,
		"item": map[string]any{
			"id":      w.reasoningID,
			"type":    "reasoning",
			"summary": []any{},
		},
	}))
	events = append(events, w.emit("response.reasoning_summary_part.added", map[string]any{
		"item_id":       w.reasoningID,
		"output_index":  idx,
		"summary_index": 0,
		"part": map[string]any{
			"type": "summary_text",
			"text": "",
		},
	}))
	w.reasoningPartAdded = true
	return events
}

func (w *responsesStreamWriter) applyReasoningMeta(itemID, encrypted string) {
	if strings.TrimSpace(itemID) != "" {
		w.reasoningID = strings.TrimSpace(itemID)
	}
	if strings.TrimSpace(encrypted) != "" {
		w.reasoningEncrypted = strings.TrimSpace(encrypted)
	}
}

func (w *responsesStreamWriter) emitReasoningDelta(text string) []map[string]any {
	events := w.ensureReasoningStarted(w.msgOutputIndex)
	if text == "" {
		return events
	}
	w.reasoningBuf.WriteString(text)
	events = append(events, w.emit("response.reasoning_summary_text.delta", map[string]any{
		"item_id":       w.reasoningID,
		"output_index":  w.reasoningIndex,
		"summary_index": 0,
		"delta":         text,
	}))
	return events
}

func (w *responsesStreamWriter) closeReasoning() []map[string]any {
	if !w.reasoningItemAdded || w.reasoningDone {
		return nil
	}
	w.reasoningDone = true
	fullText := w.reasoningBuf.String()
	return []map[string]any{
		w.emit("response.reasoning_summary_text.done", map[string]any{
			"item_id":       w.reasoningID,
			"output_index":  w.reasoningIndex,
			"summary_index": 0,
			"text":          fullText,
		}),
		w.emit("response.reasoning_summary_part.done", map[string]any{
			"item_id":       w.reasoningID,
			"output_index":  w.reasoningIndex,
			"summary_index": 0,
			"part": map[string]any{
				"type": "summary_text",
				"text": fullText,
			},
		}),
		w.emit("response.output_item.done", map[string]any{
			"output_index": w.reasoningIndex,
			"item": func() map[string]any {
				item := map[string]any{
					"id":   w.reasoningID,
					"type": "reasoning",
					"summary": []any{
						map[string]any{"type": "summary_text", "text": fullText},
					},
				}
				if w.reasoningEncrypted != "" {
					item["encrypted_content"] = w.reasoningEncrypted
				}
				return item
			}(),
		}),
	}
}

func (w *responsesStreamWriter) closeMessage() []map[string]any {
	if !w.msgItemAdded || w.msgItemDone {
		return nil
	}
	w.msgItemDone = true
	fullText := w.msgTextBuf.String()
	msgID := w.messageID()
	idx := w.msgOutputIndex
	return []map[string]any{
		w.emit("response.output_text.done", map[string]any{
			"item_id":       msgID,
			"output_index":  idx,
			"content_index": 0,
			"text":          fullText,
			"logprobs":      []any{},
		}),
		w.emit("response.content_part.done", map[string]any{
			"item_id":       msgID,
			"output_index":  idx,
			"content_index": 0,
			"part": map[string]any{
				"type":        "output_text",
				"annotations": []any{},
				"logprobs":    []any{},
				"text":        fullText,
			},
		}),
		w.emit("response.output_item.done", map[string]any{
			"output_index": idx,
			"item": map[string]any{
				"id":   msgID,
				"type": "message",
				"content": []any{
					map[string]any{
						"type":        "output_text",
						"annotations": []any{},
						"logprobs":    []any{},
						"text":        fullText,
					},
				},
				"role": "assistant",
			},
		}),
	}
}

func (w *responsesStreamWriter) emitToolCall(toolCall map[string]any) []map[string]any {
	if toolCall == nil {
		return nil
	}
	function, _ := toolCall["function"].(map[string]any)
	name := stringValue(function["name"])
	arguments := stringValue(function["arguments"])
	callID := stringValue(toolCall["id"])
	idx := intValue(toolCall["index"])
	if idx < 0 {
		idx = 0
	}

	events := w.closeMessage()
	events = append(events, w.closeReasoning()...)

	if name != "" {
		w.funcNames[idx] = name
	}
	if callID != "" && w.funcCallIDs[idx] == "" {
		w.funcCallIDs[idx] = callID
		events = append(events, w.ensureStarted()...)
		events = append(events, w.emit("response.output_item.added", map[string]any{
			"output_index": idx,
			"item": map[string]any{
				"id":        fmt.Sprintf("fc_%s", callID),
				"type":      "function_call",
				"arguments": "",
				"call_id":   callID,
				"name":      w.funcNames[idx],
			},
		}))
	}

	refCallID := w.funcCallIDs[idx]
	if refCallID == "" {
		refCallID = callID
	}
	if arguments != "" && refCallID != "" {
		events = append(events, w.emit("response.function_call_arguments.delta", map[string]any{
			"item_id":      fmt.Sprintf("fc_%s", refCallID),
			"output_index": idx,
			"delta":        arguments,
		}))
		w.funcArgsBuf[idx] += arguments
	}
	return events
}

func (w *responsesStreamWriter) closeToolCalls() []map[string]any {
	var events []map[string]any
	for idx, callID := range w.funcCallIDs {
		if callID == "" || w.funcItemDone[idx] {
			continue
		}
		args := w.funcArgsBuf[idx]
		if args == "" {
			args = "{}"
		}
		events = append(events,
			w.emit("response.function_call_arguments.done", map[string]any{
				"item_id":      fmt.Sprintf("fc_%s", callID),
				"output_index": idx,
				"arguments":    args,
			}),
			w.emit("response.output_item.done", map[string]any{
				"output_index": idx,
				"item": map[string]any{
					"id":        fmt.Sprintf("fc_%s", callID),
					"type":      "function_call",
					"arguments": args,
					"call_id":   callID,
					"name":      w.funcNames[idx],
				},
			}),
		)
		w.funcItemDone[idx] = true
	}
	return events
}

func (w *responsesStreamWriter) sendCompleted() map[string]any {
	if w.completed {
		return nil
	}
	w.completed = true
	response := map[string]any{
		"id":         w.responseID,
		"object":     "response",
		"model":      w.model,
		"created_at": w.createdAt,
		"status":     "completed",
		"background": false,
		"error":      nil,
	}
	if w.usage != nil {
		response["usage"] = map[string]any{
			"input_tokens":  w.usage.InputTokens,
			"output_tokens": w.usage.OutputTokens,
			"total_tokens":  w.usage.InputTokens + w.usage.OutputTokens,
		}
	}
	return w.emit("response.completed", map[string]any{"response": response})
}

func (w *responsesStreamWriter) handle(event canonical.Event) ([]map[string]any, bool) {
	switch event.Type {
	case canonical.EventMessageStart:
		return w.ensureStarted(), false
	case canonical.EventTextDelta:
		return w.emitTextDelta(event.Text), false
	case canonical.EventReasoningDelta:
		w.applyReasoningMeta(event.ReasoningItemID, event.ReasoningEncrypted)
		return w.emitReasoningDelta(event.Reasoning), false
	case canonical.EventToolCallDelta:
		return w.emitToolCall(event.ToolCall), false
	case canonical.EventImageDelta:
		events := w.ensureStarted()
		events = append(events, w.emit("response.output_image.delta", map[string]any{
			"response_id": w.responseID,
			"delta":       event.Media,
		}))
		return events, false
	case canonical.EventAudioDelta:
		events := w.ensureStarted()
		events = append(events, w.emit("response.output_audio.delta", map[string]any{
			"response_id": w.responseID,
			"delta":       event.Media,
		}))
		return events, false
	case canonical.EventUsage:
		if event.Usage != nil {
			copyUsage := *event.Usage
			w.usage = &copyUsage
		}
		return nil, false
	case canonical.EventMessageEnd:
		var events []map[string]any
		events = append(events, w.closeMessage()...)
		events = append(events, w.closeReasoning()...)
		events = append(events, w.closeToolCalls()...)
		if completed := w.sendCompleted(); completed != nil {
			events = append(events, completed)
		}
		return events, true
	case canonical.EventError:
		message := "stream error"
		if event.Err != nil {
			message = event.Err.Error()
		}
		return []map[string]any{
			w.emit("error", map[string]any{
				"error": map[string]any{
					"type":    "api_error",
					"message": message,
				},
			}),
		}, true
	default:
		var unexpected canonical.EventType = event.Type
		_ = unexpected
		return nil, false
	}
}

func writeResponsesStream(w http.ResponseWriter, r *http.Request, events <-chan canonical.Event, requestID, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	writer := newResponsesStreamWriter("resp_"+requestID, model)
	doneSent := false
	sendDone := func() {
		if doneSent {
			return
		}
		doneSent = true
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
	send := func(eventType string, payload map[string]any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(data))
		if flusher != nil {
			flusher.Flush()
		}
	}
	sendPassthrough := func(eventType string, data []byte) {
		if eventType == "" {
			var raw map[string]any
			if json.Unmarshal(data, &raw) == nil {
				eventType = stringValue(raw["type"])
			}
		}
		if eventType == "" {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(data))
		} else {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(data))
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	for event := range events {
		switch event.Type {
		case canonical.EventResponsesSSE:
			sendPassthrough(event.SSEEvent, event.SSEData)
			continue
		case canonical.EventError:
			message := "stream error"
			if event.Err != nil {
				message = event.Err.Error()
			}
			send("error", map[string]any{
				"error": map[string]any{
					"type":    "api_error",
					"message": message,
				},
			})
			sendDone()
			for range events {
			}
			return
		}
		payloads, done := writer.handle(event)
		for _, payload := range payloads {
			send(stringValue(payload["type"]), payload)
		}
		if done {
			sendDone()
			for range events {
			}
			return
		}
	}
	sendDone()
}
