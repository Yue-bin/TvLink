# Research Credit Estimate Design

## Scope

Change only the in-memory reservation estimate for Tavily `/research` requests.
All existing estimates for Search, Extract, Map, and Crawl remain unchanged.

## Behavior

The proxy reads the optional `model` value from a Research JSON request body and
uses these fixed credit estimates:

| Model | Estimate |
| --- | ---: |
| `mini` | 10 |
| `pro` | 40 |
| `auto` or omitted | 30 |

An invalid or unparseable Research request body falls back to 30 credits. The
upstream request remains unchanged; this estimate is solely for local pool
reservation and allocation.

## Validation

Add focused tests for every model mapping, the omitted-model default, and
non-Research behavior. Run the full Go test suite and build the service.
