---
date: 2026-08-14
topic: cursor-pager-hook
slice: 2026-08-14-cursor-pager-hook
branch: claude/pr-merge-main-2d2fc3
range: origin/main..HEAD (frontend only, zero Go, green, not yet merged)
closes: idea-2026-08-13-cursor-pager-hook
---

# Session Retro: 2026-08-14 - Four refutations in a row, and a gate that was decorative on six of the wirings it was supposed to license

**TL;DR:** `useCursorPager` now lives at `web/src/lib/useCursorPager.ts` and all seven paginated SPA
surfaces use it - `JobsPage`, `WorkersPage`, `SchedulesPage`, and the `UsersTab` / `EnrollmentsTab` /
`InvitesTab` / `ReservationsTab` admin tabs. Frontend only, zero Go. Suite 1102 -> 1116 across 152
test files.

**The extraction was an afternoon. The document below is about the four artifacts that each refuted
the one before it, and about what the refutation chain did not reach.** The backlog item's central
premise was false; the plan refuted five of the spec's claims; four review lenses then found what the
plan and a clean high-effort `/code-review` both missed; and the re-verify pass refuted nothing,
which is the only link that was allowed to hold.

**The finding of the iteration is that the gate this whole slice rests on was void on six wirings.**
The refactor's entire licence is "no test file was edited and the suite is green, therefore behaviour
is unchanged". On six of the migrated wirings that argument proved nothing, and the only way anyone
found out was by mutating the source and watching the suite stay green.

## The refutation chain, link by link

Read this first; the rest hangs off it.

### Link 1 - the item's central premise was false

`idea-2026-08-13-cursor-pager-hook` says all seven copies are character-for-character faithful, and
of `SchedulesPage` specifically: "It calls the stack `cursorStack` and the functions `goNext`/`goPrev`.
**The bodies are the same; only the identifiers differ.**"

The spec opened all seven at HEAD. **Four are byte-identical. `SchedulesPage` is a different
algorithm:** three state pieces rather than four, `cursor` **derived** from the stack rather than
stored, `goNext` pushing `data.next_cursor` (the **destination**) where the canonical `next` pushes
the current cursor (the **source**), and `goPrev` popping and **discarding** the value the canonical
`prev` pops and assigns. `JobsPage` and `WorkersPage` had no `resetPaging` at all - the first inlines
the four setters twice, the second has no reset because its list has no sort control.

A mechanical rename would have produced wrong code. A reviewer told "only identifiers differ" would
have waved it through - and the item was, at that moment, the only artifact a reviewer had.

**The item's own summary sketch is the `SchedulesPage` shape presented as the shared block.** It
writes `const cursor = stack[stack.length - 1] ?? ''` and "next(): push data.next_cursor", which is
the deviant algorithm, not the canonical one. **This is the same defect class as the owner-email item
whose code sketch contained the exact bug its prose warned against** (2026-08-14, two iterations
back). Twice now, in one week, an item's illustrative snippet has been the thing the item was wrong
about. A prose claim gets verified; a snippet gets copied.

### Link 2 - the plan refuted five of the spec's claims

The spec was treated as an artifact, not as authority, and re-derived rather than copied forward.
Two of the five matter beyond this slice:

- **The RED prediction for the key hook test named the wrong assertion and the wrong value.** The
  spec said a hook pushing the destination cursor "passes the forward walk and fails at the second
  `prev`, with `cursor === 'CUR1'` instead of `''`". It fails at the **first** `prev`, with `'CUR2'`.
  The test still catches the bug; the prediction of how was wrong. **An implementer who confirmed
  "the RED matched the plan" would have been accepting a different failure than the one described** -
  which is precisely the failure mode the narrow-viewport slice found when two mutation proofs
  reddened at an earlier assertion than claimed. Same lesson, arriving from the opposite direction:
  there the proof was run and misattributed, here the attribution was written before anyone ran it.
- **A test justification claimed coverage that cannot exist.** The spec argued that asserting only
  `cursor` in the `resetPaging` test "passes against a reset that forgets `offsets`". No test can
  catch a forgotten `setOffsets([])`: `offsets` is popped only while `stack` is non-empty and `next`
  pushes exactly one offset per stack entry, so a stale prefix is dead weight the pops never reach.
  Mutation M7 confirmed it - deleting the line reddens nothing. The hook keeps the call anyway (the
  state stays honest) and `useCursorPager.ts:100-107` now says in the source that no test covers it.
  **Writing down that a line is uncovered is worth more than a test that pretends otherwise.**

