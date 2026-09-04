# Sync-spec exclusion paths implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a `source.sync` entry say `"exclude": true`, and make that exclusion real - the excluded subtree is never transferred, and no other task can observe a workspace missing files it asked for.

**Architecture:** The exclusion set enters the workspace identity (`SourceKey`), so a differently-excluded task gets a different `short_id`, directory and p4 client. Inside that workspace the exclusion is applied with a have-list preempt - `p4 -c <client> sync -k <client-path><resolved-rev>` before the real sync. The coordinator gains `scheduler.SourceKeyFromAPISpec`, the exact sibling of `BaselineHashFromAPISpec`, so the warm bias keys on the same string the agent registers.

**Tech Stack:** Go 1.26, protobuf (`make generate`), sqlc, pydantic v2 (Python SDK), testcontainers-go + p4d.

**Spec:** `docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md`
**Item:** `docs/backlog/feature-2026-09-04-implement-sync-spec-exclusion-paths.md`

---

## Slice independence declaration

**There is no frontend slice.** `web/src/jobs/specTemplate.ts`'s `validateSpecText` deliberately does not model `source` (its own comment lists `source` among the rules "left to the server ... so the two paths cannot drift"), and the Perforce source builder does not exist yet (`feature-2026-09-03-perforce-source-builder-in-the-new-job-builder` is open). Nothing under `web/` changes. Do not dispatch `relay-frontend-engineer`.

**The backend tasks are SEQUENTIAL.** One dependency chain:

- Task 5 (`make generate`) must land before Tasks 7-11 compile: `SourceKey`, `BaselineHash`, `Prepare` and `sourceSpecToProto` all read `relayv1.SyncEntry.Exclude`.
- Task 8 (`BaselineHash`) must land after Task 0, which captures the golden literal **at HEAD**. Once the encoding changes the literal is unrecoverable.
- Task 10 (`Prepare`) needs Tasks 3, 7, 8, 9. Task 11 needs Task 7. Task 12 needs everything, and Task 1 before it.
- Task 2's assertions do not compile until Task 9 exists; the task says so and where to resume.

The only genuinely independent pair is **Task 6 (Python SDK) against Tasks 7-11**. It is small; do not build a lane for it. Run one agent, in order.

## This plan has two stages - hand them to the backlog

Two units, meant to be two PRs. After reading this plan, run:

```
/backlog phases docs/superpowers/plans/2026-09-04-sync-spec-exclusion-paths-plan.md
```

## Stage 1 - Exclusions end to end on one relay version

**Closes:** feature-2026-09-04-implement-sync-spec-exclusion-paths

Tasks 0-13. Ships the field, the validation, the workspace identity, the preempt, the warm bias, the p4d acceptance test, and a README that states the version-skew limitation Stage 2 removes.

## Stage 2 - Close the version skew (`supports_sync_exclusions`)

Tasks 14-17. Spec section 8 permits Stage 1 to ship without it **only** if the cut is recorded in README as a stated limitation, which Task 13 does. Stage 2 deletes that sentence.

---

## What I verified in the spec, and what I refuted

Read once for contradiction before planning. Checked by symbol, never by line number.

**Verified true against this worktree:**

1. `BaselineHash` reads exactly `e.Path` and `e.Rev`. An `Exclude` field hashes identically to its absence.
2. `selectWorker` compares `ws.SourceKey == taskSrc.Stream`, and `Dispatcher.dispatch` appends `s.Stream` into `streamsByType`. There is no server-side source-key function; one has to be created.
3. `DisallowUnknownFields` appears nowhere in `internal/api`. The field fails OPEN on server skew, as the spec says.
4. `validateSpecText` names `source` as a rule it deliberately does not implement.
5. `python/src/relay/models.py`'s `Sync` carries `ConfigDict(extra="forbid")`, so `exclude` is a hard SDK-side rejection until the SDK ships the field.
6. No legal stream can start with `x1|`: `validateSourceSpec` refuses a stream not starting with `//`. The composite key cannot collide with a bare stream.
7. `worker_workspaces.source_key` is `TEXT` inside `PRIMARY KEY (worker_id, source_type, source_key)` and inside `worker_workspaces_lookup_idx (source_type, source_key, baseline_hash)` - `000007_workspaces.up.sql`.
8. `allocateShortID(stream, reg)` separates two exclusion sets only through `ShortIDInUse` extending the candidate length. The plan moves its argument to the key, so the accident stops mattering.
9. `execRunner.Stream` DISCARDS stderr on a zero exit (`waitErr == nil` returns `nil`, the buffer dies). A new runner method is genuinely required, exactly as section 7 implies.
10. `Request.SyncPaths` feeds `Workspace.syncedPaths` and `PathPrefixOverlap` in `tryAdmit`/`modeForEmptyWorkspace`/`release`. Section 6.4 is right that an excluded path must not go there.

**Refuted or corrected, and these corrections are load-bearing:**

- **Section 5 rule 3 is self-contradictory, and this plan takes the stricter reading.** It says "covered by exactly one include, at one revision", then justifies refusal only for "two covering includes at different revs". Those are different rules and the weaker one is unsafe: two covering includes both spelled `#head` are literally equal, but `Prepare` calls `ResolveHead` **per path**, so `//s/x/...#head` and `//s/x/Content/...#head` can resolve to different changelists. The validator cannot see that - resolution happens on the agent, minutes later. Per the spec's own argument, a preempt at a newer revision fetches the whole excluded subtree backwards. **Task 4 refuses more than one covering include outright.** The spec's stated negative is a strict subset, so its acceptance criterion still holds.
- **Section 5 rule 4, implemented literally, never fires.** "An exclusion equal to, or a prefix of, its covering include" - but if the exclusion is a strict prefix of an include, that include does not *cover* it (coverage is directional), so "its covering include" names the wrong entry. Example: `sync: [//s/x/..., //s/x/Content/Movies/...]` with an exclusion of `//s/x/Content/...`; the coverer is the first include, the swallowed one is the second. **Task 4 checks the exclusion against EVERY include.**
- **Section 5's "implemented locally in `jobspec`" is followed for containment and DROPPED for the covering predicate.** Its reason is real (`jobspec` must not import an agent package) but its conclusion runs the wrong way: `internal/jobspec` imports only `errors`, `fmt`, `regexp`, `sort`, `strings`, so `internal/agent/source/perforce` can import **it**, no cycle. The agent needs the identical predicate to pick the covering include at preempt time. Task 4 exports `jobspec.DepotPathCovers`; Task 10 calls it. One implementation, two callers.
- **The spec never mentions that its own fixture change breaks an existing assertion.** `TestPerforce_E2E_SyncAndUnshelve` asserts `"1 files; 0 other lines"`. Adding the subdirectory file section 11 requires makes that `2 files`. **Task 1 changes the fixture and the assertion in one commit**; splitting them leaves a red p4d lane that reads as a regression in the feature.
- **The item's claim that the design routes around `idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key` is true, and true only by construction.** `SourceKey` adds exactly 20 bytes (`"x1|"` + 16 hex + `"|"`) to a string the schema already stores unbounded today. That is a property of the key FUNCTION, and it evaporates the moment someone changes the canonicalisation to inline paths. **Task 7 converts the claim into a check:** `TestSourceKey_IsBoundedAtTwentyBytesOverTheStream`. Nothing here touches the store; that item stays open and correct.
- **`bug-2026-09-04-a-subpath-of-a-renaming-remap-does-not-resolve` does NOT change this plan, and this plan makes it louder rather than fixing it.** `toClientPath` is a string rewrite, so an exclusion under a renaming remap emits a client path naming nothing. That path reaches `p4 sync -k`, p4 exits ZERO, and section 7's no-such-files refusal turns a silently inert exclusion into a failed prepare naming the path. Task 10's refusal message names the remap case. Two riders for whoever takes that bug: its acceptance bullet about `TestToClientPath` now has a second subject, `preemptSpecs`, which calls `toClientPath` on every excluded path; and Task 1 already gives it the fixture row it asks for ("a file under a subdirectory in `//test/main`"), so it should not add a second.

**Not refuted, stated so nobody re-derives it:** section 3.4's disk trade is negative for a mixed-exclusion stream on one agent (`2S - X`, `X < S` always). Nothing here adds admission control over workspace count and nothing measures the eviction churn it converts into; `idea-2026-09-04-no-workspace-size-or-eviction-instrumentation` already carries that.

## Every site that computes or compares a source key, per language

The item asks for this enumerated rather than assumed, so here it is by language, with the guard that holds each.

**Go - COMPUTES the key (2 sites).** `perforce.SourceKey` (agent, Task 7) and `scheduler.SourceKeyFromAPISpec` (coordinator, Task 11). Kept in lockstep **by construction, not by discipline**: the coordinator's function delegates to the agent's through `sourceSpecToProto`, so there is exactly one implementation. Guards: `TestSourceKeyFromAPISpec_DelegatesToThePerforceFunction` (Task 11) feeds one spec through both and asserts equality; `TestSourceSpecToProto_SyncEntryArityMatches` (Task 5) reddens if the conversion the delegation runs through starts dropping a field.

**Go - COMPARES the key (2 sites).** `selectWorker`'s `ws.SourceKey == warmKey` and `Dispatcher.dispatch`'s candidate list. Both are fed from `SourceKeyFromAPISpec` after Task 11; the candidate builder is extracted as `warmKeysForTasks` precisely so a default-lane test can pin that they use the same producer.

**Go - REGISTERS the key (4 sites, all in `Prepare`).** `reg.GetBySourceKey`, `allocateShortID`, `reg.Upsert{SourceKey:}`, `perforceHandle.sourceKey`. Guard: `TestProvider_EveryWorkspaceIdentitySiteUsesTheSameKey` (Task 10) asserts the handle's `Inventory().SourceKey` equals `SourceKey(pf)` AND that `allocateShortID(SourceKey(pf), &Registry{})` reproduces the allocated `ShortID` - so a site left on `pf.Stream` is RED.

**Proto - carries the input to the key (1 site).** `relayv1.SyncEntry.exclude`. Guard: the arity test in Task 5.

**Python - carries the input to the key (1 site).** `relay.models.Sync.exclude`. It computes no key and never has: `source_key`, `baseline`, `short_id` appear nowhere under `python/src`. Guard: `test_an_excluded_sync_serializes_a_payload_the_server_accepts` (Task 6) pins the WIRE SHAPE the Go validator must accept.

**TypeScript - none.** `web/` models no part of `source`; verified above.

**The one pair with no guard, named rather than papered over:** Go's `jobspec.SyncEntry` against Python's `relay.models.Sync`. A Go commit adding a seventh field to `SyncEntry` cannot redden anything under `python/`, and a guard placed under `.github/workflows/python.yml`'s `paths: python/**` filter could not fire on that commit anyway. This plan does not build the Go-side guard that would close it (a Go test parsing `python/src/relay/models.py`) because there is no precedent for one in this repo and the spec does not ask. **Recommend a backlog item** - see "For the conductor".

## File structure

**Create:**

| File | Responsibility |
|---|---|
| `internal/agent/source/perforce/sourcekey.go` | `SourceKey` - the workspace identity function, nothing else. |
| `internal/agent/source/perforce/sourcekey_test.go` | Its unit tests, including the 20-byte bound. |
| `internal/agent/source/perforce/preempt.go` | `preemptSpec`, `preemptSpecs`, `preemptReportedNoSuchFiles`. |
| `internal/agent/source/perforce/preempt_test.go` | Default-lane tests for both, against captured artifacts. |
| `internal/agent/source/perforce/testdata/p4-sync-k/{marked,uptodate,nosuchfile}.txt` | Captured `p4 sync -k` output for the three cases. |
| `internal/agent/source/perforce/perforce_exclusion_integration_test.go` | The capture test and the acceptance test (p4d lane). |
| `internal/scheduler/source_proto_test.go` | Arity + value guards, and `SourceKeyFromAPISpec`. |
| `internal/store/migrations/000024_workers_supports_sync_exclusions.{up,down}.sql` | Stage 2 only. |

**Modify:** `internal/jobspec/jobspec.go`, `internal/api/job_spec_source_test.go`, `proto/relayv1/relay.proto`, `internal/proto/relayv1/relay.pb.go` (regenerated only), `internal/agent/source/perforce/{baseline.go,baseline_test.go,client.go,fixtures_test.go,provider_evict_recheck_test.go,perforce.go,perforce_integration_test.go,testdata/p4d/entrypoint.sh}`, `internal/scheduler/{source_proto.go,dispatch.go,select_worker_test.go}`, `python/src/relay/models.py`, `python/tests/unit/test_models.py`, `README.md`. Stage 2 adds `internal/store/query/workers.sql`, `internal/worker/handler.go`, `internal/agent/agent.go`.

**Do not touch:** `internal/store/*.sql.go`, `internal/store/models.go`, `web/**`, `internal/agent/source/perforce/diagnostics.go` (section 9 refuses the `classifyP4Error` change).

## Standing rules for every task

1. **Windows worktree, CRLF repo.** After any programmatic edit to a tracked text file: `git ls-files --eol <path>` must read `i/lf`, and the diffstat must match the size of the change you intended. `git diff` alone cannot tell you nothing changed.
2. **After `make generate`:** `git diff --ignore-all-space`, keep only the real content change, `git checkout -- <file>` every file whose only hunks are line-ending churn. Then re-open the regenerated `.pb.go` and confirm your field is in it - the CRLF revert can silently discard the regeneration.
3. **Never edit `*.sql.go` or `models.go`.**
4. **Lanes.** `make test` is the default lane and runs in CI. The p4d lane (`//go:build integration` in `internal/agent/source/perforce`) **runs only when a human runs it**; every test this plan puts there carries a comment saying so and naming what would have to exist for CI to run it.
5. **p4d lane invocation:** `go test -tags integration -p 1 ./internal/agent/source/perforce/... -run <Name> -v -timeout 1800s`, with Docker Desktop up and `p4` on PATH.
6. **Never `git checkout --` to revert a mutation** - it discards the uncommitted guard under test. Copy the file first, restore from the copy, re-run.
7. **Commit at the end of every task** with an explicit pathspec. Never `git add -A`.

---

## Task 0: baseline both ways, and capture the golden hash AT HEAD

