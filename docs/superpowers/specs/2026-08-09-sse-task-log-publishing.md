# Publish Task-Log Lines to the SSE Event Broker - Design

Date: 2026-08-09
Status: Draft (autonomous cycle; conductor review)

## Overview

`docs/backlog/feature-2026-06-26-sse-task-log-publishing.md` is the top of Now and
the shared backend prerequisite for two consumers that both assume a live log
source which does not exist:

- `docs/backlog/feature-2026-06-26-task-log-view-sse-tailing.md` (SPA full-screen
  task log + the job-detail log tab)
- `docs/backlog/idea-2026-05-09-mcp-live-task-log-streaming.md` (MCP live logs)

This spec is the **backend enabler only**. It settles the delivery shape that both
consumers inherit, because reversing that choice later means rewriting both.

Scope: `internal/events/`, `internal/api/events.go`, `internal/worker/handler.go`,
one sqlc query, one `internal/relayclient` hardening fix, and `README.md`. No SPA
code. No MCP code. No migration.

## The ingest path today, read precisely

### What happens to a log chunk

1. The agent's `chunkWriter` (`internal/agent/runner.go:278-300`) copies subprocess
   stdout/stderr into `TaskLogChunk` messages and enqueues them on the agent's
   single bounded `sendCh`.
2. The server's `Connect` recv loop dispatches them **synchronously on the recv
   goroutine**: `internal/worker/handler.go:117-128`, specifically
   `case *relayv1.AgentMessage_TaskLog: h.handleTaskLog(ctx, p.TaskLog)`
   (handler.go:120-121). There is no goroutine and no queue between the stream and
   the DB write.
3. `handleTaskLog` (`internal/worker/handler.go:508-526`) parses the task UUID,
   maps the stream enum to a string, and calls `AppendTaskLog`. The item's citation
   of `handler.go:509-526` is correct (the doc comment is on 508).
4. `AppendTaskLog` (`internal/store/query/tasks.sql:48-56`) is
   `INSERT ... SELECT ... WHERE EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND
   assignment_epoch = $4)`. It is declared `:exec`, so the generated method
   (`internal/store/tasks.sql.go:38`) returns only `error` - never the inserted
   row, never a rows-affected count.
5. `handleTaskLog` discards that error entirely: `_ = h.q.AppendTaskLog(...)`
   (handler.go:520). Nothing is published. Nothing is logged on failure.

Three consequences the design has to work with:

- **Ingest is on the critical path of the agent's whole inbound stream.** Because
  the recv loop is serialized (handler.go:108-129), any latency added inside
  `handleTaskLog` delays that worker's *status updates, inventory updates and
  telemetry* too, not just its logs.
- **The caller cannot currently tell whether the epoch fence rejected the insert**,
  because `:exec` collapses "inserted one row" and "inserted zero rows" into the
  same `nil` error.
- **A failed persist is silent today.** `_ =` swallows it.

### Where the chunk lands

`task_logs` (`internal/store/migrations/000001_initial.up.sql:76-82`):

```
id BIGSERIAL PK | task_id UUID | stream TEXT | content TEXT | created_at TIMESTAMPTZ
```

`stream` is constrained to `('stdout','stderr')` by
`internal/store/migrations/000019_status_vocabulary_checks.up.sql:25-27`. There is
no epoch column, no step column, no job column. `idx_task_logs_task_id_id` on
`(task_id, id)` (`000018_hot_path_indexes.up.sql:13-14`) backs the paging read.

### The read path today

`GET /v1/tasks/{id}/logs` (`internal/api/tasks.go:63-137`, registered
`auth(...)` at `internal/api/server.go:124`) is polling-only: `?limit` 1..200
(default 50) and `?since_seq`, served by `GetTaskLogsPage`
(`internal/store/query/tasks.sql:159-165`, `WHERE task_id = $1 AND id > $2 ORDER BY
id LIMIT $3`) plus `CountTaskLogs` (tasks.sql:167-168). The response is
`{ items, next_seq, total }` where each item is `logEntry`
(`internal/api/tasks.go:56-61`):

```
{ "seq": <task_logs.id>, "stream": "...", "content": "...", "created_at": "..." }
```

`next_seq` is 0 when the page drained (tasks.go:128-130).

### The event broker today

`internal/events/broker.go` in full is 66 lines:

- `Event{Type, JobID, Data}` (broker.go:9-13). `Type` is one of the literals
  `"task"`, `"job"`, `"worker"`; `Data` is pre-marshalled JSON.
- `Broker{mu sync.Mutex, subs map[chan Event]string}` (broker.go:16-19) - one map
  from subscriber channel to its job-id filter.
- `Subscribe(jobID string)` returns a **64-buffered** channel plus a cancel
  (broker.go:31-44). The documented contract (broker.go:26-30): "if the buffer
  fills, the broker unsubscribes and closes the channel - consumers should treat
  channel close as 'you fell behind, reconnect if you need more'".
