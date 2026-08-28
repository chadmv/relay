---
title: "The MCP tool schema does not advertise the job-spec bounds, against the repo's own convention"
type: idea
status: open
created: 2026-08-28
priority: low
source: Invariants lens of the Phase 4 review of the retry-bounds slice (2026-08-28)
---

# The MCP tool schema does not advertise the job-spec bounds, against the repo's own convention

## Summary

`jobspec.TaskSpec` carries no `jsonschema` tags, so `relay_submit_job` and `relay_create_schedule`
advertise `retries` and `timeout_seconds` as unbounded integers. An LLM client picks a value, sends
it, and learns the bound only from a runtime refusal - having already spent a tool call.

The repo's own convention is the opposite. `internal/mcp/wait.go` spells its bound directly into the
schema description (`relay_wait_for_job`'s poll timeout says max 300), which is precisely so the
client does not have to discover it by being refused.

## Context

Found by the invariants lens of the Phase 4 review of
`docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md`, which added
`maxRetries = 10` and `maxTimeoutSeconds = 604800` to `jobspec.Validate`. The bounds are correct and
enforced on every ingest path; this item is only about whether the MCP surface tells a client what
they are.

Worth noting why it is `idea` and not `bug`: nothing is broken. A refusal is well-formed, carries the
range in its message, and `ErrorIsTransient` classifies it as permanent, so a well-behaved client does
not retry. This is about the cost of the round trip and about an MCP client's ability to construct a
valid spec on the first attempt.

## Proposal

Add `jsonschema` descriptions to `TaskSpec.Retries` and `TaskSpec.TimeoutSeconds` naming the accepted
range, matching the wording now in README's job-spec table.

**One thing to settle first, because it is the reason this is not purely mechanical:** `jobspec` is
shared by the REST API, the CLI, MCP and schedrunner, so a `jsonschema` tag on those fields is a tag
every consumer carries. Decide whether the tags belong on `jobspec.TaskSpec` itself or on an
MCP-side mirror - and if a mirror, note that the project's Single job-spec pipeline invariant exists
specifically to stop parallel spec structs, so a mirror needs a better argument than tidiness.

Also worth deciding in the same pass: if the tags live on the shared type, the literal in the tag and
the constant in `Validate` can drift. The repo's precedent for keeping a documented bound in lockstep
with its enforcement is a test that goes RED when they diverge.

## Acceptance / Done When

- `relay_submit_job` and `relay_create_schedule` advertise both ranges in their tool schemas.
- Something goes RED if a schema literal and its `jobspec` constant disagree.
- The shared-type-versus-mirror decision is recorded wherever the tags land.

## Related

- Source: `internal/jobspec/jobspec.go` (`TaskSpec`, `maxRetries`, `maxTimeoutSeconds`),
  `internal/mcp/submit.go`, `internal/mcp/schedules_write.go`, `internal/mcp/wait.go` (the convention
  this follows)
- Filed from the review of [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]]
- Invariant in contact: Single job-spec pipeline
