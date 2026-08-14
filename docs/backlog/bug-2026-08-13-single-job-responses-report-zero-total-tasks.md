---
title: Every single-job response reports total_tasks 0 and done_tasks 0, and the SPA's type says the fields are absent
type: bug
status: open
created: 2026-08-13
priority: low
source: Phase 4 review of the 2026-08-13-job-retry-endpoint slice; pre-existing on four handlers, newly pinned by a test on the fifth
---

# Every single-job response reports total_tasks 0 and done_tasks 0, and the SPA's type says the fields are absent

## Summary

`jobResponse.TotalTasks` and `jobResponse.DoneTasks` are plain `int32` with **no `omitempty`**
(`internal/api/jobs.go:70-71`). They are populated only by `applyJobEnrichment`
(`internal/api/jobs.go:122-140`), which only the list-row converters call. Every handler that returns
a **single** job therefore serializes `"total_tasks": 0, "done_tasks": 0` into the body, as a
positive assertion that the job has no tasks.

That is false on every one of those responses. The retry 200 is the sharpest instance: it can carry
`"tasks_retried": 3` and `"total_tasks": 0` in the same object, which is self-contradictory in one
payload.

Separately and in the other direction, the SPA declares the fields **optional**
(`web/src/jobs/api.ts:14-15`) and its comment at `:105-106` asserts "the detail endpoint does NOT
return `total_tasks`, `done_tasks`, `started_at`, or `finished_at`". Half of that is right:
`started_at` and `finished_at` are `*time.Time` with `omitempty` and really are absent. The two
counters are not absent, they are present and wrong. So the client contract and the wire disagree,
and the comment recording the contract is the thing that is false.

## Repro / Symptoms

`POST /v1/jobs/{id}/retry?task=failed` on a job with one failed task returns:

```json
{"id":"...","status":"running","total_tasks":0,"done_tasks":0,"tasks_retried":1, ...}
```

The five un-enriched call sites, all passing `nil` enrichment:

| Handler | Site | Note |
|---|---|---|
| `POST /v1/jobs` (create) | `internal/api/jobs.go:228` | carries the full `tasks` array, so the counts are derivable and wrong anyway |
| `GET /v1/jobs/{id}` (detail) | `internal/api/jobs.go:676` | same |
| `DELETE /v1/jobs/{id}` (cancel) | `internal/api/jobs.go:833` | no `tasks` array either, so the response carries no true count at all |
| `POST /v1/jobs/{id}/retry` | `internal/api/jobs.go:1076` | same, and the one that contradicts its own `tasks_retried` |
| `POST /v1/scheduled-jobs/{id}/run` | `internal/api/scheduled_jobs.go:678` | carries `tasks` |

**No consumer is harmed today**, and the item is filed at low for that reason. `JobDetailPage`
derives progress from `tasks[]` and says so at `web/src/jobs/JobDetailPage.tsx:85-86`, and both job
mutations invalidate rather than seed the cache (`web/src/jobs/useJobActions.ts:20-23`,
`web/src/jobs/useCreateJob.ts:17-19`), so a 0/0 body never reaches
`progressPct(j.done_tasks, j.total_tasks)` at `web/src/jobs/JobsTable.tsx:35`.

## Context

**Pre-existing on four handlers.** The enrichment fields were added for the jobs list and the
single-job handlers were never revisited. What is new is that
`internal/api/jobs_retry_integration_test.go:363-366` now asserts exact key-set equality on the
retry 200 body, with `total_tasks` and `done_tasks` in the expected set. That test is correct about
today's wire and was the right test to write; the side effect is that the wrong value is now pinned
by a passing assertion, so a future fix has to change a shipped test rather than just a struct tag.
Say so when it is fixed, rather than treating the test edit as a surprise.

**Two triggers would raise this from low.**

