---
title: A list cursor carries no filter fingerprint, so a cursor minted under one filter is accepted under another
type: idea
status: open
created: 2026-09-03
priority: medium
source: fan-in of the 2026-09-02 web-frontend batch
---

# A list cursor carries no filter fingerprint, so a cursor minted under one filter is accepted under another

## Summary
Every paged list endpoint validates a cursor against the sort only. A client that changes q, mine, since, until, status, enabled or worker_id while holding a cursor from the previous result gets a page that starts at the old position under the new filter: silently skipped rows, no error. The web pages guard this client-side (a pager reset on filter change and a disabled pager inside the debounce window), which protects the SPA and nobody else.

## Context
Raised by the JB spec and again by the JF and SF reviews, where the client-side guard had to be written twice.

## Proposal
Fold a short hash of the canonical filter set into the cursor and reject a mismatch with a 400 naming the cursor, the same way a sort mismatch is rejected today. Cover it with one cursor-walk integration test per endpoint that changes a filter mid-walk.

## Related
- internal/api/pagination.go
- [[idea-2026-09-03-adopt-usedebouncedpagingguard-on-schedulespage]] (the client-side half)
