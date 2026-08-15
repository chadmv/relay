---
date: 2026-08-15
topic: tasklog-err-limiter-keying
slice: 2026-08-15-tasklog-err-limiter-keying
branch: claude/pr-merge-main-2d2fc3
range: origin/main..HEAD (backend only, Go only, zero SQL, zero files under web/, green, not yet merged)
closes: bug-2026-08-12-tasklog-err-limiter-attacker-keyed
---

# Session Retro: 2026-08-15 - the file stated the principle correctly in one comment and violated it in the next

**TL;DR:** `internal/worker/ingest_log_limiter.go` replaces the package-global `taskLogErrLimiter`
with a **per-connection budget allocated as a stack local in `Connect`** and threaded into
`handleTaskLog`, `handleTaskStatus` and the new `handleInventoryUpdate`. It is two things stacked and
the split is the whole point: a **dedupe map with a time-based re-arm** (`ingestLogDedupeWindow`, 5m),
keyed on wire values on purpose, and a **token bucket keyed on nothing** (`ingestLogBurst` 16,
`ingestLogRefill` 10s), which is the bound. Plus `clipID`, canonical-id logging, and the two bad-id
kinds split apart. Backend only, Go only. Unit 487 -> 491 top-level, worker integration 75 -> 100,
`-race` green.

**The centrepiece is not the limiter. It is that the limiter's own file argued against its own bug,
in capitals, two comments above the code that had it.** Everything else here is the sixth consecutive
iteration of "every stage found errors in the stage before it" - and this time the conductor was one
of the stages.

## The file argued against its own bug, two comments later

`ingestLogLimiter.allow`'s capacity branch carries a defence of clearing the map, written before
review:

> The alternative, permanent suppression, is also bounded but has **NO TIME-BASED RECOVERY**: a
> connection that once tripped 128 distinct failures would lose the diagnostic for its whole lifetime.

That paragraph is correct. It is also, at the moment it was written, a description of the code
directly above it. **Four of the five kinds carry no wire value.** `kindBadTaskIDLog`,
`kindBadTaskIDStatus`, `kindStatusGetTask` and `kindInventory` are each exactly **one key** for the
connection's entire life. With a bare presence flag in `seen`, none of them can ever reach 128
entries, so none of them can ever reach the branch whose comment is arguing that permanent
suppression is unacceptable. Each of them logged **once per connection and then never again**.

The correctness lens measured it rather than reasoning about it: one failing inventory message per
second for seven simulated days on a frozen clock gave `allowed = 1`, with the bucket still full. A
Postgres outage at hour 1 reported one line; a second, unrelated outage at hour 40 on the same
long-lived stream reported nothing at all. That is a **regression against the unbounded logging this
type replaced** - shipped inside the slice whose entire subject is bounding that logging.

This project has the lesson already, in durable memory, from a different slice:
`reference_recovery_bound_must_be_time_based` - "a recovery bound must be time-based; reset on
duration, not on activity". The comment states it accurately. The code one screen up did not honour
it. The fix is `ingestLogDedupeWindow`, chosen against the bucket from both sides (long enough that
one re-arming key costs 0.6% of the steady-state budget, short enough that a new episode surfaces
within 5 minutes), and the comment now says which of the two mechanisms is the general answer and
which is only the map's size bound.

> **Stating a principle in a comment is not the same as checking the code against it.** A comment
> that names a hazard reads as evidence the hazard was considered, and it is the single most
> convincing artifact in the file - which is exactly why nobody re-derives it. When you write "the
> alternative would be X, which is bad", the next question is not whether X is bad. It is which of
> your own branches is X.

This is the same defect class as the ten-iteration "wrong prose about correct code" streak, inverted:
here the prose was **right** and the code was wrong. That is strictly worse, because right prose
immunizes the code sitting next to it.

## The item's prescribed remedy failed open, for the second iteration running

The previous retro named this as its transferable lesson (`reference_accurate_item_wrong_remedy`:
accuracy about a bug is not accuracy about its remedy). It recurred immediately, and sharper.

`bug-2026-08-12-tasklog-err-limiter-attacker-keyed` asked for "key on the authenticated worker and cap
per worker", and named the specific defect as the at-capacity `reset()` re-arming every key at 1024.
Both halves are wrong in the same way, and the reason is one word: **epoch is the map VALUE, not part
of the key.**

```go
reported map[string]int32 // task id -> the assignment epoch already logged
```

So with one fixed task id and a varying `chunk.Epoch`: the lookup hits, `got != epoch` so the early
return is skipped, `len(l.reported)` is 1 so the capacity branch never fires, the entry is
overwritten, and `shouldLog` returns true. **One log line per message, forever, from a map of exactly
one entry, with `reset()` never called.** A patch implementing the item literally - per-worker map,
capped, reset fixed to suppress - closes nothing at all.

