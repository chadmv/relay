# A wall-clock deadline on the startup validation sweep

- Date: 2026-09-10
- Backlog item: `docs/backlog/feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep.md`
- Subject: `schedrunner.ValidateStoredSpecsOnStartup` (`internal/schedrunner/startup_validation.go`),
  its call site in `cmd/relay-server/main.go`
- Status: design, awaiting review
- Lane: B, slice 2 of 2. Slice 1
  (`docs/superpowers/specs/2026-09-10-reconcile-on-startup-paging.md`) has merged.

## TL;DR

`ValidateStoredSpecsOnStartup` runs to exhaustion over every enabled schedule
before `srv.ListenAndServe()`. Paging bounded its peak memory to one page; the
per-owner cap bounds its starting work set. Its DURATION is bounded by nothing,
and its own header says so.

This slice gives it a budget, and the design work is not the budget - it is what
the boot says when the budget runs out.

1. **The budget is a parameter, not a context the caller builds.** The signature
   becomes `ValidateStoredSpecsOnStartup(ctx, q, budget) (SweepResult, error)`
   and the `context.WithTimeout` lives INSIDE the sweep. That is not a style
   preference: it is what stops a bounded context existing in `main`'s scope,
   where the next statement is `go schedrunner.NewRunner(pool, q).Run(ctx)` and
   passing the wrong one there kills the scheduler 45 seconds into every boot.
2. **The counts and their caveat travel in one value.** `SweepResult` carries
   `Checked`, `Invalid` and `Truncated` together, and one formatter owns all
   three boot-line shapes, so no caller can print a partial count without the
   sentence that says it is partial.
3. **`Truncated` is derived from the child context's `Err()`, never from the
   returned error.** A shutdown and a deadline both stop the pass; only the
   context can tell them apart, and the SIGTERM case must not claim the deadline
   fired.
4. **A zero or negative budget is an EXPIRED deadline, not an absent one.** No
   off token anywhere in this slice, at either layer.
5. **The remainder goes nowhere and the next boot starts from the beginning.**
   The tempting alternative - resume after the listener comes up - is refused on
   a fence gap, not on cost. See Decision 4.
6. **It bounds the SWEEP and not the boot.** `ReconcileOnStartup` is still
   unbounded ahead of the listener, and so is `ListGraceCandidates`. Saying
   otherwise would be the same lossy-aggregate defect one level up.

## What this spec refutes

The item was read once for content and once asking only whether it contradicts
itself, contradicts the tree, or prescribes something that does not exist. Six
findings. Two are against the item, two against the sibling spec, one against the
item's own acceptance criteria, and one against a table slice 1 wrote.

### 1. The item names only one reader of the sweep's result. There are four, and the one that matters most is a COUNT IN A UI

The item's Proposal says "Decide what the boot line says when the deadline
fires". The brief adds a second reader, `scheduled_jobs.last_error`. Both are
right and the set is incomplete. Readers of what this sweep writes:

| Reader | What truncation does to it |
| --- | --- |
| The boot log's per-row `startup validation recorded a new failure for schedule %s` lines | Fewer of them. Each remaining line is still true. |
| `scheduled_jobs.last_error`, rendered by the SPA, the CLI, the Python SDK and the MCP server | Nothing false appears. Some rows that SHOULD carry a record do not. |
| **`GET /v1/scheduled-jobs/stats`'s `failing` key**, documented in README as "Schedules in scope carrying a `last_error`. **Not windowed.**" | **This is a number in a UI strip and truncation makes it an under-count.** |
| README's prose about all three | Stale unless edited. |

`failing` is the literal instance of "a partial count presented as a total" that
the item's opening constraint is about, and the item does not know it exists.

**Two qualifications, so this is not overstated.** `failing` is a live
`count(*) WHERE last_error IS NOT NULL`, not the sweep's own number, and its
definition as written stays exactly true - it counts rows carrying a
`last_error`. And it already under-counts today for a reason that predates this
slice: a schedule that has never fired and whose spec has never been swept
carries no record. What this slice does is add a NEW cause of that under-count
and attach it to a knob an operator can turn. That is enough to owe it a
sentence; it is not enough to make fixing `failing` this slice's job. See
"Prose this slice owes" for the split.

### 2. The truncated sweep's loss lives in the ABSENCE of a record, never in the presence of one, and the item's second acceptance criterion cannot be met the way it is worded

> A truncated sweep says so wherever its result is read, and never presents a
> partial count as a total.

The first clause is not achievable and should not be claimed. To make
`scheduled_jobs.last_error` disclose per-row that a row was not checked, the
sweep would have to WRITE to every row it did not check - which is exactly the
unbounded work the deadline just declined to do. There is no per-row disclosure
that does not cost the thing being bounded.

What IS available is sharper than the criterion asks and should replace it:

> **The sweep only ever ADDS records; it has no clearing sibling
> (`RecordScheduledJobFailure`'s own comment says so). So truncation can produce
> a false NEGATIVE and can never produce a false POSITIVE. A `last_error` that is
> SET remains exactly as trustworthy after a truncated pass as after a complete
> one. Only the ABSENCE of one loses its meaning.**

That is a precise statement of the loss, it is checkable against the tree, and it
is what goes in the boot line and in README. The criterion is met in the sense
that matters and not in the sense it is written; say which.

### 3. The sibling spec's sentence about reordering is false, and the brief asks this slice to weigh it

`docs/superpowers/specs/2026-09-10-reconcile-on-startup-paging.md` says:

> Slice 2 should weigh it against a deadline, because a deadline bounds the delay
> while reordering removes it.

**Reordering does not remove the delay. It relocates it onto the scheduler.**
Moving the sweep and `go schedrunner.NewRunner(...).Run(ctx)` below the HTTP
goroutine takes the sweep off HTTP admission - and the runner still cannot start
until the sweep finishes, because `TestSchedrunnerStartupSweepIsWiredInOrderByMain`
property 3 is the sweep's entire safety argument (it takes no lock, and a fire
landing between its LIST and its UPDATE stamps a stale failure over a fresh
clear). So an unbounded sweep would then delay scheduled-job firing by an
unbounded amount instead of delaying HTTP by one.

That trade is worse in three ways and each is checkable:

- The delayed thing acquires a user-facing contract. "Your `@daily` job fires at
  03:00" is a promise; "the readiness probe is red for another 20 seconds" is not.
- The delay becomes silent. A failing readiness probe is an operator-visible
  signal. A scheduler that has not started yet produces no signal at all.
