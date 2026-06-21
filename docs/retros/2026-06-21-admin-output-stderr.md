---
date: 2026-06-21
topic: admin-output-stderr
branch: claude/distracted-allen-9c27c1
pr: "2026-06-21 / admin-output-stderr"
merge: "2026-06-21 / admin-output-stderr"
---

# Session Retro: 2026-06-21 - admin/profile data to stdout

**TL;DR:** Closed `bug-2026-06-10-admin-output-stderr`. `AdminCommand`/`ProfileCommand` wired
`stderrWriter()` as the single output writer for all subcommands, so `relay admin users list/get`
tables and `relay profile update` results went to stderr - `> users.txt` captured nothing and
`| grep` failed. Routed data output to `os.Stdout` (mirroring the jobs/workers pattern) while
keeping password prompts explicitly on `stderrWriter()`. Autopilot batch, item 1 of 7.

## What Was Built

- `internal/cli/admin.go` - `AdminCommand` passes `os.Stdout` as the data writer (was
  `stderrWriter()`); both `readPasswordFn` calls in `doAdminPasswd` now pass `stderrWriter()`
  explicitly so prompts stay off stdout.
- `internal/cli/admin_users.go` - the two `readPasswordFn` calls in `doAdminUsersCreate` route to
  `stderrWriter()`; the resulting `printUserDetail` still prints to the data writer (stdout).
- `internal/cli/profile.go` - `ProfileCommand` passes `os.Stdout` (was `stderrWriter()`).
- `internal/cli/admin_output_test.go` - three tests (`TestAdminCommand_UsersList_DataOnStdout`,
  `TestAdminCommand_UsersGet_DataOnStdout`, `TestProfileCommand_Update_DataOnStdout`) drive the real
  `AdminCommand()`/`ProfileCommand()` constructors with `os.Stdout`/`os.Stderr` redirected to pipes,
  asserting data lands on stdout.

## Key Decisions

- **Test through the command constructors, not the inner `doAdmin`/`doProfile`.** The existing
  tests inject a writer directly, so they could never catch a wiring bug in the constructor - which
  is exactly where the bug lived. The new tests redirect the real `os.Stdout`/`os.Stderr`, the only
  way to exercise the production wiring. Proven RED by reverting the two constructor lines (empty
  captured stdout) and GREEN after.
- **Fix all the data paths, not just the two named in the item.** create/update/archive also route
  `printUserDetail` through the same writer; moving them to stdout is correct and consistent (data
  is data), so the writer rewiring benefits them identically rather than leaving an inconsistent split.

## Process Note

- Trivial CLI output-stream change: no DB, no concurrency, no gRPC, none of the six invariants
  implicated, so verification was a single combined code-review rather than the relay-verify fan-out.
  The reviewer empirically re-confirmed the tests RED by reverting the wiring.
- Worktree-path discipline held: engineer and reviewer both operated against the worktree path; the
  commit landed on the conductor branch.

## Backlog Triage

- No new items. The reviewer noted a pre-existing em dash in an unrelated error string
  (`internal/cli/admin.go`), out of scope for this diff and left untouched.
