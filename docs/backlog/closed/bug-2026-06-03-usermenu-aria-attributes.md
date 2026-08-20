---
title: UserMenu toggle button lacks aria-expanded / aria-haspopup
type: bug
status: closed
created: 2026-06-03
closed: 2026-06-05
resolution: fixed
priority: low
source: web front end auth slice code review
---

# UserMenu toggle button lacks aria-expanded / aria-haspopup

## Summary
The UserMenu dropdown toggle button (`web/src/shell/UserMenu.tsx`) has no `aria-expanded` or `aria-haspopup` attributes, so screen readers don't announce it as a menu toggle. Add `aria-haspopup="menu"` and bind `aria-expanded` to the open state.

## Related
- `web/src/shell/UserMenu.tsx`
- Retro: `docs/retros/2026-06-03-web-frontend-auth.md`

## Resolution
Closed 2026-06-05 (`676d26f`). The UserMenu toggle carries `aria-expanded` bound to the open state (`web/src/shell/UserMenu.tsx`). The follow-up `feature-2026-06-05-usermenu-panel-menu-roles` was filed in the same commit and later shipped as an ARIA disclosure rather than `role="menu"`, which is why the toggle no longer advertises `aria-haspopup="menu"`.
