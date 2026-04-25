# Perforce Workspace Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a pluggable source-provider abstraction on the agent so workers can sync/unshelve a Perforce workspace before each task, reusing workspaces across jobs keyed by stream and arbitrating concurrent access on the same worker.

**Architecture:** Tasks gain an optional `source` spec (JSONB). The agent's new `internal/agent/source` package defines a `Provider` interface; the v1 Perforce implementation creates stream-bound clients, arbitrates a per-workspace RWLock with three rules (identical-baseline / disjoint-additive / serialize-otherwise), runs `p4 sync`/`p4 unshelve` under exclusive hold, streams progress, and reverts unshelves through a per-task pending changelist. Workers report a `worker_workspaces` inventory; the dispatcher's `selectWorker` adds a soft warm-preference bonus. Eviction is opt-in (age + disk-pressure) via a sweeper goroutine.

**Tech Stack:** Go 1.26, pgx/v5, sqlc, gRPC/proto3, golang-migrate, testcontainers-go, testify, `p4` CLI.

**Spec:** `docs/superpowers/specs/2026-04-24-perforce-workspace-management-design.md`

---

## File structure

**New files:**
- `internal/store/migrations/000007_workspaces.up.sql` + `.down.sql`
- `internal/store/query/worker_workspaces.sql`
- `internal/agent/source/source.go` + `source_test.go`
- `internal/agent/source/perforce/perforce.go` + `perforce_test.go`
- `internal/agent/source/perforce/client.go` + `client_test.go`
- `internal/agent/source/perforce/registry.go` + `registry_test.go`
- `internal/agent/source/perforce/baseline.go` + `baseline_test.go`
- `internal/agent/source/perforce/workspace.go` + `workspace_test.go`
- `internal/agent/source/perforce/sweeper.go` + `sweeper_test.go`
- `internal/api/workspaces.go` + `workspaces_test.go`
- `internal/cli/workers_workspaces.go` + `workers_workspaces_test.go`
- `internal/agent/source/perforce/perforce_integration_test.go` (P4 testcontainer)

**Modified files:**
- `proto/relayv1/relay.proto` — `SourceSpec`, `PerforceSource`, new enum values, `WorkspaceInventoryUpdate`, `EvictWorkspaceCommand`
- `internal/api/job_spec.go` — `Source` field on `TaskSpec`, validation, persistence
- `internal/api/jobs.go` — read/write the new column on retrieval
- `internal/store/query/tasks.sql` — `source` column in inserts and selects
- `internal/store/query/workers.sql` — N/A (workspaces are a sibling table)
- `internal/scheduler/dispatch.go` — emit `task.Source` on `DispatchTask`; warm-preference scoring in `selectWorker`
- `internal/worker/handler.go` — handle `WorkspaceInventoryUpdate`; reconcile inventory on register
- `internal/agent/agent.go` — invoke Provider in `handleDispatch`, emit `PREPARING`/`PREPARE_FAILED`, send inventory on register, send updates on workspace change, listen for `EvictWorkspaceCommand`
- `internal/agent/runner.go` — set `cmd.Dir`, merge env from Handle
- `internal/cli/cli.go` — register new subcommands
- `cmd/relay-agent/main.go` — wire `RELAY_WORKSPACE_ROOT` and sweeper config
- `CLAUDE.md` — document new env vars and endpoints

---

## Phase 1 — Foundation: schema, proto, JobSpec

### Task 1: Add `worker_workspaces` table, `source` column, status enum values

**Files:**
- Create: `internal/store/migrations/000007_workspaces.up.sql`
- Create: `internal/store/migrations/000007_workspaces.down.sql`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/store/store_test.go`:

```go
func TestWorkerWorkspacesAndSourceColumn(t *testing.T) {
    q := newTestQueries(t)
    ctx := context.Background()

    user := makeTestUser(t, q, ctx, "Wendy", "w@example.com")
    job, err := q.CreateJob(ctx, store.CreateJobParams{
        Name: "ws-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
    })
    require.NoError(t, err)

    // tasks.source must be a nullable JSONB column
    src := []byte(`{"type":"perforce","stream":"//streams/X/main"}`)
    task, err := q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
        JobID: job.ID, Name: "t", Command: []string{"true"},
        Env: []byte(`{}`), Requires: []byte(`{}`), Source: src,
    })
    require.NoError(t, err)
    require.JSONEq(t, string(src), string(task.Source))

    // status must accept new enum values
    _, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
        ID: task.ID, Status: "preparing", Epoch: 0,
    })
    require.NoError(t, err)
    _, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
        ID: task.ID, Status: "prepare_failed", Epoch: 0,
    })
    require.NoError(t, err)

    // worker_workspaces upsert + list round-trip
    worker := makeTestWorker(t, q, ctx, "render-07")
    err = q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
        WorkerID:     worker.ID,
        SourceType:   "perforce",
        SourceKey:    "//streams/X/main",
        ShortID:      "abcdef",
        BaselineHash: "deadbeefdeadbeef",
        LastUsedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
    })
    require.NoError(t, err)

    rows, err := q.ListWorkerWorkspaces(ctx, worker.ID)
    require.NoError(t, err)
    require.Len(t, rows, 1)
    require.Equal(t, "abcdef", rows[0].ShortID)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestWorkerWorkspacesAndSourceColumn -v -timeout 120s
```

Expected: FAIL — `CreateTaskWithSource`, `UpsertWorkerWorkspace`, `ListWorkerWorkspaces` undefined; status `'preparing'` rejected by enum.

- [ ] **Step 3: Write up migration**

`internal/store/migrations/000007_workspaces.up.sql`:

```sql
-- Add nullable source spec to tasks
ALTER TABLE tasks ADD COLUMN source JSONB;

-- Extend status enum for the prepare phase
ALTER TYPE task_status ADD VALUE IF NOT EXISTS 'preparing';
ALTER TYPE task_status ADD VALUE IF NOT EXISTS 'prepare_failed';

-- Worker workspace inventory
CREATE TABLE worker_workspaces (
    worker_id     UUID NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
    source_type   TEXT NOT NULL,
    source_key    TEXT NOT NULL,
    short_id      TEXT NOT NULL,
    baseline_hash TEXT NOT NULL,
    last_used_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (worker_id, source_type, source_key)
);
CREATE INDEX worker_workspaces_lookup_idx
    ON worker_workspaces (source_type, source_key, baseline_hash);
