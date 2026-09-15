package pool

import (
	"errors"
	"reflect"
	"slices"
	"sync"
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

func TestUsageExhaustionDisablesKey(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 10, Used: 10}, now)
	if _, err := p.Select(now, 1); !errors.Is(err, ErrNoEligibleKey) {
		t.Fatalf("Select() after exhausted usage error = %v, want ErrNoEligibleKey", err)
	}
	if got := p.Snapshots(now)[0].State; got != StateExhausted {
		t.Errorf("State = %q, want %q", got, StateExhausted)
	}
}

func TestSelectForRequiresFullReservation(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 1000, Used: 900}, now)

	_, err := p.SelectFor(now, Selection{Estimate: 110, Workload: WorkloadResearch})
	if !errors.Is(err, ErrNoEligibleKey) {
		t.Fatalf("SelectFor() error = %v, want ErrNoEligibleKey", err)
	}
}

func TestUsageRefreshPreservesActiveResearchReservation(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 1000, Used: 500}, now)
	research, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Select(now, 10); err != nil {
		t.Fatal(err)
	}

	p.UpdateUsage("one", Usage{Limit: 1000, Used: 520}, now.Add(time.Minute))
	snapshot := p.Snapshots(now.Add(time.Minute))[0]
	if snapshot.EstimatedUsage != 250 || snapshot.ResearchReserved != 250 {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	p.SettleResearch(research)
	p.UpdateUsage("one", Usage{Limit: 1000, Used: 570}, now.Add(2*time.Minute))
	snapshot = p.Snapshots(now.Add(2 * time.Minute))[0]
	if snapshot.EstimatedUsage != 0 || snapshot.ResearchReserved != 0 {
		t.Fatalf("settled snapshot = %#v", snapshot)
	}
}

func TestResearchQuotaRejectionDoesNotExhaustKey(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 1000, Used: 603}, now)
	lease, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}

	p.Resolve(lease, 432, 0, now)
	snapshot := p.Snapshots(now)[0]
	if snapshot.RealUsage != 603 || snapshot.State != StateReady || !snapshot.ResearchBlocked {
		t.Fatalf("snapshot after 432 = %#v", snapshot)
	}
	if _, err := p.Select(now, 1); err != nil {
		t.Fatalf("ordinary Select() = %v", err)
	}
	if _, err := p.SelectFor(now, Selection{Estimate: 110, Workload: WorkloadResearch}); !errors.Is(err, ErrNoEligibleKey) {
		t.Fatalf("Research SelectFor() = %v", err)
	}
}

func TestResearchPauseClearsWhenHeadroomIncreases(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 1000, Used: 500}, now)
	active, err := p.SelectFor(now, Selection{Estimate: 110, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}
	p.Resolve(rejected, 432, 0, now)
	p.SettleResearch(active)
	p.UpdateUsage("one", Usage{Limit: 1000, Used: 550}, now.Add(time.Minute))

	if _, err := p.SelectFor(now.Add(time.Minute), Selection{Estimate: 110, Workload: WorkloadResearch}); err != nil {
		t.Fatalf("SelectFor() after increased headroom = %v", err)
	}
}

