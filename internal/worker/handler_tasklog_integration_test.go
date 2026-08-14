//go:build integration

package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskLogFrame struct {
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// seedClaimedTask creates a user, job, worker and task, claims the task (which
// bumps assignment_epoch to 1) and returns the ids plus the current epoch. The
// worker id is returned because it is the task's assignee: every append to this
// task must be sent as that worker.
func seedClaimedTask(t *testing.T, ctx context.Context, q *store.Queries, email, hostname string) (jobID, taskID, workerID pgtype.UUID, epoch int32) {
	t.Helper()
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: email, IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: hostname, Hostname: hostname, CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	return job.ID, task.ID, w.ID, claimed.AssignmentEpoch
}

func TestHandleTaskLog_PublishesToATaskScopedSubscriber(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	jobID, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs1@example.com", "w-logs1")
	taskIDStr := h.UUIDStringForTest(taskID)
	jobIDStr := h.UUIDStringForTest(jobID)

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Stream: relayv1.LogStream_LOG_STREAM_STDERR,
		Content: []byte("line one\nline two\n"), Epoch: int64(epoch),
	})

	var e events.Event
	select {
	case e = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no task_log event was published")
	}
	require.Equal(t, events.TypeTaskLog, e.Type)
	require.Equal(t, taskIDStr, e.TaskID)

	var frame taskLogFrame
	require.NoError(t, json.Unmarshal(e.Data, &frame))
	assert.Equal(t, taskIDStr, frame.TaskID)
	assert.Equal(t, jobIDStr, frame.JobID)
	assert.Equal(t, "stderr", frame.Stream)
	assert.Equal(t, "line one\nline two\n", frame.Content)
	assert.NotZero(t, frame.Seq)

	// frame.Seq must be the row the polling endpoint will return - that identity
	// is what makes the client-side backfill join exact.
	page, err := q.GetTaskLogsPage(ctx, store.GetTaskLogsPageParams{TaskID: taskID, ID: 0, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, page[0].ID, frame.Seq)
	assert.Equal(t, page[0].Content, frame.Content)
}

func TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs2@example.com", "w-logs2")
	taskIDStr := h.UUIDStringForTest(taskID)

	// End the assignment and then start a new one, the way production does:
	// RequeueTask returns the task to 'pending' with worker_id NULL and epoch+1,
	// and ClaimTaskForWorker redispatches it with a worker and epoch+1 again. The
	// claimed generation's epoch is now stale and still NON-ZERO, so the fence is
	// exercised against a real previous generation rather than a zero-value epoch.
	//
	// Do NOT simplify this back to CancelJobTasks. Cancel bumps the epoch but
	// leaves worker_id NULL, and the assignee half of the fence correctly rejects
	// every append to an unassigned task - so the positive control below would
	// have no reachable state to write into. (epoch = current, worker_id = NULL)
	// is not a state any real agent can address: every statement that clears
	// worker_id bumps the epoch, and ClaimTaskForWorker is the only statement that
	// sets one, so an epoch an agent holds always arrived with an assignment to
	// that same agent. Requeue-then-redispatch is the reachable equivalent.
	require.NoError(t, q.RequeueTask(ctx, taskID))
	fresh, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: workerID,
	})
	require.NoError(t, err)
	// Pin the delta exactly, not just "greater". Requeue and redispatch bump once
	// each, so the chunk below sits at current-2; without this equality a fence
	// mutated to `assignment_epoch <= $2 + 1` would slip through, which the
	// original single-bump fixture would have caught.
	require.Equal(t, epoch+2, fresh.AssignmentEpoch, "requeue and redispatch must bump the epoch twice")
	require.NotZero(t, epoch, "the stale epoch under test must not be the zero value")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	// A zombie agent still streaming output for the generation that just ended.
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("from zombie\n"), Epoch: int64(epoch),
	})
	// HandleTaskLog is synchronous and Publish delivers into the subscriber's
	// buffer before returning, so a non-blocking receive is exact here - no
	// wall-clock window needed to decide "nothing was published".
	select {
	case e := <-ch:
		t.Fatalf("a stale-epoch chunk must not be published: %s", e.Data)
	default:
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Empty(t, rows, "a stale-epoch chunk must not be stored")

	// The immediately-previous generation (current-1, the requeue that had no
	// assignee) must be rejected too. The chunk above is current-2, so without
	// this leg an off-by-one fence - `assignment_epoch <= $2 + 1` - would pass
	// every assertion in this test.
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("from the requeue generation\n"), Epoch: int64(epoch + 1),
	})
	select {
	case e := <-ch:
		t.Fatalf("a chunk one generation back must not be published: %s", e.Data)
	default:
	}
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Empty(t, rows, "a chunk one generation back must not be stored")

	// Paired positive control on the SAME code path and the same subscriber: at
	// the current epoch the chunk is both stored and published. Without this a
	// broken HandleTaskLog that did nothing at all would pass the assertions above.
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("current\n"), Epoch: int64(fresh.AssignmentEpoch),
	})
	select {
	case e := <-ch:
		require.Contains(t, string(e.Data), "current")
	case <-time.After(5 * time.Second):
		t.Fatal("positive control: a current-epoch chunk was not published")
	}
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestHandleTaskLog_NoSubscriberSkipsMarshalButStillPersists(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs3@example.com", "w-logs3")
	taskIDStr := h.UUIDStringForTest(taskID)

	chunk := func(s string) *relayv1.TaskLogChunk {
		return &relayv1.TaskLogChunk{TaskId: taskIDStr, Content: []byte(s), Epoch: int64(epoch)}
	}

	// NEGATIVE: nobody tailing. A job-scoped subscription must not count either.
	_, cancelJobSub := broker.Subscribe(events.Filter{JobID: "some-other-job"})
	defer cancelJobSub()

	before := worker.TaskLogPublishesForTest()
	for i := 0; i < 3; i++ {
		h.HandleTaskLog(ctx, workerID, chunk("quiet\n"))
	}
	assert.Equal(t, before, worker.TaskLogPublishesForTest(),
		"with no log subscriber, nothing may be marshalled or published")

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 3, "the fast path must never skip the write")

	// POSITIVE CONTROL on the same code path: attach a task-scoped subscriber and
	// the very same call now bumps the counter. This is what stops the negative
	// assertion above from passing vacuously if the probe or ingest ever breaks.
	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()
	before = worker.TaskLogPublishesForTest()
	for i := 0; i < 3; i++ {
		h.HandleTaskLog(ctx, workerID, chunk("watched\n"))
	}
	assert.Equal(t, before+3, worker.TaskLogPublishesForTest())
	for i := 0; i < 3; i++ {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("positive control: only %d of 3 watched chunks were published", i)
		}
	}
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 6)
}