```

If `task_status` is a CHECK constraint rather than a Postgres ENUM in the existing schema, replace the two `ALTER TYPE` lines with the equivalent `ALTER TABLE tasks DROP CONSTRAINT ...; ADD CONSTRAINT ...` updating the allowed values list. Inspect `internal/store/migrations/000001_initial.up.sql` first to confirm.

- [ ] **Step 4: Write down migration**

`internal/store/migrations/000007_workspaces.down.sql`:

```sql
DROP TABLE IF EXISTS worker_workspaces;
ALTER TABLE tasks DROP COLUMN IF EXISTS source;
-- Postgres ENUM values cannot be dropped; if status uses an ENUM type, the
-- 'preparing' / 'prepare_failed' values remain. They are harmless without rows
-- using them.
```

- [ ] **Step 5: Add sqlc queries**

Create `internal/store/query/worker_workspaces.sql`:

```sql
-- name: UpsertWorkerWorkspace :exec
INSERT INTO worker_workspaces (worker_id, source_type, source_key, short_id, baseline_hash, last_used_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (worker_id, source_type, source_key) DO UPDATE
SET short_id = EXCLUDED.short_id,
    baseline_hash = EXCLUDED.baseline_hash,
    last_used_at = EXCLUDED.last_used_at;

-- name: DeleteWorkerWorkspace :exec
DELETE FROM worker_workspaces
WHERE worker_id = $1 AND source_type = $2 AND source_key = $3;

-- name: ListWorkerWorkspaces :many
SELECT * FROM worker_workspaces
WHERE worker_id = $1
ORDER BY source_key;

-- name: GetWorkerWorkspace :one
SELECT * FROM worker_workspaces
WHERE worker_id = $1 AND source_type = $2 AND source_key = $3;

-- name: ListWarmWorkspacesForKeys :many
-- Used by dispatcher's warm-preference scoring. $1 is source_type, $2 is an
-- array of source_keys observed in the current eligible-task batch.
SELECT * FROM worker_workspaces
WHERE source_type = $1 AND source_key = ANY($2::text[]);

-- name: ReplaceWorkerInventory :exec
-- On agent reconnect: delete all existing rows for this worker and reinsert.
-- Caller wraps in a transaction with subsequent UpsertWorkerWorkspace calls.
DELETE FROM worker_workspaces WHERE worker_id = $1;
```

In `internal/store/query/tasks.sql`, add a new query that includes `source`:

```sql
-- name: CreateTaskWithSource :one
INSERT INTO tasks (job_id, name, command, env, requires, timeout_seconds, retries, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;
```

Leave the existing `CreateTask` in place; the API layer will choose which to call based on whether `source` is set.

- [ ] **Step 6: Regenerate**

```bash
make generate
```

Expected: `internal/store/models.go` gains a `WorkerWorkspace` struct and a `Source []byte` field on `Task`. New `*sql.go` files appear for the new queries.

- [ ] **Step 7: Run test to verify pass**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestWorkerWorkspacesAndSourceColumn -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 8: Run full test suite**

```bash
make test && make test-integration
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/store/migrations/000007_workspaces.up.sql \
        internal/store/migrations/000007_workspaces.down.sql \
        internal/store/query/worker_workspaces.sql \
        internal/store/query/tasks.sql \
        internal/store/store_test.go \
        internal/store/*.sql.go internal/store/models.go
git commit -m "feat(store): add tasks.source column and worker_workspaces table"
```

---

### Task 2: Add SourceSpec, PerforceSource, new enum values, WorkspaceInventoryUpdate, EvictWorkspaceCommand to proto

**Files:**
- Modify: `proto/relayv1/relay.proto`
- Test: `internal/proto/proto_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/proto/proto_test.go`:

```go
func TestSourceSpecAndInventoryMessages(t *testing.T) {
    // Roundtrip: serialize a DispatchTask with a PerforceSource, deserialize, compare.
    src := &relayv1.SourceSpec{
        Provider: &relayv1.SourceSpec_Perforce{
            Perforce: &relayv1.PerforceSource{
                Stream: "//streams/X/main",
                Sync: []*relayv1.SyncEntry{
                    {Path: "//streams/X/main/...", Rev: "#head"},
                },
                Unshelves:          []int64{12346},
                WorkspaceExclusive: true,
            },
        },
    }
    task := &relayv1.DispatchTask{TaskId: "t1", JobId: "j1", Source: src}
    b, err := proto.Marshal(task)
    require.NoError(t, err)
    var got relayv1.DispatchTask
    require.NoError(t, proto.Unmarshal(b, &got))
    require.Equal(t, "//streams/X/main", got.Source.GetPerforce().Stream)
    require.True(t, got.Source.GetPerforce().WorkspaceExclusive)

    // New TaskStatus values exist
    _ = relayv1.TaskStatus_TASK_STATUS_PREPARING
    _ = relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED

    // New LogStream value exists
    _ = relayv1.LogStream_LOG_STREAM_PREPARE

    // AgentMessage carries WorkspaceInventoryUpdate
    inv := &relayv1.WorkspaceInventoryUpdate{
        SourceType: "perforce", SourceKey: "//streams/X/main",
        ShortId: "abcdef", BaselineHash: "deadbeef",
    }
    msg := &relayv1.AgentMessage{Payload: &relayv1.AgentMessage_WorkspaceInventory{WorkspaceInventory: inv}}
    b, err = proto.Marshal(msg)
    require.NoError(t, err)
    var gotMsg relayv1.AgentMessage
    require.NoError(t, proto.Unmarshal(b, &gotMsg))
    require.Equal(t, "abcdef", gotMsg.GetWorkspaceInventory().ShortId)

    // CoordinatorMessage carries EvictWorkspaceCommand
    cmd := &relayv1.EvictWorkspaceCommand{SourceType: "perforce", ShortId: "abcdef"}
    cm := &relayv1.CoordinatorMessage{Payload: &relayv1.CoordinatorMessage_EvictWorkspace{EvictWorkspace: cmd}}
    b, err = proto.Marshal(cm)
    require.NoError(t, err)
    var gotCm relayv1.CoordinatorMessage
    require.NoError(t, proto.Unmarshal(b, &gotCm))
    require.Equal(t, "abcdef", gotCm.GetEvictWorkspace().ShortId)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/proto/... -run TestSourceSpecAndInventoryMessages -v -timeout 30s
```

Expected: FAIL — undefined symbols.

- [ ] **Step 3: Edit proto**

Append to `proto/relayv1/relay.proto`:

```proto
message SourceSpec {
  oneof provider {
    PerforceSource perforce = 1;
  }
}

message PerforceSource {
  string   stream              = 1;
  repeated SyncEntry sync      = 2;
  repeated int64 unshelves     = 3;
  bool     workspace_exclusive = 4;
  optional string client_template = 5;
}

message SyncEntry {
  string path = 1;
  string rev  = 2;
}

message WorkspaceInventoryUpdate {
  string source_type   = 1;
  string source_key    = 2;
  string short_id      = 3;
  string baseline_hash = 4;
  google.protobuf.Timestamp last_used_at = 5;
  bool   deleted       = 6;
}

message EvictWorkspaceCommand {
  string source_type = 1;
  string short_id    = 2;
}
```

Modify existing messages:

```proto
// Add to DispatchTask (next free number is 7):
message DispatchTask {
  // ... existing fields 1-6 ...
  SourceSpec source = 7;
}

// Append to TaskStatus enum:
enum TaskStatus {
  // ... existing values ...
  TASK_STATUS_PREPARING       = 5;
  TASK_STATUS_PREPARE_FAILED  = 6;
}

// Append to LogStream enum:
enum LogStream {
  // ... existing values ...
  LOG_STREAM_PREPARE = 3;
}

// AgentMessage gains a payload:
message AgentMessage {
  oneof payload {
    RegisterRequest          register            = 1;
    TaskStatusUpdate         task_status         = 2;
    TaskLogChunk             task_log            = 3;
    WorkspaceInventoryUpdate workspace_inventory = 4;
  }
}

// RegisterRequest gains an inventory list (next free number = 11):
message RegisterRequest {
  // ... existing fields ...
  repeated WorkspaceInventoryUpdate inventory = 11;
}

// CoordinatorMessage gains a payload:
message CoordinatorMessage {
  oneof payload {
    RegisterResponse      register_response = 1;
    DispatchTask          dispatch_task     = 2;
    CancelTask            cancel_task       = 3;
    EvictWorkspaceCommand evict_workspace   = 4;
  }
}
```

Add at top of file:

```proto
import "google/protobuf/timestamp.proto";
```

- [ ] **Step 4: Regenerate**

```bash
make generate
```

- [ ] **Step 5: Run test to verify pass**

```bash
go test ./internal/proto/... -run TestSourceSpecAndInventoryMessages -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add proto/relayv1/relay.proto internal/proto/relayv1/*.go internal/proto/proto_test.go
git commit -m "feat(proto): add SourceSpec, PerforceSource, workspace inventory messages"
```

---

### Task 3: JobSpec validation for `source` field

**Files:**
- Modify: `internal/api/job_spec.go`
- Test: `internal/api/job_spec_test.go` (create if absent)

- [ ] **Step 1: Write failing tests**

Create or append to `internal/api/job_spec_test.go`:

```go
package api

import (
    "testing"

    "github.com/stretchr/testify/require"
)

func TestValidateJobSpec_Source_Perforce(t *testing.T) {
    valid := func() JobSpec {
        return JobSpec{
            Name: "j", Priority: "normal",
            Tasks: []TaskSpec{{
                Name: "t", Command: []string{"true"},
                Source: &SourceSpec{
                    Type:   "perforce",
                    Stream: "//streams/X/main",
                    Sync: []SyncEntry{
                        {Path: "//streams/X/main/...", Rev: "#head"},
                    },
                },
            }},
        }
    }

    cases := []struct {
        name    string
        mutate  func(*JobSpec)
        wantErr string
    }{
        {"happy path", func(s *JobSpec) {}, ""},
        {"unsupported type", func(s *JobSpec) { s.Tasks[0].Source.Type = "git" }, "unsupported source type"},
        {"missing stream", func(s *JobSpec) { s.Tasks[0].Source.Stream = "" }, "stream is required"},
        {"stream not depot path", func(s *JobSpec) { s.Tasks[0].Source.Stream = "GameX" }, "stream must start with //"},
        {"empty sync", func(s *JobSpec) { s.Tasks[0].Source.Sync = nil }, "at least one sync entry"},
        {"sync path outside stream", func(s *JobSpec) {
            s.Tasks[0].Source.Sync = []SyncEntry{{Path: "//other/depot/...", Rev: "#head"}}
        }, "must be under stream"},
        {"sync path not depot", func(s *JobSpec) {
            s.Tasks[0].Source.Sync = []SyncEntry{{Path: "relative/path", Rev: "#head"}}
        }, "must start with //"},
        {"bad rev", func(s *JobSpec) {
            s.Tasks[0].Source.Sync[0].Rev = "garbage"
        }, "invalid rev"},
        {"good rev #head", func(s *JobSpec) { s.Tasks[0].Source.Sync[0].Rev = "#head" }, ""},
        {"good rev @cl", func(s *JobSpec) { s.Tasks[0].Source.Sync[0].Rev = "@12345" }, ""},
        {"good rev @label", func(s *JobSpec) { s.Tasks[0].Source.Sync[0].Rev = "@label-stable" }, ""},
        {"good rev #N", func(s *JobSpec) { s.Tasks[0].Source.Sync[0].Rev = "#42" }, ""},
        {"negative unshelve", func(s *JobSpec) { s.Tasks[0].Source.Unshelves = []int64{-1} }, "unshelve must be positive"},
        {"bad client_template", func(s *JobSpec) {
            tmpl := "has space"
            s.Tasks[0].Source.ClientTemplate = &tmpl
        }, "invalid client_template"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            spec := valid()
            tc.mutate(&spec)
            err := ValidateJobSpec(spec)
            if tc.wantErr == "" {
                require.NoError(t, err)
            } else {
                require.ErrorContains(t, err, tc.wantErr)
            }
        })
    }
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/api/... -run TestValidateJobSpec_Source_Perforce -v -timeout 30s
```

Expected: FAIL — `SourceSpec`, `SyncEntry`, `TaskSpec.Source` undefined.

- [ ] **Step 3: Implement**

In `internal/api/job_spec.go`, add types and extend validator:

```go
type SourceSpec struct {
    Type               string      `json:"type"`
    Stream             string      `json:"stream,omitempty"`
    Sync               []SyncEntry `json:"sync,omitempty"`
    Unshelves          []int64     `json:"unshelves,omitempty"`
    WorkspaceExclusive bool        `json:"workspace_exclusive,omitempty"`
    ClientTemplate     *string     `json:"client_template,omitempty"`
}

type SyncEntry struct {
    Path string `json:"path"`
    Rev  string `json:"rev"`
}

// Add to TaskSpec:
type TaskSpec struct {
    // ... existing fields ...
    Source *SourceSpec `json:"source,omitempty"`
}

var (
    revHeadRe   = regexp.MustCompile(`^#head$`)
    revCLRe     = regexp.MustCompile(`^@\d+$`)
    revLabelRe  = regexp.MustCompile(`^@[A-Za-z0-9._-]+$`)
    revNumRe    = regexp.MustCompile(`^#\d+$`)
    clientTmplRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

func validateSourceSpec(s *SourceSpec) error {
    if s == nil {
        return nil
    }
    if s.Type != "perforce" {
        return fmt.Errorf("unsupported source type: %s", s.Type)
    }
    if s.Stream == "" {
        return errors.New("stream is required")
    }
    if !strings.HasPrefix(s.Stream, "//") {
        return errors.New("stream must start with //")
    }
    if len(s.Sync) == 0 {
        return errors.New("source.sync must have at least one sync entry")
    }
    for i, e := range s.Sync {
        if !strings.HasPrefix(e.Path, "//") {
            return fmt.Errorf("sync[%d].path must start with //", i)
        }
        if e.Path != s.Stream &&
            e.Path != s.Stream+"/..." &&
            !strings.HasPrefix(e.Path, s.Stream+"/") {
            return fmt.Errorf("sync[%d].path must be under stream %s", i, s.Stream)
        }
        if !(revHeadRe.MatchString(e.Rev) || revCLRe.MatchString(e.Rev) ||
            revLabelRe.MatchString(e.Rev) || revNumRe.MatchString(e.Rev)) {
            return fmt.Errorf("sync[%d].rev: invalid rev %q", i, e.Rev)
        }
    }
    for i, cl := range s.Unshelves {
        if cl <= 0 {
            return fmt.Errorf("unshelves[%d]: unshelve must be positive", i)
        }
    }
    if s.ClientTemplate != nil && !clientTmplRe.MatchString(*s.ClientTemplate) {
        return fmt.Errorf("invalid client_template %q", *s.ClientTemplate)
    }
    return nil
}
```

In `ValidateJobSpec`, after the existing per-task loop, add:

```go
for _, ts := range spec.Tasks {
    if err := validateSourceSpec(ts.Source); err != nil {
        return fmt.Errorf("task %s: %w", ts.Name, err)
    }
}
```

Add imports as needed: `"regexp"`, `"strings"`.

- [ ] **Step 4: Run test to verify pass**

```bash
go test ./internal/api/... -run TestValidateJobSpec_Source_Perforce -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/job_spec.go internal/api/job_spec_test.go
git commit -m "feat(api): validate JobSpec.source for Perforce provider"
```

---

### Task 4: CreateJobFromSpec persists `source`; dispatcher passes it on DispatchTask

**Files:**
- Modify: `internal/api/job_spec.go` — `CreateJobFromSpec`
- Modify: `internal/scheduler/dispatch.go` — `sendTask` reads `task.Source` and emits on `DispatchTask`
- Test: `internal/api/job_spec_test.go`, `internal/scheduler/dispatch_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/api/job_spec_test.go`:

```go
//go:build integration

func TestCreateJobFromSpec_PersistsSource(t *testing.T) {
    q := newTestQueriesAPI(t)
    ctx := context.Background()
    user := makeTestUserAPI(t, q, ctx)

    spec := JobSpec{
        Name: "j", Priority: "normal",
        Tasks: []TaskSpec{{
            Name: "t", Command: []string{"true"},
            Source: &SourceSpec{
                Type: "perforce", Stream: "//streams/X/main",
                Sync: []SyncEntry{{Path: "//streams/X/main/...", Rev: "#head"}},
            },
        }},
    }
    _, tasks, err := CreateJobFromSpec(ctx, q, spec, user.ID, pgtype.UUID{})
    require.NoError(t, err)
    require.Len(t, tasks, 1)
    require.NotNil(t, tasks[0].Source)
    require.Contains(t, string(tasks[0].Source), `"//streams/X/main"`)
}
```

Append to `internal/scheduler/dispatch_test.go`:

```go
func TestDispatcher_PassesSourceToAgent(t *testing.T) {
    ctx := context.Background()
    q := newTestQueries(t)
    user := makeTestUser(t, q, ctx, "Sue", "s@x")
    job := makeTestJob(t, q, ctx, user.ID)

    src := []byte(`{"type":"perforce","stream":"//s/x","sync":[{"path":"//s/x/...","rev":"#head"}]}`)
    task, err := q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
        JobID: job.ID, Name: "t", Command: []string{"true"},
        Env: []byte(`{}`), Requires: []byte(`{}`), Source: src,
    })
    require.NoError(t, err)
    _ = task

    reg := worker.NewRegistry()
    captured := make(chan *relayv1.DispatchTask, 1)
    senderFn := func(workerID pgtype.UUID, msg *relayv1.CoordinatorMessage) bool {
        if dt := msg.GetDispatchTask(); dt != nil {
            captured <- dt
        }
        return true
    }
    reg.SetSenderForTest(senderFn) // assume helper exists; otherwise inject via existing test pattern

    w := makeTestWorkerOnline(t, q, ctx, "wkr", reg)
    _ = w

    d := scheduler.NewDispatcher(q, reg, events.NewBroker())
    d.RunOnce(ctx)

    select {
    case dt := <-captured:
        require.NotNil(t, dt.Source)
        require.Equal(t, "//s/x", dt.Source.GetPerforce().Stream)
    case <-time.After(2 * time.Second):
        t.Fatal("no dispatch happened")
    }
}
```

(If `SetSenderForTest` does not exist, follow the pattern used in existing dispatcher tests for capturing sent messages — typically by spying on `worker.Registry`.)

- [ ] **Step 2: Run test to verify failure**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestCreateJobFromSpec_PersistsSource -v -timeout 120s
go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_PassesSourceToAgent -v -timeout 120s
```

Expected: both FAIL.

- [ ] **Step 3: Implement persistence**

In `internal/api/job_spec.go` `CreateJobFromSpec`, replace the `q.CreateTask` call with branching:

```go
var task store.Task
if ts.Source != nil {
    srcJSON, err := json.Marshal(ts.Source)
    if err != nil {
        return store.Job{}, nil, fmt.Errorf("marshal source for %s: %w", ts.Name, err)
    }
    task, err = q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
        JobID:          job.ID,
        Name:           ts.Name,
        Command:        ts.Command,
        Env:            envJSON,
        Requires:       requiresJSON,
        TimeoutSeconds: ts.TimeoutSeconds,
        Retries:        ts.Retries,
        Source:         srcJSON,
    })
    if err != nil {
        return store.Job{}, nil, fmt.Errorf("create task %s: %w", ts.Name, err)
    }
} else {
    task, err = q.CreateTask(ctx, store.CreateTaskParams{ /* existing args */ })
    if err != nil {
        return store.Job{}, nil, fmt.Errorf("create task %s: %w", ts.Name, err)
    }
}
```

- [ ] **Step 4: Implement dispatch propagation**

In `internal/scheduler/dispatch.go` `sendTask`, after building the existing dispatch:

```go
dispatch := &relayv1.DispatchTask{
    TaskId:         uuidStr(task.ID),
    JobId:          uuidStr(task.JobID),
    Command:        task.Command,
    Env:            env,
    TimeoutSeconds: timeout,
    Epoch:          newEpoch,
}

if len(task.Source) > 0 {
    var apiSpec api.SourceSpec
    if err := json.Unmarshal(task.Source, &apiSpec); err != nil {
        log.Printf("dispatch: bad source JSON on task %s: %v", task.ID, err)
        return false
    }
    dispatch.Source = sourceSpecToProto(&apiSpec)
}
```

Place `sourceSpecToProto` in a new helper file `internal/scheduler/source_proto.go` to avoid bloating `dispatch.go`:

```go
package scheduler

import (
    "relay/internal/api"
    relayv1 "relay/internal/proto/relayv1"
)

func sourceSpecToProto(s *api.SourceSpec) *relayv1.SourceSpec {
    if s == nil || s.Type != "perforce" {
        return nil
    }
    p := &relayv1.PerforceSource{
        Stream:             s.Stream,
        Unshelves:          s.Unshelves,
        WorkspaceExclusive: s.WorkspaceExclusive,
    }
    for _, e := range s.Sync {
        p.Sync = append(p.Sync, &relayv1.SyncEntry{Path: e.Path, Rev: e.Rev})
    }
    if s.ClientTemplate != nil {
        ct := *s.ClientTemplate
        p.ClientTemplate = &ct
    }
    return &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: p}}
}
```

If `internal/scheduler` cannot import `internal/api` (cycle check), instead place the helper in a new `internal/source` package that both `api` and `scheduler` import.

- [ ] **Step 5: Run tests to verify pass**

```bash
go test -tags integration -p 1 ./internal/api/... ./internal/scheduler/... -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/job_spec.go internal/scheduler/dispatch.go internal/scheduler/source_proto.go internal/api/job_spec_test.go internal/scheduler/dispatch_test.go
git commit -m "feat(api,scheduler): persist task source and propagate to agent"
```

---

## Phase 2 — Agent source-provider scaffold

### Task 5: Provider/Handle interfaces and registry

**Files:**
- Create: `internal/agent/source/source.go`
- Create: `internal/agent/source/source_test.go`

- [ ] **Step 1: Write failing test**

`internal/agent/source/source_test.go`:

```go
package source_test

import (
    "context"
    "errors"
    "testing"

    "github.com/stretchr/testify/require"

    "relay/internal/agent/source"
    relayv1 "relay/internal/proto/relayv1"
)

type fakeProvider struct{ typ string }

func (f *fakeProvider) Type() string { return f.typ }
func (f *fakeProvider) Prepare(ctx context.Context, taskID string, spec *relayv1.SourceSpec, progress func(string)) (source.Handle, error) {
    return nil, errors.New("nope")
}

func TestRegistry_RegisterAndGet(t *testing.T) {
    reg := source.NewRegistry()
    reg.Register("perforce", func() source.Provider { return &fakeProvider{typ: "perforce"} })

    p, err := reg.Get("perforce")
    require.NoError(t, err)
    require.Equal(t, "perforce", p.Type())

    _, err = reg.Get("git")
    require.ErrorIs(t, err, source.ErrUnknownProvider)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/... -v -timeout 30s
```

Expected: FAIL — package missing.

- [ ] **Step 3: Implement**

`internal/agent/source/source.go`:

```go
package source

import (
    "context"
    "errors"
    "fmt"
    "sync"
    "time"

    relayv1 "relay/internal/proto/relayv1"
)

type Provider interface {
    Type() string
    // Prepare acquires a workspace and prepares it (sync, optional unshelve).
    // taskID identifies the calling task — providers use it to scope side effects
    // (e.g. the per-task pending changelist used for unshelve cleanup).
    Prepare(ctx context.Context, taskID string, spec *relayv1.SourceSpec, progress func(line string)) (Handle, error)
}

type Handle interface {
    WorkingDir() string
    Env() map[string]string
    Finalize(ctx context.Context) error
    Inventory() InventoryEntry
}

type InventoryEntry struct {
    SourceType   string
    SourceKey    string
    ShortID      string
    BaselineHash string
    LastUsedAt   time.Time
    Deleted      bool
}

var ErrUnknownProvider = errors.New("unknown source provider")

type Registry struct {
    mu        sync.RWMutex
    factories map[string]func() Provider
    instances map[string]Provider
}

func NewRegistry() *Registry {
    return &Registry{factories: map[string]func() Provider{}, instances: map[string]Provider{}}
}

func (r *Registry) Register(typ string, factory func() Provider) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.factories[typ] = factory
}

func (r *Registry) Get(typ string) (Provider, error) {
    r.mu.RLock()
    if p, ok := r.instances[typ]; ok {
        r.mu.RUnlock()
        return p, nil
    }
    factory, ok := r.factories[typ]
    r.mu.RUnlock()
    if !ok {
        return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, typ)
    }
    r.mu.Lock()
    defer r.mu.Unlock()
    if p, ok := r.instances[typ]; ok {
        return p, nil
    }
    p := factory()
    r.instances[typ] = p
    return p, nil
}
```

- [ ] **Step 4: Run test to verify pass**

```bash
go test ./internal/agent/source/... -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/source.go internal/agent/source/source_test.go
git commit -m "feat(agent/source): add Provider/Handle interfaces and Registry"
```

---

### Task 6: Perforce client subprocess wrapper

**Files:**
- Create: `internal/agent/source/perforce/client.go`
- Create: `internal/agent/source/perforce/client_test.go`

The wrapper exec's `p4` and parses output. Tests fake the binary by inserting a script-on-PATH or by injecting a `runner` function.

- [ ] **Step 1: Write failing test**

`internal/agent/source/perforce/client_test.go`:

```go
package perforce

import (
    "context"
    "errors"
    "io"
    "strings"
    "testing"

    "github.com/stretchr/testify/require"
)

type fakeRunner struct {
    calls []runCall
    out   map[string]string
    err   map[string]error
}

type runCall struct {
    args  []string
    stdin string
}

func (f *fakeRunner) Run(ctx context.Context, args []string, stdin io.Reader) ([]byte, error) {
    key := strings.Join(args, " ")
    if e, ok := f.err[key]; ok && e != nil {
        return nil, e
    }
    var sb strings.Builder
    if stdin != nil {
        b, _ := io.ReadAll(stdin)
        sb.Write(b)
    }
    f.calls = append(f.calls, runCall{args: append([]string{}, args...), stdin: sb.String()})
    return []byte(f.out[key]), nil
}

