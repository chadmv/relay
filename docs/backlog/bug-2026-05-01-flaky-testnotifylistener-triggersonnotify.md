---
title: Fix flaky TestNotifyListener_TriggersOnNotify
type: bug
status: open
created: 2026-05-01
source: noticed during integration test run in self-serve registration session
---

# Fix flaky TestNotifyListener_TriggersOnNotify

## Summary

Pre-existing flaky integration test in the notify listener suite. Surfaces intermittently during `make test-integration` runs but passes in isolation. Confirmed unrelated to self-serve registration work (no diff against master). Likely a real race condition worth triaging — it will continue to generate noise on every integration test run until fixed.

## Repro / Symptoms

- `make test-integration` occasionally fails with a failure in `TestNotifyListener_TriggersOnNotify`
- Running the test in isolation (`go test -tags integration -p 1 ./internal/api/... -run TestNotifyListener_TriggersOnNotify -v`) passes consistently
- Failure appears to be timing/ordering-sensitive (race condition pattern)

## Acceptance / Done When

- `TestNotifyListener_TriggersOnNotify` passes reliably across multiple full `make test-integration` runs
- Root cause documented (race in test setup/teardown, missing synchronization, or goroutine leak)

## Related

- `internal/api/` — notify listener tests
- Retro: `docs/retros/2026-05-01-self-serve-registration.md`
