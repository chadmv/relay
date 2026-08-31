---
title: Inject per-task identity env vars (RELAY_JOB_ID, RELAY_TASK_ID, RELAY_JOB_URL) into task subprocesses
type: feature
status: open
created: 2026-08-31
priority: medium
source: asked whether a running job's process can discover its own relay job URL; it cannot
---

# Inject per-task identity env vars (RELAY_JOB_ID, RELAY_TASK_ID, RELAY_JOB_URL) into task subprocesses

## Summary
A command running as part of a job has no way to learn which job or task it belongs to, so it
cannot link back to itself. The motivating use case is a job step that posts a Slack message to
users with a link to the job detail page so they can watch status. Inject the identity the runner
already holds, plus a server-rendered job URL, into every task subprocess.

## Context
`Runner.run` builds the subprocess environment from exactly three sources
([internal/agent/runner.go:155](internal/agent/runner.go:155)): `os.Environ()` (whatever the
`relay-agent` process inherited), the job spec's `env` map, and the workspace handle's env, which
today is only `P4CLIENT`
([internal/agent/source/perforce/perforce.go:461](internal/agent/source/perforce/perforce.go:461)).

`DispatchTask` already carries `job_id` on the wire
([proto/relayv1/relay.proto:110](proto/relayv1/relay.proto:110)) and the dispatcher populates it
([internal/scheduler/dispatch.go:307](internal/scheduler/dispatch.go:307)), but no production code
under `internal/agent/` reads `JobId` - the only references are in test files. The runner uses
`r.taskID` for status and log messages and never writes either identifier into `cmd.Env`.

Neither identifier can be supplied from the job spec instead: the job id is generated server-side at
create time, so a spec cannot reference the job it is creating.

A URL needs a third input that does not exist anywhere yet. The server has no public/base-URL
setting (`RELAY_DATABASE_URL` is the only URL-shaped env var in `cmd/relay-server`), and the agent
only knows a coordinator `host:port` from its `-coordinator` flag - a gRPC address, not the
browser-facing HTTP origin.

## Proposal
1. Inject `RELAY_JOB_ID` and `RELAY_TASK_ID` in the runner from `task.JobId` and `r.taskID`. Both
   values are already in hand; no protocol or config change.
2. Add a server-side public base URL setting (e.g. `RELAY_PUBLIC_URL`) and have the server render
   the job URL and send it on `DispatchTask` as a new field, rather than teaching the agent to
   build URLs. The agent has no idea what origin a browser reaches the server on, and a
   reverse-proxied deployment makes that unknowable from the agent side. Unset means the field is
   empty and `RELAY_JOB_URL` is simply not injected - no guessed origin.
3. Frontend routes to target: `/jobs/:id` for the job detail page and `/jobs/:id/tasks/:taskId` for
   a task's logs ([web/src/app/router.tsx:28](web/src/app/router.tsx:28)). Decide whether to inject
   the task log URL too, or leave callers to build it from `RELAY_JOB_URL` plus `RELAY_TASK_ID`.

Open design question, decide before implementing: **merge precedence**. The current order is
process env, then spec `env`, then workspace env, so a later source wins. Injecting identity vars
before the spec's `env` lets a job spec overwrite its own identity; injecting after means a spec
cannot override them at all. Prefer injecting last (spec cannot spoof), and say so in the docs,
since the whole value of these variables is that a downstream notifier can trust them.

## Acceptance / Done When
- A task subprocess sees `RELAY_JOB_ID` and `RELAY_TASK_ID` matching the dispatching job and task.
- With the public base URL configured, the subprocess sees `RELAY_JOB_URL` pointing at that job's
  detail page; with it unconfigured, the variable is absent rather than wrong.
- Precedence against a job spec `env` that names the same keys is pinned by a test, in whichever
  direction is chosen.
- README documents all three variables and the new server setting.

## Related
- [internal/agent/runner.go](internal/agent/runner.go) - the env merge
- [internal/scheduler/dispatch.go](internal/scheduler/dispatch.go) - builds `DispatchTask`
- [proto/relayv1/relay.proto](proto/relayv1/relay.proto) - `DispatchTask`, would gain the URL field
- [web/src/app/router.tsx](web/src/app/router.tsx) - job detail and task log routes
