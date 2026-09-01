---
title: Reject env keys containing = or NUL in jobspec.Validate, so the agent's silent drop becomes a loud 400
type: feature
status: open
created: 2026-09-01
priority: medium
source: found by the correctness lens re-verifying the per-task-identity-env-vars fix rounds; ruled out of that slice as scope creep with a retroactive-tightening hazard
---

# Reject env keys containing = or NUL in jobspec.Validate, so the agent's silent drop becomes a loud 400

## Summary
`Runner.Run` skips any task env key containing `=`, because such a key is a different string to the
reserved-name predicate and the same variable to `os/exec`, which splits an entry at its first `=`.
Nothing on the ingestion side knows that rule: `jobspec.TaskSpec.Env` is a bare
`map[string]string` and no consumer inspects the keys, so a spec with such a key is accepted,
stored, dispatched, and then silently discarded on the agent with no log line and nothing the
submitter can observe.

## Context
The agent-side guard is a security control and must stay: without it, a spec key
`RELAY_JOB_URL=https://evil.example/x` supplies `RELAY_JOB_URL` to the subprocess on any coordinator
with no `RELAY_PUBLIC_URL` set. That was reproduced and fixed. This item is about the OTHER half:
the rule for what a valid environment key is now lives at the far end of the pipeline, in the
runner, rather than where the author can be told about it.

Two observable consequences today:

- `{"FOO=BAR": "baz"}` used to reach the child as `FOO` with the value `BAR=baz`. It now reaches
  nothing. Neither behaviour is what the author wrote, and the new one is silent.
- A NUL byte in a key is worse than silent: `dedupEnv` returns an error and `Cmd.Start` fails before
  `os.StartProcess`, and the runner turns any Start failure into `TASK_STATUS_FAILED` with an empty
  `ErrorMessage`. One bad byte fails the whole task with zero diagnosis.

## Proposal
Reject a key containing `=` or a NUL byte in `jobspec.Validate`, with a 400 naming the offending
key. Keep both runner guards as defence in depth - the runner is what a compromised or older
coordinator cannot talk past.

**The hazard that kept this out of the originating slice: tightening a validator is retroactive over
stored data.** `jobspec.Validate` runs at create time, but `schedrunner` re-validates a stored
scheduled job's spec when the schedule fires. So a scheduled job stored with such a key would stop
firing rather than fail at submit, and the failure would surface at 03:00 on a cron rather than in
the response to the request that introduced it. Decide deliberately how to handle stored specs:
sweep and report before enforcing, exempt the schedrunner path, or accept the break knowingly. Do
not discover it after shipping.

## Acceptance / Done When
- `POST /v1/jobs` with a task env key containing `=` returns 400 naming the key, not 201.
- The same for a NUL byte, rather than a task that fails at run time with an empty error message.
- The two runner guards still pass their existing tests; this adds a layer, it does not replace one.
- The schedrunner re-validation path is addressed explicitly, with the chosen handling written down.

## Related
- [[feature-2026-08-31-per-task-identity-env-vars]] - the slice whose fix rounds surfaced this
- [[feature-2026-09-01-strip-inherited-relay-identity-vars]] - the other deferred half of the same merge
- [internal/jobspec/jobspec.go](internal/jobspec/jobspec.go) - `TaskSpec.Env`, where the check belongs
- [internal/agent/runner.go](internal/agent/runner.go) - `Runner.Run`, the two guards that stay
- [internal/schedrunner](internal/schedrunner) - the re-validating reader that makes this retroactive
