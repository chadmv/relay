# Agent subprocess to task_logs end-to-end harness - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build one integration test that drives a real `internal/agent.Agent` against main's real gRPC wiring and a real Postgres, so that a real subprocess's bytes are read back out of a real `task_logs` row, and the four identity env vars the coordinator rendered are read back out of the same subprocess's own environment.

**Architecture:** A new `//go:build integration` file in `cmd/relay-server` (package `main`). It stands up the production gRPC listener (`grpcServerOptions` -> `netlimit.Wrap` -> `worker.Handler`) on loopback, starts a real `agent.NewAgent(...)`/`Agent.Run(ctx)` in-process pointed at that address, seeds a job/task, and lets the real `scheduler.Dispatcher` claim and dispatch it. The task's one command re-execs the test binary as a helper subprocess (`os.Args[0] -test.run=^TestAgentSubprocessE2EHelperProcess$`), which prints its own identity env vars and then writes a known CRLF-bearing byte string to stdout and stderr. The test then reads `task_logs` back and asserts both directions. A second slice moves `cmd/relay-server`'s Postgres helpers onto `internal/testsupport/pgdsn` and adds the package to the `pg-integration` CI job, so the new guard runs on every push.

**Tech Stack:** Go, testify, pgx/pgxpool, grpc-go, `internal/testsupport/pgdsn`, sqlc-generated `internal/store`.

---

## Slice independence declaration

**There is no frontend slice.** This is a Go-only change: one new test file, three test-helper rewrites, one Makefile line, one workflow comment, one CLAUDE.md line. Nothing under `web/` is touched.

**The two backend slices are SEQUENTIAL, not independent.** Slice B rewrites the Postgres helpers that Slice A's new file sits beside, and adds `./cmd/relay-server/...` to a Makefile target whose CI job must be green when it lands - which requires Slice A's test to already exist and pass. Do not run them in parallel in Phase 3. One engineer, one lane, A then B.

**One PR.** Slice B is ~60 lines of helper and Makefile churn. It is not a separate schedulable stage and needs no `/backlog phases` run.

**Closes:** `idea-2026-08-25-no-e2e-path-from-agent-subprocess-to-task-logs`. It also makes `bug-2026-08-25-windows-crlf-log-lines-render-blank`'s closing acceptance criterion observed at the Go level rather than argued (that item is already closed; no reopen, no second close - see the "What this does not close" note at the end).

---

## What I read the item for, and what I refuted

I read `docs/backlog/idea-2026-08-25-no-e2e-path-from-agent-subprocess-to-task-logs.md` once for contradiction before planning. Three findings:

**1. REFUTED, and it changes the design: "starts a real `agent.Runner`" is not executable as written.** `agent.Runner` is an exported type but has no exported constructor - `newRunner` is unexported (`internal/agent/runner.go`), and its `provider` field is set through `SetProviderForTest`, itself only reachable from inside `package agent`. An external test package cannot build a `Runner` at all. Every existing `Runner` test (`runner_crlf_test.go`, `runner_identity_env_test.go`) is an **in-package** `package agent` test for exactly this reason.

The remedy is not a new seam. `agent.Agent` is fully exported and is the thing that CREATES a real `Runner`: `NewAgent(coord, caps, workerID, creds, saveID, provider)` plus `Run(ctx)` dials `coord` over real TCP, registers, and `handleDispatch` constructs a real `Runner` per `DispatchTask` (`internal/agent/agent.go`). So the harness drives `agent.Agent` and gets a real `Runner` for free.

**No seam is added by this plan, and that is load-bearing.** A seam that forced the headline test onto a symbol absent at HEAD would destroy the RED. Every symbol this plan's test touches - `agent.NewAgent`, `Agent.Run`, `Agent.TelemetryInterval`, `agent.LoadCredentials`, `agent.Capabilities`, `scheduler.NewDispatcher`, `Dispatcher.RunOnce`, `worker.NewHandler`, `worker.NewRegistry`, `store.Queries.GetTaskLogs`, `grpcServerOptions`, `netlimit.Wrap` - exists at HEAD and is already called from existing tests or from `cmd/relay-server/main.go`.

**2. REFUTED: the item's own Related note says this harness cannot join the `services: postgres` CI mechanism.** `idea-2026-08-23-integration-only-guards-ci-never-runs` closes with "guards that need p4d (`internal/agent/source/perforce`) or a real gRPC agent are still not covered by this mechanism", and its 2026-08-27 entry says the same. That is true for a harness that needs the agent as a **separate built binary**. It is false for this one, and the evidence is already in the tree:

- `internal/agent/agent_test.go`'s `startFakeCoord` plus `TestAgent_registers` already runs a real `agent.Agent` against a real `grpc.NewServer` on `127.0.0.1:0` **in the default, untagged lane, with no Docker at all**. "A real gRPC agent" is a library object in this repo, not a process.
- The only thing this harness adds over that test is Postgres, which is precisely what `pgdsn` supplies without Docker when `RELAY_TEST_DATABASE_URL` is set.
- The subprocess is the test binary re-execing itself (`os.Args[0]`), so it needs no image, no p4d, and no `make build`.

So: **this lane CAN join CI, and Slice B does it.** Slice B also appends the refutation to the `idea-2026-08-23` item's own Notes so the stale sentence is corrected where it is read.

**3. Confirmed, no rot.** The item cites `internal/worker/handler_tasklog_e2e_integration_test.go` as "calls `worker.Handler.HandleTaskLog` directly via its test-exported method" - true (the emit sites call `h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{...})`; the method is declared in `internal/worker/export_test.go`, which is itself `//go:build integration`). It cites `cmd/relay-server/grpc_admission_e2e_integration_test.go` as referencing `internal/agent` only in comments - true (that file's import block has no `relay/internal/agent`; the references are in the `seedE2EWorker` and reconnect-overlap doc comments). No line citations in the item have rotted, because it cites by file and symbol rather than by line.

**Where the new file goes, and why not a new package.** `cmd/relay-server` is the only place main's real wiring runs end to end, the new file's server helper is a four-line copy of `startProductionGRPCServerWithHandler`'s shape (differing only in that it must return the `*worker.Registry` the `Dispatcher` has to share with the `Handler` - the whole composition under test), and package `main` importing `internal/agent` creates no cycle (`internal/agent` imports only `internal/agent/source` and the proto package). A new package would buy isolation and lose main's wiring.

---

## File structure

**Created:**

- `cmd/relay-server/agent_subprocess_e2e_integration_test.go` - the whole harness: the helper subprocess, the server/agent/dispatcher wiring, the bounded-wait helper, and the two headline assertions. One file, because the helper subprocess and the assertion on its output are one unit and splitting them hides the contract.

**Modified (Slice B only):**

- `cmd/relay-server/bootstrap_test.go` - `newTestQueries` body (currently a `tcpostgres.Run` block) delegates to the pgdsn helper.
- `cmd/relay-server/grpc_admission_e2e_integration_test.go` - `newTestPoolAndQueries` body delegates to the pgdsn helper.
- `cmd/relay-server/startup_reconcile_test.go` - `setupPgForStartup` body delegates to the pgdsn helper.
- `Makefile` - `test-pg-integration` package list and its comment.
- `.github/workflows/go-ci.yml` - `pg-integration` job name and its timeout comment.
- `CLAUDE.md` - the `make test-pg-integration` description in the Commands block.
- `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` - append the refutation of its own closing sentence.

**Critical files to read before starting:**

- `internal/agent/runner.go` - `chunkWriter.Write`, `flush`, `collapseCRLFInPlace`, and the env merge in `Run`. These are the three things Slice A's assertions pin.
- `internal/worker/handler.go` - `Connect`'s message loop and `handleTaskLog`. The sequential recv loop is what makes the harness deterministic; see D4 below.
- `internal/scheduler/dispatch.go` - `sendTask`, `selectWorker`, `jobURL`/`taskURL`.
- `cmd/relay-server/grpc_admission_e2e_integration_test.go` - the sibling harness whose shape the new server helper copies.
- `internal/agent/runner_crlf_test.go` and `internal/agent/runner_identity_env_test.go` - the two existing helper-subprocess tests. The new helper follows their pattern exactly; read their doc comments for why `os.Exit(0)` is not optional.

---

## Design decisions the engineer must not re-derive

**D1 - the subprocess is the test binary, re-exec'd.** `argv = []string{os.Args[0], "-test.run=^TestAgentSubprocessE2EHelperProcess$"}`, with the sentinel `RELAY_AGENT_E2E_HELPER=1` carried in `DispatchTask.Env` (which reaches the child through `Runner.Run`'s env merge) rather than in the parent's own environment.

