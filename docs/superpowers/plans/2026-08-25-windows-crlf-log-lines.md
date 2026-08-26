# Windows CRLF Log Lines Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop CRLF subprocess output rendering as a column of blank timestamped rows, by stripping every trailing carriage return inside `collapseCR` at render time, and by collapsing `\r\n` to `\n` in the agent's `chunkWriter` with a one-byte straddle hold-back.

**Architecture:** Two independent surfaces. Part 1 rewrites `collapseCR` in `web/src/jobs/logBuffer.ts` as strip-all-trailing-CRs (an index walk, not a regex) followed by the existing interior-CR collapse; four call sites are covered by construction. Part 2 gives `chunkWriter` a one-byte `held` field guarded by a mutex, collapses `\r\n` to `\n` in place per `Write`, and adds an explicit `flush()` called immediately after `cmd.Wait()` inside the per-step loop. **No migration, no `.proto`, no `.sql`, no server change, no CLI change, so no `make generate` and no CRLF-revert procedure anywhere in this plan.**

**Tech Stack:** TypeScript + vitest (Part 1); Go 1.26.2, `os/exec`, protobuf `relayv1`, testify (Part 2).

**Spec:** `docs/superpowers/specs/2026-08-25-windows-crlf-log-lines.md` - cite by section, do not re-derive. Sections 7 (Decisions), 9 (acceptance criteria) and 12 (the autonomous decision ledger) are settled; nothing in this plan re-opens them.

**Closes:** `docs/backlog/bug-2026-08-25-windows-crlf-log-lines-render-blank.md`.

> **THE SPEC REFUTES THE BACKLOG ITEM'S PART 1 DESIGN, AND THE SPEC WINS.** The item
> (`:62-71`) says "Strip at most ONE trailing `\r` ... prefer the narrower rule". Spec R1 and D1
> refute it: `"x\r\r"` is what the Windows C runtime produces for `print("done", end="\r")` followed
> by `print()`, and a single strip still renders that row blank. **Strip ALL.** An implementer who
> reads the item and "corrects" the code back to strip-one reintroduces the bug for exactly that
> shape. Task F1's test case 3 and mutation M1 exist to stop that, and the doc comment written in
> Task F1 is what explains it to the next reader. The item's Part 2 acceptance criterion ("chunks
> containing no `\r\n`") is refuted the same way by spec R5 and **must not be written as a test** -
> see the standing warning in Task B2.

---

## Slice independence declaration

**The two lanes are INDEPENDENT and MUST be dispatched CONCURRENTLY in Phase 3.** One
`relay-frontend-engineer` runs Tasks F1-F2; one `relay-backend-engineer` runs Tasks B1-B3. Neither
lane reads, writes, or blocks on the other's files, and neither lane's tests exercise the other's
code.

**Proof that the file sets are disjoint** (spec 4.4 proves the *behaviours* are disjoint; this is the
file-level claim):

| Lane | Files it may touch |
|---|---|
| **Frontend** (F1-F2) | `web/src/jobs/logBuffer.ts`, `web/src/jobs/logBuffer.test.ts`. **Nothing else under `web/`, and nothing outside `web/`.** |
| **Backend** (B1-B3) | `internal/agent/runner.go`, `internal/agent/runner_crlf_test.go` (new), `internal/worker/handler.go` (comment only), `docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md` (a few lines). |

**Every prose site in spec section 8 is allocated to exactly one owner. No file appears twice.**

