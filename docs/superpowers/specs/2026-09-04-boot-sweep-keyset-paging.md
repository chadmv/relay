# Keyset-page the startup validation sweep

- Date: 2026-09-04
- Backlog item: `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md`
- Subject: `schedrunner.ValidateStoredSpecsOnStartup`, `ListEnabledScheduledJobs`
- Status: design, awaiting review

## TL;DR

`ListEnabledScheduledJobs` reads every enabled schedule, `job_spec` and all, into one
slice before `srv.ListenAndServe()` runs. This slice keyset-pages that read at 100 rows
per page and adds a cancellation check to the row loop. It bounds the sweep's PEAK
MEMORY and its per-statement work. It does NOT bound the sweep's total boot latency,
which stays proportional to the number of enabled schedules - only a per-owner schedule
cap bounds that, and the cap is not in this slice.

Two of the item's three acceptance criteria are addressed here. The third is already
green at HEAD and this slice must not manufacture work for it.

## State of the item at HEAD

### Criterion 2 is already met - do not re-do it

> The sweep's doc comment states the property it actually has.

Met. `ValidateStoredSpecsOnStartup`'s header carries a paragraph opening
"THAT IS NARROWER THAN 'THIS SWEEP CANNOT STOP THE BOOT'". It names the unbounded read
in front of the loop as the hole, names the absent per-user quota and the absent rate
limit on `POST /v1/scheduled-jobs`, and cites this backlog item by filename as the scope
that would fix it. The claim is accurate at HEAD.

A criterion that is green before the change pins nothing, so this slice does not treat it
as work. What it DOES create is an obligation in the other direction: two paragraphs of
that header describe the unpaged shape and go stale the moment paging lands. See
"Comment changes this slice owes" below.

### The rate-limit half of the item is DECIDED - out, not deferred

The item's Proposal half 2 pairs "a per-owner schedule cap" with "a rate limit on
`POST /v1/scheduled-jobs`". A sibling lane settled the rate limit as OUT. Both arguments
check out against this tree:

- **A creation-rate limit bounds growth RATE, not table SIZE, and size is what breaks the
  boot.** `RateLimit` is a per-IP token bucket over a window. It changes how long an
  actor needs to reach N schedules; it puts no ceiling on N. The sweep's cost is a
  function of N alone. Shipping a rate limit and recording "bounded" against this item
  would be the lossy-aggregate shape: a control that reads as a fix for a property it does
  not have.
- **No HTTP rate limit anywhere bounds a schedule's FIRING.** `fireOne` has exactly one
  caller, `Runner.TickOnce`, reached from `Runner.Run` on the goroutine `cmd/relay-server`
  starts with `go schedrunner.NewRunner(pool, q).Run(ctx)`. It never touches an HTTP
  route. Confirmed by search: the only non-test references to `fireOne` are its definition
  and that one call site.

  One nuance the sibling argument does not need but a reader will ask about:
  `handleRunScheduledJobNow` IS an HTTP route and DOES create a job from a stored spec.
  It is not `fireOne` - it is a separate path with its own `jobspec.Validate` call - so
  the claim above stands as stated. Whether run-now needs a rate limit is a different
  question on a different route and is not this item's.

**Recorded decision:** the rate limit is OUT for this item. Amend half 2 to the per-owner
cap alone. The item's criterion "a decision is recorded on the quota and rate-limit half"
is thereby satisfied for the rate limit; the cap stays open as its own item.

## Design

### 1. The keyset page

`scheduled_jobs.id` is `UUID PRIMARY KEY DEFAULT gen_random_uuid()`
(`internal/store/migrations/000006_scheduled_jobs.up.sql`). sqlc maps it to
`pgtype.UUID`. Postgres compares `uuid` bytewise, so `ORDER BY id` is a total,
well-defined order and `id > $cursor` is a well-defined range - the statement already
orders by it.

