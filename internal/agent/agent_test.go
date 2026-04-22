//go:build integration

package agent_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/agent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// echoTaskCmd returns a cross-platform command that prints a message to stdout.
func echoTaskCmd(msg string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", msg}
	}
	return []string{"echo", msg}
}

// fakeCoord is a minimal in-process coordinator for testing.
type fakeCoord struct {
	relayv1.UnimplementedAgentServiceServer
	received chan *relayv1.AgentMessage
	dispatch chan *relayv1.CoordinatorMessage // messages to send to agent after registration
}

func newFakeCoord() *fakeCoord {
	return &fakeCoord{
		received: make(chan *relayv1.AgentMessage, 64),
		dispatch: make(chan *relayv1.CoordinatorMessage, 8),
	}
}

func (f *fakeCoord) Connect(stream grpc.BidiStreamingServer[relayv1.AgentMessage, relayv1.CoordinatorMessage]) error {
	// First message must be RegisterRequest.
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	f.received <- msg

	// Always respond with RegisterResponse.
	if err := stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{WorkerId: "test-worker-id"},
		},
	}); err != nil {
		return err
	}

	// A single goroutine drives stream.Recv() to avoid concurrent calls.
	msgCh := make(chan *relayv1.AgentMessage, 64)
	errCh := make(chan error, 1)
	go func() {
		for {
			m, e := stream.Recv()
			if e != nil {
				errCh <- e
				return
			}
			msgCh <- m
		}
	}()

	// Forward any queued dispatch messages and relay subsequent agent messages.
	for {
		select {
		case m := <-msgCh:
			f.received <- m
		case e := <-errCh:
			return e
		case toSend := <-f.dispatch:
			if err := stream.Send(toSend); err != nil {
				return err
			}
		case <-time.After(5 * time.Second):
			return nil
		}
	}
}

func startFakeCoord(t *testing.T) (*fakeCoord, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	coord := newFakeCoord()
	srv := grpc.NewServer()
	relayv1.RegisterAgentServiceServer(srv, coord)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	return coord, lis.Addr().String()
}

func TestAgent_registers(t *testing.T) {
	coord, addr := startFakeCoord(t)

	caps := agent.Capabilities{
		Hostname: "test-host",
		OS:       "linux",
		CPUCores: 8,
		RAMGB:    32,
		GPUCount: 1,
		GPUModel: "NVIDIA RTX 4090",
	}

	var savedID string
	a := agent.NewAgent(addr, caps, "", func(id string) error {
		savedID = id
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go a.Run(ctx)

	// Wait for the RegisterRequest to arrive.
	select {
	case msg := <-coord.received:
		reg := msg.GetRegister()
		require.NotNil(t, reg)
		assert.Equal(t, "test-host", reg.Hostname)
		assert.Equal(t, "linux", reg.Os)
		assert.Equal(t, int32(8), reg.CpuCores)
		assert.Equal(t, int32(32), reg.RamGb)
		assert.Equal(t, int32(1), reg.GpuCount)
		assert.Equal(t, "NVIDIA RTX 4090", reg.GpuModel)
	case <-ctx.Done():
		t.Fatal("timed out waiting for RegisterRequest")
	}

	// Give agent time to process the RegisterResponse and save the worker ID.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, "test-worker-id", savedID)
}

func TestAgent_dispatchAndReceiveLogs(t *testing.T) {
	coord, addr := startFakeCoord(t)

	caps := agent.Capabilities{Hostname: "test-host", OS: "linux", CPUCores: 4, RAMGB: 8}
	a := agent.NewAgent(addr, caps, "", func(string) error { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go a.Run(ctx)

	// Wait for registration.
	select {
	case <-coord.received:
	case <-ctx.Done():
		t.Fatal("timed out waiting for registration")
	}
	time.Sleep(100 * time.Millisecond)

	// Dispatch a task.
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	coord.dispatch <- &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{
				TaskId:  "task-abc",
				JobId:   "job-xyz",
				Command: echoTaskCmd("hello-from-task"),
			},
		},
	}

	// Collect messages until we see DONE.
	deadline := time.After(4 * time.Second)
	var seenRunning, seenDone bool
	for !seenDone {
		select {
		case msg := <-coord.received:
			if s := msg.GetTaskStatus(); s != nil {
				switch s.Status {
				case relayv1.TaskStatus_TASK_STATUS_RUNNING:
					seenRunning = true
				case relayv1.TaskStatus_TASK_STATUS_DONE:
					seenDone = true
				}
			}
		case <-deadline:
			t.Fatalf("timed out; seenRunning=%v seenDone=%v", seenRunning, seenDone)
		}
	}
	assert.True(t, seenRunning)
}

func TestAgent_reconnects(t *testing.T) {
	// First coordinator: closes stream immediately after registration.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()

	var connectCount atomic.Int32

	closeFirst := &closeAfterRegCoord{connectCount: &connectCount}
	srv := grpc.NewServer()
	relayv1.RegisterAgentServiceServer(srv, closeFirst)
	go func() { _ = srv.Serve(lis) }()
	defer srv.GracefulStop()

	caps := agent.Capabilities{Hostname: "reconnect-test", OS: "linux", CPUCores: 2, RAMGB: 4}
	a := agent.NewAgent(addr, caps, "", func(string) error { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go a.Run(ctx)

	// Agent should reconnect at least twice (initial + 1 retry).
	require.Eventually(t, func() bool {
		return connectCount.Load() >= 2
	}, 5*time.Second, 100*time.Millisecond)
}

type closeAfterRegCoord struct {
	relayv1.UnimplementedAgentServiceServer
	connectCount *atomic.Int32
}

func (c *closeAfterRegCoord) Connect(stream grpc.BidiStreamingServer[relayv1.AgentMessage, relayv1.CoordinatorMessage]) error {
	c.connectCount.Add(1)
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	if msg.GetRegister() == nil {
		return fmt.Errorf("expected RegisterRequest")
	}
	// Send RegisterResponse then close.
	_ = stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{WorkerId: "test-id"},
		},
	})
	return nil // close stream
}

