# Session Retro: 2026-05-27 — List Endpoint Sort

## What Was Built

Opt-in `?sort=<key>` query parameter on every paginated REST list endpoint (jobs, workers, users, scheduled-jobs, reservations, agent-enrollments), with a per-endpoint allowlist, cursor encoding the active sort key, and 400 on sort/cursor mismatch. The change is purely additive — clients that send no `?sort=` see the historical `created_at DESC, id DESC` ordering byte-for-byte unchanged, and legacy cursors emitted by pre-feature code keep working as long as the request also omits `?sort=`.

**Pagination layer (`internal/api/pagination.go`)**

- `cursor`/`cursorWire` extended with `Sort`, `StrVal`, `IsNull` fields and `S`/`V`/`N` wire fields. The decoder enforces an "exactly one of T, V, N" invariant so cursors with ambiguous or missing sort values are rejected as malformed rather than silently dispatched against the wrong query.
- New `encodeCursorV2` accepts `time.Time`, `string`, or `*time.Time` (nil → encodes as null marker). The legacy `encodeCursor(t, id)` is kept as a test-only shim so the pre-feature wire format remains testable.
- `SortSpec` + `parseSort` validate `?sort=` against a per-endpoint allowlist. `parsePage` plumbs the spec through and rejects sort/cursor mismatch with a descriptive 400.
- `buildPage` now takes a sort string and a heterogeneous row-key callback (`func(Row) (anySortVal, pgtype.UUID)`) so the emitted cursor carries the sort that produced it.
- A `historicalDefaultSort = "-created_at"` constant captures the truth that pre-feature cursors always implied `-created_at`, regardless of any endpoint's future spec default.

**Per-endpoint dispatch (~42 sqlc queries)**

For each endpoint's primary list query, generated per-(key, direction) sqlc queries and a switch in the handler dispatching on `pp.Sort`. Default arms `panic` so a future drift between the SortSpec allowlist and the switch surfaces loudly. Counts: jobs 9, workers 7, users 10 (both archived/active variants), scheduled-jobs 14 (both admin/owner variants), reservations 7, agent-enrollments 3.

**Null-timestamp handling**

Workers `last_seen_at`, reservations `starts_at` and `ends_at` are nullable. Their cursor predicates use a `CASE WHEN @cursor_is_null::bool THEN ... END` branch with explicit `NULLS LAST` / `NULLS FIRST` index variants. Null-boundary pagination tests walk across the null/non-null transition under both directions and confirm no duplication or omission.

**Migration `000013_paginated_sort_indexes`**

19 composite indexes — `(col, id)` for non-nullable keys (Postgres scans either direction), `(col NULLS LAST/FIRST, id)` pairs for nullable timestamps.

**Client surfaces**

- CLI: `--sort` flag on `relay list`, `relay workers list`, `relay schedules list`, `relay reservations list`, `relay admin users list`. Pure pass-through to the server — no client-side allowlist to drift.
- MCP: `sort` parameter on `relay_list_jobs`, `relay_list_workers`, `relay_list_schedules`, `relay_list_reservations` with the full per-tool allowlist enumerated inline in the jsonschema description. A drift test (`internal/mcp/sort_drift_test.go`) asserts bidirectionally that every MCP-advertised key is in the server allowlist *and* every server key is surfaced to MCP.

**Docs and verification**

- README REST API section gained a `#### Configurable sort order` subsection with per-endpoint allowlist table.
- `internal/store/sort_indexes_integration_test.go` asserts all 19 indexes from migration 000013 exist in `pg_indexes`. The pre-existing `testhelper_test.go` was refactored to expose `newTestPool(t)` as a sibling helper.

## Key Decisions

**Loud failure on cursor/sort mismatch.** Considered "silent reset to page 1" for UI ergonomics, rejected. A cursor that silently picks the wrong rows is the worst kind of bug — looks like the API works, only careful clients notice the drift. 400 with a clear message keeps the server contract clean; the client absorbs the UX nicety if it wants.

**Cursor encodes the sort string.** The alternative was "trust the client to remember which sort the cursor was issued under." That trades a 1-line server check for a class of silent-corruption bugs at every call site. Wire cost is a few bytes after base64; validation cost is one string compare.

**Filtered jobs variants reject `?sort=` rather than support it.** Adding sort to `?status=` and `?scheduled_job_id=` variants would have doubled the SQL surface and the test matrix for no concrete demand. Returning 400 with a clear message ("sort not supported on filtered list variant") preserves the option for later without locking it in now.

**Auth-driven variants get the full per-key matrix.** `users` (archived vs active) and `scheduled-jobs` (admin vs owner) split based on the caller's role — the caller cannot opt out. Treating them like filtered variants would have made sort unavailable on those endpoints entirely. Cost was doubling the query count (10 for users, 14 for scheduled-jobs).

**Panic on unreachable default switch arm.** Originally returned empty success, which would have hidden bugs where a SortSpec key was added without a matching switch arm. Reviewer caught this on Task 6 and the panic pattern was applied to every subsequent endpoint.

**`historicalDefaultSort` is a named constant, not `spec.Default`.** Code reviewer initially suggested replacing the literal `"-created_at"` with `canon` (the resolved spec default). That fix would have silently accepted a legacy cursor paired with `?sort=name`, because canon would equal `"name"` and the mismatch check would pass — but the cursor's actual encoded value is a `created_at` timestamp. Pushed back, kept the literal, extracted it to a named constant with a comment explaining why endpoint-relative substitution is wrong.

