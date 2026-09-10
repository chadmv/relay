---
date: 2026-09-10
topic: p4-client-ceiling
item: docs/backlog/idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates.md
lane: W, slice 2 of 2
status: draft, pending human review
---

# A bounded, self-reclaiming set of exclusion-derived workspaces per stream per agent

## Status of this document

Written by the TPM agent from tree evidence, against
`D:/dev/relay/.claude/worktrees/lane-p4-ceiling` (branch `claude/lane-p4-ceiling`, at merged `main`).
The brainstorming flow's one-question-at-a-time dialogue was not available in a subagent session, so
every design question below is resolved against the tree and each answer carries the evidence that
settled it. Section 16 lists what is left for the human to decide.

**Nothing here was measured by running it.** The Bash tool was disabled in the authoring session.
Section 12 says, per claim, which are read from the tree, which are re-derived arithmetic, and which
are judgements. Section 11 lists what plan Task 0 must observe before the numbers are fixed, and one
of those observations is a **gating premise for the whole design**, not a tuning input.

Slice 1 of this lane - byte bounds on the inventory columns - has MERGED. This slice builds on it and
does not re-derive it.

## 1. The slice, in one paragraph

Two ceilings, on two different populations, protecting two different things.

**On the agent**, a per-stream ceiling on how many *exclusion-derived* workspaces one agent will hold.
It is checked in `Prepare`'s cold branch, between `reg.GetBySourceKey` and `allocateShortID`, so it
runs before `os.MkdirAll` and before `CreateStreamClient` - the two statements that mint the
artifacts. At the ceiling it does not refuse first: it evicts the least valuable unheld
exclusion-derived workspace for that stream through the existing `Provider.EvictWorkspace`, then
admits. It refuses only when nothing is evictable, which means every slot is held by a running task.
The base (no-exclusion) workspace for the stream is outside the ceiling and is never an eviction
candidate. The number is `RELAY_WORKSPACE_MAX_EXCLUSION_SETS`, default 4, hard-clamped at 64, with no
value that disables it.

**On the coordinator**, a row-count ceiling on one `applyInventory` batch. It truncates and counts; it
never refuses the batch, because refusing re-creates the inventory freeze slice 1 removed. It is a
compile-time constant with no knob, on slice 1's argument.

Plus one README paragraph edit, one README environment row, one new file in
`internal/agent/source/perforce/`, one new counter in `internal/worker`. No migration. No SQL change.
No `internal/api` change. No `cmd/` change.

## 2. What the item gets right, and the six things this spec refutes

The item was read once for content and once more asking only whether it contradicts itself,
contradicts the tree, or prescribes something that does not exist.

### Confirmed against the tree

- `SourceKey` folds the exclusion set into the workspace identity: `stream` when nothing is excluded,
  `"x1|" + 16 hex + "|" + stream` otherwise (`sourcekey.go:42-71`). True, and deliberate - the comment
  there gives the reason and it is the reason section 8 rejects sharing one client.
- `shortID` derives from `SourceKey`, and both the workspace directory and the p4 client name derive
  from `shortID`: `wsRoot = filepath.Join(p.cfg.Root, shortID)` and
  `clientName = "relay_" + hostname + "_" + shortID` (`perforce.go:300-301`). True.
- `CreateStreamClient` runs on **every** `Prepare`, warm or cold (`perforce.go:356`), and it persists
  a client spec on the shared Perforce server: `p4 client -o -S <stream>` then `p4 client -i`
  (`client.go:134-162`). True.
- The first statement that can refuse a bogus exclusion is `PathHasFiles` at `perforce.go:530`. The
  create is at `:356`. The item says "roughly 200 lines"; it is 174. Immaterial, and recorded so
  nobody re-counts it.
- **The cheap variant needs no valid depot path.** `validateSourceSpec` requires an exclusion to start
  with `//`, sit under the stream, carry no control byte, carry no rev, and be covered by exactly one
  include (`jobspec.go:559-640`). `preemptSpecs` re-checks only the coverage, structurally
  (`preempt.go:31-59`). Nothing on either side asks the depot whether the path names anything. So
  `include //s/...` plus `exclude //s/<random>` is a valid spec whose prepare fails at `:539` with
  zero bytes transferred - after the directory and the client exist. True.
- **The probe cannot be moved ahead of the client.** `PathHasFiles` runs `p4 -c <client> files -m1
  <spec>` (`client.go:221-227`) on a client-form filespec built by `toClientPath`
  (`perforce.go:830-840`). Confirmed, and with one extension the item leaves out: `ResolveHead`
  (`client.go:180-191`) has the identical shape and the identical dependency and runs on every
  *include* at `perforce.go:418`, so "check earlier" would have to move both, not one.
- The registry rows flow into `worker_workspaces` through `applyInventory`, one transaction over a
  slice with no count bound (`handler.go:2227-2254`). True.

### Refuted or corrected

1. **Acceptance criterion 3 is already satisfied at HEAD, so the README work is a different edit than
   the item asks for.** README:599 already ends: *"Each distinct exclusion set also mints its own
   workspace directory and its own persistent p4 client spec on the shared Perforce server, and both
   are created before the exclusion paths are checked, so a spec whose exclusions name nothing leaves
   a directory and a client behind before it fails."* That **is** the author-controlled case,
   documented. An implementer working from the criterion as written would add a second sentence saying
   the same thing. What is missing from that paragraph is the ceiling, the number, and what happens at
   it. Section 9 specifies the edit against the text that exists.

