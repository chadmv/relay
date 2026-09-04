# A timer-driven progress heartbeat for a long `p4 sync` - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A running `p4 sync` writes a periodic summary line (elapsed, files, other lines, free GB, last depot path) through the existing `progress` callback so an operator can tell a live multi-hour transfer from a wedged one - and the per-file output that makes the count possible is counted and never forwarded to `task_logs`.

**Architecture:** `Client.SyncStream` drops `-q`, so p4 writes one stdout line per file. A new `syncProgress` value counts those lines and remembers the last depot path; it never buffers them. `Provider.Prepare` runs `SyncStream` on **its own goroutine** and keeps the heartbeat loop on `Prepare`'s goroutine, so `progress` still has exactly one caller. Both existing brackets (`[sync] complete`, `[sync] failed`) carry the same summary; no new completion vocabulary is added. The interval comes from `RELAY_SYNC_HEARTBEAT_INTERVAL` through a new `resolveSyncHeartbeatInterval` wrapper over the existing `parseDurationEnv`, and free space through the same `freeDiskGB` identifier `Sweeper.FreeDiskGB` already receives.

**Tech Stack:** Go 1.26, testify, testcontainers-go, p4d r25.2 in Docker, the `p4` CLI on PATH.

**No `make generate` anywhere in this plan.** No `.sql`, no `.proto`, no `web/` file is touched. `web/dist` must not be rebuilt or staged. If you find yourself running `make generate`, you have left the plan.

**Spec:** `docs/superpowers/specs/2026-09-04-p4-sync-progress-heartbeat.md` (autonomous gate). Its decisions D1-D14 stand except where this plan records a refutation below.

**Backlog item this closes** (via `/backlog close`, by the conductor, at integration):

- `docs/backlog/feature-2026-09-03-p4-sync-progress-heartbeat.md`

**In-tree exemplars this plan copies:**

- Fake-runner fixture recipe: `syncFixture` in `internal/agent/source/perforce/perforce_progress_test.go`.
- Blocking-subprocess fixture: `fakeRunner.setBlock` and its `Run` branch in `internal/agent/source/perforce/fixtures_test.go`.
- Package-var test seams: `lookPath` and `prepareAcquireHook` in `internal/agent/source/perforce/perforce.go`.
- Log-capture test shape: `TestParseDurationEnv_LogsWarningOnInvalidNonEmptyInput` in `cmd/relay-agent/main_test.go`.
- Injected platform helper: `FreeDiskGB: freeDiskGB` in the `&perforce.Sweeper{...}` literal in `cmd/relay-agent/main.go`.
- Integration scaffolding: `startP4dContainer` and the env-isolation block at the top of `TestPerforce_E2E_SyncAndUnshelve` in `internal/agent/source/perforce/perforce_integration_test.go`.

---

## Slice independence declaration

**This is backend and docs only. There is NO frontend slice.** Nothing under `web/` is read, edited, rebuilt or committed. `web/dist` must not be touched. **Do not dispatch `relay-frontend-engineer`.** The FE/BE independence question does not arise, because there is no FE slice for the BE slice to be independent of. The Phase 3 fan-out is therefore a fan-out of one.

**This is ONE lane, ONE worktree, ONE PR, ONE session.** It has NO stages and must NOT be handed to `/backlog phases`.

**`relay-backend-engineer` owns the whole lane and is the only agent that writes to the tree.** Task 10 is integration-lane work, but it edits a file nobody else edits and depends on every task before it, so it stays with the same owner. `relay-integration-tester` is a Phase 4 verifier here, not an implementer.

**Nothing in this plan may run concurrently.** Every task writes to the same Go package; two agents in one worktree share one git index, and two worktrees would produce two branches each red until merged (the counter is pointless without the output, and the output is dangerous without the counter - D14).

**Docker plus the `p4` CLI on PATH are required for Task 10 and the integration halves of Tasks 0 and 13.** Every other task runs in the default lane with no Docker. **A Docker-unavailable or p4-unavailable run is a SKIP, not a green.** Say which you obtained, every time.

---

## What I verified in the spec, and what I refuted

The spec refutes five of the item's claims and its corrections are claims too. I re-derived every load-bearing one against the tree at HEAD. **Symbols, never line numbers.**

### Confirmed against the tree

| Spec claim | How I checked it | Verdict |
|---|---|---|
| F1: `progress` takes a mutex and holds it across a send bounded only by the AGENT context | Opened `Runner.makePrepareProgressFn` in `internal/agent/runner.go`: `progress` does `mu.Lock(); defer mu.Unlock()` and calls `doFlush()` inside that window; `doFlush` calls `r.send`; `Runner.send` is `select { case r.sendCh <- msg: case <-r.ctx.Done(): }` and `r.ctx` is the agent context. `Runner.sendOrAbort` - which DOES have `forcedCh`/`cancelledCh` arms - is used only by `chunkWriter`, not by `makePrepareProgressFn` | **CONFIRMED, exactly as the spec states.** A second goroutine calling `progress` would hold `mu` while parked, blocking `Prepare`'s own completion line and `Runner.Run`'s `flushProgress`, with the workspace handle still held. **The item's architecture deadlocks `Prepare`; the spec's inversion is the fix.** D1 stands and is the reason for this plan's shape. |
| The headline claim: `-q` is present | `Client.SyncStream` in `client.go`: `args := append([]string{"-c", client, "sync", "-q", "--parallel=4"}, specs...)` | **CONFIRMED.** |
| `progress` IS the `onLine` callback today | The `if needsSync` block in `Provider.Prepare`: `p.cfg.Client.SyncStream(ctx, wsRoot, clientName, syncSpecs, progress)` | **CONFIRMED.** Removing the forwarding is a deletion of live wiring, not an addition. |
| F3: a live test asserts the OPPOSITE of the item's acceptance criterion 2 | `TestProvider_PrepareBracketsTheSyncWithExactlyOneStartAndOneCompleteLine` in `perforce_progress_test.go` ends with `assert.Equal(t, 3, countLinesContaining(lines, "of 3 files"), "p4's own output must still reach progress unchanged, got: %v", lines)`, under a comment saying the assertion exists to stop "no per-file line" degenerating into "the sync emits nothing" | **CONFIRMED, quoted above.** Task 4 rewrites it; see REFUTED 1 for how, because the spec's prescribed replacement does not work. |
| F4: a guard refuses `stop(err)` emitting the cause | `TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause`, whose comment reads "A reviewer reading this diff alone will want to add the error text back into the failure line; this test is what refuses it", and whose loop asserts `assert.NotContains(t, l, "SYNC-CAUSE-SENTINEL", ...)` over every progress line | **CONFIRMED.** The item's "`stop(err)` emits `FAILED` and the error" is refused by a test whose comment names that exact reviewer. D3 stands. |
| F6: `parseDurationEnv`'s regex, and `"0"` not disabling anything | `var durRe = regexp.MustCompile(`^(\d+)([smhd])$`)` in `cmd/relay-agent/main.go`; on no match it logs `warning: %s=%q is not a valid duration ...` and returns the fallback | **CONFIRMED.** A bare `"0"` has no unit and takes the warn-and-fall-back path; `"-30s"` is unrepresentable in the regex and takes the same path. `"0s"` matches and yields `0`. The item's spelling is wrong; D7's `0s` is right. |
| F6: three different env-duration conventions in the agent | (1) `parseDurationEnv`'s regex, used for `RELAY_WORKSPACE_MAX_AGE` and `RELAY_WORKSPACE_SWEEP_INTERVAL` in `main`; (2) an inline `time.ParseDuration` with `d > 0` for `RELAY_TELEMETRY_INTERVAL` in `main`; (3) `resolveEvictTimeout` in `internal/agent/source/perforce/sweeper.go`, a package var using `time.ParseDuration` for `RELAY_EVICTION_TIMEOUT` | **CONFIRMED, all three, at the named sites.** D7's choice of (1) is well founded: it is the convention the two neighbouring workspace knobs use and it already warns naming the variable. |
| F2: `freeDiskGB`'s real signature, unit, and that it is already injected | `cmd/relay-agent/free_disk_windows.go` and `free_disk_unix.go` both declare `func freeDiskGB(path string) (int64, error)` and both divide by `1024*1024*1024` **before returning**; `cmd/relay-agent/main.go` passes `FreeDiskGB: freeDiskGB` into the `&perforce.Sweeper{...}` literal; `Sweeper.FreeDiskGB` is declared `func(root string) (int64, error)` in `sweeper.go` | **CONFIRMED on all three points.** It returns GIGABYTES, not bytes; a 999 MB volume reports `0`. `Config.FreeDiskBytes` is not producible without editing both platform files, which the item's own next sentence forbids. D6 stands. |
| F7: `preparing` is in `AppendTaskLog`'s allow-list | `internal/store/query/tasks.sql`, the `AppendTaskLog` fence CTE: `AND (t.status IN ('pending', 'dispatched', 'preparing', 'running') OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)`, under a comment that names `internal/agent/runner.go, makePrepareProgressFn` as the reason | **CONFIRMED.** The heartbeat's lines have a durable home. Nothing to do; this is the single cheapest way the slice could have shipped a silent no-op. |
| F12: the fixture re-key surface is seven test-side keys in three files | Searched for `sync -q --parallel=4`: `perforce_test.go` x5, `perforce_progress_test.go` x1 (inside `syncFixture`), `perforce_warm_test.go` x1, plus the one production site in `client.go` | **CONFIRMED, 7 + 1.** `provider_evict_test.go`, `provider_evict_recheck_test.go`, `sweeper_claim_test.go` and `perforce_orphan_test.go` have none. |
| F9: two false integration comments plus `require.Len(t, progress2, 2, ...)` | Read `TestPerforce_E2E_SyncAndUnshelve`: both comments are present verbatim as the spec quotes them, and the length assertion is live | **CONFIRMED.** |
| D11: the seam pattern and the absence of `t.Parallel()` | `var lookPath = exec.LookPath` and `var prepareAcquireHook func(shortID string)` are both package vars in `perforce.go`; a search for `t.Parallel()` across the package returns nothing | **CONFIRMED.** Package-var seams are safe here. |
| The release-before-report ordering and its guard | The `if needsSync` failure branch calls `handle.Release()` then `progress("[sync] failed; ...")`; `TestProvider_ASyncFailureReleasesTheWorkspaceBeforeItReportsAnything` observes `heldWorkspaceCount` from inside the callback on the `[sync] failed` line and asserts `0` | **CONFIRMED.** D9 stands; the summary is rendered AFTER the release. |

