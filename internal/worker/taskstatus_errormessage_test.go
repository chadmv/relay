package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three storability properties the coordinator must give an agent-supplied
// message before it can become a task_logs row.
//
// UNTAGGED ON PURPOSE. These subtests touch no database, and the lane CI runs is
// `go test -race ./...` with no build tag; behind //go:build integration this
// guard compiles and never executes.
func TestSanitizeAgentErrorMessage_BoundsAndValidity(t *testing.T) {
	t.Run("a short ascii message is unchanged", func(t *testing.T) {
		require.Equal(t, "boom", sanitizeAgentErrorMessage("boom"))
	})

	t.Run("a NUL byte is removed", func(t *testing.T) {
		got := sanitizeAgentErrorMessage("boom\x00boom")
		require.Equal(t, "boomboom", got)
		require.NotContains(t, got, "\x00")
	})

	t.Run("a message that is only NUL bytes sanitises to empty", func(t *testing.T) {
		// The premise of the caller's admission guard: the guard has to be applied
		// to this result and not to the raw field, or a message that is entirely
		// removable bytes still writes a content-free "[failed] " line.
		require.Equal(t, "", sanitizeAgentErrorMessage("\x00\x00"))
	})

	t.Run("an oversized message is cut at the bound and stays valid UTF-8", func(t *testing.T) {
		// A three-byte rune, so the bound does NOT fall on a rune boundary: with
		// MaxAgentErrorMessageBytes not a multiple of 3, a naive msg[:N] cut lands
		// mid-rune and produces invalid UTF-8. That is the discriminating
		// property; a one-byte input, or a two-byte rune with an even bound, is
		// green under the naive cut and proves nothing. The rune is written as an
		// escape deliberately - a raw non-ASCII byte in this file is unverifiable
		// by eye.
		in := strings.Repeat("\u20ac", MaxAgentErrorMessageBytes)
		got := sanitizeAgentErrorMessage(in)
		assert.True(t, utf8.ValidString(got), "the truncated message must be valid UTF-8")
		assert.LessOrEqual(t, len(got), MaxAgentErrorMessageBytes)
		assert.Greater(t, len(got), MaxAgentErrorMessageBytes-4,
			"the cut must be AT the bound, not far below it")
		assert.True(t, strings.HasPrefix(in, got), "truncation must keep a prefix of the input")
	})

	t.Run("invalid UTF-8 from a non-wire caller is made valid", func(t *testing.T) {
		// NOT reachable from an agent: proto.Unmarshal rejects a string field
		// carrying invalid UTF-8 outright ("string field contains invalid UTF-8"),
		// so the stream dies before handleTaskStatus sees it. This arm is defence
		// in depth for a caller that builds the struct in Go, and the subtest name
		// says that rather than naming a wire state the format forbids.
		require.True(t, utf8.ValidString(sanitizeAgentErrorMessage("ok\xff\xfe tail")))
	})
}

// statusStubDB is the narrowest store.DBTX that drives handleTaskStatus's
// error-message write without Postgres, so the arms below sit in the lane CI
// runs. It dispatches on the statement rather than on a call index, because the
// property under test is WHICH statements ran and an index-keyed script would
// silently re-label them the moment one is skipped.
type statusStubDB struct {
	task      store.Task
	appendErr error

	getTaskCalls atomic.Int64
	appendCalls  atomic.Int64
	updateCalls  atomic.Int64

	// appendArgs captures the last AppendTaskLog call's positional arguments
	// (TaskID, AssignmentEpoch, WorkerID, MinFinishedAt, Stream, Content), so a
	// test can pin what was WRITTEN rather than only that a write happened. No
	// existing caller drives two AppendTaskLog calls in one test, so "the last
	// one" and "the only one" coincide; a future test that does must not trust
	// this field across more than one call without re-checking that.
	appendArgs []any
}

func (d *statusStubDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("statusStubDB: no Exec is expected on this path")
}

func (d *statusStubDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("statusStubDB: no Query is expected on this path")
}

func (d *statusStubDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "name: GetTask :one"):
		d.getTaskCalls.Add(1)
		return statusStubRow{task: &d.task}
	case strings.Contains(sql, "name: AppendTaskLog :one"):
		d.appendCalls.Add(1)
		d.appendArgs = args
		return statusStubRow{err: d.appendErr}
	case strings.Contains(sql, "name: UpdateTaskStatus :one"):
		d.updateCalls.Add(1)
		// The fence refusing. Chosen so handleTaskStatus records the rejection and
		// RETURNS, which keeps this stub to three statements: a success would
		// continue into FailDependentTasks and the job recompute.
		return statusStubRow{err: pgx.ErrNoRows}
	}
	panic("statusStubDB: unexpected statement: " + sql)
}

type statusStubRow struct {
	err  error
	task *store.Task
}

