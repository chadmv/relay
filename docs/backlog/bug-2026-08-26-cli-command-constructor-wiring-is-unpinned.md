---
title: Twelve internal/cli Command constructors are built by no test, and two of them wire a transposable same-typed writer pair
type: bug
status: open
created: 2026-08-26
priority: low
source: Phase 6 of the 2026-08-26-relay-logs-envelope-drift slice, narrowed after that slice closed its own two constructors
---

# Twelve internal/cli Command constructors are built by no test, and two of them wire a transposable same-typed writer pair

## Summary

A `Command` constructor's `Run` closure is where argv, config and the process's real streams are
wired to the `doX` function that does the work. That wiring is a **different subject** from the
`doX` function, and `internal/cli` tests almost entirely target the latter. Seventeen `XCommand()
Command` constructors exist; **twelve are constructed by no test at all** and reach production only
through `cmd/relay/main.go`.

Two of the twelve carry a same-typed adjacent-argument pair, which is the shape that transposes
silently because the compiler cannot object:

- `internal/cli/agent_enroll.go` (`AgentCommand`) - `doAgentEnroll(ctx, args[1:], cfg, os.Stdout,
  os.Stderr)`. Exactly the shape closed for `LogsCommand`/`SubmitCommand` on 2026-08-26, still open
  here, with no test constructing the command.
- `internal/cli/mcp.go` (`MCPCommand`) - `srv.Run(ctx, os.Stdin, os.Stdout)`. Both are `*os.File`,
  so `os.Stdout, os.Stdin` compiles and would make the MCP server read from stdout and write to
  stdin, breaking the stdio transport outright.

The remaining ten pass a single `os.Stdout` and have no transposition risk, only the general
unpinned-wiring exposure.

## Context

**This item is the residue of a closed case, and the closed case is the measurement.** The
2026-08-26 envelope-drift slice gave `relay logs` and `relay submit` a second writer and pinned the
split *inside* `doLogs`/`doSubmit` with two good tests. Transposing `os.Stdout, os.Stderr` at the
two constructors above them left all 21 packages green. `internal/cli/command_writer_wiring_test.go`
now closes those two, and the mutation that proves it is the useful number: each transposition was
killed by the new test **and by nothing else in the tree**.

**`MCPCommand` is the one worth reading twice, because a coverage audit scores it wrong.**
`mcp_test.go` does construct `MCPCommand()`, so any audit keyed on constructor names counts it as
covered. But `TestMCPCommand_NotLoggedIn` errors out on config before reaching `srv.Run`, so the
`os.Stdin, os.Stdout` pair is never evaluated. Coverage by constructor name is the wrong instrument
here; the question is whether a test reaches the wiring line.

## Repro / Symptoms

Transpose the two arguments at `internal/cli/agent_enroll.go`'s `doAgentEnroll` call, or at
`internal/cli/mcp.go`'s `srv.Run` call, and run `go test ./... -count=1`. Both pass.

## Proposal

Use `captureStdStreams` (`internal/cli/admin_output_test.go`), whose doc comment already states this
exact rationale, and which `command_writer_wiring_test.go` now uses for the two closed cases. Follow
that file's pattern: assert **positionally and in both directions** - the expected content is on the
stream it belongs to and **absent** from the other - since a one-sided presence assertion is
fail-open on which writer is which.

For `MCPCommand`, the wiring line is only reachable past the config check, so the test needs a
`*Config` that gets there. If that is impractical, say so on this item rather than leaving the
constructor scored as covered.

For the ten single-writer constructors, the cheaper resolution is to record that they are
single-writer and not transposable, rather than to write ten tests. The value here is in the pair
shape, not in constructor coverage for its own sake.

## Acceptance / Done When

- `AgentCommand`'s writer pair is pinned, proven by transposing the arguments once and recording
  that the new test goes RED and that nothing else does.
- `MCPCommand`'s `os.Stdin, os.Stdout` pair is either pinned the same way, or this item records why
  the wiring line cannot be reached and that the constructor is knowingly unpinned.
- The ten single-writer constructors are documented as not transposable, so a future audit does not
  re-open the question.

## Related
- `internal/cli/agent_enroll.go` (`AgentCommand`), `internal/cli/mcp.go` (`MCPCommand`)
- The closed half and its mutation evidence: `internal/cli/command_writer_wiring_test.go`
- The helper and the precedent: `internal/cli/admin_output_test.go` (`captureStdStreams`)
- [[bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped]] - also a defect that
  lives in `internal/cli` and `internal/mcp` at once, found by the same slice
- `docs/retros/2026-08-26-relay-logs-envelope-drift.md`
