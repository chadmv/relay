# Per-task identity environment variables

- **Date:** 2026-09-01
- **Type:** cross-cutting slice (proto + Go server + Go agent + README). No SQL, no migration, no frontend.
- **Closes:** `docs/backlog/feature-2026-08-31-per-task-identity-env-vars.md`
- **Blocked on:** nothing.
- **Phase:** 1 (design). Phase 2 writes the plan.
- **Gate:** this spec is the human's single review point. Plan through merge runs autonomously after
  sign-off, so every fork below is resolved here with its reasoning written down, and the places a
  reviewer is most likely to want to overrule are marked.

Verified against the tree at `cb8f707`.

---

## 1. Verification of the backlog item's claims

### Confirmed

| Claim | Evidence |
|---|---|
| The subprocess environment is built from exactly three sources | `Runner.Run` in `internal/agent/runner.go`: `env := os.Environ()`, then a loop appending `task.Env`, then a loop appending the workspace handle's `extraEnv`. `cmd.Env = env` inside the per-command loop. Nothing else writes it. |
| The workspace handle contributes only `P4CLIENT` today | `perforce.Handle.Env` returns a one-key map. |
| `DispatchTask` already carries `job_id` | `proto/relayv1/relay.proto`, `message DispatchTask`: `job_id = 2`. |
| Field numbers 1-8 are used, 3 is reserved, so 9 and 10 are free | Same message: `task_id=1`, `job_id=2`, `reserved 3`, `env=4`, `timeout_seconds=5`, `epoch=6`, `source=7`, `commands=8`. |
| The dispatcher populates `job_id` | `Dispatcher.sendTask` builds `dt` with `JobId: uuidStr(claimed.JobID)`. |
| No production code under `internal/agent/` reads `JobId` | Every occurrence outside `_test.go` is the generated accessor. `Runner.Run` uses `r.taskID` for status and log messages and never touches `task.JobId`. |
| The whole `*relayv1.DispatchTask` already reaches the runner | `Agent.handleDispatch` calls `newRunner(task.TaskId, ...)` and then `runner.Run(runCtx, task)`. **There is no plumbing to add in `agent.go`;** two new proto fields are visible to `Runner.Run` for free. |
| The frontend routes are `/jobs/:id` and `/jobs/:id/tasks/:taskId` | `web/src/app/router.tsx`. |
| The agent knows only a gRPC `host:port` | `-coordinator` flag. There is no HTTP origin anywhere on the agent side. |
| A job spec cannot supply the job id | The id is generated server-side at create time. |

### Refuted or corrected

**R1. `RELAY_DATABASE_URL` is NOT the only URL-shaped setting in `cmd/relay-server`, and the
exception is the one that matters.** The item's parenthetical is a uniqueness claim, so it is a claim
about the complement, and it is false: `RELAY_CORS_ORIGINS` is parsed by `api.ParseCORSOrigins`,
which runs `url.Parse`, enforces an `http`/`https` scheme allow-list, requires a non-empty host, and
returns an `error` that `main` turns into `log.Fatalf`. That is precisely the shape this slice needs.
The item's wrong claim would have steered an implementer away from the closest and best in-tree
precedent, toward the numeric-bounds parsers (`parseConnLimit`, `parseAutoEnrollCeiling`,
`parseTrailingLogWindow`) whose warn-and-default contract is the wrong one here. See section 5.3.

**R2. The item's precedence analysis is right about the ORDER and silent about the MECHANISM, and
the mechanism is the entire load-bearing part.** The item says "the current order is process env,
then spec `env`, then workspace env, so a later source wins", stated as though the runner's append
order made it so. The runner only appends; nothing in relay resolves duplicates. Last-wins is a
documented contract of `os/exec`, and the whole precedence guarantee rests on it, so it was verified
against the toolchain this repo pins rather than assumed. See section 4.1.

**R3. "Injecting after means a spec cannot override them at all" is platform-dependent at its edge,
and the direction of the difference is worth knowing.** `os/exec`'s dedup is case-INSENSITIVE on
Windows and case-SENSITIVE everywhere else. So a job spec's `relay_job_id` loses to relay's
`RELAY_JOB_ID` on a Windows agent, and survives on Linux as a genuinely distinct variable named
`relay_job_id`. That is not a defect - a distinct variable is not an override - but README must not
be allowed to grow the sentence "a job spec cannot set any variable resembling these", which is only
true on Windows.

**R4. The item's third acceptance criterion is ALREADY TRUE AT HEAD and is therefore not a
criterion.** It reads: "with it unconfigured, the variable is absent rather than wrong". At HEAD
nothing sets `RELAY_JOB_URL`, so it is absent, so the criterion is green against the unmodified tree
and can be satisfied by doing nothing. Section 10 replaces it with a conjunction that discriminates.

**R5. The item under-specifies the variable set and the URL count; both are settled by the user's
pre-gate decisions.** Title and summary name three variables; Proposal item 3 leaves the task-URL
question open; the decision is four variables and two server-rendered URLs. Recorded so the closing
note does not claim the item was implemented as written.

**R6 (citation hygiene, no design impact).** The item cites `Runner.run`; the method is `Runner.Run`
and there is no `run`. The item also cites five line numbers, which this project's convention
rejects because they rot. This spec cites by symbol.

---