func (f *fakeRunner) Stream(ctx context.Context, args []string, onLine func(string)) error {
    key := strings.Join(args, " ")
    if e, ok := f.err[key]; ok && e != nil {
        return e
    }
    for _, line := range strings.Split(f.out[key], "\n") {
        if line != "" {
            onLine(line)
        }
    }
    f.calls = append(f.calls, runCall{args: append([]string{}, args...)})
    return nil
}

func TestClient_CreateStreamClient_Default(t *testing.T) {
    fr := &fakeRunner{out: map[string]string{
        "client -o -S //streams/X/main relay_h_abc": `Client: relay_h_abc
Owner: relay
Root: D:\rw\abcdef
Stream: //streams/X/main
View: //streams/X/main/... //relay_h_abc/...
`,
        "client -i": "Client relay_h_abc saved.\n",
    }}
    c := &Client{r: fr}
    err := c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "")
    require.NoError(t, err)
    // Two calls: -o (read template) then -i (commit)
    require.Len(t, fr.calls, 2)
    require.Equal(t, []string{"client", "-o", "-S", "//streams/X/main", "relay_h_abc"}, fr.calls[0].args)
    require.Equal(t, []string{"client", "-i"}, fr.calls[1].args)
    require.Contains(t, fr.calls[1].stdin, `Root:	D:\rw\abcdef`)
}

func TestClient_CreateStreamClient_WithTemplate(t *testing.T) {
    fr := &fakeRunner{out: map[string]string{
        "client -o -S //streams/X/main -t base relay_h_abc": `Client: relay_h_abc
Stream: //streams/X/main
Options: clobber
View: //streams/X/main/... //relay_h_abc/...
`,
        "client -i": "Client saved.\n",
    }}
    c := &Client{r: fr}
    err := c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "base")
    require.NoError(t, err)
    require.Equal(t, []string{"client", "-o", "-S", "//streams/X/main", "-t", "base", "relay_h_abc"}, fr.calls[0].args)
}

func TestClient_ResolveHead(t *testing.T) {
    fr := &fakeRunner{out: map[string]string{
        "changes -m1 //streams/X/main/...#head": "Change 12345 on 2026-04-24 by relay@h '...'\n",
    }}
    c := &Client{r: fr}
    cl, err := c.ResolveHead(context.Background(), "//streams/X/main/...")
    require.NoError(t, err)
    require.Equal(t, int64(12345), cl)
}

func TestClient_RunFailureBubbles(t *testing.T) {
    fr := &fakeRunner{err: map[string]error{
        "changes -m1 //x/...#head": errors.New("Perforce password (P4PASSWD) invalid or unset."),
    }}
    c := &Client{r: fr}
    _, err := c.ResolveHead(context.Background(), "//x/...")
    require.ErrorContains(t, err, "P4PASSWD")
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/perforce/... -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/agent/source/perforce/client.go`:

```go
package perforce

import (
    "bufio"
    "bytes"
    "context"
    "fmt"
    "io"
    "os/exec"
    "regexp"
    "strconv"
    "strings"
)

type Runner interface {
    Run(ctx context.Context, args []string, stdin io.Reader) ([]byte, error)
    Stream(ctx context.Context, args []string, onLine func(string)) error
}

// execRunner uses os/exec to invoke the p4 binary on PATH.
type execRunner struct{ binary string }

func newExecRunner() *execRunner { return &execRunner{binary: "p4"} }

func (e *execRunner) Run(ctx context.Context, args []string, stdin io.Reader) ([]byte, error) {
    cmd := exec.CommandContext(ctx, e.binary, args...)
    if stdin != nil {
        cmd.Stdin = stdin
    }
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    out, err := cmd.Output()
    if err != nil {
        return nil, fmt.Errorf("p4 %s: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
    }
    return out, nil
}

func (e *execRunner) Stream(ctx context.Context, args []string, onLine func(string)) error {
    cmd := exec.CommandContext(ctx, e.binary, args...)
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        return err
    }
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Start(); err != nil {
        return err
    }
    sc := bufio.NewScanner(stdout)
    sc.Buffer(make([]byte, 64*1024), 1024*1024)
    for sc.Scan() {
        onLine(sc.Text())
    }
    if err := cmd.Wait(); err != nil {
        return fmt.Errorf("p4 %s: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
    }
    return nil
}

type Client struct {
    r Runner
}

func NewClient() *Client { return &Client{r: newExecRunner()} }

func (c *Client) CreateStreamClient(ctx context.Context, name, root, stream, template string) error {
    args := []string{"client", "-o", "-S", stream}
    if template != "" {
        args = append(args, "-t", template)
    }
    args = append(args, name)
    spec, err := c.r.Run(ctx, args, nil)
    if err != nil {
        return err
    }

    // Override the Root field (and add it if absent) so the spec points at our chosen on-disk dir.
    spec = setSpecField(spec, "Root", root)
    spec = setSpecField(spec, "Host", "")    // blank Host: client portable across rename
    spec = setSpecField(spec, "Owner", "")   // let p4 default to the caller

    if _, err := c.r.Run(ctx, []string{"client", "-i"}, bytes.NewReader(spec)); err != nil {
        return err
    }
    return nil
}

func (c *Client) DeleteClient(ctx context.Context, name string) error {
    _, err := c.r.Run(ctx, []string{"client", "-d", name}, nil)
    return err
}

var changeFirstLine = regexp.MustCompile(`^Change (\d+) `)

func (c *Client) ResolveHead(ctx context.Context, path string) (int64, error) {
    out, err := c.r.Run(ctx, []string{"changes", "-m1", path + "#head"}, nil)
    if err != nil {
        return 0, err
    }
    line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
    m := changeFirstLine.FindStringSubmatch(line)
    if m == nil {
        return 0, fmt.Errorf("could not parse %q", line)
    }
    return strconv.ParseInt(m[1], 10, 64)
}

// SyncStream streams `p4 sync -q --parallel=4 <specs...>` lines via onLine.
// Caller is responsible for setting P4CLIENT in the env.
func (c *Client) SyncStream(ctx context.Context, specs []string, onLine func(string)) error {
    args := append([]string{"sync", "-q", "--parallel=4"}, specs...)
    return c.r.Stream(ctx, args, onLine)
}

// CreatePendingCL with the given description; returns the new CL number.
func (c *Client) CreatePendingCL(ctx context.Context, description string) (int64, error) {
    spec, err := c.r.Run(ctx, []string{"change", "-o"}, nil)
    if err != nil {
        return 0, err
    }
    spec = setSpecField(spec, "Description", description)
    spec = removeSpecBlock(spec, "Files") // ensure empty Files block
    out, err := c.r.Run(ctx, []string{"change", "-i"}, bytes.NewReader(spec))
    if err != nil {
        return 0, err
    }
    // Output is "Change 12345 created."
    re := regexp.MustCompile(`Change (\d+) created`)
    m := re.FindSubmatch(out)
    if m == nil {
        return 0, fmt.Errorf("unexpected change -i output: %s", out)
    }
    return strconv.ParseInt(string(m[1]), 10, 64)
}

func (c *Client) Unshelve(ctx context.Context, sourceCL, targetCL int64) error {
    _, err := c.r.Run(ctx, []string{
        "unshelve", "-s", strconv.FormatInt(sourceCL, 10),
        "-c", strconv.FormatInt(targetCL, 10),
    }, nil)
    return err
}

func (c *Client) RevertCL(ctx context.Context, cl int64) error {
    _, err := c.r.Run(ctx, []string{"revert", "-c", strconv.FormatInt(cl, 10), "//..."}, nil)
    return err
}

func (c *Client) DeleteCL(ctx context.Context, cl int64) error {
    _, err := c.r.Run(ctx, []string{"change", "-d", strconv.FormatInt(cl, 10)}, nil)
    return err
}

// PendingChangesByDescPrefix returns relay-owned pending CLs on this client.
func (c *Client) PendingChangesByDescPrefix(ctx context.Context, client, prefix string) ([]int64, error) {
    out, err := c.r.Run(ctx, []string{"changes", "-c", client, "-s", "pending", "-l"}, nil)
    if err != nil {
        return nil, err
    }
    var cls []int64
    var current int64
    var inDesc bool
    for _, line := range strings.Split(string(out), "\n") {
        if m := changeFirstLine.FindStringSubmatch(line); m != nil {
            current, _ = strconv.ParseInt(m[1], 10, 64)
            inDesc = true
            continue
        }
        if inDesc && strings.TrimSpace(line) != "" && current != 0 {
            // First non-empty description line follows the header
            if strings.HasPrefix(strings.TrimSpace(line), prefix) {
                cls = append(cls, current)
            }
            inDesc = false
            current = 0
        }
    }
    return cls, nil
}

// setSpecField updates or inserts a "Field:\tvalue\n" line at top level of a p4 spec form.
func setSpecField(spec []byte, field, value string) []byte {
    var out bytes.Buffer
    re := regexp.MustCompile(fmt.Sprintf(`(?m)^%s:.*$`, regexp.QuoteMeta(field)))
    if re.Match(spec) {
        replaced := re.ReplaceAll(spec, []byte(fmt.Sprintf("%s:\t%s", field, value)))
        return replaced
    }
    // Insert at top
    fmt.Fprintf(&out, "%s:\t%s\n", field, value)
    out.Write(spec)
    return out.Bytes()
}

func removeSpecBlock(spec []byte, field string) []byte {
    // Removes a multi-line indented block starting with "Field:" until next non-indented line.
    var out bytes.Buffer
    sc := bufio.NewScanner(bytes.NewReader(spec))
    sc.Buffer(make([]byte, 64*1024), 1024*1024)
    skip := false
    for sc.Scan() {
        line := sc.Text()
        if skip {
            if line == "" || (line[0] != '\t' && line[0] != ' ') {
                skip = false
            } else {
                continue
            }
        }
        if strings.HasPrefix(line, field+":") {
            skip = true
            continue
        }
        out.WriteString(line)
        out.WriteByte('\n')
    }
    return out.Bytes()
}
```

- [ ] **Step 4: Run test to verify pass**

```bash
go test ./internal/agent/source/perforce/... -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/client_test.go
git commit -m "feat(agent/source/perforce): add p4 subprocess client wrapper"
```

---

### Task 7: Workspace registry file (`.relay-registry.json`)

**Files:**
- Create: `internal/agent/source/perforce/registry.go`
- Create: `internal/agent/source/perforce/registry_test.go`

- [ ] **Step 1: Write failing test**

`internal/agent/source/perforce/registry_test.go`:

```go
package perforce

import (
    "path/filepath"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestRegistry_RoundTrip(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, ".relay-registry.json")

    r, err := LoadRegistry(path)
    require.NoError(t, err)
    require.Empty(t, r.Workspaces)

    r.Upsert(WorkspaceEntry{
        ShortID:      "abcdef",
        SourceKey:    "//s/x",
        ClientName:   "relay_h_abcdef",
        BaselineHash: "deadbeef",
        LastUsedAt:   time.Now(),
    })
    require.NoError(t, r.Save())

    r2, err := LoadRegistry(path)
    require.NoError(t, err)
    require.Len(t, r2.Workspaces, 1)
    require.Equal(t, "//s/x", r2.Workspaces[0].SourceKey)
}

func TestRegistry_TrackPendingCLAndDirtyDelete(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, ".relay-registry.json")

    r, _ := LoadRegistry(path)
    r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})
    require.NoError(t, r.AddPendingCL("a", "task1", 91244))
    require.NoError(t, r.Save())

    r2, _ := LoadRegistry(path)
    e := r2.Get("a")
    require.NotNil(t, e)
    require.Len(t, e.OpenTaskChangelists, 1)
    require.Equal(t, int64(91244), e.OpenTaskChangelists[0].PendingCL)

    require.NoError(t, r2.RemovePendingCL("a", "task1"))
    require.NoError(t, r2.MarkDirtyDelete("a", true))
    require.NoError(t, r2.Save())

    r3, _ := LoadRegistry(path)
    e = r3.Get("a")
    require.Empty(t, e.OpenTaskChangelists)
    require.True(t, e.DirtyDelete)
}

func TestRegistry_AtomicWrite(t *testing.T) {
    // Save writes to .tmp + rename. After Save, the temp file must not exist.
    dir := t.TempDir()
    path := filepath.Join(dir, ".relay-registry.json")
    r, _ := LoadRegistry(path)
    r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})
    require.NoError(t, r.Save())

    matches, _ := filepath.Glob(filepath.Join(dir, ".relay-registry.json.tmp*"))
    require.Empty(t, matches)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/perforce/... -run TestRegistry -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/agent/source/perforce/registry.go`:

```go
package perforce

import (
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "time"
)

type WorkspaceEntry struct {
    ShortID             string                `json:"short_id"`
    SourceKey           string                `json:"source_key"`
    ClientName          string                `json:"client_name"`
    BaselineHash        string                `json:"baseline_hash"`
    LastUsedAt          time.Time             `json:"last_used_at"`
    OpenTaskChangelists []OpenTaskChangelist  `json:"open_task_changelists,omitempty"`
    DirtyDelete         bool                  `json:"dirty_delete,omitempty"`
}

type OpenTaskChangelist struct {
    TaskID    string `json:"task_id"`
    PendingCL int64  `json:"pending_cl"`
}

type Registry struct {
    mu         sync.Mutex
    path       string
    Workspaces []WorkspaceEntry `json:"workspaces"`
}

func LoadRegistry(path string) (*Registry, error) {
    r := &Registry{path: path}
    b, err := os.ReadFile(path)
    if errors.Is(err, os.ErrNotExist) {
        return r, nil
    }
    if err != nil {
        return nil, err
    }
    if err := json.Unmarshal(b, r); err != nil {
        return nil, fmt.Errorf("parse registry: %w", err)
    }
    return r, nil
}

func (r *Registry) Save() error {
    r.mu.Lock()
    defer r.mu.Unlock()
    b, err := json.MarshalIndent(r, "", "  ")
    if err != nil {
        return err
    }
    tmp := r.path + ".tmp"
    if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
        return err
    }
    if err := os.WriteFile(tmp, b, 0o600); err != nil {
        return err
    }
    return os.Rename(tmp, r.path)
}

func (r *Registry) Get(shortID string) *WorkspaceEntry {
    r.mu.Lock()
    defer r.mu.Unlock()
    for i := range r.Workspaces {
        if r.Workspaces[i].ShortID == shortID {
            return &r.Workspaces[i]
        }
    }
    return nil
}

func (r *Registry) GetBySourceKey(sourceKey string) *WorkspaceEntry {
    r.mu.Lock()
    defer r.mu.Unlock()
    for i := range r.Workspaces {
        if r.Workspaces[i].SourceKey == sourceKey {
            return &r.Workspaces[i]
        }
    }
    return nil
}

func (r *Registry) Upsert(e WorkspaceEntry) {
    r.mu.Lock()
    defer r.mu.Unlock()
    for i := range r.Workspaces {
        if r.Workspaces[i].ShortID == e.ShortID {
            r.Workspaces[i] = e
            return
        }
    }
    r.Workspaces = append(r.Workspaces, e)
}

func (r *Registry) Remove(shortID string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    out := r.Workspaces[:0]
    for _, e := range r.Workspaces {
        if e.ShortID != shortID {
            out = append(out, e)
        }
    }
    r.Workspaces = out
}

func (r *Registry) AddPendingCL(shortID, taskID string, cl int64) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    for i := range r.Workspaces {
        if r.Workspaces[i].ShortID == shortID {
            r.Workspaces[i].OpenTaskChangelists = append(r.Workspaces[i].OpenTaskChangelists,
                OpenTaskChangelist{TaskID: taskID, PendingCL: cl})
            return nil
        }
    }
    return fmt.Errorf("workspace %s not found", shortID)
}

func (r *Registry) RemovePendingCL(shortID, taskID string) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    for i := range r.Workspaces {
        if r.Workspaces[i].ShortID != shortID {
            continue
        }
        out := r.Workspaces[i].OpenTaskChangelists[:0]
        for _, c := range r.Workspaces[i].OpenTaskChangelists {
            if c.TaskID != taskID {
                out = append(out, c)
            }
        }
        r.Workspaces[i].OpenTaskChangelists = out
        return nil
    }
    return fmt.Errorf("workspace %s not found", shortID)
}

