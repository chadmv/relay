# Reconcile Canonical Task IDs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `reconcileRunningTasks` compare and key on the *canonical re-encoding* of an agent-reported task id instead of the raw wire string, so a non-canonical-but-parseable spelling no longer cancels and requeues a live, correctly-reported task on every reconnect.

**Architecture:** One parse at the top of the report loop. `pgtype.UUID.Scan` the wire string, re-encode with the package's existing `uuidStr`, and use that canonical string for both the `agentSet` key and the `serverSet` lookup. `cancelIDs` continues to echo the agent's own spelling verbatim (decision below), so the change is invisible on the wire. Unparseable ids continue to land in `cancelIDs` verbatim, and log nothing.

**Tech Stack:** Go, `github.com/jackc/pgx/v5` (`pgtype.UUID`), sqlc-generated `internal/store`, testify, testcontainers-go Postgres (`//go:build integration`).

**Closes:** `bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones`

---

## Slice independence declaration

**There is no frontend work in this slice.** Zero files under `web/` change. The whole
diff is `internal/worker/handler.go` (one loop body) plus one new
`//go:build integration` test file under `internal/worker/`.

**One slice, one PR, one session.** There are no independent frontend/backend slices to
parallelise in Phase 3 - dispatch a single `relay-backend-engineer`. There are no stages
to hand to the backlog; this is not a multi-session plan and must NOT be run through
`/backlog phases`.

**No `make generate`.** No `.sql` and no `.proto` file is touched, so the sqlc/protobuf
regeneration step and its CRLF dance do not apply. If a step in this plan appears to
require editing `*.sql.go` or `models.go`, stop - the plan is wrong.

---

## Verification of the backlog item's claims

Every claim below was checked against the tree at HEAD before this plan was written.

**CONFIRMED - the defect is real and is exactly where the item says it is.**
`internal/worker/handler.go:438-451`. `serverSet` is keyed on `uuidStr(t.ID)`;
`agentSet` is keyed on, and `serverSet` is looked up with, `rt.TaskId`.

**CONFIRMED - the library analysis.** Read at
`C:/Users/chadv/go/pkg/mod/github.com/jackc/pgx/v5@v5.9.1/pgtype/uuid.go`, lines 35-53
and 73-90. `parseUUID` switches on `len(src)`: 36 splices
`src[0:8]+src[9:13]+src[14:18]+src[19:23]+src[24:]` **without ever checking that indices
8, 13, 18 and 23 are hyphens**; 32 is accepted as already-stripped; anything else errors.
The splice result goes to `hex.DecodeString`, which accepts `A-F` as well as `a-f`. So
all three spellings the item names decode to the same 16 bytes and equal none of
`uuidStr`'s output. `Scan` on a `string` always sets `Valid: true` on success, so
`uuidStr` can never return `""` on this path.

**CONFIRMED - both consequences fire together.** `!ok` short-circuits the epoch
comparison, so the task is cancelled without its epoch ever being examined; and because
`agentSet` holds the wire spelling, the requeue loop's `agentSet[taskIDStr]` (canonical)
misses and `RequeueTaskByID` runs. `RequeueTaskByID` (`internal/store/query/tasks.sql:337-357`)
sets `status='pending'` and `assignment_epoch = assignment_epoch + 1` inside the same
UPDATE as its `WHERE ... status IN ('dispatched','running')`, so it is a genuine
conditionally-ending epoch bump. **The fix does not change how many tasks flow through it
in the correct-spelling case:** for a canonical report, `uuidStr(tID)` is byte-identical
to `rt.TaskId`, so `agentSet` gets the same key it gets today and the requeue loop takes
the same branch for every row. The pre-existing
`TestRegisterWorker_ReconcilesRunningTasks` is the permanent guard for that and must stay
green with its file untouched (see the gate in Task 2).

---

## Decision 1 - what `cancelIDs` carries: ECHO THE WIRE SPELLING

**Decision: keep echoing `rt.TaskId` verbatim. Do not canonicalize the cancel list.**

**This is NOT a wire-visible behaviour change.** For every input, parseable or not, the
bytes this slice puts into `RegisterResponse.CancelTaskIds` are the same bytes HEAD puts
there. The canonicalization lands on the *comparison*, never on the *echo*.

**Rationale, from what the agent actually does with the field.** `internal/agent/agent.go:246-253`:

```go
for _, tid := range reg.CancelTaskIds {
    a.mu.Lock()
    r, ok := a.runners[tid]
    a.mu.Unlock()
    if ok {
        r.Abandon()
    }
}
```

The agent looks each id up **in its own map**, and `a.runners` is keyed at
`handleDispatch` (line 307) on `task.TaskId` - the id the *coordinator* sent in
`DispatchTask`, which `internal/scheduler/dispatch.go:305` renders with its own
`uuidStr`. `buildRegisterRequest` (line 336) then reports `r.taskID`, i.e. that same map
key. So for the shipped Go agent, wire spelling == its own map key == canonical, and
echo and canonicalization are indistinguishable.

They are **not** indistinguishable for the client this fix exists to serve. A third-party
agent whose UUID library emits uppercase keys its runner map with the uppercase string
and reports the uppercase string. Echo it back and its lookup hits. Canonicalize it and
the agent receives a spelling it has never used, `ok` is false, `Abandon()` never runs,
and a task the coordinator has decided to cancel **keeps running**. That converts
"cancelled spuriously" into "not cancelled at all", which is the strictly worse failure,
on exactly the population this slice is meant to help.

