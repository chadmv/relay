# Keyset-page and narrow ReconcileOnStartup's overdue read

- Date: 2026-09-10
- Backlog item: `docs/backlog/bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded.md`
- Subject: `schedrunner.ReconcileOnStartup` (`internal/schedrunner/runner.go`),
  `ListOverdueScheduledJobsForCatchup` (`internal/store/query/scheduled_jobs.sql`)
- Status: design, awaiting review
- Lane: B, slice 1 of 2. Slice 2 is
  `docs/backlog/feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep.md`.

## TL;DR

`ListOverdueScheduledJobsForCatchup` is `SELECT *` with no LIMIT over every enabled
overdue row, and `ReconcileOnStartup` is its only caller, called above the goroutine
that starts `srv.ListenAndServe()`. This slice applies two independent levers:

1. **Keyset-page on `id`**, exactly as `ValidateStoredSpecsOnStartup` is paged. This is
   the lever the item's first acceptance criterion asks for.
2. **Narrow the column list to `id, name, cron_expr, timezone`.** The sweep declined this
   because `job_spec` is load-bearing twice there. Reconcile reads `job_spec` **zero**
   times, so the sweep's answer inverts and narrowing is the larger lever per row.

It also adds the `ctx.Err()` row-loop return the sweep already has, because reconcile
issues one UPDATE per row rather than one per broken row, so its cancellation exposure is
strictly worse than the one the paging slice already closed next door.

**No ceiling and no remainder: the loop pages to exhaustion.** A ceiling would leave rows
overdue, and README's never-catch-up sentence would become false for the remainder. The
DURATION stays unbounded, exactly as the sweep's does. That is slice 2's axis and this
spec must not read as if it closed it.

**The naive design is non-terminating and that is the finding that matters most.** See
Decision 1.

## What this spec refutes

Every path and symbol the item cites was checked against the tree.

### 1. The item names the wrong file for `ReconcileOnStartup`

The item's Related section says:

> `internal/schedrunner/startup_validation.go` (`ReconcileOnStartup`)

`ReconcileOnStartup` is in **`internal/schedrunner/runner.go`**, at the bottom of the file,
below `TickOnce` and the four `advance*` helpers. `startup_validation.go` holds
`ValidateStoredSpecsOnStartup`, `validateStoredRow` and `ValidateStoredSchedule`, and it
mentions `ReconcileOnStartup` only in prose. Correct the item's Related section when the
item closes.

This matters beyond tidiness: an implementer who opens `startup_validation.go` looking for
the subject finds an already-paged function and a header comment that reasons at length
about paging, and the most likely outcome is a patch to the wrong function.

### 2. "before the server accepts a request" is true of HTTP and false of gRPC

The item's Summary says the boot "materializes every overdue enabled row before the server
accepts a request". `cmd/relay-server/main.go` starts the gRPC server in a goroutine at the
`grpcSrv.Serve(grpcLis)` call **above** the `ReconcileOnStartup` call, and starts
`srv.ListenAndServe()` in a goroutine **below** both it and the sweep.

So during the reconcile the process is half up: agents are connecting, registering and
being dispatched to over gRPC, while the HTTP API is not listening. The item's severity
argument survives unchanged, because the operator's remedy (`DELETE /v1/scheduled-jobs/{id}`)
is on the HTTP side. But three things follow that neither this item nor slice 2's item states:

- A readiness probe on `:8080` fails while `:9090` is already accepting. The boot is not
  "not started", it is asymmetrically started.
- Reconcile's per-row UPDATEs compete for the same `pgxpool` (default `MaxConns` 25, or
  `RELAY_DB_MAX_CONNS`) with agent registrations already in flight.
- A `handlePatchScheduledJob` on **another replica** is reachable during this window, which
  is what makes Decision 5's clobber window real rather than theoretical.

Fix the sentence in the item to say "before the HTTP listener accepts a request".

### 3. The item's third acceptance criterion is substantially already green

> The claim that the boot's peak memory is one page is either made true or corrected
> wherever it is written down.

**No site in the tree asserts that the BOOT's peak memory is one page.** Every occurrence
found by searching the tree for `peak memory`, `peak resident` and `one page` is scoped to
the SWEEP and is true as written. The full delete-or-correct table is in "Prose this slice
owes" below; the summary is that the criterion's own subject needs no edits, and two sites
(`docs/superpowers/specs/2026-09-04-per-owner-schedule-cap.md` and its plan) already carry
the correction verbatim.

A criterion green before the change pins nothing, so this slice does not manufacture work
for it. What the search DID turn up is two genuinely false prose sites the criterion does
not point at, and both are this slice's to fix - one in README that is about the boot
ORDER rather than about memory, and one in the sibling backlog item. Both below.

### 4. The sibling spec's claim about the schedrunner lane is false against this tree

`docs/superpowers/specs/2026-09-04-boot-sweep-keyset-paging.md` section 7 says the new
schedrunner integration test runs under `make test-integration`, that
`.github/workflows/go-ci.yml` "never calls `make test-integration`", and therefore that
"CI compiles this test and does not execute it; execution is local".

Against the tree today all three links of the chain that would make that false are present:

- `internal/schedrunner/runner_test.go` takes its database from
  `pgdsn.NewIntegrationDSN(t)`.
- `Makefile`'s `test-pg-integration` target names `./internal/schedrunner/...` in its
  `go test` invocation.
- `.github/workflows/go-ci.yml`'s `pg-integration` job runs `make test-pg-integration`.

So an `//go:build integration` test in `internal/schedrunner` **does execute in CI today**.
Whether the sibling spec was wrong when written or was overtaken by a later Makefile edit
is not determinable without git history, which this session cannot read. Either way the
claim is false now, so this spec states the current wiring and the plan's Task 0 re-checks
it rather than inheriting it. Both edits CLAUDE.md requires are already in place, so this
slice adds no Makefile or workflow change.

