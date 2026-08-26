---
title: README describes task_logs as captured stdout/stderr without qualifying that CRLF is normalised
type: idea
status: open
created: 2026-08-25
priority: low
source: 2026-08-25 windows-crlf-log-lines slice - the correctness lens flagged it as a gap rather than a defect, and the spec had deliberately scoped README out
---

# README describes task_logs as captured stdout/stderr and no longer qualifies that

## Summary
As of the CRLF slice the agent normalises `\r\n` to `\n` before a chunk is ever sent, so the bytes in
`task_logs` are no longer a byte-exact copy of what the subprocess wrote. README's `task_logs` line
still describes the table as captured stdout/stderr with no qualifier. Nothing there is FALSE - no
passage claims byte-exactness - but "captured" reads as verbatim to someone deciding whether an
export can be.

## Context
The slice's spec deliberately scoped README out (section 8.5), on the accurate grounds that there was
no wrong claim to fix, and the backend engineer respected that boundary rather than drifting. The
correctness lens then flagged the same passage as a GAP rather than a defect - a distinction worth
preserving, because it is the reason this is an idea at low priority and not a bug.

The consumer most likely to care is already covered: the export piece of
[[idea-2026-08-09-task-log-tail-and-paging-improvements]] carries an explicit annotation that a
byte-exact export is foreclosed.

## Proposal
One qualifying clause on README's `task_logs` line, saying CRLF is normalised to LF at the agent and
naming the closed item as the reason. Resist expanding it - the full argument already lives in the
spec and in the export item.

## Acceptance / Done When
- A reader of README's schema section learns that stored log bytes are normalised, without having to
  find the backlog.
- No other README passage is touched.

## Related
- `README.md` - the `task_logs` schema line
- `internal/agent/runner.go` - `chunkWriter.Write`, where the normalisation happens
- [[bug-2026-08-25-windows-crlf-log-lines-render-blank]] - the slice that changed the guarantee
- [[idea-2026-08-09-task-log-tail-and-paging-improvements]] - already annotated; the export consumer