- **The readiness probe would go GREEN while a whole subsystem is not running.**
  That is a worse lie than a probe that stays red, and it is the shape this
  project calls a lossy aggregate: the health signal stops disclosing what it
  does not cover.

Reordering is therefore refused on merits, not deferred. A deadline bounds the
delay; nothing in this lane removes it.

### 4. A page ceiling is the wrong instrument - CONFIRMED, and the item's argument is weaker than the one available

The item says a page ceiling "bounds round trips, not seconds. One slow page
under load costs more wall clock than many fast ones." Confirmed, and the case is
structural rather than about load:

- `job_spec` is bounded only by `maxBodyBytes` at 1 MiB, so one page of 100 rows
  transfers anywhere from a few kilobytes to ~100 MiB.
- `jobspec.Validate`'s per-row CPU scales with the spec's contents, bounded by
  `maxCommandsPerJob` at 25,000 commands and `maxTasksPerJob` at 5000. One row
  can legitimately cost four orders of magnitude more than another.
- A page issues one UPDATE per BROKEN row, so a page's statement count varies
  from 1 to 101 before any latency is considered.

A count-based ceiling over a quantity whose per-unit cost varies by four orders
of magnitude bounds nothing in seconds. **And the stronger point the item misses:
a page ceiling has the identical under-reporting cost - it truncates, so it owes
the same disclosure - while buying strictly less protection.** It is not a
cheaper alternative that trades accuracy for simplicity; it is the same trade
with a worse exchange rate. Refuted as an alternative, confirmed as the item
framed it.

### 5. Slice 1's pre-listener table calls `store.Migrate` bounded. It is bounded in COUNT, not in DURATION, and duration is this slice's axis

That table reads:

> | `store.Migrate` (not a sqlc statement) | `main` | Yes - by the embedded migration file count, a compile-time constant | Out |

True as far as it goes and misleading on the axis this slice cares about. A
single `CREATE INDEX` or `ALTER TABLE` on a large `tasks` or `task_logs` runs for
minutes, and `applyStatementTimeout`'s own header records that
`RELAY_DB_STATEMENT_TIMEOUT` deliberately does NOT reach migrations
(`store.Migrate` opens its own connection before `pgxpool.ParseConfig` is
called). So migrations are the one pre-listener step with no duration bound at
all and no knob, by design.

Not this slice's to fix and not proposed as an item - a migration that must
finish before the schema is usable is a different thing from a diagnostic pass -
but it belongs in this spec's "does not bound" enumeration, because a reader who
takes slice 1's table at face value will conclude the boot has one unbounded step
left after this slice when it has three.

### 6. What the item gets right, checked

- The Related section names `internal/schedrunner/startup_validation.go`
  (`ValidateStoredSpecsOnStartup`) and that is correct against the tree. Slice
  1's item got the equivalent line wrong; this one does not.
- "Keyset paging bounded its peak MEMORY to one page" - accurate, and "its" is
  the sweep's.
- The cap-bounds-N0-not-N argument holds verbatim; `ListEnabledScheduledJobsPage`'s
  own comment now carries the same reasoning about INSERTs landing above the
  cursor.
- The severity framing is right: the operator's remedy
  (`DELETE /v1/scheduled-jobs/{id}`) is on the HTTP side, which is the side that
  never comes up.

## Design

### Decision 1: the budget is a parameter of the sweep, and the timeout context is built inside it

```go
func ValidateStoredSpecsOnStartup(ctx context.Context, q *store.Queries, budget time.Duration) (SweepResult, error)
```

with `ctx, cancel := context.WithTimeout(ctx, budget); defer cancel()` as the
first two lines of the body.

**The obvious alternative is to build the context in `main`, and it is refused
because of what sits two statements later.** `main`'s boot region reads:

```
ReconcileOnStartup(ctx, q)
ValidateStoredSpecsOnStartup(ctx, q)
go schedrunner.NewRunner(pool, q).Run(ctx)
go metrics.NewSweeper(...).Run(ctx)
go watchdog.Run(ctx)
go func() { srv.ListenAndServe() }()
```

Every one of those takes a `ctx` and every one of them is meant to live for the
process. A `sweepCtx` in that scope is a variable that looks exactly like `ctx`,
sits one line above five long-lived consumers, and kills whichever one receives
it 45 seconds into every boot. The failure would be a scheduler that stops after
45 seconds, on a healthy server, with no error.

**The encapsulation that would ALSO close this is forbidden by an existing
guard, and the guard is right.** Wrapping the call in a local helper
(`runStartupValidation(ctx, q, budget)`) hides the bounded context completely -
and `TestSchedrunnerStartupSweepIsWiredInOrderByMain` calls
`findPkgCalls(main.Body, "schedrunner", "ValidateStoredSpecsOnStartup")` and
requires exactly one hit inside `main`'s own statement list. A helper makes that
zero, and the guard fails with "Zero means a schedule broken by a retroactive
validation change stays invisible until its next fire". That guard exists
because the sweep's position in `main`'s statement order IS its correctness
argument; do not weaken it to buy encapsulation. Passing the budget as a value
gets the same encapsulation for free and leaves the guard untouched.

**Nothing else in `main` changes shape.** The call stays a top-level statement
between the reconcile and the runner goroutine, not inside a func literal, a `go`
or a `defer`. All four of the wiring guard's properties stay green and it needs
no edit.

### Decision 2: `SweepResult`, and why the truncation flag lives in the same value as the counts

```go
// SweepResult is what one ValidateStoredSpecsOnStartup pass observed.
type SweepResult struct {
	// Checked is how many enabled rows this pass validated.
	Checked int
	// Invalid is how many of those no longer validate. It counts VERDICTS, not
	// writes: a verdict whose UPDATE was refused by the content fence, or
	// errored, or was already recorded, is counted here and produced no log
	// line.
	Invalid int
	// Truncated means the budget ran out. When it is true, Checked and Invalid
	// are FLOORS over an enabled set whose size this pass never learned.
	Truncated bool
}
```

**The three fields travel together on purpose.** This project's rule is that a
lossy aggregate must disclose its loss where it is READ, not only where it is
computed, and the failure mode it was written for is a counter that was correct
while the line built from it lied. A caller that wanted to print `Checked`
without the caveat here would have to actively ignore an adjacent field of the
same struct. That is meaningfully stronger than a boolean the call site
reconstructs, and it is why the boot line's formatter takes the whole
`SweepResult` and never the individual counts.

