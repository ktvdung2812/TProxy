package router

import (
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

func TestStickyRoundRobinStaysOnCurrentAccount(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	credentials := []store.Credential{
		{ID: "a", Priority: 100, LastUsedAt: now.Add(-time.Minute), ConsecutiveUseCount: 1},
		{ID: "b", Priority: 90, LastUsedAt: now.Add(-2 * time.Minute), ConsecutiveUseCount: 1},
	}
	ordered, touch := stickyRoundRobinOrder(credentials, 3, now)
	if touch == nil || touch.ID != "a" || touch.ConsecutiveUseCount != 2 {
		t.Fatalf("touch = %+v", touch)
	}
	if ordered[0].ID != "a" {
		t.Fatalf("ordered primary = %s", ordered[0].ID)
	}
}

func TestStickyRoundRobinSwitchesAfterLimit(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	credentials := []store.Credential{
		{ID: "a", Priority: 100, LastUsedAt: now.Add(-time.Minute), ConsecutiveUseCount: 3},
		{ID: "b", Priority: 90, LastUsedAt: now.Add(-10 * time.Minute), ConsecutiveUseCount: 1},
	}
	ordered, touch := stickyRoundRobinOrder(credentials, 3, now)
	if touch == nil || touch.ID != "b" || touch.ConsecutiveUseCount != 1 {
		t.Fatalf("touch = %+v", touch)
	}
	if ordered[0].ID != "b" {
		t.Fatalf("ordered primary = %s", ordered[0].ID)
	}
}

func TestStickyRoundRobinPicksNeverUsedFirst(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	credentials := []store.Credential{
		{ID: "a", Priority: 100, LastUsedAt: now.Add(-time.Minute), ConsecutiveUseCount: 3},
		{ID: "b", Priority: 90},
	}
	ordered, touch := stickyRoundRobinOrder(credentials, 3, now)
	if touch == nil || touch.ID != "b" {
		t.Fatalf("touch = %+v", touch)
	}
	if ordered[0].ID != "b" {
		t.Fatalf("ordered primary = %s", ordered[0].ID)
	}
}

func TestStickyRoundRobinStaysWhenCountZeroButRecentlyUsed(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	credentials := []store.Credential{
		{ID: "a", Priority: 100, LastUsedAt: now.Add(-time.Minute), ConsecutiveUseCount: 0},
		{ID: "b", Priority: 90, LastUsedAt: now.Add(-10 * time.Minute), ConsecutiveUseCount: 1},
	}
	ordered, touch := stickyRoundRobinOrder(credentials, 3, now)
	if touch == nil || touch.ID != "a" || touch.ConsecutiveUseCount != 1 {
		t.Fatalf("touch = %+v", touch)
	}
	if ordered[0].ID != "a" {
		t.Fatalf("ordered primary = %s", ordered[0].ID)
	}
}

func TestStickyRoundRobinSingleCredentialStillTouches(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	credentials := []store.Credential{
		{ID: "solo", Priority: 100, LastUsedAt: now.Add(-time.Minute), ConsecutiveUseCount: 1},
	}
	_, touch := stickyRoundRobinOrder(credentials, 3, now)
	if touch == nil || touch.ID != "solo" || touch.ConsecutiveUseCount != 2 {
		t.Fatalf("touch = %+v", touch)
	}
}
