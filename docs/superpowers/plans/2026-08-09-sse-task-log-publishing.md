# Publish Task-Log Lines to the SSE Event Broker - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `GET /v1/events?task_id=<uuid>` stream `event: task_log` frames for that one task, published from `handleTaskLog` on the agent log-ingest path, without adding a DB round trip, without blocking the gRPC recv loop, and without changing what any existing status subscriber receives.

**Architecture:** The `events.Broker` gains a second, task-keyed index (`logSubs map[string]map[chan Event]struct{}`) alongside the existing `subs map[chan Event]Filter`, so a `task_log` publish iterates only that task's subscribers and a status publish iterates only `subs`. `Subscribe` takes an `events.Filter{JobID, TaskID}` value. `AppendTaskLog` becomes a single `:one` statement whose `WITH fence/ins` CTE returns `id`, `created_at` and the task's `job_id`, so the publish is both gated on the epoch fence having matched and fully populated from the same round trip. `handleTaskLog` guards the marshal+publish behind a new O(1) `Broker.HasLogSubscriber(taskID)`.

**Tech Stack:** Go 1.x, pgx v5, sqlc v1.30, testify, testcontainers-go (Postgres 16), stdlib `net/http` SSE.

**Spec:** `docs/superpowers/specs/2026-08-09-sse-task-log-publishing.md`

---

## Slice independence declaration

- **This is a backend-only plan. Frontend slice: none.** Zero files under `web/` change. Zero files under `internal/mcp/` change. No migration is added. If you find yourself writing a React hook, a `HoloTaskLog` view, or an MCP tool, stop - those are `docs/backlog/feature-2026-06-26-task-log-view-sse-tailing.md` and `docs/backlog/idea-2026-05-09-mcp-live-task-log-streaming.md`, and they are explicitly out of scope.
- **Backend slice: one, and it is SEQUENTIAL.** Do **not** split these tasks across two engineers running in parallel. Reasons:
  - Tasks 1 and 2 both rewrite `internal/events/broker.go` and `internal/events/broker_test.go`. Task 2 depends on Task 1's `Filter` type existing.
  - Tasks 3, 4 and 7 all edit `internal/worker/handler.go`. Task 3 changes the `AppendTaskLog` call site; Task 4 adds the publish immediately below it; Task 7 adds tests that drive it.
  - Task 3 regenerates `internal/store/*.sql.go`. A second writer touching any `.sql` file concurrently would collide on the generated tree and on the CRLF-revert step.
  - Task 5 depends on Task 1 (`Filter`) and Task 2 (`HasLogSubscriber`); Task 7 depends on Tasks 2, 4 and 5 all being in.
  - The project has already been burned by concurrent writers on shared files. One engineer, tasks in order.
- **Parallelism the conductor can use:** the only honest cut is **Task 6** (`internal/relayclient/client.go` scanner buffer). It touches one file no other task touches and has no dependency on the broker or store work, so it can be handed to a second engineer or slotted anywhere. Everything else is a single serial unit. If the batch contains other unrelated items, they can run alongside this whole plan.

---

## Ordering rationale: why the broker lands first, alone

**This is the load-bearing sequencing decision in the plan. Do not reorder it.**

A `?job_id=J&task_id=T` subscription puts **one channel in two maps**. That channel must be closed exactly once and removed from **both** maps on every exit path (explicit cancel, slow-subscriber drop via a status publish, slow-subscriber drop via a log publish, and a double cancel). Get it wrong and you get one of two panics:

- a double `close(ch)` - `close of closed channel`; or
- a stale `logSubs` entry pointing at an already-closed channel - `send on closed channel`.

`Broker.Publish` is called **synchronously from `handleTaskLog`, which is called synchronously from the `Connect` recv loop** (`internal/worker/handler.go:117-128`). A panic there kills the goroutine servicing a live agent's gRPC stream. So the failure mode is not "an SSE client sees a glitch", it is "an agent connection dies mid-job".

Therefore: **Task 1 and Task 2 land the broker with all of its unit tests green - including the both-indexes cancel test, both drop paths, and a concurrent stress test under `-race` - before a single line of `handleTaskLog` is touched.** Until Task 4, nothing publishes a `task_log` event in production, so a broker bug cannot reach the recv loop. After Task 4 it can. Land the safety first.

Task 1 is deliberately split from Task 2: Task 1 is the mechanical `Subscribe(string)` -> `Subscribe(events.Filter)` signature migration that fans out into `internal/api/events.go` and two test files with **no behaviour change at all**, so its diff is reviewable as pure mechanics. Task 2 is the behavioural change on top. Keeping them in separate commits is what the spec's Risks section asks for.

---

## Invariant map (which task carries which)

| Invariant | Tasks | How it applies here |
|---|---|---|
| **Epoch fence** | 3, 4, 7 | `task_logs` writes are already fenced (`internal/store/query/tasks.sql:48-56`). Task 3 makes the fence's *result* observable (`:one` -> `pgx.ErrNoRows` when rejected) without changing its semantics. Task 4 gates the publish on the fence having matched, so a stale-epoch chunk is neither stored nor published. **Never call `AppendTaskLog` with a zero-value `AssignmentEpoch`** except in a test that is deliberately asserting the stale-drop path (Task 3's store test does exactly that, and that is the only such call). No epoch is bumped anywhere in this plan. |
| **One bounded sender per gRPC stream** | 2, 4 | The publish sits on the log-ingest path of a live agent stream. Task 2 must preserve `Publish`'s `select { case ch <- e: default: }` bound (`internal/events/broker.go:54-59`) - no blocking send, no `time.After` fallback, no unbounded queue, no goroutine spawn per event. Task 4 must add **no DB round trip** (the job id comes from the same statement) and must skip the marshal entirely via `HasLogSubscriber` when nobody is tailing. |
| **No interior pointers across locks** | 2 | `Subscribe` returns a `<-chan Event`, not a pointer into broker state. `Filter` is passed and stored **by value**. `HasLogSubscriber` returns a `bool`. Do **not** add any getter that returns `b.subs`, `b.logSubs`, or an inner `map[chan Event]struct{}` - all mutation goes through methods holding `b.mu`. |
| **Identity-checked teardown** | - | Not in play. No worker registry or connection-epoch state is touched. |
| **Single job-spec pipeline** | - | Not in play. No job-spec ingestion. |
| **Single JSON entry point** | 5 | Not in play in the sense that no request *body* is read - `handleEvents` reads only query parameters, so `readJSON` is not involved. Do not add a body to `GET /v1/events`. |

---

## File Structure

**Modified files**

| File | Change | Task |
|---|---|---|
| `internal/events/broker.go:9-66` (whole file) | `Event.TaskID`, `Filter` type, `Subscribe(Filter)`, `logSubs` index, `removeLocked`, `HasLogSubscriber`, `TypeTaskLog`, task-aware `Publish`. | 1, 2 |
| `internal/events/broker_test.go` (9 `Subscribe` sites) | Ported to `Filter`, then extended with the delivery-matrix, both-indexes, drop-path, non-blocking and concurrency tests. | 1, 2 |
| `internal/scheduler/dispatch_test.go:569` | `broker.Subscribe("")` -> `broker.Subscribe(events.Filter{})`. Integration-tagged; `make test` will not catch it, `make vet-integration` will. | 1 |
| `internal/api/events.go:9-40` | Parse + validate `?task_id=`, build the `Filter`, emit the `dropped` frame on broker close. | 5 |
| `internal/store/query/tasks.sql:48-56` | `AppendTaskLog` `:exec` -> `:one` with the `WITH fence/ins` CTE. | 3 |
| `internal/store/tasks.sql.go` | **Generated. Never hand-edit.** Produced by `make generate` in Task 3. | 3 |
| `internal/store/store_test.go:241-283` | `TestAppendTaskLog_EpochGuarded` strengthened to assert `pgx.ErrNoRows` on the stale path and the returned `job_id`/monotonic `id` on the matching path. | 3 |
| `internal/worker/handler.go:508-526` | `handleTaskLog` consumes the returned row, logs real DB errors (never chunk content), and publishes behind `HasLogSubscriber`. Plus the `taskLogEvent` payload struct and the `taskLogPublishes` counter. | 3, 4 |
| `internal/worker/export_test.go` | Add `HandleTaskLog` and `TaskLogPublishesForTest` (integration-tagged, `package worker`). | 4 |
| `internal/relayclient/client.go:141` | `scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)`. | 6 |
| `internal/relayclient/client_test.go` | Add the 512 KiB `data:` line test. | 6 |
| `README.md:1299-1305` | Events section: `?task_id=`, `task_log`, `dropped`, the subscribe-then-backfill contract, the validation asymmetry, the single-process caveat. | 8 |

**New files**

| File | Responsibility | Task |
|---|---|---|
| `internal/worker/tasklog_payload_test.go` | Unit test (`package worker`, no build tag): the `task_log` payload's exact JSON key set and types. | 4 |
| `internal/worker/handler_tasklog_integration_test.go` | Integration (`//go:build integration`, `package worker_test`): epoch gate on publish, the `HasLogSubscriber` fast path with a paired positive control, persistence with no subscriber. | 4 |
| `internal/api/events_task_log_integration_test.go` | Integration (`//go:build integration`, `package api_test`): `?task_id=` 400/404/200, the four-row delivery matrix, the `dropped` frame plus its negative control, one-`data:`-line framing. | 5 |
| `internal/api/events_task_log_e2e_integration_test.go` | Integration (`//go:build integration`, `package api_test`): agent chunk -> subscribed SSE client receives `task_log`; status events unaffected; gapless/dedup backfill join. | 7 |

**Read-only reference (do not edit)**

- `internal/api/tasks.go:56-61` - `logEntry`. This is the authority the SSE payload's `seq`/`stream`/`content`/`created_at` must match field-for-field.
- `internal/api/tasks.go:63-137` - the polling logs endpoint (`?limit`, `?since_seq`, `{items, next_seq, total}`).
- `internal/api/server.go:171` - `GET /v1/events` is `auth(...)`-only, no admin gate, no ownership check. `internal/api/server.go:124` - same for `GET /v1/tasks/{id}/logs`. **Do not add an ownership gate here**; matching the existing model is a spec decision.
- `internal/api/server.go:217-233` - `uuidStr` and `parseUUID`.
- `internal/worker/handler.go:117-128` - the serialized recv loop. `internal/worker/handler.go:674-681` - the worker package's `uuidStr` (identical lowercase-hex output to the api package's).
- `internal/worker/handler.go:488-500` - the existing status publish sites, for `log.Printf` and `Publish` style.
- `internal/agent/runner.go:285-309` - `chunkWriter.Write`; chunk sizes come from `os/exec`'s copy buffer (~32 KiB), which is why Task 6 exists.
- `internal/store/query/tasks.sql:159-168` - `GetTaskLogsPage` / `CountTaskLogs`; `seq` is `task_logs.id`.
- `internal/api/api_test.go:27-61` - `newTestServer`, `createTestUser`, `createTestToken`. `internal/api/testhelper_test.go:72-98` - `newTestPool`.
- `internal/worker/handler_test.go:33-112` - `seedWorkerWithAgentToken`, `fakeStream`, `newTestStore`.

---

## Conventions for every task

- All Go commands run from the repo root: `D:/dev/relay/.claude/worktrees/happy-mendel-18687f`. **Use this worktree path, not `D:/dev/relay`.**
- Unit suite: `go test ./... -timeout 120s` (this is `make test`; no Docker).
- Single unit test: `go test ./internal/events/... -run TestBroker_X -v -timeout 30s`.
- Integration suite: `make test-integration` = `go test -tags integration -p 1 ./... -timeout 300s`. **Requires Docker Desktop running.** `-p 1` is not optional - it prevents parallel container conflicts.
- Single integration test: `go test -tags integration -p 1 ./internal/worker/... -run TestHandleTaskLog_X -v -timeout 300s`.
- Compile the integration-tagged tree without running it: `make vet-integration` (= `go vet -tags integration ./...`). Run this after **every** signature change - `make test` never compiles `//go:build integration` files, so a broken integration-only call site is invisible to it.
- Race detector, per the project toolchain note (the default Strawberry Perl gcc fails with exit `0xc0000139`), run from **Git Bash** via the Bash tool:
  ```bash
  PATH="/c/msys64/mingw64/bin:$PATH" CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/events/... -timeout 120s
  ```
