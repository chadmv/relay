//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestCreateJobFromSpec_CreatesJobAndTasks(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)

	user := createTestUser(t, q, "Alice", "alice@example.com", false)

	spec := api.JobSpec{
		Name:     "nightly-render",
		Priority: "normal",
		Labels:   map[string]string{"project": "test"},
		Tasks: []api.TaskSpec{
			{Name: "render", Command: []string{"echo", "hi"}},
		},
	}

	var scheduledID pgtype.UUID // invalid = NULL

	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	defer tx.Rollback(context.Background())

	job, tasks, err := api.CreateJobFromSpec(
		context.Background(), q.WithTx(tx), spec, user.ID, scheduledID,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(context.Background()))

	require.Equal(t, "nightly-render", job.Name)
	require.Len(t, tasks, 1)
	require.Equal(t, "render", tasks[0].Name)

	var labels map[string]string
	require.NoError(t, json.Unmarshal(job.Labels, &labels))
	require.Equal(t, "test", labels["project"])
}

func TestCreateJobFromSpec_PersistsSource(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	user := createTestUser(t, q, "Carol", "carol-source@example.com", false)

	spec := api.JobSpec{
		Name:     "j",
		Priority: "normal",
		Tasks: []api.TaskSpec{{
			Name:    "t",
			Command: []string{"true"},
			Source: &api.SourceSpec{
				Type:   "perforce",
				Stream: "//streams/X/main",
				Sync:   []api.SyncEntry{{Path: "//streams/X/main/...", Rev: "#head"}},
			},
		}},
	}

	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	defer tx.Rollback(context.Background())

	_, tasks, err := api.CreateJobFromSpec(context.Background(), q.WithTx(tx), spec, user.ID, pgtype.UUID{})
	require.NoError(t, err)
	require.NoError(t, tx.Commit(context.Background()))

	require.Len(t, tasks, 1)
	require.NotNil(t, tasks[0].Source)
	require.Contains(t, string(tasks[0].Source), `"//streams/X/main"`)
}

func TestCreateJobFromSpec_DefaultPriority(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	user := createTestUser(t, q, "Bob", "bob@example.com", false)

	spec := api.JobSpec{
		Name: "no-priority",
		Tasks: []api.TaskSpec{
			{Name: "t1", Command: []string{"echo", "hello"}},
		},
	}

	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	defer tx.Rollback(context.Background())

	job, _, err := api.CreateJobFromSpec(context.Background(), q.WithTx(tx), spec, user.ID, pgtype.UUID{})
	require.NoError(t, err)
	require.NoError(t, tx.Commit(context.Background()))

	require.Equal(t, "normal", job.Priority)
}
