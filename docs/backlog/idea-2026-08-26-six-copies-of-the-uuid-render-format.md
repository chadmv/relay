---
title: Six production copies of the UUID render format string, and drift in the server direction is caught by nothing
type: idea
status: open
created: 2026-08-26
priority: low
source: Phase 6 of the 2026-08-26-relay-logs-envelope-drift slice, which added the sixth copy and said so in its own comment
---

# Six production copies of the UUID render format string, and drift in the server direction is caught by nothing

## Summary

`fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])` appears
byte-identically in six production files:

| symbol | file |
|---|---|
| `uuidStr` | `internal/api/server.go` |
| (local helper) | `cmd/relay-server/main.go` |
| (local helper) | `internal/metrics/sweep.go` |
| (local helper) | `internal/scheduler/dispatch.go` |
| (local helper) | `internal/worker/handler.go` |
| `canonicalJobID` | `internal/cli/logs.go` |

Plus five more in test files (`internal/worker/handler_test.go` x3, `internal/api/workspaces_test.go`,
`internal/api/users_integration_test.go`, `internal/scheduler/dispatch_test.go`).

**The asymmetry is the point of the item, not the duplication.** The 2026-08-26 slice made this
format load-bearing across a trust boundary for the first time: `canonicalJobID` renders argv into
the one spelling the server emits, because `handleEvents` does not canonicalise `?job_id=` and the
broker filter is an exact string compare, so a mismatched spelling subscribes to a stream that
receives nothing, forever, with no heartbeat and no timeout.

- **Drift in the CLI direction is caught.** `TestWatchJobLogs_NonCanonicalJobID_IsResolvedNotRejected`
  hard-codes the expected spelling rather than deriving it from `canonicalJobID`, so a change there
  goes red.
- **Drift in the SERVER direction is caught by nothing.** `internal/api.uuidStr` is unexported, so
  no test relates the two, and the four other copies are unexported local helpers with no
  relationship to each other at all. Changing `uuidStr` - to uppercase, to the dashless form, to
  `google/uuid`'s `String()` - leaves every package green and silently breaks the CLI's `relay logs`
  subscription for non-canonical ids.

`canonicalJobID`'s own doc comment already states all of this. This item is the follow-through.

## Context

Only the **parse** half is already shared: `canonicalJobID` calls the same `pgtype.UUID.Scan` the
server's `parseUUID` calls, so what counts as a valid uuid cannot drift. The render half is the
duplicated one.

Worth noting what makes this `low` rather than higher: the format is a well-known standard, nobody
has any reason to change it, and five of the six copies are read-only renderings for log lines and
metrics keys. The one that now has a correctness consumer is `uuidStr`, and only because the CLI
compares against it.

Adjacent and NOT a duplicate: [[idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes]] argues
for keying maps on `[16]byte` so the *spelling-mismatch bug class* is deleted rather than encoded
around. That is the better fix wherever the string is an internal key. It does not apply here,
because the CLI must produce the exact bytes that go on a wire the server compares as a string.

## Proposal

Two options; decide, do not do both.

- **Option A - one exported renderer.** A `uuidStr` (or `RenderUUID`) in a small shared package that
  all six call. Cleanest, and it makes the CLI/server agreement structural rather than tested. Cost:
  a new dependency edge from `internal/cli` into a shared package, which is the kind of edge that
  has to be checked against the existing import graph before it is proposed as free.
- **Option B - leave the copies and pin the boundary.** Export the server's renderer for test use
  only, or add one test that asserts the server's rendering of a known uuid equals the CLI's, so the
  **one pair that has a correctness consumer** is guarded and the other four stay as they are. Much
  cheaper, and it targets the actual hole rather than the tidiness.

Option B is the recommendation on cost grounds; record whichever is chosen where `canonicalJobID`
lives, since its comment currently ends by naming the gap.

## Acceptance / Done When

- A change to `internal/api`'s rendering that would break `canonicalJobID`'s agreement with it
  reddens at least one test. Proven once, by making that change and observing the RED.
- Whichever option is taken, `canonicalJobID`'s comment stops saying the server direction is caught
  by nothing, because it no longer is.
- If Option A: all six production sites call the shared renderer and the test copies are left alone
  or converted deliberately, with the choice stated.

## Related

- `internal/cli/logs.go` (`canonicalJobID` and its doc comment, which enumerates the six copies)
- `internal/api/server.go` (`uuidStr`), `cmd/relay-server/main.go`, `internal/metrics/sweep.go`,
  `internal/scheduler/dispatch.go`, `internal/worker/handler.go`
- The bug this spelling class already caused, closed:
  [[bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones]]
- The delete-the-class alternative, where it applies:
  [[idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes]]
- `docs/retros/2026-08-26-relay-logs-envelope-drift.md`