**Do not use a zero-uuid seed.** A `pgtype.UUID` zero value has `Valid: false`, which
encodes as SQL NULL, and `id > NULL` is NULL - the first page would return zero rows and
the sweep would silently do nothing at all: no error, no log line. That is the same
failure shape as calling an epoch-fenced query with a zero-value epoch. Writing
`pgtype.UUID{Valid: true}` with all-zero bytes would work, since `gen_random_uuid()`
cannot realistically produce the nil uuid, but it makes the first page's correctness
depend on one easily-dropped struct field.

Use the pattern this file already uses everywhere else - an explicit `cursor_set` boolean
that disables the predicate on the first page:

```sql
-- name: ListEnabledScheduledJobsPage :many
SELECT * FROM scheduled_jobs
 WHERE enabled
   AND (NOT @cursor_set::bool OR id > @cursor_id::uuid)
 ORDER BY id
 LIMIT @page_limit::int;
```

`ListEnabledScheduledJobs` has exactly one production caller
(`ValidateStoredSpecsOnStartup`), so it is replaced in place rather than added beside.

**No `+ 1` on the LIMIT, unlike every other paged statement in this file.** The API list
statements fetch `page_limit + 1` because a client needs a `NextCursor` and therefore
needs to know whether a further page exists. The sweep needs no such signal: it loops
until a page comes back SHORT. A reader who pattern-matches the `+ 1` and adds it will
make the last full page indistinguishable from a short one. Say so in the statement's
comment.

**Termination:** `if len(rows) < sweepPageSize { break }`, and advance the cursor to
`rows[len(rows)-1].ID`. On a table whose enabled row count is an exact multiple of the
page size this costs one extra empty round trip; that is the correct trade against a
`len(rows) == 0` condition, which is equally correct here but reads as if a full-page
result could ever be the last one.

**Index behaviour, and the honest degenerate case.** The primary key index serves both the
ordering and the range; `enabled` is a filter on top. No new index is proposed - the sweep
runs once per boot and an index costs every write forever. On a table that is mostly
DISABLED, each page scans past the disabled rows to fill itself, so the total index work
across all pages is O(total rows). That is the same total as today's single scan, since
each page resumes where the previous one stopped. Paging does not make this worse; it also
does not make it better.

### 2. Page size: a new unexported constant, value 100

Introduce `sweepPageSize = 100` in `internal/schedrunner/startup_validation.go`.
**Do not reuse `BatchLimit`.**

`BatchLimit` is documented as "caps rows scanned per tick" and it is a throughput and
lock-window choice: `ListEligibleScheduledJobs` runs `FOR UPDATE SKIP LOCKED` inside a
transaction, so its limit governs how many rows one tick holds locked. The sweep takes no
locks, runs once, and its limit governs peak resident bytes. Aliasing the two couples two
independent policies through one number, and the first thing it does is make
`BatchLimit`'s own comment false - which on this project is the dominant defect class.

The VALUE matches for a reason worth stating in the new constant's comment: at 100, the
boot sweep's peak resident row set is exactly one `TickOnce` batch. `job_spec` is bounded
by `maxBodyBytes` (1 MiB, `internal/api/server.go`), so the worst case is about 100 MiB -
and the runner already sustains that ceiling every `TickInterval` for the life of the
process, while `handleListScheduledJobs` can already reach twice it in one request handler
(`maxLimit = 200` in `internal/api/pagination.go`). After this change the boot sweep costs
no more memory than a single tick of the loop that runs forever. That is the argument for
the number; it is not a claim that 100 MiB is comfortable.

**Constant, not env-configurable.** The project's convention of making operational
timeouts configurable is about waits whose right value depends on the operator's data (a
1 TB P4 sync). A page size is not that: no operator has information the code lacks about
how many rows to hold at once. If the ceiling ever needs to move it should move for
everyone, in a commit that says why. The one condition that would change this answer is a
per-row size bound much larger than 1 MiB; none is proposed.

### 3. `SELECT *` and the column list - narrowing is NOT the fix here

The columns the sweep actually consumes, exactly:

