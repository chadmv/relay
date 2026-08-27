---
title: 51 internal/cli default-lane fixture bodies encode through the CLI's own response structs, so they agree with the decoder by construction
type: idea
status: open
created: 2026-08-27
priority: medium
source: 2026-08-27 CLI real-server integration lane slice - measured during Phase 4 verification
---

# 51 `internal/cli` default-lane fixture bodies encode through the CLI's own response structs

## Summary
`internal/cli`'s fast `httptest` tests build their fake responses by marshalling the CLI's *own*
response structs (`relayclient.PageEnvelope[workerResp]`, `Encode(jobResp{...})`, and siblings).
A fixture built from the type under test agrees with the decoder **by construction** - on both the
envelope keys and the item fields - so it can never detect drift in either direction. This is a
strictly weaker position than a hand-written JSON literal, which can at least disagree.

## Context
This is the residual half of [[idea-2026-08-23-cli-tests-never-hit-real-server]], which is now
closed. That item added a real-server integration lane and a CI job to run it; the lane catches
server-shape drift, and the routing rule in CLAUDE.md tells future authors which lane a new test
belongs in. **Neither fixes the existing fixtures**, and the lane deliberately sits beside them
rather than replacing them - most of `logs_test.go`'s 42 tests assert behaviour a real server
cannot be made to produce (an empty page not reporting drained, a non-advancing cursor, a 500 on
page 2, a stdout that rejects writes), so repointing them would delete real coverage.

## Measured, 2026-08-27
**51 vacuous fixture bodies across 43 `Encode` statements.** By body: 19 paged
(`PageEnvelope[workerResp|jobResp|scheduleResp|reservationResp]`) + 32 unpaged. By statement:
19 + 24. A further 7 use `PageEnvelope[map[string]any]`, which is a genuine simulator on the item
axis and a tautology on the envelope axis.

**23 of the bodies are in `internal/cli/logs_test.go`** - the file whose `writeTaskLogPage`/`logRow`
pair the routing rule holds up as the pattern to copy. It gets the log page right and the job body
wrong 23 times.

### The count is the interesting part, and it is a lesson about instruments
Four attempts produced four different numbers - **19, 29, 42, and finally 51**. Every wrong one
grepped for `Encode(<cliType>{`, and `logs_test.go`'s `fakeJobSnapshotServer` takes a `[]jobResp`
**parameter**, routing nine call sites' literals through a single `Encode(bodies[i])` that no such
grep can see. The correct count came from an AST walker collecting composite literals whose type is
a CLI response type, suppressing literals nested inside another such literal.

A text search cannot establish a structural property. The fixture type travels through a function
signature, so the shape to search for is the type in any fixture position, not the encode call.
See [[reference_match_the_instrument_to_the_claim]].

## Proposal
Not a mass rewrite - the integration lane already covers shape drift for the paths it exercises,
so the marginal value per fixture is low and the churn is high. Two cheaper options, in order:

1. **A structural guard.** A `go/parser` test that fails when a non-integration test file in
   `internal/cli` constructs a fixture from a CLI response type. That makes the rule CLAUDE.md
   states into a check, and caps the number rather than reducing it. Note the repo's own record on
   parser guards: they were evaded five times in the 2026-08-24 finishRegister slice before holding,
   so treat this as the expensive fallback it is ([[reference_guard_inherits_mutation_shape]]).
2. **Convert opportunistically**, when a test is touched for another reason, to a locally declared
   struct whose json tags are deliberately independent of the production type.

Doing nothing is also defensible now that the integration lane exists; what is not defensible is
the current state where the number is unknown to anyone who has not measured it.

## Acceptance / Done When
- Either a guard prevents new instances, or a written decision records why the 51 are acceptable.
- If a guard: it is proven against the real producer, not against test-file literals
  ([[reference_guard_never_sees_real_producer]]), and its own evasions are recorded.

## Related
- [[idea-2026-08-23-cli-tests-never-hit-real-server]] - closed; this is its residual half
- `CLAUDE.md` "Where a CLI test goes" - the routing rule and the count
- `internal/cli/logs_test.go` (`writeTaskLogPage`, `logRow`, `fakeJobSnapshotServer`)
- `docs/superpowers/specs/2026-08-27-cli-real-server-integration-lane.md` R-i
