# Scheduled Jobs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add cron-style scheduled jobs to relay — users define a job template + cron schedule, the server instantiates fresh jobs on each trigger.

**Architecture:** A new `scheduled_jobs` table stores the cron expression, per-schedule IANA timezone, overlap policy, and the job spec as JSONB. A new `internal/schedrunner` package runs a 10-second ticker goroutine that polls eligible schedules, fires them via a helper extracted from `handleCreateJob`, and advances `next_run_at`. HTTP/CLI surface mirrors the existing `/v1/jobs` and `relay jobs` patterns.

**Tech Stack:** Go 1.26, pgx/v5, sqlc, `github.com/robfig/cron/v3`, testcontainers-go (integration), stretchr/testify, net/http stdlib.

**Spec:** [`docs/superpowers/specs/2026-04-22-scheduled-jobs-design.md`](../specs/2026-04-22-scheduled-jobs-design.md)

---

## Task 1: Add robfig/cron dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add dependency**

Run: `go get github.com/robfig/cron/v3@v3.0.1`
Expected: `go.mod` / `go.sum` updated; no compile errors from `go build ./...`.

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add robfig/cron/v3 for scheduled job expressions"
```

---

## Task 2: Database migration (up/down)

**Files:**
- Create: `internal/store/migrations/000006_scheduled_jobs.up.sql`
- Create: `internal/store/migrations/000006_scheduled_jobs.down.sql`

- [ ] **Step 1: Write the up migration**

Create `internal/store/migrations/000006_scheduled_jobs.up.sql`:

```sql
CREATE TABLE scheduled_jobs (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT         NOT NULL,
    owner_id        UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cron_expr       TEXT         NOT NULL,
    timezone        TEXT         NOT NULL DEFAULT 'UTC',
    job_spec        JSONB        NOT NULL,
    overlap_policy  TEXT         NOT NULL DEFAULT 'skip',
    enabled         BOOLEAN      NOT NULL DEFAULT TRUE,
    next_run_at     TIMESTAMPTZ  NOT NULL,
    last_run_at     TIMESTAMPTZ,
    last_job_id     UUID         REFERENCES jobs(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_scheduled_jobs_next_run ON scheduled_jobs(next_run_at) WHERE enabled;
CREATE INDEX idx_scheduled_jobs_owner ON scheduled_jobs(owner_id);

ALTER TABLE jobs ADD COLUMN scheduled_job_id UUID
    REFERENCES scheduled_jobs(id) ON DELETE SET NULL;
CREATE INDEX idx_jobs_scheduled_job_id ON jobs(scheduled_job_id);
```

- [ ] **Step 2: Write the down migration**

Create `internal/store/migrations/000006_scheduled_jobs.down.sql`:

```sql
DROP INDEX IF EXISTS idx_jobs_scheduled_job_id;
ALTER TABLE jobs DROP COLUMN IF EXISTS scheduled_job_id;
DROP INDEX IF EXISTS idx_scheduled_jobs_owner;
DROP INDEX IF EXISTS idx_scheduled_jobs_next_run;
DROP TABLE IF EXISTS scheduled_jobs;
```

- [ ] **Step 3: Verify migrations apply cleanly**

Run: `go test -tags integration ./internal/store/... -run TestMigrate -timeout 120s`
Expected: PASS (migrations apply up and down without error).

If `TestMigrate` does not exist, instead run the full integration suite for store, which exercises migrations on every container start:

Run: `go test -tags integration -p 1 ./internal/store/... -timeout 120s`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations/000006_scheduled_jobs.up.sql internal/store/migrations/000006_scheduled_jobs.down.sql
git commit -m "feat(store): scheduled_jobs table + jobs.scheduled_job_id FK"
```

---

## Task 3: sqlc queries for scheduled_jobs

**Files:**
- Create: `internal/store/query/scheduled_jobs.sql`
- Modify: `internal/store/query/jobs.sql` (add filtered list)
- Regenerate: `internal/store/scheduled_jobs.sql.go`, `internal/store/models.go`, `internal/store/jobs.sql.go`

- [ ] **Step 1: Write the queries file**

Create `internal/store/query/scheduled_jobs.sql`:

```sql
-- name: CreateScheduledJob :one
INSERT INTO scheduled_jobs (
    name, owner_id, cron_expr, timezone, job_spec,
    overlap_policy, enabled, next_run_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetScheduledJob :one
SELECT * FROM scheduled_jobs WHERE id = $1;

-- name: ListScheduledJobs :many
SELECT * FROM scheduled_jobs ORDER BY created_at DESC;

-- name: ListScheduledJobsByOwner :many
SELECT * FROM scheduled_jobs WHERE owner_id = $1 ORDER BY created_at DESC;

-- name: UpdateScheduledJob :one
UPDATE scheduled_jobs
SET name           = $2,
    cron_expr      = $3,
    timezone       = $4,
    job_spec       = $5,
    overlap_policy = $6,
    enabled        = $7,
    next_run_at    = $8,
    updated_at     = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteScheduledJob :execrows
DELETE FROM scheduled_jobs WHERE id = $1;

-- name: ListEligibleScheduledJobs :many
SELECT * FROM scheduled_jobs
 WHERE enabled
   AND next_run_at <= NOW()
 ORDER BY next_run_at ASC
 LIMIT $1
 FOR UPDATE SKIP LOCKED;

-- name: ListOverdueScheduledJobsForCatchup :many
SELECT * FROM scheduled_jobs
 WHERE enabled
   AND next_run_at < NOW();

-- name: AdvanceScheduledJob :exec
UPDATE scheduled_jobs
SET next_run_at = $2,
    last_run_at = NOW(),
    last_job_id = COALESCE($3, last_job_id),
    updated_at  = NOW()
WHERE id = $1;

-- name: CountActiveJobsForSchedule :one
SELECT COUNT(*) FROM jobs
 WHERE scheduled_job_id = $1
   AND status IN ('pending','queued','running','dispatched');
```

- [ ] **Step 2: Add scheduled_job_id filter to jobs list query**

Modify `internal/store/query/jobs.sql` — append:

```sql
-- name: ListJobsByScheduledJob :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.scheduled_job_id = $1
ORDER BY j.created_at DESC;
```

Note: the existing `CreateJob` insert does not include `scheduled_job_id`. Modify it to accept the new column:

Replace in `internal/store/query/jobs.sql`:

```sql
-- name: CreateJob :one
INSERT INTO jobs (name, priority, submitted_by, labels, scheduled_job_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;
```

- [ ] **Step 3: Regenerate sqlc**

Run: `make generate`
Expected: `internal/store/scheduled_jobs.sql.go` created; `internal/store/models.go` updated to include `ScheduledJob` struct and new `Job.ScheduledJobID pgtype.UUID` field; `internal/store/jobs.sql.go` updated.

- [ ] **Step 4: Update all existing CreateJob callers**

The sqlc regeneration will break all callers of `q.CreateJob(...)` because the `CreateJobParams` struct gained a new field `ScheduledJobID pgtype.UUID`.

Run: `grep -rn "CreateJob(" --include="*.go" | grep -v "_test.go"`
Expected: matches in `internal/api/jobs.go` (and possibly tests).

For each match, add the new `ScheduledJobID` field to the params literal. Check the regenerated `internal/store/jobs.sql.go` for the exact field type — sqlc may emit it as `pgtype.UUID` (zero-value = NULL via `.Valid == false`) or `*pgtype.UUID` (nil = NULL) depending on settings. Use the appropriate zero value.

Example for `pgtype.UUID` (non-pointer):

```go
job, err := q.CreateJob(ctx, store.CreateJobParams{
    Name:            req.Name,
    Priority:        req.Priority,
    SubmittedBy:     u.ID,
    Labels:          labelsJSON,
    ScheduledJobID:  pgtype.UUID{},  // invalid → NULL; set by schedrunner when applicable
})
```

If the field is a pointer (`*pgtype.UUID`), omit it (defaults to nil) or set it to `nil` explicitly.

Apply the same update to any test files that construct `CreateJobParams` directly. Run `go build ./...` after to catch all call sites.

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: success.

Run: `go test ./internal/store/... -timeout 60s`
Expected: PASS (unit tests).

- [ ] **Step 6: Commit**

```bash
git add internal/store
git commit -m "feat(store): sqlc queries for scheduled_jobs"
```

---

## Task 4: Cron wrapper package

**Files:**
- Create: `internal/schedrunner/cron.go`
- Create: `internal/schedrunner/cron_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/schedrunner/cron_test.go`:

```go
package schedrunner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseSchedule_StandardCron(t *testing.T) {
	s, err := ParseSchedule("0 2 * * *", "UTC")
	require.NoError(t, err)
	base := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	require.Equal(t, time.Date(2026, 4, 22, 2, 0, 0, 0, time.UTC), next)
}

func TestParseSchedule_Predefined(t *testing.T) {
	s, err := ParseSchedule("@hourly", "UTC")
	require.NoError(t, err)
	base := time.Date(2026, 4, 22, 3, 17, 0, 0, time.UTC)
	next := s.Next(base)
	require.Equal(t, time.Date(2026, 4, 22, 4, 0, 0, 0, time.UTC), next)
}

func TestParseSchedule_EveryDuration(t *testing.T) {
	s, err := ParseSchedule("@every 15m", "UTC")
	require.NoError(t, err)
	base := time.Date(2026, 4, 22, 3, 0, 0, 0, time.UTC)
	next := s.Next(base)
	require.Equal(t, time.Date(2026, 4, 22, 3, 15, 0, 0, time.UTC), next)
}

func TestParseSchedule_Timezone(t *testing.T) {
	// "0 9 * * *" in America/Los_Angeles = 17:00 UTC during PDT (April).
	s, err := ParseSchedule("0 9 * * *", "America/Los_Angeles")
	require.NoError(t, err)
	base := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	require.Equal(t, 17, next.UTC().Hour())
	require.Equal(t, 0, next.UTC().Minute())
}

func TestParseSchedule_InvalidCron(t *testing.T) {
	_, err := ParseSchedule("not a cron", "UTC")
	require.Error(t, err)
}

func TestParseSchedule_InvalidTimezone(t *testing.T) {
	_, err := ParseSchedule("@hourly", "Not/A_Real_Zone")
	require.Error(t, err)
}

func TestValidateMinInterval_TooShort(t *testing.T) {
	err := ValidateMinInterval("@every 5s", "UTC", 30*time.Second)
	require.Error(t, err)
}

func TestValidateMinInterval_LongEnough(t *testing.T) {
	err := ValidateMinInterval("@every 30s", "UTC", 30*time.Second)
	require.NoError(t, err)
}

func TestValidateMinInterval_StandardCron(t *testing.T) {
	// "* * * * *" fires every minute (60s), well above a 30s minimum.
	err := ValidateMinInterval("* * * * *", "UTC", 30*time.Second)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/schedrunner/... -run TestParseSchedule -v`
Expected: FAIL with "undefined: ParseSchedule" or similar.

- [ ] **Step 3: Implement cron.go**

Create `internal/schedrunner/cron.go`:

```go
// Package schedrunner runs scheduled jobs: parses cron expressions, ticks on a
// timer, and fires eligible schedules by creating fresh job instances.
package schedrunner

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// parser accepts standard 5-field cron, predefined schedules (@hourly, etc.),
// and @every <duration>.
var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// Schedule wraps a parsed cron expression bound to a timezone.
type Schedule struct {
	inner cron.Schedule
	loc   *time.Location
}

// Next returns the next firing time strictly after the given base time.
func (s *Schedule) Next(base time.Time) time.Time {
	// robfig/cron evaluates in the location it was parsed with. Convert
	// base into that location, compute next, then return in UTC for storage.
	return s.inner.Next(base.In(s.loc)).UTC()
}

// ParseSchedule parses a cron expression against an IANA timezone name.
func ParseSchedule(expr, tz string) (*Schedule, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	inner, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	// Wrap with a location-aware schedule by re-parsing via cron.SpecSchedule
	// — robfig/cron's SpecSchedule has a Location field we can't set directly.
	// The simpler approach: construct a cron.Schedule-like wrapper that
	// evaluates Next in the desired location.
	//
	// Trick: SpecSchedule honors the time location of the argument passed to
	// Next. So we pass base.In(loc) in our Next() method above.
	return &Schedule{inner: inner, loc: loc}, nil
}

// ValidateMinInterval rejects schedules that would fire faster than min.
// Computes two consecutive fire times and checks the gap.
func ValidateMinInterval(expr, tz string, min time.Duration) error {
	s, err := ParseSchedule(expr, tz)
	if err != nil {
		return err
	}
	// Probe from a fixed anchor to keep results deterministic.
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := s.Next(anchor)
	b := s.Next(a)
	if b.Sub(a) < min {
		return fmt.Errorf("schedule fires faster than minimum interval %s (observed %s)", min, b.Sub(a))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/schedrunner/... -run TestParseSchedule -v`
Run: `go test ./internal/schedrunner/... -run TestValidateMinInterval -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/schedrunner/cron.go internal/schedrunner/cron_test.go
git commit -m "feat(schedrunner): cron expression parser with timezone support"
```

---

## Task 5: Extract `createJobFromSpec` helper

**Why:** Both `handleCreateJob` (user-submitted) and the scheduler runner (automated) need to create a Job + its tasks + dependencies from a spec. Extract the shared logic into one helper to avoid duplication.

**Files:**
- Modify: `internal/api/jobs.go`
- Create: `internal/api/job_spec.go` (new file to hold the helper)
- Modify: `internal/api/jobs_test.go` (verify `handleCreateJob` still passes after refactor)

- [ ] **Step 1: Write a test for the helper**

Create `internal/api/job_spec_test.go` (build tag `integration`; add to existing integration harness pattern):

```go
//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"relay/internal/api"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestCreateJobFromSpec_CreatesJobAndTasks(t *testing.T) {
	h := newIntegrationHarness(t)  // existing helper; see internal/api/*_test.go
	defer h.cleanup()

	userID := h.createUser(t, "alice@example.com")

	spec := api.JobSpec{
		Name:     "nightly-render",
		Priority: "normal",
		Labels:   map[string]string{"project": "test"},
		Tasks: []api.TaskSpec{
			{Name: "render", Command: []string{"echo", "hi"}},
		},
	}

	var scheduledID pgtype.UUID // invalid = NULL
	tx, err := h.pool.Begin(context.Background())
	require.NoError(t, err)
	defer tx.Rollback(context.Background())

	job, tasks, err := api.CreateJobFromSpec(
		context.Background(), h.q.WithTx(tx), spec, userID, scheduledID,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(context.Background()))

	require.Equal(t, "nightly-render", job.Name)
	require.Len(t, tasks, 1)
	require.Equal(t, "render", tasks[0].Name)

	var labels map[string]string
	require.NoError(t, json.Unmarshal(job.Labels, &labels))
	require.Equal(t, "test", labels["project"])
}
```

Note: `newIntegrationHarness` is a pattern used elsewhere in `internal/api/*_test.go` (integration tag). If its exact signature differs, adapt accordingly — the key point is to use the existing test database harness rather than inventing a new one.

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCreateJobFromSpec -v -timeout 120s`
Expected: FAIL (undefined: api.CreateJobFromSpec, api.JobSpec, api.TaskSpec).

- [ ] **Step 3: Create the helper file**

Create `internal/api/job_spec.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// JobSpec is the canonical representation of a job template, used by both
// user-submitted jobs and scheduled-job templates. Matches createJobRequest.
type JobSpec struct {
	Name     string            `json:"name"`
	Priority string            `json:"priority"`
	Labels   map[string]string `json:"labels"`
	Tasks    []TaskSpec        `json:"tasks"`
}

// TaskSpec mirrors the existing taskSpec type, exported for reuse.
type TaskSpec struct {
	Name           string            `json:"name"`
	Command        []string          `json:"command"`
	Env            map[string]string `json:"env"`
	Requires       map[string]string `json:"requires"`
	TimeoutSeconds *int32            `json:"timeout_seconds"`
	Retries        int32             `json:"retries"`
	DependsOn      []string          `json:"depends_on"`
}

// ValidateJobSpec applies the same validation as POST /v1/jobs. Returns an
// error with a caller-facing message on the first problem.
func ValidateJobSpec(spec JobSpec) error {
	if spec.Name == "" {
		return errors.New("name is required")
	}
	if len(spec.Tasks) == 0 {
		return errors.New("at least one task is required")
	}
	nameSet := make(map[string]struct{}, len(spec.Tasks))
	for _, ts := range spec.Tasks {
		if ts.Name == "" {
			return errors.New("task name is required")
		}
		if len(ts.Command) == 0 {
			return errors.New("task command is required")
		}
		if _, dup := nameSet[ts.Name]; dup {
			return fmt.Errorf("duplicate task name: %s", ts.Name)
		}
		nameSet[ts.Name] = struct{}{}
	}
	for _, ts := range spec.Tasks {
		for _, dep := range ts.DependsOn {
			if _, ok := nameSet[dep]; !ok {
				return fmt.Errorf("unknown depends_on: %s", dep)
			}
		}
	}
	return nil
}

// CreateJobFromSpec inserts a job, its tasks, and task dependencies inside the
// provided (transactional) Queries. Caller owns Begin/Commit. Emits
// NotifyTaskSubmitted on success.
//
// If scheduledID is a valid UUID, the resulting job.scheduled_job_id is set.
func CreateJobFromSpec(
	ctx context.Context,
	q *store.Queries,
	spec JobSpec,
	submittedBy pgtype.UUID,
	scheduledID pgtype.UUID,
) (store.Job, []store.Task, error) {
	if err := ValidateJobSpec(spec); err != nil {
		return store.Job{}, nil, err
	}

	priority := spec.Priority
	if priority == "" {
		priority = "normal"
	}

	labelsJSON, err := json.Marshal(spec.Labels)
	if err != nil {
		return store.Job{}, nil, fmt.Errorf("marshal labels: %w", err)
	}

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:            spec.Name,
		Priority:        priority,
		SubmittedBy:     submittedBy,
		Labels:          labelsJSON,
		ScheduledJobID:  scheduledID,
	})
	if err != nil {
		return store.Job{}, nil, fmt.Errorf("create job: %w", err)
	}

	nameToID := make(map[string]pgtype.UUID, len(spec.Tasks))
	tasks := make([]store.Task, 0, len(spec.Tasks))
	for _, ts := range spec.Tasks {
		envJSON, err := json.Marshal(ts.Env)
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("marshal env for %s: %w", ts.Name, err)
		}
		requiresJSON, err := json.Marshal(ts.Requires)
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("marshal requires for %s: %w", ts.Name, err)
		}
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID:          job.ID,
			Name:           ts.Name,
			Command:        ts.Command,
			Env:            envJSON,
			Requires:       requiresJSON,
			TimeoutSeconds: ts.TimeoutSeconds,
			Retries:        ts.Retries,
		})
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("create task %s: %w", ts.Name, err)
		}
		nameToID[ts.Name] = task.ID
		tasks = append(tasks, task)
	}

	for _, ts := range spec.Tasks {
		for _, depName := range ts.DependsOn {
			if err := q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
				TaskID:          nameToID[ts.Name],
				DependsOnTaskID: nameToID[depName],
			}); err != nil {
				return store.Job{}, nil, fmt.Errorf("create dependency %s->%s: %w", ts.Name, depName, err)
			}
		}
	}

	_ = q.NotifyTaskSubmitted(ctx)

	return job, tasks, nil
}
```

- [ ] **Step 4: Refactor `handleCreateJob` to use the helper**

Modify `internal/api/jobs.go`:

Replace the body of `handleCreateJob` (currently lines 106-252) with:

```go
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	spec := JobSpec{
		Name:     req.Name,
		Priority: req.Priority,
		Labels:   req.Labels,
		Tasks:    make([]TaskSpec, len(req.Tasks)),
	}
	for i, t := range req.Tasks {
		spec.Tasks[i] = TaskSpec(t)
	}

	if err := ValidateJobSpec(spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin transaction failed")
		return
	}
	defer tx.Rollback(ctx)

	job, tasks, err := CreateJobFromSpec(ctx, s.q.WithTx(tx), spec, u.ID, pgtype.UUID{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create job failed: "+err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit failed")
		return
	}

	taskDeps := make(map[pgtype.UUID][]string, len(spec.Tasks))
	for i, ts := range spec.Tasks {
		taskDeps[tasks[i].ID] = ts.DependsOn
	}

	writeJSON(w, http.StatusCreated, toJobResponse(job, u.Email, tasks, taskDeps))
}
```

Keep `taskSpec` and `createJobRequest` types in `jobs.go` — they define the HTTP wire format. `TaskSpec(t)` works because the fields are identical.

Remove any now-unused imports from `jobs.go` (check with `goimports` or `go build`).

- [ ] **Step 5: Run all jobs-related tests**

Run: `go test ./internal/api/... -run TestCreateJob -v -timeout 60s`
Run: `go test -tags integration -p 1 ./internal/api/... -run TestCreateJob -v -timeout 120s`
Expected: PASS — all existing handler tests still pass plus the new helper test.

- [ ] **Step 6: Commit**

```bash
git add internal/api/jobs.go internal/api/job_spec.go internal/api/job_spec_test.go
git commit -m "refactor(api): extract CreateJobFromSpec helper from handleCreateJob"
```

---

## Task 6: HTTP handler — POST /v1/scheduled-jobs

**Files:**
- Create: `internal/api/scheduled_jobs.go`
- Create: `internal/api/scheduled_jobs_test.go` (integration build tag)

- [ ] **Step 1: Write failing tests for create**

Create `internal/api/scheduled_jobs_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateScheduledJob_HappyPath(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()

	token := h.registerAndLogin(t, "alice@example.com")

	body := `{
        "name": "nightly",
        "cron_expr": "0 2 * * *",
        "timezone": "UTC",
        "job_spec": {
            "name": "render",
            "priority": "normal",
            "tasks": [{"name":"t1","command":["echo","hi"]}]
        }
    }`
	req, _ := http.NewRequest("POST", h.server.URL+"/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.NotEmpty(t, got["id"])
	require.Equal(t, "nightly", got["name"])
	require.Equal(t, "0 2 * * *", got["cron_expr"])
	require.NotEmpty(t, got["next_run_at"])
}

func TestCreateScheduledJob_InvalidCron(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()
	token := h.registerAndLogin(t, "alice@example.com")

	body := `{"name":"x","cron_expr":"not valid","job_spec":{"name":"r","tasks":[{"name":"t","command":["echo"]}]}}`
	req, _ := http.NewRequest("POST", h.server.URL+"/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestCreateScheduledJob_InvalidTimezone(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()
	token := h.registerAndLogin(t, "alice@example.com")

	body := `{"name":"x","cron_expr":"@hourly","timezone":"Not/Real","job_spec":{"name":"r","tasks":[{"name":"t","command":["echo"]}]}}`
	req, _ := http.NewRequest("POST", h.server.URL+"/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestCreateScheduledJob_TooShortInterval(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()
	token := h.registerAndLogin(t, "alice@example.com")

	body := `{"name":"x","cron_expr":"@every 5s","job_spec":{"name":"r","tasks":[{"name":"t","command":["echo"]}]}}`
	req, _ := http.NewRequest("POST", h.server.URL+"/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
```

Adapt `newIntegrationHarness` / `registerAndLogin` calls to existing helpers — mirror the pattern used in other `*_integration_test.go` files under `internal/api/`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCreateScheduledJob -v -timeout 120s`
Expected: FAIL (404 or 500 because handler doesn't exist yet).

- [ ] **Step 3: Implement POST handler**

Create `internal/api/scheduled_jobs.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const minScheduleInterval = 30 * time.Second

type scheduledJobResponse struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	OwnerID        string          `json:"owner_id"`
	CronExpr       string          `json:"cron_expr"`
	Timezone       string          `json:"timezone"`
	JobSpec        json.RawMessage `json:"job_spec"`
	OverlapPolicy  string          `json:"overlap_policy"`
	Enabled        bool            `json:"enabled"`
	NextRunAt      time.Time       `json:"next_run_at"`
	LastRunAt      *time.Time      `json:"last_run_at,omitempty"`
	LastJobID      string          `json:"last_job_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func toScheduledJobResponse(sj store.ScheduledJob) scheduledJobResponse {
	out := scheduledJobResponse{
		ID:            uuidStr(sj.ID),
		Name:          sj.Name,
		OwnerID:       uuidStr(sj.OwnerID),
		CronExpr:      sj.CronExpr,
		Timezone:      sj.Timezone,
		JobSpec:       rawJSON(sj.JobSpec),
		OverlapPolicy: sj.OverlapPolicy,
		Enabled:       sj.Enabled,
		NextRunAt:     sj.NextRunAt.Time,
		CreatedAt:     sj.CreatedAt.Time,
		UpdatedAt:     sj.UpdatedAt.Time,
	}
	if sj.LastRunAt.Valid {
		t := sj.LastRunAt.Time
		out.LastRunAt = &t
	}
	if sj.LastJobID.Valid {
		out.LastJobID = uuidStr(sj.LastJobID)
	}
	return out
}

type createScheduledJobRequest struct {
	Name          string          `json:"name"`
	CronExpr      string          `json:"cron_expr"`
	Timezone      string          `json:"timezone"`
	OverlapPolicy string          `json:"overlap_policy"`
	Enabled       *bool           `json:"enabled"`
	JobSpec       json.RawMessage `json:"job_spec"`
}

func (s *Server) handleCreateScheduledJob(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createScheduledJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.CronExpr == "" {
		writeError(w, http.StatusBadRequest, "cron_expr is required")
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if req.OverlapPolicy == "" {
		req.OverlapPolicy = "skip"
	}
	if req.OverlapPolicy != "skip" && req.OverlapPolicy != "allow" {
		writeError(w, http.StatusBadRequest, "overlap_policy must be 'skip' or 'allow'")
		return
	}

	// Parse and validate the job spec against the same rules as POST /v1/jobs.
	var spec JobSpec
	if err := json.Unmarshal(req.JobSpec, &spec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid job_spec JSON")
		return
	}
	if err := ValidateJobSpec(spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate cron + timezone + minimum interval.
	if err := schedrunner.ValidateMinInterval(req.CronExpr, req.Timezone, minScheduleInterval); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sched, err := schedrunner.ParseSchedule(req.CronExpr, req.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	next := sched.Next(time.Now())

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	row, err := s.q.CreateScheduledJob(r.Context(), store.CreateScheduledJobParams{
		Name:          req.Name,
		OwnerID:       u.ID,
		CronExpr:      req.CronExpr,
		Timezone:      req.Timezone,
		JobSpec:       req.JobSpec,
		OverlapPolicy: req.OverlapPolicy,
		Enabled:       enabled,
		NextRunAt:     pgtype.Timestamptz{Time: next, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create scheduled job failed")
		return
	}

	writeJSON(w, http.StatusCreated, toScheduledJobResponse(row))
}

// ownedScheduledJob fetches a schedule and verifies the caller is the owner or
// an admin. Returns the row and whether the caller has access. writeError is
// already called on failure paths.
func (s *Server) ownedScheduledJob(w http.ResponseWriter, r *http.Request, id pgtype.UUID) (store.ScheduledJob, bool) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return store.ScheduledJob{}, false
	}
	row, err := s.q.GetScheduledJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "scheduled job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return store.ScheduledJob{}, false
	}
	if !u.IsAdmin && row.OwnerID != u.ID {
		writeError(w, http.StatusNotFound, "scheduled job not found") // don't leak existence
		return store.ScheduledJob{}, false
	}
	return row, true
}
```

- [ ] **Step 4: Register the route**

Modify `internal/api/server.go` — add after the agent enrollment routes (around line 109):

```go
// Scheduled jobs
mux.Handle("POST /v1/scheduled-jobs", auth(http.HandlerFunc(s.handleCreateScheduledJob)))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCreateScheduledJob -v -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/scheduled_jobs.go internal/api/scheduled_jobs_test.go internal/api/server.go
git commit -m "feat(api): POST /v1/scheduled-jobs"
```

---

## Task 7: HTTP handlers — GET list, GET one, PATCH, DELETE

**Files:**
- Modify: `internal/api/scheduled_jobs.go`
- Modify: `internal/api/scheduled_jobs_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/api/scheduled_jobs_test.go`:

```go
func TestListScheduledJobs_OwnerOnlySeesOwn(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()

	aliceTok := h.registerAndLogin(t, "alice@example.com")
	bobTok := h.registerAndLogin(t, "bob@example.com")

	createScheduleHelper(t, h, aliceTok, "alice-nightly")
	createScheduleHelper(t, h, bobTok, "bob-nightly")

	req, _ := http.NewRequest("GET", h.server.URL+"/v1/scheduled-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+aliceTok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var list []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	require.Len(t, list, 1)
	require.Equal(t, "alice-nightly", list[0]["name"])
}

func TestListScheduledJobs_AdminSeesAll(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()

	aliceTok := h.registerAndLogin(t, "alice@example.com")
	adminTok := h.registerAdminAndLogin(t, "admin@example.com")
	createScheduleHelper(t, h, aliceTok, "alice-nightly")

	req, _ := http.NewRequest("GET", h.server.URL+"/v1/scheduled-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var list []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	require.GreaterOrEqual(t, len(list), 1)
}

func TestGetScheduledJob_NotOwner_Returns404(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()
	aliceTok := h.registerAndLogin(t, "alice@example.com")
	bobTok := h.registerAndLogin(t, "bob@example.com")
	id := createScheduleHelper(t, h, aliceTok, "alice-nightly")

	req, _ := http.NewRequest("GET", h.server.URL+"/v1/scheduled-jobs/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+bobTok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPatchScheduledJob_UpdatesCronExprAndRecomputesNextRun(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()
	tok := h.registerAndLogin(t, "alice@example.com")
	id := createScheduleHelper(t, h, tok, "nightly")

	body := `{"cron_expr":"@hourly"}`
	req, _ := http.NewRequest("PATCH", h.server.URL+"/v1/scheduled-jobs/"+id, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, "@hourly", got["cron_expr"])
	require.NotEmpty(t, got["next_run_at"])
}

func TestDeleteScheduledJob(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()
	tok := h.registerAndLogin(t, "alice@example.com")
	id := createScheduleHelper(t, h, tok, "nightly")

	req, _ := http.NewRequest("DELETE", h.server.URL+"/v1/scheduled-jobs/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify gone.
	req2, _ := http.NewRequest("GET", h.server.URL+"/v1/scheduled-jobs/"+id, nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

// createScheduleHelper POSTs a minimal valid schedule and returns its id.
func createScheduleHelper(t *testing.T, h *integrationHarness, token, name string) string {
	t.Helper()
	body := `{
        "name":"` + name + `",
        "cron_expr":"0 2 * * *",
        "job_spec":{"name":"r","tasks":[{"name":"t","command":["echo","hi"]}]}
    }`
	req, _ := http.NewRequest("POST", h.server.URL+"/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	return got["id"].(string)
}
```

Note: `registerAdminAndLogin` — if no exact helper exists, use whatever pattern the existing tests use to create an admin (e.g. set `is_admin = true` directly via `q.SetAdmin` or similar). Grep existing `*_test.go` for admin test setup.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListScheduledJobs -v -timeout 120s`
Run: `go test -tags integration -p 1 ./internal/api/... -run TestGetScheduledJob -v -timeout 120s`
Run: `go test -tags integration -p 1 ./internal/api/... -run TestPatchScheduledJob -v -timeout 120s`
Run: `go test -tags integration -p 1 ./internal/api/... -run TestDeleteScheduledJob -v -timeout 120s`
Expected: FAIL (routes not registered / handlers not implemented).

- [ ] **Step 3: Add the handlers**

Append to `internal/api/scheduled_jobs.go`:

```go
func (s *Server) handleListScheduledJobs(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var rows []store.ScheduledJob
	var err error
	if u.IsAdmin {
		rows, err = s.q.ListScheduledJobs(r.Context())
	} else {
		rows, err = s.q.ListScheduledJobsByOwner(r.Context(), u.ID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
		return
	}
	out := make([]scheduledJobResponse, len(rows))
	for i, row := range rows {
		out[i] = toScheduledJobResponse(row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetScheduledJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	row, ok := s.ownedScheduledJob(w, r, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toScheduledJobResponse(row))
}

type patchScheduledJobRequest struct {
	Name          *string          `json:"name"`
	CronExpr      *string          `json:"cron_expr"`
	Timezone      *string          `json:"timezone"`
	OverlapPolicy *string          `json:"overlap_policy"`
	Enabled       *bool            `json:"enabled"`
	JobSpec       *json.RawMessage `json:"job_spec"`
}

func (s *Server) handlePatchScheduledJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	row, ok := s.ownedScheduledJob(w, r, id)
	if !ok {
		return
	}

	var req patchScheduledJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Apply overlays.
	name := row.Name
	if req.Name != nil {
		name = *req.Name
	}
	cronExpr := row.CronExpr
	if req.CronExpr != nil {
		cronExpr = *req.CronExpr
	}
	tz := row.Timezone
	if req.Timezone != nil {
		tz = *req.Timezone
	}
	overlap := row.OverlapPolicy
	if req.OverlapPolicy != nil {
		overlap = *req.OverlapPolicy
		if overlap != "skip" && overlap != "allow" {
			writeError(w, http.StatusBadRequest, "overlap_policy must be 'skip' or 'allow'")
			return
		}
	}
	enabled := row.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	jobSpecJSON := row.JobSpec
	if req.JobSpec != nil {
		var spec JobSpec
		if err := json.Unmarshal(*req.JobSpec, &spec); err != nil {
			writeError(w, http.StatusBadRequest, "invalid job_spec JSON")
			return
		}
		if err := ValidateJobSpec(spec); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		jobSpecJSON = []byte(*req.JobSpec)
	}

	// Recompute next_run_at whenever cron/tz/enabled changes.
	nextRunAt := row.NextRunAt
	if req.CronExpr != nil || req.Timezone != nil || (req.Enabled != nil && *req.Enabled && !row.Enabled) {
		if err := schedrunner.ValidateMinInterval(cronExpr, tz, minScheduleInterval); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sched, err := schedrunner.ParseSchedule(cronExpr, tz)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		nextRunAt = pgtype.Timestamptz{Time: sched.Next(time.Now()), Valid: true}
	}

	updated, err := s.q.UpdateScheduledJob(r.Context(), store.UpdateScheduledJobParams{
		ID:            id,
		Name:          name,
		CronExpr:      cronExpr,
		Timezone:      tz,
		JobSpec:       jobSpecJSON,
		OverlapPolicy: overlap,
		Enabled:       enabled,
		NextRunAt:     nextRunAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, toScheduledJobResponse(updated))
}

func (s *Server) handleDeleteScheduledJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, ok := s.ownedScheduledJob(w, r, id); !ok {
		return
	}
	n, err := s.q.DeleteScheduledJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "scheduled job not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Register the routes**

Modify `internal/api/server.go` — alongside the POST route added in Task 6:

```go
mux.Handle("GET /v1/scheduled-jobs", auth(http.HandlerFunc(s.handleListScheduledJobs)))
mux.Handle("GET /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handleGetScheduledJob)))
mux.Handle("PATCH /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handlePatchScheduledJob)))
mux.Handle("DELETE /v1/scheduled-jobs/{id}", auth(http.HandlerFunc(s.handleDeleteScheduledJob)))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListScheduledJobs -v -timeout 120s`
Run: `go test -tags integration -p 1 ./internal/api/... -run TestGetScheduledJob -v -timeout 120s`
Run: `go test -tags integration -p 1 ./internal/api/... -run TestPatchScheduledJob -v -timeout 120s`
Run: `go test -tags integration -p 1 ./internal/api/... -run TestDeleteScheduledJob -v -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/scheduled_jobs.go internal/api/scheduled_jobs_test.go internal/api/server.go
git commit -m "feat(api): list/get/patch/delete scheduled jobs"
```

---

## Task 8: HTTP handler — POST /v1/scheduled-jobs/{id}/run-now

**Files:**
- Modify: `internal/api/scheduled_jobs.go`
- Modify: `internal/api/scheduled_jobs_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write failing test**

Append to `internal/api/scheduled_jobs_test.go`:

```go
func TestRunScheduledJobNow_CreatesJob(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()
	tok := h.registerAndLogin(t, "alice@example.com")
	id := createScheduleHelper(t, h, tok, "ondemand")

	req, _ := http.NewRequest("POST", h.server.URL+"/v1/scheduled-jobs/"+id+"/run-now", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var job map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&job))
	require.NotEmpty(t, job["id"])
	require.Equal(t, "r", job["name"]) // job_spec name was "r"
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRunScheduledJobNow -v -timeout 120s`
Expected: FAIL (404).

- [ ] **Step 3: Implement the handler**

Append to `internal/api/scheduled_jobs.go`:

```go
func (s *Server) handleRunScheduledJobNow(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	row, ok := s.ownedScheduledJob(w, r, id)
	if !ok {
		return
	}

	var spec JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		writeError(w, http.StatusInternalServerError, "stored job_spec is invalid")
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx failed")
		return
	}
	defer tx.Rollback(ctx)

	// run-now uses the schedule's owner as submitted_by; that preserves
	// ownership semantics for audit. An admin triggering run-now doesn't
	// become the job submitter.
	job, tasks, err := CreateJobFromSpec(ctx, s.q.WithTx(tx), spec, row.OwnerID, row.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create job failed: "+err.Error())
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit failed")
		return
	}

	taskDeps := make(map[pgtype.UUID][]string, len(spec.Tasks))
	for i, ts := range spec.Tasks {
		taskDeps[tasks[i].ID] = ts.DependsOn
	}
	writeJSON(w, http.StatusCreated, toJobResponse(job, "", tasks, taskDeps))
}
```

- [ ] **Step 4: Register the route**

Modify `internal/api/server.go`:

```go
mux.Handle("POST /v1/scheduled-jobs/{id}/run-now", auth(http.HandlerFunc(s.handleRunScheduledJobNow)))
```

- [ ] **Step 5: Run the test and verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRunScheduledJobNow -v -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/scheduled_jobs.go internal/api/scheduled_jobs_test.go internal/api/server.go
git commit -m "feat(api): run-now endpoint for scheduled jobs"
```

---

## Task 9: Filter jobs list by scheduled_job_id

**Files:**
- Modify: `internal/api/jobs.go`
- Modify: `internal/api/jobs_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/api/jobs_test.go` (or the integration variant):

```go
//go:build integration

// TestListJobs_FilterByScheduledJobID creates a schedule, fires it once,
// then verifies GET /v1/jobs?scheduled_job_id=<id> returns only that job.
func TestListJobs_FilterByScheduledJobID(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.cleanup()
	tok := h.registerAndLogin(t, "alice@example.com")
	schedID := createScheduleHelper(t, h, tok, "nightly")

	// run-now to generate one job attached to the schedule
	req, _ := http.NewRequest("POST", h.server.URL+"/v1/scheduled-jobs/"+schedID+"/run-now", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	req2, _ := http.NewRequest("GET", h.server.URL+"/v1/jobs?scheduled_job_id="+schedID, nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var list []map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&list))
	require.Len(t, list, 1)
}
```

- [ ] **Step 2: Run the test — FAIL**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListJobs_FilterByScheduledJobID -v -timeout 120s`
Expected: FAIL (filter not honoured — returns all user's jobs).

- [ ] **Step 3: Extend handleListJobs**

Modify `internal/api/jobs.go` — at the top of `handleListJobs`, before the existing `status` handling, add:

```go
if schedIDStr := r.URL.Query().Get("scheduled_job_id"); schedIDStr != "" {
    schedID, err := parseUUID(schedIDStr)
    if err != nil {
        writeError(w, http.StatusBadRequest, "invalid scheduled_job_id")
        return
    }
    rows, err := s.q.ListJobsByScheduledJob(ctx, schedID)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "list jobs failed")
        return
    }
    resp := make([]jobResponse, len(rows))
    for i, r := range rows {
        job := store.Job{ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status, SubmittedBy: r.SubmittedBy, Labels: r.Labels, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
        resp[i] = toJobResponse(job, r.SubmittedByEmail, nil, nil)
    }
    writeJSON(w, http.StatusOK, resp)
    return
}
```

- [ ] **Step 4: Run the test — PASS**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListJobs_FilterByScheduledJobID -v -timeout 120s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/jobs.go internal/api/jobs_test.go
git commit -m "feat(api): GET /v1/jobs?scheduled_job_id= filter"
```

---

## Task 10: schedrunner.Runner — loop and fireOne

**Files:**
- Create: `internal/schedrunner/runner.go`
- Create: `internal/schedrunner/runner_test.go`

- [ ] **Step 1: Write failing integration test for the loop**

Create `internal/schedrunner/runner_test.go`:

```go
//go:build integration

package schedrunner_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// These tests reuse the existing store integration harness (Postgres via
// testcontainers). Look at internal/store/*_test.go for the pattern. The
// helper newStoreHarness(t) returns a *pgxpool.Pool and *store.Queries
// bound to a fresh database with migrations applied.

func TestRunner_FiresEligibleSchedule(t *testing.T) {
	h := newStoreHarness(t) // existing test helper in internal/store
	defer h.cleanup()

	ctx := context.Background()
	userID := h.createUser(t, "alice@example.com")
	specJSON, _ := json.Marshal(map[string]any{
		"name": "r",
		"tasks": []map[string]any{{"name": "t", "command": []string{"echo", "hi"}}},
	})

	// Insert a schedule whose next_run_at is in the past.
	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly",
		OwnerID:       userID,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       specJSON,
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	runner := schedrunner.NewRunner(h.pool, h.q)
	require.NoError(t, runner.TickOnce(ctx))

	// One job should have been created, attached to the schedule.
	jobs, err := h.q.ListJobsByScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	// next_run_at should have advanced into the future.
	row, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.True(t, row.NextRunAt.Time.After(time.Now()))
	require.True(t, row.LastRunAt.Valid)
	require.True(t, row.LastJobID.Valid)
}

func TestRunner_OverlapSkip(t *testing.T) {
	h := newStoreHarness(t)
	defer h.cleanup()

	ctx := context.Background()
	userID := h.createUser(t, "alice@example.com")
	specJSON, _ := json.Marshal(map[string]any{
		"name": "r",
		"tasks": []map[string]any{{"name": "t", "command": []string{"echo", "hi"}}},
	})

	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly",
		OwnerID:       userID,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       specJSON,
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// Pre-create a pending job attached to the schedule.
	_, err = h.q.CreateJob(ctx, store.CreateJobParams{
		Name:           "r",
		Priority:       "normal",
		SubmittedBy:    userID,
		Labels:         []byte(`{}`),
		ScheduledJobID: sj.ID,
	})
	require.NoError(t, err)

	runner := schedrunner.NewRunner(h.pool, h.q)
	require.NoError(t, runner.TickOnce(ctx))

	// Still only 1 job (the pre-existing one). next_run_at still advanced.
	jobs, err := h.q.ListJobsByScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	row, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.True(t, row.NextRunAt.Time.After(time.Now()))
	// last_job_id should NOT have been overwritten since we skipped.
	require.False(t, row.LastJobID.Valid)
}
```

Note: if `newStoreHarness` doesn't exist verbatim, mimic the integration pattern used in `internal/store/store_test.go`. What matters is a real Postgres, a real `*pgxpool.Pool`, and a `*store.Queries` bound to it.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner -v -timeout 180s`
Expected: FAIL (undefined schedrunner.NewRunner / Runner.TickOnce).

- [ ] **Step 3: Implement runner.go**

Create `internal/schedrunner/runner.go`:

```go
package schedrunner

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TickInterval is how often the runner polls for eligible schedules.
// 10 seconds is well below cron's minute granularity.
const TickInterval = 10 * time.Second

// BatchLimit caps rows scanned per tick.
const BatchLimit = 100

// Runner owns the scheduled-job polling loop.
type Runner struct {
	pool *pgxpool.Pool
	q    *store.Queries
}

func NewRunner(pool *pgxpool.Pool, q *store.Queries) *Runner {
	return &Runner{pool: pool, q: q}
}

// Run blocks until ctx is cancelled, ticking at TickInterval.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.TickOnce(ctx); err != nil {
				log.Printf("schedrunner tick: %v", err)
			}
		}
	}
}

// TickOnce performs one poll-and-fire cycle. Exposed for testing.
func (r *Runner) TickOnce(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := r.q.WithTx(tx)

	rows, err := q.ListEligibleScheduledJobs(ctx, BatchLimit)
	if err != nil {
		return err
	}
	for _, row := range rows {
		r.fireOne(ctx, q, row)
	}
	return tx.Commit(ctx)
}

func (r *Runner) fireOne(ctx context.Context, q *store.Queries, row store.ScheduledJob) {
	// Parse spec and compute next run BEFORE firing, so parse errors don't
	// wedge the schedule.
	var spec api.JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		log.Printf("schedrunner: schedule %s has invalid job_spec: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, time.Now().Add(time.Minute))
		return
	}
	sched, err := ParseSchedule(row.CronExpr, row.Timezone)
	if err != nil {
		log.Printf("schedrunner: schedule %s failed to parse: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, time.Now().Add(time.Minute))
		return
	}
	nextFire := sched.Next(time.Now())

	// Overlap policy.
	if row.OverlapPolicy == "skip" {
		active, err := q.CountActiveJobsForSchedule(ctx, row.ID)
		if err != nil {
			log.Printf("schedrunner: CountActiveJobsForSchedule: %v", err)
			return
		}
		if active > 0 {
			log.Printf("schedrunner: skipping schedule %s (previous run still active)", row.Name)
			r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
			return
		}
	}

	job, _, err := api.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)
	if err != nil {
		log.Printf("schedrunner: CreateJobFromSpec failed for %s: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
		return
	}
	r.advance(ctx, q, row, job.ID, nextFire)
}

func (r *Runner) advance(ctx context.Context, q *store.Queries, row store.ScheduledJob, newJobID pgtype.UUID, next time.Time) {
	params := store.AdvanceScheduledJobParams{
		ID:          row.ID,
		NextRunAt:   pgtype.Timestamptz{Time: next, Valid: true},
		LastJobID:   newJobID, // COALESCE in SQL preserves the old value if this is invalid
	}
	if err := q.AdvanceScheduledJob(ctx, params); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJob for %s: %v", row.Name, err)
	}
}
```

**Note on import cycle:** `internal/schedrunner` imports `internal/api` for `JobSpec`, `ValidateJobSpec`, and `CreateJobFromSpec`. `internal/api` must NOT import `internal/schedrunner` (and does not). If a cycle ever appears, extract the shared types into a new `internal/jobspec` package.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner -v -timeout 180s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/schedrunner/runner.go internal/schedrunner/runner_test.go
git commit -m "feat(schedrunner): polling loop and fireOne with overlap-skip"
```

---

## Task 11: Startup reconciliation (never-catch-up)

**Files:**
- Modify: `internal/schedrunner/runner.go`
- Modify: `internal/schedrunner/runner_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/schedrunner/runner_test.go`:

```go
func TestRunner_ReconcileOnStartup_AdvancesPastMissedTriggers(t *testing.T) {
	h := newStoreHarness(t)
	defer h.cleanup()

	ctx := context.Background()
	userID := h.createUser(t, "alice@example.com")
	specJSON, _ := json.Marshal(map[string]any{
		"name": "r",
		"tasks": []map[string]any{{"name": "t", "command": []string{"echo", "hi"}}},
	})

	// Simulate a schedule whose last computed next_run_at is 25h ago.
	oldNext := time.Now().Add(-25 * time.Hour)
	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly",
		OwnerID:       userID,
		CronExpr:      "0 2 * * *", // 02:00 UTC daily
		Timezone:      "UTC",
		JobSpec:       specJSON,
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: oldNext, Valid: true},
	})
	require.NoError(t, err)

	require.NoError(t, schedrunner.ReconcileOnStartup(ctx, h.q))

	row, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.True(t, row.NextRunAt.Time.After(time.Now()), "next_run_at should be in the future")
}
```

- [ ] **Step 2: Run the test — FAIL**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner_ReconcileOnStartup -v -timeout 120s`
Expected: FAIL (undefined schedrunner.ReconcileOnStartup).

- [ ] **Step 3: Implement ReconcileOnStartup**

Append to `internal/schedrunner/runner.go`:

```go
// ReconcileOnStartup advances next_run_at past any missed triggers for every
// enabled schedule, implementing the never-catch-up policy. Invoke after
// migrations but before Runner.Run() starts.
func ReconcileOnStartup(ctx context.Context, q *store.Queries) error {
	rows, err := q.ListOverdueScheduledJobsForCatchup(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, row := range rows {
		sched, err := ParseSchedule(row.CronExpr, row.Timezone)
		if err != nil {
			log.Printf("schedrunner: reconcile skip for %s: %v", row.Name, err)
			continue
		}
		next := sched.Next(now)
		if err := q.AdvanceScheduledJob(ctx, store.AdvanceScheduledJobParams{
			ID:        row.ID,
			NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
			LastJobID: pgtype.UUID{}, // unchanged via COALESCE
		}); err != nil {
			log.Printf("schedrunner: reconcile advance for %s: %v", row.Name, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the test — PASS**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner_ReconcileOnStartup -v -timeout 120s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/schedrunner/runner.go internal/schedrunner/runner_test.go
git commit -m "feat(schedrunner): startup reconciliation advances past missed triggers"
```

---

## Task 12: Wire runner into relay-server main

**Files:**
- Modify: `cmd/relay-server/main.go`

- [ ] **Step 1: Add imports and wiring**

Modify `cmd/relay-server/main.go` — add import:

```go
"relay/internal/schedrunner"
```

Add calls after `dispatcher.Run(ctx)` goroutine (around line 154), BEFORE the enrollment janitor goroutine:

```go
// Advance next_run_at past any triggers missed during downtime.
if err := schedrunner.ReconcileOnStartup(ctx, q); err != nil {
    log.Printf("warn: schedrunner reconcile: %v", err)
}

// Start the scheduled-jobs runner.
scheduledRunner := schedrunner.NewRunner(pool, q)
go scheduledRunner.Run(ctx)
```

- [ ] **Step 2: Build and smoke-check**

Run: `go build ./...`
Expected: success.

Run: `make test`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/main.go
git commit -m "feat(server): start schedrunner + run startup reconciliation"
```

---

## Task 13: CLI — `relay schedules` command group

**Files:**
- Create: `internal/cli/schedules.go`
- Create: `internal/cli/schedules_test.go`
- Modify: `cmd/relay/main.go`

- [ ] **Step 1: Write failing tests**

Create `internal/cli/schedules_test.go`. Mirror existing patterns in `internal/cli/reservations_test.go` and `internal/cli/jobs_test.go` (HTTP server stubs + `doSchedules*` functions). Focus on:

```go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchedulesList_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/scheduled-jobs", r.URL.Path)
		require.Equal(t, "Bearer tkn", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":"abc","name":"n","cron_expr":"@hourly","timezone":"UTC","enabled":true,"next_run_at":"2026-04-22T00:00:00Z"}]`)
	}))
	defer server.Close()
	cfg := &Config{URL: server.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"list"}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "abc")
	require.Contains(t, buf.String(), "n")
}

func TestSchedulesCreate_Success(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/scheduled-jobs", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"abc","name":"nightly","cron_expr":"@hourly"}`)
	}))
	defer server.Close()
	cfg := &Config{URL: server.URL, Token: "tkn"}

	// Write a temp spec file.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	spec := `{"name":"r","tasks":[{"name":"t","command":["echo","hi"]}]}`
	require.NoError(t, os.WriteFile(specPath, []byte(spec), 0600))

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg,
		[]string{"create", "--name", "nightly", "--cron", "@hourly", "--spec", specPath},
		&buf)
	require.NoError(t, err)
	require.Equal(t, "nightly", receivedBody["name"])
	require.Equal(t, "@hourly", receivedBody["cron_expr"])
	require.Contains(t, buf.String(), "abc")
}

func TestSchedulesDelete_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	cfg := &Config{URL: server.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"delete", "abc"}, &buf)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(buf.String()), "deleted")
}

func TestSchedulesRunNow_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc/run-now", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"jobxyz","name":"r","status":"pending"}`)
	}))
	defer server.Close()
	cfg := &Config{URL: server.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"run-now", "abc"}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "jobxyz")
}

