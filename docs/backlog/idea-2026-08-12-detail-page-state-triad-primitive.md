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
- The component itself has direct tests for all three states plus the has-data case, including
  that a 404 renders **no** Retry control (a 404 is documented as non-transient; retrying it is a
  known defect shape in this codebase).
- The comment at `ScheduleDetailPage.tsx:30-34` naming this item is removed.

## Related

- Source: `web/src/workers/WorkerDetailPage.tsx:30-57`, `web/src/jobs/JobDetailPage.tsx:57-80`,
  `web/src/schedules/ScheduleDetailPage.tsx:30-66` (the third copy and the deviation comment)
- Design record: `docs/superpowers/plans/2026-08-12-schedule-detail-page.md` ("THIRD-CONSUMER
  FLAG"), `docs/retros/2026-08-12-schedule-detail-page.md` (Problem 5)
- Sibling extraction debt, filed 2026-08-13 and considerably worse: [[idea-2026-08-13-cursor-pager-hook]]
  is the same "extract before the third consumer" rule at **seven** consumers rather than three.
  Worth reading together - they share the byte-identical-test gate, and its `statusTone` warning
  (a naive merge would flatten a deliberate per-module difference) is the same hazard this item
  faces with the 404-versus-error branch.
- Same shape, already done for a different primitive: the shared accessible-table component that
  landed earlier in this workstream is the precedent for how far to take an extraction and where to
  stop
- Adjacent frontend consistency items: [[idea-2026-08-09-table-visual-harmonization]],
  [[idea-2026-08-09-table-accessible-name-consistency]]
- Would touch the same pages: [[bug-2026-08-12-web-narrow-viewport-horizontal-overflow]] - **check
  before starting.** That item edits the two-column body of the same three pages. Doing them in
  either order is fine; doing them concurrently is not, and neither should be folded into the other
  (this one is behavior-preserving and gated on a zero test diff, that one changes rendering and
  needs new assertions).

## Notes

Priority is low because nothing is broken and three copies cost little. The reason it is filed at
all is that it is a **countdown**: the value of this item drops to zero the moment somebody writes
the fourth copy, because at that point the duplication is the codebase's convention rather than a
deferral. Whoever adds a fourth detail page should read this item first.
