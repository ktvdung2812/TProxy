package canonical

import "encoding/json"

type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolResponses Protocol = "responses"
	ProtocolClaude    Protocol = "claude"
	ProtocolGemini    Protocol = "gemini"
)

type ContentBlock struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Data map[string]any  `json:"data,omitempty"`
	Raw  json.RawMessage `json:"raw,omitempty"`
}

type Message struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []map[string]any `json:"tool_calls,omitempty"`
}

type Request struct {
	RequestID     string           `json:"request_id"`
	SessionID     string           `json:"session_id,omitempty"`
	Source        Protocol         `json:"source"`
	PublicModelID string           `json:"public_model_id"`
	UpstreamModel string           `json:"upstream_model"`
	Messages      []Message        `json:"messages,omitempty"`
	System        any              `json:"system,omitempty"`
	Tools         []map[string]any `json:"tools,omitempty"`
	ToolChoice    any              `json:"tool_choice,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	MaxTokens     int              `json:"max_tokens,omitempty"`
	Stream        bool             `json:"stream"`
	Reasoning     map[string]any   `json:"reasoning,omitempty"`
	Metadata      map[string]any   `json:"metadata,omitempty"`
	Raw           map[string]any   `json:"raw,omitempty"`
}

// InputTokens includes cached input; CachedTokens and CacheCreationTokens are subsets.
// OutputTokens includes reasoning; ReasoningTokens is a subset.
type Usage struct {
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	ReasoningTokens     int `json:"reasoning_tokens,omitempty"`
	CachedTokens        int `json:"cached_tokens,omitempty"`
}

type Response struct {
	ID           string           `json:"id"`
	Model        string           `json:"model"`
	Role         string           `json:"role,omitempty"`
	Content      any              `json:"content,omitempty"`
	ToolCalls    []map[string]any `json:"tool_calls,omitempty"`
	Reasoning    string           `json:"reasoning,omitempty"`
	FinishReason string           `json:"finish_reason,omitempty"`
	Usage        Usage            `json:"usage"`
	Raw          map[string]any   `json:"raw,omitempty"`
}

type EventType string

const (
	EventMessageStart   EventType = "message_start"
	EventTextDelta      EventType = "text_delta"
	EventReasoningDelta EventType = "reasoning_delta"
	EventToolCallDelta  EventType = "tool_call_delta"
	EventImageDelta     EventType = "image_delta"
	EventAudioDelta     EventType = "audio_delta"
	EventUsage          EventType = "usage"
	EventMessageEnd     EventType = "message_end"
	EventResponsesSSE   EventType = "responses_sse"
	EventError          EventType = "error"
	// EventUpstreamHeaders carries response headers captured from the upstream
	// HTTP response before the body stream begins (for example Codex's
	// x-codex-turn-state sticky-routing token and rate-limit header family).
	// Writers that cannot surface headers ignore it.
	EventUpstreamHeaders EventType = "upstream_headers"
)

type Event struct {
	Type               EventType         `json:"type"`
	ID                 string            `json:"id,omitempty"`
	Model              string            `json:"model,omitempty"`
	Text               string            `json:"text,omitempty"`
	Reasoning          string            `json:"reasoning,omitempty"`
	ReasoningEncrypted string            `json:"reasoning_encrypted,omitempty"`
	ReasoningItemID    string            `json:"reasoning_item_id,omitempty"`
	ToolCall           map[string]any    `json:"tool_call,omitempty"`
	Media              any               `json:"media,omitempty"`
	Usage              *Usage            `json:"usage,omitempty"`
	FinishReason       string            `json:"finish_reason,omitempty"`
	SSEEvent           string            `json:"sse_event,omitempty"`
	SSEData            []byte            `json:"sse_data,omitempty"`
	UpstreamHeaders    map[string]string `json:"-"`
	Err                error             `json:"-"`
}

func (u Usage) OpenAIUsage(responses bool) map[string]any {
	input, output, details := "prompt_tokens", "completion_tokens", "prompt_tokens_details"
	outputDetails := "completion_tokens_details"
	if responses {
		input, output, details, outputDetails = "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
	}
	return map[string]any{input: u.InputTokens, output: u.OutputTokens, "total_tokens": u.InputTokens + u.OutputTokens, details: map[string]any{"cached_tokens": u.CachedTokens, "cache_creation_tokens": u.CacheCreationTokens}, outputDetails: map[string]any{"reasoning_tokens": u.ReasoningTokens}}
}
func (u Usage) ClaudeUsage() map[string]any {
	return map[string]any{"input_tokens": max(0, u.InputTokens-u.CachedTokens-u.CacheCreationTokens), "output_tokens": u.OutputTokens, "cache_read_input_tokens": u.CachedTokens, "cache_creation_input_tokens": u.CacheCreationTokens}
}