- Never edit `internal/store/*.sql.go` or `internal/store/models.go` by hand. SQL lives in `internal/store/query/*.sql`; `make generate` produces the Go.
- House rule: never use em dashes or en dashes. Use hyphens. Note that `internal/events/broker.go:29` currently contains an em dash in a comment - when you rewrite that comment in Task 1, replace it with a hyphen.
- Never reformat or "tidy" code you were not asked to change.
- Commit at the end of every task. Use bash heredocs for multi-line commit messages (the Bash tool is Git Bash), not PowerShell here-strings.

---

## Task 1: Migrate `Subscribe` to `events.Filter` (mechanical, no behaviour change)

Pure signature migration. After this task the broker behaves **exactly** as it does today: `Filter{}` receives everything, `Filter{JobID: J}` receives `J`'s events. `TaskID` is carried but not yet consulted. No `logSubs` map yet. That is deliberate - it keeps this diff reviewable as mechanics.

**Files:**
- Modify: `internal/events/broker.go:9-44` (Event, Broker, NewBroker, Subscribe)
- Modify: `internal/events/broker_test.go` (9 `Subscribe` call sites)
- Modify: `internal/api/events.go:14-15`
- Modify: `internal/scheduler/dispatch_test.go:569`

- [ ] **Step 1: Write the failing test**

Append to `internal/events/broker_test.go`:

```go
func TestBroker_SubscribeTakesAFilterValue(t *testing.T) {
	b := events.NewBroker()

	// Filter{} is today's Subscribe("") - receives every status event.
	chAll, cancelAll := b.Subscribe(events.Filter{})
	defer cancelAll()
	// Filter{JobID: ...} is today's Subscribe("job-1").
	chJob1, cancelJob1 := b.Subscribe(events.Filter{JobID: "job-1"})
	defer cancelJob1()

	// TaskID is carried on the Event and on the Filter but is not yet consulted
	// by Publish; Task 2 adds the routing. Setting it here proves the fields
	// exist and that a status event still routes purely on JobID.
	e := events.Event{Type: "task", JobID: "job-1", TaskID: "task-1", Data: []byte(`{}`)}
	b.Publish(e)

	require.Equal(t, e, <-chAll)
	require.Equal(t, e, <-chJob1)
}
```

Now port the 9 existing `Subscribe` sites in the same file:

- line 16: `ch1, cancel1 := b.Subscribe(events.Filter{})`
- line 17: `ch2, cancel2 := b.Subscribe(events.Filter{})`
- line 31: `chAll, cancelAll := b.Subscribe(events.Filter{})`
- line 32: `chJob1, cancelJob1 := b.Subscribe(events.Filter{JobID: "job-1"})`
- line 33: `chJob2, cancelJob2 := b.Subscribe(events.Filter{JobID: "job-2"})`
- line 53: `ch, cancel := b.Subscribe(events.Filter{})`
- line 72: `slow, _ := b.Subscribe(events.Filter{})`
- line 101: `slow, _ := b.Subscribe(events.Filter{})`
- line 103: `fast, cancelFast := b.Subscribe(events.Filter{})`

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/events/... -timeout 60s`
Expected: FAIL to **compile** - `undefined: events.Filter` and `too many arguments in call to b.Subscribe`. A compile failure is the correct RED here: the type does not exist.

- [ ] **Step 3: Implement the signature change**

Replace `internal/events/broker.go` lines 8-44 with:

```go
// Event is published on any state change and delivered to SSE subscribers.
type Event struct {
	Type   string // "task", "job", "worker", or TypeTaskLog
	JobID  string // status events: empty = broadcast to all; non-empty = scoped to that job
	TaskID string // TypeTaskLog events: the task whose log line this is. Empty on status events.
	Data   []byte // JSON-encoded payload
}

// Filter is a subscription's delivery scope, passed by value. The zero value
// receives every status event, which is the historical Subscribe("") behaviour.
type Filter struct {
	JobID  string
	TaskID string
}

// Broker fans out published events to all matching subscribers.
type Broker struct {
	mu   sync.Mutex
	subs map[chan Event]Filter // channel -> its delivery scope
}

// NewBroker returns a ready-to-use Broker.
func NewBroker() *Broker {
	return &Broker{subs: make(map[chan Event]Filter)}
}

// Subscribe registers a new subscriber and returns a receive channel and a
// cancel function. Filter{} receives all status events; Filter{JobID: J}
// receives only status events for J. The channel has a buffer of 64; if the
// buffer fills, the broker unsubscribes and closes the channel - consumers
// should treat channel close as "you fell behind, reconnect if you need more".
// Cancel is idempotent.
func (b *Broker) Subscribe(f Filter) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = f
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}
```

Then change `Publish`'s loop body at `internal/events/broker.go:52-53` from `for ch, filter := range b.subs { if filter == "" || filter == e.JobID {` to:

```go
	for ch, f := range b.subs {
		if f.JobID == "" || f.JobID == e.JobID {
```

Update `internal/api/events.go:14-15`:

```go
	jobID := r.URL.Query().Get("job_id")
	ch, cancel := s.broker.Subscribe(events.Filter{JobID: jobID})
```

and add `"relay/internal/events"` to that file's import block.

Update `internal/scheduler/dispatch_test.go:569`:

```go
	ch, cancel := broker.Subscribe(events.Filter{})
```

(`internal/scheduler/dispatch_test.go` already imports `relay/internal/events` - it constructs `events.NewBroker()` on line 568.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/events/... ./internal/api/... -timeout 120s`
Expected: PASS (6 broker tests: the 5 existing plus the new one).

Run: `go vet ./...`
Expected: no output.

Run: `make vet-integration`
Expected: no output. **This is the step that proves `dispatch_test.go:569` was fixed** - `make test` cannot see it.

- [ ] **Step 5: Commit**

```bash
git add internal/events/broker.go internal/events/broker_test.go internal/api/events.go internal/scheduler/dispatch_test.go
git commit -m "refactor(events): Subscribe takes an events.Filter value"
```

---

## Task 2: Two-index broker - task-keyed `logSubs`, `HasLogSubscriber`, delivery matrix

The risky task. Everything here is about a channel that lives in two maps being closed exactly once.

**Files:**
- Modify: `internal/events/broker.go` (whole file)
- Modify: `internal/events/broker_test.go` (append tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/events/broker_test.go`. Add `"sync"` to the import block.

```go
// ─── Delivery matrix ─────────────────────────────────────────────────────────
// The four rows of the spec's matrix. A task_log event goes only to a
// subscription that named that exact TaskID; a status event goes to
// subscriptions where JobID == e.JobID, or where JobID == "" AND TaskID == "".

func logEvent(taskID string) events.Event {
	return events.Event{Type: events.TypeTaskLog, TaskID: taskID, JobID: "job-1", Data: []byte(`{"seq":1}`)}
}

func statusEvent(jobID string) events.Event {
	return events.Event{Type: "task", JobID: jobID, Data: []byte(`{}`)}
}

// mustNotReceive fails if ch produces a live event within the window. A closed
// channel is also a failure here: nothing in these tests should be drop-closed.
func mustNotReceive(t *testing.T, ch <-chan events.Event, what string) {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatalf("%s: channel was closed unexpectedly", what)
		}
		t.Fatalf("%s: unexpectedly received %+v", what, e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBroker_GlobalSubscriberReceivesNoTaskLogEvents(t *testing.T) {
	b := events.NewBroker()
	chAll, cancel := b.Subscribe(events.Filter{})
	defer cancel()

	b.Publish(logEvent("task-1"))
	// Negative assertion. Paired positive control on the same code path and the
	// same channel: a status event MUST still arrive, so a broker that delivered
	// nothing at all could not pass this test.
	mustNotReceive(t, chAll, "global subscriber after a task_log publish")
	b.Publish(statusEvent("job-9"))
	require.Equal(t, "job-9", (<-chAll).JobID)
}

func TestBroker_TaskOnlySubscriberReceivesOnlyItsOwnLogs(t *testing.T) {
	b := events.NewBroker()
	ch, cancel := b.Subscribe(events.Filter{TaskID: "task-1"})
	defer cancel()

	b.Publish(statusEvent("job-1")) // must NOT arrive: TaskID-only is not a global status sub
	b.Publish(logEvent("task-2"))   // must NOT arrive: wrong task
	mustNotReceive(t, ch, "task-1 subscriber after a status publish and a task-2 log")

	// Positive control on the same channel.
	b.Publish(logEvent("task-1"))
	got := <-ch
	require.Equal(t, events.TypeTaskLog, got.Type)
	require.Equal(t, "task-1", got.TaskID)
}

func TestBroker_JobAndTaskSubscriberReceivesBoth(t *testing.T) {
	b := events.NewBroker()
	ch, cancel := b.Subscribe(events.Filter{JobID: "job-1", TaskID: "task-1"})
	defer cancel()

	b.Publish(statusEvent("job-2")) // wrong job
	b.Publish(logEvent("task-2"))   // wrong task
	mustNotReceive(t, ch, "{job-1,task-1} subscriber after a job-2 status and a task-2 log")

	b.Publish(statusEvent("job-1"))
	b.Publish(logEvent("task-1"))
	first := <-ch
	second := <-ch
	require.Equal(t, "task", first.Type)
	require.Equal(t, "job-1", first.JobID)
	require.Equal(t, events.TypeTaskLog, second.Type)
	require.Equal(t, "task-1", second.TaskID)
}

// ─── The task-keyed index really is an index ─────────────────────────────────

func TestBroker_TaskLogPublishTouchesOnlyThatTasksSubscribers(t *testing.T) {
	b := events.NewBroker()
	logCh, cancelLog := b.Subscribe(events.Filter{TaskID: "task-1"})
	defer cancelLog()

	// 20 status subscribers, none of which reads. If a task_log publish
	// considered them at all they would each take a buffer slot; with 65 log
	// publishes and a 64-slot buffer they would be drop-closed. They must not be.
	var statusChans []<-chan events.Event
	for i := 0; i < 20; i++ {
		ch, cancel := b.Subscribe(events.Filter{})
		defer cancel()
		statusChans = append(statusChans, ch)
	}

	drained := make(chan int, 1)
	go func() {
		n := 0
		for range logCh {
			n++
			if n == 65 {
				break
			}
		}
		drained <- n
	}()
	for i := 0; i < 65; i++ {
		b.Publish(logEvent("task-1"))
	}
	select {
	case n := <-drained:
		require.Equal(t, 65, n)
	case <-time.After(2 * time.Second):
		t.Fatal("log subscriber did not receive all 65 task_log events")
	}

	// Every status subscriber must still be open and empty.
	for i, ch := range statusChans {
		mustNotReceive(t, ch, "status subscriber "+strconv.Itoa(i))
	}
}

// ─── HasLogSubscriber ────────────────────────────────────────────────────────

func TestBroker_HasLogSubscriber(t *testing.T) {
	b := events.NewBroker()
	require.False(t, b.HasLogSubscriber("task-1"), "no subscribers")

	// A job-only subscription must NOT make it true - that is what keeps the
	// handleTaskLog fast path from marshalling for status-only watchers.
	_, cancelJob := b.Subscribe(events.Filter{JobID: "job-1"})
	require.False(t, b.HasLogSubscriber("task-1"), "job-only subscription")
	cancelJob()

	_, cancelTask := b.Subscribe(events.Filter{TaskID: "task-1"})
	require.True(t, b.HasLogSubscriber("task-1"))
	require.False(t, b.HasLogSubscriber("task-2"), "different task")
	cancelTask()
	require.False(t, b.HasLogSubscriber("task-1"), "after cancel")
}

// ─── Both-indexes lifecycle: the panic surface ───────────────────────────────

func TestBroker_CancelRemovesSubscriberFromBothIndexes(t *testing.T) {
	b := events.NewBroker()
	ch, cancel := b.Subscribe(events.Filter{JobID: "job-1", TaskID: "task-1"})

	cancel()
	require.False(t, b.HasLogSubscriber("task-1"), "logSubs still holds the cancelled channel")

	// Neither publish may panic. A stale logSubs entry would be a
	// "send on closed channel" panic on the second line; a stale subs entry
	// would be one on the first.
	b.Publish(statusEvent("job-1"))
	b.Publish(logEvent("task-1"))

	// Cancel is idempotent: a second call must not double-close.
	cancel()

	_, ok := <-ch
	require.False(t, ok, "channel must be closed exactly once")
}

func TestBroker_DropViaStatusPublishRemovesFromBothIndexes(t *testing.T) {
	b := events.NewBroker()
	// Never read. Fill via STATUS events, which is the subs-index path.
	_, cancel := b.Subscribe(events.Filter{JobID: "job-1", TaskID: "task-1"})
	defer cancel()

	for i := 0; i < 65; i++ {
		b.Publish(statusEvent("job-1"))
	}
	require.False(t, b.HasLogSubscriber("task-1"),
		"a subscriber dropped by a status publish must be removed from logSubs too")

	// Would panic with "send on closed channel" if logSubs kept the entry.
	b.Publish(logEvent("task-1"))
}

func TestBroker_DropViaLogPublishRemovesFromBothIndexes(t *testing.T) {
	b := events.NewBroker()
	// Never read. Fill via LOG events, which is the logSubs-index path.
	_, cancel := b.Subscribe(events.Filter{JobID: "job-1", TaskID: "task-1"})
	defer cancel()

	for i := 0; i < 65; i++ {
		b.Publish(logEvent("task-1"))
	}
	require.False(t, b.HasLogSubscriber("task-1"))

	// Would panic with "send on closed channel" if subs kept the entry.
	b.Publish(statusEvent("job-1"))
}

func TestBroker_StatusSubscriberUnaffectedByLogSubscriberDrop(t *testing.T) {
	b := events.NewBroker()
	// A log subscriber that never reads; it gets drop-closed on event 65.
	_, cancelSlow := b.Subscribe(events.Filter{TaskID: "task-1"})
	defer cancelSlow()
	fast, cancelFast := b.Subscribe(events.Filter{})
	defer cancelFast()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 65; i++ {
			<-fast
		}
	}()

	for i := 0; i < 65; i++ {
		b.Publish(logEvent("task-1"))   // drop-closes the slow log subscriber
		b.Publish(statusEvent("job-1")) // must still reach fast, all 65 times
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("status subscriber did not receive all 65 status events while a log subscriber was dropped")
	}
}

// ─── The non-blocking guarantee, directly ────────────────────────────────────

func TestBroker_PublishNeverBlocksOnAStalledLogSubscriber(t *testing.T) {
	b := events.NewBroker()
	slow, cancel := b.Subscribe(events.Filter{TaskID: "task-1"})
	defer cancel()

	// 200 publishes against a 64-slot buffer that nobody drains. Bounded, this
	// takes well under a millisecond; unbounded (a bare `ch <- e`) it blocks
	// forever on publish 65. The 5s budget is >1000x the real cost, so it cannot
	// flake, and the assertion is a Fatal on timeout rather than a hang because
	// the publishes run on their own goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			b.Publish(logEvent("task-1"))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a task-scoped subscriber that stopped reading")
	}

	// Drain to the close: the stalled subscriber must end up closed, not stuck.
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-slow:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stalled log subscriber was never closed")
		}
	}
}

// ─── Concurrency, for the race detector to chew on ───────────────────────────

func TestBroker_ConcurrentSubscribeCancelPublish(t *testing.T) {
	b := events.NewBroker()
	var wg sync.WaitGroup

	// Churn subscriptions across both indexes.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				f := events.Filter{}
				switch i % 3 {
				case 0:
					f = events.Filter{TaskID: "task-1"}
				case 1:
					f = events.Filter{JobID: "job-1", TaskID: "task-1"}
				}
				ch, cancel := b.Subscribe(f)
				go func() {
					for range ch {
					}
				}()
				cancel()
			}
		}(g)
	}
	// Publish both families concurrently.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				b.Publish(logEvent("task-1"))
				b.Publish(statusEvent("job-1"))
				_ = b.HasLogSubscriber("task-1")
			}
		}()
	}
	wg.Wait()
}
```

Add `"strconv"` and `"sync"` to `internal/events/broker_test.go`'s imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/events/... -timeout 120s`
Expected: FAIL to compile - `undefined: events.TypeTaskLog` and `b.HasLogSubscriber undefined`.

