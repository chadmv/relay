//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE PREMISE OF THIS WHOLE SLICE, AND THE ONLY TEST THAT DISCRIMINATES IT.
//
// Every flood test below asserts an UPPER bound on log lines. An upper bound
// passes vacuously at zero, so each of them also carries a lower bound - and the
// lower bounds are only meaningful if a NUL-bearing chunk for a task the fence
// would reject surfaces a NON-pgx.ErrNoRows error. That is the claim: Postgres
// rejects an embedded NUL while converting the text bind parameter
// (pg_verify_mbstr, SQLSTATE 22021), during Bind, BEFORE the portal executes and
// therefore before the fence CTE is evaluated. The fictitious task id and the
// wrong assignee are both irrelevant.
//
// The existing TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch does
// NOT prove this: both of its NUL legs keep the task genuinely assigned at the
// chunk's epoch on purpose (see its own comment), so the fence matches and the
// ordering is unobservable.
//
// If this test is ever RED, do not fix it - the threat model in
// docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md section 2.2 is
// wrong and every bound below is measuring nothing.
func TestHandleTaskLog_ANulChunkForAnUnfencedTaskIsAPersistErrorNotAFenceRejection(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "premise@example.com", "w-premise")

	// A well-formed UUID that names no row. The fence cannot match it, so if the
	// NUL were NOT rejected first, this would come back as pgx.ErrNoRows and be
	// dropped silently.
	logged := captureLog(t)
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId:  "00000000-0000-0000-0000-0000000000ff",
		Content: []byte("x\x00"),
		Epoch:   1,
	})

	assert.Equal(t, 1, countLines(logged(), "handleTaskLog AppendTaskLog"),
		"a NUL chunk for a task the fence cannot match must still surface a persist error; "+
			"if this is 0 the NUL is being rejected AFTER the fence and this slice's threat model is wrong")

	// Control on the same code path: the SAME nonexistent task id with clean
	// content is a plain fence rejection and stays silent. Without this the
	// assertion above could pass because handleTaskLog logs every failure.
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId:  "00000000-0000-0000-0000-0000000000ff",
		Content: []byte("clean\n"),
		Epoch:   1,
	})
	assert.Equal(t, 1, countLines(logged(), "handleTaskLog AppendTaskLog"),
		"a fence rejection with clean content must stay silent")
}

// Upper bound used by every distinct-key flood test below. ingestLogBurst is 16
// (pinned by TestIngestLogLimiter_ConstantsAreWhatTheHandlerTestsAssume, which
// is where you go if you tune it). The extra headroom absorbs up to four
// ingestLogRefill intervals in case a loaded container makes 64 round trips take
// more than 10 seconds; the EXACT arithmetic is pinned deterministically in the
// unit tests with an injected clock, not here.
//
// The LOWER bound in each test is not decoration. An upper bound alone passes
// vacuously at zero, and zero is exactly what these tests would see if the NUL
// were rejected after the fence instead of before it. See
// TestHandleTaskLog_ANulChunkForAnUnfencedTaskIsAPersistErrorNotAFenceRejection.
const floodLogUpperBound = 20

// A caller emitting a fresh random task id per message defeats a limiter keyed
// on the task id. This is the item's original repro.
func TestHandleTaskLog_DistinctWireTaskIdsCannotFloodTheLog(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "flood-ids@example.com", "w-flood-ids")

	const secret = "SECRET-sentinel"
	logged := captureLog(t)
	for i := 0; i < 64; i++ {
		h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId:  fmt.Sprintf("00000000-0000-0000-0000-%012x", i+1),
			Content: []byte(secret + "\x00"),
			Epoch:   1,
		})
	}

	lines := countLines(logged(), "handleTaskLog AppendTaskLog")
	assert.GreaterOrEqual(t, lines, 1,
		"the diagnostic must not vanish entirely - if this is 0 the error class is unreachable and the bound below measures nothing")
	assert.LessOrEqual(t, lines, floodLogUpperBound,
		"64 chunks with 64 distinct wire task ids must not produce 64 log lines")
	assert.NotContains(t, logged(), secret, "chunk content must never be logged")
}