func (r *Registry) MarkDirtyDelete(shortID string, dirty bool) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    for i := range r.Workspaces {
        if r.Workspaces[i].ShortID == shortID {
            r.Workspaces[i].DirtyDelete = dirty
            return nil
        }
    }
    return fmt.Errorf("workspace %s not found", shortID)
}
```

- [ ] **Step 4: Run test to verify pass**

```bash
go test ./internal/agent/source/perforce/... -run TestRegistry -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/registry.go internal/agent/source/perforce/registry_test.go
git commit -m "feat(agent/source/perforce): add workspace registry with atomic writes"
```

---

### Task 8: Baseline canonicalization and hash

**Files:**
- Create: `internal/agent/source/perforce/baseline.go`
- Create: `internal/agent/source/perforce/baseline_test.go`

- [ ] **Step 1: Write failing test**

`internal/agent/source/perforce/baseline_test.go`:

```go
package perforce

import (
    "testing"

    "github.com/stretchr/testify/require"
    relayv1 "relay/internal/proto/relayv1"
)

func TestBaselineHash_StableUnderReorder(t *testing.T) {
    a := &relayv1.PerforceSource{
        Stream: "//s/x",
        Sync: []*relayv1.SyncEntry{
            {Path: "//s/x/a/...", Rev: "@100"},
            {Path: "//s/x/b/...", Rev: "@200"},
        },
        Unshelves: []int64{2, 1, 3},
    }
    b := &relayv1.PerforceSource{
        Stream: "//s/x",
        Sync: []*relayv1.SyncEntry{
            {Path: "//s/x/b/...", Rev: "@200"},
            {Path: "//s/x/a/...", Rev: "@100"},
        },
        Unshelves: []int64{3, 1, 2},
    }
    require.Equal(t, BaselineHash(a, nil), BaselineHash(b, nil))
}

func TestBaselineHash_HeadResolvedVsLiteral(t *testing.T) {
    a := &relayv1.PerforceSource{
        Sync: []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
    }
    resolved := map[string]string{"//s/x/...": "@12345"}
    h1 := BaselineHash(a, nil)         // #head as sentinel
    h2 := BaselineHash(a, resolved)    // resolved
    require.NotEqual(t, h1, h2, "estimated and resolved must differ — they are different baselines")
}

func TestPathOverlap(t *testing.T) {
    require.True(t, PathPrefixOverlap("//a/b/...", "//a/b/c/..."))
    require.True(t, PathPrefixOverlap("//a/b/c/...", "//a/b/..."))
    require.False(t, PathPrefixOverlap("//a/b/...", "//a/c/..."))
    require.True(t, PathPrefixOverlap("//a/b/...", "//a/b/..."))
    require.False(t, PathPrefixOverlap("//a/b/x.ma", "//a/b/y.ma"))
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/perforce/... -run TestBaseline -v -timeout 30s
go test ./internal/agent/source/perforce/... -run TestPathOverlap -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/agent/source/perforce/baseline.go`:

```go
package perforce

import (
    "crypto/sha256"
    "encoding/hex"
    "sort"
    "strings"

    relayv1 "relay/internal/proto/relayv1"
)

// BaselineHash returns a 16-char canonical hash of the resolved sync spec +
// unshelves. If `resolvedHead` is provided and a sync entry's rev is "#head",
// the resolved value (e.g. "@12345") is used; otherwise the literal "#head"
// is hashed (server-side estimate).
func BaselineHash(p *relayv1.PerforceSource, resolvedHead map[string]string) string {
    if p == nil {
        return ""
    }
    type entry struct{ path, rev string }
    es := make([]entry, 0, len(p.Sync))
    for _, e := range p.Sync {
        rev := e.Rev
        if e.Rev == "#head" && resolvedHead != nil {
            if r, ok := resolvedHead[e.Path]; ok {
                rev = r
            }
        }
        es = append(es, entry{e.Path, rev})
    }
    sort.Slice(es, func(i, j int) bool {
        if es[i].path != es[j].path {
            return es[i].path < es[j].path
        }
        return es[i].rev < es[j].rev
    })
    us := append([]int64(nil), p.Unshelves...)
    sort.Slice(us, func(i, j int) bool { return us[i] < us[j] })

    h := sha256.New()
    h.Write([]byte(p.Stream))
    h.Write([]byte{0})
    for _, e := range es {
        h.Write([]byte(e.path))
        h.Write([]byte{0})
        h.Write([]byte(e.rev))
        h.Write([]byte{0})
    }
    h.Write([]byte{1})
    for _, u := range us {
        // Avoid encoding/binary import; use decimal representation.
        h.Write([]byte(intToStr(u)))
        h.Write([]byte{0})
    }
    return hex.EncodeToString(h.Sum(nil))[:16]
}

func intToStr(v int64) string {
    // small inlined helper to avoid importing strconv here
    var b [20]byte
    i := len(b)
    neg := v < 0
    if neg {
        v = -v
    }
    if v == 0 {
        return "0"
    }
    for v > 0 {
        i--
        b[i] = byte('0' + v%10)
        v /= 10
    }
    if neg {
        i--
        b[i] = '-'
    }
    return string(b[i:])
}

// PathPrefixOverlap reports whether two depot paths could touch the same files.
// Treats trailing "/..." as a wildcard.
func PathPrefixOverlap(a, b string) bool {
    a = strings.TrimSuffix(a, "/...")
    b = strings.TrimSuffix(b, "/...")
    return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}
```

- [ ] **Step 4: Run test to verify pass**

```bash
go test ./internal/agent/source/perforce/... -run TestBaseline -v -timeout 30s
go test ./internal/agent/source/perforce/... -run TestPathOverlap -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/baseline.go internal/agent/source/perforce/baseline_test.go
git commit -m "feat(agent/source/perforce): add baseline hash and path overlap helpers"
```

---

## Phase 3 — Workspace concurrency arbitration

### Task 9: Workspace struct with arbitration state machine

**Files:**
- Create: `internal/agent/source/perforce/workspace.go`
- Create: `internal/agent/source/perforce/workspace_test.go`

- [ ] **Step 1: Write failing test**

`internal/agent/source/perforce/workspace_test.go`:

```go
package perforce

import (
    "context"
    "sync/atomic"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func newReq(baseline string, paths []string, unshelves []int64, exclusive bool) Request {
    return Request{
        BaselineHash:       baseline,
        SyncPaths:          paths,
        Unshelves:          unshelves,
        WorkspaceExclusive: exclusive,
    }
}

func TestWorkspace_IdenticalSharedAdmits(t *testing.T) {
    ws := NewWorkspace("a")
    ctx := context.Background()
    h1, err := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
    require.NoError(t, err)
    require.Equal(t, ModeShared, h1.Mode())

    done := make(chan struct{})
    go func() {
        h2, err := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
        require.NoError(t, err)
        require.Equal(t, ModeShared, h2.Mode())
        h2.Release()
        close(done)
    }()
    select {
    case <-done:
    case <-time.After(time.Second):
        t.Fatal("identical-baseline shared acquire did not admit")
    }
    h1.Release()
}

func TestWorkspace_DifferentBaselineSerializes(t *testing.T) {
    ws := NewWorkspace("a")
    ctx := context.Background()
    h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
    require.Equal(t, ModeShared, h1.Mode())

    var admitted atomic.Bool
    go func() {
        h2, err := ws.Acquire(ctx, newReq("BL2", []string{"//s/x/..."}, nil, false))
        require.NoError(t, err)
        admitted.Store(true)
        require.Equal(t, ModeExclusive, h2.Mode())
        h2.Release()
    }()
    time.Sleep(50 * time.Millisecond)
    require.False(t, admitted.Load(), "must wait while BL1 holder is active")
    h1.Release()
    require.Eventually(t, admitted.Load, time.Second, 10*time.Millisecond)
}

func TestWorkspace_DisjointAdditiveAdmits(t *testing.T) {
    ws := NewWorkspace("a")
    ctx := context.Background()
    h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/A/..."}, nil, false))
    h2, err := ws.Acquire(ctx, newReq("BL2", []string{"//s/x/B/..."}, nil, false))
    require.NoError(t, err)
    require.Equal(t, ModeShared, h2.Mode())
    h2.Release()
    h1.Release()
}

func TestWorkspace_OverlappingDifferentBaselineSerializes(t *testing.T) {
    ws := NewWorkspace("a")
    ctx := context.Background()
    h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/A/..."}, nil, false))

    var admitted atomic.Bool
    go func() {
        h2, _ := ws.Acquire(ctx, newReq("BL2", []string{"//s/x/A/sub/..."}, nil, false))
        admitted.Store(true)
        h2.Release()
    }()
    time.Sleep(50 * time.Millisecond)
    require.False(t, admitted.Load())
    h1.Release()
    require.Eventually(t, admitted.Load, time.Second, 10*time.Millisecond)
}

func TestWorkspace_ExclusiveBlocks(t *testing.T) {
    ws := NewWorkspace("a")
    ctx := context.Background()
    h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, true /*exclusive*/))
    require.Equal(t, ModeExclusive, h1.Mode())

    var admitted atomic.Bool
    go func() {
        h2, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
        admitted.Store(true)
        h2.Release()
    }()
    time.Sleep(50 * time.Millisecond)
    require.False(t, admitted.Load(), "exclusive holder must block any acquire")
    h1.Release()
    require.Eventually(t, admitted.Load, time.Second, 10*time.Millisecond)
}

func TestWorkspace_UnshelvingBlocks(t *testing.T) {
    ws := NewWorkspace("a")
    ctx := context.Background()
    h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, []int64{1234}, false))
    require.Equal(t, ModeExclusive, h1.Mode(),
        "unshelve requires exclusive end-to-end")
    h1.Release()
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/perforce/... -run TestWorkspace -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/agent/source/perforce/workspace.go`:

```go
package perforce

import (
    "context"
    "sync"
)

type Mode int

const (
    ModeShared    Mode = 0
    ModeExclusive Mode = 1
)

type Request struct {
    BaselineHash       string
    SyncPaths          []string
    Unshelves          []int64
    WorkspaceExclusive bool
}

type holder struct {
    req  Request
    mode Mode
}

type Workspace struct {
    shortID string
    mu      sync.Mutex
    cond    *sync.Cond
    holders []*holder
}

func NewWorkspace(shortID string) *Workspace {
    w := &Workspace{shortID: shortID}
    w.cond = sync.NewCond(&w.mu)
    return w
}

type WorkspaceHandle struct {
    ws   *Workspace
    h    *holder
}

func (h *WorkspaceHandle) Mode() Mode { return h.h.mode }

func (h *WorkspaceHandle) Release() {
    h.ws.release(h.h)
}

// Downgrade switches an exclusive hold to shared, allowing same-baseline
// shared peers to coexist with us.
func (h *WorkspaceHandle) Downgrade() {
    h.ws.mu.Lock()
    defer h.ws.mu.Unlock()
    h.h.mode = ModeShared
    h.ws.cond.Broadcast()
}

func (w *Workspace) Acquire(ctx context.Context, req Request) (*WorkspaceHandle, error) {
    w.mu.Lock()
    defer w.mu.Unlock()
    for {
        if err := ctx.Err(); err != nil {
            return nil, err
        }
        mode, ok := w.tryAdmit(req)
        if ok {
            h := &holder{req: req, mode: mode}
            w.holders = append(w.holders, h)
            return &WorkspaceHandle{ws: w, h: h}, nil
        }
        // Wait — but bail on ctx cancel.
        waitDone := make(chan struct{})
        go func() { w.cond.Broadcast(); <-waitDone }() // ensure unlock-on-cancel
        // Use a goroutine to wake on ctx.Done.
        wakeOnCancel := make(chan struct{})
        go func() {
            select {
            case <-ctx.Done():
                w.mu.Lock()
                w.cond.Broadcast()
                w.mu.Unlock()
            case <-wakeOnCancel:
            }
        }()
        w.cond.Wait()
        close(waitDone)
        close(wakeOnCancel)
    }
}

// tryAdmit applies the three rules. Caller holds w.mu.
func (w *Workspace) tryAdmit(req Request) (Mode, bool) {
    needsExclusive := req.WorkspaceExclusive || len(req.Unshelves) > 0

    if len(w.holders) == 0 {
        if needsExclusive {
            return ModeExclusive, true
        }
        // The very first acquire on a workspace can be shared — Prepare itself
        // upgrades to exclusive internally if the registry shows a different
        // baseline, but the lock starts where the request asked for.
        return ModeShared, true
    }

    if needsExclusive {
        return 0, false
    }

    // Rule 1: identical baseline + no unshelves on either side + no holder is exclusive.
    identical := true
    disjoint := true
    for _, h := range w.holders {
        if h.mode == ModeExclusive || len(h.req.Unshelves) > 0 {
            return 0, false
        }
        if h.req.BaselineHash != req.BaselineHash {
            identical = false
        }
        for _, hp := range h.req.SyncPaths {
            for _, rp := range req.SyncPaths {
                if PathPrefixOverlap(hp, rp) {
                    disjoint = false
                }
            }
        }
    }
    if identical {
        return ModeShared, true
    }
    if disjoint {
        return ModeShared, true
    }
    return 0, false
}

