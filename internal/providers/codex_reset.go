package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	codexResetCreditsURL        = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	codexResetCreditsConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
)

// CodexResetCredit is one Codex rate-limit reset credit entry.
type CodexResetCredit struct {
	Status    string `json:"status"`
	GrantedAt string `json:"granted_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// CodexResetCredits is the Codex reset-credit inventory for a credential.
type CodexResetCredits struct {
	AvailableCount int                `json:"available_count"`
	Credits        []CodexResetCredit `json:"credits,omitempty"`
}

// CodexResetConsumeResult is returned after consuming one reset credit.
type CodexResetConsumeResult struct {
	OK            bool   `json:"ok"`
	Reset         bool   `json:"reset"`
	Code          string `json:"code,omitempty"`
	WindowsReset  int    `json:"windows_reset,omitempty"`
	Message       string `json:"message,omitempty"`
	NoCredit      bool   `json:"no_credit,omitempty"`
	RedeemRequest string `json:"redeem_request_id,omitempty"`
}

func codexResetCreditsFromUsage(payload map[string]any) *CodexResetCredits {
	raw, ok := payload["rate_limit_reset_credits"].(map[string]any)
	if !ok {
		return nil
	}
	count := int(numberValue(firstValue(raw, "available_count", "availableCount")))
	if count < 0 {
		count = 0
	}
	return &CodexResetCredits{AvailableCount: count}
}

// CodexResetCredits fetches Codex reset-credit inventory for a credential.
func (r *Registry) CodexResetCredits(ctx context.Context, provider store.Provider, credential store.Credential) (CodexResetCredits, error) {
	if provider.Type != "codex" {
		return CodexResetCredits{}, &ProviderError{Code: "invalid_request", Message: "Codex reset credits are only available for Codex credentials", Status: http.StatusBadRequest}
	}
	if credential.Secret == "" {
		return CodexResetCredits{}, &ProviderError{Code: "authorization_required", Message: "credential has no access token", Status: http.StatusUnauthorized}
	}
	ctx = withCredentialProxy(ctx, credential)
	body, status, err := r.quotaGET(ctx, codexResetCreditsURL, codexHeaders(provider, credential, false, canonical.Request{}))
	if err != nil {
		return CodexResetCredits{}, err
	}
	if status < 200 || status >= 300 {
		message := codexResetErrorMessage(body, status)
		return CodexResetCredits{}, &ProviderError{Code: "codex_reset_credits_failed", Message: message, Status: status}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return CodexResetCredits{}, &ProviderError{Code: "codex_reset_credits_failed", Message: "invalid Codex reset credits response", Err: err}
	}
	return parseCodexResetCredits(payload), nil
}

// ConsumeCodexResetCredit spends one Codex reset credit for a credential.
func (r *Registry) ConsumeCodexResetCredit(ctx context.Context, provider store.Provider, credential store.Credential, redeemRequestID string) (CodexResetConsumeResult, error) {
	if provider.Type != "codex" {
		return CodexResetConsumeResult{}, &ProviderError{Code: "invalid_request", Message: "Codex reset credits are only available for Codex credentials", Status: http.StatusBadRequest}
	}
	if credential.Secret == "" {
		return CodexResetConsumeResult{}, &ProviderError{Code: "authorization_required", Message: "credential has no access token", Status: http.StatusUnauthorized}
	}
	redeemRequestID = strings.TrimSpace(redeemRequestID)
	if redeemRequestID == "" {
		redeemRequestID = uuid.NewString()
	}
	ctx = withCredentialProxy(ctx, credential)
	encoded, _ := json.Marshal(map[string]string{"redeem_request_id": redeemRequestID})
	body, status, err := r.quotaJSON(ctx, http.MethodPost, codexResetCreditsConsumeURL, codexHeaders(provider, credential, false, canonical.Request{}), encoded)
	if err != nil {
		return CodexResetConsumeResult{}, err
	}
	var payload map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}
	code := stringValue(payload["code"])
	windowsReset := int(numberValue(payload["windows_reset"]))
	result := CodexResetConsumeResult{
		Code:          code,
		WindowsReset:  windowsReset,
		Message:       stringValue(firstValue(payload, "message", "detail")),
		RedeemRequest: redeemRequestID,
	}
	if status >= 200 && status < 300 && (code == "reset" || windowsReset > 0) {
		result.OK = true
		result.Reset = true
		return result, nil
	}
	if status >= 200 && status < 300 && code == "no_credit" {
		result.NoCredit = true
		if result.Message == "" {
			result.Message = "No Codex reset credits available."
		}
		return result, &ProviderError{Code: "no_credit", Message: result.Message, Status: http.StatusConflict}
	}
	if result.Message == "" {
		result.Message = codexResetErrorMessage(body, status)
	}
	return result, &ProviderError{Code: "codex_reset_consume_failed", Message: result.Message, Status: status}
}

func parseCodexResetCredits(payload map[string]any) CodexResetCredits {
	result := CodexResetCredits{
		AvailableCount: int(numberValue(firstValue(payload, "available_count", "availableCount"))),
	}
	if result.AvailableCount < 0 {
		result.AvailableCount = 0
	}
	rawCredits, _ := payload["credits"].([]any)
	for _, item := range rawCredits {
		credit, _ := item.(map[string]any)
		if credit == nil {
			continue
		}
		result.Credits = append(result.Credits, CodexResetCredit{
			Status:    stringValue(firstValue(credit, "status")),
			GrantedAt: parseResetAt(firstValue(credit, "granted_at", "grantedAt")),
			ExpiresAt: parseResetAt(firstValue(credit, "expires_at", "expiresAt")),
		})
	}
	return result
}

func codexResetErrorMessage(body []byte, status int) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		if detail := stringValue(firstValue(payload, "message", "detail", "error")); detail != "" {
			return detail
		}
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return fmt.Sprintf("Codex reset credits API unavailable (HTTP %d)", status)
	}
	return text
}

func (r *Registry) quotaJSON(ctx context.Context, method, target string, headers http.Header, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, 0, &ProviderError{Code: "quota_request_failed", Err: err}
	}
	req.Header = headers.Clone()
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := r.client.Do(req)
	if err != nil {
		return nil, 0, &ProviderError{Code: "quota_request_failed", Err: err}
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return nil, response.StatusCode, &ProviderError{Code: "quota_request_failed", Err: readErr}
	}
	return raw, response.StatusCode, nil
}
