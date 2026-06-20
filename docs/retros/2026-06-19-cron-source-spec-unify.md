---
date: 2026-06-19
topic: cron-source-spec-unify
branch: claude/inspiring-lamport-6beced
range: ab6fed6..8e2e0b9
---

# Session Retro: 2026-06-19 - Cron Source-Spec Unify

**TL;DR:** Closed `bug-2026-06-10-cron-jobs-drop-source` by deleting schedrunner's
parallel hand-rolled job-creation path and routing cron fires through the canonical
`CreateJobFromSpec`, which is now extracted into a new shared `internal/jobcreate`
package so cron-fired scheduled jobs persist task `source` specs identically to
run-now - shipped via the full agent-team flow (brainstorm -> spec -> plan ->
backend engineer -> review).

## What Was Built

Cron-fired scheduled jobs no longer silently drop task `source` specs. Before,
`internal/schedrunner` had its own `runnerSpec`/`runnerTaskSpec` structs (no
`Source` field) and a private `Runner.createJob` that always called
`store.CreateTask`, never `CreateTaskWithSource` - so every cron fire of a
Perforce-sourced schedule lost the workspace spec, diverging from run-now.

- **New `internal/jobcreate` package** - `CreateJobFromSpec` moved verbatim out of
  `internal/api/job_spec.go`, now calling `jobspec.Validate(&spec)` directly. It
  imports only `jobspec` + `store`, so both `internal/api` and `internal/schedrunner`
  can share it without the real `api -> schedrunner` import cycle.
- **`api.CreateJobFromSpec` is a thin wrapper** delegating to `jobcreate`, with the
  `JobSpec`/`TaskSpec`/`SourceSpec` aliases and `ValidateJobSpec` retained, so all
  existing api callers and tests stayed green with zero call-site churn.
- **`schedrunner.fireOne` rewired** - deleted the duplicate structs and `createJob`
  (including its hand-rolled legacy `command -> commands` normalization, now done by
  `jobspec.Validate`); `fireOne` unmarshals into `jobspec.JobSpec` and calls
  `jobcreate.CreateJobFromSpec`. Error handling (log + advance to next fire),
  overlap-skip, `NotifyTaskSubmitted`, and default priority are unchanged.
- **Red-then-green integration test** - `TestRunner_FiresScheduleWithSource_PersistsSource`
  fires a sourced schedule via `TickOnce` and asserts the created task's `source`
  column round-trips the stream; observed failing (nil source) on the old path,
  passing after the rewire.

## Key Decisions

- **New package over folding into `internal/jobspec`.** The cleanest "shared home"
  candidate was `jobspec` itself (it already pairs with `Validate` in the invariant
  text), but `internal/mcp` imports `jobspec` and *not* `store` - it is a thin HTTP
  client that validates locally and POSTs to the API. Putting DB-touching creation
  in `jobspec` would force `store` + pgx as a transitive dependency onto the MCP
  binary for no benefit. A dedicated `internal/jobcreate` keeps `jobspec` pure. The
  decision turned on checking transitive importers, not just cycle-avoidance.
- **This restores the Single job-spec pipeline invariant rather than working around
  it.** The invariant already named `CreateJobFromSpec` as canonical and listed
  schedrunner as a consumer; the bug was that schedrunner had quietly grown a
  parallel path. The fix removes the last parallel creation path instead of bolting
  `Source` onto the duplicate structs.
- **Fire-time validation is a deliberate, safe behavior change.** Routing through
  `jobspec.Validate` means a previously-stored-but-malformed `job_spec` now fails at
  fire time instead of inserting garbage; failures hit the existing advance path
  (never-catch-up), so a bad row cannot hot-loop. Review confirmed this is the
  invariant's intent.

## Files Most Touched

- `internal/jobcreate/jobcreate.go` - new shared creation package (moved `CreateJobFromSpec`).
- `internal/schedrunner/runner.go` - deleted `runnerSpec`/`runnerTaskSpec`/`createJob`; rewired `fireOne`.
- `internal/api/job_spec.go` - shrank `CreateJobFromSpec` to a delegating wrapper.
- `internal/schedrunner/runner_test.go` - red/green source-persistence integration test + `makeSourceSpecJSON` helper.
- `docs/superpowers/specs/2026-06-19-cron-source-spec-unify-job-creation-design.md` - design spec.
- `docs/superpowers/plans/2026-06-19-cron-source-spec-unify-job-creation.md` - implementation plan.
- `docs/backlog/closed/bug-2026-06-10-cron-jobs-drop-source.md` - closed.

## Improvement Goals

- **Check transitive import impact on existing lightweight consumers before placing
  shared code, not just cycle-avoidance.** The obvious home (`jobspec`) would have
  silently coupled the `mcp` HTTP client to `store`/pgx. Verifying who imports the
  candidate package - and what *they* avoid pulling in - drove the right boundary.
  New this session.
- **Trace the full lifecycle of any status/event before claiming "no other changes."**
  (carried from prior retro) Applied: pre-verified the `api -> schedrunner` cycle is
  real, that `mcp` imports `jobspec` without `store`, and that `ListTasksByJob` /
  `Task.Source` exist with the right types before scoping - so the plan had no open
  unknowns and review found only nits.
- **Combined single review for trivial/no-logic tasks** (carried, already promoted to
  [[feedback-combined-review-trivial-tasks]]). Applied: one `relay-code-reviewer`
  pass over the whole coherent diff rather than per-task two-stage review.
- **Treat a backlog proposal as a starting point, not a contract** (carried, already
  promoted to [[feedback-backlog-proposal-not-contract]]). Applied: verified the
  proposal's import-cycle framing against the code before committing to the package move.
- **Match commit here-string syntax to the tool's shell** (carried across multiple
  retros - promoted to [[feedback-commit-heredoc-shell]]). Applied: bash heredocs for
  every commit message.
</content>