- `Publish(e Event)` (broker.go:48-66) holds `b.mu`, iterates all subscribers,
  delivers where `filter == "" || filter == e.JobID`, and on a full buffer does a
  `default:` branch that closes and removes the subscriber. **It never blocks on a
  subscriber.** `""` receives everything.

Publishers today: worker online (handler.go:346-349), task status + job status
(handler.go:488-500), worker offline (handler.go:588-591).

`GET /v1/events` (`internal/api/events.go:9-40`, registered `auth(...)` at
`internal/api/server.go:171`) reads `?job_id`, subscribes, and loops writing
`event: <Type>\ndata: <Data>\n\n` with a `Flush` per frame. On `!ok` (dropped by
the broker) it returns silently. `?job_id` is **not parsed or validated** - an
unknown job id yields a permanently empty stream.

The broker is **in-process only**. `internal/scheduler/notify.go:117-120` LISTENs
on `relay_task_submitted` and `relay_task_completed`, which are dispatcher wakeups;
no event fan-out crosses processes for status events today, and logs will inherit
that.

### Where the backlog item disagrees with the code

The code wins on all four points; this spec is written to the code.

1. The item says "publish ... keyed by job id (and task index)". **There is no task
   index.** `tasks` (`000001_initial.up.sql:51-66`) has `id`, `job_id`, `name` and
   no ordinal column. The SPA's `/jobs/:id/tasks/:n` `n` is a position in the
   client's task list, not a server field. The filter key is the **task UUID**.
2. The item implies `handleTaskLog` has the job id in hand. **It does not.**
   `TaskLogChunk` (`proto/relayv1/relay.proto:79-86`) carries only `task_id`.
   Publishing a job-keyed event therefore requires either an extra DB read on the
   hot recv path or getting `job_id` out of the same statement. This spec does the
   latter.
3. The item's "bound the publish so a slow SSE subscriber cannot block
   `handleTaskLog`" reads as new work. **`Broker.Publish` is already non-blocking
   by construction** (broker.go:54-59). The real risk is not blocking; it is
   per-chunk cost and cross-subscriber amplification. See "The non-blocking
   guarantee".
4. The item's sibling says the design handoff's `?follow=1` "does not exist". True,
   but the hi-fi Holo - the authoritative layer - does not ask for it: `HoloTaskLog`
   captions its stream `/v1/events?task_id={id} · single-task stream`
   (`design_handoff_relay_holo/hifi3-holo-pages.jsx:2740`). The `?follow=1` line is
   from the structure-only reference README.

## Decision: extend `GET /v1/events` with `?task_id=`

**Chosen shape**

```
GET /v1/events?task_id=<task-uuid>            -> event: task_log frames for that task
GET /v1/events?job_id=<job-uuid>&task_id=<t>  -> that job's status + that task's logs
GET /v1/events?job_id=<job-uuid>              -> unchanged (status only)
GET /v1/events                                -> unchanged (all status, no logs)
```

New event type `task_log`. **Log events are opt-in and per-task**: they are
delivered only to a subscription that named that exact `task_id`. There is no
job-wide or global log firehose in v1.

**Rejected: `GET /v1/tasks/{id}/logs?follow=1`.**

### Why

1. **A global subscriber must not become a cluster-wide log firehose.** This is
   the decisive constraint and it is a property of the existing broker, not a
   preference. `Publish` delivers to every subscriber whose filter is `""`
   (broker.go:53). If log events were published under the current filter model, a
   plain `GET /v1/events` subscriber - which `relay watch` and any future SPA app
   shell opens - would receive *every log line of every task on the cluster*,
   overrun its 64-slot buffer, and be closed. That would break status delivery for
   existing clients. Any shape at all therefore needs a broker filter change; once
   the filter is task-aware, `?task_id=` on the existing endpoint is nearly free,
   whereas `?follow=1` needs the same broker change *plus* a second streaming
   handler.
2. **Per-task filtering is server-side, so the SPA never receives another task's
   lines.** A `?task_id=` subscription is scoped in the broker, not filtered in the
   browser. Client-side filtering of a job-scoped stream was the other candidate in
   the SPA item; it is strictly worse (a 500-task job would push 500 tasks' output
   down one socket for the SPA to discard).