- [ ] **Step 3: Implement the two-index broker**

Replace `internal/events/broker.go` in full:

```go
package events

import (
	"log"
	"sync"
)

// TypeTaskLog is the Event.Type of a single persisted task-log line. Unlike the
// status types ("task", "job", "worker") it is routed purely by Event.TaskID:
// only a subscription that named that exact task receives it. There is
// deliberately no job-wide or global log firehose - a Filter{} subscriber (which
// relay watch opens) must never be handed every log line on the cluster.
const TypeTaskLog = "task_log"

// Event is published on any state change and delivered to SSE subscribers.
type Event struct {
	Type   string // "task", "job", "worker", or TypeTaskLog
	JobID  string // status events: empty = broadcast to all; non-empty = scoped to that job
	TaskID string // TypeTaskLog events: the task whose log line this is. Empty on status events.
	Data   []byte // JSON-encoded payload
}

// Filter is a subscription's delivery scope, passed by value. The zero value
// receives every status event, which is the historical Subscribe("") behaviour.
//
// Delivery matrix:
//
//	JobID  TaskID  receives
//	""     ""      all status events
//	J      ""      status events for J
//	""     T       task_log events for T only
//	J      T       status events for J plus task_log events for T
type Filter struct {
	JobID  string
	TaskID string
}

// Broker fans out published events to all matching subscribers.
//
// Two indexes, one channel. subs is the presence authority: a channel is in
// logSubs only while it is also in subs, and removeLocked is the only place a
// subscriber channel is closed. That is what makes close-exactly-once and
// removal-from-both-maps a single invariant instead of two.
//
// The second index exists because log chunks raise publish frequency by orders
// of magnitude: a task_log publish must iterate only that task's subscribers,
// never the whole subscriber set, so it cannot slow status delivery.
type Broker struct {
	mu      sync.Mutex
	subs    map[chan Event]Filter               // channel -> its delivery scope
	logSubs map[string]map[chan Event]struct{}  // task id -> channels tailing that task
}

// NewBroker returns a ready-to-use Broker.
func NewBroker() *Broker {
	return &Broker{
		subs:    make(map[chan Event]Filter),
		logSubs: make(map[string]map[chan Event]struct{}),
	}
}

// Subscribe registers a new subscriber and returns a receive channel and a
// cancel function. See Filter for the delivery matrix. The channel has a buffer
// of 64; if the buffer fills, the broker unsubscribes and closes the channel -
// consumers should treat channel close as "you fell behind, reconnect if you
// need more". Cancel is idempotent and removes the channel from both indexes.
func (b *Broker) Subscribe(f Filter) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = f
	if f.TaskID != "" {
		m := b.logSubs[f.TaskID]
		if m == nil {
			m = make(map[chan Event]struct{})
			b.logSubs[f.TaskID] = m
		}
		m[ch] = struct{}{}
	}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		b.removeLocked(ch)
		b.mu.Unlock()
	}
}

// HasLogSubscriber reports whether anyone is tailing taskID's logs. It is an
// O(1) map lookup under one uncontended mutex acquire, so the log-ingest path
// can skip marshalling and publishing entirely in the steady state where nobody
// is watching. Racing is benign: a false negative drops at most the chunks in
// flight while a subscriber was mid-Subscribe, and that subscriber's
// GET /v1/tasks/{id}/logs backfill covers them.
func (b *Broker) HasLogSubscriber(taskID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.logSubs[taskID]) > 0
}

// removeLocked removes ch from both indexes and closes it. It is the ONLY place
// a subscriber channel is closed, and it guards on presence in b.subs, so a
// double cancel, or a cancel racing a slow-subscriber drop, closes exactly once.
// b.mu must be held.
func (b *Broker) removeLocked(ch chan Event) {
	f, ok := b.subs[ch]
	if !ok {
		return // already removed and closed
	}
	delete(b.subs, ch)
	if f.TaskID != "" {
		if m := b.logSubs[f.TaskID]; m != nil {
			delete(m, ch)
			if len(m) == 0 {
				delete(b.logSubs, f.TaskID)
			}
		}
	}
	close(ch)
}

// Publish delivers e to the matching subscribers and never blocks: a subscriber
// whose 64-slot buffer is full costs one failed send and is then closed and
// removed from both indexes. Callers may therefore publish from a gRPC recv
// goroutine or an HTTP handler without risk of being stalled by a peer that
// stopped reading. Do not replace the select/default with a blocking send, a
// timeout, or an unbounded queue.
func (b *Broker) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var dropped []chan Event
	if e.Type == TypeTaskLog {
		// Task-keyed fan-out only. Status subscribers are not even considered,
		// so log traffic can never fill or drop-close a status subscription.
		for ch := range b.logSubs[e.TaskID] {
			select {
			case ch <- e:
			default:
				dropped = append(dropped, ch)
			}
		}
	} else {
		for ch, f := range b.subs {
			// A TaskID-only subscription is a log tail, not a global status
			// stream. Without this clause "?task_id=" alone would silently
			// become an accidental all-jobs status subscription.
			if f.JobID == "" && f.TaskID != "" {
				continue
			}
			if f.JobID == "" || f.JobID == e.JobID {
				select {
				case ch <- e:
				default:
					dropped = append(dropped, ch)
				}
			}
		}
	}

	for _, ch := range dropped {
		b.removeLocked(ch)
		log.Printf("events: dropped slow subscriber (buffer full)")
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/events/... -v -timeout 120s`
Expected: PASS - the 6 tests from Task 1 plus 10 new ones.

Run: `go test ./... -timeout 120s` then `go vet ./...` then `make vet-integration`
Expected: all green, no output from vet.

- [ ] **Step 5: Prove the both-indexes tests are not vacuous**

Three temporary mutations. After each, run `go test ./internal/events/... -timeout 120s` and confirm the named test **fails**, then revert before the next.

1. **Stale `logSubs` entry.** In `removeLocked`, delete the whole `if f.TaskID != "" { ... }` block.
   Expected: `TestBroker_CancelRemovesSubscriberFromBothIndexes`, `TestBroker_DropViaStatusPublishRemovesFromBothIndexes` and `TestBroker_HasLogSubscriber` all fail - the first two with a **panic: send on closed channel** raised from inside `Publish`. That panic is precisely the production failure this task exists to prevent; if you do not see it, the tests are not exercising the exposure and you must fix the tests before continuing.

2. **Stale `subs` entry.** Restore step 1, then in `removeLocked` change `delete(b.subs, ch)` to a no-op (comment it out) while leaving the `close(ch)`.
   Expected: `TestBroker_DropViaLogPublishRemovesFromBothIndexes` panics with `send on closed channel`, and `TestBroker_CancelRemovesSubscriberFromBothIndexes` fails on the idempotent second `cancel()` with `close of closed channel`.