The plan also found that the spec's twelve-file gate set had been enumerated **without recording four
other files that meet its own admission criterion**, and responded by making the primary gate
enumeration-free: not "these twelve are unchanged" but "no test file other than the new one appears
in the diff at all". That instinct was right, and section "The enumeration was wrong and it did not
matter" below shows it paid twice.

### Link 3 - four lenses caught what the plan and a clean `/code-review` both missed

`/code-review` at high effort reported **zero findings**. The four Phase 4 lenses then produced the
two most valuable findings in the slice. **Treating a clean review as a lead rather than a verdict is
what earned them.** A tool that returns nothing has told you where it looked, not that there is
nothing there.

### Link 4 - the re-verify held

Nothing in the findings was refuted by the engineer on the way back. That is the only link in the
chain that was allowed to stand, and it is worth noting explicitly: the chain terminated because
someone checked, not because anyone ran out of budget.

## The zero-diff gate was decorative on six wirings

This is the finding of the iteration. Two lenses found it independently, and both mutation-proved it.

The gate is `reference_refactor_gate_byte_identical_tests`: require a zero-line diff to the existing
test files, because an assertion needing adjustment **is** the finding. The gate held perfectly - not
one of the pre-existing test files changed by a line. And on six wirings it licensed nothing:

| Mutation | Result |
|---|---|
| Delete `resetPaging()` from `SchedulesPage.chooseSort` - leaving that file with **zero** reset sites | 11/11 green |
| Delete `resetPaging()` from `JobsPage.pickSort` | 13/13 green |
| Delete `resetPaging()` from `JobsPage.pickFilter` | 13/13 green |
| Delete `resetPaging()` from `UsersTab.pickEmail` | 23/23 green |
| Pass a bogus `pageSize` of `999` in `WorkersPage` | 13/13 green |
| Pass a bogus `pageSize` of `999` in `ReservationsTab` | 17/17 green |

Four of the nine `resetPaging` call sites and two of the seven `pageSize` arguments were unconstrained
by any test. **The refactor's entire licence is "no test file was edited and the suite is green,
therefore behaviour is unchanged" - and that argument was void on exactly those six.** Deleting a
whole surface's only cursor-reset would have shipped a server 400 on every sort change with a fully
green suite behind it.

The generalizable lesson, which is the reason this section is first among the findings:

> **A zero-diff gate proves you did not weaken the tests. It says nothing about whether the tests
> constrained the thing you changed.** Verifying that the gate *held* is not the same as verifying
> that it is *load-bearing*, and the only way to tell those apart is to mutate the thing the gate is
> supposed to be protecting.

The gate is still the right gate - it is what stopped anyone "fixing up" an assertion, and it is why
the migration of a genuinely different algorithm is trustworthy. But it is a **negative** control. It
proves an absence. The positive question - does any test fail if this specific wiring is wrong? - has
to be asked separately, per wiring, and nobody in this project had asked it before.

The remediation is six tests in five new sibling files (`*.pager.test.tsx`), each of which drives the
wiring the gate could not see: page forward, then change the sort / filter / email and assert the
request carries **no** cursor; or page onto a partial last page and assert the footer's absolute
range. Each carries a comment naming the gate-frozen sibling and stating why that file's existing
tests stay green under the mutation - `JobsPage.pager.test.tsx:10-15` is the model.

## A brand-new test did not discriminate the bug in its own title

`useCursorPager.test.ts` shipped with a test called **"paging back off a partial last page restores
the previous offset, not pageSize * depth"**. It walks `next('CUR1', 50)` -> `next('CUR2', 13)` ->
`prev()` and asserts `startOffset === 50`.

After the pop, `stack.length` is 1 and `1 * 50 === 50`. **The naive formula the test is named after
and the correct answer coincide.** The test is green under the bug it exists to catch.

The near-miss is what makes this worth recording. The plan was unusually rigorous here: a
seven-mutation battery (M1-M7), each with a predicted RED naming a specific test and value, each
required to be observed rather than assumed. It still left this hole, because **M2 mutated `next`'s
accumulation and nothing mutated `prev`'s restore**. The write half of the pair was proven and the
read half was not, and a battery organized by function rather than by property does not announce
that.

