# Session Retro: 2026-05-01 — p4client-explicit-flag

## What Was Built

Closed a latent production bug where every workspace-scoped `p4` invocation in the Perforce source provider fell back to the `P4CLIENT` environment variable rather than naming the active client explicitly. On an agent host with no `P4CLIENT` set, `p4 sync` would error with "no client specified"; with a stale `P4CLIENT` it would silently operate on the wrong workspace.

The fix threads `(cwd, client)` through six `Client` methods and the `Runner` interface:

- **`Runner` interface** gained a `cwd string` parameter on both `Run` and `Stream`. `execRunner` sets `cmd.Dir = cwd` when non-empty.
- **Six workspace-scoped methods** now prepend `-c <client>` to every p4 argv: `SyncStream`, `CreatePendingCL`, `Unshelve`, `RevertCL`, `DeleteCL`, `PendingChangesByDescPrefix`.
- **Three server-global methods** (`ResolveHead`, `CreateStreamClient`, `DeleteClient`) are unchanged — they pass `""` for cwd and don't need `-c`.
- **`recoverOrphanedCLs`** signature widened to `(ctx, wsRoot, clientName string)`.
- **`perforceHandle.Finalize`** uses `h.workspaceDir` and `h.clientName` stored at `Prepare` time.
- **`expectedClientName` helper** moved from the integration-only `p4d_container_test.go` to `fixtures_test.go` (no build tag) so unit tests can compute deterministic client names.
- **Integration test** `t.Setenv("P4CLIENT", …)` workaround removed; the test continues to pass, empirically confirming the fix.

Delivered via full brainstorm → spec → plan → subagent-driven-development cycle with two-stage review per task and a final branch code review.

## Key Decisions

**Explicit `-c` flag over P4CONFIG.** We compared `-c <client>` in argv against writing a `.p4config` file per workspace. The explicit flag wins: no new env vars, no on-disk artifact, visible in argv (and therefore in test assertions and logs), and it applies uniformly to cross-workspace operations like `ResolveHead` that have no workspace directory. P4CONFIG would require `P4CONFIG` in env + the file on disk + cwd always set correctly — three dependencies instead of one.

**Add `cwd` to `Runner` now, not later.** The cwd threading doesn't affect which client p4 selects (p4 doesn't use cwd for client resolution unless P4CONFIG is active), but it removes a hidden global and makes operator copy-paste from logs reproducible. The call sites that need cwd are exactly the six we were already touching, so the coupling is intentional.

**`PendingChangesByDescPrefix` dual `-c` pattern.** `p4 -c <client> changes -c <client> -s pending -l` uses `-c` twice: the first is p4's global client-selection flag, the second is the `changes` subcommand's "filter by client" option. The code comment at `client.go:179` explicitly documents this so future readers don't "fix" it.