The general rule the item states - "any comparison, map key, set membership or log line
involving a caller-supplied identifier must use the re-encoding" - does not reach an echo
field. `cancelIDs` is neither compared nor keyed nor rendered to a log; it is a
correlation echo of something the caller told us one function call ago. There is no
injection surface (it is a protobuf `repeated string`, not a log line) and no
amplification (one echo per reported entry, back to the sender that supplied it, over a
stream it must read).

**No agent-side test is required.** The item's acceptance criterion is conditional -
"with a test on the agent side **if the spelling changes**" - and it does not change.
`internal/agent/` is untouched by this slice.

**The decision is pinned, not just commented.** Task 1's stale-epoch positive control
asserts the cancelled id comes back in the *agent's* spelling and NOT in the canonical
one, so a future edit that canonicalizes the echo reddens a test.

---

## Decision 2 - unparseable ids: KEEP THEM IN `cancelIDs`, VERBATIM, SILENTLY

**Decision: an unparseable `rt.TaskId` is appended to `cancelIDs` unchanged and the loop
continues. No log line, no counter, no error.**

This preserves HEAD's behaviour exactly (today they fail the `serverSet` lookup and fall
into `cancelIDs`), which is what makes Decision 1's "zero wire-visible change" claim true
for *all* inputs rather than only parseable ones.

**Why keep rather than drop.** An unparseable id can name no assignment of ours, so the
agent is reporting a subprocess the coordinator does not know about. Telling it to stop
is the fail-safe direction. Dropping is the fail-open direction *and* is completely
silent: the budget constraint below forbids a log line, so a dropped entry would leave an
unknown process running with no signal anywhere.

**It cannot affect the requeue loop either way.** An unparseable string is never inserted
into `agentSet` under the fix, and under HEAD it is inserted under a key that can never
equal a canonical `serverSet` key. The requeue loop's behaviour is identical under both
choices, so this decision is confined to `cancelIDs`.

**No log line, and this is a hard constraint.** `reconcileRunningTasks` runs inside
`finishRegister`, at registration - **before** `Connect` allocates the connection's
`ingestLogLimiter` (`internal/worker/handler.go:172`), so it has no budget to spend at
all. (An earlier draft said "the four budgeted ingest sites"; there are **five** -
`ingest_log_limiter.go` defines five kinds and `handler.go` has five `lim.allow(` sites, at
lines 555, 586, 866, 958 and 1163. The count went stale when `kindBadTaskIDLog` was split
out on 2026-08-15. The load-bearing claim is "this site has no budget", not how many sites
do.) A `log.Printf` here would be unbudgeted, caller-driven volume with a
caller-chosen payload, which is the open half of
`bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget`. Do not add
one, and do not "just clip and `%q` it" - the budget, not the escaping, is the missing
control. Task 3 asserts the captured log is *empty* across a whole registration, so any
wording reddens it.

---

## The sweep

Requested by the item: every site in `internal/worker` and `internal/api` that keys,
compares, or renders a caller-supplied UUID string rather than its re-encoding.
`internal/scheduler` was checked too, since it is the third writer of these ids.
**Result: exactly one defect (the one this slice fixes) and one deliberate, documented
near-miss.** This section is the sweep note; Task 2's commit body reproduces its
conclusion.

### `internal/worker` (non-test files: `handler.go`, `registry.go`, `sender.go`, `grace.go`, `ingest_log_limiter.go`)

| Site | Verdict |
| --- | --- |
| `handler.go:446-449` `reconcileRunningTasks` | **DEFECTIVE. Fixed by this slice.** |
| `handler.go:487-514` `handleTaskStatus` id guard | Clean. The wire string reaches a log only in the parse-**failed** branch, under `lim.allow` and wrapped in `clipID(...)` + `%q`. Every subsequent line uses `uuidStr(taskID)`. |
| `handler.go:803-825, 900-916` `handleTaskLog` | Clean. `canonicalID := uuidStr(taskID)` is used for the dedupe key *and* the log line - this is the sibling site fixed on 2026-08-15. |
| `ingest_log_limiter.go:67-85` `logKey.id` | Clean. Doc comment states the invariant that only a canonical re-encoding may enter, and both writers honour it. |
| `handler.go:992-1033` `markWorkerOffline`, `requeueWorkerTasks` | Clean. `workerID` originates from `uuidStr(updated.ID)` in `finishRegister:377` and is never taken from the wire. |
| `handler.go:361` auto-enroll log line | Clean for this shape: the id is `uuidStr(workerID)`. `reg.Hostname` is caller data but is not an identifier, and is `clipID` + `%q`. |
| `RegisterRequest.WorkerId` | Clean, and worth recording: **it is never read.** `rg -n '\.WorkerId'` over non-test files in the package returns zero hits. Identity comes from the token lookup, never the wire. |
| `registry.go` / `grace.go` map keys | Clean. Every `Register`/`Send`/`Start`/`Cancel` call site in the tree passes `uuidStr(...)`: `handler.go:381,412`, `scheduler/dispatch.go:326`, `api/jobs.go:820`, `api/workers.go:502`, `api/workspaces.go:79`. |

### `internal/api`

