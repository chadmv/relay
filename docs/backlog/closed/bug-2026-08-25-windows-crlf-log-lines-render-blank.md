---
title: Windows CRLF log lines render blank on the job detail page
type: bug
status: closed
closed: 2026-08-25
resolution: fixed
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
Two parts. **Part 1 is the fix and ships alone; Part 2 does not block it and Part 2 does not
replace it.** See "Where normalisation belongs" below for why both are needed.

### Part 1 - the web fix (required, sufficient on its own)
Strip at most ONE trailing `\r` inside `collapseCR`, then do the existing progress-bar collapse. Do
not remove the collapse - progress-bar handling is wanted and correct for interior CR runs.

The ordering is the whole correctness argument, and both alternatives are wrong:

- Strip one trailing CR, then collapse: `"a\rb\r"` -> `"a\rb"` -> `"b"` (progress bar preserved), and
  `"hello\r"` -> `"hello"` -> `"hello"` (CRLF fixed). Correct.
- Collapse first, then strip: the collapse has already returned `""`. No change.
- Strip ALL trailing CRs: `"a\rb\r"` is unaffected here, but the single-strip form is the one that
  matches CRLF's definition; prefer the narrower rule.

### Part 2 - CRLF to LF at the agent (separately shippable, covers the other three clients)
Normalise CRLF to LF in the agent's `chunkWriter` (`internal/agent/runner.go:285`), holding back a
trailing `\r` and prepending it to the next `Write`, flushed on close.

**Do not skip the hold-back.** An entry is not a line: `chunkWriter` copies whatever `os/exec` hands
it, so a CRLF can straddle a chunk boundary - chunk N ends `"\r"`, chunk N+1 starts `"\n"`. A
stateless `ReplaceAll` cannot see that pair. The hold-back is O(1) state and makes every emitted
chunk straddle-free, after which a plain replace is exact. Watch the existing `len(p) == 0` guard:
a `Write` whose entire payload is `"\r"` must still return `len(p)` so `io.Copy` does not stall.

Two costs, both judged acceptable, neither a side effect to discover later:

- It does not fix rows already stored, nor output from an agent that has not been upgraded. **That
  is exactly why Part 1 is not optional** - the web client must keep handling CRLF on the wire
  forever, because it will keep receiving it from history and from un-upgraded agents.
- Stored bytes stop being a byte-exact copy of the subprocess output, which forecloses a
  byte-exact log export ([[idea-2026-08-09-task-log-tail-and-paging-improvements]] proposes one).
  CRLF-vs-LF is not information anyone will want back, so take it - but take it deliberately.

## Where normalisation belongs
Recorded 2026-08-25, after `relay logs` was found to need the same treatment
([[bug-2026-08-25-relay-logs-prints-nothing-envelope-drift]]) and the obvious reaction was to move
all of this server-side and do it once. Read this before moving any of it.

**Only ONE of the three transforms in `logBuffer.ts` is shared work.**

| transform | who wants it | shared? |
|---|---|---|
| CRLF to LF | web, CLI, Python SDK, any export | yes - Part 2 |
| interior-CR collapse (progress bars) | web only | no |
| ANSI strip | web only, and the CLI wants the OPPOSITE | no |

**Do not unify the last two, and in particular never move ANSI stripping server-side.** The web
strips ANSI because a DOM has no cursor and no colour state, so the raw bytes would render as
visible corruption. A terminal renders them correctly, which is the entire point of a program
emitting them. Stripping at or before storage permanently destroys colour output for `relay logs`
and for any future export. The interior-CR collapse is lossy in the same way - a CLI user piping to
a file wants the frames.

**"Once" has to mean a pipeline stage, not a shared function.** Four clients consume this data in
three languages - `internal/cli`, `web/`, the Python SDK, and `internal/mcp` (which passes the
envelope through untouched and is structurally immune to the whole class). No importable helper
spans them, so putting this in `internal/relayclient` would cover one of the four.

**Why the agent rather than the server.** The straddle constraint above eliminates the alternatives:

- *Server ingest* (`internal/worker/handler.go:1671`) is otherwise the attractive one - a single
  edit covers both the stored row and the SSE publish, since both derive from the same bytes, and it
  works for already-deployed agents. But the straddle needs per-`(task, stream)` partial state the
  server does not keep, on a recv-goroutine path whose own comments justify staying at one
  statement; and the fence can reject the chunk, so it would transform bytes it then discards.
  **This is the fallback if covering already-deployed agents matters more than the straddle case.**
- *Server read path* is worse, and is two sites rather than one: the REST handler reads `l.Content`
  from the row (`internal/api/tasks.go:118`) while the SSE publish sends `chunk.Content` from the
  wire (`internal/worker/handler.go`, the publish after `AppendTaskLog`). The REST side has the full
  ordered set and could reassemble perfectly; the SSE side still sees one chunk at a time. It pays
  on every read and is still not uniform.

