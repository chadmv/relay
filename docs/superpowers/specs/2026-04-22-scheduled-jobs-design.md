# Scheduled Jobs Design

**Date:** 2026-04-22
**Status:** Approved for implementation planning

## Goal

Add the ability to create auto-scheduled, recurring jobs — the relay equivalent of cron. A user defines a job spec once and a cron schedule; the server instantiates a fresh `Job` from that spec on every trigger.

## Non-Goals

- One-shot future-dated jobs ("run this job once, tomorrow at 9am"). Out of scope; a workaround is to create a schedule and disable it after it fires.
- Cross-job dependencies or coordination between scheduled runs.
- Exactly-once or missed-run catch-up semantics. On server downtime, missed triggers are dropped.

## Summary of Decisions

| Decision | Choice |
|---|---|
| Template model | Embedded job spec stored as JSONB on a `scheduled_jobs` row (Option A). |
| Schedule expression syntax | `github.com/robfig/cron/v3` — supports standard 5-field cron, `@hourly`/`@daily`/etc., and `@every <duration>`. |
| Timezone semantics | Per-schedule IANA timezone column, default `"UTC"`. |
| Overlap policy | Per-schedule `overlap_policy` column, values `"skip"` (default) or `"allow"`. |
| Catch-up on restart | Never catch up. On boot, advance `next_run_at` past any missed triggers. |
| Ownership / permissions | Any authenticated user can create/own schedules. Admins can manage everyone's. |
| Scheduler loop | 10-second ticker polling `scheduled_jobs` table. No LISTEN/NOTIFY. |

## Data Model

### New table `scheduled_jobs`

```sql
CREATE TABLE scheduled_jobs (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT         NOT NULL,
    owner_id        UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cron_expr       TEXT         NOT NULL,       -- e.g. "0 2 * * *" | "@hourly" | "@every 15m"
    timezone        TEXT         NOT NULL DEFAULT 'UTC',   -- IANA name
    job_spec        JSONB        NOT NULL,       -- full createJobRequest payload
    overlap_policy  TEXT         NOT NULL DEFAULT 'skip',  -- 'skip' | 'allow'
    enabled         BOOLEAN      NOT NULL DEFAULT TRUE,
    next_run_at     TIMESTAMPTZ  NOT NULL,
    last_run_at     TIMESTAMPTZ,
    last_job_id     UUID         REFERENCES jobs(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_scheduled_jobs_next_run ON scheduled_jobs(next_run_at) WHERE enabled;
CREATE INDEX idx_scheduled_jobs_owner ON scheduled_jobs(owner_id);
```

### Addition to existing `jobs` table

```sql
ALTER TABLE jobs ADD COLUMN scheduled_job_id UUID
    REFERENCES scheduled_jobs(id) ON DELETE SET NULL;
CREATE INDEX idx_jobs_scheduled_job_id ON jobs(scheduled_job_id);
```

**Why JSONB blob, not normalized template tables:** the spec is already accepted as JSON by `POST /v1/jobs`. Validation runs through the same code path. Schema changes to tasks don't require a parallel `task_templates` migration.

**Why `ON DELETE SET NULL` on `jobs.scheduled_job_id`:** deleting a schedule should not cascade-delete its job history. Running jobs keep executing; finished jobs keep their logs.

## Scheduler Loop

New package `internal/schedrunner/`:

```
internal/schedrunner/
  runner.go         // Runner struct, Run() loop, fireOne()
  runner_test.go
  cron.go           // thin wrapper over robfig/cron/v3 for parse + next-time
```

### Startup wiring

In `cmd/relay-server/main.go`, after migrations:

1. For every enabled row where `next_run_at < NOW()`, recompute `next_run_at` to the next future trigger using the stored cron expression and timezone, and `UPDATE` it. This implements the "never catch up" policy.
2. Start `schedrunner.Runner` as a goroutine alongside the existing task dispatcher.

### Main loop (10-second ticker)

```
for each tick:
  tx = Begin()
  rows = SELECT ... FROM scheduled_jobs
         WHERE enabled AND next_run_at <= NOW()
         FOR UPDATE SKIP LOCKED
         LIMIT 100
  for each row:
      jobID = fireOne(ctx, q, row)   // returns new job id, or nil if overlap-skipped
      next = cron.Next(row.cron_expr, row.timezone, NOW())
      UPDATE scheduled_jobs SET
         next_run_at = next,
         last_run_at = NOW(),                  -- always updated: records the trigger time
         last_job_id = COALESCE(<jobID>, last_job_id)  -- only updated on actual firing
         WHERE id = row.id
  tx.Commit()
```

`FOR UPDATE SKIP LOCKED` makes the loop safe if multiple server processes run concurrently (future-proofing; not required today).

### `fireOne`

Reuses a helper `createJobFromSpec(ctx, q, spec, submittedBy, scheduledJobID)` extracted from `handleCreateJob`. Both the HTTP handler and the scheduler runner call this helper. It issues `NotifyTaskSubmitted` at the end; the existing task dispatcher picks up the new tasks immediately via LISTEN/NOTIFY.

**Overlap handling:** if `overlap_policy = 'skip'`, before firing:

```sql
SELECT 1 FROM jobs
 WHERE scheduled_job_id = $1
   AND status IN ('pending','queued','running','dispatched')
 LIMIT 1
```

If a row is found, skip creating a new job (but still advance `next_run_at`). Log the skip at INFO level with the schedule name.

### Why not LISTEN/NOTIFY for schedule edits

Cron granularity is minute-level; a 10-second tick is well below that. Schedule edits are rare admin events. The existing `NotifyListener` pattern is appropriate for task submissions (high-frequency, low-latency); schedules don't warrant the same machinery.

