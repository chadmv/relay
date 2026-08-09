# Task Log View + Live SSE Tailing (SPA) - Design

Date: 2026-08-09
Status: Draft (autonomous cycle; conductor review)

## Overview

`docs/backlog/feature-2026-06-26-task-log-view-sse-tailing.md` is the top of Now and
became unblocked minutes ago: its backend enabler shipped as
`docs/backlog/closed/feature-2026-06-26-sse-task-log-publishing.md`. Relay now has a
live log source (`GET /v1/events?task_id=`) and no consumer.

This spec covers the **SPA consumer only**: the SPA's first SSE client, the live
job-detail log tab, and the full-screen single-task log view. It is
**frontend-only**. Every endpoint it needs exists and is documented in
`README.md:1299-1372`.

Scope: `web/src/lib/`, `web/src/jobs/`, `web/src/app/router.tsx`. No Go change, no
migration, no `internal/mcp/` change.

**Where the backlog item, the design handoff, or my reading of the README disagrees
with the code, the code wins.** Every disagreement found is recorded below rather
than silently resolved.

## The backend contract, settled - do not re-litigate

Read from `internal/api/events.go:14-95`, `internal/api/tasks.go:56-137`,
`internal/api/server.go:124,171`, and `README.md:1299-1372`. The shape decision
(`?task_id=` on `/v1/events`, not `?follow=1`) was settled on the backend half and
this spec inherits it.

| Fact | Where | Consequence for this design |
|---|---|---|
| `GET /v1/events?task_id=<uuid>` yields `task_log` frames for that task only | `events.go:30-48,59` | No client-side filtering of a job-wide firehose. One task per subscription. |
| Payload is `{task_id, job_id, seq, stream, content, created_at}` | `README.md:1322`, `handler.go` `taskLogEvent` | `seq`/`stream`/`content`/`created_at` are field-identical to `logEntry` (`tasks.go:56-61`), so **one client type covers both surfaces**. |
| `seq` is ordered but **not contiguous** (it is `task_logs.id`, a table-wide `BIGSERIAL`) | `README.md:1357-1360` | A gap is **not** a drop signal. Any "re-page on a gap" logic would re-backfill on nearly every frame on a busy farm. This is the single most important negative requirement in this spec. |
| The only drop signals are `event: dropped` (`{"reason":"slow_consumer"}`) and a closed stream | `events.go:78-86`, `README.md:1346-1350` | Recovery is a re-backfill from the last `seq` seen, plus a visible marker. |
| Gapless backfill requires subscribe **first**, then page `?since_seq=`, then discard frames with `seq <= maxSeq` | `README.md:1334-1344`, `events.go:67-70` | The subscription must be opened before the first history request. Ordering is load-bearing and must be asserted by a test, not assumed. |
| `?job_id=&task_id=` shares **one** 64-slot buffer across both families | `README.md:1352-1355` | A log burst can drop-close a combined connection including its status frames. Informs the one-vs-two decision below. |
| No `Last-Event-ID` / `id:` resume | `README.md:1349-1350` | The client owns recovery. |
| The server never ends the stream on its own; a `?task_id=`-only subscription has **no terminal signal** | `README.md:1310-1313` | Something else must tell the UI when to stop tailing. |
| `?task_id=` returns 400 on a malformed UUID, 404 on an unknown task | `events.go:31-44`, `README.md:1362-1366` | Distinguishable failure states in the UI. |
| Polling page: `?limit=` 1..200 (default 50), `?since_seq=`, response `{items, next_seq, total}`; `next_seq` is 0 when drained | `tasks.go:81-136` | Backfill paging is 200 lines per request, and `total` is available for an honest "showing N of T" notice. |
| Broker is in-process | `README.md:1368-1372` | Behind >1 replica, live tailing silently degrades while polling stays correct. Not fixable here. |

## What the SPA has today

### The log tab is static by construction, and says so

`web/src/jobs/LogTab.tsx` is a pure view over resolved items (`LogTab.tsx:10-50`).
It renders a `STATIC · HISTORY` / `live tailing pending` header strip
(`LogTab.tsx:37-40`) and one `<div>` per item with `text-err` for `stderr`
(`LogTab.tsx:41-47`), plus loading, error-with-retry and empty states
(`LogTab.tsx:21-34`). Its own comment names both backlog items as the reason there
is no follow toggle and no auto-scroll (`LogTab.tsx:4-9`), and
`LogTab.test.tsx:29-37` asserts the absence of a `LIVE` badge on purpose. That
honesty is what this slice is here to retire.

What it does **not** do: no tailing, no auto-scroll, no follow toggle, no
reconnection, no drop signalling, no line count cap, no full-screen view.

### The data hook is fetch-once and deliberately inert

`web/src/jobs/useTaskLogs.ts:10-17` is a `useQuery` with `staleTime: Infinity`, no
`refetchInterval`, a caller-controlled `enabled`, and a key (`['task-logs', id]`)
deliberately outside the `['job', ...]` prefix so a job poll cannot disturb it.
It fetches exactly one page: `getTaskLogs(taskId)` (`web/src/jobs/api.ts:121-127`)
sends no `limit` and no `since_seq`, so it returns **only the first 50 lines** and
`next_seq` is ignored. A task with more than 50 lines is silently truncated in the
current UI with no notice.

### Where the tab lives, and what drives selection

`web/src/jobs/JobDetailPage.tsx` owns a `'spec' | 'log'` tab (`:17`, `:155-174`),
a `pickedTaskId` state plus a derived `selectedTaskId` that falls back when a poll
changes the task list (`:31-42`), and gates the log query to
`selectedTaskId !== '' && tab === 'log'` (`:46`). `TasksTable`
(`web/src/jobs/TasksTable.tsx:13-21,43-51`) rows are **selection controls, not
navigation** (`aria-selected`, `onSelect`), so there is exactly one selected task at
a time and no per-row data fetching anywhere.

