package metrics

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWorkerID = "11111111-1111-1111-1111-111111111111"

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(s))
	return u
}

type fakeSweepStore struct {
	workers []store.Worker
	updates []store.SetWorkerStatusParams
}

func (f *fakeSweepStore) ListWorkersByLiveness(ctx context.Context) ([]store.Worker, error) {
	return f.workers, nil
}

func (f *fakeSweepStore) SetWorkerStatus(ctx context.Context, arg store.SetWorkerStatusParams) error {
	f.updates = append(f.updates, arg)
	return nil
}

func TestSweeper_OnlineToStale(t *testing.T) {
	st := NewStore(10)
	st.Activate(testWorkerID, time.Unix(0, 0)) // last sample at t=0

	fake := &fakeSweepStore{workers: []store.Worker{
		{ID: mustUUID(t, testWorkerID), Status: "online"},
	}}
	sw := NewSweeper(fake, events.NewBroker(), st, 30*time.Second)
	sw.now = func() time.Time { return time.Unix(40, 0) } // 40s > 30s threshold

	require.NoError(t, sw.SweepOnce(context.Background()))
	require.Len(t, fake.updates, 1)
	assert.Equal(t, "stale", fake.updates[0].Status)
}

func TestSweeper_StaleToOnline(t *testing.T) {
	st := NewStore(10)
	st.Activate(testWorkerID, time.Unix(0, 0))
	st.Append(testWorkerID, Sample{At: time.Unix(100, 0)}) // fresh sample

	fake := &fakeSweepStore{workers: []store.Worker{
		{ID: mustUUID(t, testWorkerID), Status: "stale"},
	}}
	sw := NewSweeper(fake, events.NewBroker(), st, 30*time.Second)
	sw.now = func() time.Time { return time.Unix(110, 0) } // 10s <= 30s threshold

	require.NoError(t, sw.SweepOnce(context.Background()))
	require.Len(t, fake.updates, 1)
	assert.Equal(t, "online", fake.updates[0].Status)
}

func TestSweeper_NoTransitionWhenFresh(t *testing.T) {
	st := NewStore(10)
	st.Activate(testWorkerID, time.Unix(0, 0))
	st.Append(testWorkerID, Sample{At: time.Unix(100, 0)})

	fake := &fakeSweepStore{workers: []store.Worker{
		{ID: mustUUID(t, testWorkerID), Status: "online"},
	}}
	sw := NewSweeper(fake, events.NewBroker(), st, 30*time.Second)
	sw.now = func() time.Time { return time.Unix(110, 0) }

	require.NoError(t, sw.SweepOnce(context.Background()))
	assert.Empty(t, fake.updates, "fresh online worker stays online")
}

func TestSweeper_SkipsUntrackedWorker(t *testing.T) {
	st := NewStore(10) // testWorkerID never Activated

	fake := &fakeSweepStore{workers: []store.Worker{
		{ID: mustUUID(t, testWorkerID), Status: "online"},
	}}
	sw := NewSweeper(fake, events.NewBroker(), st, 30*time.Second)
	sw.now = func() time.Time { return time.Unix(9999, 0) }

	require.NoError(t, sw.SweepOnce(context.Background()))
	assert.Empty(t, fake.updates, "untracked workers are skipped, not marked stale")
}
