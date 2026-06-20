# Dispatch Provider-Capability Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make workspace-provider capability a hard scheduling constraint so a source-bearing task is only ever dispatched to a worker that reports a workspace provider, and otherwise stays `pending`.

**Architecture:** Workers report a single `supports_workspaces` boolean at registration (proto `optional bool`, agent sets it from `a.provider != nil`). The server persists it to a new `workers.supports_workspaces` column (`DEFAULT TRUE`) on every connect via `RegisterWorkerConnection`, using `COALESCE(narg, existing)` so an old agent that omits the field never overwrites the value (rolling-upgrade safety). `selectWorker` skips providerless workers for source-bearing tasks with one guard before scoring; non-source tasks are unaffected. A nil selection leaves the task pending and is re-attempted on the next dispatch cycle; one throttled log line per cycle surfaces the held condition.

**Tech Stack:** Go, proto3 + buf (`make generate`), sqlc (`make generate`), golang-migrate, pgx/v5, testify, testcontainers-go (integration).

---

## Slice independence

**This is a BACKEND-ONLY change. There is exactly one slice (relay-backend-engineer). Phase 3 is NOT parallel.** There is no frontend work. All tasks below are sequential because later tasks depend on generated bindings from earlier tasks (proto field -> agent + handler use it; SQL column + params -> handler + selectWorker use them).

**Migration number confirmed:** the latest migration on disk is `000016_workers_connection_epoch`. `000017` is the next free number.

## Codegen and invariant notes (read before starting)

- **Two `make generate` triggers, run separately and in order.** A `.proto` edit regenerates `internal/proto/relayv1/relay.pb.go` (buf). A `.sql`/migration edit regenerates `internal/store/*.sql.go` and `internal/store/models.go` (sqlc). Both are produced by the same `make generate` command, so after each codegen-affecting edit run `make generate` once.
- **Never hand-edit generated files:** `internal/proto/relayv1/*.pb.go`, `internal/store/*.sql.go`, `internal/store/models.go`. Edit only `proto/relayv1/relay.proto`, `internal/store/query/*.sql`, and the migration files; regenerate.
- **sqlc emits LF on this CRLF repo.** After every `make generate`, run `git diff --ignore-all-space` to see the real content change, then revert pure line-ending-only hunks with `git checkout -- <file>` so the commit contains only the genuine content change. Keep only files whose `--ignore-all-space` diff is non-empty.
- **Migrations run on startup** (embedded, golang-migrate). Each up file needs a matching down file. `000017` up adds the column; down drops it.
- **Invariant: no interior pointers across locks.** `selectWorker` reads `w.SupportsWorkspaces` off a value-copied `store.Worker` from `ListWorkers`; it never touches `worker.Registry`. No new lock crossing.
- **Invariant: single job-spec pipeline.** "Source-bearing" reuses the existing `taskSrc *api.SourceSpec` already parsed in `selectWorker`. No new spec struct, no `jobspec.TaskSpec` field.
- **Invariant: epoch fence.** This change writes no `tasks.status` or `task_logs`. A held task is simply never claimed (stays `pending`, no status write). The `RegisterWorkerConnection` change adds one column to an UPDATE that already bumps `connection_epoch`; epoch semantics are untouched.

---

## Task 1: Add `optional bool supports_workspaces` to RegisterRequest (proto)

**Files:**
- Modify: `proto/relayv1/relay.proto:42-56` (the `RegisterRequest` message)
- Regenerated (do not hand-edit): `internal/proto/relayv1/relay.pb.go`

- [ ] **Step 1: Edit the proto**

In `proto/relayv1/relay.proto`, add field 12 to `RegisterRequest`, immediately after the `inventory` field (line 55). Field 12 is the next free tag (9/10 are the `oneof credential`, 11 is `inventory`):

```proto
message RegisterRequest {
  string worker_id                 = 1;
  string hostname                  = 2;
  int32  cpu_cores                 = 3;
  int32  ram_gb                    = 4;
  int32  gpu_count                 = 5;
  string gpu_model                 = 6;
  string os                        = 7;
  repeated RunningTask running_tasks = 8;
  oneof credential {
    string enrollment_token = 9;
    string agent_token      = 10;
  }
  repeated WorkspaceInventoryUpdate inventory = 11;
  // supports_workspaces reports whether this agent has a workspace provider
  // configured (provider != nil). optional gives explicit presence: a new agent
  // always sets it (true/false); an old agent omits it, and the server then
  // leaves the column's DEFAULT TRUE / prior value untouched (rolling-upgrade
  // safety). See migration 000017 and RegisterWorkerConnection.
  optional bool supports_workspaces = 12;
}
```

