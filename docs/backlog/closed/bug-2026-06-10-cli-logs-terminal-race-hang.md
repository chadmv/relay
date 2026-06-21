---
title: relay logs/submit hang forever if the job goes terminal before the SSE subscribe
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-21
resolution: fixed
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

## Resolution
Fixed 2026-06-21 (cli-logs-terminal-race-hang). `watchJobLogs` was restructured from
GET-first-then-subscribe to subscribe-first-then-snapshot, so the terminal-before-subscribe
window is closed: every terminal transition is either before the subscribe (caught by a snapshot
GET taken AFTER the subscription is live) or after it (delivered over the stream, buffered in the
broker's 64-cap channel if it races the snapshot). Implemented with a new `onSubscribed func() bool`
hook on `relayclient.StreamEvents` (called right after the HTTP 200 - the server flushes immediately
after `broker.Subscribe`, so the subscription is established by then - and before the scan loop;
returning false returns nil without reading). `watchJobLogs` does the snapshot inside that hook,
printing every already-terminal task's logs and stopping the stream if the job is already terminal;
a `printed map[string]bool` set shared with the stream handler (both run on the same goroutine, no
lock) dedupes a task seen in both snapshot and stream. This also fixes the silently-dropped
per-task logs symptom. One `StreamEvents` production caller and two test callers updated for the new
4-arg signature; both `relay logs` and `relay submit` (the two `watchJobLogs` callers) are fixed.
Server/broker unchanged; SSE keepalives were left out of scope (they only bound an unrelated
network-stall hang). Deterministic RED test models the race (job `running` until subscribe, then
`done`, no SSE event) and was proven RED via a 2s context timeout before the fix; a dedup test and
the pre-existing already-terminal/happy-path tests round out coverage. Code review returned no
high/medium findings (one low: blank task names on the rare snapshot-GET-error fall-through, a net
improvement over the old abort-on-error behavior, documented in a code comment).