3. **Unbounded send.** Restore step 2, then in the `TypeTaskLog` branch of `Publish` replace the whole `select { case ch <- e: default: ... }` with a bare `ch <- e`.
   Expected: `TestBroker_PublishNeverBlocksOnAStalledLogSubscriber` fails with "Publish blocked on a task-scoped subscriber that stopped reading" after 5 seconds. It must **fail**, not hang the package - if the run never completes, the publish loop is not on its own goroutine and the test needs fixing.

Restore the file to the Step 3 version and re-run: `go test ./internal/events/... -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Run the race detector**

From Git Bash:

```bash
PATH="/c/msys64/mingw64/bin:$PATH" CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/events/... -count=2 -timeout 180s
```

Expected: `ok relay/internal/events` with **no** `WARNING: DATA RACE` anywhere in the output. `-count=2` runs the concurrency test twice, which cheaply doubles the interleaving sample.

If the toolchain errors with `exit status 0xc0000139`, the wrong gcc is on PATH - the default Strawberry Perl gcc cannot build `-race`. Confirm `/c/msys64/mingw64/bin/gcc.exe` exists before retrying.

- [ ] **Step 7: Commit**

```bash
git add internal/events/broker.go internal/events/broker_test.go
git commit -m "feat(events): task-keyed logSubs index, HasLogSubscriber, task_log routing"
```

---

## Task 3: `AppendTaskLog` returns the inserted row plus `job_id`

Makes the epoch fence's outcome observable so Task 4 can gate the publish on it. **No migration** - `task_logs.id` and `task_logs.created_at` already exist (`internal/store/migrations/000001_initial.up.sql:76-82`).

**Files:**
- Modify: `internal/store/query/tasks.sql:48-56`
- Regenerate: `internal/store/tasks.sql.go` (**via `make generate` only**)
- Modify: `internal/store/store_test.go:241-283`
- Modify: `internal/worker/handler.go:508-526` (call site adaptation only, so the build stays green)

- [ ] **Step 1: Write the failing test**

Replace `internal/store/store_test.go` lines 267-283 (from the `// Log with matching epoch.` comment to the end of `TestAppendTaskLog_EpochGuarded`) with:

```go
	// Log with matching epoch: returns the inserted row plus the task's job id.
	first, err := q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stdout", Content: "hello\n", AssignmentEpoch: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, job.ID, first.JobID, "the row must carry the task's job id so the publish needs no second query")
	assert.NotZero(t, first.ID, "seq must be the task_logs.id BIGSERIAL")
	assert.True(t, first.CreatedAt.Valid)

	// seq is monotonically increasing across calls - it is the same value
	// GET /v1/tasks/{id}/logs pages by via ?since_seq.
	second, err := q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stderr", Content: "more\n", AssignmentEpoch: 1,
	})
	require.NoError(t, err)
	assert.Greater(t, second.ID, first.ID)

	// Log with a STALE epoch: the fence rejects it, so the statement returns
	// zero rows and sqlc surfaces pgx.ErrNoRows. This is the signal Task 4's
	// publish gate depends on - previously :exec collapsed "inserted one row"
	// and "inserted zero rows" into the same nil error.
	_, err = q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stdout", Content: "from zombie\n", AssignmentEpoch: 0,
	})
	assert.ErrorIs(t, err, pgx.ErrNoRows)

	// And it inserted nothing.
	logs, err := q.GetTaskLogs(ctx, task.ID)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, "hello\n", logs[0].Content)
	assert.Equal(t, "more\n", logs[1].Content)
}
```

`internal/store/store_test.go` already imports `pgx` (it is used at line 233).

- [ ] **Step 2: Run the test to verify it fails**

Requires Docker Desktop running.

Run: `go test -tags integration -p 1 ./internal/store/... -run TestAppendTaskLog_EpochGuarded -v -timeout 300s`
Expected: FAIL to compile - `assignment mismatch: 2 variables but q.AppendTaskLog returns 1 value` (the generated method still returns only `error`).

- [ ] **Step 3: Edit the SQL**

Replace `internal/store/query/tasks.sql` lines 48-56 with:

```sql
-- name: AppendTaskLog :one
-- Inserts a log chunk only if the caller's epoch matches the task's current
-- assignment, and returns the inserted row's id (the seq the polling endpoint
-- pages by) plus created_at plus the task's job_id - all from one round trip,
-- because this runs synchronously on the agent's gRPC recv goroutine and a
-- second query here would delay that worker's status and telemetry ingest too.
-- A stale chunk (from a reassigned or cancelled generation) matches no fence
-- row, inserts nothing, and returns zero rows -> pgx.ErrNoRows. Callers must
-- treat ErrNoRows as "stale, drop silently" and any other error as a real
-- failure worth logging.
WITH fence AS (
    SELECT id, job_id FROM tasks
    WHERE id = sqlc.arg(task_id) AND assignment_epoch = sqlc.arg(assignment_epoch)
), ins AS (
    INSERT INTO task_logs (task_id, stream, content)
    SELECT sqlc.arg(task_id), sqlc.arg(stream), sqlc.arg(content) FROM fence
    RETURNING id, created_at
)
SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence;
```

Named `sqlc.arg()` parameters (rather than `$1..$4`) are used deliberately: `task_id` appears in two places, and named args guarantee sqlc emits one parameter for it and keeps the generated `AppendTaskLogParams` field names (`TaskID`, `AssignmentEpoch`, `Stream`, `Content`) stable so the existing call site keeps compiling.

- [ ] **Step 4: Regenerate, then apply the CRLF discipline**

This repo is CRLF; sqlc emits LF and rewrites line endings across **every** generated file. Follow this exactly - do not skip it, or the one real change will be buried in hundreds of whitespace-only hunks.

```bash
make generate
git diff --ignore-all-space --stat
```

`git diff --ignore-all-space --stat` shows only the files with a **real content change**. Expect exactly one: `internal/store/tasks.sql.go`. For every other file that `git status` lists as modified, revert it:

```bash
git status --porcelain
# For each generated file that --ignore-all-space shows as unchanged:
git checkout -- <that file>
```

Then confirm:

```bash
git status --porcelain
```
Expected: only `internal/store/query/tasks.sql`, `internal/store/tasks.sql.go`, `internal/store/store_test.go` (and, after step 5, `internal/worker/handler.go`) are modified.

If `sqlc generate` **errors** on type inference for the CTE parameters, add explicit casts and re-run: `sqlc.arg(task_id)::uuid`, `sqlc.arg(assignment_epoch)::integer`, `sqlc.arg(stream)::text`, `sqlc.arg(content)::text`. Nothing else about the statement changes.

Now read the generated shape to confirm the names the rest of the plan assumes:

```bash
sed -n '1,60p' internal/store/tasks.sql.go
```

Expected: `type AppendTaskLogParams struct { TaskID pgtype.UUID; AssignmentEpoch int32; Stream string; Content string }` (field order may differ - all call sites use keyed literals, so order does not matter) and `type AppendTaskLogRow struct { ID int64; CreatedAt pgtype.Timestamptz; JobID pgtype.UUID }`, with `func (q *Queries) AppendTaskLog(ctx context.Context, arg AppendTaskLogParams) (AppendTaskLogRow, error)`. If sqlc named the row type or its fields differently, use **its** names in Steps 5 and in Task 4 - do not hand-edit the generated file.

- [ ] **Step 5: Adapt the one call site so the build stays green**

Only caller: `internal/worker/handler.go:520`. Replace lines 520-525 with:

```go
	_, err := h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID:          taskID,
		Stream:          stream,
		Content:         string(chunk.Content),
		AssignmentEpoch: int32(chunk.Epoch),
	})
	if err != nil {
		// pgx.ErrNoRows means the epoch fence rejected a stale chunk from a
		// previous assignment generation - expected, drop it silently. Anything
		// else is a real persist failure that used to be swallowed by `_ =`.
		// Never log chunk.Content: it is raw subprocess output and can contain
		// secrets a job's own script echoed.
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("worker: handleTaskLog AppendTaskLog %s: %v", chunk.TaskId, err)
		}
		return
	}
```

`internal/worker/handler.go` already imports `errors` (line 7), `log` (line 10) and `pgx` (line 19). No import change.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestAppendTaskLog_EpochGuarded -v -timeout 300s`
Expected: PASS.

Run: `go test ./... -timeout 120s` then `go vet ./...` then `make vet-integration`
Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go internal/store/store_test.go internal/worker/handler.go
git commit -m "feat(store): AppendTaskLog returns id, created_at, job_id from the fenced insert"
```

---

## Task 4: `handleTaskLog` publishes each persisted chunk

**Files:**
- Modify: `internal/worker/handler.go:508-535` (`handleTaskLog`, plus the payload type and counter)
- Modify: `internal/worker/export_test.go`
- Create: `internal/worker/tasklog_payload_test.go`
- Create: `internal/worker/handler_tasklog_integration_test.go`

- [ ] **Step 1: Write the failing unit test (payload contract)**

Create `internal/worker/tasklog_payload_test.go`. Note `package worker` (the internal test package, as `handler_telemetry_test.go:1` already does) and **no build tag** - this runs under `make test`.

```go
package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The task_log SSE payload must be field-identical to internal/api/tasks.go:56-61
// (logEntry) for seq/stream/content/created_at, plus task_id and job_id, so a
// consumer can define ONE log-line type and merge SSE frames with
// GET /v1/tasks/{id}/logs pages without a translation layer. If you are here
// because you want to add a field: adding one the polling endpoint cannot supply
// breaks that symmetry. step_index/step_total belong to
// docs/backlog/feature-2026-06-26-persist-expose-step-index-total.md.
func TestTaskLogEvent_JSONContract(t *testing.T) {
	ts := time.Date(2026, 8, 9, 14, 36, 25, 123000000, time.UTC)
	b, err := json.Marshal(taskLogEvent{
		TaskID:    "11111111-1111-1111-1111-111111111111",
		JobID:     "22222222-2222-2222-2222-222222222222",
		Seq:       1234,
		Stream:    "stdout",
		Content:   "line one\nline two\n",
		CreatedAt: ts,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t,
		[]string{"task_id", "job_id", "seq", "stream", "content", "created_at"},
		keys, "exact key set - no extra fields, no renames")

	assert.Equal(t, float64(1234), got["seq"], "seq is a JSON number, matching logEntry.Seq int64")
	assert.Equal(t, "stdout", got["stream"])
	assert.Equal(t, "line one\nline two\n", got["content"])
	assert.Equal(t, "2026-08-09T14:36:25.123Z", got["created_at"], "RFC3339, matching logEntry.CreatedAt time.Time")

	// SSE framing depends on this: handleEvents re-prefixes literal newlines in
	// Data with "data: ", so a payload containing a literal newline would split
	// into multiple data: lines. json.Marshal escapes \n inside strings, so a
	// correctly marshalled payload is exactly one line. This is why the payload
	// must never be built by string concatenation.
	assert.NotContains(t, string(b), "\n")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/worker/... -run TestTaskLogEvent_JSONContract -v -timeout 60s`
Expected: FAIL to compile - `undefined: taskLogEvent`.

- [ ] **Step 3: Implement the publish**

In `internal/worker/handler.go`, add `"encoding/json"` and `"sync/atomic"` to the import block, and `"relay/internal/events"` is already imported (line 14).

Insert immediately above `handleTaskLog` (i.e. above the current line 508 doc comment):

