---
date: 2026-09-04
topic: boot-sweep-keyset-paging
branch: claude/lane-boot-sweep-paging
range: 38725ba..HEAD
---

# Session Retro: 2026-09-04 - Boot-Sweep Keyset Paging

**TL;DR:** At startup the server checked every enabled schedule for validity, and it loaded all of
them into memory at once before it started listening for HTTP requests. Anyone with an account
could create enough schedules to stop the server booting, and the API you would use to delete them
is exactly what never came up. This session pages that read. The honest result is smaller than it
sounds: it bounds how much memory the check uses, not how long it takes, and the review found that
paging actually introduced a new way for the work to grow while the check is running.

## Handoff

Lane E of a six-item batch over ROADMAP.md's Now section. Advances
[[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]], which **stays open** -
half 1 shipped, half 2 is now [[feature-2026-09-04-per-owner-schedule-cap]]. Eight commits: spec,
plan plus the cap item, three implementation commits, a two-commit fix round, and one prose
deletion.

`ValidateStoredSpecsOnStartup` keyset-pages at `sweepPageSize = 100` using the `cursor_set` bool
pattern the rest of `scheduled_jobs.sql` already uses, and a cancelled sweep now returns instead
of logging every remaining broken row.

**One acceptance criterion was already green** - an earlier slice had corrected the doc comment -
so the spec created no work for it and instead recorded which paragraphs paging makes stale.
There were five, not the three the spec found.

**Half 1 is not a fix for the exposure and the item says so.** Paging converts an unbounded
ALLOCATION into an unbounded DURATION: an actor with a million schedules still delays
`ListenAndServe` by O(N) sequential round trips. Only a cap on N closes it.

## What Was Built

- A paged `ListEnabledScheduledJobsPage` replacing the unbounded statement, with `SELECT *` kept
  deliberately: exactly 5 of 15 columns are used, but `job_spec` **is** the megabyte and is
  load-bearing twice (validated once, then passed as a fence argument), and narrowing would emit a
  new row type outside `TestScheduledJobRowStillCarriesNoFailureSurface`.
- A `ctx.Err()` check at the top of the row loop, so a shutdown mid-sweep names its cause once.
- Two integration tests with 6- and 4-mutation batteries, each with a control that died.

## Key Decisions

- **No page-size seam in the test.** A package var lowered to 2 would force the headline test onto
  a symbol absent at HEAD, so it could not go RED at all - and a compile failure is not a RED. The
  test seeds 250 rows and asserts against the literal 100.
- **`sweepPageSize` is a new constant, not an alias of `BatchLimit`.** That constant's comment says
  it caps rows scanned per tick and it governs a `FOR UPDATE SKIP LOCKED` lock window; this sweep
  takes no locks and its limit governs peak bytes. Aliasing would have made that comment false on
  the same commit.
- **The tracer seam changes zero lines in `runner_test.go`.** `pgxpool.Pool.Config()` returns
  `config.Copy()`, which deep-copies `ConnConfig` and preserves the unexported flag
  `NewWithConfig` panics without - so the traced pool is built from the live pool. That also
  avoided a rebase conflict with a sibling lane rewiring that exact file.

## What Went Wrong and What Changes

**Paging changed the snapshot semantics, and the remedy this slice filed does not cover the new
case.** The old single statement read one MVCC snapshot, so the work set was frozen at N0 and no
concurrent writer could grow it. The paged sweep takes a fresh snapshot per page, so rows inserted
ABOVE the cursor join the work set mid-pass: an owner sitting at a per-owner cap can DELETE one
schedule and POST another, and the replacement's `gen_random_uuid()` lands above the cursor with
probability equal to the unswept fraction of the key space. It converges, so it is duration
amplification rather than non-termination - but it is amplification the cited remedy does not
close, and it exists only because of paging.