And the resource the item named was the wrong one. 1024 small entries were already bounded; **the
unbounded resource is the process-global `log` mutex**, which every other connection's error paths and
every HTTP handler serialize on. That reframing is what turned this from a memory-hygiene item into a
cross-tenant degrade vector, and it is what made the honest severity assessment possible.

> **An item that names a mechanism has usually named the mechanism it saw, not the one that binds.**
> "Key on X and cap per X" is a shape claim about a data structure. Open the structure and ask which
> field is the key, because a cap on a map the attacker cannot grow is not a cap.

## The design story changed twice, and the second change refuted the spec's headline mutation

The spec's mutation matrix listed, as a **must go RED**, "move `epoch` from the key back to the map
value". The planner refuted it: once the bucket exists, moving the epoch into the value is
**behaviour-preserving**. Sixty-four varying epochs on one task id are still 64 unreported events
requesting 64 tokens, and the bucket hands out 16. The map shape stopped being a security control the
moment something else became the bound.

The corollary is the durable part and it is now the file's doc comment:

> **The composite key is required because one map holds five kinds of key, NOT because it closes the
> flood. The bucket closes the flood.** The key shape is a diagnostics decision; the bucket is the
> security control.

A review lens later verified this empirically rather than accepting it: it rewrote the limiter to hold
the epoch in the value and confirmed the flood tests still pass. Two independent artifacts now say the
same thing, one by argument and one by measurement, which is why the claim is safe to write in a
comment that will outlive both.

## The planner rejected the spec's test seam, and the argument is reusable

The spec accepted a **throwaway-limiter-per-call** wrapper in `export_test.go`, purely to keep ~41
existing test call sites byte-identical and avoid a mechanical diff that would bury the assertion
changes that matter. The planner rejected it on a ground the spec had not considered: **it destroys the
behavioural RED.**

A throwaway limiter per call exercises no limiting at all. So the headline flood tests could not be
written against `HandleTaskLog` at all - they would have to be written against `HandleTaskLogWithLimiter`,
a symbol that **does not exist at HEAD**. The "RED" would be a compile error, and the test that
eventually goes green is not the test that was red. That is the same staging rule the previous slice's
plan applied to its own tasks and failed to apply to its own mutation battery; here it was applied to
a *test seam*, which is a third place it belongs.

The shipped seam is **one limiter per `*Handler`**, allocated lazily in `export_test.go` behind
`shimLimiterFor`. Its comment states the property honestly in both directions: "one Handler equals one
connection" is TRUE in the shims and FALSE in production, so the shims are usable evidence for what one
connection's budget does and are **never** evidence for isolation between connections.

The planner also found the spec's isolation test **vacuous**. As specced,
`TestIngestLogLimiter_PerConnectionBudgetsDoNotInterfere` asserted only that two separately-constructed
structs are independent - which is true of any two structs and proves nothing about the allocation site.
It was replaced with `TestConnect_TwoConnectionsDoNotShareTheLogBudget`, driving **two real `Connect`
streams against one `Handler`**, which is the property production actually depends on and the only test
that reddens if somebody moves the limiter onto `Handler`.

## Every stage found errors in the stage before it, and the conductor was a stage

Six links, and the last one is the one worth keeping.

1. **The spec refuted the item** on its central mechanism (above) and **struck an acceptance criterion
   as unachievable**: "a bounded number of `GetTask` round trips, proven by a test RED against today's
   handler". There is no way to know a well-formed UUID names no task without querying; the item forbade
   a new query and a cache; and dropping status messages once a budget is exhausted would strand real
   tasks. The query cost is already bounded at one in-flight statement per connection, which is the same
   bound legitimate traffic has.
2. **The planner refuted six spec claims**, including the headline mutation and the test seam above.
3. **The engineer found five plan errors.** The sharpest: a required test that **could never have gone
   green**. The plan's `clipID` assertion was RED on both sides, because pgtype's parse failure is
   `fmt.Errorf("cannot parse UUID %v", src)` and therefore carries a **verbatim, unescaped second copy
   of the caller's bytes**. The plan's own comment said "%q is the injection defence" and did not close
   the hole its own test was measuring. Both bad-id sites now render the **error** with `%q` and
   `clipID` too, which is the non-obvious half. Three of the plan's mutations were also mis-staged: one
   a no-op, one that could not cross its own bound, one that did not compile.
4. **The lenses found what all of that missed** (next section).
5. **The conductor's remediation brief contained an unsatisfiable assertion.** It asked for
   `NotContains("FORGED")` on the auto-enroll hostname fix. That can never pass: `%q` escapes the
   newline but leaves the text **visible and inert** inside the quoted hostname, which is the entire
   point of the defence. The engineer caught it and asserted the property that actually matters -
   `assert.Equal(t, 1, strings.Count(out, "\n"))`, "the whole audit record must be one physical line" -
   plus a `NotContains` on `"\nworker: auto-enrolled worker FORGED"`, the forged *line*, not the forged
   *string*.