**Lane:** default.
**Files:** Modify `internal/agent/source/perforce/baseline_test.go`.

The golden literal is only obtainable before the encoding changes.

- [ ] **Step 1: record the starting state**

```bash
cd D:/dev/relay/.claude/worktrees/now-e-exclusion-paths
go build ./... 2>&1 | tail -20
go test ./internal/jobspec/... ./internal/scheduler/... ./internal/agent/source/perforce/... ./internal/api/... -count=1 2>&1 | tail -20
```

Expected: build clean, four `ok` lines. If anything is red at HEAD, stop and report the number both ways before touching a line.

- [ ] **Step 2: write the golden test with a deliberately wrong literal**

Append to `baseline_test.go`:

```go
// The no-exclusion encoding is a CROSS-PROCESS, CROSS-FLEET contract:
// scheduler.BaselineHashFromAPISpec computes it server-side for warm scoring,
// and every warm workspace in the fleet re-syncs once if it moves. The literal
// is captured from the binary, not derived from the implementation, so an
// encoding change is a deliberate RED here rather than a silent fleet event.
//
// THE FIXTURE IS BUILT TO SEE A SHIFTED SEPARATOR: two entries whose revs are
// both non-empty and different, and two unshelves, so dropping any one NUL or
// the {1} section terminator re-associates content across a boundary and moves
// the digest.
const goldenNoExclusionBaseline = "0000000000000000"

func TestBaselineHash_NoExclusionsIsUnchanged(t *testing.T) {
	p := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/a/...", Rev: "@100"},
			{Path: "//s/x/b/...", Rev: "@200"},
		},
		Unshelves: []int64{2, 1},
	}
	require.Equal(t, goldenNoExclusionBaseline, BaselineHash(p, nil))
}
```

- [ ] **Step 3: run it, and READ the actual value out of the failure**

```bash
go test ./internal/agent/source/perforce/ -run TestBaselineHash_NoExclusionsIsUnchanged -v -count=1
```

Expected: FAIL, with testify printing `expected: "0000000000000000"` and `actual: "<16 hex chars>"`. Copy the actual.

- [ ] **Step 4: paste the captured literal and re-run**

Replace `"0000000000000000"` with the 16 characters you just read, then re-run the same command. Expected: PASS.

- [ ] **Step 5: prove the golden is not decorative**

```bash
cp internal/agent/source/perforce/baseline.go /tmp/lane-e-baseline.go.bak
```

Change `h.Write([]byte{1})` in `BaselineHash` to `h.Write([]byte{3})`, re-run: expected FAIL. Then `cp /tmp/lane-e-baseline.go.bak internal/agent/source/perforce/baseline.go` and re-run: expected PASS.

- [ ] **Step 6: commit**

```bash
git add internal/agent/source/perforce/baseline_test.go
git commit -m "test: pin the no-exclusion BaselineHash encoding to a literal captured at HEAD"
```

---

## Task 1: the p4d fixture gains a subdirectory, and the assertion it breaks moves with it

**Lane:** p4d integration (human-run only).
**Files:** Modify `internal/agent/source/perforce/testdata/p4d/entrypoint.sh`; modify `internal/agent/source/perforce/perforce_integration_test.go` (the `1 files; 0 other lines` assertion).

An exclusion needs something to exclude. `//test/main` holds exactly one file today.

- [ ] **Step 1: add the file to the fixture**

In `entrypoint.sh`, immediately after `p4 submit -d "init"` and **before** the `creating shelved CL` block:

```bash
echo "[entrypoint] populating //test/main/heavy/ so an exclusion has a subtree to exclude ..."
mkdir -p "${WORKDIR}/heavy"
echo "heavy" > "${WORKDIR}/heavy/asset.txt"
p4 add "${WORKDIR}/heavy/asset.txt"
p4 submit -d "heavy subtree"
```

- [ ] **Step 2: run the existing p4d suite and watch it go RED**

```bash
go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E -v -timeout 1800s
```

Expected: `TestPerforce_E2E_SyncAndUnshelve` FAILS on `1 files; 0 other lines` (the sync now transfers two files). `TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout` still PASSES - the new file lands at `sub/heavy/asset.txt` and that test's `NoFileExists(wsDir/readme.txt)` is untouched. This RED is the point of running it: a fixture change with no production change still breaks tests, and it is invisible to diff review.

- [ ] **Step 3: move the assertion**

```go
	// The two-file baseline (readme.txt plus heavy/asset.txt) produces exactly
	// two file lines and no totals line.
	require.Len(t, progress1, 2, "the two brackets and nothing else, got: %v", progress1)
	require.Equal(t, 1, countLinesContaining(progress1, "2 files; 0 other lines"),
		"real p4 wrote two file lines and the counter saw them, got: %v", progress1)
```

`require.Len(t, progress1, 2)` counts PROGRESS LINES, not files, and stays at 2.

- [ ] **Step 4: re-run.** Same command as Step 2. Expected: both tests PASS.

- [ ] **Step 5: commit**

```bash
git add internal/agent/source/perforce/testdata/p4d/entrypoint.sh internal/agent/source/perforce/perforce_integration_test.go
git commit -m "test(p4d): give //test/main a heavy/ subtree, and move the file-count assertion with it"
```

---

## Task 2: capture what `p4 sync -k` actually emits, before writing any parser

**Lane:** p4d integration (human-run only); the artifacts it writes are then read by the default lane.
**Files:** Create `internal/agent/source/perforce/perforce_exclusion_integration_test.go` and `testdata/p4-sync-k/{marked,uptodate,nosuchfile}.txt`.

Nothing in this repository has ever recorded this command's output. The clobber slice's `Options:` line is the precedent: the captured artifact carried a shape no document mentioned, and it was the shape that mattered.

- [ ] **Step 1: write the capture-and-assert test**

```go
//go:build integration

package perforce

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// p4dEnv points the process at the fixture container and neutralises host P4
// configuration, as every other test in this lane does.
func p4dEnv(t *testing.T) p4dHandle {
	t.Helper()
	p4d := startP4dContainer(t)
	t.Setenv("P4PORT", p4d.P4Port)
	t.Setenv("P4USER", p4d.P4User)
	t.Setenv("P4CHARSET", "none")
	t.Setenv("P4CONFIG", "")
	t.Setenv("P4PASSWD", "")
	t.Setenv("P4TICKETS", "")
	return p4d
}

// THIS TEST CANNOT RUN IN CI, AND THE GAP IS DELIBERATE.
// .github/workflows/go-ci.yml runs `go test -race ./...` with no build tags plus
// two `services: postgres` jobs; nothing there provides a p4d server or the `p4`
// client binary. For this to join a CI lane there would have to be a workflow job
// that (a) builds testdata/p4d or runs an equivalent service container, (b)
// installs the Perforce CLI on the runner, and (c) is added to a Makefile
// target's package list the way test-pg-integration hardcodes its own. Until
// then it is human-run.
//
// It cannot move to the default lane at all: the entire property under test is
// what REAL p4 writes and which exit status it pairs that text with. A fake
// runner echoes whatever it is told.
func TestPerforce_E2E_SyncKReportsNoSuchFilesOnStderrAndExitsZero(t *testing.T) {
	p4dEnv(t)

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//test/main",
			Sync:   []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}},
		},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	h, err := prov.Prepare(ctx, "task-capture", spec, func(s string) { t.Logf("prepare: %s", s) })
	require.NoError(t, err)
	defer func() { _ = h.Finalize(ctx) }()

	client := h.Env()["P4CLIENT"]
	wsRoot := h.WorkingDir()
	c := NewClient()

	capture := func(name, filespec string) string {
		t.Helper()
		var lines []string
		stderr, err := c.SyncPreempt(ctx, wsRoot, client, filespec, func(l string) { lines = append(lines, l) })
		require.NoError(t, err, "p4 sync -k must not fail for %s", filespec)
		body := "$ p4 -c <client> sync -k " + filespec + "\n" +
			"--- stdout ---\n" + strings.Join(lines, "\n") + "\n" +
			"--- stderr ---\n" + stderr + "\n"
		require.NoError(t, os.WriteFile(filepath.Join("testdata", "p4-sync-k", name), []byte(body), 0o644))
		t.Logf("captured %s:\n%s", name, body)
		return body
	}

	marked := capture("marked.txt", "//"+client+"/heavy/...#head")     // fresh client
	uptodate := capture("uptodate.txt", "//"+client+"/heavy/...#head") // already at that have-rev
	nosuch := capture("nosuchfile.txt", "//"+client+"/does-not-exist/...#head")

	// The two behaviours the parser depends on, asserted rather than described.
	// TEXT, not exit status: p4 exits ZERO for all three
	// (bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero), which is
	// the whole reason SyncPreempt returns stderr at all.
	require.Contains(t, strings.ToLower(nosuch), "no such file",
		"a filespec that matched nothing must be distinguishable, and only its text distinguishes it")
	require.NotContains(t, strings.ToLower(uptodate), "no such file",
		"an already-excluded subtree on a warm workspace must not read as a typo; "+
			"zero per-file lines is success here, not emptiness")
	require.NotContains(t, strings.ToLower(marked), "no such file")
}
```

- [ ] **Step 2: create the directory and run it**

```bash
mkdir -p internal/agent/source/perforce/testdata/p4-sync-k
go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncKReports -v -timeout 1800s
```

Expected: **compile failure, `c.SyncPreempt` undefined.** That is correct - the capture is written before the parser on purpose. Note it and **resume at Step 3 after Task 9.**

- [ ] **Step 3 (after Task 9): run and READ the three artifacts**

```bash
go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncKReports -v -timeout 1800s
cat internal/agent/source/perforce/testdata/p4-sync-k/*.txt
```

Expected: PASS, three files on disk. If your p4 server words the miss differently, the literal in `preemptReportedNoSuchFiles` (Task 3) is what moves - not these assertions, which are the behavioural contract.

- [ ] **Step 4: assert the artifacts are clean text, then commit**

```bash
git ls-files --eol internal/agent/source/perforce/testdata/p4-sync-k/
python -c "import pathlib,sys;[pathlib.Path(p).read_text(encoding='utf-8') for p in sys.argv[1:]]" internal/agent/source/perforce/testdata/p4-sync-k/*.txt
git add internal/agent/source/perforce/testdata/p4-sync-k internal/agent/source/perforce/perforce_exclusion_integration_test.go
git commit -m "test(p4d): capture p4 sync -k output for marked, up-to-date and no-such-file"
```

Expected: every path reads `i/lf`; the python line exits 0.

---

## Task 3: the no-such-files predicate, in the lane that actually runs

**Lane:** default.
**Files:** Create `internal/agent/source/perforce/preempt.go` and `preempt_test.go`.

A guard over a pure function does not need the integration tag - CLAUDE.md's first question, and the cheapest available outcome.

- [ ] **Step 1: write the failing test**

```go
package perforce

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The inputs are the ARTIFACTS captured from real p4 in the p4d lane, not
// literals typed from documentation. The two readings this predicate keeps apart
// are the whole of the refusal: a filespec that matched nothing (refuse - the
// exclusion is inert and the volume fills) versus a warm workspace already at
// the target have-revision (accept - reading that as failure would refuse every
// prepare after the first).
func TestPreemptReportedNoSuchFiles_ReadsTheCapturedArtifacts(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", "p4-sync-k", name))
		require.NoError(t, err, "capture the artifact in the p4d lane first (Task 2)")
		return string(b)
	}
	require.True(t, preemptReportedNoSuchFiles(read("nosuchfile.txt")))
	require.False(t, preemptReportedNoSuchFiles(read("uptodate.txt")))
	require.False(t, preemptReportedNoSuchFiles(read("marked.txt")))
}

// The decoy goes BEFORE the phrase, so a mutant reading only the first line is
// visible; a decoy placed last is read by neither the code nor the mutant.
func TestPreemptReportedNoSuchFiles_EdgeCases(t *testing.T) {
	require.False(t, preemptReportedNoSuchFiles(""))
	require.False(t, preemptReportedNoSuchFiles("//c/heavy/x.ma#1 - added as /w/heavy/x.ma\n"))
	require.True(t, preemptReportedNoSuchFiles(
		"//c/heavy/x.ma#1 - added as /w/heavy/x.ma\n//c/typo/... - no such file(s).\n"))
	require.True(t, preemptReportedNoSuchFiles("//c/typo/... - No such file(s).\n"),
		"the reading must not depend on p4's capitalisation")
}
```

- [ ] **Step 2: run and fail**

```bash
go test ./internal/agent/source/perforce/ -run TestPreemptReportedNoSuchFiles -v -count=1
```

Expected: FAIL - `undefined: preemptReportedNoSuchFiles`.

- [ ] **Step 3: write the predicate**

```go
package perforce

import "strings"

// preemptReportedNoSuchFiles reports whether p4 told us the preempt's filespec
// matched nothing. p4 exits ZERO in that case and writes the message to stderr
// only, so this predicate is the whole of what stands between a typo'd exclusion
// and a full-size transfer of the subtree the operator asked to leave out. Two
// live routes reach it: a mistyped depot path, and an exclusion under a stream
// whose view renames a subtree, where toClientPath emits a client path that
// resolves to nothing.
//
// IT KEYS ON THE TEXT, NEVER ON AN EMPTY STDOUT. A warm workspace whose
// have-list already covers the exclusion at the target revision produces zero
// per-file lines and is a SUCCESS. testdata/p4-sync-k holds the captured
// artifacts both readings are written against.
func preemptReportedNoSuchFiles(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "no such file")
}
```

- [ ] **Step 4: run and pass.** Same command. Expected: PASS.

- [ ] **Step 5: kill the obvious mutant.** Copy the file aside, change `strings.Contains` to `strings.HasPrefix`, re-run: the two-line `EdgeCases` case and `nosuchfile.txt` go RED. Restore from the copy.

- [ ] **Step 6: commit**

```bash
git add internal/agent/source/perforce/preempt.go internal/agent/source/perforce/preempt_test.go
git commit -m "feat(perforce): read p4's zero-exit no-such-files report from a preempt"
```

---

## Task 4: `jobspec` gains `exclude`, and the five refusals

