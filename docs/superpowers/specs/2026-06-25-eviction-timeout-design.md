# Per-eviction timeout for EvictWorkspace

Date: 2026-06-25
Status: design (autonomous Phase 1)
Source: docs/backlog/idea-2026-04-25-eviction-timeout.md (open question)

## Problem

Workspace eviction (`Sweeper.evict` in `internal/agent/source/perforce/sweeper.go`)
runs two potentially unbounded operations:

1. `s.Client.DeleteClient(ctx, w.ClientName)` -> `p4 client -d <name>` via
   `exec.CommandContext` (honors the context deadline).
2. `os.RemoveAll(filepath.Join(s.Root, w.ShortID))` -> recursive on-disk delete
   (does NOT honor any context; Go's `os.RemoveAll` is not cancelable).

The open question: should each eviction be bounded by a per-eviction timeout, or
is an unbounded block harmless because the goroutine is already tied to `runCtx`?

## Current behavior (grounded)

There are two distinct eviction entry points with very different blast radii.

### On-demand eviction (small blast radius)

`internal/agent/agent.go` lines 283-296 handle
`CoordinatorMessage_EvictWorkspace`. It spawns a fresh goroutine per request,
bound to `a.runCtx`, and logs-and-swallows the error:

```go
go func() {
    if err := ev.EvictWorkspace(runCtx, shortID); err != nil {
        log.Printf("agent: evict workspace %s: %v", shortID, err)
    }
}()
```

The recv loop is NOT blocked (the eviction runs off the loop in its own
goroutine). A wedged eviction here leaks exactly one goroutine plus its `p4`
subprocess until `runCtx` is cancelled at agent shutdown. Not great, but bounded
to one leaked goroutine per stuck eviction.

### Background sweeper eviction (large blast radius)

`internal/agent/source/perforce/sweeper.go`:
- `Sweeper.Run` (line 46) is a ticker loop; on each tick it calls `SweepOnce`.
- `SweepOnce` (line 67) iterates candidates and calls `s.evict(ctx, reg, w)`
  **synchronously** in both the age pass (line 97) and the pressure pass
  (line 123). Evictions are serialized, not concurrent.

If a single `s.evict` wedges (a hung `p4 client -d`, or an `os.RemoveAll` stuck
on a locked file handle on Windows), the consequences cascade:

- The remaining candidates in that pass are never evicted.
- `SweepOnce` never returns, so the `Run` ticker can never fire the next tick
  (the `select` is parked in the `case <-t.C` branch's body).
- The disk-pressure relief mechanism is dead for the rest of the agent's
  lifetime. The box keeps filling while no workspace is reclaimed.

This is the material risk. The sweeper is precisely the mechanism that frees disk
under pressure, and a single stuck `p4 client -d` silently disables it.

### Why runCtx is not enough

Both paths derive their context from `runCtx` (on-demand directly; the sweeper
via the `ctx` passed to `Run`/`SweepOnce`, which `cmd/relay-agent/main.go` wires
from the agent's root `ctx`). `runCtx` is cancelled only at agent shutdown.
During normal operation it never fires, so it provides zero protection against a
single wedged eviction while the agent keeps running. `runCtx` bounds the hang to
"until the process exits", which for a long-lived agent is effectively unbounded.

### Best-effort and dirty-delete already in place

Eviction is best-effort everywhere: errors are logged and swallowed
(agent.go:292; sweeper.go:101 and :127), and `ErrEvictClaimLost` is treated as a
benign skip. The dirty-delete mechanism (sweeper.go:151-160) already handles a
partial eviction: if `os.RemoveAll` fails after the client was deleted, the entry
is marked `DirtyDelete` so a later sweep retries the directory delete without
re-running `p4 client -d`. A timeout-induced failure slots cleanly into this
model.

## Decision

**A per-eviction timeout IS warranted.** Evidence: the background sweeper
serializes evictions and parks the ticker inside `SweepOnce`, so one wedged
`p4 client -d` permanently disables disk reclamation for the whole agent
lifetime. Bounding the `p4` subprocess turns an indefinite stall into a logged,
retryable best-effort failure that the existing dirty-delete path already
absorbs.

## Design

### Where the timeout goes

Wrap each `s.evict` body in a per-eviction child context derived from the passed
`ctx`:

```go
func (s *Sweeper) evict(ctx context.Context, reg *Registry, w WorkspaceEntry) error {
    // ... Claim reservation unchanged ...
    ctx, cancel := context.WithTimeout(ctx, evictTimeout)
    defer cancel()
    // ... DeleteClient(ctx, ...) and removeAll below ...
}
```

Placing the timeout inside `evict` (rather than at each call site) covers both
the sweeper passes and the on-demand path, because on-demand eviction
(`Provider.EvictWorkspace`) builds a `Sweeper` and calls `sw.evict(ctx, ...)`
(perforce.go:352-358). One change protects both blast radii.

### The p4 client -d call

`s.Client.DeleteClient(ctx, ...)` already flows through `exec.CommandContext`
(client.go:28-29 / 103-106). Passing the timeout-bounded `ctx` is sufficient:
when the deadline fires, the context kills the `p4` subprocess and `Run` returns
an error, which `evict` returns and the caller logs-and-skips. No code change in
`client.go` is needed; only the bounded `ctx` must reach it.

### The os.RemoveAll call (the hard part)

`os.RemoveAll` does not observe context. Two options were considered:

- **Option A (recommended): bound only the p4 call; leave os.RemoveAll
  unbounded.** Rationale: a hung `p4 client -d` (network/server stall) is the
  realistic, observed failure mode for a process talking to a remote Perforce
  server. `os.RemoveAll` on a local workspace directory is local I/O; a true
  permanent hang requires a pathological FS condition. Wrapping it in a
  goroutine that we abandon on timeout creates a leaked goroutine plus a
  background process still mutating the directory we just marked for dirty-delete
  retry - a worse state than a rare local stall. Keep it simple.

- **Option B (rejected): run os.RemoveAll in a goroutine and select on
  ctx.Done().** This bounds the *wait* but not the *work*: the abandoned
  goroutine keeps deleting files concurrently with a subsequent dirty-delete
  retry, risking two deleters racing on the same tree. It adds a leaked
  goroutine per timeout and does not actually stop the FS operation. The
  complexity is not justified by the rarity of a hung local delete, and it
  conflicts with CLAUDE.md's "no error handling for impossible scenarios" and
  simplicity-first guidance.

**Chosen: Option A.** The timeout bounds the `p4 client -d` subprocess. The
`os.RemoveAll` step remains a plain synchronous call. This matches where the real
unbounded-hang risk lives (the remote-server subprocess) and avoids inventing
cancellation semantics Go does not provide for local FS deletes.

If, after Option A ships, an `os.RemoveAll` hang is ever actually observed in the
field, that is a new, separately-scoped backlog item with concrete evidence -
not speculative scope here.

### Default timeout value: fixed constant, 2 minutes

```go
// evictTimeout bounds a single workspace eviction's p4 client -d subprocess so a
// wedged Perforce call cannot stall the serialized sweep loop (and thus disk
// reclamation) for the agent's lifetime. os.RemoveAll is local I/O and is not
// bounded by this deadline (os.RemoveAll does not honor context).
const evictTimeout = 2 * time.Minute
```

Justification:
- A healthy `p4 client -d` is sub-second; it deletes a client spec, not file
  content. 2 minutes is generous headroom for a momentarily slow-but-alive
  Perforce server while still being short relative to the 15-minute default
  sweep interval, so a stuck eviction cannot consume a whole sweep cycle.
- **Fixed, not configurable.** CLAUDE.md discourages unrequested configurability.
  No operator has asked to tune this; the existing `RELAY_WORKSPACE_*` env vars
  tune policy (max age, min free, interval), not subprocess safety bounds. A
  safety ceiling that exists to prevent a permanent wedge does not need to be
  operator-visible. If a real need to tune it emerges, promoting the constant to
  an env var is a trivial follow-up.

### Invariants and best-effort contract

- **Eviction stays best-effort.** A timeout-induced failure returns a normal
  error from `evict`; existing callers already log-and-skip
  (sweeper.go:101/:127; agent.go:292). The sweep loop must NOT crash or abort the
  pass on a timeout - it continues to the next candidate, exactly as it does for
  any other `evict` error today. No new panic, no `log.Fatal`.
- **Dirty-delete interaction.** If the `p4 client -d` times out, `evict` returns
  before `os.RemoveAll`, so no dirty-delete marker is set and the next sweep
  retries the full eviction (client delete + dir delete) cleanly. This is the
  desired retry behavior. (Only an `os.RemoveAll` failure sets DirtyDelete, and
  that path is unchanged.)
- **No Invariant from CLAUDE.md is touched.** Eviction is agent-side workspace
  management; it does not write `tasks.status`/`task_logs` (no epoch fence
  concern), does not touch gRPC stream senders, and the `p.evicting` reservation
  / `p.mu` -> `ws.mu` lock discipline (the "no interior pointers across locks"
  and identity-checked teardown analogues) is unchanged - the timeout wraps only
  the I/O between reservation and release, never the locking.

## Acceptance criteria

1. A single eviction whose `p4 client -d` never returns causes `evict` (and
   therefore `SweepOnce` / on-demand `EvictWorkspace`) to return an error within
   `evictTimeout`, rather than blocking indefinitely.
2. When one eviction times out, `SweepOnce` continues to the remaining
   candidates in the same pass (the stuck eviction does not abort the pass).
3. The error is logged via the existing log-and-skip paths; no panic, no process
   exit.
4. A timed-out `p4 client -d` does NOT set a DirtyDelete marker (the dir was
   never touched), so a later sweep retries the full eviction.
5. The happy path (fast `p4 client -d`) is unaffected: existing sweeper tests
   continue to pass with no behavioral change.

## Test strategy

Use the existing `fakeRunner` fixture (`fixtures_test.go`) as the test seam.
`Sweeper.Client` is a `*Client{r: fr}` in tests (sweeper_test.go:32), so the fake
`Runner.Run` is where a hang is injected.

1. **Slow/hanging p4 client -d.** Extend `fakeRunner` (test-only) with an
   optional per-key block hook: when set for the `client -d <name>` key, `Run`
   blocks on `<-ctx.Done()` and returns `ctx.Err()` instead of returning a
   canned output. This faithfully models `exec.CommandContext` killing a wedged
   subprocess on deadline. Assert that `s.evict` (or `SweepOnce`) returns within
   a small multiple of a *test-shortened* timeout.
   - To keep the test fast and deterministic without a real 2-minute wait,
     expose `evictTimeout` as a package var overridable in tests (mirroring the
     existing untagged-override pattern: `initialReconnectBackoff`,
     `reconnectSleep`, `dialContextFn` in agent.go), OR have the test pass an
     already-deadline-bounded parent `ctx` and assert the deadline is honored.
     Prefer the parent-ctx approach if it suffices, to avoid adding a new
     override var; fall back to the override var only if the fixed internal
     timeout would otherwise dominate. Implementer's choice, justified in the
     plan.
2. **Pass continues after a timeout.** Register two stale candidates; make the
   first one's `client -d` hang and the second's succeed. Assert the second is
   still evicted (its directory removed, registry entry gone) and `SweepOnce`
   returns the second short ID.
3. **No DirtyDelete on p4 timeout.** After a timed-out first eviction, assert the
   registry entry for that short ID is NOT marked DirtyDelete (eviction will be
   fully retried next sweep).
4. **Happy-path regression.** Existing `TestSweeper_AgeEviction` /
   `TestSweeper_PressureEviction` pass unchanged.

These are pure unit tests (no Docker, no real p4): `make test` exercises them.

## Out of scope

- Bounding or cancelling `os.RemoveAll` (Option B). Revisit only with field
  evidence of a hung local delete.
- Making the timeout operator-configurable via env var.
- Concurrent (parallel) eviction in the sweeper. The serialized loop is fine once
  each step is bounded.
- Any change to the on-demand eviction's one-goroutine-per-request structure in
  agent.go; it inherits the fix for free via `Sweeper.evict`.