### 5. A stale line reference, recorded so nobody chases it

`docs/superpowers/specs/2026-09-04-per-owner-schedule-cap.md` cites
`internal/store/query/scheduled_jobs.sql:130` for `ListOverdueScheduledJobsForCatchup`. The
statement is at line 226 today; the cap slice's own two statements were inserted above it.
A dated design record does not get rewritten, so this is noted and not edited.

### 6. What the item gets right, checked

- The statement text quoted in the Summary matches the tree exactly.
- `ReconcileOnStartup` is the only caller. The only other references to the statement name
  are the generated `internal/store/scheduled_jobs.sql.go`, three comments, and dated docs.
- The per-owner cap does not close it, for the reason the item gives: the result set is
  every overdue enabled row across all owners.
- The severity framing is right - the bad case is a deployment down long enough for a large
  fraction of its schedules to come due, which is the restart where the boot is already slow.

## The pre-listener query enumeration

This answers the item's own Proposal line, "Check whether the same reasoning applies to any
other pre-listener query." **Enumerated by name.** No count is asserted, because a count is
a claim about a complement and the axis here is "statements reachable from `main` before the
`srv.ListenAndServe()` goroutine", which is the axis a future reader must re-walk.

### Statements that gate the HTTP listener

| Statement | Reached from | Bounded? | Scope |
| --- | --- | --- | --- |
| `store.Migrate` (not a sqlc statement) | `main` | Yes - by the embedded migration file count, a compile-time constant | Out |
| `AdminExists` | `main`, and again inside `bootstrapAdmin` | Yes - `:one` bool | Out |
| `GetUserByEmail` | `bootstrapAdmin` | Yes - `:one`, conditional on `RELAY_BOOTSTRAP_ADMIN` | Out |
| `PromoteUserToAdmin` | `bootstrapAdmin` | Yes - `:exec` by id | Out |
| `CreateUserWithPassword` | `bootstrapAdmin` | Yes - `:one` | Out |
| `ListGraceCandidates` | `seedGraceTimersFromActiveTasks` | **No LIMIT.** See below | Out, named |
| `ListOverdueScheduledJobsForCatchup` | `ReconcileOnStartup` | **No LIMIT, `SELECT *` incl. `job_spec`** | **THIS SLICE** |
| `AdvanceScheduledJobNextRun` | `ReconcileOnStartup` | Per statement yes (`:exec` by id); the COUNT is O(overdue rows) | Duration, slice 2's axis |
| `ListEnabledScheduledJobsPage` | `ValidateStoredSpecsOnStartup` | Memory yes (keyset-paged at 100); **duration no** | Slice 2 |
| `RecordScheduledJobFailure` | `ValidateStoredSpecsOnStartup` | Per statement yes (`:execrows` by id); the COUNT is O(broken rows) | Slice 2 |

**`ListGraceCandidates` is the one nobody had named, and it is genuinely unbounded.** It is
`SELECT DISTINCT w.id, w.disconnected_at, w.connection_epoch FROM workers w JOIN tasks t ON
t.worker_id = w.id WHERE t.status IN ('dispatched', 'preparing', 'running')` with no LIMIT.
Three narrow columns - a uuid, a timestamptz and an int4 - so the per-row term is order tens
of bytes against `job_spec`'s 1 MiB ceiling, and the population is workers holding a
non-terminal task rather than user-authored content. Growing it means enrolling workers,
which `RELAY_AUTO_ENROLL_WORKER_CEILING` and the `netlimit` connection caps bound where
auto-enroll is on. **Out of scope here** - different table, different population, different
owning lane - and proposed as a backlog item below so the enumeration does not die in this
spec.

### Statements that run concurrently but do NOT gate the listener

Started as goroutines above the `ListenAndServe` goroutine, so they compete for pool
connections but cannot delay HTTP admission.

- `NotifyListener.Run` - `LISTEN` only.
- `Dispatcher.Run` - the dispatch poll.
- `Runner.Run` -> `TickOnce` -> **`ListEligibleScheduledJobs`**. **This is the direct answer
  to "does the ticker's own statement have the same shape": no.** It carries `LIMIT $1`,
  called at `BatchLimit = 100`, plus `FOR UPDATE SKIP LOCKED`, and it has since it was
  written. It is also not on the gating path. Nothing to do.
- `metrics.NewSweeper(...).Run`, `watchdog.Run`, `runEnrollmentJanitor`.

### What may honestly be claimed after this slice

> No boot statement's result set grows with the number of enabled or overdue SCHEDULES.
> `ListGraceCandidates` still grows with the number of workers holding a non-terminal
> task, at three narrow columns per row. The boot's DURATION is bounded by nothing.

That sentence is the replacement for any temptation to write "the boot's peak memory is one
page". Writing the shorter version would be the lossy-aggregate shape this item exists to
correct, one slice later.

## Design

### Decision 1: keyset on `id`, and why the obvious alternative does not terminate

The cursor is **`scheduled_jobs.id`**, the uuid primary key, ordered `ORDER BY id` with an
explicit `cursor_set` boolean rather than a zero-uuid seed. Every reason is already written
into `ListEnabledScheduledJobsPage`'s comment and holds verbatim: Postgres compares `uuid`
bytewise so the order is total and the range is served by the primary key index; no statement
in this file writes `id`, so the cursor key is immutable, which is what makes keyset paging
skip-free and duplicate-free; a `pgtype.UUID` zero value has `Valid: false` and encodes as
SQL NULL, and `id > NULL` is NULL, so a zero seed makes the first page empty and the pass
silently does nothing at all - no error, no log line, the same failure shape as an
epoch-fenced query called with a zero-value epoch. **Reuse that reasoning; do not re-derive
it in the new statement's comment.** Cite the sibling statement as the precedent and state
only what is different here.

