---
date: 2026-09-04
topic: p4-sync-progress-heartbeat
status: draft
covers:
  - docs/backlog/feature-2026-09-03-p4-sync-progress-heartbeat.md
---

# A timer-driven progress heartbeat for a long `p4 sync`

## 0. How this spec was produced

Gate mode was `autonomous`, so every place the brainstorming flow would put a question to a human,
the call is made here with the reasoning written down, and every such call is listed again in
section 9 so it is cheap to overturn.

**The tree moved under this backlog item on the day it was written and again the day after.** The
client-path slice (`c2a7eae`, spec `docs/superpowers/specs/2026-09-04-perforce-client-path-addressing.md`)
rewrote `Prepare` end to end and changed `ResolveHead`'s signature. The prepare-failure-visibility
slice (`01d3179`) changed the `[sync]` bracket lines and added the release-before-report ordering.
Every claim below was re-read off the tree at `c2a7eae`, not off the item.

Three numbering schemes appear and they are deliberately distinct. **F1-F12** are findings from
verifying the item (section 1). **R0-R12** are red-first implementation steps (section 6).
**D1-D14** are decisions (section 9).

---

## 1. Verification of the backlog item

### 1.1 The CURRENT sync call site, read off `internal/agent/source/perforce/perforce.go:334-359`

The item describes a call site that no longer looks the way it describes. This is what is there
now, verbatim in structure:

```go
if needsSync {
    progress(fmt.Sprintf("[sync] starting: %d path(s)", len(syncSpecs)))
    if err := p.cfg.Client.SyncStream(ctx, wsRoot, clientName, syncSpecs, progress); err != nil {
        handle.Release()
        progress("[sync] failed; the cause is reported on the task's final status")
        return nil, classifyP4Error(fmt.Errorf("p4 sync: %w", err))
    }
    progress("[sync] complete")
    if curOK { _ = reg.Mutate(shortID, func(e *WorkspaceEntry) { ... }) }
    _ = reg.Save()
}
```

Five properties of that block matter to everything below.

1. **`progress` IS the `onLine` callback today.** It is passed straight into `SyncStream` as the
   fifth argument. The item's Proposal 2 ("the per-file lines feed only `onLine`; nothing forwards
   them to `progress`") is therefore not a small addition: it is the removal of an existing wiring
   that a live test asserts on (F3).
2. **`handle.Release()` precedes the failure line, deliberately**, pinned by
   `TestProvider_ASyncFailureReleasesTheWorkspaceBeforeItReportsAnything`, which observes the holder
   count from inside the callback. Anything this slice adds between the failure and the release
   re-opens the window that test exists to close.
3. **The failure line carries no cause**, pinned by
   `TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause`. The item's `stop(err)` emitting
   "`FAILED` and the error" contradicts this directly (F4).
4. `SyncStream` runs on `Prepare`'s own goroutine, so **`progress` has exactly one caller goroutine
   today**. That is the property the item's design would give up, and section 4.2 is about why it
   must not be.
5. `progress` is called while the workspace handle is held for `[sync] starting` and for every p4
   output line; the failure line alone is outside the hold.

### 1.2 The headline claim is CONFIRMED

`SyncStream` (`client.go:166-169`) is:

```go
args := append([]string{"-c", client, "sync", "-q", "--parallel=4"}, specs...)
return c.r.Stream(ctx, cwd, args, onLine)
```

`-q` is present. `execRunner.Stream` (`client.go:70-96`) wires `cmd.StdoutPipe()` into a
`bufio.Scanner` and calls `onLine(sc.Text())` per line. With `-q`, p4 writes essentially nothing to
stdout on a successful sync, so on a multi-hour sync `onLine` is never called and `progress` emits
exactly two lines: `[sync] starting: N path(s)` at the beginning and `[sync] complete` at the end.
**A stalled sync and a live sync are byte-identical in the task log for their whole duration.**
The mechanism the item describes is real.

The item's second mechanism (p4 block-buffers stdout to a pipe) is also real and matters more than
the item uses it for: see F8.

### 1.3 FINDING F1 (the largest one, and it changes the design): the item's own architecture would
create the bug it is filed next to

The item asks for "a goroutine [that] emits a summary every interval through the `progress`
callback". Read `progress` at its production definition, `internal/agent/runner.go:692-726`:

```go
func (r *Runner) makePrepareProgressFn() (progress func(line string), flush func()) {
    var mu sync.Mutex
    var buf []string
    var lastFlush time.Time
    doFlush := func() { ...; r.send(&relayv1.AgentMessage{ ... }) }
    progress = func(line string) {
        mu.Lock()
        defer mu.Unlock()
        buf = append(buf, line)
        if time.Since(lastFlush) >= 500*time.Millisecond || len(buf) >= 50 { doFlush() }
    }
    flush = func() { mu.Lock(); defer mu.Unlock(); doFlush() }
    return
}
```

and `r.send` (`runner.go:654-660`):

```go
func (r *Runner) send(msg *relayv1.AgentMessage) {
    select {
    case r.sendCh <- msg:
    case <-r.ctx.Done():
    }
}
```

`r.ctx` is the **agent** context. So `progress` blocks until the coordinator drains a 64-slot
`sendCh` or the agent process shuts down. That is
`bug-2026-09-03-provider-progress-parks-while-holding-the-workspace`, confirmed at HEAD, and this
slice does not fix it.

**Three consequences the item does not account for, and the third is a deadlock.**

- `progress` **holds `mu` while parked.** It is not merely slow; it is a mutual-exclusion point.
- A heartbeat goroutine calling `progress` makes `progress` a **concurrently-called** function for
  the first time. `mu` makes that memory-safe. It does not make it live.
- If the heartbeat goroutine is parked in `progress` holding `mu`, then `Prepare`'s own
  `progress("[sync] complete")` blocks on `mu`, and `stop()` joining that goroutine blocks on the
  same thing. **`Prepare` never returns, so the workspace handle is never released, so every later
  task for that stream parks in `Workspace.Acquire`.** `Runner.Run`'s own `flushProgress()`
  (`runner.go:189`) takes the same `mu` and would park too. Today the equivalent park at least
  leaves `Prepare` on the stack of the call that is blocked; with the item's shape the blocked
  caller and the blocking caller are different goroutines and the hold is transitive.

The task brief called this the most important thing in the spec and it is. **The design therefore
inverts the goroutine** (section 4.2): the SYNC moves to a goroutine and the heartbeat loop stays on
`Prepare`'s own goroutine, so `progress` keeps exactly one caller. See D1.

### 1.4 FINDING F2 (confirmation, stronger than the item states): the free-disk helper is reachable
with no second platform pair, and the item's preferred field name is the one that would force one

`cmd/relay-agent/free_disk_unix.go` and `free_disk_windows.go`, quoted exactly:

```go
//go:build !windows
func freeDiskGB(path string) (int64, error) {
    var s syscall.Statfs_t
    if err := syscall.Statfs(path, &s); err != nil { return 0, err }
    return int64(s.Bavail) * int64(s.Bsize) / (1024 * 1024 * 1024), nil
}
```

```go
//go:build windows
func freeDiskGB(path string) (int64, error) {
    p, err := windows.UTF16PtrFromString(path)
    if err != nil { return 0, err }
    var freeBytes uint64
    if err := windows.GetDiskFreeSpaceEx(p, &freeBytes, nil, nil); err != nil { return 0, err }
    return int64(freeBytes / (1024 * 1024 * 1024)), nil
}
```

Answers to the three questions the task brief asks:

- **The real symbol is `freeDiskGB`, unexported, in package `main` of `cmd/relay-agent`.**
- **It returns GIGABYTES, truncated by integer division, not bytes.** Both files divide before
  returning. A 999 MB free volume reports `0`.
- **It is already injected**, into `Sweeper.FreeDiskGB func(root string) (int64, error)`
  (`sweeper.go:57`), from `cmd/relay-agent/main.go:101` (`FreeDiskGB: freeDiskGB`). The identical
  move into the provider needs one new `Config` field and one new line in `main.go`. **CONFIRMED
  reachable with no second platform pair.**

**REFUTATION of the item's Proposal 4 as phrased.** It offers "`Config.FreeDiskBytes` or an
adaptation of the `FreeDiskGB`". `FreeDiskBytes` is not producible from the existing helper: both
platform files truncate to GB before returning, so a bytes-granular field would require editing
**both** platform files, which is exactly the doubled Windows-uncompiled exposure the item's own
next sentence forbids. The field must be `FreeDiskGB func(root string) (int64, error)` - byte-for-byte
the signature `Sweeper` already takes - so `main.go` passes the same identifier to both. D6.

### 1.5 FINDING F3 (REFUTATION): a live test asserts the OPPOSITE of acceptance criterion 2

`perforce_progress_test.go:44-66`, quoted exactly:

```go
// The brackets are one line each, and per-file progress is a separate concern.
// The "of 3 files" assertion is what keeps "no per-file line of the provider's
// own" from degenerating into "the sync emits nothing": p4's own output must
// still reach progress unchanged.
func TestProvider_PrepareBracketsTheSyncWithExactlyOneStartAndOneCompleteLine(t *testing.T) {
    ...
    assert.Equal(t, 3, countLinesContaining(lines, "of 3 files"),
        "p4's own output must still reach progress unchanged, got: %v", lines)
}
```