// THE DISCRIMINATING TEST, and the permanent record of the spec's section 2.3.
//
// The old limiter stored ONE ENTRY PER TASK ID with the epoch as the VALUE
// (`l.reported[taskID] = epoch`). So with a single fixed task id and a varying
// chunk.Epoch: the lookup hits, the stored epoch differs, the early return is
// skipped, len(reported) is 1 so the capacity branch never fires, and shouldLog
// returns true. One log line per message, forever, with a map of exactly one
// entry and reset() never called.
//
// This is the only test in the tree that stays RED against a fix that merely
// changes reset() to suppress instead of clearing - which is what the backlog
// item itself proposed. Do not delete it and do not weaken it to the
// distinct-ids case above.
func TestHandleTaskLog_VaryingWireEpochOnOneTaskCannotFloodTheLog(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, workerID, _ := seedClaimedTask(t, ctx, q, "flood-epoch@example.com", "w-flood-epoch")
	taskIDStr := h.UUIDStringForTest(taskID)

	const secret = "SECRET-sentinel"
	logged := captureLog(t)
	for e := 1; e <= 64; e++ {
		h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId:  taskIDStr, // ONE task id, on purpose
			Content: []byte(secret + "\x00"),
			Epoch:   int64(e), // varying - this is the whole point
		})
	}

	lines := countLines(logged(), "handleTaskLog AppendTaskLog")
	assert.GreaterOrEqual(t, lines, 1,
		"the diagnostic must not vanish entirely")
	assert.LessOrEqual(t, lines, floodLogUpperBound,
		"64 chunks for ONE task id at 64 epochs must not produce 64 log lines; "+
			"a dedupe map keyed on the task id with the epoch as its VALUE never grows and never caps")
	assert.NotContains(t, logged(), secret, "chunk content must never be logged")
}

// Two legs with two different jobs.
//
// LEG A: an agent sending unparseable ids on the log path loses 100% of that
// task's output with no signal anywhere. That is the one failure mode on this
// path with total, silent data loss, and it is worth exactly one line per
// connection per dedupe window - which is only safe because of the budget.
//
// LEG B: THE TWO PATHS MUST NOT SHARE A KEY. They did until 2026-08-15, and the
// sharing was measured to defeat LEG A's whole point: one 1-byte forged
// TaskStatusUpdate{TaskId: "z"} at the top of a connection consumed the shared
// key, and 64 unparseable ids on the LOG path then produced ZERO lines. The two
// messages are worded differently, so an operator grepping the log path's marker
// saw nothing at all. It needs no attacker either - a buggy agent malforming ids
// on both paths (likely, since both ids come from the same plumbing) reported
// whichever arrived first, nondeterministically. The sharing was defending ONE
// token out of sixteen against losing the only signal for total silent log-data
// loss, so it lost.
func TestHandleTaskLog_MalformedTaskIdIsLoggedOncePerConnectionAndHasItsOwnBudgetKey(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "badid-share@example.com", "w-badid-share")

	// LEG A: fresh Handler, so a fresh budget. Only the log path.
	hA := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})
	loggedA := captureLog(t)
	for i := 0; i < 64; i++ {
		hA.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId: fmt.Sprintf("not-a-uuid-%d", i), Content: []byte("x\n"), Epoch: 1,
		})
	}
	assert.Equal(t, 1, countLines(loggedA(), "handleTaskLog bad task id"),
		"64 unparseable ids on the log path must produce exactly one line per connection")
	assert.Contains(t, loggedA(), "\"not-a-uuid-0\"",
		"the line must name the FIRST offending id, %q-quoted")

	// LEG B: a second fresh Handler, so a fresh budget. The status path burns its
	// bad-id key FIRST, exactly as one forged 1-byte message would; the log path
	// must still report.
	hB := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})
	loggedB := captureLog(t)
	hB.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
		TaskId: "z", Status: relayv1.TaskStatus_TASK_STATUS_RUNNING, Epoch: 1,
	})
	for i := 0; i < 64; i++ {
		hB.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId: fmt.Sprintf("log-side-garbage-%d", i), Content: []byte("x\n"), Epoch: 1,
		})
	}
	logLines := countLines(loggedB(), "handleTaskLog bad task id")
	statusLines := countLines(loggedB(), "handleTaskStatus bad task id")
	assert.Equal(t, 1, logLines,
		"the log path has its OWN bad-task-id key: one forged status message must not silence the "+
			"only signal for total, silent log-data loss")
	assert.Equal(t, 1, statusLines,
		"the status path still costs exactly one line per connection")
}

