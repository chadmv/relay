---
title: The app has no route-change focus or announcement policy; after sign-in, arrival at /jobs lands focus on body
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 1 spec for the 2026-09-01 usermenu-focus chain (lane C), the route 2 decision
---

# The app has no route-change focus or announcement policy

## Summary
The auth screens now claim focus on arrival, but no other route does: after a successful sign-in the
user lands on /jobs with focus on body, and no route transition is announced to a screen reader. The
conventional SPA treatment (focus the page heading on mount so the transition is announced) was
deliberately not applied to the auth screens alone, because a policy applied to two routes reads as
an inconsistency.

## Proposal
Decide the policy once (heading focus on every route change, or a live-region announcement) and apply
it in the router layer so every destination gets it, including the auth screens, which would then drop
their autoFocus in favour of the shared treatment.

## Related
- web/src/app/router.tsx, web/src/shell/HoloShell.tsx
- [[idea-2026-08-13-post-logout-focus-lands-on-body]] (closed)
