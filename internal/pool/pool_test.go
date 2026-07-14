package pool

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestSelectRequiresUsageSnapshot(t *testing.T) {
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)

	if _, err := p.Select(time.Now(), 1); !errors.Is(err, ErrNoEligibleKey) {
		t.Fatalf("Select() error = %v, want ErrNoEligibleKey", err)
	}
}

func TestSelectReservesEstimatedCredits(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 10, Used: 2}, now)

	lease, err := p.Select(now, 3)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if lease.Key.Name != "one" {
		t.Errorf("selected key = %q, want one", lease.Key.Name)
	}

	snapshot := p.Snapshots(now)[0]
	if snapshot.EstimatedUsage != 3 {
		t.Errorf("EstimatedUsage = %v, want 3", snapshot.EstimatedUsage)
	}
	if snapshot.Remaining != 5 {
		t.Errorf("Remaining = %v, want 5", snapshot.Remaining)
	}
}

func TestExhaustionStatusDisablesKey(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 10, Used: 2}, now)
	lease, err := p.Select(now, 1)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	p.Resolve(lease, 432, 0, now)
	if _, err := p.Select(now, 1); !errors.Is(err, ErrNoEligibleKey) {
		t.Fatalf("Select() after 432 error = %v, want ErrNoEligibleKey", err)
	}
	if got := p.Snapshots(now)[0].State; got != StateExhausted {
		t.Errorf("State = %q, want %q", got, StateExhausted)
	}
}

func TestRateLimitAllowsOneProbeAfterRetryAfter(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 10, Used: 2}, now)
	lease, err := p.Select(now, 1)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	p.Resolve(lease, 429, time.Minute, now)
	if _, err := p.Select(now.Add(59*time.Second), 1); !errors.Is(err, ErrNoEligibleKey) {
		t.Fatalf("Select() during cooldown error = %v, want ErrNoEligibleKey", err)
	}

	probe, err := p.Select(now.Add(time.Minute), 1)
	if err != nil {
		t.Fatalf("Select() probe error = %v", err)
	}
	if _, err := p.Select(now.Add(time.Minute), 1); !errors.Is(err, ErrNoEligibleKey) {
		t.Fatalf("second Select() during probe error = %v, want ErrNoEligibleKey", err)
	}

	p.Resolve(probe, 200, 0, now.Add(time.Minute))
	if _, err := p.Select(now.Add(time.Minute), 1); err != nil {
		t.Fatalf("Select() after successful probe error = %v", err)
	}
}

