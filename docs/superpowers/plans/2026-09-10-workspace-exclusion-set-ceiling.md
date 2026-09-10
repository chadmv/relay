# Workspace Exclusion-Set Ceiling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound, per stream and per agent, how many *exclusion-derived* Perforce workspaces one agent will hold - evicting the least valuable unheld one to admit a new task and refusing only when every slot is held by a running task - and bound how many rows one `applyInventory` batch processes on the coordinator by truncating rather than refusing.

**Architecture:** One new file `internal/agent/source/perforce/exclusion_ceiling.go` holding an env resolver with a `getenv` seam, a pure candidate-ordering function, and one `*Provider` method called from `Prepare`'s **cold** branch only, between `reg.GetBySourceKey` and `allocateShortID`. Eviction is delegated entirely to the existing `Provider.EvictWorkspace` - this slice deletes nothing itself. One exactness predicate added beside `SourceKey` in `sourcekey.go`. On the coordinator, one compile-time row-count bound ahead of `applyInventory`'s transaction plus one new `atomic.Uint64` on `Handler` with an exported reader. Three README edits. No migration, no SQL, no `internal/api`, no `cmd/` change.

**Tech Stack:** Go 1.26, testify, testcontainers-go (p4d image under `internal/agent/source/perforce/testdata/p4d`), pgx/v5 via the untagged `fakePool`/`fakeTx` fixture.

**Spec:** `docs/superpowers/specs/2026-09-10-workspace-exclusion-set-ceiling.md`. Cite by section; do not re-derive. **Read "What this plan refutes in the spec" below BEFORE the spec itself.** Seven findings; three change code or a test input you would otherwise write wrong, and one of them means a test the spec prescribes *cannot work as written*.

