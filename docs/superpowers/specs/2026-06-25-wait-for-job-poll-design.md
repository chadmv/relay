# relay_wait_for_job poll interval - design

- Date: 2026-06-25
- Status: proposed
- Backlog: `docs/backlog/bug-2026-05-09-wait-for-job-poll-interval.md`
- Author: relay-tpm (autonomous spec)

## Problem

`relay_wait_for_job` (MCP tool) polls `GET /v1/jobs/{id}` on a flat 2 s cadence
(`defaultWaitPoll` in `internal/mcp/wait.go`). For a job that completes in under
2 s, the caller waits up to one full poll interval after the job is already done.
The dominant relay workload through MCP is short jobs (an agent submits work and
waits on it conversationally), so a 0-2 s tail latency on every fast job is the
common case, not the edge case. The backlog item's open question: does dropping
to 500 ms improve typical workloads without creating excess API load on
long-running jobs?

## Current behavior (verified against code)

- `internal/mcp/wait.go`: `defaultWaitPoll = 2 * time.Second`. The loop does
  `GET /v1/jobs/{id}`, checks `status` against `{done, failed, cancelled}`,
  sleeps `poll` (clamped to remaining time), repeats until terminal or deadline.
  `poll` comes from `s.waitPoll` (overridable package/struct field; 0 -> default).
- Latency floor for a sub-2 s job: 0 to 2 s of dead wait after completion,
  averaging ~1 s, on top of the GET round trip.
- API load for a long job: one GET every 2 s for up to `timeout` (default 60 s,
  max 300 s). At 2 s that is ~30 GETs/min/wait; at 300 s, ~150 GETs per wait.

## Evidence gathered (grounding, not pointers)

- **MCP server has no DB access.** `internal/mcp/server.go`: `Server` holds
  `client *relayclient.Client`, `mcp`, `waitPoll`, `isAdmin`, `reloadToken`. No
  `*pgxpool.Pool`, no `store.Queries`. The only pgxpool reference in
  `internal/mcp` is the integration-test harness, which boots a real server. The
  MCP server is a stdio process that talks to the REST API as a client.
- **LISTEN/NOTIFY is not reachable from MCP.** `NotifyTaskCompleted` in
  `internal/store/query/tasks.sql` is `SELECT pg_notify('relay_task_completed','')`
  - a server-side query used by the scheduler/dispatcher. Wiring the MCP wait
  loop to it would require giving the MCP process a Postgres connection (new
  config, new credentials, new failure mode), which contradicts its client-only
  design. Out of scope.
- **An SSE push channel already exists and is client-reachable.**
  - `internal/api/events.go`: `GET /v1/events` is auth'd and accepts a `job_id`
    query param, subscribing the caller to broker events scoped to that job.
  - `internal/events/broker.go`: `Event{Type, JobID, Data}`; `job_id` filter,
    64-slot per-subscriber buffer, slow subscribers are dropped (channel closed).
  - A `job` event with `{"id","status"}` is published on terminal transition to
    `done`/`failed` from both completion paths (`internal/worker/handler.go`,
    `internal/scheduler/dispatch.go`) and `{"status":"cancelled"}` from the cancel
    handler (`internal/api/jobs.go`).
  - `internal/relayclient/client.go`: `StreamEvents(ctx, path, onSubscribed, handler)`
    already implements SSE consumption with an `onSubscribed` hook that fires
    after the HTTP 200 (subscription established) and before the first event.

## The fork

- **Option A - lower / adapt the poll interval (client-side only).** No server
  change. Change the cadence in `wait.go`.
- **Option B - push-based wait.** Two sub-variants:
  - B1: LISTEN/NOTIFY from the MCP process. **Not viable** - the MCP server has
    no DB connection by design (evidence above). Would be a large, separately
    scoped change (DB wiring into a client process).
  - B2: SSE long-wait over the existing `GET /v1/events?job_id=<id>` using the
    existing `StreamEvents`. **Viable today, no server change**, but carries a
    correctness tax: a job that is already terminal before the subscription is
    established never re-fires its `job` event, and a dropped slow subscriber
    closes the channel mid-wait. So B2 still needs a check-after-subscribe and a
    poll fallback - it is "push with a poll safety net," not pure push.

