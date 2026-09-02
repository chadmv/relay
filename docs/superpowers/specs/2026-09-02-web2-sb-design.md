# Lane SB: schedules status, stats, filters, plus two enablers - backend contract

Date: 2026-09-02
Branch: `claude/web2-sb-schedules-backend`
Author: relay-tpm (autonomous gate mode; no human answered questions during this
flow, so every question in the Decisions section was decided here and the calls a
human might make the other way are listed under Escalations)

## Why this lane exists

Six deferred items share one property: each is blocked on a server-side fact the
API does not currently expose. The Schedules page cannot render a run-status dot
because the list carries a job id and no job status. Its summary strip counts the
50 rows it happens to hold. Its filter chips and search box would filter one page
of a paginated set, which is a lie under pagination. The worker detail page cannot
show which reservations target the worker because `GET /v1/reservations` is global
and unfilterable. And the SPA pays a `GET /v1/users/me` round trip on every login
because the auth response carries only a token.

This lane ships the server half only. Nothing under `web/` changes. The deliverable
is the API contract below, which the later frontend lanes implement against.

Backlog items in scope:

- `docs/backlog/idea-2026-06-05-last-job-link-status.md` (backend half)
- `docs/backlog/idea-2026-06-05-schedules-stats-endpoint.md`
- `docs/backlog/idea-2026-06-05-failed-24h-stat.md`
- `docs/backlog/idea-2026-06-05-schedules-filter-search.md`
- `docs/backlog/idea-2026-05-06-list-endpoint-filters.md` (the schedules slice only;
  see the sibling lane's Recommendation section, which this lane defers to)
- `docs/backlog/feature-2026-06-05-worker-detail-reservations-panel.md` (backend half)
- `docs/backlog/idea-2026-06-03-login-return-user-object.md` (backend half)

Sibling lane: `claude/web2-jb-jobs-filters`, whose spec is
`docs/superpowers/specs/2026-09-02-web2-jb-design.md`. The `q` contract in this
spec is copied from that one deliberately, so the two search boxes behave
identically. Where the two lanes touch the same README paragraph, this spec says so.

## What I verified against the tree, and what I refuted

A backlog proposal is not a contract. Every bullet was checked against the worktree
before it was designed around.

1. **Refuted: the list-variant count.** The lane brief says "14 list variants plus
   get". There are **16**: eight admin statements (`ListScheduledJobsPage` plus
   seven `ListScheduledJobsPageBy*`) and eight owner statements
   (`ListScheduledJobsByOwnerPage` plus seven `ListScheduledJobsByOwnerPageBy*`).
   `ScheduledJobsSortSpec` has four keys in two directions, which is eight arms per
   scope. The number that governs the edit for the filters is 16 list statements
   plus 2 counts = **18 statements touched, 0 added**.

2. **Confirmed: the parenthesisation hazard is present here, in exactly 7
   statements.** The sibling lane found that an unparenthesised
   `WHERE NOT @cursor_set::bool OR (keyset)` silently drops an appended `AND filter`
   on the first page, because `NOT cursor_set` is TRUE there and the OR is satisfied
   before the filter is ever reached. The seven `ListScheduledJobsPageBy*` statements
   have exactly that shape. `ListScheduledJobsPage` does not (its disjunction is
   already wrapped), and none of the eight owner statements do (they read
   `WHERE owner_id = ... AND (NOT cursor_set OR ...)`, already wrapped). So the hazard
   is real, it is confined to the admin non-default sort arms, and its discriminating
   input is a **first-page** request with a filter and a non-default sort. That is the
   headline test property in the Testing section.

3. **Refuted: the premise that `timed_out` might count in the failed-runs stat.**
   `jobs.status` cannot be `timed_out`. Migration `000019_status_vocabulary_checks`
   constrains it to `pending`, `running`, `done`, `failed`, `cancelled`; `timed_out`
   is a **task** status. `RecomputeJobStatus` rolls a job whose tasks are not all
   `done` into `failed` through its `ELSE` arm, so a job whose task timed out is
   already `failed` and already counted. There is nothing to decide.

4. **Refuted: "the last job was deleted" is a reachable state for a dangling
   `last_job_id`.** `scheduled_jobs.last_job_id` is
   `UUID REFERENCES jobs(id) ON DELETE SET NULL` (migration `000006_scheduled_jobs`).
   The database therefore guarantees that a non-NULL `last_job_id` names a row that
   exists. Separately, the `DeleteJob` statement has **no production caller** - only
   the sqlc-generated method exists - so jobs are not deleted through any live path
   today. Both facts point the same way and the FK is the one that binds: the item's
   "what if the job was deleted" case collapses into the "no last job" case.

5. **Confirmed, and it changes the design:**
   `TestScheduledJobResponse_ArityMatchesTheRow` in
   `internal/api/scheduled_jobs_response_test.go` asserts
   `len(scheduledJobResponse) == len(store.ScheduledJob) + 1`, where the `+1` is
   `OwnerEmail`. Any approach that changes the sqlc row type for the schedules list
   (a `LEFT JOIN` selecting an extra column makes sqlc emit a per-statement `Row`
   struct instead of `store.ScheduledJob`) breaks that test's premise rather than its
   arithmetic, in 17 places at once. This is a first-class argument against the join
   approach and for the second-query approach; see Decision 1.

6. **New defect found, not in any item: `run-now` does not move `last_job_id` or
   `last_run_at`.** `handleRunScheduledJobNow` calls `CreateJobFromSpec` and returns;
   only `schedrunner`'s `advance` calls `AdvanceScheduledJob`. So a job produced by
   `POST /v1/scheduled-jobs/{id}/run-now` carries `scheduled_job_id` (it will be
   counted by `failed_runs_24h`) but is invisible to `last_job_id` and therefore to
   `last_job_status`. An operator who clicks **Run now** and watches the row will see
   the LAST JOB cell keep pointing at the previous scheduled fire. This is
   pre-existing, it is out of scope here, and it is proposed as a backlog item -
   but the contract section states it, because the frontend lane would otherwise
   ship a cell whose behaviour looks like a bug in the new field.

