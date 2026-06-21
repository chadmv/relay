---
date: 2026-06-21
topic: cli-logs-terminal-race-hang
branch: claude/gifted-meninsky-5fc18a
pr: "2026-06-21 / cli-logs-terminal-race-hang"
merge: "2026-06-21 / cli-logs-terminal-race-hang"
---

# Session Retro: 2026-06-21 - CLI logs/submit terminal-race hang

**TL;DR:** Closed `bug-2026-06-10-cli-logs-terminal-race-hang`. `relay logs` and `relay submit`
hung forever when a job went terminal in the window between `watchJobLogs`' initial job GET and
its SSE subscribe (the broker has no replay, so the missed terminal event never arrived and
`scanner.Scan` blocked with no keepalive). Restructured to subscribe-first-then-snapshot.
Autopilot iteration 4 (final) of a `/autopilot 4` run.

## What Was Built

- `internal/relayclient/client.go` - `StreamEvents` gained an `onSubscribed func() bool` 3rd param,
  called right after the HTTP 200 (the server flushes immediately after `broker.Subscribe`, so the
  subscription is live by then) and before the scan loop; returning false returns nil without
  reading the stream. One production caller + two test callers updated.
- `internal/cli/logs.go` - `watchJobLogs` restructured: the job snapshot GET moved into
  `onSubscribed`, so it runs AFTER the subscription is established. It prints every already-terminal
  task's logs and stops the stream if the job is already terminal; a `printed map[string]bool` set
  shared with the stream handler (same goroutine, no lock) dedupes the snapshot/stream overlap.
- Tests: a deterministic RED race test (job `running` until subscribe, then `done`, no SSE event;
  proven RED via a 2s ctx timeout) and a snapshot/stream dedup test; the pre-existing
  already-terminal and happy-path tests still pass.

## Key Decisions

- **Client-side subscribe-first, not a server change.** The backlog offered three options
  (client subscribe-first, server snapshot-on-subscribe, server keepalives). Subscribe-first is
  localized (one CLI function + one client hook), closes the race completely, and leaves the SSE
  contract and all other consumers untouched. Server keepalives were explicitly left out of scope
  (they only bound an unrelated network-stall hang, not this correctness bug).
- **The 200 IS the subscribe signal.** The server calls `flusher.Flush()` immediately after
  `broker.Subscribe`, so the client seeing HTTP 200 means the subscription is already registered -
  exactly the happens-before needed for the snapshot to be race-free. No new server signal required.
- **One fix covers both commands.** `watchJobLogs` is the sole shared path for `relay logs` and
  `relay submit`, and `StreamEvents` has exactly one production caller, so the surface was tiny.

## Process Note (important)

- **Subagents in a worktree autopilot run must operate in the WORKTREE path.** The plan doc's
  command blocks used `cd D:/dev/relay` (the main repo, checked out on `main`), so the backend
  engineer's three code commits landed on the main repo's local `main` instead of the conductor's
  worktree branch. Caught it from the engineer's own flag. Recovery: cherry-picked the three commits
  onto the conductor branch (they were based on the stale `0d4101f` but touched only `internal/cli`
  + `internal/relayclient`, untouched by the day's other PRs, so they applied cleanly), then
  `reset --hard origin/main` on the main repo to discard the accidental direct-to-main commits.
  Lesson: plan command blocks for a worktree session must use the worktree path, and the conductor
  should state the worktree path explicitly in the engineer's dispatch.
- Proportionate verification: a focused code-review (not the Postgres/p4d relay-verify fan-out, which
  is irrelevant to a CLI/httptest change). It confirmed the single-goroutine safety of the unlocked
  `printed`/`taskNames` maps and flagged one low cosmetic issue (blank task names on the rare
  snapshot-GET-error fall-through), addressed with a clarifying comment.

## Backlog Triage

- No new items. The one low review note was a documented tradeoff, not a defect.
