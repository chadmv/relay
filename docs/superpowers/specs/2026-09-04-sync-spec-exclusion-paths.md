# Sync-spec exclusion paths

Date: 2026-09-04
Item: `docs/backlog/idea-2026-09-03-sync-spec-exclusion-paths-design.md`
Status: design, ready for a plan

## 1. Problem

A `source.sync` entry can only ADD a path. On a real stream the subtree an operator wants to leave
out is often the difference between a workspace that fits the volume and one that does not, and
today the only way to express that is to enumerate every sibling of the unwanted subtree as its own
include entry.

That enumeration is expressible today, so this feature buys no new REACHABILITY. Be honest about
what it does buy, because the size of the win decides how much mechanism is worth spending:

- **Stability under stream growth.** An enumerated include set silently stops covering a top-level
  directory added to the stream next month. An exclusion keeps covering everything else.
- **Expressiveness proportional to depth.** Excluding `//s/x/Content/Movies/...` by enumeration
  means listing every sibling of `Movies` inside `Content`, and doing it again one level up if the
  next exclusion is a sibling of `Content`.
- **Cost per include entry.** Every `#head` entry is one `ResolveHead` round trip inside `Prepare`
  and one more argv element on the sync. Enumeration multiplies both.

The counter-argument is real and is recorded here rather than dismissed: an enumerated include set
fails CLOSED when the stream grows (a new directory is simply not synced), and an exclusion set
fails OPEN (a new directory is synced, and the volume fills). Neither is obviously safer; the
exclusion is chosen because the operator can name the one huge subtree they know about, and cannot
name the siblings they have not seen yet.

## 2. What the tree actually says

The backlog item's proposal was checked question by question against this worktree. Five of its
seven premises needed correcting. These corrections are the design, not commentary on it.

| Item's claim | What the tree says | Symbol checked |
| --- | --- | --- |
| "If exclusions live in `pf.Sync`, `BaselineHash` already covers them; confirm." | **False.** `BaselineHash` reads exactly `e.Path` and `e.Rev` from each entry and nothing else. An `Exclude` field on `SyncEntry` hashes identically to its absence, so two differently-excluded tasks would look like the same baseline and one would skip the other's sync. | `BaselineHash` |
| "Confirm the dispatcher's warm-workspace bias keys on the new `SourceKey`." | **It keys on nothing shared.** `selectWorker` compares `ws.SourceKey == taskSrc.Stream`, a literal comparison against the stream string, and the candidate set is built by appending `s.Stream` into `streamsByType`. There is no server-side source-key function to update; one has to be created, exactly as `BaselineHashFromAPISpec` was created for the other half of this contract. | `selectWorker`, `Dispatcher.dispatch`, `BaselineHashFromAPISpec` |
| "An explicit `exclude: true` is self-describing for the SPA's job builder and the Python SDK's models." | **The SPA builder does not exist yet** (`feature-2026-09-03-perforce-source-builder-in-the-new-job-builder` is open, and `validateSpecText` names `source` only as a rule it deliberately does not implement), so that half of the argument is prospective. **The Python SDK is worse than neutral:** `Sync` carries `model_config = ConfigDict(extra="forbid")`, so an `exclude` key is a hard SDK-side rejection until the SDK ships the field, where a `-` prefix would pass through its `path: str` untouched. | `validateSpecText`, `Sync` in `python/src/relay/models.py` |
| "One `[sync] EXCLUDE <path>` line per exclusion, and a `WARN` line on a preempt failure, as the fork does." | **The WARN is refused.** A warning inside a multi-hour sync's log is read after the volume fills, which is the failure the feature exists to prevent. A preempt that did not take effect must fail the prepare. The log line's SHAPE also needs changing: `syncSummary` documents that the only input-derived field is placed LAST and rendered with `%q` precisely so a forged path cannot spell a convincing line, and a bare `EXCLUDE <path>` line makes an input-derived value the entire line. | `syncSummary` |
| "Once shipped, extend the out-of-disk remedy to mention exclusions." | **Refused as written**, see section 9. The shipped message already says "reduce the sync paths", and naming exclusions there advertises an action that, under the mechanism chosen here, can INCREASE total disk use on a shared agent. | `classifyP4Error` |

Two premises hold up and are adopted:

- The fork's stated reason for "an exclusion must follow an include" is a fossil (the have-list
  mechanism never passes exclusionary filespecs on a command line). The rule is kept and given a
  real reason in section 5.
- Folding the exclusion set into the workspace identity is the right shape. Section 3 shows it is
  not a choice between mechanisms at all: it is a precondition that every candidate mechanism needs.

## 3. Decision 1: mechanism

### 3.1 Key-splitting is a precondition, not an alternative

The item frames "fold the exclusion set into `SourceKey`" as one mechanism among three. It is not.
Every candidate mechanism writes per-task state into an object that `Prepare` shares across all
tasks on a stream:

- A have-list preempt writes the CLIENT's have-list.
- A view-based exclusion writes the CLIENT's spec, and `CreateStreamClient` runs on EVERY `Prepare`,
  not only the cold one.

`Config.Clobber`'s own comment already records the consequence of the second: a per-task value would
let two concurrently admitted `ModeShared` tasks flip one shared spec against each other, one of
them possibly mid-sync. The first is the have-list poisoning the item describes. So the exclusion
set enters the workspace identity under either mechanism, and the real question is what happens
INSIDE a workspace whose exclusion set is now constant.

### 3.2 The mechanism inside the workspace: have-list preempt

Chosen: before the real sync, run `p4 -c <client> sync -k <client-path-of-each-exclusion><rev>`,
where `<rev>` is the RESOLVED revision of the include entry that covers that exclusion. Then run the
sync exactly as today. p4 skips every file whose have-revision already matches the target, so the
excluded subtree is never transferred.

Rejected alternative A: **materialise a non-stream client whose `View` carries exclusion lines.**
This is the technically cleaner shape - the have-list stays honest, `p4 clean` and `p4 reconcile`
behave, a task command that runs its own `p4 sync` also respects the exclusion, and no revision
matching is needed. It is rejected because it rests on two unverified premises about p4 that nothing
in this repository has ever captured: that `p4 client -o -S <stream>` emits a `View` that is
self-contained for a virtual or `import+` remap stream, and that dropping the `Stream:` field while
keeping that view yields a client the rest of relay still works against. `CreateStreamClient` reads
none of the view today - it sets `Root`, `Host` and `Owner` and removes `AltRoots`. Giving up the
stream binding also gives up exactly what the client-path slice just made load-bearing. If the
capture in section 11 shows both premises hold, this becomes the better successor; it is not the
thing to build first.

Rejected alternative B: **discover the include set** (list the stream's directories, subtract the
exclusions, sync the remainder). It converts a declarative exclusion into a discovered include set
that changes under the operator between two runs of the same spec, it needs a directory listing at
every level of a deep exclusion, and it needs a p4 subcommand relay does not currently invoke.

Rejected alternative C: **per-task have-list reversal on identity change.** The item already names
it as fragile. It is worse than fragile: reversing a `-k` means fetching the subtree, which is the
transfer the exclusion existed to avoid, so the "cheap" path becomes the expensive one exactly when
two tenants share a stream.

### 3.3 What a task with a DIFFERENT exclusion set sees

It sees a different workspace: a different `SourceKey`, therefore a different `short_id`, therefore
a different directory under `RELAY_WORKSPACE_ROOT` and a different p4 client. It shares nothing with
the first task except the depot. Specifically:

- A task with NO exclusions on a stream where another task excludes a subtree gets a full workspace
  containing that subtree. It never observes a workspace missing files it asked for. This is the
  item's acceptance criterion, and section 11 states it as a test.
- Two tasks whose exclusion sets differ only in ORDER or contain duplicates share one workspace, by
  canonicalisation (sort, dedupe).
- `Workspace.tryAdmit`'s three-rule arbitration is untouched. It only ever compares tasks that
  already reached the same workspace, and inside one workspace the exclusion set is constant.

### 3.4 The disk trade, quantified

This is the strongest argument against the whole design and it is stated in full.

Let `S` be the synced size of a stream and `X` the size of an excluded subtree. Today one agent
holds one workspace of size `S` per stream. After this change, an agent that runs `k` distinct
exclusion sets for one stream holds `k` workspaces totalling `sum(S - X_i)`.