// pgtype.UUID.Scan CONSTRAINS THE LENGTH, NOT THE BYTES, and every log site that
// renders a task id after a successful Scan used to trust the wire string.
//
// For a 36-byte input parseUUID splices src[0:8]+src[9:13]+src[14:18]+src[19:23]
// +src[24:] and NEVER checks that indices 8, 13, 18 and 23 are hyphens, so those
// four bytes are fully attacker-chosen and never inspected:
//
//	Scan("aaaaaaaa\nbbbb\ncccc\ndddd\neeeeeeeeeeee") -> err=<nil>, valid=true
//
// Reaching the persist-failure line needs no ownership and no epoch match: a NUL
// in content makes Postgres reject the bind before the fence CTE runs (see
// TestHandleTaskLog_ANulChunkForAnUnfencedTaskIsAPersistErrorNotAFenceRejection),
// so the error is non-pgx.ErrNoRows. One event therefore became five physical log
// lines, and the same four free bytes gave 2^32 distinct dedupe keys for ONE
// (task, epoch) pair. Both are closed by logging and keying on the canonical
// re-encoding, which is what the broker already publishes.
func TestHandleTaskLog_TheLoggedTaskIdIsCanonicalNotTheWireString(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "canon-id@example.com", "w-canon-id")

	const canonical = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	forged := func(sep string) string {
		return "aaaaaaaa" + sep + "bbbb" + sep + "cccc" + sep + "dddd" + sep + "eeeeeeeeeeee"
	}

	// LEG A: INJECTION. Newline separators forge four extra physical log lines.
	hA := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})
	loggedA := captureLog(t)
	hA.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: forged("\n"), Content: []byte("x\x00"), Epoch: 1,
	})
	outA := loggedA()
	require.Contains(t, outA, "handleTaskLog AppendTaskLog",
		"fixture: a NUL chunk must reach the persist-failure branch")
	assert.NotContains(t, outA, "aaaaaaaa\nbbbb",
		"the WIRE task id must never reach the log: its separator bytes are unvalidated and caller-chosen")
	assert.Contains(t, outA, canonical,
		"the log must carry the canonical re-encoding of the id that actually parsed")
	assert.Equal(t, 1, strings.Count(outA, "\n"),
		"one event must be one physical log line; newline separators in the wire id must not forge more")

	// LEG B: DEDUPE-KEY ESCAPE. 32 wire ids that all parse to the SAME uuid at the
	// SAME epoch must be ONE dedupe key, not 32.
	hB := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})
	loggedB := captureLog(t)
	for i := 0; i < 32; i++ {
		hB.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId: forged(string(rune('A' + i))), Content: []byte("x\x00"), Epoch: 1,
		})
	}
	assert.Equal(t, 1, countLines(loggedB(), "handleTaskLog AppendTaskLog"),
		"one (task, epoch) pair is one dedupe key; the four bytes Scan never inspects must not vary it")
}

// A status update naming a task that does not exist is indistinguishable from a
// forged one, carries nothing an operator can act on, and is the cheapest
// message an attacker can send. It is dropped SILENTLY - not budgeted - exactly
// as handleTaskLog drops an unresolvable chunk and exactly as both gates further
// down drop a rejected one.
//
// RED at HEAD: 64 lines from the unconditional GetTask branch.
func TestHandleTaskStatus_UnknownWellFormedTaskIdsAreSilent(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "status-unknown@example.com", "w-status-unknown")

	logged := captureLog(t)
	for i := 0; i < 64; i++ {
		h.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
			TaskId: fmt.Sprintf("00000000-0000-0000-0000-%012x", i+1),
			Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
			Epoch:  1,
		})
	}
	assert.Equal(t, 0, countLines(logged(), "handleTaskStatus GetTask"),
		"a status update naming a nonexistent task must be dropped silently, not budgeted")

	// Positive control on the same code path: a real update from the real
	// assignee still lands. Without this, a handleTaskStatus that had stopped
	// working entirely would pass the assertion above.
	h.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  int64(epoch),
	})
	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "running", fresh.Status, "positive control: a genuine status update must still land")
}

// RED at HEAD: 65 lines. Also pins the clip, which the budget alone does not
// give: upd.TaskId has FAILED pgtype.UUID.Scan here, so it is a proto string
// bounded only by gRPC's 4 MiB receive limit and %q escapes without truncating.
func TestHandleTaskStatus_MalformedTaskIdsAreLoggedOncePerConnectionAndClipped(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, _, workerID, _ := seedClaimedTask(t, ctx, q, "status-badid@example.com", "w-status-badid")

	// The FIRST id is the huge one, because only the first is logged.
	huge := strings.Repeat("Z", 100000)
	logged := captureLog(t)
	h.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
		TaskId: huge, Status: relayv1.TaskStatus_TASK_STATUS_RUNNING, Epoch: 1,
	})
	for i := 0; i < 64; i++ {
		h.HandleTaskStatus(ctx, workerID, &relayv1.TaskStatusUpdate{
			TaskId: fmt.Sprintf("not-a-uuid-%d", i),
			Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
			Epoch:  1,
		})
	}

	out := logged()
	assert.Equal(t, 1, countLines(out, "handleTaskStatus bad task id"),
		"65 unparseable ids must produce exactly one line per connection")
	assert.Less(t, len(out), 1000,
		"an unparsed caller-supplied id must be clipped before it reaches the log; %q escapes but does not truncate")
	assert.Contains(t, out, strings.Repeat("Z", 32),
		"the clipped line must still show the leading bytes the agent actually sent")
}

