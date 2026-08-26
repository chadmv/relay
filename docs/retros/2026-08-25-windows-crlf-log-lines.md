---
date: 2026-08-25
topic: windows-crlf-log-lines
branch: claude/windows-crlf-log-blank-c488bf
range: 03f45d4..df29d7b
---

# Session Retro: 2026-08-25 - Windows CRLF Log Lines

**TL;DR:** On Windows, nearly every line a job's subprocess printed showed up on the job page as a
timestamp with no text next to it - 260 of 264 lines on one real production job. The log renderer
treats a carriage return as "erase what came before", and Windows ends every line with one, so it
erased every line. This session fixed the display, and separately stopped the agent putting those
carriage returns on the wire at all. The bug report's own proposed fix would not have worked: it
removed one carriage return per line, and the most ordinary Windows sequence for printing a progress
bar and then a newline leaves two.

## Handoff

Two surfaces, one slice, both shipped. **Part 1** is `collapseCR`
(`web/src/jobs/logBuffer.ts`): strip EVERY trailing CR with an index walk, then the pre-existing
interior-CR collapse, in a single `lastIndexOf(s, '\r', end - 1)` with no intermediate slice. Strip-ALL
is not a tolerance, it is the fix - `"x\r\r"` is what the Windows C runtime writes for
`print("done", end="\r")` then `print()`, and strip-one leaves that row blank. Strip-all is also what
makes Part 2 provably render-invisible on every input (spec 4.4), which is the entire reason both parts
could ship together. **Part 2** is `chunkWriter` (`internal/agent/runner.go`): a `heldCR bool` guarded
by a `sync.Mutex`, `collapseCRLF` in place per `Write`, and an explicit `flush()` called for BOTH
writers inside the per-step loop after `cmd.Wait()` and after `cleanupProcTree()`. The guarantee is an
equality on the CONCATENATION against the ORIGINAL byte positions, never "no chunk contains CRLF" -
`"x\r\r\n"` correctly emits `"x\r\n"`, a CRLF at positions that did not have one, and
`TestRunner_CRLFFlushIsWiredAndPrecedesTheTerminalStatus` embeds that CRLF in its expected value on
purpose (M12 exists to stop a reviewer "correcting" it).

Three placement facts that are load-bearing and easy to undo. `flush()` is not `Close()` because
`os/exec` never closes a caller-supplied `Stdout`. It uses `sendOrAbort`, never `r.send`, which is
bounded only by the AGENT context. And `cleanupProcTree()` goes FIRST, because `flush` can park in
`sendOrAbort` for the length of a network partition and the Job Object handle is what kills leaked
grandchildren. `internal/worker/handler.go`'s FIFO comment now names `flush` as a second enqueue site
and states it as a CONSTRAINT on where the flush may be called.

Closes `bug-2026-08-25-windows-crlf-log-lines-render-blank` (`docs/backlog/closed/`), whose Resolution
records the three refutations. Annotated `idea-2026-08-09-task-log-tail-and-paging-improvements` inside
its export bullet: a byte-exact export is foreclosed as of today. **`go test -race` IS runnable on this
host, in Docker** - `MSYS_NO_PATHCONV=1 docker run --rm -v <abs>:/src -w /src -e CGO_ENABLED=1
golang:1.26 go test -race ./...` - which retires the "not runnable locally" claim three consecutive
retros carried. Next session starts at ROADMAP Now, which leads with the retries and un-fireable-schedule
pair.

### Still open

- **The wire between the two halves has never been observed.** No test in this repo drives a real
  `agent.Runner` subprocess through a real gRPC stream into a real `task_logs` row.
  `internal/worker/handler_tasklog_e2e_integration_test.go` calls `HandleTaskLog` directly. So this
  slice has two disjoint proof points - `chunkWriter` tests and SPA tests - with the wire ASSUMED, and
  the item's actual acceptance criterion ("a CRLF-emitting subprocess renders its text on the job detail
  page") has never been observed end to end. Item below.
- **M13 (remove the mutex) is declared unkillable on the record**, with the shape of the failed search
  stated rather than the verdict alone: `Cmd.Wait`'s join means no deterministic single-process test can
  observe it. CI's `-race` plus the Docker lane above are what stand in.
- **A log line has no length cap in the SPA.** `MAX_LINES` caps line COUNT; the partial buffer grows
  unbounded until a newline arrives. Pre-existing; the index walk removes the one way this slice would
  have made it worse. Item below.
