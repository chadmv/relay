# Session Retro: 2026-04-27 — Admin User List Endpoint

## What Was Built

Two features shipped as a single branch (`claude/condescending-cannon-924b41`):

**1. Password-change/admin-reset transactional fix (`f92a281`).** Prior to this session, `handleChangePassword` and `handleAdminPasswordReset` each ran their DB writes non-transactionally — password update and session revocation could diverge if one failed mid-way. Both handlers were wrapped in `pool.BeginTx` / `q.WithTx(tx)` so the writes are atomic. Integration tests exercise the rollback path using a `BEFORE DELETE` trigger that forces the session-revocation step to fail, confirming the password was not updated. (This was a carry-over bug closed from the previous session's backlog.)

**2. Admin user list/lookup endpoint (`7744f6f..cc99d08`).** Added `GET /v1/users` (admin-only) with optional `?email=<exact-match>` filter, plus `relay admin users list` and `relay admin users get <email>` CLI subcommands.

- Store layer: new `ListUsers :many` sqlc query selecting `id, email, name, is_admin, created_at` explicitly (no `password_hash`), regenerated via `make generate`.
- API handler (`internal/api/users.go`): private `userResponse` struct as an explicit allowlist; email-filter path reuses `GetUserByEmail` and maps to the same struct; both paths return a JSON array (never null).
- Route registration: `GET /v1/users` added under `auth(admin(...))` in `server.go`.
- Integration tests (5): admin sees all, non-admin 403, email hit (single-element array), email miss (empty array), ordering by created_at.
- CLI: `relay admin users list` (tabwriter table) and `relay admin users get <email>` (labeled key-value output). Both share the `userListItem` struct and route through `cfg.NewClient().do()`.
- CLAUDE.md and backlog item closed.

## Key Decisions

**Reuse `GetUserByEmail` for the email-filter path instead of a new parameterized query.** The tradeoff is that `GetUserByEmail` fetches `password_hash` from the DB unnecessarily (it's discarded in the handler). The security boundary is maintained by the `userResponse` struct regardless. A dedicated `GetUserByEmailPublic` query would be marginally more efficient but adds sqlc surface; deferred.

**`userResponse` as a private struct, not aliasing the store row.** `store.User` includes `PasswordHash`. If the handler marshaled a raw `store.User`, any future field addition to that struct would automatically appear in the API response. The private struct is an explicit allowlist — new fields only appear if deliberately added to `userResponse`.

**Exact-match `?email=` only, no substring search.** Keeps the server implementation minimal. Substring search can be added later without a breaking change. Admins can always `relay admin users list | grep` for now.

**`ORDER BY created_at ASC` with no secondary sort key.** Bootstrap admin appears first. Stable under normal conditions; a future `ORDER BY created_at, id` would be fully deterministic but was deemed unnecessary at current scale.

**Subagent-driven development for the 8-task plan.** Each task was dispatched as a fresh subagent, followed by spec-compliance then code-quality review. One task (Task 4 — CLI dispatcher stubs) was committed to the main repo instead of the worktree by the implementer subagent, and had to be cherry-picked. Otherwise the pipeline worked cleanly.

## Problems Encountered

**Task 4 subagent wrote to the main repo instead of the worktree.** The implementer's report showed the correct commit SHA but the spec reviewer found the files absent in the worktree. Root cause: the subagent path `/d/dev/relay/internal/cli/` (bash-style absolute path) resolved to the main repo, not the worktree at `D:\dev\relay\.claude\worktrees\condescending-cannon-924b41`. Fixed by cherry-picking the commit into the worktree: `git cherry-pick df2ad95`.

**Line-ending noise from `make generate` on Windows.** `sqlc generate` touched all `internal/store/*.go` files with LF line endings; git saw them as modified (CRLF vs LF). Only `users.sql.go` was staged in the commit; the others were restored with `git checkout -- internal/store/` before the retro to avoid noise.

## Known Limitations

- See [`idea-2026-04-27-getuserbyemail-fetches-password-hash`](../backlog/idea-2026-04-27-getuserbyemail-fetches-password-hash.md) — GetUserByEmail fetches password_hash unnecessarily on email filter path
- `TestListUsers_OrderedByCreatedAt` relies on `loginAsAdmin` taking long enough (bcrypt + HTTP round-trip) to guarantee a distinct `created_at` from the first `seedUser`. A `time.Sleep(10ms)` was added after the fix suggested by the final reviewer, making it structurally sound.

## What We Did Well

- **Security at two layers.** `ListUsers` SQL selects only public columns (so `ListUsersRow` has no `PasswordHash`), and the `userResponse` struct re-enforces the boundary for the `GetUserByEmail` path. Defense in depth with no extra code.
- **TDD discipline throughout.** Failing tests committed before implementation for every CLI task. The spec reviewer confirmed TDD ordering from the diff in each case.
- **Two-stage review caught real issues.** The final cross-task review flagged the `TestListUsers_OrderedByCreatedAt` flakiness risk (missing sleep between admin creation and first `seedUser`), which the per-task reviews had not caught.
- **Cherry-pick recovery was clean.** When the Task 4 subagent landed work in the wrong repo, identifying the commit and cherry-picking it took two commands and didn't disrupt the workflow.

## What We Did Not Do Well

- **Subagent working-directory confusion (recurring).** This is the second session where a subagent committed to the main repo instead of the worktree. The implementer prompt template says "Work from: `<path>`" but bash-style paths on Windows resolve ambiguously. Future prompts should include an explicit `cd <worktree-path> &&` prefix on every bash command block, or a verification step at the top.
- **`make generate` side effects not scoped.** The Task 1 subagent ran `make generate`, which regenerated all store files with LF endings. Only `users.sql.go` was committed; the others drifted until cleaned up at retro time. The implementer prompt should explicitly say "stage only the files listed in the task" or run `git diff --stat` before committing to catch unintended side-effects.

## Improvement Goals

- Add a working-directory verification step to all implementer prompts: `git rev-parse --show-toplevel` should match the worktree path before any writes.
- After running `make generate`, always run `git diff --stat` and stage only the expected files; restore the rest with `git checkout --` before committing.

## Files Most Touched

- `internal/api/users.go` — new handler with `userResponse` struct and `handleListUsers`
- `internal/api/users_integration_test.go` — 5 integration tests for the endpoint
- `internal/api/auth.go` — password-change and admin-reset handlers wrapped in transactions
- `internal/cli/admin_users.go` — `doAdminUsers`, `doAdminUsersList`, `doAdminUsersGet`, `userListItem`, `printUsersTable`
- `internal/cli/admin_users_test.go` — 6 CLI unit tests (2 list + 4 get)
- `internal/store/query/users.sql` — `ListUsers` query added
- `internal/store/users.sql.go` — regenerated with `ListUsersRow` struct
- `internal/cli/admin.go` — `users` case added to `doAdmin` dispatcher
- `docs/superpowers/plans/2026-04-27-admin-user-list-endpoint.md` — 8-task implementation plan
- `docs/superpowers/specs/2026-04-27-admin-user-list-endpoint-design.md` — design spec

## Commit Range

`9dfa015..cc99d08`
