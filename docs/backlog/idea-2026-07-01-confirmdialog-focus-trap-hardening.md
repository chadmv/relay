---
title: Harden the shared ConfirmDialog: focus trap + scoped Escape
type: idea
status: open
created: 2026-07-01
priority: low
source: worker-detail-mutations review (2026-07-01 worker-detail-mutations)
---

# Harden the shared ConfirmDialog: focus trap + scoped Escape

## Summary
The `ConfirmDialog` primitive added in the 2026-07-01 worker-detail-mutations slice sets
`role="dialog"`, `aria-modal="true"`, and `aria-labelledby`, focuses Cancel on open, and
dismisses on Escape/Cancel - but it has no focus trap and uses a document-global Escape
listener. Harden it when it is generalized for reuse by Admin and Profile.

## Context
Surfaced by the 2026-07-01 worker-detail-mutations code review as Low #3 and deferred rather
than fixed in the first mutation slice. The primitive lives at
`web/src/components/ConfirmDialog.tsx` and is intentionally minimal (no portal library, no
focus-trap dependency) for its current single-dialog-at-a-time usage on the Worker detail page.

Two gaps:

- **(a) No focus trap.** Tab can move focus to elements behind the overlay; there is no
  background `inert`/`aria-hidden` and no scroll lock, so keyboard and screen-reader users can
  escape the modal into the page underneath it.
- **(b) Document-global Escape listener.** The dialog registers its Escape handler on
  `document`, so two simultaneously-mounted dialogs would both react to a single Escape. Not
  reachable today (only one dialog mounts at a time), but the primitive is slated for reuse by
  Admin and Profile, where multiple confirm flows are more likely to coexist.

## Proposal
Harden when the primitive is generalized for the new surfaces, not before:

- Add a focus trap (Tab/Shift+Tab cycle within the dialog) plus background `inert`/`aria-hidden`
  and a scroll lock so focus cannot leave the modal.
- Scope Escape to the dialog instance (or otherwise ensure only the topmost dialog reacts)
  rather than listening on `document`.
- Alternatively, adopt a headless dialog primitive (e.g. a small accessible-dialog library) and
  drop the hand-rolled version, if that is lighter than maintaining the trap by hand.

## Acceptance / Done When
- Focus is trapped within the open dialog and cannot Tab to background elements; background is
  inert and scroll is locked while the dialog is open.
- Escape dismisses only the intended (topmost) dialog, with no cross-talk between two mounted
  dialogs.
- Existing Worker detail confirm flows continue to pass their tests.

## Related
- Source slice: 2026-07-01 worker-detail-mutations
  (`docs/superpowers/specs/2026-07-01-worker-detail-mutations-design.md`).
- Source: `web/src/components/ConfirmDialog.tsx`.
- Pairs with [[idea-2026-06-26-shared-holo-design-primitives]] - both are about generalizing
  shared UI primitives ahead of the Admin/Profile surfaces.

## Notes
Frontend-only, small. Best done just before or alongside the first Admin/Profile page that
reuses the dialog, so the trap is validated against real multi-dialog usage.
