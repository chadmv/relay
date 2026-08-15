---
title: A Table minWidth smaller than its consumer's own fixed tracks desynchronizes the columns, and every test still passes
type: idea
status: open
priority: low
created: 2026-08-14
source: promised follow-up in the Resolution of bug-2026-08-12-web-narrow-viewport-horizontal-overflow, filed at Phase 6 of the 2026-08-13-narrow-viewport-overflow slice
---

# A Table minWidth smaller than its consumer's own fixed tracks desynchronizes the columns, and every test still passes

## Summary

`web/src/components/holo/Table.tsx` takes a **required** `minWidth` prop and publishes it, joined to
`columns`, onto the header row and every body row. Requiring the prop is enforced by the type system:
`tsc -b` rejects a call site that omits it, anywhere, including an aliased import.

**Presence is enforced. Magnitude is prose.** The primitive's own header comment states the rule -
`minWidth` must be "sized at or above the sum of its own fixed tracks" - and nothing checks it. A
consumer that declares `grid-cols-[90px_1fr_120px_150px_120px_70px_150px]` (700px of fixed track)
alongside `min-w-[400px]` type-checks, renders, and passes all 1068 tests in the web suite, while
reproducing at every width below 700px the exact defect the prop exists to prevent: free space goes
negative, the `1fr` track falls back to its content minimum, and because the header row and the body
rows are **separate grid containers** with different content, the columns visibly disagree.

## Context

This is the hazard the whole primitive-versus-per-consumer decision was argued on. The
2026-08-13-narrow-viewport-overflow slice chose to put the scroll container and the min-width in the
primitive rather than in ten consumers, and the deciding argument was **not** edit count - it was that
a hand-applied min-width can be applied to one grid and not the other, which desynchronizes exactly
what the primitive exists to keep in agreement. Putting the value on one context string closed that
hole. It did not close this one: the value can still be wrong, it is just now wrong in both places at
once.

The ten shipped values were each derived from their own template's fixed-track sum and checked
arithmetically by a review lane (all ten correct at the time of writing). That check was a
point-in-time read by a human, on the same day the numbers were written. Nothing repeats it.

**Two things make this quieter than the average latent defect:**

1. **jsdom performs no layout.** No Vitest assertion in `web/` can observe a misaligned column. The
   whole acceptance criterion for the slice that introduced `minWidth` was a number taken in a real
   browser by a human, twice.
2. **The failure is invisible until a narrow viewport.** At 1280px every value is inert, so a wrong
   one ships green, reviews clean, and appears only on the surface nobody in this project's automated
   gate can reach.

**The stated rule is also necessary but not sufficient**, and the primitive says so
(`Table.tsx:30-36`): free space stays non-negative only if `minWidth` also clears the `fr` cells' own
content minimums. It holds today only because **every `fr` cell in all ten consumers carries
`truncate` or `min-w-0`**, which drops that cell's automatic minimum to 0. That precondition is a
second unchecked claim living in the same comment, and an `fr` cell added without one would need its
own minimum added to the budget. Any enforcement written for this item has to decide whether it
checks one claim or both.

## Proposal

Three options, in the order they should be considered. None is obviously right; the point of the item
is that one of them should be chosen deliberately.

**A. A dev-only runtime assertion inside `Table` (recommended).** Parse `columns` and `minWidth` at
render time behind `import.meta.env.DEV`, sum the `NNNpx` segments of the template, and throw (or
`console.error`) if the declared minimum is below that sum. This is strictly stronger than any source
scan: it reads the values **actually passed**, so it covers a computed prop, an aliased import, a
consumer in a directory a walker never visits, and a future consumer that does not exist yet. It also
needs no new test infrastructure - all ten consumers already render in the existing suite, so the
check runs ten times on every `npm test` for free. Cost: a small parser in a primitive that currently
contains no logic, and a decision about throw versus warn (the file's precedent for a configuration
error is an unconditional `throw`, twice).

