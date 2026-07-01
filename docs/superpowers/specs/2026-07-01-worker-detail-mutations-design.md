# Worker Detail Page - Admin Mutation Actions - Design

Date: 2026-07-01
Status: Draft (pending user review)

## Overview

The worker detail page (`/workers/:id`) shipped read-only in the
2026-06-05 slice (identity, telemetry, labels, admin-only workspaces). This
slice adds the deferred **admin write actions**: the frontend's first mutations.
Every endpoint already exists and is already admin-gated server-side; this slice
is **frontend-only**.

Because it introduces the first mutations, it also establishes the reusable
**mutation + cache-invalidation pattern** that Admin and Profile will follow. We
deliberately keep that pattern small (invalidate-on-success, one shared
confirm-dialog primitive) rather than building speculative machinery.

## Scope

In scope (all backed by existing endpoints, all admin-gated):

- **Rename** worker name.
- **Edit labels** (key/value map).
- **Set max_slots.**
- **Disable / Enable** (the "drain" concept == `disable?requeue=true`).
- **Revoke agent token.**
- **Evict a source workspace** (per-row action in the workspaces panel).

Explicitly re-scoped away (per roadmap guidance): there is no separate "drain",
"cordon", or "delete worker" concept. "Drain" is disable-with-requeue. There is
no DELETE-worker endpoint and none is added.

Out of scope: any backend change, optimistic UI beyond what is described in
"Mutation strategy", a generic form-library adoption, extracting shared Holo
primitives (tracked separately in
`docs/backlog/idea-2026-06-26-shared-holo-design-primitives.md`).

## Backend - already available, verified, no changes

All routes are registered `auth(admin(...))` in `internal/api/server.go`, so a
non-admin token receives 403 regardless of the UI. Frontend gating is UX only.

| Action | Method + path | Request body | Success | Notes |
|--------|---------------|--------------|---------|-------|
| Rename / labels / slots | `PATCH /v1/workers/{id}` | `{ name?, labels?, max_slots? }` | 200, full `workerResponse` | Merge-on-omit: a field left out keeps its current value. `labels` is a **full replace** of the label map, not a per-key merge. |
| Disable (plain) | `POST /v1/workers/{id}/disable` | none | 200, `disableWorkerResponse` | Already-disabled is a no-op (no re-stamp). |
| Drain | `POST /v1/workers/{id}/disable?requeue=true` | none | 200, `disableWorkerResponse` | Same endpoint; `requeue=true` requeues the worker's in-flight tasks and signals the agent to cancel them. Response adds `requeued_tasks` (count). |
| Enable | `POST /v1/workers/{id}/enable` | none | 200, `workerResponse` | Already-enabled is a no-op. |
| Revoke token | `DELETE /v1/workers/{id}/token` | none | **204 No Content** | See "Revoke is terminal" below. 404 if worker id unknown. |
| Evict workspace | `POST /v1/workers/{id}/workspaces/{short_id}/evict` | none | **202 Accepted** (no body) | Best-effort/async: the agent evicts on its stream and confirms later via inventory update. 404 if `short_id` unknown on that worker. Held workspaces are refused by the agent, not by this endpoint. |

### Response shapes (for TS types)

`disableWorkerResponse` is `workerResponse` with one extra field:

```
{ ...worker fields..., requeued_tasks: number }
```

The existing `Worker` type already models `workerResponse`. `apiFetch` returns
`undefined` for 204/no-body responses, which is correct for revoke and evict.

### Revoke is terminal (important UX consequence)

`DELETE /v1/workers/{id}/token` clears the agent token **and sets the worker's
status to `revoked`**. Revoked workers are excluded from every list/get endpoint,
so immediately after a successful revoke, `GET /v1/workers/{id}` returns 404. The
UI must not try to stay on a now-dead detail page: on revoke success, **navigate
back to `/workers`** rather than invalidating the detail query (which would just
404 and render the not-found card). The confirmation copy must make clear this is
a decommission, not a pause. (Disable is the reversible "pause"; revoke is
terminal.)

## Admin gating

- The current user's admin flag comes from `useAuth().user?.is_admin`, exactly as
  the read-only slice already uses it to mount `WorkspacesPanel`.
- **Non-admins see none of these controls.** The entire actions area (rename,
  labels, slots, disable/enable, revoke) and the per-row Evict button are wrapped
  in an `is_admin` check and are simply not rendered otherwise.
- Server-side `AdminOnly` remains the real authority; the UI check is defense in
  depth / avoiding dead buttons, never the security boundary.

## Architecture

Extend the existing `web/src/workers/` feature module. No new route; the actions
live on the existing `WorkerDetailPage`.

### New files (all under `web/src/workers/`)

