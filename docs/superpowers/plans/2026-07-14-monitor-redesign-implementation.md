# TvLink Monitor Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the existing monitor table with the approved dark, responsive usage dashboard, including aggregate usage and overlaid actual/projected progress rails.

**Architecture:** Keep `ServeHTTP` small by translating pool snapshots into presentation-only view models in `internal/monitor/view.go`. Keep the large static HTML/CSS template in `internal/monitor/template.go`; the handler remains responsible only for method validation, headers, snapshot acquisition, view construction, and template execution.

**Tech Stack:** Go 1.26 standard library, `html/template`, `net/http`, `httptest`, table-driven tests.

---

## File Structure

- Create `internal/monitor/view.go`: aggregate calculation, progress widths, numeric/timestamp formatting, and state presentation.
- Create `internal/monitor/view_test.go`: deterministic unit tests for view-model math and formatting edge cases.
- Create `internal/monitor/template.go`: parsed server-rendered HTML template and all dark responsive CSS.
- Modify `internal/monitor/handler.go`: request handling and rendering only.
- Modify `internal/monitor/handler_test.go`: rendered-page contract, headers, method handling, empty state, and secret redaction.

### Task 1: Build Monitor View Models Test-First

**Files:**
- Create: `internal/monitor/view_test.go`
- Create: `internal/monitor/view.go`

- [ ] **Step 1: Write the failing aggregate and row test**

Create `internal/monitor/view_test.go` with deterministic snapshots and assert aggregate semantics before writing production code:

```go
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
```

- [ ] **Step 2: Run the new test and verify it fails**

Run: `go test ./internal/monitor -run TestNewPageViewAggregatesUsageAndBuildsRows -v`

Expected: compilation fails because `newPageView` and its view-model types do not exist.

- [ ] **Step 3: Add failing edge-case tests**

Append tests that lock down clamping, independent remaining aggregation, unavailable limits, decimal formatting, and zero times:

```go
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
}

func TestNewPageViewRendersEmptyState(t *testing.T) {
	view := newPageView(nil, 5*time.Second, time.Now())
	if !view.Empty || view.Total.UsageText != "0 (+0) / 0" || view.RefreshSeconds != 5 {
		t.Errorf("empty view = %+v", view)
	}
}
```

- [ ] **Step 4: Implement the monitor view model**

Create `internal/monitor/view.go` with these exact types and responsibilities:

```go
package monitor

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"tvlink/internal/pool"
)

type progressView struct {
	UsageText           string
	ActualWidth         template.CSS
	ProjectedWidth      template.CSS
	ActualPercentText   string
	ProjectedPercentText string
	AriaLabel           string
	Unavailable         bool
}

type keyView struct {
	Name       string
	State      string
	StateClass string
	Metrics    progressView
	UpdatedAt  string
	Remaining  string
	Weight     string
	RetryAt    string
	ShowRetry  bool
}

type pageView struct {
	RefreshSeconds    int64
	Total             progressView
	ProjectedRemaining string
	AvailableKeys     int
	TotalKeys         int
	Rows              []keyView
	Empty             bool
}

func newPageView(snapshots []pool.Snapshot, refreshInterval time.Duration, now time.Time) pageView {
	view := pageView{
		RefreshSeconds: int64(refreshInterval.Seconds()),
		TotalKeys:      len(snapshots),
		Rows:           make([]keyView, 0, len(snapshots)),
		Empty:          len(snapshots) == 0,
	}
	var totalLimit, totalActual int64
	var totalEstimated, totalRemaining float64
	for _, snapshot := range snapshots {
		totalLimit += snapshot.Limit
		totalActual += snapshot.RealUsage
		totalEstimated += snapshot.EstimatedUsage
		totalRemaining += snapshot.Remaining
		if snapshot.Weight > 0 {
			view.AvailableKeys++
		}
		view.Rows = append(view.Rows, keyView{
			Name:       snapshot.Name,
			State:      string(snapshot.State),
			StateClass: "state-" + string(snapshot.State),
			Metrics:    newProgressView(snapshot.RealUsage, snapshot.EstimatedUsage, snapshot.Limit),
			UpdatedAt:  formatTimestamp(snapshot.RealUsageAt),
			Remaining:  formatFloat(snapshot.Remaining),
			Weight:     formatFloat(snapshot.Weight),
			RetryAt:    formatTimestamp(snapshot.RetryAt),
			ShowRetry:  snapshot.State == pool.StateCooling,
		})
	}
	view.Total = newProgressView(totalActual, totalEstimated, totalLimit)
	view.ProjectedRemaining = formatFloat(totalRemaining)
	return view
}

func newProgressView(actual int64, estimated float64, limit int64) progressView {
	actualPercent := percentage(float64(actual), limit)
	projectedPercent := percentage(float64(actual)+estimated, limit)
	return progressView{
		UsageText:            fmt.Sprintf("%s (+%s) / %s", formatInt(actual), formatFloat(estimated), formatInt(limit)),
		ActualWidth:          template.CSS(fmt.Sprintf("width:%.2f%%", actualPercent)),
		ProjectedWidth:       template.CSS(fmt.Sprintf("width:%.2f%%", projectedPercent)),
		ActualPercentText:    formatPercent(actualPercent),
		ProjectedPercentText: formatPercent(projectedPercent),
		AriaLabel:            fmt.Sprintf("实际用量 %s，预计总用量 %s，额度 %s", formatInt(actual), formatFloat(float64(actual)+estimated), formatInt(limit)),
		Unavailable:          limit <= 0,
	}
}

func percentage(value float64, limit int64) float64 {
	if limit <= 0 {
		return 0
	}
	return min(100, max(0, value/float64(limit)*100))
}

func formatPercent(value float64) string {
	return formatFloat(value) + "%"
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "--"
	}
	return value.Local().Format("01-02 15:04:05")
}

func formatInt(value int64) string {
	return groupInteger(strconv.FormatInt(value, 10))
}

func formatFloat(value float64) string {
	rounded := math.Round(value*100) / 100
	raw := strconv.FormatFloat(rounded, 'f', 2, 64)
	raw = strings.TrimRight(strings.TrimRight(raw, "0"), ".")
	parts := strings.SplitN(raw, ".", 2)
	parts[0] = groupInteger(parts[0])
	return strings.Join(parts, ".")
}

func groupInteger(value string) string {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign, value = "-", strings.TrimPrefix(value, "-")
	}
	for index := len(value) - 3; index > 0; index -= 3 {
		value = value[:index] + "," + value[index:]
	}
	return sign + value
}
```