**Item:** `docs/backlog/idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates.md`. This slice closes it. Close with `/backlog close a-job-author-controls-how-many-p4-clients` (the conductor's step, Task 14), never by editing `status` in place.

---

## Slice independence declaration

**This slice has ONE lane. It is backend-only. There is no frontend slice and no Phase 3 parallelism.**

- Files touched live in `internal/agent/source/perforce/`, `internal/worker/`, and `README.md`. **Zero files under `web/`.** Zero files under `internal/api/`, `cmd/`, `internal/jobspec/`, `internal/schedrunner/`, `internal/store/`.
- Tasks are **strictly sequential**. Four orderings are load-bearing:
  1. **Task 0 runs first and can CANCEL this slice.** Its step 1 measures the premise the whole agent-side design rests on. See the STOP branch.
  2. **Task 1 (the predicate) before Tasks 3-8.** Every later function counts through it.
  3. **Task 2 (the resolver) before Task 4.** Task 4's gate reads `p.maxExclusionSets`, which Task 2 creates.
  4. **Task 12 (mutation battery) after Tasks 1-9 and before Task 13.** A battery against a half-built control reports survivors that are not survivors.
- The agent half (Tasks 1-8) and the coordinator half (Task 9) touch disjoint packages and could in principle be two PRs. **Keep them one PR**: Task 9 is about 40 lines and the spec's section 4 table is the argument for shipping both bounds together. If the human cuts the coordinator half (spec open question 3), drop Task 9 and say so in the PR body - nothing else in this plan depends on it.

Dispatch one `relay-backend-engineer` for Tasks 0-13 in order. Task 14 is the conductor's.

---

## What this plan refutes in the spec

The spec was read once for content and once asking only whether it contradicts itself, contradicts the tree, or prescribes something that does not exist. Seven findings.

### 1. CONFIRMED: acceptance criterion 3 is already satisfied at HEAD

`README.md:599` already ends with the sentence spec refutation 1 quotes, verbatim:

> Each distinct exclusion set also mints its own workspace directory and its own persistent p4 client spec on the shared Perforce server, and both are created before the exclusion paths are checked, so a spec whose exclusions name nothing leaves a directory and a client behind before it fails.

Task 10 **appends** to that paragraph and rewrites none of it. Do not add a second sentence saying what the existing one says.

### 2. CONFIRMED: `Sweeper.Run` returns immediately when both knobs are zero

`sweeper.go:71-74` is `if s.MaxAge == 0 && s.MinFreeGB == 0 { return }`, and there is no other caller of `SweepOnce` in production. So on a default-configured agent **nothing reclaims a workspace, ever** - not the age pass either. Spec refutation 4 stands, and it is the reason the ceiling evicts instead of refusing. Build on it; do not re-derive it and **do not state it in a code comment** (it is a claim about another file's behaviour, and it would go stale if the sweeper's construction changed).

### 3. REFUTED, AND IT CHANGES A TEST INPUT: the spec's pipe example does not discriminate

Spec section 4 says *"a suffix test alone would let `//a|b`'s workspaces be counted against `//a`"*. It would not. The composite key for stream `//a|b` is `x1|<16 hex>|//a|b`, and the suffix test for stream `//a` looks for `|//a`, which that key does not end with. **The collision runs the other way: a suffix-only predicate miscounts a LONGER stream's key against a SHORTER stream whenever the longer stream ends with `"|"` followed by the shorter one.**

Verified that `|` is legal in a stream: `validateSourceSpec` (`internal/jobspec/jobspec.go:566-574`) requires only a `//` prefix and no control byte. Nothing forbids `|`.

The input that discriminates, and the one Task 1's test must use:

- outer stream `//depot/a|//depot/b` - its composite key is `x1|<16 hex>|//depot/a|//depot/b`, which **does** end with `|//depot/b`.
- inner stream `//depot/b` - the key must NOT be counted for it.

The length equality is then provably exact, not merely better: if `len(key) == len(tag)+16+1+len(stream)` and `key` ends with `"|"+stream`, then the trailing `len(stream)` bytes are both `stream` and the encoded stream, so they are equal. Task 1's test asserts the suffix premise first, so it cannot silently stop discriminating.

### 4. REFUTED, AND THE TEST THE SPEC PRESCRIBES CANNOT WORK: `client -i` carries no name in its argv

Spec section 13 test A2 says: assert *"that no `client -i` for the new name appears in the recorded argv"*. There is no such thing. `Client.CreateStreamClient` (`client.go:158`) issues `c.r.Run(ctx, "", []string{"client", "-i"}, bytes.NewReader(spec))` - the argv is two elements and **the client name travels on stdin**. Every client creation in the process has the identical `client -i` argv.

The call that *does* carry the name is the one before it: `args := []string{"client", "-o", "-S", stream}` then `append(args, name)` (`client.go:135-139`), so `client -o -S <stream> <name>` is a 5-element argv whose last element is the new client name. That is also the **earlier** of the two, so it is the stronger assertion.

And there is an even earlier mint: `os.MkdirAll(wsRoot, ...)` at `perforce.go:342` runs before `CreateStreamClient` at `:356`. Task 5 therefore asserts **both** absences - no argv element equal to the new client name, and no directory at `<root>/<new short id>`.

### 5. REFUTED: Task 0 item 2 is answerable from the tree, and the answer is the unfavourable branch

Spec section 11 item 2 leaves open whether the Makefile names `./internal/agent/...` or `./internal/agent`. It is `./internal/agent` (`Makefile:168`), and the comment above it at `:148-154` says the bare form is **load-bearing**, because the `/...` form would match `internal/agent/source/perforce`, whose tests `t.Skip` without a `p4` binary, making the job report green having run nothing.

Two consequences, both already decided:

- **`internal/agent/source/perforce`'s integration tests do not run in CI and must not be wired in by this slice.** Adding `./internal/agent/...` to that target is an explicitly forbidden change.
- The written excuse CLAUDE.md requires **already exists**, one level up, in `startP4dContainer`'s own comment (`p4d_container_test.go:40-47`), which names all three things a workflow job would have to supply. Task 8's test comment **points at it in one clause rather than restating the census** - a restated census is a claim about other files that will go stale.

### 6. CONFIRMED and sharpened: the honest bound is `K + concurrent prepares - 1`

Verified against `Prepare`'s locking. The count is read from `reg.Snapshot()` (which takes and releases `r.mu`, `registry.go:117-123`); `p.mu` is not taken until `:310`; the mint is at `:342`/`:356`. **No lock spans the check and the mint.** So two concurrent cold prepares for two distinct new keys on one stream can both pass. The comment must say `K + concurrent prepares on that stream - 1`, and **no test and no comment may assert a hard `K`**. Task 4's registry-count assertion is written as "back at the ceiling" on a single-goroutine test, which is a statement about that test's sequence, not a hard bound.

### 7. Two corrections to the spec's own enumerations, neither load-bearing

- Spec section 11 item 5 asks for the highest migration number. It is `000024_worker_workspace_text_bounds` (`internal/store/migrations/`). Confirmed from the directory; this slice adds none. Task 0 still re-reads the directory rather than trusting this line, because a sibling lane is running.
- Spec section 11 item 7 asks which seam makes a workspace un-evictable. It exists and is exactly `TestEvictWorkspace_RefusesHeldWorkspace`'s (`provider_evict_test.go:105-112`): insert a `NewWorkspace(shortID)` into `p.workspaces` under `p.mu`, then `w.Acquire(ctx, Request{SyncPaths: ...})` and keep the handle. `EvictWorkspace`'s holder check reads `p.workspaces[shortID]` only, so **a registry entry with no in-memory `Workspace` has no holder and is always evictable** - which is what makes Task 4's happy path work with no seam at all.

---

## The binding warning slice 1 handed forward

Slice 1 (PR #209) closed an inventory **freeze**: refusing a whole batch rolls `ReplaceWorkerInventory`'s DELETE back with everything else, so the worker keeps its previous rows and an agent that reports the same bad inventory on every registration **never updates its inventory again**. Task 9 must **truncate, never refuse**. Its `commits == 1` assertion is the guard, and `fakeTx`'s own comment (`handler_register_success_test.go:80-93`) records that mutation M15 - making the closure return an error - left all 21 packages green before those counters existed.

`inventoryUpsertParams` (`internal/worker/inventory_params.go`) is the sole production route to `store.UpsertWorkerWorkspaceParams` and bounds four columns in BYTES; a refused row is dropped, the batch commits, and the drop is counted into `inventoryRowRejects`. **Do not fold the new count into that counter** - distinct nouns, Task 9's B3 is the guard.

---

## Environment notes (read once)

- The tree is the worktree `D:/dev/relay/.claude/worktrees/lane-p4-ceiling`, branch `claude/lane-p4-ceiling`. **Absolute paths only. Never `cd D:/dev/relay`** - that lands commits on the main repo's `main`.
- **Concurrent agents share one git index.** Commit with an explicit pathspec (`git add <exact paths>`), never `git add -A`. Read the reflog before any reset.
- **This slice changes no `.sql` and no `.proto`, so `make generate` is not run and the sqlc CRLF revert procedure does not arise.** If you find yourself running it, you are outside the scope fence.
- **After ANY programmatic edit to a tracked text file** (`README.md` especially): check the diffstat against the size of the change you intended, run `git ls-files --eol` on the touched paths (every one must read `i/lf`), and assert the file still decodes as UTF-8. Print the before/after line count and confirm the delta is what you intended. Prefer **exact-anchor replacement** over line numbers.
- **`git diff` and `git status` disagree by design** here (`core.autocrlf=true`). Never conclude "nothing to revert" from `git diff` alone.
- `gofmt -l` is useless as a signal on this tree: it lists hundreds of files under `internal/` on a clean checkout purely because of working-copy CRLF. Use `gofmt -l` scoped to the files you touched, and compare against the same command at `HEAD`.
- The p4d integration lane needs Docker Desktop **and** the `p4` CLI on PATH. Both skip conditions call `t.Skip`, so a missing `p4` produces a green run that tested nothing. **Read the skip lines in the output.**
- **Mutation batteries must not run in this worktree** - sibling agents are reading it. Task 12 uses `go test -overlay`, which keeps every mutated file outside the tree. **Never revert a mutation with `git checkout --`**; it discards the uncommitted guard under test.
- `go test -race` may be unrunnable locally (ThreadSanitizer arena allocation, environmental). If it is, **say so plainly**; `-count=N` repetition is not a substitute and must not be reported as one.

## Task index

0. Measurements, the gating probe, and green baselines (**can cancel this slice**)
1. The shared encoding constants and `isExclusionKeyForStream`
2. The resolver, its `getenv` seam, and the `Provider` field
3. `exclusionEvictionCandidates` - the pure ordering
4. The gate in `Prepare`'s cold branch: evict, then admit
5. The refusal, and that it names no occupant
6. The base workspace is never a candidate
7. A warm prepare at the ceiling is not gated
8. The p4d guard: a client for a stream that does not exist
9. Coordinator: truncate the batch, count the drops
10. README: one appended paragraph sentence, one eviction sentence, one table row
11. Comments review pass
12. Mutation battery
13. Whole-slice verification
14. PR and backlog close (conductor)

---

### Task 0: Measurements, the gating probe, and green baselines

**This task produces no code.** Every output is recorded in the block at the end of this task and repeated in the PR body.

**What Task 0 can and cannot change.** Step 1 can **invalidate the design and cancel this slice**. Steps 2-8 can move a number, a lane, or a test mechanic. **None of steps 2-8 can move** the enforcement point, the evict-then-admit decision, the no-disable rule, or the two-counters decision - none of those depend on a measurement. Do not let a measurement be read as licence to revisit a decision it does not touch.

- [ ] **Step 1: GATING. Can a p4 client spec be persisted for a stream that does not exist?**

**Do not assume an answer to this step. It has not been measured. The design's central claim depends on the answer and the answer is recorded below before any code is written.**

The claim under test: before exclusions existed, the number of workspaces one agent could be driven to create was bounded by **the number of real streams**, because a client cannot be created for a stream that is not there. This slice's per-stream ceiling only *moves* that bound if the claim holds. If it does not hold, a job author can invent streams as freely as exclusion sets, a per-stream ceiling bounds nothing in aggregate, and the design must come back for re-scoping.

**The question is NOT "does `p4 client -o -S <bogus>` exit non-zero".** `p4` exits zero on plenty of things it also complains about on stderr - that is the whole reason `PathHasFiles` asserts positively on stdout (`client.go:209-214`). The question is **whether a client spec for a non-existent stream can be PERSISTED**, because the persisted spec plus the directory are the artifacts the ceiling bounds.

Procedure. Start the fixture server the way the lane already does, then measure three things in order:

```
cd D:/dev/relay/.claude/worktrees/lane-p4-ceiling
go test -tags integration ./internal/agent/source/perforce/ -run TestPerforce_E2E_PathHasFilesReadingsAreCaptured -v -timeout 1800s
```

Confirm that run does **not** print `--- SKIP`. If it skips, `p4` is not on PATH or Docker is unreachable, and **this step's output is "not measured", not a guess** - see the STOP branch below.

Then, against the same container (write a scratch `//go:build integration` test under the scratchpad and run it via `-overlay`, or add a temporary test and delete it before committing), capture all three:

1. `p4 client -o -S //test/no-such-stream-qqzz relay_probe_bogus` - record **exit status, stdout, stderr** separately.
2. If (1) produced a spec on stdout, pipe it to `p4 client -i` - record **exit status, stdout, stderr**.
3. Regardless: `p4 clients -e relay_probe_bogus` - record whether a client now exists on the server. Clean up with `p4 client -d relay_probe_bogus` if it does.

**The STOP branch, and it is a branch, not a footnote:**

> **If a client spec for `//test/no-such-stream-qqzz` can be persisted** (step 2 exits zero, or step 3 shows the client exists), then **STOP. Write no code. Report to the conductor**: the premise in spec sections 1, 4 and 10 item 3 does not hold, the per-stream ceiling bounds a population the author can widen at will, and the spec must be re-scoped - most likely toward a per-agent total rather than a per-stream ceiling. Record the three captures verbatim in the report. **Do not "adjust in flight."**
>
> **If it cannot be persisted**, record which of the three calls refused it and with what wording, and proceed. Task 8 turns this observation into a permanent guard.
>
> **If the lane could not run at all** (no `p4`, no Docker), record "not measured, lane unavailable" and **report that to the conductor before proceeding**. The conductor decides whether to proceed with the premise unmeasured. Do not decide that yourself, and do not substitute a documentation reading for the measurement.

- [ ] **Step 2: Is `internal/agent/source/perforce` in a lane CI runs?**

Read `Makefile:167-168` and the comment at `:148-154`. Then confirm with the command that comment names:

```
go test -tags integration -list ".*" ./internal/agent
```

Expected: the list contains **no** test name from `internal/agent/source/perforce`.

- **If the package path is `./internal/agent` (expected):** the p4d lane is human-run. Task 8's test comment must name what would have to exist for it to run - and it does so by pointing at `startP4dContainer`'s existing comment (`p4d_container_test.go:40-47`) in one clause, not by restating it. **Do not add `./internal/agent/...` to the Makefile** - the comment there explains why the bare form is load-bearing, and changing it makes the job report green having run nothing.
- **If the package path is `./internal/agent/...`:** the lane runs in CI and Task 8 needs no written excuse - but also check whether `.github/workflows/go-ci.yml`'s `pg-integration` job installs the `p4` CLI. If it does not, the lane is still effectively unrun and the first branch applies anyway.

Record which branch fired. **Do not put this fact in a code comment** - it is a census of other files.

- [ ] **Step 3: grpc-go's default receive-message limit**

`grpcServerOptions` (`cmd/relay-server/grpc_config.go:68-74`) sets exactly three options and none is `MaxRecvMsgSize`, so grpc-go's default applies and it is what bounds the coordinator-side entry count today. `go.mod:17` pins `google.golang.org/grpc v1.80.0`.

```
go doc google.golang.org/grpc MaxRecvMsgSize
go env GOMODCACHE
rg -n "defaultServerMaxReceiveMessageSize" "$(go env GOMODCACHE)/google.golang.org/grpc@v1.80.0/"
```

Record the constant's value in bytes and the version it came from. **No comment written in Task 9 may state a number for what bounds `len(inv)` today until this is recorded**, and if the comment states it, it must state it as a grpc default rather than as relay's own bound.

- [ ] **Step 4: Does the untagged `fakePool`/`fakeTx` fixture accept a 4098-entry batch?**

`fakeTx.Exec` appends to a slice and returns a command tag (`handler_register_success_test.go:105-119`); `newInventoryFixture` (`inventory_bounds_test.go:20-27`) builds the pair with no Postgres. Expected answer: yes, and cheaply. Measure rather than assume - the failure mode is a slow test, not a wrong one:

```
go test ./internal/worker/ -run TestApplyInventory -count=1 -v
```

Then, once Task 9's B1 exists, record its wall-clock time. If a 4098-entry batch takes more than a couple of seconds, say so - it does not change the design, it changes whether B1 gets a `testing.Short` guard.

- [ ] **Step 5: Which seam makes a workspace un-evictable in a test**

Read `provider_evict_test.go:86-121` and `sweeper_claim_test.go`. Record the exact seam. Expected, verified while planning: insert a `NewWorkspace(shortID)` into `p.workspaces` under `p.mu`, then hold a handle from `w.Acquire(ctx, Request{SyncPaths: []string{...}})`. Record whether any *other* seam exists, because Task 5 depends on this one.

- [ ] **Step 6: SEARCH - does any existing perforce fixture pre-populate several composite keys for one stream?**

**This is a complement claim, so it is a SEARCH with a recorded hit count and the command, not a targeted read.** A fixture holding four or more exclusion-derived registry entries for one stream would go red the moment the default ceiling is 4.

Run all four and record each hit count:

```
rg -n --glob "internal/agent/source/perforce/*_test.go" "x1\|"
rg -n --glob "internal/agent/source/perforce/*_test.go" "SourceKey\("
rg -n --glob "internal/agent/source/perforce/*_test.go" "reg\.Upsert|Registry\{"
rg -n --glob "internal/**/*_test.go" "Exclude:\s*true"
```

Record: total hits per command, and for every `reg.Upsert` site whether its `SourceKey` is a bare stream or a composite. Planning-time reading suggested every existing seeded entry uses a bare stream, so the expected finding is **zero fixtures at risk** - but that is an expectation to falsify, not a result. If any fixture is at risk, fix it by making that test set its own ceiling with `t.Setenv`, never by raising the default.

- [ ] **Step 7: README anchors at implementation time**

A sibling lane is editing README in this batch. Re-derive every anchor; **cite by anchor text, never by line number**, in Task 10 and in the PR body.

```
rg -n "Exclusions change the workspace identity" README.md
rg -n "RELAY_WORKSPACE_CLOBBER" README.md
rg -n "Active workspaces \(held by a running task\) are never evicted" README.md
```

Record the three line numbers **as of the run** and the full current text of the `Exclusions change the workspace identity.` paragraph. If any anchor text has changed, report before editing.

- [ ] **Step 8: Green baselines, before any mutation**

A uniform result across a mutation battery means a broken harness, and a compile error is not a kill. Establish both baselines now, on a clean tree.

```
go test ./internal/agent/source/perforce/ -count=1
go test ./internal/worker/ -count=1
```

Expected: both PASS. Record the `ok` line and the wall clock for each. Also record:

```
git status --porcelain
```

Expected: clean, or only files this plan has not touched yet. Record it - Task 12 restores against this.

- [ ] **Step 9: Record the results**

Fill this block in, in the plan file, replacing every `<...>`. **A blank slot is a missing measurement, not an implied default.**

```
## Task 0 results

M1 GATING - persisted client for a non-existent stream:
    Lane ran (no SKIP):           yes - p4 2026.1 on PATH, Docker Desktop 29.4.0, p4d image built,
                                  container started, "p4d ready" observed, no --- SKIP
    CONTROL (real //test/main):   p4 client -o -S //test/main relay_probe_real
                                  exit 0; stdout = a full client spec (Stream: //test/main,
                                  View: //test/main/... //relay_probe_real/...); stderr empty.
                                  The instrument works: a real stream yields a spec.
    p4 client -o -S <bogus>:      p4 client -o -S //test/no-such-stream-qqzz relay_probe_bogus
                                  exit 1; stdout EMPTY; stderr: Stream '//test/no-such-stream-qqzz'
                                  doesn't exist.
    p4 client -i (if reached):    NOT REACHED. client -o produced no spec on stdout, so there was
                                  nothing to pipe. The refusal is at the FIRST of the two calls.
    p4 clients -e <probe> after:  no client (exit 0, stdout empty). p4 clients (unfiltered) lists
                                  only the fixture's own `setup-client`, so the empty result is not
                                  an artefact of the -e filter.
    Production path:              Client.CreateStreamClient(ctx, "relay_probe_bogus_prod", tmp,
                                  "//test/no-such-stream-qqzz", "", false) returns
                                  `p4 client -o -S ... : exit status 1 (stderr: Stream '...'
                                  doesn't exist.)`, and p4 clients -e relay_probe_bogus_prod is
                                  empty afterwards.
    VERDICT: CANNOT be persisted - premise holds, proceed.

M2 Makefile lane: test-pg-integration names ./internal/agent (bare), Makefile:168.
    `go test -tags integration -list ".*" ./internal/agent` listed 84 tests; perforce names present: no.
    Branch taken for Task 8's comment: points at startP4dContainer. The bare path is load-bearing
    per the Makefile's own comment, so ./internal/agent/... must NOT be added.

M3 grpc-go default receive limit: 4194304 bytes (1024*1024*4,
    defaultServerMaxReceiveMessageSize, server.go:60), google.golang.org/grpc v1.80.0.

M4 fakeTx with 4098 entries: accepted. The fixture is in-memory only (fakeTx.Exec appends to a
    slice), and the existing TestApplyInventory family runs in 0.086s; B1's own wall clock is
    recorded in the verification block.

M5 un-evictable seam: insert NewWorkspace(shortID) into p.workspaces under p.mu, then hold a handle
    from w.Acquire(ctx, Request{SyncPaths: ...}) - TestEvictWorkspace_RefusesHeldWorkspace's seam,
    which yields EvictWorkspace's "currently in use". Other seams found: one - setting
    p.evicting[shortID] via ReserveForEvict and keeping the release closure, which yields "already
    being evicted". The holder seam is the one Task 5 uses, because "held by a running task" is the
    condition the refusal is documented against.

M6 composite-key fixture search (hit counts):
    "x1\|" 3   "SourceKey(" 24   "reg.Upsert|Registry{" 56   "Exclude: true" 45
    Fixtures holding >=4 composite entries for one stream: zero. Enumerated every `SourceKey:`
    literal in the package's test files (15 distinct forms, 37 occurrences): all are bare streams
    ("//s/x", "//s/y", "//s/a", "//s/b", "//s/good", "//s/bad", "//s/stuck", "//depot/main",
    "//s/"+id, fmt.Sprintf("//s/%d")). None is composite. The three "x1|" hits are two assertions
    and one comment, not seeded entries.

M7 README anchors as of 01deeadc: "Exclusions change..." line 599; RELAY_WORKSPACE_CLOBBER row
    line 510; "Active workspaces..." line 609 (same line also carries "Admins can also evict on
    demand via"). README is 2239 lines. Anchor text changed since the spec: no.

M8 green baseline: perforce ok, 1.139s; worker ok, 3.737s; working tree clean
    (git status --porcelain empty).

Numbers in force for this slice: RELAY_WORKSPACE_MAX_EXCLUSION_SETS default 4,
hard maximum 64, maxInventoryRowsPerBatch 4096.
Migrations: highest is 000024_worker_workspace_text_bounds; this slice adds none.
Unchanged by Task 0 regardless of result: the cold-branch enforcement point, evict-then-admit,
no value disables the ceiling, two distinct counters.
```

- [ ] **Step 10: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/lane-p4-ceiling
git add docs/superpowers/plans/2026-09-10-workspace-exclusion-set-ceiling.md
git commit -m "docs: record Task 0 measurements for the workspace exclusion-set ceiling"
```

---

### Task 1: The shared encoding constants and `isExclusionKeyForStream`

**Files:**
- Modify: `internal/agent/source/perforce/sourcekey.go` (add two constants; rewrite the `return` at `:70` to use them; add the predicate)
- Test: `internal/agent/source/perforce/sourcekey_test.go` (append two tests; **change no existing line**)

**Property pinned:** the predicate is the exact inverse of `SourceKey`'s encoding, including against a stream containing `|`. A suffix-only predicate counts a longer stream's workspace against a shorter stream, so the ceiling would evict and refuse on a population that is not its own.

**Why the constants are shared:** a predicate that disagrees with the builder returns false for every composite key, the count is always zero, and the ceiling fails **open** - the dangerous direction. Sharing the tag and the digest width makes an `x2` move a compile-level coupling.

- [ ] **Step 1: Write the failing tests**

Append to `internal/agent/source/perforce/sourcekey_test.go`:

```go
// THE DISCRIMINATING INPUT IS A STREAM THAT ENDS WITH "|" FOLLOWED BY ANOTHER
// STREAM. A "|" is legal in a stream - validateSourceSpec asks only for a "//"
// prefix and no control byte - so a suffix-only predicate counts the longer
// stream's composite key against the shorter stream, and a ceiling counting
// through it evicts and refuses on a population that is not its own.
//
// The suffix premise is asserted FIRST. Without it this test stops
// discriminating the moment the encoding changes, and reads as passing.
func TestIsExclusionKeyForStream_IsExactAgainstAStreamContainingAPipe(t *testing.T) {
	const outer = "//depot/a|//depot/b"
	const inner = "//depot/b"

	key := SourceKey(&relayv1.PerforceSource{
		Stream: outer,
		Sync: []*relayv1.SyncEntry{
			{Path: outer + "/...", Rev: "#head"},
			{Path: outer + "/heavy/...", Exclude: true},
		},
	})

	require.True(t, strings.HasSuffix(key, "|"+inner),
		"the input only discriminates if a suffix-only predicate WOULD have matched")

	require.True(t, isExclusionKeyForStream(key, outer),
		"the key must be counted for the stream it was built from")
	require.False(t, isExclusionKeyForStream(key, inner),
		"and never for a shorter stream its encoding happens to end with")
}

// The base workspace is outside the population BY CONSTRUCTION, and this is the
// single guard for that. A ceiling that counted the bare stream key, or that
// could pick it as an eviction candidate, lets a job author with a handful of
// junk exclusion sets destroy the workspace every non-exclusion task on that
// stream shares.
func TestIsExclusionKeyForStream_ABareStreamIsNotAnExclusionKey(t *testing.T) {
	require.False(t, isExclusionKeyForStream("//s/x", "//s/x"))
	require.False(t, isExclusionKeyForStream("", "//s/x"))
	require.False(t, isExclusionKeyForStream("x1|deadbeefdeadbeef|//s/y", "//s/x"),
		"a composite for another stream is not this stream's")
}
```

`strings` and `relayv1` are already imported by that file. No new import.

- [ ] **Step 2: Run the tests to verify they fail**

```
go test ./internal/agent/source/perforce/ -run TestIsExclusionKeyForStream -v -count=1
```

Expected: **compile failure**, `undefined: isExclusionKeyForStream`. That is the RED for a new symbol.

- [ ] **Step 3: Write the implementation**

In `internal/agent/source/perforce/sourcekey.go`, add `"strings"` to the import block, and insert **above** `func SourceKey`:

```go
// The two fixed parts of the exclusion-derived key encoding, shared by SourceKey
// below and isExclusionKeyForStream beneath it.
//
// THEY ARE SHARED SO A VERSION MOVE CANNOT DESYNCHRONISE THE PAIR. A predicate
// that no longer matches the builder returns false for every composite key, so
// anything counting through it counts zero and fails OPEN - the dangerous
// direction for a ceiling.
const (
	sourceKeyVersionTag = "x1|"
	sourceKeyDigestHex  = 16
)
```

Replace the `return` at the end of `SourceKey` (currently `return "x1|" + hex.EncodeToString(h.Sum(nil))[:16] + "|" + p.GetStream()`) with:

```go
	return sourceKeyVersionTag + hex.EncodeToString(h.Sum(nil))[:sourceKeyDigestHex] + "|" + p.GetStream()
```

Append at the end of the file:

```go
// isExclusionKeyForStream reports whether key is an exclusion-derived workspace
// key for exactly this stream. It is the inverse of SourceKey's encoding above
// and lives beside it so the two move together.
//
// THE LENGTH EQUALITY IS WHAT MAKES IT EXACT RATHER THAN A HEURISTIC. A stream
// may contain "|", so a suffix test alone matches whenever a longer stream's
// encoding ends with "|" followed by this shorter stream. With the length
// pinned, key's trailing len(stream) bytes can only be this stream's.
// TestIsExclusionKeyForStream_IsExactAgainstAStreamContainingAPipe carries the
// input.
//
// A bare stream key cannot reach the true branch: no legal stream starts with
// the version tag. That is what keeps the stream's shared base workspace outside
// every population computed through this function.
func isExclusionKeyForStream(key, stream string) bool {
	if len(key) != len(sourceKeyVersionTag)+sourceKeyDigestHex+1+len(stream) {
		return false
	}
	return strings.HasPrefix(key, sourceKeyVersionTag) &&
		strings.HasSuffix(key, "|"+stream)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/agent/source/perforce/ -run TestIsExclusionKeyForStream -v -count=1
go test ./internal/agent/source/perforce/ -count=1
git diff --stat internal/agent/source/perforce/sourcekey_test.go
```

Expected: both PASS. **The third command must show only additions** - the `SourceKey` edit is behaviour-preserving, so every pre-existing test line stays byte-identical. A diff showing a changed existing test line means the refactor changed behaviour; stop and find out why.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/sourcekey.go internal/agent/source/perforce/sourcekey_test.go
git commit -m "perforce: add an exact predicate for exclusion-derived keys per stream

The exclusion-set ceiling counts a population, and a suffix-only reading of the
encoding counts a longer stream's workspace against a shorter one: a stream may
contain '|' (validateSourceSpec requires only a '//' prefix and no control
byte), so //depot/a|//depot/b's composite key ends with '|//depot/b'. The length
equality makes the predicate exact. The version tag and digest width become
shared constants so an x2 encoding cannot move the builder without the
predicate - a desynchronised predicate counts zero and fails open."
```

---

### Task 2: The resolver, its `getenv` seam, and the `Provider` field

**Files:**
- Create: `internal/agent/source/perforce/exclusion_ceiling.go`
- Modify: `internal/agent/source/perforce/perforce.go` (`Provider` struct at `:117-124`; `New` at `:127-133`)
- Test: `internal/agent/source/perforce/exclusion_ceiling_test.go`

**Property pinned:** **no value disables this ceiling, and `0` is not one.** Its two neighbours in the same README table (`RELAY_WORKSPACE_MAX_AGE`, `RELAY_WORKSPACE_MIN_FREE_GB`) both mean "disabled" at zero, so an operator will try it here. And the value is resolved **inside the package**, not taken as a `Config` field: a `Config` field defaults to the zero value, and a zero here must never mean "unlimited", so an unwired `main.go` would silently switch a safety control off.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/source/perforce/exclusion_ceiling_test.go`:

```go
package perforce

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// envOnly returns a getenv that answers only for the ceiling variable, so a
// table row cannot pass because of something else in the real environment.
func envOnly(value string) func(string) string {
	return func(k string) string {
		if k == maxExclusionSetsEnv {
			return value
		}
		return ""
	}
}

// THE ZERO ROW IS WHY THIS TABLE EXISTS. Its two neighbours in README's agent
// table mean "disabled" at zero, so the text is asserted, not just the number:
// a resolver that returned the default silently would leave an operator
// believing the control is off.
func TestResolveMaxExclusionSets(t *testing.T) {
	cases := []struct {
		name        string
		value       string
		want        int
		wantWarning bool
		wantText    string
	}{
		{"unset yields the default and says nothing", "", defaultMaxExclusionSets, false, ""},
		{"a value inside the range is used verbatim", "8", 8, false, ""},
		{"the maximum itself is used verbatim", "64", 64, false, ""},
		{"zero does not disable it", "0", defaultMaxExclusionSets, true, "0 does not disable this ceiling"},
		{"a negative value does not disable it", "-1", defaultMaxExclusionSets, true, "0 does not disable this ceiling"},
		{"an unparseable value falls back", "abc", defaultMaxExclusionSets, true, "cannot be switched off"},
		{"above the maximum clamps", "1000", maxMaxExclusionSets, true, "clamped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings := resolveMaxExclusionSets(envOnly(tc.value))
			require.Equal(t, tc.want, got)
			if !tc.wantWarning {
				require.Empty(t, warnings, "a value used verbatim must not warn")
				return
			}
			require.Len(t, warnings, 1)
			require.Contains(t, warnings[0], tc.wantText)
			require.Contains(t, warnings[0], maxExclusionSetsEnv,
				"a warning an operator cannot trace to a variable is noise")
		})
	}
}

// The ceiling is resolved in New, inside the package. A Config field would
// default to zero, and a zero here must never mean unlimited, so an unwired
// main.go has to fail CLOSED.
func TestNew_ResolvesTheCeilingInsideThePackage(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "7")
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
	require.Equal(t, 7, p.maxExclusionSets)
}

// THE CONTROL, and the one that matters. With nothing wired anywhere, the
// ceiling is still in force at its default.
func TestNew_AnUnsetEnvironmentStillCarriesTheCeiling(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "")
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
	require.Equal(t, defaultMaxExclusionSets, p.maxExclusionSets)
}

// A Provider whose field is zero must use the DEFAULT, never "unlimited" and
// never "refuse everything": a bare `count >= 0` comparison refuses every cold
// exclusion prepare, which is fail-closed and still a catastrophe.
func TestExclusionCeiling_AZeroFieldMeansTheDefault(t *testing.T) {
	require.Equal(t, defaultMaxExclusionSets, (&Provider{}).exclusionCeiling())
	require.Equal(t, 9, (&Provider{maxExclusionSets: 9}).exclusionCeiling())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go test ./internal/agent/source/perforce/ -run "TestResolveMaxExclusionSets|TestNew_|TestExclusionCeiling_" -v -count=1
```

Expected: **compile failure**, `undefined: maxExclusionSetsEnv`, `undefined: resolveMaxExclusionSets`, `undefined: defaultMaxExclusionSets`, `p.maxExclusionSets undefined`, `p.exclusionCeiling undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/agent/source/perforce/exclusion_ceiling.go`:

```go
package perforce

import (
	"fmt"
	"log"
	"strconv"
)

// maxExclusionSetsEnv is the operator knob for the per-stream, per-agent ceiling
// on exclusion-derived workspaces.
const maxExclusionSetsEnv = "RELAY_WORKSPACE_MAX_EXCLUSION_SETS"

// The ceiling's default and its hard maximum.
//
// 4: the design intent is one exclusion-derived workspace per stream per agent,
// and "with and without the heavy subtree" is two. Each slot costs a nearly
// full-size workspace, so this number multiplies disk directly - which is the
// reason not to default it higher.
//
// 64: the knob only ever LOOSENS a control, so it needs a bound of its own.
const (
	defaultMaxExclusionSets = 4
	maxMaxExclusionSets     = 64
)

// resolveMaxExclusionSets reads the knob and returns the effective ceiling plus
// any warning an operator must see.
//
// NO VALUE DISABLES THIS CEILING AND 0 IS NOT ONE. Two variables beside it in
// the same operator-facing table mean "disabled" at zero, so an operator will
// try it here; 0, a negative value and an unparseable value all resolve to the
// default and say so in the warning.
//
// THE WARNINGS ARE RETURN VALUES, not log calls, so the table test reads the
// text without capturing a logger.
func resolveMaxExclusionSets(getenv func(string) string) (int, []string) {
	raw := getenv(maxExclusionSetsEnv)
	if raw == "" {
		return defaultMaxExclusionSets, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultMaxExclusionSets, []string{fmt.Sprintf(
			"%s is not an integer; using the default of %d. This ceiling cannot be switched off.",
			maxExclusionSetsEnv, defaultMaxExclusionSets)}
	}
	if n <= 0 {
		return defaultMaxExclusionSets, []string{fmt.Sprintf(
			"%s must be a positive integer; 0 does not disable this ceiling and neither does a "+
				"negative value. Using the default of %d. This ceiling cannot be switched off.",
			maxExclusionSetsEnv, defaultMaxExclusionSets)}
	}
	if n > maxMaxExclusionSets {
		return maxMaxExclusionSets, []string{fmt.Sprintf(
			"%s is above the hard maximum and was clamped to %d.",
			maxExclusionSetsEnv, maxMaxExclusionSets)}
	}
	return n, nil
}

// newMaxExclusionSets resolves the ceiling for a Provider and reports any
// warning on the agent's own log. New calls this so the value is resolved
// in-package: a Config field would default to the zero value, and a zero here
// must never mean "unlimited", so an unwired caller has to fail closed.
func newMaxExclusionSets(getenv func(string) string) int {
	n, warnings := resolveMaxExclusionSets(getenv)
	for _, w := range warnings {
		log.Printf("perforce: %s", w)
	}
	return n
}

// exclusionCeiling is the effective ceiling for this provider.
//
// A ZERO FIELD MEANS THE DEFAULT. It cannot mean "unlimited" - that is the
// failure this knob is resolved in-package to avoid - and it must not mean
// "refuse everything", which is what a bare comparison against zero would do to
// every cold exclusion prepare.
func (p *Provider) exclusionCeiling() int {
	if p.maxExclusionSets <= 0 {
		return defaultMaxExclusionSets
	}
	return p.maxExclusionSets
}
```

In `internal/agent/source/perforce/perforce.go`, add the field to `Provider` (after `reg`):

```go
	// maxExclusionSets is the resolved per-stream ceiling on exclusion-derived
	// workspaces. Per-provider rather than a package var so a test needs no
	// global mutation and no t.Cleanup restore. Read through exclusionCeiling.
	maxExclusionSets int
```

and rewrite `New`'s return:

```go
	return &Provider{
		cfg:              cfg,
		workspaces:       map[string]*Workspace{},
		evicting:         map[string]bool{},
		maxExclusionSets: newMaxExclusionSets(os.Getenv),
	}
```

`os` is already imported by `perforce.go`.

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/agent/source/perforce/ -run "TestResolveMaxExclusionSets|TestNew_|TestExclusionCeiling_" -v -count=1
go test ./internal/agent/source/perforce/ -count=1
```

Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/exclusion_ceiling.go internal/agent/source/perforce/exclusion_ceiling_test.go internal/agent/source/perforce/perforce.go
git commit -m "perforce: resolve the exclusion-set ceiling in-package, with no disable value

RELAY_WORKSPACE_MAX_EXCLUSION_SETS, default 4, hard maximum 64. Resolved in New
through a getenv seam rather than taken as a Config field: a Config field
defaults to the zero value and a zero here must not mean unlimited, so an
unwired main.go would silently disable a safety control. 0, a negative and an
unparseable value all resolve to the default with a warning that says so -
RELAY_WORKSPACE_MAX_AGE and RELAY_WORKSPACE_MIN_FREE_GB both mean disabled at
zero, so the contradiction is stated where an operator reads it."
```

---

### Task 3: `exclusionEvictionCandidates` - the pure ordering

**Files:**
- Modify: `internal/agent/source/perforce/exclusion_ceiling.go`
- Test: `internal/agent/source/perforce/exclusion_ceiling_test.go`

**Property pinned:** the candidate list holds exactly this stream's exclusion-derived entries, worst-first: empty `BaselineHash` ahead of any entry that has one, then `LastUsedAt` ascending.

**Why the empty baseline goes first, and why it is not a tie-break:** the cold path `Upsert`s with `BaselineHash: ""` (`perforce.go:365-372`) and the exclusion probe refuses at `:530-543` before any sync runs, so **an empty baseline on an unheld entry is exactly the residue a failed bogus prepare leaves** - an empty directory whose delete is free. Ordering it ahead of LRU means the attacker's junk is reclaimed before any warm workspace an operator paid to fill. The one legitimate row that can carry an empty baseline while unheld is a workspace whose agent died mid-sync, and `Prepare` already re-syncs a row whose baseline is `""` (`:382-388`, `:492-493`), so evicting it loses transferred bytes and no correctness.

- [ ] **Step 1: Write the failing tests**

Append to `internal/agent/source/perforce/exclusion_ceiling_test.go` (add `"fmt"`, `"time"` and `relayv1 "relay/internal/proto/relayv1"` to its imports):

```go
// entryFor builds a registry entry for one distinct exclusion set on stream,
// so each call yields a distinct composite source key.
func entryFor(t *testing.T, stream, tag, baseline string, lastUsed time.Time) WorkspaceEntry {
	t.Helper()
	key := SourceKey(&relayv1.PerforceSource{
		Stream: stream,
		Sync: []*relayv1.SyncEntry{
			{Path: stream + "/...", Rev: "@100"},
			{Path: fmt.Sprintf("%s/%s/...", stream, tag), Exclude: true},
		},
	})
	return WorkspaceEntry{
		ShortID:      "id-" + tag,
		SourceKey:    key,
		ClientName:   "relay_h_id-" + tag,
		BaselineHash: baseline,
		LastUsedAt:   lastUsed,
	}
}

// AN UNSYNCED ENTRY IS THE VICTIM EVEN WHEN IT IS THE NEWER ONE. The
// discriminating input is therefore an empty-baseline entry with the NEWER
// timestamp against a synced entry with the older one: under pure LRU the synced
// one is picked, which reclaims a workspace an operator paid to fill in order to
// protect a failed prepare's residue.
func TestExclusionEvictionCandidates_AnUnsyncedEntryOutranksAnOlderSyncedOne(t *testing.T) {
	now := time.Now()
	synced := entryFor(t, "//s/x", "synced", "bh-real", now.Add(-10*time.Hour))
	unsynced := entryFor(t, "//s/x", "unsynced", "", now.Add(-1*time.Hour))

	got := exclusionEvictionCandidates([]WorkspaceEntry{synced, unsynced}, "//s/x")

	require.Len(t, got, 2)
	require.Equal(t, unsynced.ShortID, got[0].ShortID,
		"the residue of a failed prepare is reclaimed before any warm workspace")
}

// Within one baseline class the order is LRU.
func TestExclusionEvictionCandidates_OrdersBySyncedThenLeastRecentlyUsed(t *testing.T) {
	now := time.Now()
	newest := entryFor(t, "//s/x", "c", "bh-real", now.Add(-1*time.Hour))
	oldest := entryFor(t, "//s/x", "a", "bh-real", now.Add(-9*time.Hour))
	middle := entryFor(t, "//s/x", "b", "bh-real", now.Add(-5*time.Hour))

	got := exclusionEvictionCandidates([]WorkspaceEntry{newest, oldest, middle}, "//s/x")

	require.Equal(t,
		[]string{oldest.ShortID, middle.ShortID, newest.ShortID},
		[]string{got[0].ShortID, got[1].ShortID, got[2].ShortID})
}

// THE DECOY GOES FIRST. The base entry is made the OLDEST and given a non-empty
// baseline, so it sorts ahead of every composite under either ordering arm. A
// list that can hold it hands a job author the outcome the ceiling exists to
// deny: destroying the workspace every non-exclusion task on that stream shares.
func TestExclusionEvictionCandidates_ExcludesTheBaseWorkspaceAndOtherStreams(t *testing.T) {
	now := time.Now()
	base := WorkspaceEntry{
		ShortID: "id-base", SourceKey: "//s/x", ClientName: "relay_h_id-base",
		BaselineHash: "bh-base", LastUsedAt: now.Add(-100 * time.Hour),
	}
	otherStream := entryFor(t, "//s/other", "z", "bh-real", now.Add(-99*time.Hour))
	mine := entryFor(t, "//s/x", "a", "bh-real", now)

	got := exclusionEvictionCandidates([]WorkspaceEntry{base, otherStream, mine}, "//s/x")

	require.Len(t, got, 1, "only this stream's exclusion-derived entries are candidates")
	require.Equal(t, mine.ShortID, got[0].ShortID)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go test ./internal/agent/source/perforce/ -run TestExclusionEvictionCandidates -v -count=1
```

Expected: **compile failure**, `undefined: exclusionEvictionCandidates`.

- [ ] **Step 3: Write the implementation**

Add `"sort"` to `exclusion_ceiling.go`'s imports and append:

```go
// exclusionEvictionCandidates returns stream's exclusion-derived entries,
// worst-first: an entry with no BaselineHash before any entry that has one, then
// by LastUsedAt ascending.
//
// THE EMPTY BASELINE ARM IS NOT A TIE-BREAK. The cold path registers a workspace
// with an empty baseline before anything is synced and the exclusion probe
// refuses before the sync runs, so an empty baseline on an unheld entry is what
// a failed bogus prepare leaves behind: an empty directory whose delete is free.
// Ordering it ahead of LRU reclaims that residue before any warm workspace an
// operator paid to fill. An entry left empty by an agent that died mid-sync is
// re-synced by Prepare anyway, so evicting one loses transferred bytes and no
// correctness. TestExclusionEvictionCandidates_AnUnsyncedEntryOutranksAnOlderSyncedOne
// carries the discriminating input.
//
// THE BASE WORKSPACE CANNOT APPEAR HERE, because isExclusionKeyForStream is
// false for a bare stream key. That is the single guard;
// TestExclusionEvictionCandidates_ExcludesTheBaseWorkspaceAndOtherStreams makes
// the base entry the oldest so a pure-LRU implementation picks it.
//
// It reads a snapshot and holds no lock, so nothing here can be a live pointer
// into registry memory.
func exclusionEvictionCandidates(snap []WorkspaceEntry, stream string) []WorkspaceEntry {
	out := make([]WorkspaceEntry, 0, len(snap))
	for _, e := range snap {
		if isExclusionKeyForStream(e.SourceKey, stream) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		iUnsynced := out[i].BaselineHash == ""
		jUnsynced := out[j].BaselineHash == ""
		if iUnsynced != jUnsynced {
			return iUnsynced
		}
		return out[i].LastUsedAt.Before(out[j].LastUsedAt)
	})
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/agent/source/perforce/ -run TestExclusionEvictionCandidates -v -count=1
go test ./internal/agent/source/perforce/ -count=1
```

Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/exclusion_ceiling.go internal/agent/source/perforce/exclusion_ceiling_test.go
git commit -m "perforce: order exclusion-workspace eviction candidates, unsynced first

Unsynced before LRU because an empty BaselineHash on an unheld entry is exactly
the residue a failed bogus prepare leaves - the cold path upserts an empty
baseline before the exclusion probe can refuse - so reclaiming it first protects
the operator's warm workspaces instead of the attacker's junk. The base
workspace cannot be a candidate: isExclusionKeyForStream is false for a bare
stream key, and the test makes the base entry the oldest so pure LRU would pick
it."
```

---

### Task 4: The gate in `Prepare`'s cold branch - evict, then admit

**Files:**
- Modify: `internal/agent/source/perforce/exclusion_ceiling.go` (add `admitExclusionWorkspace`)
- Modify: `internal/agent/source/perforce/perforce.go:292-299` (the found/not-found branch)
- Test: `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go` (new file)

**Property pinned:** at the ceiling, a cold prepare for a new exclusion set **evicts exactly one unheld slot and is admitted**. Both halves matter: "was admitted" alone is what an absent control also produces, and "evicted something" alone does not say it evicted *one* thing or the *right* thing.

**Where, positionally:** inside `Prepare`'s `else` (not-found) arm, between `reg.GetBySourceKey` (`:293`) and `allocateShortID` (`:298`). **Never inside `allocateShortID`** - that function is a SHA-256, a base32 encode and a collision probe, and the found/not-found distinction is invisible there, so a check placed inside it gates every warm prepare too. **Never above the branch**, for the same reason. Task 7 is that mutation's guard.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go`:

```go
package perforce

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// seedExclusionWorkspaces writes n exclusion-derived registry entries for
// stream, each from a distinct exclusion set so each gets a distinct composite
// key, and creates each one's directory. Returned OLDEST FIRST, so out[0] is the
// LRU victim whenever every baseline is the same.
func seedExclusionWorkspaces(
	t *testing.T, reg *Registry, root, hostname, stream string, n int, baseline string,
) []WorkspaceEntry {
	t.Helper()
	base := time.Now().Add(-24 * time.Hour)
	out := make([]WorkspaceEntry, 0, n)
	for i := 0; i < n; i++ {
		key := SourceKey(seedSpec(stream, fmt.Sprintf("seed%d", i)))
		id := allocateShortID(key, reg)
		e := WorkspaceEntry{
			ShortID:      id,
			SourceKey:    key,
			ClientName:   fmt.Sprintf("relay_%s_%s", hostname, id),
			BaselineHash: baseline,
			LastUsedAt:   base.Add(time.Duration(i) * time.Hour),
		}
		reg.Upsert(e)
		require.NoError(t, os.MkdirAll(filepath.Join(root, id), 0o755))
		out = append(out, e)
	}
	require.NoError(t, reg.Save())
	return out
}

// seedSpec is one include at @100 plus one exclusion named by tag.
func seedSpec(stream, tag string) *relayv1.PerforceSource {
	return &relayv1.PerforceSource{
		Stream: stream,
		Sync: []*relayv1.SyncEntry{
			{Path: stream + "/...", Rev: "@100"},
			{Path: fmt.Sprintf("%s/%s/...", stream, tag), Exclude: true},
		},
	}
}

// setColdPrepareFixtures registers every p4 call one successful cold prepare of
// pf makes, and returns the client name it will use. Registering them all is
// deliberate even in the refusal tests: it keeps those assertions about the MINT
// not happening rather than about a missing fixture, so removing the control
// entirely makes them fail on their own assertion.
func setColdPrepareFixtures(fr *fakeRunner, hostname, tag string, pf *relayv1.PerforceSource) string {
	client := expectedClientName(hostname, SourceKey(pf))
	fr.set("client -o -S "+pf.Stream+" "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.set("-c "+client+" files -m1 //"+client+"/"+tag+"/...@100",
		"//s/x/"+tag+"/a.ma#1 - add change 100 (text)\n")
	fr.setStream("-c "+client+" sync -k //"+client+"/"+tag+"/...@100",
		"//s/x/"+tag+"/a.ma#1 - added\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@100", "1 of 1 files\n")
	return client
}

// clientDeletes returns the deleted client names, in the order p4 was asked.
func clientDeletes(fr *fakeRunner) []string {
	var out []string
	for _, c := range fr.argHistory() {
		if len(c) == 3 && c[0] == "client" && c[1] == "-d" {
			out = append(out, c[2])
		}
	}
	return out
}

// argvNames reports whether any recorded argv element equals name. The client
// name travels in `client -o -S <stream> <name>`'s argv; `client -i` carries it
// on STDIN, so matching on that argv cannot distinguish one mint from another.
func argvNames(fr *fakeRunner, name string) bool {
	for _, c := range fr.argHistory() {
		for _, a := range c {
			if a == name {
				return true
			}
		}
	}
	return false
}

// countExclusionEntries counts stream's exclusion-derived registry rows.
func countExclusionEntries(reg *Registry, stream string) int {
	n := 0
	for _, e := range reg.Snapshot() {
		if isExclusionKeyForStream(e.SourceKey, stream) {
			n++
		}
	}
	return n
}

// AT THE CEILING A COLD PREPARE EVICTS AND IS ADMITTED. Both halves are the
// assertion: "admitted" alone is also what an absent control produces, and
// "something was evicted" alone says nothing about how many or which.
//
// Every seeded entry is unheld and carries the same non-empty baseline, so the
// ordering in force is LRU and the expected victim is the oldest. A client -d
// fixture is registered for ALL of them so an implementation that picks the
// wrong one is caught by the assertion below rather than by a fixture miss.
func TestProvider_AtTheCeilingAColdExclusionPrepareEvictsAndIsAdmitted(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "4")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	seeded := seedExclusionWorkspaces(t, reg, root, "h", "//s/x", 4, "bh-warm")
	for _, e := range seeded {
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
	}

	pf := seedSpec("//s/x", "fresh")
	client := setColdPrepareFixtures(fr, "h", "fresh", pf)

	var lines []string
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}},
		func(s string) { lines = append(lines, s) })
	require.NoError(t, err, "at the ceiling a cold prepare evicts and is admitted, not refused")
	defer h.Finalize(context.Background())

	require.Equal(t, []string{seeded[0].ClientName}, clientDeletes(fr),
		"exactly one slot is reclaimed - not all of them, not none - and it is the least "+
			"recently used")
	require.True(t, argvNames(fr, client), "and the new workspace's client is then created")

	_, victimStill := reg.Get(seeded[0].ShortID)
	require.False(t, victimStill, "the victim's registry row is gone")
	require.Equal(t, 4, countExclusionEntries(reg, "//s/x"),
		"this single-goroutine sequence ends back at the ceiling")

	require.Contains(t, lines, "[workspace] reclaimed 1 exclusion workspace(s) for this stream")
	for _, l := range lines {
		require.NotContains(t, l, seeded[0].ShortID,
			"a short id is derived from another author's exclusion set: it goes to the agent's "+
				"own log, never to the task log")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```
go test ./internal/agent/source/perforce/ -run TestProvider_AtTheCeilingAColdExclusionPrepareEvictsAndIsAdmitted -v -count=1
```

Expected: **FAIL**. At HEAD there is no ceiling, so the prepare is admitted with **zero** `client -d` calls: the `clientDeletes` equality fails against an empty slice, and the progress-line assertion fails too.

- [ ] **Step 3: Write the implementation**

Add `"context"` to `exclusion_ceiling.go`'s imports and append:

```go
// admitExclusionWorkspace decides whether a COLD prepare may mint a new
// exclusion-derived workspace for stream. It returns nil to admit.
//
// IT MUST RUN BEFORE ANYTHING IS MINTED, and that is positional rather than
// incidental: the artifacts are the os.MkdirAll on the workspace root and
// Client.CreateStreamClient, both downstream of Prepare's found/not-found
// branch, and the first statement that can refuse a bogus exclusion is the
// PathHasFiles probe, far downstream of both.
//
// IT IS CALLED FROM THE NOT-FOUND ARM ONLY. Hoisting it above that branch - or
// putting it inside allocateShortID, where the found/not-found distinction is
// invisible - gates every WARM prepare too, which denies the feature to specs
// already using it instead of bounding new ones.
//
// THE BOUND IS NOT A HARD CEILING. The count here and the mint downstream are
// not under one lock, so concurrent cold prepares for distinct new keys on one
// stream can all pass: the honest statement is ceiling + concurrent prepares on
// that stream - 1. That overshoot is bounded by the agent's slot configuration,
// which is the operator's and not the job author's, and closing it would add a
// reservation lifecycle with a leak-on-early-return failure mode.
//
// EVICTION GOES THROUGH Provider.EvictWorkspace AND NOTHING HERE DELETES. That
// call is the canonical twin of ReserveForEvict; a third copy of the holder
// check and the p.evicting reservation is the defect this avoids. It also means
// a candidate that becomes held between selection and eviction is refused there,
// so this function needs no holder test of its own.
//
// progress IS CALLED HERE BECAUSE NOTHING IS HELD HERE. It can park until agent
// shutdown, and this runs before Prepare's p.mu block and well before
// ws.Acquire, so there is no handle and no lock for it to strand.
func (p *Provider) admitExclusionWorkspace(
	ctx context.Context, reg *Registry, sourceKey, stream string, progress func(string),
) error {
	if !isExclusionKeyForStream(sourceKey, stream) {
		return nil
	}
	ceiling := p.exclusionCeiling()
	candidates := exclusionEvictionCandidates(reg.Snapshot(), stream)
	count := len(candidates)
	if count < ceiling {
		return nil
	}

	// At most one attempt per slot. A registry left over-populated by an operator
	// lowering the knob therefore converges in one prepare rather than over many,
	// and a pathological registry cannot turn one prepare into an unbounded series
	// of p4 client -d calls each bounded only by RELAY_EVICTION_TIMEOUT.
	attempts := ceiling
	reclaimed := 0
	for _, c := range candidates {
		if count < ceiling || attempts <= 0 {
			break
		}
		attempts--
		if err := p.EvictWorkspace(ctx, c.ShortID); err != nil {
			// Held, already evicting, or a p4 or disk fault. Try the next slot.
			log.Printf("perforce: exclusion ceiling: evict %s: %v", c.ShortID, err)
			continue
		}
		log.Printf("perforce: exclusion ceiling: reclaimed %s", c.ShortID)
		reclaimed++
		count--
	}

	if reclaimed > 0 {
		// THE COUNT ONLY. GET /v1/tasks/{id}/logs is authenticated but not
		// admin-only and carries no per-owner gate, so anything on this line is
		// readable by any authenticated user - and a short id is derived from
		// another job author's exclusion set. The ids go to the agent's own log
		// above, where the reader already has host access.
		progress(fmt.Sprintf("[workspace] reclaimed %d exclusion workspace(s) for this stream",
			reclaimed))
	}
	if count >= ceiling {
		// NO OCCUPANT IS NAMED, for the reason the progress line gives: naming the
		// occupying workspaces would make this refusal a disclosure oracle for
		// another tenant's exclusion sets. The stream is the caller's own and is
		// rendered %q and LAST, per syncSummary's rule, so a forged path cannot
		// spell a convincing line of its own.
		return fmt.Errorf("exclusion workspace ceiling reached: this agent holds %d of at most %d "+
			"exclusion-derived workspaces for this stream and every one is in use by a running "+
			"task; retry the task, or raise %s on the agent. Stream %q",
			count, ceiling, maxExclusionSetsEnv, stream)
	}
	return nil
}
```

In `internal/agent/source/perforce/perforce.go`, replace the block at `:292-299`:

```go
	// Find or allocate a workspace short_id for this source key.
	existing, found := reg.GetBySourceKey(sourceKey)
	var shortID string
	if found {
		shortID = existing.ShortID
	} else {
		// COLD ONLY, and the placement is the control. See
		// admitExclusionWorkspace: above this branch, or inside allocateShortID
		// where the found/not-found distinction is invisible, every warm prepare
		// is gated too. It holds no workspace handle and no lock here, so a
		// refusal has nothing to release.
		// TestProvider_AWarmExclusionPrepareAtTheCeilingIsNotGated.
		if err := p.admitExclusionWorkspace(ctx, reg, sourceKey, pf.GetStream(), progress); err != nil {
			return nil, err
		}
		shortID = allocateShortID(sourceKey, reg)
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/agent/source/perforce/ -run TestProvider_AtTheCeilingAColdExclusionPrepareEvictsAndIsAdmitted -v -count=1
go test ./internal/agent/source/perforce/ -count=1
go test ./internal/agent/... -count=1
```

Expected: all PASS. If any pre-existing perforce test fails, check it against Task 0 step 6's search result - a fixture pre-populating four composite entries for one stream is the expected cause, and the fix is `t.Setenv` in that test, never raising the default.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/exclusion_ceiling.go internal/agent/source/perforce/exclusion_ceiling_prepare_test.go internal/agent/source/perforce/perforce.go
git commit -m "perforce: bound exclusion-derived workspaces per stream, evicting to admit

The check sits in Prepare's not-found arm, between GetBySourceKey and
allocateShortID, so it runs ahead of both minting statements (the MkdirAll and
CreateStreamClient) and gates no warm prepare. At the ceiling it evicts the
least valuable unheld slot through the existing Provider.EvictWorkspace - never
a second copy of the holder check and the p.evicting reservation - and admits;
it cannot lean on the sweeper, which returns immediately when neither of its two
knobs is set. At most one eviction attempt per slot, so lowering the knob
converges in one prepare. The task log gets the count; the reclaimed short ids
go to the agent's own log, because a short id is derived from another author's
exclusion set and task logs are readable by any authenticated user.

The real bound is ceiling + concurrent prepares on that stream - 1: the count
and the mint are not under one lock. The overshoot is bounded by the operator's
slot configuration rather than by the job author."
```

---

### Task 5: The refusal, and that it names no occupant

**Files:**
- Test only: `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go`

**Property pinned:** when every slot is held by a running task, the prepare is **refused and nothing is minted**. This is the test the ceiling exists for. `err != nil` alone is not it: a prepare can fail downstream for a dozen unrelated reasons, and **the success path alone is what an absent control also produces**. The discriminator is the absence of the mint - no `client -o -S ... <new name>` argv and no new workspace directory.

**Why a HELD workspace is required:** the whole point of evict-then-admit is that refusal happens only when nothing is evictable. A test with unheld candidates measures Task 4, not this.

- [ ] **Step 1: Write the failing tests**

Append to `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go`:

```go
// holdAll makes every seeded workspace un-evictable by giving it a live holder,
// the seam TestEvictWorkspace_RefusesHeldWorkspace uses. EvictWorkspace's holder
// check reads p.workspaces, so a registry row with no in-memory Workspace has no
// holder and is always evictable - which is why this is needed.
func holdAll(t *testing.T, p *Provider, entries []WorkspaceEntry) {
	t.Helper()
	for _, e := range entries {
		p.mu.Lock()
		w := NewWorkspace(e.ShortID)
		p.workspaces[e.ShortID] = w
		p.mu.Unlock()
		h, err := w.Acquire(context.Background(), Request{SyncPaths: []string{"//s/x/..."}})
		require.NoError(t, err)
		t.Cleanup(h.Release)
	}
}

// THE REFUSAL IS THE ASSERTION, AND THE ABSENCE OF THE MINT IS THE
// DISCRIMINATOR. A prepare that errored after creating the client spec has
// already produced the artifact this ceiling exists to bound, and "returned an
// error" is also what a dozen unrelated downstream failures produce. Check this
// test by deleting the whole ceiling call from Prepare: the argv and the
// directory must both appear.
//
// Every slot is held by a running task, which is the only state in which this
// control refuses at all. The client -d fixtures are registered so a mutant that
// deletes a held workspace anyway is caught by the count, not by a fixture miss.
func TestProvider_TheCeilingRefusesWhenEverySlotIsHeld(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "4")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	seeded := seedExclusionWorkspaces(t, reg, root, "h", "//s/x", 4, "bh-warm")
	for _, e := range seeded {
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
	}
	holdAll(t, p, seeded)

	pf := seedSpec("//s/x", "fresh")
	client := setColdPrepareFixtures(fr, "h", "fresh", pf)
	newShortID := allocateShortID(SourceKey(pf), reg)

	_, err = p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})

	require.Error(t, err, "with every slot held there is nothing to evict, so the prepare is refused")
	require.Contains(t, err.Error(), "ceiling")
	require.Contains(t, err.Error(), maxExclusionSetsEnv,
		"the refusal names the knob so the remedy is reachable from the task log")

	require.False(t, argvNames(fr, client),
		"NOTHING WAS MINTED. The client spec persists on the shared Perforce server, so a "+
			"refusal that still ran `client -o -S` has already produced the artifact the ceiling bounds.")
	_, statErr := os.Stat(filepath.Join(root, newShortID))
	require.True(t, os.IsNotExist(statErr), "and no workspace directory was created either")
	require.Empty(t, clientDeletes(fr), "a held slot is never deleted")
	require.Equal(t, 4, countExclusionEntries(reg, "//s/x"), "and the population is unchanged")
}

// THE REFUSAL MUST NOT NAME AN OCCUPANT. It reaches the task log on the stderr
// stream prefixed "[failed] ", and GET /v1/tasks/{id}/logs is authenticated but
// not admin-only and carries no per-owner gate - so an occupant's identifiers
// would make this control a disclosure oracle for another tenant's workspaces.
//
// The injected marker appears in no part of the expected message and is not a
// string this environment can produce on its own, so the assertion cannot go red
// or green for an unrelated reason.
func TestProvider_TheCeilingRefusalNamesNoOccupant(t *testing.T) {
	const marker = "qqzzoccupantqqzz"
	t.Setenv(maxExclusionSetsEnv, "2")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	var occupants []WorkspaceEntry
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("%s%d", marker, i)
		e := WorkspaceEntry{
			ShortID:      id,
			SourceKey:    SourceKey(seedSpec("//s/x", fmt.Sprintf("occ%d", i))),
			ClientName:   "relay_h_" + id,
			BaselineHash: "bh-warm",
			LastUsedAt:   time.Now().Add(-time.Duration(i+1) * time.Hour),
		}
		reg.Upsert(e)
		require.NoError(t, os.MkdirAll(filepath.Join(root, id), 0o755))
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
		occupants = append(occupants, e)
	}
	require.NoError(t, reg.Save())
	holdAll(t, p, occupants)

	pf := seedSpec("//s/x", "fresh")
	setColdPrepareFixtures(fr, "h", "fresh", pf)

	_, err = p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.Error(t, err)

	require.NotContains(t, err.Error(), marker,
		"no occupant identifier may reach a task log any authenticated user can read")
	require.Contains(t, err.Error(), "2 of at most 2",
		"the count and the ceiling are what the refusal is allowed to say")
	require.Contains(t, err.Error(), `"//s/x"`,
		"the stream is the caller's own, rendered %q and LAST")
}
```

- [ ] **Step 2: Run the tests to verify they fail without the control**

Confirm they are red when the control is absent, by removing it rather than by trusting task order: comment out the `admitExclusionWorkspace` call in `Prepare` and run

```
go test ./internal/agent/source/perforce/ -run "TestProvider_TheCeilingRefuses|TestProvider_TheCeilingRefusalNamesNoOccupant" -v -count=1
```

Expected with the call removed: **FAIL**, on `require.Error` and on `require.False(t, argvNames(...))` - the argv and the directory both appear. Restore the call by re-editing the file (**do not `git checkout --`**) and re-run.

- [ ] **Step 3: No implementation needed**

Task 4's implementation already satisfies these. If either test fails with the control in place, the defect is in Task 4 - fix it there.

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/agent/source/perforce/ -run "TestProvider_TheCeiling" -v -count=1
go test ./internal/agent/source/perforce/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/exclusion_ceiling_prepare_test.go
git commit -m "perforce: pin the ceiling's refusal, with a held slot and no mint

A ceiling test must assert the refusal: the success path alone is what an absent
control also produces. The discriminator is the absence of the mint - no
'client -o -S <stream> <name>' argv and no workspace directory - because a
refusal issued after the client spec was written has already produced the
artifact being bounded. Measured by removing the control: both appear.

The second test pins that the refusal names no occupant. It reaches the task log
on stderr, and GET /v1/tasks/{id}/logs is authenticated but not admin-only with
no per-owner gate, so an occupant identifier there is a cross-tenant
disclosure. The injected marker appears nowhere in the expected message."
```

---

### Task 6: The base workspace is never a candidate

**Files:**
- Test only: `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go`

**Property pinned:** the stream's base (no-exclusion) workspace survives a ceiling eviction **even when it is the most attractive victim under every ordering arm**. This is the attacker's best outcome - a job author with a handful of junk exclusion sets destroying the workspace every non-exclusion task on that stream shares, forcing a full re-sync for everyone - so it gets its own Prepare-level test, not a comment and not only a unit test of the ordering function.

**Why the decoy goes first:** the base entry is made the **oldest** and given a **non-empty** baseline, so a pure-LRU implementation picks it ahead of every composite. A decoy placed after the target is read by neither the code nor the mutant.

- [ ] **Step 1: Write the test**

Append to `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go`:

```go
// THE BASE WORKSPACE IS NEVER EVICTED BY THE CEILING, and the decoy goes first:
// the base entry is the OLDEST row and carries a non-empty baseline, so it sorts
// ahead of every composite under both ordering arms and a pure-LRU candidate
// list picks it. A client -d fixture is registered for it too, so an
// implementation that picks it gets as far as the delete and is caught by this
// test's assertion rather than by a fixture miss.
//
// This is the attacker's best outcome: a handful of junk exclusion sets
// destroying the workspace every non-exclusion task on that stream shares.
func TestProvider_TheCeilingNeverEvictsTheStreamsBaseWorkspace(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "4")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	baseID := allocateShortID("//s/x", reg)
	baseClient := "relay_h_" + baseID
	reg.Upsert(WorkspaceEntry{
		ShortID:      baseID,
		SourceKey:    "//s/x",
		ClientName:   baseClient,
		BaselineHash: "bh-base",
		LastUsedAt:   time.Now().Add(-200 * time.Hour),
	})
	require.NoError(t, os.MkdirAll(filepath.Join(root, baseID), 0o755))
	require.NoError(t, reg.Save())
	fr.set("client -d "+baseClient, "Client deleted.\n")

	seeded := seedExclusionWorkspaces(t, reg, root, "h", "//s/x", 4, "bh-warm")
	for _, e := range seeded {
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
	}

	pf := seedSpec("//s/x", "fresh")
	setColdPrepareFixtures(fr, "h", "fresh", pf)

	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	_, baseStill := reg.Get(baseID)
	require.True(t, baseStill,
		"the base workspace is outside the population and is never an eviction candidate")
	_, statErr := os.Stat(filepath.Join(root, baseID))
	require.NoError(t, statErr, "and its directory is untouched")

	require.Equal(t, []string{seeded[0].ClientName}, clientDeletes(fr),
		"exactly one composite slot was reclaimed, and the base workspace was not it")
}
```

- [ ] **Step 2: Prove the test discriminates**

It should PASS against Task 4's implementation, so its RED must be produced by the mutation it guards. Temporarily change `exclusionEvictionCandidates`'s filter from `isExclusionKeyForStream(e.SourceKey, stream)` to `isExclusionKeyForStream(e.SourceKey, stream) || e.SourceKey == stream`, then run

```
go test ./internal/agent/source/perforce/ -run TestProvider_TheCeilingNeverEvictsTheStreamsBaseWorkspace -v -count=1
```

Expected with the mutation: **FAIL** - `clientDeletes` reports the base client. Restore by re-editing the file (**not** `git checkout --`) and re-run.

- [ ] **Step 3: No implementation needed**

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/agent/source/perforce/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/exclusion_ceiling_prepare_test.go
git commit -m "perforce: prove the ceiling never evicts a stream's base workspace

The base entry is made the oldest row and given a non-empty baseline, so it
sorts ahead of every composite under both ordering arms and a pure-LRU candidate
list picks it; its client -d fixture is registered so a wrong pick reaches the
assertion instead of a fixture miss. Measured by adding the bare stream key to
the population: this test goes red and reports the base client as the victim."
```

---

### Task 7: A warm prepare at the ceiling is not gated

**Files:**
- Test only: `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go`

**Property pinned:** a prepare whose source key is **already in the registry** is not gated, not evicted against, and not refused, even when the registry is **over** the ceiling. Putting the check above `Prepare`'s found/not-found branch, or inside `allocateShortID` where that distinction is invisible, gates every warm prepare - which denies the feature to the specs already using it instead of bounding new ones. That is spec refutation 2, and this is its guard.

**Why the registry is deliberately over the ceiling:** at exactly the ceiling a misplaced gate could still evict-and-admit and look fine. Over it, a misplaced gate has to evict, and the candidates include the very entry this prepare is for.

- [ ] **Step 1: Write the test**

Append to `internal/agent/source/perforce/exclusion_ceiling_prepare_test.go`:

```go
// A WARM PREPARE IS NOT GATED. The registry is deliberately OVER the ceiling, so
// a check hoisted above Prepare's found/not-found branch - or buried inside
// allocateShortID, where that distinction is invisible - has to evict something,
// and the candidates include the very workspace this prepare is for.
//
// The client -d fixtures are all registered, so the assertion is the COUNT of
// deletes rather than a fixture miss.
func TestProvider_AWarmExclusionPrepareAtTheCeilingIsNotGated(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "4")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	seeded := seedExclusionWorkspaces(t, reg, root, "h", "//s/x", 5, "bh-warm")
	for _, e := range seeded {
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
	}

	// The spec for seed2 - an entry that is ALREADY in the registry.
	pf := seedSpec("//s/x", "seed2")
	client := setColdPrepareFixtures(fr, "h", "seed2", pf)
	require.Equal(t, seeded[2].ClientName, client,
		"the premise: this prepare is warm, so it reuses the seeded short id and client name")

	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err, "a warm prepare is never gated by the ceiling")
	defer h.Finalize(context.Background())

	require.Empty(t, clientDeletes(fr),
		"no eviction: the ceiling is checked only in the not-found arm")
	require.Equal(t, 5, countExclusionEntries(reg, "//s/x"),
		"and the over-ceiling population is left exactly as it was found")
}
```

- [ ] **Step 2: Prove the test discriminates**

It should PASS against Task 4. Produce its RED with the mutation it guards: in `Prepare`, move the `admitExclusionWorkspace` call from inside the `else` arm to just above `existing, found := reg.GetBySourceKey(sourceKey)`. Run

```
go test ./internal/agent/source/perforce/ -run TestProvider_AWarmExclusionPrepareAtTheCeilingIsNotGated -v -count=1
```

Expected with the mutation: **FAIL** on `require.Empty(t, clientDeletes(fr))`. Restore by re-editing (**not** `git checkout --`) and re-run.

- [ ] **Step 3: No implementation needed**

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/agent/source/perforce/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/exclusion_ceiling_prepare_test.go
git commit -m "perforce: prove a warm exclusion prepare is not gated by the ceiling

The registry is deliberately over the ceiling, so a check hoisted above Prepare's
found/not-found branch has to evict and the candidates include the very
workspace the prepare is for. Measured by hoisting it: this test goes red on the
delete count."
```

---

### Task 8: The p4d guard - a client for a stream that does not exist

**Files:**
- Create: `internal/agent/source/perforce/client_stream_existence_integration_test.go`

**Property pinned:** a p4 client spec cannot be persisted for a stream that does not exist. **This is the premise the per-stream ceiling rests on** - that an author cannot invent streams, so the per-stream population is bounded by something they do not control. Without it, that claim is a sentence in a comment plus ambient p4 behaviour. Task 0 step 1 measured it; this turns the measurement into a permanent guard.

**Do not write this task until Task 0 step 1's verdict is recorded as "premise holds".**

- [ ] **Step 1: Write the test**

Create `internal/agent/source/perforce/client_stream_existence_integration_test.go`:

```go
//go:build integration

package perforce

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestClient_CreateStreamClient_RefusesAStreamThatDoesNotExist pins the premise
// the per-stream exclusion ceiling rests on: an author cannot invent streams, so
// the number of streams one agent can be driven to hold workspaces for is
// bounded by something the author does not control. The ceiling then bounds
// exclusion sets per stream, which is a bound moved rather than a bound removed.
//
// IT CANNOT RUN IN CI, and startP4dContainer's own comment names exactly what a
// workflow job would have to supply for it to. Until that exists it is human-run.
// It cannot move to the default lane at all: the property is what REAL p4 does
// with a stream name that is not there, and a fake runner echoes whatever it is
// told.
//
// THE QUESTION IS WHETHER THE SPEC CAN BE PERSISTED, not whether one command
// exits non-zero. p4 exits zero on conditions it also reports on stderr - the
// reason PathHasFiles asserts positively on stdout - so the reading is the whole
// create, which is one `client -o` followed by one `client -i`.
func TestClient_CreateStreamClient_RefusesAStreamThatDoesNotExist(t *testing.T) {
	p4dEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c := NewClient()
	const bogusStream = "//test/no-such-stream-qqzz"
	const name = "relay_ci_bogus_stream"

	err := c.CreateStreamClient(ctx, name, t.TempDir(), bogusStream, "", false)
	if err == nil {
		// Clean up before failing, so a surprising pass does not leave a client on
		// the fixture server for whatever runs next in this lane.
		_ = c.DeleteClient(ctx, name)
	}
	require.Error(t, err,
		"a client for a stream that does not exist must not be creatable: the exclusion-set "+
			"ceiling bounds workspaces PER STREAM, and that is a bound only while the set of "+
			"streams is not something a job author can invent")
}
```

- [ ] **Step 2: Run the test**

```
go test -tags integration ./internal/agent/source/perforce/ -run TestClient_CreateStreamClient_RefusesAStreamThatDoesNotExist -v -timeout 1800s
```

Expected: **PASS**, and **no `--- SKIP`**. A SKIP line means `p4` is not on PATH or Docker is unreachable; record that and say in the PR body that this guard did not run.

**This test has no RED phase, and that is correct**: it pins existing p4 behaviour rather than new relay behaviour. Its RED equivalent is Task 0 step 1's STOP branch - if the premise did not hold, there would be no slice.

- [ ] **Step 3: Confirm the lane note is accurate**

Re-read `p4d_container_test.go:40-47` and confirm the comment this test points at still names the three things a CI job would need. If it does not, fix **this** test's comment to name them in one sentence; do not edit the harness comment, and do not restate a census of workflow files.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/source/perforce/client_stream_existence_integration_test.go
git commit -m "perforce: guard that a client cannot be created for a non-existent stream

The per-stream exclusion ceiling is a bound only while the set of streams is not
author-invented, and nothing pinned that. Human-run lane, for the reason
startP4dContainer's comment gives; it cannot move to the default lane because
the property is what real p4 does."
```

---

### Task 9: Coordinator - truncate the batch, count the drops

**Files:**
- Modify: `internal/worker/inventory_params.go` (add the count bound beside the byte bounds)
- Modify: `internal/worker/handler.go` (counter field near `:446`; reader near `:498`; `applyInventory` at `:2227-2254`)
- Test: `internal/worker/inventory_batch_bound_test.go` (new file)

**Property pinned:** an over-count batch is **truncated from the tail, counted, and committed**. Refusing it re-creates the freeze slice 1 removed on a new axis: the rollback undoes `ReplaceWorkerInventory`'s DELETE, the worker keeps its previous rows, and an agent reporting the same over-count inventory every registration never updates its inventory again.

**`commits == 1` is load-bearing.** "The DELETE was issued" is satisfied just as well by a transaction that issued it and rolled back, which leaves the stale rows exactly where an early return would have.

- [ ] **Step 1: Write the failing tests**

Create `internal/worker/inventory_batch_bound_test.go`:

```go
package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storableEntries returns n entries every bound in this package accepts, each
// with a distinct source key so a positional assertion can name one.
func storableEntries(n int) []*relayv1.WorkspaceInventoryUpdate {
	out := make([]*relayv1.WorkspaceInventoryUpdate, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &relayv1.WorkspaceInventoryUpdate{
			SourceType:   "st-perforce",
			SourceKey:    fmt.Sprintf("//sk/%d", i),
			ShortId:      fmt.Sprintf("shid-%d", i),
			BaselineHash: "bh",
			LastUsedAt:   "2026-09-10T12:00:00Z",
		})
	}
	return out
}

// AN OVER-COUNT BATCH IS TRUNCATED AND COMMITS. Returning an error instead rolls
// ReplaceWorkerInventory's DELETE back with everything else, so the worker keeps
// its previous rows and an agent reporting the same over-count inventory on every
// registration never updates its inventory again - the freeze the byte-bounds
// slice removed, re-created on a new axis.
//
// commits == 1 is load-bearing: "the DELETE was issued" is satisfied just as well
// by a transaction that issued it and then rolled back.
func TestApplyInventory_AnOverCountBatchIsTruncatedAndCommits(t *testing.T) {
	h, tx := newInventoryFixture(t)

	require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
		storableEntries(maxInventoryRowsPerBatch+2)))

	execs := tx.execsSeen()
	require.Len(t, execs, maxInventoryRowsPerBatch+1, "the DELETE plus exactly max upserts")
	assert.Contains(t, execs[0].sql, "worker_workspaces")
	assert.Equal(t, "//sk/0", execs[1].args[2], "the surviving rows are the HEAD of the batch")
	assert.Equal(t, fmt.Sprintf("//sk/%d", maxInventoryRowsPerBatch-1),
		execs[len(execs)-1].args[2], "and the TAIL is what was dropped")

	commits, _ := tx.outcome()
	assert.Equal(t, 1, commits, "the transaction COMMITTED; a refusal would leave this at zero")
	assert.Equal(t, uint64(2), h.InventoryBatchOverflowDrops(), "one increment per dropped ENTRY")
}

// THE CONTROL. A batch of exactly max: every row is upserted and the counter
// moves by ZERO. Without this half, an increment on the accept path - or outside
// the branch entirely - passes the test above.
func TestApplyInventory_ABatchAtExactlyTheBoundDropsNothing(t *testing.T) {
	h, tx := newInventoryFixture(t)

	require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
		storableEntries(maxInventoryRowsPerBatch)))

	require.Len(t, tx.execsSeen(), maxInventoryRowsPerBatch+1, "the DELETE plus every row")
	commits, _ := tx.outcome()
	assert.Equal(t, 1, commits)
	assert.Equal(t, uint64(0), h.InventoryBatchOverflowDrops(),
		"THE CONTROL: an increment in the accept branch, or outside the branch, dies here")
}

// THE TWO COUNTERS ARE DISTINCT NOUNS and neither stands in for the other: one
// counts rows refused for CONTENT, the other entries past a COUNT bound. Folding
// them together makes a climbing number unattributable to either cause, and the
// remedies differ.
func TestInventoryCounters_OverflowAndContentRefusalAreSeparateNumbers(t *testing.T) {
	t.Run("an over-count batch moves only the overflow counter", func(t *testing.T) {
		h, _ := newInventoryFixture(t)
		require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
			storableEntries(maxInventoryRowsPerBatch+3)))
		assert.Equal(t, uint64(3), h.InventoryBatchOverflowDrops())
		assert.Equal(t, uint64(0), h.InventoryRowRejections(),
			"no row was refused for content")
	})

	t.Run("a content-refused row moves only the rejection counter", func(t *testing.T) {
		h, _ := newInventoryFixture(t)
		inv := storableEntries(3)
		inv[0].SourceKey = strings.Repeat("Q", overLongKey)
		require.NoError(t, h.applyInventory(context.Background(), testWorkerID, inv))
		assert.Equal(t, uint64(1), h.InventoryRowRejections())
		assert.Equal(t, uint64(0), h.InventoryBatchOverflowDrops(),
			"an under-count batch drops no entry for overflow")
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go test ./internal/worker/ -run "TestApplyInventory_AnOverCountBatch|TestApplyInventory_ABatchAtExactly|TestInventoryCounters_" -v -count=1
```

Expected: **compile failure**, `undefined: maxInventoryRowsPerBatch`, `h.InventoryBatchOverflowDrops undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/worker/inventory_params.go`, append after the byte-bound `const` block (adjacency is the point - a reader changing one bound sees the other):

```go
// maxInventoryRowsPerBatch bounds how many entries of ONE applyInventory batch
// are processed. Entries past it are dropped from the TAIL and counted.
//
// IT TRUNCATES AND NEVER REFUSES THE BATCH. Returning an error rolls
// ReplaceWorkerInventory's DELETE back with everything else, so the worker keeps
// its previous rows and an agent reporting the same over-count inventory on every
// registration never updates its inventory again. Which rows survive is the
// agent's own ordering choice, and the whole payload was the agent's choice
// already, so truncation grants it no capability it lacked.
//
// IT BOUNDS ONE TRANSACTION'S WORK, NOT THE TABLE'S SIZE. This path opens with a
// full DELETE, so it cannot accumulate rows; the streaming per-message upsert
// path is where a worker's row total can grow and it is not bounded here.
//
// THE ASYMMETRY PICKS THE NUMBER, which is why it sits far past physical
// plausibility rather than near the honest maximum. Too high costs one large
// transaction from a hostile agent. Too low silently and permanently removes real
// workspaces from warm scoring for an honest agent, with no error anywhere.
//
// NO ENV KNOB, for the reason the byte bounds above give: an authenticated agent
// drives the counter, and the remedy an operator reaches for on a climbing
// counter - raise the bound - is the attack.
const maxInventoryRowsPerBatch = 4096
```

In `internal/worker/handler.go`, add after `inventoryRowRejects atomic.Uint64` (inside the same struct):

```go
	// inventoryBatchOverflowDrops counts the workspace-inventory entries dropped
	// from the TAIL of an over-count applyInventory batch. A VALUE, not a pointer,
	// for the same reason its neighbours are. Read through
	// InventoryBatchOverflowDrops.
	//
	// A DISTINCT NOUN from inventoryRowRejects above, and no input moves more than
	// one of these counters: that one counts rows refused for CONTENT, this one
	// counts entries past a COUNT bound. A batch can produce both, and neither
	// number stands in for the other.
	// TestInventoryCounters_OverflowAndContentRefusalAreSeparateNumbers.
	// NOT YET ON GET /v1/server/counters - deferred to
	// docs/backlog/idea-2026-09-10-publish-inventory-row-rejection-counter.md.
	inventoryBatchOverflowDrops atomic.Uint64
```

Add after `InventoryRowRejections`:

```go
// InventoryBatchOverflowDrops reports how many workspace-inventory entries this
// server dropped from over-count registration batches since process start.
//
// AN AUTHENTICATED AGENT MOVES THIS NUMBER AT WILL, one increment per entry past
// the bound, and this is the sentence to read before acting on it. It is
// attributable to "some agent" and no further. THE REMEDY IS TO FIND WHICH AGENT
// IS SENDING AN OVER-COUNT INVENTORY - never to raise the bound, which is what an
// agent driving this number would want. Per PROCESS, monotonic, zeroed by a
// restart, and never returned to an agent.
//
// NOTHING READS IT YET, so that remedy is guidance for whoever wires it up rather
// than something an operator can act on today; see the field above.
func (h *Handler) InventoryBatchOverflowDrops() uint64 {
	return h.inventoryBatchOverflowDrops.Load()
}
```

In `applyInventory`, insert ahead of `return pgx.BeginTxFunc(...)`:

```go
	// TRUNCATE AHEAD OF THE TRANSACTION, never inside it and never as a refusal:
	// see maxInventoryRowsPerBatch. Counted per ENTRY so the number is comparable
	// with the per-row content refusals beside it.
	if len(inv) > maxInventoryRowsPerBatch {
		h.inventoryBatchOverflowDrops.Add(uint64(len(inv) - maxInventoryRowsPerBatch))
		inv = inv[:maxInventoryRowsPerBatch]
	}
```

and amend the function's doc comment's first paragraph so it reads (re-verify the surviving sentences while editing rather than assuming the untouched half is still true):

```go
// applyInventory does a transactional full-replace of workspace inventory for a
// worker: deletes all existing rows, then inserts each non-deleted entry that
// inventoryUpsertParams accepts, over at most maxInventoryRowsPerBatch entries.
// A refused entry is dropped from the batch and an entry past the count bound is
// dropped from its tail; the rest still commit, and the error this returns is a
// store fault and nothing else, which is what finishRegister's log-and-continue
// call site is for.
```

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/worker/ -run "TestApplyInventory|TestInventory" -v -count=1
go test ./internal/worker/ -count=1
```

Expected: both PASS, and the pre-existing `TestApplyInventory_*` and `TestInventoryRowRejections_*` tests stay green untouched. Record the over-count test's wall clock for Task 0's M4 slot.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/inventory_params.go internal/worker/handler.go internal/worker/inventory_batch_bound_test.go
git commit -m "worker: bound one applyInventory batch at 4096 entries, truncating

Truncate and commit, never refuse: refusing rolls ReplaceWorkerInventory's
DELETE back with everything else, so the worker keeps stale rows and an agent
reporting the same over-count inventory every registration never updates again -
the freeze the byte-bounds slice removed, on a new axis. commits == 1 is the
guard, because 'the DELETE was issued' is also true of a transaction that rolled
back.

A second counter rather than a wider meaning for the first: one counts rows
refused for content, this one entries past a count bound, and the remedies
differ. Neither is published. The number is far past physical plausibility
because the too-low failure is silent and permanent while the too-high failure
costs one large transaction."
```

---

### Task 10: README - one appended sentence, one eviction sentence, one table row

**Files:**
- Modify: `README.md` (three edits, all by exact anchor)

**A sibling lane owns README's startup and scheduled-jobs sections, and the batch has already produced one README merge conflict from two lanes editing adjacent rows of one table.** Use **exact-anchor replacement** for all three edits so a conflict is loud rather than silent. **Do not touch** the `stream` row or the `sync` row of the source-field table - siblings just edited them.

- [ ] **Step 1: Re-read the three anchors**

```
rg -n "so a spec whose exclusions name nothing leaves a directory and a client behind before it fails\." README.md
rg -n "Admins can also evict on demand via " README.md
rg -n "RELAY_WORKSPACE_CLOBBER" README.md
wc -l README.md
```

Record all four outputs. If any anchor text differs from what Task 0 step 7 recorded, stop and report.

- [ ] **Step 2: Edit 1 - append to the exclusions paragraph**

Find the exact string

```
so a spec whose exclusions name nothing leaves a directory and a client behind before it fails.
```

and replace it with that same string followed by a space and:

```
One agent holds at most four exclusion-derived workspaces per stream (`RELAY_WORKSPACE_MAX_EXCLUSION_SETS`), and the stream's own no-exclusion workspace is outside that count and is never reclaimed by it. At the limit the agent reclaims the least valuable unheld exclusion-derived workspace for that stream - an unsynced one first, then the least recently used - and admits the task; it refuses the prepare only when every slot is held by a running task, and the refusal names the count and the limit and no occupant.
```

**Nothing in the existing paragraph is rewritten.** It already states the author-controlled minting; what it lacked is the ceiling, the number and what happens at it.

- [ ] **Step 3: Edit 2 - one sentence on the eviction paragraph**

Find the exact string

```
Admins can also evict on demand via `relay workers evict-workspace`.
```

and replace it with

```
Admins can also evict on demand via `relay workers evict-workspace`. Workspaces are also reclaimed outside the sweeper, at prepare time, by the per-stream exclusion-set ceiling described above.
```

"Active workspaces (held by a running task) are never evicted" stays true and unedited: the ceiling's candidates exclude held entries.

- [ ] **Step 4: Edit 3 - one table row, appended after the `RELAY_WORKSPACE_CLOBBER` row**

Insert a new line immediately **after** the line beginning ``| `RELAY_WORKSPACE_CLOBBER` |`` (appended rather than grouped with the other `RELAY_WORKSPACE_*` rows, to keep the diff off lines a sibling lane may be editing):

```
| `RELAY_WORKSPACE_MAX_EXCLUSION_SETS` | How many **exclusion-derived** workspaces one agent may hold **per stream**. Not a fleet total and not an agent total, and the stream's own no-exclusion workspace is outside the count. Default `4`, hard maximum `64` (a larger value is clamped with a warning). **There is no value that disables it, and `0` is not one**: `0`, a negative value and an unparseable value all resolve to the default, each with a warning naming the variable and saying the value was not used. Note that `RELAY_WORKSPACE_MAX_AGE` and `RELAY_WORKSPACE_MIN_FREE_GB` above *do* mean "disabled" at zero; this one does not. At the limit the agent reclaims the least valuable unheld exclusion-derived workspace for that stream (an unsynced one first, then the least recently used) rather than refusing, and refuses a prepare only when every slot is held by a running task. On a refusal, in order: find which job specs are minting distinct exclusion sets on that stream, since a refusal means every slot is in use; make the fleet's exclusion sets uniform for that stream; and last, raise the number, at the cost of one more nearly-full workspace per step. |
```

**Every dash in that row is an ASCII hyphen.** This repo forbids em and en dashes; re-read the inserted line and confirm.

**Three rungs and no fourth.** Do not add "set it to 0" - an option that disables the control does not belong in its remedy ladder as a peer, and the ladder is part of the advertisement.

- [ ] **Step 5: Verify the edit mechanically**

```
wc -l README.md
git diff --stat README.md
git ls-files --eol README.md
python -c "import io;d=io.open('README.md','rb').read();d.decode('utf-8');print('utf8 ok', len(d))"
rg -n "[^\x00-\x7F]" README.md
```

Expected: the line count is **+1** (one new table row; edits 1 and 2 are in-line replacements on existing lines); the diffstat shows roughly 3 changed lines plus 1 insertion - **if the diffstat is larger, stop**, it means a line-ending or encoding reclassification; `i/lf`; the decode succeeds; and the last command's output contains **no line from the new row or the two edited paragraphs**.

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs: document the per-stream exclusion-workspace ceiling

The exclusions paragraph already stated the author-controlled minting, so it is
appended to rather than rewritten: what it lacked is the limit, the number, that
the base workspace is outside it, and that the agent reclaims rather than
refuses. The eviction paragraph gains one sentence, because after this slice it
is no longer the complete list of what deletes a workspace. The table row says
per stream per agent explicitly - the variable name alone reads as a fleet
total - and states that 0 does not disable it, since both its neighbours in that
table do mean disabled at zero. The remedy ladder has three rungs and turning
the control off is not one of them."
```

---

### Task 11: Comments review pass

**Files:** every file this slice touched.

- [ ] **Step 1: Re-read every comment written in Tasks 1-9 against the house rules**

Delete, from any comment or doc comment added by this slice:

- Dates or change history.
- Session or review narrative, and measurement provenance ("measured by...", "mutation M7 showed..."). **Those belong in the commit message**, which is where this plan has put them.
- Counts of code elsewhere ("four other sites", "16 places").
- Uniqueness or completeness claims about OTHER code ("the only", "every", "all N"). Stating this function's own contract is fine.
- Censuses of other files, packages, or workflows. Task 0 step 2's Makefile finding in particular must not appear in a comment.

Grep for the shapes:

```
git diff origin/main --unified=0 -- internal/agent/source/perforce/ internal/worker/ | rg -n "^\+.*(//|/\*).*(2026|measured|mutation M|the only|every other|N sites|previously)"
```

Expected: no hits. Each hit is either deleted or moved to a commit message.

- [ ] **Step 2: Confirm the hazard each comment states is one the code cannot show**

Walk the five comment sites the spec's section 9 lists and confirm each states its hazard and nothing more:

1. `admitExclusionWorkspace` - artifacts minted downstream and which statements; the base key deliberately outside the population; the concurrency slack in the bound; eviction through `EvictWorkspace` because a third copy of the reservation discipline is the defect; `progress` called here because nothing is held here.
2. `isExclusionKeyForStream` - inverse of the encoding; the length equality against a stream containing `|`; a desynchronised tag fails **open**.
3. `resolveMaxExclusionSets` - `0` does not disable, and why that is worth a line given its neighbours.
4. `inventoryBatchOverflowDrops` and its reader - the forgeability sentence, at the place the number is read.
5. `applyInventory`'s doc comment - the truncation arm, with the surviving sentences re-verified rather than assumed.

- [ ] **Step 3: Commit (only if anything changed)**

```bash
git add -- internal/agent/source/perforce/ internal/worker/
git commit -m "perforce, worker: trim comments to the hazard they state"
```

---

### Task 12: Mutation battery

**This runs OUTSIDE the shared worktree.** Sibling agents are reading it, and restoring shared infrastructure is itself a mutation under concurrency.

- [ ] **Step 1: Set up the overlay harness**

```powershell
$MUT = "C:/Users/chadv/AppData/Local/Temp/claude/D--dev-relay--claude-worktrees-roadmap-now-dependencies-581b21/50da71ee-f761-480b-a23d-f1f7d875cc3b/scratchpad/mut"
New-Item -ItemType Directory -Force $MUT
New-Item -ItemType Directory -Force "$MUT/pristine"
$W = "D:/dev/relay/.claude/worktrees/lane-p4-ceiling"
foreach ($f in @(
  "internal/agent/source/perforce/exclusion_ceiling.go",
  "internal/agent/source/perforce/sourcekey.go",
  "internal/agent/source/perforce/perforce.go",
  "internal/worker/handler.go",
  "internal/worker/inventory_params.go")) {
  $name = Split-Path $f -Leaf
  Copy-Item "$W/$f" "$MUT/$name"
  Copy-Item "$W/$f" "$MUT/pristine/$name"
}
```

`$MUT/pristine/` is the restore source. **It is never mutated**, and it is what replaces `git checkout --`.

Write `$MUT/overlay.json`, listing only the file being mutated in each run:

```json
{"Replace": {
  "D:/dev/relay/.claude/worktrees/lane-p4-ceiling/internal/agent/source/perforce/exclusion_ceiling.go":
  "C:/Users/chadv/AppData/Local/Temp/claude/D--dev-relay--claude-worktrees-roadmap-now-dependencies-581b21/50da71ee-f761-480b-a23d-f1f7d875cc3b/scratchpad/mut/exclusion_ceiling.go"
}}
```

- [ ] **Step 2: Prove the overlay is live before trusting any result**

Insert a deliberate syntax error into `$MUT/exclusion_ceiling.go` (a stray `}` at the end) and run

```
cd D:/dev/relay/.claude/worktrees/lane-p4-ceiling
go test -overlay "$MUT/overlay.json" ./internal/agent/source/perforce/ -count=1
```

Expected: a **compile error**. That, and only that, proves the overlay path is honoured. Restore from `$MUT/pristine/`. **A compile error is NOT a kill** - it proves nothing about any guard, so no battery row may be recorded as killed by one.

- [ ] **Step 3: Establish the green baseline through the overlay**

```
go test -overlay "$MUT/overlay.json" ./internal/agent/source/perforce/ -count=1
go test ./internal/worker/ -count=1
```

Expected: **PASS** with the unmutated copies in place. A uniform result across the battery below means a broken harness; this is the measurement that rules that out.

- [ ] **Step 4: Run the control that must die first**

**Mutation C (the control): delete the `admitExclusionWorkspace` call from `Prepare` entirely** - mutate `$MUT/perforce.go` and point `overlay.json` at it.

```
go test -overlay "$MUT/overlay.json" ./internal/agent/source/perforce/ -count=1 -run TestProvider_
```

Expected: **FAIL**, naming `TestProvider_AtTheCeilingAColdExclusionPrepareEvictsAndIsAdmitted` (zero `client -d`) **and** `TestProvider_TheCeilingRefusesWhenEverySlotIsHeld` (the mint appears). If either survives, the battery is not measuring what it claims - stop and fix the test before continuing.

- [ ] **Step 5: Run the battery**

For each row: apply the mutation to the copy in `$MUT`, `diff` the copy against `$MUT/pristine/<name>` to **confirm the intended edit is present and is the only one**, run the command, record the result, then restore from `$MUT/pristine/`. **Never `git checkout --`.** For each kill, trace the failing branch and name it - a mutation may redden a test for a different guard than the one you think.

| # | Mutation | File | Must be killed by |
|---|---|---|---|
| 1 | Delete the `admitExclusionWorkspace` call from `Prepare` | `perforce.go` | `AtTheCeiling...` (no `client -d`), `TheCeilingRefuses...` (the mint appears) |
| 2 | Hoist the call above `reg.GetBySourceKey` | `perforce.go` | `AWarmExclusionPrepareAtTheCeilingIsNotGated` |
| 3 | Add `|| e.SourceKey == stream` to `exclusionEvictionCandidates`'s filter | `exclusion_ceiling.go` | `TheCeilingNeverEvictsTheStreamsBaseWorkspace`, `...ExcludesTheBaseWorkspaceAndOtherStreams` |
| 4 | Change `isExclusionKeyForStream` to `strings.HasPrefix(key, stream)` (base key now counted) | `sourcekey.go` | `ABareStreamIsNotAnExclusionKey`, `TheCeilingNeverEvicts...` |
| 5 | Drop the length equality from `isExclusionKeyForStream` | `sourcekey.go` | `IsExactAgainstAStreamContainingAPipe` |
| 6 | Remove the `BaselineHash == ""` arm from the sort | `exclusion_ceiling.go` | `AnUnsyncedEntryOutranksAnOlderSyncedOne` |
| 7 | Set `attempts := 0` in the eviction loop | `exclusion_ceiling.go` | `AtTheCeiling...` (no delete, and the refusal fires) |
| 8 | Return the refusal before the eviction loop | `exclusion_ceiling.go` | `AtTheCeiling...` |
| 9 | Delete the `count >= ceiling` refusal (admit anyway) | `exclusion_ceiling.go` | `TheCeilingRefusesWhenEverySlotIsHeld` |
| 10 | `resolveMaxExclusionSets` returns `0` for `"0"` (treat zero as unlimited) | `exclusion_ceiling.go` | `TestResolveMaxExclusionSets/zero...` |
| 11 | Remove the `n > maxMaxExclusionSets` clamp | `exclusion_ceiling.go` | `TestResolveMaxExclusionSets/above the maximum clamps` |
| 12 | `exclusionCeiling` returns `p.maxExclusionSets` unconditionally | `exclusion_ceiling.go` | `AZeroFieldMeansTheDefault` |
| 13 | Append the candidates' `ShortID`s to the refusal text | `exclusion_ceiling.go` | `TheCeilingRefusalNamesNoOccupant` |
| 14 | Append the candidates' `ShortID`s to the `progress` line | `exclusion_ceiling.go` | `AtTheCeiling...` (its short-id loop) |
| 15 | Replace the truncation with `return errUnstorableInventoryRow` | `handler.go` | `AnOverCountBatchIsTruncatedAndCommits` (`commits == 0`) |
| 16 | Move the overflow increment to the accept path (inside the loop) | `handler.go` | `ABatchAtExactlyTheBoundDropsNothing` |
| 17 | Increment `inventoryRowRejects` instead of the new counter | `handler.go` | `OverflowAndContentRefusalAreSeparateNumbers` |
| 18 | Truncate without the increment | `handler.go` | `AnOverCountBatchIsTruncatedAndCommits` (drops == 0) |

Agent-side rows run with `-overlay` on the perforce package; coordinator rows 15-18 run the same way with `overlay.json` pointed at `$MUT/handler.go` and the test target `./internal/worker/`.

- [ ] **Step 6: Record the results**

Fill this in, in the plan file:

```
## Mutation battery results

Overlay proven live (deliberate syntax error -> compile error): yes. A stray "}" appended to
the scratchpad copy produced `mut\exclusion_ceiling.go:221:1: syntax error`, naming the
SCRATCHPAD path, which is what proves the overlay and not the worktree file was compiled.
A compile error is not a kill and no row below is one.
Green baseline through the overlay (all five files mapped, unmutated): perforce ok 1.247s;
worker ok 3.742s.
Control (row 1) died first: yes - TestProvider_AtTheCeilingAColdExclusionPrepareEvictsAndIsAdmitted,
TestProvider_TheCeilingRefusesWhenEverySlotIsHeld, TestProvider_TheCeilingRefusalNamesNoOccupant,
TestProvider_TheCeilingNeverEvictsTheStreamsBaseWorkspace.

Every row: restored from an untouched pristine copy, mutated, diffed against pristine to confirm
the intended edit was present and was the only one, then run. No row was reverted with
`git checkout --`. Results are non-uniform across rows, which is what rules out a broken harness.

| # | Result | Test that died, and which branch of it |
|---|--------|----------------------------------------|
| 1 | killed | AtTheCeiling... on clientDeletes (zero deletes); TheCeilingRefuses... and RefusalNamesNoOccupant on require.Error; NeverEvictsTheBase... on the delete list |
| 2 | killed | AWarmExclusionPrepareAtTheCeilingIsNotGated, on clientDeletes being non-empty: the hoisted check evicted 2 workspaces on a WARM prepare |
| 3 | killed | ExcludesTheBaseWorkspaceAndOtherStreams (candidate list holds 2); NeverEvictsTheStreamsBaseWorkspace (the base row is gone) |
| 4 | killed | ABareStreamIsNotAnExclusionKey at the `("//s/x","//s/x")` assertion - the named guard; plus IsExactAgainstAStreamContainingAPipe and four Provider tests |
| 5 | killed | IsExactAgainstAStreamContainingAPipe only, on the inner-stream assertion. The suffix-only predicate counts the longer stream's key against the shorter one |
| 6 | killed | AnUnsyncedEntryOutranksAnOlderSyncedOne only - the empty-baseline arm is the sole thing that test discriminates |
| 7 | killed | AtTheCeiling... (no delete issued, so the refusal fires); NeverEvictsTheBase... same branch |
| 8 | killed | AtTheCeiling... on require.NoError - the prepare is refused instead of evicting and admitting |
| 9 | killed | TheCeilingRefusesWhenEverySlotIsHeld and TheCeilingRefusalNamesNoOccupant, both on require.Error |
| 10 | killed | TestResolveMaxExclusionSets/zero_does_not_disable_it, on the returned value |
| 11 | killed | TestResolveMaxExclusionSets/above_the_maximum_clamps, on the returned value |
| 12 | killed | TestExclusionCeiling_AZeroFieldMeansTheDefault, on the zero-field row |
| 13 | killed | TheCeilingRefusalNamesNoOccupant ONLY, on the injected marker appearing in the refusal |
| 14 | killed | AtTheCeiling... ONLY, on its per-line short-id loop over the progress output |
| 15 | killed | AnOverCountBatchIsTruncatedAndCommits (commits == 0) and both counter subtests |
| 16 | killed | ABatchAtExactlyTheBoundDropsNothing - THE CONTROL - plus the over-count test's drop count |
| 17 | killed | OverflowAndContentRefusalAreSeparateNumbers, including the both-at-once subtest |
| 18 | killed | AnOverCountBatchIsTruncatedAndCommits on drops == 0, plus the counter subtests |
| 19 | killed | TheCeilingRefusesWhenEverySlotIsHeld on the ARGV assertion. require.Error still passes, so the mint-absence check is the only thing that sees this |
| 20 | killed | TheCeilingRefusesWhenEverySlotIsHeld on the DIRECTORY assertion. The argv is absent here too, so the directory check is the only thing that sees this |

Survivors: none.

Rows 19 and 20 are not in the plan's 18 and were added because the plan's own refutation 4
required asserting both absences: each row proves one of the two assertions is load-bearing
against a mutant the other cannot see, so neither is redundant.

Worktree after the battery: `git status --porcelain` empty, `git diff --stat` empty, and all five
touched files byte-identical to the pristine copies. No mutation leaked in.
```

**A survivor is a finding, not a footnote.** For each one, either add the missing assertion and re-run, or record why the property is genuinely unpinnable and name it for the backlog.

- [ ] **Step 7: Confirm the worktree is exactly as it was**

```
cd D:/dev/relay/.claude/worktrees/lane-p4-ceiling
git status --porcelain
git diff --stat
```

Expected: identical to Task 0 step 8's recording plus this slice's committed work. **If any tracked file under the worktree is modified, a mutation leaked in** - restore it from `$MUT/pristine/`, not from git.

- [ ] **Step 8: Commit the results**

```bash
git add docs/superpowers/plans/2026-09-10-workspace-exclusion-set-ceiling.md
git commit -m "docs: record the exclusion-set ceiling mutation battery"
```

---

### Task 13: Whole-slice verification

- [ ] **Step 1: Default lane, everything**

```
cd D:/dev/relay/.claude/worktrees/lane-p4-ceiling
go build ./...
go vet ./internal/agent/... ./internal/worker/...
go test ./... -count=1
```

Expected: all PASS. Record the package count and the wall clock.

- [ ] **Step 2: The integration lanes this slice's packages belong to**

```
go test -tags integration -count=1 ./internal/worker/... -timeout 1800s
go test -tags integration -count=1 ./internal/agent/source/perforce/ -timeout 1800s
```

The second needs Docker **and** `p4` on PATH. **Read the output for `--- SKIP`** - a skipped p4d lane is a lane that tested nothing, and the PR body must say so. Record which lanes actually ran.

- [ ] **Step 3: `-race`**

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

If the lane is unavailable, **say so plainly** in the PR body. `-count=N` repetition is not a substitute and must not be reported as one.

- [ ] **Step 4: Hygiene on every file touched**

```
git diff --stat origin/main
git ls-files --eol internal/agent/source/perforce/ internal/worker/ README.md
gofmt -l internal/agent/source/perforce/ internal/worker/
```

Expected: every `git ls-files --eol` line reads `i/lf`; the diffstat is proportionate to the change this plan describes (one new agent source file, two new agent test files, one new integration test file, one new worker test file, edits to `sourcekey.go`, `perforce.go`, `handler.go`, `inventory_params.go`, and four lines of README); `gofmt -l` lists nothing **that it did not also list at `origin/main`** for the same paths.

Assert UTF-8 on each touched file:

```
python -c "import io,sys;[io.open(p,'rb').read().decode('utf-8') for p in sys.argv[1:]];print('utf8 ok')" README.md internal/agent/source/perforce/exclusion_ceiling.go internal/agent/source/perforce/sourcekey.go internal/agent/source/perforce/perforce.go internal/worker/handler.go internal/worker/inventory_params.go
```

- [ ] **Step 5: Confirm the scope fence held**

```
git diff --name-only origin/main
```

Expected: **nothing** under `internal/schedrunner/`, `cmd/`, `internal/api/`, `internal/jobspec/`, `internal/store/`, `web/`. Expected: **no** `Makefile` change and **no** `.github/workflows/` change. Expected: **no** new file under `internal/store/migrations/` (highest remains `000024`).

If README shows a change outside the three anchors of Task 10, that is a sibling lane's work that got swept in - revert exactly those hunks.

- [ ] **Step 6: Record the verification block**

```
## Verification

Default lane:        <ok, N packages, Ns>
worker integration:  <ok | not run: reason>
perforce p4d lane:   <ok, no SKIP | SKIPPED: reason - this slice's premise guard did not run>
-race:               <ok, container, zero data races | not run: reason>
eol:                 all touched paths i/lf
utf8:                ok
scope fence:         <held | breach and what was reverted>
migrations:          highest is 000024, none added
```

---

### Task 14: PR and backlog close (conductor)

- [ ] **Step 1: Assemble the PR body in the scratchpad, not inline**

Write it to a scratchpad file and pass `--body-file`. A long inline `--body` heredoc trips the classifier.

The body must carry: Task 0's results block verbatim (especially M1's verdict), the mutation battery table, the verification block, and an explicit statement of **what this does not close** - spec section 10's nine items, at minimum items 1 (the transient client spec: stock is bounded, flow is not), 4 (the streaming upsert path is not bounded), 5 (the ceiling is per agent, not fleet-wide), 6 (it bounds the number of workspaces, not their size) and 7 (deleting `.relay-registry.json` resets the count). Also state that **both inventory counters remain unpublished**.

- [ ] **Step 2: Close the backlog item**

```
/backlog close a-job-author-controls-how-many-p4-clients
```

Never by editing `status` in place - the command does the `git mv` to `docs/backlog/closed/`, the frontmatter stamps, the `## Resolution` note and the commit.

- [ ] **Step 3: File the backlog items this slice proposes**

The conductor files these; this plan only names them.

1. **Probe an exclusion through an existing sibling client before minting a new one.** Spec section 8's deferred route - it closes the cheap variant in the warm case at source rather than capping it. File with its premise named as the first thing to measure: whether a sibling client's view answers the same question as the variant client's. That premise was not tested in the spec session and is not tested by this slice.
2. **Publish both `internal/worker` inventory counters on `GET /v1/server/counters`.** Update `docs/backlog/idea-2026-09-10-publish-inventory-row-rejection-counter.md` to cover two counters, each as its own section, with the forgeability sentence stated where each number is read.
3. **Bound the per-worker row total on `applyInventoryUpdate`'s streaming path.** Spec section 10 item 4. The natural shape is a count predicate inside the upsert statement, which is an `internal/store/query/` change plus `make generate` - the reason it is not in this slice.
4. **An unconditional agent startup line naming the effective workspace bounds**, in the shape of `grpcBoundsLine`. This slice's fence excludes `cmd/relay-agent/main.go`, so it emits warnings only when an operator's value was not used verbatim; an operator cannot see the effective ceiling today.
5. **Reserve a cold admit under `p.mu` to close the concurrency overshoot.** Refutation 6 accepts the overshoot because it is bounded by the operator's slot count. File it if that stops being true - specifically if per-agent concurrency ever becomes something a job author influences.
6. **Rename `allocateShortID`'s first parameter.** It is declared `stream string` (`perforce.go:866`) and every caller passes a **source key**, including this slice's new code directly above it. One word, no behaviour. Optional.

---

## Self-review

**Spec coverage.** Section 1 -> Tasks 2, 4, 9, 10. Section 2's six refutations -> the refutation block above, which confirms four and extends two. Section 3 (invariants) -> Task 4's delegation to `EvictWorkspace`, the snapshot-only read, the no-handle-held placement, and the explicit non-touching of `internal/jobspec`. Section 4 (both populations, the exact predicate, the slack) -> Tasks 1, 4, 9 and refutations 3 and 6. Section 5 (evict-then-admit, ordering, the refusal's contents, the disclosure split) -> Tasks 3, 4, 5. Section 6 (the coordinator bound) -> Task 9. Section 7 (the number, its category, the `getenv` seam, no disable) -> Task 2 and Task 10's table row. Section 8 (why the client must exist first) -> Task 14 step 3 item 1 only; nothing in this slice changes it, correctly. Section 9 (documentation, three edits, five comment sites) -> Tasks 10 and 11. Section 10 (what this does not close) -> Task 14 step 1. Section 11 (Task 0) -> Task 0, with item 2 answered from the tree and item 1 given a STOP branch. Section 12 (provenance) -> the refutation block corrects two of its claims. Section 13 (A1-A8, P1, B1-B3) -> Tasks 1, 2, 3, 4, 5, 6, 7, 8, 9; A2's prescribed assertion is replaced per refutation 4. Section 13's mutation table -> Task 12, extended from 15 rows to 18. Section 14 (scope fence) -> Task 13 step 5. Section 15 -> Task 14 step 3. Section 16 -> flagged below.

**Open questions this plan does not decide.** Spec section 16 asks the human four things the plan implements as specified, plus one that is gating: default 4; evict-then-admit (versus making only `BaselineHash == ""` entries evictable, which removes the thrash and reintroduces the denial once the slots hold real workspaces); whether the coordinator half belongs in this slice; and 4096's direction. If the human moves any of them, the affected task is Task 2, Task 3, Task 9 or Task 10 respectively and nothing else changes. The fifth - spec Task 0 item 1 - is this plan's Task 0 step 1 STOP branch.

**Type and name consistency.** `isExclusionKeyForStream(key, stream string) bool`; `sourceKeyVersionTag`, `sourceKeyDigestHex`; `maxExclusionSetsEnv`, `defaultMaxExclusionSets`, `maxMaxExclusionSets`; `resolveMaxExclusionSets(getenv func(string) string) (int, []string)`; `newMaxExclusionSets(getenv func(string) string) int`; `Provider.maxExclusionSets int`; `(*Provider).exclusionCeiling() int`; `exclusionEvictionCandidates(snap []WorkspaceEntry, stream string) []WorkspaceEntry`; `(*Provider).admitExclusionWorkspace(ctx context.Context, reg *Registry, sourceKey, stream string, progress func(string)) error`; `maxInventoryRowsPerBatch`; `Handler.inventoryBatchOverflowDrops atomic.Uint64`; `(*Handler).InventoryBatchOverflowDrops() uint64`. Test helpers: `seedExclusionWorkspaces`, `seedSpec`, `setColdPrepareFixtures`, `clientDeletes`, `argvNames`, `countExclusionEntries`, `holdAll`, `entryFor`, `envOnly`, `storableEntries`. Each is used with the signature it is defined with.

**Reused from the tree, not redefined:** `newFakeP4Fixture`, `fakeRunner.set`/`setStream`/`argHistory`, `expectedClientName`, `allocateShortID`, `NewWorkspace`, `Workspace.Acquire`, `Request`, `Registry.Upsert`/`Get`/`Snapshot`/`Save`, `Provider.EvictWorkspace`, `newInventoryFixture`, `fakeTx.execsSeen`/`outcome`, `testWorkerID`, `overLongKey`, `p4dEnv`, `startP4dContainer`.
