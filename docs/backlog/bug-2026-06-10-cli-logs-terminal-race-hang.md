---
title: relay logs/submit hang forever if the job goes terminal before the SSE subscribe
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

# relay logs/submit hang forever if the job goes terminal before the SSE subscribe

## Summary
`watchJobLogs` first GETs the job, returns early if terminal, then opens the SSE stream. The broker has no replay: only events published after `Subscribe` are forwarded. If the job reaches `done`/`failed` in the window between the GET and the subscribe (very plausible for short tasks a worker grabs instantly), no `job` event ever arrives and `scanner.Scan` blocks on an open connection with no keepalives. The CLI hangs until Ctrl-C, then shows a confusing "connection lost" error. The same window silently drops per-task logs for tasks that complete between the GET and the subscribe.

## Proposal
Subscribe first, then check the snapshot: after starting the stream, re-GET the job once; if already terminal, print logs and stop the stream. Track which tasks have been printed to dedupe. Alternatively, have the server emit a snapshot event on subscribe. Server-side SSE keepalive comment frames (see events.go notes) would also bound the hang.

## Related
- `internal/cli/logs.go:47-68` (`watchJobLogs`)
- `internal/api/events.go:25-39` (`handleEvents`, no replay)
