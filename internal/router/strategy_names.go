package router

import "github.com/tproxy/tproxy/internal/routing"

const (
	StrategyFillFirst          = routing.StrategyFillFirst
	StrategyRoundRobin         = routing.StrategyRoundRobin
	StrategyWeightedRoundRobin = routing.StrategyWeightedRoundRobin
	StrategyStickyRoundRobin   = routing.StrategyStickyRoundRobin
	StrategyLeastRecentlyUsed  = routing.StrategyLeastRecentlyUsed
	StrategyLeastUsed          = routing.StrategyLeastUsed
	StrategyRandom             = routing.StrategyRandom
	StrategyPriorityWeighted   = routing.StrategyPriorityWeighted
	StrategyCapacityAware      = routing.StrategyCapacityAware
	StrategyCostAware          = routing.StrategyCostAware
	StrategyLatencyAware       = routing.StrategyLatencyAware
	StrategyLKGP               = routing.StrategyLKGP
	StrategyTaskAware          = routing.StrategyTaskAware
	StrategyFusion             = routing.StrategyFusion
	StrategyArenaELO           = routing.StrategyArenaELO
	StrategyHealthFirst        = routing.StrategyHealthFirst
	StrategyQuotaAware         = routing.StrategyQuotaAware
	StrategySessionSticky      = routing.StrategySessionSticky
)

var AllStrategies = routing.AllStrategies

func IsValidStrategy(strategy string) bool {
	return routing.IsValidStrategy(strategy)
}
