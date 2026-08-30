# Comment Policy and Retrofit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install the comment policy (CLAUDE.md + agent briefs + review-side deletion-first rule) and retrofit the known high-drift comment essays down to policy-compliant hazard statements, with zero behavior change.

**Architecture:** Two phases from the approved spec
(`docs/superpowers/specs/2026-08-30-comment-policy-and-retrofit-design.md`). Phase 1 (Tasks 1-3)
edits policy documents only. Phase 2 (Tasks 4-11) makes comments-only edits to seven production
files, delete/condense only, never authoring new claims. Phase 2 must land after Phase 1 so the
reviewer has the policy to review against.

**Tech Stack:** Markdown, Go comments, Python comments/docstrings. No code changes; the test suite is a regression alarm only.

**Cross-cutting rules for every task:**

- Edit-tool edits only. No scripted rewrites, no `sed`/`python -c` bulk edits - this is the CRLF
  and encoding hazard CLAUDE.md documents.
- Never use em dashes or en dashes; use regular hyphens.
- After each task's edits: `git diff --stat` must be proportionate to the intended change, and
  `git ls-files --eol <touched files>` must read `i/lf` on every touched path.
- Commit with an explicit pathspec (`git commit <files> -m ...`), never a bare `git commit` - the
  worktree index is shared with concurrent sessions.

---

## Phase 1: policy

### Task 1: CLAUDE.md Comments section

**Files:**
- Modify: `CLAUDE.md` (project root)

- [ ] **Step 1: Insert the section**

Insert the following immediately BEFORE the line `## Invariants` (so it sits between
`## Key Design Decisions` and `## Invariants`):

```markdown
## Comments

A comment exists to state a hazard or constraint the code cannot show, in a few lines. It may
cite the one test that pins the claim ("deleting this guard turns every typo into a broadcast
subscription; TestCanonicalJobIDFilter's passthrough rows go red"). Everything else - the
argument that the change is correct, its history, its measurements - goes in the commit
message, spec, or retro: records of a moment, which cannot drift. If content feels worth
keeping, it is - in the commit message.

Never put in a comment or docstring:

- Dates or change history ("since 2026-08-30", "was previously two readers"). Git owns history.
  (A date inside a backlog-item filename cited as a pointer is an identifier, not history, and
  is fine.)
- Session or review narrative, and measurement provenance ("measured by rendering it uppercase
  and watching that test fail").
- Counts of anything elsewhere ("16 sites", "four other copies").
- Uniqueness or completeness claims ("the only", "every", "all N") about OTHER code. These are
  claims about the complement, pinned by nothing; replace with a named guard or delete. Stating
  this function's own contract ("prints every not-yet-printed task") is fine.
- Censuses of other files or packages, and claims about another language's source.

Test comments state the property pinned and why the input discriminates. RED/GREEN history and
mutation provenance go in the commit that adds the test.

```

- [ ] **Step 2: Verify placement and encoding**

Run: `grep -n "^## " CLAUDE.md | head -20`
Expected: `## Comments` appears directly before `## Invariants`.

Run: `git diff --stat CLAUDE.md` (expect ~+27 lines) and
`git ls-files --eol CLAUDE.md` (expect `i/lf`).

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit CLAUDE.md -m "docs: add the comment policy to CLAUDE.md (spec 2026-08-30, phase 1a)"
```

### Task 2: implementer briefs

**Files:**
- Modify: `.claude/agents/relay-backend-engineer.md` (Conventions section, currently line 48)
- Modify: `.claude/agents/relay-frontend-engineer.md` (Conventions section)
- Modify: `.claude/agents/relay-integration-tester.md` (Conventions section)

- [ ] **Step 1: Replace the density bullet in relay-backend-engineer.md**

Replace the bullet:

```markdown
- Match the surrounding code's style, naming, and comment density.
```

with:

```markdown
- Match the surrounding code's style and naming, but NOT its comment density - much of the
  existing density is history the comment policy now forbids. A comment states a hazard or
  constraint the code cannot show, in a few lines, optionally citing the one test that pins it.
  No dates, change history, measurement narratives, counts, uniqueness/completeness claims, or
  censuses of other files - that content goes in the commit message or spec. Test comments
  state the property pinned and why the input discriminates; provenance goes in the commit.
