---
title: A server-side task-log export that streams a full log as text
type: idea
status: open
created: 2026-09-02
priority: low
source: carved out of idea-2026-08-09-task-log-tail-and-paging-improvements by the 2026-09-01 tail-paging spec (lane D)
---

# A server-side task-log export

## Summary
There is no way to get a whole task log out of the browser. A server-side endpoint streaming a task's
full log as text would avoid pulling it through the SPA's memory. Log content is raw subprocess output
and can contain secrets a job's own script echoed, so an export is a more consequential surface than
the paged view: it must not be more permissive than the existing read path.

## Context
A byte-exact export is foreclosed. The agent normalises CR LF to LF before a chunk is ever sent
(docs/superpowers/specs/2026-08-25-windows-crlf-log-lines.md, Part 2), so stored bytes are no longer
a byte-exact copy of the subprocess output. CRLF-versus-LF is not information anyone will want back
and the trade was taken deliberately, but do not write this piece against a byte-exactness guarantee
that no longer holds.

## Related
- [[idea-2026-08-09-task-log-tail-and-paging-improvements]] (closed)
- [[idea-2026-08-25-readme-task-logs-no-longer-byte-exact]]
- internal/api/tasks.go (handleGetTaskLogs)