2. **"Checked before `allocateShortID`" names a correct location and understates the requirement.**
   `allocateShortID` mints nothing - it is a SHA-256, a base32 encoding and a collision probe over the
   registry (`perforce.go:866-877`). The artifacts are minted 44 and 58 lines later by `os.MkdirAll`
   (`:342`) and `CreateStreamClient` (`:356`). An implementer who reads the sketch as "`allocateShortID`
   is where workspaces are created" could put the check *inside* that function, where the
   found/not-found distinction is invisible and **every warm prepare would be gated too**. State the
   fence positionally: in `Prepare`'s `else` (not-found) arm, between `GetBySourceKey` (`:293`) and
   `allocateShortID` (`:298`), never inside either.

3. **"A ceiling on distinct source keys per stream", read literally, puts the base workspace inside
   the ceiling - and that hands the attacker the outcome they want.** The bare stream key is itself a
   distinct source key for that stream, and it is the workspace every non-exclusion task on that
   stream shares. A ceiling that counts it, or that may evict it, lets a job author with sixteen junk
   strings destroy a stream's shared warm workspace and force a full re-sync for everyone else. The
   population must be **exclusion-derived keys only**; the base key is neither counted nor an eviction
   candidate. Section 4 makes that a single guard rather than a convention.

4. **"The sweeper's pressure pass is off by default" is true and too weak.** `Sweeper.Run` returns
   immediately when both `MaxAge` and `MinFreeGB` are zero (`sweeper.go:72-74`), and README:611 says
   the sweeper is constructed only when one of them is set. So on a default-configured agent **nothing
   reclaims a workspace, ever** - not the age pass either. The item's inference ("reclamation is not
   guaranteed") is right; the reason is stronger than the one given, and it is decisive: the ceiling
   cannot lean on the sweeper at all. That is why section 5 makes the ceiling perform its own
   eviction rather than refuse and hope. The other half - "cannot evict a workspace that is in use" -
   is true of every eviction path including the one this slice adds, and it is a correctness property
   (`EvictWorkspace`'s holder check, `perforce.go:643-658`), not a limitation to design around.

5. **"A client created under a name that is not a function of the exclusion set" is refuted by the
   mechanism the feature is built on.** The exclusion is implemented as a have-list preempt: `p4 -c
   <client> sync -k <spec>` (`client.go:240-242`), which writes the **client's** have-list. Two
   exclusion sets sharing one client share one have-list, which is exactly the poisoning
   `SourceKey`'s own comment says the composite key exists to prevent (`sourcekey.go:14-18`). A client
   per exclusion set is a precondition of the mechanism, not an accident of naming. Section 8 records
   this as rejected with reason rather than leaving the question open.

6. **"A single transaction over an un-count-bounded slice" is true and is not the axis the item's
   headline is about, and conflating them would ship a false claim.** `applyInventory` opens with
   `ReplaceWorkerInventory`, a full DELETE (`handler.go:2230`), so the batch path does not accumulate
   rows: bounding `len(inv)` bounds one transaction's **work**, not the table's **size**. The growth
   path is `applyInventoryUpdate` (`:2260`), the per-message streaming upsert, which has no count
   bound and is **not closed by this slice**. Section 6 bounds the batch and section 10 names the
   other one as still open, because "we bounded the slice, so the table is protected" is the sentence
   this refutation exists to prevent.

## 3. The invariants this touches, and the shape they force

- **Identity-checked teardown.** The ceiling evicts, so it destroys state. It must not grow a third
  copy of the reservation discipline. `EvictWorkspace` (`perforce.go:636-684`) and `ReserveForEvict`
  (`:698-722`) are already documented as canonical twins that must be kept in sync; a third would be
  the defect. **The ceiling calls `Provider.EvictWorkspace` and implements no deletion of its own.**
  It gets the holder check, the `p.evicting` reservation, the `p4 client -d` deadline, the
  `DirtyDelete` handling and the `InvalidateWorkspace` callback for free, and it inherits the
  Prepare-side re-check that partitions the destructive window.
- **No interior pointers across locks.** The count is computed from `reg.Snapshot()`
  (`registry.go:117-123`), which returns an independent copy. Nothing in this slice holds `r.mu` or
  `p.mu` across a p4 call or a disk call, matching `EvictWorkspace`'s existing discipline.
- **End the generation before releasing the resource.** No new generation is introduced. The check
  site holds **no** workspace handle and **no** lock: it sits before the `p.mu` block at `:310` and
  well before `ws.Acquire` at `:449`. A refusal there returns with nothing to release, which is why
  it belongs there and not after the acquire.
- **Single job-spec pipeline.** The ceiling is **not** a job-spec validation and must not be pushed
  into `internal/jobspec`. It is a function of one agent's local registry, which no validator can see;
  `maxSyncEntries` and `maxSyncExclusions` bound entries per spec and cannot bound distinct keys per
  agent. Read `internal/jobspec/jobspec.go:299-340` for context; change nothing there.
- **Epoch fence / one bounded sender.** Not touched. This slice writes no `tasks.status` and no
  `task_logs` row, and adds no send site.

## 4. Where the ceiling is enforced, and on what population (design question 1)

**Decision: both sides, on two different populations, because they protect different things and
neither implies the other.**

| | Agent side | Coordinator side |
|---|---|---|
| Protects | the shared Perforce server's client-spec table; the agent's disk | one `worker_workspaces` transaction |
| Population | exclusion-derived source keys for one stream on one agent | rows in one `applyInventory` batch for one worker |
| Threat | a job author who can submit specs | an agent whose self-report is wrong or hostile |
| At the bound | evict one, then admit; refuse only if nothing is evictable | truncate and count; always commit |
| Configurable | yes, clamped, cannot be disabled | no |

**Why not the agent side alone.** The coordinator's rows come from the agent's self-report over gRPC.
A compromised or buggy agent sends whatever it likes regardless of what the agent code enforces, so
an agent-side ceiling is worth exactly zero at the coordinator. This is the same argument slice 1 made
for not mirroring its byte bound into the agent, read in the other direction.

**Why not the coordinator side alone.** A row bound protects a table. It does nothing about a p4
client spec on a shared Perforce server or a directory on the agent's disk, which are the artifacts
the item is about and which no coordinator statement can see.

### The agent-side population, precisely

Count entries `e` of `reg.Snapshot()` for which `e.SourceKey` is an exclusion-derived key **for this
stream**. That predicate is the inverse of `SourceKey`'s encoding and belongs beside it in
`sourcekey.go` so the two move together:

```
func isExclusionKeyForStream(key, stream string) bool
```

It is true when `key` starts with the version tag, ends with `"|" + stream`, **and** has exactly the
length the encoding produces (`len(tag) + 16 + 1 + len(stream)`). The length equality is what makes it
exact rather than a heuristic: `validateSourceSpec` does not forbid `|` in a stream, so a suffix test
alone would let `//a|b`'s workspaces be counted against `//a`. The version tag and the digest width
must be shared constants used by both the builder and this predicate, so an `x2` encoding cannot move
one without the other - a silently-desynchronised predicate returns false for everything and the
ceiling fails **open**, which is the dangerous direction.

**The base key is outside the ceiling by construction**, because it does not satisfy the predicate.
That is the single guard for refutation 3, and the test in section 13 that makes the base entry the
*oldest* row is what proves an LRU implementation did not quietly acquire it as a candidate.

### The exact bound this produces, stated with its slack

The ceiling is checked and the artifacts are minted several statements later, with no lock spanning
the two, so two concurrent cold prepares for two different new keys on one stream can both pass. The
overshoot is bounded by the number of tasks that agent will prepare concurrently, which is the
operator's slot configuration and not the author's. **The honest statement of the bound is therefore
`K + (concurrent prepares on that stream) - 1` per stream per agent, not `K`**, and it belongs in the
code comment. A reservation set under `p.mu` (the shape `p.evicting` uses) would close the overshoot
and costs a new lifecycle with a leak-on-early-return failure mode; it is rejected as
disproportionate to an overshoot that is already bounded by something the author does not control.
Section 15 names the condition that would change that.

## 5. What happens at the ceiling (design question 2)

**Decision: evict, then admit. Refuse only when nothing can be evicted.**

The item is right that a ceiling which refuses "turns a full keyspace into a denial of the feature for
legitimate specs", and refutation 4 makes it worse than the item states: with the sweeper absent on a
default-configured agent, a full keyspace would stay full forever and the stream's exclusion feature
would be permanently dead on that agent.

### The algorithm, in `Prepare`'s cold branch

1. If the new key is not exclusion-derived, admit. (The base workspace is never gated.)
2. Count exclusion-derived entries for this stream in the registry snapshot.
3. While the count is at or above the ceiling, take the next candidate and try to evict it:
   - **Candidates** are exclusion-derived entries for this stream that are not currently held. The
     holder test is `EvictWorkspace`'s own, so a candidate that becomes held between selection and
     eviction is refused there rather than here.
   - **Order**: entries whose `BaselineHash` is `""` first, then by `LastUsedAt` ascending.
   - Eviction is `p.EvictWorkspace(ctx, candidate.ShortID)`. An error is not fatal to the loop; the
     next candidate is tried.
   - The loop attempts at most `ceiling` evictions, so a registry left over-populated by an operator
     lowering the knob converges in one prepare rather than over many, and a pathological registry
     cannot turn one prepare into an unbounded series of `p4 client -d` calls each bounded by
     `RELAY_EVICTION_TIMEOUT`.
4. If the count is now below the ceiling, admit. Otherwise refuse.

**Why `BaselineHash == ""` goes first.** That is exactly the residue a failed bogus prepare leaves:
the cold path `Upsert`s with `BaselineHash: ""` (`perforce.go:365-372`) and the probe refusal at
`:539` returns before any sync, so the directory is empty and the delete is free. Ordering it ahead of
LRU means the attacker's junk is reclaimed before any legitimate warm workspace is touched, which is
the difference between a ceiling that protects the attacker's residue and one that protects the
operator's data. The one legitimate row that can carry an empty baseline while **unheld** is a
workspace whose agent died mid-sync; evicting that loses transferred bytes and no correctness, because
`Prepare` already re-syncs a row whose baseline is `""` and already clears the baseline when it finds
the directory empty (`:341`, `:385-388`).

**Why not refuse and let the sweeper reclaim.** Refutation 4: there may be no sweeper.

**Why not fall back to the stream's base workspace when the ceiling is full.** It would silently give
the task the files it asked to exclude, and its preempt would strip files from the shared workspace -
the poisoning `SourceKey` exists to prevent. Refuse loudly instead.

**Why not clean up the artifact on probe failure instead of capping.** This was considered as the
alternative to a ceiling and rejected. `Prepare` deliberately registers the workspace at creation so a
failure leaves something **reclaimable** rather than something invisible, and two named tests pin that
choice (`TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace`,
`TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory`, cited at `perforce.go:326-340`).
A rollback on one failure branch would contradict that design for one branch and not the others, and
it cannot be extended to all of them safely: the preempt loop also runs on a **warm** workspace whose
baseline moved, so "delete the workspace when an exclusion stops resolving" would let one typo destroy
a populated multi-terabyte workspace on a hot stream. With the count bounded, the residue is
self-reclaiming anyway - the next junk key evicts the previous one - which is the same outcome with no
new failure path.

### The refusal, and what it may say

The refusal is a prepare error. It reaches the task log on the **stderr** stream prefixed `[failed] `
(README:593), and `GET /v1/tasks/{id}/logs` is authenticated but **not** admin-only and carries no
per-owner gate (README:613). So the refusal is readable by any authenticated user.

**It must name the count and the ceiling, and never the occupants.** The occupying source keys are
other job authors' depot paths; putting them in a refusal would make this control a disclosure oracle
for another tenant's exclusion sets. The text says how many exclusion-derived workspaces this stream
holds on this agent, what the ceiling is, that all of them are currently in use, and that the task can
be retried. It renders the stream `%q` and last, per the rule `syncSummary` states
(`perforce.go:139-152`), since the stream is author-supplied.

### The reclamation line, and the disclosure split

One `progress` line per prepare that evicted, naming the **count** reclaimed and nothing else. The
evicted workspaces' short ids go to the agent's own `log.Printf`, where the sweeper already writes
them (`sweeper.go:126`) and where the reader has host access. A short id is a truncated base32
SHA-256 of another author's source key, which is weak but not plaintext; the split costs nothing and
keeps the task log free of any value derived from another tenant's spec.

`progress` is called here **before** `ws.Acquire`, holding no handle and no lock. That matters: the
existing failure branches release before they call `progress` precisely because it can park until
agent shutdown (`perforce.go:566-577`). Calling it at a point where there is nothing to hold is the
safe case, and the comment should say so rather than leaving the next reader to re-derive it.

## 6. The coordinator-side batch bound

**Decision: `applyInventory` processes at most `maxInventoryRowsPerBatch` entries. Entries past that
are dropped from the tail, counted, and the transaction commits.**

- **Truncate, never refuse the batch.** Returning an error rolls `ReplaceWorkerInventory`'s DELETE
  back with everything else, so the worker keeps its previous rows and an agent that reports the same
  over-count inventory on every registration **never updates its inventory again**. That is the freeze
  slice 1 removed, re-created on a new axis. The seam slice 1 left - a `len(inv)` guard ahead of
  `pgx.BeginTxFunc` - is the right one, used to slice rather than to return.
- **Truncation grants the agent no capability it lacks.** Which rows survive is the agent's ordering
  choice, and the whole payload was the agent's choice already.
- **A new counter, a distinct noun.** `InventoryRowRejections` counts rows refused **for content** and
  its comment says no input moves more than one of these counters; an over-count drop is a different
  question and needs its own number. Proposed reader: `InventoryBatchOverflowDrops()`, one increment
  per dropped row. Slice 1's counter and its comment stay as they are - the ordinal-free "no input
  moves more than one of these" sentence remains true.
- **The counter's doc comment carries the forgeability sentence where the number is READ**: an
  authenticated agent moves it at will, it is attributable to "some agent" and no further, and the
  remedy is to find which agent is sending an over-count inventory - **never** to raise the bound,
  which is what an agent driving the number would want.
- **Not logged.** Same rule as slice 1's refusals: fully agent-chosen and unboundedly repeatable, so a
  token spent there is one this connection's other diagnostics no longer have.
- **Not published on `GET /v1/server/counters`.** Deferred to
  `docs/backlog/idea-2026-09-10-publish-inventory-row-rejection-counter.md`, which becomes an item
  about two counters rather than one. That keeps this slice out of `internal/api` entirely.
- **No knob.** Slice 1's argument transfers without modification: an authenticated agent drives the
  counter, and the remedy an operator reaches for on a climbing counter is the attack.

**The number: 4096.** The asymmetry decides it. Too high costs one large transaction from a hostile
agent. Too low silently and permanently removes real workspaces from warm scoring for an honest agent,
with no error anywhere - the failure mode slice 1's section 10 already describes. So it is set far
past physical plausibility rather than near the honest maximum: 4096 nearly-full workspaces do not fit
on one agent's disk at any workspace size worth syncing. Note that a long-lived agent **can**
accumulate: with no sweeper by default (refutation 4), the honest population only grows, which is the
reason not to pick a tight number here.

**What currently bounds it:** `grpcServerOptions` (`cmd/relay-server/grpc_config.go:68-74`) sets
exactly three options and none is `MaxRecvMsgSize`, so grpc-go's default receive limit applies. The
entry count is therefore bounded today only by that limit divided by the minimum size of a
`WorkspaceInventoryUpdate`. **Do not write a number for this in any comment until Task 0 reads it.**

## 7. The number and its category (design question 3)

**The agent-side ceiling is an operational bound and IS env-configurable. The coordinator-side row
bound is not.**

The project's split is between bounds whose right value depends on the operator's data and bounds that
run over stored job specs. `internal/jobspec`'s constants say "DO NOT MAKE THIS ENV-CONFIGURABLE"
because `Validate` runs over stored `scheduled_jobs.job_spec` rows and a knob would make a stored
schedule's legality depend on an environment variable (`jobspec.go:294-296, 319-321, 337-339`). That
argument does not reach here: this ceiling runs on an agent, at prepare time, over live state, and
never over a stored spec. Its right value is "how many distinct exclusion sets does this studio use on
one stream, and how much disk does this agent have" - operator data, of the same kind as
`RELAY_WORKSPACE_MIN_FREE_GB` and `RELAY_EVICTION_TIMEOUT`.

**`RELAY_WORKSPACE_MAX_EXCLUSION_SETS`, default 4, hard maximum 64, no disable value.**

- **4.** README:599 already says exclusions pay when they are uniform for that stream on that agent, so
  the design intent is one exclusion-derived workspace per stream; "with and without the heavy
  subtree" is two; 4 is several times the intent. Each slot costs a **nearly full-size workspace**
  (README:599), so this number multiplies disk directly, and that is the reason not to default it
  higher. It is a judgement, not a measurement, and section 11 names what would move it.
- **A hard maximum of 64, clamped with a warning.** The knob only ever loosens a control, so it needs a
  ceiling of its own. 64 is well past any legitimate use and still finite.
- **There is no value that disables it, and `0` is not one.** This is the trap that makes the rule
  worth writing down: in this same README table, `RELAY_WORKSPACE_MAX_AGE` and
  `RELAY_WORKSPACE_MIN_FREE_GB` both mean "disabled" at zero, so an operator will assume it here.
  `0`, a negative, and an unparseable value all resolve to the **default**, and each logs a line
  saying the value was not used and that this ceiling cannot be switched off. An option that disables
  a control does not belong in its remedy ladder as a peer, and the ladder is part of the
  advertisement.
- **The remedy ladder in README**, in order: find which job specs are minting distinct exclusion sets
  on that stream (the ceiling refuses only when every slot is *in use*, so the first question is what
  is using them); make the fleet's exclusion sets uniform for that stream; raise the number, at the
  cost of one more nearly-full workspace per step. No fourth rung.

**Where the value is resolved: inside the package, in `New`, through a `getenv func(string) string`
seam** - the shape `resolveGRPCBounds` uses (`cmd/relay-server/grpc_config.go:334`), with the warnings
as ordinary return values so they are testable without capturing a logger. Not a `Config` field: a
`Config` field defaults to the zero value, and a zero value here must not mean "unlimited", so a
`main.go` that forgets to wire it would silently disable a safety control. Resolving in-package makes
the unwired case fail **closed**. Precedent for reading the environment inside this package is
`evictTimeout` (`sweeper.go:32-43`). Not a package-level `var` like that one, though: a per-`Provider`
unexported field needs no global mutation in tests, no `t.Cleanup` restore and no ban on `t.Parallel`.

**No unconditional startup line** naming the effective ceiling, because emitting one requires a
`cmd/relay-agent/main.go` edit and this slice's fence excludes it. Section 15 proposes that as a small
follow-up; the warning lines above still fire when an operator's value is not used verbatim.

## 8. Must the client exist before the exclusion set is known to resolve (design question 4)

**Yes. The ceiling is the practical answer, and this section says so plainly rather than leaving the
question open.**

Three routes were examined and rejected, and the reasons are the design:

1. **Move the probe ahead of `CreateStreamClient`.** Rejected, on the item's own argument, confirmed
   in the tree: `PathHasFiles` takes a client-form filespec and the case it exists to catch - a stream
   whose view remaps the subtree, reported as "file(s) not in client view" - is detectable only
   *through* a client's view (`client.go:206-227`, `perforce.go:511-521`). Extension: `ResolveHead` has
   the same dependency for every include, so this is not one call to move.
2. **A client whose name is not a function of the exclusion set.** Rejected: the preempt writes the
   **client's** have-list (`client.go:240-242`), so one client shared across exclusion sets is one
   have-list shared across them, which is the poisoning the composite key exists to prevent
   (`sourcekey.go:14-18`). A client per exclusion set is a precondition of the mechanism.
3. **Roll back the cold mint when the probe fails.** Rejected in section 5: it contradicts a tested
   design decision on one branch only, and it cannot be generalised to the warm-baseline-moved case
   without letting one typo destroy a populated workspace.

**One route is sound, narrow, and deliberately deferred.** `PathHasFiles` takes no cwd and needs no
local root - it is a depot query mapped through a view - so when a client for this stream **already
exists on this agent** (the base workspace, or another exclusion-derived one), the exclusion could be
probed through *that* client before minting a new one, making a bogus exclusion cost nothing in the
warm case. It is rejected for this slice because it closes only the warm case (an attacker picks a
stream this agent has not touched), it adds a second probe path whose failure semantics differ from
the first, and its premise - that a sibling client's view answers the same question - is an assumption
about p4 that this session cannot test. Section 15 files it as its own item with that premise named as
the thing to measure first.

## 9. Documentation (design question 5)

Sibling lanes are running in this batch and two of them have recently edited rows next to the prose
below. **The exact prose this slice touches, and nothing else:**

1. **README:599, the "Exclusions change the workspace identity." paragraph.** Its final sentence
   already states the author-controlled minting (refutation 1) and **is not rewritten**. Appended
   after it: how many exclusion-derived workspaces one stream may hold on one agent, that the base
   workspace is outside that count, that at the ceiling the agent reclaims the least valuable unheld
   one rather than refusing, that a prepare is refused only when every slot is in use, and the
   variable name. This is the item's third acceptance criterion, corrected to what the paragraph
   actually still lacks.
2. **README:605-609, the "Eviction." paragraph.** One sentence: workspaces are also reclaimed outside
   the sweeper, by the exclusion-set ceiling at prepare time. Without it an operator reads that
   paragraph as the complete list of what deletes a workspace, and after this slice it is not.
   "Active workspaces (held by a running task) are never evicted" stays true and unedited - the
   ceiling's candidates exclude held entries.
3. **The agent environment-variable table, appended after the `RELAY_WORKSPACE_CLOBBER` row
   (README:510).** Appended rather than grouped with the other `RELAY_WORKSPACE_*` rows to keep the
   diff off lines a sibling lane may be editing. The row carries: the population it bounds (**per
   stream, per agent, exclusion-derived workspaces only**, since the name alone reads as a fleet or
   agent total), the default, the hard maximum, that **`0` does not disable it**, and the remedy
   ladder from section 7.

**Explicitly NOT touched**, because siblings just edited them: the `stream` row and the `sync` row of
the source-field table (README:588-589).

**No README change for the coordinator-side row bound.** Slice 1 documented its byte bound because an
honest author can reach it (a long stream plus one exclusion). No honest agent reaches 4096
workspaces, so documenting it would describe a state no operator can produce, and the argument belongs
in the code comment instead. **The condition that changes this**, stated so it can be checked later:
if an honest agent could plausibly hold that many workspaces, the bound needs a README sentence with
the same shape as slice 1's.

**Code comments.** Each states a hazard the code cannot show, and none carries a date, a count of code
elsewhere, a uniqueness claim about other code, or measurement provenance.

- The ceiling function: that the artifacts are minted downstream of this point and which statements
  they are; that the base key is deliberately outside the population and what happens if it is not;
  that the real bound includes the concurrency slack (section 4); that eviction goes through
  `EvictWorkspace` because a third copy of the reservation discipline is the defect; that `progress`
  is called here because nothing is held here.
- The predicate in `sourcekey.go`: that it is the inverse of the encoding above it, that the length
  equality is what makes it exact against a stream containing `|`, and that a desynchronised version
  tag fails **open**.
- The resolver: that `0` does not disable, and why that is worth a line given its neighbours.
- The new counter's field and reader: the forgeability sentence, at the place the number is read.
- `applyInventory`'s existing comment gains the truncation arm. Re-verify the surviving sentences
  while editing rather than assuming the untouched half is still true.

## 10. What this does NOT close

Enumerated, because slice 1 and the reconcile slice both shipped with their limits stated and that is
the standard here.

1. **The transient client spec.** Every cold prepare still creates a client on the shared Perforce
   server before anything can refuse the exclusions. The ceiling bounds how many **persist**, not how
   many are **created over time**. An author submitting junk specs in a loop causes an unbounded
   number of create/delete pairs on the shared server; the flow is bounded only by the dispatch rate,
   and per user by `RELAY_JOB_SUBMIT_RATE_LIMIT`. Stock is bounded; flow is not.
2. **Thrash of a legitimate exclusion-derived workspace.** Once an author's distinct sets exceed the
   ceiling, a legitimate composite workspace on that stream can be evicted and later re-synced. The
   `BaselineHash == ""` ordering makes junk the preferred victim and the base workspace is never a
   victim, but a fleet using more than `K` legitimate exclusion sets per stream will thrash. That is
   what the knob is for.
3. **Streams.** An author can still cause one workspace per **real** stream, exactly as before
   exclusions existed. This restores the old bound times a constant; it does not improve on it. If the
   same human is also a p4 user who can create streams, the bound is back in their hands - a different
   principal on paper, often the same person.
4. **The coordinator table's size.** Section 6 and refutation 6: `applyInventoryUpdate`'s per-message
   upsert path can add rows without limit over time and is not bounded here. Only the batch is.
5. **Fleet width.** The ceiling is per agent. An author reaching N agents causes N times the
   artifacts. Nothing in this slice is fleet-aware, and nothing on the coordinator counts an author's
   total workspace footprint.
6. **Disk.** This bounds the **number** of workspaces, not their size. `RELAY_WORKSPACE_MIN_FREE_GB`
   remains the only size control and the sweeper that applies it is absent by default (refutation 4).
7. **Registry loss.** The count is over the agent's `.relay-registry.json`. Deleting that file orphans
   every client on the Perforce server and resets the count to zero. Pre-existing - the sweeper is
   registry-driven too - and not made worse, but it is the way the ceiling is bypassed.
8. **Both counters remain unpublished.** `GET /v1/server/counters` shows neither the content
   rejections nor the new overflow drops.
9. **Nothing validates that an exclusion path names anything, at submission.** The coordinator cannot
   ask a Perforce server it does not talk to. The refusal stays downstream, at prepare time, on the
   agent that has a ticket.

## 11. Plan Task 0

The TPM session had no Bash. None of the following was measured here.

1. **GATING: does `p4 client -o -S <stream>` fail for a stream that does not exist?** The whole
   framing - "it is a bound moved, not removed", and section 4's claim that the per-stream population
   is bounded by something the author cannot invent - rests on this. Measure it in the `p4d`
   testcontainer lane. **If it succeeds** (creating a client for a non-existent stream), the author
   can invent streams as freely as exclusion sets, the per-stream ceiling bounds nothing in aggregate,
   and the design must be revisited before implementation, not after. Turn the observation into a
   permanent guard in the same step - see section 13, test P1.
2. **Is `internal/agent/source/perforce` in a lane CI runs?** CLAUDE.md says `internal/agent` is in
   `test-pg-integration`'s package list. Read the actual `go test` invocation in the `Makefile`
   target: if it is `./internal/agent/...` the p4d-backed integration tests are in CI and an
   integration guard here needs no written excuse; if it is `./internal/agent` alone, test P1 needs
   the third branch's sentence naming what would have to exist for it to run.
3. **The current text and line numbers of README:588-613**, at the tip of `main` at implementation
   time. Sibling lanes have edited neighbouring rows in this batch; re-derive the anchors rather than
   trusting the numbers in section 9.
4. **grpc-go's effective default receive-message limit** for this server, since `grpcServerOptions`
   sets none. Needed before any comment says what bounds `len(inv)` today. Report the number with the
   grpc-go version.
5. **The highest existing migration number**, to confirm what section 14 asserts: this slice needs
   **no** migration. Expected to be a no-op check.
6. **Can `internal/worker`'s untagged `fakePool`/`fakeTx` fixture drive a 4098-entry batch?** If yes,
   the coordinator tests land in the default lane. Slice 1 recorded that fixture as untagged and
   statement-recording, so this is expected to be yes.
7. **How an existing test makes a workspace un-evictable** - read `provider_evict_test.go` and
   `sweeper_claim_test.go` for the holder seam. Test A2 (the refusal) needs it and this session could
   not verify which seam is available.
8. **Does any existing perforce test assume a cold prepare on a new exclusion set always succeeds
   regardless of registry population?** Any fixture pre-populating several composite entries for one
   stream would go red at a default of 4. Grep before implementing, not after.

**What Task 0 can and cannot change.** Item 1 can invalidate the design. Items 2-8 can move a number,
a lane, or a test mechanic. None of them can move the enforcement points, the evict-then-admit
decision, the no-disable rule, or the two-counters decision - none of those depend on a measurement.

## 12. Provenance

**Read directly from the tree in this session** (no execution): every `file:line` citation in sections
2-9. The load-bearing ones: `perforce.go:290-301` (identity and derivation), `:293-299` (the
found/not-found branch this slice fences), `:342` and `:356` (the two minting statements), `:365-372`
(the cold `Upsert` with an empty baseline), `:511-543` (the probe and its three refusals), `:636-722`
(the eviction twins); `sourcekey.go:42-71`; `sweeper.go:72-74` (the sweeper is inert when both knobs
are zero), `:126`, `:161-200`; `client.go:134-162`, `:180-191`, `:206-227`, `:240-242`;
`preempt.go:31-59`; `registry.go:88-123`; `handler.go:2227-2280` and `:433-498`;
`inventory_params.go:15-38`; `jobspec.go:294-340`, `:559-640`; `cmd/relay-server/grpc_config.go:68-74`;
README `:502-510`, `:588-613`.

**Re-derived arithmetic:** the composite key's fixed overhead is 20 bytes (`"x1|"` 3 + 16 hex + `"|"`
1), so the exact-length predicate in section 4 is `len(stream) + 20`. The gap between the create and
the first possible refusal is 174 lines (`:356` to `:530`).