| Site | Verdict |
| --- | --- |
| `server.go:235-241` `parseUUID` | Clean in itself - it returns only the parsed value. **CORRECTION (Phase 4): the original claim here, that "no caller renders it", was false.** Of the 28 non-test call sites, 26 write a fixed client message, but **two reflect the raw path segment**: `workspaces.go:19-22` (`handleListWorkerWorkspaces`) and `workspaces.go:43-46` (`handleEvictWorkerWorkspace`) both do `writeError(w, http.StatusBadRequest, err.Error())` within three lines of the call, and `parseUUID`'s error wraps pgtype's `fmt.Errorf("cannot parse UUID %v", src)`. **Same class as `reservations.go:270` below, and dismissed the same way: reflected-but-escaped, not filed.** Nothing is keyed or compared on the raw string; `writeError`/`writeJSON` encode via `json.NewEncoder`, so it is JSON-escaped on the way out; and the path length is bounded by `MaxHeaderBytes`. Whether the two should match the other 26 for consistency is a separate question, filed separately - **not changed by this slice.** |
| 22 x `parseUUID(r.PathValue("id"))` | Clean. Every one is the inline form, so no local variable retains the raw path segment; where a string id is needed downstream the code re-renders from the parsed value - `worker_metrics.go:73`, `workspaces.go:79`, `jobs.go:820-821`, `workers.go:502-503`. |
| `events.go:31-47` `?task_id=` | Clean **and already commented**: `logTaskID = uuidStr(taskID)` "so the broker key matches the one `handleTaskLog` derives from the chunk's task id". This is the exact fix pattern, already applied. |
| `events.go:53` `?job_id=` | **Same shape, deliberately accepted, documented in place.** Not parsed and not canonicalized; `broker.go:157` compares `f.JobID == e.JobID` while every publisher renders `uuidStr(...)`, so an uppercase `?job_id=` yields an open, permanently empty stream. The existing comment states the asymmetry is intentional and that "an unknown job has always yielded an open, permanently empty stream ... an existing contract with existing clients". **Not in this slice.** There is no live exposure (the SPA only ever passes ids it received from the API), and changing it is a client-contract change, not a bug fix. Recorded here so the next reader does not have to rediscover it; if it is wanted, it is a separate low-priority item, not scope creep on this one. |
| `pagination.go:125` `decodeCursor` | Clean. `cursor.ID` is a `pgtype.UUID` bound as a query parameter; the raw `w.I` is discarded. |
| `scheduled_jobs.go:206-241` `fillOwnerEmails` | Clean. Both sides of the `seen`/`emailByID` maps are server-rendered: `it.OwnerID` is set from `uuidStr(sj.OwnerID)` at line 40, and `emailByID` is keyed on `uuidStr(row.ID)`. No caller string participates. |
| `cancel_signals.go` | Clean. Both `workerID` and `taskID` come from `uuidStr` of DB values (`jobs.go:820-821`, `workers.go:502-503`). |
| `reservations.go:270` | Checked; **different class, not a finding.** It reflects the raw `wid` into a 400 body, but only after `parseUUID` has *failed*, so nothing is keyed or compared on it; the response is JSON-escaped by `writeJSON` and the input is bounded by `readJSON`'s request-size limit. Not filed. |

### `internal/scheduler` (checked, not requested)

Clean throughout. `dispatch.go` renders every id with its own `uuidStr` (lines 189, 221,
288, 305-306, 326, 336-337, 354-383) and the package holds no wire-supplied string at
all.

### Structural check

`rg -n 'uuidStr\([^)]*\) [!=]= |[!=]= uuidStr\('` over all non-test files returns **zero
matches**: nowhere in the tree is an identity or authorization decision made by comparing
UUID *strings*. Every such decision is made on `pgtype.UUID` values or in SQL.

**No second defect found. Scope is not widened.**

---

## Test seam

**Seam: `h.Connect(&fakeStream{...})` with a `RegisterRequest` carrying `RunningTasks`.**
It already exists at HEAD, in `internal/worker/handler_test.go` at
`TestRegisterWorker_ReconcilesRunningTasks` (lines 330-448), and it exercises the real
production path end to end: `Connect` -> `reconnectAndRegister` -> `finishRegister` ->
`reconcileRunningTasks` -> the `RegisterResponse` the agent actually receives.

**No new exported symbol is introduced, so the seam cannot destroy the RED.** Every
helper the new tests use exists at HEAD in `package worker_test` with `//go:build integration`:
`fakeStream` (`handler_test.go:46-83`), `newTestStore` (`handler_test.go:85`), `captureLog`
(`handler_tasklog_integration_test.go:247`), and `(*Handler).UUIDStringForTest`
(`export_test.go:173`). The tests written below compile and run against unmodified
`internal/worker/handler.go` and fail on behaviour, not on a missing identifier.

**Two seam details that will silently ruin the test if missed:**

1. **Use `NewHandlerWithGrace`, not `NewHandler`.** When the fake stream reaches EOF,
   `teardownConnection` runs. With no `GraceRegistry` it calls `requeueWorkerTasks`,
   which requeues *every* dispatched task for the worker and destroys every
   "still dispatched" assertion. A `GraceRegistry` with a 1-minute window arms a timer
   instead. This is why the pre-existing test at line 336 does the same thing.
2. **Drive the stream to completion before asserting.** Wait on `stream.sentCh`, then
   `close(stream.hold)`, then `<-done`. Reading `stream.sent` before the goroutine
   finishes is a data race.

