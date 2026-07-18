package providers

import (
	"encoding/binary"
	"testing"
)

func TestParseKiroEventFrameAssistantResponse(t *testing.T) {
	payload := `{"content":"hello"}`
	frame := buildTestKiroFrame("assistantResponseEvent", payload)
	headers, parsed := parseKiroEventFrame(frame)
	if headers[":event-type"] != "assistantResponseEvent" {
		t.Fatalf("event type = %q", headers[":event-type"])
	}
	if stringValue(parsed["content"]) != "hello" {
		t.Fatalf("payload = %+v", parsed)
	}
}

func buildTestKiroFrame(eventType, payload string) []byte {
	name := []byte(":event-type")
	value := []byte(eventType)
	headerBytes := []byte{byte(len(name))}
	headerBytes = append(headerBytes, name...)
	headerBytes = append(headerBytes, 7)
	headerBytes = append(headerBytes, byte(len(value)>>8), byte(len(value)))
	headerBytes = append(headerBytes, value...)
	body := []byte(payload)
	total := 12 + len(headerBytes) + len(body) + 4
	out := make([]byte, total)
	binary.BigEndian.PutUint32(out[0:4], uint32(total))
	binary.BigEndian.PutUint32(out[4:8], uint32(len(headerBytes)))
	copy(out[12:], headerBytes)
	copy(out[12+len(headerBytes):], body)
	return out
}
