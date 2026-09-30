// Package devin implements the Codeium Cascade chat protocol used by Devin CLI:
// Connect-RPC (application/connect+proto) calls to server.codeium.com, with
// hand-rolled protobuf encoding so no generated code or proto toolchain is
// required. Field numbers mirror exa.api_server_pb.ApiServerService /
// exa.codeium_common_pb / exa.chat_pb / exa.auth_pb as exercised by the public
// Devin CLI wire format.
package devin

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	wireTypeVarint  = 0
	wireTypeFixed64 = 1
	wireTypeLen     = 2

	connectFlagCompressed = 0x01
	connectFlagEndStream  = 0x02
)

const (
	chatSourceUser   = 1 // CHAT_MESSAGE_SOURCE_USER
	chatSourceSystem = 2 // CHAT_MESSAGE_SOURCE_SYSTEM (assistant turns use this)
	chatSourceTool   = 4 // CHAT_MESSAGE_SOURCE_TOOL

	requestTypeCascade = 5 // CHAT_MESSAGE_REQUEST_TYPE_CASCADE

	plannerModeDefault = 1 // CONVERSATIONAL_PLANNER_MODE_DEFAULT

	stopReasonUnspecified  = 0
	stopReasonMaxTokens    = 3
	stopReasonFunctionCall = 10

	// clientName/clientVersion identify as the Devin CLI ("chisel" client).
	clientName            = "chisel"
	clientVersion         = "3000.10.21"
	deviceFingerprintLen  = 732
	defaultMaxTokens      = 128000
	maxSessionTurnEntries = 5000
)

// ChatMessage is one conversation turn in the canonical request.
type ChatMessage struct {
	Role       string
	Text       string
	Thinking   string
	Signature  string
	ToolCalls  []ToolCall
	ToolCallID string
	IsError    bool
	Images     []Image
}

// ToolCall is an OpenAI-style tool call (id + function name + JSON arguments).
type ToolCall struct {
	ID            string
	Name          string
	ArgumentsJSON string
}

// Image is a base64 image attached to a message.
type Image struct {
	Base64Data string
	MimeType   string
}

// Tool is an OpenAI-style tool definition.
type Tool struct {
	Name        string
	Description string
	JSONSchema  string
}

// ChatRequest carries everything needed to build a GetChatMessageRequest.
type ChatRequest struct {
	APIKey       string
	SystemPrompt string
	Messages     []ChatMessage
	ModelUID     string
	SessionID    string
	CascadeID    string
	MaxTokens    int
	Temperature  *float64
	TopP         *float64
	Tools        []Tool
}

// ChatDelta is one parsed GetChatMessageResponse stream frame.
type ChatDelta struct {
	MessageID      string
	Text           string
	Thinking       string
	Signature      string
	StopReason     int
	ToolCalls      []ToolCall
	Usage          *Usage
	ActualModelUID string
}

// Usage mirrors exa.codeium_common_pb.ModelUsageStats.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ModelUID         string
}

// ModelEntry is one model row from GetCliModelConfigs.
type ModelEntry struct {
	UID              string
	Label            string
	SupportsImages   bool
	SupportsThinking bool
	ContextLength    int
	VendorID         int
}

