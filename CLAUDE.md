# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build all three binaries into bin/
make build

# Unit tests (no Docker required)
make test

# Integration tests (requires Docker Desktop running and the `p4` CLI on PATH;
# spins up Postgres and p4d containers; -p 1 prevents parallel container conflicts)
make test-integration

# CLI real-server integration lane (internal/cli only): every test drives a live
# internal/api server over HTTP. Needs Docker, or set RELAY_TEST_DATABASE_URL to a
# running Postgres for one fresh database per test instead of one container per test.
make test-cli-integration

# Regenerate sqlc store layer and protobuf bindings after editing .sql or .proto files
make generate

# Browser end-to-end suite (Playwright). Needs node, go, and a Postgres at
# postgres://relay:relay@127.0.0.1:5432 - the container scripts/dev.ps1 manages.
# Install the browsers once: cd web && npx playwright install chromium webkit
# Read web/e2e/README.md first - it is the live document for what is and is not covered.
make test-e2e

# Race detector. CI runs this (.github/workflows/go-ci.yml, `race + integration-build`).
# NOTHING ON THIS REPO BLOCKS A MERGE. Verified 2026-08-27: `main` has no branch
# protection and no rulesets (the protection API returns 404 "Branch not protected";
# the rulesets API returns []), so every check reports red or green and the merge
# button works either way, and a direct push to `main` bypasses PRs entirely. This is a
# deliberate choice for a solo repo, not an oversight - the gate is the convention that
# you run these locally and do not merge red, which is why the local invocation below
# matters more here than it would on a protected repo. Do not describe any check as a
# "merge gate": it was described that way for two months and it was never true.
make test-race

# ...but on Windows the native lane is unreliable, and the container below is the
# route that actually works. See "Running -race locally" after this block.
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s

# Run a single test
go test ./internal/api/... -run TestRegister_HappyPath -v -timeout 30s

# Run integration tests for one package
go test -tags integration -p 1 ./internal/api/... -run TestRegister -v -timeout 120s
# The whole internal/api integration package runs about 9.5 minutes; a 600s timeout is inside
# its variance band and reports FAIL with no --- FAIL line beneath it. Use -timeout 1800s.
```

Integration tests use `//go:build integration` and spin up real Postgres containers via testcontainers-go. On Windows the `desktop-linux` Docker context is used automatically.

### Running `-race` locally

`make test-race` is the canonical target and the Makefile comment above it carries the compiler
fix. On Windows the lane has **two distinct failure modes and they are easy to confuse** - the
second one presents exactly like a real regression:

- **Compiler.** `-race` needs cgo with a working gcc. The default Strawberry Perl gcc fails with
  `exit status 0xc0000139` on every package. Fix: MSYS2 mingw64, `CC=/c/msys64/mingw64/bin/gcc.exe`
  with its `bin` on PATH.
- **Runtime.** Even with the right `CC`, ThreadSanitizer can fail to allocate its shadow arena:
  `ThreadSanitizer failed to allocate 0x000004670000 bytes (error code: 87)`. This is
  **environmental, memory-pressure related, and intermittent** - on 2026-08-25 it reproduced on
  `internal/tokenhash` (a trivial, untouched package) at `origin/main`. **Distinguishing symptom:**
  the failure names ThreadSanitizer and an allocation, and it is not attached to any test. Before
  concluding a change caused it, re-run at `origin/main` on an untouched package - the project's
  measure-a-red-gate-both-ways rule applies here first.

**The Linux container is the reliable route on this machine**, and it closes a second gap for free:
`go test` on Windows silently skips every `//go:build !windows` file (`internal/agent/runner_cancel_test.go`
among them), so the container is also the only local way to run those at all. Verified green across
all 21 packages, zero data races, on 2026-08-25.

**If the lane is genuinely unavailable, say so rather than substituting.** `-count=N` repetition is
what one slice used instead, and it is NOT equivalent: it re-runs tests under the ordinary scheduler
and cannot observe an unsynchronised access that never happens to interleave badly. It raises
confidence in flakiness, not in race-freedom. State plainly that `-race` did not run.