**Concrete RED-proving inputs.** For a canonical id `c` (36 bytes, lowercase, hyphenated):

- `strings.ToUpper(c)` - uppercase hex.
- `strings.ReplaceAll(c, "-", "")` - 32-byte undashed.
- `c[0:8]+"_"+c[9:13]+"_"+c[14:18]+"_"+c[19:23]+"_"+c[24:]` - 36 bytes with non-hyphen
  separator bytes at indices 8, 13, 18 and 23.

Each is reported with the task's **correct** epoch. At HEAD each one lands in
`CancelTaskIds` and flips the task to `pending` with a bumped epoch. That is the RED.

**Positive controls, in the same `Connect` call so they share the fix's code path:**

- a task the agent does not report at all must still become `pending` (requeue path
  unbroken);
- a task reported at epoch 999 must still be cancelled, must come back in the agent's own
  spelling, and must stay `dispatched` (cancel-not-requeue behaviour unbroken).

**One vacuity guard.** `strings.ToUpper(c)` is a no-op if `c` happens to contain no `a-f`
digit (probability about 1.2e-7 for a random v4 id). The test asserts
`require.NotEqual(canonical, wire)` before using it, so a freak id fails loudly instead of
passing vacuously.

---

## File structure

- **Modify:** `internal/worker/handler.go:443-451` - the report loop inside
  `reconcileRunningTasks`. Nothing else in the function changes; the requeue loop
  (453-465) and `GetActiveTasksForWorker` (433-441) are untouched.
- **Create:** `internal/worker/handler_reconcile_canonical_test.go` - `//go:build integration`,
  `package worker_test`. Holds the fixture helper, the register driver, the headline
  spelling test (Task 1) and the unparseable-id lock-in test (Task 3).
- **Must NOT change:** `internal/worker/handler_test.go`. `TestRegisterWorker_ReconcilesRunningTasks`
  is the guard that the canonical case is unaffected; editing it to accommodate the fix
  would destroy that guarantee.
- **Must NOT change:** anything under `internal/agent/`, `internal/api/`,
  `internal/store/`, `internal/proto/`, or `web/`.

---

### Task 1: RED test - a non-canonical spelling must not cancel or requeue a live task

**Files:**
- Create: `internal/worker/handler_reconcile_canonical_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/worker/handler_reconcile_canonical_test.go` with exactly this content:

```go
//go:build integration

package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reconcileFixture is one worker (with an agent token) plus three tasks claimed
// by it: "match" (the agent will report it at the correct epoch), "stale" (the
// agent will report it at a wrong epoch) and "serverOnly" (the agent will not
// report it at all). The three cover the defect and both positive controls in a
// single reconcile call.
type reconcileFixture struct {
	rawToken     string
	hostname     string
	matchID      pgtype.UUID
	matchEpoch   int32
	staleID      pgtype.UUID
	serverOnlyID pgtype.UUID
}

func seedReconcileFixture(t *testing.T, ctx context.Context, q *store.Queries, tag string) reconcileFixture {
	t.Helper()

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "recon-" + tag + "@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	hostname := "recon-" + tag + "-host"
	workerRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: hostname, Hostname: hostname, CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	raw := "test-token-recon-" + tag
	hash := tokenhash.Hash(raw)
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: workerRow.ID, AgentTokenHash: &hash,
	}))

	claim := func(name string) (pgtype.UUID, int32) {
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: job.ID, Name: name, Commands: []byte(`[["true"]]`),
			Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
			ID: task.ID, WorkerID: pgtype.UUID{Bytes: workerRow.ID.Bytes, Valid: true},
		})
		require.NoError(t, err)
		return claimed.ID, claimed.AssignmentEpoch
	}

	f := reconcileFixture{rawToken: raw, hostname: hostname}
	f.matchID, f.matchEpoch = claim("match")
	f.staleID, _ = claim("stale")
	f.serverOnlyID, _ = claim("server-only")
	return f
}

// newReconcileHandler builds a Handler WITH a grace registry. The grace window
// matters: on stream EOF teardownConnection runs, and with no GraceRegistry it
// calls requeueWorkerTasks, which would requeue every dispatched task for this
// worker and make every "still dispatched" assertion below meaningless. A
// 1-minute grace arms a timer instead. Same reason handler_test.go does it.
func newReconcileHandler(t *testing.T, q *store.Queries, pool interface{ Close() }) *worker.Handler {
	t.Helper()
	grace := worker.NewGraceRegistry(1*time.Minute, func(string, int32) {})
	t.Cleanup(grace.Stop)
	return nil // replaced below; see newReconcileHandlerReal
}

// runRegister drives one complete Connect with the given running-task report and
// returns the RegisterResponse the coordinator sent to the agent.
func runRegister(t *testing.T, ctx context.Context, h *worker.Handler, f reconcileFixture, running []*relayv1.RunningTask) *relayv1.RegisterResponse {
	t.Helper()
	stream := &fakeStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{{
			Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: f.hostname,
					CpuCores: 1, RamGb: 1, Os: "linux",
					RunningTasks: running,
					Credential:   &relayv1.RegisterRequest_AgentToken{AgentToken: f.rawToken},
				},
			},
		}},
		sentCh: make(chan struct{}, 1),
		hold:   make(chan struct{}),
	}

	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()

	select {
	case <-stream.sentCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RegisterResponse never sent")
	}
	close(stream.hold)
	<-done

	require.Len(t, stream.sent, 1)
	resp := stream.sent[0].GetRegisterResponse()
	require.NotNil(t, resp)
	return resp
}

// TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings is the headline
// guard for bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones.
//
// pgtype.UUID.Scan accepts three spellings that decode to the same 16 bytes and
// equal none of uuidStr's output. Before the fix, reconcileRunningTasks keyed
// agentSet on, and looked serverSet up with, the raw wire string, so each of
// these missed the map: the `!ok` short-circuit skipped the epoch comparison and
// cancelled a live task, and the requeue loop then requeued it as "not reported".
func TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)

	cases := []struct {
		name  string
		spell func(canonical string) string
	}{
		{"uppercase", strings.ToUpper},
		{"undashed", func(c string) string { return strings.ReplaceAll(c, "-", "") }},
		{
			// parseUUID splices out indices 8, 13, 18 and 23 of a 36-byte input
			// without ever checking that they are hyphens, so these four bytes are
			// entirely caller-chosen.
			"non_hyphen_separators",
			func(c string) string {
				return c[0:8] + "_" + c[9:13] + "_" + c[14:18] + "_" + c[19:23] + "_" + c[24:]
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			grace := worker.NewGraceRegistry(1*time.Minute, func(string, int32) {})
			t.Cleanup(grace.Stop)
			h := worker.NewHandlerWithGrace(q, pool, worker.NewRegistry(), events.NewBroker(), func() {}, grace)

			f := seedReconcileFixture(t, ctx, q, tc.name)

			matchCanonical := h.UUIDStringForTest(f.matchID)
			staleCanonical := h.UUIDStringForTest(f.staleID)
			matchWire := tc.spell(matchCanonical)
			staleWire := tc.spell(staleCanonical)

			// Vacuity guard: ToUpper is a no-op on an id with no a-f digit
			// (p ~ 1.2e-7). Fail loudly rather than pass for the wrong reason.
			require.NotEqual(t, matchCanonical, matchWire, "spelling must differ from canonical or this case proves nothing")
			require.NotEqual(t, staleCanonical, staleWire, "spelling must differ from canonical or this case proves nothing")

			resp := runRegister(t, ctx, h, f, []*relayv1.RunningTask{
				{TaskId: matchWire, Epoch: int64(f.matchEpoch)},
				{TaskId: staleWire, Epoch: 999},
			})

			// THE BUG. A genuinely-assigned task, reported at the correct epoch in
			// a non-canonical spelling, must not be cancelled...
			assert.NotContains(t, resp.CancelTaskIds, matchWire, "correctly-reported task must not be cancelled")
			assert.NotContains(t, resp.CancelTaskIds, matchCanonical, "correctly-reported task must not be cancelled")

			// ...and must not be requeued, and its generation must not be ended.
			match, err := q.GetTask(ctx, f.matchID)
			require.NoError(t, err)
			assert.Equal(t, "dispatched", match.Status, "correctly-reported task must not be requeued")
			assert.Equal(t, f.matchEpoch, match.AssignmentEpoch, "correctly-reported task must keep its assignment epoch")

			// POSITIVE CONTROL 1: a stale epoch still cancels, the cancel is NOT
			// requeued, and the id comes back in the AGENT'S OWN SPELLING.
			// cancelIDs is an echo, not a comparison: the agent looks it up in its
			// own runner map, which it keyed with the string it just reported.
			// Canonicalizing here would break cancellation for exactly the
			// non-canonical clients this fix serves. See reconcileRunningTasks.
			assert.Contains(t, resp.CancelTaskIds, staleWire, "a stale-epoch task must still be cancelled")
			assert.NotContains(t, resp.CancelTaskIds, staleCanonical, "cancelIDs must echo the agent's spelling, never canonicalize it")
			stale, err := q.GetTask(ctx, f.staleID)
			require.NoError(t, err)
			assert.Equal(t, "dispatched", stale.Status, "a reported-but-stale task is cancelled, not requeued")

			// POSITIVE CONTROL 2: a task the agent never reported still requeues.
			serverOnly, err := q.GetTask(ctx, f.serverOnlyID)
			require.NoError(t, err)
			assert.Equal(t, "pending", serverOnly.Status, "an unreported task must still be requeued")

			assert.Len(t, resp.CancelTaskIds, 1, "exactly one cancel: the stale-epoch task")
		})
	}
}
```