func encodeVarint(value uint64) []byte {
	var out []byte
	for value >= 0x80 {
		out = append(out, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}

func concatBytes(parts ...[]byte) []byte {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	out := make([]byte, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func encodeTag(fieldNum, wireType int) []byte {
	return encodeVarint(uint64(fieldNum<<3 | wireType))
}

func encodeString(fieldNum int, value string) []byte {
	if value == "" {
		return nil
	}
	data := []byte(value)
	return concatBytes(encodeTag(fieldNum, wireTypeLen), encodeVarint(uint64(len(data))), data)
}

func encodeBytes(fieldNum int, data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return concatBytes(encodeTag(fieldNum, wireTypeLen), encodeVarint(uint64(len(data))), data)
}

func encodeMessage(fieldNum int, data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return concatBytes(encodeTag(fieldNum, wireTypeLen), encodeVarint(uint64(len(data))), data)
}

func encodeUint(fieldNum int, value uint64) []byte {
	if value == 0 {
		return nil
	}
	return concatBytes(encodeTag(fieldNum, wireTypeVarint), encodeVarint(value))
}

// encodeUintAlways writes a varint field even when the value is zero (oneof
// members and explicit-false bools must be present on the wire).
func encodeUintAlways(fieldNum int, value uint64) []byte {
	return concatBytes(encodeTag(fieldNum, wireTypeVarint), encodeVarint(value))
}

func encodeBool(fieldNum int, value bool) []byte {
	if !value {
		return nil
	}
	return encodeUintAlways(fieldNum, 1)
}

func encodeDouble(fieldNum int, value float64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(value))
	return concatBytes(encodeTag(fieldNum, wireTypeFixed64), buf[:])
}

// ---- message decoders ----

type fieldValue struct {
	wireType int
	varint   uint64
	data     []byte
}

func decodeMessage(payload []byte) (map[int][]fieldValue, error) {
	fields := map[int][]fieldValue{}
	offset := 0
	for offset < len(payload) {
		tag, n := binary.Uvarint(payload[offset:])
		if n <= 0 {
			return nil, fmt.Errorf("invalid protobuf tag at offset %d", offset)
		}
		offset += n
		fieldNum := int(tag >> 3)
		wireType := int(tag & 0x7)
		switch wireType {
		case wireTypeVarint:
			value, vn := binary.Uvarint(payload[offset:])
			if vn <= 0 {
				return nil, fmt.Errorf("invalid varint at offset %d", offset)
			}
			offset += vn
			fields[fieldNum] = append(fields[fieldNum], fieldValue{wireType: wireType, varint: value})
		case wireTypeFixed64:
			if offset+8 > len(payload) {
				return nil, io.ErrUnexpectedEOF
			}
			fields[fieldNum] = append(fields[fieldNum], fieldValue{wireType: wireType, varint: binary.LittleEndian.Uint64(payload[offset:])})
			offset += 8
		case wireTypeLen:
			length, ln := binary.Uvarint(payload[offset:])
			if ln <= 0 {
				return nil, fmt.Errorf("invalid length at offset %d", offset)
			}
			offset += ln
			if offset+int(length) > len(payload) {
				return nil, io.ErrUnexpectedEOF
			}
			data := make([]byte, length)
			copy(data, payload[offset:offset+int(length)])
			offset += int(length)
			fields[fieldNum] = append(fields[fieldNum], fieldValue{wireType: wireType, data: data})
		case 5: // fixed32
			if offset+4 > len(payload) {
				return nil, io.ErrUnexpectedEOF
			}
			fields[fieldNum] = append(fields[fieldNum], fieldValue{wireType: wireType, varint: uint64(binary.LittleEndian.Uint32(payload[offset:]))})
			offset += 4
		default:
			return nil, fmt.Errorf("unsupported wire type %d", wireType)
		}
	}
	return fields, nil
}

func stringField(fields map[int][]fieldValue, num int) string {
	for _, value := range fields[num] {
		if value.wireType == wireTypeLen {
			return string(value.data)
		}
	}
	return ""
}

func varintField(fields map[int][]fieldValue, num int) uint64 {
	for _, value := range fields[num] {
		if value.wireType == wireTypeVarint || value.wireType == wireTypeFixed64 || value.wireType == 5 {
			return value.varint
		}
	}
	return 0
}

func messageField(fields map[int][]fieldValue, num int) []byte {
	for _, value := range fields[num] {
		if value.wireType == wireTypeLen {
			return value.data
		}
	}
	return nil
}

// ---- protobuf message builders ----

// GenerateDeviceFingerprint produces the 732-hex device fingerprint Devin CLI
// attaches as metadata field 31. Native devin-cli generates a fresh random one
// per request; a deterministic value is derived when seed is non-empty.
func GenerateDeviceFingerprint(seed string) string {
	if seed == "" {
		var b [deviceFingerprintLen / 2]byte
		if _, err := rand.Read(b[:]); err == nil {
			return hex.EncodeToString(b[:])
		}
	}
	var sb strings.Builder
	for counter := 0; sb.Len() < deviceFingerprintLen; counter++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", seed, counter)))
		sb.WriteString(hex.EncodeToString(h[:]))
	}
	return sb.String()[:deviceFingerprintLen]
}

var sessionTurns = struct {
	sync.Mutex
	counters map[string]*atomic.Uint64
}{counters: map[string]*atomic.Uint64{}}

// NextSessionTurnIndex returns the next 0-based request ordinal for a session
// (session block field 15.2, omitted on the wire when 0).
func NextSessionTurnIndex(sessionID string) int {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return 0
	}
	sessionTurns.Lock()
	if len(sessionTurns.counters) >= maxSessionTurnEntries {
		sessionTurns.counters = map[string]*atomic.Uint64{}
	}
	counter, ok := sessionTurns.counters[id]
	if !ok {
		counter = &atomic.Uint64{}
		sessionTurns.counters[id] = counter
	}
	sessionTurns.Unlock()
	return int(counter.Add(1) - 1)
}

// Metadata mirrors exa.codeium_common_pb.Metadata with the field subset the
// Devin CLI ("chisel" client) sends: api_key carries the session token
// directly — no JWT exchange is needed for chat.
func encodeMetadata(apiKey string) []byte {
	return concatBytes(
		encodeString(1, clientName),                     // ide_name
		encodeString(2, clientVersion),                  // extension_version
		encodeString(3, apiKey),                         // api_key
		encodeString(4, "en"),                           // locale
		encodeString(5, runtime.GOOS),                   // os
		encodeString(7, clientVersion),                  // ide_version
		encodeString(12, clientName),                    // extension_name
		encodeString(31, GenerateDeviceFingerprint("")), // device_fingerprint
	)
}

// BuildGetCliModelConfigsRequest encodes GetCliModelConfigsRequest.
func BuildGetCliModelConfigsRequest(apiKey string) []byte {
	return encodeMessage(1, encodeMetadata(apiKey))
}

// BuildGetUserStatusRequest encodes
// exa.seat_management_pb.GetUserStatusRequest{metadata=1}.
func BuildGetUserStatusRequest(apiKey string) []byte {
	return encodeMessage(1, encodeMetadata(apiKey))
}

// ParseCliModelConfigsResponse decodes repeated ClientModelConfig{label=1,
// disabled=4, supports_images=5, vendor=10, context_length=18,
// model_uid=22, model_info=23{model_features=6{supports_thinking=15}}}.
func ParseCliModelConfigsResponse(payload []byte) ([]ModelEntry, error) {
	fields, err := decodeMessage(payload)
	if err != nil {
		return nil, err
	}
	entries := make([]ModelEntry, 0, len(fields[1]))
	for _, value := range fields[1] {
		if value.wireType != wireTypeLen {
			continue
		}
		cfg, err := decodeMessage(value.data)
		if err != nil {
			continue
		}
		if varintField(cfg, 4) != 0 { // disabled
			continue
		}
		uid := strings.TrimSpace(stringField(cfg, 22))
		if uid == "" {
			continue
		}
		entry := ModelEntry{
			UID:            uid,
			Label:          strings.TrimSpace(stringField(cfg, 1)),
			SupportsImages: varintField(cfg, 5) != 0,
			ContextLength:  int(varintField(cfg, 18)),
			VendorID:       int(varintField(cfg, 10)),
		}
		if info := messageField(cfg, 23); info != nil {
			if infoFields, err := decodeMessage(info); err == nil {
				if features := messageField(infoFields, 6); features != nil {
					if featureFields, err := decodeMessage(features); err == nil {
						entry.SupportsThinking = varintField(featureFields, 15) != 0
					}
				}
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// VendorName maps the ClientModelConfig vendor field to a display name.
func VendorName(id int) string {
	switch id {
	case 1:
		return "cognition"
	case 2:
		return "openai"
	case 3:
		return "anthropic"
	case 4:
		return "google"
	case 6:
		return "deepseek"
	case 7:
		return "moonshot"
	case 9:
		return "zhipu"
	case 11:
		return "nvidia"
	default:
		return "devin"
	}
}

// UserStatus is the account plan/quota snapshot from GetUserStatus.
type UserStatus struct {
	Email                       string
	UserName                    string
	UserID                      string
	TeamID                      string
	OrgID                       string
	OrgName                     string
	Plan                        string
	DailyQuotaRemainingPercent  int
	WeeklyQuotaRemainingPercent int
	DailyQuotaResetAt           int64
	WeeklyQuotaResetAt          int64
	PlanStart                   int64
	PlanEnd                     int64
}

// ParseGetUserStatusResponse decodes
// GetUserStatusResponse{user_status=1{user_name=3, team_id=5, email=7,
// plan_status=13{plan_info=1{name=2, org=33{id=4,name=8}}, start=2{s=1},
// end=3{s=1}, daily_quota_remaining=14, weekly_quota_remaining=15,
// daily_reset=17, weekly_reset=18}, user_id=36}}.
func ParseGetUserStatusResponse(payload []byte) (*UserStatus, error) {
	fields, err := decodeMessage(payload)
	if err != nil {
		return nil, err
	}
	status := &UserStatus{}
	us := messageField(fields, 1)
	if us == nil {
		return status, nil
	}
	usFields, err := decodeMessage(us)
	if err != nil {
		return status, nil
	}
	status.UserName = stringField(usFields, 3)
	status.TeamID = stringField(usFields, 5)
	status.Email = stringField(usFields, 7)
	status.UserID = stringField(usFields, 36)
	plan := messageField(usFields, 13)
	if plan == nil {
		return status, nil
	}
	planFields, err := decodeMessage(plan)
	if err != nil {
		return status, nil
	}
	status.DailyQuotaRemainingPercent = int(varintField(planFields, 14))
	status.WeeklyQuotaRemainingPercent = int(varintField(planFields, 15))
	status.DailyQuotaResetAt = int64(varintField(planFields, 17))
	status.WeeklyQuotaResetAt = int64(varintField(planFields, 18))
	// plan_status fields 2/3 are start/end timestamps as {seconds=1}.
	status.PlanStart = secondsSubfield(messageField(planFields, 2))
	status.PlanEnd = secondsSubfield(messageField(planFields, 3))
	if info := messageField(planFields, 1); info != nil {
		if infoFields, err := decodeMessage(info); err == nil {
			status.Plan = stringField(infoFields, 2)
			if org := messageField(infoFields, 33); org != nil {
				if orgFields, err := decodeMessage(org); err == nil {
					status.OrgID = stringField(orgFields, 4)
					status.OrgName = stringField(orgFields, 8)
				}
			}
		}
	}
	return status, nil
}

// secondsSubfield unwraps a {seconds=1} timestamp message into unix seconds.
func secondsSubfield(payload []byte) int64 {
	if payload == nil {
		return 0
	}
	fields, err := decodeMessage(payload)
	if err != nil {
		return 0
	}
	return int64(varintField(fields, 1))
}

func encodeToolCall(call ToolCall) []byte {
	return concatBytes(
		encodeString(1, call.ID),
		encodeString(2, call.Name),
		encodeString(3, call.ArgumentsJSON),
	)
}

func encodeImage(image Image) []byte {
	return concatBytes(
		encodeString(1, image.Base64Data),
		encodeString(2, image.MimeType),
	)
}

func encodeChatMessagePrompt(message ChatMessage, messageID string) []byte {
	source := chatSourceUser
	switch message.Role {
	case "assistant":
		source = chatSourceSystem
	case "tool":
		source = chatSourceTool
	default:
		source = chatSourceUser
	}
	parts := [][]byte{
		encodeString(1, messageID),
		encodeUintAlways(2, uint64(source)),
		encodeString(3, message.Text),
	}
	for _, call := range message.ToolCalls {
		parts = append(parts, encodeMessage(6, encodeToolCall(call)))
	}
	parts = append(parts,
		encodeString(7, message.ToolCallID),
		encodeBool(9, message.IsError),
	)
	for _, image := range message.Images {
		parts = append(parts, encodeMessage(10, encodeImage(image)))
	}
	parts = append(parts,
		encodeString(11, message.Thinking),
		encodeString(12, message.Signature),
	)
	return concatBytes(parts...)
}

// encodeCompletionConfiguration matches the minimal CompletionConfiguration
// native devin-cli sends: no first_temperature, no stop patterns, top_p as a
// float32-rounded 0.95 default.
func encodeCompletionConfiguration(request ChatRequest) []byte {
	temperature := 1.0
	if request.Temperature != nil {
		temperature = *request.Temperature
	}
	topP := float64(float32(0.95))
	if request.TopP != nil {
		topP = *request.TopP
	}
	maxTokens := uint64(defaultMaxTokens)
	if request.MaxTokens > 0 {
		maxTokens = uint64(request.MaxTokens)
	}
	return concatBytes(
		encodeUint(1, 1),         // num_completions
		encodeUint(2, maxTokens), // max_tokens
		encodeUint(3, 400),       // max_newlines
		encodeDouble(5, temperature),
		encodeUint(7, 40), // top_k
		encodeDouble(8, topP),
	)
}

func encodeChatToolDefinition(tool Tool) []byte {
	return concatBytes(
		encodeString(1, tool.Name),
		encodeString(2, tool.Description),
		encodeString(3, tool.JSONSchema),
	)
}

// encodeSessionBlock builds the field-15 thread session metadata native
// devin-cli emits: {session_id=1, turn_index=2 (omitted when 0), fixed=3:4,
// user_turn_boundary=4:14 emitted when the last prompt is a user turn that
// starts a new user-turn boundary}.
func encodeSessionBlock(sessionID string, turnIndex int, prompts []ChatMessage) []byte {
	parts := [][]byte{
		encodeString(1, sessionID),
		encodeUint(2, uint64(turnIndex)),
		encodeUintAlways(3, 4),
	}
	if len(prompts) > 0 && prompts[len(prompts)-1].Role == "user" {
		if turnIndex == 0 || len(prompts) < 2 || prompts[len(prompts)-2].Role != "user" {
			parts = append(parts, encodeUintAlways(4, 14))
		}
	}
	return concatBytes(parts...)
}

// BuildChatRequest encodes GetChatMessageRequest.
func BuildChatRequest(request ChatRequest, messageIDs []string) []byte {
	parts := [][]byte{
		encodeMessage(1, encodeMetadata(request.APIKey)),
		encodeString(2, request.SystemPrompt),
	}
	for i, message := range request.Messages {
		id := ""
		if i < len(messageIDs) {
			id = messageIDs[i]
		}
		parts = append(parts, encodeMessage(3, encodeChatMessagePrompt(message, id)))
	}
	parts = append(parts,
		encodeUintAlways(7, requestTypeCascade),
		encodeMessage(8, encodeCompletionConfiguration(request)),
	)
	for _, tool := range request.Tools {
		parts = append(parts, encodeMessage(10, encodeChatToolDefinition(tool)))
	}
	sessionID := request.SessionID
	if sessionID == "" {
		sessionID = request.CascadeID
	}
	parts = append(parts,
		encodeMessage(15, encodeSessionBlock(sessionID, NextSessionTurnIndex(sessionID), request.Messages)),
		encodeString(16, request.CascadeID),
		encodeUintAlways(20, plannerModeDefault),
		encodeString(21, request.ModelUID),
	)
	return concatBytes(parts...)
}

// ParseChatResponse decodes one GetChatMessageResponse frame.
func ParseChatResponse(payload []byte) (*ChatDelta, error) {
	fields, err := decodeMessage(payload)
	if err != nil {
		return nil, err
	}
	delta := &ChatDelta{
		MessageID:      stringField(fields, 1),
		Text:           stringField(fields, 3),
		StopReason:     int(varintField(fields, 5)),
		Thinking:       stringField(fields, 9),
		Signature:      stringField(fields, 10),
		ActualModelUID: stringField(fields, 23),
	}
	for _, value := range fields[6] {
		if value.wireType != wireTypeLen {
			continue
		}
		if callFields, err := decodeMessage(value.data); err == nil {
			delta.ToolCalls = append(delta.ToolCalls, ToolCall{
				ID:            stringField(callFields, 1),
				Name:          stringField(callFields, 2),
				ArgumentsJSON: stringField(callFields, 3),
			})
		}
	}
	if usage := messageField(fields, 7); usage != nil {
		if usageFields, err := decodeMessage(usage); err == nil {
			delta.Usage = &Usage{
				InputTokens:      int(varintField(usageFields, 2)),
				OutputTokens:     int(varintField(usageFields, 3)),
				CacheWriteTokens: int(varintField(usageFields, 4)),
				CacheReadTokens:  int(varintField(usageFields, 5)),
				ModelUID:         stringField(usageFields, 9),
			}
		}
	}
	return delta, nil
}

// ---- Connect-RPC framing ----

// WrapConnectFrame builds a Connect streaming envelope: 1-byte flags +
// big-endian uint32 length + (optionally gzip-compressed) payload.
func WrapConnectFrame(payload []byte, compress bool) []byte {
	flags := byte(0)
	body := payload
	if compress {
		var buf bytes.Buffer
		writer := gzip.NewWriter(&buf)
		if _, err := writer.Write(payload); err == nil {
			if err := writer.Close(); err == nil {
				body = buf.Bytes()
				flags = connectFlagCompressed
			}
		}
	}
	frame := make([]byte, 5+len(body))
	frame[0] = flags
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)
	return frame
}

func gunzipBytes(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

// DecodeConnectPayload returns the uncompressed payload for a frame body.
func DecodeConnectPayload(payload []byte, flags byte) []byte {
	if flags&connectFlagCompressed == 0 {
		return payload
	}
	if out, err := gunzipBytes(payload); err == nil {
		return out
	}
	return payload
}

// MaybeGunzip decompresses payload when it carries the gzip magic prefix;
// unary Connect responses (application/proto) arrive either raw or gzipped.
func MaybeGunzip(payload []byte) []byte {
	if len(payload) > 2 && payload[0] == 0x1f && payload[1] == 0x8b {
		if out, err := gunzipBytes(payload); err == nil {
			return out
		}
	}
	return payload
}

// IsEndStream reports whether the frame flags mark the end-of-stream trailer.
func IsEndStream(flags byte) bool {
	return flags&connectFlagEndStream != 0
}

// TrailerError extracts {"error":{"code","message"}} from a Connect
// end-of-stream JSON trailer, or "" when the trailer carries no error.
func TrailerError(payload []byte) string {
	var trailer map[string]any
	if err := json.Unmarshal(payload, &trailer); err != nil {
		return ""
	}
	errObj, _ := trailer["error"].(map[string]any)
	if errObj == nil {
		return ""
	}
	message, _ := errObj["message"].(string)
	code, _ := errObj["code"].(string)
	if message == "" {
		return ""
	}
	if code != "" {
		return code + ": " + message
	}
	return message
}

// StopReasonIsTerminal reports whether a STOP_REASON_* value ends the turn.
func StopReasonIsTerminal(reason int) bool {
	return reason != stopReasonUnspecified
}

// FinishReason maps the wire stop reason to an OpenAI-style finish reason.
func FinishReason(stopReason int, sawToolCalls bool) string {
	if sawToolCalls || stopReason == stopReasonFunctionCall {
		return "tool_calls"
	}
	if stopReason == stopReasonMaxTokens {
		return "length"
	}
	return "stop"
}