- [ ] **Step 2: Regenerate proto bindings**

Run: `make generate`
Expected: `internal/proto/relayv1/relay.pb.go` now defines `SupportsWorkspaces *bool` on `RegisterRequest` and a `GetSupportsWorkspaces() bool` getter. Do not hand-edit the generated file.

- [ ] **Step 3: Clean line endings, then verify it builds**

Run: `git diff --ignore-all-space`
Revert any file whose `--ignore-all-space` diff is empty (pure LF rewrite) with `git checkout -- <file>`, keeping only the genuine `relay.pb.go` content change and `relay.proto`.

Run: `go build ./...`
Expected: builds cleanly (no consumer references the field yet).

- [ ] **Step 4: Commit**

```bash
git add proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go
git commit -m "feat(proto): add optional supports_workspaces to RegisterRequest"
```

---

## Task 2: Agent sets supports_workspaces from provider presence

**Files:**
- Modify: `internal/agent/agent.go:252-261` (the `req := &relayv1.RegisterRequest{...}` literal in `buildRegisterRequest`)
- Test: `internal/agent/agent_test.go` (add a test; create the file if it does not exist - check first with a glob)

Note: this is the agent's single source of truth for capability. `cmd/relay-agent/main.go` leaves `a.provider` nil whenever `RELAY_WORKSPACE_ROOT` is unset or the Perforce preflight fails, which is exactly the providerless condition the runtime guard keys on, so reusing `a.provider != nil` keeps registration and the runtime guard consistent.

- [ ] **Step 1: Find or create the agent unit test file**

Run: `ls internal/agent/agent_test.go 2>/dev/null || echo "missing"`
If it exists, append the test below. If missing, create `internal/agent/agent_test.go` with `package agent` and the imports the test needs.

`buildRegisterRequest` is a method on `*Agent` in package `agent`, so the test must be in-package (`package agent`). The test constructs an `Agent` with the fields `buildRegisterRequest` reads: `caps`, `creds`, `runners` (map), `provider`, and the mutex `mu`. Inspect the `Agent` struct in `internal/agent/agent.go` to match field names exactly before writing the test; the snippet below assumes `caps`, `creds`, `runners`, `provider`, and an embedded/`mu sync.Mutex`. If constructing a valid `creds` is awkward in a unit test, assert only on `req.GetSupportsWorkspaces()` (the credential branch does not affect it).

- [ ] **Step 2: Write the failing test**

```go
func TestBuildRegisterRequest_SupportsWorkspaces(t *testing.T) {
	// Provider present -> reports true.
	a := &Agent{
		caps:    Capabilities{Hostname: "h1"},
		runners: map[string]*Runner{},
		provider: stubProvider{}, // any non-nil source.Provider
	}
	req, err := a.buildRegisterRequest()
	require.NoError(t, err)
	require.NotNil(t, req.SupportsWorkspaces, "field must be set with explicit presence")
	assert.True(t, req.GetSupportsWorkspaces())

	// Provider nil -> reports false (explicit presence, not absent).
	a.provider = nil
	req, err = a.buildRegisterRequest()
	require.NoError(t, err)
	require.NotNil(t, req.SupportsWorkspaces, "field must be set even when false")
	assert.False(t, req.GetSupportsWorkspaces())
}
```

Replace `Capabilities`, `Runner`, and `stubProvider{}` with the real type names from `internal/agent/agent.go` and `internal/agent/source` (or whatever the provider interface package is). `stubProvider` only needs to satisfy the `provider` field's interface type - inspect the field's type and implement its methods minimally, or reuse an existing test stub already in the package.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/agent/ -run TestBuildRegisterRequest_SupportsWorkspaces -v -timeout 30s`
Expected: FAIL - `req.SupportsWorkspaces` is nil (field never set).

- [ ] **Step 4: Set the field in buildRegisterRequest**

In `internal/agent/agent.go`, add the import `"google.golang.org/protobuf/proto"` to the import block, then set the field on the `req` literal (or immediately after it) in `buildRegisterRequest`:

```go
req := &relayv1.RegisterRequest{
	WorkerId:           a.workerID,
	Hostname:           a.caps.Hostname,
	CpuCores:           a.caps.CPUCores,
	RamGb:              a.caps.RAMGB,
	GpuCount:           a.caps.GPUCount,
	GpuModel:           a.caps.GPUModel,
	Os:                 a.caps.OS,
	RunningTasks:       running,
	SupportsWorkspaces: proto.Bool(a.provider != nil),
}
```

`proto.Bool` returns a `*bool`, giving the field explicit presence so the server can distinguish "agent reported false" from "old agent omitted it."

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/agent/ -run TestBuildRegisterRequest_SupportsWorkspaces -v -timeout 30s`
Expected: PASS.