### REFUTED 1 - the spec's R5 replacement assertion cannot pass, because the existing fixture emits no depot-path lines

**Spec R5 says:** keep the fixture and replace the deleted assertion with `assert.Equal(t, 1, countLinesContaining(lines, "3 files"), ...)`, arguing that `"3 files"` is a deliberate substring of `"of 3 files"` so a leak makes the count 4.

**The fixture is `fr.setStream(syncKey, "1 of 3 files\n2 of 3 files\n3 of 3 files\n")`.** None of those three lines begins with `//`, so `syncLineDepotPath` returns `""` for all three and they are counted as `other`. The completion bracket would therefore read **`0 files; 3 other lines`**, and `countLinesContaining(lines, "3 files")` would be **0**, not 1. The spec's replacement assertion fails against a correct implementation. The substring-collision device it is built on evaporates with it.

**Resolution, and it is load-bearing for Task 4.** The fixture content changes to three REAL p4 sync lines, and the leak probe keys on the action verbs, which appear in p4's output and never in the summary:

```go
fr.setStream(syncKey,
    "//depot/x/a.ma#3 - added as /ws/a.ma\n"+
        "//depot/x/My File - Copy.ma#1 - updating /ws/My File - Copy.ma\n"+
        "//depot/x/c.ma#2 - refreshing /ws/c.ma\n")
```

The non-degeneracy argument the original comment carries is preserved and strengthened: `3 files` proves the counter is wired to `onLine`, `0 other lines` proves every line parsed, `last //depot/x/c.ma` proves `lastPath` tracks the LAST line rather than the first, and three verb probes at zero prove nothing leaked.

**Do not verify the leak by running the spec's "count 4" check.** It is a check on an assertion this plan does not write. The real RED is Task 4 Step 4, run against a tree where `-q` is gone and forwarding is still in place.

Note also that `0 other lines` does NOT discriminate the `" - "` split: a `" - "` split on the `My File - Copy.ma` row still returns a non-empty prefix, so the line still counts as a file. Only the unit table in Task 1 kills that mutation. Do not let the two tests be conflated.

### REFUTED 2 - `fakeRunner` has no mutex, and the design moves `Stream` onto a second goroutine

The spec's R9 proposes asserting on "the recorded `fakeRunner` call for the sync key", relying on `f.calls = append(...)` being `Stream`'s last statement. It is - but `fakeRunner.calls` is a plain slice with no synchronisation, and after this slice `Stream` runs on the sync goroutine while the test asserts from the test goroutine. On the CORRECT tree the `errCh` receive establishes happens-before, so a read after `Prepare` returns is safe. **On the mutated tree the guard's own read is a data race**, which turns a clean default-lane RED into a race report and makes the outcome timing-dependent.

**Resolution:** Task 2 adds `streamDone atomic.Int32` to `fakeRunner`, incremented as `Stream`'s last statement on **every** return path. The R9 guard reads `fr.streamDone.Load()`, which is race-free in both lanes. `f.calls` is left alone, and **no test may read `fr.calls` or call `fr.argHistory()` while `Prepare` is still running** - Task 4's cadence test in particular.

### REFUTED 3 - `setStreamBlock` must not record its call before it blocks, and must delay on the cancel path

Neither point is in the spec, and both decide whether the R9 guard is real. If the blocking branch increments `streamDone` before parking, the guard is satisfied on the mutated tree too and is decorative. If it increments immediately on `ctx.Done()`, the mutant and the test race and the RED is intermittent. Task 2 therefore specifies: increment **after** the block ends, and sleep 50 ms on the `ctx.Done()` path before incrementing, modelling a p4 child that takes a moment to die. Task 6 proves the guard reddens.

### REFUTED 4 - a sixth passage is falsified and the spec's sweep misses it

Spec 7.1 lists five passages. `perforce_test.go`'s `TestProvider_PrepareCreatesClientAndSyncs` also carries `require.NotEmpty(t, lines, "sync stream should have produced progress lines")`. After this slice the assertion still passes, but only because of the bracket lines - the sync stream produces none. It stays GREEN while its message becomes false, which is this repo's dominant defect class in its least visible form. Task 4 rewrites the message in the commit that falsifies it.

### Not refuted, and enforced structurally

The spec's D14 (one commit for `-q` plus the throttle) is right. Task 4 is a single task with a single commit at its end and an explicit **DO NOT COMMIT** marker between its steps, because the intermediate tree after its Step 2 is exactly the forbidden one - `-q` gone, `progress` still the `onLine` callback, the per-file volume of a large sync landing in `task_logs`.

---

## File structure

| File | Create / Modify | Responsibility |
|---|---|---|
| `internal/agent/source/perforce/syncprogress.go` | **Create** | `syncProgress` (counter, no line buffer), `syncLineDepotPath` (parse + sanitize + clip). One responsibility: turn a stream of p4 stdout lines into O(1) state. |
| `internal/agent/source/perforce/syncprogress_test.go` | **Create** | The `syncLineDepotPath` table and the `onLine`/`snapshot` tests. |
| `internal/agent/source/perforce/sync_heartbeat_test.go` | **Create** | The `syncSummary` renderer table, the cadence test, the disabled-timer test, and the two mutation guards. |
| `internal/agent/source/perforce/perforce.go` | Modify | `Config` gains `SyncHeartbeatInterval` and `FreeDiskGB`; new `syncNow` / `newSyncTicker` seams; new `runSyncWithHeartbeat` and `syncSummary` methods; the `if needsSync` block is rewritten. |
| `internal/agent/source/perforce/client.go` | Modify | `SyncStream` drops `-q` from the argv and its doc comment states the hazard that creates. |
| `internal/agent/source/perforce/fixtures_test.go` | Modify | `fakeRunner` gains `streamBlock`, `setStreamBlock` and `streamDone`. |
| `internal/agent/source/perforce/perforce_progress_test.go` | Modify | `syncFixture`'s key loses `-q`; the brackets test's fixture, assertions, name and comment are rewritten. |
| `internal/agent/source/perforce/perforce_test.go` | Modify | Five fixture keys lose `-q`; one falsified assertion message is rewritten. |
| `internal/agent/source/perforce/perforce_warm_test.go` | Modify | One fixture key loses `-q`. |
| `internal/agent/source/perforce/perforce_integration_test.go` | Modify | Both false comments rewritten; the first prepare's completion line is asserted against a RECORDED observation. |
| `cmd/relay-agent/main.go` | Modify | `resolveSyncHeartbeatInterval` plus two fields in the `perforce.Config` literal. |
| `cmd/relay-agent/main_test.go` | Modify | The `resolveSyncHeartbeatInterval` table. |
| `README.md` | Modify | One env-table row and one sentence in "Source workspaces". |

---

## Task 0: Record the baseline, both ways

**Files:** none. No commit.

- [ ] **Step 1: Run the default lane and record the result**

```
go test ./internal/agent/source/perforce/... ./internal/agent/... ./cmd/relay-agent/... -count=1 -timeout 600s
```

Expected: PASS. Write the summary line for each package into your notes. Nothing later in this plan may be diagnosed against an unmeasured baseline.

- [ ] **Step 2: Run the integration lane and record which outcome you got**

```
go test -tags integration -p 1 ./internal/agent/source/perforce/... -count=1 -timeout 1800s
```

Needs Docker Desktop running and `p4` on PATH. Expected: PASS, or a clean SKIP if either is missing. **Record which.** A skip is not a green, and you must say the word "skipped" in your report if that is what happened.

