---
title: The detail-page loading / not-found / error triad is now duplicated verbatim in three pages
type: idea
status: open
created: 2026-08-12
priority: low
source: recorded deviation from the extract-before-the-third-consumer rule in the 2026-08-12-schedule-detail-page slice
---

# The detail-page loading / not-found / error triad is now duplicated verbatim in three pages

## Summary

Three detail pages open with the same block, copied line for line:

```tsx
if (isLoading && !data) {
  return <GlassPanel className="h-40" />
}

if (error && !data) {
  const notFound = error instanceof ApiError && error.status === 404
  return (
    <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
      {notFound ? <div className="text-[13px] text-fg-mute">X not found.</div> : (
        <>
          <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
          <Button className="w-auto px-4" onClick={() => refetch()}>Retry</Button>
        </>
      )}
      <div className="mt-4"><Link to="/xs" className="font-mono text-[11px] text-accent">&larr; Xs</Link></div>
    </GlassPanel>
  )
}

if (!data) return null
```

Live at `web/src/workers/WorkerDetailPage.tsx:30-57`, `web/src/jobs/JobDetailPage.tsx:57-80` and
`web/src/schedules/ScheduleDetailPage.tsx:35-66`. The only differences across the three are the
noun in the not-found sentence, the back-link target and label, and the query object it reads.

## Context

**This repo's rule is extract before the third consumer, and the third consumer shipped anyway.**
That was a deliberate, recorded decision, not an oversight - the plan flagged it in a
"THIRD-CONSUMER FLAG" section before the task that wrote it, and the page carries a comment naming
this item (`ScheduleDetailPage.tsx:30-34`). Two reasons were given:

1. The extraction has to migrate two already-shipped pages, which is a behavior-preserving refactor
   with its own risk profile and its own verification gate.
2. The slice's acceptance criteria confined its change set to `web/src/schedules/`,
   `web/src/jobs/api.ts` and `web/src/app/router.tsx`. Widening it would have put a feature behind
   an unrelated refactor.

**The gate is the substance of this item, not the extraction.** The extraction itself is an
afternoon; what makes it worth a spec is that this repo has a hard-won rule about
behavior-preserving refactors: require a **zero-line diff to the existing test files**. An
assertion that needs adjusting during a refactor that is supposed to change nothing IS the finding
- either the refactor changed behaviour, or the test was green because of a defect the refactor
removed. Any plan that picks this up should state that gate up front, because the tempting move
mid-migration is to "fix up" a selector or a text match and keep going.

**Read the Update section below before planning this.** The same gate has now been run to completion
on a bigger instance of the same rule, and it turned out to be weaker than this item assumes.

The honest counter-argument, recorded so it does not have to be re-argued: the rule exists to stop
this exact deferral from being used a fourth time. **If a fourth detail page arrives before the
extraction lands, the deviation has become a policy** and the copy should not ship.

## Proposal

A shared component - working name `DetailPageState` - in `web/src/components/` (not in any feature
module), taking the query's `isLoading`/`error`/`data`, a `refetch`, and the three strings that
vary: the resource noun, the back-link `to`, and the back-link label. It renders the loading panel,
the 404 card, or the retryable error card, and renders nothing when there is data.

Then migrate all three pages onto it in the same change. Do not leave one behind - a partial
migration produces a fourth variant, which is strictly worse than three identical copies.

Points to settle at spec time:

- **The API shape.** Passing the whole query result object versus three props. The former reads
  better at the call site; the latter keeps the component ignorant of TanStack. Either is fine;
  pick one and use it in all three.
- **Whether it renders as a wrapper or as an early return.** The current shape is an early return,
  which is what makes it copyable in the first place. A wrapper (`{children}` when data is present)
  is more idiomatic React but changes the control flow in three shipped files, which raises the
  refactor's risk against the zero-diff gate. Early return is the lower-risk starting point.
- **What the 404 copy is.** All three say "X not found." with the resource noun. Keep it a prop
  rather than deriving it, so nothing has to map routes to nouns.
- **Do not fold in the pages' other shared shapes.** The breadcrumb-plus-pill-plus-`ml-auto`-action-
  bar header is also near-identical across the three, and so is the mono identity sub-line. They
  are a separate question with a different set of variations; extracting them in the same change
  turns a mechanical refactor into a design exercise and defeats the zero-diff gate.

## Acceptance / Done When

- One component renders the triad; `WorkerDetailPage`, `JobDetailPage` and `ScheduleDetailPage`
  all use it and none contains a copy.
- **`WorkerDetailPage.test.tsx`, `JobDetailPage.test.tsx`, `ScheduleDetailPage.test.tsx` and
  `ScheduleDetailPage.transition.test.tsx` have a zero-line diff.** If any assertion needs
  adjustment, stop and investigate rather than adjusting - that is the finding.
- **Additionally (added 2026-08-14, see Update):** for each behaviour the migration re-wires, mutate
  it and confirm some test reddens. A zero-line diff is a negative control; it does not establish
  that the frozen tests constrain what you changed.
