# Agent-Ingest Log Budget Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the wire-keyed `taskLogErrLimiter` with a per-connection log budget that no wire value can enlarge, and route every caller-driven `log.Printf` on the gRPC recv goroutine through it, so one agent token can no longer drive one log line per message through the process-global `log` mutex.

**Architecture:** One new unexported type, `ingestLogLimiter`, in a new file `internal/worker/ingest_log_limiter.go`. It stacks two things whose separation is the whole point: a wire-keyed **dedupe map** (`seen`), which is a diagnostics-quality concern and is deliberately *not* the bound, and a **token bucket** (`tokens`/`last`) keyed on nothing, which *is* the bound. `Connect` allocates exactly one per connection as a stack local and passes it to `handleTaskStatus`, `handleTaskLog` and a new thin `handleInventoryUpdate`. Because it never leaves that goroutine it needs no mutex and has no teardown. The package-level `taskLogErrs`/`taskLogErrLimiter`/`taskLogErrLimiterMax` and the `ResetTaskLogErrLimiterForTest` hook are deleted.

**Tech Stack:** Go 1.26, pgx/v5, gRPC, testcontainers-go (integration tests), testify. **No SQL, no migration, no `.proto`, no generated file, no `make generate`.**

**Slice independence:** This is **backend-only, Go-only**. Every file touched is Go or Markdown. **Zero files under `web/`, zero `.sql`, zero `.sql.go`, zero `internal/proto/`.** The conductor should dispatch `relay-backend-engineer` only and must **NOT** allocate a frontend slice for Phase 3. There is no FE/BE ordering question because there is no FE. The tasks below are **strictly sequential** - each depends on the previous one's tree state. Do not parallelize them.

---

## Spec

Approved spec: `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md`

Backlog item being closed: `docs/backlog/bug-2026-08-12-tasklog-err-limiter-attacker-keyed.md`

**Out of scope, do not touch.** If you find yourself editing any of these, stop and report:

- `docs/backlog/idea-2026-08-14-tasklog-fence-rejection-is-unobservable.md` - the counter on the `pgx.ErrNoRows` arm. This slice leaves that arm shaped for it (Task 5 Step 5) and adds **nothing** to it.
- `docs/backlog/bug-2026-08-12-tasklog-epoch-int32-truncation.md`. You will be editing the exact statement that contains `AssignmentEpoch: int32(chunk.Epoch)` in `internal/worker/handler.go`. **Leave that line exactly as it is.** The limiter key uses the full `int64` (free, one fewer truncation site); the fence argument stays narrowed. Flag it in your report, do not fix it.
- The three post-gate log sites in `handleTaskStatus` (`IncrementTaskRetryCount`, `UpdateTaskStatus`, `FailDependentTasks`) and the three registration-time sites (`:158`, `:344`, `:375`). Verified not caller-forceable (see finding 6). Unchanged.
- Any bound on message *rate* or on `GetTask` round trips. That is the recv-loop limiter, a separate future item.

---

## Verification findings: where the spec is wrong or incomplete

The planner re-derived every line number, symbol and behavioral claim in the spec against the tree at `origin/main` + the spec commit. **The spec is substantially accurate**: every line offset it cites is correct (`handler.go:705-752`, `:831`, `:774`, `:464-475`, `:542`, `:555`, `:604`, `:649`, `:656`, `:177-180`, `:994-1009`, `export_test.go:39-43`), the limiter code it quotes is byte-accurate, `QueryExecMode` really has zero hits outside the spec itself, section 2.3's epoch-varying flood is real, and half B sits exactly where it says. These are the deltas the implementer must carry. **Most important first.**

1. **The spec's mutation matrix is wrong in two rows, and one of them is the row it calls out as headline.** Both were re-derived by hand against section 6.3's own `allow` ordering:
   - *"Move `epoch` from the key back to the map value" must redden test 2.* **It does not.** With the token bucket present, a `map[string]int64` (id -> epoch) shape still treats each of 64 varying epochs on one id as "not yet reported", so it still requests 64 tokens and still gets exactly `ingestLogBurst`. Test 2 stays GREEN. That mutation is behaviour-preserving once the bucket exists. The corollary matters for the design's own story: **the composite key is structurally required because the map now holds four kinds, not because it closes the flood. The bucket closes the flood.** Say that in the comment; do not claim the key shape is a security control.
   - *"Delete the token check in `allow`" must redden 1, 2, 4 and 6.* It reddens **1 and 2 only**. Tests 4 (malformed ids) and 6 (inventory) key on `{kind}` with no wire value, so the dedupe map alone collapses them to one line and they stay GREEN with no bucket at all. The corrected battery is Appendix A.
   This is the exact failure shape the brief warned about: a predicted RED that never appears. Appendix A replaces the spec's matrix wholesale.

2. **The spec's third line of evidence for its crux is not evidence.** Section 2.2 offers "Positive evidence from the adjacent slice": `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` passed unedited when the fence gained a third predicate. **That test cannot discriminate the ordering.** Read its fixture: both NUL-bearing legs run against a task that is *genuinely assigned to this worker at this epoch* (lines 281-292 and 329-342, with a comment at 317-328 explaining that the fixture is deliberately kept matching). A chunk whose fence matches produces the same non-`ErrNoRows` error whether or not the NUL is rejected before the fence. The claim rests on the mechanism argument plus the item's recorded empirical run - which is enough, but the plan must not build on an unverified premise silently. **Task 1 therefore adds a premise test that does discriminate it** (a NUL chunk for a task id that names nothing, asserting a line IS emitted), and every flood test carries a lower bound so it cannot pass vacuously at zero.

3. **A stale-prose defect this change creates and the spec never mentions.** `internal/worker/handler.go:504-508` currently reads, inside `handleTaskStatus`'s identity-gate comment:

   > *"Nor did this function ever have a "zero attacker-keyed log lines" property to protect: the bad-task-id and GetTask branches at the top of this function both log unconditionally, keyed on upd.TaskId, AHEAD of this gate. That is bug-2026-08-12-tasklog-err-limiter-attacker-keyed's shape, it is still live on this path, and this gate does not address it."*

   Every clause of that becomes false when this slice lands. This project has recorded "wrong prose about correct code is the dominant defect" for eight iterations running. Task 5 Step 6 rewrites it. **This is required scope, not cleanup.**

4. **The inventory line is caller-forceable WITHOUT a NUL byte, which the spec misses.** `worker_workspaces.last_used_at` is `TIMESTAMPTZ NOT NULL` (`internal/store/migrations/000007_workspaces.up.sql:11`), and `applyInventoryUpdate` binds `pgtype.Timestamptz{Time: ts, Valid: !ts.IsZero()}` where `ts, _ := time.Parse(time.RFC3339, u.LastUsedAt)` swallows the parse error (`handler.go:1000-1007`). So `LastUsedAt: ""` - the proto zero value - binds SQL NULL into a NOT NULL column and produces a `23502` error at execute time, one per message, with no NUL trick at all. That makes the inventory line the *cheapest* of the four flood vectors, not an afterthought. It is **not** a live production bug: both real agent call sites (`internal/agent/agent.go:381`, `internal/agent/runner.go:430`) always `Format` a `time.Time`, so a genuine agent never sends a blank. The plan still uses a NUL in `SourceKey` as the test fixture for consistency with the other three, and names the blank-timestamp vector in the comment and as the fallback if the NUL route ever stops erroring.

5. **The `%q` on a malformed task id is itself unbounded, on both handlers.** `upd.TaskId` / `chunk.TaskId` are proto `string`s capped only by gRPC's 4 MiB default receive limit, and `taskID.Scan` has *failed* on this path, so no length constraint applies. `%q` escapes but does not truncate, so one budgeted line can be 16 MB. The budget alone leaves `ingestLogBurst * 16 MB` per connection burst. The spec preserves `%q` (correctly) but never bounds the length. **Task 2 adds a `clipID` helper** used at both bad-id sites. Flagged as a planner-added decision (D10) so the conductor can drop it if it considers it scope creep; the planner's call is that "bound agent-driven log volume" is the slice's own title and a 16 MB line is volume.

6. **The spec's claim that the three post-gate sites are not caller-forceable is CONFIRMED**, re-derived independently: `IncrementTaskRetryCount` takes `ID` (a parsed UUID), `AssignmentEpoch` (an integer) and `WorkerID` (server-resolved) - no caller string reaches a text parameter; `UpdateTaskStatus` adds `statusStr` from a closed switch (`handler.go:561-578`) and two server-clock timestamps; `FailDependentTasks` takes only a task id. All three are additionally reachable only past both gates, i.e. only by the genuine assignee at the current epoch, and all three are already wrapped in `!errors.Is(err, pgx.ErrNoRows)`. Leave all three alone.

7. **The `applyInventoryUpdate` reachability claim is CONFIRMED for all four strings.** `source_type`, `source_key`, `short_id`, `baseline_hash` are all `TEXT NOT NULL` (migration 000007 lines 7-10) and all four are bound by `UpsertWorkerWorkspace`; the `Deleted` branch binds `source_type` and `source_key` into `DeleteWorkerWorkspace`. A NUL in any of them fails.

8. **The call-site count is 41, not 43.** `rg 'HandleTaskLog\(|HandleTaskStatus\('` over `internal/worker` returns 43 occurrences, but 2 of those are the declarations in `export_test.go`. The real inventory is 41 call sites across 4 files: `handler_test.go` (3), `handler_taskstatus_integration_test.go` (16), `handler_tasklog_integration_test.go` (20), `handler_tasklog_e2e_integration_test.go` (2). The number matters only because it is the input to the seam trade-off in finding 9.

9. **The spec's test seam (D6) is rejected. See "The test seam" below for the full argument and the replacement.** Summary: the spec's throwaway-limiter-per-call wrapper is fail-open by construction, it makes `HandleTaskLog`/`HandleTaskStatus` exercise no limiting at all, and - decisively - it makes it **impossible to record a behavioural RED for the headline tests**, because those tests would have to be written against a `WithLimiter` wrapper that does not exist at HEAD. This plan replaces it with a **one-limiter-per-`*Handler`** shim, which keeps all 41 call sites byte-identical, keeps `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` byte-identical *including its call sites* (only the now-deleted reset hook line goes), and lets every headline test be written once, RED at HEAD, GREEN after, with no edit.

10. **The spec's test 7 (`PerConnectionBudgetsDoNotInterfere`, two `LimiterHandle`s) is vacuous with respect to production.** Two separately constructed structs being independent is true of *any* struct; it proves nothing about where `Connect` allocates. The property that actually needs pinning is "the allocation site is per connection, not per `Handler` and not package-level", and only a test that drives two real `Connect` streams against **one** `Handler` can pin it. Task 4 Test T8 does that. It is also RED at HEAD.

11. **Section 8's "all handler-layer, `//go:build integration`" is wrong for four of its own tests.** Spec tests 8, 9 and 10 (dedupe-before-spend, refill, refill-does-not-stall) touch no database and no handler. Putting them behind the integration tag means they need Docker, spin a Postgres container they never use, and cannot read `seen`/`tokens` to make strong assertions. They belong in an **untagged, in-package** unit file (`package worker`, precedent: `internal/worker/tasklog_payload_test.go`), where they run under `make test` in milliseconds. Task 2 does that.

12. **The exact-count assertions in the integration flood tests must be ranges, not equalities.** `ingestLogRefill` is 10s of *wall clock*, and 64 testcontainer round trips on a loaded machine can exceed that, granting extra tokens and making an `Equal(16)` assertion flaky. The exact arithmetic is pinned deterministically in the unit tests with an injected clock; the integration tests assert `>= 1 && <= 20`. The `>= 1` half is **not decoration** - it is the anti-vacuity guard against finding 2. Tests whose key is `{kind}` only (T4, T5, T6, T7, T8) are refill-insensitive because dedupe collapses them after the first line, so those keep exact equalities.

13. **A `nil` limiter must not panic.** `cmd/relay-server/main.go:186-191` constructs `grpc.NewServer` with no recovery interceptor, and grpc-go does not recover handler panics by default, so a nil dereference on the recv goroutine takes down the whole server process. `allow` therefore starts with `if l == nil { return false }` - fail *closed* on volume, which loses a diagnostic rather than the process. The spec does not consider this.

14. **Two smaller corrections.** (a) The spec's `int(l.now().Sub(l.last) / ingestLogRefill)` converts an `int64` to `int`; harmless on 64-bit but the arithmetic is written in `int64` here and clamped before narrowing. (b) `time.Now()` carries a monotonic reading and `Sub` uses it, so a wall-clock adjustment cannot move the bucket - worth one comment line, because "a recovery bound must be time-based" is a recorded lesson and someone will ask.

---

## The test seam: why the spec's D6 is rejected, and what replaces it

The spec accepts a documented fail-open seam: keep `HandleTaskLog`/`HandleTaskStatus` at their current signatures by allocating a **throwaway limiter per call**, plus a warning comment saying "these wrappers exercise NO limiting". Judged and **rejected**, for three reasons in increasing order of force:

1. **A warning comment is not a control.** This project has recorded "a test can be green because of the bug" twice in the last three iterations, and separately that a guard which depends on a reader honouring prose in another file is not a guard. A future test that asserts a log-line count through `HandleTaskLog` would be measuring a limiter with a fresh full bucket on every call - green, meaningless, and indistinguishable from a real pass.
2. **It is strictly worse for the existing pinned test.** Under the throwaway, `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` would go to 8 lines then 16, so the spec has to rewrite its call sites to `HandleTaskLogWithLimiter` and thread a handle. That is roughly ten edited lines inside the one test the spec itself declares must stay byte-identical. Every edit inside a byte-identical gate erodes the gate.
3. **Decisive: it destroys the behavioural RED.** The headline tests must be RED at HEAD and GREEN after **with no edit to the test**. Under the throwaway they can only be written against `HandleTaskLogWithLimiter`, which does not exist at HEAD, so the "RED" is a compile error and the test that goes green is not the test that was red. That is precisely the anti-pattern the brief and the project's own lessons forbid.

**Replacement: one limiter per `*Handler`, allocated lazily by the shim.** `export_test.go` keeps a package-level `map[*Handler]*ingestLogLimiter` behind a mutex; `HandleTaskLog`/`HandleTaskStatus` look up (or create) the caller's `Handler`'s limiter and pass it down. Consequences:

- All 41 call sites compile and behave unchanged. Zero mechanical diff.
- `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` still asserts `1` then `2`, **with its calls unchanged**. The only edit anywhere in it is deleting the `worker.ResetTaskLogErrLimiterForTest()` line, because that symbol no longer exists.
- The shim exercises a **real** limiter, so a log-count assertion through it is meaningful. `h` per test == one connection per test is the natural reading and is what every new flood test relies on.
- Every headline test is written once, against the plain shim, RED at HEAD and GREEN after.

**Honest cost, which must go in the shim's own comment.** "One `Handler` == one connection" is **false in production** - a single `Handler` serves every connection. So the shim's mapping is a testing convenience, and it means *the shim can never be the evidence for per-connection isolation*. That property is pinned instead by T8, which drives two real `Connect` streams against one `Handler` and asserts two lines rather than one. The comment must say this, and must name T8.

`HandleTaskLogWithLimiter` / `HandleTaskStatusWithLimiter` plus an opaque `*LimiterHandle` are still added (the `SenderHandle` pattern at `export_test.go:88-98`), for tests that need two independent budgets against one `Handler` without going through `Connect`. They are additive; nothing existing uses them.

---

## Critical files

Read these before starting. They are the entire blast radius.

- `internal/worker/handler.go:128-185` - `Connect`. The limiter is allocated after the `workerUUID` scan (`:156-160`) and before the `for` loop (`:163`). The three call sites are in the switch at `:172-183`; the inventory case at `:177-180` is the third defect instance.
- `internal/worker/handler.go:458-475` - `handleTaskStatus`'s signature and its two pre-gate log lines (`:467`, `:473`). Both unconditional, both keyed on nothing, both ahead of the identity gate at `:542` and the currency gate at `:555`.
- `internal/worker/handler.go:504-508` - the stale paragraph inside the identity-gate comment. Finding 3. **Must be rewritten.**
- `internal/worker/handler.go:705-752` - the whole limiter block being deleted: `taskLogErrLimiterMax` (`:710`), `taskLogErrs` (`:722`), `type taskLogErrLimiter` (`:724-727`), `shouldLog` (`:732-746`), `reset` (`:748-752`).
- `internal/worker/handler.go:754-864` - `handleTaskLog`. Doc comment `:754-771`; signature `:772`; the silent parse guard `:773-776`; the `AppendTaskLogParams` literal `:792-799` (**do not touch `int32(chunk.Epoch)` at `:796`**); the error block `:800-835`, whose compound condition at `:831` is split into two arms; the publish `:837-863`, which must stay strictly after.
- `internal/worker/handler.go:993-1009` - `applyInventoryUpdate`. Read only; the new `handleInventoryUpdate` wraps it and adds no logic.
- `internal/worker/export_test.go:16-29` - the two shims being rewritten; `:39-43` - `ResetTaskLogErrLimiterForTest`, deleted; `:88-98` - the `SenderHandle` opaque-handle pattern the new `LimiterHandle` copies.
- `internal/worker/handler_tasklog_integration_test.go:33-62` - `seedClaimedTask`; `:245-255` - `captureLog`; `:257-265` - `countLines`. All three are package-level in `worker_test`, so the new test file can use them directly. `:274-370` - the pinned `PersistFailure` test.
- `internal/worker/handler_test.go:45-83` - `fakeStream`, the slice-driven mock that returns `io.EOF` when its messages are exhausted. This is what T7/T8 drive `Connect` with; `newMockConnectStream` (`handler_auth_test.go:28-103`) is the interactive one and is **not** what you want here.
- `internal/worker/handler_test.go:32-43` - `seedWorkerWithAgentToken`, used by T7/T8 to register without auto-enroll.
- `internal/worker/handler_test.go:85-112` - `newTestStore`. **One Postgres container per call.** Call it once per test.
- `internal/worker/tasklog_payload_test.go:1` - precedent for an untagged `package worker` test file.
- `internal/store/migrations/000007_workspaces.up.sql:5-13` - the `worker_workspaces` column types behind finding 4 and finding 7.

---

## Conventions and gotchas (read once, apply everywhere)

1. **No SQL and no generated code in this slice.** There is no `.sql` edit, so there is **no `make generate`, no `sqlc generate`, and no CRLF revert dance**. If a step ever seems to need one, that step is wrong - stop and report. `internal/store/models.go` and every `*.sql.go` must be untouched at the end.

2. **Almost everything is integration-tagged.** The exception is the new `internal/worker/ingest_log_limiter_test.go`, which is untagged `package worker` and *does* run under `make test`. `make test` compiles and runs **none** of the tagged files, so it is a no-regression gate, never evidence for this slice. Docker Desktop must be running for the tagged runs; `-p 1` is mandatory.

3. **`go vet -tags integration ./...` (= `make vet-integration`) is the compile gate for tagged code.** Run it after every signature change. `go build ./...` alone does not compile `//go:build integration` files, and this slice changes three handler signatures plus `export_test.go`.

4. **Plan-supplied test bodies are SKETCHES, not verified code.** Every Go snippet below was written by a planner reading the tree, not by running it. Compile it, run it, and for every assertion ask "which mutation in Appendix A makes this fail?" before accepting it. If an assertion cannot be reddened by anything in the battery, it is decoration - say so rather than keeping it for shape. **Never treat "it matches the plan" as verification.** This plan's own verification pass found two wrong RED predictions in the spec it was given (finding 1); assume there is another one in here.

5. **Import blocks in the plan are minimal-at-that-step, not final.** Go rejects an unused import, so each task's snippet lists only what that task's code uses. Task 4 Step 1 explicitly widens the new test file's import block. If a step leaves you with an "imported and not used" error, add or drop the import - that is expected churn, not a plan defect.

6. **HARD GATE - `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` may lose exactly one line and nothing else.** The permitted edit is deleting `worker.ResetTaskLogErrLimiterForTest()` at `handler_tasklog_integration_test.go:279`. **Every assertion, every call site, every comment stays byte-identical**, including `assert.Equal(t, 1, ...)`, `assert.Equal(t, 2, ...)` twice, and `assert.NotContains(t, logged(), secret)`. If any assertion needs adjusting to go green, **STOP and report it as a finding** - that means the design changed the diagnostic contract and the key granularity is wrong. Do not adjust it.

7. **Never log `chunk.Content`, and never log `pgErr.Detail`.** The comment block at `handler.go:826-830` explaining why `%v` on a `pgconn.PgError` is safe (it renders severity + message + SQLSTATE, never `Detail`) moves **verbatim** into the new non-`ErrNoRows` arm. Do not paraphrase it and do not weaken it.

8. **The `pgx.ErrNoRows` drop stays silent and stays BEFORE the publish** (CLAUDE.md epoch fence). Splitting the compound condition at `:831` into two arms must not move anything relative to `h.broker.Publish`.

9. **Nothing may be added to the recv path that blocks or queries.** `handleTaskLog` still performs exactly one DB round trip; `handleTaskStatus` gains none; no goroutine, queue, channel, lock or allocation-per-message is introduced. The limiter's hot path is one map lookup, one subtraction and one integer compare.

10. **Commit cadence.** Commit at each task boundary except Task 4, which deliberately ends RED and uncommitted.

11. **Commit messages via bash heredoc** (the Bash tool runs Git Bash), never a PowerShell here-string.

12. **No em dashes or en dashes** anywhere - code comments, test messages, commit messages, docs. Use regular hyphens.

---

## Why the Task 1 / Task 4 / Task 5 staging must not be collapsed

- **Task 1** proves the premise the entire threat model rests on: a NUL-bearing chunk for a task id that names nothing surfaces a **non-`pgx.ErrNoRows`** error, i.e. the persist error really does precede the fence. It is GREEN at HEAD and GREEN after. If it is RED at HEAD, the spec's section 2.2 is wrong, every flood test in Task 4 would pass vacuously at zero lines, and the whole slice must be re-specified. **Stop and report if it is red.**
- **Task 4** adds eight exposure tests that reference **no new symbol**, so they compile against today's code. Six are RED behaviourally. Record the output verbatim - it is the acceptance evidence and it cannot be reproduced after Task 5.
- **Task 5** is the behaviour change. The same eight tests, **unmodified**, go GREEN.

Do not collapse them. Do not "save time" by wiring the limiter before the REDs are recorded.

---

## Task 1: The premise test - a NUL chunk for an unfenced task is a persist error, not a fence rejection

Everything downstream assumes that a NUL byte in `content` is rejected by Postgres during **Bind** (`pg_verify_mbstr`, SQLSTATE `22021`), before the fence CTE is evaluated, so the error is not `pgx.ErrNoRows` even when the fence would have rejected the chunk anyway. The spec asserts this three ways but its third way (see finding 2) does not discriminate. This test does.

**Files:**
- Create: `internal/worker/handler_ingest_budget_integration_test.go`

- [ ] **Step 1: Create the file with its header and the premise test**

The import block here is minimal on purpose (convention 5). Task 4 Step 1 widens it.

```go
//go:build integration

package worker_test

import (
	"context"
	"testing"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
)

// THE PREMISE OF THIS WHOLE SLICE, AND THE ONLY TEST THAT DISCRIMINATES IT.
//
// Every flood test below asserts an UPPER bound on log lines. An upper bound
// passes vacuously at zero, so each of them also carries a lower bound - and the
// lower bounds are only meaningful if a NUL-bearing chunk for a task the fence
// would reject surfaces a NON-pgx.ErrNoRows error. That is the claim: Postgres
// rejects an embedded NUL while converting the text bind parameter
// (pg_verify_mbstr, SQLSTATE 22021), during Bind, BEFORE the portal executes and
// therefore before the fence CTE is evaluated. The fictitious task id and the
// wrong assignee are both irrelevant.
//
// The existing TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch does
// NOT prove this: both of its NUL legs keep the task genuinely assigned at the
// chunk's epoch on purpose (see its own comment), so the fence matches and the
// ordering is unobservable.
//
// If this test is ever RED, do not fix it - the threat model in
// docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md section 2.2 is
// wrong and every bound below is measuring nothing.
func TestHandleTaskLog_ANulChunkForAnUnfencedTaskIsAPersistErrorNotAFenceRejection(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "premise@example.com", "w-premise")

	// A well-formed UUID that names no row. The fence cannot match it, so if the
	// NUL were NOT rejected first, this would come back as pgx.ErrNoRows and be
	// dropped silently.
	logged := captureLog(t)
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId:  "00000000-0000-0000-0000-0000000000ff",
		Content: []byte("x\x00"),
		Epoch:   1,
	})

	assert.Equal(t, 1, countLines(logged(), "handleTaskLog AppendTaskLog"),
		"a NUL chunk for a task the fence cannot match must still surface a persist error; "+
			"if this is 0 the NUL is being rejected AFTER the fence and this slice's threat model is wrong")

	// Control on the same code path: the SAME nonexistent task id with clean
	// content is a plain fence rejection and stays silent. Without this the
	// assertion above could pass because handleTaskLog logs every failure.
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId:  "00000000-0000-0000-0000-0000000000ff",
		Content: []byte("clean\n"),
		Epoch:   1,
	})
	assert.Equal(t, 1, countLines(logged(), "handleTaskLog AppendTaskLog"),
		"a fence rejection with clean content must stay silent")
}
```

- [ ] **Step 2: Run it and confirm it is GREEN at HEAD**

Run:
```
go test -tags integration -p 1 ./internal/worker/... -run TestHandleTaskLog_ANulChunkForAnUnfencedTaskIsAPersistErrorNotAFenceRejection -v -timeout 300s
```

Expected: **PASS**.

If it FAILS on the first assertion with `expected: 1, actual: 0`, **STOP**. Report that the spec's section 2.2 is wrong, that the NUL is rejected after the fence, and that this slice needs re-specifying. Do not proceed and do not "fix" the test.

If it fails on the second assertion, the fence-rejection path is logging and something else has already regressed - report that too.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/handler_ingest_budget_integration_test.go
git commit -m "test(worker): pin the premise that a NUL chunk fails before the fence

