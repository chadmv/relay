---
date: 2026-08-20
topic: coordinator-stale-task-watchdog
slice: 2026-08-20-coordinator-stale-task-watchdog
branch: claude/pr-merge-session-eb02e4
range: origin/main..HEAD (backend only; Go + SQL + one migration; zero proto, zero files under web/; green, not yet merged)
closes: bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task
---

# Session Retro: 2026-08-20 - a server-written value is not a value beyond the caller's reach

**TL;DR:** the coordinator now has its own bound on task duration. Migration `000021` adds
`tasks.assigned_at`, written by `ClaimTaskForWorker` from the dispatcher's Go clock and nulled by
every statement that returns a task to `pending`. `ListOverdueAssignedTasks` scans the
`('dispatched','running')` partition on two independent arms - `started_at` versus
`timeout_seconds + RELAY_TASK_WATCHDOG_MARGIN`, and `assigned_at` versus `RELAY_TASK_MAX_ASSIGNMENT` -
and `internal/scheduler/watchdog.go` stamps `timed_out` through the **existing** `UpdateTaskStatus`
fence, then tells the agent to cancel, best-effort, per row as it is swept. Zero new statements that
write `tasks.status`. Unit 491 -> 510 top-level; integration store + scheduler + worker + api at 511
top-level, 0 fail.

**The finding of the iteration is that the slice as first implemented was defeatable by the exact
adversary it was built to stop, and the sentence that made it look safe was true.** The execution arm
keys on `started_at`; `handleTaskStatus` stamps `started_at = time.Now()` on **every**
`TASK_STATUS_RUNNING`; `UpdateTaskStatus`'s allow-list contains `'running'`, so `running -> running`
matches and re-stamps. An agent emitting one RUNNING every ten minutes resets the arm's clock
forever. The SQL comment defended the arm by saying `started_at` "is written by a relay-server Go
clock and by nothing else" - which is **correct**, and which does not imply what it was being used to
imply.

## The headline: provenance of the value says nothing about control of the timing

Three artifacts had a chance at this and none of them took it.

