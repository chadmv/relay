package api

import (
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToWorkerResponse_EnabledWorkerKeepsLiveStatus(t *testing.T) {
	w := store.Worker{
		Status: "online",
		Labels: []byte(`{}`),
	}
	resp := toWorkerResponse(w)
	assert.Equal(t, "online", resp.Status)
	assert.Nil(t, resp.DisabledAt, "disabled_at must be nil for an enabled worker")
}

func TestToWorkerResponse_DisabledWorkerCoalescesStatus(t *testing.T) {
	disabledAt := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	w := store.Worker{
		Status:     "online",
		Labels:     []byte(`{}`),
		DisabledAt: pgtype.Timestamptz{Time: disabledAt, Valid: true},
	}
	resp := toWorkerResponse(w)
	assert.Equal(t, "disabled", resp.Status, "status must coalesce to 'disabled'")
	require.NotNil(t, resp.DisabledAt)
	assert.True(t, resp.DisabledAt.Equal(disabledAt))
}
