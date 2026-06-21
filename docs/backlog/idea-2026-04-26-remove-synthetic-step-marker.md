---
title: Remove synthetic step marker text line once consumers use step_index/step_total
type: idea
status: open
created: 2026-04-26
source: 2026-04-26 multi-command-tech-debt retro — Known Limitations
---

# Remove synthetic step marker text line once consumers use step_index/step_total

## Summary
The synthetic `=== relay step N/M ===` text marker line in `sendStepMarker` (`internal/agent/runner.go`) is now redundant — `step_index` and `step_total` on `TaskLogChunk` carry the same information in structured form. The text line was retained for one release so existing log-tailing tools see no behavioral change. Once any consumers that render per-step status have been updated to read the structured fields, the text line should be removed (one-line deletion in `sendStepMarker`).

## Blocked - precondition not met (found 2026-06-21, autopilot)
The premise ("consumers now read the structured `step_index`/`step_total` fields") is **false**, and
cannot be true today: the server never persists or exposes those fields. `Handler.handleTaskLog`
(`internal/worker/handler.go`) calls `AppendTaskLog` with only `TaskID`/`Stream`/`Content`/`Epoch` -
`StepIndex`/`StepTotal` are dropped at persist time. The `task_logs` table has no step columns, and the
`GET /v1/tasks/{id}/logs` response (`logEntry` in `internal/api/tasks.go`) returns only
`seq`/`stream`/`content`/`created_at`. So the structured fields exist *only* on the live agent->server
gRPC `TaskLogChunk`; every API/CLI/web/MCP/Python-SDK consumer sees per-step boundaries **solely**
through the `=== relay step N/M ===` text line embedded in the content.

Removing the text line now is therefore a regression (step boundaries vanish from stored/rendered logs
with no structured replacement available to consumers), not a quick win.

**Real prerequisite** (a feature, not a one-line deletion): persist `step_index`/`step_total` onto
`task_logs` (migration + `AppendTaskLog` + `handleTaskLog`), expose them on the logs API response, and
migrate the step-rendering consumers to read them. Only after that is the text-marker deletion safe.
Deprioritized until that prerequisite lands; the `runner_multistep_test.go` assertion on
`"=== relay step"` count would also move to the structured fields at that point.

## Related
- Structured fields added in commit `c459104` (agent: structured step_index/step_total on TaskLogChunk)
- `internal/agent/runner.go` — `sendStepMarker` function
- `internal/worker/handler.go` — `handleTaskLog` / `AppendTaskLog` (drops the step fields)
- `internal/api/tasks.go` — `logEntry` / `handleGetTaskLogs` (does not expose step fields)
- `internal/agent/runner_multistep_test.go` — asserts the text-marker count