6. **One engineer refutation of a review finding, and it was right.** The conductor's Finding 1 called
   the once-per-connection suppression "a regression against `origin/main`" for all three constant-key
   kinds. True for `kindInventory` and `kindStatusGetTask`. **Not true for the log-path bad-id line**,
   which `origin/main` did not emit at all - that line is new in this slice, so "once per connection"
   is an improvement over silence, not a regression from anything.

> **A remediation brief is an artifact and gets the same treatment as a spec or a plan.** Link 5 is an
> instruction that could not be satisfied by correct code; link 6 is an instruction that over-claimed
> its scope by one third. Neither would have reddened anything. The engineer refuting the conductor
> twice in one pass is the pipeline working, not friction in it.

## Four lenses, four distinct contributions, two convergences

- **Invariants and security both found that `pgtype.UUID.Scan` does not constrain the bytes it
  accepts.** For a 36-byte input `parseUUID` splices `src[0:8]+src[9:13]+src[14:18]+src[19:23]+src[24:]`
  and **never checks that indices 8, 13, 18 and 23 are hyphens**, so four bytes are fully
  attacker-chosen and never inspected. Both proved it with probes:
  `Scan("aaaaaaaa\nbbbb\ncccc\ndddd\neeeeeeeeeeee")` succeeds.

  Security added the half that mattered most. Because `logKey.id` held the **wire** string, those four
  bytes gave a caller **2^32 distinct dedupe keys for one (task, epoch) pair** - defeating the file's
  own stated property in the same slice that stated it. And rendering the wire string with `%s` turned
  one event into **five physical log lines**. One fix closed both: `canonicalID := uuidStr(taskID)`, the
  canonical re-encoding, in the log line **and** in the key. Pinned by
  `TestHandleTaskLog_TheLoggedTaskIdIsCanonicalNotTheWireString`.

  Two lenses converging on one library behaviour from opposite arguments - "what does the fence
  constrain" versus "what can the caller vary" - is the same signal shape the previous slice got on
  parenthesization.

- **Correctness delivered the predicted "sixth artifact error": `logKey.kind` had zero coverage.**
  Inserting `k.kind = 0` at the top of `allow` and running the **entire** worker package, unit plus
  integration, reddened **nothing**. Five kinds, all collapsible into one, with no test noticing.
  Remediated by `TestIngestLogLimiter_EveryKindIsItsOwnDedupeKey`.

- **Correctness measured the two behavioural findings rather than reasoning about them.** The
  seven-simulated-days `allowed = 1` result above, and the shared-bad-id-key result: **one forged
  `TaskStatusUpdate{TaskId: "z"}`** followed by 64 malformed log chunks gave **0 log-path lines** and 1
  status-path line. The two bad-id kinds were split apart on that number. The sharing was defending
  **one token out of sixteen**; what it cost was the only signal anywhere for an agent losing 100% of a
  task's output to unparseable ids.

- **Integration proved the hostname forgery end to end** through the real `autoEnrollAndRegister` path
  rather than through a unit shim, capturing the emitted two-line forgery before the fix. It also
  settled the NUL-before-fence premise against a real database, **including the wrong-assignee leg** -
  confirming that a NUL-bearing chunk fails at bind-parameter decode before the fence CTE runs, so the
  fictitious task id and the wrong assignee are both irrelevant to which arm the error lands in.

## A cross-lens disagreement, resolved by a third measurement

Two lenses disagreed about a figure destined for a source comment: the byte count of a malformed-id log
line under a 100k-byte id. The invariants lens said 200080; the correctness lens said 205544.

The engineer settled it by measuring rather than by picking. `captureLog` calls `log.SetFlags(0)`, so
there is **no timestamp prefix**: 200080 does not reproduce, **200060** is the single line, and
**205544** is exactly right for the **whole captured buffer** (that line plus its 64 short followers).
The two numbers were answers to two different questions and neither lens had said which.

Both numbers are now in the comment, **labelled**, with a sentence saying why: "the figure alone was
previously ambiguous between them". That is the correct handling. A disagreement between two careful
measurements is usually a disagreement about the measurand.

## The honest scope, and the residual named rather than implied

**What this buys.** Before, the bad-id path emitted one `log.Printf` per message with **zero** DB work -
the cheapest message an attacker can send. Now the whole caller-driven surface on the recv goroutine
costs roughly 17 lines per connection (burst 16 plus refill) against five queries' worth of work, which
the security lens sized at **four to five orders of magnitude on lines-per-attacker-CPU**.