func (w *Workspace) release(h *holder) {
    w.mu.Lock()
    defer w.mu.Unlock()
    out := w.holders[:0]
    for _, x := range w.holders {
        if x != h {
            out = append(out, x)
        }
    }
    w.holders = out
    w.cond.Broadcast()
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/agent/source/perforce/... -run TestWorkspace -v -timeout 30s
```

Expected: PASS, no data races.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/workspace.go internal/agent/source/perforce/workspace_test.go
git commit -m "feat(agent/source/perforce): add workspace arbitration state machine"
```

---

## Phase 4 — Perforce provider

### Task 10: Perforce provider — Prepare with stream-bound client and sync

**Files:**
- Create: `internal/agent/source/perforce/perforce.go`
- Create: `internal/agent/source/perforce/perforce_test.go`

- [ ] **Step 1: Write failing test**

`internal/agent/source/perforce/perforce_test.go`:

```go
package perforce

import (
    "context"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/require"
    relayv1 "relay/internal/proto/relayv1"
)

func TestProvider_PrepareCreatesClientAndSyncs(t *testing.T) {
    root := t.TempDir()
    fr := newFakeP4Fixture()
    fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
    fr.set("client -o -S //s/x ", "Stream:\t//s/x\nView:\t//s/x/... //relay_h_xxx/...\n")
    fr.set("client -i", "Client saved.\n")
    fr.setStream("sync -q --parallel=4 //s/x/...@12345", "1 of 1 files\n")

    p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
    spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
        Perforce: &relayv1.PerforceSource{
            Stream: "//s/x",
            Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
        },
    }}
    var lines []string
    h, err := p.Prepare(context.Background(), "task-1", spec, func(s string) { lines = append(lines, s) })
    require.NoError(t, err)
    defer h.Finalize(context.Background())

    inv := h.Inventory()
    require.Equal(t, "perforce", inv.SourceType)
    require.Equal(t, "//s/x", inv.SourceKey)
    require.NotEmpty(t, inv.ShortID)
    require.NotEmpty(t, inv.BaselineHash)

    require.True(t, filepath.IsAbs(h.WorkingDir()))
    require.Contains(t, h.WorkingDir(), inv.ShortID)
    require.Contains(t, h.Env()["P4CLIENT"], inv.ShortID)
    require.NotEmpty(t, lines, "sync stream should have produced progress lines")
}

// newFakeP4Fixture is the shared test helper from client_test.go restructured for reuse;
// place it in a *_test.go file (e.g. testing.go is not visible to other tests; instead
// move fakeRunner from client_test.go to a new fixtures_test.go in this same package).
```

(Practical note: refactor `fakeRunner` from Task 6 into `internal/agent/source/perforce/fixtures_test.go` so multiple test files share it.)

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/perforce/... -run TestProvider_PrepareCreatesClientAndSyncs -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement Provider**

`internal/agent/source/perforce/perforce.go`:

```go
package perforce

import (
    "context"
    "crypto/sha256"
    "encoding/base32"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"

    "relay/internal/agent/source"
    relayv1 "relay/internal/proto/relayv1"
)

type Config struct {
    Root     string  // RELAY_WORKSPACE_ROOT
    Hostname string  // sanitized
    Client   *Client // override for tests; nil → exec p4
}

type Provider struct {
    cfg        Config
    mu         sync.Mutex
    workspaces map[string]*Workspace // keyed by short_id
    registry   *Registry
}

func New(cfg Config) *Provider {
    if cfg.Client == nil {
        cfg.Client = NewClient()
    }
    cfg.Hostname = sanitizeHostname(cfg.Hostname)
    return &Provider{cfg: cfg, workspaces: map[string]*Workspace{}}
}

func (p *Provider) Type() string { return "perforce" }

func (p *Provider) loadRegistry() (*Registry, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    if p.registry != nil {
        return p.registry, nil
    }
    r, err := LoadRegistry(filepath.Join(p.cfg.Root, ".relay-registry.json"))
    if err != nil {
        return nil, err
    }
    p.registry = r
    return r, nil
}

func (p *Provider) Prepare(ctx context.Context, taskID string, spec *relayv1.SourceSpec, progress func(line string)) (source.Handle, error) {
    _ = taskID // used by Task 11 (unshelve pending CL); kept on the signature here so the interface is stable.
    pf := spec.GetPerforce()
    if pf == nil {
        return nil, fmt.Errorf("perforce: spec.perforce is nil")
    }

    reg, err := p.loadRegistry()
    if err != nil {
        return nil, err
    }

    // Resolve #head → @CL for each sync entry; build resolved spec list.
    resolved := make(map[string]string, len(pf.Sync))
    syncSpecs := make([]string, 0, len(pf.Sync))
    syncPaths := make([]string, 0, len(pf.Sync))
    for _, e := range pf.Sync {
        rev := e.Rev
        if rev == "#head" {
            cl, err := p.cfg.Client.ResolveHead(ctx, e.Path)
            if err != nil {
                return nil, fmt.Errorf("resolve head for %s: %w", e.Path, err)
            }
            rev = fmt.Sprintf("@%d", cl)
            resolved[e.Path] = rev
        }
        syncSpecs = append(syncSpecs, e.Path+rev)
        syncPaths = append(syncPaths, e.Path)
    }

    baseline := BaselineHash(pf, resolved)

    // Look up or allocate workspace.
    existing := reg.GetBySourceKey(pf.Stream)
    var shortID string
    if existing != nil {
        shortID = existing.ShortID
    } else {
        shortID = allocateShortID(pf.Stream, reg)
    }
    wsRoot := filepath.Join(p.cfg.Root, shortID)
    clientName := fmt.Sprintf("relay_%s_%s", p.cfg.Hostname, shortID)

    p.mu.Lock()
    ws, ok := p.workspaces[shortID]
    if !ok {
        ws = NewWorkspace(shortID)
        p.workspaces[shortID] = ws
    }
    p.mu.Unlock()

    req := Request{
        BaselineHash:       baseline,
        SyncPaths:          syncPaths,
        Unshelves:          pf.Unshelves,
        WorkspaceExclusive: pf.WorkspaceExclusive,
    }
    handle, err := ws.Acquire(ctx, req)
    if err != nil {
        return nil, err
    }

    // If the workspace is fresh on disk, ensure dir + client spec exist.
    if existing == nil {
        if err := os.MkdirAll(wsRoot, 0o755); err != nil {
            handle.Release()
            return nil, err
        }
        tmpl := ""
        if pf.ClientTemplate != nil {
            tmpl = *pf.ClientTemplate
        }
        if err := p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl); err != nil {
            handle.Release()
            return nil, err
        }
        reg.Upsert(WorkspaceEntry{
            ShortID:      shortID,
            SourceKey:    pf.Stream,
            ClientName:   clientName,
            BaselineHash: "",
            LastUsedAt:   time.Now(),
        })
        if err := reg.Save(); err != nil {
            handle.Release()
            return nil, err
        }
    }

    // If we're in exclusive mode (or admitted shared on a fresh workspace)
    // and the registry's baseline doesn't match, run sync.
    cur := reg.Get(shortID)
    needsSync := handle.Mode() == ModeExclusive || cur.BaselineHash != baseline
    if needsSync {
        env := []string{"P4CLIENT=" + clientName}
        // Note: in fakeRunner this env doesn't apply, but the real exec runner
        // inherits the calling process env + this. We still set P4CLIENT below
        // via Handle.Env() for the task subprocess.
        _ = env
        if err := p.cfg.Client.SyncStream(ctx, syncSpecs, progress); err != nil {
            handle.Release()
            return nil, fmt.Errorf("p4 sync: %w", err)
        }
        cur.BaselineHash = baseline
        cur.LastUsedAt = time.Now()
        reg.Upsert(*cur)
        _ = reg.Save()
    }

    h := &perforceHandle{
        provider:     p,
        workspaceDir: wsRoot,
        clientName:   clientName,
        sourceKey:    pf.Stream,
        shortID:      shortID,
        baselineHash: baseline,
        wsHandle:     handle,
    }
    return h, nil
}

type perforceHandle struct {
    provider     *Provider
    workspaceDir string
    clientName   string
    sourceKey    string
    shortID      string
    baselineHash string
    wsHandle     *WorkspaceHandle
}

func (h *perforceHandle) WorkingDir() string { return h.workspaceDir }
func (h *perforceHandle) Env() map[string]string {
    return map[string]string{"P4CLIENT": h.clientName}
}
func (h *perforceHandle) Inventory() source.InventoryEntry {
    return source.InventoryEntry{
        SourceType:   "perforce",
        SourceKey:    h.sourceKey,
        ShortID:      h.shortID,
        BaselineHash: h.baselineHash,
        LastUsedAt:   time.Now(),
    }
}
func (h *perforceHandle) Finalize(ctx context.Context) error {
    h.wsHandle.Release()
    return nil
}

// allocateShortID returns a new 6-char base32 hash; extends to 8 on collision.
func allocateShortID(stream string, reg *Registry) string {
    sum := sha256.Sum256([]byte(stream))
    enc := strings.ToLower(base32.StdEncoding.EncodeToString(sum[:]))
    enc = strings.TrimRight(enc, "=")
    for n := 6; n <= len(enc); n += 2 {
        candidate := enc[:n]
        if !shortIDInUse(reg, candidate, stream) {
            return candidate
        }
    }
    // Should never happen — full 52 chars is unique.
    return enc
}

func shortIDInUse(reg *Registry, shortID, sourceKey string) bool {
    for _, w := range reg.Workspaces {
        if w.ShortID == shortID && w.SourceKey != sourceKey {
            return true
        }
    }
    return false
}

func sanitizeHostname(h string) string {
    var b strings.Builder
    for _, r := range h {
        switch {
        case r >= 'A' && r <= 'Z':
            b.WriteRune(r)
        case r >= 'a' && r <= 'z':
            b.WriteRune(r)
        case r >= '0' && r <= '9':
            b.WriteRune(r)
        case r == '-':
            b.WriteRune(r)
        default:
            b.WriteRune('_')
        }
    }
    out := b.String()
    if len(out) > 32 {
        out = out[:32]
    }
    return out
}
```

(The fake fixture in `fixtures_test.go` needs a `setStream` helper to feed `Stream` calls as well as `Run` calls; refactor accordingly.)

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/agent/source/perforce/... -run TestProvider_PrepareCreatesClientAndSyncs -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go internal/agent/source/perforce/fixtures_test.go internal/agent/source/perforce/client_test.go
git commit -m "feat(agent/source/perforce): Provider.Prepare creates stream client and syncs"
```

---

### Task 11: Unshelve into per-task pending CL; Finalize reverts

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go`
- Modify: `internal/agent/source/perforce/perforce_test.go`

- [ ] **Step 1: Write failing test**

Append to `perforce_test.go`:

```go
func TestProvider_UnshelveAndFinalizeRevert(t *testing.T) {
    root := t.TempDir()
    fr := newFakeP4Fixture()
    fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
    fr.set("client -o -S //s/x ", "Stream:\t//s/x\nView:\t//s/x/... //x/...\n")
    fr.set("client -i", "Client saved.\n")
    fr.setStream("sync -q --parallel=4 //s/x/...@12345", "1 of 1 files\n")
    fr.set("change -o", "Change: new\nDescription:\t\n")
    fr.set("change -i", "Change 91244 created.\n")
    fr.set("unshelve -s 12346 -c 91244", "//s/x/foo - unshelved\n")
    fr.set("revert -c 91244 //...", "//s/x/foo - reverted\n")
    fr.set("change -d 91244", "Change 91244 deleted.\n")

    p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
    spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
        Perforce: &relayv1.PerforceSource{
            Stream:    "//s/x",
            Sync:      []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
            Unshelves: []int64{12346},
        },
    }}

    h, err := p.Prepare(context.Background(), spec, func(string) {})
    require.NoError(t, err)
    require.NoError(t, h.Finalize(context.Background()))

    // Verify: change-i (create CL), unshelve, revert -c, change -d
    args := fr.argHistory()
    require.Contains(t, args, []string{"change", "-i"})
    require.Contains(t, args, []string{"unshelve", "-s", "12346", "-c", "91244"})
    require.Contains(t, args, []string{"revert", "-c", "91244", "//..."})
    require.Contains(t, args, []string{"change", "-d", "91244"})

    // After Finalize, registry must have no open_task_changelists for this workspace.
    reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
    e := reg.GetBySourceKey("//s/x")
    require.Empty(t, e.OpenTaskChangelists)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/perforce/... -run TestProvider_UnshelveAndFinalizeRevert -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

The Provider interface already takes `taskID` (defined in Task 5). In `perforce.go`, after the existing sync block in `Prepare`, replace the `_ = taskID` line with:

```go
// Per-task pending CL for unshelves.
var pendingCL int64
if len(pf.Unshelves) > 0 {
    cl, err := p.cfg.Client.CreatePendingCL(ctx, "relay-task-"+taskID)
    if err != nil {
        handle.Release()
        return nil, fmt.Errorf("create pending CL: %w", err)
    }
    pendingCL = cl
    if err := reg.AddPendingCL(shortID, taskID, cl); err != nil {
        handle.Release()
        return nil, err
    }
    _ = reg.Save()
    for _, src := range pf.Unshelves {
        if err := p.cfg.Client.Unshelve(ctx, src, cl); err != nil {
            handle.Release()
            return nil, fmt.Errorf("unshelve %d: %w", src, err)
        }
    }
}

h := &perforceHandle{
    provider:     p,
    workspaceDir: wsRoot,
    clientName:   clientName,
    sourceKey:    pf.Stream,
    shortID:      shortID,
    baselineHash: baseline,
    wsHandle:     handle,
    taskID:       taskID,
    pendingCL:    pendingCL,
}
return h, nil
```

Add fields to `perforceHandle`:

```go
type perforceHandle struct {
    // ... existing fields ...
    taskID    string
    pendingCL int64
}
```

Replace `Finalize`:

```go
func (h *perforceHandle) Finalize(ctx context.Context) error {
    defer h.wsHandle.Release()
    if h.pendingCL == 0 {
        return nil
    }
    revertErr := h.provider.cfg.Client.RevertCL(ctx, h.pendingCL)
    delErr := h.provider.cfg.Client.DeleteCL(ctx, h.pendingCL)
    reg, err := h.provider.loadRegistry()
    if err == nil {
        _ = reg.RemovePendingCL(h.shortID, h.taskID)
        _ = reg.Save()
    }
    if revertErr != nil {
        return fmt.Errorf("revert CL %d: %w", h.pendingCL, revertErr)
    }
    if delErr != nil {
        return fmt.Errorf("delete CL %d: %w", h.pendingCL, delErr)
    }
    return nil
}
```

All call sites already pass `taskID` because the interface was defined that way in Task 5.

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/agent/source/... -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/source.go internal/agent/source/source_test.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "feat(agent/source/perforce): unshelve into per-task pending CL with scoped revert"
```

---

### Task 12: Crash recovery for orphaned pending CLs and stale baselines

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go`
- Modify: `internal/agent/source/perforce/perforce_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestProvider_CrashRecovery_DeletesOrphanedPendingCLs(t *testing.T) {
    root := t.TempDir()
    fr := newFakeP4Fixture()

    // Pre-existing workspace in registry with an orphaned pending CL recorded
    reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
    reg.Upsert(WorkspaceEntry{
        ShortID: "abcdef", SourceKey: "//s/x",
        ClientName: "relay_h_abcdef", BaselineHash: "deadbeef",
        LastUsedAt: time.Now(),
        OpenTaskChangelists: []OpenTaskChangelist{{TaskID: "old", PendingCL: 91244}},
    })
    require.NoError(t, reg.Save())
    require.NoError(t, os.MkdirAll(filepath.Join(root, "abcdef"), 0o755))

    fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
    fr.set("changes -c relay_h_abcdef -s pending -l", "Change 91244 on 2026-04-24 by relay@h *pending*\n\trelay-task-old\n\nChange 99999 on 2026-04-24 by other@h *pending*\n\thuman work\n")
    fr.set("revert -c 91244 //...", "//... - reverted\n")
    fr.set("change -d 91244", "Change 91244 deleted.\n")
    fr.setStream("sync -q --parallel=4 //s/x/...@12345", "ok\n")

    p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
    spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
        Perforce: &relayv1.PerforceSource{
            Stream: "//s/x",
            Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
        },
    }}
    h, err := p.Prepare(context.Background(), "task-new", spec, func(string) {})
    require.NoError(t, err)
    require.NoError(t, h.Finalize(context.Background()))

    args := fr.argHistory()
    require.Contains(t, args, []string{"revert", "-c", "91244", "//..."})
    require.Contains(t, args, []string{"change", "-d", "91244"})
    // Must not delete CL 99999 (not relay-owned)
    for _, a := range args {
        if len(a) >= 3 && a[0] == "change" && a[1] == "-d" && a[2] == "99999" {
            t.Fatal("must not delete non-relay pending CL")
        }
    }
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/perforce/... -run TestProvider_CrashRecovery -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

In `Prepare`, after a workspace is admitted in **exclusive** mode, before sync, run a recovery pass:

```go
if handle.Mode() == ModeExclusive {
    if err := p.recoverOrphanedCLs(ctx, clientName); err != nil {
        // Best-effort; log via progress and continue. Recovery failure
        // does not stop the task — the next attempt will retry.
        progress(fmt.Sprintf("[recover] %v", err))
    }
}
```

Add:

```go
func (p *Provider) recoverOrphanedCLs(ctx context.Context, clientName string) error {
    cls, err := p.cfg.Client.PendingChangesByDescPrefix(ctx, clientName, "relay-task-")
    if err != nil {
        return err
    }
    for _, cl := range cls {
        if err := p.cfg.Client.RevertCL(ctx, cl); err != nil {
            return fmt.Errorf("revert orphan CL %d: %w", cl, err)
        }
        if err := p.cfg.Client.DeleteCL(ctx, cl); err != nil {
            return fmt.Errorf("delete orphan CL %d: %w", cl, err)
        }
    }
    return nil
}
```

- [ ] **Step 4: Run test to verify pass**

```bash
go test -race ./internal/agent/source/perforce/... -run TestProvider_CrashRecovery -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "feat(agent/source/perforce): crash-recover orphaned relay-owned pending CLs"
```

---

### Task 13: Eviction sweeper (age + disk pressure)

**Files:**
- Create: `internal/agent/source/perforce/sweeper.go`
- Create: `internal/agent/source/perforce/sweeper_test.go`

- [ ] **Step 1: Write failing test**

