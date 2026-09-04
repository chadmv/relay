---
title: Perforce virtual and import+ remap streams fail at prepare because relay addresses p4 by the stream-name depot path
type: bug
status: closed
closed: 2026-09-04
resolution: fixed
created: 2026-09-03
priority: high
source: SDNM fork divergence analysis (relay_updates.md, PR-1), evaluated 2026-09-03; reproduced in production against a real p4d
---

# Perforce virtual and import+ remap streams fail at prepare because relay addresses p4 by the stream-name depot path

## Summary

`Provider.Prepare` resolves head and syncs using `e.Path` directly, a depot path under the stream
name. For a virtual stream or an `import+` remap, the stream name is not real storage: the depot
side of the client's view is the remap source, so `p4 sync //depot/<remap>/...` fails with
"file(s) not in client view" and the task fails at prepare. Relay documents stream support without
qualification, so this is a correctness bug, not a studio preference. The fix is to address p4 by
client-relative paths (`//<client>/<rel>`), which resolve for every stream type.

## Repro / Symptoms

Create a virtual child stream whose view remaps a path from its parent, submit a task with
`source.stream` set to it and `sync[0].path` under it, and watch the prepare fail with the p4
message above. Today the task also shows empty logs
([[bug-2026-09-03-prepare-failure-error-message-is-discarded]]).

## Context

The fork's fix is correct in mechanism and introduces one hazard it documents with a TODO: because
client creation must now precede head resolution, and head resolution can fail, an early return
leaves a client spec and directory that the registry does not know about. The sweeper scans only
the registry, so the orphan is never reclaimed unless the same stream is prepared again. The fix
below removes that hazard without touching the sweeper.

The reorder is forced by a genuine cycle: `ws.Acquire` needs the baseline hash, the hash needs the
resolved revisions, and resolving a revision on a remap stream needs the client to exist.

## Proposal

All in `internal/agent/source/perforce/`.

1. **`toClientPath(clientName, stream, depotPath)`**: strip the stream prefix (jobspec validation
   guarantees the path sits under it), map an empty remainder to `/...`, return
   `//<client><rel>`. Keep it free of any exclusion handling; that belongs to
   [[idea-2026-09-03-sync-spec-exclusion-paths-design]].
2. **`Client.ResolveHead(ctx, cwd, client, path)`** runs `p4 -c <client> changes -m1 <path>#head`
   from `cwd`. This turns head resolution from a global call into a workspace-scoped one, which
   changes the contract `assertCwdContract` in `perforce_test.go` states in its comment. Update
   the comment deliberately; the assertion itself already accepts any `-c` call from `wsRoot`.
3. **Reorder `Prepare`.** Before head resolution: look up or allocate `shortID`, compute `wsRoot`
   and `clientName`, `MkdirAll`, `CreateStreamClient` (idempotent), and **immediately
   `reg.Upsert` plus `reg.Save` with an empty `BaselineHash`** when the entry was not found. The
   registry already accepts an empty baseline on first registration, and registering here is what
   closes the orphan: an early return after this point leaves a registered workspace the sweeper's
   age pass reclaims like any other. The later `!found` Upsert block becomes redundant and is
   deleted. Sync specs become `toClientPath(...) + rev`; `resolved` and `BaselineHash` stay keyed
   on the depot-form `e.Path`, so existing registries remain valid and no workspace re-syncs on
   upgrade.
4. **Re-derive the eviction ordering argument.** Creation and registration now precede
   `ws.Acquire` and the post-Acquire eviction re-check. Enumerate what `EvictWorkspace` or a sweep
   can do between the Upsert and the Acquire, and confirm the re-check after Acquire is still the
   guard that keeps a sync out of a workspace being deleted. Write the argument in the commit
   message. If a new interleaving exists, add it to the `sweeper_claim_test` family.
5. **Cost**: one `client -o` plus `client -i` per Prepare instead of per first use. Acceptable at
   task cadence; optionally skip the `client -i` when the fetched spec already carries the intended
   `Root`, `Host` and `Owner`.
6. **Tests.**
   - Unit (`perforce_test.go`, `client_test.go`, the evict and sweeper-claim tests): re-key the fake
     runner for `-c <client> changes -m1 //<client>/...#head` at `cwd = wsRoot`, and for sync specs
     in client form.
   - The RED test for the orphan: a `ResolveHead` error on first use leaves a registry entry for the
     stream, so `SweepOnce` under an age policy evicts it. At HEAD there is no orphan, so this test
     goes red only against a naive port; it exists to keep the fix's ordering from regressing.
   - **Integration against p4d (`perforce_integration_test.go`)**: extend the fixture with a virtual
     child stream that remaps a path from `//test/main`, and `Prepare` it. Assert the files land in
     the remap layout under the workspace root. This is the only test that can prove the fix; the
     fake runner echoes whatever it is told.
7. README source section: state that virtual and `import+` streams are supported and that relay
   addresses p4 by client path.

## Acceptance / Done When

- A task whose `source.stream` is a virtual or remap stream syncs into the correct layout, proven
  against p4d.
- No early return from `Prepare` leaves an unregistered client spec or directory.
- Existing workspaces do not re-sync after the upgrade (baseline unchanged).
- The cwd contract comment names head resolution as workspace-scoped.

## Related

- `internal/agent/source/perforce/perforce.go` (`Prepare`, `allocateShortID`), `client.go`
  (`ResolveHead`, `CreateStreamClient`), `sweeper.go`, `perforce_integration_test.go`
- [[feature-2026-09-03-p4-sync-progress-heartbeat]] - edits the same sync call site; land it after
  this to avoid a rebase, no hard dependency
- [[idea-2026-09-03-sync-spec-exclusion-paths-design]] - builds on `toClientPath`

## Resolution

`Prepare` now addresses p4 by client-relative path, so a virtual or `import+` remap
stream resolves through the client's own view. Proven against a real p4d: the fixture
gained a `Type: virtual` child stream with a `Remapped:` line, and
`TestPerforce_E2E_VirtualStreamWithARemapSyncsIntoTheRemappedLayout` asserts the file
lands in the remapped layout and NOT flat. It failed at HEAD with
`resolve head for //test/virt/...: could not parse ""` - one step earlier than the
`file(s) not in client view` the item quoted, because with `rev: "#head"` the failure
is in `ResolveHead`, not in the sync.

The reorder the item asked for OPENS a race the item did not anticipate: with client
creation and registration moved above `ws.Acquire`, the workspace has zero holders for
that whole window, so a sweep can complete AND release inside it and the post-Acquire
evict re-check then reads clear. Review established that a partial repair of the
registry row leaves `Prepare` holding a handle on a deleted directory and a deleted p4
client, so the shipped behaviour refuses instead: a missing registry entry proves a
sweep completed, and the task fails with a distinct, retryable cause.

Scoped out and filed rather than silently left: a sync path that is a strict subpath of
a stream whose view RENAMES a subtree still does not resolve, because `toClientPath` is
a string rewrite rather than a view resolution. That is not a regression - the depot form
failed identically before - but README no longer claims otherwise. The silent half is
worse than the resolution failure and is filed separately: `p4 sync` reports
`file(s) not in client view` on stderr and exits ZERO, and `execRunner.Stream` discards
stderr on a nil `Wait()`, so a `@CL`-pinned spec of that shape syncs nothing and the task
reports success.

Acceptance criterion 2 as written ("no early return leaves an unregistered client spec or
directory") was false as scoped - the leak was never on an early-return path - and the
spec says so.
