---
title: Agent token file is world-readable on Windows
type: bug
status: open
created: 2026-06-10
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
