# Perforce client-path addressing - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Address p4 by client path (`//<client>/<rel>`) instead of by the stream-name depot path, so a virtual stream or an `import+` remap stream prepares and syncs into the layout its client view defines - and so no exit from `Prepare`, early or successful, leaves a workspace directory or p4 client spec with no registry entry.

**Architecture:** Three coupled edits in one Go package plus one shell fixture. `internal/agent/source/perforce/perforce.go` gains an unexported `toClientPath` and reorders `Prepare` so short-id allocation, `MkdirAll`, `CreateStreamClient` and registration all happen **before** head resolution and before `ws.Acquire`; `client.go`'s `ResolveHead` widens to `(ctx, cwd, client, path)` and runs `p4 -c <client> changes -m1 <clientPath>#head` from the workspace root; and a new re-assertion after the post-Acquire eviction re-check restores a registry entry a concurrent sweep may have removed inside the newly-opened window. The p4d test container's `entrypoint.sh` gains a remapped stream so an integration test can prove the fix against a real server.

**Tech Stack:** Go 1.26, testify, testcontainers-go, a Debian-based p4d r25.2 container, the `p4` CLI on PATH. **No `make generate` step anywhere in this plan** - no `.sql`, no `.proto` and no `web/` file is touched. If you find yourself running `make generate` or rebuilding `web/dist`, you have gone outside the plan.

**Spec:** `docs/superpowers/specs/2026-09-04-perforce-client-path-addressing.md` (autonomous gate). Its decisions D1-D10 stand except where this plan records a refutation below.

**Backlog item this closes (via `/backlog close`, by the conductor, at integration):**

- `docs/backlog/bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync.md`

**In-tree exemplars this plan copies:**

- Fake-runner fixture recipe: `syncFixture` in `internal/agent/source/perforce/perforce_progress_test.go`, and the fixture block at the top of `TestProvider_PrepareCreatesClientAndSyncs` in `perforce_test.go`.
- Concurrent-sweep scaffolding: `gatingRunner` in `provider_evict_recheck_test.go` and `TestSweeperClaim_PrepareBacksOutWhenSweepReservesDuringAcquire` in `sweeper_claim_test.go`.
- Integration-lane scaffolding: `startP4dContainer` in `p4d_container_test.go` and the env-isolation block at the top of `TestPerforce_E2E_SyncAndUnshelve`.
- Sweeper wiring for a test: the `&Sweeper{...}` literal in `TestSweeperClaim_PrepareBacksOutWhenSweepReservesDuringAcquire`, which mirrors `cmd/relay-agent/main.go`.

---

## Slice independence declaration

**This is backend and docs only. There is NO frontend work.** Nothing under `web/` is read, edited, rebuilt or committed. `web/dist` must not be touched. **Do not dispatch `relay-frontend-engineer`.** The FE/BE question does not arise: there is no FE slice to be independent of.

**This is ONE lane, ONE worktree, ONE PR, ONE session. It has NO stages and must NOT be handed to `/backlog phases`.**

**Nothing in this plan may run concurrently.** The tasks are strictly sequential and every one of them writes to the same Go package. Two agents in one worktree share one git index; two worktrees would produce two branches each red until merged, because the three edits are mutually dependent (`ResolveHead` cannot take a client path until a client exists, the client cannot exist before head resolution without the reorder, and the reorder is not safe without the N14 re-assert).

**`relay-backend-engineer` owns the whole lane and is the only agent that writes to the tree.** Tasks 1, 2 and 8 are integration-fixture work, but they touch files nobody else touches and Task 2 is a hard STOP gate for everything after it, so they stay with the same owner rather than being split to `relay-integration-tester`. `relay-integration-tester` is a Phase 4 verifier here, not an implementer.

**Docker plus the `p4` CLI on PATH are required for Tasks 1, 2, 8 and the integration half of Tasks 0 and 11.** Every other task runs in the default lane with no Docker.

---

## What I verified in the spec, and what I refuted

The spec refutes six of the backlog item's claims. I re-derived the load-bearing ones against the tree at HEAD rather than taking them on trust. **Symbols, never line numbers.**

### Confirmed against the tree

| Spec claim | How I checked it | Verdict |
|---|---|---|
| The P1-P16 order of `Prepare` | Opened `Provider.Prepare` in `internal/agent/source/perforce/perforce.go` and read it top to bottom | **Confirmed, in order and in content.** The resolve loop is first; `BaselineHash` next; `GetBySourceKey`/`allocateShortID`, then `wsRoot`/`clientName`; the `p.mu` pre-check and workspace get-or-create; `prepareAcquireHook`; `ws.Acquire`; the post-Acquire `p.evicting` re-check with `handle.Release()`; the `if !found` block containing `MkdirAll`, `CreateStreamClient` and `reg.Upsert`; `reg.Get`/`needsSync`; `recoverOrphanedCLs`; the sync bracket; unshelves; the handle. |
| `ResolveHead` is server-global | `func (c *Client) ResolveHead(ctx context.Context, path string) (int64, error)` runs `c.r.Run(ctx, "", []string{"changes", "-m1", path + "#head"}, nil)` | **Confirmed.** Empty cwd, no `-c`. |
| `CreateStreamClient`'s signature and both cwds | `func (c *Client) CreateStreamClient(ctx context.Context, name, root, stream, template string) error`; both `c.r.Run` calls pass `""` for cwd | **Confirmed.** `client -o -S <stream> [-t <tmpl>] <name>` then `client -i` with `Root`, `Host`, `Owner` overridden via `setSpecField`. |
| `scheduler.BaselineHashFromAPISpec` computes the hash server-side | `internal/scheduler/source_proto.go` calls `perforce.BaselineHash(proto.GetPerforce(), nil)`; `internal/scheduler/dispatch.go`'s worker-scoring loop calls it and compares against `ws.BaselineHash`, `+10_000` on a match and `+1_000` otherwise | **Confirmed, and F4's conclusion is right.** The coordinator has no access to the agent's hostname or allocated short id, both of which feed `clientName`, so a client-form hash would score every warm workspace as merely-present. The depot-form keying is forced. |
| `jobspec.validateSourceSpec` admits exactly three path shapes | Read the `for i, e := range s.Sync` loop in `internal/jobspec/jobspec.go` | **Confirmed**, byte-exact and case-sensitive, and the `s.Stream+"/..."` case is a strict subset of the `HasPrefix(e.Path, s.Stream+"/")` case. |
| `assertCwdContract`'s body needs no change | Read the body in `perforce_test.go`: it branches solely on `c.args[0] == "-c"` | **Confirmed.** A `-c <client> changes -m1 ...` recorded at `cwd == wsRoot` takes the first branch; `client -o`/`client -i`/`client -d` keep `cwd == ""` and take the second. |
| The sweeper's age pass scans only the registry, and defaults off | `Sweeper.SweepOnce` iterates `reg.Snapshot()`; `Sweeper.Run` returns immediately when `MaxAge == 0 && MinFreeGB == 0`; `cmd/relay-agent/main.go` builds a `Sweeper` only under `if maxAge > 0 \|\| minFreeGB > 0` | **Confirmed, qualifier included.** Do not write "the sweeper reclaims it" unqualified anywhere. |
| The F5 race is real | `Provider.ReserveForEvict` and `EvictWorkspace` both check `len(ws.holders) > 0` under `p.mu`; `Workspace.Acquire` is what appends a holder. Before `Acquire` there are zero holders, so a reservation succeeds, and `Sweeper.evict` defers `release()` before returning | **Confirmed.** With registration moved above `Acquire`, a sweep can reserve, `client -d`, `os.RemoveAll`, `reg.Remove`, `reg.Save`, `OnEvictedCB` and release, all before the post-Acquire re-check reads `p.evicting`. |
| The N14 re-assert actually closes it | Traced the window forward. After `ws.Acquire` returns, the holder is in `ws.holders`, so any *subsequent* `ReserveForEvict` or `EvictWorkspace` fails its holder check. A reservation taken *before* `Acquire` is either still held (the re-check sees it and Prepare backs out) or already released (the re-check passes and the entry may be gone) | **Confirmed.** After the re-check passes, no new eviction can start, so an Upsert placed there cannot be undone by a later `reg.Remove`. N14 is sufficient and is correctly placed immediately after the re-check. |
| `README.md` documents stream support without qualification | The `### Source workspaces` section: "**v1 supports Perforce only.**", then the field table's `stream` row ("Perforce stream path. Workspaces are keyed by stream and reused across tasks.") and `sync` row | **Confirmed.** No qualification on stream TYPE anywhere. |
| The p4d fixture has exactly one stream and a history of form rejections | `testdata/p4d/entrypoint.sh` creates depot `//test` (with a `StreamDepth` comment recording an r25.x rejection), stream `//test/main` (with a `ParentView` comment recording a second), a `setup-client`, one submitted `readme.txt`, one shelved CL | **Confirmed.** D9's "prove the fixture alone, first" is well-founded. |
| Every symbol the item and spec prescribe exists | `reg.Upsert`, `reg.Save`, `reg.Get`, `reg.Mutate`, `allocateShortID`, `assertCwdContract`, `prepareAcquireHook`, `gatingRunner`, `Provider.ReserveForEvict`, `Sweeper.SweepOnce`, `Provider.LockedShortIDs`, `Provider.InvalidateWorkspace` | **Confirmed**, with one exception in the refutations below. |
| `ResolveHead` and `CreateStreamClient` have no callers outside the package | Searched the tree for both identifiers: only `client.go`, `perforce.go`, `client_test.go`, `client_error_test.go` and docs | **Confirmed.** The signature widening is package-local. |

### REFUTED 1 - the spec names a test that does not exist

**Spec R4 says:** *"`TestClient_ResolveHeadError` re-keys the same way."*

**There is no `TestClient_ResolveHeadError`.** `client_test.go` contains `TestClient_CreateStreamClient_Default`, `TestClient_CreateStreamClient_WithTemplate`, `TestClient_ResolveHead` and **`TestClient_RunFailureBubbles`** - the last is the one that drives `ResolveHead` through `fr.setErr` and is what the spec meant. Task 6 re-keys `TestClient_RunFailureBubbles`. Do not go looking for the name the spec used.

### REFUTED 2 - the spec contradicts itself on when N14 lands

**Spec R5 says:** *"Apply N3-N9 and N14 and re-key every fixture."*
**Spec section 10 says:** *"R6, R6b and R7 are written before N14 exists, so R7 has a real RED against a real un-guarded tree rather than a mutation."*

