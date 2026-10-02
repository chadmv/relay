# Task Worker Name Link Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show a task's worker by name, linked to `/workers/{id}`, on the job detail task table and the task log page header.

**Architecture:** `GET /v1/jobs/{id}` gains an optional per-task `worker_name`, filled by one batched `ListWorkerNamesByIDs` lookup after `ListTasksByJob`. The frontend adds a `TaskWorkerLink` component used by `TasksTable` and `TaskLogPage`.

**Tech Stack:** Go, sqlc, pgx, testify (integration tag); React, react-router, Vitest + Testing Library.

Spec: `docs/superpowers/specs/2026-10-02-task-worker-name-link-design.md`.

**Slices:** Task 1 is backend; Tasks 2-4 are frontend. They are independent (the frontend tests use fixtures, not the server) and may run in parallel. Within the frontend, 2 precedes 3 and 4.

**Repo traps (read CLAUDE.md):** CRLF repo. After `make generate`, keep only the real content change: run `git status` (not just `git diff`) and revert LF-only churn with `git checkout -- <file>` on files you did not mean to change, then verify `internal/store/workers.sql.go` still contains `ListWorkerNamesByIDs`. Commit with an explicit pathspec. Do NOT rebuild or commit `web/dist`. Comments follow CLAUDE.md "Comments" (no history, no counts, no claims about other code).

---

### Task 1: Backend - `worker_name` on `GET /v1/jobs/{id}`

**Files:**
- Modify: `internal/store/query/workers.sql` (append query)
- Generated: `internal/store/workers.sql.go` (via `make generate`; never hand-edit)
- Modify: `internal/api/jobs.go` (`taskResponse` struct ~line 42; `handleGetJob` ~line 802-828)
- Create: `internal/api/jobs_worker_name_integration_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/jobs_worker_name_integration_test.go`. It reuses `newRetryEnv` (worker `Name: "retry-w"`, `Hostname: "retry-host"`), `createTestUser`, `createTestToken` and `failOneQueryDB` from the same package.

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/stretchr/testify/require"
)

