package api

import (
	"reflect"
	"testing"

	"github.com/tproxy/tproxy/internal/cluster"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

func TestClusterEffectiveKey(t *testing.T) {
	cases := []struct {
		name                           string
		models, catalog, lending, want []string
	}{
		{"wildcard", []string{"*"}, []string{"a", "b"}, []string{"b"}, []string{"b"}},
		{"explicit", []string{"a", "b"}, []string{"a", "b"}, []string{"b"}, []string{"b"}},
		{"unrestricted lending", []string{"a"}, []string{"a"}, nil, []string{"a"}},
		{"empty catalog", []string{"*"}, nil, nil, []string{}},
		{"explicit empty catalog", []string{"a"}, nil, nil, []string{}},
		{"explicit missing from catalog", []string{"a", "b"}, []string{"b"}, nil, []string{"b"}},
		{"no intersection", []string{"a"}, nil, []string{"b"}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := &store.APIKey{Models: tc.models, Policy: config.ClientKeyPolicy{CredentialIDs: []string{"secret"}, Limits: config.LimitPolicy{RequestsPerMinute: 10, ConcurrentStreams: 2, MaxInputBytes: 100, MaxOutputTokens: 4, MediaJobs: 8, BudgetUSDPerDay: 2}}}
			got := clusterEffectiveKey(key, cluster.LendingPolicy{Models: tc.lending, Limits: config.LimitPolicy{RequestsPerMinute: 5, ConcurrentStreams: 0, MaxInputBytes: 200, MaxOutputTokens: 2, MediaJobs: 1, BudgetUSDPerDay: 1}}, tc.catalog)
			if !reflect.DeepEqual(got.Models, tc.want) || got.Policy.CredentialIDs != nil {
				t.Fatalf("result %+v", got)
			}
			want := config.LimitPolicy{RequestsPerMinute: 5, ConcurrentStreams: 2, MaxInputBytes: 100, MaxOutputTokens: 2, MediaJobs: 1, BudgetUSDPerDay: 1}
			if got.Policy.Limits != want {
				t.Fatalf("limits %+v", got.Policy.Limits)
			}
			if len(key.Policy.CredentialIDs) != 1 {
				t.Fatal("input mutated")
			}
		})
	}
}
