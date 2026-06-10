---
title: relay admin users list/get and profile update print data output to stderr
type: bug
status: open
created: 2026-06-10
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
