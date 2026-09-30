package devin

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"testing"
)

func TestBuildChatRequestRoundTrip(t *testing.T) {
	temp := 0.7
	req := ChatRequest{
		APIKey:       "devin-session-token$test-key",
		SystemPrompt: "You are a helpful assistant.",
		Messages: []ChatMessage{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "hi there", Thinking: "let me think", Signature: "sig",
				ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file", ArgumentsJSON: `{"path":"a.go"}`}}},
			{Role: "tool", Text: "file contents", ToolCallID: "call_1"},
			{Role: "user", Text: "thanks", Images: []Image{{Base64Data: "aGk=", MimeType: "image/png"}}},
		},
		ModelUID:    "swe-1-6-fast",
		SessionID:   "sess-1",
		CascadeID:   "cascade-1",
		MaxTokens:   1024,
		Temperature: &temp,
		Tools:       []Tool{{Name: "read_file", Description: "read a file", JSONSchema: `{"type":"object"}`}},
	}
	ids := []string{"m1", "m2", "m3", "m4"}
	fields, err := decodeMessage(BuildChatRequest(req, ids))
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}

	// metadata: chisel client identity, session token in api_key, no user_jwt
	meta, err := decodeMessage(messageField(fields, 1))
	if err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if got := stringField(meta, 3); got != "devin-session-token$test-key" {
		t.Errorf("api_key = %q", got)
	}
	if got := stringField(meta, 1); got != "chisel" {
		t.Errorf("ide_name = %q", got)
	}
	if got := stringField(meta, 12); got != "chisel" {
		t.Errorf("extension_name = %q", got)
	}
	if got := stringField(meta, 2); got != clientVersion {
		t.Errorf("extension_version = %q", got)
	}
	if got := stringField(meta, 7); got != clientVersion {
		t.Errorf("ide_version = %q", got)
	}
	if got := len(stringField(meta, 31)); got != deviceFingerprintLen {
		t.Errorf("device_fingerprint len = %d, want %d", got, deviceFingerprintLen)
	}
	if got := stringField(meta, 21); got != "" {
		t.Errorf("user_jwt should be absent, got %q", got)
	}

	if got := stringField(fields, 2); got != "You are a helpful assistant." {
		t.Errorf("prompt = %q", got)
	}

	// chat_message_prompts
	prompts := fields[3]
	if len(prompts) != 4 {
		t.Fatalf("chat_message_prompts = %d, want 4", len(prompts))
	}
	p0, _ := decodeMessage(prompts[0].data)
	if got := stringField(p0, 3); got != "hello" {
		t.Errorf("msg0 prompt = %q", got)
	}
	if got := varintField(p0, 2); got != chatSourceUser {
		t.Errorf("msg0 source = %d", got)
	}
	if got := stringField(p0, 1); got != "m1" {
		t.Errorf("msg0 message_id = %q", got)
	}
	p1, _ := decodeMessage(prompts[1].data)
	if got := varintField(p1, 2); got != chatSourceSystem {
		t.Errorf("assistant source = %d", got)
	}
	if got := stringField(p1, 11); got != "let me think" {
		t.Errorf("assistant thinking = %q", got)
	}
	if got := stringField(p1, 12); got != "sig" {
		t.Errorf("assistant signature = %q", got)
	}
	tc, _ := decodeMessage(messageField(p1, 6))
	if got := stringField(tc, 2); got != "read_file" {
		t.Errorf("tool_call name = %q", got)
	}
	if got := stringField(tc, 3); got != `{"path":"a.go"}` {
		t.Errorf("tool_call args = %q", got)
	}
	p2, _ := decodeMessage(prompts[2].data)
	if got := varintField(p2, 2); got != chatSourceTool {
		t.Errorf("tool source = %d", got)
	}
	if got := stringField(p2, 7); got != "call_1" {
		t.Errorf("tool_call_id = %q", got)
	}
	p3, _ := decodeMessage(prompts[3].data)
	img, _ := decodeMessage(messageField(p3, 10))
	if got := stringField(img, 1); got != "aGk=" {
		t.Errorf("image base64 = %q", got)
	}
	if got := stringField(img, 2); got != "image/png" {
		t.Errorf("image mime = %q", got)
	}

	// scalars
	if got := varintField(fields, 7); got != requestTypeCascade {
		t.Errorf("request_type = %d", got)
	}
	if got := stringField(fields, 16); got != "cascade-1" {
		t.Errorf("cascade_id = %q", got)
	}
	if got := varintField(fields, 20); got != plannerModeDefault {
		t.Errorf("planner_mode = %d", got)
	}
	if got := stringField(fields, 21); got != "swe-1-6-fast" {
		t.Errorf("chat_model_uid = %q", got)
	}
	// fields absent in the native minimal request
	for _, absent := range []int{11, 12, 13, 22} {
		if len(fields[absent]) != 0 {
			t.Errorf("field %d should be absent", absent)
		}
	}

	// session block (field 15): session_id, fixed 3=4, user-turn boundary 4=14
	sess, err := decodeMessage(messageField(fields, 15))
	if err != nil {
		t.Fatalf("decode session block: %v", err)
	}
	if got := stringField(sess, 1); got != "sess-1" {
		t.Errorf("session_id = %q", got)
	}
	if got := varintField(sess, 3); got != 4 {
		t.Errorf("session field3 = %d", got)
	}

	// configuration: minimal native shape
	cfg, err := decodeMessage(messageField(fields, 8))
	if err != nil {
		t.Fatalf("decode configuration: %v", err)
	}
	if got := varintField(cfg, 2); got != 1024 {
		t.Errorf("max_tokens = %d", got)
	}
	if got := varintField(cfg, 1); got != 1 {
		t.Errorf("num_completions = %d", got)
	}
	if got := varintField(cfg, 3); got != 400 {
		t.Errorf("max_newlines = %d", got)
	}
	if got := varintField(cfg, 7); got != 40 {
		t.Errorf("top_k = %d", got)
	}
	if got := stringField(cfg, 9); got != "" {
		t.Errorf("stop_patterns should be absent, got %q", got)
	}
	tempBits := varintField(cfg, 5)
	if got := uint64(0x3fe6666666666666); tempBits != got { // 0.7 as float64 LE bits
		t.Errorf("temperature bits = %#x", tempBits)
	}

	// tools
	tool, _ := decodeMessage(messageField(fields, 10))
	if got := stringField(tool, 1); got != "read_file" {
		t.Errorf("tool name = %q", got)
	}
	if got := stringField(tool, 3); got != `{"type":"object"}` {
		t.Errorf("tool schema = %q", got)
	}
}