The item's acceptance criterion "Per-file sync output never reaches `task_logs`" is the negation of
that assertion. The item does not mention it. This is not a fixture re-key: it is a deliberate
reversal of a property with a written rationale, and the rationale must be preserved rather than
deleted.

**The rationale is preservable and the replacement is better.** The comment's real concern is
non-degeneracy: an assertion of the form "no per-file lines" is satisfied vacuously by a sync that
produced nothing at all. The replacement non-degeneracy assertion is the summary's **file count**:
the final line must read `3 files` while none of the three file lines appears anywhere in `lines`.
That pins both halves at once, and it is strictly stronger than the current assertion because it
also proves the counter is wired to `onLine` rather than to nothing. R5.

### 1.6 FINDING F4 (REFUTATION): `stop(err)` emitting `COMPLETE:` / `FAILED: <err>` collides with
two live tests and one recorded rule

The item's Proposal 1 asks for "one final `COMPLETE:` or `FAILED:` line" and its test list asks for
"`stop(err)` after a failure emits `FAILED` and the error".

- The package already has a completion vocabulary: `[sync] complete` and `[sync] failed`, each
  pinned at exactly one occurrence by `countLinesContaining` in two tests. A second vocabulary for
  the same two events puts the outcome in the log twice in two spellings.
- **"and the error" is forbidden by the guard that already exists.**
  `TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause` asserts no progress line contains the
  cause sentinel, with a comment saying in terms that a reviewer will want to add the error text
  back and that the test is what refuses it. The item is that reviewer.

**Decision: the final summary IS the existing bracket, extended.** `[sync] complete: <summary>` and
`[sync] failed: <summary>; the cause is reported on the task's final status`. One line per event,
one vocabulary, both existing tests survive with their `countLinesContaining` shape intact. D3.

### 1.7 FINDING F5 (REFUTATION): the prescribed wiring guard does not exist for this binary

The item says "Wire it through the same env-to-field path the other duration knobs use, and add it
to the wiring guard ([[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]])."

- The linked file is `docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md`. It
  **exists** and is **open**, and it is an **idea**, not a shipped mechanism.
- Every guard it discusses lives in `cmd/relay-server`: `TestTrailingLogWindowIsWiredIntoTheHandler`
  (`trailing_log_window_test.go`), `TestServerCountersIsWiredByMain` (`counters_wiring_test.go`),
  `TestWatchdogIsStartedByMain`. The item's own title enumerates `Metrics`, `AllowAutoEnroll` and
  two duration knobs, all `worker.Handler` / `api.Server` fields.
- **`cmd/relay-agent` has no wiring guard at all.** Its only test file is `main_test.go` and its only
  tests are `TestParseDurationEnv`, `TestParseDurationEnv_LogsWarningOnInvalidNonEmptyInput` and
  `TestParseDurationEnv_NoWarningOnEmptyInput`.

**So "add it to the wiring guard" names a thing that does not exist for the binary this feature ships
in.** Deleting the new `SyncHeartbeatInterval:` line from the `perforce.Config` literal in
`cmd/relay-agent/main.go` will compile and leave every package green - exactly like the six existing
unguarded assignments beside it (`Root`, `Hostname`, and the sweeper's `MaxAge`, `MinFreeGB`,
`SweepInterval`, `FreeDiskGB`, `ListLocked`, `Claim`, `OnEvictedCB`).

Building the guard is out of scope (D8); what IS in scope is making the **parsing** testable in
`cmd/relay-agent` the way `parseDurationEnv` already is, so only the one assignment line is
unguarded rather than the whole knob. Section 11 proposes amending the idea item to record
`cmd/relay-agent` as a second entirely-unguarded package.

### 1.8 FINDING F6 (REFUTATION): `RELAY_SYNC_HEARTBEAT_INTERVAL=0` does not disable anything with
the parser the other agent duration knobs use

The item says "`0` disables the timer". `cmd/relay-agent/main.go:159-185`:

```go
var durRe = regexp.MustCompile(`^(\d+)([smhd])$`)

func parseDurationEnv(name, v string, fallback time.Duration) time.Duration {
    if v == "" { return fallback }
    m := durRe.FindStringSubmatch(v)
    if m == nil {
        log.Printf("warning: %s=%q is not a valid duration (want e.g. 14d, 8h, 30m); using fallback %v", name, v, fallback)
        return fallback
    }
    ...
}
```

A bare `"0"` has no unit, so it does **not** match, logs a warning, and returns the fallback. With a
30s fallback, `RELAY_SYNC_HEARTBEAT_INTERVAL=0` would produce a 30s heartbeat and a warning. The
item's spelling is wrong.

Two further facts from the same regex, both useful:

- **A negative value is unrepresentable.** `-30s` does not match `^(\d+)([smhd])$`, so it takes the
  warn-and-fall-back path with no extra guard. The task brief asks what a negative value does; the
  answer is "logs a warning naming the var and uses the default", and it needs no new code.
- **There are three different env-duration conventions in the agent already**, which is why "the
  same env-to-field path the other duration knobs use" is ambiguous: `parseDurationEnv`'s regex
  (`RELAY_WORKSPACE_MAX_AGE`, `RELAY_WORKSPACE_SWEEP_INTERVAL`), an inline `time.ParseDuration` with
  `d > 0` (`RELAY_TELEMETRY_INTERVAL`, `main.go:114-118`), and a package-var resolver using
  `time.ParseDuration` inside the provider package itself (`RELAY_EVICTION_TIMEOUT`,
  `sweeper.go:32-43`). D7 picks one and says why.

### 1.9 FINDING F7 (confirmation, and it closes a hazard the item never checked): `preparing` is in
`AppendTaskLog`'s allow-list, so these lines actually persist

The heartbeat's lines travel as `LOG_STREAM_PREPARE` chunks while the task's row sits in status
`preparing`. CLAUDE.md's Invariants warn that a non-terminal status omitted from `AppendTaskLog`'s
allow-list discards 100% of that state's log output with no error and no log line anywhere. Checked
rather than assumed, `internal/store/query/tasks.sql:352`:

```sql
AND (t.status IN ('pending', 'dispatched', 'preparing', 'running')
     OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
```

`preparing` is present, and the comment above it names `makePrepareProgressFn` as the reason.
**CONFIRMED: the feature's entire output has a durable home.** Had `feature-2026-09-03-preparing-task-status`
not shipped this arm, every heartbeat line would have been silently dropped and the feature would
have been untestable end to end. Nothing to do; recorded because it is the single cheapest way this
slice could have shipped a no-op.

### 1.10 FINDING F8: block buffering makes the file count a LAGGING lower bound, which the item's
Summary uses correctly as an argument and its Proposal 3 then over-claims

The item's Summary says "p4 block-buffers its stdout to a pipe in any case", which is why a timer is
needed. True, and it has a consequence for the *content* of the summary that the item does not draw:
the file **count** inherits the same lag. libc block-buffers a non-tty stdout at 4-8 KB; p4 sync
lines run about 60-100 bytes, so the count arrives in bursts of roughly 40-130 files and sits still
in between. On a multi-hour sync that lag is noise. On the first minute of any sync, and on a small
sync, it is the whole signal.

**So the summary must not be advertised as "files synced so far".** The field that always moves is
elapsed time; the file count is a lower bound with a buffer-sized lag. State this in the README row
and in the code comment, or the first operator to see `0 files` at 30s into a healthy sync will file
a bug against the heartbeat. D5 and section 7.2.

### 1.11 FINDING F9 (confirmation): the integration-test comment the item names is false, and there
are TWO of them, plus a length assertion that will break

The item names one comment. Quoted exactly from `perforce_integration_test.go:56-60`:

> `// Note: progress callback is not asserted on here. Production code runs`
> `// `p4 sync -q` which suppresses per-file output entirely; with the`
> `// fixture's single readme.txt baseline, the sync emits zero lines on`
> `// success. We retain the callback to surface unexpected `[recover] ...``
> `// diagnostic lines in test output if a crash-recovery path fires.`

The second, at lines 94-97, which the item does not mention:

> `// Asserting the progress callback saw nothing cannot express "no re-sync",`
> `// because `p4 sync -q` on an up-to-date single-file workspace prints nothing`
> `// either way - the same reason the first prepare above does not assert on it.`
> `// The bracket lines are what make the sync observable at all, so assert them.`

And a live assertion three lines below it that this slice breaks:

```go
require.Len(t, progress2, 2, "the two brackets and nothing else, got: %v", progress2)
```

That stays at 2 under D3 (the summary rides on the existing `[sync] complete` bracket rather than
adding a third line) **provided the heartbeat does not fire during the test's sub-second no-op sync**,
which at a 30s default it will not. Keep the assertion, keep it at 2, and add a comment saying which
of the two facts it now depends on. If the assertion is loosened to a range, the slice loses its only
end-to-end proof that per-file output does not reach `progress`.

### 1.12 FINDING F10 (confirmation): both cited backlog slugs exist and are correctly spelled

- `docs/backlog/bug-2026-08-14-task-logs-have-no-per-task-volume-cap.md` - **exists, open.** The
  item's slug is exact. Section 3.1 supplies the figure it asks for.