**What IS different here, and it is the whole design question.** The sweep's predicate
(`enabled`) is not written by the sweep. Reconcile's predicate (`enabled AND next_run_at <
NOW()`) selects on the very column the loop writes. That makes a second design tempting and
it must be rejected explicitly:

> **REJECTED - the drain loop.** Re-issue `SELECT ... WHERE enabled AND next_run_at < NOW()
> LIMIT N` until it returns fewer than N rows, with no cursor, relying on each row's own
> advance to remove it from the predicate.

It reads as simpler and it **does not terminate**, on three independent counts, all of them
live in the current function body:

- **`ParseSchedule` failure skips WITHOUT advancing `next_run_at`.** That branch exists
  today, it logs `reconcile skip for %s` and `continue`s, and `ValidateStoredSpecsOnStartup`'s
  header documents the omission as deliberate ("the row stays overdue and
  `ListEligibleScheduledJobs` picks it up on the very next tick at most 10 seconds later").
  Under a drain loop, `reconcilePageSize` such rows are re-selected forever, before the HTTP
  listener, at boot. And an unparseable stored cron is exactly the class `ValidateStoredSchedule`
  exists for - a tightened parser, a timezone the tzdata no longer carries, a row written by
  an older binary - so this is not a hypothetical input.
- **`AdvanceScheduledJobNextRun`'s error is logged, not returned.** A row whose UPDATE fails
  for any reason stays overdue and is re-selected on the next iteration, forever.
- **A short interval can outrun the pass.** Each page is a fresh statement with a fresh SQL
  `NOW()`. An `@every 1s` schedule advanced to `now+1s` re-satisfies `next_run_at < NOW()`
  a second later.

The `id` cursor closes all three with one mechanism, and the argument is worth stating as a
proof because "it terminates" is the property a reviewer must be able to check:

**Termination.** Each iteration either breaks on a short page or sets `cursor` to
`rows[len(rows)-1].ID`. Because the statement is `ORDER BY id`, that value is the maximum id
on the page, and the next statement's `id > cursor` excludes every id up to and including it.
The candidate key space is finite and strictly shrinks by at least `reconcilePageSize` ids
per full page. No row at or below the cursor can be re-read for **any** reason - not because
its `next_run_at` was advanced, not because `ParseSchedule` skipped it, not because its
UPDATE failed. Termination is a property of the cursor alone and does not depend on the loop
body succeeding at anything.

**Coverage.** Every row that satisfies the predicate at the start and is not written by
anyone else is read exactly once. Rows already processed are excluded twice over (below the
cursor, and no longer overdue), which is belt and braces rather than a dependency. A row that
BECOMES overdue during the pass - its `next_run_at` crosses `NOW()` between page 1 and page
3 - is seen if its `gen_random_uuid()` id sorts above the cursor and missed otherwise. **Missing
it is correct, not a gap**: a schedule that came due during the boot missed nothing during
downtime, it is due now, and `ListEligibleScheduledJobs` fires it once within `TickInterval`.
That is what never-catch-up wants.

**`now` stays captured once, above the loop.** It is the never-catch-up reference instant, and
every row's advance should derive from the same boot instant rather than from where the row
happened to land in `gen_random_uuid()` order. The consequence, stated so it is not mistaken
for a defect: on a long pass a short-interval schedule can be advanced to a time already past,
leaving it overdue, and the ticker then fires it once within `TickInterval`. It is not a
catch-up (one fire, not one per missed trigger) and it cannot loop (the `id` cursor precludes
revisiting). Note the deliberate asymmetry with the SQL `NOW()`, which floats per page.

### Decision 2: never-catch-up under paging - page to exhaustion, no ceiling, no remainder

**Decision: no ceiling. The loop runs until a short page.** The only remainder that can
exist is on cancellation, and that path ends in process exit.

Why not a ceiling with a remainder, which is the shape the item leaves open:

- **Every rejected destination for a remainder is wrong.** "Nowhere" leaves schedules overdue
  across boots. "The ticker" is not a null action: `ListEligibleScheduledJobs` picks the row
  up within ten seconds and `fireOne` creates **one** job for it. That is bounded - it is not
  a catch-up storm, because `fireOne` advances to `sched.Next(time.Now())` after firing - but
  it is one spurious job per remainder schedule, at boot, for a firing the never-catch-up
  policy exists to skip. A `@monthly` schedule would fire on the boot instead of on the 1st.
  "A later pass" is a new mechanism with its own placement question and is not worth buying
  to avoid a loop that already terminates.
- **It would falsify README's never-catch-up sentence** ("any firings that fell during
  downtime are skipped (no catch-up), and the schedule resumes on its next eligible fire").
  That sentence is a user-facing contract. Making it conditionally false for an unnamed subset
  is the wrong-prose-about-correct-code defect this project treats as the dominant class.
- **The criterion is about memory and paging already delivers it.** A ceiling buys duration,
  which is a different property with a different acceptance criterion, and it is slice 2's.

**Where an operator reads what happened.** Nothing new is added, and that is deliberate: a
log line about a pass that always completes is noise on every boot forever, which is the
argument `RecordScheduledJobFailure`'s own comment already makes against an unconditional
stamp. On the one path where a remainder exists - cancellation - `main`'s existing
`log.Printf("warn: schedrunner reconcile: %v", err)` names the cause once. The place a reader
looks for the policy is `ReconcileOnStartup`'s own header, which this slice rewrites to state
it: the pass is exhaustive, the only truncation is a cancelled boot, and a cancelled boot's
remainder is handled by the next boot's reconcile because the process is exiting - `ctx` at
the call site is `signal.NotifyContext`, so a cancellation there means shutdown is already
under way.

This satisfies the item's second acceptance criterion - decided and documented, not implicit.

### Decision 3: narrow the column list, and why the sweep's answer inverts

`ReconcileOnStartup` reads exactly four fields off the row:

| Column | Read by |
| --- | --- |
| `id` | `AdvanceScheduledJobNextRunParams.ID`; the page cursor |
| `name` | the two `log.Printf` lines only |
| `cron_expr` | `ParseSchedule` |
| `timezone` | `ParseSchedule` |

It never touches `job_spec`, `owner_id`, `overlap_policy`, `enabled`, `next_run_at`,
`last_run_at`, `last_job_id`, `created_at`, `updated_at`, `last_error` or `last_error_at`.

**The sweep declined narrowing for a reason that does not apply here, and the new statement's
comment must say so, because the two paged statements over the same table will otherwise look
gratuitously inconsistent.** `sweepPageSize`'s comment says "THE PAGE SIZE IS THE LEVER FOR
PEAK BYTES, NOT THE COLUMN LIST", and it is right there: `job_spec` dominates the row, it is
bounded only by `maxBodyBytes` at 1 MiB, and it is load-bearing twice - `validateStoredRow`
reads it and `RecordScheduledJobFailure`'s fence sends it back - so it cannot be dropped.
Reconcile reads it **zero** times and sends nothing back. The dominant term is therefore
droppable outright, which makes narrowing a larger lever per row here than any page size, and
the two levers multiply.

**Recommendation: do both.** Paging is what satisfies the acceptance criterion (it is what
makes peak independent of N); narrowing is a constant-factor improvement of several orders of
magnitude on top. If narrowing turns out to cost more than this spec expects, it can be
dropped without failing the criterion - but say so in the commit rather than shipping it
silently, because the guard in Decision 6 goes with it.

**What narrowing costs, stated so it is not discovered late:**

- sqlc emits a new row struct (expected `store.ListOverdueScheduledJobsForCatchupPageRow`).
  Verify the emitted name rather than assuming it; it is plan Task 0.
- That struct sits **outside** `TestScheduledJobRowStillCarriesNoFailureSurface`, which is a
  field-set guard over `store.ScheduledJob` and nothing else. This is the same cost the
  boot-sweep spec named when it declined narrowing. Here it is worth paying, and it is paid
  with the untagged guard in Decision 6.
- Nothing else changes. The statement has one caller, no test reads the row type (the
  existing reconcile tests call `schedrunner.ReconcileOnStartup`, and the paging test mentions
  the statement only in a comment), and all four surviving fields keep their names.

**Rename the statement to `ListOverdueScheduledJobsForCatchupPage`**, replacing it in place
rather than adding beside it, exactly as `ListEnabledScheduledJobs` became
`ListEnabledScheduledJobsPage`. One caller, so there is no migration period. The rename has
two comment consequences listed under "Prose this slice owes".

The resulting statement, for the plan to start from:

```sql
-- name: ListOverdueScheduledJobsForCatchupPage :many
SELECT id, name, cron_expr, timezone FROM scheduled_jobs
 WHERE enabled
   AND next_run_at < NOW()
   AND (NOT @cursor_set::bool OR id > @cursor_id::uuid)
 ORDER BY id
 LIMIT @page_limit::int;