**Judgements, with the reasoning in the section named:** 4 and 64 (section 7); 4096 (section 6);
evict-then-admit and the `BaselineHash == ""` ordering (section 5); both enforcement points (section
4); accepting the concurrency overshoot (section 4).

**Not verified in this session:** that `p4 client -o -S` fails for a non-existent stream (Task 0,
item 1); that a sibling client's view answers the same question as the variant client's (section 8,
deferred route); grpc-go's default receive limit (Task 0, item 4).

## 13. Testing

### Agent side, default lane (untagged; `Config.Client` fake runner, registry on a `t.TempDir` root)

**A1 - the ceiling admits by evicting.** Registry pre-populated with `K` exclusion-derived entries for
one stream. Prepare a `K+1`th distinct exclusion set. Assert: exactly one `client -d` was issued, for
the expected victim; the new key's `client -i` **was** issued; the registry's exclusion-derived count
for that stream is `K`.

**A2 - the ceiling refuses, and the refusal is the assertion.** Same setup with every candidate
un-evictable. Assert `Prepare` returned an error **and that no `client -i` for the new name appears in
the recorded argv**. The success path alone is what an absent control also produces; the discriminator
is the absence of the mint. Check by removing the control entirely and confirming the argv appears.

**A3 - the base workspace is never a candidate.** `K` exclusion-derived entries plus the base entry,
with the **base entry as the oldest and with a non-empty baseline** so a pure LRU implementation picks
it. Assert the base entry survives and a composite was evicted. The decoy goes before its target.

