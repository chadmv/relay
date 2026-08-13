# Web-enabler list endpoints: GET /v1/invites and GET /v1/auth/tokens - Design

Date: 2026-08-13
Status: Draft (autonomous cycle; conductor review)

## Overview

Two read-only list endpoints, each the lone backend dependency of a shipped-but-crippled
web surface:

- **`GET /v1/invites`** (admin) unblocks the list half of the Admin Invites tab
  (`docs/backlog/feature-2026-08-08-admin-invites-tab.md:30-33`).
- **`GET /v1/auth/tokens`** (any authenticated user, own rows only) unblocks the Profile
  Sessions tab, which shipped action-only with a written footnote naming this gap
  (`docs/superpowers/specs/2026-08-12-profile-pages.md:346-358`).

Backlog item: `docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md`. That item
carries three endpoints. **The conductor has split it; `POST /v1/jobs/{id}/retry` is out of
scope here** and gets its own item and spec. See "The retry split" below - I agree with the
split and give the evidence.

Backend-only. Two store query families, one index migration, two handlers, two routes. No
frontend change in this slice; the two consuming tabs are separate follow-on work.

Written in autonomous gate mode: every design question below is decided here with a
one-line rationale in the Decisions section rather than asked.

## Where the backlog item is wrong, incomplete, or right

Every claim below was re-derived from the tree at HEAD, not taken from the item.

1. **Verified correct.** `invites.sql` really does have only three queries - `CreateInvite`,
   `GetInviteByTokenHash`, `MarkInviteUsed` (`internal/store/query/invites.sql:1,6,9`).
   `tokens.sql` really has no list query - only `CreateToken`, `GetTokenWithUser`,
   `DeleteToken`, `DeleteTokensForUser`, `DeleteOtherTokensForUser`
   (`internal/store/query/tokens.sql:1,6,22,25,28`). Neither route is registered: the auth
   block is `internal/api/server.go:96-100` and the invites block is `:138-139`, which
   registers `POST /v1/invites` only.
2. **Verified correct.** `api_tokens` has exactly five columns and no `last_used_at`, no
   user agent, no IP (`internal/store/migrations/000001_initial.up.sql:13-19`). The
   2026-08-13 correction block stands.
3. **The item's stated derivation of the current-session flag is unnecessarily
   dangerous, and there is a strictly better one.** The item says to derive it "by
   comparing `tokenhash.Hash` of the caller's own bearer token". That works, but it means
   re-hashing a live credential inside a list handler and selecting `token_hash` into the
   handler so there is something to compare against - putting the stored hash one
   forgotten map key away from the wire. **`BearerAuth` has already done this work**: it
   hashes the presented token once (`internal/api/middleware.go:25`), looks the row up,
   and injects the resolved **`TokenID`** into the request context
   (`internal/api/middleware.go:27,36-42`; `AuthUser.TokenID` at
   `internal/api/context.go:16`). The flag is therefore `row.ID == authUser.TokenID`, a
   UUID comparison in Go, and the query never selects `token_hash` at all. See decision 8.
4. **`expires_at` on `api_tokens` is NULLABLE and a NULL means "never expires".** The
   column has no `NOT NULL` (`000001_initial.up.sql:18`), and `BearerAuth` only rejects on
   `row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now())`
   (`internal/api/middleware.go:32-35`) - so a NULL-expiry token authenticates forever.
   Production `issueToken` always stamps 30 days (`internal/api/auth.go:44-48`), but test
   helpers create NULL-expiry tokens (`internal/api/api_test.go:57`,
   `internal/api/middleware_test.go:45`) and the schema permits them. A list filtered with
   a bare `expires_at > NOW()` would **hide the most powerful tokens in the system**. The
   item does not mention this. See decision 10.
5. **The item's four invite states are all representable, but only three of them from
   server facts.** `active`, `expired` and `redeemed` follow from `used_at` and
   `expires_at`; `expiring` requires an invented threshold. The shipped app already solved
   this and the answer is not "add a status column to the response" - see the next point.
6. **The house rule for status pills is "server returns facts, client does the
   arithmetic", and it is written down.** `web/src/admin/enrollments/enrollmentStatus.ts:5,21-26`
   derives `ACTIVE | EXPIRING | EXPIRED` from `expires_at` and a local clock with a 1h
   window, and its header comment (`:7-20`) states exactly why the server must not assert
   it: a server-computed `expired` goes stale the instant the row is on screen, which the
   client can disprove with arithmetic it already holds. `GET /v1/agent-enrollments`
   returns no status field (`internal/api/agent_enrollments.go:83-94`). See decision 4.
7. **Two hi-fi invite columns are unbackable and the consuming item already says so**
   (`docs/backlog/feature-2026-08-08-admin-invites-tab.md:64-68`), and the sibling table
   already omitted the same two with the reason written at the site
   (`web/src/admin/enrollments/EnrollmentsTable.tsx:5-10`). `TOKEN PREFIX` cannot be
   supplied: only `tokenhash.Hash(rawHex)` is stored (`internal/api/invites.go:56`) and
   there is no prefix column. `CREATED BY` as an email **can** be supplied and should be,
   because a house precedent exists (`JOIN users u ON u.id = j.submitted_by` yielding
   `submitted_by_email`, `internal/store/query/jobs.sql:16,20`). See decisions 5 and 6.
8. **Neither table has a supporting index, and `api_tokens` has no `user_id` index at
   all.** `invites` has no index of any kind (no `CREATE INDEX ... ON invites` exists in
   any migration). `api_tokens` lost its only non-unique index at
   `000018_hot_path_indexes.up.sql:25`, leaving just the `UNIQUE(token_hash)` btree, so
   `DeleteTokensForUser` and `DeleteOtherTokensForUser` already sequential-scan today.
   Both new list queries need index support, and one of them fixes an existing wart. See
   decision 11.
