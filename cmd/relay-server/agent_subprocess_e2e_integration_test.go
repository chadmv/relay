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
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