// inventoryFloodStream returns a fakeStream that registers with a real agent
// token and then sends n WorkspaceInventoryUpdates that cannot persist.
//
// A NUL in source_key fails at bind time (source_key is TEXT NOT NULL,
// migration 000007). If that route ever stops erroring, the equally reachable
// alternative is LastUsedAt: "" - applyInventoryUpdate swallows the time.Parse
// error and binds SQL NULL into last_used_at, which is also NOT NULL. Either
// way it is one error per message with no gate ahead of it.
func inventoryFloodStream(ctx context.Context, hostname, rawToken string, n int) *fakeStream {
	msgs := []*relayv1.AgentMessage{{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: hostname, CpuCores: 1, RamGb: 1, Os: "linux",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
			},
		},
	}}
	for i := 0; i < n; i++ {
		msgs = append(msgs, &relayv1.AgentMessage{
			Payload: &relayv1.AgentMessage_WorkspaceInventory{
				WorkspaceInventory: &relayv1.WorkspaceInventoryUpdate{
					SourceType:   "perforce",
					SourceKey:    fmt.Sprintf("//depot/%d\x00", i),
					ShortId:      "s",
					BaselineHash: "b",
					LastUsedAt:   time.Now().UTC().Format(time.RFC3339),
				},
			},
		})
	}
	return &fakeStream{ctx: ctx, sentCh: make(chan struct{}, 1), msgs: msgs}
}

// The third instance of the defect, one line away from the other two in Connect
// and named by neither half of the backlog item. RED at HEAD: 64 lines.
func TestConnect_InventoryPersistFailuresAreBoundedPerConnection(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, rawToken := seedWorkerWithAgentToken(t, ctx, q, "inv-flood")

	logged := captureLog(t)
	require.NoError(t, h.Connect(inventoryFloodStream(ctx, "inv-flood", rawToken, 64)))

	assert.Equal(t, 1, countLines(logged(), "inventory update failed"),
		"64 unpersistable inventory updates on one connection must produce exactly one log line")
}

// THE ONLY TEST THAT PINS THE ALLOCATION SITE. Two real Connect streams against
// ONE Handler. Two limiters means two lines; one limiter shared on the Handler,
// or a package-level one, means one.
//
// The export_test shims cannot substitute for this: they map one limiter per
// *Handler, which is exactly the wrong thing in production. Sequential rather
// than concurrent, so the count is deterministic and there is no ordering race
// on the captured buffer.
//
// RED at HEAD: 128 lines.
func TestConnect_TwoConnectionsDoNotShareTheLogBudget(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, tokenA := seedWorkerWithAgentToken(t, ctx, q, "inv-budget-a")
	_, tokenB := seedWorkerWithAgentToken(t, ctx, q, "inv-budget-b")

	logged := captureLog(t)
	require.NoError(t, h.Connect(inventoryFloodStream(ctx, "inv-budget-a", tokenA, 64)))
	require.NoError(t, h.Connect(inventoryFloodStream(ctx, "inv-budget-b", tokenB, 64)))

	assert.Equal(t, 2, countLines(logged(), "inventory update failed"),
		"each connection gets its OWN budget: two connections must produce two lines. "+
			"One line means the budget is shared (a Handler field or a package-level var) "+
			"and one agent can suppress another's diagnostics")
}

// The existing PersistFailure test's "a stale-epoch drop must stay silent" leg
// counts one marker string, so it cannot see a DIFFERENTLY WORDED log line added
// to the pgx.ErrNoRows arm. This one asserts the whole captured buffer is empty,
// so any wording reddens it.
//
// GREEN at HEAD and after. Its job is the mutation battery, not a HEAD-RED, and
// it is the permanent guard on the branch the counter item will drop into.
func TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "fence-silent@example.com", "w-fence-silent")
	taskIDStr := h.UUIDStringForTest(taskID)

	require.NoError(t, q.RequeueTask(ctx, taskID))
	fresh, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: workerID})
	require.NoError(t, err)
	require.Equal(t, epoch+2, fresh.AssignmentEpoch, "fixture: requeue and redispatch bump twice")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	logged := captureLog(t)
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("from the dead generation\n"), Epoch: int64(epoch),
	})

	assert.Equal(t, "", logged(),
		"the pgx.ErrNoRows arm must be side-effect-free: NO log line of any wording. "+
			"Observability for it is idea-2026-08-14-tasklog-fence-rejection-is-unobservable, whose answer is a counter")

	select {
	case e := <-ch:
		t.Fatalf("a fence-rejected chunk must not be published: %s", e.Data)
	default:
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Empty(t, rows, "a fence-rejected chunk must not be stored")
}