9. **Neither table is ever reaped.** `agent_enrollments` has an hourly janitor
   (`cmd/relay-server/main.go:253` calling `DeleteExpiredAgentEnrollments`). There is no
   equivalent for `invites` or `api_tokens`, and no `DELETE FROM users` query exists
   anywhere in `internal/store/query/` - archival is soft (`archived_at`). Both tables
   grow monotonically. This decides the pagination and `total` shape below and produces
   one backlog proposal.

### The retry split - I agree, and here is the extra evidence

The conductor's rationale holds and gains a fourth reason from the schema. `POST /v1/jobs/{id}/retry`
is a **write** on `tasks`, so it lands squarely inside the epoch fence, which neither
endpoint in this spec touches at all. Bundling a fenced multi-row write with two read-only
list endpoints would put the highest-risk change in the repo behind the same review as two
`SELECT`s, and the CLAUDE.md Invariants block is explicit that the fence is where the
high-severity findings come from. The two endpoints here share pagination machinery,
sqlc/CRLF handling, and an index migration with each other and nothing with retry. Split
confirmed; retry item drafted in Phase 6.

## Verified schema facts

### invites (`internal/store/migrations/000002_invites.up.sql:1-10`)

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | UUID | no | `DEFAULT gen_random_uuid()` |
| `token_hash` | TEXT | no | `UNIQUE`. **Never leaves the database.** |
| `email` | TEXT | **yes** | Set only when the invite is bound to an address (`internal/api/invites.go:65-71`) |
| `created_by` | UUID | no | `REFERENCES users(id) ON DELETE CASCADE` |
| `created_at` | TIMESTAMPTZ | no | `DEFAULT NOW()` |
| `expires_at` | TIMESTAMPTZ | **no** | Always set; `POST` default 72h, max 720h (`invites.go:31,43-47`) |
| `used_at` | TIMESTAMPTZ | yes | The single redemption marker |
| `used_by` | UUID | yes | `REFERENCES users(id) ON DELETE SET NULL` |

`MarkInviteUsed` is the only writer of `used_at`/`used_by` and sets both together under
`WHERE id = $1 AND used_at IS NULL` (`internal/store/query/invites.sql:9-12`), called once
from registration (`internal/api/auth.go:147-158`). So redemption is one-way and terminal;
`used_at IS NOT NULL` is a complete and stable predicate for "redeemed".

Because users are never hard-deleted, an **inner** `JOIN users ON id = created_by` never
drops an invite row. Verified: no `DELETE FROM users` statement exists in
`internal/store/query/`; `users.sql` archives via `archived_at`.

### api_tokens (`internal/store/migrations/000001_initial.up.sql:13-19`)

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | UUID | no | Not a credential; auth is by hash of the raw token |
| `user_id` | UUID | no | `REFERENCES users(id) ON DELETE CASCADE` |
| `token_hash` | TEXT | no | `UNIQUE`. **Never leaves the database.** |
| `created_at` | TIMESTAMPTZ | no | `DEFAULT NOW()` |
| `expires_at` | TIMESTAMPTZ | **yes** | NULL means never expires (`middleware.go:32-35`) |

There is no `last_used_at`, no user agent, no IP, no device label, and no login-event
table anywhere. A "last active" column is a migration plus a hot-path write on every
authenticated request plus a throttling decision, and stays out of this slice.

## The house list-endpoint shape this matches

The endpoint being matched is **`GET /v1/agent-enrollments`**
(`internal/api/agent_enrollments.go:75-225`, `internal/store/query/agent_enrollments.sql:23-65`).
It is the closest sibling: admin-gated, token-issuing resource, expiry-bearing rows,
shipped and consumed by a live tab.

Its shape, which both new endpoints adopt without deviation:

- `parsePage(w, r, <Spec>)` handles `?limit=` (default 50, max 200,
  `internal/api/pagination.go:205-206,239-249`), `?cursor=` and `?sort=`, writing its own
  400s (`pagination.go:239-286`).
- A `SortSpec` allowlist (`pagination.go:149-155`); an unknown key is a 400 naming the
  supported keys and the request path (`pagination.go:254-259`).
- Keyset pagination over `(sort_col, id)` with `LIMIT n+1`, columns enumerated explicitly
  in the `SELECT` (`agent_enrollments.sql:24,28-31`).
- `buildPage` trims to `limit` and emits the cursor from the last **kept** row
  (`pagination.go:305-329`).
- A `COUNT(*)` over the **same predicate as the list**, returned as `total` in the
  `page[T]` envelope `{items, next_cursor, total}` (`pagination.go:288-293`).
- Row conversion to `map[string]any` so optional keys are omitted, not nulled
  (`agent_enrollments.go:83-94`).

**The sort+filter 400 rule (`internal/api/jobs.go:417-422`) does not bite here**, because
neither endpoint takes a filter parameter (decisions 3 and 10). It becomes live the moment
someone adds `?status=` to invites, and the spec says so there rather than pre-building it.

**The single most dangerous property of this shape**: `handleListAgentEnrollments` ends in
`default: panic("... missing dispatch arm for sort key " + pp.Sort)`
(`agent_enrollments.go:215-217`). `parseSort` strips a leading `-` before checking the
allowlist (`pagination.go:178-181`), so **every key in `SortSpec.Keys` is reachable in
both directions**, and a key with only one implemented arm is a client-triggerable panic.
`net/http` recovers per connection, so the blast radius is a 500 plus a dropped
connection rather than a crashed server, but it is still remote-triggerable by an
authenticated user. Both new specs must have an arm for every direction of every key, with
a test per arm (see Testing).

## GET /v1/invites

### Route and gating

```go
// beside internal/api/server.go:139
mux.Handle("GET /v1/invites", auth(admin(http.HandlerFunc(s.handleListInvites))))
```

