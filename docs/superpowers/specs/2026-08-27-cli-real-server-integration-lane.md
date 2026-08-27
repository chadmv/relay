# Design: a real-server integration lane for `internal/cli`, and a CI job that runs it

- Date: 2026-08-27
- Status: proposed
- Backlog item: `docs/backlog/idea-2026-08-23-cli-tests-never-hit-real-server.md`
- Gate mode: autonomous. The clarifying questions below were asked and answered against the tree
  rather than against a human; each answer names the evidence it rests on.

---

## 1. Problem

Every test in `internal/cli` talks to an `httptest.NewServer` the test itself wrote. None of them
talks to `internal/api`. So the package's whole test suite measures whether the CLI agrees with the
test file, and says nothing about whether it agrees with the server.

That is not a theoretical exposure. `relay logs` printed nothing for every job, on every task, from
2026-05-08 to 2026-08-26 - three and a half months - with the suite green throughout, because
`GET /v1/tasks/{id}/logs` moved to a pagination envelope and the CLI kept decoding a bare array.
The same defect shape, in the same week, took out the Python SDK's `task_logs()`.

And there is a second live instance at HEAD, found while designing this lane. See section 3, R7.

Two structural facts make the gap total rather than partial:

- `internal/cli` has zero `//go:build integration` files. Under `-tags integration -p 1` the package
  finishes in about 1.0s while `internal/store` takes 257s. Nothing containerized has ever run here.
- `.github/workflows/go-ci.yml` runs `go test -race ./...` with **no tags**. Integration-tagged
  tests have therefore never executed in CI on any commit, for any package. A lane CI never runs
  cannot catch the class it exists for, which is why this design ships both halves.

---

## 2. What the lane can and cannot see (the axes)

Stating this up front, because a lane described only by what it covers invites the reader to
believe the surface is closed ([[feedback_sweep_count_needs_its_axis]]).

**Covered axes.** The HTTP contract between `internal/cli` and `internal/api`: URL path and method,
authorization outcome (200 / 400 / 403 / 404 / 409), response **container** shape (envelope vs bare
array), response **field** names and types, cursor semantics across a real page boundary, and the
CLI's rendering of a real response.

**NOT covered, by construction.** Task dispatch (`scheduler.Dispatcher` is not started). Agent log
ingest and the epoch fence (`AppendTaskLog` is bypassed; rows are inserted directly, exactly as
`seedLogRow` in `internal/api/tasks_integration_test.go` already does). The gRPC surface. SSE frames
produced by a live worker (the lane drives only the subscribe-time snapshot path). `bootstrapAdmin`
(it is `package main`, unimportable). The embedded SPA. Field **nullability** on responses whose
nulls only appear under a state this lane does not create.

---

## 3. Refutations of the backlog item

The item is a proposal, not a contract ([[feedback_backlog_proposal_not_contract]]). Each claim was
checked against the tree.

**R1. "every CLI test drives an `httptest.NewServer(http.HandlerFunc(...))` that hand-writes the
JSON response literal". Half confirmed, half refuted, and the refuted half is WORSE than the claim.**

Confirmed: 89 `httptest.NewServer` occurrences across 22 files.

Refuted: they do not all hand-write literals, and the ones that do not are the more dangerous group.
`workers_test.go`, `jobs_test.go`, `schedules_test.go`, `reservations_test.go`,
`workers_delete_test.go`, `workers_disable_test.go`, `workers_revoke_test.go` and
`workers_workspaces_test.go` encode their fixtures **through the CLI's own response structs**:
`relayclient.PageEnvelope[workerResp]`, `[jobResp]`, `[scheduleResp]`, `[reservationResp]` - 29 call
sites. A hand-written literal can at least disagree with the decoder. A fixture marshalled from the
same struct the CLI unmarshals into agrees with it **by construction**, on both the envelope keys and
the item fields, and can never detect drift in either direction. The item understates its own case
for the majority of the surface.

Where the item is accurate: `admin_test.go`, `admin_users_test.go` and `admin_output_test.go` use
`relayclient.PageEnvelope[map[string]any]` with literal keys, and `logs_test.go` routes everything
through `writeTaskLogPage`, whose `logRow` and inline page struct carry json tags deliberately
independent of `taskLogEntry` and `taskLogPage`. Those are simulators, not tautologies.

**R2. "the api package's testcontainer helpers exist". Refuted as written.**

There is no importable helper anywhere in the module. `newTestPool` and `newTestQueries` live in
`internal/api/testhelper_test.go`, which is `package api_test` under `//go:build integration`.
`internal/store/testhelper_test.go` is the same shape. `internal/api/export_test.go`
(`SetBcryptCostForTest`) is `package api` and is invisible from `internal/cli`. The **pattern**
exists, three times; the **helper** does not. The lane must write its own, and that is a design
decision (D1), not a lookup.

**R3. "`relay workers delete --yes` ... including the refusal paths (409 on a connected worker, 404
on a missing one)". Confirmed but incomplete, in a way that would produce a test that measures the
wrong thing.**

`handleDeleteWorker` is routed as `auth(admin(...))`. A non-admin token gets **403 before either
refusal is reached**, so a delete test written with the wrong token proves nothing about 409 or 404.
`parseUUID` yields **400** on a malformed id before the row is ever read. And `doWorkersDelete`
calls `resolveWorkerIDIncludingRevoked` first, which short-circuits only for a UUID-shaped target;
for a **hostname** that matches nothing it fails **locally** with `no worker found with hostname %q`
and issues no delete request at all. So "404 on a missing one" is reachable only with a well-formed
UUID that names no row. The refusal ladder is four deep, not two, and the order is load-bearing.

