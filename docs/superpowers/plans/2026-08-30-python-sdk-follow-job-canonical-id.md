# A non-canonical job id must not buy a permanently empty stream - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /v1/events?job_id=<any spelling the server accepts>` must deliver that job's frames, instead of subscribing to a broker filter nothing can ever match - fixed once in `internal/api/handleEvents`, so the Go CLI, the Python SDK, the SPA and every future client get it for free.

**Architecture:** One new unexported four-line function in `internal/api/events.go`, `canonicalJobIDFilter`, called on the one line that today reads `?job_id=` raw. It runs the server's own parser (`parseUUID`, i.e. `pgtype.UUID.Scan`) and the server's own renderer (`uuidStr`), and returns the caller's string **unchanged** when the parse fails. Nothing is validated, nothing is rejected, no status code changes. The publish side already emits exactly one spelling, so canonicalising the subscribe side onto that spelling closes the whole gap. Everything else in this plan is tests and the prose the change falsifies.

**Tech Stack:** Go 1.26, `github.com/jackc/pgx/v5` v5.9.1 (`pgtype.UUID.Scan`), testify v1.11.1, testcontainers-go (integration lane), Postgres 16, Python 3.9-3.13 + httpx + pytest (docs and one unit test only). **No `make generate` step anywhere in this plan** - nothing under `internal/store/query/` or `proto/` is touched. No migration. No `web/` change.

**Spec:** `docs/superpowers/specs/2026-08-30-python-sdk-follow-job-canonical-id.md`
**Backlog item this closes:** `docs/backlog/bug-2026-08-27-python-sdk-follow-job-hangs-on-noncanonical-job-id.md` (in full; the conductor closes it with `/backlog close python-sdk-follow-job-hangs`, which `git mv`s the file into `docs/backlog/closed/`)
**Predecessor slice whose fixture literals and shape this reuses:** `docs/superpowers/plans/2026-08-26-relay-logs-envelope-drift.md`

---

## Slice independence declaration

**This is a single-slice, single-PR, single-session plan. It has no stages and must NOT be handed to `/backlog phases`.** Every task below is a few minutes of work and the whole thing is one PR against one backlog item.

**Frontend/backend: backend, plus docs, plus one Python unit test. There is no frontend slice at all.** Checked rather than assumed:

- `web/src/` subscribes to `/v1/events` with **`?task_id=` only**. `web/src/jobs/api.ts` carries the comment "Only ?task_id= is sent. Adding ?job_id= would put status frames on the same [connection]". A grep for `job_id=` across `web/src` returns only `scheduled_job_id` on a REST list route.
- Nothing under `web/` renders, asserts, or documents `?job_id=` spelling behaviour, so nothing there becomes false.
- `web/dist` must not be rebuilt or committed. If it dirties for any reason, `git checkout -- web/dist/`.

**Phase 3 must therefore be a single lane, owned by `relay-backend-engineer`.** Task 6 is Python SDK prose plus one pytest file - that is the backend engineer's lane in this repo, not the frontend engineer's (`relay-frontend-engineer` owns `web/`). Do not dispatch two agents in parallel: Tasks 1-4 are one RED/GREEN chain on two files, Task 5 corrects prose in files Tasks 1-4 just edited, and Task 6's docstring asserts the behaviour Task 2 establishes.

**The tasks are strictly sequential and each leaves the tree green.**

---

## What I refuted in the spec

The spec's central design is correct and I am adopting it: canonicalise server-side, never reject, keep `canonicalJobID`, no Python production change. Its measured acceptance surface holds - the conductor re-measured every row by execution, including the three the spec could only read from source, and they all confirm. Nine findings against the parts I checked.

### R1. The prose census is right by TOTAL and wrong by MEMBERSHIP

The spec names "eight prose sites". Eight sites do need correcting. But its list includes one that needs none and omits one that does, so the number is right by accident and the list cannot be executed as written.

- **OMITTED: `internal/cli/logs.go`, inside `watchJobLogs`'s `fatal != nil` branch** (the comment beginning "A definite answer ends the watch here"). It carries a **second** copy of the claim: *"handleEvents does not validate or canonicalise `?job_id=` (its own comment, internal/api/events.go)"* - 233 lines below the `canonicalJobID` doc comment the spec did find. The spec's own stated grep pattern, `does not validate or canonicalise`, hits it. This is a sampling miss inside a section whose whole purpose was to be exhaustive rather than sampled, which is the exact failure the section warns about.
- **INCLUDED but needing no change: `internal/cli/logs_test.go`'s block comment** beginning "A job id is argv, and an operator pastes whatever their source gave them". It is past-tense narrative about the pre-fix CLI against the pre-fix server ("Only the canonical one worked here, and it failed in the worst way available"). A correct historical note is not a defect, and rewriting it into the present tense would make it wrong. **Leave it byte-identical.** Task 5 records that as a decision so the next sweep does not re-open it.
- **Also checked and deliberately excluded: `ROADMAP.md` lines 39, 45, 483 and 664.** Lines 39 and 45 are Now/Next entries that `/roadmap` regenerates when the item closes; 483 and 664 are dated refresh and closed-item records. All four are historical by construction. Not this plan's scope, not a defect.

### R2. The spec's default-lane test cannot kill the spec's own M4, and M4 is the mutation that test exists for

Section 11.3 specifies `TestCanonicalJobIDSpellings` as a table test over "the parse-and-render pair". Against section 5's **inline three-liner**, a `package api` test can only re-express `parseUUID` + `uuidStr` itself - a second, independent copy of the production expression. The consequence:

- **M4 (an implementer hand-writes the render instead of calling `uuidStr`) SURVIVES.** The test calls `uuidStr`; the handler calls something else; both are green. The spec's own kill table assigns M4 to 11.3 and to nothing else, so under the spec as written M4 has an **empty** RED set.
- A test built out of a reconstruction of the code under test cannot detect drift in it. That is `fakeUUIDSpellingServer`'s own argument, read in the mirror direction.

**Fix:** name the expression once, as `canonicalJobIDFilter` in `internal/api/events.go`, and point the default-lane test at it. This is a deliberate departure from section 5's inline form and it costs one function. Two things fall out for free: the fail-open argument gets a doc comment **where the guard is read**, and the grammar test also kills M2 on 9 of its 10 passthrough rows, which the spec's table assigns to the integration test alone.

The departure does **not** weaken the headline RED. Task 1's test is an HTTP-level integration test that names no new symbol, so it is red at HEAD for the defect's own reason and the seam cannot destroy it.

### R3. A raw `+` in a query string is a SPACE, so the spec's most discriminating negative probe would have tested a different string

Section 11.2 names `+7e660488123443218888abcdefabcde` as a probe - the row where Python's `uuid.UUID` silently yields a *different* uuid. Built the obvious way, `httptest.NewRequest("GET", "/v1/events?job_id="+spelling, nil)`, the handler's `r.URL.Query().Get("job_id")` returns **`" 7e660488123443218888abcdefabcde"`** with a leading space: `+` is the query-string encoding of a space. The test would still pass (33 bytes, `default` branch, rejected) while exercising a string the acceptance table says nothing about, and nobody would notice because the assertion is a negative.

**Every probe in this plan is built with `url.Values{"job_id": {spelling}}.Encode()`**, which percent-encodes correctly. This is not style; it is the difference between the test the plan claims and the test it runs.

### R4. The publisher census holds; its enumeration is off by one package

Re-derived rather than inherited, by grepping every `.Publish(` in non-test Go. **11 sites. 8 carry a `JobID`, and all 8 build it as `JobID: uuidStr(...)` over a `pgtype.UUID` read from the database:**

| File:line | Type | JobID expression |
|---|---|---|
| `internal/api/jobs.go:837` | `job` (cancel) | `uuidStr(job.ID)` |
| `internal/api/jobs.go:1086` | `job` (retry) | `uuidStr(job.ID)` |
| `internal/worker/handler.go:1600` | `task` | `uuidStr(updated.JobID)` |
| `internal/worker/handler.go:1607` | `job` | `uuidStr(updated.JobID)` |
| `internal/worker/handler.go:1844` | `task_log` | `uuidStr(row.JobID)` |
| `internal/scheduler/dispatch.go:360` | `task` | `uuidStr(claimed.JobID)` |
| `internal/scheduler/dispatch.go:403` | `task` | `uuidStr(task.JobID)` |
| `internal/scheduler/dispatch.go:409` | `job` | `uuidStr(task.JobID)` |

