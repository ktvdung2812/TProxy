package resilience

import (
	"net/http"
	"testing"
	"time"
)

func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	reg := NewRegistry()
	reg.SetDefaultConfig(Config{DegradedThreshold: 2, FailureThreshold: 3, ResetTimeout: 10 * time.Millisecond})

	if !reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("expected closed circuit to allow traffic")
	}
	reg.RecordFailure("opus-5", "openrouter", http.StatusServiceUnavailable, "boom")
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	if reg.Status("opus-5", "openrouter") != StateDegraded {
		t.Fatalf("expected degraded, got %s", reg.Status("opus-5", "openrouter"))
	}
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	if reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("expected open circuit to block traffic")
	}
	time.Sleep(20 * time.Millisecond)
	if !reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("expected half-open probe after timeout")
	}
	reg.RecordSuccess("opus-5", "openrouter")
	if reg.Status("opus-5", "openrouter") != StateClosed {
		t.Fatalf("expected closed after success, got %s", reg.Status("opus-5", "openrouter"))
	}
}

func TestCircuitBreakerIgnoresAccountErrors(t *testing.T) {
	reg := NewRegistry()
	reg.RecordFailure("opus-5", "anthropic", http.StatusTooManyRequests, "rate limited")
	reg.RecordFailure("opus-5", "anthropic", http.StatusUnauthorized, "expired token")
	reg.RecordFailure("opus-5", "anthropic", http.StatusForbidden, "forbidden")
	if reg.Status("opus-5", "anthropic") != StateClosed {
		t.Fatalf("account-level errors must not trip provider circuit, got %s", reg.Status("opus-5", "anthropic"))
	}
}

func TestCircuitBreakerCountsAccountErrorsWhenEnabled(t *testing.T) {
	reg := NewRegistry()
	reg.SetCountAccountErrors(true)
	reg.SetDefaultConfig(Config{FailureThreshold: 3, ResetTimeout: time.Minute})
	for i := 0; i < 3; i++ {
		reg.RecordFailure("opus-5", "anthropic", http.StatusTooManyRequests, "rate limited")
	}
	if reg.CanExecute("opus-5", "anthropic") {
		t.Fatal("expected 429s to trip the circuit once account errors are counted")
	}
}

// The default threshold is the behaviour the user asked for: three failed
// attempts against a provider take it out of that model's chain.
func TestDefaultThresholdIsThreeFailures(t *testing.T) {
	reg := NewRegistry()
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	if !reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("provider should still serve traffic after two failures")
	}
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	if reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("provider should be disabled after the third failure")
	}
}

func TestCircuitBreakerIsScopedPerModel(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < 3; i++ {
		reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	}
	if reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("expected openrouter to be disabled for opus-5")
	}
	if !reg.CanExecute("sonnet-5", "openrouter") {
		t.Fatal("a failure on one model must not disable the provider for other models")
	}
}

func TestSuccessClearsFailureStreak(t *testing.T) {
	reg := NewRegistry()
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	reg.RecordSuccess("opus-5", "openrouter")
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	if !reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("a success between failures must reset the consecutive count")
	}
}

func TestNetworkErrorsCountAsFailures(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < 3; i++ {
		reg.RecordFailure("opus-5", "openrouter", 0, "dial tcp: connection refused")
	}
	if reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("connection failures with no HTTP status should trip the circuit")
	}
}

func TestBackoffDoublesPerTripAndIsCapped(t *testing.T) {
	cfg := Config{ResetTimeout: time.Minute, MaxResetTimeout: 10 * time.Minute}
	for trips, want := range map[int]time.Duration{
		1: time.Minute,
		2: 2 * time.Minute,
		3: 4 * time.Minute,
		4: 8 * time.Minute,
		5: 10 * time.Minute,
		9: 10 * time.Minute,
	} {
		if got := backoff(cfg, trips); got != want {
			t.Fatalf("backoff(trips=%d) = %s, want %s", trips, got, want)
		}
	}
}

func TestRepeatTripsExtendTheOpenWindow(t *testing.T) {
	reg := NewRegistry()
	reg.SetDefaultConfig(Config{FailureThreshold: 1, ResetTimeout: 20 * time.Millisecond, MaxResetTimeout: time.Minute})

	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	time.Sleep(30 * time.Millisecond)
	if !reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("expected half-open probe after the first backoff")
	}
	// Failing the probe re-opens the circuit with a longer window.
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	second := time.Until(reg.RetryAt("opus-5", "openrouter"))
	if second <= 25*time.Millisecond {
		t.Fatalf("second trip should wait longer than the first, got %s", second)
	}
	if states := reg.Snapshot(""); len(states) != 1 || states[0].Trips != 2 {
		t.Fatalf("expected two recorded trips, got %+v", states)
	}
}

func TestDisabledRegistryNeverBlocks(t *testing.T) {
	reg := NewRegistry()
	reg.SetEnabled(false)
	for i := 0; i < 10; i++ {
		reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	}
	if !reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("failover is disabled, traffic must not be blocked")
	}
	if len(reg.Snapshot("")) == 0 {
		t.Fatal("failures should still be tracked so the dashboard can show them")
	}
}

func TestSnapshotSkipsHealthyEntries(t *testing.T) {
	reg := NewRegistry()
	reg.CanExecute("opus-5", "anthropic")
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	states := reg.Snapshot("")
	if len(states) != 1 || states[0].ProviderID != "openrouter" {
		t.Fatalf("snapshot should only report unhealthy providers, got %+v", states)
	}
	if states[0].Failures != 1 || states[0].Threshold != DefaultConfig().FailureThreshold {
		t.Fatalf("snapshot should carry the failure count and threshold, got %+v", states[0])
	}
}

func TestSnapshotFiltersByModel(t *testing.T) {
	reg := NewRegistry()
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	reg.RecordFailure("sonnet-5", "openrouter", http.StatusBadGateway, "boom")
	if states := reg.Snapshot("opus-5"); len(states) != 1 || states[0].ModelID != "opus-5" {
		t.Fatalf("snapshot should be scoped to the requested model, got %+v", states)
	}
}

func TestResetProviderClearsEveryModel(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < 3; i++ {
		reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
		reg.RecordFailure("sonnet-5", "openrouter", http.StatusBadGateway, "boom")
	}
	reg.ResetProvider("openrouter")
	if !reg.CanExecute("opus-5", "openrouter") || !reg.CanExecute("sonnet-5", "openrouter") {
		t.Fatal("ResetProvider should put the provider back in rotation for every model")
	}
}

func TestPerProviderOverrideWins(t *testing.T) {
	reg := NewRegistry()
	reg.Configure("openrouter", Config{FailureThreshold: 1, ResetTimeout: time.Minute})
	reg.RecordFailure("opus-5", "openrouter", http.StatusBadGateway, "boom")
	if reg.CanExecute("opus-5", "openrouter") {
		t.Fatal("per-provider threshold of 1 should trip on the first failure")
	}
	reg.RecordFailure("opus-5", "anthropic", http.StatusBadGateway, "boom")
	if !reg.CanExecute("opus-5", "anthropic") {
		t.Fatal("providers without an override should keep the default threshold")
	}
}