## Decision: Option A (adaptive poll), with B2 explicitly deferred

Ship Option A now: an adaptive client-side poll schedule. Defer B2 (SSE) as a
documented future enhancement, and record B1 (LISTEN/NOTIFY) as out of scope for
the MCP client.

Rationale:

1. **B1 is impossible without re-architecting the MCP server** (no DB access).
   The backlog item floated LISTEN/NOTIFY before this was confirmed; it is now
   ruled out.
2. **B2 is feasible but not free.** It needs the same terminal-check-after-GET
   plus a poll fallback that Option A delivers, so it does not remove polling -
   it adds an SSE code path, an extra long-lived HTTP connection per wait, and
   reconnect/drop handling, on top of the fallback. The latency win of B2 over a
   500 ms first-poll is small (hundreds of ms) for the workload that matters
   (sub-2 s jobs), because Option A already returns within ~500 ms of completion.
   B2's real payoff is for long jobs (near-zero-latency completion notice with no
   periodic GETs), which is exactly where Option A's backoff already keeps API
   load low. The marginal benefit does not justify the added moving parts now,
   per CLAUDE.md "Simplicity First."
3. **Option A is the smallest change that satisfies the Done-When criteria** and
   keeps the MCP server a pure REST client.

## Design (Option A): adaptive poll schedule

Replace the single flat interval with a fixed adaptive schedule: poll fast while
fast completion is likely, then widen toward the current cadence so a long-running
wait does not hammer the API.

Schedule (sleep BEFORE each subsequent GET; the first GET is immediate, as today):

| Phase            | Interval | Applies to                                  |
| ---------------- | -------- | ------------------------------------------- |
| Fast             | 500 ms   | first 4 sleeps (covers ~the first 2 s)      |
| Steady (current) | 2 s      | every sleep thereafter, until deadline      |

Concretely, the inter-poll sleep sequence is:
`500ms, 500ms, 500ms, 500ms, 2s, 2s, 2s, ...`

- A job completing under ~2 s is observed within ~500 ms of completion (one fast
  interval), satisfying the primary Done-When.
- A 60 s wait does 4 fast polls + ~29 steady polls ~= 33 GETs (vs ~30 today) -
  a negligible load increase concentrated in the first 2 s, then identical to
  current steady-state. A 300 s wait does 4 + ~149 ~= 153 GETs (vs ~150). No
  excess load on long jobs.

Why adaptive, not flat 500 ms: a flat 500 ms quadruples GET volume for the entire
duration of every long wait (e.g. ~600 GETs over a 300 s wait vs ~150 today). The
adaptive schedule captures the full fast-job latency win while leaving long-job
load essentially unchanged. The added complexity is a handful of lines (a counter
and a threshold), well within the simplicity bar.

### Constants and config

- Add unexported constants in `wait.go`:
  - `fastWaitPoll = 500 * time.Millisecond`
  - `fastWaitCount = 4` (number of fast intervals before widening)
  - keep `defaultWaitPoll = 2 * time.Second` as the steady interval.
- **Not env-configurable.** This is a client-side latency/load tradeoff internal
  to one tool, not a fleet-deployment safety bound. The maintainer's preference
  for env knobs applies to operational/safety parameters (grace windows, eviction
  timeouts) that operators must tune per deployment; a poll cadence does not rise
  to that bar, and exposing it invites bikeshedding and a config surface with no
  real operator need. State this explicitly so it is a decision, not an omission.
  - Preserve the existing `s.waitPoll` override field, but redefine its meaning:
    when `s.waitPoll != 0` (test override), use it as a **flat** interval and skip
    the adaptive schedule. This keeps existing tests' determinism contract intact
    and gives tests a single dial for the flat case. The adaptive schedule is
    exercised by tests that leave `waitPoll == 0` and instead override the fast
    constants (see Test strategy) OR by introducing a small injectable schedule.
    See Test strategy for the chosen mechanism.