- `docs/backlog/idea-2026-09-01-go-ci-never-compiles-or-runs-windows-code.md` - **exists, open.**

### 1.13 FINDING F11: three of the item's four "Related" cross-links are now CLOSED

| Cross-link | State | Effect |
|---|---|---|
| `bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync` ("land after it") | **closed**, shipped as `c2a7eae` | The precondition is satisfied. This is why the call site looks different from the item's description. |
| `feature-2026-09-03-preparing-task-status` | **closed** | Good news, and load-bearing: F7. |
| `feature-2026-09-03-classify-out-of-disk-p4-errors` | **closed** | `classifyP4Error` already turns a full-disk sync failure into operator guidance. The heartbeat's free-space field is complementary (it shows the approach to the wall), not a duplicate. |

None of the three should be carried into the commit message as open work.

### 1.14 FINDING F12: the fixture re-key surface, enumerated rather than counted

`-q` leaves the argv, so every `fakeRunner` stream fixture key changes. Enumerated with
`rg "sync -q --parallel=4"` over the tree, and reported by file because a bare total is not
checkable:

| File | Hits | Kind |
|---|---|---|
| `internal/agent/source/perforce/client.go` | 1 | the production argv - the change itself |
| `internal/agent/source/perforce/perforce_test.go` | 5 | fixture keys |
| `internal/agent/source/perforce/perforce_progress_test.go` | 1 | `syncFixture`, which feeds three tests |
| `internal/agent/source/perforce/perforce_warm_test.go` | 1 | fixture key |

Seven test-side keys in three files, one production site. `docs/superpowers/specs/*` and
`docs/superpowers/plans/*` also match; they are **records of a moment and must not be edited**
(the same rule section 7.3 of the client-path spec applies to itself).

`provider_evict_test.go`, `provider_evict_recheck_test.go`, `sweeper_claim_test.go` and
`perforce_orphan_test.go` have no hits: they never reach the sync. No change there.

A text search is the right instrument for this one claim because the subject IS a literal string in
an argv, so the instrument matches the claim. It is the wrong instrument for anything behavioural
below, and is not used for anything else.

---

## 2. What this slice does NOT do

1. **It does not fix `bug-2026-09-03-provider-progress-parks-while-holding-the-workspace`.** The
   design is built so that the heartbeat adds no new instance of it (4.2) and so that landing the
   `sendOrAbort` fix later requires no change here. The item stays open. Say this in the PR body;
   a reviewer reading "the heartbeat cannot make progress blocking worse" will otherwise read it as
   "progress blocking is fixed".
2. **It does not close, or partially close,
   `bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero`.** Section 3.3 is the analysis
   the task brief asks for; the short answer is that the "nothing matched" family is on **stderr**
   and dropping `-q` touches **stdout** only.
3. **No `task_logs` volume cap.** `bug-2026-08-14-task-logs-have-no-per-task-volume-cap` gets a
   number from 3.1 and stays open.
4. **No env-to-field wiring guard for `cmd/relay-agent`.** F5, D8.
5. **No change to `Workspace`, the registry, the sweeper, or eviction.** The only file outside the
   sync call site is `client.go`'s `SyncStream` argv, `perforce.Config`, and `cmd/relay-agent/main.go`.
6. **No per-file line reaches `progress`, and no allow-list of "interesting" p4 lines is added.**
   D4 records why a phrase allow-list is refused, and what replaces the information it would carry.
7. **No buffering of p4 output.** The counter is O(1) in memory. Nothing accumulates lines.

---

## 3. The invariants, load, and threat model, up front

Checked against CLAUDE.md's Invariants before any design choice.

- **End the generation before releasing the resource.** This slice adds a goroutine inside the
  workspace hold, so the rule applies in its general form. The sync goroutine's only exit is
  `errCh <- SyncStream(...)` on a **buffered (cap 1)** channel, and the receive is the single exit
  from the heartbeat loop. There is no path on which `Prepare` returns while the sync goroutine is
  still running, so no released handle can be observed by a still-live continuation. **The loop must
  have exactly two `select` cases (`errCh` and the tick) and must never gain a `ctx.Done()` case**:
  a `ctx.Done()` arm would let `Prepare` return, release the handle, and let a sweep begin
  `os.RemoveAll` while a live `p4` child is still writing into the tree. `exec.CommandContext`
  already kills p4 on ctx cancellation, so the `errCh` arm covers cancellation correctly and the
  extra case would be pure harm. R9 is the mutation that proves it.
- **Identity-checked teardown.** No new registry, sender or handle ownership. The `handle.Release()`
  before the failure line stays byte-identical and stays first.
- **Epoch fence.** No `tasks.status` or `task_logs` write is in this package. The chunks travel
  through `makePrepareProgressFn`, which already stamps `r.epoch`. F7 confirms the store-side
  allow-list admits them.
- **One bounded sender per gRPC stream.** Unchanged in mechanism and this is where the design earns
  its shape: `progress` keeps **exactly one caller goroutine**, so no new send path is introduced and
  the existing send discipline is not widened. 4.2.
- **Single job-spec pipeline / Single JSON entry point.** Not touched.
- **No interior pointers across locks.** `syncProgress` returns a value snapshot from its getter;
  the caller never holds a pointer into it. `p.mu` and `ws.mu` are not held anywhere in the new code.

### 3.1 Load, and the figure `bug-2026-08-14` asked for

Per syncing task at the 30s default: **120 progress lines per hour.**

Whether that is 120 `task_logs` **rows** depends on `makePrepareProgressFn`'s batcher, which flushes
on `time.Since(lastFlush) >= 500ms || len(buf) >= 50`. At a 30s cadence every line is more than 500 ms
after the last flush, so **each heartbeat line is its own flush and its own row**: 120 rows per hour
per syncing task, at roughly 110-140 bytes of content each, so about **14-17 KB per hour per syncing
task**. That is the figure to quote in `bug-2026-08-14-task-logs-have-no-per-task-volume-cap`.

Fleet scale: the heartbeat only runs while a task is in `preparing` with `needsSync` true. A worker
runs a bounded number of concurrent tasks and a stream syncs once per baseline change, so the
realistic steady state is single-digit concurrent heartbeats per worker.

Per-line cost of dropping `-q`: `execRunner.Stream` already allocates one string per line via
`sc.Text()`; the new `onLine` adds one uncontended mutex acquisition (~20 ns) and one
`strings.IndexByte`. A 1M-file sync is therefore about 20 ms of added CPU and 60 MB of short-lived
allocation spread over hours. Negligible in rate. **What is NOT negligible is that p4 now writes
that output at all**, which is real pipe traffic p4 was not producing before; it is what the feature
buys and it is bounded by the sync's own size.

Free-disk cost: one `statfs` / `GetDiskFreeSpaceEx` per heartbeat line, on the same root the sweeper
already polls every `RELAY_WORKSPACE_SWEEP_INTERVAL`. See D10 for the one hazard this introduces.

### 3.2 Threat model delta

The agent is the actor. Two new pieces of attacker-influenced text reach `task_logs`.

1. **The depot path.** It is p4's own stdout, derived from files in the stream a job spec named. A
   file whose name contains a newline, a carriage return or ANSI control bytes could otherwise break
   one heartbeat line into several, or forge a line that reads as another bracket
   (`[sync] complete: ...`) and mislead an operator reading the log. **`syncLineDepotPath` therefore
   strips every byte below 0x20 and clips to 200 characters**, and the path is placed **last** in
   the summary so a clipped value truncates nothing else. This is the "render nothing input-derived
   and assert a closed set" shape applied where it is cheap; the closed set here is the four
   fixed-position numeric fields, and only the trailing path is input-derived. Route to the Phase 4
   security lens.
2. **The file count and the free-space number.** Both are integers rendered by `%d`. No path.

Nothing new is executed, no new subprocess, no new file, no new network destination. The `-q` removal
does not change what p4 is asked to do, only how much it says about it.