- **`README.md:332` still describes `task_logs` as captured stdout/stderr** with no note that stored
  bytes are no longer byte-exact. Spec 8.5 scoped README out on the grounds that no passage asserts
  byte-exactness, which is true; "captured" still reads as verbatim. Item below.
- **`relay logs` prints its `[task stream]` prefix per CHUNK, not per line.** Already scoped to
  `bug-2026-08-25-relay-logs-prints-nothing-envelope-drift`; named here so it is not re-filed.

## What Was Built

- **`web/src/jobs/logBuffer.ts`** - `collapseCR` rewritten as strip-all-then-collapse, plus a doc
  comment that is most of the diff: why strip-all rather than strip-one, why the walk rather than
  `/\r+$/` (with the measured numbers, corrected in the fix round), why it must run after `stripAnsi`,
  and why the single-`lastIndexOf` form is safe at `end === 0`.
- **`web/src/jobs/logBuffer.test.ts`** - append-only, five tests. The three-case discriminating table;
  the partial paths on BOTH streams (two separate call sites at `:230`/`:233`); the empty/CR-only
  non-regression pin; the ANSI-erase-line ordering pin; and the upgraded-vs-un-upgraded pair that makes
  spec 4.4's render-invisibility claim executable. `logBuffer.test.ts:118` (the interior-CR test) passes
  byte-identical and unedited, which is itself an acceptance criterion.
