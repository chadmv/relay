---
title: "A body-level portal opened while a dialog is open is never marked inert"
type: idea
status: open
created: 2026-08-09
priority: low
source: deferred review finding from the dialog-hardening work (2026-08-09)
---

# A body-level portal opened while a dialog is open is never marked inert

## Summary
`web/src/components/dialog/dialogStack.ts` marks background content `inert` + `aria-hidden` by
iterating `document.body.children` inside `apply()`, which runs only on register and unregister. A
node appended to `<body>` *after* a dialog opens is therefore never marked - and being a later
sibling than the dialog layer, it also paints above the scrim. Verified during review: a `<div>`
appended to `document.body` with a dialog open receives neither attribute.

## Context
Not a live defect. `createPortal` and `document.body.appendChild` appear only inside
`dialogStack.ts` itself, so nothing in the app produces a body-level node today. It is filed because
the next body-level portal will violate the constraint silently, and the failure is invisible: the
new content is reachable by keyboard from behind a modal and announced to a screen reader as if the
modal were not there.

Likely future producers: a toast/notification layer, a tooltip or popover portal, or a select-menu
portal.

## Proposal
Two routes, in increasing cost:

1. **Document the constraint** in `dialogStack.ts`'s header, next to the existing note that no node
   in `web/src` owns an `inert` or `aria-hidden` attribute. Cheapest, and matches how the module
   already records its other invariants.
2. **Enforce it** with a `MutationObserver` on `document.body`'s `childList`, active only while the
   stack is non-empty, applying the same `MARK`-attribute treatment to added nodes. This was
   explicitly considered and deferred during the dialog-hardening work as unwarranted for a
   non-existent producer.

Route 1 now; route 2 when a second body-level portal actually lands. Whoever adds that portal should
be the one to decide, since they will know whether their layer wants to sit above or below a modal.

## Acceptance / Done When
- The constraint is either enforced in code or stated where the next portal author will read it.
- If enforced: a test appends a node to `document.body` while a dialog is open and asserts it is
  marked, and that the marking is removed on close.

## Related
- `web/src/components/dialog/dialogStack.ts` (the `apply()` background marking)
- Shipped the stack this constrains: [[idea-2026-07-01-confirmdialog-focus-trap-hardening]]
