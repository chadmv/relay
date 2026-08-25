---
title: The delete response's counts are two hand-written structs with no shared contract, so a rename leaves both packages green
type: bug
status: open
created: 2026-08-26
priority: medium
source: 2026-08-26 worker-delete slice - flagged by the engineer, sharpened by the retro
---

# The delete response's counts are unpinned across the API/CLI boundary

## Summary

`DELETE /v1/workers/{id}` returns four counts. The server spells them in
`deleteWorkerResponse` (`internal/api/workers.go`); the CLI decodes them into its own
`deleteResp` (`internal/cli/workers.go`) with independently written json tags. Nothing checks the two
agree.

Rename a key server-side - or add a fifth count - and **both packages stay green** while the shipped
command silently prints `0`, or omits the new number entirely. `internal/cli`'s tests drive an
`httptest` fake whose body is a hand-written literal, so the fake is updated by whoever changes the
CLI and never by whoever changes the server.

## Context

`attribution_cleared` is the sharpest case. It counts the worker's finished tasks whose `worker_id`
the `ON DELETE SET NULL` cascade is about to clear - i.e. how much run-attribution history the delete
destroys. `internal/api/workers.go`'s own comment says the response body exists because "relay has no
audit log, so these counts are the ONLY record of what the delete destroyed."

A record that silently reads `0` is worse than no record.

Compounding: [[idea-2026-08-26-cli-has-no-integration-coverage]] - the CLI never talks to a real
server, so the boundary has no end-to-end witness either.

## Proposal

A contract test that fails on **an added field, not only a renamed one** - the added-field case is the
one a rename-only guard misses, and it is how a fifth count would ship half-wired.

Options, cheapest first:

- Reflect over both structs and require the json tag sets to be equal. The relay tree already has the
  arity-check shape for hand-written copies between two types; this is that pattern across a package
  boundary.
- Or move the response struct to a shared package both import, which removes the second spelling
  entirely and makes the test unnecessary.

Prefer the second if nothing else blocks it - a guard that exists because two copies exist is weaker
than one copy.

## Acceptance / Done When

- Renaming a json tag on either side goes RED.
- **Adding** a field to the server response without adding it to the CLI goes RED.
- The test names `attribution_cleared` specifically, since that is the count whose silent `0` is
  indistinguishable from a true `0`.

## Related

- `internal/api/workers.go` (`deleteWorkerResponse`), `internal/cli/workers.go` (`deleteResp`)
- [[idea-2026-08-26-cli-has-no-integration-coverage]] - the missing end-to-end witness
- [[bug-2026-08-25-no-worker-delete-at-any-layer]] (closed) - the slice that shipped both structs
- `docs/retros/2026-08-26-worker-delete.md`