// Scan fills a Task POSITIONALLY, in getTask's column order, because the fields
// this stub has to control collide by type: id and worker_id are both
// pgtype.UUID and name and status are both string, so a fill-by-destination-type
// stub cannot express "assigned to W" or "status dispatched" at all.
func (r statusStubRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.task == nil {
		return nil
	}
	t := r.task
	vals := []any{
		&t.ID, &t.JobID, &t.Name, &t.Env, &t.Requires, &t.TimeoutSeconds, &t.Retries,
		&t.RetryCount, &t.Status, &t.WorkerID, &t.StartedAt, &t.FinishedAt, &t.CreatedAt,
		&t.AssignmentEpoch, &t.Source, &t.Commands, &t.AssignedAt,
	}
	if len(dest) != len(vals) {
		panic("statusStubDB: getTask's column list changed; update this stub")
	}
	for i, d := range dest {
		switch v := d.(type) {
		case *pgtype.UUID:
			*v = *(vals[i].(*pgtype.UUID))
		case *string:
			*v = *(vals[i].(*string))
		case *int32:
			*v = *(vals[i].(*int32))
		case *pgtype.Timestamptz:
			*v = *(vals[i].(*pgtype.Timestamptz))
		case *[]byte:
			*v = *(vals[i].(*[]byte))
		}
	}
	return nil
}

const statusStubTaskID = "3f1c0a2e-7b64-4d8a-9c31-0e5b6a7d8c90"

func statusStubWorkerID() pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{9}, Valid: true} }

// newStatusStubHandler returns a handler whose GetTask reports a live task
// assigned to statusStubWorkerID at epoch 7, so both Go gates pass and control
// genuinely reaches the error-message write rather than returning above it.
func newStatusStubHandler(appendErr error) (*Handler, *statusStubDB) {
	var id pgtype.UUID
	if err := id.Scan(statusStubTaskID); err != nil {
		panic(err)
	}
	db := &statusStubDB{
		appendErr: appendErr,
		task: store.Task{
			ID: id, JobID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			Name: "t", Status: "dispatched", WorkerID: statusStubWorkerID(),
			AssignmentEpoch: 7, Retries: 0, RetryCount: 0,
			CreatedAt: pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true},
		},
	}
	return &Handler{q: store.New(db), broker: events.NewBroker()}, db
}

// statusStubSubscribe tails statusStubTaskID's log events, mirroring
// fenceSubscribe in tasklog_fence_counter_test.go. Subscribing is load-bearing
// here for the same reason it is there: publishTaskLog's HasLogSubscriber
// short-circuit means a chunk published with no subscriber is invisible, so
// without this the publish assertion below would pass on a handler that never
// publishes at all.
func statusStubSubscribe(t *testing.T, h *Handler) func() []events.Event {
	t.Helper()
	ch, cancel := h.broker.Subscribe(events.Filter{TaskID: statusStubTaskID})
	t.Cleanup(cancel)
	return func() []events.Event {
		var got []events.Event
		for {
			select {
			case e := <-ch:
				got = append(got, e)
			default:
				return got
			}
		}
	}
}

func statusStubUpdate(msg string) *relayv1.TaskStatusUpdate {
	return &relayv1.TaskStatusUpdate{
		TaskId:       statusStubTaskID,
		Status:       relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
		ErrorMessage: msg,
		Epoch:        7,
	}
}

// The admission guard must be applied to the SANITISED message, not to the raw
// field. A proto3 string may legally carry a NUL and the sanitiser removes it,
// so a guard on the raw field admits a message with no content left and stores a
// bare "[failed] " line - the same blank line the empty-message case exists to
// prevent, reached through a different input.
func TestHandleTaskStatus_AMessageThatSanitisesToNothingWritesNoLine(t *testing.T) {
	ctx := context.Background()
	h, db := newStatusStubHandler(nil)
	lim := newIngestLogLimiter(&h.ingestDrops)

	h.handleTaskStatus(ctx, statusStubWorkerID(), lim, statusStubUpdate("\x00\x00"))

	require.Equal(t, int64(1), db.getTaskCalls.Load(), "fixture: the report must have reached the gates")
	require.Equal(t, int64(1), db.updateCalls.Load(),
		"fixture: the report is still processed - the absence below is the guard, not an early return")
	assert.Zero(t, db.appendCalls.Load(),
		"a message with no content left after sanitisation must not be appended at all")

	// Positive control on the SAME handler: a message that survives sanitisation
	// still lands, so a guard that had stopped admitting anything cannot pass.
	h.handleTaskStatus(ctx, statusStubWorkerID(), lim, statusStubUpdate("a real cause"))
	require.Equal(t, int64(1), db.appendCalls.Load(),
		"positive control: a message with content must still be appended")
}

