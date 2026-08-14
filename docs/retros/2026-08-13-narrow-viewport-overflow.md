---
date: 2026-08-13
topic: narrow-viewport-overflow
branch: claude/pr-merging-session-6aede7
range: origin/main..HEAD (12 commits, 28 files, zero Go, green, not yet merged)
---

# Session Retro: 2026-08-13 - Four causes, two misdiagnoses, and the only evidence that counted was a number taken in a browser

**TL;DR:** Closed `bug-2026-08-12-web-narrow-viewport-horizontal-overflow`, the app-wide horizontal
overflow below roughly 840px. `HoloShell`'s nav shrinks and scrolls, four multi-column bodies got the
`md:` breakpoint that one site in the app already used, the shared `Table` primitive gained a
**required** `minWidth` that publishes one grid string to the header row and every body row and wraps
the `role="table"` subtree in a scroll container, and five non-wrapping breadcrumb/toolbar rows plus
two tab bars learned to wrap. Acceptance is a measurement and it was taken twice by two independent
parties: `documentElement.scrollWidth <= clientWidth` at **375px and 320px on all 17 surfaces, with
populated tables**. Suite 1059 -> 1068. Zero Go.

**The through-line is measurement discipline.** This item was misdiagnosed twice, in opposite
directions, and both errors have the same root: a measurement was taken on the state that was
convenient rather than on the state that was in question. The fix is only trustworthy because the
acceptance criterion was a number in a real browser rather than a green suite - and the single
highest-value review check in the slice was one that asked whether the fix produced any CSS **in a
production build at all**, because every jsdom test would have passed if it did not.

## The item was wrong twice, and both times it measured the convenient state

Read this first; the rest of the document hangs off it.

1. **Original framing (2026-08-12):** fix it once in the shared `Table` primitive. Evidence: page
   totals on schedule detail and the Invites tab. No per-element width was ever taken.
2. **Amendment (2026-08-13):** no, the driver is the header nav. Evidence: the first per-element
   measurement anyone had taken, on `/profile/sessions` - `HEADER` at 523px against a `MAIN` of 391px.
   That surface renders no table at all.
3. **The measurement pass that opened this slice** took per-element widths on eleven surfaces and
   found **both readings incomplete**. The header sets a 494-523px floor on every shell page, and on
   every page with rows present the table exceeds that floor: `/jobs` 763, `/schedules` 728,
   `/admin/users` 607, `/workers` Table view 593.

The header-only reading came from surfaces whose tables were **empty or not rendered**: the Invites
tab in its empty state, `/workers` in its default Grid view, `/profile/*` which has no table. Each of
those is what you get for free on a fresh database.

**An empty-state page and a page with rows are different layouts, and only one of them is the layout
under investigation.** Stated generally, because the specific form is too easy to file away as a
frontend detail: **a measurement taken on the convenient fixture is not a measurement of the system.**
This is the same failure the backend keeps finding in a different costume - a test whose seed already
satisfies the property, a guard whose negative control passes while the feature is inert - and it is
the reason both wrong attributions read as evidence rather than as guesses. Nobody inferred badly.
Everybody measured the wrong thing carefully.

**`/auth` is the clean control and it is the reason the header conclusion is not itself a guess.** It
renders no app shell, and it measured 375 with zero overflow at every width in both passes. A defect
that reaches every page except the one page with no shell is a defect in the shell. Without that row
the 523px floor is a correlation across eleven surfaces; with it, it is an attribution. **A
measurement pass over a defect should include the one surface expected not to have it**, and it costs
one row.

## Phase 1 was spent on a browser rather than on a document

No spec was written. Phase 1 collapsed into Phase 2 for the second consecutive iteration, and the
shape was different enough from the previous one to record.

The cross-generation-401 retro set the condition: **fold Phase 1 into Phase 2 only when the item
already carries alternatives and a recommendation, and only by naming the planner as the party that
must refute it.** Both halves held here. The item carried three lettered options (A, B, C) plus a
B1/B2 split inside one of them, and a Step 0 that said, in bold, measure the header before writing any
code. The plan opens with a section titled "What this plan CORRECTS about the backlog item - read this
first", which refutes both of the item's prior attributions and names the third cause the item never
mentions.

**What is new is where the spec's budget went.** The artifact that replaced the spec was not a
document, it was **a browser session that produced an eleven-row baseline table**. That table is
preserved verbatim in the plan with an instruction not to delete it, and every number in the final
acceptance pass is a comparison against it. This is the right trade for a defect whose entire
statement is a number: a spec would have argued about causes from source, which is precisely the
activity that produced the two wrong attributions.

The condition to reuse: **when the acceptance criterion is a measurement, spend Phase 1 on taking the
baseline, not on writing prose about what the baseline probably is.** And keep the baseline in the
plan, because a before-number that costs a full stack to obtain is more expensive than the fix.

## Four independent causes, no proper subset passes, and a fifth found at 320px

The plan's Decision 4 is one slice rather than a split, and the argument is worth preserving because
"ship it in pieces" is normally the safer instinct:

- Fix the tables only, and the header floor still fails every page.
- Fix the header only, and `/jobs` still measures 763.
- Fix both, and `/jobs/:id` still measures 458 from its own breadcrumb row.

**No proper subset of the four satisfies the acceptance predicate on even one page.** A split would
have shipped two PRs that each leave the item open and the acceptance unmet, and the second would have
had to re-run the same expensive browser pass anyway. The slice is 28 files, and every edit is a class
string or a one-line prop.

Cause 4 - non-wrapping breadcrumb, toolbar and tab-bar rows - **is a cause the item never named, and
it was found only because the per-element pass looked at `MAIN` separately.** `MAIN` alone exceeded
375px on three surfaces that have no table and no multi-column body. It was hiding underneath the
523px header floor: fixing causes 0 through 2 would have moved the failure from `HEADER` to `MAIN` and
the criterion would still have failed. **Fixing the largest contributor unmasks the next one, and a
document-level assertion cannot tell you that is what happened.** The item's own acceptance criteria
had already anticipated this and demanded per-element assertions on `HEADER` and `MAIN`, "which is how
this item spent two measurements pointing at the wrong cause". The criteria were right.

And a fifth: **Task 7's browser pass found a new offender at 320px that no pass at 375px would have
seen.** On `/profile/identity`, a bootstrap admin with no display name renders its email as the 32px
`H1`, and `docSW` measured 367 against a `clientW` of 320. The baseline never caught it because the
baseline recorded `MAIN`'s total width and not the heading's own. The fix chains `min-w-0` from the
wrapping row through the `H1` (itself a flex container) down to the name span so `truncate`'s
`overflow: hidden` has something to constrain against - the same shape as the `UserMenu` toggle fix in
Task 1, arrived at independently on a different page. It carries a paired control asserting the
initials avatar has `shrink-0`, which the comment is honest about: today the truncating name reaches
its floor before the avatar's automatic minimum would bite, so `shrink-0` is the guarantee a
fixed-size tile needs rather than a bug being fixed.

**Measuring at two widths found a cause measuring at one width did not**, and the second width was
cheap. 320px is not an exotic device; it is the narrowest viewport anyone tests.

## Two decisions taken with no design reference, and both are the reviewer's to overrule

The Holo hi-fi (`design_handoff_relay_holo/hifi3-holo-pages.jsx`) is **silent on narrow viewports** -
the measurement pass found no breakpoint, no wrap, and no mobile-nav treatment anywhere in it. There
was nothing to follow. Both decisions below were flagged in the plan and in the PR body as calls made
without a design reference, which is the correct handling and should stay standard.

**Decision 1: the nav scrolls rather than collapsing into a disclosure.** The options were wrap,
scroll, or a hamburger. Scrolling won on four counts, in the order that decided it: it changes nothing
at any width where the content already fits (a scroll container with no overflow renders no scrollbar
and no visual difference, so there is no width at which the design "switches"); the header keeps its
height at every width, where wrapping would move `main`'s offset below roughly 500px; it introduces no
new state, no focus management, no Escape route and no `aria-expanded` contract - a hamburger is a
second `UserMenu`, which is a feature and not a bug fix, and the item explicitly disclaimed shipping a
mobile navigation shell; and **it is three class strings, revertible by deleting them.**

That last property is the one worth generalizing. **When you must make a design call with no designer,
prefer the option whose revert is a deletion.** It converts an unreviewable judgment into a cheap one.

The accepted cost is stated rather than hidden: on Windows and Linux a classic scrollbar appears under
the nav at widths where it actually scrolls, and suppressing it (`[scrollbar-width:none]` plus a
`::-webkit-scrollbar` variant) was deliberately not done, because a visible scrollbar is the
affordance that tells the user the nav scrolls and hiding it is a taste call belonging to whoever owns
the design.

**Decision 2: the scroll container is the `<nav>`, never the `<header>`.** An `overflow` on the header
would establish a scroll container that clips the `UserMenu` dropdown, which deliberately hangs out of
the header over `<main>`. That is not a hypothesis: `HoloShell.tsx`'s header comment records a
275-point hit test behind the current stacking behaviour, and the non-portalled dropdown depends on
it. "Just put `overflow-x-auto` on the header" is the tempting wrong fix, so it got its own assertion -
`expect(header.className).not.toMatch(/\boverflow-/)` - which is the one test in Task 1 that is a real
guard rather than a class pin, and Task 7 re-ran the hit test at 375, 768 and 1280px and got
`occluded: 0` at all three.

**A hazard with a recorded precedent in the repo is worth more than a hazard someone remembers.** The
275-point measurement was written down in a comment eleven days earlier by a different slice, and it
is the reason this decision took minutes instead of an afternoon and the reason the guard test exists
at all.

## The primitive-versus-consumer call was argued on alignment, and a lane checked the argument rather than the code

The item framed the choice as "one edit versus nine". That is the weakest available argument and the
plan declined it.