```

No `+ 1` on the LIMIT, unlike every client-facing paged statement in this file: the caller
detects the end by a SHORT page and needs no `NextCursor`. A reader who pattern-matches the
`+ 1` makes the last full page indistinguishable from a short one, so the pass stops one page
early. Terminate on `len(rows) < reconcilePageSize`, not on an empty page - the empty-page
condition is equally correct and reads as if a full page could be the last one.

### Decision 4: `reconcilePageSize = 100`, its own constant

A new unexported constant in `internal/schedrunner/runner.go`, beside `BatchLimit`.

**Do not alias it to `sweepPageSize` and do not alias it to `BatchLimit`.** The reasoning is
`sweepPageSize`'s own, applied a third time: `BatchLimit` governs how many rows one tick holds
LOCKED, `sweepPageSize` governs the sweep's peak resident bytes, and this one governs the
reconcile's round-trip granularity. Three independent policies behind one number makes two of
the three comments false the first time any of them moves.

**The value matches the other two on purpose**, and that is the sentence the new constant's
comment should carry: after narrowing, no statement on the boot path holds more rows at once
than any other, so a reader reasoning about boot memory has one number to hold rather than
three.

**Not raised, and not measured.** After narrowing, memory stops being the binding constraint
on this number and round trips become it - which invites raising the page size. Declined here
because round trips are not the quantity this item bounds, and raising it would be a duration
change made without a measurement. If an implementer wants a larger value, it needs a number,
and getting one is plan Task 0, not a spec assertion.

**A constant, not an env var**, for `sweepPageSize`'s reason: the configurable-timeout
convention is about waits whose right value depends on the operator's data, and no operator
has information the code lacks about how many rows to hold at once.

### Decision 5: no fence on the advance, and fencing would be actively wrong

The sibling statement `RecordScheduledJobFailure` carries a three-column content fence, so a
reader will ask why this one carries none. The answer is not "it is probably fine".

`q` is pool-backed, so the LIST and every UPDATE are separate implicit transactions with
anything at all permitted in between, and `handlePatchScheduledJob` on **another replica** is
reachable during a boot (refutation 2 above). So the window is real. What sits in it:

- **The sweep's write is a VERDICT about content it read.** Stamping a stale verdict produces
  a false alarm on a repaired schedule, and a false alarm is what teaches an operator to ignore
  the field. Fencing is the right control.
- **Reconcile's write is a VALUE computed from `cron_expr` and `timezone`.** Two replicas
  reconciling the same row write the same value within clock skew. There is nothing to be
  stale about between replicas.
- **A PATCH in the window is the one real hazard, and it EXISTS TODAY.** `UpdateScheduledJob`
  writes `next_run_at` itself, so a PATCH that changed the cron between the read and the write
  has its freshly computed `next_run_at` clobbered by one derived from the pre-patch cron.
  **Paging strictly improves this**, for the reason the boot-sweep spec gives about its own
  fence: today all N rows are read first and written in one loop, so the last row's read-to-write
  window spans the whole pass; under paging a row is read at the start of ITS page and written
  within it, so the maximum per-row window falls from O(N) to O(page).
- **Adding a fence would make it worse, not better.** A fenced non-match SKIPS the advance, and
  a skipped advance leaves the row overdue - which produces exactly the one spurious fire
  never-catch-up forbids. An unfenced write leaves a slightly-wrong future time, which self-heals
  within one fire because `fireOne` recomputes `sched.Next` from the row's current cron. Leaving
  the row overdue is the worse of the two outcomes, so the fence is refused on its merits.

CLAUDE.md's identity-checked-teardown rule says that where there is no identity to check, say
so and name what replaces it. What replaces it here is idempotence across replicas plus
self-healing on the next fire, and the residual clobber is bounded at one fire. Write that in
the statement's comment, not the argument for it.

The residual is pre-existing, is not this slice's to close, and is proposed as a backlog item
below so it is findable.

**Nothing about the transaction shape changes.** The reconcile stays pool-backed, one implicit
transaction per statement, as it is today and as the sweep is. A single transaction spanning
every page would give one snapshot, and would hold a pool connection and every row lock for the
whole unbounded pass, contending with the gRPC path that is already serving. Rejected.

### Decision 6: `ctx.Err()` in the row loop, returned

Add the same check `ValidateStoredSpecsOnStartup` has: at the top of the ROW loop, `return`
rather than `break`.

**Reconcile's exposure is strictly worse than the sweep's was**, which is the argument for
doing it here rather than deferring. The sweep issues a statement only for BROKEN rows, so a
cancelled sweep logs one line per remaining broken row. Reconcile issues
`AdvanceScheduledJobNextRun` for **every** row, so a SIGTERM mid-reconcile makes every
remaining row get `context canceled` back and log `reconcile advance for %s: context
canceled`. One line per remaining row, unconditionally.

Top of the row loop rather than the page loop, for the sweep's reason: one page is already up
to `reconcilePageSize` lines. Return rather than break, so `main`'s existing warning names the
cause once instead of reporting a clean pass.

**This changes the function's error contract.** Today `ReconcileOnStartup` returns only the
LIST error; every per-row failure is logged. After this slice it returns the page query's
error or the cancellation, and per-row `ParseSchedule` and UPDATE failures stay logged. The
header must say so.

## The guards, and the lane each runs in

CLAUDE.md's "A guard behind a build tag must be able to run" is applied in its stated order:
does it need the tag at all, then can it run in a lane CI runs, then write the reason down.

### Guard 1 (UNTAGGED): the narrowed column list

**`TestOverdueCatchupPageRowCarriesOnlyTheFourColumnsReconcileReads`**, an untagged test in
`internal/schedrunner`, package `schedrunner_test`, modelled on
`scheduled_job_surface_test.go` and reusing its `scheduledJobFieldSetDiff` helper.

It asserts the generated row struct's field set is exactly `{ID, Name, CronExpr, Timezone}`.

**Why it exists.** The narrowed column list IS the bound. Someone adding a validation step to
reconcile later, or "restoring consistency" with the sweep's `SELECT *`, brings the 1 MiB
`job_spec` term back with nothing going red. This guard is the thing that makes the narrowing
a property rather than a coincidence.

**Why untagged.** It is pure reflection over a compiled struct and needs no database, so
CLAUDE.md step 1 applies in its strongest form: it runs in `go test ./...`, which
`.github/workflows/go-ci.yml` runs on every commit. Same placement decision
`scheduled_job_surface_test.go`'s own header records for itself.

**Assert the SET, not a count.** `scheduled_job_surface_test.go` records why: an earlier
version of that guard checked a substring and was walked past by four plausible spellings; a
`NumField` count alone is satisfied by any four fields. The set assertion names what is
missing and what appeared, in both directions.

### Guard 2 (integration): the page loop

**`TestReconcileOnStartup_ReadsInPagesOfOneHundred`**, in
`internal/schedrunner/reconcile_paging_integration_test.go`, `//go:build integration`, package
`schedrunner_test`.

