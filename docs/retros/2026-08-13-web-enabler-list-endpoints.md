---
date: 2026-08-13
topic: web-enabler-list-endpoints
branch: claude/pr-merge-session-f5796e
range: origin/main..HEAD (green, not yet merged)
---

# Session Retro: 2026-08-13 - Two list endpoints, and a finding about the slice that has not been written

**TL;DR:** Shipped `GET /v1/invites` (admin, four derived states, unfiltered) and
`GET /v1/auth/tokens` (self-service, own live rows only, current-session flag), plus migration
000020's three indexes and the README reference. 13 tasks, one backend engineer, strictly
sequential. The parent backlog item was **split**: `POST /v1/jobs/{id}/retry` was scoped out because
it needs its own SQL statement, its own `retry_count` and `cancelled`-status rulings, and it ties to
a second item. Four review lanes ran and **no high or medium landed against the shipped
endpoints**. The one real finding was about the **next** slice: `DeleteToken` had no `user_id`
predicate, and this PR hands every user their own session UUIDs, so the obvious next implementation
of per-session revoke would have been an IDOR. Three separate claims were corrected by measurement,
each by a different party, and one of them corrected the conductor. This is the **first backend
slice in a four-item batch that was otherwise all frontend**, and what that changed about the
process is recorded below rather than left implicit.

## The split, and why it was safe

The item (`feature-2026-06-26-web-enabler-backend-endpoints.md`) carried three endpoints for
scheduling convenience, and its own Notes sanctioned the outcome in advance: "split later if one
grows". The retry endpoint grew. Four reasons, three of which were already written down somewhere in
the tree before this cycle started:

1. **It is a fenced multi-row write on `tasks`.** Neither shipped endpoint writes anything. Bundling
   the repo's highest-risk change with two `SELECT`s would put both behind one review.
2. **It must not call `IncrementTaskRetryCount`.** That statement now fences on `assignment_epoch`,
   `worker_id` and `status IN ('pending','dispatched','running')`
   (`internal/store/query/tasks.sql:141-145`), which are the exact inverse of an operator re-run's
   preconditions. The query comment says so itself at `:126-133`, by name, and points at the backlog
   item.
3. **It has two unresolved semantic decisions** - what happens to `retry_count`, and what happens to
   a `cancelled` job's status, because `RecomputeJobStatus` counts anything not in
   `('done','failed','timed_out')` as unfinished (`internal/store/query/jobs.sql:97-101`) and
   therefore pulls a cancelled job to `running`.
4. **Its Acceptance ties it to a second item**, `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`.

**The split was safe only because the constraint block survived it.** Everything the parent item had
accumulated over seven weeks is now carried verbatim into
`docs/backlog/feature-2026-08-13-job-retry-endpoint.md`, with a fifth constraint the parent never
recorded, lifted from `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md:895-896`:
**it must not reopen a task whose dependents already ran**, or it reproduces by design the exact bug
that spec closed by accident. A split that dropped that block would have been the most expensive
thing this cycle could have done.

## What the spec found that the item did not

Two improvements, and the second is the more interesting one because it changes the class of the
control rather than its wording.

**1. `api_tokens.expires_at` is nullable and NULL means never expires.** The column has no
`NOT NULL` (`internal/store/migrations/000001_initial.up.sql:18`), and `BearerAuth` rejects only on
`row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now())`
(`internal/api/middleware.go:32-35`), so a NULL-expiry token authenticates forever. A bare
`expires_at > NOW()` on the sessions list would have hidden **precisely the most powerful
credentials in the system** from the one screen a user visits to find them. The item never mentions
it. The shipped predicate is `(expires_at IS NULL OR expires_at > NOW())` in all three statements
including the count (`internal/store/query/tokens.sql:77,97,110`).

**2. The current-session flag became a compile-time control instead of a review-time one.** The item
proposed deriving it "by comparing `tokenhash.Hash` of the caller's own bearer token". That works,
and it requires the query to select `token_hash` so there is something to compare against - putting
the stored hash inside a list handler, one forgotten map key away from the wire. The spec replaced
it with `row.ID == authUser.TokenID`, using the id `BearerAuth` already resolved
(`internal/api/middleware.go:36-42`). The query therefore never selects `token_hash`, the generated
row type has no field for it, and **returning a hash is a compile error rather than a review miss**
(`internal/api/tokens.go:51-61`, `internal/store/query/tokens.sql:74`). Same move on invites
(`internal/store/query/invites.sql`, explicit projection).