**Portability: this runs on Windows AND Linux, and neither platform is uncovered.** The producer is Go code calling `os.Stdout.WriteString` with an explicit constant; Go performs no newline translation on either platform, so the same nine bytes reach `chunkWriter` in both places. There is no shell, no `runtime.GOOS` switch, and no `//go:build !windows` file - which matters because `go test` on Windows silently skips `!windows` files, the blind spot `docs/backlog/idea-2026-09-01-go-ci-never-compiles-or-runs-windows-code.md` describes. A shell producer would be the trap inverted: `cmd /c echo` emits CRLF natively on Windows and never on Linux, so a CRLF assertion behind a GOOS switch would be vacuously green in CI and meaningful only on a developer's machine. The one genuinely platform-specific thing inside the path under test is `setupProcTree` (a Windows Job Object; a no-op on Unix), and it is exercised natively on whichever platform the lane runs on.

**D2 - the exact bytes.** All of them are ASCII, and **every CR and LF in the source file is written as a Go `\r` / `\n` escape the compiler expands - never as a literal byte.** A literal CR in a source file on this CRLF repo is the `git ls-files --eol` hazard CLAUDE.md documents, and a literal LF inside a string literal is unreviewable. There are no non-ASCII bytes anywhere in this plan's code.

| | child writes (Go literal) | bytes | expected stored (Go literal) | bytes |
|---|---|---|---|---|
| stdout payload | `"a\r\nb\r\r\nc\r"` | `61 0D 0A 62 0D 0D 0A 63 0D` (9) | `"a\nb\r\nc\r"` | `61 0A 62 0D 0A 63 0D` (7) |
| stderr payload | `"d\r\ne\r"` | `64 0D 0A 65 0D` (5) | `"d\ne\r"` | `64 0A 65 0D` (4) |

The stdout input has CRLF pairs at original offsets (1,2) and (5,6). The transform removes precisely the CR of each pair: offset 1 and offset 5. Offset 4's CR survives because its successor is another CR, not a LF - so the stored value contains a `\r\n` at a position that did not have one, and **that is not a typo.** `chunkWriter`'s contract is `\r\n -> \n` over the ORIGINAL byte positions, deliberately not `\r+\n -> \n`; removing the residue is a rendering judgement that lives in the client (`web/src/jobs/logBuffer.ts` strips it; the CLI wants the raw bytes). The trailing `\r` at offset 8 is held back by `chunkWriter` and re-emitted by `flush()` as its own one-byte chunk; it appears in the stored concatenation because a byte `Write` reported as consumed must appear somewhere.

**D3 - assert per-stream concatenations, never the interleaving.** Order WITHIN one stream is guaranteed end to end: the child writes sequentially to one fd, `io.Copy` preserves order, `sendCh` is FIFO, `Agent.runSender` is the single sender for the stream, gRPC preserves stream order, `Connect`'s recv loop is one goroutine processing messages sequentially, and `task_logs.id` is a sequence assigned at insert. Order BETWEEN stdout and stderr is not guaranteed - os/exec drives two independent copy goroutines - so nothing in this test may assert it.

**D4 - the terminal-status wait is exact, not a settling heuristic.** `handleTaskStatus` and `handleTaskLog` run on the SAME sequential recv goroutine, and `AppendTaskLog` is synchronous. `Runner.Run` enqueues every log chunk (including both `flush()` calls, which are inside the per-step loop) before `sendFinalStatus`. Therefore: the instant `tasks.status` reads terminal, every log row this task will ever produce is already committed. Wait for the status, then read the logs ONCE. Do not poll a row count and do not sleep.

**D4b - the terminal set is an allow-list taken from the migration, not invented.** `tasks_status_check` (migration `000023_task_preparing_status.up.sql`) admits exactly `pending, dispatched, preparing, running, done, failed, timed_out`. The terminal subset is therefore `done, failed, timed_out` - the exact complement of the non-terminal set `AppendTaskLog` and `UpdateTaskStatus` allow-list. There is no stored `prepare_failed`: `TASK_STATUS_PREPARE_FAILED` is a PROTO value the handler maps into the stored vocabulary. Do not add a status string to the switch in Task A5 that the CHECK constraint would reject - it would be a dead arm and a false claim about the vocabulary. `TestTasksStatusVocabularyIsExactly` (`internal/store`) is the live census if this ever needs re-checking.

**D5 - every wait is owned by this test.** Each test gets its own freshly created database from `pgdsn`, its own listener on `127.0.0.1:0`, its own worker hostname, and its own job/task UUIDs. Nothing waits on state a sibling test could satisfy. The waits are: our worker row's `status`, our task row's `status`. Never "some worker is online".

**D6 - the four identity names are poisoned in the parent before the agent starts.** `t.Setenv("RELAY_JOB_URL", "POISON-inherited-...")` and so on for all four. `Runner.Run` appends the coordinator's values AFTER `os.Environ()`, and os/exec's duplicate rule keeps the last, so a correct build overwrites the poison. A mutation that deletes one append then yields the poison value rather than an absent key - which turns an equality assertion into a kill instead of a "map key missing" that a lenient assertion might tolerate. `t.Setenv` forbids `t.Parallel`; this test must not call it.

---

# Slice A - the harness

### Task A1: The helper subprocess and its exact bytes

**Files:**
- Create: `cmd/relay-server/agent_subprocess_e2e_integration_test.go`

- [ ] **Step 1: Write the helper and its constants**

Create the file with exactly this content:

