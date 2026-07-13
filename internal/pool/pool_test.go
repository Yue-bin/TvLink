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