**A4 - a warm prepare at the ceiling is not gated.** Registry at or over `K`; prepare a spec whose key
is already present. Assert no eviction, no refusal, and that the prepare proceeds. This is the
mutation "hoist the check above the found/not-found branch".

**A5 - an unsynced composite is evicted before an older synced one.** Two candidates: one with
`BaselineHash == ""` and a **newer** `LastUsedAt`, one with a real baseline and an older one. Assert
the unsynced one is the victim. Discriminates against pure LRU.

**A6 - the predicate is exact against a stream containing a pipe.** In `sourcekey_test.go`: a
composite key for `//a|b` is not counted for `//a`, and round-trips for `//a|b`. Discriminates a
suffix-only implementation.

**A7 - the resolver.** Table: unset yields the default and no warning; `"8"` yields 8 and no warning;
`"0"` yields the default **and a warning that says it does not disable**; `"-1"`, `"abc"` yield the
default with a warning; `"1000"` yields 64 with a warning. Assert the warning **text** for the `0`
row, since that row exists to contradict the neighbouring variables' meaning.

**A8 - the refusal discloses no occupant.** Occupy the slots with keys built from a distinctive
literal that appears nowhere in the expected message, and assert the refusal does not contain it. The
injected literal must not be one the environment could produce on its own.

