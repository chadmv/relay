---
title: "Harden the shared dialogs: focus trap + scoped Escape (3 implementations, 5 ConfirmDialog call sites)"
type: idea
status: closed
created: 2026-07-01
closed: 2026-08-09
resolution: fixed
priority: medium
source: worker-detail-mutations review (2026-07-01); re-measured 2026-08-09 after the admin console
---

# Harden the shared dialogs: focus trap + scoped Escape (3 implementations, 5 ConfirmDialog call sites)

## Summary
There are now **three** hand-rolled dialog implementations, none with a focus trap, background
`inert`/`aria-hidden`, or a scroll lock, and each registering its Escape handler on `document`. The
two gaps compose: the missing focus trap is precisely what makes the previously "unreachable"
Escape cross-talk reachable, and one of the three dialogs reveals a one-time credential where
Escape destroys the only copy rather than harmlessly cancelling.

## Context
Filed 2026-07-01 against a single primitive, deferred with "harden when it is generalized for reuse
by Admin and Profile" and "best done just before or alongside the first Admin/Profile page that
reuses the dialog". **That trigger has been overshot**: the admin console shipped three tabs on
2026-08-09 and the count is now:

**Implementations (3)** - each has `role="dialog"`, `aria-modal="true"`, `aria-labelledby` and a
first-element focus, and each has zero focus trap, zero `inert`/`aria-hidden`, zero scroll lock, and
one `document`-level keydown listener:

- `web/src/components/ConfirmDialog.tsx`
- `web/src/admin/users/ResetPasswordDialog.tsx`
- `web/src/admin/TokenRevealDialog.tsx` (shared, and slated for the Invites tab too)

**`ConfirmDialog` call sites (5)**: `web/src/workers/WorkerActions.tsx`,
`web/src/workers/WorkspacesPanel.tsx`, `web/src/jobs/JobActions.tsx`,
`web/src/admin/users/UsersTab.tsx`, `web/src/admin/reservations/ReservationsTab.tsx`.

### The two gaps now compose - correcting this item's own claim

This item previously said of the document-global Escape: *"Not reachable today (only one dialog
mounts at a time)."* **That is no longer true.** On `web/src/admin/users/UsersTab.tsx` the two
dialog states are fully independent - `setResetting(u)` at `:178` and `setConfirm({...})` at
`:180-181` never clear one another, and they render from independent blocks at `:278` and `:297`.
So both can be mounted at once.

A mouse cannot reach the second trigger, because the open dialog's `fixed inset-0` scrim covers the
rows. But **there is no focus trap** - gap (a) - so a keyboard user can Tab from the open
ResetPassword dialog to a row's Archive button behind the scrim and press Enter, mounting a second
dialog. One Escape then fires both `document` listeners. Gap (a) is the reachability mechanism for
gap (b); neither is fully assessable alone.

### Escape is not a harmless cancel for every consumer

`TokenRevealDialog` shows an agent-enrollment token clear-text exactly once; it is unrecoverable
afterward. Escape there **destroys the only copy of a live credential**. The 2026-08-09 review of
that tab already found and fixed a related defect where the dialog stole focus back every 60
seconds, whose consequence was precisely that a keyboard admin pressing Enter on "Done" got nothing
and Escape was the plausible next keystroke. The focus trap is what stops that class of accident,
so for this consumer the item is closer to data-loss prevention than to a11y polish.

## Proposal
Harden all three, or replace them with one primitive. Two viable routes:

- **Extend `ConfirmDialog` into a real dialog primitive** and have the other two compose it, so the
  trap, `inert`, scroll lock and Escape scoping exist once. Note `ResetPasswordDialog` and
  `TokenRevealDialog` are deliberately *not* `ConfirmDialog` variants (it takes text-only `body`
  while they own form fields), so this means extracting a shell they can wrap, not forcing them
  through the existing API.
- **Adopt a headless accessible-dialog primitive** and delete the hand-rolled versions, if that is
  lighter than maintaining a trap by hand. The project currently carries no focus-trap dependency.

Either way: Tab/Shift+Tab must cycle within the dialog, background must be `inert`/`aria-hidden`
with scroll locked, and Escape must reach only the topmost dialog. For `TokenRevealDialog`,
decide separately whether Escape should confirm-before-discarding given what it destroys - that is a
product call, not a mechanical one, and it is the one place where matching the shipped baseline is
arguably wrong.

