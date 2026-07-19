package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/guardrails"
	"github.com/tproxy/tproxy/internal/router"
)

func applyRequestControls(r *http.Request, request *canonical.Request) error {
	if request == nil {
		return nil
	}
	if override := strings.TrimSpace(r.Header.Get("X-TProxy-Route-Model")); override != "" {
		request.PublicModelID = override
	}
	if budgetRaw := strings.TrimSpace(r.Header.Get("X-TProxy-Budget")); budgetRaw != "" {
		budget, err := strconv.ParseFloat(budgetRaw, 64)
		if err != nil || budget < 0 {
			return fmt.Errorf("invalid budget header")
		}
		if request.Metadata == nil {
			request.Metadata = map[string]any{}
		}
		request.Metadata["budget_usd"] = budget
	}
	messages := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "user" {
			messages = append(messages, messageContentString(message.Content))
		}
	}
	if guardrails.DetectInjection(messages) {
		return fmt.Errorf("prompt_injection_detected")
	}
	return nil
}

func writeCostHeaders(w http.ResponseWriter, result *router.Result) {
	if w == nil || result == nil {
		return
	}
	if result.CostUSD > 0 {
		w.Header().Set("X-TProxy-Cost", fmt.Sprintf("%.6f", result.CostUSD))
	}
}

func messageContentString(content any) string {
	switch value := content.(type) {
	case string:
		return value
	default:
		return fmt.Sprint(content)
	}
}
