---
date: 2026-06-25
topic: mcp-token-mid-session-reload
branch: claude/happy-mendel-18687f
pr: "autopilot"
---

# Session Retro: 2026-06-25 - MCP token mid-session reload

**TL;DR:** Closed the bug "MCP token read once at startup - mid-session expiry requires
client restart." The MCP server read `cfg.Token` once at construction, so a token that
expired while the long-lived `relay mcp` process kept running broke every subsequent tool
call with `auth_expired` until the user killed and restarted the client. Fixed with a hybrid
reload-on-401-retry-once: `relayclient.Client`'s token is now `sync.RWMutex`-guarded behind
`Token()`/`SetToken()` accessors, and MCP gained a single `Server.do` chokepoint
(`internal/mcp/do.go`) that all ~21 tool call sites route through. On a 401 it calls an
injected config-backed reloader and, only if the reloaded token is non-empty AND different
from the token THIS call used, swaps via `SetToken` and retries exactly once; a second 401
surfaces `auth_expired`. Steady-state calls do zero extra I/O. Full unit suite + Docker
`-race` green, vet clean, review clean.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-06-25-mcp-token-mid-session-reload-design.md`.
- **Plan** `docs/plans/2026-06-25-mcp-token-mid-session-reload-plan.md`.
- **Fix** `internal/relayclient/client.go` - `token` field guarded by `sync.RWMutex`, new
  `Token()`/`SetToken()` accessors; `Do` and `StreamEvents` read via `Token()`.
- **Fix** `internal/mcp/do.go` - single `Server.do` chokepoint with the reload-and-retry-once
  orchestration plus `SetTokenReloader`; ~21 tool call sites rerouted from `s.client.Do` to
  `s.do`.
- **Wiring** `internal/cli/mcp.go` - installs a `LoadConfig`-backed reloader via
  `SetTokenReloader` after `NewServer`.
- **Tests** `internal/relayclient/client_settoken_test.go` (accessor + concurrent race),
  `internal/mcp/do_reload_disk_test.go` (in-package white-box reload-from-disk),
  plus 401-then-200 / 401-then-401 / identical-token short-circuit / non-401 passthrough /
  concurrency coverage.
- **Backlog** closed `docs/backlog/closed/bug-2026-05-09-mcp-token-mid-session-expiry.md`.

## What Went Well

- **Steady-state cost stayed at zero.** The hybrid trigger (reload only on a real 401, retry
  at most once, short-circuit on an identical token) means the overwhelmingly common
  valid-token path adds no file read, no parse, no extra request. The cost is paid only by a
  call that was genuinely going to fail anyway.
- **The fix landed as one chokepoint, not 21 edits.** Routing every tool through `Server.do`
  put the cross-cutting concern in exactly one place. The change to each tool was mechanical
  (`s.client.Do` -> `s.do`), and the reload policy lives in a single reviewable function.
- **Right-sized verification.** Client-side fix with unit + Docker `-race` + one adversarial
  review pass. No integration tester, no full `relay-verify` workflow - proportionate for a
  client auth-attachment fix. Review found nothing.

## Lesson: under concurrency, "did the token change?" is relative to what THIS op used, not global state

The plan's first cut compared the reloaded token against the **live** client token to decide
whether to retry. Under `-race` with concurrent tool calls, that spuriously short-circuited
retries: goroutine A hits a 401 and swaps in the refreshed token via `SetToken`; goroutine B,
also holding a stale-token 401, reloads the same refreshed token, compares it to the now-live
client token, sees them equal, and gives up - surfacing `auth_expired` even though B never
retried with the good credential.

The fix was to capture `usedTok := s.client.Token()` **before** the request and compare the
reload against that, not against current live state (`internal/mcp/do.go`). The general lesson:
a retry-skip predicate phrased as "has the shared resource changed?" is wrong under
concurrency - it must be "is the new value different from what MY operation actually used?"
Global current state can be mutated by a peer between your failure and your decision, so the
baseline for "did anything change for me" has to be a value each operation captures locally,
not a shared field re-read at decision time. This is the same class of bug as a compare-and-swap
that reads the comparand twice.

## Notable

- **A new reusable seam.** Collapsing 21 direct `s.client.Do` sites into one `Server.do`
  chokepoint is the kind of structural change that pays forward: any future cross-cutting
  client concern (request tracing, structured retry/backoff on 5xx, latency metrics) now has a
  single insertion point instead of 21. We are not building any of that now - flagging it as a
  seam that exists, not work to schedule.
- **Test seam without polluting the production API.** The conductor directed an in-package
  white-box test (`do_reload_disk_test.go`) over the disk-backed reload path rather than
  exporting a `Server.CallForTest`. The reload-and-retry behavior is exercised through the real
  `do` path; the production type gained no test-only exported method.
- **Docker is authoritative for `-race`.** `-race` could not run on the Windows host (tsan
  allocator error 87, per memory `reference_race_detector_toolchain`); the concurrency
  short-circuit bug was caught and confirmed fixed only under Docker. The Windows host silently
  cannot exercise this dimension - the race must be run in the container before claiming clean.

## Follow-on / Remaining Surface

Audited the other two token-bearing call paths for the same mid-session disruption; neither
warrants a backlog item:

- **Non-MCP CLI.** Every `relay` subcommand except `relay logs` does a single short-lived
  request and exits, so there is no mid-session window at all. `relay logs` is the only
  long-running command (`StreamEvents`), but its lifetime is bounded by one job's execution
  (it tails until the job goes terminal) - minutes, not the open-ended multi-day session that
  motivated the MCP fix. A token expiring inside a single `relay logs` run is an extreme corner
  case; wiring `SetToken` reload there would be speculative.
- **Agent.** `internal/agent` does not use `relayclient` at all (confirmed: zero references).
  It is pure gRPC with a long-lived agent token via `Credentials`, on a separate transport and
  credential type, and already has its own re-enrollment/revive path (recent PRs #81/#83). No
  `SetToken` reuse applies.

No new backlog items filed - nothing concrete and actionable beyond the closed bug.
