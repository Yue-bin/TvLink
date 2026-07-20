# Research Quota Routing Design

## Context

TvLink currently treats Tavily `432` and `433` responses as proof that a Key
has no credits left. `Pool.Resolve` replaces the authoritative usage value with
the Key limit and marks the whole Key exhausted. The next `/usage` refresh can
then replace that synthetic value with a lower authoritative value and make the
Key ready again.

That model does not match Tavily Research. Tavily documents `432` as the current
request exceeding the plan usage limit and `433` as the current request
exceeding the pay-as-you-go limit. Neither response proves that all remaining
credits are zero or that low-cost endpoints cannot use the Key.

Research also has dynamic costs. The documented per-request ranges are 4-110
credits for `mini` and 15-250 credits for `pro`; `auto` may choose either model.
TvLink currently reserves only 10, 40, and 30 credits respectively. Research
tasks execute asynchronously, while each periodic `/usage` refresh clears all
pending estimates. Multiple in-flight tasks can therefore be admitted against
the same apparent balance even though Tavily still accounts for their future
cost.

## Goals

- Prevent concurrent Research tasks from oversubscribing a Key's known credit
  headroom.
- Retry rejected Research creation against other eligible Keys, attempting each
  Key at most once.
- Preserve Tavily's authoritative usage values instead of synthesizing full
  usage from an error code.
- Limit quota rejection effects to Research routing; other endpoints may still
  use a Key with positive authoritative headroom.
- Apply identical Research creation behavior to REST and MCP callers.
- Keep existing Key grouping, weighted selection, cooldown, and Research Key
  affinity behavior.
- Make in-flight Research reservations and Research-specific routing pauses
  visible on the monitor.

## Non-Goals

- Predict the final exact cost of a Research task.
- Increase Tavily plan or pay-as-you-go limits.
- Persist task mappings or reservations across a TvLink process restart.
- Change Tavily's Research polling protocol or the MCP protocol version.
- Add automatic retries for unrelated upstream errors.

## Considered Approaches

### Pool-owned persistent reservations

The Pool owns both transient request estimates and persistent Research
reservations. A Research reservation has an identity and remains part of the
Key's projected usage until the task reaches a terminal state and a successful
usage refresh accounts for its result. This keeps selection, grouping, and
monitoring on one synchronized source of truth.

This is the selected approach.

### Proxy-owned Research ledger

The proxy could subtract active Research costs before asking the Pool to select
a Key. This requires the proxy and Pool to maintain separate views of remaining
capacity and group usage. Concurrent REST and MCP requests could observe those
views at different times, recreating the current bug in another layer.

### Larger estimates with stateless retries

TvLink could use 110/250 estimates and retry 432/433 without tracking task
lifetime. A periodic usage refresh would still erase reservations for active
tasks, so this only reduces the probability of oversubscription.

## Reservation Model

Selection accepts a workload kind and a required reservation. Normal requests
retain their existing estimates. Research creation uses conservative limits:

| Research model | Reservation |
| --- | ---: |
| `mini` | 110 credits |
| `pro` | 250 credits |
| `auto` or omitted | 250 credits |

A Key is eligible for a request only when its projected remaining credits are
greater than or equal to the requested reservation. Eligibility must be tested
inside the Pool mutex before the reservation is recorded, so concurrent callers
cannot admit work against the same credits.

Each selected request receives a unique lease identity. Reservations are
tracked independently rather than only as an aggregate float so completion,
rejection, and expiry are idempotent. Snapshots continue to expose an aggregate
estimated usage and additionally expose the portion held by active Research.

Normal successful request estimates remain until the next authoritative usage
refresh, matching current behavior. A Research creation that receives a valid
`request_id` promotes its lease to a persistent Research reservation. A usage
refresh clears transient estimates but preserves active Research reservations.

## Research Lifecycle

REST and MCP use one shared Research creation function:

1. Parse the model and determine the conservative reservation.
2. Select and reserve an eligible Key.
3. Send `POST /research` with that Key.
4. On `201`, decode and validate `request_id`, promote the reservation, and
   store the existing request-to-Key mapping together with its lease identity.
5. Poll status with the creating Key, preserving Tavily's Key affinity.

When polling returns `completed` or `failed`, the reservation is marked
complete. TvLink refreshes authoritative usage for that Key. A successful
refresh removes completed Research reservations after recording the new usage.
If the refresh fails, the completed reservation remains conservative and the
next successful periodic refresh removes it.

If the client disconnects, the upstream task may continue running. TvLink keeps
the mapping and reservation rather than freeing credits immediately. A later
status request can observe the terminal state. If no caller polls again, the
existing Research mapping TTL is the final safety bound: expiry marks the
reservation abandoned, and the next successful usage refresh reconciles it.

All completion, abandonment, and removal operations are idempotent so a timer,
a REST status request, and an MCP polling loop may race without double release.

## Handling 432 And 433