**What it does not buy, stated so nobody has to rediscover it.** The bound is **per connection**, and
**connection admission is unbounded**. `cmd/relay-server/main.go` passes `grpc.KeepaliveParams` and
nothing else, so grpc-go's `MaxConcurrentStreams` default of `MaxUint32` applies, there is no
`keepalive.EnforcementPolicy`, no `MaxConnectionAge`, and no interceptor. "Per connection" is a bound
only if connections are bounded. Filed.

A lens raised an adjacent hazard that is **strictly worse than the log flood** and is the reason that
item is not cosmetic: with `RELAY_ALLOW_AUTO_ENROLL` on, a connect storm with varying hostnames creates
**unbounded `workers` rows** via `UpsertWorkerByHostname`. That is persistent state growth, not
transient lock contention.

Also unbounded, and deliberately out of scope: message *rate* on the recv loop, and the `GetTask` round
trips the item's struck criterion asked about.

## What Was Built

- **`internal/worker/ingest_log_limiter.go`** (new, 261 lines, most of them comment) - the
  `ingestLogLimiter` type, `logKind`'s five values, `logKey`, five constants, `newIngestLogLimiter` /
  `newIngestLogLimiterAt`, `allow`, and `clipID`. Read the type comment for the dedupe-versus-bound
  split, the constant block for why `ingestLogDedupeWindow` exists and why there is deliberately **no
  env knob**, and `allow`'s comment for the ordering claims.
- **`allow` fails closed on a nil receiver** rather than panicking. `Connect` has no `recover` and
  grpc-go does not recover handler panics, so a nil dereference there kills the server process; losing a
  diagnostic is the cheaper failure. Unreachable in production, which the comment says.
- **Four orderings in `allow`, three of them pinned, and the comment says which.** An earlier draft
  claimed each ordering had a mutation only it survives; a lens mutated all four and found two. The
  count was re-measured against the current unit suite and is now **three** (refill-before-spend,
  dedupe-before-spend, refill-before-dedupe). **The one that survives - moving the capacity clear above
  the dedupe check - is named in the comment as undefended.** Writing down that a position is uncovered
  is worth more than a comment that implies otherwise.
- **`internal/worker/handler.go`** - `lim` allocated as a stack local in `Connect` after `workerUUID`
  resolves, threaded into `handleTaskStatus`, `handleTaskLog` and the new thin `handleInventoryUpdate`
  wrapper. `handleTaskLog`'s error handling split into two explicit arms, the `pgx.ErrNoRows` arm left
  side-effect-free with a comment naming the counter item as the mechanism that belongs there. The
  status path's `GetTask` `ErrNoRows` line **deleted, not budgeted**. Both bad-id sites log `%q` +
  `clipID` on **the id and the error**. The persist-failure site keys and logs the **canonical**
  re-encoding.
- **`autoEnrollAndRegister`'s audit line** gained `%q` + `clipID` on `reg.Hostname`. It is the only
  record anywhere that a token-less enrollment happened, so a forgeable one corrupts the audit trail of
  the mechanism it documents.
- **`internal/worker/export_test.go`** - `shimLimiterFor` (one limiter per `*Handler`), `LimiterHandle`
  + `NewLimiterForTest`, `HandleTaskLogWithLimiter` / `HandleTaskStatusWithLimiter`.
  `ResetTaskLogErrLimiterForTest` deleted. The shim map's comment states, in as many words, that the
  mutex protects the map and **not** the pointer it returns - the "no interior pointers across locks"
  shape - and that it is safe only because nothing in the package calls `t.Parallel`.
- **`internal/worker/ingest_log_limiter_test.go`** (new, 14 untagged unit tests) and
  **`internal/worker/handler_ingest_budget_integration_test.go`** (new, 10 integration tests), including
  `TestConnect_TwoConnectionsDoNotShareTheLogBudget`,
  `TestHandleTaskLog_VaryingWireEpochOnOneTaskCannotFloodTheLog` (the discriminating one),
  `TestIngestLogLimiter_AConstantKeyKeepsReportingAcrossALongConnection` (the seven-day test), and
  `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` (asserts the whole captured log is empty, so
  any future wording on that arm reddens it).
- **`internal/worker/handler_auth_test.go`** gained the two-leg hostname test (injection, then volume)
  driven through the real `autoEnrollAndRegister`.
- **Deleted:** `taskLogErrs`, `taskLogErrLimiter`, `taskLogErrLimiterMax`,
  `ResetTaskLogErrLimiterForTest`. Net removal of global mutable state, a process-global mutex on the
  recv path, and one test hook.
- **Zero SQL, zero migration, zero generated file, zero proto, zero files under `web/`.** No new query,
  goroutine, queue or lock on the recv path; `allow`'s hot path is one map lookup plus one integer
  compare.
