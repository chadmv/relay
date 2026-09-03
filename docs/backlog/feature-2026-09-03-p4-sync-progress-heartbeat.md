---
title: A multi-hour p4 sync emits nothing, so the task log cannot distinguish progress from a stall
type: feature
status: open
created: 2026-09-03
priority: medium
source: SDNM fork divergence analysis (relay_updates.md, PR-3), evaluated 2026-09-03
---

# A multi-hour p4 sync emits nothing, so the task log cannot distinguish progress from a stall

## Summary

`Client.SyncStream` runs `p4 sync -q`, which suppresses per-file output, and p4 block-buffers its
stdout to a pipe in any case, so a sync of a large stream produces no log line for hours. An
operator watching the task log or the worker-tasks panel cannot tell a live transfer from a wedged
one. The fix is a timer-driven heartbeat that summarises progress at an interval independent of
p4's output, with the per-file lines counted but never persisted.

## Proposal

All in `internal/agent/source/perforce/`, at the sync call site in `Prepare`.

1. **`syncProgress`**: `onLine` counts files and remembers the last depot path parsed by a
   `syncLineDepotPath(line)` helper (returns `""` for non-file lines so the prior path is kept);
   a goroutine emits a summary every interval through the `progress` callback; `stop(err)` closes
   the goroutine and emits one final `COMPLETE:` or `FAILED:` line. The clock and ticker are
   injectable so a test can drive a tick.
2. **`SyncStream` drops `-q`.** The per-file lines feed only `onLine`; nothing forwards them to
   `progress`. The `-q` removal and the throttle are one change and ship in one commit - split,
   the per-file volume of a 2 TB sync lands in `task_logs`.
3. **Summary content**: file count, elapsed time, last depot path, and free space on the workspace
   volume. **Drop the fork's "bytes written" figure**, or label it as a volume delta: it is the
   free-space difference on the whole volume, and with several workspaces syncing under one root
   (the normal multi-slot case) it is wrong in both directions and can only be clamped.
4. **Free space comes from an injected function**, `Config.FreeDiskBytes` or an adaptation of the
   `FreeDiskGB` the sweeper already takes, wired from the platform pair that already exists at
   `cmd/relay-agent/free_disk_unix.go` and `free_disk_windows.go`. Do not add a second platform
   pair inside the provider: the Windows file is already never compiled in CI
   ([[idea-2026-09-01-go-ci-never-compiles-or-runs-windows-code]]), and a second copy doubles that
   exposure.
5. **Interval is operator-configurable**: `RELAY_SYNC_HEARTBEAT_INTERVAL`, default 30s, `0`
   disables the timer (the final line still emits). Wire it through the same env-to-field path the
   other duration knobs use, and add it to the wiring guard
   ([[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]]). README env table row.
6. **Volume**: at the default this is about 120 `task_logs` rows per hour per syncing task, which
   is the figure to state when [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]] picks its
   ceiling.

Tests.

- `syncLineDepotPath` table, including Windows and POSIX local paths, a `- deleted as` line, a
  line without the separator, and an empty line.
- **The wiring test**: with a fake ticker, one tick and zero `onLine` calls emits exactly one
  summary containing `0 files`. The fork's test calls `emit()` directly and proves nothing about
  the timer; a cadence test must observe the consumer.
- `stop(err)` after a failure emits `FAILED` and the error, never `COMPLETE`; `stop(nil)` emits
  `COMPLETE` with the final count.
- The goroutine exits after `stop`: assert on the done channel or with a leak check.
- `perforce_integration_test.go`'s comment that `-q` suppresses per-file output is now false; the
  fixture's single file emits one `onLine` call and at least the final summary reaches `progress`.
  Update the comment and assert the `COMPLETE` line arrives.

## Acceptance / Done When

- A running sync produces one summary line per interval in the task log, whether or not p4 has
  written anything, and a final line that says whether it completed or failed.
- Per-file sync output never reaches `task_logs`.
- Free space is read through the agent's existing platform helper.
- The interval is env-configurable and documented.

## Related

- `internal/agent/source/perforce/perforce.go`, `client.go`; `cmd/relay-agent/free_disk_*.go`
- [[bug-2026-09-03-perforce-virtual-and-remap-streams-fail-to-sync]] - same call site; land after it
- [[feature-2026-09-03-preparing-task-status]] - the status the panel shows while this runs
- [[feature-2026-09-03-classify-out-of-disk-p4-errors]]
