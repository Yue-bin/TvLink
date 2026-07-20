# 2026-07-20 Issue Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct grouped-monitor semantics and make MCP Research calls report progress through deterministic Tavily polling with structured final results.

**Architecture:** Keep presentation calculations in `internal/monitor`, with Ready counts exposed through the pool monitor snapshot. Replace the MCP-only Tavily streaming adapter with a focused proxy runner that creates a non-streaming task, polls it with the creating Key, and reports status through a callback; the MCP handler maps that callback to Streamable HTTP SSE progress notifications.

**Tech Stack:** Go standard library, `html/template`, `net/http`, `httptest`, MCP `2025-03-26`, Tavily Research create/status endpoints.

---

## File Structure

- `internal/pool/pool.go`: expose per-group Ready counts.
- `internal/pool/pool_test.go`: verify group Ready counts are independent of active weights.
- `internal/monitor/view.go`: calculate round progress and page-wide Ready counts.
- `internal/monitor/view_test.go`: specify group and page summary semantics.
- `internal/monitor/template.go`: render round progress as the group primary metric.
- `internal/monitor/handler_test.go`: preserve visible labels and statistics.
- `internal/proxy/research.go`: own Research creation, same-Key polling, parsing, and cancellation.
- `internal/proxy/research_test.go`: specify polling and failure behavior with short test intervals.
- `internal/proxy/handler.go`: remove the obsolete streaming adapter and set the default interval.
- `internal/mcp/handler.go`: emit progress and final JSON-RPC messages through SSE.
- `internal/mcp/handler_test.go`: parse and verify MCP SSE events and structured results.

### Task 1: Show Group Round Progress

**Files:**
- Modify: `internal/pool/pool.go:68-81,466-487`
- Modify: `internal/pool/pool_test.go:203-239`
- Modify: `internal/monitor/view.go:38-49,108-157`
- Modify: `internal/monitor/view_test.go:71-105`
- Modify: `internal/monitor/template.go:331-340`
- Modify: `internal/monitor/handler_test.go:38-52`

- [ ] **Step 1: Write failing pool and monitor tests**

Rename `GroupSnapshot.AvailableKeys` to `ReadyKeys`. Require every group in
`TestMonitorSnapshotIncludesGroups` to report Ready members even when inactive:

```go
for _, group := range snapshot.Groups {
	if group.ReadyKeys != group.KeyCount {
		t.Errorf("group %d ready keys = %d, want %d", group.Index, group.ReadyKeys, group.KeyCount)
	}
}
```

Update `TestNewPageViewBuildsGroupFilters`:

```go
if view.Groups[0].RoundMetrics.UsageText != "600 / 600" ||
	view.Groups[0].RoundMetrics.ActualWidth != "width:100.00%" {
	t.Errorf("first group round progress = %+v", view.Groups[0].RoundMetrics)
}
if view.Groups[0].QuotaUsage != "20 (+3) / 100" ||
	view.Groups[0].ReadyKeys != 1 || view.Groups[0].Remaining != "77" {
	t.Errorf("first group metadata = %+v", view.Groups[0])
}
```

Require rendered HTML to contain `本轮次`, `Ready`, and `600 / 600`.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `go test ./internal/pool ./internal/monitor -run 'TestMonitorSnapshotIncludesGroups|TestNewPageViewBuildsGroupFilters|TestHandlerRendersUsageDashboard' -count=1`

Expected: FAIL because the new fields do not exist and group cards use Key quota progress.

- [ ] **Step 3: Implement Ready snapshots and round progress**

Count group readiness from state:

```go
if state.state == StateReady {
	snapshot.ReadyKeys++
}
```

Use these group fields:

```go
type groupView struct {
	ID           string
	Name         string
	State        string
	StateClass   string
	Active       bool
	RoundMetrics progressView
	QuotaUsage   string
	KeyCount     int
	ReadyKeys    int
	Remaining    string
}
```

Add the round metric:

```go
func newRoundProgressView(usage, limit float64) progressView {
	percent := percentageOf(usage, limit)
	return progressView{
		UsageText:         fmt.Sprintf("%s / %s", formatFloat(usage), formatFloat(limit)),
		ActualWidth:       template.CSS(fmt.Sprintf("width:%.2f%%", percent)),
		ActualPercentText: formatPercent(percent),
		AriaLabel:         fmt.Sprintf("本轮次已使用 %s，限额 %s", formatFloat(usage), formatFloat(limit)),
		Unavailable:       limit <= 0,
	}
}

func percentageOf(value, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return min(100, max(0, value/limit*100))
}
```

