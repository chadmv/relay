package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoArgv returns a cross-platform argv that echoes the given string.
func echoArgv(s string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", s}
	}
	return []string{"echo", s}
}

// failArgv returns a cross-platform argv that exits non-zero with a
// distinctive code.
func failArgv() []string {
	if runtime.GOOS == "windows" {
		// `exit /b 7` exits with code 7 inside cmd.
		return []string{"cmd", "/c", "exit /b 7"}
	}
	return []string{"sh", "-c", "exit 7"}
}

func TestRunner_MultiStepAllSucceed(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	runner, runCtx := newRunner("multi-ok", 0, sendCh, context.Background(), 0)
	runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "multi-ok",
		Commands: []*relayv1.CommandLine{
			{Argv: echoArgv("alpha")},
			{Argv: echoArgv("bravo")},
			{Argv: echoArgv("charlie")},
		},
	})

	msgs := collectMessages(sendCh, 1500*time.Millisecond)
	require.NotEmpty(t, msgs)

	last := msgs[len(msgs)-1].GetTaskStatus()
	require.NotNil(t, last)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_DONE, last.Status)
	require.NotNil(t, last.ExitCode)
	assert.Equal(t, int32(0), *last.ExitCode)

	logs := collectStdoutLogs(msgs)
	assert.Equal(t, 3, strings.Count(logs, "=== relay step"),
		"expected one step marker per command, logs:\n%s", logs)
	for _, want := range []string{"step 1/3", "step 2/3", "step 3/3", "alpha", "bravo", "charlie"} {
		assert.Contains(t, logs, want)
	}

	// Every TaskLogChunk emitted during a step must carry step_total=3 and a
	// step_index matching the step that produced it. Chunks with step_index=0
	// would only appear from PREPARE-phase output, which this task does not have.
	stepCounts := map[int32]int{}
	for _, m := range msgs {
		l := m.GetTaskLog()
		if l == nil {
			continue
		}
		assert.Equal(t, int32(3), l.StepTotal, "every chunk should carry step_total=3, got chunk %q", string(l.Content))
		assert.GreaterOrEqual(t, l.StepIndex, int32(1), "every chunk in this task should belong to a numbered step")
		assert.LessOrEqual(t, l.StepIndex, int32(3))
		stepCounts[l.StepIndex]++
	}
	assert.Greater(t, stepCounts[1], 0, "expected at least one chunk for step 1")
	assert.Greater(t, stepCounts[2], 0, "expected at least one chunk for step 2")
	assert.Greater(t, stepCounts[3], 0, "expected at least one chunk for step 3")
}

func TestRunner_MultiStepFailFastSkipsRest(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	runner, runCtx := newRunner("multi-fail", 0, sendCh, context.Background(), 0)
	runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "multi-fail",
		Commands: []*relayv1.CommandLine{
			{Argv: echoArgv("first-ok")},
			{Argv: failArgv()},
			{Argv: echoArgv("must-not-run")},
		},
	})

	msgs := collectMessages(sendCh, 1500*time.Millisecond)
	require.NotEmpty(t, msgs)

	last := msgs[len(msgs)-1].GetTaskStatus()
	require.NotNil(t, last)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_FAILED, last.Status)
	require.NotNil(t, last.ExitCode, "failing step's exit code must be reported")
	assert.Equal(t, int32(7), *last.ExitCode)

	logs := collectStdoutLogs(msgs)
	assert.Contains(t, logs, "first-ok", "step 1 stdout should be present")
	assert.Contains(t, logs, "step 1/3")
	assert.Contains(t, logs, "step 2/3")
	assert.NotContains(t, logs, "step 3/3", "step 3 must not have run after step 2 failed")
	assert.NotContains(t, logs, "must-not-run", "step 3 stdout must not be present")

	// No chunk with step_index=3 should have been emitted (step 3 never ran).
	for _, m := range msgs {
		l := m.GetTaskLog()
		if l == nil {
			continue
		}
		assert.NotEqual(t, int32(3), l.StepIndex, "no chunk should claim to belong to step 3 (it never ran); chunk: %q", string(l.Content))
		// And every emitted chunk should report step_total=3.
		if l.StepIndex != 0 {
			assert.Equal(t, int32(3), l.StepTotal, "chunk in step %d should report step_total=3", l.StepIndex)
		}
	}
}

func TestRunner_SingleCommandReportsStepOneOfOne(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	runner, runCtx := newRunner("single", 0, sendCh, context.Background(), 0)
	runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "single",
		Commands: []*relayv1.CommandLine{
			{Argv: echoArgv("only")},
		},
	})

	msgs := collectMessages(sendCh, 1500*time.Millisecond)
	require.NotEmpty(t, msgs)

	last := msgs[len(msgs)-1].GetTaskStatus()
	require.NotNil(t, last)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_DONE, last.Status)

	sawChunk := false
	for _, m := range msgs {
		l := m.GetTaskLog()
		if l == nil {
			continue
		}
		sawChunk = true
		assert.Equal(t, int32(1), l.StepIndex, "single-command task must report step_index=1, chunk: %q", string(l.Content))
		assert.Equal(t, int32(1), l.StepTotal, "single-command task must report step_total=1, chunk: %q", string(l.Content))
	}
	assert.True(t, sawChunk, "expected at least one TaskLogChunk (the marker)")
}

func collectStdoutLogs(msgs []*relayv1.AgentMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		if l := m.GetTaskLog(); l != nil && l.Stream == relayv1.LogStream_LOG_STREAM_STDOUT {
			b.Write(l.Content)
		}
	}
	return b.String()
}
