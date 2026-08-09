---
date: 2026-08-09
topic: shared-accessible-table-primitive
branch: claude/pr-merging-session-3f03bb
range: five commits on this branch (four migration slices + the backlog close)
---

# Session Retro: 2026-08-09 - shared-accessible-table-primitive

**TL;DR:** The web app's eight CSS-grid pseudo-tables now share one primitive.
`web/src/components/holo/Table.tsx` provides `Table` + `TableRow` + `TableCell` with declarative
header config and the grid template carried on a React context, so the header row and the body rows
cannot be put out of agreement by hand. All eight consumers migrated, including the three that had
**no table semantics at all** (JobsTable, SchedulesTable, WorkspacesPanel) - the correctness half of
the item, and the reason it was raised from low to medium. `ariaSort` and `sortCaret` exist once,
replacing four duplicated pairs plus ReservationsTable's local `SortHeader`. Frontend-only, zero Go;
web suite **761 -> 776** on the migration, **-> 780** with the review fixes. Review returned **0
high**. What and why are recorded in the closed item's Resolution
(`docs/backlog/closed/idea-2026-06-05-shared-accessible-table-primitive.md`); this retro records what
is worth carrying forward, which this time is mostly about **method**: one refactor run under two
different testing regimes, chosen by whether the change was supposed to be observable.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-09-shared-accessible-table-primitive.md`, **plan**
  `docs/superpowers/plans/2026-08-09-shared-accessible-table-primitive.md` (four sequential slices,
  one frontend engineer; explicitly not parallelizable, since slice 1 creates the module slices 2-4
  import).
- `web/src/components/holo/Table.tsx` + `Table.test.tsx` - the primitive, the two pure helpers, the
  private columns context, and two deliberate throws (orphan `TableRow`, sortable header with no
  `onSort`).
- Eight migrated consumers: `WorkersTable`, `JobsTable`, `SchedulesTable`, `UsersTable`,
  `EnrollmentsTable`, `ReservationsTable`, `TasksTable`, `WorkspacesPanel`.
- Three appended role/row/columnheader/cell count tests, each proven RED against the unmigrated
  component with the failure recorded as evidence rather than claimed.
- One narrowed assertion in `web/src/workers/WorkerDetailPage.test.tsx` - the only sanctioned edit
  outside the five protected test files. See Problem #3.
- `web/src/components/holo/index.ts` + `index.test.ts`, appended to, not rewritten.
- Nothing outside `web/src/`. No Go, no SQL, no proto, no migration, no new dependency.

## Key Decisions

- **The primitive renders no frame.** This is the load-bearing decision and it was forced by
  structure, not taste (Problem #2). The caller keeps its existing wrapper, which made the migration
  visually neutral across four different frame styles, kept the `jobs-table` / `schedules-table`
  `data-testid` and its asserted classes on the same element, and kept footers, error banners and
  dialogs inside the visual surface but outside the `role="table"` subtree.
- **The grid template travels on a React context, not as a prop on both halves.** The defect the item
  names is that "the header row and the body rows must be kept in agreement by hand"; a prop on
  `Table` and another on `TableRow` would have preserved exactly that hazard while looking like a
  fix. The context value is the raw string, never a fresh object literal, so it is referentially
  stable.
- **The base class strings contain only utilities that are byte-identical across all eight
  consumers.** Two competing Tailwind utilities on one element resolve by stylesheet order, not by
  class-attribute order, so a caller `className` cannot reliably override a base class. Everything
  that varies (padding, tracking, border color, text size) is caller-supplied, never
  override-supplied.
- **Cells, empty states, loading states, footers and sort state stay with the caller.** A fully
  data-driven `DataTable` was considered and rejected: `UsersTable` holds inline rename state inside
  a cell, `WorkspacesPanel` owns a `ConfirmDialog`, `TasksTable` rows are selection controls. Every
  one of those comes back as a render prop, which is the caller's JSX with extra indirection.
- **No headless table library.** Sorting is server-side, pagination is cursor-based and already
  solved, nothing is grouped or filtered client-side. What was missing is ARIA structure, which is
  the one thing a headless table library does not supply.
- **`aria-sort` only on sortable headers, no `role="rowgroup"`, no `aria-rowcount`.** A static header
  carrying `aria-sort="none"` advertises an affordance that does not exist; a rowgroup would force
  the primitive to wrap the caller's rows in an element it owns; a row count that cursor pagination
  cannot know is worse wrong than absent.
- **Two unconditional throws instead of two silent degradations.** `TableRow` outside a `Table`
  throws rather than falling back to no grid template, because a silent fallback ships as a mangled
  layout in production while a throw surfaces in the first test render. The same reasoning was
  extended at review to a header with `field` set and no `onSort`.
- **The spec's per-header `className` escape hatch did not ship.** `TableColumn` has `label`,
  `field` and `align` only. No consumer needed the third; an unused extension point on a primitive
  seven files depend on is an invitation to per-caller drift.

## Problems Encountered

1. **The central method: the "byte-identical existing tests" gate.** The five already-roled tables'
   test files had to show a **zero-line `git diff`** after migration. Not "still passing" - unedited.
   The rule stated up front was that if an assertion needed adjusting, that *is* the finding and not
   the fix: behavior changed, and the migration is wrong. It held for all five, `WorkersTable.test.tsx`
   included, which asserts the table role with its name plus exactly 2 rows, 6 columnheaders and 6
   cells. Contrast the three newly-roled tables, where the change is supposed to be observable and a
   green test proves nothing unless it was seen RED first. **One refactor, two testing regimes,
   selected by whether the change was meant to be visible.** This is the transferable idea: before
   writing any test for a refactor, decide which half of that split you are in, because the two
   regimes have opposite failure modes. A behavior-preserving change that lets you touch the tests
   can hide anything; an observable change that reuses old tests proves nothing.
2. **The naive migration would have produced invalid ARIA, and only reading all eight consumers found
   it.** `JobsTable` and `SchedulesTable` render their footer slot inside the same `GlassPanel` as
   the rows, and `WorkspacesPanel` has an error banner and a `ConfirmDialog` as siblings of its rows.
   Putting `role="table"` on the existing wrapper - the obvious move, and the one a per-file
   migration would have made three times - makes each of those an invalid child of a `table` role.
   That is what decided the no-frame design, which in turn is what made the migration visually
   neutral. The lesson is about **when** it was found: at spec time, from reading every consumer
   before shaping the API, not at slice 3 when an API change would have meant rework in seven files.
   A primitive's API is set by its hardest consumer, so the inventory is not a formality.
3. **A test was green only because of the defect this work fixes, and the planner found it.**
   `WorkerDetailPage.test.tsx` asserted page-globally that `queryByRole('row')` and
   `queryByRole('table')` were both absent. Its *intent* was "the reservations panel fabricates no
   rows"; it over-reached to a page-global query, and it passed only because `WorkspacesPanel` had no
   roles at all. The spec did not know about it - it is a sixth test file, outside the five the gate
   protects. The plan recorded the expected failure, narrowed the assertion to "exactly one table,
   named Source workspaces, contributing exactly its header row", and required the failure be
   observed and recorded before the edit. **A gate can be silently satisfied by the bug it should
   catch**, and the tell is an assertion whose scope is wider than its stated intent. Worth a habit:
   when a test asserts the *absence* of something page-globally, ask what would have to become
   correct elsewhere on that page to break it.
4. **All three review lenses independently converged on the same top finding.** `TableRow` and
   `TableCell` spread `{...rest}` *after* `role=`, so a caller - accidentally, or through a
   loosely-typed prop bag - could silently strip the semantics the primitive exists to guarantee.
   Invariants, correctness and security each got there by a different route. **Independent
   convergence is itself evidence**: three reviewers with different briefs landing on one line is a
   much stronger signal than one reviewer's confidence, and it is worth treating as near-certain
   rather than re-litigating. The fix was deliberately two-part: **spread order** (rest first, role
   last, with a comment saying why) **and typing** the props as
   `Omit<ComponentPropsWithoutRef<'div'>, 'role' | 'dangerouslySetInnerHTML'>`. The second half is
   the durable one - it makes the hazard unrepresentable rather than merely discouraged, and it
   closes the `dangerouslySetInnerHTML` hole the spec had flagged in prose with no enforcement. A
   rule that lives only in a comment is a rule the next caller does not read.
5. **Three of the four review fixes originated in plan-supplied code.** Beyond the spread order:
   the plan's `ariaSort` used `sort.replace('-', '')`, which strips the first hyphen *anywhere* in
   the string, and defended itself with a comment asserting "field names contain underscores and
   never hyphens" - a correctness argument resting on an unenforced assumption about every future
   sort field. Shipped code anchors the prefix at position 0 and has a test named for exactly that.
   The plan also optional-chained `onSort?.(field)`, so a header configured with `field` and no
   handler would render a focusable, screen-reader-announced sort button that does nothing; and it
   keyed headers by `h.label`, which warns on duplicate labels. All three now have named tests. This
   is the standing **"plan-supplied code and tests are untrusted"** lesson, confirmed again, with the
   sharper form: the plan's *comments* are untrusted too. A stated assumption is not a guard.
6. **A conditional fix was correctly attempted and correctly reverted.** Review proposed
   `aria-hidden` on the sort caret glyph and predicted it would affect one protected test file, via a
   substring match it expected to survive. It actually broke **three**: the caret is asserted as part
   of a button's accessible name in `WorkersTable`, `UsersTable` and `EnrollmentsTable` tests. The
   engineer reverted per the zero-line-diff constraint rather than editing the protected files, and
   the change was filed as `idea-2026-08-09-sort-caret-in-accessible-name`. Two things worth keeping.
   First: **a reviewer's estimate of blast radius is a hypothesis; the gate is the instrument that
   measures it** - and the estimate was wrong by 3x in the direction that matters. Second: the
   constraint did its job precisely by being inconvenient. A softer rule ("adjust the tests if
   needed") would have turned a real signal about accessible-name coupling into three quiet test
   edits.
7. **A Tailwind v4 detail with teeth: a class literal inside a code comment is a scanner candidate.**
   The primitive's prop documentation originally contained a literal `grid-cols-[...]` inside a
   comment. Tailwind v4's static scan does not parse TypeScript, so it saw a candidate and emitted a
   real, garbage CSS rule - `.grid-cols-\[\.\.\.\]{grid-template-columns:...}` - into the shipped
   bundle. Verified present pre-fix and absent post-fix rather than argued. Reworded to describe the
   syntax instead of demonstrating it. **Comments are not inert in a scanner-based toolchain**, and
   the general form covers any content-scanning build step: Tailwind, dead-code elimination by
   string match, i18n key extraction, secret scanners. If a tool greps your source, your prose is
   input.
8. **The original backlog item carried an acceptance criterion that was unsatisfiable as written.**
   "No file declares a `COLS` constant the primitive could own" cannot be met: Tailwind v4's static
   scan requires the `grid-cols-[...]` literal to remain in the consumer file, so the template is
   per-table and must stay there. It was corrected **during specification**, restated as "declared
   once, applied to one element, passed as `columns`", and met in that form. The good outcome is the
   timing - caught by an artifact whose job is to be checked against the code, not discovered by an
   engineer mid-slice with an acceptance criterion they cannot satisfy and no authority to change.
   Fifth-plus instance of **verify a backlog item's technical claims against the code during spec**,
   and the first where the failure was in the *acceptance criteria* rather than in the item's factual
   claims.
9. **Conductor process decision: one change set with four sequenced commit groups, overriding the
   spec's four-PR recommendation.** The stated grounds were that the byte-identical-tests gate
   already supplies most of the reviewability the slicing was meant to buy, and that the item's
   acceptance requires all eight consumers before it can close. Assessed honestly: **the call was
   right, but for one of those two reasons rather than both.** The gate argument holds - a reviewer
   reading this diff starts from `git diff --stat` on the five protected files, and an empty result
   collapses five of the eight migrations to "structural, provably", which is exactly what slicing
   was meant to make legible. The acceptance argument is weaker: nothing prevents an item from
   staying open across several merges, and slices 1-3 were each independently green and mergeable by
   the plan's own halt-safety rule. The residual cost is real and should be named: the three
   newly-roled tables are the user-visible value here, and they are reviewed in the same sitting as
   five mechanical refactors, which is where attention is scarcest. The mitigation that made it
   acceptable was commit hygiene - four clean slice commits plus the backlog close, so the history
   is still reviewable slice by slice even though the change set is not. **Rule worth carrying: an
   override of the spec's sequencing is fine when the reviewability it trades away is replaced by
   something mechanical, and the replacement should be named in the plan** (it was, in "Slice
   independence declaration"). Do not read this as license to collapse slices by default.

## Findings Triage

- **0 high.** Fifth consecutive iteration with none. The pattern continues to hold that findings
  track novelty rather than diff size: this is the largest-surface change in recent memory (13 files
  modified, 8 of them independent consumers) and every finding landed in the one genuinely new file.
- **The convergent top finding (Problem #4)** was the only one all three lenses raised, and it was
  fixed twice over - ordering plus type-level prohibition.
- **Four review fixes, all in the primitive, all with named tests**: a caller-supplied role cannot
  win; the descending prefix is anchored; a sortable header without a handler throws; duplicate
  header labels do not warn. Suite 776 -> 780.
- **One review proposal correctly rejected on evidence** (Problem #6), filed instead.
- Remaining lows were triaged into the follow-up items below, or accepted as minor.

## Known Limitations

- **`TasksTable`'s `aria-selected` is inert.** Its rows carry `aria-selected` under `role="table"`,
  where assistive technology ignores it, because selection is only meaningful in `grid`/`treegrid`.
  Pre-existing, deliberately unchanged (the refactor half must be behavior-preserving), and a real
  fix means `role="grid"` plus a keyboard navigation model. Filed as
  `idea-2026-08-09-tasks-table-grid-role-selection`.
- **The sort caret glyph is part of each sort button's accessible name.** Three tables' tests assert
  it, so screen readers announce the glyph after the label. Filed as
  `idea-2026-08-09-sort-caret-in-accessible-name`; Problem #6 is why it is a follow-up and not a fix
  here.
- **Accessible-name loose ends**: `RevokedWorkersTable` is now the only unnamed table in the app,
  and `WorkspacesPanel`'s hardcoded `label` is kept in agreement with its `Panel` title by hand.
  Filed as `idea-2026-08-09-table-accessible-name-consistency`.
- **Four of eight tables still hand-roll the glass frame, and the header row carries three different
  spacing strings.** The three admin tables inline `GlassPanel`'s base minus its shadow;
  `WorkersTable` uses a pre-gradient-upgrade flat `bg-white/5`; `headerClassName` is
  `px-4 py-3 tracking-wider`, `px-4 py-2 tracking-wider` or `px-[18px] py-3 tracking-[0.16em]`
  depending on the table. All excluded here because every one of them is a *visible* change that
  would have spoiled the behavior-preserving proof. Filed this iteration, see Backlog Triage.
- **Neutrality was proved for semantics and asserted classes, not for pixels.** The suite asserts
  roles, accessible names and a handful of specific classes; it does not assert most row padding, and
  the repo has no visual regression harness (`idea-2026-06-03-web-e2e-harness` remains open). The
  defenses used instead were the byte-identical-base rule and a reviewer diffing the class strings
  before and after. Note the one known class-attribute-order change: `headerClassName` now trails the
  base instead of preceding it. The class *set* is identical, every existing assertion is
  `toHaveClass` (set-based), and Tailwind resolves by stylesheet order regardless - but that is an
  argument, not a screenshot.
- **No keyboard navigation model, no `role="grid"`, no `aria-rowcount`/`aria-colcount`, no
  virtualization.** All explicit non-goals. Every table renders a full cursor-paginated page (about
  50 rows) and this change adds one wrapper element per table plus one context read per row.
- **The primitive does not validate that `headers.length` matches the grid track count.** It holds in
  all eight today and a mismatch is a real bug, but enforcing it means parsing an opaque Tailwind
  class string at runtime. The check lives in each table's test as a columnheader count instead,
  which is why those counts are load-bearing rather than decoration.

## Backlog Triage

- Filed earlier in the iteration, from review findings deliberately not folded in:
  `idea-2026-08-09-tasks-table-grid-role-selection`,
  `idea-2026-08-09-table-accessible-name-consistency`,
  `idea-2026-08-09-sort-caret-in-accessible-name`.
- Filed with this retro: **`idea-2026-08-09-table-visual-harmonization`**, covering the spec's
  follow-ups 2 and 3, which had been proposed and left unfiled. Both are visual-only and both are
  now cheap for the first time, because the structure is shared: adopting `GlassPanel` in the four
  hand-rolled frames, and settling on one header spacing/tracking pair. Filed because the evidence is
  counted and grep-verifiable, not because the design call has been made - the item says explicitly
  that it needs a design decision first.
- **Proposed and deliberately not filed:** the test-`QueryClient` `retry: false` versus production
  `retry: 1` convention. Suite-wide, debatable, and not this change set's problem; if it is ever
  worth addressing it is one deliberate decision about the shared test harness.

## Improvement Goals

Carried forward from the recent iterations:

- **Treat the plan as an untrusted source of test design and plan-supplied code** - **honored, and
  widened again** (Problem #5). Three of four review fixes were plan-supplied code, and one of them
  was defended by a plan comment stating an assumption as if it were a guard. Read the goal as "the
  plan's code, its tests, and its reasoning are all drafts".
- **Verify a backlog item's technical claims against the code during spec** - **honored**, and this
  time the defect was in the item's *acceptance criteria* rather than its facts (Problem #8). The
  inventory itself was re-derived from the tree rather than trusted, and it held at eight/five/three.
- **A green test can be vacuous; prove RED against the real exposure** - **honored, in both
  directions.** The three newly-roled tables were RED-proven with the failure recorded as evidence,
  and the five refactored ones were held to a stricter gate than passing (Problem #1). Problem #3 is
  the inverse case the standing lesson did not previously cover: a test green for the wrong reason.
- **Independently re-verify the tree and re-run the green gate** - **honored.** Suite, build and both
  acceptance `rg` sweeps re-run on the settled tree; the five-file `git diff --stat` checked against
  the branch merge base, not just against the last commit, so no earlier slice could have touched
  them.
- **Rewriting a shared test file is coverage-losing** - **honored, and elevated to the iteration's
  primary gate.** Five protected files at zero lines; `index.test.ts`, `JobsTable.test.tsx`,
  `SchedulesTable.test.tsx` and `WorkspacesPanel.test.tsx` appended to; one file narrowed with its
  failure recorded first.
- **An overlay owns its own error surface** - **n/a as written, but its structural cousin drove the
  design.** `WorkspacesPanel`'s error banner and `ConfirmDialog` had to move out of the table subtree
  (Problem #2), which is the same class of question: what does this element's container mean, and is
  this child valid inside it.
- **Teardown ends the generation first** - **n/a by construction, and stated as such in the spec.**
  The primitive holds no state, runs no effect, opens no subscription and owns no `AbortController`.
  Any future proposal to give it internal async state should be read against that sentence.
- **A recovery bound must be time-based / diagnose a red gate / a wrong contract in docs is a defect
  / calling the clear function is not evidence / bound error logging on a hot path** - **n/a.** No
  recovery loop, no red gate, no client-facing contract, no secret, no Go.
- **Phase 4 runs the documented pipeline** - **honored, second time.** Conductor `/code-review` fed
  to three parallel `relay-code-reviewer` lenses. The integration lane was skipped on the correct
  ground that the diff contains zero Go.

New from this iteration:

- **Choose the testing regime by whether the change is meant to be observable, and say so before
  writing a line.** A behavior-preserving refactor's gate is *the tests did not change*; an
  observable change's gate is *the test was seen failing first*. Running one change under both, split
  by consumer, is what made an eight-file migration reviewable. **Candidate for durable memory** - it
  generalizes to every refactor-plus-fix change set, which is most of them.
- **Independent convergence is evidence.** Three review lenses with different briefs landing on the
  same line should raise confidence the way three independent measurements do, and should shift the
  fix from "discourage" to "make unrepresentable" - here, `Omit<..., 'role'>` rather than a comment
  about spread order. **Candidate for durable memory.**
- **A reviewer's blast-radius estimate is a hypothesis; the gate measures it.** Predicted one file,
  actually three (Problem #6). Do not let an estimate authorize an exception to a constraint; run the
  constraint and let it decide. **Candidate for durable memory**, and it pairs naturally with the
  existing "diagnose a red gate, measure both ways" note.
- **In a scanner-based toolchain, comments are input.** A `grid-cols-[...]` literal inside a code
  comment emitted a real garbage CSS rule into the bundle (Problem #7). **Candidate for a one-line
  addition** to the frontend build notes rather than a standalone memory - it is narrow, but it is
  invisible and it shipped.
- **A stated assumption is not a guard.** The plan's helper was correct only under "field names never
  contain hyphens", asserted in a comment with nothing enforcing it. Where the cost of not depending
  on an assumption is one line (anchor the prefix at index 0), do not depend on it. Belongs as a
  sentence in the existing plan-supplied-code note.

## Files Most Touched

- `web/src/components/holo/Table.tsx` - the whole of the new surface, and where every review finding
  landed. Its header comment carries the four decisions most likely to be undone by a well-meaning
  later edit: no frame, byte-identical base classes only, the context rationale, and a pointer to
  `RevokedWorkersTable` as the in-repo precedent for when a native `<table>` is the right tool
  instead. The `rest`-before-`role` ordering carries its own comment because the ordering looks
  arbitrary and is not.
- `web/src/components/holo/Table.test.tsx` - 16 tests, including the four added at review. The
  interior-hyphen, caller-role, missing-`onSort` and duplicate-label tests are each named for the
  exact hazard they pin, so a future simplification that reintroduces one fails with a message that
  explains itself.
- `web/src/jobs/JobsTable.tsx` and `web/src/schedules/SchedulesTable.tsx` - the correctness half on
  the two highest-traffic pages. Both carry the comment explaining why `{footer && ...}` sits after
  `</Table>` and inside the `GlassPanel`, which is the arrangement Problem #2 forced.
- `web/src/workers/WorkspacesPanel.tsx` - the hardest of the three newly-roled ones: empty state,
  error banner and `ConfirmDialog` all had to become siblings of the table rather than children.
- `web/src/workers/WorkersTable.tsx` - the reference migration, chosen because its test file holds
  the strongest existing semantic assertions in the repo and because its odd-one-out frame proved the
  caller-owns-frame decision on the hardest case.
- `web/src/admin/reservations/ReservationsTable.tsx` - where a local `SortHeader` component was
  deleted. Someone had already reached for exactly this abstraction, one file wide; that was the
  strongest in-repo evidence for the shape the primitive took, and it is worth noticing that the
  signal existed months before the extraction happened.
- `web/src/workers/WorkerDetailPage.test.tsx` - the single narrowed assertion, with a comment saying
  why the scoped form is the right one (Problem #3).

## Verification

- Full web suite green: **780 tests, up from 761** (761 -> 776 on the four migration slices, exactly
  the +15 the plan predicted; -> 780 with the four review fixes). Production build green
  (`tsc -b && vite build`), with `git checkout -- web/dist/` before the change set was assembled.
- Both re-run by the conductor on the settled tree rather than trusted from the implementer's report.
- **The zero-line-diff gate**: `git diff --stat` empty for `WorkersTable.test.tsx`,
  `UsersTable.test.tsx`, `EnrollmentsTable.test.tsx`, `ReservationsTable.test.tsx` and
  `TasksTable.test.tsx`, checked against the branch merge base as well as against `HEAD`.
- **RED proofs recorded, not claimed**, for `JobsTable`, `SchedulesTable` and `WorkspacesPanel`, plus
  the expected `WorkerDetailPage` fallout before it was narrowed.
- **Acceptance sweeps**: `rg 'function ariaSort|function caret' web/src` returns exactly one file;
  the role-attribute sweep returns matches only inside `Table.tsx`, taking 70 hand-written ARIA
  attributes across five files to zero outside the primitive; every table file declares its
  `grid-cols-[...]` literal exactly once.
- **The bundle check for Problem #7**: the garbage `.grid-cols-\[\.\.\.\]` rule confirmed present in
  the built CSS before the comment reword and absent after.
- Change set confirmed to be entirely under `web/src/`: no Go file, no `.sql`, no `.proto`, no
  migration, no `web/dist`.
- Code review: conductor `/code-review` fed to three parallel `relay-code-reviewer` lenses
  (invariants, correctness, security). 0 high. The integration lane was skipped on a zero-Go diff.
- No Go changed, so none of the six Invariants was in play. The frontend analogues were respected:
  no component gained a direct `fetch`, no request path changed, and the generation-ordering
  invariant has nothing to bite on - the primitive is stateless and effect-free by design.