**Lane:** default.
**Files:** Modify `internal/jobspec/jobspec.go` (`SyncEntry`, a new `maxSyncExclusions`, `validateSourceSpec`, a new exported `DepotPathCovers`) and `internal/api/job_spec_source_test.go`.

All ingestion goes through `jobspec.Validate`, so these rules bind REST, CLI, MCP, schedrunner and the SPA at once.

- [ ] **Step 1: write the failing table cases**

Add to the `cases` slice in `job_spec_source_test.go`, after the `client_template with an interior hyphen` entry:

```go
		// --- exclusions ---
		{"exclusion happy path", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Exclude: true},
			}
		}, ""},
		// The preempt's revision comes from the covering include and from nothing
		// else; a second revision here would name a different one, and a preempt
		// at the wrong revision fetches the excluded subtree BACKWARDS rather than
		// merely failing to exclude.
		{"exclusion carrying a revision", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Rev: "#head", Exclude: true},
			}
		}, "an excluded path carries no revision"},
		{"uncovered exclusion", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/Code/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Exclude: true},
			}
		}, "covered by exactly one included path, found 0"},
		{"exclusion covered twice at different revs", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "@100"},
				{Path: "//streams/X/main/Content/...", Rev: "@200"},
				{Path: "//streams/X/main/Content/Movies/...", Exclude: true},
			}
		}, "covered by exactly one included path, found 2"},
		// Two IDENTICAL literal revs, and still ambiguous: #head resolves per path
		// on the agent, so these two can land on different changelists and the
		// validator cannot see it.
		{"exclusion covered twice at the same literal rev", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Exclude: true},
			}
		}, "covered by exactly one included path, found 2"},
		{"exclusion equal to its include", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/Content/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/...", Exclude: true},
			}
		}, "leaves included path"},
		// The exclusion is BROADER than the second include, so that include has
		// nothing left. It is not covered BY that include, which is why the
		// swallow check runs against every include rather than the covering one.
		{"exclusion swallowing a narrower include", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/...", Exclude: true},
			}
		}, "leaves included path"},
		{"sixteen exclusions is allowed", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = manySyncExclusions(16)
		}, ""},
		{"seventeen exclusions", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = manySyncExclusions(17)
		}, "at most 16 excluded sync paths are allowed, got 17"},
		// An exclusion is still a path under the stream. No new code enforces this
		// - the existing per-entry containment check already runs for every entry -
		// so the case pins that the exclusion branch did not skip past it.
		{"exclusion outside the stream", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//other/depot/...", Exclude: true},
			}
		}, "must be under stream"},
		// A sibling that shares a textual prefix but is not under the include.
		// TestToClientPath's sharesATextualPrefixButIsNotUnder row is the same
		// hazard one layer down; this is the discriminator for DepotPathCovers.
		{"exclusion under a sibling sharing a textual prefix", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/Content/...", Rev: "#head"},
				{Path: "//streams/X/main/ContentExtra/Movies/...", Exclude: true},
			}
		}, "covered by exactly one included path, found 0"},
```

And at the bottom of that file, plus `"fmt"` in its imports:

```go
// manySyncExclusions returns one include covering n distinct exclusions, so the
// only rule an over-count case can trip is the count itself: each exclusion is
// covered exactly once and swallows nothing.
func manySyncExclusions(n int) []SyncEntry {
	out := []SyncEntry{{Path: "//streams/X/main/...", Rev: "#head"}}
	for i := 0; i < n; i++ {
		out = append(out, SyncEntry{Path: fmt.Sprintf("//streams/X/main/d%02d/...", i), Exclude: true})
	}
	return out
}
```

- [ ] **Step 2: run and fail**

```bash
go test ./internal/api/ -run TestValidateJobSpec_Source_Perforce -v -count=1
```

Expected: FAIL to compile - `unknown field Exclude in struct literal of type SyncEntry`.

- [ ] **Step 3: add the field**

Replace the `SyncEntry` declaration in `internal/jobspec/jobspec.go`:

```go
// SyncEntry is a single depot path + revision to sync, or - with Exclude - a
// path to leave out of the sync.
//
// AN EXCLUDED ENTRY CARRIES NO REVISION. The revision its have-list preempt runs
// at comes from the include that covers it; a revision here would name a second
// one, and a preempt at the wrong revision does not merely fail to exclude - p4
// syncs a file whose have-revision differs from the target in EITHER direction,
// so a preempt at a newer revision fetches the whole excluded subtree.
// validateSourceSpec refuses it.
type SyncEntry struct {
	Path    string `json:"path"`
	Rev     string `json:"rev"`
	Exclude bool   `json:"exclude,omitempty"`
}
```

- [ ] **Step 4: add the bound**

Immediately after the `maxCommandsPerJob` block:

```go
// maxSyncExclusions bounds how many entries of one source spec may set
// `exclude`. Each one is an additional p4 subprocess inside the task's own
// prepare phase and an additional operator-facing log line, on the same
// per-entry axis
// docs/backlog/bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded
// already flags for `unshelves`. A realistic exclusion list is a handful of
// named heavy subtrees; 16 is several times that.
//
// IT ALSO BOUNDS A QUADRATIC. The coverage and swallow rules below compare every
// exclusion against every include, and the include side is bounded only by
// maxBodyBytes. The count is therefore checked BEFORE that loop runs, so an
// over-count spec is refused after one linear pass.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.job_spec rows, and it applies
// identically to every bound in this file.
const maxSyncExclusions = 16
```

- [ ] **Step 5: rewrite the sync loop in `validateSourceSpec`**

Replace the whole `for i, e := range s.Sync { ... }` block (the one holding the `must start with //`, `must be under stream` and `invalid rev` checks) with:

```go
	excluded := 0
	for i, e := range s.Sync {
		if !strings.HasPrefix(e.Path, "//") {
			return fmt.Errorf("sync[%d].path must start with //", i)
		}
		if e.Path != s.Stream &&
			e.Path != s.Stream+"/..." &&
			!strings.HasPrefix(e.Path, s.Stream+"/") {
			return fmt.Errorf("sync[%d].path must be under stream %s", i, s.Stream)
		}
		// The rev check is CARVED OUT for an exclusion, not relaxed: an empty rev
		// matches none of the four patterns and is still refused for an include.
		if e.Exclude {
			excluded++
			if e.Rev != "" {
				return fmt.Errorf("sync[%d].rev: an excluded path carries no revision; "+
					"it is preempted at the revision of the include that covers it", i)
			}
			continue
		}
		if !(revHeadRe.MatchString(e.Rev) || revCLRe.MatchString(e.Rev) ||
			revLabelRe.MatchString(e.Rev) || revNumRe.MatchString(e.Rev)) {
			return fmt.Errorf("sync[%d].rev: invalid rev %q", i, e.Rev)
		}
	}
	if excluded > maxSyncExclusions {
		return fmt.Errorf("at most %d excluded sync paths are allowed, got %d",
			maxSyncExclusions, excluded)
	}
	for i, e := range s.Sync {
		if !e.Exclude {
			continue
		}
		covering := 0
		for _, inc := range s.Sync {
			if inc.Exclude {
				continue
			}
			// The swallow check runs against EVERY include, not the covering one.
			// An exclusion broader than an include is not covered BY that include -
			// coverage is directional - so checking only the coverer would never
			// fire for the case it exists to catch.
			if DepotPathCovers(e.Path, inc.Path) {
				return fmt.Errorf("sync[%d]: excluded path %s leaves included path %s "+
					"with nothing to sync; remove the include instead", i, e.Path, inc.Path)
			}
			if DepotPathCovers(inc.Path, e.Path) {
				covering++
			}
		}
		// EXACTLY ONE, not "at one revision". Two covering includes spelled #head
		// are literally equal and still ambiguous: the agent resolves #head per
		// path, and this function cannot see which changelists they land on.
		if covering != 1 {
			return fmt.Errorf("sync[%d]: excluded path %s must be covered by exactly one "+
				"included path, found %d", i, e.Path, covering)
		}
	}
```

- [ ] **Step 6: add the shared predicate**

At the bottom of `internal/jobspec/jobspec.go`:

```go
// DepotPathCovers reports whether outer's subtree contains inner. A trailing
// "/..." is p4's recursive wildcard and names the same subtree as the bare path,
// so it is trimmed from both sides before comparing.
//
// EXPORTED FOR ONE CALLER OUTSIDE THIS PACKAGE: the Perforce provider picks an
// exclusion's covering include with the identical predicate, and a second
// implementation there could disagree with this one about which include supplies
// the preempt revision. This package imports only the standard library, so an
// agent package importing it introduces no cycle.
//
// It is not perforce.PathPrefixOverlap and must not be replaced by it: that one
// is symmetric ("could these two touch"), this one is directional ("is inner
// inside outer"), and the direction is the whole of the coverage and swallow
// rules in validateSourceSpec.
func DepotPathCovers(outer, inner string) bool {
	o := strings.TrimSuffix(outer, "/...")
	n := strings.TrimSuffix(inner, "/...")
	return n == o || strings.HasPrefix(n, o+"/")
}
```

- [ ] **Step 7: run and pass**

```bash
go test ./internal/api/ ./internal/jobspec/ -count=1 -v -run TestValidateJobSpec
go test ./... -count=1 2>&1 | grep -v "^ok" | head -20
```

Expected: every new case passes, every pre-existing case still passes, whole tree green.

- [ ] **Step 8: mutation battery**

One at a time, `cp` the file aside first, re-run `go test ./internal/api/ -run TestValidateJobSpec_Source_Perforce -count=1`, restore from the copy.

| Mutation | Must redden |
|---|---|
| drop the `e.Rev != ""` refusal in the `e.Exclude` branch | `exclusion carrying a revision` |
| `covering != 1` -> `covering < 1` | both `covered twice` cases |
| swap the swallow arm's arguments to `DepotPathCovers(inc.Path, e.Path)` | `exclusion swallowing a narrower include` |
| `excluded > maxSyncExclusions` -> `excluded > maxSyncExclusions+1` | `seventeen exclusions` |
| `DepotPathCovers` body -> `strings.HasPrefix(n, o)` | `exclusion under a sibling sharing a textual prefix` |

A mutation that reddens nothing means the case set is missing a discriminator, not that the code is redundant. Do not proceed past a survivor.

- [ ] **Step 9: commit**

```bash
git add internal/jobspec/jobspec.go internal/api/job_spec_source_test.go
git commit -m "feat(jobspec): accept an excluded sync path, with the five refusals that make it meaningful"
```

---

## Task 5: the proto field, and the hand-written copy that must carry it

**Lane:** default.
**Files:** Modify `proto/relayv1/relay.proto`, regenerate `internal/proto/relayv1/relay.pb.go`, modify `internal/scheduler/source_proto.go`, create `internal/scheduler/source_proto_test.go`.

`sourceSpecToProto` is a hand-written field-by-field copy. When the SOURCE type gains a field it keeps compiling and silently drops it - and dropping `exclude` is not cosmetic: the agent would sync the excluded subtree into a workspace whose identity says it is excluded.

- [ ] **Step 1: write the failing tests**

```go
package scheduler

import (
	"reflect"
	"testing"

	"relay/internal/api"
	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

func TestSourceSpecToProto_CarriesTheExcludeFlag(t *testing.T) {
	s := &api.SourceSpec{
		Type:   "perforce",
		Stream: "//s/x",
		Sync: []api.SyncEntry{
			{Path: "//s/x/...", Rev: "#head"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	got := sourceSpecToProto(s).GetPerforce().GetSync()
	require.Len(t, got, 2)
	require.False(t, got[0].GetExclude(), "an include must not arrive excluded")
	require.True(t, got[1].GetExclude(),
		"dropping exclude here syncs the subtree into a workspace whose identity says it is excluded")
}

// sourceSpecToProto copies api.SyncEntry into relayv1.SyncEntry field by field,
// and the compiler is blind to a field added on either side. Comparing arities
// makes that a RED here rather than a silent drop on the wire. The proto struct
// carries unexported protoimpl fields, so only tagged exported fields count.
func TestSourceSpecToProto_SyncEntryArityMatches(t *testing.T) {
	protoFields := 0
	pt := reflect.TypeOf(relayv1.SyncEntry{})
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		if f.IsExported() && f.Tag.Get("protobuf") != "" {
			protoFields++
		}
	}
	require.Equal(t, reflect.TypeOf(api.SyncEntry{}).NumField(), protoFields,
		"api.SyncEntry and relayv1.SyncEntry have drifted; sourceSpecToProto copies them by hand")
}
```

- [ ] **Step 2: run and fail**

```bash
go test ./internal/scheduler/ -run TestSourceSpecToProto -v -count=1
```

Expected: FAIL to compile - `got[1].GetExclude undefined`.

- [ ] **Step 3: add the proto field**

```proto
message SyncEntry {
  string path = 1;
  string rev  = 2;
  // An excluded path is have-marked (p4 sync -k) before the real sync, so p4
  // never transfers it. It carries no rev: the agent preempts at the resolved
  // revision of the include that covers it, and jobspec.validateSourceSpec
  // refuses a rev here.
  //
  // A plain bool, not optional: an agent that drops the field syncs everything,
  // which is the version skew RegisterRequest.supports_sync_exclusions closes on
  // the dispatch side rather than here.
  bool   exclude = 3;
}
```

- [ ] **Step 4: regenerate, and check what the regeneration did**

```bash
make generate
git diff --ignore-all-space --name-only   # files with REAL changes
git status --porcelain                     # every file the generator touched
```

`git checkout -- <path>` every path in the second list that is not in the first. Then:

```bash
grep -n "Exclude" internal/proto/relayv1/relay.pb.go | head
git ls-files --eol proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go
```

Expected: an `Exclude bool` field and a `GetExclude()` method are present; both paths read `i/lf`.

- [ ] **Step 5: carry the field in the copy**

In `internal/scheduler/source_proto.go`:

```go
	for _, e := range s.Sync {
		p.Sync = append(p.Sync, &relayv1.SyncEntry{Path: e.Path, Rev: e.Rev, Exclude: e.Exclude})
	}
```

- [ ] **Step 6: run and pass**

```bash
go test ./internal/scheduler/ -run TestSourceSpecToProto -v -count=1
go build ./... && go test ./... -count=1 2>&1 | grep -v "^ok" | head -20
```