With `k = 2` (one task family excluding `X`, one excluding nothing) that is `2S - X`. Since at least
one include must survive an exclusion, `X < S` always, so **a mixed-exclusion stream on one agent
always costs MORE disk than today.** The exclusion pays only when it is uniform for that stream on
that agent - which, per the item, is precisely the fork's deployment shape and precisely why the
fork never saw the poisoning.

The excess does not overflow the volume, because the sweeper's pressure pass (`SweepOnce`,
`RELAY_WORKSPACE_MIN_FREE_GB`) evicts oldest-first until free disk is met. It converts into eviction
churn instead: each evicted warm workspace costs a full re-sync when its stream is next scheduled
there. **That cost is UNQUANTIFIED and cannot be quantified from this repository**: nothing records
a workspace's size (`InventoryEntry` carries `SourceKey`, `ShortID`, `BaselineHash`, `LastUsedAt`
and no bytes), and nothing counts evictions (`SweepOnce` returns the evicted ids to a caller that
discards them in `Sweeper.Run`). A backlog item for that instrumentation is recommended in
section 13; it is the measurement that would tell an operator whether exclusions are helping.

There is no admission control anywhere that considers workspace COUNT, and this design does not add
one. The dispatcher's warm bias (section 6) is the only thing that pushes a differently-excluded
task toward the agent that already holds its variant, and it is a soft bias by design.

## 4. Decision 2: spec surface

Chosen: an explicit boolean field on `SyncEntry`.

```json
{ "path": "//depot/film-x/main/Content/Movies/...", "exclude": true }
```

- `jobspec.SyncEntry` gains an `Exclude bool` field, with the json tag `exclude,omitempty`.
- `relayv1.SyncEntry` gains `bool exclude = 3;`.
- `python/src/relay/models.py`'s `Sync` gains `exclude: bool = False`.

Reasons, in the order they carried weight:

1. **The path string keeps one meaning.** `toClientPath` refuses a path that is not under the
   stream, and its doc comment records that refusing is the point. A `-` prefix makes every path
   consumer strip before comparing, and there are four of them (`validateSourceSpec`'s containment
   check, `toClientPath`, `BaselineHash`, `Request.SyncPaths` via `PathPrefixOverlap`). A missed
   strip in any one of them is a silent wrong answer, not an error.
2. **Every typed consumer gets a typed field.** The proto, the Go structs and the Python models all
   describe the shape; a prefix convention is describable only in prose.
3. **The rev field can be absent honestly.** An exclusion carries no revision (section 5), which is
   expressible on a record and not on a string.

Against, and this is a genuine cost the item does not weigh: **the field fails OPEN on version skew
and the prefix fails CLOSED.** `readJSON` does not set `DisallowUnknownFields`, so an older
relay-server accepts a spec carrying `exclude` and silently drops it, then syncs the whole stream. A
`-`-prefixed path on an older server is refused by `validateSourceSpec` with "must start with //".
The same asymmetry exists agent-side: protobuf drops an unknown field, so an older relay-agent syncs
everything, where a `-` prefix would reach `toClientPath` and fail the prepare loudly with the exact
message that symbol already emits. Section 8 buys that property back explicitly rather than
accepting a silent full sync.

## 5. Decision 3: validation in `validateSourceSpec`

All ingestion goes through `jobspec.Validate`, so these rules bind REST, CLI, MCP, schedrunner and
the SPA. Every rule below is stated as a refusal, with the reason it refuses.

1. **An exclusion is still a path under the stream.** No new code: the existing per-entry check
   (`e.Path != s.Stream && e.Path != s.Stream+"/..." && !strings.HasPrefix(e.Path, s.Stream+"/")`)
   already runs for every entry and is correct for exclusions unchanged.
2. **An exclusion carries no revision.** This is a CARVE-OUT of an existing mandatory check, not an
   addition: today an empty `rev` matches none of `revHeadRe`, `revCLRe`, `revLabelRe`, `revNumRe`
   and is refused. The rev regexp check becomes conditional on `!e.Exclude`, and a non-empty `rev`
   on an excluded entry is refused with a message saying the revision comes from the include that
   covers it.