## 2. What is being built

A command running inside a relay task learns which job and task it is, and where a human can watch
it. Four variables reach every task subprocess:

| Variable | Value | Present when |
|---|---|---|
| `RELAY_JOB_ID` | `DispatchTask.job_id`, the canonical `uuidStr` rendering | the dispatch carries a non-empty job id, which relay's own dispatcher always does. No server configuration needed. |
| `RELAY_TASK_ID` | `Runner.taskID`, same rendering | the dispatch carries a non-empty task id, which relay's own dispatcher always does. No server configuration needed. |
| `RELAY_JOB_URL` | server-rendered, `<base>/jobs/<job-id>` | `RELAY_PUBLIC_URL` is set on the server |
| `RELAY_TASK_URL` | server-rendered, `<base>/jobs/<job-id>/tasks/<task-id>` | `RELAY_PUBLIC_URL` is set on the server |

The motivating use is a job step that posts a Slack message with a link back to the job. That
consumer is why the values must be trustworthy, which drives every decision in sections 4 and 5.

**The identity half needs no configuration at all.** `RELAY_JOB_ID` and `RELAY_TASK_ID` work on an
untouched deployment the moment the binaries are upgraded. Only the two URLs need
`RELAY_PUBLIC_URL`.

### Non-goals

- No change to how job specs are validated, stored, or created. The identity variables are injected
  at run time and are **never** materialized into `tasks.env`. Putting a server-generated value into
  the stored spec would make it indistinguishable from a user-supplied one on every later read, and
  the ids do not exist when `jobspec.Validate` runs.
- No agent-side URL construction, and no new agent configuration.
- No frontend change. The routes already exist.
- No new database column, query, index, or migration.
- No stripping of inherited `RELAY_*` values from the agent's own process environment (section 12,
  limitation 1; proposed as a separate backlog item in section 13).
- No credentials, tokens, or worker identity in the subprocess environment. A task must not be able
  to act as its own agent.

---

## 3. Decision 1 - the variable names and their values

**Chosen: the four names above, values rendered by `uuidStr`, and a single absent-or-non-empty
rule.**

The ids on the wire are already the canonical 36-character lowercase hyphenated UUID rendering,
produced by `uuidStr` over the `pgtype.UUID` columns of the claimed row. The agent copies the string
through unchanged. No re-rendering, no normalization, no upper-casing, and no braces.

**The rule that makes the set coherent: relay never sets one of these four variables to the empty
string.** Each is appended only when the value in hand is non-empty. That gives a consumer a single
test - `if [ -n "$RELAY_JOB_URL" ]` or `os.environ.get("RELAY_JOB_URL")` - with no second case for
"set but blank", and it makes "the server has no public URL configured" and "this dispatch predates
the feature" the same observable state, which is the correct outcome for both.

`RELAY_TASK_ID` and `RELAY_JOB_ID` are guarded for emptiness too, even though `r.taskID` is never
empty in practice and `job_id` is always populated by relay's own dispatcher. **Do not read the
section 2 table's "always" as licence to append them unconditionally** - acceptance criterion 7
depends on the guard. The guard costs one condition, covers any future peer on that wire, and one
rule with no exceptions is cheaper to remember than three plus a footnote.

**Rejected: a single `RELAY_JOB` JSON blob.** It forces every consumer to parse before it can use
one field, and shell steps are the dominant consumer.

**Rejected: `RELAY_URL` for the base.** `RELAY_URL` is already the CLI's server-address variable
(README, CLI configuration table). Reusing it in the subprocess environment would silently
reconfigure any `relay` CLI invocation inside a task, which is a real and common thing to do. The
base URL is deliberately **not** exported to the subprocess at all - see section 6.

---

## 4. Decision 2 - the identity block is injected LAST

**Chosen (fixed by the user before this spec): the identity variables are appended after both
existing merge loops, so a job spec's `env` and the workspace handle's env cannot override them.**

The security argument is the reason the feature exists. Job specs are authored by any authenticated
user. A downstream notifier that reads `RELAY_JOB_URL` and posts it into Slack is posting a link
that other humans will click. If a spec author could set `RELAY_JOB_URL=https://evil.example/`, the
feature would be a chat-phishing primitive with relay's name on it. Injecting last means the value a
notifier reads is the one the coordinator rendered, from ids read off the fenced claim row, using a
base URL only the server operator controls.

Shape in `Runner.Run`, after the `task.Env` loop and after the `extraEnv` loop:

```go
env := os.Environ()                  // existing
// ... task.Env loop (existing)
// ... extraEnv loop (existing)
if r.taskID != "" {
    env = append(env, "RELAY_TASK_ID="+r.taskID)
}
if task.JobId != "" {
    env = append(env, "RELAY_JOB_ID="+task.JobId)
}
if task.JobUrl != "" {
    env = append(env, "RELAY_JOB_URL="+task.JobUrl)
}
if task.TaskUrl != "" {
    env = append(env, "RELAY_TASK_URL="+task.TaskUrl)
}
```

The existing comment "Merge env: current process env first, task env overrides, then workspace env"
must be extended to say that the identity block is last and why, and to name the test that pins it.
It must not restate the measurement or where it was verified; that belongs here and in the commit
message.

### 4.1 The stdlib contract this rests on, verified not assumed

