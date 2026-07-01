---
date: 2026-07-01
topic: job-detail-page
branch: claude/stoic-cannon-15b269
pr: "2026-07-01 job-detail-page (autopilot iteration 2)"
---

# Session Retro: 2026-07-01 - job-detail-page

**TL;DR:** Iteration 2 of the autopilot batch shipped the SPA's Job detail page
(`/jobs/:id`) plus row-click navigation from the jobs list. Frontend-only, consuming the
existing `GET /v1/jobs/{id}` and `GET /v1/tasks/{id}/logs` endpoints. The page has a header
with a reserved (empty) actions slot, a 55/45 split, client-derived overall progress, a
task-DAG dependency strip backed by a pure Kahn-style `dagLayout` helper, a selectable
`TasksTable`, and Spec + static Log tabs. Spec and plan committed first; feature landed in
commit cb410f2. Full web suite (248 tests, 54 files) and the production build were green;
the conductor re-ran both independently on the final stable tree. Review found no High/Medium
and three Low: two addressed with tests, one left as intended behavior. Notably, a single
frontend engineer with an explicit no-delegation framing implemented all 12 tasks cleanly -
the iteration-1 concurrent-writer mitigation held.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-07-01-job-detail-page-design.md`.
- **Plan** `docs/plans/2026-07-01-job-detail-page-plan.md`.
- **Feature** commit cb410f2:
  - `JobDetailPage` at `/jobs/:id`: header with a reserved empty actions slot (for future
    write-actions), a 55/45 split, Spec + Log tabs.
  - Overall progress derived client-side from the tasks array, because the detail response
    omits the list-only enrichment fields carried by the jobs-list payload.
  - A task-DAG dependency strip backed by a pure `dagLayout` helper: Kahn-style layering,
    edge direction `dep -> task`, with a per-node stack guard so arbitrary cyclic graphs
    terminate instead of recursing forever.
  - A selectable `TasksTable`; selecting a task drives the Log tab.
  - `useJob` polls the job; `useTaskLogs` is a static fetch-once
    (`staleTime: Infinity`), enabled only when a task is selected and the Log tab is active,
    and keyed off the `['job']` prefix so a job poll never invalidates the logs query.
  - A separate `TaskStatus` union that covers `dispatched`/`timed_out`.
  - `JobsTable` name cell is now an accessible `Link` to `/jobs/:id` (row-click navigation).

## What Went Well

- **Spec and plan committed before any code.** Same discipline as iteration 1: the design and
  plan docs landed at the phase boundary first, so implementation executed against an approved,
  file-listed target rather than design-as-you-go.
- **The iteration-1 concurrent-writer mitigation held.** Unlike iteration 1 (which suffered
  subagent role-confusion and a concurrent-writer race on shared files), this slice used a
  SINGLE frontend engineer with an explicit "you do all the work, no delegation, no waiting"
  framing. All 12 plan tasks were implemented with no role-confusion and no concurrent-writer
  race. This is the direct payoff of the lesson recorded in the iteration-1 retro.
- **The isolate-and-test-the-hard-part instinct paid off.** The DAG layering was factored into a
  pure `dagLayout` helper rather than tangled into the component, which made both the cycle
  guarantee and the ordering behavior testable in isolation and kept the two Low findings small.
- **Deferred work was filed, not lost.** Every out-of-scope item (see Deferred) was routed to a
  specific follow-up item or backlog entry rather than silently dropped.

## Notable

- **The conductor re-verified independently, as standard practice now.** Even though this slice
  had none of iteration 1's drama, the conductor still independently re-verified the working tree
  (exact file set, no stray artifacts) and re-ran the green gate (full web suite + production
  build) on the final stable tree before committing. The iteration-1 lesson ("do not trust a
  subagent's green claim") is now baseline procedure, not an incident response.
- **The detail payload is intentionally leaner than the list payload.** `GET /v1/jobs/{id}`
  omits the list-only enrichment fields, so overall progress is derived client-side from the
  tasks array. Worth remembering: the list and detail shapes are not interchangeable, and future
  detail-page work should assume the leaner shape.

## Findings Triage

- **No High, no Medium.**
- **Low #1 (fixed with tests):** `useTaskLogs` refetched on every Spec -> Log tab toggle. Fixed
  with `staleTime: Infinity` plus a toggle-count test that locks in fetch-once behavior across
  repeated tab switches.
- **Low #2 (fixed with a test, no algorithm change):** DAG cycle layering is bounded but
  order-dependent for degenerate cyclic input. Added a cycle-termination test that locks in the
  no-crash guarantee; the layout order for cyclic graphs is left as-is (cyclic task graphs are
  not a supported real input, only a robustness concern).
- **Low #3 (left as intended behavior):** a dependency name renders in two table cells. Intended;
  no change.

## Deferred (each to its own item)

- **Live SSE log tailing** -> `feature-2026-06-26-task-log-view-sse-tailing`. The Log tab ships
  as a static fetch-once for now.
- **Job write-actions (submit/cancel/retry)** -> `feature-2026-06-26-job-actions-submit-cancel-retry`.
  Its cancel UI will mount in this page's reserved header actions slot, which is why the slot
  was reserved empty now.
- **Accessible drag-resizer for the 55/45 split** -> filed as backlog
  (`docs/backlog/idea-2026-07-01-job-detail-resizable-split.md`).
- **Per-task source/workspace block in the Spec tab** -> candidate only, NOT filed. It needs a
  backend change to echo `source` in `taskResponse` first; noted here so the dependency is not
  forgotten when that block is picked up.

## Verification

- Full web suite: 248 tests passing across 54 files.
- Production build green (`tsc -b && vite build`).
- Both re-run independently by the conductor on the final stable tree (see Notable) rather than
  trusted from the implementer's claim.
- Code review: no High, no Medium; Low #1 and Low #2 fixed with tests; Low #3 accepted as
  intended behavior.