Job state comes from `useJob(id)` (`web/src/jobs/useJob.ts:7-14`), a 3000 ms
`refetchInterval` query with `keepPreviousData` that drives the header, the derived
progress strip (`JobDetailPage.tsx:80-84`), the DAG and the tasks table.

### Transport, auth, and primitives

`apiFetch` (`web/src/lib/api.ts:28-58`) prefixes `/v1`, attaches
`Authorization: Bearer <token>` from `getToken()` (`api.ts:31-32`,
`web/src/lib/token.ts:3-5`, backed by `localStorage`), notifies `onUnauthorized`
listeners on a 401 (`api.ts:43-45`), and throws `ApiError`. `AuthProvider`
subscribes to that notifier and performs the logout-and-redirect
(`web/src/auth/AuthProvider.tsx:39-49`). There is **no** streaming helper and no
SSE code anywhere in `web/`.

Holo primitives available: `GlassPanel`, `Eyebrow`, `ProgressBar`, `Chip`,
`PillButton`, `KpiStat`, `Panel`, `StatusDot`
(`web/src/components/holo/index.ts:3-10`). `PillButton` is the follow-tail control.

Test stack: Vitest 2.1.8 + jsdom 29 + MSW 2.7 (`web/package.json:29-34`), jsdom
environment with a global setup file (`web/vite.config.ts:13-18`), and
`onUnhandledRequest: 'error'` (`web/src/test/setup.ts:5`) so every request in a test
needs an explicit handler.

## Where the item and the handoff disagree with the code

1. **The item's core decision is already made.** "Consume the existing job-scoped
   `?job_id=` SSE and filter client-side, OR add `?follow=1`" - neither. It is
   `?task_id=`, server-side filtered, settled in the enabler. The item's
   "Acceptance / Done When" first bullet ("the decision is made and recorded") is
   satisfied by the enabler's Resolution.
2. **`/jobs/:id/tasks/:n` cannot use `n` as a stable index, and this is worse than
   theoretical.** There is no task ordinal column (`000001_initial.up.sql:51-66`).
   The task list comes back `ORDER BY created_at` with no tiebreaker
   (`internal/store/query/tasks.sql:10,38`), `tasks.created_at` defaults to `NOW()`
   (`000001_initial.up.sql:65`), and job creation inserts every task inside one
   transaction (`internal/api/jobs.go:202-209`) where Postgres `NOW()` is constant.
   **Every task of a job therefore has an identical `created_at` and the returned
   order is unspecified** - and because a status `UPDATE` rewrites the row, heap
   order genuinely changes as the job runs. A bookmarked `/jobs/:id/tasks/3` would
   drift to a different task mid-job. Decision: the route is
   **`/jobs/:id/tasks/:taskId`** keyed by task UUID. Recorded as a handoff
   deviation.
3. **The handoff's `?follow=1` endpoint does not exist**
   (`design_handoff_relay_holo/README.md:203,254`). The hi-fi - the authoritative
   layer - already captions the stream `/v1/events?task_id={id}`
   (`hifi3-holo-pages.jsx:2740`), so only the structure-only reference is wrong.
4. **The hi-fi's log rows show four columns (time, LEVEL, source, message)**
   (`hifi3-holo-pages.jsx:2754-2759`). Relay log lines carry **no level and no
   source**: a `logEntry` is `{seq, stream, content, created_at}` (`tasks.go:56-61`).
   Rendering a fabricated `INFO`/`DEBUG` column would invent data. Decision: two
   columns, `created_at` time plus content, with `stderr` toned as the error color
   (extending the existing `LogTab.tsx:43` treatment). Recorded as a deviation.
5. **A log entry is not a line.** `chunkWriter.Write`
   (`internal/agent/runner.go:285-309`) copies whatever `os/exec` hands it, so an
   entry is an arbitrary byte range: it can hold many lines, and a single logical
   line can straddle two entries. Today's `LogTab.tsx:41-47` renders one `<div>` per
   entry, so multi-line content collapses under default HTML whitespace handling and
   a straddling line renders as two rows. This is a pre-existing rendering defect
   that a live view makes constant, and it forces a real design decision (below).
6. **The item's "polling backfill for history before the live tail" has the order
   backwards.** The README requires subscribe first, then backfill
   (`README.md:1334-1344`). Doing it the item's way leaves a hole.

## Decision 1: transport is `fetch` + `ReadableStream`, not `EventSource`

`EventSource` cannot set an `Authorization` header, and the SPA's only credential is
a bearer token in `localStorage` (`token.ts:3-5`, `api.ts:31-32`). Four options were
weighed.

**Chosen: `fetch` with a manual SSE parse over `response.body`.** No backend change.

```
apiStream(path, { signal, onEvent })   // web/src/lib/api.ts - auth + 401, one place
parseSSE(chunk) -> frames              // web/src/lib/sse.ts  - pure, no network
```

Why:

- **It is the only option that needs no backend change and adds no credential
  exposure.** The bearer header goes on the request exactly as `apiFetch` already
  does it, same-origin, no CORS involvement (`internal/api/cors.go` is fail-closed
  and unchanged).
- **Losing `EventSource`'s automatic reconnect is a feature here, not a cost.**
  `EventSource` retries forever at a fixed interval with no cap. Requirement (4)
  below demands a *bounded* retry, so we would have had to fight that behaviour.
- **`Last-Event-ID` is irrelevant.** The server does not honour it
  (`README.md:1349-1350`), so `EventSource`'s one real advantage does not apply.
- **The parser is small and the framing is simple.** `handleEvents` writes
  `event: <type>\ndata: <payload>\n\n` with a flush per frame
  (`events.go:90-92`), and payloads are `json.Marshal`ed so no literal newline
  survives inside one. The parser still must handle multi-line `data:`, CRLF, and a
  frame split across two reader chunks, because those are properties of the
  transport, not of this server.

