---
date: 2026-08-24
topic: finishregister-strand
slice: bug-2026-08-23-failed-finishregister-strands-worker-online (the only HIGH the 2026-08-23 deep run rated; led ROADMAP "Now")
branch: claude/pr-merge-session-d3977d
range: origin/main..HEAD (backend only; Go only; zero SQL, zero migration, zero proto, zero generated file, zero files under web/; green, not yet merged)
pr: finishregister-strand - not yet opened; reference this work by date and slug, never by a predicted number
closes: bug-2026-08-23-failed-finishregister-strands-worker-online
filed-this-slice: bug-2026-08-24-crash-looping-agent-defers-requeue-indefinitely
---

# Session Retro: 2026-08-24 - the one line deciding which release owns the generation had zero CI enforcement, and the guard written to close it was evaded twice

**TL;DR:** `finishRegister` now arms a deferred `releaseWorkerGeneration` the instant
`RegisterWorkerConnection` acquires the worker's generation, guarded by a `handedOff` flag flipped
immediately after `registry.Register`. The release body is shared with `teardownConnection` so the two
paths cannot drift. Ownership on the new path is `MarkWorkerOfflineIfEpoch`'s epoch fence and nothing
else. Along the way: `markWorkerOffline` grew an `(int64, error)` signature because collapsing the two
re-created the strand in one of its own trigger scenarios; `GraceRegistry` gained epoch-monotonicity
**and** an ABA identity guard in its fired-timer closure; and one `go/ast` guard exists because no
default-lane test in `internal/worker` can drive a successful registration at all. Unit 626 -> 638;
integration exit 0 (1049 pass); `-race` clean in a Linux container; the four new timer tests survived
300 runs each.

**There is no spec document for this slice.** Phase 1 collapsed into Phase 2, as it did for the
cross-generation 401: the item carried the design, and the plan absorbed the design-decision section
with its arguments. That was the right call here and the plan is the artifact to read.

This slice's durable rule is not new to the repository, only to this kind of test:

> **A structural guard must enumerate what is ALLOWED, not what is forbidden.** Both evasions of the
> handoff guard were constructs nobody had thought to forbid - `if h.Metrics != nil { handedOff = true }`
> and `if h.pool != nil { return }` ahead of the release. The guard held only once it required the
> deferred closure's **whole body** to be one of exactly two enumerated shapes. That is CLAUDE.md's own
> "write status predicates as allow-lists, never as the equivalent deny-list" arriving in a `go/ast`
> test, and it is the answer to the ladder question below.

---

## The headline: a mutant that fires on every SUCCESSFUL registration left 21 packages green

The whole fix reduces to one statement, `handedOff = true`. Delete it and every **successful**
registration takes its own deferred release: the worker it just brought online is marked `offline`,
`Metrics.Clear` wipes the entry `Metrics.Activate` created two lines later, and a grace timer is armed
against a live agent that requeues all of its running tasks one `RELAY_WORKER_GRACE_WINDOW` later. That
is a fleet-wide outage, and **it was measured to leave all 21 packages green.**

The cause is structural rather than an oversight. `.github/workflows/go-ci.yml` runs
`go test -race ./...` with no tags. Every test in `internal/worker` that drives a **successful**
registration is `//go:build integration`, and the default lane structurally cannot drive one:
`applyInventory` calls `pgx.BeginTxFunc(ctx, h.pool, ...)` unconditionally - even for an empty
inventory - and `h.pool` is a concrete `*pgxpool.Pool` with no interface seam, so a pool-less fixture
panics one statement past the reconcile. **The success path has no default-lane behavioural witness of
any kind**, and no amount of test-writing produces one without a seam.

So the substitute is `internal/worker/handler_handoff_guard_test.go`: an untagged `go/ast` guard over
`finishRegister` that derives the flag's name from the deferred closure (renaming moves the guard with
the code instead of defeating it), then checks that the flag is declared once and starts false, is
assigned `true` exactly once, that the assignment is a **direct statement of the function body** (not
nested), that it sits at or after the `stream.Send`, that it is the statement **immediately after**
`registry.Register`, and that no error return is positioned below it.

**Adjacency is the clause worth keeping.** A plain `handoff > registerPos` ordering check would admit
every position down to the success return, and any fallible statement that drifted into that gap would
be covered by `finishRegister`'s own release for the DB generation (the flag is still false, so that
half is correct) and by **nothing at all** for the registry - `Connect` arms
`defer h.teardownConnection` only on a nil error, so the `*workerSender` published one line up stays in
the registry forever, reachable by the dispatcher, wrapping a stream whose RPC has returned. The
infallible-below-the-flip check cannot see that statement either; it only inspects returns positioned
**below** the flip. Adjacency is what makes the two checks meet with no unguarded region between them.

### It was evaded twice, and the second evasion was worse than the first

1. **`if h.Metrics != nil { handedOff = true }`.** Compiles. `h.Metrics` is nil for every handler
   `NewHandler` and `NewHandlerWithGrace` build, so in production the flag is never set and every
   successful registration releases its own generation. Closed by `directBodyStmt` - the flip must be a
   statement of `finishRegister`'s own body, which rejects a conditional wrap, a closure wrap and a
   `defer func(){}()` wrap alike without the test having to reason about what each wrapper does.
2. **`if h.pool != nil { return }` nested inside the guarded branch of the deferred closure.** This one
   inverts: nil in the default-lane fixture (`newStrandHandler` leaves `pool` nil deliberately, which is
   the whole reason that fixture works), non-nil under `main.go`. So the entire package stays green
   while **production releases nothing on any failed registration** - the original bug, restored, behind
   a passing guard.
3. The engineer found a third shape itself while fixing the second, before it shipped.

