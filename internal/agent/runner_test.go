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

// echoCmd returns a cross-platform command that prints "hello" to stdout.
func echoCmd() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", "hello"}
	}
	return []string{"echo", "hello"}
}

// sleepCmd returns a cross-platform command that sleeps for ~10 seconds.
func sleepCmd() []string {
	if runtime.GOOS == "windows" {
		// ping blocks ~1s per count; 11 counts ≈ 10s
		return []string{"ping", "-n", "11", "127.0.0.1"}
	}
	return []string{"sleep", "10"}
}

func collectMessages(ch chan *relayv1.AgentMessage, timeout time.Duration) []*relayv1.AgentMessage {
	var msgs []*relayv1.AgentMessage
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			msgs = append(msgs, msg)
		case <-deadline:
			return msgs
		}
	}
}

func TestRunner_done(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 32)
	ctx := context.Background()

	cmd := echoCmd()
	runner, runCtx := newRunner("task-1", 0, sendCh, ctx, 0)
	runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId:  "task-1",
		Command: cmd,
	})

	msgs := collectMessages(sendCh, 500*time.Millisecond)
	require.GreaterOrEqual(t, len(msgs), 2, "expected at least RUNNING + final status")

	// First message must be RUNNING.
	first := msgs[0].GetTaskStatus()
	require.NotNil(t, first)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_RUNNING, first.Status)

	// Last message must be DONE.
	last := msgs[len(msgs)-1].GetTaskStatus()
	require.NotNil(t, last)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_DONE, last.Status)

	// At least one log chunk containing "hello".
	var hasLog bool
	for _, m := range msgs {
		if chunk := m.GetTaskLog(); chunk != nil {
			if strings.Contains(string(chunk.Content), "hello") {
				hasLog = true
			}
		}
	}
	assert.True(t, hasLog, "expected at least one log chunk containing 'hello'")
}

func TestRunner_timeout(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 32)
	ctx := context.Background()

	runner, runCtx := newRunner("task-2", 0, sendCh, ctx, 1) // 1 second timeout
	runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId:  "task-2",
		Command: sleepCmd(),
	})

	msgs := collectMessages(sendCh, 3*time.Second)
	require.GreaterOrEqual(t, len(msgs), 2)

	last := msgs[len(msgs)-1].GetTaskStatus()
	require.NotNil(t, last)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_TIMED_OUT, last.Status)
}

func TestRunner_cancel(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 32)
	ctx := context.Background()

	runner, runCtx := newRunner("task-3", 0, sendCh, ctx, 0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(runCtx, &relayv1.DispatchTask{
			TaskId:  "task-3",
			Command: sleepCmd(),
		})
	}()

	// Wait for RUNNING then cancel.
	var gotRunning bool
	for !gotRunning {
		select {
		case msg := <-sendCh:
			if s := msg.GetTaskStatus(); s != nil && s.Status == relayv1.TaskStatus_TASK_STATUS_RUNNING {
				gotRunning = true
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for RUNNING status")
		}
	}

	runner.Cancel()
	<-done

	msgs := collectMessages(sendCh, 500*time.Millisecond)
	var finalStatus relayv1.TaskStatus
	for _, m := range msgs {
		if s := m.GetTaskStatus(); s != nil {
			finalStatus = s.Status
		}
	}
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_FAILED, finalStatus)
}

func TestRunner_SendBlocksUntilCapacityOrCancel(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 1)

	ctx, cancel := context.WithCancel(context.Background())
	r, _ := NewRunnerForTest("task-x", sendCh, ctx, 0)

	// First send fills the 1-slot buffer.
	RunnerSendForTest(r, &relayv1.AgentMessage{})

	// Second send must block (channel is full and nobody is reading).
	sendReturned := make(chan struct{})
	go func() {
		RunnerSendForTest(r, &relayv1.AgentMessage{})
		close(sendReturned)
	}()

	select {
	case <-sendReturned:
		t.Fatal("send returned while channel was full and context was live")
	case <-time.After(50 * time.Millisecond):
	}

	// Cancelling the context must unblock the send.
	cancel()
	select {
	case <-sendReturned:
	case <-time.After(time.Second):
		t.Fatal("send did not return after context cancel")
	}
}