3. **Connection count stays at one per view, and one per page even when a view
   needs both.** `?job_id=&task_id=` on a single subscription carries the job's
   status events (so the header's LIVE/DONE state updates) and the selected task's
   logs. The job-detail log tab adds zero connections beyond the one the page
   already wants. With `?follow=1` a page that needs status *and* logs needs two
   SSE connections, doubling the held-connection count for the same screen.
4. **One streaming surface, one auth path, one client helper.** `handleEvents` is
   the only SSE handler in the codebase, and `relayclient.StreamEvents`
   (`internal/relayclient/client.go:117-161`) is already generic over the path and
   already sets `Accept: text/event-stream` and the bearer header. The CLI already
   consumes `/v1/events`. `?follow=1` would make one endpoint return either
   `application/json` or `text/event-stream` depending on a query parameter, and
   would need its own flusher, drop policy, and reconnect story.
5. **MCP can consume it, and is not constrained the way LISTEN/NOTIFY constrained
   it.** The concern raised in the task framing is real for LISTEN/NOTIFY (the MCP
   server holds only a `relayclient`, no `*pgxpool.Pool`) but does not bite here:
   `?task_id=` is plain authenticated HTTP SSE over the client the MCP server
   already has. This is neutral between the two options - both are HTTP - so it is
   not why `?follow=1` lost; it is recorded to close the question.
6. **The hi-fi Holo already specifies this URL** (hifi3-holo-pages.jsx:2740), so
   the chosen shape needs no handoff deviation note in the consumer item.

**What `?follow=1` would have bought, and why the payload makes it unnecessary:** a
single connection that serves history and then live output, with the gapless
handoff done server-side. Because the event payload carries `seq` (the
`task_logs.id` BIGSERIAL, the same value `GET /v1/tasks/{id}/logs` returns), a
client gets an exactly-gapless, exactly-deduped join with three lines of logic
(below). That was `?follow=1`'s only real advantage and `seq` removes it.

### The backfill contract (must be documented, in this order)

1. Open the SSE subscription **first** and start buffering `task_log` events.
2. Then page `GET /v1/tasks/{id}/logs?since_seq=0` (repeat with
   `since_seq=next_seq` until `next_seq == 0`). Record `maxSeq`.
3. Render the backfill, then apply buffered and subsequent events, **discarding any
   event with `seq <= maxSeq`**.

Subscribe-then-read is load-bearing; reversing it leaves a hole between the last
page and the first event. This is safe because, per task, inserts are serialized on
one recv goroutine (handler.go:108-129), each in its own implicit transaction,
committed before the publish - so every `seq` a subscriber sees is already visible
to a subsequent `GetTaskLogsPage` read, and `seq` increases monotonically in publish
order. A retry runs under a new epoch, and the old epoch is fenced out, so two
generations never interleave.

### Restart, reconnect, and multi-process behaviour

- **Server restart:** the broker is in-memory; all subscriptions die with the
  process. Browser `EventSource` reconnects automatically; a Go client sees the
  stream end. On reconnect the client re-runs step 2 with `since_seq=<last seen>`
  and converges. No server-side resume state, no `Last-Event-ID` support (see
  Omissions).
- **Subscriber falls behind:** the broker closes its channel (broker.go:56-65).
  See the `dropped` frame below.
- **Multi-process:** a `task_log` event is only visible to subscribers connected to
  the *same* `relay-server` process that owns that agent's gRPC stream. Status
  events already have exactly this limitation and it is unchanged by this work.
  Behind a load balancer with more than one replica, live tailing silently degrades
  to "sometimes no live lines" while the polling endpoint stays correct. Recorded
  as a known limitation, not fixed here; the fix is a cross-process event fan-out
  (a `relay_events` NOTIFY channel or a shared bus), which is its own item.

## The non-blocking guarantee

Held against the CLAUDE.md invariant "one bounded sender per gRPC stream: sends
from other goroutines must be bounded - a peer that stops reading must never block
a dispatcher or HTTP handler indefinitely". Here the exposure runs the other way -
an HTTP subscriber must never block the gRPC recv loop - and the same rule applies.

**Requirements:**

1. **The publish call must never block.** `Broker.Publish` already satisfies this:
   the send is inside `select { case ch <- e: default: }` (broker.go:54-59), so a
   subscriber that has stopped reading costs one failed send and is then closed and
   removed. The new log path must use exactly this mechanism. It must not
   introduce a blocking send, a `time.After` fallback, or an unbounded queue.
2. **`handleTaskLog` must not add a DB round trip.** Because the recv loop is
   serialized (handler.go:117-128), an extra query per chunk would slow that
   worker's status and telemetry ingest as well. The job id needed for the event
   payload therefore comes from the *same* statement as the insert, not from a
   second `GetTask`.
3. **Nothing is marshalled or published when nobody is listening.** Steady state -
   no one tailing - must cost one uncontended mutex acquire and a map lookup, not a
   JSON encode per chunk. `Broker` gains `HasLogSubscriber(taskID string) bool`
   backed by an O(1) map lookup, and `handleTaskLog` guards the marshal+publish on
   it. Racing is benign: a false negative drops at most the chunks in flight while
   a subscriber was mid-`Subscribe`, and that subscriber's backfill covers them.
4. **A log publish must not slow status delivery.** `Publish` holds one global
   mutex and iterates *all* subscribers. Log chunks raise publish frequency by
   orders of magnitude, so iterating unrelated subscribers per chunk is the wrong
   shape. The broker keeps a second index, `logSubs map[string]map[chan Event]struct{}`
   keyed by task id; a `task_log` publish iterates only `logSubs[e.TaskID]`. Status
   publishes continue to iterate `subs` only. A `?job_id=&task_id=` subscription is
   registered in both maps; cancel and drop-close remove it from both and must be
   idempotent (guard on presence before `close`, as broker.go:38-42 already does).

**Acceptable failure mode:** log lines may be dropped by the broker. When a
subscriber cannot keep up, its channel is closed and its HTTP response ends - we do
**not** buffer harder and we do **not** block. That is acceptable *only* because the
DB remains the source of truth: every line the subscriber missed is still readable
via `GET /v1/tasks/{id}/logs?since_seq=`, and `seq` makes the resume point exact.

**Persistence is unconditional and strictly precedes any publish.** The publish is
derived from the insert's returned row, so there is no path where a line is
published but not stored. The converse (stored but not published) is the acceptable
direction. One honest caveat: persistence is unconditional *as attempted* - if the
INSERT itself errors, today's `_ =` hides it (handler.go:520). This spec requires
that error to be logged (once per failure, at the existing `log.Printf` style used
throughout `handler.go`) so a persist failure stops being silent; it does not add
retry.

**Gap signalling - new `dropped` frame.** Today when the broker closes a channel,
`handleEvents` just returns (events.go:29-32) and the response ends cleanly. A Go
consumer sees `StreamEvents` return `nil` - indistinguishable from a normal end of
stream. Before returning on `!ok`, the handler must write and flush one final frame:

```
event: dropped
data: {"reason":"slow_consumer"}
```

This is additive and safe for existing clients: `relayclient` surfaces it as
`SSEEvent{Type:"dropped"}` to handlers that switch on type and ignore unknowns, and
browser `EventSource` listeners are registered per event name. It gives both future
consumers an unambiguous "re-backfill from your last seq" signal, and it
distinguishes "you fell behind" from "the client went away"
(`r.Context().Done()`, events.go:27-28), which the handler already tells apart.

## The epoch fence

`task_logs` writes are already fenced (`tasks.sql:48-56`). Verified, not assumed:

- **A publish is not a status write.** It touches neither `tasks.status` nor
  `task_logs`, so the invariant's letter is untouched and no epoch bump is
  involved. The fenced write is the same single write as today.
- **But a stale-epoch line could reach a subscriber if the publish were
  unconditional**, and that would actively mislead a live view: a zombie agent from
  a previous assignment can still be streaming output for a task that has since
  been requeued to a different worker (`RequeueWorkerTasks` bumps the epoch -
  `tasks.sql:183-187`) or cancelled (`CancelJobTasks` bumps it - `tasks.sql:170-181`).
  Those lines are correctly *not stored* today, so a live view that showed them
  would disagree with its own backfill after a refresh.
- **Therefore the publish must be gated on the insert having actually happened.**
  This is not achievable with the current `:exec` signature. `AppendTaskLog` becomes
  a single statement that returns the inserted row plus the job id, and returns
  zero rows when the fence rejects:

```sql
-- name: AppendTaskLog :one
WITH fence AS (
    SELECT id, job_id FROM tasks WHERE id = $1 AND assignment_epoch = $4
), ins AS (
    INSERT INTO task_logs (task_id, stream, content)
    SELECT $1, $2, $3 FROM fence
    RETURNING id, created_at
)
SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence;
```

Same single round trip, same fence semantics, now observable. On the fence-rejected
path sqlc returns `pgx.ErrNoRows`, which `handleTaskLog` treats as "stale chunk,
drop silently" (today's behaviour) and distinguishes from a real DB error (which it
logs). **Net effect: the fence's reach is extended to the publish path, not
changed.**

