package resilience

import (
	"net/http"
	"testing"
	"time"
)

func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	reg := NewRegistry()
	reg.Configure("openai", Config{DegradedThreshold: 2, FailureThreshold: 3, ResetTimeout: 10 * time.Millisecond})

	if !reg.CanExecute("openai") {
		t.Fatal("expected closed circuit to allow traffic")
	}
	reg.RecordFailure("openai", http.StatusServiceUnavailable)
	reg.RecordFailure("openai", http.StatusBadGateway)
	if reg.Status("openai") != StateDegraded {
		t.Fatalf("expected degraded, got %s", reg.Status("openai"))
	}
	reg.RecordFailure("openai", http.StatusBadGateway)
	if reg.CanExecute("openai") {
		t.Fatal("expected open circuit to block traffic")
	}
	time.Sleep(15 * time.Millisecond)
	if !reg.CanExecute("openai") {
		t.Fatal("expected half-open probe after timeout")
	}
	reg.RecordSuccess("openai")
	if reg.Status("openai") != StateClosed {
		t.Fatalf("expected closed after success, got %s", reg.Status("openai"))
	}
}

func TestCircuitBreakerIgnoresAccountErrors(t *testing.T) {
	reg := NewRegistry()
	reg.RecordFailure("anthropic", http.StatusTooManyRequests)
	reg.RecordFailure("anthropic", http.StatusUnauthorized)
	if reg.Status("anthropic") != StateClosed {
		t.Fatalf("account-level errors must not trip provider circuit, got %s", reg.Status("anthropic"))
	}
}
