---
title: UserMenu dropdown panel lacks menu/menuitem roles and keyboard navigation
type: feature
status: open
created: 2026-06-05
priority: medium
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

## Notes
- 2026-08-12: re-confirmed still open while fixing the dropdown's z-index/stacking bug
  in the same file. The toggle's `aria-haspopup="menu"` still points at a plain `<div>`
  with no `role="menu"`, so the ARIA contract remains unfulfilled. That work also added
  `data-testid="user-menu-panel"` to the panel, which gives this item a ready handle for
  its role/keyboard assertions.
- 2026-08-12: raised **low -> medium** (user call, from a roadmap Suggested backlog action).
  The reasoning: this is not a missing nicety but a *stated and unfulfilled* contract - the
  toggle has advertised `aria-haspopup="menu"` to assistive tech since 2026-06-05 while the
  panel it points at has been a plain `<div>` the whole time, so a screen-reader user is
  told a menu is there and then handed something that is not one. The roadmap had already
  reordered it to web-frontend #4 on cost, leaving the item's own `priority` disagreeing
  with its rank; this closes that gap.
- Worth checking against `[[idea-2026-08-09-native-dialog-element-reconsideration]]` and
  the DialogShell focus work (#117) before starting: the focus-management half of this
  item (move focus in on open, restore to the toggle on close) is the same problem
  DialogShell already solved, and the reasons it could NOT use `inert` or a focus-trap
  library under this test suite apply here too. Reuse that reasoning rather than
  rediscovering it.
