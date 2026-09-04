# A server-side cost bound on `?q=` text search

Date: 2026-09-04
Item: `docs/backlog/feature-2026-09-03-server-side-bound-for-text-search.md`
Status: design, not yet planned

## 1. What is actually unbounded

`?q=` on `GET /v1/jobs` and `GET /v1/scheduled-jobs` runs `strpos` over every candidate row. No
index can serve it (`internal/store/query/jobs.sql`'s comment on `ListJobsWithEmailPage` says so
and explains why `strpos` was chosen over `ILIKE`: `%` and `_` must stay literal). The security
lens that filed the item measured, on a 200k-row jobs table, about 283 ms of database CPU for a
no-match needle against 10.8 ms for the unfiltered list.

**Those two numbers are inherited from the item and have not been reproduced here.** The item does
not record the needle, the sort arm, the page limit, or whether the 283 ms was the count statement,
the list statement or the whole request. Section 9 makes closing that gap a requirement rather than
an aspiration.

The failure mode is not "unbounded CPU" in the abstract. `RELAY_DB_MAX_CONNS` (default 25, set on
`cfg.MaxConns` in `cmd/relay-server/main.go`) already caps how many statements run at once. The
failure mode is **pool occupancy**: a caller who can issue text searches faster than they complete
occupies pool connections that the dispatcher, the agent log-ingest path and the stale-task watchdog
also need. Every one of those runs on the same `*pgxpool.Pool`. A search that is 26x the cost of an
unfiltered list, issued in a loop by an authenticated caller, is a queue-starvation primitive, not
a CPU-burn primitive.

That reframing decides the priority of the two controls, and it is the opposite of the order the
item lists them in. See section 3.

README states the cost honestly today, under **`?q=` cost** in the "Filtering the jobs list"
section: *"The server applies no rate limit and no statement timeout to this today, so the cost is
bounded only by the table size and by how often clients ask."* `JobsPage`'s own comment says the
same: *"THE DEBOUNCE IS NOT A BOUND."* Both sentences become false when this slice lands and both
are in required scope.

## 2. What is already settled, and what this spec adds

A sibling lane shipped the write-side limiter (`RELAY_JOB_SUBMIT_RATE_LIMIT`, default `120:10s`,
one shared bucket over `POST /v1/jobs`, `POST /v1/jobs/{id}/retry` and
`POST /v1/scheduled-jobs/{id}/run-now`). Its keying decision is settled and this spec builds on it
rather than re-opening it:

- The key is the authenticated user id rendered through `uuidStr`, not `RemoteAddr`. On an
  authenticated route there is a principal and the cost belongs to it; the proxy-collapse reasoning
  that makes `clientIP` correct for `handleLogin` does not transfer.
- A burst ceiling, not a budget over time.
- The mechanism is a `rateLimiter` instance plus a `userRateLimitKey` helper returning
  `(string, bool)` that fails closed, so an unidentified caller creates no map key at all.

This slice is the second consumer of that mechanism. Two things follow, and both were already ruled
on by the sibling spec; this spec confirms them against the tree.

**The item's proposal cannot be implemented as written.** It says *"The existing per-IP RateLimit
applied to authenticated reads carrying q, with its own bucket."* `RateLimit` in
`internal/api/ratelimit.go` keys on `clientIP(r)`, which reads `r.RemoteAddr` and explicitly does
not trust `X-Forwarded-For`. Mounting it on an authenticated read collapses every user behind one
proxy into one bucket. What the item wants is a second **instance** of the user-keyed mechanism,
not a second mounting of `RateLimit`.

**The middleware form must not be copied either**, and here the tree gives a sharper reason than
the sibling spec had. `parseFilterQ` in `internal/api/list_filters.go` carries this in its own
doc comment:

> qs is the query string parsePage already parsed: `r.URL.Query()` discards percent-decoding
> errors, so a second parse can disagree with the one that was validated.

Confirmed verbatim. A middleware predicate deciding "does this request carry a needle" would be a
second implementation of that decision, reading `r.URL.Query()` again, and could disagree with the
one that was validated. It would also disagree on cases `parseFilterQ` already normalizes: `?q=`
and `?q=%20%20` are both **absent** after the trim, and a middleware that tests `Get("q") != ""`
counts them. The bucket is therefore checked **inside the handler**, at the point the needle is
known to be non-nil, so the counted set and the expensive set are the same set.

The codebase already branches exactly there. `countJobs`, `countJobsByStatus` and
`countJobsByScheduledJob` in `internal/api/jobs.go` each open with `if filters.Q != nil` and route
to a `...WithText` statement. That `filters.Q != nil` test is the established in-handler expression
of "this request is the expensive kind", and the bucket check sits beside it.

## 3. Decision 1: the statement timeout