**Rejected: a token in a query parameter** (`?access_token=`). Unacceptable, and the
reason is concrete rather than stylistic: it would put a long-lived credential into
request URLs, which land in proxy and access logs, in browser history, and in
`Referer`. Relay's token is the *only* credential and is not scoped or short-lived
(`internal/tokenhash`, `README.md` auth section). It would also require a backend
change to accept it, so it is strictly worse on both axes. `handleEvents` reads only
`task_id` and `job_id` (`events.go:31-53`) and must stay that way.

**Rejected: a session cookie.** The SPA has no cookie auth at all; adding one means
a server-side session, a `Set-Cookie` path, `SameSite`/`Secure` policy, and a CSRF
story for every existing mutating endpoint. Large, security-sensitive, and entirely
for the convenience of one browser API.

**Rejected: any backend change.** None is needed. Stating this plainly because the
task asked: **this design requires zero Go changes.**

Two caveats the planner must carry:

- **`onUnauthorized` must fire on a streaming 401.** Otherwise a revoked token turns
  into a silently empty log rather than a redirect to sign-in. `apiStream` lives in
  `api.ts` precisely so the token header and the 401 notification stay in one place -
  the SPA-side analogue of the codebase's "single JSON entry point" rule. `sse.ts`
  holds framing only and knows nothing about auth.
- **A response can be non-OK before any body arrives** (400/404/401 from
  `events.go:34-43`, written as JSON before the headers switch to
  `text/event-stream`). `apiStream` must check `res.ok` and throw `ApiError` with the
  parsed envelope, matching `apiFetch`'s behaviour (`api.ts:47-53`), so a 404
  "task not found" is distinguishable from an empty log.

## Decision 2: one connection, `?task_id=` only

The job-detail page opens **one** SSE connection carrying logs only. Job and task
status keep coming from the existing `useJob` poll (`useJob.ts:7-14`).

Why not `?job_id=&task_id=`:

- **It would buy nothing the page does not already have.** Status frames are
  `{id, status}` only (`README.md:1319-1320`). The page needs the full task list,
  worker assignment, retry counts and `depends_on` (`api.ts:70-85`), which only
  `GET /jobs/:id` supplies. So `useJob` polling stays either way, and adding status
  frames creates a second, thinner source of truth for the same state that we would
  have to reconcile into the query cache.
- **It would import the shared-buffer coupling for free.** One subscription is one
  channel with one 64-slot buffer (`README.md:1352-1355`), so a log burst could
  drop-close the connection *including* its status frames. Keeping logs alone on the
  connection means a drop costs only a log re-backfill, never job state.
- **Held connections stay at one.** relay-server is plain HTTP/1.1, so browsers cap
  concurrent connections per origin at six. One held stream leaves five for polling;
  two would leave four for no benefit.

The one thing `?job_id=` would have provided is the terminal signal a `?task_id=`
subscription lacks (`README.md:1310-1313`). **`useJob`'s poll already provides it**,
more completely: the selected task's `status` transitions to a terminal value
(`done`/`failed`/`timed_out`, `api.ts:66`) within one poll interval, which is the
signal to stop tailing. Polling supplying the terminal signal is what makes the
single log-only connection sufficient.

Two consequences of using status-from-poll:

- **A terminal task opens no connection at all.** If the selected task is already
  terminal when the tab opens, the hook backfills history and never subscribes. That
  is strictly less server load than the alternative and removes the "connection open
  forever on a finished task" failure entirely.
- **When a live task becomes terminal, the hook closes the stream and issues one
  final reconciliation page** (`?since_seq=<maxSeq>`) before settling. This closes
  the "did we get the tail" question without depending on frame delivery, and costs
  one request per completed task view.

## Decision 3: log state lives in the hook, not in the TanStack cache

`useTaskLogs` (`useTaskLogs.ts`) is **deleted** and replaced by
`useTaskLogStream(taskId, { live, enabled })`, which owns its own state. The
backfill pages are fetched by direct `getTaskLogs` calls inside the hook, not via
`useQuery`.

Why not the query cache, given every other hook in the app is query-based
(`useJob`, `useJobs`, `useWorkers`, ...):

- **A live append-only stream is not a query.** It has no single fetch that
  resolves, no meaningful `staleTime`, and no meaningful `invalidate`. Modelling it
  as a query means either writing into the cache on every frame (fighting
  structural sharing, allocating a new array per frame) or keeping a shadow copy
  anyway.
- **The subscribe-before-backfill ordering is the correctness property of this
  feature, and `useQuery` does not let the caller own when its fetch starts.** The
  effect must open the stream, then page. That is imperative sequencing by nature.
- **Paging is a loop with a cap and an early exit**, not one request. `useQuery`
  would need `useInfiniteQuery` plus manual page pumping, which is more machinery
  for less control.

What this costs, stated: re-selecting a task re-backfills instead of reading a
cached page. Accepted, because a live view must re-subscribe on re-select anyway, and
the log is the one surface where a cached answer is the wrong answer. `useJob` and
every other query hook are untouched.

The pure logic lives in `web/src/jobs/logBuffer.ts` so the hook stays thin and the
interesting behaviour is testable without React, MSW, or timers:

```
appendEntries(state, entries: LogEntry[]) -> state   // dedupe by seq, reassemble, cap
markDropped(state) -> state
visibleRows(state) -> Row[]                          // lines + provisional partials
shouldFollow(scrollTop, scrollHeight, clientHeight) -> boolean
```

## Decision 4: the gapless join, concretely

Per `README.md:1334-1344`, in this exact order. `maxSeq` and the pre-backfill buffer
both live in refs inside `useTaskLogStream`, not in React state, so writing to them
never triggers a render and never changes the ordering.

