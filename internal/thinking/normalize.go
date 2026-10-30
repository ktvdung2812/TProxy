package thinking

import "github.com/tproxy/tproxy/internal/canonical"

// Normalize resolves the request's thinking choice once and returns the request
// carrying it in the normalized shape (request.Reasoning holds at most effort
// and budget_tokens, Metadata carries MetadataNormalized).
//
// It runs on the router, once, before any adapter executes, so a failover
// attempt reuses the first decision instead of re-deriving one. Raw is left
// untouched: pass-through adapters still forward the client's original body.
func Normalize(request canonical.Request) canonical.Request {
	NormalizeInto(&request)
	return request
}

// NormalizeInto is Normalize for callers that already hold a pointer.
func NormalizeInto(request *canonical.Request) Config {
	if request == nil {
		return Config{}
	}
	if Resolved(*request) {
		// FromSources restores the preserved Set flag for a choice that has no
		// effort/budget to store in Reasoning (thinking: enabled with no budget).
		return FromSources(*request)
	}
	cfg := FromSources(*request)
	if request.Metadata == nil {
		request.Metadata = map[string]any{}
	}
	request.Metadata[MetadataNormalized] = true
	request.Metadata[MetadataSet] = cfg.Set
	if !cfg.Set {
		request.Reasoning = nil
		return cfg
	}
	normalized := make(map[string]any, 2)
	if cfg.Effort != "" {
		normalized["effort"] = cfg.Effort
	}
	if cfg.BudgetTokens > 0 {
		normalized["budget_tokens"] = cfg.BudgetTokens
	}
	request.Reasoning = normalized
	return cfg
}

// SetAliasEffort records the effort a Claude tier alias contributes. It is only
// a hint: FromSources consults it after every client-provided source, so a
// client that chose its own thinking always wins over the alias.
func SetAliasEffort(request *canonical.Request, effort string) {
	if request == nil {
		return
	}
	normalized := NormalizeEffort(effort)
	if normalized == "" {
		return
	}
	if request.Metadata == nil {
		request.Metadata = map[string]any{}
	}
	request.Metadata[MetadataAliasEffort] = normalized
}