| Spec 8.x | Site | Owner | Task |
|---|---|---|---|
| 8.1 | `web/src/jobs/logBuffer.ts:90-92` (`collapseCR`'s doc comment) | frontend | **F1** - lands in the same commit as the code that makes it false |
| 8.2 | `internal/agent/runner.go:270-277` (the `chunkWriter` type comment) | backend | **B1** (transform + hold-back + third return case) and **B2** (the `flush()` sentence) - split so the comment is never false between commits |
| 8.3 | `internal/agent/runner.go:353-354` (`sendOrAbort`'s "Only `chunkWriter.Write` uses this") | backend | **B2** - becomes false the instant `flush()` exists, so it lands in that commit |
| 8.4 | `internal/worker/handler.go:173-178` (the FIFO ordering argument) | backend | **B3** - stays TRUE under D7, so it may land later; it gains one clause |
| 8.5 | `internal/store/query/*.sql`, `README.md`, `ROADMAP.md` | **nobody** | **No edits.** If a diff touches any of these, something drifted out of scope. |
| 8.6 | `docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md` | backend | **B3** - the conductor ruled spec 13.4 IN SCOPE |

**Concurrency discipline for two agents on one branch.** Both lanes commit to
`claude/windows-crlf-log-blank-c488bf`. Every commit in this plan stages an **explicit file list**.
**Never run `git add -A`, `git add .`, or `git commit -a`** - that is the only way the two lanes can
collide, and it would sweep the other agent's in-progress edits into your commit. If `git commit`
fails with an index lock, wait two seconds and retry; do not delete the lock file.

**Mutation batteries take their own detached worktree**, one per task, with distinct paths (named per
task below), so no lane ever mutates the shared tree another lane is reading.

**Task C1 (whole-slice verification and backlog close) is the conductor's** and runs after both lanes
report done.

---

## Critical files

| File | Why |
|---|---|
| `web/src/jobs/logBuffer.ts:93-96` | `collapseCR`. The entire Part 1 change. Its four callers (`:146`, `:175`, `:178`, `:197`) are fixed by construction - **do not fix at the split site `:144`**, which would leave three of the four unfixed (spec 2.1, mutation M4). |
| `web/src/jobs/logBuffer.test.ts:118-122` | The interior-CR test. **Must pass byte-identical and unedited throughout** (spec T1-B). It is green today *because* the defect only fires when a CR is the final character; editing it to accommodate the fix destroys the property it holds. |
| `internal/agent/runner.go:270-309` | `chunkWriter`, its type comment and `Write`. The entire Part 2 change plus two prose sites. |
| `internal/agent/runner.go:202-215` | The per-step writer construction and `cmd.Wait()`. The `flush()` call site, and the position is the correctness argument (spec 6.3). |
| `internal/agent/runner.go:350-370` | `sendOrAbort` - the bounded enqueue `flush()` must use, never `r.send` (`:342-348`), which is bounded only by the AGENT context. |
| `internal/worker/handler.go:173-178` | The FIFO argument that bounds `AppendTaskLog`'s trailing window. It stays true only because the flush is enqueued **before** `sendFinalStatus`. Mutation M7 is what pins it. |
| CLAUDE.md Invariants | "One bounded sender per gRPC stream" is D8; "End the generation before releasing the resource" is the held byte in its acquire/release reading (spec section 10). |

## File inventory

**New (1):** `internal/agent/runner_crlf_test.go` - default lane, **no build tag** (spec D15).

**Edited (5):**

| File | Nature | Lane |
|---|---|---|
| `web/src/jobs/logBuffer.ts` | `collapseCR` body (4 lines) + doc comment rewrite | frontend |
| `web/src/jobs/logBuffer.test.ts` | **append only** - 5 new tests, zero edits to existing ones | frontend |
| `internal/agent/runner.go` | two struct fields, one helper func, `Write` rewritten, one new method, four call-site lines, three comment rewrites, one import (`bytes`) | backend |
| `internal/worker/handler.go` | one clause in one comment | backend |
| `docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md` | one appended paragraph | backend |

**Never touched:** anything under `internal/store/`, `internal/api/`, `internal/cli/`, `internal/mcp/`, `python/`, `README.md`, `ROADMAP.md`, `web/dist/`, `web/src/jobs/useTaskLogStream.ts`, `web/src/jobs/LogView.tsx`, `internal/agent/runner_cancel_test.go`, `internal/agent/runner_multistep_test.go`, `internal/agent/runner_test.go`.

## Environment notes (read once, both lanes)

- **Tree is the worktree `D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf`.** Absolute
  paths only. **Do not `cd D:/dev/relay`** - that is the main repo and commits would land on `main`.
- **`make` is not installed on this machine.** Every command below is the raw command, not a target.
- **No `make generate`, no `sqlc generate`, no `buf generate`, and therefore no CRLF-revert
  procedure.** This slice changes no `.sql` and no `.proto`. CLAUDE.md's generated-file rules do not
  apply here; if you find yourself running `sqlc`, you are out of scope.
- **`web/node_modules` does not exist in this worktree** (it is gitignored and per-directory).
  The frontend lane's first action is `npm ci` in `web/`. Takes 1-2 minutes.
- **The frontend lane must NOT run `npm run build`.** `web/dist/index.html` is tracked while the rest
  of `web/dist/` is gitignored, so a build dirties a tracked file for no reason. Typecheck is CI's
  job (`web-ci.yml:58-59`); if you run `npx tsc -b` locally, delete any `*.tsbuildinfo` it writes and
  confirm `git status --porcelain web` shows only the two intended files.
- **`go test -race` is NOT runnable locally** (environmental ThreadSanitizer allocation failure) and
  **CI's `go-ci` job is the only detector for anything D9's mutex gets wrong** (`go-ci.yml:34` runs
  `go test -race ./... -timeout 180s` on Linux). Do not attempt it, do not claim a local race result,
  and say so in the PR body (spec 13.1).
- **Docker is NOT required for any test in this slice.** No integration-tagged file is created or
  touched, so `go test -tags integration` is not part of any task's gate. `go vet -tags integration
  ./...` still runs once in Task C1 as a compile check.
- **What each engineer can verify, and what only CI can.** Both new Go test groups and all five new
  web tests run on Windows and on Linux. The one thing a Windows developer cannot see is the
  `//go:build !windows` half of `internal/agent` (`runner_cancel_test.go` is skipped wholesale by
  `go test` on Windows) - **this slice adds nothing there and changes nothing it covers**, but
  `chunkWriter.Write` is on its path, so a regression in those Unix-only cancel tests is a CI-only
  signal.

### Mutation-worktree procedure (both lanes - read before any battery)

Three rules, each earned on this repo:

1. **Seed the mutant worktree by COPYING, not by `HEAD`.** A detached worktree at `HEAD` does not
   contain the task's implementation, because the task has not been committed yet. Create the
   worktree, then copy the task's modified files into it explicitly (spelled out per task).
2. **Revert a mutation by RE-COPYING the file from the shared worktree. Never `git checkout --` in
   the mutant tree.** `HEAD` there is the *pre-task* state, so a checkout silently reverts the
   implementation along with the mutation, and every subsequent row reports "survived" against code
   that is not the code under test.
3. **Run the unmutated suite in the mutant tree first and require it GREEN**, then run the control
   mutation and require it DEAD, before recording any other row. Uniform results mean the harness is
   broken, not that coverage is good. And **verify each mutation actually applied** with the named
   grep before recording its result - a silently unapplied edit reports "survived".

## Task index

| # | Lane | Title | Acceptance criteria covered |
|---|---|---|---|
| **F1** | frontend | `collapseCR` strips every trailing CR, plus its doc comment | T1-A, T1-B, T1-F; M0, M1, M2, M3 |
| **F2** | frontend | Pin the four call sites, the empty-unit rows, the ANSI ordering, and render-invisibility | T1-C, T1-D, T1-E, T3-B; M4, M3b, M-ansi |
| **B1** | backend | `chunkWriter` holds one byte and collapses CRLF per `Write` | T2-A, T2-B, T2-E, T2-F; M0b, M5, M9, M11 |
| **B2** | backend | `flush()` and its per-step call site, wired at a real `Run` | T2-C, T2-D, T2-G; M6, M7, M8, M10, M12, M13 |
| **B3** | backend | `handler.go`'s FIFO clause and the foreclosed byte-exact export | spec 8.4, 8.6 |
| **C1** | conductor | Whole-slice verification and backlog close | T3-A |

---

# Frontend lane

### Task F1: `collapseCR` strips EVERY trailing carriage return

**Files:**
- Modify: `web/src/jobs/logBuffer.ts:90-96` (the doc comment and the function body)
- Test: `web/src/jobs/logBuffer.test.ts` - **append** one test after the existing interior-CR test at `:118-122`. Do not edit any existing test.

- [ ] **Step 0: Install dependencies (once per worktree)**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf/web
npm ci
```

Expected: completes without error. `web/node_modules/` appears (gitignored).

- [ ] **Step 1: Write the failing test**

Append to `web/src/jobs/logBuffer.test.ts`, immediately after the existing
`'a carriage-return run collapses to the segment after the final CR'` test (`:118-122`):

```ts
// THE THREE DISCRIMINATING INPUTS (spec T1-A). All three must stay:
//   case 1 alone passes under a fix that deleted the progress-bar collapse;
//   case 2 alone passes at HEAD, where the bug lives;
//   case 3 alone passes under a fix that strips every CR, interior ones included.
//
// CASE 3 IS THE ONE THE BACKLOG ITEM'S OWN DESIGN FAILS. That item proposes
// stripping at most ONE trailing carriage return. The Windows C runtime writes
// "done\r\r\n" for print("done", end="\r") followed by print() - a literal \r is
// not translated, the \n becomes \r\n - so the line is "done\r\r" and a single
// strip still renders it blank. The spec refutes the item here (R1/D1). Do not
// narrow this rule back.
//
// Unlike the Go straddle test in internal/agent, each case here is an
// INDEPENDENT appendEntries call over a fresh state, so no case can pass by
// riding on a previous one and the table order is not load-bearing.
test('collapseCR strips EVERY trailing carriage return and still collapses interior CR runs', () => {
  const cases: Array<[string, string[]]> = [
    ['hello windows\r\nsecond line\r\n', ['hello windows', 'second line']],
    ['frame 1/100\rframe 2/100\rframe 3/100\n', ['frame 3/100']],
    ['x\r\r\n', ['x']],
  ]
  const got = cases.map(([content]) =>
    appendEntries(createLogState(), [chunk(1, content)]).lines.map((l) => l.text),
  )
  expect(got).toEqual(cases.map(([, want]) => want))
})
```

Vacuity note for the reviewer: this asserts the **strings**, not `.length`. A length-only assertion
passes against every wrong variant, because the defect produces the right number of rows with the
wrong (empty) text.

- [ ] **Step 2: Run test to verify it fails**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf/web
npx vitest run src/jobs/logBuffer.test.ts
```

Expected: **1 failed**, with a diff showing case 1 as `['', '']` and case 3 as `['']` against the
expected `['hello windows', 'second line']` and `['x']`. Case 2 already matches - that is the point
of including it.

- [ ] **Step 3: Write minimal implementation**

Replace `web/src/jobs/logBuffer.ts:90-96` (the comment and the whole function) with:

```ts
// Two rules, in this order, and the order is the whole correctness argument.
//
// 1. EVERY trailing carriage return is removed. Not one. CRLF output puts a \r
//    in the final position of every line, and "x\r\r" - two of them - is what
//    the Windows C runtime writes for print("done", end="\r") followed by
//    print(), which is the most ordinary progress-bar-then-newline sequence
//    there is. Stripping a single CR leaves that row blank.
// 2. THEN the progress-bar collapse: only the segment after the final REMAINING
//    carriage return is kept, so a run of \r-updated frames renders as one
//    updating line instead of a wall of concatenated garbage.
//
// Why removing a run of trailing CRs cannot lose visible content: this function
// is only ever called on a complete line (everything before a \n) or on the
// whole of a stream's in-flight partial. In both, a trailing carriage return has
// NO SUCCESSOR INSIDE THE UNIT, so nothing can be written after the cursor
// returns and the CR can overwrite nothing. Removing it can only stop the
// approximation from returning the empty suffix that follows it. That is as true
// for a run as for one, which is why the rule is strip-all rather than merely
// tolerant.
//
// Collapsing FIRST and stripping afterwards does nothing at all: the collapse has
// already returned '' for a line ending in a carriage return.
//
// The strip is an INDEX WALK, deliberately not a regular expression. JavaScript's
// engine backtracks, so a trailing-run pattern is quadratic in a long run of
// carriage returns, and nothing here caps line LENGTH - MAX_LINES caps line
// COUNT, and the partial buffer in appendEntries grows until a newline arrives.
// A job printing megabytes of carriage returns would freeze the operator's tab.
//
// This runs AFTER stripAnsi (appendEntries strips before it splits), and that
// ordering is in the fix's favour: an erase-line escape sequence sitting between
// a carriage return and the newline is removed first, so a CR the raw bytes had
// buried behind an escape is still trailing by the time we see it. Anything
// upstream of stripAnsi would see fewer carriage returns, not more.
//
// The remaining approximation is unchanged and deliberate: a terminal renders
// 'abc' CR 'd' as `dbc`, and this keeps `d`. That is wrong in the MIDDLE of a
// unit and is out of scope; this makes it right at the END.
function collapseCR(s: string): string {
  let end = s.length
  while (end > 0 && s.charCodeAt(end - 1) === 13) end-- // 13 === '\r'
  const t = end === s.length ? s : s.slice(0, end)
  const i = t.lastIndexOf('\r')
  return i === -1 ? t : t.slice(i + 1)
}
```

Nothing else in the file changes. In particular the four call sites (`:146`, `:175`, `:178`, `:197`)
are untouched - that is the design (spec D2: one edit, four sites).

- [ ] **Step 4: Run test to verify it passes**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf/web
npx vitest run src/jobs/logBuffer.test.ts
```

Expected: PASS, all tests in the file, including the untouched `:118-122` interior-CR test (that is
acceptance criterion T1-B - its passing unedited is itself a criterion).

Then the whole web unit suite, because `logBuffer` is consumed by `LogView` and `LogTab`:

```powershell
npm test
```

Expected: PASS. If `LogView.test.tsx` or `LogTab.test.tsx` fail, **read the failure before touching
them**: a fixture that asserted a blank row was asserting the defect.

- [ ] **Step 5: Mutation battery (isolated detached worktree)**

Create and **seed** the mutant tree - a bare `HEAD` worktree does not contain Step 3's fix:

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mut-f1 HEAD
Copy-Item web/src/jobs/logBuffer.ts      C:/Users/chadv/AppData/Local/Temp/relay-mut-f1/web/src/jobs/logBuffer.ts
Copy-Item web/src/jobs/logBuffer.test.ts C:/Users/chadv/AppData/Local/Temp/relay-mut-f1/web/src/jobs/logBuffer.test.ts
cd C:/Users/chadv/AppData/Local/Temp/relay-mut-f1/web
npm ci
npx vitest run src/jobs/logBuffer.test.ts
```

Expected: **GREEN baseline.** If it is not green, the seed copy failed - fix that before mutating.

For each row: edit `web/src/jobs/logBuffer.ts` in the **mutant** tree, confirm the mutation applied
with the named grep, run `npx vitest run src/jobs/logBuffer.test.ts`, record the result, then
**re-copy `logBuffer.ts` from the shared worktree** to revert. Do not `git checkout --` in the mutant
tree (see the procedure note above).

| # | Mutation | Applied-check | Expected |
|---|---|---|---|
| **M0** control | `collapseCR` body -> `return 'SENTINEL'` | `Select-String -Path web/src/jobs/logBuffer.ts -Pattern "SENTINEL"` | **MUST DIE** on essentially every test in the file. A survival means the harness is broken (bad seed, stale cache) - stop and fix that before recording any other row. |
| M1 | strip-ALL -> strip-ONE: replace the `while` loop with `const end = s.length > 0 && s.charCodeAt(s.length - 1) === 13 ? s.length - 1 : s.length` | grep for `s.length - 1 : s.length` | dies on **case 3** (`'x\r\r\n'` -> `['']`). **This is the item's own design, and this row is the mutation the slice exists to prove wrong.** |
| M2 | move the strip AFTER the collapse: collapse first into `t`, then walk `t`'s trailing CRs | grep for the reordered body | dies on **case 1** |
| M3 | `return s.replaceAll('\r', '')` | grep for `replaceAll` | dies on **case 2** (interior collapse destroyed: `'frame 1/100frame 2/100frame 3/100'`) |

Record every row's outcome in the commit body. M4, M3b and M-ansi belong to Task F2, which writes
their killers.

- [ ] **Step 6: Clean up the mutant worktree and commit**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
git worktree remove --force C:/Users/chadv/AppData/Local/Temp/relay-mut-f1
```

```bash
git add web/src/jobs/logBuffer.ts web/src/jobs/logBuffer.test.ts
git commit -m "fix(web): strip EVERY trailing carriage return before collapsing, so CRLF lines render

collapseCR kept only the segment after the final CR, and on CRLF output that CR
is the last character - 260 of 264 entries on one production task rendered as a
timestamp with no text. The fix is strip-all, not strip-one: a line ending in two
carriage returns is what the Windows C runtime writes for print(end='\r')
followed by print(), and a single strip leaves that row blank (spec R1/D1,
refuting the backlog item's Part 1).

An index walk, not a trailing-run regex: nothing caps line LENGTH in this file,
so a backtracking pattern is quadratic on a long CR run. M0, M1, M2 and M3 all
die; the interior-CR test at logBuffer.test.ts:118 passes unedited."
```

---

### Task F2: Pin the other three call sites, the empty rows, the ANSI ordering, and render-invisibility

**Files:**
- Test: `web/src/jobs/logBuffer.test.ts` - append four tests. **No production change is expected in this task.**

**These are PINS, and their RED is a mutation, not the absence of code.** Task F1's one-function fix
already makes all four green - which is exactly the property being pinned, because `collapseCR` is
shared by all four call sites. Their job is to make M4 killable: a fix applied at the line-split site
(`logBuffer.ts:144`) instead of inside `collapseCR` passes every test in Task F1 and leaves three of
the four sites broken. **The mutation step is mandatory here, not optional** - without it these tests
are unproven.

- [ ] **Step 1: Write the tests**

Append to `web/src/jobs/logBuffer.test.ts`:

```ts
// The IN-FLIGHT PARTIAL paths (spec T1-C), which are two of collapseCR's four
// call sites and are NOT reached by the line split. A chunk boundary landing
// between the \r and the \n leaves "text\r" in the partial buffer, so the live
// tail flickers blank and then fills in; a task whose final line has no trailing
// newline blanks that line permanently.
//
// BOTH STREAMS ON PURPOSE: visibleRows renders the stdout and stderr partials
// through two SEPARATE call sites (logBuffer.ts:175 and :178), so a stdout-only
// fixture passes against a fix applied to one of them.
test('a partial ending in a carriage return renders its text, on stdout and on stderr', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'text\r', 'stdout'), chunk(2, 'oops\r', 'stderr')])

  expect(visibleRows(s).map((r) => [r.stream, r.kind, r.text])).toEqual([
    ['stdout', 'partial', 'text'],
    ['stderr', 'partial', 'oops'],
  ])

  const f = finalizePartials(s)
  expect(f.lines.map((l) => [l.stream, l.text])).toEqual([
    ['stdout', 'text'],
    ['stderr', 'oops'],
  ])
})

