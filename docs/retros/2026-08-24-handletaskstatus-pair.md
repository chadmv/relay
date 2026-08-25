---
date: 2026-08-24
topic: handletaskstatus-pair
slice: idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted + bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget (the two arms of one `if`, shipped together)
branch: claude/pr-merge-session-d3977d
range: origin/main..HEAD (24 commits; backend only; Go plus README plus one comment-only `.sql`; zero migration, zero proto, zero files under web/; green, not yet merged)
pr: handletaskstatus-pair - not yet opened; reference this work by date and slug, never by a predicted number
closes: idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted, bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget
proposed-this-slice: taskstatus-fence-counters-are-forgeable-by-the-assignee, tasklog-persist-key-drains-the-shared-ingest-bucket, coordinator-side-fence-rejections-have-no-section (all proposals; the conductor files, the human accepts)
---

# Session Retro: 2026-08-24 - the security finding changed the slice's honesty and not one line of its code, and the rule it broke had been written down in the backlog five days earlier

**TL;DR:** `internal/worker/taskstatus_fence_counters.go` partitions the rejections from
`handleTaskStatus`'s two epoch-fenced writes three ways - `raced` / `duplicate` / `conflicting`,
classified at zero cost from the row `GetTask` already read - published as a fifth `task_status_fence`
section on admin-only `GET /v1/server/counters`. Three new `logKind`s (`status_retry_write`,
`status_update_write`, `status_fail_dependents`) bring three previously-unbudgeted log sites inside the
per-connection ingest budget, and the two arms are now structurally exclusive (`if errors.Is { record }
else if lim.allow { log }`) rather than exclusive by comment. `git diff -- internal/store/` is
comments-only apart from one test file; both fences are byte-identical. Unit 637 -> 656; integration
green across 21 packages; `-race` clean in a Linux container; no flakes at `GOMAXPROCS=1 -count=30`;
`web/` untouched and still green.

This slice's durable rule is the third rung of a ladder CLAUDE.md already carries two rungs of:

> **The epoch establishes currency. `worker_id` establishes identity. Neither establishes HONESTY.** The
> gate proves the sender is the assignee; it proves nothing about whether the report is true. That is
> harmless for the row - the fence still protects it - and it is not harmless for anything DERIVED from
> the rejection. When a fence rejection starts feeding a counter, a log line or an audit record, ask what
> a peer who can move that signal gains, **and whether the signal's own documented remedy is in their
> favour.** Here it was.

---

## The headline: the README sentence was the defect, and it pointed the operator the same way the attacker wanted

The identity gate is sound. Two lenses proved it by construction, and the shipped tests pin it. What the
security lens established is one layer up:

- A terminal transition bumps neither `assignment_epoch` nor `worker_id` - deliberately, and
  `tasks.sql`'s own terminality paragraph states the same reachable state in its own words.
- So an assignee that reports `done` at epoch N and then `failed` at epoch N passes **both** Go gates
  legitimately, every time, and each message adds one to `conflicting_total`.
- Nothing rate-limits status messages, and this path spends no log-budget token. **Measured: 10,000
  forged messages produce `{Raced:0 Duplicate:0 Conflicting:10000}` with every other counter in the
  process flat.**

None of that is the finding. The finding is what the number is read as:

> **README told an operator that a climbing `conflicting_total` means `RELAY_TASK_WATCHDOG_MARGIN` is
> set too small - so the prescribed remedy is to RAISE it, widening the unbounded-assignment window the
> watchdog exists to close.** The attacker's incentive and the documented remedy pointed the same way.

Neither README sentence was literally false. `conflicting_total` **is** the watchdog-margin signature;
raising the margin **is** the fix for a genuine one. The defect was what was absent - and it was absent
next to a sibling bullet that spends four sentences on exactly this hazard for `task_log_fence`. The
remediation is three sentences of README (`:1307`) and a paragraph in the type's doc comment; **not one
line of executable code changed.**

### The rule it broke had been written down five days earlier, in the backlog, by the slice that found it first

`docs/backlog/idea-2026-08-21-rejected-total-is-forgeable-and-its-remedy-helps-the-forger.md` is open,
was filed on 2026-08-21 by slice 3's Phase 4, and its Notes section closes with:

> **when you ship an operator signal, write down what an adversary who can move it would gain - and check
> whether the signal's own documented remedy is in their favour.** This one's is.

Five days later this slice shipped a second counter with the same shape, wrote a README bullet with the
same gap, and needed a security lens to find it again. The rule was correct, specific, already
discovered, and **stored somewhere nobody reads while writing a payload comment.** That is the argument
for the CLAUDE.md amendment below and it is a better argument than this slice's own instance: a rule that
has fired twice in five days and was not applied the second time despite being written down is a rule in
the wrong file.

Note also which direction the two forgeries run. `task_log_fence`'s is **cross-task** (any enrolled
agent, any task id off the wire); this one is **self-task** (only the assignee, only its own assignment).
The mechanisms are disjoint and so are the remedies. What they share is the shape, and the shape is the
part that transfers.

---

## The plan refuted seven claims, and the first one was the item's motivating mechanism

**Twenty-second consecutive iteration in which planning-phase verification caught something material
before a line was written.** The material one:

The item said the live cause of a discarded terminal report is *"the coordinator stale-task watchdog
having already bumped the epoch"*. **The watchdog does not bump anything.** `watchdog.go:208-219` passes
`AssignmentEpoch` as a **fence**, and `UpdateTaskStatus` writes `status`, `started_at` and `finished_at`
and nothing else - its own comment says so outright.

The outcome the item describes is real. The path is different, and the correction is what made the design
work:

1. `GetTask` returns `status='timed_out'`, `worker_id` unchanged, `assignment_epoch` unchanged.
2. The identity gate passes - the assignee is unchanged.
3. The currency gate passes - **the epoch was never bumped**.
4. The write is refused by the **terminality predicate**, at the same epoch.

So the watchdog clobber arrives in exactly the shape the item's own nuance (a) warned about as the
*healthy* case - a duplicate terminal at the same epoch. **The item's two-counter proposal (split by
statement: `IncrementTaskRetryCount` versus `UpdateTaskStatus`) would have put a healthy duplicate and a
watchdog clobber in the same key**, which is the one outcome the item was filed to prevent. Splitting by
**reason** - what the row said at T0 - is what answers the question it actually asked.

The correction also sharpened the floor semantics the item asked to be documented. Nothing between
`GetTask` and the write reads `task.Status`, so the terminality predicate has no Go-side pre-filter:
`duplicate_total` and `conflicting_total` are **exact**, and only `raced_total` is a floor. The item said
the whole number was a floor.

Six more refutations, none of which changed the justification: the identity gate cannot be the counter's
only protection *and* the "saves nothing" line it was documented as (R10 - it gained a fourth job); the
proposed dedupe key carrying task id and epoch is wrong for these three sites and the argument against it
was already written twenty-five lines above the first one (R6); there are thirteen `log.Printf` sites now,
not twelve, and the thirteenth is in neither of the item's two classes (R7); `&Handler{}` is not enough
(R8); the counter type cannot be declared in `internal/api` because that package imports `internal/worker`
(R9); and all three documents cited stale line numbers, the region having drifted twice (the table is in
the plan).

---

## A 19-row mutation matrix produced one survivor, and chasing it found a second instance the plan never listed

**M18.** Swap the operands of `!errors.Is(err, pgx.ErrNoRows) && lim.allow(...)`. It compiles, vets
clean, changes **no log line**, and left the whole module green.

What it changes is who pays. With `lim.allow` first, the cheapest message a peer can send - a well-formed
uuid naming no task at all - **spends a token and claims a dedupe slot on every call**. The bucket is 16
tokens refilling at 6/min for the whole connection, shared across all eight kinds, so draining it there
silences the diagnostics that matter. That is the exact failure mode the limiter exists to prevent,
reintroduced by an operand swap. It also corrupts the numbers in the direction that is hardest to read:
`deduped.status_get_task` climbs for a kind that emitted no line at all.

The engineer chased it properly rather than reverting:

- It found **a second site of the same shape the plan never listed** - `FailDependentTasks`' `err != nil
  && lim.allow(...)`, where the swap spends a token on **every successful dependency cascade**, i.e. on
  every terminal task in a healthy fleet. Not an adversarial case at all.
- It left a permanent test behind, `TestHandleTaskStatus_TheSilentArmsSpendNoBudget`, with the poisoned
  input **first** and a positive control on the **same limiter** afterwards (without it, a limiter mutated
  into always-refusing passes every assertion above).
- A later lens then proved by AST walk over all fifteen `&&`/`||` sites in both packages that there is
  **no third**.

> **A short-circuit is a control, and its operand ORDER is the control.** Both operands are correct in
> both orders; only one order is free for the cheapest caller. Nothing in `go vet`, the type system or a
> log-line assertion can see the difference, because the difference is a resource nobody was asserting on.
> The test that catches it asserts on the **budget**, not on the output.

---

## Two "added a property, didn't add its guard" findings, one layer apart

Both are the same mistake in two places, and the sibling in each case already had the guard.

**1. A fourth reason declared AFTER the sentinel is silently lost, with all three packages green.**
Measured, not imagined: declare `fenceReasonVanished` after `fenceReasonCount` and record it from the
plausible next call site (the `GetTask` `pgx.ErrNoRows` arm), and `internal/worker`, `internal/api` and
`cmd/relay-server` all stay green. The dense-run test cannot see it - its hardcoded `run` list still holds
three entries and the sentinel is still 3 - and the publish test iterates `r < fenceReasonCount`, so it
never reaches the new cell. `record`'s bounds check then drops every increment in silence. **The sibling
`logKind` type already has the AST guard that closes exactly this, and its comment enumerates the evasion
by name** ("a sixth kind declared AFTER `kindCount` - the dense-run test above cannot see that one").
`taskStatusFenceReason` shipped with the dense-run rung and no AST rung: the enumeration existed and its
counterpart did not. Closed by `TestEveryTaskStatusFenceReasonIsDeclaredInsideTheSentinel`, which parses
the **package** (not one file), resolves const types the way Go does, and asserts arity, per-block
sentinel position, and that every value reaching `statusFence.record` is a declared constant or a call to
a checked producer.

**2. The identity gate's newly-claimed fourth job was unpinned.** This slice's own comment and payload doc
assert that the counters are "attributable to the task's own assignee". Deleting the three-term gate left
everything green while a registered non-assignee drove `Conflicting: 1000` - task **state** stays correct
throughout, because both statements carry their own `worker_id` predicate, so nothing functional reddens.
**The SQL identity predicate protects the ROW; only the Go gate protects the COUNTER**, and that is a new
consequence that arrived with this slice. Closed by
`TestHandleTaskStatus_OnlyTheAssigneeMovesTheFenceCounters` (poisoned leg first, positive control on the
same handler) and `TestHandleTaskStatus_AZeroValueWorkerCannotMoveTheCountersOnANeverClaimedTask`, which
drives the two `.Valid` terms as a **pair** - removing either alone leaves the hole closed; removing both
makes the comparison Go's `IS NOT DISTINCT FROM` and the gate fails open on a never-claimed task.

> **When a change gives an existing control a new job, the new job needs its own subject.** The gate did
> not move. What moved is what depends on it, and the test that would have gone red for the old three
> jobs is not the test that goes red for the fourth.

---

## A remediation prescription of mine was refuted by measurement

The Go-side writability mirror (`taskStatusIsWritable`) restates a status allow-list that lives in SQL. Its
first guard iterated a **candidate universe** and required both sides to agree. I told the engineer to
widen that universe with `tasks_status_check`'s vocabulary.

It measured first, and the measurement killed the prescription: `tasks_status_check`'s vocabulary is a
**strict subset** of the SQL allow-list union the proto, so widening adds **zero members** and the
mutation still passes. The mutation in question is `cancelled` added to the Go mirror alone - it survived
**three separate widenings** of the candidate set, because a universe can only ever contain statuses
somebody has already written down somewhere, and the edit the guard exists to catch is somebody writing
one down **here first**.

The shipped answer extracts the string literals from `taskStatusIsWritable`'s own body via `go/ast` and
compares them to the parsed SQL allow-list **as a set, in both directions, with no candidate set in the
middle**. An AST parse rather than a text scan for a reason this repo has already paid for once: a `//`
comment naming a status is not a literal in the AST, so it cannot feed the parse the way it could feed a
grep - which is the lesson that deleted the `Table` `minWidth` source-scanning guard.

