# Diagnosable agent auth failures for stale token files

- Date: 2026-06-05
- Status: approved
- Backlog item: `docs/backlog/bug-2026-06-03-agent-stale-token-misleading-error.md`

## Problem

When a leftover `<state-dir>/token` file exists (e.g. from a previous agent run, or
after the server's database was recreated), the agent reconnects with that stale
token and never reads `RELAY_AGENT_ENROLLMENT_TOKEN` - by design (the
`if !creds.HasAgentToken()` gate at `cmd/relay-agent/main.go:42`). The server
rejects the stale token and the agent exits with `agent: authentication failed`
followed by `token may have been revoked; exiting` (the live string uses an em
dash before "token"). That message points operators at revocation when the real cause is a stale local
file silently shadowing the enrollment token they just set. The no-fallback
behavior is intentional and correct (`docs/superpowers/specs/2026-04-22-security-hardening-pass-2-design.md`);
the gap is purely diagnosability. The log line also uses an em dash, which
violates the project's no-em-dash house rule.

## Scope

Messaging-only. No change to the no-fallback security model: the enrollment token
is still never sent while a stored agent token exists.

## Design

Two pure helper functions in a new `internal/agent/messages.go`, each unit-tested,
with thin call sites.

### Component 1: startup warning when an enrollment token is shadowed

New exported helper (called from `package main`):

```go
// EnrollmentIgnoredWarning returns a warning when an enrollment token is set but
// will be ignored because a stored agent token already exists. "" = no warning.
func EnrollmentIgnoredWarning(hasAgentToken, enrollmentTokenSet bool, tokenPath string) string
```

When both bools are true it returns:

> `relay-agent: RELAY_AGENT_ENROLLMENT_TOKEN is set but ignored because a stored agent token already exists at <path>; delete that file to re-enroll`

Otherwise it returns `""`.

Call site in `cmd/relay-agent/main.go`, immediately after the existing credential
block (the existing `if !creds.HasAgentToken()` block is untouched):

```go
if w := agent.EnrollmentIgnoredWarning(
    creds.HasAgentToken(),
    os.Getenv("RELAY_AGENT_ENROLLMENT_TOKEN") != "",
    creds.TokenFilePath(),
); w != "" {
    log.Print(w)
}
```

### Component 2: tailored `Unauthenticated` exit message

New unexported helper in the same file:

```go
// authFailureMessage returns the exit log for an Unauthenticated registration
// failure, tailored to which credential was in use.
func authFailureMessage(hasAgentToken bool, tokenPath string, hasEnrollmentToken bool) string
```

Three branches, all hyphens (the em dash is removed):

- Stored agent token rejected (`hasAgentToken`):
  > `agent: authentication failed - stored agent token at <path> was rejected; if this agent was re-provisioned, delete that file and set RELAY_AGENT_ENROLLMENT_TOKEN to re-enroll; exiting`
- Enrollment token rejected (`!hasAgentToken && hasEnrollmentToken`):
  > `agent: authentication failed - enrollment token was rejected (invalid, expired, or already used); exiting`
- Token-less auto-enroll rejected (neither):
  > `agent: authentication failed - token-less auto-enroll was rejected; the server must have RELAY_ALLOW_AUTO_ENROLL enabled; exiting`

The three branches are distinguishable at the failure point because an enrollment
token is only persisted *after* a successful register: a failed register with an
enrollment token still reports `HasAgentToken() == false` and
`EnrollmentToken() != ""`. The third branch's wording mirrors the existing
startup log at `cmd/relay-agent/main.go:47` for consistency.

Call site at `internal/agent/agent.go:73-77` replaces the em-dash line entirely:

```go
if status.Code(err) == codes.Unauthenticated {
    log.Print(authFailureMessage(
        a.creds.HasAgentToken(),
        a.creds.TokenFilePath(),
        a.creds.EnrollmentToken() != "",
    ))
    a.runnerWG.Wait()
    return
}
```

## Testing

New `internal/agent/messages_test.go`, table-driven (white-box, `package agent`):

`EnrollmentIgnoredWarning`:
- stored token present + enrollment env set -> warning naming the path
- stored token present, no enrollment env -> `""`
- no stored token -> `""`

`authFailureMessage`:
- stored token -> names path + remedy + "exiting"
- enrollment token (no stored) -> "enrollment token was rejected" message
- token-less (neither) -> "auto-enroll was rejected" message
- all three assert no em dash (`-`, not the em dash character)

Both helpers are pure `string` functions, so `make test` covers all acceptance
criteria except the manual end-to-end repro.

## Acceptance / Done When

- Starting the agent with both a stored token file and `RELAY_AGENT_ENROLLMENT_TOKEN`
  set emits a startup warning naming the token file path.
- The `Unauthenticated` exit message names the token file path and the remedy when
  a stored agent token was rejected, and is accurate for the enrollment-token and
  auto-enroll cases.
- The no-fallback security behavior is preserved (enrollment token still not used
  while a stored token exists).
- The em dash in the agent auth-failure log line is replaced with a hyphen.
- Backlog item `git mv`'d to `docs/backlog/closed/`.

## Out of scope (preserved)

No change to the `HasAgentToken()` gate or the enrollment flow.