`auth(admin(...))`, identical to the existing `POST /v1/invites` (`server.go:139`).
Unauthenticated is 401 (`middleware.go:21,29`), non-admin is 403
(`middleware.go:50-58`). Invites carry invitee email addresses, so a non-admin read would
be a disclosure, not just a policy miss.

### Response, field by field

`page[map[string]any]` with `items` shaped:

| Key | Type | Presence | Source |
|---|---|---|---|
| `id` | string (UUID) | always | `invites.id` |
| `created_at` | RFC3339 | always | `invites.created_at` |
| `expires_at` | RFC3339 | always | `invites.expires_at` (NOT NULL) |
| `created_by` | string (UUID) | always | `invites.created_by` |
| `created_by_email` | string | always | `JOIN users` (inner; see schema facts) |
| `email` | string | **omitted when the invite is not email-bound** | `invites.email` |
| `used_at` | RFC3339 | **omitted when the invite is unredeemed** | `invites.used_at` |

Envelope: `{"items": [...], "next_cursor": "<opaque or empty>", "total": <int64>}`.

### Deliberately absent, and why

- **`token_hash`** - the stored SHA-256 of the raw token. Absent **by construction**: the
  query enumerates columns and never selects it, so the sqlc row type has no such field
  and adding it back is a deliberate edit to two files rather than a review miss. This is
  the single most important property of the endpoint.
- **`token`, `token_prefix`** - the raw token exists only in the `POST` response
  (`internal/api/invites.go:80-82`) and is not stored in any form; no prefix column
  exists. There is nothing to return, and a prefix would be a new column that weakens the
  secret for a cosmetic column. The consuming tab omits the hi-fi's `TOKEN PREFIX` header
  exactly as `EnrollmentsTable.tsx:7-9` already does.
- **`status`** - not a server field. See decision 4 and the derivation table below.
- **`used_by` / `used_by_email`** - no consumer. The hi-fi's column list is
  `TOKEN PREFIX | BINDS TO | EXPIRES | CREATED BY | STATUS | ACTIONS`
  (`design_handoff_relay_holo/hifi3-holo-pages.jsx:2096`); there is no redeemed-by column
  and no backlog item asks for one. Adding it is one `LEFT JOIN` and two map keys later
  (`used_by` is `ON DELETE SET NULL`, so it must be a LEFT join when it arrives).

### Client-side status derivation (informative, for the consuming tab)

The server ships facts; the tab derives the pill, mirroring `enrollmentStatus.ts:21-26`:

| Pill | Predicate |
|---|---|
| `REDEEMED` | `used_at` present. **Checked first** - redemption is terminal and a redeemed invite that later passes its expiry is still redeemed, never expired. |
| `EXPIRED` | `expires_at <= now` |
| `EXPIRING` | `expires_at - now < 1h` (the same window as `enrollmentStatus.ts:5`) |
| `ACTIVE` | otherwise |

All four of the item's states are representable. `EXPIRING` is a client threshold, not a
server fact, which is why the server does not assert it.

### Query and pagination

`SortSpec`: `Default: "-created_at"`, `Keys: {created_at: SortKeyTimestamp, expires_at:
SortKeyTimestamp}` - byte-identical in shape to `AgentEnrollmentsSortSpec`
(`agent_enrollments.go:75-81`). Four dispatch arms, four `:many` queries, plus
`CountInvites`.

**No `WHERE` filter.** Unlike enrollments, which lists active-only
(`agent_enrollments.sql:26-27`), the invites list returns every row in every state,
because redeemed and expired invites are exactly what the tab exists to show. `total` is
therefore `SELECT COUNT(*) FROM invites`, matching the list predicate (the empty one)
exactly, so the pagination footer cannot lie.

Column list, per arm (only the comparison and `ORDER BY` change):

```sql
-- name: ListInvitesPage :many
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.created_at, i.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;
```

`i.token_hash` is absent from the projection. That is the leak-prevention control.

## GET /v1/auth/tokens

### Route and gating

```go
// beside internal/api/server.go:100
mux.Handle("GET /v1/auth/tokens", auth(http.HandlerFunc(s.handleListTokens)))
```

`auth(...)` only, **not** `admin` - this is the self-service block, same gating as
`DELETE /v1/auth/tokens` (`server.go:100`). Rows are scoped to `authUser.ID` taken from
the context injected by `BearerAuth` (`middleware.go:36-43`). **There is no `user_id`
parameter and there must never be one**: the identity is the bearer token, exactly as
every other endpoint in that block behaves
(`docs/superpowers/specs/2026-08-12-profile-pages.md:108-116`). A `?user_id=` in the query
string is ignored, and a test asserts that.

### Response, field by field

| Key | Type | Presence | Source |
|---|---|---|---|
| `id` | string (UUID) | always | `api_tokens.id` |
| `created_at` | RFC3339 | always | `api_tokens.created_at` |
| `expires_at` | RFC3339 | **omitted when NULL, meaning the token never expires** | `api_tokens.expires_at` |
| `is_current` | bool | always | `row.ID == authUser.TokenID` (see below) |

`is_current` is unconditionally present, never omitted, because "this row is not your
current session" is a positive fact the UI must be able to state.

The consuming tab renders an absent `expires_at` as **`never`**, not as the `-` placeholder
it uses for absent optional strings (`EnrollmentsTable.tsx:59`). A non-expiring credential
is a security fact, not missing data.

### Deliberately absent, and why

- **`token_hash`** - as above, absent by construction: not in the projection, not in the
  sqlc row type. With the `is_current` derivation below, the handler has no reason to hold
  a hash at all.
- **`last_used_at`, user agent, IP, location, device, "kind"** - no column exists for any
  of them (`000001_initial.up.sql:13-19`). Each would be a migration plus a write on the
  authenticated hot path. The shipped Sessions tab already states this omission on the
  page, so the honesty debt is already paid; this endpoint does not reintroduce it as a
  null column.
