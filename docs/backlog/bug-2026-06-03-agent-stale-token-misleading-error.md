---
title: Agent logs misleading "token may have been revoked" when a stale token file shadows the enrollment token
type: bug
status: open
created: 2026-06-03
priority: medium
source: surfaced while manually testing the web Workers page; enroll appeared to fail but root cause was a leftover state-dir token file
---

# Agent logs misleading "token may have been revoked" when a stale token file shadows the enrollment token

## Summary
When a leftover `<state-dir>/token` file exists (e.g. from a previous agent run, or after the server's database was recreated), the agent reconnects with that stale token and never reads `RELAY_AGENT_ENROLLMENT_TOKEN` - by design (`cmd/relay-agent/main.go:42`, the `if !creds.HasAgentToken()` guard). The server rejects the stale token and the agent exits with `authentication failed - token may have been revoked; exiting`. That message points operators at revocation when the real cause is a stale local file silently shadowing the enrollment token they just set.

## Context
The no-fallback-to-enrollment behavior is intentional and correct (security-hardening-pass-2 spec: prevents silent re-enrollment after an admin revokes a worker). The gap is purely diagnosability: the failure mode "stale token file present + operator set a fresh enrollment token that is being ignored" is invisible from the log line. Discovered 2026-06-03 when a fresh enrollment failed because `C:\ProgramData\relay\token` (and `worker-id`) lingered from 2026-04-22, while a freshly created Postgres DB had no matching worker row.

## Repro / Symptoms
1. Run the agent once so it persists `<state-dir>/token` (Windows default `C:\ProgramData\relay`).
2. Recreate the server DB (or revoke/delete the worker) so the persisted token no longer resolves.
3. Set `RELAY_AGENT_ENROLLMENT_TOKEN` to a fresh, valid enrollment token and start the agent.
4. Observed: `agent: authentication failed - token may have been revoked; exiting`. The new enrollment token is never sent.
5. Expected: a message that explains an existing agent token file is being used (and the enrollment token ignored), naming the file path so the operator knows to remove it to re-enroll.

## Proposal
- When the agent holds a stored agent token AND `RELAY_AGENT_ENROLLMENT_TOKEN` is also set in the environment, log a warning at startup that the enrollment token is being ignored because an agent token already exists at `<tokenFilePath>` (delete it to re-enroll). The credential file path is available via `Credentials.TokenFilePath()`.
- On a `codes.Unauthenticated` failure while reconnecting with a stored token, include the token file path in the exit message so the remedy is obvious (e.g. "stored agent token at `<path>` was rejected; if this agent was re-provisioned, delete that file and set RELAY_AGENT_ENROLLMENT_TOKEN to re-enroll").
- Keep the no-silent-re-enrollment behavior unchanged; this is messaging only.
- Bonus cleanup: the existing log string in `internal/agent/agent.go:74` uses an em dash, which also violates the project's no-em-dash house rule - replace with a hyphen while touching that line.

## Acceptance / Done When
- Starting the agent with both a stored token file and `RELAY_AGENT_ENROLLMENT_TOKEN` set emits a startup warning naming the token file path.
- The `Unauthenticated` exit message names the token file path and the remedy.
- The no-fallback security behavior is preserved (enrollment token still not used while a stored token exists).
- The em dash in the agent auth-failure log line is replaced with a hyphen.

## Related
- `cmd/relay-agent/main.go:42-50` - the `HasAgentToken()` gate that skips reading the env token.
- `internal/agent/credentials.go` - `LoadCredentials`, `TokenFilePath()`, `HasAgentToken()`.
- `internal/agent/agent.go:73-77` - the `Unauthenticated` handling and the misleading log line.
- `docs/superpowers/specs/2026-04-22-security-hardening-pass-2-design.md` - the intentional no-fallback design.