Consequences the planner must carry:

- Only caller: `internal/worker/handler.go:520`.
- `internal/store/store_test.go:241-280` (`TestAppendTaskLog_EpochGuarded`)
  currently asserts `err == nil` on the stale insert. It must be updated to assert
  `pgx.ErrNoRows` - a strictly stronger assertion of the same property.
- Regenerate with `make generate`, then apply the CRLF discipline from CLAUDE.md
  (`git diff --ignore-all-space`, revert LF-only hunks).
- **No migration.** `id` and `created_at` already exist.
- Rejected alternative: keep `AppendTaskLog :exec` and add a second
  `AppendTaskLogReturning`. That leaves two insert paths into `task_logs`, one of
  which cannot publish - the same class of parallel-path mistake the Invariants
  exist to prevent.

## Payload shape

```
event: task_log
data: {"task_id":"<uuid>","job_id":"<uuid>","seq":1234,"stream":"stdout","content":"...","created_at":"2026-08-09T14:36:25.123Z"}
```

`seq`, `stream`, `content`, `created_at` are **field-identical to `logEntry`**
(`internal/api/tasks.go:56-61`), so a consumer can define one log-line type and
merge SSE frames with polling pages without a translation layer. `task_id` and
`job_id` are added because a `?task_id=`-only subscriber knows the task but should
not need a second request to route or cache-key by job.

Every field is available at publish time: `task_id` from the chunk
(relay.proto:80), `stream` from `handleTaskLog`'s existing mapping
(handler.go:515-518), `content` from `chunk.Content`, and `seq` / `created_at` /
`job_id` from the query above.