**`Invalid` counts verdicts and not writes, and that asymmetry must be written
down** in the field comment and repeated nowhere else. `RecordScheduledJobFailure`
is `:execrows` and returns 0 for two different reasons already documented at its
call site - a fence non-match, or an identical message already stored - and both
are ordinary. A count of writes would read as "how many schedules are broken" and
be wrong for every schedule that was already broken yesterday.

**Why the counts are needed at all, rather than just `Truncated`.** Without
`Checked` the truncated line can say the pass stopped early but cannot say how
far it got, which is the only number that tells an operator whether the budget is
nearly enough or nowhere near. Without `Invalid`, a boot on which every broken
schedule was already recorded prints no per-row lines and reads identically to a
boot with nothing wrong.

**`Elapsed` is deliberately NOT a field.** `main` owns the clock and measures it
with `time.Since`; a duration in the struct would be a second source of truth for
something the caller already has.

### Decision 3: `Truncated` comes from the child context, never from the returned error

At every non-nil return:

```go
res.Truncated = errors.Is(ctx.Err(), context.DeadlineExceeded)
```

where `ctx` is the CHILD built in Decision 1.

**Three reasons, and the first is the one a reviewer must be able to check.**

- **A shutdown and a deadline both stop the pass and must not be conflated.** If
  the parent `signal.NotifyContext` is cancelled by SIGTERM, the child's `Err()`
  is `context.Canceled`. If the child's own timer fires, it is
  `context.DeadlineExceeded`. Deriving `Truncated` from `err != nil` - the
  likeliest wrong implementation - makes a mid-boot SIGTERM print a line
  advertising a deadline that did not fire and prescribing a knob that is not the
  problem. `TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation` is the
  guard that kills that mutant.
- **The returned error is not reliably introspectable.** The first thing the
  sweep does is a page query; if the budget is already spent, the error comes
  back from pgx, and whether it satisfies `errors.Is(err, context.DeadlineExceeded)`
  depends on pgx's wrapping in the vendored version. Asking the context is asking
  the system rather than parsing another component's prose. (Whether pgx does
  wrap is plan Task 0; the decision is correct either way, but the test's failure
  message should not assert something unverified.)
- **It must be set on EVERY error path, including the page query's.** A version
  that sets it only in the row-loop branch reports `Truncated: false` for the
  case where the budget expired before the first page returned, which is exactly
  the zero-budget case Decision 6 makes reachable.

**One residual, stated so it is not found later.** If the page query fails for a
genuine database reason at the same moment the budget expires, `Truncated` reads
true and the error names the fault. The line then attributes an incomplete pass
to the deadline when both were true. Acceptable: both statements are correct, and
the error text is printed alongside.

### Decision 4: the remainder goes NOWHERE, and the resume-after-the-listener option is refused on a fence gap

The item asks for a later pass, the ticker, or nowhere. The brief is right that
the sibling slice's rejection of "the ticker" does not transfer - `fireOne`
creates a job per row and the sweep fires nothing - so all three are re-examined
here on the sweep's own terms.

**The ticker: it already covers the remainder, on the timescale the sweep exists
to beat, which means it covers nothing.** `fireOne` validates every spec it fires
and records the failure via `AdvanceScheduledJobAfterFailure`. So an unswept
broken schedule does get a record - at its next fire. `ValidateStoredSpecsOnStartup`'s
own header states the whole reason it exists: "for `@daily` that is up to a day;
for `@monthly`, up to a month. The population most likely to be broken right now
is exactly the population of long-cadence schedules nobody has looked at
recently". Routing the remainder to the ticker is routing it back to the latency
the sweep was built to close. Not a null action, as it was for the reconcile, but
not a remedy either.

**A later pass, resumed from the cursor after `ListenAndServe`: REFUSED, and not
for cost.** It looks obviously right - the sweep is record-only, so continuing it
concurrently with HTTP costs one pool connection and some CPU. It is refused
because of what it would have to run concurrently with.

The continuation would have to run after `go schedrunner.NewRunner(...).Run(ctx)`.
`RecordScheduledJobFailure` fences on `job_spec`, `cron_expr` and `timezone`, and
the sweep's header argues that this fence, not the placement, is what makes the
sweep safe against another replica. **The fence does not cover the interleaving
that placement covers, and that is the whole finding.** A successful fire calls
`AdvanceScheduledJob`, which CLEARS `last_error` and touches none of the three
fenced columns. So a row read by the continuation, fired successfully by the
ticker, and then written by the continuation, matches the fence on all three
columns and has its fresh clear stamped back over with a stale verdict. That is
precisely the defect `TestSchedrunnerStartupSweepIsWiredInOrderByMain`'s property
3 exists to prevent, described in its own failure message.

So the continuation is not a placement question. It needs a fence the tree does
not have - one that also covers "was this row cleared since I read it", which
means `last_error_at` or a generation column, and that is a schema-and-statement
design of its own. **Proposed as a backlog item, explicitly NOT prescribing the
remedy**, since a bad fence here is worse than no continuation.

**Decision: nowhere. The next boot starts from the beginning of the enabled set,
not from where this one stopped, and the boot line says so.** Restarting from the
beginning rather than persisting a cursor is deliberate: a persisted cursor makes
the covered subset depend on boot history, so an operator could never answer
"was this schedule checked" without knowing every previous boot's stopping point.
Starting over means the covered subset is always a prefix of the same total
order, and raising the budget always strictly increases coverage.

### Decision 5: it bounds the SWEEP, not the boot, and the reconcile keeps no deadline of its own

After this slice there are still two unbounded passes ahead of the HTTP listener
plus one unbounded step before them. That is deliberate, and here is the argument
rather than an inheritance from slice 1.

**Bounding `ReconcileOnStartup` is a different decision with a user-facing cost,
not a second application of this one.** A truncated sweep loses a diagnostic. A
truncated reconcile leaves rows with `next_run_at` in the past; the runner starts,
`ListEligibleScheduledJobs` picks each of them up within `TickInterval`, and
`fireOne` fires each ONCE. That is one unscheduled job per unreconciled
schedule - an unscheduled backup run, an unscheduled publish - and it falsifies
README's never-catch-up sentence, which is a user-facing contract. Bounding the
reconcile therefore requires first deciding what happens to the rows it did not
reach, and every answer is a new mechanism (a boot-skip marker, a resumable
cursor, or an accepted policy change). That is its own spec.

