//go:build integration

package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reconcileFixture is one worker (with an agent token) plus four tasks claimed
// by it: "match" (the agent will report it at the correct epoch), "stale" (the
// agent will report it at a wrong epoch), "serverOnly" (the agent will not
// report it at all) and "match2", a second correctly-reportable task used only
// by the unparseable-id test, so that its report can place TWO parseable entries
// AFTER the unparseable one (see the ordering note there). The first three cover
// the defect and both positive controls in a single reconcile call.
type reconcileFixture struct {
	rawToken     string
	hostname     string
	matchID      pgtype.UUID
	matchEpoch   int32
	staleID      pgtype.UUID
	serverOnlyID pgtype.UUID
	match2ID     pgtype.UUID
	match2Epoch  int32
}

func seedReconcileFixture(t *testing.T, ctx context.Context, q *store.Queries, tag string) reconcileFixture {
	t.Helper()

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "recon-" + tag + "@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	hostname := "recon-" + tag + "-host"
	workerRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: hostname, Hostname: hostname, CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	raw := "test-token-recon-" + tag
	hash := tokenhash.Hash(raw)
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: workerRow.ID, AgentTokenHash: &hash,
	}))

	claim := func(name string) (pgtype.UUID, int32) {
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: job.ID, Name: name, Commands: []byte(`[["true"]]`),
			Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
			ID: task.ID, WorkerID: pgtype.UUID{Bytes: workerRow.ID.Bytes, Valid: true},
		})
		require.NoError(t, err)
		return claimed.ID, claimed.AssignmentEpoch
	}

	f := reconcileFixture{rawToken: raw, hostname: hostname}
	f.matchID, f.matchEpoch = claim("match")
	f.staleID, _ = claim("stale")
	f.serverOnlyID, _ = claim("server-only")
	f.match2ID, f.match2Epoch = claim("match2")
	return f
}

// runRegister drives one complete Connect with the given running-task report and
// returns the RegisterResponse the coordinator sent to the agent.
func runRegister(t *testing.T, ctx context.Context, h *worker.Handler, f reconcileFixture, running []*relayv1.RunningTask) *relayv1.RegisterResponse {
	t.Helper()
	stream := &fakeStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{{
			Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: f.hostname,
					CpuCores: 1, RamGb: 1, Os: "linux",
					RunningTasks: running,
					Credential:   &relayv1.RegisterRequest_AgentToken{AgentToken: f.rawToken},
				},
			},
		}},
		sentCh: make(chan struct{}, 1),
		hold:   make(chan struct{}),
	}

	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()

	select {
	case <-stream.sentCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RegisterResponse never sent")
	}
	close(stream.hold)
	<-done

	require.Len(t, stream.sent, 1)
	resp := stream.sent[0].GetRegisterResponse()
	require.NotNil(t, resp)
	return resp
}

// TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings is the headline
// guard for bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones.
//
// pgtype.UUID.Scan accepts three spellings that decode to the same 16 bytes and
// equal none of uuidStr's output. Before the fix, reconcileRunningTasks keyed
// agentSet on, and looked serverSet up with, the raw wire string, so each of
// these missed the map: the `!ok` short-circuit skipped the epoch comparison and
// cancelled a live task, and the requeue loop then requeued it as "not reported".
func TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)

	cases := []struct {
		name  string
		spell func(canonical string) string
	}{
		{"uppercase", strings.ToUpper},
		{"undashed", func(c string) string { return strings.ReplaceAll(c, "-", "") }},
		{
			// parseUUID splices out indices 8, 13, 18 and 23 of a 36-byte input
			// without ever checking that they are hyphens, so these four bytes are
			// entirely caller-chosen.
			"non_hyphen_separators",
			func(c string) string {
				return c[0:8] + "_" + c[9:13] + "_" + c[14:18] + "_" + c[19:23] + "_" + c[24:]
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			grace := worker.NewGraceRegistry(1*time.Minute, func(string, int32) {})
			t.Cleanup(grace.Stop)
			h := worker.NewHandlerWithGrace(q, pool, worker.NewRegistry(), events.NewBroker(), func() {}, grace)

			f := seedReconcileFixture(t, ctx, q, tc.name)

			matchCanonical := h.UUIDStringForTest(f.matchID)
			staleCanonical := h.UUIDStringForTest(f.staleID)
			matchWire := tc.spell(matchCanonical)
			staleWire := tc.spell(staleCanonical)

			// Vacuity guard: ToUpper is a no-op on an id with no a-f digit
			// (p ~ 1.2e-7). Fail loudly rather than pass for the wrong reason.
			require.NotEqual(t, matchCanonical, matchWire, "spelling must differ from canonical or this case proves nothing")
			require.NotEqual(t, staleCanonical, staleWire, "spelling must differ from canonical or this case proves nothing")

			resp := runRegister(t, ctx, h, f, []*relayv1.RunningTask{
				{TaskId: matchWire, Epoch: int64(f.matchEpoch)},
				{TaskId: staleWire, Epoch: 999},
			})

			// THE BUG. A genuinely-assigned task, reported at the correct epoch in
			// a non-canonical spelling, must not be cancelled...
			assert.NotContains(t, resp.CancelTaskIds, matchWire, "correctly-reported task must not be cancelled")
			assert.NotContains(t, resp.CancelTaskIds, matchCanonical, "correctly-reported task must not be cancelled")

			// ...and must not be requeued, and its generation must not be ended.
			match, err := q.GetTask(ctx, f.matchID)
			require.NoError(t, err)
			assert.Equal(t, "dispatched", match.Status, "correctly-reported task must not be requeued")
			assert.Equal(t, f.matchEpoch, match.AssignmentEpoch, "correctly-reported task must keep its assignment epoch")

			// POSITIVE CONTROL 1: a stale epoch still cancels, the cancel is NOT
			// requeued, and the id comes back in the AGENT'S OWN SPELLING.
			// cancelIDs is an echo, not a comparison: the agent looks it up in its
			// own runner map, which it keyed with the string it just reported.
			// Canonicalizing here would break cancellation for exactly the
			// non-canonical clients this fix serves. See reconcileRunningTasks.
			assert.Contains(t, resp.CancelTaskIds, staleWire, "a stale-epoch task must still be cancelled")
			assert.NotContains(t, resp.CancelTaskIds, staleCanonical, "cancelIDs must echo the agent's spelling, never canonicalize it")
			stale, err := q.GetTask(ctx, f.staleID)
			require.NoError(t, err)
			assert.Equal(t, "dispatched", stale.Status, "a reported-but-stale task is cancelled, not requeued")

			// POSITIVE CONTROL 2: a task the agent never reported still requeues.
			serverOnly, err := q.GetTask(ctx, f.serverOnlyID)
			require.NoError(t, err)
			assert.Equal(t, "pending", serverOnly.Status, "an unreported task must still be requeued")

			assert.Len(t, resp.CancelTaskIds, 1, "exactly one cancel: the stale-epoch task")
		})
	}
}

// TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing pins
// the two deliberate decisions around an id that pgtype.UUID.Scan rejects
// outright. It is green against the pre-canonicalization handler too, because
// that behaviour was deliberately preserved - its value is that it reddens if
// anyone later changes either decision. Proven discriminating by mutation:
// replacing the `cancelIDs = append(...)` in the parse-failure branch with a bare
// `continue` fails the echo assertion; turning its `continue` into a `break`
// fails both contamination assertions; adding any log.Printf to that branch
// fails the empty-log assertion.
func TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)

	grace := worker.NewGraceRegistry(1*time.Minute, func(string, int32) {})
	t.Cleanup(grace.Stop)
	h := worker.NewHandlerWithGrace(q, pool, worker.NewRegistry(), events.NewBroker(), func() {}, grace)

	f := seedReconcileFixture(t, ctx, q, "unparseable")
	matchCanonical := h.UUIDStringForTest(f.matchID)
	match2Canonical := h.UUIDStringForTest(f.match2ID)

	// Neither 32 nor 36 bytes, so parseUUID rejects it on length alone. It also
	// carries a NUL and a newline: if this ever DID reach a log line, the line
	// would be unescaped and split in two, which is precisely why the branch may
	// not log at all outside the connection budget.
	const garbage = "not-a-uuid-at-all-\x00-with-a-NUL-and-a-\n-newline"

	logged := captureLog(t)

	// THE ORDERING IS LOAD-BEARING, NOT COSMETIC. The unparseable entry goes
	// FIRST, with two parseable-and-assigned entries after it. With the garbage
	// last, any mutation that TERMINATES the loop at the parse failure is
	// invisible - `continue` -> `break` was verified to pass the whole
	// TestRegisterWorker_Reconcile set under the old ordering - even though it
	// silently converts one bad id into "requeue every task this agent is
	// running", the exact failure this file exists to prevent. Garbage first
	// makes `break` redden both contamination assertions below; the second
	// parseable entry additionally catches a mutation that consumes only the
	// next entry rather than all of them.
	resp := runRegister(t, ctx, h, f, []*relayv1.RunningTask{
		{TaskId: garbage, Epoch: 1},
		{TaskId: matchCanonical, Epoch: int64(f.matchEpoch)},
		{TaskId: match2Canonical, Epoch: int64(f.match2Epoch)},
	})

	// DECIDED BEHAVIOUR: an unparseable id names no assignment of ours, so the
	// agent is told to stop it - echoed byte for byte, exactly as before the
	// canonicalization fix. Dropping it would be fail-open and silent.
	assert.Equal(t, []string{garbage}, resp.CancelTaskIds,
		"an unparseable id is echoed into the cancel list verbatim, and nothing else is cancelled")

	// An unparseable sibling must not contaminate the parseable entries that
	// follow it - neither the one immediately after nor any later one.
	match, err := q.GetTask(ctx, f.matchID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", match.Status, "a correctly-reported task survives an unparseable sibling")
	assert.Equal(t, f.matchEpoch, match.AssignmentEpoch, "a correctly-reported task keeps its assignment epoch")

	match2, err := q.GetTask(ctx, f.match2ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", match2.Status, "every parseable entry after an unparseable one is still reconciled")
	assert.Equal(t, f.match2Epoch, match2.AssignmentEpoch, "every parseable entry after an unparseable one keeps its epoch")

	// NO UNBUDGETED LOG LINE. reconcileRunningTasks runs inside finishRegister, at
	// registration, before Connect allocates this connection's ingestLogLimiter,
	// so it has no budget to spend and may not log a caller-supplied id at all.
	// Asserting the WHOLE captured log is empty (rather than NotContains) means
	// any wording reddens this, exactly like
	// TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll.
	assert.Empty(t, logged(), "no log line anywhere in this registration: reconcile has no budget to spend, "+
		"and this also reddens if a NEW unbudgeted registration log site is added on an adjacent path "+
		"(auto-enroll and inventory-replace already log here)")
}
