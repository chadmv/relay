---
title: "Signing out leaves keyboard focus on <body> on the sign-in page"
type: idea
status: closed
closed: 2026-09-02
resolution: fixed
created: 2026-08-13
priority: low
source: Phase 6 triage of the 2026-08-13-usermenu-menu-roles slice
---

# Signing out leaves keyboard focus on `<body>` on the sign-in page

## Summary

`UserMenu`'s logout handler calls `closeAndRestoreFocus()` and then `onLogout()`
(`web/src/shell/UserMenu.tsx:229-241`). The restore puts focus on the dropdown toggle, which is
correct and deliberate - the containment check has to run while the panel is unambiguously still
mounted. But `HoloShell.onLogout` awaits `logout()` and then navigates to `/auth`
(`web/src/shell/HoloShell.tsx:22-25`), which unmounts the entire shell **including the toggle that
just received focus**. When a focused element is removed from the document, focus falls to `<body>`.

So a keyboard user who signs out lands on the sign-in page with focus nowhere. Their next Tab starts
from the top of the document, and nothing on the destination claims focus either: the email input at
`web/src/auth/LoginScreen.tsx:41-47` has no `autoFocus`.

**Not a regression.** `main` behaves identically - it had no focus restore at all, so focus fell to
`<body>` from the clicked button rather than from the toggle. The 2026-08-13 slice changed where
focus is at the moment of the navigation, not where it ends up.

## Context

This is the same shape as the defect that slice fixed for Escape (focus dropped on the floor when the
panel unmounted), one component boundary further out. The difference is that the menu can fix its own
case and cannot fix this one: by the time the shell unmounts, `UserMenu` no longer exists to move
focus anywhere. **The fix belongs at the destination, not in the menu.**

Worth noting what is already correct, so nobody "fixes" it twice:

- `closeAndRestoreFocus()` before `onLogout()` is the right order and is argued at
  `UserMenu.tsx:231-234`. Do not reverse it.
- Every other close route in that component already lands focus somewhere sensible: Escape restores
  to the toggle, Tab out keeps the destination, a nav-item click restores to the toggle (which
  survives, since the shell is not remounted by a route change).

The one adjacent gap in the same family is [[idea-2026-08-13-usermenu-outside-mousedown-drops-focus]],
which is inside the component and has a different cause.

## Proposal

Decide once where focus goes after an authentication transition, and apply it at the destination
rather than at each departure point. Two routes:

1. **Cheapest and probably right: focus the sign-in form's first control.** Add `autoFocus` to the
   email `Input` at `LoginScreen.tsx:41-47`, or a `useEffect` + `ref` focus if `autoFocus` proves
   awkward under the test suite. This also helps the ordinary "arrive at /auth unauthenticated"
   case, which has the same gap for the same reason.
2. **More correct for a screen-reader user: focus the page heading.** Give the `<h1>Sign in</h1>
   (`LoginScreen.tsx:37`) `tabIndex={-1}` and focus it on mount, so the transition is announced
   ("Sign in, heading") before the user is dropped into a text field they did not ask for. This is
   the conventional SPA route-change treatment and would generalize to other routes later.

Route 1 is a two-line change and is the right first move; route 2 is the one to take if the app ever
grows a general route-announcement policy. **Do not** solve it by having `UserMenu` focus something
in the shell it is about to unmount.

Whichever route is chosen, the same question exists for the 401-driven teardown, which reaches
`/auth` by a different path (`web/src/auth/AuthProvider.tsx` clears the session and `ProtectedRoute`
redirects). If the fix lives on the sign-in page it covers both automatically; if it lives in a
logout handler it covers only one.

## Acceptance / Done When

- After signing out from the dropdown, `document.activeElement` is a named element on the sign-in
  page, not `<body>`.
- The same holds when `/auth` is reached by the 401 teardown and by a direct unauthenticated visit,
  or the difference is deliberate and written down.
- A test asserts it, with a positive control proving the sign-in page actually rendered - an
  `activeElement` assertion against an unmounted tree is trivially satisfiable.
- `UserMenu.tsx:229-241` keeps its close-then-hand-off ordering.

## Related

- `web/src/shell/UserMenu.tsx:229-241` (the logout handler and its restore),
  `web/src/shell/HoloShell.tsx:22-25` (the navigation that unmounts the toggle),
  `web/src/auth/LoginScreen.tsx:37,41-47` (the destination, which claims no focus)
- Where it was found: `docs/retros/2026-08-13-usermenu-menu-roles.md`,
  `docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md`
- Same family, different cause, inside the component:
  [[idea-2026-08-13-usermenu-outside-mousedown-drops-focus]]
- The focus-management reasoning this reuses (capture, then restore, and never steal):
  [[idea-2026-07-01-confirmdialog-focus-trap-hardening]] (closed),
  `web/src/components/dialog/DialogShell.tsx:234-239,276-282`
- The other open loose end on the sign-out path: [[bug-2026-08-13-cross-generation-401-clears-a-new-session]]

## Notes

Low priority because nothing is broken for a mouse user and the page is fully usable after one Tab.
It is filed because the cost lands on exactly the user who has the least slack: someone navigating by
keyboard or by screen reader loses their position at the moment the application changes out from
under them, which is the moment orientation matters most. It is also cheap - route 1 is two lines and
a test.

## Resolution
Route 1 from the item, at the destination: autoFocus on the first control of LoginScreen and RegisterScreen (9d4006b). Pinned by unit tests on both screens, three route-level tests in authArrivalFocus.test.tsx (direct visit, sign-out through the real UserMenu, 401 teardown through the real AuthProvider) and a Playwright assertion on the logout path. UserMenu's close-then-hand-off ordering is unchanged.
