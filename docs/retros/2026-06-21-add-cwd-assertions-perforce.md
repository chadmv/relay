---
date: 2026-06-21
topic: add-cwd-assertions-perforce
branch: claude/sad-feistel-4bc73c
pr: "2026-06-21 / add-cwd-assertions-perforce"
merge: "2026-06-21 / add-cwd-assertions-perforce"
---

# Session Retro: 2026-06-21 - cwd assertions in the perforce unit tests

**TL;DR:** Closed `idea-2026-05-01-add-cwd-assertions-perforce`. The three `TestProvider_*` unit
tests pinned the `-c <client>` argv half of the client-selection contract but left the cwd half
covered only implicitly by the integration test. Added a shared `assertCwdContract` helper and wired
it into all three tests. Test-only, no production change. Autopilot batch item 3.

## What Was Built

- `internal/agent/source/perforce/perforce_test.go` - new `assertCwdContract(t, fr, wsRoot)` helper
  plus one call in each of `TestProvider_PrepareCreatesClientAndSyncs`,
  `TestProvider_UnshelveAndFinalizeRevert`, and `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs`.
  The helper iterates `fr.calls` and requires every workspace-scoped invocation (argv begins with
  `-c <client>`) to run from `wsRoot` and every global invocation (no `-c` prefix) to run with an
  empty cwd, and asserts at least one workspace-scoped call was seen.

## Key Decisions

- **Classify by the `-c` prefix, assert on all calls.** The proposal suggested matching argv on
  `sync`/`change`/`unshelve`/`revert`/`changes` and asserting one representative call. Inspecting the
  Client call sites (`client.go`) showed a cleaner, total discriminator: every workspace-scoped method
  prepends `-c <client>` and is passed `wsRoot`; every global method (`ResolveHead`, `client -o/-i/-d`)
  passes `""`. So the helper asserts the full contract on every call, which is stronger than the
  proposal and not vacuous.
- **`wsRoot` via `h.WorkingDir()`.** The handle's `WorkingDir()` returns the same `wsRoot` the Client
  calls receive as cwd, so no test needed to reconstruct the path.
- **Helper takes `*testing.T`, not `tHelper`.** `tHelper` (the fixture's minimal interface) lacks
  `FailNow`, which `require` needs; the three tests already hold a real `*testing.T`.

## Verification

- Proved the assertions distinguish the contract: temporarily changed the production sync call to pass
  `""` instead of `wsRoot` and confirmed all three tests FAIL with
  `workspace-scoped call [...] must run from wsRoot`; restored the code and re-confirmed green.
- `go build ./...`, `go vet ./internal/agent/source/perforce/...`, and the full perforce package test
  all clean/green. `git status` confirms only the test file changed.
