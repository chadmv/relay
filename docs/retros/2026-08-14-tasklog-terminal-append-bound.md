---
date: 2026-08-14
topic: tasklog-terminal-append-bound
slice: 2026-08-14-tasklog-terminal-append-bound
branch: claude/pr-merge-main-2d2fc3
range: origin/main..HEAD (backend only, zero files under web/, green, not yet merged)
closes: bug-2026-08-12-tasklog-terminal-task-append-unbounded
---

# Session Retro: 2026-08-14 - the item was right, and the fix it prescribed was wrong

**TL;DR:** `AppendTaskLog`'s fence gained a third predicate bounding how long a **terminal** task
accepts log appends:

```sql
AND (t.status IN ('pending', 'dispatched', 'running')
     OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
```

Disjunctive, so the deliberate trailing flush survives. An allow-list, so it fails closed. An
absolute cutoff computed in Go, so both sides of the comparison stay in one clock domain. Knob
`RELAY_TASKLOG_TRAILING_WINDOW`, default `15m`, derived from a number rather than a feeling. Backend
only; zero files under `web/`.

**The unusual result is that the backlog item was materially accurate.** For the previous two
batches, verifying an item against the code was how the interesting findings were produced, because
the items kept being wrong. This one held on every technical claim. **And its prescribed fix still
failed open** - which is the transferable lesson, because accuracy about a bug is not accuracy about
its remedy, and this project had no prior instance separating the two.

## The item was right, and that is worth saying plainly

Eleven iterations of "verify the item's technical claims against the code" have produced eleven
findings. This is the twelfth iteration and the first clean one. The spec opened every claim:

- The SQL it quotes is **byte-accurate** against `internal/store/query/tasks.sql` at `ee88de0`.
- The pinning test exists at `internal/store/store_test.go` and asserts exactly what the item says
  it asserts - `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist`
  appends a trailing chunk after a terminal status and requires `NoError`, so a conjunctive status
  predicate really would turn it red.
- `tasks.finished_at` exists (`migrations/000001_initial.up.sql:64`), is nullable with no default,
  and **is** populated on every terminal transition that this fence can reach.
- Nothing prunes `task_logs`. Eleven `DELETE FROM` statements in `query/*.sql`, none naming it.
- The repro sequence is exact.

**A verification that comes back clean is a real result, not a wasted pass.** It is also what made
the rest of the pass cheap: the spec spent its budget on the parts the item did not have (the
threat model, the number behind the default, the shape of the predicate) instead of on repairing
premises. Recording this so the standing improvement goal reads as a *check* rather than as a
ritual that always finds something.

Two corrections the spec did make, both small and both improving the item's own argument:

- **"After that change no production statement can modify a terminal task's row at all" went stale
  two days after it was written.** `RetryJobTasks` shipped 2026-08-14 and reopens exactly the
  terminal rows the item calls frozen, bumping the epoch. That *closes* this window for the tasks an
  operator retries, and closes nothing for the `done` job nobody retries, which is the overwhelming
  majority. The bug stands; the framing needed the correction.
- **"No statement in the repo ever deletes from it" is literally true and incomplete.** There is a
  cascade via `DeleteJob`, which **has no production caller** - the only reference in the tree is its
  own generated method. So there is no operator-reachable way to reclaim `task_logs` storage at all.
  The correction makes the retention argument stronger.

## The item's prescribed fix failed open, and the spec caught it

The item asked for:

```sql
AND (t.finished_at IS NULL OR t.finished_at > NOW() - <grace interval>)
```

That admits **any terminal row whose `finished_at` is NULL** - a row from an older schema, or a row
written by a future terminal writer that forgets the stamp. This project's standing rule is that a
fence fails closed on a missing value; it is the entire content of the `=` versus
`IS NOT DISTINCT FROM` argument sitting eight lines above in the same statement.

The shipped spelling replaces the `IS NULL` disjunct with a **status allow-list**, which fails closed
on that row: a `done` task with a NULL `finished_at` fails the first arm because `done` is not in the
list, and fails the second because `NULL > cutoff` is NULL, not true.

> **A materially accurate item can still prescribe a wrong fix.** The item was right about the bug,
> right about the constraint (bound on time, not on status), right about the test that pins it - and
> the three lines of SQL it offered would have shipped a fence with a hole in it. The item's own
> Notes section warns that "anybody who fixes this with a status predicate will pass every existing
> test except one"; the correct reading is that the warning is about the **conjunctive** spelling
> only, and that disjoining a status allow-list is what makes the whole predicate fail closed.

