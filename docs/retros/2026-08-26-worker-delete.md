---
date: 2026-08-26
topic: worker-delete
branch: claude/pr-merging-session-868949
range: 02100da0858cb4875782d502542a7bebdf7373d4..327353f5a8533c66a2d463f59f75a20c14814c2e
---

# Session Retro: 2026-08-26 - Worker Delete

**TL;DR:** Relay had no way to remove a worker machine from its fleet at any layer, and the
previous session made that worse: once a machine claimed a name, nothing could reclaim it. This
session added an admin-only delete operation plus the command-line command for it. Deleting a
worker returns its in-flight work to the queue before removing the machine's record, in a single
database transaction whose statement order is the entire safety argument. Every stage of the
process refuted the stage before it - most usefully the original bug report, which overstated the
problem in three specific ways.

## Handoff

`DELETE /v1/workers/{id}` (admin-only; `handleDeleteWorker` in `internal/api/workers.go`, wired in
`internal/api/server.go`) runs one transaction whose statement order is the correctness argument:
`GetWorkerForUpdate`, requeue live assignments while `worker_id` still names them,
`ClearEnrollmentConsumerForWorker`, `RemoveWorkerFromReservations` (`array_remove` with `WHERE $1
= ANY(worker_ids)`), `CountTerminalTasksForWorker`, then `DeleteWorker` behind a `status IN
('offline','revoked')` allow-list. Reversed, the FK's `ON DELETE SET NULL` ends every assignment
with no epoch bump and the task is `running` forever. Returns `200` with four counts rather than
`204`, because with no audit log those counts plus one log line are the only record of what was
destroyed.

`sendCancelSignals` fires after commit for every requeued task with no branch on status - the HIGH
the verify fan-out found after the conductor's own `/code-review` had looked at it and cleared it.
CLI arm is `relay workers delete --yes <id-or-hostname>`, with `resolveWorkerIDIncludingRevoked`
(the workers most likely to be deleted are the ones `GET /v1/workers` excludes) and a
multi-argument ambiguity refusal the spec did not ask for. The `agent_enrollments.consumed_by`
no-action FK is deliberately left alone - it fails closed at SQLSTATE 23503 where `ON DELETE SET
NULL` would fail silent - and `TestDeleteWorker_IsRefusedWhileAnEnrollmentNamesTheWorker` pins
that premise. The ghost-command guard open since 2026-08-25 now goes green because the command
exists.

