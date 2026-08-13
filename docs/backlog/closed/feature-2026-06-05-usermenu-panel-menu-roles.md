---
title: UserMenu dropdown panel lacks menu/menuitem roles and keyboard navigation
type: feature
status: closed
created: 2026-06-05
closed: 2026-08-13
resolution: fixed
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

## Resolution
Closed 2026-08-13 (`2026-08-13-usermenu-menu-roles`) by **inverting this item's Proposal**. The
problem it names is real and is fixed; the fix it proposed is not the one that shipped, and would
have made things worse.

**What this item got right.** The contract was unfulfilled: the toggle advertised
`aria-haspopup="menu"` to assistive tech since 2026-06-05 against a panel that was a plain `<div>`.
A screen-reader user really was promised a menu and handed something else.

**Why the fix went the other way.** A contract mismatch has two repairs - build the thing, or stop
advertising it - and this item assumed the first without arguing for it. Three of the four entries
are site-navigation links, and for that case:

- WAI-ARIA 1.2 defines `menu` as a list of choices, "often a list of common actions or functions",
  and the APG's **Disclosure Navigation Menu** pattern exists for precisely this case and states
  `menu`/`menuitem` are deliberately not used when the entries are links.
- `role="menuitem"` on an `<a href>` **replaces** the link role in the platform accessibility tree,
  so the item leaves a screen reader's links list and browse-mode "next link".
- A conforming roving tabindex would make those three links unreachable by Tab.
- `aria-haspopup="true"` was not an escape hatch: ARIA 1.2 treats it as equivalent to `menu`.

So the toggle now carries `aria-expanded` plus `aria-controls`, the panel is an ordinary disclosure,
the items stay three links and one button, and Tab reaches them natively.

**The proof is mechanical, not rhetorical.** A mutation adding `role="menuitem"` broke **seven other
tests**, because `getByRole('link', {name:'Profile'})` stopped resolving. This item's own Proposal,
implemented literally, destroys the semantics it was trying to add.

**Trigger to revisit, recorded so it is re-evaluated rather than re-argued:** if this dropdown ever
stops containing navigation links and becomes actions-only, the calculus flips and `role="menu"`
becomes correct. Nothing short of that.

**Two defects this item never mentioned were also fixed:** the items had no `onClick`, so the
dropdown stayed open over the page it had just navigated to (newly visible once `/profile/*`
shipped), and Escape dropped focus to `<body>` instead of the toggle. Review then caught a
regression introduced by that first fix - the handlers also fired on modifier-clicks, so Ctrl+click
opened a background tab *and* collapsed the dropdown - now guarded with react-router's own
`isModifiedEvent` predicate.

Follow-ups filed: [[idea-2026-08-13-post-logout-focus-lands-on-body]] and
[[idea-2026-08-13-usermenu-outside-mousedown-drops-focus]].
