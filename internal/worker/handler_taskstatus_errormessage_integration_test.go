//go:build integration

package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three storability properties the coordinator must give an agent-supplied
// message before it can become a task_logs row, asserted at the layer that
// produces it. They are also exercised through the handler below, but a Postgres
// Bind failure is indistinguishable at the handler from any other dropped write,
// so only a test at this layer can say WHICH property broke.
func TestSanitizeAgentErrorMessage_BoundsAndValidity(t *testing.T) {
	t.Run("a short ascii message is unchanged", func(t *testing.T) {
		require.Equal(t, "boom", worker.SanitizeAgentErrorMessageForTest("boom"))
	})

	t.Run("a NUL byte is removed", func(t *testing.T) {
		got := worker.SanitizeAgentErrorMessageForTest("boom\x00boom")
		require.Equal(t, "boomboom", got)
		require.NotContains(t, got, "\x00")
	})

	t.Run("an oversized message is cut at the bound and stays valid UTF-8", func(t *testing.T) {
		// A three-byte rune, so the bound does NOT fall on a rune boundary: with
		// MaxAgentErrorMessageBytes not a multiple of 3, a naive msg[:N] cut lands
		// mid-rune and produces invalid UTF-8. That is the discriminating property;
		// a one-byte input, or a two-byte rune with an even bound, is green under
		// the naive cut and proves nothing. The rune is written as an escape
		// deliberately - a raw non-ASCII byte in this file is unverifiable by eye.
		in := strings.Repeat("\u20ac", worker.MaxAgentErrorMessageBytes)
		got := worker.SanitizeAgentErrorMessageForTest(in)
		assert.True(t, utf8.ValidString(got), "the truncated message must be valid UTF-8")
		assert.LessOrEqual(t, len(got), worker.MaxAgentErrorMessageBytes)
		assert.Greater(t, len(got), worker.MaxAgentErrorMessageBytes-4,
			"the cut must be AT the bound, not far below it")
		assert.True(t, strings.HasPrefix(in, got), "truncation must keep a prefix of the input")
	})

	t.Run("invalid UTF-8 on the wire is made valid", func(t *testing.T) {
		got := worker.SanitizeAgentErrorMessageForTest("ok\xff\xfe tail")
		require.True(t, utf8.ValidString(got))
	})
}


// A1 - the whole feature. RED at HEAD: handleTaskStatus maps PREPARE_FAILED onto
// "failed" and never reads ErrorMessage, so a task whose P4 sync failed shows
// `failed` with an empty log and the cause exists only in the agent process's
// own stdout, if anyone kept it.
func TestHandleTaskStatus_APrepareFailureMessageIsStoredAsAStderrLogLine(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg1", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)

	const cause = "p4 sync: out of disk space on this agent's workspace volume"
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId:       h.UUIDStringForTest(taskID),
		Status:       relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: cause,
		Epoch:        int64(claimed.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the prepare failure's cause must be stored as exactly one log line")
	assert.Equal(t, "stderr", rows[0].Stream,
		"the synthesized line lands on stderr so the SPA renders it in the error colour")
	assert.Equal(t, "[failed] "+cause+"\n", rows[0].Content)

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "failed", after.Status, "PREPARE_FAILED is still routed through the failed path")
}

// A2 - the publish ordering. A single subscriber filtered on BOTH the job and
// the task sits in both of the broker's indexes, so it receives both frame types
// on one channel in publish order (TestBroker_JobAndTaskSubscriberReceivesBoth).
// Publish is synchronous under the broker's own mutex and HandleTaskStatus is
// synchronous, so a non-blocking drain after the call is exact - no wall clock.
func TestHandleTaskStatus_TheLogEventIsPublishedBeforeTheStatusEvent(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	jobID, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg2", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	taskIDStr := h.UUIDStringForTest(taskID)

	ch, cancel := broker.Subscribe(events.Filter{JobID: h.UUIDStringForTest(jobID), TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId:       taskIDStr,
		Status:       relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "sync failed",
		Epoch:        int64(claimed.AssignmentEpoch),
	})

	var types []string
	for done := false; !done; {
		select {
		case e := <-ch:
			types = append(types, e.Type)
		default:
			done = true
		}
	}

	logAt := indexOfEventType(types, events.TypeTaskLog)
	statusAt := indexOfEventType(types, "task")
	require.GreaterOrEqual(t, logAt, 0, "the log frame must be published at all, got %v", types)
	require.GreaterOrEqual(t, statusAt, 0, "the status frame must still be published, got %v", types)
	assert.Less(t, logAt, statusAt,
		"a line published after the terminal status frame is one the live view never shows: %v", types)
}

