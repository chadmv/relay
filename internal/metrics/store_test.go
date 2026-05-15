package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_AppendAndSnapshot(t *testing.T) {
	s := NewStore(10)
	s.Activate("w1", time.Unix(0, 0))
	s.Append("w1", Sample{At: time.Unix(1, 0), CPUPercent: 10})
	s.Append("w1", Sample{At: time.Unix(2, 0), CPUPercent: 20})

	snap := s.Snapshot("w1")
	require.Len(t, snap, 2)
	assert.Equal(t, 10.0, snap[0].CPUPercent)
	assert.Equal(t, 20.0, snap[1].CPUPercent)
}

func TestStore_RingBufferEvictsOldest(t *testing.T) {
	s := NewStore(3)
	s.Activate("w1", time.Unix(0, 0))
	for i := 1; i <= 5; i++ {
		s.Append("w1", Sample{At: time.Unix(int64(i), 0), CPUPercent: float64(i)})
	}
	snap := s.Snapshot("w1")
	require.Len(t, snap, 3)
	assert.Equal(t, 3.0, snap[0].CPUPercent)
	assert.Equal(t, 5.0, snap[2].CPUPercent)
}

func TestStore_AppendUntrackedWorkerIsNoOp(t *testing.T) {
	s := NewStore(10)
	s.Append("ghost", Sample{At: time.Unix(1, 0)})
	assert.Empty(t, s.Snapshot("ghost"))
}

func TestStore_SnapshotUnknownWorkerIsEmptyNotNil(t *testing.T) {
	s := NewStore(10)
	assert.Equal(t, []Sample{}, s.Snapshot("nobody"))
}

func TestStore_LastSampleAt(t *testing.T) {
	s := NewStore(10)

	_, ok := s.LastSampleAt("unknown")
	assert.False(t, ok)

	s.Activate("w1", time.Unix(100, 0))
	at, ok := s.LastSampleAt("w1")
	require.True(t, ok)
	assert.Equal(t, time.Unix(100, 0), at, "empty buffer returns activation time")

	s.Append("w1", Sample{At: time.Unix(200, 0)})
	at, ok = s.LastSampleAt("w1")
	require.True(t, ok)
	assert.Equal(t, time.Unix(200, 0), at, "non-empty buffer returns newest sample time")
}

func TestStore_ClearStopsTracking(t *testing.T) {
	s := NewStore(10)
	s.Activate("w1", time.Unix(0, 0))
	s.Append("w1", Sample{At: time.Unix(1, 0)})
	s.Clear("w1")

	_, ok := s.LastSampleAt("w1")
	assert.False(t, ok)
	assert.Empty(t, s.Snapshot("w1"))
}
