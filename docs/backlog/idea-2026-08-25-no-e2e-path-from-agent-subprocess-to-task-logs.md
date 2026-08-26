---
title: No test drives a real agent subprocess through gRPC into a task_logs row
type: idea
status: open
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