- `useWorkerActions.ts` - the mutation hook, mirroring the shape of
  `web/src/schedules/useScheduleActions.ts`. Returns one `useMutation` per
  action. Each `onSuccess` invalidates the relevant query key (details below).
- `WorkerActions.tsx` - the admin-only action bar rendered under the header:
  Disable/Enable toggle button, Drain button, Revoke token button, and an "Edit"
  entry point for the rename/labels/slots form.
- `WorkerEditForm.tsx` - inline edit form (name, labels, max_slots) shown when
  Edit is active. Submits a single PATCH with only changed fields.
- `ConfirmDialog.tsx` - a minimal shared confirm primitive (title, body,
  confirm/cancel, destructive styling variant). Lives under `web/src/components/`
  once shaped, since Admin/Profile will reuse it. It is deliberately tiny: no
  portal library, no focus-trap dependency beyond basic accessible markup
  (role="dialog", labelled, Escape to cancel, focus the cancel button on open).

### Modified files

- `web/src/workers/api.ts` - add mutation clients: `updateWorker(id, patch)`,
  `disableWorker(id, requeue)`, `enableWorker(id)`, `revokeWorkerToken(id)`,
  `evictWorkspace(id, shortId)`; add `WorkerPatch` and `DisableWorkerResponse`
  types.
- `web/src/workers/WorkerDetailPage.tsx` - render `<WorkerActions>` under the
  header when `user?.is_admin`.
- `web/src/workers/WorkspacesPanel.tsx` - add a per-row Evict button (admin-only;
  the panel is already admin-only-mounted) wired through `useWorkerActions`.

### API clients (exact calls)

```
updateWorker(id, patch)      -> PATCH  /v1/workers/{id}          json: patch  -> Worker
disableWorker(id, requeue)   -> POST   /v1/workers/{id}/disable[?requeue=true] -> DisableWorkerResponse
enableWorker(id)             -> POST   /v1/workers/{id}/enable                 -> Worker
revokeWorkerToken(id)        -> DELETE /v1/workers/{id}/token                  -> void (204)
evictWorkspace(id, shortId)  -> POST   /v1/workers/{id}/workspaces/{shortId}/evict -> void (202)
```

`WorkerPatch` = `{ name?: string; labels?: Record<string,string>; max_slots?: number }`.
The form sends only the fields the admin changed. Because the server does a full
replace of `labels`, the labels editor loads the current label map and submits
the complete edited map (add/remove/rename keys), not a diff.

## Mutation strategy

Query keys in play (verified against the existing hooks):

- `['worker', id]` - `useWorker`
- `['worker', id, 'workspaces']` - `useWorkerWorkspaces`
- `['workers', sort]` - the list page (`useWorkers`)
- `['workers', 'revoked', cursor]` - the decommissioned list (`useRevokedWorkers`);
  note this is a prefix match under `['workers']`, so a prefix invalidation of
  `['workers']` covers it.

Default strategy is **invalidate-on-success** (the pattern already used by
`useScheduleActions`), which is simplest and correct given the pages already
poll. Per action:

| Action | onSuccess |
|--------|-----------|
| Rename / labels / slots | `setQueryData(['worker', id], updated)` from the PATCH response, then `invalidateQueries(['worker', id])` and `invalidateQueries({ queryKey: ['workers'] })` so the list reflects the new name/status. |
| Disable / Drain | `invalidateQueries(['worker', id])` + `invalidateQueries({ queryKey: ['workers'] })`. On drain, surface `requeued_tasks` in a transient success note. |
| Enable | `invalidateQueries(['worker', id])` + `invalidateQueries({ queryKey: ['workers'] })`. |
| Revoke token | **Do not invalidate the detail query** (it will 404). Invalidate `['workers']` (prefix - covers both the active list and the `['workers','revoked',...]` decommissioned list) and `navigate('/workers')`. |
| Evict workspace | `invalidateQueries(['worker', id, 'workspaces'])`. Eviction is async, so the row will not vanish immediately; show an "eviction requested" note and let the 15s workspace poll reconcile. |

**Optimistic update - one, scoped, targeted case.** The interaction that most
benefits from optimism is the Disable/Enable toggle, where the ~3s detail poll
otherwise makes the status pill lag the click. For that toggle only, apply an
optimistic status change to `['worker', id]` in `onMutate`, snapshot the previous
value, and roll back in `onError` (the standard TanStack cancel/snapshot/rollback
recipe). All other actions use plain invalidate-on-success; adding optimism to
rename/labels/slots is not worth the rollback complexity, and revoke/evict have
navigation / async semantics that make optimism misleading. This gives Admin and
Profile a concrete, minimal optimistic reference without over-generalizing.

`invalidateQueries({ queryKey: ['workers'] })` (prefix match, no sort) is used so
the invalidation is decoupled from the active sort key, consistent with the
lesson recorded in the jobs/schedules query-key work.

