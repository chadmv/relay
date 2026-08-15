//go:build integration

package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// handlerShimLimiters gives each *Handler ONE ingestLogLimiter for the lifetime
// of the test binary, so that HandleTaskLog / HandleTaskStatus behave like a
// single agent connection and the existing call sites across four test files
// compile and behave unchanged.
//
// READ THIS BEFORE ASSERTING ON LOG-LINE COUNTS. "One Handler equals one
// connection" is TRUE in these shims and FALSE in production, where a single
// Handler serves every connection and Connect allocates one limiter per stream.
// So these shims are usable evidence for what ONE connection's budget does, and
// are NEVER evidence for isolation BETWEEN connections. That property is pinned
// by TestConnect_TwoConnectionsDoNotShareTheLogBudget, which drives two real
// Connect streams against one Handler.
//
// The earlier design for this seam allocated a throwaway limiter per call, which
// would have made these wrappers exercise no limiting at all - a test asserting
// a log-line count through them would have been green and meaningless. Do not
// go back to that.
var (
	handlerShimLimitersMu sync.Mutex
	handlerShimLimiters   = map[*Handler]*ingestLogLimiter{}
)

// THE MUTEX PROTECTS THE MAP ONLY. It does NOT protect the *ingestLogLimiter it
// returns: the caller mutates that pointer unlocked, which is the shape the "no
// interior pointers across locks" invariant warns about. Safe today only because
// nothing in this package calls t.Parallel, so the shims are never driven
// concurrently - if that changes, this is the finding, not the missing lock. The
// production allocation site has no such seam: it is a stack local in Connect,
// reached from one goroutine.
//
// The map is also never pruned. It holds one entry per *Handler for the lifetime
// of the test binary, which is deliberate (that is what makes one Handler behave
// like one connection) and is bounded by the number of Handlers a test run
// creates.
func shimLimiterFor(h *Handler) *ingestLogLimiter {
	handlerShimLimitersMu.Lock()
	defer handlerShimLimitersMu.Unlock()
	l, ok := handlerShimLimiters[h]
	if !ok {
		l = newIngestLogLimiter()
		handlerShimLimiters[h] = l
	}
	return l
}

// HandleTaskStatus exposes the unexported handleTaskStatus method for
// integration tests in package worker_test. workerID is the sending
// connection's authenticated worker, exactly as Connect supplies it in
// production; the log budget is this Handler's shim limiter (see above).
func (h *Handler) HandleTaskStatus(ctx context.Context, workerID pgtype.UUID, upd *relayv1.TaskStatusUpdate) {
	h.handleTaskStatus(ctx, workerID, shimLimiterFor(h), upd)
}

// HandleTaskLog exposes the unexported handleTaskLog method for integration
// tests in package worker_test. workerID is the sending connection's
// authenticated worker, exactly as Connect supplies it in production; the log
// budget is this Handler's shim limiter (see above).
func (h *Handler) HandleTaskLog(ctx context.Context, workerID pgtype.UUID, chunk *relayv1.TaskLogChunk) {
	h.handleTaskLog(ctx, workerID, shimLimiterFor(h), chunk)
}

// LimiterHandle is an opaque wrapper around an unexported *ingestLogLimiter, in
// the same shape as SenderHandle below, so package worker_test can hold two
// independent budgets against ONE Handler without going through Connect.
type LimiterHandle struct {
	l *ingestLogLimiter
}

// NewLimiterForTest returns a fresh per-connection log budget, in the state
// Connect allocates one in.
func NewLimiterForTest() *LimiterHandle {
	return &LimiterHandle{l: newIngestLogLimiter()}
}

// HandleTaskLogWithLimiter is HandleTaskLog with an explicit budget.
func (h *Handler) HandleTaskLogWithLimiter(ctx context.Context, workerID pgtype.UUID, lim *LimiterHandle, chunk *relayv1.TaskLogChunk) {
	h.handleTaskLog(ctx, workerID, lim.l, chunk)
}