Two artifact-level notes on that. First, this is the third instance in eight days of **an item's code
sketch being the least reliable thing in it** (the owner-email item's sketch contained the bug its
prose warned against; the cursor-pager item's sketch was the deviant algorithm presented as the
shared one). A prose claim gets verified; a snippet gets copied. Second, the item's sketch also used
`NOW() - interval`, i.e. the **database** clock, which the design replaced with a Go-computed absolute
cutoff for reasons section 2.4 had to establish from scratch.

## The refutation chain, link by link

### Link 1 - the spec refuted the item's fix and one of its acceptance criteria

Beyond the fail-open spelling: the item's criterion "`TestUpdateTaskStatus_...StillPersists` passing
with **no edit**" is unachievable under **any** parameterized design. That test calls `AppendTaskLog`
with a **keyed** struct literal, so a new `AppendTaskLogParams` field defaults to the zero
`pgtype.Timestamptz`, which binds SQL NULL, which the new predicate correctly rejects. The criterion
was relaxed to "green with one mechanical parameter line and **no assertion changed**", with the
standing rule kept intact: an assertion needing adjustment IS the finding, and would have meant the
design broke the flush. It shipped with exactly one added line and no assertion touched.

### Link 2 - the spec refuted ROADMAP's own scheduling recommendation

ROADMAP.md put this item and `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` in Now together on
the rationale that they are "one slice - both are `handleTaskLog` and both fence the same
wire-supplied fields". **Both halves are wrong.** The limiter item's half B is two unconditional
`log.Printf` calls in `handleTaskStatus`, a different handler and a different message type, and the
item explicitly says both halves must be fixed together - so "one slice" would have been a
three-handler slice. And a logging rate limiter **fences nothing**: there is no predicate and no
wire-supplied field being validated. The shared shape is a coincidence of file, not of mechanism.

The conductor wrote that recommendation and carried it through **two refreshes**, and it was never
checked until a spec checked it. That is the same defect class this project keeps finding in source
comments, arriving in the one artifact whose whole job is to decide what happens next.

> **A scheduling rationale is a technical claim.** "These two are one slice" asserts something about
> the code, it survives refreshes by being copied forward, and nothing reddens when it is false.

### Link 3 - the plan refuted six spec claims, including one it had already inherited

The plan re-derived the spec rather than executing it, and the self-catch is the one worth keeping:

- The spec said the item's spelling "fails **open** on all three" fail-closed properties. It fails
  open on **one** - the NULL `finished_at` row. For "the caller omits the cutoff", the item's
  spelling still rejects, because that row's `finished_at` is non-NULL and `finished_at > NULL` is
  NULL, not true. **The plan's own first draft had already propagated the overstatement**, into a
  prediction that one mutation would redden two subtests. Self-caught, recorded as finding 1,
  corrected to two *different* discriminating mutations (the `IS NULL` rewrite for one property, a
  `COALESCE(cutoff, '-infinity')` rewrite for the other) with the subtest that stays green named and
  justified.
- The spec's boundary test (a 50 ms window, a 150 ms sleep) would flake across two testcontainer
  round trips, and did not need a sleep at all. Replaced with a two-leg test that runs the same chunk
  through the same handler twice with the field set to 1 s then 1 h - the legs differ in nothing but
  the field, which is a stronger wiring proof than any sleep.
- The spec's tests backdated with `NOW() - interval` - the **database** clock - inside a design whose
  entire justification is staying in one clock domain. Every test now writes `finished_at` from the
  Go clock, exactly as production does.
- The spec named `TASK_STATUS_PREPARING` as the trap and missed its terminal twin
  `TASK_STATUS_PREPARE_FAILED`, which needs the **opposite** treatment at the same site.
- Two stale-prose hazards the change itself created: a test comment hard-coding the fence's parameter
  numbering (`$4,$5` becomes `$5,$6`), and the vocabulary guard's intro saying "three places" while
  listing five, about to become six.
- The spec's call-site count double-counted `retry_job_tasks_integration_test.go`, which **is** in
  `internal/store`. Nine, not ten.

> **A prediction that is one link deep is not verified.** The plan caught the spec's overstatement
> because it tried to build a mutation on top of it and the mutation would not have reddened. That is
> the cheapest available check on an inherited claim: ask what test goes red if it is true.

### Link 4 - the engineer refuted the plan's own mutation P1

The plan's first mutation was "delete the entire third predicate and confirm the exposure test goes
red". It **does not compile**: deleting the predicate removes `min_finished_at` from the statement,
`sqlc` drops the field, and all eleven call sites break. The predicted *behavioural* assertion never
runs, so the mutation proves nothing about the property it is named after.

The engineer substituted a parameter-retaining equivalent (keep the bind, neutralize the comparison)
and got the real RED. **The plan had applied "stage it so RED is behavioural, not a compile error" to
its own tasks** - that is the entire argument for the Task 1 / Task 2 / Task 3 split - **and did not
apply it to its own mutation battery.** The insight was in the document, one section above the table
that violated it.