| Column | Read by |
| --- | --- |
| `id` | `RecordScheduledJobFailureParams.ID`; the new page cursor |
| `name` | the two `log.Printf` lines only |
| `job_spec` | `validateStoredRow` -> `ValidateStoredSchedule`, AND `RecordScheduledJobFailureParams.JobSpec` (the fence) |
| `cron_expr` | `ValidateStoredSchedule`, AND the fence |
| `timezone` | `ValidateStoredSchedule`, AND the fence |

Five of fifteen. Unused: `owner_id`, `overlap_policy`, `enabled`, `next_run_at`,
`last_run_at`, `last_job_id`, `created_at`, `updated_at`, `last_error`, `last_error_at`.
(`last_error` is not read: `RecordScheduledJobFailure`'s
`last_error IS DISTINCT FROM sqlc.arg(last_error)` predicate is evaluated in SQL against
the stored row, not against a value the sweep carried.)

**Recommendation: keep `SELECT *`.** The item's framing - "a page of 100 rows each holding
a 1 MiB `job_spec` is still 100 MiB" - is correct, and it is precisely why narrowing does
not help: `job_spec` IS the 1 MiB, and it is load-bearing TWICE. The validator needs it and
the fence sends it back. It cannot be dropped without dropping the fence. The ten unused
columns are four uuids, five timestamps, two short texts and a bool, plus `last_error` at
up to 1039 bytes on broken rows - order 1 KB against order 1 MiB. Narrowing changes the
constant factor by roughly a tenth of a percent and does not touch the term that breaks the
boot.

The costs of narrowing, against that: sqlc emits a new
`ListEnabledScheduledJobsPageRow` struct, `validateStoredRow(row store.ScheduledJob)`
changes signature or disappears, and the new struct sits OUTSIDE
`TestScheduledJobRowStillCarriesNoFailureSurface`, which guards `store.ScheduledJob`'s
field set and nothing else - so the narrowed read acquires a second, unguarded surface.

The column list above is recorded so a future slice that wants the narrowing (for example
if a per-row `job_spec` bound much smaller than 1 MiB ever lands, changing which term
dominates) does not have to re-derive it.

**To bound the sweep's peak in BYTES rather than rows, the lever is the page size, not the
column list.** State that in the new constant's comment so the next person asking this
question finds the answer beside the number.

### 4. `ctx.Err()` in the row loop - confirmed, with a correction to the item

The item says: "on a shutdown mid-sweep every remaining row currently logs its own
`context canceled` line."

**Half right.** `ctx` at the call site is `signal.NotifyContext(...)` from
`cmd/relay-server/main.go`, so SIGINT or SIGTERM during the sweep cancels it. But
`validateStoredRow` does no I/O, so a cancelled sweep runs through HEALTHY rows silently
and at memory speed. Only rows that are BROKEN reach `RecordScheduledJobFailure`, get a
context error back, and hit
`log.Printf("schedrunner: startup validation record for %s: %v", ...)`. So the count is one
line per remaining BROKEN row, not per remaining row.

The conclusion is unchanged, because the sweep's worst case IS "most rows broken" - that
is the release that lands a new retroactive validation rule, which is the scenario the
sweep exists to serve. The item's own Context section makes this amplification point about
the UPDATEs and then does not apply it here.

**Prescribe:** check `ctx.Err()` at the top of the ROW loop and RETURN it. Top of the row
loop rather than top of the page loop, because a single page of 100 broken rows still
emits up to 100 lines otherwise. Return rather than break, so the caller's existing
`log.Printf("warn: schedrunner startup validation: %v", err)` names the cause once.

This changes the function's error contract, and the header sentence "the only returned
error is the list query's" becomes false. Update it in the same commit.

### 5. The fence still holds, and the per-row window SHRINKS

`RecordScheduledJobFailure` fences on `(job_spec, cron_expr, timezone)` - exactly the three
columns the verdict was derived from - so a row repaired through another replica between
the read and the write refuses the stale write, and the caller treats `n == 0` as expected.

**The fence is unaffected by paging, and the reasoning is not "it is probably fine".**