Evasion 2 is the interesting one because it defeats the obvious remedy. Counting calls to
`releaseWorkerGeneration` recursively still returns 1. Checking that the release is *reachable* still
passes. What closes it is `isCallTo` versus containment: the guarded branch must **be** exactly one
expression statement calling the release, and the closure's whole body must be one of two enumerated
forms:

```go
defer func() { if !flag { h.releaseWorkerGeneration(...) } }()
defer func() { if flag { return }; h.releaseWorkerGeneration(...) }()
```

Both ordinary spellings are accepted, because failing one of them would be dictating style while
reporting a defect. Everything else is rejected with a message that prints both accepted forms rather
than naming which clause noticed.

### Does the ladder have a stable top rung? Yes, and it is not the one from slice 4

Counting the escalation family: thirteen evasions across #139-#142, three in slice 4, and two here
(the third shape was caught before it shipped, so it does not count as an evasion of a shipped guard) -
**the seventeenth and eighteenth.**

Slice 4's rung was *point the guard at the right subject*: its predicate was correct and had never seen
a byte the producer emitted, so the fix was to move the check to a package that could import both sides.
**This slice's evasions were not respellings and not misaimed subjects.** Both were semantically valid
rewrites that added a construct the guard had **no opinion about**. A guard that enumerates forbidden
shapes is unbounded by construction: the set of things a rewriter can add is infinite, and each new
member is discovered by someone doing it.

The rung that holds is therefore the repository's own allow-list rule, already written down for status
predicates and not previously read as advice about tests: **enumerate the permitted shapes and reject
everything else.** A deny-list guard fails open on the next construct anyone invents; an allow-list
guard fails closed and says what it wants. I do not expect a nineteenth evasion of *this* guard. I
expect the nineteenth evasion to be of some other guard still written as a deny-list.

---

## The bug was worse than filed, and the plan refuted six of the item's supporting claims

The item was filed at `medium` by a read-only review lens on 2026-08-23 and had **never been
reproduced**. Every line number in it predated the tree. The three material corrections:

- **The 24h stale-task watchdog marks the stranded tasks `timed_out`, it does not requeue them.** The
  item said "no requeue happens until the assignment watchdog fires". The watchdog writes a terminal
  status and cascades dependents. So the backstop does not eventually re-run the work; it **fails** it a
  day later.
- **The watchdog never writes `workers.status` at all.** It only touches `tasks`. So for the worker row
  there was no 24h backstop and no backstop of any kind - it stayed `online` until the next successful
  registration or a process restart.
- **The one runtime mechanism that would have noticed was disabled by the same missing line.** The
  metrics liveness sweeper flips a connected-looking worker `online` -> `stale`, but skips any worker
  `LastSampleAt` reports as untracked. `Metrics.Activate` sits **below both failure points**, and the
  previous disconnect's `markWorkerOffline` had already called `Metrics.Clear`. The sweeper walked past
  the stranded worker **by construction**. This is the finding that turned "worth deferring to an
  existing sweeper" into "worth shipping".

Three more corrections changed the plan's shape rather than its justification: the item's implied "the
reconcile arm needs a database fault" is wrong (`finishRegister`'s ctx is `stream.Context()`, so a
vanished peer triggers it exactly as it triggers the send arm, a few lines earlier); partial
self-healing exists but only where the bug does not matter (an agent that reconnects, when the trigger
is an agent that vanished); and every cited line number was stale.

**Twenty-first consecutive iteration in which planning-phase verification caught something material
before a line was written.**

---

## A default-lane reproduction was achievable, and was taken

Worth contrasting with the last five slices, whose headline proofs were integration-only and weaker for
it. Here the reconcile arm is provable **without Docker**, and the reason is mechanical rather than a
design preference: `finishRegister` returns on `reconcileRunningTasks`' error four lines **above** the
`applyInventory` call that needs a concrete `*pgxpool.Pool`. So a hand-written `store.DBTX` driving
`GetWorkerByAgentTokenHash`, `RegisterWorkerConnection`, `GetActiveTasksForWorker` and
`MarkWorkerOfflineIfEpoch` is a complete fixture for that arm, and a nil pool would panic one statement
later. That seam is `tasklog_fence_counter_test.go`'s precedent one layer up, combined with
`handler_registration_deadline_test.go`'s `scriptedStream`.

The send arm genuinely cannot join it, for the same reason, and its proof is integration-tagged. The
plan says so explicitly and forbids "fixing" it by making `h.pool` an interface inside this slice. That
restraint was correct for the slice and is now the strongest evidence for the seam item recommended
below: the price of not having the seam was a 500-line parser guard and two evasions.

Five of the six new default-lane tests are ordinary behavioural tests. The sixth,
`TestReleaseWorkerGeneration_WithoutAGraceRegistryRequeuesImmediately`, covers an arm that **no test
reached** - deleting the `else` outright left the whole default package green - and that is not
reachable in production today, since `main.go` always builds through `NewHandlerWithGrace`. It is
covered rather than deleted, with the reason written down: it is the arm that decides what happens when
the grace window is configured away, and an untested branch that ends a worker's generation is exactly
the kind that comes back wrong.

---

## The fix was a silent no-op in one of its own two trigger scenarios

The plan shipped `if h.markWorkerOffline(workerID, epoch) == 0 { return }`, with a careful argument for
why the early return is a correctness control rather than an optimisation. The argument is right. The
signature was not.

`markWorkerOffline` returned `0` for **three distinct causes** - an unparseable id, a query error, and
the genuine fence miss - and the caller read all three as "a fresher connection holds the epoch". The
reconcile arm this release exists to clean up after fails for exactly two reasons: a cancelled peer
context, or **a database fault**. In the second case the release's own write goes to the same unhealthy
pool, returns an error with a meaningless rowcount, and the release gives up. **The strand is
re-created, silently, in one of its own two trigger scenarios.** Found by the integration lens and
reproduced live against real Postgres.