- [ ] **Step 7: prove the arity guard is not decorative.** Add `Junk string` to `api.SyncEntry`, re-run `TestSourceSpecToProto_SyncEntryArityMatches`, confirm FAIL, remove it.

- [ ] **Step 8: commit**

```bash
git add proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go internal/scheduler/source_proto.go internal/scheduler/source_proto_test.go
git commit -m "feat(proto): SyncEntry.exclude, and an arity guard on the hand-written copy"
```

---

## Task 6: the Python SDK accepts `exclude` at all

**Lane:** the Python lane - `cd python && python -m pytest tests/unit -q`. No server needed.
**Files:** Modify `python/src/relay/models.py` (`Sync`) and `python/tests/unit/test_models.py`.

`Sync` carries `ConfigDict(extra="forbid")`, so until this ships a Python caller **cannot express the feature at all** - the SDK rejects the key before the request is built.

- [ ] **Step 1: write the failing tests**

Add after `test_sync_rejects_invalid_revs`, and add `Task` to the file's imports if absent:

```python
def test_sync_accepts_an_exclusion_with_no_rev() -> None:
    s = Sync(path="//depot/main/heavy/...", exclude=True)
    assert s.exclude is True
    assert s.rev == ""


def test_sync_exclusion_rejects_a_revision() -> None:
    # The revision an exclusion is preempted at comes from the include that
    # covers it. The server refuses a rev here for the same reason.
    with pytest.raises(PydanticValidationError):
        Sync(path="//depot/main/heavy/...", rev="#head", exclude=True)


def test_sync_include_still_requires_a_recognized_rev() -> None:
    # Making rev optional for exclusions must not make it optional for includes.
    with pytest.raises(PydanticValidationError):
        Sync(path="//depot/main/...")


def test_sync_defaults_to_not_excluded() -> None:
    assert Sync(path="//depot/main/...", rev="#head").exclude is False


def test_an_excluded_sync_serializes_a_payload_the_server_accepts() -> None:
    # to_spec_dict uses model_dump(exclude_none=True), so rev is emitted as the
    # empty string rather than omitted - which is exactly what the Go validator
    # accepts: it refuses a NON-EMPTY rev on an excluded entry, and "" passes.
    # Emitting "#head" here would produce a spec the server refuses with no
    # SDK-side signal at all.
    t = Task(
        name="t",
        commands=[["true"]],
        source=Source(
            stream="//depot/main",
            sync=[
                Sync(path="//depot/main/...", rev="#head"),
                Sync(path="//depot/main/heavy/...", exclude=True),
            ],
        ),
    )
    assert t.to_spec_dict()["source"]["sync"] == [
        {"path": "//depot/main/...", "rev": "#head", "exclude": False},
        {"path": "//depot/main/heavy/...", "rev": "", "exclude": True},
    ]
```

- [ ] **Step 2: run and fail**

```bash
cd D:/dev/relay/.claude/worktrees/now-e-exclusion-paths/python
python -m pytest tests/unit/test_models.py -q -k "exclu"
```

Expected: FAIL - `Extra inputs are not permitted [type=extra_forbidden]`.

- [ ] **Step 3: change the model**

Replace the `Sync` class body in `python/src/relay/models.py`, deleting the old `_rev_recognized` field validator:

```python
class Sync(BaseModel):
    """A single depot path + revision to sync, or a path to leave out."""

    model_config = ConfigDict(extra="forbid")

    path: str
    rev: str = ""
    exclude: bool = False

    @field_validator("path")
    @classmethod
    def _path_starts_with_slashes(cls, v: str) -> str:
        if not v.startswith("//"):
            raise ValueError("path must start with //")
        return v

    @model_validator(mode="after")
    def _rev_matches_exclude(self) -> Sync:
        # A model validator, not a field validator on `rev`: the rule depends on
        # `exclude`, and a field validator cannot see a field declared after it.
        if self.exclude:
            if self.rev:
                raise ValueError(
                    "an excluded path carries no revision; it is preempted at the "
                    "revision of the include that covers it"
                )
            return self
        if not any(p.match(self.rev) for p in _REV_PATTERNS):
            raise ValueError(
                f"invalid rev {self.rev!r} (expected #head, #N, @CL, or @label)"
            )
        return self
```

`model_validator` is already imported (`Source._sync_paths_under_stream` uses it).

- [ ] **Step 4: run and pass**

```bash
python -m pytest tests/unit -q
```

Expected: green, including the pre-existing `test_sync_rejects_invalid_revs` and `test_sync_accepts_valid_revs` - the rule moved, its outcomes did not.

- [ ] **Step 5: commit**

```bash
cd D:/dev/relay/.claude/worktrees/now-e-exclusion-paths
git add python/src/relay/models.py python/tests/unit/test_models.py
git commit -m "feat(sdk): Sync.exclude, and a rev rule that is conditional on it"
```

---

## Task 7: `SourceKey` - the workspace identity

**Lane:** default.
**Files:** Create `internal/agent/source/perforce/sourcekey.go` and `sourcekey_test.go`.

- [ ] **Step 1: write the failing tests**

```go
package perforce

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

func pfWith(entries ...*relayv1.SyncEntry) *relayv1.PerforceSource {
	return &relayv1.PerforceSource{Stream: "//s/x", Sync: entries}
}

// A task with NO exclusions keeps today's key BYTE FOR BYTE. Every existing
// registry row, every worker_workspaces row and every allocated short_id stays
// valid only because of this, which is why the assertion is an equality against
// the literal stream rather than a round trip through anything.
func TestSourceKey_NoExclusionsIsTheBareStream(t *testing.T) {
	require.Equal(t, "//s/x", SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"},
		&relayv1.SyncEntry{Path: "//s/x/a/...", Rev: "@100"},
	)))
	require.Equal(t, "", SourceKey(nil))
}

func TestSourceKey_AnExclusionMakesADistinctCompositeKey(t *testing.T) {
	bare := SourceKey(pfWith(&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"}))
	comp := SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"},
		&relayv1.SyncEntry{Path: "//s/x/heavy/...", Exclude: true},
	))
	require.NotEqual(t, bare, comp)
	// The composite can never collide with a bare stream, because
	// validateSourceSpec requires a stream to start with // and no legal stream
	// starts with x1|. A collision would put a task into another task's
	// workspace, which is the poisoning hazard by a second route.
	require.True(t, strings.HasPrefix(comp, "x1|"))
	require.True(t, strings.HasSuffix(comp, "|//s/x"))
}

func TestSourceKey_OrderAndDuplicatesCanonicalise(t *testing.T) {
	a := SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"},
		&relayv1.SyncEntry{Path: "//s/x/b/...", Exclude: true},
		&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true},
	))
	b := SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"},
		&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true},
		&relayv1.SyncEntry{Path: "//s/x/b/...", Exclude: true},
		&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true},
	))
	require.Equal(t, a, b, "order and duplicates must canonicalise to one workspace")
}

func TestSourceKey_DifferentExclusionSetsAreDifferentWorkspaces(t *testing.T) {
	a := SourceKey(pfWith(&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true}))
	b := SourceKey(pfWith(&relayv1.SyncEntry{Path: "//s/x/b/...", Exclude: true}))
	require.NotEqual(t, a, b)
}

// worker_workspaces.source_key is TEXT inside PRIMARY KEY (worker_id,
// source_type, source_key) and inside worker_workspaces_lookup_idx, and NOTHING
// on the registration-time bulk ingest bounds its length - an over-long value
// fails the whole inventory transaction rather than one row
// (idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key).
// This design stays clear of that by construction, and "by construction" is a
// property of the function below, not of the schema: a canonicalisation that
// inlined the paths would reintroduce it silently. Sixteen maximum-length depot
// paths would exceed Postgres's btree index-row limit; twenty bytes cannot.
func TestSourceKey_IsBoundedAtTwentyBytesOverTheStream(t *testing.T) {
	long := make([]*relayv1.SyncEntry, 0, 16)
	for i := 0; i < 16; i++ {
		long = append(long, &relayv1.SyncEntry{
			Path:    "//s/x/" + strings.Repeat("d", 200) + string(rune('a'+i)) + "/...",
			Exclude: true,
		})
	}
	require.Len(t, SourceKey(pfWith(long...)), len("//s/x")+20)
}
```

- [ ] **Step 2: run and fail**

```bash
go test ./internal/agent/source/perforce/ -run TestSourceKey -v -count=1
```

Expected: FAIL - `undefined: SourceKey`.

- [ ] **Step 3: write it**

Create `internal/agent/source/perforce/sourcekey.go`:

```go
package perforce

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	relayv1 "relay/internal/proto/relayv1"
)

// SourceKey returns the workspace identity for a Perforce source spec: the
// stream when nothing is excluded, and "x1|<16 hex>|<stream>" otherwise.
//
// THE EXCLUSION SET IS PART OF THE IDENTITY, and this is a precondition of the
// mechanism rather than a choice within it: the have-list preempt writes the
// CLIENT's have-list, which Prepare shares across every task on the stream, so a
// task with a different exclusion set must reach a different workspace or it
// observes files it asked for and did not get.
//
// A SPEC WITH NO EXCLUSIONS PRODUCES TODAY'S KEY BYTE FOR BYTE, which is why
// every existing registry row, worker_workspaces row and allocated short_id
// survives with no migration.
//
// The composite cannot collide with a bare stream: validateSourceSpec requires a
// stream to start with "//" and no legal stream starts with "x1|". The x1 tag is
// a version - a future change to the canonicalisation moves to x2 rather than
// silently reusing a key for a different meaning. The 16-hex truncation is the
// same shape BaselineHash already uses, so an operator sees one form twice.
//
// KEEP IT SHORT. TestSourceKey_IsBoundedAtTwentyBytesOverTheStream is the guard
// and carries the reason.
func SourceKey(p *relayv1.PerforceSource) string {
	if p == nil {
		return ""
	}
	ex := make([]string, 0, len(p.Sync))
	for _, e := range p.Sync {
		if e.GetExclude() {
			ex = append(ex, e.GetPath())
		}
	}
	if len(ex) == 0 {
		return p.GetStream()
	}
	sort.Strings(ex)
	h := sha256.New()
	prev := ""
	for i, path := range ex {
		if i > 0 && path == prev {
			continue
		}
		prev = path
		h.Write([]byte(path))
		h.Write([]byte{0})
	}
	return "x1|" + hex.EncodeToString(h.Sum(nil))[:16] + "|" + p.GetStream()
}
```

- [ ] **Step 4: run and pass.** Same command. Expected: PASS.

- [ ] **Step 5: mutation battery**

| Mutation | Must redden |
|---|---|
| drop the `if len(ex) == 0 { return p.GetStream() }` early return | `NoExclusionsIsTheBareStream` |
| drop `sort.Strings(ex)` | `OrderAndDuplicatesCanonicalise` |
| drop the `path == prev` dedupe | `OrderAndDuplicatesCanonicalise` |
| drop `h.Write([]byte{0})` | neither of the above - **add** a case with exclusions `{"//s/x/ab/...", "//s/x/c/..."}` vs `{"//s/x/a/...", "//s/x/bc/..."}` and assert `NotEqual`, which is what a missing separator collapses |
| append the paths to the key instead of the digest | `IsBoundedAtTwentyBytesOverTheStream` |

- [ ] **Step 6: commit**

```bash
git add internal/agent/source/perforce/sourcekey.go internal/agent/source/perforce/sourcekey_test.go
git commit -m "feat(perforce): SourceKey folds the exclusion set into the workspace identity"
```

---

## Task 8: `BaselineHash` covers the exclusion set

**Lane:** default.
**Files:** Modify `internal/agent/source/perforce/baseline.go` and `baseline_test.go`.

`BaselineHash` reads `entry{path, rev}` today, so an exclusion field hashes identically to its absence and two differently-excluded tasks look like the same baseline - one would skip the other's sync.

- [ ] **Step 1: write the failing tests**

Append to `baseline_test.go`:

```go
// THE DISCRIMINATING INPUT: two specs whose entries agree on path AND rev and
// differ ONLY in the exclusion flag. With the rev held equal the flag is the
// only bit that moves, so a BaselineHash that does not read it returns one value
// for both. Any pair that also varied the rev would pass against the broken
// build, because the rev is already hashed.
//
// The pair is synthetic on purpose and could not come from two valid specs:
// validateSourceSpec refuses a rev on an excluded entry, so a real exclusion
// carries rev "". The property under test is the hash function's own contract,
// and this is the only input that isolates it.
func TestBaselineHash_TheExcludeFlagIsHashed(t *testing.T) {
	mk := func(exclude bool) *relayv1.PerforceSource {
		return &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync: []*relayv1.SyncEntry{
				{Path: "//s/x/...", Rev: "@100"},
				{Path: "//s/x/heavy/...", Rev: "", Exclude: exclude},
			},
		}
	}
	require.NotEqual(t, BaselineHash(mk(false), nil), BaselineHash(mk(true), nil))
}

// Which ENTRY carries the flag has to matter, not merely how many do. A marker
// written once for the whole entry block, or a sort that ignores the flag,
// passes the test above and fails this one.
func TestBaselineHash_WhichEntryIsExcludedChangesTheHash(t *testing.T) {
	a := &relayv1.PerforceSource{Stream: "//s/x", Sync: []*relayv1.SyncEntry{
		{Path: "//s/x/a/...", Rev: "@100", Exclude: true},
		{Path: "//s/x/b/...", Rev: "@100"},
	}}
	b := &relayv1.PerforceSource{Stream: "//s/x", Sync: []*relayv1.SyncEntry{
		{Path: "//s/x/a/...", Rev: "@100"},
		{Path: "//s/x/b/...", Rev: "@100", Exclude: true},
	}}
	require.NotEqual(t, BaselineHash(a, nil), BaselineHash(b, nil))
}

// Two entries sharing a path and a rev sort unstably against each other today.
// Adding the flag to the sort key is what makes the digest independent of the
// order the two arrived in.
func TestBaselineHash_StableWhenAPathAppearsBothIncludedAndExcluded(t *testing.T) {
	a := &relayv1.PerforceSource{Stream: "//s/x", Sync: []*relayv1.SyncEntry{
		{Path: "//s/x/a/...", Rev: "@100"},
		{Path: "//s/x/a/...", Rev: "@100", Exclude: true},
	}}
	b := &relayv1.PerforceSource{Stream: "//s/x", Sync: []*relayv1.SyncEntry{
		{Path: "//s/x/a/...", Rev: "@100", Exclude: true},
		{Path: "//s/x/a/...", Rev: "@100"},
	}}
	require.Equal(t, BaselineHash(a, nil), BaselineHash(b, nil))
}
```