### Link 5 - a review lens refuted the corrected prediction

P1 was then found to be **under-predicted**: it reddens three subtests, not one. That errs safe, and
it is a second wrong prediction in the same appendix. The pattern across links 3 to 5 is not
"predictions are sometimes wrong"; it is that **every prediction in that appendix that anybody
actually executed came back different from what was written**, in one direction or the other.

### Link 6 - the conductor's own remediation instruction carried the defect class it was fixing

Finding 1 of the review told the engineer to correct a sentence at `tasks.sql:171` to say "ALL
THREE". One line below that sentence sat "Neither substitutes for the other - do not delete either".
Applying the instruction literally would have left "all three" contradicted by "neither", two lines
apart, in the same comment block.

The engineer caught it and rewrote both sentences. **The fix for a wrong-prose defect generated a
wrong-prose defect.** Same shape as the cursor-pager slice's two-line collision, where a diff
invalidated the citation directly above the comment it was correcting.

> **When you correct a sentence, read the paragraph.** Prose defects cluster because prose is written
> in paragraphs and corrected in sentences.

## Four lenses, four genuinely different contributions

Worth analyzing rather than listing, because the four briefs did not overlap once:

- **Invariants** proved the change is **monotonically narrowing**: the fence went from `A AND B AND C`
  to `A AND B AND C AND (D OR E)`, so no input accepted after the change was rejected before it. That
  is the whole safety argument for a fence change in one line. It also checked that the parentheses
  are present in **both** the source and the generated file, rather than assuming the generator
  preserved them.
- **Security** independently made the same parenthesization the headline, from the other direction:
  unparenthesized, `A AND B AND C OR D` parses as `(A AND B AND C) OR D`, which is not a weaker fence
  but **no fence at all** - any chunk from any worker at any epoch, admitted on `finished_at` alone.
  Two lenses converging on one character class of failure from opposite arguments is the strongest
  signal this pass produced. Security also established the honest scope (below) and found the
  units-confusion hazard on the knob.
- **Correctness** mutated in a **detached worktree** (the dispatch-shape lesson from the previous
  slice, applied) and proved the live arm is not decorative: removing just `'dispatched'` reddens
  eight tests. It re-derived the clock-domain argument across every `finished_at` writer independently
  of the spec and the plan, and it proved the `main.go` wiring seam was **untested rather than
  untestable** - which is what unlocked the decision below.
- **Integration** exercised real Postgres NULL semantics rather than reasoning about them: it forced
  a terminal row into `finished_at IS NULL` by direct UPDATE - the pathological state the statement
  comment warns about and that no shipped statement produces - and confirmed the fence rejects it. It
  also confirmed the `RetryJobTasks` interaction against the actual row rather than against the SQL
  text.

## The honest scope: what this buys, stated so nobody has to rediscover it

This slice bounds the **post-terminal** arm and nothing else. A token-holding attacker with **one
live assignment** can still write unboundedly, because:

- **No coordinator-side watchdog on a stale `running` task.** Only the agent writes `timed_out`, so
  an agent that simply never reports terminal holds its assignment - and its unbounded write channel -
  indefinitely. Filed.
- **No per-task byte or row cap**, live or finished. Filed.
- **No rate limit on the gRPC ingest path.** Tracked adjacently.
- **Nothing prunes `task_logs`, ever**, and `DeleteJob` (the only cascade) has no caller. Filed.

What the fix genuinely buys, and this is the part worth carrying: **eviction now works.** Before it,
requeueing or cancelling a suspect worker's tasks bumped the epoch only on `dispatched`/`running`
rows, so every historical *finished* task that worker ever ran stayed permanently writable at its old
epoch - an operator could revoke a compromised agent and leave hundreds of open write channels behind.
Now those expire on their own, without any operator action, 15 minutes after each task finished.

The spec's "closes the hole" framing was corrected to "bounds the post-terminal arm" during review.
That correction is in the artifact, and it belongs here too: **the slice's own spec overstated what
the slice does**, which is the same defect class as everything in the chain above.

## Two accepted trades, recorded so they are not rediscovered as bugs

1. **A chunk buffered in the agent's `sendCh` across a coordinator outage longer than the window is
   now dropped**, where it previously landed. The "under 2 minutes" arithmetic that justified the
   default is **per reconnect attempt**, not total: the reconnect loop is indefinite and `sendCh`
   survives reconnects, so a genuinely late chunk has no hard ceiling. The default is 15m against a
   ~2 minute per-attempt worst case, which is generous, and the operator-visible failure is silent
   truncation - which is precisely why the README row says so and why the startup warning exists.
   This is a real behaviour change, not a no-op, and it was accepted deliberately.