// Spec T1-D. GREEN AT HEAD AND GREEN AFTER THE FIX - a non-regression pin, not a
// RED test, and saying so is the point: an empty line is CONTENT, not an
// absence, and a fix that started dropping rows whose text collapses to '' would
// silently delete blank lines from the log. Mutation M3b is its kill.
test('an empty or CR-only unit still renders a row', () => {
  expect(appendEntries(createLogState(), [chunk(1, '\r\n')]).lines.map((l) => l.text)).toEqual([''])
  expect(appendEntries(createLogState(), [chunk(1, '\n\n')]).lines.map((l) => l.text)).toEqual(['', ''])
  const dangling = appendEntries(createLogState(), [chunk(1, '\r')])
  expect(visibleRows(dangling).map((r) => r.text)).toEqual([''])
})

// Spec T1-E: an erase-line escape sequence sitting BETWEEN the carriage return
// and the newline - which is exactly what a progress bar emits. stripAnsi runs
// before the split (logBuffer.ts:139-142), so the sequence is gone by the time
// the line is formed and the CR it was hiding behind is trailing after all.
//
// Without this test nothing pins that the strip runs AFTER stripAnsi: a strip
// applied to e.content would see '\n' in the final position here and remove
// nothing. The ESC byte is written as an escape rather than literally so this
// test survives being copied out of a plan document.
test('a carriage return revealed by stripping an ANSI erase-line sequence is still stripped', () => {
  const ESC = '\u001B'
  const s = appendEntries(createLogState(), [chunk(1, 'text\r' + ESC + '[K\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['text'])
})

// Spec T3-B, the render-invisibility claim (spec 4.4) made executable. The whole
// reason Part 2 (the agent's CRLF collapse) can ship in the same slice without
// changing what the SPA shows is that an UPGRADED agent's bytes and an
// UN-UPGRADED agent's bytes render identically here. Under a strip-ONE rule they
// would not, which is a second, independent reason not to narrow the rule.
test('an upgraded and an un-upgraded agent render the same line', () => {
  const upgraded = appendEntries(createLogState(), [chunk(1, 'x\r\n')])
  const notUpgraded = appendEntries(createLogState(), [chunk(1, 'x\r\r\n')])
  expect(upgraded.lines.map((l) => l.text)).toEqual(['x'])
  expect(notUpgraded.lines.map((l) => l.text)).toEqual(['x'])
})
```

- [ ] **Step 2: Run the tests**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf/web
npx vitest run src/jobs/logBuffer.test.ts
npm test
```

Expected: **PASS, all four new tests and the whole suite.** They are green because Task F1 fixed the
shared function rather than one call site. **Do not treat this as evidence they work** - go to
Step 3.

- [ ] **Step 3: Mutation battery (isolated detached worktree) - this step IS the RED**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mut-f2 HEAD
Copy-Item web/src/jobs/logBuffer.ts      C:/Users/chadv/AppData/Local/Temp/relay-mut-f2/web/src/jobs/logBuffer.ts
Copy-Item web/src/jobs/logBuffer.test.ts C:/Users/chadv/AppData/Local/Temp/relay-mut-f2/web/src/jobs/logBuffer.test.ts
cd C:/Users/chadv/AppData/Local/Temp/relay-mut-f2/web
npm ci
npx vitest run src/jobs/logBuffer.test.ts
```

Expected: **GREEN baseline** before mutating. Revert each row by re-copying `logBuffer.ts` from the
shared worktree, never by `git checkout --`.

| # | Mutation | How to apply | Applied-check | Expected |
|---|---|---|---|---|
| **M0** control | `collapseCR` -> `return 'SENTINEL'` | as in F1 | grep `SENTINEL` | **MUST DIE** |
| **M4** | Move the fix to the split site: restore `collapseCR` to its HEAD body (`const i = s.lastIndexOf('\r'); return i === -1 ? s : s.slice(i + 1)`) and change `logBuffer.ts:144` to `const raw = stripTrailingCR(buf.slice(0, nl))`, adding a module-local `stripTrailingCR` that does the index walk | grep `stripTrailingCR` in `logBuffer.ts` | **Task F1's test still PASSES** (completed lines are fixed) and the partial test **DIES on all four of its assertions** - both provisional rows and both finalized lines still render `''`. Record that split outcome explicitly: it is the evidence that F1's battery alone was insufficient. |
| **M3b** | in `appendEntries` at `:146`, wrap the push: `const text = collapseCR(raw); if (text !== '') lines.push({ ..., text, ... })` | grep `if (text !== '')` | dies on `'an empty or CR-only unit still renders a row'` (first two assertions) |
| **M-ansi** | move the strip upstream of `stripAnsi`: apply the index walk to `stripAnsi(e.content)`'s ARGUMENT at `:140` (i.e. to the raw `e.content`) and restore `collapseCR`'s HEAD body | grep the changed `:140` | dies on the ANSI test **and** on Task F1's case 1 - record both |

- [ ] **Step 4: Clean up and commit**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
git worktree remove --force C:/Users/chadv/AppData/Local/Temp/relay-mut-f2
```

```bash
git add web/src/jobs/logBuffer.test.ts
git commit -m "test(web): pin the partial paths, the empty rows and the post-stripAnsi ordering

collapseCR has four call sites; the previous commit's test exercises one. M4 -
applying the same fix at the line-split site instead - passes it and still renders
both in-flight partials and the finalize path blank, on both streams. These four
tests are what kills it, plus M3b (an over-eager fix that drops rows whose text
collapses to empty) and the render-invisibility pair that lets an upgraded and an
un-upgraded agent look identical in the SPA."
```

---

# Backend lane

### Task B1: `chunkWriter` holds one byte and collapses CRLF per `Write`

**Files:**
- Create: `internal/agent/runner_crlf_test.go` (package `agent`, **no build tag**)
- Modify: `internal/agent/runner.go` - imports (`:3-17`), the `chunkWriter` type comment and struct (`:270-283`), `Write` (`:285-309`)

**Lane note, confirming spec 9.2's requirement is achievable.** These tests are in the **default `go
test` lane with no build tag**, need **no Docker** and **no subprocess** - they call
`chunkWriter.Write` directly against a `Runner` built by the package-internal `newRunner`.
`internal/agent`'s test files are `package agent` (not `agent_test`), so unexported fields are
assertable, which is what makes the abort-path test (T2-E) deterministic instead of timing-based.
Neither the `//go:build !windows` tag of `runner_cancel_test.go` nor the `runtime.GOOS` switch of
`runner_multistep_test.go` is inherited, and neither may be: a `cmd /c echo` producer emits CRLF
natively on Windows and never on Linux, so a CRLF assertion behind either pattern is vacuously green
on CI and meaningful only on a developer's machine.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/runner_crlf_test.go`:

```go
package agent

import (
	"context"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// drainByStream joins the Content of every TaskLogChunk currently on ch, in FIFO
// order, keyed by stream. It stops when the channel is empty.
//
// THE JOIN IS THE ASSERTION SURFACE ON PURPOSE. The contract chunkWriter has is
// about the CONCATENATION of what one (step, stream) writer emits - "the
// subprocess's bytes with each \r\n OF THE ORIGINAL replaced by \n" - and it is
// deliberately NOT a per-chunk property. A payload of "\r\r" legitimately emits
// a chunk that ends in '\r', and the emitted bytes can contain a \r\n at a
// position that did not have one. Asserting "no chunk contains CRLF" would fail
// on legitimate input; asserting it only on inputs where it happens to hold
// would pass vacuously and pin the wrong contract (spec R5/D6).
func drainByStream(t *testing.T, ch chan *relayv1.AgentMessage) map[relayv1.LogStream]string {
	t.Helper()
	bufs := map[relayv1.LogStream]*strings.Builder{}
	for {
		select {
		case msg := <-ch:
			l := msg.GetTaskLog()
			if l == nil {
				continue
			}
			if bufs[l.Stream] == nil {
				bufs[l.Stream] = &strings.Builder{}
			}
			bufs[l.Stream].Write(l.Content)
		default:
			out := map[relayv1.LogStream]string{}
			for k, v := range bufs {
				out[k] = v.String()
			}
			return out
		}
	}
}

func newCRLFWriter(t *testing.T, stream relayv1.LogStream, capacity int) (*chunkWriter, *Runner, chan *relayv1.AgentMessage) {
	t.Helper()
	sendCh := make(chan *relayv1.AgentMessage, capacity)
	r, _ := newRunner("t-crlf", 0, sendCh, context.Background(), 0)
	return &chunkWriter{r: r, stream: stream, stepIndex: 1, stepTotal: 1}, r, sendCh
}

// TestChunkWriter_StraddledCRLFCollapsesAcrossWriteBoundary is THE
// discriminating test for the whole of Part 2. An entry is not a line:
// chunkWriter copies whatever os/exec hands it, so a CRLF can straddle a Write
// boundary - chunk N ends "\r", chunk N+1 begins "\n" - and a stateless
// bytes.ReplaceAll cannot see that pair.
//
// THE STRADDLED PAIR IS FIRST, and that is load-bearing here in a way it is not
// in the web table: these two Writes share ONE writer and one piece of held
// state, so a discriminating input placed after a benign one cannot detect an
// early-exit mutation.
func TestChunkWriter_StraddledCRLFCollapsesAcrossWriteBoundary(t *testing.T) {
	w, _, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 64)

	for _, p := range [][]byte{[]byte("alpha\r"), []byte("\nbravo\r\ncharlie")} {
		n, err := w.Write(p)
		require.NoError(t, err)
		require.Equal(t, len(p), n, "io.Copy stops with ErrShortWrite on a short write that carries a nil error")
	}

	got := drainByStream(t, sendCh)
	require.Equal(t, "alpha\nbravo\ncharlie", got[relayv1.LogStream_LOG_STREAM_STDOUT],
		"the straddled pair must collapse; a per-Write ReplaceAll leaves the CR that ended the first chunk")
}

// TestChunkWriter_LoneCarriageReturnIsConsumedWithoutEnqueueing is spec T2-B.
// The write of a lone '\r' consumes its byte into the writer's own state and
// enqueues NOTHING - the same thing bufio.Writer does, and not an io.Writer
// violation: io.Writer requires n < len(p) only when a non-nil error is
// returned. Emitting no chunk STRENGTHENS the never-emit-an-empty-chunk
// invariant at runner.go's len(p) == 0 guard rather than breaking it.
func TestChunkWriter_LoneCarriageReturnIsConsumedWithoutEnqueueing(t *testing.T) {
	w, _, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 64)

	n, err := w.Write([]byte("\r"))
	require.NoError(t, err)
	require.Equal(t, 1, n, "reporting fewer than len(p) with a nil error stalls io.Copy")
	require.Len(t, sendCh, 0, "a held byte must not be enqueued as a chunk of its own")

	n, err = w.Write([]byte("\n"))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := drainByStream(t, sendCh)
	require.Equal(t, "\n", got[relayv1.LogStream_LOG_STREAM_STDOUT],
		"the held CR and the following LF are one CRLF pair and collapse to a single LF")
}

// TestChunkWriter_AbandonedWriteDropsTheHeldByte is spec T2-E and D10. A Write
// either emits the held byte or drops it WITH ITS OWN CHUNK - never both, never
// neither-with-a-successor. Arming the new held byte before the enqueue instead
// of after would leave a '\r' owned by a writer whose abort path has already
// decided to stop sending.
//
// Deterministic, not timing-based: sendCh is wedged full BEFORE the write and
// forcedCh is closed, so sendOrAbort has exactly one ready case.
func TestChunkWriter_AbandonedWriteDropsTheHeldByte(t *testing.T) {
	w, r, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 1)
	sendCh <- &relayv1.AgentMessage{} // wedge it full
	r.Cancel(true)

	n, err := w.Write([]byte("abc\r"))
	require.ErrorIs(t, err, errForcedAbort)
	require.Equal(t, 0, n, "an abandoned Write reports zero bytes consumed alongside its error")
	require.Empty(t, w.held,
		"the held byte is discarded with the abandoned chunk; arming it before the enqueue re-emits it after the abort")
	require.Len(t, sendCh, 1, "nothing may be enqueued once the chunk is abandoned")
}

// TestChunkWriter_StdoutAndStderrHoldIndependently is spec T2-F. os/exec drives
// each of the two writers from its own copy goroutine and they are distinct
// pointers, so hold-back state is PER WRITER. This is the test that catches a
// "hoist held onto the Runner" refactor, which would prepend one stream's held
// carriage return to the other stream's next chunk.
func TestChunkWriter_StdoutAndStderrHoldIndependently(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	r, _ := newRunner("t-two-streams", 0, sendCh, context.Background(), 0)
	out := &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDOUT, stepIndex: 1, stepTotal: 1}
	errw := &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDERR, stepIndex: 1, stepTotal: 1}

	// The stderr write lands BETWEEN stdout's hold and its release. That is the
	// whole fixture: with shared state, stdout's held '\r' arrives on stderr.
	for _, step := range []struct {
		w *chunkWriter
		p string
	}{
		{out, "out\r"},
		{errw, "err\r\nline\n"},
		{out, "put\r\n"},
	} {
		n, err := step.w.Write([]byte(step.p))
		require.NoError(t, err)
		require.Equal(t, len(step.p), n)
	}

	got := drainByStream(t, sendCh)
	require.Equal(t, "err\nline\n", got[relayv1.LogStream_LOG_STREAM_STDERR],
		"stderr must never see stdout's held byte")
	// The interior '\r' SURVIVES: its successor is 'p', not '\n', so it is not
	// part of a CRLF pair and this transform does not touch it. Collapsing it
	// would be a judgement about visible content, which stays in the client.
	require.Equal(t, "out\rput\n", got[relayv1.LogStream_LOG_STREAM_STDOUT])
}
```

- [ ] **Step 2: Run tests to verify they fail**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
go test ./internal/agent/... -run TestChunkWriter -v -timeout 60s
```

Expected: **compile failure**, not a test failure:

```
internal\agent\runner_crlf_test.go:NN:NN: w.held undefined (type *chunkWriter has no field or method held)
```

To see the behavioural REDs as well, temporarily comment out only the `require.Empty(t, w.held, ...)`
line and re-run: `"alpha\r\nbravo\r\ncharlie"` against `"alpha\nbravo\ncharlie"`, a `"\r"` chunk
enqueued by the lone-CR write, and `"err\r\nline\n"` on stderr. **Restore the line before Step 3.**

- [ ] **Step 3: Write minimal implementation**

Add `"bytes"` to the import block at `internal/agent/runner.go:3-17`.

Replace the `chunkWriter` type comment and struct (`:270-283`) with:

```go
// chunkWriter is the io.Writer exec copies subprocess stdout/stderr into. Each
// Write builds its own buffer (exec reuses the slice between calls), collapses
// CRLF to LF in it, wraps it in a TaskLogChunk stamped with the runner's
// stream/step/epoch, and pushes it through r.sendOrAbort.
//
// THE CRLF COLLAPSE IS EXACTLY \r\n -> \n, NOT \r+\n -> \n. The wider rule would
// also remove the residue a CR-run before a newline leaves behind, but that is a
// judgement about what is VISIBLE, which is a rendering decision, and rendering
// decisions stay in the client that holds the opinion - the SPA already collapses
// carriage returns (web/src/jobs/logBuffer.ts) and the CLI deliberately wants the
// raw bytes. The narrow rule has one definition every consumer agrees on and one
// statable cost: precisely the CR of each CRLF is removed, nothing else.
//
// THE INVARIANT IS OVER THE CONCATENATION, AGAINST THE ORIGINAL BYTE POSITIONS:
// for one (step, stream) writer, the concatenation of every payload it emits
// equals the subprocess's bytes with each \r\n OF THE ORIGINAL replaced by \n.
// It is NOT the per-chunk property "no emitted chunk contains a CRLF", which is
// false on purpose: a payload of "\r\r" emits a chunk ending in '\r' (correct -
// that CR's successor is known to be another CR, not a LF), and a CR-run before
// a newline emits a CRLF at a position that did not have one. A second pass would
// be a different transform; see above.
//
// held is at most ONE byte, always: a trailing '\r' whose successor has not been
// read yet. A CRLF can straddle a Write boundary - exec hands over whatever the
// pipe had - so without the hold-back a per-Write replace silently misses every
// pair split across two reads. The held byte is folded into the next Write's
// buffer BEFORE the scan, so no pair straddles by the time the scan runs. mu
// guards held; see flush for why the lock is insurance rather than correctness.
//
// Write has THREE outcomes, not two. On a successful enqueue it returns
// (len(p), nil) so exec keeps copying until EOF (unchanged slow-consumer
// behavior). On a payload consumed entirely into held - which happens on exactly
// one input, a lone "\r" with nothing already held - it returns (len(p), nil)
// having enqueued NOTHING; that is what bufio.Writer does, io.Writer requires
// n < len(p) only alongside a non-nil error, and emitting no chunk strengthens
// the never-emit-an-empty-chunk guard below rather than breaking it. If a
// per-task cancel has closed r.forcedCh or r.cancelledCh (or the agent context
// is done), the enqueue is abandoned and Write returns errForcedAbort so exec's
// io.Copy stops and cmd.Wait() returns promptly instead of waiting out
// WaitDelay; the held byte goes with the discarded chunk and is never re-armed.
type chunkWriter struct {
	r         *Runner
	stream    relayv1.LogStream
	stepIndex int32
	stepTotal int32

	mu   sync.Mutex
	held []byte // at most one byte: a trailing '\r' whose successor is not yet known
}
```

Replace `Write` (`:285-309`) with:

```go
func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil // match the old pipeLog n>0 guard: never emit an empty chunk
	}

	// Fold the held byte in and CLEAR it in the same breath. Its lifetime is
	// exactly one Write, and newHeld below is armed only after a successful
	// enqueue, so no early return - including the errForcedAbort one the abort
	// path takes - can leave a byte owned by a writer nobody will call again.
	w.mu.Lock()
	chunk := make([]byte, 0, len(w.held)+len(p))
	chunk = append(chunk, w.held...)
	chunk = append(chunk, p...)
	w.held = nil
	w.mu.Unlock()

	var newHeld []byte
	if chunk[len(chunk)-1] == '\r' {
		newHeld = []byte{'\r'}
		chunk = chunk[:len(chunk)-1]
	}
	chunk = collapseCRLF(chunk)

	if len(chunk) == 0 {
		// Reachable on exactly one input: nothing held and p == "\r".
		// collapseCRLF keeps a byte for every pair it collapses, so only the
		// hold-back above can empty the buffer.
		w.mu.Lock()
		w.held = newHeld
		w.mu.Unlock()
		return len(p), nil
	}

	if !w.r.sendOrAbort(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{
				TaskId:    w.r.taskID,
				Stream:    w.stream,
				Content:   chunk,
				Epoch:     w.r.epoch,
				StepIndex: w.stepIndex,
				StepTotal: w.stepTotal,
			},
		},
	}) {
		// Abandoned. On a forced cancel this stops io.Copy so cmd.Wait returns.
		// On agent shutdown (ctx.Done) returning the sentinel is equally fine:
		// the runner is tearing down regardless. newHeld is deliberately NOT
		// armed - the writer must not send after the abort path decided to stop.
		return 0, errForcedAbort
	}
	w.mu.Lock()
	w.held = newHeld
	w.mu.Unlock()
	return len(p), nil
}