That is the transferable shape: when a security control can be moved from "the reviewer must
notice" to "the compiler must permit", the move is worth a design change even when the original
proposal was correct.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-13-web-enabler-list-endpoints.md`, **plan**
  `docs/superpowers/plans/2026-08-13-web-enabler-list-endpoints.md` (13 tasks, one
  `relay-backend-engineer`, strictly sequential - Tasks 2, 5, 7 and 9 each run `sqlc generate`,
  which rewrites every file in `internal/store/`, and Tasks 3 and 7 both edit
  `internal/api/server.go`).
- **`GET /v1/invites`**, `auth(admin(...))` at `internal/api/server.go:143`. Unfiltered: every
  invite in every state, because redeemed and expired invites are what the tab exists to show. Four
  sort arms (`created_at` and `expires_at`, both directions) at `internal/api/invites.go:148-240`,
  `InvitesSortSpec` at `:97-102`. No server-side `status` field; the client derives all four pills
  from `expires_at` and `used_at`, mirroring `web/src/admin/enrollments/enrollmentStatus.ts`.
  `created_by_email` via an inner `JOIN users`, safe because users are archived and never
  hard-deleted.
- **`GET /v1/auth/tokens`**, `auth(...)` and deliberately **not** `admin`, at
  `internal/api/server.go:103`, in a new `internal/api/tokens.go`. Two sort arms. Rows scoped to
  `authUser.ID` from the context; there is no `user_id` parameter and a test asserts one is ignored.
- **New store queries.** `internal/store/query/invites.sql` gained four page queries plus
  `CountInvites`; `internal/store/query/tokens.sql` gained `ListActiveTokensForUserPage`,
  `ListActiveTokensForUserPageByCreatedAsc` and `CountActiveTokensForUser` (`:46-110`). Every
  projection enumerates columns; no `SELECT *`.
- **Migration 000020**, three indexes: `idx_invites_created_id`, `idx_invites_expires_id`,
  `idx_api_tokens_user_created_id`. DESC-only variants; plain `CREATE INDEX`, never `CONCURRENTLY`,
  because golang-migrate wraps each migration in a transaction. The third also removes an existing
  wart: `api_tokens` has had no `user_id` index since `000018_hot_path_indexes.up.sql:25` dropped
  the redundant `token_hash` one, so `DeleteTokensForUser` and `DeleteOtherTokensForUser` - the
  latter on the password-change path - sequential-scanned the table until now.
- **`DeleteToken` gained a `user_id` predicate** (`internal/store/query/tokens.sql:38`) with the
  reasoning recorded at the statement (`:22-37`) and a dedicated store test
  (`internal/store/tokens_delete_scope_integration_test.go`). See Problems 4.
- **Tests.** Unit, no build tag, running under `go test ./...`:
  `internal/api/invites_response_test.go`, `internal/api/tokens_response_test.go`, and
  `internal/api/list_endpoint_projection_test.go` (the reflect gate over the generated row types).
  Integration, `//go:build integration`: `invites_list_integration_test.go`,
  `invites_sort_integration_test.go`, `tokens_list_integration_test.go`,
  `tokens_sort_integration_test.go`, plus `internal/store/list_endpoint_indexes_integration_test.go`
  and the delete-scope test.
- **README** updated: the sort allowlist table at `:1096-1097`, the session endpoints table at
  `:1154`, the "no per-session revoke endpoint" note at `:1174-1176`, and the invites section.
- **Zero files under `web/`.** The `web/dist` revert rule did not apply this iteration and the plan
  said so up front.

## The first backend slice after three frontend ones, and what changed

Worth naming, because four of the last five iterations were frontend and the reflexes had drifted.

- **The green gate moved and got slower by two orders of magnitude.** The frontend loop is Vitest in
  seconds; here it is Docker plus testcontainers at roughly 104s for the store package and 414s for
  api. That cost is what makes "run it again with `-count=1`" a decision rather than a reflex, and
  it is why three parties re-running it independently is worth recording as evidence rather than
  assumed.
