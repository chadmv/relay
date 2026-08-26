# relay logs prints nothing - envelope decode, paging, and a loud failure path

Date: 2026-08-26
Status: design approved (autonomous gate mode)
Backlog item: `docs/backlog/bug-2026-08-25-relay-logs-prints-nothing-envelope-drift.md`
Owner phase: Phase 1 (spec) -> `relay-backend-engineer` under TDD

## Problem

`printTaskLogs` in `internal/cli/logs.go` decodes `GET /v1/tasks/{id}/logs` into
`[]struct{ Stream, Content string }`. `handleGetTaskLogs` in `internal/api/tasks.go` has written
`{"items": [...], "next_seq": N, "total": N}` since `a90c727` (2026-05-08). `encoding/json` cannot
unmarshal an object into a slice, `Client.Do` returns that error faithfully, and `printTaskLogs`
bare-returns on any error under the comment "Errors are silently ignored - best-effort output".

Net effect: `relay logs <job-id>` prints zero log lines for every task of every job, and says
nothing about why. Three and a half months, user-facing subcommand, whole suite green.

Three defects compound here and the fix must address all three, because any one of them left in
place reproduces a variant of the same silence:

1. **Shape.** The decode target does not match the response.
2. **Completeness.** `next_seq` is ignored, so even a corrected decode returns at most the first
   page (default 50, or 200 if the client asks) and silently truncates a long log.
3. **Silence.** The swallow converts a loud shape mismatch into no output and exit 0. This is the
   general defect: any future breakage of this call is equally invisible.

## What was verified, and what the backlog item got wrong

Every claim below was checked against the tree at `27b6566`. The item is substantially correct on
the defect itself; five of its supporting claims are wrong or stale.

### Confirmed

- `printTaskLogs` decodes a bare slice; `handleGetTaskLogs` writes the three-key envelope. Both
  citations resolve.
- `Client.Do` (`internal/relayclient/client.go`) returns the decode error unchanged. The loss is
  entirely at the call site.
- `relayclient.PageEnvelope[T]` is keyed on `next_cursor` and `FetchAllPages` walks `?cursor=`.
  It does not apply to this endpoint unmodified. Confirmed.
- `since_seq` is **exclusive**. `GetTaskLogsPage` is `WHERE task_id = $1 AND id > $2 ORDER BY id
  LIMIT $3`, and the generated `internal/store/tasks.sql.go` doc comment agrees with its own source.
  A client that pages with `since = lastSeq + 1` skips a row whenever ids are contiguous.
- `next_seq` semantics: the handler assigns `nextSeq` from the last returned row, then overwrites it
  with `0` when `len(items) < limit`. So a full page always returns a non-zero cursor, including the
  page that happens to exhaust the table.
- The endpoint is auth-only with no per-owner gate. Unchanged by this work.

### Refuted or corrected

1. **"There is no consumer of `next_seq` anywhere in Go outside the handler that writes it."**
   False as written. `grep -rn next_seq --include=*.go` returns
   `internal/worker/handler_tasklog_e2e_integration_test.go` (which implements the full
   `since_seq` -> `next_seq == 0` loop against the real handler), `internal/api/tasks_integration_test.go`,
   and `internal/mcp/task_logs_test.go`. The defensible claim is "no *production* consumer", which
   is true. The correction matters: the worker integration test is an existing, working Go
   implementation of exactly the loop this spec specifies, and the implementer should read it rather
   than invent one.

2. **"The stale fixture is at `internal/cli/logs_test.go:47`."** Undercounts. There are **four**
   fake servers hand-writing the bare array, not one and not three: `fakeJobServer`,
   `fakeCompletedJobServer`, `fakeRaceJobServer`, and `fakeOverlapJobServer`. Any fixture fix that
   converts three leaves a fourth test asserting agreement with a shape the server stopped sending.
   Cite by symbol; the fourth server was added after the item's line numbers were taken.

