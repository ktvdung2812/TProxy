package pricing

import (
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
)

// Rates holds per-million token prices in USD (models.dev format).
type Rates struct {
	InputPerMillion     float64
	OutputPerMillion    float64
	ReasoningPerMillion float64
	CacheReadPerMillion float64
	RequestUSD          float64
}

func (r Rates) ToConfig() *config.PricingConfig {
	reasoning := r.ReasoningPerMillion
	if reasoning <= 0 {
		reasoning = r.OutputPerMillion
	}
	return &config.PricingConfig{
		InputPerMillion:     r.InputPerMillion,
		OutputPerMillion:    r.OutputPerMillion,
		ReasoningPerMillion: reasoning,
		Request:             r.RequestUSD,
	}
}

func Configured(p *config.PricingConfig) bool {
	if p == nil {
		return false
	}
	return p.InputPerMillion > 0 || p.OutputPerMillion > 0 || p.ReasoningPerMillion > 0 || p.Request > 0
}

// EstimateCost computes USD cost from token usage and rates.
func EstimateCost(usage canonical.Usage, rates Rates) float64 {
	cost := rates.RequestUSD

	inputBillable := usage.InputTokens
	if rates.CacheReadPerMillion > 0 && usage.CachedTokens > 0 {
		cached := usage.CachedTokens
		if cached > inputBillable {
			cached = inputBillable
		}
		inputBillable -= cached
		cost += float64(cached) * rates.CacheReadPerMillion / 1_000_000
	}
	cost += float64(inputBillable) * rates.InputPerMillion / 1_000_000

	outputTokens := usage.OutputTokens
	reasoningRate := rates.ReasoningPerMillion
	if reasoningRate <= 0 {
		reasoningRate = rates.OutputPerMillion
	}
	if reasoningRate > 0 && usage.ReasoningTokens > 0 {
		reasoning := usage.ReasoningTokens
		if reasoning > outputTokens {
			reasoning = outputTokens
		}
		outputTokens -= reasoning
		cost += float64(reasoning) * reasoningRate / 1_000_000
	}
	cost += float64(outputTokens) * rates.OutputPerMillion / 1_000_000
	return cost
}

// EstimateFromConfig uses route YAML pricing (legacy path).
func EstimateFromConfig(usage canonical.Usage, pricing *config.PricingConfig) float64 {
	if pricing == nil {
		return 0
	}
	return EstimateCost(usage, Rates{
		InputPerMillion:     pricing.InputPerMillion,
		OutputPerMillion:    pricing.OutputPerMillion,
		ReasoningPerMillion: pricing.ReasoningPerMillion,
		RequestUSD:          pricing.Request,
	})
}

func normalizeModelID(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if idx := strings.Index(model, ":"); idx >= 0 {
		prefix := strings.ToLower(model[:idx])
		if prefix == "codex" || prefix == "openai" || prefix == "anthropic" || prefix == "google" {
			model = model[idx+1:]
		}
	}
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		model = model[idx+1:]
	}
	return strings.ToLower(strings.TrimSpace(model))
}

func modelLookupKeys(model string) []string {
	keys := make([]string, 0, 3)
	seen := map[string]struct{}{}
	add := func(value string) {
		value = normalizeModelID(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		keys = append(keys, value)
	}
	add(model)
	if trimmed := strings.TrimSpace(model); trimmed != model {
		add(trimmed)
	}
	if bare := strings.ToLower(strings.TrimSpace(model)); bare != "" {
		add(bare)
	}
	return keys
}