- **`go test ./...` on Windows exercises none of the endpoint behaviour.** Every behavioural test
  here is `//go:build integration`. The three unit files are real but cover only the pure mapping
  helpers and the projection gate. A green `make test` on this machine is a no-regression signal and
  **is never evidence that either endpoint works**. The plan stated this in its Conventions block,
  which is the only reason nobody had to rediscover it.
- **The rule set swapped.** `git checkout -- web/dist/` was dead weight; the sqlc CRLF discipline was
  live and load-bearing. Each lane has a rule that is inert in the other. The transferable practice
  is what the plan did: declare which rules apply **before** the first task, so the absent one is a
  recorded non-applicability rather than a silent omission.
- **There is no suite-count metric.** Three frontend retros in a row could open with "959 -> 973".
  Go has no equivalent single number, so progress has to be argued from the file set and the named
  behaviours instead. That is worse for a skim and better for a reader.
- **No parallelism was available at all.** `sqlc generate` rewrites every file in
  `internal/store/`, so two engineers in one worktree would interleave line-ending churn and lose
  each other's content. The frontend batches had been getting used to two-slice fan-out; this one
  could not have it, and the plan's independence declaration said so rather than leaving the
  conductor to discover it.
- **The discipline transferred intact.** Every habit built up over the frontend arc showed up here
  in backend clothing: staged behavioural RED, key-set equality instead of per-key assertions,
  absence assertions paired with positive controls, mutation proofs that leave a test behind, and
  re-running the implementer's own proofs. None of it needed re-teaching. That is the useful
  datapoint: the lessons were about evidence, not about React.

## Key Decisions

- **The invites list applies no filter and takes no filter parameter.** Unlike enrollments, where a
  consumed row simply vanishes, redeemed and expired invites are the point of the tab. Consequence:
  `total` is the unfiltered count, so the footer cannot state a number the client cannot page to,
  and the sort+filter 400 rule at `internal/api/jobs.go:417-422` does not apply here. It becomes
  live the moment someone adds `?status=`, and that is recorded rather than pre-built.
- **The sessions list is filtered to live rows and the count uses the identical predicate.** An
  expired token cannot authenticate, nothing reaps them, and there is no per-row action a user could
  take on one. Listing them would render rows with no available action.
- **Neither endpoint returns a `status` field.** The house rule is written down at
  `web/src/admin/enrollments/enrollmentStatus.ts`: a server-asserted `expired` goes stale the
  instant the row is on screen, and `expiring` needs an invented threshold. The server ships facts.
- **Optional keys are omitted, never nulled.** An absent `email` means the invite is not
  address-bound; an absent `used_at` means unredeemed; an absent `expires_at` on a session means
  **never expires**, which the consuming tab must render as `never` and not as the `-` placeholder.
  A non-expiring credential is a security fact, not missing data.
- **Every allowlisted sort key gets an arm in both directions.** `parseSort` strips the leading `-`
  before the allowlist check (`internal/api/pagination.go:178-181`), so both directions of every key
  reach the dispatch switch, whose `default` is a `panic`. A key with one arm is a
  client-triggerable 500 plus a dropped connection. The invites test is driven off
  `api.InvitesSortSpec.Keys` rather than a literal list, so a future added key without an arm turns
  it red without anyone editing the test.
- **`handleListTokens` went in a new file.** One file per resource is the house layout;
  `auth.go` already carries five handlers. `auth.go` is not otherwise refactored.
- **`is_current` is always present, never omitted.** "This row is not your current session" is a
  positive fact the UI must be able to state.

## Problems Encountered

1. **The NULL-expiry predicate got a deliberately staged RED, and the correctness lane found the
   staging was one step short.** The plan's Task 9 lands the endpoint without the expiry filter,
   writes both tests, applies the **naive** `expires_at > NOW()` on purpose to capture the RED, then
   corrects to `(expires_at IS NULL OR expires_at > NOW())` in all three statements. The naive state
   was never committed. The engineer reported the mutation as covering all three statements.

   The correctness lane re-ran it and **went further than the engineer**: `require.NotNil` aborts
   the test before the `total` assertion is reached, so mutating all three statements at once proved
   nothing about the count - the test died upstream of it. Mutating **only** the count statement
   produced its own independent RED (`total must count the never-expiring row too`). Two mutations,
   not one, and the second is the only evidence that the count predicate is covered.

   This is the previous iteration's "a wide RED is not a strong RED" lesson from the other side: a
   mutation that reddens is not automatically a mutation that **discriminates the thing you
   claimed**, and a `require` upstream of the assertion you care about silently converts a coverage
   proof into a liveness proof.