// collapseCRLF removes the CR of every CRLF pair in b IN PLACE and returns the
// shortened prefix. b must be a buffer the caller owns - chunkWriter.Write
// already has to copy exec's slice, so no second allocation is needed. The
// compaction only ever skips bytes, so the write index never overtakes the read
// index.
//
// The IndexByte fast path is not a micro-optimisation for its own sake: it is
// every write on every Linux agent, where a full forward scan would buy nothing.
func collapseCRLF(b []byte) []byte {
	i := bytes.IndexByte(b, '\r')
	if i < 0 {
		return b
	}
	out := b[:i]
	for ; i < len(b); i++ {
		if b[i] == '\r' && i+1 < len(b) && b[i+1] == '\n' {
			continue
		}
		out = append(out, b[i])
	}
	return out
}
```

`sync` is already imported (`runner.go:11`).

**On the mutex (D9).** `Write` runs on exec's copy goroutine and `flush()` (Task B2) runs on the
`Run` goroutine. `Cmd.Wait`'s documented join means they cannot overlap - but that is a subtle
`os/exec` property and this repo's `-race` lane is not runnable locally, so the lock converts a
three-paragraph argument into one line at the cost of one uncontended lock per 32 KiB of subprocess
output. `makePrepareProgressFn` (`runner.go:377-411`) already uses exactly this pattern for exactly
this reason. **The lock is never held across `sendOrAbort`.**

- [ ] **Step 4: Run tests to verify they pass**

```powershell
go test ./internal/agent/... -run TestChunkWriter -v -timeout 60s
```

Expected: PASS, 4 tests.

```powershell
go test ./internal/agent/... -timeout 120s
go build ./...
```

Expected: PASS. Watch `TestRunner_MultiStepAllSucceed` and its neighbours - on Windows they join
stdout produced by `cmd /c echo`, which was CRLF and is now LF. They assert with `Contains` on
substrings that carry no `\r`, so they should be unaffected; **if one fails, read it before editing
it** - it is a real behaviour change, not a fixture to adjust.

- [ ] **Step 5: Mutation battery (isolated detached worktree)**

Create and **seed** the mutant tree - a bare `HEAD` worktree does not contain Step 3's implementation:

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mut-b1 HEAD
Copy-Item internal/agent/runner.go           C:/Users/chadv/AppData/Local/Temp/relay-mut-b1/internal/agent/runner.go
Copy-Item internal/agent/runner_crlf_test.go C:/Users/chadv/AppData/Local/Temp/relay-mut-b1/internal/agent/runner_crlf_test.go
cd C:/Users/chadv/AppData/Local/Temp/relay-mut-b1
go test ./internal/agent/... -run TestChunkWriter -v -timeout 60s
```

