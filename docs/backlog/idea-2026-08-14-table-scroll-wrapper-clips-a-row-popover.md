---
title: The table scroll wrapper will clip a popover rendered inside a row, and only a comment stands in front of it
type: idea
status: open
priority: low
created: 2026-08-14
source: promised follow-up in the Resolution of bug-2026-08-12-web-narrow-viewport-horizontal-overflow, filed at Phase 6 of the 2026-08-13-narrow-viewport-overflow slice
---

# The table scroll wrapper will clip a popover rendered inside a row, and only a comment stands in front of it

## Summary

`web/src/components/holo/Table.tsx` now wraps the `role="table"` subtree in
`<div className="overflow-x-auto" tabIndex={0} role="group">`, unconditionally, for all ten
consumers. That wrapper is a **scroll container on both axes**: per the CSS overflow spec, when one
axis is not `visible` the other computes from `visible` to `auto`, so `overflow-x: auto` gives the
element `overflow-y: auto` as well.

Consequence: **anything inside a row that used to escape the table's box is now clipped**, and
vertical escape is clipped just as hard as horizontal. A dropdown, a tooltip, a row-level actions menu
or an inline confirm bubble rendered in a cell will be cut off at the wrapper's edge or will grow a
vertical scrollbar inside the table, instead of painting over the page.

Nothing in the tree does this today. The claim that nothing does is a **point-in-time audit by a human
on 2026-08-13**, recorded in the primitive's comment, with no test behind it.

## Context

The audit is real and it was thorough. It is quoted here so the item is not read as an accusation that
it was skipped:

- every footer, error banner and empty-state strip in all ten consumers is a **sibling** of `<Table>`,
  not a child, so none of them entered the scroll region;
- `WorkspacesPanel`'s `ConfirmDialog` is a sibling **and** portals to a layer on `<body>`;
- no row cell renders an absolutely-positioned popover - row content is text, chips, links, buttons
  and one `Input` (the `UsersTable` rename field), all in normal flow;
- focus rings are inset outlines, so a focused control near the right edge scrolls into view rather
  than being clipped.

**What has no defence is the eleventh case.** The primitive's comment ends with the rule - "If a
future row needs a popover, it must portal" - and that sentence is the entire enforcement. This is the
same shape as the manual `rg` sweeps behind [[idea-2026-08-09-dialog-shell-sweep-test]]: a real
invariant, verified once by hand at the end of one iteration, with nothing to stop the next author who
has no reason to know the audit happened.

**The most likely trigger is already visible in the tree.** `UsersTable` carries a 270px ACTIONS
column holding three mini buttons, and `ReservationsTable` and `SchedulesTable` are the two widest
templates in the app. Collapsing a three-button column into a single overflow menu is the ordinary
next move for a wide table on a narrow viewport - it is the exact thing the closed overflow item names
as the un-taken alternative to horizontal scrolling - and it lands a popover inside a row.

The failure would also be **hard to attribute**. The developer adding the menu sees it clipped and has
no reason to suspect a wrapper that a shared primitive added months earlier for an unrelated reason;
the class string is not in their file and their component looks correct.

## Proposal

Not a rewrite. Three candidate defences, cheapest first, and the choice is the work:

1. **A test at the primitive.** Render a `Table` whose row contains an absolutely-positioned child and
   assert the failing property directly - that no ancestor of a cell's positioned descendant is a
   scroll container, or more simply that a `position: fixed`/portalled child escapes while an
   `absolute` one does not. jsdom does no layout, so this cannot assert visual clipping; it can only
   assert the structural fact that the wrapper is a scroll container and therefore a clipper. State
   which of those two it is, in the test, rather than letting a structural pin read as a rendering
   proof.
2. **A sweep test over the consumers.** Assert that no file rendering a `TableRow` also contains an
   `absolute`/`fixed` positioned element outside a portal call. Same family as
   [[idea-2026-08-09-dialog-shell-sweep-test]] and [[idea-2026-08-13-field-error-wiring-audit]], and
   the Vitest-reads-the-tree versus ESLint question should be settled once for all three. Note that
   this slice **deleted** a source-scanning guard for being pattern-fragile, so a scan here starts
   with a known cost.
3. **Make it unrepresentable, or make the escape easy.** Give the app the row-popover primitive it
   does not have - one that portals by construction, the way `DialogShell` already does - so the
   correct thing is also the convenient thing. This is the most durable and the most work, and it
   should not be done speculatively: file it as the answer only when a second consumer actually wants
   a popover in a row.

Option 1 plus a written trigger is probably the right amount of work today. **Deliberately not
proposed:** removing the scroll wrapper, or making it conditional again. It is required by every
consumer and its absence is what the closed bug was.

## Acceptance / Done When

- The clipping hazard has an automated tripwire, or an explicit written decision that it does not and
  why. Either outcome closes this item; leaving the comment as the only control does not.
- If a test is chosen, it is proven RED by temporarily adding a positioned popover to a row, with the
  failure recorded, and it names the remedy ("portal it") in its failure message rather than only
  reporting a violation.
- The vertical half is stated somewhere a reader will find it. The primitive's comment currently says
  "a scroll container clips anything inside it that used to escape the table box" and does not say
  that `overflow-x: auto` implies `overflow-y: auto`. That mechanism is the non-obvious part and it is
  what makes a **vertical** dropdown a casualty of a **horizontal** scroll fix.
- The audit's expiry is visible: whatever ships should make it obvious that the ten-consumer read was
  a 2026-08-13 snapshot, not a standing guarantee.

## Related

- Source: `web/src/components/holo/Table.tsx` - the wrapper (`:189-193`) and the audit it rests on
  (`:38-45`)
- The one in-repo instance of the correct pattern, and the rule the comment points at:
  `web/src/components/dialog/DialogShell.tsx` (portals to a layer appended to `<body>`),
  `web/src/workers/WorkspacesPanel.tsx`'s `ConfirmDialog`
- The likeliest future trigger: `web/src/admin/users/UsersTable.tsx`'s 270px three-button ACTIONS
  column, and the column-dropping alternative discussed but not taken in
  `docs/superpowers/plans/2026-08-13-narrow-viewport-overflow.md` and in
  `docs/backlog/closed/bug-2026-08-12-web-narrow-viewport-horizontal-overflow.md`
- Same "an invariant verified once by hand needs a sweep" shape, and the shared open question about
  Vitest versus ESLint: [[idea-2026-08-09-dialog-shell-sweep-test]],
  [[idea-2026-08-13-field-error-wiring-audit]]
- The other unchecked claim left by the same slice:
  [[idea-2026-08-14-table-minwidth-magnitude-is-unchecked]]
- Portal-adjacent accessibility loose end on the same layer:
  [[idea-2026-08-09-body-level-portal-inert-marking]]

## Notes

Filed **low** because it is a hazard with no instance: every consumer is compliant today, and the
audit that established that was careful and is written down at the site. It is filed rather than left
in the retro because of what the audit **is** - a claim about the whole tree, made once, by a reader
who then moved on - and because this project's own history says that class of claim does not survive.
The app reached three hand-rolled dialogs before anyone consolidated them.

The cheapest possible outcome is acceptable: a two-line addition to `Table.tsx`'s comment naming the
`overflow-y` implication, plus one structural test. What should not happen is a future author spending
an afternoon on a clipped dropdown before finding the sentence that predicted it.
</content>
