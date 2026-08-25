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

// errHostnameClaimed is returned inside the auto-enroll transaction when a
// workers row for the claimed hostname already exists - whatever its status and
// whatever its token. It replaces errWorkerRevoked, which was a DENY-LIST OF
// EXACTLY ONE STATUS VALUE and failed open on every status added to the
// vocabulary. "A row exists" is a claim about the table rather than about
// today's writers, so it cannot fail open that way, and it removes the status
// vocabulary from this decision entirely.
var errHostnameClaimed = errors.New("hostname already claimed")

// errCredentialLive is returned inside the enrollment transaction when the
// existing worker row for this hostname still holds an agent_token_hash. THIS IS
// A DIFFERENT PREDICATE FROM errHostnameClaimed's AND THE DIFFERENCE IS FORCED:
// revoking does not delete the row (ClearWorkerAgentToken nulls the hash and sets
// status='revoked'), so refusing every existing row here would make the revoked
// row block its own recovery and leave `relay workers delete` - which destroys
// assignments and reservations - as the only route. NULL means revoked
// (recovery, allowed); non-NULL means a live credential (takeover, refused).
var errCredentialLive = errors.New("worker credential is live")

// errFleetAtCeiling is returned inside the auto-enroll transaction when the
// non-revoked worker count is at or above the ceiling. IT GATES THIS PATH ONLY:
// enrollment tokens are never refused by it, which is what makes "use
// relay agent enroll" the without-downtime answer for an operator whose
// token-less budget is exhausted, and what keeps a bounded refusal from being a
// fleet-wide denial primitive.
var errFleetAtCeiling = errors.New("worker fleet at the auto-enroll ceiling")

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

// DefaultRegistrationTimeout bounds how long a peer that has opened a stream may
// go without sending its RegisterRequest before the server closes the stream.
//
// WITHOUT IT, THE CONNECTION CAPS ARE A WEAPON RATHER THAN A CONTROL. A peer
// that opens a stream and sends nothing reaches Connect and parks in the first
// stream.Recv forever. It never authenticates, so it costs no credential and no
// database round trip; and it never goes idle either, because opening a stream
// zeroes grpc-go's t.idle and MaxConnectionIdle reaps only a transport whose
// t.idle is NON-zero (internal/transport/http2_server.go:582-585, :1204-1220).
// The keepalive liveness probe does not reach it: any frame the peer reads
// re-stamps t.lastRead, so that arm resets forever. Before
// RELAY_GRPC_MAX_CONNS existed, such a peer was bounded only by file
// descriptors - a nuisance, orders of magnitude out. With a 1024-slot cap it is
// a cheap, permanent, fleet-wide denial.
//
// WHAT THIS DOES NOT CLOSE, stated rather than implied. Returning ends the
// STREAM, which re-stamps t.idle and hands the connection back to
// MaxConnectionIdle. It does not end the CONNECTION, so a peer willing to open a
// fresh stream once per idle window keeps its slot indefinitely at very low
// cost. MaxConnectionAge is the arm that would close that, and it is
// deliberately out of this slice - it terminates connections that are doing
// their job. This bound turns "free and permanent" into "requires a periodic
// round trip", which is a real reduction and not a fix.
//
// IT ALSO ADDS TO THE IDLE WINDOW RATHER THAN OVERLAPPING IT, which is what
// makes RAISING it a security change and not only a compatibility one. The
// deadline ends the stream and the idle reaper then takes the connection, so a
// stream-opening peer holds its slot for the SUM - 90s at relay's defaults,
// measured in TestGRPCServer_RegistrationDeadlineAndIdleWindowCompose. Once that
// sum passes grpc-go's 120s connectionTimeout, opening a stream becomes a
// CHEAPER way to park a slot than saying nothing at all, which inverts both
// controls; resolveGRPCBounds (cmd/relay-server/grpc_config.go) warns at startup
// when an operator's two values cross that line.
//
// 30s IS GENEROUS BY ORDERS OF MAGNITUDE, AND THE GAP WAS MEASURED RATHER THAN
// ASSUMED, because this is the knob's fail-aggressive direction: too short and
// healthy agents are cut off before they register and reconnect-loop forever.
// Between client.Connect and stream.Send the agent runs buildRegisterRequest,
// which is a lock-protected copy of its in-memory runner list plus, for a
// workspace provider, ListInventory. That last one is the only I/O, and it is a
// read of the small local .relay-registry.json - cached after the first call, no
// p4 subprocess, no walk of the workspace tree - so a 1 TB workspace does not
// make it slow. The honest window is therefore a network round trip plus one
// small local file read. Anything that changes ListInventory into real work on
// this path has to revisit this number.
//
// Override with RELAY_GRPC_REGISTRATION_TIMEOUT; it can be raised but not
// disabled.
const DefaultRegistrationTimeout = 30 * time.Second

// DefaultAutoEnrollWorkerCeiling bounds how many non-revoked workers may exist
// before token-less auto-enrollment refuses to create another row. Without it, a
// caller that can reach :9090 under RELAY_ALLOW_AUTO_ENROLL creates one
// permanent row per distinct hostname, forever: the rows outlive their
// connections, survive a restart, and appear in every GET /v1/workers page and
// every dispatcher scan.
//
// 1024 IS DERIVED FROM RELAY_GRPC_MAX_CONNS, AND THE DERIVATION IS NOT AIRTIGHT.
// The two knobs count DIFFERENT QUANTITIES: that one bounds concurrent
// connections, this one bounds total non-revoked rows. A farm of 2000
// intermittently-connected machines with 800 online at a time stays under the
// connection cap and exceeds this ceiling legitimately. Such an operator should
// set this explicitly rather than inherit a number derived from a different
// quantity; 0 disables it.
//
// THE BOUND IS APPROXIMATE AND THE ARITHMETIC IS STATED RATHER THAN IMPLIED. Two
// concurrent auto-enrolls at n == ceiling-1 both pass the check under
// read-committed isolation and both insert, so the true bound is
// ceiling + RELAY_GRPC_MAX_CONNS. Making it exact needs serializable isolation or
// an advisory lock on a hot path, for an overshoot that is a fraction of a
// percent. Do not claim an exact cap anywhere.
const DefaultAutoEnrollWorkerCeiling = 1024