The real argument: in a 375px container a template like `grid-cols-[90px_1fr_120px_...]` has
**negative** free space, so the `1fr` track cannot take a share of it and falls back to its
content-based minimum. The header row and the body rows are **separate grid containers**, so their
content minimums differ - "NAME" versus a truncating link, whose min-content is 0 - and the columns
visibly desynchronize. A shared min-width keeps free space non-negative, at which point `fr` resolves
identically in both. **That agreement is exactly the property `Table` already exists to own**: its own
header comment says the grid template "travels on a context so the header row and the body rows cannot
be put out of agreement by hand". Option B2 would have required each consumer to apply a min-width by
hand in two places, reintroducing the precise defect class the primitive was built to prevent.

A review lane was asked to verify that reasoning is **correct rather than merely plausible**, and it
did two useful things: it confirmed the CSS argument, and it sharpened it. The stated precondition -
size `minWidth` at or above the sum of the fixed tracks - is **necessary but not sufficient**. Free
space stays non-negative only if the value also clears the `fr` cells' own content minimums, and it
does today **only because every `fr` cell carries `truncate` or `min-w-0`**, which drops that cell's
automatic minimum to 0. That refinement is now written into the primitive (`Table.tsx:30-36`).

Two lessons, and the second is the one this project has not written down before:

- **Verifying a design argument is a distinct review lane from verifying the code.** The code was
  correct and the argument for it was incomplete, and an incomplete argument is what the next person
  inherits when they add the eleventh table.
- **A precondition stated as sufficient is a latent defect even when the code is right.** The
  comment's original phrasing would have let someone add an `fr` cell with no `truncate` and satisfy
  every stated rule.

## The review check that mattered most refuted a risk that would have made the whole slice a no-op

This is the finding of the iteration.

**Tailwind v4 scans source statically.** A class string that is computed rather than written as a
literal emits no CSS. Every one of this slice's fixes is a class string. So there is a failure mode in
which `npm test` is entirely green, every structural assertion passes, every class is present in the
DOM - and **the production bundle contains none of the corresponding rules, so the bug is not fixed at
all**. jsdom does not apply stylesheets; it would never notice.

A review lane built to a scratch `outDir` and read the emitted CSS. Results: **all nine `min-w-[...]`
literals present**, every consumer passing a **module-level literal** rather than a computed string,
and all ten track-sum arithmetic checks correct.

It also found something better than a clean bill of health. The bundle contains
`.min-w-\[NNNpx\]{min-width:NNNpx}` - **invalid CSS, generated from a placeholder written in prose, in
a comment.** That is hard proof that comments feed the scan, from the artifact itself rather than from
the documentation. `Table.tsx`'s comment was rewritten to stop spelling out a placeholder pattern and
to point at each consumer's own `MIN_W` constant instead.

Three things to carry:

1. **When a change is entirely class strings, verify the emitted CSS once.** The unit gate is
   structurally incapable of it. This is a build step and a grep.
2. **A no-op fix and a working fix are indistinguishable to every test in this repo.** That is the
   sharpest possible statement of why a browser lane is not optional on a frontend slice, and it is a
   different argument from the layout one.
3. **Comments are input to the build.** Nobody would have predicted the bundle could be polluted by a
   sentence, and the same mechanism is why a source-scanning guard can be broken by one - see below.

## A guard test was proven breakable by an innocent comment, and the answer was to delete it

Task 4 shipped a source-scanning guard asserting that every `<Table` call site passes `minWidth`. A
review lane reddened it against a **fully compliant** consumer by inserting one JSX comment: the tag
regex stops at the first `>`, and `{/* This <Table> renders jobs. */}` contains one. The scan stripped
`//` line comments and not `/* */` blocks - and this repo writes `{/* ... */}` constantly, six of them
in this diff alone.

The interesting part is the resolution. Two obvious moves were available: patch the scan (strip block
comments) or weaken the rule. **Neither is what shipped.** `minWidth` became a **required** prop, so
`tsc -b` rejects any call site that omits it - including an aliased import (`Table as HoloTable`) and
any file a directory walker never reaches - and **the scan was deleted**, with a comment in its place
explaining that the rule moved into the type system.

The generalization: **when a scan and a type can enforce the same rule, the scan is the weaker one and
should not survive as a belt-and-braces.** A scan is only as good as its pattern; it fails **open** on
a pattern it does not match, and the pressure when it fails **closed** on a compliant file is to
weaken the rule rather than fix the scan. A required prop cannot be pattern-dodged.

Two scans do survive in `responsive.guard.test.ts` (bare `grid-cols-N` and bare `col-span-N`), because
no type can express "a Tailwind class in a string literal must carry a breakpoint prefix". Both now
strip block comments first, both check **per line** rather than per file - so
`grid-cols-2 md:grid-cols-4` is not flagged - and both widened their numeric pattern from `[2-9]` to
`(?:[2-9]|1[0-2])`, because `\b` after a single-character class silently accepted `grid-cols-12` and
`col-span-10`, exactly the two-digit values a wide grid reaches for. **A regex that fails open on the
values most likely to be used is worse than no regex**, and it took a review pass rather than an
author to notice.

## What Was Built

