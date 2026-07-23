package cursor

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	wireTypeVarint  = 0
	wireTypeFixed64 = 1
	wireTypeLen     = 2
	wireTypeFixed32 = 5

	roleUser      = 1
	roleAssistant = 2

	unifiedModeChat  = 1
	unifiedModeAgent = 2

	thinkingLevelUnspecified = 0
	thinkingLevelMedium      = 1
	thinkingLevelHigh        = 2

	clientSideToolV2MCP = 19

	compressFlagNone       = 0x00
	compressFlagGzip       = 0x01
	compressFlagTrailer    = 0x02
	compressFlagGzipTrailer = 0x03
)

const (
	fieldRequest = 1

	fieldMessages           = 1
	fieldUnknown2           = 2
	fieldInstruction        = 3
	fieldUnknown4           = 4
	fieldModel              = 5
	fieldWebTool            = 8
	fieldUnknown13          = 13
	fieldCursorSetting      = 15
	fieldUnknown19          = 19
	fieldConversationID     = 23
	fieldMetadata           = 26
	fieldIsAgentic          = 27
	fieldSupportedTools     = 29
	fieldMessageIDs         = 30
	fieldMCPTools           = 34
	fieldLargeContext       = 35
	fieldUnknown38          = 38
	fieldUnifiedMode        = 46
	fieldUnknown47          = 47
	fieldShouldDisableTools = 48
	fieldThinkingLevel      = 49
	fieldUnknown51          = 51
	fieldUnknown53          = 53
	fieldUnifiedModeName    = 54

	fieldMsgContent         = 1
	fieldMsgRole            = 2
	fieldMsgID              = 13
	fieldMsgToolResults     = 18
	fieldMsgIsAgentic       = 29
	fieldMsgServerBubbleID  = 32
	fieldMsgUnifiedMode     = 47
	fieldMsgSupportedTools  = 51

	fieldToolResultCallID      = 1
	fieldToolResultName        = 2
	fieldToolResultIndex       = 3
	fieldToolResultRawArgs     = 5
	fieldToolResultResult      = 8
	fieldToolResultToolCall    = 11
	fieldToolResultModelCallID = 12

	fieldCV2RTool         = 1
	fieldCV2RMCPResult    = 28
	fieldCV2RCallID       = 35
	fieldCV2RModelCallID  = 48
	fieldCV2RToolIndex    = 49

	fieldMCPRSelectedTool = 1
	fieldMCPRResult       = 2

	fieldCV2CTool        = 1
	fieldCV2CMCPParams   = 27
	fieldCV2CCallID      = 3
	fieldCV2CName        = 9
	fieldCV2CRawArgs     = 10
	fieldCV2CToolIndex   = 48
	fieldCV2CModelCallID = 49

	fieldModelName  = 1
	fieldModelEmpty = 4

	fieldInstructionText = 1

	fieldSettingPath      = 1
	fieldSettingUnknown3  = 3
	fieldSettingUnknown6  = 6
	fieldSettingUnknown8  = 8
	fieldSettingUnknown9  = 9
	fieldSetting6Field1   = 1
	fieldSetting6Field2   = 2

	fieldMetaPlatform   = 1
	fieldMetaArch       = 2
	fieldMetaVersion    = 3
	fieldMetaCWD        = 4
	fieldMetaTimestamp  = 5

	fieldMsgIDID      = 1
	fieldMsgIDSummary = 2
	fieldMsgIDRole    = 3

	fieldMCPToolName   = 1
	fieldMCPToolDesc   = 2
	fieldMCPToolParams = 3
	fieldMCPToolServer = 4

	fieldToolCall     = 1
	fieldResponse     = 2
	fieldToolID       = 3
	fieldToolName     = 9
	fieldToolRawArgs  = 10
	fieldToolIsLast   = 11
	fieldToolMCPParams = 27

	fieldMCPToolsList     = 1
	fieldMCPNestedName    = 1
	fieldMCPNestedParams  = 3

	fieldResponseText = 1
	fieldThinking     = 25
	fieldThinkingText = 1
)

var knownResponseFields = map[int]struct{}{
	1:  {}, // fieldToolCall / fieldResponseText
	2:  {}, // fieldResponse
	3:  {}, // fieldToolID
	9:  {}, // fieldToolName
	10: {}, // fieldToolRawArgs
	11: {}, // fieldToolIsLast
	27: {}, // fieldToolMCPParams
	25: {}, // fieldThinking
}

