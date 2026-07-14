package pool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoordinatorRebuildsSpentGroups(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 10, Used: 0}, now)
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 1, Location: time.UTC}); err != nil {
		t.Fatalf("ConfigureGroups() error = %v", err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatalf("RebuildGroups() error = %v", err)
	}
	if _, err := p.Select(now, 1); err != nil {
		t.Fatalf("initial Select() error = %v", err)
	}
	var refreshes atomic.Int32
	coordinator := NewCoordinator(p, func(context.Context) error {
		refreshes.Add(1)
		p.UpdateUsage("one", Usage{Limit: 10, Used: 1}, now)
		return nil
	})
	if _, err := coordinator.Select(context.Background(), now, 1); err != nil {
		t.Fatalf("Coordinator.Select() error = %v", err)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refreshes = %d, want 1", got)
	}
}