### Behavior preserved

- Terminal status set unchanged (`done`, `failed`, `cancelled`).
- Timeout handling unchanged: deadline clamp, `timed_out` + `last_state` return.
- `ctx.Done()` cancellation unchanged.
- The first GET remains immediate (no initial sleep), so an already-terminal job
  returns on the first iteration with zero added latency.

## Acceptance criteria

1. A job that reaches a terminal state within ~2 s is returned by
   `relay_wait_for_job` within ~500 ms of that transition (one fast interval),
   measured deterministically in tests (no real sleeps).
2. For a long-running wait, the GET cadence after the fast phase is unchanged
   from today (2 s steady), so total API load over a 60 s / 300 s wait is within
   ~10% of the current behavior.
3. Timeout, cancellation, and already-terminal-on-first-GET behavior are
   unchanged.
4. The `s.waitPoll` test override continues to force a flat interval (existing
   tests keep passing without modification).

## Test strategy (deterministic, no real sleeps)

The loop currently sleeps via `time.After(waitFor)`. To test the adaptive
schedule deterministically:

- Make the sleep function injectable as a package var, e.g.
  `var waitSleep = func(ctx context.Context, d time.Duration) error { ... }`
  wrapping the current `select { <-ctx.Done(); <-time.After(d) }`. Tests swap it
  for a recorder that captures each requested `d` and returns immediately. This
  is the same injectable-package-var pattern the codebase already uses
  (`saveConfigFn`, `readPasswordFn`, the existing `waitPoll` field).
- Tests assert on the **recorded sleep sequence**, not wall-clock timing:
  - Adaptive progression: with a stubbed HTTP client that returns `running` for
    N polls then `done`, assert the captured sleeps are
    `[500ms x min(N,4)] + [2s x ...]` in order.
  - Fast-job latency: client returns `done` on the 2nd GET; assert exactly one
    recorded sleep of 500 ms preceded it.
  - Already terminal: client returns `done` on the 1st GET; assert zero recorded
    sleeps.
  - Flat override: set `s.waitPoll = 250ms`; assert every recorded sleep is
    250 ms (adaptive schedule bypassed).
  - Timeout: short deadline, client always `running`; assert the final return is
    `{timed_out: true, last_state: ...}` and the last sleep is clamped to the
    remaining time.
- Existing `wait` tests that set `waitPoll` keep working under the redefined
  "non-zero = flat" semantics. Confirm none of them assert on the 2 s default in
  a way the fast phase would break (they inject `waitPoll`, so they are flat).

## Invariants

Client-side only. No server change, no DB write, no gRPC stream, no shared
registry. None of the six Invariants are touched:

- Epoch fence, single job-spec pipeline, one bounded sender per stream,
  identity-checked teardown, no interior pointers across locks, single JSON entry
  point: all n/a - this changes only the MCP client's poll cadence against an
  existing read-only REST endpoint.

Confirmed: Option A requires no server change.

## Deferred / future work (do not implement now)

- **B2 (SSE long-wait).** Reuse `GET /v1/events?job_id=<id>` via
  `relayclient.StreamEvents` to return near-instantly on the terminal `job`
  event, with the adaptive poll retained as the fallback for the
  already-terminal-before-subscribe race and slow-subscriber-drop cases. Worth
  revisiting if profiling shows long-job GET load or fast-job tail latency is a
  real pain after Option A ships. File as a backlog item, not in this spec's
  scope.
- **B1 (LISTEN/NOTIFY from MCP).** Out of scope - the MCP server is a REST client
  with no DB access; wiring Postgres into it is a separate architectural change
  with its own threat model (DB credentials in the client process).