- The component itself has direct tests for all three states plus the has-data case, including
  that a 404 renders **no** Retry control (a 404 is documented as non-transient; retrying it is a
  known defect shape in this codebase).
- The comment at `ScheduleDetailPage.tsx:30-34` naming this item is removed.

## Update (2026-08-14): the sibling extraction landed, and its gate was decorative on six wirings

This item's Related section used to say that `idea-2026-08-13-cursor-pager-hook` was
"sibling extraction debt, filed 2026-08-13 and considerably worse ... at **seven** consumers rather
than three", and that the two were "worth reading together". **That extraction has now landed**
(`web/src/lib/useCursorPager.ts`, slice `2026-08-14-cursor-pager-hook`), so it is no longer a sibling
in a queue - it is the **worked precedent** for this item: the same rule, the same gate, run to
completion across seven surfaces. Read the hook, its five `*.pager.test.tsx` siblings, and
`docs/retros/2026-08-14-cursor-pager-hook.md` before planning this one.

Three things it learned that this item should inherit, and the first is a correction to this item's
own acceptance criteria:

1. **A zero-diff gate proves you did not weaken the tests. It says nothing about whether the tests
   constrained the thing you changed.** In that slice the gate held perfectly and was **void on six
   wirings**: deleting `resetPaging()` from `SchedulesPage.chooseSort` left that file with zero reset
   sites and 11/11 green; the same held for `JobsPage.pickSort`/`pickFilter` and `UsersTab.pickEmail`;
   and a bogus page size left `WorkersPage` (13/13) and `ReservationsTab` (17/17) green. The only way
   anyone found out was by mutating each wiring and watching the suite stay green. This item's
   equivalent exposure is the **404-versus-error branch** and the **Retry control's presence** -
   mutate both in each of the three pages, per page, and see what reddens.
2. **New coverage goes in new sibling files, never in the frozen ones.** That slice put its six
   remediation tests in `*.pager.test.tsx` files beside the frozen suites, and declined an "adding
   cases is obviously safe" exception on the grounds that a mechanical gate stops being a gate the
   moment it admits a judgment call.
3. **State the gate over files that existed at the base**, not over the diff's file list. That
   slice's gate was phrased as "exactly one new test file appears in the diff" and went stale the
   moment remediation added five more. "No test file that existed at `$BASE` appears in the diff"
   (`git diff --diff-filter=M`) is enumeration-free and survives the slice adding files.

The concurrency warning this item and its sibling carried - do not run the two refactors at once,
because a red run over overlapping test directories would be hard to attribute - is **discharged**.
The pager half is done and merged; this item is now free-standing.

Its `statusTone` warning still transfers, though: a naive merge that harmonizes near-identical
per-module behaviour can flatten a deliberate difference with **no test going red**, because each
module's own test simply gets rewritten to match. That is the same hazard this item faces with the
404-versus-error branch, and it is why the mutation step above matters more than the diff step.

No scope change, no priority change. Still low; still a countdown against a fourth detail page.

## Related

- Source: `web/src/workers/WorkerDetailPage.tsx:30-57`, `web/src/jobs/JobDetailPage.tsx:57-80`,
  `web/src/schedules/ScheduleDetailPage.tsx:30-66` (the third copy and the deviation comment)
- Design record: `docs/superpowers/plans/2026-08-12-schedule-detail-page.md` ("THIRD-CONSUMER
  FLAG"), `docs/retros/2026-08-12-schedule-detail-page.md` (Problem 5)
- **The worked precedent - read before planning:** `web/src/lib/useCursorPager.ts`,
  `docs/superpowers/specs/2026-08-14-cursor-pager-hook.md`,
  `docs/retros/2026-08-14-cursor-pager-hook.md`. Its parent item
  (`idea-2026-08-13-cursor-pager-hook`) is closed. See the Update section above.
- Same shape, already done for a different primitive: the shared accessible-table component that
  landed earlier in this workstream is the precedent for how far to take an extraction and where to
  stop
- Adjacent frontend consistency items: [[idea-2026-08-09-table-visual-harmonization]],
  [[idea-2026-08-09-table-accessible-name-consistency]]
- Touched the same pages, and is **now closed** (corrected 2026-08-14 - this bullet used to read
  "check before starting"): `bug-2026-08-12-web-narrow-viewport-horizontal-overflow` shipped on
  2026-08-13 and lives in `docs/backlog/closed/`. There is no concurrency hazard left, but its
  outcome constrains this one: `ScheduleDetailPage`'s and `WorkerDetailPage`'s two-column bodies
  gained `md:` breakpoints in that slice, and `Table` now takes a **required** `minWidth`. Any
  markup this extraction moves must carry those along unchanged.

## Notes

Priority is low because nothing is broken and three copies cost little. The reason it is filed at
all is that it is a **countdown**: the value of this item drops to zero the moment somebody writes
the fourth copy, because at that point the duplication is the codebase's convention rather than a
deferral. Whoever adds a fourth detail page should read this item first.