### 3.1 Confirmed against the tree

- **There is no `statement_timeout` anywhere in the tree.** A search across the whole worktree
  returns only prose: a comment in `internal/worker/ingest_log_limiter.go` naming it as a
  hypothetical contention failure, a comment in `handleGetJob` naming it as a transient failure the
  code must not silently swallow, and mentions in closed backlog items, plans and specs. No
  connection string, no SQL file and no Go statement sets it. Confirmed.
- **The pool is built in `cmd/relay-server/main.go` via `pgxpool.ParseConfig(dsn)` with
  `cfg.MaxConns` set from `RELAY_DB_MAX_CONNS`, defaulting to 25.** Confirmed, and the default is
  documented in README's environment table.

### 3.2 What the choice actually is

The item offers "a pool-wide `statement_timeout` on the pool (or a per-statement timeout for the
text-count and text-list statements)". The second half of that sentence does not describe anything
that exists.

**There is no separable set of "text statements" on the list side.** A count of top-level
`Q *string` struct fields across `internal/store/*.sql.go` returns 33: 15 in
`internal/store/jobs.sql.go` and 18 in `internal/store/scheduled_jobs.sql.go`. Only the **count**
arms are split into text and non-text statements (`CountJobs` versus `CountJobsWithText`, and the
same pair for the by-status and by-scheduled-job variants, which is the split the 2026-09-03 retro
describes as "three text-count statements so the unfiltered count does not pay for a
`JOIN users`"). Every **list** arm is a single statement serving both cases:
`ListJobsWithEmailPage` and its sort siblings take `Q` and are called whether or not a needle is
present, with `sqlc.narg(q)::text IS NULL` as the first arm of the predicate. So a timeout attached
to "the text-list statements" would either have to be attached to statements that also serve
unfiltered polling, which requirement 4 forbids, or be branched on `Q` at every one of those call
sites.

What *is* expressible narrowly is a **per-request** deadline: wrap `ctx` in a
`context.WithTimeout` inside the two handlers, gated on the same `filters.Q != nil` the count
helpers already use. Two sites, not 33. So the real comparison is:

- **(A) pool-wide `statement_timeout`**, set once on the parsed config.
- **(B) per-request `context.WithTimeout`** in `handleListJobs` and `handleListScheduledJobs`,
  gated on a non-nil needle.

### 3.3 The decision: (A), pool-wide

**Argument.**

1. **(B) is enforced by the client; (A) is enforced by the server.** A context deadline makes pgx
   send a cancel request and stop waiting. The backend stops because pgx asked it to. If that ask is
   slow, lost, or the client process is itself wedged, the backend keeps scanning and keeps holding
   the connection. `statement_timeout` is the backend cancelling itself and needs no cooperation
   from anything. The resource the item wants bounded ("so one needle cannot hold a connection for
   seconds") is a server-side resource, and only the server can reliably release it.
2. **(B) covers the paths someone remembered to wrap.**
   `docs/backlog/idea-2026-09-03-list-filters-remainder-status-labels-users-q.md` already proposes
   `?q=` on `GET /v1/users` through the same `parseFilterQ`. Under (B) that endpoint inherits
   nothing and the omission is silent. Under (A) it inherits the bound by existing.
3. **(A) is one line at the place an operator would look for it**, beside `cfg.MaxConns`, which is
   the other pool-wide bound and is already documented in the same README table.
4. **(B) changes the meaning of the bound** from "per statement" to "per request": the count's spend
   would eat the list's budget, because both run under the handler's one `ctx`. That may even be
   preferable, but it is not what the item asked for and it should not be introduced silently.

**Mechanism, confirmed to exist.** `pgconn.Config` (reached as `cfg.ConnConfig.Config` after
`pgxpool.ParseConfig`) has a `RuntimeParams map[string]string` field documented as *"Run-time
parameters to set on connection as session default values"*. pgx sends these in the startup packet,
so every pooled connection carries the setting from the moment it is established, with no extra
round trip. `pgxpool.Config.AfterConnect func(context.Context, *pgx.Conn) error` also exists and
would work, at the cost of a `SET` round trip per new connection and of a code path where a failed
`SET` has to be handled. **Prefer `RuntimeParams`.** Both were confirmed against the pgx v5.9.1
documentation; the repo pins `github.com/jackc/pgx/v5 v5.9.1` in `go.mod`.

**Precedence over the DSN.** Setting the key after `pgxpool.ParseConfig` overwrites whatever the DSN
supplied. That is intended and must be documented: relay's setting wins. The escape for a deployment
that manages the timeout at the DSN or role level is the `0` value in section 3.5, which makes relay
not set the key at all.

### 3.4 What a pool-wide setting touches, enumerated

The obligation this decision carries is to name every statement that could legitimately exceed the
value and say what happens to it. Enumerated against the tree:

- **Migrations: out of reach, and this is not incidental.** `store.Migrate` in
  `internal/store/migrate.go` uses `migrate.NewWithSourceInstance("iofs", src, dsn)`; golang-migrate
  opens its own connection from `migrateDSN`, and `main` calls it **before**
  `pgxpool.ParseConfig(dsn)` is ever reached. A setting applied to the parsed pool config cannot
  touch it. This matters concretely for
  `docs/backlog/idea-2026-09-03-pg-trgm-index-for-text-search.md`, whose whole proposal is a
  `CREATE INDEX` migration on `jobs.name` and `users.email` that can run for minutes on a large
  table. It stays safe. **This is also the reason the setting must be applied in Go and never
  documented as "put `statement_timeout` in your DSN": `migrateDSN` is derived from `dsn` by prefix
  rewriting, so a DSN-carried timeout WOULD reach migrations.**
- **The LISTEN/NOTIFY listener: believed safe, and must be pinned rather than asserted.**
  `NotifyListener.session` in `internal/scheduler/notify.go` acquires a pool connection, runs two
  `LISTEN` statements, and then blocks in `raw.WaitForNotification(ctx)` indefinitely.
  `statement_timeout` bounds statement execution; the two `LISTEN`s complete immediately and the
  wait is idle time with no statement running. If that reasoning is wrong, the process silently
  loses cross-process dispatch wakeups, which is a failure nothing else in the system would report.
  **Required: an integration test that sets a short timeout, holds a listener open for longer than
  it, issues a NOTIFY, and asserts the trigger fires.** This is not optional colour; it is the one
  claim in this section whose failure mode is silent.
- **Job creation: safe, because the timeout is per statement and not per transaction.**
  `jobcreate.CreateJobFromSpec` runs one `CreateTask` or `CreateTaskWithSource` per task inside the
  caller's transaction, plus one `CreateTaskDependency` per edge. `maxTasksPerJob` in
  `internal/jobspec/jobspec.go` is 5000, so the largest legal job is thousands of individually
  trivial statements in one long transaction. `statement_timeout` does not bound a transaction.
- **The recursive and multi-row writes** (`FailDependentTasks`, `RetryJobTasks`,
  `RequeueWorkerTasksIfEpoch`) are bounded by tasks per job, so by `maxTasksPerJob`.
- **The unfiltered count is the one statement that grows without a `LIMIT`.** `CountJobs` has no
  page limit and scans the whole table on every list request, needle or not. This is why the item's
  own 10.8 ms unfiltered baseline is not near zero. It grows with the table and it is the statement
  most likely to be the first to trip a tight timeout. That is an argument about the default value,
  not about the mechanism; see 3.5. **Bounding it is not in this slice** (section 13).
- **SSE**: `handleEvents` holds an HTTP connection, not a statement.

### 3.5 The value

`RELAY_DB_STATEMENT_TIMEOUT`, a Go duration string parsed with `time.ParseDuration`, matching the
convention of `RELAY_WORKER_GRACE_WINDOW` and `RELAY_TELEMETRY_WINDOW`. **Default `30s`.**

**Be honest about what 30s does and does not do.** At the item's measured 283 ms it will never fire.
It is not the control that bounds the amplifier at today's table size, and nobody should read it as
one. Its job is to convert "a statement can hold a pool connection indefinitely if the table grows,
the plan flips, or the box is contended" into a bounded hold, for every statement in the system
rather than for two handlers. A reviewer who wants the timeout to be the binding control should
argue about the number, not the mechanism.

**Two failure modes the parser must close.**

1. **`statement_timeout = 0` means DISABLED in Postgres.** A duration that is non-zero but rounds to
   zero milliseconds (`100us`) would silently render as `0` and turn the control off. That must be a
   startup refusal, not a silent disable.
2. **The value is not validated until a connection is opened.** `pgxpool.NewWithConfig` does not
   necessarily establish a connection eagerly, so a malformed runtime parameter surfaces as a
   connection error at first query rather than at boot. Validate in Go and `log.Fatalf` on a bad
   value, as `parsePublicURL`, `ParseRateLimit` and `RELAY_ALLOW_AUTO_ENROLL` already do.

**An explicit `0` means relay does not set the parameter at all**, leaving whatever the DSN, the
role or the server default provides. It does not send `statement_timeout=0`. This is an escape for a
deployment that manages the setting elsewhere, and it must be logged at startup as a line naming the
unarmed control. It is **not** a tuning knob and must never appear in a remedy list beside "raise
the value": an option that disables a control does not belong in the same ladder as options that
adjust it.

**Error surface.** A tripped timeout returns SQLSTATE 57014 to pgx, which the list handlers will
turn into their existing `500 list jobs failed` / `500 list scheduled jobs failed`. This slice keeps
that. Mapping 57014 to a distinguishable response is out of scope (section 13); the plan must log
the underlying error once at each list site so a timeout is diagnosable from the server log, and
README must say plainly that a search exceeding the timeout answers 500.

## 4. Decision 2: the read bucket

**`RELAY_JOB_SEARCH_RATE_LIMIT`, default `120:10s`**, parsed by the existing `ParseRateLimit`
(`N:duration`, both parts strictly positive), with `log.Fatalf` on a parse failure, matching
`RELAY_LOGIN_RATE_LIMIT` and `RELAY_REGISTER_RATE_LIMIT` in `cmd/relay-server/main.go`.

### 4.1 Where the number comes from

The binding constraint is not the adversary, it is the first-party SPA, and the SPA is far noisier
than the item's "client-side debounce" framing suggests. These are **configured cadences read from
source, not counts observed against a running server**:

| Surface | Source | q-carrying requests |
|---|---|---|
| Jobs, table view | `useJobs` `intervalMs = 3000` | 1 per 3 s = 20/min |
| Jobs, lanes view | `useJobLanes` `intervalMs = 3000` over `LANE_ORDER` (5 statuses) | 5 per 3 s = 100/min |
| Jobs, timeline view | `useJobTimeline` `refetchInterval: ANCHOR_STEP_MS` (15 s), `walkJobWindow` up to `TIMELINE_MAX_PAGES` (3) sequential pages | up to 3 per 15 s = 12/min |
| Schedules | `useSchedules` `intervalMs = 10000` | 1 per 10 s = 6/min |

`JobsPage` enables exactly one of the three views at a time, so the worst case is the **lanes view
with a needle in the box: about 17 q-carrying requests per 10 s window, from one tab, forever**, on
`GET /v1/jobs?status=...&q=...`. Add the debounce landing, where the old key's five queries can
still be in flight when the new key's five start (`JobsPage`'s `debounceMs` default is 300), and add
`retry: 1` from `web/src/lib/queryClient.ts`, which turns every refused request into two.

Worst realistic legitimate 10 s window for one tab: roughly 25 requests. `120:10s` is about 5x that,
which survives four tabs. It matches the write side's number, which is a weak reason on its own but
a real one for two knobs a reader will compare.

**Say what this default does not buy.** 120 per 10 s is 12 searches per second. At the item's
measured 283 ms each, that is more database time per second than the box has, so **this is not a CPU
budget and must not be described as one**. It is a fairness bound: it stops one principal from
monopolizing the 25-connection pool, and leaves the pool itself as the concurrency ceiling it has
always been. The honest claim is "one authenticated principal can no longer issue text searches
faster than 12 per second", not "text search is now cheap".

**And the bound degrades linearly in accounts.** The key is the user id, so extra tokens for one
user share one bucket, but extra accounts do not. Account creation is invite-gated unless
`RELAY_ALLOW_SELF_REGISTER` is on, in which case `RELAY_REGISTER_RATE_LIMIT` (5:1m, per IP) is the
only brake. State this in the threat model rather than leaving it implied.

**These cadences are configuration, not observation.** TanStack does not start an interval-triggered
refetch while one is already in flight for the same key, which `useJobTimeline`'s own comment
records, so the real counts are at or below the table. **The plan must count actual requests over
one minute in each of the three views against a running server, and record the number with the view
it was taken in.**

### 4.2 The absence of an off value

`ParseRateLimit` rejects a zero count and `main` calls `log.Fatalf`, so `RELAY_LOGIN_RATE_LIMIT=0`
is a refusal to start, not a disable. The only disabled state is Go-reachable: `Handler()` mounts
`RateLimit` only when `s.RegisterLimitN > 0 && s.RegisterLimitWin > 0`.

**Keep that for the read bucket.** Reasons:

- A control an operator can turn off from the environment is a control an operator will turn off the
  first time they see 429s in a log. The remedy that presents itself is the one that removes the
  bound.
- The escape that is actually needed is a **large number**, not an off switch: `100000:1s` gives an
  operator all the headroom they can want while leaving the control visible as a number in README
  and in the environment. Document that as the escape and do not document a disable.
- The Go-reachable disabled state is retained for tests and embedders, through the zero-value fields
  in section 10. That is also what keeps every existing construction of `api.Server` unchanged: a
  search for the text `api.New(` returns 181 matching lines across the worktree, most of them Go
  call sites in `internal/api`'s own test files and the rest in `docs/superpowers/plans` and
  `docs/retros`.

## 5. Decision 3: one bucket over both routes

**`parseFilterQ` has exactly two production callers.** Searching the worktree for the symbol returns
**25 matching lines**, which break down as:

- **2 production call sites**: `parseJobFilters` in `internal/api/job_filters.go`, reached from
  `handleListJobs`; and `parseScheduleFilters` in `internal/api/scheduled_jobs.go`, reached from
  `handleListScheduledJobs`.
- **2 lines for the definition and its doc comment**, in `internal/api/list_filters.go`.
- **2 comments in the two callers** pointing at that doc comment for why `qs` is passed rather than
  re-read.
- **1 comment in `internal/api/reservations.go`**, which points at the same doc comment for the same
  reason. Reservations takes `?worker_id=`, not `?q=`. **It is not a caller.**
- **1 test**, `internal/api/list_filter_q_parity_integration_test.go`.
- **17 lines in docs**: 13 in `docs/superpowers/plans/2026-09-02-web2-sb.md`, 2 in
  `docs/backlog/idea-2026-09-03-list-filters-remainder-status-labels-users-q.md`, 1 in
  `docs/retros/2026-09-03-web-batch-two.md`, 1 in
  `docs/superpowers/specs/2026-09-02-web2-sf-design.md`.

So: two routes, and `internal/api/reservations.go` is not one of them.

**One shared bucket over both routes.** Argument:

1. **The quantity bounded is scan work, and it does not care which route bought it.** Two buckets
   hand an adversary who alternates routes exactly twice the ceiling, which is the shape where
   per-axis bounds reduce nothing.
2. **First-party interference is negligible.** The SPA's jobs page and schedules page are separate
   routes and a user is on one at a time; even both at once (two tabs) peaks around 18 per 10 s
   against a ceiling of 120.
3. **The write side chose one shared bucket on the same reasoning** and a second precedent in the
   same file is cheaper to hold than two different rules.

**Recorded counter-argument, accepted deliberately.** A schedules table is typically tens of rows
where a jobs table is hundreds of thousands, so a schedules search is charged at the jobs rate for a
fraction of the cost. That is a conservative error, and the schedules predicate is not free either:
it matches three axes (`sj.name`, `u.email`, `sj.cron_expr`) and joins `users`.

**Separate from the write bucket.** Different quantity, different first-party cadence (a polling
read at 20 to 100 per minute versus an interactive submit), and sharing them would let a search
burst refuse a job submission, which is the worse of the two outcomes to trade away.

## 6. Decision 4: placement, and how "unfiltered polling is unaffected" is proven

### 6.1 Placement

In both handlers, immediately after the filters parse returns `ok`, gated on a non-nil needle:

- `handleListJobs`: after `parseJobFilters(w, pp.Query, u)` returns, gated on `filters.Q != nil`.
- `handleListScheduledJobs`: after `parseScheduleFilters(w, pp.Query)` returns, gated on
  `filters.Q != nil`.

**Two ordering constraints, both hard.**

1. **429 must not outrank any existing 400 or 401 on these routes.** README documents a precedence
   rule explicitly: *"On `GET /v1/jobs` the one exception runs earlier still: the sort-versus-filter
   400 below outranks this endpoint's own arity check."* That precedence has a test. Placing the
   bucket after the filters parse satisfies this for free, because `parsePage`, the
   sort-versus-filter guard, `rejectRepeatedParams` and every 400 inside `parseJobFilters`
   (including the `q` too long, `q` not valid UTF-8 and `mine`/`since`/`until` messages) have
   already run.
2. **A malformed needle must not consume budget.** An over-long or non-UTF-8 `q` is a 400 from
   `parseFilterQ` and never reaches a statement, so it must not decrement the bucket. Same
   placement, same consequence.

**One behavioural change to declare.** `handleListJobs` currently reads the identity with
`u, _ := UserFromCtx(ctx)`, discarding the `ok`. `handleListScheduledJobs` checks it and answers
401. Because the key must fail closed on a missing principal, a q-carrying request with no resolved
identity will now answer 401 on the jobs list. That is unreachable through the mux, since the route
is mounted as `auth(http.HandlerFunc(s.handleListJobs))`, but it is reachable by a test that calls
the handler directly, and it aligns the two handlers. It is also already the behaviour for
`mine=true`, which 401s on `!u.ID.Valid`.

### 6.2 The proof

Four tests, in the default lane:

1. **Unfiltered polling is not counted.** With the limit set to a small N, issue N+5 requests with no
   `q` as one user: all 200. Then N+1 requests with a needle as the same user against the same
   limiter instance: the last is 429. Same handler, same user, same instance, so the needle is the
   only discriminator.
2. **A whitespace-only `q` is not counted.** `?q=%20%20` is treated as absent by `parseFilterQ`'s
   trim. **This is the test that proves the placement**, because it is exactly the case a middleware
   predicate testing `Get("q") != ""` would count and the in-handler placement cannot.
3. **A refused needle costs no budget.** A `q` over `maxFilterQRunes`, and a non-UTF-8 `q`, each
   answer 400 and leave the bucket untouched, proven by then issuing the full quota successfully.
4. **The refusal touches no database statement.** Assert this structurally rather than by timing.
   `store.Queries` accepts any `DBTX`, so a stub that records or refuses every call is the seam;
   the assertion is that the 429 path made zero calls through it.

**Mutation checks the plan must run and record:**

- Move the bucket call above the `filters.Q != nil` gate. Test 1 must go red.
- Re-express the check as middleware keyed on `r.URL.Query().Get("q") != ""`. Test 2 must go red.
- Move the bucket call above `parseJobFilters`. Test 3 must go red.

A mutation that reddens nothing means the test does not pin the property, and each kill must name
the branch it reddened.

## 7. Decision 5: what a refused caller sees

**`429` with body `{"error": "search rate limit exceeded"}`.** Deliberately distinct from
`RateLimit`'s `"rate limit exceeded"`, which is already shared by login and register: a client and
an operator reading a log must be able to tell which control fired, and this project already
maintains byte-level guards over error bodies
(`TestFilterQ_BodiesAreIdenticalAcrossEndpoints`). **Required scope:** check what string the
write-side bucket shipped and reconcile the two in one place if they differ, reporting the choice.

**`Retry-After` is set, and its audience is documented where the header is documented.** The header
is correct HTTP and a scripted or third-party client can act on it. **No first-party client reads
it.** `ApiError` in `web/src/lib/api.ts` is constructed as
`new ApiError(res.status, code, ...)` where `code` is the parsed `{error}` string; it carries no
headers, and `apiFetch` never reads `res.headers`. So the README row for this control must not make
an unqualified `Retry-After` claim; it must say that the header is present and that the bundled SPA
does not read it. **Required scope: if the write bucket's README row makes an unqualified claim,
correct it in the same edit.** A wrong contract in docs is a defect, and repeating a known-wrong
advertisement because a sibling shipped it is the worst available reason.

**What the SPA actually does on a 429, stated so nobody has to guess.** `queryClient` sets
`retry: 1`, so one refusal becomes two requests about a second apart, then the query settles into
`error`. `keepPreviousData` keeps the previous rows on screen, and the jobs table surfaces an error
only when there is nothing else to render (`error && !data`), so the user sees **stale rows, no
visible error, and polling continuing at the same 3 s cadence**. That is the behaviour this slice
ships. It is acceptable and it is not good; making the SPA surface a refusal is named in section 13
as not covered, and the `retry: 1` doubling is part of why the default in 4.1 carries margin.

**No counter.** `/v1/server/counters` is not extended in this slice. A refusal counter is a signal an
unprivileged caller can drive at will, and the remedy such a counter invites ("429s are climbing,
raise the limit") is in the forger's favour. If operators later need it, it should ship with its
forgeability stated where the number is read, not where it is incremented.

## 8. The interaction with `maxFilterQRunes`

`maxFilterQRunes` is 200 and `parseFilterQ` rejects a longer needle with
`q is too long; maximum 200 characters`.

**What it bounds:** the per-row constant factor. `strpos(haystack, needle)` costs work proportional
to the product of the two lengths in the worst case, so capping the needle caps how expensive a
single row comparison can be, and it caps the parameter travelling to Postgres.

**What it does not bound, and this is the whole point:** the number of rows compared. That is the
dominant term and it is a function of table size, not of the needle. **The worst case is a needle
that matches nothing, and a no-match needle can be one character.** `?q=zq` is 198 runes under the
cap and pays the full walk, because a `LIMIT` can never be satisfied by a predicate nothing
satisfies, so the scan runs to completion where an unfiltered list stops after a page. The cost is
also not monotone in needle length: a longer needle is often cheaper per row, so the cap is not even
pointed at the expensive direction.

The comment on `maxFilterQRunes` says the needle length "is one of the few things bounding a single
request's scan cost". That is accurate about per-row work and it reads as more reassurance than it
is. README's own sentence is the precise one and should stay the load-bearing statement: the cost is
bounded by table size and by how often clients ask. This slice bounds the second of those. Nothing
here bounds the first.

## 9. Re-measurement, and recording the input with the number

The item requires re-measuring the amplifier after this lands. That requirement is kept and
tightened, because a measurement without its input reads as the typical case.

**Every number recorded must carry, in the same sentence:** the row count in `jobs` and in `users`,
**the exact needle string**, whether it matches zero rows, the sort arm, the `limit`, any other
filters on the request, the Postgres version, and whether the machine was otherwise idle.

**The needle is the input the existing measurements are missing.** Neither related item records it.
`feature-2026-09-03-server-side-bound-for-text-search` says "a no-match needle" at 200k rows,
283 ms versus 10.8 ms. `idea-2026-09-03-pg-trgm-index-for-text-search` says "a 50k-row probe" where
"that scan dominated the plan at about 31 ms". **Those two numbers are not comparable and neither
says so**: one is described as database CPU for a request, the other as a plan node's share, at
different row counts, with different needles nobody wrote down. A reader who puts them side by side
will infer a scaling curve that neither measurement supports.

**Three measurements are required after the change, at 200k rows so the comparison to the item's
own numbers is meaningful:**

1. **The unfiltered list** at the same row count. Expected: unchanged, near the item's 10.8 ms. This
   is the regression check for requirement 4 and for the pool-wide timeout.
2. **The no-match needle** at the same row count. **Expected: unchanged, near 283 ms.** This slice
   does not make the scan cheaper and the write-up must not let a reader conclude it did. The number
   that changes is how many of these one principal can buy, not what one costs.
3. **The refusal path.** Asserted structurally as touching no statement (section 6.2, test 4), not
   timed.

Plus the first-party count from section 4.1: actual q-carrying requests per minute in each of the
three jobs views and on the schedules page, against a running server, recorded per view.

`scripts/explain_sort_indexes/seed.go` already exists as a seeding harness and is the natural
starting point rather than a fresh one. The pg_trgm item notes that lane JB's probe files were left
uncommitted and would need re-creating; whatever this slice builds should be committed so the next
measurement starts from the same instrument.

## 10. Wiring, and a transpose hazard that must not be compounded

**Do not add the new pair to `api.New`'s positional list.** `buildHTTPServer` in
`cmd/relay-server/http_server.go` carries this in its own doc comment, as a measured fact:

> api.New is positional and takes four same-typed arguments in a row. Swapping
> loginLimitN/loginLimitWin with registerLimitN/registerLimitWin compiles, and every package stays
> green; login would then be rate-limited at the registration budget.

Adding a fifth and sixth same-typed argument to that run makes an already-unguarded transpose
strictly worse, and it would churn every existing construction (section 4.2).

**Use the exported-field route instead**, which is the established pattern for everything added to
`api.Server` after `New`: `AllowSelfRegister`, `Metrics`, `Counters` and `StaticHandler` are all set
this way, three of them inside `buildHTTPServer`. Two fields, documented at their declaration, with
zero values meaning "no limit", so every existing construction is unchanged.

**Three constraints on the wiring:**

1. **Exactly one limiter instance per Server.** `RateLimit` today mints a fresh `rateLimiter` and
   starts an unstoppable `go rl.gcLoop()` on every call, so a Server whose `Handler()` is called
   twice leaks a goroutine and splits its budget across two maps. In production `buildHTTPServer`
   calls `Handler()` once, so this is latent rather than live, but the read limiter must not
   reproduce it: construct it once per Server, not per `Handler()` call.
2. **The wiring assignment in `buildHTTPServer` is the same unguarded seam
   `idea-2026-08-14-generalize-the-env-to-field-wiring-guard` documents.** Deleting
   `s.Metrics = d.metrics` there is green across every package, measured. The new assignments will
   have the identical property. **Prefer an executed guard over a parsed one**, which that item
   names as the top rung: build a server through `buildHTTPServer` with the limit set, drive a
   q-carrying request past the ceiling, and assert 429. That test goes red if the assignment is
   deleted, and it does not add a row to any AST table.
3. **Env parsing stays in `main`**, per `httpServerDeps`'s comment: those parses end in `log.Fatalf`,
   which no test can call.

**The key space is bounded, unlike the IP-keyed limiters.** Keys are user ids and
`userRateLimitKey` fails closed, so an unauthenticated or unidentified flood creates zero map
entries. The map is bounded by the number of distinct users who searched inside the window, and
`gcOnce` prunes it. That is a real security property of failing closed, beyond the 401, and it is
worth one sentence at the helper.

## 11. Documentation changes (required scope, not cleanup)

- **README's `?q=` cost paragraph** currently says the server applies no rate limit and no statement
  timeout. It becomes false the moment this lands. Rewrite it to name both controls, both env vars,
  what each one does and does not bound (sections 3.5 and 4.1), and that a search exceeding the
  timeout answers 500.
- **README's environment table** gains `RELAY_DB_STATEMENT_TIMEOUT` and
  `RELAY_JOB_SEARCH_RATE_LIMIT`, beside `RELAY_DB_MAX_CONNS` and the two existing rate limits.
- **The schedules `?q=` paragraph** already forwards to the jobs cost anchor ("costs what it costs on
  the jobs list, for the same reason and with the same advice"), so it inherits the rewrite. Confirm
  it still reads correctly and does not need a second copy of the numbers.
- **`JobsPage`'s "THE DEBOUNCE IS NOT A BOUND" comment** says `GET /v1/jobs` carries no rate limit.
  That sentence becomes wrong. The comment's point survives (a client timer is not a server bound)
  but the premise must be corrected.
- **The write bucket's `Retry-After` row**, if it makes an unqualified claim (section 7).

## 12. Testing

**Default lane** (`make test`): everything in section 6.2; `ParseRateLimit` behaviour on the new
variable's default; the duration parser's refusal of a value that rounds to zero milliseconds and of
a malformed value; the `0` value producing no `RuntimeParams` key.

**Integration lane** (`make test-integration`): the NOTIFY listener surviving a statement timeout
shorter than its idle wait (section 3.4, the one silent failure mode); a text search actually being
cancelled by a very short pool timeout and surfacing as a 500 rather than hanging; the executed
`buildHTTPServer` wiring guard from section 10.

**Not required:** a browser lane. Nothing here changes rendering, and the SPA behaviour in section 7
is a consequence of existing `queryClient` configuration rather than of new client code.

**Every gated test needs a bounded failure.** A test that waits on a statement timeout must fail
with an assertion, never by hanging, or a mutation is indistinguishable from infrastructure trouble.

## 13. What this slice does NOT cover

- **It does not make the scan cheaper.**
  `docs/backlog/idea-2026-09-03-pg-trgm-index-for-text-search.md` reduces the cost; this bounds it.
  They are different items and landing either does not close the other. A trigram index would also
  require rewriting the `strpos` predicate to an escaped `ILIKE`, which
  `internal/store/query/jobs.sql`'s comment already flags.
- **It does not bound the unfiltered count.** `CountJobs` scans the whole table with no `LIMIT` on
  every list request, needle or not, and is what the 3 s poll pays even with an empty search box. It
  grows with the table. Not addressed here, and worth its own item if the table keeps growing.
- **It does not add a counter** to `/v1/server/counters` (section 7).
- **It does not distinguish SQLSTATE 57014** in the response body; a timed-out search answers 500
  like any other database failure (section 3.5).
- **It does not add `?q=` anywhere new.** `GET /v1/users` is
  `idea-2026-09-03-list-filters-remainder-status-labels-users-q`. It would inherit the pool-wide
  timeout automatically and would need its own decision about the bucket.
- **It does not change the SPA.** No 429 surface, no backoff, no debounce change. The observable
  behaviour on refusal is stale rows with no visible error (section 7).
- **It does not touch the write bucket or the login and register buckets**, beyond reconciling one
  error string and one README row.
- **It does not bound aggregate cost across principals.** The bound is per user id and degrades
  linearly in the number of accounts an adversary controls (section 4.1).
- **It does not reach `internal/cli` or `internal/mcp`.** Neither sends `?q=` today, confirmed by
  search. Worth knowing for later: `relayclient.ErrorIsTransient` classifies 429 as transient, so a
  future CLI or MCP search would put a refusal into a retry loop.

## 14. Where the item contradicts the tree or itself

Recorded so the plan does not inherit them.

1. **"The existing per-IP `RateLimit` applied to authenticated reads"** cannot be done as written:
   `RateLimit` keys on `clientIP(r)` and would collapse every user behind a proxy into one bucket.
   The item wants a second instance of the user-keyed mechanism (section 2).
2. **"a per-statement timeout for the text-count and text-list statements"** describes a set that
   does not exist. Only the count arms are split into text and non-text statements; every list arm
   is one statement serving both, and 33 generated structs carry `Q` (section 3.2).
3. **"so one needle cannot hold a connection for seconds"** names the right resource and the wrong
   priority. At the item's own 283 ms, no plausible statement timeout binds. The rate bucket is the
   control that bounds the amplifier at today's scale; the timeout is a floor under a failure that
   has no owner today (section 1, section 3.5).
4. **The item and its sibling report two incomparable measurements and neither says so**: 283 ms of
   "database CPU" at 200k rows here, "about 31 ms" of plan share at 50k rows in the pg_trgm item,
   with no needle recorded in either (section 9).
5. **The item's framing inherits README's "client-side debounce" as if the SPA were quiet.** The SPA
   polls the filtered list continuously while a needle sits in the box, at up to 5 requests per 3 s
   in the lanes view. The debounce bounds keystroke-driven requests and nothing else (section 4.1).
6. **"with its own bucket so list polling without q is unaffected"** is right about the requirement
   and silent about the mechanism that delivers it. A separate bucket does not deliver it; the
   in-handler placement after `parseFilterQ` does, and `?q=%20%20` is the input that tells the two
   apart (section 6).
