---
date: 2026-06-21
topic: agent-token-windows-acl
branch: claude/blissful-brown-c7780a
pr: "2026-06-21 / agent-token-windows-acl"
merge: "2026-06-21 / agent-token-windows-acl"
---

# Session Retro: 2026-06-21 - Agent token file world-readable on Windows

**TL;DR:** Closed `bug-2026-06-10-agent-token-windows-acl`. The long-lived agent bearer
token was written with `os.WriteFile(..., 0600)`, which is meaningless on Windows - the
token at `%ProgramData%\relay\token` inherited ProgramData's broad DACL (`BUILTIN\Users`
read), so any local user could steal it. Fixed by applying a protected, non-inherited DACL
on Windows.

## What Was Built

- `internal/agent/credentials_acl_windows.go` (`//go:build windows`): `secureTokenFile`
  applies the SDDL `D:PAI(A;;FA;;;OW)(A;;FA;;;SY)(A;;FA;;;BA)` - full control to Owner
  Rights, LocalSystem, and Administrators only - via `SecurityDescriptorFromString` +
  `SetNamedSecurityInfo` with `PROTECTED_DACL_SECURITY_INFORMATION`. The `P` flag plus the
  PROTECTED info flag sever the inherited ProgramData `BUILTIN\Users` ACE.
- `internal/agent/credentials_acl_unix.go` (`//go:build !windows`): no-op (0600 is correct).
- `internal/agent/credentials.go` `Persist`: calls `secureTokenFile` after the 0600 write,
  hard-fails on an ACL error, and `os.Remove`s the file on failure so a failed Persist
  never leaves a broadly-readable token. Scope is the token file only.
- `//go:build windows` test reads the DACL back and asserts it is protected
  (`SE_DACL_PROTECTED`), has exactly the three expected ACEs, exposes no broad principal,
  and still lets the owner read the token.

## Key Decisions

- **Protected DACL with Owner Rights + SYSTEM + Administrators:** the protected flag is the
  load-bearing part (it severs ProgramData's inherited broad ACE). `OW` (which resolves to
  the well-known Owner Rights SID S-1-3-4, not the literal owner SID) keeps the agent able
  to read its own token regardless of whether it runs as SYSTEM, a service account, or an
  interactive user.
- **File-only scope:** securing the token file fully closes the hole; re-ACLing the shared
  state dir was declined as needless blast-radius expansion (surgical-changes rule).
- **Hard-fail + remove on ACL error:** never leave a broadly-readable token silently.

## Backlog Triage

- None filed. Both review findings were fixed in-iteration (see below).

## Process Note

- The first test used a hermetic `t.TempDir()`, whose inherited DACL is already narrow
  (no broad principals), so the "no broad principal" assertion passed even with the fix
  absent - a vacuous guard. Verification caught it; the fix was to assert the positive
  shape only the applied SDDL produces (the `SE_DACL_PROTECTED` control bit plus the exact
  three-ACE set), which fails when `secureTokenFile` is a no-op. Second time this batch a
  green-but-vacuous test was caught by the review pass - the discriminator must be a
  property that only the fix produces, not one the environment already satisfies.