The remaining three carry no `JobID`. The spec calls them "worker events", which is true of all three, but only **two** are in `internal/worker` (`handler.go:1060` online, `handler.go:1995` offline); the third is **`internal/metrics/sweep.go:94`**, a package the spec never names. The completeness claim survives intact. One refinement: `handler.go:1844` is a `TypeTaskLog` event, and `Publish`'s log branch routes on `TaskID` and never reads `e.JobID`, so the count that is load-bearing for **this** slice is 7, not 8.

### R5. `TestEvents_TaskIDValidation` stays green with zero assertion edits - checked against the exact input, not assumed

Its `?job_id=not-a-uuid` probe is 10 bytes. `pgtype.UUID.Scan` switches on `len(src)`, hits `default`, and returns an error, so `canonicalJobIDFilter` returns the string untouched and `assert.NotEqual(t, http.StatusBadRequest, rec.Code)` holds. **Its comment changes in Task 5; not one assertion does.** That test is the proof the change is additive and must not be edited to accommodate the fix.

### R6. The fail-open escalation is narrower than "any malformed job_id", and the negative tests must be built to stay inside it

`Publish`'s status branch reads:

```go
if f.JobID == "" && f.TaskID != "" {
    continue
}
if f.JobID == "" || f.JobID == e.JobID {
```

So a request carrying **both** a malformed `?job_id=` and a valid `?task_id=` becomes a log tail under the M2 mutation, not a broadcast - the first clause catches it. The escalation needs a **`?job_id=`-only** request. That is exactly the shape `follow_job` sends and exactly the shape both negative tests use. Stated here so nobody "tidies" the tests by folding a `task_id` into them and silently makes the M2 kill vacuous.

### R7. `?task_id=` already has the identical treatment, and after this slice the asymmetry is about rejection only

`internal/api/events.go:47` is `logTaskID = uuidStr(taskID)`, with a comment saying it exists "so the broker key matches the one handleTaskLog derives from the chunk's task id". So `?task_id=` has been canonicalised all along, **six lines above the bug**. The spec says the asymmetry is intentional; verified, and the precise statement after this slice is: *both parameters are canonicalised; only `?task_id=` rejects.* Every corrected prose site in Task 5 says exactly that, because "the asymmetry is deliberate" left unqualified now reads as a claim about normalisation and is false.

### R8. The existing Python test that LOOKS like a verbatim-passthrough guard is vacuous, so the design decision has no guard at HEAD

`python/tests/unit/test_client.py`'s `test_follow_job_yields_events_and_disables_only_the_read_timeout` asserts `captured["query"] == {"job_id": "j1"}`. That reads as a pin on "the SDK sends your string unchanged". It is not one: `"j1"` is not a UUID, and every plausible canonicaliser - `canonicalJobID` is the model, and it returns non-UUIDs unchanged - passes it through. Inserting `job_id = str(uuid.UUID(job_id))` into `_stream_events` leaves that assertion green.

So this slice's most consequential decision (*no SDK-side canonicaliser, ever, because it is unsound in one direction and incomplete in the other*) is protected by nothing. Task 6 adds a guard with an **uppercase UUID** input and proves it with that exact mutation. This is the repo's "added a property, forgot its guard" shape, caught one slice early instead of one slice late.

### R9. Confirmed against the tree, not refuted, recorded so nobody re-derives it

- `parseUUID(s string) (pgtype.UUID, error)` at `internal/api/server.go:259` - a bare `pgtype.UUID.Scan` wrapped in a `fmt.Errorf`. `uuidStr(u pgtype.UUID) string` at `internal/api/server.go:249` - returns `""` when `!u.Valid`, else `fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", ...)`. Both unexported, both in `package api`, both already called by `handleEvents`.
- `internal/events/broker.go` `Event.JobID`'s doc comment: "empty = broadcast to all". The fail-open trap is real and is one deleted character away.
- `newTestServerWithBroker`, `seedTaskViaAPI`, `gateWriter` and `gateWriter.flushed()` all live in `internal/api/events_task_log_integration_test.go` (`//go:build integration`, `package api_test`). `createTestUser` / `createTestToken` are available to it.
- A `package api` untagged test file is established practice: `internal/api/server_counters_test.go:1`. No `internal/api/events_test.go` exists yet.
- testify is **v1.11.1** (`go.mod:11`), so `assert.Never(t, cond, waitFor, tick, msgAndArgs...)` exists.
- `python/tests/unit` **runs in CI** (`.github/workflows/python.yml:36`, five Python versions x three OSes, triggered on `paths: python/**`). `python/tests/integration` runs in no workflow at all.
- `internal/cli/logs_integration_test.go`'s `runLaneLogs` drives `doLogs` -> real `handleEvents` -> real `?job_id=` over a real socket. It is the **only** lane that does, and it uses the canonical spelling, so it is a regression check for this slice rather than a subject.
- The spec's acceptance criterion "the three `[unexecuted]` rows are confirmed by actually running them" is **already discharged** by the conductor: Python `uuid.UUID` accepts `+7e660488123443218888abcdefabcde` as `07e66048-8123-4432-1888-8abcdefabcde`, accepts `0x...` as a different uuid, and accepts a PEP 515 `_` inside 32 hex chars as a different uuid; `pgtype.UUID.Scan` accepts `_`/`:`/space/mixed separators and rejects brace-wrapped and `+`-prefixed. No task in this plan needs to re-run them.
- **Spec open question 3, resolved: the acceptance table lands in the repo in its GO half only.** The comment on the default-lane test states the eight accepted and ten passed-through spellings, every one of which that test actually exercises. The **Python** column does not go into a Go comment: no Go test runs `uuid.UUID`, and prose about behaviour nothing checks is this repo's dominant defect. The Go comment points at the spec by path for the Python side.

---

## File structure

| Path | Action | Responsibility |
|---|---|---|
| `internal/api/events.go` | Modify | The whole production change: `canonicalJobIDFilter` plus its one call site, plus the comment that stops being true. |
| `internal/api/events_test.go` | **Create** | Default lane (`package api`, **no build tag**). The grammar: which spellings canonicalise, which pass through. Runs under `make test`, no Docker. |
| `internal/api/events_task_log_integration_test.go` | Modify | Two new tests (the headline RED, and the scope pin that kills the fail-open mutation) plus one comment correction. Already owns `newTestServerWithBroker`, `seedTaskViaAPI` and `gateWriter`. |
| `internal/cli/logs.go` | Modify | **Comments only, two of them.** `canonicalJobID` is NOT deleted and its body is NOT touched. |
| `internal/cli/logs_test.go` | Modify | **One comment.** `fakeUUIDSpellingServer`'s behaviour and every assertion stay byte-identical. |
| `README.md` | Modify | The Events "Validation" paragraph, plus a new "Normalisation" paragraph. |
| `python/src/relay/client.py` | Modify | **`follow_job`'s docstring only.** No production Python change, so no version bump. |
| `python/README.md` | Modify | The "Following a job" section. |
| `python/tests/unit/test_client.py` | Modify | One new test: the SDK sends the caller's spelling verbatim, proved against an uppercase UUID. |

Untouched and deliberately so: `internal/events/` (the broker stays UUID-ignorant), `internal/cli/logs.go`'s `canonicalJobID` **body**, `internal/relayclient/`, `internal/mcp/`, all of `web/`, `python/src/relay/_version.py`, `python/pyproject.toml`.

---

## Task 1: The failing test - a spelling the server accepts must subscribe to the job it names

