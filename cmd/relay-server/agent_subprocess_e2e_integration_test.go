//go:build integration

package main

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
// os.Exit(0) IS NOT OPTIONAL: this binary is exec'd directly as os.Args[0]
// plus one -test.run flag, never through `go test`, so cmd/go's own
// -test.paniconexit0 is never set here and Exit(0) is a plain process exit.
// Returning normally instead would let the testing package finish its own
// run and print "PASS\n" onto the same stdout the parent harness asserts on.
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

// waitFor polls cond until it returns true, and FAILS THE TEST BY NAME if it
// does not within limit.
//
// EVERY POLL-UNTIL-TRUE WAIT IN THIS FILE GOES THROUGH IT, and that is the
// point. This harness starts a subprocess and a gRPC stream, and a mutation
// anywhere in the path under test typically manifests as "the thing never
// happens" rather than "the thing happens wrongly" - an epoch-0 mutant makes
// the fence reject every status, so the task never reaches a terminal state at
// all. Without a deadline that case is an indefinite hang, which reads in a
// mutation battery exactly like a wedged container or a lost Docker socket.
// With one it is a named failure that says which step never completed.
//
// cond runs on the test goroutine, so t.Fatalf here is legal and aborts the
// test rather than leaking a goroutine that logs after the test returns.
func waitFor(t *testing.T, what string, limit time.Duration, cond func() bool) {
	t.Helper()
	waitForOn(t, what, limit, cond)
}

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

func TestWaitFor_ATimeoutFailsByNameInsteadOfHanging(t *testing.T) {
	fake := &fakeFatalT{}
	waitForOn(fake, "the thing that never happens", 50*time.Millisecond, func() bool { return false })
	require.True(t, fake.failed, "a wait that never succeeds must fail, not return quietly")
	require.Contains(t, fake.msg, "the thing that never happens",
		"the failure must name what it was waiting for; an unnamed timeout is unreadable in a mutation battery")
}

// newPgdsnPoolAndQueries takes a fresh migrated database from the shared
// harness: pgdsn gives one testcontainer per call when RELAY_TEST_DATABASE_URL
// is unset and one freshly CREATEd database on a supplied server when it is
// set, which is the mode .github/workflows/go-ci.yml's Postgres-service jobs
// use - so this harness can run in CI with no Docker daemon at all.
func newPgdsnPoolAndQueries(t *testing.T) (*pgxpool.Pool, *store.Queries) {
	t.Helper()
	dsn := pgdsn.NewIntegrationDSN(t)
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pgdsn.BoundedCleanup(t, "cmd/relay-server pgxpool.Close", pool.Close) })
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
//
// WHY THE INTEGRATION LANE, AND WHY THAT IS NOT THE END OF THE SENTENCE HERE.
// Reaching Connect's message loop is past authenticateAndRegister, which is a
// Postgres round trip, so there is no default-lane home for this. The database
// comes from internal/testsupport/pgdsn, the gRPC server and the agent are both
// in-process, and the subprocess is this test binary. It therefore runs in
// go-ci.yml's pg-integration job on every push. Do not move it behind a helper
// that reaches for testcontainers directly; that would take it back out of CI
// without changing a line of the test.
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
	require.NotEmpty(t, rows,
		"fixture: zero task_logs rows means nothing crossed the wire; without this check the failure "+
			"below would read as a confusing marker-line mismatch instead of naming the real cause")

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
	marker, rest, ok := strings.Cut(gotOut.String(), "\n")
	require.True(t, ok,
		"expected the step marker line then the subprocess bytes in task_logs; got %q", gotOut.String())
	require.True(t, strings.HasPrefix(marker, "=== relay step 1/1 === "),
		"fixture: the first stdout content must be the synthetic step marker, so the Cut above removed "+
			"the marker and not the first line of real output; got %q", marker)

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

	require.Equal(t, e2eStdoutOut, rest,
		"the bytes a real subprocess wrote must reach task_logs as the exact CRLF transform of what it wrote")
	require.Equal(t, e2eStderrOut, gotErr.String(),
		"stderr is a SECOND flush call site and a SECOND stream mapping; asserting only on stdout leaves both unpinned")

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

// uuidStringFromPG renders a pgtype.UUID via pgtype's own Value(), deliberately
// not via dispatch.go's fmt.Sprintf-based uuidStr: the expected value must not
// derive from the code under test, or a shared formatting bug would cancel out.
func uuidStringFromPG(t *testing.T, u pgtype.UUID) string {
	t.Helper()
	v, err := u.Value()
	require.NoError(t, err)
	s, ok := v.(string)
	require.True(t, ok, "pgtype.UUID.Value must render a string, got %T", v)
	return s
}