```

- [ ] **Step 2: Add the identical bullet to the other two briefs**

Add the same bullet (verbatim from Step 1's replacement text) to the Conventions section of
`.claude/agents/relay-frontend-engineer.md` and `.claude/agents/relay-integration-tester.md`.
If a brief has no Conventions section, add one titled `## Conventions` at the end of the file
with this bullet. Do not modify anything else in those files.

- [ ] **Step 3: Verify**

Run: `grep -rn "comment density" .claude/agents/`
Expected: exactly three matches, all inside the new bullet's "NOT its comment density" phrasing.

Run: `git ls-files --eol .claude/agents/relay-backend-engineer.md .claude/agents/relay-frontend-engineer.md .claude/agents/relay-integration-tester.md`
Expected: `i/lf` on all three.

- [ ] **Step 4: Commit**

```bash
git add .claude/agents/relay-backend-engineer.md .claude/agents/relay-frontend-engineer.md .claude/agents/relay-integration-tester.md
git commit .claude/agents/relay-backend-engineer.md .claude/agents/relay-frontend-engineer.md .claude/agents/relay-integration-tester.md -m "docs: comment policy in the implementer briefs; stop matching comment density (phase 1b)"
```

### Task 3: review-side enforcement

**Files:**
- Modify: `.claude/agents/relay-code-reviewer.md`
- Modify: `docs/agent-team/README.md`

- [ ] **Step 1: Add the Prose findings section to relay-code-reviewer.md**

Insert immediately BEFORE the `## Output` section:

```markdown
## Prose findings

A checkable-but-unpinned claim in an added comment or docstring is itself a finding - counts,
uniqueness claims, dates, censuses of other files, cross-language claims, measurement
narratives. The default remedy to suggest is delete, or relocate to the commit message. Suggest
a corrected wording only with a stated reason the claim must live in code at all: corrections
to such claims regenerated the defect four times running on one docstring.
```

- [ ] **Step 2: Amend the correctness lens cell in docs/agent-team/README.md**

In the Phase 4 lens table, change the correctness row's brief cell from:

```
Correctness bugs and needless complexity. Attack the tests as their own artifact.
```

to:

```
Correctness bugs and needless complexity. Attack the tests as their own artifact. A checkable-but-unpinned prose claim is a finding; the default remedy is deletion or relocation to the commit message.
```

- [ ] **Step 3: Amend the fix-round paragraph in docs/agent-team/README.md**

At the end of the bold-opening paragraph "After a fix round, the verify lens's primary subject
is the FIX'S OWN DIFF..." (after "...rather than re-reviewing ground two lenses already
covered."), append:

```
For prose findings the fix is deletion-first; a correction is the remedy that regenerated the defect four times running on one docstring.
```

- [ ] **Step 4: Verify and commit**

Run: `grep -n "Prose findings" .claude/agents/relay-code-reviewer.md` (expect 1 match) and
`grep -cn "deletion" docs/agent-team/README.md` (expect 2 or more matches).
Run: `git ls-files --eol .claude/agents/relay-code-reviewer.md docs/agent-team/README.md` (expect `i/lf`).

```bash
git add .claude/agents/relay-code-reviewer.md docs/agent-team/README.md
git commit .claude/agents/relay-code-reviewer.md docs/agent-team/README.md -m "docs: prose findings are deletion-first in review (phase 1c)"
```

---

## Phase 2: retrofit

### Phase 2 rules (every Task 4-10 applies these; they restate CLAUDE.md's new Comments section operationally)

**STRIKE (delete the sentence or clause; condense the block afterward so it still reads):**

1. Dates and change history: "since 2026-08-30", "until 2026-08-26", "The SECOND reader was
   handleEvents, and it stopped being one on...", "has written this object since 2026-05-08",
   "used to be", "was described here as", "That remedy was written here".