Build `RoundMetrics` from `newRoundProgressView(group.RoundUsage,
group.RoundLimit)`. Build `QuotaUsage` from
`newProgressView(group.RealUsage, group.EstimatedUsage, group.Limit).UsageText`.
Render only `progress-actual` in group cards, followed by `Ready {{.ReadyKeys}}
/ {{.KeyCount}}`, `Key 用量 {{.QuotaUsage}}`, and `预计剩余 {{.Remaining}}`.

- [ ] **Step 4: Format and verify GREEN**

Run: `gofmt -w internal/pool/pool.go internal/pool/pool_test.go internal/monitor/view.go internal/monitor/view_test.go internal/monitor/template.go internal/monitor/handler_test.go`

Run: `go test ./internal/pool ./internal/monitor -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add -- internal/pool/pool.go internal/pool/pool_test.go internal/monitor/view.go internal/monitor/view_test.go internal/monitor/template.go internal/monitor/handler_test.go
git commit -m "fix(monitor): 按轮次用量展示组进度"
```

### Task 2: Count Page-Wide Ready Keys

**Files:**
- Modify: `internal/monitor/view.go:74-106`
- Modify: `internal/monitor/view_test.go:10-38`

- [ ] **Step 1: Make the summary test fail on weight-based counting**

Set both Ready snapshots to `Weight: 0`, the cooling snapshot to `Weight: 999`,
and retain expected `view.AvailableKeys == 2`.

- [ ] **Step 2: Run focused test and verify RED**

Run: `go test ./internal/monitor -run TestNewPageViewAggregatesUsageAndBuildsRows -count=1`

Expected: FAIL with available count 1, want 2.

- [ ] **Step 3: Count Ready state**

```go
if key.State == pool.StateReady {
	view.AvailableKeys++
}
```

- [ ] **Step 4: Format and verify GREEN**

Run: `gofmt -w internal/monitor/view.go internal/monitor/view_test.go`

Run: `go test ./internal/monitor -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add -- internal/monitor/view.go internal/monitor/view_test.go
git commit -m "fix(monitor): 按 Ready 状态统计可用 Key"
```

### Task 3: Poll Tavily Research And Report MCP Progress

**Files:**
- Create: `internal/proxy/research.go`
- Create: `internal/proxy/research_test.go`
- Modify: `internal/proxy/handler.go:18-87`
- Modify: `internal/mcp/handler.go:35-177`
- Modify: `internal/mcp/handler_test.go:1-111`

- [ ] **Step 1: Write failing proxy polling tests**

Return pending from POST, in-progress from the first GET, and completed from the
second GET. Set `handler.researchPollInterval = time.Millisecond` and call:

```go
var statuses []string
result, err := handler.RunResearch(context.Background(), []byte(`{"input":"test","stream":true}`), func(status string) {
	statuses = append(statuses, status)
})
if err != nil {
	t.Fatalf("RunResearch() error = %v", err)
}
if got := string(result); !strings.Contains(got, `"status":"completed"`) {
	t.Fatalf("result = %s", got)
}
if !reflect.DeepEqual(statuses, []string{"pending", "in_progress"}) {
	t.Errorf("statuses = %v", statuses)
}
```

Assert POST contains `"stream":false`, both GETs use
`/research/research-1`, and all requests use the same authorization. Add table
tests for missing request ID, failed status, malformed JSON, non-success HTTP,
and context cancellation.

Use explicit terminal-response cases:

