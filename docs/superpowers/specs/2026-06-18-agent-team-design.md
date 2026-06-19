# Agent Team Design

Date: 2026-06-18

## Purpose

Define a team of role-specialized Claude Code subagents for the relay project,
plus an orchestration pipeline that wires them together across the feature
lifecycle. The team models a small engineering org: a technical product manager,
a planner/architect, separate frontend and backend engineers, a code reviewer,
and an integration tester. The pipeline runs end-to-end by default but pauses at
opt-in approval gates when the human wants to stay in the loop.

## Goals

- Reusable subagent definitions under `.claude/agents/` that can be invoked
  individually (via the Agent tool) or composed by the orchestration.
- A phased orchestration pipeline that runs the full lifecycle: discovery → spec
  → plan → implement → verify → integrate → retro.
- Autonomous-by-default execution with three opt-in human gates (spec, plan,
  pre-merge), plus the ability to gate any single phase ad hoc.
- Embedded project discipline: the existing superpowers flow, the documented
  Invariants, TDD, and the project's conventions are baked into the relevant
  agent prompts rather than re-derived each run.

## Non-Goals

- A fully hands-off "one kickoff, finished PR" system with no human visibility.
  The human remains the approver at gates and the final accept on backlog items.
- Scripting the entire pipeline as a single background workflow. Gates need
  human-in-the-loop, which a detached background workflow cannot provide.
- Splitting the backend into multiple specialist roles. Noted as a possible
  future refinement, not built now.

## The Roster

Six custom subagents plus reuse of the built-in `Explore` agent for read-only
mapping.

| Agent | Owns | Tool scope | Model |
|-------|------|-----------|-------|
| **relay-tpm** | System design, scalability, and security at design time; roadmap and strategy; spec authorship; decomposition; retros and backlog triage | Read/Grep/Glob, Write to `docs/` only, WebSearch/WebFetch; skills: brainstorming, roadmap, backlog, retro. No code edits. | opus |
| **relay-planner** | Technical implementation plan: reads code deeply, identifies critical files, sequences tasks, declares frontend/backend independence, defines per-step verification | Read/Grep/Glob, Write to `docs/` only; skill: writing-plans. No code edits. | opus |
| **relay-backend-engineer** | Go: `internal/{api,scheduler,schedrunner,worker,agent,store,events}`, gRPC/proto, sqlc + migrations | Full (Read/Edit/Write/Bash/Grep/Glob) | opus |
| **relay-frontend-engineer** | React/Vite SPA embedded in relay-server | Full + preview/browser tools for UI verification | sonnet |
| **relay-code-reviewer** | Adversarial review against the documented Invariants + security review of the diff (runs `/code-review` and `/security-review`); reports findings, never edits | Read-only + Bash (diff/tests). No code edits. | opus |
| **relay-integration-tester** | Docker/testcontainers tests (Postgres, p4d), gRPC stream behavior, flake diagnosis | Full + Bash (docker, `go test -tags integration`) | sonnet |
| `Explore` (built-in) | Read-only fan-out mapping of subsystems during discovery | Per built-in definition | Per built-in |

### Role boundaries (deliberate)

- **relay-tpm and relay-planner cannot edit code.** They own design and docs
  only. This keeps the TPM's architectural judgment and the planner's sequencing
  independent of the code they would otherwise be tempted to defend. Plans and
  specs are docs, so Write-to-`docs/` is sufficient.
- **relay-code-reviewer cannot edit code.** It reports findings; fixes route back
  to the owning engineer. Keeps review honest.
- **Backend is a single role** despite its breadth, matching the requested
  "separate front and backend engineers." An optional later split (e.g.
  scheduler/worker vs api/store) is noted but out of scope.

## The Orchestration Pipeline

The lifecycle runs as discrete phases. A conductor (the main interactive session)
runs one phase, reads its result, then either auto-continues (autonomous mode) or
stops for human sign-off (gated mode). A gate is simply a pause between phases.

```
Phase 0  DISCOVERY    Explore xN (parallel, read-only)    -> subsystem map (opt-in)
Phase 1  SPEC         relay-tpm (brainstorming)           -> spec doc          * GATE
Phase 2  PLAN         relay-planner (writing-plans)       -> impl plan         * GATE
Phase 3  IMPLEMENT    backend + frontend (parallel*)      -> code + tests
Phase 4  VERIFY       code-reviewer + integration-tester  -> findings          loop to 3 if fails
                      (parallel fan-out)
Phase 5  INTEGRATE    finishing-a-development-branch       -> merge / PR        * GATE
Phase 6  RETRO        relay-tpm (retro + backlog triage)  -> retro doc + proposed backlog items
```

### Gates

