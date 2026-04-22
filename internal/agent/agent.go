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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Agent manages the gRPC connection to the coordinator, dispatches tasks to
// Runners, and reconnects automatically on stream failure.
type Agent struct {
	coord    string
	caps     Capabilities
	workerID string // only accessed from the single reconnect goroutine in Run; no mutex needed
	sendCh   chan *relayv1.AgentMessage // buffered 64; shared across reconnects
	runCtx   context.Context            // long-lived parent; set in Run, lives across reconnects
	mu       sync.Mutex
	runners  map[string]*Runner
	runnerWG sync.WaitGroup // tracks active runner goroutines; waited on agent shutdown
	saveID   func(string) error
	creds    *Credentials
}

// NewAgent constructs an Agent. workerID is empty on first run; saveID persists
// the coordinator-assigned ID to the state file on every change.
func NewAgent(coord string, caps Capabilities, workerID string, creds *Credentials, saveID func(string) error) *Agent {
	return &Agent{
		coord:    coord,
		caps:     caps,
		workerID: workerID,
		sendCh:   make(chan *relayv1.AgentMessage, 64),
		runners:  make(map[string]*Runner),
		saveID:   saveID,
		creds:    creds,
	}
}

// Run connects to the coordinator and reconnects with exponential backoff until
// ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	a.runCtx = ctx
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			a.runnerWG.Wait()
			return
		}
		if err := a.connect(ctx); err != nil {
			if ctx.Err() != nil {
				a.runnerWG.Wait()
				return
			}
			if status.Code(err) == codes.Unauthenticated {
				log.Printf("agent: authentication failed — token may have been revoked; exiting")
				a.runnerWG.Wait()
				return
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				a.runnerWG.Wait()
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
	regReq, err := a.buildRegisterRequest()
	if err != nil {
		return fmt.Errorf("build register: %w", err)
	}
	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{Register: regReq},
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
	if reg.AgentToken != "" {
		if err := a.creds.Persist(reg.AgentToken); err != nil {
			return fmt.Errorf("persist agent token: %w", err)
		}
		log.Printf("agent token persisted to %s", a.creds.TokenFilePath())
	}
	if reg.WorkerId != a.workerID {
		a.workerID = reg.WorkerId
		if err := a.saveID(a.workerID); err != nil {
			fmt.Fprintf(os.Stderr, "relay-agent: warning: failed to persist worker ID: %v\n", err)
		}
	}

	// Coordinator may tell us to abandon some tasks (reassigned during grace
	// expiry, or unknown to the coordinator). Abandon them silently.
	for _, tid := range reg.CancelTaskIds {
		a.mu.Lock()
		r, ok := a.runners[tid]
		a.mu.Unlock()
		if ok {
			r.Abandon()
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
			// Stream dropped. Runners survive (they bind to runCtx, not connCtx).
			// Coordinator will start a grace timer; reconnect will reconcile.
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

func (a *Agent) handleDispatch(connCtx context.Context, task *relayv1.DispatchTask) {
	// Runners bind to the long-lived runCtx, NOT the connection ctx. This is
	// what lets subprocesses survive brief disconnects.
	runner, runCtx := newRunner(task.TaskId, task.Epoch, a.sendCh, a.runCtx, task.TimeoutSeconds)
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
	_ = connCtx // connCtx is no longer used by the runner
}

func (a *Agent) handleCancel(msg *relayv1.CancelTask) {
	a.mu.Lock()
	r, ok := a.runners[msg.TaskId]
	a.mu.Unlock()
	if ok {
		r.Cancel()
	}
}

// buildRegisterRequest constructs the RegisterRequest sent on (re)connect.
// Includes the caller's capabilities AND the list of currently-executing
// tasks with their epochs, so the coordinator can reconcile.
func (a *Agent) buildRegisterRequest() (*relayv1.RegisterRequest, error) {
	a.mu.Lock()
	running := make([]*relayv1.RunningTask, 0, len(a.runners))
	for _, r := range a.runners {
		running = append(running, &relayv1.RunningTask{
			TaskId: r.taskID,
			Epoch:  r.epoch,
		})
	}
	a.mu.Unlock()

	req := &relayv1.RegisterRequest{
		WorkerId:     a.workerID,
		Hostname:     a.caps.Hostname,
		CpuCores:     a.caps.CPUCores,
		RamGb:        a.caps.RAMGB,
		GpuCount:     a.caps.GPUCount,
		GpuModel:     a.caps.GPUModel,
		Os:           a.caps.OS,
		RunningTasks: running,
	}

	switch {
	case a.creds.HasAgentToken():
		req.Credential = &relayv1.RegisterRequest_AgentToken{AgentToken: a.creds.AgentToken()}
	case a.creds.EnrollmentToken() != "":
		req.Credential = &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: a.creds.EnrollmentToken()}
	default:
		return nil, fmt.Errorf("no credentials: set RELAY_AGENT_ENROLLMENT_TOKEN or provision the agent token file")
	}
	return req, nil
}
