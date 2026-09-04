---
date: 2026-09-04
topic: perforce-client-path-addressing
status: draft
covers:
  - docs/backlog/bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync.md
---

# Address p4 by client path so virtual and `import+` remap streams prepare

## 0. How this spec was produced

Gate mode was `autonomous`, so every place the brainstorming flow would put a question to a human,
the call is made here with the reasoning written down, and every such call is listed again in
section 9 so it is cheap to overturn. The backlog item's Proposal was treated as a proposal: every
bullet was checked against the tree at `a3d0f9a` before anything was scoped. Section 1 is the
result, with the current `Prepare` sequence written out first and the refutations next.

Three numbering schemes appear below and they are deliberately distinct. **F1-F9** are the findings
from verifying the backlog item (section 1). **N1-N19** are the steps of the NEW `Prepare` sequence
(section 4.3), against **P1-P16** for the CURRENT one (section 1.1). **R0-R10** are the red-first
implementation steps (section 6), and **D1-D10** are decisions (section 9).

---

## 1. Verification of the backlog item

### 1.1 The CURRENT `Prepare` sequence, read off `internal/agent/source/perforce/perforce.go:124-310`

The fix is a reorder, so a guessed starting order would make the whole spec worthless. This is the
order at HEAD, written out step by step with the line ranges.

| # | Lines | What happens |
|---|---|---|
| **P1** | 125-128 | `pf := spec.GetPerforce()`; return an error if nil. |
| **P2** | 130-133 | `reg, err := p.loadRegistry()`. |
| **P3** | 136-151 | The resolve loop over `pf.Sync`. For each entry with `rev == "#head"`, call `p.cfg.Client.ResolveHead(ctx, e.Path)` and set `rev = "@<cl>"`, recording `resolved[e.Path] = rev`. Append `e.Path + rev` to `syncSpecs` and `e.Path` to `syncPaths`. **This is the first p4 invocation in `Prepare`.** |
| **P4** | 153 | `baseline := BaselineHash(pf, resolved)`. |
| **P5** | 156-162 | `existing, found := reg.GetBySourceKey(pf.Stream)`; `shortID` is `existing.ShortID` when found, else `allocateShortID(pf.Stream, reg)`. |
| **P6** | 163-164 | `wsRoot := filepath.Join(p.cfg.Root, shortID)`; `clientName := fmt.Sprintf("relay_%s_%s", p.cfg.Hostname, shortID)`. |
| **P7** | 167-177 | Under `p.mu`: the **pre-Acquire eviction check** (`if p.evicting[shortID]` return "being evicted"), then get-or-create `p.workspaces[shortID]`. |
| **P8** | 179-181 | `prepareAcquireHook` (test seam, nil in production). |
| **P9** | 183-192 | Build `Request{BaselineHash, SyncPaths, Unshelves, WorkspaceExclusive}`; `handle, err := ws.Acquire(ctx, req)`. |
| **P10** | 202-208 | The **post-Acquire eviction re-check**. On a hit, `handle.Release()` and return "being evicted". |
| **P11** | 211-232 | `if !found { os.MkdirAll(wsRoot, 0o755); p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl); reg.Upsert(WorkspaceEntry{..., BaselineHash: ""}); reg.Save() }`. Each failure arm releases the handle first. |
| **P12** | 239-240 | `cur, curOK := reg.Get(shortID)`; `needsSync := handle.Mode() == ModeExclusive \|\| (curOK && cur.BaselineHash != baseline)`. |
| **P13** | 244-248 | `if needsSync { recoverOrphanedCLs(ctx, wsRoot, clientName) }`, errors going to `progress` only. |
| **P14** | 250-275 | `if needsSync`: the `[sync] starting` bracket, `SyncStream(ctx, wsRoot, clientName, syncSpecs, progress)`, release-before-reporting on failure, the `[sync] complete` bracket, then `if curOK { reg.Mutate(...) }` and `reg.Save()`. |
| **P15** | 278-297 | Unshelves: `CreatePendingCL`, `reg.AddPendingCL`, `reg.Save`, then one `Unshelve` per source CL. |
| **P16** | 299-309 | Return the `perforceHandle`. |

Four properties of that order matter to everything below.

1. **`MkdirAll` and `CreateStreamClient` are at P11, which is AFTER `ws.Acquire` (P9) and after the
   post-Acquire eviction re-check (P10), and they are inside the `!found` branch.** The backlog
   item's Proposal 3 describes them as moving "before head resolution", which is correct, but it
   does not say they are also moving from **after** the acquire to **before** it. That relocation is
   the source of the new interleaving in F5.
2. **`ResolveHead` is the first p4 call**, which two test comments state in those words
   (`perforce_test.go:226`, and the `client_error_test.go` case at line 68). Both become false.
3. **The `!found` Upsert can never race a sweep today**, because it sits after `ws.Acquire`, and
   after `Acquire` the workspace has a holder, so both `Provider.ReserveForEvict` and
   `Provider.EvictWorkspace` fail their inline holder check. This is the property the reorder gives
   up, and it is why F5 exists.
4. `syncPaths` (which becomes `Request.SyncPaths`, the arbitration key) is **`e.Path` in depot
   form**, distinct from `syncSpecs` (`e.Path + rev`, the p4 argv). The item changes only
   `syncSpecs`. Section 4.5 shows why leaving `SyncPaths` alone is not merely conservative but
   behaviourally identical.

### 1.2 The headline claim is CONFIRMED

`ResolveHead(ctx, path string) (int64, error)` (`client.go:139-150`) runs
`c.r.Run(ctx, "", []string{"changes", "-m1", path + "#head"}, nil)`. The cwd is the empty string and
there is no `-c` flag: it is a server-global, depot-namespace query. `SyncStream`
(`client.go:154-157`) runs `-c <client> sync -q --parallel=4 <specs...>` from `cwd`, and `Prepare`
passes it `syncSpecs`, which are depot paths. For a virtual stream or an `import+` remap the depot
side of the client's view is the remap SOURCE, so neither call can address the stream-name path.
The mechanism the item describes is real, and addressing by `//<client>/<rel>` resolves for every
stream type because the client view is what defines it.

### 1.3 FINDING F1 (partial refutation): the quoted symptom belongs to the sync call, not to the common case

The item's Summary and Repro both quote `p4 sync ... "file(s) not in client view"`. That is the
message for a sync spec with a LITERAL rev (`@12345`, `@label`, `#4`), which reaches `SyncStream`
directly. For `rev: "#head"` - the shape in every fixture in the package, in the README example and
in the integration test - the failure happens one step earlier, at P3: `p4 changes -m1
//test/virt/...#head` against a path with no depot storage returns no `Change ` line, so
`ResolveHead` falls through to `fmt.Errorf("could not parse %q", line)` and `Prepare` wraps it
`resolve head for //test/virt/...`.

Consequences for the plan:

- The p4d RED in R2 must **record the message it actually observes** rather than assert the item's
  quoted string. Both messages prove the bug; asserting the wrong one wastes a round.
- The pre-fix failure text differs by rev form, so a report that says "the repro did not produce the
  documented message" is not evidence that the bug is absent.

### 1.4 FINDING F2 (confirmation, checked rather than relayed): `assertCwdContract` does accept the new call

The item says "the assertion itself already accepts any `-c` call from `wsRoot`". Checked by reading
the body, not the comment (`perforce_test.go:23-35`):

```go
for _, c := range fr.calls {
    if len(c.args) > 0 && c.args[0] == "-c" {
        sawWorkspaceCall = true
        require.Equalf(t, wsRoot, c.cwd, ...)
    } else {
        require.Equalf(t, "", c.cwd, ...)
    }
}
require.True(t, sawWorkspaceCall, ...)
```

The only discriminator is `args[0] == "-c"`. A `-c <client> changes -m1 //<client>/...#head`
recorded with `cwd == wsRoot` takes the first branch and passes; `client -o -S`, `client -i` and
`client -d` keep `cwd == ""` and take the second. `sawWorkspaceCall` was already satisfied by the
sync call. **CONFIRMED: the assertion body needs no change.** Its COMMENT (lines 16-22) names
"ResolveHead's `changes -m1`" as an example of a global invocation and becomes false; that is the
item's Proposal 2 and it is in scope.