### 3.3 Interaction with `bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero`
(the task brief's second required question)

**Does dropping `-q` make the "nothing matched" case observable? No.**

That item's mechanism, re-verified against `client.go:70-96`: `execRunner.Stream` sets
`cmd.Stderr = &stderr` and reads that buffer **only** inside `newP4CommandError`, which is reached
only when `cmd.Wait()` returns non-nil. The item's measured repro shows p4 printing
`- file(s) not in client view.` on **stderr** and exiting **zero**. `-q` governs p4's informational
**stdout**. Removing it adds per-file stdout lines; it does not route stderr anywhere new and it does
not change the exit code. The whole "nothing matched" family is still discarded.

**Does this slice give the item anything? One weak lever, and it must not be mistaken for the fix.**
A sync that matched nothing now renders `0 files` in its `[sync] complete` line, where before it
rendered nothing at all. That is an observable, and it is **not** a guard, because `0 files` is also
the correct reading for (a) an already-up-to-date workspace, which p4 reports on stderr and which the
integration test's second prepare exercises, and (b) a legitimately empty subtree, which the item's
own Proposal flags as undecided, and (c) a block-buffered stream that flushed nothing before exit
(F8).

**Decision: the item stays filed, unchanged, and this slice adds no "0 files means failure" rule.**
Adding one here would be a new failure for cases that work today, decided inside a feature slice,
against a criterion that item explicitly says must be decided in writing first. Record this
reasoning in the commit message so a later reader does not read `0 files` as the guard. D12.

---

## 4. Design

All production changes are in `internal/agent/source/perforce/` plus two lines in
`cmd/relay-agent/main.go` and one README table row.

### 4.1 New file: `internal/agent/source/perforce/syncprogress.go`

```go
// syncProgress counts what a running p4 sync writes to stdout. onLine runs on
// the sync goroutine, snapshot on Prepare's; the mutex is what makes that legal.
// It holds no line buffer: p4 prints one line per file and a large sync is
// millions of them.
type syncProgress struct {
    mu       sync.Mutex
    files    int    // lines that parsed as a depot path
    other    int    // lines that did not
    lastPath string // sanitized and clipped; see syncLineDepotPath
}

func (s *syncProgress) onLine(line string)
func (s *syncProgress) snapshot() (files, other int, lastPath string)

// syncLineDepotPath returns the depot path a p4 sync output line names, or ""
// for a line that is not a file line.
func syncLineDepotPath(line string) string
```

**`syncLineDepotPath`'s rule, and why it is not a split on `" - "`.** A p4 sync line is
`//depot/path/file.ext#3 - added as C:\ws\file.ext`. Splitting on `" - "` is wrong: a filename may
contain that sequence (`My File - Copy.txt`) and would truncate the depot path. p4 requires `@ # % *`
to be **escaped** inside a depot path (as `%40 %23 %25 %2A`), so the FIRST `#` in a line that begins
`//` is always the rev separator. The rule is therefore:

1. If the line does not begin with `//`, return `""`.
2. Find the first `#`. If there is none, return `""`.
3. Take the prefix, strip every byte below 0x20, clip to 200 characters, return it.

Table rows for R3, each discriminating a different mutation:

| Input | Expect | Discriminates |
|---|---|---|
| `//depot/x/a.ma#3 - added as /ws/a.ma` | `//depot/x/a.ma` | the happy path |
| `//depot/x/My File - Copy.ma#1 - updating C:\ws\My File - Copy.ma` | `//depot/x/My File - Copy.ma` | a `" - "` split, which returns `//depot/x/My File` |
| `//depot/x/b.ma#2 - deleted as /ws/b.ma` | `//depot/x/b.ma` | an action allow-list that only knows `added`/`updating` |
| `//depot/x/c.ma#5 - refreshing C:\ws\c.ma` | `//depot/x/c.ma` | a POSIX-only local-path assumption |
| `File(s) up-to-date.` | `""` | a rule that does not require the `//` prefix |
| `` (empty) | `""` | an unguarded index |
| `//depot/x/no-rev.ma - added as /ws/no-rev.ma` | `""` | a rule that falls back to `" - "` when `#` is absent |
| `//depot/x/a.ma#3 - added as /ws/a.ma` with an embedded `\r` in the path | no `\r` in the result | the control-byte strip (3.2) |
| a 400-character depot path | 200 characters | the clip (3.2) |

The last two rows are the security lens's; they belong in the same table because they are properties
of the same function.

### 4.2 The inverted loop, which is the whole safety argument (F1)

New unexported method on `Provider`, in `perforce.go` beside `Prepare`:

```go
// runSyncWithHeartbeat runs the sync on its own goroutine and stays on the
// CALLER's goroutine emitting periodic summaries, so progress keeps exactly one
// caller. A heartbeat goroutine calling progress instead would deadlock Prepare:
// progress holds a mutex across a send bounded only by the agent context
// (internal/agent/runner.go, makePrepareProgressFn and send), so a parked
// heartbeat would block Prepare's own completion line, the runner's
// flushProgress, and any join - with the workspace handle still held.
// TestProvider_TheHeartbeatNeverCallsProgressConcurrentlyWithPrepare.
//
// Exactly two select cases, and it must never gain a ctx.Done() arm:
// exec.CommandContext already kills p4 on cancellation and the errCh arm
// reports it, whereas returning on ctx.Done() would release the workspace while
// a live p4 child was still writing into it.
// TestProvider_PrepareDoesNotReturnUntilTheSyncGoroutineHasFinished.
func (p *Provider) runSyncWithHeartbeat(
    ctx context.Context, wsRoot, clientName string, specs []string,
    sp *syncProgress, progress func(string),
) error {
    errCh := make(chan error, 1)
    go func() { errCh <- p.cfg.Client.SyncStream(ctx, wsRoot, clientName, specs, sp.onLine) }()

    if p.cfg.SyncHeartbeatInterval <= 0 {
        return <-errCh
    }
    start := syncNow()
    tick, stopTick := newSyncTicker(p.cfg.SyncHeartbeatInterval)
    defer stopTick()
    for {
        select {
        case err := <-errCh:
            return err
        case <-tick:
            progress(p.syncSummary("[sync]", sp, syncNow().Sub(start)))
        }
    }
}
```

Properties, each of which is a design commitment rather than an accident:

- **`errCh` is buffered at 1.** If the caller is parked in `progress` when the sync finishes, the
  sync goroutine still completes its send and exits. An unbuffered channel would leak it for the
  duration of the park.
- **No goroutine other than the caller ever touches `progress`.** This is the F1 fix.
- **A park in `progress` does not lose the sync.** p4 keeps running; `onLine` keeps counting on the
  sync goroutine. On unpark the loop sees at most one buffered tick (`time.Ticker` drops the rest),
  emits one stale summary, and continues. At most one extra line per park.
- **Cancellation is not a special case.** Ctx cancellation kills p4, `SyncStream` returns an error,
  the `errCh` arm runs, and the existing failure path releases the handle.
- **`stop(err)` from the item does not exist as a symbol.** Its two jobs - end the emitter and emit
  the final line - are the loop's return and the caller's existing bracket. Nothing to stop, nothing
  to join, no done channel to leak-check. F4, D3.

Two package-level test seams, matching the `lookPath` / `prepareAcquireHook` pattern already in this
package (which has no `t.Parallel()` anywhere, so package vars are safe here):

```go
var syncNow = time.Now
var newSyncTicker = func(d time.Duration) (<-chan time.Time, func()) {
    t := time.NewTicker(d)
    return t.C, t.Stop
}
```

### 4.3 The rewritten call site

```go
if needsSync {
    progress(fmt.Sprintf("[sync] starting: %d path(s)", len(syncSpecs)))
    sp := &syncProgress{}
    start := syncNow()
    if err := p.runSyncWithHeartbeat(ctx, wsRoot, clientName, syncSpecs, sp, progress); err != nil {
        // RELEASE FIRST, unchanged and for the unchanged reason. The summary is
        // rendered AFTER the release because rendering reads free disk, and a
        // statfs on a wedged network volume is an uninterruptible block.
        handle.Release()
        progress(p.syncSummary("[sync] failed:", sp, syncNow().Sub(start)) +
            "; the cause is reported on the task's final status")
        return nil, classifyP4Error(fmt.Errorf("p4 sync: %w", err))
    }
    progress(p.syncSummary("[sync] complete:", sp, syncNow().Sub(start)))
    ...
}
```

`start` is taken twice (here and inside `runSyncWithHeartbeat`) rather than threaded, because the
two measure different things: the loop's elapsed is heartbeat-relative and the bracket's is
call-relative. They differ by nanoseconds. If a reviewer prefers one, thread it as a parameter; do
not derive one from the other, since a shared mutable start is how an elapsed field silently becomes
"time since the last heartbeat".

### 4.4 The summary format, which is an operator-facing contract

**Five fields, fixed order, always all present, `; ` separated, input-derived text last.**

```
[sync] 4m30s; 12483 files; 0 other lines; 811 GB free; last //depot/art/hero_body.ma
[sync] complete: 47m12s; 41231 files; 0 other lines; 802 GB free; last //depot/art/tail.ma
[sync] failed: 12m3s; 918 files; 0 other lines; 2 GB free; last //depot/art/x.ma; the cause is reported on the task's final status
```

| Field | Source | When unavailable |
|---|---|---|
| elapsed | `time.Duration.Round(time.Second).String()` | never |
| `<n> files` | lines that parsed as a depot path | never; `0 files` is meaningful |
| `<n> other lines` | lines p4 wrote that did not parse as a file line | never; `0 other lines` is the normal reading |
| `<n> GB free` | `Config.FreeDiskGB(p.cfg.Root)` | `- GB free` |
| `last <path>` | the last parsed depot path, sanitized and clipped | `last -` |

Rationale for each choice, since this becomes a contract:

- **Fixed fields, never omitted.** An omitted field is indistinguishable from a truncated line or a
  feature that is not wired. `-` is unambiguous and greppable.
- **Field ORDER is the operator's question order.** Is it moving (elapsed, and the file delta between
  two consecutive lines)? Is it going to fit (free)? Where is it (path)?
- **`other lines` is what replaces a per-file passthrough**, and it is the honest form of the drop:
  it discloses the loss where the summary is READ, without reproducing attacker-influenced text and
  without a phrase allow-list. On a normal sync it is `0`. A non-zero value tells an operator p4 said
  something that was not a file line, which is a reason to look at the task's final status. D4.
