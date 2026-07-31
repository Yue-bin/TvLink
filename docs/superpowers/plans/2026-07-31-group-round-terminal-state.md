# Group Round Terminal State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep group scheduling one-way while distinguishing quota-complete groups from groups deferred by 429 cooling.

**Architecture:** Add `closed` and `deferred` state to each in-memory group. Close a group when its round budget cannot fit the current request; defer it only when all otherwise-capable Keys are cooling or probing. Treat both as terminal for selection and rebuild, expose deferred state through pool and monitor view models, and retain current 432/433 rollback behavior.

**Tech Stack:** Go 1.26 standard library, existing pool and monitor unit tests.

---

### Task 1: Add Terminal Group Selection States

**Files:**
- Modify: `internal/pool/pool.go`
- Modify: `internal/pool/pool_test.go`

- [ ] **Step 1: Write failing pool tests**

Create two size-one groups. Resolve a lease from the first Key with 429, then select again. Assert the second group is chosen, the first snapshot group is `Deferred`, and once the second group is spent the next selection returns `ErrGroupRebuildRequired`. Add a Research 432 test that asserts its group is not deferred after rollback.

- [ ] **Step 2: Verify failing tests**

Run: `go test ./internal/pool -run 'Test(GroupDefersAfterAllKeysCool|ResearchQuotaRollbackDoesNotDeferGroup)' -count=1`

Expected: FAIL because groups do not yet retain closed or deferred terminal state.

- [ ] **Step 3: Implement group terminal state**

Add `closed` and `deferred` to `groupState`; add `Deferred` to `GroupSnapshot`. Close groups that cannot fit the selection estimate, defer groups only when their remaining-capacity Keys are cooling or probing, skip terminal groups, and treat both terminal kinds as a rebuild condition.

- [ ] **Step 4: Verify the pool package**

Run: `go test ./internal/pool -count=1`

Expected: PASS.

### Task 2: Show Terminal State in the Monitor

**Files:**
- Modify: `internal/monitor/view.go`
- Modify: `internal/monitor/template.go`
- Modify: `internal/monitor/view_test.go`

- [ ] **Step 1: Write failing view tests**

Build a snapshot containing one `Spent` and active group plus one `Deferred` group. Assert the spent group renders as completed rather than active, and the deferred group renders as “本轮延后”.

- [ ] **Step 2: Verify failing view tests**

Run: `go test ./internal/monitor -run TestNewPageViewShowsGroupTerminalStates -count=1`

Expected: FAIL because the view lacks deferred state and active currently overrides spent text.

- [ ] **Step 3: Implement monitor rendering**

Expose `Deferred` through `groupView`, give terminal states precedence over active state, and add the matching group-list style class.

- [ ] **Step 4: Verify all tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

Run: `git add internal/pool/pool.go internal/pool/pool_test.go internal/monitor/view.go internal/monitor/template.go internal/monitor/view_test.go && git commit -m "fix(pool): 区分分组完成与冷却延后"`
