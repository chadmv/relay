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
The per-task identity variables were designed to defend one trust boundary: the job spec author.
The identity block is appended last, so a job spec's `env` and the workspace handle's env both lose
the `os/exec` dedup. The agent operator is a different principal, and one relay does not defend
against - they choose the agent binary and own the machine that runs the subprocess, so there is
nothing to protect. This item is not a security fix; it is a consistency fix.

The realistic failure it removes is mundane rather than adversarial: a stale `RELAY_JOB_URL` left
exported in a debugging shell silently poisons every link the agent's tasks post, and nothing
anywhere reports it.

The current behaviour is pinned, not merely undocumented. The runner's env test asserts that with
the agent process carrying `RELAY_JOB_URL=INHERITED` and a dispatch carrying an empty `JobUrl`, the
child sees `INHERITED`. Implementing this item must therefore flip that test deliberately rather
than discover it.

## Proposal
Filter `os.Environ()` for the four reserved names before the merge in `Runner.Run`, so an inherited
value is dropped whether or not relay has a value of its own to replace it with.

Two things to settle when it is picked up:

- **Case.** `os/exec`'s dedup folds case on Windows only, so a filter that compares case-sensitively
  would leave `relay_job_url` inherited on Linux (correct, it is a distinct variable) and would let
  `Relay_Job_Url` through on Windows where it would then beat relay's own value. The comparison has
  to be case-insensitive on Windows to be honest about the platform it runs on.
- **Whether the four names become documented as reserved.** Stripping makes that a contract rather
  than a convention, and README should say so in the same place it documents the precedence rule.

## Acceptance / Done When
- With the agent process's environment carrying `RELAY_JOB_URL` and a dispatch carrying an empty
  `JobUrl`, the child subprocess has no `RELAY_JOB_URL` key at all.
- The existing inheritance test is replaced rather than deleted, so the change of contract is
  visible in the diff.
- On Windows, a mixed-case spelling of a reserved name is stripped too; on other platforms it
  survives as the distinct variable it is, and a test pins the asymmetry rather than papering over
  it.
- README's task subprocess environment section states that the four names are reserved.

## Related
- [[feature-2026-08-31-per-task-identity-env-vars]] - the slice that introduced the variables and
  deliberately left this out
- `docs/superpowers/specs/2026-09-01-per-task-identity-env-vars.md` - section 12 limitation 1 and
  section 13
- [internal/agent/runner.go](internal/agent/runner.go) - `Runner.Run`, the env merge
