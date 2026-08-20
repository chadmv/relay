---
date: 2026-08-20
topic: reconcile-canonical-task-ids
slice: 2026-08-20-reconcile-canonical-task-ids
branch: claude/pr-merge-session-eb02e4
range: origin/main..HEAD (backend only, Go only, zero SQL, zero proto, zero files under web/, green, not yet merged)
closes: bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones
---

# Session Retro: 2026-08-20 - two mutations killed is not evidence the third will be

**TL;DR:** `reconcileRunningTasks` (`internal/worker/handler.go`) now canonicalizes the
agent-reported task id - `pgtype.UUID.Scan` then `uuidStr` - at the top of its reported loop and uses
that form for **both** map operations, so a parseable-but-non-canonical spelling matches `serverSet`
and the epoch comparison is genuinely reached instead of being short-circuited by `!ok`. Before, a
live, correctly-reported task was cancelled **and** requeued, silently, on every reconnect of any
agent that spells ids differently. The behavioural diff is eight lines. The comment block around it
is forty-five. Unit 491 top-level pass (unchanged - see below), worker integration 100 -> 105.

**The finding of the iteration is not about UUIDs. It is that a mutation proof is only as strong as
the position of the poisoned input.** The engineer proved a lock-in test discriminating with two
mutations, both genuinely killed. A Phase 4 lens ran a third - `continue` -> `break` - and it
**survived**, for a reason that had nothing to do with the test's assertions and everything to do
with the order of two entries in a slice.

## The headline: a bad input placed last cannot detect an early-exit mutation

Task 3 of the plan added `TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing`,
which pins a decision rather than a fix - Decision 2 deliberately preserved the pre-fix behaviour, so
the test is **green at HEAD by design**. The plan knew that made a RED impossible and did the right
thing: it demanded a mutation proof instead, and named two.

- **Mutation A** - replace the parse-failure branch's `cancelIDs = append(...)` + `continue` with a
  bare `continue`. Killed: the cancel list came back `nil`.
- **Mutation B** - add a `log.Printf` to that branch. Killed: the whole-log-is-empty assertion
  reddened.

Both are real. Both leave a test behind. By every rule this project has written down, that battery
was complete.

The Phase 4 correctness lens ran a third, and it is the one a real editor would most plausibly
write:

```go
if err := tID.Scan(rt.TaskId); err != nil {
    cancelIDs = append(cancelIDs, rt.TaskId)
    break            // was: continue
}
```

Result: `ok relay/internal/worker 7.501s`. **Nothing reddened.**

The reason is pure ordering. The test reported two running tasks and the garbage id was **second**:

```go
{TaskId: matchCanonical, Epoch: int64(f.matchEpoch)},
{TaskId: garbage, Epoch: 1},
```

So the loop processed the good task first, hit the parse failure on the last element, and `break` and
`continue` are indistinguishable at the end of a range. Every assertion about the good task had
already been satisfied before the mutated statement executed.

`break` is not an exotic mutation. It is the single most plausible wrong edit in that branch - the
kind of thing an editor writes reaching for "stop dealing with this bad entry" - and its effect is
catastrophic and silent: **one malformed id converts reconcile into "requeue every task this agent is
running"**, because every entry after the bad one is dropped from `agentSet` and the requeue loop
then reads them all as unreported. That is a strictly worse version of the exact failure mode the
headline fix exists to prevent, reachable by any client that sends one bad id in a report of many.

The fix was to move the garbage id to the **front**, with two parseable entries after it. The test
now dies on `break`.

> **The adversarial input belongs at the FRONT of any sequence a loop walks.** A poisoned element in
> the last position can only detect mutations to what happens *at* it; it is structurally blind to
> every mutation that changes what happens *after* it - `break`, an early `return`, a `goto`, a
> swallowed error that abandons the rest of the batch. Position is part of the test input, and it is
> the part nobody reviews.

Two corollaries, both new to this project's record:

- **Two mutations passing is not evidence the third will.** A mutation battery is a sample, not a
  proof, and the plan-supplied battery is a sample somebody drew before the code existed. This
  project already knows that plan-supplied tests and plan-supplied mutations are untrusted; the new
  half is that a battery can be *individually correct and collectively blind*, with no signal at all
  that a whole class was never reachable.