- **No spec.** Phase 1 collapsed into Phase 2 and its budget went to a real-browser baseline pass; see
  above. **Plan** `docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md`, seven tasks, one
  `relay-frontend-engineer`, strictly sequential. Notable for three formats worth copying: the
  eleven-row baseline table with a do-not-delete instruction, the "What this plan CORRECTS about the
  backlog item" section, and the verification table that labels each jsdom test **regression pin**
  versus **real guard** before any of them was written.
- **`web/src/shell/HoloShell.tsx`** - the header gains `gap-3`, the left group `min-w-0`, and the
  `<nav>` `min-w-0 overflow-x-auto` (`:54-62`), under a comment that states the nav-not-header rule and
  points at the 275-point hit test it must not invalidate.
- **`web/src/shell/UserMenu.tsx`** - the container and toggle gain `min-w-0`, the email span
  `truncate`. The other half of the header floor: the toggle renders a full email address, and as a
  flex item its automatic minimum size is that text.
- **Four multi-column bodies breakpointed** (`ScheduleDetailPage`, `WorkerDetailPage` twice,
  `StatSection`), matching `admin/server/ServerTab.tsx`, the one site that was already right.
  `WorkerDetailPage`'s KPI row is deliberately `grid-cols-2 md:grid-cols-4` rather than one-up.
- **`web/src/components/holo/Table.tsx`** - a **required** `minWidth` prop, one grid string published
  to the header row and every body row, and an unconditional
  `<div className="overflow-x-auto" tabIndex={0} role="group" aria-label={...}>` around the
  `role="table"` subtree (`:189-193`). About 40 lines of new header comment carrying the alignment
  argument, the necessary-but-not-sufficient refinement, the ten-consumer clipping audit, and the
  rule that a future row popover must portal.
- **Ten consumers**, each with a `MIN_W` literal beside its `COLS`, sized from its own fixed-track sum
  and kept under its container's width at 1280px so no new scrollbar appears on a maximized desktop.
- **Five non-wrapping rows plus two tab bars** (`AdminTabs`, `ProfileTabs`) gained `flex-wrap`.
- **`web/src/profile/`** - the `H1` `min-w-0` chain and the avatar `shrink-0` control, found at 320px
  by the acceptance pass.
- **`web/src/components/holo/responsive.guard.test.ts`** (new) - two surviving scans, comment-stripped
  and per-line, plus a comment where the third one was, recording why the type system replaced it.
- **Suite 1059 -> 1068 (+9)**, enumerated by reading: 2 in `web/src/shell/` (nav scroll container, and
  the toggle truncation), 3 in `Table.test.tsx` (identical grid string, scroll wrapper placement,
  keyboard-reachable labelled group), 2 in `responsive.guard.test.ts` (`grid-cols`, `col-span`), 2 in
  `ProfilePage.test.tsx` (heading truncation, avatar `shrink-0` control). One planned test and one
  shipped guard were **deleted** during review, both replaced by the required prop.
- **Zero Go, zero SQL, zero proto, zero migration. `web/dist` untouched** - no task runs
  `npm run build`, which the plan declared up front for the fourth consecutive slice.

## Key Decisions

- **One slice, not a split.** No proper subset of the four causes satisfies the acceptance predicate on
  any page. Argued above.
- **`minWidth` is required, not optional.** It shipped optional (Task 3), and review's demonstration
  that the presence guard was breakable by a comment converted it. `tsc` enforces at every call site,
  which is stronger than the scan and let the scan be deleted rather than patched.
- **The scroll container is the `<nav>`, never the `<header>`**, with its own guard test, because an
  overflow on the header clips the `UserMenu` dropdown.
- **The nav scrolls; it does not collapse into a disclosure.** No hi-fi reference; chosen as the most
  conservative and most reversible option; flagged for the human to overrule.
- **The scrollbar under the nav is visible, deliberately.** It is the affordance that says the nav
  scrolls. Suppressing it is a design call, not a bug fix.
- **`md:` is the breakpoint, copied from the one site already doing it right**, rather than inventing
  a second convention.
- **Conventions are enforced by tests, not by comments** - and where a type can do it, by the type. The
  item's "one documented convention for each" requirement is met by
  `responsive.guard.test.ts` plus `TableProps.minWidth`, with the prose next to each.
- **No JavaScript-driven responsiveness.** No `window.innerWidth`, no `useMediaQuery`, no breakpoint
  constants in TypeScript, no new effect. The plan made that a scope guard, which is also why CLAUDE.md
  Invariant 1 ("end the generation before releasing the resource") has no instance in this slice: there
  is no async lifecycle here to get wrong.
- **Task 5 ships no unit test, deliberately.** A `flex-wrap` class assertion would need page-level msw
  scaffolding to prove a string is present, jsdom would never wrap anything, and it would leave five
  brittle pins. Its RED is the baseline table and its GREEN is the browser pass. **Declining to write a
  vacuous green test is a decision, and it belongs in the plan where a reviewer can see it**, not in a
  silent omission.

## Problems Encountered

