---
title: Agent token file is world-readable on Windows
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-21
resolution: fixed
priority: medium
source: full-codebase review (2026-06-10)
---

# Agent token file is world-readable on Windows

## Summary
`credentials.go` writes the long-lived agent token with `os.WriteFile(..., 0600)`, but the 0600 mode carries no ACL meaning on Windows. The token at `C:\ProgramData\relay\token` inherits ProgramData's ACL, which typically grants all local users read access, so any local user can steal the agent bearer token. The CLAUDE.md note about 0600 holds on Unix only.

## Proposal
On Windows, create `%ProgramData%\relay` with an explicit DACL (SYSTEM + Administrators only) via `golang.org/x/sys/windows` security attributes, or at minimum document that operators must harden the state dir with `icacls`.

## Related
- `internal/agent/credentials.go:60`
- `cmd/relay-agent/main.go:122-128`

## Resolution
fixed - added `secureTokenFile(path)` with a build-tag split (matching `proctree_*.go`). On
Windows it applies a PROTECTED (non-inherited) DACL via the SDDL
`D:PAI(A;;FA;;;OW)(A;;FA;;;SY)(A;;FA;;;BA)` - full control to Owner Rights (S-1-3-4),
LocalSystem, and Administrators only - using `SecurityDescriptorFromString` +
`SetNamedSecurityInfo` with `PROTECTED_DACL_SECURITY_INFORMATION`, which severs the
inherited ProgramData `BUILTIN\Users` ACE that was the exposure. On non-Windows it is a
no-op (0600 is correct there). `Persist` hard-fails on an ACL error and `os.Remove`s the
file so a failed Persist never leaves a broadly-readable token behind. Scope is the token
file only (the worker-ID file is a public identifier, left at 0644). Verified on Windows
with a `//go:build windows` test that reads the DACL back and asserts it is protected
(`SE_DACL_PROTECTED`), has exactly the three expected ACEs, exposes no broad principal
(Everyone / Authenticated Users / BUILTIN\Users), and still lets the owner read the token;
proven load-bearing (the test fails when the helper is a no-op). Plan:
`docs/superpowers/plans/2026-06-20-agent-token-windows-acl.md`.
