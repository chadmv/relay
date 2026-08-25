---
title: Windows CRLF log lines render blank on the job detail page
type: bug
status: open
created: 2026-08-25
priority: high
source: Production use in another environment, 2026-08-25 - a job whose steps shell out to a Python script showed timestamps with no text
---

# Windows CRLF log lines render blank on the job detail page

## Summary
`collapseCR` in `web/src/jobs/logBuffer.ts` keeps only the segment after the final carriage return, to
render `\r`-driven progress bars as one updating line. On CRLF output the trailing `\r` IS the final
carriage return, so the kept segment is the empty string and every such line renders as a timestamp
with no text. The server holds the full content; the loss is entirely at render time in the SPA.

## Repro / Symptoms
Run any task whose subprocess emits CRLF - which on Windows is every non-Go producer, including the
`python` scripts relay is used to drive. Open the task's log on the job detail page.

Observed: a column of timestamps with empty text. Expected: the log lines.

The transform reproduces directly:

```
input:  'hello windows\r\nsecond line\r\n'
output: ["", ""]
```

Measured on one production task (`7e660488`): 260 of 264 entries contained CRLF and rendered blank.
The 4 survivors were relay's own lines (`[sync] START:`, `=== relay step 1/1 ===`), emitted by Go
with a bare `\n`.

## Context
`appendEntries` splits an entry on `\n` and passes each raw line to `collapseCR`
(`web/src/jobs/logBuffer.ts:146`), so the raw line is `"hello windows\r"`. `lastIndexOf('\r')` is the
final index and `slice(i + 1)` returns `""`. `LogRowView` still renders the timestamp column from
`created_at`, which is why the rows are present but empty rather than absent.

Both read paths are affected: the polled backfill and the SSE tail funnel through the same `ingest`
helper in `web/src/jobs/useTaskLogStream.ts:182`.

Two details that are easy to miss when fixing this:

**`collapseCR` has four call sites, not one.** `logBuffer.ts:146` (completed lines), `:175` and `:178`
(the in-flight stdout/stderr partials in `visibleRows`), and `:197` (`finalizePartials`). A chunk
boundary landing between the `\r` and the `\n` puts `"...text\r"` in the partial buffer, so the live
tail flickers blank too; and `finalizePartials` on a task whose final line has no trailing newline
would blank that line as well. Fixing inside `collapseCR` covers all four - fixing at the line-split
site would leave three.

**The existing test is green because of the bug's blind spot.** `logBuffer.test.ts:118` uses
`'frame 1/100\rframe 2/100\rframe 3/100\n'` - carriage returns strictly INTERIOR to the line. No
existing case has a `\r` in the final position, which is the only position that triggers the defect.

## Proposal
Strip at most ONE trailing `\r` inside `collapseCR`, then do the existing progress-bar collapse. Do
not remove the collapse - progress-bar handling is wanted and correct for interior CR runs.

The ordering is the whole correctness argument, and both alternatives are wrong:

- Strip one trailing CR, then collapse: `"a\rb\r"` -> `"a\rb"` -> `"b"` (progress bar preserved), and
  `"hello\r"` -> `"hello"` -> `"hello"` (CRLF fixed). Correct.
- Collapse first, then strip: the collapse has already returned `""`. No change.
- Strip ALL trailing CRs: `"a\rb\r"` is unaffected here, but the single-strip form is the one that
  matches CRLF's definition; prefer the narrower rule.

## Acceptance / Done When
- A CRLF-terminated line renders its text. `appendEntries` on `'hello windows\r\nsecond line\r\n'`
  yields `['hello windows', 'second line']`.
- The interior-CR progress-bar case still collapses: the existing `logBuffer.test.ts:118` case stays
  green unmodified.
- The regression test carries BOTH inputs. Either one alone passes under a wrong ordering, so a
  single-input test does not distinguish the fix from the two broken variants above.
- The partial paths are covered: an entry ending exactly at `"text\r"` with no newline yet renders
  `text` in `visibleRows`, and `finalizePartials` flushes it as `text`.

## Related
- `web/src/jobs/logBuffer.ts:93` (`collapseCR`), `:146`, `:175`, `:178`, `:197` (its four call sites)
- `web/src/jobs/logBuffer.test.ts:118` (the interior-CR test that cannot see this)
- `web/src/jobs/useTaskLogStream.ts:182` (the single `ingest` both read paths share)
- Adjacent but distinct: [[idea-2026-08-09-task-log-tail-and-paging-improvements]] names
  `logBuffer.ts` as a source pointer, but covers tail paging, virtualization and export. Its Notes
  list what else that spec deliberately deferred (ANSI colour rendering, in-log search); CRLF is not
  among them.

## Notes
Relay is developed on Windows, so this is the default rendering for the project's own primary
platform. It was not caught earlier because relay's own log lines come from Go with a bare `\n` and
therefore render fine - the blank rows are exactly the subprocess output an operator opens the page
to read.
