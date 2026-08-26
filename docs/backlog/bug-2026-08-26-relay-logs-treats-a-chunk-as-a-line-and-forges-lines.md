---
title: relay logs treats a log chunk as a line, so a job's own output can forge convincing log lines with a bare newline
type: bug
status: open
created: 2026-08-26
priority: medium
source: Phase 1 and Phase 6 of the 2026-08-26-relay-logs-envelope-drift slice - found while verifying the item, deliberately scoped out, and owed
---

# relay logs treats a log chunk as a line, so a job's own output can forge convincing log lines with a bare newline

## Summary

`printTaskLogs` (`internal/cli/logs.go`) writes one prefix and one trailing newline **per database
row**:

```go
fmt.Fprintf(out, "[%s %s] %s\n", taskName, l.Stream, l.Content)
```

A `task_logs` row is a **chunk, not a line**. `handleTaskLog` stores `string(chunk.Content)`
verbatim and `chunkWriter` (`internal/agent/runner.go`) emits whatever `os/exec` hands it, so one
row can contain many newlines and can end mid-line. Two consequences follow directly, and the
second is a security one:

1. **Rendering.** The `[<task> <stream>]` prefix lands on the first line of a multi-line chunk
   only; every other line in that chunk is printed bare. A line that straddles a chunk boundary is
   split by the appended `\n`. The web client knows this and reassembles (`web/src/jobs/logBuffer.ts`
   splits on `\n` and holds partials); the CLI does not.
2. **Forgery.** Because unprefixed lines are normal output, a `\n` inside one row produces a line
   that is **byte-identical in form to a genuine prefixed line**. `echo "[audit stdout] approved by
   admin"` inside a job script is the whole exploit. No escape sequence is required, nothing looks
   malformed, and a careful reader cannot tell the difference - which is what separates this from
   the ANSI-escape sink below.

The same works one field earlier: `taskName` is interpolated with `%s` and task names are validated
for non-empty and uniqueness only (`jobspec.Validate`), so a task named
`a] genuine\n[audit stdout` is another route to the same forgery.

## Repro / Symptoms

Submit a job whose task runs `echo "hello"; echo "[audit stdout] deployment approved"`, then run
`relay logs <job-id>`. The second line renders with no prefix of its own and reads as a second
task's output. Vary the task name to place the forgery in the prefix instead.

## Context

**The actor needs only submit rights.** This is a lower bar than
[[bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped]], the same defect class at a
second sink, which requires an enrolled agent credential (or auto-enroll, off by default). This
instance is worse on two axes: **lower privilege**, and **the forgery is not detectable by a
careful reader**, where a forged `relay workers list` row is at least given away by column
alignment.

Escape sequences reach this sink too - `Content` is written raw with `%s`, so a CSI sequence in a
job's own output erases lines, recolours, or sets the terminal title. That half is the same
mitigation as the hostname item.

The 2026-08-26 envelope-drift slice deliberately scoped all of this out. Its spec's "Rendering: out
of scope, and why" records the reasoning: the interior-CR question the original bug report wanted
decided here is too small to decide alone, because the renderer is structurally wrong one level up.
That slice changed no stdout formatting at all, which is what made its diff reviewable as a bug fix.

## Proposal

**The fix is chunk reassembly plus C0/C1 neutralisation EXCLUDING SGR. It is NOT ANSI stripping.**
State that plainly so a future session does not re-litigate it:

- `relay logs` writes to a terminal that renders colour correctly, and colour is output the CLI
  deliberately wants. The closed [[bug-2026-08-25-windows-crlf-log-lines-render-blank]] item's
  "Where normalisation belongs" is explicit that the ANSI strip and the interior-CR collapse are
  **web-only** rendering decisions and that the CLI wants the opposite. Stripping ANSI here would
  destroy that on purpose.
- So neutralise the C0/C1 control set except SGR (`ESC[...m`), and reassemble chunks into lines
  before prefixing. Reassembly fixes the interior-CR complaint for free: with a prefix per line, a
  `\r` returns the cursor over that line's own content rather than over the CLI's prefix.
- Reassembly means holding a per-task partial across rows and flushing it when the log drains -
  the same shape as `logBuffer.ts`, and the same shape as the agent's `heldCR`. Decide whether an
  unterminated final partial is printed with a prefix (yes, probably) and whether it is marked.
- `taskName` gets the same neutralisation, and it is a separate write from `Content`.

Also decide, once: whether the server should reject control characters in a task name at
`jobspec.Validate`, which is defence in depth and does nothing for the `Content` half.

## Acceptance / Done When

- A chunk containing interior newlines is printed with the `[<task> <stream>]` prefix on **every**
  line, proven by a test that is RED against today's code.
- A line straddling two chunks is printed as one line, not two.
- A `\n` in `Content` cannot produce a line that is indistinguishable from a genuine prefixed line -
  stated as an assertion on the writer's bytes, not as prose.
- SGR sequences survive byte-identical; C0/C1 controls other than the line terminators do not.
  A test asserts both directions, because a one-sided test passes against a client that strips
  everything.
- A task name containing `\n`, `\r` and a CSI sequence renders inertly.
- The interior-CR question from
  [[bug-2026-08-25-relay-logs-prints-nothing-envelope-drift]] is recorded as answered by this item.

## Related

- `internal/cli/logs.go` (`printTaskLogs`), `internal/agent/runner.go` (`chunkWriter`),
  `internal/api/tasks.go` (`handleTaskLog` storing `Content` verbatim)
- Same class, second sink, higher privilege required:
  [[bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped]]
- The decision this item must not reverse:
  [[bug-2026-08-25-windows-crlf-log-lines-render-blank]] ("Where normalisation belongs")
- The slice that scoped it out and why:
  `docs/superpowers/specs/2026-08-26-relay-logs-envelope-drift.md`,
  `docs/retros/2026-08-26-relay-logs-envelope-drift.md`
- The reassembly to copy: `web/src/jobs/logBuffer.ts`