**Ordering and dedupe against the polling endpoint:** `seq` is `task_logs.id`
(BIGSERIAL), the same value `GET /v1/tasks/{id}/logs` returns as `seq` and pages by
via `since_seq` (`tasks.sql:159-165`). Total order per task, exact dedupe, and a
`seq` discontinuity is itself a "you missed lines" signal independent of the
`dropped` frame.

**Encoding.** The payload must be produced with `json.Marshal` (or `%q` for the
string fields), never raw concatenation. `handleEvents` re-prefixes literal
newlines in `Data` with `data: ` (events.go:35); JSON escapes `\n` inside strings,
so a correctly marshalled payload contains no literal newline and each event stays
one `data:` line. Hand-rolled framing here would corrupt SSE for any multi-line
chunk - which is nearly every chunk.

**Required client hardening (in scope).** `relayclient.StreamEvents` reads with a
default `bufio.Scanner` (`internal/relayclient/client.go:141`), whose token limit is
64 KiB. Agent chunks are up to ~32 KiB (`os/exec`'s copy buffer feeding
`chunkWriter`, runner.go:285-300), and JSON escaping can nearly double that, so a
worst-case chunk exceeds the limit and `StreamEvents` fails with
`bufio.Scanner: token too long`. Status payloads are tiny, so this has never bitten.
Fix: `scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)`. This is shared-client
hardening required for *any* Go consumer of log frames, so it lands here rather than
in the MCP item.

**`step_index` / `step_total`: explicitly out of scope.** `TaskLogChunk` carries
them (relay.proto:84-85) and `handleTaskLog` drops them at persist. Exposing them
only on the SSE path would create a field the backfill path cannot supply and break
the payload symmetry that makes the merge trivial. They belong to
`docs/backlog/feature-2026-06-26-persist-expose-step-index-total.md`, which adds the
columns under the fence and exposes them on `logEntry`; both surfaces then gain them
together for free, since this design derives the event payload from the same row.

## Existing status events must be unaffected

Guaranteed structurally, not by care:

- The status delivery rule is **byte-for-byte unchanged** for subscriptions with no
  `task_id`. The full delivery matrix:

  | `job_id` | `task_id` | receives |
  |---|---|---|
  | `""` | `""` | all status events (today's behaviour) |
  | `J` | `""` | status events for `J` (today's behaviour) |
  | `""` | `T` | `task_log` events for `T` only |
  | `J` | `T` | status events for `J` **plus** `task_log` events for `T` |

  Rule: a `task_log` event goes only to subscriptions with `TaskID == e.TaskID`; a
  status event goes to subscriptions where `JobID == e.JobID`, or where `JobID == ""`
  **and** `TaskID == ""`. The last clause is what stops `?task_id=` alone from
  becoming an accidental global status subscription.
- `task_log` events are not placed in the `subs` map at all, so a status-only
  subscriber's channel cannot be filled by log traffic and therefore cannot be
  drop-closed by it.
- No existing publish site changes. `Event` gains a `TaskID` field, which is the
  zero value for all four existing publishers (handler.go:346, 488, 495, 588).
- `Subscribe` changes from `Subscribe(jobID string)` to `Subscribe(events.Filter)`.
  Non-test callers: `internal/api/events.go:15` only. Test callers:
  `internal/events/broker_test.go` (9 sites) and
  `internal/scheduler/dispatch_test.go:569`. One entry point is kept deliberately -
  adding a parallel `SubscribeFilter` would leave two subscription paths with
  divergent filter semantics.
- `README.md:1299-1305` is updated with `?task_id=`, the `task_log` and `dropped`
  event types, and the three-step backfill contract.

Tested by: the existing broker tests (ported to `Filter`) plus new tests asserting a
`""`/`""` subscriber receives **zero** `task_log` events, and that a status
subscriber still receives all 65 events while a log subscriber is being drop-closed
(the shape of `TestBroker_HealthySubscriberUnaffectedByDrop`, broker_test.go:98-125).

## Scope boundary

**In scope**

- `internal/events/broker.go`: `Event.TaskID`, `Filter`, `Subscribe(Filter)`,
  task-keyed `logSubs` index, `HasLogSubscriber`, `events.TypeTaskLog` constant.
- `internal/api/events.go`: parse and validate `?task_id=`, build the `Filter`,
  emit the `dropped` frame on broker close.
- `internal/store/query/tasks.sql`: `AppendTaskLog` -> `:one` with the CTE above;
  regenerate.
- `internal/worker/handler.go`: `handleTaskLog` consumes the returned row, logs
  real DB errors, and publishes behind `HasLogSubscriber`.
- `internal/relayclient/client.go`: scanner buffer.
- `internal/store/store_test.go`: strengthen `TestAppendTaskLog_EpochGuarded`.
- `README.md`: events section.

**Explicitly out of scope**

- Any `web/` change. The SPA hook, the `HoloTaskLog` view and the job-detail log tab
  stay in `feature-2026-06-26-task-log-view-sse-tailing`.
- Any `internal/mcp/` change. `idea-2026-05-09-mcp-live-task-log-streaming` stays
  open; this spec only makes it possible and records that `relayclient` is the
  consumption path.
- `step_index` / `step_total` persistence and exposure.
- A `?follow=1` endpoint, in any form.
- `Last-Event-ID` / SSE `id:` support, SSE heartbeats, cross-process fan-out, and a
  concurrent-SSE-connection cap (see Omissions).
- Log retention, truncation, or a download endpoint.

## Omitted deliberately

| Candidate | Why omitted | Worth its own item? |
|---|---|---|
| SSE `id: <seq>` + `Last-Event-ID` resume | `EventSource` would then send `Last-Event-ID` on reconnect and the server would silently ignore it - an `id:` the server does not honour is a trap that reads as a bug. Honouring it means the handler backfills from the DB, which is `?follow=1` re-entering through the side door. Explicit `since_seq` backfill is unambiguous. | Only if we later decide the server should own the handoff. |
| SSE heartbeat / `:keepalive` comment frames | No SSE frame is written on an idle stream today, so proxies and load balancers may reap idle `/v1/events` connections. Pre-existing for status streams; log streams are chattier, so *less* exposed. Fixing it means touching the status path's timing behaviour. | **Yes - proposed, not filed.** One item covering `/v1/events` keepalive for both event families. |
| Larger channel buffer for log subscriptions (e.g. 256) | Rejected. 64 slots x ~32 KiB chunks already absorbs ~2 MiB of burst per subscriber; 4x-ing it 4x-es worst-case retained memory per subscriber across an uncapped number of subscribers, to buy a lower drop rate for a drop that is fully recoverable via `seq`. Keeping 64 also keeps one buffer policy instead of two. | No. |
| Cap on concurrent SSE subscriptions | Real DoS shape, but `/v1/events` is already unbounded for status subscribers, so a log-only cap leaves the identical hole open next door. The correct fix is one global authenticated-SSE concurrency cap covering both. | **Yes - proposed, not filed.** |
| Cross-process event fan-out | Live tailing (and status events) only work on the process holding the agent stream. Needs a `relay_events` NOTIFY channel or a bus, plus a payload-size story (NOTIFY payloads cap at 8000 bytes, so log content would not fit and would need a re-read by seq). Genuinely its own design. | **Yes - proposed, not filed.** Blocks multi-replica deployment generally, not just this feature. |
| Job-wide log subscription (`?job_id=` yields all its tasks' logs) | No consumer wants it: both the full-screen view and the job-detail tab show one selected task. It is the exact amplification shape argument 1 rejects. | No. Add it only when a real consumer needs it. |
| `epoch` in the payload | Available on the chunk, but the fence already guarantees only current-generation lines are stored *and* published, so it carries no information a consumer can act on - and `task_logs` has no epoch column, so the polling path could never match it, breaking payload symmetry. | No. |
| Task index / task name in the payload | There is no task ordinal column (`000001_initial.up.sql:51-66`), and a `?task_id=` subscriber already knows which task it asked for. | No. |
| `LOG_STREAM_PREPARE` as a distinct `stream` value | `handleTaskLog` maps anything that is not `LOG_STREAM_STDERR` to `"stdout"` (handler.go:515-518), and the DB CHECK only allows `stdout`/`stderr` (`000019:25-27`). So prepare-phase output is already labelled `stdout` on the polling path; the SSE payload matches. Changing it is a migration plus a constraint change plus a consumer contract change. | Adjacent to `idea-2026-04-26-remove-synthetic-step-marker`; not filed separately. |
| Retrying or queueing a failed `AppendTaskLog` | Out of scope; this spec only makes the failure visible in logs instead of silent. | Low value; not filed. |

## Security and system-design

**Authorization.** `?task_id=` grants **exactly** what already-shipped endpoints
grant, and no more. Verified: `GET /v1/events` is registered `auth(...)` with no
admin gate and no ownership check (`internal/api/server.go:171`,
`internal/api/events.go:9-40` - it does not even look at the caller);
`GET /v1/tasks/{id}/logs` is likewise `auth(...)`-only with no ownership check
(`server.go:124`, `tasks.go:63-79`); `handleGetJob` has no ownership check either
(`jobs.go:632-673`). The only per-owner gate in `jobs.go` is on the *mutation*
`handleCancelJob` (`jobs.go:712`: `if !u.IsAdmin && job.SubmittedBy != u.ID`). So
relay's current model is: **any authenticated user can read any job, task, and task
log.** Adding a live view of data that is already readable by the same token
introduces no escalation. Decision: match the existing model, do not add a
per-task ownership gate here. Tightening cross-tenant read access is a separate,
larger policy change that must land on the polling endpoint at the same time or it
accomplishes nothing.

**Input validation.** `?task_id=` is parsed with `parseUUID` -> 400 on malformed
input, and checked for existence with one `GetTask` at subscribe time -> 404 for an
unknown task. That is one query per *connection*, not per chunk, and it prevents the
worst UX failure of this feature: a typo'd id yielding a stream that hangs open
forever, silently, looking like "the task has no output". `?job_id=` behaviour is
deliberately left unchanged (unvalidated, silently empty for an unknown job) - it is
an existing contract with existing clients; the asymmetry is intentional and
documented in the README change.

**Log content is sensitive.** `content` is raw subprocess stdout/stderr: it can
contain P4 paths, hostnames, env-derived values, and anything a job's command
prints, including secrets a user's own script echoes. Two consequences: (a) the
transport must stay the authenticated same-origin `/v1/events` path - no token in a
query string, no relaxed CORS (`internal/api/cors.go` is fail-closed and wildcard
`*` is rejected; unchanged here); (b) the server must not log chunk *content* when
it logs a persist failure - log the task id and the error only. Note that `content`
is already returned verbatim by `GET /v1/tasks/{id}/logs`, so this feature widens no
exposure; it only makes the same bytes arrive sooner.

**Load and connection count.** One SSE subscription = one held HTTP connection, one
handler goroutine, one 64-slot channel (~2 MiB worst case with 32 KiB chunks). Both
consumer surfaces are designed to hold one subscription per open view, and the
`?job_id=&task_id=` combination means the job-detail page needs no second
connection. Fan-out per chunk is O(subscribers of that task) because of the
task-keyed index, so a chatty task with one viewer costs one non-blocking channel
send per chunk on top of the DB insert that already happens. Steady state with
nobody tailing is one map lookup per chunk (`HasLogSubscriber`) and no allocation.

**DoS shapes, named.**

- *Many subscribers.* An authenticated client can open unbounded concurrent SSE
  connections. Pre-existing for `/v1/events`; not fixed here; proposed as one
  global cap item (Omissions). Note the per-IP rate limiter
  (`internal/api/ratelimit.go`) is only applied to the auth routes
  (`server.go:84,91`), so it does not bound this.
- *Huge log volume.* A task that prints megabytes per second cannot amplify beyond
  the number of subscribers on that task, and each subscriber is drop-closed the
  moment it falls behind rather than accumulating memory. The DB insert is already
  the rate limiter on ingest and is unchanged.
- *Amplification via a global subscription.* Closed by construction: the delivery
  matrix means `""`/`""` receives no log events at all.

**Invariants check.**

- *Epoch fence* - strengthened, not bypassed: the publish is now downstream of the
  fenced insert's result. No new write to `tasks.status` or `task_logs`.
- *Single job-spec pipeline* - untouched.
- *One bounded sender per gRPC stream* - respected in both directions: publish is
  non-blocking, and no DB round trip is added to the recv loop that would delay the
  worker's status or telemetry ingest.
- *Identity-checked teardown* - untouched; no worker registry state involved.
- *No interior pointers across locks* - `Subscribe` returns a channel (not a
  pointer into broker state) and `Filter` is passed by value. `Event.Data` is a
  shared read-only slice, as today.
- *Single JSON entry point* - untouched; no request body is read.

## Testing

Unit (`make test`, no Docker):

1. `Broker`: a `Filter{}` subscriber receives every status event and **zero**
   `task_log` events.
2. `Broker`: `Filter{TaskID: "t1"}` receives `t1`'s log events and no `t2` log
   events and no status events.
3. `Broker`: `Filter{JobID: "j1", TaskID: "t1"}` receives `j1` status events **and**
   `t1` log events, and neither `j2` status nor `t2` logs.
4. `Broker`: a log subscriber that never reads is drop-closed after its buffer
   fills, while a concurrent status subscriber receives all its events (port of
   broker_test.go:98-125).
5. `Broker`: `Publish` of a `task_log` with 1 log subscriber and N status
   subscribers delivers to exactly 1 channel (asserts the task-keyed index, i.e.
   that status subscribers are not even considered).
6. `Broker`: `HasLogSubscriber` is false with no subscribers, true after a
   task-scoped subscribe, false again after cancel; a `Filter{JobID:...}`
   subscription does not make it true.
7. `Broker`: cancelling a `{JobID, TaskID}` subscription removes it from both
   indexes - a subsequent status publish and log publish both find no subscriber
   and do not panic on a closed channel.
8. **Non-blocking guarantee, directly:** with a task-scoped subscriber whose buffer
   is full and which is never read, N `Publish` calls from the caller's goroutine
   all return; the test fails on a timeout rather than deadlocking. Assert the
   subscriber's channel ends up closed.
9. `handleEvents`: `?task_id=` with a malformed UUID -> 400; unknown task -> 404;
   valid task -> 200 with `Content-Type: text/event-stream` (extend
   `TestSSESubscribe`, api_test.go:163-182).
10. `handleEvents`: on broker close the response's final frame is
    `event: dropped` / `data: {"reason":"slow_consumer"}`; on client-context
    cancellation no `dropped` frame is written.
11. `handleEvents`: a `task_log` event with multi-line `content` produces exactly
    one `data:` line and the frame round-trips through `relayclient`'s parser back
    to the original content, newlines intact.
12. `relayclient.StreamEvents`: a single `data:` line of 512 KiB is parsed without
    `token too long` (RED before the scanner-buffer change).
13. Payload contract: the marshalled event's field names and JSON types match
    `logEntry` (tasks.go:56-61) field-for-field for `seq`/`stream`/`content`/
    `created_at`, plus `task_id` and `job_id`.

Integration (`make test-integration`, `-p 1`):

14. `TestAppendTaskLog_EpochGuarded` (store_test.go:241-280) updated: matching epoch
    returns a row whose `job_id` is the task's job and whose `id` is monotonically
    increasing across calls; mismatched epoch returns `pgx.ErrNoRows` and inserts
    nothing.
15. End-to-end via the worker handler: with a task-scoped subscriber attached, a
    `TaskLogChunk` at the current epoch produces one `task_log` event whose `seq`
    equals the row that `GetTaskLogsPage` then returns; a chunk at a **stale** epoch
    produces **no** event and no row.
16. Backfill join has no gap and no duplicate: subscribe, emit chunks continuously,
    page `?since_seq=0` to drain, then apply buffered events with `seq > maxSeq` -
    the reconstructed sequence equals `GetTaskLogs` for that task exactly.
17. With no subscriber attached, ingesting chunks still persists every row (the
    `HasLogSubscriber` fast path cannot skip the write).

## Acceptance criteria

1. `GET /v1/events?task_id=<uuid>` streams `event: task_log` frames for that task
   only, starting from chunks that arrive after the subscription.
2. `GET /v1/events` and `GET /v1/events?job_id=` behave exactly as before and
   receive **no** `task_log` frames; the four-row delivery matrix above is
   implemented and tested.
3. `GET /v1/events?job_id=J&task_id=T` delivers `J`'s status events and `T`'s log
   events on one connection.
4. The `task_log` payload is
   `{task_id, job_id, seq, stream, content, created_at}`, with
   `seq`/`stream`/`content`/`created_at` identical in name and type to the polling
   endpoint's `logEntry`.
5. A chunk whose epoch does not match `tasks.assignment_epoch` is neither persisted
   nor published (verified by an integration test that asserts both).
6. `AppendTaskLog` is a single statement returning `id`, `created_at`, `job_id`;
   `handleTaskLog` performs no additional query per chunk; a real DB error is
   logged (without chunk content) and `pgx.ErrNoRows` is treated as a silent stale
   drop.
7. `Publish` never blocks on a task-scoped subscriber that has stopped reading; the
   subscriber is closed and the publisher returns. Covered by test 8, which fails
   by timeout rather than hanging the suite.
8. With no log subscriber for a task, ingest performs no JSON marshal and no
   publish, and still persists every chunk.
9. A dropped subscriber receives a final `event: dropped` frame before the response
   ends; a client that disconnects does not.
10. `relayclient.StreamEvents` parses a 512 KiB `data:` line.
11. `?task_id=` returns 400 for a malformed UUID and 404 for an unknown task;
    `?job_id=` validation behaviour is unchanged.
12. `README.md`'s events section documents `?task_id=`, both new event types, and
    the subscribe-then-backfill contract including its ordering requirement.
13. No file under `web/` or `internal/mcp/` changes. No migration is added. No
    `?follow=1` endpoint exists.
14. `make test` and `make test-integration` are green.

## Risks

- **The `Subscribe` signature change fans out into three test files** (`broker_test.go`
  9 sites, `dispatch_test.go:569`, plus `events.go`). Mechanical, but it is the
  change most likely to produce an unrelated-looking diff; keep it in its own commit
  ahead of the behavioural work.
- **Biggest risk for the planner: the two-index broker.** A channel that lives in
  both `subs` and `logSubs` must be closed exactly once and removed from both maps
  on cancel and on drop, under the single mutex. A double `close` panics; a partial
  removal leaks a send on a closed channel, which also panics - inside the recv loop
  of a live agent connection. Order the plan so the broker lands first with tests 1-8
  green (including the both-indexes cancel test) before `handleTaskLog` is touched,
  and require the race detector on `./internal/events/...` (per MEMORY: `-race` needs
  `CC=/c/msys64/mingw64/bin/gcc.exe`).
- **`make generate` rewrites line endings repo-wide.** Apply the CLAUDE.md CRLF
  discipline or the diff will bury the one real change.
- **Payload symmetry is load-bearing and easy to erode.** The moment someone adds a
  field to the SSE payload that the polling endpoint cannot produce, the consumers'
  merge logic needs two types. `step_index`/`step_total` is the standing temptation;
  it stays in its own item for exactly this reason.
- **Multi-replica silent degradation.** If relay is ever deployed behind a load
  balancer with more than one `relay-server`, live tailing works only when the
  subscriber lands on the process holding the agent stream. Same as status events
  today, but a live log view makes the symptom far more visible. Documented; the
  fan-out fix is proposed as its own item.