## Confirmation UX

Destructive or disruptive actions require confirmation via `ConfirmDialog`.
Non-destructive actions (rename, edit labels, set slots, enable) apply directly.

| Action | Confirm? | Dialog copy (intent) | Confirm button |
|--------|----------|----------------------|----------------|
| Rename / labels / slots | No (form Save) | - | Save |
| Enable | No | - | Enable |
| Disable | Yes | "Disable {name}? It will stop receiving new tasks. In-flight tasks keep running." | Disable |
| Drain (disable?requeue=true) | Yes | "Drain {name}? It stops receiving new tasks and its in-flight tasks are requeued to other workers and cancelled here." | Drain |
| Revoke token | Yes (destructive variant) | "Revoke {name}'s agent token? This decommissions the worker. It disappears from the fleet and must re-enroll to return." | Revoke |
| Evict workspace | Yes | "Evict workspace {short_id} from {name}? The agent removes it on next opportunity. A held workspace is refused." | Evict |

While a mutation is in flight, its trigger button is disabled (mirrors the
`pendingId` disable pattern in `SchedulesTable`). Mutation errors render an
inline message near the action (reuse the existing `err`-token styling); they do
not tear down the page.

## States and edge cases

- **Already disabled / already enabled** - server no-ops; the button label
  reflects current status (`disabled_at` present => show Enable; else show
  Disable + Drain). No client-side guard needed beyond correct labeling.
- **Revoke while worker offline** - still succeeds (token clear is DB-side);
  navigate away as usual.
- **Evict of a held workspace** - endpoint returns 202 regardless; the agent
  refuses and the row remains on the next poll. Copy sets that expectation; no
  special client handling.
- **Concurrent edit** - the PATCH handler is a non-transactional
  read-modify-write (documented server-side as acceptable for v1 admin ops); the
  UI does not attempt optimistic concurrency control.
- **Non-admin** - no controls rendered at all.

## Testing

Test infra is the existing Vitest + MSW + `renderWithQuery` / `AuthProvider`
setup used by the read-only slice tests.

### `useWorkerActions.test.tsx` (mutation + invalidation)

- `updateWorker` PATCHes and writes/invalidates `['worker', id]` and `['workers']`.
- `disableWorker(id, false)` POSTs `/disable` (no query string) and invalidates.
- `disableWorker(id, true)` POSTs `/disable?requeue=true` and surfaces
  `requeued_tasks`.
- `enableWorker` POSTs `/enable` and invalidates.
- `revokeWorkerToken` DELETEs `/token`, does **not** invalidate `['worker', id]`,
  invalidates `['workers']`.
- `evictWorkspace` POSTs the evict path and invalidates `['worker', id, 'workspaces']`.
- Disable/Enable optimistic path: assert the cached `['worker', id]` status flips
  in `onMutate` and rolls back on a mocked error response.

### `WorkerActions` / `WorkerDetailPage` tests (admin gating + confirm UX)

- Admin sees the action bar; non-admin sees no controls (assert absence of each
  button; assert no Evict button in the workspaces panel).
- Clicking Disable/Drain/Revoke/Evict opens `ConfirmDialog`; cancel fires no
  request; confirm fires exactly one request to the right path.
- Revoke success navigates to `/workers` (assert route change via a memory-router
  location probe).
- Trigger button is disabled while the mutation is pending.
- Mutation error renders an inline message and leaves the page mounted.

### `WorkerEditForm` tests

- Pre-fills current name / labels / max_slots.
- Submits only changed fields (e.g. changing name only -> PATCH body has just
  `name`).
- Editing labels submits the full edited map (add + remove a key).

### `ConfirmDialog` tests

- Renders title/body, confirm and cancel; `role="dialog"`, labelled.
- Escape and Cancel both dismiss without confirming; Confirm invokes the callback.

### Contract verification

Re-confirm TS types field-for-field against Go: `DisableWorkerResponse` vs
`disableWorkerResponse` (adds `requeued_tasks int`); `WorkerPatch` fields vs the
`handleUpdateWorker` body struct (`name *string`, `labels map[string]string`,
`max_slots *int32`). Confirm 204 (revoke) and 202 (evict) are handled as no-body
by `apiFetch`.

## Success criteria

- Admins can rename, edit labels, set max_slots, disable, drain, enable, revoke
  token, and evict workspaces from `/workers/:id`; non-admins see none of these
  controls.
- Each action reflects its result without a manual refresh (invalidate-on-success;
  optimistic on the disable/enable toggle).
- Revoke navigates back to the list rather than stranding the user on a 404.
- Destructive/disruptive actions are gated behind a confirm dialog.
- Full web test suite and production build are green.
- A reusable mutation hook shape and a `ConfirmDialog` primitive exist for Admin
  and Profile to adopt.
