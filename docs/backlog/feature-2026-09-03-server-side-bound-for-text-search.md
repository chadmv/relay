---
title: A server-side bound on the cost of ?q= text search
type: feature
status: open
created: 2026-09-03
priority: high
source: fan-in of the 2026-09-02 web-frontend batch
---

# A server-side bound on the cost of ?q= text search

## Summary
The ?q= filter on GET /v1/jobs and GET /v1/scheduled-jobs is an unbounded substring scan: on a 200k-row jobs table a no-match needle measured about 283 ms of database CPU against 10.8 ms for the unfiltered list, roughly a 26x amplifier, and nothing on the server bounds it. README states the cost honestly and points at the client-side debounce, which is not a bound because any authenticated caller can issue the request directly.

## Context
Found by the security lens of lane JB (PR #178) and deliberately left out of that lane's scope. Lane JF's spec proposed a rate limit on ?q= separately; this item merges both.

## Proposal
- An env-configurable statement_timeout on the pool (or a per-statement timeout for the text-count and text-list statements) so one needle cannot hold a connection for seconds.
- The existing per-IP RateLimit applied to authenticated reads carrying q, with its own bucket so list polling without q is unaffected.
- Measure the amplifier again after either lands and record the input (row count, needle) with the number.

## Related
- [[bug-2026-08-29-post-v1-jobs-is-not-rate-limited]] (the same limiter, on writes)
- [[idea-2026-09-03-pg-trgm-index-for-text-search]] (reduces the cost; does not bound it)
- internal/api/list_filters.go, internal/api/ratelimit.go, README.md "Filtering the jobs list"
