package pool

import (
	"context"
	"errors"
	"sync"
	"time"
)

// UsageRefresher refreshes every configured key before a group rebuild.
type UsageRefresher func(context.Context) error

// Coordinator serializes usage refreshes and group rebuilds for request paths.
type Coordinator struct {
	pool      *Pool
	refresh   UsageRefresher
	rebuildMu sync.Mutex
}

// NewCoordinator creates a grouped selection coordinator.
func NewCoordinator(keyPool *Pool, refresh UsageRefresher) *Coordinator {
	return &Coordinator{pool: keyPool, refresh: refresh}
}

// Select reserves a key, rebuilding groups once when required.
func (c *Coordinator) Select(ctx context.Context, now time.Time, estimate float64) (Lease, error) {
	return c.SelectFor(ctx, now, Selection{Estimate: estimate})
}

// SelectFor reserves a Key with endpoint-specific selection constraints.
func (c *Coordinator) SelectFor(ctx context.Context, now time.Time, selection Selection) (Lease, error) {
	lease, err := c.pool.SelectFor(now, selection)
	if !errors.Is(err, ErrGroupRebuildRequired) {
		return lease, err
	}
	c.rebuildMu.Lock()
	defer c.rebuildMu.Unlock()
	lease, err = c.pool.SelectFor(now, selection)
	if !errors.Is(err, ErrGroupRebuildRequired) {
		return lease, err
	}
	if err := c.refresh(ctx); err != nil {
		return Lease{}, ErrNoEligibleKey
	}
	if err := c.pool.RebuildGroups(now); err != nil {
		return Lease{}, ErrNoEligibleKey
	}
	return c.pool.SelectFor(now, selection)
}
