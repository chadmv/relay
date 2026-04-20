package agent

import (
	"context"

	relayv1 "relay/internal/proto/relayv1"
)

// NewRunnerForTest is a test-only wrapper around newRunner.
func NewRunnerForTest(taskID string, sendCh chan *relayv1.AgentMessage, parent context.Context, timeoutSec int32) (*Runner, context.Context) {
	return newRunner(taskID, sendCh, parent, timeoutSec)
}

// RunnerSendForTest exposes Runner.send for tests.
func RunnerSendForTest(r *Runner, msg *relayv1.AgentMessage) {
	r.send(msg)
}
