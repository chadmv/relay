//go:build integration

package worker

import (
	"context"

	relayv1 "relay/internal/proto/relayv1"
)

// HandleTaskStatus exposes the unexported handleTaskStatus method for
// integration tests in package worker_test.
func (h *Handler) HandleTaskStatus(ctx context.Context, upd *relayv1.TaskStatusUpdate) {
	h.handleTaskStatus(ctx, upd)
}