**R4. "for full value a `relay-agent` too - the paging boundary test ... needed a task that actually
produced 1221 log rows, which only a real agent does". Refuted for this lane.**

That measurement came from the Python lane, which drove a genuine end-to-end submit. It does not
transfer. `GetTaskLogsPage` is `WHERE task_id = $1 AND id > $2` with no fence of any kind, and
`internal/api/tasks_integration_test.go`'s `seedLogRow` already inserts into `task_logs` directly via
the pool. N rows are one `INSERT ... SELECT FROM generate_series` away. **No agent, and no gRPC, is
needed for any part of this lane.** The cost is that the lane does not exercise `AppendTaskLog`'s
epoch/identity/recency fence - recorded in section 2 as an uncovered axis rather than pretended away.

**R5. "pointing the existing `httptest.NewServer` fixtures at a live `relay-server` plus a Postgres
container keeps the assertions and swaps the seam, rather than a rewrite". Refuted as a plan.**

Most of `logs_test.go`'s 42 tests assert behaviour a real server **cannot be made to produce**: an
empty page that does not report the log as drained, a cursor that does not advance, a 500 on the
second page, a stdout that rejects every write, a body describing a different job. Those are
adversarial-server tests and they are the reason `printTaskLogs` has three stops. Repointing them at
a live server would delete that coverage outright. The division is per-test, not per-file, and D5
states the rule.

**R6. "A deliberately introduced response-shape change in `internal/api` reddens the CLI lane
(proven once)". Accepted, and strengthened.**

As written, the criterion is satisfied by a mutation that reddens *everything*, which would prove
nothing about the new lane's marginal value. The proof needs its other half: under the same mutation
the **default lane must stay green**. D4 specifies both halves plus a control that must die.

**R7. "The residual risk is prospective." Refuted. There is a live instance at HEAD.**

`internal/api`'s `taskResponse` carries `Commands json.RawMessage` with tag `json:"commands"`.
`internal/cli`'s `taskResp` carries `Command []string` with tag `json:"command"`. Migration
`000008_task_commands` dropped `tasks.command` and added `tasks.commands`, so the server has not sent
a `command` key since that migration. The field decodes to nil on every response.

It is not merely dead. `doGetJob`'s `--json` and `--pretty` paths re-encode `jobResp`, so
`relay get <job-id> --json` emits `"command":null` for every task and **carries no commands at all**.
A user reading the machine-readable form of a job gets a null where the task definition should be.
The human-readable path prints only name, status and worker, which is why nobody noticed.

This is the item's own predicted payoff arriving before the lane is built. It is filed, not fixed
here - see section 13 and D4's note on why the lane must not assert the defect's output.

**R8. Line citations.** The item cites `internal/cli/admin_test.go:16`, `logs_test.go:47` and
`python/tests/unit/test_client.py:184`. `logs_test.go` has since grown to about 1996 lines and those
offsets no longer locate what they named. This spec cites symbols only.

**R9. "zero integration-tagged tests" and "no testcontainers usage" in `internal/cli`. Confirmed.**
Also confirmed: there is no `TestMain` anywhere in the module, so the lane introduces none by
inheritance.

---

## 4. Decision D1 - where the Postgres and server harness lives

**Decision: a fourth copy, sited inside `internal/cli` as two `//go:build integration` files, written
so that a later extraction is a file move rather than a redesign.**

- `internal/cli/pgharness_integration_test.go` - `newIntegrationDSN(t) string`. Imports only
  `relay/internal/store`, `pgx`, and the testcontainers modules. Imports nothing from `internal/cli`
  and nothing from `internal/api`. This is the extraction candidate.
- `internal/cli/relayharness_integration_test.go` - `startRelayServer(t) *relayServer`. Imports
  `internal/api`, `internal/events`, `internal/worker`, `internal/store`, `internal/tokenhash`,
  `bcrypt`.

Both are `package cli` (not `cli_test`), because the tests must reach the unexported `doWorkers`,
`doListJobs`, `doGetJob`, `doSubmit`, `doSchedules`, `doAdminUsers` and `doLogs`.

**Reasoning.** The genuinely new work here - the DSN mode (D2), the per-test database, the
`api.New` wiring, the token seeding - is new in either option. Only about twenty lines of container
boot are duplicated. Against that, option (b) buys a five-package refactor into the slice whose
subject is proving a lane catches drift, and the packages it would touch (`internal/api` at 320-530s,
`internal/store` at 104-257s, `internal/worker` at 104-175s) are the slowest and most expensive lanes
in the repo to break. Extraction is much better done **after** the DSN mode has been proven green in
CI, because then it is moving a working thing rather than designing a shared thing speculatively.

**Rejected: (b), a new `internal/pgtest` or `internal/relaytest` package now.** Its real payoff is
not deduplication, it is that the DSN mode would then unlock the whole integration suite for CI,
which is `idea-2026-08-23-integration-only-guards-ci-never-runs`. That is a separate item with its
own cost analysis (`internal/api` alone would blow a 15-minute budget), and folding it in here would
make this slice's success depend on it. Two further costs worth recording: a non-test package that
takes `*testing.T` must import `testing` into the production module, and tagging every file
`//go:build integration` to avoid that makes `go build ./...` fail with "build constraints exclude
all Go files" unless one untagged file carries the package clause.

