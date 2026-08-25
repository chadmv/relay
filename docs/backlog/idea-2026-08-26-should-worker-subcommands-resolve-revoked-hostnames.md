---
title: Four worker subcommands cannot resolve a revoked worker by hostname, and nobody has decided whether they should
type: idea
status: open
created: 2026-08-26
priority: low
source: 2026-08-26 worker-delete slice - measured while narrowing resolveWorkerID back to delete-local
---

# Four worker subcommands cannot resolve a revoked worker by hostname

## Summary

`relay workers delete` needs to find a revoked worker by hostname - that is the whole point of the
command, since a revoked row is exactly what keeps a hostname claimed. It therefore needs a resolver
that looks past the default worker list.

`resolveWorkerID` (`internal/cli/workers.go:213`) has **six** call sites, so giving it that fallback
would extend the capability to `disable`, `enable`, `workspaces` and `evict-workspace` as well as
`revoke` and `delete`. The worker-delete slice deliberately did **not** do that - it used a
delete-local resolver instead, because the shared route was unrequested scope and two existing tests
pinned the old contract.

So the question is open rather than answered: **should those four commands resolve a revoked worker
by hostname?**

## Context

Measured 2026-08-26. The shared-resolver route was implemented first, broke
`TestWorkersRevoke_NotFound` (`internal/cli/workers_revoke_test.go:68`) and
`TestDoWorkersWorkspaces_UnknownHostname` (`internal/cli/workers_workspaces_test.go:152`), and was
then narrowed back. Both tests are byte-identical to `origin/main` again.

The spec for that slice pre-registered the trigger ("fall back to a delete-local resolver if a test
pins it"), and it fired twice. Narrowing was the conservative call, not a judgement that the
capability is wrong.

## Proposal

Decide per command rather than in one sweep - they are not the same question:

- **`disable` / `enable`** - a revoked worker is already excluded from dispatch, so disabling it is
  close to a no-op. Is the operator's intent meaningful, or should the CLI say "that worker is
  revoked" and stop? Note `README.md` documents disabled and revoked as independent states.
- **`workspaces`** - read-only. Showing a revoked worker's last known workspaces is plausibly useful
  for deciding whether to delete it, and is the strongest candidate of the four.
- **`evict-workspace`** - acts on an agent that is by definition not connected. Probably should
  refuse, and say why.

Whatever is decided, the outcome should be one resolver with an explicit parameter rather than two
resolvers that drift, and the tests that pin today's contract should be updated deliberately rather
than as collateral.

## Acceptance / Done When

- Each of the four commands has a stated, tested decision about revoked-hostname resolution.
- If any gains the capability, `README.md`'s worker-management section says so, and the change is
  visible in the command's own help text rather than only in a resolver.
- `delete` and `revoke` keep working unchanged.

## Related

- `internal/cli/workers.go` (`resolveWorkerID` and its six callers), `internal/cli/workers_workspaces.go`
- `docs/superpowers/specs/2026-08-26-worker-delete.md` section 8.5 - the pre-registered hedge
- [[bug-2026-08-25-no-worker-delete-at-any-layer]] - the slice that surfaced this