- [ ] **Step 6: Build and vet**

Run: `go build ./... && go vet ./internal/agent/...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat(agent): report supports_workspaces from provider presence"
```

---

## Task 3: Migration 000017 - add workers.supports_workspaces column

**Files:**
- Create: `internal/store/migrations/000017_workers_supports_workspaces.up.sql`
- Create: `internal/store/migrations/000017_workers_supports_workspaces.down.sql`

The column defaults `TRUE`: the safe assumption is "a worker can manage a workspace unless it explicitly says it cannot," so a pre-field agent (or the first insert) does not strand the fleet's source tasks.

- [ ] **Step 1: Write the up migration**

`internal/store/migrations/000017_workers_supports_workspaces.up.sql`:

```sql
ALTER TABLE workers ADD COLUMN supports_workspaces BOOLEAN NOT NULL DEFAULT TRUE;
```

- [ ] **Step 2: Write the down migration**

`internal/store/migrations/000017_workers_supports_workspaces.down.sql`:

```sql
ALTER TABLE workers DROP COLUMN supports_workspaces;
```

- [ ] **Step 3: Commit (the queries and regeneration come in Task 4)**

```bash
git add internal/store/migrations/000017_workers_supports_workspaces.up.sql internal/store/migrations/000017_workers_supports_workspaces.down.sql
git commit -m "feat(store): migration 000017 add workers.supports_workspaces"
```

---

## Task 4: Add the column to the worker queries and regenerate sqlc

**Files:**
- Modify: `internal/store/query/workers.sql` (`RegisterWorkerConnection`, `UpsertWorkerByHostname`)
- Regenerated (do not hand-edit): `internal/store/workers.sql.go`, `internal/store/models.go`

`RegisterWorkerConnection` is the authoritative per-connect write of the capability (it runs on every `finishRegister`). `UpsertWorkerByHostname` writes it too so a freshly-inserted worker row has a correct value before `RegisterWorkerConnection` runs. Both use `COALESCE(narg, existing)` so a NULL param (old agent / absent field) leaves the value untouched. `RegisterWorkerConnection` already uses `RETURNING *`, so the new column flows into the returned `store.Worker` automatically; `UpsertWorkerByHostname` has an explicit `RETURNING` list that must be extended.

- [ ] **Step 1: Edit RegisterWorkerConnection**

In `internal/store/query/workers.sql`, change the `RegisterWorkerConnection` SET clause (lines 30-41) to add the COALESCE write. Keep `RETURNING *`:

```sql
-- name: RegisterWorkerConnection :one
-- Marks the worker online and atomically allocates a fresh connection_epoch for
-- this connection. The returned connection_epoch is the value this connection
-- owns; all later teardown writes for this connection fence on it. Clears
-- disconnected_at because a reconnected worker has no live disconnect timestamp.
-- supports_workspaces is the authoritative per-connect capability write: a NULL
-- param (old agent that omits proto field 12) leaves the existing value.
UPDATE workers
SET status = 'online',
    last_seen_at = $2,
    disconnected_at = NULL,
    connection_epoch = connection_epoch + 1,
    supports_workspaces = COALESCE(sqlc.narg(supports_workspaces)::bool, supports_workspaces)
WHERE id = $1
RETURNING *;
```

- [ ] **Step 2: Edit UpsertWorkerByHostname**

In `internal/store/query/workers.sql`, change `UpsertWorkerByHostname` (lines 53-64). Add the column to the insert list, VALUES, the `ON CONFLICT DO UPDATE SET`, and the explicit RETURNING list (the RETURNING list omits some columns, so add `supports_workspaces` to it explicitly):

```sql
-- name: UpsertWorkerByHostname :one
-- Insert a new worker or update hardware specs on reconnect.
-- Admin-managed fields (name, labels, max_slots) are preserved on conflict.
INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, supports_workspaces)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.narg(supports_workspaces)::bool, TRUE))
ON CONFLICT (hostname) DO UPDATE
    SET cpu_cores = EXCLUDED.cpu_cores,
        ram_gb    = EXCLUDED.ram_gb,
        gpu_count = EXCLUDED.gpu_count,
        gpu_model = EXCLUDED.gpu_model,
        os        = EXCLUDED.os,
        supports_workspaces = COALESCE(sqlc.narg(supports_workspaces)::bool, workers.supports_workspaces)
RETURNING id, name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, max_slots, labels, status, last_seen_at, created_at, disabled_at, supports_workspaces;
```