These cannot both hold. If R5 applies N14, R7 has no un-guarded tree to redden against and its RED degrades to a mutation.

**Resolution, and it is load-bearing for the task order below:** the reorder task (Task 5 and Task 6) applies **N3 through N9 only**. N14 is its own task (Task 7), preceded by writing its guard against the reordered, unguarded tree. Section 10's intent wins because it is the one that produces real evidence.

### REFUTED 3 - R6b is not "green at HEAD"

**Spec section 5 says:** *"R6b ... is green at HEAD, vacuously, because HEAD creates nothing."*

**Not as it will actually be written.** R6b drives a failed `Prepare` through the same fixture as R6. At HEAD the fake runner has no fixture for the head-resolution key that R6 registers, so `fakeRunner.Run` calls `t.Errorf("fakeRunner.Run: no fixture for args %q ...")` and the test is RED at HEAD for a bookkeeping reason that has nothing to do with its subject. "Green at HEAD" is only true of an idealised version nobody will type.

**Resolution:** R6b is written inside Task 5, alongside the reorder, where it is genuinely green the moment the reorder lands. It is labelled a regression guard, never a red-first criterion, and Task 5 **proves it is not vacuous with an explicit mutation** (delete the `reg.Upsert` in N8) rather than asserting that it would be red against a hypothetical naive port.

### REFUTED 4 - deferring the whole prose sweep to one late task ships wrong comments for several commits

**Spec R9** collects the comment rewrites into a single late step. Two of those passages (`assertCwdContract`'s comment naming `changes -m1` as a global invocation, and `perforce_test.go`'s "ResolveHead is the first p4 call inside Prepare") are falsified by Tasks 5 and 6 respectively. Leaving them wrong for the intervening commits is exactly this repo's dominant defect class.

**Resolution:** each comment is rewritten in the commit that falsifies it (named in that task's steps). Task 10 keeps only the README edits plus a final sweep that *verifies* no falsified passage survives. Nothing is dropped; it is redistributed.

### Not refuted, but sharpened - what actually reddens in R7

The spec asserts R7's RED is "the entry is absent". Traced through: without N14, the swept-away entry means `reg.Get(shortID)` returns `curOK == false`, so `needsSync` is `handle.Mode() == ModeExclusive || false`, and the mode for a holder-free workspace with a nil `syncedPaths` and no unshelves is `ModeShared`. **So without N14 the sync never runs at all**, and the test's `-c <client> sync ...` and `-c <client> changes -c <client> -s pending -l` fixtures are exercised only in the GREEN state. Register them anyway: with N14 the restored entry carries `BaselineHash: ""`, `needsSync` becomes true, and a missing fixture would turn the GREEN into a `t.Errorf`. This is stated in Task 7 because it is not deducible from the spec.

### Criteria already true at HEAD - do not count these as evidence

- **`assertCwdContract`'s body staying unchanged.** Its green is evidence for F2 only if the body diff is literally zero lines. State it as "the body was not edited and the three call sites are green", never as "the comment was updated".
- **`baseline_test.go` staying green with no edit.** That is the `BaselineHash`-is-byte-identical criterion. Its value comes entirely from the file having a zero-line diff. If you touch it, the criterion is gone.
- **`TestEvictWorkspace_PrepareRefusedWhileReserved` staying green with no fixture change.** This is the evidence for putting the pre-Acquire eviction check ahead of `MkdirAll`/`CreateStreamClient`. It registers no fixtures at all, so it is only green if `Prepare` refuses before it makes any p4 call. Task 5 measures this deliberately, once, in both directions.

---

## File structure

| File | Action | Responsibility after this slice |
|---|---|---|
| `internal/agent/source/perforce/testdata/p4d/entrypoint.sh` | Modify | Additionally creates a stream whose client view remaps the parent's storage to a subdirectory, so a client-path sync lands a file at a path depot addressing cannot reach |
| `internal/agent/source/perforce/perforce_remap_integration_test.go` | **Create** | `//go:build integration`. The single test that can prove the fix against a real p4d |
| `internal/agent/source/perforce/perforce.go` | Modify | Adds `toClientPath`; `Prepare` reorders N3-N9 and gains the N14 re-assertion; the old `if !found` creation block is gone |
| `internal/agent/source/perforce/perforce_clientpath_test.go` | **Create** | The `toClientPath` table |
| `internal/agent/source/perforce/client.go` | Modify | `ResolveHead` takes `(ctx, cwd, client, path)` and emits `-c <client> changes -m1 <path>#head` from `cwd` |
| `internal/agent/source/perforce/client_test.go` | Modify | Re-keyed `TestClient_ResolveHead` (plus a new cwd assertion) and `TestClient_RunFailureBubbles` |
| `internal/agent/source/perforce/perforce_orphan_test.go` | **Create** | The orphan/registration guard and the no-unregistered-directory guard |
| `internal/agent/source/perforce/perforce_test.go` | Modify | Re-keyed fixtures in four tests; `assertCwdContract`'s comment; the "first p4 call" comment |
| `internal/agent/source/perforce/perforce_progress_test.go` | Modify | Re-keyed `syncFixture` |
| `internal/agent/source/perforce/client_error_test.go` | Modify | Re-keyed fixtures plus the added clause on why the wrap keeps the depot path |
| `internal/agent/source/perforce/provider_evict_recheck_test.go` | Modify | Two new client fixtures |
| `internal/agent/source/perforce/sweeper_claim_test.go` | Modify | Two new client fixtures; the second test in the file |
| `internal/agent/source/perforce/provider_evict_test.go` | **Unchanged** | Its unchanged-ness is the evidence for the N5-before-N6 ordering |
| `internal/agent/source/perforce/baseline_test.go` | **Unchanged** | Its unchanged-ness is the `BaselineHash`-byte-identical criterion |
| `README.md` | Modify | The `stream` field row names the supported stream types and the client-path addressing |

---

## Standing rules for every task

1. **Never `cd D:/dev/relay`.** Work only in `D:/dev/relay/.claude/worktrees/pr-merging-session-65b658`.
2. **This is a CRLF repo.** After any programmatic edit to a tracked text file, check the diffstat against the size of the change you intended, run `git ls-files --eol` on the touched paths and require `i/lf`, and confirm the file still decodes as UTF-8. `gofmt -l` is useless as a signal here.
3. **`entrypoint.sh` is the one file where a line-ending mistake is executed rather than reported.** `.gitattributes` carries `*.sh text eol=lf`, so its working copy must be LF too - Docker's build context reads the working tree, and `bash` inside a Linux container will fail on `\r` with a confusing p4d error. Task 1 has an explicit byte check.
4. **Cite tests by symbol in comments, never by line number.** No dates, no counts, no history, no completeness claims about other code.
5. **Every test body sketched in this plan is a GUESS.** The plan author did not compile or run any of it. Fixture helper names (`newFakeP4Fixture`, `fr.set`, `fr.setErr`, `fr.setStream`, `expectedClientName`, `gatingRunner`, `assertCwdContract`, `startP4dContainer`) were read off the tree and are real, but the exact assertions, imports and struct literals must be checked against the real helpers before you trust them. Where a sketch and the tree disagree, the tree wins - and say so in the task report.
6. **Commit with an explicit pathspec** (`git add <exact files>`), never `git add -A`.

---

## Task 0: baseline, both ways

**Files:** none.

- [ ] **Step 1: Record the default lane at HEAD**

Run: `go test ./internal/agent/source/perforce/... -count=1 -timeout 300s`
Expected: `ok  	relay/internal/agent/source/perforce`. Record the literal line.

- [ ] **Step 2: Record the integration lane at HEAD**

Requires Docker running and `p4` on PATH.

Run: `go test -tags integration -p 1 ./internal/agent/source/perforce/... -count=1 -v -timeout 1800s`
Expected: `--- PASS: TestPerforce_E2E_SyncAndUnshelve`.

**A SKIP IS NOT A GREEN.** `startP4dContainer` calls `t.Skip("p4 client binary required on PATH")` when `p4` is missing and `t.Skipf("Docker required: ...")` when the daemon is unreachable, and the package still prints `ok`. Read the `-v` output for the literal `--- PASS:` line. If you see `--- SKIP:`, report "the integration lane did not run" and **stop the whole slice** - Tasks 1, 2 and 8 are the only proof this fix works, and without them the slice must not proceed.

- [ ] **Step 3: Record the race lane at HEAD (optional but preferred)**

Run (Bash tool, from the worktree root):
`MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 golang:1.26 go test -race ./internal/agent/... -count=1 -timeout 900s`
Expected: `ok`. If unavailable, write down that `-race` did not run. Do not substitute `-count=N`.

- [ ] **Step 4: No commit.** Record the three results in the task report.

---

## Task 1: the p4d fixture, ALONE, before any Go change

This is the slice's largest unknown and it is proved in isolation on purpose (spec D9). **Docker plus `p4` on PATH required.**

**Files:**
- Modify: `internal/agent/source/perforce/testdata/p4d/entrypoint.sh`

- [ ] **Step 1: Add the remapped stream to the entrypoint**

Insert this block immediately after the `p4 stream -i` heredoc that creates `//test/main` and before the `WORKDIR=$(mktemp -d)` line. The `Remapped:` field is what makes the client view place the parent's storage under `sub/`.

```bash
echo "[entrypoint] creating virtual stream //test/virt (remaps parent storage under sub/) ..."
# A virtual stream has no depot storage of its own: its client view resolves
# through the parent, and Remapped moves that view under sub/. That is the whole
# point of the fixture - //test/virt/... addresses nothing, //<client>/... does.
p4 stream -i <<'EOF'
Stream:      //test/virt
Owner:       perforce
Name:        virt
Parent:      //test/main
Type:        virtual
ParentView:  inherit
Paths:       share ...
Remapped:    ... sub/...
EOF
```

- [ ] **Step 2: Assert the file is still LF and still UTF-8**

Run (PowerShell):
```powershell
$p = "internal/agent/source/perforce/testdata/p4d/entrypoint.sh"
(Get-Content -Raw -Encoding Byte $p | Where-Object { $_ -eq 13 } | Measure-Object).Count
git ls-files --eol $p
git diff --stat -- $p
```
Expected: the CR count is `0`; `git ls-files --eol` reports `i/lf` and `w/lf`; the diffstat is roughly `1 file changed, 13 insertions(+)`. If the CR count is non-zero, revert and re-apply with an editor tool that preserves LF - do not commit.

- [ ] **Step 3: Rebuild the image and run the EXISTING integration test, with no Go change**

Run: `go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -count=1 -v -timeout 1800s`

Expected: `--- PASS: TestPerforce_E2E_SyncAndUnshelve`. The mainline stream is untouched by the fixture change, so this proves only that p4d **accepted the new stream form** - if p4d rejects it, `set -euo pipefail` makes the entrypoint exit non-zero, `wait.ForLog("p4d ready")` never fires, and the container start fails with a startup timeout after two minutes.

- [ ] **Step 4: If p4d rejected the form, walk this fallback ladder in order. Do not "investigate".**

Read the container log first (`docker logs` on the failed container, or re-run with testcontainers' logs surfaced) and find the p4d rejection line. Then:

1. **Ask the server for its own template instead of hand-writing one.** This is the first fallback because the recorded failures in this file are both "r25.x requires a field the draft omitted", and a server-generated skeleton cannot omit one. Replace the heredoc with:
   ```bash
   p4 stream -o -t virtual -P //test/main //test/virt \
     | sed -e 's|^Paths:.*|Paths:\tshare ...|' \
     > /tmp/virt.spec
   printf 'Remapped:\t... sub/...\n' >> /tmp/virt.spec
   p4 stream -i < /tmp/virt.spec
   ```
2. **Switch to the `import+` remap on a development stream** (the bug title names both forms, and either proves the fix). Replace the block with:
   ```bash
   echo "[entrypoint] creating development stream //test/dev (import+ remap of the parent) ..."
   p4 stream -i <<'EOF'
   Stream:      //test/dev
   Owner:       perforce
   Name:        dev
   Parent:      //test/main
   Type:        development
   ParentView:  inherit
   Paths:       share ...
                import+ vendor/... //test/main/...
   EOF
   ```
   The stream is never populated, so `//test/dev/...` has no storage and depot addressing still resolves nothing, while the client view carries `//test/main/... -> //<client>/vendor/...`. **If you take this branch, every occurrence of `//test/virt` becomes `//test/dev` and every occurrence of `sub/readme.txt` becomes `vendor/readme.txt` in Tasks 2 and 8.** Say so in the task report so the next task does not use the wrong path.
3. **Downgrade `import+` to plain `import`.** The view generation is identical; only submit semantics differ, and no test in this slice submits.
4. **If all three are rejected, STOP the slice and report.** Do not proceed to the Go change on the strength of fake-runner tests: the fake runner echoes whatever it is told, so without a real p4d there is no evidence the fix works at all. This is a genuine stop, not a formality.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/testdata/p4d/entrypoint.sh
git commit -m "test(p4d): add a remapped stream to the p4d fixture

The client view maps the parent's storage under a subdirectory, so a file
synced through the client view lands somewhere depot addressing cannot reach.
Landed and proven alone, before any Go change: this entrypoint has twice been
rejected by p4d for a version-specific missing field, and discovering that
after a reorder would cost a round with an ambiguous cause.

Records which form p4d accepted: <virtual with Remapped | server-generated
template | import+ on a development stream | import>.
TestPerforce_E2E_SyncAndUnshelve is green on the rebuilt image, so a plain
mainline stream is unaffected."
```

Fill in the bracketed form. Do not leave it as written.

---

## Task 2: the p4d RED, still with no Go change

**Docker plus `p4` on PATH required.** This is the only test that can prove the bug exists.

**Files:**
- Create: `internal/agent/source/perforce/perforce_remap_integration_test.go`

The spec suggests extending `perforce_integration_test.go`. A separate file is used instead: the new test shares no state with the existing one, and `feature-2026-09-03-p4-sync-progress-heartbeat` is queued against the same call site, so a separate file keeps the two rebases apart.

- [ ] **Step 1: Write the failing test**

**Sketch - check every helper against the tree before trusting it.** The env-isolation block is copied verbatim from `TestPerforce_E2E_SyncAndUnshelve`; it exists because a developer's persisted `p4 set` values otherwise leak into the agent's subprocesses.

```go
//go:build integration

package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// A stream whose client view remaps its parent's storage has no depot storage
// under its own name, so addressing p4 by the stream-name depot path resolves
// nothing. The remapped on-disk path below is reachable only through the client
// view, which is why it is the assertion: a fake runner echoes whatever it is
// told and can say nothing about this.
func TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout(t *testing.T) {
	p4d := startP4dContainer(t)
	t.Setenv("P4PORT", p4d.P4Port)
	t.Setenv("P4USER", p4d.P4User)
	t.Setenv("P4CHARSET", "none")
	t.Setenv("P4CONFIG", "")
	t.Setenv("P4PASSWD", "")
	t.Setenv("P4TICKETS", "")

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})

	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//test/virt",
			Sync:   []*relayv1.SyncEntry{{Path: "//test/virt/...", Rev: "#head"}},
		},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	h, err := prov.Prepare(ctx, "task-remap", spec, func(s string) { t.Logf("prepare-progress: %s", s) })
	require.NoError(t, err, "Prepare must succeed against a remapped stream")
	defer h.Finalize(ctx)

	inv := h.Inventory()
	wsDir := filepath.Join(root, inv.ShortID)
	b, err := os.ReadFile(filepath.Join(wsDir, "sub", "readme.txt"))
	require.NoError(t, err, "the baseline file must land at the REMAPPED path under the workspace root")
	require.Equal(t, "baseline\n", string(b))
}
```

If Task 1 took fallback 2 or 3, the stream is `//test/dev`, the sync path is `//test/dev/...`, and the file is at `vendor/readme.txt`.