1. **Review found one real regression this slice introduced, and two lanes found it independently.**
   `StatSection` breakpointed its container to `grid-cols-1 md:grid-cols-2` and left the wide cell's
   modifier at the bare `col-span-2`. Below `md` the explicit grid has one track, so
   `grid-column: span 2` forces an **implicit second track plus a `gap-3` (12px) gutter**, rendering
   the wide card about 12px wider than every sibling - a ragged layout produced by the fix for ragged
   layouts.

   **The existing guard could not catch it**, and the reason is the transferable part: it matches
   `grid-cols-*` only. `col-span-*` is the same bug wearing different clothes - a bare numeric Tailwind
   utility that survives into a single-column layout - and it arrives from the **opposite** direction,
   because here the container was corrected and the child was not. The fix is `md:col-span-2` plus a
   second scan, and the pattern to carry: **when you breakpoint a container, enumerate every child
   modifier that referenced the old track count.** `col-span`, `col-start`, `row-span` and
   `grid-column` are all in that family and only one of them was in this diff.

2. **Two mutation proofs reddened at an earlier assertion than the engineer claimed**, leaving the
   assertions called load-bearing unreached. Both were re-run against genuinely discriminating
   mutations and both are real. This is the same vacuity pattern this project keeps finding, in its
   most specific form yet: **a multi-assertion test that goes RED tells you the test failed, not that
   the assertion you care about failed.** The load-bearing line here is
   `expect(gridOf(row)).toBe(gridOf(header))`, which is the only assertion in the file that
   distinguishes "both rows have a min-width" from "both rows have the **same** one" - the property the
   whole primitive-versus-consumer decision rests on. A mutation that trips `toHaveClass` two lines
   earlier proves nothing about it.

   The rule, now stated concretely enough to apply: **a mutation proof must name the assertion it
   expects to redden and the report must show that assertion's failure output**, not the test name. If
   an earlier line fails first, the mutation is the wrong one - either weaken the mutation or reorder
   the test.

3. **Both browser sessions hit the same environment limit, and it is recorded as a limitation rather
   than papered over.** No screenshot or compositing, and **no real `Tab` keypress**. Everything was
   measured through `elementFromPoint`, `getBoundingClientRect` and `scrollWidth`.

   Consequence for this slice specifically: keyboard reachability of the scroll regions for
   `EnrollmentsTable` and `InvitesTable` - the two tables with **zero focusable elements in any row**,
   whose clipped right-hand columns are reachable only via the wrapper's own tab stop - **could not be
   watched working by anyone**. The response was to stop relying on the thing that could not be
   observed: the wrapper no longer depends on Chromium's implicit scroller focusability (which Safari
   does not grant, and which is an axe `scrollable-region-focusable` violation as shipped) and instead
   carries an explicit `tabIndex={0}`, `role="group"` and an `aria-label` derived from the table's own
   `label` so the two names cannot drift.

   **That is the right move when verification is unavailable: remove the dependence on the unobservable
   behaviour rather than assume it.** The residual honesty is that the replacement is pinned by a jsdom
   attribute assertion, which proves the attribute exists and says nothing about keyboard reachability.
   And **visual confirmation of the nav scrolling and the tab bars wrapping was not obtained by
   anyone.**

4. **jsdom does no layout, so nearly every unit test in this slice can only pin a class string** - and
   the slice handled that by labelling rather than by pretending. The plan's verification table
   assigned each test "regression pin" or "real guard" before implementation; the engineer carried the
   labels into the test comments; and a review lane **audited the labelling** and found it honest, with
   the one caveat that two test titles overstate in isolation while carrying explicit pin disclaimers
   in their bodies.

   **Auditing a test's own honesty is a review lens worth keeping.** It is cheap, it is the only
   defence against a class-string pin being cited later as behavioural evidence, and this is the first
   iteration in this project's history where a lane was asked to do it explicitly.

5. **The acceptance pass found a defect the baseline could not have found**, because it measured at a
   width the baseline did not (320px) and because it recorded per-element widths rather than page
   totals. See the `/profile/identity` heading above. Third distinct instance in this item's history of
   "the measurement's resolution decided what could be found".

## Findings Triage

