---
title: Source-bearing tasks silently run without a workspace when the provider is nil
type: bug
status: open
created: 2026-06-10
priority: high
source: full-codebase review (2026-06-10)
---

# Source-bearing tasks silently run without a workspace when the provider is nil

## Summary
`Runner.Run` gates the prepare phase on `task.Source != nil && r.provider != nil`. When the provider is nil (p4 missing at preflight, or `RELAY_WORKSPACE_ROOT` unset), a task that has a source spec falls straight through to command execution in the agent's cwd with no sync and no `P4CLIENT`. The comment in `cmd/relay-agent/main.go:77-80` claims source tasks "will fail at dispatch with the existing 'no source provider' path", but no such path exists; the dispatcher's `selectWorker` has no provider-capability filter (warm-workspace affinity is only a score bonus). A misconfigured worker will "successfully" run builds against whatever happens to be on disk.

## Proposal
In `Run`:

```go
if task.Source != nil && r.provider == nil {
    r.send(/* TASK_STATUS_PREPARE_FAILED, ErrorMessage:
        "task has a source spec but this worker has no workspace provider" */)
    return
}
```

Longer term, consider a provider-capability requirement in dispatch so such tasks are never sent to providerless workers.

## Related
- `internal/agent/runner.go:113`
- `cmd/relay-agent/main.go:77-80` (stale comment)
- `internal/scheduler/dispatch.go:157-199` (`selectWorker`)