Note: on insert the COALESCE falls back to `TRUE` (matching the column default) so an old agent that omits the field gets the safe default; on conflict it falls back to the existing `workers.supports_workspaces` so an old agent never overwrites a known value.

- [ ] **Step 3: Regenerate sqlc**

Run: `make generate`
Expected:
- `internal/store/models.go` gains `SupportsWorkspaces bool` on `type Worker struct`.
- `internal/store/workers.sql.go` adds `SupportsWorkspaces *bool` to `RegisterWorkerConnectionParams` and `UpsertWorkerByHostnameParams` (a nullable `*bool` param because of `sqlc.narg` + `emit_pointers_for_null_types: true`), and the generated `RETURNING` scans include the new column.

- [ ] **Step 4: Clean line endings**

Run: `git diff --ignore-all-space`
Revert any generated file whose `--ignore-all-space` diff is empty (pure LF rewrite) with `git checkout -- <file>`. Keep only `workers.sql`, `workers.sql.go`, and `models.go` (and any other file with a real content diff).

- [ ] **Step 5: Verify the generated param types**

Run: `git diff internal/store/workers.sql.go | grep -A4 "RegisterWorkerConnectionParams\|UpsertWorkerByHostnameParams"`
Expected: both params now contain `SupportsWorkspaces *bool`. If sqlc emitted a non-pointer `bool` (presence lost), stop - the COALESCE narg is wrong; re-check the `sqlc.narg(...)::bool` syntax.

Run: `go build ./...`
Expected: FAILS in `internal/worker/handler.go` only if the handler already referenced the new field (it does not yet) - so this should build cleanly. If it builds, good. The handler wiring is Task 5.

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go internal/store/models.go
git commit -m "feat(store): persist supports_workspaces on connect and upsert"
```

---

## Task 5: Wire the capability through the register paths in handler.go

**Files:**
- Modify: `internal/worker/handler.go:294-300` (`finishRegister` - the `RegisterWorkerConnection` call)
- Modify: `internal/worker/handler.go:177-185` (`enrollAndRegister` - the `UpsertWorkerByHostname` call)
- Modify: `internal/worker/handler.go:259-267` (`autoEnrollAndRegister` - the `UpsertWorkerByHostname` call)

`reg.SupportsWorkspaces` is already a `*bool` on the generated `RegisterRequest`, which matches the new `*bool` param exactly - pass it straight through so presence (nil = old agent) is preserved end to end.

- [ ] **Step 1: Set the param in finishRegister**

In `internal/worker/handler.go`, change the `RegisterWorkerConnection` call in `finishRegister` to pass `reg.SupportsWorkspaces`:

```go
updated, err := h.q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
	ID:                 id,
	LastSeenAt:         pgtype.Timestamptz{Time: time.Now(), Valid: true},
	SupportsWorkspaces: reg.SupportsWorkspaces,
})
```

- [ ] **Step 2: Set the param in enrollAndRegister**

In the `UpsertWorkerByHostname` call inside `enrollAndRegister`:

```go
w, err := txq.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
	Name:               reg.Hostname,
	Hostname:           reg.Hostname,
	CpuCores:           reg.CpuCores,
	RamGb:              reg.RamGb,
	GpuCount:           reg.GpuCount,
	GpuModel:           reg.GpuModel,
	Os:                 reg.Os,
	SupportsWorkspaces: reg.SupportsWorkspaces,
})
```

- [ ] **Step 3: Set the param in autoEnrollAndRegister**

Apply the identical `SupportsWorkspaces: reg.SupportsWorkspaces` addition to the `UpsertWorkerByHostname` call inside `autoEnrollAndRegister`.

- [ ] **Step 4: Build and vet**

Run: `go build ./... && go vet ./internal/worker/...`
Expected: clean. (If sqlc named the param field differently than `SupportsWorkspaces`, match the generated name from `workers.sql.go`.)

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go
git commit -m "feat(worker): pass supports_workspaces through register paths"
```

---

## Task 6: Hard filter in selectWorker - skip providerless workers for source tasks

**Files:**
- Modify: `internal/scheduler/dispatch.go:177-181` (inside the worker loop, after the `free <= 0` check, before `score := free`)
- Test: `internal/scheduler/select_worker_test.go` (extend with the source-bearing helpers and three cases)

The existing `taskSrc *api.SourceSpec` is non-nil iff `task.Source` is non-empty and the JSON unmarshals - this is exactly the existing "source-bearing" notion (reused, not duplicated).