- [ ] **Step 2: Run it at HEAD and RECORD the failure text**

Run: `go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout -count=1 -v -timeout 1800s`

Expected: FAIL at the `require.NoError(t, err, "Prepare must succeed...")` line.

**Do not assert the message; record it.** The backlog item quotes `file(s) not in client view`, which is the message for a sync spec with a literal rev reaching `SyncStream`. This test uses `rev: "#head"`, which fails one step earlier inside `ResolveHead`: `p4 changes -m1 //test/virt/...#head` against a path with no depot storage prints no `Change ` line, so `ResolveHead` returns `could not parse ""` and `Prepare` wraps it `resolve head for //test/virt/...`. Either message proves the bug. Copy the literal `Error:` block from the `-v` output into the task report; it goes into the PR body and the commit message.

- [ ] **Step 3: STOP GATE. If the test PASSES at HEAD, stop the slice.**

A pass means either the fixture is not actually remapping (check the generated client view with `p4 -c <client> client -o` against the container) or the bug is not what the item says. Either way nothing after this point is justified. Report and stop.

- [ ] **Step 4: Commit the RED test**

This is a deliberate exception to "commit only green". The spec makes this test a gate that must exist as an artifact before any Go change, and it lives behind `//go:build integration`, so `make test` and the default lane stay green throughout. **The branch must not be merged before Task 8.**

```bash
git add internal/agent/source/perforce/perforce_remap_integration_test.go
git commit -m "test(p4d): RED - a remapped stream fails at prepare

Reproduces the bug against a real p4d. Committed RED, ahead of the fix, because
it is the only proof the bug exists - the fake runner echoes whatever it is told
- and because 'does it already pass at HEAD?' is a stop gate for the whole
change. Goes green in the commit that addresses p4 by client path.

Observed failure at HEAD:
  <paste the literal Error: block here>

Note the failure is NOT the 'file(s) not in client view' the backlog item
quotes: that is the message for a literal rev reaching p4 sync. A '#head' rev
fails one step earlier, inside head resolution."
```

---

## Task 3: `toClientPath`

