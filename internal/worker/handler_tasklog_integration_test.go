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
// bumps assignment_epoch to 1) and returns the ids plus the current epoch.
func seedClaimedTask(t *testing.T, ctx context.Context, q *store.Queries, email, hostname string) (jobID, taskID pgtype.UUID, epoch int32) {
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
	return job.ID, task.ID, claimed.AssignmentEpoch
}

func TestHandleTaskLog_PublishesToATaskScopedSubscriber(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	jobID, taskID, epoch := seedClaimedTask(t, ctx, q, "logs1@example.com", "w-logs1")
	taskIDStr := h.UUIDStringForTest(taskID)
	jobIDStr := h.UUIDStringForTest(jobID)

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
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

	jobID, taskID, epoch := seedClaimedTask(t, ctx, q, "logs2@example.com", "w-logs2")
	taskIDStr := h.UUIDStringForTest(taskID)

	// End the assignment the way production does: cancelling the job bumps
	// assignment_epoch (CancelJobTasks). The claimed generation's epoch is now
	// stale and still NON-ZERO, so the fence is exercised against a real previous
	// generation rather than a zero-value epoch.
	require.NoError(t, q.CancelJobTasks(ctx, jobID))
	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Greater(t, fresh.AssignmentEpoch, epoch, "cancel must bump the epoch")
	require.NotZero(t, epoch, "the stale epoch under test must not be the zero value")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	// A zombie agent still streaming output for the generation that just ended.
	h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
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

	// Paired positive control on the SAME code path and the same subscriber: at
	// the current epoch the chunk is both stored and published. Without this a
	// broken HandleTaskLog that did nothing at all would pass the assertions above.
	h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
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

	_, taskID, epoch := seedClaimedTask(t, ctx, q, "logs3@example.com", "w-logs3")
	taskIDStr := h.UUIDStringForTest(taskID)

	chunk := func(s string) *relayv1.TaskLogChunk {
		return &relayv1.TaskLogChunk{TaskId: taskIDStr, Content: []byte(s), Epoch: int64(epoch)}
	}

	// NEGATIVE: nobody tailing. A job-scoped subscription must not count either.
	_, cancelJobSub := broker.Subscribe(events.Filter{JobID: "some-other-job"})
	defer cancelJobSub()

	before := worker.TaskLogPublishesForTest()
	for i := 0; i < 3; i++ {
		h.HandleTaskLog(ctx, chunk("quiet\n"))
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
		h.HandleTaskLog(ctx, chunk("watched\n"))
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

	jobID, taskID, epoch := seedClaimedTask(t, ctx, q, "logs4@example.com", "w-logs4")
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
		h.HandleTaskLog(ctx, bad())
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
	// the bound is per task per epoch rather than once per task forever.
	require.NoError(t, q.CancelJobTasks(ctx, jobID))
	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Greater(t, fresh.AssignmentEpoch, epoch)
	for i := 0; i < n; i++ {
		h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
			TaskId: taskIDStr, Content: []byte(secret + "\x00"),
			Epoch: int64(fresh.AssignmentEpoch),
		})
	}
	assert.Equal(t, 2, countLines(logged(), marker),
		"a new assignment epoch must be reported once more")

	// Positive control on the same code path: the stale-epoch path stays silent,
	// and a well-formed chunk at the current epoch still persists. Without this a
	// handleTaskLog that had stopped calling AppendTaskLog at all would pass every
	// assertion above.
	h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("stale\n"), Epoch: int64(epoch),
	})
	assert.Equal(t, 2, countLines(logged(), marker), "a stale-epoch drop must stay silent")

	h.HandleTaskLog(ctx, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("good\n"), Epoch: int64(fresh.AssignmentEpoch),
	})
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a well-formed chunk at the current epoch must still persist")
	assert.Equal(t, "good\n", rows[0].Content)
}
