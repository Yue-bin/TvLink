package monitor

import (
	"testing"
	"time"

	"tvlink/internal/pool"
)

func TestNewPageViewAggregatesUsageAndBuildsRows(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.Local)
	snapshots := []pool.Snapshot{
		{Name: "primary-01", Limit: 500, RealUsage: 210, EstimatedUsage: 18, Remaining: 272, Weight: 0, State: pool.StateReady, RealUsageAt: now.Add(-12 * time.Second), ResearchReserved: 110, ResearchBlocked: true},
		{Name: "primary-02", Limit: 500, RealUsage: 330, EstimatedUsage: 37, Remaining: 133, Weight: 0, State: pool.StateReady, RealUsageAt: now.Add(-18 * time.Second)},
		{Name: "backup-cn", Limit: 500, RealUsage: 200, EstimatedUsage: 0, Remaining: 300, Weight: 999, State: pool.StateCooling, RealUsageAt: now.Add(-23 * time.Second), RetryAt: now.Add(42 * time.Second)},
	}

	view := newPageView(pool.MonitorSnapshot{Keys: snapshots}, now)

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
	if view.Rows[0].ResearchReserved != "110" || !view.Rows[0].ShowResearchReserved || !view.Rows[0].ResearchBlocked {
		t.Errorf("first row research state = %+v", view.Rows[0])
	}
	if !view.Rows[2].ShowRetry || view.Rows[2].RetryAt != "07-14 12:00:42" {
		t.Errorf("cooling retry = show %v, value %q", view.Rows[2].ShowRetry, view.Rows[2].RetryAt)
	}
}

func TestNewPageViewHandlesUnavailableAndClampedUsage(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.Local)
	view := newPageView(pool.MonitorSnapshot{Keys: []pool.Snapshot{
		{Name: "over", Limit: 10, RealUsage: 12, EstimatedUsage: 0.25, Remaining: 0, State: pool.StateExhausted},
		{Name: "pending", Limit: 0, State: pool.StatePending},
	}}, now)

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
	view := newPageView(pool.MonitorSnapshot{}, time.Now())
	if !view.Empty || view.Total.UsageText != "0 (+0) / 0" {
		t.Errorf("empty view = %+v", view)
	}
}

func TestNewPageViewBuildsGroupFilters(t *testing.T) {
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.Local)
	snapshot := pool.MonitorSnapshot{
		GroupingEnabled: true,
		ActiveGroup:     2,
		Keys: []pool.Snapshot{
			{Name: "one", Group: 1, Limit: 100, RealUsage: 20, EstimatedUsage: 3, Remaining: 77, State: pool.StateReady},
			{Name: "two", Group: 2, Limit: 200, RealUsage: 80, EstimatedUsage: 5, Remaining: 115, Weight: 100, State: pool.StateReady},
		},
		Groups: []pool.GroupSnapshot{
			{Index: 1, Spent: true, KeyCount: 1, ReadyKeys: 1, Limit: 100, RealUsage: 20, EstimatedUsage: 3, Remaining: 77, RoundUsage: 600, RoundLimit: 600},
			{Index: 2, Active: true, KeyCount: 1, ReadyKeys: 1, Limit: 200, RealUsage: 80, EstimatedUsage: 5, Remaining: 115, RoundUsage: 384, RoundLimit: 600},
		},
	}

	view := newPageView(snapshot, now)
	if !view.GroupingEnabled || view.ActiveGroupName != "Group 2" || len(view.Groups) != 2 {
		t.Fatalf("group view = %+v", view)
	}
	if view.Groups[0].ID != "group-1" || view.Groups[0].State != "本轮完成" {
		t.Errorf("first group = %+v", view.Groups[0])
	}
	if view.Groups[0].RoundMetrics.UsageText != "600 / 600" || view.Groups[0].RoundMetrics.ActualWidth != "width:100.00%" {
		t.Errorf("first group round progress = %+v", view.Groups[0].RoundMetrics)
	}
	if view.Groups[0].QuotaUsage != "20 (+3) / 100" || view.Groups[0].ReadyKeys != 1 || view.Groups[0].Remaining != "77" {
		t.Errorf("first group metadata = %+v", view.Groups[0])
	}
	if view.Groups[1].RoundMetrics.UsageText != "384 / 600" || view.Groups[1].ReadyKeys != 1 {
		t.Errorf("active group = %+v", view.Groups[1])
	}
	if view.Rows[0].GroupID != "group-1" || view.Rows[1].GroupName != "Group 2" {
		t.Errorf("key groups = %+v", view.Rows)
	}
	if view.GeneratedAt != "07-15 12:00:00" {
		t.Errorf("GeneratedAt = %q", view.GeneratedAt)
	}
}