- [ ] **Step 2: run and fail**

```bash
go test ./internal/agent/source/perforce/ -run TestBaselineHash -v -count=1
```

Expected: `TheExcludeFlagIsHashed` and `WhichEntryIsExcludedChangesTheHash` FAIL (equal digests); `NoExclusionsIsUnchanged` and the two pre-existing tests PASS.

- [ ] **Step 3: change the encoding**

In `internal/agent/source/perforce/baseline.go`, inside `BaselineHash`:

```go
	type entry struct {
		path, rev string
		exclude   bool
	}
	es := make([]entry, 0, len(p.Sync))
	for _, e := range p.Sync {
		rev := e.Rev
		if e.Rev == "#head" && resolvedHead != nil {
			if r, ok := resolvedHead[e.Path]; ok {
				rev = r
			}
		}
		es = append(es, entry{e.Path, rev, e.GetExclude()})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].path != es[j].path {
			return es[i].path < es[j].path
		}
		if es[i].rev != es[j].rev {
			return es[i].rev < es[j].rev
		}
		// The flag joins the sort key because two entries sharing a path and a
		// rev otherwise sort unstably against each other.
		return !es[i].exclude && es[j].exclude
	})
```

and in the write loop:

```go
	for _, e := range es {
		h.Write([]byte(e.path))
		h.Write([]byte{0})
		h.Write([]byte(e.rev))
		h.Write([]byte{0})
		// WRITTEN ONLY FOR AN EXCLUDED ENTRY, so a spec with no exclusions hashes
		// to the byte sequence it always has. That encoding is a cross-process
		// contract (scheduler.BaselineHashFromAPISpec computes it server-side) and
		// changing it re-syncs every warm workspace in the fleet once;
		// TestBaselineHash_NoExclusionsIsUnchanged is the guard.
		//
		// The marker cannot be mistaken for the start of the next entry's path:
		// every path reaching this function has passed validateSourceSpec's "//"
		// prefix rule, so no path can begin with this byte.
		if e.exclude {
			h.Write([]byte{2})
		}
	}
```

- [ ] **Step 4: run and pass**

```bash
go test ./internal/agent/source/perforce/ -run TestBaselineHash -v -count=1
```

Expected: all five tests PASS, **including the golden**. If the golden went RED the marker is being written unconditionally - that is the mutation this task must not ship.

- [ ] **Step 5: mutation battery**

| Mutation | Must redden |
|---|---|
| `if e.exclude` -> unconditional `h.Write([]byte{2})` | `NoExclusionsIsUnchanged` (the golden) |
| drop the `h.Write([]byte{2})` entirely | `TheExcludeFlagIsHashed` |
| drop the flag from the sort key | `StableWhenAPathAppearsBothIncludedAndExcluded` |
| hash `len(exclusions)` once after the loop instead of per entry | `WhichEntryIsExcludedChangesTheHash` |

- [ ] **Step 6: commit**

```bash
git add internal/agent/source/perforce/baseline.go internal/agent/source/perforce/baseline_test.go
git commit -m "fix(perforce): BaselineHash covers the exclusion set without moving the no-exclusion encoding"
```

---

## Task 9: `StreamWithStderr` and `Client.SyncPreempt`

**Lane:** default.
**Files:** Modify `internal/agent/source/perforce/client.go`, `fixtures_test.go`, `provider_evict_recheck_test.go`.

`execRunner.Stream` returns `nil` on a zero exit and the stderr buffer dies with the call. p4 exits ZERO when a filespec matches nothing and says so **only** on stderr, so the preempt cannot use `Stream`.

- [ ] **Step 1: write the failing test**

Append to `internal/agent/source/perforce/client_test.go`:

```go
// SyncPreempt is the only p4 call in this package whose CORRECTNESS depends on
// stderr from a ZERO exit, which is what separates it from SyncStream. It must
// also stream, not buffer: the excluded subtree can be millions of lines.
func TestClient_SyncPreempt_ArgvAndStderr(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.setStream("-c cl sync -k //cl/heavy/...@99", "//cl/heavy/a.ma#1 - added\n")
	fr.setStreamStderr("-c cl sync -k //cl/heavy/...@99", "//cl/heavy/... - no such file(s).\n")

	var lines []string
	stderr, err := (&Client{r: fr}).SyncPreempt(
		context.Background(), "/ws", "cl", "//cl/heavy/...@99",
		func(l string) { lines = append(lines, l) })

	require.NoError(t, err, "p4 exits ZERO here; the refusal is the caller's, from the text")
	require.Equal(t, "//cl/heavy/... - no such file(s).\n", stderr,
		"stderr must survive a zero exit or a typo'd exclusion is undetectable")
	require.Equal(t, []string{"//cl/heavy/a.ma#1 - added"}, lines,
		"stdout must reach the callback, not a buffer")
	require.Equal(t, []string{"-c", "cl", "sync", "-k", "//cl/heavy/...@99"}, fr.argHistory()[0])
}
```

- [ ] **Step 2: run and fail**

```bash
go test ./internal/agent/source/perforce/ -run TestClient_SyncPreempt -v -count=1
```

Expected: FAIL to compile - `fr.setStreamStderr` and `SyncPreempt` undefined.

- [ ] **Step 3: extend the `Runner` interface and `execRunner`**

In `client.go`, add to the interface:

```go
	// StreamWithStderr is Stream plus p4's stderr, returned even on a ZERO exit.
	// Stream discards it there, which is right for a call whose only signal is
	// its exit status and wrong for any caller that must tell "matched nothing"
	// (p4 exits zero and says so on stderr) from "already up to date".
	StreamWithStderr(ctx context.Context, cwd string, args []string, onLine func(string)) (string, error)
```

Then **move the existing `Stream` body verbatim** into `StreamWithStderr`, changing only the returns, and make `Stream` delegate:

```go
func (e *execRunner) Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error {
	_, err := e.StreamWithStderr(ctx, cwd, args, onLine)
	return err
}

func (e *execRunner) StreamWithStderr(ctx context.Context, cwd string, args []string, onLine func(string)) (string, error) {
	cmd := exec.CommandContext(ctx, e.binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", newP4CommandError(args, err, "")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		// Structured like every other failure out of this type, because
		// classifyP4Error only classifies what came from a p4 invocation and this
		// is the route a missing binary takes on the sync path.
		return stderr.String(), newP4CommandError(args, err, "")
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		onLine(sc.Text())
	}
	// THE DRAIN IS WHAT MAKES THE CHECK BELOW REACHABLE, and skipping it is a
	// hang rather than a wrong count. StdoutPipe's contract is that Wait must not
	// run until every read from the pipe has completed; a scanner that stopped on
	// an error has not reached EOF, so p4 blocks writing into a full pipe, Wait
	// blocks on p4, and this never returns - while Prepare holds the workspace
	// handle and nothing on this path carries a deadline. cmd.WaitDelay does not
	// cover it: WaitDelay bounds a cancelled context and an exited-but-unclosed
	// child, and here the context is live and the child has not exited.
	scanErr := sc.Err()
	if scanErr != nil {
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()
	// The scan error is reported IN PREFERENCE to the exit status. A scan that
	// ended on an error, not on EOF, means the caller saw only part of p4's
	// output while p4 itself exited zero, and the sync summary's file count is
	// built from those lines - so the truncation is the more specific fact.
	// TestExecRunner_AStdoutScanFailureOutranksANonZeroExitStatus.
	if scanErr != nil {
		return stderr.String(), newP4CommandError(args, scanErr, stderr.String())
	}
	if waitErr != nil {
		return stderr.String(), newP4CommandError(args, waitErr, stderr.String())
	}
	return stderr.String(), nil
}
```

**This is a behaviour-preserving refactor of a subtle function.** The gate is a zero-line diff in the existing `client_test.go` / `client_error_test.go` assertions for `Stream`.

- [ ] **Step 4: add `SyncPreempt`**

```go
// SyncPreempt runs `p4 -c <client> sync -k <spec>` from cwd, marking every file
// under spec as already-have at that revision so the following real sync never
// transfers it.
//
// It STREAMS stdout, for the reason SyncStream does - the excluded subtree can
// be millions of lines - and it RETURNS stderr even on a zero exit, which is
// what separates it from SyncStream: p4 exits ZERO when a filespec matches
// nothing and reports it on stderr only, so a preempt that silently did nothing
// is otherwise indistinguishable from one that worked. The caller reads the text
// with preemptReportedNoSuchFiles.
//
// NOT --parallel: the preempt transfers no file content, so there is nothing to
// parallelise and the flag would only add threads to a metadata-only update.
func (c *Client) SyncPreempt(ctx context.Context, cwd, client, spec string, onLine func(string)) (string, error) {
	return c.r.StreamWithStderr(ctx, cwd, []string{"-c", client, "sync", "-k", spec}, onLine)
}
```

- [ ] **Step 5: teach both test runners the new method**

In `fixtures_test.go`, add `streamStderr map[string]string` to `fakeRunner`, initialise it in `newFakeP4Fixture`, and add:

```go
func (f *fakeRunner) setStreamStderr(key, s string) { f.streamStderr[key] = s }

func (f *fakeRunner) StreamWithStderr(ctx context.Context, cwd string, args []string, onLine func(string)) (string, error) {
	err := f.Stream(ctx, cwd, args, onLine)
	return f.streamStderr[strings.Join(args, " ")], err
}
```

In `provider_evict_recheck_test.go`:

```go
func (g *gatingRunner) StreamWithStderr(ctx context.Context, cwd string, args []string, onLine func(string)) (string, error) {
	return g.inner.StreamWithStderr(ctx, cwd, args, onLine)
}
```

- [ ] **Step 6: run and pass**

```bash
go test ./internal/agent/source/perforce/ -count=1
git diff --stat internal/agent/source/perforce/client_test.go internal/agent/source/perforce/client_error_test.go
```

Expected: package green, and **zero lines changed** in the two existing client test files.

- [ ] **Step 7: prove the refactor kept the subtle part.** Delete the `io.Copy(io.Discard, stdout)` drain and re-run: `TestExecRunner_AStdoutScanFailureOutranksANonZeroExitStatus` must still be the test that notices. Restore from a copy.

- [ ] **Step 8: return to Task 2 Step 3** and capture the artifacts, then commit both.

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/client_test.go internal/agent/source/perforce/fixtures_test.go internal/agent/source/perforce/provider_evict_recheck_test.go
git commit -m "feat(perforce): SyncPreempt, and a runner method that returns stderr on a zero exit"
```

---

## Task 10: `Prepare` - identity, the preempt, and the two log lines

**Lane:** default.
**Files:** Modify `internal/agent/source/perforce/perforce.go` and `preempt.go`; add tests to `perforce_test.go`.

- [ ] **Step 1: write the failing tests**

Append to `internal/agent/source/perforce/perforce_test.go`:

```go
// The exact preempt argv, and that it PRECEDES the sync. The discriminating
// input is an include at #head: a mutant that passes the literal revision
// through emits "#head" where the resolved "@12345" belongs, and a preempt at
// the wrong revision does not merely fail to exclude - p4 syncs a file whose
// have-revision differs in EITHER direction, so it fetches the whole subtree.
func TestProvider_AnExclusionIsPreemptedAtItsCoveringIncludesResolvedRevision(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "#head"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	client := expectedClientName("h", SourceKey(pf))
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -m1 //"+client+"/...#head", "Change 12345 on 2026-09-04 by relay@h '...'\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/heavy/...@12345", "//x/heavy/a.ma#1 - added\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@12345", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	var lines []string
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}},
		func(s string) { lines = append(lines, s) })
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	var preemptAt, syncAt = -1, -1
	for i, c := range fr.argHistory() {
		if len(c) >= 4 && c[2] == "sync" && c[3] == "-k" {
			preemptAt = i
			require.Equal(t, []string{"-c", client, "sync", "-k", "//" + client + "/heavy/...@12345"}, c,
				"the preempt runs at the covering include's RESOLVED revision")
		}
		if len(c) >= 4 && c[2] == "sync" && c[3] == "--parallel=4" {
			syncAt = i
		}
	}
	require.NotEqual(t, -1, preemptAt, "expected a sync -k invocation")
	require.Less(t, preemptAt, syncAt, "the preempt must precede the sync or it excludes nothing")

	// The excluded path must NOT reach the real sync's argv, and must NOT be
	// recorded as synced: Request.SyncPaths feeds Workspace.syncedPaths, and
	// putting an excluded path there asserts content is present that is
	// deliberately absent.
	for _, c := range fr.argHistory() {
		if len(c) >= 4 && c[3] == "--parallel=4" {
			require.NotContains(t, strings.Join(c, " "), "/heavy/")
		}
	}

	// One count line, then one line per exclusion rendering the DEPOT path with
	// %q and LAST, following syncSummary's rule: a forged path cannot spell a
	// convincing line of its own.
	require.Contains(t, lines, "[sync] excluding: 1 path(s)")
	require.Contains(t, lines, `[sync] exclude "//s/x/heavy/..."`)
}

// Every workspace-identity site must read the same key. A site left on pf.Stream
// puts an excluded task into the unexcluded task's workspace, which is the
// poisoning hazard this design exists to close.
func TestProvider_EveryWorkspaceIdentitySiteUsesTheSameKey(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "@100"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	key := SourceKey(pf)
	client := expectedClientName("h", key)
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/heavy/...@100", "//x/heavy/a.ma#1 - added\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@100", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	inv := h.Inventory()
	require.Equal(t, key, inv.SourceKey, "the handle and the registry row carry the composite key")
	require.Equal(t, allocateShortID(key, &Registry{}), inv.ShortID,
		"the short id is derived from the key, not from the bare stream")

	reg, err := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	e, ok := reg.GetBySourceKey(key)
	require.True(t, ok, "GetBySourceKey must find the row the Upsert wrote")
	require.Equal(t, inv.ShortID, e.ShortID)
}