Known holes carried forward: `handleDeleteWorker`'s `n == 0` branch is unreachable by
construction, labelled DO NOT DELETE, and mutation M8 survived for that reason;
`attribution_cleared` is unpinned across the API/CLI boundary, since `internal/cli`'s `deleteResp`
is a hand-written struct and the CLI test's fixture is a hand-written literal using the same
spellings, so renaming the server-side tag leaves both green while the shipped command prints 0.
Closes `bug-2026-08-25-no-worker-delete-at-any-layer`; unblocks the delete arm of
`idea-2026-08-25-ttl-reaper-for-never-reconnected-workers`. Five items filed this slice:
`idea-2026-08-26-cli-has-no-integration-coverage`,
`bug-2026-08-26-integration-lane-times-out-on-docker-teardown`,
`bug-2026-08-26-deleting-a-worker-frees-its-hostname-to-a-race`,
`bug-2026-08-26-re-enrolling-a-deleted-worker-escapes-its-reservations`,
`idea-2026-08-26-should-worker-subcommands-resolve-revoked-hostnames`. Shipped as squash commit
`327353f` (PR #151). Next session starts at ROADMAP Now, which leads with the retries and
un-fireable-schedule pair.

### Still open

- **The five filed items** (named in the Handoff): CLI integration coverage; the Docker teardown timeout; the
  hostname freed to a race; re-enrollment escaping its reservations; whether the other worker subcommands
  should resolve revoked hostnames.
- **`attribution_cleared` is not pinned across the API/CLI boundary.** `internal/cli`'s `deleteResp` is a
  hand-written struct with five JSON tags; the CLI test's fixture is a hand-written literal using the
  same spellings. Rename the server-side tag and both stay green while the shipped command prints `0`.
  Flagged by the engineer. Item below.
- **The `n == 0` branch is genuinely untested.** M8 survived, for the same reason no deterministic test
  can drive it. Documented in place rather than papered over.
- **T-C2 has no mutation** and one was not manufactured: the CASCADE lives in migration `000007`, and
  mutating a migration changes what every other fixture builds.
- **A reservation whose `worker_ids` empties is silently inert.** `selector` is never read by the
  dispatcher, so it reserves nothing and nothing says so. Disclosed in README, proposed in spec 17.3.
- **The enrollment audit link is broken by design.** `consumed_by` is nulled; preserving it would need
  the consuming hostname denormalised onto `agent_enrollments` at consume time - a migration plus a
  change to the authenticated enrollment write path, for a value nothing reads. Spec 17.2.
- **No SPA affordance.** Spec 17.1.
- **`go test -race` unrunnable locally**, third consecutive slice. CI's `race + integration-build` is the
  gate.

## What Was Built

- **`internal/store/query/workers.sql`** - `GetWorkerForUpdate` (the id-keyed twin of
  `GetWorkerByHostnameForUpdate`) and `DeleteWorker` (`:execrows`, carrying the
  `status IN ('offline','revoked')` allow-list as **the control**, with the Go gate as a second question
  and a better error). `CountWorkers`'s comment corrected: revoke is no longer the only cleanup relay
  has.
- **`internal/store/query/agent_enrollments.sql`** - `ClearEnrollmentConsumerForWorker` (`:execrows`).
  The no-action FK is deliberately **left alone**: it fails closed with SQLSTATE 23503 for any future
  deleter that forgets to unlink, where `ON DELETE SET NULL` would fail silent.
  `TestDeleteWorker_IsRefusedWhileAnEnrollmentNamesTheWorker` pins that premise and goes green only if
  somebody adds the cascade.
- **`internal/store/query/reservations.sql`** - `RemoveWorkerFromReservations`, `array_remove` with a
  `WHERE $1 = ANY(worker_ids)` that makes the `:execrows` count mean "how many reservations named this
  worker" rather than "how many rows are in the table".
- **`internal/store/query/tasks.sql`** - `CountTerminalTasksForWorker`, the allow-list of section 6, plus
  the `ListOverdueAssignedTasks` comment rewritten: the strand it names is now prevented by **ordering**
  rather than by nothing-in-this-repo-deleting-a-worker.
- **`internal/api/workers.go`** - `handleDeleteWorker` and `deleteWorkerResponse` (four counts; `200`
  with a body rather than `204`, because with no audit log those counts are the only record of what was
  destroyed). One unbudgeted success log line, with `%.200q` on the peer-supplied hostname and the budget
  question answered in the comment rather than skipped. The `n == 0` branch is unreachable by
  construction, labelled DO NOT DELETE, and honest that mutation M8 survived for that reason.
- **`internal/api/server.go`** - `mux.Handle("DELETE /v1/workers/{id}", auth(admin(...)))`.
- **`internal/cli/workers.go`** - `doWorkersDelete`, the switch arm, all three usage strings,
  `resolveWorkerIDIncludingRevoked` (R8: the workers most likely to be deleted are the ones
  `GET /v1/workers` excludes), and an **ambiguity refusal** the spec did not ask for: `delete --yes hostA
  hostB` used to destroy `hostA`, report success, and name neither.
- **`internal/agent/messages.go`** - the token-less exit message names `relay workers delete` as remedy
  3, behind revoke-then-enrollment-token and rename, which stay 1 and 2.
- **Tests**: `internal/api/workers_delete_integration_test.go`,
  `internal/store/workers_delete_integration_test.go`, `internal/cli/workers_delete_test.go`, plus the
  `internal/agent` message edits.
- **README** - four passages plus the CLI reference. Delete frees the **hostname** always and frees
  ceiling **budget** only for a worker that was not already revoked, and it is deliberately **not** added
  to the ceiling remedy ladder.

### Verification

- **This pass had no shell.** Nothing was executed here - no `git log`, no `git diff`, no test run. Every
  claim below that could be checked by reading was checked against the worktree.
- **Confirmed against code, not inferred:** that `handleDeleteWorker` runs lock, gate, requeue, unlink,
  scrub, count, delete, notify, commit, log, cancel, respond, in that order; that the 409 string now
  states revoking does not disconnect; that the cancel block has no status branch and routes through
  `sendCancelSignals`; that `CountTerminalTasksForWorker` is an allow-list on
  `('done','failed','timed_out')` with the deny-list asymmetry in its comment; that the `NOWAIT` probe
  and its post-rollback control exist; that `deleteResp` in `internal/cli/workers.go` is a hand-written
  five-field struct and that `workers_delete_test.go`'s fixture is a hand-written literal; that
  `DELETE /v1/workers/{id}` is routed behind `auth(admin(...))`.
- **Reported by the implementing and verifying lanes, not re-run here:** all 21 unit packages green;
  `internal/api` and `internal/cli` green at `-count=5`; integration green on a cache-busted run
  (`internal/api` 474.2s, `internal/store` 200.5s) plus earlier full runs of `internal/worker` and
  `internal/cli`; `go vet` and `go vet -tags integration` clean.
- **Both landmines held throughout:** `internal/agent/cli_commands_exist_test.go` passes
  **byte-identical and unedited** - it goes green because `case "delete":` exists, which is the item's
  closing acceptance criterion - and both restored CLI fixtures are byte-identical to `origin/main`.
- **`go test -race` was unrunnable locally all session** - environmental, third consecutive slice. CI's
  `race + integration-build` job is the gate and this slice relies on it.
- **Not verified here:** all test results, the commit set, the diff stat, and the change set as `git`
  sees it.
- **No PR number appears anywhere in this retro or in the proposed items**, by instruction.
- **Outstanding and belonging to the conductor:** the item filings below, the `/backlog close` of
  `bug-2026-08-25-no-worker-delete-at-any-layer` including its `git mv` to `docs/backlog/closed/` and a
  Resolution note recording the severity correction, the final gates, all commits, and a ROADMAP refresh.

## Key Decisions

- **Requeue before delete, in one transaction, and it is the whole correctness argument.** Reversed, the
  FK's `ON DELETE SET NULL` ends every assignment with no epoch bump, and the row becomes unreachable by
  every worker-keyed statement in the tree - `running` forever, holding no slot, its job never leaving
  `running`. CLAUDE.md's first invariant in its original wording.
- **Cancel signals are sent after commit**, for every requeued task, with no branch on status
  (section 2).
- **No migration.** Decision A (leave the FK no-action) and Decision C (no FK on a `UUID[]`) both
  declined one.
- **The status gate is an allow-list on both arms**, SQL and Go, and so is the new terminal-task count
  (section 6).
- **`200` with four counts, not `204`.** No audit log exists; the counts and one log line are the record.
- **`--yes` is a required flag, never an interactive prompt** - every destructive CLI path in relay is
  flag-driven, and a prompt breaks scripted use.
- **The reservation scrub's position in the transaction is convention, not necessity**, established by
  mutation and labelled as such so it is not re-derived as a constraint.
- **Delete does not touch `worker.Registry` or `worker.GraceRegistry`.** There is no sender to
  identity-check, and reaching for one would rebuild the finishRegister strand on the HTTP side. What
  replaces the identity check is the connected-status refusal plus the row lock, and the code says so.

### CLAUDE.md verdict

**No amendment is earned, and the recommendation is deliberate rather than a shrug.**

The strongest candidate is section 2, and it is genuinely tempting: "a fenced write path proves nothing
about a running subprocess" is a cross-cutting rule, it was violated in shipped code, and the Invariants
section already carries the epoch fence at length. **It still does not belong there, for three reasons.**

1. **The Invariants section is for rules new code must not bypass, and this is a rule for a REVIEWER.**
   The code that was wrong did not bypass an invariant - it satisfied every one of them, which is exactly
   why the record looked clean. The defect was in an *audit*, and CLAUDE.md's Invariants are not an audit
   checklist.
2. **The concrete instance already lives where it will be read.** `internal/api/workers.go:708-724` states
   the argument, names the mistake, and explains why there is no status branch. A future author writing a
   fourth teardown path meets that comment; they do not necessarily re-read the fence bullet, which is
   already the longest item in the file.
3. **The fence bullet is at its carrying capacity.** It already runs to a page, absorbed the identity
   clause on 2026-08-24 and the honesty clause after that. Appending a fourth qualification makes the
   whole thing less likely to be read, which costs more than the clause buys. This slice's own section 3
   is the evidence: **a clause added yesterday did not fire today**, and the diagnosis was that it was
   filed under a register the author was not in. Adding more text to a bullet that is already skimmed is
   the wrong response to a discoverability failure.

**Also considered and rejected:**

- **Section 3's lesson** (a refusal string must not name a state its own allow-list admits). Real and
  checkable, but it is one site, and the correct response is the guard proposed below rather than prose.
  CLAUDE.md gained a clause about this class **yesterday**; a second clause one day later, for the same
  class, is the exhortative fix this project has repeatedly measured as ineffective.
- **Sections 4 and 5** (mutation harness positioning, unkillable claims). Test-methodology rules. Durable
  memory already carries three mutation lessons and they are the right home; the same call was made on
  2026-08-25 for the same reason.
- **Section 6.** The rule it vindicates is already in CLAUDE.md, correctly phrased, and it *worked*. The
  only lesson is that it worked, which is not an amendment.
- **Section 1's internal-contradiction pass.** A process rule for artifact authors, not for code. It
  belongs in the agent-team README's phase briefs if anywhere, and that is the conductor's call.

One thing the conductor should consider and I do **not** recommend: adding "delete requeues before it
releases the row" to the Invariants list. It is one code path with a comment, two integration tests and a
mutation (M2) that kills it. The invariant it instances - end the generation before releasing the
resource - is already there, in its original wording, and this handler is a *worked example* of it rather
than a peer rule. The right place for a worked example is the code, and it is there.

## What Went Wrong and What Changes

*Original headline: every layer refuted the layer above it, the most valuable refutation was of
the conductor's own backlog item, and the headline defect is a review finding that reached the
right suspicion and the wrong conclusion.*

> **Retrofit note.** This file was restructured onto the `/retro` skill's documented format after
> the fact; do not take its remaining shape as the template. Only the frontmatter, title, TL;DR
> and Handoff were rewritten - every body section is the original prose, reordered and demoted
> under the skill's menu headings. Two things below are *not* the format to copy. The lesson
> bullets predate the skill's paired `**<what went wrong>.** ... -> **What changes:** ...` form and
> are left in their original wording rather than re-invented. And bullets tagged **Candidate for
> durable memory** were never promoted at the time - the skill's Step 5 offers each reusable rule a
> durable home and stamps the bullet `(promoted to <home>)` or `[[slug]]`, which is what stops the
> next retro carrying it. Annotating a lesson is not promoting it.

The lessons are listed first below, then the detailed accounts that produced them.

### Lesson ledger

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, twenty-fourth iteration, and
  the strongest instance yet: the item's *severity* fell, not just its facts.
- **A backlog proposal is not a contract** - twenty-four for twenty-four.
- **An accurate item can prescribe a wrong remedy** - and this time the accurate item prescribed a right
  remedy for a wrong reason, which is the variant.
- **Each stage treats the previous stage's output as untrusted** - honored at every boundary, and the
  counts are in section 1.
- **Verify the mutation actually applied** - honored and **extended**: applied is not enough, it must be
  applied *where* (section 4).
- **A mutation proof must leave a test behind** - honored; the `NOWAIT` probe and its control are
  permanent.
- **Diagnose a red gate, measure both ways** - honored literally, with a baseline at `origin/main`
  (section 7).
- **Wrong prose about correct code is the dominant defect class** - **twelfth consecutive iteration**,
  and this time the wrong prose was a `writeError` literal rather than a doc.

New from this iteration:

- **Enumerating what an actor can SAY is not enumerating what it can DO.** A fenced write path proves
  nothing about a running subprocess, and a correct fence is what hides it. **Candidate for durable
  memory.**
- **An applied-check must assert WHERE the mutation landed**, and a mutation battery must never run
  behind a `-run` filter narrower than the mutation's blast radius. **Candidate for durable memory.**
- **"Unkillable" is a claim about the instruments reached for.** Require the shape of the failed search,
  which is falsifiable, instead of the verdict, which is not. **Candidate for durable memory.**
- **A green re-run bounds a red run's frequency; it does not retire it.** Report "N green, one red, cause
  unestablished".
- **Read every artifact once for internal contradiction before handing it down** - not for truth, for
  self-consistency. Two of this slice's cheapest findings were that pass not being run.
- **A test whose name asserts more than its body is a latent unkillable claim.**

### Findings triage

- **1 HIGH, duplicate execution** (section 2): a revoked-and-connected worker's tasks requeued and
  re-dispatched with no cancel to the running agent. Found by two verify lenses after the conductor's own
  `/code-review` cleared it. Closed by `sendCancelSignals`.
- **1 HIGH-adjacent, operator-facing** (section 3): the 409 prescribed the bypass to its own gate.
- **2 false survivals** (section 4): a harness that patched the wrong function, behind a `-run` filter
  that hid the evidence.
- **1 refuted unkillable** (section 5): `FOR UPDATE` killed deterministically with `NOWAIT` plus a
  control, and the test whose name claimed the property gained a body that tests it.
- **1 upward correction** (section 6): deny-list to allow-list, engineer over conductor, on a written
  rule.
- **1 undiagnosed red, now attributed** (section 7): `go-winio` in testcontainers' `ContainerRemove`.
- **14 upstream refutations** (section 1), of which **2** were self-consistency defects catchable
  without new information.

### 1. The refutation ladder, counted first, because the shape is the story

| Layer | Refuted | Of what |
|---|---|---|
| Spec (Phase 1) | **3** | the backlog item, written by the conductor the previous day |
| Plan (Phase 2) | **4** | the spec |
| Engineer (Phase 3) | **7** | the plan and the spec |
| Verify fan-out (Phase 4) | 1 HIGH plus a cluster | the shipped code, and the conductor's own review finding |

**The most valuable single refutation was of the conductor's own artifact.**
`bug-2026-08-25-no-worker-delete-at-any-layer` was written immediately after the slice that motivated
it, in the flush of discovery, with no independent review and no cooling-off. The conductor knew that
and asked the spec pass for *extra* scepticism on this item **for that reason**. That instruction paid
for itself three times:

- **The severity argument was self-refuting.** The item's terminal case was a machine re-provisioned in
  place whose operator "will not or cannot have an enrollment token issued", with **no remedy at all**.
  But delete is a destructive fleet operation, so it must be admin-only - which means the proposed fix
  requires *exactly the admin the premise removes*. "No remedy at all" was really "no remedy that avoids
  an admin", and the fix does not avoid an admin. Priority HIGH became `medium`. The feature survived on
  a different and honest case: nothing reclaims the row or the hostname, README already says so, and the
  TTL reaper's delete arm is blocked on it.
- **The FK blocker does not apply to the item's own motivating case.** `agent_enrollments.consumed_by`
  has no `ON DELETE` action, so a raw `DELETE FROM workers` fails for a token-enrolled worker. But the
  item's motivating machine **auto-enrolled**, consumed no enrollment token, and so has
  `consumed_by IS NULL` - a raw delete already succeeds for it today, out of band. The blocker is real
  for the general case and not for the case the item argued from.
- **The stale-reservation claim was backwards.** The item said "the dispatcher keeps matching against"
  dangling ids. `reservedIDs` (`internal/scheduler/dispatch.go:185-191`) is an **exclusion** set,
  consulted while iterating live `workers` rows at `:221`. An id with no row is compared against nothing
  and excludes nothing. And `handleCreateReservation` never existence-checks `worker_ids`, so the state
  is already creatable through the public API. The scrub still shipped, for the contract that a deleted
  id ceases to exist - **not** for the item's reason, and the code says so where the statement lives.

#### Is layered refutation working, or is it evidence the upstream artifacts are written too fast?

**Both, and the split is roughly one part speed to three parts irreducible.** The honest accounting:

**Evidence for "written too fast", and it is the minority of the count.** Two findings needed no new
information at all. The item's severity argument needed only re-reading the premise against the fix -
two sentences, one document, no code. And the spec's cancel-signal argument (section 2) cited section
8.1 as its authority while **8.1 contained the sentence that refutes it**. A self-consistency read of
one's own document catches both. Those are the two to feel the cost of.

**Evidence for "irreducible", and it is the majority.** The other twelve needed an instrument the
upstream layer did not have. The plan answered the spec's open lane question by *opening*
`internal/api`'s test files and finding no default-lane server seam. It refuted the spec's
`resolveWorkerID` hedge by finding `TestWorkersRevoke_NotFound` asserting
`require.Equal(t, "/v1/workers", r.URL.Path)` on every request - one grep deep, but a grep no
spec-shaped reading would have thought to run. And the engineer's sharpest refutation only fell to
**running the mutation**: spec 6.3 and the plan both claimed the reservation scrub must precede the
`DELETE` "because after it there is no id to scrub by", which is self-refuting on its face (the array
has no FK, which is the entire reason the statement must exist, and is equally the reason the `DELETE`
does not disturb it) - and the way it was actually established was moving the call after `DeleteWorker`
and watching every delete test stay green. Reasoning got the suspicion; the experiment got the answer,
and the shipped comment at `internal/api/workers.go:626-633` now labels that step's position
"CONVENTION, NOT NECESSITY" so nobody re-derives a constraint that is not there.

**The verdict.** A pipeline where each layer refutes the one above is doing its job, and the count going
*up* as you descend is the healthy direction: the cheapest refutations are the ones nearest the code.
The thing to watch is not the count, it is the *class*. Two self-consistency defects in two consecutive
artifacts is a speed signal, and it is actionable:

> **Before handing an artifact down, read it once for internal contradiction only** - not for truth,
> not for completeness. Both cheap findings this slice were a claim in one section refuted by a sentence
> in another section of the same document. That pass costs minutes and it is the only pass that catches
> the defect class the next layer inherits wholesale.

### 2. THE HEADLINE: the conductor's own review finding reached the right suspicion and the wrong conclusion

**The suspicion was correct and precise.** The conductor's Phase 4 `/code-review` flagged that `revoked`
sits in the delete allow-list while revoking does not close the agent's stream, and asked whether that
was safe. It then concluded: **safe, for reasons stated nowhere.**

**Two verify lenses independently escalated it to a HIGH correctness bug**, and they were right.

#### The error, stated as specifically as it deserves

The clearing argument enumerated **what a stranded agent could still write**, and every entry in that
enumeration is correct:

- `UpdateTaskStatus`, `AppendTaskLog`, `IncrementTaskRetryCount`, `RequeueTask` and `RequeueTaskByID`
  all carry a plain `worker_id = $n` plus an epoch fence. `RequeueWorkerTasks` has just bumped
  `assignment_epoch` and nulled `worker_id`. Every one of the stranded agent's writes is refused.
- Its stored credential is dead: `GetWorkerByAgentTokenHash` finds no row after the delete, and
  `internal/agent` exits on `Unauthenticated`.
- Its grace timer fails closed on `RequeueWorkerTasksIfEpoch`'s `EXISTS` guard.

So the task **record** is clean. The conductor checked the record and concluded the world was clean.

**What it forgot is that the agent's subprocess does not write. It executes.** `internal/agent`'s
`Runner` has a `p4 sync`, a render, or a submit running as a child process, and nothing about a fenced
gRPC write path reaches into that process. The requeue hands the task back to the dispatcher, the
dispatcher places it on a second worker, and **no cancel was sent to the first**. Two machines run the
same task. The duplicate side effect - a submit, a shared output path, a license seat - is real, and it
is **invisible in the task record precisely because the original agent's writes are correctly fenced
away**. The fence that made the record clean is what made the damage unobservable.

#### The spec contained its own refutation, two sections apart

Worth pinning, because it is section 1's "internal contradiction" class in its sharpest form. Spec 6.3:

> After step 7 there are no cancel signals. ... Delete refuses a connected worker (8.1), so **by
> construction there is no agent to tell**.

Spec 8.1, the section it names as its authority:

> Not required: revoke-first. It adds no safety over the connected check (**a revoked worker is not
> necessarily disconnected**; `ClearWorkerAgentToken` does not close the stream, README:353) ...

8.1's own framing carried the same slip one line earlier - "`online` and `stale` both mean connected,
**so the permitted set is exactly the not-connected set**" - which is true of `offline` and false of
`revoked`. One member of a two-member allow-list was doing work the surrounding argument had already
disproved.

**And revoked-and-connected is not a narrow window.** `handleDeleteWorkerToken` is a single
`ClearWorkerAgentToken`: it does not close the stream, unregister the sender, or requeue anything, and
the liveness sweeper only moves `online` and `stale`. A revoked worker stays connected and running tasks
**indefinitely**. It is a stable state, not a race - which is why "the window is small" was never
available as a defence.

#### The fix, and why it is the right shape

`handleDeleteWorker` now builds a `cancelSignal` per requeued task and routes them through
`s.sendCancelSignals` (`internal/api/workers.go:708-733`), exactly as `handleDisableWorker` does, so the
sends stay bounded - the one-bounded-sender invariant. **There is deliberately no branch on status.**
`Registry.Send` on an unregistered id is one map lookup returning an error that a best-effort path
discards, so the `offline` arm costs nothing and, crucially, **cannot imply a connection this path
forbids**. The comment at `:708-724` states the whole argument *including the mistake it corrects*, in
the tree, where the next reader lands.

#### The general lesson

> **Enumerating what an actor can SAY is not enumerating what it can DO.** An audit of a stranded or
> revoked peer that walks its write paths, finds every one fenced, and concludes "safe" has audited the
> database and called it the world. Ask separately what the peer is *executing*: subprocesses, open file
> handles, held licenses, in-flight calls, transactions submitted into another system. Those have no
> fence - and a correct fence is what hides them, because the evidence of the second actor never reaches
> the row you are reading.

This generalises past this handler. Every place relay decides a worker is "gone" - the watchdog, the
grace window, disable, and now delete - is making a claim about a *process* on evidence that is a
*record*. The gap between the two is exactly where cancel signals live, which is why `sendCancelSignals`
already existed at two call sites and now has three.

### 3. The refusal message prescribed the bypass to its own gate

The 409 shipped into review reading:

> worker is connected; disable it and wait for it to go offline, **or revoke it**, before deleting

**Revoking does not disconnect anything.** It clears the credential and sets `status = 'revoked'` - which
moves the worker *into the delete allow-list* while it is still connected and still running tasks. The
message told an operator, in the moment they were being refused, how to defeat the refusal. It is the
same defect as section 2 in the operator-facing register: the gate's premise was "revoked implies
disconnected", and the message was that premise spoken out loud.

Corrected at `internal/api/workers.go:602-605`, and the correction says the fact rather than merely
removing the word:

> worker is connected; disable it and wait for it to go offline before deleting. Revoking does NOT
> disconnect it - it only clears the credential, and a revoked worker may still be connected and running
> tasks

**This is the second consecutive slice where a remedy string pointed at the thing the control was
defending against.** Yesterday it was README's ceiling ladder, whose step 1 (revoke the junk) is the
engine of the loop under an active attacker, and whose `ceiling=0` option was sitting in the ladder as a
peer. That slice's retro proposed a CLAUDE.md clause, and **the clause landed yesterday**: an option
that disables a control, or that is a treadmill under attack, belongs outside the remedy ladder rather
than inside it as a peer.

#### What it means that the clause did not prevent this

Three things, and the third is the useful one.

1. **It is not a counterexample to the clause's truth.** The clause is correct and it names the right
   question. It simply was not asked here, because nothing about writing an HTTP 409 string feels like
   writing an operator remedy ladder.
2. **The clause is filed under the wrong trigger.** It was written from a README example and it reads as
   guidance about *documentation*. The site that needed it this time was a `writeError` literal ten
   lines below the gate it describes. Same defect, different register, and the register is what carries
   the lesson's discoverability.
3. **This is now the fourth recorded instance of a written lesson failing to prevent its own recurrence
   one slice later.** The pattern is stable enough to state as a property: **a prose lesson fires when
   you are already looking for it.** Writing a 409 message is a moment of *closing* a finding, not of
   auditing one, which is precisely the state the 2026-08-25 retro identified as the least-verified
   moment in a document's life.

The structural response available here is small and was not built: nothing checks that a refusal message
does not name a state that the refusal's own allow-list admits. That is a real property and it is
genuinely checkable - the allow-list is a literal `switch` a few lines above the string. It is proposed
below as a low-priority item rather than absorbed, because one site does not carry a guard, and this
project measured that price eight days ago.

> **Read every refusal string as an attacker's instruction manual.** The question is not "is this
> accurate" but "what does the reader do next, and does the gate still hold after they do it".

### 4. A mutation harness produced two FALSE SURVIVALS, and the failure is sharper than the recorded lesson

The recorded lesson is **"verify the mutation actually applied"** - born of CRLF silently defeating four
mutations in a row on this tree. This slice found the next case down: **the mutation applied cleanly, to
the wrong function.**

**Mutation 1.** The script replaced the *first* occurrence of `s.sendCancelSignals(cancels)`. That
string exists in **two** functions - `handleDisableWorker` (`internal/api/workers.go:526`) and the new
`handleDeleteWorker` (`:733`) - and `handleDisableWorker` comes first in the file. So the harness
faithfully mutated disable while reporting on delete. It then compounded the error: the run was filtered
with `-run TestDeleteWorker`, so **the disable test that would have caught the real mutation never
ran**. Two independent failures, each individually survivable, combining into a clean "survived".

Re-run against the correct site, with an applied-check that asserts the patch landed **at the expected
line number** rather than merely that the file changed, the mutation dies.

**Mutation 2 hit the identical trap** on `if n == 0 {`, and produced a **compile error**. A compile error
is not a behavioural kill - the recorded lesson "a mutation battery needs a green baseline" says exactly
this - so it was reported as an inconclusive result rather than banked as coverage.

#### Why this is a distinct lesson rather than an instance of the old one

The recorded lesson's remedy is "check that the file changed". **Both of these mutations changed the
file.** A diff-based applied-check passes on both. The property that failed is *positional*:

> **An applied-check must assert WHERE the mutation landed, not THAT something changed.** Any
> string-replace mutation of a helper call, an error return, a guard clause or a status literal is
> replacing a string that almost certainly appears more than once in a file whose whole design is
> parallel handlers. The check is a line number, or a containing-function name, and it is one extra
> assertion.

And the second half, which is the one that turned a detectable error into a false negative:

> **Never run a mutation battery behind a `-run` filter narrower than the mutation's blast radius.** The
> filter is chosen from the mutation's *intent* and the blast radius is a property of the *tree*. A
> mutation that lands outside the filter is guaranteed to survive, and the survival is indistinguishable
> from coverage.

That is now six recorded instances of a mutation harness silently lying on this repository. The count is
the argument for the harness being a committed artifact rather than an ad hoc per-session script; there
is no mutation tooling under `scripts/` today. Proposed below.

### 5. A declared-unkillable mutation was empirically refuted, and the test's name was half a lie

**M12** in the spec's battery: drop `FOR UPDATE` from `GetWorkerForUpdate`. The spec declared it
**unkillable**, reasoned that observing it needs concurrent transactions, and explicitly warned against
manufacturing a flaky concurrency test to claim a kill. The engineer ran it, watched it survive, and
**repeated the unkillable claim**.

A verify lens killed it deterministically, in one test, with no sleep and no second goroutine
(`internal/store/workers_delete_integration_test.go:101-139`):

```go
const probe = `SELECT id FROM workers WHERE id = $1 FOR UPDATE NOWAIT`
```

`NOWAIT` turns "would block" into an immediate `55P03`, so the lock is observable as an *error code*
rather than as a timing artifact. A second pool connection probes the row while the first transaction
holds it; the probe must fail with `55P03`. Then - and this is the half that makes it evidence rather
than a coincidence - **the holder rolls back and the identical probe must succeed**, so a `55P03` from
any unrelated cause cannot be mistaken for the property.

**The test's name was already asserting the property it did not test.**
`TestGetWorkerForUpdate_LocksAnExistingRowAndDistinguishesAMissingOne` had two assertions, both of which
pass identically against a plain `SELECT`. The name promised a lock; the body tested only the 404
discrimination. The shipped comment now says so in its own words: "THE LOCK HALF WAS MISSING AND THE NAME
WAS A LIE."

#### What "unkillable" claims are worth

> **"Unkillable" is a statement about the instruments the claimant reached for, not about the property.**
> Three artifacts in a row asserted it here, and each inherited it from the one above rather than
> re-deriving it. The refutation did not need a new idea; it needed one database feature nobody had
> thought to look up. So an unkillable declaration should carry the *shape* of the search that failed -
> "no deterministic single-process observation of a row lock exists" - because that is a falsifiable
> sentence, and "unkillable" is not.

Two corollaries this slice paid for:

- **The spec's warning against a flaky concurrency test was correct and was not the same question.**
  Refusing to manufacture a bad kill is right. Concluding from that refusal that no good kill exists is
  the error, and the two were fused into one claim.
- **A test whose name asserts more than its body is a latent unkillable claim.** The name is what a
  future reader treats as coverage. This is the same family as the recorded "a cadence test must assert
  the wiring": the artifact says the property is held, and nothing holds it.

### 6. A correction that travelled upward: the conductor prescribed a deny-list, the engineer refused it

For the new `attribution_cleared` count, the conductor suggested predicating on
`status NOT IN ('pending','dispatched','running')` - the rows the requeue does not rescue.

**The engineer used an allow-list on the terminal set instead**, citing CLAUDE.md's explicit rule, and
wrote the reasoning into the statement (`internal/store/query/tasks.sql:895-903`):

```sql
SELECT COUNT(*) FROM tasks
WHERE worker_id = $1 AND status IN ('done', 'failed', 'timed_out');
```

The comment states the failure asymmetry precisely: the deny-list counts the same rows today and **fails
open on the next status added** - a new non-terminal status would be reported as attribution destroyed
while its rows were in fact requeued - whereas the allow-list fails closed by under-counting. And it
names the site in `TestTasksStatusVocabularyIsExactly` so the partition is revisited rather than
silently desynchronized, joining `RecomputeJobStatus`'s terminal set as its explicit twin.

**Recording that the correction went upward is the point of this section.** The pipeline is designed to
push refutations downward, and every other section here is an instance of that. This one ran the other
way: a subordinate lane declined a conductor instruction on the strength of a written project rule, and
was right. Two observations:

- **It only worked because the rule was written where the engineer would meet it.** CLAUDE.md's
  allow-list clause is in Invariants, phrased in terms of `tasks.status` predicates, which is exactly the
  thing being written. Compare section 3, where the applicable clause existed and was filed under a
  register the author was not in.
- **The conductor's instruction was not careless, it was a *default*.** The deny-list is the natural
  spelling of "everything the requeue did not take", because that is how the requirement was phrased.
  The allow-list requires re-deriving the set from the other end. That is a real cognitive cost and it is
  why the rule has to be written down rather than reasoned to each time.

### 7. One undiagnosed red, resolved with evidence rather than assumption

One `internal/api` integration run went RED, and its identity was **lost to a `tail -6`** - the capture
truncated everything above the summary line, so the failure surfaced as a bare `FAIL` with no test name.

**What was not done: assume it was pre-existing, or assume it was the slice.** The recorded lesson
"diagnose a red gate, measure pre-existing both ways" says never to merge past a failing gate on the
strength of a story. What was done instead:

1. **A baseline run at `origin/main`** - green, 511s. So the tree without the change is healthy.
2. **Four full-capture runs on the branch** - all green. So it is not deterministic and not the diff.
3. **A lens captured the real failure**: `panic: test timed out`, with the **entire stack inside
   `go-winio`**, during testcontainers' `ContainerRemove`. It is Docker teardown on Windows hanging, not
   relay code, and not any test's assertion.

That last fact is the one that explains why it looked like a code bug: a panic during teardown renders
as a bare `FAIL` on the package with **no test name attached**, which is visually identical to a test
that failed without printing. Six runs and a stack trace to establish "the lane is flaky on this host in
a specific, attributable way", and it is now filed as
`bug-2026-08-26-integration-lane-times-out-on-docker-teardown`.

#### Why reporting it as undiagnosed rather than green was the right call

The engineer had four green runs in hand. Reporting "green" would have been *true* and would have been
the wrong report.

> **A green re-run does not retire a red run; it only bounds its frequency.** "It passed the next four
> times" is evidence about flakiness, not evidence about cause, and the two get conflated by anyone under
> pressure to close a gate. The honest report is "N green, one red, cause unestablished", and it is what
> made someone go get the stack trace instead of moving on.

The counterfactual is concrete: had this been reported green, the Docker teardown hang would be an
unfiled, unattributed intermittent that the next slice rediscovers from scratch, and the *next* genuine
red in `internal/api` would have this one as its precedent for shrugging.

Two process notes worth carrying:

- **`tail -6` on a test run is a lossy aggregate, and it lost the only attributable part.** The
  project's own recorded lesson is that a lossy aggregate discloses its loss where it is READ. Capture
  full output for a lane that can take 500s; the storage is free and the re-run is not.
- **The lens that found it did what the four re-runs could not**: it changed the instrument rather than
  repeating the measurement.

### 8. A coverage gap the lane measured rather than inferred

`internal/cli` has **zero integration coverage**. Not thin coverage - none:

- No build-tagged files in the package.
- No testcontainers usage.
- The lane finishes in **1.0s**, against 175-257s for packages that spin containers. That number is the
  measurement: a package with any real database in it cannot run in a second.

Every `internal/cli` test drives an `httptest` fake whose responses are hand-written literals. So
**`relay workers delete --yes` - the first irreversible command in the product, with no undo and no
audit log - is verified entirely against a server that does not exist.** The CLI tests prove the flag
parsing, the ambiguity refusal, the revoked-hostname fallback, and that the printed numbers come from the
right JSON keys. They prove nothing about whether those keys are the ones the server sends.

**This was measured, not inferred, and that distinction earned the item its shape.** "The CLI probably
has weak integration coverage" is a hunch that files as a vague cleanup. "Zero build-tagged files, zero
testcontainers, 1.0s versus 175-257s" is a fact with an acceptance criterion attached. Filed as
`idea-2026-08-26-cli-has-no-integration-coverage`.

The immediate residue is section 9's open item on `attribution_cleared`, which is exactly the defect
class this gap cannot see.

The design was not the expensive part. **The refutation ladder was.** The item lost three claims to the
spec, the spec lost four to the plan, and the plan and spec together lost seven to the engineer. Then
the verify fan-out found a HIGH that the conductor's own `/code-review` had looked straight at and
cleared.

## Recommended Backlog Items

Proposals only. The conductor files via `/backlog` and the human gives final accept. **Five candidates
were weighed; two are recommended and three are rejected with reasons.** Nothing here overlaps the five
items already filed this slice.

**1. `bug`: the `attribution_cleared` JSON key is unpinned across the API/CLI boundary, and a rename
breaks the CLI silently** - priority `medium`.

- **The gap is mechanical and the shape is recorded.** `internal/api`'s `deleteWorkerResponse` and
  `internal/cli`'s `deleteResp` (`workers.go:168-174`) are **two hand-written structs with independently
  spelled JSON tags**, and the CLI's only test drives an `httptest` fake whose body is a hand-written
  literal using the CLI's spellings. Rename or drop a tag server-side and **both packages stay green**
  while `relay workers delete` prints `0` for a count that is one of only four records of what was
  destroyed. This is the recorded "a guard that never sees the real producer", crossed with "a
  hand-written copy between two types needs an arity check".
- **It is not the same item as `cli-has-no-integration-coverage`**, and the item should say so. That item
  proposes a lane; this one is a specific unguarded contract that the lane would happen to cover, and it
  is also closeable *without* a lane - a compile-time or reflective check that the two structs agree on
  field count and tag spellings is cheap, whereas standing up testcontainers for `internal/cli` is not.
  If the lane lands first, close this by pointing at the test that would fail; if not, the guard stands
  alone.
- **The arity half is the part that is easy to get wrong.** A tag-by-tag comparison passes when the
  server *adds* a fifth count, which is the more likely future (this slice added `attribution_cleared` as
  a fourth after review). Acceptance should require the check to fail on an added field, not only on a
  renamed one.
- Related: `internal/api/workers.go:49-58`, `internal/cli/workers.go:165-214`,
  `internal/cli/workers_delete_test.go:25-47`.

**2. `idea`: commit a mutation harness that asserts WHERE a mutation landed and refuses a narrowing
`-run` filter** - priority `low`.

- **The count is the argument.** There is no mutation tooling under `scripts/` - every battery is an ad
  hoc per-session script, and this repository has now recorded **six** instances of one silently lying:
  four CRLF non-applications, and this slice's two patches that applied cleanly to the wrong function
  behind a filter that hid the miss (section 4).
- **The acceptance criteria are concrete**, which is what makes this an item rather than a lesson: apply
  by `file:line` and verify the changed line is the expected one; run a control mutation that must die
  before banking any survival; treat a compile error as inconclusive rather than as a kill or a survival;
  and refuse (or loudly warn on) a `-run` filter, since a mutation's blast radius is a property of the
  tree and not of the mutation's intent.
- **Priority `low` on purpose.** It is tooling, it has no user-visible effect, and every one of the six
  instances was eventually caught. It earns a row because the catching cost review rounds each time, and
  because the fix is a script rather than a design.

**Rejected, with reasons:**

- **An item for "reservations are keyed on worker uuid rather than hostname/label", as the root cause of
  the re-enrollment escape.** Rejected as **duplicative**.
  `bug-2026-08-26-re-enrolling-a-deleted-worker-escapes-its-reservations` is already filed and its
  natural remedy space includes re-keying; splitting the root cause into a second row means two items
  that must be closed together and a triage question about which is real. The keying argument belongs
  **inside** that item as a candidate remedy, and the conductor should add it there rather than filing it
  beside it. Note also that it is not obviously the right remedy: hostname-keyed reservations would break
  the moment a host is renamed, which README already documents as a supported way to rejoin as a new
  worker.
- **A guard that a refusal string may not name a state its own allow-list admits** (section 3). Rejected
  as an item, **recommended as a two-line addition to whatever touches that handler next.** There is
  exactly one site; a guard for one site is the deny-list-tripwire shape this project priced eight days
  ago and again yesterday. The property-level version - parse the `switch` above a `writeError` and
  assert no admitted status is named in the string - is thirty lines of AST work for a single call site,
  and the honest verdict is that it is not carried yet. If a second such site appears, file it then, and
  say so in the item so the count is the trigger.
- **An item for the internal-contradiction reading pass** (section 1). Rejected: no acceptance criterion
  a future slice could satisfy and close, so it would be a permanent open row. Same call as 2026-08-25,
  for the same reason. It belongs in the agent-team phase briefs and in durable memory.

## Files Most Touched

- `internal/api/workers.go:545-742` - the whole transaction, in order, with the reason for each position
  and the section 2 correction stated in place. Where the next person to touch delete lands.
- `internal/api/workers.go:708-733` - the cancel block and the comment that names the mistake it
  corrects. The headline is legible from this block alone.
- `internal/api/workers.go:656-676` - the `n == 0` branch, unreachable, load-bearing, and honest that M8
  survived.
- `internal/store/query/tasks.sql:882-903` - `CountTerminalTasksForWorker` and the allow-list argument of
  section 6.
- `internal/store/workers_delete_integration_test.go:85-139` - the refuted unkillable, the `NOWAIT`
  probe, and the control.
- `internal/cli/workers.go:165-214` - the CLI arm, the ambiguity refusal, and the unguarded JSON
  boundary of the open item below.
- `docs/superpowers/specs/2026-08-26-worker-delete.md:186-264` - the R1-R10 refutation block. Worth
  reading as a worked example of a spec refuting the item it implements.