### Line endings: this is a CRLF repo and the tooling normalizes inconsistently

The `make generate` note under `internal/store/` below is the best-known instance, **but this is not
a `make generate` problem.** It bites any programmatic edit to a tracked text file, and the two
commands you would use to check disagree by design.

- **`git diff` and `git status` do not agree.** `core.autocrlf=true` makes `git diff` normalize LF
  churn away while `git status` still lists the files as modified. On 2026-08-28 a `make generate`
  step produced identical file lists at two stages, which reads as "nothing to revert" - and 13
  generated files were modified. **Never conclude "nothing to revert" from `git diff` alone.**
- **A programmatic rewrite can silently reclassify a file as binary.** The same day, a
  `.replace('\n','\r\n')` applied to a string whose anchor line already ended `\r\n` produced
  `\r\r\n`; git's lone-CR heuristic marked `README.md` binary, `autocrlf` stopped normalizing it, and
  a two-line change committed as **1845 insertions**. It was caught from the diffstat, not by any
  gate.
- **After ANY programmatic edit to a tracked text file**, before committing: check the diffstat
  against the size of the change you intended, and run `git ls-files --eol` on the touched paths -
  every one should read `i/lf`. `gofmt -l` is useless as a signal here; it lists ~349 files under
  `internal/` on a clean tree purely because of working-copy CRLF.
- **ENCODING is a second axis and none of the above can see it.** On 2026-08-30 an edit wrote `e`
  with an acute accent as a raw Latin-1 `0xE9` instead of UTF-8 `C3 A9`. `git ls-files --eol` read
  `i/lf`, the diffstat was proportionate, `gofmt` was clean and every test was green - and README.md
  had stopped being valid UTF-8 from that commit on. It compounded: the example was meant to show a
  37-byte string a parser REJECTS, and the mangled literal was 36 bytes and **accepted**, so the
  sentence's own example proved the opposite of the sentence, under a heading saying "measured". The
  identical defect was reproduced in a Go test file within the hour, by writing a byte as a character
  where a four-character escape was needed. So: after a programmatic edit, assert the file still
  decodes as UTF-8, and assert any non-ASCII byte is the sequence you intended. **Where an example
  needs a non-ASCII byte, prefer describing it in words or writing it as an escape the compiler
  expands** - a raw non-ASCII literal in a document is unverifiable by eye and survives every check
  this repo runs. Watch the shell layer specifically: a heredoc, a `python -c` and an editor tool
  each treat backslash escapes differently.

## Architecture

Three binaries: **`relay-server`** (HTTP `:8080` + gRPC `:9090` + scheduler), **`relay-agent`** (worker; gRPC stream to server; runs tasks as subprocesses), **`relay`** (CLI client). Full architecture, env vars, REST API, and CLI reference live in [README.md](README.md).

Code map:

- `cmd/{relay-server,relay-agent,relay}/main.go` — entrypoints; `relay-server` wires `*pgxpool.Pool` → `*store.Queries` → `*events.Broker` → `*worker.Registry` → `*scheduler.Dispatcher` and starts the schedrunner loop.
- `internal/api/` — HTTP handlers, one file per resource (`auth.go`, `jobs.go`, `tasks.go`, `workers.go`, `users.go`, `agent_enrollments.go`, `scheduled_jobs.go`, …). `BearerAuth` middleware injects `AuthUser` into context; `AdminOnly` chains after it. `cors.go` is fail-closed (wildcard `*` rejected). `ratelimit.go` is per-IP via `RemoteAddr` only — `X-Forwarded-For` is not trusted. `job_spec.go` exports `JobSpec`/`ValidateJobSpec`/`CreateJobFromSpec` for reuse by `schedrunner`.
- `internal/store/` — sqlc-generated. SQL lives in `internal/store/query/*.sql`; run `make generate` after edits (sqlc emits LF; on this CRLF repo it rewrites line endings across all generated files - after generating, run `git diff --ignore-all-space` and keep only the real content change, reverting LF-only hunks with `git checkout -- <file>`). **Never edit `*.sql.go` or `models.go` directly.** `store.Queries` accepts any `DBTX` (pool or transaction); use `q.WithTx(tx)` for transactions.
- `internal/scheduler/` — `Dispatcher` polls eligible tasks and dispatches via `worker.Registry`. Wake it with `Dispatcher.Trigger()` (passed as a callback to avoid import cycles). `NotifyListener` consumes Postgres `LISTEN/NOTIFY` to trigger across processes.
- `internal/schedrunner/` — Cron engine for `scheduled_jobs` (5-field cron, `@hourly`/`@daily`, `@every <dur>`, IANA TZ via `robfig/cron/v3`). 10 s ticker; `ReconcileOnStartup()` advances missed `next_run_at` (never-catch-up). **Does NOT import `internal/api`** — calls store directly to avoid a cycle.
- `internal/worker/` — `Registry` (in-memory worker ID → gRPC sender) plus `Handler.Connect()` which dispatches to `enrollAndRegister` (first boot, consumes enrollment token) or `reconnectAndRegister` (long-lived agent token), then `finishRegister`. `GraceRegistry` defers requeue of a disconnected worker's tasks until `RELAY_WORKER_GRACE_WINDOW`.
- `internal/agent/` — `Agent` maintains one gRPC stream; **a single send goroutine owns all writes** (gRPC streams are not concurrent-send-safe; messages queue on a 64-cap `sendCh`). `Runner` runs each task as a subprocess and streams stdout/stderr back. Hardware caps detected at startup (`capabilities.go`; GPU is NVIDIA-only via `nvidia-smi`). `credentials.go` reads/persists the long-lived token at `<state-dir>/token` (0600).
- `internal/events/` — SSE `Broker`. Events carry a `JobID` filter; `""` = broadcast.
- `internal/cli/` — stdlib `flag`, no cobra. Each subcommand is a `cli.Command`; `cli.Dispatch()` routes by name. Config at `~/.relay/config.json` or `%APPDATA%\relay\config.json`.
- `internal/discovery/` — mDNS browse for `_relay._tcp.local`.

## Key Design Decisions

**Token format.** 32 random bytes → hex-encode → SHA-256 of the hex → hex-encode the digest → store hash. Raw hex returned to the client and never stored. **All hashing goes through `internal/tokenhash.Hash` — never inline `sha256.Sum256` at a new site.**

**Password hashing.** bcrypt cost 12. The `bcryptCost` package var in `internal/api/auth.go` is overridden to `bcrypt.MinCost` in integration tests via `SetBcryptCostForTest()` (exported from `internal/api/export_test.go` under `//go:build integration`).

**Email enumeration prevention.** `handleLogin` always calls `bcrypt.CompareHashAndPassword`, even on unknown emails, against a pre-computed dummy hash (`getDummyHash()` via `sync.Once`).

**Testability overrides** (no build tags). `internal/cli` exposes `saveConfigFn`, `configFilePathFn`, `readPasswordFn` as package vars for swapping in tests.