```go
//go:build integration

package main

// THE ONLY TEST IN THE REPO THAT CROSSES THE WIRE IN BOTH DIRECTIONS.
//
// internal/worker/handler_tasklog_e2e_integration_test.go proves
// handleTaskLog -> AppendTaskLog -> broker -> SSE, entering through a
// test-exported method and never through a gRPC stream.
// internal/agent/runner_crlf_test.go proves a real subprocess's bytes reach
// chunkWriter and come out transformed, ending at a channel.
// internal/scheduler/dispatch_test.go proves the coordinator renders the four
// identity values, ending at a fakeSender. Nothing joined them, so chunk
// framing, epoch stamping and the env merge composed only by assumption.
//
// This file starts a real agent.Agent against the listener cmd/relay-server's
// main() builds, dispatches a real task through the real scheduler.Dispatcher,
// runs a real subprocess, and reads task_logs back out of Postgres.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relay/internal/agent"
	"relay/internal/events"
	"relay/internal/netlimit"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/testsupport/pgdsn"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// agentE2EHelperEnv selects helper mode. It travels through DispatchTask.Env,
// so the parent's own environment is never mutated by it, and it is not one of
// the four identity names so it can never be mistaken for the subject.
const agentE2EHelperEnv = "RELAY_AGENT_E2E_HELPER"

// identityLinePrefix delimits the helper's environment report from the byte
// payload that follows it on the same stream. No byte of either payload can
// produce this prefix.
const identityLinePrefix = "relayenv "

// The four names the coordinator owns in a task subprocess.
var e2eIdentityNames = []string{"RELAY_TASK_ID", "RELAY_JOB_ID", "RELAY_JOB_URL", "RELAY_TASK_URL"}

// The bytes the subprocess writes, and the bytes AppendTaskLog must store.
//
// Every CR and LF here is a Go escape the compiler expands, never a literal
// byte: a literal CR in a source file on this CRLF repo is invisible to review
// and to gofmt, and a literal LF inside a string literal is unreadable.
//
// stdout in  "a\r\nb\r\r\nc\r" is 61 0D 0A 62 0D 0D 0A 63 0D (9 bytes).
// stdout out "a\nb\r\nc\r"     is 61 0A 62 0D 0A 63 0D       (7 bytes).
//
// THE \r\n IN THE EXPECTED VALUE IS NOT A TYPO. chunkWriter removes precisely
// the CR of each CRLF pair OF THE ORIGINAL, at original offsets 1 and 5. The CR
// at offset 4 survives because its successor is another CR. "Correcting" this
// to "a\nb\nc\r" silently changes the transform to \r+\n -> \n, which is a
// judgement about VISIBLE content and belongs in the client that holds the
// opinion (web/src/jobs/logBuffer.ts strips the residue; the CLI wants the raw
// bytes). The trailing \r is chunkWriter's held byte, re-emitted by flush().
const (
	e2eStdoutIn  = "a\r\nb\r\r\nc\r"
	e2eStdoutOut = "a\nb\r\nc\r"
	e2eStderrIn  = "d\r\ne\r"
	e2eStderrOut = "d\ne\r"
)

// TestAgentSubprocessE2EHelperProcess IS NOT A TEST. It is the subprocess the
// harness execs: the test binary re-executes itself with -test.run pointed
// here, so the producer is Go writing exact bytes and the same bytes reach the
// runner on Windows and on Linux. Go performs no newline translation.
//
// A SHELL PRODUCER CANNOT CARRY THE CRLF ASSERTION. `cmd /c echo` emits CRLF
// natively on Windows and never on Linux, so a CRLF assertion behind a
// runtime.GOOS switch is vacuously green in CI and meaningful only on a
// developer's machine. That is why this file carries no GOOS switch and no
// !windows build tag.
//
// THE ENVIRONMENT REPORT COMES FIRST, ON STDOUT, and every line it emits ends
// in \n and contains no \r - which is what lets the parent split the two
// payloads deterministically. os/exec deduplicates cmd.Env at Start time, so
// reporting the CHILD's own environ is the only observation that proves what
// the subprocess actually resolved.
//
// os.Exit(0) IS NOT OPTIONAL: without it the testing framework appends
// "PASS\nok ..." to the very stdout the parent asserts on.
func TestAgentSubprocessE2EHelperProcess(t *testing.T) {
	if os.Getenv(agentE2EHelperEnv) == "" {
		return // an ordinary test run; this process is not the helper
	}
	var report strings.Builder
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		for _, want := range e2eIdentityNames {
			if strings.EqualFold(k, want) {
				report.WriteString(identityLinePrefix + k + "=" + v + "\n")
				break
			}
		}
	}
	_, _ = os.Stdout.WriteString(report.String())
	_, _ = os.Stdout.WriteString(e2eStdoutIn)
	_, _ = os.Stderr.WriteString(e2eStderrIn)
	os.Exit(0)
}
```

The import block above lists everything the finished file needs, so the intermediate tasks compile without churn. `go vet` will flag unused imports until Task A4; run the per-test command in Step 2 rather than vetting the package until then, or add the later helpers first.

- [ ] **Step 2: Run it to confirm the helper is inert in an ordinary run**

Run: `go test -tags integration -count=1 ./cmd/relay-server/... -run TestAgentSubprocessE2EHelperProcess -v -timeout 60s`
Expected: PASS, and the helper returns immediately because `RELAY_AGENT_E2E_HELPER` is unset. This test touches no database, so it needs neither Docker nor `RELAY_TEST_DATABASE_URL`.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/agent_subprocess_e2e_integration_test.go
git commit -m "test: add the helper subprocess for the agent-to-task_logs e2e harness"
```

---

### Task A2: The bounded-wait helper

Every wait in this harness must produce a FAILED test with a message naming what it was waiting for, never a hang. This is its own task rather than a footnote because a hang under mutation is indistinguishable from infrastructure trouble, and the mutation battery in Task A9 is unreadable without it.

**Files:**
- Modify: `cmd/relay-server/agent_subprocess_e2e_integration_test.go`

- [ ] **Step 1: Write the failing test**

Append to the file:

```go
// waitFor polls cond until it returns true, and FAILS THE TEST BY NAME if it
// does not within limit.
//
// EVERY WAIT IN THIS FILE GOES THROUGH IT, and that is the point. This harness
// starts a subprocess and a gRPC stream, and a mutation anywhere in the path
// under test typically manifests as "the thing never happens" rather than "the
// thing happens wrongly" - an epoch-0 mutant makes the fence reject every
// status, so the task never reaches a terminal state at all. Without a deadline
// that case is an indefinite hang, which reads in a mutation battery exactly
// like a wedged container or a lost Docker socket. With one it is a named
// failure that says which step never completed.
//
// cond runs on the test goroutine, so t.Fatalf here is legal and aborts the
// test rather than leaking a goroutine that logs after the test returns.
func waitFor(t *testing.T, what string, limit time.Duration, cond func() bool) {
	t.Helper()
	waitForOn(t, what, limit, cond)
}

func TestWaitFor_ATimeoutFailsByNameInsteadOfHanging(t *testing.T) {
	fake := &fakeFatalT{}
	waitForOn(fake, "the thing that never happens", 50*time.Millisecond, func() bool { return false })
	require.True(t, fake.failed, "a wait that never succeeds must fail, not return quietly")
	require.Contains(t, fake.msg, "the thing that never happens",
		"the failure must name what it was waiting for; an unnamed timeout is unreadable in a mutation battery")
}
```

`waitForOn` and `fakeFatalT` do not exist yet - that is the RED.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -tags integration -count=1 ./cmd/relay-server/... -run TestWaitFor -v -timeout 60s`
Expected: FAIL to compile - `undefined: waitForOn`, `undefined: fakeFatalT`.

- [ ] **Step 3: Write the minimal implementation**

Insert above `waitFor`:

```go
// fatalT is the minimal surface waitForOn needs. It exists so waitFor's OWN
// timeout behaviour is testable: there is no way to prove a real *testing.T did
// not hang by making it hang, and a t.Fatalf on a real *testing.T marks the
// parent permanently failed with no way to un-fail it. Every real call site
// passes a genuine *testing.T. Same reasoning as pgdsn.AssertT.
type fatalT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// fakeFatalT records the first Fatalf instead of aborting, so the timeout arm
// can be observed as a value.
type fakeFatalT struct {
	failed bool
	msg    string
}

func (f *fakeFatalT) Helper() {}
func (f *fakeFatalT) Fatalf(format string, args ...any) {
	if !f.failed {
		f.failed = true
		f.msg = fmt.Sprintf(format, args...)
	}
}

func waitForOn(t fatalT, what string, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", limit, what)
			return // the fake does not abort; a real *testing.T never reaches here
		}
		time.Sleep(25 * time.Millisecond)
	}
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test -tags integration -count=1 ./cmd/relay-server/... -run TestWaitFor -v -timeout 60s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/agent_subprocess_e2e_integration_test.go
git commit -m "test: bound every wait in the agent e2e harness with a named deadline"
```

---

### Task A3: The Postgres, server, and worker-seed helpers

**Files:**
- Modify: `cmd/relay-server/agent_subprocess_e2e_integration_test.go`

- [ ] **Step 1: Write the helpers**

Append to the file:

```go
// newPgdsnPoolAndQueries takes a fresh migrated database from the shared
// harness. NOT tcpostgres directly, unlike this package's three older helpers:
// pgdsn gives one testcontainer per call when RELAY_TEST_DATABASE_URL is unset
// and one freshly CREATEd database on a supplied server when it is set, which
// is the mode .github/workflows/go-ci.yml's Postgres-service jobs use - so this
// harness can run in CI with no Docker daemon at all.
func newPgdsnPoolAndQueries(t *testing.T) (*pgxpool.Pool, *store.Queries) {
	t.Helper()
	dsn := pgdsn.NewIntegrationDSN(t)
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pgdsn.BoundedCleanup(t, "agent e2e pgxpool.Close", pool.Close) })
	return pool, store.New(pool)
}

// startAgentE2EServer wires a listener exactly as cmd/relay-server's main()
// does: grpcServerOptions -> netlimit.Wrap -> grpc.Server -> worker.Handler,
// over real TCP on loopback.
//
// IT RETURNS THE REGISTRY, which is why it is not
// startProductionGRPCServerWithHandler. The scheduler.Dispatcher must send
// through the SAME *worker.Registry the Handler registers its sender into -
// that shared object is the composition under test, and a helper that hides it
// cannot express this harness at all.
func startAgentE2EServer(t *testing.T, pool *pgxpool.Pool, q *store.Queries) (string, *worker.Registry) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	lis := netlimit.Wrap(raw, netlimit.Config{MaxTotal: 10, MaxPerIP: 10})

	registry := worker.NewRegistry()
	broker := events.NewBroker()
	handler := worker.NewHandler(q, pool, registry, broker, func() {})

	srv := grpc.NewServer(grpcServerOptions(grpcBounds{maxConns: 10, maxConnsPerIP: 10, maxConnIdle: 0})...)
	relayv1.RegisterAgentServiceServer(srv, handler)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return raw.Addr().String(), registry
}

// seedAgentE2EWorker creates a worker row with a known agent token and writes
// that token into a fresh state dir, so agent.LoadCredentials picks it up and
// the agent takes the RECONNECT path (RegisterRequest_AgentToken). This avoids
// needing an agent_enrollments row and matches what a long-lived agent does on
// every boot after its first.
func seedAgentE2EWorker(t *testing.T, ctx context.Context, q *store.Queries, hostname string) (pgtype.UUID, *agent.Credentials) {
	t.Helper()
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: hostname, Hostname: hostname, CpuCores: 4, RamGb: 8, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	raw := "e2e-agent-token-" + hostname
	hash := tokenhash.Hash(raw)
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{ID: w.ID, AgentTokenHash: &hash}))

	stateDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "token"), []byte(raw), 0600))
	creds, err := agent.LoadCredentials(stateDir)
	require.NoError(t, err)
	require.True(t, creds.HasAgentToken(),
		"the seeded token must be loadable, or the agent takes the auto-enroll path instead")
	return w.ID, creds
}
```

`workers.max_slots` defaults to 1 (migration `000001_initial.up.sql`), which is enough: this harness dispatches one task.

- [ ] **Step 2: Run it to verify the package still builds**

Run: `go vet -tags integration ./cmd/relay-server/...`
Expected: no output (clean), except for imports the later tasks introduce - if `encoding/json` or `scheduler` are flagged unused, complete Task A4 before re-running.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/agent_subprocess_e2e_integration_test.go
git commit -m "test: wire the agent e2e harness's Postgres, gRPC and worker-seed helpers"
```

---

### Task A4: The seeding helper for the job and task

**Files:**
- Modify: `cmd/relay-server/agent_subprocess_e2e_integration_test.go`

- [ ] **Step 1: Write the helper**

Append:

```go
// e2ePublicBase carries a PATH PREFIX because that is the shape a
// reverse-proxied deployment uses and the one where an accidental separator
// shows up. Same shape internal/scheduler's URL test uses.
const e2ePublicBase = "https://relay.example.test/ops"

