---
title: "Closing the account dropdown by pressing dead space drops focus to <body>"
type: idea
status: closed
closed: 2026-09-02
resolution: fixed
created: 2026-08-13
priority: low
source: Phase 6 triage of the 2026-08-13-usermenu-menu-roles slice (deferred review finding)
---

# Closing the account dropdown by pressing dead space drops focus to `<body>`

## Summary

`UserMenu`'s outside-mousedown path closes the dropdown **without touching focus**
(`web/src/shell/UserMenu.tsx:123-131`, which calls `close()` rather than `closeAndRestoreFocus()`).
That is correct whenever the press target is focusable: `mousedown` fires **before** the browser moves
focus to whatever was pressed, so a restore at handler time would see `activeElement` still inside the
panel and yank focus onto the toggle, away from the control the user just clicked. The reasoning is
written at the call site (`:124-129`) and pinned by
`web/src/shell/UserMenu.test.tsx:223-252`, which spies on `toggle.focus` because the end state cannot
tell a stealing implementation from a correct one.

The uncovered case is a press on **non-focusable** content. Nothing takes focus, the panel unmounts,
and focus falls to `<body>`. A user who had tabbed into the panel and then clicked dead space to
dismiss it has lost their place in the tab order.

**Not a regression.** `main` had no focus restore on any close route, so it dropped focus here too.
The 2026-08-13 slice fixed the Escape and item-selection routes and left this one deliberately
unfixed, with the reason recorded.

## Context

The population is narrow and worth stating honestly: a keyboard user dismisses with Escape, which
restores correctly; a pure mouse user never had focus in the panel to lose. The affected user is a
mixed-input one who tabbed in and then reached for the mouse.

The reason to file it rather than to forget it is that the component now argues the **current**
behaviour at the call site, in convincing detail. A future author reading `UserMenu.tsx:124-129` will
correctly conclude that a naive shared close is wrong, and will have nothing telling them that the
dead-space case was considered separately and left open. That is exactly how a considered omission
turns into an assumed invariant.

## Proposal

Three routes, in increasing cost. **Route 1 is the recommended one** unless somebody wants the
behaviour.

1. **Do nothing, and say so where the decision is made.** Add one sentence to the comment at
   `UserMenu.tsx:124-129`: the dead-space press is a known, accepted gap, distinct from the
   focusable-target case the code is arguing about. Cheapest, and it converts an invisible omission
   into a recorded decision.
2. **Restore in a microtask, re-checking who won.** Schedule the restore with
   `queueMicrotask`/`requestAnimationFrame` after `close()`, and restore to the toggle **only if**
   `document.activeElement` is `document.body` at that point - i.e. only if nothing else claimed
   focus. This preserves the focusable-target case exactly (the clicked control has focus by then, so
   the branch does not fire) and repairs the dead-space case. The cost is a second scheduling
   primitive in a component that currently has none, and a test that has to prove **both** branches,
   because a microtask restore that always fires is precisely the focus theft the existing test
   exists to prevent.
3. **Move focus to the toggle only when the press target is not focusable**, decided synchronously
   from `e.target`. Rejected on sight during the slice: "is this element focusable" has no reliable
   synchronous answer (`tabindex`, `contenteditable`, disabled states, shadow content), and getting
   it wrong reintroduces the theft. Recorded so it is not re-proposed.

If route 2 is taken, the existing spy-based test at `UserMenu.test.tsx:223-252` is the control that
must stay green, and the new test needs a press on a genuinely non-focusable node (a bare `<div>`,
not `document.body` - user-event blurs the active element when the click target has no focusable
ancestor, so the two are not the same probe).

## Acceptance / Done When

- Either the gap is closed with both branches tested, or it is recorded as accepted at
  `UserMenu.tsx:124-129` so the next reader does not mistake silence for coverage.
- If closed: pressing a non-focusable node while focus is inside the panel leaves focus on the
  toggle, and pressing a focusable control still leaves focus on that control with `toggle.focus`
  never called. Both proven, and the second one is the regression guard.

## Related

- `web/src/shell/UserMenu.tsx:103-105` (`close()`), `:123-131` (the mousedown handler and its
  rationale), `:95-99` (`closeAndRestoreFocus`, the path that does restore)
- `web/src/shell/UserMenu.test.tsx:223-252` (the spy-based test that pins the current behaviour)
- Where it was found: `docs/retros/2026-08-13-usermenu-menu-roles.md`,
  `docs/superpowers/specs/2026-08-13-usermenu-menu-roles.md` (Focus rules)
- Same family, different cause, one component boundary out:
  [[idea-2026-08-13-post-logout-focus-lands-on-body]]
- The focus-restore reasoning both reuse: `web/src/components/dialog/DialogShell.tsx:234-239,276-282`,
  [[idea-2026-07-01-confirmdialog-focus-trap-hardening]] (closed)

## Notes

Filed at low priority with route 1 as the recommendation, which means the cheapest honest outcome of
this item is a one-sentence comment. That is a legitimate close. The item exists so the decision is
made once, by someone who has read both cases, rather than being re-derived from a comment that only
discusses one of them.

## Resolution
Route 1 from the item: the dead-space press stays an accepted gap, recorded as a hazard comment in UserMenu's mousedown handler and pinned by 'pressing non-focusable dead space closes the menu and leaves focus on <body>' in UserMenu.test.tsx (81a16bd). Route 2's microtask restore was refuted at spec time: a microtask runs before the browser's mousedown focusing step, so its body gate reads body in both branches; a real-browser version is proposed as a follow-up.