2. **Three claims were corrected by measurement, and each correction came from a different party.**
   Recorded together because the pattern matters more than any one of them.
   - **The correctness lane refuted the engineer.** The engineer claimed one projection mutation
     fired both leak gates. It did not: changing the projection alone left the behavioural sweep
     green, and only actually emitting the field into the response fired it. The two gates
     (the reflect gate over the generated row types in
     `internal/api/list_endpoint_projection_test.go`, and the raw-body substring sweep) catch
     different mistakes, and a single mutation does not prove both.
   - **The integration lane caught its own fixture error.** Its first attempt seeded 20,000 tokens
     under a **single** user, which makes `user_id = $1` match the whole table, so Postgres
     seq-scans regardless of the index and the migration's planner claim looked false. Reseeded at
     1000 users by 20 tokens, both index claims held. A performance fixture whose selectivity does
     not resemble production measures the fixture, not the index.
   - **The engineer corrected the conductor.** The conductor's brief relayed a lens's claim that
     plain `CREATE INDEX` takes `ACCESS EXCLUSIVE`. The engineer measured it via `pg_locks` on
     PG16: it takes `ShareLock`, so it blocks writes (logins, which insert into `api_tokens`) and
     **not** reads. The conductor had relayed a claim without verifying it, and the correction
     changes the operational statement from "an outage" to "logins stall for the duration of the
     build".

   Three for three, in three directions, including upward. The standing rule that a subagent's
   claims are verified against the tree earns its keep only if it also runs against the conductor's
   own briefs.

3. **A README typo the plan instructed Phase 6 to file does not exist.** The plan's Phase 6 section
   names a `\v1\users\me\password` backslash typo. Two lanes confirmed it is not at HEAD, and this
   pass confirmed it independently: `README.md:1153` and `:1175` both read `/v1/users/me/password`
   with forward slashes. It was a plan-authoring artifact. **Not filed.** This is the second
   consecutive iteration in which a plan document asserted something about the tree that was true
   only inside the plan (the previous one was the stale golden-copy appendix), and the two failures
   have the same root: a plan is written before the code and is not re-read against the tree
   afterwards.

4. **`DeleteToken` was unscoped, and the finding's subject is the slice that has not been written
   yet.** This is the only real finding of the cycle and it deserves its own reading.

   The statement was `DELETE FROM api_tokens WHERE id = $1`, sitting between `DeleteTokensForUser`
   and `DeleteOtherTokensForUser`, **both** of which carry a `user_id` predicate. It was not
   exploitable at HEAD: the single call site passes `authUser.TokenID`, the id `BearerAuth` resolved
   from the presented credential (`internal/api/auth.go`), so no attacker-supplied id reaches it.

   What changed is the surrounding context. **This PR hands every authenticated user the UUIDs of
   their own sessions**, and `README.md:1174` says there is no per-session revoke endpoint - which
   is exactly the endpoint the list exists to enable, and which would take its id from the path. At
   that moment an id-only `DELETE` becomes an IDOR that force-logs-out any user, admins included.
   The next implementer would have reached for the statement that already exists, seen the two
   siblings' predicate as boilerplate, and shipped it.

   Fixed here, with a test that goes RED against the unscoped statement
   (`internal/store/tokens_delete_scope_integration_test.go`) and the reasoning written at the
   statement itself (`internal/store/query/tokens.sql:22-37`) in the non-task form of the epoch-fence
   rule: **the id proves which row, never whose.** That sentence is the same distinction the
   Invariants block draws on tasks, restated for a table that has no epoch.

   **Record the shape.** A review lane whose brief is "the diff" found a defect whose exploitability
   depends on a diff that does not exist yet, and the trigger for it was a property of **this**
   diff: it published identifiers that were previously server-only. That is a question worth asking
   on every slice - *what does this change make it newly reasonable for the next author to build,
   and does the existing machinery fail safe under that use?*