3. **Every exclusion is covered by exactly one include, at one revision.** This replaces the fork's
   fossil rule ("an exclusion must follow an include", justified by p4's left-to-right argv
   handling, which this mechanism never uses). The real reason is that **the preempt's revision is
   defined by the covering include and by nothing else.** An uncovered exclusion excludes nothing
   and is a typo that would otherwise do nothing silently. Two covering includes at different revs
   make the preempt revision ambiguous, and a preempt at the wrong revision does not merely fail to
   exclude - p4 syncs a file whose have-revision differs from the target in EITHER direction, so a
   preempt at a newer revision than the include causes the entire excluded subtree to be fetched
   backwards. Refuse both cases. This rule implies the item's weaker "at least one include exists".
4. **An exclusion must not swallow an include.** An exclusion equal to, or a prefix of, its covering
   include leaves that include with nothing to sync. Refuse; the operator meant to delete the
   include.
5. **At most 16 exclusions per source spec.** Each exclusion is one additional p4 subprocess per
   sync, on the same per-entry axis that
   `bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded` already flags for
   `unshelves`, and one additional operator-facing log line. A realistic exclusion list is a handful
   of named heavy subtrees; 16 is several times that. Following the doctrine stated on `maxRetries`
   and repeated on every other bound in `jobspec.go`, this bound is NOT env-configurable: `Validate`
   runs over stored `scheduled_jobs.job_spec` rows on paths that write `last_error` and that decide
   a PATCH's clear-decision, so an environment-dependent bound makes retroactive schedule
   invalidation depend on which replica answered.

The containment and prefix comparisons are implemented locally in `jobspec`. `PathPrefixOverlap`
lives in the perforce package and `jobspec` must not import an agent package to reuse it.

**The Python mirror.** `Source._sync_paths_under_stream` and `Sync._rev_recognized` reproduce rules 1
and 2. Adding `exclude` to the SDK is required for the SDK to accept the field at all, given
`extra="forbid"`. Do not mirror rule 5's number there, for the same reason the count bounds are
deliberately absent from that file today.

## 6. Decision 4: `SourceKey`, `BaselineHash`, and the warm-workspace contract

### 6.1 The key

```
SourceKey(pf) = pf.Stream                                            when no entry is excluded
              = "x1|" + hex16(sha256(canonical)) + "|" + pf.Stream   otherwise
```

where `canonical` is the excluded paths, sorted, deduplicated, each followed by a NUL byte.

- **A task with no exclusions keeps today's key exactly** (the item's question 6), byte for byte, so
  every existing registry row, every `worker_workspaces` row and every allocated `short_id` stays
  valid with no migration.
- **The composite can never collide with a bare stream**, because `validateSourceSpec` already
  requires a stream to start with `//` and no legal stream starts with `x1|`. This matters: a
  collision would put a task into another task's workspace, which is the poisoning hazard by another
  route. The rejected alternative was a `#`-delimited suffix plus a new rule refusing `#` in a
  stream; that tightening is retroactive over stored `scheduled_jobs` rows and buys nothing the
  prefix form does not already give for free.
- **The `x1` tag is a version.** A future change to the canonicalisation moves to `x2` rather than
  silently reusing a key for a different meaning.
- **The key stays short.** `worker_workspaces.source_key` is `TEXT` inside `PRIMARY KEY (worker_id,
  source_type, source_key)` and inside `worker_workspaces_lookup_idx`; a raw concatenation of 16
  depot paths can exceed Postgres's btree index-row limit, and the failure lands inside the
  registration-time bulk inventory ingest, failing the whole transaction rather than one row.
  Nothing on the ingest path bounds the length (`applyInventoryUpdate` passes `u.SourceKey`
  straight through). It also stays one readable column in `relay workers workspaces`, whose
  `doWorkersWorkspaces` prints `SOURCE_KEY` through a `tabwriter`.
- The hash uses the same 16-hex-character truncation `BaselineHash` already uses, so an operator
  sees one shape twice rather than two.

Call sites that must move from `pf.Stream` to `SourceKey(pf)` inside `Prepare`:
`reg.GetBySourceKey`, `allocateShortID` (whose argument is currently the stream and which currently
separates two exclusion sets only by accident, via `ShortIDInUse` extending the candidate length),
`reg.Upsert`'s `SourceKey` field, and `perforceHandle.sourceKey`. `clientName` derives from
`shortID` and separates for free.

### 6.2 The baseline