Mirrors `TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred` and the reasoning
transfers: plant **250** enabled OVERDUE rows for one owner (more than one page, more than two
pages, not a multiple of the page size so the last page is short); attach the existing
`sweepTracer` via `tracedPool`; assert **3** matching SELECTs against a literal, since
`reconcilePageSize` is unexported and this is an external test package; assert **all 250** rows
now have `next_run_at` in the future, which is the positive assertion without which a dropped
final page satisfies a statement count alone. RED at HEAD: 1 SELECT.

Run it under a `context.WithTimeout` of about 60 seconds and assert a nil error, so a cursor
that fails to advance fails as a named timeout rather than by consuming the package clock. A
hang is indistinguishable from infrastructure trouble.

**The statement matcher is the one thing that is NOT a copy.** `isSweepPageRead` matches
`FROM scheduled_jobs` + `WHERE enabled` + `ORDER BY id`, and the reconcile's new page read
satisfies all three - so reusing it unchanged makes each test count the other's statements.
The discriminator is `next_run_at <`, which the sweep's page read does not contain.

Two riders on that:

- **Do not key the counter on the statement name.** The sweep's own comment gives the reason
  and it applies here identically: the name is what this slice changes, so a name matcher
  reports zero before the change - which is also what a test that never reached the function
  reports, making a broken instrument and a real failure indistinguishable.