```go
package perforce

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestSweeper_AgeEviction(t *testing.T) {
    root := t.TempDir()
    fr := newFakeP4Fixture()
    fr.set("client -d relay_h_old", "Client deleted.\n")

    reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
    reg.Upsert(WorkspaceEntry{ShortID: "old", SourceKey: "//s/x",
        ClientName: "relay_h_old", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
    reg.Upsert(WorkspaceEntry{ShortID: "fresh", SourceKey: "//s/y",
        ClientName: "relay_h_fresh", LastUsedAt: time.Now()})
    require.NoError(t, reg.Save())
    require.NoError(t, os.MkdirAll(filepath.Join(root, "old"), 0o755))
    require.NoError(t, os.MkdirAll(filepath.Join(root, "fresh"), 0o755))

    s := &Sweeper{
        Root:      root,
        MaxAge:    14 * 24 * time.Hour,
        Client:    &Client{r: fr},
        ListLocked: func() map[string]bool { return nil }, // nothing locked
    }
    evicted, err := s.SweepOnce(context.Background())
    require.NoError(t, err)
    require.Equal(t, []string{"old"}, evicted)

    _, err = os.Stat(filepath.Join(root, "old"))
    require.ErrorIs(t, err, os.ErrNotExist)
    _, err = os.Stat(filepath.Join(root, "fresh"))
    require.NoError(t, err)
}

func TestSweeper_PressureEviction(t *testing.T) {
    root := t.TempDir()
    fr := newFakeP4Fixture()
    fr.set("client -d relay_h_a", "Client deleted.\n")

    reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
    reg.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/a",
        ClientName: "relay_h_a", LastUsedAt: time.Now().Add(-5 * time.Hour)})
    reg.Upsert(WorkspaceEntry{ShortID: "b", SourceKey: "//s/b",
        ClientName: "relay_h_b", LastUsedAt: time.Now().Add(-1 * time.Hour)})
    require.NoError(t, reg.Save())

    var freeGB int64 = 50 // below threshold
    s := &Sweeper{
        Root: root, MinFreeGB: 100, Client: &Client{r: fr},
        FreeDiskGB:  func(string) (int64, error) { return freeGB, nil },
        ListLocked:  func() map[string]bool { return nil },
        OnEvictedCB: func(string) { freeGB = 200 }, // simulates disk freed
    }
    evicted, err := s.SweepOnce(context.Background())
    require.NoError(t, err)
    require.Equal(t, []string{"a"}, evicted) // oldest first
}

func TestSweeper_SkipsLockedWorkspaces(t *testing.T) {
    root := t.TempDir()
    fr := newFakeP4Fixture()

    reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
    reg.Upsert(WorkspaceEntry{ShortID: "locked", SourceKey: "//s/x",
        ClientName: "relay_h_locked", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
    require.NoError(t, reg.Save())

    s := &Sweeper{
        Root: root, MaxAge: 14 * 24 * time.Hour, Client: &Client{r: fr},
        ListLocked: func() map[string]bool { return map[string]bool{"locked": true} },
    }
    evicted, err := s.SweepOnce(context.Background())
    require.NoError(t, err)
    require.Empty(t, evicted)
    require.Empty(t, fr.argHistory(), "must not call p4 on locked workspaces")
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/source/perforce/... -run TestSweeper -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/agent/source/perforce/sweeper.go`:

```go
package perforce

import (
    "context"
    "os"
    "path/filepath"
    "sort"
    "time"
)

type Sweeper struct {
    Root         string
    MaxAge       time.Duration
    MinFreeGB    int64
    SweepInterval time.Duration
    Client       *Client

    // ListLocked returns short_ids of workspaces currently held under the
    // arbitration lock; never evict these.
    ListLocked   func() map[string]bool
    FreeDiskGB   func(root string) (int64, error)
    OnEvictedCB  func(shortID string)
}

func (s *Sweeper) Run(ctx context.Context) {
    if s.MaxAge == 0 && s.MinFreeGB == 0 {
        return
    }
    interval := s.SweepInterval
    if interval == 0 {
        interval = 15 * time.Minute
    }
    t := time.NewTicker(interval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            _, _ = s.SweepOnce(ctx)
        }
    }
}

func (s *Sweeper) SweepOnce(ctx context.Context) ([]string, error) {
    reg, err := LoadRegistry(filepath.Join(s.Root, ".relay-registry.json"))
    if err != nil {
        return nil, err
    }
    locked := map[string]bool{}
    if s.ListLocked != nil {
        locked = s.ListLocked()
    }

    candidates := make([]WorkspaceEntry, 0, len(reg.Workspaces))
    for _, w := range reg.Workspaces {
        if !locked[w.ShortID] {
            candidates = append(candidates, w)
        }
    }
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].LastUsedAt.Before(candidates[j].LastUsedAt)
    })

    var evicted []string
    now := time.Now()

    // Age pass
    if s.MaxAge > 0 {
        for _, w := range candidates {
            if now.Sub(w.LastUsedAt) > s.MaxAge {
                if err := s.evict(ctx, reg, w); err != nil {
                    return evicted, err
                }
                evicted = append(evicted, w.ShortID)
            }
        }
    }

    // Pressure pass
    if s.MinFreeGB > 0 && s.FreeDiskGB != nil {
        for _, w := range candidates {
            // skip if already evicted in age pass
            if reg.Get(w.ShortID) == nil {
                continue
            }
            free, err := s.FreeDiskGB(s.Root)
            if err != nil {
                return evicted, err
            }
            if free >= s.MinFreeGB {
                break
            }
            if err := s.evict(ctx, reg, w); err != nil {
                return evicted, err
            }
            evicted = append(evicted, w.ShortID)
        }
    }
    return evicted, nil
}

func (s *Sweeper) evict(ctx context.Context, reg *Registry, w WorkspaceEntry) error {
    if err := s.Client.DeleteClient(ctx, w.ClientName); err != nil {
        // Don't progress to disk removal — preserves any unowned pending CLs.
        return err
    }
    if err := os.RemoveAll(filepath.Join(s.Root, w.ShortID)); err != nil {
        _ = reg.MarkDirtyDelete(w.ShortID, true)
        _ = reg.Save()
        return err
    }
    reg.Remove(w.ShortID)
    if err := reg.Save(); err != nil {
        return err
    }
    if s.OnEvictedCB != nil {
        s.OnEvictedCB(w.ShortID)
    }
    return nil
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/agent/source/perforce/... -run TestSweeper -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/sweeper.go internal/agent/source/perforce/sweeper_test.go
git commit -m "feat(agent/source/perforce): age + disk-pressure eviction sweeper"
```

---

## Phase 5 — Agent integration

### Task 14: handleDispatch invokes Provider, emits PREPARING / PREPARE_FAILED

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/runner.go`
- Modify: `internal/agent/runner_test.go`

- [ ] **Step 1: Write failing test**

Append to `runner_test.go`:

```go
// fakeProvider implements source.Provider for runner tests.
type fakeProvider struct {
    prepareErr error
    handle     source.Handle
}
func (f *fakeProvider) Type() string { return "perforce" }
func (f *fakeProvider) Prepare(ctx context.Context, taskID string, spec *relayv1.SourceSpec, p func(string)) (source.Handle, error) {
    if f.prepareErr != nil {
        return nil, f.prepareErr
    }
    p("simulated sync line")
    return f.handle, nil
}

type fakeHandle struct {
    dir       string
    finalized bool
}
func (h *fakeHandle) WorkingDir() string                { return h.dir }
func (h *fakeHandle) Env() map[string]string            { return map[string]string{"P4CLIENT": "fake"} }
func (h *fakeHandle) Finalize(ctx context.Context) error { h.finalized = true; return nil }
func (h *fakeHandle) Inventory() source.InventoryEntry {
    return source.InventoryEntry{SourceType: "perforce", SourceKey: "//s/x", ShortID: "abc"}
}

func TestRunner_PrepareEmitsPreparing(t *testing.T) {
    sendCh := make(chan *relayv1.AgentMessage, 16)
    fh := &fakeHandle{dir: t.TempDir()}
    prov := &fakeProvider{handle: fh}

    task := &relayv1.DispatchTask{
        TaskId: "t1", JobId: "j1",
        Command: echoTaskCmd(),
        Source:  &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
            Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
        }},
    }

    r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
    r.SetProviderForTest(prov)
    r.Run(runCtx, task)

    // Drain sendCh and assert order: PREPARING (with prepare log), then RUNNING, then DONE.
    var phases []relayv1.TaskStatus
    var sawPrepareLog bool
    drain := func() {
        for {
            select {
            case m := <-sendCh:
                if ts := m.GetTaskStatus(); ts != nil {
                    phases = append(phases, ts.Status)
                }
                if log := m.GetTaskLog(); log != nil && log.Stream == relayv1.LogStream_LOG_STREAM_PREPARE {
                    sawPrepareLog = true
                }
            default:
                return
            }
        }
    }
    drain()
    require.Equal(t, []relayv1.TaskStatus{
        relayv1.TaskStatus_TASK_STATUS_PREPARING,
        relayv1.TaskStatus_TASK_STATUS_RUNNING,
        relayv1.TaskStatus_TASK_STATUS_DONE,
    }, phases)
    require.True(t, sawPrepareLog)
    require.True(t, fh.finalized)
}

func TestRunner_PrepareFailureEmitsPrepareFailed(t *testing.T) {
    sendCh := make(chan *relayv1.AgentMessage, 16)
    prov := &fakeProvider{prepareErr: errors.New("p4 unreachable")}

    task := &relayv1.DispatchTask{
        TaskId: "t1", JobId: "j1",
        Command: echoTaskCmd(),
        Source:  &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
            Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
        }},
    }

    r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
    r.SetProviderForTest(prov)
    r.Run(runCtx, task)

    var phases []relayv1.TaskStatus
    for {
        select {
        case m := <-sendCh:
            if ts := m.GetTaskStatus(); ts != nil {
                phases = append(phases, ts.Status)
            }
        default:
            require.Equal(t, []relayv1.TaskStatus{
                relayv1.TaskStatus_TASK_STATUS_PREPARING,
                relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
            }, phases)
            return
        }
    }
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/... -run TestRunner_Prepare -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

In `runner.go`, give `Runner` a provider and a working-dir:

```go
type Runner struct {
    // ... existing fields ...
    provider source.Provider
}

// SetProviderForTest is a test hook (no build tag — keep parallel with the
// existing pattern in internal/cli where override fns are package-level).
func (r *Runner) SetProviderForTest(p source.Provider) { r.provider = p }
```

Replace the early portion of `Run`:

```go
func (r *Runner) Run(ctx context.Context, task *relayv1.DispatchTask) {
    defer r.cancel()

    // 1) Prepare phase, if a source is set.
    var workDir string
    var extraEnv map[string]string
    if task.Source != nil && r.provider != nil {
        r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskStatus{
            TaskStatus: &relayv1.TaskStatusUpdate{
                TaskId: r.taskID, Status: relayv1.TaskStatus_TASK_STATUS_PREPARING, Epoch: r.epoch,
            },
        }})
        progress := r.makePrepareProgressFn()
        handle, err := r.provider.Prepare(ctx, r.taskID, task.Source, progress)
        if err != nil {
            r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskStatus{
                TaskStatus: &relayv1.TaskStatusUpdate{
                    TaskId: r.taskID, Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
                    ErrorMessage: err.Error(), Epoch: r.epoch,
                },
            }})
            return
        }
        defer func() {
            if err := handle.Finalize(r.ctx); err != nil {
                log.Printf("runner: finalize failed for %s: %v", r.taskID, err)
            }
            // Emit inventory update on success.
            r.sendInventory(handle.Inventory())
        }()
        workDir = handle.WorkingDir()
        extraEnv = handle.Env()
    }

    // 2) Existing command exec, with workDir + extraEnv merged.
    if len(task.Command) == 0 {
        r.sendFinalStatus(relayv1.TaskStatus_TASK_STATUS_FAILED, nil)
        return
    }
    env := os.Environ()
    for k, v := range task.Env {
        env = append(env, k+"="+v)
    }
    for k, v := range extraEnv {
        env = append(env, k+"="+v)
    }
    cmd := exec.CommandContext(ctx, task.Command[0], task.Command[1:]...)
    cmd.WaitDelay = 5 * time.Second
    cmd.Env = env
    if workDir != "" {
        cmd.Dir = workDir
    }
    // ... rest of existing Run unchanged ...
}

func (r *Runner) makePrepareProgressFn() func(line string) {
    var buf []string
    var lastFlush time.Time
    var mu sync.Mutex
    flush := func() {
        if len(buf) == 0 {
            return
        }
        content := []byte(strings.Join(buf, "\n") + "\n")
        buf = nil
        lastFlush = time.Now()
        r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskLog{
            TaskLog: &relayv1.TaskLogChunk{
                TaskId: r.taskID, Stream: relayv1.LogStream_LOG_STREAM_PREPARE,
                Content: content, Epoch: r.epoch,
            },
        }})
    }
    return func(line string) {
        mu.Lock()
        defer mu.Unlock()
        buf = append(buf, line)
        if time.Since(lastFlush) > 500*time.Millisecond {
            flush()
        }
    }
}

func (r *Runner) sendInventory(e source.InventoryEntry) {
    // Implemented in Task 15. Stubbed here as a no-op in case Task 15 lands later.
}
```

In `agent.go` `NewAgent`, accept a provider:

```go
func NewAgent(coord string, caps Capabilities, workerID string, creds *Credentials, saveID func(string) error, provider source.Provider) *Agent {
    return &Agent{
        // ... existing init ...
        provider: provider,
    }
}
```

Field on `Agent`:

```go
provider source.Provider
```

In `handleDispatch`, after creating the runner:

```go
runner.provider = a.provider
```

Update `cmd/relay-agent/main.go` to construct the Provider:

```go
import "relay/internal/agent/source"
import "relay/internal/agent/source/perforce"

// In main, after capabilities and creds:
sourceProvider := perforce.New(perforce.Config{
    Root:     os.Getenv("RELAY_WORKSPACE_ROOT"),
    Hostname: caps.Hostname,
})
agent := agent.NewAgent(coord, caps, workerID, creds, saveID, sourceProvider)
```

If `RELAY_WORKSPACE_ROOT` is empty, pass `nil` provider — tasks without a `source` continue to work; tasks with a `source` will fail in Prepare with a clear error.

Update existing test call sites that construct `agent.NewAgent` to pass `nil` as the provider argument.

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/agent/... -v -timeout 60s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go internal/agent/runner.go internal/agent/runner_test.go cmd/relay-agent/main.go
git commit -m "feat(agent): wire source provider into runner with PREPARING phase"
```

---

### Task 15: Agent reports WorkspaceInventoryUpdate; RegisterRequest carries inventory

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/runner.go`
- Modify: `internal/agent/agent_test.go`

- [ ] **Step 1: Write failing test**

Append to `agent_test.go`:

```go
func TestAgent_BuildRegisterRequest_IncludesInventory(t *testing.T) {
    root := t.TempDir()
    reg, _ := perforce.LoadRegistry(filepath.Join(root, ".relay-registry.json"))
    reg.Upsert(perforce.WorkspaceEntry{
        ShortID: "abcdef", SourceKey: "//s/x", ClientName: "relay_h_abcdef",
        BaselineHash: "deadbeef", LastUsedAt: time.Now(),
    })
    require.NoError(t, reg.Save())

    p := perforce.New(perforce.Config{Root: root, Hostname: "h"})
    a := NewAgent("addr", Capabilities{Hostname: "h"}, "", nil, func(string) error { return nil }, p)
    req, err := a.buildRegisterRequest()
    require.NoError(t, err)
    require.Len(t, req.Inventory, 1)
    require.Equal(t, "//s/x", req.Inventory[0].SourceKey)
    require.Equal(t, "abcdef", req.Inventory[0].ShortId)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/agent/... -run TestAgent_BuildRegisterRequest_IncludesInventory -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

Add an `InventoryLister` to the Provider interface (avoid duplicating registry read logic):

`internal/agent/source/source.go`:

```go
type InventoryLister interface {
    ListInventory() ([]InventoryEntry, error)
}
```

`internal/agent/source/perforce/perforce.go`:

```go
func (p *Provider) ListInventory() ([]source.InventoryEntry, error) {
    reg, err := p.loadRegistry()
    if err != nil {
        return nil, err
    }
    out := make([]source.InventoryEntry, 0, len(reg.Workspaces))
    for _, w := range reg.Workspaces {
        out = append(out, source.InventoryEntry{
            SourceType:   "perforce",
            SourceKey:    w.SourceKey,
            ShortID:      w.ShortID,
            BaselineHash: w.BaselineHash,
            LastUsedAt:   w.LastUsedAt,
        })
    }
    return out, nil
}
```

In `agent.go` `buildRegisterRequest`:

```go
if il, ok := a.provider.(source.InventoryLister); ok {
    inv, err := il.ListInventory()
    if err == nil {
        for _, e := range inv {
            req.Inventory = append(req.Inventory, &relayv1.WorkspaceInventoryUpdate{
                SourceType:   e.SourceType,
                SourceKey:    e.SourceKey,
                ShortId:      e.ShortID,
                BaselineHash: e.BaselineHash,
                LastUsedAt:   timestamppb.New(e.LastUsedAt),
            })
        }
    }
}
```

Implement `Runner.sendInventory` (stub from Task 14):

```go
func (r *Runner) sendInventory(e source.InventoryEntry) {
    r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_WorkspaceInventory{
        WorkspaceInventory: &relayv1.WorkspaceInventoryUpdate{
            SourceType:   e.SourceType,
            SourceKey:    e.SourceKey,
            ShortId:      e.ShortID,
            BaselineHash: e.BaselineHash,
            LastUsedAt:   timestamppb.New(e.LastUsedAt),
            Deleted:      e.Deleted,
        },
    }})
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./internal/agent/... -v -timeout 60s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/source.go internal/agent/source/perforce/perforce.go internal/agent/agent.go internal/agent/runner.go internal/agent/agent_test.go
git commit -m "feat(agent): emit WorkspaceInventoryUpdate and include inventory in RegisterRequest"
```

---

## Phase 6 — Server inventory and dispatcher

### Task 16: Server handles WorkspaceInventoryUpdate and reconciles inventory on register

**Files:**
- Modify: `internal/worker/handler.go`
- Modify: `internal/worker/handler_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestHandler_RegisterReplacesWorkerInventory(t *testing.T) {
    q, ctx := newTestHandlerEnv(t)
    h := NewHandlerForTest(q)

    worker := makeTestWorker(t, q, ctx, "h")
    require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
        WorkerID: worker.ID, SourceType: "perforce", SourceKey: "//old", ShortID: "old",
        BaselineHash: "x", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
    }))

    req := &relayv1.RegisterRequest{
        WorkerId: worker.ID.String(), Hostname: "h",
        Inventory: []*relayv1.WorkspaceInventoryUpdate{
            {SourceType: "perforce", SourceKey: "//new", ShortId: "n",
             BaselineHash: "y", LastUsedAt: timestamppb.Now()},
        },
    }
    require.NoError(t, h.applyInventory(ctx, worker.ID, req.Inventory))

    rows, _ := q.ListWorkerWorkspaces(ctx, worker.ID)
    require.Len(t, rows, 1)
    require.Equal(t, "//new", rows[0].SourceKey)
}