`go.mod` declares `go 1.26.2`. Read in `C:\Program Files\Go\src\os\exec\exec.go` at that exact
version:

- The `Cmd.Env` field documentation states: "If Env contains duplicate environment keys, only the
  last value in the slice for each duplicate key is used."
- `(*Cmd).environ()` calls `dedupEnv(env)` unconditionally - the call sits **after** the
  `if env == nil` branch, so it applies to a caller-supplied `c.Env` exactly as it applies to the
  inherited default. `Start` builds the child's environment through `environ()`.
- `dedupEnv` delegates to `dedupEnvCase(runtime.GOOS == "windows", runtime.GOOS == "plan9", env)`,
  whose loop walks the slice backwards under the comment "Construct the output in reverse order, to
  preserve the last occurrence of each key", keeps the first key it sees in that reverse walk, and
  then reverses the result.

So: **last occurrence wins, it is a documented field contract rather than an implementation detail,
and it is case-insensitive on Windows only.** The append-last design is correct as stated by the
user, and the explicit-merge fallback the brief asked me to hold in reserve is not needed.

**Rejected: build a `map[string]string` and flatten it.** It would produce the same child
environment and cost more. Three concrete costs: map iteration order is random, so `cmd.Env` would
become nondeterministic and would need sorting purely to keep tests stable; it discards the
process-inherited entries' original ordering for no benefit; and it silently changes behaviour for
malformed entries, because `dedupEnvCase` deliberately preserves items that are not of `key=value`
form while a naive split-on-`=` flatten would drop or mangle them. The current code is correct and
the minimal diff is the right diff.

**One hazard the flatten would have avoided, recorded so nobody rediscovers it as a bug:** a test
that asserts precedence by reading `cmd.Env` sees BOTH the spec's entry and relay's, because dedup
happens inside `os/exec` at `Start` time. Such a test could be written to pass in either direction
and proves nothing. Section 9 requires the precedence test to observe the **child process's own
environment**.

---

## 5. Decision 3 - `RELAY_PUBLIC_URL`

### 5.1 What it is

One server-side setting naming the browser-facing origin, optionally with a path prefix, at which
the relay web UI is reachable. Read once at startup. Unset means the URL half of the feature is off.

### 5.2 Accepted and rejected values

`parsePublicURL(name, raw string) (string, error)`:

1. `s := strings.TrimSpace(raw)`. If `s == ""`, return `("", nil)`. Unset and whitespace-only are the
   same thing, matching `ParseCORSOrigins`.
2. Reject if `s` contains any byte below `0x21` or equal to `0x7f` (space, tab, newline, control
   characters). No legitimate base URL contains any of these, and whitespace in a value that a shell
   step interpolates unquoted is the realistic footgun.
3. `u, err := url.Parse(s)`. Reject on error.
4. Reject unless `u.Scheme` is exactly `http` or `https`. `url.Parse` lower-cases the scheme, so
   `HTTPS://relay.example.com` is accepted and normalizes.
5. Reject if `u.Host == ""`.
6. **Reject if `u.User != nil`.** A base URL carrying userinfo is both a credential in an
   environment variable and a classic phishing shape (`https://relay.example.com@evil.example/`)
   that would be rendered into every link relay posts. `ParseCORSOrigins` does not check this,
   correctly, because an `Origin` header never carries userinfo; a base URL does.
7. Reject if `u.RawQuery != ""`, `u.ForceQuery`, or `u.Fragment != ""`. A base that carries a query
   or fragment cannot have a path appended to it and produce a working link.
8. Normalize: `path := strings.TrimRight(u.EscapedPath(), "/")`. Result is
   `u.Scheme + "://" + u.Host + path`. `u.Host` already carries the port.

`u.EscapedPath()` rather than `u.Path` so an operator's percent-encoding survives. Explicit assembly
rather than `u.String()`: today they agree because every other component has been rejected, and
explicit assembly cannot resurrect a component if a future edit stops rejecting one.

Worked normalizations, which the plan should turn directly into table-driven cases:

| Input | Result |
|---|---|
| _(unset)_ or `"   "` | `""` (feature off) |
| `https://relay.example.com` | `https://relay.example.com` |
| `https://relay.example.com/` | `https://relay.example.com` |
| `https://relay.example.com///` | `https://relay.example.com` |
| `HTTPS://Relay.Example.com` | `https://Relay.Example.com` (scheme lower-cased by `url.Parse`; host case preserved, and DNS does not care) |
| `http://10.0.0.5:8080` | `http://10.0.0.5:8080` |
| `https://ops.example.com/relay/` | `https://ops.example.com/relay` |
| `https://relay.example.com@evil.example/` | rejected (userinfo) |
| `ftp://relay.example.com` | rejected (scheme) |
| `relay.example.com` | rejected (no scheme, and `url.Parse` puts it in `Path` leaving `Host` empty) |
| `https://relay.example.com/?x=1` | rejected (query) |
| `https://relay.example.com/#frag` | rejected (fragment) |
| `https://relay example.com` | rejected (space) |

### 5.3 Failure policy: reject at startup

**Chosen: an invalid `RELAY_PUBLIC_URL` is fatal. `parsePublicURL` returns an `error` and `main`
calls `log.Fatalf`, exactly as it already does for `RELAY_CORS_ORIGINS`.**