Default lane. No Docker.

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go` (add the function beside `allocateShortID`)
- Create: `internal/agent/source/perforce/perforce_clientpath_test.go`

- [ ] **Step 1: Add the STUB that supplies the RED**

A test for a function that does not exist fails to COMPILE, and a compile failure is not evidence about behaviour. So the RED is taken against a stub that implements the design this slice explicitly declines - silently emitting the depot path unchanged, which is today's bug. Its death is the refutation.

Add to `perforce.go`, immediately above `allocateShortID`:

```go
func toClientPath(clientName, stream, depotPath string) (string, error) {
	return depotPath, nil
}
```

- [ ] **Step 2: Write the failing test**

**Sketch - check it compiles and that the expectations are not derived from the function under test.** Every `want` below is written out literally on purpose: an expectation computed from `toClientPath`'s own inputs by the same rule would move with the thing under test.

```go
package perforce

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The prefix test is stream+"/" and not stream. The sharesATextualPrefix row is
// what goes RED against HasPrefix(depotPath, stream): //depot/xy is not under
// //depot/x, and rewriting it would synthesize a client path the operator never
// wrote. The error rows exist because the jobspec validator that establishes
// this precondition runs in the coordinator process and this function runs in
// the agent's - a single enforcement point across a process boundary is prose.
func TestToClientPath(t *testing.T) {
	const client = "relay_h_ab12cd"
	for _, tc := range []struct {
		name    string
		stream  string
		path    string
		want    string
		wantErr bool
	}{
		{"pathEqualsTheStream", "//s/x", "//s/x", "//relay_h_ab12cd/...", false},
		{"streamWildcard", "//s/x", "//s/x/...", "//relay_h_ab12cd/...", false},
		{"strictlyUnderTheStream", "//s/x", "//s/x/sub/dir/...", "//relay_h_ab12cd/sub/dir/...", false},
		{"notUnderTheStreamAtAll", "//s/x", "//other/y/...", "", true},
		{"sharesATextualPrefixButIsNotUnder", "//depot/x", "//depot/xy/...", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toClientPath(client, tc.stream, tc.path)
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.path, "the error must name the offending path")
				require.Contains(t, err.Error(), tc.stream, "and the stream it was measured against")
				require.Empty(t, got, "a refused path must not also return a value")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
```

- [ ] **Step 3: Run it and verify all five rows fail**

Run: `go test ./internal/agent/source/perforce/... -run TestToClientPath -count=1 -v -timeout 60s`

Expected FAILs, all five:
- `pathEqualsTheStream`: `Not equal: expected: "//relay_h_ab12cd/..." actual: "//s/x"`
- `streamWildcard`: `expected: "//relay_h_ab12cd/..." actual: "//s/x/..."`
- `strictlyUnderTheStream`: `expected: "//relay_h_ab12cd/sub/dir/..." actual: "//s/x/sub/dir/..."`
- `notUnderTheStreamAtAll`: `Error:  An error is expected but got nil.`
- `sharesATextualPrefixButIsNotUnder`: `Error:  An error is expected but got nil.`

Record all five. **Five distinct failures, not one** - if fewer than five fail, the table is not discriminating and you must find out why before implementing.

- [ ] **Step 4: Replace the stub with the implementation**

```go
// toClientPath rewrites a depot-form sync path into client syntax so p4 resolves
// it through the client's view. A virtual or import+ remap stream has no depot
// storage under the stream name, so the depot form addresses nothing. The spec
// this receives has passed jobspec.validateSourceSpec, which requires the path
// to equal the stream or sit under it; a path that does not is refused rather
// than rewritten, because emitting it unchanged is the defect this exists to
// fix and synthesizing a tail the operator never wrote is worse.
// TestToClientPath's sharesATextualPrefixButIsNotUnder row is why the prefix
// test is stream+"/".
func toClientPath(clientName, stream, depotPath string) (string, error) {
	if depotPath == stream {
		// //<client> with no wildcard names nothing, so an empty remainder maps
		// to the whole client. This is a behaviour change for a spec whose path
		// equals its stream: p4 read the old //s/x@12345 as a single file and
		// synced nothing.
		return "//" + clientName + "/...", nil
	}
	if strings.HasPrefix(depotPath, stream+"/") {
		return "//" + clientName + depotPath[len(stream):], nil
	}
	return "", fmt.Errorf("sync path %q is not under stream %q; this spec did not come through jobspec validation", depotPath, stream)
}
```

`strings` and `fmt` are already imported by `perforce.go`.

- [ ] **Step 5: Run it and verify it passes**

Run: `go test ./internal/agent/source/perforce/... -run TestToClientPath -count=1 -v -timeout 60s`
Expected: `--- PASS` on all five subtests.

- [ ] **Step 6: Run the whole default lane**

Run: `go test ./internal/agent/source/perforce/... -count=1 -timeout 300s`
Expected: `ok`. Nothing calls `toClientPath` yet, so nothing else can move.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_clientpath_test.go
git commit -m "feat(perforce): add toClientPath, the depot-to-client seam

One place where depot-to-client translation lives, so the queued exclusion-path
design has a single seam to extend. Not wired into Prepare yet.

The empty-remainder case is a deliberate behaviour change for a spec whose
sync path equals its stream: p4 read the old //s/x@12345 as a single file with
no wildcard and synced nothing. Such a workspace's baseline is unchanged, so it
does not re-sync on upgrade and stays under-synced until another input to the
hash moves. Recorded rather than mitigated: forcing a re-sync means changing
BaselineHash, which the coordinator also computes and which would re-sync every
warm workspace in the fleet.

RED was taken against a stub returning the depot path unchanged - the silent
passthrough this design declines - so every row reddens for a behavioural
reason rather than a compile error."
```

---

## Task 4: the orphan and no-unregistered-directory guards, written at the OLD argv

Default lane. This task writes the two guards and takes their honest RED **before** the reorder. It does not change production code.

**Files:**
- Create: `internal/agent/source/perforce/perforce_orphan_test.go`

- [ ] **Step 1: Write both guards, keyed to the CURRENT argv**

The fixture keys here are the **HEAD** forms (`changes -m1 //s/x/...#head`, no `-c`, and depot-form sync specs). They are re-keyed in Task 6 when the argv changes. Keying them to HEAD is what makes this task's RED clean - a new-form key would produce a `fakeRunner: no fixture` `t.Errorf` that has nothing to do with the subject.

**Sketch - verify `Sweeper`'s field names and `reg.Mutate`'s signature against the tree.**

```go
package perforce

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// prepareFailingAtHeadResolution drives one Prepare that creates the workspace
// and then fails resolving head, and returns the provider, its registry, the
// workspace root, the short id and the client name. Head resolution is where
// the injection goes because it is the first call after creation that carries a
// job-supplied path, so it is the realistic first-use failure.
func prepareFailingAtHeadResolution(t *testing.T) (*Provider, *Registry, string, string, string) {
	t.Helper()
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	client := expectedClientName("h", "//s/x")
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("client -d "+client, "Client deleted.\n")
	fr.setErr("changes -m1 //s/x/...#head", errors.New("no such file(s)."))

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	_, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.Error(t, err, "the injected head-resolution failure must fail Prepare")

	reg, regErr := p.Registry()
	require.NoError(t, regErr)
	shortID := allocateShortID("//s/x", &Registry{})
	return p, reg, root, shortID, client
}

// The three assertions are ORDERED so the one that fails names which tree you
// are looking at: assertion 2 fails where nothing is created before head
// resolution, assertion 3 fails where creation happens without registration,
// and assertion 4 fails where a registered workspace is not reclaimable.
func TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace(t *testing.T) {
	p, reg, root, shortID, client := prepareFailingAtHeadResolution(t)

	require.DirExists(t, filepath.Join(root, shortID),
		"the workspace directory must exist after the failed Prepare")

	e, ok := reg.Get(shortID)
	require.True(t, ok, "and the registry must have an entry for it")
	require.Equal(t, "", e.BaselineHash, "registered before any sync, so no baseline")
	require.Equal(t, client, e.ClientName)

	require.NoError(t, reg.Mutate(shortID, func(w *WorkspaceEntry) {
		w.LastUsedAt = time.Now().Add(-30 * 24 * time.Hour)
	}))
	sw := &Sweeper{
		Root:        root,
		Reg:         reg,
		MaxAge:      14 * 24 * time.Hour,
		Client:      p.Client(),
		ListLocked:  p.LockedShortIDs,
		Claim:       p.ReserveForEvict,
		OnEvictedCB: p.InvalidateWorkspace,
	}
	evicted, err := sw.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{shortID}, evicted, "a configured sweeper must reclaim it")
	require.NoDirExists(t, filepath.Join(root, shortID))
}

// The acceptance criterion itself, and the only assertion in the slice whose
// subject is that criterion verbatim. It is a regression guard, not a red-first
// criterion: it is satisfied where nothing is created at all as readily as where
// everything created is registered. Deleting the registration in Prepare is what
// makes it fail, and that mutation is what proves it is not vacuous.
func TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory(t *testing.T) {
	_, reg, root, _, _ := prepareFailingAtHeadResolution(t)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		_, ok := reg.Get(d.Name())
		require.Truef(t, ok, "directory %q under the workspace root has no registry entry", d.Name())
	}
}
```

- [ ] **Step 2: Run both and record the RED**

Run: `go test ./internal/agent/source/perforce/... -run "TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace|TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory" -count=1 -v -timeout 60s`

Expected at HEAD:
- `TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace`: **FAIL**, aborting at `require.DirExists`, with a message of the shape `unable to find file "<tmp>/<shortid>"`. This is the honest RED: HEAD creates nothing before head resolution, so it has no orphan.
- `TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory`: **PASS**, vacuously. It is labelled a guard in its own comment for exactly this reason.

**Confirm the DirExists line is what aborts, and that there is no `fakeRunner: no fixture` line.** A fixture miss here means the keys are wrong and the RED is measuring the wrong thing.

Write the three-way outcome table into the task report:

| Tree | `AResolveHeadFailure...` fails at | `AFailedPrepare...` |
|---|---|---|
| HEAD | assertion 2, `DirExists` | passes vacuously |
| reorder without registration (the naive port) | assertion 3, `reg.Get` | fails, naming the orphan directory |
| this slice complete | passes | passes |

- [ ] **Step 3: No commit.** These files stay in the working tree and are committed in Task 5 with the code that makes them green. Do not commit a red default-lane test.

---

## Task 5: the `Prepare` reorder (N3-N8), argv unchanged

Default lane. This moves work above the resolve loop and above `ws.Acquire`. **It changes no p4 argv**, so the only fixture changes are the tests that pre-seeded a registry and therefore never needed client-creation fixtures before.

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go`
- Modify: `internal/agent/source/perforce/perforce_test.go` (fixtures in `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` and `TestProvider_Prepare_ClassifiesAuthError`; the "first p4 call" comment)
- Modify: `internal/agent/source/perforce/client_error_test.go` (fixtures)
- Modify: `internal/agent/source/perforce/provider_evict_recheck_test.go` (fixtures)
- Modify: `internal/agent/source/perforce/sweeper_claim_test.go` (fixtures)
- Commit alongside: `internal/agent/source/perforce/perforce_orphan_test.go` from Task 4

### What must be true so that no early return here calls `handle.Release()`

The spec warns that a symmetry-seeking reviewer will want to add one. **There is no handle to release between client creation and `ws.Acquire`.** Enumerated, so the property is checkable rather than asserted:

1. **`handle` is introduced by `handle, err := ws.Acquire(ctx, req)` - a short variable declaration at N12.** Do not hoist it to `var handle *WorkspaceHandle` above the new code. With `:=`, any reference to `handle` above that statement in the same block is a compile error (`undefined: handle`), so the compiler enforces this property for free. That is the whole enforcement mechanism; preserve it deliberately.
2. **There is no `defer` that releases the handle, and you must not add one.** On the success path `perforceHandle.Finalize` owns the release; on every failure path after N12 the release is an explicit statement in that arm. Keep it that way.
3. **The four new early-return arms return `(nil, <err>)` and name no handle:** `os.MkdirAll`, `CreateStreamClient`, and (in Task 6) `toClientPath` and `ResolveHead`.
4. **Taking `ws` out of `p.workspaces` is not a hold.** `Workspace.Acquire` is the only thing that appends to `ws.holders`. So there is nothing acquired at N5 to give back: do not call `ws.release`, do not construct a `WorkspaceHandle`, and do not `delete(p.workspaces, shortID)` on a failure path - that last would be `InvalidateWorkspace`'s job and calling it here would clobber a concurrent Prepare's arbitrator, which is the exact shape the identity-checked-teardown invariant forbids.
5. **What replaces the release, since there is no identity to check:** the registry entry written at N8 and restored at N14. Named guards: `TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace`, `TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory` and (Task 7) `TestSweeperClaim_ASweepThatCompletesBetweenRegistrationAndAcquireIsRepaired`.
6. **Reviewer check, one command:** `git grep -n "handle" -- internal/agent/source/perforce/perforce.go` and confirm the first hit inside `Prepare` is the `:=` at `ws.Acquire`.

- [ ] **Step 1: Add the client fixtures the reorder will demand, and run to see the RED**

Creation becomes unconditional and moves above head resolution, so four tests that previously reached neither `client -o` nor `client -i` now do.

In `perforce_test.go`, `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` (the registry is pre-seeded, so `found == true` and it never created a client before), add beside the existing fixtures:
```go
	fr.set("client -o -S //s/x "+clientName, "")
	fr.set("client -i", "Client saved.\n")
```

In `perforce_test.go`, `TestProvider_Prepare_ClassifiesAuthError`, add before the `setErr`:
```go
	expectedClient := expectedClientName("h", "//s/x")
	fr.set("client -o -S //s/x "+expectedClient, "")
	fr.set("client -i", "Client saved.\n")
```

In `client_error_test.go`, `TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified`, add:
```go
	client := expectedClientName("h", "//depot/disk full")
	fr.set("client -o -S //depot/disk full "+client, "")
	fr.set("client -i", "Client saved.\n")
```
Its **assertions stay exactly as they are.** `Prepare` keeps wrapping the error with `e.Path` (the depot path with the space), so the spec path still reaches the error text and the test still discriminates.

In `provider_evict_recheck_test.go` and in `sweeper_claim_test.go`, both of which pre-seed `//depot/main` with `clientName`, add to each:
```go
	fr.set("client -o -S //depot/main "+clientName, "")
	fr.set("client -i", "Client saved.\n")
```

Run: `go test ./internal/agent/source/perforce/... -count=1 -timeout 300s`
Expected: `ok`. Registering a fixture that is never used is not an error in `fakeRunner`, so adding them ahead of the reorder is inert. This step exists so the next step's RED is about the reorder and nothing else.

- [ ] **Step 2: Apply the reorder in `Prepare`**

Delete the whole `// First time: create on-disk dir and p4 client spec.` / `if !found { ... }` block that sits after the post-Acquire eviction re-check, and move `existing, found := reg.GetBySourceKey(...)` through the workspace get-or-create up to just after `reg, err := p.loadRegistry()`. The resulting head of `Prepare`, between the registry load and the resolve loop:

```go
	// Find or allocate a workspace short_id for this stream.
	existing, found := reg.GetBySourceKey(pf.Stream)
	var shortID string
	if found {
		shortID = existing.ShortID
	} else {
		shortID = allocateShortID(pf.Stream, reg)
	}
	wsRoot := filepath.Join(p.cfg.Root, shortID)
	clientName := fmt.Sprintf("relay_%s_%s", p.cfg.Hostname, shortID)

	// Get or create the in-memory Workspace arbitrator.
	// This check runs ahead of the creation below so a workspace we have already
	// been told is going away costs no p4 work and its client -i cannot race the
	// evictor's client -d. Correctness does not depend on that ordering - the
	// post-Acquire re-check is what partitions the destructive window - but
	// TestEvictWorkspace_PrepareRefusedWhileReserved registers no p4 fixtures at
	// all, so it goes red the moment any p4 call moves ahead of this.
	p.mu.Lock()
	if p.evicting[shortID] {
		p.mu.Unlock()
		return nil, fmt.Errorf("perforce: workspace %s is being evicted", shortID)
	}
	ws, ok := p.workspaces[shortID]
	if !ok {
		ws = NewWorkspace(shortID)
		p.workspaces[shortID] = ws
	}
	p.mu.Unlock()

	// The p4 client must exist before any client-scoped call, and head resolution
	// becomes one: a virtual or import+ remap stream has no depot storage under
	// the stream name. So creation moves above the resolve loop and runs on every
	// Prepare, which is also what repairs a half-built workspace on a later
	// attempt. Registration goes with creation because nothing else records that
	// the directory and client spec exist; a Prepare that fails between here and
	// ws.Acquire holds no workspace handle and must not try to release one.
	// TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace and
	// TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory.
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		return nil, err
	}
	tmpl := ""
	if pf.ClientTemplate != nil {
		tmpl = *pf.ClientTemplate
	}
	if err := p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl); err != nil {
		return nil, classifyP4Error(fmt.Errorf("create client: %w", err))
	}
	// The !found guard stays. An unconditional Upsert replaces the whole struct,
	// dropping another task's OpenTaskChangelists and resetting BaselineHash to
	// "", which re-syncs every warm workspace on upgrade. Registry.Mutate is the
	// sanctioned way to edit an entry in place.
	if !found {
		reg.Upsert(WorkspaceEntry{
			ShortID:      shortID,
			SourceKey:    pf.Stream,
			ClientName:   clientName,
			BaselineHash: "",
			LastUsedAt:   time.Now(),
		})
		_ = reg.Save()
	}
```

The resolve loop, `baseline := BaselineHash(pf, resolved)`, `prepareAcquireHook`, the `Request` literal and `ws.Acquire` follow, in that order, otherwise unchanged. The post-Acquire re-check block and its comment stay exactly where they are and exactly as they are.

- [ ] **Step 3: Run the whole default lane**

Run: `go test ./internal/agent/source/perforce/... -count=1 -v -timeout 300s`

Expected: PASS everywhere, including both Task 4 guards - `TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace` now gets past `DirExists`, finds the registry entry, and the sweeper reclaims both. If `TestProvider_PrepareCreatesClientAndSyncs`'s `assertCwdContract` fails, you have moved a `-c` call to an empty cwd or a `client` call to a non-empty one - fix the call, not the assertion.

- [ ] **Step 4: Rewrite the comment this task falsifies**

In `perforce_test.go`, `TestProvider_Prepare_ClassifiesAuthError` opens with "ResolveHead is the first p4 call inside Prepare." That is now false. Replace with the reason the injection point is still the right one:

```go
	// Head resolution is the first p4 call carrying a job-supplied path, which is
	// what makes it the realistic place for a ticket failure to surface. Inject
	// the canonical "ticket invalid" stderr that execRunner would surface in
	// production.
```

- [ ] **Step 5: Prove the N5-before-N6 ordering deliberately, once, then revert**

Move the `p.mu` eviction pre-check block to sit *after* the `CreateStreamClient` call.

Run: `go test ./internal/agent/source/perforce/... -run TestEvictWorkspace_PrepareRefusedWhileReserved -count=1 -v -timeout 60s`
Expected: **FAIL**, with `fakeRunner.Run: no fixture for args "client -o -S //depot/main relay_host_<shortid>" (cwd="")` - the test registers no fixtures at all, so any p4 call ahead of the refusal reddens it.

**Restore by re-applying the edit in reverse - do NOT `git checkout --` the file**, which would discard the uncommitted reorder this task just wrote. Re-run the same command and confirm PASS, then re-run the whole lane.

- [ ] **Step 6: Prove the registration guards are not vacuous**

Delete the `reg.Upsert(WorkspaceEntry{...})` and `_ = reg.Save()` inside the `if !found` block (leave `MkdirAll` and `CreateStreamClient` in place). This is the naive port.

Run: `go test ./internal/agent/source/perforce/... -run "TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace|TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory" -count=1 -v -timeout 60s`

Expected: **both FAIL**, and each for its own guard -
- `AResolveHeadFailure...` at `require.True(t, ok, "and the registry must have an entry for it")`,
- `AFailedPrepare...` at `directory "<shortid>" under the workspace root has no registry entry`.

Both dying is expected and is not a sign the mutation is too coarse: they pin the same production statement from two directions (one names what should be there, one names what must not be left behind). Record both failing lines. **Restore by re-applying the deleted lines, not by `git checkout`**, and re-run to green.

- [ ] **Step 7: Verify line endings and commit**

```powershell
git diff --stat
git ls-files --eol internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go internal/agent/source/perforce/client_error_test.go internal/agent/source/perforce/provider_evict_recheck_test.go internal/agent/source/perforce/sweeper_claim_test.go internal/agent/source/perforce/perforce_orphan_test.go
```
Every path must read `i/lf`. The diffstat should be on the order of 60-100 changed lines across six files plus the new test file; a diffstat an order of magnitude larger means a line-ending rewrite.

```bash
git add internal/agent/source/perforce/perforce.go \
        internal/agent/source/perforce/perforce_test.go \
        internal/agent/source/perforce/client_error_test.go \
        internal/agent/source/perforce/provider_evict_recheck_test.go \
        internal/agent/source/perforce/sweeper_claim_test.go \
        internal/agent/source/perforce/perforce_orphan_test.go
git commit -m "refactor(perforce): create and register the workspace before head resolution

Head resolution is about to become a client-scoped call, so the p4 client has to
exist before it. That breaks a genuine cycle: ws.Acquire takes the baseline hash,
the hash needs the resolved revisions, and resolving a revision on a remap stream
needs the client. The cycle is broken by moving creation out of the !found branch
and above the acquire, not by moving the acquire.

Two consequences, deliberate:

CreateStreamClient now runs on every Prepare rather than on first use. The
alternative - skip client -i when the fetched spec already looks right - was
declined: a false positive silently disables repair and the symptom is a
workspace syncing to the wrong root with no error anywhere. The unconditional
path is also what heals a half-built workspace on the next attempt. Cost is two
metadata round trips per task against a sync measured in minutes to hours; under
contention N queued Prepares pay 2N up front instead of 2 in total.

Registration moves with creation, and the !found guard is KEPT. An unconditional
Upsert would replace the whole struct, dropping another task's
OpenTaskChangelists and resetting BaselineHash to \"\", which re-syncs every warm
workspace in the fleet on upgrade.

A Prepare that now fails between creation and ws.Acquire holds no workspace
handle, so it releases nothing; there is no identity to check there, and the
registry entry is what replaces it. What makes that entry useful does not depend
on the sweeper, which is off unless RELAY_WORKSPACE_MAX_AGE or
RELAY_WORKSPACE_MIN_FREE_GB is set: the next Prepare for the stream reuses the
short id and repairs the half-built workspace, and ListInventory now reports it
so an operator can evict it by hand - EvictWorkspace previously refused with
'not found in registry'.

Measured: with the registration deleted, both new guards go red on their own
assertions. With the eviction pre-check moved below client creation,
TestEvictWorkspace_PrepareRefusedWhileReserved goes red demanding fixtures it
does not register - which is why the pre-check stays first."
```

---

## Task 6: `ResolveHead` becomes client-scoped, and the sync specs become client-form

Default lane. This is the fix. The argv changes on two calls.

**Files:**
- Modify: `internal/agent/source/perforce/client.go`
- Modify: `internal/agent/source/perforce/perforce.go` (the resolve loop, N9)
- Modify: `internal/agent/source/perforce/client_test.go`
- Modify: `internal/agent/source/perforce/perforce_test.go` (three fixture blocks, plus `assertCwdContract`'s comment)
- Modify: `internal/agent/source/perforce/perforce_progress_test.go` (`syncFixture`)
- Modify: `internal/agent/source/perforce/client_error_test.go` (fixture + comment)
- Modify: `internal/agent/source/perforce/perforce_orphan_test.go` (fixture)
- Modify: `internal/agent/source/perforce/provider_evict_recheck_test.go`, `sweeper_claim_test.go` (sync fixture keys)

- [ ] **Step 1: Re-key `client_test.go` and add the assertion the file does not make today**

Replace `TestClient_ResolveHead` and `TestClient_RunFailureBubbles`:

```go
func TestClient_ResolveHead(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.set("-c relay_h_abc changes -m1 //relay_h_abc/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	c := &Client{r: fr}
	cl, err := c.ResolveHead(context.Background(), `D:\rw\abcdef`, "relay_h_abc", "//relay_h_abc/...")
	require.NoError(t, err)
	require.Equal(t, int64(12345), cl)
	// The cwd is not required by p4 - the global -c pins the client server-side -
	// so nothing else would notice a future edit dropping it, and the package's
	// cwd contract (assertCwdContract) would then be false for this call.
	require.Len(t, fr.calls, 1)
	require.Equal(t, `D:\rw\abcdef`, fr.calls[0].cwd)
}

func TestClient_RunFailureBubbles(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.setErr("-c relay_h_abc changes -m1 //relay_h_abc/...#head",
		errors.New("Perforce password (P4PASSWD) invalid or unset."))
	c := &Client{r: fr}
	_, err := c.ResolveHead(context.Background(), `D:\rw\abcdef`, "relay_h_abc", "//relay_h_abc/...")
	require.ErrorContains(t, err, "P4PASSWD")
}
```

- [ ] **Step 2: Run and record the RED**

Run: `go test ./internal/agent/source/perforce/... -run "TestClient_ResolveHead|TestClient_RunFailureBubbles" -count=1 -v -timeout 60s`

Expected: a **compile failure** first - `too many arguments in call to c.ResolveHead`. That is a compile error, not behavioural evidence, so widen the signature in Step 3 and then take the behavioural RED in Step 4.

- [ ] **Step 3: Widen the signature only, leaving the body at HEAD's behaviour**

```go
func (c *Client) ResolveHead(ctx context.Context, cwd, client, path string) (int64, error) {
	out, err := c.r.Run(ctx, "", []string{"changes", "-m1", path + "#head"}, nil)
```

and at the single call site in `Prepare`'s resolve loop, pass the values that now exist above it:

```go
			cl, err := p.cfg.Client.ResolveHead(ctx, wsRoot, clientName, e.Path)
```

- [ ] **Step 4: Run and record the BEHAVIOURAL RED**

Run: `go test ./internal/agent/source/perforce/... -run "TestClient_ResolveHead|TestClient_RunFailureBubbles" -count=1 -v -timeout 60s`

Expected FAIL:
```
fakeRunner.Run: no fixture for args "changes -m1 //relay_h_abc/...#head" (cwd="")
```
plus, in `TestClient_ResolveHead`, the `require.Equal` on `fr.calls[0].cwd` (or a `fr.calls` length failure, since `fakeRunner.Run` records a call only on the fixture-hit path). Record the literal lines.

- [ ] **Step 5: Fix the body**

```go
// ResolveHead resolves a path to its head CL number via `p4 -c <client> changes
// -m1`, run from cwd. The path is client syntax and the -c flag is what makes it
// resolvable: a virtual or import+ remap stream has no depot storage under the
// stream name, so only the client's view can address it. The cwd is not required
// by p4 and is passed for the package's cwd contract and for .p4config
// predictability; TestClient_ResolveHead pins it.
func (c *Client) ResolveHead(ctx context.Context, cwd, client, path string) (int64, error) {
	out, err := c.r.Run(ctx, cwd, []string{"-c", client, "changes", "-m1", path + "#head"}, nil)
	if err != nil {
		return 0, err
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	m := changeFirstLine.FindStringSubmatch(line)
	if m == nil {
		return 0, fmt.Errorf("could not parse %q", line)
	}
	return strconv.ParseInt(m[1], 10, 64)
}
```

The parse and both non-`p4CommandError` returns are unchanged; `TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified` depends on them.

Run: `go test ./internal/agent/source/perforce/... -run "TestClient_ResolveHead|TestClient_RunFailureBubbles" -count=1 -v -timeout 60s`
Expected: PASS.

- [ ] **Step 6: Wire `toClientPath` into the resolve loop**

Replace the resolve loop body in `Prepare`:

```go
	// resolved and syncPaths stay keyed on the DEPOT path. BaselineHash is a
	// cross-process contract: scheduler.BaselineHashFromAPISpec computes the same
	// function server-side to score warm-workspace affinity, and the coordinator
	// cannot know this agent's hostname or allocated short id, both of which feed
	// clientName. Only syncSpecs - the p4 argv - becomes client-form.
	resolved := make(map[string]string, len(pf.Sync))
	syncSpecs := make([]string, 0, len(pf.Sync))
	syncPaths := make([]string, 0, len(pf.Sync))
	for _, e := range pf.Sync {
		cp, err := toClientPath(clientName, pf.Stream, e.Path)
		if err != nil {
			return nil, err
		}
		rev := e.Rev
		if rev == "#head" {
			cl, err := p.cfg.Client.ResolveHead(ctx, wsRoot, clientName, cp)
			if err != nil {
				// The wrap names the DEPOT path: it is what the operator wrote and
				// can act on, and it is now the only place the job's own path
				// reaches the error, because the argv no longer carries it.
				// TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified.
				return nil, classifyP4Error(fmt.Errorf("resolve head for %s: %w", e.Path, err))
			}
			rev = fmt.Sprintf("@%d", cl)
			resolved[e.Path] = rev
		}
		syncSpecs = append(syncSpecs, cp+rev)
		syncPaths = append(syncPaths, e.Path)
	}
```

`Request.SyncPaths` stays depot-form. It feeds only `PathPrefixOverlap` through `tryAdmit`, `modeForEmptyWorkspace` and `release`, all of which compare paths belonging to holders of one `Workspace` - one short id, one source key, one stream - so every path in every comparison shares the same constant prefix and the prefix cancels out of `HasPrefix`. Depot and client form give identical answers. There is a second reason not to move it: `Workspace.syncedPaths` is in-memory and keyed on `SyncPaths`, so changing the key form mid-process would make every existing entry stop matching and silently re-classify a warm workspace as never-synced.

- [ ] **Step 7: Re-key the remaining fixtures**

Everywhere below, `<c>` is that test's client name (`expectedClient`, `clientName`, or `client`, per the local variable already in scope).

| File and test | Old key | New key |
|---|---|---|
| `perforce_test.go` `TestProvider_PrepareCreatesClientAndSyncs` | `changes -m1 //s/x/...#head` | `-c <c> changes -m1 //<c>/...#head` |
| same | `-c <c> sync -q --parallel=4 //s/x/...@12345` | `-c <c> sync -q --parallel=4 //<c>/...@12345` |
| `perforce_test.go` `TestProvider_UnshelveAndFinalizeRevert` | both of the above | both of the above |
| `perforce_test.go` `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` | both of the above | both of the above |
| `perforce_test.go` `TestProvider_Prepare_ClassifiesAuthError` | the `setErr` on `changes -m1 //s/x/...#head` | `-c <c> changes -m1 //<c>/...#head` |
| `perforce_test.go` `TestProvider_Prepare_ClassifiesRecoverError` | both | both |
| `perforce_progress_test.go` `syncFixture` | both | both (it feeds all three progress tests) |
| `client_error_test.go` `TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified` | `changes -m1 //depot/disk full/...#head` | `-c <c> changes -m1 //<c>/...#head` |
| `perforce_orphan_test.go` `prepareFailingAtHeadResolution` | the `setErr` on `changes -m1 //s/x/...#head` | `-c <c> changes -m1 //<c>/...#head` |
| `provider_evict_recheck_test.go` | (no `changes -m1`; rev is `@1`) | no sync fixture is reached; leave as is |
| `sweeper_claim_test.go` | same | same |

In `TestProvider_PrepareCreatesClientAndSyncs` the `syncCall[:2]` assertion on `[]string{"-c", expectedClient}` is unchanged and stays.

- [ ] **Step 8: Rewrite the two comments this task falsifies**

`perforce_test.go`, `assertCwdContract`'s doc comment - head resolution moves to the workspace-scoped side. Name the remaining global invocations rather than writing "the client calls":

```go
// assertCwdContract pins the cwd half of the client-selection contract: every
// workspace-scoped invocation (argv begins with `-c <client>`) must run from
// wsRoot, while every global invocation - `client -o`, `client -i` and
// `client -d`, which are the ones that address no client - must run with an
// empty cwd. The `-c <client>` argv assertions already pin the client half;
// this locks the cwd half, previously only covered implicitly by the
// integration test.
```

`client_error_test.go`, the doc comment on `TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified` - add the clause that stops a future edit "simplifying" the wrap:

```go
// ResolveHead has returns that are NOT p4CommandError - a parse failure and a
// strconv error - and Provider.Prepare wraps them with the job's own depot path.
// That wrap is now the only route by which the job's path reaches the error at
// all, because the argv carries the client path instead; rewriting the wrap to
// the client path would make this test vacuous without failing it. Driven
// through Prepare rather than a hand-built error: an assertion built from a
// p4CommandError cannot see this, because having one in the chain is the very
// condition that triggers the exclusion.
```

- [ ] **Step 9: Run the whole default lane**

Run: `go test ./internal/agent/source/perforce/... -count=1 -v -timeout 300s`
Expected: PASS everywhere.

**`baseline_test.go` and `provider_evict_test.go` must have a zero-line diff.** Check with `git diff --stat` before committing; if either moved, the criteria they carry are gone.

- [ ] **Step 10: Run the full default suite and vet**

Run:
```
go test ./... -count=1 -timeout 900s
go vet ./internal/agent/source/perforce/...
go vet -tags integration ./internal/agent/source/perforce/...
```
Expected: `ok` / no output.

- [ ] **Step 11: Commit**

```bash
git add internal/agent/source/perforce/client.go \
        internal/agent/source/perforce/perforce.go \
        internal/agent/source/perforce/client_test.go \
        internal/agent/source/perforce/perforce_test.go \
        internal/agent/source/perforce/perforce_progress_test.go \
        internal/agent/source/perforce/client_error_test.go \
        internal/agent/source/perforce/perforce_orphan_test.go
git commit -m "fix(perforce): address p4 by client path so remap streams resolve

ResolveHead now runs p4 -c <client> changes -m1 //<client>/...#head from the
workspace root, and sync specs go out in client syntax. A virtual or import+
remap stream has no depot storage under the stream name - the depot side of the
client's view is the remap source - so the depot form addresses nothing and the
task fails at prepare. Client syntax resolves for every stream type because the
client view is what defines it.

resolved, the registry's BaselineHash and Request.SyncPaths all stay keyed on
the depot path. For BaselineHash that is forced, not merely conservative:
scheduler.BaselineHashFromAPISpec computes the same function server-side to
score warm-workspace affinity, and the coordinator cannot know this agent's
hostname or its allocated short id, both of which feed the client name. A
client-form hash would score every warm workspace in the fleet as
merely-present instead of at-baseline, silently, with no test able to see it.

Request.SyncPaths is inert either way and stays depot-form on a proof rather
than a preference: it feeds only PathPrefixOverlap, every comparison is between
holders of one Workspace and therefore shares one constant prefix, and the
prefix cancels out of HasPrefix. Workspace.syncedPaths is keyed on it in memory,
so moving it would also make every live entry stop matching.

Prepare's error wrap keeps naming the depot path. It is what the operator wrote,
and it is now the only place the job's own path reaches the error, since the
argv no longer carries it.

assertCwdContract's BODY is unchanged and its three call sites are green: it
branches on args[0] == \"-c\", which the new head-resolution call satisfies. Its
comment named that call as a global invocation and has been corrected."
```

---

## Task 7: the N14 re-assertion, and the interleaving it closes

Default lane. This is the spec's own headline correction to the backlog item, and it is a first-class task.

**Files:**
- Modify: `internal/agent/source/perforce/sweeper_claim_test.go`
- Modify: `internal/agent/source/perforce/perforce.go`

### The window, stated so the guard can be read against it

At HEAD, `reg.Upsert` runs after `ws.Acquire`, so the workspace has a holder and both `EvictWorkspace` and `ReserveForEvict` fail their inline holder check inside the same `p.mu` critical section as the reservation. No sweep can remove the entry between the Upsert and the end of `Prepare`.

After Task 5 the Upsert sits before `Acquire`, and for the whole stretch from the workspace get-or-create to `ws.Acquire` the workspace has **zero holders**, so `ReserveForEvict` succeeds. A sweep can therefore reserve, run `client -d`, `os.RemoveAll`, `reg.Remove`, `reg.Save`, `OnEvictedCB`, release the reservation, and be entirely finished before `Prepare`'s post-Acquire re-check reads `p.evicting` and finds it clear.

The mutual-exclusion property the re-check exists for still holds - exactly one of the two proceeds into the destructive window. What does not hold is the invariant this fix is sold on: the sweep deleted Prepare's registry entry while leaving Prepare running, and the leak is not reached by an early return at all. The re-check partitions the window; it does not promise the registration survived one.

After the re-check passes, `Prepare` holds a handle, so no *new* eviction can start. That is why an Upsert placed immediately after the re-check is sufficient and cannot be undone.

- [ ] **Step 1: Write the failing test**

Append to `sweeper_claim_test.go`. It reuses `gatingRunner` and `prepareAcquireHook` from the existing test in the same file, but unlike that test it lets the sweep run to **completion** inside the hook.

**Sketch - the existing test in this file is the exemplar; copy its `Sweeper` literal and its `t.Cleanup` discipline exactly.**

```go
// The registration moved above ws.Acquire, and between them the workspace has no
// holders, so a sweep can reserve, evict and release entirely inside that gap.
// The post-Acquire re-check then reads p.evicting and finds it clear, so Prepare
// proceeds - with its registry entry deleted. The re-check partitions the
// destructive window; it does not promise the registration survived one. This
// pins the re-assertion that restores it.
//
// The fake runner does not model a deleted p4 client, so what is pinned here is
// the REGISTRATION and not the sync's fate; the fake echoes what it is told and
// pretending otherwise would make the assertion vacuous.
func TestSweeperClaim_ASweepThatCompletesBetweenRegistrationAndAcquireIsRepaired(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	gate := &gatingRunner{
		inner:   fr,
		entered: make(chan struct{}),
		proceed: make(chan struct{}),
	}
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: gate}})

	reg, err := p.Registry()
	require.NoError(t, err)
	shortID := allocateShortID("//depot/main", reg)
	clientName := "relay_host_" + shortID

	fr.set("client -o -S //depot/main "+clientName, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("client -d "+clientName, "Client deleted.\n")
	// Reached only in the GREEN state: without the re-assertion reg.Get misses,
	// needsSync is false and neither of these runs at all.
	fr.set("-c "+clientName+" changes -c "+clientName+" -s pending -l", "")
	fr.setStream("-c "+clientName+" sync -q --parallel=4 //"+clientName+"/...@1", "")
	gate.gateKey = "client -d " + clientName

	reg.Upsert(WorkspaceEntry{
		ShortID:    shortID,
		SourceKey:  "//depot/main",
		ClientName: clientName,
		LastUsedAt: time.Now().Add(-30 * 24 * time.Hour),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

	sw := &Sweeper{
		Root:        root,
		Reg:         reg,
		MaxAge:      14 * 24 * time.Hour,
		Client:      p.Client(),
		ListLocked:  p.LockedShortIDs,
		Claim:       p.ReserveForEvict,
		OnEvictedCB: p.InvalidateWorkspace,
	}

	type sweepResult struct {
		evicted []string
		err     error
	}
	sweepDone := make(chan sweepResult, 1)
	var once bool
	prepareAcquireHook = func(string) {
		if once {
			return
		}
		once = true
		go func() {
			ev, err := sw.SweepOnce(context.Background())
			sweepDone <- sweepResult{ev, err}
		}()
		<-gate.entered
		close(gate.proceed)
		// Wait for the sweep to FINISH, not just to reserve: the reservation must
		// be released before Prepare's post-Acquire re-check reads it, or this
		// test measures the existing back-out path instead of the new one.
		res := <-sweepDone
		require.NoError(t, res.err)
		require.Equal(t, []string{shortID}, res.evicted)
	}
	t.Cleanup(func() { prepareAcquireHook = nil })

	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//depot/main",
			Sync:   []*relayv1.SyncEntry{{Path: "//depot/main/...", Rev: "@1"}},
		},
	}}

	h, prepErr := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.NoError(t, prepErr, "the sweep finished and released, so Prepare must not back out")
	t.Cleanup(func() { _ = h.Finalize(context.Background()) })

	_, ok := reg.Get(shortID)
	require.True(t, ok, "Prepare must leave a registry entry for the workspace it is holding")
}
```

Two things to check against the real helpers before running: `gatingRunner.gatedOne` gates only the FIRST matching call, so `close(gate.proceed)` inside the hook is safe; and `require` calls inside `prepareAcquireHook` run on `Prepare`'s goroutine, which is the test goroutine here, so `require`'s `FailNow` is legal.

- [ ] **Step 2: Run it and record the RED**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeperClaim_ASweepThatCompletesBetweenRegistrationAndAcquireIsRepaired -count=1 -v -timeout 120s`

Expected: **FAIL** at the last assertion, `Prepare must leave a registry entry for the workspace it is holding`, `Should be true`.

This is a real RED against a real unguarded tree, not a mutation. Confirm the failure is on that assertion and not on `require.NoError(t, prepErr, ...)` - if `Prepare` returned "being evicted", the hook did not wait for the sweep to finish and the test is measuring the existing back-out path.

- [ ] **Step 3: Add the re-assertion**

In `Prepare`, immediately after the post-Acquire eviction re-check block (after the `if evicting { handle.Release(); return ... }`) and before `cur, curOK := reg.Get(shortID)`:

```go
	// A sweep that reserved this short id in the window between the registration
	// above and ws.Acquire can have run to completion - client -d, RemoveAll,
	// reg.Remove - and released before the re-check read p.evicting, so the
	// re-check passes and the entry is gone. The re-check partitions the
	// destructive window; it does not promise the registration survived one. We
	// hold a handle now, so no further eviction can reserve, and restoring the
	// entry here makes the last registry write in Prepare's own ordering an
	// upsert. TestSweeperClaim_ASweepThatCompletesBetweenRegistrationAndAcquireIsRepaired.
	if _, ok := reg.Get(shortID); !ok {
		reg.Upsert(WorkspaceEntry{
			ShortID:      shortID,
			SourceKey:    pf.Stream,
			ClientName:   clientName,
			BaselineHash: "",
			LastUsedAt:   time.Now(),
		})
		_ = reg.Save()
	}
```

It is an Upsert only. It deliberately does not re-`MkdirAll` or re-`CreateStreamClient`: the unconditional creation in Task 5 means the *next* `Prepare` for this stream rebuilds both and re-syncs (the restored entry carries `BaselineHash: ""`), so the richer version buys one retry and costs a second place where creation lives.

- [ ] **Step 4: Add the sentence the neighbouring comment now needs**

The post-Acquire re-check's existing comment stays in substance and must not be trimmed. Append one sentence to it:

```go
	// ... exactly one of the two proceeds.
	//
	// It does not follow that the registry entry survived: a sweep that completed
	// entirely inside the window leaves p.evicting clear here. The re-assertion
	// below is what restores it.
```

- [ ] **Step 5: Run it and verify it passes, then the whole lane**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeperClaim -count=1 -v -timeout 120s`
Expected: both tests in the file PASS.

Run: `go test ./internal/agent/source/perforce/... -count=1 -timeout 300s`
Expected: `ok`.

- [ ] **Step 6: Run this package under the race detector**

The new test drives a real concurrent sweep against a live `Prepare`, which is the strongest reason in the slice to run `-race`.

Run (Bash tool, from the worktree root):
`MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 golang:1.26 go test -race ./internal/agent/source/perforce/... -count=1 -timeout 600s`
Expected: `ok`, zero data races.

If the container is unavailable, **say `-race` did not run**. Do not substitute `-count=N`: repetition re-runs under the ordinary scheduler and cannot observe an unsynchronised access that never happens to interleave badly.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/sweeper_claim_test.go
git commit -m "fix(perforce): re-assert the registration after the post-Acquire evict re-check

Moving registration above ws.Acquire opened a window the backlog item did not
have: for the whole stretch from the upsert to the acquire the workspace has
zero holders, so ReserveForEvict succeeds. A sweep can reserve, run client -d,
RemoveAll, reg.Remove and release - all of it - before Prepare's post-Acquire
re-check reads p.evicting and finds it clear. Prepare then runs on with its
registry entry deleted.

The mutual-exclusion property the re-check exists for is intact: exactly one of
the two enters the destructive window, and a Prepare that overlaps an in-flight
eviction still backs out. So the item's Proposal 4 question, asked narrowly,
answers yes. What the re-check cannot promise is that the registration survived
a sweep that completed INSIDE the window, and that leak is not reached by an
early return at all - which is what the item's acceptance criterion assumed.

Restated criterion: no exit from Prepare, early or successful, leaves a
workspace directory or p4 client spec with no registry entry.

After the re-check Prepare holds a handle, so no further eviction can reserve
and the restored entry cannot be removed again. Upsert only: unconditional
CreateStreamClient means the next Prepare rebuilds the client and the directory
and re-syncs from the empty baseline, so a richer repair here buys one retry and
costs a second place where creation lives.

RED was taken against the reordered, unguarded tree - a real interleaving driven
by a real concurrent SweepOnce - not against a mutation.

One operator-facing behaviour change worth knowing: EvictWorkspace refused an
unregistered short id with 'not found in registry'. With early registration, a
manual evict issued during a stream's first-ever Prepare now finds the entry and
proceeds. Safe - the reservation and the re-check still partition the two."
```

---

## Task 8: the p4d GREEN

**Docker plus `p4` on PATH required.** This is the only proof the fix works.

**Files:** none modified.

- [ ] **Step 1: Run the new integration test**

Run: `go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout -count=1 -v -timeout 1800s`

Expected: `--- PASS`. The baseline file is present at `<wsRoot>/sub/readme.txt` (or `<wsRoot>/vendor/readme.txt` on the Task 1 fallback) with content `baseline\n`.

**A SKIP IS NOT A GREEN.** If the output reads `--- SKIP:`, Docker or `p4` was unavailable and this task did not run. Report that, and do not report the slice as verified.

- [ ] **Step 2: Run the whole integration package**

Run: `go test -tags integration -p 1 ./internal/agent/source/perforce/... -count=1 -v -timeout 1800s`

Expected: `--- PASS: TestPerforce_E2E_SyncAndUnshelve` **and** `--- PASS: TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout`. The first proves a plain mainline stream is unaffected; its `//test/main/...` sync now goes out as `//<client>/...` and must still land `readme.txt` at the workspace root and still unshelve.

- [ ] **Step 3: Record the `client -i` no-op claim, or record that you could not**

Spec D2 asserts p4 no-ops an unchanged client submission, reporting `Client <name> not changed.`, so the unconditional `client -i` costs a round trip on the wire and not a server-side write. The second `Prepare` in `TestPerforce_E2E_SyncAndUnshelve` exercises exactly this. Add nothing to the test; run the lane with `-v`, look for the line in the captured output, and record what you saw.

**The decision does not rest on this** - it rests on the failure mode of the conditional alternative - so if the message is not visible in the output, write "not observed" and move on. Do not add an assertion for it and do not change the decision.

- [ ] **Step 4: No commit** (nothing changed). Record both PASS lines verbatim; they go in the PR body.

---

## Task 9: whole-slice verification

**Files:** none modified.

- [ ] **Step 1: Default lane, whole repo**

Run: `go test ./... -count=1 -timeout 1200s`
Expected: `ok` for every package.

- [ ] **Step 2: Vet under both tag sets**

Run:
```
go vet ./...
go vet -tags integration ./internal/agent/source/perforce/...
```
Expected: no output.

- [ ] **Step 3: The race lane**

Run (Bash tool, from the worktree root):
`MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 golang:1.26 go test -race ./... -count=1 -timeout 900s`
Expected: `ok` across every package, zero data races.

This container is also the only local way to run this repo's `//go:build !windows` files, which `go test` on Windows silently skips. If it is unavailable, state plainly that `-race` did not run.

- [ ] **Step 4: Line-ending and encoding sweep over everything this slice touched**

```powershell
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD | ForEach-Object { git ls-files --eol $_ }
```
Every path must read `i/lf`. `entrypoint.sh` must also read `w/lf`. Confirm the diffstat is proportionate: this slice is roughly 400-500 changed lines across about twelve files. An order-of-magnitude larger number on any single file means a line-ending or encoding rewrite, not a content change.

- [ ] **Step 5: No commit.** Record every result.

---

## Task 10: the prose sweep - README and the verification pass

Default lane. The comment rewrites already landed with the commits that falsified them (Tasks 5, 6 and 7). What remains is README, plus a check that nothing false survived.

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Edit the `stream` row of the source field table**

In `### Source workspaces`, the `stream` row currently reads:

```
| `stream` | Yes | Perforce stream path. Workspaces are keyed by stream and reused across tasks. |
```

Replace with:

```
| `stream` | Yes | Perforce stream path. Mainline, development, task, virtual and `import+` remap streams are all supported: relay addresses p4 by client path, so the stream's own view - remaps included - is what defines the layout on disk. Workspaces are keyed by stream and reused across tasks. |
```

**The `sync` row does not change.** The spec field is still a depot path under the stream; the client-path rewrite is internal, and naming it in the field table would invite an operator to write one. The `**Workspace arbitration.**` paragraph does not change either: the arbitration keying is unmoved.

`bug-2026-09-03-readme-source-workspaces-table-omits-client-template` is open against the same table and adds a `client_template` row. Whichever lands second rebases trivially - flag it in the PR body so a reviewer is not surprised by a second edit to adjacent lines.

- [ ] **Step 2: Verify the README edit did not corrupt the file**

```powershell
git diff --stat -- README.md
git ls-files --eol README.md
```
Expected: `1 file changed, 1 insertion(+), 1 deletion(-)` and `i/lf`. **A diffstat of hundreds of lines on a one-line change means the file was reclassified as binary by a stray CR** - revert and re-apply with an exact-anchor replacement.

Also confirm the file still decodes as UTF-8:
```powershell
[System.Text.Encoding]::UTF8.GetString([System.IO.File]::ReadAllBytes("README.md")) | Out-Null
```

- [ ] **Step 3: Verify no falsified passage survived**

Run:
```
git grep -n "ResolveHead" -- internal/agent/source/perforce/
git grep -n "first p4 call" -- internal/agent/source/perforce/
git grep -n "global invocation" -- internal/agent/source/perforce/
```
For each hit, read the surrounding sentence and confirm it is true of the tree now. The four passages this slice falsifies are: `assertCwdContract`'s comment (rewritten in Task 6), `TestProvider_Prepare_ClassifiesAuthError`'s "first p4 call" (Task 5), `client.go`'s `ResolveHead` doc (Task 6), and the deleted `// First time: create on-disk dir and p4 client spec.` (Task 5). If any is still wrong, fix it here.

**Do not touch `docs/superpowers/specs/2026-05-01-p4client-explicit-flag-design.md` or its retro**, which say `ResolveHead` is server-global and stays as-is. Those are records of a moment and stay as written; "fixing" them destroys the record that the decision was made against a tree where no remap stream was in scope.

- [ ] **Step 4: Run the default lane once more and commit**

Run: `go test ./internal/agent/source/perforce/... -count=1 -timeout 300s`
Expected: `ok`.

```bash
git add README.md
git commit -m "docs: name the supported Perforce stream types

README documented stream support with no qualification on stream TYPE, which is
what made the virtual/remap failure a correctness bug rather than a studio
preference. The sync field row is unchanged on purpose: the spec field is still
a depot path under the stream and the client-path rewrite is internal, so
naming it in the field table would invite an operator to write one."
```

---

## Self-review against the spec

Run through this before handing the plan back.

**Spec coverage.** Every item in spec section 6 maps to a task: R0 -> Task 0; R1 -> Task 1; R2 -> Task 2; R3 -> Task 3; R4 -> Task 6 Steps 1-5; R5 -> Task 5 plus Task 6 Steps 6-7; R6 and R6b -> Task 4 plus Task 5 Step 6; R7 -> Task 7; R8 -> Task 8; R9 -> distributed into Tasks 5, 6, 7 plus Task 10; R10 -> Task 9. Section 4.6's call-site table is fully covered. Section 7.1's six passages and 7.2's one row are covered. Section 8.3's ten criteria are each asserted by a named test or a named command.

**Decisions carried, unchanged:** D1 (client-path addressing), D2 (unconditional `CreateStreamClient`, no `client -i` skip), D3 (`toClientPath` fails closed), D4 (empty remainder maps to `/...`, upgrade edge recorded), D5 (`!found` guard kept), D6 (N14 is Upsert only), D7 (`SyncPaths` depot-form), D8 (`ResolveHead` runs from `wsRoot`), D9 (fixture first, alone), D10 (no new `classifyP4Error` case).

**Decisions changed, with reasons stated above:** the spec's R5 is split (the N14 contradiction), R6b moves after the reorder (it is not green at HEAD as written), R9 is distributed (wrong prose must not live across commits), and `TestClient_ResolveHeadError` becomes `TestClient_RunFailureBubbles` (the former does not exist).

**Explicitly out of scope, per spec section 2:** no exclusion handling in `toClientPath`; no change to `Request.SyncPaths` or `Workspace` arbitration; no `sweeper.go` edit; no new `classifyP4Error` case; no progress heartbeat; no `jobspec` change; no `client -i` skip.

---

## For the conductor

**Backlog item to close at integration:** `/backlog close bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync`.

**Backlog candidates this slice surfaces** (proposed by the spec's section 11, not filed - they need the human's acceptance):

1. A validator tightening for `sync[].path == stream`, since the empty-remainder mapping is a behaviour change the validator's own permissiveness created. Retroactive over stored `scheduled_jobs.job_spec` rows, so it needs its own item and its own re-validating-reader analysis.
2. A warn-once at agent startup when `RELAY_WORKSPACE_ROOT` is set but neither `RELAY_WORKSPACE_MAX_AGE` nor `RELAY_WORKSPACE_MIN_FREE_GB` is, since in that configuration nothing ever reclaims disk. This is the qualifier behind F3.
3. A README sentence for `CreateStreamClient` regenerating an operator's hand-edited client spec on every `Prepare` rather than on next first use - a widening of existing behaviour, worth documenting only if a studio reports it.

**Sequencing note for the PR body:** `feature-2026-09-03-p4-sync-progress-heartbeat` edits the same `SyncStream` call site and should land after this. `bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr` is **not** closed here: the stream-name portion of the path leaves argv but the remainder does not, and p4 still echoes offending paths into its own stderr, which `classifiableText` deliberately still classifies. Report that as a partial mitigation, never as a fix.