Two more things worth keeping from that guard:

- **The behavioural universe loop STAYS**, and it is not redundant with the stronger rung. It *calls* the
  function instead of reading it, so it survives a rewrite the literal parse cannot interpret, and it is
  what kills the deny-list rewrite (`case "done","failed","timed_out": return false` - measured, fails
  naming `prepare_failed`). The two fail on disjoint edits.
- **The comment strip is load-bearing and was found by running it.** `IncrementTaskRetryCount`'s doc block
  *quotes* `RetryJobTasks`' allow-list, so a parse over the raw block sees two clauses and reads a set that
  is not a predicate at all. A quoted allow-list in prose is exactly what a guard over SQL must not mistake
  for the statement's own.

> **A guard whose universe comes from the sources it is checking cannot catch an addition to one of them.**
> Compare the two sets directly, or the guard is checking whether the sources agree about things they
> already agree about.

---

## The stated-honesty line moved twice more, both times in the right direction

Slice 3's headline was that "structurally impossible" is the wrong word when a priced alternative exists.
This slice inherited that wording and then found two more places where a claim was stronger than its
evidence:

- **`record`'s bounds check.** The shipped comment claimed one test kept the branch unreachable. Two do,
  and it takes both - measured. The comment now names both and says which mistake each sees. It also
  refuses to restore a `< 0` arm copied from `ingestLogCounters.record`: `taskStatusFenceReason` is
  `uint8`, so `int(r)` cannot be negative and such an arm would be **dead code wearing the costume of a
  control**. The sibling's `i <= 0` is live only because `logKind` starts at 1.
- **The classifier's "the only statement that reopens a terminal row".** That is a claim about the
  complement, which CLAUDE.md already warns cannot be checked by opening its subject - and it was wrong:
  `tasks.sql` carries a second epoch-only status write with neither a status predicate nor a bump, which
  could move a row terminal -> writable at a fixed epoch, precisely the shape the classifier assumes away.
  The claim holds **only because that statement is test-only and only because that is enforced** by
  `internal/store/updatetaskstatusepoch_guard_test.go`. The comment now names it by **file** rather than by
  identifier, because spelling the identifier turns that guard RED - measured, not assumed.

That second one is the good artifact of the pair: a comment that discovered its own silent dependency,
named the guard holding it up, and told the next reader to re-derive the paragraph if the guard is ever
relaxed.

---

## What five slices of this cluster bought, and what the facility cost

The individual slice retros could not say this, so it belongs here.

**The joint spec scoped four items and four PRs.** Slice 4's retro says "slice 4 of 4 - the cluster closes
here". That was wrong by one, and the miss is instructive rather than embarrassing: the two
`handleTaskStatus` items were filed *by* slices 2 and 3 as they went, so they could not have been in a
spec written before those slices ran. **This slice is the fifth consumer of a facility designed for
four, and the first built with no spec of its own** - which makes it the only real test of whether the
design generalises rather than fits.

**It held.** Across five consumers, nothing in the mechanism was redesigned:

- `CounterSources` + one nil filter per source, absence meaning "not wired" and zeros meaning "ran and
  stopped nothing" - unchanged since slice 1, five sections in.