2. **`'pending'` in the allow-list is provably unreachable.** Every statement that returns a task to
   `pending` also sets `worker_id = NULL` in the same UPDATE, so the `worker_id` predicate above can
   never match a pending row. It is kept anyway so the arm stays **byte-identical** to the two
   sibling allow-lists in `UpdateTaskStatus` and `IncrementTaskRetryCount` - a divergence would be
   read as meaningful by the next person to diff them. Note the gap: **this reasoning exists in the
   review record and in this retro, and not in the statement's comment**, which is 60 lines long and
   documents nearly everything else. It is now also written into
   `idea-2026-07-01-dead-status-vocabulary` as an explicit scope boundary, because that item is
   exactly the sweep that would delete it. See Known Limitations.

## The conductor override, and my judgment on it

The plan said, of the `main.go` env-to-field assignment: "**report the wiring blind spot, do not
invent a test**". The conductor overrode that after the lenses established two facts the plan did not
have: deleting the six-line wiring block leaves **all 20 packages green**, and the repo **already
ships the countermeasure pattern** (`internal/store/incrementtaskretrycount_guard_test.go`). The
guard was built with `go/ast` rather than a regex, because a regex source-scanning guard in this repo
was proven breakable by one JSX comment and the fix there was to delete it.

**I judge the override correct**, and the reasoning generalizes:

- The plan's default is a good default. "Do not invent a test" protects against a test that pins the
  implementation rather than a behaviour, written by someone who has just spent an hour in the file
  and will assert whatever they remember writing.
- The override cleared both bars that default protects against. **The seam was measured, not
  assumed** - a green 20-package suite after deleting the wiring is evidence, not an intuition - and
  **the mechanism already existed in the repo**, so nothing novel was invented under time pressure.
- `go/ast` over regex is the right call and is the second application of a lesson from the slice two
  passes back. A structural matcher ignores formatting, comments, string literals and the local
  variable's name; a regex matches text that happens to look like code.

The residual, which the guard's own comment does **not** state: it proves the value is **derived
from** `parseTrailingLogWindow`, not that it arrives **unmodified**. `agentHandler.TrailingLogWindow
= trailingLogWindow / 2` passes. It also keys on the field **name** only, so an assignment to any
object's `.TrailingLogWindow` satisfies it. Those are acceptable limits for a guard whose job is "the
wiring did not get deleted" - but the test is named `...IsWiredIntoTheHandler`, which claims more
than it checks. Named here and in the generalization item rather than filed on its own.

## What Was Built

- **`internal/store/query/tasks.sql`** - `AppendTaskLog`'s fence gains the two-arm disjunct, plus
  about 60 lines of comment: why it is a disjunction and must never become a conjunction; why the
  first arm is an allow-list **whose guidance is inverted from every other allow-list in the repo**;
  the `preparing` / `prepare_failed` trap; why the cutoff is Go-computed; why the pair fails closed;
  and an explicit "no EvalPlanQual reasoning applies here, do not import it from `RetryJobTasks`" -
  because a reviewer who has read that comment will reach for it.
- **`internal/store/tasks.sql.go`** - regenerated via `sqlc generate` alone (not `make generate`, to
  skip `buf` churn), with the mandatory read-back of the emitted body **and doc comment**:
  `MinFinishedAt pgtype.Timestamptz` (not `interface{}`), six `QueryRow` arguments, stream and content
  shifted to `$5,$6`, and the new prose actually present. The CRLF revert has silently discarded a
  regeneration in this repo before.
- **`internal/worker/handler.go`** - `DefaultTrailingLogWindow = 15 * time.Minute`, the exported
  `Handler.TrailingLogWindow` field beside `Metrics` and `AllowAutoEnroll`, and per-call resolution in
  `handleTaskLog` (non-positive means the default, so every existing `NewHandler` call site stays
  correct with no edit). **Zero Go changes on the rejection path** - a closed window joins the
  existing `pgx.ErrNoRows` silent drop, before the publish.
- **`cmd/relay-server/main.go`** - `RELAY_TASKLOG_TRAILING_WINDOW`, parsed by
  `parseTrailingLogWindow`, which returns **(duration, warning string) rather than (duration, ok)**
  because there are three outcomes, not two: accepted silently; rejected-and-defaulted (unparseable,
  zero, negative); and **accepted-but-alarming** - a parseable value below `minSaneTrailingLogWindow`
  (2m) is *kept*, because narrowing the knob is the operator's prerogative, and warned about, because
  it silently truncates real output. That third case is the security lens's units-confusion finding
  (`15s` is likelier than a typo and is the only one of the three that loses data), and a bool pair
  would have had to lie about one of the outcomes.