The fix is a second test walking three pages at three distinct sizes (13, 50, 7), chosen so that
neither wrong formula can coincide: `copy.length * 50` fails because no page size doubles as "the"
page size, and `startOffset - 50` fails because the third size breaks the arithmetic accident. The
reasoning is written above the test at `useCursorPager.test.ts:90-101` rather than left in a review
comment, so the next person to edit it knows which coincidences the input values are defending
against. **A mutation proof must leave a test behind, and a discriminating input must leave its
reasoning behind too** - the numbers 13, 50 and 7 are load-bearing and look arbitrary.

The general form, which the mutation battery should adopt next time:

> **Enumerate mutations by property, not by function.** For every symmetric pair - push/pop,
> write/read, set/clear, encode/decode - mutate both halves. Proving the writer is correct proves
> nothing about the reader that has to agree with it.

## The doc comment laundered the refuted premise back in

Four claims in the new hook's doc comment were false. The hook is correct; the paragraph above it was
not. **Ninth consecutive iteration in which wrong prose about correct code was a leading defect.**

The sharpest one: the comment opened with **"Seven list surfaces used to carry a copy of this each"** -
the item's central premise, refuted by the spec, refuted again by the plan, and then written into the
one artifact that outlives the item, the spec, the plan and all seven copies. It is the same shape as
the phantom bug citation the plan caught at S3 (three shipped source comments citing
`bug-2026-06-21-jobs-pagination-footer-absolute-range`, which is the **schedules** item; the jobs one
is `bug-2026-06-05-...`, and the citation conflated their dates).

**A refutation does not stay refuted.** It has to be re-checked in every artifact the work produces,
including the ones written after the refutation, because the false claim is the one everybody already
knows how to say. The shipped comment now reads accurately: five byte-identical copies, `WorkersPage`
under prefixed identifiers with no reset call, and `SchedulesPage` stacking destination cursors -
`useCursorPager.ts:3-9`. The `resetPaging` doc corrected "six trigger conditions" to the measured
"9 call sites across 6 surfaces, on four distinct trigger conditions".

One good outcome worth naming: the stale `bug-2026-06-21-jobs-...` citation no longer appears
anywhere in `web/src` - the comments carrying it were the ones this slice deleted, and the hook's own
comment cites both closed items correctly. The remaining copy is in the backlog item itself
(`idea-2026-08-13-cursor-pager-hook.md:68`), which is about to be closed; the Resolution should not
repeat it.

## This diff invalidated 13 line-number citations that were accurate on `origin/main`

Two of them ran past EOF. **Second time in three iterations that editing a file stranded citations of
that file** - the cross-generation-401 retro is the prior instance, where a comment the diff added
was invalidated by the same diff before it was committed.

The sharpest instance here is a two-line collision: `InvitesTab.tsx:14` cited
`EnrollmentsTab.tsx:16-21`, and this diff **rewrote the comment block two lines below that citation**
(correcting the FOURTH -> FIFTH off-by-one the spec found) while silently invalidating the citation
directly above it. The corrective and the new defect were in the same edit, two lines apart.

The fix was not renumbering. **Citations inside the change set were converted to symbol names** -
`InvitesTab.tsx:14` now reads "Same shape as EnrollmentsTab's toggleSort (EnrollmentsTab.tsx)" with
no line range. A symbol name cannot drift; a line number drifts every time anyone edits above it,
silently, with no test and no compiler to notice.

> **Prefer `File.tsx`'s `symbolName` to `File.tsx:16-21`.** A line range is a claim that goes stale
> on somebody else's diff and reddens nothing when it does. This project has now produced the same
> defect in three consecutive slices under three different disguises.

## A concurrent-agent hazard the conductor created, recorded because it is the conductor's to own

The four Phase 4 lenses ran in parallel **in the shared worktree**, and one of them was instructed to
mutate source to test the gate. It did so from an untracked `web/src/lib/__mut__/` directory. A
second lens, running concurrently, observed `useCursorPager.ts` modified mid-run and a full suite at
**16 failed / 1100 passed** - and correctly diagnosed contamination rather than a regression by
re-measuring from a clean detached worktree. Both lenses reported it.

Nothing shipped wrong. But the second lens spent real budget proving that the tree it was reading was
not the tree under review, and the only reason its report is trustworthy is that it went and got a
clean one.

> **Mutation testing needs an isolated tree.** A lens that writes to source cannot share a worktree
> with lenses that read it. Either serialize the mutating lens or give it its own checkout. This is a
> dispatch-shape rule, not an agent-behaviour rule: both agents did exactly the right thing.

## A red gate was measured both ways rather than waved through

The conductor's first full run showed **6 timeout failures across 5 files at 104.9s**. `origin/main`
ran green at 39.7s and the branch ran green at 37.6s when the machine was quiet.