```go
// taskLogEvent is the JSON payload of an events.TypeTaskLog SSE frame. seq,
// stream, content and created_at are field-identical to the polling endpoint's
// logEntry (internal/api/tasks.go:56-61) so a consumer can merge live frames
// with GET /v1/tasks/{id}/logs pages using one type. task_id and job_id are
// added so a "?task_id="-only subscriber can route and cache-key by job without
// a second request. seq is task_logs.id, so it is a total order per task and an
// exact dedupe key against the backfill.
type taskLogEvent struct {
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// taskLogPublishes counts chunks that got past the HasLogSubscriber fast path
// and were marshalled + published. Test-only observability (read via
// TaskLogPublishesForTest in export_test.go) for the "nothing is marshalled when
// nobody is tailing" guarantee, which is otherwise unobservable from outside.
// Production code never reads it.
var taskLogPublishes atomic.Int64
```

Then replace `handleTaskLog` in full:

```go
// handleTaskLog appends a log chunk from an agent and, if anyone is tailing that
// task, publishes it to the SSE broker.
//
// This runs synchronously on the Connect recv goroutine (handler.go:117-128),
// which also carries that worker's status, inventory and telemetry messages, so
// everything below is deliberately cheap: exactly one DB round trip (the insert
// itself returns the job id and seq), one map lookup when nobody is watching,
// and a non-blocking Publish. Do not add a query, a goroutine, or a queue here.
func (h *Handler) handleTaskLog(ctx context.Context, chunk *relayv1.TaskLogChunk) {
	var taskID pgtype.UUID
	if err := taskID.Scan(chunk.TaskId); err != nil {
		return
	}

	stream := "stdout"
	if chunk.Stream == relayv1.LogStream_LOG_STREAM_STDERR {
		stream = "stderr"
	}

	row, err := h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID:          taskID,
		Stream:          stream,
		Content:         string(chunk.Content),
		AssignmentEpoch: int32(chunk.Epoch),
	})
	if err != nil {
		// pgx.ErrNoRows means the epoch fence rejected a stale chunk from a
		// previous assignment generation (the task was requeued or cancelled, and
		// both bump assignment_epoch). Expected - drop it silently, and in
		// particular do NOT publish it: a zombie agent's output would otherwise
		// appear in a live view and then vanish on refresh, because it was
		// correctly never stored. Anything else is a real persist failure.
		// Never log chunk.Content: it is raw subprocess output and can contain
		// secrets a job's own script echoed.
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("worker: handleTaskLog AppendTaskLog %s: %v", chunk.TaskId, err)
		}
		return
	}

	// Persistence is unconditional and strictly precedes any publish; the publish
	// is derived from the stored row, so no line is ever published unstored.
	taskIDStr := uuidStr(taskID)
	if !h.broker.HasLogSubscriber(taskIDStr) {
		return // steady state: one map lookup, no marshal, no allocation
	}

	taskLogPublishes.Add(1)
	data, err := json.Marshal(taskLogEvent{
		TaskID:    taskIDStr,
		JobID:     uuidStr(row.JobID),
		Seq:       row.ID,
		Stream:    stream,
		Content:   string(chunk.Content),
		CreatedAt: row.CreatedAt.Time,
	})
	if err != nil {
		log.Printf("worker: handleTaskLog marshal %s: %v", chunk.TaskId, err)
		return
	}

	h.broker.Publish(events.Event{
		Type:   events.TypeTaskLog,
		JobID:  uuidStr(row.JobID),
		TaskID: taskIDStr,
		Data:   data,
	})
}
```

- [ ] **Step 4: Run the unit test to verify it passes**

Run: `go test ./internal/worker/... -run TestTaskLogEvent_JSONContract -v -timeout 60s`
Expected: PASS.

Run: `go test ./... -timeout 120s` and `go vet ./...`
Expected: green.

- [ ] **Step 5: Write the failing integration tests**

Add to `internal/worker/export_test.go` (integration-tagged, `package worker`):

```go
// HandleTaskLog exposes the unexported handleTaskLog method for integration
// tests in package worker_test.
func (h *Handler) HandleTaskLog(ctx context.Context, chunk *relayv1.TaskLogChunk) {
	h.handleTaskLog(ctx, chunk)
}

// TaskLogPublishesForTest reports how many chunks have been marshalled and
// published as task_log events since process start. Lets a test assert the
// HasLogSubscriber fast path really skipped the marshal, which is otherwise
// unobservable from outside the package.
func TaskLogPublishesForTest() int64 {
	return taskLogPublishes.Load()
}
```

Create `internal/worker/handler_tasklog_integration_test.go`:

```go
//go:build integration

package worker_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskLogFrame struct {
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// seedClaimedTask creates a user, job, worker and task, claims the task (which
// bumps assignment_epoch to 1) and returns the ids plus the current epoch.
func seedClaimedTask(t *testing.T, ctx context.Context, q *store.Queries, email, hostname string) (jobID, taskID pgtype.UUID, epoch int32) {
	t.Helper()
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: email, IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: hostname, Hostname: hostname, CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	return job.ID, task.ID, claimed.AssignmentEpoch
}

func TestHandleTaskLog_PublishesToATaskScopedSubscriber(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	jobID, taskID, epoch := seedClaimedTask(t, ctx, q, "logs1@example.com", "w-logs1")
	taskIDStr := h.UUIDStringForTest(taskID)
	jobIDStr := h.UUIDStringForTest(jobID)

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Stream: relayv1.LogStream_LOG_STREAM_STDERR,
		Content: []byte("line one\nline two\n"), Epoch: int32(epoch),
	})

	var e events.Event
	select {
	case e = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no task_log event was published")
	}
	require.Equal(t, events.TypeTaskLog, e.Type)
	require.Equal(t, taskIDStr, e.TaskID)

	var frame taskLogFrame
	require.NoError(t, json.Unmarshal(e.Data, &frame))
	assert.Equal(t, taskIDStr, frame.TaskID)
	assert.Equal(t, jobIDStr, frame.JobID)
	assert.Equal(t, "stderr", frame.Stream)
	assert.Equal(t, "line one\nline two\n", frame.Content)
	assert.NotZero(t, frame.Seq)

	// frame.Seq must be the row the polling endpoint will return - that identity
	// is what makes the client-side backfill join exact.
	page, err := q.GetTaskLogsPage(ctx, store.GetTaskLogsPageParams{TaskID: taskID, ID: 0, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, page[0].ID, frame.Seq)
	assert.Equal(t, page[0].Content, frame.Content)
}

func TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, epoch := seedClaimedTask(t, ctx, q, "logs2@example.com", "w-logs2")
	taskIDStr := h.UUIDStringForTest(taskID)

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	// A zombie agent from the previous generation. epoch-1 is stale because
	// ClaimTaskForWorker already bumped assignment_epoch to `epoch`.
	h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("from zombie\n"), Epoch: int32(epoch) - 1,
	})
	select {
	case e := <-ch:
		t.Fatalf("a stale-epoch chunk must not be published: %s", e.Data)
	case <-time.After(200 * time.Millisecond):
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Empty(t, rows, "a stale-epoch chunk must not be stored")

	// Paired positive control on the SAME code path and the same subscriber: at
	// the current epoch the chunk is both stored and published. Without this a
	// broken HandleTaskLog that did nothing at all would pass the assertions above.
	h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("current\n"), Epoch: int32(epoch),
	})
	select {
	case e := <-ch:
		require.Contains(t, string(e.Data), "current")
	case <-time.After(5 * time.Second):
		t.Fatal("positive control: a current-epoch chunk was not published")
	}
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestHandleTaskLog_NoSubscriberSkipsMarshalButStillPersists(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, epoch := seedClaimedTask(t, ctx, q, "logs3@example.com", "w-logs3")
	taskIDStr := h.UUIDStringForTest(taskID)

	chunk := func(s string) *relayv1.TaskLogChunk {
		return &relayv1.TaskLogChunk{TaskId: taskIDStr, Content: []byte(s), Epoch: int32(epoch)}
	}

	// NEGATIVE: nobody tailing. A job-scoped subscription must not count either.
	_, cancelJobSub := broker.Subscribe(events.Filter{JobID: "some-other-job"})
	defer cancelJobSub()

	before := worker.TaskLogPublishesForTest()
	for i := 0; i < 3; i++ {
		h.HandleTaskLog(ctx, chunk("quiet\n"))
	}
	assert.Equal(t, before, worker.TaskLogPublishesForTest(),
		"with no log subscriber, nothing may be marshalled or published")

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 3, "the fast path must never skip the write")

	// POSITIVE CONTROL on the same code path: attach a task-scoped subscriber and
	// the very same call now bumps the counter. This is what stops the negative
	// assertion above from passing vacuously if the probe or ingest ever breaks.
	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()
	before = worker.TaskLogPublishesForTest()
	for i := 0; i < 3; i++ {
		h.HandleTaskLog(ctx, chunk("watched\n"))
	}
	assert.Equal(t, before+3, worker.TaskLogPublishesForTest())
	for i := 0; i < 3; i++ {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("positive control: only %d of 3 watched chunks were published", i)
		}
	}
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 6)
}
```

- [ ] **Step 6: Run the integration tests to verify they pass**

Docker Desktop must be running.

Run: `go test -tags integration -p 1 ./internal/worker/... -run 'TestHandleTaskLog_' -v -timeout 300s`
Expected: PASS (3 tests).

- [ ] **Step 7: Prove the fast-path test is not vacuous**

Temporarily invert the guard in `handleTaskLog`:

```go
	if false && !h.broker.HasLogSubscriber(taskIDStr) {
```

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestHandleTaskLog_NoSubscriberSkipsMarshalButStillPersists -v -timeout 300s`
Expected: FAIL - the counter advanced by 3 with no subscriber attached. If it still passes, the counter is not on the path the guard protects and the test is worthless.

Now delete the guard's `return` body instead and restore the condition - no: revert cleanly to the Step 3 version and re-run:

Run: `go test -tags integration -p 1 ./internal/worker/... -run 'TestHandleTaskLog_' -v -timeout 300s`
Expected: PASS (3 tests).

- [ ] **Step 8: Commit**

```bash
git add internal/worker/handler.go internal/worker/export_test.go internal/worker/tasklog_payload_test.go internal/worker/handler_tasklog_integration_test.go
git commit -m "feat(worker): publish each persisted task-log chunk behind HasLogSubscriber"
```

---

## Task 5: `GET /v1/events?task_id=` and the `dropped` frame

**Files:**
- Modify: `internal/api/events.go` (whole file)
- Create: `internal/api/events_task_log_integration_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/events_task_log_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServerWithBroker is newTestServer (api_test.go:27-35) but hands back the
// broker so a test can publish directly and drive the SSE handler's delivery and
// drop paths without an agent.
func newTestServerWithBroker(t *testing.T) (*api.Server, *store.Queries, *events.Broker) {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	broker := events.NewBroker()
	srv := api.New(pool, q, broker, worker.NewRegistry(), nil, 0, 0, 0, 0)
	return srv, q, broker
}

// seedTaskViaAPI submits a one-task job and returns (jobID, taskID) as strings.
func seedTaskViaAPI(t *testing.T, srv *api.Server, token string) (string, string) {
	t.Helper()
	body := `{"name":"j","tasks":[{"name":"t","command":["echo","hi"]}]}`
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var job map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&job))
	return job["id"].(string), job["tasks"].([]any)[0].(map[string]any)["id"].(string)
}

func TestEvents_TaskIDValidation(t *testing.T) {
	srv, q, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-valid@example.com", false)
	token := createTestToken(t, q, user.ID)
	_, taskID := seedTaskViaAPI(t, srv, token)

	do := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/v1/events"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// Malformed UUID -> 400. A typo must not yield a stream that hangs open
	// forever looking like "the task has no output".
	rec := do("?task_id=not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Well-formed but unknown -> 404.
	rec = do("?task_id=11111111-1111-1111-1111-111111111111")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// ?job_id= validation is deliberately UNCHANGED: an unknown job is still an
	// open, silently empty stream. It is an existing contract with existing
	// clients; the asymmetry is intentional and documented in README.md.
	// (Served with a cancelled context so the handler returns immediately.)
	req := httptest.NewRequest("GET", "/v1/events?job_id=not-a-uuid", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req.WithContext(ctx))
	assert.NotEqual(t, http.StatusBadRequest, rec.Code)

	// Valid task -> a live SSE stream. Positive control for the two rejections
	// above: it proves the handler can reach 200 on this same path at all.
	req = httptest.NewRequest("GET", "/v1/events?task_id="+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	streamCtx, streamCancel := context.WithCancel(req.Context())
	defer streamCancel()
	rec = httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); srv.Handler().ServeHTTP(rec, req.WithContext(streamCtx)) }()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	streamCancel()
	<-done
}

