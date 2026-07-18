package providers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/canonical"
)

type kiroStreamState struct {
	finishEmitted       bool
	hasToolCalls        bool
	hasReasoningContent bool
	toolCallIndex       int
	seenToolIDs         map[string]int
	inThinking          bool
	totalContentLength  int
	contextUsagePct     float64
	hasContextUsage     bool
	hasMeteringEvent    bool
	usage               *canonical.Usage
	responseID          string
	model               string
	chunkIndex          int
}

func parseKiroEventFrame(data []byte) (headers map[string]string, payload map[string]any) {
	if len(data) < 16 {
		return nil, nil
	}
	headersLength := int(binary.BigEndian.Uint32(data[4:8]))
	headers = map[string]string{}
	offset := 12
	headerEnd := 12 + headersLength
	for offset < headerEnd && offset < len(data) {
		nameLen := int(data[offset])
		offset++
		if offset+nameLen > len(data) {
			break
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen
		if offset >= len(data) {
			break
		}
		headerType := data[offset]
		offset++
		if headerType != 7 {
			break
		}
		if offset+2 > len(data) {
			break
		}
		valueLen := int(data[offset])<<8 | int(data[offset+1])
		offset += 2
		if offset+valueLen > len(data) {
			break
		}
		value := string(data[offset : offset+valueLen])
		offset += valueLen
		headers[name] = value
	}
	payloadStart := 12 + headersLength
	payloadEnd := len(data) - 4
	if payloadEnd <= payloadStart {
		return headers, nil
	}
	payloadStr := strings.TrimSpace(string(data[payloadStart:payloadEnd]))
	if payloadStr == "" {
		return headers, nil
	}
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		payload = map[string]any{"raw": payloadStr}
	}
	return headers, payload
}

func streamKiroEventStream(ctx context.Context, response *http.Response, model string, out chan<- canonical.Event) {
	defer response.Body.Close()
	state := &kiroStreamState{
		seenToolIDs: map[string]int{},
		responseID:  fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli()),
		model:       model,
	}
	out <- canonical.Event{Type: canonical.EventMessageStart, ID: state.responseID, Model: model}
	buffer := make([]byte, 0, 64<<10)
	chunk := make([]byte, 32<<10)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := response.Body.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
			buffer = state.consumeKiroBuffer(buffer, out)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			out <- canonical.Event{Type: canonical.EventError, Err: err}
			return
		}
	}
	if !state.finishEmitted {
		out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: kiroFinishReason(state)}
	}
}

func (s *kiroStreamState) consumeKiroBuffer(buffer []byte, out chan<- canonical.Event) []byte {
	iterations := 0
	for len(buffer) >= 16 && iterations < 1000 {
		iterations++
		totalLength := int(binary.BigEndian.Uint32(buffer[:4]))
		if totalLength < 16 || totalLength > len(buffer) {
			break
		}
		frame := buffer[:totalLength]
		buffer = buffer[totalLength:]
		headers, payload := parseKiroEventFrame(frame)
		if headers == nil {
			continue
		}
		s.handleKiroEvent(headers[":event-type"], payload, out)
	}
	return buffer
}

func (s *kiroStreamState) handleKiroEvent(eventType string, payload map[string]any, out chan<- canonical.Event) {
	switch eventType {
	case "assistantResponseEvent":
		content := stringValue(payload["content"])
		content = s.stripThinkingTags(content)
		if content == "" && s.hasReasoningContent {
			return
		}
		s.totalContentLength += len(content)
		if content != "" {
			out <- canonical.Event{Type: canonical.EventTextDelta, Text: content}
			s.chunkIndex++
		}
	case "reasoningContentEvent":
		reasoning := extractKiroReasoning(payload)
		if reasoning == "" {
			return
		}
		s.hasReasoningContent = true
		s.totalContentLength += len(reasoning)
		out <- canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: reasoning}
		s.chunkIndex++
	case "codeEvent":
		if text := stringValue(payload["content"]); text != "" {
			out <- canonical.Event{Type: canonical.EventTextDelta, Text: text}
			s.chunkIndex++
		}
	case "toolUseEvent":
		s.emitToolUse(payload, out)
	case "messageStopEvent":
		if !s.finishEmitted {
			s.finishEmitted = true
			out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: kiroFinishReason(s)}
		}
	case "contextUsageEvent":
		if pct := numberValue(firstValue(payload, "contextUsagePercentage", "context_usage_percentage")); pct > 0 {
			s.contextUsagePct = float64(pct)
			s.hasContextUsage = true
		}
	case "meteringEvent":
		s.hasMeteringEvent = true
	case "metricsEvent":
		s.captureMetrics(payload)
	}
	if s.hasMeteringEvent && s.hasContextUsage && !s.finishEmitted {
		s.finishEmitted = true
		if s.usage == nil {
			s.usage = estimateKiroUsage(s)
		}
		if s.usage != nil {
			u := *s.usage
			out <- canonical.Event{Type: canonical.EventUsage, Usage: &u}
		}
		out <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: kiroFinishReason(s)}
	}
}

