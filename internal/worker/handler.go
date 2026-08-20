package worker

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync/atomic"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/events"
	"relay/internal/metrics"
	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// agentTokenGenerator generates a fresh (rawHex, hash) pair for a new agent token.
// Overridable in tests via SetAgentTokenGeneratorForTest.
var agentTokenGenerator = func() (string, string) {
	rawBytes := make([]byte, 32)
	if _, err := cryptorand.Read(rawBytes); err != nil {
		return "", ""
	}
	rawHex := hex.EncodeToString(rawBytes)
	return rawHex, tokenhash.Hash(rawHex)
}

// errEnrollmentNotConsumable is returned inside the enrollment transaction when
// ConsumeAgentEnrollment returns rows == 0 (already consumed or concurrent race).
var errEnrollmentNotConsumable = errors.New("enrollment not consumable")

// errWorkerRevoked is returned inside the auto-enroll transaction when the
// existing worker row for this hostname has status 'revoked'.
var errWorkerRevoked = errors.New("worker revoked")

// remoteAddr returns the gRPC peer address for logging, or "unknown".
func remoteAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return "unknown"
}

// DefaultTrailingLogWindow is how long after a task's finished_at its assignee
// may still append log chunks to it. It bounds a window that used to be
// infinite: a terminal transition deliberately keeps worker_id and
// assignment_epoch so a trailing chunk still lands (see UpdateTaskStatus), and
// with no third predicate that stayed true for as long as the row existed.
//
// 15m is a JUDGEMENT CALL, not a derived bound, and the distinction matters
// because the obvious derivation is wrong. Three agent-side timers do compose to
// bound a SINGLE reconnect attempt at roughly 105s: cmd.WaitDelay (5s,
// internal/agent/runner.go), gRPC keepalive ping + timeout (30s + 10s,
// cmd/relay-server/main.go) and the reconnect backoff cap (60s, internal/agent).
// But that cap is PER ATTEMPT. Agent.Run retries indefinitely, and sendCh is
// buffered 64 and shared across reconnects (internal/agent/agent.go); runSender
// drops at most the one in-flight message per stream drop and does not
// re-enqueue, so everything else survives the outage and flushes on reconnect.
// The real bound on a late chunk is therefore the outage duration, which is
// unbounded: a 20-minute network partition delivers a chunk 20 minutes late and
// this window drops it. That is deliberate, and RELAY_TASKLOG_TRAILING_WINDOW is
// the escape hatch - but do not defend the number with a total the code does not
// enforce.
//
// The reachable case is narrower than "any queued chunk". chunkWriter's writes
// are enqueued before sendFinalStatus and sendCh is FIFO, so a chunk ordered
// BEFORE the terminal status cannot outlive it - by the time the status has
// landed, that chunk already has. What can arrive after the terminal status is a
// chunk enqueued after it: the WaitDelay orphan-writer race, and the
// cancel/abandon cleanup paths.
//
// So: large enough that no ordinary agent-side delay truncates real output,
// small enough that "forever" is genuinely closed. Override with
// RELAY_TASKLOG_TRAILING_WINDOW.
const DefaultTrailingLogWindow = 15 * time.Minute

// Handler implements relayv1.AgentServiceServer.
type Handler struct {
	relayv1.UnimplementedAgentServiceServer
	q               *store.Queries
	pool            *pgxpool.Pool
	registry        *Registry
	broker          *events.Broker
	triggerDispatch func()
	grace           *GraceRegistry

	// Metrics, when non-nil, receives worker utilization samples and tracks
	// per-worker liveness. Set by cmd/relay-server after construction.
	Metrics *metrics.Store

	// AllowAutoEnroll, when true, permits agents to register with no credential
	// (token-less auto-enrollment). Set by cmd/relay-server after construction.
	AllowAutoEnroll bool

	// TrailingLogWindow bounds how long after a task's finished_at its assignee
	// may still append log chunks. Non-positive means DefaultTrailingLogWindow,
	// which is what keeps every existing NewHandler/NewHandlerWithGrace call site
	// correct with no edit and lets a test narrow the window to prove the wiring.
	// Set by cmd/relay-server after construction, from
	// RELAY_TASKLOG_TRAILING_WINDOW. Read-only after startup.
	TrailingLogWindow time.Duration
}

// NewHandler returns a Handler wired to the given dependencies.
func NewHandler(q *store.Queries, pool *pgxpool.Pool, r *Registry, b *events.Broker, triggerDispatch func()) *Handler {
	return &Handler{q: q, pool: pool, registry: r, broker: b, triggerDispatch: triggerDispatch}
}

// NewHandlerWithGrace is like NewHandler but also wires in a GraceRegistry so
// that agent disconnects start a grace timer instead of immediately requeueing.
func NewHandlerWithGrace(q *store.Queries, pool *pgxpool.Pool, r *Registry, b *events.Broker, triggerDispatch func(), g *GraceRegistry) *Handler {
	return &Handler{q: q, pool: pool, registry: r, broker: b, triggerDispatch: triggerDispatch, grace: g}
}