7. **Confirmed: `last_error` and `last_job_status` are genuinely independent and can
   disagree.** `AdvanceScheduledJobAfterFailure` touches neither `last_job_id` nor
   `last_run_at` (its own comment says so: "no run completed and no job exists to
   point at"). So a schedule can carry `last_job_status: "done"` and a `last_error`
   at the same time, and the correct reading is "the last job it managed to produce
   finished successfully; the most recent attempt produced no job at all". The
   contract section states this and the render precedence for the frontend lane.

8. **Refuted (partially): the login item's field list is incomplete, and completing
   it is not optional.** The item asks for `id`, `email`, `name`, `is_admin`, and its
   2026-08-13 note adds `created_at`. `userResponse` also carries `archived_at`
   (not `omitempty`, so always present, `null` when active). Reusing `toUserResponse`
   - which the lane brief requires and which is the right call - brings `archived_at`
   along. That is correct, not a leak, and it must stay: a hand-built second literal
   that dropped it would be exactly the parallel-builder drift the reuse exists to
   prevent. On these two endpoints its value is always `null`, because an archived
   user is refused at login and a newly created one is never archived.

9. **Confirmed: the cross-generation 401 bug is closed, and the backend change is
   safe today.** `docs/backlog/closed/bug-2026-08-13-cross-generation-401-clears-a-new-session.md`
   shipped option A: `apiFetch` and `apiStream` stamp each 401 with the token the
   request attached, and `AuthProvider`'s listener ignores a 401 unless that token
   still equals `getToken()`. The reasoning is recorded at the listener itself. This
   lane's change adds one key to a response body; `applyAuth` still performs its
   `/users/me` fetch and `LoginResponse` is a TypeScript type that ignores unknown
   keys, so the addition is **inert for `web/`** until the frontend lane consumes it.
   See the Client compatibility section for the note that lane must carry.

10. **Nothing refuted** in the stats item, the failed-24h item, or the reservations
    panel item's own claims. The reservations item's "acceptable only if the list
    stays small" fallback (client-side filtering) is rejected on the same grounds the
    sibling lane rejected client-side search: a filter applied to one page is
    misleading under pagination, and `total` would label a different set.

## API contract

This section is the deliverable. The frontend lanes implement against it.

### 1. `last_job_status` on the scheduled-job list and get responses

`scheduledJobResponse` gains one field:

```
LastJobStatus string `json:"last_job_status,omitempty"`
```

- It carries `jobs.status` **verbatim**, from the closed set
  `pending` | `running` | `done` | `failed` | `cancelled`. No remapping. In
  particular it is **not** the `pending` -> `queued` rename that `jobStatsResponse`
  performs; it agrees with `jobResponse.status`, which is what the LAST JOB cell
  links to.
- **`last_job_status` is present exactly when `last_job_id` is present.** Not "usually";
  the two keys appear together or neither appears. This is enforceable because the FK
  guarantees a non-NULL `last_job_id` names an existing row (finding 4), and because
  a failure of the enrichment lookup is a 500 rather than a silently absent key
  (Decision 2).
- Absent means **there has never been a scheduled fire that produced a job**. It does
  not mean "unknown" and it never means "healthy". The key set is closed and pinned
  by a response-shape test in both states; the project's rule that an `omitempty`
  field absent on both sides compares equal whatever its name is why the pin is by
  **key presence against hand-written names**, never by deep-equal against a fixture
  built from `scheduledJobResponse`.
- Interplay with `last_error`: independent, and both may be present at once
  (finding 7). For the frontend lane: `last_error` is the row-level health signal and
  should win the row's warning treatment; `last_job_status` describes the linked job
  and belongs on the LAST JOB cell. A row with `last_error` present and
  `last_job_status: "done"` is not a contradiction.
- Known wart, pre-existing (finding 6): a `run-now` job does not update
  `last_job_id`, so `last_job_status` does not reflect it.

`GET /v1/scheduled-jobs/{id}` carries the same field on the same terms.

### 2. `GET /v1/scheduled-jobs/stats`

Auth: **authenticated, not admin-only.** Scope: fleet-wide for admins, owner-scoped
for everyone else, by exactly the predicate `ListScheduledJobsByOwnerPage` uses
(`scheduled_jobs.owner_id = <caller>`), so the strip can never disagree with the page
beneath it.

This follows `GET /v1/workers/stats` (auth-only) and deliberately not
`GET /v1/server/counters` (admin-only). The distinction is already written down at
the route table: `/v1/server/counters` is process-lifetime in-memory state describing
adversary activity, while a stats endpoint is a database census of rows the caller may
already list one page at a time. This endpoint discloses no row the caller could not
already page through.

Response body:

```json
{
  "enabled": 12,
  "paused": 3,
  "total": 15,
  "failed_runs_24h": 2,
  "failing": 1
}
```

| Field | Definition |
|---|---|
| `enabled` | schedules with `enabled = TRUE`, in scope |
| `paused` | schedules with `enabled = FALSE`, in scope. `paused` is exactly `NOT enabled`; there is no third state and no `paused` column |
| `total` | `enabled + paused`, computed in Go from the two buckets, so the identity holds by construction |
| `failed_runs_24h` | jobs `j` where `j.scheduled_job_id IS NOT NULL`, `j.status = 'failed'`, and `j.updated_at >= NOW() - INTERVAL '24 hours'`, scoped through `scheduled_jobs.owner_id` |
| `failing` | schedules in scope whose `last_error IS NOT NULL`. **Not windowed** |

All five are non-negative integers and all five keys are always present; there is no
`omitempty` anywhere in this response.

Three definitions need their reasoning on the record, because each has a plausible
alternative that a reader will assume was chosen:

**`failed_runs_24h` is windowed on `jobs.updated_at`, not `created_at`.** That is the
same finish-time proxy `JobStatusCounts` already uses for the dashboard's `failed_24h`,
along with its documented limitation (`updated_at` has two writers and means "time of
the last task-level event"; `docs/backlog/bug-2026-06-05-jobs-stats-24h-updated-at-proxy.md`
is open against it). Inheriting one known limitation is strictly better than inventing
a second, divergent definition of "in the last 24 hours" on the same dashboard, and it
means this number inherits the fix when that item closes. `created_at` would answer a
different question - "runs that STARTED in the window" - and would count a job that
started 23 hours ago and is still running as neither failed nor not.

**`failed_runs_24h` excludes `cancelled`, unlike `jobStatsResponse.failed_24h`, and the
field is named differently for exactly that reason.** A cancelled job is an operator
action, not a schedule fault; a strip whose purpose is "which of my schedules is broken"
must not turn an operator's own cancellation into an alarm. Two fields spelled
`failed_24h` with different definitions on two pages of the same product is the
wrong-prose-about-correct-code defect this repo keeps finding, so the name carries the
difference: `failed_runs_24h` is about **runs of schedules** and is not the dashboard
number.

**`failing` is a separate field, not folded into `failed_runs_24h`.** A spawn failure
recorded in `last_error` never becomes a job, so it is invisible to any count over
`jobs`. It is also counted in a different unit: `last_error` and `last_error_at` record
only the most recent failure, so a schedule that failed 48 times in 24 hours contributes
one. Summing a job count and a schedule count into a single integer produces a number
whose loss is invisible at the point it is read, which is the failure mode this project
has already written down as a rule. Two fields, two units, two definitions.

**`/stats` accepts no filters.** It is always the whole in-scope census. It is not
affected by `?enabled=` or `?q=`, and `stats.total` equals the list's `total` only when
no filter is active. The frontend reads `total` off the list for "N matching" and off
`/stats` for the strip. README says this explicitly.

Route: `GET /v1/scheduled-jobs/stats`, registered with `auth(...)` alongside the other
scheduled-jobs routes. Go 1.22's ServeMux prefers the literal segment over
`/v1/scheduled-jobs/{id}`, the same way `/v1/jobs/stats` already coexists with
`/v1/jobs/{id}`, and `stats` is not a UUID so the id route could never have matched it
usefully anyway.

### 3. New query parameters on `GET /v1/scheduled-jobs`

| Param | Type | Format | Absent means |
|---|---|---|---|
| `enabled` | boolean | `true` / `false` (Go `strconv.ParseBool` spellings) | no enabled filter |
| `q` | string | free text, case-insensitive substring | no text filter |

**`enabled`** is a genuine tri-state and differs from the sibling lane's `mine` in
exactly this: `enabled=false` is a real filter meaning "only paused schedules", not a
synonym for absent. It parses into a `*bool`. An empty value (`?enabled=`) is treated
as absent, matching how every other optional string parameter in this codebase is read.

**`q`** matches when the trimmed needle is a case-insensitive substring of the
schedule's `name`, the owner's `email`, or the `cron_expr`. Matching is plain substring
containment: `%` and `_` are **literal characters**, not wildcards. A `q` that is empty
or whitespace-only after trimming is treated exactly as an absent `q`.

Two notes on the email axis, so the frontend lane is not surprised:

- For a **non-admin**, the email axis can only ever match all-or-nothing, because every
  row in scope has the same owner. Typing your own address returns your whole list;
  typing anyone else's returns nothing. That is correct and it is not worth special-casing.
- The cron axis matches the stored text verbatim, so `@daily` is found by `daily` and
  `0 4 * * *` is found by `0 4`.

### 4. New query parameter on `GET /v1/reservations`

| Param | Type | Format | Absent means |
|---|---|---|---|
| `worker_id` | UUID | canonical UUID text | no worker filter |

Matches reservations whose `worker_ids` array contains that id. The endpoint **stays
admin-only**, so the worker-detail reservations panel the frontend lane builds against
it must be admin-gated; a non-admin viewing the worker detail page sees no panel, not
an empty one.

An id that names no worker, or a worker no reservation targets, returns an empty page
with `total: 0`. It is deliberately **not** a 404: `reservations.worker_ids` is a bare
`UUID[]` with no foreign key (this is the one place a worker id can outlive its row, as
`RemoveWorkerFromReservations` documents), so "unknown id" is not a state this endpoint
can authoritatively distinguish, and adding a workers lookup to find out would couple
two tables to answer a question nobody asked. `?worker_id=` with an empty value is
treated as absent.

### Errors

All errors use the existing `writeError` shape, `{"error": "<message>"}`, with status
400. The messages are part of the contract and are pinned by tests. The `q` rows are
byte-identical to the sibling lane's by design.

| Endpoint | Condition | Body |
|---|---|---|
| `/v1/scheduled-jobs` | `enabled` not parseable as a bool | `invalid enabled; expected true or false` |
| `/v1/scheduled-jobs` | `q` longer than 200 runes | `q is too long; maximum 200 characters` |
| `/v1/scheduled-jobs` | `q` not valid UTF-8 | `q is not valid UTF-8` |
| `/v1/scheduled-jobs` | `enabled` or `q` repeated in the query string | `query parameter "<name>" must appear at most once` |
| `/v1/reservations` | `worker_id` not a UUID | `invalid worker_id; expected a UUID` |
| `/v1/reservations` | `worker_id` repeated in the query string | `query parameter "worker_id" must appear at most once` |

The UTF-8 check is not defensive decoration, and the reason is the sibling lane's: Go's
query parser percent-decodes without validating UTF-8, so a `q` containing an invalid
byte sequence would otherwise reach Postgres as a text parameter and user input could
produce a 5xx. The test pins the 400 and must not assert anything about what the
database would otherwise have done.

The at-most-once rule follows the precedent set by `?task=` on the retry endpoint:
`Query().Get` silently returns the first of a repeated parameter, and a silently wrong
filter renders a list that looks authoritative.

**The `worker_id` 400 does not echo the supplied value**, which is a deliberate
divergence from `handleCreateReservation`'s `invalid worker_id: `+wid. Rendering nothing
input-derived is the project's standing preference and it costs nothing here. The
create-path echo is pre-existing, is inside an admin-only JSON response, and is not
changed by this lane; it is proposed as a low-priority backlog item.

### Composition rules

- `enabled` and `q` **AND together**, and compose with `?limit=`, `?cursor=` and
  `?sort=`. There is no rejected combination.
- `worker_id` composes with `?limit=`, `?cursor=` and `?sort=` on
  `GET /v1/reservations`. There is no rejected combination.
- **There is no sort-versus-filter rule on either endpoint today, and this lane does not
  create one.** `handleListScheduledJobs` and `handleListReservations` have no filter
  branch at all: no `hasFilter`, no filtered statement variants, no 400. The rule that
  exists on `GET /v1/jobs` is a guard over a gap in the SQL layer - two filtered
  statements there hard-code one `ORDER BY` - and the new predicates here do not create
  that gap, because they are threaded into every sort variant as optional arguments and
  never touch `ORDER BY`. That is the whole reason for the sqlc strategy below.

### Cursor behaviour

Unchanged. Cursors stay opaque, stay tagged with the sort they were issued under, and
carry no record of the filters that were active. Consequences the frontend lanes must
respect:

- **Reset the cursor whenever `enabled`, `q` or `worker_id` changes.** The server does
  not enforce this.
- Filter correctness is nevertheless cursor-independent. Every predicate is applied in
  SQL alongside the keyset comparison, so a stale cursor can produce a page that starts
  at a surprising position but can **never** return a row that fails the current
  predicates. That property is an acceptance criterion with its own test.

The missing filter fingerprint in the cursor belongs to every paginated endpoint, not
to these two; the sibling lane already proposes it as a backlog item and this lane does
not duplicate the proposal.

### `total` semantics

Unchanged in meaning, and load-bearing: `total` is the server-side count of **all rows
matching every active predicate**, independent of the cursor. So `CountScheduledJobs`,
`CountScheduledJobsByOwner` and `CountReservations` gain the same optional predicates
as their list statements. A `total` that ignored `q` would make the page footer read
"1 - 50 of 312" while showing three search hits.

### 5. `user` on the login and register response bodies

`POST /v1/auth/login`, and both arms of `POST /v1/auth/register` (invite redemption and
self-serve), return one additional key:

```json
{
  "token": "3f0a...c91d",
  "expires_at": "2026-10-02T12:00:00Z",
  "user": {
    "id": "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
    "email": "operator@example.com",
    "name": "Operator",
    "is_admin": false,
    "created_at": "2026-09-02T12:00:00Z",
    "archived_at": null
  }
}
```

- `user` is **exactly** the `GET /v1/users/me` body, produced by the same
  `toUserResponse` builder. Six keys, `archived_at` always present and always `null` on
  these two endpoints (finding 8).
- `token` and `expires_at` are byte-identical to today. The three handlers currently
  write a `map[string]any` holding just those two keys; they move to a single shared
  `authResponse` struct so that three literals cannot drift apart, and a `time.Time`
  marshals identically as a map value and as a struct field.
- No password hash can appear: `userResponse` is a private struct defined precisely so
  the hash cannot leak even if a store row type changes, and its own comment says so.
- Security: the row returned is the caller's own, after successful authentication.
  `is_admin` is already readable at `/v1/users/me` by the same caller. No new exposure,
  and no change to the email-enumeration behaviour of `handleLogin` - the addition sits
  after every existing refusal.

## Design

### sqlc strategy

Three separate problems, three different answers, and the differences are the design.

#### 5a. `last_job_status`: a second query, not a join

Three options were considered.

**Option A (chosen): a batch enrichment query, mirroring `fillOwnerEmails`.** One new
statement:

```
-- name: GetJobStatusesByIDs :many
SELECT id, status FROM jobs WHERE id = ANY(@ids::uuid[]);
```

and one new helper `fillLastJobStatuses(r, items) error` in
`internal/api/scheduled_jobs.go`, which collects the non-empty `LastJobID` values from
the items, dedupes them, issues one query, and writes the statuses back in place. It is
called from the two places `fillOwnerEmails` is called from: the two list arms and
`handleGetScheduledJob`.

**Zero list statements change. Zero sqlc row types change.** That is the point. Adding
`LEFT JOIN jobs lj ON lj.id = sj.last_job_id` and selecting `lj.status` would make sqlc
emit a per-statement `Row` struct in place of `store.ScheduledJob` for all 16 list
statements and for `GetScheduledJob`. That cascades into 17 new conversion functions,
eight new row-key functions, and - worse - it breaks the premise of
`TestScheduledJobResponse_ArityMatchesTheRow`, which exists to catch exactly the class
of silent omission this lane is adding a field to (finding 5). `GetScheduledJob`'s type
is also load-bearing for `ownedScheduledJob`, `handlePatchScheduledJob` and
`handleRunScheduledJobNow`, none of which want a wider row.

**Option B (rejected): a `LEFT JOIN` on all 17 statements.** Above.

**Option C (rejected): a scalar subquery in the SELECT list.** Reads better than the
join and has an identical Go blast radius, because it still adds a column and therefore
still changes the row type. It buys nothing.

The cost of Option A is one extra round trip per list request, batched so it is O(1) in
the page size, on a primary-key `= ANY` lookup. That is the same cost `fillOwnerEmails`
already pays on every request to this endpoint.

The one place Option A deliberately diverges from `fillOwnerEmails` is its failure
handling; see Decision 2.

#### 5b. Schedules filters: optional predicates threaded through the 18 existing statements

Every list statement gains one `WHERE` block of the shape

```
AND (sqlc.narg(enabled)::bool IS NULL OR sj.enabled = sqlc.narg(enabled)::bool)
AND (sqlc.narg(q)::text IS NULL
     OR strpos(lower(sj.name),      lower(sqlc.narg(q)::text)) > 0
     OR strpos(lower(u.email),      lower(sqlc.narg(q)::text)) > 0
     OR strpos(lower(sj.cron_expr), lower(sqlc.narg(q)::text)) > 0)
```

and the two count statements gain the same block. All 18 acquire
`JOIN users u ON u.id = sj.owner_id`.

**The join cannot change any result.** `scheduled_jobs.owner_id` is
`UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE` and `users.id` is the primary
key, so the join is total on the left and at most one row on the right: it can neither
drop a schedule nor duplicate one, on the lists or on the counts.

**The row type does not change, because only `sj.*` is selected.** This is the
difference from the jobs list, which gets a `Row` struct because it selects
`u.email` as an extra column. Selecting `sj.*` through a join leaves sqlc emitting
`[]store.ScheduledJob`, so `toScheduledJobResponse`, the four row-key functions and
`buildPage`'s call sites are all untouched. `fillOwnerEmails` stays exactly as it is;
this lane does not fold the email lookup into the join, because doing so would change
the row type for no benefit this lane needs.

**Parenthesise first, then append.** The seven `ListScheduledJobsPageBy*` statements
must have their cursor disjunction wrapped in parentheses in the same edit that appends
the filter block (finding 2). Doing the two in one edit is deliberate: a reviewer sees
the bare `OR` and the new `AND` in the same hunk.

**`strpos` and not `ILIKE`,** for the reason the sibling lane recorded: an `ILIKE`
pattern built by concatenating percent signs around `q` makes user input a pattern, so a
user typing `%` matches every schedule. Escaping fixes that and then lives in 18 copies
of a nested `replace`, where one miscopy is a silent wildcard.
`strpos(lower(col), lower(needle)) > 0` has no metacharacters and nothing to escape, and
it reads identically in all 18 copies. The cost, recorded so nobody rediscovers it: a
`pg_trgm` index can never serve `strpos`. If pg_trgm is adopted the predicate must be
rewritten to escaped `ILIKE` at that time, which is mechanical and fully covered by the
arm-enumerating test battery. Loud-and-later beats silent-and-now.

`lower()` is Postgres's locale-dependent lowercasing, ASCII-correct and therefore
correct for email addresses, cron expressions, and the schedule names this farm produces.

Option A's real risk is not SQL, it is Go: each generated `Params` struct gains two
fields that must be set at 18 call sites, and a forgotten field is a zero value that
silently disables a filter for exactly one arm, or leaves `total` counting a larger set
than the page it labels. Two mitigations, both required:

1. **One parse, one struct.** A single `parseScheduleFilters(w, r)` in
   `internal/api/scheduled_jobs.go` is the only place that reads `enabled` and `q`,
   validates them, and produces store-typed values. Every call site spreads from that
   one struct; no call site parses anything itself. Same spirit as the single-JSON-entry-point
   invariant.
2. **A behavioural guard that enumerates the arms.** See Testing.

#### 5c. Reservations filter: the same technique, 9 statements

The eight list statements and `CountReservations` gain

```
AND (sqlc.narg(worker_id)::uuid IS NULL
     OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
```

Seven of the eight reservations list statements carry the unparenthesised
`WHERE NOT @cursor_set::bool OR ...` shape - the four `CASE`-based NULLS-ordering
statements included, since their outer disjunction is bare even though the `CASE` inside
it is wrapped. **Those seven need the same parenthesisation fix before the `AND` is
appended.** `ListReservationsPage` is the exception, already wrapped, exactly as
`ListScheduledJobsPage` is. The hazard and its discriminating input (a first-page request
with the filter active) are identical to the schedules one.

**Containment (`@>`) rather than `= ANY`,** even though this lane adds no index. The two
are equivalent for a single element; `= ANY` cannot be served by a GIN index and `@>`
can, so choosing the indexable spelling now means a later index needs no query rewrite
in nine places.

`parseReservationFilters(w, r)` is the sole producer, on the same terms as 5b.

#### 5d. The shared `q` parsing, and the fan-in hazard this lane must not create

This lane and the sibling lane both need identical `q` validation: trim-to-absent, the
200-rune cap, the UTF-8 check, at-most-once, and four exact 400 bodies. Two independent
copies of that is a drift hazard that will not show up in either lane's tests, because
each lane only tests its own endpoint.

Required: **whichever lane merges second deletes its copy and calls the first lane's
helper**, which lives in `internal/api` beside `parsePage`. The conductor owns this at
fan-in; it is not optional cleanup. The property to assert after the merge is that the
same malformed `q` returns a byte-identical body from `GET /v1/jobs` and from
`GET /v1/scheduled-jobs`, and it is listed under Acceptance criteria as a fan-in item
rather than a lane item, because neither lane can run it alone.

The at-most-once check is a second shared helper of the same kind
(`queryAtMostOnce(w, r, name)`), used by `enabled`, `q` and `worker_id` here and by the
sibling lane's four parameters, and it is subject to the same rule.

### Performance, and what is out of scope

**`q` on schedules is a sequential scan.** There is no index that serves substring
containment and none is being added. The honest scale argument: `scheduled_jobs` is the
smallest of the paginated tables in this product - one row per recurring definition, not
per run - so a farm with thousands of schedules would be remarkable. There is no
`LEFT JOIN LATERAL` on these statements, so unlike the jobs list there is no per-row
aggregate to dominate the cost. This is one debounced search box over a small table; the
frontend lane must debounce at 250 ms or more, `q` is capped at 200 runes, and the
endpoint sits behind the existing per-IP rate limiter.

**`worker_id` on reservations is a sequential scan.** No index, and this is a decision
rather than an omission: `reservations` is an admin-managed table holding tens of rows
in any realistic deployment, and Postgres will not choose a GIN index on a table that
small anyway. Revisit when a deployment has thousands of reservations; the query
spelling is already the indexable one.

**No `EXPLAIN` is required for this lane**, and the reason is worth stating rather than
leaving as an omission: the sibling lane requires one because it filters the busiest read
path in the product through a per-row LATERAL aggregate. Neither table here is on a hot
path and neither statement has an aggregate per row. If the implementation finds
otherwise, record the plan with its seeded row count, because a measurement without its
input reads as the typical case.

**No migration is needed in this lane.** No new column (`last_job_status` is derived,
`failing` reads the existing `last_error`), and no new index (above). The sibling lane
takes `000023`; **this lane adds nothing, so `000024` remains free** for whichever slice
needs it next. If review overturns the no-index decision, the index migration is
`000024` and must be plain `CREATE INDEX`, never `CONCURRENTLY`, because golang-migrate
wraps each migration in a transaction.

### Client compatibility

Four consumers decode a body this lane changes.

**The login and register bodies** are decoded by `internal/cli/login.go` and
`internal/cli/register.go`, each into a local anonymous struct with `token` and
`expires_at` only. `encoding/json` ignores unknown keys by default and nothing in this
repo sets `DisallowUnknownFields` (verified by search; the only mentions are in a
backlog item discussing whether `readJSON` should). The python SDK never calls login -
it reads a token from config or `RELAY_TOKEN` - and `internal/mcp` calls it only from
its own integration test, into a `map[string]any`. `web/`'s `applyAuth` decodes into the
`LoginResponse` TypeScript type and ignores the new key.

**The scheduled-jobs list body** is decoded by `relay schedules list`
(`internal/cli/schedules.go`) and by the SPA. Both ignore an added key.

**The reservations body does not change** - only a query parameter is added - so
`internal/cli/reservations.go` is unaffected.

That is an argument, not evidence, so both changed bodies get a named live-server test;
see Testing. The honest state of the gates matters here: the CLI real-server lane covers
`schedules list` today (`internal/cli/schedules_integration_test.go`) and covers
**nothing for `relay login`**, because `startRelayServer` seeds tokens directly through
the store rather than through the endpoint. So no lane pins CLI-to-server compatibility
for the auth body today. This lane closes that gap with one new test. The harness's
seeded users have a known password, so the test needs no new fixture machinery.

**Exposing the new parameters and fields in the CLI, the MCP tool and the python SDK is
out of scope**, and is proposed as a **single** follow-up item rather than five, because
all three clients implement the same contract and should be reviewed together. This
matches the sibling lane's recommendation, and the conductor should consider filing one
item covering both lanes' client exposure rather than two.

### README changes (required scope)

Seven edits, named by section.

1. **`### Scheduled Jobs` endpoint table.** Add the `GET /v1/scheduled-jobs/stats` row.
   Amend the `GET /v1/scheduled-jobs` row to name `?enabled=` and `?q=`.
2. **`### Scheduled Jobs`, new response-fields paragraph.** Document `last_job_status`:
   the vocabulary, the present-exactly-when-`last_job_id`-is-present invariant, that
   absent means no scheduled fire has produced a job, and that `run-now` does not move
   it.
3. **`### Scheduled Jobs`, new stats subsection.** The response body with all five
   fields, each definition in full, including that `failed_runs_24h` excludes
   `cancelled` and is **not** the dashboard's `failed_24h`, that `failing` is not
   windowed, and that `/stats` accepts no filters so `stats.total` equals the list's
   `total` only when no filter is active.
4. **`### Scheduled jobs` narrative section**, where the `last_error` contract is
   already written out at length. One sentence distinguishing `last_error` (a fire that
   produced no job) from `last_job_status` (the last job that was produced), stating
   that both may be present at once and what that combination means.
5. **`#### Configurable sort order`, the Filter + sort paragraph.** It currently ends
   "Other endpoints' filters do not currently combine with sort." **That sentence becomes
   false when this lane merges** and must be corrected to say that schedules `?enabled=`
   and `?q=` and reservations `?worker_id=` all compose with `?sort=`. This is the same
   paragraph the sibling lane rewrites for its own four parameters; **it is the batch's
   merge hotspot** and the conductor should expect a conflict and resolve it by keeping
   both corrections, not by taking one side. Wrong prose about correct code is this
   repo's dominant defect class and a reader who infers "filters exclude sort" from the
   unamended paragraph will write a frontend that never combines search with sorting.
6. **Pagination section, the `q` subsection.** The sibling lane adds a subsection
   documenting `q` for `GET /v1/jobs`. This lane's `q` has identical semantics and
   identical 400 bodies, so the second lane to merge must generalise that subsection to
   cover both endpoints and name the per-endpoint match axes (jobs: name and owner email;
   schedules: name, owner email and cron expression) rather than writing a second
   near-duplicate subsection.
7. **`### Reservations` endpoint table and `### Public` auth section.** Name
   `?worker_id=` on `GET /v1/reservations`, that the endpoint is admin-only and that an
   unknown id is an empty page rather than a 404. Under the auth endpoints, show the new
   login and register response bodies including `user`, and state that it is exactly the
   `GET /v1/users/me` shape.

`python/README.md` documents SDK method signatures rather than endpoint parameter sets,
so it needs no change while the SDK does not expose any of this. `web/` is unchanged.

## Testing

The lane is TDD, engineer-owned; the properties below are what the spec requires, not
prescribed test bodies.

**The anti-drift guard, and its discriminating input.** A table-driven integration test
in `internal/api` that, for each schedules list arm, seeds two schedules distinguishable
on both filter axes and asserts each predicate returns exactly the one matching row with
`total == 1`. Two requirements on it:

- The arm table must be **derived from `ScheduledJobsSortSpec.Keys`** (each key in both
  directions) crossed with admin and owner scope, so a future sort key added without its
  filter arms turns this test RED rather than shipping one silently unfiltered ordering.
- **Every arm must be exercised on the FIRST page, with no cursor.** That is the input
  that discriminates the parenthesisation defect (finding 2): with a cursor present the
  unparenthesised disjunction behaves correctly, so a test that only walks to page two
  passes against the bug. This is the single most important sentence in this section.

The same guard, with the same first-page requirement, for the eight reservations arms
against `?worker_id=`.

Also required:

- One test per 400 in the error table, asserting the exact `error` string.
- `enabled=false` returns only paused schedules and is **not** treated as absent. The
  fixture must contain at least one enabled and one paused schedule, so a mutant that
  ignores the parameter fails.
- `q` matches on all three axes independently: a schedule matched by name only, one by
  owner email only, one by cron expression only. Three separate rows, so a dropped `OR`
  arm is caught.
- `q` treats `%` and `_` as literals.
- `total` equals the count of rows matching every active predicate, on both scopes,
  independent of the cursor.
- Cursor-independence: take a cursor under one filter, resend it with a different
  filter, assert the response contains no row failing the new predicate.
- Owner scoping holds under filters: two users' schedules are disjoint under the identical
  filtered request, and an admin sees both.
- **`last_job_status` presence pairing**, in the default lane and through
  `map[string]any` decoded from real marshalled JSON, never through a fixture built from
  `scheduledJobResponse`: a schedule with no `last_job_id` carries **neither** key; a
  schedule with one carries **both**; the always-present keys are asserted as a control
  so a mutant that dropped every optional key does not look like a pass. This extends
  `internal/api/scheduled_jobs_response_test.go`, whose header already states this rule.
- **`TestScheduledJobResponse_ArityMatchesTheRow` moves from `responseOnlyFields = 1` to
  `2`**, and the commit message says what supplies the new one (`fillLastJobStatuses`).
  Changing that constant is the deliberate acknowledgement the test exists to force; it
  must not be changed in a commit that does not also add the field.
- An integration test that the status served is the live one: seed a schedule with a
  `last_job_id`, flip that job's status, re-read, assert the response follows.
- `/stats` per bucket: a fixture with enabled, paused, a schedule whose last job failed
  inside the window, one whose last job failed outside it, one whose last job is `done`,
  one cancelled job (asserting it is **not** counted), and one schedule carrying
  `last_error` with an older successful job (asserting it counts in `failing` and not in
  `failed_runs_24h`). Each bucket must have at least one row that belongs to no other
  bucket, so a mutant that swaps two `FILTER` clauses fails.
- `/stats` owner scoping: two users get different numbers from the identical request,
  and each user's `total` equals the `total` their unfiltered list returns.
- `/stats` `total == enabled + paused` exactly, with a fixture where both are non-zero
  and unequal (equal buckets make a transposition invisible).
- `/stats` is reachable by a non-admin and is not shadowed by the `{id}` route.
- Auth body: a `map[string]any` test that `POST /v1/auth/login` and both register arms
  return exactly `token`, `expires_at`, `user`, and that `user` has exactly the six keys
  `GET /v1/users/me` returns - asserted against **hand-written key names**, and asserted
  against `/users/me`'s own live response in the integration lane so the two cannot
  drift. No key named anything like `password` appears anywhere in the body.
- **Existing schedules, reservations and auth tests keep a zero-line diff**, with the
  single named exception of `responseOnlyFields` above. If any other existing test needs
  editing, that is a behaviour change and must be justified in review, not absorbed.
  `internal/api/scheduled_jobs_sort_integration_test.go`,
  `scheduled_jobs_owner_email_integration_test.go`,
  `scheduled_jobs_failure_visibility_integration_test.go` and
  `scheduled_jobs_run_now_bounds_integration_test.go` are the files this applies to most
  directly.
- **A new `internal/cli` live-server test for `relay login`**, driving the real login
  command against the harness with `readPasswordFn` and `saveConfigFn` stubbed,
  asserting the token is saved and authenticates a subsequent call. This is the only
  lane that can pin CLI-to-server compatibility for the auth body, and it does not exist
  today.
- **The existing `internal/cli` `schedules list` live-server test stays green** against
  the new response field, with a zero-line diff. That is the pin that the added key does
  not break the CLI decoder.

Gates: `make test`, `make test-integration` for `internal/api` with `-timeout 1800s`,
`make test-cli-integration`, and `-race` through the Linux container per CLAUDE.md.
After `make generate`, follow the CRLF procedure: never conclude "nothing to revert"
from `git diff` alone, keep only content hunks, and confirm `git ls-files --eol` reads
`i/lf` on every touched path. Because this lane edits SQL comments as well as SQL,
verify the regenerated `.sql.go` files actually carry the content change after the CRLF
revert - the revert can silently discard a regenerated file.

## Decisions

**1. `last_job_status` is enriched by a second batched query, not by a join.** Options:
`LEFT JOIN` on all 17 statements; a scalar subquery; a batch enrichment query. Chose the
batch query. The join and the subquery both change the sqlc row type from
`store.ScheduledJob` to 17 per-statement `Row` structs, cascading into 17 conversion
functions and eight row-key functions, widening `GetScheduledJob` for three handlers
that do not want it, and breaking the premise of the arity test that exists to catch
exactly this class of silent omission. The batch query changes zero list statements and
follows `fillOwnerEmails`, an established precedent in the same file for the same shape
of problem.

**2. An enrichment lookup failure is a 500, not a silently absent field - and this
deliberately differs from `fillOwnerEmails`.** Options: degrade best-effort as
`fillOwnerEmails` does; fail the request. Chose fail. The two are not analogous.
`owner_email` is a non-`omitempty` key that is always present, so an empty value is
visibly unknown and the list is still usable. `last_job_status` signals through key
**presence**, so degrading would forge the signal "this schedule has never produced a
job" out of a database fault, and the frontend would render a missing dot as a fact.
Collapsing a fault into a data state is the lossy-aggregate defect this project has
already written down. The cost is one more 500 surface on a primary-key `= ANY` lookup
that is unlikely to fail alone if the list query just succeeded.

**3. `last_job_status` is `omitempty` and paired to `last_job_id`, rather than always
present as an empty string.** Options: always present, empty when absent; `omitempty`
with a presence contract. Chose `omitempty`, matching `last_job_id` immediately beside
it, whose own `omitempty` this field must agree with or the pair becomes incoherent.
Decision 2 is what makes it safe: because a fault cannot produce an absent key, absence
has exactly one meaning. The project's rule that an absent `omitempty` field compares
equal on both sides whatever its name is answered by pinning key presence against
hand-written names in both states, never by deep-equal against a typed fixture.

**4. The status is served verbatim from `jobs.status`.** Options: verbatim; remapped to
the dashboard's vocabulary (`pending` -> `queued`). Chose verbatim, so the field agrees
with `jobResponse.status` on the page the LAST JOB cell links to. The `queued` rename
exists only inside `jobStatsResponse` and should not spread.

**5. `/v1/scheduled-jobs/stats` is auth-only and owner-scoped, not admin-only.** Options:
admin-only like `/v1/server/counters`; auth-only and owner-scoped like
`/v1/workers/stats`. Chose auth-only. The list beneath the strip is owner-scoped for
non-admins, so an admin-only stats endpoint would leave every non-admin with a
page-scoped strip and the item unresolved for exactly the users who have the fewest
schedules to page through. The endpoint discloses no row the caller cannot already list.

**6. `failed_runs_24h` is windowed on `jobs.updated_at`.** Options: `updated_at`;
`created_at`; a derived finish time from the tasks LATERAL. Chose `updated_at`, matching
`JobStatusCounts` exactly, so the two "in the last 24 hours" numbers on the product's
two summary strips mean the same thing and inherit the same open limitation and the same
future fix. `created_at` answers a different question. A LATERAL-derived finish time is
not predicable without restructuring the query.

**7. `failed_runs_24h` excludes `cancelled`, and is named differently from
`jobStatsResponse.failed_24h` because of it.** Options: mirror the dashboard exactly and
keep the name; exclude `cancelled` and keep the name; exclude `cancelled` and rename.
Chose the third. A cancelled job is an operator action, not a schedule fault, and a strip
that flags it teaches the operator to ignore the strip. Two same-named fields with
different definitions on two pages of one product is the exact defect class this repo
keeps finding, so the name carries the difference rather than a README sentence nobody
reads.

**8. `failing` is a separate current-state field, not folded into `failed_runs_24h`.**
Options: fold `last_error` into the 24h count; a separate field; omit it entirely. Chose
a separate field. Folding sums a job count and a schedule count into one integer, and
the two are counted in different units - `last_error` records only the latest failure, so
48 failures contribute one - which produces a number whose loss is invisible where it is
read. Omitting it would leave the most important failure mode, a schedule producing no
jobs at all, absent from the strip that exists to surface exactly that.

**9. `/stats` accepts no filters.** Options: mirror the list's filters; accept none.
Chose none. The strip's stated purpose is fleet-accurate counts; a filtered stats
response would be a second, differently-shaped answer to a question the list's `total`
already answers. README states that `stats.total` and the list's `total` agree only when
no filter is active, because a reader will otherwise assume they always do.

**10. `enabled` is a tri-state pointer to bool.** Options: treat `enabled=false` as
absent, as the sibling lane's `mine=false` is treated; a genuine tri-state. Chose
tri-state. The Holo design's chips are All / Enabled / Disabled, so "only paused" is a
first-class request, unlike "not mine" which is not a thing anyone asks for.

**11. `q` matches name, owner email and cron expression; `strpos`, not `ILIKE`.** Options
and reasoning are the sibling lane's, adopted verbatim so the two search boxes cannot
diverge. The third axis (cron) is this endpoint's addition and comes straight from the
backlog item.

**12. `worker_id` is a query parameter on the existing endpoint, not a new
`/v1/workers/{id}/reservations` route.** Options: a query parameter; a nested route;
client-side filtering. Chose the query parameter. A nested route would fork the
`page[reservationResponse]` envelope, the eight sort arms and the `total` contract into a
second place that can drift, for no gain - the same argument the sibling lane made
against a second jobs endpoint. Client-side filtering filters one page and mislabels
`total`.

**13. An unknown `worker_id` is an empty page, not a 404.** Options: 404 if no such
worker; empty page. Chose empty page. `worker_ids` has no foreign key, so a worker id
can legitimately outlive its row, and this endpoint cannot authoritatively distinguish
"never existed" from "deleted". Adding a workers lookup to find out couples two tables
to answer a question the caller did not ask.

**14. `worker_id`'s 400 does not echo the supplied value.** Options: echo it as
`handleCreateReservation` does; a fixed message. Chose fixed. Rendering nothing
input-derived is free here and is the project's standing preference. The create-path echo
is pre-existing and unchanged; it is proposed as a low-priority item rather than fixed in
a lane whose evidence should stay clean.

**15. Login and register share one `authResponse` struct.** Options: add the key to the
three existing `map[string]any` literals; introduce a shared struct. Chose the struct.
Three literals that must agree is the drift hazard the reuse of `toUserResponse` is
already there to avoid, and a typed response makes the key set pinnable by a test. The
wire bytes for `token` and `expires_at` are unchanged either way.

**16. `user` includes `archived_at`.** Options: reuse `toUserResponse` and accept the
sixth key; build a five-key literal matching the backlog item's list. Chose reuse. A
second builder is a parallel path that drifts from `/v1/users/me` the first time that
shape changes, which is precisely what the lane brief forbids. The value is always
`null` on these endpoints and costs five bytes.

**17. No migration and no new index.** Options: a GIN index on `reservations.worker_ids`;
a trigram index for `q`; none. Chose none. `reservations` is tens of rows and Postgres
would not choose an index on it; `pg_trgm` cannot serve `strpos` and would require an
extension dependency inside a transaction-wrapped migration; and `scheduled_jobs` is the
smallest paginated table in the product with no per-row aggregate. `000024` stays free.

**18. Client-side exposure of every new parameter and field is out of scope, as one
follow-up item.** The CLI, the MCP tools and the python SDK implement the same contract
and share a review.

## Escalations

Calls a human might reasonably make the other way.

1. **Drop `failing` from the stats response** (Decision 8). It is the one field no
   backlog item names, and a reviewer who wants the strip to have four tiles rather than
   five may prefer it deferred. I kept it because the alternative resolutions of the
   "does `last_error` count" question are either a lossy sum or leaving the silent
   failure mode off the surface built to surface it. Cutting it is cheap now and awkward
   after the frontend lane builds the strip.
2. **Name it `failed_24h` after all** (Decision 7), matching the Holo label and the
   dashboard field. That trades a documented cross-page inconsistency for a shorter name.
   I chose the divergent name because a same-named field with a different definition is
   the failure mode this repo keeps paying for.
3. **Include `cancelled` in `failed_runs_24h`** (Decision 7), if the strip is meant to
   read "runs that did not produce a result" rather than "runs that broke". Reversing
   this is a one-word SQL change now and a contract change after the frontend ships.
4. **Fail the request rather than degrade, or degrade rather than fail, on the
   enrichment lookup** (Decision 2). I chose to fail, which makes one more query able to
   500 the schedules list. A reviewer who weights list availability above signal honesty
   would degrade and add an explicit `last_job_status: "unknown"` sentinel instead - that
   is the third option, and it is defensible; I rejected it only because it adds a value
   to the vocabulary that no job can ever have.
5. **Fix `run-now` not moving `last_job_id` in this lane** (finding 6). It is a genuine
   surprise the new field makes visible, and the lane is already inside that handler. I
   kept it out because it is a behaviour change to the schedule row that deserves its own
   decision about whether an interactive fire should count as a "run" for `last_run_at`
   too - the SQL comments show that question was already contested once.
6. **Make `enabled=false` a synonym for absent** (Decision 10), for symmetry with the
   sibling lane's `mine`. I chose the tri-state because the design's chips ask for it.
7. **Add the `relay login` CLI integration test as a separate item** rather than in this
   lane. It is test-only and closes a real gap, but it does put a lane titled "schedules
   backend" into `internal/cli`.
8. **Unify the two lanes' `q` helpers before either merges**, by having one lane land the
   helper alone in a tiny prerequisite PR, rather than by a fan-in cleanup step. That
   removes the drift window entirely at the cost of serialising the two lanes.

## Backlog items proposed (conductor files; not auto-filed)

1. **bug** - `POST /v1/scheduled-jobs/{id}/run-now` creates a job carrying
   `scheduled_job_id` but never updates `last_job_id` or `last_run_at`, so the schedule's
   LAST JOB column keeps pointing at the previous scheduled fire after an interactive
   run. Newly visible now that the row renders a status for that job. Includes the open
   question of whether an interactive fire should stamp `last_run_at`.
2. **idea** - expose the new parameters and fields in the CLI (`relay schedules list`
   filters, a schedules stats line, `relay reservations list --worker`), the MCP tools,
   and the python SDK. One item covering both lanes' client exposure.
3. **bug, low** - `handleCreateReservation` echoes the caller's supplied `worker_id`
   verbatim into its 400 body. Admin-only and JSON-encoded, so low, but it is
   input-derived text in an error surface and the query-parameter sibling added by this
   lane deliberately does not do it.
4. **idea** - fold the schedules list's `fillOwnerEmails` lookup into the SQL join this
   lane adds for `q`, retiring one round trip per request. Deliberately not done here
   because it would change the sqlc row type and break the arity test's premise, which is
   a separate decision from adding a filter.
5. **idea, low** - `GET /v1/scheduled-jobs/stats`, `GET /v1/workers/stats` and
   `GET /v1/jobs/stats` are three near-identical handlers with three near-identical
   response shapes and no shared contract test. A single test asserting each returns only
   non-negative integers and only its documented keys would pin all three.

## Acceptance criteria

1. `scheduledJobResponse` carries `last_job_status` with the vocabulary and the
   present-exactly-when-`last_job_id`-is-present invariant, on both the list and the get,
   for both admin and owner scopes.
2. `TestScheduledJobResponse_ArityMatchesTheRow` is updated to `responseOnlyFields = 2`
   in the same commit that adds the field, and the commit message names
   `fillLastJobStatuses` as its supplier.
3. Zero schedules list SQL statements change their sqlc row type; `toScheduledJobResponse`
   and the four row-key functions are untouched by the `last_job_status` work.
4. An enrichment lookup failure returns 500; it never produces a response in which
   `last_job_id` is present and `last_job_status` is absent. Pinned by a test.
5. `GET /v1/scheduled-jobs/stats` exists, is auth-only, is owner-scoped for non-admins
   and fleet-wide for admins, and returns exactly the five documented keys with
   `total == enabled + paused`.
6. `failed_runs_24h` counts jobs with `scheduled_job_id IS NOT NULL`,
   `status = 'failed'`, `updated_at >= NOW() - INTERVAL '24 hours'`, scoped through
   `scheduled_jobs.owner_id`, and does **not** count `cancelled`. `failing` counts
   in-scope schedules with `last_error IS NOT NULL` and is not windowed. Each pinned by a
   test with a row belonging to no other bucket.
7. `GET /v1/scheduled-jobs` accepts `?enabled=` and `?q=` with the semantics in the
   contract, threaded through all 16 list statements and both counts, composing with
   `?sort=`, `?limit=` and `?cursor=`.
8. The cursor disjunction is parenthesised in the seven affected schedules statements and
   the seven affected reservations statements, and the arm-enumerating tests exercise
   every arm on the **first page with no cursor**.
9. `GET /v1/reservations` accepts `?worker_id=`, threaded through all eight list
   statements and the count, stays admin-only, and returns an empty page with `total: 0`
   for an id no reservation targets.
10. `total` equals the count of rows matching every active predicate on both endpoints,
    on every arm, independent of the cursor.
11. Every 400 in the error table is returned with that exact body, each pinned by a test.
12. A cursor issued under one set of filters, resent under another, returns no row that
    fails the current predicates, on both endpoints.
13. `POST /v1/auth/login` and both arms of `POST /v1/auth/register` return `user` built by
    `toUserResponse`, through one shared `authResponse` struct, with `token` and
    `expires_at` unchanged. No second user-response literal is introduced.
14. Existing schedules, reservations and auth tests pass with a zero-line test diff, with
    criterion 2 as the single named exception.
15. A new `internal/cli` live-server test drives `relay login` against the harness and
    passes; the existing `schedules list` live-server test stays green with a zero-line
    diff. No production change to `internal/cli`, `internal/mcp`, the python SDK or `web/`.
16. No migration is added; `000024` remains unused by this lane.
17. README updated in all seven places, including the corrected **Filter + sort**
    paragraph.
18. Zero files changed under `web/`.
19. `make generate` has been run and the CRLF procedure in CLAUDE.md followed: only
    content hunks kept, `git ls-files --eol` reads `i/lf` on every touched path, the
    diffstat matches the intended change size, and the regenerated `.sql.go` files are
    confirmed to still carry the content change after the revert.
20. `make test`, `make test-integration` (`internal/api`, `-timeout 1800s`),
    `make test-cli-integration` green, and `-race` run through the Linux container.
21. **Fan-in, conductor-owned:** whichever of lanes SB and JB merges second deletes its
    copy of the `q` validation and the at-most-once helper and calls the first lane's, and
    a test asserts the same malformed `q` returns a byte-identical 400 body from
    `GET /v1/jobs` and `GET /v1/scheduled-jobs`. Neither lane can satisfy this alone.