// p4 exits ZERO when a filespec matches nothing, so a silently inert exclusion
// is indistinguishable from a working one and the volume fills. Refusing costs a
// false refusal on a legitimately-empty subtree; that trade is taken, and the
// operator's escape is to delete an exclusion that was doing nothing anyway.
func TestProvider_APreemptThatMatchedNothingFailsThePrepare(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "@100"},
			{Path: "//s/x/typo/...", Exclude: true},
		},
	}
	client := expectedClientName("h", SourceKey(pf))
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/typo/...@100", "")
	fr.setStreamStderr("-c "+client+" sync -k //"+client+"/typo/...@100",
		"//"+client+"/typo/... - no such file(s).\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	var lines []string
	_, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}},
		func(s string) { lines = append(lines, s) })

	require.Error(t, err)
	require.ErrorContains(t, err, "//s/x/typo/...")
	// The cause travels on the returned error and is NOT repeated on a progress
	// line, the convention TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause
	// already pins for the sync branch.
	for _, l := range lines {
		require.NotContains(t, l, "no such file")
	}
	// No sync may have run: an inert exclusion followed by a full sync is the
	// exact outcome the refusal exists to prevent.
	for _, c := range fr.argHistory() {
		require.NotContains(t, strings.Join(c, " "), "--parallel=4")
	}
}

// A preempt reporting nothing at all is an ALREADY-EXCLUDED subtree on a warm
// workspace, which is success. Reading zero output as failure would refuse every
// prepare after the first.
func TestProvider_APreemptReportingUpToDateSucceeds(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	pf := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/...", Rev: "@100"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	client := expectedClientName("h", SourceKey(pf))
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -k //"+client+"/heavy/...@100", "")
	fr.setStreamStderr("-c "+client+" sync -k //"+client+"/heavy/...@100",
		"//"+client+"/heavy/... - file(s) up-to-date.\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@100", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err, "zero per-file lines is success, not emptiness")
	require.NoError(t, h.Finalize(context.Background()))
}
```

Add `"path/filepath"` and `"strings"` to that file's imports if absent.

- [ ] **Step 2: run and fail**

```bash
go test ./internal/agent/source/perforce/ -run 'TestProvider_AnExclusion|TestProvider_EveryWorkspace|TestProvider_APreempt' -v -count=1
```

Expected: all four FAIL - the fake reports unregistered fixture keys, because `Prepare` still keys on `pf.Stream` and issues no `sync -k`.

- [ ] **Step 3: add the resolution helper**

Append to `internal/agent/source/perforce/preempt.go`:

```go
import (
	"fmt"
	"strings"

	"relay/internal/jobspec"
	relayv1 "relay/internal/proto/relayv1"
)

// preemptSpec is one exclusion resolved into what the p4 call and the log line
// each need: the CLIENT-form argv element, and the DEPOT path the operator
// wrote and can act on.
type preemptSpec struct {
	depotPath  string
	clientSpec string
}

// preemptSpecs resolves every excluded entry to the have-list preempt that
// implements it. revOf supplies the resolved revision of an include by its depot
// path - "@12345" where the spec said "#head".
//
// IT REFUSES RATHER THAN GUESSES, and the wording follows toClientPath's: a spec
// reaching here has passed jobspec.validateSourceSpec, which already requires
// exactly one covering include, so a violation means the spec did not come
// through validation and synthesising a revision would preempt at the wrong one
// - which fetches the excluded subtree rather than skipping it.
//
// It uses jobspec.DepotPathCovers, the SAME predicate the validator used to
// decide there is exactly one coverer. A second implementation here could
// disagree with the validator about which include supplies the revision.
func preemptSpecs(clientName, stream string, sync []*relayv1.SyncEntry, revOf map[string]string) ([]preemptSpec, error) {
	var out []preemptSpec
	for _, e := range sync {
		if !e.GetExclude() {
			continue
		}
		cover := ""
		for _, inc := range sync {
			if inc.GetExclude() || !jobspec.DepotPathCovers(inc.GetPath(), e.GetPath()) {
				continue
			}
			if cover != "" {
				return nil, fmt.Errorf("perforce: excluded path %s is covered by more than one sync path; "+
					"this spec did not come through jobspec validation", e.GetPath())
			}
			cover = inc.GetPath()
		}
		if cover == "" {
			return nil, fmt.Errorf("perforce: excluded path %s is covered by no sync path; "+
				"this spec did not come through jobspec validation", e.GetPath())
		}
		cp, err := toClientPath(clientName, stream, e.GetPath())
		if err != nil {
			return nil, err
		}
		out = append(out, preemptSpec{depotPath: e.GetPath(), clientSpec: cp + revOf[cover]})
	}
	return out, nil
}
```

Move the existing `import "strings"` into that block.

- [ ] **Step 4: rewrite the identity and sync-spec construction in `Prepare`**

Replace the four identity sites. Immediately after the `pf == nil` guard and the registry load:

```go
	// The workspace identity, computed ONCE and used by every site below. It is
	// the stream when nothing is excluded, so every pre-existing registry row,
	// short id and client name stays valid.
	// TestProvider_EveryWorkspaceIdentitySiteUsesTheSameKey.
	sourceKey := SourceKey(pf)

	existing, found := reg.GetBySourceKey(sourceKey)
	var shortID string
	if found {
		shortID = existing.ShortID
	} else {
		shortID = allocateShortID(sourceKey, reg)
	}
```

In the cold-path `reg.Upsert`, `SourceKey: sourceKey`. In the returned `perforceHandle`, `sourceKey: sourceKey`.

Then replace the resolve loop so it walks INCLUDES only, and build the preempts after it:

```go
	resolved := make(map[string]string, len(pf.Sync))
	revOf := make(map[string]string, len(pf.Sync))
	syncSpecs := make([]string, 0, len(pf.Sync))
	syncPaths := make([]string, 0, len(pf.Sync))
	for _, e := range pf.Sync {
		// An EXCLUDED entry contributes no argv element and no synced path.
		// Request.SyncPaths feeds Workspace.syncedPaths, and recording an
		// excluded path there would assert content is present that is
		// deliberately absent.
		if e.GetExclude() {
			continue
		}
		cp, err := toClientPath(clientName, pf.Stream, e.Path)
		if err != nil {
			return nil, err
		}
		rev := e.Rev
		if rev == "#head" {
			cl, err := p.cfg.Client.ResolveHead(ctx, clientName, cp)
			if err != nil {
				return nil, classifyP4Error(fmt.Errorf("resolve head for %s: %w", e.Path, err))
			}
			rev = fmt.Sprintf("@%d", cl)
			resolved[e.Path] = rev
		}
		revOf[e.Path] = rev
		syncSpecs = append(syncSpecs, cp+rev)
		syncPaths = append(syncPaths, e.Path)
	}
	preempts, err := preemptSpecs(clientName, pf.Stream, pf.Sync, revOf)
	if err != nil {
		return nil, err
	}
```

- [ ] **Step 5: run the preempts inside the `needsSync` block**

Immediately after the `recoverOrphanedCLs` block and **before** `progress("[sync] starting: ...")`:

```go
	if needsSync && len(preempts) > 0 {
		// The count first, then one line per exclusion with the DEPOT path
		// rendered %q and LAST - syncSummary's rule, so a forged path cannot
		// spell a convincing line of its own. The count is bounded by
		// jobspec's maxSyncExclusions.
		progress(fmt.Sprintf("[sync] excluding: %d path(s)", len(preempts)))
		for _, pe := range preempts {
			progress(fmt.Sprintf("[sync] exclude %q", pe.depotPath))
			sp := &syncProgress{}
			stderr, err := p.cfg.Client.SyncPreempt(ctx, wsRoot, clientName, pe.clientSpec, sp.onLine)
			if err != nil {
				handle.Release()
				return nil, classifyP4Error(fmt.Errorf("exclude %s: %w", pe.depotPath, err))
			}
			// A PREEMPT THAT MATCHED NOTHING FAILS THE PREPARE. p4 exits ZERO on
			// a filespec that matches nothing, so a silent preempt is
			// indistinguishable from a working one and the operator reads the log
			// after the volume is full. Two live routes reach it: a typo, and an
			// exclusion under a stream whose view renames a subtree, where
			// toClientPath emits a client path that resolves to nothing.
			// The cause travels on the error and is NOT repeated on a progress
			// line, as the sync-failure branch below documents.
			if preemptReportedNoSuchFiles(stderr) {
				handle.Release()
				return nil, fmt.Errorf("exclude %s: p4 reports no such file(s) under that path; "+
					"the exclusion would do nothing. Check the path, and note that a stream whose "+
					"view renames a subtree does not address that subtree by its depot path", pe.depotPath)
			}
		}
	}
```

- [ ] **Step 6: run and pass**

```bash
go test ./internal/agent/source/perforce/ -count=1
go test ./... -count=1 2>&1 | grep -v "^ok" | head -20
```

Expected: the four new tests pass and every pre-existing perforce test still passes - a spec with no exclusions produces the same key, the same argv and the same two progress lines it always did.

- [ ] **Step 7: mutation battery**

| Mutation | Must redden |
|---|---|
| `sourceKey := SourceKey(pf)` -> `pf.Stream` | `EveryWorkspaceIdentitySiteUsesTheSameKey` (and Task 12's p4d test) |
| `allocateShortID(sourceKey, reg)` -> `allocateShortID(pf.Stream, reg)` | same |
| `cp + revOf[cover]` -> `cp + "#head"` | `AnExclusionIsPreemptedAtItsCoveringIncludesResolvedRevision` |
| move the preempt loop after `runSyncWithHeartbeat` | the `preemptAt < syncAt` assertion |
| drop the `preemptReportedNoSuchFiles` refusal | `APreemptThatMatchedNothingFailsThePrepare` |
| refuse when `sp` counted zero lines instead of on the text | `APreemptReportingUpToDateSucceeds` |
| keep excluded entries in `syncPaths` | the `NotContains "/heavy/"` assertion |

- [ ] **Step 8: commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/preempt.go internal/agent/source/perforce/perforce_test.go
git commit -m "feat(perforce): key the workspace on the exclusion set and preempt the excluded subtree"
```

---

## Task 11: the coordinator computes the same key, and the warm bias uses it

**Lane:** default.
**Files:** Modify `internal/scheduler/source_proto.go`, `dispatch.go`, `select_worker_test.go`, `source_proto_test.go`.

`selectWorker` compares `ws.SourceKey == taskSrc.Stream`. Left alone, the warm bias silently stops firing for every excluded task: a composite key never equals a bare stream, so an excluded task is scattered across cold agents and every scatter is a full sync.

- [ ] **Step 1: write the failing tests**

Add to `internal/scheduler/source_proto_test.go`:

```go
// The coordinator and the agent must never compute different strings for one
// spec. They cannot, because this delegates to the agent's own function - and
// this test is what keeps a future "optimisation" from reimplementing it here.
func TestSourceKeyFromAPISpec_DelegatesToThePerforceFunction(t *testing.T) {
	s := &api.SourceSpec{
		Type:   "perforce",
		Stream: "//s/x",
		Sync: []api.SyncEntry{
			{Path: "//s/x/...", Rev: "#head"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	require.Equal(t, perforce.SourceKey(sourceSpecToProto(s).GetPerforce()), SourceKeyFromAPISpec(s))
	require.Equal(t, "", SourceKeyFromAPISpec(nil))
	require.Equal(t, "", SourceKeyFromAPISpec(&api.SourceSpec{Type: "git"}))
}

func TestSourceKeyFromAPISpec_NoExclusionsIsTheBareStream(t *testing.T) {
	s := &api.SourceSpec{Type: "perforce", Stream: "//s/x",
		Sync: []api.SyncEntry{{Path: "//s/x/...", Rev: "#head"}}}
	require.Equal(t, "//s/x", SourceKeyFromAPISpec(s))
}

// The candidate list the warm lookup is built from must use the SAME producer
// selectWorker compares against, or the lookup fetches rows the comparison can
// never match.
func TestWarmKeysForTasks_UsesTheKeySelectWorkerCompares(t *testing.T) {
	tasks := []store.Task{
		{Source: []byte(`{"type":"perforce","stream":"//s/x","sync":[{"path":"//s/x/...","rev":"#head"}]}`)},
		{Source: []byte(`{"type":"perforce","stream":"//s/x","sync":[{"path":"//s/x/...","rev":"#head"},{"path":"//s/x/heavy/...","exclude":true}]}`)},
		{Source: nil},
		{Source: []byte(`{`)},
	}
	got := warmKeysForTasks(tasks)
	require.Len(t, got["perforce"], 2)
	require.Contains(t, got["perforce"], "//s/x")
	require.NotContains(t, got["perforce"], "//s/x",
		"") // replaced below - see Step 1b
}
```

**Step 1b.** The last two lines above are deliberately contradictory so you fix them against the real key rather than pasting: replace them with

```go
	excl := &api.SourceSpec{Type: "perforce", Stream: "//s/x",
		Sync: []api.SyncEntry{
			{Path: "//s/x/...", Rev: "#head"},
			{Path: "//s/x/heavy/...", Exclude: true},
		}}
	require.Contains(t, got["perforce"], SourceKeyFromAPISpec(excl),
		"the excluded task's candidate key is the composite one, not the bare stream")
```

Add to `internal/scheduler/select_worker_test.go`:

```go
// sourceTaskWithExclusion is the same spec as sourceTask plus one exclusion, so
// the only thing that differs between the pair of tests below is the key.
func sourceTaskWithExclusion() store.Task {
	t := baseTask()
	t.Source = []byte(`{"type":"perforce","stream":"//depot/main","sync":` +
		`[{"path":"//depot/main/...","rev":"#head"},` +
		`{"path":"//depot/main/heavy/...","exclude":true}]}`)
	return t
}

func warmOn(id byte, key string) (store.Worker, []store.WorkerWorkspace) {
	w := baseWorker(id, "online")
	w.MaxSlots = 1
	w.SupportsWorkspaces = true
	return w, []store.WorkerWorkspace{{WorkerID: w.ID, SourceType: "perforce", SourceKey: key}}
}

// An excluded task must NOT be scored warm on a workspace holding the whole
// stream: that workspace has no have-list preempt, and preferring it is how the
// bias would keep pushing excluded tasks onto workspaces they cannot use. The
// discriminator is the fewer-slots warm worker versus a freer cold one: with the
// bias firing the warm worker wins, so a cold winner is the observable proof it
// did not fire.
func TestSelectWorker_AnExcludedTaskIsNotWarmOnAnUnexcludedWorkspace(t *testing.T) {
	d := newDispatcherForTest()
	warm, rows := warmOn(80, "//depot/main")
	cold := baseWorker(81, "online")
	cold.MaxSlots = 4
	cold.SupportsWorkspaces = true

	got := d.selectWorker(sourceTaskWithExclusion(), []store.Worker{warm, cold}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{warm.ID: rows})

	require.NotNil(t, got)
	assert.Equal(t, cold.ID, got.ID, "the whole-stream workspace is not warm for an excluded task")
}

// The exact sibling. Widening the comparison without this would lose the warm
// bias entirely and nothing would say so.
func TestSelectWorker_AnUnexcludedTaskIsStillWarmOnItsStreamKeyedWorkspace(t *testing.T) {
	d := newDispatcherForTest()
	warm, rows := warmOn(82, "//depot/main")
	cold := baseWorker(83, "online")
	cold.MaxSlots = 4
	cold.SupportsWorkspaces = true

	task := baseTask()
	task.Source = []byte(`{"type":"perforce","stream":"//depot/main","sync":[{"path":"//depot/main/...","rev":"#head"}]}`)

	got := d.selectWorker(task, []store.Worker{warm, cold}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{warm.ID: rows})

	require.NotNil(t, got)
	assert.Equal(t, warm.ID, got.ID, "an unexcluded task keeps today's warm bias, byte for byte")
}

// And the excluded task IS warm on the matching composite key, which kills a
// mutant that simply stops scoring warm for anything carrying an exclusion.
func TestSelectWorker_AnExcludedTaskIsWarmOnItsOwnCompositeKey(t *testing.T) {
	d := newDispatcherForTest()
	task := sourceTaskWithExclusion()
	var s api.SourceSpec
	require.NoError(t, json.Unmarshal(task.Source, &s))
	warm, rows := warmOn(84, SourceKeyFromAPISpec(&s))
	cold := baseWorker(85, "online")
	cold.MaxSlots = 4
	cold.SupportsWorkspaces = true

	got := d.selectWorker(task, []store.Worker{warm, cold}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{warm.ID: rows})

	require.NotNil(t, got)
	assert.Equal(t, warm.ID, got.ID)
}
```

Add `"encoding/json"` and `"relay/internal/api"` to that file's imports.

- [ ] **Step 2: run and fail**

```bash
go test ./internal/scheduler/ -run 'TestSourceKeyFromAPISpec|TestWarmKeysForTasks|TestSelectWorker_AnExcluded|TestSelectWorker_AnUnexcluded' -v -count=1
```

Expected: compile failure on `SourceKeyFromAPISpec` and `warmKeysForTasks`; once those exist, `AnExcludedTaskIsNotWarmOnAnUnexcludedWorkspace` FAILS (the warm worker wins today, because the bare stream matches the bare-stream comparison).

- [ ] **Step 3: add the key function**

In `internal/scheduler/source_proto.go`:

```go
// SourceKeyFromAPISpec computes the workspace source key from an API SourceSpec:
// the exact sibling of BaselineHashFromAPISpec, and the ONLY server-side
// producer of the string compared against worker_workspaces.source_key.
//
// IT DELEGATES rather than reimplements. The agent registers the key
// perforce.SourceKey produced, so a second implementation here would be a
// silent disagreement between two processes about which workspace a task's
// files are in. Returns "" for non-perforce or nil specs.
func SourceKeyFromAPISpec(s *api.SourceSpec) string {
	if s == nil || s.Type != "perforce" {
		return ""
	}
	proto := sourceSpecToProto(s)
	if proto == nil {
		return ""
	}
	return perforce.SourceKey(proto.GetPerforce())
}
```

- [ ] **Step 4: extract the candidate builder and rewire the comparison**

In `dispatch.go`, replace the inline `streamsByType` loop with a call, and add the function:

```go
	streamsByType := warmKeysForTasks(tasks)
```

```go
// warmKeysForTasks groups the eligible tasks' workspace source keys by source
// type, for the warm-workspace lookup. Extracted from dispatch so the keys it
// asks the database for and the keys selectWorker compares against provably come
// from one producer; a lookup keyed on anything else fetches rows the comparison
// can never match, and the bias just silently stops firing.
func warmKeysForTasks(tasks []store.Task) map[string][]string {
	out := make(map[string][]string)
	for _, task := range tasks {
		if len(task.Source) == 0 {
			continue
		}
		var s api.SourceSpec
		if err := json.Unmarshal(task.Source, &s); err != nil {
			continue
		}
		if s.Type == "" || s.Stream == "" {
			continue
		}
		if k := SourceKeyFromAPISpec(&s); k != "" {
			out[s.Type] = append(out[s.Type], k)
		}
	}
	return out
}
```

In `selectWorker`, after `sourceBearing := taskIsSourceBearing(task)`:

```go
	// Computed ONCE, outside the worker loop, and through the same function the
	// agent's registry key comes from. Comparing taskSrc.Stream here - what this
	// did before exclusions existed - silently stops matching for every excluded
	// task, because a composite key never equals a bare stream.
	warmKey := SourceKeyFromAPISpec(taskSrc)
```

and inside the loop:

```go
		score := free
		if warmKey != "" {
			for _, ws := range warmByWorker[w.ID] {
				if ws.SourceType == taskSrc.Type && ws.SourceKey == warmKey {
					estimate := BaselineHashFromAPISpec(taskSrc)
					if estimate != "" && ws.BaselineHash == estimate {
						score += 10_000
					} else {
						score += 1_000
					}
					break
				}
			}
		}
```

- [ ] **Step 5: run and pass**

```bash
go test ./internal/scheduler/ -count=1 -v
go test ./... -count=1 2>&1 | grep -v "^ok" | head -20
```

Expected: all new tests pass; every pre-existing `select_worker_test.go` and `dispatch_test.go` case still passes.

- [ ] **Step 6: mutation battery**

| Mutation | Must redden |
|---|---|
| `ws.SourceKey == warmKey` -> `ws.SourceKey == taskSrc.Stream` | `AnExcludedTaskIsNotWarmOnAnUnexcludedWorkspace` |
| `warmKey != ""` -> `false` | `AnUnexcludedTaskIsStillWarmOnItsStreamKeyedWorkspace` |
| `SourceKeyFromAPISpec` returns `s.Stream` | `AnExcludedTaskIsWarmOnItsOwnCompositeKey` and `DelegatesToThePerforceFunction` |
| `warmKeysForTasks` appends `s.Stream` | `WarmKeysForTasks_UsesTheKeySelectWorkerCompares` |

- [ ] **Step 7: commit**

```bash
git add internal/scheduler/source_proto.go internal/scheduler/source_proto_test.go internal/scheduler/dispatch.go internal/scheduler/select_worker_test.go
git commit -m "fix(scheduler): key the warm-workspace bias on the source key, not the bare stream"
```

---

## Task 12: the acceptance test - an excluding task must not strip files from an unexcluding peer

**Lane:** p4d integration (human-run only).
**Files:** Modify `internal/agent/source/perforce/perforce_exclusion_integration_test.go`.

This is the item's headline criterion and the only test that can prove any of it.

- [ ] **Step 1: write the test**

Append to `perforce_exclusion_integration_test.go`:

```go
// THE ORDER IS LOAD-BEARING: the EXCLUDING task runs FIRST.
//
// Run the unexcluding task first and its full sync leaves heavy/asset.txt on
// disk; the excluding task then shares the same directory under the broken
// build, finds the file already there, and every assertion below passes against
// exactly the defect this design exists to prevent. Only excluding-then-including
// can observe a workspace missing files the second task asked for.
//
// THE MUTATION THIS MUST KILL: make SourceKey ignore exclusions and return the
// bare stream. Task B then shares Task A's workspace, the preempted files are
// never fetched, and step 4's FileExists goes RED.
//
// Same CI note as TestPerforce_E2E_SyncKReportsNoSuchFilesOnStderrAndExitsZero
// above: nothing in .github/workflows provides p4d or the p4 client, so this is
// human-run until a workflow job builds testdata/p4d, installs the Perforce CLI
// and is added to a Makefile target's package list. It cannot move to the
// default lane at all - a fake runner cannot say whether a file is on disk.
func TestPerforce_E2E_AnExcludingTaskDoesNotStripFilesFromAnUnexcludingPeer(t *testing.T) {
	p4dEnv(t)

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	mk := func(exclude bool) *relayv1.SourceSpec {
		sync := []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}}
		if exclude {
			sync = append(sync, &relayv1.SyncEntry{Path: "//test/main/heavy/...", Exclude: true})
		}
		return &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//test/main", Sync: sync},
		}}
	}

	// --- Task A: EXCLUDES heavy/. Runs FIRST; see the comment above. ---
	hA, err := prov.Prepare(ctx, "task-a", mk(true), func(s string) { t.Logf("A: %s", s) })
	require.NoError(t, err, "the excluding prepare must succeed")
	invA := hA.Inventory()
	wsA := hA.WorkingDir()

	require.NoFileExists(t, filepath.Join(wsA, "heavy", "asset.txt"),
		"the excluded subtree must never be transferred")
	require.FileExists(t, filepath.Join(wsA, "readme.txt"),
		"everything outside the exclusion must still be synced")

	require.NoError(t, hA.Finalize(ctx))

	// --- Task B: NO exclusion. It must not observe a workspace missing files. ---
	hB, err := prov.Prepare(ctx, "task-b", mk(false), func(s string) { t.Logf("B: %s", s) })
	require.NoError(t, err)
	invB := hB.Inventory()
	wsB := hB.WorkingDir()
	defer func() { _ = hB.Finalize(ctx) }()

	require.NotEqual(t, wsA, wsB, "a different exclusion set is a different workspace")
	require.NotEqual(t, invA.ShortID, invB.ShortID)

	b, err := os.ReadFile(filepath.Join(wsB, "heavy", "asset.txt"))
	require.NoError(t, err,
		"THE ACCEPTANCE CRITERION: a task with no exclusions gets the whole stream, "+
			"whatever a previous task on the same stream excluded")
	require.Equal(t, "heavy", strings.TrimSpace(string(b)))

	// The keys themselves: B's is exactly today's, A's is the versioned composite.
	require.Equal(t, "//test/main", invB.SourceKey,
		"a task with no exclusions keeps today's key byte for byte")
	require.True(t, strings.HasPrefix(invA.SourceKey, "x1|"))
	require.True(t, strings.HasSuffix(invA.SourceKey, "|//test/main"))

	reg, err := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	keys := map[string]bool{}
	for _, e := range reg.Snapshot() {
		keys[e.SourceKey] = true
	}
	require.Len(t, keys, 2, "the registry holds two entries with distinct source keys")
	require.True(t, keys["//test/main"] && keys[invA.SourceKey])
}
```

- [ ] **Step 2: run it**

```bash
go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_AnExcludingTask -v -timeout 1800s
```

Expected: PASS.

- [ ] **Step 3: run the mutation the item names**

```bash
cp internal/agent/source/perforce/sourcekey.go /tmp/lane-e-sourcekey.go.bak
```

In `SourceKey`, replace the whole body after the nil check with `return p.GetStream()`. Re-run the command from Step 2.

Expected: **RED**, on `require.NoError(t, err)` for `wsB/heavy/asset.txt` - B shares A's workspace and the preempted file was never fetched. If it passes, the test is not discriminating and must not be committed; check that Task A really ran first and that the workspace root is not being reused between runs.

Then `cp /tmp/lane-e-sourcekey.go.bak internal/agent/source/perforce/sourcekey.go` and re-run: PASS.

- [ ] **Step 4: run the whole p4d lane once**

```bash
go test -tags integration -p 1 ./internal/agent/source/perforce/... -v -timeout 1800s
```

Expected: every test green, including the two that Task 1 touched.

- [ ] **Step 5: commit**

```bash
git add internal/agent/source/perforce/perforce_exclusion_integration_test.go
git commit -m "test(p4d): an excluding task must not strip files from an unexcluding peer"
```

---

## Task 13: README - the field, the disk coupling, and the limitation Stage 2 removes

**Lane:** docs. No test.
**Files:** Modify `README.md` (the "Source workspaces" section).

- [ ] **Step 1: add the field to the example**

In the `source` field-shape JSON block, change the `sync` array to:

```json
    "sync": [
      { "path": "//depot/film-x/main/...", "rev": "#head" },
      { "path": "//depot/film-x/main/Content/Movies/...", "exclude": true }
    ],
```

- [ ] **Step 2: replace the `sync` table row and add nothing else to the table**

```
| `sync` | Yes | One or more paths to sync. Each entry has `path` (depot path or `...`) and `rev` (`"#head"`, `@CL`, or `@label`). An entry may instead set `"exclude": true`, which leaves that path out of the sync; an excluded entry carries no `rev` (it is applied at the revision of the include that covers it) and must be covered by exactly one included path. At most 16 exclusions per spec. A relay-server that does not know the field ignores it and syncs the whole path. |
```

- [ ] **Step 3: add two paragraphs after "Warm-workspace preference"**

```
**Exclusions change the workspace identity.** A task's exclusion set is part of the key its workspace is stored under, so two tasks on one stream with different exclusion sets hold two separate workspaces on the same agent - each nearly full size. That is what stops one task's exclusion from removing files another task asked for, and it means an exclusion can INCREASE total disk use on a shared agent rather than reduce it. Exclusions pay when they are uniform for that stream on that agent. The excess does not overflow the volume: the sweeper's pressure pass (`RELAY_WORKSPACE_MIN_FREE_GB`) evicts oldest-first, so it converts into eviction churn, and each evicted warm workspace costs a full re-sync when its stream is next scheduled there.

**An exclusion that matches nothing fails the task's prepare.** p4 exits zero when a path matches no files, so relay checks p4's own report and refuses rather than continuing - a silently inert exclusion would be discovered only after the volume filled. A subtree that is legitimately empty at that revision is refused too; the remedy is to delete an exclusion that was doing nothing.
```

- [ ] **Step 4: state the limitation Stage 2 removes**

Append to the second paragraph above (same block, so Stage 2 deletes exactly these sentences):

```
Exclusions require every agent in the fleet to be running a relay-agent build that supports them. An older agent silently drops the field and syncs the whole path, and the coordinator does not currently detect that, so do not roll exclusions out during a mixed-version upgrade.
```

- [ ] **Step 5: verify the edit did not damage the file**

```bash
git diff --stat README.md
git ls-files --eol README.md
python -c "import pathlib;pathlib.Path('README.md').read_text(encoding='utf-8')"
grep -n "exclude" README.md | head
```

Expected: a diffstat proportionate to five short edits (not hundreds of lines), `i/lf`, a clean UTF-8 decode, and no stray non-ASCII bytes introduced.

- [ ] **Step 6: commit**

```bash
git add README.md
git commit -m "docs: sync exclusions, the disk coupling they create, and the version-skew limitation"
```

---

# Stage 2 - Close the version skew

Spec section 8. An older relay-agent drops the proto field and syncs the whole stream: a silent multi-terabyte over-sync where relay elsewhere (`supports_workspaces`) takes rolling-upgrade skew seriously enough to spend an `optional bool` on it.

## Task 14: the capability field, the column, and the statements

**Lane:** default for the Go tests; `make test-pg-integration` for anything touching the store.
**Files:** Modify `proto/relayv1/relay.proto`; create `internal/store/migrations/000024_workers_supports_sync_exclusions.{up,down}.sql`; modify `internal/store/query/workers.sql`; regenerate.

- [ ] **Step 1: add the proto field**

On `RegisterRequest`, after `optional bool supports_workspaces = 12;`:

```proto
  // supports_sync_exclusions reports whether this agent honours
  // SyncEntry.exclude. optional gives explicit presence.
  //
  // UNLIKE supports_workspaces, THE SAFE DEFAULT IS FALSE and the column is
  // written on EVERY connect rather than preserved on omission. An agent that
  // does not report is presumed not to honour exclusions, which is also the
  // correct reading of a DOWNGRADE: a new agent replaced by an older binary
  // stops sending the field, and preserving the previous TRUE would keep
  // dispatching excluded tasks to a build that ignores them.
  optional bool supports_sync_exclusions = 13;
```

- [ ] **Step 2: write the migration**

`000024_workers_supports_sync_exclusions.up.sql`:

```sql
ALTER TABLE workers ADD COLUMN supports_sync_exclusions BOOLEAN NOT NULL DEFAULT FALSE;
```

`...down.sql`:

```sql
ALTER TABLE workers DROP COLUMN supports_sync_exclusions;
```

- [ ] **Step 3: write the statements**

In `internal/store/query/workers.sql`, in `RegisterWorkerConnection`'s SET list:

```sql
    supports_workspaces = COALESCE(sqlc.narg(supports_workspaces)::bool, supports_workspaces),
    -- NOT the COALESCE-to-previous shape above it, deliberately. The safe
    -- default for exclusions is FALSE: an agent that does not report the
    -- capability is presumed not to honour it, and that reading must survive a
    -- DOWNGRADE, where preserving the previous TRUE would keep sending excluded
    -- tasks to a build that ignores the field.
    supports_sync_exclusions = COALESCE(sqlc.narg(supports_sync_exclusions)::bool, FALSE)
```

In `UpsertWorkerByHostname` and `InsertWorkerForAutoEnroll`, add `supports_sync_exclusions` to the column list, `COALESCE(sqlc.narg(supports_sync_exclusions)::bool, FALSE)` to `VALUES`, and to `UpsertWorkerByHostname`'s `DO UPDATE SET` and its explicit `RETURNING` column list.

- [ ] **Step 4: regenerate and check**

```bash
make generate
git diff --ignore-all-space --name-only
git status --porcelain
```

`git checkout --` every line-ending-only file, then confirm `SupportsSyncExclusions` is in `internal/store/models.go` and `workers.sql.go`, and that `relay.pb.go` has `GetSupportsSyncExclusions`.

- [ ] **Step 5: build and commit**

```bash
go build ./... && go test ./... -count=1 2>&1 | grep -v "^ok" | head -20
git add proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go internal/store/migrations internal/store/query/workers.sql internal/store/models.go internal/store/workers.sql.go
git commit -m "feat(store): workers.supports_sync_exclusions, defaulting closed"
```

---

## Task 15: the agent reports it and the handler persists it

**Lane:** `make test` for the agent test; the handler test lives beside `TestHandler_*` in `internal/worker` and follows whatever lane its neighbours use.
**Files:** Modify `internal/agent/agent.go`, `internal/agent/register_request_test.go`, `internal/worker/handler.go`.

- [ ] **Step 1: write the failing agent test**

In `register_request_test.go`, mirroring `TestBuildRegisterRequest_SupportsWorkspaces`:

```go
func TestBuildRegisterRequest_SupportsSyncExclusions(t *testing.T) {
	// This build honours SyncEntry.exclude, so it always reports true - with
	// explicit presence, so an OLD agent's omission stays distinguishable from a
	// new agent reporting false.
	a := &Agent{caps: capabilities.Caps{}}
	req := a.buildRegisterRequest()
	require.NotNil(t, req.SupportsSyncExclusions, "field must be set with explicit presence")
	assert.True(t, req.GetSupportsSyncExclusions())
}
```

Match the surrounding tests' construction of `*Agent` exactly - read `TestBuildRegisterRequest_SupportsWorkspaces` and copy its setup rather than the sketch above.

- [ ] **Step 2: run and fail.** Expected: `req.SupportsSyncExclusions undefined` (the proto field exists after Task 14, so this fails on the Go side only if you skipped it).

- [ ] **Step 3: set it**

In `buildRegisterRequest`, beside `SupportsWorkspaces`:

```go
		// Unconditionally true: this binary honours SyncEntry.exclude. It is not
		// derived from a.provider - a providerless agent runs no source spec at
		// all - and it is not a config knob, because it describes the BUILD.
		SupportsSyncExclusions: proto.Bool(true),
```

- [ ] **Step 4: plumb it at all three handler sites**

In `internal/worker/handler.go`, add `SupportsSyncExclusions: reg.SupportsSyncExclusions` beside each existing `SupportsWorkspaces: reg.SupportsWorkspaces` (the `UpsertWorkerByHostname` call, the `InsertWorkerForAutoEnroll` call, and the `RegisterWorkerConnection` call in `finishRegister`). **All three, not two:** a missed site means a whole enrollment route registers workers that can never receive an excluded task.

- [ ] **Step 5: run and pass**

```bash
go build ./... && go test ./... -count=1 2>&1 | grep -v "^ok" | head -20
```

- [ ] **Step 6: commit**

```bash
git add internal/agent/agent.go internal/agent/register_request_test.go internal/worker/handler.go
git commit -m "feat(agent): report supports_sync_exclusions, and persist it on every registration route"
```

---

## Task 16: the dispatcher refuses to send an excluded task to an agent that would ignore it

**Lane:** default.
**Files:** Modify `internal/scheduler/dispatch.go` and `select_worker_test.go`.

- [ ] **Step 1: write the failing tests**

```go
// An agent that does not honour exclusions would sync the whole stream - the
// silent multi-terabyte over-sync the capability exists to prevent. A hard skip,
// not a lower score, exactly as the SupportsWorkspaces filter beside it.
func TestSelectWorker_AnExcludedTaskSkipsAWorkerWithoutTheCapability(t *testing.T) {
	d := newDispatcherForTest()
	w := baseWorker(90, "online")
	w.SupportsWorkspaces = true // capable of workspaces, NOT of exclusions

	got := d.selectWorker(sourceTaskWithExclusion(), []store.Worker{w}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{})

	assert.Nil(t, got, "an excluded task must not dispatch to an agent that would ignore the exclusion")
}

// The exact sibling: a task with NO exclusion is unaffected by the new
// predicate, so the whole existing fleet keeps working after the upgrade.
func TestSelectWorker_AnUnexcludedTaskIgnoresTheExclusionCapability(t *testing.T) {
	d := newDispatcherForTest()
	w := baseWorker(91, "online")
	w.SupportsWorkspaces = true

	got := d.selectWorker(sourceTask(), []store.Worker{w}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{})

	require.NotNil(t, got)
	assert.Equal(t, w.ID, got.ID)
}

func TestSelectWorker_AnExcludedTaskSelectsACapableWorker(t *testing.T) {
	d := newDispatcherForTest()
	w := baseWorker(92, "online")
	w.SupportsWorkspaces = true
	w.SupportsSyncExclusions = true

	got := d.selectWorker(sourceTaskWithExclusion(), []store.Worker{w}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{})

	require.NotNil(t, got)
	assert.Equal(t, w.ID, got.ID)
}
```

- [ ] **Step 2: run and fail.** Expected: `AnExcludedTaskSkipsAWorkerWithoutTheCapability` FAILS - the worker is selected today.

- [ ] **Step 3: add the predicate**

In `selectWorker`, beside `sourceBearing`:

```go
	// Whether this task's source carries an exclusion, decided once. The proto
	// conversion is the same one that produces warmKey, so the two cannot
	// disagree about what "carries an exclusion" means.
	taskHasExclusion := false
	if taskSrc != nil {
		for _, e := range taskSrc.Sync {
			if e.Exclude {
				taskHasExclusion = true
				break
			}
		}
	}
```

and inside the worker loop, immediately after the `SupportsWorkspaces` filter:

```go
		// The same hard-filter shape as the line above, for the same reason: an
		// agent that drops the exclude field syncs the whole stream, which is a
		// silent over-sync rather than a visible failure, so it must not be
		// scored lower - it must not be chosen at all.
		if taskHasExclusion && !w.SupportsSyncExclusions {
			continue
		}
```

- [ ] **Step 4: run and pass**

```bash
go test ./internal/scheduler/ -count=1 && go test ./... -count=1 2>&1 | grep -v "^ok" | head -20
```

- [ ] **Step 5: mutation battery**

| Mutation | Must redden |
|---|---|
| `continue` -> `score -= 1000` | `AnExcludedTaskSkipsAWorkerWithoutTheCapability` |
| drop `taskHasExclusion &&` | `AnUnexcludedTaskIgnoresTheExclusionCapability` |
| `taskHasExclusion` always false | `AnExcludedTaskSkipsAWorkerWithoutTheCapability` |

- [ ] **Step 6: commit**

```bash
git add internal/scheduler/dispatch.go internal/scheduler/select_worker_test.go
git commit -m "feat(scheduler): hold an excluded task rather than send it to an agent that ignores exclusions"
```

---

## Task 17: delete the stated limitation

**Lane:** docs.
**Files:** Modify `README.md`.

- [ ] **Step 1: remove the sentences Task 13 added**

Delete the "Exclusions require every agent ... mixed-version upgrade." sentences and replace them with:

```
An agent that does not support exclusions reports so at registration, and the dispatcher holds an excluded task rather than sending it to that agent - so a mixed-version fleet delays the task instead of silently syncing the whole stream.
```

- [ ] **Step 2: verify and commit**

```bash
git diff --stat README.md && git ls-files --eol README.md
git add README.md
git commit -m "docs: exclusions are no longer a mixed-version hazard"
```

---

## Self-review against the spec

| Spec section | Covered by |
|---|---|
| 3.2 mechanism (have-list preempt) | Tasks 9, 10 |
| 3.3 what a differently-excluded task sees | Task 7 (`OrderAndDuplicatesCanonicalise`), Task 12 |
| 3.4 the disk trade | Task 13's README paragraph. No code; deliberately. |
| 4 spec surface (`exclude` on all three types) | Tasks 4, 5, 6 |
| 5 rules 1-5 | Task 4, one table case per refusal plus two negatives the spec did not name |
| 6.1 `SourceKey` | Task 7; the four Prepare call sites in Task 10 |
| 6.2 `BaselineHash` + golden | Tasks 0, 8 |
| 6.3 warm bias | Task 11 |
| 6.4 exclusions are not `SyncPaths` | Task 10, Step 4's comment and the `NotContains "/heavy/"` assertion |
| 7 operator visibility, both refusals | Task 10, Steps 5 and 1 |
| 8 migration (nothing to migrate) + skew | Task 7's byte-for-byte test; Stage 2 |
| 9 out-of-disk remedy | Deliberately no code. `diagnostics.go` is on the do-not-touch list; the coupling is stated in README instead. |
| 10 threat model | Key collision: Task 7's `x1|` prefix assertions. Log forgery: Task 10's `%q` assertion. Poisoning: Task 12. Workspace multiplication: recorded, not solved. |
| 11 capture first, acceptance test, further tests | Tasks 2, 12, and the per-task batteries |
| 12 out of scope | Nothing here touches `toClientPath`, the general zero-exit defect, admission control, instrumentation, the SPA builder, other providers, or unshelve interaction. |

## For the conductor

**Backlog items to file** (I write plan docs only; I do not run `/backlog`):

1. **No guard keeps `jobspec.SyncEntry` in step with the Python SDK's `Sync`.** A Go commit adding a field to `SyncEntry` cannot redden anything under `python/`, and a guard under `.github/workflows/python.yml`'s `paths: python/**` filter could not fire on that commit either. The guard would have to live on the Go side and read `python/src/relay/models.py`. This slice adds the third field to that pair and does not close it. Related: `idea` item 3 in the spec's section 13 (the `_CLIENT_TEMPLATE_RE` drift) is the same pair, already drifting.
2. **`source.sync` has no entry-count bound** (spec section 13 item 1). This slice bounds exclusions at 16 and leaves the include side bounded only by `maxBodyBytes`, while `Prepare` runs one `ResolveHead` round trip per `#head` entry. It is now the only one of three per-entry axes with no bound.
3. **Nothing records a workspace's size or counts evictions** (spec section 13 item 2). Already filed as `idea-2026-09-04-no-workspace-size-or-eviction-instrumentation` - confirm rather than duplicate.

**Phase 4 note.** The p4d lane is human-run, so the acceptance test and its mutation (Task 12 Step 3) are the one piece of verification that cannot be delegated to a green CI run. Whoever verifies this must state plainly whether they ran it, and report the mutation result, not just the pass.