2. Measurement narratives: any "measured, ..." clause and the numbers it carries ("measured, an
   85-character name gives 196, 90 gives 203", "measured, uppercasing internal/worker's fails
   internal/worker").
3. Counts and censuses of other code: "all 8 JobID-carrying broker.Publish sites",
   "the sixth production copy of it (internal/api/server.go, cmd/relay-server/main.go, ...)",
   "uuidStr is THREE different unexported functions ... (2 of the 8 sites)", "16 sites".
4. Uniqueness/completeness claims about OTHER code: "the only untrusted input this file has",
   "nothing relates the three to each other", "Every LIST query is ...", "every list handler
   goes through buildPage".
5. Review/session narrative: "The claim that it did ... was measured false", "It was described
   here as a X and it is not one", "Earlier versions of this comment said...".

**KEEP (do not delete; condense wording only if the block around it shrinks):**

- The hazard sentence: what breaks, in which direction, if this code is changed or deleted
  ("rendering unconditionally would promote every typo'd ?job_id= into the server-wide status
  feed"; "Do NOT point a reader at Client(timeout=): httpx has no total-time and no
  response-size setting").
- One pinning-test citation per claim ("TestCanonicalJobIDFilter's passthrough rows go red").
- A backlog-item filename cited as a pointer (its embedded date is an identifier).
- Statements of THIS function's own contract, including its "every"/"only" ("prints every
  not-yet-printed task in the snapshot"; "returns "" when the body is usable").
- Semantic constraints the code cannot show ("the arms must never be conjoined or the trailing
  flush closes"; "a task-less cancelled job is not a case to accommodate - jobspec.Validate
  rejects a zero-task spec").

**NEVER author a new claim.** If a struck claim looks load-bearing and is pinned by no test,
record it in your task report as a backlog candidate (symbol, file, the claim, why it matters);
the conductor files the items. Do not write tests in this slice.

**Per-file verification (every Task 4-10):**

1. `go build ./...` (or for Python: `cd python && python -m pytest tests/unit -q`) - green.
2. `go test ./<package>/... -count=1` for the touched package - green.
3. Read every hunk of `git diff <file>`: each must touch only comment/docstring lines. Zero
   executable lines changed.
4. `git ls-files --eol <file>` reads `i/lf`; for Python files also confirm the file decodes as
   UTF-8 (open and read it with the Read tool - a decode failure is visible as garbled text).
5. Commit that file alone with an explicit pathspec.

### Task 4: internal/api/events.go

**Files:**
- Modify: `internal/api/events.go` (comments in `handleEvents` and the `canonicalJobIDFilter` doc comment, lines ~50-160)

- [ ] **Step 1: Trim the handleEvents job_id comment (lines ~50-58)**

Replace the existing 8-line comment above `jobID := canonicalJobIDFilter(...)` with:

```go
	// ?job_id= is deliberately NOT VALIDATED: an unknown or unparseable job id
	// yields an open, permanently empty stream rather than a 4xx - an existing
	// contract (README.md "Events"; TestEvents_TaskIDValidation pins that
	// `not-a-uuid` is not a 400). Both parameters are canonicalised; see
	// canonicalJobIDFilter for why the unparseable case must pass through
	// UNCHANGED rather than be rendered.
```

- [ ] **Step 2: Rewrite the canonicalJobIDFilter doc comment (lines ~102-160)**

Replace the entire ~58-line doc comment with:

```go
// canonicalJobIDFilter renders raw in the spelling publishers emit, and returns
// raw UNCHANGED when it is not a UUID this server accepts.
//
// The server accepts more spellings than it emits (parseUUID is pgtype.UUID.Scan:
// case-insensitive, dashless form, and separator bytes in the 36-byte form are
// sliced out unexamined), while the broker filter is an exact string compare -
// so without this render, an accepted-but-non-canonical ?job_id= subscribes to a
// filter nothing ever matches: an open, silently empty stream forever.
// TestCanonicalJobIDFilter pins this package's rendering; relating it to the
// publisher packages' copies of the format string is
// docs/backlog/idea-2026-08-26-six-copies-of-the-uuid-render-format.md.
//
// THE err != nil GUARD IS THE WHOLE CORRECTNESS ARGUMENT, NOT NOISE. parseUUID
// returns the zero UUID on failure, uuidStr renders that as "", and
// Filter{JobID: ""} is the broker's BROADCAST subscription - so rendering
// unconditionally would promote every typo'd ?job_id= from "one job, silently
// empty" into "every job's status events on this server". A scope surprise, not
// a privilege escalation: /v1/events is bearer-auth-only and omitting ?job_id=
// already buys that feed. Deleting the guard reddens
// TestCanonicalJobIDFilter's passthrough rows (default lane) and
// TestEvents_JobIDRejectedSpellingsAreNotCanonicalised (integration lane, the
// one that asserts SCOPE).
//
// The !u.Valid arm is belt-and-braces against a Scan that reports success
// without setting Valid: one comparison, versus the broadcast above.
func canonicalJobIDFilter(raw string) string {
```

- [ ] **Step 3: Verify and commit**

Apply the Phase 2 per-file verification (build, `go test ./internal/api/... -count=1`,
comment-only hunks, eol). Then:

```bash
git add internal/api/events.go
git commit internal/api/events.go -m "docs(api): retrofit events.go comments to the comment policy

Comments only, zero behavior change. Removed per the policy: the publisher-site
census (8 sites, three uuidStr copies), the measured-drift narrative, and the
change-history dating. Kept: the fail-open hazard, its two pinning tests, and
the six-copies backlog pointer."
```

### Task 5: internal/cli/logs.go

**Files:**
- Modify: `internal/cli/logs.go` (comments only; the file is 474 comment lines of 801)

- [ ] **Step 1: Rewrite the canonicalJobID doc comment (lines ~199-248)**

Replace the entire doc comment with:

```go
// canonicalJobID renders jobID in the one spelling the server uses for it, and
// returns jobID unchanged when it is not a UUID at all (the server answers
// 400/404 for those and nothing downstream depends on the value).
//
// The server accepts more spellings than it emits (pgtype.UUID.Scan is
// case-insensitive and takes the dashless form), and two readers need one
// spelling: jobSnapshotUnusable compares the body's id against ours - entirely
// client-side, so no server change can replace this function - and the
// ?job_id= subscription against an OLDER relay-server that does not
// canonicalise. Canonicalising ARGV, before either request line is built,
// covers both; adopting the id from the first snapshot instead could not fix
// the subscription, which is established before any snapshot is read.
//
// Only the PARSE half is shared with the server (same pgtype.UUID.Scan). The
// RENDER below is a duplicate of the server's format string, related to it by
// nothing; TestWatchJobLogs_NonCanonicalJobID_IsResolvedNotRejected pins this
// side's spelling.
func canonicalJobID(jobID string) string {
```

- [ ] **Step 2: Strike-pass over the rest of the file's comments**

Apply the Phase 2 STRIKE/KEEP rules to every other comment in the file. Known instances the
grep survey flagged (line numbers approximate, pre-edit):

- Lines 18-21 (`taskLogPage`): strike "The handler has written this object since 2026-05-08;
  the CLI decoded a bare array into a slice until 2026-08-26, which fails and printed nothing
  for three and a half months." Keep the first sentence (which handler/envelope it mirrors).
- Lines 144-147 (`taskIsTerminal` block): strike the second paragraph ("The second guard exists
  because the first one is not one... prose that could never fire" - change history). Keep the
  lockstep-guard registration pointers and the consequence sentences ("A new TERMINAL task
  status omitted from taskIsTerminal means...").
- Lines 167-173 (`jobSnapshotUnusable`): strike "and handleGetJob discarded ListTasksByJob's
  error until 2026-08-26, so a pool exhaustion... arriving through the function written to
  close it." Keep the omitempty hazard sentence ("a body that carries no task list decodes into
  a silently-empty slice") and both unusable-body reasons, including the jobspec.Validate
  zero-task argument.
- Lines 263-266 (`jobPath`/`jobEventsPath`): strike "It is the only untrusted input this file
  has, and it reached all three of these request lines raw while printTaskLogs escaped the ONE
  id that was..." (history + census). Keep the two-escapers hazard paragraph in full.
- Lines 299-301 (inside `watchJobLogs`): replace "See canonicalJobID for the ONE reader here
  that still needs it, and for why the second one stopped being a reader on 2026-08-30." with
  "See canonicalJobID for the readers that need it."
- Lines 305-320, 344-363, 397-415, 500-540, 590-620, 690-800: apply the rules. In particular
  strike "and the third one used to be indistinguishable from the second", "the only server
  that reaches this line is one that is..." style history, and any "measured"/dated clause;
  keep every sentence stating what a reader/writer of the code must not do and why.

- [ ] **Step 3: Verify and commit**

Phase 2 per-file verification (build, `go test ./internal/cli/... -count=1`, comment-only
hunks, eol). Then:

```bash
git add internal/cli/logs.go
git commit internal/cli/logs.go -m "docs(cli): retrofit logs.go comments to the comment policy

Comments only, zero behavior change. Removed: dated change history, the
format-string census, measurement narratives, and 'only input' completeness
claims. Kept: every hazard sentence, the lockstep-guard pointers, and the
pinning-test citations."
```

### Task 6: python/src/relay/client.py

**Files:**
- Modify: `python/src/relay/client.py` (comments/docstrings only; 347 comment lines of 1094)

- [ ] **Step 1: Rewrite the cursor-quote bound comment (lines ~45-70)**

Replace the block above the 200-character constant with:

```python
# How much of a server-supplied cursor a ProtocolError message may quote.
#
# The cursor is chosen by the SERVER and its length is unbounded, so a message
# that interpolates it whole is unbounded too. No fixed number covers every
# legitimate cursor (a text-sort cursor carries the row's unbounded sort
# value), and cutting one is cosmetic: _quote_cursor reports the true length
# beside the prefix, so a long-but-legitimate cursor stays distinguishable
# from a pathological one.
```

- [ ] **Step 2: Rewrite the page-request-limit comment (lines ~118-140)**

Replace the block with:

```python
    # Bounds the NUMBER OF REQUESTS the log paging loop makes against a server
    # whose next_seq keeps advancing but which never reports the log as
    # drained. Requests is ALL it bounds - not wall clock (httpx's read
    # timeout is per socket read), not bytes (httpx decompresses with no
    # bound), not memory. Do NOT point a reader at Client(timeout=) for
    # those: httpx has no total-time and no response-size setting. See
    # bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout.
```

- [ ] **Step 3: Strike-pass over the rest of the file**

Apply the Phase 2 STRIKE/KEEP rules to the remaining comments and docstrings. Known flagged
instances: "were measured:" narratives (~line 124), "was measured false" (~line 54, handled in
Step 1), "it made every stop below operate on..." history (~line 332), "Every LIST query is
`LIMIT sqlc.arg(page_limit)::int + 1`, and every list handler goes through buildPage"
(~lines 482-483 - a cross-language census; strike it, and if the surrounding argument depends
on it, keep only the conclusion it argued for), "After this removal the only message in
either..." (~line 500, history), "the old position" narratives (~lines 410-415). Keep every
backlog-item pointer and every "do not do X because Y" hazard.

- [ ] **Step 4: Verify and commit**

Phase 2 per-file verification: `cd python && python -m pytest tests/unit -q` green, plus
`ruff check .` and `mypy src` if they are part of the file's normal gate; comment-only hunks;
eol; UTF-8. Then:

```bash
git add python/src/relay/client.py
git commit python/src/relay/client.py -m "docs(python): retrofit client.py comments to the comment policy

Comments only, zero behavior change. Removed: measurement narratives, dated
history, and cross-language censuses of Go list handlers. Kept: hazard
statements (httpx has no total-time/size bound) and backlog pointers."
```

### Task 7: python/src/relay/models.py

**Files:**
- Modify: `python/src/relay/models.py` (docstrings/comments only)

- [ ] **Step 1: Strike-pass**

Apply the Phase 2 STRIKE/KEEP rules. Known flagged instance: "That is an exemption on the
AUTHORING axis and it is the only axis it is" (~line 343) - review the surrounding block;
strike review-narrative and axis-taxonomy prose, keep the constraint it protects. The `Page`
docstring should already cite a named Go guard rather than making claims about Go source
(the 2026-08-29 slice fixed it); verify it still does, and strike anything that has crept back.

- [ ] **Step 2: Verify and commit**

Phase 2 per-file verification (pytest unit lane, comment-only hunks, eol, UTF-8). Then:

```bash
git add python/src/relay/models.py
git commit python/src/relay/models.py -m "docs(python): retrofit models.py docstrings to the comment policy"
```

### Task 8: internal/api/server_counters.go

**Files:**
- Modify: `internal/api/server_counters.go` (comments only; 422 comment lines of 584)

- [ ] **Step 1: Strike-pass**

Apply the Phase 2 STRIKE/KEEP rules. Known flagged instances: "fixed for all four sections
before the first one shipped" (~line 19, history), "'measured' and 'nothing there' are the
same payload there" (~line 56 - KEEP if it states the payload's live ambiguity, strike if it
narrates how the design was chosen; read it and decide by that test), "this is the only place
both are visible" (~line 92 - a completeness claim; strike or ground it in the code structure
visible in this file), "with all three packages green" (~line 205, session narrative), "the
only candidates were len(SweptByWorker)..." (~lines 260-264, design-history narrative; the
backlog pointer on line 264 stays). This file is 72% comment; expect the largest reduction of
the slice. Every sentence saying what a counter means, what its zero value claims, or what a
reader must not conclude from it is a KEEP.

- [ ] **Step 2: Verify and commit**

Phase 2 per-file verification (build, `go test ./internal/api/... -count=1`, comment-only
hunks, eol). Then:

```bash
git add internal/api/server_counters.go
git commit internal/api/server_counters.go -m "docs(api): retrofit server_counters.go comments to the comment policy"
```

### Task 9: internal/netlimit/listener.go

**Files:**
- Modify: `internal/netlimit/listener.go` (comments only; 299 comment lines of 452)

- [ ] **Step 1: Strike-pass**

Apply the Phase 2 STRIKE/KEEP rules. Known flagged instances: "'measured', not 'nothing
there'. Pinned by TestLimitListener_ZeroDisables." (~line 261 - the pinning-test citation
stays; strike any surrounding measurement narrative), "that Close is the only hook that..."
(~line 26 - this states THIS type's own mechanism, likely a KEEP; judge by whether it is
about this code or a census of other code). Keep every sentence about semaphore ordering,
double-close, or fail-closed behavior.

- [ ] **Step 2: Verify and commit**

Phase 2 per-file verification (build, `go test ./internal/netlimit/... -count=1`,
comment-only hunks, eol). Then:

```bash
git add internal/netlimit/listener.go
git commit internal/netlimit/listener.go -m "docs(netlimit): retrofit listener.go comments to the comment policy"
```

### Task 10: internal/relayclient/page.go

**Files:**
- Modify: `internal/relayclient/page.go` (comments only; 144 comment lines of 238)

- [ ] **Step 1: Strike-pass**

Apply the Phase 2 STRIKE/KEEP rules. Known flagged instances: "it did was measured false
against the real encoder... measured, 85 gives 196, 88 gives 200 (the last that fits)"
(~lines 26-33 - this is the Go twin of client.py's cursor-bound block; condense the same way
as Task 6 Step 1: unbounded server value, no number covers every cursor, the quote reports
true length), "once the premise behind it was measured against the wire. Every..."
(~line 197, measurement narrative). Keep the drained/next-cursor fail-closed hazard sentences
and any pinning-test citation.

- [ ] **Step 2: Verify and commit**

Phase 2 per-file verification (build, `go test ./internal/relayclient/... -count=1`,
comment-only hunks, eol). Then:

```bash
git add internal/relayclient/page.go
git commit internal/relayclient/page.go -m "docs(relayclient): retrofit page.go comments to the comment policy"
```

### Task 11: slice verification

**Files:** none modified.

- [ ] **Step 1: Full gates**

Run: `make build` and `make test`
Expected: green. (Comments cannot change behavior; a failure means an executable line was
touched - find it with `git diff` and revert that hunk.)

Run: `cd python && python -m pytest tests/unit -q && ruff check . && mypy src`
Expected: green.

- [ ] **Step 2: Comments-only audit**

Run: `git diff <phase-2 base commit>..HEAD -- internal/ python/src/ | grep -E "^[+-]" | grep -vE "^[+-]{3}" | grep -vE "^[+-]\s*(//|#)" | grep -vE "^[+-]\s*$"`
Expected: empty output (every changed line is a comment line or blank). For Python docstring
edits (lines inside triple-quoted strings do not start with `#`), read those hunks by eye
instead and confirm they touch docstring text only.

- [ ] **Step 3: Line endings and encoding**

Run: `git ls-files --eol internal/api/events.go internal/cli/logs.go internal/api/server_counters.go internal/netlimit/listener.go internal/relayclient/page.go python/src/relay/client.py python/src/relay/models.py`
Expected: every row reads `i/lf`.

Confirm both Python files still decode as UTF-8 (Read them; garbled text = failure).

- [ ] **Step 4: Report backlog candidates**

Collect the backlog candidates recorded by Tasks 4-10 (load-bearing claims struck with no
pinning test) into the task report for the conductor to file via /backlog. Do not file them
yourself.

- [ ] **Step 5: Acceptance greps (spec Phase 2 acceptance)**

Run: `grep -nE "(//|#).*(measured|since 20[0-9][0-9]-|until 20[0-9][0-9]-|on 20[0-9][0-9]-)" internal/api/events.go internal/cli/logs.go internal/api/server_counters.go internal/netlimit/listener.go internal/relayclient/page.go python/src/relay/client.py python/src/relay/models.py`
Expected: no matches, except lines whose date is inside a cited backlog-item filename.

Spot-check the KEEP side: `canonicalJobIDFilter` still states the broadcast fail-open hazard;
`client.py`'s page-limit comment still says what the bound does not cover; `logs.go`'s
`jobSnapshotUnusable` still states both unusable-body reasons.