**Files:**
- Test: `internal/api/events_task_log_integration_test.go` (append at end of file)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/events_task_log_integration_test.go`. Add `"net/url"` to the import block (it currently imports `bytes`, `context`, `encoding/json`, `net/http`, `net/http/httptest`, `strings`, `sync`, `testing`, `time` plus the four project/third-party groups).

```go
// TestEvents_JobIDSpellingIsCanonicalisedNotRejected is the headline test for
// bug-2026-08-27-python-sdk-follow-job-hangs-on-noncanonical-job-id.
//
// GET /v1/jobs/{id} accepts every spelling pgtype.UUID.Scan takes; the broker's
// status filter is an exact string compare against a JobID that every publisher
// builds with uuidStr. So before this slice, an id that answered 200 on the REST
// route subscribed to a filter nothing could ever match - an open, silently
// empty SSE stream, forever, on a client with no read timeout.
//
// THE UNDERSCORE CASE IS FIRST AND IS NOT DECORATION. pgtype.UUID.Scan slices
// indexes 8, 13, 18 and 23 out of the 36-byte form WITHOUT EXAMINING THEM, so
// any byte may sit there. That row is the one no client-side canonicaliser built
// on Python's uuid.UUID can normalise, and it is therefore the single assertion
// that discriminates a server-side fix from every SDK-side one. A discriminating
// input placed last cannot detect an early-exit defect, so it leads.
//
// Every probe is built through url.Values. Concatenating the spelling into the
// query string directly is wrong for at least one spelling in the sibling test
// below - a raw `+` decodes to a SPACE - and a negative assertion on the wrong
// string passes for the wrong reason.
//
// Bounding: a httptest request's context is never cancelled, so a handler that
// streams forever would hang the package rather than fail the test. Each subtest
// owns a cancel and every wait is a bounded require.Eventually.
func TestEvents_JobIDSpellingIsCanonicalisedNotRejected(t *testing.T) {
	srv, q, broker, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-spelling@example.com", false)
	token := createTestToken(t, q, user.ID)
	jobID, _ := seedTaskViaAPI(t, srv, token)

	for _, sp := range []struct{ name, id string }{
		{"underscore separators", strings.ReplaceAll(jobID, "-", "_")},
		{"uppercase", strings.ToUpper(jobID)},
		{"dashless", strings.ReplaceAll(jobID, "-", "")},
	} {
		sp := sp
		t.Run(sp.name, func(t *testing.T) {
			require.NotEqual(t, jobID, sp.id,
				"the probe must differ from the canonical spelling, or this subtest proves nothing; "+
					"for the uppercase row this can only happen if gen_random_uuid() produced 32 "+
					"decimal digits, about 1 in 6.7 million - re-run")

			vals := url.Values{"job_id": {sp.id}}
			req := httptest.NewRequest("GET", "/v1/events?"+vals.Encode(), nil)
			req.Header.Set("Authorization", "Bearer "+token)
			ctx, cancel := context.WithCancel(req.Context())
			gw := newGateWriter()
			close(gw.release) // never block; the handler should write normally
			done := make(chan struct{})
			go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(ctx)) }()
			defer func() { cancel(); <-done }()

			// A recorded Flush is the file's deterministic barrier for "this
			// subscription is live": handleEvents subscribes and then flushes,
			// before its first receive. No sleeps.
			require.Eventually(t, func() bool { return gw.flushed() >= 1 },
				5*time.Second, 5*time.Millisecond, "the subscription never became live")

			// The canonical spelling is what every production publisher emits.
			broker.Publish(events.Event{
				Type:  "job",
				JobID: jobID,
				Data:  []byte(`{"status":"done","probe":"canonicalised"}`),
			})

			require.Eventually(t, func() bool {
				return strings.Contains(gw.body(), `"probe":"canonicalised"`)
			}, 5*time.Second, 5*time.Millisecond,
				"a spelling GET /v1/jobs/{id} ACCEPTS must subscribe to the job it names")
		})
	}
}
```

- [ ] **Step 2: Run it and watch it fail for the right reason**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestEvents_JobIDSpellingIsCanonicalisedNotRejected -v -timeout 300s
```

Expected: **FAIL**, all three subtests, each after its own 5 s `Eventually` budget:

```
    --- FAIL: TestEvents_JobIDSpellingIsCanonicalisedNotRejected/underscore_separators (5.0Xs)
        events_task_log_integration_test.go:NNN:
            	Error:      	Condition never satisfied
            	Test:       	TestEvents_JobIDSpellingIsCanonicalisedNotRejected/underscore_separators
            	Messages:   	a spelling GET /v1/jobs/{id} ACCEPTS must subscribe to the job it names
    --- FAIL: TestEvents_JobIDSpellingIsCanonicalisedNotRejected/uppercase (5.0Xs)
    --- FAIL: TestEvents_JobIDSpellingIsCanonicalisedNotRejected/dashless (5.0Xs)
FAIL
```

The `flushed() >= 1` barrier PASSES in every subtest - the handler accepts the request and opens the stream. Only the frame never arrives. That is the defect exactly: not a rejection, a subscription to a filter that matches nothing.

If instead the barrier fails, the request was rejected and something other than this bug is wrong - stop and diagnose rather than proceeding.

- [ ] **Step 3: Commit the RED**

```bash
git add internal/api/events_task_log_integration_test.go
git commit -m "test(api): RED - an accepted job id spelling subscribes to nothing"
```

---

## Task 2: The fix - canonicalise the subscribe side, and correct the comment that stops being true

**Files:**
- Modify: `internal/api/events.go:50-53` (the comment and the `jobID :=` line) and append the new function at end of file

- [ ] **Step 1: Replace the comment and the read**

`internal/api/events.go` currently reads, at lines 50-53:

```go
	// ?job_id= is deliberately NOT validated: an unknown job has always yielded
	// an open, permanently empty stream, and that is an existing contract with
	// existing clients. The asymmetry with task_id is intentional.
	jobID := r.URL.Query().Get("job_id")
```

Replace those four lines with:

```go
	// ?job_id= is still deliberately NOT VALIDATED: an unknown or unparseable
	// job id yields an open, permanently empty stream rather than a 4xx, and
	// that is an existing contract with existing clients (README.md, "Events",
	// and TestEvents_TaskIDValidation asserts the `not-a-uuid` case is not a
	// 400). The asymmetry with task_id is intentional and is about REJECTION
	// only - both parameters are canonicalised, task_id eleven lines above and
	// job_id here since 2026-08-30. See canonicalJobIDFilter for why the
	// unparseable case must pass through UNCHANGED rather than be rendered.
	jobID := canonicalJobIDFilter(r.URL.Query().Get("job_id"))
```

- [ ] **Step 2: Append the function at the end of `internal/api/events.go`**

```go
// canonicalJobIDFilter renders raw in the one spelling every publisher emits,
// and returns raw UNCHANGED when it is not a UUID this server accepts.
//
// The server accepts far more spellings than it emits. parseUUID is
// pgtype.UUID.Scan, which takes hex case-insensitively, takes the dashless
// 32-character form, and on the 36-byte form slices out indexes 8, 13, 18 and
// 23 WITHOUT EXAMINING THEM - so `7e660488_1234_4321_8888_abcdefabcdef` names
// the same job as the canonical spelling, and GET /v1/jobs/{id} answers 200 for
// it. uuidStr renders exactly one of those spellings, and every JobID-carrying
// broker.Publish in the tree builds its value with uuidStr over a pgtype.UUID
// read from the database. internal/events' filter is an exact string compare,
// so without this an accepted-but-non-canonical id subscribed to a filter
// nothing could ever match: an open, silently empty stream forever.
//
// THE err != nil GUARD IS THE WHOLE CORRECTNESS ARGUMENT, NOT NOISE. parseUUID
// returns pgtype.UUID{} on failure and uuidStr returns "" for an invalid UUID,
// and Filter{JobID: ""} is the broker's BROADCAST subscription - Publish's
// status branch delivers to every filter whose JobID is empty. Rendering
// unconditionally would therefore promote every typo'd ?job_id= from "one job,
// silently empty" into "every job on the cluster": a silent change of scope from
// what the caller wrote, and the one way this change can be worse than doing
// nothing. Gate the render on the parse having actually succeeded, the same
// shape as gating a write on a fence having actually matched.
// TestEvents_JobIDRejectedSpellingsAreNotCanonicalised is the test that dies
// when this guard is deleted, and it asserts SCOPE rather than absence of error
// because a fail-open here is an escalation, not a crash.
//
// The !u.Valid arm mirrors internal/cli/logs.go's canonicalJobID and is
// belt-and-braces against a pgx whose Scan reports success without setting
// Valid. It costs one comparison; being wrong costs the broadcast above.
func canonicalJobIDFilter(raw string) string {
	u, err := parseUUID(raw)
	if err != nil || !u.Valid {
		return raw
	}
	return uuidStr(u)
}
```