- [ ] **Step 5: Run view-model tests and verify they pass**

Run: `go test ./internal/monitor -run 'TestNewPageView' -v`

Expected: all three view-model tests pass.

- [ ] **Step 6: Commit the tested view model**

```powershell
git add -- internal/monitor/view.go internal/monitor/view_test.go
git commit -m "feat: 计算监控页汇总用量"
```

### Task 2: Render the Approved Dashboard Test-First

**Files:**
- Create: `internal/monitor/template.go`
- Modify: `internal/monitor/handler.go`
- Modify: `internal/monitor/handler_test.go`

- [ ] **Step 1: Replace the handler test with the rendered dashboard contract**

Keep the existing actual/estimated setup, add a second Key, and assert stable semantic output rather than full HTML snapshots:

```go
func TestHandlerRendersUsageDashboard(t *testing.T) {
	now := time.Now()
	p := pool.New([]pool.Key{
		{Name: "primary-01", APIKey: "tvly-secret-one"},
		{Name: "primary-02", APIKey: "tvly-secret-two"},
	}, 1)
	p.UpdateUsage("primary-01", pool.Usage{Limit: 100, Used: 20}, now)
	p.UpdateUsage("primary-02", pool.Usage{Limit: 200, Used: 40}, now)
	if _, err := p.Select(now, 3); err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	response := httptest.NewRecorder()
	New(p, 5*time.Second).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, text := range []string{
		"TvLink 用量监控", "总用量", "60 (+3) / 300", "primary-01", "primary-02",
		"usage-progress", "progress-actual", "progress-projected", "预计剩余", "自动刷新 5 秒",
	} {
		if !strings.Contains(body, text) {
			t.Errorf("page does not contain %q", text)
		}
	}
	for _, secret := range []string{"tvly-secret-one", "tvly-secret-two"} {
		if strings.Contains(body, secret) {
			t.Errorf("page exposes Tavily secret %q", secret)
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
}

func TestHandlerRejectsNonGET(t *testing.T) {
	p := pool.New(nil, 1)
	response := httptest.NewRecorder()
	New(p, 5*time.Second).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlerRendersEmptyState(t *testing.T) {
	p := pool.New(nil, 1)
	response := httptest.NewRecorder()
	New(p, 5*time.Second).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(response.Body.String(), "尚未配置可监控的 Key") {
		t.Fatalf("page does not contain empty state")
	}
}
```

- [ ] **Step 2: Run handler tests and verify the dashboard test fails**

Run: `go test ./internal/monitor -run TestHandler -v`