### 1.5 FINDING F3 (confirmation with a qualifier the item omits): the registry and sweeper claims

- `reg.Upsert` (`registry.go:139-149`) writes the struct through with no validation, so
  `BaselineHash: ""` is accepted on a first registration. **CONFIRMED.** The `!found` block at P11
  already does exactly this today, so the value is not new either.
- `reg.Save` exists under that name (`registry.go:55-70`), atomic via temp-plus-rename. **CONFIRMED.**
- The sweeper's age pass (`sweeper.go:119-132`) evicts any unlocked entry with
  `now.Sub(w.LastUsedAt) > s.MaxAge`. A registry entry is the ONLY thing it scans (`reg.Snapshot()`),
  so registering the workspace is indeed what makes an orphan reclaimable. **CONFIRMED.**

**The qualifier the item omits.** `Sweeper.Run` returns immediately when
`s.MaxAge == 0 && s.MinFreeGB == 0` (`sweeper.go:72-74`), and `cmd/relay-agent/main.go:85-88` only
constructs a sweeper at all when `RELAY_WORKSPACE_MAX_AGE > 0 || RELAY_WORKSPACE_MIN_FREE_GB > 0`.
Both default to unset. So on a default deployment **nothing reclaims anything**, orphan or not, and
the sentence "an early return after this point leaves a registered workspace the sweeper's age pass
reclaims like any other" is true only where an operator configured a sweeper.

That does not sink the design, because registration buys three things independent of the sweeper,
and the spec leans on all three rather than on the sweeper alone:

1. The next `Prepare` for the same stream finds `found == true`, reuses the same `shortID` and
   `clientName`, and (because `CreateStreamClient` becomes unconditional, D2) repairs whatever the
   failed attempt left half-built.
2. `ListInventory` reports the entry, so it reaches the coordinator's `worker_workspaces` table and
   appears in the SPA's Workspaces panel and in `relay workers workspaces`. An operator can evict it
   by hand through `EvictWorkspace`, which today refuses with "not found in registry".
3. Where a sweeper IS configured, the age and pressure passes reclaim it.

Write the qualifier into the commit message. Do not write "the sweeper reclaims it" unqualified.

### 1.6 FINDING F4 (confirmation, and it is stronger than the item's reason): the `BaselineHash` keying is FORCED, not merely convenient

The item argues `resolved` and `BaselineHash` should stay keyed on depot-form `e.Path` so "existing
registries remain valid and no workspace re-syncs on upgrade". True, and there is a second reason
that is not a preference at all.

`BaselineHash` is a **cross-process contract**. `internal/scheduler/source_proto.go:12-21` computes
the same function server-side:

```go
func BaselineHashFromAPISpec(s *api.SourceSpec) string { ... return perforce.BaselineHash(proto.GetPerforce(), nil) }
```

and `internal/scheduler/dispatch.go:255-265` compares it against the agent-reported
`worker_workspaces.baseline_hash` to score warm-workspace affinity, `+10_000` on a match against
`+1_000` on a mismatch. The full chain is `Prepare` -> `perforceHandle.Inventory()` ->
`internal/agent/agent.go:376-382` -> `internal/worker/handler.go:2207-2235` ->
`UpsertWorkerWorkspace`.

The server cannot compute a client-form hash: it does not know the agent's hostname or the
`shortID` the agent allocated, and both feed `clientName`. So hashing client-form paths would make
every warm workspace in the fleet score as merely-present instead of at-baseline, silently, with no
test in the tree able to see it. **The depot-form keying is forced by the server's inability to
know the client name.** Say that in the commit message; "existing registries remain valid" alone
invites a future reader to think the constraint is a migration concern that expires.

Traced consumers of the two keyings, per the task brief:

| Key | Read by | Verdict |
|---|---|---|
| `resolved[e.Path]` | `BaselineHash(pf, resolved)` only, in the same function | stays depot-form |
| registry `BaselineHash` | `needsSync` at P12, `reg.Mutate` at P14, `ListInventory`, then the whole cross-process chain above | stays depot-form, forced |
| `Request.SyncPaths` | `Workspace.tryAdmit`, `modeForEmptyWorkspace`, `release` via `PathPrefixOverlap` | stays depot-form; see 4.5 for the proof that the choice is behaviourally inert |

### 1.7 FINDING F5 (REFUTATION, and the largest one): the reorder opens a registration race that does not exist at HEAD, and the post-Acquire re-check does not close it

The item's Proposal 4 asks to "confirm the re-check after Acquire is still the guard that keeps a
sync out of a workspace being deleted", and adds "If a new interleaving exists, add it to the
`sweeper_claim_test` family." A new interleaving exists, and it is a different guard's job.

**At HEAD**, `reg.Upsert` (P11) runs after `ws.Acquire` (P9). After `Acquire` the workspace has a
holder, and both eviction entry points check holders inside the same `p.mu` critical section as the
reservation (`EvictWorkspace`, `perforce.go:331-346`; `ReserveForEvict`, `perforce.go:386-402`). So
at HEAD no sweep can reserve, and therefore none can `reg.Remove`, between the Upsert and the end of
`Prepare`.

**After the reorder** the Upsert sits before `Acquire`, and for the whole stretch from N5 through
N12 the workspace has zero holders. `ReserveForEvict` succeeds against zero holders. So this
sequence becomes reachable:

1. Prepare passes the pre-Acquire eviction check.
2. Prepare `MkdirAll`s, runs `client -o -S` and `client -i`, `reg.Upsert`s and `reg.Save`s.
3. A sweep (pressure pass, or a manual `EvictWorkspace`) reserves the short id, runs `client -d`,
   `os.RemoveAll`, `reg.Remove`, `reg.Save`, `OnEvictedCB`, and releases the reservation.
4. Prepare acquires. The post-Acquire re-check reads `p.evicting[shortID]` and finds it **clear**,
   because step 3 released. Prepare proceeds.

The mutual-exclusion property the re-check exists for still holds: exactly one of the two proceeds
into the destructive window, and a Prepare that overlaps an in-flight eviction still backs out. So
the item's Proposal 4 question, asked narrowly, answers "yes, the re-check is still the guard". But
the invariant the fix is SOLD on - "no early return from `Prepare` leaves an unregistered client
spec or directory" - is now violable by a path that is not an early return at all: the sweep deleted
Prepare's registry entry while leaving Prepare running, and Prepare will go on to sync, creating a
directory with no registry entry and (in the interleave where `client -i` lands after `client -d`) a
p4 client spec with no registry entry.

**Therefore the design adds one step the item does not have (N14, section 4.3): after the
post-Acquire eviction re-check, re-assert the registration if it is missing.** It is three lines, it
is only reached on a lost race, and it makes the last registry write in `Prepare`'s own ordering an
Upsert. R7 is its RED.

Two further consequences of the same relocation, recorded so nobody rediscovers them as bugs:

- `EvictWorkspace` returns "workspace %s not found in registry" for an unregistered short id
  (`perforce.go:358-361`). With early registration a manual evict issued during a first-ever
  `Prepare` now finds the entry and proceeds where before it refused. Safe (the reservation and the
  re-check still partition the two), but it is a behaviour change in an operator-facing command.
- Under contention on one stream, N Prepares queued in `ws.Acquire` will each already have run
  `client -o` and `client -i`. The p4 cost is paid on entry to the queue rather than on exit from
  it. At task cadence this is noise against a sync measured in minutes to hours, and D2 records the
  cost decision.

### 1.8 FINDING F6 (confirmation with a caveat): `CreateStreamClient` is idempotent for relay's usage, and the caveat is what it overwrites

`CreateStreamClient(ctx, name, root, stream, template)` (`client.go:108-128`) is two p4 invocations,
**both with `cwd == ""`**:

1. `p4 client -o -S <stream> [-t <template>] <name>` - fetches the spec, generating a fresh one when
   the client does not exist and regenerating the stream view when it does.
2. `p4 client -i` with `Root` set to the workspace directory and `Host` and `Owner` blanked
   (`setSpecField` on all three), fed on stdin.