- **`cmd/relay-server/trailing_log_window_test.go`** - nine table cases including the two boundary
  cases (`1m59s` warns, exactly `2m` does not, so the threshold cannot warn about the value it is
  compared against), plus `TestTrailingLogWindowIsWiredIntoTheHandler`, the `go/ast` structural guard.
  **Untagged**, so both run under `make test`, unlike every other test in that package.
- **`internal/worker/handler_tasklog_integration_test.go`** - five new tests: the exposure
  (`RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow`, RED against HEAD, asserting **both**
  not-stored and not-published so the battery can catch an ungated publish), the trailing-flush
  control, the live-task control, `TheWindowIsReadFromTheHandlerFieldAtEveryCall` (the sleep-free
  two-leg wiring proof), and `AZeroWindowMeansTheDefaultNotAZeroLengthWindow`.
- **`internal/store/store_test.go`** - `TestAppendTaskLog_TerminalTaskAcceptsOnlyInsideTheTrailingWindow`,
  seven table cases, every cutoff explicit so nothing sleeps. The last two - terminal with a NULL
  `finished_at`, and a caller that omits the cutoff - are the ones that discriminate this spelling
  from the item's, **against different mutations**. Plus the one-line addition to the pinned flush
  test and eight mechanical call-site additions.
- **`internal/store/tasks_status_vocabulary_lockstep_test.go`** - a sixth named site with **inverted**
  guidance stated in capitals, and the intro's stale "three places" corrected (it already listed five).
- **`README.md`** - the env row, naming what a too-small value does silently and the `8760h` escape
  hatch back to the old unbounded behaviour.
- **No migration, no proto change, no new query, no new round trip, no new goroutine.** Per chunk the
  change costs one `time.Now()` and one bound parameter.
- **CLAUDE.md deliberately not amended** - the change adds a predicate *underneath* the epoch-fence
  invariant rather than changing it. Recorded as a decision because the previous three slices in this
  family each amended that bullet, so silence should read as deliberate.

## Key Decisions

- **One item, not two.** The err-limiter item was kept out on four independent grounds (different
  handler, no fence, non-overlapping layers, non-interacting test strategies) plus attribution: two of
  the last three iterations found the item wrong about its own premise, and a one-item blast radius is
  what made that recoverable. It should still go **next**, and second, so it can reuse this slice's
  handler fixtures.
- **The knob's third outcome.** Keeping a too-small value while warning about it, rather than clamping
  it, treats the operator as the authority on their own deployment and treats the *consequence* as the
  thing that must be said out loud. The warning names the truncation, not just the value.
- **The window's default is defended by arithmetic** (5 s `cmd.WaitDelay` + 40 s gRPC keepalive + 60 s
  reconnect cap, so ~8x margin at 15m) rather than by taste, so it can be re-argued from the written
  numbers rather than re-derived. The arithmetic's own limit is recorded above: it is per-attempt.
- **The rejection stays silent and identical to the other two.** A log line here would be
  caller-driven volume on the recv goroutine and would hand back the exact flood vector the adjacent
  item is about, and it would fire on the legitimate late flush - the case an operator would most want
  quiet. The cost is diagnosability, which is why an observability item is filed rather than shrugged
  at.
- **`int32(chunk.Epoch)` was deliberately not fixed**, though the engineer was editing that exact
  struct literal. Separate filed item; folding it in blurs attribution. Flagged in the report, left in
  the tree.

## Findings Triage

- **0 findings against the item's technical claims.** First clean verification in twelve iterations.
- **1 finding against the item's prescribed fix** (fail-open spelling), caught at spec time, which is
  the cheapest place it could have been caught.
- **1 finding against ROADMAP's scheduling rationale**, wrong on both halves, carried through two
  refreshes unchecked.
- **6 findings against the spec, by the plan**, one of which the plan had already inherited into its
  own draft and self-caught.
- **2 findings against the plan's mutation battery** - one that does not compile (found by the
  engineer), one under-predicted (found by a lens). Both in the same appendix.
- **1 finding against the conductor's remediation instruction**, which would have created the defect
  class it was correcting. Caught by the engineer.
- **2 lenses converged independently on parenthesization** as the single character-level failure that
  would void the entire fence.
- **0 findings against the shipped behaviour of the predicate.** Everything above is about artifacts.

## Deferred Findings

**Filed this pass (proposals for human accept - the conductor commits, the human accepts):**

1. `bug-2026-08-14-task-logs-have-no-per-task-volume-cap` (**bug/medium**) - a single task can insert
   unbounded rows at up to 4 MB per chunk, live or finished, and this slice does not touch it. Not
   scope creep: the terminal-append bound is about **duration**, this is about **rate**, and the spec
   rejected folding it in because it needs either a count subquery or a counter column plus a
   migration on a path that is deliberately one round trip.