- **`user_id`** - every row belongs to the caller by construction. Echoing it adds nothing
  and invites a client to key off it.

### The current-session flag: derivation, and where the comparison happens

**In Go, comparing row ids. Not in SQL, and not by re-hashing anything.**

`BearerAuth` already hashes the presented bearer token once through the single hashing
entry point (`tokenhash.Hash`, `internal/api/middleware.go:25`), resolves the row via
`GetTokenWithUser` (`tokens.sql:6-20`), and injects `AuthUser.TokenID`
(`middleware.go:36-42`, `context.go:16`). The handler therefore does:

```go
entry["is_current"] = row.ID == authUser.TokenID   // pgtype.UUID is comparable
```

Why this and not the alternatives:

- **Versus re-hashing the bearer token in the handler** (what the backlog item proposes):
  that adds a second `tokenhash.Hash` call site on a live credential, and it only works if
  the query selects `token_hash` so there is something to compare - which puts the stored
  hash inside the handler, one map key away from the response. The id comparison needs
  neither.
- **Versus `(id = sqlc.arg(current_token_id)) AS is_current` in SQL**: functionally
  equivalent and safe, but it pushes a presentation concern into the store layer and makes
  the query non-reusable, while saving nothing. Keeping store queries dumb is the house
  pattern (`agent_enrollments.go:83-94` maps in Go).

Correctness note: `TokenID` is resolved server-side from the presented credential and is
never read from the wire. That is the same discipline the Invariants require of a worker
identity at registration. A NULL/zero `TokenID` cannot arise here - the handler is
unreachable without a successful `BearerAuth` - but the comparison is against a
`pgtype.UUID` from the same table, and both sides carry `Valid: true`, so a zero-value
comparison would fail closed (no row marked current) rather than marking an arbitrary row.

### Which rows are listed

**Unexpired only**, scoped to the caller:

```sql
WHERE user_id = sqlc.arg(user_id)
  AND (expires_at IS NULL OR expires_at > NOW())
```

The `expires_at IS NULL OR` arm is mandatory, not defensive noise: NULL means never
expires and such a token authenticates (`middleware.go:32-35`). Omitting it hides
precisely the credentials a user most needs to see. This is the one place a plausible
implementation is silently, dangerously wrong, and it gets a dedicated test.

Why exclude expired rows:

1. **The tab's question is "what can authenticate as me right now?"** An expired row
   cannot; `BearerAuth` 401s it (`middleware.go:32-35`).
2. **Nothing reaps them** (finding 9), so a long-lived account accumulates one dead row
   per login forever. An unfiltered list is mostly archaeology.
