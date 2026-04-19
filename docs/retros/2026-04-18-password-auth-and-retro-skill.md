# Session Retro: 2026-04-18 — Password Auth and Retro Skill

## What Was Built

Password authentication across the full stack: a `password_hash` column migration, updated store queries (`CreateUserWithPassword`, `SetPasswordHash`, `GetUserByEmail`), HTTP handlers for `POST /v1/auth/register`, `POST /v1/auth/login`, and `PUT /v1/users/me/password`, and CLI commands `relay register`, `relay login` (updated), and `relay passwd`. Also designed and created the personal `/retro` skill (`~/.claude/skills/retro/SKILL.md`) for writing session retrospectives.

## Key Decisions

- **Token endpoint replaced by register/login/passwd**: The prior `POST /v1/auth/token` stub was removed in favour of three purpose-specific endpoints, making the auth surface explicit.
- **Email enumeration prevention**: `handleLogin` always runs `bcrypt.CompareHashAndPassword` even when the email is not found, using a pre-computed dummy hash via `sync.Once` — prevents timing-based user enumeration.
- **Invite-gated registration**: `handleRegister` validates and atomically redeems an invite token in the same transaction as user creation, preventing races.
- **bcrypt cost override for tests**: `bcryptCost` is a package-level `var` set to `bcrypt.MinCost` in integration tests via `SetBcryptCostForTest()` in `export_test.go`, keeping tests fast without build-tag gymnastics.
- **Retro skill uses git SHAs not dates**: The non-trivial check uses `git log -- docs/retros/` to find the last retro commit SHA, avoiding fragile filename-date comparisons.

## Problems Encountered

- The original `handleLogin` implementation issued tokens before the bcrypt check was wired in, causing compile errors. Fixed by moving the token issuance after the password comparison.
- `relay login` previously POSTed to `/v1/auth/token` (the old stub). Updated to POST to `/v1/auth/login` and prompt for password via `golang.org/x/term` for secure terminal input.
- The code quality review of the retro skill caught that `first..HEAD` excludes the first commit — corrected to `first^..HEAD`.

## Known Limitations

- `relay register` requires an invite token; there is no self-serve registration path.
- The CLI `relay passwd` requires the current password; there is no admin password-reset flow.
- The retro skill cannot be invoked mid-session (skills load at session start); first-run verification was done by manually executing the skill steps.

## Open Questions

- Should `PUT /v1/users/me/password` invalidate existing tokens after a password change?
- Should there be a `DELETE /v1/auth/token` (logout) endpoint?

## What We Did Well

- The brainstorming → spec → plan → subagent-driven execution workflow ran smoothly end-to-end for the retro skill.
- The two-stage review (spec compliance + code quality) caught real issues in the retro skill before it was finalised, particularly the fragile date-comparison logic.
- Password hashing security decisions (bcrypt cost, enumeration prevention, dummy hash) were all made explicitly and documented in CLAUDE.md.

## What We Did Not Do Well

- The retro skill's non-trivial check required a full code-quality review cycle to catch the date-vs-SHA fragility — this could have been caught earlier in the spec.
- The plan's Task 1 Step 4 (commit the plan file) was already done during the planning phase, creating a redundant step that the implementer had to consciously skip.

## Improvement Goals

- When writing skill specs, explicitly spell out the failure mode for each decision point (e.g., "why not dates?") to catch fragility before the review cycle.
- Keep plan steps idempotent or gate them with existence checks so implementers don't need to mentally skip already-completed steps.

## Files Most Touched

- `internal/api/auth.go` — register, login, and change-password handlers; token issuance; enumeration prevention
- `internal/cli/register.go` + `register_test.go` — `relay register` command with invite token prompt
- `internal/cli/passwd.go` + `passwd_test.go` — `relay passwd` command
- `internal/cli/login.go` + `login_test.go` — updated to POST `/v1/auth/login` and prompt for password
- `internal/store/users.sql.go` — `CreateUserWithPassword`, `SetPasswordHash`, `GetUserByEmail` queries
- `internal/api/token.go` + `token_test.go` — deleted (replaced by auth.go)
- `~/.claude/skills/retro/SKILL.md` — new personal skill for session retrospectives

## Commit Range

`88e96cb^..ff0f2ed`