2. `idea-2026-08-14-task-logs-retention-and-pruning` (**idea/medium**) - nothing prunes `task_logs`
   and the only cascade (`DeleteJob`) has no caller, so there is no operator-reachable way to reclaim
   the space. Pairs with `idea-2026-08-13-reap-expired-invites-and-tokens`. Carries the trap up front:
   a reaper keyed on `created_at` deletes the logs of a long-running task that is still writing, and
   **a server-side terminal writer would break what the retry-resurrect slice just closed**.
3. `bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task` (**bug/medium**) - only the agent
   writes `timed_out`, so an agent that never reports terminal holds an assignment, and an unbounded
   write channel, forever. `GraceRegistry` covers the **disconnected** case only and
   `Dispatcher.failClaimedTask` covers the **dispatch-failure** case only. This is the arm the
   trailing window does not bound, and it was found by asking what the fix does not buy.
4. `idea-2026-08-14-tasklog-fence-rejection-is-unobservable` (**idea/medium**) - `ErrNoRows` used to
   mean "forged or zombie" and now also means "legitimately late", which will happen, and an operator
   who sets the window too small gets **no signal of any kind**. Proposed shape is a counter through
   the existing `Handler.Metrics` seam, one atomic add on the hot path, and **explicitly not a log
   line** - that would hand back the flood vector the adjacent limiter item tracks. The item records
   what I verified while writing it: there is **no coordinator-level counters endpoint today**, so the
   read surface is a real dependency, not a detail.
5. `idea-2026-08-14-generalize-the-env-to-field-wiring-guard` (**idea/low**) - `agentHandler.Metrics`
   and `agentHandler.AllowAutoEnroll` have the identical untested seam the new `go/ast` guard closes
   for `TrailingLogWindow`, and `RELAY_TELEMETRY_WINDOW` / `RELAY_WORKER_GRACE_WINDOW` have the same
   shape. One generalized guard beats three copies, and **this project's own
   extract-before-the-third-consumer rule just fired on the cursor-pager item**. The new guard's
   comment already says "the conductor is filing that as its own item - do not generalize here", so
   not filing it would leave a source comment pointing at nothing.

**Amendments applied to existing items (no scope change, no frontmatter change beyond `updated:`):**

- `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` gains a dated section recording that this slice
  deliberately did **not** fold it in, why ROADMAP's combining rationale is wrong on both halves, the
  positive evidence that the two are independent (`TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch`
  stayed green with **no edit at all**, because NUL-bearing content fails during bind-parameter decode
  **before** the fence is evaluated), and the new constraint this slice creates for it: the
  trailing-window rejection now shares the silent `ErrNoRows` branch, so the "should a rejection log?"
  question is settled harder in the "no" direction and must agree with item 4 above on one mechanism.
- `bug-2026-08-12-tasklog-epoch-int32-truncation` gains a dated section recording that a review lens
  judged it **neither more nor less reachable** after this change - the new predicate constrains the
  row's status and `finished_at`, never the epoch value - so the next reader does not re-derive it.
  Its stale line citations (`handler.go:622`, `:433`, `:487`) are converted to symbol names in the
  same edit: the params literal moved roughly 170 lines and every offset was wrong.
- `idea-2026-07-01-dead-status-vocabulary` gains an explicit **scope boundary**, and this is the
  amendment most likely to prevent a future defect. That item asks someone to sweep hard-coded status
  vocabulary and delete what the schema makes impossible. `AppendTaskLog`'s new first arm contains
  `'pending'`, which is **valid but provably unreachable through that fence**, and which is kept
  deliberately so the three sibling allow-lists stay byte-identical. Without the boundary, an
  otherwise-correct execution of that item deletes it and nothing goes red. Its own stale citations
  (`tasks.sql:181`) are converted to symbol names in the same edit.

**Considered and NOT filed, with reasons:**

- **`TrailingLogWindow` as an `atomic.Int64`.** It is documented "read-only after startup", it is set
  once in `main()` before the gRPC server accepts a connection, and it matches the `AllowAutoEnroll`
  and `Metrics` precedent exactly. Filing it would propose a race that does not exist and would put a
  third pattern in a file that currently has one. **Non-item**, deliberately.
- **The guard test's name claiming more than it checks** (derivation, not value; any object's field).
  One sentence of comment, and it is recorded in the generalization item rather than as its own file.
- **The `'pending'` unreachability note missing from the statement comment.** Recorded as a known
  limitation below, and defended against from the other side by the dead-vocabulary amendment. The fix
  is a sentence in a comment block the next editor of that statement will read anyway.
