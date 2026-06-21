---
title: relay admin users list/get and profile update print data output to stderr
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-21
resolution: fixed
priority: medium
source: full-codebase review (2026-06-10)
---

# relay admin users list/get and profile update print data output to stderr

## Summary
`AdminCommand` and `ProfileCommand` pass `stderrWriter()` as the output writer for all subcommands, so the users table from `doAdminUsersList` and `printUserDetail` output go to stderr. `relay admin users list > users.txt` captures nothing and piping to `grep` fails. stderr is right for password prompts, but list/get/update results are data and belong on stdout, consistent with the jobs/workers subcommands.

## Proposal
Route prompts to stderr and results to stdout: give `doAdminUsers*` and the profile result printing an explicit `out io.Writer = os.Stdout` while keeping `readPasswordFn(stderr, ...)` for prompts.

## Related
- `internal/cli/admin.go:16`
- `internal/cli/profile.go:18`
- `internal/cli/admin_users.go:76-78`
- `internal/cli/jobs.go:52`, `internal/cli/workers.go:38` (the correct pattern)

## Resolution
fixed (2026-06-21). `AdminCommand`/`ProfileCommand` now pass `os.Stdout` as the data writer (mirroring the jobs/workers pattern) while password prompts in `doAdminPasswd` and `doAdminUsersCreate` route explicitly to `stderrWriter()`. Admin users list/get tables, profile-update results, and `printUserDetail` (also used by create/update/archive) now land on stdout, so redirect and pipe capture them. Covered by three tests that drive the real command constructors with `os.Stdout`/`os.Stderr` redirected to pipes, proven RED against the old wiring.