var composerModelRe = regexp.MustCompile(`(?i)^composer(?:-|$)`)

// Message is an OpenAI-style chat message for Cursor protobuf encoding.
type Message struct {
	Role        string       `json:"role"`
	Content     string       `json:"content"`
	ToolCalls   []any        `json:"tool_calls,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

// ToolResult carries tool execution results inside a conversation message.
type ToolResult struct {
	ToolCallID    string `json:"tool_call_id,omitempty"`
	ToolName      string `json:"tool_name,omitempty"`
	Name          string `json:"name,omitempty"`
	RawArgs       string `json:"raw_args,omitempty"`
	ResultContent string `json:"result_content,omitempty"`
	Result        string `json:"result,omitempty"`
	ToolIndex     int    `json:"tool_index,omitempty"`
	Index         int    `json:"index,omitempty"`
}

// OpenAITool describes a tool definition in OpenAI format.
type OpenAITool struct {
	Type        string         `json:"type,omitempty"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Function    *struct {
		Name        string         `json:"name,omitempty"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters,omitempty"`
	} `json:"function,omitempty"`
}

// ExtractedToolCall is a parsed tool call from a Cursor response.
type ExtractedToolCall struct {
	ID       string
	Type     string
	Function ExtractedToolFunction
	IsLast   bool
}

// ExtractedToolFunction holds the function name and arguments from a tool call.
type ExtractedToolFunction struct {
	Name      string
	Arguments string
}

// ExtractResult holds parsed content from a Cursor protobuf response payload.
type ExtractResult struct {
	Text     *string
	Error    *string
	ToolCall *ExtractedToolCall
	Thinking *string
}

// ConnectRPCFrame is a parsed ConnectRPC 5-byte frame.
type ConnectRPCFrame struct {
	Flags    byte
	Length   uint32
	Payload  []byte
	Consumed int
}

type decodedField struct {
	wireType int
	value    any
}

// IsComposerModel reports whether model is a Cursor Composer model.
func IsComposerModel(model string) bool {
	modelID := model
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		modelID = model[idx+1:]
	}
	return composerModelRe.MatchString(modelID)
}

// VisibleComposerContentFromThinking returns user-visible content after </think>.
func VisibleComposerContentFromThinking(thinking string) string {
	if thinking == "" {
		return ""
	}
	const endTag = "</think>"
	endIdx := strings.LastIndex(thinking, endTag)
	if endIdx < 0 {
		return ""
	}
	return strings.TrimLeft(thinking[endIdx+len(endTag):], " \t\r\n")
}

// DecompressPayload decompresses a ConnectRPC payload based on frame flags.
func DecompressPayload(payload []byte, flags byte) []byte {
	if len(payload) > 10 && payload[0] == 0x7b && payload[1] == 0x22 {
		if strings.HasPrefix(string(payload), `{"error"`) {
			return payload
		}
	}

	if flags != compressFlagGzip && flags != compressFlagTrailer && flags != compressFlagGzipTrailer {
		return payload
	}

	if out, err := gunzipBytes(payload); err == nil {
		return out
	}
	if out, err := inflateBytes(payload); err == nil {
		return out
	}
	if out, err := inflateRawBytes(payload); err == nil {
		return out
	}
	return payload
}

// GenerateCursorBody builds the full ConnectRPC-framed protobuf body for a chat request.
func GenerateCursorBody(messages []Message, modelName string, tools []OpenAITool, reasoningEffort string, forceAgentMode bool) []byte {
	protobuf := buildChatRequest(messages, modelName, tools, reasoningEffort, forceAgentMode)
	return wrapConnectRPCFrame(protobuf, false)
}

// ParseConnectRPCFrame parses a ConnectRPC 5-byte framed message.
func ParseConnectRPCFrame(buffer []byte) *ConnectRPCFrame {
	if len(buffer) < 5 {
		return nil
	}

	flags := buffer[0]
	length := uint32(buffer[1])<<24 | uint32(buffer[2])<<16 | uint32(buffer[3])<<8 | uint32(buffer[4])
	if uint32(len(buffer)) < 5+length {
		return nil
	}

	payload := make([]byte, length)
	copy(payload, buffer[5:5+length])

	if flags == compressFlagGzip {
		if decompressed, err := gunzipBytes(payload); err == nil {
			payload = decompressed
		}
	}

	return &ConnectRPCFrame{
		Flags:    flags,
		Length:   length,
		Payload:  payload,
		Consumed: int(5 + length),
	}
}