Expected: **GREEN baseline**, 4 tests. If not, the seed copy failed - fix that before mutating.

For each row: edit `internal/agent/runner.go` in the mutant tree, run `go build ./...`, confirm the
mutation applied with the named grep, run the test command, record, then **re-copy `runner.go` from
the shared worktree** to revert. A mutation that fails to compile is not a behavioural kill - fix the
mutation and re-run.

| # | Mutation | Applied-check | Expected |
|---|---|---|---|
| **M0b** control | make `Write` enqueue nothing: `return len(p), nil` as the first statement after the `len(p) == 0` guard | `Select-String internal/agent/runner.go -Pattern "return len\(p\), nil"` shows an extra hit near the top of `Write` | **MUST DIE** on the straddle test (drained content is `""`). A survival means the harness is broken - stop. |
| M5 | replace the hold-back with a stateless per-`Write` replace: delete the `held` fold and the trailing-`\r` capture, keep `chunk = collapseCRLF(chunk)` | grep shows no `w.held` inside `Write` | **dies on the straddle test** (`"alpha\r\nbravo\ncharlie"`). If it survives, the fixture is not actually straddled - fix the fixture, not the code. |
| M9 | move `w.held = newHeld` from AFTER the `sendOrAbort` to BEFORE it | grep the reordered block | dies on `TestChunkWriter_AbandonedWriteDropsTheHeldByte`'s `require.Empty(t, w.held)` |
| M11 | hoist `held`/`mu` off `chunkWriter` onto `Runner` and address them as `w.r.held`/`w.r.mu` | grep `w.r.held` | dies on `TestChunkWriter_StdoutAndStderrHoldIndependently`, on **both** assertions |

- [ ] **Step 6: Clean up and commit**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
git worktree remove --force C:/Users/chadv/AppData/Local/Temp/relay-mut-b1
```

```bash
git add internal/agent/runner.go internal/agent/runner_crlf_test.go
git commit -m "feat(agent): chunkWriter collapses CRLF to LF with a one-byte straddle hold-back

An entry is not a line: exec hands over whatever the pipe had, so a CRLF can
straddle a Write boundary and a stateless ReplaceAll cannot see that pair. One
held byte, folded into the next Write before the scan, makes one pass exact.

The invariant is over the CONCATENATION against the ORIGINAL byte positions, not
per chunk: a CR-run before a newline correctly emits a CRLF at a new position, so
the item's 'chunks containing no CRLF' criterion is false and is deliberately not
tested (spec R5/D6). Exactly \r\n -> \n; the residue is the SPA's job. M0b, M5,
M9 and M11 all die."
```

---

### Task B2: `flush()` and its per-step call site, wired at a real `Run`

**Files:**
- Modify: `internal/agent/runner_crlf_test.go` (append; adds imports `os`, `time`)
- Modify: `internal/agent/runner.go` - a new `flush` method after `Write`, one paragraph added to the `chunkWriter` type comment, `sendOrAbort`'s comment (`:350-354`), and the per-step writer construction plus the line after `cmd.Wait()` (`:202-215`)

> **STANDING WARNING FOR THIS TASK.** The backlog item asks for a test that "a subprocess emitting
> `\r\n` produces chunks containing no `\r\n`". **Do not write it.** It is false on legitimate input
> and a fixture narrowed until it holds pins the wrong contract (spec R5). Every assertion below is
> an equality against the expected transform of the whole concatenation.

- [ ] **Step 1: Write the failing tests**

Append to `internal/agent/runner_crlf_test.go`, adding `"os"` and `"time"` to its import block:

```go
// TestCRLFHelperProcess IS NOT A TEST. It is the SUBPROCESS the two wiring tests
// exec: the test binary re-executes itself with -test.run pointed here, so the
// producer is Go code writing exact bytes and the same bytes reach the runner on
// Windows and on Linux. Go performs no newline translation.
//
// A SHELL PRODUCER CANNOT CARRY THIS ASSERTION. `cmd /c echo` emits CRLF
// natively on Windows and never on Linux, so a CRLF assertion behind a
// runtime.GOOS switch is vacuously green on CI and meaningful only on a
// developer's machine - the platform-gated-verification trap, inverted. That is
// why this file carries no build tag and no GOOS switch (spec D15).
//
// os.Exit(0) IS NOT OPTIONAL: without it the testing framework appends "PASS\nok
// ..." to the very stdout the parent asserts on. Nothing is written before the
// test body runs in non-verbose mode, and a raw test binary does not read
// GOFLAGS (that is the go command's variable), so the child's stdout is exactly
// what this function writes.
func TestCRLFHelperProcess(t *testing.T) {
	mode := os.Getenv("RELAY_CRLF_HELPER")
	if mode == "" {
		return // an ordinary test run; this process is not the helper
	}
	switch mode {
	case "crlf":
		// Two CRLF pairs and a bare trailing CR. The middle CR-CR-LF is the
		// shape the whole slice turns on.
		_, _ = os.Stdout.Write([]byte("a\r\nb\r\r\nc\r"))
	case "trailing-cr":
		_, _ = os.Stdout.Write([]byte("step-one\r"))
	}
	os.Exit(0)
}

// crlfHelperCmd returns the argv and env that re-exec this test binary as the
// helper above. os.Args[0] under `go test` is the built test binary, an absolute
// path. The sentinel travels through DispatchTask.Env, which Run merges into the
// child's environment (runner.go:155-161), so the parent's own environment is
// never mutated.
func crlfHelperCmd(mode string) ([]string, map[string]string) {
	return []string{os.Args[0], "-test.run=^TestCRLFHelperProcess$"},
		map[string]string{"RELAY_CRLF_HELPER": mode}
}

