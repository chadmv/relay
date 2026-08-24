//go:build integration

package worker_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// sendFailStream is handler_test.go's fakeStream with one difference: Send
// fails. It is a separate type rather than a flag on fakeStream so that every
// existing test file in this package stays byte-identical.
type sendFailStream struct {
	msgs []*relayv1.AgentMessage
	pos  int
	ctx  context.Context
}

func (s *sendFailStream) Recv() (*relayv1.AgentMessage, error) {
	if s.pos >= len(s.msgs) {
		return nil, io.EOF
	}
	m := s.msgs[s.pos]
	s.pos++
	return m, nil
}

// Send is what a peer that vanished between RegisterWorkerConnection and the
// RegisterResponse looks like from the server's side. It is the plausible half
// of this bug: the reconcile arm needs the same peer to vanish a few lines
// earlier (its ctx is stream.Context()), this one needs nothing but a closed
// socket.
func (s *sendFailStream) Send(*relayv1.CoordinatorMessage) error {
	return errors.New("rpc error: code = Unavailable desc = transport is closing")
}

func (s *sendFailStream) Context() context.Context     { return s.ctx }
func (s *sendFailStream) RecvMsg(any) error            { return io.EOF }
func (s *sendFailStream) SendMsg(any) error            { return nil }
func (s *sendFailStream) SetHeader(metadata.MD) error  { return nil }
func (s *sendFailStream) SendHeader(metadata.MD) error { return nil }
func (s *sendFailStream) SetTrailer(metadata.MD)       {}

// TestRegisterWorker_SendFailureReleasesTheGeneration is the second of the two
// arms the strand can take, and the one this lane exists for: it needs a real
// pool, because applyInventory opens a transaction on *pgxpool.Pool
// unconditionally between the reconcile and the send, so a pool-less fixture
// cannot reach stream.Send at all.
//
// It carries what the default-lane proof cannot: the actual worker ROW and the
// actual TASK, through a real grace timer, to a real requeue.
func TestRegisterWorker_SendFailureReleasesTheGeneration(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()

	// onExpire wired exactly as cmd/relay-server/main.go wires it, so what this
	// test proves is what production does.
	grace := worker.NewGraceRegistry(100*time.Millisecond, func(workerID string, epoch int32) {
		var id pgtype.UUID
		if err := id.Scan(workerID); err != nil {
			return
		}
		_, _ = q.RequeueWorkerTasksIfEpoch(context.Background(), store.RequeueWorkerTasksIfEpochParams{
			WorkerID: id, ConnectionEpoch: epoch,
		})
	})
	defer grace.Stop()

	h := worker.NewHandlerWithGrace(q, pool, registry, broker, func() {}, grace)

	wkID, rawToken := seedWorkerWithAgentToken(t, ctx, q, "strand-01")

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "strand-user", Email: "strand-user@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "strand-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "strand-task",
		Commands: []byte(`[["echo","hi"]]`), Env: []byte("{}"), Requires: []byte("[]"), Retries: 0,
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: wkID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, "dispatched", claimed.Status)

	// THE AGENT MUST REPORT THE RUNNING TASK. reconcileRunningTasks requeues any
	// task the coordinator has assigned that the agent did NOT report, so an
	// empty RunningTasks makes the task go pending before the send is even
	// attempted - and this test would then pass at HEAD for entirely the wrong
	// reason. Reporting it at the matching epoch makes reconcile a no-op and
	// leaves the requeue to be caused by the fix, or by nothing.
	stream := &sendFailStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{{Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname:   "strand-01",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
				RunningTasks: []*relayv1.RunningTask{{
					TaskId: h.UUIDStringForTest(claimed.ID),
					Epoch:  int64(claimed.AssignmentEpoch),
				}},
			},
		}}},
	}

	err = h.Connect(stream)
	require.Error(t, err)
	require.Contains(t, err.Error(), "send register response",
		"fixture: the registration must fail on the RegisterResponse send, i.e. AFTER "+
			"RegisterWorkerConnection and grace.Cancel and AFTER a reconcile that moved nothing")

	wAfter, err := q.GetWorker(ctx, wkID)
	require.NoError(t, err)
	assert.Equal(t, "offline", wAfter.Status,
		"a registration that failed on the RegisterResponse send must not leave the worker 'online'. "+
			"GET /v1/workers is where an operator reads this, and at HEAD it says 'online' for as "+
			"long as the process lives - the liveness sweeper cannot correct it, because "+
			"Metrics.Activate is never reached on this path.")
	assert.True(t, wAfter.DisconnectedAt.Valid,
		"disconnected_at is what a server restart reads to decide how much grace remains "+
			"(seedGraceTimersFromActiveTasks); RegisterWorkerConnection cleared it a moment ago and "+
			"the release has to put it back")
	assert.Equal(t, int32(1), wAfter.ConnectionEpoch,
		"marking offline must NOT bump the epoch: the grace timer armed alongside it is fenced on "+
			"exactly this value, and bumping here would make its requeue a silent no-op")

	require.Eventually(t, func() bool {
		after, err := q.GetTask(ctx, task.ID)
		return err == nil && after.Status == "pending"
	}, 5*time.Second, 50*time.Millisecond,
		"the grace timer grace.Cancel discarded must be re-armed at the new epoch, or this task is "+
			"stranded on a connection that does not exist until the 24h stale-task watchdog marks it "+
			"timed_out - which fails the work rather than re-running it")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.False(t, after.WorkerID.Valid, "a requeued task must be unassigned")
	assert.Equal(t, int32(2), after.AssignmentEpoch,
		"the requeue must bump assignment_epoch: returning a task to pending without ending its "+
			"generation is the epoch-fence invariant's own named counter-example")
}
