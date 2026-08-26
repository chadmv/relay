# Windows CRLF log lines: strip every trailing CR at render, and stop emitting CRLF at the source

- **Date:** 2026-08-25
- **Type:** two-surface slice. Part 1 is `web/src/jobs/logBuffer.ts` (TypeScript, ~4 lines plus a doc
  comment). Part 2 is `internal/agent/runner.go` (Go, two struct fields, one method, one call site).
  **No migration, no proto change, no SQL, no server change, no CLI change.**
- **Closes:** `docs/backlog/bug-2026-08-25-windows-crlf-log-lines-render-blank.md`
- **Verified against:** worktree `windows-crlf-log-blank-c488bf`, branch
  `claude/windows-crlf-log-blank-c488bf`, tree clean at `03f45d4`
- **Phase:** 1 (design). Phase 2 writes the plan.
- **Gate mode:** autonomous. Every fork a human would have been asked about is decided here with the
  reasoning written down; section 12 is the ledger.

Every claim about current code carries a `file:line` or a symbol. Claims that could not be established
from the tree are labelled as assumptions.

**Read section 3 before section 4.** Two of the item's own claims do not survive. Its Part 1 design
("strip at most ONE trailing `\r`") still leaves blank rows on the single most common Windows shape
there is, and its Part 2 acceptance criterion ("chunks containing no `\r\n`") is not a property the
design can deliver and must not be tested for. Both errors have the same root: `\r\r\n`. Fixing Part 1
to strip **all** trailing CRs resolves both, and is the reason the two parts belong in one slice.

---

## 1. Problem, restated after verification

`collapseCR` (`web/src/jobs/logBuffer.ts:93-96`) keeps only the segment after the final carriage
return in a line:

```ts
function collapseCR(s: string): string {
  const i = s.lastIndexOf('\r')
  return i === -1 ? s : s.slice(i + 1)
}
```

`appendEntries` splits an entry on `\n` and passes each raw line to it (`:144-146`), so on CRLF output
the raw line is `"hello windows\r"`, the final `\r` is the last character, and `slice(i + 1)` is `""`.
`LogRowView` still renders the timestamp column from `created_at`, so the row is present and empty
rather than absent.

**Measured production evidence.** On one production task (`7e660488`), **260 of 264** log entries
contained CRLF and rendered blank. The 4 survivors were relay's own lines (`[sync] START:`,
`=== relay step 1/1 ===`), emitted by Go with a bare `\n` - `sendStepMarker` builds its line with
`+ "\n"` (`internal/agent/runner.go:248`). Every non-Go producer on Windows hits the defect, which
includes the `python` scripts relay exists to drive. Relay is developed on Windows, so this is the
default rendering on the project's own primary platform, and the blank rows are exactly the subprocess
output an operator opens the page to read.

The server holds the full content. The loss is entirely at render time.

---

## 2. What the code actually does, verified at HEAD

### 2.1 The four call sites, and why the fix goes inside `collapseCR`

| Site | What it collapses | What CRLF does to it today |
|---|---|---|
| `logBuffer.ts:146` | a completed line, everything before a `\n` | the whole line renders blank |
| `:175` | the in-flight **stdout** partial in `visibleRows` | a chunk boundary between the `\r` and the `\n` puts `"text\r"` in the partial, so the live tail flickers blank and then fills in |
| `:178` | the in-flight **stderr** partial | same |
| `:197` | `finalizePartials`, the dangling tail of a task with no final newline | that line renders blank permanently |

All four take the same string-to-string function, so fixing inside `collapseCR` covers four sites with
one edit. Fixing at the line-split site (`:144`) would leave three. **Confirmed** against the file.

**A fifth candidate site is rejected on evidence.** Normalising `e.content` at `:140`, before the
split, does not work: only the final `\r` of the whole entry is trailing there, so `"a\r\nb\r\n"` would
keep `"a\r"` as a line. And it would not repair the partials. `collapseCR` is the only site that sees
each rendering unit whole.

### 2.2 `stripAnsi` runs before the split, and it MATTERS - CONFIRMED, in the fix's favour

