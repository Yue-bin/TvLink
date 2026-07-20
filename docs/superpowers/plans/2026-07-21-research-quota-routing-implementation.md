# Research Quota Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent in-flight Tavily Research tasks from oversubscribing Key credits and transparently fail over Research creation after 432/433 without falsifying authoritative usage.

**Architecture:** The Pool owns identified request reservations, preserves active Research reservations across `/usage` refreshes, and applies a Research-only quota pause. REST and MCP share one admission helper that reserves the documented maximum cost, retries distinct Keys, and stores the successful lease with the Research mapping. The monitor renders authoritative usage, in-flight Research reservations, and Research-only pause state separately.

**Tech Stack:** Go 1.26, `net/http`, `sync.Mutex`, table-driven `testing`, `httptest`, and the existing TvLink Pool/Proxy/Monitor packages.

---

## File Map

- `internal/pool/pool.go`: workload-aware selection, identified reservations, refresh reconciliation, and Research quota pauses.
- `internal/pool/coordinator.go`: pass selection constraints through grouped rebuilds.
- `internal/pool/pool_test.go`, `coordinator_test.go`: Pool state-machine and concurrency coverage.
- `internal/proxy/research.go`: conservative cost calculation, distinct-Key quota failover, and task settlement.
- `internal/proxy/handler.go`: REST Research admission and lease-backed mappings.
- `internal/proxy/research_test.go`, `handler_test.go`: MCP/REST failover and lifecycle coverage.
- `cmd/tvlink/main.go`, `main_test.go`: inject per-Key usage reconciliation.
- `internal/monitor/view.go`, `template.go`, `view_test.go`: endpoint-specific routing status.

## Task 1: Make Pool Reservations Persistent And Workload-Aware

**Files:**
- Modify: `internal/pool/pool.go:47-66,98-124,312-422,518-616`
- Modify: `internal/pool/coordinator.go:10-43`
- Test: `internal/pool/pool_test.go`
- Test: `internal/pool/coordinator_test.go`

- [ ] **Step 1: Write failing admission and refresh tests**

Add these contracts to `pool_test.go` before production changes:

```go
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
```

- [ ] **Step 2: Write failing Research-only quota tests**

```go
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
	active, _ := p.SelectFor(now, Selection{Estimate: 110, Workload: WorkloadResearch})
	rejected, _ := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
	p.Resolve(rejected, 432, 0, now)
	p.SettleResearch(active)
	p.UpdateUsage("one", Usage{Limit: 1000, Used: 550}, now.Add(time.Minute))

	if _, err := p.SelectFor(now.Add(time.Minute), Selection{Estimate: 110, Workload: WorkloadResearch}); err != nil {
		t.Fatalf("SelectFor() after increased headroom = %v", err)
	}
}
```

- [ ] **Step 3: Write a concurrent admission test**

Use eight goroutines behind a start channel. With 500 remaining credits and a 250-credit Research reservation, assert exactly two calls succeed and the rest return `ErrNoEligibleKey`:

```go
start := make(chan struct{})
results := make(chan error, 8)
var wg sync.WaitGroup
for range 8 {
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, err := p.SelectFor(now, Selection{Estimate: 250, Workload: WorkloadResearch})
		results <- err
	}()
}
close(start)
wg.Wait()
close(results)
```

Also extend the grouped tests: a 432 must roll back the rejected reservation from the group round budget, while successful Research settlement must leave the group's monotonic round budget charged.

- [ ] **Step 4: Run focused tests and verify red state**

```powershell
$env:GOCACHE = (Resolve-Path '.cache/go-build').Path
go test ./internal/pool -run 'Test(SelectFor|UsageRefresh|ResearchQuota|ResearchPause|ConcurrentResearch)' -count=1
```

Expected: compilation fails because the workload selection API and snapshot fields do not exist.

- [ ] **Step 5: Add the Pool contracts**

Add these types next to `Lease` and `Snapshot`:

```go
type Workload uint8

const (
	WorkloadDefault Workload = iota
	WorkloadResearch
)

type Selection struct {
	Estimate float64
	Workload Workload
	Excluded map[string]struct{}
}

type Lease struct {
	ID       uint64
	Key      Key
	Estimate float64
	Workload Workload
	group    int
}

type reservation struct {
	lease      Lease
	persistent bool
	settled   bool
}
```

Extend `Snapshot` with `ResearchReserved float64` and `ResearchBlocked bool`. Extend `keyState` with `researchBlockedAt *float64`. Extend `Pool` with `reservations map[uint64]*reservation` and `nextLeaseID uint64`, initialized in `New`.

- [ ] **Step 6: Implement workload-aware selection inside the Pool mutex**

Keep existing callers source-compatible:

```go
func (p *Pool) Select(now time.Time, estimate float64) (Lease, error) {
	return p.SelectFor(now, Selection{Estimate: estimate})
}

func (p *Pool) SelectFor(now time.Time, selection Selection) (Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selectFor(now, selection)
}
```

Thread `Selection` through `candidatesFor` and `groupCandidates`. Exclude Keys named in `selection.Excluded`, require `state.remaining() >= selection.Estimate`, and exclude Research-paused Keys only for `WorkloadResearch`. Allocate the lease ID, store its reservation, and update Key/group estimates while still holding the lock. Mark Research reservations persistent immediately, before any HTTP work can race with refresh.

- [ ] **Step 7: Implement idempotent settlement and refresh reconciliation**

```go
func (p *Pool) SettleResearch(lease Lease) {
	p.mu.Lock()
	defer p.mu.Unlock()
	reservation, ok := p.reservations[lease.ID]
	if !ok || reservation.lease.Workload != WorkloadResearch {
		return
	}
	reservation.settled = true
}

func (p *Pool) removeReservation(id uint64, rollbackGroup bool) {
	reservation, ok := p.reservations[id]
	if !ok {
		return
	}
	state := p.keys[reservation.lease.Key.Name]
	state.estimated = max(0, state.estimated-reservation.lease.Estimate)
	if rollbackGroup {
		p.rollbackGroupEstimate(reservation.lease)
	}
	delete(p.reservations, id)
}
```

`UpdateUsage` replaces authoritative fields, removes transient and settled reservations for that Key without reducing group round usage, retains active persistent reservations, and derives global exhaustion only from `Used >= Limit`.

- [ ] **Step 8: Correct 432/433 Resolve semantics**

```go
case 432, 433:
	p.removeReservation(lease.ID, true)
	if lease.Workload == WorkloadResearch {
		remaining := state.remaining()
		state.researchBlockedAt = &remaining
	}
```

Do not assign `realUsage = limit` and do not set global `StateExhausted`. Research eligibility clears the pause only after projected remaining credits rise above the captured value. Normal workloads ignore the pause.

- [ ] **Step 9: Pass Selection through Coordinator rebuilds**

Keep `Coordinator.Select` as a wrapper and add `SelectFor` containing the existing serialized refresh/rebuild flow. Every select before and after rebuild must receive the same estimate, workload, and exclusion set:

```go
func (c *Coordinator) Select(ctx context.Context, now time.Time, estimate float64) (Lease, error) {
	return c.SelectFor(ctx, now, Selection{Estimate: estimate})
}
```

- [ ] **Step 10: Verify and commit Task 1**

```powershell
$env:GOCACHE = (Resolve-Path '.cache/go-build').Path
go test ./internal/pool -count=1
go vet ./internal/pool
git add internal/pool/pool.go internal/pool/pool_test.go internal/pool/coordinator.go internal/pool/coordinator_test.go
git commit -m "fix(pool): 持久化 Research 在途额度预留"
```

Attempt `go test -race ./internal/pool -count=1 -cpu 1,8 -timeout 60s`. If Windows exits with the previously observed `0xc0000139`, record the environment limitation and retain the passing standard test result.

## Task 2: Share Research Admission And Fail Over Quota Errors

