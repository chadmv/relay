# Surface Revoked Workers for Admin Audit - Design

- **Date:** 2026-06-05
- **Backlog item:** [`idea-2026-06-04-surface-revoked-workers-admin-audit`](../../backlog/idea-2026-06-04-surface-revoked-workers-admin-audit.md)
- **Status:** Approved

## Problem

Revoked workers are invisible across the entire operational view. The
workers-stats work (PR #12) excluded `status = 'revoked'` from all 8 paginated
`ListWorkersPage*` queries, from `CountWorkers`, and from `WorkerStatusCounts`.
That is the correct default for the operational fleet view, but it removed the
only place a revoked worker was previously visible. An admin who needs to audit
which workers were decommissioned - and when - now has no surface for it.

## Scope

**Audit / visibility only.** This feature adds a read-only surface that lists
revoked workers. It deliberately does **not** add any mutating action
(re-enable, un-revoke, or permanent delete). Re-enrollment continues to happen
out-of-band exactly as today (a fresh enrollment token revives a revoked
worker; auto-enroll still refuses to).

Delivered across all three surfaces: REST API, `relay` CLI, and the React web
UI.

### Out of scope

- Any action to re-enable, un-revoke, or delete a revoked worker.
- Changes to `WorkerStatusCounts` / `GET /v1/workers/stats` - revoked workers
  stay excluded from the operational summary strip.
- The scheduler's `ListWorkers` query (used by the dispatch loop) - untouched.

## Design

### 1. Schema

New migration `000014_workers_revoked_at`:

```sql
-- up
ALTER TABLE workers ADD COLUMN revoked_at TIMESTAMPTZ NULL;
-- down
ALTER TABLE workers DROP COLUMN revoked_at;
```

No backfill. Workers revoked before this migration keep `revoked_at = NULL`;
they render as "unknown" and sort to the tail (`NULLS LAST`). No new index - the
revoked set is small and the query filters on `status = 'revoked'` first.

`revoked_at` is meaningful only while `status = 'revoked'`. It is stamped when a
worker is revoked and cleared when the worker regains valid credentials (see
query changes below), so a live worker never carries a stale stamp.

### 2. Store queries

Edited in `internal/store/query/workers.sql`; regenerate with `make generate`.
Never edit `*.sql.go` or `models.go` directly.

- **`ClearWorkerAgentToken`** (the revoke path) - stamp the time:

  ```sql
  UPDATE workers
  SET agent_token_hash = NULL, status = 'revoked', revoked_at = NOW()
  WHERE id = $1;
  ```

- **`SetWorkerAgentToken`** (the (re)enroll path) - clear the stamp:

  ```sql
  UPDATE workers SET agent_token_hash = $2, revoked_at = NULL WHERE id = $1;
  ```

  This query is called only from `enrollAndRegister` and
  `autoEnrollAndRegister`. The enrollment-token path is the one way a revoked
  worker is revived (revocation nulls the token, so the reconnect path can no
  longer find it). Clearing `revoked_at` here is therefore the correct, surgical
  revive point. In the auto-enroll path it is a harmless no-op (that path
  rejects revoked workers before reaching the upsert).

- **`ListRevokedWorkersPage`** - new. Mirrors the nullable-cursor predicate
  already proven in `ListWorkersPageByLastSeenDesc`, but filtered to revoked and
  keyed on `revoked_at`:

  ```sql
  SELECT * FROM workers
  WHERE status = 'revoked'
    AND (
         NOT @cursor_set::bool
      OR (
         CASE WHEN @cursor_is_null::bool THEN
              revoked_at IS NULL AND id < @cursor_id::uuid
         ELSE
              (revoked_at IS NOT NULL AND
               (revoked_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
           OR revoked_at IS NULL
         END
     ))
  ORDER BY revoked_at DESC NULLS LAST, id DESC
  LIMIT @page_limit + 1;
  ```

- **`CountRevokedWorkers`** - new:

  ```sql
  SELECT COUNT(*) FROM workers WHERE status = 'revoked';
  ```

### 3. REST API

- Route: `GET /v1/workers/revoked`, **admin-only**, registered in
  `internal/api/server.go` as `auth(admin(http.HandlerFunc(s.handleListRevokedWorkers)))`.
  Admin-only matches the revoke action (`DELETE /v1/workers/{id}/token` is
  admin-only) and the backlog's "admin-only listing" framing.
- Handler `handleListRevokedWorkers` in `internal/api/workers.go`: a
  single-arm version of `handleListWorkers`. It uses a minimal sort spec with
  one key so the cursor encoding and the `page[T]` envelope are identical to
  every other list endpoint:

  ```go
  var RevokedWorkersSortSpec = SortSpec{
      Default: "-revoked_at",
      Keys:    map[string]SortKeyKind{"revoked_at": SortKeyTimestamp},
  }
  ```

  It counts via `CountRevokedWorkers`, lists via `ListRevokedWorkersPage`, and
  builds the page with a `revoked_at` row-key helper analogous to
  `workersRowKeyByLastSeen`.
- `workerResponse` gains `RevokedAt *time.Time \`json:"revoked_at,omitempty"\``,
  populated in `toWorkerResponse` from `w.RevokedAt`. Additive and harmless on
  the operational endpoints, where it is always null (they exclude revoked).

### 4. CLI

In `internal/cli/workers.go`:

- `relay workers list --revoked` - a `--revoked` flag on the existing `list`
  command routes the request to `/v1/workers/revoked`. Chosen over a
  `relay workers revoked` subcommand to avoid one-keystroke collision with the
  existing `relay workers revoke`.
- `workerResp` gains `RevokedAt string \`json:"revoked_at"\``. In `--revoked`
  mode the table shows a `REVOKED AT` column (blank/"unknown" when null).
  `--json` returns the raw objects unchanged.

### 5. Web UI

In `web/src/workers/`:

- `api.ts`: `WorkerStatus` union gains `'revoked'`; `Worker` gains
  `revoked_at?: string`; new `listRevokedWorkers()` calling `/workers/revoked`.
- A view toggle on the Workers page: "Active" (existing list) and
  "Decommissioned" (revoked list). A `useRevokedWorkers` hook fetches the
  revoked page; the existing table is reused with a `revoked_at` column shown in
  the decommissioned view. Empty state: "No revoked workers."

### Invariants preserved

- Stats (`WorkerStatusCounts`) and the operational list still exclude revoked
  workers; their totals continue to agree.
- A re-enrolled worker (`status` flips to `online`) leaves the revoked list and
  carries no stale `revoked_at`.

## Testing

- **API integration** (`internal/api/`): revoked-list returns only revoked
  workers with `revoked_at`; operational list and stats still exclude them
  (regression guard); endpoint is admin-only (403 for non-admin user);
  re-enrollment via enrollment token clears `revoked_at` and drops the worker
  from the revoked list.
- **Store / unit:** `ClearWorkerAgentToken` stamps `revoked_at`;
  `SetWorkerAgentToken` clears it.
- **CLI:** `relay workers list --revoked` targets `/v1/workers/revoked` and
  renders the `REVOKED AT` column.
- **Web:** `api.test.ts` covers `listRevokedWorkers`; a render test covers the
  decommissioned view and its empty state.

## Files Touched

- `internal/store/migrations/000014_workers_revoked_at.{up,down}.sql` - new.
- `internal/store/query/workers.sql` - edit two queries, add two; `make generate`.
- `internal/api/workers.go` - `RevokedWorkersSortSpec`, `handleListRevokedWorkers`,
  `revoked_at` on `workerResponse`/`toWorkerResponse`.
- `internal/api/server.go` - route registration.
- `internal/cli/workers.go` - `--revoked` flag, `REVOKED AT` column.
- `web/src/workers/api.ts`, `useRevokedWorkers.ts` (new), `WorkersPage.tsx`,
  table component - view toggle and decommissioned view.
- Tests as listed above.
- `README.md` - document `GET /v1/workers/revoked` and `relay workers list --revoked`.