- **A green mutation is a finding about the test's inputs before it is a finding about its
  assertions.** The instinct on a surviving mutation is to add an assertion. Here no assertion was
  missing - the test already asserted exactly the right thing about exactly the right rows. What was
  missing was an execution path from the mutated statement to any assertion at all.

It generalizes well past UUIDs: any test that feeds a handler, parser, batch importer or message loop
a list containing one bad element should put the bad element first, and should have at least two good
elements behind it so both "skips the bad one" and "keeps going" are observable.

## The item's remedy was right and both of its open questions resolved against its own lead

This project has a running streak of backlog items whose diagnosis was accurate and whose prescribed
remedy was not (`reference_accurate_item_wrong_remedy`, first recorded 2026-08-14 and recurring
sharper on 2026-08-15). **This iteration is a different animal, and it should not be filed under that
lesson.**

The item's diagnosis was accurate. Its prescribed remedy - parse, re-encode, use the canonical form
for both map operations - was also accurate, and shipped essentially verbatim. What went wrong was in
neither: it was in the two questions the item asked the implementer to *settle*, both of which it
framed with a lead, and both of which resolved **against** that lead.

On what `cancelIDs` should carry, the item wrote:

> Today it echoes the wire string back to the agent, which is arguably friendlier (the agent
> recognizes its own spelling) and arguably wrong (**the coordinator should speak canonically**).

The parenthetical is a good general principle and it is the wrong answer here. `internal/agent/agent.go`
looks each cancelled id up in `a.runners[tid]`, a map keyed at dispatch with the string the agent
itself later reports back. Canonicalizing the echo hands a re-spelling agent - **the exact client
this whole fix exists to serve** - a spelling it has never used. Its lookup misses, `Abandon()` never
runs, and a task the coordinator has decided to cancel keeps running. "Not cancelled at all" is
strictly worse than "cancelled spuriously".

On unparseable ids the item leaned the same way ("Drop or cancel - decide deliberately"), and the
answer was again to keep the existing behaviour: echo verbatim, log nothing, because reconcile runs
inside `finishRegister` before `Connect` allocates the connection's `ingestLogLimiter` and therefore
has no budget to spend.

Both questions resolving to "keep HEAD's behaviour" is what makes this a **zero-wire-change diff**:
for every input, parseable or not, the bytes that land in `RegisterResponse.CancelTaskIds` are the
bytes HEAD put there. The canonicalization lands on the comparison and never on the echo.

> **A question phrased with a lead is a claim, and it gets checked like one.** "Settle X; arguably A,
> arguably B (and B is the principled one)" reads as an invitation to think and functions as an
> instruction. Two of two settled the other way here. The tell is that the lead was a *general*
> principle ("speak canonically") applied without opening the consumer - which is the same failure
> shape as the closed item's own headline lesson, one layer up.

**Is this the same lesson as `reference_accurate_item_wrong_remedy`? No, and forcing it there would
lose both.** That lesson says: verify the diagnosis and the remedy as two separate claims, because
the item that got the bug right has earned trust it did not earn for the fix. This one says: an item
can be right about the bug *and* right about the fix and still steer the implementer wrong through
the framing of a question it explicitly left open. The artifact is different (an open question, not a
prescription), the failure is different (direction, not correctness), and the countermeasure is
different - the remedy lesson says "re-derive the fix", this one says "when an item hands you a
choice, open the consumer before accepting either arm". They are siblings, not duplicates.

The **cheap version of the countermeasure** is what Phase 2 actually did: it read
`internal/agent/agent.go` and found the map. Three minutes of reading a *different package* settled
both questions and turned what the item framed as a wire-visible behaviour change into a no-op.

## The prose this slice wrote was the least-verified part of its own diff

Four of the six Phase 4 findings were errors in prose the slice **itself authored** - the forty-five
line comment block, which is five and a half times the size of the behavioural change, plus one test
assertion message. A fifth was in the plan document's committed sweep table.

That continues the "wrong prose about correct code" streak to a **ninth consecutive iteration**, with
a twist that is new: in every prior instance the wrong prose was *inherited* - a stale comment, an
old spec claim, a scheduling rationale copied forward. Here it was written during the slice, by the
lane that had just spent an hour in the file, and reviewed in the same pass that wrote it.