func TestNewPageViewShowsGroupTerminalStates(t *testing.T) {
	view := newPageView(pool.MonitorSnapshot{
		GroupingEnabled: true,
		ActiveGroup:     1,
		Groups: []pool.GroupSnapshot{
			{Index: 1, Active: true, Spent: true, KeyCount: 1, RoundLimit: 600, RoundUsage: 600},
			{Index: 2, Deferred: true, KeyCount: 1, RoundLimit: 600, RoundUsage: 250},
		},
	}, time.Now())

	if got := view.Groups[0]; got.State != "本轮完成" || got.StateClass != "group-spent" {
		t.Errorf("spent active group = %+v, want completed state", got)
	}
	if got := view.Groups[1]; got.State != "本轮延后" || got.StateClass != "group-deferred" {
		t.Errorf("deferred group = %+v, want deferred state", got)
	}
}

func TestNewPageViewShowsRefreshAndRoundStatus(t *testing.T) {
	refreshedAt := time.Date(2026, time.September, 15, 8, 24, 37, 0, time.Local)
	rebuiltAt := time.Date(2026, time.September, 15, 8, 4, 12, 0, time.Local)

	healthy := newPageView(pool.MonitorSnapshot{
		Refresh: pool.RefreshStatus{At: refreshedAt},
	}, time.Now())
	if !healthy.Status.Visible || healthy.Status.Alert {
		t.Errorf("healthy status = %+v, want a visible non-alert status", healthy.Status)
	}
	if healthy.Status.Refresh != "用量刷新正常 · 09-15 08:24:37" {
		t.Errorf("healthy refresh text = %q", healthy.Status.Refresh)
	}

	failed := newPageView(pool.MonitorSnapshot{
		Refresh: pool.RefreshStatus{
			At:       refreshedAt,
			Failures: 10,
			Error:    "send usage request: Get \"https://api.tavily.com/usage\": context deadline exceeded",
		},
	}, time.Now())
	if !failed.Status.Alert {
		t.Errorf("failed status = %+v, want an alert", failed.Status)
	}
	if failed.Status.Refresh != "用量刷新失败 · 10 个 Key · 09-15 08:24:37" {
		t.Errorf("failed refresh text = %q", failed.Status.Refresh)
	}
	if failed.Status.Note == "" {
		t.Error("failed status note = empty, want the sweep error")
	}

	pending := newPageView(pool.MonitorSnapshot{
		Refresh: pool.RefreshStatus{At: refreshedAt, Pending: true, RebuildAt: rebuiltAt},
	}, time.Now())
	if !pending.Status.Alert {
		t.Errorf("pending status = %+v, want an alert", pending.Status)
	}
	if pending.Status.Round != "本轮已无可用组：下一次请求将重建分组（上次重建 09-15 08:04:12）" {
		t.Errorf("pending round text = %q", pending.Status.Round)
	}

	empty := newPageView(pool.MonitorSnapshot{}, time.Now())
	if empty.Status.Visible {
		t.Errorf("status without any sweep = %+v, want it hidden", empty.Status)
	}
}