- **A CLAUDE.md amendment.** The invariant did not move. Recorded as a decision above.
- **ROADMAP's wrong "one slice" rationale.** Not an item - it is a refresh action, and the refresh is
  the conductor's. Flagged to the conductor rather than filed, because an item asking someone to edit
  a document that is regenerated every cycle would be stale before it was read.
- **A stray `</content>` line at the end of eight backlog files** (found by grep while amending two of
  them; the two I rewrote no longer carry it). Junk from whatever wrote them, zero behavioural effect,
  and a one-command sweep for whoever is next in `docs/backlog/`. Reported to the conductor rather
  than filed as work.

## Known Limitations

- **This bounds one arm.** A live assignment is still unbounded in rate and, absent a watchdog,
  unbounded in duration. Four filed items describe the remaining surface; none of them is closed by
  this slice and the spec's "closes the hole" phrasing was corrected to say so.
- **The `'pending'` trade is documented in the review record, this retro and a backlog item, but not
  in the code.** The statement's comment block is roughly 60 lines and explains nearly every other
  decision in the predicate. The next person to diff the three sibling allow-lists will find one arm
  carrying a value that provably cannot match and will have to re-derive why it is there.
- **The window's justification is per reconnect attempt.** A long coordinator outage plus a full
  `sendCh` can exceed 15m and drop a chunk that would previously have landed. Accepted, documented in
  the README row, and not detectable by any signal today - which is exactly why the observability item
  is filed.
- **The wiring guard proves derivation, not fidelity.** `TrailingLogWindow = trailingLogWindow / 2`
  passes it, and so would an assignment to a different object carrying the same field name.
- **The reported suite counts do not reconcile against the tree, and nobody re-measured after Phase 4.**
  The implementing lane reported **476 -> 477** top-level and **565 -> 569** with subtests. Reading the
  tree, `cmd/relay-server/trailing_log_window_test.go` contains **two** new untagged top-level tests
  and **nine** subtests, which predicts +2 and +9. The likeliest explanation is that the figures were
  taken before the Phase 4 remediation added the structural guard and the two boundary cases, which is
  the same "the prediction moved, not the gate" shape the last slice recorded. **Trust the tree, and
  re-measure before the number is copied forward** - this project has carried a stale test-file count
  for three slices before.
- **Everything integration-tagged is invisible to `make test`.** The store and worker tests here all
  carry `//go:build integration`, so the unit gate would stay green if every one of them were broken.
  It is a no-regression gate, never evidence.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **twelfth iteration
  running**, and the first that came back clean. Record the clean result as a result.
- **A backlog proposal is not a contract** - honored, and this iteration sharpens it: the *diagnosis*
  can be a contract while the *prescription* is not.
- **An item's code sketch is the least verified thing in it** - third instance in eight days.
- **Plan-supplied tests and plan-supplied mutations are untrusted** - honored, and it paid twice: one
  mutation did not compile, one was under-predicted.
- **Wrong prose about correct code is the dominant defect class** - **tenth consecutive iteration**,
  and this time it reached a scheduling document and a remediation instruction, not just source
  comments.
- **Diagnose a red gate; measure both ways** - not exercised; no gate went red.
- **Mutation testing needs an isolated tree** - honored on the first attempt after last slice's
  hazard; the correctness lens mutated in a detached worktree.
- **Prefer a symbol name to a line range in any cross-file citation** - honored, and applied
  retroactively to three items whose offsets this diff invalidated.
- **Backlog housekeeping is required scope** - the close is outstanding and named below.

New from this iteration:

- **A materially accurate item can still prescribe a wrong fix.** Verify the diagnosis and the remedy
  as two separate claims; the remedy is the one nobody re-derives, because the item that got the bug
  right has already earned the reader's trust. **Candidate for durable memory.**
- **A scheduling rationale is a technical claim.** "These two are one slice" asserts something about
  the code, survives refreshes by being copied, and reddens nothing when false. Check it the first
  time a slice is scoped against it.
- **Apply the plan's own staging insight to the plan's own mutation battery.** "Stage it so RED is
  behavioural, not a compile error" applies to every mutation in the appendix; a mutation that removes
  a parameter breaks every call site and proves nothing.
- **When you correct a sentence, read the paragraph.** A one-line correction that contradicts its
  neighbour is the same defect wearing the fix's clothes.
- **State what a fix does not buy, in the spec, before review has to correct it.** "Closes the hole"
  became "bounds the post-terminal arm" only after a lens went looking; the four scope items filed
  here all come from that one question.
- **A silent rejection path needs a counter the day a second reason to reject is added.** `ErrNoRows`
  meaning one thing is diagnosable by reasoning; meaning three things is not.