Then **delete the `newReconcileHandler` stub above** - it is a leftover placeholder and
must not survive into the committed file. The handler is constructed inline inside each
subtest. Verify the file contains no `newReconcileHandler` before running anything:

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
grep -n "newReconcileHandler" internal/worker/handler_reconcile_canonical_test.go
```

Expected: no output.

- [ ] **Step 2: Run the test to verify it fails**

Docker Desktop must be running.

Run:
```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings -v -timeout 600s
```

Expected: **FAIL**, all three subtests, with (per subtest) at minimum:
- `Error: "[<matchWire>]" should not contain "<matchWire>"` from the first `NotContains`
- `Error: Not equal: expected: "dispatched" actual: "pending"`
- `Error: Not equal: expected: 1 actual: 2` (the bumped `assignment_epoch`)
- `Error: "[<matchWire> <staleWire>]" should have 1 item(s), but has 2`

If any subtest **passes** at this step, stop and diagnose: the fix is not in place, so a
green here means the test is not exercising `reconcileRunningTasks`. The most likely cause
is that the fixture's tasks were never claimed (check `ClaimTaskForWorker` returned
`AssignmentEpoch == 1`).

- [ ] **Step 3: Commit the RED test**

Do not commit a red test to a shared branch. Skip this step - Task 2 commits the test and
the fix together. Move on to Task 2.

---

### Task 2: Canonicalize the comparison in `reconcileRunningTasks`

**Files:**
- Modify: `internal/worker/handler.go:443-451`
- Test: `internal/worker/handler_reconcile_canonical_test.go` (from Task 1)
- Must not change: `internal/worker/handler_test.go`

- [ ] **Step 1: Write the minimal implementation**

In `internal/worker/handler.go`, replace lines 443-451 - i.e. exactly this block:

```go
	var cancelIDs []string
	agentSet := make(map[string]bool, len(reported))
	for _, rt := range reported {
		agentSet[rt.TaskId] = true
		srvEpoch, ok := serverSet[rt.TaskId]
		if !ok || srvEpoch != rt.Epoch {
			cancelIDs = append(cancelIDs, rt.TaskId)
		}
	}
```

with:

```go
	var cancelIDs []string
	agentSet := make(map[string]bool, len(reported))
	for _, rt := range reported {
		// A STRING THAT PARSED IS NOT A STRING THAT IS CANONICAL. serverSet above
		// is keyed on uuidStr - lowercase, hyphenated, 36 bytes - and rt.TaskId is
		// whatever the agent chose to send. pgtype.UUID.Scan accepts three
		// spellings that decode to the same 16 bytes and equal none of them:
		// uppercase hex (hex.DecodeString takes A-F), the 32-byte undashed form,
		// and the 36-byte form with ANY four bytes at indices 8, 13, 18 and 23,
		// which parseUUID splices out without ever checking they are hyphens.
		//
		// Keying and looking up on the wire string therefore missed the map, and
		// the `!ok` short-circuit below skipped the epoch comparison ENTIRELY - so
		// a live, correctly-reported task was cancelled here AND requeued by the
		// loop that follows (its canonical key looked "not reported"), silently,
		// on every reconnect. Compare on the RE-ENCODING, never on the input. Same
		// rule as handleTaskLog's canonicalID block and logKey's doc comment.
		var tID pgtype.UUID
		if err := tID.Scan(rt.TaskId); err != nil {
			// UNPARSEABLE. It can name no assignment of ours, so tell the agent to
			// stop it: that is HEAD's behaviour, preserved DELIBERATELY, not an
			// oversight. Dropping it instead would be fail-open and completely
			// silent, leaving a subprocess the coordinator knows nothing about
			// running with no signal anywhere.
			//
			// IT IS NOT LOGGED, AND MUST NOT BE. This runs inside finishRegister,
			// at registration - BEFORE Connect allocates this connection's
			// ingestLogLimiter, so this site has no budget to spend at all. A line
			// here would be unbudgeted, caller-driven volume with a caller-chosen
			// payload; clip + %q is not a substitute for the missing budget. See
			// bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget.
			// TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing
			// asserts the whole captured log is empty, so any wording reddens it.
			cancelIDs = append(cancelIDs, rt.TaskId)
			continue
		}
		canonical := uuidStr(tID)
		agentSet[canonical] = true
		srvEpoch, ok := serverSet[canonical]
		if !ok || srvEpoch != rt.Epoch {
			// cancelIDs ECHOES THE AGENT'S OWN SPELLING, and that is a wire
			// contract, not laziness. The agent looks each id up in its own runner
			// map (internal/agent/agent.go: `a.runners[tid]`), keyed with the same
			// string it just reported to us. Canonicalizing here would hand a
			// non-canonical agent - the exact client this canonicalization exists
			// to serve - a spelling it has never used; its lookup would miss,
			// Abandon() would never run, and a task the coordinator has decided to
			// cancel would keep running. "Not cancelled at all" is strictly worse
			// than "cancelled spuriously". The canonical form belongs on the
			// COMPARISON, never on the echo. Pinned by the stale-epoch control in
			// TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings.
			cancelIDs = append(cancelIDs, rt.TaskId)
		}
	}