**The hazard to design against now, so the later move is cheap.** A shared harness must not import
its consumer's types ([[reference_guard_never_sees_real_producer]]). `newIntegrationDSN` returns a
string and `startRelayServer` returns only production types plus its own struct; neither takes nor
returns anything declared in `internal/cli`. Keeping that true is a review condition on this slice,
not a suggestion, because it is the single property that decides whether the extraction is a move or
a rewrite.

**Teardown, fixed here and not elsewhere.** `bug-2026-08-26-integration-lane-times-out-on-docker-teardown`
records `t.Cleanup(func() { _ = pg.Terminate(ctx) })` with `context.Background()`, no bound, error
discarded, rendering as a bare `panic:`/`FAIL` with **no test name attached**. The new helper bounds
both `Terminate` and `DROP DATABASE` with a 30s context and reports failure through `t.Errorf`, so a
hung teardown fails one named test instead of the package. The three existing copies are untouched;
that is the backlog item's scope, and an append to it is recommended in section 13.

---

## 5. Decision D2 - testcontainers vs a shared Postgres service. Both, selected by env.

**Decision: one helper with two modes.**

```
RELAY_TEST_DATABASE_URL unset  -> testcontainer, one container per test (today's model)
RELAY_TEST_DATABASE_URL set    -> one Postgres, one freshly CREATEd database per test
```

The variable name follows the existing `RELAY_E2E_DATABASE_URL` convention rather than inventing a
new one.

`newIntegrationDSN(t)`:

1. If the variable is unset: `tcpostgres.Run` with `postgres:16`, database `relay_test`, user and
   password `relay`, and `wait.ForLog("database system is ready to accept connections").WithOccurrence(2)`.
   The occurrence-2 wait is load-bearing and must be copied verbatim: `postgres:16` emits that line
   once during its own init pass before the real listener is up. Splice the scheme to `pgx5://`,
   `store.Migrate`, return the `postgres://` DSN. Cleanup terminates the container under a bounded
   context.
2. If it is set: connect to the supplied DSN with its path replaced by `/postgres` (the same move
   `web/e2e/ensure-db.mjs` makes with `adminUrl.pathname`). Generate a name
   `relaytest_<16 hex from crypto/rand>`. `CREATE DATABASE`. Build the per-test DSN by substituting
   that name into the path. `store.Migrate` against the `pgx5://` form. Cleanup closes the pool and
   `DROP DATABASE ... WITH (FORCE)` under a bounded context.

**Safety.** The database name is **generated, never read from the environment**, so the DROP can only
ever target something this helper created. The prefix is asserted immediately before the DROP anyway,
so a future edit that lets a name in from outside fails closed rather than silently widening. This is
the lesson `ensure-db.mjs`'s `ALLOWED_DB_NAME` records, applied in the direction that matters here:
the danger is not a hostile env value, it is a plausible future refactor.

**Isolation.** A fresh database per test, in both modes. This is not gold-plating: the lane's most
valuable assertions are list-endpoint ones (`Total: 1`, one row rendered), and those are only
meaningful against a known-empty database. It also keeps one test's failure from poisoning the next.

**Cost, answered rather than hand-waved.** The current model already pays `store.Migrate` once per
test - it is inside `newTestPool`. The shared-service mode pays the same 21 migrations plus a
`CREATE DATABASE` (roughly 100ms) and a `DROP` (roughly 50ms), and **removes** a container start of
2-4s and the image pull. So per-database migration is not a new cost, it is the existing cost minus
the container. For a ten-test lane the shared-service mode should land near 10-20s against 40-60s for
containers. If that ever becomes the budget problem, the escalation is a migrated **template**
database plus `CREATE DATABASE x TEMPLATE relaytest_template` (a file copy, not 21 statements); it is
deliberately not done now, because it adds a "no other connections to the template" constraint for a
saving the lane does not yet need.

**Parallelism.** Separate databases make `t.Parallel()` safe in DSN mode. The lane will not use it
initially - serial keeps failure attribution clean - and this is recorded as available headroom.

**Rejected: testcontainers only.** It works on `ubuntu-latest`, but it pays an image pull and a Ryuk
reaper per job, inherits the unbounded-teardown hazard as a CI failure mode, and needs Docker API
access. More importantly it forecloses the thing that makes this slice generalize.

**Rejected: `services: postgres` only.** It would leave local developers needing a running Postgres
before `go test -tags integration ./internal/cli/...` does anything, which is a regression against
every other integration package in the repo and a good way to get the lane ignored.

**Why this is the highest-leverage decision in the slice.** `web-ci.yml` already runs a
`services: postgres:16` with `POSTGRES_USER/PASSWORD/DB: relay` on `ports: 5432:5432`, and its own
comment ties that to `scripts/dev.ps1` so `postgres://relay:relay@127.0.0.1:5432` is one string in
both environments. `web/e2e/ensure-db.mjs` then creates a dedicated database on it and boots a real
`relay-server` against it. So **the repo already runs a real relay-server against a shared Postgres
service in CI** - just from Node, for the browser suite. This design does the same thing from Go. If
it works, the identical mechanism is what would let the rest of the integration suite run in CI,
which is the open item `idea-2026-08-23-integration-only-guards-ci-never-runs`.