```
on effect mount for (taskId, live, enabled):
  if !enabled or taskId == '':            return
  reset state; buffering = true; pending = []
  if live:
    open apiStream('/events?task_id=' + taskId)
      onEvent(task_log): entry = payload
                         if buffering        -> pending.push(entry)
                         else                -> ingest([entry])
      onEvent(dropped):  recover('dropped')
      onClose:           recover('closed')      // the server never closes cleanly
    await firstResponseHeaders                  // 200 == subscription is live
  page = 0
  since = 0
  loop:
    r = await getTaskLogs(taskId, since, 200)
    ingest(r.items); total = r.total
    if r.next_seq == 0                 -> historyComplete = true;  break
    if ++page >= MAX_BACKFILL_PAGES    -> historyTruncated = true; break
    since = r.next_seq
  buffering = false
  ingest(pending); pending = []                 // dedupe drops seq <= maxSeq
```

`ingest(entries)` runs `appendEntries`, which:

1. drops any entry with `seq <= maxSeq` (the dedupe from README step 3),
2. raises `maxSeq` to the highest seq accepted,
3. reassembles content into lines (Decision 5),
4. enforces the line cap (Decision 7).

Both the buffered replay and the live path go through the same `ingest`, so there is
one dedupe rule and one place it can be wrong.

**`seq` gaps are never acted on.** There is no gap detection anywhere in this design,
by design. The only two recovery triggers are the `dropped` frame and stream close.
This is called out because the naive implementation is a load amplifier
(`README.md:1357-1360`) and because the README itself carried the wrong contract
until the enabler's review corrected it.

**Awaiting the subscription before the first page** uses the response headers, not a
timer: `apiStream` resolves a promise once `fetch` returns a 200, and
`handleEvents` subscribes and then flushes before its first receive
(`events.go:59-70`), so a 200 means the subscription is registered. A sleep here
would be exactly the barrier that the enabler's retro caught as a broken test
pattern.

## Decision 5: entries are reassembled into lines

Because a log entry is an arbitrary byte range (`runner.go:285-309`), the client
reassembles a character stream into lines rather than treating an entry as a line:

- One pending-partial buffer **per stream** (`stdout`, `stderr`). An entry's content
  is appended to its stream's partial; every `\n` emits a completed line in the order
  its terminating newline arrived, which is what a terminal shows for merged output.
- **Carriage returns are collapsed**: within one emitted line, only the segment after
  the final `\r` is kept. Progress bars (`\rframe 12/100`) then render as one
  updating line instead of a wall of concatenated garbage.
- **A dangling partial is rendered**, as a provisional trailing row per stream. A
  task that prints a prompt with no trailing newline must not look silent. On
  terminal status the partials are flushed as final lines.
- Emitted lines render with `whitespace-pre-wrap` so indentation survives, and always
  as React text children. **Never `dangerouslySetInnerHTML`** - this is untrusted
  subprocess output.
- **ANSI SGR escape sequences are stripped** for display by one regex in
  `logBuffer.ts`. Rendering the colors is omitted (see Omissions); leaving the raw
  bytes in would show `[32m` litter that reads as corruption.

Dedupe happens on the entry's `seq` *before* reassembly, so replaying a buffered
frame can never duplicate a partial line.

## Decision 6: reconnection, drops, and teardown

| Event | UI behaviour |
|---|---|
| `event: dropped` | Insert a persistent in-stream marker row ("lines may be missing here"), set `status = 'recovering'`, then immediately re-subscribe and re-backfill from `maxSeq`. No backoff delay: this is the server telling us it dropped us, and one immediate recovery is correct. |
| Stream closes with no `dropped` | Same recovery, but through the bounded backoff below. A clean close is abnormal (`README.md:1310-1313`), so it is treated as a failure for backoff purposes, not as an end of data. |
| `fetch` rejects (network loss, server down) | Bounded backoff. `status = 'reconnecting'` with the attempt number visible. |
| 401 | `apiStream` fires `onUnauthorized`; `AuthProvider` (`AuthProvider.tsx:39-49`) redirects to sign-in. No retry. |
| 404 / 400 | Terminal error state with the message, no retry. A deleted task or a bad id is not transient. |
| Selected task changes | Effect cleanup aborts the previous stream before the new effect runs. State fully resets. |
| Leaving the Log tab (`tab !== 'log'`) | `enabled` goes false, the effect tears down, the connection closes. Matches the existing gating at `JobDetailPage.tsx:46`. |
| Unmount / route change | `AbortController.abort()` in cleanup. |
| Selected task reaches a terminal status | Close the stream, issue one `?since_seq=<maxSeq>` reconciliation page, settle to `status = 'ended'`. |

**Bounded retry.** Delays `1s, 2s, 4s, 8s, 15s`, capped at 15s, **maximum 5
consecutive failed attempts**, then `status = 'disconnected'` with a manual
"Reconnect" button that resets the counter. No unbounded loop: a server restart with
50 open browser tabs must not become a reconnect storm.

**The counter resets only on a connection that has proven itself**, not on one that
merely opened: the attempt count resets after a stream has stayed open for
`RESET_AFTER_MS = 10_000` or has delivered at least one frame. Resetting on open
alone is precisely the bug relay already shipped once on the agent side
(`docs/retros/2026-06-20-reconnect-backoff-never-resets.md`), where a connection that
opens and immediately fails produces an unbounded tight loop. This rule gets its own
test.

**The drop marker is permanent for the session.** Once lines have been missed, the
view is no longer provably complete, so the marker row stays even after recovery
succeeds. Silence here would misrepresent an incomplete log as complete, which is the
exact failure the current `STATIC · HISTORY` label exists to avoid.

**Status vocabulary** surfaced in the header strip, replacing
`STATIC · HISTORY` / `live tailing pending` (`LogTab.tsx:37-40`):
`live` (green dot, matching the hi-fi at `hifi3-holo-pages.jsx:2742-2744`),
`loading`, `recovering`, `reconnecting (n/5)`, `disconnected`, `ended`, `history`
(terminal task, no stream), `error`.