// TestRunner_CRLFFlushIsWiredAndPrecedesTheTerminalStatus is spec T2-C, and it
// is what makes M6 and M7 killable: unit-testing flush() directly, or asserting
// that the method exists, proves nothing about the CALL SITE.
func TestRunner_CRLFFlushIsWiredAndPrecedesTheTerminalStatus(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	argv, env := crlfHelperCmd("crlf")
	r, runCtx := newRunner("t-crlf-wire", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "t-crlf-wire",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	})

	msgs := collectMessages(sendCh, 1500*time.Millisecond)
	require.NotEmpty(t, msgs)

	// Everything the step emitted BEFORE the terminal status, in FIFO order.
	// internal/worker/handler.go:173-178 bounds AppendTaskLog's trailing window
	// on exactly this ordering: a chunk enqueued before sendFinalStatus cannot
	// outlive the terminal status. A flush hoisted past the loop makes that
	// sentence false and pushes a one-byte chunk into the trailing-window
	// carve-out instead of the status allow-list.
	term := -1
	for i, m := range msgs {
		if ts := m.GetTaskStatus(); ts != nil {
			switch ts.Status {
			case relayv1.TaskStatus_TASK_STATUS_DONE,
				relayv1.TaskStatus_TASK_STATUS_FAILED,
				relayv1.TaskStatus_TASK_STATUS_TIMED_OUT:
				term = i
			}
		}
	}
	require.GreaterOrEqual(t, term, 0, "the task must reach a terminal status")

	var before strings.Builder
	for _, m := range msgs[:term] {
		if l := m.GetTaskLog(); l != nil && l.Stream == relayv1.LogStream_LOG_STREAM_STDOUT {
			before.Write(l.Content)
		}
	}
	// Drop the synthetic step marker, which is always the first stdout content
	// and is terminated by the first '\n' in the joined stream.
	_, payload, ok := strings.Cut(before.String(), "\n")
	require.True(t, ok, "expected the step marker line then the subprocess bytes; got %q", before.String())

	// THE EXPECTED VALUE CONTAINS A CR-LF AND THAT IS NOT A TYPO. The transform is
	// defined on the ORIGINAL byte positions. The input "a\r\nb\r\r\nc\r" has two
	// CRLF pairs; removing both leaves "a\nb" + "\r" + "\nc" - a CRLF at a
	// position that did not have one. "Correcting" this to "a\nb\nc\r" silently
	// changes the design to \r+\n -> \n, which the spec rejects on purpose (6.2):
	// that is a judgement about visible content, and visible-content judgements
	// stay in the client that holds the opinion. The residue is removed by
	// web/src/jobs/logBuffer.ts, which strips ALL trailing carriage returns.
	//
	// The trailing "c\r" is the FLUSHED held byte. Swallowing it would be silent
	// loss - Write would have reported a byte consumed that appears nowhere - and
	// it would break the concatenation invariant.
	require.Equal(t, "a\nb\r\nc\r", payload)
}

// TestRunner_HeldCarriageReturnIsFlushedBeforeTheNextStepMarker is spec T2-D and
// it pins the flush call site as PER STEP. The writers are constructed fresh
// inside the command loop and become garbage at the end of each iteration, so a
// flush hoisted to the end of Run finds step 1's writer already replaced and
// loses its byte outright.
func TestRunner_HeldCarriageReturnIsFlushedBeforeTheNextStepMarker(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	argv, env := crlfHelperCmd("trailing-cr")
	r, runCtx := newRunner("t-crlf-steps", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "t-crlf-steps",
		Commands: []*relayv1.CommandLine{
			{Argv: argv},
			{Argv: echoArgv("second")},
		},
		Env: env,
	})

	joined := collectStdoutLogs(collectMessages(sendCh, 2500*time.Millisecond))

	held := strings.Index(joined, "step-one\r")
	require.GreaterOrEqual(t, held, 0,
		"step 1's held trailing CR was never flushed; logs:\n%q", joined)
	marker2 := strings.Index(joined, "=== relay step 2/2")
	require.GreaterOrEqual(t, marker2, 0, "step 2 must have run; logs:\n%q", joined)
	require.Less(t, held, marker2,
		"the held byte must be enqueued before the next step's marker")
}

// TestChunkWriter_FlushIsBoundedByTheCancelChannels is spec T2-G / D8. flush()
// runs after cmd.Wait() on the CANCEL path too, and r.send is bounded only by
// the AGENT context - not the run context - so a per-task cancel with a wedged
// sendCh would park Run until agent shutdown. That is precisely the wedge
// sendFinalStatus's cancelled branch and sendInventory were both written to
// avoid, and it would delay the terminal status indefinitely.
//
// The 2s bound is not a timing assertion in disguise: correct code returns in
// microseconds and the mutant parks FOREVER, so there is no margin to tune.
func TestChunkWriter_FlushIsBoundedByTheCancelChannels(t *testing.T) {
	w, r, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 2)

	n, err := w.Write([]byte("abc\r"))
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Len(t, w.held, 1, "the trailing CR must be held for the next write")

	for len(sendCh) < cap(sendCh) {
		sendCh <- &relayv1.AgentMessage{} // wedge it full
	}
	r.Cancel(false) // closes cancelledCh; r.ctx is Background and stays live

	done := make(chan struct{})
	go func() { defer close(done); w.flush() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flush parked on a wedged sendCh: it must use sendOrAbort, not the agent-context-only r.send")
	}
	require.Len(t, sendCh, cap(sendCh), "an abandoned flush must enqueue nothing")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```powershell
go test ./internal/agent/... -run "TestRunner_CRLF|TestRunner_HeldCarriage|TestChunkWriter_Flush" -v -timeout 60s
```

Expected: **compile failure**:

```
internal\agent\runner_crlf_test.go:NN:NN: w.flush undefined (type *chunkWriter has no field or method flush)
```

- [ ] **Step 3: Add the `flush()` method ONLY - not the call site yet**

Add after `Write` in `internal/agent/runner.go`:

```go
// flush enqueues any held trailing '\r' as its own chunk and clears it.
//
// IT MUST BE CALLED EXPLICITLY, IMMEDIATELY AFTER cmd.Wait() RETURNS, FOR BOTH
// WRITERS, INSIDE THE PER-STEP LOOP. os/exec closes only the pipes it created
// and never calls Close on a caller-supplied Stdout/Stderr, so there is no close
// hook and naming this Close() would imply a call that does not happen. The
// writers are per STEP, so a byte not flushed before the iteration ends is
// silently lost - no error, no log line - and a flush deferred to the end of Run
// would find the step's writer already replaced. See makePrepareProgressFn's
// flush, which is the same shape and the same hazard.
//
// SENDS THROUGH sendOrAbort, NEVER r.send. flush runs after cmd.Wait on the
// cancel path too, and r.send is bounded only by the AGENT context, so a
// per-task forced cancel with a wedged sendCh would park Run until agent
// shutdown - the exact wedge sendFinalStatus's cancelled branch and sendInventory
// were both written to avoid. After an abandoned Write there is nothing held, so
// this sends nothing: the writer must not send after the abort path decided to
// stop sending.
//
// The lock is released before the send. Holding it across a bounded-but-slow
// enqueue is the first step toward the lock-scope problems the Invariants exist
// to prevent.
func (w *chunkWriter) flush() {
	w.mu.Lock()
	held := w.held
	w.held = nil
	w.mu.Unlock()
	if len(held) == 0 {
		return
	}
	w.r.sendOrAbort(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{
				TaskId:    w.r.taskID,
				Stream:    w.stream,
				Content:   held,
				Epoch:     w.r.epoch,
				StepIndex: w.stepIndex,
				StepTotal: w.stepTotal,
			},
		},
	})
}
```

- [ ] **Step 4: Run the tests - one passes, two fail, and THAT is the proof the call site matters**

```powershell
go test ./internal/agent/... -run "TestRunner_CRLF|TestRunner_HeldCarriage|TestChunkWriter_Flush" -v -timeout 60s
```

Expected:

- `TestChunkWriter_FlushIsBoundedByTheCancelChannels` **PASSES** (the method exists and is bounded).
- `TestRunner_CRLFFlushIsWiredAndPrecedesTheTerminalStatus` **FAILS**:
  `Not equal: expected: "a\nb\r\nc\r", actual: "a\nb\r\nc"` - the held byte was never flushed.
- `TestRunner_HeldCarriageReturnIsFlushedBeforeTheNextStepMarker` **FAILS**:
  `step 1's held trailing CR was never flushed`.

**Record this output.** It is the live demonstration that M6 (deleting the call site while keeping
the method) is killed, which is the whole reason the wiring test exists.

- [ ] **Step 5: Add the call site**

In `internal/agent/runner.go`, replace `:202-203`:

```go
		outW := &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDOUT, stepIndex: step, stepTotal: stepTotal}
		errW := &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDERR, stepIndex: step, stepTotal: stepTotal}
		cmd.Stdout = outW
		cmd.Stderr = errW
```

and insert immediately after `waitErr := cmd.Wait()` (`:215`), before `cleanupProcTree()`:

```go
		// Flush each writer's held trailing '\r' HERE, and nowhere else. Four
		// constraints have exactly one satisfying position:
		//   - the writers are per STEP and become garbage at the end of this
		//     iteration, and both the `continue` and the `break` below come after
		//     this line, so every path flushes;
		//   - a held byte must be enqueued before the NEXT step's sendStepMarker,
		//     which is at the top of the next iteration;
		//   - a log chunk must be enqueued before sendFinalStatus (after the
		//     loop), or internal/worker/handler.go:173-178's FIFO argument
		//     becomes false and a one-byte chunk lands in AppendTaskLog's
		//     trailing-window carve-out instead of its status allow-list;
		//   - no copy goroutine may still be running: Cmd.Wait waits for the
		//     command to exit AND for the copying from stdout and stderr to
		//     complete, and WaitDelay force-closes the pipes so those copies do
		//     complete.
		// A no-op when nothing is held, so the two earlier breaks (nil argv,
		// cmd.Start failure) need no reasoning about.
		outW.flush()
		errW.flush()
```