**Windows note, inherited.** Use `127.0.0.1`, never `localhost`: `localhost` can resolve to `::1`
first and a published Docker port may not answer there. `ensure-db.mjs` carries the same note.

---

## 6. Decision D3 - CI job scope and siting

**Decision: a new job in `.github/workflows/go-ci.yml`, scoped to `internal/cli` only.**

```yaml
  cli-integration:
    name: cli integration (real server)
    runs-on: ubuntu-latest
    timeout-minutes: 10
    services:
      postgres:
        image: postgres:16
        env: { POSTGRES_USER: relay, POSTGRES_PASSWORD: relay, POSTGRES_DB: relay }
        ports: [ "5432:5432" ]
        options: >-
          --health-cmd pg_isready --health-interval 10s
          --health-timeout 5s --health-retries 5
    env:
      RELAY_TEST_DATABASE_URL: postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with: { go-version-file: go.mod, cache: true }
      - name: CLI integration lane (real relay-server, real Postgres)
        run: go test -tags integration ./internal/cli/... -timeout 480s
```

A matching `make test-cli-integration` target runs the identical command, so the local and CI
invocations are one string.

**Timeouts are deliberately distinct.** `go-ci.yml`'s existing `test` job uses `timeout-minutes: 15`
and `make test-integration` uses `-timeout 900s`; those are the same number, so today a Go panic and
a GitHub job kill are indistinguishable in the log. The new lane uses 10 minutes at the job level and
480s at the Go level, so the two failures name themselves.

**Scope: `internal/cli` only.** Priced: the whole integration suite at `-p 1` is 15-20 minutes
against recorded per-package times (api 320-530s and trending up, store 104-257s, worker 104-175s,
scheduler 26s, cmd/relay-server 25.5s), which exceeds `go-ci`'s existing cap before anything is
added; and `internal/agent/source/perforce` needs a `p4d` image build plus the `p4` CLI or it pays an
image build to reach a `t.Skip`. The CLI lane alone should be one to two minutes. `-p 1` is not
needed, because the pattern names one package.

**New job, not a step in `test`.** A step would inherit required-check status for free, which is the
one real advantage and it is a significant one. It is outweighed by three things: the Postgres
service would attach to the race job and make every race run wait on a health check; the two lanes
would share one 15-minute clock, which makes worse exactly the panic-versus-job-kill ambiguity the
paragraph above is fixing; and a separate job runs in parallel and names itself in the checks list
when it goes red.

**The consequence, stated plainly because the spec cannot fix it.** A new job does **not**
automatically become a required check. There is no ruleset file in `.github/` (it holds four
workflows and nothing else) - branch protection is a GitHub repository setting. Until a human adds
`cli integration (real server)` to the required checks for `main`, the lane runs on every PR and
shows red without blocking a merge. That is advisory, and advisory is the failure mode this whole
item is about. It is therefore an explicit acceptance item (section 11, A8) owned by the human, with
a documented fallback: if changing branch protection is not acceptable, fold the lane into the `test`
job as a second step and accept the shared clock.

---

## 7. Decision D4 - the tests, and the discriminating mutation

### 7.1 Harness surface

```go
type relayServer struct {
    BaseURL    string
    Pool       *pgxpool.Pool
    Q          *store.Queries
    AdminToken string
    UserToken  string
    AdminEmail string
    UserEmail  string
}

func startRelayServer(t *testing.T) *relayServer
func (s *relayServer) adminCfg() *Config  // &Config{ServerURL: s.BaseURL, Token: s.AdminToken}
func (s *relayServer) userCfg() *Config
```

`startRelayServer` calls `newIntegrationDSN`, opens a `*pgxpool.Pool`, builds
`api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)`, wraps
`apiSrv.Handler()` in `httptest.NewServer`, and seeds two users plus two tokens.

`Config` is exactly `{ServerURL, Token}` and is constructed as a literal at every existing call site,
so **swapping `srv.URL` for the live base URL is the entire injection**. `internal/relayclient` is
the single HTTP path out of `internal/cli` and has no transport seam, which is why `Config.ServerURL`
has to be the injection point - and why it is sufficient.

`pool` is non-optional: `handleDeleteWorker` calls `s.pool.Begin` directly.

Seeding needs **no test-only exports**, which is what makes this reachable from `internal/cli` at
all: `bcrypt.GenerateFromPassword(pw, bcrypt.MinCost)` then `q.CreateUserWithPassword`, and for the
token 16 random bytes to hex, `tokenhash.Hash(rawHex)`, `q.CreateToken` with a zero
`pgtype.Timestamptz{}` (SQL NULL, never expires), returning the raw hex. Every symbol is exported
production code. `internal/api/export_test.go`'s `SetBcryptCostForTest` is `package api` and is not
visible here, which is why the bcrypt cost is passed at the call site instead.

The four trailing zeros to `api.New` disable the login and register rate limiters. Evidence that 0
means "off" and not "zero requests allowed": `startRelayForMCP` in `internal/mcp/mcp_integration_test.go`
passes the same zeros and then requires a real `POST /v1/auth/login` to return 201.

**Not wired, deliberately:** the gRPC listener, `scheduler.Dispatcher`, `schedrunner`, the metrics
sweeper, `GraceRegistry`, the stale-task watchdog, `webui.Handler()`, and `bootstrapAdmin`. The
consequence is that no task ever runs and no job leaves `pending` on its own, so every test that
needs a terminal job cancels it explicitly. `worker.NewRegistry()` is empty, so `handleDisableWorker`
and `handleDeleteWorker` send no cancels - which is why worker status is driven through
`q.UpsertWorkerByHostname` plus `q.UpdateWorkerStatus` rather than through a connection.