// Connect implements relayv1.AgentServiceServer.
func (h *Handler) Connect(stream relayv1.AgentService_ConnectServer) error {
	ctx := stream.Context()

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv first message: %w", err)
	}
	reg := first.GetRegister()
	if reg == nil {
		return fmt.Errorf("first message must be RegisterRequest")
	}

	workerID, sender, err := h.authenticateAndRegister(ctx, stream, reg)
	if err != nil {
		return err
	}

	// Registered above, so the teardown defer must be armed BEFORE any path that
	// can return early below, or a failed connection leaves its sender in the
	// registry (identity-checked teardown).
	defer h.teardownConnection(workerID, sender)

	// This UUID is the identity every task_log write from this connection is
	// fenced on, so a failure to parse it is not survivable: it would silently
	// drop 100% of this worker's log output. workerID came from uuidStr() over a
	// UUID the server just read out of Postgres, so a failure here means
	// something is badly wrong. Fail loudly and close the stream instead.
	var workerUUID pgtype.UUID
	if err := workerUUID.Scan(workerID); err != nil {
		log.Printf("worker: connection rejected, worker id %q is not a usable UUID: %v", workerID, err)
		return status.Errorf(codes.Internal, "worker identity unusable")
	}

	// ONE log budget per CONNECTION, for every caller-driven log line on this
	// recv goroutine. It is deliberately NOT a field on Handler: Handler is
	// shared by every connection, and a shared budget would let one agent
	// suppress another's diagnostics. It is deliberately not in a registry
	// either - as a stack local it dies with this frame, so there is no teardown
	// to get wrong and no way for a stale connection to clobber a fresh one.
	//
	// It never escapes this goroutine, which is what lets it be mutex-free. DO
	// NOT capture it in a goroutine, store it anywhere, or hand it to anything
	// that outlives this call. TestConnect_TwoConnectionsDoNotShareTheLogBudget
	// is what pins this allocation site.
	lim := newIngestLogLimiter()

	// Message loop.
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch p := msg.Payload.(type) {
		case *relayv1.AgentMessage_TaskStatus:
			h.handleTaskStatus(ctx, workerUUID, lim, p.TaskStatus)
		case *relayv1.AgentMessage_TaskLog:
			h.handleTaskLog(ctx, workerUUID, lim, p.TaskLog)
		case *relayv1.AgentMessage_WorkspaceInventory:
			h.handleInventoryUpdate(ctx, workerUUID, lim, p.WorkspaceInventory)
		case *relayv1.AgentMessage_Telemetry:
			h.handleTelemetry(workerID, p.Telemetry)
		}
	}
}

// authenticateAndRegister dispatches to the appropriate auth path based on the credential type.
func (h *Handler) authenticateAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest) (string, *workerSender, error) {
	switch cred := reg.Credential.(type) {
	case *relayv1.RegisterRequest_EnrollmentToken:
		return h.enrollAndRegister(ctx, stream, reg, cred.EnrollmentToken)
	case *relayv1.RegisterRequest_AgentToken:
		return h.reconnectAndRegister(ctx, stream, reg, cred.AgentToken)
	default:
		if h.AllowAutoEnroll {
			return h.autoEnrollAndRegister(ctx, stream, reg)
		}
		return "", nil, status.Errorf(codes.Unauthenticated, "auto-enroll disabled")
	}
}

// enrollAndRegister handles first-time enrollment using an enrollment token.
// All DB writes (worker upsert, enrollment consume, agent-token set) execute
// inside a single transaction so that a failure anywhere leaves no partial
// state — either the agent is fully enrolled or not at all.
func (h *Handler) enrollAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest, rawEnroll string) (string, *workerSender, error) {
	if rawEnroll == "" {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}

	hash := tokenhash.Hash(rawEnroll)
	enroll, err := h.q.GetAgentEnrollmentByTokenHash(ctx, hash)
	if err != nil {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	if enroll.ConsumedAt.Valid {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	if time.Now().After(enroll.ExpiresAt.Time) {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}

	rawAgent, agentHash := agentTokenGenerator()
	if rawAgent == "" || agentHash == "" {
		return "", nil, status.Errorf(codes.Internal, "token gen failed")
	}

	var workerID pgtype.UUID
	txErr := pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := h.q.WithTx(tx)

		w, err := txq.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
			Name:               reg.Hostname,
			Hostname:           reg.Hostname,
			CpuCores:           reg.CpuCores,
			RamGb:              reg.RamGb,
			GpuCount:           reg.GpuCount,
			GpuModel:           reg.GpuModel,
			Os:                 reg.Os,
			SupportsWorkspaces: reg.SupportsWorkspaces,
		})
		if err != nil {
			return fmt.Errorf("upsert worker: %w", err)
		}
		workerID = w.ID

		rows, err := txq.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{
			ID:         enroll.ID,
			ConsumedBy: w.ID,
		})
		if err != nil {
			return fmt.Errorf("consume enrollment: %w", err)
		}
		if rows == 0 {
			return errEnrollmentNotConsumable
		}

		if err := txq.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
			ID: w.ID, AgentTokenHash: &agentHash,
		}); err != nil {
			return fmt.Errorf("set agent token: %w", err)
		}
		return nil
	})

	if txErr != nil {
		if errors.Is(txErr, errEnrollmentNotConsumable) {
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
		return "", nil, txErr
	}

	return h.finishRegister(ctx, stream, reg, workerID, rawAgent)
}

// reconnectAndRegister handles agent reconnection using a previously issued agent token.
func (h *Handler) reconnectAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest, rawAgent string) (string, *workerSender, error) {
	if rawAgent == "" {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	hash := tokenhash.Hash(rawAgent)

	w, err := h.q.GetWorkerByAgentTokenHash(ctx, &hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
		return "", nil, status.Errorf(codes.Internal, "token lookup failed")
	}

	return h.finishRegister(ctx, stream, reg, w.ID, "")
}

// autoEnrollAndRegister handles token-less enrollment when AllowAutoEnroll is
// set. It upserts the worker by hostname and issues a fresh agent token without
// consuming any enrollment record.
func (h *Handler) autoEnrollAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest) (string, *workerSender, error) {
	rawAgent, agentHash := agentTokenGenerator()
	if rawAgent == "" || agentHash == "" {
		return "", nil, status.Errorf(codes.Internal, "token gen failed")
	}

	var workerID pgtype.UUID
	txErr := pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := h.q.WithTx(tx)

		existing, err := txq.GetWorkerByHostnameForUpdate(ctx, reg.Hostname)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lookup worker: %w", err)
		}
		if err == nil && existing.Status == "revoked" {
			return errWorkerRevoked
		}

		w, err := txq.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
			Name:               reg.Hostname,
			Hostname:           reg.Hostname,
			CpuCores:           reg.CpuCores,
			RamGb:              reg.RamGb,
			GpuCount:           reg.GpuCount,
			GpuModel:           reg.GpuModel,
			Os:                 reg.Os,
			SupportsWorkspaces: reg.SupportsWorkspaces,
		})
		if err != nil {
			return fmt.Errorf("upsert worker: %w", err)
		}
		workerID = w.ID

		if err := txq.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
			ID: w.ID, AgentTokenHash: &agentHash,
		}); err != nil {
			return fmt.Errorf("set agent token: %w", err)
		}
		return nil
	})
	if txErr != nil {
		if errors.Is(txErr, errWorkerRevoked) {
			return "", nil, status.Errorf(codes.Unauthenticated, "worker revoked")
		}
		return "", nil, txErr
	}

	// reg.Hostname is a caller-supplied proto string: validated nowhere, bounded
	// only by gRPC's 4 MiB default receive limit. %q is the injection defence and
	// clipID the volume defence, the same pair the ingest log sites use. This line
	// is the ONLY record anywhere that a token-less enrollment happened, so a
	// forgeable one corrupts the audit trail of the mechanism it documents - the
	// RELAY_ALLOW_AUTO_ENROLL gate is not a substitute. It is bounded to one line
	// per connection by registration itself, so it takes no budget key.
	log.Printf("worker: auto-enrolled worker %s (hostname=%q) from %s", uuidStr(workerID), clipID(reg.Hostname), remoteAddr(ctx))
	return h.finishRegister(ctx, stream, reg, workerID, rawAgent)
}