func indexOfEventType(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

// A3 - the identity gate must cover the NEW write too. The rows clause is what
// discriminates; the counters clause is already true at HEAD and is kept as a
// backstop for the decision that this site adds no counted arm (spec 4.5).
func TestHandleTaskStatus_ANonAssigneeCannotWriteAnErrorMessageLine(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, w2 := seedTaskAndTwoWorkers(t, ctx, q, "errmsg3", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	taskIDStr := h.UUIDStringForTest(taskID)

	before := h.TaskStatusFenceRejections()
	h.HandleTaskStatus(ctx, w2, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "forged", Epoch: int64(claimed.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a non-assignee must not be able to write into a task's log")
	assert.Equal(t, before, h.TaskStatusFenceRejections(),
		"a forged report is dropped a round trip before any write, so no counter moves")
	if t.Failed() {
		t.FailNow() // the forgery got through; the positive control below is moot
	}

	// Positive control on the SAME code path, without which a handler that had
	// stopped writing anything at all satisfies every assertion above.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "genuine", Epoch: int64(claimed.AssignmentEpoch),
	})
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "positive control: the assignee's own message must land")
	require.Contains(t, rows[0].Content, "genuine")
}

// A4 - the currency gate. Requeue-then-reclaim is the reachable way to make a
// NON-ZERO epoch stale while leaving the same worker as the assignee, so the
// positive control at the end stays reachable. epoch+1 is reported as well as
// epoch, because an off-by-one fence would admit exactly that one.
func TestHandleTaskStatus_AStaleEpochWritesNoErrorMessageLine(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg4", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	taskIDStr := h.UUIDStringForTest(taskID)

	n, err := q.RequeueTask(ctx, store.RequeueTaskParams{
		ID: taskID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w1,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "fixture: the requeue must have landed")

	fresh, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	require.Equal(t, claimed.AssignmentEpoch+2, fresh.AssignmentEpoch,
		"fixture: the requeue and the reclaim must each have bumped the epoch")

	for _, stale := range []int32{claimed.AssignmentEpoch, claimed.AssignmentEpoch + 1} {
		h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
			TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
			ErrorMessage: "from a dead generation", Epoch: int64(stale),
		})
		rows, err := q.GetTaskLogs(ctx, taskID)
		require.NoError(t, err)
		require.Empty(t, rows, "epoch %d is stale and must write nothing", stale)
	}

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "from the live generation", Epoch: int64(fresh.AssignmentEpoch),
	})
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "positive control: the current generation's message must land")
	require.Contains(t, rows[0].Content, "from the live generation")
}

// A5 - the above-the-retry-branch position. A prepare failure that is going to
// be retried is exactly the case where the operator most needs the cause of this
// attempt recorded, and the retry branch RETURNS after bumping the epoch.
func TestHandleTaskStatus_TheErrorMessageLineSurvivesARequeueingRetry(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg5", 1) // ONE retry
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID), Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "attempt one died", Epoch: int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "pending", after.Status, "fixture: this report must take the RETRY branch")
	require.Equal(t, claimed.AssignmentEpoch+1, after.AssignmentEpoch,
		"fixture: the retry must have bumped the epoch, which is what makes the position load-bearing")

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the cause of the retried attempt must survive the requeue")
	require.Contains(t, rows[0].Content, "attempt one died")
}

// A6 - spec 4.4.1 and 4.4.2 together, exercised THROUGH the database, which the
// sanitiser's own unit test cannot do: without the rune-boundary cut this is a
// Bind failure and no row is written at all. The assertions are against the
// constant rather than a byte count, so widening the bound is a decision and a
// byte-offset cut is a defect.
func TestHandleTaskStatus_AnOversizedErrorMessageIsTruncatedAtARuneBoundary(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg6", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)

	// Three bytes per rune, so the bound does not fall on a rune boundary.
	msg := strings.Repeat("\u20ac", worker.MaxAgentErrorMessageBytes)
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID), Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: msg, Epoch: int64(claimed.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "an oversized message must still produce a stored line, truncated")
	stored := strings.TrimSuffix(strings.TrimPrefix(rows[0].Content, "[failed] "), "\n")
	assert.True(t, utf8.ValidString(rows[0].Content), "the stored content must be valid UTF-8")
	assert.LessOrEqual(t, len(stored), worker.MaxAgentErrorMessageBytes)
	assert.Greater(t, len(stored), worker.MaxAgentErrorMessageBytes-4,
		"the cut must be AT the bound, not far below it")
	assert.True(t, strings.HasPrefix(msg, stored), "truncation must keep a prefix of the input")
}

// A7 - spec 4.4.3. A proto3 string may legally carry a NUL; Postgres TEXT may
// not (SQLSTATE 22021). Without the strip this is a Bind failure, which is not
// pgx.ErrNoRows, so the row is never written and the silent error arm swallows a
// real defect.
func TestHandleTaskStatus_ANulByteInTheErrorMessageIsStripped(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg7", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID), Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "boom\x00boom", Epoch: int64(claimed.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a message carrying a NUL must still produce a stored line")
	assert.Equal(t, "[failed] boomboom\n", rows[0].Content)
	assert.NotContains(t, rows[0].Content, "\x00")
}

