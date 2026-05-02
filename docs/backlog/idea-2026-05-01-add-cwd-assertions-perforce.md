---
title: Add cwd assertions to the three perforce unit tests
type: idea
status: open
created: 2026-05-01
source: p4client-explicit-flag retro — Known Limitations
---

# Add cwd assertions to the three perforce unit tests

## Summary
`fakeRunner` already records `cwd` per call on `runCall` structs in `fixtures_test.go`, but `TestProvider_PrepareCreatesClientAndSyncs`, `TestProvider_UnshelveAndFinalizeRevert`, and `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` none assert that workspace-scoped invocations receive `wsRoot` as cwd or that global invocations receive `""`. The `-c <client>` argv assertions pin the client-selection contract, but the cwd contract is only tested implicitly via the integration test. Adding explicit cwd assertions would fully lock in both halves of the contract introduced by the p4client-explicit-flag fix.

## Proposal
For each of the three unit tests, iterate `fr.calls` (or equivalent) and assert that calls whose argv contains `"sync"`, `"change"`, `"unshelve"`, `"revert"`, or `"changes"` have `cwd == wsRoot`, while calls like `"changes -m1"` (ResolveHead) have `cwd == ""`. Pattern:

```go
for _, c := range fr.calls {
    if len(c.args) >= 3 && c.args[2] == "sync" {
        require.Equal(t, wsRoot, c.cwd, "sync must run from workspace dir")
    }
}
```

## Acceptance / Done When
- Each of the three `TestProvider_*` unit tests in `perforce_test.go` asserts cwd on at least one representative workspace-scoped call.
- `make test` passes.

## Related
- `internal/agent/source/perforce/fixtures_test.go` — `runCall.cwd` field
- `internal/agent/source/perforce/perforce_test.go` — the three tests to update
- Retro: `docs/retros/2026-05-01-p4client-explicit-flag.md` § Known Limitations