## Decision 7: bounded memory, bounded requests, bounded renders

Three separate bounds, because they fail differently.

- **`MAX_BACKFILL_PAGES = 10`** at `limit=200`, so history costs at most 10 requests
  and 2000 lines. If `next_seq != 0` when the cap is hit, `historyTruncated = true`.
- **`MAX_LINES = 2000`** retained, drop-oldest. Unbounded growth is **not** accepted:
  a render task can emit hundreds of thousands of lines and the browser tab is a
  worse place to hold them than Postgres, which already has them all.
- **Frames are coalesced into one state update per ~100 ms** rather than one per
  frame. This is not only a render optimization: the client's read rate is part of
  the server's drop story, since a browser that stops draining the socket fills the
  server's 64-slot buffer and gets drop-closed (`README.md:1346-1348`). Reducing
  React work per frame directly reduces server-side drops.

**The honest ugly corner.** The polling endpoint only pages *forward* from
`since_seq` (`tasks.go:91-105`), and `seq` is not contiguous so `total` cannot be
turned into an offset. There is therefore **no cheap way to fetch the last N lines of
a long history**. When the page cap is hit we hold the *oldest* 2000 lines of a
possibly-huge log, which is the wrong end for a tail view. Two mitigations:

1. The notice is explicit and uses the real numbers from `total`: "Showing the first
   2000 of 94,312 lines. Live output continues below."
2. Once live lines start arriving, drop-oldest converges the view to a true tail
   within 2000 lines, and the notice switches to "Earlier output not shown."

The real fix is a descending or `?before_seq=` mode on
`GET /v1/tasks/{id}/logs`, which is a backend change and **proposed as a follow-up
item** below. In practice most task logs are far under 2000 lines, so this corner is
rare - but it is the design's weakest point and should not be discovered later.

**No virtualization in this slice.** 2000 monospace rows in one scroll container is
acceptable; adding a windowing dependency (`react-window` or equivalent) is a
dependency decision that deserves its own item, and the `MAX_LINES` cap is what makes
deferring it safe.

**Follow-tail and jump-to-latest.** `follow` defaults true. On append, if `follow` is
on, scroll to the bottom. A user scroll that moves more than `FOLLOW_EPSILON = 24px`
off the bottom turns `follow` off and reveals a "Jump to latest" pill; clicking it
turns `follow` back on and scrolls. The threshold decision is the pure
`shouldFollow(scrollTop, scrollHeight, clientHeight)` helper (see Testing - the pixel
effect cannot be honestly asserted in jsdom).

## Decision 8: routes, components, and the connection-count guarantee

```
web/src/lib/sse.ts               NEW  pure incremental SSE frame parser (no network, no auth)
web/src/lib/api.ts               EDIT + apiStream(path, {signal, onEvent, fetchImpl})
web/src/jobs/api.ts              EDIT + TaskLogEvent, streamTaskLog(), getTaskLogs(id, sinceSeq, limit)
web/src/jobs/logBuffer.ts        NEW  pure: dedupe, line reassembly, cap, shouldFollow, ANSI strip
web/src/jobs/useTaskLogStream.ts NEW  the one stateful hook: subscribe -> backfill -> merge -> recover
web/src/jobs/useTaskLogs.ts      DELETE (superseded)
web/src/jobs/LogView.tsx         NEW  shared presentational log body: header strip, rows, follow pill
web/src/jobs/LogTab.tsx          EDIT LogView inside the job-detail panel + a link to the full view
web/src/jobs/TaskLogPage.tsx     NEW  full-screen view at /jobs/:id/tasks/:taskId
web/src/jobs/JobDetailPage.tsx   EDIT swap the hook; pass the selected task's status through
web/src/app/router.tsx           EDIT + <Route path="/jobs/:id/tasks/:taskId" .../> inside ProtectedRoute
```

`TaskLogPage` reuses `useJob(id)` for the header (job id, task name, status, worker),
which also gives it the same terminal signal, and reuses `useTaskLogStream` and
`LogView` unchanged. Chrome per the hi-fi (`hifi3-holo-pages.jsx:2716-2745`):
breadcrumb "← Job detail", job id, task name, status pill, the endpoint caption
`/v1/events?task_id=<id> · single-task stream`, and the follow-tail control. The
`↧ Download` button at `hifi3-holo-pages.jsx:2732` is **omitted** (see Omissions).

**The connection-count guarantee, structurally.** `useTaskLogStream` is mounted in
exactly two places, each of which renders at most one instance:
`JobDetailPage` (one, for `selectedTaskId`) and `TaskLogPage` (one, for the route
param). `TasksTable` and `LogView` are presentational and never call it - `TasksTable`
rows are already selection controls, not data owners
(`TasksTable.tsx:13-21,43-51`), so there is no path by which a 500-task job opens 500
connections. The hook's effect keys on `[taskId, live, enabled]` and its cleanup
aborts unconditionally, so a rapid sequence of selections opens and aborts one stream
each with none left behind. Both properties get explicit tests.

## Omitted deliberately