- **Generalizing `isSweepPageRead` edits a file two existing tests depend on.** If their
  bodies must change, that is a re-verification obligation and not a free refactor: state the
  test-file diff in the commit and re-run both. Preferring a second matcher function beside
  the first, leaving `isSweepPageRead` byte-identical, is the cheaper route and is the
  recommendation.

### Guard 3 (integration): termination against an unparseable cron

**`TestReconcileOnStartup_TerminatesWhenAStoredCronNoLongerParses`**. This is the guard that
matters most, because it is the one that goes RED against the drain loop of Decision 1 - the
design a reader is most likely to reach for - and the page-count guard does not.

Plant more than one page of overdue enabled rows, at least one carrying an unparseable
`cron_expr`. The paging test already plants rows with a raw `INSERT ... SELECT` over
`generate_series`, which bypasses `handleCreateScheduledJob`'s validation entirely - and that
is also the proof the input is reachable in production, since a migration or an older binary
writes through the same statement.

Run under a `context.WithTimeout` of about 60 seconds. Assert the call returns nil inside it,
and assert every PARSEABLE row advanced while the poisoned one did not. **Put the poisoned row
FIRST in id order if the fixture can arrange it; if it cannot** - `gen_random_uuid()` is random
and the paging test explicitly declines to control uuid ordering - then plant more than one
poisoned row so at least one lands early with high probability, and say in the test comment
that the position is probabilistic and why the assertion does not depend on it.

The bounded failure is the whole design here: without the deadline, the drain-loop mutant hangs
and reads as infrastructure trouble.

### The wiring guard needs no change

`cmd/relay-server/schedrunner_startup_wiring_test.go` is untagged, runs in `go test ./...`, and
already pins that `main` calls `ReconcileOnStartup` exactly once and not inside a func literal,
a `go` or a `defer`. This slice does not move any call in `main`, so the guard is unchanged and
must stay green. **Do not extend it to assert anything about `ListenAndServe` in this slice** -
that would pin a property this slice neither changes nor fixes, and a criterion green before the
change pins nothing.

## Prose this slice owes: delete or correct, site by site

Project rule is deletion-first for prose findings. A correction authors fresh claims, which is
what regenerated this class four times on one docstring, so each row below says which and why.

### Genuinely false today - fix these

| Site | The claim | Action |
| --- | --- | --- |
| `README.md`, "Startup sequence" list, items 6 and 7 | The list reads "6. Start the HTTP server (CLI / API traffic)" then "7. Reconcile scheduled jobs...". `main.go` calls `ReconcileOnStartup` and `ValidateStoredSpecsOnStartup` **above** the `srv.ListenAndServe()` goroutine. README tells an operator the HTTP server comes up first, which is the exact inversion of the property this item is about. The list also omits the startup validation sweep. | **CORRECT, by reordering.** Swap the two items so reconcile precedes the HTTP server, and name the sweep in the reconcile item. Not deleted: the never-catch-up fact is real information and the defect is ORDER, which is a checkable fact about `main.go` rather than a claim about a complement. **Keep the edit to these two list items.** Item 4 lumps the dispatcher, the LISTEN/NOTIFY trigger and the watchdog, which straddle reconcile in `main.go`; that imprecision is pre-existing and out of scope. Sibling lanes may also touch README. |
| `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md`, Context: "Every other read of `job_spec` in the tree is bounded: ... and `ListOverdueScheduledJobsForCatchup` is unbounded but filtered by `next_run_at < NOW()`, which newly created schedules do not satisfy." | The lead-in asserts every other read is bounded and the sentence's own third clause names one that is not. It also presents a filter as if it were a bound. | **DELETE the sentence.** A correction would author a fresh census over `job_spec` readers - a claim about the complement, pinned by nothing, and the exact shape that keeps regenerating. Nothing is lost: that item's Summary carries the accurate statement and this spec carries the enumeration. The item is still OPEN, so this is housekeeping on a live document rather than a rewrite of a closed record. |
| `internal/schedrunner/runner.go`, `ReconcileOnStartup` header: "advances `next_run_at` past any missed triggers for **every enabled schedule**" | The statement filters on `next_run_at < NOW()`, so it is every OVERDUE enabled schedule. Defensible as vacuous truth for a non-overdue row, and misleading enough that an implementer could reach for `WHERE enabled` alone - which is how a paging slice quietly turns a reconcile into a full-table pass. | **CORRECT.** The header is being rewritten for this slice anyway; name the actual predicate. |

### Required by the rename, if Decision 3's rename lands

| Site | Action |
| --- | --- |
| `internal/store/query/scheduled_jobs.sql`, `ListEnabledScheduledJobsPage`'s comment, which names `ListOverdueScheduledJobsForCatchup` in its "EVERY enabled schedule, not just the overdue ones" paragraph | **CORRECT** the name. The paragraph's substance stays true. |
| `internal/schedrunner/startup_validation_paging_integration_test.go`, `seedBrokenSchedules`'s comment, which names the statement | **CORRECT** the name. |

### True today, true after this slice - no edit, and checked so a reviewer need not re-check