- **Override "do not invent a test" only on measured evidence plus an existing in-repo pattern.** Both
  bars were cleared here; either alone would not have been enough.
- **A deliberate no-op in shipped code needs a defence in the item that would delete it**, not only in
  the retro. The `'pending'` arm is kept for symmetry and would be swept away by an existing open item
  with nothing going red; the scope boundary now lives in that item.

## Files Most Touched

- `internal/store/query/tasks.sql` - the fence and its comment block. Read the four bullets under
  "THE TRAILING WINDOW": the disjunction rule, the **inverted** allow-list guidance, the clock-domain
  argument (including the honest multi-replica caveat added in review), and the fail-closed spelling
  with its explicit "DO NOT rewrite the second arm as `finished_at IS NULL OR ...`".
- `internal/worker/handler.go` - `DefaultTrailingLogWindow`'s comment carries the arithmetic behind
  the default; `handleTaskLog` carries the per-call resolution and the note that it is never cached
  because a test moves the field between two calls on the same handler.
- `cmd/relay-server/main.go` - `parseTrailingLogWindow` and `minSaneTrailingLogWindow`. The
  three-outcome return is the most re-usable idea in the slice: an env parser whose second return is a
  message, not a bool, because "we took your value" and "you should know what it costs" are different
  questions.
- `cmd/relay-server/trailing_log_window_test.go` - the `go/ast` guard, with its comment stating why it
  is not a regex and why it should be generalized rather than pasted a third time.
- `internal/store/store_test.go` - the seven-case table; the last two cases are the ones that
  discriminate the shipped spelling from the item's.
- `internal/store/tasks_status_vocabulary_lockstep_test.go` - the sixth site, and the only one of the
  six whose guidance runs backwards.
- `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md` - section 3 (the item versus
  HEAD) and section 5 (why this is one slice and not two, refuting ROADMAP) are the parts worth
  reading again.
- `docs/superpowers/plans/2026-08-14-tasklog-terminal-append-bound.md` - "Verification findings: where
  the spec is wrong or incomplete" and Appendix A's "Known blind spots", which name three things the
  battery cannot cover instead of pretending otherwise.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No
  `git log`, no `git diff`, no test run. Every claim below that could be checked by reading was
  checked against the worktree.
- **Verified by reading:** the full spec and plan; `internal/store/query/tasks.sql` lines 196-278
  (the comment block and the shipped fence CTE, including the parentheses); `internal/worker/handler.go`
  (the `Metrics`/`AllowAutoEnroll`/`TrailingLogWindow` field block, the params literal at :790-799 with
  `MinFinishedAt` present and `int32(chunk.Epoch)` **untouched** at :796, and the unchanged
  `ErrNoRows`/`shouldLog` branch); `cmd/relay-server/main.go` (the wiring at :145-149, and
  `parseTrailingLogWindow` plus `minSaneTrailingLogWindow` at :312-356, which return a warning string
  rather than the plan's `ok` bool); `cmd/relay-server/trailing_log_window_test.go` in full (nine
  subtests, two top-level tests, the `go/ast` walk); the five new handler test names and the two new
  store test names by grep; that `timeout_sec` is enforced only in `internal/agent/runner.go` and that
  `timed_out` is written only by `handleTaskStatus`, which is the evidence behind the watchdog item;
  that `internal/api/server.go` routes no server-wide counters endpoint, which is the evidence behind
  the observability item; that `internal/store/models.go` and everything under `web/` are absent from
  the change set by file listing; that `docs/backlog/bug-2026-08-12-tasklog-terminal-task-append-unbounded.md`
  is **still in the open directory**; and the full text of every backlog item filed or amended.
- **Reported by the implementing and verifying lanes, not re-run here:** the verbatim RED output for
  the exposure test; every mutation result including the eight tests reddened by removing `'dispatched'`
  and the non-compiling P1 substitution; all four integration suite results and timings (store ~120s,
  worker ~104s, scheduler 26s, api 459s); `go build`, `go vet -tags integration`, and the unit gate;
  the five generated-file read-backs after `sqlc generate`.
- **Not verified, and flagged rather than repeated:** the suite counts (476 -> 477 / 565 -> 569), which
  do not reconcile against the two new untagged test functions in the tree. See Known Limitations.
- **The five items filed by this pass are in `docs/backlog/` as proposals**; the human gives final
  accept. The three amendments append dated sections, change no scope, and touch only `updated:` in
  frontmatter. **The close of `bug-2026-08-12-tasklog-terminal-task-append-unbounded` is outstanding
  and belongs to the conductor** (`/backlog close`, never a hand-edited `status:`), as do the
  exact-file-set check, the final gates, all commits, and a ROADMAP refresh that drops the refuted
  "one slice" rationale.