The specific shapes, because they are more useful than the count:

- **An enumeration read as exhaustive when it was a basis.** The comment said `pgtype.UUID.Scan`
  accepts "three spellings that decode to the same 16 bytes". They are not three spellings; they are
  three independent *axes* that compose, and the third axis alone (four unchecked bytes at indices 8,
  13, 18 and 23) is 2^32 strings per id. The corrected comment says "three families, not three
  strings" and states why the tests cover the axes rather than their combinations - which is the
  sentence that makes the test coverage defensible instead of looking sparse.
- **A dropped reachability qualifier.** The comment described the bug firing "silently, on every
  reconnect" with no statement of *whose* reconnect. The shipped Go agent never triggered it, on any
  reconnect, because `scheduler/dispatch.go` sends `uuidStr(claimed.ID)` and the agent reports that
  same string back. The comment now carries an explicit **SCOPE** paragraph. Without it, the next
  reader takes an interop bug for a live production one and mis-prices everything nearby.
- **An over-claim about what a decision buys.** The echo argument, as first written, implied the
  verbatim echo keeps cancellation working for a re-spelling agent. It keeps it working *on this one
  path*: `api/cancel_signals.go` always sends the DB's canonical rendering, so such an agent already
  misses every runtime `CancelTask`. The comment now says so in the same breath. This is the standing
  "say what a fix does not buy, in the same sentence that says what it does" goal, arriving inside a
  comment rather than inside a spec.
- **A stale count.** The comment cited a number of budgeted ingest sites that went stale when
  `kindBadTaskIDLog` was split out on 2026-08-15. The remediation **deleted the count** rather than
  correcting it, on the grounds that the load-bearing claim is "this site has no budget", not how
  many sites do. That is the right repair: a number in a comment is a maintenance liability that
  reddens nothing, and the correct fix for a stale count is usually to stop asserting a count.
- **An over-broad assertion message**, reported by the verifying lane and not re-derived here.

> **A comment block five times the size of its diff is a third artifact and needs a third review.**
> It was reviewed by the lens that reviews code, in the pass that reviews the diff, and four defects
> got through. Every one of them is the kind a reader would act on.

## Four lenses, and one of them found something the tree still carries

- **Correctness** produced the headline: the `continue` -> `break` mutation and the ordering
  diagnosis behind it. Note *how* it got there - it did not review the two plan-supplied mutations
  and agree with them, it drew a third from the branch's own edit surface. That is the difference
  between checking a battery and sampling the space the battery claims to cover.
- **Security** produced two, and the second is the one that should travel. First: `RequeueTaskByID`
  carries neither an epoch fence nor a `worker_id` predicate - filed, below. Second: a stale-epoch
  report now cancels **without** requeueing, because `agentSet[canonical] = true` runs
  unconditionally on parse success, before and independently of the epoch comparison. The task stays
  `dispatched` with no holder and no recovery path. Behaviour is **unchanged for canonical ids** and
  it needs a lying agent, so it is not a regression and not this slice's bug - see "Carried forward"
  below for where it went.
- **Invariants** found `RequeueTaskByID` independently, from the opposite direction: not "what can a
  caller forge" but "what does this write fence on". Two lenses converging on one statement from
  opposite arguments is the same signal shape this project got on `pgtype.UUID.Scan` (2026-08-15) and
  on parenthesization (2026-08-14). **Third consecutive slice where the convergent finding was the
  most valuable one**, and it is worth stating as a heuristic: when the invariants and security
  briefs land on the same symbol, promote it immediately - the arguments are independent, so the
  agreement carries real information.
- **Integration** ran the whole `./internal/worker/...` package under `-p 1` rather than the two new
  tests, verified the RED at `0fc1efc` independently instead of accepting the engineer's report, and
  took worker integration from 100 to 105.

The fifth finding, in the **plan document's own sweep table**, is the reason the third backlog item
exists: the sweep's first pass concluded that no `parseUUID` caller renders its raw-string error, and
two do. The plan now carries a dated in-place correction, which is the right handling.

**One prose defect is still in the tree and belongs to the conductor.** The plan's Task 2 commit body
reproduces the sweep's *original*, false conclusion ("no caller renders its raw-string error") and
also says "all 23 `parseUUID(r.PathValue("id"))` sites" where the corrected table two hundred lines
above says 22. Counting them in `internal/api` gives **22**. Whether the shipped commit message
carries the false claim is not checkable from this lane (no shell); if it does, it is worth a note in
the PR body rather than a rewrite of history.