The repo has two established policies and the choice between them turns on one question.

The numeric-bounds family (`parseConnLimit`, `parseGRPCConnIdle`, `parseAutoEnrollCeiling`,
`parseTrailingLogWindow`, `parseWatchdogDuration`) warns and falls back to a default, and each says
why in its own comment: "a bad limit must not stop a server booting when a safe default exists". The
fail-closed family (`ParseCORSOrigins`, `RELAY_ALLOW_AUTO_ENROLL`, `RELAY_ALLOW_SELF_REGISTER`,
`ParseRateLimit`) refuses to boot.

Three reasons this belongs in the second family:

1. **The degraded mode is indistinguishable from the unconfigured mode.** A warn-and-disable typo
   produces exactly "the URL variables are absent", which is what an operator who never set the
   variable also sees. `parseAutoEnrollCeiling`'s own comment names this as the thing to avoid ("a
   silently-ignored typo would leave an operator believing they had tightened a bound they had
   not"); it settles for a warning only because it cannot fail-fast a bound safely. Here we can.
2. **There is no "reasonable but wrong" middle.** A number typed with the wrong units is still a
   number and a running server can honour it. A URL either parses into a usable base or it does not.
   Falling back to a default is not available either, because there is no defensible default origin
   - guessing one is the exact failure mode the item was written to prevent.
3. **Two of the rejections are security rejections.** Accepting a userinfo-bearing or non-HTTP base
   and quietly rendering it into links is worse than not booting.

**The honest cost, stated so a reviewer can overrule it at the gate:** a deployment whose
`RELAY_PUBLIC_URL` is edited badly will fail to come back up on its next restart, taking the whole
coordinator with it over a cosmetic setting. That is the trade. It is mitigated by the value being
read once at boot with an error message that names the variable and the specific rule broken, and by
the unconditional startup line in 5.4 which shows the effective value on every successful boot.

**The rejection message must not leak a credential.** Every rejection after step 3 renders the value
through `(*url.URL).Redacted()`, which is the standard library's own answer to this problem
(username preserved, password replaced). The single branch that echoes the raw trimmed value is the
`url.Parse` failure in step 3, where there is no structured URL to redact; `url.Parse` almost never
fails, so that branch is near-dead by construction, which is the right place for the one concession.

### 5.4 The startup line

`publicURLLine(base string) string`, logged unconditionally, in the shape of `grpcBoundsLine` and
`autoEnrollCeilingLine`:

- `base == ""`: says the public URL is not configured, that `RELAY_JOB_URL` and `RELAY_TASK_URL` are
  therefore not injected, and that `RELAY_JOB_ID` and `RELAY_TASK_ID` still are.
- otherwise: names the effective base and shows both rendered shapes with `<job-id>` and `<task-id>`
  placeholders.

This is the only defence against the failure the validator cannot catch: a value that parses
perfectly and points at the wrong host. A fail-closed parser plus a silent success is half a
control.

### 5.5 The joining rule

Two unexported pure functions in `internal/scheduler`:

```go
func jobURL(base, jobID string) string
func taskURL(base, jobID, taskID string) string
```

- Any empty argument yields `""`. That is the single gate for "the field goes on the wire empty",
  and it means the emptiness decision lives in one place rather than at the call site.
- Otherwise `base + "/jobs/" + jobID` and `base + "/jobs/" + jobID + "/tasks/" + taskID`.
- Plain concatenation with no separator logic, because `parsePublicURL` guarantees `base` has no
  trailing slash. That guarantee is the reason normalization happens at parse time and not here.
- The ids are used verbatim and are not escaped. The premise: both are `uuidStr` output over
  `pgtype.UUID` values read from the claimed row, so they can only contain `[0-9a-f-]`. If task or
  job ids ever stop being UUIDs, the escaping question reopens - which is why the premise is written
  down here rather than assumed.

---

## 6. Decision 4 - two rendered URL fields, not one base field

**Chosen: `string job_url = 9;` and `string task_url = 10;` on `DispatchTask`. The server renders
both. The base URL never crosses the wire and is never exported to the subprocess.**

The user's constraint was "the agent must not build URLs". The brief asked me to check whether the
stated argument survives, and **it does not, on its own terms**: "the agent has no idea what origin a
browser reaches the server on" is an argument for the BASE coming from the server, not an argument
about who performs the concatenation once the base is on the wire. If `public_url = 9` carried the
normalized base, the agent would know everything it needs to build both URLs.

The conclusion survives on a different argument, which is stronger:

**Agents are independently deployed and long-lived; the frontend route shape is not theirs to
know.** An agent persists a token, reconnects across server restarts, and is upgraded on its
operator's schedule, not the server's. Putting `/jobs/%s/tasks/%s` in the agent binary versions the
SPA's routing table against a fleet the server cannot force to upgrade. The day
`/jobs/:id/tasks/:taskId` moves, option B needs every agent in the fleet redeployed, and until then
old agents emit dead links, silently, with nothing red anywhere. Rendering server-side makes a route
change a server-only deploy.

Secondary, smaller: two fields mean one empty-base branch (in `jobURL`/`taskURL`) instead of two
implementations of it on both sides of the wire, and a new agent talking to an old server needs no
version negotiation - the fields are simply empty, which is already the "not configured" state the
design handles.

