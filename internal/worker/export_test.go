//go:build integration

package worker

import (
	"context"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/jackc/pgx/v5/pgtype"
)

// HandleTaskStatus exposes the unexported handleTaskStatus method for
// integration tests in package worker_test.
func (h *Handler) HandleTaskStatus(ctx context.Context, upd *relayv1.TaskStatusUpdate) {
	h.handleTaskStatus(ctx, upd)
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
