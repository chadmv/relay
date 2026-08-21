---
date: 2026-08-20
topic: requeue-task-by-id-fence
slice: 2026-08-20-requeue-task-by-id-fence
branch: claude/pr-merge-session-eb02e4
range: origin/main..HEAD (backend only; SQL + one generated file + two Go callers + two new integration test files; zero migration, zero proto, zero files under web/; full integration gate running at the time of writing, not yet merged)
closes: bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence
---

# Session Retro: 2026-08-20 - a uniqueness claim is the one claim you cannot check by reading the thing it names

**TL;DR:** `RequeueTaskByID` now fences on `assignment_epoch` and `worker_id` (plain `=`,
NULL-rejecting) alongside its existing id and status allow-list, and is `:execrows` so
`reconcileRunningTasks` counts matches rather than attempts. **And so does `RequeueTask`**, three
statements away in the same file, which had the identical unfenced shape and which the closing item
asserted did not exist. Its caller - the dispatcher's send-failure path - had **no test coverage at
all** before this slice. Both statements now carry the same four predicates, both callers pass values
read off one consistent row, and both are pinned by a per-predicate test series with a mutation
battery behind it.

**The finding of the iteration is not the second statement. It is that three artifacts swept the
family and reported it clean.** The item made a claim quantified over the whole tree ("the only
requeue statement whose `WHERE` names nothing but the task id and a status allow-list"); the plan's
"Verification of the backlog item against HEAD" section checked seven other claims and refuted five,
and never checked that one; the implementer edited the file the counter-example lives in. The
conductor's `/code-review` found it.

## The headline: verify a uniqueness claim by re-running its search, never by reading its subject

Every claim the plan checked was a claim **about a named symbol**. Does `RequeueTaskByID` fence on id
and status only - read the statement. Does it have one production caller - grep the identifier. Does
`Handler.Metrics` have a counter shape - open the package. All seven were checkable by reading the
thing the item named, and the plan was rigorous about it: it refuted the caller count, refuted the
metrics seam, refuted an acceptance criterion that did not test the property it named, and refuted
the handler-test shape.

The one claim that was not checkable that way is the one nobody checked. "The **only** requeue
statement in the tree" is not a statement about `RequeueTaskByID` at all - it is a statement about
every other statement, and the only way to test it is to run a search for the *shape*:
a `WHERE` clause over `tasks` naming an id and a status and nothing else. That search takes about a
minute in a file with four requeue statements in it. It was never run, by anyone, at any stage.

`RequeueTask` was `WHERE id = $1 AND status = 'dispatched'`. Same shape, same consequence, and
arguably **more** reachable: its caller is `dispatchOne`'s `registry.Send` failure branch, so it fires
precisely when a worker is gone or wedged, which is exactly when a second writer is likely to be
releasing that worker's tasks.

> **A claim of the form "X is the only Y" cannot be verified by opening X.** It is a claim about the
> complement, and the complement is only reachable through a search over the *shape* rather than the
> *name*. When an item's central sentence contains "the only", "the last", "no other" or "everywhere
> else", the corresponding verification task is a grep for the pattern, and it belongs in the plan as
> a task with an expected result - not as an assumption inherited from the item's title.

**Is this a new lesson for this project, or an instance of an existing one? It is genuinely
distinct, and the three siblings are worth naming so it is not collapsed into any of them.**

- **`reference_backlog_proposal_not_contract`** ("verify each proposal bullet against current code
  before scoping") is the closest, and it does not cover this. That lesson is honored by checking
  each bullet against the code it names - which the plan did, exhaustively and successfully. The
  failure here is that one bullet **named nothing checkable**. The countermeasure differs: for an
  ordinary claim you open the symbol, for a uniqueness claim you re-run the search that produced it.
- **`reference_accurate_item_wrong_remedy`** is about a right diagnosis with a wrong fix. Here the
  diagnosis was right *and* the prescribed SQL was right - it shipped essentially verbatim, and the
  plan's own handoff notes say so ("the item's diagnosis and its prescribed SQL were both correct -
  the first time in three slices"). **That sentence is itself falsified by this finding**, which is a
  small and instructive irony: the artifact congratulating the item on accuracy was written by the
  stage that had just failed to check its scope.
- **`reference_item_naming_a_fix_precommits_the_reader`** ("X lacks Y" bakes the remedy into the
  title) is about the *remedy* being pre-committed. Here what was pre-committed is the **extent**.
  The title named one symbol, so the plan's file table had one SQL row, so the engineer's edit had
  one target, so the tests covered one statement. Nothing after the item ever re-opened the question
  of how many statements there were.

So: diagnosis right, remedy right, **scope wrong**, and scope propagates further than either of the
others because every downstream artifact inherits it as its file list.

The correction is recorded where the next reader will hit it, in `RequeueTask`'s own doc comment:

> `THIS IS THE SECOND OF TWO STATEMENTS THAT HAD THE UNFENCED SHAPE, AND BOTH ARE NOW FENCED.`
> `Recorded here because bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence says`
> `RequeueTaskByID was "the only requeue statement in the tree ...". That was FALSE when it was`
> `written ... The sweep is finished - do not re-derive it.`

Leaving the refuted claim visible and stating that the sweep is closed is the right handling and
matches how `ListOverdueAssignedTasks` and `jobs.sql`'s `updated_at` note record their own
corrections.

## The conductor supplied a wrong reachability mechanism and it shipped into three files

The `/code-review` finding that found `RequeueTask` was right about the bug and right about the fix.
Its **mechanism** was wrong: it said the window is reachable because "a disconnected worker is what
arms its own grace timer", with the 5s `sendTimeout` as the width of the window. Those two halves
cannot co-occur.

- If the worker is gone from the registry, `Registry.Send` returns `worker %q is not connected`
  before `workerSender.Send` is ever reached. **No window at all.**
- If the worker disconnected, `sender.closed` is closed when the send loop returns, so a blocked
  `Send` takes `case <-sender.closed` and returns **immediately** - and `teardownConnection` closes
  the sender **before** it calls `h.grace.Start`. The requeue is unblocked strictly before the grace
  timer exists, and the timer then waits its full window on top.
- The 5s belongs to exactly one branch: `case <-timeout.C` on a full 64-slot queue, which requires
  the send loop alive inside `stream.Send`. That is a worker **wedged but still registered** - online,
  with no grace timer armed.

Two mechanisms that do reach it went unmentioned: an **admin disable** (`handleDisableWorker` ->
`RequeueWorkerTasks`), and a **second `Connect` from the same agent** while the first stream is wedged
(nothing serializes `Connect` per worker, so the new stream's reconcile requeues the task through the
sibling statement). Both release the assignment without any grace timer, the dispatcher re-claims for
W2, and the wedged goroutine then tears the task off W2 on its stale snapshot.

The wrong story travelled into the SQL doc comment, the dispatcher call site's comment, and the new
test's numbered repro before a Phase 4 lens ran the sender's branches and refuted it. All three now
carry the corrected mechanism with the refuted version preserved by name, so nobody reinstates it.

> **A remediation brief is an artifact and gets the same treatment as a spec or a plan.** This project
> recorded that on 2026-08-15; this is the third instance in this batch and the most expensive shape
> yet. The previous two were **incomplete enumerations** (a caller sweep that omitted
> `RequeueTaskByID`; lens findings relayed accurately but partially), which the receiving lane caught
> because something did not compile or did not add up. **A wrong causal story compiles.** It reads as
> authoritative, it explains the symptom, and the only way to catch it is to trace the branches -
> which is exactly the work the brief appears to have already done.

Three consecutive iterations in which the conductor was the stage that introduced a defect is a
pattern, not a coincidence, and the honest reading is that **the conductor is a lane with no reviewer
above it.** Every other artifact in the pipeline gets read by somebody whose job is to disagree with
it: the item is checked by the plan, the plan by the engineer, the code by four (here two) lenses plus
`/code-review`. Conductor-authored text - the remediation brief, the caller sweep, the relayed finding
- goes straight into an engineer's hands as an instruction. The cheap countermeasure is not "the
conductor should be more careful"; it is to **write the brief's mechanism as a claim the receiving
lane is explicitly asked to verify before acting on it**, the same way the planner is asked to verify
the item.

## Testing that a predicate REJECTS is not testing that it ACCEPTS

The `RequeueTaskByID` series (tests A-E) pinned the epoch predicate, the worker predicate three ways
(wrong worker, zero-value worker, and a planted NULL/NULL row that is the only real discriminator
between `=` and `IS NOT DISTINCT FROM`), and the status allow-list's **negative** arm - a terminal
task is not resurrected. By every rule this project has written down, that is a complete
per-predicate battery, and the mutation table (M1-M4) proved each predicate load-bearing.

Nothing pinned the allow-list's **positive** arm. Narrowing `IN ('dispatched','running')` to
`IN ('dispatched')` left the store, worker, scheduler **and** api suites all green - the last of those
a 440-second run.

That is not a cosmetic hole. `GetActiveTasksForWorker` returns `dispatched` **and** `running`, so a
task the agent was genuinely executing and no longer reports on reconnect is `running` in the
database. It is the single most common thing this statement exists to requeue. Drop `'running'` and
every one of them silently stays `running` against a worker that is not executing it: no requeue, no
log line, left for the watchdog, CI green. `TestRegisterWorker_ReconcilesRunningTasks` does **not**
cover it despite its name - all three of its fixture tasks are left `dispatched` by
`ClaimTaskForWorker` and none is ever transitioned to `running`.

`TestRequeueTaskByID_RequeuesARunningTaskForItsAssignee` now pins it.

**The sibling statement gives the mirror image, and reading the two together is what makes the lesson
general.** `RequeueTask`'s allow-list is deliberately narrower (`= 'dispatched'` only, because the
dispatch that would have made the task `running` is the thing that just failed). Until
`TestRequeueTask_RunningTaskIsNotRequeuedByTheSendFailurePath` existed, a mutation **adding**
`'running'` there also left the whole suite green - despite a paragraph in `query/tasks.sql`
explaining at length why it must not be added.

So the same file produced both directions of the same gap in one slice: one allow-list unpinned
against **narrowing**, its sibling unpinned against **widening**, and in both cases the prose was
correct and the check was absent.

> **An allow-list is a boundary and needs pinning on both sides: a positive test per admitted value
> that matters, and a negative test per excluded value that somebody will be tempted to add.** One
> negative test for the excluded set is not coverage of the predicate; it is coverage of half of it.
> The tell that you have only half is that every test in the series asserts `n == 0`.

**This is a third distinct way a mutation battery can look complete and not be**, and the three
belong side by side:

| lesson | what was wrong | what a battery run would have shown |
|---|---|---|
| `reference_mutation_proof_position` (2026-08-20 reconcile) | the poisoned input sat last, so early-exit mutations were unreachable | mutations survive, and it looks like the *assertions* are weak |
| `reference_mutation_battery_needs_green_baseline` (2026-08-20 watchdog) | the fixture bound `LIMIT 0`, so nothing was measured at all | every mutation "passes", uniformly |
| **this slice** | the battery covered exactly the predicates the tests named, and the tests named only rejections | **every mutation is killed** - the battery is green, complete, and blind to a whole direction |

The third is the most dangerous of the three because it produces a **perfect** result. M1-M4 all
reddened exactly the tests they were supposed to redden. There was no smell to notice. The only thing
that found it was a lens drawing a mutation the plan had not listed - the same move that produced the
`continue` -> `break` finding two slices ago. **The plan-supplied battery samples the space the plan
author imagined**, and both times what it missed was found by somebody sampling it independently.

### A footnote worth keeping: the caller guard and the fence guard are different tests

`TestDispatcher_SendFailureRequeuesWithRealFenceValues` is new in this slice and it guards the
**caller's arguments**, not the statement's predicates: de-fencing the SQL while leaving the arguments
bound would leave it green. That split is deliberate and correct - the store tests own the statement,
the scheduler test owns the wiring - and the shipped comment now names it explicitly ("No store-level
test can see that, because the store statement is perfectly correct"). What remains is the test's
**name**, which reads as if it covers the fence. A reader auditing coverage by test name will
mis-price it. Not worth a rename in a slice this size; worth knowing.

The hazard the test exists for is worth restating, because it is the thing that could have made this
whole slice ship inert: **adding a required field to a generated params struct silently binds a zero
value at every keyed literal that omits it.** `store.RequeueTaskParams{ID: claimed.ID}` compiles,
binds epoch 0 and a NULL worker, and the fence then matches nothing - forever, in silence. Mutating
either argument to a zero value left the whole scheduler suite green before this test existed.

## Two lenses, and what each contributed

The Phase 4 fan-out was deliberately scaled down to **two** lenses (invariants, correctness) instead
of four, on the grounds that this is a focused store-layer slice with no HTTP surface, no proto, no
`web/`, and no new package.

- **Invariants** produced the reachability refutation above (tracing `workerSender.Send`'s branches
  against the brief's story), and the out-of-scope observation that the fences close the window on the
  **write** side and cannot close it on the **execution** side - a late-but-successful dispatch send
  starts a subprocess for an assignment that has already been ended. Filed, below.
- **Correctness** produced the status-predicate mutation that exposed the missing positive arm, and
  the `serverSet` shape observation (two consumers now, so the value should be a struct carrying
  `pgtype.UUID` + `int32` rather than a string and an `int64`). Folded into an existing item, below.
- **`/code-review`**, conductor-run, produced `RequeueTask` - the largest finding in the slice.

**Was the scale-down the right call?** Two honest halves to the answer.

For the shipped diff, yes: the code is a four-predicate `WHERE` in two statements plus two call sites,
and a security lens on that would be re-deriving the invariants lens's argument, while an integration
lane is largely redundant with the full integration gate the conductor runs anyway (every test in this
slice is `//go:build integration`, so the integration lane's usual contribution - "run it for real
rather than trusting the report" - collapses into the gate).

For the **item**, no, and the shape of what was missed says why. What went uncaught was not a property
of the diff at all; it was a claim about the rest of the tree. The value a second independent reader
buys is precisely a second independent *sweep*, and the one step that did find it was the one the
scale-down did not touch. The generalizable rule is not "always run four lenses":

> **Scale the fan-out to the diff, but scale the verification tasks to the item.** When an item's
> central sentence is quantified over the tree, the plan owes a task that re-runs the search with an
> expected result, and that task is cheap enough that no lens budget can justify skipping it.

### `/code-review` diff shape and result, fourth data point

Recorded per the standing instruction from the two previous retros.

| | diff shape | `/code-review` | fan-out |
|---|---|---|---|
| 2026-08-14 cursor-pager | behaviour-preserving refactor, no logic delta | 0 | 2 (the most valuable in the slice) |
| 2026-08-20 reconcile | 8 behavioural lines under 45 lines of comment | 0 | 6 |
| 2026-08-20 watchdog | new migration, new query, new package file, new wiring, new send path | 3 | 4 |
| **2026-08-20 requeue fence** | **two SQL predicate sets, one generated file, two call sites, two new test files** | **1, and it was the slice's largest finding** | **2** |

The covariate continues to hold: the two zero-finding runs had essentially no new logic; the two
non-empty runs changed real predicates. This row adds something the previous three did not - the tool
found a defect **outside** the diff, in a statement nobody had touched, which is a category the lenses
had structurally excluded by scoping themselves to the change. Worth carrying forward: a clean
`/code-review` is still a lead rather than a verdict, but a `/code-review` finding that points *away
from the diff* deserves more weight than its severity label suggests, because nothing else in the
pipeline is looking there.

## The process shape: a batch that closed its own loop

**This is the third iteration of a three-item autopilot batch, and it closed an item the batch itself
filed.** Iteration 1 (reconcile-canonical-task-ids) found `RequeueTaskByID`'s missing fence twice,
independently, from opposite directions, and **carried it outward as an item rather than widening its
own scope**. Iteration 2 (the coordinator watchdog) checked the item's own written promotion condition
("if the watchdog is specced with the requeue shape, this becomes a hard prerequisite"), found the
fail shape had been chosen, recorded the check so nobody re-derived it, and left the item at medium.
Iteration 3 implemented it.

That is the discipline working end to end, and it is worth saying plainly because the alternative was
available at every step: iteration 1 could have fixed the statement inline (a two-line change in a
file it was already editing) and destroyed its own zero-wire-change guarantee; iteration 2 could have
promoted it on a hunch. Neither did. The item carried its own repro, its own callers-checked list, its
own promotion condition and its own open questions across three slices, and every one of those was
used.

**Phase 1 was collapsed into Phase 2, and that was right by both clauses of the test the previous two
retros built.** The item named its own open questions (what the caller does on zero rows; whether
`requeued > 0` should still gate `triggerDispatch`) - clause one. And answering them did **not**
require evidence the item lacked - clause two: the item itself pointed at the log-budget constraint
and at the `Handler.Metrics` seam, and the plan settled both by opening `internal/metrics/store.go`
and reading `finishRegister`'s ordering. Both resolved against the item's lead (the metrics counter it
suggested is the wrong shape *and* the wrong lifecycle), which is exactly the Phase-1 work happening
one stage later, in writing, before code.

**The clause the collapse test still does not have is the one this slice needed:** neither clause asks
whether the item makes a claim about anything other than the symbol it names. Proposed third clause,
stated as a question rather than a rule, because it is cheap to ask and expensive to skip: *does this
item assert anything about the rest of the tree, and if so, what search would falsify it?*

## What Was Built

- **`RequeueTaskByID` fenced on four predicates** - id (which row), `assignment_epoch` (currency),
  `worker_id` (identity, plain `=`), `status IN ('dispatched','running')` (still assigned at all).
  `:execrows`. Its doc comment names what the status allow-list alone did *not* cover and why the
  epoch and the worker predicates are not redundant with each other.
- **`RequeueTask` fenced on the same four**, with `status = 'dispatched'` deliberately narrower, and a
  doc comment that (a) records the false uniqueness claim and declares the sweep finished, (b) traces
  which send failure actually leaves a window and names the refuted "grace timer" story, (c) argues
  the narrow status list from the caller's own semantics and is precise about what that proof covers -
  "cannot for a well-behaved agent", not "cannot", because `handleTaskStatus` takes the epoch off the
  wire.
- **`reconcileRunningTasks` passes both fences it already held** - `serverSet`'s *value* is the epoch
  `GetActiveTasksForWorker` read under the same snapshot as the id, and `workerID` is the connection's
  authenticated worker resolved at registration. `requeued` counts matches.
- **`dispatchOne` passes both fences off ONE `RETURNING` row.** `claimed.AssignmentEpoch` and
  `claimed.WorkerID` come from the same `ClaimTaskForWorker` result, and the comment forbids
  substituting `w.ID` by name: it is equal today, but `ClaimTaskForWorker` does not reject a NULL
  worker argument and `tasks.worker_id` is nullable, so sourcing the pair from one row is what keeps
  the two facts from drifting apart.
- **The `triggerDispatch` gate's justification was rewritten during the slice and the refuted version
  is named.** The plan's argument for counting matches ("whoever ended the assignment already woke the
  dispatcher") is true for a legitimate release and **false when `n` is 0 because the statement
  errored**. The shipped argument is structural instead: `finishRegister` fires
  `go h.triggerDispatch()` unconditionally, so the gate is load-bearing on exactly one path - the
  `RegisterResponse` send failing and `finishRegister` returning early - and on that path zero matches
  means zero rows moved, so there is nothing to wake for.
- **Two new store test files, twelve tests**, mirrored per statement: the repro, then A (epoch
  isolated), B (wrong worker), C (zero-value worker, a behaviour pin), D (planted NULL/NULL row, the
  only real `=` versus `IS NOT DISTINCT FROM` discriminator), E (terminal not resurrected), F (the
  positive/negative arm of the status list). `newRequeueFence` is shared across both files.
- **`TestDispatcher_SendFailureRequeuesWithRealFenceValues`** - the first coverage `RequeueTask`'s
  production call site has ever had.
- **`TestTasksStatusVocabularyIsExactly`'s list went from seven sites to thirteen.** The five members
  of the "currently assigned" partition that were unlisted - `GetActiveTasksForWorker`,
  `ListGraceCandidates`, `RequeueTaskByID`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch` - are now
  named as a group with the inverted guidance spelled out, plus `RequeueTask` with its deliberate
  narrowness. The failure message now says **seven of thirteen fail open**, and traces `preparing`
  through all of them: invisible to reconcile, uncovered by grace, unmatched by every requeue,
  unswept by the watchdog, outside `idx_tasks_worker_active`.
- **Zero migration, zero proto, zero files under `web/`.**

## Key Decisions

- **`:execrows`, not `:exec`**, at both statements - so the fence tests can assert a rowcount rather
  than infer rejection from row state (row state alone is satisfiable by an UPDATE that matched and
  rewrote the same values), and so the reconcile caller counts matches.
- **The two callers handle the rowcount differently, on purpose, and the asymmetry is argued rather
  than copied.** Reconcile adds it to `requeued`; the dispatcher discards it. The dispatcher **has** a
  log budget and already logs the send failure unconditionally on the line above, so a second line
  saying the requeue moved nothing adds no information. Reconcile has no budget at all - it runs
  inside `finishRegister`, ahead of the connection's `ingestLogLimiter`.
- **No observability at either site**, deliberately. `Handler.Metrics` is a per-worker utilization ring
  buffer, and it is not even `Activate`d at reconcile time, so a counter call would be dropped by
  construction. The general gap stays with
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]]; do not close it here with a one-off.
- **Keep both status predicates exactly as they are** - `RequeueTaskByID` admits `running`,
  `RequeueTask` does not. Do not harmonize either way. Both directions are now pinned by tests and
  both are argued in the statements' comments.
- **The `t.ID` / `[16]byte` cleanup was NOT taken and the companion item was NOT narrowed.** The epoch
  arrives as `serverSet`'s value, so `for taskIDStr, srvEpoch := range serverSet` gets it with a
  one-token diff; holding `t.ID` would require re-keying both maps, which is a separate change with
  its own fail-closed trap.
- **No handler-level repro integration test.** The repro's essential content is a store property and
  is fully expressible there; staging a genuinely stale in-flight `serverSet` through `Connect` needs
  an export seam or a function extraction - symbols absent at HEAD - and this project's rule is that a
  test seam must not destroy the RED. The caller is pinned instead by an existing byte-identical test
  plus a mandated mutation.

## Findings Triage

- **1 finding against the item's central scope claim**, by `/code-review`, and it doubled the slice.
  Missed by the item, the plan and the implementer, all three of which had swept the family.
- **1 finding against the conductor's remediation brief** - a wrong causal mechanism that had already
  shipped into three files plus a test's numbered repro. Third consecutive slice with a defect in a
  conductor-authored artifact, and the first where the defect was a *story* rather than an omission.
- **1 finding against the test battery's direction** - every test asserted `n == 0`; narrowing the
  allow-list left four suites green, including a 440s api run. Its mirror image (widening
  `RequeueTask`'s list) was equally uncovered.
- **1 finding against the plan's `:execrows` justification** - true for the legitimate case, false on
  the error path. Corrected in place with the refuted version named.
- **2 findings carried outward** - the execution-side orphan window (filed) and the `serverSet` value
  shape (folded).
- **1 prose defect this slice created and did NOT fix, still in the tree**: CLAUDE.md's epoch-fence
  bullet says the identity predicate is carried by "`AppendTaskLog`, `UpdateTaskStatus` and
  `IncrementTaskRetryCount`". As of this slice there are **five**. The previous retro predicted this
  exact edit ("if item 1 is scheduled, that is the slice that touches the bullet - specifically the
  sentence about the `worker_id` predicate, which currently names three statements and would name
  four"), and the prediction was right about the sentence and wrong about the number. Flagged to the
  conductor as a one-clause fix in this slice's own scope, not filed. **The TPM lane may not edit
  CLAUDE.md.**
- **0 findings against the shipped predicates after remediation.**

This is the **eleventh consecutive iteration** in which wrong prose about correct code is the dominant
defect class. Its shape here is worth one line: of the four prose defects, three were **authored by
this slice** (the mechanism, the `:execrows` justification, and the over-claimed proof about what the
narrow status list covers) and were caught and corrected in place with the refuted versions preserved.
The fourth is inherited, sits in CLAUDE.md, and is still there.

## Recommended Backlog Items

**Filed this pass (proposal for human accept - the conductor commits, the human accepts):**

1. `bug-2026-08-20-a-late-dispatch-send-can-start-an-orphaned-subprocess` (**bug/low**) - the fences
   close the window on the **write** side and cannot close it on the **execution** side. A
   `registry.Send` that blocks on a wedged-but-registered sender and then **succeeds** delivers a
   `DispatchTask` for an assignment that another writer already ended, and the agent starts the
   subprocess. The coordinator's own state stays consistent (the old epoch's status updates are fenced
   out; the next reconnect's reconcile cancels it on the stale-epoch arm), so this is a resource and
   duplicate-execution residue, not a data-integrity hole. **The item is explicit about what is and is
   not reachable**, because the draft's mechanism did not survive tracing: on a single stream the
   cancel cannot overtake the dispatch (both go through `Registry.Send` into one FIFO `sendCh`), so
   the cancel-arrives-first variant needs the two messages on **different senders** - the
   second-`Connect` case. `handleCancel` drops a cancel for an unknown task id silently
   (`a.runners[msg.TaskId]`, `if ok`), and `handleDispatch` is what registers the runner. **Not scope
   creep:** this slice is what establishes that no requeue predicate can reach it.

**Folded into existing items rather than filed (evidence appended, `updated:` added, no scope
change):**

- `idea-2026-07-01-dead-status-vocabulary` gains the `CancelJobTasks` evidence. It already names that
  statement and its dead `'queued'` literal, so a new item would be a duplicate. What is new is that
  `TestTasksStatusVocabularyIsExactly`'s statement list grew from seven sites to thirteen **in this
  slice** and `CancelJobTasks` is still not among them - it appears in the test's preamble only, as
  the motivation for a future task-level `cancelled` status. So the guard that exists to force a
  per-site decision when the vocabulary moves would not name the one statement whose filter is already
  wrong. **One acceptance criterion added**, tightly bound to a statement the item already claims.
- `idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes` gains the second-consumer evidence.
  `serverSet` now has **two** consumers (the reported loop's epoch comparison and the requeue loop's
  fence argument), and this slice added an `int32(srvEpoch)` narrowing conversion that did not exist
  when the item was filed. The item's proposal is unchanged and un-narrowed - the loop still holds a
  string and still re-`Scan`s it - but the case for the change is stronger and the change now removes
  three things instead of two.

**Considered and NOT filed, with reasons:**

- **CLAUDE.md's three-statement enumeration.** In this slice's scope, one clause, flagged to the
  conductor. An item asking somebody to update an enumeration the current PR made stale would be
  stale before it was read. Note the standing "delete the count" lesson applies in its stronger form
  here: the right repair may be to stop enumerating and name the property, since the list has now been
  wrong twice.
- **`TestDispatcher_SendFailureRequeuesWithRealFenceValues`'s name.** The comment already states the
  caller/statement split correctly. A rename item would be churn.
- **A per-assignment fencing token at the agent**, which would eliminate the orphan residual entirely.
  Already a named non-goal in `tasks.sql`'s own comment and in the watchdog spec; filing it would
  contradict a shipped decision rather than record a gap. Cross-referenced from the filed item
  instead.
- **The remaining unfenced requeue statements** - `RequeueWorkerTasks` and `RequeueWorkerTasksIfEpoch`.
  Checked, not filed: the first is the admin-disable and disconnect path, which is *entitled* to take
  every task from a worker, and the second already carries the `connection_epoch` fence that closes
  its own race. Recording the check here so the next pass does not re-derive it - and note that this
  is exactly the sweep-by-shape the item's uniqueness claim needed and did not get.

## Known Limitations

- **The fences close the write side only.** A subprocess already started, or started by a late
  successful send, keeps running; the coordinator's writes from it are fenced out silently. The filed
  item covers the remaining window; the watchdog bounds its duration.
- **Zero-row fence rejections remain invisible at both callers.** By decision, with the reason at both
  sites, and tracked by [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] - now a
  five-statement motivation rather than four.
- **The whole slice is invisible to `make test`.** Every test added here is `//go:build integration`
  and needs Docker. The unit gate would stay green if all twelve were broken.
- **The production wiring is guarded only by mutation.** Both call sites are pinned by mutating an
  argument to its zero value and watching an existing test redden. That is real coverage, but it is
  coverage that only exists while somebody re-runs it; nothing structural prevents a future keyed
  literal from omitting a field.
- **No suite counts are reported in this retro.** The full integration gate was running against this
  tree while it was written, and this lane had no shell. Every number the conductor records belongs in
  the PR body, measured, and should say whether it is top-level or including subtests.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **sixteenth iteration**,
  and the most instructive result yet: five refutations, all correct, and the one unchecked claim was
  the one that mattered.
- **A backlog proposal is not a contract** - sixteen for sixteen, with a new failure mode (scope, not
  content).
- **An item's open question, framed with a lead, is a claim** - honored; both open questions resolved
  against the item's lead.
- **Plan-supplied tests and plan-supplied mutations are untrusted** - honored, and it paid for the
  third consecutive slice: the battery was complete for the predicates the tests named and blind to
  a direction.
- **A mutation proof must leave a test behind** - honored; the narrowing mutation left
  `TestRequeueTaskByID_RequeuesARunningTaskForItsAssignee`, the widening mutation left
  `TestRequeueTask_RunningTaskIsNotRequeuedByTheSendFailurePath`, and the zero-argument mutations left
  `TestDispatcher_SendFailureRequeuesWithRealFenceValues`.
- **A mutation battery needs a green, non-trivial baseline** - honored; the plan mandated it as an
  explicit step with the previous slice's `LIMIT 0` failure cited by name.
- **Wrong prose about correct code is the dominant defect class** - **eleventh consecutive iteration**;
  three of four instances were authored by this slice and corrected in place, one is inherited and
  outstanding.
- **Say what a fix does not buy in the same sentence that says what it does** - honored; the fence
  comments say the write side is closed and the execution side is not.
- **The correct fix for a stale count in a comment is usually to delete the count** - honored inside
  the tree, **violated in CLAUDE.md**, where an enumeration that has now been wrong twice is still an
  enumeration.
- **Record the diff shape with the `/code-review` result** - honored; fourth row added.
- **Prefer a symbol name to a line range in any cross-file citation** - honored throughout.
- **Backlog housekeeping is required scope** - the close of the source item belongs to the conductor.

New from this iteration:

- **A uniqueness claim ("the only X", "the last X", "no other") cannot be verified by opening X.** It
  is a claim about the complement and is falsifiable only by a search over the *shape*. When an item's
  central sentence is quantified over the tree, the plan owes a task that re-runs that search with an
  expected result. **Candidate for durable memory**; a sibling of "a backlog proposal is not a
  contract", not an instance of it - the diagnosis and the remedy were both right and the *scope* was
  wrong, and scope propagates into every downstream file list.
- **An allow-list must be pinned on both sides.** A positive test per admitted value that matters, and
  a negative test per excluded value somebody will be tempted to add. The tell that a battery covers
  only half is that every assertion in the series is `n == 0`. Third distinct way a mutation battery
  can look complete and not be, and the only one of the three that produces a *perfect* result.
- **A conductor-authored brief is an artifact, and a wrong causal story in one compiles.** The two
  previous conductor defects in this batch were incomplete enumerations, which the receiving lane
  caught. A mechanism cannot be caught that way - it must be traced. Write the brief's mechanism as a
  claim the receiving lane is asked to verify, not as context it is asked to act on.
- **Scale the lens fan-out to the diff, but scale the verification tasks to the item.** A two-lens
  Phase 4 was right for this diff and would never have found what was missed, because what was missed
  was not in the diff.
- **A `/code-review` finding that points away from the diff deserves extra weight.** Everything else in
  the pipeline is scoped to the change; that finding is the only signal from outside it.

## Files Most Touched

- `internal/store/query/tasks.sql` - read three comments in order: `RequeueTask`'s in full (the
  false-uniqueness record, the WHICH SEND FAILURE ACTUALLY LEAVES A WINDOW paragraph with its refuted
  version preserved, and the precision paragraph about what the narrow status list's proof does and
  does not cover), `RequeueTaskByID`'s WHAT THE STATUS ALLOW-LIST ALONE DOES NOT COVER paragraph, and
  `CancelJobTasks`'s `WHERE`, which still carries the dead `'queued'` literal.
- `internal/store/tasks_status_vocabulary_lockstep_test.go` - the THE ASSIGNMENT-PARTITION GROUP
  paragraph is the most valuable prose in the slice: it traces one hypothetical new status
  (`preparing`) through five statements at once and shows it holding a worker slot forever. Note the
  test now counts thirteen sites and says seven of them fail open.
- `internal/worker/handler.go` - the requeue loop's comment, specifically the paragraph that names the
  argument it *used* to make and why that argument was wrong on the error path, and the
  `triggerDispatch` gate comment that replaces it with a structural one.
- `internal/scheduler/dispatch.go` - the "do not substitute `w.ID`" paragraph: both fence values come
  off one `RETURNING` row so the pair cannot drift.
- `internal/store/requeue_task_fence_integration_test.go` - its file-header comment is the corrected
  reachability story in numbered form, and the cheapest way for a future reader to get it.
- `internal/store/requeue_task_by_id_fence_integration_test.go` - test F's comment states the
  narrowing mutation and the four suites it left green.
- `docs/superpowers/plans/2026-08-20-requeue-task-by-id-fence.md` - "Verification of the backlog item
  against HEAD" is the reusable part, and is worth reading twice: once for the five refutations it
  got right, once for the claim it never asked about.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane, and a full integration gate was
  running against this worktree, so nothing was executed: no `git log`, no `git diff`, no test run, no
  `sqlc generate`. Every claim below that could be checked by reading was checked against the
  worktree.
- **Verified by reading:** `RequeueTask` and `RequeueTaskByID` in full, statements and doc comments,
  in `internal/store/query/tasks.sql`, plus `ClaimTaskForWorker`, `GetActiveTasksForWorker`,
  `ListGraceCandidates` and `CancelJobTasks`'s `WHERE` (confirming the `'queued'` literal is still
  there); `reconcileRunningTasks`'s reported loop, requeue loop and `triggerDispatch` gate in
  `internal/worker/handler.go`; `dispatchOne`'s send-failure branch in `internal/scheduler/dispatch.go`;
  `Registry.Send`, `Registry.SendCancel` and `Registry.SendEvictCommand` in
  `internal/worker/registry.go`; `handleDispatch` and `handleCancel` in `internal/agent/agent.go`,
  confirming the runner map is populated by dispatch and that an unknown id is dropped silently;
  `internal/store/tasks_status_vocabulary_lockstep_test.go` in full, including the thirteen-site list
  and the failure message; both new store test files' test names and their A-F comments;
  `TestDispatcher_SendFailureRequeuesWithRealFenceValues` and `failingSender` in
  `internal/scheduler/dispatch_test.go`; every `RequeueTask(` and `RequeueTaskByID(` call site in the
  tree by grep (2 production, 14 test); CLAUDE.md's epoch-fence bullet, confirming it still names
  exactly three statements; the closing item in full; the plan in full; and the two prior retros for
  structure, for the mutation lessons and for the `/code-review` table.
- **The uniqueness-claim failure was confirmed by reading, not inferred.** The item's Summary contains
  the literal sentence; the plan's verification section contains seven rows and five refutations, none
  of which concerns any statement other than `RequeueTaskByID`; `RequeueTask` sits 115 lines above it
  in the same file.
- **Duplicate check run before the filed item and before each fold**, against every open item in
  `docs/backlog/`. The filed item has no neighbour: a grep for `orphan`, `subprocess`,
  `sendCancelSignals` and `fencing token` across the open set returns only
  `idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced` (an observability item
  about aggregating sweeps by worker, not about a dispatch/cancel ordering window) and
  `idea-2026-08-09-sse-revoked-token-keeps-streaming` (unrelated). The two folds each matched an open
  item on their central claim, which is why they are folds.
- **Reported by the implementing and verifying lanes, not re-run here:** every suite count; the M1-M4
  and W1/W2 mutation results; the narrowing mutation that left four suites green and the 440s api run;
  the byte-identical gate on `TestRequeueTaskByID_BumpsEpochAndFencesStaleUpdates` and on
  `internal/worker/handler_test.go`; the `make generate` read-back checks; `go build` and
  `go vet -tags integration`.
- **Not verified:** all test results, the commit set and diff stat, the change set as `git` sees it,
  and whether the generated `internal/store/tasks.sql.go` doc comments match their source (the plan's
  check 4 - the CRLF revert has silently discarded a regeneration in this repo before, and this lane
  could not run the greps that would confirm it).
- **The one item filed by this pass is in `docs/backlog/` as a proposal**; the human gives final
  accept. The two folds append a dated section, change no scope, and add only `updated:` to
  frontmatter plus one acceptance criterion on the vocabulary item. **Outstanding and belonging to the
  conductor:** the close of `bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence`
  (`/backlog close`, never a hand-edited `status:`), the CLAUDE.md epoch-fence enumeration, the
  exact-file-set check, the final gates, all commits, and a ROADMAP refresh.