- The **spec** asked who writes `started_at`, answered it correctly (section 4.3: "written by the
  relay-server Go clock, confirmed, and it is the only writer that *sets* it"), and used that answer
  to conclude that the Go-computed-cutoff argument from the trailing-window slice "transfers here
  intact". The clock argument does transfer. The *unforgeability* argument, which is the one the
  execution arm actually needs, was never separately made.
- The **plan** inherited the conclusion.
- The **implementer** wrote the arm and wrote the comment asserting the property.

A Phase 4 lens found it by asking a different question: not *what value lands in the column* but *what
causes a write to that column at all*. The answer is a message on an untrusted stream, sent at a time
the sender chooses, as many times as it likes.

> **A value's provenance is not a statement about who controls the write.** "This column is written
> by our own clock, by our own process, from our own code" can be true in every clause and still
> describe a field an attacker moves at will, because the attacker's lever is the **trigger**, not the
> value. When a bound is measured from a timestamp, the question is not "who computes it" - it is
> "what causes it to be written, and how many times can that happen".

**Is this new for this project, or a variant?** It is new, and the closest sibling is the
already-codified **"the epoch establishes currency, not identity"**. Both are the same species: a
property that is real, verified, and does not carry the weight put on it. They differ in the artifact
and in the countermeasure. Currency-versus-identity is about a **guard predicate** and its
countermeasure is to add a second predicate. This one is about a **provenance argument in prose** and
its countermeasure is to ask a second question - who triggers the write - before the argument is
accepted at all. It is emphatically *not*
`reference_secret_hides_below_where_defended`, which is about one thing existing in two places; here
there is one place and an invalid inference about it.

The sharper reason to record it separately: **this project's dominant defect class is wrong prose,
and this comment was right.** Ten iterations of "probe the claim and grep its literal wording" would
not have caught it, because the literal wording checks out. The only thing that catches it is asking
what the true sentence does *not* say.

The comment now says so, in the statement's own doc block, and names the mechanism that actually
holds the arm up:

> `BEING A SERVER CLOCK IS NOT THE SAME AS BEING BEYOND THE AGENT'S REACH, and an earlier version of
> this comment conflated them. handleTaskStatus stamps started_at from the server's clock, but the
> agent chooses WHEN by sending TASK_STATUS_RUNNING, and it may send it repeatedly. What makes the
> execution arm survive that is UpdateTaskStatus's COALESCE, which makes started_at write-once per
> assignment - not the provenance of the value.`

Leaving the refuted version visible as "an earlier version of this comment conflated them" is the
right handling and matches how `jobs.sql`'s `updated_at` note records its own correction.

## One fix closed the hole in both directions, which is the tell that it was the right fix

The remedy is one clause in one statement:

```sql
started_at = COALESCE(started_at, sqlc.arg(started_at))
```

The security direction is the headline: the agent can no longer re-stamp, so the execution arm's
clock starts once per assignment and runs.

The **correctness** direction was found independently, by a different lens, and it is the mirror
image. The watchdog binds `StartedAt: t.StartedAt` - the value read at **scan** time. For a row that
was `dispatched` when scanned, that value is NULL. If the agent legitimately reports `running` inside
the scan-to-write window, that write passes every fence and stamps a real start time, and the
watchdog's write - which also passes every fence, at the same epoch, on a still-non-terminal row -
then **overwrites it with the stale NULL**. The lens proved it against a live Postgres rather than
arguing it. `COALESCE` closes that too: the argument can now only ever fill a NULL.

Neither lens saw the other's direction. Two independent failure modes, opposite in sign, one clause.

> **When a candidate fix closes a second, independently-found defect you were not aiming at, that is
> evidence about the fix, not a coincidence.** The two findings were "the caller can move this value"
> and "the caller's value can be clobbered by us". Both are statements that the column has no
> write-once discipline. A remedy that makes the column write-once answers both because they were
> always one question.

Two smaller notes from the same neighbourhood:

- **The clock is now read per row, not once per sweep.** Sharing one reading across a batch stamps
  every row with a `finished_at` from the start of the loop, and once `started_at` is write-once, a
  row whose agent stamped a real start mid-sweep could end up with `started_at` **later** than
  `finished_at`. The comment in `SweepOnce` says exactly that, which is the kind of consequence that
  is invisible until the first fix lands.
- **The conductor's relay of the caller sweep omitted `RequeueTaskByID`** from the list of statements
  that null `started_at`, and the implementer caught it and added the `assigned_at` line there. This
  project recorded on 2026-08-15 that a remediation brief is an artifact that gets the same treatment
  as a spec or a plan; this is the second instance, and the failure shape is the cheaper one - an
  incomplete enumeration rather than an unsatisfiable instruction.

## A mutation battery run against a broken baseline is worthless, and the tell is uniformity

`ListOverdueAssignedTasks` grew `LIMIT sqlc.arg(max_rows)::int` during the slice. The store fixture
did not set `MaxRows`, so an unset `int32` bound `LIMIT 0`, so **every scan returned nothing** and
every mutation in the battery "passed" - uniformly, with the same shape of result, across mutations
that test completely different predicates.

The implementer caught it themselves, and the tell was the uniformity, not any individual result.

> **A mutation battery must be preceded by a green baseline on the same tree, and uniform results are
> a signal about the harness, not a result about the code.** Mutations probe different predicates and
> should fail differently; when they all behave identically, the most likely explanation is that none
> of them was executed against anything.

This is the second consecutive slice whose finding is about mutation testing giving false comfort,
and the two are distinct in a way worth keeping apart:

- **2026-08-20 reconcile** (`reference_mutation_proof_position`): the battery ran correctly, each
  mutation was individually meaningful, and the battery was **collectively blind** because the
  poisoned input sat last in the sequence. The defect was in the *input's position*.
- **This slice:** the battery could not have detected anything at all, because the fixture's
  precondition was broken. The defect was in the *harness*.

Together they say: a mutation result is a measurement, and a measurement needs both a working
instrument and a specimen in the beam. Yesterday's lesson checks the specimen; today's checks the
instrument. The cheap combined discipline is: **run the battery's un-mutated baseline first and
confirm it is green and non-trivial** (rows returned, assertions actually evaluated), then mutate.

The fixture now carries the hazard where it will be read:

> `MaxRows is NOT optional and omitting it is not harmless: an unset int32 binds 0, which is LIMIT 0,
> which returns nothing at all. That is fail-closed ... but it is silent, so the production caller is
> guarded separately by TestWatchdog_ScanIsBounded, which asserts the bound it binds is
> WatchdogMaxRowsPerSweep.`

That is the correct split: the fail-closed direction is stated honestly, and the *production* binding
is pinned by its own test rather than by the fixture's diligence.

## A structural guard was broken four ways without reddening, for the second time in three slices

`TestWatchdogIsStartedByMain` is an AST guard (deliberately `go/ast` and not a regex, because a
regex guard in this repo was proven breakable by one stray JSX comment on 2026-08-13). A Phase 4 lens
broke it four ways. Two were fixed; two are documented as unreachable.

**Fixed:**

1. **`go` statement nested inside a conditional.** A plain `ast.Inspect` walk descends into an
   if-body, so `if watchdogMargin < 0 { go ...NewWatchdog(...) }` satisfied the guard while leaving
   the goroutine unreachable. The guard now requires the `GoStmt` to be a **direct element of a
   function body's statement list**.
2. **Only one of the two bounds traced back to `parseWatchdogDuration`.** A BFS that stopped at the
   first seed reaching the parser reported success with `scheduler.DefaultMaxAssignment` hardcoded
   and `RELAY_TASK_MAX_ASSIGNMENT` silently ignored - **while the guard's own failure message named
   both variables.** That is the sharp one: the test's message described a stronger check than the
   test performed. It now requires two *distinct* arguments to reach the parser.

**Not reachable by any source scanner, and now said so in the comment rather than papered over:**

3. `watchdogMargin, maxAssignment = 0, 0` inserted immediately above the `go` statement. Both arms
   off; every package green.
4. A pre-cancelled context handed to `Run`. Ticker never fires; every package green.

> **A guard whose failure message promises more than the guard checks is worse than no guard**, and
> case 2 is the exact instance: an operator reading that message would have concluded both env vars
> were wired. The honest repair is two-sided - strengthen the check where a scanner can reach, and
> write the boundary down where it cannot. The honest coverage for cases 3 and 4 is a boot-level test
> that a wedged task actually gets swept; nobody should try to strengthen the scanner into catching
> them.

Second time in three slices that a structural guard proved weaker than it claimed (the first was the
`Table` `minWidth` source-scanning guard, deleted in favour of letting `tsc` enforce it). The
emerging rule: **when a guard's claim and a compiler's or a runtime's claim overlap, prefer the
compiler or the runtime; when they do not overlap, state the gap in the guard's own comment.**

## A prose defect this slice falsified is still in the tree

Found in this pass, by grepping the literal claim rather than reasoning about where it lives.
`internal/store/store_test.go`, case 6 of the terminal-transition test:

> `// Case 6: the same shape with timed_out over failed ... There is no server-side timeout writer`
> `// in the tree at all - the agent picks one finalStatus and sends it once - so this pins the`
> `// vocabulary rather than a live path.`

Every clause of that is false as of this slice, and the middle clause is the exact premise the slice
exists to break. The test still passes and still asserts the right thing; the comment now
mis-describes why.

**The spec's section 10 enumerated the prose that had to move and missed this site.** It named
`RetryJobTasks`'s doc comment, `UpdateTaskStatus`'s doc comment,
`TestTasksStatusVocabularyIsExactly`, CLAUDE.md's epoch-fence bullet and the README - all five of
which were done correctly, including the generated-file read-back that the CRLF revert has silently
discarded before (verified: `internal/store/tasks.sql.go` carries the amendment). The miss is that
the enumeration was assembled from *doc comments on statements* and this claim lives in a **test
comment**.

This is the **tenth consecutive iteration** in which wrong prose about correct code is the dominant
defect class, and its shape here is new in one respect: the prose was falsified *by the slice itself*
and the slice had an explicit, prioritized task for exactly that work. A checklist of prose to move is
only as good as the search that produced it.

> **When a slice's acceptance criteria include "amend the prose this falsifies", the search must be a
> grep for the literal claim across the whole tree, not a list of the doc comments you happened to
> read.** The claim here is "one writer" / "no server-side writer"; two minutes of
> `rg -i "(only|sole|no server-side).*writer"` finds all four sites, of which three were amended and
> one was not.

Flagged to the conductor as a one-sentence fix in this slice's own scope rather than filed - it is a
comment on a test the slice's premise invalidated, which is precisely what the slice's Task 10
claimed to cover.

## Four lenses, four distinct contributions, and this time they did not converge

Unlike the previous three slices, no two lenses landed on the same symbol. That is itself worth
recording, because the standing heuristic ("when invariants and security land on the same symbol,
promote it immediately") is about the *information* in agreement, and its absence carries no negative
information at all.

- **Security** produced the headline: the `started_at` re-stamp, reached by asking what the agent can
  trigger rather than what it can write. It also produced the observability gap - repeated sweeps
  against one `worker_id` are the tell that a worker should be disabled, and nothing aggregates by
  worker - and the error-branch log repetition. The last two are filed.
- **Correctness** produced the mirror direction: the watchdog clobbering a legitimately-stamped
  `started_at` back to NULL, **proven with a live Postgres probe** rather than argued. Same clause,
  opposite sign, no awareness of the other lens's finding.
- **Invariants** produced the guard-breaking pass on `TestWatchdogIsStartedByMain`, four ways, and the
  discipline of separating the two AST-reachable ways from the two that are not.
- **Integration** ran the real end-to-end criterion the item demanded and the previous coverage could
  not give: a **connected** worker holding a hung task, swept, with its own late terminal report
  proven a silent no-op. Also `TestWatchdog_SweepsADispatchedOrphan`, which is the 2026-08-20
  amendment's case 1 seeded exactly as the item specified, and
  `TestWatchdog_DoesNotClobberAStartedAtStampedInsideItsWindow`, which is the correctness lens's
  finding turned into a permanent test rather than a note.

## `/code-review` found three real things, so the zero-findings pattern does not extend

The previous retro recorded two instances of a high-effort `/code-review` returning zero findings on
a diff where the fan-out then found six real things, declined to call two instances a pattern, and
asked for the **diff shape to be recorded alongside the result** so a third data point could confirm
or refute "zero findings correlates with a low-logic-delta diff".

Third data point, recorded per that instruction:

| | diff shape | `/code-review` (high) | fan-out |
|---|---|---|---|
| 2026-08-14 cursor-pager | behaviour-preserving refactor, no logic delta | 0 | 2 (the most valuable in the slice) |
| 2026-08-20 reconcile | 8 behavioural lines under 45 lines of comment | 0 | 6 |
| 2026-08-20 watchdog | new migration, new query, new package file, new wiring, new send path | **3** | 4 |

**All three of its findings were confirmed by the lenses**, none refuted. The covariate the previous
retro asked for holds so far: the two zero-finding diffs had essentially no new logic, and the diff
that carried a lot of new logic produced findings. So the correct reading of the earlier zeroes is
"the tool had little to say about diffs where the risk was not in the changed expressions", not "the
tool is weak" - and the standing goal ("a clean `/code-review` is a lead, not a verdict") is
unchanged and was not tested this time, because the result was not clean.

The instruction to keep is the one that produced this table: **record the diff shape with the result,
every time.** Two observations with no covariate were uninterpretable; three with one are already
telling us something.

## The process shape: the full pipeline, and it needed it

This is the first slice in a while to run **spec + plan + implement + verify** rather than collapsing
Phase 1 into Phase 2. The previous two collapses (cross-generation-401, reconcile-canonical-task-ids)
were both judged correct, under a test the last retro articulated: **collapse only when the item names
its own open questions.**

This item named four - what "too long" means, which clock, fail versus requeue, and whether the agent
is told - and by that test it would have qualified. Running Phase 1 anyway was right, and the
evidence is specific: **the spec made the central fork on evidence the item did not contain.**

The item presented fail-versus-requeue as a two-sided trade and told the implementer to "pick one, and
write down why". The spec picked *fail*, and its decisive arguments are three things the item does
not mention:

1. **Requeue does not terminate.** The sweeper cannot call `IncrementTaskRetryCount` -
   `TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath` is a module-wide structural guard - so
   nothing burns a retry and a hang-prone workload loops forever. The item's own headline symptom is
   *not fixed* by requeue in the pathological case; it becomes one permanently churning task instead
   of one stuck one.
2. **A coordinator-side terminal writer over a live assignment is not new.** `CancelJobTasks` already
   stamps `failed` on `dispatched`/`running` rows whose agents may still be executing, and
   `handleCancelJob` pairs it with a best-effort `sendCancelSignals`. That gave the slice a reviewed
   precedent to copy rather than a risk class to invent, and it is what made the cancel decision
   (section 7) cheap.
3. **Failing through the existing `UpdateTaskStatus` costs zero new statements that write
   `tasks.status` and zero new status partitions on a write path** - against a file whose invariants
   are this densely documented, that is the whole argument.

It also **refuted the item on a structural claim that changed the design**: there is no timestamp
that bounds a `dispatched` row. `started_at` is NULL, `finished_at` is NULL, and `created_at` is job
submission time, which for a task that queued six hours behind a busy fleet is unrelated to when its
assignment began. Keying the absolute arm on `created_at` would kill healthy, just-dispatched work.
**That is what makes migration `000021` a requirement rather than a nicety**, and no plan written
directly from the item would have contained it.

Two more spec-stage results worth keeping:

- It refuted the item's guess that telling the agent "may be a second slice", by enumerating the three
  existing callers of `worker.Registry.Send` and showing there was no new send machinery to build.
  Without that, the slice ships as bookkeeping: a released worker slot on a machine that is still
  busy, plus a `RetryJobTasks` duplicate-execution exposure created without the mitigation the tree's
  own precedent already pairs with it.
- It applied the previous retro's own lesson to a comment it had to edit anyway
  (`TestTasksStatusVocabularyIsExactly`'s "six statements" count, which was wrong and is now
  **deleted** rather than incremented to seven).

> **The collapse test needs a second clause.** "Does the item name its own open questions" is
> necessary and not sufficient. The other half is whether answering those questions requires evidence
> the item does not carry - and the cheapest tell is a fork the item frames as a preference ("pick
> one, and write down why") where one arm is actually foreclosed by something in the tree. Here it
> was foreclosed by a structural guard test three packages away.

## What Was Built

- **Migration `000021_tasks_assigned_at`** - nullable `TIMESTAMPTZ`, no default, with a
  `UPDATE ... WHERE status IN ('dispatched','running')` backfill to `NOW()` so a deploy cannot sweep
  a fleet's worth of healthy long-running work in its first minute. Down migration drops the column.
  Pinned by `TestMigration000021AddsAssignedAt` and `TestMigration000021DownUp`.
- **`ClaimTaskForWorker` stamps `assigned_at` from a Go-supplied parameter**, and its doc comment
  states the load-bearing property in the strongest available form: it is the **only** route into the
  scanned partition, so a stale `assigned_at` left by a requeue can never be observed by the watchdog.
- **Every statement that nulls `worker_id` also nulls `assigned_at`** - `RequeueTask`,
  `RequeueTaskByID`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`, `IncrementTaskRetryCount`,
  `RetryJobTasks`, `CancelJobTasks`. Pinned by `TestAssignedAtIsClearedWhereverWorkerIDIs`. **The
  claim is one-directional and the comment says so at length**: `assigned_at IS NULL` means no
  assignment, but the converse does **not** hold, because `UpdateTaskStatus` writes neither column -
  so every `done`/`failed`/`timed_out` row that got there through it still carries a non-NULL
  `assigned_at`. Anything meaning "currently assigned" must say so with the status predicate.
- **`UpdateTaskStatus`'s `started_at = COALESCE(started_at, ...)`** - the write-once clause, closing
  both directions above. Its doc comment now describes three callers, and names why the watchdog's
  worker predicate is tautological for the same reason `failClaimedTask`'s is while the **epoch**
  predicate is the real TOCTOU guard in both.
- **`ListOverdueAssignedTasks`** - read-only, two independent arms, each with its own explicit
  `_enabled` bool rather than a sentinel cutoff, `EXTRACT(EPOCH FROM ...)` rather than
  `make_interval` so only a `timestamptz` and a `bigint` are bound, `ORDER BY assigned_at NULLS LAST,
  id`, and `LIMIT sqlc.arg(max_rows)::int`. **Every arm fails closed on a missing value** and the
  comment forbids the `IS NULL OR ...` "fix" by name.
- **The `LIMIT` is not only a volume cap.** Every row in a batch is written against one scan, so an
  unbounded sweep makes the scan-to-write window for the last row the whole loop rather than an
  instant - and that window is exactly where a concurrent agent transition lands. Capping the batch
  caps the window. `WatchdogMaxRowsPerSweep = 500`; the caller logs when a sweep comes back full so a
  truncated sweep is never mistaken for a complete one.
- **`internal/scheduler/watchdog.go`** - `Watchdog` over two narrow interfaces (`watchdogStore`,
  `taskCanceller`) plus an injectable clock, which is what makes the whole sweep unit-testable
  without Docker. `Run(ctx)` on a 60s ticker, `SweepOnce(ctx)` exported. Read the type comment for
  the grace-ordering argument in both directions and for the registry-blindness rule.
- **Both arms off skips the scan entirely**, and the condition is an `AND` - with an `OR`,
  configuring one arm off would silently disable the whole watchdog. The comment says that.
- **`Registry.SendCancel`**, mirroring `SendEvictCommand`, with `force=false` deliberately:
  `force=true` skips workspace finalize and risks poisoning warm-workspace scoring for every later
  task on that machine, while `force=false` still closes `cancelledCh`, which is the escape that
  frees a log write parked on a full `sendCh`. Matches `handleDisableWorker`, the other place the
  coordinator unilaterally takes tasks from a still-connected agent.
- **Cancels are dispatched as each row is swept, not batched after the loop.** `CancelTask` carries
  no epoch, so a late cancel can kill a **fresh** run of the same task id that an operator retry
  reopened. Batching would delay every send by the length of the whole sweep for no gain. Each send
  still runs on its own goroutine, so N overdue tasks on one wedged worker cost ~one send timeout.
- **`finalizeTerminalTask` extracted from `Dispatcher.failClaimedTask`** with the published status as
  a parameter, gated on byte-identical existing `internal/scheduler` tests **and** on a mutation
  proving the gate is not decorative.
- **`overdueReason` / `watchdogSweptLine`** - the per-task log line. It returns `"unknown"` when
  neither arm explains the row rather than falling back to `"absolute"`, because the previous version
  could print `"assignment age 3h0m0s exceeds 24h0m0s"`, a sentence contradicted by its own numbers.
  Since that line is the entire justification for logging one per swept task without a budget, it may
  not assert something an operator can see is false.
- **`cmd/relay-server/watchdog_config.go`** - `parseWatchdogDuration` following
  `parseTrailingLogWindow`'s shape, with **per-knob floors**: `minWatchdogMarginDur = 2m` (the
  smallest round number above README's ~105s reconnect analysis, deliberately the same number as
  `minSaneTrailingLogWindow` next door, because both answer "how late may a legitimate agent still
  be") and `minMaxAssignmentDur = 1h` (anything below is almost certainly `24m` for `24h`). A value
  below its floor is **kept and warned about**, and the warning names the units mistake.
- **`watchdogBoundsLine`, unconditional at every boot.** A mechanism that can terminate a user's work
  must state its limits at every start; the ordinary path was otherwise completely silent, so an
  operator could not tell whether the watchdog was running, what it would do, or that somebody had
  switched it off.
- **Prose moved:** `RetryJobTasks`'s doc comment (appended as "THAT WATCHDOG NOW EXISTS, so read the
  paragraph above as history", preserving the original argument rather than editing it away),
  `UpdateTaskStatus`'s, `TestTasksStatusVocabularyIsExactly`'s comment and failure message (with the
  stale "six statements" count **deleted**), CLAUDE.md's epoch-fence bullet (one clause naming
  `ListOverdueAssignedTasks` as the **second** carve-out that reads backwards), and two README rows.
- **Zero proto, zero files under `web/`.**

## Key Decisions

- **Fail (`timed_out`), not requeue**, through the **existing** `UpdateTaskStatus`. Zero new
  statements that write `tasks.status`, zero new status partitions on a write path, and no
  dependency on `bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence` - whose promotion
  condition ("if the watchdog is specced with the requeue shape") is therefore **not triggered**.
- **Fail without an epoch bump.** A bump would close the swept task's log write channel immediately
  instead of at `finished_at + RELAY_TASKLOG_TRAILING_WINDOW`, and would cost an amendment to
  CLAUDE.md's "never an epoch bump on terminal transitions" clause to buy fifteen minutes. Adding a
  bump later is a strictly later decision; shipping one and discovering it truncates legitimate
  output means undoing a write path.
- **Two independent bounds, a row is overdue if either fires.** The execution arm exists because it
  is the one an operator's `timeout_sec` maps onto; the absolute arm exists because `timeout_sec = 0`
  is documented as "no deadline" and because a row may never reach `running` at all.
- **`0` means "this arm is off" in both env vars, one rule with no exceptions.** A margin of exactly
  zero is a legitimate aggressive setting, but giving the same literal two meanings across two
  adjacent knobs is a footgun; an operator who genuinely wants no margin writes `1s`.
- **The partition is `('dispatched','running')`, never `status = 'running'`.** A task spends the whole
  workspace sync as `dispatched` (`handleTaskStatus` has no case for `TASK_STATUS_PREPARING`), and a
  stale-epoch reconcile can strand a `dispatched` row. Keying on `running` misses both.
- **The bound is on assignment age, never on last activity.** A `MAX(task_logs.created_at)` liveness
  signal is agent-controlled - a hung-but-chatty agent would look healthy forever - and the volume
  needed to do that is itself unbounded today. `TestListOverdueAssignedTasks_ActivityDoesNotCount` is
  a tripwire for exactly that "improvement".
- **Registry-blind when deciding to write, registry-consulted only for delivery.** Under
  multi-replica operation a local registry miss proves nothing, and the orphaned-`dispatched` case is
  precisely a row whose agent has been told to abandon it.
- **The sweep interval is a constant, not a knob** (`WatchdogSweepInterval`), named for the watchdog
  so it is never confused with `metrics.SweepInterval`. It is an implementation cadence, not an
  operational timeout.
- **A swept task is not automatically retried**, stated in the README row and in the startup warning
  text. `retries: 3` buys zero automatic attempts if the task hangs; recovery is
  `POST /v1/jobs/{id}/retry`.

## Findings Triage

- **1 finding against the shipped execution arm**, and it is the one that mattered: the arm was
  defeatable by the adversary the slice was built to stop. Found by a Phase 4 lens; missed by the
  item, the spec, the plan and the implementer.
- **1 finding against the shipped write path**, opposite in sign, found independently by a second
  lens with a live-Postgres probe. Same one-clause fix.
- **4 ways a structural guard was broken without reddening**, by a third lens. Two fixed, two
  documented as unreachable.
- **1 self-caught harness defect**: a mutation battery run against a `LIMIT 0` baseline.
- **1 finding against the conductor's remediation relay** (an incomplete caller enumeration), caught
  by the implementer. Second consecutive slice with a defect in a conductor-authored brief.
- **2 findings against the item, both by the spec, both design-changing**: no timestamp bounds a
  `dispatched` row (which is why the migration exists), and telling the agent does not need a second
  slice.
- **1 finding against the item's framing of the fail-versus-requeue fork** - the item presented a
  two-sided trade where one arm was foreclosed by a structural guard test.
- **1 prose defect from this slice's own falsification list still in the tree**, found in this pass
  (`internal/store/store_test.go` case 6). Flagged to the conductor.
- **3 `/code-review` findings, all confirmed by the lenses, none refuted.**
- **0 findings against the shipped behaviour after remediation.**

## Recommended Backlog Items

**Filed this pass (proposals for human accept - the conductor commits, the human accepts):**

1. `idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced` (**idea/medium**) -
   a swept task frees the worker slot while the coordinator has no way to **compel** the agent to
   stop, so a wedged or hostile worker becomes a slow **sink**: it drains queued work at roughly
   (slots / max-assignment) and fails each item, instead of holding a fixed set. Repeated sweeps
   against one `worker_id` are the tell that a worker should be disabled, and there is no counter, no
   metric and no aggregated line. Filed as a **sibling** of
   [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] and
   [[idea-2026-08-15-ingest-log-suppression-is-uncounted]], not as an amendment to either, and the
   item says why: all three are "the system now silently drops or kills something and nobody can see
   it", they share one read-surface dependency, and they should be **specced together and shipped
   separately**. **Not scope creep:** this slice created the sink behaviour by releasing the slot.
2. `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` (**bug/low**) - the error branch in
   `SweepOnce` logs a line for a row it did **not** transition, so that row stays in the scan
   partition and logs again every 60 seconds for as long as the failure persists. Under a partial
   degradation where the scan succeeds and the write fails, that is N lines per minute indefinitely.
   **The comment four lines below argues the SUCCESS line is safe because "each task can be swept at
   most once (it is terminal afterwards)" - true for the success line, false for the error line
   above it, and both are in the same comment block.** Not attacker-driven, so not a new instance of
   the 2026-08-15 flood class; this is operator-visible volume during a database incident, which is
   when log volume is least welcome. **Not scope creep:** the line is new in this slice.
3. `idea-2026-08-20-claimtaskforworker-fixtures-bind-a-null-assigned-at` (**idea/low**) - a keyed Go
   literal without the new field compiles and binds SQL NULL, and almost every
   `ClaimTaskForWorkerParams` literal in the tree omits `AssignedAt`. **No live consequence** - every
   test that runs the watchdog or the scan sets it - so the cost is entirely future: the next person
   writing a watchdog test from an existing fixture gets a row silently exempt from the absolute arm,
   with nothing red. **The item explicitly forbids churning the literals**; the fix is the fixture,
   and the repo already has the right ones (`overdueFixture.dispatched`/`running`,
   `assignedFixture.claimedAt`). Filed deliberately without a count, per the standing "delete the
   count" lesson.

**Amendment applied to an existing item (no scope change, `updated:` added):**

- `bug-2026-08-12-retries-unvalidated-and-budget-only-in-go` **now claims `timeout_seconds`**, which
  it previously named and disclaimed ("`timeout_seconds` sits in the same struct with the same
  absence of bounds and should be looked at in the same pass, but is not claimed by this item"). The
  amendment records the consequence that is **new as of this slice**: a user now chooses their own
  task's execution-arm bound with no ceiling, so `timeout_seconds: 2147483647` (~68 years) is exempt
  from the execution arm and bounded only by the absolute arm, and a negative value is silently a
  synonym for "no deadline" (`newRunner` sets a deadline only when `timeoutSec > 0`; the SQL arm
  requires `timeout_seconds > 0`) where the README documents that only for `0`.

  **Amended rather than filed separately, and this is the judgment call in this pass.** The
  precedent from 2026-08-15 says to file a sibling when amending would silently widen an item's scope
  and falsify its own Done-When. That test does not bite here: the item's half A is *one check in
  `jobspec.Validate`*, the new field's bound is adjacent lines in the same function, the item itself
  says the two should be "looked at in the same pass", and two items pointing at one four-line change
  would both be closed by one commit while only one gets the `git mv`. The disclaimer predates the
  evidence that promotes this field from "same shape" to "has its own live consequence".

**Considered and NOT filed, with reasons:**

- **`internal/store/store_test.go`'s falsified case-6 comment.** In this slice's own scope (Task 10 is
  "the prose that this slice falsifies") and a one-sentence fix. Flagged to the conductor. An item
  asking somebody to correct a comment the current PR was supposed to correct would be stale before
  it was read - same handling as the plan's residual count error two slices back. **Non-item,
  deliberately.**
- **Cases 3 and 4 of the broken structural guard** (reassigning the bounds to zero, a pre-cancelled
  context). Not reachable by any source scanner, and the guard's comment now says so in as many
  words. The honest coverage is a boot-level test, which is a different and much larger piece of work
  than "strengthen the AST walk". Filing it as an item would produce an attempt at the scanner.
- **A per-assignment fencing token at the agent**, which would eliminate the swept-then-retried
  duplicate-execution residual entirely. Already a **named non-goal** in `tasks.sql`'s own comment
  and in the spec; filing it would contradict a shipped decision rather than record a gap.
- **`bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence`** - checked and **not amended**.
  Its promotion condition was explicitly "if the watchdog item is specced with the requeue shape",
  and the fail shape was chosen, so the condition is **not triggered** and the item stays at medium,
  unchanged. Recording the check here so the next pass does not re-derive it.
- **`bug-2026-08-14-task-logs-have-no-per-task-volume-cap`** - checked and **not amended**. This slice
  bounds an assignment's *duration*, not its log *volume*, and the interaction (a swept task keeps
  its write channel for the trailing window) is stated in Known Limitations below and in the spec.
  The item's scope and severity are unchanged.

## Known Limitations

- **The retry budget does not apply to a swept task.** `retries: 3` buys zero automatic attempts if
  the task hangs; recovery is `POST /v1/jobs/{id}/retry`. Chosen over a requeue-and-burn hybrid
  because an automatic retry of a task that may still be executing is duplicate execution on a
  schedule, and because the hybrid needs a new fenced statement that the structural guard on
  `IncrementTaskRetryCount` deliberately prevents reusing.
- **A swept task an operator retries can duplicate-execute** if the cancel did not take. Bounded,
  operator-gated, mitigated by the cancel, and already true of the cancel-then-retry path today.
- **The zombie keeps its log write channel for `RELAY_TASKLOG_TRAILING_WINDOW` after the sweep**, and
  its volume within that window is unbounded until
  [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]] is closed.
- **The freed slot is optimistic.** The coordinator releases the worker's slot while the subprocess
  may still be running, so a machine with a wedged task can be handed more work. This is inherent to
  declaring a task over from the coordinator's side, and it is what makes filed item 1 more than a
  nicety.
- **A `dispatched`/`running` row with a NULL `worker_id` is not recoverable** by this or any fenced
  writer. Unreachable today - nothing in this repo `DELETE`s a worker - and named in the statement's
  comment rather than filed.
- **`main()`'s wiring is guarded only structurally**, and the guard cannot reach two of the four ways
  it was broken. Its comment states that boundary.
- **The end-to-end criterion is invisible to `make test`.** `TestWatchdog_SweepsAHungTaskOnAConnectedWorker`,
  `TestWatchdog_SweepsADispatchedOrphan` and
  `TestWatchdog_DoesNotClobberAStartedAtStampedInsideItsWindow` all need Docker. The `Watchdog` type's
  own contract is covered by unit tests with fakes and a frozen clock, which is deliberate and is why
  the narrow interfaces exist.
- **The suite figures were reported by the implementing and verifying lanes and are not re-measured
  here** (unit 491 -> 510 top-level; integration store + scheduler + worker + api 511 top-level, 0
  fail). Per the previous retro's instruction: these are **top-level** counts.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **fifteenth iteration**.
  Two design-changing refutations this time, after two clean results.
- **A backlog proposal is not a contract** - fifteen for fifteen.
- **An item's open question, framed with a lead, is a claim** - honored, and extended: here the item
  framed a fork as a **preference** ("pick one, and write down why") where one arm was foreclosed by a
  structural guard test three packages away.
- **Plan-supplied tests and plan-supplied mutations are untrusted** - honored, and it paid in a new
  way: the battery's *harness* was broken, not its contents.
- **A mutation proof must leave a test behind** - honored; the `COALESCE` finding left
  `TestWatchdog_DoesNotClobberAStartedAtStampedInsideItsWindow`, and the scan bound left
  `TestWatchdog_ScanIsBounded`.
- **Wrong prose about correct code is the dominant defect class** - **tenth consecutive iteration**,
  and the surviving instance was falsified by this slice's own change while the slice had a
  prioritized task for exactly that work.
- **The correct fix for a stale count in a comment is usually to delete the count** - honored twice:
  `TestTasksStatusVocabularyIsExactly`'s "six statements", and filed item 3, which states a property
  and no number.
- **Say what a fix does not buy in the same sentence that says what it does** - honored; the README
  row says a swept task is not automatically retried, and the log line says when a sweep was capped.
- **A clean `/code-review` is a lead, not a verdict** - not tested; the result was not clean. The
  companion instruction, **record the diff shape with the result**, was honored and produced the
  three-row table above.
- **Prefer a symbol name to a line range in any cross-file citation** - honored; every citation in
  this retro and in the filed items names a symbol.
- **Backlog housekeeping is required scope** - the close of the source item is outstanding and belongs
  to the conductor.

New from this iteration:

- **A value's provenance is not a statement about who controls the write.** "Written by our own clock,
  by our own process, from our own code" can be true in every clause and still describe a field the
  caller moves at will, because the lever is the **trigger**, not the value. When a bound is measured
  from a timestamp, ask what causes the write and how many times it can happen. Sibling of "the epoch
  establishes currency, not identity"; **candidate for durable memory**, and the one lesson in the
  record that a prose-checking pass structurally cannot find, because the prose was true.
- **When a candidate fix closes a second, independently-found defect nobody was aiming at, that is
  evidence for the fix.** Two lenses found opposite signs of "this column has no write-once
  discipline"; one `COALESCE` answered both because they were always one question.
- **A mutation battery needs a green, non-trivial baseline on the same tree, and uniform results
  indict the harness rather than exonerate the code.** Complements yesterday's positional lesson:
  that one checks the specimen, this one checks the instrument.
- **A guard whose failure message promises more than it checks is worse than no guard.** Strengthen
  where a scanner can reach; write the boundary down where it cannot; never let the message describe
  the stronger check.
- **When a slice must amend "the prose it falsifies", produce the list by grepping the literal claim
  across the whole tree**, not by listing the doc comments you happened to read. Three of four sites
  were amended; the fourth was in a test comment and was never on the list.
- **The Phase-1-collapse test needs a second clause.** "Does the item name its own open questions" is
  necessary and not sufficient; also ask whether answering them requires evidence the item does not
  carry. The tell is a fork the item frames as a preference where one arm is already foreclosed.

## Files Most Touched

- `internal/store/query/tasks.sql` - read three comments in order: `UpdateTaskStatus`'s (three
  callers, and why the worker predicate is tautological while the epoch predicate is not),
  `ClaimTaskForWorker`'s (`assigned_at` is written here and nowhere else that matters, and why that
  makes a stale value unobservable), and `ListOverdueAssignedTasks`'s in full - the backwards
  allow-list, the fail-closed rule, the `LIMIT`-is-a-window argument, and the
  **BEING A SERVER CLOCK IS NOT THE SAME AS BEING BEYOND THE AGENT'S REACH** paragraph, which is the
  slice's headline lesson written where the next editor of that arm will hit it.
- `internal/scheduler/watchdog.go` - the type comment (grace ordering in both directions,
  registry-blindness), `SweepOnce`'s per-row clock comment, the cancel-per-row comment, and
  `overdueReason`'s note on why it returns `"unknown"` instead of guessing.
- `cmd/relay-server/watchdog_config.go` - the two per-knob floors and why they are not one number.
- `cmd/relay-server/watchdog_config_test.go` - `TestWatchdogIsStartedByMain`'s comment, specifically
  the **WHAT IT CANNOT REACH** paragraph. (Its numbered list is introduced as "EXACTLY THREE THINGS"
  and enumerates two - flagged to the conductor.)
- `internal/worker/handler_watchdog_e2e_integration_test.go` - the three end-to-end tests, including
  the one that pins the `COALESCE`.
- `internal/store/list_overdue_assigned_tasks_integration_test.go` - `bothArms`'s comment on why
  `MaxRows` is not optional, and `TestListOverdueAssignedTasks_KeysOnAssignedAtNotCreatedAt`, the
  single row that pins the whole reason the migration exists.
- `docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md` - section 1's R2 (no
  timestamp bounds a `dispatched` row) and section 3 (fail versus requeue) are the reusable parts.
- `docs/superpowers/plans/2026-08-20-coordinator-stale-task-watchdog.md` - the `assigned_at` decision
  table and the `make generate` procedure.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No
  `git log`, no `git diff`, no test run, no `sqlc generate`. Every claim below that could be checked
  by reading was checked against the worktree.
- **Verified by reading:** `internal/scheduler/watchdog.go` in full; `ListOverdueAssignedTasks`,
  `UpdateTaskStatus`, `ClaimTaskForWorker`, `RetryJobTasks` and all seven `assigned_at = NULL` sites
  in `internal/store/query/tasks.sql`; `internal/store/tasks.sql.go` for the `RetryJobTasks`
  amendment, confirming the regeneration was not lost to the CRLF revert; `cmd/relay-server/main.go`'s
  wiring block and `watchdog_config.go`'s two floors; `cmd/relay-server/watchdog_config_test.go` in
  full, including `TestWatchdogIsStartedByMain`'s AST walk and its stated boundary;
  `internal/store/list_overdue_assigned_tasks_integration_test.go`'s fixture and test names;
  `internal/store/tasks_assigned_at_integration_test.go`'s test names;
  `internal/worker/handler_watchdog_e2e_integration_test.go`'s three test names and the `COALESCE`
  assertions; `internal/store/tasks_status_vocabulary_lockstep_test.go`, confirming
  `ListOverdueAssignedTasks` is named and the "six statements" count is gone; CLAUDE.md's epoch-fence
  bullet, confirming the one-clause amendment landed and nothing else moved; both migration `000021`
  files by name; README's two configuration rows and the startup-sequence line; `jobspec.Validate` in
  full, confirming neither `Retries` nor `TimeoutSeconds` is examined; every `AssignedAt:` binding in
  the tree by grep (one production, six test); the full text of the closed-out backlog item including
  its 2026-08-20 amendment; the spec in full; and the two prior retros for structure and for the
  `/code-review` claim.
- **The falsified prose in `internal/store/store_test.go` was confirmed by reading**, not inferred:
  case 6's comment reads "There is no server-side timeout writer in the tree at all". The grep that
  found it (`(only|sole|no server-side).*writer`, case-insensitive, across `internal/`) returned four
  sites; three carry correct amendments and this one does not.
- **Duplicate check run before each filed item**, against every open item in `docs/backlog/`. Item 1's
  nearest neighbours are the two open observability items; they are cross-linked, not merged, and the
  item states the split. Item 2 has no neighbour - `bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget`
  is about a *budget's* coverage boundary on the gRPC recv path, not about a repeating line on a
  periodic writer. Item 3 has no neighbour. The `timeout_seconds` finding **did** have one and was
  folded into it rather than filed.
- **Reported by the implementing and verifying lanes, not re-run here:** all suite counts; the
  `LIMIT 0` mutation-battery result and its diagnosis; the live-Postgres probe behind the `COALESCE`
  clobber finding; the four ways `TestWatchdogIsStartedByMain` was broken and which two now redden;
  the byte-identical-test gate and its mutation proof on the `finalizeTerminalTask` extraction; the
  three `/code-review` findings; `go build` and `go vet -tags integration`.
- **Not verified:** all test results, the commit set and diff stat, and the change set as `git` sees
  it. Each is attributed above.
- **The three items filed by this pass are in `docs/backlog/` as proposals**; the human gives final
  accept. The amendment appends a dated section and adds only `updated:` to frontmatter, plus one
  acceptance criterion and one cross-link. **Outstanding and belonging to the conductor:** the close
  of `bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task` (`/backlog close`, never a
  hand-edited `status:`), the one-sentence prose fix in `internal/store/store_test.go`, the
  "EXACTLY THREE THINGS" wording in `TestWatchdogIsStartedByMain`, the exact-file-set check, the final
  gates, all commits, and a ROADMAP refresh.
