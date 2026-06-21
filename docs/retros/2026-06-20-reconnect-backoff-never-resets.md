---
date: 2026-06-20
topic: reconnect-backoff-never-resets
branch: claude/blissful-brown-c7780a
pr: "2026-06-20 / reconnect-backoff-never-resets"
merge: "2026-06-20 / reconnect-backoff-never-resets"
---

# Session Retro: 2026-06-20 - Reconnect backoff never resets

**TL;DR:** Closed `bug-2026-06-10-reconnect-backoff-never-resets`. Both the agent reconnect
loop and the server's `NotifyListener` doubled their reconnect backoff over the process
lifetime and never reset it (the agent's reset was unreachable; the listener's only fired
on shutdown), so after ~6 disconnects the backoff pinned at the 60s cap and every later
blip cost a 60s outage. Fixed by signalling an established session out of the connect/
session call and resetting backoff to 1s before sleeping.

## What Was Built

- `internal/agent/agent.go`: `connect()` returns `(registered bool, err error)`,
  `registered = true` set once the coordinator accepts registration (before the `sendWG`
  add / `runSender` spawn, preserving the one-bounded-sender ordering). Dead always-nil
  error return on `buildRegisterRequest` removed.
- `internal/scheduler/notify.go`: `session()` returns `(listened bool, err error)`,
  `listened = true` once both `LISTEN`s succeed.
- Both Run loops reset `backoff` to 1s BEFORE sleeping when the prior session was healthy,
  then double via the pure `nextReconnectBackoff(current, healthy)` helper on the unhealthy
  path. New deterministic unit tests (no real sleeps) via a `reconnectSleep` test seam.

## Key Decisions

- **Bool-signal reset over elapsed-time threshold:** a `registered`/`listened` bool is a
  precise signal of an established session; the wall-clock `time.Since(start) > 30s` idea
  false-positives on slow dials and false-negatives on healthy-but-short sessions. It also
  makes the reset a pure function of `(currentBackoff, healthy)`, deterministically
  testable with zero sleeps and zero `-race` dependence (relevant: `make test-race`
  excludes `internal/agent` on Windows).
- **Reset BEFORE the sleep, not after** (verification catch): the first implementation
  reset after `time.After(backoff)`, so only the second reconnect benefited - the headline
  symptom (prompt reconnect after a healthy drop) was unfixed. Verification flagged this as
  two medium findings; corrected to reset-before-sleep, with deterministic RED->GREEN tests
  proving the first reconnect after a capped-backoff healthy drop now waits ~1s.

## Backlog Triage

- None. Verification's low findings (verbatim `nextReconnectBackoff` duplicated across two
  packages; the test-only `dialContextFn`/`reconnectSleep` seams) were rated acceptable /
  no-action by the reviewer per the simplicity guideline. No new items filed.

## Process Note

- The reset-before-vs-after-sleep ordering bug slipped past the engineer's own tests
  (which only asserted the helper and the post-sleep value) but was caught by the
  relay-verify pass, which reasoned about the actual first-reconnect wall-clock scenario.
  Reinforces running verification on logic that "looks done" - the unit tests were green
  while the headline behavior was still broken.