The remedy is stronger than the one first proposed. `markWorkerOffline` now returns `(int64, error)` and
`releaseWorkerGeneration` reads `err == nil && rows == 0` - a zero is believed only when the fence was
actually evaluated. On an error it **proceeds**, because both continuations carry their own fence:
`grace.Start`'s expiry runs `RequeueWorkerTasksIfEpoch`, and `requeueWorkerTasks` calls that statement
directly. A release that really had been superseded costs a fenced no-op; giving up cost a permanent
strand.

> **A failed check and a check that failed to run are opposite conclusions from the same value.** Where
> a predicate can return "no" and "I could not ask" as the same zero, the signature is the bug. Decide
> which way the unknown fails, and prefer the direction whose worst case is a fenced no-op.

The unparseable-id branch was then re-read in that light and **deliberately** returns an error rather
than `(0, nil)`, so it too proceeds. The residue is one grace entry that deletes itself when the window
elapses. The comment says so, and says why the tidier-looking alternative would spell "unparseable" as
"the fence said no".

---

## GraceRegistry had two defects, not one, and the second was found by asking what the first leans on

The slice's own new comment asserted that `GraceRegistry` was epoch-monotonic. It was not. Fixed here
rather than filed, per the rule about latent holes in the exposing change.

1. **A delayed superseded release could evict a live generation's timer.** `releaseWorkerGeneration`
   checks the fence inside Postgres and then calls `grace.Start` on the result - a database round trip
   apart, with nothing held across the gap - so a fresher connection can register, disconnect and arm
   its own timer in between. The stale caller's `Start` replaced the live entry; the stale entry then
   fired against `RequeueWorkerTasksIfEpoch`'s own fence, matched zero rows, and **that worker's tasks
   were requeued by nobody** until the 24h watchdog failed them. `StartWithDuration` now refuses a
   **strictly** older epoch. Equal epochs still replace, or `Start`'s idempotent-with-reset semantics
   are lost - and that half needed its own test.
2. **A timer that had already fired could walk in afterwards and delete whatever entry replaced it.**
   This was found by asking what the monotonicity rule leans on: refusing a stale `Start` is worthless
   if a fired timer can evict the winner, and `Stop` is exactly the call that cannot help once a timer
   has fired. The `AfterFunc` closure now checks entry **identity** before deleting.

The second guard's test pair is a good artifact. `AFiredTimerDoesNotEvictTheEntryThatReplacedIt` uses
differing epochs, and the obvious weakening - comparing epochs instead of identity - **survives it**
(2 != 1 refuses the stale closure just as identity did). `AFiredTimerDoesNotEvictItsSameEpochReplacement`
makes the two epochs equal, which is the case an epoch comparison cannot distinguish, and kills the
weakening. The window is **reproduced rather than raced for**: resetting the first entry's timer to zero
after the replacement has landed runs the very same closure against the very same map state, on every
run rather than on the unlucky ones.

`grace.go` now states, with the mutation results, that the two controls are **independent** rather than
one propping up the other: deleting the refusal fails only the stale-epoch test, deleting the identity
check fails only the two fired-timer tests.

---

## The plan's own fixture made the headline assertions vacuous, and argued for it in a comment

The plan's `strandWorkerRow.Scan` filled by destination type, giving every `int32` column the same
constant, and its comment defended the choice:

> "fill by DESTINATION TYPE rather than by position means a reordered column list cannot turn a success
> into a failure; the cost is that cpu_cores, ram_gb, gpu_count and max_slots come back as 7 too, which
> nothing on this path looks at."

Both halves of that sentence are true. The stated cost is not the cost. `store.Worker` has several
`int32` columns, so with one constant `releaseWorkerGeneration(workerID, updated.MaxSlots)` produces
**byte-identical behaviour** to `updated.ConnectionEpoch` - and `assert.Contains(execs[0].args,
strandEpoch)` and `assert.Equal(strandEpoch, f.epoch)`, the two assertions the whole default-lane proof
rests on, become assertions about nothing. What is under test on this path is not "some epoch was used"
but "**the** epoch this registration created was used", and only distinct values can tell those apart.

Shipped instead: `strandInt32Base` with each successive `int32` column getting the next integer, and
`strandEpoch` **derived by reflection** over `store.Worker`'s field order rather than written down, so a
column added or reordered in a migration moves the fixture and the constant together instead of silently
re-pointing the constant at `max_slots`. `TestStrandFixture_EveryInt32ColumnScansDistinct` pins
distinctness (not arity - it walks whatever the scan produced, so a new column cannot falsify a number
written in a comment). And the `Scan` switch gained a `default:` arm that **errors** on an unmodelled
destination type, which is slice 2's arity lesson applied by construction to a hand-written fixture.

Two named patterns fired at once here: plan-supplied tests are untrusted, and a principle in a comment
is not a check - this time the comment was not merely silent about the bug, it **argued for it**.

---

## Prose asserting properties the code lacks, two slices running, three times inside the remediation

Fifteenth consecutive iteration for the class in slice 4; **sixteenth** here, and it recurred inside the
fixing rounds rather than only in the original:

- **Round 1's fixes produced three of round 2's four findings.**
- **Round 2 invalidated its own prose.** Deriving `strandEpoch` by reflection turned five comments that
  named a literal epoch into false statements - three of them inside **assertion failure messages**,
  which is the worst place for one, since it is read only by someone already debugging.
- **Round 3 was entirely guard and prose, with no behavioural change.**
- The engineer **caught and corrected one false claim it had just written** (`eb89dcc`), which is the
  first time in this batch that the loop closed inside the implementing lane rather than at a review.

