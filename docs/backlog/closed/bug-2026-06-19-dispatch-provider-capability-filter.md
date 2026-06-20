---
title: Dispatch has no provider-capability filter; selectWorker can route source-bearing tasks to providerless workers
type: bug
status: closed
created: 2026-06-19
priority: medium
source: follow-up to closed bug-2026-06-10-source-tasks-run-without-workspace
---

> Closed 2026-06-20: implemented per the design spec
> `docs/superpowers/specs/2026-06-19-dispatch-provider-capability-filter-design.md`
> and plan `docs/superpowers/plans/2026-06-19-dispatch-provider-capability-filter.md`.
> Workers now report `supports_workspaces` at registration (proto field 12),
> persisted to `workers.supports_workspaces` (migration 000017, COALESCE keeps the
> value for old agents), and `selectWorker` hard-skips providerless workers for
> source-bearing tasks - holding them pending with one throttled log line per
> dispatch cycle.

# Dispatch has no provider-capability filter; selectWorker can route source-bearing tasks to providerless workers

## Summary
`selectWorker` (`internal/scheduler/dispatch.go:157-202`) can route a source-bearing task to a worker that has no workspace provider. As of 2026-06-19 the agent fails such a task fast with `TASK_STATUS_PREPARE_FAILED` (now mapped to a terminal `"failed"`), so the failure is loud rather than silent - but dispatch still does not *avoid* providerless workers. Warm-workspace affinity is only a score bonus, not a hard requirement.

## Context
Documented follow-up to [[bug-2026-06-10-source-tasks-run-without-workspace]] (closed 2026-06-19), whose fix added the agent-side guard and server-side terminal handling but explicitly deferred the dispatch-side filter. With the guard in place a misrouted task fails fast and (if `retries` are configured) is requeued, but it can be re-routed to another providerless worker and burn its retry budget without ever reaching a capable worker.

## Proposal
Have workers report workspace-provider capability over gRPC (e.g. a boolean/enum on registration or telemetry), and make `selectWorker` treat it as a hard requirement for source-bearing tasks - skip providerless workers entirely rather than merely scoring them lower. Add an unschedulable path so a source-bearing task with no capable worker available is held (and surfaced) rather than dispatched to fail.

## Acceptance / Done When
- A source-bearing task is never dispatched to a worker without a workspace provider.
- When no capable worker exists, the task stays pending (not failed) and the condition is observable.
- Non-source tasks are unaffected and still schedule on any eligible worker.

## Related
- `internal/scheduler/dispatch.go:157-202` (`selectWorker`)
- `internal/agent/runner.go` (agent-side `PREPARE_FAILED` guard)
- `internal/worker/handler.go` (`PREPARE_FAILED` -> terminal `"failed"`)
- [[bug-2026-06-10-source-tasks-run-without-workspace]]
