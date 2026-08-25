---
title: internal/cli has zero integration coverage - every CLI test drives a hand-written fake
type: idea
status: open
created: 2026-08-26
priority: medium
source: 2026-08-26 worker-delete slice - the integration lens measured the lane at 1.0s and found no containers
---

# internal/cli has zero integration coverage

## Summary

`internal/cli` has **no** `//go:build integration` files and no testcontainers usage. Every CLI test,
including the four for the newly-shipped destructive `relay workers delete`, drives an
`httptest.NewServer` with a hand-written `http.HandlerFunc` response. No test in the package has ever
talked to a real `relay-server` or a real Postgres.

The tell is the runtime: under `-tags integration -p 1`, `internal/cli` finishes in **1.0s** while
`internal/store` takes 257s and `internal/worker` 175s. Nothing containerized runs.

## Context

Measured 2026-08-26 by the integration lens during the worker-delete slice:

```
grep -rl "go:build integration" internal/cli/   -> no matches
grep -rl testcontainers internal/cli            -> no matches
ok  relay/internal/cli  1.046s
```

This is the **invented-contract** class that `idea-2026-06-03-web-e2e-harness` was filed for on the
frontend and that `idea-2026-08-23-cli-tests-never-hit-real-server` names for the CLI - that item is
the direct sibling and should probably absorb this one. What is new is the evidence and the stakes:
`relay workers delete --yes` is the first **irreversible** operation in the product, and its
end-to-end coverage is "does the CLI construct the request and parse the response the fake returns."

A drift in the real handler's status codes, its response shape, or its error strings is invisible to
every CLI test.

## Proposal

Check first whether [[idea-2026-08-23-cli-tests-never-hit-real-server]] already covers this - if so,
fold this evidence into it and close this as a duplicate rather than carrying two.

Otherwise: one integration-tagged lane that drives the real `relay` binary against a real
`relay-server` and a real Postgres, covering the handful of commands where a contract mismatch is
expensive - `workers delete` first, since it is destructive and irreversible.

Keep the fast fake-driven tests. They cover flag parsing and output formatting well, and that is a
different job.

## Acceptance / Done When

- At least `relay workers delete --yes` is exercised end to end against a real server, including the
  refusal paths (409 on a connected worker, 404 on a missing one).
- The lane is tagged so the default suite stays fast.
- If this folds into the 2026-08-23 item, that item carries the measurement and this one closes as a
  duplicate.

## Related

- [[idea-2026-08-23-cli-tests-never-hit-real-server]] - the sibling; check for overlap first
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the other half: a lane CI does not run
- `internal/cli/workers_delete_test.go`, `internal/cli/workers.go`
- `docs/retros/2026-08-26-worker-delete.md`