- **`GB free`, not a byte figure and not a delta.** F2 fixes the unit at GB. D2 kills the delta.
- **The `[sync] starting: N path(s)` line is unchanged.** It is emitted before any counting exists
  and its `1 path` substring is asserted by an existing test.

**`Round(time.Second)` renders `0s` for a sub-second sync and `1h2m3s` for a long one.** Stdlib
formatting, so there is no formatter to test and no locale to get wrong.

### 4.5 `SyncStream` and `Config`

```go
// SyncStream runs `p4 -c <client> sync --parallel=4 <specs...>` from cwd,
// streaming lines to onLine. NOT -q: the per-file lines are the only evidence a
// multi-hour sync is moving, and the heartbeat counts them rather than
// forwarding them (a 2 TB sync is millions of lines and task_logs has no
// per-task cap). TestProvider_PerFileSyncOutputIsCountedAndNeverForwarded.
func (c *Client) SyncStream(ctx context.Context, cwd, client string, specs []string, onLine func(string)) error {
    args := append([]string{"-c", client, "sync", "--parallel=4"}, specs...)
    return c.r.Stream(ctx, cwd, args, onLine)
}
```

```go
type Config struct {
    Root     string
    Hostname string
    Client   *Client

    // SyncHeartbeatInterval is how often a running p4 sync emits a progress
    // summary. Zero or negative disables the timer; the bracket lines still
    // carry a summary. RELAY_SYNC_HEARTBEAT_INTERVAL.
    SyncHeartbeatInterval time.Duration

    // FreeDiskGB reports free gigabytes on the volume holding root. Identical
    // signature to Sweeper.FreeDiskGB so cmd/relay-agent passes the same
    // platform-gated helper to both; nil renders the field as "-" rather than
    // adding a second platform pair inside this package.
    FreeDiskGB func(root string) (int64, error)
}
```

`New()` leaves both at their zero values when unset. A nil `FreeDiskGB` is a supported production
state (every existing in-package test constructs `Config` without it), so the renderer must be
nil-safe and R6 pins that.

### 4.6 `cmd/relay-agent` wiring

```go
heartbeat := resolveSyncHeartbeatInterval(os.Getenv("RELAY_SYNC_HEARTBEAT_INTERVAL"))
pp := perforce.New(perforce.Config{
    Root:                  root,
    Hostname:              caps.Hostname,
    SyncHeartbeatInterval: heartbeat,
    FreeDiskGB:            freeDiskGB,
})
```

`FreeDiskGB: freeDiskGB` is the same identifier already passed at `main.go:101` to
`Sweeper.FreeDiskGB`. No new file, no new build tag, no adaptation. F2.

```go
// resolveSyncHeartbeatInterval reads RELAY_SYNC_HEARTBEAT_INTERVAL.
// "0s" disables the timer. A bare "0", a negative value and any other
// unparseable input take parseDurationEnv's warn-and-fall-back path, because
// the shared regex has no unit-less or signed form. A positive value below
// syncHeartbeatFloor is refused with its own warning and falls back too:
// the only cost of this knob is durable task_logs rows, which nothing caps yet
// (docs/backlog/bug-2026-08-14-task-logs-have-no-per-task-volume-cap.md).
func resolveSyncHeartbeatInterval(v string) time.Duration
```

with `defaultSyncHeartbeat = 30 * time.Second` and `syncHeartbeatFloor = 5 * time.Second`. It is a
thin wrapper over the existing `parseDurationEnv`, so `cmd/relay-agent` gains one testable function
and one untestable assignment line rather than an untestable knob (F5).

---

## 5. Where a property cannot go red at HEAD

Stated before the sequence so no step below has to hedge.

- **`syncLineDepotPath`'s table** cannot go red at HEAD by absence: a test for a function that does
  not exist fails to compile, and a compile failure is not evidence about behaviour. R3 introduces a
  **stub that splits on `" - "` and returns the prefix** - the plausible-wrong implementation, not a
  panic - and takes its RED against that. Rows 2, 5, 6, 7 and the two sanitizer rows all redden for
  a behavioural reason, and the stub's death is the argument for the `#` rule.
- **The cadence test** (R4) cannot go red at HEAD at all: there is no timer. Its RED is against a
  **naive implementation that calls `emit()` at the top of the loop instead of on the tick**, which
  the item itself warns about: "The fork's test calls `emit()` directly and proves nothing about the
  timer; a cadence test must observe the consumer." The test therefore drives a **fake ticker** and
  a **blocking stream fixture**, and asserts that with zero ticks delivered, zero heartbeat lines
  appear; then that after exactly one tick, exactly one appears. **Zero-ticks-zero-lines is the half
  that kills the naive implementation** and it is the half that is easy to leave out.
- **`TestProvider_PrepareDoesNotReturnUntilTheSyncGoroutineHasFinished`** is **green at HEAD,
  vacuously**, because at HEAD the sync is not on a goroutine. It is labelled a regression guard, not
  a red-first criterion. Its RED is the R9 mutation (adding a `case <-ctx.Done()` arm).
- **The concurrency guard** `TestProvider_TheHeartbeatNeverCallsProgressConcurrentlyWithPrepare` is
  also green at HEAD vacuously. Its RED is the R10 mutation (moving the emit into its own goroutine,
  which is the item's literal proposal). That mutation is the one that most needs to be run: it is
  what a future reader will "simplify" the design back into.
- **`0 files` on the integration lane's second prepare** is not a discriminating observation: p4
  reports an up-to-date client on stderr, so `0 files` there is expected both before and after. Do
  not build an assertion on it (3.3).

---

## 6. The red-first sequence

**R0 - baseline, both ways.** At `c2a7eae`, run and record:
`go test ./internal/agent/source/perforce/... ./internal/agent/... ./cmd/relay-agent/... -count=1`, and
`go test -tags integration -p 1 ./internal/agent/source/perforce/... -count=1 -timeout 1800s`
(needs Docker and `p4` on PATH; it skips cleanly without them, and **a skip is not a green - say
which was obtained**). Nothing below may be diagnosed against an unmeasured baseline.

**R1 - the fixture seam, alone.** `fakeRunner.Stream` cannot block, so nothing can currently drive a
tick during a sync. Add `setStreamBlock(key string, release <-chan struct{})`, mirroring the existing
`setBlock` for `Run`: `Stream` selects on `release` and `ctx.Done()`, and honours `ctx.Done()` by
returning `ctx.Err()` so a cancelled test cannot hang. Land it with no production change and confirm
the package is still green. This is a test-only file and its only job is to make R4 writable.

**R2 - `-q` leaves the argv. Default lane.** Change `SyncStream` and re-key the seven fixture keys
in the three files from F12. Expected RED before the re-key:
`fakeRunner.Stream: no fixture for args "-c relay_h_... sync --parallel=4 ..."`. Confirm the count of
re-keyed sites equals seven and that no fourth file needed one.
**This commit must also carry R5**, or a tree exists in which per-file output reaches `progress` and
`-q` is gone: the item is right that split, the per-file volume of a 2 TB sync lands in `task_logs`.

**R3 - `syncLineDepotPath`. Default lane.** New `syncprogress_test.go`, the nine-row table from 4.1.
RED against the `" - "` split stub; record all failing messages, then implement. Add
`syncProgress.onLine`/`snapshot` in the same commit with a two-row test (a file line increments
`files` and sets `lastPath`; a non-file line increments `other` and leaves `lastPath` alone).

**R4 - the cadence test, observing the CONSUMER. Default lane.** New
`TestProvider_ARunningSyncEmitsOneSummaryPerTickWithNoP4Output`:

- `newSyncTicker` swapped for one returning a test-controlled channel; `syncNow` swapped for a
  stepping fake; both restored in `t.Cleanup`.
- `setStreamBlock` on the sync key, so p4 produces **zero** lines: this is the item's "one tick and
  zero `onLine` calls" case and it is what proves the heartbeat is independent of p4's output.
- `Config.SyncHeartbeatInterval` non-zero, `FreeDiskGB` a stub returning `(811, nil)`.
- Run `Prepare` on a goroutine. Assert, in this order: **with no tick delivered, no line containing
  `0 files` has appeared** (this is the half that kills a loop that emits before waiting); then send
  one tick, wait for one line, and assert it contains exactly `0 files`, `0 other lines`,
  `811 GB free` and `last -`; then close `release` and assert `Prepare` returns and the
  `[sync] complete:` line also carries a summary.
- The progress recorder must be mutex-guarded: it is read by the test goroutine while `Prepare` runs.

RED at HEAD: the symbols do not exist. RED against the naive `emit()`-at-top-of-loop implementation:
the first assertion. Record both.

**R5 - per-file output is counted, never forwarded. Default lane.** Rewrite
`TestProvider_PrepareBracketsTheSyncWithExactlyOneStartAndOneCompleteLine` (F3). Keep the
`1 [sync] starting` / `1 [sync] complete` / `1 "1 path"` assertions verbatim. Replace

```go
assert.Equal(t, 3, countLinesContaining(lines, "of 3 files"),
    "p4's own output must still reach progress unchanged, got: %v", lines)
```

with two assertions and a new comment that carries the ORIGINAL comment's non-degeneracy argument:

```go
assert.Equal(t, 0, countLinesContaining(lines, "of 3 files"),
    "p4's per-file output is counted, never forwarded: a 2 TB sync is millions of lines "+
        "and task_logs has no per-task cap, got: %v", lines)
assert.Equal(t, 1, countLinesContaining(lines, "3 files"),
    "and the count is what keeps the line above from being satisfied by a sync that "+
        "emitted nothing at all, got: %v", lines)
```

The second assertion is the replacement for the deleted one and it is strictly stronger: it fails
both if the lines leak and if the counter is wired to nothing. **Check the substring collision** -
`"3 files"` is a substring of `"of 3 files"`, so if the forwarding is NOT removed the second
assertion counts 4 and the test is red for the right reason. Verify that by running R5's test
against the tree with `-q` removed and forwarding still in place, once, deliberately.

RED at HEAD: the first assertion (HEAD forwards all three).

**R6 - the renderer's degenerate inputs. Default lane.** Table over `syncSummary` with a nil
`FreeDiskGB` (expect `- GB free`), an erroring `FreeDiskGB` (expect `- GB free`), no file line yet
(expect `last -`), and a zero elapsed (expect `0s`). RED against a renderer that dereferences nil,
which is what an implementation written against only the happy path does.

**R7 - the disabled timer. Default lane.** `SyncHeartbeatInterval: 0` plus a ticker seam that
`t.Fatal`s if it is ever constructed. Assert `Prepare` succeeds and the `[sync] complete:` line still
carries a full summary. This is the acceptance criterion "`0` disables the timer (the final line
still emits)" made executable. The seam-fatals-on-construction shape is what stops a "disabled"
implementation that builds a ticker and discards its ticks.

