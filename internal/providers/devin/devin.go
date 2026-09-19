package devin

const (
	// BaseURL is the Codeium/Windsurf Cascade API host Devin CLI talks to.
	BaseURL = "https://server.codeium.com"
	// ChatPath is the Connect-RPC streaming chat endpoint.
	ChatPath = "/exa.api_server_pb.ApiServerService/GetChatMessage"
	// ModelConfigsPath returns the account's client model catalog.
	ModelConfigsPath = "/exa.api_server_pb.ApiServerService/GetCliModelConfigs"
	// UserStatusPath returns the account plan and quota snapshot.
	UserStatusPath = "/exa.seat_management_pb.SeatManagementService/GetUserStatus"
)

// StaticModels is the offline fallback catalog used when live discovery via
// GetCliModelConfigs is unavailable. Model UIDs follow Codeium's naming;
// legacy MODEL_* aliases also resolve server-side.
var StaticModels = []ModelEntry{
	{UID: "swe-1-7-lightning", Label: "SWE-1.7 Lightning Max"},
	{UID: "swe-1-7", Label: "SWE-1.7 Max"},
	{UID: "swe-1-7-medium", Label: "SWE-1.7 Medium"},
	{UID: "swe-1-6-fast", Label: "SWE-1.6 Fast"},
	{UID: "swe-1-6", Label: "SWE-1.6"},
	{UID: "swe-2-max", Label: "SWE-2 Max"},
	{UID: "swe-2-high", Label: "SWE-2 High"},
	{UID: "swe-2-medium", Label: "SWE-2 Medium"},
	{UID: "claude-opus-5-medium", Label: "Claude Opus 5 Medium"},
	{UID: "claude-opus-5-high", Label: "Claude Opus 5 High"},
	{UID: "claude-sonnet-5-medium", Label: "Claude Sonnet 5 Medium"},
	{UID: "claude-sonnet-5-high", Label: "Claude Sonnet 5 High"},
	{UID: "gemini-3-8-flash-medium", Label: "Gemini 3.8 Flash Medium"},
	{UID: "gemini-3-1-pro-high", Label: "Gemini 3.1 Pro High Thinking"},
	{UID: "gpt-5-6-sol-medium", Label: "GPT-5.6 Sol Medium Thinking"},
	{UID: "gpt-5-6-sol-high", Label: "GPT-5.6 Sol High Thinking"},
	{UID: "kimi-k3-high", Label: "Kimi K3 High"},
	{UID: "glm-5-3-high", Label: "GLM-5.3 High"},
	{UID: "deepseek-v4-pro-high", Label: "DeepSeek V4 Pro High"},
	{UID: "grok-4-6-high", Label: "Grok 4.6 High"},
}
