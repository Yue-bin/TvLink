# Key Group Rotation Design

## Scope

Add optional Tavily key groups that keep requests on one balanced group for a
configured credit budget, then rotate to the next group. The feature controls
key-usage cadence only; it does not change the process egress IP.

The existing monitor page is explicitly out of scope. Its redesign will be
specified separately.

## Configuration

Grouping is disabled when both `key_group_size` and `group_usage_limit` are
absent, preserving the current global key-selection behavior. When enabled,
the following settings are all required and validated:

```toml
key_group_size = 3
group_usage_limit = 600
group_rotation_timezone = "Asia/Shanghai"
```

- `key_group_size` is a positive maximum number of keys in a group.
- `group_usage_limit` is a positive Tavily-credit budget. It uses the same
  estimate unit as the existing proxy allocation.
- `group_rotation_timezone` must be a valid IANA time-zone name. It defines
  natural-month boundaries because Tavily documents a reset on the first day
  of every month but does not document the reset time zone.

## Grouping

After the startup usage refresh, TvLink forms groups from keys with known,
positive remaining capacity that are not exhausted. Temporarily cooling or
probing keys remain members so a transient rate limit does not change group
ownership.

For `N` eligible keys and a group size `S`, TvLink creates `ceil(N / S)`
groups. It first chooses capacities that differ by at most one, so ten keys
with a size of three become `3/3/2/2`. It then sorts keys by remaining credits
and greedily assigns each key to the non-full group with the smallest total
remaining credits. This balances group carrying capacity despite different
plans and pre-existing usage.

Rebuilding groups starts a new rotation round. The first active group is the
successor of the previously active group position when possible, so a rebuild
does not repeatedly begin with the same group.

## Request Selection And Rotation

The pool keeps the current group, each group's accumulated estimated credits,
and the current month in the configured time zone. Within the active group it
retains the existing remaining-credit weighted key selection, cooldown, probe,
and exhausted-key behavior.

Before reserving a request, the pool compares its estimated cost with the
active group's budget. A request that would cross the budget switches to the
next eligible, unspent group before selection. A single request larger than
the budget is allowed as the first reservation of a fresh group, preventing a
permanent rejection of high-cost research requests.

Each reservation increments both the selected key's estimate and the active
group's estimate. Results that currently roll back a key estimate, including
429 and deterministically non-chargeable 4xx responses, roll back the group
estimate too. Usage refreshes reset only per-key pending estimates; they never
erase a group's accumulated budget for its current round.

A key becoming exhausted does not cause an immediate regroup. Its group keeps
its remaining members. If a group has no selectable key, TvLink skips it. If
every group is unavailable because of cooldown, pending usage, or exhaustion,
the existing `503` behavior applies.

When every group has spent its budget, or when the configured natural month
changes, the next selection requires a rebuild: refresh authoritative usage
for all keys, redistribute the eligible keys, reset group estimates, and retry
selection. The budget remains the configured absolute credit limit rather than
a percentage of the newly reduced balance; this avoids progressively shorter
rotation rounds.

## Coordination And Concurrency

`Pool` remains the owner of key and group state. It reports a distinct
rebuild-required outcome rather than performing HTTP work while holding its
state mutex. A small coordinator, shared by REST and MCP proxy paths, handles
that outcome by refreshing all usage snapshots, rebuilding groups, and
retrying the selection.

Only one coordinator call may refresh and rebuild at a time. A request that
waits for a concurrent rebuild retries selection against the completed round.
Normal selections retain the pool's short critical section and do not serialize
on network I/O. The coordinator preserves the last valid grouping when a full
refresh cannot complete, then returns the normal no-eligible-key result rather
than constructing groups from partial data.

## Logs And Tests

Structured logs record group initialization, threshold rotation, skipped
groups, monthly rebuilds, and rebuild failures using group indexes, key names,
and credit counts only. API keys are never logged.

Tests cover configuration compatibility and validation; balanced grouping with
uneven key counts and capacities; group-scoped weighted selection; budget
crossing; oversized requests; estimate rollback; exhausted-key stability;
full-round rebuilds; month changes in the configured time zone; coordinator
single-rebuild behavior under concurrent requests; and existing REST/MCP
regressions.
