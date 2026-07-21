package router

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func fusionChildRequest(request canonical.Request) bool {
	if request.Metadata == nil {
		return false
	}
	value, ok := request.Metadata["_fusion_child"].(bool)
	return ok && value
}

func comboRoutingStrategy(model store.PublicModel) string {
	if model.Policy == nil {
		return ""
	}
	if value, ok := model.Policy["strategy"].(string); ok {
		return strings.TrimSpace(strings.ToLower(value))
	}
	return ""
}

func (r *Router) shouldExecuteFusion(model store.PublicModel) bool {
	if len(model.ComboItems) < 2 {
		return false
	}
	strategy := comboRoutingStrategy(model)
	return strategy == StrategyFusion || strategy == "merge"
}

func (r *Router) executeFusion(ctx context.Context, model store.PublicModel, request canonical.Request) (*Result, error) {
	start := time.Now()
	type candidate struct {
		result *Result
		err    error
	}
	results := make(chan candidate, len(model.ComboItems))
	var wg sync.WaitGroup
	for _, item := range model.ComboItems {
		wg.Add(1)
		go func(publicModelID string) {
			defer wg.Done()
			subModel, err := r.store.PublicModel(ctx, publicModelID)
			if err != nil {
				results <- candidate{err: err}
				return
			}
			subRequest := request
			subRequest.Metadata = cloneMetadata(request.Metadata)
			if subRequest.Metadata == nil {
				subRequest.Metadata = map[string]any{}
			}
			subRequest.Metadata["_fusion_child"] = true
			result, err := r.Execute(ctx, *subModel, subRequest)
			results <- candidate{result: result, err: err}
		}(item.PublicModelID)
	}
	wg.Wait()
	close(results)
	var winners []*Result
	var lastErr error
	for item := range results {
		if item.err != nil {
			lastErr = item.err
			continue
		}
		if item.result != nil && strings.TrimSpace(responseText(item.result.Response.Content)) != "" {
			winners = append(winners, item.result)
		}
	}
	if len(winners) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("fusion produced no responses for %s", model.ID)
	}
	best := pickFusionWinner(model, winners)
	if model.RewriteResponseModel {
		best.Response.Model = model.ID
	}
	_ = r.store.AddUsage(ctx, store.UsageEvent{
		RequestID:     request.RequestID,
		PublicModelID: model.ID,
		ProviderID:    best.Selection.Provider.ID,
		UpstreamModel: best.Selection.Route.UpstreamModel,
		CredentialID:  best.Selection.Credential.ID,
		Status:        200,
		InputTokens:   best.Response.Usage.InputTokens,
		OutputTokens:  best.Response.Usage.OutputTokens,
		LatencyMS:     time.Since(start).Milliseconds(),
		CreatedAt:     time.Now(),
	})
	if r.arena != nil {
		for _, winner := range winners {
			r.arena.RecordOutcome(winner.Selection.Credential, winner == best)
		}
	}
	return best, nil
}

func pickFusionWinner(model store.PublicModel, winners []*Result) *Result {
	judgeModel := ""
	if model.Policy != nil {
		if value, ok := model.Policy["judge_model"].(string); ok {
			judgeModel = strings.TrimSpace(value)
		}
	}
	if judgeModel != "" {
		for _, winner := range winners {
			if strings.EqualFold(winner.Selection.Model.ID, judgeModel) {
				return winner
			}
		}
	}
	best := winners[0]
	bestLen := len(strings.TrimSpace(responseText(best.Response.Content)))
	for _, candidate := range winners[1:] {
		length := len(strings.TrimSpace(responseText(candidate.Response.Content)))
		if length > bestLen {
			best = candidate
			bestLen = length
		}
	}
	return best
}

func cloneMetadata(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func responseText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	default:
		return fmt.Sprint(content)
	}
}
