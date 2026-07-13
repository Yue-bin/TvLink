package monitor

import (
	"testing"
	"time"

	"tvlink/internal/pool"
)

func TestNewPageViewAggregatesUsageAndBuildsRows(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.Local)
	snapshots := []pool.Snapshot{
		{Name: "primary-01", Limit: 500, RealUsage: 210, EstimatedUsage: 18, Remaining: 272, Weight: 272, State: pool.StateReady, RealUsageAt: now.Add(-12 * time.Second)},
		{Name: "primary-02", Limit: 500, RealUsage: 330, EstimatedUsage: 37, Remaining: 133, Weight: 133, State: pool.StateReady, RealUsageAt: now.Add(-18 * time.Second)},
		{Name: "backup-cn", Limit: 500, RealUsage: 200, EstimatedUsage: 0, Remaining: 300, Weight: 0, State: pool.StateCooling, RealUsageAt: now.Add(-23 * time.Second), RetryAt: now.Add(42 * time.Second)},
	}

	view := newPageView(snapshots, 5*time.Second, now)

	if view.Total.UsageText != "740 (+55) / 1,500" {
		t.Errorf("total usage = %q", view.Total.UsageText)
	}
	if view.Total.ProjectedPercentText != "53%" {
		t.Errorf("projected percent = %q", view.Total.ProjectedPercentText)
	}
	if view.ProjectedRemaining != "705" || view.AvailableKeys != 2 || view.TotalKeys != 3 {
		t.Errorf("summary = remaining %q, available %d/%d", view.ProjectedRemaining, view.AvailableKeys, view.TotalKeys)
	}
	if view.Rows[0].Metrics.UsageText != "210 (+18) / 500" {
		t.Errorf("first row usage = %q", view.Rows[0].Metrics.UsageText)
	}
	if view.Rows[0].Metrics.ActualWidth != "width:42.00%" || view.Rows[0].Metrics.ProjectedWidth != "width:45.60%" {
		t.Errorf("first row widths = %q, %q", view.Rows[0].Metrics.ActualWidth, view.Rows[0].Metrics.ProjectedWidth)
	}
	if !view.Rows[2].ShowRetry || view.Rows[2].RetryAt != "07-14 12:00:42" {
		t.Errorf("cooling retry = show %v, value %q", view.Rows[2].ShowRetry, view.Rows[2].RetryAt)
	}
}

func TestNewPageViewHandlesUnavailableAndClampedUsage(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.Local)
	view := newPageView([]pool.Snapshot{
		{Name: "over", Limit: 10, RealUsage: 12, EstimatedUsage: 0.25, Remaining: 0, State: pool.StateExhausted},
		{Name: "pending", Limit: 0, State: pool.StatePending},
	}, 5*time.Second, now)

	if view.Total.UsageText != "12 (+0.25) / 10" {
		t.Errorf("total usage = %q", view.Total.UsageText)
	}
	if view.Total.ActualWidth != "width:100.00%" || view.Total.ProjectedWidth != "width:100.00%" {
		t.Errorf("clamped widths = %q, %q", view.Total.ActualWidth, view.Total.ProjectedWidth)
	}
	if view.ProjectedRemaining != "0" {
		t.Errorf("remaining = %q, want 0", view.ProjectedRemaining)
	}
	if !view.Rows[1].Metrics.Unavailable || view.Rows[1].UpdatedAt != "--" {
		t.Errorf("pending row = unavailable %v, updated %q", view.Rows[1].Metrics.Unavailable, view.Rows[1].UpdatedAt)
	}
	if view.Rows[1].Metrics.UsageText != "尚无用量数据" {
		t.Errorf("pending usage = %q", view.Rows[1].Metrics.UsageText)
	}
}

func TestNewPageViewRendersEmptyState(t *testing.T) {
	view := newPageView(nil, 5*time.Second, time.Now())
	if !view.Empty || view.Total.UsageText != "0 (+0) / 0" || view.RefreshSeconds != 5 {
		t.Errorf("empty view = %+v", view)
	}
}