- **Three gates** (marked `*`): spec, plan, pre-merge.
- **gateMode** is set at kickoff:
  - `autonomous` - gates auto-pass with a one-line summary logged.
  - `gated` - the conductor stops at each gate and asks the human.
- Any single phase can also be gated ad hoc regardless of mode.

### Phase notes

- **Phase 0 (Discovery) is opt-in.** Skip for small changes; run when scoping
  something unfamiliar. The conductor decides based on task size by default.
- **Phase 3 parallelism is conditional** (the `*`). Backend and frontend run
  concurrently only when the plan marks their slices independent. If the frontend
  depends on a new backend endpoint, they sequence. The planner declares this in
  the plan.
- **Phase 4 is a true parallel fan-out** and is the one phase implemented as an
  actual Workflow script (`.claude/workflows/relay-verify.js`): reviewer
  (invariants + security) and tester run at once, and the reviewer can fan out
  across review dimensions. It is pure parallel-with-no-gates, which is exactly
  what the Workflow machinery is for. The rest of the pipeline is conductor-driven
  because gates need human-in-the-loop.
- **Verify -> Implement loop.** Confirmed findings route back to the owning
  engineer (backend findings -> backend-engineer, etc.), then re-verify, until
  clean.
- **Phase 6 (Retro) is TPM-owned.** The TPM runs the retro skill and triages
  extracted backlog items. The retro -> backlog -> roadmap loop is one continuous
  product-ownership activity, so it belongs to the role that owns roadmap and
  backlog. The backlog skill's "never auto-file without confirmation" rule is
  preserved: in gated mode the human gives the final accept; in autonomous mode
  the TPM files only high-confidence, specific items and logs each one for review.

## Deliverables

```
.claude/agents/
  relay-tpm.md
  relay-planner.md
  relay-backend-engineer.md
  relay-frontend-engineer.md
  relay-code-reviewer.md
  relay-integration-tester.md
.claude/workflows/
  relay-verify.js              # Phase 4 parallel fan-out (reviewer + tester + review dimensions)
docs/agent-team/
  README.md                    # the playbook: phases, gates, gateMode, how to kick off
```

## Embedded Discipline

Each agent's system prompt carries only what its role needs, not the entire
CLAUDE.md.

- **relay-tpm** - brainstorming flow; decomposition of oversized work;
  security/scalability lens at design time; roadmap/backlog/retro skills; backlog
  triage with the human as final approver.
- **relay-planner** - the writing-plans discipline; must declare frontend/backend
  independence for Phase 3; saves the plan to `docs/superpowers/plans/`.
- **relay-backend-engineer** - TDD; the six Invariants verbatim (epoch fence,
  single job-spec pipeline, one bounded sender per stream, identity-checked
  teardown, no interior pointers across locks, single JSON entry point) plus the
  tokenhash and bcrypt rules; `make generate` after `.sql`/`.proto` edits; never
  edit `*.sql.go` or `models.go`.
- **relay-frontend-engineer** - match existing SPA component patterns; ARIA and
  accessibility (per prior retros); preview-tool verification.
- **relay-code-reviewer** - runs `/code-review` and `/security-review`; checks the
  diff against each Invariant explicitly (per the 2026-06-10 review, every
  high-severity finding was an invariant sidestep); reports, never edits.
- **relay-integration-tester** - `//go:build integration`, testcontainers, `-p 1`
  to avoid container conflicts, Docker `desktop-linux` context on Windows; flake
  diagnosis.

### Cross-cutting rules baked into every agent prompt

- No em dashes or en dashes; use regular hyphens.
- Surgical changes only (CLAUDE.md Behavior section 3): touch only what the task
  requires; clean up only orphans your own changes create.
- Honor `docs/retros/` for prior context at the start of work.
- The conductor playbook additionally notes the backlog-housekeeping rule (the
  `git mv` to `docs/backlog/closed/` when work closes items is required scope) and
  the full-superpowers-flow expectation (never skip spec, plan, user review, or
  subagent-driven steps).

## Testing / Validation

This design produces configuration and prompt artifacts rather than runtime code,
so validation is behavioral:

- Each agent definition is exercised on a representative task and reviewed for
  correct tool scope, correct model, and adherence to its embedded discipline.
- `.claude/workflows/relay-verify.js` is run against a known diff to confirm the
  parallel fan-out completes and returns structured findings.
- The conductor playbook is dry-run on one small feature in both `autonomous` and
  `gated` modes to confirm gates pause/continue as intended.

## Future Refinements (out of scope)

- Optional backend split into subsystem specialists.
- A dedicated conductor agent (vs. the main session acting as conductor).
- Scripting additional phases as workflows where gates are not required.
