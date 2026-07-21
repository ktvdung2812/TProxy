package providers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/store"
)

func applyIflowHeaders(headers http.Header, credential store.Credential) {
	userAgent := headers.Get("User-Agent")
	if userAgent == "" {
		userAgent = "iFlow-Cli"
		headers.Set("User-Agent", userAgent)
	}
	apiKey := credential.Secret
	if credential.OAuthToken != nil {
		if value := credentialExtraString(credential, "api_key", "apiKey"); value != "" {
			apiKey = value
		}
	}
	sessionID := "session-" + uuid.NewString()
	timestamp := time.Now().UnixMilli()
	headers.Set("session-id", sessionID)
	headers.Set("x-iflow-timestamp", strconv.FormatInt(timestamp, 10))
	headers.Set("x-iflow-signature", iflowSignature(userAgent, sessionID, timestamp, apiKey))
	if apiKey != "" {
		headers.Set("Authorization", "Bearer "+apiKey)
	}
}

func iflowSignature(userAgent, sessionID string, timestamp int64, apiKey string) string {
	if apiKey == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(apiKey))
	_, _ = mac.Write([]byte(userAgent + ":" + sessionID + ":" + strconv.FormatInt(timestamp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func applyKilocodeHeaders(headers http.Header, credential store.Credential) {
	if credential.OAuthToken == nil || credential.OAuthToken.Extra == nil {
		return
	}
	if orgID := credentialExtraString(credential, "org_id", "orgId"); orgID != "" {
		headers.Set("X-Kilocode-OrganizationID", orgID)
	}
}