// seedAgentE2EJob creates a user, a job, and one task whose single command
// re-execs this test binary as the helper. Returns the job and task ids.
//
// timeout_seconds IS SET DELIBERATELY. It becomes DispatchTask.TimeoutSeconds,
// which newRunner turns into a context deadline on the subprocess - so a child
// that wedges is killed by the code under test rather than left to be killed by
// the Go test timeout with no test name attached.
func seedAgentE2EJob(t *testing.T, ctx context.Context, q *store.Queries) (pgtype.UUID, pgtype.UUID) {
	t.Helper()
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "e2e", Email: "agent-e2e@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "agent-e2e-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte(`{}`), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	commands, err := json.Marshal([][]string{{os.Args[0], "-test.run=^TestAgentSubprocessE2EHelperProcess$"}})
	require.NoError(t, err)
	env, err := json.Marshal(map[string]string{agentE2EHelperEnv: "1"})
	require.NoError(t, err)

	timeout := int32(60)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "agent-e2e-task", Commands: commands, Env: env,
		Requires: []byte(`{}`), TimeoutSeconds: &timeout, Retries: 0,
	})
	require.NoError(t, err)
	return job.ID, task.ID
}
```

- [ ] **Step 2: Run vet**

Run: `go vet -tags integration ./cmd/relay-server/...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/agent_subprocess_e2e_integration_test.go
git commit -m "test: seed a helper-exec task for the agent e2e harness"
```

---

### Task A5: The headline test - the log direction

**Files:**
- Modify: `cmd/relay-server/agent_subprocess_e2e_integration_test.go`

- [ ] **Step 1: Write the test**

Append. This is the whole harness; the identity assertions arrive in Task A6.

```go
// TestAgentSubprocessEndToEnd_BytesAndIdentityCrossTheRealWire is the single
// harness the backlog item asks for, in BOTH directions, because a dispatch has
// to reach the runner before any log can come back.
//
// WHAT IS REAL HERE: a listener built by grpcServerOptions and wrapped by
// netlimit.Wrap; a worker.Handler over a real Postgres; a real agent.Agent that
// dials that address, registers with a real agent token, and runs its own send
// goroutine; a real scheduler.Dispatcher that claims the task with
// ClaimTaskForWorker and renders the identity URLs from the claimed row; a real
// Runner; a real subprocess. The only fake is the subprocess's identity - it is
// this test binary, which is what makes the byte payload exact on both
// platforms.
func TestAgentSubprocessEndToEnd_BytesAndIdentityCrossTheRealWire(t *testing.T) {
	ctx := context.Background()

	// POISON THE FOUR NAMES IN THE PARENT FIRST. Runner.Run appends the
	// coordinator's values after os.Environ(), and os/exec keeps the last
	// duplicate, so a correct build overwrites these. A mutation that deletes one
	// append then yields the poison rather than an absent key, which an equality
	// assertion kills and a presence assertion would not. t.Setenv forbids
	// t.Parallel; this test must never call it.
	for _, name := range e2eIdentityNames {
		t.Setenv(name, "POISON-inherited-"+name)
	}

	pool, q := newPgdsnPoolAndQueries(t)
	addr, registry := startAgentE2EServer(t, pool, q)

	const hostname = "e2e-agent-subprocess"
	workerID, creds := seedAgentE2EWorker(t, ctx, q, hostname)
	jobID, taskID := seedAgentE2EJob(t, ctx, q)

	// Start the real agent. TelemetryInterval is pushed out of the way: this
	// test asserts nothing about telemetry and a 10s sampler only adds noise.
	agentCtx, agentCancel := context.WithCancel(context.Background())
	a := agent.NewAgent(addr, agent.Capabilities{
		Hostname: hostname, OS: "linux", CPUCores: 4, RAMGB: 8,
	}, "", creds, func(string) error { return nil }, nil)
	a.TelemetryInterval = time.Hour
	agentDone := make(chan struct{})
	go func() { defer close(agentDone); a.Run(agentCtx) }()
	t.Cleanup(func() {
		agentCancel()
		select {
		case <-agentDone:
		case <-time.After(10 * time.Second):
			t.Errorf("agent.Run did not return within 10s of its context being cancelled")
		}
	})

	// WAIT ON OUR OWN WORKER ROW, never on "some worker is online": this database
	// belongs to this test alone, and the id is one we seeded.
	waitFor(t, "the agent to register and its worker row to go online", 30*time.Second, func() bool {
		w, err := q.GetWorker(ctx, workerID)
		return err == nil && w.Status == "online"
	})

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), e2ePublicBase)

	// RunOnce inside the wait, not before it: the first cycle can legitimately
	// find the worker not yet online. Re-running after the claim is a no-op - the
	// task is no longer pending, so ClaimTaskForWorker matches nothing.
	waitFor(t, "the dispatcher to claim the task and send it to the agent", 30*time.Second, func() bool {
		d.RunOnce(ctx)
		tk, err := q.GetTask(ctx, taskID)
		return err == nil && tk.Status != "pending"
	})

	// THE TERMINAL STATUS IS AN EXACT SYNCHRONISATION POINT, NOT A SETTLING
	// HEURISTIC. handleTaskStatus and handleTaskLog run on the SAME sequential
	// recv goroutine and AppendTaskLog is synchronous, while Runner.Run enqueues
	// every chunk - including both per-step flush() calls - before
	// sendFinalStatus. So the instant this row reads terminal, every log row this
	// task will ever produce is already committed. Read the logs once; do not
	// poll a row count and do not sleep.
	//
	// AN ALLOW-LIST, and it is the exact terminal subset of tasks_status_check
	// (migration 000023): pending/dispatched/preparing/running are the
	// non-terminal complement. Adding a value the constraint would reject makes
	// this a dead arm and a false claim about the vocabulary.
	var finalStatus string
	waitFor(t, "the task to reach a terminal status", 60*time.Second, func() bool {
		tk, err := q.GetTask(ctx, taskID)
		if err != nil {
			return false
		}
		switch tk.Status {
		case "done", "failed", "timed_out":
			finalStatus = tk.Status
			return true
		}
		return false
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)

	// Per-stream concatenations. Order WITHIN a stream is guaranteed all the way
	// down (one fd, io.Copy, FIFO sendCh, one send goroutine, one gRPC stream,
	// one recv goroutine, a task_logs id sequence). Order BETWEEN the two streams
	// is NOT - os/exec drives two independent copy goroutines - so nothing here
	// may assert the interleaving.
	var gotOut, gotErr strings.Builder
	for _, r := range rows {
		switch r.Stream {
		case "stdout":
			gotOut.WriteString(r.Content)
		case "stderr":
			gotErr.WriteString(r.Content)
		default:
			t.Fatalf("unexpected task_logs.stream %q", r.Stream)
		}
	}

	require.Equal(t, "done", finalStatus,
		"the helper exits 0, so anything else means the path broke before the assertions below could "+
			"mean anything; stdout=%q stderr=%q", gotOut.String(), gotErr.String())

	// Drop the synthetic step marker, which is always the first stdout content
	// and is terminated by the first \n in the joined stream.
	_, rest, ok := strings.Cut(gotOut.String(), "\n")
	require.True(t, ok,
		"expected the step marker line then the subprocess bytes in task_logs; got %q", gotOut.String())

	// Consume the identity report. Task A6 asserts on it; here it is only
	// skipped, so the byte payload can be compared exactly.
	for strings.HasPrefix(rest, identityLinePrefix) {
		_, tail, cut := strings.Cut(rest, "\n")
		require.True(t, cut, "an identity line must be newline-terminated; remainder %q", rest)
		rest = tail
	}

	require.Equal(t, e2eStdoutOut, rest,
		"the bytes a real subprocess wrote must reach task_logs as the exact CRLF transform of what it wrote")
	require.Equal(t, e2eStderrOut, gotErr.String(),
		"stderr is a SECOND flush call site and a SECOND stream mapping; asserting only on stdout leaves both unpinned")
}
```

- [ ] **Step 2: Run it**

Run: `go test -tags integration -count=1 ./cmd/relay-server/... -run TestAgentSubprocessEndToEnd -v -timeout 300s`

Expected on a first run: **PASS. That is not a failure of the plan and it must be recorded, not glossed.** This slice adds no production code - the production path already works, and the RED this test carries is a MUTATION red, not a missing-feature red. Task A9 is where the RED is established, and a reviewer must treat A9's results, not this step, as the evidence that the test discriminates. If this step does NOT pass, stop and diagnose before continuing; a green A9 on top of a broken A5 proves nothing.

Set `RELAY_TEST_DATABASE_URL=postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable` to run against the `relay-postgres` container `scripts/dev.ps1` manages instead of pulling an image.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/agent_subprocess_e2e_integration_test.go
git commit -m "test: drive a real subprocess's bytes through a real gRPC stream into task_logs"
```

---

### Task A6: The dispatch direction - the identity env vars the subprocess observed

**Files:**
- Modify: `cmd/relay-server/agent_subprocess_e2e_integration_test.go`

- [ ] **Step 1: Write the assertions**

Replace the identity-skipping loop from Task A5 with a collecting one:

```go
	// COLLECT the identity report rather than skipping it. This is the DISPATCH
	// direction, and it is half of what this harness exists for: three closed
	// slices proved the coordinator RENDERS these values, that they SURVIVE a
	// proto round trip, and that Runner.Run MERGES them - each against a
	// different fixture, none composed.
	observed := map[string]string{}
	for strings.HasPrefix(rest, identityLinePrefix) {
		line, tail, cut := strings.Cut(rest, "\n")
		require.True(t, cut, "an identity line must be newline-terminated; remainder %q", rest)
		k, v, split := strings.Cut(strings.TrimPrefix(line, identityLinePrefix), "=")
		require.True(t, split, "malformed identity line %q", line)
		observed[k] = v
		rest = tail
	}
```

and append after the two byte assertions, closing the test function:

```go
	// THE EXPECTED VALUES ARE BUILT FROM THE IDS THIS TEST SEEDED, never from
	// anything the message carried. Sourcing both sides from the dispatch would
	// make the comparison agree with itself by construction and go blind to the
	// two ids being transposed. jobID and taskID are independently generated
	// UUIDs, so a transposed argument pair cannot produce these strings.
	jobStr := uuidStringFromPG(t, jobID)
	taskStr := uuidStringFromPG(t, taskID)
	require.Equal(t, map[string]string{
		"RELAY_TASK_ID":  taskStr,
		"RELAY_JOB_ID":   jobStr,
		"RELAY_JOB_URL":  e2ePublicBase + "/jobs/" + jobStr,
		"RELAY_TASK_URL": e2ePublicBase + "/jobs/" + jobStr + "/tasks/" + taskStr,
	}, observed,
		"the four identity variables the coordinator rendered must be the four the subprocess resolved. "+
			"A POISON-inherited-* value here means the coordinator's append was deleted and the parent's "+
			"environment leaked through; a missing key means the variable never reached the child at all.")
}

// uuidStringFromPG renders a pgtype.UUID the way uuidStr does across the
// coordinator, so the expected URL strings are built from the same spelling the
// dispatcher used.
func uuidStringFromPG(t *testing.T, u pgtype.UUID) string {
	t.Helper()
	v, err := u.Value()
	require.NoError(t, err)
	s, ok := v.(string)
	require.True(t, ok, "pgtype.UUID.Value must render a string, got %T", v)
	return s
}
```

- [ ] **Step 2: Run it to verify it passes**