- [ ] **Step 3: Confirm the tree is clean**

```
git status --porcelain
```

Expected: empty. If it is not, stop and report - do not build on a dirty tree.

---

## Task 1: `syncLineDepotPath` and `syncProgress`

**Files:**
- Create: `internal/agent/source/perforce/syncprogress.go`
- Create: `internal/agent/source/perforce/syncprogress_test.go`

The RED here cannot come from absence: a test for a function that does not exist is a compile error, and a compile error is not evidence about behaviour. Step 1 therefore lands a **plausible-wrong stub** - the `" - "` split a reasonable engineer writes first - and the table's RED is measured against it.

- [ ] **Step 1: Land the plausible-wrong stub**

Create `internal/agent/source/perforce/syncprogress.go`:

```go
package perforce

import "strings"

// STUB, replaced in this same task. Splits on " - ", which is the obvious
// wrong rule; the table in syncprogress_test.go is what kills it.
func syncLineDepotPath(line string) string {
	i := strings.Index(line, " - ")
	if i < 0 {
		return ""
	}
	return line[:i]
}
```

Run `go build ./internal/agent/source/perforce/...`. Expected: success.

- [ ] **Step 2: Write the failing table**

Create `internal/agent/source/perforce/syncprogress_test.go` with `TestSyncLineDepotPath`, a `[]struct{ name, in, want string }` table driven by `t.Run`. **Write all seven rows, and give each its own `name` so the failure output names the row:**

| `name` | `in` | `want` | Discriminates |
|---|---|---|---|
| `added` | `//depot/x/a.ma#3 - added as /ws/a.ma` | `//depot/x/a.ma` | the happy path |
| `dash_in_filename` | `//depot/x/My File - Copy.ma#1 - updating C:\ws\My File - Copy.ma` | `//depot/x/My File - Copy.ma` | a `" - "` split, which returns `//depot/x/My File` |
| `deleted` | `//depot/x/b.ma#2 - deleted as /ws/b.ma` | `//depot/x/b.ma` | an action allow-list that only knows `added`/`updating` |
| `windows_local_path` | `//depot/x/c.ma#5 - refreshing C:\ws\c.ma` | `//depot/x/c.ma` | a POSIX-only local-path assumption |
| `not_a_file_line` | `File(s) up-to-date.` | `""` | a rule that does not require the `//` prefix |
| `empty` | `""` | `""` | an unguarded index |
| `no_rev_separator` | `//depot/x/no-rev.ma - added as /ws/no-rev.ma` | `""` | a rule that falls back to `" - "` when `#` is absent |

Plus two cases that need their own assertions rather than a `want` string. Write them as separate `t.Run` blocks in the same test function:

- `control_bytes`: input is `"//depot/x/a\rb.ma#3 - added as /ws/a.ma"`. Assert the result contains no `"\r"` and no byte below `0x20` (`assert.NotContains(t, got, "\r")` plus a loop over the bytes). **Write the carriage return as the Go escape `\r`, never as a literal byte in the source file** - a raw non-ASCII or control byte in a source file survives every gate this repo runs.
- `clip_at_200`: input is `"//depot/" + strings.Repeat("z", 400) + "#1 - added as /ws/z"`. Assert `len(got) == 200`.

- [ ] **Step 3: Run the table against the stub and record every failing row**

```
go test ./internal/agent/source/perforce/... -run TestSyncLineDepotPath -v -count=1 -timeout 60s
```

Expected failures include `dash_in_filename` (the stub returns `//depot/x/My File`), `no_rev_separator` (the stub returns `//depot/x/no-rev.ma`), `control_bytes` and `clip_at_200`. `not_a_file_line` and `empty` may PASS against the stub, because neither input contains `" - "`. **Record the set of rows that actually failed rather than the set this plan predicts.** If fewer than three rows fail, the table is too weak - add rows before proceeding.

- [ ] **Step 4: Replace the stub with the `#` rule**

Rewrite `syncLineDepotPath`. The rule, in order:

1. If the line does not begin with `//`, return `""`.
2. Find the FIRST `#` with `strings.IndexByte`. If there is none, return `""`.
3. Take the prefix, strip every byte below `0x20`, clip to 200 characters, return it.

Clip AFTER stripping, so a path padded with control bytes cannot smuggle content past the 200-character bound.

The doc comment states why the split is on the first `#` and not on `" - "`: p4 requires `@ # % *` to be escaped inside a depot path, so the first `#` in a line beginning `//` is always the rev separator, whereas a filename may legitimately contain `" - "`. Name `TestSyncLineDepotPath` as the guard. State the hazard, cite the test; do not re-argue it at length.

- [ ] **Step 5: Run the table**

```
go test ./internal/agent/source/perforce/... -run TestSyncLineDepotPath -v -count=1 -timeout 60s
```

Expected: PASS, every row.

- [ ] **Step 6: Add `syncProgress` and its test**

Append to `syncprogress.go`:

```go
// syncProgress counts what a running p4 sync writes to stdout. onLine runs on
// the sync goroutine and snapshot on Prepare's; the mutex is what makes that
// legal. It holds no line buffer: p4 prints one line per file and a large sync
// is millions of them.
type syncProgress struct {
	mu               sync.Mutex
	files            int
	other            int
	lastPath         string
	freeDiskDisabled bool
}
```

with `func (s *syncProgress) onLine(line string)` (call `syncLineDepotPath`; on a non-empty result increment `files` and set `lastPath`, otherwise increment `other` and leave `lastPath` alone), `func (s *syncProgress) snapshot() (files, other int, lastPath string)`, and the sticky free-disk pair `func (s *syncProgress) freeDiskIsDisabled() bool` and `func (s *syncProgress) disableFreeDisk()`. All five take `s.mu`. `snapshot` returns a value copy; no caller ever holds a pointer into the struct (the "no interior pointers across locks" invariant).

Add `TestSyncProgress_CountsFilesAndOtherLines`: feed one depot-path line then one non-file line, and assert `snapshot()` returns `(1, 1, "//depot/x/a.ma")` - the third value proving a non-file line does not clear `lastPath`.

- [ ] **Step 7: Run the package**

```
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

Expected: PASS. Nothing outside the two new files has changed.

- [ ] **Step 8: Commit**

```bash
git add internal/agent/source/perforce/syncprogress.go internal/agent/source/perforce/syncprogress_test.go
git commit -m "perforce: count a p4 sync's stdout lines without buffering them"
```

---

## Task 2: The blocking-stream fixture seam

**Files:**
- Modify: `internal/agent/source/perforce/fixtures_test.go`

Test-only, no production change. Without it no cadence test is writable at all, and the temptation is to write the test the item itself warns against - one that calls the emitter directly and proves nothing about the timer.

- [ ] **Step 1: Add the fields and the setter**

Add to `fakeRunner`:

```go
	streamBlock map[string]<-chan struct{}
	streamDone  atomic.Int32
```

Initialise `streamBlock` in `newFakeP4Fixture` alongside the other maps. Add:

```go
// setStreamBlock makes Stream park on the given args key until release is
// closed or ctx is cancelled, modelling a long-running p4 sync. streamDone is
// incremented as Stream's LAST statement on every path, and the ctx path sleeps
// first, modelling a p4 child that takes a moment to die: a guard that asserts
// Prepare waited for the sync goroutine is decorative unless both hold.
func (f *fakeRunner) setStreamBlock(key string, release <-chan struct{}) {
	f.streamBlock[key] = release
}
```

- [ ] **Step 2: Add the branch to `Stream`**

At the top of `fakeRunner.Stream`, after `key := strings.Join(args, " ")` and **before** the `streamErr` and `streamOut` lookups:

```go
	if rel, ok := f.streamBlock[key]; ok {
		select {
		case <-rel:
		case <-ctx.Done():
			time.Sleep(50 * time.Millisecond)
			f.streamDone.Add(1)
			return ctx.Err()
		}
		f.streamDone.Add(1)
		return nil
	}
```

Then add `f.streamDone.Add(1)` as the last statement before each of the other three returns in `Stream` (the `streamErr` return, the missing-fixture return, and the success return after the `f.calls` append). Add `"sync/atomic"` and `"time"` to the imports.

Note in a comment above `streamDone` that `fakeRunner.calls` remains unsynchronised, so no test may read it or call `argHistory()` while `Prepare` is still running.

- [ ] **Step 3: Run the package**

```
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

Expected: PASS, unchanged from Task 0's baseline. Nothing calls `setStreamBlock` yet.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/source/perforce/fixtures_test.go
git commit -m "perforce: give the fake runner a blocking stream fixture"
```

---

## Task 3: `Config` fields and the summary renderer

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go`
- Create: `internal/agent/source/perforce/sync_heartbeat_test.go`

The renderer lands before the loop because every existing in-package test constructs a `Config` with no `FreeDiskGB`, so a renderer that is not nil-safe turns Task 4 into a mass panic that looks like a design failure.

- [ ] **Step 1: Write the failing renderer table**