Idempotent for relay's usage: the `clientName` is derived from `shortID`, `shortID` is derived from
`pf.Stream` (with `ShortIDInUse` refusing a collision against a different `SourceKey`), and the
registry keys on `SourceKey`, so one client name is bound to one stream for the life of the
workspace. Re-running the pair with the same arguments therefore re-derives the same view and
re-submits the same three overridden fields.

**Caveat, in scope for the commit message and not for code:** it is idempotent with respect to
RELAY's fields, not with respect to an operator's. A human who hand-edited the client spec (an
`AltRoots`, a `LineEnd`, a `SubmitOptions`, a description) between two tasks on the same stream has
those edits regenerated away on the next `Prepare` rather than on the next first-use. That is a
widening of an existing behaviour, not a new one, and relay already owns these clients by name.

### 1.9 FINDING F7 (confirmation): jobspec validation does guarantee the path sits under the stream

`internal/jobspec/jobspec.go:511-524`, quoted exactly:

```go
for i, e := range s.Sync {
    if !strings.HasPrefix(e.Path, "//") {
        return fmt.Errorf("sync[%d].path must start with //", i)
    }
    if e.Path != s.Stream &&
        e.Path != s.Stream+"/..." &&
        !strings.HasPrefix(e.Path, s.Stream+"/") {
        return fmt.Errorf("sync[%d].path must be under stream %s", i, s.Stream)
    }
    ...
}
```

Three admitted shapes, and the second is a strict subset of the third (`//s/x/...` has the prefix
`//s/x/`), so `toClientPath` has exactly two cases to handle: an exact match on the stream, and a
prefix match on `stream + "/"`. The comparison is byte-exact and case sensitive, which is stricter
than a case-insensitive p4 server, so no additional normalization is needed.

`validateSourceSpec` is reached from every ingest path (`jobspec.Validate`, the single job-spec
pipeline invariant), so a spec that reaches an agent has passed it.

**`toClientPath` still returns an error for a non-matching path.** Reasons, since "validation
guarantees it" is an argument for deleting the check:

- The validator runs in the coordinator process; `toClientPath` runs in the agent process. The
  agent's only knowledge of the rule is the assumption. A single enforcement point across a process
  boundary is a prose contract, and this repo's own record is that a wrong prose contract is a live
  bug.
- `Validate` is run over STORED `scheduled_jobs.job_spec` rows on several paths, so the rule's
  retroactive reach over old data is a property of when the predicate was added, which a reader of
  `toClientPath` cannot check.
- The alternative failure modes are both worse. Silently emitting the depot path unchanged
  reintroduces exactly today's bug for the case that reaches it, and hides a coordinator-side
  regression behind a p4 error. Emitting `//<client>` plus an unrelated tail synthesizes a path the
  operator never wrote.

D3 records the shape of the failure.

### 1.10 FINDING F8: the p4d fixture supports the new stream, and the fixture change must land and be proven BEFORE any Go change

`internal/agent/source/perforce/testdata/p4d/Dockerfile` pins `P4D_VERSION=r25.2` and downloads the
standalone `p4d` and `p4` binaries. `entrypoint.sh` creates, in order: a stream depot `//test` with
`StreamDepth: //test/1`, a mainline stream `//test/main` with `Parent: none`, `Type: mainline`,
`ParentView: noinherit`, `Paths: share ...`; a `setup-client` rooted at a temp dir; one submitted
file `readme.txt`; and one shelved changelist whose number is written to `/var/p4root/shelved-cl.txt`.
There is exactly **one** stream today, no virtual stream and no `import` or `import+` line.

r25.2 supports everything the fixture needs. `Type: virtual` has existed since 2011.1, the
`Remapped:` stream field since 2011.1, and the `import+` Paths type since 2015.1. The specific form
the fixture will add:

```
Stream:      //test/virt
Owner:       perforce
Name:        virt
Parent:      //test/main
Type:        virtual
ParentView:  inherit
Paths:       share ...
Remapped:    ... sub/...
```

which generates a client view mapping `//test/main/...` to `//<client>/sub/...`, so the baseline
file lands at `<wsRoot>/sub/readme.txt` rather than `<wsRoot>/readme.txt`. That single path is the
whole assertion: it is reachable only through the client view, so it cannot be produced by depot
addressing.

**The risk is the exact form, not the capability.** The existing entrypoint carries two comments
recording that r25.x rejected earlier drafts for missing `StreamDepth` and missing `ParentView`, so
this fixture family has a history of version-specific form requirements. R1 therefore lands the
entrypoint change ALONE, rebuilds the container, and runs the existing E2E test with no Go change,
before anything else. If p4d rejects the virtual form, the fallback is an `import+` remap on a
development stream:

```
Stream:      //test/dev
Parent:      //test/main
Type:        development
ParentView:  inherit
Paths:       share ...
             import+ vendor/... //test/main/...
```

which produces the same shape of assertion (`<wsRoot>/vendor/readme.txt`) through the same
mechanism. Record which form p4d accepted in the commit message; the bug title names both, and
either fixture proves the fix.

### 1.11 FINDING F9 (confirmation): README documents stream support without qualification

README's "Source workspaces" section, quoted exactly:

- Line 553: "**v1 supports Perforce only.**" The qualification is on the PROVIDER, not on the stream
  type.
- Line 580: "| `stream` | Yes | Perforce stream path. Workspaces are keyed by stream and reused
  across tasks. |" No qualification of any kind on which stream types work.
- Line 581: "| `sync` | Yes | One or more paths to sync. Each entry has `path` (depot path or `...`)
  and `rev` (`\"#head\"`, `@CL`, or `@label`). |"

**CONFIRMED**: the item's "Relay documents stream support without qualification, so this is a
correctness bug, not a studio preference" is accurate. Line 581 stays as written: the user-facing
spec field remains a depot path, and the client-path translation is internal.

### 1.12 Everything else in the item, checked against the tree

| Claim | Evidence |
|---|---|
| The reorder is forced by a genuine cycle | `ws.Acquire` takes `Request.BaselineHash` (P9); `BaselineHash` needs `resolved` (P4); resolving a remap stream needs the client to exist. Confirmed, and the cycle is broken by moving client creation out of the `!found` branch rather than by moving the acquire. |
| "The later `!found` Upsert block becomes redundant and is deleted" | Confirmed as written: P11's whole body moves. What survives at N8 is the same `if !found` guard, not an unconditional Upsert. D5 records why the guard must not be dropped. |
| Related file list (`perforce.go`, `client.go`, `sweeper.go`, `perforce_integration_test.go`) | All exist. `allocateShortID` exists at `perforce.go:510`. `sweeper.go` needs no edit; only its tests do. |
| The `sweeper_claim_test` "family" | One file, `sweeper_claim_test.go`, one test. The plural is aspirational; R7 adds the second member. |
| Cross-link to `bug-2026-09-03-prepare-failure-error-message-is-discarded` | **STALE.** That item is closed (`docs/backlog/closed/`), shipped by `01d3179`. The Repro's "Today the task also shows empty logs" is no longer true, which is good news for R2: the pre-fix failure text is readable in the task log. |
| No prescribed hook, command, symbol or p4 feature is missing | Checked one by one: `reg.Upsert`, `reg.Save`, `allocateShortID`, `assertCwdContract`, `prepareAcquireHook`, `Provider.ReserveForEvict`, `Sweeper.SweepOnce`, the `p4` global `-c` flag, `p4 changes -m1`, p4 client-syntax file arguments, `Type: virtual`, the `Remapped:` field and `import+` all exist. The only non-existent thing named is the target `ResolveHead(ctx, cwd, client, path)` signature, which the item presents as the target and not as a claim about HEAD. |

---

## 2. What this slice does NOT do

1. **No exclusion handling in `toClientPath`.** `idea-2026-09-03-sync-spec-exclusion-paths-design`
   is blocked on this item and explicitly asks for `toClientPath` to stay free of prefix parsing.
   The function takes a path and returns a path.