A `432` or `433` response to Research creation means that Tavily did not admit
the task. TvLink rolls back that attempt's Key and group reservation, records a
Research-only routing pause for the Key, and retries another eligible Key.

The current request maintains an exclusion set, guaranteeing that each Key is
attempted at most once even if concurrent state changes remove a routing pause.
The retry can advance to another group when the current group has no Research-
eligible Key, using the existing group traversal rules.

The routing pause stores the Key's projected remaining credits after the
rejected reservation is removed. It clears only after projected headroom rises
above that value, such as when an in-flight Research reservation completes, the
authoritative usage decreases on monthly reset, or the Key limit increases.
An ordinary refresh with unchanged or worse headroom does not clear it.

The pause affects only Research selection. Search, Extract, Crawl, and Map keep
using the normal Key state and estimates. `Pool.Resolve` no longer assigns
`realUsage = limit` for 432/433 and no longer changes the global state to
`EXHAUSTED` from those responses. Global exhaustion is derived only from an
authoritative usage snapshot where `used >= limit`.

If every candidate returns 432/433, REST returns the last Tavily status, headers,
and body. MCP returns an error containing that upstream status and body. TvLink
returns its existing 503 only when selection has no candidate before any
upstream quota response is available.

## Group Accounting

Selecting a Research request reserves its conservative cost from both the Key's
projected headroom and the active group's round budget. A rejected creation
rolls back both reservations before retrying.

After successful creation, the group's round budget remains charged by the
conservative reservation even after the task completes. Group round usage is a
routing budget rather than an authoritative billing total, so it must not fall
when the per-Key in-flight reservation is reconciled. This preserves the
existing monotonic group rotation behavior and deliberately favors safety over
maximum utilization.

## Usage Refresh Semantics

`/usage` remains the only source of `RealUsage`, `Limit`, and global exhausted
state. A refresh performs these operations atomically under the Pool mutex:

- replace authoritative limit, usage, and timestamp;
- clear transient estimates that the snapshot now accounts for;
- remove completed or abandoned Research reservations;
- retain active Research reservations;
- recompute whether a Research routing pause now has increased headroom;
- preserve group round budgets.

TvLink must not infer authoritative usage from 432/433 response codes.

## Monitor

The monitor keeps actual usage tied strictly to `/usage`. It shows persistent
Research reservations within projected usage and adds a concise per-Key value
for the active Research reservation total. A Key paused only for Research keeps
its normal READY state and receives a separate `RESEARCH PAUSED` indicator.

Page-wide available-Key counts retain their current meaning: Keys in global
READY state. The Research pause indicator explains endpoint-specific routing
without incorrectly declaring the Key unavailable for all work.

## Error Handling And Observability

Research creation errors retain the upstream status and body for the caller.
Retryable quota attempts record structured logs containing the redacted Key
name, status code, model, reservation size, and attempt count. Logs never contain
API keys or the Research prompt.

Logs also record reservation promotion, terminal reconciliation, failed usage
refresh after completion, TTL abandonment, and exhaustion of all candidate
Keys. This provides enough evidence to distinguish Tavily quota rejection from
local selection failure.

## Testing

Pool tests cover:

- rejecting selection when remaining credits are below the reservation;
- atomic admission of concurrent Research reservations;
- preserving active Research reservations across usage refreshes;
- clearing transient estimates on refresh;
- terminal and abandoned reservation reconciliation;
- 432/433 rollback without modifying authoritative usage or global state;
- Research pause behavior and headroom-based recovery;
- continued normal-endpoint eligibility while Research is paused;
- group rollback on rejection and monotonic group budget after completion.

Proxy tests cover:

- REST and MCP 432/433 failover to another Key;
- at-most-once attempts for every Key;
- preservation of the last upstream quota response when all Keys reject;
- successful creation promotion and same-Key polling;
- terminal usage refresh and reservation reconciliation;
- failed terminal refresh retaining a conservative reservation;
- cancellation retaining the reservation until later terminal status or TTL;
- mapping expiry abandoning the reservation idempotently.

Monitor tests cover authoritative usage stability, projected Research usage,
and the separate Research pause indicator.

Repository verification runs formatting, `go test ./... -count=1`, focused race
tests when the local Go race runtime is available, `go vet ./...`, and
`go build ./cmd/tvlink`.

## Compatibility

Public REST paths, authentication, request and success response bodies, MCP tool
names, and the Research mapping TTL configuration remain unchanged. The only
intentional response change is that Research creation may transparently try
additional Keys after 432/433, and an all-Key quota failure preserves Tavily's
quota response instead of returning a synthetic exhausted-Key result.

## Atomic Commit Boundaries

1. `docs: 记录 Research 在途额度修复设计`
2. `docs: 添加 Research 额度修复实施计划`
3. `fix(pool): 持久化 Research 在途额度预留`
4. `fix(proxy): 为 Research 额度错误切换 Key`
5. `fix(monitor): 展示 Research 路由状态`

Each implementation commit includes its focused tests and leaves the repository
buildable and testable.