### Agent side, p4d integration lane

**P1 - `TestClient_CreateStreamClient_RefusesAStreamThatDoesNotExist`.** This is Task 0 item 1 turned
into a permanent guard, and it is the only test that pins the premise the per-stream population rests
on: that an author cannot invent streams. Without it, the design's central claim is a sentence in a
comment and ambient p4 behaviour. If Task 0 item 2 finds this lane does not run in CI, the test's own
comment names what would have to exist for it to run.

### Coordinator side, default lane (`fakePool` / `fakeTx`)

**B1 - an over-count batch truncates and commits.** `max + 2` entries. Assert
`ReplaceWorkerInventory` was issued; exactly `max` upserts; `commits == 1`; the new counter moved by
exactly 2; the returned error is nil.

**B2 - the control.** A batch of exactly `max`: every row upserted, the new counter moves by **zero**.
Without this half, an increment in the wrong branch passes B1.

**B3 - the two counters are distinct nouns.** One batch that is over-count only, one that carries one
content-refused row only. Each moves its own counter by the expected amount and the other by zero.

### Mutation battery

| Mutation | Killed by |
|---|---|
| The ceiling check deleted | A1 (no `client -d`), A2 (the mint appears) |
| The check hoisted above the found/not-found branch | A4 |
| The base key included in the population | A3 |
| The base key made an eviction candidate | A3 |
| The suffix test written without the length equality | A6 |
| `BaselineHash == ""` ordering removed | A5 |
| The eviction loop capped at zero attempts | A1 |
| Refusal returned before attempting any eviction | A1 |
| The refusal branch removed (admit anyway when nothing is evictable) | A2 |
| `0` treated as "unlimited" | A7 |
| The clamp removed | A7 |
| The occupants' keys added to the refusal text | A8 |
| Truncation replaced with an error return | B1 (`commits == 0`) |
| The overflow increment placed on the accept path | B2 |
| The overflow drop folded into `InventoryRowRejections` | B3 |

