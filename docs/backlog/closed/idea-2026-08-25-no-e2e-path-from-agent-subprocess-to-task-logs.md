---
title: No test drives a real agent subprocess through gRPC into a task_logs row
type: idea
status: closed
closed: 2026-09-04
resolution: fixed
created: 2026-08-25
priority: medium
source: 2026-08-25 windows-crlf-log-lines slice - the integration lane surveyed the harness and found the wire between the two proof points is assumed, never exercised
---

# No test drives a real agent subprocess through gRPC into a task_logs row

## Summary
Nothing in this repo exercises `chunkWriter` -> `sendCh` -> the gRPC stream -> `HandleTaskLog` ->
`AppendTaskLog` as one path. `internal/worker/handler_tasklog_e2e_integration_test.go` calls
`worker.Handler.HandleTaskLog` directly via its test-exported method, skipping the wire entirely, and
`cmd/relay-server/grpc_admission_e2e_integration_test.go` references `internal/agent` only in
comments. Log-pipeline changes therefore land with two disjoint proof points and an assumption
between them.

## Context
Surfaced by the CRLF slice ([[bug-2026-08-25-windows-crlf-log-lines-render-blank]], closed). That
slice proved its agent half at the `chunkWriter` layer (27,994 exhaustive `(string, split)`
combinations) and its web half at the `logBuffer` layer, both thoroughly - but its own closing
acceptance criterion, "a CRLF-emitting subprocess renders its text on the job detail page", was
never observed end to end. The integration tester reported this explicitly rather than letting it
pass as covered.

The gap is structural, not specific to CRLF: any future change to chunk framing, epoch stamping, or
log ordering has the same blind spot.

## Proposal
A Go integration test (testcontainers Postgres, no browser) that starts a real `agent.Runner`
against a real gRPC worker service, runs a subprocess emitting known bytes, and asserts the stored
`task_logs` content equals the expected transform of those bytes. Byte fidelity is the assertion, so
it needs no Playwright and belongs in the existing integration lane.

## Acceptance / Done When
- A test exists in which bytes written by a real subprocess are read back from a real `task_logs`
  row, having crossed a real gRPC stream.
- It fails if the agent-side transform, the chunk framing, or the handler's ingest changes
  incompatibly.
- The CRLF case is one of its inputs, so the closed item's criterion becomes observed rather than
  argued.

## Related
- `internal/worker/handler_tasklog_e2e_integration_test.go` - the harness that stops short of the wire
- `internal/agent/runner.go` (`chunkWriter`, `flush`) and `internal/worker/handler.go` (ingest)
- [[bug-2026-08-25-windows-crlf-log-lines-render-blank]] - the slice that exposed the gap
- Distinct from [[idea-2026-08-24-e2e-harness-slice-2-agent-in-harness]], which runs a real agent
  inside the PLAYWRIGHT harness so browser surfaces become reachable. This is byte fidelity at the Go
  integration level and is closeable without a browser. Doing either does not close the other.
- Adjacent: [[idea-2026-08-23-integration-only-guards-ci-never-runs]]

## Notes

**The DISPATCH direction has the same gap, found 2026-09-01.** The per-task identity env vars slice
(`feature-2026-08-31-per-task-identity-env-vars`, closed) put four variables into every task
subprocess and proved it in three disjoint slices: `Dispatcher` to a `fakeSender`, a proto marshal
round trip, and `Runner.Run` handed a `*relayv1.DispatchTask` built in process. Nothing carried a
dispatch across a real gRPC stream into a real subprocess, so the composition was assumed exactly
as the log direction's is.

That widens this item rather than adding a second one: the harness proposed above (a real
`agent.Runner` against a real gRPC worker service, testcontainers Postgres, no browser) covers both
directions, since a dispatch has to reach the runner before any log can come back. If it is built,
assert both: bytes written by the subprocess arrive in `task_logs`, AND the four identity variables
the coordinator rendered are the ones the subprocess observes.

## Resolution

Closed by PR #201 (`dca0035`). `cmd/relay-server/agent_subprocess_e2e_integration_test.go`
drives a real `agent.Agent` against a real `grpc.NewServer` on `127.0.0.1:0`, over a real
`worker.Handler` and real Postgres, running a real subprocess, and asserts BOTH directions the
item asks for: the subprocess's bytes arrive in a real `task_logs` row, and the identity env vars
the coordinator rendered are the ones the subprocess observed. The CRLF case is one of the inputs,
so `bug-2026-08-25-windows-crlf-log-lines-render-blank`'s closing criterion is now observed rather
than argued.

Two corrections to this item, both recorded in the PR. Its Proposal was not executable as written:
`newRunner` is unexported and `provider` is settable only from inside `package agent`, so the
harness routes through the exported `agent.Agent`, whose `handleDispatch` builds a real `Runner`
per `DispatchTask` - no new seam, and the RED survives against HEAD. And this item's own note that
the harness would need no CI lane was wrong in the other direction: because a real agent is a
library object rather than a process, `cmd/relay-server` joined the existing `pg-integration` job,
so the guard runs on every push.

Eight mutations kill it, control green either side. The load-bearing one is
`newRunner(task.TaskId, 0, ...)` in `handleDispatch`: nothing else in the repo reddens on it, since
`dispatch_test.go` stops at a `fakeSender` and every `Runner` test supplies its own epoch. It dies
on a bounded 62s named timeout rather than a hang. 24 lane runs green, including six under
concurrent contention, with no leaked databases or connections.