**And the sweep is the term worth bounding first, on structure rather than on a
measurement this session did not take.** The two passes differ on both axes:

| | work set | per-row work |
| --- | --- | --- |
| reconcile | rows with `next_run_at < NOW()` | 4 narrow columns, one `ParseSchedule`, one UPDATE |
| sweep | **every enabled row** | full row including `job_spec` (up to 1 MiB), JSON unmarshal, `ParseSchedule`, full `jobspec.Validate`, one UPDATE if broken |

On a fleet that has been running, the overdue count is near zero because the
previous process was reconciling continuously, so the reconcile's work set is
near-empty while the sweep's is the whole enabled table. The reconcile becomes
the dominant term only after an outage long enough for a large fraction of
schedules to come due - a rarer boot, and one where the operator already knows
why it is slow. **This is a structural argument about work sets and per-row work.
It is not a measurement and must not be restated as one.** Plan Task 0 gets the
numbers.

### Decision 6: no off token at either layer, and a zero budget is an EXPIRED deadline

Two independent controls, and the failure directions differ.

**In the library.** `context.WithTimeout(ctx, 0)` returns an already-expired
context with no special case, so a zero or negative budget makes the pass check
nothing and report `Truncated: true, Checked: 0` - loudly, in the boot line.
**Do not add a `budget <= 0 means unbounded` branch.** That would restore the
unbounded boot from a zero value, which is the same failure shape this project
already documents twice: an epoch-fenced query called with a zero-value epoch,
and `cursor_set` versus a zero-uuid seed. The header must carry that sentence,
because "unbounded" is the branch a later reader will think is the kind one.

**In `main`.** `parseStartupValidationDeadline` follows `parseScheduleCap`'s
shape exactly, ported to a duration:

- Unset, or a valid positive Go duration: used as-is, silently.
- Zero, negative or unparseable: the default is used and the message names the
  ignored value.

**No zero-disables arm, and this diverges from `parseWatchdogDuration` next
door.** That one accepts `0` because disabling a control that TERMINATES A USER'S
WORK is a legitimate operational choice with real consequences either way. This
control terminates nothing; the only thing an off switch buys is the unbounded
boot the slice exists to close, and an operator who genuinely wants a complete
sweep at any cost writes `24h`, which stays visible as a number in the
environment and in the boot line. That is `parseScheduleCap`'s own argument and
it applies here without modification.

**No floor arm either, and this diverges from `parseTrailingLogWindow` and
`parseWatchdogDuration`.** Both of those keep a too-small value and warn, because
their fail-aggressive direction is SILENT: a rejected log chunk produces no error
and no line, and an aggressive watchdog destroys work. Here the fail-aggressive
direction announces itself - a 1-second budget prints the truncation line on
every boot, naming the budget and the counts. A floor would be a constant nobody
can justify guarding a failure mode that is already loud. **The distinguishing
question is "is the failure silent", not "is the value small"**; write that in
the parser's header so the next knob is reasoned about the same way.

### Decision 7: `RELAY_STARTUP_VALIDATION_DEADLINE`, default `45s`

**The name** matches the vocabulary already in the log. Every line this pass
emits begins `schedrunner: startup validation`, so that is the phrase an operator
greps after seeing one. `RELAY_SCHEDULE_VALIDATION_DEADLINE` was considered and
rejected for that reason alone.

**The default is chosen against three constraints and none of them is a
measurement.**

- **It must be short enough that an orchestrator's startup probe does not restart
  the process.** A boot slow enough to be killed turns an incomplete diagnostic
  into a crash loop, which is strictly worse than the thing being avoided. The
  operator knows their own probe budget; the knob exists for that.
- **It must be long enough that a healthy fleet is never truncated**, so the
  truncation line means something when it appears. The pass is one round trip per
  100 rows plus CPU, with an UPDATE only for broken rows.
- **It must be visibly independent of `RELAY_DB_STATEMENT_TIMEOUT`, whose default
  is 30s.** This is the load-bearing one. A budget at or below the per-statement
  bound means one slow statement can consume the entire pass, and - worse - two
  knobs sharing a number read as coupled and get maintained as if they were. That
  is the exact reasoning `maxTimeoutSeconds` records for sitting deliberately
  above `RELAY_TASK_MAX_ASSIGNMENT`'s default. 45s sits above 30s, so a pass that
  loses one whole statement to the statement timeout still has budget to make
  progress, and the numbers do not look derived from each other.

For scale within the same file: `main`'s entire shutdown budget is 15s (5s for
gRPC `GracefulStop`, 10s for `srv.Shutdown`). A 45s boot-side budget is three
times that and of the same order.

**This is an operational timeout, and `internal/jobspec`'s "DO NOT MAKE THIS
ENV-CONFIGURABLE" does NOT apply.** That paragraph's argument is specific and
worth restating so the distinction is checkable: `jobspec.Validate` runs over
STORED `scheduled_jobs.job_spec` rows on four enumerated paths, so a
per-deployment bound would make the same stored row valid on one server and
invalid on another, and tightening one retroactively invalidates data already
accepted. None of that is true here. This value governs how long a process waits
before continuing to boot; it is evaluated once, at startup, in one process; it
changes no verdict about any stored row; and its right value depends on the
operator's fleet size, their database, and their probe configuration - which is
the project's stated test for a configurable operational timeout, alongside
`RELAY_EVICTION_TIMEOUT`, `RELAY_TASK_MAX_ASSIGNMENT` and
`RELAY_DB_STATEMENT_TIMEOUT`. **The category is operational timeout, and the
argument that puts it there is "does this decide anything about stored data".**

**Note the upgrade consequence and put it in the release notes.** A deployment
whose sweep currently takes longer than 45s boots identically today and starts
truncating after this deploy. The truncation line is how the operator learns.
That is the intended behaviour and it is still a behaviour change.

### Decision 8: what the boot log says - the exact lines

Two `log.Print` statements. `main` gains no `if err != nil` branch at all: one
formatter owns all three outcomes, which is what makes it impossible to print a
count without its caveat.

```go
startupValidationDeadline, warn := parseStartupValidationDeadline(
	"RELAY_STARTUP_VALIDATION_DEADLINE", os.Getenv("RELAY_STARTUP_VALIDATION_DEADLINE"))
if warn != "" {
	log.Printf("WARNING: %s", warn)
}
log.Print(startupValidationDeadlineLine(startupValidationDeadline))

start := time.Now()
res, err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q, startupValidationDeadline)
log.Print(startupValidationLine(res, err, startupValidationDeadline, time.Since(start)))
```