- **`TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` kept every assertion byte-identical.**
  Only the wrapper call changed and the deleted reset hook was removed. The standing rule held: an
  assertion needing adjustment would have been the finding.

## Key Decisions

- **The bucket is the bound; the key shape is diagnostics.** Recorded in the doc comment because the
  next reader's instinct will be that the composite key is the security control, and acting on that
  instinct (by deleting the bucket, or by "simplifying" the key) re-opens the original bug invisibly.
- **A time-based dedupe window, not a presence flag.** 5m, argued against the bucket from both sides so
  the number can be re-derived from what is written rather than re-guessed.
- **The two bad-id kinds are separate**, reversing the spec's D2 decision to share one key. Decided on a
  measurement (0 log-path lines after one forged status message), not on a preference.
- **The `GetTask` `ErrNoRows` line deleted rather than budgeted.** Its only legitimate cause is a
  cascaded job delete, which is not actionable; its dominant cause is forged traffic. This makes
  `handleTaskStatus` internally consistent - every `ErrNoRows` silent, every real error budgeted.
- **`%q` and `clipID` on the error, not just on the id.** The non-obvious half, and the plan's own
  comment was wrong about it.
- **No env knob**, stated in capitals in the constant block. An operator raising the budget re-opens the
  vector this type exists to close.
- **The `int32(chunk.Epoch)` narrowing was again deliberately not fixed.** The limiter key uses the full
  int64 (free); the fence argument is untouched. Third slice in a row to edit that neighbourhood and
  step around it, which the item now records.
- **One item, not two.** The counter item (`idea-2026-08-14-tasklog-fence-rejection-is-unobservable`)
  ships separately, against ROADMAP's standing pairing instruction. The two live on **complementary arms
  of one `if`** and no input can execute both; the counter also carries an unresolved read-surface
  dependency. What this slice owed it was delivered: the `ErrNoRows` arm is now a named, commented,
  side-effect-free branch, so the counter is a one-line insertion with no re-litigation.

## Findings Triage

- **1 finding against the item's diagnosis** (the epoch is the map value, so the named defect is
  neither sufficient nor the real resource concern) and **1 against its prescribed remedy** (per-worker
  keying with a cap closes nothing). Second consecutive iteration where the remedy was the wrong half.
- **1 finding against the item's acceptance criteria** - the bounded-`GetTask` criterion is
  unachievable under the item's own constraints. Struck, with the reason recorded on the item.
- **6 findings against the spec, by the planner**, including its headline mutation and its test seam.
- **5 findings against the plan, by the engineer**, including one required test that could never have
  gone green and three mis-staged mutations.
- **4 findings by the lenses**, two of them convergent on `pgtype.UUID.Scan`'s byte permissiveness, one
  a measured zero-coverage result on `logKey.kind`, one a measured seven-day permanent-suppression
  result.
- **2 findings against the conductor's own remediation brief**, both caught by the engineer: one
  unsatisfiable assertion, one over-scoped regression claim.
- **1 cross-lens numeric disagreement**, resolved by a third measurement rather than by choosing.
- **0 findings against the shipped behaviour of the limiter after remediation.** Everything above is
  about artifacts and about the code as first written.

## Deferred Findings

**Filed this pass (proposals for human accept - the conductor commits, the human accepts):**

1. `bug-2026-08-15-grpc-connection-admission-is-unbounded` (**bug/medium**) - `MaxConcurrentStreams`
   unset so grpc-go's `MaxUint32` default applies, no `keepalive.EnforcementPolicy`, no
   `MaxConnectionAge`, no interceptor. This is precisely what makes "per connection" a soft bound, and
   it carries the adjacent hazard a lens raised: with auto-enroll on, a connect storm creates unbounded
   `workers` rows via `UpsertWorkerByHostname` with varying hostnames. **Not scope creep:** this slice's
   whole security claim is stated per connection, and shipping a per-connection bound without recording
   that connections are unbounded would be the "closes the hole" overstatement the previous retro
   corrected.
2. `idea-2026-08-15-ingest-log-suppression-is-uncounted` (**idea/medium**) - budget suppression and
   dedupe collapse are both silent, so a flood is now **invisible rather than noisy**. **Filed as a
   sibling of `idea-2026-08-14-tasklog-fence-rejection-is-unobservable`, not as an amendment to it, and
   the reason is scope integrity:** that item is scoped to the `ErrNoRows` **rejection** arm, its
   acceptance criteria are about a rejection counter, and its whole design discussion is about where a
   rejection counter lives. This is the **complementary arm** - five kinds across three handlers, on the
   error path, counting log lines dropped rather than chunks rejected. Amending it would silently widen
   it from one arm to five kinds and falsify its own Done-When, which is exactly the failure this
   pipeline keeps finding, where an item is wrong about its own scope. The two are cross-linked, they share the read-surface
   dependency (`feature-2026-08-09-server-info-allowlist-endpoint`), and the sibling says they should be
   specced together even though they are two items.