- [ ] **Step 1: Add a source-bearing task helper and write the failing tests**

In `internal/scheduler/select_worker_test.go`, add a helper that produces a task with a parseable `Source`, then add the three cases. The `api.SourceSpec` JSON shape must match what `selectWorker` unmarshals - inspect `internal/api` for the `SourceSpec` type and confirm its JSON field names before finalizing the literal; the snippet uses `{"type":"perforce","stream":"//depot/main"}` which must round-trip through `json.Unmarshal` into a non-nil `taskSrc` with `Type != ""`.

```go
// sourceTask returns a pending task whose Source is a parseable Perforce spec,
// making it "source-bearing" (taskSrc != nil in selectWorker).
func sourceTask() store.Task {
	t := baseTask()
	t.Source = []byte(`{"type":"perforce","stream":"//depot/main"}`)
	return t
}

func TestSelectWorker_SourceTaskSkipsProviderlessWorker(t *testing.T) {
	d := newDispatcherForTest()
	task := sourceTask()
	w := baseWorker(70, "online") // SupportsWorkspaces defaults false
	workers := []store.Worker{w}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	assert.Nil(t, got, "a source-bearing task must NOT be dispatched to a providerless worker")
}

func TestSelectWorker_SourceTaskSelectsCapableWorker(t *testing.T) {
	d := newDispatcherForTest()
	task := sourceTask()
	w := baseWorker(71, "online")
	w.SupportsWorkspaces = true
	workers := []store.Worker{w}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got, "a source-bearing task must be dispatched to a provider-capable worker")
	assert.Equal(t, w.ID, got.ID)
}

func TestSelectWorker_SourceTaskPrefersCapableOverFreerProviderless(t *testing.T) {
	// The providerless worker has more free slots (higher base score) but must
	// still be skipped; the capable worker wins despite fewer free slots.
	d := newDispatcherForTest()
	task := sourceTask()
	providerless := baseWorker(72, "online")
	providerless.MaxSlots = 16 // more free slots
	capable := baseWorker(73, "online")
	capable.MaxSlots = 1
	capable.SupportsWorkspaces = true
	workers := []store.Worker{providerless, capable}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got)
	assert.Equal(t, capable.ID, got.ID, "capability is a hard filter that outranks free-slot scoring")
}

func TestSelectWorker_NonSourceTaskIgnoresProviderlessFlag(t *testing.T) {
	// Regression guard: a non-source task (empty Source) schedules on a
	// providerless worker; the guard is a no-op for taskSrc == nil.
	d := newDispatcherForTest()
	task := baseTask() // no Source
	w := baseWorker(74, "online") // SupportsWorkspaces false
	workers := []store.Worker{w}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got, "a non-source task must still schedule on a providerless worker")
	assert.Equal(t, w.ID, got.ID)
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/scheduler/ -run "TestSelectWorker_SourceTask|TestSelectWorker_NonSourceTaskIgnoresProviderlessFlag" -v -timeout 30s`
Expected: `SourceTaskSkipsProviderlessWorker` and `SourceTaskPrefersCapableOverFreerProviderless` FAIL (the providerless worker is still selected). `SourceTaskSelectsCapableWorker` and `NonSourceTaskIgnoresProviderlessFlag` may already pass - that is fine; the two failing cases prove the filter is missing.

- [ ] **Step 3: Add the guard in selectWorker**

In `internal/scheduler/dispatch.go`, inside the worker loop in `selectWorker`, add the guard immediately after the `free <= 0` check (lines 177-180) and before `score := free` (line 181):

```go
		free := int64(w.MaxSlots) - activeByWorker[w.ID]
		if free <= 0 {
			continue
		}
		// Source-bearing tasks require a worker with a workspace provider.
		// Skipping providerless workers here (rather than scoring them lower) is
		// the hard requirement: a task whose Source is set must never be
		// dispatched to a worker that will only PREPARE_FAILED it. For a
		// non-source task taskSrc == nil and this is a no-op.
		if taskSrc != nil && !w.SupportsWorkspaces {
			continue
		}
		score := free
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/scheduler/ -run "TestSelectWorker" -v -timeout 30s`
Expected: all `TestSelectWorker_*` PASS, including the pre-existing non-source cases (they use the zero-value `SupportsWorkspaces=false` but are non-source, so the guard never fires).

- [ ] **Step 5: Full scheduler package test, build, vet**

Run: `go test ./internal/scheduler/... -timeout 60s && go build ./... && go vet ./internal/scheduler/...`
Expected: all pass, clean.

- [ ] **Step 6: Commit**

