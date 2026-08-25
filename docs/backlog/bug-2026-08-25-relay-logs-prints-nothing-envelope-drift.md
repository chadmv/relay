---
title: relay logs prints nothing for any job - the CLI decodes a bare array from an envelope endpoint and swallows the error
type: bug
status: open
created: 2026-08-25
priority: high
source: Production use in another environment, 2026-08-25 - `relay logs` returned no output for a job whose logs the web UI could show
---

# relay logs prints nothing for any job - the CLI decodes a bare array from an envelope endpoint and swallows the error

## Summary
`printTaskLogs` decodes `GET /v1/tasks/{id}/logs` into a bare Go slice, but the handler has returned
a pagination envelope since 2026-05-08. Decoding a JSON object into a slice fails, and
`printTaskLogs` is documented as best-effort and discards the error, so `relay logs` prints nothing
for every task of every job and reports no reason. The CLI suite is green because its test server
hand-writes the old bare-array shape.

## Repro / Symptoms
`relay logs <job-id>` on any job with log output.

Observed: zero lines on stdout and stderr. Expected: the task's log lines.

```
exit=1
stdout+stderr lines=0
```

The exit code is a red herring and is correct on its own terms: the measured job's status was
`failed`, so `doLogs` returns `silentError{}` (`internal/cli/logs.go:39`). A successful job exhibits
the same empty output with exit 0.

Server side, the same task's logs are intact - the live response was
`{"items":[...],"next_seq":0,"total":264}`.

## Context
`internal/cli/logs.go:126` declares the decode target as `[]struct{ Stream, Content string }`.
`internal/api/tasks.go:132` writes `map[string]any{"items":…, "next_seq":…, "total":…}`. The envelope
landed in `a90c727` (2026-05-08, "paginate GET /v1/tasks/{id}/logs with since_seq+limit envelope");
the CLI was never updated.

`Client.Do` returns the `json.Decode` error unchanged (`internal/relayclient/client.go:100`), so the
failure is reported correctly by the client layer. It dies at the call site: `printTaskLogs` carries
the comment "Errors are silently ignored - best-effort output" and bare-returns on any error. **That
swallow is what converts a loud shape mismatch into a silent no-output bug**, and it is the more
general defect - any future breakage of this call is equally invisible.

Both call sites are affected: `logs.go:72` (the already-terminal job path) and `logs.go:96` (the
live SSE-follow path).

**Why CI is green: the fixture encodes the shape the server stopped sending.** The test server at
`internal/cli/logs_test.go:47` responds to the logs route with a hand-written bare array, so the
`TestLogs*` family asserts the CLI agrees with the fixture, never with the handler. This is a live,
confirmed instance of [[idea-2026-08-23-cli-tests-never-hit-real-server]], which predicted exactly
this: "A handler response-shape change can silently break every `relay` subcommand with the whole
suite green."

## Proposal
1. Decode the envelope. The generic `relayclient.PageEnvelope[T]` exists
   (`internal/relayclient/page.go:11`) but is keyed on `next_cursor`; this endpoint pages on
   `next_seq`, so `FetchAllPages` does **not** apply unmodified. Either add a seq-paging sibling or
   decode the three fields locally - do not force this endpoint onto the cursor helper without
   checking which key it emits.
2. Page. The CLI currently ignores `next_seq` entirely; there is no consumer of that field anywhere
   in Go outside the handler that writes it (`grep -rn next_seq --include=*.go`). Fixing only the
   decode yields the first page and silently truncates a long log, which is the same class of
   silent incompleteness as the original bug.
3. Decide what `printTaskLogs` does on error. Best-effort was a defensible choice when the call
   worked; it is what hid this for three and a half months. At minimum distinguish "no logs" from
   "could not fetch logs" on stderr.
4. Fix the fixture, or the fix is untested. A test that keeps the hand-written bare array will pass
   against a CLI that still cannot talk to the real server.

## Acceptance / Done When
- `relay logs <job-id>` prints the task's log lines for a job with output, on both the terminal-job
  and the SSE-follow paths.
- A log longer than one page is printed in full, not truncated at the first page.
- The CLI test fixture emits the envelope shape the handler actually writes. Reverting the
  production fix while keeping the new fixture turns the test RED - prove this once; a fixture
  change that leaves the test green regardless is not covering anything.
- A fetch failure is distinguishable from an empty log at the terminal.

## Related
- `internal/cli/logs.go:126` (the decode), `:123-124` (the swallow comment), `:72` and `:96` (call sites)
- `internal/api/tasks.go:132` (the envelope), `a90c727` (the commit that introduced it, 2026-05-08)
- `internal/cli/logs_test.go:47` (the stale fixture)
- `internal/relayclient/client.go:100` (returns the decode error faithfully), `internal/relayclient/page.go:11`
- [[idea-2026-08-23-cli-tests-never-hit-real-server]] - the mechanism item this confirms. Worth
  re-prioritising off `low` on the strength of a confirmed production instance.
- [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] - the same endpoint, same drift,
  second client
- Precedent, closed: [[bug-2026-05-26-python-sdk-list-pagination-envelope]]

## Notes
The CLI is a second consumer of the CRLF handling tracked in
[[bug-2026-08-25-windows-crlf-log-lines-render-blank]]: it prints `Content` raw, so a Windows
subprocess's `\r` reaches the terminal as-is.

**That question is now decided - see "Where normalisation belongs" in that item.** The summary, so
this item does not re-open it:

- **CRLF to LF is the only shared transform**, and it goes in the agent's `chunkWriter`
  (`internal/agent/runner.go:285`) with a one-byte hold-back for the straddled case. That covers the
  CLI, the SPA, the Python SDK and any future export in one place. It is filed as Part 2 of the
  CRLF item, not as scope here.
- **Do NOT move ANSI stripping or the interior-CR collapse out of `web/`.** The web strips ANSI
  because a DOM has no cursor; a terminal renders it correctly, so stripping it server-side would
  destroy colour output for this very command. The two transforms are not duplicated work - they
  are a rendering decision that belongs to exactly one client.

What remains CLI-local, and is in scope for THIS item's fix: `printTaskLogs` prints
`[<task> <stream>] <content>`, so an interior `\r` in the content returns the terminal cursor over
its own prefix. Decide what that line should do - the agent-side change does not address it,
because an interior CR is legitimate progress-bar output rather than a line terminator.
