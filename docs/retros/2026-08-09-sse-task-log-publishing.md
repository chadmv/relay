---
date: 2026-08-09
topic: sse-task-log-publishing
branch: claude/pr-merging-session-0674dd
range: e8e0bc1..HEAD
---

# Session Retro: 2026-08-09 - sse-task-log-publishing

**TL;DR:** Iteration 2 of the same 5-item unattended `/autopilot` batch gave relay a live task-log
source for the first time. `GET /v1/events` now accepts `?task_id=` and emits a `task_log` event per
persisted chunk, routed through a second, task-keyed broker index so a log publish iterates only that
task's tailers and a plain `GET /v1/events` never becomes a cluster-wide log firehose. `AppendTaskLog`
went from `:exec` to `:one` via a `fence`/`ins` CTE that returns `job_id`/`seq`/`created_at`, which
both keeps ingest at one DB round trip and makes the epoch fence's outcome observable so the publish
can be gated on it. Two adjacent defects were fixed on the way: `relayclient.StreamEvents`'s default
64 KiB scanner (which would have failed a whole stream on one oversized log frame) and persist
failures being swallowed by `_ =`. Backend-only: zero `web/` and zero `internal/mcp/` changes, no
migration. Review returned 0 high / 3 medium / 9 low, and the most consequential finding was a wrong
contract in the README rather than a bug in the code. The `test-integration` gate turned out to have
been failing for every change for some time, which this iteration diagnosed and fixed (300s -> 900s)
rather than absorbing.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-09-sse-task-log-publishing.md`.
- **Plan** `docs/superpowers/plans/2026-08-09-sse-task-log-publishing.md`.
- **Feature**, landed as a sequential single-engineer slice in the plan's order (broker first, alone,
  before anything published):
  - `internal/events/broker.go` - `Event.TaskID`, a `Filter{JobID, TaskID}` value replacing
    `Subscribe(string)`, the task-keyed `logSubs map[string]map[chan Event]struct{}` index,
    `HasLogSubscriber`, `TypeTaskLog`, and `removeLocked` as the single close-point. `Publish` now
    branches: a `task_log` iterates `logSubs[e.TaskID]` only; a status event iterates `subs` and
    skips any subscription that named a task but no job, which is what stops `?task_id=` alone from
    becoming an accidental all-jobs status stream.
  - `internal/api/events.go` - `?task_id=` parsed with `parseUUID` and existence-checked with one
    `GetTask` **per connection**, both before any response header is written, so a rejection is JSON
    rather than an error buried inside a half-started `text/event-stream`. Plus the terminal
    `event: dropped` / `{"reason":"slow_consumer"}` frame on broker close.
  - `internal/store/query/tasks.sql` - `AppendTaskLog` as `:one` with the `fence`/`ins` CTE.
  - `internal/worker/handler.go` - the `taskLogEvent` payload type, the `HasLogSubscriber` fast path,
    the publish derived from the returned row, and a bounded persist-failure limiter
    (`taskLogErrs`, one line per task per assignment epoch, capped at 1024 retained task ids).
  - `internal/relayclient/client.go` - `scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)`.
  - `README.md` - the events section rewritten: the four-row delivery matrix, both new event types,
    the ordered subscribe-then-backfill recipe, the shared-buffer caveat, the `?job_id=` /
    `?task_id=` validation asymmetry, and the single-process caveat.
  - `Makefile` - `test-integration` timeout 300s -> 900s, with the reason recorded in the comment.

## Key Decisions

- **`?task_id=` on `/v1/events`, not `?follow=1` on the task-logs endpoint - settled here for both
  consumers.** The backlog item left the shape open and explicitly asked for it to be decided on the
  backend half. The decisive argument is a property of the existing broker, not a preference: either
  shape needs a task-aware filter anyway, because publishing log events under today's filter model
  would hand every log line on the cluster to every `Filter{}` subscriber (which `relay watch` opens),
  overrun its 64-slot buffer, and drop-close it - breaking *status* delivery for existing clients.
  Once the filter is task-aware, `?task_id=` is nearly free, whereas `?follow=1` additionally needs a
  second streaming handler, a JSON-vs-SSE content switch on a query parameter, and a second
  connection for any view that wants status too. `?job_id=&task_id=` on one subscription gives both.
- **Task-keyed delivery with deliberately no job-wide and no cluster-wide log firehose.** The second
  index is not an optimization bolted on; it is the mechanism that makes the amplification shape
  unreachable. No consumer wants a job-wide log stream (both the full-screen view and the job-detail
  tab show one selected task), so adding one would be building the exact fan-out the design rejects.
- **`removeLocked` as the single close-point.** A `?job_id=&task_id=` subscription puts one channel in
  two maps, and that channel must be closed exactly once and removed from both on four exit paths
  (explicit cancel, double cancel, drop via a status publish, drop via a log publish). Getting it
  wrong panics inside `Publish`, which runs synchronously on the `Connect` recv goroutine, so the
  failure mode is "an agent connection dies mid-job", not "an SSE client glitches". Making `subs` the
  presence authority and routing every close through one locked helper collapses
  close-exactly-once and removal-from-both-indexes into a single invariant instead of two that have
  to agree.
- **The epoch fence gained reach rather than changing.** No new write to `tasks.status` or
  `task_logs`, no epoch bump anywhere. What changed is that the fence's *outcome* is now observable:
  `:exec` collapsed "inserted one row" and "inserted zero rows" into the same `nil` error, so the
  caller could not tell a stale chunk from a stored one. As `:one`, a stale chunk matches no fence row
  and surfaces `pgx.ErrNoRows`, which `handleTaskLog` drops silently and, critically, drops *before*
  publishing. A zombie agent's output must never appear in a live view and then vanish on refresh,
  having correctly never been stored.
- **Match the existing authorization model, do not tighten it here.** `GET /v1/events` and
  `GET /v1/tasks/{id}/logs` are both `auth(...)`-only with no ownership check, so any authenticated
  user can already read any task's logs. A live view of bytes the same token already reads is no
  escalation, and gating only the live path would accomplish nothing. Tightening cross-tenant reads
  has to land on the polling endpoint at the same time or not at all.
- **No `step_index`/`step_total`.** `TaskLogChunk` carries them, but the polling endpoint cannot
  supply them, and payload symmetry with `logEntry` is the whole reason a consumer can define one
  log-line type and merge SSE frames with pages without a translation layer.
  `feature-2026-06-26-persist-expose-step-index-total` adds the columns under the fence and lights up
  both surfaces together.

## Problems Encountered

1. **The backlog item was wrong about the code in four places.** Most consequentially it framed
   "bound the publish so a slow SSE subscriber cannot block `handleTaskLog`" as the work to be done,
   when `Broker.Publish` was **already** bounded by construction (`select { case ch <- e: default: }`,
   then close-and-drop). Had that gone unchecked, the plan would have budgeted a task for a guarantee
   that already held, and the real risks - per-chunk cost and cross-subscriber amplification - would
   have gone unaddressed. The other three: the item said "keyed by job id (and task index)", but there
   is **no task ordinal column** on `tasks` (the SPA's `/jobs/:id/tasks/:n` `n` is a client-side list
   position); it implied `handleTaskLog` has the job id in hand, but `TaskLogChunk` carries only
   `task_id`, so a job-keyed event needed either a second query on the hot recv path or the job id out
   of the same statement; and it did not know that `AppendTaskLog` as `:exec` could not distinguish a
   fence rejection from success, which is the fact the whole publish gate turns on. All four were
   caught during spec by reading the code. **This is the second iteration in a row where spec-time
   verification corrected the item**, which reinforces rather than discovers the durable lesson that
   [[feedback_backlog_proposal_not_contract]]: an item's Proposal is a hypothesis about the code, and
   the code wins.
2. **Five plan-supplied tests were vacuous or broken and the engineer had to fix them
   mid-implementation.** Not one slip - five, in one plan:
   - The task-keyed-index test used a drainer goroutine counting to 65 against a 64-slot buffer, an
     off-by-one that made the assertion depend on scheduling. Replaced with no drainer at all: every
     `Publish` completes before the assertions run, so the outcome cannot depend on timing.
   - A `time.After` where a non-blocking select was correct.
   - A backfill assertion that was satisfied while the tail was still in flight, so it could pass
     without the property holding.
   - A sleep-based barrier for "the subscription is live", replaced by a recorded flush count on a
     test `ResponseWriter` (`handleEvents` subscribes and then flushes before its first receive, so a
     recorded flush is an exact barrier where a wall-clock sleep could not distinguish a slow
     subscribe from a broken one).
   - A validation test that **hung for 400s in RED**, because an `httptest` request's context is never
     cancelled, so a handler that wrongly accepted `?task_id=` streamed forever instead of failing.
     Fixed by bounding each probe with a 2s context.

   **This is now the pattern, not an incident.** A plan is written before the code exists, so its test
   bodies are guesses, and the guesses are wrong often enough that treating them as drafts is the only
   safe posture. The conductor promoted this to durable memory during this batch, which is the right
   response; the retro's job here is to record that the promotion was earned by five more instances in
   the very next iteration.
3. **A non-vacuity proof initially hung instead of failing.** The stalled-subscriber test
   (`TestBroker_PublishNeverBlocksOnAStalledLogSubscriber`) had a `defer cancel()`. When the plan's
   step-5 mutation replaced the bounded send with a bare `ch <- e` to prove the test RED, the
   publishing goroutine parked inside `Publish` still holding `b.mu`, so `t.Fatal`'s deferred `cancel`
   blocked forever on `b.mu.Lock()` and the whole package timed out instead of reporting the failure.
   **A concurrency test that hangs instead of failing is worse than a vacuous one**: a vacuous test
   lies quietly and cheaply, while a hanging test consumes the entire suite budget and produces a
   package-level timeout that points at nothing. The fix was to drop the `defer cancel()` entirely and
   say why in the test: the broker is test-local, so leaking the subscription on the failure path
   costs nothing, and on the success path the broker has already closed and removed the channel
   itself. General rule: a test whose failure mode is "the thing under test is stuck holding a lock"
   must not take that same lock on its own failure path.
4. **`sqlc` rejected the spec's CTE.** The spec's `SELECT id, job_id FROM tasks` in the fence CTE
   failed generation with `column reference "id" is ambiguous`, because the analyzer cannot resolve
   `id` across the two CTEs. Fixed with a `tasks t` alias, qualified column references, and dropping
   the unused `id` from the fence (only `job_id` is needed; the fence's job is to yield exactly one
   row or none). Small, but the lesson generalizes: **spec-authored SQL is not settled until the
   generator has run against it.** The comment in `tasks.sql` now says the alias is load-bearing so it
   is not "tidied" away later.
5. **Review found 0 high / 3 medium / 9 low, and the most consequential finding was documentation, not
   code.** The README told clients that a `seq` discontinuity signals a drop. But `seq` is
   `task_logs.id`, a **table-wide** `BIGSERIAL` shared by every task, so per-task values are ordered
   but not contiguous: any other task logging concurrently consumes ids. A client implementing "gap
   means re-backfill" would have re-paged on nearly every frame on a busy farm - a documentation
   sentence turning into a self-inflicted load amplifier in every consumer. **A wrong contract in docs
   is a real defect**, because consumers implement against the docs, not the code. What makes it
   instructive is *how it survived*: the code comments were correct about what `seq` is, and the
   README overreached one sentence beyond them. Prose written next to correct code can still be wrong,
   and nothing in a Go toolchain checks it. The README now states explicitly that a `seq` gap is not a
   drop signal and that the `dropped` frame and an unexpectedly closed stream are the only ones.
6. **A persist-failure log line, added as an improvement, was itself an unbounded agent-driven
   flood.** Un-swallowing the `_ =` on `AppendTaskLog` is straightforwardly better than silence, but
   the realistic non-stale failure does not happen once - it repeats for every chunk of a task.
   Non-UTF-8 subprocess output makes Postgres reject each insert with
   `invalid byte sequence for encoding "UTF8"`, so a single large binary stream produces roughly 32k
   serialized log writes **on the `Connect` recv goroutine**, in front of that worker's status,
   inventory and telemetry ingest. Fixed with `taskLogErrs`: one line per task per assignment epoch, a
   later epoch reporting again (a new generation is a new failure worth one line), and the retained set
   capped at 1024 task ids so a long-lived server cannot accumulate them. **Lesson: on a hot path
   shared with a live gRPC stream, even error logging needs a bound.** The bounded-sender invariant is
   usually read as being about sends, but the underlying property is "nothing on this goroutine may be
   driven unboundedly by a peer", and a `log.Printf` is driven by the peer just as much as a channel
   send is.
7. **The `test-integration` gate could not go green for any change, and nobody had noticed.**
   `internal/api`'s integration package alone runs ~320-340s against a 300s `-timeout`, so
   `make test-integration` was failing on a package timeout regardless of the diff. The conductor
   measured the baseline **both ways** to establish that it was pre-existing rather than a regression
   this iteration was masking: 338.8s with the new `internal/api` test removed, 321.6s with it
   present. The new test is therefore not the cause, and the bound was simply too tight for a suite
   where every test spins up its own real Postgres container. Raised to 900s with the reason recorded
   in the Makefile. **Lesson: a verification gate that has quietly been failing is worse than no gate,
   because its red is indistinguishable from a real red and the habit of absorbing it is exactly what
   makes a real failure invisible.** "The suite is red" must be diagnosed to a cause before any work
   is declared done, and a suspected pre-existing failure has to be *measured* both ways, not asserted.
8. **Process deviations, recorded honestly.** Two:
   - **Phase 4 again did not run the documented `relay-verify` workflow.**
     `.claude/workflows/relay-verify.js` needs an explicit opt-in to run a Workflow, which an
     unattended batch does not have, so the conductor substituted a direct `relay-code-reviewer`
     dispatch. Defensible, and this iteration did have real Go surface for the integration lane, but
     the honest statement is that this slice got a single-reviewer pass rather than the documented
     parallel fan-out across dimensions. This is now two iterations in a row. The fix is a documented
     unattended Phase 4 path in `docs/agent-team/README.md`, not per-run improvisation.
   - **The implementing agent was killed mid-run by an API connection error at task 2 of 9 and was
     resumed from its transcript with its context intact.** The resume worked and lost nothing: no
     work was redone, no file was left half-edited, and the remaining tasks proceeded in order. Worth
     recording as a positive, because long sequential iterations are exactly where a mid-run kill is
     likely, and knowing that transcript resume is a real recovery path (rather than "restart the
     iteration") changes how such a failure should be handled next time.
9. **A `-race` environment trap cost the conductor time.** Setting only `CC=/c/msys64/mingw64/bin/gcc.exe`
   without also putting `C:\msys64\mingw64\bin` on `PATH` produces
   `ThreadSanitizer failed to allocate ... (error code: 87)`. That reads as memory pressure, and it was
   initially misattributed to the concurrent Docker containers the integration suite was running.
   Already promoted to durable memory. The generalizable half: a toolchain error phrased as a resource
   failure is not evidence of a resource failure.

## Findings Triage

- **0 high.**
- **3 medium.** The README `seq`-gap contract (Problem #5) and the unbounded persist-failure logging
  (Problem #6) are two of them; all three were addressed.
- **9 low**, triaged and either fixed or accepted as minor.
- **Two pre-existing security properties filed rather than fixed**, because both are wider than this
  slice and fixing them inside it would have mixed a policy change into an enabler:
  `docs/backlog/bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero.md` (`handleTaskLog` fences
  on epoch but never checks sender identity, and `assignment_epoch` defaults to 0, so any enrolled
  agent can append forged lines to any never-claimed task - the fence is doing exactly the job it was
  designed for, and this is a missing *second* check it was never meant to provide) and
  `docs/backlog/idea-2026-08-09-sse-revoked-token-keeps-streaming.md` (bearer auth on `/v1/events` is
  checked once at connect, so a revoked token keeps receiving live content for the life of the held
  connection). Both are amplified by this work rather than introduced by it: before, a forged line
  reached the DB and surfaced on the next poll; now it is also fanned out live.

## Known Limitations

- **A `?job_id=&task_id=` subscription shares one 64-slot buffer across both event families.** One
  subscription is one channel, so a burst of log lines can drop-close the whole connection - including
  its status frames. That coupling is inherent to letting a page tail logs and watch job status over a
  single connection, which is precisely the shape the SPA job-detail view wants. Documented in the
  README with the recovery it implies: re-backfill the log **and** refetch job/task state, not just
  the log.
- **No `Last-Event-ID` / SSE `id:` resume.** Deliberately omitted: an `id:` the server does not honour
  is a trap that reads as a bug, and honouring it means the handler backfills from the DB, which is
  `?follow=1` re-entering through the side door. Recovery is the explicit `?since_seq=` recipe.
- **The broker is single-process.** A `task_log` event is visible only to clients connected to the
  `relay-server` process that owns that agent's gRPC stream. Status events already have exactly this
  limitation, but a live log view makes the symptom far more visible: behind a load balancer with more
  than one replica, live tailing silently degrades to "sometimes no live lines" while the polling
  endpoints stay correct. Cross-process fan-out is genuinely its own design (NOTIFY payloads cap at
  8000 bytes, so log content would not fit and would need a re-read by `seq`) and was proposed, not
  filed.
- **No SSE heartbeat and no cap on concurrent SSE subscriptions.** No frame is written on an idle
  stream, so proxies may reap idle `/v1/events` connections; and an authenticated client can open
  unbounded concurrent subscriptions, which the per-IP rate limiter does not bound because it is
  applied only to the auth routes. Both are pre-existing for status streams, so a log-only fix would
  leave the identical hole open next door. Proposed as one item each, not filed here.
- **The two pre-existing security items above are open**, not fixed by this slice.
- **`step_index` / `step_total` are still not exposed**, on either surface. Owned by
  `feature-2026-06-26-persist-expose-step-index-total`.
- **No consumer yet.** This is the backend enabler only.
  `feature-2026-06-26-task-log-view-sse-tailing` (SPA) and
  `idea-2026-05-09-mcp-live-task-log-streaming` (MCP) are now unblocked and both inherit the shape
  settled here.

## Improvement Goals

Carried forward from `2026-08-09-admin-console-shell-users-tab` (iteration 1 of this batch):

- **Treat the plan as an untrusted source of test design** - **honored, and immediately vindicated.**
  The engineer found and fixed five vacuous or broken plan-supplied tests mid-implementation
  (Problem #2). The goal was written after one instance in the previous iteration; the next iteration
  produced five. It is now promoted to durable memory.
- **Pair every absence assertion with a positive control on the same code path** - **honored.** Every
  negative assertion in this slice has one on the same channel or the same handler path: the
  `Filter{}`-receives-no-`task_log` test follows its `mustNotReceive` with a status event that must
  arrive; the epoch-gate integration test follows "a stale chunk is neither stored nor published" with
  a current-epoch chunk that must be both; the `taskLogPublishes` counter test (the
  "nothing is marshalled when nobody is tailing" guarantee) pairs its zero-count assertion with three
  watched chunks that must publish; and the `?task_id=` validation test pairs its 400 and 404 with a
  valid id that must reach 200 and start streaming. The reviewer verified the counter test's control
  specifically.
- **Verify a backlog item's technical claims against the code during spec, not implementation** -
  **honored**, and it paid for itself four times over (Problem #1).
- **Independently re-verify rather than trust subagent claims**
  ([[feedback_verify_tree_not_subagent_claims]]) - **honored, and the most load-bearing goal of the
  iteration.** The conductor re-ran the unit suite, `go vet`, `make vet-integration`,
  `-race -count=2` on `./internal/events/...`, and the full integration suite itself on the settled
  tree - and went further by measuring the `test-integration` timeout baseline **both ways** (338.8s
  without the new api test, 321.6s with it) to establish that the red gate was pre-existing rather
  than a regression the iteration was masking (Problem #7). Trusting the engineer's report here would
  have shipped on top of a gate nobody could pass.
- **Give the playbook an explicit unattended Phase 4 path** - **not honored, and now overdue.** The
  same substitution happened again (Problem #8). Still a doc change rather than a habit.

New goals from this iteration:

- **A concurrency test must fail fast, and must never take a lock on its failure path that the code
  under test could be holding.** A test that hangs is worse than one that is vacuous: it burns the
  whole suite budget and reports a package timeout that names nothing. When writing a test whose RED
  state is "a goroutine is parked inside the code under test", audit every `defer` in it for
  something that could block on the same lock. **Candidate for durable memory**, as a companion to
  the existing RED-proving note - proving RED is not enough if RED manifests as a hang.
- **A wrong contract in the docs is a defect, and the docs are not covered by any gate.** Consumers
  implement against the README, so a sentence that overreaches what the code guarantees ships a bug
  into every consumer. When a change adds or edits a client-facing contract, re-derive each claim from
  the code rather than from the adjacent comment - Problem #5 survived precisely because the code
  comments were right and only the prose was wrong. **Strong candidate for durable memory:** it is
  cheap to state, the failure is invisible to `make test`, and this instance would have caused
  re-backfill on nearly every frame in production.
- **On a hot path shared with a live stream, bound the error logging too.** Anything on that goroutine
  that a peer can drive - a send, an insert, or a `log.Printf` - is a peer-controlled cost. Adding
  visibility to a previously silent failure is right, but the increment has to be bounded per cause,
  not per occurrence. **Candidate for durable memory**, framed as a widening of the bounded-sender
  invariant from "sends" to "anything the peer drives".
- **Diagnose a red gate to a cause; never absorb it, and measure a suspected pre-existing failure
  both ways.** A verification gate that has quietly been failing is worse than no gate. If the suite
  is red, either it is this change or it is not, and the way to know is to run it with the change's
  new tests removed as well as present. **Strong candidate for durable memory**, and it generalizes
  past this one timeout to any "it was already broken" claim.
- **Run the generator against spec-authored SQL before treating the statement as settled.** `sqlc`'s
  analyzer rejects statements Postgres would accept, so a spec's SQL is a proposal until
  `make generate` has passed on it. Not a memory candidate on its own - it belongs as a line in the
  spec-writing habit for any change that touches `internal/store/query/`.

## Files Most Touched

- `internal/events/broker.go` - the structural heart of the change: `Filter`, the two indexes, and
  `removeLocked` as the single close-point. Grew from 66 lines to a file whose comments carry the
  reasoning, because every future edit here is one mistake away from a panic on a live agent's recv
  goroutine.
- `internal/events/broker_test.go` - the delivery matrix, both drop paths, the both-indexes cancel
  test, the non-blocking guarantee (Problem #3), and a concurrency test run under `-race -count=2`.
  The file where three of the five plan-supplied test problems were fixed.
- `internal/worker/handler.go` - `handleTaskLog` rewritten around the returned row, plus
  `taskLogEvent`, the `taskLogPublishes` counter, and the `taskLogErrs` limiter from Problem #6. The
  only file where a bug reaches an agent connection.
- `internal/api/events.go` - `?task_id=` validation ahead of the response headers, the `Filter`
  construction, and the `dropped` frame.
- `internal/store/query/tasks.sql` - the `fence`/`ins` CTE, with the alias that Problem #4 forced and
  a comment saying the alias is load-bearing.
- `internal/store/tasks.sql.go` - generated; regenerated under the CRLF discipline so the one real
  content change was not buried in whitespace-only hunks.
- `internal/api/events_task_log_integration_test.go` - the `gateWriter` with its recorded flush count
  (the barrier that replaced a sleep) and the bounded validation probes that replaced the 400s hang.
- `internal/worker/handler_tasklog_integration_test.go` and
  `internal/worker/handler_tasklog_e2e_integration_test.go` - the epoch gate on publish with its
  paired positive control, the no-subscriber-still-persists case, and the gapless/dedup backfill join.
- `internal/relayclient/client.go` - the scanner buffer, with the ~6x JSON-escape expansion reasoning
  written down so nobody shrinks it back on the assumption that escaping only doubles.
- `README.md` - the events section, and the file that carried the iteration's most consequential
  finding.
- `Makefile` - the `test-integration` timeout, and the comment that records why it is generous.

## Verification

- Unit suite green (`go test ./... -timeout 120s`), `go vet ./...` clean, `make vet-integration`
  clean. The last of these is not optional here: `make test` never compiles `//go:build integration`
  files, so the `Subscribe(string)` -> `Subscribe(Filter)` migration's break at
  `internal/scheduler/dispatch_test.go` was invisible to the unit suite.
