package cursor

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

const cursorClientVersion = "3.1.0"

// GenerateHashed64Hex returns SHA-256(input+salt) as a 64-char hex string.
func GenerateHashed64Hex(input, salt string) string {
	sum := sha256.Sum256([]byte(input + salt))
	return hex.EncodeToString(sum[:])
}

// GenerateSessionId returns a deterministic UUID v5 (DNS namespace) for the auth token.
func GenerateSessionId(authToken string) string {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(authToken)).String()
}

// GenerateCursorChecksum builds the x-cursor-checksum value (Jyh cipher).
func GenerateCursorChecksum(machineID string) string {
	timestamp := time.Now().UnixMilli() / 1_000_000

	byteArray := [6]byte{
		byte(timestamp >> 40),
		byte(timestamp >> 32),
		byte(timestamp >> 24),
		byte(timestamp >> 16),
		byte(timestamp >> 8),
		byte(timestamp),
	}

	t := byte(165)
	for i := range byteArray {
		byteArray[i] = byte((int(byteArray[i]^t) + (i % 256)) & 0xff)
		t = byteArray[i]
	}

	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var encoded strings.Builder
	for i := 0; i < len(byteArray); i += 3 {
		a := byteArray[i]
		b := byte(0)
		c := byte(0)
		if i+1 < len(byteArray) {
			b = byteArray[i+1]
		}
		if i+2 < len(byteArray) {
			c = byteArray[i+2]
		}

		encoded.WriteByte(alphabet[a>>2])
		encoded.WriteByte(alphabet[((a&3)<<4)|(b>>4)])

		if i+1 < len(byteArray) {
			encoded.WriteByte(alphabet[((b&15)<<2)|(c>>6)])
		}
		if i+2 < len(byteArray) {
			encoded.WriteByte(alphabet[c&63])
		}
	}

	return encoded.String() + machineID
}

// BuildCursorHeaders returns all required Cursor API request headers.
func BuildCursorHeaders(accessToken string, machineID *string, ghostMode bool) map[string]string {
	cleanToken := accessToken
	if idx := strings.Index(accessToken, "::"); idx >= 0 {
		cleanToken = accessToken[idx+2:]
	}

	effectiveMachineID := ""
	if machineID != nil && *machineID != "" {
		effectiveMachineID = *machineID
	} else {
		effectiveMachineID = GenerateHashed64Hex(cleanToken, "machineId")
	}

	sessionID := GenerateSessionId(cleanToken)
	clientKey := GenerateHashed64Hex(cleanToken, "")
	checksum := GenerateCursorChecksum(effectiveMachineID)

	osName := "linux"
	switch runtime.GOOS {
	case "windows":
		osName = "windows"
	case "darwin":
		osName = "macos"
	}

	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}

	ghost := "false"
	if ghostMode {
		ghost = "true"
	}

	tz := "UTC"
	if loc := time.Now().Location(); loc != nil {
		if name := loc.String(); name != "" {
			tz = name
		}
	}

	return map[string]string{
		"authorization":               "Bearer " + cleanToken,
		"connect-accept-encoding":     "gzip",
		"connect-protocol-version":    "1",
		"content-type":                "application/connect+proto",
		"user-agent":                  "connect-es/1.6.1",
		"x-amzn-trace-id":             "Root=" + uuid.New().String(),
		"x-client-key":                clientKey,
		"x-cursor-checksum":           checksum,
		"x-cursor-client-version":     cursorClientVersion,
		"x-cursor-client-type":        "ide",
		"x-cursor-client-os":          osName,
		"x-cursor-client-arch":        arch,
		"x-cursor-client-device-type": "desktop",
		"x-cursor-config-version":     uuid.New().String(),
		"x-cursor-timezone":           tz,
		"x-ghost-mode":                ghost,
		"x-request-id":                uuid.New().String(),
		"x-session-id":                sessionID,
	}
}