// ExtractTextFromResponse extracts text, tool calls, thinking, or error from a protobuf payload.
func ExtractTextFromResponse(payload []byte) ExtractResult {
	defer func() {
		_ = recover()
	}()

	fields, err := decodeMessage(payload)
	if err != nil {
		return ExtractResult{}
	}

	for fieldNum := range fields {
		if _, ok := knownResponseFields[fieldNum]; !ok {
			_ = fieldNum
		}
	}

	if entries, ok := fields[fieldToolCall]; ok && len(entries) > 0 {
		if data, ok := entries[0].value.([]byte); ok {
			if toolCall := extractToolCall(data); toolCall != nil {
				return ExtractResult{ToolCall: toolCall}
			}
		}
	}

	if entries, ok := fields[fieldResponse]; ok && len(entries) > 0 {
		if data, ok := entries[0].value.([]byte); ok {
			text, thinking := extractTextAndThinking(data)
			if text != nil || thinking != nil {
				return ExtractResult{Text: text, Thinking: thinking}
			}
		}
	}

	return ExtractResult{}
}

func encodeVarint(value uint64) []byte {
	var bytes []byte
	v := value
	for v >= 0x80 {
		bytes = append(bytes, byte((v&0x7f)|0x80))
		v >>= 7
	}
	bytes = append(bytes, byte(v&0x7f))
	return bytes
}

func concatBytes(arrays ...[]byte) []byte {
	total := 0
	for _, arr := range arrays {
		total += len(arr)
	}
	out := make([]byte, total)
	offset := 0
	for _, arr := range arrays {
		copy(out[offset:], arr)
		offset += len(arr)
	}
	return out
}

func encodeField(fieldNum int, wireType int, value any) []byte {
	tag := uint64((fieldNum << 3) | wireType)
	tagBytes := encodeVarint(tag)

	switch wireType {
	case wireTypeVarint:
		var v uint64
		switch n := value.(type) {
		case int:
			v = uint64(n)
		case uint64:
			v = n
		case uint32:
			v = uint64(n)
		default:
			v = 0
		}
		return concatBytes(tagBytes, encodeVarint(v))
	case wireTypeLen:
		var dataBytes []byte
		switch v := value.(type) {
		case string:
			dataBytes = []byte(v)
		case []byte:
			dataBytes = v
		default:
			dataBytes = nil
		}
		lengthBytes := encodeVarint(uint64(len(dataBytes)))
		return concatBytes(tagBytes, lengthBytes, dataBytes)
	default:
		return nil
	}
}

func formatToolName(name string) string {
	base := name
	if base == "" {
		base = "tool"
	}
	if strings.HasPrefix(base, "mcp__") {
		rest := base[len("mcp__"):]
		if splitIdx := strings.Index(rest, "__"); splitIdx >= 0 {
			server := rest[:splitIdx]
			if server == "" {
				server = "custom"
			}
			toolName := rest[splitIdx+2:]
			if toolName == "" {
				toolName = "tool"
			}
			return "mcp_" + server + "_" + toolName
		}
		if rest == "" {
			rest = "tool"
		}
		return "mcp_custom_" + rest
	}
	if strings.HasPrefix(base, "mcp_") {
		return base
	}
	return "mcp_custom_" + base
}

func parseToolName(formattedName string) (serverName, selectedTool string) {
	if !strings.HasPrefix(formattedName, "mcp_") {
		if formattedName == "" {
			return "custom", "tool"
		}
		return "custom", formattedName
	}
	tail := formattedName[len("mcp_"):]
	splitIdx := strings.Index(tail, "_")
	if splitIdx < 0 {
		if tail == "" {
			return "custom", "tool"
		}
		return "custom", tail
	}
	serverName = tail[:splitIdx]
	if serverName == "" {
		serverName = "custom"
	}
	selectedTool = tail[splitIdx+1:]
	if selectedTool == "" {
		selectedTool = "tool"
	}
	return serverName, selectedTool
}

