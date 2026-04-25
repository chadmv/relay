//go:build integration

package worker

import (
	"context"

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
