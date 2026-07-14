# Key Group Rotation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional, balanced Tavily key groups that rotate after a fixed credit budget and rebuild at the configured natural-month boundary.

**Architecture:** `pool.Pool` owns group membership, group credit reservations, and rotation decisions. A request-path coordinator detects a rebuild-required selection, serializes a full usage refresh plus regroup, and retries selection; the pool never performs HTTP I/O while locked. Existing weighted key selection continues within the active group.

**Tech Stack:** Go 1.24, BurntSushi TOML, standard-library `sync`, `time`, and `errors`.

---

### Task 1: Add Grouping Configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `tvlink.example.toml`

- [ ] **Step 1: Write failing configuration tests**

Add table cases that load this valid grouped configuration and assert its values:

```toml
key_group_size = 3
group_usage_limit = 600
group_rotation_timezone = "Asia/Shanghai"
```

Also add cases for a zero group size, zero usage limit, invalid time zone, and partially specified grouping configuration. Each must produce a non-nil `Load` error.

- [ ] **Step 2: Run the configuration tests to verify failure**

Run: `go test ./internal/config`

Expected: FAIL because the new TOML fields are currently undecoded.

- [ ] **Step 3: Implement typed validation**

Add these `Config` fields and validate them as one all-or-nothing feature set:

```go
KeyGroupSize          int     `toml:"key_group_size"`
GroupUsageLimit       float64 `toml:"group_usage_limit"`
GroupRotationTimezone string  `toml:"group_rotation_timezone"`
```

Add `GroupingEnabled() bool`, returning true only when any group setting is present. When enabled, require `KeyGroupSize > 0`, `GroupUsageLimit > 0`, a non-empty time zone, and successful `time.LoadLocation`. When disabled, reject a lone time-zone setting and retain global allocation.

- [ ] **Step 4: Run the configuration tests**

Run: `go test ./internal/config`

Expected: PASS.

- [ ] **Step 5: Document an opt-in example and commit**

Add the three fields below the top-level settings in `tvlink.example.toml`, with a comment that the limit is estimated Tavily credits.

```bash
git add internal/config/config.go internal/config/config_test.go tvlink.example.toml
git commit -m "feat: 增加 Key 分组配置"
```

### Task 2: Model And Build Balanced Groups In The Pool

**Files:**
- Modify: `internal/pool/pool.go`
- Modify: `internal/pool/pool_test.go`

- [ ] **Step 1: Write failing grouping tests**

Create deterministic tests with refreshed keys whose remaining credits are `100, 90, 80, 70, 60, 50, 40, 30, 20, 10`. Configure size `3`, rebuild groups, and assert four groups with member counts `3, 3, 2, 2`, no duplicate members, and balanced group totals. Add a test that a cooling key remains assigned, while an exhausted key is excluded from a new rebuild.

- [ ] **Step 2: Run the pool tests to verify failure**

Run: `go test ./internal/pool`

Expected: FAIL because pool group configuration and group snapshots do not exist.

- [ ] **Step 3: Add group state and rebuild logic**

Introduce this API:

```go
type GroupConfig struct {
    Size       int
    UsageLimit float64
    Location   *time.Location
}

func (p *Pool) ConfigureGroups(config GroupConfig) error
func (p *Pool) RebuildGroups(now time.Time) error
```

Keep groups private to `Pool`. A zero-value `GroupConfig` disables grouping. `RebuildGroups` collects known, positive-remaining, non-exhausted keys; calculates `ceil(n / Size)` target groups whose sizes differ by at most one; sorts keys by remaining credits descending; and assigns each to the non-full group with the lowest accumulated remaining credits. Initialize each group's reserved usage to zero and select the successor of the prior active index.

- [ ] **Step 4: Run the tests and commit**

Run: `go test ./internal/pool`

Expected: PASS.

```bash
git add internal/pool/pool.go internal/pool/pool_test.go
git commit -m "feat: 构建均衡 Key 分组"
```

### Task 3: Restrict Selection To The Active Group

**Files:**
- Modify: `internal/pool/pool.go`
- Modify: `internal/pool/pool_test.go`

- [ ] **Step 1: Write failing allocation tests**

Add tests that prove only active-group members are selected; a reservation increments matching group usage; a 429 removes that usage; a request that crosses the limit rotates first; an oversized first request is allowed and spends its group; and exhausting one member does not move other members.

- [ ] **Step 2: Run focused allocation tests to verify failure**

Run: `go test ./internal/pool -run TestGroup`

Expected: FAIL because `Select` still considers every eligible key.

- [ ] **Step 3: Implement group-aware leases and selection**

Extend `Lease` with an unexported group index. In grouped mode, make `Select` build candidates only from the active group while retaining the existing weighting calculation. Before reserve, advance across groups that are spent, have no selectable key, or would exceed `UsageLimit`; permit the oversized-request exception only when a group's reserved usage is zero. Increment the selected group's reserved usage with the key estimate.

Make `Resolve` apply every existing estimate rollback to both the selected key and the lease's group. Do not clear group usage in `UpdateUsage`.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/pool`

