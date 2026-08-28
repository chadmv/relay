---
title: "`POST /v1/jobs` echoes a raw internal error in its 500 body, unlike every other 500 in the file"
type: idea
status: open
created: 2026-08-28
priority: low
source: Security lens of the Phase 4 review of the retry-bounds slice (2026-08-28); its concrete repro was refuted during triage
---

# `POST /v1/jobs` echoes a raw internal error in its 500 body, unlike every other 500 in the file

## Summary

`handleCreateJob` answers a `CreateJobFromSpec` failure with
`writeError(w, http.StatusInternalServerError, err.Error())` - the raw error, whatever it is. Every
other 500 in `internal/api/jobs.go` writes a fixed string. If that error ever originates in pgx, the
body carries a constraint name, a table name and a SQLSTATE to the caller.

**Read the priority before scoping this.** This is filed as hygiene, not as a live disclosure, because
**the repro that motivated it was refuted.** No currently-reachable input is known to produce a raw
Postgres error there.

## Context

The security lens of the Phase 4 review of
`docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md` reported this as a live leak
with a specific repro: `depends_on: ["a","a"]` passes `jobspec.Validate` (nothing rejects duplicates),
then `jobcreate` calls `CreateTaskDependency` twice for the same pair against
`PRIMARY KEY (task_id, depends_on_task_id)`, and the resulting Postgres error is echoed verbatim.

**That repro is wrong, and it was checked rather than accepted.** `CreateTaskDependency` in
`internal/store/query/tasks.sql` carries `ON CONFLICT DO NOTHING`. A duplicate `depends_on` inserts
once and returns no error, so nothing reaches the echo. The refuted claim is recorded here rather than
deleted, per this project's convention, because the next person to look at this handler will think of
the same repro.

The surviving observation is the shape: an error path that echoes whatever it is handed, in a handler
whose siblings all write fixed strings. Whether any input can reach it with a database error is the
open question, and the honest answer today is that nobody has found one. The candidate error sources
inside `CreateJobFromSpec` are `CreateJob`, `CreateTask`, `CreateTaskWithSource`,
`CreateTaskDependency` and three `json.Marshal` calls; `jobspec.Validate` runs first and rejects
unknown `depends_on` targets and cycles, which closes the two obvious routes.

## Proposal

Either:

- **Make it uniform.** Write a fixed string and log `err` server-side, matching every other 500 in the
  file. Cheap, and removes the question permanently. The cost is that a genuine infrastructure fault
  gets less diagnosable from the client side - which is what the other 500s already accept.
- **Or establish that it is unreachable and say so at the call site**, so the asymmetry reads as
  deliberate rather than as an oversight. This is the more useful outcome if someone is willing to
  enumerate the error sources properly, since it turns an open question into a recorded one.

Do not do both halves half-way: a comment claiming unreachability without the enumeration behind it is
the wrong-prose-about-correct-code shape this project keeps hitting.

## Acceptance / Done When

- Either the body no longer varies with the underlying error, or the call site records why varying is
  safe with the enumeration that establishes it.
- If a reachable input IS found during the work, it is filed or fixed on its own merits - and this
  item's priority was wrong, which is worth saying in the resolution note.

## Related

- Source: `internal/api/jobs.go` (`handleCreateJob`'s `CreateJobFromSpec` error arm),
  `internal/jobcreate/jobcreate.go`, `internal/store/query/tasks.sql` (`CreateTaskDependency`, whose
  `ON CONFLICT DO NOTHING` is what refutes the original repro)
- Filed from the review of [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]]
