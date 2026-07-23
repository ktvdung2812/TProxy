package cursor

const (
	BaseURL  = "https://api2.cursor.sh"
	ChatPath = "/aiserver.v1.ChatService/StreamUnifiedChatWithTools"
)

// StaticCursorModels mirrors 9router open-sse/providers/registry/cursor.js models array.
var StaticCursorModels = []struct {
	ID   string
	Name string
}{
	{ID: "default", Name: "Auto (Server Picks)"},
	{ID: "claude-4.5-opus-high-thinking", Name: "Claude 4.5 Opus High Thinking"},
	{ID: "claude-4.5-opus-high", Name: "Claude 4.5 Opus High"},
	{ID: "claude-4.5-sonnet-thinking", Name: "Claude 4.5 Sonnet Thinking"},
	{ID: "claude-4.5-sonnet", Name: "Claude 4.5 Sonnet"},
	{ID: "claude-4.5-haiku", Name: "Claude 4.5 Haiku"},
	{ID: "claude-4.5-opus", Name: "Claude 4.5 Opus"},
	{ID: "gpt-5.2-codex", Name: "GPT 5.2 Codex"},
	{ID: "claude-4.6-opus-max", Name: "Claude 4.6 Opus Max"},
	{ID: "claude-4.6-sonnet-medium-thinking", Name: "Claude 4.6 Sonnet Medium Thinking"},
	{ID: "kimi-k2.5", Name: "Kimi K2.5"},
	{ID: "gemini-3-flash-preview", Name: "Gemini 3 Flash Preview"},
	{ID: "gpt-5.2", Name: "GPT 5.2"},
	{ID: "gpt-5.3-codex", Name: "GPT 5.3 Codex"},
}
