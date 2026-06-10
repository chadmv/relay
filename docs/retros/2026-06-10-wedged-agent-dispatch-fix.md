# Session Retro: 2026-06-10 — Wedged-Agent Halts Dispatch Fix

## What Was Built

Fixed the high-priority backlog bug "One wedged agent can halt task dispatch for
the entire farm" end-to-end, through the full brainstorm -> spec -> plan ->
subagent-driven-development flow.

Three behavioral changes:
- **Bounded send** (`internal/worker/sender.go`): `workerSender.Send` now races
  a `time.NewTimer(sendTimeout)` (5s, package var) alongside the buffer-enqueue
  and close cases, returning a new `ErrSendTimeout` instead of blocking forever
  when the 64-slot buffer fills. Cramped struct fields were renamed for clarity
  (`ch`->`queue`, `stop`->`stopReq`, `done`->`closed`, receiver `ws`->`sender`).
- **Server-side gRPC keepalive** (`cmd/relay-server/main.go`):
  `keepalive.ServerParameters{Time: 30s, Timeout: 10s}` on `grpc.NewServer` so a
  wedged/half-open stream is torn down (~40s), unblocking the parked send
  goroutine and triggering the normal disconnect -> grace -> requeue path.
- **Observability** (`internal/scheduler/dispatch.go`): a `log.Printf` on the
  dispatcher send-failure branch so a wedged worker is diagnosable, not silent.

Two new unit tests drive the real full-buffer block deterministically
(`TestWorkerSender_TimesOutWhenBufferFull`,
`TestWorkerSender_ReturnsDisconnectedWhenStreamDiesMidWait`), plus a
non-build-tagged test hook `SetSendTimeoutForTest`.

## Key Decisions

- **Minimal scope: bound + keepalive only.** Rejected proactive eviction of the
  wedged worker and a dispatcher cooldown. Worst case is a few wasted ~5s
  timeouts during the keepalive detection window - acceptable for the
  simplicity.
- **Server-only keepalive.** Agent/client-side keepalive deferred to the
  existing `bug-2026-06-10-reconnect-backoff-never-resets` item.
- **Hardcoded `sendTimeout` (var, not env).** Matches CLAUDE.md "no speculative
  configurability"; a package var keeps it test-overridable.
- **Why a timeout is safe (no duplicate dispatch):** `Send` only blocks on the
  buffer when full, which means the message was never enqueued; a timeout
  therefore cannot later deliver a duplicate. The requeue-on-error path is the
  existing one.
- **Clean scope boundaries.** The related `RequeueTask` epoch-bump and
  stale-stream teardown bugs were explicitly left to their own backlog items;
  the spec documents why the timeout path is safe without the epoch bump.

## Problems Encountered

- **Race detector unavailable on this Windows box.** `go test -race` fails with
  a `0xc0000139` entry-point error on a trivial unrelated package - environmental
  toolchain breakage, not a code defect. The code-quality reviewer reasoned
  through the concurrency manually instead (all shared state is channel-only).
- **`make` not on PATH in the bash tool.** Ran the equivalent `go build` per
  binary and `go test ./... -timeout 120s` directly; `web/dist` already existed
  so the embed built cleanly.

## Known Limitations

- See [`bug-2026-06-10-cancel-disable-handlers-send-synchronously`](../backlog/bug-2026-06-10-cancel-disable-handlers-send-synchronously.md) — Cancel/disable handlers send synchronously to workers (up to N x ~5s in the request path for a multi-task job on one wedged worker).

## Improvement Goals

- The two trivial config/log tasks (keepalive, dispatch log) each got a single
  combined spec+quality review rather than two passes - a reasonable adaptation
  of subagent-driven-development for changes with no logic to quality-review.
  Worth keeping as the norm for one-liner tasks.

## Files Most Touched

- `internal/worker/sender.go` - core fix: timeout select, `ErrSendTimeout`, field renames.
- `internal/worker/sender_test.go` - two new deterministic full-buffer tests + `wedgedStream` stub.
- `internal/worker/export_sender_test.go` - new test hook to lower `sendTimeout`.
- `cmd/relay-server/main.go` - server keepalive params.
- `internal/scheduler/dispatch.go` - send-failure log line.
- `docs/superpowers/specs/2026-06-10-wedged-agent-halts-dispatch-design.md` - design spec (+ per-request worst-case clarification).
- `docs/superpowers/plans/2026-06-10-wedged-agent-halts-dispatch.md` - implementation plan.

## Commit Range

67fc84144312fee27c2b80c9be12b48e9fa1765f..6ac89dfccb96cccf2ba335fb0c02e7d06ac95dda

(Narrative covers this session's 6 commits, `a845c6b..6ac89df`. The commits
between the prior retro and this session's start were earlier sessions'
backlog/codebase-review work, not separately retro'd.)
