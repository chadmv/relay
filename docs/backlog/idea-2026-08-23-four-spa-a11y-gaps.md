---
title: "Four small SPA accessibility gaps: MetricChart value, JobDetail tablist, WorkerActions aria-expanded, SortControl title-only hint"
type: idea
status: open
created: 2026-08-23
priority: low
source: 2026-08-23 deep roadmap refresh - frontend lens findings; filed as one sweep item
---

# Four small SPA accessibility gaps: MetricChart value, JobDetail tablist, WorkerActions aria-expanded, SortControl title-only hint

## Summary
Four accessibility gaps no existing item covers, each narrow and independently fixable; filed as
one sweep item so they are scheduled together rather than lost individually:

1. **`MetricChart`'s accessible name omits the current reading and the trend**
   (`web/src/workers/MetricChart.tsx:27-35`): the current value renders as a sighted-only sibling
   of `<svg role="img" aria-label={title}>`, whose label is just "CPU"/"MEMORY"/"GPU" - a
   screen-reader user on the worker Utilization panel hears "CPU, image" with no number.
2. **`JobDetailPage`'s Spec/Log tabs declare `role="tab"`/`role="tablist"` without the contract
   those roles promise** (`web/src/jobs/JobDetailPage.tsx:168-194`): no `aria-controls`, no
   `role="tabpanel"`, and two Tab stops instead of arrow-key roving tabindex - the same
   "declaring a role obliges its keyboard model" shape as the closed UserMenu item and the open
   [[idea-2026-08-09-tasks-table-grid-role-selection]].
3. **`WorkerActions`' inline Edit toggle has no `aria-expanded`**
   (`web/src/workers/WorkerActions.tsx:59,86-93`): the app's only other disclosure besides
   UserMenu, and the one that did not inherit its fix - `grep -rn "aria-expanded" web/src` hits
   exactly one file.
4. **`SortControl`'s disabled-state explanation is `title`-only**
   (`web/src/jobs/SortControl.tsx:30`): the "sorting is unavailable while a status filter is
   active" hint exists only as a hover tooltip, invisible to keyboard and AT users; pair it with a
   visually-hidden `aria-describedby` node.

## Acceptance / Done When
- Each of the four either ships its fix or records a deliberate decision (e.g. tabs downgraded to
  a non-tablist pattern, per the UserMenu disclosure precedent - correcting the advertisement is a
  legitimate resolution).
- No fix regresses the protected-test-file gates; role changes are checked against the
  `getByRole` assertions that broke the literal menuitem attempt in #124.

## Related
- `web/src/workers/MetricChart.tsx`, `web/src/jobs/JobDetailPage.tsx`, `web/src/workers/WorkerActions.tsx`, `web/src/jobs/SortControl.tsx`
- [[idea-2026-08-09-tasks-table-grid-role-selection]], [[idea-2026-08-13-field-error-wiring-audit]] - the adjacent a11y debt items
- `docs/backlog/closed/feature-2026-06-05-usermenu-panel-menu-roles.md` - the disclosure precedent items 2 and 3 lean on