The cost is about 90 bytes of shared prefix per dispatch, at dispatch rates. It does not matter.

**Rejected: one `job_url` field with the agent appending `/tasks/<task-id>`.** Same route-shape
coupling as the base-field option, with the added defect that it hard-codes the assumption that the
task route is nested under the job route, which is the part most likely to change.

Proto3 scalars: an empty string is the default and is not serialized, so an unconfigured server
sends nothing extra on the wire. Old agent plus new server, and new agent plus old server, both
degrade to "URL variables absent" with no branch anywhere.

---

## 7. Decision 5 - the wiring path, by symbol

```
os.Getenv("RELAY_PUBLIC_URL")
  -> parsePublicURL              (cmd/relay-server/publicurl_config.go)   [new]
  -> log.Fatalf on error         (cmd/relay-server/main.go)
  -> log.Print(publicURLLine(..))(cmd/relay-server/main.go)               [new line]
  -> scheduler.NewDispatcher(q, registry, broker, publicBaseURL)          [signature change]
  -> Dispatcher.publicBaseURL    (internal/scheduler/dispatch.go)         [new field]
  -> jobURL / taskURL            (internal/scheduler/publicurl.go)        [new]
  -> DispatchTask.JobUrl / .TaskUrl in Dispatcher.sendTask
  -> gRPC stream, via worker.Registry.Send (unchanged)
  -> Agent.handleDispatch -> Runner.Run(runCtx, task)                     (unchanged)
  -> cmd.Env in Runner.Run       (internal/agent/runner.go)
```

**Which config pattern is being followed, precisely, since the brief asks:** neither of the two
in-tree families wholesale. The **error-returning signature and the `log.Fatalf` at the call site**
come from `api.ParseCORSOrigins`, for the reasons in 5.3. The **file layout** (`*_config.go` plus
`*_config_test.go` in `cmd/relay-server`, parsing kept out of `main` so it is an ordinary tested
function) and the **unconditional startup line** come from the `grpc_config.go` /
`autoenroll_config.go` family. `parsePublicURL` takes the variable name as its first parameter, as
`parseConnLimit` and `parseAutoEnrollCeiling` do, so the message names the variable without the
literal being duplicated.

### 7.1 Constructor parameter, not a settable field

`NewDispatcher` gains a fourth parameter rather than the dispatcher gaining an exported field set
after construction, which is the shape `agentHandler.TrailingLogWindow` and
`agentHandler.AutoEnrollWorkerCeiling` use.

The reason is that a settable field can be forgotten in `main`, and forgetting it produces exactly
the failure this design spent section 5.3 refusing to accept: URLs silently absent, indistinguishable
from unconfigured, with everything green. The repo's existing answer to that risk is a structural
test that parses `main.go` (`TestGRPCAdmissionIsWiredByMain`, `TestServerCountersIsWiredByMain`), and
`grpc_config.go`'s own comment records that such a test has a disclosed blind spot. A constructor
parameter converts the whole question into a compile error and needs no guard test at all.

Cost: 13 call sites in `internal/scheduler/*_test.go` and `internal/worker/handler_test.go` gain a
`""` argument. That is mechanical, and it has a side benefit - every existing dispatcher test then
says out loud that it runs with no public URL. Historical plan documents under
`docs/superpowers/plans/` also contain the old call shape and **must not be edited**; they are
records of a moment.

There is no same-typed-adjacent-argument transposition risk: `*store.Queries`, `*worker.Registry`,
`*events.Broker`, `string` are four distinct types.

### 7.2 Ordering constraint in `main`

`scheduler.NewDispatcher` is called well above the `api.ParseCORSOrigins` block, so the parse and
the startup line must move up to sit immediately before the dispatcher construction. A `log.Fatalf`
in that position is consistent with the several that already fire later at boot.

### 7.3 Rendering from the claimed row

In `Dispatcher.sendTask`, both ids are already read from `claimed` - the `RETURNING` row of the
fenced `ClaimTaskForWorker` - and not from the pre-claim scan row `task`. The URLs must be rendered
from the same two locals:

```go
jobIDStr := uuidStr(claimed.JobID)
taskIDStr := uuidStr(claimed.ID)
dt := &relayv1.DispatchTask{
    TaskId:  taskIDStr,
    JobId:   jobIDStr,
    JobUrl:  jobURL(d.publicBaseURL, jobIDStr),
    TaskUrl: taskURL(d.publicBaseURL, jobIDStr, taskIDStr),
    // ... unchanged fields
}
```

This is the same discipline the function's existing comment states for the requeue fences: source
the facts from one row so they cannot drift apart.

### 7.4 Regeneration

`make generate` after the `.proto` edit. Per CLAUDE.md, sqlc and protoc emit LF on this CRLF repo:
check the diffstat against the size of the intended change, run `git ls-files --eol` on the touched
paths and confirm `i/lf`, and revert LF-only hunks. No `.sql` file is touched by this slice, so the
generated-store half of that hazard does not apply.

---

## 8. Invariants, threat model, and behaviour under load

### 8.1 Invariant compliance

- **Epoch fence.** This slice writes no `tasks.status` and no `task_logs`. It adds two strings to a
  message already built after `ClaimTaskForWorker` returned. Untouched.
