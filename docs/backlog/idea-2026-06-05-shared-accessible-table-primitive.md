---
title: Adopt role-layering pattern or extract a shared accessible-table primitive for pseudo-tables
type: idea
status: open
created: 2026-06-05
priority: low
source: 2026-06-05 workers-table-aria-semantics retro
---

# Adopt role-layering pattern or extract a shared accessible-table primitive for pseudo-tables

## Summary
Should the other pseudo-table-like surfaces in the web app (if any emerge) adopt the same ARIA role-layering pattern used in `WorkersTable.tsx`, or is a shared accessible-table primitive worth extracting once a second instance appears? Raised in the 2026-06-05 workers-table-aria-semantics retro.

## Context
`WorkersTable.tsx` renders a CSS-grid pseudo-table and recently gained explicit ARIA roles (`role="table"/"row"/"columnheader"/"cell"`) plus `aria-sort` on columnheader wrappers, rather than being converted to a native `<table>`. If a second grid-based pseudo-table appears in the web app, the per-element role wiring would be duplicated, which is the usual signal to extract a small shared primitive (e.g. an accessible table/row/cell wrapper component or a set of role helpers).

## Acceptance / Done When
- A decision is recorded: keep applying the role-layering pattern inline per component, or extract a shared accessible-table primitive.
- If extraction is chosen, `WorkersTable.tsx` and any new pseudo-table consume the shared primitive.

## Related
- `web/src/workers/WorkersTable.tsx`
- `docs/backlog/closed/idea-2026-06-05-workers-table-aria-semantics.md`
- `docs/retros/2026-06-05-workers-table-aria-semantics.md`