func TestHandler_WorkspaceInventoryUpdate_Apply(t *testing.T) {
    q, ctx := newTestHandlerEnv(t)
    h := NewHandlerForTest(q)
    worker := makeTestWorker(t, q, ctx, "h")

    upd := &relayv1.WorkspaceInventoryUpdate{
        SourceType: "perforce", SourceKey: "//s/x", ShortId: "abc",
        BaselineHash: "xyz", LastUsedAt: timestamppb.Now(),
    }
    require.NoError(t, h.applyInventoryUpdate(ctx, worker.ID, upd))
    rows, _ := q.ListWorkerWorkspaces(ctx, worker.ID)
    require.Len(t, rows, 1)

    upd.Deleted = true
    require.NoError(t, h.applyInventoryUpdate(ctx, worker.ID, upd))
    rows, _ = q.ListWorkerWorkspaces(ctx, worker.ID)
    require.Empty(t, rows)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test -tags integration -p 1 ./internal/worker/... -run TestHandler -v -timeout 120s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

Add to `handler.go`:

```go
func (h *Handler) applyInventory(ctx context.Context, workerID pgtype.UUID, inv []*relayv1.WorkspaceInventoryUpdate) error {
    return pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
        q := h.q.WithTx(tx)
        if err := q.ReplaceWorkerInventory(ctx, workerID); err != nil {
            return err
        }
        for _, u := range inv {
            if u.Deleted {
                continue
            }
            if err := q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
                WorkerID:     workerID,
                SourceType:   u.SourceType,
                SourceKey:    u.SourceKey,
                ShortID:      u.ShortId,
                BaselineHash: u.BaselineHash,
                LastUsedAt:   pgtype.Timestamptz{Time: u.LastUsedAt.AsTime(), Valid: true},
            }); err != nil {
                return err
            }
        }
        return nil
    })
}

func (h *Handler) applyInventoryUpdate(ctx context.Context, workerID pgtype.UUID, u *relayv1.WorkspaceInventoryUpdate) error {
    if u.Deleted {
        return h.q.DeleteWorkerWorkspace(ctx, store.DeleteWorkerWorkspaceParams{
            WorkerID: workerID, SourceType: u.SourceType, SourceKey: u.SourceKey,
        })
    }
    return h.q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
        WorkerID:     workerID,
        SourceType:   u.SourceType,
        SourceKey:    u.SourceKey,
        ShortID:      u.ShortId,
        BaselineHash: u.BaselineHash,
        LastUsedAt:   pgtype.Timestamptz{Time: u.LastUsedAt.AsTime(), Valid: true},
    })
}
```

In the message-receive loop where existing `TaskStatusUpdate` and `TaskLogChunk` payloads are dispatched, add a branch:

```go
case *relayv1.AgentMessage_WorkspaceInventory:
    if err := h.applyInventoryUpdate(ctx, workerID, p.WorkspaceInventory); err != nil {
        log.Printf("worker: inventory update failed: %v", err)
    }
```

In `finishRegister` (or wherever `RegisterRequest` is processed), call:

```go
if err := h.applyInventory(ctx, workerID, req.Inventory); err != nil {
    log.Printf("worker: register inventory replace failed: %v", err)
}
```

The handler needs a `*pgxpool.Pool` for the transaction; pass it through `NewHandler` if not already available, mirroring how the existing transaction-using paths work.

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -tags integration -p 1 ./internal/worker/... -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_test.go
git commit -m "feat(worker): persist workspace inventory on register and per-update"
```

---

### Task 17: Dispatcher warm-preference scoring

**Files:**
- Modify: `internal/scheduler/dispatch.go`
- Modify: `internal/scheduler/dispatch_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestDispatcher_PrefersWarmWorker(t *testing.T) {
    ctx := context.Background()
    q := newTestQueries(t)
    user := makeTestUser(t, q, ctx, "S", "s@x")
    job := makeTestJob(t, q, ctx, user.ID)

    src := []byte(`{"type":"perforce","stream":"//s/x","sync":[{"path":"//s/x/...","rev":"#head"}]}`)
    task, err := q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
        JobID: job.ID, Name: "t", Command: []string{"true"},
        Env: []byte(`{}`), Requires: []byte(`{}`), Source: src,
    })
    require.NoError(t, err)

    cold := makeTestWorkerOnline(t, q, ctx, "cold-and-bigger")
    warm := makeTestWorkerOnline(t, q, ctx, "warm-but-smaller")

    // cold has 8 free slots, warm has 1 — without scoring, cold would win.
    require.NoError(t, q.SetWorkerSlots(ctx, store.SetWorkerSlotsParams{ID: cold.ID, MaxSlots: 8}))
    require.NoError(t, q.SetWorkerSlots(ctx, store.SetWorkerSlotsParams{ID: warm.ID, MaxSlots: 1}))

    require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
        WorkerID: warm.ID, SourceType: "perforce", SourceKey: "//s/x", ShortID: "abc",
        BaselineHash: "ignored", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
    }))

    captured := make(chan pgtype.UUID, 1)
    reg := captureRegistry(t, captured)
    d := scheduler.NewDispatcher(q, reg, events.NewBroker())
    d.RunOnce(ctx)
    chosen := <-captured
    require.Equal(t, warm.ID, chosen)

    _ = task
}

func TestDispatcher_ColdFallback_NoWarmWorker(t *testing.T) {
    // Symmetric setup but no warm row exists; dispatcher falls back to free-slot winner.
    // ... same shape as above, omitting the UpsertWorkerWorkspace call ...
}
```

(`SetWorkerSlots`, `captureRegistry` are conventional helpers; if absent, follow patterns in existing dispatch tests.)

- [ ] **Step 2: Run test to verify failure**

```bash
go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_PrefersWarmWorker -v -timeout 120s
```

Expected: FAIL — cold worker wins on free slots.

- [ ] **Step 3: Implement**

In `dispatch.go`, change `dispatch` to compute `warmByWorker` once per cycle:

```go
func (d *Dispatcher) dispatch(ctx context.Context) {
    // ... existing setup: load workers, reservations, eligibleTasks ...

    // Build the set of distinct (source_type, source_key) tuples.
    type key struct{ Type, K string }
    keysByType := make(map[string][]string)
    for _, t := range eligibleTasks {
        if len(t.Source) == 0 {
            continue
        }
        var s api.SourceSpec
        if err := json.Unmarshal(t.Source, &s); err != nil {
            continue
        }
        keysByType[s.Type] = append(keysByType[s.Type], s.Stream)
    }
    warmByWorker := make(map[pgtype.UUID][]store.WorkerWorkspace)
    for typ, ks := range keysByType {
        rows, err := d.q.ListWarmWorkspacesForKeys(ctx, store.ListWarmWorkspacesForKeysParams{
            SourceType: typ, SourceKey: ks,
        })
        if err == nil {
            for _, w := range rows {
                warmByWorker[w.WorkerID] = append(warmByWorker[w.WorkerID], w)
            }
        }
    }

    for _, t := range eligibleTasks {
        w := d.selectWorker(t, workers, reservations, activeByWorker, warmByWorker)
        // ... existing dispatch path ...
    }
}
```

Update `selectWorker` signature and scoring:

```go
func (d *Dispatcher) selectWorker(
    task store.Task,
    workers []store.Worker,
    reservations []store.Reservation,
    activeByWorker map[pgtype.UUID]int64,
    warmByWorker map[pgtype.UUID][]store.WorkerWorkspace,
) *store.Worker {
    var best *store.Worker
    var bestScore int64 = -1

    var taskSrc *api.SourceSpec
    if len(task.Source) > 0 {
        var s api.SourceSpec
        if err := json.Unmarshal(task.Source, &s); err == nil {
            taskSrc = &s
        }
    }

    for i := range workers {
        w := &workers[i]
        if !d.isEligible(w, task, reservations, activeByWorker) {
            continue
        }
        free := int64(w.MaxSlots) - activeByWorker[w.ID]
        score := free
        if taskSrc != nil {
            for _, ws := range warmByWorker[w.ID] {
                if ws.SourceType == taskSrc.Type && ws.SourceKey == taskSrc.Stream {
                    estimate := perforce.BaselineHashFromAPISpec(taskSrc) // see below
                    if ws.BaselineHash == estimate {
                        score += 10_000
                    } else {
                        score += 1_000
                    }
                    break
                }
            }
        }
        if score > bestScore {
            bestScore = score
            best = w
        }
    }
    return best
}
```

Extract the existing filter logic into `isEligible` so `selectWorker` is cleaner; `isEligible` returns true for online + not reserved + label-match + free slot.

Add a server-side baseline-hash helper that mirrors agent-side canonicalization but treats `#head` as a sentinel (no resolution available server-side):

```go
// internal/agent/source/perforce/baseline.go (already implemented in Task 8)
// Add a wrapper that takes the api.SourceSpec form:
func BaselineHashFromAPISpec(s *api.SourceSpec) string {
    if s == nil || s.Type != "perforce" {
        return ""
    }
    p := &relayv1.PerforceSource{Stream: s.Stream, Unshelves: s.Unshelves}
    for _, e := range s.Sync {
        p.Sync = append(p.Sync, &relayv1.SyncEntry{Path: e.Path, Rev: e.Rev})
    }
    return BaselineHash(p, nil)
}
```

(If the server's import graph dislikes pulling in `internal/agent/source/perforce`, move the canonicalization helpers to `internal/source/perforce` and have both sides import that.)

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -tags integration -p 1 ./internal/scheduler/... -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/dispatch.go internal/scheduler/dispatch_test.go internal/agent/source/perforce/baseline.go
git commit -m "feat(scheduler): warm-preference scoring in selectWorker"
```

---

## Phase 7 — Operator surfaces

### Task 18: HTTP endpoints — list workspaces and trigger eviction

**Files:**
- Create: `internal/api/workspaces.go`
- Create: `internal/api/workspaces_test.go`
- Modify: `internal/api/server.go` (route registration)
- Modify: `internal/worker/handler.go` (add `EvictWorkspace` send method)

- [ ] **Step 1: Write failing test**

```go
func TestListWorkerWorkspaces_AdminOnly(t *testing.T) {
    srv, q, ctx := newAPITestServer(t)
    user, _ := makeTestUserAndToken(t, q, ctx, "regular@x", false)
    admin, adminTok := makeTestUserAndToken(t, q, ctx, "admin@x", true)

    worker := makeTestWorker(t, q, ctx, "h")
    require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
        WorkerID: worker.ID, SourceType: "perforce", SourceKey: "//s/x", ShortID: "abc",
        BaselineHash: "deadbeef", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
    }))
    _ = user

    rec := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/v1/workers/"+worker.ID.String()+"/workspaces", nil)
    req.Header.Set("Authorization", "Bearer "+adminTok)
    srv.Handler().ServeHTTP(rec, req)
    require.Equal(t, 200, rec.Code)
    require.Contains(t, rec.Body.String(), `"//s/x"`)
    _ = admin
}

func TestEvictWorkerWorkspace_SendsCommandAndDeletes(t *testing.T) {
    srv, q, ctx := newAPITestServer(t)
    _, adminTok := makeTestUserAndToken(t, q, ctx, "a@x", true)
    worker := makeTestWorker(t, q, ctx, "h")
    require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
        WorkerID: worker.ID, SourceType: "perforce", SourceKey: "//s/x", ShortID: "abc",
        BaselineHash: "x", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
    }))

    rec := httptest.NewRecorder()
    req := httptest.NewRequest("POST", "/v1/workers/"+worker.ID.String()+"/workspaces/abc/evict", nil)
    req.Header.Set("Authorization", "Bearer "+adminTok)
    srv.Handler().ServeHTTP(rec, req)
    require.Equal(t, 202, rec.Code)
    // The DB row is left in place until the agent confirms via inventory update.
    rows, _ := q.ListWorkerWorkspaces(ctx, worker.ID)
    require.Len(t, rows, 1)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestListWorkerWorkspaces -v -timeout 120s
go test -tags integration -p 1 ./internal/api/... -run TestEvictWorkerWorkspace -v -timeout 120s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/api/workspaces.go`:

```go
package api

import (
    "encoding/json"
    "net/http"
    "time"

    "relay/internal/store"
    relayv1 "relay/internal/proto/relayv1"

    "github.com/go-chi/chi/v5"
)

type workspaceJSON struct {
    SourceType   string    `json:"source_type"`
    SourceKey    string    `json:"source_key"`
    ShortID      string    `json:"short_id"`
    BaselineHash string    `json:"baseline_hash"`
    LastUsedAt   time.Time `json:"last_used_at"`
}