Run each with a control that should die, verify the mutation actually applied before recording a
survivor, and never revert a mutation with `git checkout --` - restore from a copy.

## 14. Scope fence

**Owned:** `internal/agent/source/perforce/` (one new file for the ceiling plus its test; small edits
to `perforce.go`'s cold branch and to `sourcekey.go`), `internal/worker/` (`handler.go`'s
`applyInventory` and one counter), README's source-workspaces prose per section 9.

**Not touched:**

- `internal/schedrunner/` and `cmd/relay-server/main.go`'s boot region - a sibling lane is specifying a
  wall-clock deadline on the boot sweep there.
- `internal/jobspec/` - read for context (`maxSyncEntries = 512` bounds entries per spec, not distinct
  keys per agent, so it does not close this). Changed in no way.
- `internal/api/` - the counters stay unpublished, on slice 1's precedent.
- `cmd/relay-agent/main.go` - section 7 resolves the variable inside the package, which is what keeps
  this slice inside its fence and what makes an unwired build fail closed.
- **No migration and no SQL.** No statement in `internal/store/query/` changes, so `make generate`
  produces no diff and the CRLF hazard does not arise.
- `Provider.EvictWorkspace`, `ReserveForEvict`, `Sweeper.evict` - **called**, never modified and never
  copied.
