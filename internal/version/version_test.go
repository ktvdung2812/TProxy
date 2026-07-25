package version

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.6", "0.1.5", 1},
		{"0.1.6", "0.1.6", 0},
		{"0.1.6", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
	}
	for _, tc := range cases {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Fatalf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCurrentFromEmbeddedPackage(t *testing.T) {
	if Current() == "" || Current() == "0.0.0" {
		t.Fatalf("Current() = %q, want embedded npm version", Current())
	}
}

func TestCheckWithoutRemote(t *testing.T) {
	info := Check(t.Context(), false)
	if info.CurrentVersion == "" {
		t.Fatal("expected current version")
	}
	if info.HasUpdate {
		t.Fatal("remote check disabled should not mark update")
	}
	if info.InstallCommand == "" || info.ReleaseURL == "" {
		t.Fatalf("info = %+v", info)
	}
}