3. `bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget` (**bug/low**) -
   `autoEnrollAndRegister`'s audit line and `finishRegister`'s inventory-replace line sit outside the
   budget, because `lim` is allocated after `authenticateAndRegister` returns. The clean structural
   option a lens named: allocate at the top of `Connect` and thread it into `finishRegister`, bringing
   all three sites under one budget. **Filed as the structural gap only** - the `%q` + `clipID` fix on
   the auto-enroll line shipped in this slice, so the injection half is closed. **Not scope creep:**
   both sites are one line per connection today, so the exposure is item 1's, not this slice's; the gap
   is that the budget's coverage is defined by an allocation point rather than by a rule.
4. `bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped` (**bug/medium**) -
   `internal/cli/workers.go` prints `wk.Hostname` (and `wk.Name`) with `%s` in both `doWorkersList` and
   `doWorkersGet`, so terminal-escape and newline injection survives from the gRPC wire, through the
   database and the JSON API, into an operator's terminal. The server-side half is that `Hostname` is
   validated **nowhere** at registration. **Not scope creep:** this slice fixed the rendering at the one
   log site it owns and thereby established that the value is attacker-controlled and unvalidated; the
   same value reaching a different sink is a different sink's bug, and the CLI is the sink with the
   escape-sequence interpreter attached.
5. `bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones` (**bug/medium**) - found by
   carrying the lenses' `pgtype.UUID.Scan` result one function outward. `reconcileRunningTasks` builds
   `serverSet` from `uuidStr(t.ID)` (canonical, lowercase) and looks up `serverSet[rt.TaskId]` with the
   **raw wire string**. Any non-canonical spelling that still parses - uppercase hex, or the
   non-hyphen-byte forms this slice documented - misses the map, so the task is reported as unknown
   (added to `cancelIDs`), **bypasses the epoch comparison entirely**, and is then **requeued** by the
   second loop because the agent "did not report" it. Silent, at every reconnect, with no log line.
   **Not scope creep:** this slice's fix was to canonicalize at the one site it owned; the identical
   defect class 350 lines up in the same file was out of scope and would go invisible again the moment
   nobody is holding the `Scan` result in their head.

**Amendments applied to existing items:**