// finishRegister updates worker status, reconciles running tasks, sends RegisterResponse,
// registers the sender, and triggers dispatch.
func (h *Handler) finishRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest, id pgtype.UUID, rawAgentToken string) (string, *workerSender, error) {
	updated, err := h.q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:                 id,
		LastSeenAt:         pgtype.Timestamptz{Time: time.Now(), Valid: true},
		SupportsWorkspaces: reg.SupportsWorkspaces,
	})
	if err != nil {
		return "", nil, fmt.Errorf("register worker connection: %w", err)
	}

	workerID := uuidStr(updated.ID)

	// Agent reconnected within its grace window — stop the requeue timer.
	if h.grace != nil {
		h.grace.Cancel(workerID)
	}

	// Reconcile the agent's running-task report against DB state.
	cancelIDs, err := h.reconcileRunningTasks(ctx, updated.ID, reg.RunningTasks)
	if err != nil {
		return "", nil, fmt.Errorf("reconcile: %w", err)
	}

	// Replace workspace inventory with what the agent reported at reconnect.
	if err := h.applyInventory(ctx, updated.ID, reg.Inventory); err != nil {
		log.Printf("worker: register inventory replace failed for %s: %v", uuidStr(updated.ID), err)
	}

	// Send RegisterResponse on the raw stream. At this point the worker is not
	// yet in the registry, so no other goroutine can race us on stream.Send.
	if err := stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{
				WorkerId:      workerID,
				CancelTaskIds: cancelIDs,
				AgentToken:    rawAgentToken,
			},
		},
	}); err != nil {
		return "", nil, fmt.Errorf("send register response: %w", err)
	}

	// From here on, all sends go through the serializing wrapper.
	sender := NewWorkerSender(stream)
	sender.connEpoch = updated.ConnectionEpoch
	h.registry.Register(workerID, sender)

	if h.Metrics != nil {
		h.Metrics.Activate(workerID, time.Now())
	}

	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"online"}`, workerID)),
	})

	go h.triggerDispatch()

	return workerID, sender, nil
}

// reconcileRunningTasks compares the agent's reported running tasks against
// the coordinator's DB state. Returns the list of task IDs the agent should
// cancel (stale epoch or unknown to coordinator). Any task the coordinator
// has assigned to this worker but the agent didn't report is requeued.
func (h *Handler) reconcileRunningTasks(ctx context.Context, workerID pgtype.UUID, reported []*relayv1.RunningTask) ([]string, error) {
	serverTasks, err := h.q.GetActiveTasksForWorker(ctx, workerID)
	if err != nil {
		return nil, err
	}

	serverSet := make(map[string]int64, len(serverTasks))
	for _, t := range serverTasks {
		serverSet[uuidStr(t.ID)] = int64(t.AssignmentEpoch)
	}

	var cancelIDs []string
	agentSet := make(map[string]bool, len(reported))
	for _, rt := range reported {
		// A STRING THAT PARSED IS NOT A STRING THAT IS CANONICAL. serverSet above
		// is keyed on uuidStr - lowercase, hyphenated, 36 bytes - and rt.TaskId is
		// whatever the agent chose to send. pgtype.UUID.Scan accepts three
		// independent WAYS a spelling can differ from that and still decode to the
		// same 16 bytes: uppercase hex (hex.DecodeString takes A-F), the 32-byte
		// undashed form, and the 36-byte form with ANY four bytes at indices 8, 13,
		// 18 and 23, which parseUUID splices out without ever checking they are
		// hyphens. They COMPOSE - the third axis alone is 2^32 strings per id - so
		// these are three families, not three strings. Re-encoding is total across
		// all of them by construction, which is why the tests cover the axes and
		// not their combinations.
		//
		// Keying and looking up on the wire string therefore missed the map, and
		// the `!ok` short-circuit below skipped the epoch comparison ENTIRELY - so
		// a live, correctly-reported task was cancelled here AND requeued by the
		// loop that follows (its canonical key looked "not reported"), silently.
		//
		// SCOPE - the shipped Go agent never triggered this, on any reconnect.
		// scheduler/dispatch.go sends uuidStr(claimed.ID), agent.go keys a.runners
		// on exactly that string and reports it back verbatim, so its spelling is
		// canonical by construction. The exposure is a reimplemented or
		// third-party agent that spells ids differently: an interop bug with a
		// silent failure mode, not a live production one.
		//
		// Compare on the RE-ENCODING, never on the input. Same rule as
		// handleTaskLog's canonicalID block and logKey's doc comment.
		var tID pgtype.UUID
		if err := tID.Scan(rt.TaskId); err != nil {
			// UNPARSEABLE. It can name no assignment of ours, so tell the agent to
			// stop it: that is the pre-canonicalization behaviour, preserved
			// DELIBERATELY, not an oversight. Dropping it instead would be
			// fail-open and completely silent, leaving a subprocess the
			// coordinator knows nothing about running with no signal anywhere.
			//
			// IT IS NOT LOGGED, AND MUST NOT BE. This runs inside finishRegister,
			// at registration - BEFORE Connect allocates this connection's
			// ingestLogLimiter, so this site has no budget to spend at all. A line
			// here would be unbudgeted, caller-driven volume with a caller-chosen
			// payload; clip + %q is not a substitute for the missing budget. See
			// bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget.
			// TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing
			// asserts the whole captured log is empty, so any wording reddens it.
			cancelIDs = append(cancelIDs, rt.TaskId)
			continue
		}
		canonical := uuidStr(tID)
		agentSet[canonical] = true
		srvEpoch, ok := serverSet[canonical]
		if !ok || srvEpoch != rt.Epoch {
			// cancelIDs ECHOES THE AGENT'S OWN SPELLING, and that is a wire
			// contract, not laziness. The agent looks each id up in its own runner
			// map (internal/agent/agent.go: `a.runners[tid]`), keyed with the same
			// string it just reported to us. Canonicalizing here would hand a
			// non-canonical agent - the exact client this canonicalization exists
			// to serve - a spelling it has never used; its lookup would miss,
			// Abandon() would never run, and a task the coordinator has decided to
			// cancel would keep running. "Not cancelled at all" is strictly worse
			// than "cancelled spuriously". It buys less than it looks like, mind:
			// api/cancel_signals.go always sends the DB's canonical rendering, so a
			// re-spelling agent already misses every runtime CancelTask; the echo
			// only helps on this register-response path. The canonical form belongs
			// on the COMPARISON, never on the echo. Pinned by the stale-epoch control in
			// TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings.
			cancelIDs = append(cancelIDs, rt.TaskId)
		}
	}

	// Anything server has but agent didn't report → requeue.
	requeued := 0
	for taskIDStr := range serverSet {
		if agentSet[taskIDStr] {
			continue
		}
		var tID pgtype.UUID
		if err := tID.Scan(taskIDStr); err != nil {
			continue
		}
		_ = h.q.RequeueTaskByID(ctx, tID)
		requeued++
	}

	// Wake the scheduler so requeued tasks are dispatched immediately.
	if requeued > 0 {
		go h.triggerDispatch()
	}

	return cancelIDs, nil
}

// handleTaskStatus processes a TaskStatusUpdate from an agent.
//
// workerID is the connection's own authenticated worker, resolved at
// registration and never taken from the wire - an agent cannot influence it, so
// it cannot claim to be somebody else. It is threaded here the same way
// handleTaskLog and applyInventoryUpdate already receive it.
//
// lim is this connection's log budget, allocated once in Connect. Both pre-gate
// log lines below run AHEAD of the identity and currency gates, so the budget is
// the only thing bounding them - see
// docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md.
func (h *Handler) handleTaskStatus(ctx context.Context, workerID pgtype.UUID, lim *ingestLogLimiter, upd *relayv1.TaskStatusUpdate) {
	var taskID pgtype.UUID
	if err := taskID.Scan(upd.TaskId); err != nil {
		// Under the connection's budget, keyed on kindBadTaskIDStatus with NO wire
		// value. NOT shared with handleTaskLog's identical guard, though it was
		// until 2026-08-15: sharing saved one token out of sixteen and cost the
		// log path's line entirely, which is the only signal anywhere for an agent
		// losing 100% of a task's output. See the logKind block.
		//
		// %q is MANDATORY and is the injection defence; clipID is the volume
		// defence. Neither substitutes for the other: upd.TaskId has just FAILED
		// to parse, so it is a proto string bounded only by gRPC's receive limit,
		// and %q escapes without truncating.
		//
		// BOTH defences must also be applied to the ERROR, which is the
		// non-obvious half: pgtype's parse failure is
		// fmt.Errorf("cannot parse UUID %v", src), so err carries a verbatim,
		// unescaped SECOND COPY of the same caller bytes. Rendering it with a
		// bare %v would leave the line unbounded and unescaped no matter what was
		// done to upd.TaskId. Measured against the pre-slice handler with a 100k
		// id: the SINGLE line rendered 200060 bytes, and the WHOLE captured buffer
		// of TestHandleTaskStatus_MalformedTaskIdsAreLoggedOncePerConnectionAndClipped
		// (that line plus its 64 short followers) rendered 205544. Both numbers are
		// stated because the figure alone was previously ambiguous between them.
		if lim.allow(logKey{kind: kindBadTaskIDStatus}) {
			log.Printf("worker: handleTaskStatus bad task id %q: %q", clipID(upd.TaskId), clipID(err.Error()))
		}
		return
	}

	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		// pgx.ErrNoRows here means the named task does not exist. That is
		// indistinguishable from a forged message, carries nothing an operator
		// can act on, and is the cheapest message an attacker can send - so it is
		// dropped SILENTLY, exactly as handleTaskLog drops an unresolvable chunk
		// and exactly as both gates below drop a rejected one. It also has one
		// legitimate cause: DeleteJob cascades to tasks (tasks.job_id ... ON
		// DELETE CASCADE, migration 000001), so a task row can vanish under a
		// running agent, and there is nothing to do about that either.
		//
		// Any other error is real infrastructure - a pool failure, a context
		// cancellation - and logs under the connection's budget. Keyed on
		// kindStatusGetTask with no wire value, because such an episode is not
		// per-task: keying on the task id would multiply one infra event by the
		// task count.
		//
		// The id is rendered from the PARSED value, never from upd.TaskId. It
		// needs no clip - Scan succeeded, so the wire string is at most 36 bytes -
		// but that is a length constraint and not a byte constraint: parseUUID
		// splices out indices 8, 13, 18 and 23 of a 36-byte input without ever
		// checking they are hyphens, so four bytes of upd.TaskId are caller-chosen
		// and uninspected. uuidStr re-encodes canonically, which is what keeps a
		// newline out of this line. Every id-bearing log site in this file does the
		// same; see the logKey doc comment.
		if !errors.Is(err, pgx.ErrNoRows) && lim.allow(logKey{kind: kindStatusGetTask}) {
			log.Printf("worker: handleTaskStatus GetTask %s: %v", uuidStr(taskID), err)
		}
		return
	}

	// Assignment gate, part one: IDENTITY. A task's status machine may only be
	// driven by the agent the task is assigned to. workerID is resolved at
	// registration and never read off the wire, so a sender cannot claim to be
	// somebody else.
	//
	// This gate is NOT the correctness control any more, and the honest form of
	// that is uncomfortable, so it is written down rather than implied. Delete
	// it and the observable state is unchanged: a forged terminal from a
	// non-assignee reaches IncrementTaskRetryCount, which rejects on its own
	// worker_id predicate, or UpdateTaskStatus, which rejects on its own. Both
	// statements also fence on assignment_epoch and on the task not already
	// being terminal, so the retry branch is atomic with respect to the GetTask
	// above and cannot resurrect a finished, cancelled or requeued task.
	//
	// What the gate still buys, stated at its true size rather than its
	// flattering one. An earlier draft of this comment claimed "zero round trips
	// and zero log lines", and BOTH halves were wrong - measure before you
	// justify:
	//
	// First, cost: ONE FEWER round trip per forged message, not zero. GetTask
	// above has already run by the time control reaches here, so the saving is
	// one statement instead of two, not two instead of none. Real on a recv
	// goroutine that a single sender can drive as fast as it likes, but bounded.
	//
	// It does NOT save a log line. Both write-error branches below are wrapped
	// in `if !errors.Is(err, pgx.ErrNoRows)`, so a forged message rejected by
	// either fence logs nothing at all - delete this gate and the log volume is
	// unchanged. Nor does this function have a "zero attacker-keyed log lines"
	// property to protect: the two branches at the top of this function still
	// run AHEAD of this gate, and a gate cannot protect a line placed before
	// it. What bounds them now is the CONNECTION'S BUDGET (ingestLogLimiter),
	// which is keyed on nothing the caller supplies - the GetTask branch's
	// pgx.ErrNoRows case is silent outright, and everything else there costs at
	// most one line per connection.
	// bug-2026-08-12-tasklog-err-limiter-attacker-keyed is closed; do not cite
	// it here as live.
	//
	// Second, and this is the load-bearing reason: it answers a different
	// question. This gate asks "may this sender drive this task's status machine
	// at all", the predicates ask "is the row still in the state the branch
	// decision was made from". Merging them loses the first question, and the
	// first question is the one this function's branch structure actually asks.
	// Third, defense in depth against a future edit to either half. Keep BOTH;
	// do not delete this as redundant with the SQL, and do not delete the SQL
	// predicates as redundant with this.
	//
	// Keep all three terms. Against a real, non-zero worker UUID the two .Valid
	// checks are mutually redundant with the Bytes comparison, and !workerID.Valid
	// is unreachable from Connect, which closes the stream on a Scan failure
	// rather than calling in with a zero value. What they buy is NULL rejection:
	// pgtype.UUID is a comparable struct, so with BOTH .Valid checks dropped a
	// zero-value workerID (a caller that lost its identity) compares EQUAL to a
	// never-claimed task's NULL worker_id - the Go form of SQL's
	// IS NOT DISTINCT FROM - and the gate fails OPEN. Removing either one alone
	// leaves the hole closed; removing both opens it. That is defense in depth
	// against a future caller. Note honestly that NO test in the tree
	// discriminates it any more:
	// TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask
	// was written as its permanent guard, but once IncrementTaskRetryCount gained
	// its own worker_id predicate that test stays green with this whole gate
	// deleted - measured, not assumed. Its discriminating power moved to the SQL
	// layer, and what remains here is non-functional (one round trip), which is
	// why it is recorded as a Known Limitation rather than pinned by a new test.
	// Same rule the SQL states as "a plain =,
	// never IS NOT DISTINCT FROM"; see internal/store/query/tasks.sql.
	//
	// Silent return, exactly like the currency gate below. A log line here would
	// be attacker-keyed volume on the recv goroutine, with no sink to send it to;
	// detection belongs with the audit-log work.
	if !workerID.Valid || !task.WorkerID.Valid || task.WorkerID.Bytes != workerID.Bytes {
		return
	}

	// Assignment gate, part two: CURRENCY. Reject any status update whose epoch
	// does not match the current assignment. Retry logic below must not run on
	// stale updates. The epoch answers "is this generation current"; the identity
	// check above answers "are you who you say you are". Neither substitutes for
	// the other - do not delete either, and do not merge them into one condition.
	//
	// Note this comparison widens the stored int32 to int64 rather than narrowing
	// the wire value, so there is no truncation window here. Nothing must be
	// inserted above it that reads or narrows upd.Epoch.
	if int64(task.AssignmentEpoch) != upd.Epoch {
		return
	}

	// Map proto enum to string status.
	var statusStr string
	switch upd.Status {
	case relayv1.TaskStatus_TASK_STATUS_RUNNING:
		statusStr = "running"
	case relayv1.TaskStatus_TASK_STATUS_DONE:
		statusStr = "done"
	case relayv1.TaskStatus_TASK_STATUS_FAILED:
		statusStr = "failed"
	case relayv1.TaskStatus_TASK_STATUS_TIMED_OUT:
		statusStr = "timed_out"
	case relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED:
		// A prepare failure (sync failed, or the worker has no workspace
		// provider for a source-bearing task) is a terminal failure: route it
		// through the existing "failed" path so retry, dependent-cascade, and
		// slot release all apply.
		statusStr = "failed"
	default:
		return
	}

	terminal := statusStr == "failed" || statusStr == "timed_out"

	// Retry if applicable. The branch decision is made from the T0 row read by
	// GetTask above, so the statement re-checks that row: AssignmentEpoch is the
	// generation the currency gate already proved current (int32 is safe for the
	// same reason it is at the UpdateTaskStatus call below - the gate compared it
	// against an int32 column), and WorkerID is this connection's own identity,
	// not the task.WorkerID we just read, for the reason written at the
	// UpdateTaskStatus call site. If a cancel or a requeue landed in between, or
	// the task is already finished, the statement affects zero rows and the
	// retry is dropped.
	if terminal && task.RetryCount < task.Retries {
		if _, err := h.q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{
			ID:              taskID,
			AssignmentEpoch: int32(upd.Epoch),
			WorkerID:        workerID,
		}); err != nil {
			// pgx.ErrNoRows is the fence rejecting, not a failure: the task
			// finished, was cancelled, or the generation ended between the
			// GetTask above and here. Drop silently, exactly like the two gates.
			// In the genuine case the cancel or the requeue won, which is the
			// correct outcome, so there is nothing to diagnose. Any other error
			// is real.
			if !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("worker: handleTaskStatus IncrementTaskRetryCount %s: %v", uuidStr(taskID), err)
			}
		} else {
			// Both of these are already correctly gated on the write having
			// happened, and must stay that way: a rejected retry must not
			// recompute the job status (which would drag a cancelled job back to
			// running - RecomputeJobStatus has no notion of `cancelled`) and must
			// not wake the dispatcher.
			updateJobStatusFromTasks(ctx, h.q, task.JobID)
			_ = h.q.NotifyTaskSubmitted(ctx)
		}
		return
	}

	// Determine timestamps.
	startedAt := task.StartedAt
	if statusStr == "running" {
		startedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	var finishedAt pgtype.Timestamptz
	if terminal || statusStr == "done" {
		finishedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}

	// WorkerID is a FENCE here, not a value to write: UpdateTaskStatus no longer
	// writes worker_id at all. Pass the connection's own identity rather than the
	// task.WorkerID we just read - the gate above already proved they are equal,
	// and binding our own identity keeps the SQL predicate a genuine second check
	// instead of a self-comparison against a value read moments earlier.
	updated, err := h.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:              taskID,
		Status:          statusStr,
		WorkerID:        workerID,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		AssignmentEpoch: int32(upd.Epoch),
	})
	if err != nil {
		// pgx.ErrNoRows is the fence rejecting, not a failure: the row is
		// already terminal (a duplicate terminal message), or the generation
		// ended between the GetTask above and here. Drop silently, exactly like
		// the two gates - a log line here would be caller-controlled volume on
		// the recv goroutine with no sink to send it to, and detection belongs
		// with the audit-log work. Any other error is real.
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("worker: handleTaskStatus UpdateTaskStatus %s -> %s: %v", uuidStr(taskID), statusStr, err)
		}
		return
	}

	if terminal {
		if err := h.q.FailDependentTasks(ctx, taskID); err != nil {
			log.Printf("worker: handleTaskStatus FailDependentTasks %s: %v", uuidStr(taskID), err)
		}
	}

	jobStatus := updateJobStatusFromTasks(ctx, h.q, updated.JobID)

	h.broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(updated.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(taskID), statusStr)),
	})

	if jobStatus == "done" || jobStatus == "failed" {
		h.broker.Publish(events.Event{
			Type:  "job",
			JobID: uuidStr(updated.JobID),
			Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(updated.JobID), jobStatus)),
		})
	}

	// Any terminal status or task completion frees a worker slot — wake dispatcher.
	if terminal || statusStr == "done" {
		_ = h.q.NotifyTaskCompleted(ctx)
	}
}

// taskLogEvent is the JSON payload of an events.TypeTaskLog SSE frame. seq,
// stream, content and created_at are field-identical to the polling endpoint's
// logEntry (internal/api/tasks.go) so a consumer can merge live frames with
// GET /v1/tasks/{id}/logs pages using one type. task_id and job_id are added so
// a "?task_id="-only subscriber can route and cache-key by job without a second
// request. seq is task_logs.id, so it is a total order per task and an exact
// dedupe key against the backfill.
type taskLogEvent struct {
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// taskLogPublishes counts chunks that got past the HasLogSubscriber fast path,
// i.e. that are about to be marshalled and published. Test-only observability (read via
// TaskLogPublishesForTest in export_test.go) for the "nothing is marshalled when
// nobody is tailing" guarantee, which is otherwise unobservable from outside.
// Production code never reads it.
var taskLogPublishes atomic.Int64

// handleTaskLog appends a log chunk from an agent and, if anyone is tailing that
// task, publishes it to the SSE broker.
//
// workerID is the connection's own authenticated worker, resolved at
// registration and never taken from the wire - an agent cannot influence it, so
// it cannot claim to be somebody else. It is threaded here in the same way
// applyInventoryUpdate already receives it.
//
// The trailing window is resolved per call and passed to the fence as an
// absolute cutoff. It costs one time.Now() and one bound parameter - still one
// round trip, still no allocation on the quiet path. See AppendTaskLog's comment
// for why the cutoff is computed here rather than as NOW() - interval in SQL.
//
// This runs synchronously on the Connect recv goroutine, which also carries that
// worker's status, inventory and telemetry messages, so everything below is
// deliberately cheap: exactly one DB round trip (the insert itself returns the
// job id and seq), one map lookup when nobody is watching, and a non-blocking
// Publish. Do not add a query, a goroutine, or a queue here.
//
// lim is this connection's log budget, allocated once in Connect. It bounds
// every caller-driven log line below; it performs one map lookup and one integer
// compare and never blocks, which is what keeps this function inside the
// one-round-trip budget stated above.
func (h *Handler) handleTaskLog(ctx context.Context, workerID pgtype.UUID, lim *ingestLogLimiter, chunk *relayv1.TaskLogChunk) {
	var taskID pgtype.UUID
	if err := taskID.Scan(chunk.TaskId); err != nil {
		// Deliberately symmetric with handleTaskStatus's identical guard, but with
		// its OWN budget key (kindBadTaskIDLog). They shared one key until
		// 2026-08-15; one forged 1-byte status message then suppressed this line
		// for the connection's whole life, and this is the line that must survive.
		//
		// This used to return silently, and that was correct BEFORE the budget
		// existed: an unbounded line on this path is a flood vector. Logging it
		// is safe only because of the budget. It earns its line because an agent
		// sending unparseable ids on the log path loses 100% of that task's
		// output with no other signal anywhere - the one failure mode here with
		// total, silent data loss.
		//
		// %q is the injection defence, clipID the volume defence. Keep both, and
		// keep them on the ERROR too: pgtype renders the offending string
		// verbatim into its parse error, so a bare %v would re-emit the same
		// unbounded caller bytes unescaped. See handleTaskStatus's guard.
		if lim.allow(logKey{kind: kindBadTaskIDLog}) {
			log.Printf("worker: handleTaskLog bad task id %q: %q", clipID(chunk.TaskId), clipID(err.Error()))
		}
		return
	}

	stream := "stdout"
	if chunk.Stream == relayv1.LogStream_LOG_STREAM_STDERR {
		stream = "stderr"
	}

	// Resolved per call, never cached: a test moves the field between two calls
	// on the same handler to prove this call site actually reads it. Non-positive
	// means the default, which is what keeps every existing NewHandler call site
	// correct with no edit.
	window := h.TrailingLogWindow
	if window <= 0 {
		window = DefaultTrailingLogWindow
	}

	row, err := h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID:          taskID,
		Stream:          stream,
		Content:         string(chunk.Content),
		AssignmentEpoch: int32(chunk.Epoch),
		WorkerID:        workerID,
		MinFinishedAt:   pgtype.Timestamptz{Time: time.Now().Add(-window), Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// pgx.ErrNoRows means the fence rejected the chunk, for any of three
			// independent reasons: the sender is not the task's current assignee (a
			// forged or misrouted chunk - workerID comes from the authenticated
			// registration, never from the wire); the sender's generation is stale
			// because the task was requeued or cancelled (both bump
			// assignment_epoch); or the task finished longer ago than the trailing
			// window, which is DefaultTrailingLogWindow unless
			// RELAY_TASKLOG_TRAILING_WINDOW overrides it. THAT THIRD ONE IS THE ONE TO
			// SUSPECT FIRST WHEN OUTPUT IS MISSING RATHER THAN SPURIOUS: it is the
			// only cause that is operator-configurable, time-dependent, and triggered
			// by a perfectly legitimate sender, so a window set too small truncates
			// the tail of real task output with no other symptom anywhere. The three
			// are deliberately indistinguishable here; see the comment on
			// AppendTaskLog. Expected - drop it silently, and in
			// particular do NOT publish it: a zombie agent's output would otherwise
			// appear in a live view and then vanish on refresh, because it was
			// correctly never stored.
			//
			// THIS ARM IS DELIBERATELY SIDE-EFFECT-FREE AND MUST STAY SILENT. A log
			// line here would be caller-driven volume on the recv goroutine, and it
			// would fire on the legitimate late-flush case as well as on forged
			// chunks. Observability for this arm is
			// idea-2026-08-14-tasklog-fence-rejection-is-unobservable, whose answer
			// is a COUNTER, not a log line. Nothing here may publish. Pinned by
			// TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll, which asserts the
			// whole captured log is empty, so any wording reddens it.
			return
		}

		// A real persist failure that used to be swallowed by `_ =`.
		//
		// Deduplicated to one line per task per assignment epoch: the realistic
		// such failure repeats for every chunk of the task (binary stdout ->
		// 'invalid byte sequence for encoding "UTF8"'), and this runs on the recv
		// goroutine, so logging per chunk would delay that worker's status,
		// inventory and telemetry ingest. The dedupe is keyed on wire values on
		// purpose and is NOT the bound - the connection's token bucket is. See
		// ingestLogLimiter.
		//
		// The epoch goes in at full int64 width here. The int32 narrowing at the
		// fence parameter above is bug-2026-08-12-tasklog-epoch-int32-truncation
		// and is deliberately untouched by this slice.
		//
		// Never log chunk.Content: it is raw subprocess output and can contain
		// secrets a job's own script echoed. Logging the error with %v is safe
		// because pgconn.PgError.Error() renders only severity, message and
		// SQLSTATE - never Detail, which is where Postgres puts "Failing row
		// contains (...)". Do not start logging pgErr.Detail here.
		//
		// THE ID IS THE CANONICAL RE-ENCODING, NOT THE WIRE STRING, in the log line
		// AND in the dedupe key - and the distinction is the whole point. Passing
		// taskID.Scan constrains the wire string's LENGTH (36 bytes, so no clip is
		// needed) and NOT its bytes: for a 36-byte input parseUUID splices
		// src[0:8]+src[9:13]+src[14:18]+src[19:23]+src[24:] and never checks that
		// indices 8, 13, 18 and 23 are hyphens, so four bytes are fully
		// caller-chosen and never inspected. Scan("aaaaaaaa\nbbbb\ncccc\ndddd\n
		// eeeeeeeeeeee") succeeds. Rendering the wire string with a bare %s turned
		// one event into five physical log lines, and keying on it gave a caller
		// 2^32 distinct dedupe keys for one (task, epoch) pair. uuidStr closes both
		// at once and is already what the broker publishes. Pinned by
		// TestHandleTaskLog_TheLoggedTaskIdIsCanonicalNotTheWireString.
		canonicalID := uuidStr(taskID)
		if lim.allow(logKey{kind: kindTaskLogPersist, id: canonicalID, epoch: chunk.Epoch}) {
			log.Printf("worker: handleTaskLog AppendTaskLog %s: %v", canonicalID, err)
		}
		return
	}

	// Persistence is unconditional and strictly precedes any publish; the publish
	// is derived from the stored row, so no line is ever published unstored.
	taskIDStr := uuidStr(taskID)
	if !h.broker.HasLogSubscriber(taskIDStr) {
		return // steady state: one map lookup, no marshal, no allocation
	}

	taskLogPublishes.Add(1)
	data, err := json.Marshal(taskLogEvent{
		TaskID:    taskIDStr,
		JobID:     uuidStr(row.JobID),
		Seq:       row.ID,
		Stream:    stream,
		Content:   string(chunk.Content),
		CreatedAt: row.CreatedAt.Time,
	})
	if err != nil {
		log.Printf("worker: handleTaskLog marshal %s: %v", taskIDStr, err)
		return
	}

	h.broker.Publish(events.Event{
		Type:   events.TypeTaskLog,
		JobID:  uuidStr(row.JobID),
		TaskID: taskIDStr,
		Data:   data,
	})
}

// handleTelemetry records a host-utilization sample from an agent, stamped
// with the server's receipt time.
func (h *Handler) handleTelemetry(workerID string, t *relayv1.WorkerTelemetry) {
	if h.Metrics == nil {
		return
	}
	h.Metrics.Append(workerID, metrics.Sample{
		At:             time.Now(),
		CPUPercent:     t.CpuPercent,
		MemUsedBytes:   t.MemUsedBytes,
		MemTotalBytes:  t.MemTotalBytes,
		HasGPU:         t.HasGpu,
		GPUUtilPercent: t.GpuUtilPercent,
		GPUMemUsed:     t.GpuMemUsedBytes,
		GPUMemTotal:    t.GpuMemTotalBytes,
	})
}

// teardownConnection runs when a Connect stream ends. It always stops this
// connection's own send goroutine, but only tears down shared worker state
// (DB offline flag, grace timer / requeue) when this connection still owns the
// worker's registry slot. A newer connection for the same worker must not be
// clobbered (Identity-checked teardown invariant).
func (h *Handler) teardownConnection(workerID string, sender *workerSender) {
	owned := h.registry.UnregisterIf(workerID, sender)
	sender.Close() // always stop our own send goroutine
	if !owned {
		return // a newer connection owns this worker; leave shared state alone
	}
	epoch := sender.connEpoch
	if h.markWorkerOffline(workerID, epoch) == 0 {
		return // a fresher connection holds the epoch; leave grace/requeue alone
	}
	if h.grace != nil {
		h.grace.Start(workerID, epoch)
	} else {
		h.requeueWorkerTasks(workerID, epoch)
	}
}

// markWorkerOffline is called in a defer after the stream ends. It is fenced on
// connection_epoch: if a fresher connection has bumped the epoch, the write
// affects zero rows and the offline broker event / metrics-clear are skipped.
// Returns the number of rows updated (0 = fence superseded, 1 = applied).
func (h *Handler) markWorkerOffline(workerID string, epoch int32) int64 {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return 0
	}
	ctx := context.Background()
	now := time.Now()
	rows, err := h.q.MarkWorkerOfflineIfEpoch(ctx, store.MarkWorkerOfflineIfEpochParams{
		ID:              id,
		LastSeenAt:      pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ConnectionEpoch: epoch,
	})
	if err != nil || rows == 0 {
		return 0
	}
	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"offline"}`, workerID)),
	})
	if h.Metrics != nil {
		h.Metrics.Clear(workerID)
	}
	return rows
}