Run: `go test -tags integration -count=1 ./cmd/relay-server/... -run TestAgentSubprocessEndToEnd -v -timeout 300s`
Expected: PASS. If `uuidStringFromPG` disagrees with the coordinator's spelling, the map comparison shows it as a case or hyphenation mismatch - fix the helper to match `uuidStr`, do not loosen the assertion.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/agent_subprocess_e2e_integration_test.go
git commit -m "test: assert the coordinator's identity env vars are the ones the subprocess observed"
```

---

### Task A7: Prove the assertions cannot pass vacuously

An assertion that never sees data is green for the wrong reason. Two of the assertions above compare against a non-empty constant, so they cannot be vacuous; the identity map comparison fails on an empty `observed`, so it cannot either. What is NOT yet pinned is that the marker `strings.Cut` succeeded because a real marker was there rather than because the string happened to contain a newline.

**Files:**
- Modify: `cmd/relay-server/agent_subprocess_e2e_integration_test.go`

- [ ] **Step 1: Add the fixture assertions**

Immediately after `rows, err := q.GetTaskLogs(ctx, taskID)` and its `require.NoError`, insert:

```go
	require.NotEmpty(t, rows,
		"fixture: zero task_logs rows means nothing crossed the wire at all, and every assertion below "+
			"would then be measuring an empty string against another empty string")
```

and immediately after the marker `strings.Cut`, insert:

```go
	marker, _, _ := strings.Cut(gotOut.String(), "\n")
	require.True(t, strings.HasPrefix(marker, "=== relay step 1/1 === "),
		"fixture: the first stdout content must be the synthetic step marker, so the Cut above removed "+
			"the marker and not the first line of real output; got %q", marker)
```

- [ ] **Step 2: Run**

Run: `go test -tags integration -count=1 ./cmd/relay-server/... -run TestAgentSubprocessEndToEnd -v -timeout 300s`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/agent_subprocess_e2e_integration_test.go
git commit -m "test: pin the agent e2e harness's fixture so no assertion can pass on an empty read"
```

---

### Task A8: Record why this test is where it is

**Files:**
- Modify: `cmd/relay-server/agent_subprocess_e2e_integration_test.go`

- [ ] **Step 1: Add the lane comment**

Append to the test function's doc comment, above `func TestAgentSubprocessEndToEnd_...`:

```go
// WHY THE INTEGRATION LANE, AND WHY THAT IS NOT THE END OF THE SENTENCE HERE.
// Reaching Connect's message loop is past authenticateAndRegister, which is a
// Postgres round trip, so there is no default-lane home for this. Unlike the
// other guards in this package, though, it does NOT need Docker: the database
// comes from internal/testsupport/pgdsn, the gRPC server and the agent are both
// in-process, and the subprocess is this test binary. It therefore runs in
// go-ci.yml's pg-integration job on every push. Do not move it behind a helper
// that reaches for testcontainers directly; that would take it back out of CI
// without changing a line of the test.
```

- [ ] **Step 2: Run vet**

Run: `go vet -tags integration ./cmd/relay-server/...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/agent_subprocess_e2e_integration_test.go
git commit -m "docs: record the agent e2e harness's lane decision in the test itself"
```

---

### Task A9: The mutation battery - establish the RED

This is the task that proves the test discriminates. **It is the evidence for the item's second acceptance criterion** ("It fails if the agent-side transform, the chunk framing, or the handler's ingest changes incompatibly"), and without it Task A5's green is unearned.

**Run this in an isolated copy of the tree, never in a worktree a sibling agent is reading.** Copy the worktree to the scratchpad, mutate there, and restore by re-copying the saved original file - **never** `git checkout -- <file>`, which would discard the uncommitted guard under test.

Get a GREEN baseline first: an unmutated run must pass. Uniform results across every mutation mean a broken harness, not a strong test. A compile error is not a kill.

**Files:** (mutations are applied and then reverted; nothing is committed from this task)
- Mutate: `internal/agent/runner.go`, `internal/agent/agent.go`, `internal/worker/handler.go`, `internal/scheduler/dispatch.go`

- [ ] **Step 1: Baseline**

Run: `go test -tags integration -count=1 ./cmd/relay-server/... -run TestAgentSubprocessEndToEnd -v -timeout 300s`
Expected: PASS. Record the wall-clock time; the mutants below are compared against it.

- [ ] **Step 2: M1 - the agent-side transform (the collapse)**

In `internal/agent/runner.go`, in `chunkWriter.Write`, change:

```go
	chunk = collapseCRLFInPlace(chunk)
```

to:

```go
	_ = collapseCRLFInPlace(chunk)
```

Run the test. Expected: FAIL at `require.Equal(t, e2eStdoutOut, rest, ...)` - got `"a\r\nb\r\r\nc\r"`, want `"a\nb\r\nc\r"`.
**Named kill: the stdout payload equality.** Revert.

- [ ] **Step 3: M1b - the agent-side transform (the second flush call site)**

In `internal/agent/runner.go`, in `Run`'s per-step loop, delete the line `errW.flush()`.

Run the test. Expected: FAIL at `require.Equal(t, e2eStderrOut, gotErr.String(), ...)` - got `"d\ne"`, want `"d\ne\r"`.
**Named kill: the stderr payload equality.** This is the mutation CLAUDE.md records as having left all 21 packages green before `runner_crlf_test.go` existed; it must die here too, because that test ends at a channel and this one ends at a database row. Revert.

- [ ] **Step 4: M2 - the chunk framing (epoch stamping)**

In `internal/agent/agent.go`, in `handleDispatch`, change:

```go
	runner, runCtx := newRunner(task.TaskId, task.Epoch, a.sendCh, a.runCtx, task.TimeoutSeconds)
```

to:

```go
	runner, runCtx := newRunner(task.TaskId, 0, a.sendCh, a.runCtx, task.TimeoutSeconds)
```

Run the test. Expected: FAIL, **bounded, at** `timed out after 1m0s waiting for the task to reach a terminal status`. Every chunk and every status now carries epoch 0, so `AppendTaskLog`'s and `UpdateTaskStatus`'s fences reject all of them and the task never leaves `dispatched`.
**Named kill: the terminal-status `waitFor`.** Note the shape: this mutant costs a full 60 seconds and produces a FAILED test with a message naming the step, not a hang - which is exactly what Task A2 exists for. Nothing else in the repo reddens on this mutation: `dispatch_test.go` stops at a `fakeSender`, and every `Runner` test builds its own epoch. Revert.

- [ ] **Step 5: M2b - the chunk framing (stream stamping)**

In `internal/agent/runner.go`, in `chunkWriter.Write`'s enqueued `TaskLogChunk`, change `Stream: w.stream` to `Stream: relayv1.LogStream_LOG_STREAM_STDOUT`.

Run the test. Expected: FAIL at `require.Equal(t, e2eStderrOut, gotErr.String(), ...)` - got `""`, want `"d\ne\r"`.
**Named kill: the stderr payload equality.** Revert.

- [ ] **Step 6: M3 - the handler's ingest (byte fidelity)**

In `internal/worker/handler.go`, in `handleTaskLog`, change:

```go
	content := string(chunk.Content)
```

to:

```go
	content := strings.ReplaceAll(string(chunk.Content), "\r", "")
```

(`strings` is already imported there.) This is the plausible bad fix - a server-side CR strip written by someone "fixing" the CRLF bug one layer too low.

Run the test. Expected: FAIL at `require.Equal(t, e2eStdoutOut, rest, ...)` - got `"a\nbc"`, want `"a\nb\r\nc\r"`.
**Named kill: the stdout payload equality.** This is the assertion that makes the CRLF criterion OBSERVED: it is the only place in the repo where a stored `task_logs.content` is compared byte-for-byte against what a real subprocess wrote. Revert.

- [ ] **Step 7: M3b - the handler's ingest (stream mapping)**