2. **No change to `Request.SyncPaths` or to `Workspace` arbitration.** Section 4.5 proves the choice
   is inert; changing it would move an arbitration decision inside a bug fix.
3. **No sweeper change.** `sweeper.go` is untouched. Only its tests gain fixtures.
4. **No new p4 error classification.** `classifyP4Error` gains no "not in client view" case, because
   after this change that error is the symptom of a defect rather than an operator condition. The
   open item `bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr` is NOT closed by
   this work; see 3.2 for the partial and honest interaction.
5. **No progress heartbeat.** `feature-2026-09-03-p4-sync-progress-heartbeat` edits the same call
   site and lands after this, per the item's own note.
6. **No `jobspec` change.** The validator already carries the predicate `toClientPath` relies on
   (F7). A proposed tightening is recorded in section 11 as a backlog candidate, not scoped here.
7. **No skip of `client -i`.** Decision D2.

---

## 3. The invariants lens, up front

Checked against CLAUDE.md's Invariants before any design choice.

- **End the generation before releasing the resource.** The acquire direction is the one this slice
  touches. `ws.Acquire` and the post-Acquire eviction re-check stay adjacent and in that order, and
  the `handle.Release()` on every failure arm after them is preserved verbatim. The reorder moves
  work to BEFORE the acquire, which is the safe side of that rule: nothing between N5 and N12 holds
  a workspace handle, so no new early return can strand one. Every new early return added in
  N6-N9 (`MkdirAll`, `CreateStreamClient`, `toClientPath`, `ResolveHead`) returns before `Acquire`
  and therefore must NOT call `handle.Release()`. Writing one in would be a nil dereference; a
  reviewer should check that none of the four new returns names `handle`.
- **Identity-checked teardown.** Unchanged in mechanism, and the rule's "where there is no identity
  to check, say so and name what replaces it" clause applies to N6-N9 exactly as it does to a
  registration that fails before it registers a sender: a `Prepare` that fails before `Acquire` has
  no handle, so it must unregister nothing, and adding a symmetric-looking release there would BE
  the clobber the rule forbids. What replaces the identity check for the artifacts it leaves behind
  is the registry entry (N8) plus the re-assertion (N14).
- **Epoch fence.** Not touched. No `tasks.status` or `task_logs` write is in this package.
- **Single job-spec pipeline.** Not touched, and specifically: `toClientPath` is a READER of a
  property `jobspec.validateSourceSpec` establishes, not a second validator. It defines no spec
  type and creates no task.
- **One bounded sender per gRPC stream.** Not touched. `progress` call sites are unchanged in count
  and position; the `[sync] failed` release-before-report ordering
  (`TestProvider_ASyncFailureReleasesTheWorkspaceBeforeItReportsAnything`) is preserved verbatim.
- **No interior pointers across locks.** `Registry.Get` and `GetBySourceKey` return value copies and
  the new N14 read uses `Get`, not a pointer. `p.mu` and `ws.mu` keep their existing order and
  neither is held across any p4 call: N6 through N9 run with no lock held.
- **Single JSON entry point.** Not touched.

### 3.1 Load and cost

Per `Prepare`, before this change: one `p4 changes -m1` per `#head` sync entry, plus (first use of a
stream only) one `client -o` and one `client -i`. After: the same `changes` calls, plus one
`client -o` and one `client -i` on **every** `Prepare` rather than on first use.

Two round trips per task against a sync measured in minutes to hours. The interesting case is not the
steady state but contention: N tasks queued on one stream now pay 2N p4 calls up front instead of 2
total. Even at the `maxTasksPerJob` ceiling this is a few thousand cheap metadata calls against a
Perforce server sized for a render farm, arriving over the span of a dispatch loop rather than in a
burst. Accepted, and D2 records the alternative that was declined.

`toClientPath` is string slicing with no allocation beyond the result, once per sync entry.

### 3.2 Threat model delta

The agent is the actor; the coordinator supplies `pf.Stream` and `pf.Sync` from a validated spec.

What changes: **user-supplied text stops being the leading component of the p4 argv on two calls.**
Today `p4 changes -m1 //depot/<user text>...#head` and `p4 sync ... //depot/<user text>...` put the
whole depot path, stream name included, into argv. After the change both carry
`//<clientName>/<remainder>`, where `clientName` is derived from a sanitized hostname and a base32
hash, and `remainder` is the part of the sync path BELOW the stream.

**This is a partial mitigation of `bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr`
and must not be reported as a fix for it.** That item's mechanism is p4 echoing the offending path
into its own STDERR, which `classifiableText` deliberately still classifies. The stream-name portion
of the path leaves argv; the remainder does not. A spec with stream `//depot/x` and path
`//depot/x/no space left on device/...` still puts the phrase in argv, and p4 may still echo it. The
item stays open and its acceptance criteria are unaffected.

What does not change: no new subprocess, no new file written, no new network destination, no
credential handling, no change to what an agent may ask the coordinator for. `toClientPath` cannot
emit a path outside `//<clientName>/` because it constructs the result from a constant prefix plus a
substring of a validated input, and it refuses any input that would require it to do otherwise.

One residual worth naming: `//<clientName>/...` is ambiguous with depot syntax if a DEPOT is
literally named `relay_<hostname>_<shortid>`. p4 resolves the first component against the current
client (which the `-c` flag pins) before the depot table, and the name shape makes a collision
implausible. No code guards it; recorded rather than defended.

---

## 4. Design

All changes are in `internal/agent/source/perforce/`.

### 4.1 `toClientPath`

New unexported function, in `perforce.go` beside `allocateShortID` (it is a `Provider`-level naming
concern, not a `Client` one, and it needs no `Client` state).

```go
// toClientPath rewrites a depot-form sync path into client syntax so p4 resolves
// it through the client's view. A virtual or import+ remap stream has no depot
// storage under the stream name, so the depot form addresses nothing.
// jobspec.validateSourceSpec admits only a path equal to the stream or under it;
// anything else is a spec that did not come through that validator and is refused
// rather than rewritten.
func toClientPath(clientName, stream, depotPath string) (string, error)
```

Behaviour, exhaustively:

| Input | Result |
|---|---|
| `depotPath == stream` | `"//" + clientName + "/..."` |
| `strings.HasPrefix(depotPath, stream+"/")` | `"//" + clientName + depotPath[len(stream):]` |
| anything else | `("", error)` naming both `depotPath` and `stream` |

The second row subsumes the validator's `stream + "/..."` case: the remainder is `/...` and the
result is `//<client>/...`.

The empty-remainder mapping in the first row is the item's "map an empty remainder to `/...`", and
it is a **deliberate behaviour change** for that one spec shape. Today a spec with
`path == stream` produces the sync argv `//s/x@12345`, which p4 reads as a single file with no
wildcard and which therefore syncs nothing. After the change it produces `//<client>/...@12345` and
syncs the whole client. This is the right reading of the operator's intent (they named the stream)
and it is the only sensible mapping, since `//<client>` with no wildcard names nothing. The upgrade
edge is in D4.

### 4.2 `Client.ResolveHead`

```go
func (c *Client) ResolveHead(ctx context.Context, cwd, client, path string) (int64, error) {
    out, err := c.r.Run(ctx, cwd, []string{"-c", client, "changes", "-m1", path + "#head"}, nil)
    ...
}
```

The parse (`changeFirstLine`, `strconv.ParseInt`) is unchanged, and so are its two non-`p4CommandError`
returns, which `TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified` depends on.

`cwd` is `wsRoot`. It is not strictly required - `-c` pins the client server-side and client-syntax
resolution does not consult the working directory - but three things argue for it: the package's
stated contract that every `-c` invocation runs from `wsRoot` (`assertCwdContract`), predictable
behaviour if a `.p4config` exists somewhere up the agent's own cwd, and consistency with
`SyncStream`, `CreatePendingCL` and the rest. It also makes `MkdirAll` a hard precondition of head
resolution, which the new ordering satisfies.

**`Prepare`'s wrap keeps naming the DEPOT path**, not the client path:
`fmt.Errorf("resolve head for %s: %w", e.Path, err)`. Two reasons: the depot path is what the
operator wrote and can act on, and `TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified`
needs a spec-derived path (with a space in it) to reach the error text so it can assert that a spec
path is not evidence about the disk. Changing the wrap to the client path makes that test vacuous
without failing it.