```bash
git add internal/scheduler/dispatch.go internal/scheduler/select_worker_test.go
git commit -m "feat(scheduler): hard-filter providerless workers for source tasks"
```

---

## Task 7: One throttled held-pending log line per dispatch cycle

**Files:**
- Modify: `internal/scheduler/dispatch.go:68-128` (`dispatch` - compute `anyProviderWorker`, count held source-bearing tasks, log once)

The dispatch loop must distinguish "no worker because all capable workers busy" (normal backpressure, stay silent) from "source-bearing task with zero connected capable workers" (the observable held condition). Log at most one line per cycle. Determining "source-bearing" in the loop must reuse the same parse as `selectWorker` (non-empty `task.Source` that unmarshals into a `Type != ""` spec).

- [ ] **Step 1: Compute anyProviderWorker and the held count, then log once**

In `internal/scheduler/dispatch.go`, in `dispatch`, after `workers, err := d.q.ListWorkers(ctx)` succeeds, compute a cheap per-cycle boolean for whether any connected worker advertises a provider. Then in the per-task loop, count source-bearing tasks that got no worker while `!anyProviderWorker`, and emit one log line after the loop.

Add after the `ListWorkers` block (around line 77):

```go
	anyProviderWorker := false
	for i := range workers {
		w := &workers[i]
		if (w.Status == "online" || w.Status == "stale") && !w.DisabledAt.Valid && w.SupportsWorkspaces {
			anyProviderWorker = true
			break
		}
	}
```

Change the per-task dispatch loop (lines 120-127) to count held source-bearing tasks:

```go
	heldSourceTasks := 0
	for _, task := range tasks {
		w := d.selectWorker(task, workers, reservations, activeByWorker, warmByWorker)
		if w != nil {
			if d.sendTask(ctx, task, *w) {
				activeByWorker[w.ID]++ // track in-cycle dispatches
			}
			continue
		}
		// No worker selected. If this is a source-bearing task and no connected
		// worker advertises a workspace provider, it is held for lack of a
		// capable worker (distinct from normal "all busy" backpressure).
		if !anyProviderWorker && taskIsSourceBearing(task) {
			heldSourceTasks++
		}
	}
	if heldSourceTasks > 0 {
		log.Printf("dispatch: %d source-bearing task(s) held pending; no connected worker has a workspace provider", heldSourceTasks)
	}
```

Add a small helper near the bottom of the file (mirrors the `selectWorker` parse so "source-bearing" stays one notion):

```go
// taskIsSourceBearing reports whether a task carries a parseable, non-empty
// source spec - the same condition selectWorker uses to require a workspace
// provider. Kept in sync with the taskSrc parse in selectWorker.
func taskIsSourceBearing(task store.Task) bool {
	if len(task.Source) == 0 {
		return false
	}
	var s api.SourceSpec
	if err := json.Unmarshal(task.Source, &s); err != nil {
		return false
	}
	return s.Type != ""
}
```

Note: `log`, `json`, `api`, and `store` are already imported in `dispatch.go`.

- [ ] **Step 2: Build and vet**

Run: `go build ./... && go vet ./internal/scheduler/...`
Expected: clean.

- [ ] **Step 3: Run the scheduler unit tests (no regression)**

Run: `go test ./internal/scheduler/... -timeout 60s`
Expected: PASS. (The log line is exercised by the integration test in Task 8; the unit tests do not call `dispatch`, only `selectWorker`.)

- [ ] **Step 4: Commit**

```bash
git add internal/scheduler/dispatch.go
git commit -m "feat(scheduler): log held source-bearing tasks when no provider worker"
```

---

## Task 8: Integration test - registration persists capability and dispatch filters

**Files:**
- Test: `internal/worker/handler_test.go` (add a new `//go:build integration` test in `package worker_test`)

This is the end-to-end proof: agent reports the bit -> `RegisterWorkerConnection` persists it -> `ListWorkers` returns it -> a source-bearing task stays `pending` (not `failed`) and is never claimed. It also asserts the rolling-upgrade default: an agent that omits the field leaves the column `TRUE`.

**Platform note (load-bearing):** this test is gated `//go:build integration` and needs Docker (testcontainers Postgres). On Windows, `make test` skips it. It MUST be run with the integration build tag, and per project memory on platform-gated verification, run it on a runnable platform (Docker Desktop running, `p4` not required for this test) before claiming done - do not claim it passes from a `make test` run that silently skipped it.

- [ ] **Step 1: Write the integration test**

