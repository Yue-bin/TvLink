# TvLink Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single-instance Go Tavily key load balancer with REST, MCP, quota monitoring, research affinity, and streaming proxy support.

**Architecture:** A pool owns synchronized per-key usage snapshots, reservations, and circuit state. An HTTP server authenticates callers, picks a key, proxies Tavily requests, and renders a public snapshot. Background refreshes keep pool usage authoritative while local reservations bridge refresh intervals.

**Tech Stack:** Go standard library, `github.com/BurntSushi/toml`, `net/http`, `html/template`, `log/slog`, `httptest`.

---

## File Structure

- `cmd/tvlink/main.go`: process setup, configuration, startup refresh, graceful shutdown.
- `internal/config/config.go`: TOML decode and validation.
- `internal/pool/pool.go`: synchronized key state, weighted selection, reservations, circuit state.
- `internal/tavily/client.go`: usage refresh and upstream request construction.
- `internal/proxy/handler.go`: REST auth, bounded request replay, fallback proxy, Research affinity, SSE copy.
- `internal/mcp/handler.go`: Streamable HTTP JSON-RPC tools backed by the proxy service.
- `internal/monitor/handler.go`: public HTML snapshot rendering.
- `internal/*/*_test.go`: focused table-driven unit and handler tests.
- `tvlink.example.toml`, `.gitignore`, `README.md`: operations documentation.

### Task 1: Bootstrap configuration and executable

**Files:** `go.mod`, `.gitignore`, `tvlink.example.toml`, `internal/config/config.go`, `internal/config/config_test.go`, `cmd/tvlink/main.go`

- [ ] Write configuration tests for unknown fields, missing client key, duplicate key labels, and valid TOML.
- [ ] Run `go test ./internal/config` and confirm it fails because configuration code does not exist.
- [ ] Implement strict TOML loading, semantic validation, command flag parsing, and structured startup logging.
- [ ] Run `go test ./internal/config` and confirm it passes.
- [ ] Commit bootstrap files with `feat: 初始化 TvLink 配置与启动入口`.

### Task 2: Implement quota pool with test-first state transitions

**Files:** `internal/pool/pool.go`, `internal/pool/pool_test.go`

- [ ] Write tests that assert zero weight before refresh, weighted selection favors remaining credits, 432/433 zero a key, 429 respects Retry-After, and a half-open probe is exclusive.
- [ ] Run `go test ./internal/pool` and confirm it fails because the pool package does not exist.
- [ ] Implement locked per-key snapshots, reservation generations, endpoint estimates, weighted random selection, and circuit transitions.
- [ ] Run `go test ./internal/pool` and confirm it passes.
- [ ] Commit with `feat: 实现配额权重池与熔断状态`.

### Task 3: Add Tavily client and periodic usage synchronization

**Files:** `internal/tavily/client.go`, `internal/tavily/client_test.go`

- [ ] Write tests using `httptest.Server` for usage decoding, Retry-After parsing, and refresh reconciliation.
- [ ] Run `go test ./internal/tavily` and confirm it fails because the client package does not exist.
- [ ] Implement context-aware usage requests, endpoint cost updates, startup refresh, and cancellable periodic scheduling.
- [ ] Run `go test ./internal/tavily` and confirm it passes.
- [ ] Commit with `feat: 同步 Tavily 用量并校正估算`.

### Task 4: Add authenticated REST proxy and Research affinity

**Files:** `internal/proxy/handler.go`, `internal/proxy/handler_test.go`

- [ ] Write handler tests for client-key rejection, upstream authorization replacement, one fallback after 429, 432/433 zeroing, Research ID affinity, and byte-preserving SSE relay.
- [ ] Run `go test ./internal/proxy` and confirm it fails because the proxy package does not exist.
- [ ] Implement bounded request buffering, header allowlisting, response forwarding, fallback selection, and expiring Research map.
- [ ] Run `go test ./internal/proxy` and confirm it passes.
- [ ] Commit with `feat: 添加 Tavily REST 代理与 Research 粘性`.

### Task 5: Add MCP endpoint and public monitor

**Files:** `internal/mcp/handler.go`, `internal/mcp/handler_test.go`, `internal/monitor/handler.go`, `internal/monitor/handler_test.go`

- [ ] Write tests for MCP initialization, tools/list, authenticated tools/call delegation, monitor public access, and separate actual usage timestamp and estimated usage fields.
- [ ] Run the package tests and confirm they fail because handlers do not exist.
- [ ] Implement minimal Streamable HTTP JSON-RPC handling and server-rendered no-store monitor HTML with a five-second refresh.
- [ ] Run package tests and confirm they pass.
- [ ] Commit with `feat: 提供 MCP 接口与用量监控页`.

### Task 6: Document and verify the complete service

**Files:** `README.md`, all production and test files as needed.

- [ ] Write end-to-end handler tests for startup unavailable state and all-keys-exhausted behavior.
- [ ] Run `go test ./...`, `go test -race ./...`, `go vet ./...`, and installed formatting/lint commands.
- [ ] Fix any failures with a failing regression test before each production change.
- [ ] Document configuration, routes, auth, monitor semantics, restart behavior, and commands.
- [ ] Commit with `docs: 补充 TvLink 使用与运行说明`.