// A8 - the != "" condition. An empty message must not become a blank line. The
// positive control uses a SECOND task, because the first is terminal by then and
// its own re-report is the duplicate case A10 covers.
func TestHandleTaskStatus_AnEmptyErrorMessageWritesNoLine(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, quietID, qw1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg8a", 0)
	quiet, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: quietID, WorkerID: qw1})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, qw1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(quietID), Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "", Epoch: int64(quiet.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, quietID)
	require.NoError(t, err)
	assert.Empty(t, rows, "an empty message must not become a blank log line")
	after, err := q.GetTask(ctx, quietID)
	require.NoError(t, err)
	assert.Equal(t, "failed", after.Status, "the report itself is still accepted")
	if t.Failed() {
		t.FailNow()
	}

	_, loudID, lw1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg8b", 0)
	loud, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: loudID, WorkerID: lw1})
	require.NoError(t, err)
	h.HandleTaskStatus(ctx, lw1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(loudID), Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "a real cause", Epoch: int64(loud.AssignmentEpoch),
	})
	rows, err = q.GetTaskLogs(ctx, loudID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "positive control: the same shape WITH a message must land")
}

// A9 - the `terminal` condition (spec 4.3). A RUNNING report leaves status,
// worker_id and assignment_epoch untouched and nothing rate-limits status
// messages, so admitting one would be an unbudgeted insert the assignee can
// repeat forever at one gRPC message per row. The task really does go `running`,
// so the absence is the condition rather than a rejected message.
func TestHandleTaskStatus_ARunningReportWithAMessageWritesNoLine(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg9", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	taskIDStr := h.UUIDStringForTest(taskID)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		ErrorMessage: "chatty", Epoch: int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "running", after.Status, "fixture: the RUNNING report must have been accepted")
	require.Equal(t, claimed.AssignmentEpoch, after.AssignmentEpoch,
		"fixture: a non-terminal report ends no generation, which is the whole reason for the bound")
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a non-terminal report must not be able to write a log line")
	if t.Failed() {
		t.FailNow()
	}

	// Positive control on the SAME task: a terminal report with a message lands.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: "and now it really died", Epoch: int64(claimed.AssignmentEpoch),
	})
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "positive control: a terminal report on the same task must land")
	require.Contains(t, rows[0].Content, "and now it really died")
}

// A10 - all four clauses of spec 4.5 at once: a fence-refused append stores
// nothing, publishes nothing, logs nothing and moves no task_log_fence counter.
//
// The rejection is driven by an already-terminal row whose finished_at is
// outside the trailing window, stamped on THIS process's clock - never
// NOW() - interval, which would compare the container clock against the Go one.
// A terminal transition bumps neither the epoch nor worker_id, so both Go gates
// still pass and control genuinely reaches the write.
func TestHandleTaskStatus_AFenceRefusedErrorMessageIsSilentUncountedAndUnpublished(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	jobID, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "errmsg10", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w1})
	require.NoError(t, err)
	taskIDStr := h.UUIDStringForTest(taskID)

	_, err = q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "done", WorkerID: w1, AssignmentEpoch: claimed.AssignmentEpoch,
		FinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err, "fixture: the terminal transition must land")

	ch, cancel := broker.Subscribe(events.Filter{JobID: h.UUIDStringForTest(jobID), TaskID: taskIDStr})
	defer cancel()

	fenceBefore := h.TaskLogFenceRejections()
	statusBefore := h.TaskStatusFenceRejections()
	logged := captureLog(t)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: taskIDStr, Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		ErrorMessage: "too late", Epoch: int64(claimed.AssignmentEpoch),
	})

	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	assert.Empty(t, rows, "a fence-refused append must store nothing")

	var published []byte
	select {
	case e := <-ch:
		published = e.Data
	default:
	}
	assert.Nil(t, published, "a fence-refused append must publish nothing")
	assert.Empty(t, logged(), "the fence-rejection arm must be entirely silent")
	assert.Equal(t, fenceBefore, h.TaskLogFenceRejections(),
		"the new site must not join task_log_fence, whose published meaning rests on that arm having no Go-side pre-filter")

	// POSITIVE CONTROL, and it is what stops the four assertions above being
	// vacuous: the message really did reach the write path. The status write one
	// statement below is refused by its terminality predicate and IS counted,
	// which is the whole strictly-weaker-fence argument for adding no counter
	// here.
	after := h.TaskStatusFenceRejections()
	assert.Equal(t, statusBefore.Conflicting+1, after.Conflicting,
		"the following status write must have been reached and refused")
}
