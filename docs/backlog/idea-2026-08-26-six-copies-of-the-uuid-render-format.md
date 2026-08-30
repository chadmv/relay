---
title: Six production copies of the UUID render format string, and drift in the two PUBLISHER copies is caught by nothing
type: idea
status: open
created: 2026-08-26
priority: low
source: Phase 6 of the 2026-08-26-relay-logs-envelope-drift slice, which added the sixth copy and said so in its own comment
---

# Six production copies of the UUID render format string, and drift in the publisher copies is caught by nothing

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

Plus six more in test files (`internal/worker/handler_test.go` x3, `internal/api/workspaces_test.go`,
`internal/api/users_integration_test.go`, `internal/scheduler/dispatch_test.go`).

**The asymmetry is the point of the item, not the duplication.** The 2026-08-26 slice made this
format load-bearing across a trust boundary for the first time: `canonicalJobID` renders argv into
the one spelling the server emits, so a mismatched spelling subscribes to a stream that receives
nothing, forever, with no heartbeat and no timeout.

- **Drift in the CLI direction is caught.** `TestWatchJobLogs_NonCanonicalJobID_IsResolvedNotRejected`
  hard-codes the expected spelling rather than deriving it from `canonicalJobID`, so a change there
  goes red.
- **Drift in `internal/api.uuidStr` is now caught too, as of 2026-08-30.** See the amendment below.
- **The four other copies are still related to nothing.** They are unexported local helpers with no
  relationship to each other, and no test pins any of them to a literal.

## Amendment 2026-08-30 - narrowed, and the argument got stronger

The slice that shipped `canonicalJobIDFilter` (`internal/api/events.go`) changed two things this
item asserted, and neither of them weakens it.

**What is now false, and was left standing by that slice's own "correct the sites" commit:**

- *"`canonicalJobID` renders argv ... because `handleEvents` does not canonicalise `?job_id=`."*
  It does canonicalise it, since 2026-08-30. `canonicalJobID` is still not redundant - the reason
  narrowed to the one reader no server change can reach, `jobSnapshotUnusable`'s client-side id
  comparison, plus keeping a non-canonical spelling out of the request line against an OLDER
  `relay-server` a CLI built from this tree may be pointed at.
- *"Drift in the SERVER direction is caught by nothing - `internal/api.uuidStr` is unexported, so no
  test relates the two."* Refuted. `TestCanonicalJobIDFilter` (`internal/api/events_test.go`,
  default lane, no container) hard-codes the canonical literal and asserts 8 spellings render to it
  through `uuidStr`. **Measured**: rendering `uuidStr` uppercase turns 8 of its subtests red - every `accepted/` row, because all eight compare against the single hard-coded `canonical` literal, so they redden or survive together; every `passthrough/` row survives, since the mutation is only reachable past the guard. (An earlier revision of this line said 5, which is not reachable by any case mutation.) This
  satisfies the first Acceptance bullet below on its own, by accident rather than by intent.

**Why the item gets STRONGER, not weaker.** The format is now load-bearing on BOTH sides of the
boundary, not just the CLI's. `?job_id=` canonicalisation only works because the subscribe side's
`internal/api.uuidStr` renders what the publish side emits - and the publish side is
`internal/scheduler.uuidStr` (3 of the 8 JobID-carrying `broker.Publish` sites) and
`internal/worker.uuidStr` (3 more), which are DIFFERENT unexported functions that no test relates to
`internal/api`'s. If either publisher copy drifts, the SSE fix silently evaporates back into the bug
it closed. That is a strictly larger hole than the CLI one this item was filed for.

**Be precise about what is and is not caught, because an earlier revision of this item was not.**
Drifting a publisher copy alone DOES redden something today - measured 2026-08-30, uppercasing
`internal/worker.uuidStr` fails `internal/worker`, and `internal/scheduler.uuidStr` fails
`cmd/relay-server` and `internal/scheduler`. Those failures are TaskID-keyed and watchdog-key
assertions, not the job-status `JobID` path `canonicalJobIDFilter` rides. So the accurate statement
is about the PATH, not the package: nothing relates `internal/api`'s rendering to a publisher's on
that path, and a drift applied to both sides together leaves the whole tree green.

**Remaining scope**, therefore: the two publisher copies plus `cmd/relay-server/main.go` and
`internal/metrics/sweep.go`. `internal/api`'s and the CLI's are each pinned to a hard-coded literal
now, by two independent tests and by no relationship between the functions.

`canonicalJobID`'s doc comment records all of this as of 2026-08-30.

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

- ~~A change to `internal/api`'s rendering that would break `canonicalJobID`'s agreement with it
  reddens at least one test. Proven once, by making that change and observing the RED.~~ **Done
  incidentally 2026-08-30** by `TestCanonicalJobIDFilter`; proven by mutating `uuidStr` to uppercase
  and observing 8 red subtests - every `accepted/` row. Not the item's own doing, and it does not close the item.
- A test fails when `internal/api`'s rendering and a PUBLISHER's disagree **with each other** -
  proven by changing one and leaving the other, in both directions. Deliberately NOT "a change to a
  publisher's rendering reddens at least one test": that is **already true at HEAD** (measured
  2026-08-30) and would let this item close vacuously, which is the same accidental-coverage shape as
  the bullet struck through above. What is uncovered is the AGREEMENT between the two, not either one
  alone.
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