// captureLog redirects the standard logger for the duration of the test and
// returns an accessor for what was written.
func captureLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return buf.String
}

func countLines(s, substr string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// A persist failure that is NOT a stale-epoch drop repeats for every chunk of
// the task: a subprocess writing binary stdout makes Postgres reject each one
// with 'invalid byte sequence for encoding "UTF8": 0x00'. Since handleTaskLog
// runs synchronously on the Connect recv goroutine, logging per chunk would put
// ~32k serialized log writes in front of that worker's status, inventory and
// telemetry ingest for a 1 GB binary stream. The log must be bounded to one line
// per task per assignment epoch.
func TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})
	worker.ResetTaskLogErrLimiterForTest()

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs4@example.com", "w-logs4")
	taskIDStr := h.UUIDStringForTest(taskID)

	// A NUL byte cannot be stored in a Postgres text column, so every one of
	// these chunks fails to persist for the same reason - the realistic shape of
	// a repeating, non-stale error.
	const secret = "SECRET-sentinel"
	bad := func() *relayv1.TaskLogChunk {
		return &relayv1.TaskLogChunk{
			TaskId: taskIDStr, Content: []byte(secret + "\x00"), Epoch: int64(epoch),
		}
	}

	logged := captureLog(t)
	const n = 8
	for i := 0; i < n; i++ {
		h.HandleTaskLog(ctx, workerID, bad())
	}

	const marker = "handleTaskLog AppendTaskLog"
	assert.Equal(t, 1, countLines(logged(), marker),
		"%d failing chunks for one task+epoch must produce exactly one log line", n)

	// The failures were real, not silently skipped: nothing persisted.
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Empty(t, rows, "these chunks must genuinely have failed to persist")

	// Chunk content must never reach the log: it is raw subprocess output and can
	// contain secrets a job's own script echoed. pgconn.PgError.Error() renders
	// only severity + message + SQLSTATE, never Detail (where Postgres puts
	// "Failing row contains (...)"), which is what makes %v on the error safe.
	assert.NotContains(t, logged(), secret, "chunk content must never be logged")

	// A NEW assignment generation is a new failure worth reporting once more, so
	// the bound is per task per epoch rather than once per task forever. The new
	// generation is produced by requeue-then-redispatch, so the task is genuinely
	// assigned to this worker at the new epoch and these chunks reach a matching
	// fence - the failure below is a real persist failure on a live assignment.
	//
	// Do NOT simplify this back to CancelJobTasks. Cancel bumps the epoch but
	// leaves worker_id NULL, so the assignee half of the fence rejects every
	// append and the positive control at the end of this test has nowhere to
	// write. (epoch = current, worker_id = NULL) is a perfectly reachable database
	// state - RequeueTask and CancelJobTasks both produce it - but it is
	// UNADDRESSABLE by a real agent: every statement that clears worker_id bumps
	// the epoch, and only ClaimTaskForWorker sets one, so no agent ever holds a
	// matching epoch for it.
	require.NoError(t, q.RequeueTask(ctx, taskID))
	fresh, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: workerID,
	})
	require.NoError(t, err)
	// Exact delta: requeue and redispatch bump once each, so the chunks below are
	// at current and the stale ones at current-2.
	require.Equal(t, epoch+2, fresh.AssignmentEpoch)
	for i := 0; i < n; i++ {
		h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId: taskIDStr, Content: []byte(secret + "\x00"),
			Epoch: int64(fresh.AssignmentEpoch),
		})
	}
	assert.Equal(t, 2, countLines(logged(), marker),
		"a new assignment epoch must be reported once more")
	// Scope note: this leg pins the LIMITER's key (task + epoch), not the fence.
	// Postgres rejects a NUL byte while decoding the bind parameter, before the
	// fence CTE is evaluated, so a NUL chunk surfaces the same non-ErrNoRows error
	// whether or not the fence would have matched. That is why the fixture above
	// keeps the task genuinely assigned at this epoch: the error is then a real
	// persist failure on a live assignment rather than an artifact. The fence
	// itself is pinned by TestHandleTaskLog_RejectsAChunk* and by
	// TestAppendTaskLog_EpochGuarded.

	// Positive control on the same code path: the stale-epoch path stays silent,
	// and a well-formed chunk at the current epoch still persists. Without this a
	// handleTaskLog that had stopped calling AppendTaskLog at all would pass every
	// assertion above.
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("stale\n"), Epoch: int64(epoch),
	})
	assert.Equal(t, 2, countLines(logged(), marker), "a stale-epoch drop must stay silent")

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("good\n"), Epoch: int64(fresh.AssignmentEpoch),
	})
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a well-formed chunk at the current epoch must still persist")
	assert.Equal(t, "good\n", rows[0].Content)
}

