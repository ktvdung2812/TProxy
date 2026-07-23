package cursor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGenerateHashed64Hex(t *testing.T) {
	got := GenerateHashed64Hex("hello", "machineId")
	sum := sha256.Sum256([]byte("hellomachineId"))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("GenerateHashed64Hex() = %q, want %q", got, want)
	}
}

func TestGenerateSessionId(t *testing.T) {
	token := "test-auth-token"
	got := GenerateSessionId(token)
	want := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(token)).String()
	if got != want {
		t.Fatalf("GenerateSessionId() = %q, want %q", got, want)
	}
}

func TestGenerateCursorChecksumSuffix(t *testing.T) {
	machineID := "abc123machine"
	checksum := GenerateCursorChecksum(machineID)
	if !strings.HasSuffix(checksum, machineID) {
		t.Fatalf("checksum %q should end with machine id %q", checksum, machineID)
	}
	prefix := strings.TrimSuffix(checksum, machineID)
	if prefix == "" {
		t.Fatal("expected non-empty encoded checksum prefix")
	}
}

func TestBuildCursorHeadersTokenCleaning(t *testing.T) {
	headers := BuildCursorHeaders("user::secret-token", nil, true)
	if headers["authorization"] != "Bearer secret-token" {
		t.Fatalf("authorization = %q", headers["authorization"])
	}
	if headers["x-session-id"] != GenerateSessionId("secret-token") {
		t.Fatalf("unexpected session id")
	}
	if headers["x-client-key"] != GenerateHashed64Hex("secret-token", "") {
		t.Fatalf("unexpected client key")
	}
}

func cursorResponseFrame(text, thinking string) []byte {
	const lenWire = 2
	var responseFields [][]byte
	if text != "" {
		responseFields = append(responseFields, encodeField(fieldResponseText, lenWire, text))
	}
	if thinking != "" {
		thinkingMessage := encodeField(fieldThinkingText, lenWire, thinking)
		responseFields = append(responseFields, encodeField(fieldThinking, lenWire, thinkingMessage))
	}
	response := concatBytes(responseFields...)
	envelope := encodeField(fieldResponse, lenWire, response)
	return wrapConnectRPCFrame(envelope, false)
}

func extractComposerVisibleContent(model string, frames [][]byte) string {
	var totalThinking strings.Builder
	for _, frame := range frames {
		parsed := ParseConnectRPCFrame(frame)
		if parsed == nil {
			continue
		}
		result := ExtractTextFromResponse(parsed.Payload)
		if result.Thinking != nil {
			totalThinking.WriteString(*result.Thinking)
		}
	}
	if IsComposerModel(model) {
		return VisibleComposerContentFromThinking(totalThinking.String())
	}
	return ""
}

func TestVisibleComposerContentFromThinking(t *testing.T) {
	got := VisibleComposerContentFromThinking("private reasoning that must not leak</think>OK")
	if got != "OK" {
		t.Fatalf("got %q, want %q", got, "OK")
	}
	if strings.Contains(got, "private reasoning") {
		t.Fatal("visible content leaked private reasoning")
	}
}

func TestIsComposerModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"cu/composer-2.5", true},
		{"composer-2.5-fast", true},
		{"gpt-5.3-codex", false},
	}
	for _, tc := range cases {
		if got := IsComposerModel(tc.model); got != tc.want {
			t.Fatalf("IsComposerModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestComposerThinkingNonStreamingResponse(t *testing.T) {
	buffer := cursorResponseFrame("", "private reasoning that must not leak</think>OK")
	parsed := ParseConnectRPCFrame(buffer)
	if parsed == nil {
		t.Fatal("failed to parse frame")
	}
	result := ExtractTextFromResponse(parsed.Payload)
	if result.Thinking == nil {
		t.Fatal("expected thinking in response")
	}

	visible := VisibleComposerContentFromThinking(*result.Thinking)
	if visible != "OK" {
		t.Fatalf("visible content = %q, want OK", visible)
	}
	if strings.Contains(visible, "private reasoning") {
		t.Fatal("visible content leaked private reasoning")
	}
}

func TestComposerThinkingStreamingResponse(t *testing.T) {
	frames := [][]byte{
		cursorResponseFrame("", "private reasoning"),
		cursorResponseFrame("", " that must not leak</think>O"),
		cursorResponseFrame("", "K"),
	}
	content := extractComposerVisibleContent("composer-2.5-fast", frames)
	if content != "OK" {
		t.Fatalf("streamed visible content = %q, want OK", content)
	}
	if strings.Contains(content, "private reasoning") {
		t.Fatal("streamed content leaked private reasoning")
	}
}

func TestNonComposerModelDoesNotExposeThinking(t *testing.T) {
	buffer := cursorResponseFrame("", "private reasoning</think>SHOULD_NOT_APPEAR")
	parsed := ParseConnectRPCFrame(buffer)
	if parsed == nil {
		t.Fatal("failed to parse frame")
	}
	result := ExtractTextFromResponse(parsed.Payload)
	visible := extractComposerVisibleContent("gpt-5.3-codex", [][]byte{buffer})
	if visible != "" {
		t.Fatalf("non-composer visible content = %q, want empty", visible)
	}
	if result.Text != nil {
		t.Fatalf("expected nil text, got %q", *result.Text)
	}
}

func TestDecompressPayloadGzip(t *testing.T) {
	payload := []byte("hello cursor")
	framed := wrapConnectRPCFrame(payload, true)
	parsed := ParseConnectRPCFrame(framed)
	if parsed == nil {
		t.Fatal("failed to parse gzip frame")
	}
	if !bytes.Equal(parsed.Payload, payload) {
		t.Fatalf("decompressed payload = %q, want %q", parsed.Payload, payload)
	}
}

func TestGenerateCursorBodyForceAgentMode(t *testing.T) {
	body := GenerateCursorBody(
		[]Message{{Role: "user", Content: "hi"}},
		"gpt-5.3-codex",
		nil,
		"",
		true,
	)
	if len(body) < 5 {
		t.Fatal("expected framed body")
	}
	parsed := ParseConnectRPCFrame(body)
	if parsed == nil {
		t.Fatal("failed to parse generated body")
	}
	fields, err := decodeMessage(parsed.Payload)
	if err != nil {
		t.Fatal(err)
	}
	requestEntries, ok := fields[fieldRequest]
	if !ok || len(requestEntries) == 0 {
		t.Fatal("missing request field")
	}
	requestData, ok := requestEntries[0].value.([]byte)
	if !ok {
		t.Fatal("invalid request payload")
	}
	requestFields, err := decodeMessage(requestData)
	if err != nil {
		t.Fatal(err)
	}
	isAgentic, ok := requestFields[fieldIsAgentic]
	if !ok || len(isAgentic) == 0 {
		t.Fatal("missing is_agentic field")
	}
	val, ok := isAgentic[0].value.(uint64)
	if !ok || val != 1 {
		t.Fatalf("is_agentic = %v, want 1 with forceAgentMode", isAgentic[0].value)
	}
}