func TestRebuildGroupsBalancesRemainingCapacity(t *testing.T) {
	now := time.Now()
	keys := make([]Key, 0, 10)
	for index := 0; index < 10; index++ {
		keys = append(keys, Key{Name: string(rune('a' + index)), APIKey: "tvly-key"})
	}
	p := New(keys, 1)
	for index, remaining := range []int64{100, 90, 80, 70, 60, 50, 40, 30, 20, 10} {
		p.UpdateUsage(keys[index].Name, Usage{Limit: 100, Used: 100 - remaining}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 3, UsageLimit: 600, Location: time.UTC}); err != nil {
		t.Fatalf("ConfigureGroups() error = %v", err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatalf("RebuildGroups() error = %v", err)
	}

	if len(p.groups) != 4 {
		t.Fatalf("len(groups) = %d, want 4", len(p.groups))
	}
	wantCounts := []int{3, 3, 2, 2}
	seen := make(map[string]bool)
	for index, group := range p.groups {
		if len(group.keys) != wantCounts[index] {
			t.Errorf("group %d size = %d, want %d", index, len(group.keys), wantCounts[index])
		}
		for name := range group.keys {
			if seen[name] {
				t.Errorf("key %q belongs to more than one group", name)
			}
			seen[name] = true
		}
	}
	if len(seen) != len(keys) {
		t.Errorf("assigned keys = %d, want %d", len(seen), len(keys))
	}
}

func TestSelectUsesActiveGroupAndRollsBackRateLimit(t *testing.T) {
	now := time.Now()
	keys := []Key{{Name: "one", APIKey: "tvly-one"}, {Name: "two", APIKey: "tvly-two"}}
	p := New(keys, 1)
	for _, key := range keys {
		p.UpdateUsage(key.Name, Usage{Limit: 100, Used: 0}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 10, Location: time.UTC}); err != nil {
		t.Fatalf("ConfigureGroups() error = %v", err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatalf("RebuildGroups() error = %v", err)
	}

	lease, err := p.Select(now, 3)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if _, ok := p.groups[p.activeGroup].keys[lease.Key.Name]; !ok {
		t.Errorf("selected key %q is not in active group", lease.Key.Name)
	}
	if got := p.groups[p.activeGroup].reserved; got != 3 {
		t.Errorf("group reserved = %v, want 3", got)
	}
	p.Resolve(lease, 429, time.Minute, now)
	if got := p.groups[p.activeGroup].reserved; got != 0 {
		t.Errorf("group reserved after 429 = %v, want 0", got)
	}
}

func TestSelectRotatesBeforeCrossingGroupLimit(t *testing.T) {
	now := time.Now()
	keys := []Key{{Name: "one", APIKey: "tvly-one"}, {Name: "two", APIKey: "tvly-two"}}
	p := New(keys, 1)
	for _, key := range keys {
		p.UpdateUsage(key.Name, Usage{Limit: 100, Used: 0}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 2, Location: time.UTC}); err != nil {
		t.Fatalf("ConfigureGroups() error = %v", err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatalf("RebuildGroups() error = %v", err)
	}
	first, err := p.Select(now, 2)
	if err != nil {
		t.Fatalf("first Select() error = %v", err)
	}
	second, err := p.Select(now, 1)
	if err != nil {
		t.Fatalf("second Select() error = %v", err)
	}
	if first.Key.Name == second.Key.Name {
		t.Errorf("second selection reused spent group key %q", second.Key.Name)
	}
}

func TestMonitorSnapshotAggregatesGroupsAndSelectionWeights(t *testing.T) {
	now := time.Now()
	keys := []Key{
		{Name: "one", APIKey: "tvly-one"},
		{Name: "two", APIKey: "tvly-two"},
		{Name: "three", APIKey: "tvly-three"},
		{Name: "four", APIKey: "tvly-four"},
	}
	p := New(keys, 1)
	for index, key := range keys {
		p.UpdateUsage(key.Name, Usage{Limit: 100, Used: int64(10 * (index + 1))}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 2, UsageLimit: 10, Location: time.UTC}); err != nil {
		t.Fatalf("ConfigureGroups() error = %v", err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatalf("RebuildGroups() error = %v", err)
	}
	if _, err := p.Select(now, 3); err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	snapshot := p.MonitorSnapshot(now)
	if !snapshot.GroupingEnabled || snapshot.ActiveGroup != 1 {
		t.Fatalf("grouping = %v, active group = %d", snapshot.GroupingEnabled, snapshot.ActiveGroup)
	}
	if len(snapshot.Groups) != 2 || len(snapshot.Keys) != 4 {
		t.Fatalf("groups = %d, keys = %d", len(snapshot.Groups), len(snapshot.Keys))
	}

	var totalLimit, totalUsage int64
	var totalEstimated, totalRemaining float64
	for _, group := range snapshot.Groups {
		if group.KeyCount != 2 || group.RoundLimit != 10 {
			t.Errorf("group %d = %+v", group.Index, group)
		}
		totalLimit += group.Limit
		totalUsage += group.RealUsage
		totalEstimated += group.EstimatedUsage
		totalRemaining += group.Remaining
		if group.Active && group.RoundUsage != 3 {
			t.Errorf("active group round usage = %v, want 3", group.RoundUsage)
		}
	}
	if totalLimit != 400 || totalUsage != 100 || totalEstimated != 3 || totalRemaining != 297 {
		t.Errorf("group totals = limit %d, usage %d, estimated %v, remaining %v", totalLimit, totalUsage, totalEstimated, totalRemaining)
	}
	for _, key := range snapshot.Keys {
		if key.Group == 0 {
			t.Errorf("key %q has no group", key.Name)
		}
		if key.Group == snapshot.ActiveGroup && key.Weight <= 0 {
			t.Errorf("active key %q weight = %v", key.Name, key.Weight)
		}
		if key.Group != snapshot.ActiveGroup && key.Weight != 0 {
			t.Errorf("inactive key %q weight = %v", key.Name, key.Weight)
		}
	}
}

func TestRebuildGroupsMinimizesCapacitySpread(t *testing.T) {
	now := time.Now()
	keys := []Key{
		{Name: "lemon-01"}, {Name: "lemon-02"}, {Name: "lemon-03"}, {Name: "lemon-04"},
		{Name: "moncak-01"}, {Name: "moncak-02"}, {Name: "moncak-03"}, {Name: "moncak-04"},
		{Name: "moncak-05"}, {Name: "moncak-06"},
	}
	remaining := []int64{99, 960, 992, 1000, 473, 442, 543, 994, 994, 994}
	p := New(keys, 1)
	for index, key := range keys {
		p.UpdateUsage(key.Name, Usage{Limit: 1000, Used: 1000 - remaining[index]}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 3, UsageLimit: 600, Location: time.UTC}); err != nil {
		t.Fatalf("ConfigureGroups() error = %v", err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatalf("RebuildGroups() error = %v", err)
	}

	if got := []int{len(p.groups[0].keys), len(p.groups[1].keys), len(p.groups[2].keys), len(p.groups[3].keys)}; !reflect.DeepEqual(got, []int{3, 3, 2, 2}) {
		t.Fatalf("group sizes = %v, want [3 3 2 2]", got)
	}
	totals := make([]float64, len(p.groups))
	for index, group := range p.groups {
		for name := range group.keys {
			totals[index] += p.keys[name].remaining()
		}
	}
	spread := slices.Max(totals) - slices.Min(totals)
	if spread != 344 {
		t.Errorf("group totals = %v, spread = %v, want 344", totals, spread)
	}
}