| Candidate | Why omitted | Worth an item? |
|---|---|---|
| `step_index` / `step_total` (step grouping, "STAGE 4 / 8") | The backend exposes them on **neither** surface: `handleTaskLog` drops them at persist and `logEntry` has no such field (`tasks.go:56-61`). Displaying them is impossible, not merely deferred. Owned by `docs/backlog/feature-2026-06-26-persist-expose-step-index-total.md`, which lights up the polling page and the SSE payload together. | Already filed. |
| Descending / `?before_seq=` log paging | The real fix for Decision 7's ugly corner: without it there is no cheap way to fetch the tail of a long history. Backend change, so out of a frontend-only slice. | **Yes - proposed, not filed.** |
| Log row virtualization | Needs a new dependency and a measurement pass. `MAX_LINES = 2000` makes deferring it safe. | **Yes - proposed, not filed.** |
| `↧ Download` / copy-to-clipboard (`hifi3-holo-pages.jsx:2732`) | Two reasons. It needs the full history, which the page cap explicitly does not fetch, so it would either be a lie (downloading 2000 lines) or an unbounded request loop. And it is the affordance most likely to move secret-bearing output into a file on disk or a shared clipboard. Do it properly as a server-side export once descending paging exists. | **Yes - proposed, not filed**, coupled to the paging item. |
| ANSI color rendering | Sequences are stripped so the text is readable; parsing them into spans is a real sub-feature (SGR state machine, 256-color, nesting) with no design in the handoff. | **Yes - proposed, not filed.** Low value. |
| A stable task ordinal / tiebreaker on the task list ordering | Finding (2): every task of a job shares one `created_at` and `ORDER BY created_at` has no tiebreaker, so the task table's row order is unspecified and shifts as rows are updated. This design routes around it (UUID routes), but the tasks table visibly reorders while a user watches, which is a real defect independent of logs. Backend change. | **Yes - proposed, not filed.** |
| Pause tailing when the browser tab is hidden | A hidden tab still drains a socket (unlike throttled timers), so drop risk is low, and pausing adds a state axis plus a resume-backfill path. | No. |
| SSE `Last-Event-ID` resume | The server does not honour it (`README.md:1349-1350`). Nothing to consume. | No (settled in the enabler). |
| Client-side gap detection on `seq` | Actively harmful: `seq` is non-contiguous (`README.md:1357-1360`), so this would re-backfill on nearly every frame. | No. Never. |
| Tailing more than one task at once (split view) | No consumer asks for it; both hi-fi surfaces show one selected task, and it multiplies held connections. | No. |
| Live status via `?job_id=` on the same connection | Decision 2. Would create a second source of truth for job state and import the shared-buffer coupling. | No. |
| Search / filter / stderr-only toggle within the log | Genuinely useful, but it interacts with the line cap (filtering 2000 retained lines is not filtering the log) and belongs after descending paging. | **Yes - proposed, not filed.** |
| Fixing the polling endpoint's silent 50-line truncation independently | This slice removes it by paging with `limit=200` up to the cap, so there is nothing left to file. | No. |

## Security and system-design

**Credential handling.** The bearer token stays in the `Authorization` header on the
streaming request, attached in the same single place as every other request
(`api.ts:31-32`). It must never appear in a URL, a query parameter, or a log line.
`apiStream` is same-origin `/v1`-prefixed, so `internal/api/cors.go`'s fail-closed
policy is untouched.

**Log content is untrusted and can contain secrets.** `content` is raw subprocess
stdout/stderr: P4 paths, hostnames, env-derived values, and anything a user's own
script echoed, including credentials. Rules the implementation must follow:

- **Never `console.log`, `console.debug`, or `console.error` a frame, a payload, or a
  line.** Browser consoles are captured by extensions and screen-shared. Error paths
  log the status code and the task id only, mirroring the server-side rule the
  enabler adopted for the same bytes.
- **Never `dangerouslySetInnerHTML`.** Content is rendered as React text children,
  which escapes it. This is also an XSS boundary: a job that prints `<img onerror>`
  must render as characters.
- **No download and no copy affordance in this slice** (Omissions). When one is
  added it should be deliberate and server-side, not an accidental "select all" of
  secret-bearing output.
- Content is never written to `localStorage`, `sessionStorage`, or the query cache
  (Decision 3 keeps it in component-lifetime memory, which is discarded on unmount).

**Authorization is unchanged and deliberately not re-solved.** `GET /v1/events` and
`GET /v1/tasks/{id}/logs` are both registered `auth(...)` with no ownership check
(`server.go:124,171`; `events.go:25-29` says so explicitly), so **any authenticated
user can already read any task's logs**. This slice surfaces bytes the same token
already fetches on the same page and introduces no escalation. Tightening
cross-tenant reads is a policy change that must land on the polling endpoint at the
same time or it accomplishes nothing. Noted, not addressed.

**Related open items this design inherits rather than fixes:**
`docs/backlog/idea-2026-08-09-sse-revoked-token-keeps-streaming.md` (bearer auth on
`/v1/events` is checked once at connect, so a revoked token keeps receiving live
content for the life of the held connection - a browser tab left open across a
revocation keeps tailing) and
`docs/backlog/bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero.md` (forged
lines on a never-claimed task would appear in this view live). Both are backend items;
this consumer makes their symptoms more visible, which is an argument for their
priority, not for solving them here.

**Load and connection count.** Worst case per user: one held SSE connection per open
job-detail-with-log-tab or full-screen-log tab, plus the existing 3 s `useJob` poll.
Server cost per connection is one goroutine, one held HTTP connection and one 64-slot
channel (`README.md:1352`). Steady-state cost with nobody tailing is zero, because
`HasLogSubscriber` short-circuits publishing. The client-side bounds that keep this
honest: exactly one hook instance per page (Decision 8), a terminal task opening no
connection at all (Decision 2), teardown on tab switch and unmount (Decision 6), and
at most 5 reconnect attempts before requiring a human click (Decision 6). There is
still **no server-side cap on concurrent SSE subscriptions** - pre-existing, proposed
in the enabler's omissions, unchanged here.

**Failure modes under load, named.**

- *Server restart:* every stream closes at once. Each client backs off
  `1s..15s` with jitter absent (accepted: 5 attempts x <=15 s is bounded, and adding
  jitter is a one-line improvement the planner may include), re-backfills from
  `maxSeq`, and converges. Polling keeps the page correct throughout.