// requeueWorkerTasks requeues dispatched/running tasks for a disconnected
// worker, fenced on connection_epoch: if a fresher connection has bumped the
// epoch, the EXISTS guard fails and zero tasks move. Bumps assignment_epoch on
// each requeued task (task-level fence preserved).
func (h *Handler) requeueWorkerTasks(workerID string, epoch int32) {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return
	}
	ctx := context.Background()
	_, _ = h.q.RequeueWorkerTasksIfEpoch(ctx, store.RequeueWorkerTasksIfEpochParams{
		WorkerID:        id,
		ConnectionEpoch: epoch,
	})
	go h.triggerDispatch()
}

// updateJobStatusFromTasks atomically recomputes and persists a job's status
// from its tasks in a single SQL statement, so concurrent last-task completions
// can never strand the job in 'running'. Returns the new status string, or ""
// if it could not be determined (e.g. the job has no tasks).
func updateJobStatusFromTasks(ctx context.Context, q *store.Queries, jobID pgtype.UUID) string {
	status, err := q.RecomputeJobStatus(ctx, jobID)
	if err != nil {
		return ""
	}
	return status
}

// applyInventory does a transactional full-replace of workspace inventory for
// a worker: deletes all existing rows, then inserts each non-deleted entry.
func (h *Handler) applyInventory(ctx context.Context, workerID pgtype.UUID, inv []*relayv1.WorkspaceInventoryUpdate) error {
	return pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := h.q.WithTx(tx)
		if err := q.ReplaceWorkerInventory(ctx, workerID); err != nil {
			return err
		}
		for _, u := range inv {
			if u.Deleted {
				continue
			}
			ts, _ := time.Parse(time.RFC3339, u.LastUsedAt) // blank → zero time
			if err := q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
				WorkerID:     workerID,
				SourceType:   u.SourceType,
				SourceKey:    u.SourceKey,
				ShortID:      u.ShortId,
				BaselineHash: u.BaselineHash,
				LastUsedAt:   pgtype.Timestamptz{Time: ts, Valid: !ts.IsZero()},
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// applyInventoryUpdate upserts or deletes a single workspace inventory row.
func (h *Handler) applyInventoryUpdate(ctx context.Context, workerID pgtype.UUID, u *relayv1.WorkspaceInventoryUpdate) error {
	if u.Deleted {
		return h.q.DeleteWorkerWorkspace(ctx, store.DeleteWorkerWorkspaceParams{
			WorkerID: workerID, SourceType: u.SourceType, SourceKey: u.SourceKey,
		})
	}
	ts, _ := time.Parse(time.RFC3339, u.LastUsedAt)
	return h.q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID:     workerID,
		SourceType:   u.SourceType,
		SourceKey:    u.SourceKey,
		ShortID:      u.ShortId,
		BaselineHash: u.BaselineHash,
		LastUsedAt:   pgtype.Timestamptz{Time: ts, Valid: !ts.IsZero()},
	})
}

