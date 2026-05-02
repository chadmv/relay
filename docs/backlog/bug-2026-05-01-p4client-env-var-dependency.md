---
title: Production agent relies on env-var P4CLIENT but no caller sets it
type: bug
status: open
created: 2026-05-01
source: 2026-05-01 p4d-testcontainer Task 4 fix and final review
---

# Production agent relies on env-var P4CLIENT but no caller sets it

## Summary
`internal/agent/source/perforce/client.go:117` documents that "Caller is responsible for setting P4CLIENT in env before calling" `SyncStream`, but no production caller actually fulfills that contract. `Provider.Prepare` in `perforce.go` calls `cfg.Client.SyncStream` without setting `P4CLIENT` first, so `p4 sync` falls back to whatever `P4CLIENT` happens to be in the agent process's environment (or, on Windows, in the `p4 set` registry). In practice this works only if the operator's machine happens to have a usable `P4CLIENT` set — which won't match the agent's freshly-created `relay_<hostname>_<shortid>` client.

## Repro / Symptoms
- On a machine with no `P4CLIENT` in env or registry: `p4 sync` errors with "no client specified".
- On a machine with a stale or unrelated `P4CLIENT`: `p4 sync` errors with `Client '<name>' unknown` or applies sync to the wrong workspace.
- Surfaced concretely while building the p4d testcontainer (2026-05-01): the integration test had to inject `P4CLIENT` explicitly before `Prepare` to work around this.

## Proposal
Pass the client name explicitly to each p4 invocation that needs one. Two viable shapes:

1. **Add `-c <client>` to the args** in `Client.SyncStream`, `Client.CreatePendingCL`, `Client.Unshelve`, `Client.RevertCL`, `Client.DeleteCL` — anywhere the operation depends on a client. Thread the client name through from `Provider.Prepare`.
2. **Store the active client on the `Client` struct** at construction time and have its methods inject `-c` automatically. Less intrusive at call sites; more global state.

Option 1 is more explicit and easier to reason about under concurrency; recommended.

## Acceptance / Done When
- Production `p4 sync` (and other client-dependent p4 commands) work on a host with no `P4CLIENT` set anywhere.
- The `// Caller is responsible for setting P4CLIENT in env` comment in `client.go` is removed (the gap is closed at the source).
- The integration test in `perforce_integration_test.go` no longer needs to inject `P4CLIENT` (or, if it still does, only as defense-in-depth against host pollution rather than as a workaround for the production gap).

## Related
- `internal/agent/source/perforce/client.go:117` — the comment that documents the unfulfilled contract.
- `internal/agent/source/perforce/perforce.go` — the `Provider.Prepare` site that should set or pass the client.
- `internal/agent/source/perforce/perforce_integration_test.go` — the test workaround that surfaces this gap.