## The `/code-review` question, answered with what is actually on the record

The conductor-run `/code-review` at high effort returned **zero findings** on a diff where the
four-lens fan-out then found six real things, four of them in the prose the tool was reading.

The task framing for this retro said that was the second iteration running. **Checking the record
rather than accepting it: it is the second recorded instance, and it is not consecutive.** The first
is the 2026-08-14 cursor-pager slice, whose retro states it plainly - "`/code-review` at high effort
reported zero findings; the four Phase 4 lenses then produced the two most valuable findings in the
slice" - and which promoted "a clean `/code-review` is a lead, not a verdict" to an improvement goal.
The 2026-08-15 slice's retro **records no `/code-review` result at all**, in either direction, so
there is no third data point and no evidence about what happened in between.

**Is two instances a pattern worth acting on? Not yet, and there is a more useful reading available
than "the tool is weak".** Both zero-finding diffs share a shape: the cursor-pager slice was a
behaviour-preserving refactor, and this one was an eight-line change surrounded by forty-five lines of
comment. In both, essentially none of the risk lived in the changed expressions - it lived in what
the changed code no longer proved (the refactor's void gate) and in what the surrounding prose
claimed (this slice's four comment defects). A reviewer tuned to find correctness bugs in changed
logic has, correctly, nothing to say about either.

So the recommendation is **not** to drop the step. It is cheap, it feeds the fan-out as prior
findings, and a *non*-empty result would be genuinely informative. Two changes instead:

1. **Stop treating the result as a signal about whether the fan-out is needed.** It never was, and
   both instances confirm it. This is already the standing goal; it held here.
2. **Record the diff shape alongside the result, every time**, so a third data point can either
   confirm "zero findings correlates with a low-logic-delta diff" or refute it. Right now the record
   has two observations and no covariate, which is why this is an observation and not yet a pattern.
   The 2026-08-15 gap - a retro that records neither - is what makes the count unrecoverable, and
   that is the cheap thing to fix.

## The process shape: Phase 1 collapsed into Phase 2, and it was right

No spec document was written. The backlog item carried its own design - a code sketch, the library
analysis with the pgx source quoted, the acceptance criteria, and an explicit "settle these" list -
so Phase 2 verified those claims against the tree and went straight to a plan. The same collapse was
taken on 2026-08-14 for the cross-generation-401 slice, for the same reason.

**I judge it correct here, and the bar it cleared is worth naming**, because the collapse is
attractive for the wrong reasons (it is faster) and this project's own record shows what happens when
a stage is skipped rather than absorbed:

- **The design questions were still answered in writing, before code.** The plan's Decision 1 and
  Decision 2 sections are a spec by another name: both carry the alternative, the argument, the
  consumer evidence from `internal/agent/`, and the test that pins the choice. Nothing was deferred
  to the engineer's judgment at edit time.
- **The item's claims were verified rather than inherited.** The plan opens with a
  "Verification of the backlog item's claims" section that re-read pgx's `parseUUID` at the pinned
  module version and re-derived both consequences. A skipped Phase 1 is a problem when it means
  nobody checked the item; this was the opposite.
- **The refutations still happened.** Phase 2 refuted the item's lead on both open questions. That is
  exactly the work Phase 1 exists to do, done one stage later.

**The honest cost, stated so the next collapse is priced correctly:** the artifact that normally
carries the "what does this not buy" framing did not exist, and the two things that framing would
have surfaced - the echo's limited reach past `cancel_signals.go`, and the stale-epoch
cancel-without-requeue case - were both found in Phase 4 instead. Neither is expensive to find late.
But it does mean the collapse is safe **when the item carries the design**, not when the item merely
carries a diagnosis, and the test for which one you have is whether the item names its own open
questions. This one did.

## Carried forward: the case the watchdog slice inherits

The security lens's second finding is not a bug in this slice and not a regression, and it must not
be lost between the two.

**A stale-epoch report cancels without requeueing.** `agentSet[canonical] = true` runs
unconditionally on parse success, so an agent that reports a task it genuinely holds at the *wrong*
epoch gets that task into `cancelIDs` (correct) while simultaneously marking it "reported", which
suppresses the requeue. The row stays `dispatched`, with `worker_id` pointing at a worker that has
been told to abandon it, and nothing sweeps it. Behaviour is identical to pre-fix for canonical ids;
it needs a lying agent; and such an agent can already wedge its own tasks by never sending a terminal
status.

**It has been written onto `bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task` as a
dated amendment, not left in this retro.** That item is the very next roadmap entry and it is the
mechanism that would recover this state, so the amendment is where the implementing slice will
actually read it. It also adds an acceptance criterion (seed that exact state and prove the sweeper
recovers it) and one design consequence: **key the sweeper on non-terminal duration, not on
`status = 'running'`**, because this row may never have gone `running` at all.

The amendment carries a second input to the same item: if the watchdog is built with the
requeue-shaped fix, it adds a second, periodic, non-agent-driven caller of the unfenced
`RequeueTaskByID`, which promotes the fence item below from a race hardening to a hard prerequisite.

## What Was Built

- **`internal/worker/handler.go`, `reconcileRunningTasks`** - one `pgtype.UUID.Scan` at the top of
  the reported loop, `canonical := uuidStr(tID)` used for the `agentSet` key **and** the `serverSet`
  lookup. Eight behavioural lines. `pgtype` was already imported; no new import.
- **The parse-failure branch echoes verbatim, continues, and logs nothing** - all three deliberate,
  all three commented, and the "logs nothing" one pinned by a whole-log-is-empty assertion so any
  future wording reddens it.
- **`cancelIDs` still carries the agent's own spelling.** Zero wire-visible change for every input.
  Pinned by the stale-epoch positive control, which asserts the cancelled id comes back in the
  agent's spelling and **not** in the canonical one.
- **`internal/worker/handler_reconcile_canonical_test.go`** (new, `//go:build integration`) -
  `TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings` (three spelling axes x
  {not cancelled, not requeued, epoch intact}, plus both positive controls in the same `Connect`
  call), and `TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing` (the
  decision lock-in, with the garbage id now **first**).
- **The vacuity guard.** `strings.ToUpper` is a no-op on an id containing no `a-f` digit
  (p ~ 1.2e-7), so the test `require.NotEqual`s the canonical and wire spellings before using them. A
  freak id fails loudly instead of passing for the wrong reason.
- **`internal/worker/handler_test.go` untouched**, deliberately. `TestRegisterWorker_ReconcilesRunningTasks`
  is the guard that the canonical case is unchanged and that the same number of rows still reach
  `RequeueTaskByID`; editing it to accommodate the fix would have destroyed that guarantee.
- **Zero SQL, zero migration, zero generated file, zero proto, zero files under `web/`, no
  `make generate`.** Per reported element the change costs one `Scan` and one `Sprintf`.

## Key Decisions

- **Canonicalize the comparison, never the echo.** Decided on what the consumer does with the field
  (`a.runners[tid]`), not on a principle about coordinators speaking canonically. Commented at the
  site and pinned by a test.
- **Unparseable ids stay in `cancelIDs`, verbatim and silent.** Fail-safe direction: an id that names
  no assignment of ours means the agent is running something we do not know about, and telling it to
  stop is the safer of the two. Dropping would be fail-open **and** completely silent, because the
  budget constraint forbids a log line here.
- **No log line at registration, and this is a hard constraint, not a preference.** `reconcileRunningTasks`
  runs before `Connect` allocates the connection's `ingestLogLimiter`, so it has no budget at all;
  `clipID` + `%q` is not a substitute for a missing budget.
- **The comment's stale count was deleted, not corrected.** The claim that matters is "this site has
  no budget".
- **The sweep was recorded in the plan and reproduced in the commit body**, per the item's own
  acceptance criterion - and its `internal/api` row now carries a dated correction rather than a
  quiet edit, so the next reader sees that the first pass was wrong.

## Findings Triage

- **0 findings against the item's diagnosis**, and **0 against its prescribed remedy** - which
  shipped essentially verbatim. Second clean diagnosis in nine iterations; the first clean *remedy*
  in three.
- **2 findings against the item's framing of its own open questions.** Both resolved against the lead
  it gave. New shape; see the section above for why it is not the existing accurate-item lesson.
- **1 finding against the plan's mutation battery**, by a lens - not that a mutation was wrong, but
  that the battery was **collectively blind to an entire class** because of input ordering. This is
  the finding of the iteration.
- **4 findings against prose this slice itself wrote** (the comment block, plus one assertion
  message).
- **1 finding against the plan's committed sweep table** - the `parseUUID` conclusion, false, and
  corrected in place. Its residue is still in the plan's commit body and in a count that disagrees
  with the corrected table by one.
- **2 pre-existing findings carried outward** - `RequeueTaskByID`'s missing fence (found twice,
  independently) and the stale-epoch cancel-without-requeue case. Neither is a regression; the slice
  strictly narrows what reaches the first.
- **0 findings against the shipped eight lines.** Everything above is about artifacts, about test
  inputs, and about code this slice did not write.

## Recommended Backlog Items

**Filed this pass (proposals for human accept - the conductor commits, the human accepts):**

1. `bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence` (**bug/medium**) - the statement's
   only predicates are the task id and a status allow-list, while its one caller has both the epoch
   and the authenticated worker id in hand. Concrete duplicate-execution scenario in the item, plus a
   second window that needs no concurrent `Connect` (a grace expiry landing inside one reconcile's
   read-write gap). **Not scope creep:** this slice narrowed what reaches the statement and thereby
   established what still can. **Filed at medium, not high, and the item states the promotion
   condition explicitly** - if the watchdog item is specced with the requeue shape, it becomes a
   prerequisite. All callers were checked before proposing the signature change: one production
   caller, two test call sites in `internal/store`.
2. `idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes` (**idea/low**) - `pgtype.UUID.Bytes`
   is a comparable `[16]byte`, so keying both maps on it deletes the bug class instead of
   re-encoding around it. Carries the one trap: `uuidStr` returns `""` for an invalid UUID and the
   current code therefore fails closed **by accident**, where raw-byte keying would promote a zero
   UUID to `Valid: true`. Unreachable today; the replacement must fail closed on purpose.
3. `idea-2026-08-20-workspaces-handlers-reflect-parseuuid-error-text` (**idea/low**) - two of 28
   `parseUUID` call sites render the parser's error to the client where 23 write a fixed message.
   **Deliberately not filed as security**, and the item says why in its own body: `writeJSON` uses
   `json.NewEncoder` so the segment is JSON-escaped, the input is bounded by `MaxHeaderBytes`, and
   nothing is keyed or compared on the raw string. It also names the collision to settle -
   `bug-2026-08-09-create-reservation-500-on-client-error` has "no raw error text in the body" as an
   acceptance criterion, and the two must agree on one rule.

**Amendment applied to an existing item (no scope change, `updated:` added):**

- `bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task` gains a dated amendment carrying
  both inputs above: the stale-epoch cancel-without-requeue state the sweeper should recover (with a
  new acceptance criterion and the "key on non-terminal duration, not `status = 'running'`" design
  consequence), and the `RequeueTaskByID` dependency if the requeue shape is chosen. **Preferred to
  burying it in this retro** because that item is the next roadmap entry and is the artifact its
  implementing slice will read.

**Considered and NOT filed, with reasons:**

- **The `internal/api` `?job_id=` asymmetry** (`events.go`). Same shape as the fixed bug - an
  uppercase `job_id` yields an open, permanently empty SSE stream - but it is **deliberate,
  commented in place, and a client-contract change rather than a bug fix**. Recorded in the plan's
  sweep and in item 3's Notes so it is not opened a third time. **Non-item, deliberately.**
- **`pgtype.UUID.Scan`'s permissiveness as an upstream item.** Documented library behaviour, not a
  defect we can fix. Already dismissed on the same grounds on 2026-08-15; the actionable form is the
  source comments and item 2.
- **A CLAUDE.md amendment.** The epoch-fence bullet did not move; this slice fixes a comparison, not
  a fence. Recorded as a decision because the family of slices before it amended that bullet three
  times, so silence should read as deliberate rather than forgotten. **If item 1 is scheduled, that
  is the slice that touches the bullet** - specifically the sentence about the `worker_id` predicate
  needing to accompany the epoch, which currently names three statements and would name four.
- **The plan's residual count error (23 versus 22).** Flagged to the conductor for the PR body rather
  than filed; an item asking someone to fix a number in a plan document would be stale before it was
  read. Same handling as the stray-`</content>` sweep two slices back.

## Known Limitations

- **The fix is invisible to `make test`.** Both new tests are `//go:build integration` and need
  Docker, consistent with every other handler test in the package. The unit gate would stay green if
  both were broken.
- **`RequeueTaskByID` is still unfenced.** This slice narrowed the inputs that reach it and changed
  nothing about the statement. Item 1.
- **A stale-epoch report still cancels without requeueing**, leaving a `dispatched` row with no
  holder. Unchanged from pre-fix behaviour; recovery belongs to the watchdog item.
- **A re-spelling agent is still not fully served.** The echo makes the register-response cancel path
  work for it, but `api/cancel_signals.go` sends the DB's canonical rendering, so every *runtime*
  `CancelTask` still misses that agent's runner map. The comment says so; nobody has decided whether
  full interop for non-canonical agents is a goal.
- **Three spelling axes are tested; their combinations are not.** Deliberate, and the comment states
  the argument: re-encoding is total across all of them by construction, so the axes are the basis
  and the combinations add nothing. If anybody weakens the canonicalization, that argument stops
  holding and the coverage becomes thin.
- **The suite figures needed disambiguating, and the ambiguity was real.** The implementing lane
  reported "unit 583 pass", which does not reconcile with the previous retro's 487 -> 491. The
  conductor re-measured both conventions on this tree: **491 top-level, 583 including subtests.**
  So 583 and 491 are the same suite counted two ways, and the previous retro's convention is
  top-level.

  Counted that way, **this slice added zero unit tests: 491 -> 491.** Both new tests are
  `//go:build integration`, which is correct (the whole `Handler` needs a real Postgres, and every
  other handler test in the package is gated the same way) but is worth stating plainly rather than
  letting a 583 imply growth that did not happen in `make test`. The real movement is **worker
  integration 100 -> 105**, and that is the only number this slice moved.

  **Say which one you are counting before copying the number forward** - and prefer top-level, since
  that is what the record already uses.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **fourteenth iteration**,
  and the second clean diagnosis. The clean result is a result.
- **A backlog proposal is not a contract** - fourteen for fourteen, and this time the deviation was
  in the item's *questions* rather than its claims.
- **Plan-supplied tests and plan-supplied mutations are untrusted** - honored, and it paid in a new
  way: the mutations were individually right and collectively blind.
- **A mutation proof must leave a test behind** - honored on all three mutations, including the third
  one, which left the reordered input behind rather than a note.
- **Wrong prose about correct code is the dominant defect class** - **ninth consecutive iteration**,
  and the first where the prose was authored by the slice itself rather than inherited.
- **Say what a fix does not buy in the same sentence that says what it does** - honored after
  correction; the echo comment now names the `cancel_signals.go` limit inline.
- **Prefer a symbol name to a line range in any cross-file citation** - honored; every citation in
  this retro and in the three filed items names a symbol.
- **A clean `/code-review` is a lead, not a verdict** - honored, second recorded instance, and
  refined above into "record the diff shape with the result".
- **Backlog housekeeping is required scope** - the item was closed to `docs/backlog/closed/` with a
  full `## Resolution`, before this retro was written.

New from this iteration:

- **A mutation proof is only as strong as the position of the poisoned input.** An adversarial
  element in the last position of a sequence cannot detect any mutation that terminates the loop
  early. Put it first, with at least two good elements behind it. **Candidate for durable memory.**
- **Two mutations passing is not evidence the third will.** A battery is a sample of the mutation
  space, and a battery drawn before the code existed samples the space the *plan author* imagined.
  When a mutation survives, ask first whether it was ever executed against an assertion.
- **An item's open question, framed with a lead, is a claim and gets checked like one.** "Settle X;
  arguably A, arguably B" plus a nudge toward B functions as an instruction. Open the consumer before
  accepting either arm. This is a **sibling of, not the same as**, "an accurate item can prescribe a
  wrong remedy".
- **A comment block several times the size of its diff is a third artifact needing its own review
  pass.** Four defects got through a review that was reading the same screen.
- **The correct fix for a stale count in a comment is usually to delete the count.** A number that
  reddens nothing is a maintenance liability; assert the property instead.
- **When the invariants and security lenses land on the same symbol, promote it immediately.** Third
  consecutive slice where the convergent finding was the most valuable one, and the arguments are
  independent, so the agreement carries information that neither lens alone does.
- **Collapse Phase 1 into Phase 2 only when the item names its own open questions.** An item carrying
  a diagnosis is not an item carrying a design; the test is whether it knows what it left undecided.

## Files Most Touched

- `internal/worker/handler.go` - `reconcileRunningTasks`. Read the three paragraphs in order: the
  three-families argument and why the tests cover axes not combinations; the **SCOPE** paragraph
  (which agent actually triggers this) - it is the sentence that prices the whole bug; and the echo
  argument at the bottom of the loop, including what the echo does **not** buy.
- `internal/worker/handler_reconcile_canonical_test.go` - the lock-in test's report ordering is
  load-bearing and now says so. Do not reorder those two entries.
- `docs/backlog/closed/bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones.md` -
  its `## Resolution` records both settled questions and the corrected sweep result, and is the
  cheapest way for a future reader to get the whole story.
- `docs/superpowers/plans/2026-08-20-reconcile-canonical-task-ids.md` - Decisions 1 and 2 are the
  reusable part (a design argument settled by opening the consumer). The sweep tables are worth
  reading for the dated `internal/api` correction, and worth distrusting for the two residual count
  errors named above.
- `docs/backlog/bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task.md` - the 2026-08-20
  amendment at the end is this slice's main outbound artifact.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No
  `git log`, no `git diff`, no test run. Every claim below that could be checked by reading was
  checked against the worktree.
- **Verified by reading:** `reconcileRunningTasks` in full, including the shipped comment block and
  both loops, and `uuidStr`; `finishRegister` (confirming nothing serializes two `Connect`s for one
  worker before reconcile, and that `grace.Cancel` precedes the reconcile call); `requeueWorkerTasks`
  and `RequeueWorkerTasksIfEpoch`'s call site, which is the fenced sibling the filed item contrasts
  against; `RequeueTaskByID`'s full statement and doc comment in `internal/store/query/tasks.sql`;
  every `RequeueTaskByID` reference in the tree by grep, establishing one production caller and two
  test call sites; all 28 non-test `parseUUID` call sites in `internal/api` with three lines of
  context each, confirming 2 render `err.Error()`, 1 reflects the raw input, 2 discard, and 23 write
  a fixed message - and that **22**, not 23, use the `parseUUID(r.PathValue("id"))` form;
  `parseUUID`'s own wrapper (`fmt.Errorf("invalid UUID %q: %w", s, err)`), which is why the reflected
  body carries the segment twice; `workspaces.go`'s two handlers in full; the closed item including
  its `## Resolution`; the plan in full, including the `internal/api` sweep row's dated Phase 4
  correction and the Task 2 commit body that still contradicts it; the two prior retros for structure
  and for the `/code-review` claim, and the cursor-pager retro's three passages on it; and the full
  text of the amended watchdog item plus every open backlog item matched by duplicate-check greps on
  `requeue`, `parseUUID`, `uuidStr`, `[16]byte` and `epoch fence`.
- **Duplicate check result: none of the three filed items duplicates an open item.** The nearest
  neighbour is the watchdog item, which *mentions* `RequeueTaskByID` as one candidate fix shape but
  is about a coordinator-side sweeper; it is cross-linked in both directions rather than merged.
- **Reported by the implementing and verifying lanes, not re-run here:** the `continue` -> `break`
  survival (`ok relay/internal/worker 7.501s`) and both killed mutations; the RED at `0fc1efc`; all
  suite counts; `go build` and `go vet -tags integration`.
- **Not verified:** all test results, the commit set and diff stat, the change set as `git` sees it,
  and whether the shipped commit message carries the plan's uncorrected sweep sentence. Each is
  attributed above.
- **The three items filed by this pass are in `docs/backlog/` as proposals**; the human gives final
  accept. The amendment appends a dated section, changes no scope, and adds only `updated:` to
  frontmatter plus one cross-link and one acceptance criterion. **The close of the source item is
  already done**; the remaining conductor work is the exact-file-set check, the final gates, all
  commits, and a ROADMAP refresh.