- The fence is a per-row compare-and-set on content. Its correctness has no dependence on
  elapsed time at all. A longer window raises the PROBABILITY of a non-match; a non-match
  is the safe outcome. The failure mode is fail-closed by construction.
- **The window that matters gets SHORTER, not longer.** The premise that paging widens the
  read-then-write window is wrong for the window the fence is about. Today ALL N rows are
  read first and then written in a loop, so the last row processed has a read-to-write
  window spanning the entire sweep. Under paging, a row is read at the start of ITS page
  and written within that page, so the maximum per-row window falls from O(N) to O(page).
  What does grow is the wall-clock span from the FIRST read to the LAST write, by the added
  round trips - and nothing depends on that span, because there is no cross-row invariant
  in this sweep. Every row's verdict is a fact about that row alone.
- `id` is the primary key and no statement in `internal/store/query/scheduled_jobs.sql`
  writes it (`UpdateScheduledJob` rewrites the mutable columns and matches on `id`). The
  cursor key is therefore immutable, which is what makes keyset paging skip-free and
  duplicate-free over rows that exist for the whole sweep.

So: **the fence holds, and the change strictly improves the property it protects.**

### 6. The page boundary as a seam - inserts, deletes, and one wrong assumption

State all four cases in the statement's comment rather than leaving them implied.

- **DELETE mid-sweep.** Safe. A keyset cursor is a value in the key space, not a row
  offset, so deleting a row before the cursor cannot shift later rows into or out of a
  page. This is the whole reason to prefer keyset over `OFFSET`: with `OFFSET n`, deleting
  one already-swept row makes the next page skip a row that was never examined, silently.
  `OFFSET` is the rejected alternative and this is why.
- **INSERT mid-sweep. The item's implicit model is wrong and the spec must say so.**
  `id` defaults to `gen_random_uuid()`, which is UUIDv4 - RANDOM, not monotonic. An
  inserted row does not append to the end of the key order; it lands uniformly at random
  relative to the cursor, so it is seen or missed roughly in proportion to how much of the
  key space is left. Do not write "stable for appends"; there are no appends here.

  Missing it is harmless, and the reason is worth writing down: a row inserted during the
  sweep came through `handleCreateScheduledJob` or `handlePatchScheduledJob` on a running
  replica, both of which ran `jobspec.Validate` from the SAME binary generation the sweep
  is applying. It cannot be broken by a retroactive rule this binary carries. And the sweep
  is a diagnostic pass with another opportunity at the next boot and at the row's next fire.
- **`enabled` flipped mid-sweep.** `enabled` is in the WHERE clause and is mutable. A row
  disabled after being read is still processed (it may get a `last_error` recorded); that
  is identical to today's behaviour after the single LIST. A row ENABLED mid-sweep is seen
  if its id sorts above the cursor and missed otherwise - today it is always missed, since
  it was disabled at LIST time. Paging can therefore only see MORE rows than today, never
  fewer, and seeing more of a diagnostic pass is not a hazard.
- **The sweep never re-reads a row it already wrote**, so there is no read-your-writes
  question across the boundary.

### 7. The regression test

**The discriminating input: more enabled rows than one page.** A test with fewer rows than
`sweepPageSize` passes identically on a paged and an unpaged sweep and pins nothing.

**No test seam, and that is deliberate.** The tempting design - make the page size a
package var and have the test lower it to 2 - forces the headline test onto a symbol that
does not exist at HEAD, so it cannot be run RED against HEAD at all; a compile failure is
not a RED. Plant 250 rows instead and assert against the literal 100. The test then
compiles and runs at HEAD, where it goes red for the real reason.

**File:** `internal/schedrunner/startup_validation_paging_integration_test.go`,
`//go:build integration`, package `schedrunner_test`.

**Lane:** `internal/schedrunner`'s integration lane, run by `make test-integration`. Be
precise about what that means here. `.github/workflows/go-ci.yml` runs `make
vet-integration` (which COMPILES the integration tests), `go test -race ./...` (untagged,
so this test does not run there), and `make test-cli-integration`. It never calls `make
test-integration`. So CI compiles this test and does not execute it; execution is local.
Nothing in this repo blocks a merge in any case. If a sibling lane adds the schedrunner
integration lane to CI, this test starts executing there for free - but do not write the
spec, the plan, or the commit message as if that has already happened.