// The epoch fence answers "is this generation current". It never answered "are
// you the worker this task is assigned to", so any agent that could guess a
// task's current epoch could write into it. This test sends the task's CURRENT
// epoch on purpose: the epoch predicate matches, so nothing but an assignee
// predicate can reject the chunk. A stale-epoch variant would be green today and
// therefore vacuous.
func TestHandleTaskLog_RejectsAChunkFromANonAssignee(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, w1, epoch := seedClaimedTask(t, ctx, q, "logs5@example.com", "w-logs5")
	taskIDStr := h.UUIDStringForTest(taskID)

	// A second, entirely legitimate worker that simply is not this task's
	// assignee - the realistic attacker here holds a valid agent token.
	w2row, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-logs5-other", Hostname: "w-logs5-other", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w2 := w2row.ID
	require.NotEqual(t, w1, w2, "the forging worker must be a different row")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, w2, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("forged by a non-assignee\n"), Epoch: int64(epoch),
	})

	// HandleTaskLog is synchronous and Publish delivers into the subscriber's
	// buffer before returning, so a non-blocking receive is exact here - no
	// wall-clock window is needed to decide "nothing was published".
	var published []byte
	select {
	case e := <-ch:
		published = e.Data
	default:
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	// Non-fatal asserts so a RED run reports BOTH halves of the exposure - the
	// forged chunk is stored AND fanned out live - rather than stopping at one.
	assert.Empty(t, rows, "a chunk from a non-assignee must not be stored")
	assert.Nil(t, published, "a chunk from a non-assignee must not be published")
	if t.Failed() {
		t.FailNow() // the forgery got through; the positive control below is moot
	}

	// Positive control on the SAME code path: the real assignee at the same epoch
	// is stored and published. Without it, a handleTaskLog that had stopped
	// ingesting anything at all would pass every assertion above.
	h.HandleTaskLog(ctx, w1, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("genuine\n"), Epoch: int64(epoch),
	})
	select {
	case e := <-ch:
		require.Contains(t, string(e.Data), "genuine")
	case <-time.After(5 * time.Second):
		t.Fatal("positive control: the assignee's own chunk was not published")
	}
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "genuine\n", rows[0].Content)
}