// txBeginner is the subset of *pgxpool.Pool this package uses: the single method
// pgx.BeginTxFunc requires of its second argument, which is itself declared as an
// anonymous interface (pgx/tx.go). Handler.pool is typed as this rather than as
// the concrete pool for the same reason internal/scheduler narrowed
// failClaimedStore - it is what makes finishRegister's SUCCESS path drivable by a
// fake, without Postgres, and therefore in the lane CI actually runs.
//
// THREE CALL SITES SHARE IT, not one: enrollAndRegister, autoEnrollAndRegister
// and applyInventory all open their transaction with the identical expression
// pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, ...) and differ only in the
// closure they pass.
//
// *pgxpool.Pool satisfies it, so cmd/relay-server's wiring is unchanged and
// production behaviour is identical. The field keeps the name `pool` because in
// production it still is one; this comment carries the type's real meaning.
type txBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// Handler implements relayv1.AgentServiceServer.
type Handler struct {
	relayv1.UnimplementedAgentServiceServer
	q               *store.Queries
	pool            txBeginner
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

	// RegistrationTimeout bounds how long a peer that has opened a stream may go
	// without sending its RegisterRequest. Non-positive means
	// DefaultRegistrationTimeout. Set by cmd/relay-server after construction,
	// from RELAY_GRPC_REGISTRATION_TIMEOUT. Read-only after startup.
	RegistrationTimeout time.Duration

	// TrailingLogWindow bounds how long after a task's finished_at its assignee
	// may still append log chunks. Non-positive means DefaultTrailingLogWindow,
	// which is what keeps every existing NewHandler/NewHandlerWithGrace call site
	// correct with no edit and lets a test narrow the window to prove the wiring.
	// Set by cmd/relay-server after construction, from
	// RELAY_TASKLOG_TRAILING_WINDOW. Read-only after startup.
	TrailingLogWindow time.Duration

	// AutoEnrollWorkerCeiling bounds non-revoked workers on the auto-enroll path
	// only. Set by cmd/relay-server after construction, from
	// RELAY_AUTO_ENROLL_WORKER_CEILING. Read-only after startup.
	//
	// A *int, NOT AN int, AND THAT DIFFERS DELIBERATELY FROM RegistrationTimeout
	// AND TrailingLogWindow. Those two resolve "non-positive means the default",
	// which works because zero is meaningless for them. THIS KNOB HAS A MEANINGFUL
	// ZERO - disabled - so an int would leave the zero value ambiguous between
	// "unset, use the default" and "explicitly disabled", and only
	// cmd/relay-server could express the difference. nil means the default; a
	// non-nil 0 means DISABLED; a negative value means the default (the parser
	// warns).
	AutoEnrollWorkerCeiling *int

	// ingestDrops counts what this server's per-connection log budgets dropped,
	// split by kind and by arm. A VALUE, not a pointer: the zero value is ready
	// to use, so a Handler built by any route (including a bare &Handler{} in a
	// test) has working counters and there is no nil case anywhere. Read through
	// IngestLogDropCounts; wired to GET /v1/server/counters by
	// cmd/relay-server's buildHTTPServer.
	//
	// It contains atomics, which makes Handler non-copyable - go vet's copylocks
	// check will say so at any `*h` copy. That is a feature: nothing should ever
	// copy a Handler.
	ingestDrops ingestLogCounters

	// taskLogFenceRejects counts chunks handleTaskLog dropped because
	// AppendTaskLog's fence returned pgx.ErrNoRows. A VALUE, not a pointer, for
	// the same reason ingestDrops is: the zero value works, so a bare &Handler{}
	// in a test has a working counter and there is no nil case anywhere. Read
	// through TaskLogFenceRejections; wired to GET /v1/server/counters by
	// cmd/relay-server's buildHTTPServer under its OWN section and its OWN
	// CounterSources field.
	//
	// A DIFFERENT NOUN FROM ingestDrops, and neither number covers any part of
	// the other. ingestDrops counts LOG LINES THE BUDGET DROPPED; this counts
	// CHUNKS THE FENCE REJECTED, on an arm that never consults the budget at all.
	// No input moves both. Do not sum them and do not merge the sections.
	//
	// NOT IN metrics.Store, AND THE REASON IS MECHANICAL RATHER THAN STYLISTIC.
	// That type's Append is a no-op for an untracked worker, and its Clear
	// DELETES the whole entry when a worker goes offline (internal/metrics, called
	// from this file's teardown), so a cumulative rejection counter parked there
	// is destroyed by the very disconnect that produced the rejections: a worker
	// that floods and then drops leaves zero behind. The Metrics WIRING pattern -
	// an exported thing main sets, nil-checked at every use - is the right
	// precedent and is what api.CounterSources uses. metrics.Store is the wrong
	// HOME and must not gain a counter method.
	taskLogFenceRejects atomic.Uint64

	// statusFence counts the rejections handleTaskStatus's two epoch-fenced
	// writes produced, split by what the row said when GetTask read it. A VALUE,
	// not a pointer, for the same reason ingestDrops and taskLogFenceRejects are:
	// the zero value works, so a bare &Handler{} in a test has working counters
	// and there is no nil case anywhere. Read through TaskStatusFenceRejections;
	// wired to GET /v1/server/counters by cmd/relay-server's buildHTTPServer
	// under its OWN section and its OWN CounterSources field.
	//
	// A THIRD DISTINCT NOUN. ingestDrops counts LOG LINES THE BUDGET DROPPED;
	// taskLogFenceRejects counts LOG CHUNKS AppendTaskLog's fence rejected; this
	// counts STATUS REPORTS the status fence rejected. No input moves more than
	// one of the three. Do not sum them and do not merge the sections.
	statusFence statusFenceCounters

	// autoEnrollRefusals counts what the two enrollment guards refused, split by
	// cause. A VALUE, not a pointer, for the same reason its three neighbours are.
	//
	// A FOURTH DISTINCT NOUN, and no input moves more than one of the four. Read
	// through AutoEnrollRefusals. NOT YET ON GET /v1/server/counters - the section
	// is deliberately deferred to its own item; see the plan's scope decision.
	autoEnrollRefusals autoEnrollRefusalCounters
}

// IngestLogDropCounts reports what this server's ingest log budget has dropped
// since process start, split by kind and by arm.
//
// It satisfies api.IngestLogBudgetSource. The numbers are per PROCESS - there is
// one Handler per server - and are never sent to an agent: the only read path is
// the admin-authenticated GET /v1/server/counters.
func (h *Handler) IngestLogDropCounts() IngestLogDrops { return h.ingestDrops.snapshot() }

// TaskLogFenceRejections reports how many task-log chunks this server's
// AppendTaskLog fence has rejected since process start, across every worker and
// all FOUR rejection reasons, including the one that reads as a lookup rather
// than a predicate: a well-formed uuid naming no task is refused by the same
// statement and lands in this number too.
//
// It satisfies api.TaskLogFenceSource. ONE NUMBER, AND THE REASON IS NOT
// AVAILABLE - see the pgx.ErrNoRows arm in handleTaskLog for why, and do not add
// a second query to find out. Per PROCESS, monotonic, zeroed by a restart, and
// never returned to an agent: the only read path is the admin-authenticated
// GET /v1/server/counters.
func (h *Handler) TaskLogFenceRejections() uint64 { return h.taskLogFenceRejects.Load() }

// AutoEnrollRefusals reports what this server's enrollment guards have refused
// since process start, split by cause. Per PROCESS, monotonic, and never returned
// to an agent.
func (h *Handler) AutoEnrollRefusals() AutoEnrollRefusalCounts { return h.autoEnrollRefusals.snapshot() }

// TaskStatusFenceRejections reports what handleTaskStatus's two epoch-fenced
// writes have refused since process start, split by what the row said at T0.
//
// It satisfies api.TaskStatusFenceSource. NOTE THE NEIGHBOUR: TaskLogFence
// Rejections is one letter away in the middle and returns a uint64, so a crossed
// wiring is a compile error rather than a silently wrong section. Per PROCESS,
// monotonic, zeroed by a restart, and never returned to an agent.
func (h *Handler) TaskStatusFenceRejections() TaskStatusFenceCounts {
	return h.statusFence.snapshot()
}

