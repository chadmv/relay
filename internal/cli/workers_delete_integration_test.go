//go:build integration

package cli

import (
	"bytes"
	"errors"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/require"
)

// TestIntegration_WorkersDelete_OfflineWorker_Succeeds targets the worker BY
// HOSTNAME, so it exercises resolveWorkerIDIncludingRevoked against the real
// list endpoint as well as the delete itself. The four counts come from the
// real deleteWorkerResponse, whose identity fields arrive via an EMBEDDED
// workerResponse - a flattening a hand-written fixture gets right only by
// accident.
func TestIntegration_WorkersDelete_OfflineWorker_Succeeds(t *testing.T) {
	s := startRelayServer(t)
	id := seedWorker(t, s, "render-node-a-display", "render-node-a", "offline")

	var out bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), s.adminCfg(),
		[]string{"delete", "--yes", "render-node-a"}, &out))

	got := out.String()
	require.Contains(t, got, `deleted worker "render-node-a" (`+id+`)`)
	require.Contains(t, got, "0 task(s) requeued")
	require.Contains(t, got, "0 reservation(s) updated")
	require.Contains(t, got, "0 enrollment(s) unlinked")
	require.Contains(t, got, "0 finished task(s) lost their worker attribution")

	var n int64
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM workers`).Scan(&n))
	require.Zero(t, n)
}

// TestIntegration_WorkersDelete_ConnectedWorker_Is409 targets by UUID, so the
// list endpoint is never called and the 409 is attributable to the status gate
// alone.
func TestIntegration_WorkersDelete_ConnectedWorker_Is409(t *testing.T) {
	s := startRelayServer(t)
	id := seedWorker(t, s, "render-node-b-display", "render-node-b", "online")

	var out bytes.Buffer
	err := doWorkers(testCtx(t), s.adminCfg(), []string{"delete", "--yes", id}, &out)
	require.Error(t, err)

	var re *relayclient.ResponseError
	require.True(t, errors.As(err, &re), "want a ResponseError, got %T: %v", err, err)
	require.Equal(t, 409, re.StatusCode)
	require.Contains(t, err.Error(), "worker is connected")

	// The row survives a refusal.
	var n int64
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM workers WHERE id = $1::uuid`, id).Scan(&n))
	require.EqualValues(t, 1, n)
}

// TestIntegration_WorkersDelete_UnknownUUID_Is404. The target MUST be
// UUID-shaped: for a hostname that matches nothing, doWorkersDelete fails
// locally in resolveWorkerIDIncludingRevoked and never issues a request, so a
// hostname here would assert nothing about the handler.
func TestIntegration_WorkersDelete_UnknownUUID_Is404(t *testing.T) {
	s := startRelayServer(t)

	var out bytes.Buffer
	err := doWorkers(testCtx(t), s.adminCfg(),
		[]string{"delete", "--yes", "3f2b1a0c-9d8e-4c7b-8a6f-5e4d3c2b1a09"}, &out)
	require.Error(t, err)

	var re *relayclient.ResponseError
	require.True(t, errors.As(err, &re), "want a ResponseError, got %T: %v", err, err)
	require.Equal(t, 404, re.StatusCode)
	require.Contains(t, err.Error(), "worker not found")
}

// TestIntegration_WorkersDelete_NonAdmin_Is403BeforeTheStatusGate pins the
// LADDER ORDER. The worker is online, so a delete that reached the handler
// would be a 409; DELETE /v1/workers/{id} is auth(admin(...)), so a non-admin
// gets 403 first. Asserting 403 and not merely "an error" is the whole test.
func TestIntegration_WorkersDelete_NonAdmin_Is403BeforeTheStatusGate(t *testing.T) {
	s := startRelayServer(t)
	id := seedWorker(t, s, "render-node-c-display", "render-node-c", "online")

	var out bytes.Buffer
	err := doWorkers(testCtx(t), s.userCfg(), []string{"delete", "--yes", id}, &out)
	require.Error(t, err)

	var re *relayclient.ResponseError
	require.True(t, errors.As(err, &re), "want a ResponseError, got %T: %v", err, err)
	require.Equal(t, 403, re.StatusCode,
		"a non-admin must be refused by AdminOnly BEFORE the status gate returns 409")
	require.Contains(t, err.Error(), "admin access required")
}
