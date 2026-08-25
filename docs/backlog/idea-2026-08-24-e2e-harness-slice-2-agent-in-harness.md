---
title: Slice 2 of the browser harness - run a real relay-agent so job and task lifecycle surfaces are reachable
type: idea
status: open
created: 2026-08-24
priority: medium
source: spec section 8 of the 2026-08-24 web-e2e-harness slice; filed as the condition for closing the parent item
---

# Slice 2 of the browser harness: run a real relay-agent inside it

## Summary

Slice 1 stood up the harness with three specs and 51 tests, all against surfaces reachable with **no
worker connected**. Everything that requires an agent is therefore either absent or asserted only in
its empty state:

- `/workers` is covered in its empty state; `/workers/:id` is never visited at all.
- No job ever executes. No task reaches `running`. `/jobs/:id/tasks/:taskId` is not covered.
- The SSE task-log stream is never opened, so the reconnect and generation-fencing behaviour that
  `web/src/jobs/useTaskLogStream.ts` documents at length is unexercised in a browser.
- `WorkerPicker` renders only its empty state, so the reservations create form is half-covered.

Slice 2 adds a `relay-agent` to the harness so those surfaces become reachable.

## Context

This is the condition on closing [[idea-2026-06-03-web-e2e-harness]]. That item's dominant argument -
its 2026-08-14 amendment - names exactly two capability gaps, screenshots and real key events, and
slice 1 delivers both. What remains is not "the rest of that item"; it is this, plus two other
separable questions (visual baselines, automated accessibility checks) that should be filed on their
own merits rather than bundled.

`/workers/:id` is the specific page the 2026-08-13 retro flagged as having under 15px of layout
headroom, so it is the highest-value single surface this unlocks.

## Proposal

Start one `relay-agent` from the Playwright `webServer` array (it accepts multiple entries), enrolled
through the enrollment token `fixtures.ts` already creates and currently discards. Then extend the
existing specs rather than adding a fourth: `surfaces.ts` gains the worker-detail and task-detail
paths with readiness predicates, and `layout.spec.ts` picks them up for free.

Decisions this needs and slice 1 deliberately did not make:

- **What the agent runs.** A task that exits immediately gives a `done` task and no `running` window;
  a long-running one makes the suite wait. Probably a short sleep, with the spec waiting on status
  rather than on time.
- **Determinism.** Task execution is asynchronous and the suite is serialized at `workers: 1`. Every
  assertion must wait on an observable state transition, never a duration - this is the classic source
  of a flaky required check, and slice 1's zero-flake record is worth protecting.
- **Whether the agent runs on the same host as CI's `webServer`.** It needs a working subprocess
  environment; on ubuntu that is fine, on a developer's Windows box it is the harness's first real
  platform divergence.
- **Cleanup.** A left-behind agent process is worse than a left-behind server, because it will keep
  claiming tasks from the recreated database on the next run.

## Acceptance / Done When

- A worker appears `online` in the harness, and `/workers/:id` is a covered surface with a readiness
  predicate that is not satisfied by the loading state.
- At least one job runs to a terminal status through the UI, with the transition asserted on observed
  state rather than elapsed time.
- The suite's flake record holds: five consecutive clean runs with `web/e2e/.run/` deleted each time.
- `web/e2e/README.md`'s "what it does not cover" section shrinks by exactly the items this closes, and
  does not silently grow new ones.

## Related

- `web/e2e/` - the slice 1 harness; `README.md` there is the live coverage document
- `docs/superpowers/specs/2026-08-24-web-e2e-harness.md` section 8 - where this was first proposed
- [[idea-2026-06-03-web-e2e-harness]] - the parent, closed by slice 1 with this filed as the remainder
- [[idea-2026-08-24-layout-overflow-gate-cannot-see-inner-scrollers]] - a gate weakness this does not fix