// The literal repro from the backlog item: a never-claimed task sits at
// assignment_epoch 0 with a NULL worker_id, so Epoch 0 is a free guess. Seeded
// by hand rather than through seedClaimedTask because the whole point is a task
// that was never claimed.
func TestHandleTaskLog_RejectsAChunkForANeverClaimedTask(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "logs6@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w1row, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-logs6", Hostname: "w-logs6", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w1 := w1row.ID
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	unclaimed, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, int32(0), unclaimed.AssignmentEpoch, "a never-claimed task must sit at epoch 0")
	require.False(t, unclaimed.WorkerID.Valid, "a never-claimed task must have a NULL worker_id")

	taskIDStr := h.UUIDStringForTest(task.ID)
	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, w1, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("forged at epoch zero\n"), Epoch: 0,
	})

	var published []byte
	select {
	case e := <-ch:
		published = e.Data
	default:
	}
	rows, err := q.GetTaskLogs(ctx, task.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a chunk for a never-claimed task must not be stored")
	assert.Nil(t, published, "a chunk for a never-claimed task must not be published")
	if t.Failed() {
		t.FailNow() // the forgery got through; the positive control below is moot
	}

	// Positive control: once the task really is claimed by w1, that same worker at
	// the new epoch is stored and published. Rejection must be about assignment,
	// not about this worker or this task being inert.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	h.HandleTaskLog(ctx, w1, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("genuine\n"), Epoch: int64(claimed.AssignmentEpoch),
	})
	select {
	case e := <-ch:
		require.Contains(t, string(e.Data), "genuine")
	case <-time.After(5 * time.Second):
		t.Fatal("positive control: the assignee's chunk was not published after the claim")
	}
	rows, err = q.GetTaskLogs(ctx, task.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "genuine\n", rows[0].Content)
}

// Every other test in this file reaches handleTaskLog through the export_test
// shim, passing a worker id the test chose. That leaves the ONE line that wires
// the connection's real identity into it - Connect's
// `h.handleTaskLog(ctx, workerUUID, p.TaskLog)` - pinned by nothing. If that
// call site bound a zero-value UUID, or the wrong variable, every test in this
// package would stay green while production silently dropped 100% of log
// ingest, because the assignee predicate would never match. That is precisely
// the failure mode spec 6.4 calls out as miserable to debug, so it gets a test
// that drives the real Connect message loop end to end.
//
// The register message must report the claimed task in RunningTasks:
// finishRegister runs reconcileRunningTasks, which requeues any task the
// coordinator has assigned to this worker but the agent did not report, and a
// requeue bumps assignment_epoch - which would make the chunk below stale for a
// reason that has nothing to do with what is under test.
func TestConnect_TaskLogChunkIsFencedOnTheConnectionsOwnWorker(t *testing.T) {
	fx := newWorkerTestFixture(t)
	fx.Handler.AllowAutoEnroll = true
	ctx := context.Background()
	q := fx.Q

	const hostname = "w-connect-wiring"
	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs7@example.com", hostname)
	taskIDStr := fx.Handler.UUIDStringForTest(taskID)

	stream := newMockConnectStream(t)
	// Auto-enroll upserts by hostname and returns the EXISTING row's id, so this
	// connection resolves to the very worker the task above is assigned to.
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: hostname,
				CpuCores: 1, RamGb: 1, Os: "linux",
				RunningTasks: []*relayv1.RunningTask{
					{TaskId: taskIDStr, Epoch: int64(epoch)},
				},
			},
		},
	})
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{
				TaskId: taskIDStr, Content: []byte("through the real Connect loop\n"),
				Epoch: int64(epoch),
			},
		},
	})

	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	resp := stream.RecvFromServer(t, 5*time.Second).GetRegisterResponse()
	require.NotNil(t, resp)
	require.Equal(t, fx.Handler.UUIDStringForTest(workerID), resp.WorkerId,
		"the connection must resolve to the task's assignee, or this test proves nothing")

	// The chunk is processed after the register response is sent, so poll rather
	// than racing it.
	require.Eventually(t, func() bool {
		rows, err := q.GetTaskLogs(ctx, taskID)
		return err == nil && len(rows) == 1
	}, 10*time.Second, 20*time.Millisecond,
		"Connect must pass its own authenticated worker id to handleTaskLog")

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "through the real Connect loop\n", rows[0].Content)

	// The task must still be assigned at the same generation: if reconciliation
	// had requeued it, the append above would have been rejected for the wrong
	// reason and this test would be silently testing nothing.
	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, epoch, fresh.AssignmentEpoch, "reconciliation must not have bumped the epoch")
	assert.Equal(t, workerID, fresh.WorkerID, "the task must still be assigned to this worker")

	stream.CloseSend()
	<-done
}