Append to `internal/worker/handler_test.go`. It reuses the existing `newTestStore`, `seedWorkerWithAgentToken`, and `fakeStream` helpers in that file. Confirm the exact `store.Create*` param fields against `internal/store` before finalizing (the job/task creation mirrors `TestHandleTaskStatus_EpochGate` above in the same file).

```go
func TestRegisterAndDispatch_SourceTaskHeldOnProviderlessWorker(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	// Seed a worker with a known agent token so it reconnects (finishRegister
	// -> RegisterWorkerConnection runs and persists the capability).
	workerID, rawToken := seedWorkerWithAgentToken(t, ctx, q, "providerless-01")

	// Register reporting supports_workspaces = false (a new agent with no
	// provider). Hold the stream open so Connect stays in its message loop.
	hold := make(chan struct{})
	stream := &fakeStream{
		ctx:    ctx,
		sentCh: make(chan struct{}, 1),
		hold:   hold,
		msgs: []*relayv1.AgentMessage{
			{Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname:           "providerless-01",
					CpuCores:           4,
					RamGb:              8,
					Os:                 "linux",
					Credential:         &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
					SupportsWorkspaces: proto.Bool(false),
				},
			}},
		},
	}
	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()
	select {
	case <-stream.sentCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for RegisterResponse")
	}

	// Capability persisted as false.
	wk, err := q.GetWorker(ctx, workerID)
	require.NoError(t, err)
	assert.False(t, wk.SupportsWorkspaces, "providerless agent must persist supports_workspaces=false")
	assert.Equal(t, "online", wk.Status)

	// Submit a source-bearing job/task.
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "u@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "src-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "src-task",
		Commands: []byte(`[["echo","hi"]]`),
		Env:      []byte("{}"),
		Requires: []byte("[]"),
		Retries:  0,
	})
	require.NoError(t, err)
	// Set the task's source so it is source-bearing. If CreateTaskParams has no
	// Source field, use the appropriate setter query (e.g. an UPDATE) - inspect
	// internal/store/query/tasks.sql for how source is written.
	// (The job-spec pipeline normally sets this; for the test set it directly.)

	// Run one dispatch cycle.
	disp := scheduler.NewDispatcher(q, registry, broker)
	disp.RunOnce(ctx)

	// The source-bearing task on a providerless worker must stay pending, not
	// failed, and must never have been claimed.
	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", after.Status, "source-bearing task must stay pending on a providerless fleet")
	assert.Equal(t, int32(0), after.AssignmentEpoch, "task must never be claimed (no epoch bump)")

	close(hold)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Connect to return")
	}
}
```

Notes for the implementer:
- Add imports as needed: `"relay/internal/scheduler"`, `"google.golang.org/protobuf/proto"`.
- The critical assertions are `after.Status == "pending"` (not `failed`) and `after.AssignmentEpoch == 0` (never claimed). Together these satisfy acceptance bullets 1 and 2 without needing to capture log output.
- Setting the task source: `CreateTaskParams` may not expose a `Source` field. Inspect `internal/store/query/tasks.sql` and the generated `CreateTaskParams`. If there is no direct field, either add the source via the same query the job-spec pipeline uses (`CreateJobFromSpec` path) or via a direct `UPDATE tasks SET source = $2`. Prefer creating the task through the real job-spec path if a helper exists, to honor the single job-spec pipeline invariant; a raw UPDATE in a test is acceptable only to seed state, not as production code.
- If asserting the held-pending log line is desired, capture `log` output by setting `log.SetOutput(buf)` for the cycle and asserting the line appears once. Optional - the status + epoch assertions are the primary proof.

- [ ] **Step 2: Add a rolling-upgrade default assertion (old agent omits the field)**

Add a second, smaller integration test asserting the COALESCE/default safety: an agent that does NOT set `SupportsWorkspaces` (nil pointer, simulating an old agent) must leave the column at its `TRUE` default.

```go
func TestRegister_OldAgentOmittingFieldKeepsDefaultTrue(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	workerID, rawToken := seedWorkerWithAgentToken(t, ctx, q, "oldagent-01")

	// New worker rows default supports_workspaces = TRUE (column DEFAULT).
	before, err := q.GetWorker(ctx, workerID)
	require.NoError(t, err)
	require.True(t, before.SupportsWorkspaces, "column default must be TRUE")

	hold := make(chan struct{})
	stream := &fakeStream{
		ctx:    ctx,
		sentCh: make(chan struct{}, 1),
		hold:   hold,
		msgs: []*relayv1.AgentMessage{
			{Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname:   "oldagent-01",
					Os:         "linux",
					Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
					// SupportsWorkspaces deliberately left nil (old agent).
				},
			}},
		},
	}
	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()
	select {
	case <-stream.sentCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for RegisterResponse")
	}

	after, err := q.GetWorker(ctx, workerID)
	require.NoError(t, err)
	assert.True(t, after.SupportsWorkspaces, "old agent (nil field) must NOT overwrite to false via COALESCE")

	close(hold)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Connect to return")
	}
}
```

