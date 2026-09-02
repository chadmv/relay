# Lane JB: jobs list filters (q, mine, since, until) - backend contract

Date: 2026-09-02
Branch: `claude/web2-jb-jobs-filters`
Author: relay-tpm (autonomous gate mode; no human answered questions during this
flow, so every question in the Decisions section was decided here and the calls a
human might make the other way are listed under Escalations)

## Why this lane exists

Three deferred Jobs-page features - the search box, the "My jobs" toggle and the
Timeline view - are all blocked on the same thing: `GET /v1/jobs` has no way to
express "matching this text", "submitted by me", or "created in this window". A
client-side filter over one page is misleading under pagination, because it can
only see the 50 rows it happens to hold.

This lane ships the server half only. Nothing under `web/` changes. The deliverable
is the API contract below, which the later frontend lane implements against.

Backlog items in scope:

- `docs/backlog/idea-2026-06-05-job-search-box-q-filter.md`
- `docs/backlog/idea-2026-06-05-my-jobs-toggle-mine-filter.md`
- `docs/backlog/idea-2026-06-05-jobs-timeline-view.md` (backend query only; the
  view itself is a later frontend lane)
- `docs/backlog/idea-2026-05-06-list-endpoint-filters.md` (partially; see
  Recommendation for the generic item)

## What I verified against the tree, and what I refuted

A backlog proposal is not a contract. Each bullet below was checked against the
worktree before it was designed around.

1. **Refuted (partially).** The `mine` item says the predicate should be added
   "similar to how `status != 'revoked'` was threaded through the workers
   queries". That is not the same technique. `status != 'revoked'` is a
   **constant** predicate hand-repeated in every workers sort variant, with no
   parameter. There is no existing precedent in this repo for an **optional,
   caller-supplied** predicate threaded through a family of sort variants. The
   only `sqlc.narg` uses in the tree are `supports_workspaces` in `workers.sql`,
   which are `COALESCE` defaults on writes, not filters. So the technique this
   lane needs is new here, and that is exactly why the arm-enumerating guard test
   in the Testing section is required rather than optional.
2. **Refuted.** The Timeline item says "Being window-bounded, it needs no cursor
   pagination." A time window bounds nothing about cardinality. A 7 day window on
   a busy farm holds far more than `maxLimit` (200). The endpoint stays paged and
   the Timeline client walks pages; see Decision 7.
3. **Refuted (partially).** The Timeline item says it "Requires a new server
   endpoint or query parameter". No new endpoint is needed, and a second endpoint
   would be actively harmful: it would fork the `page[jobResponse]` envelope, the
   LATERAL task-progress enrichment and the `total` contract into a second place
   that can drift. Query parameters on the existing endpoint only.
4. **Confirmed, not refuted.** The search item says the semantics of combining
   `?q=` with `?sort=` and `?status=` "need to be defined when implemented". True,
   and Decision 3 defines them.
5. **Count correction.** The lane brief says there are nine `ListJobsWithEmailPage*`
   sort variants. There are ten statements whose names start with that prefix (the
   base `ListJobsWithEmailPage` plus nine suffixed ones), which is what
   `listJobsBySort`'s own comment means by "All 10 sort arms are covered". Nine is
   right only if the base statement is excluded. The number that governs the edit
   is 10 unfiltered list statements + 2 filtered list statements + 3 count
   statements = **15 statements touched, 0 added**.
6. **New defect found, not in any item.** `handleListJobs` checks
   `?scheduled_job_id=` first and returns from that branch without ever reading
   `?status=`. So `GET /v1/jobs?scheduled_job_id=X&status=running` **silently
   ignores the status filter** and answers with every job of that schedule.
   Nothing rejects the combination either: the sort-versus-filter guard treats the
   two as an OR. This is out of scope for this lane (it is a pre-existing bug in a
   branch this lane does not otherwise change) and is proposed as a backlog item.
   The four new predicates deliberately do **not** repeat the mistake: they are
   honored in every branch.