**Every test runs under an explicit deadline** - `ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)`,
passed into the `doX` call. `t.Context()` alone is not enough: it is cancelled at test end, so a hang
inside `doLogs`'s SSE wait would still consume the package timeout and produce the nameless
`panic:` banner the teardown backlog item describes.

Note the entrypoint signatures are **not** uniform. `doWorkers`, `doListJobs`, `doGetJob`,
`doCancelJob`, `doSchedules` and `doAdminUsers` take `(ctx, cfg, args, out)`; `doSubmit` and `doLogs`
take a second `errOut`; `doInviteCreate` and `doAgentEnroll` take args **before** cfg. The
per-subcommand helpers under `doWorkers` and `doSchedules` take a `*relayclient.Client`, not a
`*Config`, so tests must enter through the top-level `doWorkers` / `doSchedules`.

### 7.2 Test inventory

`workers` (goes first, per the item, because `delete` is the first irreversible command):

1. `TestIntegration_WorkersDelete_OfflineWorker_Succeeds` - seed via `q.UpsertWorkerByHostname` then
   `q.UpdateWorkerStatus` to `offline`; target it **by hostname**, so the test exercises
   `resolveWorkerIDIncludingRevoked` against the real list endpoint as well as the delete. Assert the
   printed hostname and all four counts, which come from the real `deleteWorkerResponse`. No gRPC
   stream, no agent and no `worker.Registry` entry is needed - the gate reads `current.Status` off
   the locked row.
2. `TestIntegration_WorkersDelete_ConnectedWorker_Is409` - status `online`; assert the error carries
   the handler's own refusal text and that the row survives.
3. `TestIntegration_WorkersDelete_UnknownUUID_Is404` - a well-formed random UUID. It must be
   UUID-shaped, or `resolveWorkerIDIncludingRevoked` fails locally and no request is ever made (R3).
4. `TestIntegration_WorkersDelete_NonAdmin_Is403BeforeTheStatusGate` - `userCfg` against an `online`
   worker; assert 403, not 409. This pins the ladder order that R3 identified.
5. `TestIntegration_WorkersList_RendersARealWorker` - one seeded worker; assert `Total: 1` and the
   rendered hostname, status, cpu, ram and gpu model. This is the container-axis witness.

`jobs`:

6. `TestIntegration_SubmitListGet_RoundTrip` - `doSubmit` with `--detach` writing a real spec file,
   then `doListJobs` (assert `Total` and the row), then `doGetJob` (assert id, name, priority,
   status, submitted-by email, and the task's name and status).

`schedules`:

7. `TestIntegration_SchedulesCreateListShow` - `doSchedules` create with a real spec file, then list
   (assert `Total`, name, cron, timezone, enabled and a non-empty NEXT column - `next_run_at` is
   `time.Time` on the server and `*time.Time` on the client, which is exactly the kind of pairing a
   struct-encoded fixture cannot test), then show.

`admin`:

8. `TestIntegration_AdminUsersListGet` - `doAdminUsers` list and get against the two seeded users;
   assert `Total`, both emails, and the admin column. Also assert `userCfg` gets 403 on list, since
   `GET /v1/users` is `auth(admin(...))`.

`logs` (see D6):

9. `TestIntegration_Logs_PagesARealLogAcrossThePageBoundary` - submit a job, insert 201 rows into
   `task_logs` for its task, cancel the job, then `doLogs`. Assert all 201 lines in order on stdout
   and a clean exit. 201 forces two requests, since `relayclient.PageRequestLimit` is 200.
10. `TestIntegration_Logs_ExactPageMultiple_TerminatesOnTheEmptyFinalPage` - the same with exactly
    200 rows, where page 1 is full and therefore carries a non-zero `next_seq`, and page 2 comes back
    empty with `next_seq = 0`. `handleGetTaskLogs` sets `nextSeq = 0` when `len(items) < limit`, so
    this is the real handler's drain rule under test rather than a fixture's imitation of it.

**Fixture ordering.** Where a test seeds many rows and asserts on content, the discriminating row
must not be last ([[reference_mutation_proof_position]]): a distinctive input placed at the end
cannot detect an early-exit mutation. Seed distinctive content at **row 1 and row 201**, with
ordinary rows between, and assert on both.

**Why `doLogs` terminates without an agent.** `doLogs` subscribes to SSE and waits for a terminal job
status; `handleEvents` holds the connection open with no heartbeat and no server-side timeout, so a
non-terminal job would hang forever. The sequence that makes it deterministic: submit, insert log
rows, **cancel the job** (`handleCancelJob` runs `CancelJobTasks`, which flips every non-terminal
task to `failed` in one statement, and the job status recompute then makes the job `cancelled`), and
only then call `doLogs`. `watchJobLogs`'s `onSubscribed` snapshot sees a terminal job and a terminal
task, `emitSnapshot` prints, and the stream is stopped from inside the callback. No event timing is
involved. **The cancel must precede the `doLogs` call**; reversing them produces a hang, which is
what A5's per-test deadline exists to convert into a named failure.

### 7.3 The discriminating mutation

The Acceptance requires one proven mutation. This design specifies **two, on two different axes**,
plus a control, because a container-shape mutation says nothing about field names and vice versa.

**A Go constraint that must be respected in every mutant.** `handleListWorkers` declares
`var items []workerResponse`, `var next string` and `total, err := s.q.CountWorkers(ctx)`, and `next`
and `total` are **read only** inside the final `writeJSON(w, http.StatusOK, page[workerResponse]{...})`.
Deleting or replacing that literal therefore makes `next` and `total` unused and the package fails to
compile. A mutation that does not compile is neither killed nor survived - it is no result at all
([[reference_mutation_battery_needs_green_baseline]]). Each mutant below is written to compile.

**M0 - the control that must die.** In `handleListWorkers`, change the status argument of the final
`writeJSON` from `http.StatusOK` to `http.StatusInternalServerError`, leaving the body literal
untouched. One token, no unused variables. Tests 1 and 5 **must** go red. If they do not, the harness
is not reaching the endpoint and every subsequent result in this battery is meaningless. Revert
before proceeding.

**M1 - container axis.** In `handleListWorkers`, replace the final call with:

```go
_, _ = next, total
writeJSON(w, http.StatusOK, items)
```

This reproduces the exact shape of the real historical drift (`a90c727`, which moved a bare array to
an envelope) on a different endpoint. `relayclient.FetchAllPages[workerResp]` decodes into
`PageEnvelope`, so a bare array is a json unmarshal error and `relay workers list` fails outright.

- Expected RED in the new lane: **test 5** (the list) and **test 1** (which resolves by hostname and
  therefore reaches the same endpoint). Tests 2, 3 and 4 pass UUID-shaped targets, so
  `resolveWorkerIDIn`'s `looksLikeUUID` short-circuit means they never call the list endpoint and
  they stay green. Record that split; a claim that "the workers tests" go red would be false.
- Expected: `go test ./internal/cli/...` (no tags) **GREEN**, because `workers_test.go`'s fixture
  still marshals `relayclient.PageEnvelope[workerResp]` itself.

**M2 - field axis.** In `handleGetTaskLogs`, change the literal key `"next_seq"` to `"nextSeq"` in
its `map[string]any` response. This reproduces the exact defect class that broke `relay logs`, at the
exact endpoint it broke at, and it is a realistic drift because the wire keys there are string
literals in the handler rather than struct tags, so a rename touches no type and compiles silently.
`taskLogPage.NextSeq` then decodes as 0, the CLI concludes "drained" after page 1, and prints 200 of
201 rows.

- Expected: new lane RED at **test 9** on the row count.
- Expected: default lane **GREEN**, because `writeTaskLogPage` marshals through its own inline struct
  with its own `json:"next_seq"` tag, deliberately independent of `taskLogPage`.

**Verification protocol, mandatory in this order** ([[reference_verify_the_mutation_applied]] - CRLF
has silently broken four mutations on this repo, and a harness that fails to apply a mutant reports
"survived"):

1. Green baseline: both lanes green, on the exact command lines used later.
2. Apply M0. **Grep the mutated literal and record the hit count** - it must be exactly 1. Anchor the
   pattern **within one line**, never across a line break, since a CRLF tree silently defeats
   multi-line anchors. Confirm tests 1 and 5 go red. Revert. Confirm green again.
3. Apply M1. Grep and record. Run the new lane (expect RED at tests 1 and 5, green at 2, 3 and 4;
   record the names). Run the default lane (expect GREEN; record the pass count). Revert. Confirm
   both green.
4. Apply M2. Same four steps, expecting RED at test 9 only.
5. End by asserting the working tree is clean. A crashed or partially reverted mutation harness is
   indistinguishable from a survived mutant.

**What the lane must NOT assert.** The live `command`/`commands` drift from R7 is present at HEAD, so
`relay get <job-id> --json` genuinely emits `"command":null`. Test 6 must assert only the fields that
agree today and must **not** pin `"command":null`, even with a comment explaining it. A test whose
expected value is the defect's output, documented by a careful comment as though it were the
contract, is the exact shape of [[reference_test_green_because_of_the_bug]] - four instances on this
project already. The drift is filed instead (section 13, B1), with the acceptance criterion that its
fix adds the commands assertion to test 6.

---

## 8. Decision D5 - the new tests sit beside the old ones, and the routing rule

**Decision: confirmed, with a sharper rule than the item states, and a second rule the item does not
state at all.**

The item's division ("full per-flag coverage stays in the fast `httptest` tests; the integration lane
exists to catch shape drift, not flag logic") is correct and is confirmed. No existing test is
deleted or repointed by this slice. The reasons are R5's: a large fraction of the existing coverage
asserts adversarial-server behaviour that a real server cannot be made to produce, and the fast lane
is 1.0s against the new lane's minutes.

**Rule 1 - where a new CLI test goes.** Ask: *does the truth of this assertion depend on what the
server puts on the wire?*

- Yes (status codes from real handlers, response container shape, field names and types, cursor
  behaviour across a real page boundary, authorization outcomes) -> integration lane.
- No (flag parsing, argument reordering, a refusal that happens before any request is issued, output
  formatting given a known input, error wording, adversarial or impossible server responses) ->
  default lane with an `httptest` fixture.

**Rule 2 - how a default-lane fixture must be written.** A fixture must **never** encode its response
through the CLI's own response struct. Hand-write the JSON, or marshal through a locally declared
struct whose json tags are independent of the production type. `writeTaskLogPage`'s `logRow` (with
its "do not de-duplicate these two structs" comment) is the model.
`relayclient.PageEnvelope[workerResp]` is the anti-pattern, and R1 shows there are 29 instances of it.