**Files:**
- Modify: `internal/proxy/handler.go:16-53,55-196,207-225`
- Modify: `internal/proxy/research.go:14-149`
- Modify: `internal/proxy/handler_test.go`
- Modify: `internal/proxy/research_test.go`
- Modify: `cmd/tvlink/main.go:46-79`
- Test: `cmd/tvlink/main_test.go`

- [ ] **Step 1: Write failing maximum-cost tests**

```go
func TestResearchReservationUsesDocumentedMaximum(t *testing.T) {
	tests := []struct {
		name string
		body string
		want float64
	}{
		{name: "mini", body: `{"model":"mini"}`, want: 110},
		{name: "pro", body: `{"model":"pro"}`, want: 250},
		{name: "auto", body: `{"model":"auto"}`, want: 250},
		{name: "omitted", body: `{}`, want: 250},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := estimate("/research", []byte(test.body)); got != test.want {
				t.Fatalf("estimate() = %v, want %v", got, test.want)
			}
		})
	}
}
```

- [ ] **Step 2: Write failing REST and MCP quota-failover tests**

Configure two ready Keys. Make the upstream return 432 for the first Authorization and 201 for the second. Assert two distinct Keys are attempted once, the result succeeds, the first Key's authoritative usage/state remain unchanged, and the accepted Key retains a Research reservation. Add equivalent coverage for direct REST and `RunResearch`.

Use this upstream behavior:

```go
if len(authorizations) == 1 {
	w.WriteHeader(432)
	_, _ = w.Write([]byte(`{"detail":{"error":"plan limit"}}`))
	return
}
w.WriteHeader(http.StatusCreated)
_, _ = w.Write([]byte(`{"request_id":"research-1","status":"completed","content":"ok"}`))
```

- [ ] **Step 3: Write failing all-Keys-reject and lifecycle tests**

Return 432/433 from every Key and assert attempts equal the Key count, Authorizations are unique, and the last Tavily status/body reaches REST and MCP instead of 503. Inject a fake `RefreshUsage(context.Context, string) error` and test:

- terminal success updates authoritative usage and clears the settled reservation;
- refresh failure keeps the conservative reservation;
- cancellation keeps an active reservation;
- TTL expiry marks the reservation settled, and the next `UpdateUsage` removes it;
- repeated terminal/expiry calls are idempotent.

- [ ] **Step 4: Run focused Proxy tests and verify red state**

```powershell
$env:GOCACHE = (Resolve-Path '.cache/go-build').Path
go test ./internal/proxy -run 'Test(ResearchReservation|RunResearchRetries|ResearchAllKeys|ResearchTerminal|ResearchCancellation|ResearchStatus)' -count=1
```

Expected: failures show old estimates, one quota attempt, synthetic exhaustion, and missing lease lifecycle/refresher wiring.

- [ ] **Step 5: Add the per-Key refresher and lease-backed mapping**

```go
type keyUsageRefresher interface {
	RefreshUsage(context.Context, string) error
}

type researchMapping struct {
	keyName   string
	lease     pool.Lease
	expiresAt time.Time
}
```

Add the refresher to `Handler`, pass `nil` from the convenience `New`, extend `NewWithCoordinator`, and inject `usageClient` from `cmd/tvlink/main.go`.

- [ ] **Step 6: Replace Research estimates with conservative limits**

```go
if path == "/research" {
	var request struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &request) == nil && request.Model == "mini" {
		return 110
	}
	return 250
}
```

- [ ] **Step 7: Implement the shared admission helper**

Add `admitResearch(ctx, payload, headers)`. It creates a `pool.Selection` with `WorkloadResearch`, passes a growing `Excluded` set to `Coordinator.SelectFor`, sends a fresh POST body per attempt, and retries only 432/433. Each rejected response is fully read and closed before continuing. A typed error retains cloned headers, status, and body from the last quota response.

Define the helper types and error preservation explicitly:

