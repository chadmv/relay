---
title: The CLI reconcile assumes job cancellation publishes no task frames, and no test pins that absence
type: idea
status: open
created: 2026-08-30
source: comment-policy retrofit; a struck logs.go census was the only record of the property
---

# The CLI reconcile assumes job cancellation publishes no task frames, and no test pins that absence

## Summary
`internal/cli/logs.go`'s final-snapshot reconcile exists because a cancelled job's tasks never get
terminal task frames on the SSE stream - the cancel path publishes job status only. That absence is
a property of the server's publish sites (`internal/scheduler/dispatch.go`, `internal/worker/handler.go`),
and nothing relates it to the CLI's assumption: a future task-frame publish added to the cancel path
would make the reconcile double-print or become dead weight, and nothing would go red.

## Context
The 2026-08-30 comment retrofit struck the census that enumerated the `Type: "task"` publish sites
from the reconcile's comment (drift-prone prose per the new CLAUDE.md comment policy). The policy's
remedy for a load-bearing claim is a named guard; this item is that guard's intake.

## Acceptance / Done When
- A test pins that cancelling a job publishes no task-typed events for its tasks (or the reconcile's
  behaviour is made insensitive to the property, and the comment updated to say why).

## Related
- internal/cli/logs.go (reconcileFinalSnapshot)
- internal/scheduler/dispatch.go, internal/worker/handler.go (task-frame publish sites)
