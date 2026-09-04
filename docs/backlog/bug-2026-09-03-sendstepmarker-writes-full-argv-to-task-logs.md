---
title: sendStepMarker writes the full argv into task_logs, so a secret passed as a command argument is stored and readable by every authenticated user
type: bug
status: open
created: 2026-09-03
priority: medium
source: prepare-failure-visibility spec section 2, and the Phase 4 security lens, 2026-09-03
---

# sendStepMarker writes the full argv into task_logs

## Summary

`Runner.sendStepMarker` writes `"=== relay step i/n === " + strings.Join(argv, " ")` into the task
log stream. A token, password or signed URL passed as a command argument is therefore stored
durably in `task_logs` and readable through the API by **every authenticated user** - the logs
endpoint is `auth`-only, with no owner scope and no admin gate, and the SSE path is the same.

## Repro / Symptoms

Submit a job whose command is `["tool", "--token", "SECRET"]`. `GET /v1/tasks/{id}/logs` returns a
line containing `SECRET`, as does the live SSE tail. Any authenticated account can read it, not
only the job's owner.

## Context

The 2026-09-03 prepare-failure-visibility batch added agent host-log lines that deliberately
log `argv[0]` only, with `%q` and clipping. **That narrowing bounds the new host-log surface and
closes nothing here** - the spec says so explicitly in its section 2, and the code comment beside
the narrowing says so too, so this item is the decision that was deferred rather than a newly
discovered hole.

The per-task identity slice strips reserved names from the child environment, but nothing
sanitises command arguments anywhere in the pipeline: `normalizeTaskCommands` checks only that
argv is non-empty - no content validation, no length bound, no character class.

## Proposal

A decision, not a line change. Three shapes, and they are not exclusive:

1. **Redact at the agent.** `sendStepMarker` logs `argv[0]` plus an argument count, or applies a
   pattern-based redaction. Cheapest; loses the debugging value of seeing the real command, and a
   pattern-based redactor is the per-branch-sanitiser shape that leaks by construction.
2. **Refuse secret-shaped arguments at ingest**, in the single job-spec pipeline. Fails closed and
   is visible to the submitter at submit time rather than silently mangling their log.
3. **Accept and document**, scoping task logs to the job's owner and admins so the audience is at
   least bounded. This is the one that changes the authorization model, and it interacts with
   whatever the log-export work assumes.

## Acceptance / Done When

- A secret passed as a command argument is either not stored, or is readable only by principals who
  could already see the job spec that contains it.
- README states which of the three the project chose, and says it where the log endpoint is
  documented rather than only in a design doc.

## Related

- `internal/agent/runner.go` - `Runner.sendStepMarker`
- `internal/jobspec/jobspec.go` - `normalizeTaskCommands`, which bounds counts and not content
- `internal/api/tasks.go`, `internal/api/events.go` - the `auth`-only read paths
- [[feature-2026-09-03-agent-task-lifecycle-logging]] - the slice whose `argv[0]` narrowing bounds
  only the new surface
- [[idea-2026-09-02-task-log-export-endpoint]] - a second consumer of whatever audience decision is made