`CreateStreamClient` and `DeleteClient` keep their signatures and their `cwd == ""`.

### 4.3 The NEW `Prepare` sequence

Deltas from section 1.1 are marked. Everything unmarked is byte-identical.

| # | What happens | Delta |
|---|---|---|
| **N1** | `pf := spec.GetPerforce()`; nil check | = P1 |
| **N2** | `reg, err := p.loadRegistry()` | = P2 |
| **N3** | `existing, found := reg.GetBySourceKey(pf.Stream)`; `shortID` | **moved up** from P5 |
| **N4** | `wsRoot`, `clientName` | **moved up** from P6 |
| **N5** | under `p.mu`: pre-Acquire eviction check, then get-or-create `p.workspaces[shortID]` | **moved up** from P7 |
| **N6** | `os.MkdirAll(wsRoot, 0o755)` | **moved up** from inside P11, now **unconditional** |
| **N7** | `p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl)`, wrapped `create client:` and classified exactly as today | **moved up** from inside P11, now **unconditional** (D2) |
| **N8** | `if !found { reg.Upsert(WorkspaceEntry{ShortID, SourceKey: pf.Stream, ClientName, BaselineHash: "", LastUsedAt: now}); reg.Save() }` | **moved up** from inside P11; the `!found` guard is KEPT (D5) |
| **N9** | the resolve loop: for each entry, `cp, err := toClientPath(clientName, pf.Stream, e.Path)`; if `rev == "#head"`, `ResolveHead(ctx, wsRoot, clientName, cp)`; `resolved[e.Path] = rev`; `syncSpecs = append(syncSpecs, cp+rev)`; `syncPaths = append(syncPaths, e.Path)` | **new call shape**; `resolved` and `syncPaths` keys unchanged (F4) |
| **N10** | `baseline := BaselineHash(pf, resolved)` | = P4 |
| **N11** | `prepareAcquireHook` | = P8 |
| **N12** | `req := Request{...}`; `handle, err := ws.Acquire(ctx, req)` | = P9 |
| **N13** | post-Acquire eviction re-check; `handle.Release()` and return on a hit | = P10, comment unchanged |
| **N14** | **NEW**: `if _, ok := reg.Get(shortID); !ok { reg.Upsert(same entry as N8); reg.Save() }` | see F5 |
| **N15** | `cur, curOK := reg.Get(shortID)`; `needsSync := ...` | = P12 |
| **N16** | `recoverOrphanedCLs` when `needsSync` | = P13 |
| **N17** | the sync, with `syncSpecs` now client-form; brackets, release-before-report, `reg.Mutate`, `reg.Save` all verbatim | = P14 |
| **N18** | unshelves | = P15 |
| **N19** | return the handle | = P16 |

Two ordering choices inside that table are load-bearing and are argued rather than asserted.

**Why N5 (the pre-Acquire eviction check) precedes N6/N7 rather than following them.** Correctness
does not depend on it: the post-Acquire re-check at N13 is what partitions the race, and it is
downstream of both. Three positive reasons put it first anyway. It avoids provably wasted p4 work
when the answer is already known. It avoids `client -i` racing a concurrent `client -d` for a
workspace we have already been told is going away. And it keeps
`TestEvictWorkspace_PrepareRefusedWhileReserved` green with **no fixture change**: that test seeds
`p.evicting[shortID]` before calling `Prepare` and asserts the refusal, and it registers no
`client -o`/`client -i` fixtures, so a `fakeRunner` miss would fail it. Preserving an existing test
unchanged is evidence the ordering is the conservative one.

**Why N14 is an Upsert only, and not also a re-`MkdirAll` plus re-`CreateStreamClient`.** The richer
version would let a Prepare that lost the registration race go on to succeed instead of failing at
the sync. It is declined because the simple version already self-heals one attempt later: the
unconditional `CreateStreamClient` at N7 (D2) means the NEXT `Prepare` for that stream recreates the
client and the directory, and `reg.Get` returns `BaselineHash: ""` so it re-syncs. The failing
attempt fails closed with a p4 error the task log now carries, and it is retried by the task's own
retry budget. Adding a second creation path for a rare branch buys one retry and costs a second
place where creation lives.

### 4.4 What `needsSync` does on the first `Prepare` after the reorder

Unchanged, and worth writing down because the Upsert moved across the read. At N8 a first-ever
workspace is registered with `BaselineHash: ""`. At N15, `reg.Get(shortID)` returns `curOK == true`
with `BaselineHash == ""`, which differs from `baseline`, so `needsSync` is true. That is the same
answer P12 produced at HEAD from the same values, reached in the same way. The `if curOK` guard on
the post-sync `reg.Mutate` at N17 is likewise unchanged and is now true where before it was also
true. No first-sync behaviour moves.

### 4.5 Why `Request.SyncPaths` stays depot-form, proven rather than assumed

`SyncPaths` feeds only `PathPrefixOverlap` (`baseline.go:60-64`), through `tryAdmit`,
`modeForEmptyWorkspace` and `release`. All comparisons happen between paths belonging to holders of
**one** `Workspace`, and one `Workspace` is one `shortID`, which is one `SourceKey`, which is one
stream. So every path in any comparison shares the same constant prefix `P` (the stream in depot
form, or `//<clientName>` in client form).

