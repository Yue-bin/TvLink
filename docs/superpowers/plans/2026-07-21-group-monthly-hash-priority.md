# Group Monthly Hash Priority Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make equal-capacity group rotation vary deterministically by calendar month without persisted state.

**Architecture:** Preserve descending remaining-capacity priority. For equal totals, compare stable SHA-256 ranks derived from the local rotation month and sorted group member names, falling back to member-name ordering only on equal hashes. Reset the active group to index zero at a month boundary so long-running and restarted processes share the same monthly rule.

**Tech Stack:** Go 1.26 standard library, existing `internal/pool` unit tests.

---

### Task 1: Add Monthly Hash Tie-Breaking

**Files:**
- Modify: `internal/pool/pool_test.go`
- Modify: `internal/pool/pool.go`

- [x] **Step 1: Write the failing regression test**

Create eight equal-capacity, size-one groups. Rebuild separate pools twice in January 2026 and once in February 2026. Assert January orders are equal, February differs from January, and a live pool rebuilt across the month boundary has `activeGroup == 0`.

- [x] **Step 2: Run the focused test to verify it fails**

Run: `go test ./internal/pool -run TestRebuildGroupsUsesMonthlyHashForEqualCapacity -count=1`

Expected: FAIL because equal-capacity groups currently use the same member-name order every month and an existing pool advances its active index on a rebuild.

- [x] **Step 3: Implement the minimal ordering rule**

Pass the local rotation month to `orderGroupsForRotation`. For equal `remaining` values, compare SHA-256 hashes of the month and sorted member names, then retain member-name comparison as a collision fallback. In `RebuildGroups`, reset `activeGroup` to zero when the local month changes.

- [x] **Step 4: Run package tests**

Run: `go test ./internal/pool -count=1`

Expected: PASS.

- [x] **Step 5: Run all Go tests**

Run: `go test ./...`

Expected: PASS.

- [x] **Step 6: Commit**

Run: `git add internal/pool/pool.go internal/pool/pool_test.go && git commit -m "fix(pool): 按月打散等额分组顺序"`