Two of the failing files were **in the gate set**, so "unrelated flake" was not assumable - a red in a
gate-set file is exactly the signal the gate exists to produce. The measurement was taken with and
without the change (`feedback_diagnose_a_red_gate`) and the diagnosis is the already-filed
`idea-2026-08-13-web-suite-waitfor-flakiness-under-concurrency`: `waitFor` timeouts under CPU
contention, not a behaviour change. The branch being **2.1s faster** than main on a quiet machine is
the corroborating number.

No new item; the existing one now has a second slice's worth of evidence and a concrete trigger
(four parallel lenses plus a full suite on one machine).

## The enumeration was wrong and it did not matter, twice

The spec enumerated twelve gate files. The plan checked, kept the twelve, found four more files that
meet the same admission criterion (they render `AppRoutes`) but mount no migrated surface, and then -
correctly - **refused to let the primary gate depend on the enumeration at all**.

It was right to. **The true count is at least thirteen.** `web/src/admin/AdminTabs.test.tsx` imports
`web/src/admin/tabs.ts`, which imports all four migrated admin tab components at module scope
(`tabs.ts:2-6`). The plan's method - grep every test file for a **direct** import of a migrated
surface, of `AppRoutes`, of `AdminPage` or of `App` - **structurally cannot see a transitive import
through a registry module**. No amount of care applied to that method would have found it.

That is an argument about enumeration methods generally, and it is why this is a retro line and not a
backlog item: the plan had already routed around the class of error before anyone found an instance
of it.

**But the enumeration-free formulation itself went stale during the slice, and this is the part worth
carrying forward.** The primary gate was stated as: `git diff --name-only $BASE -- web/src` filtered
to test files must print **exactly one line**. After the Phase 4 remediation added five sibling
`*.pager.test.tsx` files, it prints **six**. The substance of the gate is intact - no file that
existed at `$BASE` was touched - but the command's stated expectation is now wrong, and a reviewer
running it verbatim would see a failure that is not one.

> **State a refactor gate over files that existed at the base, not over the diff's file list.**
> "No test file that existed at `$BASE` appears in the diff" (`git diff --diff-filter=M`) is
> enumeration-free **and** survives the slice adding new test files. "Exactly one new test file"
> conflates the gate with a prediction about the change set, and the prediction is the part that
> moves.

## What Was Built

- **`web/src/lib/useCursorPager.ts`** (new) - four `useState` pieces (`cursor`, `stack`,
  `startOffset`, `offsets`), three transitions, and `canPrev` instead of the stack. Plain setters, no
  functional updaters, no `useCallback` - deliberately byte-identical mechanics to what shipped,
  with the three surfaces' separate StrictMode warnings merged into one comment that now defends one
  implementation instead of three copies. Imports only `react`.
- **`web/src/lib/useCursorPager.test.ts`** (new) - eight tests: first page, forward walk, backward
  walk, a `next_cursor`-less page, `next(undefined, n)`, a partial last page, the **three-page
  three-size** walk added in review, and `resetPaging` plus `prev`-on-first-page (whose guard is
  observable only as a render count).
- **Seven surfaces migrated.** Four mechanical (`UsersTab`, `EnrollmentsTab`, `ReservationsTab`,
  `InvitesTab`), `JobsPage` (two inlined reset sites become calls), `WorkersPage` (prefixed
  identifiers, hook call above the `section === 'decommissioned'` early return, never calls
  `resetPaging`), and `SchedulesPage` (the different algorithm, retired - `cursorStack`, `goNext` and
  `goPrev` appear nowhere in `web/src`).
- **Nine `prev`/`canPrev` render sites, not seven.** `EnrollmentsTab` and `ReservationsTab` each
  carry a second, deliberately **undisabled** `prev` inside their empty-state escape hatch; `UsersTab`
  wraps its whole footer pair in `{!filtering && ...}`; `InvitesTab` carries a comment explaining why
  it has no hatch. All preserved.
- **Five sibling `*.pager.test.tsx` files** (`JobsPage`, `SchedulesPage`, `UsersTab`, `WorkersPage`,
  `ReservationsTab`) carrying the six tests that cover the wirings the gate could not.
- **`InvitesTab.tsx:14-25`** - the only extraction-debt comment in `web/src` naming this work. Edited,
  not deleted: the pager half is discharged and points at the hook, the `toggleSort` half survives
  because that debt is still open, and its `FOURTH` is corrected to `FIFTH`.