**Python SDK scoped out.** Surfaced during brainstorming that the SDK's `list_jobs()` predates pagination entirely — it iterates `response.json()` assuming a bare array, but the server returns the envelope. That's a pre-existing bug independent of sort. Filed as [bug-2026-05-26-python-sdk-list-pagination-envelope](../backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md) and left out of this design.

## Problems Encountered

**Reviewer suggested a buggy fix on Task 3.** Code-quality reviewer flagged that `parsePage`'s literal `"-created_at"` was a hidden coupling and suggested using `canon` instead. Their diagnosis of the smell was correct; their fix would have introduced silent corruption. The skill's `receiving-code-review` guidance ("requires technical rigor, not performative agreement") was load-bearing here — applied the named-constant fix that addressed the underlying clarity concern without taking the bug.

**Tests assumed string comparison would work for timestamps.** The integration tests' `assertSorted` helper compares JSON values as strings. Works for RFC3339 timestamps (lexicographically sortable when UTC-normalized), but a couple of reviewers flagged this as fragile. Left as-is — the seed data confirms it, the property holds for the relevant time range.

**SQL parameter style inconsistency.** Existing queries used both `sqlc.arg(name)::type` (older verbose form) and `@name::type` (modern). Implementers matched whichever style the surrounding file used. The result is a per-file mix that's locally consistent but globally inconsistent. Worth a tidy-up sweep eventually.

**Implementer fixed a `LIMIT @page_limit` → `LIMIT @page_limit + 1` mid-task without flagging it as a change.** This was the existing convention (verified after the fact by checking `internal/store/query/jobs.sql`), so the fix was correct. Caught the discrepancy during review by spot-checking the diff against existing patterns.

**Branch cleanup failed on Windows.** `git worktree remove --force` reported "Permission denied" because the shell session's CWD was anchored in the worktree being removed. The `ExitWorktree` tool is a no-op for harness-created worktrees (not session-created via `EnterWorktree`). Worked around by running git operations from `D:/dev/relay` via PowerShell — the worktree was unregistered from `git worktree list` but the orphan directory remains on disk for the harness to reclaim at session end.

**The 14-task plan was long but the cadence held.** Each task averaged ~2 subagent dispatches (implementer + 1-2 reviewers, plus occasional fix loops). Total ~30 subagent calls. The mechanical similarity of Tasks 6-11 (one per endpoint, each ~250 lines of SQL + handler + integration test) made the per-task review fast — reviewers could spot drift from the established pattern in seconds.

## Known Limitations

- **Filtered jobs variants (`?status=`, `?scheduled_job_id=`) still don't support `?sort=`.** Combining them returns 400 with a clear message; adding the full per-(key, direction) matrix to the filtered branches would have ~doubled the SQL surface for no concrete demand.
- **No multi-key sort.** `?sort=priority,-created_at` is not supported — would require a different cursor scheme. Single sort key + `id` tiebreaker is the only shape.
- **No JOIN-based sort keys.** "Sort jobs by submitter name" needs a `JOIN users` and a cursor scheme that handles cross-table ordering. Out of scope.
- See [`bug-2026-05-27-explain-analyze-sort-indexes`](../backlog/bug-2026-05-27-explain-analyze-sort-indexes.md) — Run EXPLAIN ANALYZE on sort indexes against populated jobs table
- **Python SDK is broken against any pagination-aware server.** See [bug-2026-05-26-python-sdk-list-pagination-envelope](../backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md). Sort will land in the SDK whenever pagination does.
- See [`bug-2026-05-27-sort-flag-cli-help-examples`](../backlog/bug-2026-05-27-sort-flag-cli-help-examples.md) — Add --sort example usage to non-jobs list CLI help

## Open Questions

- See [`idea-2026-05-27-sort-error-message-endpoint-path`](../backlog/idea-2026-05-27-sort-error-message-endpoint-path.md) — Include endpoint path in SortSpec "unsupported sort key" error
- **Should `--sort` ship in `relay-cli` against pre-feature servers with a client-side warning?** Today the flag silently falls back to default ordering when the server ignores the unknown query param. A warning would help users debug "why isn't my sort working." Marginal value; defer until a user asks.
- **Is there demand for sorting filtered jobs variants?** If so, the `?status=running&sort=priority` flow needs ~8 more sqlc queries on the BY-status path. Open question for a future iteration.

## Files Most Touched

- `internal/store/scheduled_jobs.sql.go` — +880 lines; 14 new sqlc methods for the admin and owner-scoped variants
- `internal/store/jobs.sql` / `jobs.sql.go` — +72 / +657 lines; 9 new queries for jobs sort
- `internal/store/users.sql.go` — +650 lines; 10 new queries across active-only and including-archived variants
- `internal/store/workers.sql.go` — +488 lines; 7 new queries including the nullable `last_seen_at` predicates
- `internal/store/reservations.sql.go` — +467 lines; 7 new queries with two more nullable-timestamp predicates
- `internal/api/pagination.go` — cursor format extensions, SortSpec, parseSort, buildPage refactor, historicalDefaultSort constant
- `internal/api/pagination_test.go` — extensive cursor/sort/parsePage unit coverage
- `internal/store/migrations/000013_paginated_sort_indexes.up.sql` — 19 composite indexes
- `internal/mcp/sort_drift_test.go` — bidirectional MCP↔server allowlist drift test
- `README.md` — `#### Configurable sort order` subsection + per-endpoint allowlist table + MCP pointer + CLI examples
- 6 per-endpoint `*_sort_integration_test.go` files — ordering, paginated walk, mismatch rejection, plus null-boundary tests for workers and reservations

## Commit Range

1020dc8d3ce6a64ad59deeec6c14b91d40b35b7a..a3fb27f8df32caf62d97c4651901c1296717a8c3