```go
type researchAdmission struct {
	response *http.Response
	lease    pool.Lease
}

type upstreamResponseError struct {
	status int
	header http.Header
	body   []byte
}

func (e *upstreamResponseError) Error() string {
	return fmt.Sprintf("research request returned %d: %s", e.status, strings.TrimSpace(string(e.body)))
}

func readUpstreamResponseError(response *http.Response) *upstreamResponseError {
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return &upstreamResponseError{
		status: response.StatusCode,
		header: response.Header.Clone(),
		body:   body,
	}
}

func quotaOrSelectionError(quota *upstreamResponseError, selection error) error {
	if quota != nil {
		return quota
	}
	return selection
}
```

Core loop:

```go
selection := pool.Selection{
	Estimate: estimate("/research", payload),
	Workload: pool.WorkloadResearch,
	Excluded: make(map[string]struct{}),
}
for {
	lease, err := h.selector.SelectFor(ctx, time.Now(), selection)
	if err != nil {
		return researchAdmission{}, quotaOrSelectionError(lastQuota, err)
	}
	selection.Excluded[lease.Key.Name] = struct{}{}
	response, err := h.researchRequest(ctx, http.MethodPost, "/research", lease.Key.APIKey, payload, headers)
	if err != nil {
		h.deferResearchSettlement(lease)
		return researchAdmission{}, err
	}
	if response.StatusCode != 432 && response.StatusCode != 433 {
		h.pool.Resolve(lease, response.StatusCode, retryAfter(response.Header.Get("Retry-After")), time.Now())
		return researchAdmission{response: response, lease: lease}, nil
	}
	lastQuota = readUpstreamResponseError(response)
	h.pool.Resolve(lease, response.StatusCode, 0, time.Now())
}
```

Structured logs include Key name, status, model/reservation, and attempt count, never API keys or prompts.

- [ ] **Step 8: Route MCP and REST through admission**

`RunResearch` keeps forcing `stream=false`, then uses `admitResearch`, validates `request_id`, and stores the accepted lease. Direct REST `/research` branches before the generic loop and uses the same helper while preserving the caller's stream setting and upstream response.

For non-streaming success, store request ID, Key, and lease. For a streaming response, settle and reconcile after clean EOF; ambiguous transport/read failures retain the reservation until the existing TTL safety bound. Non-Research endpoints retain the existing single 429 retry unchanged.

- [ ] **Step 9: Reconcile terminal and expired mappings**

```go
func (h *Handler) settleResearch(ctx context.Context, lease pool.Lease) {
	h.pool.SettleResearch(lease)
	if h.usage == nil {
		return
	}
	if err := h.usage.RefreshUsage(ctx, lease.Key.Name); err != nil {
		slog.Warn("research usage reconciliation failed", "key", lease.Key.Name, "error", err)
	}
}
```

Call it on `completed` and `failed`. Do not call it on client cancellation. Mapping expiry calls `SettleResearch` idempotently; the next successful periodic usage refresh removes the abandoned reservation without changing group round usage.

Requests that may have reached Tavily but never yielded a usable request ID receive the same TTL safety bound without a mapping:

```go
func (h *Handler) deferResearchSettlement(lease pool.Lease) {
	time.AfterFunc(h.researchTTL, func() {
		h.pool.SettleResearch(lease)
	})
}
```

- [ ] **Step 10: Verify and commit Task 2**

```powershell
$env:GOCACHE = (Resolve-Path '.cache/go-build').Path
go test ./internal/proxy ./internal/mcp ./cmd/tvlink -count=1
go vet ./internal/proxy ./internal/mcp ./cmd/tvlink
go build ./cmd/tvlink
git add internal/proxy/handler.go internal/proxy/handler_test.go internal/proxy/research.go internal/proxy/research_test.go cmd/tvlink/main.go cmd/tvlink/main_test.go
git commit -m "fix(proxy): 为 Research 额度错误切换 Key"
```

## Task 3: Expose Research Routing State In The Monitor