// gateWriter is an http.ResponseWriter whose first Write blocks until release is
// closed. That pins handleEvents inside one write for as long as the test needs,
// so the broker can be filled and the subscription drop-closed deterministically
// - no sleeps and no assumption about how fast the handler drains.
type gateWriter struct {
	mu      sync.Mutex
	hdr     http.Header
	buf     bytes.Buffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGateWriter() *gateWriter {
	return &gateWriter{
		hdr:     make(http.Header),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *gateWriter) Header() http.Header  { return g.hdr }
func (g *gateWriter) WriteHeader(int)      {}
func (g *gateWriter) Flush()               {}
func (g *gateWriter) Write(p []byte) (int, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buf.Write(p)
}
func (g *gateWriter) body() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buf.String()
}

func TestEvents_DroppedFrameOnSlowConsumer(t *testing.T) {
	srv, q, broker := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-dropped@example.com", false)
	token := createTestToken(t, q, user.ID)
	_, taskID := seedTaskViaAPI(t, srv, token)

	req := httptest.NewRequest("GET", "/v1/events?task_id="+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	gw := newGateWriter()
	done := make(chan struct{})
	go func() { defer close(done); srv.Handler().ServeHTTP(gw, req) }()

	require.Eventually(t, func() bool { return broker.HasLogSubscriber(taskID) },
		5*time.Second, 5*time.Millisecond, "handler never subscribed")

	pub := func() {
		broker.Publish(events.Event{
			Type: events.TypeTaskLog, TaskID: taskID, Data: []byte(`{"seq":1}`),
		})
	}
	// One event, which the handler pops and then blocks writing.
	pub()
	select {
	case <-gw.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never entered Write")
	}
	// 70 more: 64 fill the buffer, the next is a drop-close.
	for i := 0; i < 70; i++ {
		pub()
	}
	require.False(t, broker.HasLogSubscriber(taskID), "broker should have dropped the stalled subscriber")

	close(gw.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the broker closed its channel")
	}

	body := gw.body()
	assert.Contains(t, body, "event: dropped\ndata: {\"reason\":\"slow_consumer\"}\n\n")
	assert.True(t, strings.HasSuffix(body, "event: dropped\ndata: {\"reason\":\"slow_consumer\"}\n\n"),
		"the dropped frame must be the LAST frame written")
}

func TestEvents_NoDroppedFrameWhenTheClientDisconnects(t *testing.T) {
	srv, q, broker := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-nodrop@example.com", false)
	token := createTestToken(t, q, user.ID)
	_, taskID := seedTaskViaAPI(t, srv, token)

	req := httptest.NewRequest("GET", "/v1/events?task_id="+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ctx, cancel := context.WithCancel(req.Context())
	gw := newGateWriter()
	close(gw.release) // never block; we want the handler to write normally
	done := make(chan struct{})
	go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(ctx)) }()

	require.Eventually(t, func() bool { return broker.HasLogSubscriber(taskID) },
		5*time.Second, 5*time.Millisecond)

	// Positive control first: one real frame proves the write path works, so the
	// "no dropped frame" assertion below cannot pass because nothing was written.
	broker.Publish(events.Event{
		Type: events.TypeTaskLog, TaskID: taskID,
		Data: []byte(`{"seq":1,"stream":"stdout","content":"line one\nline two\n"}`),
	})
	require.Eventually(t, func() bool { return strings.Contains(gw.body(), "event: task_log") },
		5*time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return on client disconnect")
	}
	assert.NotContains(t, gw.body(), "event: dropped",
		"a client that went away must not be told it fell behind")

	// Framing: a marshalled payload contains no literal newline (json escapes \n),
	// so each task_log event is exactly ONE data: line. handleEvents re-prefixes
	// literal newlines with "data: ", so a hand-concatenated payload would split
	// here and corrupt every multi-line chunk.
	frame := gw.body()
	i := strings.Index(frame, "event: task_log")
	require.GreaterOrEqual(t, i, 0)
	frame = frame[i:]
	assert.Equal(t, 1, strings.Count(frame, "data: "), "exactly one data: line per task_log frame")
	line := strings.TrimPrefix(strings.SplitN(strings.SplitN(frame, "\n", 2)[1], "\n", 2)[0], "data: ")
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &payload))
	assert.Equal(t, "line one\nline two\n", payload["content"], "newlines survive the round trip")
}

func TestEvents_DeliveryMatrix(t *testing.T) {
	srv, q, broker := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-matrix@example.com", false)
	token := createTestToken(t, q, user.ID)
	jobID, taskID := seedTaskViaAPI(t, srv, token)

	// Four concurrent subscriptions through the real HTTP handler.
	open := func(query string) (*gateWriter, func()) {
		req := httptest.NewRequest("GET", "/v1/events"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		ctx, cancel := context.WithCancel(req.Context())
		gw := newGateWriter()
		close(gw.release)
		done := make(chan struct{})
		go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(ctx)) }()
		return gw, func() { cancel(); <-done }
	}

	global, closeGlobal := open("")
	defer closeGlobal()
	jobOnly, closeJobOnly := open("?job_id=" + jobID)
	defer closeJobOnly()
	taskOnly, closeTaskOnly := open("?task_id=" + taskID)
	defer closeTaskOnly()
	both, closeBoth := open("?job_id=" + jobID + "&task_id=" + taskID)
	defer closeBoth()

	require.Eventually(t, func() bool { return broker.HasLogSubscriber(taskID) },
		5*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond) // let all four Subscribe calls land

	broker.Publish(events.Event{Type: "task", JobID: jobID, Data: []byte(`{"id":"x","status":"running"}`)})
	broker.Publish(events.Event{Type: events.TypeTaskLog, JobID: jobID, TaskID: taskID, Data: []byte(`{"seq":1}`)})

	// Positive expectations first, so the negatives below cannot pass vacuously.
	for name, gw := range map[string]*gateWriter{"global": global, "jobOnly": jobOnly, "both": both} {
		require.Eventually(t, func() bool { return strings.Contains(gw.body(), "event: task") },
			5*time.Second, 5*time.Millisecond, name+" should receive the status event")
	}
	for name, gw := range map[string]*gateWriter{"taskOnly": taskOnly, "both": both} {
		require.Eventually(t, func() bool { return strings.Contains(gw.body(), "event: task_log") },
			5*time.Second, 5*time.Millisecond, name+" should receive the task_log event")
	}

	// Negatives. This is the whole point: a plain GET /v1/events (which relay
	// watch opens) must never become a cluster-wide log firehose.
	assert.NotContains(t, global.body(), "task_log", "a global subscriber must receive NO task_log frames")
	assert.NotContains(t, jobOnly.body(), "task_log", "a job-scoped subscriber must receive NO task_log frames")
	assert.NotContains(t, taskOnly.body(), "event: task\n",
		"?task_id= alone must not become a global status subscription")
}
```

Add `"context"` to that file's import block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_' -v -timeout 300s`
Expected: FAIL - `TestEvents_TaskIDValidation` gets 200 instead of 400/404 (the parameter is ignored today), `TestEvents_DroppedFrameOnSlowConsumer` finds no `event: dropped`, and `TestEvents_DeliveryMatrix` fails because nothing subscribes with a `TaskID`.

- [ ] **Step 3: Implement the handler**

Replace `internal/api/events.go` in full:

```go
package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"relay/internal/events"

	"github.com/jackc/pgx/v5"
)

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Validate ?task_id= BEFORE touching the response headers, so an error can be
	// written as JSON by writeError instead of arriving inside a half-started
	// text/event-stream response.
	//
	// One GetTask per CONNECTION (never per chunk). It exists to stop the worst
	// UX failure of live tailing: a typo'd id yielding a stream that hangs open
	// forever, silently, looking like "the task produced no output".
	//
	// No ownership check, deliberately: GET /v1/events and
	// GET /v1/tasks/{id}/logs are both auth(...)-only with no per-owner gate
	// (server.go:171, server.go:124), so any authenticated user can already read
	// any task's logs. Adding a live view of data the same token already reads
	// introduces no escalation, and gating only here would accomplish nothing.
	var logTaskID string
	if raw := r.URL.Query().Get("task_id"); raw != "" {
		taskID, err := parseUUID(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid task id")
			return
		}
		if _, err := s.q.GetTask(ctx, taskID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "task not found")
			} else {
				writeError(w, http.StatusInternalServerError, "get task failed")
			}
			return
		}
		// Canonical lowercase-hex form, so the broker key matches the one
		// handleTaskLog derives from the chunk's task id (worker/handler.go:674).
		logTaskID = uuidStr(taskID)
	}

	// ?job_id= is deliberately NOT validated: an unknown job has always yielded
	// an open, permanently empty stream, and that is an existing contract with
	// existing clients. The asymmetry with task_id is intentional.
	jobID := r.URL.Query().Get("job_id")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := s.broker.Subscribe(events.Filter{JobID: jobID, TaskID: logTaskID})
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	// Subscribe-then-flush: when the client's request returns 200 the
	// subscription is already live, which is what lets a consumer subscribe first
	// and then backfill via ?since_seq without a gap.
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			// The client went away. Nothing to tell it.
			return
		case e, ok := <-ch:
			if !ok {
				// The broker dropped us for falling behind. Say so explicitly:
				// without this frame a Go consumer sees StreamEvents return nil,
				// indistinguishable from a normal end of stream. Additive and
				// safe - clients switch on event type and ignore unknowns.
				// The recovery is to re-backfill from the last seq seen.
				fmt.Fprint(w, "event: dropped\ndata: {\"reason\":\"slow_consumer\"}\n\n")
				flusher.Flush()
				return
			}
			// Replace newlines in data to keep SSE frame valid.
			// Per SSE spec, each line in the data value needs its own "data:" prefix.
			dataStr := strings.ReplaceAll(string(e.Data), "\n", "\ndata: ")
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, dataStr)
			flusher.Flush()
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_|TestSSESubscribe' -v -timeout 300s`
Expected: PASS (4 new tests plus the existing `TestSSESubscribe`, which must be unaffected - it opens `/v1/events` with no query at all).

Run: `go test ./... -timeout 120s`, `go vet ./...`, `make vet-integration`
Expected: green.

- [ ] **Step 5: Prove the dropped-frame test is not vacuous**

Temporarily change the `!ok` branch back to a bare `return` (delete the `Fprint` and `Flush`).

Run: `go test -tags integration -p 1 ./internal/api/... -run TestEvents_DroppedFrameOnSlowConsumer -v -timeout 300s`
Expected: FAIL - no `event: dropped` in the body. If it passes, the gate never actually caused a drop; check that `broker.HasLogSubscriber(taskID)` went false before `close(gw.release)`.