- `bug-2026-08-12-tasklog-epoch-int32-truncation` - the engineer already corrected one falsified
  sentence during the slice (the parenthetical claiming `handleTaskLog` "already returns silently on an
  unparseable task id", which became false on 2026-08-15). Checked the rest this pass and applied a
  second, dated amendment: **its "do not add a log line, that would hand an attacker a new flood vector"
  justification now rests on a closed item**, and the flood argument itself is materially weaker now
  that a per-connection budget exists. The recommendation is unchanged and the reason is different -
  drop silently because an out-of-range epoch is indistinguishable from a forgery and carries nothing
  actionable, **not** because a line there is unbounded. Its `[[...]]` link to the limiter item now
  points into `docs/backlog/closed/`. Priority, severity and the one-line fix are unchanged.
- `idea-2026-08-14-tasklog-fence-rejection-is-unobservable` - amended with a dated note recording that
  the arm it is scoped to is now a **named, commented, side-effect-free branch with its own pinning
  test** (`TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll`), so the counter is a one-line
  insertion; that the branch's comment cites this item **by name in source**, so leaving it unfiled
  would strand a source citation; and that the sibling above shares its read-surface dependency. **Scope
  unchanged, deliberately** - see item 2's reasoning.
- `idea-2026-07-01-dead-status-vocabulary` - **checked and NOT amended, deliberately.** This slice
  touched no SQL, added no status vocabulary and edited none of the five allow-lists that item's
  acceptance criteria name. Its 2026-08-14 scope boundary about `AppendTaskLog`'s `'pending'` arm is
  unaffected. Recording the check here so the next pass does not re-derive it.

**Considered and NOT filed, with reasons:**

- **The surviving mutation (capacity clear above the dedupe check).** It is a readability position, not
  a behavioural one, and `allow`'s comment already says no test defends it. Filing "add a test for a
  line ordering nobody depends on" would put a nuisance in the queue against real work. **Non-item,
  deliberately.**
- **The `export_test.go` shim map's interior pointer across a lock.** Real in shape, and the comment on
  it names both the hazard and the trigger ("safe today only because nothing in this package calls
  `t.Parallel`; if that changes, this is the finding, not the missing lock"). A comment sitting on the
  code that would move is a better carrier than a backlog file, and the `-race` gate is already the
  detector. Same handling as the `formatExpiryLabel` deferral two slices back.
- **`pgtype.UUID.Scan`'s permissiveness as an upstream item.** It is documented library behaviour, not a
  defect we can fix, and the actionable form of it is item 5 plus the source comments. Filing an item
  against a dependency's parser would be a request nobody can close.
- **An env knob for the budget.** Actively rejected in source, in capitals. Filing it would contradict a
  shipped decision.
- **ROADMAP's "coordinate with fence-rejection-is-unobservable" pairing rationale**, refuted in the
  spec's section 5 on grounds the roadmap did not have. Not an item - it is a refresh action and the
  refresh is the conductor's. Second consecutive slice to refute a ROADMAP pairing rationale for this
  file, which is itself worth the conductor's attention.

## Known Limitations

- **"Per connection" is a bound only if connections are bounded, and they are not.** Item 1. The
  security lens sized the win fairly and named this residual in the same breath; it belongs here so the
  slice is not read as closing more than it does.
- **A flood is now invisible rather than noisy.** Nothing counts what the limiter dropped, so the
  operator-visible signature of the attack this slice defends against is *fewer* log lines than normal.
  Item 2.
- **Two registration-time log sites are outside the budget**, bounded only by connection admission,
  which is item 1. Item 3.
- **Three of the four orderings in `allow` are pinned; the fourth is not**, and the comment says so.
- **The shim seam's "one Handler equals one connection" is true only in tests.** Every log-count
  assertion that goes through `HandleTaskLog` / `HandleTaskStatus` is evidence about one budget and
  never about isolation between budgets. Only `TestConnect_TwoConnectionsDoNotShareTheLogBudget` speaks
  to the latter, and it is the only test that reddens if the limiter is moved onto `Handler`.
- **Most of this slice's tests are integration-tagged and invisible to `make test`.** Ten of the 24 new
  tests need Docker. The 14 in `ingest_log_limiter_test.go` are untagged, which is deliberate: the
  limiter's own contract needs no database and should redden on the cheap gate.
- **The suite figures were reported by the implementing lane and are not re-measured here** (unit
  487 -> 491 top-level, worker integration 75 -> 100, `-race` green). The previous slice carried a count
  that did not reconcile against the tree; the new-test names in this retro were enumerated by grep, but
  the totals were not.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **thirteenth iteration**, and
  the second not-clean one in a row after the first clean result.
- **A backlog proposal is not a contract** - thirteen for thirteen.
- **A materially accurate item can still prescribe a wrong fix** - second instance in two iterations,
  and this one is sharper: the *diagnosis* was also wrong about which resource was unbounded.
- **Plan-supplied tests and plan-supplied mutations are untrusted** - honored, and it paid three times:
  a test that could never have gone green, and three mis-staged mutations.
- **Wrong prose about correct code is the dominant defect class** - **eleventh consecutive iteration**,
  arriving this time as its own inverse (right prose about wrong code) and reaching a remediation brief
  for the second slice running.
- **A recovery bound must be time-based** - rediscovered inside a comment block that states the rule
  correctly. The strongest instance this project has produced.
- **Prefer a symbol name to a line range in any cross-file citation** - honored; every citation in this
  retro and in the filed items names a symbol.
- **Mutation testing needs an isolated tree** - honored; the lens that rewrote the limiter to hold the
  epoch in the value did so out of tree.
- **Backlog housekeeping is required scope** - the close is outstanding and named below.

New from this iteration:

- **Stating a principle in a comment is not the same as checking the code against it.** A comment that
  names a hazard and rejects it is the most convincing artifact in a file and the least likely to be
  re-derived. When you write "the alternative would be X, which is bad", immediately ask which of your
  own branches is X. **Candidate for durable memory.**
- **An item that names a mechanism has usually named the one it saw, not the one that binds.** Open the
  data structure and identify the key before accepting "cap it per X". A cap on a map the attacker
  cannot grow is not a cap.
- **A test seam that keeps call sites byte-identical can destroy the behavioural RED.** If the new test
  must be written against a symbol that does not exist at HEAD, the "RED" is a compile error and the
  test that goes green is not the test that was red. This is the third place the staging rule belongs,
  after task ordering and mutation batteries.
- **An isolation test between two separately-constructed structs is vacuous.** It asserts a property of
  every struct. Isolation has to be driven through the **allocation site** the production code uses, or
  it proves nothing about the allocation site.
- **A remediation brief is an artifact and gets the same treatment as a spec or a plan.** Two defects in
  one brief this pass, one of them an assertion that correct code cannot satisfy.
- **When two careful measurements disagree, they are usually measuring different things.** Get a third
  measurement and then **label both numbers** in the artifact, rather than picking one and leaving the
  ambiguity that produced the disagreement.
- **Carry a library finding outward one call at a time.** `pgtype.UUID.Scan` does not constrain bytes
  was found at the log site and is true at every site that keys, compares or renders a wire UUID string.
  One of those was 350 lines away in the same file.
- **Say what a security fix does not buy, in the same sentence that says what it does.** "Four to five
  orders of magnitude on lines-per-attacker-CPU, and per connection is only a bound if connections are
  bounded" is one honest claim; either half alone is misleading.

## Files Most Touched

- `internal/worker/ingest_log_limiter.go` - the artifact that outlives the slice. Read the type comment
  (dedupe versus bound, and what the composite key does **not** buy), `ingestLogDedupeWindow`'s comment
  (the seven-day measurement and why the suppression must be time-bounded), `logKey`'s comment (the
  `Scan` finding and the 2^32 consequence), and `allow`'s comment (which three orderings are pinned and
  which one is not).