// handleInventoryUpdate applies one workspace inventory update and reports a
// failure under the connection's log budget.
//
// It exists as a named method rather than an inline block in Connect so that the
// budgeted path is testable at the same layer as handleTaskLog and
// handleTaskStatus, and so the log line has an owner. It adds no logic.
//
// This line needs the budget for the same reason the other three do, and it is
// the CHEAPEST of the four for an attacker: every string in u is bound straight
// into UpsertWorkerWorkspace or DeleteWorkerWorkspace, whose source_type,
// source_key, short_id and baseline_hash columns are all TEXT NOT NULL, so a NUL
// byte in any of them fails during bind-parameter conversion. And no NUL is even
// needed: applyInventoryUpdate swallows the time.Parse error on u.LastUsedAt, so
// an empty string binds SQL NULL into last_used_at, which is also NOT NULL. One
// error per message either way, with no gate ahead of it.
//
// Key is kindInventory with NO wire value: a persist failure here is an episode,
// not a per-workspace event, and keying on the source key would multiply one
// infra event by the workspace count. Never log u itself - source_key is a
// caller-supplied, unbounded depot path.
func (h *Handler) handleInventoryUpdate(ctx context.Context, workerID pgtype.UUID, lim *ingestLogLimiter, u *relayv1.WorkspaceInventoryUpdate) {
	err := h.applyInventoryUpdate(ctx, workerID, u)
	if err == nil {
		return
	}
	if lim.allow(logKey{kind: kindInventory}) {
		log.Printf("worker: inventory update failed: %v", err)
	}
}

// uuidStr converts a pgtype.UUID to its canonical string representation.
func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