Revert and re-run:

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_' -v -timeout 300s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/events.go internal/api/events_task_log_integration_test.go
git commit -m "feat(api): GET /v1/events?task_id= streams task_log frames, plus a dropped frame"
```

---

## Task 6: Raise `relayclient.StreamEvents`' scanner token limit

Independent of every other task - the only one the conductor can safely run in parallel. Pure unit test, no Docker.

`bufio.NewScanner`'s default token limit is 64 KiB. Agent chunks are up to ~32 KiB (`os/exec`'s copy buffer feeding `chunkWriter`, `internal/agent/runner.go:285-309`) and JSON escaping can nearly double that, so a worst-case log frame exceeds the limit and `StreamEvents` fails with `bufio.Scanner: token too long`. Status payloads are tiny, which is why this has never bitten. This is shared-client hardening required by any Go consumer of log frames (including the future MCP live-logs item), so it lands here.

**Files:**
- Modify: `internal/relayclient/client.go:141`
- Modify: `internal/relayclient/client_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/relayclient/client_test.go`:

```go
func TestStreamEvents_ParsesALargeDataLine(t *testing.T) {
	// 512 KiB of content on ONE data: line - far past bufio.Scanner's 64 KiB
	// default. A single agent log chunk can be ~32 KiB raw, and JSON escaping
	// can nearly double that, so this is the shape of a real worst-case task_log
	// frame with headroom.
	big := strings.Repeat("x", 512*1024)
	payload, err := json.Marshal(map[string]string{"content": big})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "event: task_log\ndata: %s\n\n", payload)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	var got []SSEEvent
	err = c.StreamEvents(context.Background(), "/v1/events", nil, func(e SSEEvent) bool {
		got = append(got, e)
		return true
	})
	require.NoError(t, err, "large data: line must not fail with bufio.Scanner: token too long")
	require.Len(t, got, 1)
	require.Equal(t, "task_log", got[0].Type)

	var out struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal([]byte(got[0].Data), &out))
	require.Len(t, out.Content, 512*1024, "the payload must round-trip whole, not truncated")
}
```

Add `"encoding/json"` and `"strings"` to that file's import block.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/relayclient/... -run TestStreamEvents_ParsesALargeDataLine -v -timeout 60s`
Expected: FAIL - `require.NoError` reports `bufio.Scanner: token too long`, and `got` is empty because the handler was never called.

- [ ] **Step 3: Raise the limit**

In `internal/relayclient/client.go`, replace line 141:

```go
	scanner := bufio.NewScanner(resp.Body)
```

with:

```go
	scanner := bufio.NewScanner(resp.Body)
	// bufio.Scanner's default token limit is 64 KiB, which a single task_log
	// frame can exceed: an agent chunk is up to ~32 KiB raw
	// (internal/agent/runner.go:285-309) and JSON escaping can nearly double
	// that. Without this, StreamEvents fails the whole stream with
	// "bufio.Scanner: token too long" on one oversized log line. Status payloads
	// are tiny, so this never bit before task_log existed. 1 MiB max token.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/relayclient/... -v -timeout 60s`
Expected: PASS - the new test plus the existing `TestStreamEvents_ParsesFrames` and `TestStreamEvents_HandlerReturnFalseStops`. Those two are the paired positive control that small frames still parse and that early-stop still works.

- [ ] **Step 5: Commit**

```bash
git add internal/relayclient/client.go internal/relayclient/client_test.go
git commit -m "fix(relayclient): raise StreamEvents scanner token limit to 1 MiB"
```

---

## Task 7: End-to-end - agent chunk to a subscribed SSE client

The only test that exercises the whole path: `worker.Handler.handleTaskLog` -> fenced insert -> `Broker.Publish` -> `handleEvents` -> HTTP SSE -> `relayclient.StreamEvents`. Depends on Tasks 2, 4, 5 and 6 all being in.

**Files:**
- Create: `internal/api/events_task_log_e2e_integration_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/events_task_log_e2e_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/relayclient"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type e2eLogLine struct {
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// claimTaskForE2E bumps the task's assignment_epoch to 1 so chunks stamped with
// epoch 1 pass the fence.
func claimTaskForE2E(t *testing.T, ctx context.Context, q *store.Queries, taskID string) int32 {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, id.Scan(taskID))
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "e2e", Hostname: "e2e-" + taskID[:8], CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: id, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	return claimed.AssignmentEpoch
}

func TestEndToEnd_AgentChunkReachesASubscribedSSEClient(t *testing.T) {
	srv, q, broker := newTestServerWithBroker(t)
	ctx := context.Background()
	user := createTestUser(t, q, "Alice", "e2e-logs@example.com", false)
	token := createTestToken(t, q, user.ID)
	jobID, taskID := seedTaskViaAPI(t, srv, token)
	epoch := claimTaskForE2E(t, ctx, q, taskID)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	pool := newTestPoolFromQueries(t) // see note below
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	// A single connection carrying BOTH families: the job's status events and the
	// selected task's logs. This is the shape the job-detail log tab uses, and it
	// is why ?follow=1 was rejected - that would need two connections per screen.
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	c := relayclient.NewClient(ts.URL, token)

	subscribed := make(chan struct{})
	var mu sync.Mutex
	var logs []e2eLogLine
	var statusTypes []string
	streamErr := make(chan error, 1)

	go func() {
		streamErr <- c.StreamEvents(streamCtx,
			"/v1/events?job_id="+jobID+"&task_id="+taskID,
			func() bool { close(subscribed); return true },
			func(e relayclient.SSEEvent) bool {
				mu.Lock()
				defer mu.Unlock()
				switch e.Type {
				case "task_log":
					var l e2eLogLine
					require.NoError(t, json.Unmarshal([]byte(e.Data), &l))
					logs = append(logs, l)
				default:
					statusTypes = append(statusTypes, e.Type)
				}
				return true
			})
	}()

	// onSubscribed fires after the 200, and handleEvents subscribes before it
	// flushes, so the subscription is live here. No sleep needed.
	<-subscribed

	const n = 5
	for i := 0; i < n; i++ {
		h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
			TaskId:  taskID,
			Stream:  relayv1.LogStream_LOG_STREAM_STDOUT,
			Content: []byte(fmt.Sprintf("line %d\nmore %d\n", i, i)),
			Epoch:   epoch,
		})
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(logs) == n
	}, 10*time.Second, 10*time.Millisecond, "not all task_log frames arrived")

	mu.Lock()
	got := append([]e2eLogLine(nil), logs...)
	mu.Unlock()

	// Payload symmetry with the polling endpoint, asserted against the real
	// response rather than a hand-written expectation.
	req := httptest.NewRequest("GET", "/v1/tasks/"+taskID+"/logs?limit=200", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var page struct {
		Items []struct {
			Seq       int64     `json:"seq"`
			Stream    string    `json:"stream"`
			Content   string    `json:"content"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"items"`
		NextSeq int64 `json:"next_seq"`
		Total   int64 `json:"total"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&page))
	require.Len(t, page.Items, n)

	for i := range got {
		assert.Equal(t, taskID, got[i].TaskID)
		assert.Equal(t, jobID, got[i].JobID)
		assert.Equal(t, page.Items[i].Seq, got[i].Seq, "seq must be the polling endpoint's seq")
		assert.Equal(t, page.Items[i].Stream, got[i].Stream)
		assert.Equal(t, page.Items[i].Content, got[i].Content)
		assert.Equal(t, fmt.Sprintf("line %d\nmore %d\n", i, i), got[i].Content,
			"multi-line content survives SSE framing intact")
		if i > 0 {
			assert.Greater(t, got[i].Seq, got[i-1].Seq, "seq increases monotonically in publish order")
		}
	}

	// Status events on the SAME connection are unaffected. Positive control that
	// this stream can carry status at all.
	broker.Publish(events.Event{Type: "job", JobID: jobID, Data: []byte(`{"id":"x","status":"done"}`)})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, ty := range statusTypes {
			if ty == "job" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "status events must still be delivered alongside logs")

	streamCancel()
	<-streamErr
}