- `internal/worker/handler.go` - `Connect`'s allocation comment (why the budget is a stack local and not
  a `Handler` field), `handleTaskLog`'s two arms (the silent `ErrNoRows` arm citing the counter item by
  name, and the canonical-id block), the two bad-id sites (`%q` + `clipID` on **id and error**, with the
  200060 / 205544 pair labelled), and `autoEnrollAndRegister`'s audit line.
- `internal/worker/export_test.go` - `shimLimiterFor` and its two-directional honesty about what the
  shim is and is not evidence for.
- `internal/worker/handler_ingest_budget_integration_test.go` -
  `TestConnect_TwoConnectionsDoNotShareTheLogBudget` (the only test that pins the allocation site) and
  `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` (asserts the whole captured log is empty).
- `internal/worker/handler_auth_test.go` - the two-leg hostname test, and the assertion the conductor's
  brief asked for wrongly: "the forged text may still appear INSIDE the `%q`-quoted hostname; what must
  not exist is a second physical line".
- `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md` - section 2.3 (why the flood needs
  neither a fresh task id nor the overflow reset) and section 5 (why this is one slice and not two) are
  the parts worth reading again. Sections 6.4 and 6.6 carry dated in-place corrections where review
  overturned the spec; leaving those visible rather than editing them away is the right handling.
- `docs/superpowers/plans/2026-08-15-tasklog-err-limiter-keying.md` - the verification section refuting
  six spec claims, and the test-seam argument, which is the most reusable paragraph in the slice.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No `git log`,
  no `git diff`, no test run. Every claim below that could be checked by reading was checked against the
  worktree.
- **Verified by reading:** `internal/worker/ingest_log_limiter.go` in full; `Connect` (the allocation
  site and the three threaded call sites), `handleTaskStatus`'s two pre-gate sites, `handleTaskLog`'s
  two arms and its canonical-id block, `handleInventoryUpdate`, `autoEnrollAndRegister`'s audit line and
  `finishRegister`'s inventory line (confirming both sit **after** the point `lim` is allocated and
  therefore outside the budget), `reconcileRunningTasks` (confirming the wire-versus-canonical map
  comparison behind filed item 5), and `uuidStr`; `internal/worker/export_test.go` in full, confirming
  the per-`Handler` shim and the absence of `ResetTaskLogErrLimiterForTest`; the 24 new test names by
  grep across `ingest_log_limiter_test.go` and `handler_ingest_budget_integration_test.go`; the hostname
  test's three assertions in `handler_auth_test.go`; the 200060 / 205544 comment; `cmd/relay-server/main.go`'s
  `grpc.NewServer` call, confirming `KeepaliveParams` is the only option passed; `internal/cli/workers.go`'s
  two `%s` hostname renderings; the full spec; and the full text of every backlog item filed or amended,
  plus `bug-2026-08-12-auto-enroll-hostname-takeover` and `idea-2026-07-01-dead-status-vocabulary` for
  overlap.
- **Reported by the implementing and verifying lanes, not re-run here:** every mutation result (the
  `k.kind = 0` zero-coverage sweep, the epoch-in-the-value rewrite, the four ordering mutations of which
  two then three reddened, the shared-bad-id-key measurement, the seven-simulated-day `allowed = 1`
  result); the 200060 / 205544 byte measurements; all suite counts and the `-race` run; `go build` and
  `go vet -tags integration`.
- **Not verified:** all test results, the exact commit count and diff stat, and the change set as `git`
  sees it. Each is attributed above.
- **The five items filed by this pass are in `docs/backlog/` as proposals**; the human gives final
  accept. The two amendments append dated sections, change no scope, and touch only `updated:` in
  frontmatter. **The close of `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` is outstanding and
  belongs to the conductor** (`/backlog close`, never a hand-edited `status:`), as do the exact-file-set
  check, the final gates, all commits, and a ROADMAP refresh that drops the refuted pairing rationale.
