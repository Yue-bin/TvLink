# Group Rotation Priority Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure every rebuilt group rotation begins with the group holding the most remaining capacity.

**Architecture:** Keep the capacity-partition solver unchanged. Normalize its returned groups into a deterministic rotation order before `RebuildGroups` assigns activity index zero. Compare total remaining capacity descending; resolve equal totals by the sorted member-key names.

**Tech Stack:** Go standard library, existing `internal/pool` unit tests.

---

### Task 1: Order Rebuilt Groups for Rotation

**Files:**
- Modify: `internal/pool/pool_test.go`
- Modify: `internal/pool/pool.go`

- [x] **Step 1: Write the failing test**

Add a test using the existing ten-Key, size-three capacity distribution. Assert that each `p.groups[index].remaining` is no less than the following group's remaining capacity and that the first selected lease belongs to `p.groups[0]`.

- [x] **Step 2: Run the focused test to verify it fails**

Run: `go test ./internal/pool -run TestRebuildGroupsOrdersRotationByRemainingCapacity -count=1`

Expected: FAIL because the current partition output is not normalized into descending rotation order.

- [x] **Step 3: Write the minimal implementation**

Add a helper in `internal/pool/pool.go` that stable-sorts `[]groupState` by `remaining` descending and uses the sorted member-key names as an equal-capacity tie-breaker. Invoke it immediately after `optimalGroups` returns in `RebuildGroups`.

- [x] **Step 4: Run focused and package tests**

Run: `go test ./internal/pool -count=1`

Expected: PASS.

- [x] **Step 5: Run all Go tests**

Run: `go test ./...`

Expected: PASS.

- [x] **Step 6: Commit**

Run: `git add internal/pool/pool.go internal/pool/pool_test.go && git commit -m "fix(pool): 优先轮换高余量分组"`
