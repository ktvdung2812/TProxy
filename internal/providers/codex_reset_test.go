package providers

import "testing"

func TestParseCodexResetCreditsIncludesExpiryDetails(t *testing.T) {
	parsed := parseCodexResetCredits(map[string]any{
		"reset_credits": map[string]any{
			"available_count": float64(2),
			"credits": []any{
				map[string]any{
					"id":         "credit-1",
					"status":     "available",
					"granted_at": "2026-10-27T10:00:00Z",
					"expires_at": "2026-11-03T10:00:00Z",
				},
			},
		},
	})

	if parsed.AvailableCount != 2 || len(parsed.Credits) != 1 {
		t.Fatalf("parsed reset credits = %+v", parsed)
	}
	credit := parsed.Credits[0]
	if credit.ID != "credit-1" || credit.Status != "available" {
		t.Fatalf("credit identity = %+v", credit)
	}
	if credit.GrantedAt != "2026-10-27T10:00:00Z" || credit.ExpiresAt != "2026-11-03T10:00:00Z" {
		t.Fatalf("credit dates = %+v", credit)
	}
}

func TestCodexResetCreditsFromUsageKeepsCreditExpiry(t *testing.T) {
	parsed := codexResetCreditsFromUsage(map[string]any{
		"rate_limit_reset_credits": map[string]any{
			"availableCount": float64(1),
			"credits": []any{
				map[string]any{"expiresAt": "2026-11-04T10:00:00Z"},
			},
		},
	})

	if parsed == nil || parsed.AvailableCount != 1 || len(parsed.Credits) != 1 {
		t.Fatalf("usage reset credits = %+v", parsed)
	}
	if parsed.Credits[0].ExpiresAt != "2026-11-04T10:00:00Z" {
		t.Fatalf("usage expiry = %q", parsed.Credits[0].ExpiresAt)
	}
}