7. **Nothing refuted** in the generic list-endpoint-filters item's own claims. Its
   dependency note ("filters that change sort order need a different cursor
   scheme") is correct and does not bind this lane, because `q` here is a pure
   `WHERE` predicate with no relevance ranking and therefore does not change
   ordering. That note must travel into the re-filed remainder item, since ranked
   search is still unbuilt.

## API contract

This section is the deliverable. The frontend lane implements against it.

### New query parameters on `GET /v1/jobs`

| Param | Type | Format | Absent means |
|---|---|---|---|
| `q` | string | free text, case-insensitive substring | no text filter |
| `mine` | boolean | `true` / `false` (Go `strconv.ParseBool` spellings) | no owner filter |
| `since` | timestamp | RFC3339, offset or `Z` required, fractional seconds allowed | window open at the start |
| `until` | timestamp | RFC3339, offset or `Z` required, fractional seconds allowed | window open at the end |

**`q`** matches when the trimmed needle is a case-insensitive substring of either
the job's `name` or the owner's `email` (the same value the row already returns as
`submitted_by_email`). Matching is plain substring containment: `%` and `_` are
**literal characters**, not wildcards. A `q` that is empty or whitespace-only after
trimming is treated exactly as an absent `q`, matching how the handler already
treats `status=""`.

**`mine=true`** restricts to jobs whose `submitted_by` is the authenticated caller.
The user id comes from `UserFromCtx`, never from the wire; there is no parameter
that lets a caller ask for someone else's jobs (see Decision 2 and the Escalations).
`mine=false` is accepted and means the same as absent.

**`since` / `until`** bound `jobs.created_at` as a **half-open interval**:
`created_at >= since AND created_at < until`. Half-open so that consecutive
Timeline buckets tile without a job appearing in two of them. Either bound may be
given alone. `until == since` is a legal empty window.

### Errors

All errors use the existing `writeError` shape, `{"error": "<message>"}`, with
status 400. The messages are part of the contract and are pinned by tests:

| Condition | Body |
|---|---|
| `mine` not parseable as a bool | `invalid mine; expected true or false` |
| `since` not RFC3339 | `invalid since; expected an RFC3339 timestamp` |
| `until` not RFC3339 | `invalid until; expected an RFC3339 timestamp` |
| `until` earlier than `since` | `until is earlier than since` |
| `q` longer than 200 runes | `q is too long; maximum 200 characters` |
| `q` not valid UTF-8 | `q is not valid UTF-8` |
| any of the four repeated in the query string | `query parameter "<name>" must appear at most once` |

The UTF-8 check is not defensive decoration. Go's query parser percent-decodes
without validating UTF-8, so a `q` containing an invalid byte sequence would reach
Postgres as a text parameter; the handler must reject it before the query runs so
that user input cannot produce a 5xx. The test pins the 400, and must not assert
anything about what the database would otherwise have done.

The at-most-once rule follows the precedent set by `?task=` on the retry endpoint:
`Query().Get` silently returns the first of a repeated parameter, and a silently
wrong filter renders a list that looks authoritative.

### Composition rules

- `q`, `mine`, `since`, `until` **AND together**, and compose with `?limit=`,
  `?cursor=`, `?sort=`, `?status=` and `?scheduled_job_id=`. There is no
  combination of the four that is rejected except the `until < since` case above.
- The existing rule stands **unchanged**: `?sort=` combined with `?status=` or
  `?scheduled_job_id=` is still `400 sort not supported on filtered list variant;
  remove the filter or remove the sort`.
- The four new parameters are **not** part of that rule and must not be added to
  `hasFilter`.

Why the new predicates compose while the two old ones do not, since a reader will
otherwise conclude the rule is "filters exclude sort": the rule is not a semantic
statement about filtering. It is a guard over a gap in the SQL layer.
`ListJobsByStatusWithEmailPage` and `ListJobsByScheduledJobWithEmailPage` hard-code
`ORDER BY j.created_at DESC, j.id DESC` and a keyset comparison on
`(created_at, id)`; no per-sort filtered statement exists. Allowing
`?status=running&sort=name` would order rows by `created_at` while telling the
client they are ordered by name, and page two would be worse than wrong: the
handler passes `pp.CursorTs()` unconditionally, so a text-sorted cursor (which
carries `V`, not `T`) would arrive as a zero timestamp and the second page would
come back empty. The 400 is a guard against that, so it is scoped to exactly the
two parameters that have their own statements.

The four new predicates do not create that gap, because they are threaded into
**every** variant as optional arguments and never touch `ORDER BY`. That is the
whole reason for the sqlc strategy below.

### Cursor behaviour

Unchanged. Cursors stay opaque, stay tagged with the sort they were issued under,
and carry no record of the filters that were active. Consequences the frontend
lane must respect:

- **Reset the cursor whenever `q`, `mine`, `since` or `until` changes.** This is
  the same requirement that already applies to `status`, and it is not enforced by
  the server.
- Filter correctness is nevertheless cursor-independent. Because every predicate is
  applied in SQL alongside the keyset comparison, a stale cursor can produce a page
  that starts at a surprising position but can **never** return a row that fails
  the current predicates. That property is an acceptance criterion with its own
  test, so the unguarded case has a defined, safe failure mode.

A filter fingerprint inside the cursor is the honest fix and is proposed as a
backlog item, because the hole belongs to every list endpoint, not to this one.

### `total` semantics

Unchanged in meaning, and this is load-bearing: `total` is the server-side count of
**all rows matching every active predicate**, independent of the cursor. So the
three count statements gain the same four optional predicates as the list
statements. A `total` that ignored `q` would make the Jobs page footer read
"1 - 50 of 3812" while showing three search hits.

### Response body

No change. Same `page[jobResponse]` envelope, same enrichment fields, `items: []`
(never `null`) on an empty result.

## Design

### sqlc strategy: optional predicates threaded through the existing variants

Three options were considered.

**Option A (chosen): thread four optional predicates through the 15 existing
statements.** Every list statement gains one `WHERE` block of the shape

```
AND (sqlc.narg(q)::text IS NULL
     OR strpos(lower(j.name), lower(sqlc.narg(q)::text)) > 0
     OR strpos(lower(u.email), lower(sqlc.narg(q)::text)) > 0)
AND (sqlc.narg(owner_id)::uuid IS NULL OR j.submitted_by = sqlc.narg(owner_id)::uuid)
AND (sqlc.narg(since)::timestamptz IS NULL OR j.created_at >= sqlc.narg(since)::timestamptz)
AND (sqlc.narg(until)::timestamptz IS NULL OR j.created_at <  sqlc.narg(until)::timestamptz)
```

and the three count statements gain the same block. `CountJobs` and
`CountJobsByStatus` acquire the `JOIN users u ON u.id = j.submitted_by` that the
list statements already have; that join is total and cannot change the count,
because `jobs.submitted_by` is `NOT NULL REFERENCES users(id) ON DELETE RESTRICT`.
All three count statements are called from `handleListJobs` and nowhere else, so
their signatures are free to change.

Statement inventory: 10 unfiltered sort variants, 2 filtered variants
(`ByStatus`, `ByScheduledJob`), 3 counts. 15 edited, 0 added.

**Option B (rejected): new per-filter statements.** Adding filtered variants
multiplies against the sort dimension. Even the restrained version - one
`*Filtered` twin per existing statement - is 15 new statements, two ways to list
jobs, and a permanent drift hazard between the twin and the original (the LATERAL
aggregate and the enrichment column list would have to stay identical in 30
places).

**Option C (rejected): build the SQL string in Go.** Abandons sqlc's compile-time
column checking for the busiest read path in the product and reintroduces an
injection surface that the current design does not have.

Option A's real risk is not SQL, it is the Go side: each generated `Params` struct
gains four fields that must be set at 15 call sites (12 list, 3 count), and a
forgotten field is a zero value that silently disables a filter for exactly one arm
- or, on a count, leaves `total` counting a larger set than the page it labels.
Two mitigations, both required:

1. **One parse, one struct.** A single `parseJobFilters(w, r, u)` in
   `internal/api/jobs.go` is the only place that reads the four parameters,
   validates them, and produces the store-typed values. It is the sole producer of
   those values, in the same spirit as the single-JSON-entry-point invariant. Every
   call site spreads from that one struct; no call site parses anything itself.
2. **A behavioural guard that enumerates the arms** rather than asserting a
   structural property. See Testing.

### Why `strpos` and not `ILIKE`

`ILIKE '%' || q || '%'` makes user input a pattern: a user typing `%` matches every
job and a user typing `_` gets a single-character wildcard. Escaping fixes that, but
the escape then either lives in many copies of a nested `replace` (one miscopy is a
silent wildcard) or lives in Go, which makes the store parameter a raw LIKE pattern
that any future caller can get wrong invisibly.

`strpos(lower(col), lower(needle)) > 0` is exact substring containment with no
metacharacters and nothing to escape, and it reads identically in all 15 copies.

The cost of this choice, recorded so the next person does not discover it the hard
way: **a trigram (`pg_trgm`) index can never serve `strpos`.** If pg_trgm is ever
adopted, the predicate must be rewritten to escaped `ILIKE` at that time. That
rewrite is mechanical and fully covered by the arm-enumerating test battery, which
is why the loud future work was preferred over the silent present hazard.

`lower()` is Postgres's locale-dependent lowercasing, which is ASCII-correct and
therefore correct for email addresses and for the job names this farm produces.

### Performance, and what is out of scope

`q` is a sequential scan. There is no index that serves substring containment, and
one is not being added.

The item frames the cost as "ILIKE is a seq scan", which **understates it**. Each
of these statements carries a `LEFT JOIN LATERAL` that aggregates the job's tasks.
Today, with no filter, the planner can walk `idx_jobs_created_id` in order and stop
after `limit + 1` rows, so the aggregate runs a page's worth of times. With a `q`
predicate that only a few rows satisfy, the ordering index no longer lets it stop
early, and the per-row task aggregate may run across a large fraction of the table.
The dominant term is plausibly the aggregate, not the string comparison.

Decision: **acceptable at this project's scale**, with three conditions.

- Relay is a single-farm deployment; the jobs table is realistically thousands to
  low hundreds of thousands of rows, and this is one debounced search box, not a
  hot path. The frontend lane must debounce at 250 ms or more.
- `q` is length-capped at 200 runes, and the endpoint sits behind the existing
  per-IP rate limiter, so an authenticated caller cannot cheaply drive unbounded
  scan volume.
- **Measure rather than assume.** The implementation must run
  `EXPLAIN (ANALYZE, BUFFERS)` on one `q` query against a seeded database and
  record the plan **together with the seeded row count** in the PR description. A
  timing without its input reads as the typical case. If the plan shows the string
  comparison rather than the LATERAL dominating, say so; that changes which
  follow-up is worth filing first.

Out of scope, with reasons:

- **pg_trgm.** Adds an extension dependency inside a transaction-wrapped migration,
  only helps the `ILIKE` form this lane deliberately does not use, and does nothing
  about the LATERAL cost. Revisit when a measured plan says the string match
  dominates.
- **Restructuring the queries so the keyset and `LIMIT` run in a CTE over
  `jobs`/`users` alone, with the LATERAL applied to at most one page.** This is
  probably the right long-term shape and it would improve the unfiltered path too,
  but it rewrites the structure of 12 statements rather than appending a `WHERE`
  block, and the lane's risk budget is already spent. Proposed as a backlog item,
  to be decided from the measured plan.

### One index

New migration `000023_jobs_owner_created_index`:

```
CREATE INDEX idx_jobs_submitted_created_id ON jobs (submitted_by, created_at DESC, id DESC);
```

This serves `mine=true` under the default sort and its count. It is the same shape
as the existing `idx_sched_jobs_owner_created` on `scheduled_jobs`, so it follows a
precedent rather than inventing one. It does not help `mine` under a non-default
sort, and it does not help `q`; both are accepted.

Plain `CREATE INDEX`, never `CONCURRENTLY`: golang-migrate wraps each migration in
a transaction. The operational note in `000020_list_endpoint_indexes.up.sql`
applies - the build takes a SHARE lock on `jobs` for its duration. The down
migration drops the index.

`since`/`until` need no new index: `idx_jobs_created_id` already covers a
`created_at` range under the default ordering.

### Client compatibility

Every existing consumer builds its query string from a fixed set of known keys and
adds nothing: `internal/cli`'s `doListJobs` sets only `status` and `sort`;
`internal/mcp`'s `callListJobs` sets only `status`, `limit`, `cursor`, `sort`; the
python SDK's `_job_filters` sets only `status` and `scheduled_job_id`; `web/` is
untouched by this lane. The change is purely additive, so none of them can break.

That is an argument, not evidence, so it is written as an acceptance criterion with
a named test - and the honest state of the gates matters here. `internal/mcp`'s
`TestListJobs_PassesQueryParams` and the python unit tests drive `httptest` and
recorded fixtures; they pin **nothing** about the real server and cannot detect
this change either way. The CLI real-server lane has integration coverage for
`workers list`, `schedules list` and `admin users list`, and **none for
`jobs list`**. So today no lane pins CLI-to-server compatibility for this endpoint.
This lane closes that gap with one new test (see Testing).

Exposing the new parameters in the CLI, the MCP tool and the python SDK is **out of
scope**, and is proposed as a single follow-up item rather than three, because all
three implement the same contract and should be done together.

### README changes (required scope)

Three edits, and the third is the one most likely to be skipped:

1. The `### Jobs` table row for `GET /v1/jobs` currently reads "(`?status=` and
   `?scheduled_job_id=` filters optional)". It must name the four new parameters.