// NewHandler returns a Handler wired to the given dependencies. pool is a
// txBeginner, which *pgxpool.Pool satisfies; see that type for why.
func NewHandler(q *store.Queries, pool txBeginner, r *Registry, b *events.Broker, triggerDispatch func()) *Handler {
	return &Handler{q: q, pool: pool, registry: r, broker: b, triggerDispatch: triggerDispatch}
}

// NewHandlerWithGrace is like NewHandler but also wires in a GraceRegistry so
// that agent disconnects start a grace timer instead of immediately requeueing.
func NewHandlerWithGrace(q *store.Queries, pool txBeginner, r *Registry, b *events.Broker, triggerDispatch func(), g *GraceRegistry) *Handler {
	return &Handler{q: q, pool: pool, registry: r, broker: b, triggerDispatch: triggerDispatch, grace: g}
}

// Connect implements relayv1.AgentServiceServer.
func (h *Handler) Connect(stream relayv1.AgentService_ConnectServer) error {
	ctx := stream.Context()

	first, err := h.recvRegistration(stream)
	if err != nil {
		return err
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
	//
	// IT COVERS EVERYTHING BELOW AND NOTHING ABOVE, which is the half this
	// comment used to leave unsaid and which cost a HIGH-severity strand.
	// finishRegister acquires the worker's generation - status 'online', a bumped
	// connection_epoch, a cancelled grace timer - several statements before it
	// returns the sender this defer needs, and two of its own statements can
	// still fail after that acquisition. Those are released by finishRegister's
	// OWN deferred release (see its handedOff block), not here, because this
	// defer cannot be armed without a sender that a failed registration never
	// creates. Between the two, every path that acquires the generation ATTEMPTS
	// a release exactly once - and the attempt is authoritative only where the
	// epoch fence is actually evaluated. When the write itself errors the fence
	// answers nothing, so releaseWorkerGeneration proceeds on its own initiative;
	// see its doc comment for why that is the safe direction.
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
	//
	// ONE THING DOES POINT OUT OF THIS FRAME, and it is not the budget: the
	// limiter carries a pointer to the Handler's drop COUNTERS, which are shared
	// by every connection on purpose, because a count that died with the
	// connection would read zero exactly when an operator went looking for it.
	// The budget stays private; the counters are process-wide and atomic. Do not
	// merge the two.
	lim := newIngestLogLimiter(&h.ingestDrops)

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

// recvRegistration is the FIRST stream.Recv, bounded by registrationTimeout. See
// DefaultRegistrationTimeout for why an unbounded one is a denial primitive.
//
// ONLY THE FIRST Recv IS BOUNDED. The message loop's Recv must stay unbounded
// forever: a healthy agent holds one silent stream for hours between dispatches,
// and a deadline there would disconnect the entire fleet on a timer. That is
// what makes this a separate function called exactly once rather than a wrapper
// around Recv.
//
// THAT PARAGRAPH USED TO BE THE ONLY THING ENFORCING IT. Replacing the message
// loop's stream.Recv() with h.recvRegistration(stream) - one token, three lines
// below a comment saying not to - compiled and left `go test ./internal/worker`
// entirely green, while cutting every healthy agent at 30s of stream silence,
// fleet-wide and permanently. The wrapped error means `err == io.EOF` stops
// matching too, and nothing objected to that either.
// TestHandler_MessageLoopRecvIsNotBoundedByTheRegistrationDeadline is the check
// behind the principle now, and it is RED under exactly that edit. It lives in
// the integration lane because Connect only reaches the message loop after
// authenticateAndRegister, and every credential path there goes to the store.
//
// The Recv runs in a goroutine because there is no other way to bound it -
// grpc-go's ServerStream takes its deadline from the stream context, which the
// client controls. The channel is buffered so that goroutine can NEVER block
// after this function has returned: it stays parked in Recv until grpc tears the
// stream down (which returning is what causes), then sends and exits. Exactly
// one goroutine calls Recv at any time - this one before we return, the message
// loop only after a successful return - so the "one Recv caller at a time"
// contract holds.
func (h *Handler) recvRegistration(stream relayv1.AgentService_ConnectServer) (*relayv1.AgentMessage, error) {
	type recvResult struct {
		msg *relayv1.AgentMessage
		err error
	}
	ch := make(chan recvResult, 1)
	go func() {
		msg, err := stream.Recv()
		ch <- recvResult{msg, err}
	}()

	timer := time.NewTimer(h.registrationTimeout())
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("recv first message: %w", r.err)
		}
		return r.msg, nil
	case <-timer.C:
		// Deliberately NOT logged. This is reachable by any unauthenticated peer
		// that can open a TCP connection, so a line here would be a new
		// attacker-driven log site inside a control that exists to bound
		// attacker-driven resource use. The refusal summary in cmd/relay-server
		// is where admission pressure is surfaced, in counts.
		return nil, status.Errorf(codes.DeadlineExceeded,
			"no RegisterRequest within %s", h.registrationTimeout())
	}
}

// registrationTimeout resolves the effective bound. Non-positive means the
// default, which keeps every NewHandler/NewHandlerWithGrace call site correct
// with no edit, exactly as TrailingLogWindow does. There is deliberately no
// "disabled" value: the only fail-aggressive direction is a window too SHORT,
// whose remedy is to raise it, and unlike the connection caps this cannot be
// delegated to a proxy - no proxy can enforce "send a RegisterRequest within N"
// on the server's behalf. An operator who wants the old behaviour writes a very
// large duration and can see in the startup line that they did.
func (h *Handler) registrationTimeout() time.Duration {
	if h.RegistrationTimeout > 0 {
		return h.RegistrationTimeout
	}
	return DefaultRegistrationTimeout
}