**Where a CLI test goes.** Ask whether the assertion's truth depends on what the SERVER puts on the wire. Yes (status codes from real handlers, response container shape, field names and types, cursor behaviour across a real page boundary, authorization outcomes) -> the integration lane, `internal/cli/*_integration_test.go`, `make test-cli-integration`. Note the lane crosses a real *log* page boundary and has never crossed a *list* one - every list in it holds one or two rows against a 200-row limit, so `page[T].NextCursor` survives being renamed. No (flag parsing, argument reordering, a refusal issued before any request, output formatting given a known input, error wording, adversarial or impossible server responses) -> the default lane with an `httptest` fixture. **And a default-lane fixture must never encode its response through the CLI's own response struct.** A fixture marshalled from `relayclient.PageEnvelope[workerResp]` agrees with the decoder by construction, on both the envelope keys and the item fields, and can never detect drift in either direction. **51 vacuous fixture bodies remain, across 43 `Encode` statements** - and both numbers are given because neither alone is honest. By body: 19 paged (`PageEnvelope[workerResp|jobResp|scheduleResp|reservationResp]`) + 32 unpaged. By statement: the same 19 + 24, because `logs_test.go`'s `fakeJobSnapshotServer` takes a `[]jobResp` parameter and routes **nine** call sites' literals through one `Encode(bodies[i])`. A further 7 use `PageEnvelope[map[string]any]`: a genuine simulator on the item axis, a tautology on the envelope axis. **Do not count these with a text search.** Two earlier attempts got 19 and 29; both grepped for `Encode(<cliType>{` and neither could see the parameter indirection, which is exactly the instrument-to-claim mismatch a structural property demands - the fixture type travels through a function signature, so the shape to search for is the *type in any fixture position*, not the encode call. Hand-write the JSON, or marshal through a locally declared struct whose json tags are deliberately independent of the production type, as `writeTaskLogPage`'s `logRow` in `internal/cli/logs_test.go` does. **Read that exemplar narrowly**: the same file gets it right for the log page and wrong 23 times for the job body, which is the largest single concentration of the defect in the repo.

**Task DAG.** `task_dependencies` table; `FailDependentTasks` recursive CTE for transitive cascade on failure.

**Database.** Migrations are embedded in the binary and run on startup. Files in `internal/store/migrations/` use `golang-migrate` format (`000N_name.up.sql` / `000N_name.down.sql`).

**Tailwind v4 scans prose as source - see [web/CLAUDE.md](web/CLAUDE.md).** A class-shaped substring anywhere under `web/` (comments and tests included) emits CSS, and a rule that seems not to disappear may be a stale `web/dist` embed (`//go:embed` snapshots at compile time), not the scanner.

**Source providers.** Relay assumes `p4` is installed and a valid P4 ticket is active on the agent. Provision tickets out-of-band (`p4 login`); relay does not manage P4 credentials. The Perforce integration test spins up a `p4d` container via testcontainers-go.

## Comments