**R8 - `resolveSyncHeartbeatInterval`. Default lane, `cmd/relay-agent`.** Table:
`""` -> 30s; `"0s"` -> 0; `"45s"` -> 45s; `"2m"` -> 2m; `"0"` -> 30s **and a warning naming the var**;
`"-30s"` -> 30s and a warning; `"1s"` -> 30s and a floor warning; `"garbage"` -> 30s and a warning.
Capture the log output the way `TestParseDurationEnv_LogsWarningOnInvalidNonEmptyInput` already does.
RED at HEAD: the function does not exist. Note explicitly in the test's comment that the
**assignment** of the result into `perforce.Config` is NOT covered by this or any other test (F5).

**R9 - the mutation that pins the two-case select.** Add `case <-ctx.Done(): return ctx.Err()` to
`runSyncWithHeartbeat`'s select. Run
`TestProvider_PrepareDoesNotReturnUntilTheSyncGoroutineHasFinished` (which cancels ctx while the
stream is blocked and asserts the recorded `fakeRunner` call for the sync key is present the instant
`Prepare` returns; `fakeRunner.Stream` appends to `f.calls` as its last statement, so the call being
recorded proves the goroutine finished before `Prepare` did). It must go RED. **Restore from a copy,
never with `git checkout --`**, and re-run a control that should stay green. If it does not redden,
the guard is decorative and must be rewritten before the slice proceeds.

**R10 - the mutation that pins the single-caller property, and it is the most important one.**
Change the tick arm to `go progress(...)` - the item's literal proposal. Run
`TestProvider_TheHeartbeatNeverCallsProgressConcurrentlyWithPrepare`, whose recorder does
`if atomic.AddInt32(&inFlight, 1) != 1 { t.Error(...) }`, holds for a beat, then decrements, and
whose fixture arranges one tick to land while `Prepare` is emitting. It must go RED.
An atomic in-flight counter rather than reliance on `-race` alone, because `-race` on this repo runs
only in the Linux container and this property must redden in `make test`. Run it under the container
too; both instruments, one claim.

**R11 - the integration lane.** Update both comments from F9 and assert the new behaviour:

- First prepare: the fixture's single `readme.txt` produces exactly one `onLine` call, so the
  `[sync] complete:` line must contain `1 files`. That is the end-to-end proof that dropping `-q`
  reached real p4 and that the counter is wired to it. **Record the line verbatim in the commit
  message**; if p4 r25.2 emits a different number of stdout lines for one file, the assertion is
  wrong and the observation is what to trust.
- Second prepare: keep `require.Len(t, progress2, 2)` at 2, and add a comment saying it now depends
  on the summary riding on the `[sync] complete` bracket (D3) and on the 30s default not firing
  during a sub-second no-op sync. Do not loosen it to a range: it is the only end-to-end assertion
  that per-file output does not reach `progress`.
- Do NOT assert on `0 files` for the second prepare (3.3).

Run with Docker and `p4`. **If the lane cannot run, say so plainly**; do not substitute the default
lane for it, because the default lane's `fakeRunner` echoes whatever it is told and can prove nothing
about p4's real output.

**R12 - full verification.** `go test ./... -count=1`; the perforce integration lane; `go vet` under
both tag sets; `-race` in the `golang:1.26` Linux container across all packages - which this slice
needs more than most, since it introduces the package's first production goroutine handoff. If the
container lane is unavailable, **say `-race` did not run** rather than substituting `-count=N`.

### 6.1 An optional final step, labelled so it can be dropped without invalidating the rest

**R13 (conditional) - `execRunner.Stream` never checks `sc.Err()`.** `client.go:87-95` scans stdout
and then calls `cmd.Wait()` without ever reading `sc.Err()`. Today that branch is close to
unreachable: with `-q` there is almost no stdout. **This slice is what makes it reachable**, and its
failure mode is a silent undercount in the exact number the summary is built from: a
`bufio.ErrTooLong` (a line over the 1 MB `sc.Buffer` cap) or a pipe read error ends the loop, `Wait()`
then returns nil, and the task log reads `[sync] complete: ... 41231 files` for a sync whose output
was truncated. Three lines fix it:

```go
if err := cmd.Wait(); err != nil { return newP4CommandError(args, err, stderr.String()) }
if err := sc.Err(); err != nil { return newP4CommandError(args, err, stderr.String()) }
return nil
```

Testing it needs a real subprocess that emits an over-long line, which means the stdlib
`TestHelperProcess` re-exec pattern (`execRunner` already takes a `binary` field and
`client_error_test.go` already constructs one directly, so the seam exists).

**It is labelled conditional on purpose.** It edits the same six lines as
`bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero`, and it changes a
would-have-succeeded sync into a prepare failure. If the helper-process test proves awkward, **drop
the code change and file the item** rather than shipping the fix untested or shipping a comment in
place of a guard. Do not ship it silently either way. D13.

---

## 7. The prose sweep

Wrong prose about correct code is this repo's dominant defect class, and this change falsifies four
live passages plus one README table row.

### 7.1 Comments

