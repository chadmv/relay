---
title: The schedules footer renders its row range unlocalized where the other six paginated surfaces localize
type: bug
status: closed
closed: 2026-09-02
resolution: fixed
priority: low
created: 2026-08-14
source: found while verifying the footer for scope exclusion during the 2026-08-14-cursor-pager-hook slice; not fixable there because changing rendered text would have broken that slice's zero-diff gate
---

# The schedules footer renders its row range unlocalized where the other six paginated surfaces localize

## Summary

Seven paginated surfaces render an absolute row range in their footer. Six of them format every
number with `toLocaleString()`. `SchedulesPage` does not:

| Surface | Renders |
|---|---|
| `web/src/schedules/SchedulesPage.tsx:149` | `` SHOWING <span>{x}-{y} of {total}</span> `` |
| `web/src/jobs/JobsPage.tsx:70-71` | `` `${x.toLocaleString()}-${y.toLocaleString()} of ${total.toLocaleString()}` `` |
| `web/src/workers/WorkersPage.tsx:84-85` | same |
| `web/src/admin/users/UsersTab.tsx:99-100` | same |
| `web/src/admin/enrollments/EnrollmentsTab.tsx:53-54` | same |
| `web/src/admin/invites/InvitesTab.tsx:66-67` | same |
| `web/src/admin/reservations/ReservationsTab.tsx:88-89` | same |

At 1,000 schedules the schedules footer reads `1-50 of 1000` where every sibling reads
`1-50 of 1,000`.

The six that localize also share a second shape the schedules one lacks: a zero-rows branch rendering
`0 of ${total}` instead of a `0-0` range.

## Context

**Why it was not fixed when it was found.** It surfaced during the cursor-pager extraction, whose
acceptance gate was a byte-identical diff to every pre-existing test file. Changing rendered text is
exactly what that gate forbids, and `SchedulesPage.test.tsx` asserts on the footer. Fixing it there
would have converted a clean gate result into a gate result with an exception, which was the one
thing that slice was built to prevent.

**Why it needs an item rather than a mental note.** It is only visible when someone reads all seven
footers side by side, which happens roughly never - it took a scope-exclusion audit to find it, and
the audit is over. Note also that the line number moved during that slice (`:179` -> `:149`), which is
its own small argument for filing rather than remembering.

**Is it a bug or a taste call?** It is a consistency defect with a live consequence: the SPA presents
the same fact in two formats on two pages, and the schedules page is the one that disagrees with the
majority and with `StatSection`'s deliberate en-US thousands separation. There is a real
counter-argument worth recording - `web/src/lib/time.ts:45` and `StatSection.test.tsx:33-35` both note
that `toLocaleString()` output is **locale-dependent on the runner**, and the project has previously
chosen literal formatting over `Intl` for exactly that reason. If the fix is applied, it should match
what the six siblings already do rather than invent a third convention, and the test for it must not
assert `(1234).toLocaleString()` against itself.

## Proposal

Bring `SchedulesPage`'s footer into line with the six, including the zero-rows branch, and assert it
with a literal expectation rather than a `toLocaleString()` round trip.

One open question to settle first, cheaply: whether the six should instead move to an explicit
`toLocaleString('en-US')` like `LogView.tsx:117` and `StatSection` do, so that CI locale cannot change
rendered output. That would make this a seven-file change rather than a one-file change and is
arguably the better fix; decide before writing.

## Acceptance / Done When

- The schedules footer formats its range identically to the other six surfaces, zero-rows branch
  included.
- A test asserts the formatted output against a literal string, not against a `toLocaleString()` call.
- No other surface's rendered text changes (or, if the `'en-US'` option is taken, all seven change
  together and each has an assertion).

## Related

- Source: `web/src/schedules/SchedulesPage.tsx:149` and the six siblings listed above
- Found by: `docs/superpowers/specs/2026-08-14-cursor-pager-hook.md` (scope exclusion 6),
  `docs/retros/2026-08-14-cursor-pager-hook.md`
- The locale-dependence caveat: `web/src/lib/time.ts:45`,
  `web/src/admin/server/StatSection.test.tsx:33-35`
- The same footer's arithmetic, twice shipped wrong:
  `docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md`,
  `docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md`

## Resolution
One-file change (b44b95a): SchedulesPage joins the six as they are, zero-rows branch included, which also makes it honour computePageRange's documented empty-page contract. The seven-file explicit en-US option was rejected: a product regression for non-US readers, and the suite already depends on the runner locale in JobsPage.test.tsx and StatSection.test.tsx. Tests assert literal strings. The range half of toLocaleString stays unpinned on all seven surfaces; filed separately.