```

`pgtype` is already imported in this file (line 22). Do not add an import.

- [ ] **Step 2: Run the headline test to verify it passes**

Run:
```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings -v -timeout 600s
```

Expected: **PASS**, all three subtests.

- [ ] **Step 3: Run the pre-existing reconcile test, with its file untouched**

This is the gate on "the canonical case is unchanged" and on the requeue count through
`RequeueTaskByID`.

Run:
```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git diff --stat internal/worker/handler_test.go
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ReconcilesRunningTasks -v -timeout 600s
```

Expected: `git diff --stat` prints **nothing** (the file is unmodified), and the test
**PASSES**. If it fails, the fix has changed behaviour in the canonical case - do not
edit the test to accommodate it; the implementation is wrong.

- [ ] **Step 4: Run the full unit suite and the whole worker integration package**

Run:
```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
make test
go test -tags integration -p 1 ./internal/worker/... -timeout 900s
```

Expected: both green. `make test` needs no Docker; the second command does.

- [ ] **Step 5: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git add internal/worker/handler.go internal/worker/handler_reconcile_canonical_test.go
git commit -F- <<'EOF'
fix(worker): reconcile on the canonical task id, not the wire spelling

reconcileRunningTasks keyed agentSet on, and looked serverSet up with, the raw
rt.TaskId while serverSet was keyed on uuidStr. pgtype.UUID.Scan accepts three
spellings that decode to the same 16 bytes and equal none of uuidStr's output:
uppercase hex, the 32-byte undashed form, and the 36-byte form with any four
bytes at indices 8, 13, 18 and 23 (parseUUID splices those out without ever
checking they are hyphens). Each missed the map, the !ok short-circuit then
skipped the epoch comparison entirely, and the requeue loop saw the canonical key
as unreported - so a live, correctly-reported task was cancelled AND requeued,
silently, on every reconnect.

Parse once, compare and key on uuidStr(tID).

Two decisions, both commented at the site and pinned by tests:

- cancelIDs still ECHOES the agent's own spelling. The agent looks each id up in
  its own runner map, keyed with the string it just reported (agent.go:246).
  Canonicalizing the echo would break cancellation for exactly the non-canonical
  clients this fix serves. Zero wire-visible change for every input.
- An unparseable id still lands in cancelIDs verbatim (fail-safe: stop work the
  coordinator does not know about) and logs NOTHING - this runs at registration,
  outside the per-connection ingest budget.

Sweep for the same shape (a caller-supplied UUID string keyed, compared or
rendered instead of its re-encoding), recorded in full in
docs/superpowers/plans/2026-08-20-reconcile-canonical-task-ids.md:

- internal/worker: this was the only defect. handleTaskStatus, handleTaskLog,
  logKey, markWorkerOffline, requeueWorkerTasks and every registry/grace key are
  canonical already; RegisterRequest.WorkerId is never read at all.
- internal/api: parseUUID is clean and no caller renders its raw-string error;
  all 23 parseUUID(r.PathValue("id")) sites are inline, so no raw path segment is
  retained; events.go's ?task_id= already canonicalizes for the broker key.
  events.go's ?job_id= is the same shape but is deliberate, documented in place,
  and a client-contract question rather than a bug - not changed here.
- internal/scheduler: no wire-supplied id string exists in the package.

Closes bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones.
EOF
```

---

### Task 3: Lock in the unparseable-id behaviour and the no-log constraint

This test is **GREEN at HEAD by design** - it pins a decision, not a fix, because
Decision 2 deliberately preserves HEAD's behaviour. Do not describe it as a RED test.
Its discriminating power is proved by mutation in Step 3 instead, and the mutation leaves
the test behind.

**Files:**
- Modify: `internal/worker/handler_reconcile_canonical_test.go` (append)

- [ ] **Step 1: Write the test**

Append to `internal/worker/handler_reconcile_canonical_test.go`:

```go
// TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing pins
// the two deliberate decisions around an id that pgtype.UUID.Scan rejects
// outright. It is green against the pre-canonicalization handler too, because
// that behaviour was deliberately preserved - its value is that it reddens if
// anyone later changes either decision. Proven discriminating by mutation: swap
// the `cancelIDs = append(...)` in the parse-failure branch for a bare `continue`
// and the first assertion fails; add any log.Printf to that branch and the last
// one fails.
func TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)

	grace := worker.NewGraceRegistry(1*time.Minute, func(string, int32) {})
	t.Cleanup(grace.Stop)
	h := worker.NewHandlerWithGrace(q, pool, worker.NewRegistry(), events.NewBroker(), func() {}, grace)

	f := seedReconcileFixture(t, ctx, q, "unparseable")
	matchCanonical := h.UUIDStringForTest(f.matchID)

	// Neither 32 nor 36 bytes, so parseUUID rejects it on length alone. It also
	// carries a NUL and a newline: if this ever DID reach a log line, the line
	// would be unescaped and split in two, which is precisely why the branch may
	// not log at all outside the connection budget.
	const garbage = "not-a-uuid-at-all-\x00-with-a-NUL-and-a-\n-newline"

	logged := captureLog(t)

	resp := runRegister(t, ctx, h, f, []*relayv1.RunningTask{
		{TaskId: matchCanonical, Epoch: int64(f.matchEpoch)},
		{TaskId: garbage, Epoch: 1},
	})

	// DECIDED BEHAVIOUR: an unparseable id names no assignment of ours, so the
	// agent is told to stop it - echoed byte for byte, exactly as before the
	// canonicalization fix. Dropping it would be fail-open and silent.
	assert.Equal(t, []string{garbage}, resp.CancelTaskIds,
		"an unparseable id is echoed into the cancel list verbatim, and nothing else is cancelled")

	// An unparseable sibling must not contaminate a correctly-reported task.
	match, err := q.GetTask(ctx, f.matchID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", match.Status, "a correctly-reported task survives an unparseable sibling")
	assert.Equal(t, f.matchEpoch, match.AssignmentEpoch, "a correctly-reported task keeps its assignment epoch")

	// NO UNBUDGETED LOG LINE. reconcileRunningTasks runs inside finishRegister, at
	// registration, before Connect allocates this connection's ingestLogLimiter,
	// so it has no budget to spend and may not log a caller-supplied id at all.
	// Asserting the WHOLE captured log is empty (rather than NotContains) means
	// any wording reddens this, exactly like
	// TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll.
	assert.Empty(t, logged(), "registration-time reconcile must log nothing; it is outside the connection log budget")
}
```

- [ ] **Step 2: Run the test to verify it passes**

Run:
```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing -v -timeout 600s
```

Expected: **PASS**.

