---
title: A single log line has no length cap in the SPA, so one newline-less job degrades the tab
type: bug
status: open
created: 2026-08-25
priority: medium
source: 2026-08-25 windows-crlf-log-lines slice - measured by the security lens while checking the new collapseCR for quadratic exposure
---

# A single log line has no length cap in the SPA

## Summary
`MAX_LINES` (`web/src/jobs/logBuffer.ts`) caps how many lines are RETAINED. Nothing caps how long one
line may be: `appendEntries` accumulates into `partials[stream].text` and grows it until a `\n`
arrives. A subprocess that writes megabytes without a newline produces one unbounded string that is
re-scanned on every accepted frame.

## Repro / Symptoms
Submit a job whose step writes a large payload with no newline - `python -c "import sys;
sys.stdout.write('\r'*10**8)"` is the extreme form, and a progress bar that never terminates its
line is the ordinary one. Open that task's log page.

Observed: the operator's tab degrades progressively as the partial grows, because `collapseCR` and
`stripAnsi` both run over the whole partial once per accepted SSE frame. Measured during the CRLF
slice: a 3.3 MB partial cost ~2.5 s per call in one form and ~226 ms in another - the point being
that both are unbounded in the partial's size, which nothing limits.

Note the two principals are different. Whoever can submit a job controls the bytes; the operator who
opens the page pays the cost.

## Context
Pre-existing, not introduced by the CRLF slice - named in that spec's sections 13.1 and 13.3. It came
up because the slice's new index walk was measured against it, which is how the absence of a length
cap became visible as a number rather than an observation.

## Proposal
Cap the per-stream partial at a byte budget in `appendEntries`, dropping from the head and marking
the truncation, so an un-terminated line degrades to a bounded window instead of growing forever. The
existing `DROP_MARKER_TEXT` machinery is the obvious precedent for making the loss visible rather
than silent.

## Acceptance / Done When
- A partial that exceeds the budget is truncated rather than retained in full, and the truncation is
  visible in the rendered output.
- Per-frame work becomes O(cap) rather than O(total bytes since the last newline).
- The ordinary progress-bar case (a partial well under the cap) renders exactly as it does today.

## Related
- `web/src/jobs/logBuffer.ts` - `MAX_LINES`, `appendEntries`, `collapseCR`, `stripAnsi`
- Not covered by [[idea-2026-08-09-task-log-tail-and-paging-improvements]], whose caps and
  virtualization are all line-COUNT based
- Not covered by [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]], which is the server-side
  durable-row bound; this one is client memory and main-thread time
- [[bug-2026-08-25-windows-crlf-log-lines-render-blank]] - the slice that measured it
