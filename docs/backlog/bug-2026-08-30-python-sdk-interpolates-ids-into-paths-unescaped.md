---
title: The Python SDK interpolates caller-supplied ids into request paths unescaped at most sites, while task_logs_page escapes
type: bug
status: open
created: 2026-08-30
priority: medium
source: combined review of the 2026-08-30 comment retrofit slice
---

# The Python SDK interpolates caller-supplied ids into request paths unescaped at most sites, while task_logs_page escapes

## Summary
`python/src/relay/client.py` builds most id-bearing paths with bare f-string interpolation
(`f"/v1/jobs/{job_id}"` at `cancel_job`, `get_tasks`, `get_task`, and siblings), while
`task_logs_page` uses `quote(task_id, safe="")` and its comment states exactly why: an id of
`../../v1/users` resolves to another endpoint on the same host with the caller's bearer token
attached. The inconsistency means the surviving escaped site reads as if the class is handled
SDK-wide, and it is not.

## Context
Found by the combined review of the comment-retrofit slice (pre-existing, untouched by that diff).
This is the Python surface of a class already filed per-surface:
[[bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped]] (Go CLI and MCP) and
[[bug-2026-08-12-unencoded-path-interpolation-api-clients]] (web SPA). Filed separately rather than
appended there because the surfaces and fixes are per-codebase, and the 2026-08-26 file is currently
git-binary-classified (CRLF damage), so appending to it commits a whole-file rewrite.

## Acceptance / Done When
- Every caller-supplied id interpolated into a path in `python/src/relay/client.py` goes through
  `quote(..., safe="")` (or a shared path-builder that does), with a test driving a `../`-shaped id.

## Related
- python/src/relay/client.py (task_logs_page is the escaped exemplar)