Every bound in the coming log-budget slice is an upper bound on log lines, and
an upper bound passes vacuously at zero. This test is the discriminating proof
that the error class those bounds measure is reachable at all: a NUL-bearing
chunk for a task id that names nothing still surfaces a non-ErrNoRows persist
error, because Postgres rejects the NUL while converting the bind parameter,
before the fence CTE runs.

The existing PersistFailure test cannot prove this - both of its NUL legs keep
the fence matching on purpose."
```

---

## Task 2: The limiter, its constants, `clipID`, and their unit tests

Pure addition. Nothing in production reads any of it yet, so behaviour is provably unchanged. Go permits unused unexported types and functions at package level.

These tests need **no Docker**: they are untagged and in-package, so they run under `make test` in milliseconds and can read `seen` and `tokens` directly.

**Files:**
- Create: `internal/worker/ingest_log_limiter.go`
- Create: `internal/worker/ingest_log_limiter_test.go`

- [ ] **Step 1: Write the failing unit tests**

Create `internal/worker/ingest_log_limiter_test.go`:

```go
package worker

import (
	"strings"
	"testing"
	"time"
)

// The integration tests in package worker_test cannot see these constants, so
// they assert literals. This test is the single place the literals and the
// constants are tied together: if you tune a constant, this test tells you which
// literals to move.
//   - ingestLogBurst 16  -> handler_ingest_budget_integration_test.go uses 20 as
//     its upper bound (burst plus headroom for a slow container).
//   - ingestLogIDClip 64 -> the same file asserts a clipped id.
func TestIngestLogLimiter_ConstantsAreWhatTheHandlerTestsAssume(t *testing.T) {
	if ingestLogSeenMax != 128 {
		t.Errorf("ingestLogSeenMax = %d, want 128", ingestLogSeenMax)
	}
	if ingestLogBurst != 16 {
		t.Errorf("ingestLogBurst = %d, want 16", ingestLogBurst)
	}
	if ingestLogRefill != 10*time.Second {
		t.Errorf("ingestLogRefill = %v, want 10s", ingestLogRefill)
	}
	if ingestLogIDClip != 64 {
		t.Errorf("ingestLogIDClip = %d, want 64", ingestLogIDClip)
	}
}

// newFrozen returns a limiter whose clock never advances unless the test moves
// it, so every count below is exact rather than wall-clock dependent. Both `now`
// and `last` must be reset: newIngestLogLimiter stamps last from time.Now.
func newFrozen() (*ingestLogLimiter, *time.Time) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	clock := &now
	l := newIngestLogLimiter()
	l.now = func() time.Time { return *clock }
	l.last = *clock
	return l, clock
}

func key(n int) logKey { return logKey{kind: kindTaskLogPersist, id: "t", epoch: int64(n)} }

// THE BOUND. Distinct keys are limited by tokens, not by the map.
func TestIngestLogLimiter_DistinctKeysAreCappedAtBurst(t *testing.T) {
	l, _ := newFrozen()
	allowed := 0
	for i := 0; i < 1000; i++ {
		if l.allow(key(i)) {
			allowed++
		}
	}
	if allowed != ingestLogBurst {
		t.Errorf("allowed = %d, want %d - the token bucket is the bound", allowed, ingestLogBurst)
	}
}

// THE DIAGNOSTIC. Distinct task ids at the same epoch are distinct keys: a real
// multi-task persist failure must not collapse to one line. This is the only
// test that pins the `id` component of the persist key; the existing
// PersistFailure integration test pins the `epoch` component.
func TestIngestLogLimiter_TaskIdIsPartOfThePersistKey(t *testing.T) {
	l, _ := newFrozen()
	a := l.allow(logKey{kind: kindTaskLogPersist, id: "task-a", epoch: 1})
	b := l.allow(logKey{kind: kindTaskLogPersist, id: "task-b", epoch: 1})
	again := l.allow(logKey{kind: kindTaskLogPersist, id: "task-a", epoch: 1})
	if !a || !b {
		t.Errorf("two different tasks failing at the same epoch must both log: a=%v b=%v", a, b)
	}
	if again {
		t.Error("the same task+epoch must be deduplicated")
	}
}

// DEDUPE HAPPENS BEFORE THE SPEND. An honest repeating failure costs exactly one
// token no matter how many chunks it produces. Spend-before-dedupe would pass
// every flood test while burning a token per repeated chunk.
func TestIngestLogLimiter_DedupeHappensBeforeTheSpend(t *testing.T) {
	l, _ := newFrozen()
	k := logKey{kind: kindTaskLogPersist, id: "hot", epoch: 1}
	for i := 0; i < 500; i++ {
		l.allow(k)
	}
	if l.tokens != ingestLogBurst-1 {
		t.Fatalf("tokens = %d, want %d - 500 repeats of one key must spend exactly one token",
			l.tokens, ingestLogBurst-1)
	}
	fresh := 0
	for i := 0; i < ingestLogBurst-1; i++ {
		if l.allow(key(i)) {
			fresh++
		}
	}
	if fresh != ingestLogBurst-1 {
		t.Errorf("fresh keys allowed = %d, want %d - the repeats consumed budget they should not have",
			fresh, ingestLogBurst-1)
	}
}

// A KEY SUPPRESSED FOR LACK OF A TOKEN IS NOT RECORDED, so the diagnostic
// reappears when tokens refill instead of being swallowed for the connection's
// whole lifetime.
func TestIngestLogLimiter_ASuppressedKeyIsNotRecorded(t *testing.T) {
	l, clock := newFrozen()
	for i := 0; i < ingestLogBurst; i++ {
		l.allow(key(i))
	}
	victim := logKey{kind: kindTaskLogPersist, id: "victim", epoch: 9}
	if l.allow(victim) {
		t.Fatal("fixture: the bucket must be empty here")
	}
	if _, recorded := l.seen[victim]; recorded {
		t.Fatal("a key suppressed for lack of a token must NOT be recorded in seen")
	}
	*clock = clock.Add(ingestLogRefill)
	if !l.allow(victim) {
		t.Error("after a refill the suppressed key must log, not dedupe-drop")
	}
}

// REFILL ADVANCES BY WHOLE CONSUMED INTERVALS AND CAPS AT BURST.
func TestIngestLogLimiter_RefillsWholeIntervalsAndCapsAtBurst(t *testing.T) {
	l, clock := newFrozen()
	for i := 0; i < ingestLogBurst; i++ {
		l.allow(key(i))
	}
	*clock = clock.Add(3 * ingestLogRefill)
	got := 0
	for i := 100; i < 200; i++ {
		if l.allow(key(i)) {
			got++
		}
	}
	if got != 3 {
		t.Errorf("after 3 refill intervals, allowed = %d, want 3", got)
	}

	*clock = clock.Add(1000 * ingestLogRefill)
	got = 0
	for i := 1000; i < 2000; i++ {
		if l.allow(key(i)) {
			got++
		}
	}
	if got != ingestLogBurst {
		t.Errorf("after 1000 refill intervals, allowed = %d, want %d (capped at burst)", got, ingestLogBurst)
	}
}

// THE REFILL MUST NOT STALL UNDER CALLS MORE FREQUENT THAN THE INTERVAL. This is
// the test that catches `l.last = l.now()` on a call that added zero tokens: a
// connection calling more often than the refill interval would then never refill
// at all. 20000 calls at 1ms apart is 20s, exactly two intervals.
func TestIngestLogLimiter_RefillDoesNotStallUnderCallsMoreFrequentThanTheInterval(t *testing.T) {
	l, clock := newFrozen()
	for i := 0; i < ingestLogBurst; i++ {
		l.allow(key(i))
	}
	got := 0
	for i := 0; i < 20000; i++ {
		*clock = clock.Add(time.Millisecond)
		if l.allow(key(1000 + i)) {
			got++
		}
	}
	if got != 2 {
		t.Errorf("allowed = %d over 20s of 1ms-spaced calls, want 2 (one per refill interval)", got)
	}
}

// THE DEDUPE MAP IS BOUNDED, and clearing it at capacity is safe ONLY because
// the bucket is the bound. Re-arming every key can at worst produce another
// burst; permanent suppression would have no time-based recovery.
func TestIngestLogLimiter_SeenMapIsBoundedAndClearsAtCapacity(t *testing.T) {
	l, clock := newFrozen()
	maxSeen := 0
	for i := 0; i < 4*ingestLogSeenMax; i++ {
		*clock = clock.Add(ingestLogRefill)
		l.allow(key(i))
		if len(l.seen) > maxSeen {
			maxSeen = len(l.seen)
		}
	}
	if maxSeen > ingestLogSeenMax {
		t.Errorf("len(seen) reached %d, want at most %d", maxSeen, ingestLogSeenMax)
	}
	if maxSeen < ingestLogSeenMax/2 {
		t.Errorf("len(seen) only reached %d - the fixture never approached capacity, so this test proves nothing", maxSeen)
	}
}

// A NIL LIMITER DROPS THE LINE INSTEAD OF PANICKING. Connect has no recover and
// grpc-go does not recover handler panics, so a nil dereference on the recv
// goroutine would take down the whole server process. Fail closed on volume.
func TestIngestLogLimiter_NilLimiterDropsTheLineInsteadOfPanicking(t *testing.T) {
	var l *ingestLogLimiter
	if l.allow(logKey{kind: kindBadTaskID}) {
		t.Error("a nil limiter must drop the line, not allow it")
	}
}

