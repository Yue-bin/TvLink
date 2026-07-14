# Interactive Group Monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reorganize the existing dark monitor into a static interactive all-keys/group-filter view without changing its established visual language or usage semantics.

**Architecture:** The pool exposes one atomic monitor snapshot containing key membership and aggregate group metrics. The monitor view model formats global, group, and key progress using the same actual/projected calculation. The server renders all rows once; small inline JavaScript filters existing DOM nodes, with a desktop group rail and mobile select.

**Tech Stack:** Go 1.24, standard-library `html/template`, inline CSS and JavaScript.

---

### Task 1: Expose atomic group monitor state

**Files:**
- Modify: `internal/pool/pool.go`
- Modify: `internal/pool/pool_test.go`

- [ ] Add failing tests for key group membership, aggregate group real/estimated/limit/remaining values, active/spent state, and selection-aware weights.
- [ ] Run `go test ./internal/pool -run Monitor -count=1` and confirm the new assertions fail.
- [ ] Add `MonitorSnapshot`, `GroupSnapshot`, and `Pool.MonitorSnapshot(now)` while retaining `Snapshots(now)` compatibility.
- [ ] Run `go test ./internal/pool -count=1`.
- [ ] Commit with `feat: 暴露分组监控快照`.

### Task 2: Build group-aware monitor view models

**Files:**
- Modify: `internal/monitor/view.go`
- Modify: `internal/monitor/view_test.go`

- [ ] Add failing tests for group aggregation formatting, shared dual-progress semantics, all/group filter metadata, ungrouped mode, pending keys, and clamped widths.
- [ ] Run `go test ./internal/monitor -run View -count=1` and confirm failure.
- [ ] Add group and page view models while reusing `newProgressView` for global, group, and key progress.
- [ ] Run `go test ./internal/monitor -count=1`.
- [ ] Commit with `feat: 构建分组监控视图模型`.

### Task 3: Render the interactive static page

**Files:**
- Modify: `internal/monitor/template.go`
- Modify: `internal/monitor/handler.go`
- Modify: `internal/monitor/handler_test.go`
- Modify: `cmd/tvlink/main.go`

- [ ] Add failing handler assertions for no meta refresh, group filter controls, inline filtering script, ARIA labels, and `no-store`.
- [ ] Run `go test ./internal/monitor -count=1` and confirm failure.
- [ ] Render the approved desktop group rail, mobile select, all Key rows, group progress, and inline static filter script.
- [ ] Remove the monitor refresh interval from handler construction while keeping the old config field parse-compatible.
- [ ] Run `go test ./internal/monitor ./cmd/tvlink -count=1`.
- [ ] Commit with `feat: 重构分组监控交互页面`.

### Task 4: Verify behavior and documentation

**Files:**
- Modify: `README.md`
- Modify: `tvlink.example.toml`

- [ ] Remove the monitor auto-refresh setting from the example and document manual reload plus static group filtering.
- [ ] Run `go test ./...` and build to a temporary output path.
- [ ] Check desktop and mobile layout through the local preview/server without horizontal overflow.
- [ ] Commit with `docs: 更新分组监控说明`.

