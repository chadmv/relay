package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Agent manages the gRPC connection to the coordinator, dispatches tasks to
// Runners, and reconnects automatically on stream failure.
type Agent struct {
	coord    string
	caps     Capabilities
	workerID string // only accessed from the single reconnect goroutine in Run; no mutex needed
	sendCh   chan *relayv1.AgentMessage // buffered 64; shared across reconnects
	mu       sync.Mutex
	runners  map[string]*Runner
	runnerWG sync.WaitGroup // tracks active runner goroutines; waited before sendCh drain
	saveID   func(string) error
}

// NewAgent constructs an Agent. workerID is empty on first run; saveID persists
// the coordinator-assigned ID to the state file on every change.
func NewAgent(coord string, caps Capabilities, workerID string, saveID func(string) error) *Agent {
	return &Agent{
		coord:    coord,
		caps:     caps,
		workerID: workerID,
		sendCh:   make(chan *relayv1.AgentMessage, 64),
		runners:  make(map[string]*Runner),
		saveID:   saveID,
	}
}

// Run connects to the coordinator and reconnects with exponential backoff until
// ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := a.connect(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		backoff = time.Second
	}
}

// connect dials the coordinator, registers, and runs the recv loop until the
// stream closes or ctx is cancelled.
func (a *Agent) connect(ctx context.Context) error {
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	// Wait for all runner goroutines from the previous connection to finish before
	// draining sendCh. Runners were cancelled when the previous stream closed;
	// waiting here ensures no runner is still writing to sendCh during the drain.
	a.runnerWG.Wait()

	// Discard any messages queued from the previous connection. They were never
	// sent on the old stream and sending them on a new stream would confuse the
	// coordinator with stale task IDs.
	for len(a.sendCh) > 0 {
		<-a.sendCh
	}

	conn, err := grpc.NewClient(a.coord, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := relayv1.NewAgentServiceClient(conn)
	stream, err := client.Connect(connCtx)
	if err != nil {
		return err
	}

	// Send RegisterRequest.
	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				WorkerId: a.workerID,
				Hostname: a.caps.Hostname,
				CpuCores: a.caps.CPUCores,
				RamGb:    a.caps.RAMGB,
				GpuCount: a.caps.GPUCount,
				GpuModel: a.caps.GPUModel,
				Os:       a.caps.OS,
			},
		},
	}); err != nil {
		return err
	}

	// First response must be RegisterResponse.
	resp, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := resp.GetRegisterResponse()
	if reg == nil {
		return fmt.Errorf("agent: expected RegisterResponse, got %T", resp.Payload)
	}
	if reg.WorkerId != a.workerID {
		a.workerID = reg.WorkerId
		if err := a.saveID(a.workerID); err != nil {
			fmt.Fprintf(os.Stderr, "relay-agent: warning: failed to persist worker ID: %v\n", err)
		}
	}

	log.Printf("connected to coordinator %s (worker ID: %s)", a.coord, a.workerID)

	// Start send goroutine — gRPC streams are not concurrent-send-safe.
	go func() {
		for {
			select {
			case msg := <-a.sendCh:
				if err := stream.Send(msg); err != nil {
					connCancel()
					return
				}
			case <-connCtx.Done():
				return
			}
		}
	}()

	// Recv loop.
	for {
		msg, err := stream.Recv()
		if err != nil {
			// Cancel all runners for this connection.
			a.mu.Lock()
			for _, r := range a.runners {
				r.Cancel()
			}
			a.mu.Unlock()
			connCancel()
			return err
		}

		switch p := msg.Payload.(type) {
		case *relayv1.CoordinatorMessage_DispatchTask:
			a.handleDispatch(connCtx, p.DispatchTask)
		case *relayv1.CoordinatorMessage_CancelTask:
			a.handleCancel(p.CancelTask)
		}
	}
}

func (a *Agent) handleDispatch(ctx context.Context, task *relayv1.DispatchTask) {
	runner, runCtx := newRunner(task.TaskId, a.sendCh, ctx, task.TimeoutSeconds)
	a.mu.Lock()
	a.runners[task.TaskId] = runner
	a.mu.Unlock()
	a.runnerWG.Add(1)
	go func() {
		defer a.runnerWG.Done()
		runner.Run(runCtx, task)
		a.mu.Lock()
		delete(a.runners, task.TaskId)
		a.mu.Unlock()
	}()
}

func (a *Agent) handleCancel(msg *relayv1.CancelTask) {
	a.mu.Lock()
	r, ok := a.runners[msg.TaskId]
	a.mu.Unlock()
	if ok {
		r.Cancel()
	}
}
