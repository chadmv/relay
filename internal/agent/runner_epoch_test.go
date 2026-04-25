package agent

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
)

func TestRunnerTagsOutgoingMessagesWithEpoch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendCh := make(chan *relayv1.AgentMessage, 8)
	runner, runCtx := newRunner("task-123", 42, sendCh, ctx, 0)

	go runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "task-123",
		Commands: singleCmd(echoCmd()), // cross-platform: "echo hello" on Unix, "cmd /c echo hello" on Windows
	})

	// Collect all messages until channel drains.
	var msgs []*relayv1.AgentMessage
	for i := 0; i < 2; i++ {
		select {
		case m := <-sendCh:
			msgs = append(msgs, m)
		case <-ctx.Done():
			t.Fatal("timed out waiting for messages")
		}
	}

	// Every outgoing TaskStatusUpdate must carry epoch=42.
	for _, m := range msgs {
		if ts := m.GetTaskStatus(); ts != nil {
			assert.Equal(t, int64(42), ts.Epoch)
		}
		if tl := m.GetTaskLog(); tl != nil {
			assert.Equal(t, int64(42), tl.Epoch)
		}
	}
}

func TestRunnerAbandon_SuppressesFinalStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendCh := make(chan *relayv1.AgentMessage, 8)
	runner, runCtx := newRunner("task-abandon", 1, sendCh, ctx, 0)

	// Start a long-running subprocess that would normally report DONE.
	done := make(chan struct{})
	go func() {
		runner.Run(runCtx, &relayv1.DispatchTask{
			TaskId: "task-abandon", Commands: singleCmd(sleepCmd()),
		})
		close(done)
	}()

	// Wait for RUNNING status, then abandon.
	select {
	case <-sendCh: // RUNNING message
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for RUNNING status")
	}
	runner.Abandon()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not exit after Abandon")
	}

	// Drain remaining messages on sendCh.
	var sawFinal bool
	for {
		select {
		case m := <-sendCh:
			if ts := m.GetTaskStatus(); ts != nil {
				switch ts.Status {
				case relayv1.TaskStatus_TASK_STATUS_DONE,
					relayv1.TaskStatus_TASK_STATUS_FAILED,
					relayv1.TaskStatus_TASK_STATUS_TIMED_OUT:
					sawFinal = true
				}
			}
		default:
			goto check
		}
	}
check:
	assert.False(t, sawFinal, "Abandon must suppress final status message")
}