- The `stream` and `sync` rows of README's source-field table.

## 15. Backlog items this spec proposes, for the human to accept

None are filed by this spec.

1. **Probe an exclusion through an existing sibling client before minting a new one.** Section 8's
   deferred route. It closes the cheap variant in the warm case at source rather than capping it. File
   with its premise named as the first thing to measure: whether a sibling client's view answers the
   same question as the variant client's.
2. **Publish the two `internal/worker` inventory counters on `GET /v1/server/counters`.** Update
   `idea-2026-09-10-publish-inventory-row-rejection-counter.md` to cover both, each as its own
   section, with the forgeability sentence stated where each number is read.
3. **Bound the per-worker row total on `applyInventoryUpdate`'s streaming path.** Section 10 item 4.
   The natural shape is a count predicate inside the upsert statement so it costs no extra round trip;
   that is an `internal/store/query/` change and a `make generate`, which is why it is not in this
   slice.
4. **An unconditional agent startup line naming the effective workspace bounds**, in the shape of
   `grpcBoundsLine`. Section 7 explains why this slice cannot emit one.
5. **Reserve a cold admit under `p.mu` to close the concurrency overshoot.** Section 4 accepts the
   overshoot because it is bounded by the operator's slot count. File it if that stops being true -
   specifically, if per-agent concurrency ever becomes something a job author influences.