| Site | Why no edit |
| --- | --- |
| `internal/schedrunner/startup_validation.go` lines about `sweepPageSize`, "peak memory and per-statement work are bounded by `sweepPageSize`", and the "Cost:" paragraph | All three are scoped to the SWEEP and are accurate. The paragraph they sit in already opens "THAT IS NARROWER THAN 'THIS SWEEP CANNOT STOP THE BOOT'". |
| `internal/schedrunner/startup_validation.go`, "IT IS NOT THE SAME QUESTION AS `ReconcileOnStartup`'S OWN `ParseSchedule` FAILURE ... the row stays overdue and `ListEligibleScheduledJobs` picks it up on the very next tick" | Still exactly true, and it becomes **load-bearing** for Decision 1's termination argument. The new comment should cite it rather than re-derive it. |
| `README.md`: "The server reconciles `next_run_at` on startup: any firings that fell during downtime are skipped (no catch-up), and the schedule resumes on its next eligible fire." | Preserved exactly by the no-remainder decision. **This is the tripwire sentence**: it is what goes false if any future slice adds a ceiling with a remainder. |
| `cmd/relay-server/main.go`, the one-line comment above the reconcile call, and the sweep's long placement comment ("AFTER `ReconcileOnStartup` only for cost, not for correctness") | Neither mentions memory or paging, and this slice moves nothing. The commute argument ("the sweep never reads or writes `next_run_at`, reconcile never reads or writes the failure columns") is unaffected by paging or by narrowing. |
| `ROADMAP.md` lines describing the sweep's paging and both open items | All sweep-scoped and accurate. The roadmap's own entries for this item restate its framing correctly. |
| `docs/superpowers/specs/2026-09-04-boot-sweep-keyset-paging.md`, `...-per-owner-schedule-cap.md`, their plans, and `docs/backlog/closed/feature-2026-09-04-per-owner-schedule-cap.md` | Dated records of a moment. All sweep-scoped and true; two of them already carry the "not true of the boot" correction verbatim. Specs are not rewritten. |
| `docs/backlog/feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep.md`: "Keyset paging bounded its peak MEMORY to one page" | "its" is the sweep's. Accurate. |

## Interaction with the concurrent lanes

**Slice 2 of this lane** (`feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep`) needs
three things from this spec and they are collected here so it does not have to re-derive them:

1. **The reconcile ends up PAGED TO EXHAUSTION with no deadline.** Its duration is unbounded
   in exactly the way the sweep's is. So after this slice there are **two** unbounded-duration
   passes ahead of the HTTP listener, not one.
2. **A deadline on the sweep is NOT the same decision as a deadline on the reconcile, and
   slice 2 must not silently absorb the second.** A truncated sweep under-reports a diagnostic,
   which is that item's stated opening constraint. A truncated reconcile leaves schedules
   overdue and produces one spurious fire each on the ticker, which falsifies README's
   never-catch-up sentence. Different hazard, different disclosure, different acceptance
   criterion. Slice 2 must say which of the two it bounds and must not write an acceptance
   criterion about "the boot".
3. **The option that actually closes the exposure, named and declined here.** Reconcile must
   complete before the RUNNER goroutine - otherwise the first tick fires every overdue schedule,
   which is precisely the catch-up the policy forbids, and
   `TestSchedrunnerStartupSweepIsWiredInOrderByMain` already pins the non-async placement. But
   it does **not** have to complete before `ListenAndServe`. Moving the reconcile, the sweep and
   the runner start below the HTTP goroutine takes the boot's schedule work off HTTP admission
   entirely, at the cost of the first seconds of uptime having no scheduler and of changing when
   a boot reports ready. **Declined for this slice** - it is the sibling item's third option, it
   is a behaviour change wanting its own slice, and it is not needed for the memory criterion.
   Slice 2 should weigh it against a deadline, because a deadline bounds the delay while
   reordering removes it.

**LANE S** (`source.sync` entry-count bound in `validateSourceSpec`): **no interaction with this
slice.** No shared file, and reconcile never calls `jobspec.Validate` - it advances a broken
schedule's `next_run_at` exactly as it advances a healthy one's, and the spec's brokenness
surfaces at fire time through `fireOne`. **There IS an interaction with slice 2**, on the
user-visible surface rather than on any file: LANE S's bound is retroactive over stored specs,
`ValidateStoredSpecsOnStartup` is what enforces it retroactively, and the sweep issues one
UPDATE per BROKEN row. So the release that lands LANE S is the release where the sweep is
slowest, which is the release a deadline is most likely to truncate, which is the release the
sweep exists to serve. Slice 2 must account for that coincidence rather than choosing a number
against a steady-state fleet.

**LANE W** and **LANE E**: no interaction. Note for the implementer that LANE E is fixing a
guard that is RED on a clean tree inside the `golang:1.26` container, so a red there may not be
this slice's.

## What this slice does NOT cover

- **It does not bound the boot's DURATION.** Paging converts an unbounded allocation into an
  unbounded duration, the same trade the sweep made. Do not let the item record "bounded"
  without that sentence.
- **It does not bound `ListGraceCandidates`**, the third unbounded pre-listener read. Named in
  the enumeration, proposed as an item.
- **It does not move any call in `cmd/relay-server/main.go`.** The boot ordering is unchanged
  and the wiring guard is unchanged.
- **It does not close the PATCH clobber window**, which is pre-existing, bounded at one fire,
  and improved from O(N) to O(page) as a side effect. Proposed as an item.
- **It does not add a fence to `AdvanceScheduledJobNextRun`.** Decision 5 refuses it on merits.
- **It does not touch `ListEligibleScheduledJobs`, `BatchLimit`, `TickOnce`, `sweepPageSize` or
  `ValidateStoredSpecsOnStartup`.**
- **It does not re-derive the item's third acceptance criterion as work.** Refutation 3.
- **No new index on `scheduled_jobs`.** The primary key index serves the ordering and the range;
  `enabled` and `next_run_at` are filters on top. On a table that is mostly not-overdue, each
  page scans past non-matching rows to fill itself, so the total index work across all pages is
  the same total as today's single scan, since each page resumes where the previous stopped.
  Paging makes it neither worse nor better. An index costs every write forever for a statement
  that runs once per boot.

## Files touched