func (s *kiroStreamState) stripThinkingTags(content string) string {
	if s.inThinking {
		if idx := strings.Index(content, "</thinking>"); idx >= 0 {
			s.inThinking = false
			after := content[idx+len("</thinking>"):]
			if strings.HasPrefix(after, "\n") {
				after = after[1:]
			}
			content = after
		} else {
			return ""
		}
	}
	if strings.Contains(content, "<thinking>") {
		s.inThinking = true
		if idx := strings.Index(content, "</thinking>"); idx >= 0 {
			s.inThinking = false
			before := strings.Split(content, "<thinking>")[0]
			after := strings.Join(strings.Split(content, "</thinking>")[1:], "</thinking>")
			if strings.HasPrefix(after, "\n") {
				after = after[1:]
			}
			content = before + after
		} else {
			content = strings.Split(content, "<thinking>")[0]
		}
	}
	return content
}

func extractKiroReasoning(payload map[string]any) string {
	if nested, ok := payload["reasoningContentEvent"].(map[string]any); ok {
		if text := stringValue(firstValue(nested, "text", "content")); text != "" {
			return text
		}
	}
	if text := stringValue(firstValue(payload, "text", "content")); text != "" {
		return text
	}
	return ""
}

func (s *kiroStreamState) emitToolUse(payload map[string]any, out chan<- canonical.Event) {
	s.hasToolCalls = true
	items := []map[string]any{}
	if list, ok := payload["toolUses"].([]any); ok {
		for _, raw := range list {
			if item, ok := raw.(map[string]any); ok {
				items = append(items, item)
			}
		}
	} else if payload != nil {
		items = append(items, payload)
	}
	for _, toolUse := range items {
		toolID := stringValue(firstValue(toolUse, "toolUseId", "tool_use_id"))
		if toolID == "" {
			toolID = "call_" + uuid.NewString()
		}
		name := stringValue(toolUse["name"])
		input := toolUse["input"]
		if _, seen := s.seenToolIDs[toolID]; !seen {
			idx := s.toolCallIndex
			s.seenToolIDs[toolID] = idx
			s.toolCallIndex++
			out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: map[string]any{
				"index": idx,
				"id":    toolID,
				"type":  "function",
				"function": map[string]any{
					"name":      name,
					"arguments": "",
				},
			}}
		}
		if input == nil {
			continue
		}
		args := ""
		switch v := input.(type) {
		case string:
			args = v
		default:
			b, _ := json.Marshal(v)
			args = string(b)
		}
		if args == "" {
			continue
		}
		out <- canonical.Event{Type: canonical.EventToolCallDelta, ToolCall: map[string]any{
			"index": s.seenToolIDs[toolID],
			"function": map[string]any{
				"arguments": args,
			},
		}}
	}
}

func (s *kiroStreamState) captureMetrics(payload map[string]any) {
	metrics, _ := payload["metricsEvent"].(map[string]any)
	if metrics == nil {
		metrics = payload
	}
	in := numberValue(firstValue(metrics, "inputTokens", "input_tokens"))
	outTok := numberValue(firstValue(metrics, "outputTokens", "output_tokens"))
	if in == 0 && outTok == 0 {
		return
	}
	usage := canonical.Usage{InputTokens: in, OutputTokens: outTok}
	if cached := numberValue(firstValue(metrics, "cacheReadInputTokens", "cache_read_input_tokens")); cached > 0 {
		usage.CachedTokens = cached
	}
	s.usage = &usage
}