func TestSessionTurnIndex(t *testing.T) {
	if got := NextSessionTurnIndex("turn-test"); got != 0 {
		t.Fatalf("first turn = %d, want 0", got)
	}
	if got := NextSessionTurnIndex("turn-test"); got != 1 {
		t.Fatalf("second turn = %d, want 1", got)
	}
	if got := NextSessionTurnIndex("turn-test-2"); got != 0 {
		t.Fatalf("other session turn = %d, want 0", got)
	}
}

func TestConnectFrameRoundTrip(t *testing.T) {
	payload := []byte("hello protobuf")
	frame := WrapConnectFrame(payload, true)
	if frame[0] != connectFlagCompressed {
		t.Fatalf("flags = %#x", frame[0])
	}
	length := binary.BigEndian.Uint32(frame[1:5])
	if int(length) != len(frame)-5 {
		t.Fatalf("length = %d", length)
	}
	reader, err := gzip.NewReader(bytes.NewReader(frame[5:]))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("payload = %q", body)
	}
	if got := DecodeConnectPayload(frame[5:], connectFlagCompressed); !bytes.Equal(got, payload) {
		t.Fatalf("DecodeConnectPayload = %q", got)
	}
}

func TestParseChatResponse(t *testing.T) {
	toolCall := concatBytes(
		encodeString(1, "call_9"),
		encodeString(2, "write_file"),
		encodeString(3, `{"a":1`),
	)
	usage := concatBytes(
		encodeUint(2, 1500),
		encodeUint(3, 220),
		encodeUint(5, 900),
		encodeString(9, "swe-1-6-fast"),
	)
	payload := concatBytes(
		encodeString(1, "msg-1"),
		encodeString(3, "partial text"),
		encodeUint(5, 10),
		encodeMessage(6, toolCall),
		encodeMessage(7, usage),
		encodeString(9, "thinking..."),
		encodeString(10, "sig-data"),
		encodeString(23, "actual-uid"),
	)
	delta, err := ParseChatResponse(payload)
	if err != nil {
		t.Fatalf("ParseChatResponse: %v", err)
	}
	if delta.MessageID != "msg-1" || delta.Text != "partial text" {
		t.Fatalf("delta = %+v", delta)
	}
	if delta.StopReason != stopReasonFunctionCall {
		t.Errorf("stop_reason = %d", delta.StopReason)
	}
	if delta.Thinking != "thinking..." || delta.Signature != "sig-data" {
		t.Errorf("thinking/signature = %q/%q", delta.Thinking, delta.Signature)
	}
	if len(delta.ToolCalls) != 1 || delta.ToolCalls[0].Name != "write_file" {
		t.Fatalf("tool calls = %+v", delta.ToolCalls)
	}
	if delta.Usage == nil || delta.Usage.InputTokens != 1500 || delta.Usage.CacheReadTokens != 900 {
		t.Fatalf("usage = %+v", delta.Usage)
	}
	if delta.ActualModelUID != "actual-uid" {
		t.Errorf("actual_model_uid = %q", delta.ActualModelUID)
	}
}

