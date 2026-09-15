package pool

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// refreshSweepBudget bounds one background usage sweep so a slow Tavily usage
// endpoint cannot keep it open indefinitely.
const refreshSweepBudget = 30 * time.Second

// UsageRefresher refreshes every configured key before a group rebuild.
type UsageRefresher func(context.Context) error

// Coordinator serializes usage refreshes and group rebuilds for request paths.
type Coordinator struct {
	pool       *Pool
	refresh    UsageRefresher
	rebuildMu  sync.Mutex
	refreshing atomic.Bool
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
	// Rebuilding only redistributes the capacity the pool already tracks, so it
	// must never wait for a usage refresh. Requiring a completed sweep let one
	// slow Tavily /usage call leave every group terminal and the pool refusing
	// Keys that were ready to serve requests.
	if err := c.pool.RebuildGroups(now); err != nil {
		c.refreshAsync(ctx)
		return Lease{}, ErrNoEligibleKey
	}
	lease, err = c.pool.SelectFor(now, selection)
	if err == nil {
		slog.Info("key groups rebuilt for the next round", "key", lease.Key.Name, "group", lease.group+1)
	} else {
		slog.Warn("key groups rebuilt without an eligible key", "error", err)
	}
	c.refreshAsync(ctx)
	return lease, err
}

// refreshAsync converges usage data without blocking the request that triggered
// a rebuild. The sweep runs on a detached context so a client that gives up
// early cannot abort it half way through the Keys.
func (c *Coordinator) refreshAsync(ctx context.Context) {
	if c.refresh == nil || !c.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.refreshing.Store(false)
		sweepCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshSweepBudget)
		defer cancel()
		err := c.refresh(sweepCtx)
		c.pool.RecordRefreshBatch(time.Now(), 0, err)
		if err != nil {
			slog.Warn("usage refresh sweep incomplete", "error", err)
		}
	}()
}