Two prose decisions worth keeping as examples of the corrected shape:

- The `grace.Cancel` comment now says the release's placement above it is **defensive, not required** -
  `GraceRegistry.Cancel` cannot fail, so moving the flag below is behaviour-preserving today and
  mutation confirms it. Saying which it is matters: the opposite claim invites someone to rely on it.
- The handoff comment says the constraint is a **range, not a point**, that the guard pins a point
  anyway, and exactly what the extra strictness buys. The item's third acceptance criterion - re-read
  the wake-gate comment - resolved the same way: that sentence turned out **still literally true** after
  the fix, so it gained a paragraph rather than a rewrite.

---

## A plan defect worth recording on its own

The plan's Task 3 Step 3 proof recipe was:

```bash
git stash push internal/worker/handler.go && go test -tags integration ... ; git stash pop
```

By the time the engineer reached Task 3, Task 2 Step 7 had already **committed** `handler.go`. So
nothing was stashed, and the `pop` popped a **pre-existing unrelated stash belonging to the user**.
Nothing was lost - `git stash pop` only drops the entry on full success and this one aborted - but the
margin was thin and the failure mode is silent.

> **A plan's proof recipe must not assume uncommitted state when the plan's own task order commits
> first.** The safe form is `git checkout <sha> -- <path>`, which names what it is reverting to and
> touches no shared stack. `git stash` is a global, unnamed LIFO shared with the human's own working
> state; a plan step is the wrong place to push onto it.

---

## Process shape

- `/code-review` (conductor-run) and a four-lens fan-out (invariants, correctness, security,
  integration-tester).
- **Three remediation rounds.** Round 1's fixes produced three of round 2's four findings. Round 3 was
  guard and prose only.
- The integration lens reproduced the `markWorkerOffline` silent-no-op **live against real Postgres**,
  which is the finding that changed the fix's signature.
- **Every iteration of the last five has needed at least one remediation round; this one needed three.**
  That number is still not improving, and the honest reading is unchanged from slice 4: the fan-out is
  what makes these slices correct, not the implementation pass.

---

## What Was Built

- **`internal/worker/handler.go`**
  - `finishRegister`: `handedOff` plus the deferred `releaseWorkerGeneration`, armed immediately after
    `RegisterWorkerConnection` returns and before the `grace.Cancel` that destroys the previous
    disconnect's requeue timer; the flag flipped in the statement immediately after `registry.Register`.
  - `releaseWorkerGeneration(workerID string, epoch int32)` extracted out of `teardownConnection`, so
    the failed-registration path and the stream-ended path are provably the same code. Reads
    `err == nil && rows == 0`.
  - `markWorkerOffline` now returns `(int64, error)`; the two unfenced side effects (the `offline`
    broker publish and `Metrics.Clear`) stay gated on the write having actually applied.
  - Five comments corrected at their true scope: `Connect`'s teardown defer ("it covers everything below
    and nothing above"), the `grace.Cancel` block, the wake gate inside `reconcileRunningTasks`
    (appended to, not rewritten), and the doc comments on `markWorkerOffline` and `requeueWorkerTasks`,
    both of which had described themselves as disconnect-only.
  - The handoff block additionally documents what the guard **cannot** reach: a panic below the flip
    escapes both releases, and `go h.triggerDispatch()` panics on a new goroutine where none of this
    function's defers exist. Disclosed with the durable consequence spelled out, not closed.
- **`internal/worker/grace.go`** - `StartWithDuration` refuses a strictly older epoch; the `AfterFunc`
  closure checks entry identity before deleting. `ExpireNow`'s exemption from the monotonicity rule is
  argued from a property of `ListGraceCandidates` (a `SELECT DISTINCT` whose non-key columns come from
  one `workers` row, so no id repeats in that loop) rather than from "the map is empty", which is true
  of the first candidate only.
- **`internal/worker/handler_register_strand_test.go`** (new, no build tag) - `strandDB` over
  `store.DBTX`, the reflection-derived `strandEpoch`, and six tests: the strand reproduction, the
  superseded-release guard (including the two unfenced side effects), the errored-write case, the
  fixture-distinctness pin, the previous-disconnect-timer replacement, and the `h.grace == nil` arm.
- **`internal/worker/handler_handoff_guard_test.go`** (new, no build tag) - the parser-level ownership
  guard described above, plus `returnStmts` / `returnsNil` / `directBodyStmt` / `stmtIndexContaining` /
  `isCallTo` / `countCallsNamed` / `onlyCallOnReceiver` / `paramNamedByType` / `findFuncDecl`.
- **`internal/worker/handler_register_strand_integration_test.go`** (new, `//go:build integration`) -
  the `RegisterResponse`-send arm end to end: real worker row, real claimed task, `onExpire` wired
  exactly as `main.go` wires it, asserting `offline` at an **unbumped** epoch with `disconnected_at`
  restored, and the task back to `pending` with `assignment_epoch` bumped. The agent reports its running
  task deliberately: with an empty `RunningTasks`, reconcile requeues the task before the send is
  attempted and the test passes at HEAD for the wrong reason.
- **`internal/worker/grace_test.go`** - four new timer tests (stale-epoch refusal, same-epoch reset, and
  the two fired-timer ABA cases).
- **Zero SQL, zero migration, zero proto, zero generated file, zero files under `web/`.**

## Key Decisions

- **A `defer` plus a flag, not a call at each error return.** Two error returns exist today; writing the
  release at each is more visible and is exactly the shape that rotted into this bug, because the next
  early return added below will not have it.
- **The flag is named `handedOff`, not `registered`** - it names the ownership transfer, and
  "registered" already means three other things in this file.