func parseToolID(id string) (toolCallID, modelCallID string, hasModelCallID bool) {
	const delimiter = "\nmc_"
	if idx := strings.Index(id, delimiter); idx >= 0 {
		return id[:idx], id[idx+len(delimiter):], true
	}
	return id, "", false
}

func encodeMcpResult(selectedTool, resultContent string) []byte {
	return concatBytes(
		encodeField(fieldMCPRSelectedTool, wireTypeLen, selectedTool),
		encodeField(fieldMCPRResult, wireTypeLen, resultContent),
	)
}

func encodeClientSideToolV2Result(toolCallID, modelCallID string, hasModelCallID bool, selectedTool, resultContent string, toolIndex int) []byte {
	if toolIndex <= 0 {
		toolIndex = 1
	}
	parts := [][]byte{
		encodeField(fieldCV2RTool, wireTypeVarint, clientSideToolV2MCP),
		encodeField(fieldCV2RMCPResult, wireTypeLen, encodeMcpResult(selectedTool, resultContent)),
		encodeField(fieldCV2RCallID, wireTypeLen, toolCallID),
	}
	if hasModelCallID {
		parts = append(parts, encodeField(fieldCV2RModelCallID, wireTypeLen, modelCallID))
	}
	parts = append(parts, encodeField(fieldCV2RToolIndex, wireTypeVarint, toolIndex))
	return concatBytes(parts...)
}

func encodeMcpParamsForCall(toolName, rawArgs, serverName string) []byte {
	tool := concatBytes(
		encodeField(fieldMCPToolName, wireTypeLen, toolName),
		encodeField(fieldMCPToolParams, wireTypeLen, rawArgs),
		encodeField(fieldMCPToolServer, wireTypeLen, serverName),
	)
	return encodeField(fieldMCPToolsList, wireTypeLen, tool)
}

func encodeClientSideToolV2Call(toolCallID, toolName, selectedTool, serverName, rawArgs, modelCallID string, hasModelCallID bool, toolIndex int) []byte {
	if toolIndex <= 0 {
		toolIndex = 1
	}
	parts := [][]byte{
		encodeField(fieldCV2CTool, wireTypeVarint, clientSideToolV2MCP),
		encodeField(fieldCV2CMCPParams, wireTypeLen, encodeMcpParamsForCall(selectedTool, rawArgs, serverName)),
		encodeField(fieldCV2CCallID, wireTypeLen, toolCallID),
		encodeField(fieldCV2CName, wireTypeLen, toolName),
		encodeField(fieldCV2CRawArgs, wireTypeLen, rawArgs),
		encodeField(fieldCV2CToolIndex, wireTypeVarint, toolIndex),
	}
	if hasModelCallID {
		parts = append(parts, encodeField(fieldCV2CModelCallID, wireTypeLen, modelCallID))
	}
	return concatBytes(parts...)
}

func encodeToolResult(toolResult ToolResult) []byte {
	originalName := toolResult.ToolName
	if originalName == "" {
		originalName = toolResult.Name
	}
	toolName := formatToolName(originalName)
	rawArgs := toolResult.RawArgs
	if rawArgs == "" {
		rawArgs = "{}"
	}
	resultContent := toolResult.ResultContent
	if resultContent == "" {
		resultContent = toolResult.Result
	}
	toolCallID, modelCallID, hasModelCallID := parseToolID(toolResult.ToolCallID)
	toolIndex := toolResult.ToolIndex
	if toolIndex == 0 {
		toolIndex = toolResult.Index
	}
	if toolIndex <= 0 {
		toolIndex = 1
	}
	serverName, selectedTool := parseToolName(toolName)

	parts := [][]byte{
		encodeField(fieldToolResultCallID, wireTypeLen, toolCallID),
		encodeField(fieldToolResultName, wireTypeLen, toolName),
		encodeField(fieldToolResultIndex, wireTypeVarint, toolIndex),
	}
	if hasModelCallID {
		parts = append(parts, encodeField(fieldToolResultModelCallID, wireTypeLen, modelCallID))
	}
	parts = append(parts,
		encodeField(fieldToolResultRawArgs, wireTypeLen, rawArgs),
		encodeField(fieldToolResultResult, wireTypeLen, encodeClientSideToolV2Result(toolCallID, modelCallID, hasModelCallID, selectedTool, resultContent, toolIndex)),
		encodeField(fieldToolResultToolCall, wireTypeLen, encodeClientSideToolV2Call(toolCallID, toolName, selectedTool, serverName, rawArgs, modelCallID, hasModelCallID, toolIndex)),
	)
	return concatBytes(parts...)
}