**Both the parse and its line sit here, in the boot region, rather than up with
the other `parse*` calls.** Two reasons: this parser cannot fatal, so there is no
fail-early benefit to hoisting it; and the bounds line's entire value is its
ADJACENCY to the pass it bounds. The boot log between the dispatcher and
`HTTP listening on` is otherwise silent - the reconcile prints nothing - so an
operator watching a slow boot sees nothing at all during the window this knob
governs. One line immediately before it says both what is running and how long it
can last.

#### The bounds line, printed unconditionally on every boot

```
schedrunner: startup validation bounded at 45s (RELAY_STARTUP_VALIDATION_DEADLINE). A pass that runs out of budget checks only part of the enabled set; the line after it says which.
```

#### The completed case

```
schedrunner: startup validation completed: 1432 enabled schedules checked in 4.1s, 3 of which no longer validate.
```

Empty table, so the zero case does not read as a broken instrument:

```
schedrunner: startup validation completed: no enabled schedules to check.
```

`completed` is the word, not `all`. The pass reads a moving table one page at a
time and `ListEnabledScheduledJobsPage`'s comment enumerates which concurrent
writes it can miss; "all" would overclaim on a boundary this project has already
written down.

#### The truncated case

```
schedrunner: startup validation STOPPED AT ITS 45s DEADLINE after checking 901 enabled schedules in 45.0s, 3 of which no longer validate. THESE ARE FLOORS, NOT TOTALS: the rest of the enabled set was not checked on this boot and its size is unknown, so a schedule carrying no recorded failure may simply never have been looked at. Recorded failures are still trustworthy - this pass only ever adds them, never clears them. To get a complete pass, first reduce the enabled set (RELAY_MAX_SCHEDULES_PER_OWNER bounds it per owner, not per fleet); raising RELAY_STARTUP_VALIDATION_DEADLINE also works and costs exactly that much more boot time before the HTTP API answers. The next boot starts again from the beginning of the set, not from here.
```

Six things it does, each of them required by something:

1. Names the bound and the elapsed time, so the operator can see how close the
   budget was.
2. Gives both counts and labels them floors in the same sentence.
3. States that the unchecked remainder's SIZE is unknown - not zero, not
   estimated.
4. States the absence-versus-presence asymmetry from refutation 2, which is the
   only thing that keeps `last_error` readable after a truncated boot.
5. **Orders the remedy ladder tightening-first, and it is ordered that way for a
   reason.** The quantity that drives truncation is the enabled schedule count,
   which any authenticated user grows - bounded per owner by
   `RELAY_MAX_SCHEDULES_PER_OWNER`, and the owner population is itself unbounded
   under `RELAY_ALLOW_SELF_REGISTER`. So "raise the deadline" is a remedy that
   WIDENS the boot delay a hostile or merely careless population can drive. This
   project's rule is to ask what a peer who can move a signal gains and whether
   the documented remedy is in their favour; the answer here is that the
   loosening remedy is, so it goes second with its cost stated inline. **And
   there is no disabling option anywhere in the ladder**, because there is no off
   token to offer.
6. States where the remainder went (nowhere) and what the next boot does.

#### The incomplete case - shutdown, or a page-query fault

```
warn: schedrunner startup validation DID NOT COMPLETE after checking 901 enabled schedules, 3 of which no longer validate (floors, not totals - the rest of the enabled set was not checked): context canceled
```

**One shape for both**, rather than a third and fourth vocabulary. A mid-boot
SIGTERM means the process is exiting and nobody will read that boot's
`last_error`; a page-query fault means the process IS continuing with partial
coverage and the operator must know. The shared shape costs nothing on the first
and is required by the second. It carries the same floor framing and does not
mention the deadline, because the deadline is not why it stopped.

**This replaces the existing `if err != nil { log.Printf("warn: schedrunner
startup validation: %v", err) }`.** The old line is strictly weaker: it printed
the error with no coverage information at all.

## The guards, and the lane each runs in

CLAUDE.md's order is applied: does it need the tag, can it run in a lane CI runs,
otherwise write the reason down.

### Untagged, in `cmd/relay-server` - these run in `go test ./...` on every commit

**G1. `TestParseStartupValidationDeadline`**, in
`cmd/relay-server/startupvalidation_config_test.go`, modelled on
`TestParseScheduleCap` and `TestParseWatchdogDuration`.

One case per arm. **The load-bearing arm is zero and negative**: assert the
returned duration is the default AND that the warning names the ignored value.
That arm is what goes RED against the mutant that treats `0` as "no deadline",
which is the mutation that silently restores the unbounded boot. The unset arm
must assert an empty warning, so a parser that warns on the ordinary path (boot
noise forever) is caught.

**G2. `TestStartupValidationLine_ATruncatedPassNeverReadsAsATotal`**, same file.
A pure function over `SweepResult`, so no database and no tag.

Assertions on the truncated shape: it contains the checked count; it contains the
word the completed shape does not (`STOPPED AT ITS`); it does NOT contain
`completed`; it states the remainder is unknown; it names
`RELAY_STARTUP_VALIDATION_DEADLINE`; and it does not contain any string offering
to disable the bound.

**The mutation this must kill** is a formatter that falls through to the
completed string for a truncated result, which is the single change that
re-creates the item's headline defect. Assert against the presence and absence of
words that differ between the two shapes, not against the whole line, so
rewording either one does not produce a false alarm.

**Choose fixture counts no other path can produce** - `Checked: 901, Invalid: 3` -
so the assertion cannot pass because the value it matched came from somewhere
else. Do not use `0`, `1` or the default duration's digits as a count.

### Integration, in `internal/schedrunner` - `make test-pg-integration`, run by CI's `pg-integration` job

New file `internal/schedrunner/startup_validation_deadline_integration_test.go`,
`//go:build integration`, package `schedrunner_test`, reusing `newRunnerHarness`,
`seedBrokenSchedules`, `countRecordedFailures`, `sweepTracer`, `tracedPool` and
`captureLog` from the existing files.

**Why these need the tag**: the property is about a pass that issues real
statements against real rows, and the sweep's first action is a page query, so
there is no seam that reaches the deadline logic without a database. Step 1 of
CLAUDE.md's ladder does not apply; step 2 does, and `internal/schedrunner` is
already wired to `pgdsn` and already named in `test-pg-integration`'s package
list, so no Makefile or workflow edit is needed.