**Fixtures.** `newRunnerHarness` already spins one Postgres container per test and
`makeOverBudgetSpecJSON` produces a tiny broken spec (a single task with `retries: 50`),
so 250 rows is a few hundred bytes each and cheap. Set `next_run_at` far in the future, as
`TestValidateStoredSpecsOnStartup` does, so neither `ListEligibleScheduledJobs` nor
`ListOverdueScheduledJobsForCatchup` can reach the rows and a pass is attributable to the
sweep.

**Instrument.** Attach a `pgx.QueryTracer` to a pool built for this test and count the
statements the sweep issues whose SQL selects `FROM scheduled_jobs` with `WHERE enabled`
and `ORDER BY id`. This needs a small, additive harness change: expose the DSN on
`runnerHarness` so the new test can build its own traced pool via `pgxpool.ParseConfig`
plus `pgxpool.NewWithConfig`, leaving every existing test's pool untouched. That is a
test-only change and puts no production symbol in the test's path, so the RED survives.

Counting statements is the direct instrument for the claim "the read is issued in pages".
Do not substitute a text search over the query file - that would establish that a `LIMIT`
was typed, not that the loop uses it.

If `pgx.TraceQueryEndData`'s `CommandTag.RowsAffected()` turns out to be populated for
SELECT in the vendored pgx version, add the stronger per-statement assertion (no matching
statement returned more than 100 rows). VERIFY that before relying on it; do not write the
test around an API behaviour nobody checked.

**Assertions, in this order:**

1. With 250 enabled broken rows, the sweep issues exactly 3 matching SELECTs
   (100 + 100 + 50, the third short). At HEAD: 1. This is the RED, and it is a real one.
2. All 250 rows carry a non-nil `last_error`. This is the positive assertion that stops
   the rest from passing vacuously, and it is what a dropped final page or a wrong
   termination condition breaks. Make it a `require`.
3. `ValidateStoredSpecsOnStartup` returns nil.

**Bound the failure.** A cursor that fails to advance is an infinite loop, and a hang is
indistinguishable from infrastructure trouble. Run the sweep under a
`context.WithTimeout` of about 60 seconds and assert a nil error, so that mutant fails as
a named timeout rather than by consuming the package timeout.

**Do NOT try to control which uuid sorts last** in order to pin "the final page is
processed". `gen_random_uuid()` is random and the rows cannot be planted in key order.
Asserting all 250 rows recorded covers the same mutant without depending on an order the
test cannot arrange.

No untagged-lane companion is proposed: a page-loop property cannot be observed without a
database, and a test that only reads the constant would prove nothing about its consumer.

### 8. Comment changes this slice owes

The header of `ValidateStoredSpecsOnStartup` is accurate at HEAD and two of its paragraphs
describe the unpaged shape. Both go stale on this commit.

- **The "THAT IS NARROWER THAN..." paragraph.** It currently says the unbounded read is
  the hole and that paging is out of scope for the comment. After this slice it must say
  what is now true and what is still not: the read is paged, so PEAK MEMORY and
  per-statement work are bounded by one page; the sweep's total wall-clock cost is STILL
  proportional to the number of enabled schedules, and nothing in this slice bounds that
  number. Cite the surviving backlog scope (the per-owner cap) rather than the whole item,
  since the item's rate-limit half is decided out.
- **The "Cost:" paragraph.** "one pass over N enabled schedules at boot, with no I/O per
  row beyond the read that lists them" becomes false in its singular "the read". Replace
  with the paged shape: at most `floor(N/sweepPageSize) + 1` SELECTs, one UPDATE per
  BROKEN row, and peak resident rows equal to one page rather than N.
- **The "A PER-ROW FAILURE MUST NOT STOP THE SERVER BOOTING" paragraph.** Its closing
  clause "the only returned error is the list query's" becomes false when the `ctx.Err()`
  return lands.