| File | Passage at HEAD | Becomes |
|---|---|---|
| `client.go:164-165` (`SyncStream` doc) | "runs `p4 -c <client> sync -q --parallel=4 <specs...>` from cwd, streaming lines to onLine" | drop `-q` from the quoted argv, and state the hazard the removal creates: the lines are counted, never forwarded, because a large sync is millions of them and `task_logs` has no per-task cap. Name the guard (R5's test). 4.5 has the wording. |
| `perforce_integration_test.go:56-60` | "Production code runs `p4 sync -q` which suppresses per-file output entirely; with the fixture's single readme.txt baseline, the sync emits zero lines on success. We retain the callback to surface unexpected `[recover] ...` diagnostic lines" | **false in its first clause and its second.** The sync now emits one line for `readme.txt`, and the callback is now asserted on rather than retained for diagnostics. Rewrite to say what the assertion pins: one file line reaches `onLine`, none of it reaches `progress`, and the count surfaces in the completion bracket. |
| `perforce_integration_test.go:94-97` | "Asserting the progress callback saw nothing cannot express 'no re-sync', because `p4 sync -q` on an up-to-date single-file workspace prints nothing either way" | the `-q` reason is gone; **the conclusion survives for a different reason**, which is the interesting part: an up-to-date client is reported by p4 on **stderr**, which `execRunner.Stream` discards on a zero exit. Say that, and cross-reference `bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero` so the next reader of this comment finds the item rather than re-deriving it. |
| `perforce_progress_test.go:44-47` | "The 'of 3 files' assertion is what keeps 'no per-file line of the provider's own' from degenerating into 'the sync emits nothing': p4's own output must still reach progress unchanged." | the property inverts, the **argument does not**. Rewrite to say the file COUNT is now what carries the non-degeneracy, and that the substring relationship between `"3 files"` and `"of 3 files"` is deliberate. F3, R5. |
| `perforce.go`'s sync block | one comment on the brackets and one on release-before-report | both stay; add the two new ones from 4.2 (the single-caller argument and the two-case select), each naming its guard. Do not restate the argument in the code - name the hazard and cite the test, per the Comments rule. |

### 7.2 README

| Location | Change |
|---|---|
| Agent env table (around line 503, after `RELAY_EVICTION_TIMEOUT`) | new row: `RELAY_SYNC_HEARTBEAT_INTERVAL` - "How often a running `p4 sync` writes a progress summary to the task log (`4m30s; 12483 files; 0 other lines; 811 GB free; last //depot/...`). Default `30s`. **`0s` disables the timer**; the completion line still carries a summary. A bare `0`, a negative value, or an interval under `5s` is refused with a warning and the default is used. The file count is a lower bound: p4 block-buffers its output to a pipe, so it moves in bursts while the elapsed field moves continuously." |
| "Source workspaces" section, near the eviction paragraph (line ~591) | one sentence naming the heartbeat as the way to tell a live multi-hour sync from a wedged one, and stating that the free-space figure is the same volume `RELAY_WORKSPACE_MIN_FREE_GB` is compared against. |

The block-buffering caveat is in the README row on purpose (F8): it is the one thing about this
feature that will generate a false bug report, and the row is where an operator meets it.

### 7.3 Not changed, on purpose

`docs/superpowers/specs/2026-04-24-perforce-workspace-management-design.md`,
`2026-05-01-p4client-explicit-flag-design.md`, `2026-09-04-perforce-client-path-addressing.md` and the
plans beside them all quote `sync -q --parallel=4`. They are records of a moment and stay as written.
The temptation to "fix" them destroys the record of what the tree looked like when those decisions
were made.

---

## 8. Acceptance criteria

### 8.1 The item's criteria, assessed

1. *"A running sync produces one summary line per interval in the task log, whether or not p4 has
   written anything, and a final line that says whether it completed or failed."*
   **TRUE with two corrections.** (a) "a final line" is the EXISTING `[sync] complete` /
   `[sync] failed` bracket, extended - not a new line (F4, D3); the item's `COMPLETE:`/`FAILED:`
   vocabulary is refused. (b) "in the task log" is conditional on the coordinator draining `sendCh`.
   When it is not, `progress` parks and **no** line arrives - that is
   `bug-2026-09-03-provider-progress-parks-while-holding-the-workspace` and this slice does not close
   it. Restate as: *"...produces one summary line per interval through the `progress` callback,
   whether or not p4 has written anything, and the completion bracket carries the same summary and
   says whether the sync completed or failed."*
2. *"Per-file sync output never reaches `task_logs`."*
   **TRUE and achievable, and it REVERSES a property a live test asserts** with a written rationale
   (F3). The criterion is fine; the item is silent about the cost of meeting it, and the rationale
   must be carried across rather than deleted. R5.
3. *"Free space is read through the agent's existing platform helper."*
   **TRUE**, and achievable with strictly less work than the item budgets: no adaptation, no new
   field shape, the same identifier already passed to `Sweeper.FreeDiskGB`. The item's own preferred
   spelling (`Config.FreeDiskBytes`) would have **violated** its own next sentence (F2).
4. *"The interval is env-configurable and documented."*
   **TRUE**, with the `0` -> `0s` correction (F6) and with the honest note that only the PARSING is
   covered by a test; the assignment into `perforce.Config` is unguarded like every other knob in
   this binary (F5).

### 8.2 Prescribed remedies naming something that does not exist

Three, all in the item's Proposal.

- **"add it to the wiring guard ([[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]])"** -
  the linked file exists but is an OPEN IDEA, and every guard it describes is in `cmd/relay-server`.
  **There is no wiring guard in `cmd/relay-agent` to add a row to.** F5.
- **"`Config.FreeDiskBytes`"** - does not exist, and cannot be produced from the existing helper
  without editing both platform files, which the item's next sentence forbids. F2.
- **"`stop(err)`"** - not a missing symbol so much as a design that collides with two live tests and
  one recorded rule. F4.

Two near-misses that check out and should not be re-verified by a reviewer: the two backlog slugs
(`bug-2026-08-14-task-logs-have-no-per-task-volume-cap`,
`idea-2026-09-01-go-ci-never-compiles-or-runs-windows-code`) are both exact and both open (F10).
Three of the four "Related" cross-links are closed (F11).

### 8.3 This spec's criteria

- With p4 producing **zero** output and a fake ticker, one tick produces exactly one summary line
  containing `0 files`, and **zero ticks produce zero summary lines**.
- No line p4 writes to stdout appears in any `progress` line, and the file **count** appears in the
  completion bracket, proven by the same test.
- `progress` is never called from more than one goroutine, proven by an in-flight counter that
  reddens in `make test` and by `-race` in the Linux container.
- `Prepare` never returns while the sync goroutine is live, proven by a mutation that adds a
  `ctx.Done()` select arm and reddens.
- `SyncHeartbeatInterval <= 0` builds no ticker at all and still emits a full summary on the
  completion bracket.
- `syncLineDepotPath` returns `""` for every non-file line, handles a filename containing `" - "`,
  strips control bytes, and clips at 200 characters.
- `RELAY_SYNC_HEARTBEAT_INTERVAL` resolves per the R8 table, and every rejected input logs a warning
  naming the variable.
- The integration lane's first prepare reports `1 files` in its completion bracket, against real p4d.
- The integration lane's second prepare still emits exactly two progress lines.
- Every passage in 7.1 and 7.2 is rewritten; no count is incremented and no enumeration left partial.
- `make test`, the perforce integration lane, `go vet` under both tag sets, and `-race` in the
  `golang:1.26` container are all green.

---

## 9. Decisions

Made autonomously. Each is stated so it is cheap to overturn.

**D1 - INVERT THE GOROUTINE: the sync runs on a goroutine, the heartbeat loop runs on `Prepare`'s
own.** This is the spec's central decision and it overturns the item's architecture. The item asks
for a goroutine that calls `progress` on a timer; `progress` holds a mutex across a send bounded only
by the agent context, so a parked heartbeat goroutine would block `Prepare`'s completion line, the
runner's `flushProgress`, and any join - **with the workspace handle still held**, which is a
strictly worse instance of the bug filed one directory over (F1).
The alternatives were weighed. (a) *Heartbeat goroutine plus a 1-slot drop-on-full mailbox and a
dedicated emitter goroutine that `stop` does not join*: bounds the queue, does not bound `mu`, still
lets a parked emitter block `Prepare`, and leaks one goroutine per prepare. (b) *Drain a staged
summary from `onLine`*: dead on arrival, since a stalled sync produces no lines and that is the whole
premise. (c) *Land the `sendOrAbort` fix first*: worth doing and it is not sufficient - `sendOrAbort`
is woken by the task's cancel, so on a healthy uncancelled task against a wedged coordinator it still
parks. Inversion is the only shape that keeps `progress` single-caller, and single-caller is what
makes every other question about blocking moot.

**D2 - "bytes written" is DROPPED ENTIRELY, not kept as a labelled volume delta.** The item argues
drop-or-label; the reasoning holds and is stronger against the current tree than the item states.
`p.cfg.Root` holds every workspace on the host (`wsRoot = Root/<shortID>`), and `freeDiskGB` stats
the **volume**, so a second concurrent sync, a task's build output, and a sweeper eviction all move
the number during one sync. On top of that the helper truncates to whole GB, so a 999 MB sync reads
as `0 GB written` and a delta straddling a boundary reads as 1 GB whatever it actually moved. A
figure that is wrong in both directions and quantized at 1 GB is not improvable by a label. **Report
free space as an absolute instead**, which is monotonically meaningful, actionable ("am I about to
run out"), and directly comparable to `RELAY_WORKSPACE_MIN_FREE_GB`.

**D3 - the final summary rides on the EXISTING bracket; no `COMPLETE:` / `FAILED:` vocabulary, and
the failure line still carries no cause.** F4. Two vocabularies for one event is the "cause has one
home" problem, and "FAILED and the error" is refused by a test whose comment says in terms that it
exists to refuse exactly this. Side benefit: the integration test's `require.Len(progress2, 2)`
survives unchanged, which keeps the slice's only end-to-end no-leak assertion.

**D4 - per-file lines are COUNTED ONLY; none reaches `task_logs`; and no phrase allow-list is added
to promote "interesting" ones.** Checked against what an operator debugging a stalled sync actually
needs, which is the task brief's question. They need three things: is it moving (the file-count delta
between two consecutive heartbeats, plus elapsed), will it fit (free space), and where is it (the last
depot path). All three are in the summary. What a per-file firehose would add is the identity of every
file, which is millions of durable rows against a table with no per-task cap.
The tempting middle path - promote lines matching a small allow-list of p4 diagnostics - is refused:
it is a phrase match on p4 output, which is the shape
`bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr` is about, and an allow-list is
silently wrong on the next p4 version. **What replaces it is the `other lines` counter**: an operator
learns that p4 said something that was not a file line, and how much of it, without any of the text
being reproduced. That discloses the loss where the aggregate is READ, which is the property this
repo's own record says a lossy aggregate needs.

**D5 - the summary is five fixed fields in a fixed order with explicit `-` placeholders, and the
input-derived path goes last.** 4.4. Fixed fields because an omitted field is indistinguishable from
a truncated line or an unwired feature; the order because it is the operator's question order; the
path last because it is the only clipped, sanitized, attacker-influenced component (3.2).
`time.Duration.Round(time.Second)` for elapsed, so there is no formatter to write or test.

**D6 - `Config.FreeDiskGB func(root string) (int64, error)`, nil-safe, wired from the same identifier
`Sweeper.FreeDiskGB` already receives.** F2. Not `FreeDiskBytes`. Nil renders `- GB free` rather than
panicking, because every existing in-package test constructs a `Config` without it and a nil
dereference there would be a test-only defect that looks like a production one. It stats
`p.cfg.Root`, not `wsRoot`, so the number in the log is the same number the sweeper and
`RELAY_WORKSPACE_MIN_FREE_GB` act on.

**D7 - the env knob is parsed by `parseDurationEnv`, wrapped in `resolveSyncHeartbeatInterval`, with
`0s` as the disable spelling and a 5s floor.** F6 is the finding; this is the whole remedy
for it. Three env-duration conventions exist in the agent already; `parseDurationEnv` is chosen because it
is the one the two neighbouring workspace knobs use, it lives in the same file as the wiring, it
already warns naming the variable, and it already has three tests. Consequences, all of them recorded
rather than papered over: **`0s` disables**; a bare **`0` does not** and instead warns and falls back
to 30s; a **negative** value is unrepresentable in the regex and takes the same path with no new
guard; a positive value **below 5s** is refused with its own warning and falls back, because the only
cost of this knob is durable `task_logs` rows and nothing caps them yet.
The alternative - special-casing `"0"` inside `parseDurationEnv` - is declined because that function
is shared by two other knobs whose warning behaviour is pinned by two tests, and it would make `0`
mean "disabled" in one caller and "warn" in another.

**D8 - no env-to-field wiring guard is built for `cmd/relay-agent`.** F5. Building one is
`idea-2026-08-14-generalize-the-env-to-field-wiring-guard`'s whole scope, it carries an unresolved
constructor-versus-guard question, and its own Acceptance list requires proving each row by deleting
its wiring and watching that row alone go red - a slice, not a bullet. What ships instead is
`resolveSyncHeartbeatInterval` as a separately testable function, so the untested surface is one
assignment line rather than a whole knob, and a note in the test saying so. Section 11 proposes the
item amendment.

**D9 - the failure path renders its summary AFTER `handle.Release()`.** The release-before-report
ordering is preserved verbatim and the summary is rendered inside the released window, not before it,
because rendering reads free disk and a `statfs` on a wedged network volume is an uninterruptible
block. Putting the render before the release would re-open the exact window
`TestProvider_ASyncFailureReleasesTheWorkspaceBeforeItReportsAnything` closes, with a new blocking
primitive the test does not model.

**D10 - the free-disk read is best-effort and STICKY-DISABLED after its first error within one
sync.** One `statfs` per heartbeat line is cheap, but it is a **new position** for that syscall: the
sweeper's copy runs on a background goroutine holding nothing, while this one runs on a goroutine
holding a workspace. It cannot be given a timeout (Go cannot cancel a blocking syscall), so the
exposure is recorded rather than defended, and section 11 proposes an item. What IS done cheaply:
after the first error, the field renders `-` for the rest of the sync without retrying, so a broken
mount produces one failed call rather than 120 an hour.

**D11 - two package-level test seams (`syncNow`, `newSyncTicker`), not `Config` fields.** They match
`lookPath` and `prepareAcquireHook`, already in this package, and the package has no `t.Parallel()`
anywhere, so cross-test interference is not reachable. `Config` stays production-shaped. Tests
restore both in `t.Cleanup`.

**D12 - `bug-2026-09-04-p4-sync-reports-not-in-client-view-and-exits-zero` stays filed, unchanged,
and is NOT partially closed here.** 3.3. Dropping `-q` touches stdout; that bug is on stderr. The new
`0 files` reading is a weak observable, not a guard, and it is ambiguous with three legitimate cases.
No "0 files means failure" rule is added, because that item says in terms that the
deliberately-empty case must be decided in writing first, and deciding it inside a feature slice is
exactly the scope creep the brief forbids.

**D13 - the `sc.Err()` fix is a labelled conditional final step (R13), shipped with a real
subprocess test or filed, never shipped untested.** 6.1. It is this slice's own reachability that
promotes the branch from theoretical to live, which is why it is in scope at all; it edits the same
six lines as an open bug and converts a success into a failure, which is why it is separable.

**D14 - the `-q` removal and the counting change ship in ONE commit (R2 and R5 together).** Adopted
from the item and it is right: a tree in which `-q` is gone and `progress` is still the `onLine`
callback puts the per-file volume of a 2 TB sync into `task_logs`. There is no reviewable
intermediate state, so there is no commit boundary there.

---

## 10. Lane structure and sequencing

**One lane.** The `-q` removal, the counter, the heartbeat loop and the summary renderer are mutually
dependent: the counter is pointless without the output, the output is dangerous without the counter
(D14), and the renderer is what makes either observable. A second worktree would produce two branches
each red until merged.

Commit order follows section 6. Two orderings inside it are load-bearing:

- **R1 (the blocking stream fixture) lands before R4**, because without it no cadence test is
  writable at all and the temptation is to write the test the item warns against - one that calls
  `emit()` directly and proves nothing about the timer.
- **R9 and R10 (the two mutations) run before R11**, so the guards are proven against a real
  un-guarded tree rather than being assumed. A mutation must leave its test behind and must be
  restored from a copy, never with `git checkout --`.

`gofmt` is useless as a signal on this repo (CRLF working copy), so after any programmatic edit to a
tracked file check the diffstat against the intended change size, run `git ls-files --eol` on the
touched paths and require `i/lf`, and confirm each file still decodes as UTF-8. The README row in
7.2 contains no non-ASCII byte and must not acquire one.

---

## 11. Residual risks and backlog candidates

Proposed, not filed. The conductor should route these to the human for acceptance.

1. **`progress` still parks while holding the workspace.** Not new, not made worse by this design
   (D1), and now exercised 120 times an hour instead of twice per prepare. The heartbeat does not add
   a new instance, but it does move the *expected number of blocking opportunities* per sync from 2
   to `duration/interval`. That raises the odds of hitting the existing bug without changing its
   shape. **No new item; it strengthens the case for
   `bug-2026-09-03-provider-progress-parks-while-holding-the-workspace` and that argument belongs in
   that item as an amendment.**
2. **A `statfs` on a workspace-holding goroutine.** D10 records the exposure and the sticky disable.
   Candidate item: move the free-disk sample onto the sweeper's existing goroutine and have the
   heartbeat read a cached value, so no syscall runs on a path that holds a workspace. Low priority,
   and it needs a decision about staleness that this slice does not need to make.
3. **`cmd/relay-agent` has no wiring guard of any kind.** F5, D8. Candidate: amend
   `idea-2026-08-14-generalize-the-env-to-field-wiring-guard` to record `cmd/relay-agent` as a second
   entirely-unguarded package, with its unguarded assignments enumerated (`Root`, `Hostname`,
   `SyncHeartbeatInterval`, `FreeDiskGB` on the provider config; `MaxAge`, `MinFreeGB`,
   `SweepInterval`, `Client`, `ListLocked`, `FreeDiskGB`, `Claim`, `OnEvictedCB` on the sweeper), and
   to note that the agent's provider construction is inline in `main()` so the executed-test route the
   item prefers needs a `buildWorkspaceProvider` extraction first. **An amendment to an existing item,
   not a new one.**
4. **`task_logs` volume.** 3.1 gives `bug-2026-08-14-task-logs-have-no-per-task-volume-cap` its
   figure: 120 rows and about 15 KB per hour per syncing task at the default. **An amendment, not a
   new item.**
5. **`execRunner.Stream`'s unchecked `sc.Err()`** if R13 is dropped. D13. New item if and only if the
   step is dropped.

---

## 12. Routing for the Phase 4 lenses

- **Invariants lens.** Section 3 and 4.2. The two questions that matter: does any path let `Prepare`
  return while the sync goroutine lives (R9's mutation), and does anything other than `Prepare`'s own
  goroutine call `progress` (R10's mutation). Also check that the failure path's release still
  precedes every new statement, including the free-disk read (D9).
- **Correctness lens.** `syncLineDepotPath`'s table against real p4 output shapes, especially the
  `" - "`-in-a-filename row and the no-`#` row. The `"3 files"` / `"of 3 files"` substring
  relationship in R5, which is deliberate and easy to break by renaming a fixture. And whether R13
  ships or is filed.
- **Security lens.** 3.2. The depot path is p4-derived text on its way to `task_logs` and then to the
  SPA: confirm the control-byte strip and the 200-character clip, and confirm the path is last in the
  line so a clip truncates nothing structural. Ask specifically whether a file named so that the
  rendered line reads as another bracket can mislead an operator, and whether the closed set of four
  numeric fields plus one clipped tail is actually closed.
- **Integration tester.** The p4d lane, R11. The `1 files` observation on the first prepare is the
  only evidence in the slice that dropping `-q` reached real p4; record what p4 r25.2 actually emits
  rather than asserting the predicted number. Confirm the second prepare still emits exactly two
  lines, and confirm `-race` in the Linux container across the whole tree, since this slice adds the
  package's first production goroutine handoff.

## 13. Open question for the human

None that blocks. Two worth a look on review:

- **D1**, because it overturns the backlog item's architecture rather than refining it. A reviewer who
  thinks the item's heartbeat goroutine is fine should say so before the plan is written, and should
  say what bounds `progress`'s mutex hold.
- **D7's 5s floor.** It refuses an operator input rather than clamping it. The argument is that the
  cost is durable rows nothing caps; the counter-argument is that an operator debugging a wedged sync
  on a test farm may legitimately want 1s. If the floor is dropped, the README row must say what the
  row rate becomes.