func TestSchedulesUnknownSubcommand(t *testing.T) {
	cfg := &Config{URL: "http://x", Token: "t"}
	err := doSchedules(context.Background(), cfg, []string{"bogus"}, io.Discard)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/... -run TestSchedules -v -timeout 60s`
Expected: FAIL (undefined doSchedules).

- [ ] **Step 3: Implement schedules.go**

Create `internal/cli/schedules.go`:

```go
// internal/cli/schedules.go
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"
)

type scheduleResp struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CronExpr  string     `json:"cron_expr"`
	Timezone  string     `json:"timezone"`
	Enabled   bool       `json:"enabled"`
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
}

// SchedulesCommand returns the relay schedules Command.
// Subcommands: list, create, show, update, delete, run-now
func SchedulesCommand() Command {
	return Command{
		Name:  "schedules",
		Usage: "schedules <list|create|show|update|delete|run-now> [args]",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doSchedules(ctx, cfg, args, os.Stdout)
		},
	}
}

func doSchedules(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules <list|create|show|update|delete|run-now>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()
	switch args[0] {
	case "list":
		return doSchedulesList(ctx, c, args[1:], w)
	case "create":
		return doSchedulesCreate(ctx, c, args[1:], w)
	case "show":
		return doSchedulesShow(ctx, c, args[1:], w)
	case "update":
		return doSchedulesUpdate(ctx, c, args[1:], w)
	case "delete":
		return doSchedulesDelete(ctx, c, args[1:], w)
	case "run-now":
		return doSchedulesRunNow(ctx, c, args[1:], w)
	default:
		return fmt.Errorf("unknown schedules subcommand: %s", args[0])
	}
}

