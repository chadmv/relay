package agent

import (
	"bytes"
	"context"
	"errors"
	"log"
	"runtime"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureAgentLog redirects the standard logger for the test's duration and
// returns an accessor for what was written. The restore is unconditional. The
// capture is process-global, so it is only safe while no test in this package
// calls t.Parallel; do not add the first one.
func captureAgentLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return buf.String
}

// tokenArgv is a command that succeeds on either platform while carrying a
// token-shaped argument. The token is the discriminator; the program name is
// what the step line is allowed to carry.
func tokenArgv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", "--token", "SUPER-SECRET-TOKEN"}
	}
	return []string{"echo", "--token", "SUPER-SECRET-TOKEN"}
}

// The presence assertions come FIRST and are what makes this test discriminate:
// "does not contain the token" is satisfied by a runner that logs nothing at all.
//
// The narrowing bounds the HOST log and closes nothing. sendStepMarker, two
// statements away, writes strings.Join(argv, " ") into task_logs, so the same
// token is already stored and readable through the API. Do not read this test as
// a secrecy guarantee.
func TestRunner_AStepLineNamesTheProgramAndNotItsArguments(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 64)

	r, runCtx := newRunner("secret-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "secret-task",
		Commands: singleCmd(tokenArgv()),
	})

	out := logged()
	require.Contains(t, out, "exec step 1/1",
		"every step must announce itself on the host log before it runs")
	require.Contains(t, out, tokenArgv()[0],
		"the step line must name the program so an operator can see what is running")
	require.Contains(t, out, "secret-task", "every lifecycle line carries the task id")
	assert.NotContains(t, out, "SUPER-SECRET-TOKEN",
		"nothing beyond argv[0] may reach the host log; arguments are unsanitised")
}

// The prepare cause on the host log is the record that survives when the
// PREPARE_FAILED send does not, which is the case this line exists for: a
// coordinator that never received the message has no other trace of the cause.
func TestRunner_APrepareFailureIsOnTheHostLogWithItsCause(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 16)

	const cause = "p4 sync failed: PREPARE-CAUSE-SENTINEL"
	r, runCtx := newRunner("prep-task", 0, sendCh, context.Background(), 0)
	r.SetProviderForTest(&fakeProvider{prepareErr: errors.New(cause)})
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "prep-task",
		Commands: singleCmd(echoTaskCmd()),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	})

	out := logged()
	assert.Contains(t, out, "preparing workspace", "the prepare phase must announce itself")
	assert.Contains(t, out, "PREPARE-CAUSE-SENTINEL", "the cause must reach the host log")
	assert.Contains(t, out, "prep-task", "every lifecycle line carries the task id")
}

// A source-bearing task on a worker with no provider returns before the prepare
// line's position, so without a line of its own it leaves no host record at all -
// and the operator who fixes it is standing at this host.
func TestRunner_ASourceTaskWithNoProviderLogsWhyOnTheHost(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 16)

	// No SetProviderForTest call: r.provider is nil.
	r, runCtx := newRunner("noprov-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "noprov-task",
		Commands: singleCmd(echoTaskCmd()),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	})

	out := logged()
	assert.Contains(t, out, "no workspace provider")
	assert.Contains(t, out, "RELAY_WORKSPACE_ROOT", "the line must name what the operator has to fix")
	assert.Contains(t, out, "noprov-task")
	assert.NotContains(t, out, "exec step", "the command must not run when the provider is nil")
}