// HandleTaskStatusWithLimiter is HandleTaskStatus with an explicit budget.
func (h *Handler) HandleTaskStatusWithLimiter(ctx context.Context, workerID pgtype.UUID, lim *LimiterHandle, upd *relayv1.TaskStatusUpdate) {
	h.handleTaskStatus(ctx, workerID, lim.l, upd)
}

// TaskLogPublishesForTest reports how many chunks have got past the
// HasLogSubscriber fast path and been marshalled + published as task_log events
// since process start. Lets a test assert the fast path really skipped the
// marshal, which is otherwise unobservable from outside the package.
func TaskLogPublishesForTest() int64 {
	return taskLogPublishes.Load()
}

// ApplyInventory exposes the unexported applyInventory method for integration tests.
func (h *Handler) ApplyInventory(ctx context.Context, workerID pgtype.UUID, inv []*relayv1.WorkspaceInventoryUpdate) error {
	return h.applyInventory(ctx, workerID, inv)
}

// ApplyInventoryUpdate exposes the unexported applyInventoryUpdate method for integration tests.
func (h *Handler) ApplyInventoryUpdate(ctx context.Context, workerID pgtype.UUID, u *relayv1.WorkspaceInventoryUpdate) error {
	return h.applyInventoryUpdate(ctx, workerID, u)
}

// RegisteredSenderForTest wraps stream in a real *workerSender, registers it
// for workerID exactly as finishRegister does, and returns an opaque handle.
// Used by package worker_test to drive teardownConnection with a known sender.
func (h *Handler) RegisteredSenderForTest(workerID string, stream Sender) *SenderHandle {
	s := NewWorkerSender(stream)
	h.registry.Register(workerID, s)
	return &SenderHandle{s: s}
}

// RegisteredSenderWithEpochForTest wraps stream in a real *workerSender, stamps
// the given connEpoch (as finishRegister does), registers it for workerID, and
// returns an opaque handle. Lets worker_test drive teardown with a known,
// possibly-stale, owned epoch.
func (h *Handler) RegisteredSenderWithEpochForTest(workerID string, stream Sender, epoch int32) *SenderHandle {
	s := NewWorkerSender(stream)
	s.connEpoch = epoch
	h.registry.Register(workerID, s)
	return &SenderHandle{s: s}
}

// RegisterWorkerConnectionForTest invokes the store's RegisterWorkerConnection
// so worker_test can advance a worker's connection_epoch and read the new value.
func (h *Handler) RegisterWorkerConnectionForTest(ctx context.Context, id pgtype.UUID) (int32, error) {
	w, err := h.q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:         id,
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		return 0, err
	}
	return w.ConnectionEpoch, nil
}

// SenderHandle is an opaque wrapper around an unexported *workerSender so that
// package worker_test can hold and pass senders without touching the type.
type SenderHandle struct {
	s *workerSender
}

// TeardownConnectionForTest invokes the unexported teardownConnection with the
// handle's sender, exercising the production ownership gate.
func (h *Handler) TeardownConnectionForTest(workerID string, handle *SenderHandle) {
	h.teardownConnection(workerID, handle.s)
}

// SendToWorkerForTest delivers msg to workerID through the registry, exercising
// the production send path so the test can prove which sender is registered.
func (h *Handler) SendToWorkerForTest(workerID string, msg *relayv1.CoordinatorMessage) error {
	return h.registry.Send(workerID, msg)
}

// UUIDStringForTest renders a pgtype.UUID via the package's canonical uuidStr,
// so package worker_test can derive a worker-ID string without reimplementing
// UUID formatting.
func (h *Handler) UUIDStringForTest(u pgtype.UUID) string {
	return uuidStr(u)
}

// SetAgentTokenGeneratorForTest replaces the random-token generator used by
// enrollAndRegister for the duration of t. The generator returns (rawToken, hash);
// in production these come from cryptorand + tokenhash.Hash.
func SetAgentTokenGeneratorForTest(t *testing.T, fn func() (raw string, hash string)) {
	t.Helper()
	prev := agentTokenGenerator
	agentTokenGenerator = fn
	t.Cleanup(func() { agentTokenGenerator = prev })
}