`BaselineHash` must hash the exclusion flag, and **its encoding for a spec with no exclusions must
stay byte-identical.** It is a cross-process contract: `BaselineHashFromAPISpec` computes it
server-side for warm scoring, and the client-path retro records the reason not to disturb it -
changing the encoding re-syncs every warm workspace in the fleet once. Therefore: write the
exclusion marker only for an excluded entry, and add the flag to the sort key (two entries sharing a
path and rev currently sort unstably against each other).

A golden-value test pins the no-exclusion hash to a literal captured at HEAD, so a future
encoding change cannot cause a fleet-wide re-sync silently.

### 6.3 The warm bias

`selectWorker` compares `ws.SourceKey == taskSrc.Stream` and `Dispatcher.dispatch` builds its
candidate key list from `s.Stream`. Both must use a new `scheduler.SourceKeyFromAPISpec`, the exact
sibling of `BaselineHashFromAPISpec`, delegating to the same `perforce.SourceKey` through
`sourceSpecToProto` so the two processes cannot compute different strings. Without this change the
warm bias silently stops firing for every excluded task: it would compare a composite key against a
bare stream and never match, so an excluded task would be scattered across cold agents and each
scatter is a full sync.

### 6.4 What an exclusion is NOT part of

`Request.SyncPaths`, and therefore `Workspace.syncedPaths` and `PathPrefixOverlap`. Those record
what was SYNCED so a later holder can decide whether it must re-sync exclusively. Putting an
excluded path there would assert content is present that is deliberately absent.

## 7. Decision 5: operator visibility

- One line before the sync bracket: `[sync] excluding: N path(s)`, then one line per exclusion
  rendering the DEPOT path (what the operator wrote) with `%q`, last on the line. This follows
  `syncSummary`'s stated rule: the input-derived field goes last and is quoted, so a forged path
  cannot spell a convincing line of its own. The count is bounded by rule 5 of section 5.
- **A preempt that fails, fails the prepare**, through `classifyP4Error`, with the cause on the
  returned error and NOT repeated on the progress line - the convention `Prepare`'s sync-failure
  branch already documents and `TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause` pins.
  The item's WARN is refused: the operator reads that log after the volume is full.
- **A preempt that matched nothing also fails the prepare.** p4 exits ZERO when a filespec matches
  nothing (`bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero`), so a silent preempt
  is indistinguishable from a working one, and the two ways to get there are both live: a typo in
  the exclusion path, and an exclusion under a renaming remap, where `toClientPath` is a string
  rewrite that emits a client path resolving to nothing
  (`bug-2026-09-04-a-subpath-of-a-renaming-remap-does-not-resolve`). In both cases the exclusion
  does nothing and the volume fills. The preempt therefore uses a client method that streams stdout
  through a counting callback (the excluded subtree can be millions of lines, so it must not be
  buffered, exactly as `SyncStream` must not be) and returns p4's stderr even on a zero exit, and
  the provider refuses when p4 reported no such files for that path.
  - **Accepted false refusal:** an exclusion naming a subtree that is legitimately empty at that
    revision is refused. The trade is taken because a silently inert exclusion is the exact failure
    mode this feature exists to prevent, and the operator's escape is to delete an exclusion that
    was doing nothing anyway.
  - **"Up to date" is success, not emptiness.** On a warm workspace whose have-list already covers
    the exclusion at the target revision, p4 reports up-to-date with no per-file lines. Zero
    per-file lines alone must NOT be read as failure; the refusal keys on p4 reporting no such
    files.
  - This is a narrow, local instance of the general fix
    `bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero` asks for. It does not depend
    on that item and must not be duplicated by it.

## 8. Decision 6: migration and version skew

**Registry migration.** Nothing to migrate. A task with no exclusions produces today's key, today's
`short_id` and today's baseline hash, so every existing on-disk registry entry and every
`worker_workspaces` row remains valid and warm.

**Version skew.** Section 4 records that the field fails open: an older relay-server drops
`exclude` and an older relay-agent drops the proto field, and in both cases the result is a silent
full sync of the stream the operator asked to trim. Relay takes skew seriously elsewhere -
`RegisterRequest.supports_workspaces` is an `optional bool` specifically so an old agent's omission
is distinguishable, for rolling-upgrade safety - so accepting a silent full sync here would
contradict an established position.

Close it with the mechanism that already exists in both halves:

- `RegisterRequest` gains `optional bool supports_sync_exclusions`. Unlike `supports_workspaces`,
  whose column defaults TRUE, the safe default here is FALSE: an agent that does not report is
  presumed not to honour exclusions.