Rule 2 is what keeps the fast lane worth running once the slow lane exists. Both rules belong in
`CLAUDE.md` alongside the existing testing guidance, and putting them there (one paragraph) is in
scope for this slice, because a rule that lives only in a spec is a rule nobody reads.

Repointing the 29 vacuous fixture sites is **out of scope** and filed (section 13, B2). The
integration lane bounds what that vacuity can cost; it does not remove it.

---

## 9. Decision D6 - `logs` is IN

**Decision: in, as two tests.**

Reasons, in order of weight:

- It is the only area with a **confirmed** live breakage, and that breakage is the item's strongest
  evidence. A lane that skips it covers everything except the thing that actually broke.
- It has the largest simulator surface in the package: `logs_test.go` is about 1996 lines with 18
  helpers, and `writeTaskLogPage` is called from 17 sites. The item names it as "the seam a
  real-server lane would replace". After the point fix, **more** fixture logic goes unexercised
  against a real server than before it.
- It carries the cursor semantics, which is the one place the client and server share a protocol
  rather than just a schema: `since_seq` is exclusive server-side (`WHERE task_id = $1 AND id > $2`),
  so the CLI passes the cursor **verbatim, never +1**, because `task_logs.id` is a global BIGSERIAL
  and +1 skips the next row when one task is logging alone. Nothing but a real server can test that.

**No agent is needed** - see R4. Direct row insertion suffices, `GetTaskLogsPage` has no fence, and
`internal/api/tasks_integration_test.go`'s `seedLogRow` is the existing precedent. The cost is that
`AppendTaskLog`'s epoch, identity and recency fence is not exercised by this lane, which section 2
records as an uncovered axis.

`logs` is a fifth area beyond the four the item names, and it is additive: the four named areas are
all still covered.

---

## 10. Scope

**In:**

- `internal/cli/pgharness_integration_test.go` and `internal/cli/relayharness_integration_test.go`.
- New `*_integration_test.go` files in `internal/cli` carrying the ten tests of 7.2, split by area:
  `workers_delete_integration_test.go`, `workers_list_integration_test.go`,
  `jobs_integration_test.go`, `schedules_integration_test.go`, `admin_users_integration_test.go`,
  `logs_integration_test.go`.
- A `cli-integration` job in `.github/workflows/go-ci.yml`.
- A `test-cli-integration` target in the `Makefile`, plus the `.PHONY` entry.
- Two paragraphs in `CLAUDE.md`: the D5 routing rules, and one line on the new lane under Commands.

**Out (each with a filed item where it is worth one):**

- Repointing or rewriting any existing `httptest` fixture. (B2)
- Fixing the `command`/`commands` drift. (B1)
- Python's `tests/integration` in CI. (B3, per the task's stated scope)
- Extracting a shared `internal/pgtest` package, and running the rest of the integration suite in CI.
  (B4)
- CLI auth-area coverage: `login`, `register`, `passwd`, `profile`, `invites`, `agent enroll`. Not
  one of the four decided areas, and `doLogin` needs the `readPasswordFn` and `saveConfigFn`
  overrides, which is a different setup shape. (B5)
- `reservations` and `workers workspaces`. Not among the four decided areas.
- `t.Parallel()` in the new lane.
- The template-database optimisation from D2.

---

## 11. Acceptance criteria

Each is checkable against this design, not against an imagined one.

- **A1.** `go test -tags integration ./internal/cli/... -timeout 480s` passes with
  `RELAY_TEST_DATABASE_URL` **unset** (testcontainer mode).
- **A2.** The same command passes with `RELAY_TEST_DATABASE_URL` **set** to a running Postgres
  (shared-service mode). Both modes ship, so both are measured; a mode nobody ran is a mode that does
  not work.
- **A3.** `go test ./internal/cli/...` with no tags still passes, and its runtime is within noise of
  the recorded 1.0s. The new files are all `//go:build integration`, so this is true by construction
  and is asserted to catch a missing tag.
- **A4.** `make vet-integration` passes, so the new files compile under the tag.
- **A5.** Every new test passes an explicitly deadlined context into its `doX` call. Verified by
  reading the diff; a test that omits it can hang the package with no name attached.
- **A6.** The mutation battery of 7.3 is run in full and its results are recorded in the PR: M0
  killed (the control), M1 new-lane RED at tests 1 and 5 with tests 2-4 and the default lane GREEN,
  M2 new-lane RED at test 9 with the default lane GREEN, each with the grep hit count that proves the
  mutant applied, and a clean tree at the end.
- **A7.** The `cli-integration` job runs and is green on the PR that introduces it.
- **A8.** **Human step, not automatable from the repo.** `cli integration (real server)` is added to
  the required status checks for `main`. Until this is done the job is advisory. If it is declined,
  the fallback in D3 (fold the lane into the `test` job as a second step) is taken instead. There is
  no ruleset file in `.github/`, so nothing in the diff can assert this.
- **A9.** The D5 routing rules are in `CLAUDE.md`.
- **A10.** Items B1 through B6 are proposed to the human, and on acceptance filed. The backlog item
  `idea-2026-08-23-cli-tests-never-hit-real-server` is then closed via `/backlog close`, which
  `git mv`s it to `docs/backlog/closed/`. Its Python half does not block the close: it is carried
  forward by B3, and the item's own Acceptance section is scoped to `internal/cli`.