3. **"Both call sites are affected"** is true but invites two fixes. Both `onSubscribed` and the SSE
   `task` handler call the same `printTaskLogs`. This is **one defect with two callers**, and one
   decode fix covers both. The existing suite already exercises both paths
   (`fakeCompletedJobServer` reaches `onSubscribed`; `fakeJobServer` reaches the stream handler), so
   the fixture flip reddens both without new call-site-specific tests.

4. **"CRLF normalisation is filed as Part 2 of the CRLF item, not as scope here."** Stale by one
   commit. Both parts of `bug-2026-08-25-windows-crlf-log-lines-render-blank` **shipped** on
   2026-08-25 (`27b6566`, PR #154); the item is closed under `docs/backlog/closed/`. The agent's
   `chunkWriter` now converts CRLF to LF at the source with a hold-back for the straddled case, and
   the shipped guarantee is an equality on the concatenation of emitted payloads, not the weaker
   "chunks contain no CRLF" the item originally proposed. So the CLI's CRLF exposure for new output
   is already closed upstream; what remains is historical rows and un-upgraded agents.

5. **"[[idea-2026-08-23-cli-tests-never-hit-real-server]] is worth re-prioritising off `low`."**
   Already done. That item is `priority: medium`, raised on 2026-08-25 citing this very bug. No
   action needed; the recommendation below is about scope, not priority.

### Found while verifying, not in the item

- **A `task_logs` row is a chunk, not a line.** `handleTaskLog` stores `string(chunk.Content)`
  verbatim, and `chunkWriter` emits whatever `os/exec` hands it. A row can contain many newlines and
  can end mid-line. `printTaskLogs` formats one row as `[<task> <stream>] <content>` plus a
  newline, so it prefixes only the first line of a multi-line chunk and injects a newline into any
  line that straddles a chunk boundary. The web client knows this and reassembles (`logBuffer.ts`
  splits on `\n` and holds partials); the CLI does not. This is a real rendering defect, it is
  independent of this bug, and it subsumes the interior-CR question the item wanted decided here.
  See "Rendering: out of scope, and why".
- **README oversells the command.** "Stream task logs for a running or completed job via
  Server-Sent Events" is wrong about the mechanism a user will feel. The CLI subscribes to
  `/v1/events?job_id=`, which carries status frames only - `task_log` frames require an explicit
  `task_id` subscription (README's own event table says so). Log content is fetched over REST once
  a task goes terminal, so output arrives in a burst per finished task, never live. The `LogsCommand`
  usage string ("tail logs until job completes") makes the same claim.
- **The MCP tool advertises the wrong cursor semantics.** `getTaskLogsArgs.SinceSeq` in
  `internal/mcp/task_logs.go` is documented to the model as "Return only log entries with seq >=
  this value", while the SQL is `id > $2`. Out of scope here (different client, different package);
  drafted as a follow-up item below.

## Design

### 1. The decode, and where the type lives

Add a package-private named type in `internal/cli/logs.go`:

```
type taskLogPage struct {
    Items   []taskLogEntry `json:"items"`
    NextSeq int64          `json:"next_seq"`
    Total   int64          `json:"total"`
}

type taskLogEntry struct {
    Seq     int64  `json:"seq"`
    Stream  string `json:"stream"`
    Content string `json:"content"`
}
```

`created_at` is deliberately not decoded: the CLI does not print it, and an unused field is a
maintenance claim the CLI cannot keep. `Seq` is decoded because the diagnostics below name the last
seq printed.

**Rejected: a seq-keyed sibling in `internal/relayclient`.** Two reasons, and the second is
decisive.

- One caller. A generic helper with exactly one caller moves the loop's termination bound behind a
  package boundary where the one thing a reviewer needs to see - what stops it - is no longer next
  to the thing it stops.
- **`FetchAllPages` returns `[]T`.** A seq-keyed sibling with the same signature would materialize
  the entire log in memory before printing a byte of it. `relay logs` on a multi-hundred-megabyte
  task log would then be an OOM where the local loop is O(one page). The CLI must stream to the
  writer, which is a different shape from "fetch all pages", not a variant of it.

Promotion criterion, stated so the next slice does not have to re-derive it: if a second consumer of
a seq-paged endpoint appears **and** it also wants streaming rather than accumulation, promote the
loop to `relayclient` then. Not before.

### 2. The paging loop

```
since := int64(0)
pages := 0
for {
    fetch page at ?since_seq=<since>&limit=<relayclient.PageRequestLimit>
    print every item in order
    pages++
    if len(page.Items) == 0 { break }         // defensive: nothing further can be printed
    if page.NextSeq == 0 { break }            // server says drained
    if page.NextSeq <= since { stop, loudly } // cursor did not advance
    if pages >= maxLogPages { stop, loudly }  // cap
    since = page.NextSeq
}
```

Reuse `relayclient.PageRequestLimit` (already `200`, already documented as "matches the server's
max"). Do not introduce a second literal.

**Termination argument.** `since_seq` is exclusive and `id` is ascending, so each iteration strictly
advances past every row already printed: no duplicate, no gap. On a correct server the loop
terminates because either the page is short (`next_seq == 0`) or the cursor strictly increases
toward a finite maximum id.

**The exact-multiple case is the one to get right.** With 400 rows and `limit=200`, page 1 and page
2 are both full, so both carry a non-zero `next_seq`; page 3 returns zero items and `next_seq == 0`.
Three requests, 400 lines, no duplicate and no drop. A loop that breaks on `len(items) < limit`
instead of on `next_seq` happens to agree here but is not equivalent in general - it re-derives a
rule the server already applied, and it desynchronizes the moment the server's drain rule changes.
Break on `next_seq`. The `len(items) == 0` arm is defensive only: a correct handler assigns
`next_seq` from a returned row, so an empty page always carries `next_seq == 0` and that arm never
fires against the real server. It is ordered first because it is the one arm that is right no matter
what the cursor claims.

**Two independent bounds, and both are needed.** An unbounded `for` over a server-supplied cursor is
a hang, not a fix. The cursor is a value the CLI does not control - provenance of the value says
nothing about who controls its content or the timing of the writes behind it.

- `next_seq <= since` catches the non-advancing cursor (constant, decreasing, or replayed) on the
  **second** request, which is the shape that loops forever at zero cost to the server.
- `maxLogPages` catches the ever-increasing-but-never-draining server, which the first guard cannot
  see. Set `maxLogPages = 10000`, a package var rather than a const so a test can shrink it - the
  project's documented testability-override convention (`saveConfigFn`, `configFilePathFn`,
  `readPasswordFn`). 10000 pages x 200 rows is 2,000,000 log rows, which is a hang bound rather than
  a product limit: no real task log approaches it, and reaching it means the server is misbehaving.

Both stops are loud (see 3). Silent truncation at a cap is the same defect class as the bug being
fixed.

### 3. Errors: loud on stderr, non-zero exit, and the watch continues

`printTaskLogs` gains an `error` return. Its two callers are closures in the same function, so the
ripple is local.

Signatures change to thread an error writer, following the in-package precedent
`doAgentEnroll(ctx, args, cfg, out, errOut io.Writer)`:

- `LogsCommand` passes `os.Stdout, os.Stderr`.
- `doLogs(ctx, cfg, args, out, errOut io.Writer) error`
- `watchJobLogs(ctx, c, jobID string, out, errOut io.Writer) (status string, logFailures int, err error)`
- `printTaskLogs(ctx, c, taskID, taskName string, out io.Writer) error`

The six existing `doLogs`/`watchJobLogs` call sites in `internal/cli/logs_test.go` update
mechanically to pass a second `strings.Builder`; those updates are not behaviour changes and need no
test of their own.

Behaviour:

- **Print as you go.** Items are written to `out` as each page arrives. A failure on page 5 leaves
  pages 1-4 on stdout. A partial log with a stated boundary beats no log.
- **A log-fetch failure does not abort the watch.** Other tasks still stream and print. It is
  recorded and counted.
- **One diagnostic per failing task, on `errOut`, immediately**, naming the task, the task id and
  the last seq successfully printed. The seq is what makes the message actionable - it tells the
  operator where the output stops and what `since_seq` to resume from by hand.
- **Exit code.** `doLogs` returns a non-nil, non-silent error when `logFailures > 0`, so `Dispatch`
  prints `error: ...` and exits 1. This takes **precedence over** the existing `silentError{}` for a
  non-`done` job: both exit 1, and the descriptive one is strictly more informative. Silence is the
  thing being fixed, so where the two compete, silence loses.

Suggested strings, to be pinned by substring assertions rather than by exact match:

```
relay: logs for task frame-001 (7e660488) are incomplete - stopped after seq 4200: request failed (500)
relay: logs for task frame-001 (7e660488) truncated after 10000 pages (last seq 2000000) - the server never reported the log as drained
error: logs incomplete for 1 of 3 tasks
```

An empty log stays empty on stdout. No "(no output)" marker: that is a rendering decision, and the
acceptance criterion "a fetch failure is distinguishable from an empty log" is met by the failure
being loud, not by decorating success.

### 4. The fixture

All **four** fake servers in `internal/cli/logs_test.go` must serve the real envelope. Do not
hand-write four more literals - hand-written literals are what caused this bug. Add one behavioural
helper in the test file:

```
// writeTaskLogPage serves rows the way handleGetTaskLogs does: ?since_seq is
// EXCLUSIVE (WHERE id > since), ?limit defaults to 50 and caps at 200, and
// next_seq is the last returned row's seq, or 0 when the page is short.
func writeTaskLogPage(w http.ResponseWriter, r *http.Request, rows []taskLogEntry)
```

Every fake server routes its logs case through it. All four existing servers pass the same one-row
slice they hand-write today, so their tests keep asserting `frame rendered`; the new multi-page
tests pass a generated slice. When the handler's contract next moves, one helper is wrong instead of
four literals.

This is a **point fix**, not the general one. It removes four hand-written literals and replaces
them with one hand-written simulator, which is a strictly better single point of failure but still a
simulator. The general fix is `idea-2026-08-23-cli-tests-never-hit-real-server` (already `medium`,
already citing this bug). Do not build that harness here. Do record in the retro that this slice is
the second confirmed instance and that the helper is the seam a real-server lane would replace.

### Rendering: out of scope, and why

The item asks this spec to decide what the `[<task> <stream>] <content>` line does with an interior
carriage return. **Decision: out of scope, and the reason is that the question as posed is too
small.**

- CRLF from new output is already normalised to LF at the agent, shipped 2026-08-25. The CLI-local
  exposure the item described is largely closed upstream.
- The interior CR that remains is legitimate progress-bar output. `relay logs` writes to a terminal
  that renders it correctly, and the closed CRLF item's "Where normalisation belongs" is explicit
  that the interior-CR collapse and the ANSI strip are web-only rendering decisions and that the CLI
  wants the opposite. So the CLI must **not** collapse it. The only genuine complaint is that the
  cursor returns over the CLI's own prefix.
- And the prefix is wrong for a larger reason than CR. A row is a chunk, not a line. The prefix
  already lands on only the first line of a multi-line chunk, and the appended newline already
  splits any line that straddles a chunk boundary. Any interior-CR fix that leaves that in place is
  a cosmetic patch on a renderer that is structurally wrong.
- A bug fix that also changes rendering is harder to review, and this fix's acceptance test ("the
  CLI can talk to the real server") is independent of output format.

So: this slice changes **no** stdout formatting. The rendering work is drafted as a follow-up item
below and should be specified on its own, where chunk reassembly, the prefix, and the interior CR
can be decided together.

### Prose corrections in scope

Wrong prose is this project's dominant defect class, and both of these are about the command being
changed:

- README `#### relay logs`: replace "Stream task logs for a running or completed job via
  Server-Sent Events" with an accurate description - the CLI watches the job's status events and
  prints each task's log once that task reaches a terminal state, so output arrives per finished
  task rather than live. Document the new stderr diagnostic and that an incomplete log exits 1.
- `LogsCommand`'s usage string: "tail logs until job completes" claims live tailing. Correct it.

## Load, failure modes, threat model

**Load.** One request per 200 rows. The measured production task was 264 rows: 2 requests where
today's broken call makes 1. A 100,000-line log is 500 requests. Memory is O(one page) because the
loop streams to the writer.

**The one real regression risk, stated rather than discovered later.** `printTaskLogs` is called
from inside the SSE read loop. Paging now blocks that reader for the duration of the fetch instead
of one round trip. The server drops a subscriber that falls behind its 64-slot buffer and sends a
final `event: dropped`. The CLI's subscription is `job_id`-only, so the buffer carries status frames
only - roughly one per task or job transition - and overflow needs about 64 transitions to land
while one task's log is being paged. Reachable on a large job under load, not on ordinary ones. The
failure mode if it happens is **loud and already implemented**: the stream ends, `finalStatus` stays
empty, and `watchJobLogs` returns `connection lost - job <id> may still be running` with exit 1. The
CLI ignores the `dropped` frame rather than naming the reason, which is a diagnostic gap, not a
correctness one. Accepted for this slice; drafted as a follow-up item.

**Failure modes.**

| Condition | Behaviour |
|---|---|
| Non-2xx or decode error on page 1 | Nothing printed for that task, one stderr line, exit 1 |
| Failure on page N > 1 | Pages 1..N-1 printed, one stderr line naming the last seq, exit 1 |
| Cursor does not advance | Stops on the second request, stderr line, exit 1 |
| Cursor advances forever | Stops at `maxLogPages`, stderr line naming the cap, exit 1 |
| Task has no output | Nothing printed, no diagnostic, exit follows job status |
| Broker drops the subscription | Existing `connection lost` error, exit 1 |

**Threat model.** The paging cursor is server-supplied and drives a client loop, which is the
"a server-written value is not out of the caller's reach" shape: the value's provenance says nothing
about who controls its content or the timing of the writes. A hostile or broken server can attempt
an unbounded loop or unbounded memory growth. The two bounds close the first; streaming rather than
accumulating closes the second. No credential, no write path, and no privilege boundary is touched -
this is a read-only client change against an endpoint the CLI already calls with the same token.

**Invariants.** Checked against CLAUDE.md and none apply as a constraint, which is itself worth
stating so a reviewer does not have to re-derive it. No write to `tasks.status` or `task_logs`, so
no epoch fence. No job-spec ingestion. No gRPC stream and no sender. No connection teardown and no
registry identity. No shared registry and no lock. The single-JSON-entry-point invariant governs
`readJSON` for **server** request bodies; this is a client decoding a response, and `Client.Do` is
already that single entry point for the CLI.

## Test plan (RED first, each with its failing assertion named)

The engineer must record the RED output for each before writing the fix. New multi-page fixtures use
content `line <seq>` for the row whose `seq` is `<seq>`, so an assertion naming a line names a
specific row; the four existing servers keep their single `frame rendered` row.

**T1 - the fixture flip is the RED.** Convert all four fake servers to `writeTaskLogPage`. At HEAD,
four existing assertions go red because stdout is empty:

- `TestWatchJobLogs_TerminalBeforeSubscribe_DoesNotHang`: `require.Contains(out.String(), "[frame-001 stdout] frame rendered")`
- `TestWatchJobLogs_TaskInSnapshotAndStream_PrintedOnce`: `require.Equal(1, strings.Count(...))` observes 0
- `TestWatchJobLogs_DoneExits0`: same `Contains`
- `TestWatchJobLogs_AlreadyDone_PrintsLogsAndExits`: same `Contains`

This single measurement is also the backlog item's "reverting the production decode while keeping
the new fixture turns the test RED" criterion. Record it once, in the commit message.

**T2 - multi-page.** `TestWatchJobLogs_PagesUntilDrained`, 450 rows, seq 1..450.
Failing assertion at decode-fixed-but-not-paging: `require.Contains(out.String(), "line 450")`.
Also assert: 450 lines printed in ascending order; exactly 3 requests to the logs route; the first
request carries `limit=200` and no `since_seq` (or `since_seq=0`); the second carries the first
page's `next_seq`.

**T3 - exact multiple, and the exclusivity of `since_seq`.** `TestWatchJobLogs_ExactPageMultiple_NoDropNoDuplicate`,
400 rows with **contiguous** seq ids 1..400. Page 1 returns seq 1..200 and `next_seq = 200`.
Failing assertions:

- `require.Contains(out.String(), "line 201")` reddens an implementation that pages with
  `since = lastSeq + 1`: `id > 201` skips row 201 entirely.
- `require.Equal(1, strings.Count(out.String(), "line 200"))` reddens one that pages with
  `since = lastSeq - 1`: `id > 199` re-returns row 200.
- `require.Equal(400, lineCount)` reddens a loop that stops on the second full page rather than
  issuing the third, empty request.

**The contiguity is load-bearing and must be commented in the test.** `task_logs.id` is a global
`BIGSERIAL`, so per-task seqs are usually gapped, and a gapped fixture makes the `+1` bug invisible:
`id > lastSeq+1` and `id > lastSeq` return the same rows when no id equals `lastSeq+1`. Contiguous
ids are a legitimate production state (one task logging alone) and are the discriminating input.

**T4 - the cap stops a never-draining server.** `TestWatchJobLogs_ServerNeverDrains_StopsAtCap`.
Set the `maxLogPages` package var to 3 for the test and restore it. Server always returns a full
page with a strictly increasing `next_seq`.
Failing behaviour without the cap: the test times out (it must run under a `context.WithTimeout`, so
the RED is a timeout, and the engineer must report that plainly rather than calling it a pass).
With the cap: exactly 3 requests, `errOut` contains the task name and "truncated", `doLogs` returns
a non-nil non-silent error.
Assert the request count, never the constant. Asserting `maxLogPages == 10000` proves nothing about
the code that consumes it.

**T5 - the non-advancing cursor stops before the cap.** `TestWatchJobLogs_CursorDoesNotAdvance_StopsImmediately`.
Server returns a full page with a **constant** `next_seq`, with `maxLogPages` left at its default.
Failing assertion against an implementation carrying only the page cap: `require.Equal(2, requests)`
observes 10000 (and takes minutes). This is the test that distinguishes the two bounds; without it,
one guard can be deleted and everything stays green.

**T6 - a fetch failure is loud.** `TestWatchJobLogs_LogsFetchFails_ReportsOnStderr`. Logs route
returns 500; job goes `done`.
Failing assertions at HEAD: `require.Contains(errOut.String(), "frame-001")` observes an empty
buffer, and `require.Error(t, err)` observes nil - the job is `done`, so today's `doLogs` returns
nil and the shell sees exit 0 with no output, which is the exact production symptom.
Also assert the error is **not** a `silentError`, so `Dispatch` prints it.

**T7 - a mid-log failure prints the prefix.** `TestWatchJobLogs_FailsOnSecondPage_PrintsFirstPage`.
Page 1 succeeds, page 2 returns 500.
Failing assertions against a fetch-then-print implementation: `require.Contains(out.String(), "line 1")`
and `require.Contains(errOut.String(), "seq")`. This is the test that pins incremental printing;
T6 alone passes on an implementation that buffers the whole log and discards it on failure.

**Guard the fixture itself.** T2 through T7 all route through `writeTaskLogPage`, so the simulator's
own correctness is load-bearing. Its `since_seq`-exclusive behaviour is asserted by T3 and its
`next_seq`-when-short behaviour by T2; state that in the helper's doc comment so a future edit knows
what it will break.

**Lanes.** `make test` covers all of the above (`internal/cli` has no integration-tagged tests -
that gap is the general item, not this slice). CI's `-race` gate applies; the Linux container route
in CLAUDE.md is the reliable local one on this machine. No integration or e2e lane is required, and
no behaviour is being **removed**, so the removal-scaffolding hazard does not apply here.

## Acceptance / Done When

Mapping 1:1 onto the backlog item's own criteria, plus what this spec adds.

From the item:

1. `relay logs <job-id>` prints the task's log lines for a job with output, on both the
   terminal-job path (`onSubscribed`) and the SSE-follow path (the `task` event handler). Covered by
   T1's four reddened tests, which already span both paths.
2. A log longer than one page is printed in full, not truncated at the first page. T2, T3.
3. The CLI test fixture emits the envelope shape the handler actually writes, in **all four** fake
   servers. Reverting the production decode while keeping the new fixture turns the test RED, proven
   once and recorded. T1.
4. A fetch failure is distinguishable from an empty log at the terminal. T6, T7.

Added by this spec:

5. The paging loop terminates on a server that never drains (T4) and on one whose cursor does not
   advance (T5), and says so on stderr in both cases.
6. An incomplete or failed log exits non-zero with a printed message, taking precedence over the
   existing `silentError{}` for a non-`done` job.
7. Memory is O(one page): the loop writes each page to the output writer and does not accumulate the
   log.
8. No stdout formatting change. The exact byte format of a printed log line is identical to today's,
   which is what makes the diff reviewable as a bug fix.
9. README's `relay logs` section and the `LogsCommand` usage string describe what the command
   actually does, and document the new stderr diagnostic and exit code.
10. Phase 5 closes `docs/backlog/bug-2026-08-25-relay-logs-prints-nothing-envelope-drift.md` with
    `/backlog close`, which `git mv`s it to `docs/backlog/closed/`. Required scope, not cleanup.

## Explicitly out of scope

- **The Python SDK's version of this drift** (`bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys`).
  Same endpoint, different client, separate item.
- **The general "CLI tests never hit a real server" harness**
  (`idea-2026-08-23-cli-tests-never-hit-real-server`). Already `medium`; no re-prioritisation
  needed. This slice is a point fix and the retro should say so.
- **Agent-side CRLF normalisation.** Already shipped.
- **CLI log rendering** - chunk-to-line reassembly, the per-line prefix, and interior CR. Follow-up
  item drafted below.

## Proposed follow-up backlog items - NOT filed by this phase

Drafted for the human to accept or reject. Do not file them without that decision.

1. **`bug` - `relay logs` treats a log chunk as a line, so the prefix and newline are wrong on
   multi-line and straddled chunks.** A `task_logs` row is whatever `os/exec` handed the agent's
   `chunkWriter`, stored verbatim. `printTaskLogs` writes one prefix and one trailing newline per
   row, so a chunk containing three newlines gets one prefix and a chunk ending mid-line gets a
   spurious newline. The web reassembles (`logBuffer.ts`); the CLI does not. Subsumes the
   interior-CR question deferred from the envelope-drift item: the CLI must not collapse interior CR
   (a terminal renders progress bars correctly, and the closed CRLF item is explicit that the
   collapse is web-only), but an interior CR does return the cursor over the CLI's own prefix, which
   a prefix-per-line renderer fixes for free.
2. **`bug` - the MCP `relay_get_task_logs` tool advertises `since_seq` as inclusive.** Its
   jsonschema says "Return only log entries with seq >= this value"; `GetTaskLogsPage` is `id > $2`.
   A model following the description re-requests the row it already has, or believes it has a row it
   does not. One-line prose fix in `internal/mcp/task_logs.go`; check the README MCP tool table at
   the same time.
3. **`idea` - `relay logs` ignores the `dropped` SSE frame.** The server's final frame before
   closing a slow subscriber says `{"reason":"slow_consumer"}`. The CLI's handler switches on `task`
   and `job` only, so the drop surfaces as the generic `connection lost - job <id> may still be
   running`. Naming the reason costs one case arm. Low priority; the current message is loud and
   exits 1, so this is a diagnostic improvement, not a correctness fix.