3. **A user cannot act on them individually.** The only revocation controls are
   `DELETE /v1/auth/token` (the current one) and `DELETE /v1/auth/tokens` (all of them,
   including the caller's - `internal/api/auth.go:350-357`, `tokens.sql:25-26`); there is
   no `DELETE /v1/auth/token/{id}`. Listing rows with no available action and no bearing
   on present security is the "render a dash in three of four cells" mistake the profile
   spec already rejected.
4. **Precedent.** `GET /v1/agent-enrollments` lists active-only and the tab states so on
   the page (`web/src/admin/enrollments/EnrollmentsTab.tsx:200-203`). The Sessions tab
   will carry the equivalent sentence.

Interaction with the two existing delete paths, which the consuming tab must state:

- `DELETE /v1/auth/tokens` deletes **every** row including the caller's, so the list goes
  empty and the caller is signed out. The SPA already tears its own session down on that
  204 (`2026-08-12-profile-pages.md:456-469`), so it never renders an empty list against a
  dead token.
- `PUT /v1/users/me/password` calls `DeleteOtherTokensForUser` (`auth.go:325-328`,
  `tokens.sql:28-29`), so after a password change the list contains exactly one row and it
  is `is_current: true`. That is a clean, testable observable and a good acceptance
  criterion.

### Pagination

Same envelope and machinery. `SortSpec`: `Default: "-created_at"`, `Keys: {created_at:
SortKeyTimestamp}` - **two** dispatch arms (`-created_at`, `created_at`), because
`parseSort` accepts both directions of every allowlisted key.

`expires_at` is deliberately **not** a sort key: it is nullable, so it would need the
`NULLS LAST`/`NULLS FIRST` index pair and cursor-null handling that
`000013_paginated_sort_indexes.up.sql:15-16` needed for `workers.last_seen_at`, for a list
whose realistic length is single digits. `total` is
`SELECT COUNT(*) FROM api_tokens WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > NOW())`
- the identical predicate to the list, so the footer count matches what paging can reach.

Pagination is kept rather than dropped even though the list is short, because nothing
reaps `api_tokens` and a scripted `relay login` loop can make it long; an unbounded
response body is the failure mode we do not want to discover in production.

## Invariants: which apply, which do not

Read against the CLAUDE.md Invariants block.

**Do not apply, stated explicitly rather than left silent:**

- **Epoch fence.** Both endpoints are `SELECT`-only. Neither writes `tasks.status` nor
  `task_logs`, neither touches `assignment_epoch`, `worker_id`, or any task row. There is
  no generation to fence and no assignment to end. Nothing in this slice may acquire a
  write; if a future revision adds one, the fence question reopens from scratch.
- **End the generation before releasing the resource.** No async lifecycle, no stream, no
  abortable continuation. Two request-scoped handlers.
- **Single job-spec pipeline.** No job spec is parsed, validated, or created.
- **One bounded sender per gRPC stream.** No gRPC surface is touched.
- **Identity-checked teardown.** No connection state, no registry, nothing torn down.
- **No interior pointers across locks.** No shared mutable registry is read or written;
  both handlers touch only request-scoped values and sqlc row structs.

**Do apply:**

- **Single JSON entry point.** Both are `GET`s with no request body, so `readJSON`
  (`internal/api/server.go:199-211`) is not called - the invariant is satisfied by there
  being no body to read. The live obligation is the negative one: **neither handler may
  introduce a body reader**, and neither may read query parameters into a decoder. All
  input arrives through `parsePage` and `r.URL.Query()`. Responses go through `writeJSON`
  (`server.go:182-186`) and errors through `writeError` (`:188-190`).
- **All hashing goes through `internal/tokenhash.Hash`; never inline `sha256.Sum256` at a
  new site.** Honored in the strongest available form: **neither handler hashes anything
  at all.** `GET /v1/invites` never sees a token; `GET /v1/auth/tokens` reuses the hash
  `BearerAuth` already computed, by way of the resolved `TokenID`. A patch that adds a
  hash call to either handler is a design regression, not a detail.
- **Authorization is resolved server-side, never taken from the wire.** The invites list is
  admin-gated by middleware; the tokens list is scoped by `authUser.ID` from context. This
  is the same discipline the epoch-fence bullet demands of worker identity, applied to a
  read path: the caller does not get to name the rows they receive.

## Architecture

New and modified files:

| File | Change |
|---|---|
| `internal/store/query/invites.sql` | Append 4 `:many` page queries + `CountInvites`. Existing 3 queries untouched. |
| `internal/store/query/tokens.sql` | Append 2 `:many` page queries + `CountActiveTokensForUser`. Existing 5 untouched. |
| `internal/store/migrations/000020_list_endpoint_indexes.{up,down}.sql` | New. Three indexes (below). Next free number: highest today is `000019_status_vocabulary_checks`. |
| `internal/api/invites.go` | Add `InvitesSortSpec`, four row-to-map + row-key pairs, `handleListInvites`. |
| `internal/api/tokens.go` | **New file.** `TokensSortSpec`, two row-to-map + row-key pairs, `handleListTokens`. |
| `internal/api/server.go` | Two route registrations (`:100` block and `:139` block). |
| `internal/store/*.sql.go`, `models.go` | Regenerated by `make generate`. Never hand-edited. |

`handleListTokens` goes in a **new** `internal/api/tokens.go` rather than into `auth.go`.
`auth.go` already carries register, login, password change, and both logout paths at 357+
lines; the house convention is one file per resource (`invites.go`, `reservations.go`,
`workers.go`), and `/v1/auth/tokens` is a resource. `auth.go` is not otherwise refactored -
that is unrelated work.

### Migration 000020

```sql
CREATE INDEX idx_invites_created_id ON invites(created_at DESC, id DESC);
CREATE INDEX idx_invites_expires_id ON invites(expires_at DESC, id DESC);
CREATE INDEX idx_api_tokens_user_created_id ON api_tokens(user_id, created_at DESC, id DESC);
```

Only DESC variants, matching `000013_paginated_sort_indexes.up.sql:7-10`: Postgres scans a
btree backwards for the ASC arms. Plain `CREATE INDEX`, no `CONCURRENTLY` -
golang-migrate wraps each migration in a transaction and `CONCURRENTLY` cannot run in one
(`000018_hot_path_indexes.up.sql:2-4`). The `.down.sql` drops all three with `IF EXISTS`.

`idx_api_tokens_user_created_id` also removes an existing wart: `api_tokens` has had no
`user_id` index since `000018_hot_path_indexes.up.sql:25` dropped the redundant
`token_hash` one, so `DeleteTokensForUser` and `DeleteOtherTokensForUser` - the latter on
the password-change path - sequential-scan the table today. This is a genuine incidental
improvement, in scope because the index is needed anyway.

### The sqlc CRLF hazard (read before running `make generate`)

`make generate` regenerates the whole store layer. sqlc emits LF; this repo is CRLF, so it
rewrites line endings across **every** generated file, not just the ones with real changes.
Per CLAUDE.md: after generating, run `git diff --ignore-all-space`, keep only the real
content change, and `git checkout -- <file>` every LF-only hunk.

Two known traps:

1. **The revert can silently discard the regenerated file.** The standing lesson is that a
   query-comment edit was lost this way, leaving a generated doc comment contradicting its
   own source. Both `invites.sql.go` and `tokens.sql.go` here have real content changes, so
   they must **not** be blanket-reverted. After the cleanup, re-read both files and confirm
   the new `ListInvitesPage*` / `ListTokensForUserPage*` functions and their doc comments
   are present and match the `.sql` source.
2. **`models.go` should not change.** No column is added, so a content diff in `models.go`
   means something unintended happened.

## Security and system design

- **Threat model, invites.** The asset is the invite token, which grants account creation.
  It exists in plaintext exactly once, in the `POST` 201 body (`invites.go:80-82`); only
  its SHA-256 is stored. The list endpoint's entire security story is that it cannot
  return that hash, enforced by column enumeration rather than by remembering. Secondary
  asset: invitee email addresses, which is why the endpoint is admin-only rather than
  merely authenticated.
- **Threat model, sessions.** The asset is the set of live bearer tokens. The endpoint
  returns row ids, timestamps and a boolean. A row `id` is not a credential: authentication
  is `GetTokenWithUser(hash)` (`tokens.sql:6-20`), and no route accepts a token id. The
  horizontal-privilege vector is a `user_id` parameter, and there is none - the scoping
  value comes from the context, never the query string.
- **Enumeration.** Neither endpoint takes a user-supplied identifier, so there is no
  existence oracle to probe. `GET /v1/invites` is admin-only; `GET /v1/auth/tokens` can
  only ever describe the caller.
- **Load and failure modes.** Both are indexed keyset scans capped at `limit+1` rows,
  limit <= 200 (`pagination.go:206`), plus one `COUNT(*)`. The invites `COUNT(*)` is over
  an unreaped table; at farm scale invites are hand-created by admins so this is
  small-number territory, and it is the same pattern `CountJobs` already runs
  (`jobs.sql:34-35`) over a far larger table. Failure mode on a DB error is a 500 with a
  generic sentence and no detail, matching `agent_enrollments.go:171-172`. No rate limiting
  is added: `ratelimit.go` is applied only to login and register (`server.go:82-94`), both
  new endpoints are authenticated, and neither is expensive.
- **Availability.** Read-only, no locks taken beyond MVCC snapshots, no transaction needed
  (single statement plus a count; a stale-by-microseconds `total` is acceptable and matches
  every shipped list endpoint).
- **The remaining honest gap.** Neither endpoint can tell a user *where* a session came
  from, so "sign out the suspicious one" is still not answerable. The blunt instrument
  (`DELETE /v1/auth/tokens`) remains the only remedy. The consuming tab must keep saying so.

## Testing

Integration tests are the gate. Both endpoints are pure database reads, so almost nothing
about them is exercisable without Postgres. **`make test` on Windows will not run any of
the tests below** - they are `//go:build integration` and require Docker; the real command
is `make test-integration` (or `go test -tags integration -p 1 ./internal/api/... -run TestListInvites -v -timeout 120s`).

### Unit (no Docker), `package api`

Only the pure mapping helpers, which is a small but non-vacuous surface:

- `inviteRowToMap` and `tokenRowToMap` produce **exactly** the documented key set for a
  synthesized row - asserted as a key-set equality, not a series of `assert.Equal` on
  individual keys, so an added key fails. This is the regression gate on the response
  shape.
- Optional-key omission: an invite with no `email`/`used_at` yields a map where those keys
  are **absent**, verified with the two-return map lookup, not by comparing to nil.
- `is_current` is `true` exactly when the row id equals the supplied token id, including a
  negative case with a different id.

### Integration, `GET /v1/invites`

- 401 without a token; 403 for an authenticated non-admin (mirroring
  `internal/api/invites_test.go:19-39`); 200 for an admin.
- **Hash non-leakage, discriminating form.** Create an invite through `POST`, compute
  `tokenhash.Hash(rawToken)` in the test, then assert the raw list response **body string**
  does not contain that value and does not contain the substring `token`. Asserting on the
  parsed struct is weaker: it passes against a handler that adds the key under a different
  name.
- Rows in all four presentation states appear: active, near-expiry, expired, and redeemed
  (seeded by direct store calls plus `MarkInviteUsed`). Assert the redeemed row carries
  `used_at` and the others omit it, and that expired and redeemed rows are **present** -
  this endpoint does not filter, unlike enrollments.
- **A redeemed-and-expired invite reports `used_at`** and the client's precedence rule
  therefore resolves to REDEEMED. Pins the terminal-state ordering at the data level.
- **Every sort arm returns 200.** Four requests: `-created_at`, `created_at`,
  `-expires_at`, `expires_at`. This is the panic test; without an arm per key-direction one
  of these 500s.
- Ordering is actually applied: seed three invites with distinct `created_at` and
  `expires_at` and assert the returned id order per arm. A 200 alone does not prove the
  arm dispatched to the right query.
- Paging with `limit=1` over three rows walks the cursor to exhaustion with no duplicate
  and no omitted id, and `next_cursor` is empty on the last page.
- `total` equals the full row count regardless of `limit`, on both the first and a later
  page.
- 400s: unknown sort key (message names the supported keys and the path,
  `pagination.go:256-258`), malformed cursor, and a cursor issued under a different sort
  (`pagination.go:279-281`).
- `created_by_email` is the creating admin's email, and is present on every row.

### Integration, `GET /v1/auth/tokens`

- 401 without a token. **200 for a non-admin** - a paired positive against the invites 403
  test; writing both together is what catches a mis-chained `admin(...)`.
- **Only the caller's rows.** Seed a second user with two tokens; assert none of their ids
  appear, and assert the count. Then re-issue the same request with
  `?user_id=<other-user-id>` appended and assert the response is byte-identical - the
  parameter is ignored.
- **`is_current` is true for exactly one row, and it is the presented token.** Mint two
  tokens for the caller, authenticate with a known one, assert exactly one `true` and that
  its `id` matches. A test asserting only "some row is current" passes against a handler
  that marks the first row.
- **The NULL-expiry row is listed.** Seed a token with `pgtype.Timestamptz{}` (as
  `internal/api/api_test.go:57` already does), assert it appears and that its `expires_at`
  key is **absent**. This is the discriminating test for the `expires_at > NOW()` trap; an
  implementation missing the `IS NULL` arm passes every other test in this file and fails
  only this one.
- **The expired row is not listed.** Seed a token with `expires_at` in the past, assert its
  id is absent and that `total` also excludes it. Paired with the previous test so the two
  cannot both be satisfied by "return everything" or "return nothing".
- **Hash non-leakage**, same discriminating form as invites: the body string never contains
  the caller's token hash nor their raw token.
- **After `PUT /v1/users/me/password`, the list has exactly one row and it is
  `is_current: true`.** Directly exercises `DeleteOtherTokensForUser`
  (`auth.go:325-328`) through the new read path.
- Both sort arms return 200 and produce the asserted order; unknown key 400; bad cursor
  400; `limit=1` cursor walk.
- Paging preserves `is_current`: with `limit=1` and two tokens, the current flag lands on
  the right page rather than being computed per-page from the first row.

Plan-supplied test bodies are guesses until run RED. Every absence assertion above carries
a positive control in the representation the real failure would take.

## Acceptance criteria

1. `GET /v1/invites` is registered as `auth(admin(...))`: 401 unauthenticated, 403 for a
   non-admin, 200 for an admin.
2. Its items carry exactly `id`, `created_at`, `expires_at`, `created_by`,
   `created_by_email`, and - only when set - `email` and `used_at`. No other key.
3. No response from either endpoint contains a token hash, a raw token, or a token prefix,
   asserted against the raw body bytes; neither store query selects `token_hash`.
4. The invites list returns rows in every state (active, expired, redeemed) with no status
   filter and no server-side `status` field; `total` is the unfiltered row count.
5. `GET /v1/auth/tokens` is registered as `auth(...)` (not admin), returns only the
   caller's rows, ignores any `user_id` query parameter, and 401s unauthenticated.
6. Its items carry exactly `id`, `created_at`, `is_current`, and - only when the column is
   non-NULL - `expires_at`. `is_current` is always present.
7. `is_current` is true for exactly one row per response set, and it is the row whose id
   equals `AuthUser.TokenID`. Neither handler calls `tokenhash.Hash` or any hash function.
8. A NULL-`expires_at` token is listed with `expires_at` absent; an expired token is not
   listed and is not counted in `total`.
9. Both endpoints use `parsePage`/`buildPage`/`page[T]` with the shipped defaults
   (limit 50, max 200) and return `{items, next_cursor, total}`; `total` is computed with
   the same predicate as the list.
10. Every key in each `SortSpec.Keys` returns 200 in **both** directions and produces the
    correct order; an unknown key returns 400 naming the supported keys and the path.
11. Migration `000020` adds the three indexes and its `.down.sql` drops them; `make
    generate` output is committed with LF-only hunks reverted and the two new generated
    files verified to contain their new functions and doc comments.
12. `make test-integration` is green, including the existing invites and auth suites; no
    existing test file requires an edit.
13. No source file outside the table in Architecture is modified; `web/` is untouched.
14. Backlog items are **proposed**, not auto-filed, in Phase 6; the retry item is drafted
    as its own file; `feature-2026-06-26-web-enabler-backend-endpoints.md` is **not** closed
    by this slice (it still owns the retry endpoint) - it is amended to reflect the split.

## Scoped out, with the enabler to propose

| Element | Why it is out | Enabler |
|---|---|---|
| `POST /v1/jobs/{id}/retry` | Conductor split; it is a fenced multi-row write with an unresolved `retry_count` decision and a `cancelled`-blind `RecomputeJobStatus` ruling, and it ties to a second backlog item. Nothing shared with these two reads. | **Propose:** a dedicated `feature-2026-08-13-job-retry-endpoint.md`, carrying the item's existing constraint block verbatim and cross-linking `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`. |
| `last_used_at` / "last active" on sessions | No column; a migration plus a write on every authenticated request plus a throttling decision. | **Propose:** `feature-2026-08-13-api-token-last-used-tracking.md` (medium). Must state the hot-path write cost and a throttle (for example, update at most once per N minutes per token). |
| Per-session revoke (`DELETE /v1/auth/token/{id}`) | Out of scope for a read slice, but the list makes its absence visible: the tab will show rows with no per-row action. | **Propose:** `feature-2026-08-13-revoke-session-by-id.md` (medium). Now cheap: this endpoint supplies the id, and the store needs only a `DeleteTokenForUser(id, user_id)` - user-scoped, never a bare `WHERE id = $1`. |
| `used_by` / `used_by_email` on invites | No consumer; the hi-fi has no redeemed-by column. | **None.** One `LEFT JOIN` plus two keys when a consumer appears; recorded here so it is a lookup, not a rediscovery. |
| Invite revoke / delete | No endpoint and none requested; the hi-fi footnote states invites are one-time with expiry or redemption as the only terminal states (`hifi3-holo-pages.jsx:2130`), which is accurate. | **None.** |
| `?status=` filter on invites | No consumer asked for it; the list is short and the client already derives the pill. Adding it later makes `jobs.go:417-422`'s sort+filter 400 rule live for this endpoint. | **None** - recorded so whoever adds it knows the rule attaches. |
| Reaping expired invites and api_tokens | Both tables grow monotonically; only `agent_enrollments` has a janitor (`cmd/relay-server/main.go:253`). Not a blocker at farm scale. | **Propose:** `idea-2026-08-13-reap-expired-invites-and-tokens.md` (low) - extend the existing hourly janitor goroutine rather than adding a second timer. |
| A server-asserted `status` enum on either endpoint | Contradicts the shipped derivation rule (`enrollmentStatus.ts:7-20`) and would go stale on screen. | **None.** |
| Sorting sessions by `expires_at` | Nullable column; needs the NULLS-ordered index pair and cursor-null handling for a single-digit list. | **None.** |
| The two consuming UI tabs | Separate frontend slices with their own items. | **None** - both items already exist and can drop their "backend-blocked" caveat once this lands. |

Per the standing rule these are proposals. Phase 6 files them for human accept; nothing is
auto-filed.

## Decisions

1. **Spec the two list endpoints only; retry moves to its own item.** Retry is a fenced
   write on `tasks` with unresolved semantics, sharing no code with two `SELECT`s; bundling
   would review the repo's highest-risk change alongside two reads.
2. **Match `GET /v1/agent-enrollments` exactly for both endpoints.** It is the closest
   shipped sibling (admin, token-issuing, expiry-bearing, consumed by a live tab), so its
   `parsePage`/`SortSpec`/`buildPage`/`page[T]` shape transfers without adaptation.
3. **The invites list applies no filter and takes no filter parameter.** Redeemed and
   expired invites are the point of the tab, unlike enrollments where a consumed row simply
   vanishes. Consequence: the sort+filter 400 rule at `jobs.go:417-422` does not apply here
   and is noted for whoever adds `?status=`.
4. **Neither endpoint returns a `status` field; the client derives the pill.**
   `enrollmentStatus.ts:7-20` already wrote down why: a server-asserted `expired` is stale
   the moment the row is on screen, and `expiring` needs an invented threshold. The server
   ships `expires_at` and `used_at`; all four states are derivable, three from server facts
   and `expiring` from the same 1h client window enrollments uses.
5. **`TOKEN PREFIX` is not supplied and must not be added.** Only the SHA-256 is stored
   (`invites.go:56`); a prefix column would be a new persisted fragment of a secret for a
   cosmetic column. `EnrollmentsTable.tsx:7-9` already omits the same header.
6. **`created_by_email` is supplied via an inner `JOIN users`.** The precedent is
   `submitted_by_email` (`jobs.sql:16,20`); a bare UUID is unusable in the tab; and the
   inner join is safe because users are archived, never deleted (no `DELETE FROM users`
   exists in `internal/store/query/`).
7. **`used_by` / `used_by_email` are omitted.** No consumer and no hi-fi column; YAGNI. A
   `LEFT JOIN` when one appears - `used_by` is `ON DELETE SET NULL`.
8. **`is_current` is computed in Go as `row.ID == authUser.TokenID`.** `BearerAuth` already
   hashed the credential and resolved the token id into the context
   (`middleware.go:25,36-42`), so this needs no second hash call site and lets the query
   omit `token_hash` entirely - the leak becomes impossible rather than merely avoided.
9. **Both queries enumerate columns; neither uses `SELECT *`.** The generated row type then
   has no `TokenHash` field at all, so leaking it is a compile error rather than a review
   catch. `ListActiveAgentEnrollmentsPage` (`agent_enrollments.sql:24`) does the same.
10. **The sessions list is filtered to unexpired, and the predicate is
    `expires_at IS NULL OR expires_at > NOW()`.** NULL means never expires and such a token
    authenticates (`middleware.go:32-35`), so the `IS NULL` arm is required for
    correctness, not defensiveness. Expired rows are excluded because they cannot
    authenticate, nothing reaps them, and no per-row action exists for them.
11. **Migration `000020` adds three DESC indexes; ASC arms scan backwards.** Follows
    `000013:7-10`. `idx_api_tokens_user_created_id` also removes an existing sequential
    scan on `DeleteTokensForUser` / `DeleteOtherTokensForUser`, orphaned when
    `000018:25` dropped the only other `api_tokens` index.
12. **Optional keys are omitted from the response, never nulled.** Matches
    `agent_enrollments.go:90-92` and the consuming table's `?? '-'` idiom
    (`EnrollmentsTable.tsx:55-59`). It also makes the wrong client check a compile error:
    with `used_at?: string`, TypeScript reports a no-overlap error on `used_at !== null`,
    whereas a nulled field would let the same mistake compile.
13. **An absent `expires_at` on a session renders as `never`, not `-`.** A non-expiring
    credential is a security fact; the `-` placeholder means "not set" and would understate
    it.
14. **Sessions sort on `created_at` only (both directions); invites on `created_at` and
    `expires_at`.** `api_tokens.expires_at` is nullable and would need the NULLS-ordered
    index pair (`000013:15-16`) for a single-digit list; `invites.expires_at` is `NOT NULL`
    and the tab sorts by expiry.
15. **Every allowlisted sort key gets an arm in both directions, with a test per arm.**
    `parseSort` strips the leading `-` before the allowlist check
    (`pagination.go:178-181`), so a missing arm is a client-triggerable
    `panic` (`agent_enrollments.go:215-217`) - a 500 plus a dropped connection, reachable by
    any authenticated user.
16. **`handleListTokens` lives in a new `internal/api/tokens.go`, not in `auth.go`.**
    One file per resource is the house layout; `auth.go` already carries five handlers and
    357+ lines. `auth.go` is otherwise untouched.
17. **`total` is computed with the list's own predicate on both endpoints.** A count over a
    different predicate makes the pagination footer state a number the user cannot page to,
    which is the defect `bug`/retro `2026-06-21-jobs-pagination-footer-absolute-range`
    already covered.
18. **No rate limiting is added.** `RateLimit` is applied only to login and register
    (`server.go:82-94`); both endpoints are authenticated, indexed, and bounded at 200 rows.
19. **The consuming web tabs are out of scope.** Their items exist and are separately
    tracked; this slice is verified entirely by integration tests.
20. **The enabler backlog item is amended, not closed.** It still owns the retry endpoint
    after the split, so closing it would lose that work.

## Risks

- **The `expires_at > NOW()` trap on sessions.** A bare comparison passes every test that
  does not seed a NULL-expiry token, and silently hides the only tokens in the system that
  never expire. The dedicated NULL test is a requirement, not a nicety.
- **A missing sort arm panics on user input.** The `default:` arm is a `panic`, not a 400.
  With four arms on invites and two on sessions, "I implemented the default sort" is a
  plausible partial implementation that 500s the moment a client sends `?sort=created_at`.
- **`SELECT *` is the tempting shortcut** and it is exactly how `token_hash` reaches a
  handler. `GetInviteByTokenHash` (`invites.sql:7`) uses it, so it is right there in the
  file being edited as a pattern to copy.
- **The CRLF revert can discard the regenerated store files.** Both `invites.sql.go` and
  `tokens.sql.go` carry real content changes here; a blanket `git checkout --` on the
  generated directory silently loses them and leaves a build that does not compile, or
  worse, doc comments that contradict their source. Verify both files after cleanup.
- **`make test` on Windows proves nothing about this slice.** Every behavioral test is
  `//go:build integration`. A green `make test` is not evidence; `make test-integration` is.
- **The invites list will look like a natural place to add `?status=`.** It is not, in this
  slice: adding it makes the sort+filter 400 rule live and doubles the query count. Ship
  the unfiltered list; the client already has the arithmetic.
- **Scope creep toward `last_used_at`.** Once the sessions list exists, "just add last
  active" will look small. It is a migration plus a write on every authenticated request
  plus a throttling policy. Keep it in its proposed item.
