---
title: "Table accessible-name loose ends: RevokedWorkersTable is unnamed, WorkspacesPanel duplicates its title"
type: idea
status: open
created: 2026-08-09
priority: low
source: deferred review findings from the shared accessible-table primitive (2026-08-09)
---

# Table accessible-name loose ends: RevokedWorkersTable is unnamed, WorkspacesPanel duplicates its title

## Summary
Two small accessible-name gaps left after the 2026-08-09 table-primitive migration gave all eight
grid pseudo-tables an accessible name. Both are cheap and neither is a correctness defect on its
own, but together they are what stands between the app and "every table announces what it is".

## Context
Filed from review findings deliberately left out of the migration PR, which was gated on visual and
behavioral neutrality and so avoided touching anything outside the eight consumers.

## Proposal
1. **`web/src/workers/RevokedWorkersTable.tsx` has no accessible name.** It is a genuine native
   `<table>` (correctly excluded from the migration - it is not a grid pseudo-table and its
   semantics were already right), but it carries no `<caption>` and no `aria-label`. Now that all
   eight pseudo-tables are named, it is the only unnamed table in the app. Add a `<caption>` (the
   native, visible-by-default choice) or an `aria-label`.
2. **`web/src/workers/WorkspacesPanel.tsx` hardcodes `label="Source workspaces"` to match the
   `<Panel title="Source workspaces">` in `web/src/workers/WorkerDetailPage.tsx`.** The two strings
   are kept in agreement by hand, and `WorkerDetailPage.test.tsx` asserts the name on the table
   rather than on the Panel title - so renaming the visible title would leave the two diverged with
   a green suite. Derive one from the other: give the Panel title an id and point the table at it
   with `aria-labelledby`, or at minimum assert that the visible title and the table's accessible
   name are the same string.

## Acceptance / Done When
- Every table in the app, native or grid-based, has an accessible name.
- The Source workspaces table's name cannot silently diverge from its visible Panel title - either
  it is derived from it, or a test pins them equal.

## Related
- `web/src/workers/RevokedWorkersTable.tsx`, `web/src/workers/WorkspacesPanel.tsx`,
  `web/src/workers/WorkerDetailPage.tsx`
- Shipped the naming convention these complete:
  [[idea-2026-06-05-shared-accessible-table-primitive]]
- The same "a shared primitive shipped without the a11y behaviour and every consumer stayed green"
  pattern, on the form-error surface rather than the table surface:
  [[idea-2026-08-13-field-error-wiring-audit]] - note `WorkspacesPanel.tsx:65` appears in both