- [ ] **Step 3: Run the headline test and watch it pass**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestEvents_JobIDSpellingIsCanonicalisedNotRejected -v -timeout 300s
```

Expected: **PASS**, all three subtests, each in well under a second (the `Eventually` returns on its first or second tick).

- [ ] **Step 4: Prove the change is ADDITIVE - the existing contract test is still green, unedited**

```bash
go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_' -v -timeout 300s
```

Expected: **PASS** for `TestEvents_TaskIDValidation`, `TestEvents_DroppedFrameOnSlowConsumer`, `TestEvents_NoDroppedFrameWhenTheClientDisconnects`, `TestEvents_DeliveryMatrix` and the new test. `TestEvents_TaskIDValidation`'s `assert.NotEqual(t, http.StatusBadRequest, rec.Code)` on `?job_id=not-a-uuid` is the one that matters: `not-a-uuid` is 10 bytes, `pgtype`'s `default` branch, so it passes through and there is still no 400. **Do not edit that test to make anything pass.**

- [ ] **Step 5: Default lane, unaffected**

```bash
go test ./internal/api/... ./internal/events/... ./internal/cli/... -count=1
```

Expected: **ok** for all three.

- [ ] **Step 6: Check the line endings before committing (CRLF repo)**

```bash
git diff --stat internal/api/events.go
git ls-files --eol internal/api/events.go
```

Expected: a diffstat in the region of `1 file changed, ~40 insertions(+), 4 deletions(-)`, and `i/lf` for the file. A diffstat wildly larger than the edit means the file was reclassified - stop and fix before committing.

- [ ] **Step 7: Commit**

```bash
git add internal/api/events.go
git commit -m "fix(api): canonicalise ?job_id= so an accepted spelling subscribes to its job"
```

---

## Task 3: The scope pin - a spelling the server rejects must not be widened into one it accepts

This test is **not** RED at HEAD, and the plan says so rather than pretending. At HEAD there is no canonicalisation, so rejected spellings pass through and the frame does not arrive - green, vacuously. Its RED is proved by **mutation**, in Step 3, and the discriminating inputs survive into the permanent test. That is the honest form of a fail-open guard.

**Files:**
- Test: `internal/api/events_task_log_integration_test.go` (append)

- [ ] **Step 1: Write the test**

```go
// TestEvents_JobIDRejectedSpellingsAreNotCanonicalised is the other direction of
// the item's acceptance criterion, and the test that kills the one mutation that
// makes this slice WORSE than doing nothing.
//
// uuidStr returns "" for an invalid pgtype.UUID, and events.Filter{JobID: ""} is
// the broker's BROADCAST scope (internal/events/broker.go: "empty = broadcast to
// all"). So `u, _ := parseUUID(raw); return uuidStr(u)` - one deleted guard -
// turns every unparseable ?job_id= into a whole-cluster status feed. This test
// asserts SCOPE, not the absence of an error: a fail-open here is an escalation
// and nothing about it looks like a failure.
//
// Each probe is ?job_id= ONLY. Publish's status branch skips a filter whose
// JobID is "" and whose TaskID is not, so a probe carrying a task_id would be
// routed as a log tail under the mutation and the kill would be vacuous. Do not
// add a task_id to these.
//
// Each probe is built through url.Values because a raw `+` in a query string
// decodes to a SPACE - the sign-prefixed row would otherwise arrive as a
// 33-byte string with a leading space, still rejected, but not the string this
// test claims to be about.
//
// The four spellings are the ones Python's uuid.UUID ACCEPTS and this server
// does not (spec section 4.4). For the sign-prefixed row uuid.UUID does not
// merely over-accept: it resolves to a DIFFERENT uuid than the string names.
// That is why no canonicaliser may live in a client.
//
// A rejected spelling is still not a 4xx: the flushed()>=1 barrier and the
// Content-Type check below are the observable form of that, because gateWriter
// discards the status code.
func TestEvents_JobIDRejectedSpellingsAreNotCanonicalised(t *testing.T) {
	srv, q, broker, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-rejected@example.com", false)
	token := createTestToken(t, q, user.ID)
	jobID, _ := seedTaskViaAPI(t, srv, token)

	open := func(t *testing.T, spelling string) *gateWriter {
		t.Helper()
		vals := url.Values{"job_id": {spelling}}
		req := httptest.NewRequest("GET", "/v1/events?"+vals.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		ctx, cancel := context.WithCancel(req.Context())
		gw := newGateWriter()
		close(gw.release)
		done := make(chan struct{})
		go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(ctx)) }()
		t.Cleanup(func() { cancel(); <-done })
		require.Eventually(t, func() bool { return gw.flushed() >= 1 },
			5*time.Second, 5*time.Millisecond,
			"the subscription never became live for %q - a rejected spelling must still open a stream", spelling)
		assert.Equal(t, "text/event-stream", gw.header().Get("Content-Type"),
			"%q must be an open stream, never a 4xx", spelling)
		return gw
	}

	// Positive control first, so the negatives below cannot pass vacuously.
	control := open(t, jobID)

	dashless := strings.ReplaceAll(jobID, "-", "")
	rejected := map[string]*gateWriter{}
	for name, spelling := range map[string]string{
		"brace wrapped":   "{" + jobID + "}",
		"urn prefixed":    "urn:uuid:" + jobID,
		"trailing hyphen": jobID + "-",
		"sign prefixed":   "+" + dashless[:31],
	} {
		rejected[name] = open(t, spelling)
	}

	broker.Publish(events.Event{
		Type:  "job",
		JobID: jobID,
		Data:  []byte(`{"status":"done","probe":"scope"}`),
	})

	require.Eventually(t, func() bool { return strings.Contains(control.body(), `"probe":"scope"`) },
		5*time.Second, 5*time.Millisecond,
		"the canonical control never received the frame - every negative below would be vacuous")

	for name, gw := range rejected {
		name, gw := name, gw
		assert.Never(t, func() bool { return strings.Contains(gw.body(), `"probe":"scope"`) },
			500*time.Millisecond, 25*time.Millisecond,
			"%s: a spelling the server REJECTS must not be silently widened into one it accepts. "+
				"Receiving this frame means the filter became \"\", which is the broker's BROADCAST scope",
			name)
	}
}
```

- [ ] **Step 2: Run it and watch it pass**

```bash
go test -tags integration -p 1 ./internal/api/... -run TestEvents_JobIDRejectedSpellingsAreNotCanonicalised -v -timeout 300s
```

Expected: **PASS** in roughly one second (five subscriptions, one publish, one 500 ms `Never` window shared across the four negatives).

- [ ] **Step 3: Prove the RED by mutation - this is the step that makes the test non-vacuous**

Edit `internal/api/events.go` and replace the body of `canonicalJobIDFilter` with the unguarded form:

```go
func canonicalJobIDFilter(raw string) string {
	u, _ := parseUUID(raw)
	return uuidStr(u)
}
```

```bash
go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_JobID' -v -timeout 300s
```

Expected:

- `TestEvents_JobIDRejectedSpellingsAreNotCanonicalised` **FAILS**, four times - one `Condition satisfied` from `assert.Never` per rejected spelling, each with the BROADCAST message.
- `TestEvents_JobIDSpellingIsCanonicalisedNotRejected` still **PASSES**. That asymmetry is the point: the headline test cannot see this mutation, so the guard needed its own subject.

Now revert the mutation:

```bash
git checkout -- internal/api/events.go
git diff --stat internal/api/events.go   # expect: no output
git ls-files --eol internal/api/events.go   # expect: i/lf
```

Re-run to confirm green again:

```bash
go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_JobID' -v -timeout 300s
```

Expected: **PASS** for both.

- [ ] **Step 4: Commit**

```bash
git add internal/api/events_task_log_integration_test.go
git commit -m "test(api): pin subscription SCOPE - a rejected job id spelling is never widened"
```

---

## Task 4: The grammar, in the default lane

Also mutation-proved rather than defect-RED, for the reason stated in Task 3, and for one more: sequencing it before `canonicalJobIDFilter` exists would make it fail to compile, and a compile error is not a behavioural kill.

**Files:**
- Create: `internal/api/events_test.go`

- [ ] **Step 1: Write the file**

```go
package api