func encodeMessage(content string, role int, messageID string, isLast, hasTools bool, toolResults []ToolResult, serverBubbleID string) []byte {
	parts := [][]byte{
		encodeField(fieldMsgContent, wireTypeLen, content),
		encodeField(fieldMsgRole, wireTypeVarint, role),
		encodeField(fieldMsgID, wireTypeLen, messageID),
	}
	if serverBubbleID != "" {
		parts = append(parts, encodeField(fieldMsgServerBubbleID, wireTypeLen, serverBubbleID))
	}
	for _, tr := range toolResults {
		parts = append(parts, encodeField(fieldMsgToolResults, wireTypeLen, encodeToolResult(tr)))
	}
	agentic := 0
	if hasTools {
		agentic = 1
	}
	parts = append(parts, encodeField(fieldMsgIsAgentic, wireTypeVarint, agentic))
	mode := unifiedModeChat
	if hasTools {
		mode = unifiedModeAgent
	}
	parts = append(parts, encodeField(fieldMsgUnifiedMode, wireTypeVarint, mode))
	if isLast && hasTools {
		parts = append(parts, encodeField(fieldMsgSupportedTools, wireTypeLen, encodeVarint(1)))
	}
	return concatBytes(parts...)
}

func encodeInstruction(text string) []byte {
	if text == "" {
		return nil
	}
	return encodeField(fieldInstructionText, wireTypeLen, text)
}

func encodeModel(modelName string) []byte {
	return concatBytes(
		encodeField(fieldModelName, wireTypeLen, modelName),
		encodeField(fieldModelEmpty, wireTypeLen, []byte{}),
	)
}

func encodeCursorSetting() []byte {
	unknown6 := concatBytes(
		encodeField(fieldSetting6Field1, wireTypeLen, []byte{}),
		encodeField(fieldSetting6Field2, wireTypeLen, []byte{}),
	)
	return concatBytes(
		encodeField(fieldSettingPath, wireTypeLen, `cursor\aisettings`),
		encodeField(fieldSettingUnknown3, wireTypeLen, []byte{}),
		encodeField(fieldSettingUnknown6, wireTypeLen, unknown6),
		encodeField(fieldSettingUnknown8, wireTypeVarint, 1),
		encodeField(fieldSettingUnknown9, wireTypeVarint, 1),
	)
}

func encodeMetadata() []byte {
	platform := runtime.GOOS
	arch := runtime.GOARCH
	version := runtime.Version()
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		cwd = "/"
	}
	return concatBytes(
		encodeField(fieldMetaPlatform, wireTypeLen, platform),
		encodeField(fieldMetaArch, wireTypeLen, arch),
		encodeField(fieldMetaVersion, wireTypeLen, version),
		encodeField(fieldMetaCWD, wireTypeLen, cwd),
		encodeField(fieldMetaTimestamp, wireTypeLen, time.Now().UTC().Format(time.RFC3339Nano)),
	)
}

func encodeMessageId(messageID string, role int, summaryID string) []byte {
	parts := [][]byte{
		encodeField(fieldMsgIDID, wireTypeLen, messageID),
	}
	if summaryID != "" {
		parts = append(parts, encodeField(fieldMsgIDSummary, wireTypeLen, summaryID))
	}
	parts = append(parts, encodeField(fieldMsgIDRole, wireTypeVarint, role))
	return concatBytes(parts...)
}

func encodeMcpTool(tool OpenAITool) []byte {
	toolName := ""
	toolDesc := ""
	var inputSchema map[string]any
	if tool.Function != nil {
		toolName = tool.Function.Name
		toolDesc = tool.Function.Description
		inputSchema = tool.Function.Parameters
	}
	if toolName == "" {
		toolName = tool.Name
	}
	if toolDesc == "" {
		toolDesc = tool.Description
	}
	if inputSchema == nil {
		inputSchema = tool.InputSchema
	}

	var parts [][]byte
	if toolName != "" {
		parts = append(parts, encodeField(fieldMCPToolName, wireTypeLen, toolName))
	}
	if toolDesc != "" {
		parts = append(parts, encodeField(fieldMCPToolDesc, wireTypeLen, toolDesc))
	}
	if len(inputSchema) > 0 {
		if b, err := json.Marshal(inputSchema); err == nil {
			parts = append(parts, encodeField(fieldMCPToolParams, wireTypeLen, string(b)))
		}
	}
	parts = append(parts, encodeField(fieldMCPToolServer, wireTypeLen, "custom"))
	return concatBytes(parts...)
}