Expected: PASS.

```bash
git add internal/pool/pool.go internal/pool/pool_test.go
git commit -m "feat: 按分组额度轮换 Key"
```

### Task 4: Signal And Serialize Rebuilds

**Files:**
- Modify: `internal/pool/pool.go`
- Create: `internal/pool/coordinator.go`
- Modify: `internal/pool/pool_test.go`
- Create: `internal/pool/coordinator_test.go`

- [ ] **Step 1: Write failing rebuild tests**

Add tests that report a rebuild-required error when every group is spent or when `now.In(Location)` belongs to a different year-month. Add coordinator tests using a counting refresh callback to prove concurrent rebuild-required selections perform one refresh and every waiter retries. Add a failed-refresh test asserting that no partial regroup occurs.

- [ ] **Step 2: Run rebuild tests to verify failure**

Run: `go test ./internal/pool -run 'Test.*Rebuild|TestCoordinator'`

Expected: FAIL because no rebuild outcome or coordinator exists.

- [ ] **Step 3: Add the rebuild outcome and coordinator**

Export `ErrGroupRebuildRequired`. `Select` returns it only when grouping is enabled and the local year-month changed or all groups are spent; it continues returning `ErrNoEligibleKey` for cooling, pending, or exhausted groups.

Create this coordinator API:

```go
type UsageRefresher func(context.Context) error

type Coordinator struct {
    pool      *Pool
    refresh   UsageRefresher
    rebuildMu sync.Mutex
}

func NewCoordinator(keyPool *Pool, refresh UsageRefresher) *Coordinator
func (c *Coordinator) Select(ctx context.Context, now time.Time, estimate float64) (Lease, error)
```

`Coordinator.Select` first calls `Pool.Select`. For `ErrGroupRebuildRequired`, it acquires the rebuild mutex, retries selection to observe another caller's completed rebuild, then calls `refresh(ctx)`, `Pool.RebuildGroups(now)`, and retries selection. If refresh or rebuild fails, keep previous groups and return `ErrNoEligibleKey`.

- [ ] **Step 4: Run race tests and commit**

Run: `go test -race ./internal/pool`

Expected: PASS with one callback invocation in the concurrent test.

```bash
git add internal/pool/pool.go internal/pool/pool_test.go internal/pool/coordinator.go internal/pool/coordinator_test.go
git commit -m "feat: 协调分组重建"
```

### Task 5: Refresh All Usage And Wire Request Paths

**Files:**
- Modify: `internal/tavily/client.go`
- Modify: `internal/tavily/client_test.go`
- Modify: `internal/proxy/handler.go`
- Modify: `internal/proxy/handler_test.go`
- Modify: `cmd/tvlink/main.go`
- Modify: `cmd/tvlink/main_test.go`

- [ ] **Step 1: Write failing client and proxy tests**

Add `Client.RefreshAll` tests with two successful usage responses and one failure; assert every configured key is attempted and any failure is returned. In proxy tests, inject a coordinator, force a rebuild-required selection, and assert the request retries after rebuilding. Keep research-status lookup tests using `Pool.Key`.

- [ ] **Step 2: Run affected tests to verify failure**

Run: `go test ./internal/tavily ./internal/proxy ./cmd/tvlink`

Expected: FAIL because the bulk refresher and coordinator injection do not exist.

- [ ] **Step 3: Implement request-path integration**

Add `RefreshAll(ctx context.Context) error` to `tavily.Client`; it iterates every configured key, keeps `RefreshUsage` behavior, and returns `errors.Join` after attempting all keys.

In `main`, run startup refresh, configure and rebuild groups when enabled, then create a `pool.Coordinator` using `usageClient.RefreshAll`. Change proxy construction to receive the pool for research request-ID lookups and the coordinator for costed selections. Replace direct `pool.Select(time.Now(), estimate)` calls in normal and streaming research paths with `coordinator.Select(ctx, time.Now(), estimate)`. Keep periodic refresh as a snapshot updater only.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/tavily ./internal/proxy ./cmd/tvlink`

Expected: PASS.

```bash
git add internal/tavily/client.go internal/tavily/client_test.go internal/proxy/handler.go internal/proxy/handler_test.go cmd/tvlink/main.go cmd/tvlink/main_test.go
git commit -m "feat: 接入分组轮换请求路径"
```

### Task 6: Document And Verify The Feature

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document grouping semantics**

Add a concise README configuration section explaining group size, fixed credit budget, IANA time zone, startup/month/full-round rebuilding, no immediate regroup on one exhausted key, and that grouping does not change the egress IP.

- [ ] **Step 2: Run complete verification**

Run: `go test ./...`

Expected: PASS.

Run: `go build -o "$env:TEMP\\tvlink-verify.exe" ./cmd/tvlink`

Expected: successful build with no output.

Run: `git diff --check HEAD~6..HEAD`

Expected: no whitespace errors.

- [ ] **Step 3: Commit documentation**

```bash
git add README.md
git commit -m "docs: 说明 Key 分组轮换配置"
```
