---
date: 2026-09-01
topic: per-task-identity-env-vars
branch: claude/retro-per-task-identity-env-vars
range: 5fcdadb940ac41ba9d897095b3301221ccb1c766..4ad8d6552bad9318f2f915900b946481f380fae2
---

# Session Retro: 2026-09-01 - Per-Task Identity Environment Variables

**TL;DR:** A command running inside a relay job had no way to find out which job it belonged to, so
it could not link back to itself. The motivating case is a build step that posts a message into chat
with a link to the job's page. This session gave every task four environment variables: the job and
task ids, plus two web links the server builds from a new setting. The feature itself was small. The
hard part was making the values trustworthy, and the original plan for that did not work: it relied
on relay writing its own value last so that a job's own settings could not overwrite it, which
protects nothing when the server has no link to write, and that is the default. The fix was to
delete the job's competing values outright instead. Review also found that the new setting printed
an operator's password into the startup log when the value was mistyped, and that it accepted
addresses which read as the server's own but resolve to someone else's domain.

## Handoff

Merged as `4ad8d65` (PR #166, squash). Closes `feature-2026-08-31-per-task-identity-env-vars`, moved
to `docs/backlog/closed/`.

Shipped: `RELAY_JOB_ID` and `RELAY_TASK_ID` (no configuration needed) plus `RELAY_JOB_URL` and
`RELAY_TASK_URL`, rendered coordinator-side from a new fail-closed `RELAY_PUBLIC_URL` and carried on
`DispatchTask.job_url` / `.task_url` (fields 9, 10). `jobURL`/`taskURL` in `internal/scheduler`;
`parsePublicURL`/`publicURLLine` in `cmd/relay-server/publicurl_config.go`; `NewDispatcher` gained a
fourth parameter rather than a settable field.

**The trust control is a STRIP, not an ordering.** `Runner.Run` removes the four reserved names from
`task.Env` and from the workspace handle's env before merging either, and additionally skips any key
containing an equals sign. Both halves are load-bearing and both were found by review, not by design:

- Ordering alone is vacuous when `RELAY_PUBLIC_URL` is unset. There is no coordinator value to
  append, so a spec's `RELAY_JOB_URL` is the only occurrence and `os/exec` has nothing to dedup it
  against. Reproduced through the real `Runner`.
- A key CONTAINING an equals sign is a different string to a name predicate and the same variable to
  `dedupEnvCase`, which keys on everything before the first one. A spec key
  `RELAY_JOB_URL=https://evil.example/jobs/x?t` with value `1` reaches the child as
  `RELAY_JOB_URL` set to `https://evil.example/jobs/x?t=1`, attacker-controlled end to end. Refused
  as a class; do not reimplement `dedupEnvCase`'s key parse, which carries its own leading-equals
  carve-out.

`isReservedIdentityNameFor(goos, k)` folds case with `strings.ToLower` on both sides under a Windows
arm, which is `dedupEnvCase`'s exact relation (`EqualFold` diverges on U+017F). The `goos` seam
exists so both halves of the rule die on every platform; the Go CI lanes are `ubuntu-latest` only.

`parsePublicURL` renders NOTHING derived from the input in any rejection branch, and
`TestParsePublicURL_Rejects` and `TestParsePublicURL_RejectionDoesNotLeakAPassword` hold every
message to a closed set of eleven fixed strings. That structural assertion is the pin: three
separate per-branch attempts each left a different hole (`Redacted()` substitutes nothing for a
userinfo with no password; `url.Error` and `EscapeError` embed slices of the input; the port check
fired ahead of the userinfo check).

Still open and deliberately not built:
`feature-2026-09-01-strip-inherited-relay-identity-vars` (the agent's own `os.Environ()` is
inherited unfiltered; `TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone` pins the
current behaviour so a change is a visible RED) and
`feature-2026-09-01-validate-env-keys-in-the-job-spec-pipeline` (the equals refusal is silent on the
agent instead of a 400 at submit; note `schedrunner` re-validates stored specs at fire time, so that
tightening is retroactive).

Gates, all re-run by the conductor rather than taken from an agent report: 22 packages default,
both `go vet` lanes, `-tags integration -p 1` over `internal/scheduler`, `internal/worker` and
`cmd/relay-server`, and `-race` with zero data races in the `golang:1.26` container. Note
`internal/worker` integration needs `-timeout 300s`; it takes about 150s and times out at 120s.

Next session starts at the Now section of `ROADMAP.md`.

## What Was Built

- `parsePublicURL` - fail-closed, following `api.ParseCORSOrigins` rather than the warn-and-default
  numeric parsers. Rejects a non-http(s) scheme, a missing host (via `Hostname()`, so a port-only
  authority is caught), a non-ASCII host, a port outside 1-65535, an authority ending in a bare
  colon, userinfo, a query, a fragment, and embedded whitespace or control characters.
- `publicURLLine` - an unconditional startup line, the only thing an operator has against a value
  that parses perfectly and names the wrong host.
- `jobURL` and `taskURL` - pure joiners with one empty-argument gate, so the "this field goes on the
  wire empty" decision lives in one place.
- The strip and the equals-key class refusal in `Runner.Run`, plus `isReservedIdentityNameFor`.
- `runner_identity_env_test.go` - 12 tests observing the CHILD process's environment through a
  re-exec'd helper, never `cmd.Env`. That distinction is the whole test design: `os/exec` dedups at
  `Start` time, so `cmd.Env` legitimately holds both a spec entry and relay's, and an assertion on
  it can be written to pass in either direction.

## Key Decisions

- **Two rendered URL fields, not one base field.** The spec refuted its own stated rationale (the
  agent not knowing the origin argues for the BASE coming from the server, not for who
  concatenates) and kept the conclusion on a better argument: agents are upgraded on their
  operator's schedule, so putting the SPA route shape in the agent binary versions the frontend
  routing table against a fleet the server cannot force to upgrade.
- **Fail-closed on an invalid `RELAY_PUBLIC_URL`**, chosen at the spec gate with its cost stated: a
  bad edit takes the coordinator down on its next restart. The argument that won it is that
  warn-and-disable is indistinguishable from unconfigured.
- **A constructor parameter, not a settable field**, so a forgotten wiring is a compile error rather
  than something a `main.go`-parsing structural test has to notice.
- **`os.Environ()` stays inherited**, filed rather than fixed. The trust boundary this feature
  defends is the job spec author, not the agent operator, who already chooses the binary.

## What Went Wrong and What Changes

Ledger: the prior retro's three entries were all promoted or ruled one-off, so none carries.
Promoted lessons that fired this session:
[[feedback_assert_encoding_after_a_programmatic_edit]] and CLAUDE.md's CRLF section were applied
after every programmatic edit (non-ASCII byte counts compared against the pre-edit commit on every
file, every round); [[reference_a_replacement_criterion_must_not_be_already_green]] caught the
backlog item's own acceptance criterion being green at HEAD;
[[feedback_reproduction_outranks_argument]] settled a direct disagreement between two review lenses;
[[feedback_verify_tree_not_subagent_claims]] was applied after every agent batch;
[[feedback_autopilot_squash_merge_resync]]'s "existence is not reachability" section was applied in
this retro's own step 1, where `git cat-file -e` passed on a squash-orphaned SHA and
`merge-base --is-ancestor` refuted it; [[reference_tightening_a_validator_is_retroactive]] is why
the env-key validation was filed instead of built. [[feedback_commit_heredoc_shell]] **recurred**
(below).

- **The backlog item's prescribed remedy was wrong in the default configuration, and nothing in
  spec or plan caught it.** The item, the spec and the plan all reasoned that appending relay's
  values last makes them unspoofable, because `os/exec` keeps the last occurrence of a duplicate
  key. That is true only when there IS a relay occurrence. With `RELAY_PUBLIC_URL` unset, the guard
  on a non-empty URL appends nothing, so a job spec's `RELAY_JOB_URL` is the sole occurrence and
  reaches the subprocess intact. Found by the Phase 4 security lens, reproduced by the conductor
  through the real `Runner`.
  -> **What changes:** when a control protects a value by OUTRANKING a competitor, ask what happens
  when you have no value to enter. If the answer is "the competitor wins uncontested", the control
  is conditional on a configuration, not a property. Prefer removing the competitor to outranking
  it.
  (promoted to [[reference_outranking_a_competitor_is_conditional]])

- **A per-branch sanitiser had a different hole every round, three rounds running.** Round 1 used
  `(*url.URL).Redacted()`, which substitutes nothing for a userinfo with no password. Round 2
  switched the parse branch to the inner error, but `url.Error` and `EscapeError` embed slices of
  the input, and the newly added port check fired ahead of the userinfo check. Each fix was correct
  about the hole it targeted and blind to the next one.
  -> **What changes:** when a sanitiser is applied per-branch and successive review rounds each find
  a new hole in a different branch, stop patching branches and remove the capability - render
  nothing derived from the input at all. Then the property is structural and can be asserted over a
  closed set of outputs rather than enumerated with sentinels.
  (promoted to [[reference_per_branch_sanitiser_remove_the_capability]])

- **A count of call sites hid a lane gap.** The spec said 13 test call sites for `NewDispatcher`;
  the real number was 12 plus `main.go`. The number was the trivial half. The consequence nobody had
  stated is that all 12 sit behind a `//go:build integration` tag, so the default lane COMPILES NONE
  of them and a half-finished signature change is fully green.
  -> **What changes:** when a change enumerates call sites, the useful question is not how many but
  which lanes COMPILE them. A build tag removes a call site from a lane as completely as a `paths:`
  filter removes a guard, and the failure is quieter because it is a compile error nobody runs.
  (promoted: extended [[feedback_guard_must_live_in_a_lane_that_runs_on_the_breaking_commit]] with the
  build-tag trigger)

- **A mutation battery reported all three mutants surviving, and the cause was line endings.** A
  `git stash` round-trip left the working copy CRLF, so the harness's LF-only anchors stopped
  matching and the mutations silently never applied. Caught by the engineer's own applied-check and
  re-run; the conductor then re-ran two of the mutants independently in an isolated tree with the
  application verified by file diff.
  -> **What changes:** on this repo, verify a mutation applied by DIFFING against a saved copy, not
  by assuming a string replace succeeded. An anchor that matched when written can stop matching
  after any operation that rewrites the working copy, and CRLF normalization is one.
  (promoted: extended [[reference_verify_the_mutation_applied]] with the stale-anchor trigger)

- **A close commit with an explicit pathspec committed the add but not the rename's deletion.** The
  `git mv` staged both halves; naming only the destination path in the commit left the source
  deletion staged, which would have left the backlog item in BOTH directories - the exact malformed
  state the close flow exists to prevent. Caught from `git status` after the commit.
  -> **What changes:** an explicit pathspec is still right when other work may be staged, but a
  `git mv` has two paths and the commit must name both. Check `git status` after any
  pathspec-scoped commit, not just before.
  (promoted: extended [[feedback_concurrent_agents_share_one_git_index]] with the rename trap)

- **A PowerShell here-string leaked into a bash heredoc on the first commit of the session
  (CARRIED).** Passing a PowerShell-style quoted here-string to `git commit -m` through the Bash
  tool produced a commit whose subject was a bare at-sign followed by the intended subject. Amended
  immediately. [[feedback_commit_heredoc_shell]] states exactly this rule and it was not applied.
  -> No process change - the memory already states it and the miss was momentary non-compliance,
  not a gap. Recorded so the ledger shows it recurred rather than being retired.

- **A review lens reasoned correctly about a mechanism and drew the opposite conclusion from it.**
  The security lens cited `dedupEnvCase` keying on the first equals sign as its reason the
  equals-key shape was CLOSED; that is precisely why it is open. The correctness lens ran it against
  a real subprocess on both platforms.
  -> **What changes:** already encoded in [[feedback_reproduction_outranks_argument]] and applied.
  Noted because this is the first time on this project the two lenses reached opposite conclusions
  from the same correct premise, which is the case that rule exists for.

## Recommended Backlog Items

Intake, not a priority order.

- See [`feature-2026-09-01-strip-inherited-relay-identity-vars`](../backlog/feature-2026-09-01-strip-inherited-relay-identity-vars.md) - the agent's own `os.Environ()` is still inherited unfiltered
- See [`feature-2026-09-01-validate-env-keys-in-the-job-spec-pipeline`](../backlog/feature-2026-09-01-validate-env-keys-in-the-job-spec-pipeline.md) - the equals-key refusal is silent on the agent instead of a 400 at submit
- See [`idea-2026-08-25-no-e2e-path-from-agent-subprocess-to-task-logs`](../backlog/idea-2026-08-25-no-e2e-path-from-agent-subprocess-to-task-logs.md) - the dispatch direction has the same untested wire as the log direction

## Files Most Touched

- `internal/agent/runner.go` - the env merge, the strip, the equals-key class refusal, `isReservedIdentityNameFor`
- `internal/agent/runner_identity_env_test.go` - 12 child-observed tests; the largest single artifact of the slice
- `cmd/relay-server/publicurl_config.go` - `parsePublicURL` and `publicURLLine`
- `cmd/relay-server/publicurl_config_test.go` - the closed-message-set assertion that pins the no-render rule
- `internal/scheduler/dispatch.go` - the fourth constructor parameter and the two rendered fields
- `internal/scheduler/publicurl.go` - `jobURL` and `taskURL`
- `internal/scheduler/dispatch_test.go` - wire-level assertions against seeded ids, never against the message's own fields
- `internal/agent/runner_reserved_name_test.go` - the `goos` seam table
- `proto/relayv1/relay.proto` - the two new rendered-URL fields
- `README.md` - `RELAY_PUBLIC_URL` and the new task subprocess environment section