Add one paragraph to the `chunkWriter` type comment written in Task B1, immediately before the
`type chunkWriter struct` line:

```go
// flush() MUST be called after cmd.Wait() returns for the step that created this
// writer, for BOTH writers, or a trailing '\r' at the end of a step is silently
// dropped. exec will not call it; see flush's own comment.
```

And amend `sendOrAbort`'s comment (`:353-354`). The sentence "Only `chunkWriter.Write` uses this; all
other callers use send so their blocking discipline is unchanged" is now false. Replace it with:

```go
// Both of chunkWriter's enqueue paths use this - Write and flush; all other
// callers use send, so their blocking discipline is unchanged. flush is the
// second caller BECAUSE it runs after cmd.Wait on the cancel path too, where
// r.send (bounded only by the agent context) would park Run until agent
// shutdown.
```

- [ ] **Step 6: Confirm the `Cmd.Wait` join sentence against the pinned toolchain**

Spec 13.1 asks the plan not to trust the spec's own paraphrase of `os/exec`. Run:

```powershell
go doc os/exec.Cmd.Wait
```

Expected: the output contains "Wait waits for the command to exit and waits for any copying to stdin
or copying from stdout or stderr to complete." (Go 1.26.2, per `go.mod:3`.) If that sentence is
absent or weaker in this toolchain, **do not change the code** - D9's mutex makes the slice correct
either way - but record the discrepancy in the commit body and soften the call-site comment's fourth
bullet so it does not overclaim.

- [ ] **Step 7: Run tests to verify they pass**

```powershell
go test ./internal/agent/... -run "TestRunner_CRLF|TestRunner_HeldCarriage|TestChunkWriter" -v -timeout 60s
go test ./internal/agent/... -timeout 120s
go test ./... -timeout 120s
```

Expected: PASS all three. The last is the whole default lane.

- [ ] **Step 8: Mutation battery (isolated detached worktree)**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mut-b2 HEAD
Copy-Item internal/agent/runner.go           C:/Users/chadv/AppData/Local/Temp/relay-mut-b2/internal/agent/runner.go
Copy-Item internal/agent/runner_crlf_test.go C:/Users/chadv/AppData/Local/Temp/relay-mut-b2/internal/agent/runner_crlf_test.go
cd C:/Users/chadv/AppData/Local/Temp/relay-mut-b2
go test ./internal/agent/... -run "TestRunner_CRLF|TestRunner_HeldCarriage|TestChunkWriter" -v -timeout 60s
```

Expected: **GREEN baseline**, 7 tests. Revert each row by re-copying `runner.go` from the shared
worktree, never by `git checkout --`. Applied-check for every row: `Select-String -Path
internal/agent/runner.go -Pattern "<distinctive new text>"` **plus** a clean `go build ./...`.

| # | Mutation | Expected |
|---|---|---|
| **M0** control | in `flush`, `Content: held` -> `Content: []byte("SENTINEL")` | **MUST DIE** on T2-C's `require.Equal` (`"a\nb\r\ncSENTINEL"`). Survival means the harness is broken - stop. |
| M6 | delete the two `outW.flush()` / `errW.flush()` lines, keep the method | dies on T2-C (`"a\nb\r\nc"`) **and** on T2-D. This is the silent-loss shape: no error, no log line. |
| M7 | move both flush calls out of the loop, to immediately after `r.sendFinalStatus(...)` (`:240`) | dies on T2-C's ordering (the byte lands after the terminal status, so the pre-terminal payload is `"a\nb\r\nc"`) **and** on T2-D (step 1's writer is already replaced). This is the row that pins `handler.go:173-178`. |
| M8 | `flush` uses `w.r.send(...)` instead of `w.r.sendOrAbort(...)` | dies on `TestChunkWriter_FlushIsBoundedByTheCancelChannels`, by the 2s bound against an infinite park. **The spec permitted declaring this unkilled; it is killable deterministically and this test is the kill.** |
| M10 | in `flush`, drop the byte instead of sending it: return immediately after clearing `held` | dies on T2-C (`"a\nb\r\nc"`) |
| M12 | second collapse pass: after `chunk = collapseCRLF(chunk)`, add `chunk = collapseCRLF(chunk)` again (i.e. `\r+\n -> \n`) | **dies on T2-C's expected value** (produces `"a\nb\nc\r"`). This row exists to prove the embedded CR-LF in that expected value is load-bearing and not a typo a reviewer should "fix". |
| **M13** | delete the `sync.Mutex` field and its Lock/Unlock pairs | **NOTHING DIES. Declared unkillable on the record** (spec 6.4/M13): `Cmd.Wait`'s join means no deterministic test can observe the race, and the local `-race` lane does not run. **Do not invent a flaky concurrency test to manufacture a kill.** What stands in: this row, the doc comment, and CI's `go test -race`. Apply it, confirm the suite is green, record the survival, revert. |

- [ ] **Step 9: Clean up and commit**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
git worktree remove --force C:/Users/chadv/AppData/Local/Temp/relay-mut-b2
```

```bash
git add internal/agent/runner.go internal/agent/runner_crlf_test.go
git commit -m "feat(agent): flush the held trailing CR after cmd.Wait, per step, before the terminal status

os/exec never closes a caller-supplied Stdout, so there is no close hook and the
flush has to be an explicit call - named flush(), not Close(), so nobody assumes
exec makes it. Its position satisfies four constraints at once and no other
position satisfies all four: per-step lifetime, ahead of the next step marker,
ahead of sendFinalStatus (internal/worker/handler.go:173-178 depends on that
ordering), and after the copy goroutines have joined.

Sends through sendOrAbort, never r.send, which is bounded only by the agent
context. M0, M6, M7, M8, M10 and M12 all die. M13 (removing the mutex) survives
and is declared unkillable: the Cmd.Wait join means no deterministic test can see
it, and -race is a CI-only gate here."
```

---

### Task B3: The FIFO clause gains a second site, and the foreclosed byte-exact export

**Files:**
- Modify: `internal/worker/handler.go:173-178`
- Modify: `docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md`

Two small prose edits, in one commit. Neither is optional: wrong prose about correct code is this
project's dominant defect class, and both of these are read by someone deciding what to build next.

- [ ] **Step 1: `internal/worker/handler.go` - the FIFO argument gains a second enqueue site**

The sentence at `:173-176` stays **TRUE** under this slice's call-site choice; what changes is that a
second statement now depends on it. Replace:

```go
// The reachable case is narrower than "any queued chunk". chunkWriter's writes
// are enqueued before sendFinalStatus and sendCh is FIFO, so a chunk ordered
// BEFORE the terminal status cannot outlive it - by the time the status has
// landed, that chunk already has.
```

with:

```go
// The reachable case is narrower than "any queued chunk". chunkWriter's writes
// AND its flush (the held trailing '\r', enqueued right after cmd.Wait inside
// the per-step loop - internal/agent/runner.go) are both enqueued before
// sendFinalStatus, and sendCh is FIFO, so a chunk ordered BEFORE the terminal
// status cannot outlive it - by the time the status has landed, that chunk
// already has. THAT IS A CONSTRAINT ON WHERE THE FLUSH MAY BE CALLED, not an
// observation about it: moving the flush after sendFinalStatus makes this
// sentence false and pushes a one-byte chunk out of AppendTaskLog's status
// allow-list and into the trailing-window carve-out this constant bounds.
```

- [ ] **Step 2: `docs/backlog/idea-2026-08-09-...` - annotate the foreclosed guarantee**

The conductor ruled spec 13.4 **in scope**. Site it where a reader of that item actually hits it -
inside proposal **(3) Log export/download** (`:40-44`), not in Notes, because that bullet is the
paragraph an export spec would be written from. Append to it, keeping the list indentation:

```markdown
   **A byte-exact export is foreclosed as of 2026-08-25.** The agent normalises `\r\n` to `\n`
   before a chunk is ever sent (`docs/superpowers/specs/2026-08-25-windows-crlf-log-lines.md`,
   Part 2), so stored bytes are no longer a byte-exact copy of the subprocess output. CRLF-vs-LF is
   not information anyone will want back and the trade was taken deliberately - but do not write
   this piece against a byte-exactness guarantee that no longer holds.
```

Do not change that item's `status`, `priority`, or any other frontmatter, and do not touch its
Acceptance section. This is an annotation, not a resolution.