`appendEntries:139-140` strips ANSI on the entry, then splits (`:142`). The comment ("Strip before
splitting: an escape sequence never contains a newline") is correct.

The consequence the item does not state: **an escape sequence cannot hide a `\r` from the fix, and the
strip can promote a non-trailing `\r` into a trailing one.** Take the byte sequence
`"text" + CR + ESC + "[K" + LF`. `stripAnsi` removes the erase-line sequence first, leaving
`"text" + CR + LF`, so the raw line handed to `collapseCR` is `"text\r"` and the fix sees a CR that the
raw bytes had buried behind an escape. This is a real shape - erase-line after a carriage return is
exactly what a progress bar emits, and `ANSI_RE` covers it (`logBuffer.test.ts:132-136`).

So the ordering is load-bearing in one more direction than the item claims, and it is a second reason
the fix belongs inside `collapseCR` rather than anywhere upstream of `stripAnsi`.

### 2.3 The existing test is green because of the bug's blind spot - CONFIRMED

`logBuffer.test.ts:118-122` feeds `'frame 1/100\rframe 2/100\rframe 3/100\n'` - carriage returns
strictly **interior** to the line. **No existing test in the file has a `\r` in the final position**,
which is the only position that triggers the defect. Confirmed by reading the whole test file: the
other CR-adjacent cases are the two ANSI tests (`:124`, `:132`), which carry no `\r` at all.

### 2.4 `collapseCR` is the only `\r` handling anywhere in `web/src` - CONFIRMED

A search for `\r` across `web/src` returns **zero** hits outside `logBuffer.ts`. There is no second
renderer, no export path, and no other consumer to keep in step.

### 2.5 `chunkWriter` has exactly two instances and no flush path at all - CONFIRMED, and it is a real gap

`chunkWriter` is constructed at exactly two sites, both inline in the per-step loop:

```go
cmd.Stdout = &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDOUT, stepIndex: step, stepTotal: stepTotal}
cmd.Stderr = &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDERR, stepIndex: step, stepTotal: stepTotal}
```

(`internal/agent/runner.go:202-203`). A search for `chunkWriter` across `*.go` finds no other
construction - the remaining hits are the type and method (`:270-309`), three comments (`:261`, `:353`,
`internal/worker/handler.go:173`, `internal/relayclient/client.go:144`) and two test comments.

Three facts follow, and the item glosses all three:

1. **There is no close, flush, or Close hook today, and `os/exec` will never call one.** `exec` closes
   only the pipes it created; it does not call `Close` on a caller-supplied `io.Writer` for
   `Stdout`/`Stderr`. So the flush must be an explicit call the runner makes, and **naming the method
   `Close()` would be actively misleading** - a reader would assume `exec` calls it. Section 6.3 names
   the method `flush()` and names its call site.
2. **The writers are per STEP, not per task.** They are created fresh inside `for i, cl := range
   task.Commands` (`:179`) and become garbage at the end of each iteration. A held byte that is not
   flushed before the next iteration is silently lost, and the next step's `sendStepMarker` (`:187`)
   would be enqueued ahead of it if the flush were deferred to the end of `Run`.
3. **Stdout and stderr are distinct pointers**, so `os/exec` does not take its `c.Stdout == c.Stderr`
   shared-descriptor path and each writer is driven by exactly one copy goroutine. Hold-back state is
   per-writer and is written by one goroutine. Section 6.4 handles the one place a second goroutine
   touches it.

### 2.6 The abort path, and the FIFO ordering argument the flush must not break

`chunkWriter.Write` (`:285-309`) sends through `r.sendOrAbort` (`:355-370`), which is bounded by
`r.ctx.Done()`, `r.forcedCh` and `r.cancelledCh` and returns `false` when it abandons; `Write` then
returns `(0, errForcedAbort)` so `io.Copy` stops and `cmd.Wait()` returns instead of waiting out
`WaitDelay`.

`r.send` (`:342-348`) is **not** equivalently bounded: it selects only on `r.sendCh` and `r.ctx.Done()`,
and `r.ctx` is the **agent** context, not the run context (`:24`, `:50`). On a per-task cancel with a
wedged `sendCh`, `r.send` parks until agent shutdown. That is exactly why `sendFinalStatus` has a
separate bounded branch for `r.cancelled` (`:331-338`) and why `sendInventory` mirrors it (`:413-419`).

**`internal/worker/handler.go:173-178` depends on the enqueue order**, in a comment that bounds the
`AppendTaskLog` trailing window:

> chunkWriter's writes are enqueued before sendFinalStatus and sendCh is FIFO, so a chunk ordered
> BEFORE the terminal status cannot outlive it.

A flush enqueued after `sendFinalStatus` would make that sentence false and would push a one-byte chunk
into the trailing-window carve-out. Section 6.3 places the call so the sentence stays true, and section
9 makes the ordering a test.

---

## 3. Discrepancies between the item and HEAD

Most important first.

**R1. REFUTED, and it changes the design: "strip at most ONE trailing `\r`" is not enough, and
"prefer the narrower rule" is wrong.** The item works `"a\rb\r"` and `"hello\r"` and concludes that
strip-all is merely equivalent. It is not equivalent. Take `"x\r\r"`:

| input line | strip-one, then collapse | strip-all, then collapse | what a terminal shows |
|---|---|---|---|
| `"hello windows\r"` | `"hello windows"` | `"hello windows"` | `hello windows` |
| `"a\rb\r"` | `"b"` | `"b"` | `b` (approximated; see 4.2) |
| **`"x\r\r"`** | **`""` - still blank** | **`"x"`** | **`x`** |
| `"\r"` | `""` | `""` | empty line |
| `""` | `""` | `""` | empty line |
| `"a\rb\r\r"` | `""` - still blank | `"b"` | `b` |

`"x\r\r"` is not exotic. It is what the Windows C runtime produces for the most ordinary
progress-bar-then-newline sequence there is:

```python
print("done", end="\r")   # writes  "done\r"   - a literal \r is NOT translated
print()                    # writes  "\n" -> "\r\n" by text-mode translation
```

The stream is `"done\r\r\n"`; the line, after splitting on `\n`, is `"done\r\r"`. **Under the item's
single-strip design that row is still blank.** Any producer that ends a `\r`-updated line with a normal
newline lands here. The production evidence is a Python script, so this is the same population that
produced the 260 blank rows.

There is no case where strip-one beats strip-all: stripping trailing CRs can only remove characters
that have no successor within the rendering unit, so nothing they could have overwritten exists (4.2).
**Decision D1 takes strip-all.**

**R2. REFUTED as stated: the item's claim that its two named inputs are the discriminating set.** The
input `"\r"` alone does not discriminate anything - `collapseCR("\r")` is already `""`, so
collapse-then-strip and strip-then-collapse agree on it. The genuinely discriminating inputs are
`'hello windows\r\n...'` (kills collapse-then-strip), the interior-CR progress bar (kills any fix that
drops the collapse), and `"x\r\r\n"` (kills strip-one). **All three must be in the regression test.**
The item required the first two; the third is new here.

**R3. NEW, and it is the strongest reason to take both parts in one slice: with strip-ONE the two
parts interact; with strip-ALL they do not.** Work `"x\r\r\n"`:

| configuration | bytes reaching the web | rendered |
|---|---|---|
| neither part | `"x\r\r\n"` -> line `"x\r\r"` | `""` blank |
| Part 2 only | `"x\r\n"` -> line `"x\r"` | `""` **still blank** |
| Part 1 as strip-ONE, no Part 2 | line `"x\r\r"` | `""` **still blank** |
| Part 1 as strip-ONE, plus Part 2 | line `"x\r"` | `"x"` |
| **Part 1 as strip-ALL, no Part 2** | line `"x\r\r"` | `"x"` |
| **Part 1 as strip-ALL, plus Part 2** | line `"x\r"` | `"x"` |

Two conclusions. First, **Part 2 alone does not fix the bug** - not even for a freshly upgraded agent -
because replacing each `\r\n` in `"x\r\r\n"` yields `"x\r\n"`, whose line still ends in a CR. Second,
under strip-one the SPA's output would depend on whether the agent had been upgraded, which is the
worst possible property for a rendering fix. **Under strip-all, Part 2 is render-invisible to the web
on every input** (proved in 4.4), which is what lets Part 2 be judged on its own merits for the other
clients instead of as a second half of the render fix.

**R4. CONFIRMED, and it is a design gap the item glosses: `chunkWriter` has no close path and one has
to be created, per step, by hand.** Section 2.5. The item says "flushed on close" as though a close
hook existed. It does not, `exec` will not create one, and the writers are per-step so the flush call
site is inside the loop, not at the end of `Run`. Section 6.3.

**R5. REFUTED, and this one would have produced a false test: "every emitted chunk is straddle-free"
and the acceptance criterion "a subprocess emitting `\r\n` produces chunks containing no `\r\n`" are
both properties the design does not have and must not have.** Two independent reasons:

1. Holding back one trailing `\r` does not stop an emitted chunk from *ending* in `\r`. A payload of
   `"\r\r"` emits `"\r"` after holding one back. That is correct - the emitted CR's successor is known
   to be another CR, not a LF - but it means per-chunk straddle-freedom is false.
2. **The transform is defined on the ORIGINAL byte positions, so it can create a `\r\n` at new
   positions.** `"x\r\r\n"` contains exactly one CRLF (the last two bytes); replacing it gives
   `"x\r\n"`, which contains a CRLF that was not there before. A second pass would be a *different*
   transform (`\r+\n -> \n`), which section 6.2 rejects on purpose.

The property the design actually has, and the one to test, is about the **concatenation**:

> For one `(step, stream)` writer, the concatenation of every payload it emits equals the subprocess's
> bytes with each `\r\n` **of the original** replaced by `\n`.

That is exact, directly assertable by joining the chunks a test drains from `sendCh`, and it is what
section 9.2 asserts. A test written to the item's wording would fail on legitimate input; a test that
asserted it only on input without `\r\r\n` would pass vacuously and pin the wrong contract.

**R6. CONFIRMED with one addition: a `Write` returning `(len(p), nil)` without enqueueing anything does
NOT violate `io.Writer` and does not violate the doc comment's invariant - but it does make the doc
comment incomplete.** `io.Writer` requires `n < len(p)` **iff** a non-nil error is returned; the bytes
are consumed into the writer's own state, which is exactly what `bufio.Writer` does. The `len(p) == 0`
guard's stated invariant is "never emit an empty chunk" (`:287`) and the hold-back emits **no** chunk
rather than an empty one, so the invariant strengthens rather than breaks. What must change is the type
comment at `:270-277`, which says "On a successful enqueue Write returns `(len(p), nil)`" and now has a
third case. Wrong prose about correct code is this project's dominant defect class; section 8 lists it
as a required site.

**R7. CONFIRMED: `internal/mcp` is structurally immune.** `callGetTaskLogs`
(`internal/mcp/task_logs.go:27-51`) issues `GET /v1/tasks/{id}/logs`, decodes into `map[string]any`,
and returns it; `server.go:125-131` marshals that map straight into a `TextContent`. It never reads
`content`, never splits on `\n`, and never renders. It cannot exhibit the class. Confirmed.

**R8. CONFIRMED, and Part 1 must remain forever - for THREE reasons, not the item's one.** The item
gives history and un-upgraded agents. Two more, both from this analysis: the `\r\r\n` residue Part 2
provably cannot remove (R3/R5), and the fact that Part 2's hold-back can delay a `\r` indefinitely
(4.5) - which is only safe because Part 1 renders a trailing `\r` and its absence identically.

**R9. NEW, security lens: implement the trailing strip as an index walk, NOT as `/\r+$/`.** JavaScript's
regex engine backtracks: `/\r+$/` against `n` carriage returns followed by any non-CR character is
O(n^2), because every start position retries the whole run. Nothing in `logBuffer.ts` caps line
**length** - `MAX_LINES` (`:10`) caps line **count**, and the partial buffer at `:140` grows unbounded
until a `\n` arrives. A job whose command prints a few megabytes of `\r` followed by one character would
freeze the operator's tab. A `while (end > 0 && s.charCodeAt(end - 1) === 13) end--` walk is O(k) in the
number of CRs actually stripped and has no backtracking. The unbounded partial itself is pre-existing
and out of scope (section 13.1).

**R10. NEW: the sibling item's claim that "the CLI wants the CRLF fix too" is weaker than it reads, and
the spec should not oversell Part 2's benefit to the CLI.** `internal/cli/logs.go:134` prints
`fmt.Fprintf(w, "[%s %s] %s\n", taskName, l.Stream, l.Content)`. A terminal renders `\r\n` correctly -
that is what CR LF means - so the CLI is not blanked by CRLF the way the SPA is. What Part 2 buys it is
the removal of one stray blank line per chunk (`"...\r\n"` plus the format's own `\n`). Real, minor.
`bug-2026-08-25-relay-logs-prints-nothing-envelope-drift:111-114` already scopes the CLI's genuine
CR wart (an interior `\r` returning the cursor over the `[task stream]` prefix) to itself and states it
is **not** addressed by the agent-side change. That division is correct and this spec keeps it.

**R11. CONFIRMED: the "Where normalisation belongs" decision record survives verification unchanged.**
Section 5 carries it forward with its reasoning intact. The one thing added is that the sibling item
(`:99-109`) already defers to it, so the decision is load-bearing for two items and must not be
re-litigated in either.

---

## 4. Part 1 - the web fix

### 4.1 The change

Inside `collapseCR`, remove **every** trailing carriage return, then run the existing progress-bar
collapse unchanged.

```
collapseCR(s):
    end = s.length
    while end > 0 and s.charCodeAt(end - 1) == 13:   # 13 == '\r'; index walk, not /\r+$/ (R9)
        end--
    t = (end == s.length) ? s : s.slice(0, end)      # the no-CR case does no work at all
    i = t.lastIndexOf('\r')
    return i == -1 ? t : t.slice(i + 1)
```

Four call sites are covered by construction (2.1). The progress-bar collapse is **not** removed:
interior-CR handling is wanted and correct.

### 4.2 Why stripping trailing CRs is always safe - the position argument

The general terminal semantics of a carriage return are an **overlay**, not a truncation: a real
terminal renders `"abc\rd"` as `dbc`. `collapseCR` deliberately approximates that as "keep the segment
after the final CR" (`:90-92`), and this spec does not change the approximation.

Under that approximation, is dropping a trailing CR ever wrong? No, and the reason is **positional**.
`collapseCR` is only ever called on one of two units: a complete line (everything before a `\n`) or the
entirety of a stream's current partial. In both, a trailing `\r` has **no successor inside the unit**.
Nothing can be written after the cursor returns, so the CR contributes no visible character and can
overwrite nothing. Removing it therefore cannot change what the unit shows - it can only stop the
approximation from returning the empty suffix that follows it. That is true for one trailing CR and for
a run of them, which is what makes strip-all the correct rule rather than merely a tolerant one.

The approximation stays wrong in the same way it was already wrong for interior CRs (`"abc\rd"` renders
as `d`, not `dbc`). That is a deliberate, pre-existing simplification and is out of scope (13.2).

### 4.3 The two rejected orderings, plus a third the item does not name

- **Collapse first, then strip.** `collapseCR("hello\r")` has already returned `""`; a strip afterwards
  has nothing to work on. Verified: no change on any CRLF input. **Rejected.**
- **Strip exactly one trailing CR, then collapse.** R1. Blanks `"x\r\r"`, which is the standard Windows
  `print(end="\r")` + `print()` output, and it couples Part 1 to Part 2 (R3). **Rejected.**
- **Replace `\r\n` with `\n` in the web, mirroring Part 2 client-side.** Not in the item; a reviewer
  will propose it. It fixes `"hello\r\n"` and fails `"x\r\r\n"` for the same reason Part 2 alone does,
  and it does nothing for a dangling partial `"abc\r"`, which has no `\n` to pair with. It also puts the
  shared transform in the one client that already has the private one. **Rejected.**

### 4.4 Proof that Part 2 is render-invisible under strip-all

This is the claim that lets both parts ship together without either changing the other's observable
output. Part 2 deletes exactly those `\r` bytes that are immediately followed by `\n` in the original
stream, and moves a trailing `\r` from the end of one chunk to the start of the next (or to the flush).

- **Completed lines.** A `\r` immediately followed by `\n` is, by construction, the last byte of the
  unit that `buf.slice(0, nl)` produces at `logBuffer.ts:144`. So Part 2 changes that unit from
  `X + "\r"` to `X`. Strip-all then removes every trailing CR from both, and
  `stripTrailing(X + "\r") == stripTrailing(X)`. **Identical.**
- **Partials** (`visibleRows` at `:175`/`:178`). Part 2 delays a trailing `\r` by one chunk, so the
  partial is `X` where it would have been `X + "\r"`. Strip-all renders both as `stripTrailing(X)`.
  **Identical.**
- **End of stream** (`finalizePartials` at `:197`). The held byte is flushed (6.3), restoring
  `X + "\r"`. **Identical.**
- **Aborted stream.** The held byte is dropped with the abandoned chunk (6.5), so the web sees `X`
  instead of `X + "\r"`. Strip-all: **identical.**

There is no input on which the two fixes combined produce a different rendered line than Part 1 alone.
Under strip-one, the `"x\r\r\n"` row differs (R3), which is why that variant is rejected.

### 4.5 The hold-back's delay is unbounded, and that is only acceptable because of Part 1

A subprocess that writes `"abc\r"` and then produces nothing for an hour without exiting leaves the
`\r` held for an hour. The SPA shows `abc` during that hour instead of `abc\r`. Under strip-all those
render identically, so the delay is unobservable. **Under any Part 1 that does not strip trailing CRs,
this would be a behaviour change with an unbounded window.** Naming it here so nobody later "optimises"
Part 1 back to strip-one without noticing what it re-couples.

---

## 5. Where normalisation belongs - the decision record, carried forward

Recorded 2026-08-25 in the item after `relay logs` was found to need CRLF handling too
(`bug-2026-08-25-relay-logs-prints-nothing-envelope-drift`) and the obvious reaction was to move all of
it server-side. **The reasoning was already litigated across two items and survives verification. It is
carried forward here in substance, not re-opened.**

**Only one of the three transforms in `logBuffer.ts` is shared work.**

| transform | who wants it | shared? |
|---|---|---|
| CRLF to LF | web, CLI, Python SDK, any export | yes - Part 2 |
| interior-CR collapse (progress bars) | web only | no |
| ANSI strip | web only, and the CLI wants the OPPOSITE | no |

**Never move ANSI stripping server-side.** The web strips ANSI because a DOM has no cursor and no
colour state, so raw bytes render as visible corruption. A terminal renders them correctly, which is
the entire point of a program emitting them. Stripping at or before storage permanently destroys colour
output for `relay logs` and for any future export. The interior-CR collapse is lossy the same way - a
CLI user piping frames to a file wants the frames.

**"Once" has to mean a pipeline stage, not a shared function.** Four clients consume this data in three
languages: `internal/cli`, `web/`, the Python SDK, and `internal/mcp` (immune, R7). No importable helper
spans them, so `internal/relayclient` would cover one of four.

**Why the agent rather than the server.** The straddle constraint eliminates both alternatives:

- *Server ingest* (`internal/worker/handler.go:1671`) is otherwise attractive - one edit covers the
  stored row and the SSE publish, and it works for already-deployed agents. But the straddle needs
  per-`(task, stream)` partial state the server does not keep, on a recv-goroutine path whose own
  comments justify staying at one statement; and the epoch fence can reject the chunk, so it would
  transform bytes it then discards. **This remains the documented fallback** if covering
  already-deployed agents ever matters more than the straddle case.
- *Server read path* is worse and is two sites: the REST handler reads `l.Content` from the row
  (`internal/api/tasks.go:118`) while the SSE publish sends `chunk.Content` from the wire. The REST side
  has the full ordered set; the SSE side still sees one chunk at a time. It pays on every read and is
  still not uniform.

The agent is the only site holding the contiguous byte stream, so it is the only one that can be both
complete and O(1).

---

## 6. Part 2 - CRLF to LF in the agent's `chunkWriter`

### 6.1 The transform, stated exactly

`chunkWriter` gains two fields: the hold-back (at most **one byte**, always) and a `sync.Mutex` guarding
it (D9). Because that byte is only ever `'\r'`, the shipped field is a `bool` named `heldCR` and the
byte itself is a literal at the two places it is re-emitted; the prose below says "the held byte" for
the design property, which is what matters here. The representation is not neutral, though - see the
"No interior pointers across locks" row in section 10 for what it removes. The guarantee is R5's:

> For one `(step, stream)` writer, the concatenation of every payload it emits equals the subprocess's
> bytes with each `\r\n` of the original replaced by `\n`.

Algorithm, per `Write(p)`:

1. `len(p) == 0` -> return `(0, nil)`, unchanged. The existing guard stays **first** and an empty write
   does not trigger a flush.
2. Build one buffer `chunk = held + p` (a copy is required anyway - `exec` reuses `p`), and **clear
   `held`**. The held byte's lifetime is exactly one `Write`.
3. If `chunk`'s last byte is `\r`, remove it and remember it as `newHeld`. At most one byte.
4. Collapse `\r\n` to `\n` **in place** over `chunk` - it is our own copy, so a second allocation is
   not needed. A `bytes.IndexByte(chunk, '\r') < 0` fast path skips the scan entirely, which is the
   common case on every Linux agent.
5. If `chunk` is now empty, set `held = newHeld` and return `(len(p), nil)` **without enqueueing**. This
   happens on exactly one input: no held byte and `p == "\r"`. Step 4 can never empty a buffer, since
   `\r\n -> \n` keeps a byte, so step 3 is the only step that can. No empty chunk is ever emitted, which
   strengthens the `:287` invariant rather than breaking it (R6).
6. Otherwise `sendOrAbort` the chunk. On `false`, return `(0, errForcedAbort)` and leave `held` empty -
   both the consumed byte and `newHeld` are discarded with the abandoned chunk (6.5). On success set
   `held = newHeld` and return `(len(p), nil)`.

`flush()` sends `held` as its own chunk if non-empty, through `sendOrAbort`, and clears it.

**Why one pass is exact after step 3.** The only `\r\n` pair the writer could miss is one straddling a
`Write` boundary, and step 2 folds the held byte into the next buffer before the scan, so no pair
straddles. Note what this does **not** claim, per R5: an emitted chunk may still end in `\r` (a payload
of `"\r\r"` emits `"\r"`), and the emitted concatenation may still contain a `\r\n` at new byte
positions (`"x\r\r\n"` becomes `"x\r\n"`). Both are correct, both are why Part 1 must strip **all**
trailing CRs, and both are why section 9.2 asserts the concatenation rather than "contains no CRLF".

### 6.2 What Part 2 deliberately does NOT do: collapse `\r+\n`

An `\r`-run immediately before a `\n` contributes nothing visible, so `\r+\n -> \n` would remove the
`"x\r\r\n"` residue at the agent. **Rejected.** It is a judgement about visible content, which is a
rendering decision, and section 5's whole argument is that rendering decisions stay in the client that
holds the opinion. `\r\n -> \n` has one unambiguous definition every consumer agrees on and is exactly
statable as a cost ("precisely the CR of each CRLF is removed, nothing else"). The residue is handled
by Part 1, in the client that already collapses CRs.

### 6.3 Where `flush()` is called, and why exactly there

**Immediately after `waitErr := cmd.Wait()` (`internal/agent/runner.go:215`), inside the loop, for both
writers, before any of the branches that follow.** That position, and no other, satisfies four
constraints simultaneously:

| constraint | why this position satisfies it |
|---|---|
| The writers are per-step (2.5) and become garbage at the end of the iteration | the flush runs before the iteration ends, on every path - `continue` at `:227` and `break` at `:237` both come after |
| A held `\r` must be enqueued before the next step's `sendStepMarker` (`:187`) | the marker is at the top of the next iteration |
| `handler.go:173-178`'s FIFO argument: a log chunk must be enqueued before `sendFinalStatus` | `sendFinalStatus` is at `:240`, after the loop |
| No concurrent copy goroutine may be running (6.4) | `Cmd.Wait` waits for the command to exit **and** for the copying from stdout and stderr to complete; with `WaitDelay` (`:190`) the pipes are force-closed, which makes the copies complete, and `Wait` still collects them |

The two earlier `break`s need no flush and get one harmlessly: the nil-argv break (`:180-183`) precedes
writer creation, and the `cmd.Start()` failure break (`:205-208`) means no `Write` ever ran. `flush()`
is a no-op when nothing is held, so the call site does not have to reason about them.

**Naming.** `flush()`, unexported, not `Close()`. `exec` never closes a caller-supplied `Stdout`, so
`Close()` would imply a call that does not happen (2.5). The name also matches the in-package precedent:
`makePrepareProgressFn` returns a `flush` that "must be called after provider.Prepare returns so
tail-end progress lines are not silently dropped" (`:372-376`, called at `:122`). That is the same
shape and the same hazard.

### 6.4 Concurrency: take the mutex, and say why it is belt-and-braces

`Write` runs on `exec`'s copy goroutine. `flush()` runs on the `Run` goroutine. `Cmd.Wait`'s documented
join means they cannot overlap - but that is a three-paragraph argument resting on a subtle `os/exec`
property, and this repo's `-race` lane is not runnable locally
(`idea-2026-08-25-no-documented-working-local-race-lane`), so CI is the only detector.

**Decision: guard `held` with a `sync.Mutex` on the `chunkWriter`.** One uncontended lock per 32 KiB of
subprocess output, against a channel send and a proto allocation already on that path - immeasurable.
It converts the argument to one line. `makePrepareProgressFn` already uses exactly this pattern for
exactly this reason (`:378-410`: a mutex around `buf`, `progress` called from the provider's goroutine,
`flush` from `Run`). The `Cmd.Wait` join is recorded as the reason the mutex is insurance rather than
correctness, so a future reader does not mistake it for evidence that the two really do race.

The lock is held only around the `held` read/clear/set - **never across `sendOrAbort`**, which can block
for as long as `sendCh` is full. Holding a lock across a bounded-but-slow send would be the first step
toward the lock-scope problems the Invariants exist to prevent.

### 6.5 The abort path: the held byte is dropped, never double-emitted

This is the highest-consequence interaction and the item does not analyse it.

**Rule: a `Write` either emits the held byte or drops it with its own chunk. Never both, never neither
without a successor.** Concretely, from 6.1: `held` is cleared at step 2 (folded into the buffer) and
`newHeld` is armed **only after a successful enqueue** at step 6. So:

- **Enqueue succeeds:** the old held byte is in the delivered chunk; `newHeld` is armed. Nothing lost,
  nothing duplicated.
- **Enqueue abandoned** (`sendOrAbort` false): the chunk is discarded, and with it the old held byte and
  `newHeld`. `Write` returns `errForcedAbort`, `io.Copy` stops, no further `Write` runs, and the
  subsequent `flush()` finds `held` empty and **enqueues nothing**. That last point matters: the writer
  must not perform a send *after* the abort path decided to stop sending.
- **Nothing to enqueue** (step 5): `newHeld` is armed with no send at all. A later abort discards it.

The bytes lost on an abort are exactly one trailing `\r` more than today's behaviour would have lost
(today the whole chunk, including that `\r`, is discarded together). Under Part 1 that difference is
render-invisible (4.4), and the process is being force-killed. **Accepted, bounded, and stated.**

**`flush()` must use `sendOrAbort`, never `r.send`.** `flush()` runs after `cmd.Wait()` on the cancel
path too, and `r.send` is bounded only by the **agent** context (2.6), so a per-task forced cancel with
a wedged `sendCh` would park `Run` until agent shutdown - reintroducing precisely the wedge that
`sendFinalStatus`'s cancelled branch (`:331-338`) and `sendInventory` (`:413-419`) were both written to
avoid, and delaying the terminal status indefinitely.

### 6.6 Load, failure modes, and threat model

- **State:** one bit per writer, two writers per step. Not proportional to input. `os/exec`'s copy
  buffer is 32 KiB (`internal/relayclient/client.go:141-151`), so the payload grows by at most 1 byte
  over a 32 KiB chunk; the SSE scanner's 1 MiB limit against a ~192 KiB worst case is untouched.
- **Work:** one branch plus one backward byte test per `Write`, and one forward scan only when the fast
  path finds a `\r`. In-place collapse, so no second allocation.
- **Amplification:** a pathological producer writing one `\r` per `Write` emits at most one chunk per
  write today and **exactly as many** with the hold-back - not fewer, which an earlier draft of this
  line claimed. Measured: 100,000 lone-`\r` writes produce 100,000 chunks, 99,999 from `Write` and 1
  from `flush`. Only the FIRST write emits nothing; every later one folds the held `\r` in and emits,
  and `flush` returns the one chunk the hold-back suppressed. The conclusion is unchanged and does not
  depend on the wrong word: no new unbounded queue, no new goroutine, no new parse, and one extra
  chunk is never created. `io.Copy` cannot spin, because a `Write` returning `(1, nil)` is followed
  by a blocking pipe read.
- **Threat model:** log bytes are influenced by anyone authorized to submit a job, which is already
  "run code on a worker". The hold-back introduces no new signal, no counter and no operator-facing
  remedy, so the forgeable-signal class does not apply. The one new attacker-shaped concern is on the
  **web** side and is R9's regex, which the index walk closes.
- **Failure mode if the flush is forgotten or mis-sited:** a trailing `\r` at the end of a step is
  silently dropped, with no error and no log line. That is a silent-loss shape, which is why the wiring
  test (9.2, T2-C) exists and why M6 mutates the call site rather than the method.

---

## 7. Decisions

| # | Decision | Basis |
|---|---|---|
| **D1** | **Part 1 strips ALL trailing CRs, then collapses.** Not one. | R1: `"x\r\r"` is the standard Windows `print(end="\r")` + `print()` line and stays blank under strip-one. R3: strip-all also decouples the two parts. 4.2: a trailing CR has no successor in its unit, so removing a run of them is as safe as removing one. |
| **D2** | The fix lives **inside `collapseCR`**, after `stripAnsi`, and the collapse is kept. | 2.1: one edit covers four call sites. 2.2: `stripAnsi` runs first and can promote a buried `\r` to trailing, so any site upstream of it sees fewer CRs. Progress-bar collapse is wanted behaviour. |
| **D3** | The strip is an **index walk**, not `/\r+$/`. | R9: the regex is O(n^2) on a CR run and no line-length cap exists in `logBuffer.ts`. |
| **D4** | `collapseCR` **keeps its name**; its doc comment (`:90-92`) is rewritten to state both rules and the position argument. | A rename touches four sites and buries a two-line behavioural diff in noise. The comment currently claims "only the segment after the final carriage return is kept", which becomes false - a required prose site (section 8). |
| **D5** | Part 2 normalises **exactly `\r\n` -> `\n`**, with a one-byte hold-back, at the agent. | Section 5, carried forward from two items. 6.2: `\r+\n` is a rendering judgement and stays client-side. |
| **D6** | The hold-back invariant is stated over the **concatenation**, against the **original** byte positions - never per chunk, never as "contains no CRLF". | R5: `"\r\r"` legitimately emits a chunk ending in `\r`, and `"x\r\r\n"` legitimately emits a CRLF at new positions. The concatenation form is exact and directly testable. |
| **D7** | `flush()` is an explicit unexported method called **immediately after `cmd.Wait()`, inside the per-step loop**, for both writers. | R4/2.5: no close hook exists and `exec` will not call one; the writers are per-step. 6.3's four constraints have exactly one satisfying position. |
| **D8** | `flush()` sends through **`sendOrAbort`**, never `r.send`. | 6.5: `r.send` is bounded only by the agent context, so a per-task cancel with a wedged `sendCh` would park `Run`. Matches `sendFinalStatus` and `sendInventory`. |
| **D9** | `held` is guarded by a **`sync.Mutex`**, never held across `sendOrAbort`. | 6.4: converts a subtle `os/exec` join argument into a one-line one, at immeasurable cost, matching `makePrepareProgressFn`'s in-package precedent. The `-race` lane is not locally runnable. |
| **D10** | On abort the held byte is **dropped**, never double-emitted, and `flush()` after an abort sends nothing. | 6.5. Arm `newHeld` only after a successful enqueue; clear `held` when folding it in. |
| **D11** | A trailing `\r` at end of stream is **flushed, not swallowed**. | Swallowing makes `Write` report a byte consumed that appears nowhere - silent loss. Emitting keeps the concatenation invariant (D6) exactly true. |
| **D12** | Both parts ship in **one slice**. | R3: under D1 they are independent, but the analysis proving that is the slice's main content, and Part 2 alone provably does not fix the bug. Shipping Part 2 without Part 1 would look like a fix and not be one. |
| **D13** | Part 1 **remains forever**; Part 2 is not a replacement. | R8: history, un-upgraded agents, the `\r\r\n` residue (R3/R5), and the unbounded hold delay (4.5). |
| **D14** | The item's **priority (`high`) stands.** | Unlike the worker-delete precedent, nothing here is overstated: 260/264 blank rows on a real task, on the project's primary platform, with no operator workaround at all. If anything the item understates it - Part 2 alone would not have fixed it. |

---

## 8. Prose sites

Wrong prose about correct code is this project's dominant defect class, and each of these currently
asserts something the slice makes false.

1. **`web/src/jobs/logBuffer.ts:90-92`** - "Within one emitted line only the segment after the final
   carriage return is kept". Becomes false. Rewrite to state both rules, the order, and 4.2's position
   argument (why stripping trailing CRs cannot lose visible content). Name CRLF explicitly so a future
   reader does not "simplify" the strip away, and name `\r\r\n` so nobody narrows it to strip-one.
2. **`internal/agent/runner.go:270-277`** - the `chunkWriter` type comment. "Each Write copies its slice
   ... wraps it in a TaskLogChunk ... On a successful enqueue Write returns (len(p), nil)". Add: the
   CRLF collapse, the one-byte hold-back, the third return case (consumed but not enqueued, R6), that
   `flush()` must be called after `cmd.Wait()` per step or a trailing `\r` is lost, and D6's
   concatenation invariant stated in the form that is true (R5) rather than as "no chunk contains CRLF".
3. **`internal/agent/runner.go:353-354`** - `sendOrAbort`'s "Only `chunkWriter.Write` uses this; all
   other callers use send so their blocking discipline is unchanged". `flush()` becomes a second caller.
   Amend to name `chunkWriter` (both methods) and keep the blocking-discipline sentence, which is still
   the point (D8).
4. **`internal/worker/handler.go:173-178`** - the FIFO ordering argument. It stays **true** under D7, and
   it is now depended on by a second enqueue site. Add one clause naming `chunkWriter.flush` alongside
   `chunkWriter`'s writes so the next person to move the call site sees what it breaks.
5. **`internal/store/query/*.sql`, README, ROADMAP** - **no edits.** Part 2 changes no stored contract
   beyond the byte-level one in item 6, and no README passage asserts byte-exactness.
6. **`docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md`** - proposes a log export.
   Part 2 forecloses a **byte-exact** one: stored bytes are no longer a byte-exact copy of the
   subprocess output. CRLF-vs-LF is not information anyone will want back, so take it - but take it
   deliberately, and annotate that item so the export spec is not written against a guarantee that no
   longer holds. Scope call in 13.4.

---

## 9. Acceptance criteria

Each is testable, each names its RED, and each carries the vacuity question: what would make it pass
while the code is wrong.

**One global correction to the item's Part 2 criteria, per R5.** The item asks that "a subprocess
emitting `\r\n` produces chunks containing no `\r\n`". **Do not write that test.** It is false on
legitimate input (`"x\r\r\n"` correctly becomes `"x\r\n"`), and restricting the fixture to inputs where
it happens to hold pins the wrong contract. Every Part 2 assertion below is an **equality against the
expected transform of the whole concatenation** instead.

### 9.1 Part 1 - `web/src/jobs/logBuffer.test.ts`, vitest, default lane

**T1-A. One regression test carrying all THREE discriminating inputs.** A single test (or one table)
whose cases are, in this order:

| # | input to `appendEntries` | expected lines | what it kills |
|---|---|---|---|
| 1 | `'hello windows\r\nsecond line\r\n'` | `['hello windows', 'second line']` | collapse-then-strip, and HEAD |
| 2 | `'frame 1/100\rframe 2/100\rframe 3/100\n'` | `['frame 3/100']` | any fix that removes the progress-bar collapse |
| 3 | `'x\r\r\n'` | `['x']` | **strip-one** (R1) |

**All three must be present.** Case 1 alone passes under a fix that deleted the collapse. Case 2 alone
passes at HEAD. Case 3 alone passes under a fix that strips all CRs including interior ones. This is
the item's own "either one alone passes under a wrong ordering" criterion, extended by one case that
the item's own design would have failed.
**RED at HEAD:** cases 1 and 3 yield `['', '']` and `['']`.
**Vacuity:** asserting `.length` rather than the text passes against every wrong variant. Assert the
strings.

**T1-B. The existing interior-CR test at `logBuffer.test.ts:118-122` stays green, byte-identical and
unedited.** Its passing is an acceptance criterion, not a new test. Editing it to accommodate the fix
would destroy the property it holds.

**T1-C. The partial paths.** An entry ending exactly at `"text\r"` with no newline renders `text` in
`visibleRows`, and `finalizePartials` flushes it as `text`.
**RED at HEAD:** both render `''`.
**Vacuity:** a fixture whose partial has no CR passes trivially - the `\r` must be the partial's last
character. Cover **stderr as well as stdout**, because `:175` and `:178` are separate string literals
and a partial fix would pass a stdout-only test.

**T1-D. Empty and CR-only units.** `'\r\n'` yields `['']` (an empty line is content, not an absence),
`'\n\n'` yields `['', '']`, and `'\r'` as a dangling partial renders `''`. These pin that the fix did
not start dropping rows.

**T1-E. An ANSI erase-line sequence between the CR and the newline.** Feed
`'text' + ESC + '[K'` preceded by a CR and followed by a LF - i.e. the bytes
`text`, CR, ESC, `[K`, LF, spelled the way `logBuffer.test.ts:132-136` already spells an ESC. Expect
`['text']` (2.2).
**Vacuity:** without it, nothing pins that the strip runs after `stripAnsi` rather than on `e.content`.

**T1-F. The implementation is an index walk, not a backtracking regex** (R9/D3). Reviewed, not tested -
a timing test would be flaky. Named here so the reviewer checks it.

### 9.2 Part 2 - `internal/agent`, default lane, NO build tag

**Lane note.** `runner_cancel_test.go` is `//go:build !windows` and `runner_multistep_test.go` uses a
`runtime.GOOS` switch (`echoArgv`, `:16-22`). **Neither pattern may carry the discriminating test.** A
`cmd /c echo` producer emits CRLF natively on Windows and never on Linux, so an assertion about CRLF
would be vacuously green on CI and meaningful only on a developer's machine - the recorded
platform-gated-verification trap, inverted.

**T2-A. The straddle. THE discriminating test.** Direct `chunkWriter.Write` calls against a `Runner`
built by `newRunner(..., make(chan *relayv1.AgentMessage, 64), context.Background(), 0)`. Feed, in this
order:

1. `[]byte("alpha\r")` - chunk N ends in `\r`
2. `[]byte("\nbravo\r\ncharlie")` - chunk N+1 begins with `\n`

Drain `sendCh`, join every `TaskLog.Content`, and assert the joined bytes are exactly
`"alpha\nbravo\ncharlie"` (D6).
**RED at HEAD:** the joined bytes are the input verbatim.
**Vacuity, and this is the whole point:** without the straddle, a stateless `bytes.ReplaceAll` and the
stateful hold-back are indistinguishable and the test passes on the version that still drops lines. The
same-chunk `\r\n` case does not stand in for it. **Put the straddled pair FIRST** so an early-exit
mutation cannot hide behind a benign leading chunk.

**T2-B. `Write` of exactly `"\r"` returns `(1, nil)` and enqueues nothing.** Then a following
`Write([]byte("\n"))` produces a single chunk `"\n"`.
**RED at HEAD:** the first call enqueues a `"\r"` chunk and the second a `"\n"` chunk.
**Vacuity:** asserting only the return value passes against a build that also enqueues. Assert the
channel is empty after the first call, **and** that `n == len(p)` with `err == nil`, which is the exact
contract `io.Copy` checks before deciding it has stalled.

**T2-C. The flush is WIRED, at a real `Run`, and it lands before the terminal status.** A cross-platform
producer that writes exact bytes: the test binary re-executing itself (`os.Args[0]` +
`-test.run=TestCRLFHelperProcess` + an env sentinel), whose helper does
`os.Stdout.Write([]byte("a\r\nb\r\r\nc\r"))`. Go performs no newline translation, so the bytes are
identical on Windows and Linux and no shell is needed. Assert, from the drained `sendCh`:

1. the joined stdout content for the step equals **exactly `"a\nb\r\nc\r"`** - the transform applied to
   the original positions, plus the flushed held byte;
2. it ends with `"c\r"`, so the held byte was flushed rather than swallowed (D11);
3. the flushed chunk appears **before** the terminal `TaskStatus` message in FIFO order
   (`handler.go:173-178`).

**Note what assertion 1 deliberately contains: a `\r\n`.** That is R5 made executable. A reviewer who
"fixes" this expected value to `"a\nb\nc\r"` has silently changed the design to `\r+\n -> \n`, which
6.2 rejects. Put that sentence in the test as a comment.
**RED at HEAD:** assertion 1 (HEAD emits the input verbatim).
**Vacuity:** asserting the method exists, or unit-testing `flush()` directly, proves nothing about the
call site - the recorded "a cadence test must assert the wiring" lesson. This test is what makes M6 and
M7 killable.

**T2-D. Multi-step: a held `\r` from step 1 is flushed before step 2's marker.** Two commands, the first
ending its stdout in a bare `\r`. Assert the `"\r"` chunk precedes the `=== relay step 2/2 ===` marker
chunk. This is the test that pins D7's *per-step* call site: a flush hoisted to the end of `Run` would
find the step-1 writer already replaced.

**T2-E. Abort: nothing is emitted after the abandon.** Force-cancel a runner mid-stream with a held byte
(per 6.5); assert no chunk is enqueued after the abandon and that `Run` returns promptly (the existing
cancel tests' 5 s bound). Reuse `runner_cancel_test.go`'s harness shape but **do not inherit its
`!windows` tag** if the test can be written without a `sleep` producer - the self-exec helper of T2-C
can block on stdin instead. If it genuinely cannot, state that it is the one platform-gated test in the
slice and that T2-A/B/C carry the correctness weight.

**T2-F. `stdout` and `stderr` hold independently.** Interleave a trailing `\r` on stdout with complete
lines on stderr; assert neither stream's held byte appears in the other's chunks. Pins that the state is
per-writer (2.5) and would catch a "hoist it onto the Runner" refactor.

### 9.3 Cross-part

**T3-A. Part 1 passes unchanged after Part 2.** The `web/` tests are untouched by the Go change; stated
as an acceptance criterion so nobody later deletes a `\r`-bearing web test on the grounds that "the
agent handles it now" (D13).

**T3-B. The render-invisibility claim (4.4) is asserted at least once.** Add a `logBuffer.test.ts` case
pair asserting that the post-Part-2 byte stream (`'x\r\n'`) and the pre-Part-2 stream (`'x\r\r\n'`) both
render `['x']`. That is the property that lets an upgraded and an un-upgraded agent look identical in
the SPA, and it is cheap to pin.

### 9.4 The mutation battery

Run in an **isolated detached worktree**, never the shared tree. Run the control first, and **verify
each mutation actually applied** before recording a result - CRLF has silently defeated four mutations
in a row on this repo.

| # | Mutation | Expected killer | Note |
|---|---|---|---|
| **M0** | Control: `collapseCR` returns `'SENTINEL'` unconditionally | every `logBuffer` test | Must die. Uniform results mean the harness is broken. |
| **M0b** | Control: `chunkWriter.Write` returns `(len(p), nil)` and enqueues nothing | T2-A | Must die. |
| M1 | strip-ALL -> strip-**one** trailing CR | **T1-A case 3** | The item's own design. This is the mutation the slice exists to prove wrong. |
| M2 | Move the strip **after** the collapse | T1-A case 1 | |
| M3 | Strip **all** CRs, interior included (`replaceAll('\r','')`) | T1-A case 2 | Kills the progress-bar collapse. |
| M4 | Strip trailing CRs at `logBuffer.ts:144` (the split site) instead of in `collapseCR` | T1-C | Three call sites left unfixed. Run it against the stderr arm too - a stdout-only T1-C would survive. |
| M5 | Replace the hold-back with a stateless per-`Write` replace | **T2-A** | The item's named hazard. If this survives, T2-A's straddle is not really straddled. |
| M6 | Delete the `flush()` **call site** (keep the method) | T2-C assertion 2, T2-D | The silent-loss shape (6.6). |
| M7 | Move the `flush()` call after the loop (after `sendFinalStatus`) | T2-C assertion 3, T2-D | Pins `handler.go:173-178`. |
| M8 | `flush()` uses `r.send` instead of `sendOrAbort` | T2-E, by timeout | If nothing dies, T2-E is not wedging `sendCh`. If a deterministic kill is not achievable, **declare it unkilled on the record** rather than inventing a flaky test - the reviewer check in D8 stands in. |
| M9 | Arm `newHeld` **before** the `sendOrAbort` instead of after | T2-E: a `"\r"` chunk appears after the abandon | The double-emit half of D10. |
| M10 | Drop the held byte in `flush()` instead of sending it | T2-C assertion 2 | The swallow half of D11. |
| M11 | Hoist `held` from the writer onto the `Runner` (shared across streams) | T2-F | |
| M12 | Second collapse pass, i.e. `\r+\n -> \n` | **T2-C assertion 1** | Proves the expected value's embedded `\r\n` is load-bearing and not a typo somebody will "correct" (6.2). |
| M13 | Remove the `sync.Mutex` | **Nothing.** | **Declared unkillable and stated as such** (6.4): the `Cmd.Wait` join means no deterministic test can observe the race. Do not invent a flaky concurrency test to manufacture a kill. What stands in: the doc comment and this row. |

---

## 10. Invariants

| Invariant | Status |
|---|---|
| **End the generation before releasing the resource** | **Implicated in its acquire/release reading, in the small.** The held byte is state taken in one `Write` and released in the next (or in `flush()`). D10 arms it in the same breath as the successful handoff and clears it when it is folded in, so no early return - including the `errForcedAbort` return the abort path adds - can leave a byte owned by a writer nobody will call again. The per-step call site (D7) is the same rule at the outer scale: the writer's state is released before the writer is. |
| **Epoch fence** | Not implicated in the fence itself: the flushed chunk carries `w.r.epoch` exactly as every other chunk does, and no `tasks.status` or `task_logs` **write path** changes. One second-order note: the flushed chunk is a `task_logs` write like any other and lands under `AppendTaskLog`'s status-allow-list-or-recency disjunction. D7 keeps it ordered before the terminal status, so it is admitted by the first arm and never depends on the trailing window. |
| **Single job-spec pipeline** | Not implicated. No spec ingestion. |
| **One bounded sender per gRPC stream** | **Directly implicated and it is D8.** `flush()` is a new enqueue site on the `Run` goroutine and must be bounded: `sendOrAbort` (bounded by `forcedCh`, `cancelledCh`, `ctx.Done`), never `r.send` (bounded only by the agent context). All writes still funnel through the single `sendCh` and the one send goroutine. No new sender. |
| **Identity-checked teardown** | Not implicated: nothing is unregistered, no connection state is touched, no worker row is written. |
| **No interior pointers across locks** | Satisfied, and worth stating because D9 adds a lock - but **the two sites needed two different arguments, which is why the field is a `bool`**. In `Write` the hold-back is genuinely *copied*: the byte is appended into a freshly allocated `chunk` under the lock, so nothing the outgoing message points at is reachable from the writer. In `flush` a `[]byte` field would NOT have been copied - it would take the slice header out under the lock and put the same backing array into `Content`, safe only by a *sole-ownership* argument (clearing the field in the same critical section leaves exactly one owner), not by a copying one. Carrying the hold-back as a `bool` retires that second argument outright: no writer-owned slice header leaves the critical section at all, and `flush`'s `Content` is a fresh one-byte literal. In both, **the lock is never held across `sendOrAbort`** (6.4). |
| **Single JSON entry point** | Not implicated. No HTTP body is read. |
| **Status predicates as allow-lists** | Not implicated: no status predicate changes and no task status is added. Named because `AppendTaskLog`'s disjunction and `ListOverdueAssignedTasks`'s partition are the two carve-outs a log-path slice must be shown to have considered. Neither moves here. |

---

## 11. Non-goals

- **Do NOT move ANSI stripping server-side or into `internal/relayclient`.** Section 5. A terminal
  renders escape sequences correctly; stripping at or before storage permanently destroys colour output
  for `relay logs` and for any future export. This is decided across two backlog items and is not
  re-opened by this slice.
- **Do NOT move the interior-CR collapse out of `web/`.** Same section, same reason: it is lossy, and a
  CLI user piping frames to a file wants the frames.
- **No byte-exact log export work.** Part 2 forecloses byte-exactness deliberately (8.6); the export
  itself belongs to `idea-2026-08-09-task-log-tail-and-paging-improvements`.
- **No server-side normalisation**, at ingest (`internal/worker/handler.go:1671`) or on either read path
  (`internal/api/tasks.go:118`, the SSE publish). Both remain documented fallbacks, not scope.
- **No `internal/cli` change.** `logs.go:134`'s `[task stream] content` prefix versus an interior `\r`
  belongs to `bug-2026-08-25-relay-logs-prints-nothing-envelope-drift`, which explicitly scopes it to
  itself (`:111-114`). An implementer who "just fixes it while in there" is drifting across two items'
  boundaries.
- **No `internal/mcp` change.** Structurally immune (R7).
- **No Python SDK change.**
- **No log virtualization, tail paging, in-log search, or ANSI colour rendering.** All belong to
  `idea-2026-08-09-task-log-tail-and-paging-improvements`.
- **No `collapseCR` rename** (D4).
- **No `\r+\n` collapse at the agent** (D5/6.2) - and see M12, which exists to stop it being added by a
  reviewer who mistakes T2-C's expected value for a typo.
- **No Playwright e2e.** The transform is a pure string function with no layout component, so vitest is
  the right instrument; a browser lane would assert nothing extra here. (The browser lane earns its slot
  when layout is the claim. It is not.)

---

## 12. Autonomous decision ledger

Gate mode is autonomous. Every fork a human would have been asked about, with the call and its basis:

| # | Question | Call | Basis |
|---|---|---|---|
| 1 | Strip one trailing CR or all of them? | **All** | R1: `"x\r\r"` is standard Windows output and stays blank under strip-one; 4.2 shows strip-all is never worse. **This overrules the item.** |
| 2 | Where does the strip go? | **Inside `collapseCR`, after `stripAnsi`** | 2.1 (four sites, one edit), 2.2 (the strip must see post-ANSI text). |
| 3 | Regex or index walk? | **Index walk** | R9: `/\r+$/` is O(n^2) on an uncapped line. |
| 4 | Rename `collapseCR`? | **No** | D4: a rename buries a two-line diff; the comment repairs the name. |
| 5 | `\r\n -> \n` or `\r+\n -> \n` at the agent? | **`\r\n -> \n` only** | 6.2: the wider rule is a rendering judgement, and section 5 keeps those client-side. |
| 6 | How is the Part 2 invariant stated and tested? | **Equality of the concatenation against the original-position transform** | R5: the item's "no chunk contains CRLF" is false on `"x\r\r\n"` and would have produced a failing or vacuous test. |
| 7 | Where does `flush()` go? | **Right after `cmd.Wait()`, inside the per-step loop** | 6.3: four constraints, exactly one satisfying position. |
| 8 | `Close()` or `flush()`? | **`flush()`** | 2.5: `exec` never closes a caller-supplied `Stdout`, so `Close()` implies a call that does not happen. |
| 9 | `r.send` or `sendOrAbort` for the flush? | **`sendOrAbort`** | 6.5: `r.send` parks until agent shutdown on a per-task cancel. |
| 10 | Mutex on `held`, or rely on `Cmd.Wait`'s join? | **Mutex, with the join recorded as the reason it is insurance** | 6.4: immeasurable cost, one-line argument, no locally runnable `-race` lane, and `makePrepareProgressFn` is the in-package precedent. |
| 11 | Held byte on abort: drop or keep? | **Drop, with the chunk** | 6.5: a post-abort send contradicts the abort; the loss is one render-invisible `\r`. |
| 12 | Held byte at end of stream: flush or swallow? | **Flush** | D11: swallowing is silent loss and breaks the concatenation invariant. |
| 13 | One slice or two? | **One** | D12/R3: Part 2 alone provably does not fix the bug, so shipping it separately would look like a fix. |
| 14 | Does the item's `high` priority stand? | **Yes** | D14: 260/264, primary platform, no operator workaround. Unusually for this project, the item under-states rather than over-states. |
| 15 | Test lane for Part 2? | **Default lane, no build tag, self-exec helper process** | 9.2: `runtime.GOOS`-switched and `!windows` producers make the assertion vacuous on CI. |

Where a fork was close, the more conservative and more reversible arm was taken: 4 (no rename), 5
(narrow transform), 10 (take the lock), 12 (lose no bytes).

---

## 13. Open questions and risks

### 13.1 Risks

- **`Cmd.Wait`'s join guarantee is documented but subtle.** `Wait` waits for the command to exit and for
  the copying from stdout and stderr to complete, and `WaitDelay` (`runner.go:190`) force-closes the
  pipes so those copies complete. D9's mutex makes the slice correct either way, so this is a
  documentation risk rather than a correctness one. The plan should still confirm the sentence against
  the pinned toolchain's `os/exec` documentation rather than trusting this spec.
- **The `-race` lane is not locally runnable** (`idea-2026-08-25-no-documented-working-local-race-lane`),
  so CI is the only detector for anything D9 gets wrong. Say so in the PR body.
- **A log line is unbounded in the web.** `MAX_LINES` caps line count, not length, and the partial
  buffer at `logBuffer.ts:140` grows until a `\n` arrives. Pre-existing, not introduced here, and D3
  removes the one way this slice would have made it worse. **Proposed as a follow-on** (13.3).
- **Part 2 changes stored bytes.** Accepted deliberately (8.6). The risk is that a later export spec is
  written against a byte-exactness guarantee that no longer holds, which is why the annotation in 8.6
  matters.
- **The `\r\r\n` residue is easy to un-learn.** If a future reader "simplifies" D1 back to strip-one on
  the grounds that Part 2 removes CRLF anyway, the bug returns for that shape and only for that shape.
  T1-A case 3 and M1 are what stop it; the doc comment in 8.1 is what explains it. The symmetric hazard
  is a reviewer "correcting" T2-C's expected value, which M12 covers.

### 13.2 Known incompleteness, stated rather than fixed

`collapseCR` approximates a terminal's overlay semantics with "keep the segment after the final CR":
`"abc\rd"` renders as `d` where a terminal shows `dbc`. That is pre-existing, deliberate
(`logBuffer.ts:90-92`), and unchanged. This slice makes the approximation right at the **end** of a
unit; it does not make it right in the **middle**. Anyone who wants true overlay semantics is asking
for a terminal emulator in the SPA, which is a different feature.

### 13.3 Follow-on items proposed, NOT filed

Per the TPM boundary, these are proposals for a human to accept:

1. **Annotate `idea-2026-08-09-task-log-tail-and-paging-improvements`** that a byte-exact export is
   foreclosed by this slice (8.6). See 13.4 - this may be in-scope rather than a new item.
2. **A log line has no length cap in the SPA.** The partial buffer grows unbounded until a `\n`. A job
   printing megabytes with no newline degrades the tab regardless of D3. Cap the partial, or truncate
   with a marker.
3. **`relay logs` prints a `[task stream]` prefix per chunk, not per line**, so an interior `\r` returns
   the cursor over the prefix and a chunk ending in `\n` produces a stray blank line. Already named in
   `bug-2026-08-25-relay-logs-prints-nothing-envelope-drift:111-114`; flagged here only so it is not
   silently absorbed into this slice.

### 13.4 The one thing the conductor may want to decide before planning

**Whether item 1 above (the `idea-2026-08-09` annotation) is in this slice's scope or a separate item.**
It is a two-line docs edit and this slice is the reason the guarantee changes, which argues for
in-scope. Everything else in this spec is decided.