2. A new subsection under Pagination, beside "Configurable sort order",
   documenting `q`, `mine`, `since`, `until`: formats, the half-open window, the
   exact 400 bodies, that all four compose with everything, and that a client must
   drop the cursor when it changes a filter.
3. The existing **Filter + sort** paragraph says `GET /v1/jobs` rejects `?sort=`
   combined with `?status=` or `?scheduled_job_id=`. That sentence stays true but
   becomes misleading the moment four filters that *do* compose with sort exist. It
   must say explicitly that `q`, `mine`, `since` and `until` compose with `?sort=`.
   Wrong prose about correct code is this repo's dominant defect class; a reader
   who infers "filters exclude sort" from the unamended paragraph will write a
   frontend that never combines search with sorting.

`python/README.md` documents SDK method signatures rather than the endpoint's
parameter set, so it needs no change while the SDK does not expose the new
parameters. `web/` is unchanged.

## Testing

The lane is TDD, engineer-owned; the tests below are the properties the spec
requires, not prescribed bodies.

**The anti-drift guard.** A table-driven integration test in `internal/api` that,
for each list arm, seeds two jobs distinguishable on all four axes and asserts each
predicate returns exactly the one matching row with `total == 1`. The arm table
must be **derived from `JobsSortSpec.Keys`** (each key in both directions) plus the
`status` branch and the `scheduled_job_id` branch, so that a future sort key added
without its filter arms turns this test RED rather than shipping one silently
unfiltered ordering. This is the guard that stands in for the fact that Option A's
failure mode is a forgotten struct field, and it is behavioural: it asserts what
the endpoint returns, not what a struct declares.