5. **Two pre-existing issues were found by two lanes independently and deliberately left
   unfixed.** Both are filed rather than patched, because both are wider than this slice.
   - **A cursor carrying a value of the wrong kind decodes to a zero timestamp.**
     `decodeCursor` requires exactly one of `T`/`V`/`N` (`internal/api/pagination.go:109-124`), and
     `parsePage` checks that the cursor's `S` matches the resolved sort
     (`:272-283`) - but nothing checks the value's **kind** against `pp.SortKind`, which is parsed
     at `:265` and then read by no production code at all (the only other reference in the tree is
     an assertion in `pagination_test.go:359`). A hand-crafted cursor with `S:"-created_at"` and a
     text `V` therefore passes both gates, leaves `cursor.T` at its zero value, and `CursorTs()`
     returns `{Time: 0001-01-01, Valid: true}`. The keyset comparison then excludes every row and
     the caller gets an empty page **with a non-zero `total`**, instead of the 400 the same input
     would earn under any other malformation. Affects every paginated endpoint identically.
   - **Two clocks.** The sessions list filters on Postgres `NOW()`
     (`internal/store/query/tokens.sql:77,97,110`) while `BearerAuth` filters on Go `time.Now()`
     (`internal/api/middleware.go:32`). Under skew between the app host and the database, a
     caller's own token can authenticate and be excluded from their own session list, or be listed
     and 401 on the next request. Narrow and low-severity, and it is a property of the pair, not of
     either statement.

## Findings Triage

- **0 high, 0 medium against the shipped endpoints. 1 real finding, about the next slice
  (Problem 4). 2 pre-existing issues, filed. 1 plan artifact, rejected.**
