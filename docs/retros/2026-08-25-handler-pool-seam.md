---
date: 2026-08-25
topic: handler-pool-seam
slice: idea-2026-08-24-handler-pool-has-no-seam (filed by the previous slice; led ROADMAP "Now")
branch: claude/pr-merging-session-868949
range: origin/main..HEAD (backend only; Go only; one production field's type; zero SQL, zero migration, zero proto, zero generated file, zero files under web/)
pr: handler-pool-seam - reference this work by date and slug, never by a predicted number
closes: idea-2026-08-24-handler-pool-has-no-seam
amends: bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory, idea-2026-08-23-integration-only-guards-ci-never-runs
filed-this-slice: none
---

# Session Retro: 2026-08-25 - one field's type bought the default lane its first successful worker registration, and the prose defending it failed review three times while the code passed every lens once

**TL;DR:** `Handler.pool` is now `txBeginner`, a one-method interface (`BeginTx`) that
`*pgxpool.Pool` satisfies, so `cmd/relay-server`'s wiring is unchanged source text. That one change
gave `internal/worker` its first route to a **successful** `finishRegister` without Postgres, in the
lane CI actually runs. Six new default-lane test functions landed; `handler_handoff_guard_test.go`
shed five of its nineteen clauses, each deletion re-proved by mutation after the fact. The mutant
that motivated the whole item - deleting `handedOff = true`, which on 2026-08-24 left all 21
packages green - now reddens the default lane four ways.

This slice exists because the previous one was expensive. It was, and the ledger is more
interesting than "the seam was cheaper".

---

## 1. Was the debt worth paying? Yes, and the bill was smaller than 2026-08-24 thought

The finishregister-strand retro closed with a number offered as the argument for this slice: **five
evasions of one 669-line parser guard.** The implication a reader takes from that sentence is that
the guard was largely wasted and a seam would have avoided it. Having now done the clause-by-clause
work, that implication is wrong, and the correction is the useful finding.

**Every one of the five evasions landed on a clause this slice KEPT.**

| Evasion | Closed by | Disposition this slice |
|---|---|---|
| `if h.Metrics != nil { handedOff = true }` | `directBodyStmt` (G14) | **Kept.** Still the only thing that kills it - measured again as M2. |
| `if h.pool != nil { return }` in the closure | `isCallTo` / body-shape switch (G4) | **Kept.** Now *also* killed behaviourally, because both fixtures carry a fake pool (M13). |
| Round 4: nesting `registry.Register` in a compound statement | identity-not-containment at the anchor (G16) | **Kept.** Source position only. |
| Round 5: `(handedOff) = false` | `ast.Unparen` in `otherWrites` | **Kept.** |
| (the third shape, caught pre-ship) | body-shape switch | **Kept.** |

The five clauses deleted here - G3, G6, G7, G12, G15, about 70 lines - were **never the ones anyone
evaded**. G6 and G7 were bookkeeping for G15, G3 was redundant with a sibling clause, and only G12
and G15 were genuine structure-replaced-by-behaviour. So the expensive part of that slice, the
hardening rounds, bought exactly the residue that survives today and is still load-bearing. What
the seam retired was the guard's cheap half.

**Judgement: the trade was worth it, and the guard was a correct purchase that was mis-scoped rather
than over-bought.** The avoidable cost was not the 669 lines; it was that the guard was written
without declaring which of its clauses were *substitutes for a missing test* (expected to die when a
seam lands) and which were *permanent* (source position, shape, write set). This slice had to make
that classification retroactively, nineteen clauses deep, in a spec table. A guard bought as a
substitute should carry that table on the day it is written.

> **When a slice buys a structural guard because the runnable lane cannot witness a property, label
> each clause SUBSTITUTE or PERMANENT at the time of writing.** The substitutes are the slice's debt
> and should be named in the backlog item that files the seam; the permanent ones are the guard's
> real product and are not debt at all. Without the labels, the next slice cannot tell the two apart
> and the whole guard reads as a liability it is not.

One thing genuinely was avoidable and should be said plainly: the seam is **one field's type and two
constructor signatures**. That was available on 2026-08-24. It was declined on scope grounds, which
was correct at the slice level, and the declination was recorded with the price of the guard but not
with a price on the alternative. The rule "say declined, and here is the price" was honored for one
side of the comparison only.

---

## 2. The spec and the plan each found a gap that would have broken the slice

Twenty-second consecutive iteration in which planning-phase verification caught something material
before a line was written, and this time both phases contributed independently. Neither finding was
in the backlog item; each alone would have failed the slice.

**The spec (F1): the pool seam alone does not reach a successful `finishRegister`.**
`reconcileRunningTasks` sits four lines above `applyInventory` and calls `GetActiveTasksForWorker`,
which sqlc emits as a `:many` - `rows, err := q.db.Query(...)` then `defer rows.Close()` on the very
next line. The existing fake returned `(nil, d.queryErr)`, so a fixture with no injected error
returned `(nil, nil)` and the generated code called `Close()` on a nil `pgx.Rows` interface. A
repo-wide search for `Next() bool` returned no matches, so no empty-rows fake existed to reuse. A
slice scoped to "narrow the pool and stop" would have panicked **one frame short of the defect it
was fixing**, in generated code, which is precisely where a misdiagnosis is most likely. The item's
own acceptance criterion would not have been met.

**The plan: the `pgxpool` import deletion the spec never mentions.** Narrowing the field removes the
package's last use of `github.com/jackc/pgx/v5/pgxpool` in `handler.go`, and Go makes an unused
import a compile error. The spec's step list, followed literally, does not build.

These are different *kinds* of gap, and that is the argument for the phased pipeline rather than a
merged design-and-plan document. The spec's gap is about **what the path actually touches** and was
found by walking the call sequence and reading generated code. The plan's gap is about **what the
compiler requires** and was found by writing the edit out concretely. A single phase written by a
single lane tends to find one of those and not the other, because the two need different reading
postures. The plan also refused one spec instruction outright - the spec asked for an observable
(`sender.connEpoch` read directly) that a `Connect`-driven test structurally cannot see, and the
plan substituted the teardown fence argument rather than adding a registry getter to make the
spec's wording literally true. That is the correct direction: **do not grow production surface to
satisfy a spec sentence.**

---

## 3. The headline: prose failed review three times, and the code passed every lens once

Count the rounds against the same body of text:

- **Planned Task 8** corrected **6** claims. These were known in advance: the spec enumerated them
  as acceptance criteria, because the slice itself falsified them.
- **Verify round 1** found **9 more**, one of them inside a guard's `t.Fatalf` failure message -
  the text a developer reads at the exact moment the guard fires and they are already debugging.
- **Verify round 2** found a **newly introduced** false claim at **4 sites**, one of which refuted
  itself inside a single `t.Fatalf` string.

Meanwhile the shipped production diff - one field's type, two signatures, one deleted import -
passed the invariants, correctness, security and integration lenses on the first pass and needed no
behavioural change at any point in the slice.

### Why prose and not code, weighed against the evidence rather than assumed

**Hypothesis A: volume.** About 670 lines of prose defend one boolean. True, and insufficient. It
explains a higher absolute count; it does not explain why each round *introduced* new errors. A long
correct comment is not a defect generator.

**Hypothesis B: uniqueness claims.** Every one of the round-1 and round-2 defects has the form "no
fixture varies X". CLAUDE.md already says a uniqueness claim is a claim about the complement and
cannot be checked by opening its subject. Strongly supported by the evidence. The round-2
introduction is the clean instance: the corrected text asserted that
`if h.AllowAutoEnroll { return }` is "false in every fixture in this package". That is false.
`handler_auth_test.go`, `handler_tasklog_integration_test.go` and
`handler_taskstatus_integration_test.go` all set `AllowAutoEnroll = true`. The writer checked the
default-lane fixtures - the ones they were editing - and never searched the integration lane.

**Hypothesis C: path-scope versus state-scope.** This is the one that explains the *recurrence*, and
it is the finding worth keeping. In every case the true statement was **path**-scoped and the
written one was **state**-scoped:

- Written (false): "no fixture varies `AllowAutoEnroll`."
- True: "no fixture that reaches this closure **on a FAILED registration** varies it. The ones that
  do vary it drive **successful** registrations, where the closure returns without releasing and
  there is nothing to notice."

The same shape appeared for `Metrics`. Written: "`Metrics` is nil in every default-lane fixture."
True: "`Metrics` **is** varied in this lane - `newSuccessFixture` sets it, `newStrandHandler` leaves
it nil, `handler_telemetry_test.go` exercises both head-on. What no fixture does is **reach the
flip** with `Metrics` nil."

That gap is why the corrections kept failing. `grep AllowAutoEnroll` **refutes** the state-scoped
claim in one command, and says **nothing at all** about the path-scoped one - for that you must open
each hit and ask which arm it drives. So the instrument that proves the old sentence wrong cannot
confirm the new sentence right, and a corrector working from grep output writes a fresh
complement-claim carrying the same unverifiability. Which is the general lesson:

> **The correction of a uniqueness claim is itself a uniqueness claim, and inherits its
> unverifiability.** When a review kills "no X does Y", demand the replacement be a **different
> shape**, not a narrower instance of the same one. The shape that terminates the loop is one that
> **names its own counter-examples**: "the ones that do vary it are A, B and C, and each drives the
> other arm." A claim listing its exceptions can be checked by opening three files. A claim
> asserting none exist can only be checked by searching for a shape, which is exactly what nobody
> does when they are correcting prose rather than hunting a bug.

The shipped text now has that form, at both sites and in the failure message. That is why round 3
held.

**And the reframe worth carrying:** the code passed every lens because every claim the code makes is
executable - by `go build`, by 21 packages of tests, by `go vet`, by a fifteen-mutation battery.
The prose's claims were about the **complement of the test suite**, a set no test can enumerate.
Prose is where a project puts the assertions it cannot execute, so it is structurally where the
wrong ones accumulate. Ten consecutive iterations of "wrong prose is the dominant defect class" is
not ten iterations of carelessness; it is the measurement of that structural fact. The remedy is not
to write comments more carefully. It is to prefer claim shapes that a reader can falsify cheaply,
and to route every negative claim through the fan-out on the assumption it is wrong.

### One instance survived into the shipped tree

`docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md:100` says this slice "put
**four** behavioural tests in the default lane". `handler_register_success_test.go` contains
**five** test functions and `handler_registration_deadline_test.go` gained a sixth
(`TestScriptedStream_DoesNotRetainARawAgentToken`). The note was written in Task 8; two of the tests
arrived from verification rounds afterwards. Four was right when written and is wrong now. **It is a
one-line correction and the conductor should make it before the PR**, listed here as a finding
rather than an item because it costs a word.

Note the shape, because it is the tenth iteration arriving inside the very task whose stated purpose
was correcting prose: **a count written mid-slice is a claim about a set the slice is still
changing.** Prefer "the default lane now covers the success path, the send-failure arm and both
inventory-failure arms" - a description that survives the next test being added - over a cardinal
number that does not.

---

## 4. A measured vacuity defect, inside the test written to prevent vacuity

`TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone` exists to make a prohibition
enforceable: the backlog item forbids "return early on an empty inventory" by name, because an agent
legitimately reporting zero workspaces would stop clearing its stale `worker_workspaces` rows, which
the dispatcher scores warm-workspace affinity off. The test asserted that `fakeTx` recorded exactly
one statement and that it was the `worker_workspaces` delete.

`fakeTx` recorded `Exec`. It did not record `Commit` or `Rollback`.

So mutation **M15** - making `applyInventory`'s closure return an error instead of nil, which rolls
back **every** inventory replace in production - left **all 21 Go packages green**, including the
test whose own docstring forbids exactly that outcome. A control mutation died as designed, so the
harness was valid and the survival was real. Found by review, not by the battery, because no planned
mutation had that shape.

> **Asserting that a statement was ISSUED is not asserting that the transaction COMMITTED.** A
> replace that runs and then rolls back leaves precisely the stale rows the forbidden early return
> would have left. When a fake stands in for a transaction, the fake must record how the transaction
> **ended**, or every assertion built on it is an assertion about a write that may never have
> landed.

The fix carries a second detail worth keeping, because the obvious version of it fails against
correct code. `commits` is the discriminator, **not** `rollbacks`: pgx's `beginFuncExec` defers a
`Rollback` unconditionally and then returns `tx.Commit(ctx)`, so a **successful** transaction records
one commit and one rollback, while a failed one records zero commits and two. `assert.Equal(0,
rollbacks)` on the success path would fail against a correct implementation. The three inventory
tests now each assert the outcome pair that distinguishes their own arm, including the
begin-failure arm, which is the only one that legitimately records neither.

This is the project's own "a test can be green because of the bug" pattern occurring **inside the
anti-vacuity test**, and it argues that the anti-vacuity discipline needs to be applied recursively:
the question "what would make this assertion true without the behaviour being true" has to be asked
of the check as well as of the thing checked.

---

## 5. A latent secret-retention hole, caught by looking at the slice not yet written

`scriptedStream.Send` used to discard its argument. This slice made it record, which is what made
"the RegisterResponse was actually sent" observable at all. Recording is also how a raw agent token
would come to sit in a test fixture's slice.

It is safe **today** for one reason only: every current user of the fake drives the reconnect path,
where `finishRegister`'s caller passes `rawAgentToken` `""`. Both enrollment callers pass a real
minted token - and **making the enrollment paths drivable without Postgres is the seam's entire
purpose**. The next test written against this fixture is, by construction, the one that would have
leaked.

Fixed by scrubbing **at the point of retention**, not in the accessor: `Send` clones the message
with `proto.Clone`, redacts `AgentToken`, and appends the clone. A projection that cleaned the field
on the way out through `sentMsgs()` would leave the secret sitting in the slice behind it, reachable
by a panic dump or by the next accessor someone adds. `proto.Clone` rather than a hand-written field
copy, because a field-by-field rebuild of `RegisterResponse` silently drops anything added to the
message later - a fixture that quietly stops recording a new field makes every test reading it prove
less.

The scope line is written down rather than left implicit: `RegisterResponse.AgentToken` is the only
field on any `CoordinatorMessage` that relay **mints**, so it is the only one whose retention leaks
something the coordinator created. `DispatchTask.env` reaches the same fake and is retained
unscrubbed, deliberately, because it is user-authored job-spec input and scrubbing it would cost a
future dispatch test the ability to assert what the agent was told to run. Stating the boundary is
what makes it a decision instead of an oversight.

Two named patterns fired together: "review the slice that has not been written yet", and "a secret
hides one layer below where it was defended".

---

## 6. "Added a property, forgot its guard", again - and the recorded lesson did not prevent it

`agentTokensSent` was added to a mutex-guarded struct. It was written under the lock in `Send` and
read as a **bare field** from the test goroutine. Its sibling `sent`, in the same struct, already had
`sentMsgs()`. The fixture's own stated next consumer is a `Connect`-on-a-goroutine test, where that
read is a real `-race` failure, not a theoretical one. Fixed with `tokensSent()`.

This repository has a recorded memory entry named, exactly,
`reference_added_a_property_forgot_its_guard`. It did not prevent the recurrence.

That is worth stating rather than glossing, because it is the third recorded lesson in a row to be
violated after being recorded (wrong prose: ten iterations; uniqueness claims: recurred inside this
slice's own corrections; this one). The honest implication:

> **A recorded lesson is a retrieval aid, not a control.** It fires when you are already looking for
> it, which is precisely not the moment you add a field to a struct. What has actually caught these
> in this project is the review fan-out, every single time. Two consequences: do not treat "we have a
> memory entry for that" as coverage, and keep the fan-out funded even on slices that look small -
> this one's production diff was four lines and the fan-out found three defects.

Where a lesson recurs often enough, the answer is a check rather than another paragraph. This one is
mechanically detectable - a field in a struct that also contains a `sync.Mutex`, read anywhere
outside a method that locks - but this project has just finished measuring what a structural guard
costs, so the honest recommendation is **not** to write one for a test fixture. Recorded here so the
next recurrence has a count to argue from.

---

## 7. The race lane is down on this machine, and what that leaves unverified

`go test -race` fails on this Windows host with a ThreadSanitizer allocation failure, error code 87.
**Reproduced on an untouched package at `origin/main`**, so it is environmental and pre-existing, not
caused by this slice. The 2026-08-24 recipe (MSYS2 mingw64 `CC`, plus `PATH`) no longer suffices.

Coverage was substituted with `-count=10..50` repetition: 200/200 on a 50-iteration flake hunt of
the four headline tests.

**What that leaves unverified, stated plainly rather than implied:** repetition without the detector
proves the tests are not flaky; it does **not** prove the absence of a data race. The new
`scriptedStream` mutex, the `fakeTx` mutex and the `Connect`-on-a-goroutine reads have never been
exercised under `-race` on this machine. CI runs `go test -race ./...` with no tags on every push, so
the property **is** checked - just not before the push. That is the real check and this slice is
relying on it.

This is the second consecutive slice in which the local `-race` recipe moved. The 2026-08-24 slice
got a clean run inside a Linux container; that fallback works and is written nowhere in the
repository. Proposed as an item below.

---

## 8. Concurrency friction worth recording

- **Two review lanes collided on a shared `scratchpad/mut` directory** and produced one nonsense
  mutation result before it was caught. The existing lesson
  (`feedback_mutation_testing_needs_isolated_tree`) says to give each mutation lane its own detached
  worktree. It did. **Isolated trees are not enough if the lanes share a scratchpad path.** The
  lesson needs the amendment: isolate the scratchpad directory too, or key it by lane name.
- **A sibling session left a stray `.claire/worktrees/hungry-neumann-b1ba4e/` directory inside this
  worktree** (one file, `internal/schedrunner/runner_test.go`). It is not this slice's work. Per "verify
  the tree, not subagent claims", the conductor should confirm it is absent from `git status` and
  from the PR diff before assembling.

---

## What Was Built

- **`internal/worker/handler.go`** - the entire production diff.
  - `txBeginner` interface (`:138-155`), with the doc comment stating that **three** call sites share
    it, not one, and that `*pgxpool.Pool` satisfies it so `cmd/relay-server` is unchanged.
  - `pool` field retyped (`:161`); both constructor parameters retyped; the `pgxpool` import deleted.
  - The ownership-handoff comment (`:771-786`) rewritten to what is now true: the guard covers source
    position, closure shape and the flag's write set, and names the behavioural half. It additionally
    says which clauses are **still doing the work** - the closure-shape ones - because "source
    position" reads like the whole story and is not.
- **`internal/worker/handler_register_success_test.go`** (new, no build tag) - `fakePool`, `fakeTx`
  (embedded nil `pgx.Tx`, `Exec`/`Commit`/`Rollback` recorded, `outcome()`), `successFixture`,
  `startConnect`, and five tests: the successful registration through `Connect`, the
  RegisterResponse-send failure, the empty-inventory replace, the inventory exec failure, and the
  inventory **begin** failure.
- **`internal/worker/handler_register_strand_test.go`** - `emptyRows`, the `strandDB.Query` empty arm
  (proved inert: all five existing construction sites set `queryErr` non-nil), the fake pool in
  `newStrandHandler`, header prose corrected.
- **`internal/worker/handler_registration_deadline_test.go`** - `scriptedStream` records sends under a
  mutex, scrubs minted agent tokens at the point of retention via `proto.Clone`, counts them in
  `agentTokensSent` behind `tokensSent()`, and gains `sendErr`. Plus
  `TestScriptedStream_DoesNotRetainARawAgentToken`.
- **`internal/worker/handler_handoff_guard_test.go`** - G3, G6, G7, G12, G15 deleted with
  `paramNamedByType`, the `strings` import and the `aliases` counter; header and both worked examples
  rewritten to path-scoped claims that name their counter-examples.
- **`internal/worker/handler_register_strand_integration_test.go`** - prose only. The test stays; its
  justification changed to the one that survives.
- **Two backlog items amended, one closed.** Zero SQL, zero migration, zero proto, zero generated
  file, zero files under `web/`.

## Key Decisions

- **Narrow the field, do not inject a function.** A settable `applyInventoryFn` on `Handler` is a
  *production* seam anything can replace at runtime; an interface is a *type* seam only a test can
  exploit by constructing a different value. It also covers one of three call sites instead of three.
- **`txBeginner` stays unexported and the field keeps the name `pool`.** No external caller names the
  type; renaming the field would churn the very guard the slice is shrinking. The doc comment carries
  the type's real meaning.
- **Drive the tests through `Connect`, not `finishRegister`.** The flag partitions a window between
  *two* releases, and calling `finishRegister` directly sees only one. The property worth pinning is
  that the generation is released **exactly once across the connection's life**, by teardown.
- **Empty rows, not populated.** Reconcile's content is covered in the integration lane; a populated
  fixture adds failure modes without adding coverage of what is under test.
- **Reduce the guard, do not retire it**, and only after the behavioural tests were green - the item's
  own acceptance criterion, and the reason M13 and M14 were run **after** the deletions rather than
  before, where they would have proved nothing.
- **The forbidden fix gets a test, not a comment.** "Return early on an empty inventory" is now blocked
  by `TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone` (M9), and the item's prohibition
  stopped being advisory.
- **Cite by symbol, not by line.** After a line citation in a new test comment drifted **within the
  slice**, the comments were rewritten to name `finishRegister`'s `applyInventory` call rather than a
  number. The spec had already conceded the same thing about its own table.

## Findings Triage

- **1 HIGH, vacuity: `fakeTx` recorded no `Commit`/`Rollback`**, so M15 (roll back every inventory
  replace) left all 21 packages green - including the test whose docstring forbids that outcome.
  Found by review; control mutation died, so the harness was sound.
- **1 HIGH, latent secret retention:** the new send recorder would retain a minted agent token the
  moment anyone points it at the enrollment paths, which is the seam's stated purpose. Fixed in the
  exposing change, at the point of retention.
- **1 MEDIUM, concurrency:** `agentTokensSent` written under the lock, read bare, with its sibling
  already guarded.
- **19 prose defects across three rounds** (6 planned + 9 + 4), one inside a guard failure message and
  one refuting itself inside a single `t.Fatalf` string. **One more survives in the shipped tree** and
  is a one-word conductor fix.
- **2 gaps found before implementation** - the missing `pgx.Rows` fake (spec) and the `pgxpool` import
  deletion (plan) - either of which would have broken the slice.
- **15 mutations run** (M1-M15), each with an applied-check and each known-survivor preceded by a
  control that died. M2 and M12 are deliberate known survivors, recorded so nobody "fixes" them.

## What Remains Open

- **`enrollAndRegister` and `autoEnrollAndRegister` are now fakeable and untested in the default
  lane.** Deliberate scope line. **Recommended as an item below.**
- **`applyInventory`'s SQL remains integration-only.** The seam moves the boundary; a fake tx proves a
  statement was issued and committed, never that it is correct against the schema.
- **G14's residual claim:** the wrapped-flip evasion is invisible to a runtime test because no fixture
  reaches the flip with `Metrics` nil. It is **not** broken in production - `main.go` sets `Metrics`
  unconditionally - but the day `Metrics` becomes optional it is a live defect and nothing runtime
  would notice on the way there. Written into the guard header rather than filed.
- **`bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory` stays open**, amended: its
  regression test is now a cheap default-lane test, and its line citations were corrected.
- **`idea-2026-08-23-integration-only-guards-ci-never-runs` stays open**, with one named instance
  removed.
- **No local `-race` path works on this host.** **Recommended as an item below.**

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **twenty-second
  iteration**. Everything load-bearing in the item was confirmed; two things it did not say changed
  the design.
- **A backlog proposal is not a contract** - twenty-two for twenty-two. The item's proposal was
  correct and incomplete in exactly the way that would have failed its own acceptance criterion.
- **Each stage treats the previous stage's output as untrusted** - honored in all three directions:
  the spec found the item's gap, the plan refuted two spec instructions, and the fan-out found the
  vacuity and the secret hole.
- **A mutation proof must leave a test behind** - honored. The commit/rollback counters, the
  begin-failure arm and the token-scrub test are all permanent.
- **Verify the mutation actually applied** - honored; every mutation step carried an applied-check
  command and every known-survivor was preceded by a control that died.
- **A test seam must not destroy the RED** - honored, and this was the sharpest instance yet: the
  headline test was written against a **pool-less** handler so it panicked inside `applyInventory` at
  HEAD, and the test function body did not change between RED and GREEN. Only the fixture helper did.
- **Say "declined, and here is the price"** - honored for the enrollment paths and for
  `applyInventory`'s bug, both declined with the price written down.
- **Wrong prose about correct code is the dominant defect class** - **tenth consecutive iteration**,
  three rounds deep, and one instance still shipped.

New from this iteration:

- **Label each guard clause SUBSTITUTE or PERMANENT when the guard is written.** The evasions all
  landed on permanent clauses; the deletions were all of substitutes. Without the labels the whole
  guard reads as debt it is not. **Candidate for durable memory.**
- **The correction of a uniqueness claim is itself a uniqueness claim.** Demand a different shape, not
  a narrower instance. The shape that terminates the loop names its own counter-examples.
  **Candidate for durable memory.**
- **A true claim is often path-scoped where the false one is state-scoped.** "No fixture varies X" is
  refutable by grep and says nothing about "no fixture **reaches this line** while varying X". The
  instrument that kills the old sentence cannot confirm the new one.
- **Asserting a statement was ISSUED is not asserting the transaction COMMITTED.** A tx fake must
  record how the transaction ended. **Candidate for durable memory.**
- **A recorded lesson is a retrieval aid, not a control.** Three lessons violated after being
  recorded; the fan-out caught all three. Do not treat a memory entry as coverage.
- **A count written mid-slice is a claim about a set the slice is still changing.** Prefer a
  description that survives the next test being added.
- **Isolated worktrees are not isolated lanes if the scratchpad path is shared.**

## Files Most Touched

- `internal/worker/handler.go:138-161` - `txBeginner` and the retyped field. The whole production
  change, and the comment that carries the type's real meaning.
- `internal/worker/handler.go:771-801` - the rewritten ownership-handoff prose: what the guard now
  covers, which clauses are still doing the work, and the disclosed panic boundary. Where the next
  person to touch `finishRegister` lands.
- `internal/worker/handler_register_success_test.go:54-140` - `fakeTx`, and the comment carrying the
  M15 story plus the commits-not-rollbacks argument. The vacuity lesson is here.
- `internal/worker/handler_register_success_test.go:293-419` - the first default-lane successful
  registration, and the exactly-once release assertion that reddens independently at both ends.
- `internal/worker/handler_registration_deadline_test.go:71-134` - the scrub-at-retention comment, the
  scope boundary for relay-minted credentials, and `tokensSent()`.
- `internal/worker/handler_handoff_guard_test.go:11-90` - the header, now path-scoped and naming its
  counter-examples. The three-round prose story is legible from this block alone.
- `docs/superpowers/specs/2026-08-25-handler-pool-seam.md:36-52` and `:366-397` - F1/F2 and the
  clause-by-clause disposition table.
- `docs/superpowers/plans/2026-08-25-handler-pool-seam.md:64-73` - the two spec defects called out
  rather than smoothed over.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No
  `git log`, no `git diff`, no test run. Every claim below that could be checked by reading was
  checked against the worktree.
- **Verified by reading:** `internal/worker/handler.go:130-175`, `:440-470`, `:545-560`, `:750-805`;
  `internal/worker/handler_register_success_test.go:1-145` and `:478-596`;
  `internal/worker/handler_registration_deadline_test.go:20-152`;
  `internal/worker/handler_handoff_guard_test.go:1-100` and `:430-520`, `:613-620`; the spec and the
  plan in full; the closed item in full; both amended items; and a full enumeration of `func Test` in
  `internal/worker`.
- **Confirmed against code, not inferred:** that `Handler.pool` is `txBeginner` and the interface has
  exactly one method; that three `pgx.BeginTxFunc(ctx, h.pool, ...)` sites share it (`:490`, `:565`,
  `:1776`); that `fakeTx` counts commits and rollbacks and exposes them through `outcome()`; that
  `scriptedStream.Send` clones with `proto.Clone` and redacts before appending, and that
  `agentTokensSent` is read only through `tokensSent()`; that the guard file no longer imports
  `strings` and no longer mentions `paramNamedByType`; that the guard header and both worked examples
  are now path-scoped and name `AllowAutoEnroll` in the integration lane as the counter-example; and
  that `.claire/worktrees/hungry-neumann-b1ba4e/internal/schedrunner/runner_test.go` exists inside
  this worktree.
- **One live prose defect found by reading**, stated in section 3: the Progress note on
  `idea-2026-08-23-integration-only-guards-ci-never-runs` said four behavioural tests; there are five
  in the success file and six new default-lane test functions in total. **Corrected before the PR** -
  the tenth-iteration instance of this project's dominant defect class, caught by the retro rather
  than by any of the three prose rounds that preceded it, which is itself section 3's point.
- **One arithmetic correction, applied.** The figure "93 pass, up from 91" that circulated during the
  slice compares against **HEAD~**, one commit back, not against `origin/main`, so it understates the
  slice roughly threefold. The durable, checkable statement used from here on: `internal/worker`
  reports **93 pass / 0 fail / 0 skip**, and this slice adds **six new default-lane test functions** -
  five in `handler_register_success_test.go` plus `TestScriptedStream_DoesNotRetainARawAgentToken`.
  Both halves were verified against `origin/main...HEAD` rather than quoted forward.
- **Reported by the implementing and verifying lanes, not re-run here:** 21 Go packages green;
  `internal/worker` 93 pass / 0 fail / 0 skip; integration lane 163 pass under `-tags integration
  -p 1` plus `cmd/relay-server` green; 200/200 on the 50-iteration flake hunt; `go vet` and
  `go vet -tags integration` clean; and every mutation result M1-M15, including M15's 21-packages-green
  survival before the counters existed.
- **Not verified:** all test results, the commit set, the diff stat, and the change set as `git` sees
  it. `-race` was not run at all, locally or here; see section 7.
- **No PR number appears anywhere in this retro or in the proposed items**, by instruction.
- **Outstanding and belonging to the conductor:** the one-word Progress-note correction, the stray
  `.claire/` directory check, the item filings below, the CLAUDE.md decision, the final gates, all
  commits, and a ROADMAP refresh.

## CLAUDE.md verdict

**No amendment is earned by this slice, and that is a deliberate call rather than an omission.**

The two strongest candidates are already in CLAUDE.md in a form this slice sharpens rather than
contradicts:

- "A uniqueness claim is a claim about the complement" is already written down. What this slice adds -
  that a *correction* inherits the shape, and that the true claim is usually path-scoped - is a
  refinement of an existing rule, not a new invariant, and CLAUDE.md's Invariants section is for rules
  that new **code** must not bypass. A prose-writing heuristic does not belong there. It belongs in
  durable memory, and it is proposed as such above.
- "End the generation before releasing the resource", including the acquire-direction reading added on
  2026-08-24, is **untouched** by this slice and is now, for the first time, **observed** rather than
  asserted: the success test proves exactly one release across a connection's life, and the send-failure
  test proves the failing arm still releases exactly once. That is the rule working as written.

One thing the conductor may consider, and I do not recommend it: adding `txBeginner` to CLAUDE.md's
`internal/worker/` code-map bullet. The type is unexported, changes no wiring and moves nothing
operator-visible; the two `internal/scheduler` precedents it copies are not in CLAUDE.md either.
Adding it would set a precedent for documenting test seams in the code map, which is churn.

## Recommended Backlog Items

Proposals only - the conductor files via `/backlog`, and the human gives final accept. Two are
recommended. Three candidates were weighed and **rejected**, with reasons, because an item that never
gets picked still costs a file and every edge into it.

**1. `idea`: extend the default-lane registration fixture to `enrollAndRegister` and
`autoEnrollAndRegister`** - priority `medium`.

- The seam this slice landed covers **all three** `pgx.BeginTxFunc(ctx, h.pool, ...)` sites
  (`internal/worker/handler.go:490`, `:565`, `:1776`), not just `applyInventory`'s. Confirmed by grep
  in this pass. The two enrollment transactions became fakeable the moment this slice merged and no
  test exercises them without Postgres.
- Both hold branch logic **no default-lane test reaches**: `errEnrollmentNotConsumable`,
  `errWorkerRevoked`, and the auto-enroll audit log line whose own comment argues at length about its
  forgeability. Today every witness for those is `//go:build integration`, which CI compiles and never
  runs - the same structural problem this slice just closed for the reconnect path.
- **It is also the item that retires a speculative defense.** `scriptedStream` now scrubs minted agent
  tokens at the point of retention, and the reason that is speculative rather than load-bearing is
  written into the fixture: every current user drives the reconnect arm, where the token is `""`. Both
  enrollment callers pass a real one. `TestScriptedStream_DoesNotRetainARawAgentToken` is what keeps
  that from being silent, and this item is what makes it matter.
- Acceptance should require at least one default-lane test per named branch, and should say plainly
  that the integration coverage stays.

**2. `idea`: relay has no documented, working local `-race` path** - priority `low`.

- CLAUDE.md's Commands section does not mention `-race` at all, yet `.github/workflows/go-ci.yml`
  gates every push on `go test -race ./...`. So the one command that can fail CI has no documented
  local form.
- Two consecutive slices have had to rediscover the recipe and the second could not get it working:
  2026-08-24 found that MSYS2 mingw64 `CC` alone no longer sufficed and needed `PATH` too, and got its
  clean run **inside a Linux container**; 2026-08-25 hit a ThreadSanitizer allocation failure, error
  code 87, reproduced on an untouched package at `origin/main` and therefore environmental. This slice
  substituted `-count=50` repetition, which proves the tests are not flaky and proves nothing about
  data races.
- The remedy is cheap and mostly already known: write down the container fallback that worked, or
  record a decision that `-race` is CI-only on Windows hosts with the container recipe as the
  pre-push option. Acceptance is a command in the repo that a Windows dev host can run to a passing
  `-race` result, **or** a written decision saying it is CI-only.
- Filed at `low` deliberately: CI does check the property on every push, so this is developer
  feedback-loop cost, not a coverage hole.

**Rejected candidates, with reasons:**

- **A durable fix for the prose-failure class (section 3).** Rejected as an item. The two lessons -
  the correction inherits the shape, and path-scope versus state-scope - have no acceptance criterion
  a future slice could satisfy and close; an item that cannot be closed is a permanent open row. They
  belong in durable memory (proposed in Improvement Goals) and, if anywhere in a file, appended to
  CLAUDE.md's existing uniqueness-claim bullet. The one item-shaped residue is the four-versus-five
  count, which is a one-word correction the conductor makes directly.
- **Sweep the tree for other "issued but not committed" fakes (section 4).** Rejected on evidence.
  `fakeTx` is the **only** `pgx.Tx` fake in the tree - grep for `pgx.Tx` in `*_test.go` returns no
  other implementation. Every other transaction site (`internal/api`, ten `s.pool.Begin` calls;
  `internal/schedrunner/runner.go:59` with a nested savepoint) is covered against real Postgres, where
  an uncommitted write is visible as an absent row. There is no second instance to sweep.
- **Narrow `internal/api`'s and `internal/schedrunner`'s pools the same way.** Rejected as premature.
  Unlike `internal/worker`, no named branch in those packages has been **shown** unreachable in the
  default lane, so the item would have no acceptance criterion beyond "do the refactor". The rule the
  strand slice followed is the right one and should be followed again: file the seam when a slice
  needs to pin a line behind it, and pay for the guard in the meantime with the price written down.