- [ ] **Step 3: Run the integration tests (Docker required, integration tag)**

Run: `go test -tags integration -p 1 ./internal/worker/... -run "TestRegisterAndDispatch_SourceTaskHeldOnProviderlessWorker|TestRegister_OldAgentOmittingFieldKeepsDefaultTrue" -v -timeout 180s`
Expected: PASS. (Docker Desktop must be running. On Windows the `desktop-linux` context is used automatically.)

If you cannot run Docker on this platform, run the integration suite inside a Linux container per the project memory on platform-gated verification - do NOT claim the integration tests pass from a `make test` run that skipped them.

- [ ] **Step 4: Run the full integration suite for the touched packages**

Run: `go test -tags integration -p 1 ./internal/worker/... -timeout 300s`
Expected: PASS (no regression in existing register/teardown integration tests).

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler_test.go
git commit -m "test(worker): integration cover capability persist and source-task hold"
```

---

## Task 9: Final verification and backlog close

**Files:**
- Move: `docs/backlog/bug-2026-06-19-dispatch-provider-capability-filter.md` -> `docs/backlog/closed/bug-2026-06-19-dispatch-provider-capability-filter.md` (per project memory: the `git mv` to `closed/` is required scope)

- [ ] **Step 1: Full unit build and test**

Run: `go build ./... && go vet ./... && make test`
Expected: build clean, vet clean, all unit tests pass. (`make test` does not run the integration tests; those were verified in Task 8.)

- [ ] **Step 2: Confirm the integration round trip one more time**

Run: `go test -tags integration -p 1 ./internal/worker/... ./internal/scheduler/... -timeout 300s`
Expected: PASS.

- [ ] **Step 3: Close the backlog item**

Confirm `docs/backlog/closed/` exists (create if not), then:

```bash
git mv docs/backlog/bug-2026-06-19-dispatch-provider-capability-filter.md docs/backlog/closed/bug-2026-06-19-dispatch-provider-capability-filter.md
```

Update the moved file's front-matter `status: open` to `status: closed` and add a one-line closure note referencing this plan and the spec.

- [ ] **Step 4: Commit**

```bash
git add docs/backlog/
git commit -m "docs(backlog): close dispatch-provider-capability-filter"
```

---

## Self-Review

**Spec coverage:**
- Q1 (report + persist capability): proto field (Task 1), agent sets it (Task 2), column (Task 3), persisted on both upsert and per-connect via COALESCE (Task 4), wired through all three register paths (Task 5). Covered.
- Q2 (hard filter in selectWorker): Task 6, guard after eligibility, before scoring, reusing `taskSrc`. Covered.
- Q3 (no new status; stays pending; observable log): Task 6 leaves the task pending by returning nil; Task 7 adds the one-line-per-cycle held log. Covered.
- Q4 (rolling-upgrade default): `optional bool` (Task 1) + `DEFAULT TRUE` (Task 3) + COALESCE narg (Task 4) + nil pass-through (Task 5), asserted in Task 8 Step 2. Covered.
- Success criteria bullets 1/2/3: unit Task 6 (skip providerless, select capable, capable-over-freer, non-source unaffected); integration Task 8 (persist + held-pending). Covered.

**Invariants:** no-interior-pointers (value-copied `store.Worker`, no Registry access) - noted and honored in Tasks 6/7; single job-spec pipeline (reuses `taskSrc`, no new spec; test seeding flagged to prefer the real pipeline) - Tasks 6/8; epoch fence (no status/log write; held task never claimed; integration asserts `AssignmentEpoch == 0`) - Task 8. Covered.

**Codegen sequencing:** proto edit -> `make generate` (Task 1); SQL/migration edit -> `make generate` (Task 4); LF/CRLF cleanup step included after each generate. `*.sql.go`/`models.go` never hand-edited. Covered.

**Migration number:** `000017` confirmed next free (latest on disk is `000016`); up adds column, down drops it; both files present (Task 3). Covered.

**Open items the implementer must resolve from the code (flagged inline, not placeholders):** exact `api.SourceSpec` JSON field names; exact `Agent`/provider type names for the agent unit test stub; whether `CreateTaskParams` exposes a `Source` field or needs a setter for the integration test. Each step names the file to inspect.
