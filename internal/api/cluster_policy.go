package api

import (
	"slices"

	"github.com/tproxy/tproxy/internal/cluster"
	"github.com/tproxy/tproxy/internal/store"
)

func clusterEffectiveKey(key *store.APIKey, lending cluster.LendingPolicy, catalog []string) *store.APIKey {
	out := *key
	out.Models = []string{}
	for _, id := range key.Models {
		candidates := []string{id}
		if id == "*" {
			candidates = catalog
		}
		for _, candidate := range candidates {
			if slices.Contains(catalog, candidate) && (len(lending.Models) == 0 || slices.Contains(lending.Models, candidate)) && !slices.Contains(out.Models, candidate) {
				out.Models = append(out.Models, candidate)
			}
		}
	}
	out.Policy.CredentialIDs = nil
	a, b := out.Policy.Limits, lending.Limits
	a.RequestsPerMinute = clusterMin(a.RequestsPerMinute, b.RequestsPerMinute)
	a.ConcurrentStreams = clusterMin(a.ConcurrentStreams, b.ConcurrentStreams)
	a.MaxInputBytes = clusterMin(a.MaxInputBytes, b.MaxInputBytes)
	a.MaxOutputTokens = clusterMin(a.MaxOutputTokens, b.MaxOutputTokens)
	a.MediaJobs = clusterMin(a.MediaJobs, b.MediaJobs)
	a.BudgetUSDPerDay = clusterMin(a.BudgetUSDPerDay, b.BudgetUSDPerDay)
	out.Policy.Limits = a
	return &out
}
func clusterMin[T ~int | ~int64 | ~float64](a, b T) T {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	return min(a, b)
}
