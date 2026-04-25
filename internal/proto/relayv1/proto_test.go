package relayv1_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

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
	_ = relayv1.LogStream_LOG_STREAM_STDOUT
}

func TestSourceSpecAndInventoryMessages(t *testing.T) {
	// Roundtrip: serialize a DispatchTask with a PerforceSource, deserialize, compare.
	src := &relayv1.SourceSpec{
		Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{
				Stream: "//streams/X/main",
				Sync: []*relayv1.SyncEntry{
					{Path: "//streams/X/main/...", Rev: "#head"},
				},
				Unshelves:          []int64{12346},
				WorkspaceExclusive: true,
			},
		},
	}
	task := &relayv1.DispatchTask{TaskId: "t1", JobId: "j1", Source: src}
	b, err := proto.Marshal(task)
	require.NoError(t, err)
	var got relayv1.DispatchTask
	require.NoError(t, proto.Unmarshal(b, &got))
	require.Equal(t, "//streams/X/main", got.Source.GetPerforce().Stream)
	require.True(t, got.Source.GetPerforce().WorkspaceExclusive)

	// New TaskStatus values exist
	_ = relayv1.TaskStatus_TASK_STATUS_PREPARING
	_ = relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED

	// New LogStream value exists
	_ = relayv1.LogStream_LOG_STREAM_PREPARE

	// AgentMessage carries WorkspaceInventoryUpdate
	inv := &relayv1.WorkspaceInventoryUpdate{
		SourceType: "perforce", SourceKey: "//streams/X/main",
		ShortId: "abcdef", BaselineHash: "deadbeef",
	}
	msg := &relayv1.AgentMessage{Payload: &relayv1.AgentMessage_WorkspaceInventory{WorkspaceInventory: inv}}
	b, err = proto.Marshal(msg)
	require.NoError(t, err)
	var gotMsg relayv1.AgentMessage
	require.NoError(t, proto.Unmarshal(b, &gotMsg))
	require.Equal(t, "abcdef", gotMsg.GetWorkspaceInventory().ShortId)

	// CoordinatorMessage carries EvictWorkspaceCommand
	cmd := &relayv1.EvictWorkspaceCommand{SourceType: "perforce", ShortId: "abcdef"}
	cm := &relayv1.CoordinatorMessage{Payload: &relayv1.CoordinatorMessage_EvictWorkspace{EvictWorkspace: cmd}}
	b, err = proto.Marshal(cm)
	require.NoError(t, err)
	var gotCm relayv1.CoordinatorMessage
	require.NoError(t, proto.Unmarshal(b, &gotCm))
	require.Equal(t, "abcdef", gotCm.GetEvictWorkspace().ShortId)
}
