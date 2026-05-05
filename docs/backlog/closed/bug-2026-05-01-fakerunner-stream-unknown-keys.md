---
title: fakeRunner.Stream silently no-ops on unknown fixture keys
type: bug
status: open
created: 2026-05-01
source: p4client-explicit-flag retro — What We Did Not Do Well
---

# fakeRunner.Stream silently no-ops on unknown fixture keys

## Summary
`fakeRunner.Stream` in `fixtures_test.go` returns `nil` with no output when called with args that have no matching fixture entry, rather than failing the test loudly. This allowed `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` to carry a dead (un-prefixed) sync fixture for four tasks without any test failure; the stale fixture was caught only by code review. `fakeRunner.Stream` should return an error or call `t.Errorf` when an argv has no matching entry, so stale or missing fixture keys surface immediately at the failing test rather than silently passing.

## Repro / Symptoms
- Call `fr.setStream("old key", "output")` then change production code so it calls `Stream` with a different argv.
- The test continues to pass silently — the `Stream` call returns `nil` with no output, and no test failure is reported for the now-dead fixture.
- Expected: the test should fail at the `Stream` call site, pointing directly at the unmatched argv.

## Proposal
In `fakeRunner.Stream`, after the fixture-lookup falls through with no match, record a test failure and return an error:

```go
func (f *fakeRunner) Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error {
    key := strings.Join(args, " ")
    if entry, ok := f.streams[key]; ok {
        // ... existing happy path
        return nil
    }
    // No match — fail loudly so stale fixtures are caught at the call site.
    f.t.Errorf("fakeRunner.Stream: no fixture for args %q (cwd=%q)", key, cwd)
    return fmt.Errorf("fakeRunner: no fixture for %q", key)
}
```

`fakeRunner.Run` has the same silent-nil behaviour and should get the same treatment.

## Acceptance / Done When
- `fakeRunner.Stream` and `fakeRunner.Run` both call `t.Errorf` and return a non-nil error when no fixture matches.
- A test that calls `Stream` with an unregistered key fails immediately at that call, not silently.
- All existing unit tests still pass (all fixture keys are correctly registered).

## Related
- `internal/agent/source/perforce/fixtures_test.go` — `fakeRunner.Stream` and `fakeRunner.Run`
- Retro: `docs/retros/2026-05-01-p4client-explicit-flag.md` § What We Did Not Do Well
