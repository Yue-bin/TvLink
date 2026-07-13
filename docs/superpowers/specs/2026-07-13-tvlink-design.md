# TvLink Design

## Goal

TvLink is a single-instance Go service that balances independent, finite Tavily API keys by cached credit usage. It exposes Tavily-compatible REST proxy routes and an authenticated MCP endpoint, while offering an unauthenticated human-readable status page.

## Configuration

Configuration is read once from a TOML file passed through `-config`. It contains a fixed TvLink client key, named Tavily keys, listener address, refresh intervals, and request limits. The parser is `github.com/BurntSushi/toml`, a pure-Go dependency. Unknown fields and invalid values fail startup. Real configuration is ignored by Git; an example file is committed.

## Upstream Contract

TvLink sends Tavily requests to `https://api.tavily.com` with the selected Tavily key in the `Authorization` header. It forwards Tavily project and attribution headers but never forwards the caller's authorization header. Supported REST routes are Search, Extract, Crawl, Map, Research creation, and Research status. The MCP endpoint offers corresponding tools.

## Pool State

Per-key in-memory state stores the latest `/usage` response, the time it was fetched, endpoint-specific local reservations, circuit state, and an endpoint-cost moving estimate. The monitor distinguishes:

- real usage: Tavily's last `key.usage` value;
- real usage fetched at: the successful `/usage` refresh timestamp;
- estimated usage: local reservations since the latest usage snapshot;
- estimated remaining: `limit - real usage - estimated usage`.

At startup TvLink immediately refreshes every key. A key starts with weight zero and becomes eligible only after a successful refresh. Refreshes run every 90 seconds and are single-flight per key. A usage-refresh `429` preserves existing state; for an uninitialized key it remains ineligible until the `Retry-After` moment.

## Selection and Failure Handling

For eligible keys, selection is weighted random using remaining estimated credits, a bounded remaining-ratio correction factor, and a rate-limit state factor. Key and PAYG exhaustion responses (`432`, `433`) set remaining credits to zero. A business `429` opens that key's circuit until Tavily's `Retry-After`; it then transitions to one half-open probe. A successful probe closes the circuit.

Search, Extract, Crawl, and Map may retry once against a different eligible key only after a definite upstream `429` before any client response is written. No retry follows a network failure or `5xx`. Initial Research creation and Research status do not switch keys automatically because Research has no idempotency key.

## Research and Streaming

Non-streaming Research creation stores a `request_id -> key` mapping for 24 hours. Later status requests use exactly that key. SSE Research responses are relayed without buffering or transforming bytes and never switch keys after response headers are sent. Research mappings are intentionally lost on process restart because all state is in memory.

## Monitoring and Operations

`GET /` is public, rendered with `html/template`, set to no-store, and refreshes every five seconds. It exposes only redacted key names and operational state. All proxy and MCP traffic uses the fixed TvLink API key. Background workers use a root context and stop before HTTP server shutdown completes.

## Validation

The implementation uses table-driven tests and `httptest` for configuration validation, pool selection, reservation reconciliation, 429/432/433 behavior, sticky Research routing, auth, proxy header replacement, SSE forwarding, and monitor fields. Completion requires formatting, `go test`, race tests, `go vet`, and any installed linter.
