---
title: Strip inherited RELAY_* identity variables from the agent's own process environment
type: feature
status: open
created: 2026-09-01
priority: low
source: proposed in the per-task-identity-env-vars spec (section 13) and deferred out of that slice
---

# Strip inherited RELAY_* identity variables from the agent's own process environment

## Summary
`Runner.Run` appends `RELAY_JOB_ID`, `RELAY_TASK_ID`, `RELAY_JOB_URL` and `RELAY_TASK_URL` only
when it has a non-empty value in hand, so a value exported into `relay-agent`'s own shell is
inherited by every task subprocess and relay does not override it. That makes "absent" mean "absent
unless the agent operator exported it" rather than absolute. Filtering the four reserved names out
of `os.Environ()` before the merge would make the rule unconditional.

## Context
The per-task identity variables defend one trust boundary: the job spec author. The agent already
strips the four names from a job spec's `env` and from the workspace handle's env before merging
either, so those two principals are closed. The agent operator is a different principal, and one
relay does not defend against - they choose the agent binary and own the machine that runs the
subprocess, so there is nothing to protect. This item is not a security fix; it is a consistency
fix, and it is what is LEFT once the two closed principals are subtracted: `os.Environ()` is the
only merge input still passed through unfiltered.

The realistic failure it removes is mundane rather than adversarial: a stale `RELAY_JOB_URL` left
exported in a debugging shell silently poisons every link the agent's tasks post, and nothing
anywhere reports it.

The current behaviour is pinned, not merely undocumented. The runner's env test asserts that with
the agent process carrying `RELAY_JOB_URL=INHERITED` and a dispatch carrying an empty `JobUrl`, the
child sees `INHERITED`. Implementing this item must therefore flip that test deliberately rather
than discover it.

## Proposal
Extend the existing strip to `os.Environ()`. `Runner.Run` already filters `task.Env` and `extraEnv`
through `isReservedIdentityName`; this is the same predicate applied to the third merge input, so an
inherited value is dropped whether or not relay has a value of its own to replace it with.

Two things to settle when it is picked up:

- **Case.** `isReservedIdentityName` already folds case on Windows only, matching `os/exec`'s own
  dedup rule, so the predicate needs no change - only its third call site. Reusing it is the point:
  a second, differently-cased comparison here would be the defect.
- **Whether the four names become documented as reserved.** Stripping makes that a contract rather
  than a convention, and README should say so in the same place it documents the precedence rule.

## Acceptance / Done When
- With the agent process's environment carrying `RELAY_JOB_URL` and a dispatch carrying an empty
  `JobUrl`, the child subprocess has no `RELAY_JOB_URL` key at all.
- `TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone` is replaced rather than
  deleted, so the change of contract is visible in the diff. It exists today to pin the current
  behaviour as a behaviour, precisely so this change cannot be made silently.
- `TestRunner_ACoordinatorValueBeatsAnInheritedOne` still passes: stripping the inherited value must
  not be mistaken for the append no longer having to come after `os.Environ()`.
- The Windows/Unix case asymmetry stays pinned by
  `TestRunner_TheReservedNamesAreCaseFoldedExactlyWhereOsExecFoldsThem`, extended to the new input.
- README's task subprocess environment section states that the four names are reserved.

## Related
- [[feature-2026-08-31-per-task-identity-env-vars]] - the slice that introduced the variables and
  deliberately left this out
- `docs/superpowers/specs/2026-09-01-per-task-identity-env-vars.md` - section 12 limitation 1 and
  section 13
- [internal/agent/runner.go](internal/agent/runner.go) - `Runner.Run`, the env merge