**G3. `TestValidateStoredSpecsOnStartup_AnExpiredBudgetChecksNothingRatherThanRunningUnbounded`.**
Plant 3 broken rows, call with `budget = 0`. Assert `res.Truncated` is true,
`res.Checked == 0`, `res.Invalid == 0`, the error is non-nil, and
`countRecordedFailures == 0`.

**This is the guard for Decision 6 and its discriminating input is the budget
itself.** Against the `budget <= 0 means unbounded` mutant the recorded-failure
count is 3, not 0. Fully deterministic - no timing, no sleep.

**G4. `TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation`.** Reuse the
existing cancellation test's instrument exactly: `tr.setOnEnd` fires on the first
`RecordScheduledJobFailure` end and calls `cancel()` on the PARENT, with a
generous budget (60s) that cannot fire. Assert `errors.Is(err, context.Canceled)`
AND `res.Truncated == false`, plus `countRecordedFailures == 1` for anti-vacuity.

**This is the guard for Decision 3 and it kills the likeliest wrong
implementation** - `Truncated: err != nil`. It is also the reason the existing
`TestValidateStoredSpecsOnStartup_ACancelledSweepReturnsInsteadOfLoggingEveryRow`
is not simply extended: that test's subject is the log volume, and a second
subject in one test makes a red ambiguous.

**G5. `TestValidateStoredSpecsOnStartup_ADeadlineMidPassReportsPartialCountsAsFloors`.**
Plant more than one page of broken rows. Give a budget the first page comfortably
fits inside, and make `tr.setOnEnd` sleep once, past the remaining budget, on the
first `RecordScheduledJobFailure`. The hook runs on the caller's goroutine after
the statement completes, so the row loop's next `ctx.Err()` check is the very
next thing to run and it sees an expired deadline. No polling and no second
goroutine, exactly as the existing cancellation test arranges its own timing.

Assert `res.Truncated == true`, `errors.Is(err, context.DeadlineExceeded)`,
`0 < res.Checked < planted`, and `countRecordedFailures(t, h) == res.Invalid`.

**The `Checked` assertion is a range and not an equality, deliberately, and it
has exact siblings on both sides.** An exact `1` would go red on a machine where
the page read itself outlives the budget, and a flaky guard for a real property
gets deleted. G3 pins `Checked == 0` exactly at one end and G6 pins the full
count exactly at the other, so the loosened middle assertion is bracketed. Give
the `Checked == 0` failure its own message naming a too-tight budget for that
machine, so the two causes are distinguishable.

**G6. `TestValidateStoredSpecsOnStartup_CountsCheckedAndInvalidSeparately`.**
Plant a MIXED fixture - 5 enabled rows whose specs validate, 3 that do not - and
a generous budget. Assert `res.Checked == 8`, `res.Invalid == 3`,
`res.Truncated == false`.

**The mix is the whole point and the existing fixture cannot substitute.**
`seedBrokenSchedules` plants only broken rows, so
`TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred`'s 250 rows give
`Checked == Invalid == 250` and a mutant that returns `Checked` for both fields
survives. A degenerate fixture value is a guard that cannot fail. This needs a
`seedHealthySchedules` helper alongside the existing one.

### The existing tests' call sites

The signature change touches seven call sites, all in the integration lane:
`startup_validation_integration_test.go:94`,
`startup_validation_fence_integration_test.go:130, 194, 214`,
`startup_validation_paging_integration_test.go:179, 226, 278`.

All seven are mechanical (`require.NoError(t, f(ctx, q))` becomes a two-value
call with a generous budget). **They must be re-run, not merely re-read**: a
production change that is a no-op for a fixture still breaks fixtures, and a test
that goes vacuous under an edit is invisible to diff review. Extend
`...ReadsInPagesOfOneHundred` with `res.Checked == 250` and `!res.Truncated`
while it is being edited; do not extend the fence tests, whose subject is
unrelated.

### The wiring guard needs no change, and must not be extended

`cmd/relay-server/schedrunner_startup_wiring_test.go` stays byte-identical and
stays green. This slice moves no call in `main` and introduces no async
placement. Do not add an assertion about `ListenAndServe` or about the new
statements: a criterion that is green before the change pins nothing.

## Prose this slice owes: correct or delete, site by site

Deletion-first, and each row says which and why.

### Required, inside the scope fence

| Site | Action |
| --- | --- |
| `README.md` "Startup sequence", item 5 | **CORRECT.** Currently: "Reconcile scheduled jobs ..., then re-validate every enabled schedule's stored spec and record the ones that no longer validate, then start the scheduler polling loop. All three run before the HTTP listener." Add that the re-validation pass is bounded by `RELAY_STARTUP_VALIDATION_DEADLINE` (default 45s) and says so in the log when it stops early - **and that the reconcile is NOT bounded**, so a long outage can still delay the HTTP listener by an amount that grows with the number of overdue schedules. Slice 1's standard: name what is still unbounded in the same breath. |
| `README.md` server env-var table | **ADD** a row for `RELAY_STARTUP_VALIDATION_DEADLINE`, default `45s`, naming the truncation consequence, that zero/negative/unparseable falls back to the default, that there is no value that disables it, and that it should be read together with `RELAY_DB_STATEMENT_TIMEOUT`. |
| `internal/schedrunner/startup_validation.go`, the sweep's header paragraph beginning "THAT IS NARROWER THAN..." | **CORRECT.** It currently says "THE SWEEP'S TOTAL WALL CLOCK IS STILL PROPORTIONAL TO THE NUMBER OF ENABLED SCHEDULES, and nothing here bounds that number" and closes by pointing at this backlog item. Both halves become false. Replace with the honest bound: **the pass is bounded at `budget` plus one row's work** - the `ctx.Err()` check is at the top of the ROW loop, so the last row's `jobspec.Validate` (up to `maxCommandsPerJob` commands) and its `RecordScheduledJobFailure` round trip run past the deadline. That residual does not depend on the number of stored schedules, which is what the item's first acceptance criterion asks for; stating it as exactly `budget` would be the overclaim. Delete the pointer to the backlog item. |
| `internal/schedrunner/startup_validation.go`, sweep header, new paragraph | **ADD** the record-only asymmetry (truncation produces false negatives and never false positives) and the zero-budget rule from Decision 6, both as constraints the code cannot show. |
| `internal/schedrunner/runner.go`, `ReconcileOnStartup` header, "THE DURATION IS BOUNDED BY NOTHING ... the same trade `ValidateStoredSpecsOnStartup` makes" | **CORRECT** the trailing clause only. After this slice the sweep no longer makes that trade and the sentence becomes a false comparison. The reconcile's own "bounded by nothing" stays true and stays. This is the changing-a-global-property case: the claim went stale in a file this slice otherwise does not touch. |

