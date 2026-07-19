package router

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

// AutoVariant describes zero-config auto routing suffixes (OmniRoute-compatible).
type AutoVariant struct {
	Category string
	Tier     string
}

func ParseAutoModel(requested string) (AutoVariant, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(requested))
	if trimmed == "" {
		return AutoVariant{}, false
	}
	if trimmed == "auto" {
		return AutoVariant{}, true
	}
	if !strings.HasPrefix(trimmed, "auto/") && !strings.HasPrefix(trimmed, "auto:") {
		return AutoVariant{}, false
	}
	body := strings.TrimPrefix(strings.TrimPrefix(trimmed, "auto/"), "auto:")
	if body == "" {
		return AutoVariant{}, true
	}
	category, tier, _ := strings.Cut(body, ":")
	if tier == "" {
		tier, category = category, ""
	}
	return AutoVariant{Category: category, Tier: tier}, true
}

func (r *Router) resolveAutoModel(ctx context.Context, variant AutoVariant, apiKey *store.APIKey) (*store.PublicModel, error) {
	models, err := r.store.PublicModels(ctx)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		model store.PublicModel
		score int
	}
	candidates := make([]candidate, 0, len(models))
	for _, model := range models {
		if !model.Enabled || len(model.ComboItems) > 0 {
			continue
		}
		if !r.store.PublicModelAllowed(apiKey, model.ID) {
			continue
		}
		if strings.HasPrefix(model.ID, "auto") {
			continue
		}
		if !autoCategoryMatches(variant.Category, model) {
			continue
		}
		score := autoTierScore(variant.Tier, model)
		if score < 0 {
			continue
		}
		candidates = append(candidates, candidate{model: model, score: score})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("model_not_found: auto")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].model.ID < candidates[j].model.ID
		}
		return candidates[i].score > candidates[j].score
	})
	best := candidates[0].model
	virtual := best
	virtual.ID = autoModelID(variant)
	virtual.DisplayName = "Auto: " + best.DisplayName
	virtual.RewriteResponseModel = true
	if virtual.Limits == nil {
		virtual.Limits = map[string]any{}
	}
	virtual.Limits["_auto_source_model"] = best.ID
	return &virtual, nil
}

func autoModelID(variant AutoVariant) string {
	if variant.Category == "" && variant.Tier == "" {
		return "auto"
	}
	if variant.Tier == "" {
		return "auto/" + variant.Category
	}
	return fmt.Sprintf("auto/%s:%s", variant.Category, variant.Tier)
}

func autoCategoryMatches(category string, model store.PublicModel) bool {
	switch strings.TrimSpace(strings.ToLower(category)) {
	case "", "chat":
		return true
	case "coding", "code":
		return hasCapability(model, "tools") || strings.Contains(strings.ToLower(model.ID), "coder") || strings.Contains(strings.ToLower(model.ID), "code")
	case "reasoning":
		return hasCapability(model, "reasoning") || strings.Contains(strings.ToLower(model.ID), "reason")
	case "vision", "multimodal":
		return hasCapability(model, "vision")
	default:
		return true
	}
}

func autoTierScore(tier string, model store.PublicModel) int {
	switch strings.TrimSpace(strings.ToLower(tier)) {
	case "", "balanced", "smart", "lkgp":
		return 50
	case "fast":
		if strings.Contains(strings.ToLower(model.ID), "mini") || strings.Contains(strings.ToLower(model.ID), "flash") || strings.Contains(strings.ToLower(model.ID), "haiku") {
			return 100
		}
		return 40
	case "cheap", "floor":
		if strings.Contains(strings.ToLower(model.ID), "mini") || strings.Contains(strings.ToLower(model.ID), "flash") || strings.Contains(strings.ToLower(model.ID), "haiku") {
			return 90
		}
		return 50
	case "free":
		if strings.Contains(strings.ToLower(model.ID), "free") {
			return 100
		}
		return -1
	case "pro", "reliable":
		if strings.Contains(strings.ToLower(model.ID), "pro") || strings.Contains(strings.ToLower(model.ID), "opus") {
			return 100
		}
		return 60
	case "offline":
		return 30
	default:
		return 50
	}
}

func hasCapability(model store.PublicModel, capability string) bool {
	for _, item := range model.Capabilities {
		if strings.EqualFold(item, capability) {
			return true
		}
	}
	return false
}
