---
date: 2026-06-25
topic: eviction-timeout
branch: claude/happy-mendel-18687f
pr: autopilot (this branch)
---

# Session Retro: 2026-06-25 - per-eviction timeout for the Perforce sweeper

**TL;DR:** Resolved the open-question idea "Per-eviction timeout for EvictWorkspace?".
`Sweeper.evict` (internal/agent/source/perforce/sweeper.go) now wraps the `p4 client -d`
(`DeleteClient`) subprocess call in a `context.WithTimeout`, so a wedged Perforce call can no
longer permanently stall the serialized sweep loop - which would otherwise kill disk
reclamation for the agent's entire lifetime. The bound is operator-tunable via
`RELAY_EVICTION_TIMEOUT` (Go duration, mirroring `RELAY_WORKER_GRACE_WINDOW`), default 30m.
Only the remote subprocess is bounded; `os.RemoveAll` stays unbounded by design. Best-effort
semantics unchanged, dirty-delete retry path preserved. Tests green on Windows + Docker;
clean review.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-06-25-eviction-timeout-design.md` (revised to
  env-configurable after maintainer input - see lesson below).
- **Plan** `docs/plans/2026-06-25-eviction-timeout-plan.md`.
- **Fix** `internal/agent/source/perforce/sweeper.go` - `defaultEvictTimeout` const (30m),
  `resolveEvictTimeout()` reading `RELAY_EVICTION_TIMEOUT`, package var `evictTimeout` as the
  test-shortening seam, and a `context.WithTimeout(ctx, evictTimeout)` wrapping `DeleteClient`
  inside `evict`.
- **Tests** `sweeper_test.go` + `fixtures_test.go` - hanging-`p4 client -d` block hook,
  pass-continues-after-timeout, no-DirtyDelete-on-p4-timeout, env-parse, happy-path regression.
- **Docs** `README.md` - `RELAY_EVICTION_TIMEOUT` documented.
- **Backlog** closed `docs/backlog/closed/idea-2026-04-25-eviction-timeout.md`.

## Lesson: surface the configurability question at spec time for ops/safety timeouts

The spec's first draft chose a fixed 2-minute `const`, citing CLAUDE.md's anti-configurability
and simplicity-first guidance. The maintainer interrupted and corrected it: production Perforce
workspaces can be 1 TB+, fleets differ, and an operator must be able to tune a disk-reclamation
safety bound per deployment. The conductor then asked two clarifying questions (scope: bound
only `p4 client -d` vs the whole eviction; default: env var with a generous default), got
answers, revised the spec to env-configurable 30m, and re-planned.

The takeaway, for next time:

- **CLAUDE.md's anti-configurability default is a default for in-process behavioral knobs, not
  a blanket rule for operational/safety bounds in an ops-deployed system.** Timeouts, grace
  windows, sweep intervals, disk thresholds - anything an operator must adapt to their fleet's
  hardware and SLAs - are exactly the knobs real fleets need. The existing
  `RELAY_WORKER_GRACE_WINDOW` / `RELAY_WORKSPACE_*` env vars were the precedent the first draft
  should have matched.
- **Defaulting a configurability question "closed" silently is the failure.** The right move
  is to surface it as an explicit spec-time question ("fixed const vs operator env var, and
  what default?") rather than picking the closed option and citing a guideline. The maintainer
  catching it cost a spec revision and a re-plan; asking would have cost one question.
- Heuristic to apply going forward: **if a number bounds an operation whose right value depends
  on the deployment's hardware, network, or workload size, it is operator config by default,
  not a const.** Only bound it as a const when the value is genuinely environment-independent
  (e.g. a 5s local pipe-drain `WaitDelay`).

## Technical decision: bound the remote subprocess, not the local FS delete

`evict` does two things: `DeleteClient` (-> `p4 client -d` via `exec.CommandContext`, honors
context) and `os.RemoveAll` (local recursive delete, does NOT honor context). Only the first is
bounded.

- The realistic, observed wedge is a hung `p4 client -d` talking to a remote/network-stalled
  Perforce server - `exec.CommandContext` kills it cleanly on deadline.
- `os.RemoveAll` is uncancelable local I/O; wrapping it in an abandon-on-timeout goroutine
  would leak a goroutine still mutating the tree we just marked for dirty-delete retry - two
  deleters racing, a worse state than the rare local stall. Rejected (Option B in the spec).
- The ordering makes this clean: a `p4` timeout returns *before* `os.RemoveAll`, so no
  DirtyDelete marker is set and the next sweep retries the full eviction. The deadline converts
  an indefinite stall into a logged, retryable, best-effort failure that the existing
  dirty-delete machinery already absorbs.
- Note the size interaction: because only the (sub-second, size-independent) spec deletion is
  bounded, a 1 TB+ workspace `rm -rf` is never interrupted by `RELAY_EVICTION_TIMEOUT`. The
  generous 30m default therefore exists to avoid killing a momentarily-slow-but-alive server,
  not to bound the big delete.

## What Went Well

- **Right blast-radius analysis drove the decision.** The spec grounded the "is a timeout
  warranted?" open question in the difference between the two eviction entry points: on-demand
  (one leaked goroutine, small blast radius) vs the serialized background sweeper (parks the
  ticker inside `SweepOnce`, disables disk reclamation for the agent lifetime, large blast
  radius). Placing the timeout inside `evict` covers both with one change.
- **Reused established conventions.** The `RELAY_WORKER_GRACE_WINDOW` env-parse pattern
  (`time.ParseDuration`, ignore-on-error) and the untagged package-var override seam
  (`initialReconnectBackoff`, `reconnectSleep`) gave the test a clean, fast way to shorten the
  timeout to milliseconds without a real wait.
- **Pure unit tests, no Docker required** for the new behavior - the `fakeRunner` block hook
  faithfully models `exec.CommandContext` killing a wedged subprocess.

## Backlog Triage

Assessed the spec's two "Out of scope" items and audited other agent timeouts. **No new
backlog items filed - nothing concrete found.** Detail:

- **Bounding `os.RemoveAll`** - correctly remains a documented non-goal. There is no field
  evidence of a hung local delete, and the rejected Option B is strictly worse (leaked
  goroutine racing the dirty-delete retry). Filing now would be speculative scope. The spec
  already states the re-open trigger: concrete evidence of a hung local delete.
- **Concurrent/parallel sweeper eviction** - documented non-goal. Now that each step is
  bounded, the serialized loop cannot wedge for the agent lifetime; parallelism is a
  throughput optimization with no current driver. Not actionable.
- **Other fixed-const agent timeouts** - audited. None warrants the "make ops timeouts
  configurable" lesson:
  - `runner.go:190` `cmd.WaitDelay = 5 * time.Second` bounds local pipe draining after a
    process kill. It is environment-independent (not workload- or hardware-scaled), so it is
    correctly a const, not operator config.
  - Perforce client operations during Prepare (`CreateStreamClient`, `ResolveHead`, sync, etc.
    in `client.go` via `exec.CommandContext`) inherit the runner's `runCtx`, which already
    carries the per-task timeout (`newRunner`, runner.go:42). They are bounded by an existing
    per-task knob, not an unbounded fixed const.
  - Reconnect backoff (`agent.go`) caps at 60s and is a transient retry interval, not an
    operation timeout; telemetry/sweep intervals are already env-configurable
    (`RELAY_TELEMETRY_INTERVAL`, `RELAY_WORKSPACE_SWEEP_INTERVAL`).
