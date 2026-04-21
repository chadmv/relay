package agent

import (
	"context"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
)

func TestRunnerTagsOutgoingMessagesWithEpoch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendCh := make(chan *relayv1.AgentMessage, 8)
	runner, runCtx := newRunner("task-123", 42, sendCh, ctx, 0)

	go runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId:  "task-123",
		Command: []string{"true"},
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
