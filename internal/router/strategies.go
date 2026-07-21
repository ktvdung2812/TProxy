package router

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/intelligence"
	"github.com/tproxy/tproxy/internal/routing"
	"github.com/tproxy/tproxy/internal/store"
)

type credentialOrderContext struct {
	routeKey              string
	priority              int
	stickyRoundRobinLimit int
	taskHint              string
	arena                 *intelligence.Arena
	rotateWeighted        func(string, int, []store.Credential) []store.Credential
}

func orderCredentialsByStrategy(strategy string, credentials []store.Credential, ctx credentialOrderContext) ([]store.Credential, *rotationTouch) {
	if len(credentials) < 2 {
		return credentials, nil
	}
	now := time.Now().UTC()
	switch strings.TrimSpace(strings.ToLower(strategy)) {
	case routing.StrategyFillFirst, routing.StrategyLeastUsed, routing.StrategyHealthFirst:
		return credentials, nil
	case routing.StrategyRoundRobin, routing.StrategyStickyRoundRobin:
		return stickyRoundRobinOrder(credentials, ctx.stickyRoundRobinLimit, now)
	case routing.StrategyWeightedRoundRobin, routing.StrategyPriorityWeighted, routing.StrategyCapacityAware:
		if ctx.rotateWeighted == nil {
			return credentials, nil
		}
		return ctx.rotateWeighted(ctx.routeKey, ctx.priority, credentials), nil
	case routing.StrategyLeastRecentlyUsed, routing.StrategyLKGP:
		return sortByRecency(credentials, false), nil
	case routing.StrategyRandom, routing.StrategyFusion:
		shuffled := append([]store.Credential(nil), credentials...)
		rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		return shuffled, nil
	case routing.StrategyCostAware:
		return sortByWeightAsc(credentials), nil
	case routing.StrategyLatencyAware:
		return sortByRecency(credentials, true), nil
	case routing.StrategyTaskAware:
		return orderTaskAware(credentials, ctx.taskHint), nil
	case routing.StrategyArenaELO:
		if ctx.arena == nil {
			return credentials, nil
		}
		return ctx.arena.OrderCredentials(credentials), nil
	case routing.StrategyQuotaAware:
		return sortQuotaHealthy(credentials), nil
	case routing.StrategySessionSticky:
		return stickyRoundRobinOrder(credentials, ctx.stickyRoundRobinLimit*2, now)
	default:
		if ctx.rotateWeighted == nil {
			return credentials, nil
		}
		return ctx.rotateWeighted(ctx.routeKey, ctx.priority, credentials), nil
	}
}

func sortByRecency(credentials []store.Credential, mostRecentFirst bool) []store.Credential {
	ordered := append([]store.Credential(nil), credentials...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return compareCredentialRecency(ordered[i], ordered[j], mostRecentFirst)
	})
	return ordered
}

func sortByWeightAsc(credentials []store.Credential) []store.Credential {
	ordered := append([]store.Credential(nil), credentials...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := credentials[i].Weight
		right := credentials[j].Weight
		if left <= 0 {
			left = 1
		}
		if right <= 0 {
			right = 1
		}
		if left == right {
			return credentials[i].ID < credentials[j].ID
		}
		return left < right
	})
	return ordered
}

func sortQuotaHealthy(credentials []store.Credential) []store.Credential {
	ordered := append([]store.Credential(nil), credentials...)
	sort.SliceStable(ordered, func(i, j int) bool {
		leftDepleted := store.QuotaAutoDisabled(credentials[i].Metadata)
		rightDepleted := store.QuotaAutoDisabled(credentials[j].Metadata)
		if leftDepleted != rightDepleted {
			return !leftDepleted
		}
		return credentials[i].Priority > credentials[j].Priority
	})
	return ordered
}

func orderTaskAware(credentials []store.Credential, taskHint string) []store.Credential {
	hint := strings.ToLower(strings.TrimSpace(taskHint))
	if hint == "" {
		return credentials
	}
	ordered := append([]store.Credential(nil), credentials...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := taskAwareScore(credentials[i], hint)
		right := taskAwareScore(credentials[j], hint)
		if left == right {
			return credentials[i].ID < credentials[j].ID
		}
		return left > right
	})
	return ordered
}

func taskAwareScore(credential store.Credential, hint string) int {
	score := credential.Priority
	label := strings.ToLower(credential.Label + " " + credential.Email)
	switch {
	case strings.Contains(hint, "code") || strings.Contains(hint, "coding"):
		if strings.Contains(label, "codex") || strings.Contains(label, "copilot") || strings.Contains(label, "code") {
			score += 50
		}
	case strings.Contains(hint, "reason"):
		if strings.Contains(label, "opus") || strings.Contains(label, "pro") {
			score += 40
		}
	case strings.Contains(hint, "fast") || strings.Contains(hint, "mini"):
		if strings.Contains(label, "mini") || strings.Contains(label, "flash") || strings.Contains(label, "haiku") {
			score += 40
		}
	}
	return score
}

func taskHintFromRequest(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	for _, key := range []string{"task", "task_type", "category", "intent"} {
		if value, ok := metadata[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return ""
}
