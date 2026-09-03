package agent

import (
	"bytes"
	"context"
	"errors"
	"log"
	"runtime"
	"strings"
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

// Without an exit line an operator watching a wedged agent cannot tell "still
// running step 2" from "step 2 finished and nothing happened next". The two
// steps differ in outcome so a line emitted on only one path fails the count.
func TestRunner_EveryStepLogsItsStartAndItsExit(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 64)

	r, runCtx := newRunner("steps-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "steps-task",
		Commands: []*relayv1.CommandLine{
			{Argv: echoArgv("alpha")},
			{Argv: failArgv()},
		},
	})

	out := logged()
	assert.Contains(t, out, "exec step 1/2")
	assert.Contains(t, out, "exec step 2/2")
	assert.Contains(t, out, "step 1/2 for steps-task exited (exit=0")
	assert.Contains(t, out, "step 2/2 for steps-task exited (exit=7")
	assert.Equal(t, 2, strings.Count(out, "exited ("),
		"one exit line per step that ran, no more: %s", out)
}

// forgedProgram carries a real newline, written as a raw literal rather than an
// escape so no shell or editor layer can quietly turn it into something else.
const forgedProgram = `nope
runner: step 9/9 for victim-task exited (exit=0, err=<nil>)`

// forgedTail is the second line of forgedProgram with its leading newline.
const forgedTail = `
runner: step 9/9 for victim-task`

// argv is validated nowhere: normalizeTaskCommands checks only that it is
// non-empty, so argv[0] may contain a newline. runner.go designates the host log
// the record that survives when the send does not, which is what makes a forged
// entry in it worth something to a submitter.
// TestRunner_AStepLineNamesTheProgramAndNotItsArguments cannot see this: its
// token lives in argv[1:].
func TestRunner_AStepLineQuotesTheProgramSoItCannotForgeALogLine(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 64)

	r, runCtx := newRunner("forge-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "forge-task",
		Commands: singleCmd([]string{forgedProgram}),
	})

	out := logged()
	require.Contains(t, out, "exec step 1/1", "the step line must still be emitted")
	assert.NotContains(t, out, forgedTail,
		"a newline in argv[0] must not be able to start a new host-log line")
}

// The volume half of the pair. An unbounded argv[0] writes an unbounded line per
// step, and the same string reaches the log a second time inside the exec error.
func TestRunner_AStepLineBoundsAnOverlongProgramName(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 64)

	r, runCtx := newRunner("huge-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "huge-task",
		Commands: singleCmd([]string{strings.Repeat("A", 100_000)}),
	})

	out := logged()
	require.Contains(t, out, "exec step 1/1", "the step line must still be emitted")
	assert.Less(t, len(out), 10_000,
		"one over-long argv[0] must not write an unbounded host-log line")
}

// A step whose binary is missing breaks out of the loop BEFORE the exit line, so
// without a line of its own it announces its start and then falls silent - the
// exact ambiguity the exit line exists to remove. sendFinalStatus carries no
// ErrorMessage on this path either, so the coordinator has no cause to store.
func TestRunner_AStepThatCannotStartSaysSoInsteadOfGoingSilent(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 64)

	r, runCtx := newRunner("nostart-task", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "nostart-task",
		Commands: singleCmd([]string{"relay-no-such-binary-xyz"}),
	})

	out := logged()
	require.Contains(t, out, "exec step 1/1", "the step still announces itself")
	assert.Contains(t, out, "failed to start",
		"a step that never ran must say so rather than leaving the log ambiguous")
	assert.Contains(t, out, "executable file not found", "and it must carry the cause")
}

// The prepare cause is caller-controlled by the same route as argv[0] and by a
// longer one: validateSourceSpec constrains a stream to a `//` prefix and no
// character set, so a newline in a depot path reaches p4's args, the error the
// provider returns, and this line. It is unbounded for the same reason - the
// error carries p4 output.
func TestRunner_APrepareFailureQuotesAndBoundsItsCause(t *testing.T) {
	logged := captureAgentLog(t)
	sendCh := make(chan *relayv1.AgentMessage, 16)

	r, runCtx := newRunner("prep-forge", 0, sendCh, context.Background(), 0)
	r.SetProviderForTest(&fakeProvider{
		prepareErr: errors.New(forgedProgram + strings.Repeat("B", 100_000)),
	})
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "prep-forge",
		Commands: singleCmd(echoTaskCmd()),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	})

	out := logged()
	require.Contains(t, out, "prepare failed for prep-forge", "the line must still be emitted")
	assert.NotContains(t, out, forgedTail,
		"a newline in the prepare cause must not start a new host-log line")
	assert.Less(t, len(out), 10_000, "and the cause must be bounded")
}