func doSchedulesList(ctx context.Context, c *Client, args []string, w io.Writer) error {
	var out []scheduleResp
	if err := c.do(ctx, "GET", "/v1/scheduled-jobs", nil, &out); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tCRON\tTZ\tENABLED\tNEXT")
	for _, s := range out {
		next := ""
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\n", s.ID, s.Name, s.CronExpr, s.Timezone, s.Enabled, next)
	}
	return tw.Flush()
}

func doSchedulesCreate(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	name := fs.String("name", "", "schedule name (required)")
	cron := fs.String("cron", "", "cron expression (required)")
	tz := fs.String("tz", "UTC", "IANA timezone")
	overlap := fs.String("overlap", "skip", "overlap policy: skip|allow")
	specFile := fs.String("spec", "", "path to job spec JSON (required)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if *name == "" || *cron == "" || *specFile == "" {
		return fmt.Errorf("usage: relay schedules create --name NAME --cron EXPR --spec FILE [--tz ZONE] [--overlap skip|allow]")
	}

	data, err := os.ReadFile(*specFile)
	if err != nil {
		return fmt.Errorf("read spec file: %w", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("invalid spec JSON: %w", err)
	}

	body := map[string]any{
		"name":           *name,
		"cron_expr":      *cron,
		"timezone":       *tz,
		"overlap_policy": *overlap,
		"job_spec":       spec,
	}

	var out scheduleResp
	if err := c.do(ctx, "POST", "/v1/scheduled-jobs", body, &out); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s created: %s\n", out.ID, out.Name)
	return nil
}

func doSchedulesShow(ctx context.Context, c *Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules show <id>")
	}
	var out scheduleResp
	if err := c.do(ctx, "GET", "/v1/scheduled-jobs/"+args[0], nil, &out); err != nil {
		return err
	}
	fmt.Fprintf(w, "ID:       %s\n", out.ID)
	fmt.Fprintf(w, "Name:     %s\n", out.Name)
	fmt.Fprintf(w, "Cron:     %s\n", out.CronExpr)
	fmt.Fprintf(w, "Timezone: %s\n", out.Timezone)
	fmt.Fprintf(w, "Enabled:  %t\n", out.Enabled)
	if out.NextRunAt != nil {
		fmt.Fprintf(w, "Next:     %s\n", out.NextRunAt.Format(time.RFC3339))
	}
	return nil
}