Also required:

- One test per 400 in the table above, asserting the exact `error` string.
- `mine=true` is resolved from the token: two users submitting jobs to the same
  server get disjoint lists from the identical request.
- Half-open boundary: a job created exactly at `since` is included; a job created
  exactly at `until` is excluded.
- Cursor-independence of filter correctness: take a cursor under one filter, resend
  it with a different filter, assert the response contains no row failing the new
  predicate.
- `q` matches on owner email as well as job name, and treats `%` and `_` as
  literals.
- **Existing jobs list, sort, pagination and enrichment tests keep a zero-line
  diff.** If any of them needs editing, that is a behaviour change and must be
  justified in review, not absorbed.
- One new `internal/cli` integration test driving `doListJobs` against the live
  harness server, bare and with `--status`, asserting the rows decode and the total
  line renders. This is the only lane that can pin CLI-to-server compatibility for
  this endpoint, and it does not exist today.

Gates: `make test`, `make test-integration` for `internal/api` with
`-timeout 1800s`, `make test-cli-integration`, and `-race` through the Linux
container per CLAUDE.md. After `make generate`, follow the CRLF procedure: never
conclude "nothing to revert" from `git diff` alone, keep only content hunks, and
confirm `git ls-files --eol` reads `i/lf` on every touched path.

