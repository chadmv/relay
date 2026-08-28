---
title: The CLI integration lane crosses a real log page boundary but never a list one, so page[T].NextCursor survives being renamed
type: idea
status: open
created: 2026-08-27
priority: low
source: 2026-08-27 CLI real-server integration lane slice - measured by the Phase 4 correctness lens
---

# The CLI lane never crosses a list page boundary

## Summary
The integration lane added by [[idea-2026-08-23-cli-tests-never-hit-real-server]] exercises the
**log** cursor across a real page boundary (201 rows against a 200-row page, plus an
exact-multiple case that pins the empty-final-page drain rule). It never exercises the **list**
cursor: every list in the lane holds one to three rows against `relayclient.PageRequestLimit = 200`,
so `FetchAllPages` returns on the first page every time.

Measured consequence: renaming `page[T].NextCursor`'s json tag (`next_cursor` -> `nextCursor`) in
`internal/api` **survives** the whole lane.

## Context
This matters more than a generic coverage gap because it is precisely the shape of the defect the
lane was built for. `relay logs` broke for three and a half months when `GET /v1/tasks/{id}/logs`
moved to a pagination envelope and the CLI kept decoding a bare array. The envelope's *container*
is now guarded (the M1 mutation - envelope to bare array - reddens three tests), but the
**cursor field inside it** is not, on the list path.

Two smaller siblings measured at the same time, both feeding only a diagnostic no test triggers:
`handleGetTaskLogs`'s `"total"` key and `logEntry`'s `"seq"` key also survive renaming.

CLAUDE.md's routing rule currently sends "cursor behaviour across a real page boundary" to the
integration lane without distinguishing the two cursors; the sentence now carries a note, but the
gap itself is open.

## Proposal
One test that seeds more than 200 jobs (or workers) and drives `doListJobs`, asserting the full set
comes back. The seeding is the only real cost - a bulk `INSERT ... SELECT FROM generate_series` into
`jobs` is the cheap route, and the harness already does the equivalent for `task_logs` in
`seedLogRows`. Note the existing lane's per-test database makes the row count safe to assume.

Worth checking while there: `--json` on any list path encodes a **bare array**, because
`FetchAllPages` unwraps the envelope and returns `([]T, int64, error)`. So `next_cursor` and `total`
never reach `relay list --json` output at all. That is envelope-level loss orthogonal to this item's
cursor gap, and it may deserve its own decision about whether `--json` should be a passthrough.

## Acceptance / Done When
- A `next_cursor` json-tag rename in `internal/api` reddens the CLI integration lane.
- The uncovered-axis note in CLAUDE.md's routing rule is updated or removed accordingly.

## Related
- [[idea-2026-08-23-cli-tests-never-hit-real-server]] - the closed item that built the lane
- `internal/relayclient/page.go` (`FetchAllPages`, `PageRequestLimit`)
- `internal/cli/logs_integration_test.go` - the log-boundary tests this would mirror for lists
- `CLAUDE.md` "Where a CLI test goes"