Expected: `TestHandlerRendersUsageDashboard` and `TestHandlerRendersEmptyState` fail because the current table has no aggregate summary, progress rails, or empty state; the non-GET test passes.

- [ ] **Step 3: Move template ownership out of the handler**

Create `internal/monitor/template.go` with `var pageTemplate = template.Must(template.New("monitor").Parse(pageHTML))` and a `pageHTML` document containing these exact semantic blocks:

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="refresh" content="{{.RefreshSeconds}}">
  <title>TvLink 监控</title>
  <style>
    :root{color-scheme:dark;--page:#090e12;--panel:#111920;--panel-2:#0d1318;--edge:#283946;--text:#eef5f8;--muted:#8598a4;--accent:#70c9f3;--actual:#203b4b;--actual-edge:#4e7890;--estimate:#b6d9e9;--track:#060a0d;--ready:#77d2ad;--warning:#ff9080}
    *{box-sizing:border-box}body{margin:0;background:var(--page);color:var(--text);font-family:Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;letter-spacing:0}.shell{width:min(980px,calc(100% - 36px));margin:0 auto;padding:32px 0 48px}.topbar{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:18px}.identity h1{margin:0 0 5px;font-size:21px}.identity p{margin:0;color:var(--muted);font-size:12px}.live{display:flex;align-items:center;gap:7px;color:var(--muted);font-size:10px;text-transform:uppercase}.live-dot{width:7px;height:7px;border-radius:50%;background:var(--ready);box-shadow:0 0 0 3px rgba(119,210,173,.09)}.summary{padding:18px;background:var(--panel);border:1px solid var(--edge);border-radius:7px}.summary-top,.row-top{display:flex;align-items:end;justify-content:space-between;gap:14px}.eyebrow{color:var(--muted);font-size:10px;font-weight:700;text-transform:uppercase}.usage-number{margin-top:6px;font-size:28px;line-height:1;font-weight:760;font-variant-numeric:tabular-nums}.projected-percent{color:var(--accent);font-size:14px;font-weight:700}.usage-progress{position:relative;height:14px;margin-top:16px;background:var(--track);border-radius:3px}.progress-projected{position:absolute;z-index:1;inset:0 auto 0 0;border:1px dashed var(--estimate);border-radius:3px}.progress-actual{position:absolute;z-index:2;top:3px;bottom:3px;left:3px;background:var(--actual);border:1px solid var(--actual-edge);border-radius:2px}.unavailable{opacity:.45}.summary-meta,.row-meta{display:flex;flex-wrap:wrap;gap:7px 16px;margin-top:12px;color:var(--muted);font-size:10px}.summary-meta strong,.row-meta strong{color:var(--text);font-weight:650}.key-list{margin-top:17px;border-top:1px solid var(--edge)}.key-row{padding:16px 2px 15px;border-bottom:1px solid var(--edge)}.key-line{display:flex;align-items:center;gap:9px;min-width:0}.key-name{overflow:hidden;font-size:13px;font-weight:700;text-overflow:ellipsis;white-space:nowrap}.status{padding:3px 6px;border-radius:4px;font-size:9px;font-weight:760;text-transform:uppercase}.state-ready{color:var(--ready);border:1px solid rgba(119,210,173,.24);background:rgba(119,210,173,.09)}.state-cooling,.state-exhausted{color:var(--warning);border:1px solid rgba(255,144,128,.24);background:rgba(255,144,128,.08)}.state-pending,.state-probing{color:var(--muted);border:1px solid var(--edge);background:var(--panel-2)}.row-usage{font-size:12px;font-weight:700;font-variant-numeric:tabular-nums}.empty{padding:32px 0;color:var(--muted);font-size:13px;text-align:center}@media(max-width:620px){.shell{width:min(100% - 20px,980px);padding-top:22px}.topbar,.summary-top,.row-top{align-items:flex-start;flex-direction:column}.usage-number{font-size:23px}.row-usage{white-space:normal}}
  </style>
</head>
<body>
  <main class="shell">
    <header class="topbar">
      <div class="identity"><h1>TvLink 用量监控</h1><p>权威快照与本地预估</p></div>
      <div class="live"><i class="live-dot"></i>自动刷新 {{.RefreshSeconds}} 秒</div>
    </header>
    <section class="summary">
      <div class="summary-top"><div><div class="eyebrow">总用量</div><div class="usage-number">{{.Total.UsageText}}</div></div><div class="projected-percent">{{.Total.ProjectedPercentText}} projected</div></div>
      <div class="usage-progress{{if .Total.Unavailable}} unavailable{{end}}" role="img" aria-label="{{.Total.AriaLabel}}"><div class="progress-projected" style="{{.Total.ProjectedWidth}}"></div><div class="progress-actual" style="{{.Total.ActualWidth}}"></div></div>
      <div class="summary-meta"><span>实际 <strong>{{.Total.ActualPercentText}}</strong></span><span>预计 <strong>{{.Total.ProjectedPercentText}}</strong></span><span>预计剩余 <strong>{{.ProjectedRemaining}}</strong></span><span>可用 Key <strong>{{.AvailableKeys}} / {{.TotalKeys}}</strong></span></div>
    </section>
    {{if .Empty}}<div class="empty">尚未配置可监控的 Key</div>{{else}}
    <section class="key-list">{{range .Rows}}
      <article class="key-row">
        <div class="row-top"><div class="key-line"><span class="key-name">{{.Name}}</span><span class="status {{.StateClass}}">{{.State}}</span></div><span class="row-usage">{{.Metrics.UsageText}}</span></div>
        <div class="usage-progress{{if .Metrics.Unavailable}} unavailable{{end}}" role="img" aria-label="{{.Metrics.AriaLabel}}"><div class="progress-projected" style="{{.Metrics.ProjectedWidth}}"></div><div class="progress-actual" style="{{.Metrics.ActualWidth}}"></div></div>
        <div class="row-meta"><span>实际更新 <strong>{{.UpdatedAt}}</strong></span><span>预计剩余 <strong>{{.Remaining}}</strong></span><span>权重 <strong>{{.Weight}}</strong></span>{{if .ShowRetry}}<span>重试时间 <strong>{{.RetryAt}}</strong></span>{{end}}</div>
      </article>{{end}}</section>{{end}}
  </main>
</body>
</html>
```

The Go file must import only `html/template`, parse this document once at package initialization, and contain no handler logic.

- [ ] **Step 4: Simplify `ServeHTTP` to render the view model**

Replace the inline template and anonymous data struct in `internal/monitor/handler.go` with:

```go
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now()
	view := newPageView(h.pool.Snapshots(now), h.refreshInterval, now)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, view); err != nil {
		http.Error(w, "render monitor", http.StatusInternalServerError)
	}
}
```

Remove the `html/template` import from `handler.go`; retain `net/http`, `time`, and `tvlink/internal/pool`.

- [ ] **Step 5: Run monitor tests and verify they pass**

Run: `go test ./internal/monitor -v`

Expected: all view-model and handler tests pass.

- [ ] **Step 6: Format and commit the rendered dashboard**

Run: `gofmt -w internal/monitor/handler.go internal/monitor/handler_test.go internal/monitor/template.go internal/monitor/view.go internal/monitor/view_test.go`

Run: `go test ./internal/monitor -v`

Expected: PASS.

```powershell
git add -- internal/monitor/handler.go internal/monitor/handler_test.go internal/monitor/template.go
git commit -m "feat: 重构深色用量监控页"
```

### Task 3: Verify Repository Behavior And Visual Output

**Files:**
- Modify only if a failing verification reveals a monitor regression.

- [ ] **Step 1: Run all unit tests**

Run: `go test ./...`

Expected: every package reports `ok`; no package fails.

- [ ] **Step 2: Run race tests**

Run: `go test -race ./...`

Expected: every package reports `ok` and the race detector reports no races.

- [ ] **Step 3: Run static analysis**

Run: `go vet ./...`

Expected: exit code 0 with no diagnostics.

- [ ] **Step 4: Run formatting and patch checks**

Run: `gofmt -l internal cmd`

Expected: no output.

Run: `git diff --check`

Expected: no whitespace errors.

- [ ] **Step 5: Inspect the live page at desktop and mobile widths**

Start the existing service with the local configuration:

Run: `go run ./cmd/tvlink -config tvlink.toml`

Open `http://localhost:8080/` and verify at approximately 1280px and 390px widths:

- aggregate summary is first and fully visible;
- actual fill overlays the projected dashed outline;
- usage text does not wrap incoherently or overlap badges;
- metadata wraps without horizontal scrolling;
- pending, cooling, and exhausted colors remain readable;
- the page refreshes at the configured interval.

If the configured port is already occupied, use a temporary config with another local port and do not modify the committed example configuration.

- [ ] **Step 6: Commit only verification-driven fixes**

If Step 1-5 require code changes, first add a focused failing regression test, implement the minimum fix, rerun all verification commands, then commit only the monitor files:

```powershell
git add -- internal/monitor
git commit -m "fix: 完善监控页边界状态"
```

If no fixes are required, do not create an empty commit.