If the final `assert.Empty` fails on an unrelated line, read the line before weakening
the assertion. The only log sites reachable on this path are
`finishRegister`'s inventory-replace failure (`handler.go:392`) and the auto-enroll line
(`handler.go:361`, unreachable here - this test registers with an agent token). Either one
appearing is a real finding, not test noise.

- [ ] **Step 3: Prove the test discriminates, by mutation**

Mutation A - drop instead of echo. In `internal/worker/handler.go`, temporarily replace
the parse-failure branch's two lines

```go
			cancelIDs = append(cancelIDs, rt.TaskId)
			continue
```

with

```go
			continue
```

Run:
```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing -v -timeout 600s
```

Expected: **FAIL** with `expected: []string{"not-a-uuid-at-all-..."} actual: []string(nil)`.

Mutation B - add the log line the budget forbids. Revert Mutation A, then temporarily
insert above the `cancelIDs = append(...)` in that same branch:

```go
			log.Printf("worker: reconcile bad task id %q", clipID(rt.TaskId))
```

Run the same command. Expected: **FAIL** on
`registration-time reconcile must log nothing`.

Now revert Mutation B and confirm the file is back to the Task 2 state:

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git diff internal/worker/handler.go
```

Expected: **no output**. Both mutations are proved and both tests survive them; nothing
about the mutations is committed.

- [ ] **Step 4: Run the whole worker integration package plus the unit suite**

Run:
```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
make test
go test -tags integration -p 1 ./internal/worker/... -timeout 900s
git status --short
```

Expected: both green, and `git status --short` shows only
`?? internal/worker/handler_reconcile_canonical_test.go` as modified-since-last-commit
content (the file was committed in Task 2, so expect `M` on it and nothing else). In
particular there must be no change to `web/dist/`, `internal/store/*.sql.go`, or
`internal/proto/`.

- [ ] **Step 5: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git add internal/worker/handler_reconcile_canonical_test.go
git commit -F- <<'EOF'
test(worker): pin the unparseable-task-id decision in reconcile

An id that pgtype.UUID.Scan rejects outright is echoed into cancelIDs verbatim
(fail-safe: it names no assignment of ours, so the agent is told to stop it) and
logs nothing (reconcile runs at registration, outside the per-connection ingest
budget). Both were HEAD's behaviour and both are now deliberate, so this test is
green against the pre-fix handler by design.

Its discriminating power was proved by mutation rather than by a RED: replacing
the echo with a bare `continue` fails the first assertion, and adding any
log.Printf to that branch fails the empty-log assertion. Neither mutation is
committed; the test that catches them is.
EOF
```

---

## Self-review

**Spec coverage** (against the item's Acceptance / Done When):

| Criterion | Task |
| --- | --- |
| Uppercase spelling is not cancelled and not requeued, RED at HEAD | Task 1, subtest `uppercase` |
| Same for 32-char undashed | Task 1, subtest `undashed` |
| Same for 36-char with non-hyphen separators | Task 1, subtest `non_hyphen_separators` |
| Positive control: unreported task still requeues | Task 1, `serverOnly` assertion in every subtest |
| Positive control: stale-epoch task still cancels | Task 1, `staleWire` assertions in every subtest |
| Unparseable case has a stated, tested behaviour and no unbudgeted log line | Decision 2 + Task 3 |
| `cancelIDs` checked against `internal/agent/`; agent-side test if the spelling changes | Decision 1. Spelling does not change, so no agent-side test is required and `internal/agent/` is untouched. |
| Sweep note in the commit or PR | The Sweep section above; its conclusion is reproduced in Task 2's commit body. |

**Placeholder scan:** none. Every code step carries complete, compilable code; every run
step carries an exact command and its expected output. The one stub in Task 1 Step 1
(`newReconcileHandler`) is deliberately called out for deletion with a `grep` check,
because leaving a dangling helper would break `go vet`.

**Type consistency:** `reconcileFixture` fields (`rawToken`, `hostname`, `matchID`,
`matchEpoch`, `staleID`, `serverOnlyID`) are spelled identically in
`seedReconcileFixture`, `runRegister` and both tests. `h.UUIDStringForTest` matches
`export_test.go:173`. `worker.NewHandlerWithGrace` matches `handler.go:123`.
`worker.NewGraceRegistry(d, func(string, int32) {})` matches the call at
`handler_test.go:335`. `fakeStream`'s fields (`ctx`, `msgs`, `sentCh`, `hold`, `sent`)
match `handler_test.go:46-53`. `captureLog(t) func() string` matches
`handler_tasklog_integration_test.go:247`.

## Phase 4 notes for the verification lanes

- **Invariants lens:** the load-bearing question is whether the fix changes how many rows
  reach `RequeueTaskByID` in the canonical case. It must not; `TestRegisterWorker_ReconcilesRunningTasks`
  with a byte-identical file is the evidence. Also check that no new log site appeared at
  registration.
- **Correctness lens:** confirm the three spellings really are non-canonical for the ids
  the test generated (the `require.NotEqual` guards), and that the RED at HEAD was
  observed rather than assumed.
- **Security lens:** the echoed unparseable id is caller bytes going back to the caller
  over a protobuf field. Confirm it reaches no log, no map key and no SQL, and that the
  amplification is 1:1 to the sender.
- **Integration lane:** run the whole `./internal/worker/...` integration package, not
  just the two new tests. `-p 1` is required.