func TestClipID_BoundsTheLoggedIdentifier(t *testing.T) {
	short := "00000000-0000-0000-0000-000000000001"
	if got := clipID(short); got != short {
		t.Errorf("clipID(%q) = %q, want it unchanged", short, got)
	}
	long := strings.Repeat("A", 100000)
	got := clipID(long)
	if len(got) > ingestLogIDClip+32 {
		t.Errorf("clipID returned %d bytes, want at most %d", len(got), ingestLogIDClip+32)
	}
	if !strings.HasPrefix(got, strings.Repeat("A", ingestLogIDClip)) {
		t.Error("clipID must keep the leading bytes so the operator sees what was sent")
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail to compile**

Run:
```
go test ./internal/worker/... -run TestIngestLogLimiter -v -timeout 60s
```

Expected: **FAIL to build**, with `undefined: ingestLogSeenMax`, `undefined: newIngestLogLimiter`, `undefined: logKey`, `undefined: clipID` and friends.

- [ ] **Step 3: Write the implementation**

Create `internal/worker/ingest_log_limiter.go`:

```go
package worker

import "time"

// ingestLogLimiter bounds caller-driven log volume for ONE agent connection.
//
// It is two things stacked, and the split is the whole point.
//
//   - `seen` is a DEDUPLICATOR. It collapses a repeating failure to one line,
//     and it is keyed on wire-supplied values on purpose, because a caller that
//     varies the key only makes its own diagnostics noisier.
//   - `tokens` is the BOUND. It is keyed on nothing, so no wire value can
//     enlarge it.
//
// The predecessor (taskLogErrs, removed 2026-08-15) used the dedupe map AS the
// bound, so one wire field defeated both: it stored one entry per task id with
// the epoch as the VALUE, so a caller varying chunk.Epoch on a single task id
// overwrote that one entry forever, never reached the capacity cap, and got one
// log line per message with a map of exactly one entry. Do not merge these two
// back together, and in particular do not delete the bucket on the grounds that
// the map already limits things - the map deliberately does not.
//
// Note what the composite key does and does not buy. It is required because one
// map now holds four kinds of key, NOT because it closes the flood. Moving the
// epoch back into the value would still be bounded, because the bucket is the
// bound. The key shape is a diagnostics decision; the bucket is the security
// control.
//
// Owned by ONE goroutine: allocated in Connect and used only from that
// connection's recv loop. No mutex, by design - adding one would be the second
// lock on a path whose whole complaint is lock contention. If a caller ever
// appears off that goroutine, that caller is the finding, not the missing lock.
// It is a stack local in Connect, so it dies with the frame: there is no
// teardown to get wrong and no way for one connection to reach another's.
type ingestLogLimiter struct {
	seen   map[logKey]struct{}
	tokens int
	last   time.Time
	now    func() time.Time // injectable for the deterministic refill tests only
}

// logKind partitions the budget's dedupe keys. Values are never persisted or
// sent anywhere, so they may be renumbered freely.
type logKind uint8

const (
	kindTaskLogPersist logKind = iota + 1 // handleTaskLog's non-ErrNoRows persist failure
	kindBadTaskID                         // an unparseable task id, SHARED by both handlers
	kindStatusGetTask                     // handleTaskStatus's non-ErrNoRows GetTask failure
	kindInventory                         // handleInventoryUpdate's persist failure
)

// logKey is the dedupe key. Only kindTaskLogPersist populates id and epoch; the
// other three kinds deliberately carry NO wire value, so the caller cannot vary
// them (see the per-site table in the spec's section 6.4).
//
// The only wire string that ever reaches this struct is a task id that has
// already passed pgtype.UUID.Scan, so it is at most 36 characters. The bad-id
// kind, whose string has NOT been validated, stores nothing at all.
type logKey struct {
	kind  logKind
	id    string
	epoch int64
}

// All per connection, all untunable. THERE IS DELIBERATELY NO ENV KNOB: an
// operator raising the budget re-opens the vector this type exists to close, and
// no value here has an operational reason to move. Do not add one.
const (
	// Dedupe capacity. Far above any realistic count of distinct concurrently
	// failing tasks on one agent, so the honest case never clears.
	ingestLogSeenMax = 128

	// Tokens at connection start. Covers several tasks failing at once at
	// connection start without waiting on a refill.
	ingestLogBurst = 16

	// Steady state: 6 lines per minute per connection. A genuine repeating infra
	// failure still reports continuously; a flood becomes 6 lines/min instead of
	// one line per message.
	ingestLogRefill = 10 * time.Second

	// Longest prefix of an UNPARSED, caller-supplied identifier that may reach
	// the log. Both bad-task-id sites run AFTER pgtype.UUID.Scan has failed, so
	// no length constraint applies to the string: it is a proto string bounded
	// only by gRPC's 4 MiB receive limit, and %q escapes without truncating. The
	// budget alone would still permit ingestLogBurst multi-megabyte lines.
	ingestLogIDClip = 64
)

func newIngestLogLimiter() *ingestLogLimiter {
	return &ingestLogLimiter{
		seen:   make(map[logKey]struct{}),
		tokens: ingestLogBurst,
		last:   time.Now(),
		now:    time.Now,
	}
}

// allow reports whether this log line may be emitted, recording the key if so.
//
// The ORDER of the four steps is load-bearing; each one has a mutation in the
// plan's battery that only this ordering survives.
func (l *ingestLogLimiter) allow(k logKey) bool {
	// Fail CLOSED rather than panic. Connect has no recover and grpc-go does not
	// recover handler panics, so a nil dereference here would kill the whole
	// server process. Losing a diagnostic is the cheaper failure. Production has
	// exactly one allocation site (Connect) so this is unreachable there.
	if l == nil {
		return false
	}

	// 1. REFILL. Advance `last` by WHOLE CONSUMED INTERVALS, never to now:
	// setting last = now on a call that added zero tokens means a connection
	// calling more often than the refill interval never refills at all. That is
	// the most likely way to get this wrong.
	//
	// time.Now carries a monotonic reading and Sub uses it, so a wall-clock
	// adjustment cannot move this bucket.
	if elapsed := l.now().Sub(l.last); elapsed >= ingestLogRefill {
		n := int64(elapsed / ingestLogRefill)
		l.last = l.last.Add(time.Duration(n) * ingestLogRefill)
		if got := int64(l.tokens) + n; got >= int64(ingestLogBurst) {
			l.tokens = ingestLogBurst
		} else {
			l.tokens = int(got)
		}
	}

	// 2. DEDUPE, BEFORE the spend. An honest repeating failure - one task
	// streaming binary output - costs exactly ONE token no matter how many chunks
	// it produces.
	if _, ok := l.seen[k]; ok {
		return false
	}

	// 3. SPEND. A key suppressed for lack of a token is deliberately NOT
	// recorded, so the diagnostic reappears when tokens refill rather than being
	// swallowed for the connection's whole lifetime.
	if l.tokens == 0 {
		return false
	}

	// 4. CAPACITY. Clearing re-arms every key, which is exactly the 2026-08-12
	// defect this type replaces - BUT ONLY WHEN THE MAP IS THE BOUND. With the
	// bucket as the bound, re-arming can at worst produce another burst and then
	// 6 lines/min. The alternative, permanent suppression, is also bounded but
	// has NO TIME-BASED RECOVERY: a connection that once tripped 128 distinct
	// failures would lose the diagnostic for its whole lifetime. Deleting the
	// bucket is therefore visibly the thing that re-opens the original bug.
	if len(l.seen) >= ingestLogSeenMax {
		clear(l.seen)
	}

	l.tokens--
	l.seen[k] = struct{}{}
	return true
}

// clipID bounds an UNPARSED caller-supplied identifier before it reaches a log
// line. Callers must still use %q on the result: clipping is a volume control,
// %q is the injection control, and neither substitutes for the other. Slicing
// may split a UTF-8 rune; %q renders the partial bytes as \xNN escapes, which is
// safe.
func clipID(s string) string {
	if len(s) <= ingestLogIDClip {
		return s
	}
	return s[:ingestLogIDClip] + "...(truncated)"
}
```

- [ ] **Step 4: Run the unit tests**

Run:
```
go test ./internal/worker/... -run 'TestIngestLogLimiter|TestClipID' -v -timeout 60s
```
Expected: **all PASS**, 10 tests.

- [ ] **Step 5: Full unit gate and the tagged compile gate**

Run:
```
go build ./...
go vet -tags integration ./...
go test ./... -timeout 120s
```
Expected: all succeed. Nothing in production references the new file yet, so no behaviour changed.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/ingest_log_limiter.go internal/worker/ingest_log_limiter_test.go
git commit -m "feat(worker): add ingestLogLimiter, a per-connection log budget

Two layers, deliberately separate. seen is a DEDUPLICATOR keyed on wire values
on purpose - a caller that varies the key only makes its own diagnostics
noisier. tokens is the BOUND, keyed on nothing, so no wire value can enlarge
it. The predecessor conflated the two, which is why one proto field defeated
both.

Dedupe runs before the spend, so an honest repeating failure costs one token
however many chunks it produces; a key suppressed for lack of a token is not
recorded, so the diagnostic returns on refill instead of being swallowed.

Nothing reads this yet. Unit-tested with an injected clock, no Docker."
```

---

## Task 3: Read the export_test shim design (no edit, no commit)

The shim added in Task 5 Step 8 is the mechanism that lets Task 4's REDs be written once and never edited. Read it now, before writing the tests that depend on it. **This task produces no diff.**

The text below is the specification for Task 5 Step 8. It cannot be applied yet: it calls `h.handleTaskStatus` and `h.handleTaskLog` with an extra argument, and those signatures do not change until Task 5.

```go
// handlerShimLimiters gives each *Handler ONE ingestLogLimiter for the lifetime
// of the test binary, so that HandleTaskLog / HandleTaskStatus behave like a
// single agent connection and the ~41 existing call sites across four test files
// compile and behave unchanged.
//
// READ THIS BEFORE ASSERTING ON LOG-LINE COUNTS. "One Handler equals one
// connection" is TRUE in these shims and FALSE in production, where a single
// Handler serves every connection and Connect allocates one limiter per stream.
// So these shims are usable evidence for what ONE connection's budget does, and
// are NEVER evidence for isolation BETWEEN connections. That property is pinned
// by TestConnect_TwoConnectionsDoNotShareTheLogBudget, which drives two real
// Connect streams against one Handler.
//
// The earlier design for this seam allocated a throwaway limiter per call, which
// would have made these wrappers exercise no limiting at all - a test asserting
// a log-line count through them would have been green and meaningless. Do not
// go back to that.
var (
	handlerShimLimitersMu sync.Mutex
	handlerShimLimiters   = map[*Handler]*ingestLogLimiter{}
)

func shimLimiterFor(h *Handler) *ingestLogLimiter {
	handlerShimLimitersMu.Lock()
	defer handlerShimLimitersMu.Unlock()
	l, ok := handlerShimLimiters[h]
	if !ok {
		l = newIngestLogLimiter()
		handlerShimLimiters[h] = l
	}
	return l
}

// HandleTaskStatus exposes the unexported handleTaskStatus method for
// integration tests in package worker_test. workerID is the sending
// connection's authenticated worker, exactly as Connect supplies it in
// production; the log budget is this Handler's shim limiter (see above).
func (h *Handler) HandleTaskStatus(ctx context.Context, workerID pgtype.UUID, upd *relayv1.TaskStatusUpdate) {
	h.handleTaskStatus(ctx, workerID, shimLimiterFor(h), upd)
}

// HandleTaskLog exposes the unexported handleTaskLog method for integration
// tests in package worker_test. workerID is the sending connection's
// authenticated worker, exactly as Connect supplies it in production; the log
// budget is this Handler's shim limiter (see above).
func (h *Handler) HandleTaskLog(ctx context.Context, workerID pgtype.UUID, chunk *relayv1.TaskLogChunk) {
	h.handleTaskLog(ctx, workerID, shimLimiterFor(h), chunk)
}

// LimiterHandle is an opaque wrapper around an unexported *ingestLogLimiter, in
// the same shape as SenderHandle below, so package worker_test can hold two
// independent budgets against ONE Handler without going through Connect.
type LimiterHandle struct {
	l *ingestLogLimiter
}

// NewLimiterForTest returns a fresh per-connection log budget, in the state
// Connect allocates one in.
func NewLimiterForTest() *LimiterHandle {
	return &LimiterHandle{l: newIngestLogLimiter()}
}

// HandleTaskLogWithLimiter is HandleTaskLog with an explicit budget.
func (h *Handler) HandleTaskLogWithLimiter(ctx context.Context, workerID pgtype.UUID, lim *LimiterHandle, chunk *relayv1.TaskLogChunk) {
	h.handleTaskLog(ctx, workerID, lim.l, chunk)
}

// HandleTaskStatusWithLimiter is HandleTaskStatus with an explicit budget.
func (h *Handler) HandleTaskStatusWithLimiter(ctx context.Context, workerID pgtype.UUID, lim *LimiterHandle, upd *relayv1.TaskStatusUpdate) {
	h.handleTaskStatus(ctx, workerID, lim.l, upd)
}
```

- [ ] **Step 1: Confirm you have understood the two consequences**

Before writing Task 4, be able to answer both:

1. Why does `h := worker.NewHandler(...)` inside a test mean "one connection's budget"? (Because `shimLimiterFor` keys on the `*Handler` pointer, so every `HandleTaskLog`/`HandleTaskStatus` call through that `h` shares one limiter.)
2. Why can a test that uses two `Handler`s NOT prove per-connection isolation? (Because per-connection isolation is a property of where `Connect` allocates, and the shim is a different allocation site. Only `TestConnect_TwoConnectionsDoNotShareTheLogBudget` proves it.)

No file is edited and nothing is committed in this task.

---

## Task 4: The eight exposure tests (no production change, DO NOT COMMIT)

Every test here references **only symbols that exist at HEAD**, so all eight compile now and none of them will be edited in Task 5. Six are RED behaviourally; two are green-at-HEAD and carry mutation-only discrimination, which is stated at each site.

**Files:**
- Modify: `internal/worker/handler_ingest_budget_integration_test.go`

- [ ] **Step 1: Widen the import block**

Replace the import block written in Task 1 Step 1 with:

```go
import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Every one of these is used by the code appended in Steps 2-6. If `go vet -tags integration ./internal/worker/...` reports an unused import after Step 6, something in a snippet was dropped - find it rather than deleting the import.

- [ ] **Step 2: T2 and T3 - the two floods on the log path**

Append:

```go
// Upper bound used by every distinct-key flood test below. ingestLogBurst is 16
// (pinned by TestIngestLogLimiter_ConstantsAreWhatTheHandlerTestsAssume, which
// is where you go if you tune it). The extra headroom absorbs up to four
// ingestLogRefill intervals in case a loaded container makes 64 round trips take
// more than 10 seconds; the EXACT arithmetic is pinned deterministically in the
// unit tests with an injected clock, not here.
//
// The LOWER bound in each test is not decoration. An upper bound alone passes
// vacuously at zero, and zero is exactly what these tests would see if the NUL
// were rejected after the fence instead of before it. See
// TestHandleTaskLog_ANulChunkForAnUnfencedTaskIsAPersistErrorNotAFenceRejection.
const floodLogUpperBound = 20

// A caller emitting a fresh random task id per message defeats a limiter keyed
// on the task id. This is the item's original repro.
func TestHandleTaskLog_DistinctWireTaskIdsCannotFloodTheLog(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "flood-ids@example.com", "w-flood-ids")

	const secret = "SECRET-sentinel"
	logged := captureLog(t)
	for i := 0; i < 64; i++ {
		h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId:  fmt.Sprintf("00000000-0000-0000-0000-%012x", i+1),
			Content: []byte(secret + "\x00"),
			Epoch:   1,
		})
	}

	lines := countLines(logged(), "handleTaskLog AppendTaskLog")
	assert.GreaterOrEqual(t, lines, 1,
		"the diagnostic must not vanish entirely - if this is 0 the error class is unreachable and the bound below measures nothing")
	assert.LessOrEqual(t, lines, floodLogUpperBound,
		"64 chunks with 64 distinct wire task ids must not produce 64 log lines")
	assert.NotContains(t, logged(), secret, "chunk content must never be logged")
}

// THE DISCRIMINATING TEST, and the permanent record of the spec's section 2.3.
//
// The old limiter stored ONE ENTRY PER TASK ID with the epoch as the VALUE
// (`l.reported[taskID] = epoch`). So with a single fixed task id and a varying
// chunk.Epoch: the lookup hits, the stored epoch differs, the early return is
// skipped, len(reported) is 1 so the capacity branch never fires, and shouldLog
// returns true. One log line per message, forever, with a map of exactly one
// entry and reset() never called.
//
// This is the only test in the tree that stays RED against a fix that merely
// changes reset() to suppress instead of clearing - which is what the backlog
// item itself proposed. Do not delete it and do not weaken it to the
// distinct-ids case above.
func TestHandleTaskLog_VaryingWireEpochOnOneTaskCannotFloodTheLog(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, workerID, _ := seedClaimedTask(t, ctx, q, "flood-epoch@example.com", "w-flood-epoch")
	taskIDStr := h.UUIDStringForTest(taskID)

	const secret = "SECRET-sentinel"
	logged := captureLog(t)
	for e := 1; e <= 64; e++ {
		h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId:  taskIDStr, // ONE task id, on purpose
			Content: []byte(secret + "\x00"),
			Epoch:   int64(e), // varying - this is the whole point
		})
	}

	lines := countLines(logged(), "handleTaskLog AppendTaskLog")
	assert.GreaterOrEqual(t, lines, 1,
		"the diagnostic must not vanish entirely")
	assert.LessOrEqual(t, lines, floodLogUpperBound,
		"64 chunks for ONE task id at 64 epochs must not produce 64 log lines; "+
			"a dedupe map keyed on the task id with the epoch as its VALUE never grows and never caps")
	assert.NotContains(t, logged(), secret, "chunk content must never be logged")
}
```

- [ ] **Step 3: T4 - the shared bad-task-id key, and the log path gaining a line**

Append:

```go
// Two legs with two different jobs, stated separately because only one of them
// is RED at HEAD.
//
// LEG A is RED at HEAD (0 lines, want 1): today handleTaskLog's taskID.Scan
// failure returns SILENTLY, so an agent sending unparseable ids on the log path
// loses 100% of that task's output with no signal anywhere. That is the one
// failure mode on this path with total, silent data loss, and it is worth
// exactly one line per connection - which is only safe because of the budget.
//
// LEG B is GREEN at HEAD for the wrong reason (0 lines from the log path plus 1
// from the status path is also 1). It carries no HEAD-RED and is honestly a
// MUTATION-ONLY test: it goes red when the two handlers are given separate key
// kinds, which would let an agent malforming ids on both streams cost two lines
// instead of one. Keep it for that, not for shape.
func TestHandleTaskLog_MalformedTaskIdIsLoggedOncePerConnectionAndSharesTheStatusBudget(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "badid-share@example.com", "w-badid-share")

	// LEG A: fresh Handler, so a fresh budget. Only the log path.
	hA := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})
	loggedA := captureLog(t)
	for i := 0; i < 64; i++ {
		hA.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId: fmt.Sprintf("not-a-uuid-%d", i), Content: []byte("x\n"), Epoch: 1,
		})
	}
	assert.Equal(t, 1, countLines(loggedA(), "handleTaskLog bad task id"),
		"64 unparseable ids on the log path must produce exactly one line per connection")
	assert.Contains(t, loggedA(), `"not-a-uuid-0"`,
		"the line must name the FIRST offending id, %q-quoted")

	// LEG B: a second fresh Handler. One malformed id on each handler, one
	// budget, must cost ONE line in total, not two.
	hB := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})
	loggedB := captureLog(t)
	hB.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: "log-side-garbage", Content: []byte("x\n"), Epoch: 1,
	})
	hB.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
		TaskId: "status-side-garbage", Status: relayv1.TaskStatus_TASK_STATUS_RUNNING, Epoch: 1,
	})
	total := countLines(loggedB(), "handleTaskLog bad task id") +
		countLines(loggedB(), "handleTaskStatus bad task id")
	assert.Equal(t, 1, total,
		"both handlers share ONE bad-task-id budget key, so an agent malforming ids on both streams costs one line, not two")
}
```

> **Note on `captureLog` being called twice in one test:** it installs a fresh buffer and registers a `t.Cleanup` restore each time, so the second call shadows the first for the rest of the test and `loggedA()` keeps returning leg A's buffer. Verify this behaviour when you run the test; if the two accessors interfere, split the legs into two `t.Run` subtests rather than reworking `captureLog`.

- [ ] **Step 4: T5 and T6 - the status path**

Append:

```go
// A status update naming a task that does not exist is indistinguishable from a
// forged one, carries nothing an operator can act on, and is the cheapest
// message an attacker can send. It is dropped SILENTLY - not budgeted - exactly
// as handleTaskLog drops an unresolvable chunk and exactly as both gates further
// down drop a rejected one.
//
// RED at HEAD: 64 lines from the unconditional GetTask branch.
func TestHandleTaskStatus_UnknownWellFormedTaskIdsAreSilent(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "status-unknown@example.com", "w-status-unknown")

	logged := captureLog(t)
	for i := 0; i < 64; i++ {
		h.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
			TaskId: fmt.Sprintf("00000000-0000-0000-0000-%012x", i+1),
			Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
			Epoch:  1,
		})
	}
	assert.Equal(t, 0, countLines(logged(), "handleTaskStatus GetTask"),
		"a status update naming a nonexistent task must be dropped silently, not budgeted")

	// Positive control on the same code path: a real update from the real
	// assignee still lands. Without this, a handleTaskStatus that had stopped
	// working entirely would pass the assertion above.
	h.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  int64(epoch),
	})
	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "running", fresh.Status, "positive control: a genuine status update must still land")
}

// RED at HEAD: 65 lines. Also pins the clip, which the budget alone does not
// give: upd.TaskId has FAILED pgtype.UUID.Scan here, so it is a proto string
// bounded only by gRPC's 4 MiB receive limit and %q escapes without truncating.
func TestHandleTaskStatus_MalformedTaskIdsAreLoggedOncePerConnectionAndClipped(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "status-badid@example.com", "w-status-badid")

	// The FIRST id is the huge one, because only the first is logged.
	huge := strings.Repeat("Z", 100000)
	logged := captureLog(t)
	h.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
		TaskId: huge, Status: relayv1.TaskStatus_TASK_STATUS_RUNNING, Epoch: 1,
	})
	for i := 0; i < 64; i++ {
		h.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
			TaskId: fmt.Sprintf("not-a-uuid-%d", i),
			Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
			Epoch:  1,
		})
	}

	out := logged()
	assert.Equal(t, 1, countLines(out, "handleTaskStatus bad task id"),
		"65 unparseable ids must produce exactly one line per connection")
	assert.Less(t, len(out), 1000,
		"an unparsed caller-supplied id must be clipped before it reaches the log; %q escapes but does not truncate")
	assert.Contains(t, out, strings.Repeat("Z", 32),
		"the clipped line must still show the leading bytes the agent actually sent")
}
```

- [ ] **Step 5: T7 and T8 - the inventory line, through the real Connect loop**

Append:

```go
// inventoryFloodStream returns a fakeStream that registers with a real agent
// token and then sends n WorkspaceInventoryUpdates that cannot persist.
//
// A NUL in source_key fails at bind time (source_key is TEXT NOT NULL,
// migration 000007). If that route ever stops erroring, the equally reachable
// alternative is LastUsedAt: "" - applyInventoryUpdate swallows the time.Parse
// error and binds SQL NULL into last_used_at, which is also NOT NULL. Either
// way it is one error per message with no gate ahead of it.
func inventoryFloodStream(ctx context.Context, hostname, rawToken string, n int) *fakeStream {
	msgs := []*relayv1.AgentMessage{{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: hostname, CpuCores: 1, RamGb: 1, Os: "linux",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
			},
		},
	}}
	for i := 0; i < n; i++ {
		msgs = append(msgs, &relayv1.AgentMessage{
			Payload: &relayv1.AgentMessage_WorkspaceInventory{
				WorkspaceInventory: &relayv1.WorkspaceInventoryUpdate{
					SourceType:   "perforce",
					SourceKey:    fmt.Sprintf("//depot/%d\x00", i),
					ShortId:      "s",
					BaselineHash: "b",
					LastUsedAt:   time.Now().UTC().Format(time.RFC3339),
				},
			},
		})
	}
	return &fakeStream{ctx: ctx, sentCh: make(chan struct{}, 1), msgs: msgs}
}

// The third instance of the defect, one line away from the other two in Connect
// and named by neither half of the backlog item. RED at HEAD: 64 lines.
func TestConnect_InventoryPersistFailuresAreBoundedPerConnection(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, rawToken := seedWorkerWithAgentToken(t, ctx, q, "inv-flood")

	logged := captureLog(t)
	require.NoError(t, h.Connect(inventoryFloodStream(ctx, "inv-flood", rawToken, 64)))

	assert.Equal(t, 1, countLines(logged(), "inventory update failed"),
		"64 unpersistable inventory updates on one connection must produce exactly one log line")
}

// THE ONLY TEST THAT PINS THE ALLOCATION SITE. Two real Connect streams against
// ONE Handler. Two limiters means two lines; one limiter shared on the Handler,
// or a package-level one, means one.
//
// The export_test shims cannot substitute for this: they map one limiter per
// *Handler, which is exactly the wrong thing in production. Sequential rather
// than concurrent, so the count is deterministic and there is no ordering race
// on the captured buffer.
//
// RED at HEAD: 128 lines.
func TestConnect_TwoConnectionsDoNotShareTheLogBudget(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, tokenA := seedWorkerWithAgentToken(t, ctx, q, "inv-budget-a")
	_, tokenB := seedWorkerWithAgentToken(t, ctx, q, "inv-budget-b")

	logged := captureLog(t)
	require.NoError(t, h.Connect(inventoryFloodStream(ctx, "inv-budget-a", tokenA, 64)))
	require.NoError(t, h.Connect(inventoryFloodStream(ctx, "inv-budget-b", tokenB, 64)))

	assert.Equal(t, 2, countLines(logged(), "inventory update failed"),
		"each connection gets its OWN budget: two connections must produce two lines. "+
			"One line means the budget is shared (a Handler field or a package-level var) "+
			"and one agent can suppress another's diagnostics")
}
```

- [ ] **Step 6: T9 - the fence-rejection arm emits nothing at all**

Append:

```go
// The existing PersistFailure test's "a stale-epoch drop must stay silent" leg
// counts one marker string, so it cannot see a DIFFERENTLY WORDED log line added
// to the pgx.ErrNoRows arm. This one asserts the whole captured buffer is empty,
// so any wording reddens it.
//
// GREEN at HEAD and after. Its job is the mutation battery, not a HEAD-RED, and
// it is the permanent guard on the branch the counter item will drop into.
func TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "fence-silent@example.com", "w-fence-silent")
	taskIDStr := h.UUIDStringForTest(taskID)

	require.NoError(t, q.RequeueTask(ctx, taskID))
	fresh, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: workerID})
	require.NoError(t, err)
	require.Equal(t, epoch+2, fresh.AssignmentEpoch, "fixture: requeue and redispatch bump twice")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	logged := captureLog(t)
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("from the dead generation\n"), Epoch: int64(epoch),
	})

	assert.Equal(t, "", logged(),
		"the pgx.ErrNoRows arm must be side-effect-free: NO log line of any wording. "+
			"Observability for it is idea-2026-08-14-tasklog-fence-rejection-is-unobservable, whose answer is a counter")

	select {
	case e := <-ch:
		t.Fatalf("a fence-rejected chunk must not be published: %s", e.Data)
	default:
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Empty(t, rows, "a fence-rejected chunk must not be stored")
}
```

- [ ] **Step 7: Run all nine tests and capture the output verbatim**

Run:
```
go test -tags integration -p 1 ./internal/worker/... -run 'TestHandleTaskLog_(ANulChunk|DistinctWireTaskIds|VaryingWireEpoch|MalformedTaskId|AFenceRejection)|TestHandleTaskStatus_(UnknownWellFormed|MalformedTaskIds)|TestConnect_(InventoryPersistFailures|TwoConnectionsDoNot)' -v -timeout 900s
```

Expected: **6 FAIL, 3 PASS.** Exactly this split:

| Test | HEAD | Predicted assertion and value |
|---|---|---|
| `..._ANulChunkForAnUnfencedTask...` | **PASS** | Task 1 already proved it |
| `..._AFenceRejectionEmitsNoLogLineAtAll` | **PASS** | today's `ErrNoRows` arm is already silent |
| `..._DistinctWireTaskIdsCannotFloodTheLog` | **FAIL** | `assert.LessOrEqual(t, lines, floodLogUpperBound)` - `lines` is **64** |
| `..._VaryingWireEpochOnOneTaskCannotFloodTheLog` | **FAIL** | `assert.LessOrEqual(t, lines, floodLogUpperBound)` - `lines` is **64** |
| `..._MalformedTaskIdIsLoggedOncePerConnection...` | **FAIL** (leg A) | `assert.Equal(t, 1, countLines(loggedA(), "handleTaskLog bad task id"))` - got **0** (today's parse guard returns silently). Leg B is green at HEAD and stays green - see its comment |
| `TestHandleTaskStatus_UnknownWellFormedTaskIdsAreSilent` | **FAIL** | `assert.Equal(t, 0, countLines(..., "handleTaskStatus GetTask"))` - got **64** |
| `TestHandleTaskStatus_MalformedTaskIdsAreLoggedOncePerConnectionAndClipped` | **FAIL** | `assert.Equal(t, 1, countLines(..., "handleTaskStatus bad task id"))` - got **65**; then `assert.Less(t, len(out), 1000)` - got roughly **102000** |
| `TestConnect_InventoryPersistFailuresAreBoundedPerConnection` | **FAIL** | `assert.Equal(t, 1, ...)` - got **64** |
| `TestConnect_TwoConnectionsDoNotShareTheLogBudget` | **FAIL** | `assert.Equal(t, 2, ...)` - got **128** |

**If any of the six predicted FAILs passes at HEAD, stop and report** - the fixture is not reaching the code under test and the RED is not evidence. In particular, if `TestConnect_InventoryPersistFailures...` reports 0 lines rather than 64, the NUL is not producing an error at that site: switch the fixture to `LastUsedAt: ""` (finding 4) and report the substitution.

Paste the full `-v` output into your task report. This is the acceptance evidence for criteria 1-6 and it cannot be reproduced after Task 5.

- [ ] **Step 8: Do NOT commit**

Leave the tree dirty. These tests are committed as part of Task 5, where they are green.

---

## Task 5: The wiring - three signatures, four call sites, one deletion

This is the behaviour change. It is one task because it is the smallest unit that leaves the tree compiling: the handler signatures, `Connect`'s call sites and `export_test.go` all move together.

**Files:**
- Modify: `internal/worker/handler.go` (`Connect` `:162-184`, `handleTaskStatus` `:464-475` and `:504-508`, the limiter block `:705-752`, `handleTaskLog` `:772-835`, new `handleInventoryUpdate` beside `applyInventoryUpdate`)
- Modify: `internal/worker/export_test.go:1-43`
- Modify: `internal/worker/handler_tasklog_integration_test.go:279` (delete one line)

- [ ] **Step 1: Delete the old limiter**

In `internal/worker/handler.go`, delete lines 705-752 in their entirety - the `taskLogErrLimiterMax` const and its comment, the `taskLogErrs` var and its comment, `type taskLogErrLimiter`, `shouldLog` and `reset`. Nothing between `taskLogPublishes` (ending at `:703`) and `// handleTaskLog appends a log chunk...` (`:754`) survives.

Remove `"sync"` from the import block at `:12` **only if** nothing else in the file uses it. Check with:
```
grep -n "sync\." internal/worker/handler.go
```
`sync/atomic` is a separate import and is still used by `taskLogPublishes`; do not remove that one.

- [ ] **Step 2: Allocate the limiter in Connect and thread it to four call sites**

In `internal/worker/handler.go`, between the `workerUUID` scan block (ending `:160`) and `// Message loop.` (`:162`), insert:

```go

	// ONE log budget per CONNECTION, for every caller-driven log line on this
	// recv goroutine. It is deliberately NOT a field on Handler: Handler is
	// shared by every connection, and a shared budget would let one agent
	// suppress another's diagnostics. It is deliberately not in a registry
	// either - as a stack local it dies with this frame, so there is no teardown
	// to get wrong and no way for a stale connection to clobber a fresh one.
	//
	// It never escapes this goroutine, which is what lets it be mutex-free. DO
	// NOT capture it in a goroutine, store it anywhere, or hand it to anything
	// that outlives this call. TestConnect_TwoConnectionsDoNotShareTheLogBudget
	// is what pins this allocation site.
	lim := newIngestLogLimiter()
```

Then rewrite the switch at `:172-183`:

```go
		switch p := msg.Payload.(type) {
		case *relayv1.AgentMessage_TaskStatus:
			h.handleTaskStatus(ctx, workerUUID, lim, p.TaskStatus)
		case *relayv1.AgentMessage_TaskLog:
			h.handleTaskLog(ctx, workerUUID, lim, p.TaskLog)
		case *relayv1.AgentMessage_WorkspaceInventory:
			h.handleInventoryUpdate(ctx, workerUUID, lim, p.WorkspaceInventory)
		case *relayv1.AgentMessage_Telemetry:
			h.handleTelemetry(workerID, p.Telemetry)
		}
```

Note the inventory case loses its inline `if err := ...` block entirely; the log line moves into the new method in Step 3.

- [ ] **Step 3: Add handleInventoryUpdate**

In `internal/worker/handler.go`, immediately after `applyInventoryUpdate` (which ends at `:1009`), append:

```go

// handleInventoryUpdate applies one workspace inventory update and reports a
// failure under the connection's log budget.
//
// It exists as a named method rather than an inline block in Connect so that the
// budgeted path is testable at the same layer as handleTaskLog and
// handleTaskStatus, and so the log line has an owner. It adds no logic.
//
// This line needs the budget for the same reason the other three do, and it is
// the CHEAPEST of the four for an attacker: every string in u is bound straight
// into UpsertWorkerWorkspace or DeleteWorkerWorkspace, whose source_type,
// source_key, short_id and baseline_hash columns are all TEXT NOT NULL, so a NUL
// byte in any of them fails during bind-parameter conversion. And no NUL is even
// needed: applyInventoryUpdate swallows the time.Parse error on u.LastUsedAt, so
// an empty string binds SQL NULL into last_used_at, which is also NOT NULL. One
// error per message either way, with no gate ahead of it.
//
// Key is kindInventory with NO wire value: a persist failure here is an episode,
// not a per-workspace event, and keying on the source key would multiply one
// infra event by the workspace count. Never log u itself - source_key is a
// caller-supplied, unbounded depot path.
func (h *Handler) handleInventoryUpdate(ctx context.Context, workerID pgtype.UUID, lim *ingestLogLimiter, u *relayv1.WorkspaceInventoryUpdate) {
	err := h.applyInventoryUpdate(ctx, workerID, u)
	if err == nil {
		return
	}
	if lim.allow(logKey{kind: kindInventory}) {
		log.Printf("worker: inventory update failed: %v", err)
	}
}
```

- [ ] **Step 4: Budget handleTaskStatus's two pre-gate lines**

In `internal/worker/handler.go`, replace lines 464-475 (the signature and the two blocks) with:

```go
func (h *Handler) handleTaskStatus(ctx context.Context, workerID pgtype.UUID, lim *ingestLogLimiter, upd *relayv1.TaskStatusUpdate) {
	var taskID pgtype.UUID
	if err := taskID.Scan(upd.TaskId); err != nil {
		// Under the connection's budget, keyed on kindBadTaskID with NO wire
		// value, and SHARED with handleTaskLog's identical guard: an agent
		// malforming ids on both streams costs one line, not two. The trade is
		// per-id detail for a key the caller cannot vary - the content of the
		// second malformed id adds nothing an operator can act on, and the fact
		// that this agent sends malformed ids is the whole signal.
		//
		// %q is MANDATORY and is the injection defence; clipID is the volume
		// defence. Neither substitutes for the other: upd.TaskId has just FAILED
		// to parse, so it is a proto string bounded only by gRPC's receive limit,
		// and %q escapes without truncating.
		if lim.allow(logKey{kind: kindBadTaskID}) {
			log.Printf("worker: handleTaskStatus bad task id %q: %v", clipID(upd.TaskId), err)
		}
		return
	}

	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		// pgx.ErrNoRows here means the named task does not exist. That is
		// indistinguishable from a forged message, carries nothing an operator
		// can act on, and is the cheapest message an attacker can send - so it is
		// dropped SILENTLY, exactly as handleTaskLog drops an unresolvable chunk
		// and exactly as both gates below drop a rejected one. It also has one
		// legitimate cause: DeleteJob cascades to tasks (tasks.job_id ... ON
		// DELETE CASCADE, migration 000001), so a task row can vanish under a
		// running agent, and there is nothing to do about that either.
		//
		// Any other error is real infrastructure - a pool failure, a context
		// cancellation - and logs under the connection's budget. Keyed on
		// kindStatusGetTask with no wire value, because such an episode is not
		// per-task: keying on the task id would multiply one infra event by the
		// task count.
		if !errors.Is(err, pgx.ErrNoRows) && lim.allow(logKey{kind: kindStatusGetTask}) {
			log.Printf("worker: handleTaskStatus GetTask %s: %v", upd.TaskId, err)
		}
		return
	}
```

`upd.TaskId` at the second site keeps its bare `%s` and needs no clip: control only reaches it after `taskID.Scan` **succeeded**, so the string is already constrained to pgtype's accepted UUID forms.

Update the doc comment above the function (`:458-463`) by appending one paragraph after the existing `workerID is the connection's own authenticated worker` paragraph:

```go
// lim is this connection's log budget, allocated once in Connect. Both pre-gate
// log lines below run AHEAD of the identity and currency gates, so the budget is
// the only thing bounding them - see
// docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md.
```

- [ ] **Step 5: Budget handleTaskLog's parse guard and split its error arms**

In `internal/worker/handler.go`, replace the signature and parse guard (`:772-776`) with:

```go
func (h *Handler) handleTaskLog(ctx context.Context, workerID pgtype.UUID, lim *ingestLogLimiter, chunk *relayv1.TaskLogChunk) {
	var taskID pgtype.UUID
	if err := taskID.Scan(chunk.TaskId); err != nil {
		// Deliberately symmetric with handleTaskStatus's identical guard, and
		// they SHARE ONE budget key (kindBadTaskID) so an agent malforming ids on
		// both streams costs one line, not two.
		//
		// This used to return silently, and that was correct BEFORE the budget
		// existed: an unbounded line on this path is a flood vector. Logging it
		// is safe only because of the budget. It earns its line because an agent
		// sending unparseable ids on the log path loses 100% of that task's
		// output with no other signal anywhere - the one failure mode here with
		// total, silent data loss.
		//
		// %q is the injection defence, clipID the volume defence. Keep both.
		if lim.allow(logKey{kind: kindBadTaskID}) {
			log.Printf("worker: handleTaskLog bad task id %q: %v", clipID(chunk.TaskId), err)
		}
		return
	}
```

Then split the error block. Lines 800-835 currently open with `if err != nil {`, carry one long comment, and end with the compound condition at `:831`. Replace the whole block with:

```go
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// pgx.ErrNoRows means the fence rejected the chunk, for any of three
			// independent reasons: the sender is not the task's current assignee (a
			// forged or misrouted chunk - workerID comes from the authenticated
			// registration, never from the wire); the sender's generation is stale
			// because the task was requeued or cancelled (both bump
			// assignment_epoch); or the task finished longer ago than the trailing
			// window, which is DefaultTrailingLogWindow unless
			// RELAY_TASKLOG_TRAILING_WINDOW overrides it. THAT THIRD ONE IS THE ONE TO
			// SUSPECT FIRST WHEN OUTPUT IS MISSING RATHER THAN SPURIOUS: it is the
			// only cause that is operator-configurable, time-dependent, and triggered
			// by a perfectly legitimate sender, so a window set too small truncates
			// the tail of real task output with no other symptom anywhere. The three
			// are deliberately indistinguishable here; see the comment on
			// AppendTaskLog. Expected - drop it silently, and in
			// particular do NOT publish it: a zombie agent's output would otherwise
			// appear in a live view and then vanish on refresh, because it was
			// correctly never stored.
			//
			// THIS ARM IS DELIBERATELY SIDE-EFFECT-FREE AND MUST STAY SILENT. A log
			// line here would be caller-driven volume on the recv goroutine, and it
			// would fire on the legitimate late-flush case as well as on forged
			// chunks. Observability for this arm is
			// idea-2026-08-14-tasklog-fence-rejection-is-unobservable, whose answer
			// is a COUNTER, not a log line. Nothing here may publish. Pinned by
			// TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll, which asserts the
			// whole captured log is empty, so any wording reddens it.
			return
		}

		// A real persist failure that used to be swallowed by `_ =`.
		//
		// Deduplicated to one line per task per assignment epoch: the realistic
		// such failure repeats for every chunk of the task (binary stdout ->
		// 'invalid byte sequence for encoding "UTF8"'), and this runs on the recv
		// goroutine, so logging per chunk would delay that worker's status,
		// inventory and telemetry ingest. The dedupe is keyed on wire values on
		// purpose and is NOT the bound - the connection's token bucket is. See
		// ingestLogLimiter.
		//
		// The epoch goes in at full int64 width here. The int32 narrowing at the
		// fence parameter above is bug-2026-08-12-tasklog-epoch-int32-truncation
		// and is deliberately untouched by this slice.
		//
		// Never log chunk.Content: it is raw subprocess output and can contain
		// secrets a job's own script echoed. Logging the error with %v is safe
		// because pgconn.PgError.Error() renders only severity, message and
		// SQLSTATE - never Detail, which is where Postgres puts "Failing row
		// contains (...)". Do not start logging pgErr.Detail here.
		//
		// chunk.TaskId needs no clip at this site: taskID.Scan succeeded above, so
		// it is already constrained to pgtype's accepted UUID forms.
		if lim.allow(logKey{kind: kindTaskLogPersist, id: chunk.TaskId, epoch: chunk.Epoch}) {
			log.Printf("worker: handleTaskLog AppendTaskLog %s: %v", chunk.TaskId, err)
		}
		return
	}
```

**Change nothing below this block.** The `taskIDStr := uuidStr(taskID)` line, the `HasLogSubscriber` fast path, the marshal and the `Publish` stay exactly where they are, strictly after the fence has matched.

Also append one paragraph to `handleTaskLog`'s doc comment (`:754-771`), after the `workerID is the connection's own authenticated worker` paragraph:

```go
// lim is this connection's log budget, allocated once in Connect. It bounds
// every caller-driven log line below; it performs one map lookup and one integer
// compare and never blocks, which is what keeps this function inside the
// one-round-trip budget stated at the end of this comment.
```

- [ ] **Step 6: Fix the stale prose in the identity-gate comment**

At `internal/worker/handler.go:504-508` (inside the identity gate's comment, now shifted by the edits above), the paragraph currently reads:

```go
		// It does NOT save a log line. Both write-error branches below are wrapped
		// in `if !errors.Is(err, pgx.ErrNoRows)`, so a forged message rejected by
		// either fence logs nothing at all - delete this gate and the log volume is
		// unchanged. Nor did this function ever have a "zero attacker-keyed log
		// lines" property to protect: the bad-task-id and GetTask branches at the
		// top of this function both log unconditionally, keyed on upd.TaskId, AHEAD
		// of this gate. That is bug-2026-08-12-tasklog-err-limiter-attacker-keyed's
		// shape, it is still live on this path, and this gate does not address it.
```

Every clause after "Nor did this function" is now false. Replace that paragraph with:

```go
		// It does NOT save a log line. Both write-error branches below are wrapped
		// in `if !errors.Is(err, pgx.ErrNoRows)`, so a forged message rejected by
		// either fence logs nothing at all - delete this gate and the log volume is
		// unchanged. Nor does this function have a "zero attacker-keyed log lines"
		// property to protect: the two branches at the top of this function still
		// run AHEAD of this gate, and a gate cannot protect a line placed before
		// it. What bounds them now is the CONNECTION'S BUDGET (ingestLogLimiter),
		// which is keyed on nothing the caller supplies - the GetTask branch's
		// pgx.ErrNoRows case is silent outright, and everything else there costs at
		// most one line per connection.
		// bug-2026-08-12-tasklog-err-limiter-attacker-keyed is closed; do not cite
		// it here as live.
```

**Do not change any other part of that comment**, and do not touch the gate condition at `:542` or the currency gate at `:555`.

- [ ] **Step 7: Verify no other caller of the three handlers exists**

Run:
```
grep -n "h.handleTaskStatus(\|h.handleTaskLog(\|h.handleInventoryUpdate(\|applyInventoryUpdate(" internal/worker/*.go
```

Expected: `Connect` (three calls), `handleInventoryUpdate` (one call to `applyInventoryUpdate`), and `export_test.go`. Nothing else. If a fourth production caller exists, it is a finding - the limiter's single-goroutine ownership assumption may not hold there.

- [ ] **Step 8: Apply the export_test shim**

Apply the code block from Task 3 to `internal/worker/export_test.go`, replacing the two wrapper functions at lines 16-29, and **delete** lines 39-43:

```go
// ResetTaskLogErrLimiterForTest clears the persist-failure log rate limiter so a
// test starts from a known state regardless of what earlier tests reported.
func ResetTaskLogErrLimiterForTest() {
	taskLogErrs.reset()
}
```

Add `"sync"` to the import block.

- [ ] **Step 9: Delete the one permitted line from the pinned test**

In `internal/worker/handler_tasklog_integration_test.go`, delete line 279:

```go
	worker.ResetTaskLogErrLimiterForTest()
```

**That is the ONLY edit permitted anywhere in that file's existing content.** Every assertion in `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` - `assert.Equal(t, 1, ...)`, both `assert.Equal(t, 2, ...)`, `assert.NotContains(t, logged(), secret)` - and every call site stays byte-identical. If any of them needs adjusting to go green, **STOP and report it as a finding**: it means the key granularity changed the diagnostic contract.

- [ ] **Step 10: Compile gates**

Run:
```
go build ./...
go vet -tags integration ./...
go test ./... -timeout 120s
```
Expected: all succeed. `go test ./...` still runs the untagged limiter unit tests from Task 2; all 10 must still pass.

- [ ] **Step 11: The Task 4 REDs must now be GREEN**

Run the exact command from Task 4 Step 7:
```
go test -tags integration -p 1 ./internal/worker/... -run 'TestHandleTaskLog_(ANulChunk|DistinctWireTaskIds|VaryingWireEpoch|MalformedTaskId|AFenceRejection)|TestHandleTaskStatus_(UnknownWellFormed|MalformedTaskIds)|TestConnect_(InventoryPersistFailures|TwoConnectionsDoNot)' -v -timeout 900s
```
Expected: **9 PASS, 0 FAIL**, with **no edit to any test**.

- [ ] **Step 12: Full worker integration suite**

Run:
```
go test -tags integration -p 1 ./internal/worker/... -timeout 900s
```
Expected: all PASS. In particular:
- `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` - green with exactly one deleted line and no assertion change.
- `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished`, `..._RejectsAChunkFromANonAssignee`, `..._RejectsAChunkForANeverClaimedTask`, `..._RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow`, `..._TrailingChunkJustAfterATerminalStatusIsStillStored`, `..._LiveTaskWithNoFinishedAtIsStillStoredAndPublished`, `..._TheWindowIsReadFromTheHandlerFieldAtEveryCall`, `..._AZeroWindowMeansTheDefaultNotAZeroLengthWindow` - all green with no edit.
- Every test in `handler_taskstatus_integration_test.go` - green with no edit.
- `TestConnect_TaskLogChunkIsFencedOnTheConnectionsOwnWorker` and `TestConnect_TaskStatusIsFencedOnTheConnectionsOwnWorker` - green with no edit; they exercise the new `lim` allocation in `Connect`.

**Any test whose log-count assertion moves is a finding to report, not to fix.** The one plausible source is a test that was silently relying on cross-test state in the old package-level `taskLogErrs`; that is information, not breakage.

- [ ] **Step 13: Confirm the deleted symbols are gone**

Run:
```
grep -rn "taskLogErrs\|taskLogErrLimiter\|ResetTaskLogErrLimiterForTest" internal/ cmd/
```
Expected: **zero matches.**

- [ ] **Step 14: Commit**

```bash
git add internal/worker/handler.go internal/worker/export_test.go \
        internal/worker/handler_ingest_budget_integration_test.go \
        internal/worker/handler_tasklog_integration_test.go
git commit -m "fix(worker): bound agent-driven log volume on the gRPC recv goroutine

Replaces taskLogErrLimiter, whose keys were read straight off the wire, with a
per-connection ingestLogLimiter that separates the two jobs the old code
conflated: a wire-keyed dedupe map for diagnostics quality, and a token bucket
keyed on nothing for the bound.

The old limiter did not need its overflow path to be defeated. It stored one
entry per task id with the epoch as the VALUE, so a caller varying chunk.Epoch
on a single task id overwrote that one entry forever, never reached the 1024
cap, and got one log.Printf per message - which is why keying on
(worker, task, epoch) with the same map shape would not have closed it either.

All four caller-driven log sites on the recv path now share one budget:
handleTaskLog's persist failure and its previously-silent parse failure,
handleTaskStatus's two pre-gate lines, and Connect's inventory line, which was
never limited at all and is the cheapest of the four to drive. The GetTask
pgx.ErrNoRows line is deleted rather than budgeted - a status update naming a
nonexistent task is indistinguishable from a forged one and its one legitimate
cause, a cascaded job delete, is not actionable.

The fence-rejection arm stays silent and side-effect-free, split into its own
branch so the tracked counter item can drop straight into it. Chunk content
still never reaches the log, both bad-id lines keep %q and now clip the
unparsed identifier, and no query, goroutine, queue or lock is added.

Closes bug-2026-08-12-tasklog-err-limiter-attacker-keyed."
```

---

## Task 6: Full gates, the mutation battery, race, and closing the item

- [ ] **Step 1: Run every gate**

Run, in order:
```
go build ./...
go vet -tags integration ./...
go test ./... -timeout 120s
go test -tags integration -p 1 ./internal/worker/... -timeout 900s
go test -tags integration -p 1 ./internal/store/... -timeout 900s
go test -tags integration -p 1 ./internal/scheduler/... -timeout 900s
go test -tags integration -p 1 ./internal/api/... -timeout 900s
```
Expected: all PASS. Record the pass counts. If anything is red, **diagnose it and get a number both with and without the change** before calling it pre-existing.

- [ ] **Step 2: Run the Connect-level integration tests under the race detector**

The limiter is mutex-free on the strength of single-goroutine ownership. This is the check on that claim.

On Windows, `-race` needs MSYS2 mingw64 gcc; the default Strawberry Perl gcc fails with exit `0xc0000139` on every package:
```
CC=/c/msys64/mingw64/bin/gcc.exe go test -race -tags integration -p 1 ./internal/worker/... -timeout 1800s
```
On Linux/CI, drop the `CC=`.

Expected: all PASS, **zero `WARNING: DATA RACE`**.

State honestly in the report what this does and does not prove: it exercises the existing `Connect` tests, which drive one stream at a time, so it confirms no *current* caller touches the limiter off the recv goroutine. It cannot prove a future one will not. The structural control is that `lim` is a stack local in `Connect` and is never stored.

- [ ] **Step 3: Run the mutation battery**

Work through Appendix A. For each row: apply the mutation, run the named test, confirm the named assertion fails with the named value, then revert and confirm green again.

Every mutation in the battery is staged so the RED is **behavioural, not a compile error**. If a mutation you try does not compile, you have mis-applied it - reread the "how to stage it" column. Report any mutation whose prediction was wrong: **a mutation that does not redden the predicted assertion is a finding about the test, not about the plan.**

- [ ] **Step 4: Verify the working tree is exactly the intended file set**

Run:
```
git status --short
git diff --stat origin/main...HEAD
```
Expected file set, and nothing else:
```
docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md
docs/superpowers/plans/2026-08-15-tasklog-err-limiter-keying.md
internal/worker/ingest_log_limiter.go
internal/worker/ingest_log_limiter_test.go
internal/worker/handler.go
internal/worker/export_test.go
internal/worker/handler_ingest_budget_integration_test.go
internal/worker/handler_tasklog_integration_test.go
```
Plus, after Step 5, the `git mv` of the backlog item. **Nothing under `internal/store/`, nothing under `internal/proto/`, nothing under `web/`, no `.sql` and no `.sql.go`.** If any generated file appears, something ran `make generate` and that is a finding.

- [ ] **Step 5: Close the backlog item**

Run the command; do not hand-edit the item's frontmatter. The `git mv` into `docs/backlog/closed/` is required scope, not cleanup.

```
/backlog close tasklog-err-limiter-attacker-keyed
```

The `## Resolution` note must record all four of these, because each contradicts something the item itself says:

1. **The item's own Proposal A fails open as written.** Keying on `(workerID, taskID, epoch)` closes nothing if the epoch stays the map *value*, which is the shape the item's text implies. The epoch-varying flood needs no fresh task id and never reaches the capacity cap.
2. **The acceptance criterion about "a bounded number of `GetTask` round trips" is STRUCK** (spec D5). It is unachievable inside the item's own constraints - there is no way to know a well-formed UUID names no task without querying, and the item rules out a new query, a cache and a recv-loop limiter. The query cost is already bounded at one in-flight statement per connection, the same bound legitimate traffic has. That criterion belongs to the recv-loop limiter item.
3. **A third instance was found and fixed** that the item does not enumerate: the unlimited `log.Printf("worker: inventory update failed: %v", err)` in `Connect`. The item's own Notes ask for the grep that finds it. It is additionally reachable with no NUL byte at all, via an empty `last_used_at`.
4. **The bad-task-id asymmetry was settled by making BOTH handlers log**, once per connection, under one shared key - not by making both silent.

Confirm afterwards:
```
git status --short docs/backlog/
```
Expected: the item now lives at `docs/backlog/closed/bug-2026-08-12-tasklog-err-limiter-attacker-keyed.md` with `status: closed`, a `closed:`/`resolution:` stamp, and a `## Resolution` section.

- [ ] **Step 6: Final report**

Report, in this order:

1. The verbatim RED output from Task 4 Step 7, with the 6-fail / 3-pass split confirmed.
2. Confirmation that Task 1's premise test was GREEN at HEAD (and therefore that every bound in Task 4 measures something).
3. The mutation battery results from Appendix A, including any prediction that was wrong and the blind spots restated.
4. Confirmation that `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` lost exactly one line and no assertion changed.
5. The `-race` result, with the honest statement of what it does and does not prove.
6. The `int32(chunk.Epoch)` truncation you deliberately did not fix, flagged for `bug-2026-08-12-tasklog-epoch-int32-truncation`.
7. Your judgement on the `clipID` addition (finding 5 / D10): keep or drop.
8. Anything that surprised you.

---

## Appendix A: Mutation battery

**Enumerated by PROPERTY, not by function.** A battery organized by function covers the write half of a pair and misses the read half; a previous slice in this batch shipped exactly that shape. Each row names a behavioural property and, where a property has more than one half, the same mutation must redden every listed test.

**Every mutation is staged to fail BEHAVIOURALLY.** None of them removes a parameter or otherwise breaks the build, because a compile error means the predicted behavioural assertion never runs and the row proves nothing.

`U` = unit, no Docker (`go test ./internal/worker/... -run <name>`).
`I` = integration, Docker required (`go test -tags integration -p 1 ./internal/worker/... -run <name> -timeout 900s`).

| # | Property under test | Mutation (how to stage it) | Lane | Test that must go RED | Predicted assertion and value |
|---|---|---|---|---|---|
| M1a | **The bucket, not the map, is the bound** | In `allow`, replace `if l.tokens == 0 { return false }` with `if l.tokens < 0 { return false }` (keeps the field read, keeps it compiling, never fires) | I | `TestHandleTaskLog_DistinctWireTaskIdsCannotFloodTheLog` | `assert.LessOrEqual(t, lines, floodLogUpperBound)` - `lines` is **64** |
| M1b | Same property, epoch-varying arm | Same as M1a | I | `TestHandleTaskLog_VaryingWireEpochOnOneTaskCannotFloodTheLog` | `assert.LessOrEqual(t, lines, floodLogUpperBound)` - `lines` is **64** |
| M1c | Same property at the limiter | Same as M1a | U | `TestIngestLogLimiter_DistinctKeysAreCappedAtBurst` | `allowed = 1000, want 16` |
| M2 | **The item's own remedy is insufficient** | Revert to a dedupe-only limiter with the old shape: give `ingestLogLimiter` a `reported map[string]int64` keyed on `k.id` with `k.epoch` as the value, drop the bucket entirely, and make the capacity branch *suppress* instead of clearing (the item's proposal, in its best version) | I | **`TestHandleTaskLog_VaryingWireEpochOnOneTaskCannotFloodTheLog`** | `assert.LessOrEqual(t, lines, floodLogUpperBound)` - `lines` is **64**. This row is the reason that test exists; it is the ONLY test in the tree that stays red against the item's own fix |
| M3 | **The epoch is part of the persist key** (the diagnostic's read half) | Delete `epoch: chunk.Epoch` from the `logKey` literal in `handleTaskLog`'s persist arm (leave the struct field in place so `logKey` still compiles) | I | `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` | `assert.Equal(t, 2, countLines(logged(), marker), "a new assignment epoch must be reported once more")` - got **1** |
| M4 | **The task id is part of the persist key** (the diagnostic's other half) | At the limiter level, make `allow` zero `k.id` on entry (`k.id = ""`) | U | `TestIngestLogLimiter_TaskIdIsPartOfThePersistKey` | `two different tasks failing at the same epoch must both log: a=true b=false`. **Blind spot: no integration test covers this** - see below |
| M5 | **Dedupe runs before the spend** | In `allow`, move the `if l.tokens == 0 { return false }` block ABOVE the `if _, ok := l.seen[k]; ok` block | U | `TestIngestLogLimiter_DedupeHappensBeforeTheSpend` | `t.Fatalf("tokens = %d, want 15")` - got **0**; if that leg is relaxed, `fresh keys allowed = 0, want 15` |
| M6 | **A suppressed key is not recorded** | In `allow`, move `l.seen[k] = struct{}{}` to immediately before the `if l.tokens == 0` check | U | `TestIngestLogLimiter_ASuppressedKeyIsNotRecorded` | `t.Fatal("a key suppressed for lack of a token must NOT be recorded in seen")`; if that leg is relaxed, `t.Error("after a refill the suppressed key must log, not dedupe-drop")` |
| M7 | **Refill advances by whole consumed intervals** | In `allow`, relax the guard to `elapsed > 0` and replace `l.last = l.last.Add(time.Duration(n) * ingestLogRefill)` with `l.last = l.now()`. Both edits are needed: with the original `>=` guard the mutation is unreachable on sub-interval calls and would prove nothing | U | `TestIngestLogLimiter_RefillDoesNotStallUnderCallsMoreFrequentThanTheInterval` | `allowed = 0 over 20s of 1ms-spaced calls, want 2` |
| M8 | **Refill caps at burst** | In `allow`, replace the clamp with `l.tokens = int(int64(l.tokens) + n)` | U | `TestIngestLogLimiter_RefillsWholeIntervalsAndCapsAtBurst` | second leg: `after 1000 refill intervals, allowed = 1000, want 16` |
| M9 | **The dedupe map is bounded** | In `allow`, delete the `if len(l.seen) >= ingestLogSeenMax { clear(l.seen) }` block | U | `TestIngestLogLimiter_SeenMapIsBoundedAndClearsAtCapacity` | `len(seen) reached 512, want at most 128` |
| M10 | **The `GetTask` `ErrNoRows` line is deleted, not budgeted** | In `handleTaskStatus`, change `if !errors.Is(err, pgx.ErrNoRows) && lim.allow(...)` to `if lim.allow(...)` | I | `TestHandleTaskStatus_UnknownWellFormedTaskIdsAreSilent` | `assert.Equal(t, 0, countLines(..., "handleTaskStatus GetTask"))` - got **1** (not 64: the budget dedupes it, which is exactly why this assertion is `Equal(0)` and not an upper bound) |
| M11 | ~~**Both handlers share one bad-id key**~~ **INVERTED 2026-08-15 by review.** The sharing was the defect: use ONE key for both parse guards | I | `TestHandleTaskLog_MalformedTaskIdIsLoggedOncePerConnectionAndHasItsOwnBudgetKey` LEG B | `assert.Equal(t, 1, logLines, "the log path has its OWN bad-task-id key")` - got **0**, because one forged status message consumed the shared key |
| M12 | **The log path logs a malformed id at all** (spec D2) | In `handleTaskLog`'s parse guard, delete the `if lim.allow(...) { log.Printf(...) }` block, leaving the bare `return` | I | Same test, LEG A | `assert.Equal(t, 1, countLines(loggedA(), "handleTaskLog bad task id"))` - got **0** |
| M13 | **The budget is per CONNECTION** | Move `lim := newIngestLogLimiter()` out of `Connect` and into `NewHandler`/`NewHandlerWithGrace` as a `Handler` field, reading `h.lim` at the three call sites | I | `TestConnect_TwoConnectionsDoNotShareTheLogBudget` | `assert.Equal(t, 2, countLines(logged(), "inventory update failed"))` - got **1**. **The only test that pins the allocation site**; the export_test shim cannot substitute for it |
| M14 | **The inventory line is budgeted at all** | In `handleInventoryUpdate`, drop the `lim.allow` guard so it logs unconditionally | I | `TestConnect_InventoryPersistFailuresAreBoundedPerConnection` | `assert.Equal(t, 1, ...)` - got **64** |
| M15a | **The fence-rejection arm is silent, any wording** | Add `log.Printf("worker: tasklog fence rejected %s", chunk.TaskId)` to the `pgx.ErrNoRows` arm | I | `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` | `assert.Equal(t, "", logged(), "...NO log line of any wording")` - got a one-line string |
| M15b | Same property, existing marker | Add `log.Printf("worker: handleTaskLog AppendTaskLog %s: fence", chunk.TaskId)` to the same arm | I | `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` | `assert.Equal(t, 2, countLines(logged(), marker), "a stale-epoch drop must stay silent")` - got **3** |
| M16 | **The fence-rejection arm publishes nothing** (the read half of M15) | Move the `HasLogSubscriber`/marshal/`Publish` block above the `if err != nil` check | I | `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` AND `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished` | first: `t.Fatalf("a fence-rejected chunk must not be published: %s", e.Data)`; second: `t.Fatalf("a stale-epoch chunk must not be published: %s", e.Data)` |
| M17 | **Chunk content never reaches the log** | Change the persist-arm `log.Printf` format to `"...%s: %v (%s)"` with `string(chunk.Content)` appended | I | `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch`, `..._DistinctWireTaskIds...`, `..._VaryingWireEpoch...` | `assert.NotContains(t, logged(), secret, "chunk content must never be logged")` - the buffer contains `SECRET-sentinel` |
| M18 | **An unparsed caller id is clipped** | In `handleTaskStatus`'s parse guard, replace `clipID(upd.TaskId)` with `upd.TaskId` | I | `TestHandleTaskStatus_MalformedTaskIdsAreLoggedOncePerConnectionAndClipped` | `assert.Less(t, len(out), 1000)` - got roughly **102000** |
| M19 | Same property at the helper | In `clipID`, change `if len(s) <= ingestLogIDClip` to `if true` | U | `TestClipID_BoundsTheLoggedIdentifier` | `clipID returned 100000 bytes, want at most 96` |
| M20 | **A nil budget drops rather than panics** | In `allow`, delete the `if l == nil { return false }` guard | U | `TestIngestLogLimiter_NilLimiterDropsTheLineInsteadOfPanicking` | panic: `runtime error: invalid memory address or nil pointer dereference` (a panicking test is a FAIL; that is the RED) |
| M21 | **The premise: a NUL fails before the fence** | Not a code mutation - a fixture one. In the premise test, change `Content: []byte("x\x00")` to `[]byte("x")` | I | `TestHandleTaskLog_ANulChunkForAnUnfencedTaskIsAPersistErrorNotAFenceRejection` | `assert.Equal(t, 1, countLines(...))` - got **0**. Proves the test is measuring the NUL and not something else |

### Known blind spots - state these in the report, do not pretend they are covered

1. **"Dedupe before spend" (M5) and "a suppressed key is not recorded" (M6) have NO integration coverage.** Both are unit-only. The reason is structural, not laziness: the existing `PersistFailure` test sends 8 chunks of one key, and under spend-before-dedupe it still logs 1 then 1, so it stays green. Any integration test that could see the difference would need to exhaust a 16-token bucket through 16 distinct real DB round trips and then observe a 17th - which is exactly what the unit test does deterministically and in microseconds. Do not add a slow, flaky integration version for the sake of symmetry.
2. **The `id` component of the persist key (M4) is pinned only at the unit layer.** The existing `PersistFailure` test uses one task, so dropping `id` from the key does not redden it. An integration version would need two concurrently-failing tasks on one connection; it is worth exactly one line in the report, not a new container.
3. **`-race` proves single-goroutine ownership only for the callers that exist today.** It cannot prove a future caller will not appear off the recv goroutine. The structural control is that `lim` is a stack local in `Connect`, never stored and never captured; the comment says so and a reviewer is the enforcement.
4. **Nothing tests that `handleTelemetry` stays outside the budget.** It logs nothing, so there is nothing to bound; if it ever gains a log line, that line needs the budget and no test will say so.
5. **The three post-gate log sites and the three registration-time sites are unbudgeted by design** and no test asserts they stay that way. Finding 6 records why each is unreachable by a caller; that reasoning is the control.
6. **Tuning `ingestLogBurst` breaks the coupling deliberately.** `TestIngestLogLimiter_ConstantsAreWhatTheHandlerTestsAssume` is the only link between the constant and the `floodLogUpperBound = 20` literal in the integration file. That is intentional: changing the bound should require touching the tests that assert it.

---

## Appendix B: Constraint checks (state these in the Phase 4 report)

- **Epoch fence (CLAUDE.md).** This slice adds no write to `tasks.status` or `task_logs`, changes no predicate, and changes no fence argument. `AppendTaskLog`'s three predicates and its status allow-list are untouched, as is `int32(chunk.Epoch)` at the fence parameter. The `pgx.ErrNoRows` drop stays silent and stays **before** the publish: Task 5 Step 5 moves the `return` into its own arm without reordering anything relative to `h.broker.Publish`, and M16 is the mutation that catches a regression there. **Enumerate what runs before the fence:** `taskID.Scan` (which now may emit one budgeted line and then returns), the stream enum mapping, the window resolution, and the `AppendTaskLogParams` literal. One extra branch, no database access, no new state read.
- **The epoch establishes currency, not identity.** Untouched. `workerID` still comes from the authenticated registration and never from the wire, and the plain NULL-rejecting `=` on `worker_id` is unchanged.
- **Status predicates as allow-lists.** No SQL changed, so nothing to restate.
- **One bounded sender per gRPC stream.** Untouched. No send is added; this is entirely on the recv path.
- **Identity-checked teardown.** The limiter has **no teardown**: it is a stack local in `Connect` and dies with the frame. It is never registered, never stored on `Handler`, and never reachable from another connection - by construction rather than by a key. `teardownConnection` is unmodified.
- **No interior pointers across locks.** The limiter has no lock. It is passed by pointer only to synchronous callees on the same goroutine and never escapes. The one lock added anywhere is in `export_test.go`, guarding a test-only map, and it hands out a pointer that the caller then uses on the same goroutine - the same ownership contract, in a file that never ships.
- **Single job-spec pipeline / single JSON entry point / `internal/tokenhash.Hash`.** Not implicated.
- **Never edit `*.sql.go` or `models.go`.** No `.sql` change, therefore no `make generate`, no CRLF revert dance, and no generated file in the final diff. Task 6 Step 4 checks this.
- **Load.** No new query, statement, index, goroutine, channel or queue. `handleTaskLog` still performs exactly one DB round trip; `handleTaskStatus` gains none; `handleInventoryUpdate` performs exactly the one `applyInventoryUpdate` already did. Per message, the added cost is one map lookup, one integer compare and (on the failure path only) one map insert.
- **CLAUDE.md is not amended.** This slice adds no invariant and changes none. Recorded as a deliberate decision because three of the last four slices in this family each amended the epoch-fence bullet, so silence here should read as intentional. The general shape the item names - *"a rate limiter keyed on a field the rate-limited party supplies is not a rate limiter"* and *"an unlimited log line placed before an authorization gate is not protected by that gate"* - is a candidate for the next CLAUDE.md refresh, but adding it is out of this slice's scope; raise it in the retro.
