---
title: UserMenu toggle button lacks aria-expanded / aria-haspopup
type: bug
status: open
created: 2026-06-03
priority: low
source: web front end auth slice code review
---

# UserMenu toggle button lacks aria-expanded / aria-haspopup

## Summary
The UserMenu dropdown toggle button (`web/src/shell/UserMenu.tsx`) has no `aria-expanded` or `aria-haspopup` attributes, so screen readers don't announce it as a menu toggle. Add `aria-haspopup="menu"` and bind `aria-expanded` to the open state.

## Related
- `web/src/shell/UserMenu.tsx`
- Retro: `docs/retros/2026-06-03-web-frontend-auth.md`
