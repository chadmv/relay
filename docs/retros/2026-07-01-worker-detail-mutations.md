---
date: 2026-07-01
topic: worker-detail-mutations
branch: claude/stoic-cannon-15b269
pr: "2026-07-01 worker-detail-mutations (autopilot iteration 1)"
---

# Session Retro: 2026-07-01 - worker-detail-mutations

**TL;DR:** Iteration 1 of an autopilot batch (max 4) shipped the SPA's first mutations: admin
actions on the Worker detail page (edit, enable/disable, revoke, per-workspace evict). Fixed
`apiFetch` to tolerate 202/204 no-body responses, added the worker mutation clients + types, a
shared `ConfirmDialog` primitive, a `useWorkerActions` hook (invalidate-on-success with
optimistic disable/enable rollback), a changed-fields-only edit form, an admin-gated actions
bar, and per-row Evict in the workspaces panel. Spec and plan committed first; feature landed
in commit 0616029. Full web suite (209 tests, 44 files) and the production build were green;
the conductor re-ran both independently on the final stable tree. Review found no High, two
Medium and one Low fixed with tests, one Low left as minor, and one Low deferred to backlog.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-07-01-worker-detail-mutations-design.md`.
- **Plan** `docs/plans/2026-07-01-worker-detail-mutations-plan.md`.
- **Feature** commit 0616029:
  - `apiFetch` 202/204 no-body fix so mutation responses without a JSON body do not throw.
  - Worker mutation clients + types in `web/src/workers/api.ts`.
  - Shared `ConfirmDialog` primitive in `web/src/components/` (role=dialog, aria-modal,
    aria-labelledby, focuses Cancel on open, dismisses on Escape/Cancel).
  - `useWorkerActions` hook: invalidate-on-success; optimistic disable/enable with rollback;
    revoke navigates to `/workers` and never invalidates `['worker', id]`; bare `['workers']`
    prefix invalidation.
  - `WorkerEditForm`: changed-fields-only PATCH, labels sent as a full replace, client-side
    validation of `max_slots` and name.
  - `WorkerActions` bar + `WorkerDetailPage` admin gating.
  - `WorkspacesPanel` per-row Evict.

## What Went Well

- **Spec and plan committed before any code.** The design and plan docs landed at the phase
  boundary first, so implementation was an execute against an approved, file-listed target
  rather than design-as-you-go. Both implementer runs converged on the same plan-specified
  file set (see Notable), which is only possible because the file set was pinned up front.
- **Right-sized safety on the first mutation surface.** This is the SPA's first write path, so
  the risk was concentrated in the client cache and confirm flows rather than the server. The
  hook centralizes the invalidation/rollback contract in one place, and the review pass focused
  there: the two Medium findings and the actionable Low were caught and fixed with tests before
  the slice was called done.
- **Deferred hardening was filed, not lost.** The one accessibility Low that did not belong in
  a first slice (ConfirmDialog focus trap + scoped Escape) was written up as a backlog item
  rather than silently dropped or gold-plated in place.

## Notable

Real incident worth naming candidly, because it is a process hazard, not a code one:

- **Subagent role-confusion plus concurrent writers to the same files.** The first
  frontend-engineer subagent lost track of its role - its final message was a meta-comment
  about "waiting for a background agent" - and did almost no work. A retry was dispatched. A
  concurrent implementer then raced that retry on the same files: `WorkspacesPanel.tsx` was
  reverted mid-run, stray `.new`/`.tmp` artifacts appeared in the tree, and files showed up
  before they had actually been written. Both runs did eventually converge on the same
  plan-specified file set, so the end state was correct - but only because the plan pinned the
  files.
- **Lesson: do not trust a subagent's green claim.** The conductor must independently
  re-verify the working tree (exact file set present, no stray `.new`/`.tmp` artifacts) and
  re-run the green gate (full web suite + production build) on the final stable tree rather
  than believing an implementer that says it passed. That is what happened here: the conductor
  re-ran the 209-test suite and the `tsc -b && vite build` production build on the settled tree
  before accepting the slice, which is why the race did not ship a broken artifact.

## Findings Triage

- **Medium (fixed with tests):** `max_slots` empty -> 0 silently disables the worker, since
  the server does no validation on that field; the form now validates it client-side.
- **Medium (fixed with tests):** empty name was accepted; the form now rejects it client-side.
- **Low #1 (fixed with tests):** stale "Requeued N" note lingered after re-enable; cleared.
- **Low #2 (left as minor):** one shared `evict.isPending` disables all workspace rows at once,
  not just the row being evicted. Cosmetic; deferred as a known minor.
- **Low #3 (deferred, filed as backlog):** `ConfirmDialog` has no focus trap and uses a
  document-global Escape listener. Not reachable today (only one dialog mounts at a time and
  nothing steals focus behind it), but the primitive is slated for reuse by Admin and Profile.
  Filed as `docs/backlog/idea-2026-07-01-confirmdialog-focus-trap-hardening.md`.

## Verification

- Full web suite: 209 tests passing across 44 files.
- Production build green (`tsc -b && vite build`).
- Both re-run independently by the conductor on the final stable tree (see Notable) rather than
  trusted from the implementer's claim.
- Code review: no High; 2 Medium + Low #1 fixed with tests; Low #2 accepted as minor; Low #3
  deferred to backlog.