Follow the project's comment rule: state the hazard and the constraint, not the history of
this change. The argument for the page size, the fence reasoning and the
uuid-is-not-monotonic finding belong in the commit message and in this spec; only the
load-bearing constraint goes in the code.

## What this slice does NOT cover

- **It does not close the denial of service.** Paging converts an unbounded ALLOCATION
  into an unbounded DURATION. An actor with a million enabled schedules still delays the
  boot by O(N) round trips before `ListenAndServe`, and the HTTP API an operator would use
  to delete them still comes up last. Do not let this item record "bounded" without that
  sentence. The thing that bounds N is the per-owner schedule cap, which is not here.
- **The per-owner schedule cap.** Half 2's surviving half. Stays open as its own item with
  its own spec: it needs a limit value, a counting query, an error shape on
  `POST /v1/scheduled-jobs`, and an answer for owners who already exceed the cap when it
  lands (tightening a validator is retroactive over stored data, and the readers are hard
  to find).
- **A rate limit on `POST /v1/scheduled-jobs`.** Decided out for this item, above.
  Whether that route wants a rate limit for unrelated reasons is a separate question on a
  separate item.
- **Moving the sweep off the boot path.** Out of scope, and the item's stated reason for
  deferring it is wrong: it says "the current no-lock argument depends on the sweep
  completing before the runner starts", which is the exact claim the comment in
  `cmd/relay-server/main.go` already retired ("an earlier version of this comment claimed
  it was ... which is false the moment a second replica exists"). The fence, not the
  placement, is what makes the sweep safe, and the fence is placement-independent. The
  real reasons to defer async placement are that it changes when a boot reports ready and
  that it puts the sweep's UPDATEs in contention with `TickOnce`'s row locks - neither is
  a correctness objection, both are behaviour changes that want their own slice.
- **Narrowing the sweep's SELECT.** Declined above, with the column list recorded.
- **A new index on `scheduled_jobs`.** Declined above.
- **Any change to `ListEligibleScheduledJobs`, `BatchLimit`, or `TickOnce`.**
- **`handleRunScheduledJobNow`.** Named only to keep the `fireOne` argument honest.

## Amendments to the backlog item

Apply these when the slice closes, in the same commit that moves the file to
`docs/backlog/closed/` via `/backlog close`:

1. Rewrite Proposal half 2 as the per-owner cap alone, and record the rate limit as
   DECIDED OUT with the two arguments from this spec. That satisfies the item's third
   acceptance criterion for the rate limit.
2. Correct half 1's `ctx.Err()` sentence: one line per remaining BROKEN row, not per
   remaining row.
3. Correct the option-3 paragraph: the no-lock argument was already retired by the fence;
   the reasons to defer async placement are readiness semantics and lock contention.
4. Note that the second acceptance criterion was already met before this slice.
5. Restate the first acceptance criterion honestly. "The boot sweep's memory and query cost
   is proportional to a page, not to the table" is met; the sweep's DURATION is not, and
   the item should not read as if the exposure is closed.
6. If the per-owner cap is not filed as its own item at close time, file it. A decision
   conditioned on "the cap is coming" is unfalsifiable until the cap is a findable item.

## Files touched

| File | Change |
| --- | --- |
| `internal/store/query/scheduled_jobs.sql` | `ListEnabledScheduledJobs` becomes `ListEnabledScheduledJobsPage` with `cursor_set` / `cursor_id` / `page_limit`; comment covering the no-`+1` difference, the delete and insert cases, and the uuid-is-not-monotonic point |
| `internal/store/scheduled_jobs.sql.go` | regenerated by `make generate` (watch the CRLF revert, and verify the regenerated `.sql.go` actually survived it) |
| `internal/schedrunner/startup_validation.go` | `sweepPageSize` constant; the page loop; the `ctx.Err()` return; the three header paragraphs above |
| `internal/schedrunner/startup_validation_paging_integration_test.go` | new |
| `internal/schedrunner/runner_test.go` | expose the harness DSN (additive, test-only) |
