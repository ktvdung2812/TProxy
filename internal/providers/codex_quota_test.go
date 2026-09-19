package providers

import "testing"

func TestCodexPlanName(t *testing.T) {
	cases := map[string]string{
		"free":         "Free",
		"plus":         "Plus",
		"pro":          "Pro",
		"team":         "Team",
		"business":     "Business",
		"edu_account":  "Edu Account",
		"":             "",
		" enterprise ": "Enterprise",
	}
	for raw, want := range cases {
		if got := codexPlanName(raw); got != want {
			t.Fatalf("codexPlanName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseResetAtDateOnlyLayouts(t *testing.T) {
	if got := parseResetAt("10/01/2026"); got != "2026-10-01T00:00:00Z" {
		t.Fatalf("us date = %q", got)
	}
	if got := parseResetAt("2026-10-01"); got != "2026-10-01T00:00:00Z" {
		t.Fatalf("iso date = %q", got)
	}
	if got := parseResetAt("not a date"); got != "not a date" {
		t.Fatalf("unparseable = %q", got)
	}
}