## HTTP API

All routes under `/v1/scheduled-jobs`, behind `BearerAuth`. Ownership is enforced by the handler (owner-or-admin). New handler file: `internal/api/scheduled_jobs.go`.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/scheduled-jobs` | user | Create a schedule. Body: `{ name, cron_expr, timezone?, overlap_policy?, enabled?, job_spec: {...createJobRequest} }`. Server validates cron expression, timezone, and `job_spec` using the same validator as `POST /v1/jobs`. Returns 201 with computed `next_run_at`. |
| `GET` | `/v1/scheduled-jobs` | user | List. Non-admins see only their own; admins see all. Query params: `?enabled=true`, `?owner=<uuid>` (admin-only filter). |
| `GET` | `/v1/scheduled-jobs/{id}` | owner/admin | Fetch one. |
| `PATCH` | `/v1/scheduled-jobs/{id}` | owner/admin | Update any mutable field. If `cron_expr`, `timezone`, or `enabled` changes, recompute `next_run_at` in the same transaction. |
| `DELETE` | `/v1/scheduled-jobs/{id}` | owner/admin | Delete. Existing job instances keep their logs; `scheduled_job_id` becomes NULL via `ON DELETE SET NULL`. |
| `POST` | `/v1/scheduled-jobs/{id}/run-now` | owner/admin | Fire immediately, regardless of schedule. Ignores overlap policy (explicit user action). Returns the created `Job`. Does not affect `next_run_at`. |
| `GET` | `/v1/jobs?scheduled_job_id={id}` | user | Filter existing jobs listing by schedule. Adds one query param to `handleListJobs`. |

### Validation errors

- Invalid cron expression → 400, error body includes the parser's message.
- Invalid timezone → 400 (`time.LoadLocation` failed).
- Invalid `job_spec` → 400 with the same messages as `POST /v1/jobs`.
- Interval faster than 30s → 400 (footgun guard; see Edge Cases).

## CLI Surface

New `relay schedules` command group, mirroring existing `relay jobs` patterns:

```
relay schedules create --name NAME --cron EXPR [--tz ZONE] [--overlap skip|allow] --spec FILE.json
relay schedules list [--mine] [--enabled] [--owner USER]
relay schedules show <id-or-name>
relay schedules update <id> [--cron EXPR] [--tz ZONE] [--enable|--disable] [--overlap ...]
relay schedules delete <id>
relay schedules run-now <id>
```

`--spec FILE.json` accepts the same payload format as `relay jobs submit`, keeping one canonical spec format.

## Edge Cases

| Case | Behavior |
|---|---|
| Invalid cron at insert | Transaction rolls back; 400 to caller. |
| Timezone fails to load at fire time | Log ERROR, advance `next_run_at` by one minute (avoid hot loop), leave the schedule alone for an admin to fix. |
| Server clock jumps backward | `next_run_at` sits in the past longer; no special handling. |
| Owner deleted | `ON DELETE CASCADE` removes schedules. Running job instances keep `scheduled_job_id = NULL`. |
| DST transitions | `robfig/cron` handles these correctly via `cron.WithLocation`. |
| Very short intervals (`@every 1s`) | Rejected at insert; minimum 30s. |
| Server downtime spans many triggers | Only one missed run's `next_run_at` advancement applies; the schedule resumes at the next future trigger. No catch-up. |
| Two server processes running | `FOR UPDATE SKIP LOCKED` ensures exactly one process fires each trigger. |

## Testing

- **Unit tests** for the `cron.Next()` wrapper, including DST transitions in `America/Los_Angeles`.
- **Store integration tests** (extensions to `internal/store/store_test.go`) for all new queries: create, list-by-owner, advance-next-run, overlap-check.
- **API integration tests** (`internal/api/scheduled_jobs_test.go`) for CRUD, ownership enforcement, run-now, invalid inputs, admin overrides.
- **Runner integration test** (`internal/schedrunner/runner_test.go`) with a testcontainers Postgres: insert a schedule with `next_run_at = NOW() - 1s`, tick once, assert a job was created and `next_run_at` advanced correctly.
- **Overlap-skip test** — insert a schedule, pre-create a job with the schedule ID in `pending` state, tick, assert no new job was created but `next_run_at` still advanced.
- **Startup reconciliation test** — insert a schedule with `next_run_at` an hour in the past, simulate startup, assert `next_run_at` has been advanced to the next future trigger.

## Files Most Touched

| File | Purpose |
|---|---|
| `internal/store/migrations/000006_scheduled_jobs.up.sql` / `.down.sql` | New table, jobs column, indexes. |
| `internal/store/query/scheduled_jobs.sql` | sqlc queries. |
| `internal/store/scheduled_jobs.sql.go`, `models.go` | Regenerated. |
| `internal/api/scheduled_jobs.go` | HTTP handlers. |
| `internal/api/jobs.go` | Extract `createJobFromSpec` helper; add `scheduled_job_id` filter to list. |
| `internal/api/server.go` | Route registration. |
| `internal/schedrunner/runner.go` | Scheduler loop. |
| `internal/schedrunner/cron.go` | Cron parser wrapper. |
| `cmd/relay-server/main.go` | Startup reconciliation; goroutine wiring. |
| `internal/cli/schedules.go` | CLI commands. |
| `CLAUDE.md` | Document `relay schedules` and any new env vars. |
| `go.mod` | Add `github.com/robfig/cron/v3` dependency. |

## Out-of-Scope Extensions (future work, not this spec)

- Per-schedule audit log ("here are the last 100 times this schedule fired, and their outcomes").
- Web UI for schedule management.
- Inline schedule spec editing (currently requires re-submitting the full JSON blob).
- Alert / notification when a scheduled job fails repeatedly.