## Decisions

**1. `q` matches job name and owner email, not job id or labels.** Options: name
only; name plus email; name plus email plus id prefix plus labels. Chose name plus
email, which is exactly what the item asks for. The id has a dedicated detail route
and a uuid prefix search is a different feature with a different index story;
labels are a JSONB containment design that belongs with the deferred label filters.

**2. `mine=true`, not `?submitted_by=<id|email>`.** Options: ship `mine` only; ship
`submitted_by` only (the generic item's spelling); ship both. Chose `mine` only.
The server resolves the identity from the bearer token, so the answer cannot be
wrong, needs no `/v1/users/me` round trip, and stays correct when the SPA's cached
user is stale. `submitted_by` is a strictly larger feature (an admin viewing
another user's jobs) with its own authorization question, and shipping two
parameters that mean the same thing for the common case is the kind of redundancy
that gets one of them wrong. `submitted_by` moves to the re-filed remainder item.

**3. The new predicates compose with `?sort=`; the existing exclusion is
unchanged.** Options: treat them as filters and extend the 400 to cover them; treat
them as orthogonal and compose. Chose compose. The exclusion exists because two
filters live in statements that hard-code one `ORDER BY`, which is a gap in the SQL
layer rather than a semantic rule; threading the new predicates through every
variant means they never create that gap. Extending the 400 would have made the
search box and the sort headers mutually exclusive on the Jobs page for no reason.

**4. Optional predicates threaded through existing statements.** See the sqlc
strategy section for the three options and the reasoning. 15 edited, 0 added.

**5. `strpos` rather than escaped `ILIKE`.** Options: `ILIKE` with escaping
repeated in SQL; `ILIKE` with a single Go-side escaper; `strpos`. Chose `strpos`.
Both `ILIKE` routes fail silently when someone gets them wrong (an unescaped `_`
becomes a wildcard nobody notices); the `strpos` route fails loudly and later (a
future pg_trgm adopter must rewrite the predicate, with a test battery watching).
Silent-and-now loses to loud-and-later.

**6. Time window is on `created_at`, half-open `[since, until)`.** Options:
`created_at`; `updated_at`; the derived `started_at`/`finished_at`. Chose
`created_at`: it is submission time, it is immutable, and it is already indexed for
the keyset. `updated_at` has two writers and is stamped on every task-level event,
so rows would drift in and out of a fixed window while nothing about them changed.
`started_at`/`finished_at` come from the LATERAL aggregate over tasks and are not
predicable without restructuring the query. Half-open so that consecutive Timeline
buckets tile without double-counting a boundary job. `until < since` is a 400;
`until == since` is a legal empty window.

**7. Timeline stays paged; no cap, no unbounded mode.** Options: keep the endpoint
paged and let the client walk; add a "return the whole window" mode; cap the window
size. Chose paged. A cap would silently truncate a timeline, and a chart that draws
a lie is worse than a chart that says "narrow the window". The client can read
`total` before deciding to walk, which is a better bound than anything the server
could impose blindly. The frontend lane should bound its own walk and offer a
"narrow the window" affordance when `total` is large; that is a frontend decision,
not a server behaviour.

**8. Validation is strict: exact 400s, at-most-once, UTF-8 checked.** Options:
lenient parsing in the style of `?force=`, where an unparseable value falls back to
false; strict in the style of `?task=`. Chose strict. `?force=`'s leniency is safe
because a misread fails toward the gentler action; here a misread renders a list
that is wrong and looks authoritative, which is the same reasoning the retry
endpoint used to reject leniency. The UTF-8 check specifically keeps user input
from being able to produce a 5xx.

**9. `total` reflects every active predicate.** Options: keep `total` as the
unfiltered job count and let the client interpret; filter it. Chose filtered. The
README already promises "server-side count of all matching rows", so the
alternative would make the documented contract false, and the page footer would
show a range against a denominator from a different set.

**10. Empty or whitespace-only `q` is treated as absent, not as an error.**
Options: 400; treat as absent. Chose absent, matching how `status=""` is already
treated, and because a search box that has just been cleared will send it.

**11. One index for `mine`, none for `q`.** Options: no index; the owner
composite; owner composite plus trigram indexes. Chose the owner composite alone:
it follows the existing `scheduled_jobs` owner precedent, is cheap, and serves the
toggle's common case. Trigram indexing is deferred behind a measurement.

**12. The pre-existing `scheduled_job_id` ignores `status` defect is not fixed
here.** Options: fix by composing; fix by rejecting the combination with a 400;
leave and file. Chose leave and file. It is a behaviour change in a branch this
lane does not otherwise touch, either fix needs its own decision about which
semantic is right, and bundling it would blur what this lane's tests are proving.

**13. Client-side exposure is out of scope, as one follow-up item.** The CLI, MCP
and python SDK all implement the same contract and share a review; three separate
items would be three partial views of one job.

## Recommendation for `idea-2026-05-06-list-endpoint-filters`

**Close it at batch fan-in, not in this lane, and re-file the remainder as one
narrower item.**

Reasoning. The item is a menu of six proposals across four endpoints. An item like
that can never be "done": it will be partially satisfied forever, and a future
reader either re-implements a slice that already shipped or treats the whole menu
as pending. It is a theme, not an item.

This batch ships two of its slices - the jobs time-range filters here, and the
schedules `?enabled=` filter in the schedules backend lane. Neither lane can close
the item alone without the resolution being wrong about the other. So:

- **This lane does not close it.** The conductor closes it at fan-in, once both
  lanes have merged, with `/backlog close` so the file is `git mv`d into
  `docs/backlog/closed/` with the `status`/`closed`/`resolution` frontmatter and a
  `## Resolution` note. A hand-edited `status` field would leave it in the open
  directory and `/backlog list` would report it malformed.
- The resolution names precisely what shipped: jobs `?since=`/`?until=` and
  schedules `?enabled=`, plus jobs `?q=` and `?mine=` which the item did not
  itself propose in that form.
- **Re-file the remainder as one item**, carrying over: jobs `?submitted_by=`,
  jobs multi-value `?status=a,b` (today `?status=` is single-valued and exact),
  workers multi-value `?status=`, users `?q=`, and label containment filters for
  jobs and workers (which need a JSONB containment design and a GIN index, and are
  the largest piece by far). The item's dependency note about ranked search needing
  a different cursor scheme travels with it, since ranked search is still unbuilt.

The alternative - leave it open with a note - was rejected because a note inside a
menu item is exactly what nobody reads before re-proposing a shipped slice.

## Backlog items proposed (conductor files; not auto-filed)

1. **bug** - `GET /v1/jobs?scheduled_job_id=X&status=Y` silently ignores `status`;
   the scheduled branch returns before the status branch is reached and nothing
   rejects the combination.
2. **idea** - thread `status` and `scheduled_job_id` through the sort variants as
   optional predicates too, and retire the sort-versus-filter 400. This lane proves
   the technique; doing it would delete a documented error message, which is a
   deliberate contract change and deserves its own decision.
3. **idea** - expose `q`, `mine`, `since`, `until` in the CLI (`relay jobs list`),
   the `relay_list_jobs` MCP tool, and the python SDK's `list_jobs` /
   `list_jobs_page`.
4. **idea** - cursors carry no filter fingerprint, so changing a filter mid-walk is
   unguarded on every paginated endpoint, not just this one. Proposal: a filter
   fingerprint field in the cursor wire, absent meaning "no filters", with a 400 on
   mismatch.
5. **idea** - bound the jobs-list `LEFT JOIN LATERAL` task aggregate to one page by
   moving the filter, keyset and `LIMIT` into a CTE over `jobs`/`users`. Decide from
   the EXPLAIN plan this lane records, not from the argument.
6. **idea** - the re-filed remainder of the generic list-endpoint-filters item; see
   the Recommendation section. Filed at batch fan-in, together with the close.
7. **bug, low** - a query parameter carrying an invalid UTF-8 byte sequence reaches
   Postgres as a text parameter on other list endpoints too (`status` on this
   endpoint, and the equivalents elsewhere). This lane guards only `q`.

## Acceptance criteria

1. `GET /v1/jobs` accepts `q`, `mine`, `since`, `until` with the formats, defaults
   and semantics in the API contract section.
2. All four compose with each other, with `?limit=`, `?cursor=`, `?sort=`,
   `?status=` and `?scheduled_job_id=`. The existing sort-versus-filter 400 is
   unchanged and the new parameters are not added to `hasFilter`.
3. The arm-enumerating integration test passes, with its arm table derived from
   `JobsSortSpec.Keys` plus the two filtered branches, covering each of the four
   predicates on every arm.
4. `total` equals the count of rows matching every active predicate, on every
   branch, independent of the cursor.
5. Every 400 in the error table is returned with that exact body, each pinned by a
   test.
6. `mine=true` resolves the owner from `UserFromCtx`; no query parameter can select
   another user's jobs. Pinned by a two-user test.
7. A cursor issued under one set of filters, resent under another, returns no row
   that fails the current predicates.
8. The existing jobs list, sort, pagination and enrichment tests pass with a
   zero-line test diff.
9. A new `internal/cli` integration test drives `doListJobs` against the live
   harness server, bare and with `--status`, and passes. No production change to
   `internal/cli`, `internal/mcp`, the python SDK or `web/`.
10. Migration `000023` adds `idx_jobs_submitted_created_id` with a matching down
    migration, plain `CREATE INDEX`, and the integration lane runs it.
11. `make generate` has been run and the CRLF procedure in CLAUDE.md followed: only
    content hunks kept, `git ls-files --eol` reads `i/lf` on every touched path,
    and the diffstat matches the intended change size.
12. An `EXPLAIN (ANALYZE, BUFFERS)` plan for one `q` query is recorded in the PR
    description **with the seeded row count** it was measured against.
13. README updated in all three places, including the corrected **Filter + sort**
    paragraph.
14. Zero files changed under `web/`.
15. `make test`, `make test-integration` (`internal/api`, `-timeout 1800s`),
    `make test-cli-integration` green, and `-race` run through the Linux container.

## Escalations

Calls a human might reasonably make the other way.

1. **Ship `?submitted_by=` instead of, or alongside, `mine=true`** (Decision 2).
   The generic backlog item asks for `submitted_by`, and an admin viewing another
   user's jobs is a plausible near-term need. I chose `mine` because it cannot be
   wrong and needs no round trip, and because two parameters for one common case
   invites drift. Reversing this is cheap now and expensive after the frontend lane
   builds against `mine`.
2. **Escaped `ILIKE` instead of `strpos`** (Decision 5), if pg_trgm is considered
   near-term rather than hypothetical. That trades a present silent-wildcard hazard
   for a future mechanical rewrite avoided.
3. **Fix the `scheduled_job_id` ignores `status` defect in this lane**
   (Decision 12). It is a genuine silent-wrong-data bug and the lane is already
   inside that handler. I kept it out to keep the lane's evidence clean, but a
   reviewer who dislikes shipping a known silent-ignore may prefer it fixed now.
4. **Add the CLI jobs-list integration test as a separate item** rather than in
   this lane (Acceptance criterion 9). It is test-only and closes a real gap, but
   it does put a lane titled "backend filters" into `internal/cli`.
5. **A maximum window size or a default window for `since`/`until`**
   (Decision 7). I chose no cap so the Timeline can never draw a truncated
   window silently. A human worried about a client walking hundreds of pages might
   prefer a server-side ceiling with an explicit error over a client-side bound.
6. **Retire the sort-versus-filter 400 in this lane** (proposed item 2) rather
   than after it. Doing it here would leave the endpoint with one uniform rule
   instead of two classes of filter, which is easier to document, at the cost of a
   larger diff and a contract change inside a lane other lanes are waiting on.