6. **Rename `allocateShortID`'s first parameter.** It is declared `stream string`
   (`perforce.go:866`) and every caller passes a **source key**. One word, no behaviour, and this
   slice's new code sits directly above it. Optional; not required scope.

## 16. Open questions for the human

1. **Default 4 (section 7).** It is a judgement about how many distinct exclusion sets a real fleet
   uses per stream, anchored on README's own "exclusions pay when they are uniform". If the studio's
   real answer is 6, say so now.
2. **Evict-then-admit (section 5).** It means a job author's junk specs can, at the ceiling, cause a
   legitimate exclusion-derived workspace to be re-synced. The alternative is refusing, which the item
   rules out and refutation 4 makes worse. Confirm the trade, or narrow it - for example by making
   only `BaselineHash == ""` entries evictable, which removes the thrash and reintroduces the denial
   once the slots hold real workspaces.
3. **The coordinator half's inclusion (section 4).** It is a different threat model from the item's
   headline. It is here because slice 1 left the seam and it costs a handful of lines. Cut it into its
   own item if this slice should stay purely agent-side.
4. **4096 (section 6).** Deliberately far past plausibility because the too-low failure is silent and
   permanent. Confirm the direction of that asymmetry.
5. **Task 0 item 1 is gating.** If `p4 client -o -S` turns out to succeed for a non-existent stream,
   this design does not hold and should come back for re-scoping rather than be adjusted in flight.