In `internal/worker/handler.go`, in `handleTaskLog`, delete:

```go
	if chunk.Stream == relayv1.LogStream_LOG_STREAM_STDERR {
		stream = "stderr"
	}
```

Run the test. Expected: FAIL at `require.Equal(t, e2eStderrOut, gotErr.String(), ...)` - got `""`.
**Named kill: the stderr payload equality.** Revert.

- [ ] **Step 8: M4 - the dispatch direction (a deleted append)**

In `internal/agent/runner.go`, in `Run`, delete:

```go
	if task.TaskUrl != "" {
		env = append(env, "RELAY_TASK_URL="+task.TaskUrl)
	}
```

Run the test. Expected: FAIL at the identity map `require.Equal` - `RELAY_TASK_URL` reads `"POISON-inherited-RELAY_TASK_URL"`, not the rendered URL.
**Named kill: the identity map equality, and the poison is what makes it an equality kill rather than an absence kill.** Revert.

- [ ] **Step 9: M5 - the dispatch direction (a transposed pair)**

In `internal/scheduler/dispatch.go`, in `sendTask`, change:

```go
		TaskUrl:        taskURL(d.publicBaseURL, jobIDStr, taskIDStr),
```

to:

```go
		TaskUrl:        taskURL(d.publicBaseURL, taskIDStr, jobIDStr),
```

Run the test. Expected: FAIL at the identity map `require.Equal` - the two UUIDs appear in the wrong path positions.
**Named kill: the identity map equality, and it holds only because the expected string is built from the ids the test seeded rather than from `dt.JobId`/`dt.TaskId`.** Revert.

- [ ] **Step 10: Verify the reverts and re-run the control**

Run: `git status --porcelain` - expect no modification under `internal/`.
Run: `go test -tags integration -count=1 ./cmd/relay-server/... -run TestAgentSubprocessEndToEnd -v -timeout 300s`
Expected: PASS. A mutation that was silently not applied reports "survived"; this control is what distinguishes that from a genuinely surviving mutant.

- [ ] **Step 11: Record the battery in the commit, not in the test**

RED/GREEN history and mutation provenance go in the commit message, per CLAUDE.md's Comments rules. Do not add a census of these eight mutations to the test file.

```bash
git commit --allow-empty -m "test: record the agent e2e harness's mutation battery

Eight mutations, all killed, control green before and after:
  M1  runner.go collapseCRLFInPlace dropped        -> stdout payload equality
  M1b runner.go errW.flush() deleted               -> stderr payload equality
  M2  agent.go newRunner epoch 0                   -> terminal-status waitFor (bounded, 60s)
  M2b runner.go chunk Stream hardcoded to STDOUT   -> stderr payload equality
  M3  handler.go content CR-stripped on ingest     -> stdout payload equality
  M3b handler.go stderr stream mapping deleted     -> stderr payload equality
  M4  runner.go RELAY_TASK_URL append deleted      -> identity map equality (reads the poison)
  M5  dispatch.go taskURL args transposed          -> identity map equality

M2 is the one nothing in the repo reddened on before: dispatch_test.go stops at
a fakeSender and every Runner test supplies its own epoch, so agent-side epoch
stamping composed with the server-side fence only by assumption."
```

---

# Slice B - put the lane in CI

Sequential after Slice A. Its whole purpose is that the guard lives in a lane that runs on the commits that can break it.

### Task B1: Move `cmd/relay-server`'s three Postgres helpers onto pgdsn

**Files:**
- Modify: `cmd/relay-server/bootstrap_test.go` (`newTestQueries`)
- Modify: `cmd/relay-server/grpc_admission_e2e_integration_test.go` (`newTestPoolAndQueries`)
- Modify: `cmd/relay-server/startup_reconcile_test.go` (`setupPgForStartup`)

- [ ] **Step 1: Rewrite `newTestPoolAndQueries`**

In `cmd/relay-server/grpc_admission_e2e_integration_test.go`, replace the whole `newTestPoolAndQueries` function with:

```go
// newTestPoolAndQueries takes a fresh migrated database from the shared harness
// and returns the pool alongside the *store.Queries wrapping it. worker.Handler
// needs the pool itself, not just the Queries: applyInventory opens its own
// transaction via pgx.BeginTxFunc(ctx, h.pool, ...) even for an empty inventory
// update, on every finishRegister call including the reconnect path this file
// exercises.
func newTestPoolAndQueries(t *testing.T) (*pgxpool.Pool, *store.Queries) {
	t.Helper()
	return newPgdsnPoolAndQueries(t)
}
```

Delete the imports that become unused (`testcontainers`, `tcpostgres`, `wait`) - check each against the rest of the file first; `context` is used by the tests themselves.

- [ ] **Step 2: Rewrite `newTestQueries`**

In `cmd/relay-server/bootstrap_test.go`, replace `newTestQueries` with:

```go
func newTestQueries(t *testing.T) *store.Queries {
	t.Helper()
	_, q := newPgdsnPoolAndQueries(t)
	return q
}
```

Delete the imports that become unused (`context`, `pgxpool`, `testcontainers`, `tcpostgres`, `wait`) - check each against the rest of the file first.

- [ ] **Step 3: Rewrite `setupPgForStartup`**

In `cmd/relay-server/startup_reconcile_test.go`, replace `setupPgForStartup` with:

```go
func setupPgForStartup(t *testing.T) (*store.Queries, *pgxpool.Pool) {
	t.Helper()
	pool, q := newPgdsnPoolAndQueries(t)
	return q, pool
}
```

Note the swapped return order - this helper returns `(queries, pool)` while `newPgdsnPoolAndQueries` returns `(pool, queries)`. The two types differ, so a transposition is a compile error rather than a silent bug, but keep the named locals above rather than a bare `return newPgdsnPoolAndQueries(t)`, which would not compile. Delete the imports that become unused.

- [ ] **Step 4: Run the whole package's integration lane both ways**

Run (container mode, needs Docker):
`go test -tags integration -count=1 ./cmd/relay-server/... -timeout 900s`
Expected: PASS.

Run (shared-service mode, the CI mode):
```
$env:RELAY_TEST_DATABASE_URL="postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable"
go test -tags integration -count=1 ./cmd/relay-server/... -timeout 900s
```
Expected: PASS, and noticeably faster - record both wall-clock numbers, since the second is what CI will pay.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/bootstrap_test.go cmd/relay-server/grpc_admission_e2e_integration_test.go cmd/relay-server/startup_reconcile_test.go
git commit -m "test: take cmd/relay-server's integration databases from the shared pgdsn harness"
```

---

### Task B2: Add the package to the CI lane

**Files:**
- Modify: `Makefile` (`test-pg-integration`)
- Modify: `.github/workflows/go-ci.yml` (`pg-integration` job)

- [ ] **Step 1: Extend the Makefile target**

Change the recipe line to:

```make
test-pg-integration:
	go test -tags integration -count=1 ./internal/store/... ./internal/schedrunner/... ./internal/testsupport/... ./cmd/relay-server/... -timeout 600s
```

Replace the target comment's first paragraph (currently "Run the store and schedrunner integration lanes, plus internal/testsupport/pgdsn's own database-touching self-test - the packages ... uses, so the same two modes ... apply here too.") with:

```make
# Run the Postgres-only integration lanes: internal/store, internal/schedrunner,
# internal/testsupport/pgdsn's own database-touching self-test, and
# cmd/relay-server - the packages .github/workflows/go-ci.yml's pg-integration
# job runs. All four take their database from internal/testsupport/pgdsn, the
# same harness test-cli-integration uses, so the same two modes (unset: one
# testcontainer per test; RELAY_TEST_DATABASE_URL set: one CREATEd database per
# test on a shared server) apply here too.
#
# cmd/relay-server is here because its integration lane needs POSTGRES AND
# NOTHING ELSE. Its gRPC servers listen on 127.0.0.1:0 in-process, its agent
# (agent_subprocess_e2e_integration_test.go) is an in-process agent.Agent rather
# than a built binary, and the subprocess it runs is the test binary itself. No
# Docker daemon, no image, no p4d.
```

Leave the existing `-count=1` and no-`-p 1` paragraphs unchanged.

- [ ] **Step 2: Update the workflow job**

In `.github/workflows/go-ci.yml`, change the `pg-integration` job's `name:` to:

```yaml
    name: pg integration (store, schedrunner, relay-server)
