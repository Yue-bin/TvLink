package pool

import (
	"context"
	"errors"
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
	if err := p.ConfigureRefresh(time.Hour); err != nil {
		t.Fatalf("ConfigureRefresh() error = %v", err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatalf("RebuildGroups() error = %v", err)
	}
	if _, err := p.Select(now, 1); err != nil {
		t.Fatalf("initial Select() error = %v", err)
	}
	var refreshes atomic.Int32
	coordinator := NewCoordinator(p, func(_ context.Context, due []Key) error {
		refreshes.Add(1)
		if len(due) == 0 {
			t.Error("refresh called with no due keys")
		}
		p.UpdateUsage("one", Usage{Limit: 10, Used: 1}, now)
		return nil
	})
	if _, err := coordinator.Select(context.Background(), now, 1); err != nil {
		t.Fatalf("Coordinator.Select() error = %v, want a lease after rebuilding the spent round", err)
	}
	// 槽位从重建时刻重排，槽 0 带 ±10% 槽距抖动，因此重建后立即补刷只是尽力而为。
	if got := refreshes.Load(); got > 1 {
		t.Errorf("refreshes = %d, want at most 1", got)
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

	coordinator := NewCoordinator(p, func(_ context.Context, _ []Key) error {
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

	release := make(chan struct{})
	var sweeps atomic.Int32
	coordinator := NewCoordinator(p, func(context.Context, []Key) error {
		sweeps.Add(1)
		<-release
		return errors.New("send usage request: context deadline exceeded")
	})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	lease, err := coordinator.SelectFor(context.Background(), later, Selection{Estimate: 1})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Coordinator.SelectFor() error = %v, want a lease built from stored usage", err)
	}
	if lease.Key.Name == "" {
		t.Fatalf("Coordinator.SelectFor() lease = %+v, want a Key", lease)
	}
	// The request must never wait for the sweep: that wait is what turned a slow
	// /usage endpoint into a 20 minute outage.
	if elapsed > time.Second {
		t.Fatalf("Coordinator.SelectFor() took %v, want it not to block on the sweep", elapsed)
	}
	if got := sweeps.Load(); got > 1 {
		t.Errorf("refresh sweeps = %d, want at most 1", got)
	}
}

func TestCoordinatorRecordsFailedRefreshSweep(t *testing.T) {
	p, _ := terminalGroupPool(t, time.Now())
	p.RescheduleRefreshes(time.Now().Add(-time.Hour))

	refreshed := make(chan struct{})
	coordinator := NewCoordinator(p, func(_ context.Context, due []Key) error {
		defer close(refreshed)
		if len(due) == 0 {
			t.Error("refresh called with no due keys")
		}
		return errors.New("send usage request: context deadline exceeded")
	})
	coordinator.refreshAsync(context.Background())

	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("usage refresh was never attempted")
	}
	status := awaitRefreshStatus(t, p, time.Now())
	if status.Failures != 1 {
		t.Errorf("refresh failures = %d, want 1", status.Failures)
	}
	if status.Error == "" {
		t.Error("refresh error = empty, want the sweep failure recorded by the pool")
	}
}

// A rebuild must not turn into a burst: every Key inside its throttle window is
// skipped instead of being refreshed again.
func TestCoordinatorSweepSkipsThrottledKeys(t *testing.T) {
	p, _ := terminalGroupPool(t, time.Now())
	now := time.Now()
	for _, name := range p.keyOrder {
		p.RecordRefreshAttempt(name, now, 0, nil)
	}

	var sweeps atomic.Int32
	coordinator := NewCoordinator(p, func(context.Context, []Key) error {
		sweeps.Add(1)
		return nil
	})
	coordinator.refreshAsync(context.Background())
	time.Sleep(50 * time.Millisecond)
	if got := sweeps.Load(); got != 0 {
		t.Errorf("refresh sweeps = %d, want 0 while every Key is throttled", got)
	}
	if status := p.MonitorSnapshot(now).Refresh; !status.At.IsZero() {
		t.Errorf("refresh batch status = %+v, want nothing recorded", status)
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
