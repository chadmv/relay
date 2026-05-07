# Session Retro: 2026-05-06 — User Archive

## What Was Built

Admin-only soft-delete (archive) for users, fully reversible via unarchive. The feature closes the offboarding gap where Relay had no way to remove a user once created.

**Schema:** Migration 000010 adds `archived_at TIMESTAMPTZ` to `users` plus a partial index `WHERE archived_at IS NULL` for efficient active-user queries.

**Store:** Five new sqlc queries (`ArchiveUser`, `UnarchiveUser`, `CountActiveAdmins`, `DeleteUserAPITokens`, `ListUsersIncludingArchived`) plus modifications to `ListUsers` (now filters `WHERE archived_at IS NULL`), `GetUserByEmailPublic`, and `UpdateUserName` to expose the new column.

**API:** Two new admin-only endpoints:
- `POST /v1/users/{id}/archive` — transactional: checks self-archive guard, last-admin guard, archives user, deletes all API tokens in one commit.
- `POST /v1/users/{id}/unarchive` — simple: clears `archived_at`, old tokens remain revoked.
- `GET /v1/users` extended with `?include_archived=true` on both the list and email-lookup branches.
- `handleLogin` now rejects archived users with the same generic "invalid email or password" message, placed **after** the bcrypt compare to preserve timing parity.

**CLI:**
- `relay admin users archive <email-or-id>` / `relay admin users unarchive <email-or-id>` — resolve email to UUID via `GET /v1/users?email=…&include_archived=true`, then POST to the action endpoint.
- `relay admin users list --include-archived` — passes `?include_archived=true` and renders an `ARCHIVED` column.
- `printUserDetail` gains an `Archived:` line.

**Tests:** 14 new integration tests in `users_integration_test.go` covering happy paths, all guards (self-archive, last-admin, already-archived, not-archived), email lock, API token cascade, and list filtering. 1 new integration test in `auth_integration_test.go` for login-message parity across 4 cases. 5 new CLI unit tests.

## Key Decisions

- **Soft-delete only (no hard delete).** `jobs.submitted_by` is `ON DELETE RESTRICT` by design; hard delete is incompatible with preserved job history. Archive is the correct model.
- **Email stays locked.** The `UNIQUE` constraint on `users.email` is intentionally preserved — archived email cannot be re-registered. If email recycling is ever needed, an `archived_email` column can be added additively.
- **Scheduled jobs and running tasks continue.** Archive offboards the human; it does not touch their workloads. Admins disable schedules explicitly.
- **Login check placed after bcrypt.** Moving the `archived_at` guard before bcrypt would create a timing oracle (unknown email → fast; archived + correct password → fast; archived + wrong password → slow). The guard is placed after bcrypt completes so all rejection paths share the same dominant cost.
- **Email lookup uses `include_archived=true` in CLI.** `relay admin users unarchive bob@example.com` must find archived users; `archive` is harmless to allow (server rejects re-archive with 409).
- **`GetUserByEmail` is left unmodified in SQL.** Filtering at the SQL layer on the login path would bypass the timing defense. The check is done in Go after bcrypt.

## Problems Encountered

- **Double-create bug in test setup.** An early version of `TestArchiveUser_HappyPath` called both `seedUser` and `loginUserWithPassword` for the same email, causing a unique-constraint violation. Fixed by using `createAndLoginUser` (creates + logs in in one shot) and then fetching the row with `GetUserByEmail`.
- **Last-admin guard test was non-obvious to construct.** The race the guard protects against (admin A authenticates, then A gets archived before A's archive call) can't happen in normal test flow. Solved by exploiting the fact that `BearerAuth` does not filter on `archived_at` — admin A authenticates, then A's row is directly archived via raw SQL, and A's still-valid token is used to attempt archiving B (the last active admin).

## Known Limitations

- **Hard delete is not implemented.** Users who have never submitted a job or held enrollments could safely be hard-deleted, but no `DELETE /v1/users/{id}` endpoint exists yet.
- **Email reuse requires an additive schema change.** Rehires or shared addresses can't reuse an archived email without an `archived_email` column to free the constraint.
- See [`idea-2026-05-06-audit-log-archive-unarchive`](../backlog/idea-2026-05-06-audit-log-archive-unarchive.md) — Audit log for archive/unarchive actions
- **Worktree removal failed with a Windows permission error.** The git registration was pruned and the branch deleted, but the directory at `.claude/worktrees/laughing-bouman-c92d86` remains on disk as an orphan. It can be removed manually.

## What We Did Well

- **Two-stage review per task** (spec compliance first, then code quality) caught no critical issues. The implementation matched the spec closely throughout.
- **Timing defense preserved correctly.** The bcrypt placement was validated by code review and by the login-parity test covering 4 distinct cases.
- **Subagent-driven development worked smoothly.** 11 tasks, each dispatched to a fresh subagent with precise context from the plan; no BLOCKED or NEEDS_CONTEXT escalations.
- **The plan was detailed enough to drive implementation without ambiguity.** Every task had exact code, file paths, expected test output, and commit messages.

## Files Most Touched

- `internal/api/users_integration_test.go` — 14 new archive/unarchive/list-filter integration tests (+377 lines)
- `internal/store/users.sql.go` — regenerated with 5 new query implementations (+195 lines)
- `internal/api/users.go` — `handleAdminArchiveUser`, `handleAdminUnarchiveUser`, updated `handleListUsers`, updated `userResponse` (+164 lines)
- `internal/cli/admin_users.go` — archive/unarchive subcommands, `--include-archived` flag, `printUserDetail`/`printUsersTable` updates (+105 lines)
- `internal/cli/admin_users_test.go` — 5 new CLI unit tests (+80 lines)
- `internal/api/auth.go` — 5-line archived-user guard in `handleLogin`
- `internal/api/server.go` — 2 new route registrations
- `docs/superpowers/plans/2026-05-05-user-archive.md` — 11-task implementation plan (new file)
- `docs/superpowers/specs/2026-05-05-user-archive-design.md` — full design document (new file)
- `README.md` — REST endpoint table and CLI reference updated

## Commit Range

5556ea8..387272b48f273a6cd692ef1f137ba4db3a6276c1
