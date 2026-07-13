# TvLink Monitor Redesign

## Goal

Replace the current light table with a dark operational dashboard that makes quota consumption immediately readable. The page keeps all existing monitor data public and redacted, preserves server-side rendering and automatic refresh, and adds an aggregate usage summary.

## Visual Direction

Use the approved "console list" layout with the "ink black and ice blue" palette:

- near-black page background with slightly lighter graphite surfaces;
- ice blue for primary emphasis and projected usage;
- restrained green for ready states and coral for cooling or unavailable states;
- compact typography, square-edged progress rails, and radii no larger than 8px;
- no gradients, external assets, client-side JavaScript, or decorative effects.

The information hierarchy is:

1. page identity and live/refresh state;
2. aggregate usage summary;
3. one compact row per configured Key;
4. operational metadata in a quieter secondary line.

## Usage Semantics

For each Key:

- actual usage is `Snapshot.RealUsage`;
- estimated increment is `Snapshot.EstimatedUsage`, representing local reservations since the latest authoritative refresh;
- projected total usage is `RealUsage + EstimatedUsage`;
- quota limit is `Snapshot.Limit`;
- the displayed number is `actual (+estimated increment) / limit`.

For example, actual usage `20`, estimated increment `3`, and limit `100` render as `20 (+3) / 100`. The projected endpoint is 23%, not 3%.

The aggregate summary sums actual usage, estimated increments, and limits across all snapshots. Its projected total is the sum of aggregate actual usage and aggregate estimated increment. This follows the pool's existing assumption that configured credentials expose independent effective quotas.

Aggregate projected remaining is the sum of each snapshot's already-clamped `Remaining` value. It is not recomputed as aggregate limit minus aggregate projected total, because unused quota on one independent Key must not hide an overrun on another.

Displayed counts use comma grouping for thousands. Estimated values retain up to two meaningful decimal places but omit unnecessary trailing zeroes, so integer reservations render as `+3` rather than `+3.00`.

Progress widths use the quota limit as their common scale. Values below zero clamp to 0% and values beyond the limit clamp visually to 100%; numeric values remain truthful and are not truncated. A missing or non-positive limit renders an unavailable rail rather than dividing by zero.

## Progress Component

Each usage visualization is a single overlaid rail:

- a pale dashed outline extends from zero to the projected-total percentage;
- a darker solid segment is layered above it and extends from zero to the actual-usage percentage;
- the visible dashed extension therefore represents the unconfirmed estimated increment;
- a compact legend on the aggregate summary names actual and projected percentages.

The rail includes an accessible text description containing actual usage, projected total, and limit. It is informational and not an interactive control.

## Aggregate Summary

The summary appears first and contains:

- `总用量` label;
- aggregate `actual (+estimated) / limit` value;
- projected percentage;
- overlaid progress rail;
- projected remaining quota;
- available-Key count over configured-Key count;
- automatic refresh interval.

An available Key is one whose snapshot has `Weight > 0`. This reuses the pool's existing eligibility result instead of duplicating its state machine in the monitor. The summary is informative only and does not alter pool selection.

## Key Rows

Each row contains:

- redacted Key name and state badge;
- `actual (+estimated) / limit` value;
- overlaid progress rail;
- authoritative usage refresh time;
- projected remaining quota;
- allocation weight;
- retry time when it is relevant.

Operational metadata remains visible but uses smaller, muted text. Absolute timestamps are rendered in a compact local form to avoid ambiguous raw Go timestamp output. A zero timestamp or retry time displays `--`.

Rows remain sorted by Key name, matching the existing pool snapshot contract.

## Rendering Architecture

The monitor remains a standard-library `html/template` handler. `ServeHTTP` obtains one timestamp and one snapshot slice, transforms them into monitor-specific view models, computes aggregate values, and executes the static template. Presentation calculations stay in `internal/monitor`; no UI-only fields are added to `pool.Snapshot`.

The existing behavior remains unchanged:

- only `GET` is accepted;
- response content type is UTF-8 HTML;
- `Cache-Control: no-store` is retained;
- meta refresh uses the configured monitor refresh interval;
- API key secrets never enter the view model or template.

## Responsive Behavior

Desktop uses a centered content column wide enough for fast comparison. On narrow screens, row headings and usage numbers wrap into two lines, metadata wraps naturally, and the progress rail retains a stable height. No horizontal scrolling is required for the primary workflow.

## Failure And Edge States

- Pending or uninitialized Keys show a pending badge, unavailable usage text, and an empty rail.
- Exhausted Keys retain their numeric usage and show an exhausted badge.
- Cooling and probing Keys keep their usage visible; cooling also shows retry time.
- Empty snapshot lists render a calm empty state beneath a zeroed aggregate summary.
- Template execution failures retain the current HTTP 500 behavior.

## Verification

Focused monitor tests will verify:

- aggregate actual, estimated, projected, limit, remaining, and available-Key values;
- `actual (+estimated) / limit` formatting and projected progress percentage;
- clamping and non-positive-limit behavior;
- state and timestamp formatting;
- method rejection, no-store headers, refresh metadata, and secret redaction;
- responsive and dark-theme structure through stable semantic classes rather than brittle full-HTML snapshots.

Repository verification will run formatting, `go test ./...`, `go test -race ./...`, and `go vet ./...`.