func normalizeMessages(messages []Message) []Message {
	var normalized []Message
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		hasToolCalls := len(msg.ToolCalls) > 0
		hasToolResults := len(msg.ToolResults) > 0

		if msg.Role == "assistant" && hasToolCalls && hasToolResults {
			normalized = append(normalized, Message{
				Role:        msg.Role,
				Content:     msg.Content,
				ToolCalls:   msg.ToolCalls,
				ToolResults: nil,
			})

			sameIDs := false
			if i+1 < len(messages) {
				next := messages[i+1]
				nextHasToolResults := next.Role == "assistant" && len(next.ToolResults) > 0
				currentIDs := make(map[string]struct{})
				for _, tr := range msg.ToolResults {
					if tr.ToolCallID != "" {
						currentIDs[tr.ToolCallID] = struct{}{}
					}
				}
				nextIDs := make(map[string]struct{})
				for _, tr := range next.ToolResults {
					if tr.ToolCallID != "" {
						nextIDs[tr.ToolCallID] = struct{}{}
					}
				}
				sameIDs = len(currentIDs) > 0 && len(currentIDs) == len(nextIDs)
				if sameIDs {
					for id := range currentIDs {
						if _, ok := nextIDs[id]; !ok {
							sameIDs = false
							break
						}
					}
				}
				if nextHasToolResults && sameIDs {
					continue
				}
			}

			normalized = append(normalized, Message{
				Role:        "assistant",
				Content:     "",
				ToolResults: msg.ToolResults,
			})
			continue
		}
		normalized = append(normalized, msg)
	}
	return normalized
}

func encodeRequest(messages []Message, modelName string, tools []OpenAITool, reasoningEffort string, forceAgentMode bool) []byte {
	hasTools := len(tools) > 0
	isAgentic := hasTools || forceAgentMode

	normalizedMessages := normalizeMessages(messages)

	type formattedMessage struct {
		content     string
		role        int
		messageID   string
		isLast      bool
		hasTools    bool
		toolResults []ToolResult
	}

	formatted := make([]formattedMessage, 0, len(normalizedMessages))
	messageIDs := make([]struct {
		messageID string
		role      int
	}, 0, len(normalizedMessages))

	for i, msg := range normalizedMessages {
		role := roleAssistant
		if msg.Role == "user" {
			role = roleUser
		}
		msgID := uuid.New().String()
		formatted = append(formatted, formattedMessage{
			content:     msg.Content,
			role:        role,
			messageID:   msgID,
			isLast:      i == len(normalizedMessages)-1,
			hasTools:    hasTools,
			toolResults: msg.ToolResults,
		})
		messageIDs = append(messageIDs, struct {
			messageID string
			role      int
		}{messageID: msgID, role: role})
	}

	thinkingLevel := thinkingLevelUnspecified
	switch reasoningEffort {
	case "medium":
		thinkingLevel = thinkingLevelMedium
	case "high":
		thinkingLevel = thinkingLevelHigh
	}

	var parts [][]byte
	for _, fm := range formatted {
		parts = append(parts, encodeField(fieldMessages, wireTypeLen, encodeMessage(
			fm.content, fm.role, fm.messageID, fm.isLast, fm.hasTools, fm.toolResults, "",
		)))
	}

	parts = append(parts,
		encodeField(fieldUnknown2, wireTypeVarint, 1),
		encodeField(fieldInstruction, wireTypeLen, encodeInstruction("")),
		encodeField(fieldUnknown4, wireTypeVarint, 1),
		encodeField(fieldModel, wireTypeLen, encodeModel(modelName)),
		encodeField(fieldWebTool, wireTypeLen, ""),
		encodeField(fieldUnknown13, wireTypeVarint, 1),
		encodeField(fieldCursorSetting, wireTypeLen, encodeCursorSetting()),
		encodeField(fieldUnknown19, wireTypeVarint, 1),
		encodeField(fieldConversationID, wireTypeLen, uuid.New().String()),
		encodeField(fieldMetadata, wireTypeLen, encodeMetadata()),
		encodeField(fieldIsAgentic, wireTypeVarint, boolToInt(isAgentic)),
	)
	if isAgentic {
		parts = append(parts, encodeField(fieldSupportedTools, wireTypeLen, encodeVarint(1)))
	}
	for _, mid := range messageIDs {
		parts = append(parts, encodeField(fieldMessageIDs, wireTypeLen, encodeMessageId(mid.messageID, mid.role, "")))
	}
	for _, tool := range tools {
		parts = append(parts, encodeField(fieldMCPTools, wireTypeLen, encodeMcpTool(tool)))
	}

	mode := unifiedModeChat
	modeName := "Ask"
	disableTools := 1
	if isAgentic {
		mode = unifiedModeAgent
		modeName = "Agent"
		disableTools = 0
	}

	parts = append(parts,
		encodeField(fieldLargeContext, wireTypeVarint, 0),
		encodeField(fieldUnknown38, wireTypeVarint, 0),
		encodeField(fieldUnifiedMode, wireTypeVarint, mode),
		encodeField(fieldUnknown47, wireTypeLen, ""),
		encodeField(fieldShouldDisableTools, wireTypeVarint, disableTools),
		encodeField(fieldThinkingLevel, wireTypeVarint, thinkingLevel),
		encodeField(fieldUnknown51, wireTypeVarint, 0),
		encodeField(fieldUnknown53, wireTypeVarint, 1),
		encodeField(fieldUnifiedModeName, wireTypeLen, modeName),
	)

	return concatBytes(parts...)
}

