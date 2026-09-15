package pool

import (
	"context"
	"errors"
	"sync"
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
	refreshed := make(chan struct{})
	var once sync.Once
	coordinator := NewCoordinator(p, func(context.Context) error {
		refreshes.Add(1)
		p.UpdateUsage("one", Usage{Limit: 10, Used: 1}, now)
		once.Do(func() { close(refreshed) })
		return nil
	})
	if _, err := coordinator.Select(context.Background(), now, 1); err != nil {
		t.Fatalf("Coordinator.Select() error = %v", err)
	}
	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("usage refresh sweep was never attempted")
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refreshes = %d, want 1", got)
	}
}

func TestCoordinatorPreservesResearchExclusionsAcrossRebuild(t *testing.T) {
	now := time.Now()
	keys := []Key{{Name: "one", APIKey: "tvly-one"}, {Name: "two", APIKey: "tvly-two"}}
	p := New(keys, 1)
	for _, key := range keys {
		p.UpdateUsage(key.Name, Usage{Limit: 1000, Used: 0}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 1, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}
	// Spend every group so the coordinator has to rebuild the round, and keep
	// both Keys usable: reservations are estimates, not real Tavily usage.
	for range keys {
		if _, err := p.Select(now, 1); err != nil {
			t.Fatalf("Select() while spending every group = %v", err)
		}
	}
	if _, err := p.Select(now, 1); !errors.Is(err, ErrGroupRebuildRequired) {
		t.Fatalf("Select() with every group spent = %v, want ErrGroupRebuildRequired", err)
	}

	coordinator := NewCoordinator(p, func(context.Context) error {
		for _, key := range keys {
			p.UpdateUsage(key.Name, Usage{Limit: 1000, Used: 1}, now)
		}
		return nil
	})
	lease, err := coordinator.SelectFor(context.Background(), now, Selection{
		Estimate: 110,
		Workload: WorkloadResearch,
		Excluded: map[string]struct{}{"one": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Key.Name != "two" {
		t.Fatalf("selected Key = %q, want two", lease.Key.Name)
	}
}

// Regression for the 2026-09-15 outage: every group reached a terminal state
// while both Keys stayed usable, and the rebuild only happened when a full
// usage sweep completed. A slow Tavily /usage endpoint (20s per Key) therefore
// locked the whole pool out for as long as the sweep kept failing.
func TestCoordinatorRebuildsWithoutCompletedRefresh(t *testing.T) {
	p, later := terminalGroupPool(t, time.Now())

	var sweeps atomic.Int32
	refreshed := make(chan struct{})
	var once sync.Once
	coordinator := NewCoordinator(p, func(context.Context) error {
		sweeps.Add(1)
		once.Do(func() { close(refreshed) })
		return errors.New("send usage request: context deadline exceeded")
	})

	lease, err := coordinator.SelectFor(context.Background(), later, Selection{Estimate: 1})
	if err != nil {
		t.Fatalf("Coordinator.SelectFor() error = %v, want a lease built from stored usage", err)
	}
	if lease.Key.Name == "" {
		t.Fatalf("Coordinator.SelectFor() lease = %+v, want a Key", lease)
	}
	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("usage refresh sweep was never attempted")
	}
	if got := sweeps.Load(); got != 1 {
		t.Errorf("refresh sweeps = %d, want 1", got)
	}
}

func TestCoordinatorRecordsFailedRefreshSweep(t *testing.T) {
	p, later := terminalGroupPool(t, time.Now())
	coordinator := NewCoordinator(p, func(context.Context) error {
		return errors.New("send usage request: context deadline exceeded")
	})
	if _, err := coordinator.SelectFor(context.Background(), later, Selection{Estimate: 1}); err != nil {
		t.Fatalf("Coordinator.SelectFor() error = %v, want a lease", err)
	}

	status := awaitRefreshStatus(t, p, later)
	if status.Failures != 1 {
		t.Errorf("refresh failures = %d, want 1", status.Failures)
	}
	if status.Error == "" {
		t.Error("refresh error = empty, want the sweep failure recorded by the pool")
	}
}

func awaitRefreshStatus(t *testing.T, p *Pool, now time.Time) RefreshStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := p.MonitorSnapshot(now).Refresh
		if !status.At.IsZero() {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatal("usage refresh outcome was never recorded")
		}
		time.Sleep(5 * time.Millisecond)
	}
}