Note where the error was and was not. The engineer's own `Cost:` paragraph got it right ("for N
enabled schedules **that do not change during the pass**"). The conductor's header sentence three
lines above it, and the conductor's backlog item, did not. **A caveat stated in one paragraph does
not travel to the paragraph next to it**, and the reviewer found a third copy of the same claim in
the item's own acceptance bullet that neither the conductor nor the engineer had counted.

**A completeness claim about the complement, in the load-bearing position.** A comment said paging
"can only see MORE rows, never fewer". False: a row disabled after the sweep starts but before its
own page is read is missed, where the t0 snapshot would have recorded it. The behaviour is
defensible; the sentence was the paragraph's safety claim and was pinned by nothing.

**The instrument nearly could not tell a broken test from a real RED.** The spec's RED was "assert
3 matching SELECTs, HEAD issues 1". That holds only for a structural matcher: sqlc emits the
`-- name:` header into the SQL const, so a matcher keyed on the new statement name returns 0 at
HEAD - and 0 is also what a test that never reached the sweep returns. The plan caught it and
pinned the matcher to `FROM scheduled_jobs` + `WHERE enabled` + `ORDER BY id`; the review then
confirmed the matcher is specific rather than merely loose, checking that three sibling statements
do not match it.

**Two of the plan's mutation predictions were wrong, and running them was the only way to know.**
The "no cursor advance" mutation does not hang: an un-advanced `pgtype.UUID` is `Valid:false`, so
page 2 asks `id > NULL`, gets nothing, and the short-page break fires - it kills at `selects=2`. A
different mutation is the one that loops, and it is bounded by a 60s deadline rather than
consuming the package clock. **A gated test needs a bounded failure**, and this one has it.

**An assertion was decorative until a mutation earned it.** The plan said a cancellation test's
second assertion was separate because "err may or may not be nil depending on the page count".
With the chosen 3-row input it is always nil, so the mutation died on assertion 1 and assertion 2
killed nothing. Rather than delete it, the engineer wrote a plausible alternative implementation
(record the cancellation, return it after the loop) that passes assertion 1 and dies on assertion
2 - which is also the independent confirmation that HEAD logged exactly one line per remaining
broken row.

**`git diff` reported nothing for two files that `git status` listed as modified.** `buf generate`
LF-churned two files under `internal/proto/relayv1`, and the plan's post-generate check looked in
`internal/pb`, which is not where the proto output lives. Caught by the final `git status`. This is
the repo's documented trap and it fired exactly as documented - **never conclude "nothing to
revert" from `git diff` alone.**

**A measurement was reported with its limits rather than as a number.** The lane baseline measured
30.6s, not the plan's predicted 25s, and after two new tests it measured 29-30s. Rather than claim
the tests were free, the engineer said the two-container cost is not separable from contention at
this resolution, with four sibling lanes running on the machine.

**Two refusals worth keeping.** The engineer declined to add a pointer from the plan doc to the
correcting commits, because a SHA written into a doc on a branch that will be squash-merged names
a commit that will not exist on `main`. And it declined to rewrite the item's Context section,
which is framed as a record of what three lenses found on a date - correctly distinguishing a
historical note from a live claim. Only one sentence there was a live claim in present tense, and
the conductor deleted that alone.

## Recommended Backlog Items

- [[feature-2026-09-04-per-owner-schedule-cap]] - filed. Carries half 2, minus the rate limit,
  which a sibling lane decided OUT rather than deferred. It now also carries the open question this
  slice created: a count cap bounds the sweep's starting work set, and a wall-clock deadline or a
  total-pages ceiling is what would bound its duration.

## Files Most Touched

- `internal/schedrunner/startup_validation.go` - the page loop and the `ctx.Err()` return
- `internal/store/query/scheduled_jobs.sql` - the paged statement
- `internal/schedrunner/startup_validation_paging_integration_test.go` - both tests
- `docs/backlog/` - the parent item's amendments and the new cap item