| File | Change |
| --- | --- |
| `internal/store/query/scheduled_jobs.sql` | `ListOverdueScheduledJobsForCatchup` becomes `ListOverdueScheduledJobsForCatchupPage`: narrowed column list, `cursor_set` / `cursor_id` / `page_limit`, `ORDER BY id`, no `+ 1`. Comment cites `ListEnabledScheduledJobsPage` as the precedent and states only what differs - the mutated predicate, why the cursor is `id` and not `next_run_at`, why there is no fence. Plus the name fix in `ListEnabledScheduledJobsPage`'s own comment. |
| `internal/store/scheduled_jobs.sql.go` | regenerated by `make generate`. See implementation notes. |
| `internal/schedrunner/runner.go` | `reconcilePageSize` constant; the page loop; the `ctx.Err()` return; `now` hoisted above the loop; the header rewritten for the predicate, the error contract and the no-remainder policy. |
| `internal/schedrunner/reconcile_paging_integration_test.go` | new: guards 2 and 3. |
| `internal/schedrunner/scheduled_job_surface_test.go` (or a sibling untagged file) | guard 1, reusing `scheduledJobFieldSetDiff`. |
| `internal/schedrunner/startup_validation_paging_integration_test.go` | the statement-name fix in `seedBrokenSchedules`'s comment; a second matcher beside `isSweepPageRead` if that route is taken. Keep the two existing test bodies byte-identical. |
| `README.md` | "Startup sequence" list items 6 and 7 only. |
| `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md` | delete the one false sentence. |

## Implementation notes

**`make generate` on this CRLF repo.** Never edit `*.sql.go` or `models.go` directly. sqlc
emits LF and rewrites line endings across every generated file, so after generating: run
`git diff --ignore-all-space`, keep only the real content change, and revert the LF-only hunks
with `git checkout -- <file>`. Then verify the regenerated `.sql.go` actually survived the
revert - discarding it is a known outcome of that procedure and it fails silently, because the
build still compiles against the old function until something calls the new one.

**`git diff` and `git status` disagree by design here.** `core.autocrlf=true` normalizes LF
churn away in `git diff` while `git status` still lists the files as modified. Never conclude
"nothing to revert" from `git diff` alone. Before committing, check the diffstat against the
size of the change intended and run `git ls-files --eol` on every touched path - each should
read `i/lf`.

**The README edit is a programmatic edit to a tracked text file.** Print the before and after
line counts, prefer exact-anchor replacement over a rewrite of the section, and assert the file
still decodes as UTF-8 afterwards.

**Commit order.** The SQL rename plus `make generate` will not compile until `runner.go` is
updated in the same commit. Guard 1 (untagged, pure reflection) can only be RED against a tree
where the narrowed struct does not exist as a compile failure, which is not a RED - so write it
in the same commit as the narrowing and mutate it afterwards to prove it bites. Guards 2 and 3
both compile and run against HEAD and go red for real reasons, so they lead.

## Measurements deferred to plan Task 0

**Nothing in this spec was measured. This session had no Bash tool**, so there is no timing, no
`psql` session and no test run behind any statement here. Every figure is read from the tree.
The following must be measured or observed before the implementation relies on them:

1. **The generated SQL text pgx actually sees for both page statements.** Guard 2's matcher is a
   string predicate over it. sqlc prepends a `-- name:` header (the existing cancellation hook
   keys on `-- name: RecordScheduledJobFailure`, so the header is present in the traced SQL) -
   capture the real strings for both statements and pick the discriminator against them, rather
   than against the `.sql` source this spec read.
2. **The name sqlc emits for the narrowed row struct.** `ListOverdueScheduledJobsForCatchupPageRow`
   is the expected form and is not verified.
3. **Whether `pgx.TraceQueryEndData`'s `CommandTag.RowsAffected()` is populated for SELECT in the
   vendored pgx version.** If it is, guard 2 gains a stronger per-statement assertion (no matching
   statement returned more than `reconcilePageSize` rows). The boot-sweep spec flagged this as
   verify-before-relying and it was not resolved; do not write the test around it unverified.
4. **The test-file diff to `startup_validation_paging_integration_test.go`.** If the two existing
   tests' bodies change at all, re-run both and say so in the commit.
5. **A green baseline for `make test-pg-integration` in this worktree before any mutation.**
   Uniform results mean a broken harness, and LANE E is concurrently fixing a guard that is RED
   on a clean tree in the `golang:1.26` container.
6. **Only if the implementer wants to raise `reconcilePageSize` above 100**: a round-trip or
   wall-clock number for the boot at a realistic overdue count. Decision 4 declines the raise
   precisely because no such number exists yet.

## Backlog items this spec proposes, for the human to accept

None are filed by this spec.

1. **`ListGraceCandidates` is unbounded and runs before the HTTP listener.** The third
   unbounded pre-listener read, and the one no existing item names. Three narrow columns per
   row and a fleet-sized population, so it is a much smaller exposure than either schedule
   statement - which is the argument for filing it rather than folding it into one of them.
2. **`ReconcileOnStartup` can clobber a concurrent PATCH's `next_run_at`.** Pre-existing,
   reachable multi-replica, bounded at one fire, and improved from O(N) to O(page) by this
   slice without being closed. Decision 5 explains why a fence is the wrong remedy, so the item
   must not prescribe one.
3. **The boot is asymmetrically started: gRPC accepts while HTTP does not.** Refutation 2. A
   readiness question and a pool-contention question rather than a bug, and it changes how both
   boot-bounds items should be read.

## Open questions for the human

1. **Narrow AND page, or page only?** This spec recommends both, with guard 1 as the price of
   the narrowing. Paging alone satisfies the acceptance criterion.
2. **The README fix scope.** This spec reorders two list items and names the sweep. A fuller
   correction of item 4's lumping is available and is declined as out of scope.
3. **Whether to delete or correct the sibling item's false sentence.** Deletion is recommended
   on the project's deletion-first rule; correction is available if the enumeration is judged
   worth keeping in that item rather than only here.