- *Slow client:* a browser that cannot keep up is drop-closed by the server, gets a
  `dropped` frame, shows the marker, and re-backfills. The 100 ms coalescing exists
  to make this rare.
- *Multi-replica deployment:* live frames may never arrive at all
  (`README.md:1368-1372`). The UI degrades to backfilled history with a `live` badge
  and no lines, which is misleading. Accepted for now because relay is single-replica
  today; the honest mitigation available in this slice is that history is always
  fetched, so the view is never empty when output exists.

**Invariants check.** No Go code changes, so the backend invariants are untouched by
construction: no write to `tasks.status` or `task_logs` (epoch fence), no job-spec
ingestion, no gRPC sender, no worker registry teardown, no shared-registry getters,
no request body (single JSON entry point). The SPA-side analogue this design does
respect: **one place attaches the bearer token and fires the 401 notifier**
(`web/src/lib/api.ts`), and `apiStream` is added there rather than opening a second
authenticated transport path.

## Testing

Standing project lesson: **a plan's test bodies are guesses.** Every test below that
could pass vacuously is marked with how to prove it RED.

### Pure, no React, no network (highest value)

1. `sse.ts` parser: a frame split across two reader chunks is parsed once and
   correctly. **Non-vacuity:** feed the split at every byte offset of a known frame
   and assert the same result for all of them; an implementation that only handles
   whole frames fails on most offsets.
2. `sse.ts`: multi-line `data:` lines concatenate with `\n`; `\r\n` line endings
   parse; an unknown event type is surfaced rather than dropped; a comment line
   (`:keepalive`) is ignored.
