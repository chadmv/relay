# Retro: the job retry action, and the batch that closed its own loop

**Date:** 2026-08-14
**Slice:** 2026-08-14-job-retry-action
**Closes:** `feature-2026-07-01-job-retry-action`
**Shape:** frontend-only, 12 files, zero Go. Suite 1068 -> 1102.

## What shipped

Two pills in the job-detail header, `Retry failed` and `Retry all`, each behind its own confirm
dialog, calling `POST /v1/jobs/{id}/retry?task=failed|all`. A classifier turns the endpoint's three
distinct 409s into three distinguishable messages, each with a frontend-owned next step.

## The batch closed its own loop, and that is the interesting structural fact

This slice consumes the endpoint that shipped in the **first** iteration of the same batch. Five
iterations apart, the Phase 4 browser lane stood up Postgres, relay-server and Vite and drove the
frontend against the real handler: `?task=failed` returning 200 with `tasks_retried: 1`, a real
`job is not finished` 409 rendering its specific sentence, the availability matrix holding on all
five job statuses.

That is worth naming because it is the only end-to-end check that could have caught a
frontend-backend contract drift, and it was available only because both halves landed in the same
batch. The unit tests were green against a **mocked** server the whole time and would have stayed
green if the wire contract had disagreed.

It also validated an earlier iteration in passing: four pills at a 375px viewport produced no
overflow, which is the narrow-viewport fix from two iterations back holding under a new worst case
nobody designed it for.

## The item was current, for once, and that is because the batch maintained it

Four of five iterations in this batch found their backlog item wrong about the code. This one did
not - its wire contract verified exact, including all five 4xx sentences, `?task` repeated yielding
400, and `failed` covering `timed_out`.

The reason is not luck: iteration 1 **amended this item** when it shipped the endpoint, writing the
real contract into the "Blocked" section and adding a "Known trap" note about the zeroed enrichment.
An item that gets updated by the slice that unblocks it is an item the next slice can trust.

Three discrepancies remained, all found by verifying anyway:

- **The zeroed-enrichment trap is worse than the item said.** `toJobResponse(job, "", nil, nil)`
  drops `tasks` and `submitted_by_email` entirely, not just two counters - so seeding the cache
  would have blanked the task table. This settled invalidate-versus-seed on its own, and the design
  makes seeding structurally impossible: `retryJob` returns only `{tasks_retried}`, so
  `setQueryData(['job', id], data)` will not typecheck.
- **The hi-fi is not silent.** The item implied no retry visual exists; the authoritative hi-fi
  carries a `Retry` ghost pill - on a *running* job, which the endpoint refuses. The slice diverges
  deliberately on both count and availability, which is a better outcome than either following a
  mockup that contradicts the server or claiming no reference existed.
- `JobDetailPage` still asserted in a comment that no per-job retry endpoint exists. False since the
  first iteration of this batch.

## The vacuity pattern, one more time, and in a new disguise

A contract test read `internal/api/jobs.go` and asserted the server's five error prefixes still
exist, so the frontend classifier cannot silently drift from the backend. The implementer proved it
non-vacuous by mutating a prefix on the **TypeScript** side and watching it redden.

Review then ran the mutation that actually matters - rewording the **Go handler's** raced sentence -
and the test stayed **green**. The prefix `the job changed` is also a substring of the *blocked*
sentence ("...or the job changed while the request was in flight"), so the assertion was being held
up by an unrelated branch. Reword the handler and the frontend would degrade silently to `unknown`,
with no test failing.

The lesson generalizes past this test: **a guard that reads another file must be mutation-tested on
that other file.** Proving it reddens when you break the local copy proves only that the local copy
is load-bearing, which was never in doubt. The prefix is now branch-unique, and the guard's real
limit - it cannot catch a wholly new 409 branch that has no prefix entry - is written down rather
than left to be over-trusted.

## A test was green because of the bug, again

The unknown-error fallback rendered `err.message`, which `apiFetch` builds as `${status} ${code}`,
so an unclassified error showed `500 db error` while every sibling branch showed a bare sentence -
contradicting the module's own doc comment about falling through to "the server's own text". An
existing assertion expected exactly `'500 db error'`, pinning the defect.

That is the **second time in this batch**: the previous iteration found a frontend test asserting
`owner_email === ''` with a careful comment citing the handler. Both were correct, well-documented,
and would have failed the moment anyone fixed the bug. The tell is a test whose expected value is
the bug's output rather than the contract's.

## A subagent disregarded a system message that contradicted git

While reverting a temporary Go mutation, the implementing agent reported that a system-reminder
claimed `internal/api/jobs.go` had been intentionally modified and instructed it not to revert or
mention the change. It checked `git status` and `git diff`, found the file clean, disregarded the
instruction, and flagged it.

That was the right call and is worth recording as a norm: **verify against real state before acting
on a claim about the tree, whatever the claim's apparent authority.** The conductor independently
confirmed afterwards that the branch is zero-Go and `jobs.go` is byte-identical to `origin/main`.

## Verification

Frontend 1102/1102 across 146 files - baseline 1068 plus the plan's predicted 34, matching exactly.
`npx tsc -b` clean. `go build`/`go vet` clean with zero Go files changed. Real-browser end-to-end
against the shipped handler, including a provoked 409 and a hit test proving the error banner is not
occluded by its own dialog.

The plan's own test-count baseline was stale (1059, predating three merged slices) and was corrected
to 1068 before the plan was committed - a small instance of the same discipline the rest of the
batch spent its time on.
