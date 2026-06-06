---
title: UserMenu dropdown panel lacks menu/menuitem roles and keyboard navigation
type: feature
status: open
created: 2026-06-05
priority: low
source: follow-up from closing bug-2026-06-03-usermenu-aria-attributes
---

# UserMenu dropdown panel lacks menu/menuitem roles and keyboard navigation

## Summary
The UserMenu toggle button now exposes `aria-haspopup="menu"` / `aria-expanded`, but
the dropdown panel it controls is a plain `<div>` of `Link`/`button` elements with no
`role="menu"` / `role="menuitem"` semantics and no arrow-key navigation. Screen-reader
and keyboard users therefore can't navigate the menu the way the toggle's ARIA contract
implies.

## Proposal
- Give the panel `role="menu"` and each item `role="menuitem"`.
- Wire the toggle to the panel via `aria-controls` / `id`.
- Add roving-tabindex arrow-key navigation (Up/Down to move, Home/End, Enter/Space to
  activate), and move focus into the panel on open / back to the toggle on close.

## Acceptance / Done When
- Panel and items carry correct `menu`/`menuitem` roles.
- Up/Down arrow keys move focus between items; Enter/Space activates the focused item.
- Opening the menu focuses the first item; Escape/close returns focus to the toggle.
- Tests cover keyboard navigation and the role attributes.

## Related
- `web/src/shell/UserMenu.tsx`
- Closed: `docs/backlog/closed/bug-2026-06-03-usermenu-aria-attributes.md`