```go
tests := []struct {
	name       string
	statusCode int
	body       string
	wantError  string
}{
	{name: "missing request id", statusCode: http.StatusCreated, body: `{"status":"pending"}`, wantError: "missing request_id"},
	{name: "failed", statusCode: http.StatusCreated, body: `{"request_id":"research-1","status":"failed"}`, wantError: "research task failed"},
	{name: "malformed", statusCode: http.StatusCreated, body: `{`, wantError: "decode research response"},
	{name: "non-success", statusCode: http.StatusBadGateway, body: `bad gateway`, wantError: "research request returned 502"},
}
```

For cancellation, create a cancelled context before `RunResearch` and require
`errors.Is(err, context.Canceled)`.

- [ ] **Step 2: Run proxy tests and verify RED**

Run: `go test ./internal/proxy -run RunResearch -count=1`

Expected: FAIL because `RunResearch` and `researchPollInterval` do not exist.

- [ ] **Step 3: Implement the Research runner**

Add `const defaultResearchPollInterval = 5 * time.Second` and:

```go
type researchStatus struct {
	RequestID string          `json:"request_id"`
	Status    string          `json:"status"`
	Content   json.RawMessage `json:"content"`
}
```

`RunResearch` decodes an arguments object, forces `stream` false, selects one
lease, POSTs with that lease, resolves only the creation reservation, stores
the request mapping, and polls with `time.NewTicker`. Pending and in-progress
call the callback; completed returns exact response bytes; failed and unknown
statuses return errors. Close every body before waiting. Remove `StreamResearch`.

- [ ] **Step 4: Write failing MCP SSE progress test**

Use this fake:

```go
type fakeResearchRunner struct {
	result []byte
	err    error
}

func (f fakeResearchRunner) RunResearch(_ context.Context, _ []byte, progress func(string)) ([]byte, error) {
	progress("pending")
	progress("in_progress")
	return f.result, f.err
}
```

Call with `_meta.progressToken: "research-progress"`; split SSE events on blank
lines and require progress values 1 and 2 followed by the original response ID.

- [ ] **Step 5: Run MCP test and verify RED**

Run: `go test ./internal/mcp -run TestResearchReportsProgressOverSSE -count=1`

Expected: FAIL because the handler returns one JSON response.

- [ ] **Step 6: Implement MCP SSE messages**

```go
type researchRunner interface {
	RunResearch(context.Context, []byte, func(string)) ([]byte, error)
}
```

Set `text/event-stream` for Research only. Write each notification and the final
response as `data: <JSON>\n\n` and flush. Emit progress only for string or
integral numeric tokens, increment from one, and omit `total`. Move the JSON
content type to normal writers. Runner errors become final JSON-RPC error SSE.

- [ ] **Step 7: Format and verify GREEN**

Run: `gofmt -w internal/proxy/handler.go internal/proxy/research.go internal/proxy/research_test.go internal/mcp/handler.go internal/mcp/handler_test.go`

Run: `go test ./internal/proxy ./internal/mcp -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add -- internal/proxy/handler.go internal/proxy/research.go internal/proxy/research_test.go internal/mcp/handler.go internal/mcp/handler_test.go
git commit -m "fix(mcp): 轮询并报告 Research 进度"
```

### Task 4: Return Structured Research Results

**Files:**
- Modify: `internal/mcp/handler.go`
- Modify: `internal/mcp/handler_test.go`

- [ ] **Step 1: Write failing string and object content tests**

Use completed results with string content and object content:

```go
[]byte(`{"request_id":"research-1","status":"completed","content":"report","sources":[{"title":"source"}]}`)
[]byte(`{"request_id":"research-2","status":"completed","content":{"summary":"report"},"sources":[]}`)
```

Require the complete response in `structuredContent`; require text `report` for
the first and `{"summary":"report"}` for the second.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `go test ./internal/mcp -run 'TestResearchReturnsStructuredContent|TestResearchFormatsObjectContentAsText' -count=1`

Expected: FAIL because Research has no `structuredContent`.

- [ ] **Step 3: Build the final structured result**

Decode the body as `map[string]any` and decode `content` as `json.RawMessage`.
JSON strings become plain text; objects and arrays retain compact JSON text:

```go
result := map[string]any{
	"content":           []map[string]string{{"type": "text", "text": researchText(completed.Content)}},
	"structuredContent": structured,
	"isError":           false,
}
```

Malformed completed JSON returns a JSON-RPC error SSE event.

- [ ] **Step 4: Format and verify GREEN**

Run: `gofmt -w internal/mcp/handler.go internal/mcp/handler_test.go`

Run: `go test ./internal/mcp -count=1`

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add -- internal/mcp/handler.go internal/mcp/handler_test.go
git commit -m "fix(mcp): 返回 Research 结构化结果"
```

### Task 5: Full Verification

**Files:**
- Verify only; no planned source changes.

- [ ] **Step 1: Check formatting and diff**

Run: `gofmt -w internal/pool/pool.go internal/pool/pool_test.go internal/monitor/view.go internal/monitor/view_test.go internal/monitor/template.go internal/monitor/handler_test.go internal/proxy/handler.go internal/proxy/research.go internal/proxy/research_test.go internal/mcp/handler.go internal/mcp/handler_test.go`

Run: `git diff --check`

Expected: no output and exit 0.

- [ ] **Step 2: Run all tests and race tests**

Run: `go test ./... -count=1`

Run: `go test -race ./... -count=1`

Expected: every package reports `ok`; no race report.

- [ ] **Step 3: Run static analysis and build**

Run: `go vet ./...`

Run: `go build ./cmd/tvlink`

Expected: both exit 0 without diagnostics.

- [ ] **Step 4: Audit history and worktree**

Run: `git log -7 --oneline`

Run: `git status --short`

Expected: the design, plan, and four fix commits are present; only the original
human-authored `docs/2026-7-20问题整理.md` remains untracked.