1. **A client that seeds its cache from a mutation response.** `feature-2026-07-01-job-retry-action`
   specifies invalidation (`:47`), which is safe. A future author reaching for
   `setQueryData(['job', id], response)` as the obvious optimization would overwrite real counts
   with zeros and the job header would read "0 of 0 tasks" until the next refetch. This is the
   `DeleteToken` shape from the 2026-08-13 web-enabler slice: the hazard is in the slice that has
   not been written yet, and this diff is what makes writing it reasonable.
2. **Any non-SPA consumer.** The CLI and the MCP tools read these bodies without the SPA's derived
   progress logic.

## Proposal

Pick one direction and make the wire and the type agree. Both are cheap; the second is the smaller
diff and the safer contract.

**A. Make absence representable.** Change `TotalTasks`/`DoneTasks` to `*int32` with `omitempty`, so
list rows (which always call `applyJobEnrichment`) always emit them, including a legitimate
`done_tasks: 0`, and single-job responses omit them entirely. This makes the SPA's existing optional
type and its `:105-106` comment true as written, and makes "I do not know" distinguishable from
"zero", which is the actual thing being represented.

**B. Populate them.** The detail, create and run-now handlers already have `tasks` in hand and could
compute both counts in Go without a query. Cancel and retry would each need a count, which on retry
is one more statement inside a transaction that already holds the job row lock. More work, and it
still leaves the fields lying about jobs whose tasks were not loaded, unless every future single-job
handler remembers.

**Recommendation: A.** It is a type change plus a nil-guard, it makes the failure unrepresentable
rather than merely fixed at five call sites, and it does not add a query to a write path.

Either way, correct `web/src/jobs/api.ts:105-106`, which currently describes option A's behaviour as
if it were already shipped.

## Acceptance / Done When

- No single-job response asserts a task count it does not know. Under option A, `total_tasks` and
  `done_tasks` are **absent** from the create, detail, cancel, retry and run-now bodies, proven by a
  key-set assertion on each rather than by a per-key check.
- A list row with a genuine `done_tasks: 0` still emits the key. This is the discriminating case for
  option A and the one a naive `omitempty` on a non-pointer would break; it must be proven RED
  against the naive spelling.
- `internal/api/jobs_retry_integration_test.go:363-366` and the cancel-response assertions are
  updated **in the same commit**, with the change noted as intended rather than as a test fixup.
- `web/src/jobs/api.ts:14-15,105-106` matches the wire, and whichever statement is now true is the
  one written down.
- `web/src/jobs/JobDetailPage.tsx:85-86` still derives progress from `tasks[]`; this item does not
  change where the detail page gets its numbers.

## Related

- Source: `internal/api/jobs.go:70-71` (the fields), `:122-140` (`applyJobEnrichment`), `:228`,
  `:676`, `:833`, `:1076`; `internal/api/scheduled_jobs.go:678`
- Consumer side: `web/src/jobs/api.ts:14-15,105-106`, `web/src/jobs/JobsTable.tsx:35`,
  `web/src/jobs/JobDetailPage.tsx:85-86`, `web/src/jobs/useJobActions.ts:20-23`
- The frontend slice that would trip trigger 1: [[feature-2026-07-01-job-retry-action]]
- Adjacent enrichment work that would touch the same struct:
  [[feature-2026-07-01-job-detail-timing-enrichment]]
- Found during: `docs/superpowers/specs/2026-08-13-job-retry-endpoint.md` and
  `docs/retros/2026-08-13-job-retry-endpoint.md`

## Notes

The generalizable shape is one this project has now hit twice in a week from opposite sides: **a
zero value and an unknown value must not share a spelling.** `pageParams.SortKind` was a field with
no reader; these are fields with no writer on five paths, and Go's zero value silently supplied a
confident answer in both cases. When a struct is shared between a converter that populates a field
and one that does not, the field's type has to be able to say which happened.

It is also a live instance of the standing "a wrong contract in prose is a defect" rule: the
comment at `web/src/jobs/api.ts:105-106` is the only written statement of this endpoint family's
shape, and it is wrong about two of the four fields it names.