- The `counts` / `levels` split - unchanged, and this section is counts-only for a stated reason.
- The **cardinality rule** stated up front in slice 1 ("a counter key must come from a set the server
  enumerates") - never relaxed. It survived its hardest test in slice 4, where the only peer-keyed map in
  the payload shipped with a hard 256 bound plus an overflow counter; this section is keyed on nothing.
- `cmd/relay-server/counters_wiring_test.go`'s completeness relation - rewritten twice inside slice 3,
  then **held unchanged for slices 4 and 5**, and it fired exactly as the plan predicted (five source
  fields, six top-level keys) at the moment the section was renderable but unwired.

**Three things were decided per-slice rather than inherited, and that was correct each time.** The
concurrency primitive went mutex (netlimit) -> atomics (ingest) -> scalar (task-log fence) -> mutex
(watchdog) -> atomics (here), and each choice was argued from two properties - is there a cross-field
invariant, is there a mutable container - rather than copied from the precedent next door. Where the
type lives got three different answers, all **forced by import direction**: producer-owned with a
hand-written mapper (slice 2), consumer-owned and used directly (slice 4), producer-owned and **wrapped**
with no mapper at all (here). Of those three, exactly one produced a defect: **the mapper**. That is the
cluster's one transferable engineering conclusion - prefer the shape with no mapper, and where the import
direction forces one, the arity guard ships in the same commit.

**What it cost.** Five slices, every one of which needed at least one remediation round and several of
which needed two or three. Something like sixteen structural-guard evasions across the batch, plus two
more here. And the recurring finding across the whole cluster is recursive and should be said plainly:

> **The mechanism built because controls silently stop reporting kept almost shipping counters that could
> silently stop counting.** Slice 2: a fully correct sixth log kind, counted on the recv path and
> published under no JSON key, three packages green. Slice 5: a fully correct fourth reason, declared one
> position too late, dropped by its own bounds check, three packages green. Same defect, two layers apart,
> three slices apart, and **both times the fix was an arity or AST guard rather than a behavioural test** -
> because in both cases the behavioural test iterates the same sentinel the bug moved.

That is the number I would carry forward if only one survives: **two of five slices shipped a counter that
could have counted into a cell nobody reads, and neither was caught by any test that counts things.**

---

## Process shape

- Conductor-run `/code-review`, then a four-lens fan-out (invariants, correctness, security,
  integration-tester).
- Remediation rounds, with the security HIGH landing as documentation-plus-doc-comment and **zero
  executable change** - the first finding in this batch whose whole remedy was honesty.
- The mutation matrix ran in an isolated detached worktree, as the standing rule requires. One survivor
  out of nineteen, chased to a second unlisted instance and closed with a permanent test.
- **Sixth consecutive iteration needing at least one remediation round.** The honest reading is unchanged
  from slices 4 and 5 of the previous batch: the fan-out is what makes these slices correct, not the
  implementation pass.

## What Was Built

- **`internal/worker/taskstatus_fence_counters.go`** (new) - `taskStatusFenceReason` (a dense run from
  **0**, deliberately unlike `logKind`'s `iota+1`, because this array is sized by the sentinel and indexed
  directly), `statusFenceCounters` (`[fenceReasonCount]atomic.Uint64`, a **value** field on `Handler` so
  the zero value works and there is no nil case), `record` (fails closed, never panics - a panic on the
  recv goroutine kills the process and `Connect` has no `recover`), `snapshot`, the exported
  `TaskStatusFenceCounts` with its JSON tags, `taskStatusIsWritable` and `classifyStatusFenceRejection`.
- **`internal/worker/handler.go`** - `h.statusFence.record(classifyStatusFenceRejection(task.Status,
  statusStr))` on both `pgx.ErrNoRows` arms; `lim.allow(logKey{kind: ...})` on all three database-error
  lines; both arms rewritten from `if !errors.Is` to `if errors.Is { record } else if lim.allow { log }`
  so "no input executes both" is a property of the branch structure rather than a claim in two comments;
  the identity gate's fourth job; and the `lim` paragraph corrected from two budgeted lines to five, with
  the above-the-gates / below-the-gates split stated.
- **`internal/worker/ingest_log_limiter.go` / `ingest_log_counters.go`** - three kinds with the
  three-versus-one argument in the const block (mutually exclusive per message; different remedies -
  `FailDependentTasks` is a recursive CTE and the first thing to deadlock), three fields, three `byKind`
  lines, and a "what the budget covers" paragraph naming all eight sites and what is still outside.
- **`internal/api/server_counters.go`** - `TaskStatusFenceSource`, `taskStatusFenceSection` **wrapping**
  `worker.TaskStatusFenceCounts` (no mapper, no arity), the section doc block, and the endpoint doc
  amended from two controls on one `*worker.Handler` to three.
- **`cmd/relay-server/http_server.go`** - one assignment inside the existing `if d.agentHandler != nil`.
  No new `wiredDep` row, because the section reuses a deps field that already has one.
- **Tests** - fifteen new top-level functions in `internal/worker/taskstatus_fence_counters_test.go`
  including the two AST guards, the three-rung SQL lockstep guard, the identity pin pair, the
  operand-order survivor and the `-race` exactness test; plus the `internal/api` section tests, the
  `cmd/relay-server` route test, and counter assertions on two existing integration tests so the
  classification is driven once by **real** Postgres rather than only by a stub.
- **`internal/store/query/tasks.sql`** - comment text only, then `make generate`. Two false claims
  corrected. `git diff -- internal/store/` is comments-only apart from one test file.
- **`README.md:1300-1308`** - the three new `ingest_log_budget` keys, the `task_status_fence` reading
  bullets, the exact/floor asymmetry, the forgeability bullet, and the not-a-census bullet.

## Key Decisions

- **Split by REASON, not by statement**, against the item's own proposal. The two statements carry
  identical predicates; which runs is decided by the reported status and the retry budget, not by anything
  about the rejection; and both mean the same thing to an operator.
- **No `rejected_total`.** Three keys partition the rejections, so a published sum would sit beside its own
  summands where it can only agree or be a bug - the defect slice 4's plan refuted in the joint spec's own
  payload.
- **Three log kinds, not one.** Mutually exclusive per message, and "your recursive cascade is timing out"
  and "your simple updates are failing" are different incidents.
- **Atomics, with the mutex reasons checked rather than copied.** No cross-field invariant (a consequence
  of publishing no total) and no container; against a recv goroutine whose standing constraint is no new
  lock, queue, goroutine or round trip.
- **`Dispatcher.failClaimedTask` left OUT**, with the price stated in three places. Its target is
  `dispatched` by construction, so every rejection there is `raced` - a one-valued partition is not a
  partition - and it counts a different noun. `task_status_fence` says in its own doc comment and in README
  that it is **not a census**.
- **`handleTaskLog`'s marshal line left unbudgeted**, with the argument written down rather than the
  decision left implicit: no input is known to reach it, and "revisit if a field is added to
  `taskLogEvent`".
- **Per-predicate splitting declined ON PRICE, never "impossible"** - slice 3's correction, inherited
  deliberately and with slice 3's own wording named as the model.

## Findings Triage

- **1 HIGH, security, and its entire remedy was prose.** `conflicting_total` / `duplicate_total` are
  forgeable by the task's own assignee, indefinitely, at one unbudgeted message per increment - and the
  README's prescribed response to the signature they produce widens the window the watchdog exists to
  close. Disclosed where the signal is read; **not closed**, and closing it needs a second round trip on
  the recv goroutine. Recommended as an item.
- **1 HIGH, guard: a fourth reason declared after the sentinel is silently lost**, all three packages
  green. Closed by an AST guard whose sibling type already had one.
- **1 HIGH, guard: the identity gate's newly-claimed fourth job was unpinned.** Deleting the gate left
  everything green while a non-assignee drove `Conflicting: 1000`.
- **1 mutation survivor out of 19 (M18), plus a second instance the plan never listed.** Operand order in
  an `&&`; compiles, vets clean, changes no log line, drains a shared 16-token bucket.
- **1 refuted remediation prescription (mine)**, killed by measurement before it was implemented.
- **2 over-strong claims corrected in shipped comments**, one of which found a genuine silent dependency
  on a test-only SQL statement and named the guard holding it up.
- **7 item/plan claims refuted at planning time**, one of them the item's motivating mechanism.

## What Remains Open

- **The fence counters are forgeable by the assignee, and the documented remedy helps the forger.**
  Disclosed in README and in the type's doc comment; unbounded in code. **Recommended as an item below**,
  and it is the second instance of a shape already filed for `task_log_fence`.
- **The bound this slice bought for the three status lines is conditional on a bucket another kind can
  drain.** All three now consult the budget; the budget is one 16-token bucket per connection shared
  across all eight kinds, and `kindTaskLogPersist`'s key carries a wire value. **Recommended as an item
  below.**
- **`Dispatcher.failClaimedTask` remains a ready but uncounted fence-rejection site**, declined on merit
  here. **Recommended as an item below, framed as the question rather than the fix.**
- **`markWorkerOffline`'s teardown line is the thirteenth `log.Printf` in `handler.go` and belongs to
  neither class of the registration-budget item.** Recommended as an amendment, not a new file.
- **`task_status_fence` is the fourth section whose `internal/api` payload guards see only fixtures.**
  Recommended as an amendment to the item slice 4 already filed for this.
- **The status-writability mirror is a hand copy of a SQL allow-list.** The plan proposed filing this; **I
  recommend not filing it.** The guard is already at the top rung of the ladder (a two-way set comparison
  read out of the mirror's own AST), no third consumer exists, and an item whose trigger is "if a third
  consumer ever appears" has no trigger. The decision is recorded where the next reader will hit it.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **twenty-second iteration**.
  Seven refutations, one of them the item's motivating mechanism.
- **A backlog proposal is not a contract** - twenty-two for twenty-two, and this is the sharpest instance
  yet: the item's own proposed split would have merged the healthy case with the actionable one.
- **Each stage treats the previous stage's output as untrusted** - honored in all three directions,
  including the engineer refuting the conductor's remediation prescription by measurement.
- **A mutation proof must leave a test behind** - honored; `TestHandleTaskStatus_TheSilentArmsSpendNoBudget`
  is permanent and carries a positive control.
- **A mutation proof is only as strong as the poisoned input's POSITION** - honored at three sites (the
  conflicting leg first in both arm tests, the forged leg first in the identity pin).
- **A guard must enumerate what is ALLOWED** - honored: the writable set is compared as a set in both
  directions, and the reason walk fails **closed** on any value it cannot name.
- **Wrong prose about correct code is the dominant defect class** - **seventeenth consecutive iteration**,
  and this time the worst instance was in README rather than in a comment, which is a wider blast radius.
- **Say "declined, and here is the price"** - honored at four sites (`failClaimedTask`, per-predicate
  splitting, the marshal line, the forgeability).
- **State a coverage limit rather than implying it** - honored: not a census, not comparable with
  `task_log_fence`, will not reconcile with `watchdog.swept_total`, counts reports not tasks.
- **Backlog housekeeping is required scope** - the two closes and their `git mv` belong to the conductor,
  via `/backlog close`, never a hand-edited `status:`.

New from this iteration:

- **A fence proves currency and identity; it never proves honesty, and anything DERIVED from a rejection
  inherits that gap.** **Candidate for durable memory, and for CLAUDE.md - see below.**
- **A short-circuit's operand ORDER is the control.** Both orders are correct; only one is free for the
  cheapest caller. The test that catches a swap asserts on the **budget**, not on the output.
- **When a change gives an existing control a new job, the new job needs its own subject.** The gate did
  not move; what depends on it did, and the old tests cannot see the new dependency.
- **A guard whose candidate universe is drawn from the sources it compares cannot catch an addition to one
  of them.** Compare the sets directly. Measured over three widenings.
- **Do not copy a sibling's bounds check without re-deriving its type.** `i <= 0` is live for `logKind`
  (starts at 1) and dead for `taskStatusFenceReason` (starts at 0) - dead code wearing the costume of a
  control, and it would have excluded every `raced`.

## Files Most Touched

- `internal/worker/taskstatus_fence_counters.go:86-125` - "what these numbers do not cover", including the
  forgeability paragraph with the measurement and the "the consequence is not the number, it is what the
  number is read as" sentence. This is the paragraph the security HIGH produced.
- `internal/worker/taskstatus_fence_counters.go:154-187` - `record`'s bounds check, the two-tests
  correction, and the argument against restoring a `< 0` arm.
- `internal/worker/taskstatus_fence_counters.go:239-280` - the classifier, its silent dependency on a
  test-only SQL statement, and why that statement is named by file rather than by identifier.
- `internal/worker/handler.go:1099-1110` - the identity gate's fourth job.
- `internal/worker/handler.go:1210-1222` and `:1270-1282` - the two arms, now `if errors.Is / else if
  lim.allow`, with the "note which predicate actually bites here" correction at the update arm.
- `internal/worker/taskstatus_fence_counters_test.go:58-92` - the sentinel AST guard and the measured
  evasion it closes.
- `internal/worker/taskstatus_fence_counters_test.go:297-380` - the SQL lockstep guard's three rungs, the
  comment-strip discovery, and the record of the universe loop failing three times.
- `internal/worker/taskstatus_fence_counters_test.go:1297-1380` - the operand-order survivor.
- `README.md:1300-1308` - the six bullets an operator actually reads, including `:1307`, which is the
  security remediation in full.
- `docs/superpowers/plans/2026-08-24-handletaskstatus-pair.md` - the seven refutations, the design
  decisions D1-D8, and the 19-row mutation matrix.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No `git log`, no
  `git diff`, no test run. Every claim below that could be checked by reading was checked against the
  worktree.
- **Verified by reading:** `internal/worker/taskstatus_fence_counters.go` in full;
  `internal/worker/taskstatus_fence_counters_test.go` at `:39-238`, `:297-460`, `:1000-1135` and
  `:1290-1381`, plus its full test inventory; `internal/worker/handler.go` at the five changed regions;
  `README.md:1286-1313`; the plan in full; both closing items in full;
  `docs/superpowers/specs/2026-08-21-silent-drop-observability.md:1-40` and its section index;
  `docs/retros/2026-08-24-silent-drop-observability-slice4.md:1-60`;
  `docs/retros/2026-08-24-finishregister-strand.md` in full; and
  `docs/backlog/idea-2026-08-21-rejected-total-is-forgeable-and-its-remedy-helps-the-forger.md`,
  `idea-2026-08-21-per-stream-log-budget-renewal-is-unpriced.md` and the full open-backlog listing for
  duplicate checking.
- **Confirmed against code, not inferred:** that both `pgx.ErrNoRows` arms call
  `record(classifyStatusFenceRejection(...))` and both write arms are `else if lim.allow`; that
  `FailDependentTasks` is budgeted and has no `errors.Is` gate; that `record`'s bounds check is an upper
  bound only; that the reason run starts at 0 and the sentinel is last; that
  `TestEveryTaskStatusFenceReasonIsDeclaredInsideTheSentinel` parses the package rather than one file and
  fails closed on unnameable values; that the writable-set guard compares as a set in both directions from
  an AST literal extraction; that the identity pin drives 1000 forged messages with a same-handler positive
  control; that the `.Valid` pair is driven together; and that README `:1307` carries the forgeability
  disclosure with the cross-check instruction.
- **Reported by the implementing and verifying lanes, not re-run here:** unit 637 -> 656; integration green
  across 21 packages; the `-race` Linux-container run and the Windows TSan control against `origin/main`;
  `GOMAXPROCS=1 -count=30` with no flakes; the 24-commit set; `git diff -- internal/store/` being
  comments-only; the web lane; and every mutation result, including the 10,000-message forgery
  measurement, the three-package-green fourth reason, the three-package-green gate deletion, and the M18
  survivor.
- **One arithmetic reconciliation the conductor should confirm, and it resolves a flag from the previous
  retro.** `docs/retros/2026-08-24-finishregister-strand.md` reported 626 -> **638** and flagged that it
  could only enumerate **eleven** new tests against a delta of twelve. This slice's reported baseline is
  **637**. If 637 is right, the finishRegister delta was 11 and matches its own enumeration exactly. Take
  637 as the baseline and treat that retro's flagged gap as closed; confirm with one `go test ./...` before
  the PR is assembled.
- **Not verified:** all test results, the commit count, the diff stat, and the change set as `git` sees it.
  Each is attributed above.
- **No PR number appears anywhere in this retro or in the proposed items**, by instruction. The work is
  referenced by date and slug. The four merged predecessors (#139-#142) are named because they exist.
- **Outstanding and belonging to the conductor:** both `/backlog close` runs with their `git mv` into
  `docs/backlog/closed/`, the item filings and two amendments below, the CLAUDE.md decision, the file-set
  check, the final gates, all commits, and a ROADMAP refresh.

## CLAUDE.md verdict

**One amendment is earned. Both of the candidates offered are declined, and I want to be plain about why
rather than take all three.**

**Declined: "a fence rejection's classification is a different question from the fence itself."** True, and
it is a good design note, but it is not bypassable and it is one instance. Nothing about it fails open
next month. It is recorded where it belongs - in `classifyStatusFenceRejection`'s doc comment, which
already says what it knows and what it does not.

**Declined: "the counter counts messages, not incidents."** This is a payload-documentation caveat, and it
is already written in three places that an operator or an implementer will actually reach: the type's doc
comment, the section's doc comment, and README `:1307`. A caveat about one section's semantics is not a
cross-cutting rule that new code must not bypass, and putting it in the Invariants dilutes the list.

**Earned: the third rung of the currency/identity ladder, appended to the Epoch fence bullet's existing
"The epoch establishes currency, not identity" sentence.** The argument for this one and not the others:
it has fired **twice in five days** (`task_log_fence.counts.rejected_total` on 2026-08-21, this section
today), the rule was **already written down in a backlog item** and was still not applied the second time,
it is stated as a question a reviewer can ask, and its enforceable half - pin the gate a derived signal
depends on - is a test that goes red.

> **And an identity check establishes identity, not honesty.** A `worker_id` predicate proves the sender is
> the assignee; it proves nothing about whether the report is true. Every terminal transition bumps neither
> the epoch nor `worker_id`, so an assignee may contradict its own outcome at the same epoch forever. That
> is harmless for the ROW - the fence still refuses the write - and it is not harmless for anything
> DERIVED from the rejection. So when a fence rejection starts feeding a counter, a log line or an audit
> record, ask two more things. **First, what does a peer who can move this signal gain, and is the signal's
> own documented remedy in their favour?** `task_status_fence.counts.conflicting_total` is one increment
> per forged message, unbudgeted and unbounded, and README told an operator that a climbing value means
> `RELAY_TASK_WATCHDOG_MARGIN` is too small - so the prescribed fix widened the unbounded-assignment window
> the watchdog exists to close. The counter was not the defect; the advertisement was. State the
> forgeability where the signal is READ, in the commit that ships it. **Second, pin the gate the new signal
> leans on.** The SQL identity predicate protects the row; only `handleTaskStatus`'s Go gate keeps the
> counters attributable, and deleting that gate left every package green while a non-assignee drove
> `conflicting_total` at will. A control that acquires a new job needs a test with that job as its subject.

If the conductor wants to keep that bullet from growing further, the first half stands alone as a new
one-line Invariant and the second half can live in the finishRegister slice's Amendment 2 territory
("Identity-checked teardown"). I would take it whole in the epoch-fence bullet: the reader who needs it is
the reader already reading about currency and identity.

## Recommended Backlog Items

Proposals only - the conductor files via `/backlog`, and the human gives final accept. Three new files and
two amendments. Every factual claim was verified by reading the worktree in this pass, and each names its
evidence. **One candidate is deliberately NOT proposed** (the writability mirror; see What Remains Open).

**1. `task_status_fence`'s conflicting and duplicate counters are forgeable by the task's own assignee, and
the documented remedy helps the forger**
- type: `idea`, priority: `medium`
- The identity gate proves the sender is the assignee; it does not prove the report is true. A terminal
  transition bumps neither `assignment_epoch` nor `worker_id`, so an agent reporting `done` at epoch N and
  then `failed` at epoch N passes both Go gates legitimately, every time, and each message adds one to
  `conflicting_total`. Nothing rate-limits status messages and this path spends no log-budget token, so the
  climb is smooth, silent and unbounded. **Measured this slice: 10,000 forged messages give
  `{Raced:0 Duplicate:0 Conflicting:10000}` with every other counter flat.**
- **The exposure is misdirection of the one signal, not disclosure.** An agent cannot read
  `/v1/server/counters` and the payload carries no identifiers. What makes it worth an item is that the
  shape produced is *exactly* the watchdog-margin signature, whose documented remedy is to raise
  `RELAY_TASK_WATCHDOG_MARGIN` - widening the unbounded-assignment window the watchdog exists to close.
- **The documentation half is already closed** (README `:1307` carries the disclosure and the cross-check
  instruction; `TaskStatusFenceCounts`' doc comment carries the mechanism and the measurement). What
  remains is that the number is unbounded.
- **Read together with, and NOT merged into,
  [[idea-2026-08-21-rejected-total-is-forgeable-and-its-remedy-helps-the-forger]].** Same shape, disjoint
  mechanisms and disjoint remedy spaces: that one is **cross-task** (any enrolled agent can name any task
  id off the wire) and its candidate fixes are a chunk-rate bucket or a predicate split; this one is
  **self-task** (only the assignee, only its own assignment, and the per-reason split already exists), so
  neither of those fixes applies. The honest remedy menu here is a rate bound on status messages per
  connection, a second "reports after the bound" counter following `ingest_log_budget`'s two-arm
  precedent, or a written decision to leave it disclosed. **Do not solve it with a per-worker map** - that
  is the cardinality rule this whole cluster shipped.
- Acceptance should require that whatever ships keeps README's reading guidance true, and that the
  cross-check instruction survives.

**2. One log kind's dedupe key carries a wire value, so a peer can drain the shared bucket and suppress its
own other seven kinds' diagnostics**
- type: `bug`, priority: `low`
- The ingest budget is **one 16-token bucket per connection shared by all eight kinds**. Seven keys are
  keyed on nothing the caller supplies. `kindTaskLogPersist` is the exception: its key carries the chunk's
  epoch straight off the wire, and a chunk whose *content* Postgres refuses at Bind (a `\x00` byte,
  SQLSTATE 22021) fails **before** the fence's `WHERE` is evaluated - so neither the task id nor the epoch
  has to name anything real. Distinct keys are therefore free: 16 drain the burst and six a minute hold it
  at zero for the life of the connection. **Measured and already documented at README `:1301`:** 40 such
  chunks at distinct epochs logged 16 and suppressed 24 with `deduped` still zero, and the next
  malformed-task-id status message on that connection was suppressed while the same message on a fresh
  connection was not.
- **Why file it now rather than treat README as sufficient.** This slice's own deliverable is what changes
  its weight: the three `handleTaskStatus` write-error lines that previously fired **unconditionally** now
  draw on that bucket, so a peer that drains it silences the three diagnostics that report database faults
  on its own stream. The bound the closing item bought is real but **conditional on a bucket another kind
  can empty**, and that condition is now load-bearing rather than theoretical.
- The blast radius is one connection, and the suppression is itself counted, so a high
  `suppressed.task_log_persist` next to a quiet status log is the tell - which is why this is `low` and not
  `medium`. Candidate remedies to argue: drop the epoch from that key (it was added to bound a per-task
  failure, and a Bind-stage rejection is not per-task), or give the id-bearing kind its own sub-bucket.
- **Not a duplicate of [[idea-2026-08-21-per-stream-log-budget-renewal-is-unpriced]]** (that one is about
  renewing the bucket across streams; this is about draining it **within** one) nor of the closed
  `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` (that fixed the wire **spelling** escape by using the
  canonical id; the epoch **value** still comes off the wire, and the Bind-stage path means the id need not
  resolve either).

**3. Should the coordinator's own fence rejections have a section, or is the declination permanent?**
- type: `idea`, priority: `low`
- **Framed as the question on purpose.** `Dispatcher.failClaimedTask` and `Watchdog.SweepOnce` are refused
  by the same statements `task_status_fence` counts and are counted nowhere; slice 4 made `failClaimedTask`
  a ready site and deliberately left it uncounted so as not to pre-empt this slice's design. This slice then
  declined it **on merit**, not on cost: its target is `dispatched` by construction, so every rejection
  there is `raced` and a one-valued partition is not a partition; it counts a different noun (the
  dispatcher failing to record a terminal *it* decided, versus an agent's report being discarded); and it
  would need a fifth `CounterSources` field and its own section for one number. `task_status_fence`'s doc
  comment and README both say the section is **not a census**.
- Titling this "the rejection is uncounted" would bake the remedy into the title and pre-commit the reader
  to shipping a section this slice argued against. The two legitimate outcomes are a plain total under its
  own `dispatch_fence` section, or **closing the item as a written decision** - and the second is at least
  as likely. Acceptance should permit it explicitly.

**4. AMEND (no new file): [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]**
- Its site inventory is stale twice over. `internal/worker/handler.go` now has **thirteen** `log.Printf`
  sites, of which **eight** are budgeted (the three this slice added). Record that the three
  `handleTaskStatus` sites it listed as unbudgeted are now closed by
  `bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget`.
- Record that `markWorkerOffline`'s teardown line, added 2026-08-24 by the finishRegister slice, belongs to
  **neither** of that item's two classes: it is not registration-time, and it has no `lim` on its call
  chain because it runs on the teardown path. It is bounded by the connection caps rather than by message
  volume, and this slice recorded that reasoning in `ingest_log_limiter.go`'s "what the budget covers"
  paragraph and in README `:1300`. The item should adopt it or refute it, not silently inherit a fourth
  site.

**5. AMEND (no new file): [[idea-2026-08-24-counter-payload-guards-check-fixtures-not-producers]]**
- `task_status_fence` is the **fourth** section whose `internal/api` payload walks see only literals
  declared in `server_counters_test.go`. `cmd/relay-server`'s new section test is again the only place real
  producer bytes reach the route, and it asserts key names and explicit zeros rather than a
  producer-driven value.
- Record the one thing that is genuinely better here and does not generalise: this section **wraps** the
  producer's type instead of restating it, so there is no mapper and no arity to drift. That removes the
  slice-2 defect class for this section only. It does not remove the fixture-versus-producer gap, which is
  about which **bytes** the walk sees.
