package scheduler

// Unit tests for selectWorker that run without Docker or a real database.
// They construct store.Worker/store.Task values in memory and call the
// unexported selectWorker method directly (possible because this file is in
// package scheduler, not package scheduler_test).

import (
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeUUID returns a valid pgtype.UUID whose first byte is set to id so tests
// can construct distinct UUIDs without a database.
func makeUUID(id byte) pgtype.UUID {
	var b [16]byte
	b[0] = id
	return pgtype.UUID{Bytes: b, Valid: true}
}

// baseTask returns a minimal pending task with no label requirements.
func baseTask() store.Task {
	return store.Task{
		ID:       makeUUID(1),
		Requires: []byte(`{}`),
	}
}

// baseWorker returns a minimal worker with 4 slots and empty labels.
func baseWorker(id byte, status string) store.Worker {
	return store.Worker{
		ID:       makeUUID(id),
		MaxSlots: 4,
		Labels:   []byte(`{}`),
		Status:   status,
	}
}

// newDispatcherForTest returns a *Dispatcher whose database fields are nil;
// selectWorker does not touch them so this is safe for unit tests.
func newDispatcherForTest() *Dispatcher {
	return &Dispatcher{}
}

func TestSelectWorker_StaleWorkerIsEligible(t *testing.T) {
	d := newDispatcherForTest()
	task := baseTask()
	workers := []store.Worker{baseWorker(10, "stale")}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got, "stale worker must be selected for dispatch")
	assert.Equal(t, workers[0].ID, got.ID)
}

func TestSelectWorker_OnlineWorkerIsEligible(t *testing.T) {
	d := newDispatcherForTest()
	task := baseTask()
	workers := []store.Worker{baseWorker(20, "online")}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got, "online worker must be selected for dispatch")
	assert.Equal(t, workers[0].ID, got.ID)
}

func TestSelectWorker_OfflineWorkerIsExcluded(t *testing.T) {
	d := newDispatcherForTest()
	task := baseTask()
	workers := []store.Worker{baseWorker(30, "offline")}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	assert.Nil(t, got, "offline worker must NOT be selected for dispatch")
}

func TestSelectWorker_BothOnlineAndStaleAreEligibleCandidates(t *testing.T) {
	// Both workers have the same free slots; the scheduler scores them equally
	// because there is no status-based preference - both online and stale are
	// eligible candidates, and this test simply ensures one of them is returned.
	d := newDispatcherForTest()
	task := baseTask()
	workers := []store.Worker{
		baseWorker(40, "stale"),
		baseWorker(41, "online"),
	}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got, "at least one worker must be selected")
}

func TestSelectWorker_StaleAndOfflineMixed_StaleSelected(t *testing.T) {
	d := newDispatcherForTest()
	task := baseTask()
	workers := []store.Worker{
		baseWorker(50, "offline"),
		baseWorker(51, "stale"),
	}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got, "stale worker must be selected when offline worker is also present")
	assert.Equal(t, workers[1].ID, got.ID, "the stale worker (index 1) must be chosen, not the offline one")
}

func TestSelectWorker_DisabledWorkerIsNotEligible(t *testing.T) {
	d := newDispatcherForTest()
	task := baseTask()
	wk := baseWorker(60, "online")
	wk.DisabledAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	workers := []store.Worker{wk}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	assert.Nil(t, got, "a disabled worker must NOT be selected for dispatch")
}