- **The release does BOTH offline and a fresh grace timer**, not either. Two things were acquired and
  both must be given back. The discarded timer cannot be restored: it carried the **old** epoch, and
  `RequeueWorkerTasksIfEpoch` fences on `workers.connection_epoch`, so re-arming at the old epoch would
  move zero rows - a silent no-op that looks like a fix.
- **The epoch fence is the whole ownership check on the new path, and no registry call is added.** A
  failed registration has no sender to identity-check, and unregistering one it never registered would
  be the clobber the identity-checked-teardown invariant forbids.
- **Proceed on a write error.** Both continuations are independently epoch-fenced, so a genuinely
  superseded release costs a fenced no-op where giving up costs a permanent strand.
- **`GraceRegistry` fixed here rather than filed**, because this slice's own comment asserted the
  property it lacked.
- **The crash-loop case filed rather than fixed**, with the limit written into the code beside
  `grace.Cancel` rather than left in the backlog only.

## Findings Triage

- **1 HIGH, self-inflicted and fleet-scale: `handedOff` had no CI enforcement.** Deleting it left 21
  packages green and made every successful registration requeue a healthy agent's tasks. Closed by a
  parser-level guard, **evaded twice** before it held.
- **1 HIGH, correctness: the fix was a silent no-op when the offline write errored** - one of the
  reconcile arm's own two trigger scenarios. Reproduced live against real Postgres.
- **1 HIGH, latent in the exposing change: `GraceRegistry` was not epoch-monotonic**, and a second,
  independent ABA hole was found by asking what the first control leans on.
- **1 MEDIUM: the plan's fixture made both epoch assertions vacuous**, with a comment arguing for the
  choice.
