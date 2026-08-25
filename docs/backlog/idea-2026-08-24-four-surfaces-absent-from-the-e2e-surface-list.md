---
title: Four reachable SPA surfaces are absent from the browser harness's surface list
type: idea
status: open
created: 2026-08-24
priority: low
source: coverage audit during Phase 4 of the 2026-08-24 web-e2e-harness slice
---

# Four reachable SPA surfaces are absent from the harness's surface list

## Summary

`web/e2e/surfaces.ts` enumerates 13 surfaces, and `layout.spec.ts` walks all of them at three widths.
Four routes a user can reach today are not in the list:

- `/register` - the self-registration flow, named in the original 2026-06-03 item's own proposal.
- `/profile/password` and `/profile/sessions` - two of the three profile tabs; only `identity` is
  covered.
- `/jobs/:id/tasks/:taskId` - the task detail page.

Three of them need no agent and no configuration change. `/register` is the exception (see below).

## Context

Found while correcting `web/e2e/README.md`, which had claimed coverage for a surface that was never in
the list at all. The list is not wrong so much as unfinished - it was built from the surfaces the
slice's three specs needed, and nobody enumerated the complement afterwards.

`/jobs/:id/tasks/:taskId` and `/register` each have a reason to be deferred rather than simply added:

- **`/register`** requires `RELAY_ALLOW_SELF_REGISTER`, and the harness now **pins that to `false`
  deliberately**, so the one test server runs the production-default posture. Covering `/register`
  means either a second server with a different posture or accepting that the harness no longer tests
  the default. That is a real decision, not an omission.
- **`/jobs/:id/tasks/:taskId`** is populated only once a task exists, which needs
  [[idea-2026-08-24-e2e-harness-slice-2-agent-in-harness]]. It can be added in its empty or
  error state now, but a passing empty-state assertion is worth less than it looks - see the readiness
  discussion in `surfaces.ts`.

So the cheap, unambiguous work is the two profile tabs.

## Proposal

Add `/profile/password` and `/profile/sessions` with readiness predicates that are **not** satisfied by
the loading state - the slice already had to fix three predicates that resolved before their data
arrived, so copy the corrected pattern rather than the original one. Then decide `/register`'s posture
question explicitly and record the answer in `web/e2e/README.md`, and leave the task detail page to
slice 2.

## Acceptance / Done When

- The two profile tabs are in `surfaces.ts` with predicates that fail under a forced API error.
- `web/e2e/README.md`'s coverage section matches `surfaces.ts` exactly - the previous mismatch is what
  produced this item.
- The `/register` decision is written down wherever the posture pin is explained, so the next reader
  does not rediscover the tension.

## Related

- `web/e2e/surfaces.ts`, `web/e2e/README.md`
- `web/playwright.config.ts` - where the posture is pinned
- [[idea-2026-08-24-e2e-harness-slice-2-agent-in-harness]] - unblocks the task detail page