**Files:**
- Modify: `internal/monitor/view.go:24-36,75-117`
- Modify: `internal/monitor/template.go`
- Test: `internal/monitor/view_test.go`

- [ ] **Step 1: Write failing view and HTML tests**

```go
func TestPageViewSeparatesResearchPauseFromGlobalReadiness(t *testing.T) {
	view := newPageView(pool.MonitorSnapshot{Keys: []pool.Snapshot{{
		Name: "one", Limit: 1000, RealUsage: 603, EstimatedUsage: 250,
		ResearchReserved: 250, Remaining: 147, State: pool.StateReady,
		ResearchBlocked: true,
	}}}, time.Now())

	if view.AvailableKeys != 1 {
		t.Fatalf("AvailableKeys = %d, want 1", view.AvailableKeys)
	}
	row := view.Rows[0]
	if row.Metrics.UsageText != "603 (+250) / 1,000" || !row.ResearchBlocked || row.ResearchReserved != "250" {
		t.Fatalf("row = %#v", row)
	}
}
```

Render the page and assert it contains `RESEARCH PAUSED`, `Research 预留 250`, and `READY`, while real usage remains 603 rather than a synthetic 1,000.

- [ ] **Step 2: Run focused tests and verify red state**

```powershell
$env:GOCACHE = (Resolve-Path '.cache/go-build').Path
go test ./internal/monitor -run 'Test(PageViewSeparatesResearch|RenderedResearch)' -count=1
```

- [ ] **Step 3: Extend the view model without changing READY semantics**

```go
type keyView struct {
	// Existing fields remain unchanged.
	ResearchReserved string
	ResearchBlocked  bool
}
```

Populate these fields from the Pool snapshot. Keep page-wide available-Key counts based only on `StateReady`.

- [ ] **Step 4: Render secondary Research state in the existing row**

Add a compact `RESEARCH PAUSED` badge beside existing state/group metadata and show `Research 预留 <value>` only when nonzero. Reuse existing badge dimensions and colors, do not add a card, and allow metadata to wrap on narrow layouts.

- [ ] **Step 5: Verify and commit Task 3**

```powershell
$env:GOCACHE = (Resolve-Path '.cache/go-build').Path
go test ./internal/monitor -count=1
go test ./... -count=1
go vet ./...
go build ./cmd/tvlink
git add internal/monitor/view.go internal/monitor/view_test.go internal/monitor/template.go
git commit -m "fix(monitor): 展示 Research 路由状态"
```

## Task 4: Final Verification And Commit Audit

**Files:**
- Verify only; no source changes expected.

- [ ] **Step 1: Format modified Go files**

```powershell
gofmt -w internal/pool/pool.go internal/pool/pool_test.go internal/pool/coordinator.go internal/pool/coordinator_test.go internal/proxy/handler.go internal/proxy/handler_test.go internal/proxy/research.go internal/proxy/research_test.go internal/monitor/view.go internal/monitor/view_test.go cmd/tvlink/main.go cmd/tvlink/main_test.go
```

- [ ] **Step 2: Run complete verification**

```powershell
$env:GOCACHE = (Resolve-Path '.cache/go-build').Path
go test ./... -count=1
go vet ./...
go build ./cmd/tvlink
go test -race ./internal/pool ./internal/proxy -count=1 -cpu 1,8 -timeout 60s
```

Standard tests, vet, and build must pass. Record `0xc0000139` separately if the local Windows race runtime remains unavailable.

- [ ] **Step 3: Audit diffs and atomic history**

```powershell
git diff --check
git status --short
git log --oneline -5
```

Expected target commits in relative order (unrelated user commits may appear between them):

```text
fix(monitor): 展示 Research 路由状态
fix(proxy): 为 Research 额度错误切换 Key
fix(pool): 持久化 Research 在途额度预留
docs: 添加 Research 额度修复实施计划
docs: 记录 Research 在途额度修复设计
```

Only pre-existing unrelated files such as `previews/` may remain outside these commits.
