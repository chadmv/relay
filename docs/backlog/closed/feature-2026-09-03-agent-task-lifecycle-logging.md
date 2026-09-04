---
title: The agent logs nothing between accepting a task and finalizing it, so a wedged prepare or step is invisible on the host
type: feature
status: closed
created: 2026-09-03
closed: 2026-09-03
resolution: fixed
priority: low
source: SDNM fork divergence analysis (relay_updates.md, PR-6), evaluated 2026-09-03
---

# The agent logs nothing between accepting a task and finalizing it, so a wedged prepare or step is invisible on the host

## Summary

`Runner.Run` in `internal/agent/runner.go` logs only on finalize failures and forced cancels. An
operator on the worker host cannot tell from the agent's log whether a task is syncing, executing
step 2 of 5, or stuck, and a prepare failure's cause appears nowhere on the host once the send has
gone out. A fork of relay added four lifecycle lines; this item ports them with one narrowing.

## Proposal

1. Four `log.Printf` calls in `Runner.Run`: before `provider.Prepare` (`preparing workspace`), on a
   prepare error (`PREPARE_FAILED: <err>`), before each command (`exec step i/n`), and after each
   command (`step i exited (exit=..., err=...)`).
2. **Log the step index and `argv[0]` only, never the full `argv`.** The per-task identity slice
   strips reserved names from the environment, but nothing sanitises command arguments, and a
   token or password passed as an argument would land in the host log verbatim. The fork logs the
   whole vector; do not port that.
3. The sync start, complete and failed lines the fork adds inside the perforce provider should go
   through the `progress` callback rather than `log.Printf`. The callback already reaches
   `task_logs` as `LOG_STREAM_PREPARE` chunks, so those lines become visible through the API and
   the SPA, not only on the host. One line each; no per-file output (that is
   [[feature-2026-09-03-p4-sync-progress-heartbeat]]'s job).
4. Rebase note: only one commit has touched `runner.go` since the fork's base (the identity env
   vars slice), so the fork's hunks apply with an offset and no conflict.

Tests, in `internal/agent`: capture the log through `log.SetOutput` for the test's duration (restore
in cleanup; the package's tests run in parallel only where they already do). Assert the
`PREPARE_FAILED` line contains the provider's error text; assert a step line for a command such as
`["tool", "--token", "SECRET"]` contains `tool` and does not contain `SECRET`. The second test is
the one that carries the narrowing and must be written first.

## Acceptance / Done When

- The agent log shows prepare start, prepare failure with cause, each step's start with its
  index and program name, and each step's exit.
- No command argument beyond `argv[0]` appears in any lifecycle line.
- Sync lifecycle lines from the provider arrive as task log lines, not host log lines.

## Related

- `internal/agent/runner.go` - `Runner.Run`, `makePrepareProgressFn`
- `internal/agent/source/perforce/perforce.go` - `Provider.Prepare`, the sync call site
- [[bug-2026-09-03-prepare-failure-error-message-is-discarded]] - the server-side half of the
  same prepare-failure visibility gap

## Resolution

Fixed on `claude/top-3-roadmap-items-65856f`. **Five host-log lines, not the four this item
specified:** the no-provider `PREPARE_FAILED` returns above the prepare line and would otherwise
leave no host record at all, so it got its own. The others are prepare start, prepare failure with
cause, each step's start, and each step's exit - plus a sixth added during review for the
`cmd.Start()` failure, which otherwise announced a step and then fell silent.

The `argv[0]` narrowing shipped as specified and is pinned by a test whose secret lives in
`argv[1:]`. Review found the narrowing bounds secrecy and not injection, so every caller-supplied
string on these lines now carries `%q` and a clip - `argv[0]` is job-author-controlled with no
content validation anywhere, so a newline in it could otherwise forge host-log lines attributing
outcomes to other tasks.

The provider's sync lifecycle goes through the `progress` callback into `task_logs` as specified,
and the failure bracket deliberately carries no error text, because the coordinator now stores the
cause exactly once. `handle.Release()` was moved above that bracket: `progress` flushes through a
send bounded only by the agent context, so it could park while holding the workspace.

**Two things this item's text got wrong**, both harmless: its rebase note was a history claim
nothing in the tree could confirm, and its test hedge about `t.Parallel` was unnecessary -
`internal/agent` has none, which the new tests preserve.

Residuals filed separately:
[[bug-2026-09-03-sendstepmarker-writes-full-argv-to-task-logs]] (the narrowing closes nothing
already open - `sendStepMarker` still writes the whole vector into `task_logs`),
[[bug-2026-09-03-provider-progress-parks-while-holding-the-workspace]] (two callbacks still park
under the hold and cannot be reordered away), and
[[feature-2026-09-03-sendfinalstatus-carries-no-error-message]].