The agent is the only site holding the contiguous byte stream, so it is the only one that can be
both complete and O(1).

## Acceptance / Done When
### Part 1 (web)
- A CRLF-terminated line renders its text. `appendEntries` on `'hello windows\r\nsecond line\r\n'`
  yields `['hello windows', 'second line']`.
- The interior-CR progress-bar case still collapses: the existing `logBuffer.test.ts:118` case stays
  green unmodified.
- The regression test carries BOTH inputs. Either one alone passes under a wrong ordering, so a
  single-input test does not distinguish the fix from the two broken variants above.
- The partial paths are covered: an entry ending exactly at `"text\r"` with no newline yet renders
  `text` in `visibleRows`, and `finalizePartials` flushes it as `text`.

### Part 2 (agent), if taken
- A subprocess emitting `\r\n` produces chunks containing no `\r\n`, end to end against a real
  agent.
- **The test feeds a STRADDLED CRLF** - one chunk ending in `\r`, the next beginning with `\n`.
  Without that input a stateless `ReplaceAll` and the stateful hold-back are indistinguishable, and
  the test passes on the version that still drops lines. This is the discriminating input; the
  ordinary same-chunk `\r\n` case does not stand in for it.
- A `Write` whose payload is exactly `"\r"` returns `len(p)` and does not stall `io.Copy`.
- A trailing `\r` at end of stream is flushed rather than swallowed on close.
- Part 1 still passes unchanged afterwards. Part 2 must not be treated as making the web fix
  redundant - history and un-upgraded agents keep sending CRLF.

## Related
- `web/src/jobs/logBuffer.ts:93` (`collapseCR`), `:146`, `:175`, `:178`, `:197` (its four call sites)
- `web/src/jobs/logBuffer.test.ts:118` (the interior-CR test that cannot see this)
- `web/src/jobs/useTaskLogStream.ts:182` (the single `ingest` both read paths share)
- `internal/agent/runner.go:285` (`chunkWriter.Write`) - the Part 2 site
- `internal/worker/handler.go:1671` (ingest, the documented fallback site);
  `internal/api/tasks.go:118` (the REST read site, rejected)
- [[bug-2026-08-25-relay-logs-prints-nothing-envelope-drift]] - the second client that needs the
  same CRLF treatment, and the reason Part 2 exists
- Adjacent but distinct: [[idea-2026-08-09-task-log-tail-and-paging-improvements]] names
  `logBuffer.ts` as a source pointer, but covers tail paging, virtualization and export. Its Notes
  list what else that spec deliberately deferred (ANSI colour rendering, in-log search); CRLF is not
  among them.

## Notes
Relay is developed on Windows, so this is the default rendering for the project's own primary
platform. It was not caught earlier because relay's own log lines come from Go with a bare `\n` and
therefore render fine - the blank rows are exactly the subprocess output an operator opens the page
to read.

## Resolution
Both parts shipped, in one slice, on branch `claude/windows-crlf-log-blank-c488bf`.

**Part 1 shipped a different fix than this item proposed.** The spec refuted the strip-ONE design
above: `"x\r\r"` is what the Windows C runtime writes for `print("done", end="\r")` followed by
`print()` - the literal CR passes through untranslated and the LF becomes CRLF - so a single strip
still leaves that row blank, for the same reason the bug exists. `collapseCR` strips EVERY trailing
carriage return, then does the pre-existing interior-CR collapse. Strip-all also makes Part 2
provably render-invisible on every input, which is what allowed both parts to ship together.

**Part 2's stated acceptance criterion was also wrong and was replaced.** "Chunks contain no `\r\n`"
is false by design: `"x\r\r\n"` correctly emits `"x\r\n"`, a CRLF at byte positions that did not
have one. The guarantee is an equality on the CONCATENATION of emitted payloads, verified over
27,994 exhaustive `(string, split)` combinations and several hundred thousand randomised cases with
zero mismatches.

**A design gap this item glossed:** `chunkWriter` had no close path and `os/exec` never closes a
caller-supplied `Stdout`, so "flushed on close" named a hook that does not exist. The writers are
constructed per STEP, so `flush()` is called explicitly for both writers inside the per-step loop
after `cmd.Wait()`.

Verified beyond the local Windows gate: the `//go:build !windows` cancel tests and `go test -race`
were both run green in a `golang:1.26` Linux container, and the integration lane passed 626 tests
against real Postgres and p4d containers.

Commits: `a97568d` (spec), `840de0d` (plan), `ff8dac2`/`9d60a45`/`5f96f95` (web),
`fe68f4e`/`b7acca9`/`c7931d4`/`acd588b` (agent), `faaa506`/`769a51a`/`2c1e300` (prose).
