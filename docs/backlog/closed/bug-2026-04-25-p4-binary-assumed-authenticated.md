---
title: "`p4` binary assumed on PATH and authenticated"
type: bug
status: open
created: 2026-04-25
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---

# `p4` binary assumed on PATH and authenticated

## Summary
**`p4` binary assumed on PATH and authenticated.** Provisioning P4 tickets is documented as out-of-band operator work; the agent makes no attempt to `p4 login`.

## Resolution (2026-05-02)

Closed by the diagnostics pass:

- `(*perforce.Provider).Preflight` runs `exec.LookPath("p4")` at agent startup. On failure, `cmd/relay-agent/main.go` logs `workspace provider disabled: ...` once and continues running with `provider = nil` so non-source tasks still execute.
- `classifyP4Error` (`internal/agent/source/perforce/diagnostics.go`) rewraps four common stderr patterns ("executable not found", "P4PASSWD invalid", "session expired", "connect to server failed") with operator-facing guidance. Applied at every `Client.*` call site in `perforce.go`.

Per the original design contract (CLAUDE.md), Relay still does not manage P4 credentials — operators provision tickets via `p4 login` out-of-band. This closes the diagnostics gap, not the credential-management question (which remains a non-goal).

Spec: `docs/superpowers/specs/2026-05-02-p4-binary-diagnostics-design.md`.