func getJobTasks(t *testing.T, srv *api.Server, token, jobID string) (int, []map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var resp struct {
		Tasks []map[string]any `json:"tasks"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return rec.Code, resp.Tasks
}

// The worker's name ("retry-w") differs from its hostname ("retry-host") and its
// id, so the assertion fails if the handler reads the wrong column. The pending
// task has no worker and must carry no worker_name key at all.
func TestGetJob_TaskCarriesItsWorkersName(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "worker-name@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "assigned", "running")
	e.task(t, job, "unassigned", "pending")

	code, tasks := getJobTasks(t, e.srv, token, uuidString(job.ID))
	require.Equal(t, http.StatusOK, code)
	require.Len(t, tasks, 2)
	byName := map[string]map[string]any{}
	for _, tk := range tasks {
		byName[tk["name"].(string)] = tk
	}
	require.Equal(t, "retry-w", byName["assigned"]["worker_name"])
	require.Equal(t, uuidString(e.w.ID), byName["assigned"]["worker_id"])
	_, has := byName["unassigned"]["worker_name"]
	require.False(t, has, "a task with no worker must omit worker_name")
}

// A failed name lookup answers 500 rather than a 200 with names silently missing.
func TestGetJob_WorkerNameReadFails_IsAnError(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "worker-name-fail@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "assigned", "running")

	var fired atomic.Int64
	crippled := api.New(e.pool, store.New(failOneQueryDB{pool: e.pool, name: "ListWorkerNamesByIDs", fired: &fired}),
		events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	code, _ := getJobTasks(t, crippled, token, uuidString(job.ID))
	require.Equal(t, http.StatusInternalServerError, code)
	require.NotZero(t, fired.Load(), "the injector must have matched ListWorkerNamesByIDs")
}
```

`uuidString(pgtype.UUID) string` already exists in this package (`internal/api/users_integration_test.go`); do not redeclare it.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestGetJob_TaskCarriesItsWorkersName|TestGetJob_WorkerNameReadFails_IsAnError' -v -timeout 300s`
Expected: first FAILS (`worker_name` nil, not "retry-w"); second FAILS (200, not 500).

- [ ] **Step 3: Add the query**

Append to `internal/store/query/workers.sql`:

```sql
-- name: ListWorkerNamesByIDs :many
SELECT id, name FROM workers WHERE id = ANY(@ids::uuid[]);
```

Run `make generate`, then the CRLF cleanup described in "Repo traps". Confirm `ListWorkerNamesByIDs(ctx context.Context, ids []pgtype.UUID) ([]ListWorkerNamesByIDsRow, error)` exists in `internal/store/workers.sql.go`.

- [ ] **Step 4: Implement**

In `internal/api/jobs.go`, add the field to `taskResponse` after `WorkerID`:

```go
	WorkerName     string          `json:"worker_name,omitempty"`
```

In `handleGetJob`, replace the final `writeJSON(w, http.StatusOK, toJobResponse(job, row.SubmittedByEmail, tasks, taskDeps))` with:

```go
	resp := toJobResponse(job, row.SubmittedByEmail, tasks, taskDeps)
	var workerIDs []pgtype.UUID
	seen := make(map[pgtype.UUID]bool)
	for _, t := range tasks {
		if t.WorkerID.Valid && !seen[t.WorkerID] {
			seen[t.WorkerID] = true
			workerIDs = append(workerIDs, t.WorkerID)
		}
	}
	if len(workerIDs) > 0 {
		rows, err := s.q.ListWorkerNamesByIDs(ctx, workerIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		names := make(map[pgtype.UUID]string, len(rows))
		for _, r := range rows {
			names[r.ID] = r.Name
		}
		// resp.Tasks is built index-aligned with tasks by toJobResponse.
		for i, t := range tasks {
			resp.Tasks[i].WorkerName = names[t.WorkerID]
		}
	}
	writeJSON(w, http.StatusOK, resp)
```

(`toJobResponse`'s signature is unchanged; it has many other callers.)

- [ ] **Step 5: Run tests to verify they pass**

Run the Step 2 command, plus `go test -tags integration -p 1 ./internal/api/... -run 'TestGetJob|TestCreateAndGetJob' -v -timeout 600s` and `go build ./... && go vet ./internal/api/...`.
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go internal/api/jobs.go internal/api/jobs_worker_name_integration_test.go
git commit -m "Return each task's worker name on GET /v1/jobs/{id}" -- internal/store/query/workers.sql internal/store/workers.sql.go internal/api/jobs.go internal/api/jobs_worker_name_integration_test.go
```
Check `git ls-files --eol` on these paths reads `i/lf` and the diffstat is proportionate. End the message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

---

### Task 2: Frontend - `TaskWorkerLink` component

**Files:**
- Modify: `web/src/jobs/api.ts` (`TaskDetail`, ~line 157-172)
- Create: `web/src/jobs/TaskWorkerLink.tsx`
- Create: `web/src/jobs/TaskWorkerLink.test.tsx`

- [ ] **Step 1: Add the type field**

In `TaskDetail`, after `worker_id?: string`:

```ts
  // Resolved server-side on GET /v1/jobs/:id only; absent when the task has no
  // worker or the worker row is gone.
  worker_name?: string
```

- [ ] **Step 2: Write the failing test**

`web/src/jobs/TaskWorkerLink.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { TaskWorkerLink } from './TaskWorkerLink'

function renderLink(task: { worker_id?: string; worker_name?: string }) {
  return render(
    <MemoryRouter>
      <TaskWorkerLink task={task} />
    </MemoryRouter>,
  )
}

test('no worker renders a dash and no link', () => {
  renderLink({})
  expect(screen.getByText('-')).toBeInTheDocument()
  expect(screen.queryByRole('link')).not.toBeInTheDocument()
})

test('a named worker renders its name as a link to the worker page', () => {
  renderLink({ worker_id: 'w9abc123-0000', worker_name: 'render-node-07' })
  const link = screen.getByRole('link', { name: 'render-node-07' })
  expect(link).toHaveAttribute('href', '/workers/w9abc123-0000')
})

test('a worker with no resolved name falls back to the short id, still linked', () => {
  renderLink({ worker_id: 'w9abc123-0000' })
  const link = screen.getByRole('link', { name: 'w9abc1' })
  expect(link).toHaveAttribute('href', '/workers/w9abc123-0000')
})
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd web && npx vitest run src/jobs/TaskWorkerLink.test.tsx`
Expected: FAIL, cannot resolve `./TaskWorkerLink`.

- [ ] **Step 4: Implement**

`web/src/jobs/TaskWorkerLink.tsx`:

```tsx
import type { MouseEvent } from 'react'
import { Link } from 'react-router-dom'
import type { TaskDetail } from './api'

// A task's worker as a link to its detail page. Click propagation is stopped so
// that inside a selectable row, following the link does not also select the row.
export function TaskWorkerLink({
  task,
  className = '',
}: {
  task: Pick<TaskDetail, 'worker_id' | 'worker_name'>
  className?: string
}) {
  if (!task.worker_id) return <span className={className}>-</span>
  return (
    <Link
      to={`/workers/${task.worker_id}`}
      onClick={(e: MouseEvent) => e.stopPropagation()}
      className={`hover:text-accent focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent ${className}`}
    >
      {task.worker_name || task.worker_id.slice(0, 6)}
    </Link>
  )
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd web && npx vitest run src/jobs/TaskWorkerLink.test.tsx && npx tsc -b`
Expected: 3 passed; tsc clean.

- [ ] **Step 6: Commit**

```bash
git commit -m "Add TaskWorkerLink: a task's worker by name, linked" -- web/src/jobs/api.ts web/src/jobs/TaskWorkerLink.tsx web/src/jobs/TaskWorkerLink.test.tsx
```
(after `git add` of the same paths; end with the Co-Authored-By line.)

---

### Task 3: Frontend - `TasksTable` worker cell

**Files:**
- Modify: `web/src/jobs/TasksTable.tsx` (worker cell line ~100; comment block ~lines 28-49)
- Modify: `web/src/jobs/TasksTable.test.tsx`

`TasksTable` will now render a router `<Link>`, so every render in `TasksTable.test.tsx` needs a router.

- [ ] **Step 1: Update the test file**

1. Add `import { MemoryRouter } from 'react-router-dom'` and a helper, and replace every `render(<TasksTable ... />)` with `renderTable(<TasksTable ... />)`:

```tsx
function renderTable(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}
```
(import `type ReactElement` from 'react' and use it instead of `React.ReactElement` if the file has no `React` namespace import.)

2. Give the `denoise` fixture a name: `worker_id: 'w9abc123', worker_name: 'render-node-07'`.

3. In `clicking a non-button cell in a row calls onSelect...`, replace the final no-link assertion and its comment with:

```tsx
  // Task selection is a click or key action, never navigation: the only link in
  // the table is the worker link, which leaves for the worker page.
  const links = screen.getAllByRole('link')
  expect(links).toHaveLength(1)
  expect(links[0]).toHaveAttribute('href', '/workers/w9abc123')
```

4. Add new tests:

```tsx
test('the worker cell shows the worker name as a link to the worker page', () => {
  renderTable(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={() => {}} />)
  expect(screen.getByRole('link', { name: 'render-node-07' })).toHaveAttribute('href', '/workers/w9abc123')
  // The 6-char id is no longer what the cell shows.
  expect(screen.queryByText('w9abc1')).not.toBeInTheDocument()
})

test('following the worker link does not select the row', async () => {
  const onSelect = vi.fn()
  renderTable(<TasksTable tasks={tasks} selectedTaskId="t1" onSelect={onSelect} />)
  await userEvent.click(screen.getByRole('link', { name: 'render-node-07' }))
  expect(onSelect).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: Run to verify the new tests fail**

Run: `cd web && npx vitest run src/jobs/TasksTable.test.tsx`
Expected: the two new tests and the modified link assertion FAIL (no link rendered); the rest pass.

- [ ] **Step 3: Implement**

In `TasksTable.tsx`, `import { TaskWorkerLink } from './TaskWorkerLink'` and replace the worker cell with:

```tsx
              <TableCell className="truncate text-fg-mute">
                <TaskWorkerLink task={t} />
              </TableCell>
```

Edit the comment block above `export function TasksTable`:
- In the "ONE HANDLER, BY BUBBLING" paragraph, append: "That rule is for the name-cell button. The worker cell's link is navigation, not selection, and stops its click from reaching the row; `following the worker link does not select the row` pins that."
- In the last paragraph, delete the sentence "The worker cell stays plain text; a link to the worker is a deferred follow-up."

- [ ] **Step 4: Run to verify they pass**

Run: `cd web && npx vitest run src/jobs/TasksTable.test.tsx src/jobs/JobDetailPage.test.tsx src/jobs/JobDetailPage.split.test.tsx && npx tsc -b`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git commit -m "Show the task table's worker by name, linked to the worker" -- web/src/jobs/TasksTable.tsx web/src/jobs/TasksTable.test.tsx
```
(after `git add`; Co-Authored-By line.)

---

### Task 4: Frontend - `TaskLogPage` header

**Files:**
- Modify: `web/src/jobs/TaskLogPage.tsx` (~line 63-65)
- Modify: `web/src/jobs/TaskLogPage.test.tsx` (fixture ~line 25; assertion ~line 68)

- [ ] **Step 1: Update the test**

In the `JOB` fixture task add `worker_name: 'render-node-07'` beside `worker_id: 'w1abcdef'`. Replace `expect(screen.getByText(/w1abcd/)).toBeInTheDocument()` with:

```tsx
  expect(screen.getByRole('link', { name: 'render-node-07' })).toHaveAttribute('href', '/workers/w1abcdef')
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npx vitest run src/jobs/TaskLogPage.test.tsx`
Expected: FAIL, no link named render-node-07.

- [ ] **Step 3: Implement**

`import { TaskWorkerLink } from './TaskWorkerLink'` and replace the span with:

```tsx
        <span className="font-mono text-[11px] text-fg-mute">
          worker <TaskWorkerLink task={task} /> · retry {task.retry_count}/{task.retries}
        </span>
```

- [ ] **Step 4: Run to verify it passes, then the whole web suite**

Run: `cd web && npx vitest run src/jobs/TaskLogPage.test.tsx && npx vitest run && npx tsc -b`
Expected: all pass. Do not run `vite build` (`web/dist` is tracked and not maintained per-PR).

- [ ] **Step 5: Commit**

```bash
git commit -m "Show the task log page's worker by name, linked to the worker" -- web/src/jobs/TaskLogPage.tsx web/src/jobs/TaskLogPage.test.tsx
```
(after `git add`; Co-Authored-By line.)