// autoEnrollWorkerCeiling resolves the effective ceiling. 0 is a real answer
// here and means disabled - do not "simplify" this to the non-positive-means-
// default rule its two neighbours use.
func (h *Handler) autoEnrollWorkerCeiling() int {
	if h.AutoEnrollWorkerCeiling == nil || *h.AutoEnrollWorkerCeiling < 0 {
		return DefaultAutoEnrollWorkerCeiling
	}
	return *h.AutoEnrollWorkerCeiling
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

// enrollAndRegister handles enrollment using an admin-issued enrollment token.
// All DB writes (the FOR UPDATE worker lookup, the worker upsert, the enrollment
// consume, the agent-token set) execute inside a single transaction, so a failure
// anywhere leaves no partial state - either the agent is fully enrolled or not at
// all.
//
// IT REFUSES A HOSTNAME WHOSE WORKER HOLDS A LIVE CREDENTIAL and still binds to
// one whose credential is NULL. The lookup is INSIDE the transaction and holds
// FOR UPDATE, which is what makes the check and the upsert one atomic decision
// for an existing row. Rotating a LIVE agent's credential therefore requires a
// revoke first - same rule, same remedy as the auto-enroll path.
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

		// FOR UPDATE, INSIDE THE SAME TRANSACTION AS THE UPSERT. The lock is what
		// makes this non-racy for the case that matters, an existing row. For a
		// FRESH hostname it locks nothing, so two admin-issued tokens racing on one
		// brand-new hostname still resolve to one row via ON CONFLICT DO UPDATE -
		// out of the threat model, and disclosed rather than closed.
		//
		// LOCK ORDERING: this transaction takes a workers row lock and then updates
		// an agent_enrollments row. Re-check before adding a caller - at time of
		// writing ConsumeAgentEnrollment has no other caller, so no transaction
		// takes the two in the opposite order and there is no deadlock cycle.
		existing, err := txq.GetWorkerByHostnameForUpdate(ctx, reg.Hostname)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lookup worker: %w", err)
		}
		if err == nil && existing.AgentTokenHash != nil {
			return errCredentialLive
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
		if errors.Is(txErr, errCredentialLive) {
			h.autoEnrollRefusals.record(autoEnrollReasonCredentialLive)
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
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
// set. IT MAY CREATE A WORKER AND MAY NEVER CLAIM ONE: a single
// InsertWorkerForAutoEnroll (ON CONFLICT DO NOTHING) both creates the row and
// refuses a hostname that already has one, with no window between the check and
// the write. It then issues a fresh agent token without consuming any enrollment
// record.
//
// THE REFUSAL IS DELIBERATELY THE SAME status AND THE SAME STRING every other
// credential failure on this surface returns. The previous "worker revoked"
// message told an unauthenticated caller that a row for that hostname existed and
// was revoked - a live hostname-state oracle, and exactly the disclosure the new
// guard must not add a second instance of. The oracle that REMAINS is inherent:
// a caller learns a hostname is claimed because claiming it fails. Refusing
// everything is the only way to close that, and README says so rather than
// claiming the refusal is opaque.
func (h *Handler) autoEnrollAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest) (string, *workerSender, error) {
	rawAgent, agentHash := agentTokenGenerator()
	if rawAgent == "" || agentHash == "" {
		return "", nil, status.Errorf(codes.Internal, "token gen failed")
	}

	var workerID pgtype.UUID
	txErr := pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := h.q.WithTx(tx)

		// BEFORE THE INSERT, which is what makes the refusal free of side effects.
		if ceiling := h.autoEnrollWorkerCeiling(); ceiling > 0 {
			n, err := txq.CountWorkers(ctx)
			if err != nil {
				return fmt.Errorf("count workers: %w", err)
			}
			if n >= int64(ceiling) {
				return errFleetAtCeiling
			}
		}

		id, err := txq.InsertWorkerForAutoEnroll(ctx, store.InsertWorkerForAutoEnrollParams{
			Name:               reg.Hostname,
			Hostname:           reg.Hostname,
			CpuCores:           reg.CpuCores,
			RamGb:              reg.RamGb,
			GpuCount:           reg.GpuCount,
			GpuModel:           reg.GpuModel,
			Os:                 reg.Os,
			SupportsWorkspaces: reg.SupportsWorkspaces,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errHostnameClaimed
		}
		if err != nil {
			return fmt.Errorf("insert worker: %w", err)
		}
		workerID = id

		if err := txq.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
			ID: id, AgentTokenHash: &agentHash,
		}); err != nil {
			return fmt.Errorf("set agent token: %w", err)
		}
		return nil
	})
	if txErr != nil {
		if errors.Is(txErr, errHostnameClaimed) {
			h.autoEnrollRefusals.record(autoEnrollReasonHostnameClaimed)
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
		if errors.Is(txErr, errFleetAtCeiling) {
			h.autoEnrollRefusals.record(autoEnrollReasonFleetAtCeiling)
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
		return "", nil, txErr
	}

	// reg.Hostname is a caller-supplied proto string: validated nowhere, bounded
	// only by gRPC's 4 MiB default receive limit. %q is the injection defence and
	// clipID the volume defence, the same pair the ingest log sites use. This line
	// is the ONLY record anywhere that a token-less enrollment happened, so a
	// forgeable one corrupts the audit trail of the mechanism it documents - the
	// RELAY_ALLOW_AUTO_ENROLL gate is not a substitute. It is bounded to one line
	// per STREAM by registration itself - NOT per connection, which is a weaker
	// claim than it sounds: grpc.MaxConcurrentStreams bounds CONCURRENT streams,
	// not how many a connection may open over its life, so a caller that opens
	// and closes streams in a loop emits one of these per cycle. What prices that
	// loop is RELAY_GRPC_REGISTRATION_TIMEOUT plus the connection caps, not this
	// line, which is why it still takes no budget key.
	//
	// AND THE ASYMMETRY WITH THE REFUSAL BELOW IT IS THE ARGUMENT FOR COUNTING
	// RATHER THAN LOGGING. A SUCCESSFUL token-less enrollment is now one line per
	// hostname FOREVER - the hostname can never be auto-enrolled again, because
	// the row it just created refuses the next attempt. A REFUSAL is unboundedly
	// repeatable by the same caller with the same hostname, so it takes a counter
	// and no log site at all (see AutoEnrollRefusals).
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

	// THE GENERATION IS ACQUIRED, SO ITS RELEASE IS ARMED HERE AND NOT ONE
	// STATEMENT LATER. RegisterWorkerConnection above has already flipped the row
	// to 'online', bumped connection_epoch and cleared disconnected_at, and the
	// grace.Cancel below is about to throw away the pending requeue from the
	// PREVIOUS disconnect. Everything after this point can still fail -
	// reconcileRunningTasks returns an error, and so does the RegisterResponse
	// send - and until this defer existed those two returns left the worker
	// 'online' at a live epoch with no connection behind it and no timer to clean
	// up after it.
	//
	// CONNECT'S OWN DEFER CANNOT COVER THIS AND NEVER COULD. It is armed only
	// after this function RETURNS, and it takes the *workerSender that only the
	// success path below creates - so on a failed registration there is nothing
	// to arm it with. The two defers partition the window rather than overlapping
	// it.
	//
	// NOTHING ELSE CATCHES THE GAP EITHER, which is why this is a defer and not a
	// backlog note. The metrics liveness sweeper skips any worker Metrics has not
	// been told to track, and Metrics.Activate is below the failure points; the
	// stale-task watchdog marks tasks timed_out at RELAY_TASK_MAX_ASSIGNMENT
	// (24h) rather than requeueing them, and never writes workers.status at all.
	//
	// This is CLAUDE.md's "End the generation before releasing the resource" read
	// in the acquire direction: take the state and arm its release in the same
	// breath, so no future early return can be added that forgets to. handedOff
	// is flipped at exactly ONE place, so the two releases are mutually exclusive
	// by construction and neither can be skipped.
	//
	// BOTH HALVES OF THAT SENTENCE ARE CHECKED, NOT MERELY ASSERTED, which is the
	// only reason it is worth writing. handoffFlagIdent in
	// handler_handoff_guard_test.go requires this defer to be an unconditional
	// statement of finishRegister's body (so it is armed on every path) and
	// requires the closure to be nothing but this decision - exactly one call to
	// releaseWorkerGeneration, on the not-handed-off branch, with no else arm and
	// no other statement. Each of those clauses was added because a rewrite that
	// broke it left this whole package green.
	handedOff := false
	defer func() {
		if !handedOff {
			h.releaseWorkerGeneration(workerID, updated.ConnectionEpoch)
		}
	}()

	// Agent reconnected within its grace window - stop the requeue timer. THIS IS
	// THE SECOND HALF OF THE ACQUISITION: the cancelled timer is not recoverable
	// (GraceRegistry.Cancel stops it and deletes the entry), so a failure below
	// has to arm a FRESH one at the epoch RegisterWorkerConnection just created.
	// Restoring the OLD epoch's timer would be a silent no-op -
	// RequeueWorkerTasksIfEpoch fences on workers.connection_epoch and the row has
	// moved on.
	//
	// ARMING THE RELEASE ABOVE THIS RATHER THAN BELOW IT IS DEFENSIVE, NOT
	// REQUIRED - and saying which it is matters, because the opposite claim
	// invites someone to rely on it. GraceRegistry.Cancel cannot fail and cannot
	// return early, so moving the flag and its defer below this block is
	// behaviour-preserving today and mutation confirms it. Keep the order anyway:
	// the rule that survives is "arm the release in the same breath as the
	// acquisition", and it stops being merely defensive the moment anything
	// fallible is added here.
	//
	// THIS COVERS THE SINGLE-SHOT CASE. An agent that crash-loops faster than
	// RELAY_WORKER_GRACE_WINDOW gets a Cancel and a fresh Start every cycle, so
	// the requeue is pushed out indefinitely and its tasks sit until the 24h
	// stale-task watchdog fails them as timed_out. That is not a regression - the
	// same was true before any of this existed, and worse, because nothing armed
	// a fresh timer at all - but it is the limit of what this release buys.
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

	// OWNERSHIP HANDOFF. From this instant the release belongs to Connect's
	// `defer h.teardownConnection(workerID, sender)`, which Connect arms the
	// moment this function returns a nil error.
	//
	// THE CONSTRAINT IS A RANGE, NOT A POINT. The semantic requirement is only
	// this: after the send has succeeded, and before this function returns.
	// Earlier than that reopens the strand for the RegisterResponse send. LATER
	// cannot break it at all - the deferred closure reads the flag after the
	// return value has been evaluated - so every position inside the range is
	// semantically identical, the two flanking registry.Register included, and
	// mutation confirms it.
	//
	// It is shipped at the TIGHTEST point in the range, immediately after the
	// sender becomes reachable, and
	// TestFinishRegisterHandsOffOwnershipInsideTheWindow pins that exact position
	// - the statement immediately following h.registry.Register - rather than the
	// looser semantic range.
	//
	// WHAT FORBIDDING THE DRIFT BUYS is not tidiness. Any statement that comes to
	// sit between h.registry.Register and this flip runs with handedOff still
	// false, so if it fails, the deferred release above ends the DB generation
	// correctly - and the sender published one line up stays in the registry with
	// nothing to remove it, because Connect arms `defer h.teardownConnection`
	// only on a nil error. Such a statement would be covered for the generation
	// and uncovered for the registry, and the "EVERYTHING BELOW THIS LINE" rule
	// below cannot see it: that rule is about positions below the flip.
	//
	// TWO THINGS TOGETHER MAKE THE TWO RULES MEET WITH NOTHING IN BETWEEN, and
	// adjacency is only one of them. The other is that h.registry.Register stays a
	// statement of finishRegister's own body. Nest it in a compound statement -
	// a bare block, an `if`/`switch` init, or the plausible `if h.registry != nil {
	// ... }` - and the flip is still the next BODY statement while any number of
	// fallible statements sit inside the wrapper between the two, in exactly the
	// unguarded region described above. All four shapes were measured passing the
	// guard, `go vet` and the whole package before the guard grew the clause that
	// requires the indexed statement to BE the call rather than merely contain it.
	// The guard lives in the default lane, and what it covers there has narrowed.
	// It is now the half no runtime test can observe: source position, the
	// deferred closure's SHAPE (the clauses described at the defer that arms the
	// release, earlier in this function),
	// and the flag's write set. The behavioural
	// half is TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration,
	// which drives this whole function to a successful return without Postgres and
	// asserts that the generation is released exactly once across the connection's
	// life. It exists because Handler.pool is a txBeginner rather than a
	// *pgxpool.Pool; before that seam no default-lane test could get past
	// applyInventory.
	//
	// THE CLOSURE-SHAPE CLAUSES ARE THE ONES STILL DOING THE WORK, and saying so
	// matters because "source position" sounds like the whole story and is not.
	// `if h.AllowAutoEnroll { return }` inserted ahead of the release is killed by
	// those clauses and by nothing else, in either lane.
	//
	// EVERYTHING BELOW THIS LINE MUST STAY INFALLIBLE. Connect arms its defer only
	// on a nil error, so a future statement here that returns an error would be
	// covered by neither release and would additionally strand a live sender in
	// the registry. If such a statement is ever needed, it must log and continue
	// (as applyInventory does), not return.
	// TestFinishRegisterHandsOffOwnershipInsideTheWindow is what enforces it: it
	// fails on any return positioned below the flip whose last result is not the
	// predeclared nil.
	//
	// THAT CHECK REACHES ERROR RETURNS AND NOTHING ELSE. A panic below this line
	// escapes the same way: this function's own deferred release has already been
	// waived, and Connect's teardown defer is not armed until this function
	// returns - so the sender stays in the registry and the generation stays
	// unreleased while the goroutine unwinds.
	//
	// NOTHING BELOW PANICS AS THIS PACKAGE'S CONSTRUCTORS BUILD A HANDLER, which
	// is a narrower claim than "nothing below can panic". Metrics is nil-checked,
	// and NewHandler and NewHandlerWithGrace both supply broker and
	// triggerDispatch. A handler built as a bare &Handler{} - which the tests in
	// this package do routinely - leaves both nil, and both then panic on a nil
	// dereference (measured, not assumed). Keep the nil-checked style if either
	// ever becomes optional.
	//
	// AND THE UNWINDING ANALYSIS ABOVE DOES NOT COVER `go h.triggerDispatch()`:
	// that panic happens on a NEW goroutine, where none of this function's defers
	// exist at all. Either way the process dies - there is no recover() and no
	// gRPC recovery interceptor anywhere in this tree - so the sender in the
	// registry and the unreleased generation go with it. What survives a crash is
	// the durable half: the workers row RegisterWorkerConnection set to 'online'
	// at a live connection_epoch, with no connection behind it and no grace timer
	// armed. Nothing at startup clears it: releaseWorkerGeneration is the only
	// caller of MarkWorkerOfflineIfEpoch in the tree, and the startup grace
	// seeding requeues that worker's TASKS without ever writing workers.status.
	// The row is corrected when that agent next registers, and not before.
	handedOff = true

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
	//
	// BOTH FENCES ARE ALREADY IN HAND HERE, which is why the statement can demand
	// them. serverSet's VALUE is the assignment_epoch GetActiveTasksForWorker read
	// under the same snapshot as the id, and workerID is this connection's own
	// authenticated worker, resolved at registration and never taken from the
	// wire. Passing them is what stops a reconcile walking a STALE snapshot from
	// tearing a task off the worker it was re-dispatched to in the meantime - see
	// the statement's own comment in query/tasks.sql.
	//
	// The int32 conversion is lossless: serverSet widened tasks.assignment_epoch
	// (int32) to int64 above only so the reported-task loop can compare it against
	// proto's int64 RunningTask.Epoch.
	requeued := 0
	for taskIDStr, srvEpoch := range serverSet {
		if agentSet[taskIDStr] {
			continue
		}
		var tID pgtype.UUID
		if err := tID.Scan(taskIDStr); err != nil {
			continue
		}
		// n counts MATCHES, not attempts. Zero is normal and CORRECT post-fence: it
		// means another writer ended this assignment first. Counting matches loses
		// no legitimate wake - see the gate below for why, which is NOT the
		// "whoever did that already woke the dispatcher" argument this comment used
		// to make: that is true for a grace-path release but false when n is 0
		// because the statement ERRORED, which the next paragraph relies on.
		//
		// THE ERROR IS DROPPED ON PURPOSE, exactly as it was before this fence
		// existed. This runs inside finishRegister, BEFORE Connect allocates this
		// connection's ingestLogLimiter, so the site has no log budget at all -
		// the same rule as the unparseable-id branch above. n is 0 on error, so a
		// failed statement can neither inflate the count nor fake a dispatch wake.
		n, _ := h.q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
			ID:              tID,
			AssignmentEpoch: int32(srvEpoch),
			WorkerID:        workerID,
		})
		requeued += int(n)
	}

	// Wake the scheduler so requeued tasks are dispatched immediately.
	//
	// THIS GATE IS NEARLY ALWAYS REDUNDANT, and knowing why is what makes
	// counting matches instead of attempts safe. finishRegister fires
	// `go h.triggerDispatch()` UNCONDITIONALLY once registration completes, so
	// every path that reaches reconcile and then finishes registering gets a wake
	// regardless of what this gate decides. The gate is load-bearing on exactly
	// one path: the RegisterResponse send fails and finishRegister returns early,
	// never reaching its own trigger. On that path zero matches means zero rows
	// were actually requeued, so there is nothing to wake for - which is precisely
	// why switching from attempts to matches cannot drop a needed wake, including
	// when n is 0 because the statement errored.
	//
	// THAT PATH IS NO LONGER A DEAD END, and the gate's job on it narrowed
	// accordingly. finishRegister now releases the generation it acquired when it
	// returns early (see its handedOff defer), and that release ends in either
	// grace.Start - whose expiry calls dispatcher.Trigger in cmd/relay-server -
	// or requeueWorkerTasks, which triggers dispatch itself. So on that path a
	// wake now arrives eventually even with this gate deleted, EXCEPT when the
	// release's epoch fence is evaluated and rejects us: a superseded release
	// returns before arming anything. That exception is not a hole - a fresher
	// connection owning this worker will do its own dispatch trigger - but it is
	// why the sentence needs the qualifier. What the gate buys on the non-excepted
	// path is PROMPTNESS for the rows this loop moved to pending here and now,
	// which would otherwise wait out RELAY_WORKER_GRACE_WINDOW (2m by default).
	// Keep it, and keep it counting matches rather than attempts: the paragraph
	// above is still the reason that switch is safe.
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
// lim is this connection's log budget, allocated once in Connect. FIVE log lines
// in this function go through it and the split matters: the two above the gates
// (bad task id, GetTask failure) are bounded by the budget ALONE, because a gate
// cannot protect a line placed before it; the three below (retry write, status
// write, dependency cascade) are additionally behind the identity and currency
// gates, so reaching them costs a valid assignment. All five carry NO wire value
// in their dedupe key - each is exactly one key for the connection's whole life,
// re-armed by ingestLogDedupeWindow. See
// docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md.
//
// TWO OF THIS FUNCTION'S ARMS ARE COUNTED RATHER THAN LOGGED. Both epoch-fenced
// writes below drop pgx.ErrNoRows silently and record it in h.statusFence, which
// GET /v1/server/counters publishes as task_status_fence. That arm and the log
// budget are DISJOINT: no input moves both, and neither number covers any part
// of the other.
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
	// it and the observable TASK STATE is unchanged - but not everything
	// observable is task state; see FOURTH below, which is the half that is now
	// pinned by a test. A forged terminal from a
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
	// It does NOT save a log line. Both write-error branches below put the
	// pgx.ErrNoRows case in its own arm of an `if errors.Is(...) / else if
	// lim.allow(...)` pair, so a forged message rejected by either fence logs
	// nothing at all - delete this gate and the log volume is
	// unchanged. Nor does this function have a "zero attacker-keyed log lines"
	// property to protect: the two branches at the top of this function still
	// run AHEAD of this gate, and a gate cannot protect a line placed before
	// it. What bounds them now is the CONNECTION'S BUDGET (ingestLogLimiter),
	// which is keyed on nothing the caller supplies - the GetTask branch's
	// pgx.ErrNoRows case is silent outright, and everything else there costs at
	// most one line per STREAM - the limiter is allocated per Connect call, and
	// a connection may open streams over its life without limit.
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
	// FOURTH, AND NEW SINCE task_status_fence SHIPPED: this gate is what makes
	// the fence counters ATTRIBUTABLE. A non-assignee's forged report is dropped
	// here, one round trip before either write, so it never reaches a counter -
	// which is what stops a registered peer inflating conflicting_total by
	// naming tasks it does not own. Deleting the gate would leave the observable
	// TASK STATE unchanged (both statements reject on their own worker_id
	// predicate) and would make the counters peer-drivable noise. The numbers
	// are still not peer-KEYED - the cardinality rule holds either way - but a
	// signal an unrelated agent can move is not the signal an operator is
	// reading.
	//
	// THIS IS THE ONE JOB THAT IS PINNED, and it shipped unguarded: measured,
	// deleting the gate left internal/worker, internal/api and cmd/relay-server
	// all green while a non-assignee drove Conflicting to 1000 in 1000 messages.
	// TestHandleTaskStatus_OnlyTheAssigneeMovesTheFenceCounters is that probe,
	// with a same-handler positive control so a handler mutated into rejecting
	// everything cannot satisfy it. Note what it does NOT establish: the gate
	// proves the sender is the assignee, never that the report is HONEST, so the
	// assignee's own forged conflicts are still free. That exposure is
	// documented rather than closed - see TaskStatusFenceCounts and the README's
	// task_status_fence bullets.
	//
	// Keep all three terms. Against a real, non-zero worker UUID the two .Valid
	// checks are mutually redundant with the Bytes comparison, and !workerID.Valid
	// is unreachable from Connect, which closes the stream on a Scan failure
	// rather than calling in with a zero value. What they buy is NULL rejection:
	// pgtype.UUID is a comparable struct, so with BOTH .Valid checks dropped a
	// zero-value workerID (a caller that lost its identity) compares EQUAL to a
	// never-claimed task's NULL worker_id - the Go form of SQL's
	// IS NOT DISTINCT FROM - and the gate fails OPEN. Removing either one alone
	// leaves the hole closed; removing both opens it - each of those three
	// variants was run, not reasoned about. That is defense in depth against a
	// future caller.
	//
	// The NULL-rejection half now has its own guard, which it did not when the
	// counters shipped:
	// TestHandleTaskStatus_AZeroValueWorkerCannotMoveTheCountersOnANeverClaimedTask
	// drives a never-claimed task from a zero-value worker id and requires the
	// counters to stay flat. It discriminates the pair, not either term alone,
	// which is exactly the shape of the hole. What it guards is the COUNTER, not
	// the row: the older
	// TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask
	// was written as this gate's permanent guard and stays green with the whole
	// gate deleted, because IncrementTaskRetryCount gained its own worker_id
	// predicate - measured, not assumed. Same rule the SQL states as "a plain =,
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
			// TWO ARMS, MUTUALLY EXCLUSIVE BY CONSTRUCTION, and written as
			// if/else so that no future edit can make both fire. They are the
			// subjects of two separate backlog items and neither number covers
			// any part of the other.
			if errors.Is(err, pgx.ErrNoRows) {
				// The fence rejecting, not a failure: the task finished, was
				// cancelled, or the generation ended between the GetTask above
				// and here. COUNTED, NEVER LOGGED - a line here would be
				// caller-driven volume on the recv goroutine and would fire on
				// the legitimate duplicate-terminal case. The reason is
				// classified from the row this handler already read; see
				// classifyStatusFenceRejection for what that can and cannot
				// establish.
				h.statusFence.record(classifyStatusFenceRejection(task.Status, statusStr))
			} else if lim.allow(logKey{kind: kindStatusRetryWrite}) {
				// Real infrastructure - a serialization failure, a statement
				// timeout, a connection reset - and now under the connection's
				// budget. It runs on the recv goroutine at whatever rate the
				// agent chooses to send, and the read above (GetTask) can
				// succeed while this write fails, so nothing else bounds it.
				// NO WIRE VALUE IN THE KEY: such an episode is not per-task, and
				// keying on the task id would multiply one infra event by the
				// task count - the same argument the GetTask site above makes.
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
		if errors.Is(err, pgx.ErrNoRows) {
			// The fence rejecting, not a failure: the row is already terminal (a
			// duplicate terminal message, or a task the coordinator's stale-task
			// watchdog already ended), or the generation ended between the
			// GetTask above and here. COUNTED, NEVER LOGGED, for the reason at
			// the retry arm above.
			//
			// NOTE WHICH PREDICATE ACTUALLY BITES HERE, because the obvious
			// reading is wrong: the watchdog does NOT bump assignment_epoch -
			// UpdateTaskStatus writes only status, started_at and finished_at -
			// so a swept task's own agent still passes BOTH Go gates and is
			// refused by the terminality predicate, at the same epoch. That is
			// why the reasons split on the ROW'S STATUS rather than on the
			// statement.
			h.statusFence.record(classifyStatusFenceRejection(task.Status, statusStr))
		} else if lim.allow(logKey{kind: kindStatusUpdateWrite}) {
			log.Printf("worker: handleTaskStatus UpdateTaskStatus %s -> %s: %v", uuidStr(taskID), statusStr, err)
		}
		return
	}

	if terminal {
		// UNDER THE CONNECTION'S BUDGET, and NOT gated on pgx.ErrNoRows: this is
		// an :exec, so ErrNoRows is not a shape it can return, and adding an
		// errors.Is here would be cargo-culted from the two arms above. It is
		// also NOT a fence-rejection site in task_status_fence's sense -
		// FailDependentTasks satisfies the epoch fence with a terminal-only
		// `WHERE status = 'pending'` predicate and yields no rowcount to inspect
		// (see the partition comment in internal/scheduler/dispatch.go).
		//
		// Reached only AFTER a successful UpdateTaskStatus, which is exactly the
		// condition the sibling item's Repro names: the read succeeds and the
		// WRITE fails, so the budgeted GetTask line above never spends a token
		// and nothing else bounds this one.
		if err := h.q.FailDependentTasks(ctx, taskID); err != nil &&
			lim.allow(logKey{kind: kindStatusFailDependents}) {
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
			// pgx.ErrNoRows means the fence rejected the chunk, for any of FOUR
			// independent reasons, and the count is FOUR rather than three because
			// the statement's WHERE has four conjuncts: `t.id = task_id` is one of
			// them, and it is easy to read past as a lookup. A well-formed uuid
			// naming no task at all lands here while being none of the other three
			// (TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers
			// drives exactly that), and it belongs with the first of the three
			// below - an unwelcome sender - so an operator reading rejected_total
			// still concludes correctly. The other three: the sender is not the
			// task's current assignee (a
			// forged or misrouted chunk - workerID comes from the authenticated
			// registration, never from the wire); the sender's generation is stale
			// because the task was requeued or cancelled (both bump
			// assignment_epoch); or the task finished longer ago than the trailing
			// window, which is DefaultTrailingLogWindow unless
			// RELAY_TASKLOG_TRAILING_WINDOW overrides it. THAT THIRD ONE IS THE ONE TO
			// SUSPECT FIRST WHEN OUTPUT IS MISSING RATHER THAN SPURIOUS: it is the
			// only cause that is operator-configurable, time-dependent, and triggered
			// by a perfectly legitimate sender, so a window set too small truncates
			// the tail of real task output with no other symptom anywhere. All four
			// are deliberately indistinguishable here; see the comment on
			// AppendTaskLog. Expected - drop it silently, and in
			// particular do NOT publish it: a zombie agent's output would otherwise
			// appear in a live view and then vanish on refresh, because it was
			// correctly never stored.
			//
			// THIS ARM IS DELIBERATELY SIDE-EFFECT-FREE APART FROM THE COUNT, AND
			// MUST STAY SILENT. A log line here would be caller-driven volume on
			// the recv goroutine, and it would fire on the legitimate late-flush
			// case as well as on forged chunks; a BUDGETED line is no better,
			// because it would spend a token from a six-per-minute bucket that a
			// genuine infra failure needs. Nothing here may publish. Pinned by
			// TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll, which asserts
			// the whole captured log is empty, so any wording reddens it, and by
			// its default-lane twin in tasklog_fence_counter_test.go.
			//
			// THE COUNTER IS THE OBSERVABILITY, and it is one atomic add: no
			// allocation, no lock, no map, no round trip, sitting next to a
			// Postgres round trip that already happened. The one-statement
			// constraint at the top of this function is respected in substance and
			// not merely in letter. Without this number an operator who set
			// RELAY_TASKLOG_TRAILING_WINDOW too small gets silently truncated task
			// output with NO runtime signal of any kind. Served as
			// task_log_fence.counts.rejected_total on the admin-only
			// GET /v1/server/counters. Never returned to an agent.
			//
			// IT IS ONE NUMBER AND NOT FOUR, AND THAT IS A PRICED DECISION RATHER
			// THAN AN IMPOSSIBILITY. The four cases are not recoverable from this
			// statement's RESULT: the fence is a CTE that yields no row at all when
			// any predicate fails, so there is nothing to carry a reason column on
			// (see AppendTaskLog's comment). Recovering the reason needs either a
			// SECOND query, which the top of this function forbids, or a rewrite of
			// AppendTaskLog to return a row on the rejection path - a LEFT JOIN over
			// the task row exposing the four predicates as booleans, which IS
			// expressible in one round trip. That rewrite is DECLINED, and here is
			// its price: it deletes the pgx.ErrNoRows signal that every caller,
			// every comment and every test of this fence is written against; it
			// makes AppendTaskLogRow's three columns nullable on the success path,
			// so the publish below would have to re-derive "did the insert happen"
			// from a NULL check - a new way to publish an unstored chunk, which the
			// paragraph above forbids absolutely; and it puts a rewrite of the most
			// security-sensitive statement in the repo inside an observability
			// change. Bigger and riskier than one number is worth. Do not spend an
			// afternoon rediscovering either half, and do not restate this as
			// "impossible" - it is not, it is declined.
			h.taskLogFenceRejects.Add(1)
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
	h.releaseWorkerGeneration(workerID, sender.connEpoch)
}

// releaseWorkerGeneration ends the worker generation identified by epoch: it
// marks the worker offline and then either arms the grace timer or requeues the
// worker's tasks directly. It is the ONE place shared worker state is released,
// and it has exactly two callers - teardownConnection above, when a registered
// stream ends, and finishRegister's deferred release, when a registration failed
// after RegisterWorkerConnection had already acquired the generation. Keeping
// one body is the point: those two paths must not be able to drift apart.
//
// THE EPOCH ARGUMENT IS THE OWNERSHIP CHECK, AND ON THE SECOND CALLER IT IS THE
// ONLY ONE. It is compared, inside MarkWorkerOfflineIfEpoch's WHERE clause,
// against workers.connection_epoch as it stands right now; a caller whose
// generation has been superseded by a fresher RegisterWorkerConnection matches
// zero rows and returns having touched nothing.
// TestConnect_ASupersededFailedRegistrationReleasesNothing is what holds that,
// in the default lane.
//
// A ZERO IS ONLY BELIEVED WHEN THE FENCE WAS ACTUALLY EVALUATED, which is why
// markWorkerOffline reports an error separately from a row count. The two
// failures are correlated: finishRegister's reconcile arm fails for exactly two
// reasons - a cancelled peer context or a database fault - and in the second
// case this release's own write goes to the same unhealthy pool. Reading that
// error as "a fresher connection holds the epoch" would silently re-create the
// strand this release exists to close, in one of its two trigger scenarios. So
// on an error we PROCEED: grace.Start's expiry runs RequeueWorkerTasksIfEpoch
// and requeueWorkerTasks calls it directly, both carrying their own
// workers.connection_epoch guard, so a release that was in fact superseded costs
// a fenced no-op while giving up costs a permanent strand.
//
// WHAT THE EARLY RETURN BUYS IS NARROWER THAN IT LOOKS, and GraceRegistry
// carries the rest. The fence is evaluated inside Postgres and grace.Start is
// called on the result, a round trip later with nothing held across the gap, so
// this return cannot by itself stop a delayed superseded caller from arming a
// timer against a live worker. GraceRegistry.StartWithDuration refuses a
// strictly older epoch for that reason. This return is what keeps the common,
// promptly-superseded case from touching shared state at all - including the
// h.grace == nil branch, which has no registry to refuse it.
//
// teardownConnection's registry.UnregisterIf gate is a SECOND, EARLIER check
// that this function deliberately does not duplicate and the failed-registration
// caller deliberately does not have: that caller has no sender in the registry
// to compare against - which is precisely what makes it a failed registration -
// so sender identity is unavailable there and the epoch is the whole of the
// question. Do NOT add a registry call here to "make the two paths symmetric";
// unregistering a sender this caller never registered is the clobber the
// invariant forbids.
func (h *Handler) releaseWorkerGeneration(workerID string, epoch int32) {
	if rows, err := h.markWorkerOffline(workerID, epoch); err == nil && rows == 0 {
		return // the fence was evaluated and a fresher connection holds the epoch
	}
	if h.grace != nil {
		h.grace.Start(workerID, epoch)
	} else {
		h.requeueWorkerTasks(workerID, epoch)
	}
}

// markWorkerOffline is called only from releaseWorkerGeneration, which reaches
// it from two places: a defer after a registered stream ends, and a registration
// that failed after RegisterWorkerConnection had already acquired the generation
// - in which case no stream ever carried traffic at all. It is fenced on
// connection_epoch: if a fresher connection has bumped the epoch, the write
// affects zero rows and the offline broker event / metrics-clear are skipped.
//
// Returns (rows, err). A (0, nil) means the fence WAS evaluated and rejected us;
// a non-nil error means we could not tell, and the row count that comes with it
// says nothing. Keeping those apart is the whole point of the signature: they
// are the same value and opposite conclusions, and collapsing them re-creates
// the strand releaseWorkerGeneration exists to close. The two unfenced side
// effects below - the offline broker event and Metrics.Clear - stay gated on the
// write having actually applied either way.
func (h *Handler) markWorkerOffline(workerID string, epoch int32) (int64, error) {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		// Unreachable in practice and deliberately silent: workerID is uuidStr()
		// over a UUID Postgres just RETURNed, and Connect logs loudly about the
		// same impossibility on the paths that get that far.
		//
		// IT DOES CHANGE WHAT THE CALLER DOES, and the change is deliberate: an
		// error makes releaseWorkerGeneration's `err == nil && rows == 0` false, so
		// it proceeds to grace.Start (or requeueWorkerTasks) with a workerID that
		// both continuations parse themselves and both reject the same way. The
		// residue is one grace entry that deletes itself when the window elapses.
		// Returning (0, nil) instead would skip that, at the cost of spelling an
		// unparseable id as 'the fence said no' - the exact conflation this
		// signature exists to prevent.
		return 0, err
	}
	ctx := context.Background()
	now := time.Now()
	rows, err := h.q.MarkWorkerOfflineIfEpoch(ctx, store.MarkWorkerOfflineIfEpochParams{
		ID:              id,
		LastSeenAt:      pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ConnectionEpoch: epoch,
	})
	if err != nil {
		// THIS LOG IS BOUNDED BY DATABASE HEALTH, NOT BY PEER VOLUME, which is why
		// it is allowed on a frame that has no ingest budget. Nothing here is
		// peer-drivable: workerID is uuidStr() over a RETURNING UUID, the statement
		// runs on context.Background() so a dead peer cannot cancel it into an
		// error, and no code path deletes a worker row. An agent reconnect loop
		// cannot make this line fire; only a broken pool can, and then the volume
		// is the pool's, not the fleet's.
		log.Printf("worker: could not mark %s offline at epoch %d, releasing anyway: %v", workerID, epoch, err)
		return 0, err
	}
	if rows == 0 {
		return 0, nil
	}
	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"offline"}`, workerID)),
	})
	if h.Metrics != nil {
		h.Metrics.Clear(workerID)
	}
	return rows, nil
}

// requeueWorkerTasks requeues dispatched/running tasks for a worker whose
// generation has ended - a disconnect, or a registration that acquired the
// generation and then failed - fenced on connection_epoch: if a fresher
// connection has bumped the epoch, the EXISTS guard fails and zero tasks move.
// Bumps assignment_epoch on each requeued task (task-level fence preserved).
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