**B. A source-scanning test that pairs each consumer's `COLS` with its `MIN_W`** and does the same
arithmetic. Cheaper to reason about, and it can also assert the ten values as a frozen table so a
change is deliberate. But this slice **deleted** a source-scanning guard from
`web/src/components/holo/responsive.guard.test.ts` precisely because a scan is only as good as its
pattern - a review lane reddened a fully compliant consumer with one JSX comment. The two surviving
scans in that file now strip comments; a third would inherit the same fragility for a rule that
option A can enforce without pattern-matching source text at all.

**C. Do nothing, and record that the magnitude is unenforced.** Defensible: the ten values are
correct today, and the failure mode is a cosmetic misalignment rather than data loss or a security
boundary. If this is the answer, it should be written into `Table.tsx`'s comment as an accepted gap
rather than left implied by the comment's confident phrasing.

## Acceptance / Done When

- A wrong `minWidth` fails something automatically. Proven RED by temporarily setting one consumer's
  `MIN_W` below its fixed-track sum, with the failure recorded, and reverted.
- The check reports **which** consumer and **both** numbers, not a bare boolean - the person who trips
  it is choosing a replacement value.
- An explicit written decision on the second claim: whether the check also verifies that every `fr`
  cell carries `truncate` or `min-w-0`, or whether that stays prose. If it stays prose, say so at
  `Table.tsx:30-36` rather than leaving the comment reading as though the fixed-track sum were the
  whole rule.
- The upper bound is considered too, and probably declined in writing: `minWidth` must also stay
  **below** its container's width at 1280px or a scrollbar appears where none exists today.
  `WorkspacesPanel`'s `min-w-[600px]` against a roughly 614px detail column is the tight one. That
  ceiling depends on the page's layout, not on the table, so it is likely not statically checkable -
  say that rather than omitting it.

## Related

- Source: `web/src/components/holo/Table.tsx` - the `minWidth` prop (`:96-102`), the rule as prose
  (`:14-36`), and the necessary-but-not-sufficient paragraph (`:30-36`)
- The ten consumers and their `MIN_W` constants: `web/src/jobs/JobsTable.tsx`,
  `web/src/jobs/TasksTable.tsx`, `web/src/schedules/SchedulesTable.tsx`,
  `web/src/schedules/ScheduleRunsPanel.tsx`, `web/src/workers/WorkersTable.tsx`,
  `web/src/workers/WorkspacesPanel.tsx`, `web/src/admin/users/UsersTable.tsx`,
  `web/src/admin/invites/InvitesTable.tsx`, `web/src/admin/enrollments/EnrollmentsTable.tsx`,
  `web/src/admin/reservations/ReservationsTable.tsx`
- Where the prop came from, why the value is per-consumer, and the per-table arithmetic:
  `docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md` (Decision 2 and Task 4's table),
  `docs/backlog/closed/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md`
- The scan that was deleted for being fragile, and the two that survive with comment-stripping:
  `web/src/components/holo/responsive.guard.test.ts`
- Same enforcement question (a Vitest file reading the tree versus something stronger), and the place
  that question should be settled once: [[idea-2026-08-09-dialog-shell-sweep-test]],
  [[idea-2026-08-13-field-error-wiring-audit]]
- The other unchecked claim left by the same slice:
  [[idea-2026-08-14-table-scroll-wrapper-clips-a-row-popover]]
- Why nothing in `npm test` can see the symptom: [[idea-2026-06-03-web-e2e-harness]]

## Notes

Filed **low** because all ten values are correct today and the consequence is a ragged layout at a
narrow width, not lost data or a broken boundary. The reason to file it at all is the shape rather
than the severity: this is the third member of a family this project keeps rediscovering - a test that
restates a constant tests the constant, a cadence test must assert the wiring, and now a required prop
proves the prop was passed and nothing about its value. **Presence checks are cheap and satisfying and
they are not magnitude checks.**

Raise the priority if either trigger fires: an eleventh table is added by someone who did not read
`Table.tsx`'s comment, or someone edits a `COLS` template (adding a column changes the fixed-track sum
and nothing will remind them to revisit `MIN_W`). The second is the more likely of the two and it is
the one no reviewer would think to check.
</invoke>
