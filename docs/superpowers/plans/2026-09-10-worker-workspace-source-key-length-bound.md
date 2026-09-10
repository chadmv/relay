# Worker Workspace Inventory Byte Bounds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put one validating, refusing constructor between the agent's workspace-inventory wire message and `worker_workspaces`, so a row the table cannot store is dropped and counted instead of freezing the worker's whole inventory, with a `NOT VALID` `CHECK` behind it for a writer that does not exist yet.

**Architecture:** One new unexported pure constructor `inventoryUpsertParams` in a new file `internal/worker/inventory_params.go`, four compile-time byte bounds with no env knob, a `NUL` predicate, a timestamp predicate, one new `atomic.Uint64` on `Handler` with an exported reader, both existing writers rewired to it, one migration pair adding four `NOT VALID` `CHECK` constraints, one README sentence, four comment edits. No new exported signature except the counter reader. No environment variable.

**Tech Stack:** Go 1.26, pgx/v5, sqlc, Postgres, testify, testcontainers-go via `internal/testsupport/pgdsn`.

**Spec:** `docs/superpowers/specs/2026-09-10-worker-workspace-source-key-length-bound.md`. Cite by section; do not re-derive. Read "What this plan refutes in the spec" below **before** the spec itself: five of the spec's statements are corrected here and two of the corrections change what you write.

**Closes (two items, not one):**

- `docs/backlog/idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key.md` - the item the spec was written from.
- `docs/backlog/bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory.md` - **the spec never mentions this item exists.** Task 5's timestamp arm satisfies all three of its Acceptance criteria verbatim. If the human cuts the timestamp arm (spec open question 2), this item stays open and the PR must say so; see refutation 3.

---

## Slice independence declaration

**This slice has ONE lane. It is backend-only. There is no frontend slice and no Phase 3 parallelism.**