func doSchedulesUpdate(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	cron := fs.String("cron", "", "new cron expression")
	tz := fs.String("tz", "", "new IANA timezone")
	enable := fs.Bool("enable", false, "enable the schedule")
	disable := fs.Bool("disable", false, "disable the schedule")
	overlap := fs.String("overlap", "", "new overlap policy: skip|allow")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: relay schedules update <id> [--cron EXPR] [--tz ZONE] [--enable|--disable] [--overlap ...]")
	}
	id := fs.Arg(0)

	body := map[string]any{}
	if *cron != "" {
		body["cron_expr"] = *cron
	}
	if *tz != "" {
		body["timezone"] = *tz
	}
	if *overlap != "" {
		body["overlap_policy"] = *overlap
	}
	if *enable {
		body["enabled"] = true
	}
	if *disable {
		body["enabled"] = false
	}
	if len(body) == 0 {
		return fmt.Errorf("no changes specified")
	}

	var out scheduleResp
	if err := c.do(ctx, "PATCH", "/v1/scheduled-jobs/"+id, body, &out); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s updated.\n", out.ID)
	return nil
}

func doSchedulesDelete(ctx context.Context, c *Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules delete <id>")
	}
	if err := c.do(ctx, "DELETE", "/v1/scheduled-jobs/"+args[0], nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(w, "Schedule %s deleted.\n", args[0])
	return nil
}