---

## 12. Risks

- **R-a. An advisory job is the failure mode this item is about.** A `cli-integration` job that is
  not a required check reproduces, at the workflow level, the exact defect the item describes at the
  test level: a signal that exists and blocks nothing. A8 is the mitigation and D3 names the
  fallback. This is the largest risk in the slice and it lives outside the repo.
- **R-b. The two harness modes can diverge.** CI will exercise only the DSN path; local developers
  will default to the container path. A test that passes on one and fails on the other would be found
  late. A1 and A2 measure both once. The residual is accepted and recorded here rather than papered
  over; a periodic manual run of A1 is the cheap mitigation.
- **R-c. The lane's covered axes are narrower than "the CLI works".** Section 2 enumerates them. In
  particular no job ever runs, so no test observes a `running` or `done` task, and no test exercises
  the epoch fence or agent ingest. A future reader who takes this lane as end-to-end coverage will be
  wrong.
- **R-d. The 403-before-409 trap.** `handleDeleteWorker` is `auth(admin(...))`, so a delete test
  written with a non-admin token measures authorization, not the status gate. Test 4 pins the order
  deliberately; tests 1-3 must use `adminCfg`.
- **R-e. `doLogs` hangs on a non-terminal job.** `handleEvents` has no heartbeat and no server-side
  timeout. The cancel-before-call sequence in 7.2 and the deadline in A5 are both required; either
  alone leaves a way to produce a nameless package timeout.
- **R-f. Mutation results that were never real.** CRLF has silently defeated four mutations on this
  repo, and Go's unused-variable rule turns the naive `handleListWorkers` mutants into compile
  errors. 7.3's compile-safe mutants, its grep-and-count step and its M0 control are not optional
  ceremony; a battery with uniform results means the harness is broken.
- **R-g. `DROP DATABASE ... WITH (FORCE)` runs against a developer-supplied DSN.** The name is
  generated and prefix-asserted, so the blast radius is databases this helper created. The residual
  is a developer pointing the variable at a production host, where the effect is a created-and-dropped
  `relaytest_*` database and nothing else. Recorded rather than guarded further.
- **R-h. The lane adds a first Docker/Postgres dependency to a package that had none**, so
  `internal/cli`'s time in the integration lane goes from 1.0s to minutes. That cost is the point,
  and it is why `make test` is untouched.
- **R-i. Existing fixture vacuity survives this slice.** 29 sites still encode through the CLI's own
  structs. The lane makes them cheaper to be wrong about; it does not fix them. B2.

---

## 13. Recommended backlog items

Proposed, not filed. The human accepts or declines each.

- **B1 (bug, high).** `relay get <job-id> --json` emits `"command":null` and carries no commands.
  `internal/cli`'s `taskResp.Command` decodes `json:"command"`; `internal/api`'s `taskResponse.Commands`
  emits `json:"commands"`. The server stopped sending `command` at migration `000008_task_commands`.
  Live at HEAD, found while designing this lane, and a second confirmed instance of the item's class
  after `relay logs`. Acceptance should include: the CLI lane's job-area test asserts a real task's
  commands round-trip, and the change to `relay get --json` output is a deliberate contract decision
  rather than a silent fix.
- **B2 (idea, medium).** 29 `internal/cli` fixture sites marshal through the CLI's own response
  structs (`relayclient.PageEnvelope[workerResp]` and siblings), so they agree with the decoder by
  construction and can never detect drift in either direction. Repoint them at hand-written literals,
  following `writeTaskLogPage`'s `logRow`. This is a stronger statement of the problem than the
  parent item makes.
- **B3 (idea, medium).** `python/tests/integration/` has never executed in CI;
  `.github/workflows/python.yml` runs only `tests/unit` and `lint`. Standing that lane up by hand
  during the SDK sweep immediately found `Job.labels` arriving as `null`, which four reading-based
  review lenses had passed. Explicitly out of scope here.
- **B4 (idea, medium).** Extract `newIntegrationDSN` into a shared package (`internal/pgtest`) that
  `api`, `store`, `worker`, `mcp` and `cli` all use, and price running the rest of the integration
  suite in CI on a shared Postgres service. Best filed as an append to
  `idea-2026-08-23-integration-only-guards-ci-never-runs`, since that item is where the payoff lands.
  Precondition: this slice's DSN mode proven green in CI. Note the two costs in D1 (importing
  `testing` into the production module, or the untagged-package-clause workaround).
- **B5 (idea, low).** The CLI's auth area - `login`, `register`, `passwd`, `profile`, `invites`,
  `agent enroll` - has no real-server coverage. `relay login` is how every user starts and its
  response shape is unpinned against the real handler.
- **B6 (append).** To `bug-2026-08-26-integration-lane-times-out-on-docker-teardown`: this slice's
  new helper bounds both `Terminate` and `DROP DATABASE` with a 30s context and reports failure via
  `t.Errorf`, so a hung teardown fails one named test. The three existing copies
  (`internal/api/testhelper_test.go`'s `newTestPool` and `newTestQueries`,
  `internal/store/testhelper_test.go`, `internal/mcp/mcp_integration_test.go`'s `startRelayForMCP`)
  still discard the error under `context.Background()`. The shape to copy now exists.