func TestParseCliModelConfigsResponse(t *testing.T) {
	features := encodeBool(15, true) // supports_thinking
	info := encodeMessage(6, features)
	cfg := func(uid, label string, disabled bool, images bool) []byte {
		return concatBytes(
			encodeString(1, label),
			encodeString(22, uid),
			encodeBool(4, disabled),
			encodeBool(5, images),
			encodeUint(10, 3),
			encodeUint(18, 200000),
			encodeMessage(23, info),
		)
	}
	payload := concatBytes(
		encodeMessage(1, cfg("swe-1-6-fast", "SWE-1.6 Fast", false, true)),
		encodeMessage(1, cfg("disabled-model", "Hidden", true, false)),
	)
	entries, err := ParseCliModelConfigsResponse(payload)
	if err != nil {
		t.Fatalf("ParseCliModelConfigsResponse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	got := entries[0]
	if got.UID != "swe-1-6-fast" || got.Label != "SWE-1.6 Fast" || !got.SupportsImages || !got.SupportsThinking || got.ContextLength != 200000 || got.VendorID != 3 {
		t.Fatalf("entry = %+v", got)
	}
	if VendorName(got.VendorID) != "anthropic" {
		t.Errorf("vendor = %q", VendorName(got.VendorID))
	}
}

func TestUserStatusRoundTrip(t *testing.T) {
	body := BuildGetUserStatusRequest("devin-session-token$k")
	fields, err := decodeMessage(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	meta, _ := decodeMessage(messageField(fields, 1))
	if got := stringField(meta, 3); got != "devin-session-token$k" {
		t.Errorf("api_key = %q", got)
	}
	if got := stringField(meta, 1); got != "chisel" {
		t.Errorf("ide_name = %q", got)
	}

	org := concatBytes(encodeString(4, "org-1"), encodeString(8, "Acme Org"))
	planInfo := concatBytes(encodeString(2, "teams"), encodeMessage(33, org))
	planStatus := concatBytes(
		encodeMessage(1, planInfo),
		encodeUint(14, 62),
		encodeUint(15, 80),
		encodeUint(17, 1700000000),
	)
	userStatus := concatBytes(
		encodeString(3, "devuser"),
		encodeString(5, "team-1"),
		encodeString(7, "dev@example.com"),
		encodeMessage(13, planStatus),
		encodeString(36, "user-1"),
	)
	resp := encodeMessage(1, userStatus)
	status, err := ParseGetUserStatusResponse(resp)
	if err != nil {
		t.Fatalf("ParseGetUserStatusResponse: %v", err)
	}
	if status.Email != "dev@example.com" || status.UserName != "devuser" || status.UserID != "user-1" || status.TeamID != "team-1" {
		t.Fatalf("status = %+v", status)
	}
	if status.Plan != "teams" || status.OrgID != "org-1" || status.OrgName != "Acme Org" {
		t.Fatalf("plan = %+v", status)
	}
	if status.DailyQuotaRemainingPercent != 62 || status.WeeklyQuotaRemainingPercent != 80 || status.DailyQuotaResetAt != 1700000000 {
		t.Fatalf("quota = %+v", status)
	}
}

func TestTrailerError(t *testing.T) {
	if got := TrailerError([]byte(`{"error":{"code":"resource_exhausted","message":"quota exhausted"}}`)); got != "resource_exhausted: quota exhausted" {
		t.Fatalf("TrailerError = %q", got)
	}
	if got := TrailerError([]byte(`{}`)); got != "" {
		t.Fatalf("TrailerError empty = %q", got)
	}
}