- **`internal/agent/runner.go`** - `heldCR bool` + `mu sync.Mutex` on `chunkWriter`, `collapseCRLF`,
  `Write` rewritten with a three-outcome contract, `flush()`, the two call-site lines, and three prose
  sites (the type comment, `sendOrAbort`'s "only `Write` uses this", and the call-site block). The
  `bool` rather than `[]byte` is deliberate: it retires the sole-ownership argument a slice header
  leaving the critical section would have needed (spec section 10).
- **`internal/agent/runner_crlf_test.go`** - new, default lane, NO build tag. Eight functions, one of
  which (`TestCRLFHelperProcess`) is not a test but the self-exec subprocess the two wiring tests
  drive. A shell producer could not carry these assertions: `cmd /c echo` emits CRLF natively on
  Windows and never on Linux, so a CRLF assertion behind a `runtime.GOOS` switch is vacuously green on
  CI - the platform-gated-verification trap, inverted.
- **`internal/worker/handler.go`** - the FIFO ordering comment gains `flush` and becomes a constraint.
- **`docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md`** - the foreclosed
  byte-exact export, sited inside the export proposal bullet where an export spec's author lands.

### Verification

- **This retro pass had no shell.** No `git log`, no `git diff`, no test run was executed here. Every
  claim that could be checked by reading was checked against the worktree; commit SHAs come from the
  closed item's Resolution note.
- **What the conductor verified independently during the session**, since a retro that launders
  subagent claims is worthless: the full Go suite uncached (21 packages, exit 0); vitest twice (152
  files / 1121 tests), the second run after a `node_modules` rebuild; the `errW.flush()` mutation before
  AND after the fix, each time with a working control; per-commit file sets; the reflog after the index
  collision; and `-race` plus the `//go:build !windows` cancel tests in a `golang:1.26` Linux container
  at TWO different HEADs, the second because the `heldCR bool` refactor and the `cleanupProcTree`
  reorder both landed after the first.
- **Integration lane green**: 626 tests against real Postgres and p4d containers, per the closed item.
- **Not verified here**: the commit set as git sees it, the diff stat, and the working tree state at
  `df29d7b`.

## Key Decisions

- **Strip ALL trailing carriage returns, not one.** This overrules the backlog item. It is the fix for
  `"x\r\r"`, and it is what decouples the two parts - under strip-one the SPA's output would depend on
  whether the agent had been upgraded, the worst possible property for a rendering fix.
- **Both parts in one slice.** Part 2 alone provably does not fix the bug (collapsing the CRLF in
  `"x\r\r\n"` still leaves a line ending in a CR), so shipping it separately would have looked like a
  fix and not been one.
- **The agent-side transform is exactly `\r\n -> \n`, never `\r+\n -> \n`.** The wider rule is a
  judgement about what is VISIBLE, and visible-content judgements stay in the client that holds the
  opinion. The residue is Part 1's job.
- **The invariant is stated over the concatenation, against the original byte positions.** The item's
  "chunks contain no CRLF" is false on legitimate input; a fixture narrowed until it holds would have
  pinned the wrong contract.
- **`flush()` is explicit and per-step, and `cleanupProcTree()` precedes it.** Four constraints bound a
  REGION rather than a point; the ordering inside that region was then decided by a fifth consideration
  the spec did not have.
- **No Playwright spec.** The transform is a pure string function with no layout component. The browser
  lane earns its slot when layout is the claim; it is not.
- **No README edit, no server change, no CLI change, no migration.** Every one of these was checked
  against a named site rather than assumed.

### CLAUDE.md verdict

**No amendment is earned.** The strongest candidate is the concurrent-lane git discipline (`git commit
-- <pathspec>`), and it is real - it nearly cost a commit. It still does not belong in Invariants, which
is for rules new *code* must not bypass; this is a rule for an orchestrator, and the plan already tried
the prose version of it ("stage an explicit file list") one document earlier and it was insufficient.
The durable home is a `feedback` memory plus, if anywhere in-repo, `docs/agent-team/README.md`. Two
others were considered and rejected: the CRLF byte-edit trap (already in durable memory, and the fix is
a widened trigger there, not a fourth CLAUDE.md paragraph), and the symbol-citation rule (also already
in durable memory, and CLAUDE.md is not where an agent writing a comment is looking).

One thing worth flagging to the conductor and NOT recommending: adding the trailing-CR rule to the
Invariants list. `AppendTaskLog`'s status-allow-list-or-recency disjunction is already documented there
at length as a carve-out, and this slice touched none of it. A worked example belongs in the code, and
the `collapseCR` and `flush` comments are that.

## What Went Wrong and What Changes

**Ledger.** From the 2026-08-26 worker-delete retro: *verify a backlog item's technical claims* -
applied, twenty-fifth iteration, and the strongest instance yet, since the item's proposed FIX fell
rather than its facts. *A backlog proposal is not a contract* - applied. *An accurate item can prescribe
a wrong remedy* - applied twice over (Part 1's design and Part 2's acceptance criterion were both wrong
in an otherwise accurate item). *Each stage treats the previous stage's output as untrusted* - applied
at every boundary; that is this session's whole spine. *Verify the mutation actually applied*
[[reference_verify_the_mutation_applied]] - **recurred, four times, on three different actors**; entry
below. *An applied-check must assert WHERE* and *"unkillable" is a claim about the instruments*, both
promoted into that same memory - applied and vindicated: the plan killed M8 deterministically where the
spec permitted declaring it unkilled, and M13 kept its declaration only with the shape of the failed
search attached. *A mutation proof must leave a test behind* - applied; the `errW.flush()` fix left a
permanent stderr assertion. *Wrong prose about correct code*
[[reference_wrong_prose_is_the_dominant_defect]] - **recurred, ninth consecutive iteration**; two
entries below. *A green re-run bounds a red run's frequency* and *a test whose name asserts more than
its body* - not exercised, both still unpromoted, carried. *Read every artifact once for internal
contradiction before handing it down* - **applied, and it is the most valuable thing that happened
here**; still unpromoted, offered below.

- **(CARRIED) Four `file:line` citations were stale, and three were invalidated by the very commits
  that wrote them.** The commit inserted comment lines above its own pointer. The fourth pointed into a
  file a sibling lane was editing concurrently. Caught in Phase 4; both engineers then converted their
  citations wholesale rather than renumbering.
  -> **What changes:** cite the SYMBOL, never a line range - unconditionally, not only when you are
  editing the file you are citing. The recorded rule carries that conditional, and under concurrent
  lanes you do not know which files are under edit. `:45 -> :46` is a fix with the same expiry date as
  the defect.

- **(CARRIED) The CRLF byte-edit trap fired FOUR times in one slice, on three different actors.**
  Line-anchored `sed`/`perl` patterns do not match `\r\n`, so the edit silently does not apply and the
  run reports "survived" against unmutated code. The correctness lens lost four mutations to it; the
  backend engineer lost some to heredoc backslash mangling; and the CONDUCTOR hit it outside mutation
  testing entirely, when a `perl -0pi -e 's/^status: open$/.../m'` on backlog frontmatter silently did
  nothing.
  -> **What changes:** this is no longer a lesson, it is a tooling rule. Every byte-level edit on this
  tree - mutation or not - carries a `count(anchor) == 1` uniqueness assertion plus a before/after
  applied-check. The recorded rule is filed under mutation testing; the conductor's instance was
  frontmatter, which is why it did not fire.

- **Two concurrent agents share ONE git index, and `git add <files>` then a bare `git commit` is not
  atomic.** The frontend lane's commit swept in the backend lane's in-progress `runner.go`. Recovery was
  `git reset HEAD~1` plus a path-scoped recommit. It worked, and it was luck: had the backend lane
  committed inside that window, the reset would have discarded ITS commit.
  -> **What changes:** when two lanes commit to one branch, use `git commit -- <pathspec>`, which
  bypasses the index entirely. The plan said "stage an explicit file list" and that was insufficient -
  it constrains what you add, not what is already staged.

- **`git worktree remove --force` recursed through a Windows junction and deleted the real
  `web/node_modules`.** Not the link, the target. Restored with `npm ci`, which is why vitest was run
  twice. The technique that caused it - junction `node_modules` into a detached mutation worktree - is
  the one the project's own durable memory recommends.
  -> **What changes:** remove the junction before removing the worktree, or do not junction at all and
  pay the `npm ci`. Warn wherever mutation worktrees are described, because the destructive step is the
  teardown, not the setup.

- **`errW.flush()` was a deletable line: the whole package stayed green without it.** Found by two
  Phase 4 lenses independently and reproduced by the conductor with a working control (deleting
  `outW.flush()` killed two tests, so the harness was sound and the gap was real). The wiring test
  asserted only on stdout, and the diff had created two symmetric call sites.
  -> **What changes:** [[reference_added_a_property_forgot_its_guard]] in its N-subjects variant - when
  a diff gives an existing symbol a new job at more than one call site, every call site is its own
  subject and needs its own assertion. A guard that covers one of two reads as coverage for both, and
  the surviving mutation is the only thing that says otherwise.

- **The security rationale for the index walk named the wrong input shape, and the mitigation was 11x
  slower than what it replaced on that shape.** The comment claimed `/\r+$/` is quadratic on a trailing
  CR run. Measured in V8 at N = 200,000, a genuinely trailing run is where the regex is FASTEST (0.2 ms
  against the walk's 0.5 ms) - it matches on its first attempt. The catastrophic case is an INTERIOR
  run with more bytes still to come (23,637 ms), which is exactly what a progress bar leaves in an
  in-flight partial. The conclusion (use the walk) was right; the reason was wrong, and the shipped
  walk had a regression on the input the comment named. Fixed with a single-`lastIndexOf` form; all
  seven web mutations still die.
  -> **What changes:** when a mitigation is justified by an asymptotic or security claim, benchmark it
  against the code it REPLACES on the input the claim names - not only against the threat. Two separate
  errors hid behind one correct conclusion, and neither was visible to reading.

- **A spec-designed test can be structurally incapable of the kill it is paired with.** Spec T2-E
  framed the abort test at the `Run` level; once `forcedCh` is closed, `flush()` sends nothing whether
  or not `held` was armed, so mutant and original are observationally identical through the channel.
  M9 could not have died. The plan re-sited it as a white-box `chunkWriter` test asserting `w.held`
  directly, and it dies deterministically.
  -> **What changes:** when a spec names a mutation and its killer in the same row, the plan traces the
  mutant's observable difference all the way to the test's assertion surface before accepting the
  pairing. "The test exercises the code" is not "the test can see the difference".

- **A near-miss that went right: the plan refuted the spec's T2-F fixture as green-at-HEAD.** The
  spec's sketch (a trailing `\r` on stdout interleaved with complete lines on stderr) produces an
  interior CR that survives the correct transform unchanged, so it would have asserted the same string
  before and after the fix. The plan added a CRLF to the stderr arm, making it RED at HEAD on both
  assertions while still dying under M11.
  -> **What changes:** for every pin, state its colour at HEAD explicitly - RED, or "green at HEAD and
  its kill is mutation M<n>". The plan did this for T1-D and caught T2-F by doing it. No process change
  beyond making that statement mandatory rather than customary.

## Recommended Backlog Items

Proposals only; the human gives final accept. This is intake, not a priority order - `ROADMAP.md` orders
the work and the Handoff names the next entry point.

- [idea] **No test drives a real agent subprocess through gRPC into a `task_logs` row.** This slice's
  two halves are proven independently and the wire between them is assumed. `internal/worker/
  handler_tasklog_e2e_integration_test.go` calls `HandleTaskLog` directly, so nothing exercises
  `chunkWriter` -> `sendCh` -> the gRPC stream -> `HandleTaskLog` -> `AppendTaskLog` as one path. The
  measured consequence is that the closing acceptance criterion of
  `bug-2026-08-25-windows-crlf-log-lines-render-blank` ("a CRLF-emitting subprocess renders its text on
  the job detail page") has never been observed. Distinct from
  `idea-2026-08-24-e2e-harness-slice-2-agent-in-harness`, which is about which browser SURFACES become
  reachable; this is about byte fidelity through the log pipeline and is closeable at the Go
  integration level without Playwright. Also adjacent to
  `idea-2026-08-23-integration-only-guards-ci-never-runs`.
- [bug] **A log line has no length cap in the SPA.** `MAX_LINES` (`web/src/jobs/logBuffer.ts`) caps line
  COUNT; the partial buffer in `appendEntries` grows unbounded until a newline arrives. A job printing
  megabytes with no newline degrades the operator's tab regardless of the index walk. Pre-existing and
  named in spec 13.1/13.3. Not covered by
  `idea-2026-08-09-task-log-tail-and-paging-improvements` (retained-line cap and virtualization, both
  count-based) nor by `bug-2026-08-14-task-logs-have-no-per-task-volume-cap` (server-side volume).
- [idea] **`README.md:332` describes `task_logs` as captured stdout/stderr and no longer qualifies
  that.** As of this slice the agent normalises `\r\n` to `\n` before a chunk is sent, so stored bytes
  are not a byte-exact copy. Spec 8.5 scoped README out on the accurate grounds that no passage claims
  byte-exactness; "captured" nonetheless reads as verbatim to someone deciding whether an export can be
  byte-exact. Small, and the `idea-2026-08-09` annotation already covers the consumer most likely to
  care.
- [append to `idea-2026-08-25-no-documented-working-local-race-lane`] **The container `-race` lane has a
  verified working invocation and it should go in the item.** `MSYS_NO_PATHCONV=1 docker run --rm -v
  <abs>:/src -w /src -e CGO_ENABLED=1 golang:1.26 go test -race ./...` was run green here at two
  different HEADs, and it also runs the `//go:build !windows` tests that `go test` skips wholesale on
  Windows. That is the item's second acceptance criterion satisfied with a command rather than a
  promise; what remains open is the CLAUDE.md edit and naming both failure modes.

**Already tracked** (linked, not offered): `relay logs` prints its `[task stream]` prefix per chunk
rather than per line -> [`bug-2026-08-25-relay-logs-prints-nothing-envelope-drift`](../backlog/bug-2026-08-25-relay-logs-prints-nothing-envelope-drift.md).

**Not filed as backlog, routed to durable homes instead:** the concurrent-lane git index collision and
the junction deletion. Both are process rules with no code acceptance criterion, and a permanently-open
row is the wrong shape for them. They are promotion candidates below.

## Files Most Touched

- `web/src/jobs/logBuffer.ts` - `collapseCR` and its doc comment. The comment is where the corrected
  ReDoS reasoning and the strip-all argument live; it is what stops a future reader narrowing the rule.
- `internal/agent/runner.go` - `chunkWriter`, `Write`, `collapseCRLF`, `flush`, and the per-step call
  site. The call-site comment block is the one to read: four constraints bounding a region, plus why
  `cleanupProcTree()` goes first.
- `internal/agent/runner_crlf_test.go` - new. `TestChunkWriter_StraddledCRLFCollapsesAcrossWriteBoundary`
  is the discriminating test for all of Part 2; `TestRunner_CRLFFlushIsWiredAndPrecedesTheTerminalStatus`
  is what makes deleting either flush call site fatal, and its stderr assertion is the fix round's
  residue.
- `web/src/jobs/logBuffer.test.ts` - append-only. `logBuffer.test.ts:118` unedited is an acceptance
  criterion, not an accident.
- `internal/worker/handler.go` - the FIFO ordering comment, now a constraint on two enqueue sites.
- `docs/superpowers/specs/2026-08-25-windows-crlf-log-lines.md` - sections 3 (R1-R11) and 12 (the
  autonomous decision ledger). Worth reading as a worked example of a spec overruling the item it
  implements, and of a spec correcting an acceptance criterion rather than a fact.
- `docs/superpowers/plans/2026-08-25-windows-crlf-log-lines.md` - the "Self-review notes, and where this
  plan sharpens or departs from the spec" section, which is where the two test-design refutations are
  recorded.
- `docs/backlog/closed/bug-2026-08-25-windows-crlf-log-lines-render-blank.md` - the Resolution note; the
  item's own body still argues for the narrower rule, so the note is what a future reader needs.
