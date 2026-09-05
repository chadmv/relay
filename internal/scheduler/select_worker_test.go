package scheduler

// Unit tests for selectWorker that run without Docker or a real database.
// They construct store.Worker/store.Task values in memory and call the
// unexported selectWorker method directly (possible because this file is in
// package scheduler, not package scheduler_test).

import (
	"encoding/json"
	"testing"
	"time"

	"relay/internal/api"
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

// sourceTask returns a pending task whose Source is a parseable Perforce spec,
// making it "source-bearing" (taskSrc != nil in selectWorker).
func sourceTask() store.Task {
	t := baseTask()
	t.Source = []byte(`{"type":"perforce","stream":"//depot/main"}`)
	return t
}

func TestSelectWorker_SourceTaskSkipsProviderlessWorker(t *testing.T) {
	d := newDispatcherForTest()
	task := sourceTask()
	w := baseWorker(70, "online") // SupportsWorkspaces defaults false
	workers := []store.Worker{w}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	assert.Nil(t, got, "a source-bearing task must NOT be dispatched to a providerless worker")
}

func TestSelectWorker_SourceTaskSelectsCapableWorker(t *testing.T) {
	d := newDispatcherForTest()
	task := sourceTask()
	w := baseWorker(71, "online")
	w.SupportsWorkspaces = true
	workers := []store.Worker{w}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got, "a source-bearing task must be dispatched to a provider-capable worker")
	assert.Equal(t, w.ID, got.ID)
}

func TestSelectWorker_SourceTaskPrefersCapableOverFreerProviderless(t *testing.T) {
	// The providerless worker has more free slots (higher base score) but must
	// still be skipped; the capable worker wins despite fewer free slots.
	d := newDispatcherForTest()
	task := sourceTask()
	providerless := baseWorker(72, "online")
	providerless.MaxSlots = 16 // more free slots
	capable := baseWorker(73, "online")
	capable.MaxSlots = 1
	capable.SupportsWorkspaces = true
	workers := []store.Worker{providerless, capable}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got)
	assert.Equal(t, capable.ID, got.ID, "capability is a hard filter that outranks free-slot scoring")
}

func TestSelectWorker_NonSourceTaskIgnoresProviderlessFlag(t *testing.T) {
	// Regression guard: a non-source task (empty Source) schedules on a
	// providerless worker; the guard is a no-op for taskSrc == nil.
	d := newDispatcherForTest()
	task := baseTask() // no Source
	w := baseWorker(74, "online") // SupportsWorkspaces false
	workers := []store.Worker{w}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.NotNil(t, got, "a non-source task must still schedule on a providerless worker")
	assert.Equal(t, w.ID, got.ID)
}

func TestSelectWorker_TypelessSourceAgreesWithSourceBearing(t *testing.T) {
	// A task with a parseable but typeless Source ({}) is not source-bearing:
	// it carries no provider requirement. The selectWorker capability filter and
	// taskIsSourceBearing must agree, so such a task schedules on a providerless
	// worker (matching the held-pending count, which keys off taskIsSourceBearing).
	d := newDispatcherForTest()
	task := baseTask()
	task.Source = []byte(`{}`)
	w := baseWorker(75, "online") // SupportsWorkspaces false
	workers := []store.Worker{w}

	got := d.selectWorker(task, workers, nil,
		map[pgtype.UUID]int64{},
		map[pgtype.UUID][]store.WorkerWorkspace{},
	)

	require.False(t, taskIsSourceBearing(task), "typeless source must not be source-bearing")
	require.NotNil(t, got, "a typeless-source task must still schedule on a providerless worker")
	assert.Equal(t, w.ID, got.ID)
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

// sourceTaskWithExclusion is the same stream as sourceTask plus one exclusion,
// so the only thing that differs between the pair of tests below is the key.
func sourceTaskWithExclusion() store.Task {
	t := baseTask()
	t.Source = []byte(`{"type":"perforce","stream":"//depot/main","sync":` +
		`[{"path":"//depot/main/...","rev":"#head"},` +
		`{"path":"//depot/main/heavy/...","exclude":true}]}`)
	return t
}

func warmOn(id byte, key string) (store.Worker, []store.WorkerWorkspace) {
	w := baseWorker(id, "online")
	w.MaxSlots = 1
	w.SupportsWorkspaces = true
	return w, []store.WorkerWorkspace{{WorkerID: w.ID, SourceType: "perforce", SourceKey: key}}
}

// An excluded task must NOT be scored warm on a workspace holding the whole
// stream: that workspace has no have-list preempt, and preferring it is how the
// bias would keep pushing excluded tasks onto workspaces they cannot use. The
// discriminator is the fewer-slots warm worker versus a freer cold one: with
// the bias firing the warm worker wins, so a cold winner is the observable
// proof it did not fire.
func TestSelectWorker_AnExcludedTaskIsNotWarmOnAnUnexcludingWorkspace(t *testing.T) {
	d := newDispatcherForTest()
	warm, rows := warmOn(80, "//depot/main")
	cold := baseWorker(81, "online")
	cold.MaxSlots = 4
	cold.SupportsWorkspaces = true

	got := d.selectWorker(sourceTaskWithExclusion(), []store.Worker{warm, cold}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{warm.ID: rows})

	require.NotNil(t, got)
	assert.Equal(t, cold.ID, got.ID, "the whole-stream workspace is not warm for an excluded task")
}

// The exact sibling. Widening the comparison without this would lose the warm
// bias entirely and nothing would say so.
func TestSelectWorker_AnUnexcludedTaskIsStillWarmOnItsStreamKeyedWorkspace(t *testing.T) {
	d := newDispatcherForTest()
	warm, rows := warmOn(82, "//depot/main")
	cold := baseWorker(83, "online")
	cold.MaxSlots = 4
	cold.SupportsWorkspaces = true

	task := baseTask()
	task.Source = []byte(`{"type":"perforce","stream":"//depot/main","sync":[{"path":"//depot/main/...","rev":"#head"}]}`)

	got := d.selectWorker(task, []store.Worker{warm, cold}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{warm.ID: rows})

	require.NotNil(t, got)
	assert.Equal(t, warm.ID, got.ID, "an unexcluded task keeps today's warm bias, byte for byte")
}

// And the excluded task IS warm on the matching composite key, which kills a
// mutant that simply stops scoring warm for anything carrying an exclusion.
func TestSelectWorker_AnExcludedTaskIsWarmOnItsOwnCompositeKey(t *testing.T) {
	d := newDispatcherForTest()
	task := sourceTaskWithExclusion()
	var s api.SourceSpec
	require.NoError(t, json.Unmarshal(task.Source, &s))
	warm, rows := warmOn(84, SourceKeyFromAPISpec(&s))
	cold := baseWorker(85, "online")
	cold.MaxSlots = 4
	cold.SupportsWorkspaces = true

	got := d.selectWorker(task, []store.Worker{warm, cold}, nil,
		map[pgtype.UUID]int64{}, map[pgtype.UUID][]store.WorkerWorkspace{warm.ID: rows})

	require.NotNil(t, got)
	assert.Equal(t, warm.ID, got.ID)
}