### Required, and it crosses the section named in the scope fence - flagged for the conductor

Acceptance criterion 2 is about the readers, and refutation 1 found that the only
NUMERIC reader of this sweep's output lives in README's scheduled-jobs section,
not its startup section. Editing only the startup section would document the
mechanism and not its coverage.

| Site | Action |
| --- | --- |
| `README.md`, the `/v1/scheduled-jobs/stats` field table, `failing` row: "Schedules in scope carrying a `last_error`. **Not windowed.**" | **CORRECT** by adding one sentence: `failing` is a floor when the boot's validation sweep was truncated, because a schedule that was never checked carries no record. Do not rewrite the definition - it is exactly true as written, and rewriting it would author a fresh claim about a complement. |
| `README.md`, the `last_error` / `last_job_status` explanation around "**When a schedule reports a failure:**" | **ADD** one sentence stating the asymmetry: a `last_error` that is present is trustworthy; its absence means "no failure has been recorded", which after a truncated boot sweep does not mean "this spec validates". |

**Cut line for the conductor.** If a sibling lane is editing README's
scheduled-jobs section, the startup-section row and the env-var row are the
minimum this slice cannot ship without, and these two become a backlog item
titled after the `failing` floor. Say which was done in the commit; do not ship
the pair silently reduced to one.

### True today and after - checked, so a reviewer need not re-check

| Site | Why no edit |
| --- | --- |
| `internal/schedrunner/startup_validation.go`, `sweepPageSize`'s three paragraphs | All scoped to page size and peak bytes. A duration bound changes none of them, including "A CONSTANT, NOT AN ENV VAR" - that argument is about how many rows to hold at once, which is still not the operator's information. **Note the adjacency**: this slice adds an env-configurable knob in the same file as a comment refusing one, so the new parameter's header must say why the two answers differ (Decision 7), or the file reads as self-contradictory. |
| `RecordScheduledJobFailure`'s SQL header, "RECORD-ONLY: there is no clearing sibling for the sweep" | Load-bearing for refutation 2 and for Decision 4. Cite it; do not re-derive it. |
| `cmd/relay-server/main.go`, the sweep's long placement comment | Placement is unchanged. The "AFTER `ReconcileOnStartup` only for cost, not for correctness" argument is unaffected by a budget. |
| `README.md`'s never-catch-up sentence | Untouched: this slice does not bound the reconcile. It is the tripwire for anyone who later does. |
| `internal/jobspec/jobspec.go`'s "DO NOT MAKE THIS ENV-CONFIGURABLE" paragraphs | Correct and unaffected. Decision 7 distinguishes rather than contradicts. |
| `docs/superpowers/specs/2026-09-10-reconcile-on-startup-paging.md` | A dated record. Refutations 3 and 5 above are recorded here, not edited there. |

## What this slice does NOT bound

The standard is slice 1's. After this lands, still unbounded ahead of
`srv.ListenAndServe()`:

- **`ReconcileOnStartup`.** Paged and exhaustive, one round trip per 100 overdue
  rows plus one UPDATE per row, no deadline. Decision 5 argues why.
- **`ListGraceCandidates`** (`seedGraceTimersFromActiveTasks`). `SELECT DISTINCT`
  with no LIMIT. Named by slice 1's enumeration, still open, and it runs even
  earlier in the boot.
- **`store.Migrate`.** Bounded in COUNT by the embedded file list and in DURATION
  by nothing, and `applyStatementTimeout`'s header records that
  `RELAY_DB_STATEMENT_TIMEOUT` deliberately does not reach it. Refutation 5.
- **The sweep's own last row.** The bound is `budget` plus one row's work plus
  cancellation propagation, not `budget`. That residual is a per-row constant and
  does not grow with the number of schedules, which is what the acceptance
  criterion asks; it is still not zero.
- **The gRPC side is already serving throughout.** `grpcSrv.Serve` and
  `dispatcher.Run` start above all of this, so agents connect, register and
  receive work for the entire window while a readiness probe on `:8080` fails.
  This slice bounds HTTP admission specifically. The asymmetry is slice 1's
  finding and is proposed as its own item there.

And behaviourally:

- It does not fix `GET /v1/scheduled-jobs/stats`'s `failing` under-count; it
  documents it.
- It does not add any per-row disclosure to `scheduled_jobs`, and refutation 2
  argues that none is available at a price worth paying.
- It does not persist a cursor across boots. Decision 4.
- It does not move any call in `main`, touch the wiring guard, add a migration,
  or change any SQL statement. **No migration is needed by this slice**, so
  nothing here can collide with the sibling lane's `000024`.
- It does not touch `internal/worker/`, `internal/agent/source/perforce/`, or
  `internal/store/query/worker_workspaces.sql`.

## Files touched

| File | Change |
| --- | --- |
| `internal/schedrunner/startup_validation.go` | `SweepResult` type; signature gains `budget time.Duration` and returns `(SweepResult, error)`; `context.WithTimeout` + `defer cancel()` at the top; counters in the loop; `Truncated` from the child `ctx.Err()` on every error path; header corrections per "Prose this slice owes". |
| `internal/schedrunner/runner.go` | One clause in `ReconcileOnStartup`'s header. No code change. |
| `cmd/relay-server/startupvalidation_config.go` | **New.** `defaultStartupValidationDeadline`, `parseStartupValidationDeadline`, `startupValidationDeadlineLine`, `startupValidationLine`. |
| `cmd/relay-server/startupvalidation_config_test.go` | **New, untagged.** G1 and G2. |
| `cmd/relay-server/main.go` | Boot region only: the parse, the two `log.Print`s, `time.Since`, the new call shape, and removal of the `if err != nil` branch. |
| `internal/schedrunner/startup_validation_deadline_integration_test.go` | **New.** G3, G4, G5, G6, plus `seedHealthySchedules`. |
| `internal/schedrunner/startup_validation_integration_test.go`, `..._fence_integration_test.go`, `..._paging_integration_test.go` | Seven mechanical call-site edits; two added assertions in the paging test. |
| `README.md` | Startup sequence item 5; env-var table row; and the two scheduled-jobs-section sentences flagged above. |

