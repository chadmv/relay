---
title: TaskDag's horizontal scroller has no tab stop, role or accessible name, on a surface the harness visits
type: bug
status: open
created: 2026-09-02
priority: low
source: Phase 4 lenses on the 2026-09-01 header-nav slice, refuting a README completeness claim
---

# TaskDag's horizontal scroller has no tab stop, role or accessible name

## Summary
web/src/jobs/TaskDag.tsx wraps its SVG in a GlassPanel with overflow-x-auto and no tabIndex, role or
name. The SVG has no focusable descendants and its width grows with the DAG's layer count, so at 320
on /jobs/:id it clips behind an unlabelled scroller a keyboard user cannot reach. This is the same
shape that put tabIndex and role group on Table's wrapper, and the document-overflow gate in
layout.spec.ts cannot see it.

## Context
Found while refuting web/e2e/README.md's claim that the only remaining scrollers were Table's.
Further horizontal scrollers outside Table: LogView.tsx, TaskLogPage.tsx and two panes in
ScheduleDetailPage.tsx. Audit those in the same pass.

## Acceptance / Done When
- Every horizontal scroll container under web/src either has a focusable descendant or carries its
  own tab stop and an accessible name that says it scrolls.
- The reachability predicate in web/e2e/nav.ts (toBeInViewport) or a sibling covers at least the DAG.

## Related
- [[idea-2026-08-24-layout-overflow-gate-cannot-see-inner-scrollers]]
- web/src/components/holo/Table.tsx (the precedent)