// A REAL append failure must not be silent. The fence refusing is expected and
// stays silent; a statement timeout or serialization failure that hits
// AppendTaskLog's CTE while the smaller UPDATE below succeeds would otherwise
// lose the prepare-failure cause with nothing recording it anywhere.
//
// The poisoned input is FIRST: an unconditional log line on the error path is
// killed by the ErrNoRows leg, and a leg placed after the real-failure leg could
// not see it.
func TestHandleTaskStatus_ARealAppendFailureIsLoggedAndAFenceRejectionIsNot(t *testing.T) {
	const marker = "handleTaskStatus AppendTaskLog"

	t.Run("the fence refusing stays silent", func(t *testing.T) {
		h, db := newStatusStubHandler(pgx.ErrNoRows)
		lim := newIngestLogLimiter(&h.ingestDrops)
		logged := captureUnitLog(t)

		h.handleTaskStatus(context.Background(), statusStubWorkerID(), lim, statusStubUpdate("too late"))

		require.Equal(t, int64(1), db.appendCalls.Load(), "fixture: the append must have been attempted")
		assert.Equal(t, "", logged(),
			"a fence rejection is expected and caller-driven: no line of any wording")
	})

	t.Run("a real failure is logged under the connection budget", func(t *testing.T) {
		h, db := newStatusStubHandler(
			errors.New("ERROR: canceling statement due to statement timeout (SQLSTATE 57014)"))
		lim := newIngestLogLimiter(&h.ingestDrops)
		logged := captureUnitLog(t)

		h.handleTaskStatus(context.Background(), statusStubWorkerID(), lim, statusStubUpdate("boom"))

		require.Equal(t, int64(1), db.appendCalls.Load(), "fixture: the append must have been attempted")
		assert.Contains(t, logged(), marker,
			"a real persist failure loses the prepare-failure cause and must say so")
		assert.NotContains(t, logged(), "boom",
			"the message is agent-supplied and can carry whatever a job's script echoed; never log it")
	})
}

// AppendTaskLog's positional args are TaskID, AssignmentEpoch, WorkerID,
// MinFinishedAt, Stream, Content (internal/store/tasks.sql.go), so Stream is
// args[4] and Content is args[5]. Before statusStubDB captured them, the stub's
// QueryRow discarded every variadic argument at its own signature
// (`_ ...any`), so no untagged test could observe errorMessageLogStream's
// value at all - mutating it to "stdout" reddened nothing here. Both args are
// pinned in one assertion so a transposition of the two reddens too, and the
// publish leg is checked against the same stream so a caller that replaces the
// constant at only one of handler.go's two read sites still reddens.
func TestHandleTaskStatus_ErrorMessageWritesAndPublishesTheDocumentedStream(t *testing.T) {
	ctx := context.Background()
	h, db := newStatusStubHandler(nil)
	lim := newIngestLogLimiter(&h.ingestDrops)
	published := statusStubSubscribe(t, h)

	h.handleTaskStatus(ctx, statusStubWorkerID(), lim, statusStubUpdate("boom"))

	require.Equal(t, int64(1), db.appendCalls.Load(), "fixture: the append must have happened")
	require.Len(t, db.appendArgs, 6, "fixture: AppendTaskLog's positional arg count changed; update this stub and this test")
	assert.Equal(t, "stderr", db.appendArgs[4], "args[4] is Stream")
	assert.Equal(t, "[failed] boom\n", db.appendArgs[5], "args[5] is Content, pinned alongside Stream so a swap of the two reddens")

	events := published()
	require.Len(t, events, 1, "the success leg must publish exactly one task-log event")
	var got map[string]any
	require.NoError(t, json.Unmarshal(events[0].Data, &got))
	assert.Equal(t, "stderr", got["stream"], "the published event must carry the same stream the row was stored with")
}

// The status path's persist line must not be silenceable by the LOG path.
//
// This is the shape the two bad-task-id kinds were split for, and it is NOT
// multiplication: it is one site consuming the other's dedupe entry. The log
// path's persist arm fires on a chunk whose CONTENT Postgres refuses at Bind,
// which happens before the fence's WHERE is evaluated - so a sender that owns
// neither the task nor the generation can arm the entry for any (task, epoch)
// it names. Sharing one kind then costs the status-path line exactly when a
// concurrent real fault makes it the only record that the cause was lost.
func TestHandleTaskStatus_TheLogPathCannotSilenceTheStatusPersistLine(t *testing.T) {
	ctx := context.Background()
	h, db := newStatusStubHandler(
		errors.New("ERROR: canceling statement due to statement timeout (SQLSTATE 57014)"))
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)

	// Arm the key from the log path at the SAME (task, epoch) the report below
	// carries. One connection, one budget, exactly as Connect allocates it.
	h.handleTaskLog(ctx, statusStubWorkerID(), lim, &relayv1.TaskLogChunk{
		TaskId: statusStubTaskID, Content: []byte("x"), Epoch: 7,
	})
	require.Contains(t, logged(), "handleTaskLog AppendTaskLog",
		"fixture: the log path must have logged, which is what arms its dedupe entry")

	h.handleTaskStatus(ctx, statusStubWorkerID(), lim, statusStubUpdate("boom"))

	require.Equal(t, int64(2), db.appendCalls.Load(),
		"fixture: both sites must have attempted their append, so the absence below is the dedupe key")
	assert.Contains(t, logged(), "handleTaskStatus AppendTaskLog",
		"a different incident at a different site must not be deduped away by the log path's entry")
}