- [ ] **Step 3: Verify no other site claims byte-exactness**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
Select-String -Path README.md,ROADMAP.md -Pattern "byte-exact|byte for byte|verbatim"
Select-String -Path internal/worker/*.go,internal/api/*.go,internal/store/query/*.sql -Pattern "byte-exact"
```

Expected: **no hits.** Spec 8.5 predicts this. If a hit appears, it is a missed prose site - report it
to the conductor rather than silently expanding scope.

- [ ] **Step 4: Build and commit**

```powershell
go build ./...
go test ./internal/worker/... -timeout 120s
```

Expected: PASS (comment-only change; the build is the real check).

```bash
git add internal/worker/handler.go docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md
git commit -m "docs: the FIFO ordering argument now has two enqueue sites, and an export is no longer byte-exact

handler.go's trailing-window comment justified its bound on chunkWriter's writes
being enqueued before sendFinalStatus. flush() is a second such site, and the same
sentence is what constrains where it may be called - state it as a constraint, not
an observation. And the log-export idea now says so where its author will read it:
the agent's CRLF collapse means stored bytes are not a byte-exact copy."
```

---

### Task C1: Whole-slice verification and backlog close (conductor)

- [ ] **Step 1: Run every gate this machine can run**

```powershell
cd D:/dev/relay/.claude/worktrees/windows-crlf-log-blank-c488bf
go build ./...
go test ./... -timeout 120s
go vet -tags integration ./...
cd web
npm test
```

Expected: all PASS. `go vet -tags integration` catches shared-signature breaks the default lane never
compiles; it needs no Docker.

**`go test -race` is not runnable here** (environmental ThreadSanitizer allocation failure). CI's
`go-ci` job runs `go test -race ./... -timeout 180s` on Linux and **is the only detector for D9's
mutex**. Say so in the PR body. `web-ci` runs `npx tsc -b`, `npm test`, the production SPA build and
Playwright; **no Playwright spec is added by this slice** (spec 11: the transform is a pure string
function with no layout component, so a browser lane would assert nothing extra - the browser lane
earns its slot when layout is the claim, and it is not).

- [ ] **Step 2: Confirm T3-A explicitly**

T3-A is the criterion that Part 1's tests pass unchanged after Part 2 lands, so that nobody later
deletes a `\r`-bearing web test on the grounds that "the agent handles it now" (D13). It is
structurally guaranteed here - the two lanes share no file - but assert it:

```powershell
git diff --stat main -- web/src/jobs/logBuffer.test.ts
git diff main -- web/src/jobs/logBuffer.test.ts | Select-String -Pattern "^-" | Select-String -NotMatch "^---"
```

Expected: **additions only.** The second command must print nothing: zero deleted or modified lines,
so the interior-CR test at `logBuffer.test.ts:118-122` is byte-identical (T1-B).

- [ ] **Step 3: Confirm the working tree is exactly the expected file set**

```powershell
git diff --name-only main
git worktree list
```

Expected, and nothing else:

```
docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md
docs/superpowers/plans/2026-08-25-windows-crlf-log-lines.md
docs/superpowers/specs/2026-08-25-windows-crlf-log-lines.md
internal/agent/runner.go
internal/agent/runner_crlf_test.go
internal/worker/handler.go
web/src/jobs/logBuffer.test.ts
web/src/jobs/logBuffer.ts
```

**No `web/dist/`** (a stray frontend build dirties the tracked `web/dist/index.html`), no
`*.tsbuildinfo`, no `internal/store/`, no `*.sql.go`, no `README.md`, no `ROADMAP.md`. `git worktree
list` must show no surviving `relay-mut-*` trees.

- [ ] **Step 4: Close the backlog item**

Run `/backlog close windows-crlf-log-lines-render-blank`. Never hand-edit the item's `status`.

The Resolution note must record what the spec refuted, because the item's own text still argues for
the narrower rule and a future reader will find it:

- **The item's Part 1 design was refuted and the fix is strip-ALL, not strip-one.** A line ending in
  two carriage returns is standard Windows `print(end="\r")` + `print()` output and stays blank under
  a single strip.
- **The item's Part 2 acceptance criterion was refuted and deliberately not tested.** "Chunks
  containing no `\r\n`" is false on legitimate input (a CR-run before a newline correctly emits a
  CRLF at a new position); the criterion shipped is an equality over the concatenation against the
  original byte positions.
- **The item's "flushed on close" glossed a gap**: `chunkWriter` had no close path, `os/exec` never
  closes a caller-supplied writer, and the writers are per step - so the flush is an explicit
  per-step call, named `flush()` rather than `Close()`.
- **Both parts shipped in one slice.** Part 2 alone provably does not fix the bug: collapsing the
  CRLF in a CR-CR-LF line still leaves a line ending in a carriage return.
- Priority `high` stood (spec D14): 260/264 blank rows on a real production task, on the project's
  primary platform, with no operator workaround.

---

## Whole-slice verification commands

| Gate | Command | Notes |
|---|---|---|
| Go build | `go build ./...` | |
| Go default lane | `go test ./... -timeout 120s` | includes both new agent test groups; **no build tag, so it runs on Windows and on Linux** |
| Integration-tag compile | `go vet -tags integration ./...` | no Docker needed |
| Web unit lane | `cd web; npm test` | `vitest run`, per `web/package.json:10` |
| Web single file | `cd web; npx vitest run src/jobs/logBuffer.test.ts` | |
| Race | **CI only** (`go-ci.yml:34`) | local ThreadSanitizer failure is environmental; the only gate on D9 |
| Typecheck / SPA build / Playwright | **CI only** (`web-ci.yml:58-103`) | do not run `npm run build` locally - it dirties tracked `web/dist/index.html` |
| Integration lane | **not applicable** | no integration-tagged file is created or touched |

---

## Risks this plan carries forward

From spec 13.1:

1. **`Cmd.Wait`'s join guarantee is documented but subtle.** Task B2 Step 6 confirms the sentence
   against the pinned toolchain rather than trusting the spec. D9's mutex makes the slice correct
   either way, so this is a documentation risk, not a correctness one.
2. **The `-race` lane is not runnable locally.** CI is the only detector for anything the mutex gets
   wrong, and M13 is declared unkillable. Both facts belong in the PR body.
3. **A log line is unbounded in the web.** `MAX_LINES` caps line count, not length, and the partial
   buffer grows until a newline arrives. Pre-existing; D3's index walk removes the one way this slice
   would have made it worse. Proposed as a follow-on (spec 13.3 item 2), not filed.
4. **Part 2 changes stored bytes.** Accepted deliberately; Task B3 Step 2 is the mitigation.
5. **The CR-CR-LF residue is easy to un-learn.** A future reader who "simplifies" strip-all back to
   strip-one on the grounds that the agent removes CRLF anyway reintroduces the bug for exactly that
   shape. Task F1's case 3, mutation M1, and the doc comment are what stop it. The symmetric hazard
   is a reviewer "correcting" T2-C's expected value, which M12 covers.

Added by this decomposition:

6. **The self-exec helper process is a new pattern in this repo** - a grep for `GO_WANT_HELPER` and
   `HelperProcess` finds no precedent anywhere in the tree. Three hazards are handled inline in Task
   B2: the testing framework's `PASS\nok` polluting the asserted stdout (handled by `os.Exit(0)`),
   `GOFLAGS` injecting `-test.v` into the child (it cannot - `GOFLAGS` is read by the `go` command,
   not by a raw test binary), and `os.Args[0]` under `go test` (it is the built test binary, an
   absolute path). **If the helper proves unworkable, the fallback is NOT a shell producer** -
   `cmd /c echo` makes the assertion vacuous on CI (spec 9.2). The fallback is to assert T2-C's
   payload at the `chunkWriter` level and declare the wiring untested, which loses M6 and M7 and must
   be reported to the conductor, not absorbed silently.
7. **Two agents commit to one branch.** Every commit stages an explicit file list; `git add -A` is
   forbidden. Mutation worktrees are per-task and per-path, seeded by copy and reverted by copy.
8. **Task F2's four tests are PINS, not REDs.** They are green the moment F1 lands, and their whole
   value is mutation M4. A lane that skips the mutation step ships four tests nobody has proven can
   fail. Task B1's `require.Empty(t, w.held)` is the exception - a genuine compile-level RED at HEAD.
9. **Existing `internal/agent` tests that join Windows `cmd /c echo` output now see LF where they saw
   CRLF.** They assert with `Contains` on CR-free substrings, so they should be unaffected; Task B1
   Step 4 runs the whole package for exactly this reason. A failure there is a real behaviour change
   to read, not a fixture to edit.

## Self-review notes, and where this plan sharpens or departs from the spec

1. **M8 is killed, where the spec permitted declaring it unkilled.** Spec 9.4 hedges: "If a
   deterministic kill is not achievable, declare it unkilled on the record".
   `TestChunkWriter_FlushIsBoundedByTheCancelChannels` (T2-G) achieves it - a wedged-full `sendCh`
   plus a closed `cancelledCh` gives `sendOrAbort` exactly one ready case while `r.send` has none, so
   correct code returns in microseconds and the mutant parks forever. The 2s bound has no margin to
   tune, which is what distinguishes it from a flaky timing test.
2. **T2-E is re-sited from a `Run`-level cancel test to a white-box `chunkWriter` test, and it is
   stronger for it.** The spec's framing (force-cancel a real runner, assert nothing is enqueued
   after the abandon) cannot deterministically kill M9: once `forcedCh` is closed, `flush()` sends
   nothing regardless of whether `held` was armed, so the mutant and the original are observationally
   identical through the channel. `internal/agent`'s tests are `package agent`, so asserting `w.held`
   directly makes M9 die deterministically. **This also resolves the spec's own hedge about
   inheriting `runner_cancel_test.go`'s `//go:build !windows` tag: nothing in this slice needs it,
   and every new test runs on both platforms.**
3. **T2-F's fixture is not the one the spec sketches.** The spec suggests "a trailing `\r` on stdout
   interleaved with complete lines on stderr". That fixture is GREEN AT HEAD - the interior CR it
   produces survives the correct transform unchanged, so the test would assert the same string before
   and after the fix. The fixture written here puts a CRLF on the stderr stream too, which makes it
   RED at HEAD on both assertions while still dying under M11.
4. **T1-D is green at HEAD and stays green.** The spec lists it as an acceptance criterion; it is a
   non-regression pin, not a RED test, and this plan says so rather than implying a kill it does not
   have. Mutation M3b (plan-added) is its kill, and M-ansi (plan-added) is T1-E's.
5. **Mutation worktrees are seeded by COPY and reverted by COPY, never by `git checkout --`.** A
   detached worktree at `HEAD` does not contain the task's uncommitted implementation, and a
   `checkout` inside it silently reverts that implementation along with the mutation - after which
   every remaining row reports "survived" against code that is not the code under test. Each battery
   also requires a green unmutated baseline in the mutant tree before its control row runs.
6. **Spec 13.4 is resolved as in-scope by conductor decision** and is Task B3 Step 2, sited inside
   the export proposal bullet rather than in that item's Notes.
7. **T1-F ("the implementation is an index walk, not a backtracking regex") is reviewed, not
   tested.** A timing test would be flaky. It is stated in Task F1's implementation comment and named
   here so the Phase 4 reviewer checks it.
8. **Nothing here is multi-session.** One spec, one plan, one PR, two concurrent lanes inside it. No
   `## Stage N` units, so **no `/backlog phases` run is needed for this plan.** Spec 13.3's items 2
   and 3 remain proposals for the conductor to file if wanted (the uncapped SPA line length, and
   `relay logs`'s per-chunk prefix - the latter already scoped to
   `bug-2026-08-25-relay-logs-prints-nothing-envelope-drift:111-114`).