A comment exists to state a hazard or constraint the code cannot show, in a few lines. It may
cite the test or tests that pin the claim ("deleting this guard turns every typo into a broadcast
subscription; TestCanonicalJobIDFilter's passthrough rows go red"). Everything else - the
argument that the change is correct, its history, its measurements - goes in the commit
message, spec, or retro: records of a moment, which cannot drift. If content feels worth
keeping, it is - in the commit message.

Never put in a comment or docstring:

- Dates or change history ("since 2026-08-30", "was previously two readers"). Git owns history.
  (A date inside a backlog-item filename cited as a pointer is an identifier, not history, and
  is fine.)
- Session or review narrative, and measurement provenance ("measured by rendering it uppercase
  and watching that test fail").
- Counts of anything elsewhere ("16 sites", "four other copies").
- Uniqueness or completeness claims ("the only", "every", "all N") about OTHER code. These are
  claims about the complement, pinned by nothing; replace with a named guard or delete. Stating
  this function's own contract ("prints every not-yet-printed task") is fine.
- Censuses of other files or packages, and claims about another language's source.

Test comments state the property pinned and why the input discriminates. RED/GREEN history and
mutation provenance go in the commit that adds the test.

## Invariants

Cross-cutting rules that new code must not bypass. Every high-severity finding in the 2026-06-10 codebase review was a path that sidestepped an invariant already enforced elsewhere - check changes against these.

Most are stated in backend terms because that is where they were first codified, but the reasoning is **not backend-specific**. The generation-ordering rule below was rediscovered as a frontend bug on 2026-08-09 precisely because it was phrased only as a database concern, so it was invisible to whoever wrote the equivalent async lifecycle in `web/`. When you are working in `web/`, read these for the shape, not just the nouns.

- **End the generation before releasing the resource.** Wherever a generation, epoch, or token guards whether an async continuation is still current, bump it *first* and release the resource *second*. Releasing first leaves the dying resource's own callbacks running while they still look current, so they pass the staleness guard and clobber the state the teardown just set. This is the general form of the epoch fence below; the frontend instance is an `AbortController` in an effect (`web/src/jobs/useTaskLogStream.ts`), where aborting an SSE stream without bumping the run generation let the dying connection's rejection overwrite an `error` status with `reconnecting` and retry a 404 the code documented as non-transient. When reviewing, search for `abort()`, `close()`, `cancel()`, and unregister calls, and ask what the released thing's own handlers will do on the next tick. **Read the rule in the ACQUIRE direction too, which is where it was missed on 2026-08-24.** The same window has two ends, and a release armed only after the resource is fully assembled leaves everything between the acquisition and the arming uncovered: `finishRegister` took the worker's generation (`RegisterWorkerConnection` sets `online`, bumps `connection_epoch`, and the next line discards the previous disconnect's grace timer) several statements before it returned the sender that `Connect`'s teardown defer needs, and two statements in between could still fail - leaving the worker `online` with tasks assigned to a connection that did not exist. **Take the state and arm its release in the same breath**, so no early return added later can forget to. Where two defers partition one window, the flag deciding which of them owns the release is the whole correctness argument: flip it exactly once, keep it adjacent to the handoff, and CHECK it - deleting that one line left all 21 packages green, and the mutant makes every *successful* registration mark its own worker offline and requeue a healthy agent's running tasks.
- **Epoch fence.** Every write to `tasks.status` or `task_logs` must do one of three things: fence on `assignment_epoch` (match the caller's epoch); *conditionally* end the assignment (bump it, as `ClaimTaskForWorker` and `RequeueWorkerTasks` do) - the bump must be predicated on the generation actually being ended, not unconditional, or the rule is satisfied vacuously; `IncrementTaskRetryCount` was the live counter-example and now fences on epoch, `worker_id` and terminality before it bumps, so the branch has no known exception left; or, for a terminal-only writer, guard on a status predicate instead - `FailDependentTasks` is the live example, satisfying neither of the first two branches and relying entirely on its `WHERE status = 'pending'`. Never call an epoch-fenced query with a zero-value epoch, and never return a task to `pending` without bumping the epoch. Gate any side effect on the fence having actually matched - `AppendTaskLog` returns `pgx.ErrNoRows` for a stale chunk, and `handleTaskLog` must drop it *before* publishing, or a zombie agent's output appears in a live view and then vanishes on refresh. **The epoch establishes currency, not identity.** A matching epoch proves the caller's generation is current; it proves nothing about *who* the caller is, so an epoch fence alone is never an authorization check. Any write that should be restricted to a task's assignee needs a second predicate on `tasks.worker_id` matching the connection's authenticated worker (resolved at registration, never taken from the wire), and that comparison must stay NULL-rejecting - a plain `=`, never `IS NOT DISTINCT FROM` - so a zero-value worker id fails closed instead of matching an unassigned task. **And an identity check establishes identity, not honesty.** A `worker_id` predicate proves the sender is the assignee; it proves nothing about whether the report is true. A terminal transition bumps neither the epoch nor `worker_id`, so an assignee may contradict its own outcome at the same epoch forever. That is harmless for the ROW - the fence still refuses the write - and it is not harmless for anything DERIVED from the rejection. So when a fence rejection starts feeding a counter, a log line or an audit record, ask two more things. **First, what does a peer who can move this signal gain, and is the signal's own documented remedy in their favour?** `task_status_fence.counts.conflicting_total` is one increment per forged message, unbudgeted and unbounded, and README told an operator that a climbing value means `RELAY_TASK_WATCHDOG_MARGIN` is too small - so the prescribed fix widened the unbounded-assignment window the watchdog exists to close. The counter was not the defect; the advertisement was. State the forgeability where the signal is READ, in the commit that ships it. **And an option that DISABLES the control belongs outside the remedy ladder, never inside it as a peer** - the 2026-08-25 auto-enroll ceiling listed `set it to 0` as step 3 beside "revoke the junk" and "raise the number", so the documented escape from a signal an attacker drives was to turn the bound off. Check the whole ladder, not just the counter: a remedy list is part of the advertisement. **Second, pin the gate the new signal leans on.** The SQL identity predicate protects the row; only `handleTaskStatus`'s Go gate keeps the counters attributable, and deleting that gate left every package green while a non-assignee drove `conflicting_total` at will. A control that acquires a new job needs a test with that job as its subject. `AppendTaskLog`, `UpdateTaskStatus`, `IncrementTaskRetryCount`, `RequeueTask` and `RequeueTaskByID` all do this - the last two since 2026-08-20, when they were found to be the two statements that ended an assignment on the task id and a status allow-list alone. Note the shape of that miss: the backlog item asserted `RequeueTaskByID` was the *only* such statement, and a uniqueness claim is a claim about the complement - it cannot be checked by opening its subject, only by searching for the shape. `UpdateTaskStatus` no longer writes `worker_id` at all - the argument is a fence, not a value - so a terminal transition cannot strand a live agent by clearing it. A terminal status is not writable at all: `UpdateTaskStatus` and `IncrementTaskRetryCount` both carry `AND status IN ('pending','dispatched','running')`, the exact complement of the terminal set `RecomputeJobStatus` uses, so an already-finished task cannot be flipped or resurrected by a second terminal message from its own assignee at the same epoch - and the fix for that is a status predicate, never an epoch bump on terminal transitions, which would break the trailing-log flush. **Write status predicates as allow-lists, never as the equivalent deny-list**: the two are interchangeable against today's vocabulary, but a deny-list fails *open* on the next status added while an allow-list fails closed, and `TestTasksStatusVocabularyIsExactly` goes RED when the vocabulary moves so the partition is revisited rather than silently desynchronized. **`AppendTaskLog` is the third status-predicate site and the one carve-out to that guidance - read it backwards.** Its `status IN ('pending','dispatched','running')` is the first arm of a disjunction with a `finished_at > cutoff` recency window (the trailing window that bounds how long a finished task's assignee may still append; `RELAY_TASKLOG_TRAILING_WINDOW`), and the arms must never be conjoined or the trailing flush closes. Everywhere else omitting a new status from the allow-list fails closed harmlessly; here omitting a new *non-terminal* status silently discards 100% of that state's log output, because a non-terminal row has `finished_at IS NULL` and so fails the second arm too - no error, no log line. A new non-terminal status **must** be added there (`preparing` is the live candidate: `TASK_STATUS_PREPARING` is already in the proto and the agent already streams `LOG_STREAM_PREPARE` chunks); a new *terminal* status must stay out and be bounded by `finished_at` like `done`/`failed`/`timed_out`. `ListOverdueAssignedTasks` (the coordinator stale-task watchdog's scan) and the per-worker active-task statements are further carve-outs of the same shape; `TestTasksStatusVocabularyIsExactly` is the live census. `ListOverdueAssignedTasks` reads the same way: omitting a new non-terminal status from its `status IN ('dispatched','running')` means a task in that state is never swept, so the unbounded-assignment hole that statement exists to close silently reopens for it; a new terminal status must stay out. `handleTaskStatus` additionally checks identity in Go ahead of the retry branch; that gate is now a second question plus one saved round trip, not the correctness control it was when `IncrementTaskRetryCount` had a bare `WHERE id = $1`. It saves no log lines - both write sites drop `pgx.ErrNoRows` silently - so keep it for the question it asks, not for a cost claim. Keep both. When you add a fence, enumerate what runs before it.
- **Single job-spec pipeline.** All job-spec ingestion (REST API, CLI, MCP, schedrunner) goes through `jobspec.Validate` and `CreateJobFromSpec`. Never define parallel spec structs or task-creation paths; if a field is added to `jobspec.TaskSpec`, every consumer gets it for free only if they share the types.
- **One bounded sender per gRPC stream.** All writes to a stream go through its single send goroutine (agent: `sendCh`; server: `workerSender`). Sends from other goroutines must be bounded - a peer that stops reading must never block a dispatcher or HTTP handler indefinitely.
- **Identity-checked teardown.** Connection cleanup must only tear down state it owns: verify the registered sender or handle is yours before unregistering a worker, marking it offline, or arming a grace timer. A stale connection's defers must not clobber a fresh registration. **Where there is no identity to check, say so and name what replaces it.** A registration that fails before it registers a sender has no handle to compare, so the epoch alone is the ownership check there - and adding a registry call to make the two paths look symmetric would itself be the clobber this rule forbids, since it would unregister a sender the failing caller never registered. **A fence decides ownership only where it was actually evaluated**: `markWorkerOffline` returns `(rows, error)` precisely so a database fault is distinguishable from a fence that said no. Collapsing the two makes a failure-to-ask indistinguishable from a non-match, which on 2026-08-24 silently re-created the very strand the fix had just closed.
- **No interior pointers across locks.** Shared registries return value copies from getters; mutation happens through methods that hold the lock, never on pointers that escaped it.
- **Single JSON entry point.** HTTP request bodies are read only via `readJSON` in `internal/api/server.go`; request-size limits and decode policy live there, not at call sites.

## Subagent team and routing

Development is supported by a team of role-specialized subagents in `.claude/agents/`
(`relay-tpm`, `relay-planner`, `relay-backend-engineer`, `relay-frontend-engineer`,
`relay-integration-tester`, `relay-code-reviewer`, plus the built-in `Explore` for
read-only discovery). The main interactive session is the conductor: it orchestrates them
through a phased `spec -> plan -> implement -> verify -> integrate -> retro` lifecycle. See
[docs/agent-team/README.md](docs/agent-team/README.md) for the roster, the phase pipeline,
gate modes, and when to dispatch which agent. The design spec behind the team is
`docs/superpowers/specs/2026-06-18-agent-team-design.md`.

**Doc-only vs code agents.** `relay-tpm`, `relay-planner`, and `relay-code-reviewer` never
touch source - they produce specs, plans, and review findings respectively. Committing the
spec doc (Phase 1) and the plan doc (Phase 2) is always the conductor's step, since those
agents hold no git access; both commits land at the phase boundary before any code is
written. `relay-backend-engineer`, `relay-frontend-engineer`, and `relay-integration-tester`
edit code (and tests) under TDD and the Invariants above.

**Verification.** Phase 4 is a conductor-run `/code-review` followed by a parallel fan-out of four
agents dispatched in one message: `relay-code-reviewer` on three separate lenses (invariants,
correctness, security) plus `relay-integration-tester`. No Workflow, so no opt-in to obtain. The
agents cannot run `/code-review` themselves - it is a slash command, not a skill - so the conductor
runs it and feeds the output in as prior findings, which each agent then confirms or refutes with its
own evidence before adding its own passes. Confirmed findings route back to the owning engineer, then
re-verify until clean. See [docs/agent-team/README.md](docs/agent-team/README.md) for the lens briefs.

**Closing backlog items.** Always close a backlog item with the `/backlog close <fragment>`
command, never by hand-editing the item's `status` field in place. The command runs the full
close sequence the skill enforces: it `git mv`s the file from `docs/backlog/` into
`docs/backlog/closed/`, stamps `status: closed` plus `closed:`/`resolution:` frontmatter,
appends a `## Resolution` note, and commits. The `git mv` to `docs/backlog/closed/` is required
scope when a task closes a backlog item, not optional cleanup. Flipping `status` alone leaves
the file in the open directory and skips these steps, which `/backlog list` then reports as a
malformed open item.