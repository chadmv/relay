//go:build integration

package worker_test

import (
	"context"
	"testing"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
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