func doSchedulesRunNow(ctx context.Context, c *Client, args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay schedules run-now <id>")
	}
	var job map[string]any
	if err := c.do(ctx, "POST", "/v1/scheduled-jobs/"+args[0]+"/run-now", nil, &job); err != nil {
		return err
	}
	fmt.Fprintf(w, "Job %v created for schedule %s (status: %v)\n", job["id"], args[0], job["status"])
	return nil
}
```

- [ ] **Step 4: Register the command**

Modify `cmd/relay/main.go` — add `cli.SchedulesCommand()` to the commands slice:

```go
commands := []cli.Command{
    cli.LoginCommand(),
    cli.RegisterCommand(),
    cli.PasswdCommand(),
    cli.InviteCommand(),
    cli.AgentCommand(),
    cli.SubmitCommand(),
    cli.ListCommand(),
    cli.GetCommand(),
    cli.CancelCommand(),
    cli.LogsCommand(),
    cli.WorkersCommand(),
    cli.ReservationsCommand(),
    cli.SchedulesCommand(),
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/... -run TestSchedules -v -timeout 60s`
Expected: PASS.

Run: `go build ./...`
Expected: success.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/schedules.go internal/cli/schedules_test.go cmd/relay/main.go
git commit -m "feat(cli): relay schedules subcommand"
```

---

## Task 14: Documentation

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Document the new CLI**

Modify `CLAUDE.md`. In the CLI-internals section (around the `relay workers revoke` entry), append:

````markdown
- `relay schedules create --name NAME --cron EXPR [--tz ZONE] [--overlap skip|allow] --spec FILE.json` — create a recurring schedule; owner is the calling user
- `relay schedules list` / `show <id>` / `update <id> [...]` / `delete <id>` / `run-now <id>` — manage schedules. Non-admins see only their own.
````

In the `relay-server internals` section, add a sub-bullet under the startup list:

```markdown
7. Calls `schedrunner.ReconcileOnStartup()` to advance `scheduled_jobs.next_run_at` past any missed triggers during downtime (never-catch-up policy), then starts the `schedrunner.Runner` goroutine (10s ticker) which fires eligible schedules by creating fresh `Job` rows via `api.CreateJobFromSpec`.
```

No new env vars are introduced; the scheduler loop interval is a package constant (`schedrunner.TickInterval`). If that needs tuning in the future, add a `RELAY_SCHED_TICK_INTERVAL` env var — out of scope for this plan.

- [ ] **Step 2: Full test sweep**

Run: `make test`
Expected: PASS.

Run: `make test-integration`
Expected: PASS (all integration tests, including new ones).

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document relay schedules and schedrunner startup"
```

---

## Final verification

- [ ] **Build all three binaries**

Run: `make build`
Expected: success.

- [ ] **Full unit test sweep**

Run: `make test`
Expected: PASS.

- [ ] **Full integration test sweep**

Run: `make test-integration`
Expected: PASS.

- [ ] **Manual smoke**

Start the server and exercise the CLI end-to-end:

```bash
RELAY_DATABASE_URL=postgres://relay:relay@localhost:5432/relay?sslmode=disable \
    RELAY_BOOTSTRAP_ADMIN=admin@example.com \
    RELAY_BOOTSTRAP_PASSWORD=test1234 \
    ./bin/relay-server &

./bin/relay login --url http://localhost:8080 --email admin@example.com
# echo a spec to /tmp/spec.json: {"name":"r","tasks":[{"name":"t","command":["echo","hi"]}]}
./bin/relay schedules create --name smoke --cron "@every 30s" --spec /tmp/spec.json
./bin/relay schedules list
./bin/relay schedules run-now <id-from-list>
./bin/relay list
```

Expected: a schedule is listed; run-now creates a job; after ~30s the ticker creates another job attached to the schedule.