`PathPrefixOverlap(a, b)` after trimming `/...` is `HasPrefix(a, b) || HasPrefix(b, a)`. With
`a = P + ra` and `b = P + rb` for the same `P`, `HasPrefix(P+ra, P+rb)` is exactly `HasPrefix(ra, rb)`.
The prefix cancels. **Depot form and client form give identical answers for every comparison this
code performs**, so the choice is behaviourally inert and depot form wins on diff size. Record the
cancellation argument in the commit message; the tempting alternative ("client form is more correct
because overlap is about files on disk") reaches the same answers and would only be a change worth
making if `SyncPaths` were ever compared across streams, which nothing does.

### 4.6 The full list of call sites that change, with why

| Site | Change | Why |
|---|---|---|
| `client.go` `ResolveHead` | signature gains `cwd, client`; argv gains `-c <client>` | a remap stream's head is resolvable only through the client view |
| `perforce.go` new `toClientPath` | added | the single place depot-to-client translation lives, so the exclusion design has one seam to extend |
| `perforce.go` `Prepare` N3-N8 | shortID, wsRoot, clientName, evict pre-check, MkdirAll, CreateStreamClient, Upsert move above head resolution | the client must exist before any client-scoped p4 call; registering with it is what makes the artifacts reclaimable |
| `perforce.go` `Prepare` N7 | `CreateStreamClient` becomes unconditional | D2; also the self-healing that makes N14 sufficient |
| `perforce.go` `Prepare` N9 | sync specs become `toClientPath(...) + rev` | the fix |
| `perforce.go` `Prepare` N14 | new re-assertion of registration | F5 |
| `perforce.go` `Prepare` P11 | deleted | its body is now N6-N8 |

---

## 5. Where a property cannot go red at HEAD

Stated before the sequence, so no step below has to hedge.

- **`toClientPath`'s table** cannot go red at HEAD by absence alone in a useful way: a test for a
  function that does not exist fails to compile, and a compile failure is not evidence about
  behaviour. R3 therefore introduces a **stub that returns the depot path unchanged with a nil
  error** - which is precisely the rejected "silently emit a wrong path" design - and takes its RED
  against that. Every row of the table then reddens for a behavioural reason, and the stub's death
  is the refutation of the design D3 declines.
- **The p4d virtual-stream test** goes red at HEAD for real, against a real p4d, and that RED is the
  only proof of the fix that exists (the fake runner echoes whatever it is told). Its exact message
  is not predictable in advance (F1) and must be recorded, not asserted in advance.
- **The orphan test (R6)** goes red at HEAD, but for a reason worth stating: at HEAD nothing is
  created before head resolution, so the assertion that reddens is "the workspace directory exists",
  not "the registry entry exists". The item says this test "goes red only against a naive port".
  **That is incomplete**, and the incompleteness matters: written as the item describes it (assert
  the registry entry and sweep it), the test is red at HEAD too, and a reader would conclude HEAD
  leaks an orphan, which it does not. R6 therefore asserts three things in order - directory exists,
  registry entry exists, sweep reclaims it - so the failing assertion NAMES which tree it is looking
  at. Is the item's guard acceptable? **Yes as a regression guard, no as written.** Keep it, in the
  three-assertion form, and pair it with the safety guard in R6b whose subject is the invariant
  itself rather than one implementation of it.
- **R6b (no unregistered directory under `Root` after a failed `Prepare`)** is **green at HEAD**,
  vacuously, because HEAD creates nothing. Labelled as a regression guard, not a red-first
  criterion, per the rule that a replacement criterion which is already green must be labelled as
  one. It is the only assertion in the slice whose subject is the acceptance criterion verbatim.

---

## 6. The red-first sequence

**R0 - baseline, both ways.** At `a3d0f9a`, run and record:
`go test ./internal/agent/source/perforce/... -count=1`, and
`go test -tags integration -p 1 ./internal/agent/source/perforce/... -count=1 -timeout 1800s`
(Docker plus `p4` on PATH; it skips cleanly without them, and a skip is not a green - say which was
obtained). Nothing below may be diagnosed against an unmeasured baseline.

**R1 - the p4d fixture, ALONE, before any Go change.** Add the virtual stream (or the `import+`
fallback, F8) to `testdata/p4d/entrypoint.sh`. Change nothing else. Rebuild the image and re-run
`TestPerforce_E2E_SyncAndUnshelve`. Expected: still green, and the container log shows the new
stream created. This step exists because the fixture form is the single largest unknown in the slice
and it is cheap to falsify in isolation. Record which form p4d accepted.

**R2 - the integration RED, still with no Go change.** Add
`TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout`: `Stream: "//test/virt"`,
`Sync: [{Path: "//test/virt/...", Rev: "#head"}]`, no unshelves. Assert `Prepare` succeeds and that
`<wsRoot>/sub/readme.txt` exists with content `baseline`. Run it at HEAD.
Expected RED, and **record the exact error text** rather than asserting it - F1 says it is most
likely `resolve head for //test/virt/...: could not parse ""` and not the item's quoted
"file(s) not in client view". If the test passes at HEAD, stop: either the fixture is not a real
virtual stream or the bug is not what the item says, and either way nothing below is justified.

**R3 - `toClientPath`. Default lane.** New `perforce_clientpath_test.go`, table-driven, five rows:
`path == stream`; `path == stream+"/..."`; `path` strictly under the stream; `path` not under the
stream at all; and a path that shares a textual prefix with the stream but is not under it
(`stream = "//depot/x"`, `path = "//depot/xy/..."`). The last two assert an error whose text names
both the path and the stream.
RED: add a stub returning `(depotPath, nil)`. Rows one to three fail on the value, rows four and
five fail on the absent error. Record all five messages, then implement.
The fifth row is the one that reddens against the sloppy implementation
`strings.HasPrefix(depotPath, stream)`, and it is why the design writes `stream + "/"`.

**R4 - `ResolveHead`. Default lane, and the RED is behavioural, not a compile error.** First widen
the signature to `(ctx, cwd, client, path)` while leaving the body passing `""` and omitting `-c`,
and update `client_test.go`'s `TestClient_ResolveHead` to the new fixture key
`-c relay_h_abc changes -m1 //relay_h_abc/...#head` with a non-empty `cwd`.
Expected RED: `fakeRunner.Run: no fixture for args "changes -m1 //relay_h_abc/...#head" (cwd="")`.
Then fix the body. Add one assertion `TestClient_ResolveHead` does not make today: that the recorded
call's `cwd` equals the `cwd` passed in, so a future edit cannot silently drop it.
`TestClient_ResolveHeadError` re-keys the same way.

**R5 - the `Prepare` reorder. Default lane.** Apply N3-N9 and N14 and re-key every fixture. The
affected files and what each needs, enumerated rather than counted so the list can be checked:

| File | Change |
|---|---|
| `perforce_test.go` `TestProvider_PrepareCreatesClientAndSyncs` | `changes -m1` key becomes `-c <c> changes -m1 //<c>/...#head`; sync key becomes `-c <c> sync -q --parallel=4 //<c>/...@12345` |
| `perforce_test.go` `TestProvider_UnshelveAndFinalizeRevert` | same two keys |
| `perforce_test.go` `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` | same two keys, **plus new `client -o -S //s/x <c>` and `client -i` fixtures**, because the registry is pre-seeded (`found == true`) and creation is now unconditional |
| `perforce_test.go` `TestProvider_Prepare_ClassifiesAuthError` | re-key the `setErr`; **add `client -o`/`client -i` fixtures**; rewrite the comment "ResolveHead is the first p4 call inside Prepare", which becomes false |
| `perforce_test.go` `TestProvider_Prepare_ClassifiesRecoverError` | re-key both |
| `perforce_progress_test.go` `syncFixture` | re-key both; it feeds all three progress tests |
| `client_error_test.go` `TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified` | re-key the `setErr`; add `client -o`/`client -i`; **the assertions stay as they are** - the depot path with the space still reaches the error through `Prepare`'s wrap (4.2), which is the whole point of keeping that wrap on `e.Path` |
| `provider_evict_recheck_test.go` | add `client -o -S //depot/main <c>` and `client -i` (rev is `@1`, so no `changes` call) |
| `sweeper_claim_test.go` | same two fixtures |
| `provider_evict_test.go` `TestEvictWorkspace_PrepareRefusedWhileReserved` | **no change** - and confirm that, because it is the evidence for the N5-before-N6 ordering |

Expected RED before the production edit: `fakeRunner: no fixture for` on the re-keyed tests. Expected
RED before N5 is placed ahead of N6: `TestEvictWorkspace_PrepareRefusedWhileReserved` starts
demanding client fixtures. Measure that one deliberately, once, by placing the pre-check late - it is
the cheapest available proof that the ordering choice in 4.3 is the one the tests already encode.

**R6 - the orphan guard. Default lane.** New test named
`TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace`. Fixtures for `client -o -S`,
`client -i` and `client -d <name>`; `setErr` on the new `ResolveHead` key; empty registry.
Assert, in this order:

1. `Prepare` returns an error.
2. `filepath.Join(root, shortID)` exists on disk.
3. `reg.Get(shortID)` is present, with `BaselineHash == ""` and `ClientName == clientName`.
4. A `Sweeper{Root, Reg: reg, MaxAge: tiny, Client, ListLocked: p.LockedShortIDs, Claim:
   p.ReserveForEvict, OnEvictedCB: p.InvalidateWorkspace}` with the entry backdated past `MaxAge`
   returns `[]string{shortID}` from `SweepOnce`, and the directory is gone.

RED at HEAD on assertion 2, which is the honest one (HEAD creates nothing, so it has no orphan). RED
against a naive port on assertion 3. Section 5 records why the item's one-assertion version would
have been misread.

**R6b - the safety guard, labelled green at HEAD.** New test named
`TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory`. After the same failed `Prepare`,
read `os.ReadDir(root)` and require every directory entry to have a matching `reg.Get`. Green at HEAD
vacuously, green after the fix, RED against the naive port. It is the only assertion whose subject is
the acceptance criterion itself; the label is part of the test's comment, not just this spec.

**R7 - the new interleaving. Default lane, `sweeper_claim_test.go`.** New test named
`TestSweeperClaim_ASweepThatCompletesBetweenRegistrationAndAcquireIsRepaired`. Reuse
`prepareAcquireHook` and `gatingRunner`, but unlike the existing test let the sweep run to
COMPLETION inside the hook: launch `SweepOnce`, wait on `gate.entered`, `close(gate.proceed)`, then
wait on `sweepDone` before returning from the hook. At that point the reservation has been released
and the entry has been removed, so `Prepare`'s post-Acquire re-check finds `p.evicting` clear and
proceeds.
Assert: `Prepare` returns without a "being evicted" error, and `reg.Get(shortID)` is **present**
afterwards.
RED without N14: the entry is absent. This is F5's guard and the concrete answer to the item's
Proposal 4 ("If a new interleaving exists, add it to the `sweeper_claim_test` family").
State in the test's comment that `fakeRunner` does not model the deleted client, so the test pins
the REGISTRATION and not the sync's fate - the fake echoes what it is told, and pretending otherwise
would be the vacuous-fixture shape this repo already tracks.

**R8 - the p4d GREEN.** Re-run R2's test. It must now pass, with `<wsRoot>/sub/readme.txt` present.
Also re-run `TestPerforce_E2E_SyncAndUnshelve` on the same image: a non-remapped mainline stream must
be unaffected, and its `//test/main/...` sync now goes out as `//<client>/...`.

**R9 - the cwd contract and the comment sweep.** Section 7. `assertCwdContract`'s BODY is unchanged
(F2); its comment is rewritten. Run the three tests that call it and confirm green with no assertion
edit - that green is the evidence for F2's claim, and it should be stated as such in the commit
message rather than as "the comment was updated".

**R10 - full verification.** `go test ./internal/agent/source/perforce/... -count=1`; the integration
lane with Docker and `p4`; `go vet` under both tag sets; and `-race` in the `golang:1.26` Linux
container across all packages, which is also the only local way to run this package's
`//go:build !windows` files. If the container lane is unavailable, say `-race` did not run rather
than substituting `-count=N`.

---

## 7. The prose sweep

Wrong prose about correct code is this repo's dominant defect class, and this change falsifies four
live passages plus one README table row.

### 7.1 Comments

| File | Passage at HEAD | Becomes |
|---|---|---|
| `perforce_test.go:16-22` (`assertCwdContract`) | "every workspace-scoped invocation (argv begins with `-c <client>`) must run from wsRoot, while every global invocation (no `-c` prefix - ResolveHead's `changes -m1`, the `client` create/delete calls) must run with an empty cwd" | head resolution moves to the workspace-scoped side. The remaining global invocations are exactly `client -o`, `client -i` and `client -d`. Name them; do not write "the client calls" and leave a reader to enumerate. The sentence about the assertion being the cwd half of the client-selection contract stays. |
| `perforce_test.go:226-227` | "ResolveHead is the first p4 call inside Prepare. Inject the canonical 'ticket invalid' stderr..." | `client -o -S` is now the first p4 call. The test still injects at `ResolveHead`, and the reason it is still worth injecting there is that it is the first call carrying a job-supplied path - say that, rather than deleting the sentence. |
| `client_error_test.go:63-67` | "ResolveHead has returns that are NOT p4CommandError ... and Provider.Prepare wraps them with the job's own depot path" | still true and now load-bearing in a second way: the wrap is the ONLY place the job's depot path reaches the error, because the argv no longer carries it (4.2). Add that clause; it is what stops a future edit from "simplifying" the wrap to the client path. |
| `client.go:138` (`ResolveHead` doc) | "resolves a depot path to its head CL number via `p4 changes -m1`" | resolves a CLIENT path through the named client's view, and why: the stream name is not depot storage for a virtual or `import+` stream. Two lines, stating the hazard, no history. |
| `perforce.go` P11's absence | the `// First time: create on-disk dir and p4 client spec.` comment disappears with the block | N6-N8 need a comment stating the ordering constraint (creation before head resolution, registration with creation) and naming the guard: R6 and R7. |
| `perforce.go` N13's comment (lines 194-201) | the post-Acquire re-check argument | unchanged in substance and **must stay**, plus one sentence for N14: the re-check partitions the destructive window, and it does not guarantee the registry entry survived a sweep that completed inside the window, which is what N14 restores. |

### 7.2 README

| Line | Text at HEAD | Change |
|---|---|---|
| 580 | "`stream` \| Yes \| Perforce stream path. Workspaces are keyed by stream and reused across tasks." | add that mainline, development, task, **virtual** and `import+` remap streams are all supported, and that relay addresses p4 by client path so the stream's view (including remaps) is what defines the layout on disk. |
| 581 | "`sync` \| Yes \| One or more paths to sync. Each entry has `path` (depot path or `...`) and `rev` ..." | **no change to the field description** - the spec field is still a depot path under the stream. Do not describe the client-path rewrite here; it is internal and naming it in the field table invites an operator to write one. |
| 587 (Workspace arbitration) | "tasks needing additional but disjoint sync paths join additively" | no change. 4.5 shows the arbitration keying is unmoved. |

Note for whoever edits this table: `bug-2026-09-03-readme-source-workspaces-table-omits-client-template`
is open against the SAME table and adds a `client_template` row. Whichever lands second rebases
trivially, but flag it in the PR so a reviewer is not surprised by a second edit to five adjacent
lines.

### 7.3 Not changed, on purpose

`docs/superpowers/specs/2026-05-01-p4client-explicit-flag-design.md` says `ResolveHead` is
"server-global; called *before* a workspace exists ... Stays as-is and runs with no cwd binding",
and its retro says the same. Both are records of the tree on 2026-05-01 and stay as written. Specs
and retros are records of a moment; comments and README are live contracts. The temptation to
"fix" the older spec should be refused: doing so destroys the record that the 2026-05-01 decision
was made against a tree where no remap stream was in scope.

---

## 8. Acceptance criteria, and which of the item's are false

### 8.1 The item's criteria, assessed

1. *"A task whose `source.stream` is a virtual or remap stream syncs into the correct layout, proven
   against p4d."* **TRUE and achievable**, conditional on the fixture form (F8), which R1 falsifies
   in isolation before anything depends on it.
2. *"No early return from `Prepare` leaves an unregistered client spec or directory."* **FALSE as
   the item scopes it.** Early registration alone does not achieve it: the reorder opens a window in
   which a sweep can complete and remove the entry while `Prepare` runs on (F5), and the resulting
   leak is not reached by an early return at all. It becomes true with N14. Restate the criterion as
   *"no exit from `Prepare`, early or successful, leaves a workspace directory or p4 client spec
   with no registry entry"*, which is what R6b asserts.
3. *"Existing workspaces do not re-sync after the upgrade (baseline unchanged)."* **TRUE**, and the
   reason is stronger than the item's (F4: the server computes the same hash and cannot know the
   client name). **One narrow exception, and it must be recorded**: a spec whose `sync[].path`
   equals the stream exactly changes MEANING under `toClientPath`'s empty-remainder rule (4.1) while
   its baseline is unchanged, so such a workspace does not re-sync and stays under-synced until some
   other input to the hash moves. D4.
