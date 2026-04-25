//go:build integration

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// fmtUUID converts a pgtype.UUID to its canonical string form for use in URLs.
func fmtUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func TestListWorkerWorkspaces_AdminOnly(t *testing.T) {
	srv, q := newTestServer(t)
	ctx := context.Background()

	regularUser := createTestUser(t, q, "regular", "regular@x", false)
	regularTok := createTestToken(t, q, regularUser.ID)
	adminUser := createTestUser(t, q, "admin", "admin@x", true)
	adminTok := createTestToken(t, q, adminUser.ID)

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "h", Hostname: "h", CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: w.ID, SourceType: "perforce", SourceKey: "//s/x", ShortID: "abc",
		BaselineHash: "deadbeef", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}))

	// Non-admin must get 403.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/workers/"+fmtUUID(w.ID)+"/workspaces", nil)
	req.Header.Set("Authorization", "Bearer "+regularTok)
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// Admin gets 200 with workspace data.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/workers/"+fmtUUID(w.ID)+"/workspaces", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"//s/x"`)
	require.Contains(t, rec.Body.String(), `"abc"`)
	_ = adminUser
	_ = regularUser
}

func TestEvictWorkerWorkspace_NotConnected_Returns202(t *testing.T) {
	srv, q := newTestServer(t)
	ctx := context.Background()

	adminUser := createTestUser(t, q, "admin2", "admin2@x", true)
	adminTok := createTestToken(t, q, adminUser.ID)

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "h2", Hostname: "h2", CpuCores: 1, RamGb: 1, GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	require.NoError(t, q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID: w.ID, SourceType: "perforce", SourceKey: "//s/x", ShortID: "abc",
		BaselineHash: "x", LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/workers/"+fmtUUID(w.ID)+"/workspaces/abc/evict", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	srv.Handler().ServeHTTP(rec, req)
	// Worker is offline; still returns 202 (fire-and-forget).
	require.Equal(t, http.StatusAccepted, rec.Code)

	// DB row is left in place.
	rows, err := q.ListWorkerWorkspaces(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	_ = adminUser
}