Create `internal/agent/source/perforce/sync_heartbeat_test.go` with `TestProvider_SyncSummaryRendersFiveFixedFields`, covering:

- **The full contract, as one exact-string assertion.** Build a `syncProgress` by feeding `onLine` three depot-path lines, construct `p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}, FreeDiskGB: func(string) (int64, error) { return 811, nil }})`, and assert `p.syncSummary("[sync]", sp, 270*time.Second)` equals exactly
  `"[sync] 4m30s; 3 files; 0 other lines; 811 GB free; last //depot/x/c.ma"`.
  Exact equality is deliberate: this string is an operator-facing contract and a `Contains` check cannot see a field reordering.
- **Nil `FreeDiskGB`** -> the free field renders `- GB free`.
- **Erroring `FreeDiskGB`** -> `- GB free`, AND a call counter showing the function was invoked exactly once across TWO consecutive `syncSummary` calls (D10's sticky disable).
- **No file line yet** -> `last -`.
- **Zero elapsed** -> `0s`.
- **A `complete` prefix** -> the line begins `[sync] complete: ` and still carries all five fields.
- **The free-disk root**: give `FreeDiskGB` a recorder that captures its argument and assert it received `p.cfg.Root`, not a workspace subdirectory (D6 - the number in the log must be the number the sweeper and `RELAY_WORKSPACE_MIN_FREE_GB` act on).

- [ ] **Step 2: Run it and record the failure, labelling it correctly**

```
go test ./internal/agent/source/perforce/... -run TestProvider_SyncSummary -v -count=1 -timeout 60s
```

Expected: FAIL to compile - `p.cfg.FreeDiskGB undefined` and `p.syncSummary undefined`. **This is a compile failure, not behavioural evidence.** Record it as such; the behavioural RED for this task is Step 4.

- [ ] **Step 3: Add the `Config` fields**

```go
type Config struct {
	Root     string  // RELAY_WORKSPACE_ROOT - directory for all workspaces
	Hostname string  // worker hostname, used in client name; sanitized on New()
	Client   *Client // override for tests; nil -> exec real p4

	// SyncHeartbeatInterval is how often a running p4 sync emits a progress
	// summary. Zero or negative builds no ticker at all; the bracket lines still
	// carry a summary. RELAY_SYNC_HEARTBEAT_INTERVAL.
	SyncHeartbeatInterval time.Duration

	// FreeDiskGB reports free GIGABYTES on the volume holding root. Same
	// signature as Sweeper.FreeDiskGB so cmd/relay-agent passes the same
	// platform-gated helper to both; nil renders the field as "-" rather than
	// adding a second platform pair inside this package.
	FreeDiskGB func(root string) (int64, error)
}
```

`New()` needs no change: both fields keep their zero values when unset, and a nil `FreeDiskGB` is a supported production state.

- [ ] **Step 4: Write the happy-path-only renderer and watch the degenerate rows die**

Add `func (p *Provider) syncSummary(prefix string, sp *syncProgress, elapsed time.Duration) string`, deliberately WITHOUT the nil guard and WITHOUT the sticky disable - call `p.cfg.FreeDiskGB(p.cfg.Root)` unconditionally and render its result. Re-run Step 2's command.

Expected: PASS on the full-contract row; **PANIC (nil pointer dereference) on the nil row**; FAIL on the erroring row (the counter reads 2, not 1). Record both. This is the behavioural RED for the task, and it is exactly what a renderer written against only the happy path produces.

- [ ] **Step 5: Add the nil guard and the sticky disable**

If `p.cfg.FreeDiskGB == nil` or `sp.freeDiskIsDisabled()`, render `-`; otherwise call it, and on error call `sp.disableFreeDisk()` and render `-`. Format the whole line with one `fmt.Sprintf`:

`"%s %s; %d files; %d other lines; %s GB free; last %s"` with `elapsed.Round(time.Second)`, the two counts, the free string, and `lastPath` or `-`.

The doc comment states the operator-facing contract - five fields, fixed order, never omitted, input-derived path LAST so a clip truncates nothing structural - and names `TestProvider_SyncSummaryRendersFiveFixedFields`.

- [ ] **Step 6: Run the table, then the package**

```
go test ./internal/agent/source/perforce/... -run TestProvider_SyncSummary -v -count=1 -timeout 60s
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

Expected: PASS both.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/sync_heartbeat_test.go
git commit -m "perforce: render a five-field sync summary from the counter"
```

---

## Task 4: `-q` OUT, the inverted loop IN, per-file output counted and never forwarded - ONE COMMIT

**Files:**
- Modify: `internal/agent/source/perforce/client.go`
- Modify: `internal/agent/source/perforce/perforce.go`
- Modify: `internal/agent/source/perforce/perforce_progress_test.go`
- Modify: `internal/agent/source/perforce/perforce_test.go`
- Modify: `internal/agent/source/perforce/perforce_warm_test.go`
- Modify: `internal/agent/source/perforce/sync_heartbeat_test.go`

> **DO NOT COMMIT BETWEEN THE STEPS OF THIS TASK.** After Step 2 the tree has `-q` removed while `progress` is still `SyncStream`'s `onLine` callback. That tree puts the per-file volume of a multi-terabyte sync into `task_logs`, against a table with no per-task cap (`docs/backlog/bug-2026-08-14-task-logs-have-no-per-task-volume-cap.md`). There is no reviewable intermediate state, so there is no commit boundary here (D14). Run `git status` before you commit and confirm all six files are staged together.

- [ ] **Step 1: Drop `-q` from the argv and re-key `syncFixture`**

In `client.go`, change `SyncStream`'s args to `append([]string{"-c", client, "sync", "--parallel=4"}, specs...)` and rewrite its doc comment. It must quote the new argv and state the hazard the removal creates: the per-file lines are counted, never forwarded, because a large sync is millions of them and `task_logs` has no per-task cap. Name `TestProvider_PerFileSyncOutputIsCountedAndNeverForwarded` as the guard (the new name of the rewritten brackets test - Step 3).

In `perforce_progress_test.go`, drop `-q` from the key `syncFixture` returns.

- [ ] **Step 2: Run, watch the six other fixture keys miss, re-key them**

```
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

Expected: FAIL with `fakeRunner.Stream: no fixture for args "-c relay_h_... sync --parallel=4 ..."` from tests in `perforce_test.go` and `perforce_warm_test.go`. Record the message.

Now drop `-q` from the remaining six keys: five in `perforce_test.go`, one in `perforce_warm_test.go`. **Their fixture CONTENT does not change** (`"1 of 1 files\n"`, `"ok\n"`, `""`): none of those tests asserts on progress content, and those strings are now counted as `other` rather than as files, which changes nothing they measure.

Re-run. Expected: PASS. **This is the forbidden tree. Do not commit.**

- [ ] **Step 3: Rewrite the brackets test - fixture, assertions, name and comment**

In `perforce_progress_test.go`, rename `TestProvider_PrepareBracketsTheSyncWithExactlyOneStartAndOneCompleteLine` to `TestProvider_PerFileSyncOutputIsCountedAndNeverForwarded` and rewrite it.

Fixture content becomes three real p4 sync lines (REFUTED 1):

```go
	fr.setStream(syncKey,
		"//depot/x/a.ma#3 - added as /ws/a.ma\n"+
			"//depot/x/My File - Copy.ma#1 - updating /ws/My File - Copy.ma\n"+
			"//depot/x/c.ma#2 - refreshing /ws/c.ma\n")
```

Keep these three assertions verbatim:

```go
	assert.Equal(t, 1, countLinesContaining(lines, "[sync] starting"), ...)
	assert.Equal(t, 1, countLinesContaining(lines, "[sync] complete"), ...)
	assert.Equal(t, 1, countLinesContaining(lines, "1 path"), ...)
```

Delete the `"of 3 files"` assertion and add six in its place:

```go
	for _, verb := range []string{"added as", "updating", "refreshing"} {
		assert.Equal(t, 0, countLinesContaining(lines, verb),
			"p4's per-file output is counted, never forwarded: a large sync is millions of "+
				"lines and task_logs has no per-task cap, got: %v", lines)
	}
	assert.Equal(t, 1, countLinesContaining(lines, "3 files"),
		"and the count is what keeps the assertions above from being satisfied by a sync "+
			"that emitted nothing at all, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "0 other lines"),
		"every fixture line parsed as a depot path, got: %v", lines)
	assert.Equal(t, 1, countLinesContaining(lines, "last //depot/x/c.ma"),
		"lastPath tracks the LAST line, not the first, got: %v", lines)
```

Rewrite the comment above the test. It must carry the ORIGINAL comment's argument rather than delete it: the property inverts, the non-degeneracy concern does not. Say that the file COUNT is what now stops "no per-file line" from degenerating into "the sync emits nothing", and that the three verb probes and the count are two halves of one property.

- [ ] **Step 4: Run it and record the RED - this is the task's real behavioural evidence**

```
go test ./internal/agent/source/perforce/... -run TestProvider_PerFileSyncOutputIsCountedAndNeverForwarded -v -count=1 -timeout 60s
```

Expected: FAIL. The three verb counts read 1 each (forwarding is still live), `3 files` reads 0, `0 other lines` reads 0, `last //depot/x/c.ma` reads 0. **Record the actual numbers.** If the verb counts are already 0, the fixture did not take effect - stop and diagnose before writing any production code.

- [ ] **Step 5: Add the seams and the DELIBERATELY NAIVE loop**

In `perforce.go`, beside `lookPath` and `prepareAcquireHook`:

```go
// syncNow and newSyncTicker are the heartbeat's clock seams, following the
// package-level var pattern lookPath and prepareAcquireHook already use. The
// package has no t.Parallel() anywhere, so cross-test interference is not
// reachable; tests restore both in t.Cleanup.
var syncNow = time.Now

var newSyncTicker = func(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}
```

Add `func (p *Provider) runSyncWithHeartbeat(ctx context.Context, wsRoot, clientName string, specs []string, sp *syncProgress, progress func(string)) error` with the structure from the spec's 4.2: `errCh := make(chan error, 1)`, `go func() { errCh <- p.cfg.Client.SyncStream(ctx, wsRoot, clientName, specs, sp.onLine) }()`, an early `return <-errCh` when `p.cfg.SyncHeartbeatInterval <= 0`, then `start := syncNow()`, `tick, stopTick := newSyncTicker(...)`, `defer stopTick()`, and the loop - **but write the loop body naively for now**, emitting at the top of each iteration before selecting:

```go
	for {
		progress(p.syncSummary("[sync]", sp, syncNow().Sub(start)))
		select {
		case err := <-errCh:
			return err
		case <-tick:
		}
	}
```

This is not a typo. Step 7's cadence test kills it, and that death is the argument that the test observes the timer rather than the emitter.

Rewrite the `if needsSync` block in `Prepare`:

```go
		progress(fmt.Sprintf("[sync] starting: %d path(s)", len(syncSpecs)))
		sp := &syncProgress{}
		start := syncNow()
		if err := p.runSyncWithHeartbeat(ctx, wsRoot, clientName, syncSpecs, sp, progress); err != nil {
			handle.Release()
			progress(p.syncSummary("[sync] failed:", sp, syncNow().Sub(start)) +
				"; the cause is reported on the task's final status")
			return nil, classifyP4Error(fmt.Errorf("p4 sync: %w", err))
		}
		progress(p.syncSummary("[sync] complete:", sp, syncNow().Sub(start)))
```

The existing release-before-report comment stays and `handle.Release()` stays the FIRST statement in the failure branch (D9). Add one sentence to it saying the summary is rendered after the release because rendering reads free disk and a `statfs` on a wedged network volume is an uninterruptible block.

Add two comments on `runSyncWithHeartbeat`, each naming its guard and stating the hazard rather than re-arguing it:

- the single-caller property (`progress` holds a mutex across a send bounded only by the agent context - `internal/agent/runner.go`, `makePrepareProgressFn` and `send` - so a heartbeat goroutine calling it would block `Prepare`'s own completion line with the workspace handle still held). Guard: `TestProvider_TheHeartbeatNeverCallsProgressConcurrentlyWithPrepare`.
- exactly two select cases, and it must never gain a `ctx.Done()` arm: `exec.CommandContext` already kills p4 on cancellation and the `errCh` arm reports it, whereas returning on `ctx.Done()` would release the workspace while a live p4 child was still writing into it. Guard: `TestProvider_PrepareDoesNotReturnUntilTheSyncGoroutineHasFinished`.

Also note that `errCh` is buffered at 1 so a sync that finishes while the caller is parked in `progress` still completes its send and exits.

- [ ] **Step 6: Run the brackets test and the whole package**

```
go test ./internal/agent/source/perforce/... -run TestProvider_PerFileSyncOutputIsCountedAndNeverForwarded -v -count=1 -timeout 60s
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

Expected: PASS both. In particular `TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause` and `TestProvider_ASyncFailureReleasesTheWorkspaceBeforeItReportsAnything` must still pass unchanged - both key on the substring `[sync] failed`, which the extended line still contains exactly once. **If either goes red, stop:** the extension has changed the failure vocabulary and D3 is violated.

- [ ] **Step 7: Write the cadence test and kill the naive loop**

Add `TestProvider_ARunningSyncEmitsOneSummaryPerTickWithNoP4Output` to `sync_heartbeat_test.go`:

- Build the standard `syncFixture`, then `release := make(chan struct{})` and `fr.setStreamBlock(syncKey, release)`, so p4 produces **zero** lines. This is the item's "one tick and zero `onLine` calls" case and it is what proves the heartbeat is independent of p4's output.
- Swap `newSyncTicker` for one returning an **unbuffered** test-controlled `chan time.Time` and a no-op stop; swap `syncNow` for a stepping fake. Restore both in `t.Cleanup`.
- `Config` gets a non-zero `SyncHeartbeatInterval` and `FreeDiskGB: func(string) (int64, error) { return 811, nil }`.
- The progress recorder must be **mutex-guarded** - the test goroutine reads it while `Prepare` writes to it - and must expose a snapshot helper that copies the slice under the lock.
- Run `Prepare` on a goroutine.
- **Assert, in this order.** First: with NO tick delivered, wait a beat and assert no line containing `0 files` has appeared. *This is the half that kills the naive loop, and it is the half that is easy to leave out.* Second: send one tick, wait for one line (poll the guarded snapshot with a bounded deadline - never an unbounded `for`), and assert it contains `0 files`, `0 other lines`, `811 GB free` and `last -`. Third: `close(release)`, wait for `Prepare` to return, and assert the `[sync] complete:` line also carries a full summary.
- Do NOT call `fr.argHistory()` or read `fr.calls` before `Prepare` has returned (REFUTED 2).

- [ ] **Step 8: Run it against the naive loop and record the RED**

```
go test ./internal/agent/source/perforce/... -run TestProvider_ARunningSyncEmitsOneSummaryPerTick -v -count=1 -timeout 60s
```

Expected: FAIL on the FIRST assertion - a `0 files` line is already present with zero ticks delivered. Record the message. **If it passes, the naive loop was not written as Step 5 specifies and the test has no evidence behind it.**

- [ ] **Step 9: Fix the loop and re-run**

Move the emit into the tick arm:

```go
	for {
		select {
		case err := <-errCh:
			return err
		case <-tick:
			progress(p.syncSummary("[sync]", sp, syncNow().Sub(start)))
		}
	}
```

```
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

Expected: PASS.

- [ ] **Step 10: Fix the sixth falsified passage**

In `perforce_test.go`, `TestProvider_PrepareCreatesClientAndSyncs` asserts `require.NotEmpty(t, lines, "sync stream should have produced progress lines")`. After this change the sync stream produces none - the brackets do. Rewrite the message to say the brackets are what make the sync observable at all (REFUTED 4). Do not delete the assertion.

- [ ] **Step 11: Line-ending and encoding check, then ONE commit**

```
git status --porcelain
git ls-files --eol internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_progress_test.go internal/agent/source/perforce/perforce_test.go internal/agent/source/perforce/perforce_warm_test.go internal/agent/source/perforce/sync_heartbeat_test.go
git diff --stat
```

Every path must read `i/lf`. The diffstat must be proportionate to the change you intended - if it is in the hundreds of lines for a file you touched in one place, a CRLF rewrite has happened and you must fix it before committing. Confirm no non-ASCII byte entered any file (the carriage return in Task 1's table is a `\r` escape, not a literal byte).

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_progress_test.go internal/agent/source/perforce/perforce_test.go internal/agent/source/perforce/perforce_warm_test.go internal/agent/source/perforce/sync_heartbeat_test.go
git commit -m "perforce: drop sync -q and heartbeat the sync from Prepare's own goroutine

The per-file lines p4 now writes are COUNTED and never forwarded to progress:
a large sync is millions of lines and task_logs has no per-task cap
(docs/backlog/bug-2026-08-14-task-logs-have-no-per-task-volume-cap.md). The two
changes are one commit because the intermediate tree - -q gone, progress still
the onLine callback - is the defect.

The sync runs on a goroutine and the heartbeat loop stays on Prepare's, so
progress keeps exactly one caller. The reverse (a heartbeat goroutine calling
progress, as the backlog item proposes) deadlocks Prepare: progress holds a
mutex across a send bounded only by the agent context.

This does NOT fix bug-2026-09-03-provider-progress-parks-while-holding-the-workspace.
It adds no new instance of it, and it does raise the number of blocking
opportunities per sync from two to duration/interval."
```

---

## Task 5: The disabled timer, and the two mutation guards

**Files:**
- Modify: `internal/agent/source/perforce/sync_heartbeat_test.go`

- [ ] **Step 1: Write `TestProvider_ADisabledHeartbeatBuildsNoTickerAndStillSummarises`**

`SyncHeartbeatInterval: 0`, and swap `newSyncTicker` for one that calls `t.Fatal` if it is ever constructed (restore in `t.Cleanup`). `t.Fatal` is legal here only because `newSyncTicker` is called on `Prepare`'s goroutine and this test runs `Prepare` on the test goroutine - **do not run `Prepare` on a goroutine in this test.** Assert `Prepare` succeeds and the `[sync] complete:` line still carries a full summary.

The fatal-on-construction shape is what stops a "disabled" implementation that builds a ticker and discards its ticks. A test that merely asserted no heartbeat line appeared would pass against that implementation.

- [ ] **Step 2: Write `TestProvider_PrepareDoesNotReturnUntilTheSyncGoroutineHasFinished`**

`setStreamBlock` on the sync key with a release channel the test never closes. `ctx, cancel := context.WithCancel(...)`. Run `Prepare` on a goroutine, wait until it is inside the sync (poll for the `[sync] starting` line via the guarded recorder, bounded), then `cancel()`. Wait for `Prepare` to return. **The instant it returns**, assert `fr.streamDone.Load() == int32(1)` (REFUTED 2) - the fake increments that counter as `Stream`'s last statement, after a 50 ms sleep on the cancel path, so a `Prepare` that returned early cannot have observed it.

Label it in a comment as a **regression guard, not a red-first criterion**: it is green at HEAD vacuously, because at HEAD the sync is not on a goroutine at all. Its RED is Task 6's mutation, and the comment must say so.

- [ ] **Step 3: Write `TestProvider_TheHeartbeatNeverCallsProgressConcurrentlyWithPrepare`**

The progress recorder does, per call: `if atomic.AddInt32(&inFlight, 1) != 1 { t.Error("progress called concurrently") }`, then `time.Sleep(2 * time.Millisecond)`, then `atomic.AddInt32(&inFlight, -1)`, then append under the mutex. `t.Error` is safe from a non-test goroutine; `t.Fatal` is not - do not use it here.

The fixture must arrange overlap two ways, and both must be present: send **several ticks back to back** on the unbuffered test tick channel (with the correct implementation each send blocks until the previous `progress` returns, so there is no overlap; with `go progress(...)` two recorder calls overlap inside the 2 ms sleep), and then close `release` while a tick is in flight so a spawned emit can overlap `Prepare`'s own `[sync] complete:` line.

Label it a regression guard too, and say in the comment that its RED is Task 7's mutation - the one a future reader will "simplify" the design back into.

An atomic in-flight counter is used rather than relying on `-race` alone **because this property must redden in `make test`**, which on this machine is the only lane that reliably runs.

- [ ] **Step 4: Run the three tests, then the package**

```
go test ./internal/agent/source/perforce/... -run "TestProvider_ADisabledHeartbeat|TestProvider_PrepareDoesNotReturn|TestProvider_TheHeartbeatNeverCalls" -v -count=1 -timeout 120s
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/sync_heartbeat_test.go
git commit -m "perforce: guard the disabled timer and the heartbeat's two structural properties"
```

---

## Task 6: MUTATION - add a `ctx.Done()` arm to the select

**Files:** none committed. This is verification.

- [ ] **Step 1: Copy the file before touching it**

Copy `internal/agent/source/perforce/perforce.go` to the scratchpad directory. **Never restore with `git checkout --`**: it discards uncommitted work in the tree and is the documented way to destroy a guard under test.

- [ ] **Step 2: Apply the mutation**

Add a third case to `runSyncWithHeartbeat`'s select:

```go
		case <-ctx.Done():
			return ctx.Err()
```

- [ ] **Step 3: Run the guard and a control**

```
go test ./internal/agent/source/perforce/... -run TestProvider_PrepareDoesNotReturnUntilTheSyncGoroutineHasFinished -v -count=1 -timeout 60s
go test ./internal/agent/source/perforce/... -run TestProvider_ADisabledHeartbeatBuildsNoTickerAndStillSummarises -v -count=1 -timeout 60s
```

Expected: the first FAILS (`fr.streamDone.Load()` is 0 when `Prepare` returns); the second PASSES. The control matters: a uniform result across both means the harness is broken, not that the mutation was killed. **Trace which assertion failed and confirm it is the `streamDone` one** - a mutation may redden a test for a reason unrelated to the guard it is meant to pin.

If the guard does NOT redden, it is decorative. Stop, restore, and rewrite it before the slice proceeds.

- [ ] **Step 4: Restore from the copy and re-run the control**

Copy the saved file back, then:

```
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
git diff --stat internal/agent/source/perforce/perforce.go
```

Expected: PASS, and an EMPTY diffstat. A non-empty diffstat means the restore was lossy - fix it before continuing.

---

## Task 7: MUTATION - `go progress(...)` in the tick arm

**Files:** none committed. This is verification. **This is the most important mutation in the slice:** it is the item's literal proposal and it is what a future reader will simplify the design back into.

- [ ] **Step 1: Copy the file**

Copy `internal/agent/source/perforce/perforce.go` to the scratchpad directory under a distinct name.

- [ ] **Step 2: Apply the mutation**

Change the tick arm to `go progress(p.syncSummary("[sync]", sp, syncNow().Sub(start)))`.

- [ ] **Step 3: Run the guard and a control in the default lane**

```
go test ./internal/agent/source/perforce/... -run TestProvider_TheHeartbeatNeverCallsProgressConcurrentlyWithPrepare -v -count=1 -timeout 60s
go test ./internal/agent/source/perforce/... -run TestProvider_ARunningSyncEmitsOneSummaryPerTick -v -count=1 -timeout 60s
```

Expected: the first FAILS with `progress called concurrently`. Record the message. The second is the control - record whether it passed. **The RED must be in the default lane**, not only under `-race`; that is the whole reason for the atomic counter.

- [ ] **Step 4: Run it under `-race` in the Linux container as well - both instruments, one claim**

From the Bash tool, in the worktree root:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

Expected: a data-race report on the progress recorder. **If Docker is unavailable, say `-race` did not run** and report the default-lane result alone. Do not substitute `-count=N`: repetition raises confidence in flakiness, not in race-freedom.

- [ ] **Step 5: Restore and re-run**

Copy the saved file back, then:

```
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
git diff --stat internal/agent/source/perforce/perforce.go
```

Expected: PASS, empty diffstat.

---

## Task 8: `resolveSyncHeartbeatInterval`

**Files:**
- Modify: `cmd/relay-agent/main.go`
- Modify: `cmd/relay-agent/main_test.go`

- [ ] **Step 1: Write the failing table**

Add `TestResolveSyncHeartbeatInterval` to `main_test.go`, capturing log output the way `TestParseDurationEnv_LogsWarningOnInvalidNonEmptyInput` already does (`log.SetOutput(&buf)` with a `defer log.SetOutput(os.Stderr)`). Rows:

| Input | Expect | Warning |
|---|---|---|
| `""` | 30s | none - assert `require.Empty(t, buf.String())`, mirroring `TestParseDurationEnv_NoWarningOnEmptyInput` |
| `"0s"` | 0 | none |
| `"45s"` | 45s | none |
| `"2m"` | 2m | none |
| `"0"` | 30s | yes, naming `RELAY_SYNC_HEARTBEAT_INTERVAL` and echoing `0` |
| `"-30s"` | 30s | yes, naming the var |
| `"1s"` | 30s | yes, naming the var AND mentioning the floor |
| `"garbage"` | 30s | yes, naming the var |

Add a comment to the test stating plainly that this covers the PARSING only: **the assignment of the result into `perforce.Config` in `main()` is not covered by this or any other test, because `cmd/relay-agent` has no env-to-field wiring guard of any kind** (spec F5, D8). Deleting that assignment line compiles and leaves every package green, exactly like the six existing unguarded assignments beside it.

- [ ] **Step 2: Run it**

```
go test ./cmd/relay-agent/... -run TestResolveSyncHeartbeatInterval -v -count=1 -timeout 60s
```

Expected: FAIL to compile - `undefined: resolveSyncHeartbeatInterval`.

- [ ] **Step 3: Implement it**

In `main.go`, beside `parseDurationEnv`:

```go
const (
	defaultSyncHeartbeat = 30 * time.Second
	syncHeartbeatFloor   = 5 * time.Second
)

// resolveSyncHeartbeatInterval reads RELAY_SYNC_HEARTBEAT_INTERVAL. "0s"
// disables the timer. A bare "0", a negative value and any other unparseable
// input take parseDurationEnv's warn-and-fall-back path, because the shared
// regex has no unit-less or signed form. A positive value below
// syncHeartbeatFloor is refused with its own warning and falls back too: the
// only cost of this knob is durable task_logs rows, which nothing caps yet
// (docs/backlog/bug-2026-08-14-task-logs-have-no-per-task-volume-cap.md).
func resolveSyncHeartbeatInterval(v string) time.Duration {
	d := parseDurationEnv("RELAY_SYNC_HEARTBEAT_INTERVAL", v, defaultSyncHeartbeat)
	if d > 0 && d < syncHeartbeatFloor {
		log.Printf("warning: RELAY_SYNC_HEARTBEAT_INTERVAL=%q is below the %v minimum; using %v",
			v, syncHeartbeatFloor, defaultSyncHeartbeat)
		return defaultSyncHeartbeat
	}
	return d
}
```

- [ ] **Step 4: Run the table and the package**

```
go test ./cmd/relay-agent/... -run TestResolveSyncHeartbeatInterval -v -count=1 -timeout 60s
go test ./cmd/relay-agent/... -count=1 -timeout 120s
```

Expected: PASS. The three existing `parseDurationEnv` tests must be untouched and green.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-agent/main.go cmd/relay-agent/main_test.go
git commit -m "relay-agent: resolve RELAY_SYNC_HEARTBEAT_INTERVAL with a 5s floor"
```

---

## Task 9: Wire the two fields in `main()`

**Files:**
- Modify: `cmd/relay-agent/main.go`

- [ ] **Step 1: Extend the `perforce.Config` literal**

In the `if root := os.Getenv("RELAY_WORKSPACE_ROOT"); root != ""` block:

```go
		pp := perforce.New(perforce.Config{
			Root:                  root,
			Hostname:              caps.Hostname,
			SyncHeartbeatInterval: resolveSyncHeartbeatInterval(os.Getenv("RELAY_SYNC_HEARTBEAT_INTERVAL")),
			FreeDiskGB:            freeDiskGB,
		})
```

`freeDiskGB` is the same identifier already passed to `Sweeper.FreeDiskGB` further down the same block. No new file, no new build tag, no adaptation (spec F2).

- [ ] **Step 2: Build for BOTH platforms and vet**

```
go build ./cmd/relay-agent/...
go vet ./cmd/relay-agent/...
```

Then cross-build the other GOOS, because CI never compiles the Windows file (`docs/backlog/idea-2026-09-01-go-ci-never-compiles-or-runs-windows-code.md`). From PowerShell: `$env:GOOS="windows"; go build ./cmd/relay-agent/...; $env:GOOS=""`. From the Bash tool: `GOOS=windows go build ./cmd/relay-agent/...` and `GOOS=linux go build ./cmd/relay-agent/...`. Expected: success on both.

- [ ] **Step 3: Run the package**

```
go test ./cmd/relay-agent/... -count=1 -timeout 120s
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-agent/main.go
git commit -m "relay-agent: pass the heartbeat interval and free-disk helper to the perforce provider

This assignment is not covered by any test: cmd/relay-agent has no env-to-field
wiring guard (docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md
is an open idea, and every guard it describes lives in cmd/relay-server).
Deleting these two lines compiles and leaves every package green."
```

---

## Task 10: The p4d integration lane - RECORD, do not predict

**Files:**
- Modify: `internal/agent/source/perforce/perforce_integration_test.go`

**Requires Docker and `p4` on PATH.** If either is missing, this task cannot be done - say so plainly and stop. **Do not substitute the default lane:** `fakeRunner` echoes whatever it is told and can prove nothing about what real p4 writes.

> **Nobody knows what p4 r25.2 emits for the one-file fixture once `-q` is dropped.** This plan does NOT bake in a predicted `1 files`. Step 2 is an observation step and Step 3 writes the assertion from what you observed.

- [ ] **Step 1: Rewrite the first prepare's comment and instrument the callback**

Replace the false comment above the first `prov.Prepare` call. Both of its clauses are now wrong: the sync no longer emits zero lines on success, and the callback is no longer retained only for diagnostics. The new comment says what the assertion below it pins - some file lines reach `onLine`, none of that text reaches `progress`, and the count surfaces in the completion bracket.

Change the callback to accumulate into a `[]string` (as the second prepare already does) as well as calling `t.Logf`.

- [ ] **Step 2: RUN IT AND RECORD what p4 actually says**

```
go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -v -count=1 -timeout 1800s
```

Read the `[sync] complete:` line out of the test log **verbatim** and write it into your notes and, later, into the commit message. The file count for a single `readme.txt` baseline may be `1 files`; it may not be, because p4's stdout for a one-file sync can also carry totals lines that `syncLineDepotPath` classifies as `other`. **The observation is what to trust, not this plan and not the spec.**

- [ ] **Step 3: Write the assertion from the observation**

Assert the first prepare's `[sync] complete:` line contains the file count you actually observed, with a message saying this is the end-to-end proof that dropping `-q` reached real p4 and that the counter is wired to it. Also assert that no line in the recorded slice contains `readme.txt` - the end-to-end proof that the per-file text did not reach `progress`.

- [ ] **Step 4: Rewrite the second prepare's comment; keep `require.Len(t, progress2, 2)` at 2**

The `-q` reason in the existing comment is gone, but its conclusion survives for a different reason, and that is the interesting part: an up-to-date client is reported by p4 on **stderr**, which `execRunner.Stream` discards on a zero exit. Say that, and cross-reference `docs/backlog/bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero.md` so the next reader finds the item rather than re-deriving it.

Keep the length assertion at 2 and add a sentence saying it now depends on two facts: the summary rides on the existing `[sync] complete` bracket rather than adding a third line (D3), and the 30s default does not fire during a sub-second no-op sync. **Do not loosen it to a range** - it is the only end-to-end assertion that per-file output does not reach `progress`.

**Do NOT assert on `0 files` for the second prepare.** It is not a discriminating observation: p4 reports an up-to-date client on stderr, so `0 files` reads the same before and after this change, and it is equally the correct reading for a legitimately empty subtree and for a sync that matched nothing.

- [ ] **Step 5: Re-run the lane**

```
go test -tags integration -p 1 ./internal/agent/source/perforce/... -count=1 -timeout 1800s
```

Expected: PASS. Record the result and say explicitly that Docker and `p4` were both available.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/perforce_integration_test.go
git commit -m "perforce: assert the sync count against real p4d, and fix two false -q comments

Observed against p4d r25.2 for the single-readme.txt fixture:
<paste the [sync] complete: line verbatim here>"
```

---

## Task 11: README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add the agent env-table row**

Immediately after the `RELAY_EVICTION_TIMEOUT` row in the agent env table, add:

```
| `RELAY_SYNC_HEARTBEAT_INTERVAL` | How often a running `p4 sync` writes a progress summary to the task log (`4m30s; 12483 files; 0 other lines; 811 GB free; last //depot/...`). Default `30s`. **`0s` disables the timer**; the completion line still carries a summary. A bare `0`, a negative value, or an interval under `5s` is refused with a warning and the default is used. The file count is a lower bound: p4 block-buffers its output to a pipe, so it moves in bursts while the elapsed field moves continuously. |
```

The block-buffering caveat is in the row on purpose: it is the one thing about this feature that will generate a false bug report, and the row is where an operator meets it.

- [ ] **Step 2: Add one sentence to "Source workspaces"**

Near the eviction paragraph (the one listing "Oldest workspaces (LRU) when free disk drops below `RELAY_WORKSPACE_MIN_FREE_GB`"), add one sentence naming the heartbeat as the way to tell a live multi-hour sync from a wedged one, and stating that the free-space figure it reports is the same volume `RELAY_WORKSPACE_MIN_FREE_GB` is compared against.

- [ ] **Step 3: Verify the edit did not damage the file**

```
git diff --stat README.md
git ls-files --eol README.md
```

The diffstat must show roughly two added lines, not hundreds. `git ls-files --eol` must read `i/lf`. **Neither addition may contain a non-ASCII byte** - check, because a raw Latin-1 byte survives every gate this repo runs and `git diff` alone cannot see it.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document RELAY_SYNC_HEARTBEAT_INTERVAL and the sync heartbeat"
```

---

## Task 12: CONDITIONAL - `execRunner.Stream` never checks `sc.Err()`

**Files:**
- Modify: `internal/agent/source/perforce/client.go`
- Modify: `internal/agent/source/perforce/client_error_test.go`

**This task is optional and labelled so it can be dropped without invalidating anything before it.** `execRunner.Stream` scans stdout and calls `cmd.Wait()` without ever reading `sc.Err()`. At HEAD that branch is close to unreachable, because `-q` produces almost no stdout. **This slice is what makes it reachable**, and its failure mode is a silent undercount in the exact number the summary is built from: a `bufio.ErrTooLong` (a line over the 1 MB `sc.Buffer` cap) or a pipe read error ends the loop, `Wait()` returns nil, and the log reads a confident file count for a truncated sync.

- [ ] **Step 1: Decide, and record the decision either way**

The fix edits the same six lines as `docs/backlog/bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero.md`, and it converts a would-have-succeeded sync into a prepare failure. Testing it needs a real subprocess emitting an over-long line, via the stdlib `TestHelperProcess` re-exec pattern - the seam exists, because `execRunner` takes a `binary` field and `client_error_test.go` already constructs one directly.

**If the helper-process test proves awkward, DROP the code change and tell the conductor to file an item** naming `execRunner.Stream`'s unchecked `sc.Err()` and the undercount it produces. **Never ship the fix untested, and never ship a comment in place of a guard.** Say which you did.

- [ ] **Step 2 (only if proceeding): Write the failing test**

Add a test to `client_error_test.go` that builds an `execRunner` whose `binary` re-execs the test binary as a helper process, has the helper write a single line longer than 1 MB to stdout and exit 0, and asserts `Stream` returns a non-nil error mentioning the scan failure.

- [ ] **Step 3 (only if proceeding): Run it**

```
go test ./internal/agent/source/perforce/... -run TestExecRunner -v -count=1 -timeout 120s
```

Expected: FAIL - `Stream` returns nil today.

- [ ] **Step 4 (only if proceeding): Fix `Stream`**

```go
	if err := cmd.Wait(); err != nil {
		return newP4CommandError(args, err, stderr.String())
	}
	if err := sc.Err(); err != nil {
		return newP4CommandError(args, err, stderr.String())
	}
	return nil
```

- [ ] **Step 5 (only if proceeding): Run and commit**

```
go test ./internal/agent/source/perforce/... -count=1 -timeout 300s
```

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/client_error_test.go
git commit -m "perforce: fail a sync whose stdout scan errored instead of undercounting it"
```

---

## Task 13: Whole-slice verification

**Files:** none. No commit.

- [ ] **Step 1: The full default lane**

```
go test ./... -count=1 -timeout 900s
```

Expected: PASS. Compare against Task 0's baseline package by package.

- [ ] **Step 2: Vet under both tag sets**

```
go vet ./...
go vet -tags integration ./...
```

Expected: clean.

- [ ] **Step 3: The perforce integration lane**

```
go test -tags integration -p 1 ./internal/agent/source/perforce/... -count=1 -timeout 1800s
```

Expected: PASS. **Report SKIP as SKIP** if Docker or `p4` is missing. A skip is not a green.

- [ ] **Step 4: `-race` across the whole tree in the Linux container**

This slice needs it more than most: it introduces the package's first production goroutine handoff, and the container is also the only local way to run the `//go:build !windows` files that `go test` on Windows silently skips.

From the Bash tool, in the worktree root:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 900s
```

Expected: PASS, zero data races. **If the container lane is unavailable, say `-race` did not run.** Do not substitute `-count=N`: it re-runs under the ordinary scheduler and cannot observe an unsynchronised access that never happens to interleave badly.

If you see `ThreadSanitizer failed to allocate ... (error code: 87)`, that is the known environmental failure, not a regression - re-run, and if it persists, re-run an untouched package at `origin/main` to measure the red both ways before concluding anything.

- [ ] **Step 5: Line endings and encoding across the whole diff**

```
git diff --stat origin/main
git ls-files --eol $(git diff --name-only origin/main)
```

Every touched path must read `i/lf`, and the diffstat must be proportionate. Confirm every touched file still decodes as UTF-8.

- [ ] **Step 6: Confirm nothing outside scope moved**

```
git diff --name-only origin/main
```

Expected: only the files in the File structure table. **No `web/` file, no `web/dist`, no `*.sql`, no `*.sql.go`, no `models.go`, no `*.proto`.** If any appears, revert it.

---

## Things the PR body must say

- **This does NOT fix `bug-2026-09-03-provider-progress-parks-while-holding-the-workspace`.** The design adds no new instance of it and landing the `sendOrAbort` fix later needs no change here - but the heartbeat moves the expected number of blocking opportunities per sync from 2 to `duration/interval`. A reviewer reading "the heartbeat cannot make progress blocking worse" will otherwise read it as "progress blocking is fixed". The item stays open.
- **`0 files` is not a failure signal.** A sync that matched nothing now renders `0 files` where before it rendered nothing, but that is also the correct reading for an already-up-to-date workspace, a legitimately empty subtree, and a block-buffered stream that flushed nothing before exit. `bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero` is on **stderr** and stays filed, unchanged; no "0 files means failure" rule is added here.
- **The `[sync] complete:` line observed against real p4d**, verbatim, from Task 10 Step 2.
- **Which lanes ran and which skipped**, naming Docker and `p4` availability and whether `-race` ran.

## Backlog work for the conductor (this plan files nothing)

The spec's section 11 proposes four amendments and one conditional new item. Route these to the conductor; **four of the five are amendments to existing open items, not new items**:

1. `docs/backlog/bug-2026-08-14-task-logs-have-no-per-task-volume-cap.md` - amend with the figure this slice produces: **120 rows and roughly 14-17 KB per hour per syncing task** at the 30s default, because `makePrepareProgressFn` flushes on `time.Since(lastFlush) >= 500ms`, so at a 30s cadence every heartbeat line is its own flush and its own row.
2. `docs/backlog/bug-2026-09-03-provider-progress-parks-while-holding-the-workspace.md` - amend with the raised exposure (2 blocking opportunities per sync becomes `duration/interval`).
3. `docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md` - amend to record `cmd/relay-agent` as a second entirely-unguarded package, enumerating its unguarded assignments, and to note that the agent's provider construction is inline in `main()` so the executed-test route needs a `buildWorkspaceProvider` extraction first.
4. A candidate NEW item, low priority: move the free-disk sample onto the sweeper's existing goroutine and have the heartbeat read a cached value, so no `statfs` runs on a goroutine holding a workspace (D10 records the exposure and the sticky disable; it cannot be given a timeout, because Go cannot cancel a blocking syscall).
5. A NEW item **if and only if Task 12 is dropped**: `execRunner.Stream` never checks `sc.Err()`, and this slice is what makes that branch reachable.

## Phase 4 lens routing

- **Invariants lens.** Two questions: does any path let `Prepare` return while the sync goroutine lives (Task 6's mutation), and does anything other than `Prepare`'s own goroutine call `progress` (Task 7's mutation). Also confirm the failure path's `handle.Release()` still precedes every new statement, including the free-disk read.
- **Correctness lens.** `syncLineDepotPath`'s table against real p4 output shapes, especially the `" - "`-in-a-filename row and the no-`#` row. The three verb probes plus the count in `TestProvider_PerFileSyncOutputIsCountedAndNeverForwarded`, which are two halves of one property and easy to break by editing the fixture. And whether Task 12 shipped or was filed.
- **Security lens.** The depot path is p4-derived text on its way to `task_logs` and then to the SPA. Confirm the control-byte strip and the 200-character clip, that clipping happens AFTER stripping, and that the path is last in the line so a clip truncates nothing structural. Ask specifically whether a file named so that the rendered line reads as another bracket can mislead an operator, and whether the closed set of four numeric fields plus one clipped tail is actually closed.
- **Integration tester.** The p4d lane (Task 10). The observed completion line is the only evidence in the slice that dropping `-q` reached real p4 - confirm it was RECORDED, not predicted. Confirm the second prepare still emits exactly two lines. Confirm `-race` in the Linux container across the whole tree.

## Self-review

**Spec coverage.** R0 -> Task 0. R1 -> Task 2. R2 + R5 + D14 -> Task 4 (one commit). R3 -> Task 1. R4 -> Task 4 Steps 7-9. R6 -> Task 3. R7 -> Task 5 Step 1. R8 -> Task 8. R9 -> Task 5 Step 2 + Task 6. R10 -> Task 5 Step 3 + Task 7. R11 -> Task 10. R12 -> Task 13. R13 -> Task 12. Section 7.1's five comment rewrites -> Task 4 (client.go's doc, the brackets test's comment, the `perforce.go` sync-block comments) and Task 10 (both integration comments); the sixth, which the spec missed, -> Task 4 Step 10. Section 7.2's README edits -> Task 11. Section 11's backlog candidates -> the conductor list above. D6's `Config.FreeDiskGB` -> Tasks 3 and 9. Section 4.6's `main.go` wiring -> Task 9.

**Type consistency.** `syncProgress`'s fields and methods (`onLine`, `snapshot`, `freeDiskIsDisabled`, `disableFreeDisk`) are defined in Task 1 and used unchanged in Tasks 3, 4 and 5. `syncSummary(prefix string, sp *syncProgress, elapsed time.Duration) string` is defined in Task 3 and called with the same three arguments in Task 4's loop and both brackets. `runSyncWithHeartbeat(ctx, wsRoot, clientName, specs, sp, progress)` has one definition and one call site. `Config.FreeDiskGB func(root string) (int64, error)` matches `Sweeper.FreeDiskGB` byte for byte, which is what lets Task 9 pass the same identifier to both. `setStreamBlock(key string, release <-chan struct{})` and `streamDone atomic.Int32` are defined in Task 2 and used in Tasks 4 and 5.

**Known gap, stated rather than papered over.** The assignment of `SyncHeartbeatInterval` and `FreeDiskGB` into the `perforce.Config` literal in `main()` is covered by no test in this plan and by no guard in the repo. Deleting either line compiles and leaves every package green. That is recorded in Task 8's test comment, in Task 9's commit message, and in the backlog amendment above; building a guard is `idea-2026-08-14-generalize-the-env-to-field-wiring-guard`'s whole scope and is deliberately out of scope here (D8).