- `selectWorker` skips a worker without the capability for a task whose source carries an exclusion,
  the same shape as its existing `if sourceBearing && !w.SupportsWorkspaces { continue }`.

The cost is one proto field, one migration and one dispatcher predicate. The benefit is turning a
silent multi-terabyte over-sync into a task that does not dispatch. **If a plan cuts this step, the
cut must be recorded in README as a stated limitation, not dropped silently** - a document written
alongside a partial fix is where the partiality becomes a lie.

The server side of the skew (an old server dropping `exclude` from the JSON) is not closable from
this repository, since the client is whatever the caller runs. It is a documentation matter: the
README row for `exclude` states that a server that does not know the field ignores it.

## 9. Decision 7: the out-of-disk remedy

The item asks that `classifyP4Error`'s out-of-disk case be extended to mention exclusions once this
ships. **Refused as written.**

The shipped message reads "out of disk space on this agent's workspace volume - free space on the
worker host, or reduce the sync paths". "Reduce the sync paths" already covers an exclusion
semantically. Naming exclusions explicitly would advertise, in an operator-facing remedy, an action
that under section 3.4 can INCREASE total disk use on that agent: adding an exclusion to one job
family creates a SECOND full-size workspace beside the existing one and pushes the sweeper into
evicting another tenant's warm workspace. That is the same test the closed item applied when it
dropped `RELAY_WORKSPACE_MIN_FREE_GB` from this very message - a remedy that a job author can drive
and that acts against other tenants does not belong in the ladder.

Replacement: leave `classifyP4Error` alone, and state the coupling where exclusions are DOCUMENTED,
in README's source-workspaces section - an exclusion set changes the workspace identity, so a stream
with two exclusion sets holds two workspaces on one agent.

## 10. Threat model

The actor is a job author who can submit a spec, which is any authenticated user.

- **Workspace multiplication.** N submitted specs differing only in their exclusion sets create up
  to N workspaces per stream per agent, each nearly full size, evicting other tenants' warm
  workspaces through the sweeper's pressure pass. Bounded per spec by rule 5 of section 5, and NOT
  bounded across specs. This is the same unbounded-repetition shape `maxCommandsPerJob` records: the
  control for repetition is a rate limit, and `POST /v1/jobs` carries none. Recorded, not solved
  here.
- **Key collision.** Closed by construction in section 6.1: no legal stream starts with `x1|`.
- **Log forgery.** The exclusion log line renders an input-derived depot path. Closed by placing it
  last and rendering it with `%q`, per `syncSummary`'s existing argument.
- **Have-list poisoning across tenants.** Closed by construction: the exclusion set is in the
  workspace identity, so a preempt can only ever affect a client every one of whose tasks carries
  the same exclusions. Section 11 pins it.

## 11. Test plan

**Capture first.** Nothing in this repository has ever recorded what `p4 sync -k` emits: not its
per-file line shape, not its output when every file is already at the target have-revision, not its
stderr when the filespec matches nothing. The first implementation task is to capture all three
against the existing p4d fixture and put the artifact in a test fixture, before any parser is
written against documentation. The clobber slice's `Options:` line is the precedent: the captured
artifact carried a shape no document mentioned, and it was the shape that mattered.

**The acceptance test** (p4d integration lane, the only lane that can prove any of this - the fake
runner echoes whatever it is told):

`TestPerforce_E2E_AnExcludingTaskDoesNotStripFilesFromAnUnexcludingPeer`

1. Fixture: `//test/main` gains a subdirectory with a file in it, alongside the existing top-level
   file.
2. Task A prepares with an include of `//test/main/...` at `#head` and an excluded entry for
   `//test/main/<sub>/...`. Assert the subdirectory is absent under A's working directory and the
   top-level file is present.
3. Release A. Task B prepares with the same include and NO exclusion.
4. Assert B's working directory differs from A's, and the file under the subdirectory EXISTS in B's
   tree.
5. Assert the registry holds two entries with distinct `SourceKey`s, and that B's is exactly
   `//test/main`.

**The order is load-bearing and must be stated in the test.** The excluding task has to run FIRST.
Run B first and its full sync leaves the files on disk, so the poisoning is invisible and the test
passes against the broken build.