3. `logBuffer.ts` dedupe: entries with `seq <= maxSeq` are discarded, `seq > maxSeq`
   are appended, `maxSeq` advances. **Paired positive control on the same call path**
   (the enabler's standing rule): the same test feeds one below and one above and
   asserts both outcomes.
4. `logBuffer.ts` **non-contiguous seq is not a drop signal**: feeding `seq` 10, 40,
   41 produces three lines, no marker, and no recovery request. **This is the test
   that protects against the README's old wrong contract.** RED proof: add gap
   detection and it fails.
5. `logBuffer.ts` line reassembly: one entry containing three `\n` yields three
   lines; a line split across two entries yields one line; a dangling partial is
   exposed as a provisional row and flushed on finalize; `\r` runs collapse to the
   last segment; ANSI SGR sequences are stripped; `stdout` and `stderr` partials do
   not corrupt each other.
6. `logBuffer.ts` cap: feeding `MAX_LINES + 50` retains the last `MAX_LINES` and sets
   the eviction flag. **Non-vacuity:** assert the *first* retained line is the
   expected one, not just the length.
7. `shouldFollow` boundary behaviour at, just inside, and just outside
   `FOLLOW_EPSILON`.

### `apiStream` (transport)

8. Auth: the request carries `Authorization: Bearer <token>` and the `/v1` prefix.
9. A 401 response fires the `onUnauthorized` listeners and does not retry.
10. A 404 with `{"error":"task not found"}` throws `ApiError(404, 'task not found')`
    before any frame is delivered.
11. `abort()` stops delivery: no `onEvent` fires after abort, and the reader is
    released.
12. **Incremental delivery.** MSW 2 can return a `ReadableStream` body, and this test
    asserts the *first* frame is observed **before** the stream closes. **This is the
    assertion that must not be skipped**, because a buffering implementation passes a
    naive "both frames arrived" test. If MSW + undici under jsdom cannot be made to
    deliver incrementally (a real risk - it is an interception layer, not a socket),
    the assertion moves to a test that injects `fetchImpl` returning a hand-built
    `ReadableStream`, and the MSW test is kept for the auth/status-code path only.
    **The seam exists for this reason:** `apiStream` accepts an optional `fetchImpl`
    defaulting to `globalThis.fetch`, matching the project's existing
    package-var-override convention (`internal/cli`'s `saveConfigFn` and friends).
    Do not delete the seam if MSW happens to work.

### `useTaskLogStream` (`renderHook`, MSW for pages, injected transport for frames)

13. **Subscribe before backfill.** Record the order of "stream opened" and "first
    `/logs` request". Assert the stream came first. **RED proof: swap the two
    statements in the hook and this test must fail.** Without that proof this test is
    the most likely one in the suite to be vacuous.
14. Buffered frames arriving during backfill are applied after it, deduped: a frame
    with a seq also present in a page appears once. Paired positive: a frame above
    `maxSeq` appears.
15. Multi-page backfill pumps `since_seq` from `next_seq` until `next_seq == 0`.
16. Page cap: a server that never drains stops the loop at `MAX_BACKFILL_PAGES`,
    sets `historyTruncated`, and **still starts applying live frames**. Non-vacuity:
    assert the exact request count.
17. `event: dropped` produces exactly **one** re-backfill (not a loop) plus the
    marker. Paired with test 4's negative.
18. **Bounded reconnect.** With fake timers, N consecutive closes produce attempts at
    1/2/4/8/15 s and then stop. **Non-vacuity:** assert the total attempt count stops
    growing after the cap - a test that only asserts "it retried" passes for an
    unbounded loop. Then assert the manual Reconnect path resets it.
19. **Backoff reset requires a proven connection.** A stream that opens and closes
    immediately, repeatedly, must not reset the counter (this is the
    `2026-06-20-reconnect-backoff-never-resets` bug class). A stream that stays open
    past `RESET_AFTER_MS` or delivers a frame must reset it. Both directions asserted.
20. A **terminal** task opens no stream and only backfills. Paired positive control:
    a `running` task opens one.
21. A task that becomes terminal mid-tail closes the stream and issues exactly one
    reconciliation page.
22. **No connection leak:** switching `taskId` three times opens exactly three
    streams and aborts exactly two; disabling opens none more; unmount aborts the
    last. Assert exact counts, not "at least one".
23. Frame coalescing: a burst of 50 frames within the flush window produces far fewer
    renders than 50. Non-vacuity: assert a render count, not just final content.

### Components

24. `LogView`: renders lines with a `stdout`/`stderr` distinction (port of
    `LogTab.test.tsx:11-16`), the empty state, the error state, the drop marker, the
    truncation notice with real counts, and each status badge including `live` (the
    inverse of `LogTab.test.tsx:29-37`, which asserted `LIVE` must be absent - that
    test is **replaced**, and its replacement should assert `LIVE` appears only when
    the stream is actually open).
25. Follow-tail: the pill toggles, and "Jump to latest" appears only when `follow` is
    off. **Auto-scroll position cannot be honestly asserted in jsdom** (`scrollTop`
    and `scrollHeight` are 0 there), so the test asserts the *decision* via
    `shouldFollow` (test 7) plus that an injected scroll callback was invoked -
    never a pixel value. A test asserting `scrollTop === scrollHeight` in jsdom would
    be vacuously green.
26. `JobDetailPage`: the Log tab renders the live view; switching tasks resets it;
    leaving the tab tears it down. Existing `JobDetailPage.test.tsx` cases must keep
    passing.
27. `TaskLogPage`: the route resolves `:taskId` from the job's task list, renders the
    header from `useJob`, and 404s gracefully when the task is not in the job.
28. Nothing logs content: a `console` spy asserts no console method is called with a
    string containing a known log line, across a full mount-stream-drop-unmount
    cycle.

`onUnhandledRequest: 'error'` (`setup.ts:5`) means every test needs explicit handlers
for `/v1/jobs/:id`, `/v1/tasks/:id/logs`, and `/v1/events` - a missing handler is a
loud failure rather than a hang, which is the behaviour we want.

## Acceptance criteria

1. The job-detail Log tab tails the selected task live: new output appears without a
   reload, and the header shows `LIVE` only while a stream is actually open.
2. A full-screen view exists at **`/jobs/:id/tasks/:taskId`** (task UUID, not `:n`)
   with the hi-fi's breadcrumb, status pill, endpoint caption and follow-tail control,
   and it reuses the same hook and the same `LogView` as the tab.
3. The SPA authenticates the stream with the bearer token in an `Authorization`
   header via `fetch` + `ReadableStream`. **No token appears in any URL** and **no Go
   file changes.**
4. The join is gapless and duplicate-free: the subscription is opened before the first
   `/logs` request (asserted by a test proven RED by swapping the order), and frames
   with `seq <= maxSeq` are discarded.
5. **No behaviour anywhere reacts to a `seq` gap.** Feeding non-contiguous seqs
   produces no marker and no extra request.
6. `event: dropped` and an unexpected stream close both produce a visible,
   session-persistent "lines may be missing" marker and exactly one re-backfill per
   event.
7. Reconnection is bounded: at most 5 consecutive attempts at 1/2/4/8/15 s, then a
   manual Reconnect control. The attempt counter resets only after a connection stays
   open past the reset threshold or delivers a frame.
8. Exactly one SSE connection exists per page at any time; switching tasks N times
   opens N streams and leaves none open; a terminal task opens none at all; leaving
   the Log tab or unmounting closes it.
9. History is bounded: at most 10 pages of 200, at most 2000 retained lines
   (drop-oldest), with an explicit notice carrying real counts from `total` whenever
   either bound bites.
10. A log entry containing multiple newlines renders as multiple lines; a line split
    across two entries renders as one line; a dangling partial is visible; ANSI
    escapes do not appear as literal text.
11. Log content is never passed to any `console` method, never written to storage or
    the query cache, and never rendered as HTML.
12. `useTaskLogs.ts` and its test are deleted; no `useQuery` holds log lines.
13. `web/` unit tests pass (`npm test` in `web/`), `tsc -b` clean, and
    `git checkout -- web/dist/` is applied before the PR is assembled (`web/dist` is
    tracked but stale; a build dirties it).
14. No file outside `web/src/` and `docs/` changes.

## Risks

- **Biggest risk for the planner: the streaming test seam.** Everything else in this
  slice is ordinary React work, but whether MSW 2.7 + undici under jsdom 29 delivers
  a `ReadableStream` body *incrementally* is unverified, and a buffering interception
  layer would make the transport test silently vacuous while the parser still looks
  green. Sequence the plan so `sse.ts` + `logBuffer.ts` (pure, zero network) land
  first with tests 1-7 green, then `apiStream` with test 12 as an explicit spike: if
  incremental delivery cannot be demonstrated through MSW, keep the `fetchImpl` seam
  and move the assertion there rather than dropping it. Do not let the hook be built
  on an unverified transport.
- **Order-of-operations regression risk is invisible without its RED proof.**
  Subscribe-then-backfill is one line's worth of ordering that a later refactor can
  silently invert, and the resulting hole is intermittent and small. Test 13 must be
  proven RED, and the hook should carry a comment saying the order is load-bearing
  with a pointer to `README.md:1334-1344`.
- **Reconnect logic is where an SPA becomes a load generator.** Every bound in
  Decision 6 exists because the unbounded version is the natural one to write. The
  reset rule (test 19) is the specific trap relay already fell into once on the agent
  side.
- **The oldest-2000-lines corner (Decision 7)** will look like a bug to whoever first
  opens a 100k-line log. The notice text is the mitigation and must be implemented
  exactly, not paraphrased; the real fix is the proposed descending-paging item.
- **Deleting `useTaskLogs` touches `JobDetailPage`'s existing tests.** Mechanical, but
  it is the change most likely to produce an unrelated-looking diff; keep it in its
  own commit after the hook is green.
- **Multi-replica silent degradation** (`README.md:1368-1372`) will read as "live
  tailing is broken" the first time relay runs behind a load balancer. Not fixable in
  the SPA; already proposed as a backend fan-out item.