func buildChatRequest(messages []Message, modelName string, tools []OpenAITool, reasoningEffort string, forceAgentMode bool) []byte {
	return encodeField(fieldRequest, wireTypeLen, encodeRequest(messages, modelName, tools, reasoningEffort, forceAgentMode))
}

func wrapConnectRPCFrame(payload []byte, compress bool) []byte {
	finalPayload := payload
	flags := byte(compressFlagNone)

	if compress {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write(payload)
		_ = gz.Close()
		finalPayload = buf.Bytes()
		flags = compressFlagGzip
	}

	frame := make([]byte, 5+len(finalPayload))
	frame[0] = flags
	length := uint32(len(finalPayload))
	frame[1] = byte(length >> 24)
	frame[2] = byte(length >> 16)
	frame[3] = byte(length >> 8)
	frame[4] = byte(length)
	copy(frame[5:], finalPayload)
	return frame
}

func decodeVarint(buffer []byte, offset int) (uint64, int, error) {
	var result uint64
	var shift uint
	pos := offset
	for pos < len(buffer) {
		b := buffer[pos]
		result |= uint64(b&0x7f) << shift
		pos++
		if b&0x80 == 0 {
			return result, pos, nil
		}
		shift += 7
		if shift > 63 {
			return 0, pos, io.ErrUnexpectedEOF
		}
	}
	return 0, pos, io.ErrUnexpectedEOF
}

func decodeField(buffer []byte, offset int) (fieldNum int, wireType int, value any, newOffset int, err error) {
	if offset >= len(buffer) {
		return 0, 0, nil, offset, io.EOF
	}

	tag, pos, err := decodeVarint(buffer, offset)
	if err != nil {
		return 0, 0, nil, offset, err
	}
	fieldNum = int(tag >> 3)
	wireType = int(tag & 0x07)

	switch wireType {
	case wireTypeVarint:
		var v uint64
		v, pos, err = decodeVarint(buffer, pos)
		if err != nil {
			return 0, 0, nil, offset, err
		}
		return fieldNum, wireType, v, pos, nil
	case wireTypeLen:
		var length uint64
		length, pos, err = decodeVarint(buffer, pos)
		if err != nil {
			return 0, 0, nil, offset, err
		}
		end := pos + int(length)
		if end > len(buffer) {
			return 0, 0, nil, offset, io.ErrUnexpectedEOF
		}
		value = append([]byte(nil), buffer[pos:end]...)
		return fieldNum, wireType, value, end, nil
	case wireTypeFixed64:
		end := pos + 8
		if end > len(buffer) {
			return 0, 0, nil, offset, io.ErrUnexpectedEOF
		}
		value = append([]byte(nil), buffer[pos:end]...)
		return fieldNum, wireType, value, end, nil
	case wireTypeFixed32:
		end := pos + 4
		if end > len(buffer) {
			return 0, 0, nil, offset, io.ErrUnexpectedEOF
		}
		value = append([]byte(nil), buffer[pos:end]...)
		return fieldNum, wireType, value, end, nil
	default:
		return fieldNum, wireType, nil, pos, nil
	}
}

