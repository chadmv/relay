---
date: 2026-09-10
topic: reconcile-on-startup-paging
branch: claude/roadmap-now-dependencies-581b21
range: 0922fd12..3dc21367
---

# Session Retro: 2026-09-10 - ReconcileOnStartup Paging

**TL;DR:** When the server boots it catches up any scheduled jobs whose time passed while it was
down, and it was loading every one of those rows into memory at once - including a field that can be
a megabyte per row - before the web API would accept a single request. This session made it read a
page at a time and stop fetching the big field it never used. The valuable part was discovering that
the obvious way to write the test would have passed against the broken design it was meant to reject.

## Handoff

Item 3 of 6 in the roadmap Now batch. Closes
[[bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded]]. Merged as PR #208,
five commits plus spec/plan.

`ListOverdueScheduledJobsForCatchupPage`: keyset on `id` with an explicit `cursor_set` bool,
`ORDER BY id`, exact `LIMIT` with no `+1`, terminating on a short page, narrowed from `SELECT *` to
`id, name, cron_expr, timezone`. `reconcilePageSize = 100`, its own constant - deliberately not
aliased to `BatchLimit` or `sweepPageSize`, which govern different policies. Paged to exhaustion,
**no remainder and no ceiling**. Returns `ctx.Err()` on cancellation instead of logging one line per
remaining row.