No `.sql` file, no `make generate`, no migration.

## Implementation notes

**No `make generate`, so the CRLF/sqlc hazard does not apply to this slice.** The
CRLF hazards that DO apply are the ones for any programmatic edit to a tracked
text file: README is edited programmatically, so print the before and after line
counts, prefer exact-anchor replacement over a section rewrite, check the
diffstat against the size of the change intended, run `git ls-files --eol` on
every touched path (each should read `i/lf`), and assert the file still decodes
as UTF-8. Never conclude "nothing to revert" from `git diff` alone -
`core.autocrlf=true` makes it disagree with `git status` by design.

**Commit order.** The signature change does not compile until all eight call
sites move, so `startup_validation.go`, `main.go` and the seven test call sites
are one commit. G1 and G2 are pure functions over new symbols, so they cannot be
RED against a tree where those symbols do not exist - write them in the same
commit as the formatter and mutate afterwards to prove they bite. G3 through G6
name a parameter that does not exist at HEAD, so the same applies; the RED that
matters for all six is the post-hoc mutation, not a pre-change run. **Record each
mutation and its kill in the commit message, and name the guard that went red for
each** - a mutation can redden a test for a different guard than the one intended.

**Do not mutate the shared worktree** while sibling lanes read it. Mutation runs
go in an isolated copy.

**Restoring a mutated file: never `git checkout --`.** The guard under test is
uncommitted at that point and `checkout` discards it. Restore from a copy taken
before the mutation, then re-run a control that should die.

## Measurements deferred to plan Task 0

**Nothing in this spec was measured. This session had no Bash tool**, so there is
no timing, no `psql` session and no test run behind any statement here. Every
figure is read from the tree, and the two structural arguments (Decision 5's
relative cost, Decision 7's default) are arguments about work sets and per-row
work, not measurements. Before the implementation relies on them:

1. **Sweep throughput in two regimes**, at a realistic `job_spec` size: all rows
   healthy (page reads plus CPU only) and all rows broken (one UPDATE per row).
   Report rows per second for each, and how many rows 45s covers. **Report the
   INPUT alongside the number** - row count, spec size, whether the database is a
   container on the same host - because a bare figure reads as the typical case.
   If 45s does not cover a plausible fleet, the default moves and the commit says
   so.
2. **Whether pgx's error for a query whose context expired satisfies
   `errors.Is(err, context.DeadlineExceeded)`** in the vendored version. The
   design does not depend on it (Decision 3 asks the context), but G5's assertion
   on the returned error does.
3. **That `context.WithTimeout(ctx, 0)` produces a prompt error from the first
   page query rather than a hang or a panic**, which G3 depends on entirely.
4. **The `tr.setOnEnd` sleep timing for G5**: confirm the hook runs on the
   caller's goroutine after the `RecordScheduledJobFailure` Exec completes (the
   existing cancellation test asserts this shape, so it is strongly evidenced but
   not for a sleeping hook), and pick a budget and a sleep with margin on the
   slowest lane. Give the `Checked == 0` outcome its own failure message.
5. **A green baseline for `make test-pg-integration` in this worktree before any
   mutation.** Uniform results mean a broken harness, and a compile error is not
   a kill.
6. **Whether any sibling lane is editing README's scheduled-jobs section**, which
   decides the cut line above. Check at the moment of the edit, not at plan time -
   in a concurrent batch the complement moves while you count it.
7. **The exact wording of the seven existing call sites after the edit**, and
   whether any of them goes vacuous. Re-run the lane rather than reading the
   diff.

## Backlog items this spec proposes, for the human to accept

None are filed by this spec.

1. **A resumable startup validation pass needs a fence that covers a concurrent
   CLEAR.** Decision 4's finding: `RecordScheduledJobFailure` fences on
   `job_spec`, `cron_expr` and `timezone`, and `AdvanceScheduledJob` clears
   `last_error` without touching any of them, so a sweep continuing after the
   runner starts can stamp a stale verdict over a fresh clear. **The item must
   not prescribe the fence** - `last_error_at` and a generation column are both
   candidates and the wrong one is worse than no continuation. This is the item
   any future "resume the truncated sweep" work is blocked on, and filing it is
   what makes Decision 4's refusal falsifiable.
2. **`GET /v1/scheduled-jobs/stats`'s `failing` is a floor and nothing on the
   wire says so.** It under-counts for three independent reasons - a never-fired
   never-swept schedule, a truncated boot sweep, and the sweep's own fence
   non-matches. The remedy is a disclosure on the response or in the SPA strip,
   not a change to the count. Owned by `internal/api` and `web/`, outside this
   lane.
3. **A deadline on `ReconcileOnStartup` needs a policy for the rows it does not
   reach.** Decision 5. The item's design question is "what happens to
   unreconciled schedules", not "what number" - each unreconciled row costs one
   unscheduled fire on the ticker, which falsifies README's never-catch-up
   contract.
4. **`store.Migrate` has no duration bound and deliberately no statement
   timeout.** Refutation 5. Probably correct as designed; worth an item so the
   boot's enumeration is complete rather than three-quarters complete.

## Open questions for the human

The brainstorming flow's interactive gates - one question at a time, approval per
design section - were not available in this dispatch. The decisions below were
made unilaterally and are the ones most worth overriding.

1. **The default, 45s.** Argued structurally against three constraints, one of
   which is visible independence from `RELAY_DB_STATEMENT_TIMEOUT`'s 30s. Not
   measured. Task 0 may move it.
2. **The signature change** costs seven mechanical test-call edits. The
   alternative - schedrunner logging its own summary line, no signature change -
   was rejected because it would put `RELAY_STARTUP_VALIDATION_DEADLINE`'s name
   in a package that never reads it, where a rename in `main` makes the log line
   lie with nothing going red. Say if the churn is not worth that.
3. **The scheduled-jobs README sentences** cross the section named in the scope
   fence. Argued as required by acceptance criterion 2; a cut line is given.
4. **One boot line or two.** This spec prints an unconditional bounds line before
   the pass and an unconditional outcome line after it. The bounds line is
   justified by adjacency (the boot log is otherwise silent for the whole window
   this knob governs); it is also one more line on every boot forever.
5. **The truncated line is long** - roughly 90 words. It matches
   `watchdogBoundsLine` and `parseTrailingLogWindow`'s register, and every clause
   is required by something in Decision 8. It can be shortened only by dropping a
   disclosure, so say which one.