- **Single job-spec pipeline.** Untouched, and deliberately so: the identity variables are injected
  at run time and never enter `jobspec.TaskSpec` or `tasks.env`. A spec that happens to contain
  `RELAY_JOB_ID` is still stored and still dispatched verbatim; it simply loses at exec time.
- **One bounded sender per gRPC stream.** No new send. The dispatch message is marginally larger.
- **Identity-checked teardown / no interior pointers across locks / single JSON entry point.** Not
  reached.
- **End the generation before releasing the resource.** No generation, no resource, no async
  lifecycle introduced.

### 8.2 Threat model

| Principal | Can they forge a variable? |
|---|---|
| Job spec author (any authenticated user) | **No.** The four names are stripped from their `env` before it is merged. (Implemented as a strip rather than the append-last ordering this section originally described: with no `RELAY_PUBLIC_URL` set there is no relay value to append, so ordering alone left the spec's entry as the only occurrence.) This is the property the whole design exists to provide. |
| Workspace provider (`perforce.Handle.Env`) | **No.** Same reason; `extraEnv` is stripped before it is merged too. |
| A task subprocess | Only for its own children, which is inherent and uninteresting. |
| Server operator | Yes, by definition - they set `RELAY_PUBLIC_URL`. Constrained to an `http`/`https` URL with no userinfo, no query, no fragment, and no whitespace. |
| Agent operator | **Yes**, by exporting `RELAY_JOB_URL` into `relay-agent`'s own environment; relay will not override it when it has no value of its own. See section 12, limitation 1. This principal already chooses the agent binary and owns the machine that runs the subprocess, so there is nothing to defend. |
| A malicious coordinator | Yes. An agent trusts its coordinator by construction; that is the trust relationship the enrollment token establishes. |

Injection surface: the value lands in an environment variable, not in a shell command line. A task
that does `curl $RELAY_JOB_URL` unquoted could be affected by whitespace or a metacharacter in the
base, which is why step 2 of 5.2 rejects whitespace. A metacharacter inside a valid URL path
(`https://x.example/a;b`) is accepted and is self-inflicted by the operator who typed it; noted, not
gated, because a stricter path character class would start rejecting legitimate reverse-proxy
prefixes.

### 8.3 Load and failure modes

- Two extra strings per dispatch. No new query, no round trip, no allocation on any hot loop.
  `parsePublicURL` runs once, at boot.
- Four extra environment entries per subprocess, roughly 200 bytes against a 32767-character Windows
  environment block limit.
- `RELAY_PUBLIC_URL` set to a value that parses but points at the wrong host: links are wrong,
  nothing breaks, and the startup line shows the operator what the server believes.
- `RELAY_PUBLIC_URL` unset: the ids still flow. The feature degrades to its configuration-free half.
- Version skew in both directions degrades to "URL variables absent" (section 6).

---

## 9. Testing

RED-first is achievable for every behavioural criterion here except the parser's, whose subject does
not exist at HEAD. Where the honest RED is only "the symbol does not compile", section 10 pairs the
criterion with a behavioural outcome so it is not merely a naming test.

### 9.1 `internal/agent` - the subprocess environment, observed from the child

The default lane, no Docker. The critical constraint: **every assertion about precedence and about
presence must read the CHILD process's environment, never `cmd.Env`.** `os/exec` dedups at `Start`
time, so `cmd.Env` legitimately contains both the spec's entry and relay's, and a test that inspects
it can be written to pass in either direction.

Use the `os/exec` helper-process idiom: dispatch a task whose command is
`[]string{os.Args[0], "-test.run=TestRunnerEnvHelperProcess"}`, with `GO_WANT_HELPER_PROCESS=1`
placed in `task.Env` (not in a `RELAY_*` name, so it does not collide with the subject), and have the
helper dump `os.Environ()` to stdout. The runner already streams stdout through `chunkWriter` into
`sendCh`, which existing runner tests drain, so no new plumbing is needed. This idiom is
cross-platform with no shell-quoting branch and, unlike `echo $VAR` / `echo %VAR%`, it distinguishes
absent from present-and-empty exactly.

1. `RELAY_TASK_ID` and `RELAY_JOB_ID` in the child equal the dispatched ids.
2. `RELAY_JOB_URL` and `RELAY_TASK_URL` in the child equal `DispatchTask.JobUrl` / `.TaskUrl`
   verbatim.
3. **Precedence.** A dispatch whose `task.Env` sets all four names to `SPOOFED` yields a child that
   sees the dispatched values, not `SPOOFED`. This is the headline security test.
4. **Workspace precedence.** A `fakeHandle` whose `Env()` returns `RELAY_JOB_URL=SPOOFED` loses too.
   `fakeHandle` already exists in `runner_test.go` and returns a one-key map; this needs a variant.
   Without this case, moving the identity block between the two existing loops passes.
5. **Absence.** A dispatch with `JobUrl` and `TaskUrl` empty yields a child whose environment has no
   key `RELAY_JOB_URL` and no key `RELAY_TASK_URL`, while `RELAY_JOB_ID` and `RELAY_TASK_ID` ARE
   present. The conjunction is what makes this discriminating; see R4 and section 10.
   To make it hermetic, clear any ambient value first with the `t.Setenv` then `os.Unsetenv` idiom -
   `t.Setenv` registers the restore whether or not the variable was originally set.
6. **No empty-string variable.** A dispatch with an empty `JobId` yields a child with no
   `RELAY_JOB_ID` key at all, rather than `RELAY_JOB_ID=`.
7. **Inheritance is pinned, not assumed.** With the agent process's own environment carrying
   `RELAY_JOB_URL=INHERITED` and a dispatch carrying an empty `JobUrl`, the child sees `INHERITED`.
   This test **documents limitation 1 as a behaviour rather than a comment**, and it is the test that
   must go RED if a later slice decides to start stripping.

### 9.2 `cmd/relay-server` - `publicurl_config_test.go`

Table-driven over every row of the 5.2 table, plus:

8. The rejection message for a userinfo-bearing value does **not** contain the password substring.
   Feed `https://user:hunter2@relay.example.com` and assert `hunter2` is absent from the error
   string. This is the only test that pins 5.3's redaction rule.
9. `publicURLLine("")` names both URL variables as not injected and both id variables as still
   injected; `publicURLLine(base)` contains the base. Assert on substrings that would survive
   rewording only if the fact survives - name the variables, not the sentence.

### 9.3 `internal/scheduler` - `publicurl_test.go` (default lane, no Docker)

10. `jobURL` and `taskURL` over: a bare base, a base with a path prefix, an empty base, an empty job
    id, an empty task id.

### 9.4 `internal/scheduler` - integration lane

`dispatch_test.go` is `//go:build integration` and already has a `fakeSender` capturing
`*relayv1.CoordinatorMessage`.

11. A dispatcher constructed with a base URL sends a `DispatchTask` whose `JobUrl` and `TaskUrl`
    match the claimed row's ids. **Assert the ids in the URL against the ids the test seeded**, not
    against `dt.JobId`, or the test agrees with itself by construction and cannot see the URLs being
    rendered from the wrong row.
12. A dispatcher constructed with `""` sends a `DispatchTask` with both URL fields empty.

### 9.5 Mutation battery

Each must redden a permanent test, and each must leave its discriminating input behind. Run these in
an isolated tree, never in a worktree a sibling agent is reading. Never revert a mutation with
`git checkout --`; restore from a copy and re-run a control that should die.

| Mutation | Killed by |
|---|---|
| Move the identity block above the `task.Env` loop | 3 |
| Move the identity block between the `task.Env` and `extraEnv` loops | 4 |
| Drop the `if task.JobUrl != ""` guard (always append) | 5, 6 |
| Swap the `jobID`/`taskID` arguments at the `taskURL` call site | 11, provided the seeded ids differ in more than order - use two visibly distinct UUIDs |
| Render the URLs from `task` instead of `claimed` | 11, only if the test seeds a row whose pre-claim and post-claim identity can differ; if it cannot, say so rather than claiming the mutation is covered |
| Drop the `u.User != nil` check | the 5.2 table row plus 8 |
| Change `TrimRight(path, "/")` to `TrimSuffix` | the `https://relay.example.com///` row |
| Make `parsePublicURL` return a warning instead of an error | acceptance criterion 8 |

### 9.6 Lanes to run

`make test` for everything in 9.1, 9.2, 9.3. `make test-integration -run` over `internal/scheduler`
for 9.4. `make test-race` matters here because the identity block is appended inside `Runner.Run`,
which runs one goroutine per task; on Windows use the Linux container route from CLAUDE.md, and if
the lane is genuinely unavailable, say so rather than substituting `-count=N`.

---

## 10. Acceptance criteria

Each is falsifiable and each was checked to be **FALSE against HEAD** before being written down.

1. A task subprocess dispatched by relay observes `RELAY_TASK_ID` equal to the dispatched task id
   and `RELAY_JOB_ID` equal to the dispatched job id, both read from the child's own environment.
   *(HEAD: neither name is ever written.)*
2. A dispatch whose job spec `env` sets `RELAY_JOB_ID`, `RELAY_TASK_ID`, `RELAY_JOB_URL` and
   `RELAY_TASK_URL` to attacker-chosen values yields a subprocess that observes the coordinator's
   values for all four. *(HEAD: the spec's values reach the subprocess unopposed.)*
3. A workspace handle whose `Env()` names any of the four loses to the coordinator's value.
   *(HEAD: the workspace env is the last writer.)*
4. With `RELAY_PUBLIC_URL=https://relay.example.com`, a subprocess of task T in job J observes
   `RELAY_JOB_URL=https://relay.example.com/jobs/J` and
   `RELAY_TASK_URL=https://relay.example.com/jobs/J/tasks/T`. *(HEAD: no setting, no field, no
   variable.)*
5. With `RELAY_PUBLIC_URL=https://ops.example.com/relay/`, the rendered job URL is
   `https://ops.example.com/relay/jobs/J` - one slash, prefix preserved. *(HEAD: as above.)*
6. With `RELAY_PUBLIC_URL` unset, a subprocess observes **no key** `RELAY_JOB_URL` and **no key**
   `RELAY_TASK_URL`, and **does** observe `RELAY_JOB_ID` and `RELAY_TASK_ID`. *(HEAD: all four are
   absent, so the absence half alone is already green - see R4. The conjunction is false at HEAD and
   is the criterion.)*
7. No relay-injected identity variable is ever present with an empty value: a dispatch carrying an
   empty `job_id` produces a subprocess with no `RELAY_JOB_ID` key, while a dispatch carrying a
   non-empty one produces the key with that value. *(HEAD: the second half is false.)*
8. `relay-server` refuses to start when `RELAY_PUBLIC_URL` is set to a value with userinfo, a
   non-`http(s)` scheme, no host, a query, a fragment, or embedded whitespace. *(HEAD: the variable
   is not read at all, so the server starts.)*
9. The startup failure for a userinfo-bearing value does not contain that value's password.
   *(HEAD: no such message exists.)*
10. Every successful boot logs one line naming the effective public URL, or naming its absence and
    which two variables are consequently not injected. *(HEAD: no such line.)*
11. `DispatchTask` carries `job_url` and `task_url`, rendered from the ids of the row
    `ClaimTaskForWorker` returned. *(HEAD: the fields do not exist.)*
12. An agent built from this slice, connected to a server that leaves both URL fields unset, injects
    the two ids and neither URL. *(HEAD: it injects nothing.)*
13. README documents all four subprocess variables, the precedence rule, the absent-not-empty rule,
    `RELAY_PUBLIC_URL`, and the two limitations in section 12. *(HEAD: none of it is documented.)*

---

## 11. Documentation

1. **README, `## relay-server` -> `### Configuration` table.** One row for `RELAY_PUBLIC_URL`, placed
   after `RELAY_GRPC_ADDR` with the other addressing settings. It must state: default empty; the
   accepted shape; that a path prefix is supported; that an invalid value **refuses to boot**; and
   that leaving it unset means `RELAY_JOB_URL` and `RELAY_TASK_URL` are absent rather than guessed.
2. **README, `## relay-agent`, a new `### Task subprocess environment` subsection** after the
   existing `### Environment variables` table (which documents the agent's OWN process environment -
   the two are different things and the new heading must make that plain). It carries the four-row
   table from section 2, then:
   - the precedence rule, stated as a guarantee a notifier may rely on: a job spec cannot override
     these four names;
   - the absent-or-non-empty rule, so consumers write one check;
   - limitation 1 (agent process environment) and limitation 2 (Windows case-folding) from section
     12. Per R3, the precedence sentence must be scoped to these exact names and must not be written
     as a claim about variables resembling them.
3. **`Runner.Run`'s existing merge comment** gains the fact that the identity block is last and that
   the guarantee rests on `os/exec` keeping the last occurrence of a duplicate key - a hazard the
   code genuinely cannot show - and cites the test that pins it. Per CLAUDE.md's comment policy it
   must not carry the verification narrative, the Go version it was checked against, or a count of
   anything. Those live in this spec and in the commit message.
4. **No CLAUDE.md change.** No invariant moves and no new carve-out is created.

---

## 12. Known limitations, stated so nobody has to rediscover them

1. **The agent's own process environment still shows through when relay has no value.** If
   `relay-agent` is started from a shell that exported `RELAY_JOB_URL`, every task inherits it and
   relay does not override it, because relay only appends when it has a non-empty value. The trust
   boundary this feature defends is the **job spec author**, not the agent operator, and the agent
   operator already chooses the binary and owns the machine. The four names should be documented as
   reserved. Test 7 in section 9.1 pins the behaviour so a later decision to strip is a visible RED,
   not a silent change.
2. **Windows folds case; other platforms do not.** A job spec's `relay_job_id` loses to
   `RELAY_JOB_ID` on a Windows agent and survives as a distinct variable elsewhere. Neither is a
   defect; the asymmetry must not be papered over in README (R3).
3. **A path prefix is a string, not a route rewrite.** relay serves the embedded SPA from its own
   routes; setting `RELAY_PUBLIC_URL=https://ops.example.com/relay` produces links that work only if
   the operator's reverse proxy actually strips `/relay`. relay cannot verify this and does not try.
4. **Nested relay is confusing by construction.** A task that itself submits a relay job runs with
   its parent's `RELAY_JOB_ID` in scope, so a naive script that reads the variable after submitting
   gets the parent's id. Out of scope; worth one README sentence only if it costs one sentence.
5. **The `log.Fatalf` call itself is untested**, as with every other `main`-resident Fatalf in this
   binary. `parsePublicURL`'s error is fully tested; the decision to make it fatal is one line in
   `main` protected only by sitting adjacent to its identical siblings.
6. **A running deployment can be bricked by a bad edit to `RELAY_PUBLIC_URL`** on its next restart.
   This is the accepted cost of 5.3 and the most likely place a reviewer will want to overrule.

---

## 13. Backlog recommendations

**One proposal, not filed.** Backlog items are never auto-filed; this is offered for the human's
acceptance at the gate.

- *Strip inherited relay identity variables when relay has no value of its own.* Would make "absent"
  absolute rather than "absent unless the agent operator exported it" (limitation 1), by filtering
  `os.Environ()` for the four reserved names before the merge. Cost: a filter loop plus a
  case-insensitive comparison on Windows to be honest about it. Benefit: removes a state where a
  stale debugging export silently poisons every link relay posts. Deliberately out of this slice
  because it defends against an already-fully-trusted principal, and because section 9.1 test 7
  makes the current behaviour a pinned, findable fact rather than an unexamined one.

Nothing else was split out. R1 through R6 are corrections to the backlog item itself and are
recorded here rather than filed.