- **Suite 1102 -> 1116 (+14)**, enumerated: 8 in `useCursorPager.test.ts`, 6 across the five sibling
  files. **152 test files.** Note the arithmetic: six new files, so the baseline was 146 (which
  matches the job-retry retro's "1102 across 146 files"), not the 147 carried into this pass. Small,
  and exactly the kind of number that gets copied forward for three slices.
- **Zero Go, zero SQL, zero proto, zero migration. `web/dist` untouched** - fifth consecutive slice.
- `statusTone` in `inviteStatus.ts` / `enrollmentStatus.ts` / `reservationStatus.ts` is byte-identical
  to the merge base and none of the three files appears in the change set. `EXPIRED` still maps to
  `err` in invites and `muted` in enrollments. That difference is deliberate, documented on both
  sides, and would have been erased by a harmonizing edit with **no test going red** - which is why it
  was a dedicated review lens brief rather than an acceptance criterion nobody reads.

## Key Decisions

- **New coverage went into new sibling test files, never into the seven frozen ones.** The engineer
  asked for an "adding cases is obviously safe" exception and the conductor declined it. **A mechanical
  gate stops being a gate the moment it admits a judgment call**, and "obviously safe" is the
  judgment call that admits the next one. The cost is five extra files and a naming convention
  (`*.pager.test.tsx`); the benefit is that the numstat evidence stays a fact rather than a fact with
  a footnote.
- **Three stale citations inside gate-frozen test files were deliberately left**, to be filed rather
  than fixed, so the gate lands literally clean. This is the correct trade and it has an obligation
  attached: an item, filed below. A deliberate deferral that nobody records is indistinguishable from
  an omission a month later.
- **`toggleSort` did not come along.** Five copies, typed over five per-module sort unions, needing a
  generic plus a cast at every call site - a type-level design question inside a change whose premise
  is that nothing changes. Proposed as its own item.
- **The `formatExpiryLabel` / `EXPIRING_WINDOW_MS` pair did not come along, and got no item on
  purpose.** Two consumers, so the extract-before-the-third trigger has not fired, and
  `inviteStatus.ts:5-9`/`:76-77` already name both the destination (`web/src/lib/expiry.ts`) and the
  trigger (a third status module) **in source**. A comment sitting on the code that would move is a
  better carrier than a backlog file nobody greps. The obligation this creates is on the Resolution
  of the closing item, which must say the half was deliberately not done and where its trigger lives.
- **`SchedulesPage`'s first-page cursor changed from `undefined` to `''`**, and that is a real
  observable difference held harmless by exactly two lines: `schedules/api.ts:41`'s
  `if (cursor) q.set(...)` truthiness guard and `useSchedules.ts:10`'s `cursor ?? ''` key
  normalization. Both were verified before the edit, not after, and neither was touched.
  `SchedulesPage.test.tsx:113` - which asserts the `cursor` param is **absent** at least twice - is
  green because of them. If either is ever tightened, that test reddens legitimately.
- **The integration lane was reassigned to a fourth review lens** (gate integrity + documentation
  truth) rather than to a real browser, on the grounds that the standing "browser lane on a zero-Go
  diff" rule is about **rendering** changes and this slice has no layout, no paint, no focus, no key
  events and no wire-shape change.

  **Plainly: it paid off, and it was not close.** The fourth lens produced the six-wiring mutation
  result and the doc-comment findings - the two most valuable outputs of the slice. A browser lane
  would have confirmed that unchanged pages still load and that next/prev still walk, which the msw
  suites already assert at the query-param level. Its one unique contribution would have been driving
  `SchedulesPage`'s first page against a real server to confirm the `undefined -> ''` change omits
  the parameter, and `api.ts:41` makes that a two-line read.

  **The condition to reuse, stated so it does not over-generalize:** reassign the integration slot to
  a browser when the diff changes what is **rendered**; reassign it to a fourth lens when the diff
  changes what is **wired**. This slice changed wiring only, and wiring is exactly what a mutation
  lens can interrogate and a screenshot cannot.

## Findings Triage

- **2 findings against the gate's sufficiency** (six decorative wirings), found independently by two
  lenses, both mutation-proven, both remediated with permanent tests. **0 findings against the shipped
  behaviour of the hook or any of the seven migrations.**
- **1 finding against a brand-new test** - the partial-last-page test did not discriminate the bug in
  its own name. Fixed with a second, three-size walk.
- **4 false claims in the new hook's doc comment**, one of which re-imported the item's refuted
  premise. All corrected in source.
- **13 stale line-number citations created by this diff**, two past EOF. Ten fixed in place by
  converting to symbol names; three deliberately left inside gate-frozen files and filed.
- **`/code-review` at high effort returned zero findings**, and the four lenses that followed returned
  the whole list. Recorded as evidence for the standing shape rather than as a criticism of the tool:
  the output is a lead, and the lenses are what convert a lead into a verdict.
- **Nothing was refuted on the re-verify pass.** The engineer confirmed every finding with its own
  evidence.
- **A red full-suite run was diagnosed with a measurement on both sides**, not waved through, and
  attributed to an already-filed item.

## Deferred Findings

**Filed this pass (proposals for human accept - the conductor commits, the human accepts):**

1. `bug-2026-08-14-stale-citations-in-gate-frozen-test-files` (**bug/low**) - three citations left
   deliberately stale so the zero-diff gate could land literally clean, each verified against the tree
   during this pass: `ReservationsTab.test.tsx:137` cites `ReservationsTab.tsx:45` for a phrase that
   now sits at `:46` (`:45` is a bare `}`); `:139` cites `:253` in a **243-line** file, so it runs
   past EOF, and the footnote it means is at `:223`; `EnrollmentsTab.test.tsx:263` cites
   `UsersTab.tsx:238-245` for a `reset()`-before-reopen convention now at `:205-207` and `:221`. The
   item asks for **symbol names, not renumbering** - a renumber is a fix with the same expiry date as
   the defect. Not scope creep: these three are inside the frozen files, so fixing them here would
   have cost the gate its evidence.
2. `idea-2026-08-14-cursor-pager-next-takes-the-page` (**idea/medium**) - the hook hides `stack` and
   `offsets` so a consumer cannot desync them, then takes `pageSize` as a bare, unvalidatable
   `number` - **precisely the value both closed footer-range bugs were about**
   (`bug-2026-06-05-jobs-pagination-footer-absolute-range`,
   `bug-2026-06-21-schedules-pagination-footer-absolute-range`), and precisely the argument two of the
   six decorative wirings turned out to be unconstrained on. A stronger shape is `next(page)` taking
   the query result so the hook derives both the cursor and the row count. That is a real API change
   across seven call sites with a genuine design question (a structural type the hook does not own),
   so it is an item, not a fix.
3. `idea-2026-08-14-toggle-sort-generic` (**idea/low**) - five copies of a five-line pure function,
   each typed over its own sort union. Deliberately excluded from this slice; the `InvitesTab` comment
   keeps accounting for them, and a comment is not a queue. Unblocked now that the pager has landed.
4. `bug-2026-08-14-schedules-footer-range-not-localized` (**bug/low**) - `SchedulesPage.tsx:149`
   renders `{x}-{y} of {total}` where the other six surfaces render `x.toLocaleString()`. Found while
   verifying the footer for scope exclusion, and this slice **must not** fix it: changing rendered
   text is exactly what the gate forbids. It goes invisible again the moment nobody is looking at all
   seven footers side by side.

**Amendment applied to an existing item (no scope change, no frontmatter change):**

- `idea-2026-08-12-detail-page-state-triad-primitive:112-116` described this extraction as pending and
  "considerably worse ... at **seven** consumers rather than three". That went stale the moment this
  landed. The paragraph is now an appended update pointing at `web/src/lib/useCursorPager.ts` as the
  **worked precedent**: same rule, same gate, and - more usefully - a record that the gate was
  decorative on six wirings there, which is the single most transferable thing that item can inherit,
  since its own acceptance criteria rest on the identical zero-diff argument. The concurrency warning
  the spec wanted added is now moot for the pager half and is recorded as discharged.

**Considered and NOT filed, with reasons:**

- **The `formatExpiryLabel` / `EXPIRING_WINDOW_MS` extraction.** Two consumers; the trigger has not
  fired; `inviteStatus.ts` names both the destination and the trigger in source. Filing it would
  contradict the extract-before-the-third rule this whole item is founded on and would put a
  two-consumer aesthetic extraction in a queue against real work.
- **`AdminTabs.test.tsx` as a thirteenth gate file.** A real gap in the plan's enumeration method
  (transitive import through `admin/tabs.ts`), and it changed nothing, because the primary gate was
  already enumeration-free by design. The lesson is above; there is no work to do.
- **Mutation testing in an isolated worktree.** A dispatch-shape rule for the conductor's playbook,
  not project work. Both agents behaved correctly; the hazard was in how they were launched.
- **A `canNext` on the hook.** Every surface computes its next button's disabled state as
  `!data?.next_cursor || isPlaceholderData`. The first half is a fact about the query result, not
  about the pager. Deliberately excluded and still excluded.
- **A merged single-`useState` internal shape.** Three shipped comments defended the current
  mechanics against exactly this and there is no defect. The reasoning now lives in the hook so it
  does not have to be re-derived; that is not an invitation.
- **The web-suite flakiness.** Already filed as
  `idea-2026-08-13-web-suite-waitfor-flakiness-under-concurrency`; this slice added evidence, not a
  second file.

## Known Limitations

- **The six sibling-file tests are the only coverage of six wirings that shipped in seven surfaces on
  2026-08-13 and earlier.** The mutation sweep covered the wirings this slice touched. Nobody has
  asked the same question of the wirings it did not - the sort-header plumbing, the
  `isPlaceholderData` disabling, the empty-state hatches. **The gate-versus-load-bearing distinction
  applies to every test file in `web/`, and it has been measured on seven.**
- **`resetPaging`'s `setOffsets([])` is uncovered and cannot be covered.** Proven, documented in
  source, kept deliberately. If the hook's internals ever change so that `offsets` can outlive its
  stack, that line becomes load-bearing with no test attached and nothing will announce it.
- **No browser was opened and no screenshot exists**, for the fourth consecutive session. Defensible
  here (nothing renders differently) but the standing gap is unchanged: `idea-2026-06-03-web-e2e-harness`
  still has no screenshots and no real key events after four sessions of evidence.
- **The plan's own change-set audit (Task 10, Step 6) is now stale**: it predicts nine source paths,
  and the Phase 4 remediation added five test files. The prediction moved, not the gate - but a
  reviewer running the plan's commands verbatim will see two "failures" that are not.
- **The backlog item is still open at the time of this pass.** Closing
  `idea-2026-08-13-cursor-pager-hook` via `/backlog close` - with a Resolution recording the
  `SchedulesPage` algorithm difference, the deliberate `toggleSort` and expiry exclusions, and the
  gate result - is **required scope for this slice**, not optional cleanup.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **eleventh iteration
  running**, and the highest-stakes instance yet: the item was wrong about the shape of three of the
  seven files it was asking someone to rewrite.
- **A backlog proposal is not a contract** - eleven for eleven.
- **An item's code sketch is the least verified thing in it** - second instance in one week. The
  sketch here was the deviant algorithm presented as the shared one.
- **Plan-supplied tests are untrusted** - honored and it paid: the plan's own test body shipped a
  non-discriminating assertion, caught by mutating the half the battery did not.
- **Re-running the implementer's own proofs is cheap and should stay standard** - honored, eighth
  iteration.
- **Wrong prose about correct code is the dominant defect class** - **ninth consecutive iteration**,
  and the first in which the wrong prose was a claim that two prior artifacts had already refuted.
- **Diagnose a red gate; measure both ways** - honored, with numbers on both sides.
- **Backlog housekeeping is required scope** - the close is still outstanding and is named above.

New from this iteration:

- **A zero-diff gate proves you did not weaken the tests; it says nothing about whether the tests
  constrained what you changed.** Verify the gate held **and** that it is load-bearing, by mutating
  each wiring the refactor touched. Six of the wirings here were unconstrained. **Candidate for
  durable memory.**
- **Enumerate mutations by property, not by function.** Push/pop, write/read, set/clear, encode/decode:
  mutate both halves. The seven-mutation battery covered `next`'s accumulation and missed `prev`'s
  restore, which is how a test ended up green under the bug in its own title.
- **A refutation does not stay refuted.** Re-check it in every artifact produced *after* it, including
  source comments, because the false claim is the sentence everybody already knows how to write.
- **Prefer a symbol name to a line range in any cross-file citation.** A line number is a claim that
  goes stale on somebody else's diff and reddens nothing. Third consecutive slice to produce this
  defect. **Candidate for durable memory.**
- **State a refactor gate over files that existed at the base**, not over the diff's file list, so it
  survives the slice adding new test files.
- **A clean `/code-review` is a lead, not a verdict.** Zero findings preceded the two best findings of
  the slice.
- **Mutation testing needs an isolated tree.** A lens that writes source cannot share a worktree with
  lenses that read it.
- **Reassign the integration slot by what the diff changes:** a browser when it changes what is
  **rendered**, a fourth lens when it changes what is **wired**. This is a refinement of the standing
  zero-Go-diff rule, not a replacement for it.
- **A deliberate deferral inside a frozen file must be filed in the same pass**, or the deferral
  becomes an omission the moment the reasoning leaves the room.

## Files Most Touched

- `web/src/lib/useCursorPager.ts` - the artifact that outlives everything else in this slice. Read
  `:3-9` (what the seven copies actually were, after two refutations), `:35-42` (why `canPrev` and not
  the stack), `:43-51` (why `string | undefined` makes `tsc` enforce the falsy guard), and `:100-107`
  (the honest note that clearing `offsets` is unobservable and no test covers it).
- `web/src/lib/useCursorPager.test.ts:90-101` - the three-size walk added in review, with the
  coincidence analysis that makes 13, 50 and 7 non-arbitrary.
- `web/src/jobs/JobsPage.pager.test.tsx:10-15` - the model comment for the five sibling files: names
  the frozen sibling, states what the gate licensed, and states exactly why the sibling's 13 tests
  stay green under the mutation this file catches.
- `web/src/schedules/SchedulesPage.tsx` - the migration that was not a rename. Its footer at `:149` is
  the one that skips `toLocaleString()`, filed rather than fixed.
- `web/src/admin/invites/InvitesTab.tsx:14-25` - the edited debt comment. Pager half discharged,
  `toggleSort` half surviving and corrected to FIFTH, and the `EnrollmentsTab` citation converted to
  a symbol name.
- `docs/superpowers/specs/2026-08-14-cursor-pager-hook.md` - section 1.2 ("only four of seven are
  verbatim") is the finding the whole slice hangs off. Sections 8.1 carries two inline Phase 4
  corrections marking where the spec itself was wrong; leaving those in the document rather than
  editing them away is the right handling.
- `docs/superpowers/plans/2026-08-14-cursor-pager-hook.md` - "The item is wrong, and so is the spec in
  three places" (S1-S3) and "The gate is enumeration-free, and that is deliberate" are the two
  sections worth copying into future plans.

## Verification

- **This pass had no shell.** Bash was unavailable; nothing was executed. No `git log`, no `git diff`,
  no test run. Every claim below that could be checked by reading was checked against the worktree.
- **Verified by reading:** `useCursorPager.ts` in full, including every doc comment corrected in
  review; `useCursorPager.test.ts` in full (8 tests, including the three-size walk and its reasoning);
  the five `*.pager.test.tsx` files enumerated by glob and `JobsPage.pager.test.tsx` in full; the
  152-file test count by glob; the 6 sibling tests by grep, giving 8 + 6 = 14 and reconciling
  1102 -> 1116; `InvitesTab.tsx:1-45` (import, corrected comment, `pager` in use);
  `ReservationsTab.tsx:40-51` and its 243-line length, confirming `:45` is a bare `}` and `:253` is
  past EOF; `ReservationsTab.test.tsx:130-142` and `EnrollmentsTab.test.tsx:260-266`, both stale
  citations confirmed against the current tree; `UsersTab.tsx`'s `reset()` sites at `:205-207`/`:221`;
  `SchedulesPage.tsx:149` and the six sibling `toLocaleString()` footers; `admin/tabs.ts` and
  `AdminTabs.test.tsx`, confirming the transitive import; a grep proving no
  `bug-2026-06-21-jobs-pagination-...` citation survives in `web/src`; and the full text of the spec,
  the plan and the backlog item.
- **Reported by the implementing and verifying lanes, not re-run here:** every mutation result
  (M1-M7, the six decorative-wiring mutations with their per-file green counts, and the two that
  disproved spec claims); every suite run and timing (1102 baseline, 1116 final, the 104.9s red run,
  the 39.7s/37.6s clean comparison, the 16-failed contaminated run); `tsc -b` and `npm run build`;
  the `git diff --numstat` gate output; the count of 13 invalidated citations.
- **Not verified:** all test results, all mutation outputs, the exact commit count and diff stat, and
  the change set as `git` sees it. Each is attributed above.
- **The four items filed by this pass are in `docs/backlog/` as proposals**; the human gives final
  accept. The amendment to `idea-2026-08-12-detail-page-state-triad-primitive` is an appended update
  changing no frontmatter and no scope. **The close of `idea-2026-08-13-cursor-pager-hook` is
  outstanding and belongs to the conductor** (`/backlog close`, never a hand-edited `status:`), as do
  the exact-file-set check, the final gate run and all commits.
