package routing

import "strings"

// Strategy identifiers shared by config validation and the router.
const (
	StrategyFillFirst          = "fill-first"
	StrategyRoundRobin         = "round-robin"
	StrategyWeightedRoundRobin = "weighted-round-robin"
	StrategyStickyRoundRobin   = "sticky-round-robin"
	StrategyLeastRecentlyUsed  = "least-recently-used"
	StrategyLeastUsed          = "least-used"
	StrategyRandom             = "random"
	StrategyPriorityWeighted   = "priority-weighted"
	StrategyCapacityAware      = "capacity-aware"
	StrategyCostAware          = "cost-aware"
	StrategyLatencyAware       = "latency-aware"
	StrategyLKGP               = "lkgp"
	StrategyTaskAware          = "task-aware"
	StrategyFusion             = "fusion"
	StrategyArenaELO           = "arena-elo"
	StrategyHealthFirst        = "health-first"
	StrategyQuotaAware         = "quota-aware"
	StrategySessionSticky      = "session-sticky"
)

// AllStrategies lists every supported credential rotation strategy.
var AllStrategies = []string{
	StrategyFillFirst,
	StrategyRoundRobin,
	StrategyWeightedRoundRobin,
	StrategyStickyRoundRobin,
	StrategyLeastRecentlyUsed,
	StrategyLeastUsed,
	StrategyRandom,
	StrategyPriorityWeighted,
	StrategyCapacityAware,
	StrategyCostAware,
	StrategyLatencyAware,
	StrategyLKGP,
	StrategyTaskAware,
	StrategyFusion,
	StrategyArenaELO,
	StrategyHealthFirst,
	StrategyQuotaAware,
	StrategySessionSticky,
}

func IsValidStrategy(strategy string) bool {
	switch strings.TrimSpace(strings.ToLower(strategy)) {
	case StrategyFillFirst, StrategyRoundRobin, StrategyWeightedRoundRobin,
		StrategyStickyRoundRobin, StrategyLeastRecentlyUsed, StrategyLeastUsed,
		StrategyRandom, StrategyPriorityWeighted, StrategyCapacityAware,
		StrategyCostAware, StrategyLatencyAware, StrategyLKGP, StrategyTaskAware,
		StrategyFusion, StrategyArenaELO, StrategyHealthFirst, StrategyQuotaAware,
		StrategySessionSticky:
		return true
	default:
		return false
	}
}