- **6 item claims refuted by the plan**, three of them changing the fix's justification.
- **3 remediation rounds**; round 1's fixes produced three of round 2's four findings.
- **1 plan defect with a real-world side effect** (the `git stash pop` against the user's own stash).
- **1 known limitation filed rather than fixed** (crash-loop deferral).

## What Remains Open

- **[[bug-2026-08-24-crash-looping-agent-defers-requeue-indefinitely]]** - an agent that re-registers
  and fails faster than `RELAY_WORKER_GRACE_WINDOW` gets `Cancel` then a fresh `Start` every cycle, so
  the requeue is pushed out indefinitely. Not a regression (the pre-fix outcome was identical and
  worse), and the limit is documented in `handler.go` beside `grace.Cancel`. This slice closes the
  single-shot case.
- **A panic below the handoff flip escapes both releases**, and `go h.triggerDispatch()` panics on a
  goroutine where none of `finishRegister`'s defers exist. There is no `recover()` and no gRPC recovery
  interceptor anywhere in this tree (verified by grep: the only `recover()` calls are two in
  `internal/api/server_test.go`). **Recommended as an item below.**
- **What survives such a crash is the durable half**: the `workers` row is `online` at a live
  `connection_epoch` with no connection behind it, and **nothing at startup corrects it** -
  `seedGraceTimersFromActiveTasks` requeues that worker's tasks and never writes `workers.status`.
  This generalizes past panics to every ungraceful restart. **Recommended as an item below.**
- **`internal/worker`'s default lane cannot drive a successful registration at all.** The mechanical
  cause is `h.pool` being a concrete `*pgxpool.Pool` with no seam. **Recommended as an item below**,
  plus an amendment to the existing lane item.
- **`TestReleaseWorkerGeneration_WithoutAGraceRegistryRequeuesImmediately` covers a branch unreachable
  in production.** Covered deliberately, with the reason in the test comment. Not proposed as an item;
  the decision is recorded where the next reader will hit it.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **twenty-first iteration**.
  Six refutations, three of which changed the fix's justification.
- **A backlog proposal is not a contract** - twenty-one for twenty-one. The item's proposal ("arm the
  teardown at the point `RegisterWorkerConnection` succeeds") was directionally right and understated
  the damage three ways.
- **Each stage treats the previous stage's output as untrusted** - honored in all three directions: the
  plan refuted the item, the lenses refuted the plan's shipped signature and its fixture, and the
  re-verify rounds refuted the remediation.
- **A guard that counts a PROPERTY, not a spelling** - honored and **extended**: a guard can count the
  right property and still be a deny-list, which is unbounded by construction.
- **A mutation proof must leave a test behind** - honored; `"UPDATE 0"`, the errored-Exec fixture, the
  equal-epoch fired-timer case and the distinctness pin are all permanent.
- **A mutation proof is only as strong as the poisoned input's POSITION** - honored, and the
  fired-timer pair is a clean instance: the differing-epoch case cannot detect the epoch-comparison
  weakening, so the equal-epoch case exists to.
- **Wrong prose about correct code is the dominant defect class** - **sixteenth consecutive iteration**,
  and it appeared three times inside the remediation.
- **Say "declined, and here is the price"** - honored at both sites (the `h.pool` seam, declined with
  the price now measured; the crash-loop case, declined and filed with the limit written into the code).
- **State a coverage limit rather than implying it** - honored: the handoff comment names what the guard
  cannot reach (a panic), and the guard's own header names the two failure modes it substitutes for.
- **Backlog housekeeping is required scope** - the close and the `git mv` belong to the conductor, via
  `/backlog close`, never a hand-edited `status:`.

New from this iteration:

- **A structural guard must enumerate what is ALLOWED, not what is forbidden.** Every evasion added a
  construct the guard had no opinion about, and the set of such constructs is infinite. Requiring the
  guarded body to be one of an enumerated set of shapes fails closed. This is CLAUDE.md's own
  allow-list-not-deny-list rule, arriving in a `go/ast` test. **Candidate for durable memory.**
- **A failed check and a check that failed to run are opposite conclusions from the same value.** Where
  a predicate returns "no" and "I could not ask" as the same zero, the signature is the bug. Decide
  which way the unknown fails, and prefer the direction whose worst case is a fenced no-op.
- **When the runnable lane structurally cannot witness a behaviour, the substitute is a different kind
  of claim, and its cost should be counted.** A ~500-line parser guard, evaded twice, is what a missing
  test seam costs. "File an item if a future slice needs it" is the right rule; this was that slice, so
  the item is filed.
- **A plan's proof recipe must not assume uncommitted state when its own task order commits first.**
  `git stash` is a global unnamed LIFO shared with the human's working state; `git checkout <sha> --
  <path>` names what it reverts to and touches nothing shared.
- **Ask what a new rule leans on.** Epoch-monotonicity is worthless if a fired timer can evict the
  winner. The second `GraceRegistry` defect was found by that question and by nothing else.

## Files Most Touched

- `internal/worker/handler.go:589-659` - the acquisition, the armed release, and the `grace.Cancel`
  block whose comment states the defensive-not-required distinction and the crash-loop limit.
- `internal/worker/handler.go:689-755` - the handoff block. The range-not-a-point argument, what
  forbidding the drift buys, the infallible-below rule, and the disclosed panic boundary. This is where
  the next person to touch `finishRegister` will land.
- `internal/worker/handler.go:1478-1532` - `releaseWorkerGeneration`'s doc comment: the epoch as the
  whole ownership check, why a zero is believed only when the fence was evaluated, and why the early
  return buys less than it looks (with `GraceRegistry` carrying the rest).
- `internal/worker/handler_handoff_guard_test.go:12-69` and `:346-374` - the header's two failure modes,
  and `handoffFlagIdent`'s argument for pinning the closure's whole body, which carries both measured
  evasions.
- `internal/worker/grace.go:53-104` - the monotonicity rule, the ABA identity guard, and the measured
  independence of the two.
- `internal/worker/handler_register_strand_test.go:102-179` - `strandInt32Base`, the reflection-derived
  `strandEpoch`, and the `default:` arity arm. The vacuous-assertion story is here.
- `docs/superpowers/plans/2026-08-24-finishregister-strand.md` - the verification log (six refutations),
  the design-decisions section that stood in for a spec, and the test-lane decision.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No `git log`,
  no `git diff`, no test run. Every claim below that could be checked by reading was checked against the
  worktree.
- **Verified by reading:** `internal/worker/handler.go` at `:250-299`, `:580-769`, `:1455-1584` and
  `:1629-1701`; `internal/worker/grace.go` in full;
  `internal/worker/handler_handoff_guard_test.go` in full;
  `internal/worker/handler_register_strand_test.go:30-541`; `internal/worker/grace_test.go:150-279` and
  its full test inventory; `internal/metrics/sweep.go` in full; `cmd/relay-server/main.go:335-361`;
  `.github/workflows/go-ci.yml` in full; the plan in full; the closed item in full; the newly filed
  crash-loop item in full; and `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`
  for duplicate checking.
- **Confirmed against code, not inferred:** that CI runs `go test -race ./...` with no tag and only
  `make vet-integration` for the tagged build; that `releaseWorkerGeneration` reads
  `err == nil && rows == 0` and `markWorkerOffline` returns `(int64, error)`; that `handedOff` is
  flipped in the statement immediately after `h.registry.Register`; that the handoff guard rejects a
  containment-only release via `isCallTo` and constrains the closure body to two enumerated shapes; that
  `StartWithDuration` refuses only a **strictly** older epoch and the `AfterFunc` closure compares entry
  identity; that there is **no** `recover()` in production code and **no** `Interceptor` anywhere in
  `*.go`; that `MarkWorkerOfflineIfEpoch` has one production caller and `SetWorkerStatus` has exactly
  one (the metrics sweeper, which skips untracked workers); and that `seedGraceTimersFromActiveTasks`
  writes no `workers.status`.
- **Reported by the implementing and verifying lanes, not re-run here:** unit 626 -> 638; integration
  exit 0 with 1049 passing; the `-race` Linux-container run; the four timer tests at 300 runs each with
  zero failures; the commit set; and every mutation result, including the 21-packages-green deletion of
  `handedOff = true` and both guard evasions.
- **One arithmetic discrepancy the conductor should resolve:** reading the tree I can enumerate
  **eleven** new default-lane tests (six in the strand file, one handoff guard, four in `grace_test.go`)
  against a reported top-level delta of **twelve** (626 -> 638). The gap is one test I did not locate by
  reading; it is not evidence of a problem, but the file set should be confirmed with `git diff --stat`
  before the PR is assembled.
- **Not verified:** all test results, the commit count, the diff stat, and the change set as `git` sees
  it. Each is attributed above.
- **`-race` toolchain note worth carrying forward:** this slice reports that on the Windows host,
  `-race` needs `PATH=/c/msys64/mingw64/bin:$PATH` **in addition to** `CC=/c/msys64/mingw64/bin/gcc.exe`.
  The CC-only recipe recorded in memory no longer suffices. Worth amending
  [[reference_race_detector_toolchain]].
- **No PR number appears anywhere in this retro or in the proposed items**, by instruction. The work is
  referenced by date and slug.
- **Outstanding and belonging to the conductor:** the `/backlog close` run with its `git mv` into
  `docs/backlog/closed/`, the item filings and the one amendment below, the CLAUDE.md decision, the
  file-set check, the final gates, all commits, and a ROADMAP refresh.

## CLAUDE.md verdict

**Two amendments are earned, both to existing bullets, and they are the two halves of one change.** I
recommend taking both; if only one, take the first.

Unlike slice 4's declined candidates, neither of these is compiler-enforced and neither is a
one-instance lesson. The acquire-direction reading is the **second** defect this exact registration
window has produced in this class - the 2026-06-20 `finishregister-gap-connection-epoch` work codified
the release direction and left the acquire direction open - and it is bypassable in precisely the way
CLAUDE.md's Invariants exist to name: a future early return added between the acquisition and the
handoff silently reopens it.

**Amendment 1 - append to "End the generation before releasing the resource":**

> **Read the same rule in the acquire direction: arm the release in the same breath as the
> acquisition.** The statement that takes shared state - a row flipped to `online`, a bumped
> `connection_epoch`, a cancelled timer - must be immediately followed by the unconditional code that
> gives it back, which in Go means a `defer` at the acquisition site and not a call at each error
> return. The counter-example is the one this clause was written from: `finishRegister` acquired the
> worker's generation via `RegisterWorkerConnection`, and `Connect`'s `defer h.teardownConnection`
> covers everything BELOW the registration and nothing above it, because it needs a `*workerSender`
> only the success path creates - so a registration that failed in between left the worker `online` at
> a live epoch with its grace timer cancelled and its tasks assigned to a connection that did not
> exist, for as long as the process lived. Where two defers partition a window like that, the flag
> deciding which one owns the generation is the whole correctness argument: flip it at exactly one
> place, keep it adjacent to the handoff, and require everything below it to be infallible. **Check the
> flag.** Deleting `handedOff = true` left all 21 packages green, because no default-lane test in
> `internal/worker` can drive a successful registration at all;
> `TestFinishRegisterHandsOffOwnershipInsideTheWindow` exists for that reason, and it was evaded twice
> before it held - by `if h.Metrics != nil { ... }` and by `if h.pool != nil { return }`, both of which
> are nil-in-the-fixture and non-nil-in-production or the reverse. **A structural guard must enumerate
> the shapes it ALLOWS**; a guard that lists forbidden shapes is a deny-list and fails open on the next
> construct anyone invents, exactly as the status-predicate rule below says.

**Amendment 2 - append to "Identity-checked teardown":**

> **Where there is no identity to check, say so and name what replaces it.** A registration that failed
> before `registry.Register` has no sender to compare against, so `releaseWorkerGeneration`'s epoch
> fence is the entire ownership check on that path; adding a registry call there to make the two paths
> symmetric would unregister a sender the caller never registered, which is the clobber this rule
> forbids. **And a fence decides ownership only when it was actually evaluated.** `markWorkerOffline`
> returns `(rows, error)` because a zero rowcount that arrived with an error is not "a fresher
> connection holds this worker", it is "I could not ask" - and collapsing the two made the release give
> up in exactly the scenario, a database fault, that had caused the failure it was cleaning up after.
> When a check cannot run, decide which way the unknown fails and prefer the direction whose worst case
> is a fenced no-op: `releaseWorkerGeneration` proceeds on error, because both continuations carry
> their own `connection_epoch` guard.

## Recommended Backlog Items

Proposals only - the conductor files via `/backlog`, and the human gives final accept. All four are
high-confidence; every factual claim below was verified by reading the worktree in this pass, and the
verification is named in each.

**1. `Handler.pool` has no seam, so the default lane cannot drive a successful worker registration**
- type: `idea`, priority: `medium`
- `applyInventory` calls `pgx.BeginTxFunc(ctx, h.pool, ...)` unconditionally - even for an empty
  inventory - and `h.pool` is a concrete `*pgxpool.Pool` field with no interface seam
  (`internal/worker/handler.go:1629-1653`, confirmed). Everything below that call is therefore
  unreachable in a pool-less fixture, and **every test in `internal/worker` that drives a successful
  registration is `//go:build integration`**, which CI never runs. The measured cost is not
  hypothetical: this slice needed a ~500-line `go/ast` guard
  (`internal/worker/handler_handoff_guard_test.go`) as a substitute for a behavioural test of the
  success path, and that guard was **evaded twice** during review before it held, once by a construct
  (`if h.pool != nil { return }`) that was true in production and false in every default-lane fixture.
- Note what is **not** proposed: an early return on an empty inventory would change behaviour, since
  `ReplaceWorkerInventory` deletes the worker's existing rows first, and `applyInventory` also has an
  open bug of its own ([[bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory]]). The seam
  wanted is a narrow one - a `beginTx func(context.Context) (pgx.Tx, error)` field, or a two-method
  interface - injected by `NewHandler`/`NewHandlerWithGrace`. Acceptance should require at least one
  default-lane test that drives a **successful** registration end to end, since that is the property
  currently held by structure alone.
- The plan's own Phase 6 note said to file this "if a future slice needs to unit-test anything below
  `applyInventory`". This was that slice.

**2. After an ungraceful restart, every worker that was online stays `online` in the database forever**
- type: `bug`, priority: `medium`
- Only two statements move `workers.status` away from `online`: `MarkWorkerOfflineIfEpoch`, whose sole
  production caller is `markWorkerOffline` via `releaseWorkerGeneration` (so it requires a live
  connection's teardown), and `SetWorkerStatus`, whose sole caller is the metrics liveness sweeper
  (`internal/metrics/sweep.go:90`), which `continue`s past any worker `LastSampleAt` reports as
  untracked. **All three facts confirmed by grep in this pass.** After a crash or SIGKILL the in-memory
  metrics store is empty, so the sweeper tracks nobody until each worker reconnects, and
  `seedGraceTimersFromActiveTasks` (`cmd/relay-server/main.go:341-361`) enumerates only workers with
  non-terminal **tasks** and writes no `workers.status` at all. So a worker that was online and never
  comes back reads `online` in `GET /v1/workers` indefinitely - and a worker that was online with **no**
  active tasks is not even a grace candidate.
- This is the same operator-visible symptom the finishRegister slice just closed for one path, and this
  slice's own handoff comment documents it as what survives a panic; the item generalizes it to every
  ungraceful shutdown. Likely remedy is a startup statement that marks all non-revoked `online`/`stale`
  workers `offline` with `disconnected_at = now()`, run **before** `seedGraceTimersFromActiveTasks` so
  the grace seeding sees the persisted disconnect time - but that ordering interaction is exactly what
  the item needs to make explicit, since the seeding's `disconnected_at IS NULL` branch currently
  distinguishes "server crashed while the worker was online" and would stop firing.

**3. A panic on any gRPC handler goroutine kills relay-server for the whole fleet**
- type: `idea`, priority: `low`
- There is **no `recover()` in production code and no `Interceptor` anywhere in the tree** - verified by
  grep in this pass; the only two `recover()` calls are in `internal/api/server_test.go`. `net/http`
  recovers per connection by default, so the HTTP surface already has this protection and the gRPC
  surface does not; that asymmetry is the argument. `internal/worker/handler.go:730-754` documents the
  consequence for this one path: a panic below the handoff flip escapes both releases, and
  `go h.triggerDispatch()` panics on a goroutine where none of `finishRegister`'s defers exist, leaving
  the durable `workers` row `online` at a live epoch with nothing at startup to correct it (item 2).
- Filed at **low** deliberately, and the item should say why: **no reachable panic is claimed**. This is
  defense in depth plus a blast-radius argument (one agent's message path taking down the coordinator
  for every agent), not a live defect. If item 2 ships, the durable half of the consequence is already
  bounded and this drops further.

**4. AMEND (no new file): [[idea-2026-08-23-integration-only-guards-ci-never-runs]]**
- Record this slice as a **fourth instance**, and the sharpest one: it is not that a guard happened to
  be tagged, it is that `internal/worker`'s default lane **structurally cannot observe a successful
  worker registration at all**, so the ownership flag deciding whether a healthy agent's tasks get
  requeued had zero CI enforcement - deleting `handedOff = true` left all 21 packages green.
- Record that the item's remedy menu is **incomplete for this case**. Its first option ("add a
  non-Docker default-lane regression test for the property") is not available here without a seam
  change; its second (a Docker CI lane) is; its third (a written decision) is what shipped, in
  `handler_handoff_guard_test.go`'s header. Cross-reference item 1 above as the mechanical cause, and
  say plainly that the two are **not** duplicates: fixing the lane would run the integration tests, and
  fixing the seam would let the property be tested in the lane CI already runs. Either closes this
  instance; neither closes the other item.
- Also record that the substitute cost is now measured: a ~500-line parser guard, evaded twice, one of
  those evasions being production-only.

## Addendum: rounds 4 and 5, written after this retro was first committed

This retro was drafted at the end of round 3. Two more verification rounds followed, and both found a
HIGH. The record is corrected here rather than left to imply the slice ended at three.

**Round 4 - the adjacency assertion was bypassed by nesting.** `stmtIndexContaining` located the
direct body statement whose source range *contained* `registry.Register`. When the call stops being a
top-level `ExprStmt` and moves inside a compound direct-body statement, the index points at the
enclosing statement and adjacency still holds - so arbitrarily many fallible statements can live
between the register and the flip, which is the exact region the check's own comment said could not
exist. Four spellings survived, including the plausible-refactor shape
`if h.registry != nil { Register; if err := ...; err != nil { return } }`, plus a fifth that stranded
the other direction. Closed by requiring the statement at that index to *be* the call: identity, not
containment - a distinction the file already argued for on the closure body and had simply not applied
to this anchor.

**Round 5 - one pair of parentheses defeated the whole write-set analysis.** Four expression sites
type-asserted straight to `*ast.Ident`, so an `*ast.ParenExpr` wrapper made the flag invisible to all
of them. `(handedOff) = false` after the flip needs no pointer, no closure and no indirection, and it
is the total defeat: the flag is false at every return, so every SUCCESSFUL registration takes the
deferred release. Measured with `go vet` clean and the **whole repo** green. `gofmt` does not
normalise it away, and this tree has no fmt gate - CRLF makes `gofmt -l` flag every file at baseline.
Closed with `ast.Unparen` at all four sites.

**The comment beside it was the real defect, and it is the same shape three rounds running.** It said a
local bool has "exactly one other way to be written: through a pointer to it". That is a uniqueness
claim - a claim about the complement - and CLAUDE.md already warns that such a claim cannot be checked
by opening its subject. It was false, and the failure message a reader sees at failure time repeated
it. Both now say what is actually checked: writes counted by name after dropping parens, plus any
address-of.

### Why the hunt stopped at five, stated as a judgement rather than a conclusion

Every round found one more shape, so a sixth probably would too. The decision to stop is not a claim
that the guard is exhaustive - it demonstrably has not been, five times. The reasoning:

- The shipped `internal/worker/handler.go` was confirmed correct by five independent passes, and needed
  no behavioural change after round 1. Every later commit was a guard or a comment.
- Every residual finding is in the guard, which protects against a **future** regression to a line that
  is currently correct and is additionally covered by an integration test.
- The last two fixes were at the property level (identity rather than containment; paren normalisation)
  rather than another spelling patch, which is the right rung to stop on.

**The honest residual**: this guard exists only because the default lane structurally cannot drive a
successful worker registration. That is filed as
[[idea-2026-08-24-handler-pool-has-no-seam]] and promoted into Next. When that seam lands, the right
move is to replace most of this guard with a behavioural test and delete what the behavioural test
covers - not to harden it a sixth time. Five evasions of one parser guard is the measurement that
argues for the seam, and it is the most useful number this slice produced.
