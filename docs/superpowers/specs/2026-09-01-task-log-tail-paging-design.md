# Task-Log Tail Paging: Descending Mode Plus a Tail-Opening Log View - Design

Date: 2026-09-01
Status: Draft (autonomous cycle; conductor review)
Item: `docs/backlog/idea-2026-08-09-task-log-tail-and-paging-improvements.md` (piece 1 of 3)
Lane: D of a six-lane web SPA batch. Backend first, then frontend, on ONE branch and ONE PR.

## Summary

`GET /v1/tasks/{id}/logs` pages forward only. There is no way to ask for the end of a long log, so
the SPA's log view opens at the BEGINNING of history and converges to the tail only after enough live
output arrives to evict 2000 lines. On a finished 90,000-entry log it never converges at all: the
user sees the first 2000 lines of a log they opened to see the end of.

This slice adds a descending mode to the existing endpoint (`?order=desc`, cursored by
`?before_seq=`), and changes the SPA to open at the tail with ONE request and walk earlier on demand
with a control at the top of the log body. It is additive: every existing client keeps working
without change, `?since_seq=` keeps its exact meaning, and the subscribe-then-backfill join stays
gapless in both directions.

Pieces 2 (row virtualization) and 3 (log export) of the item are OUT, with reasons and proposed
follow-up items below.

## Context at HEAD, by symbol

Everything below was read at HEAD in this worktree. Where the item, the conductor's brief, or the
2026-08-09 spec disagrees with the code, the code wins, and the disagreement is recorded.

### The endpoint

`handleGetTaskLogs` (`internal/api/tasks.go`):

- Resolves the task id, then calls `s.q.GetTask` PURELY as an existence check, and 404s an unknown
  task **before** any query-parameter validation. So today an unknown task with `?limit=0` is a 404,
  not a 400. That precedence is preserved (Decision 9).
- `limit`: absent or empty is 50; otherwise `strconv.Atoi`, and `n < 1 || n > 200` is a 400
  `"limit must be 1..200"`.
- `since_seq`: absent or empty is 0; otherwise `strconv.ParseInt`, and `n < 0` is a 400
  `"since_seq must be a non-negative integer"`.
- Calls `s.q.GetTaskLogsPage` then `s.q.CountTaskLogs` (two round trips) and writes
  `map[string]any{"items", "next_seq", "total"}`, where `next_seq` is the last row's id, forced to 0
  when `int32(len(items)) < limit` ("drained").
- `logEntry` is `{seq, stream, content, created_at}`.