func decodeMessage(data []byte) (map[int][]decodedField, error) {
	fields := make(map[int][]decodedField)
	pos := 0
	for pos < len(data) {
		fieldNum, wireType, value, newPos, err := decodeField(data, pos)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		fields[fieldNum] = append(fields[fieldNum], decodedField{wireType: wireType, value: value})
		pos = newPos
	}
	return fields, nil
}

func extractToolCall(toolCallData []byte) *ExtractedToolCall {
	toolCall, err := decodeMessage(toolCallData)
	if err != nil {
		return nil
	}

	var toolCallID, toolName, rawArgs string
	isLast := false

	if entries, ok := toolCall[fieldToolID]; ok && len(entries) > 0 {
		if data, ok := entries[0].value.([]byte); ok {
			fullID := string(data)
			toolCallID = strings.SplitN(fullID, "\n", 2)[0]
		}
	}
	if entries, ok := toolCall[fieldToolName]; ok && len(entries) > 0 {
		if data, ok := entries[0].value.([]byte); ok {
			toolName = string(data)
		}
	}
	if entries, ok := toolCall[fieldToolIsLast]; ok && len(entries) > 0 {
		if v, ok := entries[0].value.(uint64); ok {
			isLast = v != 0
		}
	}

	if entries, ok := toolCall[fieldToolMCPParams]; ok && len(entries) > 0 {
		if data, ok := entries[0].value.([]byte); ok {
			if mcpParams, err := decodeMessage(data); err == nil {
				if toolsList, ok := mcpParams[fieldMCPToolsList]; ok && len(toolsList) > 0 {
					if toolData, ok := toolsList[0].value.([]byte); ok {
						if tool, err := decodeMessage(toolData); err == nil {
							if nameEntries, ok := tool[fieldMCPNestedName]; ok && len(nameEntries) > 0 {
								if nameData, ok := nameEntries[0].value.([]byte); ok {
									toolName = string(nameData)
								}
							}
							if paramEntries, ok := tool[fieldMCPNestedParams]; ok && len(paramEntries) > 0 {
								if paramData, ok := paramEntries[0].value.([]byte); ok {
									rawArgs = string(paramData)
								}
							}
						}
					}
				}
			}
		}
	}

	if rawArgs == "" {
		if entries, ok := toolCall[fieldToolRawArgs]; ok && len(entries) > 0 {
			if data, ok := entries[0].value.([]byte); ok {
				rawArgs = string(data)
			}
		}
	}

	if toolCallID != "" && toolName != "" {
		if rawArgs == "" {
			rawArgs = "{}"
		}
		return &ExtractedToolCall{
			ID:   toolCallID,
			Type: "function",
			Function: ExtractedToolFunction{
				Name:      toolName,
				Arguments: rawArgs,
			},
			IsLast: isLast,
		}
	}
	return nil
}

func extractTextAndThinking(responseData []byte) (text, thinking *string) {
	nested, err := decodeMessage(responseData)
	if err != nil {
		return nil, nil
	}

	if entries, ok := nested[fieldResponseText]; ok && len(entries) > 0 {
		if data, ok := entries[0].value.([]byte); ok {
			s := string(data)
			text = &s
		}
	}

	if entries, ok := nested[fieldThinking]; ok && len(entries) > 0 {
		if data, ok := entries[0].value.([]byte); ok {
			if thinkingMsg, err := decodeMessage(data); err == nil {
				if textEntries, ok := thinkingMsg[fieldThinkingText]; ok && len(textEntries) > 0 {
					if thinkingData, ok := textEntries[0].value.([]byte); ok {
						s := string(thinkingData)
						thinking = &s
					}
				}
			}
		}
	}

	return text, thinking
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func gunzipBytes(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func inflateBytes(payload []byte) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(payload))
	defer reader.Close()
	return io.ReadAll(reader)
}

func inflateRawBytes(payload []byte) ([]byte, error) {
	reader := flate.NewReaderDict(bytes.NewReader(payload), nil)
	defer reader.Close()
	return io.ReadAll(reader)
}
