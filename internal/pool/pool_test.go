package pool

import (
	"errors"
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
