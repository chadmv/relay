//go:build integration

package worker_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

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