4. *"The cwd contract comment names head resolution as workspace-scoped."* **TRUE**, and note the
   corresponding claim about the assertion body is also true and was checked rather than relayed
   (F2).

### 8.2 Prescribed remedies naming things that do not exist

None. Every symbol, command, hook and p4 feature the item names exists (1.12). Two near-misses worth
recording so a reviewer does not re-check them: the `sweeper_claim_test` "family" is currently one
file with one test (R7 makes it two), and the item's cross-link to
`bug-2026-09-03-prepare-failure-error-message-is-discarded` points at a CLOSED item, so its
"Today the task also shows empty logs" clause is stale and should not be carried into the commit
message.

### 8.3 This spec's criteria

- A `Prepare` against a virtual or `import+` remap stream succeeds against the p4d fixture, and the
  synced file is at the REMAPPED path under the workspace root.
- `TestPerforce_E2E_SyncAndUnshelve` is still green on the same image, so a plain mainline stream is
  unaffected.
- `toClientPath` returns an error, naming both the path and the stream, for any path not equal to
  the stream and not under `stream + "/"`, including a path that merely shares a textual prefix.
- Every p4 invocation whose argv begins with `-c` runs from `wsRoot`; every other invocation runs
  with an empty cwd; `assertCwdContract` proves it with an unchanged body.
- After a `Prepare` that fails at head resolution on a stream's first use, the workspace directory
  exists, the registry has an entry for it with an empty baseline, and a configured sweeper reclaims
  both.