- **Four lanes ran and everything held under independent re-testing**: identity scoping (the
  sessions list returns only the caller's rows and ignores a `user_id` parameter), sort dispatch
  (every allowlisted key in both directions, with the order asserted per arm rather than just a
  200), keyset pagination (cursor walk with no duplicate and no omitted id), `total` parity with the
  list predicate, and the two leak gates.
- **The generated `.sql.go` files matched their sources byte for byte including the comment
  blocks.** The CRLF drift that shipped two iterations ago - a revert that silently discarded a
  regenerated file and left a doc comment contradicting its own SQL - **did not recur**. The plan
  carried the countermeasure explicitly (re-open each regenerated file after the cleanup and confirm
  the new functions and doc comments by eye, never `git checkout -- internal/store/` as a
  directory), and it worked. That is a standing lesson converting into a non-event, which is the
  outcome the lesson was written for.
- **The one review claim that was wrong was the conductor's own**, relayed from a lens without
  verification (Problem 2, third bullet). Worth stating plainly in a document that otherwise reads
  as a clean cycle.
- **No existing test file required an edit.** The plan made that a hard gate. It held.

## Deferred Findings

Filed this pass (five items, each **proposed** for human review rather than treated as accepted):

1. `feature-2026-08-13-job-retry-endpoint.md` (**feature/medium**) - the split-out endpoint,
   carrying the parent item's entire accumulated constraint block plus the dependents constraint
   from `2026-08-12-retry-resurrect-status-guard.md:895-896`. This is the important one: the split
   was only safe because this item exists and is complete.
2. `bug-2026-08-13-cursor-value-kind-not-validated.md` (**bug/medium**) - Problem 5, first bullet.
   Filed at medium rather than low because it affects every paginated endpoint identically, the
   observable is a silently wrong answer rather than an error, and `pageParams.SortKind` already
   holds the value the check needs.
3. `bug-2026-08-13-token-expiry-two-clocks.md` (**bug/low**) - Problem 5, second bullet.
4. `idea-2026-08-13-reap-expired-invites-and-tokens.md` (**idea/low**) - neither `invites` nor
   `api_tokens` is ever reaped, while `agent_enrollments` has an hourly janitor
   (`cmd/relay-server/main.go:245-253`). Both tables grow monotonically. Proposed as an extension of
   the existing janitor goroutine, not a second timer.
5. `idea-2026-08-13-no-store-on-json-responses.md` (**idea/low**) - `writeJSON`
   (`internal/api/server.go:186-190`) sets no `Cache-Control`; the only such header in the whole API
   is the SSE `no-cache` at `internal/api/events.go:56`. Pre-existing and global, one line at the
   single JSON exit point. Filed with its own rejection case stated, because the argued harm is thin.

Reciprocal `[[wiki-links]]` were added to
`feature-2026-07-01-job-retry-action`, `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`,
`bug-2026-08-09-task-list-ordering-has-no-tiebreaker`, and the parent enabler item. The parent was
amended to record which two endpoints shipped and that the third moved; **its `status` was not
touched and no Resolution was written**, because closing it is the conductor's step.

Considered and **not** filed, with reasons:

- **The README `\v1\users\me\password` typo the plan instructed Phase 6 to file.** It does not
  exist at HEAD (Problem 3). Filing it would have created an item nobody could close.
- **The absent per-session revoke endpoint** (`DELETE /v1/auth/token/{id}`), which this list makes
  visible: the Sessions tab will render rows with no per-row action. **Not filed as a new item.** It
  is the natural next slice, the store side is now a two-argument statement that already exists and
  is already scoped (`DeleteToken`), and the constraint that matters - never a bare `WHERE id = $1`
  - is recorded at the statement itself. An item would restate what the SQL comment already says.
  If the human wants it tracked, it belongs as a bullet on
  `feature-2026-06-26-profile-identity-password-sessions`, not as its own file.
- **`last_used_at` / "last active" on sessions.** The spec's Scoped-out table proposed an item.
  **Not filed**, and this is a judgment call worth stating rather than burying. It is a migration
  plus a write on the authenticated hot path plus a throttling policy, which makes it a product
  decision ("do we want session provenance at all, and at what write cost?") rather than a backlog
  chore, and nobody has asked for it. The shipped Sessions tab already states the omission on the
  page and the spec records the full shape of the work, so nothing is silently missing and nothing
  has to be rediscovered. Filing it would put a large speculative item in front of a human who never
  requested the feature. Note that `idea-2026-04-25-last-used-at-accuracy-sweeper` is **not** the
  same question despite the shared column name - it is about whether a workspace's `last_used_at` is
  accurate enough for the sweeper's age policy under long-held resources - but whoever picks up
  session last-active should read it first, because it is the project's existing worked example of a
  last-used stamp whose cadence turned out to be the whole problem.
- **`used_by` / `used_by_email` on invites.** No consumer, no hi-fi column. One `LEFT JOIN` and two
  map keys when one appears; recorded in the spec so it is a lookup rather than a rediscovery.

## Known Limitations

- **Nothing in this slice has been exercised by a browser or a human.** Both endpoints are proven
  entirely by integration tests. The two consuming tabs are separate frontend slices and neither
  exists yet, so the response shapes are argued against the hi-fi and the sibling table, not
  observed in a rendered page.
- **`go test ./...` on Windows exercises none of the endpoint behaviour.** Stated again here because
  it is the single most likely way a future reader over-reads this document's Verification section.
- **The index claims rest on one reseeded fixture.** The 1000-by-20 seeding is more realistic than
  the 20,000-by-1 it replaced, but it is still a synthetic distribution on a container, and no
  production `EXPLAIN` has been run.
- **The `ShareLock` measurement is PG16-specific** and was taken on a testcontainer. The operational
  claim it supports - that migration 000020 stalls logins rather than reads - has not been observed
  on a deployed server.
- **`total` is a second statement, not a snapshot.** Both endpoints run the list and the count
  outside a transaction, so a concurrent insert can make the footer disagree with the page by one.
  This matches every shipped list endpoint and is accepted, not overlooked.
- **The sessions list cannot answer the question users actually ask.** There is no `last_used_at`,
  no user agent, no IP, so "sign out the suspicious one" remains unanswerable and
  `DELETE /v1/auth/tokens` (which signs the caller out too) is still the only remedy. The tab must
  keep saying so.
- **The invites `COUNT(*)` runs over an unreaped table.** Small-number territory at farm scale, and
  the same pattern `CountJobs` already runs over a far larger table, but it is a real unbounded
  growth path with no janitor behind it (filed item 4).
- **No `?status=` filter on invites**, deliberately. Adding one makes the sort+filter 400 rule at
  `internal/api/jobs.go:417-422` live for this endpoint and doubles the query count.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, seventh
  iteration running. This time it produced two additions rather than a correction: the nullable
  `expires_at` semantics the item never mentions, and a strictly better derivation for the flag it
  did specify.
- **A backlog proposal is not a contract** - seven for seven. The item's `tokenhash.Hash`
  comparison was not wrong; it was a worse instrument for the same job, and the spec was free to
  replace it because the item is a description of a problem, not a design.
- **Stage the work so RED is behavioral** - honored in its strongest form yet. The NULL-expiry
  predicate's RED was manufactured on purpose by applying the naive statement, capturing the
  failure, and correcting forward without ever committing the wrong state. The dispatch-arm RED was
  real (a 400 before the key was allowlisted). The 405 route-absence REDs were declared **weak in
  advance** by the plan, with the substitute evidence named per task.
- **Re-running the implementer's own proofs is cheap and should stay standard** - honored, and it
  paid twice this cycle: the leak-gate claim was refuted, and the NULL-expiry mutation was found to
  be one mutation short of the claim it supported.
- **A finding's stated scope is a starting point, not a census** - applied to the `DeleteToken`
  finding in the strongest possible direction: the lane extended a statement's scope past the
  current diff into the diff the current one invites.
- **Backlog housekeeping is required scope** - the `/backlog close` on the parent item is the
  conductor's step and had not run when this was written. See the note below on what its Resolution
  must say; the parent must **not** be made to look as though all three endpoints shipped.
- **When the Go diff is empty, spend the integration lane on a real browser** - **not applicable**,
  and pleasantly so. The Go diff is the whole slice. Recorded rather than skipped, because it was
  unverifiable last iteration and a reader tracking the goal deserves to know why it is silent here.
- **A wide RED is not a strong RED** - honored and sharpened from the other direction; see the new
  goal below.
- **An invalidation is a continuation**, **a cadence test must assert the wiring**, **an overlay
  owns its own error surface** - all frontend-shaped and not applicable to a backend read slice.
  Listed so their silence is not read as a lapse.

New from this iteration:

- **A mutation that reddens is not a mutation that discriminates the claim.** A `require` upstream of
  the assertion you care about converts a coverage proof into a liveness proof: the test dies before
  reaching the thing the mutation was supposed to break. Before reporting "mutating X made the test
  red", check that the test failed **at the assertion about X**. The count-predicate mutation is the
  worked example. **Candidate for durable memory**, as a companion to "a wide RED is not a strong
  RED".
- **Ask what the diff makes it newly reasonable to build next.** The `DeleteToken` finding was
  invisible under a diff-scoped review and obvious under this question. Publishing identifiers,
  documenting an absent endpoint, or exposing a list are all changes that reshape what the next
  author will reach for, and the existing machinery has to fail safe under that use, not merely
  under today's single call site.
- **A performance fixture's selectivity is part of the assertion.** 20,000 rows under one user makes
  `user_id = $1` match the whole table, so the planner correctly ignores the index and the
  measurement says nothing about the claim. When a test asserts a plan, the fixture's distribution
  must resemble the one the plan is for. The integration lane catching this in **its own** fixture
  is the best form of the finding.
- **Verify a lens's claim before relaying it as a brief.** The `ACCESS EXCLUSIVE` claim went from a
  lens, through the conductor, into an engineer's task brief without anyone measuring it, and the
  engineer's `pg_locks` measurement was the first check it received. The standing rule that subagent
  claims are verified against the tree has to run in the upward direction too.
- **Moving a control from review-time to compile-time is worth a design change on its own.** The
  item's re-hash proposal was correct and would have required the query to select `token_hash`; the
  id comparison makes the leak a compile error. When two designs are equally correct and one makes
  the failure impossible to express, that is not a tie.
- **State which lane's rules do not apply, before the first task.** `web/dist` reverts were inert
  here; the sqlc CRLF discipline was load-bearing. The plan declaring both up front is why neither
  was rediscovered mid-slice. On a batch that switches lanes, the non-applicable rule set is worth
  as many words as the applicable one.
- **A plan is not re-read against the tree after the code lands.** Two consecutive iterations have
  had a plan assert something about the tree that was false by the time Phase 6 read it (a stale
  golden copy, then a nonexistent typo). Phase 6 should treat plan-sourced facts exactly as it
  treats item-sourced ones: as claims to verify, never as instructions to execute.
- **A field with no reader is either dead or a check that was never written.**
  `pageParams.SortKind` has been computed and stored since the `?sort=` feature landed and is read
  by no production code. That single fact is the whole cursor-validation bug, and it was findable by
  grep at any point in the intervening weeks. Worth a periodic sweep for the others.

## Files Most Touched

- `internal/store/query/tokens.sql` - the center of the slice. The three new statements at
  `:46-110`, each carrying the `(expires_at IS NULL OR expires_at > NOW())` predicate and a comment
  block explaining why the `IS NULL` arm is mandatory rather than defensive. Also the corrected
  `DeleteToken` at `:22-38`, whose comment is the non-task restatement of the epoch fence's
  identity-versus-currency distinction.
- `internal/api/tokens.go` - new, 131 lines, of which roughly a third is the comment block on
  `tokenEntry` (`:32-50`) recording that nothing in the file hashes anything and why `expires_at`
  absence means `never` rather than `-`.
- `internal/api/invites.go` - `InvitesSortSpec` (`:97-102`), `inviteEntry`, and
  `handleListInvites` (`:148-240`) with four dispatch arms. The pre-existing create handler is
  untouched.
- `internal/store/query/invites.sql` - four page queries plus `CountInvites`, all with explicit
  projections. `GetInviteByTokenHash` sits four lines above them as a `SELECT *` the plan
  specifically warned against copying.
- `internal/store/migrations/000020_list_endpoint_indexes.{up,down}.sql` - three indexes and their
  drops.
- `internal/api/list_endpoint_projection_test.go` - the reflect gate over the generated row types.
  The structural half of the leak control; the raw-body substring sweeps in the two integration
  files are the behavioural half, and Problem 2 established that one mutation does not fire both.
- `internal/store/tokens_delete_scope_integration_test.go` - the RED for the `DeleteToken` fix.
- `internal/api/pagination.go` - **not touched, and that is now a filed finding.** `pp.SortKind` is
  populated at `:265` and read by no production code.
- `internal/api/middleware.go` - not touched. Its `:32-35` expiry check is the source of both the
  NULL-expiry design decision and the two-clocks item.
- `README.md` - `:1096-1097`, `:1154`, `:1174-1176`, and the invites section.

## Verification

- **This pass had no shell.** Every claim below that could be checked by reading was checked against
  the worktree; nothing was executed.
- **Reported by the implementing and verifying lanes, not re-run here:** unit tests green across 19
  packages; integration green for `internal/store` at roughly 104s and `internal/api` at roughly
  414s, on multiple independent `-count=1` runs by three different parties.
- **Verified by reading:** both route registrations and their middleware chains
  (`internal/api/server.go:103,143`); the three-statement expiry predicate
  (`internal/store/query/tokens.sql:77,97,110`); the scoped `DeleteToken` and its comment
  (`:22-38`); the absence of `token_hash` from both new projections; `tokenEntry` and the
  `is_current` id comparison (`internal/api/tokens.go:51-61`); `InvitesSortSpec` carrying both
  `created_at` and `expires_at` with four arms present (`internal/api/invites.go:97-102,200,220`);
  migration 000020's two files; the full new test-file set in `internal/api/` and `internal/store/`;
  `IncrementTaskRetryCount`'s three predicates and its own "must NOT" comment
  (`internal/store/query/tasks.sql:126-145`); `RequeueTaskByID` (`:262-273`);
  `RecomputeJobStatus`'s cancelled-blind CASE (`internal/store/query/jobs.sql:97-101`);
  `BearerAuth`'s Go-clock expiry check (`internal/api/middleware.go:32`); `pp.SortKind` having no
  production reader (`internal/api/pagination.go:225,265`); the single `Cache-Control` in the whole
  API (`internal/api/events.go:56`); the hourly enrollment janitor
  (`cmd/relay-server/main.go:245-253`); the README rows at `:1096-1097`, `:1153-1154`, `:1174-1176`;
  and the **absence** of the `\v1\users\me\password` typo the plan told Phase 6 to file.
- **Not verified:** all test results, the `pg_locks` `ShareLock` measurement, the reseeded index
  fixture's planner output, the mutation REDs, the byte-for-byte generated-file comparison, and
  anything requiring execution. Each is attributed above to the lane that reported it.
- **The parent backlog item is still open in `docs/backlog/` and was not edited to look closed.**
  The `/backlog close` is the conductor's required scope. Its Resolution must state that two of the
  three endpoints shipped here and that the third moved intact to
  `feature-2026-08-13-job-retry-endpoint`; a Resolution implying all three landed would strand the
  retry constraint block that is the whole reason the split was safe.
