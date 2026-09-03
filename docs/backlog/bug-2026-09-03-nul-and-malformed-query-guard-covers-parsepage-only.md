---
title: The NUL-byte and malformed-query rejection covers parsePage endpoints only
type: bug
status: open
created: 2026-09-03
priority: medium
source: fan-in of the 2026-09-02 web-frontend batch
---

# The NUL-byte and malformed-query rejection covers parsePage endpoints only

## Summary
PR #178 made parsePage parse the query string once through url.ParseQuery and reject a NUL byte or a malformed escape with a 400. Handlers that do not page still read r.URL.Query() directly: handleEvents, handleCancelJob, handleRetryJob, handleGetTaskLogs and handleDisableWorker, and two of them (task logs, events) pass a query value toward the database, which is the path that produced a 500 on q=%00 before the guard.

## Context
Found in lane JB's second re-verify; out of that lane's scope.

## Proposal
Route every handler's query read through one parse-and-validate helper and add a table test that sends %00 and a bad escape to each route.

## Related
- internal/api/pagination.go (rejectNulBytes, rejectRepeatedParams)
- [[bug-2026-08-13-cursor-value-kind-not-validated]]