- **1 real regression introduced by the slice** (Problem 1, `StatSection`'s `col-span-2`), found
  independently by two lanes, fixed before merge with a second guard scan behind it. **0 findings
  against the shipped behaviour of the header fix or the `Table` primitive.**
- **The highest-value finding refuted a risk rather than confirming one** (the Tailwind emitted-CSS
  check). Worth noting because a lane that reports "the risk I was sent to find does not exist, and
  here is the artifact proving it" is doing the job, and its output is easy to undervalue against a
  lane that returns a list.
- **A shipped guard test was proven breakable and then deleted rather than patched.** The stronger
  enforcement (a required prop) replaced it. This is the second iteration running in which the best
  outcome of a review was **removing** a test.
- **Two mutation proofs were re-run and found to redden at the wrong assertion.** Both properties are
  real; the proofs were not proofs. Seventh iteration of "re-running the implementer's own proofs is
  cheap and should stay standard", and the fifth in which it refuted something.
- **The `col-span` regression was invisible to every existing guard**, which is the argument for the
  second scan and against assuming a scan generalizes to its neighbours.
- **A real-browser lane replaced the integration lane on a zero-Go diff**, per the standing rule, and
  ran **twice, independently**. Both passes agree on all 17 surfaces at both widths.
- **The conductor's `/code-review` output was supplied to the lanes as prior findings**, per the
  standing shape.

## Deferred Findings

**Filed this pass (two items, each proposed for human review rather than treated as accepted):**

1. `idea-2026-08-14-table-minwidth-magnitude-is-unchecked.md` (**idea/low**) - the type system enforces
   that `minWidth` is **passed**; nothing enforces that it is **large enough**. A consumer declaring
   `min-w-[400px]` against 700px of fixed track type-checks, renders, and passes all 1068 tests while
   reproducing the exact desynchronization the primitive exists to prevent, invisibly, at every width
   below 700px. The item proposes a dev-only runtime assertion inside `Table` as the recommended option
   (it reads the values actually passed, so it covers aliased imports and files no walker reaches, and
   it runs ten times per `npm test` for free) over a third source scan, and it carries the
   necessary-but-not-sufficient caveat plus the open question of whether the `truncate`/`min-w-0`
   precondition should be checked too. Filed low because all ten values are correct today; the named
   trigger is someone editing a `COLS` template, which changes the fixed-track sum with nothing to
   remind them.
2. `idea-2026-08-14-table-scroll-wrapper-clips-a-row-popover.md` (**idea/low**) - `overflow-x: auto`
   computes `overflow-y` to `auto`, so the wrapper clips on **both** axes. A future row-level actions
   menu, tooltip or inline confirm bubble will be clipped or will grow a vertical scrollbar inside the
   table. The ten-consumer audit that says nothing does this today is thorough, correct, and a
   point-in-time claim by a reader who then moved on. The likeliest trigger is already in the tree:
   `UsersTable`'s 270px three-button ACTIONS column is exactly what gets collapsed into an overflow
   menu. The item deliberately does **not** propose removing the wrapper, and notes that the
   `overflow-y` mechanism is the non-obvious part that should be written into the primitive's comment
   whatever else is decided.

**Amendment applied to an existing item (measurement recorded, framing updated - no scope change):**

- `idea-2026-06-03-web-e2e-harness` gains an update recording the two specific capability gaps the
  ad-hoc browser lane has now failed to close in **three separate sessions**: no screenshot or
  compositing, and no real key events. Both are Playwright table stakes, and the second matters here
  because the `tabIndex={0}` fix was chosen precisely to stop depending on a
  Chromium-versus-Safari divergence that only a multi-engine harness could exercise. The update also
  records that **seven of eight verification points in this slice were obtainable only by a human
  driving a real browser**, and none is protected against regression by `npm test`.

**Considered and NOT filed, with reasons:**

- **The no-screenshot / no-`Tab`-keypress environment limitation as its own item.** It meets the bar on
  evidence - three sessions, a named consequence in each - and it fails on the duplicate check.
  `idea-2026-06-03-web-e2e-harness` **is** its remedy, and its 2026-06-03 framing (auth contract drift,
  redirects) is simply stale rather than wrong. A second file would split one question in two and the
  two would be triaged against each other forever. Amended instead, which is where the evidence is
  useful: it converts a four-month-old idea into an item with three sessions of receipts attached.
- **Suppressing the nav scrollbar at narrow widths.** A taste call on a surface with no hi-fi
  reference. Filing it would put a design decision in an engineering queue and imply the current
  behaviour is a defect; it is a deliberate affordance, argued in the plan. If the human overrules
  Decision 1 this question disappears with it.
- **Column dropping below a breakpoint for the widest tables** (`SchedulesTable`'s nine columns,
  `ReservationsTable`'s eight). The closed item already ruled on this: **scrolling is the honest
  minimum** because it makes every column reachable and hides no data, and deciding which columns
  matter at 375px is a per-table product decision. There is no specific proposal to file - "decide
  something about ten tables" is the kind of item that absorbs triage without converging - and the work
  it would gate is already unblocked.
- **Keyboard access to the table scroll regions**, which the plan listed as a retro follow-up. It was
  **fixed in the slice** after review, not deferred: `tabIndex={0}`, `role="group"` and a derived
  `aria-label` on the wrapper. Recorded here so the plan's follow-up list is not read as outstanding.
- **The `web/dist` rule**, declared inapplicable up front for the fourth consecutive slice, with no
  incident. The practice has stopped producing findings, which is the outcome it was written for.

## Known Limitations

- **No one has looked at this.** Zero screenshots, in three sessions. The nav's horizontal scroll, the
  wrapped breadcrumb and tab-bar rows, and the two design decisions taken with no hi-fi reference are
  all proven as numbers only. `docSW <= clientW` says the page does not overflow; it says nothing about
  whether the result reads well.
- **No real `Tab` press has ever been sent.** The `tabIndex={0}` wrapper fix is pinned by a jsdom
  attribute assertion. Keyboard reachability of the `EnrollmentsTable` and `InvitesTable` scroll
  regions - the two tables where it is the **only** route to the clipped columns - is argued from the
  HTML spec and axe's rule, not observed.
- **Safari has not been opened.** The Chromium-versus-Safari divergence in implicit scroller
  focusability is the stated reason the explicit tab stop exists, and it was read, not tested.
- **`minWidth`'s magnitude is unenforced** and **the row-popover clipping audit has no test**. Both
  filed above. Both are claims in a comment.
- **The 1280px no-regression check is a spot check.** `WorkspacesPanel`'s `min-w-[600px]` against a
  roughly 614px detail column has under 15px of headroom and was checked explicitly; the other nine
  were checked by the wrapper-scrollWidth snippet on the pages they appear on. A layout change to a
  detail column could put a scrollbar under a table with nothing to catch it.
- **jsdom does no layout**, for the fifth consecutive frontend slice, and this is the one where it
  mattered most: not one assertion in `web/` can observe an overflow, a misalignment, or an emitted
  CSS rule.
- **Two design decisions are unreviewed by a human** at the time of writing. Both are flagged in the
  PR body as the reviewer's to overrule and both revert by deleting class strings.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **tenth iteration running**,
  and the highest-yield instance in the arc: the item was wrong about its own cause twice and the plan
  refuted both attributions before a line was written.
- **A backlog proposal is not a contract** - ten for ten. Here the item's *acceptance criteria* were
  right and its *diagnosis* was wrong, which is a new split: the criteria demanded per-element
  assertions precisely because the diagnosis kept failing, so the item contained its own corrective.
- **Plan-supplied tests are untrusted** - honored; the guard test the plan supplied was proven
  breakable by a comment and then deleted.
- **Re-running the implementer's own proofs is cheap and should stay standard** - honored and it paid
  twice (Problem 2).
- **A mutation proof must leave a test behind** - honored, with a new failure mode found: the proof
  must also redden **the assertion it claims to**.
- **Wrong prose about correct code is the dominant defect class** - **eleventh consecutive iteration**,
  in a new location: a placeholder written in a comment was compiled into invalid CSS in the production
  bundle. Comments are not inert here; they are build input.
- **When the Go diff is empty, spend the integration lane on a real browser** - honored, and run twice
  independently. Second consecutive slice where it produced the only real evidence.
- **Fold Phase 1 into Phase 2 only when the item carries alternatives and a recommendation, and only by
  naming the planner as the party that must refute it** - honored, second consecutive iteration, and
  extended: the freed budget went to a measurement rather than to prose.
- **Backlog housekeeping is required scope** - the item was closed and `git mv`d on this branch before
  this pass, with a Resolution that records both wrong attributions rather than only the fix. Second
  consecutive iteration where the close was done rather than left pending.

New from this iteration:

- **A measurement taken on the convenient fixture is not a measurement of the system.** Empty tables,
  default views and pages that render no rows are the state you get for free, and they are a different
  layout. Measure the populated state. **Candidate for durable memory** - the frontend instance of a
  failure this project already knows in three backend costumes.
- **Include the surface expected NOT to have the defect.** `/auth` has no shell and never overflowed;
  that one row converted a correlation across eleven surfaces into an attribution. A measurement pass
  without a control measures a correlation.
- **Fixing the largest contributor unmasks the next one, and a document-level assertion cannot tell you
  that.** Four causes, and the fourth was found only because `MAIN` was measured separately from the
  document. Per-element numbers or nothing.
- **When you must make a design call with no designer, prefer the option whose revert is a deletion.**
  Scroll over disclosure was chosen on four counts and this was the decisive one. It converts an
  unreviewable judgment into a cheap one.
- **When a scan and a type can enforce the same rule, delete the scan.** A scan fails open on a pattern
  it does not match and fails closed on a compliant file, and the pressure under the second is to
  weaken the rule. `minWidth` became required and the guard was removed rather than patched.
  **Candidate for durable memory.**
- **When a change is entirely class strings, read the emitted CSS once.** A computed class name emits
  no rule under Tailwind v4's static scan, which makes a no-op fix indistinguishable from a working one
  to every test in this repo. One build to a scratch `outDir` and one grep.
- **A mutation proof must redden the assertion it names.** A multi-assertion test going RED proves the
  test failed. Name the target assertion, show its failure output, and if an earlier line trips first,
  the mutation is wrong.
- **When you breakpoint a container, enumerate every child modifier that referenced the old track
  count.** `col-span-2` under `grid-cols-1` forces an implicit track and a gutter. The guard watching
  `grid-cols-*` cannot see it, and neither can a reviewer reading the container's line.
- **Verifying a design argument is a distinct lane from verifying the code.** The alignment argument
  for the primitive was correct and incomplete; the refinement (necessary but not sufficient) is what
  the next author inherits.
- **When verification is unavailable, remove the dependence on the unobservable behaviour.** No real
  `Tab` press could be sent, so the wrapper stopped relying on implicit scroller focusability and
  declared an explicit tab stop instead of assuming the browser's.
- **Ask a lane to audit the test suite's own honesty.** Labelling each test "proves behaviour" versus
  "pins a class" before writing it, then having someone check the labels, is the only defence against a
  pin being cited later as evidence.

## Files Most Touched

- `web/src/components/holo/Table.tsx` - the center of the slice. The required `minWidth`
  (`:96-102`), the single grid string (`:131`), the unconditional wrapper with its tab stop and
  `role="group"` (`:189-193`), and roughly 40 lines of new header comment: the alignment argument, the
  necessary-but-not-sufficient refinement (`:30-36`), the ten-consumer clipping audit (`:38-45`), and
  the portal rule. Both filed items point here.
- `web/src/components/holo/responsive.guard.test.ts` - two surviving scans and one deleted one. Worth
  reading for the three defects review found in it: block comments unstripped, a per-file check where a
  per-line check was needed, and `[2-9]` silently accepting two-digit values. Its closing comment is
  the record of why the third scan is gone.
- `web/src/shell/HoloShell.tsx` - `:49-62`, the nav scroll container and the comment tying it to the
  275-point hit test above it. The header comment block at `:29-48` is **untouched, deliberately**.
- `web/src/shell/HoloShell.test.tsx:88` - the one assertion in Task 1 that is a real guard rather than a
  pin: the header carries no `overflow-` class. It passed before the change too, which is correct - it
  guards a regression this task could have introduced.
- `web/src/admin/server/StatSection.tsx:61-74` - the `col-span` regression and its fix, with the
  reasoning written at the site.
- `web/src/profile/ProfilePage.test.tsx:53-88` - the fifth cause, found at 320px, plus the avatar
  `shrink-0` paired control whose comment is candid that it guards a future change rather than a
  present bug.
- `web/src/components/holo/Table.test.tsx:246-328` - the three new tests, including
  `expect(gridOf(row)).toBe(gridOf(header))`, the load-bearing assertion Problem 2 is about, and the
  comment at `:299-304` recording which test was deleted and why.
- The ten consumers - each a two-line diff (`MIN_W` beside `COLS`, `minWidth={MIN_W}` on `<Table`) and
  each carrying its own arithmetic in a comment.
- `docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md` - the baseline table, the corrections
  section, and the pin-versus-guard verification table. All three formats are worth copying.

## Verification

- **This pass had no shell.** Nothing was executed: no `git log`, no `git diff`, no test run. Every
  claim below that could be checked by reading was checked against the worktree.
- **Verified by reading:** `Table.tsx` in full, including the required `minWidth` prop, the single
  `grid` string, the unconditional wrapper with `tabIndex={0}`/`role="group"`/derived `aria-label`, and
  every paragraph of the header comment; all ten consumers passing a **module-level literal** `MIN_W`
  (grep across `web/src` returns exactly ten `MIN_W` constants and ten `minWidth={MIN_W}` call sites,
  plus `PLACEHOLDER_MIN_W` in `Table.test.tsx`); `responsive.guard.test.ts` in full, including the
  comment-stripping helper, the per-line checks, the widened numeric pattern, and the closing comment
  where the deleted call-site scan was; `HoloShell.tsx`'s nav classes and its untouched 275-point
  comment; `StatSection.tsx`'s `md:grid-cols-2` container with the gated `md:col-span-2` cell;
  `Table.test.tsx`'s three new tests and the deletion note; `ProfilePage.test.tsx`'s two new tests;
  one appended test in each of `HoloShell.test.tsx` and `UserMenu.test.tsx`; the presence of
  `narrow-viewport` references in `AdminTabs.tsx` and `ProfileTabs.tsx` (cause 4's tab bars); the full
  text of the closed backlog item and its Resolution; and the full text of the plan.
- **Suite arithmetic reconciled by reading**, not by running: 1059 + 9 = 1068, with all nine tests
  enumerated by file above. The conductor should still confirm the number off the actual run.
- **Reported by the implementing and verifying lanes, not re-run here:** every browser measurement,
  including the eleven-row baseline, the 17-surface acceptance pass at 375px and 320px, the 768px
  re-check of `/schedules/:id`, the 1280px wrapper `scrollWidth` checks, and the `occluded: 0` hit
  tests at three widths; the emitted-CSS build and its nine literals, ten arithmetic checks and the
  invalid `.min-w-\[NNNpx\]` rule; every mutation output, including the two that reddened early and
  their re-runs; the comment-injection demonstration that broke the deleted guard; the green
  `npm test`, `tsc -b` and Go gates.
- **Not verified:** all test results, all mutation outputs, the exact commit count and diff stat of the
  branch, the file set of the diff (inferred from reading the tree, not from `git`), and every browser
  number. Each is attributed above.
- **The backlog item was already closed and `git mv`d to `docs/backlog/closed/` before this pass**, with
  a Resolution recording both wrong attributions, the four causes, and the two follow-ups. The two
  items filed by this pass are in `docs/backlog/` as **proposals**; the human gives final accept. The
  amendment to `idea-2026-06-03-web-e2e-harness` is an appended update section that changes no
  frontmatter and no scope. The exact-file-set check, the final gate run and all commits remain the
  conductor's, per the standing rule that subagent claims are verified against the tree rather than
  trusted.
</content>