- Race detector green on `./internal/events/...` with `-count=2`, run from Git Bash with
  `PATH="/c/msys64/mingw64/bin:$PATH" CC=/c/msys64/mingw64/bin/gcc.exe` (Problem #9).
- Full `make test-integration` green after the timeout fix, with the baseline measured both ways to
  prove the previous red was pre-existing (Problem #7).
- All of the above re-run by the conductor itself on the settled tree, not taken from the
  implementer's report.
- The plan's step-5 non-vacuity mutations were performed and each named test confirmed failing:
  deleting the `logSubs` cleanup in `removeLocked` produces `send on closed channel` from inside
  `Publish` (the exact production failure the task exists to prevent), suppressing the `subs` delete
  produces `close of closed channel` on the idempotent second cancel, and replacing the bounded send
  with a bare `ch <- e` fails the non-blocking test - after Problem #3's fix made it fail rather than
  hang.
- Code review: 0 high, 3 medium, 9 low, by a direct `relay-code-reviewer` dispatch rather than the
  documented `relay-verify` fan-out (Problem #8).
- **Invariants.** *Epoch fence* - strengthened in reach, unchanged in semantics; the publish is now
  downstream of the fenced insert's result, and no epoch is bumped anywhere. *One bounded sender per
  gRPC stream* - respected in both directions: `Publish` keeps its `select`/`default` bound, ingest
  adds no DB round trip, and Problem #6 extended the same reasoning to the error log. *No interior
  pointers across locks* - `Subscribe` returns a channel, `Filter` is passed and stored by value,
  `HasLogSubscriber` returns a `bool`, and no getter exposes `subs` or `logSubs`. *Identity-checked
  teardown* - not in play, though the filed epoch-0 bug is the log path's missing identity check.
  *Single job-spec pipeline* and *single JSON entry point* - not in play; `handleEvents` reads query
  parameters only, no request body.
