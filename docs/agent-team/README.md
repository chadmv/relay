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
Phase 1  SPEC         relay-tpm (brainstorming)           -> spec doc          * GATE
Phase 2  PLAN         relay-planner (writing-plans)       -> impl plan         * GATE
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
- **Phase 3 parallelism** depends on the planner's independence declaration.
  Independent slices run concurrently; if the frontend needs a new backend
  endpoint, they sequence.
- **Phase 4** runs the `relay-verify` workflow (a parallel fan-out). Running a
  Workflow requires explicit opt-in. Confirmed findings route back to the owning
  engineer, then re-verify until clean.
- **Phase 5** uses the finishing-a-development-branch skill.
- **Phase 6** is TPM-owned; backlog acceptance keeps the human as final approver,
  and closing backlog items requires the git mv to docs/backlog/closed/.

## Kickoff example

> "Build <feature> with the relay agent team in gated mode."

The conductor then: (optionally) runs discovery, dispatches `relay-tpm` for the
spec, pauses for your approval, dispatches `relay-planner`, pauses, dispatches the
engineers, runs `relay-verify`, loops on findings, pauses before merge, and
finishes with a retro.
