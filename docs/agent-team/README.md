# Relay Agent Team Playbook

A team of role-specialized subagents plus a phased orchestration for working on
relay. Design spec: `docs/superpowers/specs/2026-06-18-agent-team-design.md`.

## The roster

| Agent | Role | Edits code? |
|-------|------|-------------|
| `relay-tpm` | Spec, roadmap/strategy, design-time security/scalability, retro + backlog triage | No (docs only) |
| `relay-planner` | Implementation plan via writing-plans; declares FE/BE independence | No (docs only) |
| `relay-backend-engineer` | Go backend under TDD + Invariants | Yes |
| `relay-frontend-engineer` | React/Vite SPA | Yes |
| `relay-code-reviewer` | Review vs Invariants + security | No (reports only) |
| `relay-integration-tester` | testcontainers integration tests, flake diagnosis | Yes (tests) |
| `Explore` (built-in) | Read-only subsystem mapping for discovery | No |

Invoke any agent directly with the Agent tool (`subagent_type: "relay-..."`).

## The pipeline

```
Phase 0  DISCOVERY    Explore xN (parallel, read-only)    -> subsystem map (opt-in)
Phase 1  SPEC         relay-tpm (brainstorming)           -> spec doc          * GATE -> commit
Phase 2  PLAN         relay-planner (writing-plans)       -> impl plan         * GATE -> commit
Phase 3  IMPLEMENT    backend + frontend (parallel*)      -> code + tests
Phase 4  VERIFY       relay-verify workflow               -> findings          loop to 3 if fails
Phase 5  INTEGRATE    finishing-a-development-branch       -> merge / PR        * GATE
Phase 6  RETRO        relay-tpm (retro + backlog)         -> retro + backlog items
```

The conductor is the main interactive session. It runs one phase, reads the
result, then continues (autonomous) or pauses for sign-off (gated).

## gateMode

State the mode at kickoff:

- `autonomous` (default) - the three gates (spec, plan, pre-merge) auto-pass with
  a one-line summary logged. Backlog items: only high-confidence specific items
  are filed, each logged for later review.
- `gated` - the conductor stops at each gate and waits for your approval.

You can also gate a single phase ad hoc in autonomous mode ("pause after the
plan").

## Phase notes

- **Phase 0** is opt-in: skip for small changes; run when scoping something
  unfamiliar.
- **Phase 1** - the conductor commits the spec doc that relay-tpm produced, on its
  own (`docs: add <slug> spec`), when the gate passes, before planning begins.
- **Phase 2** - the conductor commits the plan doc that relay-planner produced, on
  its own (`docs: add <slug> plan`), when the gate passes, before implementation
  begins. The spec and plan must be in history before Phase 3 writes any code - so
  the record reflects the order work was done, and a halt mid-implementation still
  leaves the design captured. The commit happens at the phase boundary in both gate
  modes (autonomous auto-passes the gate, then commits; gated commits after
  sign-off). relay-tpm and relay-planner only write the docs - they hold no git
  access, so committing is always the conductor's step.
- **Phase 3 parallelism** depends on the planner's independence declaration.
  Independent slices run concurrently; if the frontend needs a new backend
  endpoint, they sequence.
- **Phase 4** always begins with the **conductor running `/code-review` itself**
  on the diff, then feeding that output into the `relay-code-reviewer` dispatch
  as prior findings. The agent cannot run it: `/code-review` ships as
  `commands/code-review.md` with no `skills/` directory, so it is a slash command
  no subagent can invoke, and `security-review` is harness-provided rather than a
  file. Earlier versions of this playbook and of the agent's own prose claimed
  otherwise, and the call silently never happened.

  The agent's job is then to **triage and extend**, not to re-derive: confirm or
  refute each fed-in finding with evidence, then run its own adversarial passes
  over the dimensions it owns (the seven Invariants, security, test non-vacuity).
  Feeding the output in rather than replacing the agent matters because the two
  find different things - across the 2026-08-09 batch the agent's own passes
  produced 2 high and 13 medium findings, several reproduced with probes.

  After that, `relay-verify` (a parallel fan-out) if its Workflow opt-in is
  available. That opt-in is per-session, so an unattended run (e.g.
  `/autopilot`) does not have it unless the user granted it in that session; when
  it is missing, the direct dispatch above plus `relay-integration-tester` when
  the diff has integration surface (skip that lane on a zero-Go diff and say so)
  is the full path. Same agents, same coverage, conductor-orchestrated - not a
  licence to lower the bar. Log which path ran. Confirmed findings route back to
  the owning engineer, then re-verify until clean.
- **Phase 5** uses the finishing-a-development-branch skill.
- **Phase 6** is TPM-owned; backlog acceptance keeps the human as final approver,
  and closing backlog items requires the git mv to docs/backlog/closed/.

## Kickoff example

> "Build <feature> with the relay agent team in gated mode."

The conductor then: (optionally) runs discovery, dispatches `relay-tpm` for the
spec, pauses for your approval, dispatches `relay-planner`, pauses, dispatches the
engineers, runs `relay-verify`, loops on findings, pauses before merge, and
finishes with a retro.
