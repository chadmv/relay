---
title: Task-log history has no tail fetch, no virtualization, and no export
type: idea
status: open
created: 2026-08-09
priority: low
source: Spec phase of the task-log view iteration (2026-08-09)
---

# Task-log history has no tail fetch, no virtualization, and no export

## Summary
Three related gaps in reading a long task log, all accepted as out of scope when the task-log view
shipped. Paging is forward-only from `since_seq`, so there is no cheap way to fetch the **tail** of a
long history; the log view renders every retained row rather than virtualizing; and there is no way
to get a full log out of the browser.

## Context
Filed from the 2026-08-09 task-log view iteration, which shipped live tailing plus a gapless
subscribe-then-backfill join. The view caps retained lines and shows an explicit truncation notice
rather than pretending completeness, so none of this is silently broken - it is bounded and
disclosed. But on a job that produced a very large log, the only way to reach the end is to page
forward through all of it.

Note why the tail is not simply an offset query: `seq` is `task_logs.id`, a table-wide `BIGSERIAL`,
so it is ordered but **not contiguous** - values are consumed by every other task logging
concurrently. That means neither `total` nor arithmetic on `seq` yields an offset, and "give me the
last N" cannot be derived client-side from what the API exposes today.

## Proposal
Three independent pieces, in rough order of value:

1. **Descending or bounded-tail paging.** Either a `?before_seq=` parameter paging backwards, or an
   `?order=desc` on the existing endpoint. Backwards paging is what a log reader actually wants -
   open at the end, scroll up into history. This is the piece that unblocks the other two being
   worth doing.
2. **Row virtualization** in the log view, so the retained-line cap can rise substantially without
   the DOM cost. Only worth doing after (1), since without a tail fetch a higher cap does not help
   reach the end of a long log.
3. **Log export/download.** A server-side endpoint streaming a task's full log as text would avoid
   pulling it through the SPA's memory entirely. Note the security consideration that log content is
   raw subprocess output and can contain secrets a job's own script echoed, so an export is a more
   consequential surface than the paged view - it should not be more permissive than the existing
   read path.

   **A byte-exact export is foreclosed as of 2026-08-25.** The agent normalises `\r\n` to `\n`
   before a chunk is ever sent (`docs/superpowers/specs/2026-08-25-windows-crlf-log-lines.md`,
   Part 2), so stored bytes are no longer a byte-exact copy of the subprocess output. CRLF-vs-LF is
   not information anyone will want back and the trade was taken deliberately - but do not write
   this piece against a byte-exactness guarantee that no longer holds.

## Acceptance / Done When
Treat each piece as separately shippable. For (1): a client can fetch the most recent N log entries
for a task without paging through the whole history, with a test covering the non-contiguous-`seq`
case. For (2): the retained-line cap rises with no rendering regression. For (3): a full log can be
retrieved as a single stream under the same authorization as reading it in the UI.

## Related
- Accepted as out of scope in `docs/superpowers/specs/2026-08-09-task-log-view-sse-tailing.md`
- Source: `internal/api/tasks.go` (the paged logs endpoint), `web/src/jobs/LogView.tsx` (the
  retained-line cap and truncation notice), `web/src/jobs/logBuffer.ts`
- Adjacent: [[idea-2026-08-09-sse-revoked-token-keeps-streaming]] and the general point that log
  content is more sensitive than status payloads

## Notes
Also noted during the same spec work and deliberately not filed as separate items: ANSI colour
rendering (the client currently strips ANSI rather than interpreting it) and in-log search. Both are
presentation polish that only becomes worthwhile once long logs are actually navigable, so they
belong behind (1) if they are wanted at all.