- After a sweep that completes between registration and acquire, `Prepare` leaves a registry entry.
- No directory under `RELAY_WORKSPACE_ROOT` lacks a registry entry after any `Prepare` outcome.
- `BaselineHash` is byte-identical to HEAD's for every spec, verified by the unchanged
  `baseline_test.go` staying green with no edit.
- `make test`, the perforce integration lane with Docker and `p4`, `go vet` under both tag sets, and
  `-race` in the `golang:1.26` container are all green.
- Every passage in 7.1 and 7.2 is rewritten; no count is incremented and no enumeration is left
  partial.

---

## 9. Decisions

Made autonomously. Each is stated so it is cheap to overturn.

**D1 - address p4 by client path, as the item proposes.** The alternatives were weighed and both
lose. (a) Resolve the remap ourselves: read the client view with `p4 client -o` and translate depot
to client in Go. It reimplements p4's view resolution, including exclusion lines, overlays,
`import+` and inherited parent views, in a place with no test oracle. (b) Keep depot addressing and
document that virtual and remap streams are unsupported: cheapest, and it contradicts a documented
capability (F9) for a case reproduced in production. Client addressing is one string rewrite, and p4
does the resolution.

**D2 - `CreateStreamClient` runs UNCONDITIONALLY on every `Prepare`; the `client -i` is NOT skipped.**
The item offers "optionally skip the `client -i` when the fetched spec already carries the intended
`Root`, `Host` and `Owner`". Declined.
The cost being avoided is one p4 round trip per task, against a sync measured in minutes to hours
(3.1). The cost being incurred is a conditional whose FALSE POSITIVE silently disables repair: if the
comparison ever reads "already correct" for a spec that is not, the client is never fixed and the
symptom is a workspace syncing to the wrong root with no error anywhere. And the unconditional path
buys a property the design leans on twice: it is what heals a half-built workspace on the next
attempt, which is what makes N14 sufficient as an Upsert-only step (4.3) and what makes the
registered orphan recoverable on a deployment with no sweeper configured (F5, F3).
Additionally, p4 itself already no-ops an unchanged submission (it reports `Client <name> not
changed.`), so the saving is one round trip on the wire and not a server-side write. **The plan
should confirm that against the p4d fixture and record it**, but the decision does not rest on it -
it rests on the conditional's failure mode.

**D3 - `toClientPath` fails closed with an error, and `Prepare` returns it as the task's failure.**
Not a panic (an agent-side panic takes down other tasks' runners), not a silent passthrough (that is
today's bug, reintroduced for the case that reaches it), not a synthesized best guess. The error
names both the offending path and the stream so an operator can read the malformed spec off the task
log, which `01d3179` made visible. The message should say the spec did not come through the
validator, because that is what the condition means (F7); it is not an operator input error at the
agent.

**D4 - the empty remainder maps to `/...`, and the upgrade edge is recorded rather than mitigated.**
Adopted from the item. It is the only sensible mapping (4.1) and it is a real behaviour change for a
spec with `path == stream`: from syncing nothing to syncing the whole client. The upgrade edge is
that such a workspace's baseline is unchanged (F4), so it does not re-sync and stays under-synced
until another input to the hash moves. Not mitigated, because the alternatives are worse: forcing a
re-sync would mean changing `BaselineHash`, which breaks the server-side estimate for every workspace
in the fleet; and refusing `path == stream` at the agent would break a shape the validator admits.
Recorded here, in the commit message, and proposed as a validator tightening in section 11.

**D5 - the `if !found` guard on the Upsert is KEPT; the Upsert does NOT become unconditional.**
"The `!found` block moves up" reads like an invitation to simplify it to an unconditional Upsert now
that its neighbours are unconditional. That would clobber an existing entry's `OpenTaskChangelists`
(losing the crash-recovery record for another task's in-flight pending CL) and reset its
`BaselineHash` to `""`, forcing a full re-sync of every warm workspace in the fleet on upgrade -
which is precisely the acceptance criterion the item is protecting. `Registry.Mutate` exists so that
in-place edits do not do this; `Upsert` replaces the whole struct.

**D6 - N14 is added, and it is an Upsert only.** F5 establishes the window; 4.3 argues the scope.

**D7 - `Request.SyncPaths` stays depot-form.** Proven inert in 4.5 rather than assumed conservative.

**D8 - `ResolveHead` runs from `wsRoot` rather than from `""`.** Not required by p4 (the `-c` flag
pins the client server-side), adopted for the package's stated cwd contract, for `.p4config`
predictability, and for consistency with every other `-c` call. It makes `MkdirAll` a precondition
of head resolution, which the ordering already satisfies.

**D9 - the p4d fixture change lands and is proven first, in isolation (R1).** The fixture form is
the slice's largest unknown and the entrypoint has a recorded history of version-specific form
rejections (F8). Falsifying it costs one container rebuild; discovering it after the Go reorder
costs a round with an ambiguous cause.

**D10 - no new `classifyP4Error` case for "not in client view".** After this fix that string is the
symptom of a defect, not of an operator condition, so a classification would attach operator guidance
to a bug report. The classifier's open item stays open and is not partially closed here (3.2).

---

## 10. Lane structure and sequencing

**One lane.** The three edits (`toClientPath`, `ResolveHead`'s signature, the `Prepare` reorder) are
mutually dependent: `ResolveHead` cannot take a client path until one exists, the client cannot exist
before head resolution without the reorder, and the reorder is not safe without N14. A second
worktree would produce two branches each red until merged.

Commit order inside the lane follows section 6, and two orderings within it are load-bearing:

- **R1 and R2 come before any Go change.** The fixture is the unknown and the p4d RED is the only
  proof the bug exists.
- **R6, R6b and R7 are written before N14 exists**, so R7 has a real RED against a real un-guarded
  tree rather than a mutation.

`gofmt` is useless as a signal on this repo (CRLF working copy), so after any programmatic edit to a
tracked file check the diffstat against the intended change size, run `git ls-files --eol` on the
touched paths and require `i/lf`, and confirm the file still decodes as UTF-8. The entrypoint script
is a shell file with LF semantics inside a Linux container; it is the one file in this slice where a
CRLF corruption would be silently executed by `bash` and produce a confusing p4d failure, so check it
specifically.

---

## 11. Residual risks and backlog candidates

Proposed, not filed. The conductor should route these to the human for acceptance.

1. **`p4 sync -q` output volume on a wide remap.** Unchanged by this slice, but a remap stream is
   exactly the shape that pulls a very large subtree, and `feature-2026-09-03-p4-sync-progress-heartbeat`
   is the item that will make that observable. No new item needed; noted so the two are sequenced.
2. **A validator tightening for `sync[].path == stream`.** D4's behaviour change exists because the
   validator admits a shape that means nothing useful in depot form. Requiring `stream + "/..."` or
   a strict sub-path would remove the ambiguity at the single enforcement point. Retroactive over
   stored `scheduled_jobs.job_spec` rows, so it needs its own item and its own re-validating-reader
   analysis; do not fold it in here.
3. **A configured-sweeper prerequisite for orphan reclamation.** F3's qualifier means the acceptance
   criterion "the sweeper reclaims it" is conditional on operator configuration that defaults off. A
   candidate item: warn once at agent startup when `RELAY_WORKSPACE_ROOT` is set but neither
   `RELAY_WORKSPACE_MAX_AGE` nor `RELAY_WORKSPACE_MIN_FREE_GB` is, since in that configuration
   nothing ever reclaims disk.
4. **`CreateStreamClient` regenerating operator edits on every `Prepare`** (F6). A widening of an
   existing behaviour rather than a new one, but it changes when a hand-edited client spec is
   overwritten from "next first use" to "next task". Worth a README sentence if a studio reports it;
   not worth code today.

## 12. Open question for the human

None that blocks. The one worth a look on review is **D2**: whether an unconditional `client -o`
plus `client -i` on every `Prepare` is acceptable against a Perforce server that is already a
contention point in some studios. The design says yes, with the numbers in 3.1 and the failure mode
of the alternative in D2. A reviewer who disagrees should say so before the plan is written, because
the conditional would have to be designed with its false-positive path pinned by a test, and that is
a different slice rather than a patch.