**Refutation 1 (the conductor's premise).** The brief says to follow "the repo's `page[T]` envelope
conventions (items, next_cursor, total) and the `buildPage` limit+1 routing". This endpoint does
none of that. `page[T]` and `buildPage` live in `internal/api/pagination.go` and are used by
`jobs.go`, `workers.go`, `users.go`, `invites.go`, `tokens.go`, `reservations.go`,
`scheduled_jobs.go` and `agent_enrollments.go`; `handleGetTaskLogs` uses a hand-built map with a
NUMERIC `next_seq` cursor and no `limit+1` fetch. Migrating it to `page[T]` would simultaneously
break `internal/cli`'s `taskLogPage` struct, the Python SDK's `LogPage` model (whose `next_seq` is
required and undefaulted on purpose), the SPA's `TaskLogPage` type, and the README backfill recipe
that tells clients to loop on `next_seq`. This design therefore keeps the seq envelope and extends
it. The `buildPage` conventions that DO carry over are the ones about honesty rather than shape: all
envelope keys are always present, and a cursor's zero value means "no more", never "unknown".

### The store layer

`GetTaskLogsPage` (`internal/store/query/tasks.sql`):

```sql
SELECT id, task_id, stream, content, created_at
FROM task_logs
WHERE task_id = $1 AND id > $2
ORDER BY id
LIMIT $3;
```

`CountTaskLogs` is `SELECT COUNT(*) FROM task_logs WHERE task_id = $1`.

`task_logs` (`000001_initial.up.sql`): `id BIGSERIAL PRIMARY KEY`, `task_id UUID`, `stream TEXT`,
`content TEXT`, `created_at TIMESTAMPTZ`, plus `task_logs_stream_check` from `000019`.

**Finding: the index this needs already exists.** `000018_hot_path_indexes.up.sql` creates
`idx_task_logs_task_id_id ON task_logs(task_id, id)` and drops the old `task_id`-only index. A
descending scan of an equality-prefixed range uses the same index backwards. **No migration is
needed for this slice**, and the plan must not add one.

**Confirmed: the item's non-contiguity claim.** `seq` is `task_logs.id`, a table-wide `BIGSERIAL`
consumed by every task logging concurrently, so neither `total` (a per-task `COUNT`) nor arithmetic
on `seq` yields an offset. "Give me the last N" cannot be derived client-side from what the API
exposes today. This is the whole reason the change is a backend change.

### The other clients of this endpoint (all four checked)

| Client | Symbol | Sends | Decodes | Effect of an additive parameter and an additive response field |
|---|---|---|---|---|
| SPA | `getTaskLogs` (`web/src/jobs/api.ts`) | `limit`, `since_seq` when > 0 | `TaskLogPage` | Changed by this slice deliberately. |
| CLI | `printTaskLogs` (`internal/cli/logs.go`) | `since_seq=<n>&limit=200`, always explicit | `taskLogPage` struct | Unaffected. `encoding/json` ignores unknown keys. |
| Python SDK | `Client.task_logs_page` / `LogPage` (`python/src/relay/models.py`) | `since_seq`, `limit` | pydantic, `extra="ignore"` | Unaffected. |
| MCP | `callGetTaskLogs` (`internal/mcp/task_logs.go`) | `since_seq`, `limit`, always explicit | `map[string]any` passthrough | Acquires `prev_seq` with a zero-line diff and forwards it to the model. Harmless, and worth stating because a passthrough consumer is invisible to diff review. |

**Refutation 2 (not a defect of this slice, already filed).** The MCP tool's jsonschema says
`since_seq` returns "entries with seq >= this value"; the SQL is `id > $2`, exclusive. Already filed
as `bug-2026-08-26-mcp-since-seq-is-advertised-inclusive-and-is-exclusive`. Not re-filed, not fixed
here.

### The SPA log view

- `useTaskLogStream` (`web/src/jobs/useTaskLogStream.ts`) owns the whole lifecycle: subscribe via
  `openStream`, then a forward paging loop from `since`, then `buffering = false` and a replay of
  `pending`. `run(sinceSeq)` is called with `0` on a fresh mount, with `carried.maxSeq` on a
  same-task re-run, and with `logState.maxSeq` from `recover` and `scheduleRetry`.
- The forward loop is bounded by `MAX_BACKFILL_PAGES = 10` at `BACKFILL_PAGE_SIZE = 200`, and sets
  `historyTruncated` when it stops early. **This is the exact wrong-end problem**: a fresh open of a
  90,000-entry log costs 10 requests and returns the OLDEST 2000 lines.
- `carry` (a ref keyed on `taskId`) preserves `LogState` across a re-run that continues the same
  logical tail (terminal transition, manual reconnect) and is cleared on the disabled early return.
- The generation discipline is already correct and is the model for anything new here: `recover`
  bumps `gen` BEFORE `controller.abort()`; the backfill failure path sets `fatal = true`, bumps
  `gen`, and only then aborts; the cleanup does `cancelled = true; gen++; controller.abort()`.
- `logBuffer.ts` is pure: `appendEntries` (dedupe on `seq <= maxSeq`, then per-stream line
  reassembly, then `capLines` drop-oldest at `MAX_LINES = 2000`), `visibleRows`, `finalizePartials`,
  `markDropped` (idempotent when the last row is already a marker; the marker row is emitted with
  `stream: 'stdout'` regardless of stream), `shouldFollow`, `collapseCR`, `stripAnsi`.
- `LogView.tsx` renders the header strip, the `disconnected` bar, the notice strip, and the scroll
  box. Its notice already keeps LINES and ENTRIES as separate units, which this slice must not
  regress.
- `LogTab.tsx` and `TaskLogPage.tsx` are presentational consumers of one `TaskLogStreamResult`.

**Confirmed: the 2026-08-09 spec's own Decision 7 predicted this slice.** It names "the honest ugly
corner", says "the real fix is a descending or `?before_seq=` mode on `GET /v1/tasks/{id}/logs`", and
files it as a proposed follow-up. That is the item being specified here, and the corner is still
present at HEAD exactly as described.

### Test lanes, verified

- `internal/api/api_test.go` and `internal/api/tasks_integration_test.go` are `//go:build integration`
  in `package api_test`, so every existing handler test needs Docker. `TestTaskLogs_Pagination` and
  `TestTaskLogs_LimitClamping` live there.
- `internal/api/pagination_test.go` is `package api` with NO build tag, so a pure helper in
  `internal/api` is testable in the default lane. This is why Decision 9 extracts the query parse.
- SPA fixtures for `/v1/tasks/:id/logs` are hand-written JSON object literals passed to
  `HttpResponse.json` (`JobDetailPage.test.tsx`, `TaskLogPage.test.tsx`, `api.test.ts`,
  `detailApi.test.ts`), NOT marshalled through the `TaskLogPage` type. They are honest simulators on
  the envelope axis, and they will need `prev_seq` added by hand.
- `web/e2e` contains no reference to logs at all. No Playwright change, verified by search rather
  than assumed.

## Decisions

Autonomous mode: there was no human to ask. Every question that would have been asked is recorded
with its options, the choice, and the reason.

**D1. Scope: which of the item's three pieces ship?**
Options: (a) piece 1 only, backend; (b) piece 1 plus its frontend consumption; (c) all three.
**Chosen: (b), per the conductor's brief, and independently the right cut.** Piece 1 alone would ship
a parameter with no consumer, which the repo's own history shows is how a contract drifts. Piece 2
(virtualization) is out because the retained-line cap is not the binding constraint once the view
opens at the right end, and because jsdom does no layout, so a windowing implementation cannot be
pinned in the unit lane at all; it would need the browser lane, which is a different slice. Piece 3
(export) is out because it is the most consequential surface in the item (raw subprocess output,
possibly secrets, moved to disk in one request) and deserves its own threat model, and because its
byte-exactness premise is foreclosed (see D22).

**D2. API shape for the tail.**
Options: (a) `?before_seq=` alone, with some sentinel meaning "from the end"; (b) `?order=desc` alone,
with `since_seq` re-interpreted; (c) `?order=desc` selecting direction plus `?before_seq=` as the
descending cursor; (d) a separate `?tail=1`.
**Chosen: (c).** (a) needs a magic sentinel (`before_seq=0`, or a bigint max) for the single most
common call, which is the tail-open; a sentinel that means "the opposite of what the number means" is
exactly the kind of contract that gets misread. (b) overloads `since_seq` with an inverted meaning,
so a client that sends `since_seq` with `order=desc` gets a surprising answer instead of an error -
the API would fail open on a client mistake. (d) adds a third vocabulary for something `order`
already names. (c) has one parameter per question: direction, and where in that direction to
continue. `order=desc` with no cursor is "the newest page", which is the one-call tail-open; a client
walks earlier by feeding `prev_seq` back as `before_seq`. `order=desc` is also the vocabulary the
backlog item itself proposed, so it is the project's own word for this.

**D3. Item order WITHIN a descending page: ascending or descending?**
Options: (a) ascending (oldest first) regardless of direction; (b) descending, matching the parameter
name.
**Chosen: (a), ascending, always, documented explicitly.** Three reasons. The existing renderers,
`appendEntries`'s per-stream line reassembly, and the SSE dedupe all consume an ascending sequence;
a descending payload would force every consumer to reverse, and a consumer that forgets produces
scrambled log lines that still look plausible. Reassembling a batch requires ascending order anyway,
so a descending page would be reversed and then reassembled - the reversal would simply move into
every client. And `order` names the SELECTION (which rows: `ORDER BY id DESC LIMIT n`), not the
presentation; the README says so in one sentence and `TestTaskLogs_DescendingTailReturnsTheNewestPageInAscendingOrder`
pins it. The cost is a genuine naming ambiguity, which is why it gets an explicit sentence and a test
rather than being left to the reader.

**D4. The backwards cursor field.**
Options: (a) reuse `next_seq` to mean "the cursor for the next page in the requested direction";
(b) add `prev_seq`; (c) an opaque base64 cursor like `page[T]`.
**Chosen: (b) `prev_seq`, always present, mirroring `next_seq`'s drained rule.** (a) makes one field
mean two different things depending on another parameter, and a client that gets the direction wrong
walks the wrong way silently. (c) is a bigger change than the endpoint needs and would fork the
envelope. With (b) each direction populates exactly one cursor and zeroes the other, so a
direction-confused client stops immediately instead of looping. `prev_seq` is the lowest `seq` in the
page, or 0 when `len(items) < limit` (the beginning of the log has been reached), which is exactly
the rule `next_seq` already uses at the other end. `0` is never a valid seq (`BIGSERIAL` starts at 1),
an assumption `next_seq` already makes.

**D5. Envelope migration to `page[T]`.** Refuted, see Refutation 1. **Chosen: keep
`{items, next_seq, prev_seq, total}`.**

**D6. Cross-parameter validation.**
Options: (a) ignore a parameter that does not apply to the requested direction; (b) 400.
**Chosen: (b), fail closed.** `since_seq` with `order=desc` is a 400
(`"since_seq is not valid with order=desc; use before_seq"`), and `before_seq` with the default
ascending order is a 400 (`"before_seq requires order=desc"`). Silently ignoring a cursor is how a
client ends up looping over page 1 forever while believing it is paging; the CLI's own
`printTaskLogs` has three separate guards against exactly that class, which is evidence the project
already pays for this failure mode.

**D7. `before_seq` value domain.**
Options: (a) accept `>= 0`, where 0 returns an empty page; (b) require `>= 1`.
**Chosen: (b), 400 on `< 1`.** `before_seq=0` is the empty query by construction and is far more
likely to be an unset client variable than an intention, and the contract already says to STOP when
`prev_seq` is 0. Rejecting makes the mistake loud at the moment it is made. The cost, stated: a
client that blindly feeds `prev_seq` back without checking for 0 gets a 400 rather than an empty
page. The README says explicitly not to do that, and the SPA honours it.

**D8. `order` vocabulary.**
**Chosen: an allow-list of exactly `asc` and `desc`**; anything else is a 400
(`"order must be asc or desc"`). An absent or empty `?order=` is `asc`, matching how `limit` and
`since_seq` already treat an empty value. This follows the repo's allow-list rule: a deny-list would
fail open on the next value someone adds.

**D9. Where validation lives.**
Options: (a) inline in `handleGetTaskLogs` next to the existing parsing; (b) extract a pure
`parseTaskLogQuery(url.Values) (taskLogQuery, error)` in `internal/api`.
**Chosen: (b).** The validation matrix is now two cursors, a direction, a limit and four
cross-parameter rules; inline, every case of it can only be tested through Docker, because the
handler's first act is a `GetTask` round trip. Extracted, the whole matrix is a default-lane test in
`package api` (the lane `pagination_test.go` already uses), and the handler keeps its shape. The
existing 404-before-400 precedence is deliberately PRESERVED: the parse is called where the current
parsing is, after the existence check, so no currently observable behaviour changes.

**D10. Store query shape.**
Options: (a) one statement with `($2 = 0 OR id < $2)`; (b) one statement with a bigint-max sentinel
bound in Go; (c) two statements, `GetTaskLogsTailPage` and `GetTaskLogsBeforePage`.
**Chosen: (c).** (a) risks losing the index range scan behind a parameterized OR, which is the one
thing this feature must not do on a table with no per-task volume cap. (b) leaks a magic constant
into Go. (c) is two four-line statements, each a clean backward index scan, and the Go side picks
one with an `if`. Both wrap the descending scan in a subquery that re-orders ascending, so the
ascending-items guarantee (D3) lives in ONE place and the handler's existing item-building loop is
reused verbatim rather than forked.

**D11. Authorization.**
**Chosen: identical to the existing read path, no more permissive, and deliberately not tightened
here.** `GET /v1/tasks/{id}/logs` is registered with `auth(...)` and no ownership check, so any
authenticated user can already read any task's logs; the 2026-08-09 spec records this and declines to
solve it, and so does this one. The tail parameter is not a new capability: every row it returns is
already reachable today by paging forward. Tightening cross-tenant log reads is a policy change that
must land on the endpoint as a whole, and doing it inside a paging slice would hide it.

**D12. How the SPA opens.**
Options: (a) keep the forward walk and add "load earlier" only; (b) walk BACKWARDS from the tail up
to `MAX_BACKFILL_PAGES`; (c) fetch ONE tail page, then let the user load earlier on demand.
**Chosen: (c).** (a) does not fix the reported problem. (b) preserves today's behaviour exactly for
logs that fit in 10 pages and fixes the wrong end for the rest, but it keeps costing up to 10
requests on EVERY open, including every task re-selection in the job-detail tab, and it makes "load
earlier" almost unreachable. (c) costs one request per open instead of up to ten, always shows the
right end, and makes the history depth a user decision. The cost, stated plainly: a 400-entry log
that today renders in full now opens showing the last 200 entries with a "Load earlier" control and a
notice carrying the real `total`. That is a visible reduction for medium logs, traded for a 10x
reduction in requests per open and correctness at the end that matters. A log whose first tail page
is short (`prev_seq == 0`) is complete, and shows no notice and no control at all.

**D13. Tail versus forward, as one rule.**
**Chosen: `maxSeq === 0` means "we hold nothing", so fetch the tail; otherwise page FORWARD from
`maxSeq`.** One predicate covers all four entries into `run`: a fresh mount (tail), a recovery after
a drop or close (forward from what we have), the terminal-transition reconciliation page (forward),
and a drop that happened before any line arrived (`maxSeq` still 0, so tail - which is the correct
answer, not an accident). The forward loop, its `MAX_BACKFILL_PAGES` bound and its `historyTruncated`
flag are unchanged on that path.

**D14. What "load earlier" fetches.**
**Chosen: one page of `BACKFILL_PAGE_SIZE` per click**, requested as
`?order=desc&before_seq=<minSeq>&limit=200`, never automatic, never a loop.

**D15. The seam between a prepended batch and the retained window.**
Options: (a) accept that a line straddling the seam renders as two rows, and disclose it; (b) join
exactly.
**Chosen: (b).** The 2026-08-09 spec's acceptance criterion 10 is "a line split across two entries
renders as one line"; silently breaking that once per click, per stream, would be a regression of a
property the project deliberately bought. The join is exact and cheap: the first COMPLETED line row
of a given stream in the window is, by construction, the text from the window's start to that
stream's first newline, so replacing its text with (the batch's trailing text) + (that row's text) is
the right answer, and the batch's own reassembly can reuse `appendEntries` against a fresh state. It
refuses when `evicted` is set (D16), which is the one case where "the first row" is no longer
contiguous with `minSeq`.

**D16. The retained-line cap versus prepending.**
Options: (a) raise `MAX_LINES`; (b) evict from the tail when prepending; (c) keep `MAX_LINES = 2000`
drop-oldest, and disable "load earlier" once the window is full.
**Chosen: (c).** (a) is virtualization's argument, which is out of scope. (b) would have the earlier
region fight live output for the same budget and would make a live tail unstable. With (c) the cap
stays exactly one number with one policy, a prepend that overflows keeps the newest lines and sets
the existing `evicted` flag, and `evicted` then permanently disables further loading - which is also
precisely the guard the seam join needs, so one predicate does two jobs. At 200 entries per click
against a 2000-line window that is roughly nine clicks of history, bounded and disclosed.

**D17. Aborting the "load earlier" request.**
Options: (a) give it an `AbortController` and pass the signal to `apiFetch`; (b) no signal; the
generation fence discards the response.
**Chosen: (b), and the reason is evidence, not taste.** `isAbortSignalRealmMismatch` in
`web/src/lib/api.ts` documents that under vitest's jsdom, a jsdom-constructed `AbortSignal` handed to
Node's native fetch throws a `TypeError` - which is why `apiStream` carries a fallback that re-issues
the request without the signal. `apiFetch` has no such fallback. Passing a signal to it would import
a known environment landmine into the exact lane this feature is tested in, to save one in-flight
response per click. The generation fence is the control instead: a result whose generation is no
longer current is discarded, and the control returns to enabled so the click is never silently lost.

**D18. Generation ordering for the new async continuation.**
**Chosen: the same rule as everything else in this file - end the generation before releasing the
resource.** `loadEarlier` captures `myGen` at issue time and writes state only when
`!cancelled && myGen === gen`. Nothing in `loadEarlier` releases a resource, so there is no new
abort ordering to get wrong; the existing `gen++`-then-`abort()` order in `recover` and in the
cleanup is unchanged. This is called out because `useTaskLogStream` is the file where the
generation-ordering invariant was rediscovered as a frontend bug, and because the failure mode here
would be quiet: a stale earlier page prepended into a DIFFERENT task's window, joined at a seam that
does not exist.

**D19. Scroll position when content is added above.**
**Chosen: a pure `preservedScrollTop(scrollTop, prevScrollHeight, nextScrollHeight)` helper in
`logBuffer.ts`, applied in a layout effect, with an injected callback for the component test.**
jsdom reports every geometry as 0, so a pixel assertion there is vacuously green. This follows
`shouldFollow`'s existing precedent exactly, and the 2026-08-09 spec's rule that the DECISION is
asserted, never the pixel.

**D20. Which docs change.**
**Chosen: README only, and specifically a new "Task log paging" subsection under Tasks plus a tail
variant of the Events backfill recipe.** The endpoint's parameters (`limit`, `since_seq`) and its
envelope are currently documented NOWHERE in the REST section - the REST table says only "Get task
log entries", and the contract lives implicitly in the Events backfill recipe and in the MCP tool
table. That gap is why every client re-derived the rule from the handler. The subsection is written
once and both recipes point at it.

**D21. Do the CLI, the Python SDK and MCP gain the parameter in this batch?**
Options: (a) all of them; (b) none; (c) the cheapest one.
**Chosen: (b), none, with a follow-up item proposed.** Each is a real product decision, not a
passthrough: `relay logs` prints a whole finished log and would need a `--tail N` flag with its own
completeness semantics (it currently reasons hard about whether it printed everything, and a tail
mode makes that claim false by design); the Python SDK's `task_logs` helper auto-paginates and would
need a documented direction; the MCP tool's schema is an LLM-facing contract with a known
inclusive/exclusive bug already filed against it. Bundling any of them here would put an unreviewed
semantic change inside a paging slice. The server side is additive, so all three keep working
unchanged today.

**D22. Item disposition.**
Options: (a) amend the item to strike piece 1; (b) close it and re-file the remainder.
**Chosen: (b), close and re-file, and the re-filed items must exist BEFORE the close lands.** The
project's close flow is a `git mv` into `docs/backlog/closed/` plus stamped frontmatter, run through
`/backlog close`; a partially-completed item left open with one third struck through is exactly the
malformed state that flow exists to prevent, and the item's own text says to treat each piece as
separately shippable. The export item must carry the byte-exactness foreclosure verbatim: the agent
normalises `\r\n` to `\n` in `chunkWriter` before a chunk is sent
(`docs/superpowers/specs/2026-08-25-windows-crlf-log-lines.md`, Part 2), so stored bytes are not a
byte-exact copy of subprocess output, and an export must not be written against a guarantee that no
longer holds.

## API design

### Parameters

`GET /v1/tasks/{id}/logs`

| Parameter | Default | Rule |
|---|---|---|
| `limit` | 50 | 1..200. Out of range, unparseable: 400 `limit must be 1..200`. Unchanged. |
| `order` | `asc` | Allow-list `asc` or `desc`. Absent or empty is `asc`. Anything else: 400 `order must be asc or desc`. |
| `since_seq` | 0 | Ascending only, exclusive (`id > since_seq`). Negative or unparseable: 400 `since_seq must be a non-negative integer`. Present with `order=desc`: 400 `since_seq is not valid with order=desc; use before_seq`. Unchanged in the ascending direction. |
| `before_seq` | none | Descending only, exclusive (`id < before_seq`). Absent with `order=desc` means "the newest page". Less than 1 or unparseable: 400 `before_seq must be a positive integer`. Present without `order=desc`: 400 `before_seq requires order=desc`. |

Authorization, rate limiting and the 404-for-an-unknown-task behaviour are unchanged, and the
existence check still runs before parameter validation.

### Envelope

```json
{
  "items":    [ { "seq": 41, "stream": "stdout", "content": "...", "created_at": "..." } ],
  "next_seq": 0,
  "prev_seq": 41,
  "total":    94312
}
```

- `items` is **always ascending by `seq`**, in both directions. `order` selects WHICH rows, not their
  order within the page.
- `next_seq` is the ascending cursor: the last row's `seq`, or 0 when the page was short (drained).
  In a descending response it is always 0.
- `prev_seq` is the descending cursor: the FIRST row's `seq` (the lowest), or 0 when the page was
  short (the beginning of the log has been reached). In an ascending response it is always 0.
- `total` is the per-task row count, unchanged. It counts ENTRIES, not lines.
- All four keys are always present, including on an empty page.

### Examples

Open at the tail, then walk earlier:

```
GET /v1/tasks/{id}/logs?order=desc&limit=200
  -> items [93_913 .. 94_312 by seq, ascending], next_seq 0, prev_seq 93_913, total 94_312

GET /v1/tasks/{id}/logs?order=desc&before_seq=93913&limit=200
  -> the 200 entries immediately older, ascending; prev_seq is the lowest of those, or 0 at the start
```

Continue forward from a tail page (this is what the SSE join and the recovery path do):

```
GET /v1/tasks/{id}/logs?since_seq=94312&limit=200
  -> anything appended since, ascending, next_seq 0 when drained
```

Rejected:

```
GET ...?order=desc&since_seq=10     -> 400
GET ...?before_seq=10               -> 400
GET ...?order=desc&before_seq=0     -> 400
GET ...?order=DESC                  -> 400
```

## Store query design

Two new statements in `internal/store/query/tasks.sql`, beside `GetTaskLogsPage`. Both scan
`idx_task_logs_task_id_id` backwards over an equality-prefixed range and re-order ascending in an
outer select, so the ascending-items guarantee lives in the SQL rather than in each caller.

```sql
-- name: GetTaskLogsTailPage :many
-- The NEWEST $2 rows for a task, returned ASCENDING. The inner ORDER BY id DESC
-- is what makes this a bounded backward index scan on idx_task_logs_task_id_id
-- rather than a full read of the task's log; the outer ORDER BY is the response
-- contract (items are ascending in both directions).
SELECT t.id, t.task_id, t.stream, t.content, t.created_at
FROM (
    SELECT l.id, l.task_id, l.stream, l.content, l.created_at
    FROM task_logs l
    WHERE l.task_id = $1
    ORDER BY l.id DESC
    LIMIT $2
) AS t
ORDER BY t.id;

-- name: GetTaskLogsBeforePage :many
-- The $3 rows immediately OLDER than $2 (exclusive), returned ASCENDING.
SELECT t.id, t.task_id, t.stream, t.content, t.created_at
FROM (
    SELECT l.id, l.task_id, l.stream, l.content, l.created_at
    FROM task_logs l
    WHERE l.task_id = $1 AND l.id < $2
    ORDER BY l.id DESC
    LIMIT $3
) AS t
ORDER BY t.id;
```

Implementation hazards the plan must carry:

- **Alias the subquery and qualify every column.** `AppendTaskLog`'s comment records that sqlc's
  analyzer cannot resolve a bare `id` across CTEs without the alias and qualified references; the
  same applies to this derived table.
- **`make generate` rewrites line endings across every generated file on this CRLF repo.** Follow
  the CLAUDE.md procedure: `git diff --ignore-all-space`, keep only the real content change, revert
  LF-only hunks, and check `git ls-files --eol` on the touched paths. Do not conclude "nothing to
  revert" from `git diff` alone.
- **No migration.** `idx_task_logs_task_id_id` already exists (000018). Adding one would be a
  no-op index and a schema-version bump for nothing.
- `CountTaskLogs` is unchanged and still runs once per request, so a descending page costs the same
  two round trips as an ascending one.

The handler picks the statement:

```
asc             -> GetTaskLogsPage(task, since_seq, limit)
desc, no cursor -> GetTaskLogsTailPage(task, limit)
desc, before    -> GetTaskLogsBeforePage(task, before_seq, limit)
```

and then runs the EXISTING item loop unchanged; only the cursor computation forks (`next_seq` from
the last item in ascending mode, `prev_seq` from the first item in descending mode, each zeroed when
`int32(len(items)) < limit`).

## Frontend design

### The state machine

`useTaskLogStream` keeps its shape. Only the first history fetch changes, and one user-driven
continuation is added.

```
effect(taskId, live, enabled, fetchImpl, manualRetry):
  if !enabled or taskId == '':
      carry = null; reset view; status = 'idle'; return

  logState = carry-for-this-task ?? empty
  run()

run():
  myGen = ++gen; buffering = true; pending = []; controller = new AbortController()

  if live:
      await openStream(myGen)        # UNCHANGED, and still FIRST. Order is load-bearing.

  if logState.maxSeq == 0:           # we hold nothing: open at the END
      page = GET ?order=desc&limit=200
      ingest(page.items)             # ascending within the page; appendEntries unchanged
      earlierComplete = (page.prev_seq == 0)
      total = page.total
  else:                              # recovery / reconciliation: forward, UNCHANGED
      forward loop from logState.maxSeq, bounded by MAX_BACKFILL_PAGES

  buffering = false; ingest(pending); pending = []; flushNow()
  status = live ? 'live' : (carried ? 'ended' : 'history')

loadEarlier():                       # user click only
  if loadingEarlier or !canLoadEarlier: return
  myGen = gen; before = logState.minSeq; loadingEarlier = true
  page = await GET ?order=desc&before_seq=before&limit=200      # no AbortSignal, see D17
  loadingEarlier = false
  if cancelled or myGen != gen: return                          # discarded; control re-enables
  setLogState(prependEntries(logState, page.items))
  earlierComplete = (page.prev_seq == 0); total = page.total; flushNow()

cleanup: cancelled = true; gen++; controller.abort(); clear timers      # UNCHANGED
```

Why this stays gapless, restated for the tail direction: the subscription is opened first, so every
chunk written after `T0` reaches the buffer; the tail page read at `T1 > T0` returns the newest rows
as of `T1`; `appendEntries` discards buffered frames with `seq <= maxSeq`. A chunk written in
`(T0, T1]` is therefore either in the page (and dropped from the replay) or above `maxSeq` (and
appended). Nothing is fetched twice and nothing between the page and the first applied frame is
missed. The bound the tail direction deliberately does NOT cover is everything OLDER than the page,
which is the point of the feature and is disclosed by the notice and the "Load earlier" control.

**No gap detection is added.** `seq` is non-contiguous and always will be; the only drop signals
remain the `dropped` frame and an unexpected close.

### `logBuffer.ts` additions (pure)

```
LogState gains:
  minSeq: number            # lowest seq accepted; 0 when the window is empty

prependEntries(state, entries): LogState
  # entries ascending, every seq < state.minSeq, contiguous with the window by construction
  1. if state.evicted or entries empty -> return state unchanged
  2. batch = appendEntries(createLogState(), entries)     # reuse the tested reassembly
  3. for each stream with a dangling batch partial:
       target = first row in state.lines with kind === 'line' and that stream
                (a marker row is kind 'marker' and is skipped - markDropped emits
                 markers with stream 'stdout' regardless of the real stream)
       if target exists: target.text = collapseCR(batchPartial.text + target.text)
       else:             state.partials[stream] = batchPartial + existing partial text
  4. lines = re-keyed(batch.lines) ++ state.lines        # keys from state.nextKey
  5. capLines(lines)                                     # overflow sets evicted
  6. minSeq = entries[0].seq

appendEntries: sets minSeq on the first accepted entry when minSeq is 0. Otherwise unchanged.
preservedScrollTop(scrollTop, prevHeight, nextHeight) = scrollTop + (nextHeight - prevHeight)
```

Step 3 is the exact seam join (D15). Step 1 is why the join is always safe: once drop-oldest has
evicted the front of the window, the first row is no longer the continuation of `minSeq`, and the
same condition permanently disables the control that would produce another prepend.

### `TaskLogStreamResult` additions

`canLoadEarlier` (`!earlierComplete && !evicted && lines.length < MAX_LINES`), `loadingEarlier`,
`earlierComplete`, `loadEarlier()`.

`historyTruncated` keeps its meaning on the forward path and is no longer set on a fresh open, since
a fresh open no longer walks forward from 0.

### `LogView.tsx`

- A "Load earlier" row is the FIRST child inside the scroll box: a button when `canLoadEarlier`,
  "Loading earlier..." while `loadingEarlier`, and nothing at all when `earlierComplete` and nothing
  has been evicted (a complete log must not grow a control that implies missing history).
- The notice strip keeps its existing unit discipline (retained LINES and server-side ENTRIES are
  different units and stay named separately) and resolves in this order, first match wins:
  1. `evicted` -> the existing `Earlier output not shown.`
  2. `!earlierComplete` -> `Showing the most recent <rows> lines of <total> log entries.`
  3. `historyTruncated` -> the existing forward-walk wording, which can now only appear after a
     recovery that hit `MAX_BACKFILL_PAGES`.
  4. otherwise no notice. A log whose first tail page was short shows neither a notice nor a control.
- On a prepend with follow off, a layout effect applies `preservedScrollTop` so the user's viewport
  does not jump, and calls an injected `onPrependAdjust` seam for the component test.
- Nothing else changes. Log content is still rendered as React text children, never as HTML, and is
  still never passed to any `console` method.

### `web/src/jobs/api.ts`

- `TaskLogPage` gains `prev_seq: number`, required. Existing MSW fixtures are hand-written literals
  and must gain `prev_seq: 0`; that is mechanical and in scope.
- New `getTaskLogsDesc(taskId, beforeSeq = 0, limit = BACKFILL_PAGE_SIZE)` sends `order=desc`, plus
  `before_seq` only when `beforeSeq > 0`.
- `getTaskLogs` (forward) is UNCHANGED, including its query string. A test pins that it still sends
  no `order`.

## Acceptance criteria

Each criterion names the test that proves it and the lane it runs in. Test bodies from a plan are
guesses; where a criterion could pass vacuously, the non-vacuity proof is stated.

**Go, default lane (`package api`, `go test ./internal/api/...`)**

1. An absent or empty `order` parses as ascending with today's exact defaults ->
   `TestParseTaskLogQuery_DefaultsToAscendingFromZero`.
2. `order` is an allow-list: `asc` and `desc` pass, `DESC`, `descending`, `-id` and `1` are 400 ->
   `TestParseTaskLogQuery_RejectsAnUnknownOrder`. Non-vacuity: the same table asserts the two
   accepted values in the same call path.
3. `since_seq` with `order=desc` is a 400, and `before_seq` without `order=desc` is a 400 ->
   `TestParseTaskLogQuery_RejectsACursorFromTheWrongDirection`.
4. `before_seq` of `0`, `-1` and `abc` are each a 400; `1` is accepted ->
   `TestParseTaskLogQuery_RejectsMalformedBeforeSeq`.
5. The limit clamp is identical in both directions ->
   `TestParseTaskLogQuery_LimitClampIsTheSameInBothDirections`.
6. `order=desc` with no cursor is a valid tail request, not an error ->
   `TestParseTaskLogQuery_DescWithNoCursorIsTheTailRequest`.

**Go, integration lane (`-tags integration -p 1 ./internal/api/...`)**

7. A descending tail page returns the newest `limit` rows, ASCENDING within the page ->
   `TestTaskLogs_DescendingTailReturnsTheNewestPageInAscendingOrder`. Non-vacuity: seed distinct
   contents and assert the exact ordered slice, not a length.
8. **The non-contiguous-`seq` case has a named test** ->
   `TestTaskLogs_NonContiguousSeqTailIsExactlyTheNewestRowsOfThatTask`: seed task A and a SECOND task
   B interleaved so A's ids are non-contiguous, then assert A's tail page is exactly A's newest rows
   and that no arithmetic on `seq` or on `total` would have produced that set. This is the item's
   required test.
9. A backwards walk to the beginning yields exactly the same rows, once each, as the forward walk ->
   `TestTaskLogs_DescendingWalkEqualsTheForwardWalkWithNoGapAndNoDuplicate` at `limit=2` over 5 rows.
   This is the strongest single property in the slice and it also pins `prev_seq`'s cursor rule.
10. `prev_seq` is 0 on a short page and the page's lowest `seq` on a full one; `next_seq` is 0 in
    every descending response and unchanged in every ascending one ->
    `TestTaskLogs_CursorsAreDirectionExclusive`.
11. All four envelope keys are present on an empty log ->
    `TestTaskLogs_EnvelopeCarriesAllFourKeysOnAnEmptyLog`, mirroring
    `TestPageEnvelope_AllThreeKeysArePresentOnAZeroValuePage`.
12. The wire rejects the same combinations the parser does, and an unknown task is still a 404 even
    with a malformed `order` -> `TestTaskLogs_DescValidationOverTheWire`,
    `TestTaskLogs_UnknownTaskIs404AheadOfParameterValidation`.
13. Reading the tail needs exactly the auth the forward read needs: no token is a 401, an ordinary
    non-admin user succeeds -> `TestTaskLogs_TailUsesTheSameAuthorizationAsTheForwardRead`.
14. `TestTaskLogs_Pagination` and `TestTaskLogs_LimitClamping` still pass unmodified. A diff to
    either is a signal that the change was not additive.

**Vitest (`web`, `npm test`)**

15. A line straddling the seam becomes ONE row after a prepend ->
    `logBuffer.test.ts`, "prependEntries joins a line that straddles the seam". Non-vacuity: assert
    the exact resulting row text and the exact row count, so an implementation that appends the
    fragment as its own row fails.
16. Stdout and stderr seams are independent, and a leading drop marker is not mistaken for a stdout
    line -> "prependEntries keeps the two streams' seams apart",
    "prependEntries does not join into a marker row".
17. `prependEntries` refuses once `evicted` is set, and an overflowing prepend keeps the NEWEST lines
    and sets `evicted` -> "prependEntries refuses after eviction", "prependEntries that overflows the
    cap keeps the newest lines". Non-vacuity: assert the first retained line, not the length.
18. `minSeq` falls on a prepend and `maxSeq` does not move -> "prependEntries lowers minSeq only".
19. `preservedScrollTop` arithmetic, including a shrinking container ->
    "preservedScrollTop keeps the viewport anchored".
20. The hook opens with EXACTLY ONE history request and it is the descending tail ->
    `useTaskLogStream.test.tsx`, "opens at the tail with one request". Non-vacuity: assert the exact
    request count and the exact query string, so a leftover forward walk fails.
21. The subscription is still opened BEFORE the first history request ->
    the existing ordering test, retained and re-proved RED by swapping the two statements.
22. A frame arriving during the tail fetch is applied once, after it, and a frame whose `seq` is in
    the page appears once -> "buffered frames replay after the tail page, deduped".
23. A recovery after a drop pages FORWARD from `maxSeq`, and a drop before any line pages the tail ->
    "recovery direction follows what we hold". Paired positive control in one test.
24. `loadEarlier` issues exactly one request, prepends exactly one page, and is a no-op while one is
    in flight -> "load earlier fetches one page per click".
25. A `loadEarlier` result that arrives after a task switch never appears in the new task's rows, and
    one discarded by a recovery leaves the control enabled -> "a stale earlier page is discarded",
    "a discarded earlier page re-enables the control". These two are the generation-fence tests.
26. `canLoadEarlier` is false when `prev_seq` was 0, when `evicted` is set, and at `MAX_LINES` ->
    "canLoadEarlier is false in each of the three terminal cases".
27. The terminal-transition reconciliation still sends `since_seq` and no `order` ->
    "the reconciliation page is still a forward request".
28. `getTaskLogs` still sends `since_seq` and no `order`; `getTaskLogsDesc` sends `order=desc` and
    omits `before_seq` on a tail request -> `api.test.ts` / `detailApi.test.ts`.
29. `LogView` renders the Load earlier control only when `canLoadEarlier`, calls `loadEarlier` once
    per click, and shows the tail notice with lines and entries as separate units ->
    `LogView.test.tsx`.
30. Log content still never reaches any `console` method across a full mount, tail, prepend, drop and
    unmount cycle -> the existing `logSecrecy.test.tsx` cycle, extended with a prepend.

**Docs**

31. README documents `order`, `before_seq`, `prev_seq`, the ascending-items rule and the tail recipe
    in one place, and the Events backfill recipe gains a tail variant that keeps subscribe-first.

**Gates**

32. `make test`, both `go vet` lanes, `-tags integration -p 1 ./internal/api/...`, `npm test` and
    `tsc -b` in `web/`, and `-race` in the `golang:1.26` container. `git checkout -- web/dist/` before
    the PR is assembled: `web/dist` is tracked but stale and a build dirties it.

## Out of scope

| Candidate | Why out |
|---|---|
| Row virtualization (item piece 2) | The retained-line cap stops being the binding constraint once the view opens at the right end, and jsdom does no layout, so a windowing implementation cannot be pinned in the unit lane at all. It needs a dependency decision, a measurement pass and the browser lane. Proposed as a follow-up. |
| Log export or download (item piece 3) | The most consequential surface in the item: raw subprocess output, possibly secret-bearing, in one file. Needs its own threat model and its own authorization argument, and its byte-exactness premise is foreclosed. Proposed as a follow-up. |
| ANSI colour rendering, in-log search | The item's own Notes park both behind piece 1. Piece 1 ships here, so they become fileable, not buildable in the same slice. |
| `?order=desc` in the CLI, the Python SDK or MCP | D21. Each is a product decision with its own completeness semantics. |
| Migrating the logs envelope to `page[T]` / `next_cursor` | Refutation 1. It would break four clients at once for no gain. |
| Tightening authorization on the logs endpoint | D11. Pre-existing, orthogonal, and it must land on the endpoint as a whole. |
| A per-line length cap in `logBuffer` | `bug-2026-08-25-spa-log-line-has-no-length-cap` is open and is about partial LENGTH, not line COUNT. The seam join concatenates two fragments once per click, which does not change that item's exposure and does not fix it either. |
| A server-side per-task log volume cap | `bug-2026-08-14-task-logs-have-no-per-task-volume-cap`, unchanged. This slice makes a huge log cheaper to READ, which slightly reduces the pressure to solve it, not more. |
| A new index or migration | Already covered by `idx_task_logs_task_id_id` (000018). |
| Playwright / `web/e2e` | Verified: the e2e suite contains no reference to the logs surface. |

## Backlog items this closes or partially closes

`idea-2026-08-09-task-log-tail-and-paging-improvements` - **piece 1 of 3 fully; pieces 2 and 3 not at
all.**

Recommendation: **close it via `/backlog close` when this PR merges, AND file the two follow-up items
below first**, so no content is lost in the move. Rationale in D22: an item left open with one third
struck out is the malformed state the close flow exists to prevent, its own text says each piece is
separately shippable, and the export item in particular must inherit the byte-exactness foreclosure
verbatim rather than referring back to a closed file. The `git mv` into `docs/backlog/closed/` is
required scope for this PR, not optional cleanup.

Nothing else is closed. This slice does not touch
`bug-2026-08-25-spa-log-line-has-no-length-cap`, `bug-2026-08-14-task-logs-have-no-per-task-volume-cap`,
`bug-2026-08-26-mcp-since-seq-is-advertised-inclusive-and-is-exclusive`,
`idea-2026-08-09-sse-revoked-token-keeps-streaming`, or
`bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero`.

## Proposed follow-up backlog items

Proposals, not filed. The first two are required to preserve the closing item's content.

1. **`idea-2026-09-01-task-log-row-virtualization`** (required). Carry over the item's piece 2
   verbatim plus what this slice learned: with tail-open shipped, the argument for virtualization
   changes from "reach the end" to "hold more than 2000 lines at once", and the concrete consequence
   is that "Load earlier" is disabled once the window is full (D16), which is the user-visible cost
   of not virtualizing. Note the lane problem: jsdom does no layout, so the acceptance evidence has
   to come from the Playwright lane or from a pure windowing-arithmetic helper plus a browser check.
2. **`idea-2026-09-01-task-log-export-endpoint`** (required). Carry over the item's piece 3 verbatim,
   including both the authorization constraint (an export must be no more permissive than the paged
   read) and **the byte-exactness foreclosure**: the agent normalises `\r\n` to `\n` in `chunkWriter`
   before a chunk is sent (`docs/superpowers/specs/2026-08-25-windows-crlf-log-lines.md`, Part 2), so
   stored bytes are not a byte-exact copy of the subprocess output; the trade was taken deliberately
   and an export must not be written against a guarantee that no longer holds. Add what this slice
   establishes: a descending mode now exists, so a server-side stream is no longer blocked on paging.
3. **`idea-2026-09-01-tail-mode-for-the-cli-python-sdk-and-mcp`**. `relay logs --tail N`, a direction
   argument on the SDK's log paging, and the MCP tool's schema. Each needs its own completeness
   semantics: `printTaskLogs` currently reasons carefully about whether it printed everything, and a
   tail mode makes that claim false by design, so the diagnostic wording has to change with it. Note
   the already-filed inclusive/exclusive schema bug should be fixed in the same pass.
4. **`idea-2026-09-01-in-log-search`** (optional, from the item's Notes). Now unblocked by tail
   paging. Note the interaction it was parked on: filtering 2000 retained lines is not filtering the
   log, so a useful search is a server-side one, which is a bigger feature than it looks.
5. **`idea-2026-09-01-ansi-colour-rendering-in-the-log-view`** (optional, from the item's Notes). Low
   value. `stripAnsi` currently removes the sequences; rendering them is an SGR state machine.

## Risks

- **The gapless join, restated as a risk.** There is a theoretical hole shared by BOTH directions and
  not introduced here: `task_logs.id` is assigned at insert, but visibility is ordered by commit, so
  a chunk that took a lower id and committed later than the page read would be dropped by the
  `seq <= maxSeq` dedupe. It is not reachable in practice for one task, because a task's chunks are
  appended by that task's single assignee on one gRPC recv goroutine, one statement at a time. The
  tail read is a single query with the same exposure window as the forward walk's last page, so this
  slice does not widen it. Stated so nobody discovers it later and attributes it to the tail mode.
- **`order=desc` returning ascending items is the one genuinely confusing thing in this API.** It is
  mitigated by an explicit README sentence and by criterion 7, and the alternative (descending items)
  moves a reversal into every consumer where forgetting it produces plausible-looking scrambled logs.
  A reviewer who dislikes it should say so at the spec gate, not after the tests are written.
- **A medium-length log now opens showing less than it did** (D12). This will read as a regression to
  whoever notices it first. The notice text and the Load earlier control are the entire mitigation
  and must be implemented as specified, not paraphrased.
- **The seam join is the most intricate new logic in the slice**, and its failure mode is quiet: a
  wrong join corrupts exactly one line per click and still renders. Criteria 15 to 18 exist for this,
  and the marker-row case (16) is the one a naive implementation gets wrong, because `markDropped`
  emits its marker with `stream: 'stdout'` regardless of which stream dropped.
- **`prev_seq` becoming a required field in the SPA's `TaskLogPage`** means every existing MSW fixture
  must gain it. They are hand-written literals, so the compiler will not find them; a missing one
  decodes as `undefined`, which is not `0`, which leaves "Load earlier" enabled and produces an extra
  request in a test that did not expect one. Sweep them deliberately rather than waiting for a
  failure to point at one.
- **The lane split hides a class of half-finished change.** All wire-level behaviour of this endpoint
  is behind `//go:build integration`, so a signature or envelope change can be fully green in the
  default lane. The integration lane must actually be run before this is called done, not inferred
  from the default lane being green.
