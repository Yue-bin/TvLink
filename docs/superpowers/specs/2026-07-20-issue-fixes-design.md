# 2026-07-20 Issue Fixes Design

## Scope

Fix the five issues recorded in `docs/2026-7-20问题整理.md` while preserving
the existing REST routes, grouped key rotation behavior, and MCP protocol
version `2025-03-26`.

The monitor changes correct display semantics only. The Research changes
replace the MCP tool's dependency on sparse Tavily SSE events with Tavily's
asynchronous create-and-poll workflow.

## Current Causes

The group cards currently build their primary progress metric from the sum of
member Key quota usage. That value describes the Keys' monthly quota state,
not the group's configured lifetime budget for the current rotation round.

The page-wide available-Key count currently uses `Snapshot.Weight > 0`.
Grouped selection assigns positive weight only to eligible Keys in the active
group, so the count means "currently selectable" rather than "Ready".

The MCP Research tool currently forces Tavily streaming and waits for upstream
SSE events. Sparse upstream events leave the MCP client without traffic for a
long time. Its final branch also returns text content only, despite the tool
declaring an output schema.

## Monitor Design

Each group card uses `RoundUsage / RoundLimit` as its primary number and
progress scale. This is a single-layer progress bar because round usage is one
local reservation counter, not an authoritative-plus-estimated quota pair.
The width clamps to the existing 0-100 percent visual range while the displayed
values remain unmodified.

Member-Key quota information remains available as secondary group metadata:

- aggregate actual and estimated Key usage;
- aggregate projected remaining quota;
- Ready Key count over total Key count.

The page-wide "可用 Key" value counts snapshots whose state is exactly
`StateReady`. It no longer derives availability from selection weight.

The group metadata label remains "本轮次". This wording is already present on
`main` through commit `a5b1a6a`; a regression assertion will preserve it.

## Research Design

The MCP Research path creates a non-streaming Tavily task with
`POST /research`, explicitly overriding any caller-provided `stream` value to
`false`. It retains the selected Key for the task and polls
`GET /research/{request_id}` with that same Key every five seconds.

The polling abstraction accepts a progress callback. Production uses a
five-second ticker; tests inject a controllable interval so they remain fast
and deterministic. Cancellation of the MCP HTTP request cancels creation or
polling immediately.

An HTTP 202 response or an `in_progress`/`pending` status continues polling.
A completed response ends polling and returns the full decoded JSON object.
A failed status, non-success response, missing request ID, malformed JSON, or
transport error ends the call with a descriptive error.

## MCP Transport And Results

When `tools/call` invokes `tavily_research`, the handler returns a Streamable
HTTP SSE response. Each poll produces one JSON-RPC
`notifications/progress` message when the request supplied a valid string or
integer progress token. The numeric progress value increases on every
notification; no total is sent because Tavily exposes task state but no
reliable completion percentage. The message reports the current upstream
status.

The final SSE event contains the JSON-RPC response for the original request,
then the stream closes. The tool result contains:

- text content derived from the completed Research `content` field;
- `structuredContent` containing the complete completed Tavily response;
- `isError: false`.

If the completed `content` field is an object, the text fallback is its JSON
encoding while `structuredContent` preserves the original structure. Errors
produce the existing JSON-RPC server-error shape and no success result.

## Compatibility

Ordinary MCP tools continue returning one `application/json` response. REST
`POST /research` and `GET /research/{request_id}` keep their current external
behavior. No MCP Tasks capability or newer protocol version is introduced.

## Tests And Verification

Tests will be added before production changes and observed failing for the
intended reason. Coverage includes:

- group round usage text and progress width;
- secondary group quota and Ready-count metadata;
- page-wide Ready-count semantics and the "本轮次" label;
- non-streaming Research creation and same-Key polling;
- repeated five-second polling behavior through an injected test interval;
- increasing MCP progress notifications and final SSE response;
- completed string and structured Research content;
- failed, malformed, non-success, and cancelled Research operations;
- no regressions for ordinary MCP tools and REST Research routes.

Repository verification runs `gofmt`, `go test ./... -count=1`,
`go test -race ./... -count=1`, `go vet ./...`, and `go build ./cmd/tvlink`.

## Commit Boundaries

Implementation remains split into independently reviewable conventional
commits with Chinese subjects:

1. `fix(monitor): 按轮次用量展示组进度`
2. `fix(monitor): 按 Ready 状态统计可用 Key`
3. `fix(mcp): 轮询并报告 Research 进度`
4. `fix(mcp): 返回 Research 结构化结果`

The human-authored issue document remains untouched and untracked unless the
user separately asks to add it.