## Acceptance / Done When
- Focus is trapped in the open dialog and cannot Tab to background elements; background is inert and
  scroll is locked while any dialog is open.
- Escape dismisses only the topmost dialog, with a test that **mounts two dialogs simultaneously**
  (the `UsersTab` reset-plus-archive path above is a real reproduction, not a synthetic one) and
  proves a single Escape closes exactly one. Prove it RED against today's code.
- All three implementations share the hardened behavior rather than one being fixed in isolation.
- The five `ConfirmDialog` call sites keep passing their existing tests unchanged, proving the
  hardening preserved behavior rather than redefining it.
- A decision is recorded for `TokenRevealDialog`'s Escape, given that it discards a credential.

## Related
- Source slice: `docs/superpowers/specs/2026-07-01-worker-detail-mutations-design.md` (review Low #3)
- The three implementations and five call sites listed above
- [[feature-2026-08-08-admin-invites-tab]] - will add a fourth consumer via `TokenRevealDialog`
- Same accumulation shape: [[idea-2026-06-05-shared-accessible-table-primitive]],
  [[feature-2026-06-05-usermenu-panel-menu-roles]]

## Notes
Priority raised low -> medium on 2026-08-09, for two reasons rather than the count alone: the
document-global Escape gap became genuinely reachable via the missing focus trap, and one consumer
now discards an unrecoverable credential on Escape. The Invites tab will make it a fourth consumer,
so doing this before that tab is the cheap ordering.

## Resolution
Shipped 2026-08-09 (autopilot, dialog-hardening). `web/src/components/dialog/dialogStack.ts` is a
module-level LIFO owning the shared portal layer, the body scroll lock and the inert/aria-hidden
background marking, every one of them derived from the post-removal stack rather than restored
directly; `DialogShell.tsx` is the one modal shell. All three implementations compose it and their
`document`-level Escape listeners are gone as independent listeners.

Route chosen on measured evidence rather than preference. Native `<dialog>`/`showModal()` was
rejected because jsdom's `HTMLDialogElementImpl` is an empty subclass, so every existing dialog test
would throw and any workaround would test a polyfill instead of the platform. A headless focus-trap
library was rejected by the same reasoning inverted: those enforce via `inert`/`focusin`/top-layer,
and `user-event@14` computes its Tab destination from a document-wide `querySelectorAll` that never
consults `inert` - the property that must be proved is exactly what a library makes unprovable in
this harness. `preventDefault()` on keydown *is* honored, so a Tab-intercepting trap is both correct
and testable. Both routes are filed for revisit with explicit trigger conditions
([[idea-2026-08-09-native-dialog-element-reconsideration]]).

`TokenRevealDialog` takes `dismissOnEscape={false}`: Escape there is not a cancel, it is the
destructive act, and Done stays inside the trap two Tabs away. That decision is the one this item
asked to be recorded.

Review found one HIGH, and its history is the lesson: moving Escape onto the panel broke it whenever
focus is outside the panel, which a scrim click always causes - a regression against main on all
three dialogs. It had already been correctly diagnosed inside the diff, as a comment justifying a
test's ordering rather than as a defect. All three lenses found it independently. Fixed by restoring
a document-level listener gated on `isTopmost(id)` plus a scrim `onMouseDown` that prevents the blur
outright, then re-verified with probes. A follow-up pass found the focus-parking branch was
overwritten by React DOM's `restoreSelection`; deferred one microtask and pinned by a test.

Two live defects were fixed as side effects: `WorkspacesPanel`'s scrim never covered the viewport
(`GlassPanel`'s `backdrop-blur` is the containing block for `position: fixed` descendants), and
`ReservationsTab`'s secret-leak sweep read a render container that no longer holds the portaled
dialog while its positive control also matched the page footnote, so it would have passed while its
negatives went vacuous.

Suite 780 -> 811 tests, with the byte-identical existing-tests gate holding across nine protected
files (additions only, two sanctioned exceptions named before implementation). Follow-ups filed:
[[idea-2026-08-09-body-level-portal-inert-marking]], [[idea-2026-08-09-dialog-shell-sweep-test]],
[[idea-2026-08-09-native-dialog-element-reconsideration]].
