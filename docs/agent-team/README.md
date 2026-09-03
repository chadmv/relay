# Relay Agent Team Playbook

A team of role-specialized subagents plus a phased orchestration for working on
relay. Design spec: `docs/superpowers/specs/2026-06-18-agent-team-design.md`.

## The roster

| Agent | Role | Edits code? |
|-------|------|-------------|
| `relay-tpm` | Spec, roadmap/strategy, design-time security/scalability, backlog triage | No (docs only) |
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
Phase 4  VERIFY       /code-review + 4 agents (parallel)  -> findings          loop to 3 if fails
Phase 5  INTEGRATE    finishing-a-development-branch       -> merge / PR        * GATE
Phase 6  RETRO        CONDUCTOR runs /retro (never an agent) -> retro + backlog items
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
- **Phase 3 briefs put everything outside the worktree off-limits.** Databases other
  than the lane's own e2e database, processes the agent did not start, and global
  npm or Go caches are not touched without asking, and any such action is reported
  in the first line of the engineer's report. On 2026-09-03 one engineer dropped and
  recreated the local `relay` dev database to populate a page and another killed a
  `relay-server.exe` left by an earlier session; neither brief had said anything
  about shared state, and the conductor learned of both from the reports. When
  several lanes may run the browser suite, the brief also carries the e2e lock
  protocol from `web/e2e/README.md`.
- **Phase 4** is a conductor-run `/code-review` followed by a **parallel fan-out of
  four agents in a single message**. There is no Workflow and no opt-in to obtain.

  1. The conductor runs `/code-review` itself on the diff. The agents cannot: it
     ships as `commands/code-review.md` with no `skills/` directory, so it is a
     slash command no subagent can invoke, and `security-review` is
     harness-provided rather than a file. Earlier versions of this playbook and of
     the reviewer's own prose said to call both via the Skill tool, and the calls
     silently never happened.
  2. Then dispatch these four in **one message** so they run concurrently, feeding
     the `/code-review` output into the three reviewer lenses as prior findings:

     | agentType | lens | brief |
     |---|---|---|
     | `relay-code-reviewer` | invariants | Only the seven Invariants in CLAUDE.md. Report any path that sidesteps one. Read them for their shape, not their nouns, on a `web/` diff. |
     | `relay-code-reviewer` | correctness | Correctness bugs and needless complexity. Attack the tests as their own artifact. A checkable-but-unpinned prose claim is a finding; the default remedy is deletion or relocation to the commit message. |
     | `relay-code-reviewer` | security | Auth and authorization paths, input validation, secret handling, token hashing via `internal/tokenhash.Hash`. |
     | `relay-integration-tester` | integration | Run the integration tests relevant to the diff (`go test -tags integration -p 1`). Failures are high, flakes medium. Skip this lane entirely on a zero-Go diff and say so. |

     Three narrow lenses beat one long multi-dimension prompt, which is the one
     thing the old fan-out did better than a single dispatch. Keep them separate.

  Each agent triages the fed-in findings before adding its own: confirm or refute
  each with `file:line` evidence and a concrete failure scenario, and say which.
  A fed-in finding is a lead, not a verdict. Then run its own adversarial passes -
  that is the part that finds what a generic pass does not, so do not stop early
  because the list already looks long. Across the 2026-08-09 batch the agents' own
  passes produced 2 high and 13 medium findings, several reproduced with probes.

  Ask for **prose with evidence**, not a rigid findings schema. The most valuable
  output of that batch was a 25-cycle probe showing 26 connections, a
  demonstration that `JSON.stringify([new Error(secret)])` is `'[{}]'`, and a trace
  through library source proving a cache setting both necessary and sufficient -
  all of which a `{file, severity, summary}` schema would have flattened into
  assertions the conductor could not check.

  Confirmed findings route back to the owning engineer, then re-verify until clean.

  **After a fix round, the verify lens's primary subject is the FIX'S OWN DIFF, not
  the original defect.** State it as the round's opening question: "what does this
  round's new code do with the input that produced the original symptom?" On
  2026-08-26 three consecutive fix rounds each introduced a regression in their own
  newest code, and each was found by the NEXT round reviewing the previous round's
  diff - a task-less 200 read as "nothing owed" by the reconcile written to close the
  omission, a non-canonical id hanging on the SSE filter after that reconcile made the
  snapshot authoritative, and a 404 retried by the retry added for transient failures.
  The fourth round is the only evidence the sequence terminates. Scope the re-verify
  to the new delta and say so, rather than re-reviewing ground two lenses already
  covered. For prose findings the fix is deletion-first; a correction is the remedy
  that regenerated the defect four times running on one docstring.
- **Phase 5** uses the finishing-a-development-branch skill.
- **Phase 6 runs in the CONDUCTOR's own session. Do NOT dispatch it to `relay-tpm`
  or any other agent.** The `/retro` skill stops partway to present the human with
  the backlog candidates and the promotion candidates, then waits for a decision. A
  subagent has no one to present to, so dispatching it silently skips those steps -
  and the promotion step is the only thing that gives a lesson a life beyond the next
  session. Measured on 2026-08-26: the retro was dispatched to `relay-tpm`, eight
  promotion candidates were never offered, and the conductor promoted one lesson
  unilaterally without asking. Backlog acceptance keeps the human as final approver,
  and closing backlog items requires the git mv to docs/backlog/closed/.

## Read every handed-down artifact once for contradiction, before acting on it

Each phase hands the next one a document, and on this project **every stage has refuted the stage
before it** - the spec refuted the backlog item's own fix, the plan refuted two of the spec's test
designs, the engineers refuted the plan's mutations, and Phase 4 refuted the engineers. That is the
pipeline working, but it is cheapest when the reader looks for it deliberately rather than
stumbling on it mid-implementation.

So before executing a spec or plan, read it ONCE asking only: does it contradict itself, does it
contradict the tree, and does it prescribe something that does not exist? Three recurring shapes,
each caught on this project after it had already been handed down:

- **A remedy that names a hook or command that is not there.** A spec said to flush a writer "on
  close" for a type with no close path, and `os/exec` never closes a caller-supplied `Stdout`.
- **An acceptance criterion that is false against the design it accompanies.** "Chunks contain no
  CRLF" was stated for a transform that deliberately emits one on some inputs. A test written to
  that wording would have failed on correct code, and the natural fix is to weaken it.
- **Line citations that were accurate when written.** They rot as soon as anything inserts lines
  above them - including the same commit. Cite by symbol.

Say what you refuted in your report. A stage that returns "the input was correct, here is the work"
is a stage that either verified nothing or found nothing worth saying - and the two are
indistinguishable to the conductor unless you state which.

## Kickoff example

> "Build <feature> with the relay agent team in gated mode."

The conductor then: (optionally) runs discovery, dispatches `relay-tpm` for the
spec, pauses for your approval, dispatches `relay-planner`, pauses, dispatches the
engineers, runs `/code-review` and fans out the four verify agents in parallel,
loops on findings, pauses before merge, and finishes with a retro.