**The mutation that must kill it:** make `SourceKey` ignore exclusions and return the bare stream.
Then B shares A's workspace, the preempted files are never fetched, and step 4 goes RED. That
mutation is the item's hazard, reproduced.

Further tests:

- `TestProvider_AnExclusionIsPreemptedAtItsCoveringIncludesResolvedRevision` (fake runner): assert
  the exact preempt argv precedes the sync argv and carries the resolved changelist, not `#head`.
  The discriminating input is a spec whose include is `#head`, so a mutant that passes the literal
  revision through is visible.
- `TestSourceKey_NoExclusionsIsTheBareStream`, and a case asserting that order and duplicates
  canonicalise to one key.
- `TestBaselineHash_NoExclusionsIsUnchanged`, pinned to a literal captured at HEAD.
- `TestSelectWorker_AnExcludedTaskIsNotWarmOnAnUnexcludedWorkspace` in `internal/scheduler`.
- `validateSourceSpec` table cases, one per refusal in section 5, each with the negative that would
  pass a weaker implementation: an exclusion with a revision, an uncovered exclusion, an exclusion
  covered twice at different revisions, an exclusion equal to its include, seventeen exclusions.
- A preempt that reports no such files fails the prepare, and a preempt that reports up-to-date does
  not.

## 12. What this spec does NOT cover

- **The renaming-remap subpath gap.** `toClientPath` is a string rewrite, so an exclusion under a
  stream whose view renames a subtree resolves to a client path that names nothing. This spec does
  not fix that; it makes the failure LOUD (section 7) instead of letting the exclusion be silently
  inert. `bug-2026-09-04-a-subpath-of-a-renaming-remap-does-not-resolve` owns the fix, and this
  design gains correctness for free when it lands.
- **The general "p4 exits zero on a filespec that matches nothing" defect.** Section 7 closes it for
  the preempt call only.
- **A bound on workspace COUNT, or any admission control over disk.** Section 3.4 states the cost
  and section 10 states the repetition shape; neither is solved here.
- **Instrumentation for workspace size or eviction churn**, which is what would let an operator
  decide whether exclusions are net positive on a given agent.
- **The SPA source builder.** `feature-2026-09-03-perforce-source-builder-in-the-new-job-builder`
  owns it. Note for whoever ships it: that design REFUSES to enter builder mode on a key it cannot
  model, so an unmodelled `exclude` keeps the user in the JSON editor with their text intact. That
  is the safe direction, and it means this feature does not block that item, but the builder must
  model `exclude` or every excluded spec is stuck in JSON mode.
- **Exclusions for any source provider other than Perforce.** There is only one.
- **Unshelve interaction.** An unshelve into an excluded subtree is not refused and is not
  specified. p4 unshelves into the client view, and the excluded files are in the view (they are
  merely have-marked), so the behaviour is p4's; nothing here changes it.

## 13. Recommended backlog items

Proposals for the conductor to file. Not filed here.

1. **`source.sync` has no entry-count bound.** `validateSourceSpec` bounds nothing about
   `len(s.Sync)`, and `Prepare` runs one `ResolveHead` round trip per `#head` entry inside the task's
   own prepare phase. This is the third instance of the per-entry subprocess axis, beside
   `unshelves` and now exclusions, and it is the only one of the three with no bound at all.
2. **Nothing records a workspace's size or counts evictions.** `InventoryEntry` carries no bytes and
   `Sweeper.Run` discards `SweepOnce`'s returned ids, so eviction churn - the cost this design
   trades disk for - is unobservable. Without it, section 3.4's trade stays unquantified forever.
3. **The Python SDK's `_CLIENT_TEMPLATE_RE` has drifted from Go's `clientTmplRe`.** Go was tightened
   to refuse a leading hyphen because `CreateStreamClient` places the value immediately after `-t`;
   the SDK's pattern still allows one. The server is the validator of record so this is not a hole,
   but the SDK accepts a spec the server refuses, which is the drift class that file's own comment
   warns about.
4. **`worker_workspaces.source_key` is agent-supplied, unbounded, and inside a primary key.**
   `applyInventoryUpdate` and the registration-time bulk ingest pass it straight to
   `UpsertWorkerWorkspace`. This design keeps the key short by construction, which means the
   underlying absence of a length bound stays untested.