// A terminal transition deliberately keeps worker_id and assignment_epoch so a
// trailing chunk from the agent that just finished still lands (see
// TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist).
// Without a third predicate that is true FOREVER: anything holding this worker's
// agent token can keep appending rows to a task it finished for as long as the
// row exists, and nothing in the repo prunes task_logs.
//
// This test sends the CORRECT epoch as the GENUINE assignee on purpose. The
// other two fence predicates match, so only a time bound can reject this chunk -
// a wrong-worker or stale-epoch variant would be green today and therefore
// vacuous.
func TestHandleTaskLog_RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs-win1@example.com", "w-logs-win1")
	taskIDStr := h.UUIDStringForTest(taskID)

	// Finish it an hour ago, on THIS process's clock. Production writes
	// finished_at from the same clock (handleTaskStatus), and the cutoff is
	// computed from it too, so this test never straddles two clocks. Do NOT
	// rewrite this as NOW() - interval '1 hour': that would compare the container
	// clock against the Go clock and reintroduce exactly the skew the design
	// eliminates.
	_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "done", WorkerID: workerID, AssignmentEpoch: epoch,
		FinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err, "fixture: the terminal transition must land")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("long after the task finished\n"), Epoch: int64(epoch),
	})

	// HandleTaskLog is synchronous and Publish delivers into the subscriber's
	// buffer before returning, so a non-blocking receive is exact here - no
	// wall-clock window is needed to decide "nothing was published".
	var published []byte
	select {
	case e := <-ch:
		published = e.Data
	default:
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	// Non-fatal asserts so a RED run reports BOTH halves of the exposure - the
	// chunk is stored AND fanned out live - rather than stopping at one. The
	// publish half is not decoration: it is what makes this test able to catch a
	// publish that is not gated on the fence having matched.
	assert.Empty(t, rows, "a chunk for a task that finished outside the window must not be stored")
	assert.Nil(t, published, "a chunk for a task that finished outside the window must not be published")
	if t.Failed() {
		t.FailNow() // the window is open; the rest of the run is moot
	}
}

// The regression control for the trailing flush, at the layer the flush actually
// happens. A chunk arriving just after the terminal status is a real and common
// ordering, and it MUST still be stored and published: that flush is the REASON
// the assignment outlives the task. This test goes red the moment the fence's
// two arms are conjoined instead of disjoined.
func TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs-win2@example.com", "w-logs-win2")
	taskIDStr := h.UUIDStringForTest(taskID)

	_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "done", WorkerID: workerID, AssignmentEpoch: epoch,
		FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err, "fixture: the terminal transition must land")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("trailing after done\n"), Epoch: int64(epoch),
	})

	select {
	case e := <-ch:
		require.Contains(t, string(e.Data), "trailing after done")
	case <-time.After(5 * time.Second):
		t.Fatal("a trailing chunk from the assignee must still be published after a terminal status")
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a trailing chunk from the assignee must still be stored")
	require.Equal(t, "trailing after done\n", rows[0].Content)
}

// The positive control on a LIVE task, and the test that guards the trap this
// predicate creates. A running task has finished_at IS NULL, so it can only pass
// on the status arm of the disjunction - the finished_at arm rejects it (NULL >
// anything is NULL). This is therefore the test that goes red if a non-terminal
// status is ever dropped from that allow-list, which would silently drop 100% of
// that state's log output with no error anywhere.
func TestHandleTaskLog_LiveTaskWithNoFinishedAtIsStillStoredAndPublished(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs-win3@example.com", "w-logs-win3")
	taskIDStr := h.UUIDStringForTest(taskID)

	_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "running", WorkerID: workerID, AssignmentEpoch: epoch,
		StartedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err, "fixture: the running transition must land")
	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.False(t, fresh.FinishedAt.Valid,
		"fixture: a live task must have a NULL finished_at, or this test proves nothing about the status arm")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("from a live task\n"), Epoch: int64(epoch),
	})

	select {
	case e := <-ch:
		require.Contains(t, string(e.Data), "from a live task")
	case <-time.After(5 * time.Second):
		t.Fatal("a chunk from a live task must be published")
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a chunk from a live task must be stored")
	require.Equal(t, "from a live task\n", rows[0].Content)
}