func TestConcurrentResearchAdmissionDoesNotOversubscribe(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one", APIKey: "tvly-one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 1000, Used: 500}, now)

	start := make(chan struct{})
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		if !errors.Is(err, ErrNoEligibleKey) {
			t.Fatalf("unexpected selection error: %v", err)
		}
	}
	if succeeded != 2 {
		t.Fatalf("successful reservations = %d, want 2", succeeded)
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
	wantCounts := []int{2, 2, 3, 3}
	seen := make(map[string]bool)
	gotCounts := make([]int, 0, len(p.groups))
	for _, group := range p.groups {
		gotCounts = append(gotCounts, len(group.keys))
		for name := range group.keys {
			if seen[name] {
				t.Errorf("key %q belongs to more than one group", name)
			}
			seen[name] = true
		}
	}
	slices.Sort(gotCounts)
	if !reflect.DeepEqual(gotCounts, wantCounts) {
		t.Errorf("group sizes = %v, want %v", gotCounts, wantCounts)
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

func TestResearchReservationsKeepGroupRoundAccountingMonotonic(t *testing.T) {
	now := time.Now()
	keys := []Key{{Name: "one", APIKey: "tvly-one"}, {Name: "two", APIKey: "tvly-two"}}
	p := New(keys, 1)
	for _, key := range keys {
		p.UpdateUsage(key.Name, Usage{Limit: 1000, Used: 0}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 2, UsageLimit: 600, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}

	rejected, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}
	p.Resolve(rejected, 432, 0, now)
	if got := p.groups[p.activeGroup].reserved; got != 0 {
		t.Fatalf("group reserved after 432 = %v, want 0", got)
	}

	accepted, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}
	p.Resolve(accepted, 201, 0, now)
	p.SettleResearch(accepted)
	p.UpdateUsage(accepted.Key.Name, Usage{Limit: 1000, Used: 100}, now.Add(time.Minute))
	if got := p.groups[p.activeGroup].reserved; got != 250 {
		t.Fatalf("group reserved after settlement = %v, want 250", got)
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

func TestGroupDefersAfterAllKeysCool(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one"}, {Name: "two"}}, 1)
	for _, name := range []string{"one", "two"} {
		p.UpdateUsage(name, Usage{Limit: 100}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 1, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}

	first, err := p.Select(now, 1)
	if err != nil {
		t.Fatal(err)
	}
	p.Resolve(first, 429, time.Hour, now)
	second, err := p.Select(now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.group == second.group {
		t.Fatalf("second lease reused cooling group %d", second.group)
	}
	snapshot := p.MonitorSnapshot(now)
	if !snapshot.Groups[first.group].Deferred || snapshot.Groups[first.group].Spent {
		t.Fatalf("first group snapshot = %+v, want deferred and not spent", snapshot.Groups[first.group])
	}
	if _, err := p.Select(now, 1); !errors.Is(err, ErrGroupRebuildRequired) {
		t.Fatalf("Select() after terminal groups = %v, want ErrGroupRebuildRequired", err)
	}
}

func TestResearchReservationClosesGroupWhenNextRequestDoesNotFit(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one"}, {Name: "two"}}, 1)
	for _, name := range []string{"one", "two"} {
		p.UpdateUsage(name, Usage{Limit: 1000}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 600, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}

	first, err := p.SelectFor(now, Selection{Estimate: 500, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}
	if first.group == second.group {
		t.Fatalf("second Research lease reused closed group %d", second.group)
	}
	if group := p.MonitorSnapshot(now).Groups[first.group]; !group.Spent || group.RoundUsage != 500 {
		t.Fatalf("first group snapshot = %+v, want closed with 500 round usage", group)
	}
}

func TestResearchQuotaRollbackDoesNotDeferGroup(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one"}}, 1)
	p.UpdateUsage("one", Usage{Limit: 1000}, now)
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 600, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}

	lease, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
	if err != nil {
		t.Fatal(err)
	}
	p.Resolve(lease, 432, 0, now)
	if group := p.MonitorSnapshot(now).Groups[lease.group]; group.Deferred || group.Spent {
		t.Fatalf("group after Research 432 = %+v, want neither deferred nor spent", group)
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
		if group.ReadyKeys != group.KeyCount {
			t.Errorf("group %d ready keys = %d, want %d", group.Index, group.ReadyKeys, group.KeyCount)
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

	gotSizes := []int{len(p.groups[0].keys), len(p.groups[1].keys), len(p.groups[2].keys), len(p.groups[3].keys)}
	slices.Sort(gotSizes)
	if !reflect.DeepEqual(gotSizes, []int{2, 2, 3, 3}) {
		t.Fatalf("group sizes = %v, want [2 2 3 3]", gotSizes)
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

func TestRebuildGroupsOrdersRotationByRemainingCapacity(t *testing.T) {
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
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}

	for index := 1; index < len(p.groups); index++ {
		if p.groups[index-1].remaining < p.groups[index].remaining {
			t.Fatalf("group totals = %v, want descending rotation order", groupRemaining(p.groups))
		}
	}

	lease, err := p.Select(now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.groups[0].keys[lease.Key.Name]; !ok {
		t.Errorf("first selected key %q is not in highest-capacity group", lease.Key.Name)
	}
}

func TestGroupRotationNameLessUsesMemberName(t *testing.T) {
	alpha := groupState{keys: map[string]struct{}{"alpha": {}}}
	zulu := groupState{keys: map[string]struct{}{"zulu": {}}}

	if !groupRotationNameLess(alpha, zulu) {
		t.Errorf("groupRotationNameLess(%v, %v) = false, want true", groupRotationNames(alpha), groupRotationNames(zulu))
	}
}

func TestRebuildGroupsUsesMonthlyHashForEqualCapacity(t *testing.T) {
	january := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	february := january.AddDate(0, 1, 0)
	names := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}

	januaryOrder := rebuildEqualCapacityGroupOrder(t, names, january)
	if sameJanuaryOrder := rebuildEqualCapacityGroupOrder(t, names, january); !reflect.DeepEqual(januaryOrder, sameJanuaryOrder) {
		t.Fatalf("same-month orders differ: first %v, second %v", januaryOrder, sameJanuaryOrder)
	}
	if februaryOrder := rebuildEqualCapacityGroupOrder(t, names, february); reflect.DeepEqual(januaryOrder, februaryOrder) {
		t.Fatalf("monthly orders are identical: %v", januaryOrder)
	}

	p := newEqualCapacityGroupPool(t, names, january)
	if err := p.RebuildGroups(january); err != nil {
		t.Fatal(err)
	}
	p.activeGroup = 3
	if err := p.RebuildGroups(february); err != nil {
		t.Fatal(err)
	}
	if p.activeGroup != 0 {
		t.Errorf("active group after month boundary = %d, want 0", p.activeGroup)
	}
}

func rebuildEqualCapacityGroupOrder(t *testing.T, names []string, now time.Time) []string {
	t.Helper()
	p := newEqualCapacityGroupPool(t, names, now)
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}
	order := make([]string, len(p.groups))
	for index, group := range p.groups {
		order[index] = groupRotationNames(group)[0]
	}
	return order
}

func newEqualCapacityGroupPool(t *testing.T, names []string, now time.Time) *Pool {
	t.Helper()
	keys := make([]Key, 0, len(names))
	for _, name := range names {
		keys = append(keys, Key{Name: name})
	}
	p := New(keys, 1)
	for _, key := range keys {
		p.UpdateUsage(key.Name, Usage{Limit: 100, Used: 0}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 10, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMonitorSnapshotReportsRefreshAndRebuildStatus(t *testing.T) {
	now := time.Now()
	p, later := terminalGroupPool(t, now)

	status := p.MonitorSnapshot(later).Refresh
	if !status.Pending {
		t.Errorf("Pending = false, want true while every group is terminal")
	}
	if status.RebuildAt.IsZero() {
		t.Error("RebuildAt is zero, want the last group rebuild time")
	}
	if status.Failures != 0 || status.Error != "" {
		t.Errorf("refresh status = %+v, want no failure before any sweep", status)
	}

	p.RecordRefreshBatch(later, 2, errors.Join(errors.New("one failed"), errors.New("two failed")))
	status = p.MonitorSnapshot(later).Refresh
	if status.Size != 2 || status.Failures != 2 || status.Error == "" || !status.At.Equal(later) {
		t.Errorf("refresh status = %+v, want two recorded Key failures in a batch of two", status)
	}

	p.RecordRefreshBatch(later.Add(time.Minute), 2, nil)
	if status = p.MonitorSnapshot(later).Refresh; status.Failures != 0 || status.Error != "" {
		t.Errorf("refresh status = %+v, want a successful sweep to clear the failure", status)
	}

	if err := p.RebuildGroups(later); err != nil {
		t.Fatal(err)
	}
	if status = p.MonitorSnapshot(later).Refresh; status.Pending {
		t.Errorf("Pending = true after a rebuild, want false: %+v", status)
	}
}

func TestRefreshSlotsStaggerByGroup(t *testing.T) {
	now := time.Now()
	names := []string{"one", "two", "three", "four"}
	keys := make([]Key, 0, len(names))
	for _, name := range names {
		keys = append(keys, Key{Name: name})
	}
	p := New(keys, 1)
	for _, name := range names {
		p.UpdateUsage(name, Usage{Limit: 100}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 2, UsageLimit: 10, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	if err := p.ConfigureRefresh(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}

	// 槽 0 的偏移上限是 10% 槽距（3m）+ 10s 槽内抖动，此后它的两个 Key 必然到期。
	due, wait := p.DueKeys(now.Add(3*time.Minute + 11*time.Second))
	if len(due) != 2 {
		t.Fatalf("due keys at slot 0 = %d, want 2", len(due))
	}
	if wait <= 0 || wait > 30*time.Second {
		t.Fatalf("wait = %v, want within (0, 30s]", wait)
	}
	for _, key := range due {
		p.RecordRefreshAttempt(key.Name, now.Add(3*time.Minute+11*time.Second), 0, nil)
	}

	// 槽 1 最早在 30m - 3m = 27m，10 分钟时不应有到期 Key。
	if due, _ = p.DueKeys(now.Add(10 * time.Minute)); len(due) != 0 {
		t.Fatalf("due keys at +10m = %d, want 0", len(due))
	}
	// 槽 1 最晚在 30m + 3m + 10s，35 分钟时必然到期。
	if due, _ = p.DueKeys(now.Add(35 * time.Minute)); len(due) != 2 {
		t.Fatalf("due keys at +35m = %d, want 2", len(due))
	}
}

func TestRefreshSlotsSpreadWithoutGroups(t *testing.T) {
	now := time.Now()
	names := []string{"one", "two", "three", "four"}
	keys := make([]Key, 0, len(names))
	for _, name := range names {
		keys = append(keys, Key{Name: name})
	}
	p := New(keys, 1)
	if err := p.ConfigureRefresh(time.Hour); err != nil {
		t.Fatal(err)
	}
	p.RescheduleRefreshes(now)

	// 槽距 15m（±1.5m）：起始只有第一把 Key 到期。
	due, _ := p.DueKeys(now.Add(time.Second))
	if len(due) != 1 {
		t.Fatalf("due keys at start = %d, want 1", len(due))
	}
	p.RecordRefreshAttempt(due[0].Name, now.Add(time.Second), 0, nil)
	if due, _ = p.DueKeys(now.Add(13 * time.Minute)); len(due) != 0 {
		t.Fatalf("due keys at +13m = %d, want 0", len(due))
	}
	if due, _ = p.DueKeys(now.Add(17 * time.Minute)); len(due) != 1 {
		t.Fatalf("due keys at +17m = %d, want 1", len(due))
	}
}

func TestRecordRefreshAttemptThrottle(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one"}}, 1)
	if err := p.ConfigureRefresh(time.Hour); err != nil {
		t.Fatal(err)
	}

	p.RecordRefreshAttempt("one", now, 0, nil)
	if at := p.keys["one"].refreshAt; at.Before(now.Add(time.Hour)) || at.After(now.Add(time.Hour+refreshSlotJitter)) {
		t.Fatalf("success refreshAt = %v, want now+period+[0,10s)", at)
	}
	if p.Refreshable("one", now) {
		t.Error("Refreshable right after a success = true, want false")
	}
	if !p.Refreshable("one", now.Add(time.Hour+refreshSlotJitter)) {
		t.Error("Refreshable after the period = false, want true")
	}

	p.RecordRefreshAttempt("one", now, 0, errors.New("send usage request: timeout"))
	if at := p.keys["one"].refreshAt; !at.Equal(now.Add(refreshRetryBackoff)) {
		t.Fatalf("failure refreshAt = %v, want now+2m", at)
	}

	p.RecordRefreshAttempt("one", now, time.Minute, errors.New("rate limited"))
	if at := p.keys["one"].refreshAt; !at.Equal(now.Add(refreshRateFloor)) {
		t.Fatalf("429 refreshAt = %v, want now+max(Retry-After,300s)", at)
	}
	p.RecordRefreshAttempt("one", now, 20*time.Minute, errors.New("rate limited"))
	if at := p.keys["one"].refreshAt; !at.Equal(now.Add(20 * time.Minute)) {
		t.Fatalf("429 refreshAt = %v, want now+20m when Retry-After is longer", at)
	}
}

func TestRescheduleKeepsThrottleFloor(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one"}, {Name: "two"}}, 1)
	for _, name := range []string{"one", "two"} {
		p.UpdateUsage(name, Usage{Limit: 100}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 10, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	if err := p.ConfigureRefresh(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}

	p.RecordRefreshAttempt("one", now, time.Minute, errors.New("rate limited"))
	if err := p.RebuildGroups(now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if at := p.keys["one"].refreshAt; at.Before(now.Add(refreshRateFloor)) {
		t.Fatalf("refreshAt after rebuild = %v, want the throttle floor preserved", at)
	}
}

func TestRefreshMetricsWindows(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one"}, {Name: "two"}}, 1)
	if err := p.ConfigureRefresh(time.Hour); err != nil {
		t.Fatal(err)
	}

	p.RecordRefreshAttempt("one", now.Add(-90*time.Minute), 0, nil)
	p.RecordRefreshAttempt("two", now.Add(-30*time.Minute), 0, nil)
	p.RecordRefreshAttempt("one", now.Add(-5*time.Minute), time.Minute, errors.New("rate limited"))

	status := p.MonitorSnapshot(now).Refresh
	if status.Requests != 2 {
		t.Errorf("Requests = %d, want 2 within the last period", status.Requests)
	}
	if status.RateLimited != 1 {
		t.Errorf("RateLimited = %d, want 1 within the last 10 minutes", status.RateLimited)
	}
	if status.MaxBurst != 1 {
		t.Errorf("MaxBurst = %d, want 1 for isolated requests", status.MaxBurst)
	}

	p.RecordRefreshAttempt("one", now.Add(-2*time.Minute), 0, nil)
	p.RecordRefreshAttempt("two", now.Add(-2*time.Minute+5*time.Second), 0, nil)
	if status = p.MonitorSnapshot(now).Refresh; status.MaxBurst != 2 {
		t.Errorf("MaxBurst = %d, want 2 for requests 5s apart", status.MaxBurst)
	}
}

func TestDueKeysRequiresConfiguredPeriod(t *testing.T) {
	now := time.Now()
	p := New([]Key{{Name: "one"}}, 1)
	if due, wait := p.DueKeys(now); due != nil || wait != time.Minute {
		t.Fatalf("DueKeys() = (%v, %v), want no schedule before ConfigureRefresh", due, wait)
	}
}

// terminalGroupPool returns a pool whose every group is terminal while both
// Keys are still usable, plus the time at which the first Key's Retry-After has
// elapsed. This is the state captured during the 2026-09-15 outage.
func terminalGroupPool(t *testing.T, now time.Time) (*Pool, time.Time) {
	t.Helper()
	p := New([]Key{{Name: "one"}, {Name: "two"}}, 1)
	for _, name := range []string{"one", "two"} {
		p.UpdateUsage(name, Usage{Limit: 10, Used: 0}, now)
	}
	if err := p.ConfigureGroups(GroupConfig{Size: 1, UsageLimit: 2, Location: time.UTC}); err != nil {
		t.Fatal(err)
	}
	if err := p.ConfigureRefresh(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := p.RebuildGroups(now); err != nil {
		t.Fatal(err)
	}

	cooled, err := p.Select(now, 1)
	if err != nil {
		t.Fatal(err)
	}
	p.Resolve(cooled, 429, time.Minute, now)
	if _, err := p.Select(now, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SelectFor(now, Selection{Estimate: 2}); !errors.Is(err, ErrGroupRebuildRequired) {
		t.Fatalf("SelectFor() with every group terminal = %v, want ErrGroupRebuildRequired", err)
	}
	// 槽 0 的偏移上限是 10% 槽距（30m 槽距下为 3m）再加 10s 槽内抖动。
	return p, now.Add(4 * time.Minute)
}

func groupRemaining(groups []groupState) []float64 {
	remaining := make([]float64, len(groups))
	for index, group := range groups {
		remaining[index] = group.remaining
	}
	return remaining
}