```

In its `timeout-minutes` comment, change "this target names three packages" to "this target names four packages". Change the step name to:

```yaml
      - name: Postgres integration lanes (internal/store, internal/schedrunner, cmd/relay-server)
```

- [ ] **Step 3: Run the target exactly as CI will**

```
$env:RELAY_TEST_DATABASE_URL="postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable"
make test-pg-integration
```
Expected: PASS. Compare the wall clock against the job's `timeout-minutes: 12`. If the total is above about 8 minutes, raise the job timeout in the same commit and say so in the message - do not merge a lane that is one slow run from an ambiguous kill.

- [ ] **Step 4: Commit**

```bash
git add Makefile .github/workflows/go-ci.yml
git commit -m "ci: run cmd/relay-server's integration lane in the pg-integration job"
```

---

### Task B3: Correct the two prose claims this slice falsifies

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`

- [ ] **Step 1: Update CLAUDE.md's Commands block**

The `make test-pg-integration` entry currently reads:

```
# internal/store and internal/schedrunner's integration lanes, plus
# internal/testsupport/pgdsn's own database-touching self-test - the packages
# this lane covers. Same two modes as test-cli-integration above.
```

Replace with:

```
# The Postgres-only integration lanes: internal/store, internal/schedrunner,
# cmd/relay-server, and internal/testsupport/pgdsn's own database-touching
# self-test. Same two modes as test-cli-integration above. cmd/relay-server
# qualifies because its gRPC servers and its agent both run in-process and its
# task subprocess is the test binary, so it needs a database and nothing else.
```

**After this edit**, per CLAUDE.md's own line-endings section: print the before/after line counts and check the delta is the four lines intended, check the diffstat against the size of the change, run `git ls-files --eol CLAUDE.md` (expect `i/lf`), and confirm the file still decodes as UTF-8. This edit introduces no non-ASCII bytes; assert that rather than assuming it.

- [ ] **Step 2: Append the refutation to the backlog item**

Append to `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`:

```markdown
## Appended 2026-09-04 - the "a real gRPC agent is not covered" clause was wrong, and cmd/relay-server has joined the lane

The 2026-09-04 entry above closes with "guards that need p4d
(`internal/agent/source/perforce`) or a real gRPC agent are still not covered by this
mechanism". **The gRPC-agent half of that is refuted.** "A real gRPC agent" is a library
object in this repo, not a process: `agent.NewAgent(addr, ...)` plus `Agent.Run(ctx)` dials
any address, and `internal/agent/agent_test.go`'s `startFakeCoord` has been running a real
`agent.Agent` against a real `grpc.NewServer` on `127.0.0.1:0` in the DEFAULT, untagged,
Docker-free lane the whole time.

`cmd/relay-server/agent_subprocess_e2e_integration_test.go` is the demonstration: a real
`agent.Agent` against the listener `grpcServerOptions`/`netlimit.Wrap` build, a real
`scheduler.Dispatcher`, and a real task subprocess (the test binary, re-exec'd), reading
`task_logs` back out of Postgres. Its only external dependency is a database, so it takes one
from `internal/testsupport/pgdsn` and `cmd/relay-server` was added to `make test-pg-integration`
and the `pg-integration` job. That also brings the `grpc_admission_e2e` guards this item names
in its Summary into a lane CI runs, for the first time.

The p4d half of the clause stands unchanged.
```

Apply the same post-edit checks as Step 1.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md
git commit -m "docs: correct the claim that a real gRPC agent cannot join the services-postgres lane"
```

---

## Self-review

**Spec coverage against the item's Acceptance / Done When:**

| Criterion | Task |
|---|---|
| "A test exists in which bytes written by a real subprocess are read back from a real `task_logs` row, having crossed a real gRPC stream." | A5 (the whole harness; `q.GetTaskLogs` is the read, `startAgentE2EServer` + `agent.NewAgent` is the wire) |
| "It fails if the agent-side transform ... changes incompatibly." | A9 steps 2-3 (M1, M1b) |
| "... the chunk framing ..." | A9 steps 4-5 (M2, M2b) |
| "... or the handler's ingest changes incompatibly." | A9 steps 6-7 (M3, M3b) |
| "The CRLF case is one of its inputs, so the closed item's criterion becomes observed rather than argued." | A1 (the exact bytes), A5 (the exact stored bytes), A9 step 6 (the kill that makes it discriminating) |
| Notes: "assert both: bytes ... AND the four identity variables the coordinator rendered are the ones the subprocess observes." | A6, with A9 steps 8-9 as the kills |
| Brief: bounded failure, no hangs | A2 (its own task, with its own test), and A9 step 4 records the one mutant whose kill is a timeout, showing it is bounded and named |
| Brief: portability, Windows and Linux | D1, and the doc comment in A1 |
| Brief: waits owned by the test | D5, enforced in A5 (own worker id, own task id, own database) |
| Brief: CI decision with evidence | The refutation section, plus Slice B |

**Placeholder scan:** no TBD, no "add appropriate error handling", no "similar to Task N". Every code step carries the code.

**Type consistency:** `newPgdsnPoolAndQueries` returns `(*pgxpool.Pool, *store.Queries)` and is called with that order in A3, A5, B1 step 1 and B1 step 2, with the swapped destructure explicitly flagged in B1 step 3. `waitFor(t, what, limit, cond)` has one signature and is used at three call sites identically; `waitForOn` takes `fatalT` and is the only bridge. `e2eStdoutIn`/`e2eStdoutOut`/`e2eStderrIn`/`e2eStderrOut` are named consistently in A1, A5 and A9. `identityLinePrefix`, `agentE2EHelperEnv`, `e2eIdentityNames` and `e2ePublicBase` are each defined once. `startAgentE2EServer` returns `(string, *worker.Registry)` in A3 and is destructured that way in A5. `seedAgentE2EWorker` returns `(pgtype.UUID, *agent.Credentials)`; `seedAgentE2EJob` returns `(pgtype.UUID, pgtype.UUID)` as `(jobID, taskID)` and is used in that order in A5 and A6.

**Vocabulary check:** the terminal switch in A5 uses `done, failed, timed_out` - the exact terminal subset of `tasks_status_check` in migration `000023_task_preparing_status.up.sql`. See D4b.

---

## What this does not close

- **p4d-dependent guards.** `internal/agent/source/perforce` still has no CI lane, and Slice B does not give it one.
- **The rest of `make test-integration`.** `internal/api` and `internal/worker` still run only when a human runs them; their helpers still reach for testcontainers directly. Moving them is the same mechanical change as Task B1 and is deliberately out of scope here - `idea-2026-08-23` stays open for them.
- **The browser half.** `idea-2026-08-24-e2e-harness-slice-2-agent-in-harness` runs a real agent inside the PLAYWRIGHT harness so browser surfaces become reachable. This plan is byte fidelity at the Go level and closes none of it, exactly as the item's Related section says.
- **`bug-2026-08-25-windows-crlf-log-lines-render-blank` is already closed and stays closed.** This plan makes its criterion observed at the Go level; the "renders its text on the job detail page" half is the browser harness's job.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-09-04-agent-subprocess-to-task-logs-e2e-plan.md`. Two execution options:

**1. Subagent-Driven (recommended)** - a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