func TestEndToEnd_BackfillJoinIsGaplessAndDeduped(t *testing.T) {
	srv, q, broker := newTestServerWithBroker(t)
	ctx := context.Background()
	user := createTestUser(t, q, "Alice", "e2e-backfill@example.com", false)
	token := createTestToken(t, q, user.ID)
	_, taskID := seedTaskViaAPI(t, srv, token)
	epoch := claimTaskForE2E(t, ctx, q, taskID)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	pool := newTestPoolFromQueries(t)
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	// Step 1 of the documented contract: SUBSCRIBE FIRST and buffer events.
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	c := relayclient.NewClient(ts.URL, token)
	subscribed := make(chan struct{})
	var mu sync.Mutex
	var buffered []e2eLogLine
	go func() {
		_ = c.StreamEvents(streamCtx, "/v1/events?task_id="+taskID,
			func() bool { close(subscribed); return true },
			func(e relayclient.SSEEvent) bool {
				if e.Type != "task_log" {
					return true
				}
				var l e2eLogLine
				require.NoError(t, json.Unmarshal([]byte(e.Data), &l))
				mu.Lock()
				buffered = append(buffered, l)
				mu.Unlock()
				return true
			})
	}()
	<-subscribed

	const total = 20
	// Emit half, page, then emit the rest - so the pages and the events overlap,
	// which is exactly the window a reversed order would leave a hole in.
	emit := func(from, to int) {
		for i := from; i < to; i++ {
			h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
				TaskId: taskID, Content: []byte(fmt.Sprintf("chunk-%02d\n", i)), Epoch: epoch,
			})
		}
	}
	emit(0, 10)

	// Step 2: page ?since_seq=0 until next_seq == 0, recording maxSeq.
	type item struct {
		Seq     int64  `json:"seq"`
		Content string `json:"content"`
	}
	var backfill []item
	var maxSeq int64
	sinceSeq := int64(0)
	for {
		req := httptest.NewRequest("GET",
			fmt.Sprintf("/v1/tasks/%s/logs?limit=3&since_seq=%d", taskID, sinceSeq), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var page struct {
			Items   []item `json:"items"`
			NextSeq int64  `json:"next_seq"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&page))
		backfill = append(backfill, page.Items...)
		for _, it := range page.Items {
			if it.Seq > maxSeq {
				maxSeq = it.Seq
			}
		}
		if page.NextSeq == 0 {
			break
		}
		sinceSeq = page.NextSeq
		emit(10, 11) // keep producing while the client pages
	}
	emit(11, total)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(buffered) >= total-len(backfill)
	}, 10*time.Second, 10*time.Millisecond, "live events did not cover the tail")

	// Step 3: render the backfill, then apply events with seq > maxSeq.
	merged := append([]item(nil), backfill...)
	mu.Lock()
	for _, l := range buffered {
		if l.Seq > maxSeq {
			merged = append(merged, item{Seq: l.Seq, Content: l.Content})
		}
	}
	mu.Unlock()
	sort.Slice(merged, func(i, j int) bool { return merged[i].Seq < merged[j].Seq })

	// The reconstruction must equal the DB exactly: no gap, no duplicate.
	var id pgtype.UUID
	require.NoError(t, id.Scan(taskID))
	rows, err := q.GetTaskLogs(ctx, id)
	require.NoError(t, err)
	require.Len(t, merged, len(rows), "backfill+events must reconstruct every row exactly once")
	for i := range rows {
		assert.Equal(t, rows[i].ID, merged[i].Seq)
		assert.Equal(t, rows[i].Content, merged[i].Content)
	}

	streamCancel()
}
```

Add `"relay/internal/events"` to the import block (used for the status publish).

**Note on `newTestPoolFromQueries`:** `worker.NewHandler` needs a `*pgxpool.Pool`, but `newTestServerWithBroker` (Task 5) currently discards the pool it created. Fix this by changing `newTestServerWithBroker` in `internal/api/events_task_log_integration_test.go` to also return the pool:

```go
func newTestServerWithBroker(t *testing.T) (*api.Server, *store.Queries, *events.Broker, *pgxpool.Pool) {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	broker := events.NewBroker()
	srv := api.New(pool, q, broker, worker.NewRegistry(), nil, 0, 0, 0, 0)
	return srv, q, broker, pool
}
```

Add `"github.com/jackc/pgx/v5/pgxpool"` to that file's imports, update its four call sites to `srv, q, broker, _ := newTestServerWithBroker(t)`, and in this new file use `srv, q, broker, pool := newTestServerWithBroker(t)` and delete the `newTestPoolFromQueries` references. Do **not** call `newTestPool` twice - that would spin up a second Postgres container per test with a different database.

- [ ] **Step 2: Run the tests to verify they fail**

If Tasks 2/4/5/6 are all committed this should already pass. Before running, confirm you get a **meaningful RED** by proving the test exercises the real path: temporarily revert the `HasLogSubscriber` publish block in `handleTaskLog` to the pre-Task-4 `return`-after-insert shape.

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestEndToEnd_' -v -timeout 300s`
Expected: FAIL - `not all task_log frames arrived` (0 of 5) and `live events did not cover the tail`. This is the proof that the end-to-end tests actually depend on the publish, and not on some other path.

Restore `handleTaskLog`.

- [ ] **Step 3: Run the tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestEndToEnd_' -v -timeout 300s`
Expected: PASS (2 tests).

Run: `make vet-integration`
Expected: no output (this catches the `newTestServerWithBroker` signature change across all its call sites).

- [ ] **Step 4: Commit**

```bash
git add internal/api/events_task_log_e2e_integration_test.go internal/api/events_task_log_integration_test.go
git commit -m "test(api): end-to-end agent chunk to SSE task_log frame, plus gapless backfill join"
```

---

## Task 8: Document the events surface in README.md

**Files:**
- Modify: `README.md:1299-1305`

- [ ] **Step 1: Replace the Events section**

Replace `README.md` lines 1299-1305 (the `### Events (Server-Sent Events)` heading through the sentence ending "JSON data payloads.") with:

````markdown
### Events (Server-Sent Events)

```
GET /v1/events                                  # all job/task/worker status changes
GET /v1/events?job_id=<id>                      # status changes for one job
GET /v1/events?task_id=<id>                     # live log lines for one task
GET /v1/events?job_id=<id>&task_id=<id>         # both, on one connection
```

Authenticated (bearer token), one held connection per subscription.

**Event types**

| Type | Payload | Delivered to |
|---|---|---|
| `job` | `{"id": "...", "status": "..."}` | subscriptions with no `job_id`, or a matching `job_id` |
| `task` | `{"id": "...", "status": "..."}` | subscriptions with no `job_id`, or a matching `job_id` |
| `worker` | `{"id": "...", "status": "..."}` | subscriptions with no `job_id`, or a matching `job_id` |
| `task_log` | `{"task_id","job_id","seq","stream","content","created_at"}` | **only** subscriptions that named that exact `task_id` |
| `dropped` | `{"reason":"slow_consumer"}` | the one subscription being closed, as its final frame |

Log events are opt-in and per-task: there is no job-wide or cluster-wide log
firehose, so a plain `GET /v1/events` never receives `task_log` frames. A
subscription that supplies only `task_id` receives log frames and no status
events.

`seq`, `stream`, `content` and `created_at` are identical in name and type to
the items returned by `GET /v1/tasks/{id}/logs`, so one client-side type covers
both surfaces. `seq` is a per-task total order.

**Backfilling a task log without a gap or a duplicate - do these in order:**

1. Open the SSE subscription **first** and start buffering `task_log` events.
   The subscription is live by the time the response returns 200.
2. Then page `GET /v1/tasks/{id}/logs?since_seq=0`, repeating with
   `since_seq=<next_seq>` until `next_seq` is `0`. Record the highest `seq` seen
   as `maxSeq`.
3. Render the backfill, then apply the buffered and subsequent events,
   discarding any event whose `seq <= maxSeq`.

Reversing steps 1 and 2 leaves a hole between the last page and the first event.

**`dropped` and reconnection.** If a subscriber stops reading, the server closes
its 64-slot buffer rather than blocking the producer, and writes one final
`event: dropped` frame. The database remains the source of truth, so recover by
re-running step 2 with `since_seq=<last seq seen>`. A `seq` discontinuity is the
same signal. There is no `id:` / `Last-Event-ID` resume.

**Validation.** `?task_id=` returns `400` for a malformed UUID and `404` for an
unknown task. `?job_id=` is not validated - an unknown job yields an open but
permanently empty stream. This asymmetry is deliberate: `?job_id=` is an
existing contract with existing clients, while an unvalidated `?task_id=` would
look identical to "this task produced no output".

**Single-process caveat.** The broker is in-memory, so events are visible only
to clients connected to the `relay-server` process that owns the relevant
agent's gRPC stream. Behind a load balancer with more than one replica, live
delivery degrades to best-effort while the polling endpoints stay correct. This
already applies to status events; a live log view just makes it more visible.
````

- [ ] **Step 2: Verify the documented contract against the code**

No code change expected. Confirm each claim, and fix the README if any disagrees:

- The event type names and payload keys: `internal/events/broker.go` (`TypeTaskLog`), `internal/worker/handler.go` (`taskLogEvent`), `internal/api/events.go` (the `dropped` frame).
- The delivery matrix: `Publish` in `internal/events/broker.go`.
- `seq`/`stream`/`content`/`created_at` parity: `internal/api/tasks.go:56-61`.
- `?since_seq` / `next_seq == 0` drain semantics: `internal/api/tasks.go:91-130`.
- Subscribe-then-flush ordering: `internal/api/events.go` (`Subscribe` precedes `flusher.Flush()`).
- 400/404 behaviour: `internal/api/events.go`.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document ?task_id=, task_log/dropped events, and the backfill contract"
```

---

## Task 9: Whole-plan verification gate

Nothing new is written here. This is the gate the plan must clear before it is handed back.

- [ ] **Step 1: Confirm the generated tree is committed and clean**

`make generate` must have been run in Task 3 and its LF-only churn reverted, or the suite will not compile against the new `AppendTaskLog` signature.

```bash
git status --porcelain
```
Expected: empty. If generated files appear modified, re-apply the Task 3 Step 4 discipline (`git diff --ignore-all-space --stat`, `git checkout -- <file>` for whitespace-only files).

- [ ] **Step 2: Unit suite**

Run: `make test`
Expected: `ok` for every package, no `FAIL`.

- [ ] **Step 3: Vet, both tag sets**

Run: `go vet ./...`
Expected: no output.

Run: `make vet-integration`
Expected: no output.

- [ ] **Step 4: Race detector on the broker**

From Git Bash:

```bash
PATH="/c/msys64/mingw64/bin:$PATH" CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/events/... -count=2 -timeout 180s
```
Expected: `ok relay/internal/events`, no `WARNING: DATA RACE`.

- [ ] **Step 5: Integration suite**

Docker Desktop must be running.

Run: `make test-integration`
Expected: `ok` for every package. `-p 1` is baked into the target; do not run it in parallel.

- [ ] **Step 6: Build**

Run: `make build`
Expected: `bin/relay-server`, `bin/relay-agent`, `bin/relay` produced with no errors.

Note that `make build` depends on `web-build`, which rewrites `web/dist`. `web/dist` is tracked but stale from the scaffold, so a build dirties it. Revert it before assembling any PR:

```bash
git checkout -- web/dist/
git status --porcelain
```
Expected: empty.

- [ ] **Step 7: Scope audit**

```bash
git diff --stat main...HEAD
```
Confirm the changed-file list contains **no** path under `web/` and **no** path under `internal/mcp/`, **no** new file under `internal/store/migrations/`, and no new route resembling `?follow=1`. If any appear, they are out of scope and must come out.

---

## Self-review against the spec

**Spec coverage.** Every numbered spec requirement maps to a task:

| Spec item | Task |
|---|---|
| `Event.TaskID`, `Filter`, `Subscribe(Filter)` | 1 |
| `logSubs` index, `HasLogSubscriber`, `events.TypeTaskLog`, delivery matrix | 2 |
| `AppendTaskLog` -> `:one` CTE, regenerate, no migration | 3 |
| `TestAppendTaskLog_EpochGuarded` strengthened to `pgx.ErrNoRows` | 3 |
| `handleTaskLog` consumes the row, logs real DB errors without content, publishes behind `HasLogSubscriber` | 4 |
| Payload shape `{task_id, job_id, seq, stream, content, created_at}` + `logEntry` parity | 4 (unit) + 7 (against the live polling response) |
| `?task_id=` parse/validate (400/404), `Filter` construction | 5 |
| `dropped` frame on broker close, not on client disconnect | 5 |
| `relayclient` scanner buffer | 6 |
| End-to-end agent -> SSE, status unaffected, gapless backfill | 7 |
| `README.md` events section | 8 |
| Spec test 1-8 (broker unit) | 2 |
| Spec test 9-11, 13 (handleEvents / payload) | 5, 4, 7 |
| Spec test 12 (relayclient 512 KiB) | 6 |
| Spec test 14-17 (integration) | 3, 4, 7 |
| Acceptance criteria 1-14 | 2, 4, 5, 6, 7, 8, 9 |

**Type consistency.** `events.Filter{JobID, TaskID}`, `events.Event{Type, JobID, TaskID, Data}`, `events.TypeTaskLog`, `Broker.HasLogSubscriber(string) bool`, `Broker.removeLocked(chan Event)`, `store.AppendTaskLogParams{TaskID, Stream, Content, AssignmentEpoch}`, `store.AppendTaskLogRow{ID, CreatedAt, JobID}`, `worker.taskLogEvent{TaskID, JobID, Seq, Stream, Content, CreatedAt}`, `worker.Handler.HandleTaskLog`, `worker.TaskLogPublishesForTest`, `api_test.newTestServerWithBroker`, `api_test.gateWriter` - each name is introduced in exactly one task and used with the same spelling everywhere after.

**Underspecified points I resolved (planner calls, flagged for the conductor):**

1. **How to verify "nothing is marshalled when nobody is tailing".** Spec acceptance criterion 8 is not observable from outside `internal/worker`. I added a package-level `atomic.Int64` counter (`taskLogPublishes`) incremented at the marshal, exposed through the already-integration-tagged `export_test.go` as `TaskLogPublishesForTest()`. This follows the project's documented "testability overrides as package vars" pattern. If a reviewer objects to production instrumentation, the fallback is to drop criterion 8's negative assertion to "no event reached any subscriber", which is strictly weaker.
2. **Where `taskLogEvent` lives.** The spec does not say. I put it in `internal/worker/handler.go` (unexported) rather than in `internal/events` or a shared package, because it is a payload the publisher builds, `internal/events` is transport-only (`Data []byte`), and a shared type would couple `worker` to `api`. Parity with `api.logEntry` is enforced by a key-set unit test plus the Task 7 comparison against the live `GET /v1/tasks/{id}/logs` response.
3. **Broker key canonicalisation.** The spec says the filter key is the task UUID but not in what form. An agent could send an uppercase UUID string. Both sides now key on the canonical lowercase-hex `uuidStr(...)` output, which is why `handleEvents` re-renders the parsed UUID rather than using the raw query string.
4. **Only `TypeTaskLog` is added as a constant.** The spec mentions `events.TypeTaskLog`; I did not add `TypeTask`/`TypeJob`/`TypeWorker` constants, because doing so would mean touching all six existing publish sites for no behavioural gain (unrelated refactoring).
5. **Task 1 / Task 2 split.** The spec asks for the mechanical `Subscribe` change in its own commit ahead of the behavioural work; I made that an explicit task boundary rather than a note inside one task.
6. **`newTestServerWithBroker` gains a `*pgxpool.Pool` return in Task 7.** Task 5 introduces it with three returns and Task 7 needs the pool for `worker.NewHandler`. I chose to widen it in Task 7 (with the call-site updates spelled out) rather than pre-emptively return an unused value in Task 5, since Task 5's own tests do not need it. If the engineer prefers, returning the pool from the start in Task 5 is equivalent and saves one edit.