**TDD discipline throughout.** Each task updated the test fixture to expect the new `-c <client>` prefix first, watched the test fail, then updated production code. One plan oversight (Task 4's `change -i` found-check update was missed in the test delta spec) was caught by the spec reviewer and corrected before moving on.

## Problems Encountered

**Plan oversight — Task 4 `change -i` assertion gap.** The plan's Task 4 said the `found()` assertion for `change -i` in `TestProvider_UnshelveAndFinalizeRevert` would be updated "later." But `CreatePendingCL`'s argv changed in Task 4 itself, so the assertion needed updating immediately. The spec reviewer caught it; fixed in a follow-up commit `a0dc03c` before Task 5 started.

**Stale sync fixture in crash-recovery test.** After Task 3 prefixed the sync fixture key with `-c <client>`, `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` still held the un-prefixed key. The `fakeRunner.Stream` silently returns nil for unknown keys, so the test passed (with dead fixture) rather than failing loudly. The spec reviewer for Task 6 noticed the inconsistency; fixed in Task 8 (`fr.setStream("-c "+clientName+" sync …")`).

**Session continuity across context compaction.** The implementation spanned multiple sessions with a context-window compaction in the middle. The summary incorrectly described the integration test as still containing the `P4CLIENT` workaround when the file was already clean (the edit had been applied before compaction but after the summary was written). A quick `git diff HEAD` at session resume immediately clarified the true state.

## What We Did Well

**Spec quality.** The design spec was detailed enough that the implementation required almost no judgment calls — method signatures, caller wiring table, fake-runner key policy, and the exact test assertion to add were all pre-specified.

**Two-stage review caught the only real gap.** The spec-compliance reviewer caught the Task 4 assertion omission. Without that gate it would have been a silent correctness gap in the test suite (test passing but asserting wrong shape).

**Integration test as empirical proof.** Removing `t.Setenv("P4CLIENT", …)` and watching the integration test continue to pass against a live p4d container is a clean, unforgeable signal that the bug is genuinely closed.

**Methodical commit sequence.** Eleven task commits plus two cleanup commits make the change history easy to bisect. Each commit is a working state (tests pass after every step).

## What We Did Not Do Well

**Plan test-delta spec was imprecise.** The plan said the `change -i` found-check in `TestProvider_UnshelveAndFinalizeRevert` would be updated "in a later task," but the argv change that made it wrong happened in Task 4. The plan should have listed every assertion that changes at each step rather than deferring. The two-stage review caught it, but the review loop is a safety net, not a substitute for a tight plan.

See [`bug-2026-05-01-fakerunner-stream-unknown-keys`](../backlog/bug-2026-05-01-fakerunner-stream-unknown-keys.md) — `fakeRunner.Stream` silently no-ops on unknown fixture keys, allowing stale fixtures to hide for multiple tasks without a test failure.

**Session summary inaccuracy after compaction.** When the session was compacted, the auto-summary described the integration test as still containing the `P4CLIENT` workaround when the file had already been edited. This required a quick state check at resume rather than being able to trust the summary cold. The mitigation (run `git diff HEAD` at resume) worked fine, but the root problem — summaries can lag the actual file state — is worth keeping in mind.

## Improvement Goals

- See [`bug-2026-05-01-fakerunner-stream-unknown-keys`](../backlog/bug-2026-05-01-fakerunner-stream-unknown-keys.md) — Make `fakeRunner.Stream` fail loudly on unknown fixture keys.
- See [`idea-2026-05-01-add-cwd-assertions-perforce`](../backlog/idea-2026-05-01-add-cwd-assertions-perforce.md) — Lock in cwd contract in unit tests.
- **Plan test-delta tables.** For implementation plans that modify existing test assertions, list the exact `before → after` delta for every assertion at the task where the production change occurs, not at a later "catch-up" task.

## Known Limitations

- See [`idea-2026-05-01-add-cwd-assertions-perforce`](../backlog/idea-2026-05-01-add-cwd-assertions-perforce.md) — Add cwd assertions to the three perforce unit tests

## Open Questions

- See [`idea-2026-05-01-add-cwd-assertions-perforce`](../backlog/idea-2026-05-01-add-cwd-assertions-perforce.md) — Add cwd assertions to the three perforce unit tests

## Files Most Touched

- `internal/agent/source/perforce/client.go` — Runner interface widened; six Client methods updated with `(cwd, client)` params and `-c <client>` argv prefix
- `internal/agent/source/perforce/perforce_test.go` — all three unit tests updated: fixture keys prefixed, `expectedClientName` wired, explicit sync-argv assertion added
- `internal/agent/source/perforce/perforce.go` — `recoverOrphanedCLs` widened; `Prepare` and `Finalize` thread wsRoot + clientName through all client calls
- `internal/agent/source/perforce/fixtures_test.go` — `runCall` gains `cwd` field; `fakeRunner.Run`/`.Stream` record cwd (not in lookup key); `expectedClientName` migrated here
- `internal/agent/source/perforce/perforce_integration_test.go` — `t.Setenv("P4CLIENT", …)` workaround and comment block removed
- `internal/agent/source/perforce/p4d_container_test.go` — `expectedClientName` helper removed (now in fixtures_test.go)
- `docs/superpowers/specs/2026-05-01-p4client-explicit-flag-design.md` — full design doc
- `docs/superpowers/plans/2026-05-01-p4client-explicit-flag.md` — implementation plan (1 100-line task breakdown)
- `docs/backlog/closed/bug-2026-05-01-p4client-env-var-dependency.md` — backlog item closed with resolution note

## Commit Range

e545b94..7cd160f