import "testing"

// TestCanonicalJobIDFilter is the cheap, exhaustive statement of what
// ?job_id= accepts, and the only place a pgx upgrade that narrowed or widened
// pgtype.UUID.Scan would be caught. It runs on `make test` with no container.
//
// Every row below is exercised by this test, so this table is proof rather than
// prose. The PYTHON half of the acceptance surface - the seven spellings
// uuid.UUID takes and this server does not, three of which uuid.UUID resolves to
// a DIFFERENT uuid than the string names - is deliberately NOT restated here,
// because no Go test runs uuid.UUID and a comment about behaviour nothing checks
// is exactly this repo's dominant defect. It is measured, with its instrument,
// in docs/superpowers/specs/2026-08-30-python-sdk-follow-job-canonical-id.md
// section 4.
//
// This test says NOTHING about whether handleEvents calls the function. That is
// TestEvents_JobIDSpellingIsCanonicalisedNotRejected's job, in the integration
// lane. Both, or neither is worth much.
func TestCanonicalJobIDFilter(t *testing.T) {
	const canonical = "7e660488-1234-4321-8888-abcdefabcdef"

	// pgtype.UUID.Scan succeeds iff the input is 32 bytes of hex, or 36 bytes
	// whose indexes 0-7, 9-12, 14-17, 19-22 and 24-35 are hex. Indexes 8, 13, 18
	// and 23 are sliced out and NEVER EXAMINED, which is what admits the four
	// separator rows - and what no client-side canonicaliser built on Python's
	// uuid.UUID can reproduce. Hex is case-insensitive by table lookup, not by a
	// normalisation step.
	accepted := []struct{ name, in string }{
		{"canonical", canonical},
		{"uppercase", "7E660488-1234-4321-8888-ABCDEFABCDEF"},
		{"dashless", "7e660488123443218888abcdefabcdef"},
		{"dashless uppercase", "7E660488123443218888ABCDEFABCDEF"},
		{"underscore separators", "7e660488_1234_4321_8888_abcdefabcdef"},
		{"colon separators", "7e660488:1234:4321:8888:abcdefabcdef"},
		{"space separators", "7e660488 1234 4321 8888 abcdefabcdef"},
		{"mixed separators", "7e660488-1234*4321-8888-abcdefabcdef"},
	}
	for _, tc := range accepted {
		tc := tc
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			if got := canonicalJobIDFilter(tc.in); got != canonical {
				t.Fatalf("canonicalJobIDFilter(%q) = %q, want %q", tc.in, got, canonical)
			}
		})
	}

	// Everything else is returned BYTE-IDENTICAL. Never "", which would be the
	// broker's broadcast filter - see canonicalJobIDFilter's doc comment. The
	// empty row is the one case where "unchanged" and "broadcast" coincide, and
	// it is today's behaviour for GET /v1/events with no job_id at all.
	passthrough := []struct{ name, in string }{
		{"empty", ""},
		{"brace wrapped", "{" + canonical + "}"},
		{"urn prefixed", "urn:uuid:" + canonical},
		{"trailing hyphen", canonical + "-"},
		{"hyphen at a non-canonical position", "7e6604881234432188-88abcdefabcdef"},
		{"sign prefixed", "+7e660488123443218888abcdefabcde"},
		{"base prefixed", "0x7e660488123443218888abcdefabcd"},
		{"pep515 underscore inside 32 hex chars", "7e660488_23443218888abcdefabcdef"},
		{"not a uuid at all", "not-a-uuid"},
		// 36 BYTES, not 36 characters: two hex positions replaced by one
		// two-byte rune. The length test is over bytes, so this reaches the
		// 36-byte branch and is rejected by the hex table (a continuation byte
		// exceeds 0x7f and maps to 0xff).
		{"multi-byte rune occupying two hex positions", "7e6604é-1234-4321-8888-abcdefabcdef"},
	}
	for _, tc := range passthrough {
		tc := tc
		t.Run("passthrough/"+tc.name, func(t *testing.T) {
			if got := canonicalJobIDFilter(tc.in); got != tc.in {
				t.Fatalf("canonicalJobIDFilter(%q) = %q, want it returned UNCHANGED", tc.in, got)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and watch it pass**

```bash
go test ./internal/api/... -run TestCanonicalJobIDFilter -v -count=1
```

Expected: **PASS**, 18 subtests, in milliseconds. No Docker.

If `accepted/multi-byte...` or any passthrough row unexpectedly canonicalises, the pinned pgx has changed behaviour - that is exactly what this test is for; do not adjust the expectation without reading `pgtype/uuid.go` at the pinned version.

- [ ] **Step 3: Prove it discriminates - two mutations, both must kill**

**M4 (hand-written render, one group width wrong).** Replace `return uuidStr(u)` in `internal/api/events.go` with:

```go
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%011x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
```

(add `"fmt"` if the compiler asks; it is already imported in `events.go`).

```bash
go test ./internal/api/... -run TestCanonicalJobIDFilter -v -count=1
```

Expected: **FAIL** on all 8 `accepted/` subtests. This is the mutation the spec assigned to this test and that the spec's own inline design could not kill - see R2.

Revert (`git checkout -- internal/api/events.go`), confirm `git diff --stat` is empty and `git ls-files --eol internal/api/events.go` reads `i/lf`.

**M2 again, in the cheap lane.** Re-apply the unguarded body from Task 3 Step 3.

```bash
go test ./internal/api/... -run TestCanonicalJobIDFilter -v -count=1
```

Expected: **FAIL** on **9 of 10** `passthrough/` subtests. `passthrough/empty` survives, because `uuidStr(pgtype.UUID{})` is `""` and the input was `""` - the one input for which "unchanged" and "broadcast" are the same string. Record that: it is why the integration test in Task 3, not this one, is the guard of record for M2.

Revert and re-confirm green.

- [ ] **Step 4: Commit**

```bash
git add internal/api/events_test.go
git commit -m "test(api): pin the ?job_id= acceptance grammar in the default lane"
```

---

## Task 5: The prose sweep, by symbol

Six edits across four files. Enumerated by SYMBOL, never by line number, because Tasks 1-4 have already moved the line numbers in two of them. Every replacement below is exact text: do not paraphrase.

**Files:**
- Modify: `internal/api/events_task_log_integration_test.go` (in `TestEvents_TaskIDValidation`)
- Modify: `internal/cli/logs.go` (in `canonicalJobID`'s doc comment, and in `watchJobLogs`)
- Modify: `internal/cli/logs_test.go` (in `fakeUUIDSpellingServer`'s doc comment)
- Modify: `README.md` (the Events "Validation" paragraph)

Site 1, `internal/api/events.go`'s `jobID :=` comment, shipped in Task 2 with the code. That is deliberate: the file whose behaviour changed must never be committed carrying the sentence the change falsified.

- [ ] **Step 1: `internal/api/events_task_log_integration_test.go`, inside `TestEvents_TaskIDValidation`**

Find the comment beginning `// ?job_id= validation is deliberately UNCHANGED`. Replace those four comment lines with:

```go
	// ?job_id= VALIDATION is deliberately unchanged, and this assertion is the
	// proof that the 2026-08-30 canonicalisation was additive: `not-a-uuid` is
	// 10 bytes, so pgtype.UUID.Scan takes its default branch, canonicalJobIDFilter
	// returns the string untouched, and this is still an open silently empty
	// stream rather than a 400. It is an existing contract with existing clients.
	// The asymmetry with task_id is intentional and is about REJECTION only -
	// both parameters are canonicalised. See
	// TestEvents_JobIDSpellingIsCanonicalisedNotRejected.
	// (Served with a cancelled context so the handler returns immediately.)
```

The four assertion lines below it are untouched.

- [ ] **Step 2: `internal/cli/logs.go`, `canonicalJobID`'s doc comment - the highest-risk edit in this slice**

Two of its four paragraphs assert that `handleEvents` does not canonicalise. **The obvious edit - delete the paragraph - deletes the sentence explaining why argv is canonicalised before either request line is built, which is the sentence that keeps this function alive.** Replace the third paragraph (beginning `// Two things here read the id`) with two paragraphs, and leave the fourth (beginning `// Canonicalising ARGV`) **byte-identical**:

```go
// ONE thing here still reads the id and does not tolerate a second spelling:
// jobSnapshotUnusable compares the body's id against ours, so a canonical answer
// to a non-canonical request reads as a response about a different job. That
// comparison is entirely client-side and no server change can reach it, which is
// why this function is not redundant and must not be deleted.
//
// The SECOND reader was handleEvents, and it stopped being one on 2026-08-30:
// canonicalJobIDFilter (internal/api/events.go) now renders an accepted
// `?job_id=` into the one spelling every publisher emits, so a non-canonical
// subscription matches. `?job_id=` is still not VALIDATED - an unparseable id
// passes through and still buys an open, permanently empty stream on a
// connection with no heartbeat and no server-side timeout - and this function
// still keeps a non-canonical spelling out of the request line against an OLDER
// relay-server, which a CLI built from this tree may be pointed at.
```

- [ ] **Step 3: `internal/cli/logs.go`, inside `watchJobLogs` - the site the spec missed**

Find the comment inside the `if fatal != nil {` branch, beginning `// A definite answer ends the watch here`. Replace the clause `handleEvents does not validate or canonicalise `?job_id=` (its own comment, internal/api/events.go), so an id naming no job gets an open,` so the sentence reads:

```go
			// A definite answer ends the watch here, and this is the arm the whole
			// slice exists for. The stream cannot improve on it: handleEvents
			// canonicalises `?job_id=` but still does not VALIDATE it (its own
			// comment, internal/api/events.go), so an id naming no job - whether
			// it parses or not - gets an open, permanently empty stream with no
			// heartbeat and no server-side timeout, against a context cmd/relay
			// gives no deadline. Falling through would print nothing on either
			// stream until Ctrl-C - and a well-formed uuid that names no job is
			// the likeliest thing an operator mistypes into this command.
```

The two paragraphs after it (`// The error is carried out through the defer ...`) are untouched.

- [ ] **Step 4: `internal/cli/logs_test.go`, `fakeUUIDSpellingServer`'s doc comment**

Spec open question 2, resolved: **relabel, do not rework.** The fixture's `accepted` map, its handler, and every assertion in `TestWatchJobLogs_NonCanonicalJobID_IsResolvedNotRejected` stay byte-identical - it exists to prove the CLI works against a server that does not canonicalise, and that server still exists in the field.

Replace the header line and the second bullet:

```go
// fakeUUIDSpellingServer models a PRE-2026-08-30 relay-server: the three things
// every version does with a job id, plus the one thing versions before that date
// did not do. It is deliberately NOT updated to the current server - `relay logs`
// may be pointed at any server, the older one still exists in the field, and this
// fixture is the only thing proving the CLI works against it.
//
//   - GET /v1/jobs/{id} accepts every spelling pgtype.UUID.Scan takes - hex is
//     case-insensitive and the dashless 32-char form parses - and always answers
//     with the canonical lowercase-dashed form uuidStr renders.
//   - GET /v1/events?job_id= is taken VERBATIM here, and the broker's filter is
//     an exact string compare (internal/events/broker.go), so a non-canonical
//     spelling matches nothing that is published. A server from 2026-08-30
//     onward canonicalises it first (canonicalJobIDFilter, internal/api/events.go)
//     and would deliver; that server is not what this fixture models.
//   - That stream is then held open with no heartbeat and no timeout, which is
//     what turns "matches nothing" into a hang rather than an error.
```

The final paragraph (`// The accepted spellings are written out literally ...`) is untouched.

**Do NOT touch** the block comment further down beginning `// A job id is argv, and an operator pastes whatever their source gave them`. It is past-tense narrative about the pre-fix CLI against the pre-fix server and is a correct historical note; the spec listed it for completeness and it needs no change. Rewriting it into the present tense would make it wrong.

- [ ] **Step 5: `README.md`, the Events "Validation" paragraph**

Replace the paragraph beginning `**Validation.** \`?task_id=\` returns \`400\`` with:

```markdown
**Validation.** `?task_id=` returns `400` for a malformed UUID and `404` for an
unknown task. `?job_id=` is not validated - an unknown or unparseable job id
yields an open but permanently empty stream rather than an error. This
asymmetry is deliberate: `?job_id=` is an existing contract with existing
clients, while an unvalidated `?task_id=` would look identical to "this task
produced no output".

**Normalisation.** The asymmetry above is about REJECTION only. Both parameters
are canonicalised. Any spelling the server accepts - uppercase hex, the dashless
32-character form, and the 36-character form with any byte in the four separator
positions - is normalised to the lowercase dashed form the server emits, so
`?job_id=7E660488-1234-4321-8888-ABCDEFABCDEF` subscribes to the job it names
rather than to a filter nothing matches. A spelling the server does not accept
is passed through unchanged and is never widened into one it does accept.
```

- [ ] **Step 6: Confirm no known-false sentence survives**

```bash
git grep -n "does not validate or canonicalise" -- ':!docs'
git grep -n "deliberately NOT validated" -- ':!docs'
git grep -n "NOT validated or canonicalised" -- ':!docs'
```

Expected: **no matches** for all three. A grep is the instrument for finding these, never the claim - read each replacement and confirm it is true. `docs/` and `ROADMAP.md` are excluded on purpose: specs, retros, closed items and dated refresh entries are historical records.

- [ ] **Step 7: Run the two suites these files belong to**

```bash
go test ./internal/cli/... ./internal/api/... -count=1
go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_' -v -timeout 300s
```

Expected: **ok** and **PASS**. These are all comment edits, so a failure here means a comment terminator was mangled.

- [ ] **Step 8: Check the diffstat against the size of the change, then commit**

```bash
git diff --stat
git ls-files --eol internal/api/events_task_log_integration_test.go internal/cli/logs.go internal/cli/logs_test.go README.md
```

Expected: five files, on the order of 60 insertions and 30 deletions; every path `i/lf`. A `README.md` diffstat in the hundreds or thousands means the file was reclassified as binary - stop, revert, and re-apply with an exact-anchor replacement.

```bash
git add internal/api/events_task_log_integration_test.go internal/cli/logs.go internal/cli/logs_test.go README.md
git commit -m "docs: correct the six sites that said ?job_id= is not canonicalised"
```

---

## Task 6: The Python SDK - zero production diff, one real guard, two corrected docs

**The item closes with no change to `python/src/relay/client.py`'s code, and therefore no version bump.** `python/src/relay/_version.py` and `pyproject.toml` are kept in lockstep by `test_version_files_are_in_lockstep`, so moving one alone is RED; a docstring moves neither.

What the SDK does ship: a docstring paragraph, a README paragraph, and the guard the decision has been missing (R8).

**Files:**
- Modify: `python/src/relay/client.py` (`follow_job`'s docstring only)
- Modify: `python/README.md` ("Following a job")
- Test: `python/tests/unit/test_client.py` (append after `test_follow_job_without_a_token_raises_before_the_request`)

- [ ] **Step 1: Write the guard test**

```python
def test_follow_job_sends_the_callers_job_id_spelling_verbatim() -> None:
    """The SDK must NOT canonicalise the job id, and nothing pinned that.

    test_follow_job_yields_events_and_disables_only_the_read_timeout asserts
    `captured["query"] == {"job_id": "j1"}`, which READS like a verbatim
    guard and is not one: "j1" is not a UUID, and every plausible
    canonicaliser - internal/cli/logs.go's canonicalJobID is the model -
    passes a non-UUID through unchanged. Inserting
    `job_id = str(uuid.UUID(job_id))` into _stream_events leaves that
    assertion green. This test uses an UPPERCASE UUID, which is exactly the
    input such a canonicaliser rewrites.

    Canonicalising here would be wrong in both directions, measured in
    docs/superpowers/specs/2026-08-30-python-sdk-follow-job-canonical-id.md
    section 4. uuid.UUID REJECTS four spellings pgtype.UUID.Scan accepts
    (any byte may sit in the four separator positions of the 36-character
    form), so a Python canonicaliser cannot fix the very ids it is needed
    for; and it ACCEPTS seven the server rejects, three of which resolve to
    a DIFFERENT uuid than the string names - so it would silently subscribe
    the caller to the wrong job. The server canonicalises instead, since
    2026-08-30 (internal/api/events.go, canonicalJobIDFilter).
    """
    uppercase = "7E660488-1234-4321-8888-ABCDEFABCDEF"
    captured: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["query"] = dict(request.url.params)
        return httpx.Response(
            200, text="", headers={"content-type": "text/event-stream"}
        )

    client = _make_client(handler)
    list(client.follow_job(uppercase))

    assert captured["query"] == {"job_id": uppercase}
```

- [ ] **Step 2: Run it and watch it pass**

```bash
cd python && python -m pytest tests/unit/test_client.py -k follow_job -v
```

Expected: **PASS**, three `follow_job` tests.

- [ ] **Step 3: Prove the guard discriminates - insert the canonicaliser and watch exactly one test die**

Temporarily edit `python/src/relay/client.py`. Add `import uuid as _uuid` at the top of the imports, and insert at the very start of `_stream_events`'s body, before `base = self._http.timeout`:

```python
        try:
            job_id = str(_uuid.UUID(job_id))
        except ValueError:
            pass
```

```bash
cd python && python -m pytest tests/unit/test_client.py -k follow_job -v
```

Expected:

- `test_follow_job_sends_the_callers_job_id_spelling_verbatim` **FAILS**:
  `AssertionError: assert {'job_id': '7e660488-1234-4321-8888-abcdefabcdef'} == {'job_id': '7E660488-1234-4321-8888-ABCDEFABCDEF'}`
- `test_follow_job_yields_events_and_disables_only_the_read_timeout` still **PASSES**. That is the demonstration that the pre-existing assertion was blind, and it is the reason this test exists.

Revert:

```bash
git checkout -- python/src/relay/client.py
git diff --stat python/src/relay/client.py   # expect: no output
```

- [ ] **Step 4: Add the docstring paragraph to `follow_job`**

Insert as a new paragraph immediately before the final line `The underlying HTTP connection is closed on generator exit.`:

```python
        The server normalises the id's SPELLING, so any form
        ``GET /v1/jobs/{id}`` accepts - uppercase hex, the dashless
        32-character form - subscribes to the job it names. The SDK
        deliberately sends your string unchanged: Python's ``uuid.UUID`` and
        the server's parser accept different sets in BOTH directions, and for
        three spellings ``uuid.UUID`` accepts (``+<31 hex>``, ``0x<30 hex>``,
        and a PEP 515 ``_`` inside 32 hex digits) it yields a DIFFERENT id
        than the string names - so canonicalising here could subscribe you to
        the wrong job. Against a relay-server older than 2026-08-30 a
        non-canonical spelling still yields a permanently empty stream; if you
        may be talking to one, pass the id exactly as ``get_job()`` returned it.
```

- [ ] **Step 5: Add the same disclosure to `python/README.md`, "Following a job"**

Insert immediately after the code block in that section, before the `Or use \`wait(id)\`` paragraph:

```markdown
**Job id spelling.** The server normalises `?job_id=`, so any spelling
`get_job(id)` accepts - uppercase hex, the dashless 32-character form -
follows the job it names. The SDK sends your string unchanged on purpose:
Python's `uuid.UUID` and the server's parser accept different sets in both
directions, and for three spellings `uuid.UUID` accepts it resolves to a
*different* id than the string names, so canonicalising client-side could
follow the wrong job. Against a `relay-server` older than 2026-08-30 a
non-canonical spelling still yields a permanently empty stream - pass the id
exactly as `get_job()` returned it if you may be talking to one.
```

- [ ] **Step 6: Run the full Python gate**

```bash
cd python && python -m pytest tests/unit -q
cd python && ruff check src tests
cd python && mypy src
```

Expected: all green. `test_version_files_are_in_lockstep` passes untouched - nothing moved a version.

**`python/tests/integration/` gets no new test.** It is a manual lane in no workflow (`.github/workflows/python.yml` runs `pytest tests/unit` only), so an assertion added there would be a claim nobody runs. There is also nothing left for it to assert that the Go integration lane does not already prove against a real server: the behaviour under test is entirely server-side, and `TestEvents_JobIDSpellingIsCanonicalisedNotRejected` drives the real handler against real Postgres. If a human wants an end-to-end demonstration, run `follow_job(job.id.upper())` by hand against a live server; do not file it as a gate.

- [ ] **Step 7: Commit**

```bash
git add python/src/relay/client.py python/README.md python/tests/unit/test_client.py
git commit -m "test(python): pin follow_job sending the caller's job id spelling verbatim"
```

---

## Task 7: Mutation battery and full gates

**Files:** none permanently. Every mutation in this task is applied and reverted.

- [ ] **Step 1: The CONTROL first - a mutation that MUST die**

Four mutations in a row have silently failed to apply on this repo under CRLF and reported "survived", so run one that must die before believing any result.

Edit `internal/events/broker.go`, in `Publish`'s status branch: change `if f.JobID == "" || f.JobID == e.JobID {` to `if f.JobID == "" || f.JobID != e.JobID {`.

```bash
go test ./internal/events/... -count=1 -v
```

Expected: **FAIL**. If it passes, the mutation did not apply - stop, check `git diff --stat internal/events/broker.go` and `git ls-files --eol internal/events/broker.go`, and do not draw a conclusion from any other row in this table.

Revert: `git checkout -- internal/events/broker.go`, then confirm `git diff --stat` is empty.

- [ ] **Step 2: Run the battery**

Apply each mutation, run the named command, revert with `git checkout --`, and after every revert check `git diff --stat` is empty and `git ls-files --eol <path>` reads `i/lf`.

| # | Mutation | Command | Expected |
|---|---|---|---|
| M1 | In `handleEvents`, revert the call site to `jobID := r.URL.Query().Get("job_id")` (leave `canonicalJobIDFilter` defined and unused - add `_ = canonicalJobIDFilter` if the compiler objects) | `go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_JobID' -timeout 300s` and `go test ./internal/api/... -run TestCanonicalJobIDFilter -count=1` | integration **FAILS** (headline, 3 subtests); grammar test **PASSES**. That asymmetry is the point: the grammar test alone cannot see a missing call. |
| M2 | `func canonicalJobIDFilter(raw string) string { u, _ := parseUUID(raw); return uuidStr(u) }` | both commands above | `TestEvents_JobIDRejectedSpellingsAreNotCanonicalised` **FAILS** on all 4 rejected spellings; `TestCanonicalJobIDFilter` **FAILS** on 9 of 10 `passthrough/` rows (`passthrough/empty` survives); headline **PASSES**. |
| M3 | In `handleEvents`, replace the job_id read with: `rawJobID := r.URL.Query().Get("job_id")`, then `if rawJobID != "" { if _, err := parseUUID(rawJobID); err != nil { writeError(w, http.StatusBadRequest, "invalid job id"); return } }`, then `jobID := canonicalJobIDFilter(rawJobID)`. **Written this way deliberately:** the plan first drafted this as `parseUUID(raw)`, and `raw` is scoped inside the `task_id` branch, so the mutation would have failed to COMPILE rather than running. A mutation that does not build is not a behavioural kill | `go test -tags integration -p 1 ./internal/api/... -run 'TestEvents_' -timeout 300s` | `TestEvents_TaskIDValidation` **FAILS** on `assert.NotEqual(t, http.StatusBadRequest, ...)`, and `TestEvents_JobIDRejectedSpellingsAreNotCanonicalised` **FAILS** on the `flushed()>=1` barrier. Two independent kills of the "improve it by rejecting" temptation. |
| M4 | Replace `return uuidStr(u)` with a hand-written `fmt.Sprintf("%08x-%04x-%04x-%04x-%011x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])` | both commands from M1 | `TestCanonicalJobIDFilter` **FAILS** on all 8 `accepted/` rows; headline **FAILS** on all 3. This is the mutation the spec's own design could not kill (R2). |
| M5 | Python: insert the `uuid.UUID` canonicaliser from Task 6 Step 3 into `_stream_events` | `cd python && python -m pytest tests/unit/test_client.py -k follow_job -v` | `test_follow_job_sends_the_callers_job_id_spelling_verbatim` **FAILS**; the other two **PASS**. |

Record every result. A row that survives is a finding, not a formality - report it rather than adjusting the expectation.

- [ ] **Step 3: The full gates**

```bash
make test
```
Expected: **ok** across all packages, including the new `internal/api` default-lane test.

```bash
make test-integration
```
Expected: green. Requires Docker Desktop and `p4` on PATH.

```bash
make test-cli-integration
```
Implicated as a **regression check only**: `internal/cli/logs_integration_test.go`'s `runLaneLogs` drives `doLogs` -> a live `internal/api` server -> `handleEvents` with a real `?job_id=` over a real socket, and it is the only lane in the repo that does. It uses the canonical spelling, for which `canonicalJobIDFilter` is the identity, so it must stay green with a zero-line diff to `internal/cli`'s test files. If Docker is unavailable, `RELAY_TEST_DATABASE_URL` gives one fresh database per test instead.

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```
The reliable `-race` route on this machine, per CLAUDE.md; `make test-race` natively is unreliable here for two distinct reasons documented there. **If the lane is genuinely unavailable, say so plainly rather than substituting `-count=N` repetition, which measures flakiness and not race-freedom.**

```bash
cd python && python -m pytest tests/unit -q && ruff check src tests && mypy src
```

**No web gate.** `web/` is untouched and sends only `?task_id=`. Do not run `make test-e2e`, do not rebuild `web/dist`; if `web/dist` dirties for any reason, `git checkout -- web/dist/`.

**No `make generate`.** No `.sql` and no `.proto` file is touched by this plan. If a `*.sql.go` or `models.go` appears in `git status`, something went badly wrong - revert it.

- [ ] **Step 4: Final tree check before the PR**

```bash
git status --short
git diff --stat origin/main
git ls-files --eol internal/api/events.go internal/api/events_test.go internal/api/events_task_log_integration_test.go internal/cli/logs.go internal/cli/logs_test.go README.md python/src/relay/client.py python/README.md python/tests/unit/test_client.py
```

Expected: exactly the nine files from the file-structure table, no stray artifacts, every path `i/lf`. The total diff should be roughly 300 insertions and 40 deletions - the great majority of it test code and comments, with four lines of production Go.

---

## Verification summary

| Requirement (item or spec) | Where it is satisfied |
|---|---|
| A non-canonical but server-acceptable job id yields the same frames as its canonical spelling | Task 1, `TestEvents_JobIDSpellingIsCanonicalisedNotRejected`, three spellings including the underscore row no SDK-side fix could reach |
| The acceptance surface is pinned in BOTH directions | Task 3 (four rejected spellings, scope asserted) and Task 4 (8 accepted, 10 passed through) |
| A test covers uppercase, dashless, and at least one spelling `uuid.UUID` takes and the server does not | Task 1 (uppercase, dashless), Task 3 (braced, `urn:uuid:`, trailing hyphen, sign-prefixed - all four accepted by `uuid.UUID`) |
| The `err != nil` guard is present and pinned; M2 has a non-empty RED set when actually run | Task 2 Step 2 (the guard and its doc comment), Task 3 Step 3 (RED proved by mutation), Task 7 M2 |
| `TestEvents_TaskIDValidation`'s `?job_id=not-a-uuid` assertion is still green, unedited | Task 2 Step 4, and R5 for the reason it must be |
| All prose sites corrected; `canonicalJobID` NOT deleted | Task 5 (six sites, by symbol), Task 2 Step 1 (the seventh, shipped with the code); `canonicalJobID`'s body untouched, per G3 and R1 |
| The publisher census is re-derived, not inherited | R4, with an eight-row table and a correction to the spec's package enumeration |
| The three `[unexecuted]` Python rows are confirmed by running them | Already discharged by the conductor; recorded in R9. No task re-runs them, and they do not enter any Go comment (R9, spec open question 3) |
| Gates: `make test`, `make test-integration`, `make test-race` | Task 7 Step 3, plus the CLI integration lane as a regression check and the Python unit lane |

---

## What this plan does NOT do

- **It does not validate or reject `?job_id=`.** Canonicalise only. M3 in Task 7 exists to keep it that way, and `TestEvents_TaskIDValidation` is the standing guard.
- **It does not make a well-formed id for a job that does not exist distinguishable from a job that is simply quiet.** That is unchanged, deliberate, documented, and owned by proposal 1 below.
- **It does not add a heartbeat, a read timeout, or any `/v1/events` framing change.**
- **It does not change any Python production code**, and therefore does not bump `_version.py` or `pyproject.toml`.
- **It does not delete `internal/cli/logs.go`'s `canonicalJobID`** or change its body by one byte. It has a second, purely client-side reader (`jobSnapshotUnusable`) that no server change can reach.
- **It does not change `internal/events/`.** The broker stays UUID-ignorant; teaching it `pgtype` would give the events package a `pgx` dependency and put id policy in the per-publish fan-out path.
- **It does not change `?task_id=` handling**, which already validates, 404s, and canonicalises.
- **It does not rework `fakeUUIDSpellingServer`.** Its behaviour and every assertion stay byte-identical; only its label changes.
- **It does not touch `web/`, `web/dist`, `internal/mcp/`, `internal/relayclient/`, any migration, any `.sql`, or any `.proto`.**
- **It does not bound the length of `?job_id=`.** A caller-supplied string of any size still becomes a broker map key for the connection's lifetime. Pre-existing, narrowed slightly by this change for strings that parse, and owned by proposal 2 below.
- **It does not unify the six hand-written copies of the `uuidStr` format string.** Proposal 3, and an already-filed item.
- **It does not add a `python/tests/integration/` test.** That lane is in no workflow; Task 6 Step 6 states why and what to do by hand instead.

---

## Phase 6 proposals (the conductor files these; the planner does not run `/backlog`)

Carried from spec section 13, verified against the tree, with one already filed.

1. **`/v1/events` has no heartbeat, so a healthy idle stream and a wedged one are indistinguishable at every layer.** This slice removes the most common cause of a permanently silent stream and none of the others: a well-formed id naming no job, a quiet job, and a proxy that dropped the connection all present identically to a client with `read=None`. A periodic SSE comment frame would let every client tell alive-but-silent from dead and would stop intermediaries idling connections out. Affects the Go CLI, the Python SDK and the SPA's `task_id` streams equally. *(feature, medium.)* **Filing this is what makes G2's "out of scope" a decision rather than an omission** - the project's rule is that a decision conditioned on future work needs a findable item, and both `internal/cli/logs.go`'s corrected comment and `python/README.md`'s new paragraph now point at the gap in prose.
2. **`handleEvents` admits an unbounded caller-supplied string as a broker map key for the connection's lifetime.** Pre-existing and untouched; narrowed only for strings that happen to parse. Bounded in practice by connection count and `internal/api/ratelimit.go`, not by anything in the handler. The likely remedy is a length cap before `Subscribe`, decided against the same asymmetry as G2 - cap without rejecting. *(bug, low.)*
3. **Already filed, and this slice raises its stakes:** `docs/backlog/idea-2026-08-26-six-copies-of-the-uuid-render-format.md`. This plan adds no copy, but the CLI and the server must now agree on the canonical rendering for `jobSnapshotUnusable` **and** for the subscription to match, and `uuidStr` is unexported so no test relates them. Add a line to that item noting `canonicalJobIDFilter` as a new consumer of the agreement rather than filing a duplicate.