**Termination depends on the cursor alone, not on the loop body succeeding at anything** - `id >
cursor_id` excludes every row already read whatever happened to it, so each full page permanently
retires `reconcilePageSize` rows from a finite candidate set. The rejected alternative (re-query the
predicate, rely on each row's own advance) does NOT terminate, on three counts all live in the
current body: a `ParseSchedule` failure continues without advancing, the advance's error is logged
rather than returned, and a fresh per-page `NOW()` lets a short-interval schedule re-enter.

**Duration is still bounded by nothing**, and the header says so. That is slice 2's subject, now
specced as `docs/superpowers/specs/2026-09-10-startup-validation-deadline.md`.

Untagged guard `TestOverdueCatchupPageRowCarriesOnlyTheFourColumnsReconcileReads` asserts the field
SET, not a count, sharing `scheduledJobFieldSetDiff` with the existing row guard - the column list IS
the bound, so without it someone restoring `SELECT *` brings the 1 MiB term back with nothing red.

Next entry point: the two slice-2 items.

## What Was Built

- The paged statement with a header that reuses `ListEnabledScheduledJobsPage`'s reasoning rather than
  re-deriving it, and states only what differs.
- Three integration guards: the page-read count, termination under a poisoned set, and cancellation.
- The untagged column-set guard.
- README's startup sequence corrected - it had listed the HTTP server BEFORE the reconcile, the
  inverse of the property this item is about, and omitted the validation sweep entirely.

## Key Decisions

- **No remainder, and the reconcile's reason differs from the sweep's.** A ceiling leaves rows overdue,
  and `ListEligibleScheduledJobs` then fires each within `TickInterval` - one job per remainder
  schedule, for a firing never-catch-up exists to skip. "The ticker" is not a null action here.
- **`now` captured once above the page loop while SQL `NOW()` floats per page.** The asymmetry is
  deliberate and disclosed: on a long pass a short-interval schedule can be advanced to an
  already-past time and be fired once by the ticker. One fire, not one per missed trigger, and the
  cursor makes revisiting it within the pass impossible.
- **No fence on the advance**, unlike `RecordScheduledJobFailure` beside it, because a fenced
  non-match SKIPS the advance and a row left overdue produces exactly the spurious fire the policy
  forbids. The residual - a PATCH between read and advance losing its freshly computed value - is
  bounded at one fire, and paging NARROWS that window from O(N) to O(page).
- **A short page ends the loop, not an empty one.** On an exact multiple this costs one empty round
  trip; breaking on empty costs the same trip on every table and reads as if a full page could be last.

## What Went Wrong and What Changes

Ledger: the prior retro's entries were all promoted, so none are carried. Promoted lessons that
fired: [[reference_a_ceiling_test_must_assert_the_refusal]] - the headline entry below is its
sharpest instance yet; [[reference_a_gated_test_needs_a_bounded_failure]];
[[reference_verify_the_generated_file_after_a_query_comment_edit]];
[[reference_a_kill_must_name_its_guard]]; [[feedback_mutation_testing_needs_isolated_tree]].

- **The spec's headline guard would not have reddened the design it exists to reject.** It planted
  "at least one" unparseable-cron row. Work the rejected drain loop through: healthy rows advance out
  of the predicate, poisoned rows stay, and the loop ends when a page comes back short - so with fewer
  poisoned rows than one page, the rejected design TERMINATES and satisfies every assertion. The
  load-bearing property is P >= page size; the fixture now plants 150 poisoned against a page of 100.
  Found by the planner, then proven by three independent mutation runs.
  -> **What changes:** when a test exists to reject a specific alternative design, write that design
  out and trace it through the fixture before accepting the fixture. The question is not "does this
  input exercise the code" but "does this input make the REJECTED design fail".
- **Two mutations survive the guard that looks like it should catch them**, because the reconcile
  writes the column its own predicate selects on. A min-cursor mutation still yields 100/100/50 - a
  page count identical to correct code.
  -> **What changes:** where a loop writes the column its own query filters on, a page-count assertion
  cannot detect a cursor defect. Record such a result as a SURVIVAL in the battery rather than
  letting the guard's name imply a kill it did not earn.
- **A false claim contradicted its own header three paragraphs up.** The SQL comment argued no fence
  was needed because the statement "writes a VALUE computed from cron_expr and timezone, so two
  replicas write the same value". The value is `sched.Next(now)` with each replica's own clock; two
  replicas straddling a cron boundary write different values. Deleted, along with "idempotence across
  replicas", which asserts the same equality in condensed form.
  -> **What changes:** when a comment argues from what a statement writes, re-read the write site for
  every input the expression actually depends on. A clock, a random source or an environment read
  makes "computed from X and Y" false even when X and Y are the only named arguments.
- **The prescribed remedy for the matcher fix was wrong, and only a mutation showed it.** Making
  `isSweepPageRead` reciprocal by excluding on the bare column `next_run_at` matches NOTHING, because
  sqlc expands the sweep's `SELECT *` into a column list that itself contains `next_run_at`. The
  needle has to be the whole predicate.
  -> **What changes:** when a matcher discriminates on SQL text, test the needle against the GENERATED
  statement, not the `.sql` source. The two differ in exactly the way that makes a substring match
  wrong. (promoted to project CLAUDE.md)
- **One-sided fixes are traps for the next person.** The new matcher got the discriminator; its
  sibling did not, and the next person to need it is whoever traces both startup passes - which slice
  2 has every reason to do.
  -> **What changes:** when adding a discriminator to one of a matched pair of predicates, add it to
  both or write the guard that fails when one is used where the other belongs.
- **A PowerShell here-string was used through the Bash tool** and leaked a literal `@` into a commit
  subject, caught from `git log` and amended.
  -> **What changes:** write commit messages to a scratchpad file and pass `-F`. [[feedback_commit_heredoc_shell]]

## Recommended Backlog Items

Backlog intake, not a priority order.

- [bug] **`ListGraceCandidates` is a `SELECT DISTINCT` with no LIMIT on the pre-listener path.** Three
  narrow columns over a fleet-sized population, so far smaller than either schedule statement, but it
  is the one pre-listener read nobody had enumerated. `internal/store/query/`.
- [idea] **The `ReconcileOnStartup`/PATCH clobber window.** A PATCH landing between the read and the
  advance can have its freshly computed `next_run_at` overwritten by one derived from the pre-patch
  cron. Bounded at one fire and narrowed from O(N) to O(page) by this slice. **The item must not
  prescribe a fence** - a fenced non-match skips the advance and produces the spurious fire the
  never-catch-up policy exists to prevent.
- [idea] **The boot admits gRPC work while HTTP is still down.** `grpcSrv.Serve` and `dispatcher.Run`
  start above the pre-listener passes and `ListenAndServe` below them, so agents register and receive
  dispatched work for the whole pass while a readiness probe on `:8080` fails. Pre-existing and
  unchanged by this slice.
- [idea] **`make` is not runnable on this dev machine, and the obvious workaround corrupts `git
  status`.** CLAUDE.md's canonical commands are all `make ...`, but no `make` is on PATH - only
  `C:\msys64\usr\bin\make.exe` and Strawberry's `gmake`. Prepending msys64 brings an msys `git` that
  reports every tracked file as modified under `core.autocrlf`, which is the
  never-conclude-nothing-to-revert trap arriving from an unexpected direction. Either document the
  absolute-path invocation or note that agents should run the recipe's `go test` line directly.

## Files Most Touched

- `internal/store/query/scheduled_jobs.sql` - the renamed, paged, narrowed statement and its header.
- `internal/store/scheduled_jobs.sql.go` - regenerated; verified after the CRLF revert to still carry
  both the edit and the four-column statement.
- `internal/schedrunner/runner.go` - the page loop, the cancellation return, `reconcilePageSize`.
- `internal/schedrunner/reconcile_paging_integration_test.go` - new, three guards.
- `internal/schedrunner/overdue_catchup_row_surface_test.go` - new untagged column-set guard.
- `internal/schedrunner/startup_validation_paging_integration_test.go` - the reciprocal matcher and its
  own guard.
- `README.md` - three startup-sequence list items corrected.