- Files touched live in `internal/worker/`, `internal/store/migrations/`, and `README.md`. **Zero files under `web/`.** Zero files under `internal/api/`, `cmd/relay-server/`, `internal/jobspec/`, `internal/schedrunner/`, `internal/testsupport/`, `internal/agent/`.
- Tasks are **strictly sequential**. Three orderings are load-bearing:
  1. **Task 0 runs before every task that writes the number 512.** Task 0 can move that number and nothing else.
  2. **Task 8 (migration) lands before Task 9 (the at-bound integration test).** Task 9's whole value is that an accepted row inserts through the real primary key, the real lookup index **and** the new `CHECK`. Run before the constraint exists, it proves one third of that.
  3. **Task 7 (`handleInventoryUpdate`'s refusal branch) lands with Task 6 (`applyInventoryUpdate`), not after.** Without the branch, a refused row spends a log-budget token, which is the flood class the counter exists to avoid.

Dispatch one `relay-backend-engineer` for Tasks 0-12 in order. Task 13 is the conductor's.

---

## What this plan refutes in the spec

The spec was read once for self-contradiction, contradiction with the tree, and prescription of things that do not exist. Five findings. Two of them change code you write.

### 1. CONFIRMED, not refuted: the rollback undoes the DELETE

Spec refutation 1 says `pgx.BeginTxFunc` rolling back undoes `ReplaceWorkerInventory`'s DELETE, so a failed batch leaves the worker's previous rows intact. **This is correct and the tree already asserts it twice.** `fakeTx`'s comment (`internal/worker/handler_register_success_test.go:80-93`) records mutation M15 - "making applyInventory's closure return an error instead of nil rolls back every inventory replace" - and `TestFinishRegister_SucceedsWhenTheInventoryTransactionFails:570-572` asserts `rollbacks >= 1` with the reason "so the worker's previous rows survive a failed replace intact rather than being half-cleared". Build on it.

### 2. REFUTED: `sqlc`'s schema input is the migrations directory, so this slice DOES feed the generator

Spec section 16 says: "No statement in `internal/store/query/worker_workspaces.sql`. [...] so `make generate` produces no `.sql.go` diff and the CRLF hazard does not arise." The **conclusion** is probably right; the **reasoning** is wrong, and the reasoning is what an implementer acts on. `sqlc.yaml:5` sets `schema: "internal/store/migrations/"`. The new migration is therefore parsed by sqlc on the next `sqlc generate`, whether or not any query changes. Two consequences:

- **`ALTER TABLE ... ADD CONSTRAINT ... CHECK (...) NOT VALID` must parse under sqlc's Postgres engine.** If it does not, generation fails outright and this slice needs a different migration shape. That is a real, unmeasured risk and it is Task 8 step 4, before anything is committed.
- **The CRLF procedure is live.** Task 8 runs `sqlc generate` and carries CLAUDE.md's full revert procedure. Do not skip it on the strength of the spec's sentence.

### 3. GAP: the timestamp arm closes an open backlog item the spec never names

`docs/backlog/bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory.md` is open, priority medium, and its Acceptance criteria are: (a) a malformed `last_used_at` in one entry no longer aborts the whole replace and the rest of the inventory lands; (b) both the replace and streaming-update paths are covered by the same test shape; (c) the "blank -> zero time" comment is corrected. Task 5 satisfies all three. The spec discusses this arm across two sections and open question 2 without ever mentioning the item. Consequence for the implementer: **Task 5 also makes a comment in `handler_register_success_test.go:541-544` false.** That comment currently reads:

```
// The injected error is the one from the open
// bug-2026-08-23-applyinventory-null-timestamp item, because this seam is what
// makes that bug cheaply reproducible for the first time. Fixing it is NOT this
// slice's job; the item stays open.
```

The test body stays correct - it injects a **store** error through `fakeTx.execErr`, which the constructor never sees and which is still a store fault after this slice. Only that paragraph goes stale. Task 11 deletes it rather than rewriting it into a fresh claim about another item's status.

### 4. REFUTED: spec test 5 cannot pass against the spec's own design without a change the spec does not list

Spec section 15 test 5 requires that a refused row leaves the connection's log-budget token count **unchanged**. Spec section 1 says "no change to any handler signature" and section 14 rewrites `handleInventoryUpdate`'s comment - but nothing in the spec adds a branch to `handleInventoryUpdate`'s body. At HEAD it is:

```go
	err := h.applyInventoryUpdate(ctx, workerID, u)
	if err == nil {
		return
	}
	if lim.allow(logKey{kind: kindInventory}) {
```

A refused row returns a non-nil error from `applyInventoryUpdate` (spec test 3 requires that), so it falls straight into `lim.allow` and **spends a token**. Test 5 would be red forever. Resolution, and it is the design the spec's own section 6 argues for: **`handleInventoryUpdate` gains one `errors.Is` branch that returns before the budget.** Task 7. Two riders that follow from it and that the spec does not state:

- **The refusal error must be value-free**, because a wrapped agent string would reach `log.Printf("%v", err)` on any future path that logs it. The sentinel and every wrapper carry only a column name and a bound, both compile-time literals.
- **The constructor is pure and takes no `*Handler`.** The spec's signature (`inventoryUpsertParams(workerID, u)`) cannot increment a field on `Handler`, so **the counter is incremented by the two callers on their refusal branch**, not inside the constructor. This is the right split - it keeps spec test 1 a pure unit test - but it means test 6's "an accepted batch moves the counter by zero" is pinning a branch in `applyInventory`, not in the constructor.

### 5. Recorded, not refuted: the "sole route" property, with its hit count

The spec calls `inventoryUpsertParams` "the only route by which either of the package's two writers builds those params". Searched for the **shape** `store.UpsertWorkerWorkspaceParams` across the whole tree (not for a call to `UpsertWorkerWorkspace`, which is the weaker instrument):

**9 construction sites outside generated code, in 5 packages.** 2 production (`internal/worker/handler.go:2206`, `:2229`) and 7 test fixtures (`internal/scheduler/dispatch_test.go:309`; `internal/worker/handler_test.go:492`, `:550`; `internal/api/workspaces_test.go:40`, `:72`; `internal/api/workers_delete_integration_test.go:322`; `internal/store/store_test.go:569`). Plus the generated type declaration at `internal/store/worker_workspaces.sql.go:165` and four occurrences inside `docs/`. This matches the spec's section 2 search exactly, by a different instrument, so the spec's search stands.

After this slice the production count is **1** (the constructor's own literal) and the test count is **8**, because Task 8's constraint test constructs the params directly **on purpose**, to bypass the Go chokepoint. That deliberate bypass is why the spec correctly declines to buy a `go/ast` guard forbidding direct literals: such a guard would have to carve out its own slice's test. Task 12 re-runs the search and records the post-slice number. Do not write "the only" anywhere in a comment - it is a claim about the complement, pinned by nothing.

### 6. Note, not a refutation: `~2704` is already in a code comment in the file you are editing

The spec says "Do not carry this figure into a code comment until it is measured." It is already in two places in `internal/worker/handler.go` (`registrationStoreFault`'s comment, at `:107-108` and `:129-132`), inherited from the hostname item. **Those are not yours to fix** - they belong to `bug-2026-08-25-hostname-is-unvalidated-and-reaches-a-unique-index`. Do not read them as a measurement, do not copy them, and do not edit them.

---

## Critical files

| File | Why |
|---|---|
| `internal/worker/handler.go:2193-2267` | Both production writers, `handleInventoryUpdate`, and the comment that becomes partly false. |
| `internal/worker/handler.go:385-431` | The four existing counter fields; three of their comments carry ordinals that go stale. |
| `internal/worker/handler_register_success_test.go:39-165` | `fakePool`/`fakeTx`, **untagged**. Records every `Exec` with its args and counts commits. This is what makes the whole behavioural battery default-lane. Do not change its behaviour. |
| `internal/store/migrations/000007_workspaces.up.sql:5-15` | The primary key and `worker_workspaces_lookup_idx`; the two index entries the numbers are derived against. |
| `sqlc.yaml:5` | `schema: internal/store/migrations/`. The reason refutation 2 exists. |
| `internal/store/query/worker_workspaces.sql` | **Not edited.** Task 12 asserts it is byte-identical. |
| `internal/agent/source/perforce/sourcekey.go:42-71` | The producer. `SourceKey` returns the bare stream or `"x1|"+16 hex+"|"+stream`; the composite overhead is exactly 20 bytes. **Not edited** - spec section 10 argues an agent-side mirror trades a silent slowdown for a task failure. |
| CLAUDE.md Invariants | "Single JSON entry point" is the shape this slice is an instance of, one layer down. |

## File inventory

**New (4):**

| File | Lane | Tasks |
|---|---|---|
| `internal/worker/inventory_params.go` | production | 2 |
| `internal/worker/inventory_params_test.go` | default (untagged) | 2, 4, 5, 6, 7 |
| `internal/worker/inventory_bounds_test.go` | default (untagged) | 3, 4, 5, 6 |
| `internal/worker/inventory_bounds_integration_test.go` | `//go:build integration`, package `worker_test` | 8, 9 |

Plus the migration pair, whose number is not fixed until Task 8 step 1.

**Edited (4):**

| File | Nature |
|---|---|
| `internal/worker/handler.go` | one new field + one new reader method (Task 6); `applyInventory` loop (Task 3); `applyInventoryUpdate` (Task 6); `handleInventoryUpdate` (Task 7); four comment rewrites (Task 11) |
| `internal/worker/handler_register_success_test.go` | one stale paragraph deleted (Task 11) |
| `README.md` | one sentence appended to the `stream` row (`:588`) (Task 10) |
| `internal/store/*.sql.go` | **generated. Never hand-edit.** Task 8 asserts no content diff. |

**Never touched:** `internal/store/query/*.sql`, `internal/store/models.go`, any `*.sql.go` by hand, `internal/jobspec/`, `internal/schedrunner/`, `cmd/relay-server/`, `internal/api/`, `internal/testsupport/pgdsn/`, `internal/agent/`, `web/`, `ROADMAP.md`.

## Scope fence (three sibling lanes are running concurrently)

- **`internal/jobspec/` is LANE S's** and is bounding `source.sync` entry counts in `validateSourceSpec` right now. A stream-length bound there is a **complement** to this slice, not a fix for it (the value this slice bounds arrives from the agent over gRPC, which can send any string regardless of what any spec said). It is spec section 13 item 2, it must be filed as its own item for a lane that owns that file, and **it must not be filed as closing this item.** Do not touch that package.
- **`internal/schedrunner/`, `cmd/relay-server/main.go`, `internal/store/query/scheduled_jobs.sql` are LANE B's.** Untouched. This is part of why publishing the new counter on `GET /v1/server/counters` is deferred.
- **`internal/testsupport/pgdsn/` is LANE E's.** Untouched. **If you see a red guard in that package on a clean tree inside the `golang:1.26` container, it is LANE E's and not yours.** Do not investigate it and do not fix it.
- **`internal/api/` and `cmd/relay-server/` stay out of this slice entirely.** The counter is deliberately not wired to `GET /v1/server/counters`. `enrollmentRefusals` is already deferred the same way (`handler.go:429-430`), so this is the established shape here. Spec section 13 item 1 proposes the follow-up item that publishes both.

## Handoff to slice 2 of this lane (do NOT implement any of it)

Slice 2 is `docs/backlog/idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates.md` - the **count** axis, landing in the same `applyInventory`. This slice adds a per-**row** predicate and no count of any kind, and leaves both count seams clean: a `len(inv)` guard ahead of `pgx.BeginTxFunc`, or a counter inside the `for`.

**The warning that must not be lost between slices:** if slice 2 refuses a whole over-count batch rather than truncating it, **it re-creates exactly the freeze this slice closes** - the rollback undoes `ReplaceWorkerInventory`, the worker keeps stale rows, and an agent that reports the same over-count inventory every registration never updates again. Read refutation 1 above and spec refutation 1 before choosing. Slice 2's ceiling is also a **different noun** from this slice's counter (this one counts rows refused for content) and needs its own number, per the distinct-nouns discipline the existing counters follow.

## Environment notes (read once)

- The tree is the worktree `D:/dev/relay/.claude/worktrees/lane-ws-bounds`. **Absolute paths only. Never `cd D:/dev/relay`** - that lands commits on the main repo's `main`.
- If `make` is not installed, run the underlying commands. `make generate` is `sqlc generate` + `buf generate`; **this slice changes no `.proto`, so run `sqlc generate` alone.**
- **CRLF procedure after any `sqlc generate`** (CLAUDE.md): sqlc emits LF and rewrites line endings across all generated files, and `git diff` and `git status` **disagree by design** here (`core.autocrlf=true` normalises LF churn out of `git diff` while `git status` still lists the file modified). (1) `git status --porcelain internal/store/`; (2) `git diff --ignore-all-space --stat internal/store/`; (3) `git checkout --` every generated file with **no real content change**; (4) **re-open any file you expected to change and confirm the change survived the revert** - the recorded failure mode is the revert silently discarding the regeneration. **Never conclude "nothing to revert" from `git diff` alone.**
- **After ANY programmatic edit to a tracked text file:** check the diffstat against the size of the change you intended, run `git ls-files --eol` on the touched paths (every one must read `i/lf`), and assert the file still decodes as UTF-8. Where an example needs a non-ASCII byte, **write it as an escape the compiler expands** (`"\u00e9"`), never as a raw literal - a raw non-ASCII byte in a file is unverifiable by eye and survives every check this repo runs.
- Integration lanes need Docker Desktop, or `RELAY_TEST_DATABASE_URL` pointing at a running Postgres. `-p 1` is mandatory in container mode.
- `internal/worker` is **already** in `test-pg-integration`'s package list (`Makefile:168`) and that target is run by `.github/workflows/go-ci.yml`'s `pg-integration` job (`:128, :183-184`). **Both CLAUDE.md-required links already exist; this slice adds no `Makefile` change and no workflow change.** Confirm this by reading those two lines once in Task 0; do not restate it in a comment.
- **Mutation batteries run in an isolated detached worktree**, never in the shared tree (three sibling agents are reading it): `git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mut-w HEAD`. **Never revert a mutation with `git checkout --`** - that discards the uncommitted guard under test. Copy the file to `<file>.orig` first and restore from the copy.
- `go test -race` may be unrunnable locally (ThreadSanitizer arena allocation, environmental). If it is, **say so plainly**; `-count=N` repetition is not a substitute and must not be reported as one.

## Task index

0. Measurements and green baseline (**blocks every number below**)
1. Read the two lane facts and the two writers (orientation, no code)
2. The constants, the sentinel and the pure constructor - `source_key` only
3. Wire `applyInventory` to drop the row and commit the batch (**the behavioural RED at HEAD**)
4. The other three columns, and the NUL predicate
5. The timestamp arm (closes the second backlog item)
6. The counter, its reader, and `applyInventoryUpdate`
7. `handleInventoryUpdate` spends no budget on a refusal
8. The migration pair, and the sqlc parse check
9. Integration: the at-bound row inserts, the one-over row does not
10. README: one sentence
11. Comments: four rewrites and one deletion
12. Mutation battery
13. Whole-slice verification, PR, backlog closes (conductor)

---

### Task 0: Measurements and green baseline

**This task produces no code.** Its three outputs are recorded below, in this file, and repeated in the PR body.

**Task 0 can move the 512-byte number and NOTHING else.** It cannot move the enforcement point (the constructor), the drop-the-row behaviour, the decision to have no environment variable, the `NOT VALID` choice, or the deferral of counter publication. None of those depend on a number. Do not let a measurement be read as licence to revisit a decision it does not touch.

- [ ] **Step 1: Measure the real btree index entry limit**

The `~2704` figure in spec section 4 is **quoted from another backlog item and unverified**. Do not carry it forward. Measure it.

Against the same Postgres the integration lane uses (`psql "$RELAY_TEST_DATABASE_URL"`, or a throwaway testcontainer):

```sql
SELECT version();
CREATE TEMP TABLE t_limit (k TEXT);
CREATE INDEX t_limit_k_idx ON t_limit (k);
INSERT INTO t_limit
SELECT string_agg(md5(random()::text), '') FROM generate_series(1, 400);
```

The value is 12800 bytes of high-entropy hex. It is deliberately **not** `repeat('a', N)`: pglz compresses a run of one character to almost nothing, and a TOASTed value may not reproduce the failure at all. The INSERT must fail, and the error states the limit, in the shape `index row size NNNN exceeds btree version 4 maximum MMMM for index "t_limit_k_idx"`.

Record **MMMM** and the `version()` string. Then re-run spec section 4's sum against it: the largest accepted `worker_workspaces_lookup_idx` entry payload is `source_type + source_key + baseline_hash` = 64 + 512 + 128 = **704 bytes**, and the largest accepted primary-key payload is 16 (UUID) + 64 + 512 = **592 bytes**. If MMMM is materially below 2704, redo the headroom argument before accepting 512.

- [ ] **Step 2: Measure the longest real `stream` reachable in this environment, WITH its input**

A measurement without its input reads as the typical case. Record the number **and the string that produced it** (clipped to 120 bytes if long) **and where it came from**. Try, in order, and stop at the first that yields data:

```sql
-- any dev/test database that has run real jobs
SELECT octet_length(source_key) AS n, left(source_key, 120)
FROM worker_workspaces ORDER BY n DESC LIMIT 5;

SELECT max(octet_length(source::text)) FROM tasks WHERE source IS NOT NULL;
SELECT left(source::text, 200) FROM tasks WHERE source IS NOT NULL LIMIT 5;
```

Then: any real job spec JSON on disk; then the `p4d` test container's stream names used by `internal/agent/source/perforce`'s integration tests; then README's own example (`//depot/film-x/main`, 19 bytes) as the floor.

**Escalation rule, carried verbatim from spec section 10: "if any real stream exceeds 300 bytes, stop and revisit the number before merging", because 512 minus the 20-byte composite overhead leaves 492 and the margin argument stops holding.**

**If no p4 environment is reachable, the output of this step is to say so plainly and record 512 as top-down-anchored only.** It is not to substitute a guess, and it is not to skip the step.

- [ ] **Step 3: Green baseline for `internal/worker`, before any mutation**

A uniform result across a mutation battery means a broken harness, and a compile error is not a kill. Establish the baseline now, on a clean tree.

Run:
```
go test ./internal/worker/... -count=1
go test -tags integration -p 1 ./internal/worker/... -count=1 -timeout 1800s
```
Expected: both PASS. Record the pass/fail line and the test count for each.

If the integration lane cannot run (no Docker, no `RELAY_TEST_DATABASE_URL`), record that plainly - it means Tasks 8 and 9 cannot be verified locally and the PR must say which lanes actually ran.

- [ ] **Step 4: Confirm the two lane links already exist**

Read `Makefile:168` and confirm `./internal/worker/...` is in `test-pg-integration`'s package list. Read `.github/workflows/go-ci.yml` and confirm the `pg-integration` job runs `make test-pg-integration`. **This is a confirmation, not a change.** If both hold, this slice adds no `Makefile` edit and no workflow edit, and no integration test written below needs CLAUDE.md's third-branch written excuse. Do not put this fact in a code comment - it is a census of other files.

- [ ] **Step 5: Record the results**

Fill this block in, in the committed plan file, replacing every `<...>`:

```
## Task 0 results

M1 btree index entry limit: <MMMM> bytes, on <version() string>.
    Instrument: 12800-byte high-entropy value into a single-column btree; limit read off the error.
    704-byte lookup-index payload against it is a headroom factor of <ratio>.
M2 longest real stream: <N> bytes.
    Input: <the string, clipped> 
    Source: <where it came from, or "no p4 environment reachable in this session">
    Escalation (>300 bytes): <did not fire | FIRED - stopped and revisited, see below>
M3 green baseline: internal/worker default lane <ok/N tests>; integration lane <ok/N tests | not runnable: reason>.

Number in force for this slice: source_key <512 or the revised value> bytes.
Unchanged by Task 0 regardless of result: the constructor as enforcement point, drop-the-row,
no env knob, NOT VALID.
```

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/plans/2026-09-10-worker-workspace-source-key-length-bound.md
git commit -m "plan: record Task 0 measurements for the workspace inventory bounds"
```

---

### Task 1: Orientation (no code)

**Files:** read only.

- [ ] **Step 1: Read the two writers and the handler**

`internal/worker/handler.go:2193-2267`. Note three things you will need:
- `applyInventory` opens `pgx.BeginTxFunc`, runs `ReplaceWorkerInventory` (a DELETE of every row for this worker), then loops. A `return` from the closure rolls the DELETE back with everything else.
- `applyInventoryUpdate`'s delete arm and upsert arm are separate; only the upsert arm gets the constructor.
- `handleInventoryUpdate` logs under the connection's budget on any error.

- [ ] **Step 2: Read the fake transaction**

`internal/worker/handler_register_success_test.go:39-165`. `fakePool` and `fakeTx` are **untagged**, so `applyInventory` is drivable with no Postgres. `fakeTx.execsSeen()` returns `[]strandExec{sql, args}` in order; `fakeTx.outcome()` returns `(commits, rollbacks)`.

**`commits` is the discriminator, not `rollbacks`.** pgx's `beginFuncExec` defers a `Rollback` after the `Commit`, so a successful transaction records one commit **and** one rollback, and asserting `rollbacks == 0` fails against correct code. The comment at `:88-93` says so; believe it.

- [ ] **Step 3: Read the two index definitions**

`internal/store/migrations/000007_workspaces.up.sql:5-15`. `PRIMARY KEY (worker_id, source_type, source_key)` and `CREATE INDEX worker_workspaces_lookup_idx ON worker_workspaces (source_type, source_key, baseline_hash)`. Three of the four bounded columns reach an index; `short_id` reaches neither.

No commit. This task exists so Task 2's code is written against the tree rather than against the spec's summary of it.

---

### Task 2: The constants, the sentinel, and the pure constructor (`source_key` only)

**Files:**
- Create: `internal/worker/inventory_params.go`
- Create: `internal/worker/inventory_params_test.go`

The bound is on **bytes, not runes**, for three reasons in order of weight: the failure being closed is a byte failure (a btree entry limit is a byte limit); SQL's `octet_length()` and Go's `len()` are the same measure, so a rune bound in Go against an `octet_length` `CHECK` would be two implementations of one policy that agree on ASCII and disagree on everything else; and a depot path is a byte string to `p4`.

- [ ] **Step 1: Write the failing test**

Create `internal/worker/inventory_params_test.go`:

```go
package worker

import (
	"errors"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testWorkerID is the resolved-at-registration identity the constructor takes as
// a parameter. It is deliberately NOT reachable from any WorkspaceInventoryUpdate
// field, which is what "the constructor never reads an identity off the wire"
// means concretely.
var testWorkerID = pgtype.UUID{
	Bytes: [16]byte{0x77, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	Valid: true,
}

// legalUpdate is a row every predicate accepts. Each of its four TEXT fields
// carries a value that names its own field, so a transposition of two same-typed
// adjacent arguments cannot survive: the four fields of
// store.UpsertWorkerWorkspaceParams that this feeds - SourceType, SourceKey,
// ShortID, BaselineHash - are all string and all adjacent.
func legalUpdate() *relayv1.WorkspaceInventoryUpdate {
	return &relayv1.WorkspaceInventoryUpdate{
		SourceType:   "st-perforce",
		SourceKey:    "//sk/one",
		ShortId:      "shid-one",
		BaselineHash: "bh-one",
		LastUsedAt:   "2026-09-10T12:00:00Z",
	}
}

// TestInventoryUpsertParams_MapsEveryFieldToItsOwnColumn is the transposition
// guard. Four adjacent string fields can be permuted without a compile error, so
// every expectation here must be a value only that one field could have produced.
func TestInventoryUpsertParams_MapsEveryFieldToItsOwnColumn(t *testing.T) {
	p, err := inventoryUpsertParams(testWorkerID, legalUpdate())
	require.NoError(t, err)

	assert.Equal(t, testWorkerID, p.WorkerID, "worker_id must come from the parameter, never from the wire message")
	assert.Equal(t, "st-perforce", p.SourceType)
	assert.Equal(t, "//sk/one", p.SourceKey)
	assert.Equal(t, "shid-one", p.ShortID)
	assert.Equal(t, "bh-one", p.BaselineHash)
	assert.True(t, p.LastUsedAt.Valid, "a parseable non-zero timestamp must bind as NOT NULL")
}

// TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes pins BOTH the
// measure and the boundary, and one input does both.
//
// The 513-byte multi-byte row is the discriminator: it is 257 runes, so an
// implementation using utf8.RuneCountInString accepts it (257 <= 512) while the
// byte measure refuses it (513 > 512). The ASCII pair pins the off-by-one on the
// boundary itself, where > and >= differ.
//
// The over-long fillers are built from "\u00e9" and "a", neither of which appears
// in any expectation below, so no assertion can be satisfied by the injected
// value itself.
func TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes(t *testing.T) {
	multiAtBound := strings.Repeat("\u00e9", maxWorkspaceSourceKeyBytes/2)
	require.Equal(t, maxWorkspaceSourceKeyBytes, len(multiAtBound),
		"fixture: the at-bound multi-byte input must be exactly at the bound in BYTES")

	multiOneOver := multiAtBound + "a"
	require.Equal(t, maxWorkspaceSourceKeyBytes+1, len(multiOneOver),
		"fixture: one byte over")
	require.Less(t, len([]rune(multiOneOver)), maxWorkspaceSourceKeyBytes,
		"fixture: and comfortably UNDER the bound in runes - this is what makes it "+
			"discriminate a rune implementation from a byte one")

	cases := []struct {
		name     string
		key      string
		accepted bool
	}{
		{"ascii at the bound", strings.Repeat("a", maxWorkspaceSourceKeyBytes), true},
		{"ascii one byte over", strings.Repeat("a", maxWorkspaceSourceKeyBytes+1), false},
		{"multi-byte at the bound", multiAtBound, true},
		{"multi-byte one byte over", multiOneOver, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := legalUpdate()
			u.SourceKey = tc.key
			p, err := inventoryUpsertParams(testWorkerID, u)
			if tc.accepted {
				require.NoError(t, err)
				assert.Equal(t, tc.key, p.SourceKey, "an accepted key must be stored whole, never truncated")
				return
			}
			require.Error(t, err, "REFUSAL is the property. The accepted rows above are what an "+
				"ABSENT bound also produces; only this arm distinguishes the two.")
			assert.True(t, errors.Is(err, errUnstorableInventoryRow),
				"callers branch on this sentinel, so the wrapper must keep it wrapped")
			assert.NotContains(t, err.Error(), tc.key,
				"the refusal must carry no wire value: a caller that logs it must not be "+
					"loggable-into by the agent")
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/worker/... -run TestInventoryUpsertParams -count=1`
Expected: FAIL to build, `undefined: inventoryUpsertParams`, `undefined: maxWorkspaceSourceKeyBytes`, `undefined: errUnstorableInventoryRow`.

**This RED is non-compilation, which is a weak RED.** Step 6 below is what converts it into a real one; do not skip it.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/worker/inventory_params.go`:

```go
package worker

import (
	"errors"
	"fmt"
	"strings"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// Byte bounds on the agent-supplied strings worker_workspaces stores. BYTES, not
// runes: a btree entry limit is a byte limit, and octet_length() in the CHECK
// behind these and len() here are the same measure, so a rune bound in Go would
// be a second implementation of one policy that agrees on ASCII and disagrees on
// everything else.
//
// THERE IS DELIBERATELY NO ENV KNOB, and the reason is one step further on than
// ingestLogLimiter's. An authenticated agent drives the refusal counter at one
// increment per inventory entry, and the remedy an operator reaches for on a
// climbing counter - raise the bound - is the attack. Changing one of these
// costs a rebuild and leaves a visible diff.
//
// source_type and source_key reach the primary key. source_type, source_key and
// baseline_hash share one worker_workspaces_lookup_idx entry, so what has to fit
// is their SUM rather than each separately. short_id reaches no index at all and
// is bounded for row size and response size, not for the index hazard.
const (
	maxWorkspaceSourceTypeBytes   = 64
	maxWorkspaceSourceKeyBytes    = 512
	maxWorkspaceShortIDBytes      = 128
	maxWorkspaceBaselineHashBytes = 128
)

// errUnstorableInventoryRow is what inventoryUpsertParams returns for a row the
// coordinator will not store.
//
// IT CARRIES NO WIRE VALUE AND NO WRAPPER OF IT MAY. Every wrapper below formats
// a column name and a bound, both compile-time literals. A caller that renders
// this error into a log line therefore cannot be made to render an agent-chosen
// depot path.
var errUnstorableInventoryRow = errors.New("inventory row is not storable")

// inventoryUpsertParams builds the params UpsertWorkerWorkspace binds, or refuses
// the row. Both of this package's writers go through it, so the policy about what
// is storable lives at one function rather than at each call site.
//
// IT REFUSES; IT NEVER TRANSFORMS, which is why sanitizeAgentErrorMessage
// (handler.go) is deliberately not reused here. That value is human-readable
// prose, where losing a byte loses a byte. source_key is an IDENTITY: truncating
// it, or stripping a byte from it, makes two distinct workspaces canonicalise
// onto one primary-key row. That is the poisoning hazard perforce.SourceKey's NUL
// set-terminator exists to prevent, re-created at the coordinator by a sanitiser
// instead of at the agent by a missing separator.
//
// workerID is a PARAMETER and no identity is read out of u: it is resolved at
// registration from the credential, which is what scopes every statement on this
// path to the sending worker's own rows.
//
// Pinned by TestInventoryUpsertParams_MapsEveryFieldToItsOwnColumn and
// TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes.
func inventoryUpsertParams(workerID pgtype.UUID, u *relayv1.WorkspaceInventoryUpdate) (store.UpsertWorkerWorkspaceParams, error) {
	if err := checkStorableText("source_key", u.SourceKey, maxWorkspaceSourceKeyBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	return store.UpsertWorkerWorkspaceParams{
		WorkerID:     workerID,
		SourceType:   u.SourceType,
		SourceKey:    u.SourceKey,
		ShortID:      u.ShortId,
		BaselineHash: u.BaselineHash,
		LastUsedAt:   pgtype.Timestamptz{Time: time.Time{}, Valid: false},
	}, nil
}

// checkStorableText refuses a string this table cannot hold. column and max are
// compile-time literals and s never enters the returned error.
func checkStorableText(column, s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("%w: %s exceeds %d bytes", errUnstorableInventoryRow, column, max)
	}
	return nil
}
```

Then make the timestamp compile without changing behaviour yet - the caller still computes it. Replace the `LastUsedAt` line above with a parameterless placeholder by giving the constructor the same parse the callers do today:

```go
	ts, _ := time.Parse(time.RFC3339, u.LastUsedAt)
	return store.UpsertWorkerWorkspaceParams{
		WorkerID:     workerID,
		SourceType:   u.SourceType,
		SourceKey:    u.SourceKey,
		ShortID:      u.ShortId,
		BaselineHash: u.BaselineHash,
		LastUsedAt:   pgtype.Timestamptz{Time: ts, Valid: !ts.IsZero()},
	}, nil
```

and add `"time"` to the import block. **This is HEAD's behaviour moved, not kept as correct** - Task 5 replaces it with the checked arm and is where the discarded error dies. Leaving it identical for now keeps this task's diff to the length bound alone.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/worker/... -run TestInventoryUpsertParams -count=1 -v`
Expected: PASS, both tests, all four subtests.

- [ ] **Step 5: Run the whole default lane**

Run: `go test ./internal/worker/... -count=1`
Expected: PASS. Nothing calls the constructor yet, so nothing else can have moved.

- [ ] **Step 6: Prove the refusal assertion is not vacuous (remove the control entirely)**

A ceiling test that only checks the success path is satisfied by an **absent** control. Check by removing the control:

```bash
cp internal/worker/inventory_params.go internal/worker/inventory_params.go.orig
```

Edit `checkStorableText` so its body is `return nil` - the control deleted, not weakened.

Run: `go test ./internal/worker/... -run TestInventoryUpsertParams -count=1`
Expected: **FAIL**, on both `ascii one byte over` and `multi-byte one byte over`, with "REFUSAL is the property".

If it passes, the test is vacuous and must be fixed before proceeding.

Restore **from the copy**, never with `git checkout --` (that would discard the whole uncommitted task):

```bash
mv internal/worker/inventory_params.go.orig internal/worker/inventory_params.go
go test ./internal/worker/... -count=1
```
Expected: PASS again. This re-run is the control that proves the restore actually applied.

- [ ] **Step 7: Hygiene and commit**

```bash
git diff --stat internal/worker/
git ls-files --eol internal/worker/inventory_params.go internal/worker/inventory_params_test.go
```
Expected: a diffstat proportionate to two new files; every path reads `i/lf`. Confirm both files decode as UTF-8 and that the only non-ASCII sequence in either is the four-character escape `\u00e9`, written as ASCII characters in the source.

```bash
git add internal/worker/inventory_params.go internal/worker/inventory_params_test.go
git commit -m "worker: add inventoryUpsertParams, bounding source_key in bytes"
```

---

### Task 3: Wire `applyInventory` to drop the row and commit the batch

**Files:**
- Create: `internal/worker/inventory_bounds_test.go`
- Modify: `internal/worker/handler.go:2201-2216` (the loop inside `applyInventory`)

**This is the slice's behavioural RED and the only test here that fails at HEAD for a behavioural reason rather than a compilation one.** Write it against HEAD first, referencing no new symbol, and record the failure output in the PR.

The interesting property is not "the bad row is refused". It is **drop the row AND keep the batch AND do not erase what was there**, and a test whose only row is the bad one cannot distinguish "row dropped" from "batch failed" - both leave zero upserts. The discriminator is a batch with a bad row **and** good rows, plus the commit count.

- [ ] **Step 1: Write the failing test**

Create `internal/worker/inventory_bounds_test.go`:

```go
package worker

import (
	"context"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newInventoryFixture is applyInventory driven with no Postgres: a fake pool
// handing out one recording fakeTx (handler_register_success_test.go), and a
// strandDB that only exists to construct the *store.Queries the closure calls
// WithTx on.
func newInventoryFixture(t *testing.T) (*Handler, *fakeTx) {
	t.Helper()
	tx := &fakeTx{}
	return &Handler{
		q:    store.New(&strandDB{execTag: "INSERT 1"}),
		pool: &fakePool{tx: tx},
	}, tx
}

// overLongKey is 4096 bytes of a filler character that appears in no assertion in
// this file, so no expectation can be satisfied by the injected value itself. It
// is a literal length rather than a reference to maxWorkspaceSourceKeyBytes,
// which is what lets this test be written and run at HEAD, where that constant
// does not exist.
const overLongKey = 4096

// TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits is the
// property the slice exists for. Failing the batch instead of the row is not a
// smaller version of this behaviour, it is a worse one: BeginTxFunc rolls back
// ReplaceWorkerInventory's DELETE with everything else, so the worker keeps its
// previous rows and an agent that reports the same bad row on every registration
// never updates its inventory again - a permanent, self-sustaining desync with no
// error anywhere.
//
// THE BAD ENTRY IS FIRST, DELIBERATELY. Placed last it cannot detect an
// implementation that returns or breaks on the first refusal, which is the
// mutation most likely to be written by accident.
func TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits(t *testing.T) {
	h, tx := newInventoryFixture(t)

	bad := &relayv1.WorkspaceInventoryUpdate{
		SourceType:   "st-perforce",
		SourceKey:    "//bad/" + strings.Repeat("Q", overLongKey),
		ShortId:      "shid-bad",
		BaselineHash: "bh-bad",
		LastUsedAt:   "2026-09-10T12:00:00Z",
	}
	good1 := &relayv1.WorkspaceInventoryUpdate{
		SourceType:   "st-perforce",
		SourceKey:    "//good/one",
		ShortId:      "shid-one",
		BaselineHash: "bh-one",
		LastUsedAt:   "2026-09-10T12:00:01Z",
	}
	good2 := &relayv1.WorkspaceInventoryUpdate{
		SourceType:   "st-perforce",
		SourceKey:    "//good/two",
		ShortId:      "shid-two",
		BaselineHash: "bh-two",
		LastUsedAt:   "2026-09-10T12:00:02Z",
	}

	require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
		[]*relayv1.WorkspaceInventoryUpdate{bad, good1, good2}),
		"a dropped row is not a store fault: finishRegister's only response to an error here "+
			"is a log line, so returning one would be a diagnostic about a working system")

	execs := tx.execsSeen()
	require.Len(t, execs, 3,
		"one ReplaceWorkerInventory plus exactly TWO upserts. Three upserts means no bound; "+
			"one means the loop stopped at the refusal")
	assert.Contains(t, execs[0].sql, "DELETE FROM worker_workspaces",
		"the replace must still run: it is what clears the worker's stale rows")

	// POSITIONAL, not a set and not a count. args are (worker_id, source_type,
	// source_key, short_id, baseline_hash, last_used_at); each row's four TEXT
	// values name their own row and their own column, so neither a reordering of
	// the surviving rows nor a transposition of two adjacent string columns can
	// pass.
	assert.Equal(t, "//good/one", execs[1].args[2])
	assert.Equal(t, "shid-one", execs[1].args[3])
	assert.Equal(t, "bh-one", execs[1].args[4])
	assert.Equal(t, "//good/two", execs[2].args[2])
	assert.Equal(t, "shid-two", execs[2].args[3])
	assert.Equal(t, "bh-two", execs[2].args[4])

	commits, _ := tx.outcome()
	assert.Equal(t, 1, commits,
		"THE LOAD-BEARING ASSERTION. Failing the batch produces zero commits and rolls the "+
			"DELETE back with it, leaving the worker's previous rows in place forever. Asserting "+
			"rollbacks == 0 instead would fail against correct code: pgx defers a rollback after a "+
			"successful commit.")
}
```

- [ ] **Step 2: Run it at HEAD and record the failure**

Run: `go test ./internal/worker/... -run TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits -count=1 -v`
Expected: **FAIL** at `require.Len(t, execs, 3)` with `len == 4` - one DELETE plus **three** upserts, because HEAD bounds nothing.

**Paste this exact output into the PR body.** It is the only RED in this slice that is not a compile error.

- [ ] **Step 3: Write the minimal implementation**

In `internal/worker/handler.go`, replace the loop body inside `applyInventory` (currently `:2201-2216`):

```go
		for _, u := range inv {
			if u.Deleted {
				continue
			}
			p, err := inventoryUpsertParams(workerID, u)
			if err != nil {
				// DROP THE ROW, DO NOT FAIL THE BATCH. Returning here rolls
				// ReplaceWorkerInventory's DELETE back with everything else, so the
				// worker keeps the rows it had and an agent that reports the same bad
				// row on every registration never updates its inventory again. The
				// dropped workspace is invisible to the dispatcher's warm scoring, and
				// that degradation is bounded to that one workspace.
				continue
			}
			if err := q.UpsertWorkerWorkspace(ctx, p); err != nil {
				return err
			}
		}
```

Update `applyInventory`'s doc comment to describe what it now does:

```go
// applyInventory does a transactional full-replace of workspace inventory for a
// worker: deletes all existing rows, then inserts each non-deleted entry that
// inventoryUpsertParams accepts. A refused entry is dropped from the batch and
// the rest still commit; the error this returns is a store fault and nothing
// else, which is what finishRegister's log-and-continue call site is for.
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/worker/... -run TestApplyInventory -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Run the whole default lane**

Run: `go test ./internal/worker/... -count=1`
Expected: PASS, including `TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone` and `TestFinishRegister_SucceedsWhenTheInventoryTransactionFails`, both of which drive this function.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handler.go internal/worker/inventory_bounds_test.go
git commit -m "worker: drop an unstorable inventory row instead of failing the batch"
```

---

### Task 4: The other three columns, and the NUL predicate

**Files:**
- Modify: `internal/worker/inventory_params.go` (the constructor)
- Modify: `internal/worker/inventory_params_test.go` (add one test)
- Modify: `internal/worker/inventory_bounds_test.go` (add one test)

Without this task, an implementation that bounds only `source_key` passes Task 3. `baseline_hash` matters for correctness, not tidiness: the lookup index's entry is the **sum** of `source_type`, `source_key` and `baseline_hash`, so "no accepted row can fail an index" is false while any one of the three is unbounded.

A **NUL** is legal in a proto3 string and illegal in `TEXT` (SQLSTATE 22021), so it is a second way one row fails one statement. Invalid UTF-8 arriving whole is **not** reachable: `proto.Unmarshal` rejects a proto3 string field carrying it and the stream dies before `Connect`'s message loop runs. Do not add a `ToValidUTF8` arm; say in the comment why there is none.

- [ ] **Step 1: Write the failing tests**

Append to `internal/worker/inventory_params_test.go`:

```go
// TestInventoryUpsertParams_EveryAgentSuppliedStringIsBounded pins the boundary
// on each column separately: exactly at the bound accepted, one byte over
// refused. Only one field varies per case, so a case can fail for exactly one
// reason.
func TestInventoryUpsertParams_EveryAgentSuppliedStringIsBounded(t *testing.T) {
	cases := []struct {
		column string
		max    int
		set    func(u *relayv1.WorkspaceInventoryUpdate, v string)
	}{
		{"source_type", maxWorkspaceSourceTypeBytes, func(u *relayv1.WorkspaceInventoryUpdate, v string) { u.SourceType = v }},
		{"source_key", maxWorkspaceSourceKeyBytes, func(u *relayv1.WorkspaceInventoryUpdate, v string) { u.SourceKey = v }},
		{"short_id", maxWorkspaceShortIDBytes, func(u *relayv1.WorkspaceInventoryUpdate, v string) { u.ShortId = v }},
		{"baseline_hash", maxWorkspaceBaselineHashBytes, func(u *relayv1.WorkspaceInventoryUpdate, v string) { u.BaselineHash = v }},
	}
	for _, tc := range cases {
		t.Run(tc.column+" at the bound is accepted", func(t *testing.T) {
			u := legalUpdate()
			tc.set(u, strings.Repeat("Q", tc.max))
			_, err := inventoryUpsertParams(testWorkerID, u)
			require.NoError(t, err)
		})
		t.Run(tc.column+" one byte over is refused", func(t *testing.T) {
			u := legalUpdate()
			tc.set(u, strings.Repeat("Q", tc.max+1))
			_, err := inventoryUpsertParams(testWorkerID, u)
			require.Error(t, err, "REFUSAL is the property; the at-bound case above is what an "+
				"absent bound also produces")
			assert.True(t, errors.Is(err, errUnstorableInventoryRow))
			assert.Contains(t, err.Error(), tc.column,
				"the refusal must name the COLUMN, which is a compile-time literal, and nothing else")
		})
		t.Run(tc.column+" carrying a NUL is refused", func(t *testing.T) {
			u := legalUpdate()
			tc.set(u, "ok\x00ay")
			_, err := inventoryUpsertParams(testWorkerID, u)
			require.Error(t, err, "a NUL is legal in a proto3 string and illegal in TEXT "+
				"(SQLSTATE 22021), so without this the row fails its statement instead")
			assert.True(t, errors.Is(err, errUnstorableInventoryRow))
		})
	}
}
```

Append to `internal/worker/inventory_bounds_test.go`:

```go
// TestApplyInventory_EveryRefusedRowIsDroppedAndTheBatchStillCommits is the batch
// half of the table above: it is not enough that the constructor refuses, the
// loop must keep going. The refused entry is FIRST in every case, for the reason
// its sibling gives.
func TestApplyInventory_EveryRefusedRowIsDroppedAndTheBatchStillCommits(t *testing.T) {
	cases := []struct {
		name string
		bad  func(u *relayv1.WorkspaceInventoryUpdate)
	}{
		{"source_type over bound", func(u *relayv1.WorkspaceInventoryUpdate) { u.SourceType = strings.Repeat("Q", 4096) }},
		{"source_key over bound", func(u *relayv1.WorkspaceInventoryUpdate) { u.SourceKey = strings.Repeat("Q", 4096) }},
		{"short_id over bound", func(u *relayv1.WorkspaceInventoryUpdate) { u.ShortId = strings.Repeat("Q", 4096) }},
		{"baseline_hash over bound", func(u *relayv1.WorkspaceInventoryUpdate) { u.BaselineHash = strings.Repeat("Q", 4096) }},
		{"source_type with a NUL", func(u *relayv1.WorkspaceInventoryUpdate) { u.SourceType = "st\x00perforce" }},
		{"source_key with a NUL", func(u *relayv1.WorkspaceInventoryUpdate) { u.SourceKey = "//sk\x00bad" }},
		{"short_id with a NUL", func(u *relayv1.WorkspaceInventoryUpdate) { u.ShortId = "shid\x00bad" }},
		{"baseline_hash with a NUL", func(u *relayv1.WorkspaceInventoryUpdate) { u.BaselineHash = "bh\x00bad" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, tx := newInventoryFixture(t)

			bad := &relayv1.WorkspaceInventoryUpdate{
				SourceType: "st-perforce", SourceKey: "//sk/bad", ShortId: "shid-bad",
				BaselineHash: "bh-bad", LastUsedAt: "2026-09-10T12:00:00Z",
			}
			tc.bad(bad)
			good := &relayv1.WorkspaceInventoryUpdate{
				SourceType: "st-perforce", SourceKey: "//good/survivor", ShortId: "shid-survivor",
				BaselineHash: "bh-survivor", LastUsedAt: "2026-09-10T12:00:01Z",
			}

			require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
				[]*relayv1.WorkspaceInventoryUpdate{bad, good}))

			execs := tx.execsSeen()
			require.Len(t, execs, 2, "the DELETE and exactly one upsert")
			assert.Equal(t, "//good/survivor", execs[1].args[2],
				"the surviving row must be the GOOD one, positionally")
			commits, _ := tx.outcome()
			assert.Equal(t, 1, commits, "and the batch must have committed")
		})
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/worker/... -run 'TestInventoryUpsertParams_EveryAgentSuppliedStringIsBounded|TestApplyInventory_EveryRefusedRowIsDropped' -count=1`
Expected: FAIL on every `source_type`, `short_id`, `baseline_hash` and every NUL subtest. The `source_key over bound` subtests PASS already - that is Task 3 still holding, and it is what shows the new cases are testing something Task 3 did not.

- [ ] **Step 3: Write the minimal implementation**

In `internal/worker/inventory_params.go`, extend the constructor's predicate block and `checkStorableText`:

```go
	if err := checkStorableText("source_type", u.SourceType, maxWorkspaceSourceTypeBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	if err := checkStorableText("source_key", u.SourceKey, maxWorkspaceSourceKeyBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	if err := checkStorableText("short_id", u.ShortId, maxWorkspaceShortIDBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	if err := checkStorableText("baseline_hash", u.BaselineHash, maxWorkspaceBaselineHashBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
```

```go
// checkStorableText refuses a string this table cannot hold: over its column's
// byte bound, or carrying a NUL. column and max are compile-time literals and s
// never enters the returned error.
//
// A NUL is legal in a proto3 string and illegal in TEXT (SQLSTATE 22021). There
// is no invalid-UTF-8 arm and there must not be one: proto.Unmarshal rejects a
// proto3 string field carrying invalid UTF-8 and the stream dies before Connect's
// message loop runs, so such a value cannot reach here over the wire.
func checkStorableText(column, s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("%w: %s exceeds %d bytes", errUnstorableInventoryRow, column, max)
	}
	if strings.IndexByte(s, 0) >= 0 {
		return fmt.Errorf("%w: %s carries a NUL", errUnstorableInventoryRow, column)
	}
	return nil
}
```

Add to the constructor's doc comment, in the paragraph about which columns reach an index:

```go
// short_id reaches neither index, so it cannot produce the index failure this
// constructor was written for; it is bounded because it is stored per row and
// echoed to admins, which makes an unbounded one a row-size and response-size
// cost. Say that rather than implying it shares the hazard.
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/worker/... -count=1 -v -run 'TestInventoryUpsertParams|TestApplyInventory'`
Expected: PASS, all subtests.

- [ ] **Step 5: Run the whole default lane**

Run: `go test ./internal/worker/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/inventory_params.go internal/worker/inventory_params_test.go internal/worker/inventory_bounds_test.go
git commit -m "worker: bound source_type, short_id and baseline_hash, and refuse a NUL"
```

---

### Task 5: The timestamp arm

**Files:**
- Modify: `internal/worker/inventory_params.go`
- Modify: `internal/worker/inventory_params_test.go`
- Modify: `internal/worker/inventory_bounds_test.go`

**This is a behaviour change and the PR must call it out.** Today a blank or unparseable `LastUsedAt` yields the zero time, binds SQL NULL into a `NOT NULL` column, and fails the statement - on the batch path rolling the whole transaction back, on the single-message path spending one budgeted log line. After this task it drops one row and commits the rest.

**It closes `docs/backlog/bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory.md`** (see refutation 3). If the human cuts this arm per spec open question 2, skip this task, leave that item open, say so in the PR, and narrow Task 10's README sentence and the constructor's comment to over-length rows only.

An honest agent never sends a blank string here: `buildRegisterRequest` formats with `e.LastUsedAt.Format("2006-01-02T15:04:05Z07:00")` (`internal/agent/agent.go:381`). A **zero** `time.Time` formats as `0001-01-01T00:00:00Z`, which **parses** and then trips `IsZero()`, so the reachable honest case is a registry entry with an unset timestamp rather than a malformed string. Both arms are needed.

- [ ] **Step 1: Write the failing tests**

Append to `internal/worker/inventory_params_test.go`:

```go
// TestInventoryUpsertParams_LastUsedAtMustParseAndBeNonZero closes the other way
// one bad row rolls back a batch. HEAD does `ts, _ := time.Parse(...)` and binds
// pgtype.Timestamptz{Valid: !ts.IsZero()}, i.e. SQL NULL, into a TIMESTAMPTZ NOT
// NULL column.
//
// The zero-time case is NOT a restatement of the unparseable one and is the
// reachable honest case: an agent whose registry entry has an unset timestamp
// formats it as "0001-01-01T00:00:00Z", which parses cleanly and is still NULL by
// the time it is bound.
func TestInventoryUpsertParams_LastUsedAtMustParseAndBeNonZero(t *testing.T) {
	cases := []struct {
		name string
		ts   string
	}{
		{"blank", ""},
		{"not RFC3339", "10 September 2026"},
		{"parses but is the zero time", "0001-01-01T00:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := legalUpdate()
			u.LastUsedAt = tc.ts
			_, err := inventoryUpsertParams(testWorkerID, u)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errUnstorableInventoryRow))
			assert.Contains(t, err.Error(), "last_used_at")
		})
	}

	t.Run("a real timestamp binds as NOT NULL", func(t *testing.T) {
		p, err := inventoryUpsertParams(testWorkerID, legalUpdate())
		require.NoError(t, err)
		require.True(t, p.LastUsedAt.Valid,
			"Valid false is SQL NULL, which is what the NOT NULL column refuses")
		assert.Equal(t, 2026, p.LastUsedAt.Time.Year())
	})
}
```

Append to `internal/worker/inventory_bounds_test.go`, as two more rows in the `cases` slice of `TestApplyInventory_EveryRefusedRowIsDroppedAndTheBatchStillCommits`:

```go
		{"last_used_at unparseable", func(u *relayv1.WorkspaceInventoryUpdate) { u.LastUsedAt = "" }},
		{"last_used_at is the zero time", func(u *relayv1.WorkspaceInventoryUpdate) { u.LastUsedAt = "0001-01-01T00:00:00Z" }},
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/worker/... -run 'TestInventoryUpsertParams_LastUsedAt|TestApplyInventory_EveryRefusedRowIsDropped' -count=1`
Expected: FAIL - the constructor currently returns `nil` error for all three, and the two new batch cases issue two upserts instead of one.

- [ ] **Step 3: Write the minimal implementation**

In `internal/worker/inventory_params.go`, replace the discarded parse with a checked one:

```go
	// The discarded error here was the OTHER way one bad row rolled back a whole
	// batch: an unparseable value yields the zero time, which binds as SQL NULL
	// against a TIMESTAMPTZ NOT NULL column and fails the statement. A zero
	// time.Time formats as "0001-01-01T00:00:00Z", which PARSES, so the parse alone
	// is not the check.
	ts, err := time.Parse(time.RFC3339, u.LastUsedAt)
	if err != nil {
		return store.UpsertWorkerWorkspaceParams{}, fmt.Errorf(
			"%w: last_used_at is not RFC3339", errUnstorableInventoryRow)
	}
	if ts.IsZero() {
		return store.UpsertWorkerWorkspaceParams{}, fmt.Errorf(
			"%w: last_used_at is the zero time", errUnstorableInventoryRow)
	}
	return store.UpsertWorkerWorkspaceParams{
		WorkerID:     workerID,
		SourceType:   u.SourceType,
		SourceKey:    u.SourceKey,
		ShortID:      u.ShortId,
		BaselineHash: u.BaselineHash,
		LastUsedAt:   pgtype.Timestamptz{Time: ts, Valid: true},
	}, nil
```

Note the error wrapper formats `err` nowhere: `time.Parse`'s message echoes the input string, which is agent-supplied.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/worker/... -run 'TestInventoryUpsertParams|TestApplyInventory' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Run the whole default lane**

Run: `go test ./internal/worker/... -count=1`
Expected: PASS. `TestFinishRegister_SucceedsWhenTheInventoryTransactionFails` still passes: it injects a **store** error via `fakeTx.execErr`, which this arm never sees.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/inventory_params.go internal/worker/inventory_params_test.go internal/worker/inventory_bounds_test.go
git commit -m "worker: refuse an inventory row whose last_used_at cannot be stored"
```

---

### Task 6: The counter, its reader, and `applyInventoryUpdate`

**Files:**
- Modify: `internal/worker/handler.go` (new field near `:431`; new reader method near `:458`; the `applyInventory` refusal branch; `applyInventoryUpdate`)
- Modify: `internal/worker/inventory_bounds_test.go`

The counter answers one question an operator cannot otherwise ask: is this control inert in production, or is it silently discarding rows? A control that has never fired and a control that fires constantly are the same code with opposite meanings.

**The counter is a signal an authenticated peer can move, at one increment per inventory entry per message, unbudgeted.** Two consequences that are design, not decoration:

- The obvious remedy for a climbing counter - "the bound is too tight, raise it" - **is the attack**. So there is no environment variable, and **no documented remedy anywhere may include raising a bound or a disable value.** A remedy list is part of the advertisement. The remedy is to find which agent is sending malformed inventory.
- **No log line carries the value** (Task 7).

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/inventory_bounds_test.go`:

```go
// TestInventoryRowRejections_CountsRefusalsAndNothingElse has two halves and the
// SECOND is the control. Without it the test is satisfied just as well by an
// increment placed in the accept branch, or in both - and a counter that also
// counts accepted rows is not attributable to anything.
func TestInventoryRowRejections_CountsRefusalsAndNothingElse(t *testing.T) {
	t.Run("two refused rows move it by exactly two", func(t *testing.T) {
		h, _ := newInventoryFixture(t)
		before := h.InventoryRowRejections()

		bad1 := &relayv1.WorkspaceInventoryUpdate{
			SourceType: "st-perforce", SourceKey: strings.Repeat("Q", 4096), ShortId: "shid-b1",
			BaselineHash: "bh-b1", LastUsedAt: "2026-09-10T12:00:00Z",
		}
		bad2 := &relayv1.WorkspaceInventoryUpdate{
			SourceType: "st-perforce", SourceKey: "//sk/b2", ShortId: "shid-b2",
			BaselineHash: "bh-b2", LastUsedAt: "not a timestamp",
		}
		good := &relayv1.WorkspaceInventoryUpdate{
			SourceType: "st-perforce", SourceKey: "//good/counted", ShortId: "shid-c",
			BaselineHash: "bh-c", LastUsedAt: "2026-09-10T12:00:01Z",
		}

		require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
			[]*relayv1.WorkspaceInventoryUpdate{bad1, bad2, good}))
		assert.Equal(t, before+2, h.InventoryRowRejections(),
			"one per REFUSED row, and the accepted row in the same batch must add nothing")
	})

	t.Run("an accepted batch moves it by zero", func(t *testing.T) {
		h, _ := newInventoryFixture(t)
		before := h.InventoryRowRejections()

		good1 := &relayv1.WorkspaceInventoryUpdate{
			SourceType: "st-perforce", SourceKey: "//good/alpha", ShortId: "shid-a",
			BaselineHash: "bh-a", LastUsedAt: "2026-09-10T12:00:00Z",
		}
		good2 := &relayv1.WorkspaceInventoryUpdate{
			SourceType: "st-perforce", SourceKey: "//good/beta", ShortId: "shid-b",
			BaselineHash: "bh-b", LastUsedAt: "2026-09-10T12:00:01Z",
		}

		require.NoError(t, h.applyInventory(context.Background(), testWorkerID,
			[]*relayv1.WorkspaceInventoryUpdate{good1, good2}))
		assert.Equal(t, before, h.InventoryRowRejections(),
			"THE CONTROL. An increment in the accept branch, or outside the branch entirely, "+
				"passes the first half of this test and dies here.")
	})
}

// TestApplyInventoryUpdate_ARefusedRowIssuesNoStatement is the single-message
// path. ZERO STATEMENTS is the assertion that matters: "returned an error" is
// also what a fixture whose Exec errors produces, which would prove the refusal
// happened at the database rather than ahead of it.
func TestApplyInventoryUpdate_ARefusedRowIssuesNoStatement(t *testing.T) {
	db := &strandDB{execTag: "INSERT 1"}
	h := &Handler{q: store.New(db)}
	before := h.InventoryRowRejections()

	u := &relayv1.WorkspaceInventoryUpdate{
		SourceType: "st-perforce", SourceKey: strings.Repeat("Q", 4096), ShortId: "shid-x",
		BaselineHash: "bh-x", LastUsedAt: "2026-09-10T12:00:00Z",
	}
	err := h.applyInventoryUpdate(context.Background(), testWorkerID, u)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errUnstorableInventoryRow),
		"handleInventoryUpdate branches on this sentinel to decide whether to spend a log token")
	assert.Empty(t, db.execsSeen(),
		"the refusal must happen AHEAD of the statement, not at the database")
	assert.Equal(t, before+1, h.InventoryRowRejections())
}
```

Add `"errors"` to this file's import block.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/worker/... -run 'TestInventoryRowRejections|TestApplyInventoryUpdate_ARefusedRow' -count=1`
Expected: FAIL to build, `h.InventoryRowRejections undefined`.

- [ ] **Step 3: Write the minimal implementation**

Add the field to `Handler` in `internal/worker/handler.go`, immediately after `enrollmentRefusals` (`:431`):

```go
	// inventoryRowRejects counts the workspace-inventory rows inventoryUpsertParams
	// refused: over a column's byte bound, carrying a NUL, or with a last_used_at
	// that cannot be stored. A VALUE, not a pointer, for the same reason its
	// neighbours are: the zero value works, so a bare &Handler{} in a test has a
	// working counter and there is no nil case anywhere. Read through
	// InventoryRowRejections.
	//
	// A DISTINCT NOUN, and no input moves more than one of these counters. It
	// counts ROWS REFUSED FOR CONTENT and nothing else - an accepted row moves it
	// by zero, which TestInventoryRowRejections_CountsRefusalsAndNothingElse's
	// second half is what pins. NOT YET ON GET /v1/server/counters - the section is
	// deliberately deferred to its own item.
	inventoryRowRejects atomic.Uint64
```

Add the reader next to the others (after `TaskStatusFenceRejections`, `:469`):

```go
// InventoryRowRejections reports how many agent-reported workspace-inventory rows
// this server refused as unstorable since process start, across every worker.
//
// AN AUTHENTICATED AGENT MOVES THIS NUMBER AT WILL, one increment per inventory
// entry per message, and this is the sentence to read before acting on it. It is
// attributable to "some agent" and no further. THE REMEDY IS TO FIND WHICH AGENT
// IS SENDING MALFORMED INVENTORY - never to raise a bound, which is what an agent
// driving this number would want. Per PROCESS, monotonic, zeroed by a restart,
// and never returned to an agent.
func (h *Handler) InventoryRowRejections() uint64 { return h.inventoryRowRejects.Load() }
```

Add the increment to `applyInventory`'s refusal branch (the `continue` from Task 3):

```go
			p, err := inventoryUpsertParams(workerID, u)
			if err != nil {
				h.inventoryRowRejects.Add(1)
				// DROP THE ROW, DO NOT FAIL THE BATCH. (comment as written in Task 3)
				continue
			}
```

Rewrite `applyInventoryUpdate`:

```go
// applyInventoryUpdate upserts or deletes a single workspace inventory row. The
// upsert arm goes through inventoryUpsertParams; a refused row issues no
// statement and returns errUnstorableInventoryRow, which the caller uses to tell
// a refusal from a store fault.
func (h *Handler) applyInventoryUpdate(ctx context.Context, workerID pgtype.UUID, u *relayv1.WorkspaceInventoryUpdate) error {
	if u.Deleted {
		// NO CONSTRUCTOR ON THIS ARM, AND THAT IS A DECISION RATHER THAN AN
		// OVERSIGHT. A DELETE binds these values as COMPARISON keys, never as an
		// index tuple, so the hazard the constructor closes is absent here. Refusing
		// an over-long delete would additionally make any row stored before the bound
		// existed agent-undeletable, because the admin evict path deletes a row only
		// by way of the agent's confirming update; such rows are cleared by the next
		// ReplaceWorkerInventory instead, which every reconnect runs.
		return h.q.DeleteWorkerWorkspace(ctx, store.DeleteWorkerWorkspaceParams{
			WorkerID: workerID, SourceType: u.SourceType, SourceKey: u.SourceKey,
		})
	}
	p, err := inventoryUpsertParams(workerID, u)
	if err != nil {
		h.inventoryRowRejects.Add(1)
		return err
	}
	return h.q.UpsertWorkerWorkspace(ctx, p)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/worker/... -run 'TestInventoryRowRejections|TestApplyInventoryUpdate' -count=1 -v`
Expected: PASS, both halves.

- [ ] **Step 5: Run the whole default lane**

Run: `go test ./internal/worker/... -count=1`
Expected: PASS. `go vet ./internal/worker/...` must also pass - `Handler` now holds one more atomic and `copylocks` will name any `*h` copy.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handler.go internal/worker/inventory_bounds_test.go
git commit -m "worker: count refused inventory rows and route the single-message path"
```

---

### Task 7: `handleInventoryUpdate` spends no budget on a refusal

**Files:**
- Modify: `internal/worker/handler.go:2259-2267`
- Modify: `internal/worker/inventory_params_test.go`

See refutation 4: without this, a refused row falls into `lim.allow` and spends a token from the connection's log budget. That would let one agent evict its own connection's other diagnostics using input it fully chooses, which is the flood class the budget exists to bound.

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/inventory_params_test.go`:

```go
// TestHandleInventoryUpdate_ARefusedRowSpendsNoLogBudget pins "count it, do not
// log it" STRUCTURALLY rather than by convention. The refusal is fully
// agent-chosen and unboundedly repeatable, so a token spent here is a token the
// same connection's task-log and status diagnostics no longer have.
//
// The frozen clock is what makes the token count exact rather than wall-clock
// dependent; newFrozen is ingest_log_limiter_test.go's existing helper and reads
// l.tokens directly.
func TestHandleInventoryUpdate_ARefusedRowSpendsNoLogBudget(t *testing.T) {
	lim, _ := newFrozen()
	before := lim.tokens

	h := &Handler{q: store.New(&strandDB{execTag: "INSERT 1"})}
	u := &relayv1.WorkspaceInventoryUpdate{
		SourceType: "st-perforce", SourceKey: strings.Repeat("Q", 4096), ShortId: "shid-y",
		BaselineHash: "bh-y", LastUsedAt: "2026-09-10T12:00:00Z",
	}
	h.handleInventoryUpdate(context.Background(), testWorkerID, lim, u)

	assert.Equal(t, before, lim.tokens,
		"a refused row must cost the connection's budget NOTHING; it is counted by "+
			"InventoryRowRejections instead")
	assert.Equal(t, uint64(1), h.InventoryRowRejections(),
		"and it must actually have been refused - without this the test passes just as "+
			"well against a build where nothing reached the constructor")
}

// TestHandleInventoryUpdate_AStoreFaultStillSpendsABudgetToken is the control. A
// store fault is NOT peer-chosen, and it is the condition the budgeted line
// exists for; suppressing it along with the refusals would make an infrastructure
// failure silent.
func TestHandleInventoryUpdate_AStoreFaultStillSpendsABudgetToken(t *testing.T) {
	lim, _ := newFrozen()
	before := lim.tokens

	db := &strandDB{execErr: errors.New("ERROR: connection reset (SQLSTATE 08006)")}
	h := &Handler{q: store.New(db)}
	h.handleInventoryUpdate(context.Background(), testWorkerID, lim, legalUpdate())

	assert.Equal(t, before-1, lim.tokens,
		"exactly one token, which is what the budget is for")
	assert.Equal(t, uint64(0), h.InventoryRowRejections(),
		"and a store fault is not a refusal: the two nouns must not merge")
}
```

Add `"context"`, `"strings"` (already present) and `"relay/internal/store"` to this file's imports as needed.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/worker/... -run TestHandleInventoryUpdate -count=1`
Expected: FAIL on the first test - `tokens` went `before` to `before-1`, because the refusal currently falls straight into `lim.allow`. The second test PASSES already; that is correct and is what makes it a control rather than a duplicate.

- [ ] **Step 3: Write the minimal implementation**

In `internal/worker/handler.go`, `handleInventoryUpdate`:

```go
	err := h.applyInventoryUpdate(ctx, workerID, u)
	if err == nil {
		return
	}
	// A REFUSED ROW IS COUNTED, NEVER LOGGED. It is fully agent-chosen and
	// unboundedly repeatable, so a token spent here is one this connection's other
	// diagnostics no longer have. InventoryRowRejections is the signal.
	if errors.Is(err, errUnstorableInventoryRow) {
		return
	}
	if lim.allow(logKey{kind: kindInventory}) {
		log.Printf("worker: inventory update failed: %v", err)
	}
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/worker/... -run TestHandleInventoryUpdate -count=1 -v`
Expected: PASS, both.

- [ ] **Step 5: Run the whole default lane**

Run: `go test ./internal/worker/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handler.go internal/worker/inventory_params_test.go
git commit -m "worker: a refused inventory row spends no connection log budget"
```

---

### Task 8: The migration pair, and the sqlc parse check

**Files:**
- Create: `internal/store/migrations/000NNN_worker_workspace_text_bounds.up.sql`
- Create: `internal/store/migrations/000NNN_worker_workspace_text_bounds.down.sql`
- Create: `internal/worker/inventory_bounds_integration_test.go`

**What the constraint buys over the Go check, stated honestly: nothing today.** The two functions this slice fixes are the only production writers. It buys the writer who does not exist yet - a sweeper statement, an eviction path, a second provider - failing closed instead of failing an index. That is also why this slice does **not** buy a `go/ast` guard forbidding a direct `store.UpsertWorkerWorkspaceParams{...}` literal: the constructor is the seam, the constraint is the backstop, and the AST guard is the expensive fallback bought after a guard has actually been evaded. **Name the condition instead of pre-buying it: if a third production writer to this table appears, that is the argument for the AST guard.**

- [ ] **Step 1: Determine the next free migration number**

```bash
ls internal/store/migrations/
```
At the time this plan was written the highest was `000023_task_preparing_status`, so the next free is `000024`. **Do not assume it still is.** A sibling lane could have added one. Take the highest number present **plus one**, and use the same four-digit zero-padded form. If `000024` is taken, use `000025`, and so on. Record the number you used.

- [ ] **Step 2: Write the failing test**

Create `internal/worker/inventory_bounds_integration_test.go`:

```go
//go:build integration

package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpsertWorkerWorkspace_TheCheckConstraintRefusesAnOverLongKey calls the
// store statement DIRECTLY, bypassing internal/worker's constructor by
// construction. BYPASSING THE GO CHOKEPOINT IS THE POINT: driving this through
// the handler would prove nothing about the constraint, because the Go check
// would refuse the row first and the database would never see it.
//
// The key is MULTI-BYTE, which is what makes this discriminate: a CHECK written
// on length() instead of octet_length() measures 258 runes here and accepts the
// row, while octet_length measures 514 bytes and refuses it. The two agree on
// every ASCII input, so an ASCII key could not tell them apart.
//
// The non-ASCII byte is written as the escape "\u00e9" rather than as a literal,
// so what this file contains is verifiable by eye and by any encoding check.
func TestUpsertWorkerWorkspace_TheCheckConstraintRefusesAnOverLongKey(t *testing.T) {
	ctx := context.Background()
	q, _ := newTestStore(t)

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "ck", Hostname: "ck", CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	key := "//" + strings.Repeat("\u00e9", 256)
	require.Equal(t, 514, len(key), "fixture: 514 BYTES")
	require.Equal(t, 258, len([]rune(key)), "fixture: and only 258 runes, well under the 512 bound")

	err = q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: w.ID, SourceType: "perforce", SourceKey: key, ShortID: "ck1",
		BaselineHash: "ckbase", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.Error(t, err,
		"the database must refuse this on its own. Without the constraint the row inserts, "+
			"and the backstop for a future writer that skips the Go chokepoint does not exist.")
	assert.Contains(t, err.Error(), "source_key",
		"the constraint's name must say which column refused it - that name is the only "+
			"diagnostic a writer bypassing the Go layer gets")
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestUpsertWorkerWorkspace_TheCheckConstraint -count=1 -timeout 900s -v`
Expected: FAIL at `require.Error` - the row inserts, because no constraint exists yet.

- [ ] **Step 4: Write the migration**

Create `internal/store/migrations/000NNN_worker_workspace_text_bounds.up.sql`:

```sql
-- Byte bounds on worker_workspaces' four agent-supplied TEXT columns, behind
-- internal/worker's inventoryUpsertParams and at the same four numbers. It buys
-- nothing against today's writers, which all go through that constructor; it
-- buys a writer that does not exist yet - a sweeper, an eviction path, a second
-- provider - failing closed instead of failing an index.
--
-- NOT VALID, DELIBERATELY. Postgres enforces a NOT VALID CHECK on every
-- subsequent INSERT and UPDATE and only skips the backfill scan. A validated
-- constraint scans every existing row and FAILS THE MIGRATION on a violation,
-- and migrations are embedded and run on startup - so one over-long row planted
-- before this lands would be a server that will not boot after upgrade. A
-- control whose deployment can be denied in advance by the data it exists to
-- reject is the wrong control.
--
-- The pre-existing violating set drains on its own: every agent reconnect runs
-- ReplaceWorkerInventory and re-reports through the bounded constructor. A later
-- VALIDATE CONSTRAINT is an optional operator action, is not part of this
-- change, and MAY FAIL while rows from a never-reconnecting worker remain. Do
-- not run it expecting success.
--
-- octet_length, not length: the Go bound is len() on a string, and a rune
-- measure here would be a second implementation of one policy that agrees on
-- ASCII and disagrees on everything else.
--
-- Four constraints rather than one compound predicate, because the constraint
-- NAME is the whole diagnostic available to the writer this exists for - the one
-- that got no Go error naming the column.
--
-- No NUL predicate: a NUL cannot be stored in TEXT at all, so the database
-- already refuses it and a redundant predicate would restate a rule Postgres
-- owns.
ALTER TABLE worker_workspaces
  ADD CONSTRAINT worker_workspaces_source_type_len_check
  CHECK (octet_length(source_type) <= 64) NOT VALID;

ALTER TABLE worker_workspaces
  ADD CONSTRAINT worker_workspaces_source_key_len_check
  CHECK (octet_length(source_key) <= 512) NOT VALID;

ALTER TABLE worker_workspaces
  ADD CONSTRAINT worker_workspaces_short_id_len_check
  CHECK (octet_length(short_id) <= 128) NOT VALID;

ALTER TABLE worker_workspaces
  ADD CONSTRAINT worker_workspaces_baseline_hash_len_check
  CHECK (octet_length(baseline_hash) <= 128) NOT VALID;
```

**Substitute Task 0's number for 512 if Task 0 moved it, in all four places it appears across this slice: here, `maxWorkspaceSourceKeyBytes`, the README sentence, and Task 9's at-bound fixture.**

Create `internal/store/migrations/000NNN_worker_workspace_text_bounds.down.sql`:

```sql
ALTER TABLE worker_workspaces DROP CONSTRAINT worker_workspaces_baseline_hash_len_check;
ALTER TABLE worker_workspaces DROP CONSTRAINT worker_workspaces_short_id_len_check;
ALTER TABLE worker_workspaces DROP CONSTRAINT worker_workspaces_source_key_len_check;
ALTER TABLE worker_workspaces DROP CONSTRAINT worker_workspaces_source_type_len_check;
```

- [ ] **Step 5: Verify sqlc still parses the schema, and that nothing generated changed**

**This step exists because of refutation 2: `sqlc.yaml:5` sets `schema: internal/store/migrations/`, so the new migration IS parsed by the generator even though no query changed.**

```bash
sqlc generate
git status --porcelain internal/store/
git diff --ignore-all-space --stat internal/store/
```

Expected: `sqlc generate` exits 0. If it errors on `NOT VALID`, **stop** - the migration needs a different shape and that is a plan-level decision, not an implementation detail. Report it.

Expected diff: **no content change to any `*.sql.go` or `models.go`.** A `CHECK` constraint changes no column type, so nothing generated should move. `git status` may still list generated files as modified while `git diff` shows nothing - that is `core.autocrlf=true` normalising LF churn and it is exactly the case CLAUDE.md warns never to read as "nothing to revert". Revert each such file:

```bash
git checkout -- internal/store/<each generated file git status listed>
git status --porcelain internal/store/
```
Expected: only the two new migration files remain, untracked.

- [ ] **Step 6: Run the constraint test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestUpsertWorkerWorkspace_TheCheckConstraint -count=1 -timeout 900s -v`
Expected: PASS. The error text must contain `worker_workspaces_source_key_len_check`.

- [ ] **Step 7: Run the whole integration lane for this package**

Run: `go test -tags integration -p 1 ./internal/worker/... -count=1 -timeout 1800s`
Expected: PASS. Compare against Task 0's M3 baseline; a new failure here is yours.

Also run `go test -tags integration -p 1 ./internal/store/... -count=1 -timeout 1800s` once: migrations run on startup for every test in that package, so a migration that fails to apply shows up there first.

- [ ] **Step 8: Hygiene and commit**

```bash
git diff --stat
git ls-files --eol internal/store/migrations/000NNN_worker_workspace_text_bounds.up.sql internal/store/migrations/000NNN_worker_workspace_text_bounds.down.sql internal/worker/inventory_bounds_integration_test.go
```
Expected: `i/lf` on every path (after `git add`, since `--eol` reports on tracked files). Confirm the integration test file decodes as UTF-8 and contains **no raw non-ASCII byte** - the only non-ASCII value in it is written as the six ASCII characters `\u00e9`.

```bash
git add internal/store/migrations/ internal/worker/inventory_bounds_integration_test.go
git commit -m "store: add NOT VALID byte-length checks on worker_workspaces"
```

---

### Task 9: Integration - the at-bound row inserts, the one-over row does not

**Files:** Modify `internal/worker/inventory_bounds_integration_test.go`

This is the **only** test that proves a value at the bound actually inserts through the real primary key, the real lookup index and the new `CHECK`. A fake transaction structurally cannot show it, and it is the half that goes red if Task 0's arithmetic is wrong or if someone raises a bound past what the index can hold.

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/inventory_bounds_integration_test.go`:

```go
// TestApplyInventory_ARowAtTheBoundIsStoredAndOneOverIsNot is the end-to-end
// half. Against a real database, the at-bound row must survive the Go check, the
// primary key, worker_workspaces_lookup_idx AND the new CHECK - which is what
// dies if a bound is ever raised past what a btree entry can hold, since all
// three of source_type, source_key and baseline_hash share one lookup-index
// entry.
//
// The pre-existing row is the third assertion and the one a batch-failure
// implementation cannot satisfy: BeginTxFunc rolling back would leave it in
// place, so its ABSENCE is what proves the replace committed.
func TestApplyInventory_ARowAtTheBoundIsStoredAndOneOverIsNot(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "bnd", Hostname: "bnd", CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)

	require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: w.ID, SourceType: "perforce", SourceKey: "//pre/existing", ShortID: "pre",
		BaselineHash: "prebase", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}))

	// 512 is the bound in force; keep this literal in step with the constant and
	// the CHECK. "//" plus filler, so the key is shaped like a depot path.
	atBound := "//" + strings.Repeat("Q", 510)
	require.Equal(t, 512, len(atBound), "fixture: exactly at the bound")
	oneOver := atBound + "Q"

	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, h.ApplyInventory(ctx, w.ID, []*relayv1.WorkspaceInventoryUpdate{
		{SourceType: "perforce", SourceKey: oneOver, ShortId: "over", BaselineHash: "overbase", LastUsedAt: now},
		{SourceType: "perforce", SourceKey: atBound, ShortId: "at", BaselineHash: "atbase", LastUsedAt: now},
	}))

	rows, err := q.ListWorkerWorkspaces(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1,
		"exactly one row: the at-bound one landed, the one-over one was dropped, and the "+
			"pre-existing row was replaced - which only happens if the transaction COMMITTED")
	assert.Equal(t, atBound, rows[0].SourceKey,
		"and it is stored WHOLE - a truncating implementation would store 512 bytes of the "+
			"513-byte key here and canonicalise two workspaces onto one primary-key row")
	assert.Equal(t, uint64(1), h.InventoryRowRejections())
}
```

Add `relayv1 "relay/internal/proto/relayv1"`, `"relay/internal/events"` and `"relay/internal/worker"` to the imports; `newTestStore` is already in this package (`handler_test.go:82`).

- [ ] **Step 2: Run to verify it fails**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestApplyInventory_ARowAtTheBound -count=1 -timeout 900s -v`
Expected: PASS immediately, because Tasks 3-6 already implemented the behaviour. **That is not a TDD failure - it is the correct shape for a test whose whole job is to check that an already-implemented behaviour survives real infrastructure.** Establish its discriminating power in step 3 instead of by a RED.

- [ ] **Step 3: Prove it discriminates (two removals, restored from copies)**

```bash
cp internal/worker/inventory_params.go internal/worker/inventory_params.go.orig
```

(a) Raise `maxWorkspaceSourceKeyBytes` to `3000`. Run the test.
Expected: **FAIL** - the one-over row is now accepted by Go and refused by the `CHECK`, so `ApplyInventory` returns a store error and `ListWorkerWorkspaces` does not hold one row. This is what "the bound raised past what the constraint allows" looks like.

(b) Restore from the copy, then change `checkStorableText`'s length arm to truncate instead of refuse (`s = s[:max]` and return nil). Run the test.
Expected: **FAIL** on `assert.Equal(t, atBound, rows[0].SourceKey)` - two workspaces canonicalised onto one row.

Restore from the copy both times, never with `git checkout --`:

```bash
mv internal/worker/inventory_params.go.orig internal/worker/inventory_params.go
go test -tags integration -p 1 ./internal/worker/... -run TestApplyInventory_ARowAtTheBound -count=1 -timeout 900s
```
Expected: PASS - the control proving the restore applied.

- [ ] **Step 4: Run the whole integration lane**

Run: `go test -tags integration -p 1 ./internal/worker/... -count=1 -timeout 1800s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/inventory_bounds_integration_test.go
git commit -m "worker: pin that an at-bound inventory row inserts through the real indexes"
```

---

### Task 10: README - one sentence

**Files:** Modify `README.md:588` (the `stream` row of the source-field table)

**One sentence, in one row, and nothing else in README changes.** No environment-table row (there is no variable). No counters section (deferred). **Not line 599**, the "Exclusions change the workspace identity" disk paragraph - slice 2 owns that and names it in its own acceptance criterion. Three sibling lanes are running concurrently, so the collision surface is deliberately one line.

- [ ] **Step 1: Read the target line**

Line 588 currently ends: `... Workspaces are keyed by stream, plus the exclusion set when the spec has one (see below), and reused across tasks.`

- [ ] **Step 2: Append the sentence**

Append to that cell, after `reused across tasks.`:

```
 The workspace key is bounded at 512 bytes; the composite form (any spec with an exclusion) adds a fixed 20 bytes, so a stream over 492 bytes falls outside the bound as soon as one exclusion is added. A workspace whose key exceeds the bound is not recorded in the coordinator's inventory, so tasks on that stream always dispatch cold and the workspace does not appear under `relay workers workspaces`. The task still runs.
```

**Substitute Task 0's number for 512 and recompute 492 if it moved.** The 20 is the composite overhead - `"x1|"` (3) plus 16 hex plus `"|"` (1) - and is not affected by Task 0.

`relay workers workspaces` is a real subcommand (`internal/cli/workers.go:65`); if you change the wording, re-check any command you name against that switch.

- [ ] **Step 3: Verify the edit landed as one line**

```bash
git diff --stat README.md
git diff README.md
git ls-files --eol README.md
```
Expected: `1 file changed, 1 insertion(+), 1 deletion(-)`. **A diffstat larger than that means the programmatic edit did something else** - the recorded failure mode is a `\r\r\n` sequence making git reclassify README.md as binary and commit a two-line change as 1845 insertions. `git ls-files --eol` must read `i/lf`. Confirm README.md still decodes as UTF-8 and that the inserted text is pure ASCII.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: state the workspace key bound and what exceeding it costs"
```

---

### Task 11: Comments - four rewrites and one deletion

**Files:**
- Modify: `internal/worker/handler.go` (three counter comments, one handler comment)
- Modify: `internal/worker/handler_register_success_test.go:541-544`

CLAUDE.md forbids in a comment: dates, change history, session or review narrative, measurement provenance, counts of code elsewhere, and uniqueness or completeness claims about **other** code. Stating this function's own contract is fine. **Correcting prose regenerates claims** - re-verify each surviving sentence while editing rather than assuming the untouched half is still true.

- [ ] **Step 1: Rewrite `handleInventoryUpdate`'s comment**

Two of its sentences became false in Task 7: "every string in `u` is bound straight into `UpsertWorkerWorkspace` or `DeleteWorkerWorkspace`" and "no gate ahead of it". The **delete** arm is still exactly that, so the correction is narrower than a deletion. The "Never log `u` itself" sentence **stays and is strengthened**.

Replace the two mechanism paragraphs (`handler.go:2246-2258`) with:

```go
// The UPSERT arm now goes through inventoryUpsertParams, which refuses an
// over-long value, a NUL, or a last_used_at that will not store - so a row that
// used to reach the database and fail its statement is refused ahead of it and
// costs no round trip and no log token. The DELETE arm has no such gate and needs
// none: it binds these values as comparison keys rather than as index tuples.
//
// This line still needs the budget for what remains, which is a genuine store
// fault. Key is kindInventory with NO wire value: a persist failure here is an
// episode, not a per-workspace event, and keying on the source key would multiply
// one infra event by the workspace count.
//
// NEVER LOG u ITSELF - source_key is a caller-supplied, unbounded depot path -
// and note that this is now true one layer further out too: the refusal error
// inventoryUpsertParams returns carries a column name and a bound and never the
// value, so even a future caller that logs it cannot be logged into by an agent.
```

- [ ] **Step 2: Delete the ordinals from the three counter comments**

A fifth counter makes "the four" stale, and a count of other fields is a count of code elsewhere. **Delete the ordinals rather than bumping them.** Keep the load-bearing property: no input moves more than one of these, do not sum them, do not merge the sections.

- `handler.go:396` - `// A DIFFERENT NOUN FROM ingestDrops, and neither number covers any part of` becomes `// A DISTINCT NOUN FROM ingestDrops, and neither number covers any part of`. Rest of that paragraph unchanged.
- `handler.go:419` - `// A THIRD DISTINCT NOUN. ingestDrops counts LOG LINES THE BUDGET DROPPED;` becomes `// A DISTINCT NOUN AGAIN. ingestDrops counts LOG LINES THE BUDGET DROPPED;`, and three lines later `// one of the three. Do not sum them...` becomes `// one of them. Do not sum them...`.
- `handler.go:428` - `// A FOURTH DISTINCT NOUN, and no input moves more than one of the four. Read` becomes `// A DISTINCT NOUN, and no input moves more than one of these counters. Read`. Also drop the trailing `; see the plan's scope decision` from `:430`, which points at a document rather than at code.

- [ ] **Step 3: Delete the stale paragraph in the test file**

`internal/worker/handler_register_success_test.go:541-544` currently claims `bug-2026-08-23-applyinventory-null-timestamp` is open and that fixing it is not this slice's job. Task 5 fixed it. Delete those four lines outright. Do **not** rewrite them into a fresh claim about another item's status - that is history, and a correction that restates a claim buys a new one.

The two lines above them ("Turning that log.Printf into a return is a plausible edit...") stay: they describe this test's own property and are still true. The injected error stays as it is - a NOT NULL violation is still a plausible store fault, and the test asserts the log-and-continue shape, not the bug.

**If Task 5 was cut, skip this step entirely** and leave the paragraph exactly as it is.

- [ ] **Step 4: Verify no forbidden content entered any comment**

Grep the diff for: a four-digit year, "was previously", "used to be" attached to a session rather than to code behaviour, "measured", "the only", "every other", "N sites", and the figure `2704`. None may appear in a comment this slice adds.

```bash
git diff internal/worker/ | grep -nE '^\+.*(20[0-9]{2}-|measured|the only|2704)'
```
Expected: no output. Note that `registrationStoreFault`'s pre-existing `~2704` comments are **not** in this diff and are not yours (see refutation 6).

- [ ] **Step 5: Run the default lane and check hygiene**

```bash
go test ./internal/worker/... -count=1
gofmt -l internal/worker/inventory_params.go internal/worker/inventory_params_test.go internal/worker/inventory_bounds_test.go
git diff --stat internal/worker/
git ls-files --eol internal/worker/handler.go internal/worker/handler_register_success_test.go
```
Expected: tests PASS; `gofmt -l` names none of the three files (do **not** run bare `gofmt -l internal/` - it lists hundreds of files purely because of working-copy CRLF and is useless as a signal here); diffstat is a few dozen lines; both paths read `i/lf`.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_register_success_test.go
git commit -m "worker: correct the inventory and counter comments for the new gate"
```

---

### Task 12: Mutation battery

**Files:** none committed. Run in an isolated worktree.

- [ ] **Step 1: Create the isolated worktree and confirm a green baseline in it**

```bash
git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mut-w HEAD
cd C:/Users/chadv/AppData/Local/Temp/relay-mut-w
go test ./internal/worker/... -count=1
```
Expected: PASS. **A uniform result across the mutations below means a broken harness, not a strong suite** - this baseline is what distinguishes the two. Never mutate the shared worktree: three sibling agents are reading it.

- [ ] **Step 2: Run each mutation**

For each row: copy the file to `<file>.orig`, apply the mutation, **confirm the mutation actually applied** (`git diff` in the isolated worktree shows the edit, and it is a behavioural edit rather than an inert one), run the named test, record RED or SURVIVED, then restore from the copy and re-run to confirm the restore applied. **Never `git checkout --`.**

A kill must **name its guard**: if a mutation reddens a test other than the one predicted, trace the failing assertion before recording the kill - it may be dying for a different reason, and a degenerate fixture value is the usual cause.

| # | Mutation | Must go RED |
|---|---|---|
| 1 | Delete the `source_key` length arm of `checkStorableText` | `TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits` (four execs, want three) and `TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes` |
| 2 | `len(s) > max` becomes `len(s) >= max` | `TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes` at-bound rows, and every `at the bound is accepted` subtest |
| 3 | `len(s) > max` becomes `len(s) > max+1` | `one byte over is refused`, all four columns |
| 4 | `len(s)` becomes `utf8.RuneCountInString(s)` | `TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes`, the `multi-byte one byte over` row **only** - confirm the other three rows still pass, which is what shows that row is the discriminator |
| 5 | `maxWorkspaceSourceKeyBytes` 512 -> 4096 | `TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits` (the 4096-byte fixture is at the new bound; if it survives, raise the mutation to 8192 and record which value killed it) |
| 6 | `maxWorkspaceSourceTypeBytes` 64 -> 4096 | `source_type one byte over is refused` |
| 7 | `maxWorkspaceShortIDBytes` 128 -> 4096 | `short_id one byte over is refused` |
| 8 | `maxWorkspaceBaselineHashBytes` 128 -> 4096 | `baseline_hash one byte over is refused` |
| 9 | Delete the `source_type` predicate call from the constructor | `source_type one byte over is refused`, `TestApplyInventory_EveryRefusedRowIsDropped/source_type over bound` |
| 10 | Delete the `short_id` predicate call | `short_id` subtests |
| 11 | Delete the `baseline_hash` predicate call | `baseline_hash` subtests |
| 12 | Delete the NUL arm of `checkStorableText` | every `carrying a NUL is refused` subtest and the four NUL batch cases |
| 13 | Delete the `ts.IsZero()` arm | `parses but is the zero time` and `last_used_at is the zero time` |
| 14 | Restore `ts, _ := time.Parse(...)` (discard the error) | `blank`, `not RFC3339`, `last_used_at unparseable` |
| 15 | **The drop-vs-fail branch:** `continue` in `applyInventory`'s refusal branch becomes `return err` | `TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits` on **commits == 0** and on the `require.NoError`. **This is the mutation the slice exists to prevent; if it survives, stop and fix the test before anything else.** |
| 16 | `continue` becomes `break` | Same test: two execs instead of three, and the bad entry being FIRST is what makes this detectable |
| 17 | The constructor is called and its error discarded (`p, _ := ...`) | The same test (a zero-value params row is upserted, so four execs) and `TestApplyInventoryUpdate_ARefusedRowIssuesNoStatement` |
| 18 | `h.inventoryRowRejects.Add(1)` moved to the accept branch | `TestInventoryRowRejections_CountsRefusalsAndNothingElse`, **second half** |
| 19 | `h.inventoryRowRejects.Add(1)` deleted from `applyInventory` | Same test, first half |
| 20 | The `errors.Is` early return deleted from `handleInventoryUpdate` | `TestHandleInventoryUpdate_ARefusedRowSpendsNoLogBudget` |
| 21 | `handleInventoryUpdate` returns early on **every** error, not just the sentinel | `TestHandleInventoryUpdate_AStoreFaultStillSpendsABudgetToken` |
| 22 | The `CHECK` on `source_key` written with `length()` instead of `octet_length()` | `TestUpsertWorkerWorkspace_TheCheckConstraintRefusesAnOverLongKey` (integration) |
| 23 | The `source_key` `CHECK` deleted from the migration | Same test |
| 24 | The `CHECK` bound raised to a value the btree cannot hold (e.g. 4000 on `source_key`, with `maxWorkspaceSourceKeyBytes` raised to match) | `TestApplyInventory_ARowAtTheBoundIsStoredAndOneOverIsNot` (integration) - the at-bound row fails to insert |

Mutations 22-24 need a fresh database (migrations run on startup), so run them with the integration lane and a clean container or a fresh `RELAY_TEST_DATABASE_URL` database.

- [ ] **Step 3: Record the results and clean up**

Record every row as RED (naming the failing test and assertion) or SURVIVED. **A survivor is a finding**: name the missing assertion and either add it or state plainly why it is accepted.

```bash
cd D:/dev/relay/.claude/worktrees/lane-ws-bounds
git worktree remove --force C:/Users/chadv/AppData/Local/Temp/relay-mut-w
```

No commit; the results go in the PR body.

---

### Task 13: Whole-slice verification, PR and backlog closes (conductor)

- [ ] **Step 1: Confirm the scope fence held**

```bash
git diff --name-only origin/main...HEAD
```
Expected, and nothing else: `README.md`, `internal/worker/handler.go`, `internal/worker/handler_register_success_test.go`, `internal/worker/inventory_params.go`, `internal/worker/inventory_params_test.go`, `internal/worker/inventory_bounds_test.go`, `internal/worker/inventory_bounds_integration_test.go`, two files under `internal/store/migrations/`, and the plan doc.

**Any path under `internal/jobspec/`, `internal/schedrunner/`, `cmd/relay-server/`, `internal/api/`, `internal/testsupport/`, `internal/agent/`, `internal/store/query/`, `internal/store/*.sql.go`, or `web/` is a scope breach.** Investigate before proceeding - `internal/store/query/worker_workspaces.sql` in particular must be byte-identical.

- [ ] **Step 2: Re-run the construction-site search and record the number**

```bash
git grep -n "store.UpsertWorkerWorkspaceParams" -- '*.go'
```
Expected after this slice: **9 sites outside generated code** - 1 production (inside `inventoryUpsertParams`) and 8 test fixtures (the 7 that pre-date this slice plus Task 8's deliberate bypass). Record the number. Do **not** write "the only" anywhere: it is a claim about the complement and this search is the only thing that can support it, so the number belongs in the PR and not in a comment.

- [ ] **Step 3: Run every lane**

```bash
go test ./... -count=1
go test -tags integration -p 1 ./internal/worker/... -count=1 -timeout 1800s
go test -tags integration -p 1 ./internal/store/... -count=1 -timeout 1800s
go vet ./internal/worker/...
```
Expected: PASS throughout.

Attempt the race lane, preferably in the container (`MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 golang:1.26 go test -race ./... -count=1 -timeout 600s`). **If it will not run, say so plainly in the PR.** Do not substitute `-count=N`; that raises confidence in flakiness, not in race-freedom. If `internal/testsupport/pgdsn` is red in the container on an otherwise clean tree, **that is LANE E's** - report it, do not fix it.

- [ ] **Step 4: Whole-diff hygiene**

```bash
git diff --stat origin/main...HEAD
git ls-files --eol $(git diff --name-only origin/main...HEAD)
```
Expected: a diffstat proportionate to the change (roughly 600-800 lines, most of it new test files); every path reads `i/lf`. Confirm each touched file still decodes as UTF-8, and that the only non-ASCII value anywhere in the diff is written as the escape `\u00e9`.

- [ ] **Step 5: Open the PR**

Write the body to a scratchpad file and pass `--body-file` (a long inline `--body` heredoc trips the classifier). It must carry:

1. **Task 0's three measurements**, with M2's input alongside its number, and an explicit statement if no p4 environment was reachable.
2. **The behavioural RED output** from Task 3 step 2, verbatim.
3. **The behaviour change in the timestamp arm**: a currently loud failure (a rolled-back transaction, or one budgeted log line) becomes a silent counted drop. This is the intended direction and it is what makes "one malformed row cannot freeze a worker's inventory" true rather than nearly true.
4. **The mutation battery table** with RED/SURVIVED per row.
5. **Which lanes ran** and which did not, by name.
6. **The construction-site count** from step 2.
7. **The derivation of 512** - top-down from the summed lookup-index entry (64 + 512 + 128 = 704 against the measured limit), bottom-up against the honest producers - which belongs here and not in a code comment.
8. **The condition that would buy the AST guard**: a third production writer to `worker_workspaces`.

- [ ] **Step 6: Close both backlog items**

Use the command, never a hand edit of the `status` field. The command does the `git mv` into `docs/backlog/closed/`, the frontmatter stamps and the `## Resolution` note; flipping `status` alone leaves a malformed open item behind.

```
/backlog close idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key
/backlog close bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory
```

**Close the second only if Task 5 shipped.** If the timestamp arm was cut, leave it open and say so in the PR.

- [ ] **Step 7: File the follow-ups the spec proposes (conductor, `/backlog`)**

None are filed by the spec and none are filed by this plan. Four are proposed:

1. **Publish the deferred `internal/worker` counters on `GET /v1/server/counters`.** This slice adds the second unpublished one; `enrollmentRefusals` is the first. One new section per noun, with the forgeability sentence stated where each number is READ. "Deferred to its own item" has now happened twice, which is the argument for raising its priority.
2. **Bound `source.stream` and `sync[i].path` lengths in `jobspec.validateSourceSpec`.** The complement: it refuses an over-long stream at submission with a 400 instead of silently losing the warm bias later. **It does not close the item this slice closes and must not be filed as doing so** - the value bounded here arrives from the agent over gRPC, which can send any string regardless of what any spec said. `internal/jobspec/` is LANE S's file in this batch, so this needs its own item for a lane that owns it.
3. **A vocabulary check on `source_type`.** Length is bounded here; the set is not. An allow-list was deliberately rejected at this site: a new provider is introduced by a coordinated change on both sides, and an allow-list would fail closed by silently discarding a new provider's entire inventory.
4. **Mirror the key bound into the agent, or decide not to, once a second provider exists.** Today the mirror is wrong because it converts a silent slowdown into a task failure on input the operator chose legitimately. That balance changes if a provider ever produces keys near the bound.

---

## Task 0 results

```
M1 btree index entry limit: 2704 bytes, on
    "PostgreSQL 16.13 (Debian 16.13-1.pgdg13+1) on x86_64-pc-linux-gnu, compiled by
     gcc (Debian 14.2.0-19) 14.2.0, 64-bit" - a throwaway postgres:16 container, the
     same image internal/testsupport/pgdsn runs.
    Error, verbatim: index row size 3024 exceeds btree version 4 maximum 2704 for
    index "t2_k_idx"
    704-byte lookup-index payload against it is a headroom factor of 3.84.

    THE PLAN'S PRESCRIBED INSTRUMENT DOES NOT PRODUCE THIS MEASUREMENT. A
    12800-byte high-entropy value (400 md5 hexes) does fail, but with "index row
    requires 12816 bytes, maximum size is 8191" - the INDEX_SIZE_MASK ceiling on
    index-tuple formation, which fires before the btree page check and states a
    different, larger number. Reading 8191 off that error and treating it as the
    btree limit would have been a 3x overstatement of the headroom. The value that
    reaches the btree message is 3008 bytes (94 md5 hexes): over 2704, under 8191.

    Measured directly as well, which is stronger than the arithmetic: a row of
    64/512/128/128 high-entropy bytes INSERTs through a temp table carrying
    worker_workspaces' real PRIMARY KEY (worker_id, source_type, source_key) and its
    real worker_workspaces_lookup_idx (source_type, source_key, baseline_hash).

M2 longest real stream: 20 bytes.
    Input: "//streams/GameX/main"
    Source: docs/superpowers/specs/2026-04-24-perforce-workspace-management-design.md,
    which is a design document, not production data.
    NO P4 ENVIRONMENT AND NO POPULATED DATABASE WAS REACHABLE IN THIS SESSION. There
    is no dev Postgres running, so worker_workspaces and tasks.source could not be
    queried; examples/*.json carry no source.stream field at all. What was reachable:
    the p4d integration containers' own streams, "//test/main" and "//test/virt", 11
    bytes each (internal/agent/source/perforce/perforce_integration_test.go); README's
    example stream "//depot/film-x/main", 19 bytes. The longest depot path of any kind
    in README is "//depot/film-x/main/Content/Movies/...", 38 bytes, and that is a
    sync path rather than a stream.
    Escalation (>300 bytes): did not fire, by a factor of 15 against the largest
    figure available - but the corpus is test and documentation values, so 512 is
    recorded as ANCHORED TOP-DOWN ONLY, on M1. The bottom-up direction is a sanity
    check here and not a derivation.

M3 green baseline, on a clean tree, before any change:
    go test ./internal/worker/... -count=1
      -> ok  relay/internal/worker  3.814s   (124 top-level tests)
    RELAY_TEST_DATABASE_URL=... go test -tags integration -p 1 ./internal/worker/...
      -> ok  relay/internal/worker  90.569s  (213 top-level tests)
    No package reported anything but ok in either lane.

Number in force for this slice: source_key 512 bytes. Unmoved by Task 0.
Unchanged by Task 0 regardless of result: the constructor as enforcement point,
drop-the-row, no env knob, NOT VALID.
```