func estimateKiroUsage(s *kiroStreamState) *canonical.Usage {
	outTok := 0
	if s.totalContentLength > 0 {
		outTok = max(1, s.totalContentLength/4)
	}
	inTok := 0
	if s.contextUsagePct > 0 {
		inTok = int(float64(200000) * s.contextUsagePct / 100)
	}
	return &canonical.Usage{InputTokens: inTok, OutputTokens: outTok}
}

func kiroFinishReason(s *kiroStreamState) string {
	if s.hasToolCalls {
		return "tool_calls"
	}
	return "stop"
}

func collectKiroResponse(ctx context.Context, response *http.Response, model string) (*canonical.Response, error) {
	out := streamKiroEventStreamCollector(ctx, response, model)
	result := &canonical.Response{Model: model, Role: "assistant"}
	var text, reasoning strings.Builder
	toolCalls := map[int]map[string]any{}
	for event := range out {
		switch event.Type {
		case canonical.EventTextDelta:
			text.WriteString(event.Text)
		case canonical.EventReasoningDelta:
			reasoning.WriteString(event.Reasoning)
		case canonical.EventToolCallDelta:
			mergeToolCallDelta(toolCalls, event.ToolCall)
		case canonical.EventUsage:
			if event.Usage != nil {
				result.Usage = *event.Usage
			}
		case canonical.EventMessageEnd:
			result.FinishReason = event.FinishReason
		case canonical.EventError:
			if event.Err != nil {
				return nil, event.Err
			}
		}
	}
	result.Content = text.String()
	result.Reasoning = reasoning.String()
	if len(toolCalls) > 0 {
		result.ToolCalls = orderedToolCalls(toolCalls)
	}
	return result, nil
}

func streamKiroEventStreamCollector(ctx context.Context, response *http.Response, model string) <-chan canonical.Event {
	out := make(chan canonical.Event, 32)
	go func() {
		defer close(out)
		streamKiroEventStream(ctx, response, model, out)
	}()
	return out
}

func mergeToolCallDelta(toolCalls map[int]map[string]any, delta map[string]any) {
	idx := numberValue(delta["index"])
	existing := toolCalls[idx]
	if existing == nil {
		existing = map[string]any{"index": idx, "type": "function", "function": map[string]any{}}
		toolCalls[idx] = existing
	}
	if id := stringValue(delta["id"]); id != "" {
		existing["id"] = id
	}
	if fnDelta, ok := delta["function"].(map[string]any); ok {
		fn, _ := existing["function"].(map[string]any)
		if fn == nil {
			fn = map[string]any{}
			existing["function"] = fn
		}
		if name := stringValue(fnDelta["name"]); name != "" {
			fn["name"] = name
		}
		if args := stringValue(fnDelta["arguments"]); args != "" {
			fn["arguments"] = stringValue(fn["arguments"]) + args
		}
	}
}

func orderedToolCalls(toolCalls map[int]map[string]any) []map[string]any {
	maxIdx := -1
	for idx := range toolCalls {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	out := make([]map[string]any, 0, len(toolCalls))
	for i := 0; i <= maxIdx; i++ {
		if call := toolCalls[i]; call != nil {
			out = append(out, call)
		}
	}
	return out
}

func isKiroEventStream(response *http.Response) bool {
	ct := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(ct, "application/vnd.amazon.eventstream") {
		return true
	}
	peek := make([]byte, 4)
	n, _ := response.Body.Read(peek)
	if n == 4 && bytes.Equal(peek, []byte{0, 0, 0, 0}) == false {
		// Event stream frames start with total length uint32; re-wrap body.
		response.Body = io.NopCloser(io.MultiReader(bytes.NewReader(peek[:n]), response.Body))
		return binary.BigEndian.Uint32(peek) >= 16
	}
	response.Body = io.NopCloser(io.MultiReader(bytes.NewReader(peek[:n]), response.Body))
	return false
}
