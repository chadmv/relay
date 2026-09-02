//go:build integration

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDispatcher_ClaimStampsAssignedAt is the production guard for the one
// load-bearing write of tasks.assigned_at. ClaimTaskForWorkerParams gained a new
// field, and a keyed struct literal that omits it still COMPILES and binds SQL
// NULL - which silently exempts every claimed task from the watchdog's absolute
// arm forever, with no error anywhere. Nothing but this test fails if
// dispatch.go's call site stops passing it.
func TestDispatcher_ClaimStampsAssignedAt(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "assignedat@x.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	registry.Register(uuidStr(w.ID), &fakeSender{})

	before := time.Now()
	scheduler.NewDispatcher(q, registry, events.NewBroker(), "").RunOnce(ctx)

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "dispatched", got.Status, "precondition: the task must have been claimed")
	require.True(t, got.AssignedAt.Valid,
		"a claimed task must carry assigned_at; a NULL here exempts it from the watchdog's absolute bound forever")
	assert.False(t, got.AssignedAt.Time.Before(before.Add(-time.Second)),
		"assigned_at must be the claim's own clock, not something older")
	assert.False(t, got.AssignedAt.Time.After(time.Now().Add(time.Second)),
		"assigned_at must not be in the future")
}
