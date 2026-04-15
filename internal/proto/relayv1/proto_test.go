package relayv1_test

import (
	"testing"

	relayv1 "relay/internal/proto/relayv1"
)

// TestProtoCompiles verifies the generated proto package compiles correctly.
func TestProtoCompiles(t *testing.T) {
	_ = &relayv1.AgentMessage{}
	_ = &relayv1.CoordinatorMessage{}
	_ = &relayv1.RegisterRequest{}
	_ = &relayv1.DispatchTask{}
	_ = &relayv1.TaskLogChunk{}
	_ = &relayv1.TaskStatusUpdate{}
}
