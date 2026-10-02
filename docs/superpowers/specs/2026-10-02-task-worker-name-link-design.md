# Task worker shown by name, linked to the worker - design

## Problem

The job detail page's task table (`web/src/jobs/TasksTable.tsx`) and the single-task log page
header (`web/src/jobs/TaskLogPage.tsx`) render a task's worker as the first 6 characters of
`worker_id`. That identifies nothing to a user. The task table's own comment lists a link to the
worker as a deferred follow-up.

## Goal

Both places show the worker's **name** as a link to `/workers/{worker_id}`.

## Design

### Backend - `GET /v1/jobs/{id}`

- New sqlc query in `internal/store/query/workers.sql`:
  `ListWorkerNamesByIDs :many` - `SELECT id, name FROM workers WHERE id = ANY(@ids::uuid[])`.
  A separate lookup rather than a JOIN in `ListTasksByJob`, so `ListTasksByJob` keeps returning
  `store.Task` and its other callers are untouched.
- `taskResponse` gains `WorkerName string \`json:"worker_name,omitempty"\``.
- `handleGetJob` collects the distinct non-NULL `worker_id`s of the job's tasks, calls the query
  once (skipped when there are none), and sets `WorkerName` on the response's tasks, which
  `toJobResponse` builds index-aligned with `tasks`. `toJobResponse`'s signature is unchanged. A query error answers 500, like every other read in that handler.
- Scope: only `GET /v1/jobs/{id}`, the endpoint both pages read through `useJob`. Other endpoints
  returning `taskResponse` (`/v1/jobs/{id}/tasks`, `/v1/tasks/{id}`, `/v1/workers/{id}/tasks`) are
  unchanged; the field is `omitempty`, so they simply omit it.
- `tasks.worker_id` is `ON DELETE SET NULL`, so a task with a `worker_id` normally has a name. A
  worker deleted between the two reads leaves `worker_name` absent; the frontend handles that.

### Frontend

- `TaskDetail` (`web/src/jobs/api.ts`) gains `worker_name?: string`.
- One small shared component (`web/src/jobs/TaskWorkerLink.tsx`) renders, given a task:
  - no `worker_id`: `-`
  - otherwise a react-router `<Link to={/workers/${worker_id}}>` whose text is `worker_name`,
    falling back to the 6-char id when the name is absent.
- `TasksTable` WORKER cell uses it. The link calls `stopPropagation` on click so following it
  does not also fire the row's select handler. The table's "ONE HANDLER, BY BUBBLING" comment is
  amended: that rule governs the name-cell selection button; the worker link is a separate
  navigation control that must not select.
- `TaskLogPage` header renders `worker <TaskWorkerLink/> · retry n/m`.
- Styling: existing link idiom (accent on hover, focus-visible ring), truncating in the table cell.

## Testing

- Go integration (`internal/api`): a job with one task assigned to a named worker and one
  unassigned task; `GET /v1/jobs/{id}` returns `worker_name` equal to the worker's name on the
  first and no `worker_name` on the second. The worker name is distinct from its hostname and id
  so the assertion cannot pass on the wrong column.
- Vitest: `TaskWorkerLink` renders `-`, the name as a link to `/workers/{id}`, and the id
  fallback; `TasksTable` clicking the worker link does not call `onSelect`; `TaskLogPage` header
  shows the linked name.

## Out of scope

- Adding `worker_name` to the other task-returning endpoints, the CLI, or MCP output.
