package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Cloud Code reads requestId as an IDE trace identifier, not an opaque string.
// The Antigravity IDE builds it as
//
//	agent/<conversation uuid>/<unix millis>/<trajectory uuid>/<step>
//
// where the two UUIDs are stable for a conversation and for a
// conversation+model+kind pair respectively, and the step counts turns. A flat
// "agent-<uuid>" carries none of that structure.
//
// Ported from 9router's buildIdeRequestId, including its seeded UUID
// derivation, so a conversation replays the same identifiers the IDE would.
const antigravityStepsPerTurn = 2

// antigravityIDERequestID builds the IDE-shaped identifier for a request.
// sessionKey scopes the conversation, turnCount is the number of content
// entries being sent.
func antigravityIDERequestID(sessionKey, model, requestType string, turnCount int) string {
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = "anonymous"
	}
	conversation := antigravitySeededUUID("antigravity:conversation:" + sessionKey)
	trajectory := antigravitySeededUUID(fmt.Sprintf("antigravity:trajectory:%s:%s:%s", sessionKey, model, requestType))
	step := turnCount*antigravityStepsPerTurn - 1
	if step < 1 {
		step = 1
	}
	return fmt.Sprintf("agent/%s/%d/%s/%d", conversation, time.Now().UnixMilli(), trajectory, step)
}

// antigravitySeededUUID derives a deterministic RFC 4122 version 5 style UUID
// from a seed, matching 9router's uuidFromSeed so the same conversation yields
// the same identifier across requests and restarts.
func antigravitySeededUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	bytes := sum[:16]
	bytes[6] = (bytes[6] & 0x0f) | 0x50
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

// antigravityContentCount reports how many content entries a prepared inner
// request carries, which is what the IDE's step counter is derived from.
func antigravityContentCount(inner map[string]any) int {
	if inner == nil {
		return 1
	}
	if count := len(antigravityMapSlice(inner["contents"])); count > 0 {
		return count
	}
	return 1
}