func (s *Server) handleListWorkerWorkspaces(w http.ResponseWriter, r *http.Request) {
    workerID, err := parseUUIDParam(r, "id")
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    rows, err := s.q.ListWorkerWorkspaces(r.Context(), workerID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    out := make([]workspaceJSON, 0, len(rows))
    for _, row := range rows {
        out = append(out, workspaceJSON{
            SourceType:   row.SourceType,
            SourceKey:    row.SourceKey,
            ShortID:      row.ShortID,
            BaselineHash: row.BaselineHash,
            LastUsedAt:   row.LastUsedAt.Time,
        })
    }
    _ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleEvictWorkerWorkspace(w http.ResponseWriter, r *http.Request) {
    workerID, err := parseUUIDParam(r, "id")
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    shortID := chi.URLParam(r, "short_id")
    rows, err := s.q.ListWorkerWorkspaces(r.Context(), workerID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    var found *store.WorkerWorkspace
    for i, row := range rows {
        if row.ShortID == shortID {
            found = &rows[i]
            break
        }
    }
    if found == nil {
        http.Error(w, "workspace not found", http.StatusNotFound)
        return
    }
    cmd := &relayv1.EvictWorkspaceCommand{SourceType: found.SourceType, ShortId: shortID}
    if err := s.workerSender.SendEvictCommand(workerID, cmd); err != nil {
        http.Error(w, err.Error(), http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusAccepted)
}
```

In `server.go`, register routes inside the existing admin chain:

```go
r.With(BearerAuth, AdminOnly).Get("/v1/workers/{id}/workspaces", s.handleListWorkerWorkspaces)
r.With(BearerAuth, AdminOnly).Post("/v1/workers/{id}/workspaces/{short_id}/evict", s.handleEvictWorkerWorkspace)
```

In `internal/worker/registry.go` (the `Registry` or `SenderRegistry`), add:

```go
func (r *SenderRegistry) SendEvictCommand(workerID pgtype.UUID, cmd *relayv1.EvictWorkspaceCommand) error {
    return r.sendToWorker(workerID, &relayv1.CoordinatorMessage{
        Payload: &relayv1.CoordinatorMessage_EvictWorkspace{EvictWorkspace: cmd},
    })
}
```

In agent `connect()` recv-loop, handle the new payload:

```go
case *relayv1.CoordinatorMessage_EvictWorkspace:
    if il, ok := a.provider.(interface {
        EvictWorkspace(ctx context.Context, shortID string) error
    }); ok {
        go func() {
            if err := il.EvictWorkspace(a.runCtx, p.EvictWorkspace.ShortId); err != nil {
                log.Printf("agent: evict %s failed: %v", p.EvictWorkspace.ShortId, err)
            }
        }()
    }
```

Add to `perforce.Provider`:

```go
func (p *Provider) EvictWorkspace(ctx context.Context, shortID string) error {
    s := &Sweeper{Root: p.cfg.Root, Client: p.cfg.Client,
        ListLocked: p.lockedShortIDs}
    reg, err := LoadRegistry(filepath.Join(p.cfg.Root, ".relay-registry.json"))
    if err != nil {
        return err
    }
    e := reg.Get(shortID)
    if e == nil {
        return fmt.Errorf("workspace %s not found", shortID)
    }
    return s.evict(ctx, reg, *e)
}

func (p *Provider) lockedShortIDs() map[string]bool {
    p.mu.Lock()
    defer p.mu.Unlock()
    out := map[string]bool{}
    for id, ws := range p.workspaces {
        ws.mu.Lock()
        if len(ws.holders) > 0 {
            out[id] = true
        }
        ws.mu.Unlock()
    }
    return out
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -tags integration -p 1 ./internal/api/... ./internal/worker/... ./internal/agent/source/... -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/workspaces.go internal/api/server.go internal/api/workspaces_test.go internal/worker/registry.go internal/agent/agent.go internal/agent/source/perforce/perforce.go
git commit -m "feat(api,agent): list and evict worker workspaces via admin endpoints"
```

---

### Task 19: CLI subcommands — `workers workspaces` and `workers evict-workspace`

**Files:**
- Create: `internal/cli/workers_workspaces.go`
- Create: `internal/cli/workers_workspaces_test.go`
- Modify: `internal/cli/cli.go` (register subcommands)

- [ ] **Step 1: Write failing test**

```go
func TestCLI_WorkersWorkspaces_Lists(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        require.Equal(t, "/v1/workers/00000000-0000-0000-0000-000000000001/workspaces", r.URL.Path)
        require.Equal(t, "Bearer testtok", r.Header.Get("Authorization"))
        _, _ = w.Write([]byte(`[{"source_type":"perforce","source_key":"//s/x","short_id":"abc","baseline_hash":"deadbeef","last_used_at":"2026-04-24T00:00:00Z"}]`))
    }))
    defer srv.Close()

    var out bytes.Buffer
    cli.SetOutputForTest(&out)
    err := cli.Dispatch([]string{"workers", "workspaces", "00000000-0000-0000-0000-000000000001"},
        cli.Config{URL: srv.URL, Token: "testtok"})
    require.NoError(t, err)
    require.Contains(t, out.String(), "//s/x")
    require.Contains(t, out.String(), "abc")
}

func TestCLI_WorkersEvictWorkspace_PostsAndPrints(t *testing.T) {
    var called bool
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        require.Equal(t, "POST", r.Method)
        require.Contains(t, r.URL.Path, "/workspaces/abc/evict")
        called = true
        w.WriteHeader(202)
    }))
    defer srv.Close()
    err := cli.Dispatch([]string{"workers", "evict-workspace",
        "00000000-0000-0000-0000-000000000001", "abc"},
        cli.Config{URL: srv.URL, Token: "testtok"})
    require.NoError(t, err)
    require.True(t, called)
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/cli/... -run TestCLI_Workers -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/cli/workers_workspaces.go`:

```go
package cli

import (
    "encoding/json"
    "flag"
    "fmt"
    "net/http"
    "text/tabwriter"
    "time"
)

func cmdWorkersWorkspaces(args []string, cfg Config) error {
    fs := flag.NewFlagSet("workers workspaces", flag.ContinueOnError)
    if err := fs.Parse(args); err != nil {
        return err
    }
    if fs.NArg() != 1 {
        return fmt.Errorf("usage: relay workers workspaces <worker-id>")
    }
    workerID := fs.Arg(0)

    req, _ := http.NewRequest("GET", cfg.URL+"/v1/workers/"+workerID+"/workspaces", nil)
    req.Header.Set("Authorization", "Bearer "+cfg.Token)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return fmt.Errorf("server returned %d", resp.StatusCode)
    }
    var rows []struct {
        SourceType   string    `json:"source_type"`
        SourceKey    string    `json:"source_key"`
        ShortID      string    `json:"short_id"`
        BaselineHash string    `json:"baseline_hash"`
        LastUsedAt   time.Time `json:"last_used_at"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
        return err
    }
    tw := tabwriter.NewWriter(out(), 0, 2, 2, ' ', 0)
    fmt.Fprintln(tw, "SHORT_ID\tSOURCE\tBASELINE\tLAST_USED")
    for _, row := range rows {
        fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.ShortID, row.SourceKey, row.BaselineHash, row.LastUsedAt.Format(time.RFC3339))
    }
    return tw.Flush()
}

func cmdWorkersEvictWorkspace(args []string, cfg Config) error {
    fs := flag.NewFlagSet("workers evict-workspace", flag.ContinueOnError)
    if err := fs.Parse(args); err != nil {
        return err
    }
    if fs.NArg() != 2 {
        return fmt.Errorf("usage: relay workers evict-workspace <worker-id> <short-id>")
    }
    workerID, shortID := fs.Arg(0), fs.Arg(1)

    req, _ := http.NewRequest("POST",
        fmt.Sprintf("%s/v1/workers/%s/workspaces/%s/evict", cfg.URL, workerID, shortID), nil)
    req.Header.Set("Authorization", "Bearer "+cfg.Token)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusAccepted {
        return fmt.Errorf("server returned %d", resp.StatusCode)
    }
    fmt.Fprintf(out(), "evict requested for %s on worker %s\n", shortID, workerID)
    return nil
}
```

In `cli.go`, register inside the existing `workers` subcommand dispatch:

```go
case "workspaces":
    return cmdWorkersWorkspaces(rest, cfg)
case "evict-workspace":
    return cmdWorkersEvictWorkspace(rest, cfg)
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./internal/cli/... -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/workers_workspaces.go internal/cli/workers_workspaces_test.go internal/cli/cli.go
git commit -m "feat(cli): add 'workers workspaces' and 'workers evict-workspace' subcommands"
```

---

### Task 20: Wire sweeper in relay-agent main; document new env vars

**Files:**
- Modify: `cmd/relay-agent/main.go`
- Modify: `CLAUDE.md`
- Modify: `internal/agent/source/perforce/perforce.go` (expose `Sweeper` accessor)

- [ ] **Step 1: Write failing test**

Append to `cmd/relay-agent/main_test.go` (create if absent):

```go
//go:build !windows  // free-disk syscall test trims to unix; skip on Windows for this task

func TestParseDurationEnv(t *testing.T) {
    require.Equal(t, 14*24*time.Hour, parseDurationEnv("14d", 0))
    require.Equal(t, 5*time.Minute, parseDurationEnv("5m", 0))
    require.Equal(t, 30*time.Second, parseDurationEnv("30s", 0))
    require.Equal(t, time.Hour, parseDurationEnv("garbage", time.Hour))
    require.Equal(t, time.Duration(0), parseDurationEnv("", 0))
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./cmd/relay-agent/... -v -timeout 30s
```

Expected: FAIL.

- [ ] **Step 3: Implement**

`cmd/relay-agent/main.go`:

```go
import (
    "fmt"
    "regexp"
    "strconv"
    "strings"
    "syscall"
    "time"

    "relay/internal/agent/source"
    "relay/internal/agent/source/perforce"
)

var durRe = regexp.MustCompile(`^(\d+)([smhd])$`)

func parseDurationEnv(v string, fallback time.Duration) time.Duration {
    if v == "" {
        return fallback
    }
    m := durRe.FindStringSubmatch(v)
    if m == nil {
        return fallback
    }
    n, _ := strconv.Atoi(m[1])
    switch m[2] {
    case "s":
        return time.Duration(n) * time.Second
    case "m":
        return time.Duration(n) * time.Minute
    case "h":
        return time.Duration(n) * time.Hour
    case "d":
        return time.Duration(n) * 24 * time.Hour
    }
    return fallback
}

// In main(), after sourceProvider is constructed:
maxAge := parseDurationEnv(os.Getenv("RELAY_WORKSPACE_MAX_AGE"), 0)
minFree, _ := strconv.ParseInt(os.Getenv("RELAY_WORKSPACE_MIN_FREE_GB"), 10, 64)
sweepEvery := parseDurationEnv(os.Getenv("RELAY_WORKSPACE_SWEEP_INTERVAL"), 15*time.Minute)
if pp, ok := sourceProvider.(*perforce.Provider); ok && (maxAge > 0 || minFree > 0) {
    sw := &perforce.Sweeper{
        Root: os.Getenv("RELAY_WORKSPACE_ROOT"),
        MaxAge: maxAge, MinFreeGB: minFree, SweepInterval: sweepEvery,
        Client:     pp.Client(),
        ListLocked: pp.LockedShortIDs,
        FreeDiskGB: freeDiskGB,
    }
    go sw.Run(ctx)
}
```

Add `freeDiskGB(root string) (int64, error)` — Linux uses `syscall.Statfs`; Windows uses `GetDiskFreeSpaceExW` via `golang.org/x/sys/windows`. Implement both with a `// +build` (`//go:build linux`) split:

`free_disk_unix.go`:

```go
//go:build !windows
package main

import "syscall"

func freeDiskGB(path string) (int64, error) {
    var s syscall.Statfs_t
    if err := syscall.Statfs(path, &s); err != nil {
        return 0, err
    }
    return int64(s.Bavail) * int64(s.Bsize) / (1024 * 1024 * 1024), nil
}
```

`free_disk_windows.go`:

```go
//go:build windows
package main

import (
    "golang.org/x/sys/windows"
    "unsafe"
)

func freeDiskGB(path string) (int64, error) {
    p, err := windows.UTF16PtrFromString(path)
    if err != nil { return 0, err }
    var freeBytes uint64
    if err := windows.GetDiskFreeSpaceEx(p, &freeBytes, nil, nil); err != nil {
        return 0, err
    }
    _ = unsafe.Sizeof(freeBytes)
    return int64(freeBytes / (1024 * 1024 * 1024)), nil
}
```

Add `Client()` and `LockedShortIDs` accessors on `perforce.Provider`:

```go
func (p *Provider) Client() *Client { return p.cfg.Client }
func (p *Provider) LockedShortIDs() map[string]bool { return p.lockedShortIDs() }
```

In `CLAUDE.md`, add a new row block under "Environment Variables (relay-agent)" — create the section if absent:

```markdown
## Environment Variables (relay-agent)

| Variable | Default | Purpose |
|---|---|---|
| `RELAY_WORKSPACE_ROOT` | _(unset)_ | Absolute path under which Perforce workspaces are created. Required to use `source` on tasks. |
| `RELAY_WORKSPACE_MAX_AGE` | _(unset)_ | Age (e.g. `14d`) past which idle workspaces are evicted. |
| `RELAY_WORKSPACE_MIN_FREE_GB` | _(unset)_ | Free-disk threshold; below this, evict LRU workspaces. |
| `RELAY_WORKSPACE_SWEEP_INTERVAL` | `15m` | Eviction sweep interval. |
```

Also add a "Source providers" section briefly documenting that operators provision P4 tickets out-of-band; relay assumes `p4` works.

- [ ] **Step 4: Run tests to verify pass**

```bash
go test ./cmd/... -v -timeout 30s
make build
```

Expected: PASS, build succeeds on the current platform.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-agent/main.go cmd/relay-agent/main_test.go cmd/relay-agent/free_disk_*.go internal/agent/source/perforce/perforce.go CLAUDE.md
git commit -m "feat(relay-agent): wire workspace sweeper and document env vars"
```

---

## Phase 8 — End-to-end verification

### Task 21: Integration test against a real Perforce server

**Files:**
- Create: `internal/agent/source/perforce/perforce_integration_test.go`

The standard testcontainers-go pattern in this repo (`-tags integration`) extends naturally to a Perforce container; an established choice is `bitnami/perforce-helix-core` or `gerritcodereview/p4d`. Pick one already used in your CI setup if available; otherwise this test gates on `P4_TEST_HOST` env var being set and is `t.Skip`'d in CI without it.

- [ ] **Step 1: Write the test**

```go
//go:build integration

package perforce

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    relayv1 "relay/internal/proto/relayv1"
)

func TestPerforce_E2E_SyncAndUnshelve(t *testing.T) {
    p4port := os.Getenv("P4_TEST_HOST")
    if p4port == "" {
        t.Skip("set P4_TEST_HOST=host:port to run; assumes a depot //test/main exists with one shelved CL referenced by P4_TEST_SHELVED_CL")
    }
    shelved := os.Getenv("P4_TEST_SHELVED_CL")

    t.Setenv("P4PORT", p4port)
    require.NoError(t, exec.Command("p4", "info").Run(), "P4 unreachable")

    root := t.TempDir()
    prov := New(Config{Root: root, Hostname: "ci"})
    spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
        Perforce: &relayv1.PerforceSource{
            Stream: "//test/main",
            Sync:   []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}},
        },
    }}
    if shelved != "" {
        var cl int64
        fmt.Sscanf(shelved, "%d", &cl)
        spec.GetPerforce().Unshelves = []int64{cl}
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    var lines []string
    h, err := prov.Prepare(ctx, "task-1", spec, func(s string) { lines = append(lines, s) })
    require.NoError(t, err)
    require.NotEmpty(t, lines)

    inv := h.Inventory()
    require.Equal(t, "//test/main", inv.SourceKey)

    // Workspace dir exists
    _, err = os.Stat(filepath.Join(root, inv.ShortID))
    require.NoError(t, err)

    // Finalize cleans up pending CLs
    require.NoError(t, h.Finalize(ctx))
    reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
    e := reg.Get(inv.ShortID)
    require.Empty(t, e.OpenTaskChangelists)

    // Re-prepare on identical baseline → no sync required (idempotent)
    h2, err := prov.Prepare(ctx, "task-2", spec, func(s string) {})
    require.NoError(t, err)
    require.NoError(t, h2.Finalize(ctx))
}
```

- [ ] **Step 2: Run with a P4 server available**

```bash
P4_TEST_HOST=localhost:1666 go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E -v -timeout 600s
```

Expected: PASS. CI without P4 will skip.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/source/perforce/perforce_integration_test.go
git commit -m "test(agent/source/perforce): integration test against real P4 server"
```

---

## Final checklist

- [ ] `make test && make test-integration` clean.
- [ ] `make build` produces all three binaries.
- [ ] `relay-agent` started without `RELAY_WORKSPACE_ROOT` still runs tasks that have no `source` field — backward-compatible.
- [ ] `relay-agent` with `RELAY_WORKSPACE_ROOT` set but the operator hasn't installed `p4` — tasks with a `source` produce `PREPARE_FAILED` with a clear error; tasks without remain unaffected.
- [ ] `CLAUDE.md` documents `RELAY_WORKSPACE_ROOT`, `RELAY_WORKSPACE_MAX_AGE`, `RELAY_WORKSPACE_MIN_FREE_GB`, `RELAY_WORKSPACE_SWEEP_INTERVAL`, the new HTTP endpoints, and the new CLI subcommands.
- [ ] Spec `docs/superpowers/specs/2026-04-24-perforce-workspace-management-design.md` matches behavior.
- [ ] Manual smoke: submit a job with a `source` field via `relay jobs submit`, observe `[prepare]` log lines, observe `relay workers workspaces <id>` shows the workspace, observe a second job's task lands on the same worker (warm preference), evict via `relay workers evict-workspace`, observe inventory removed.
