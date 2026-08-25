package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAutoEnrollRefusalReasonsAreADenseRunFromZero. The values are ARRAY INDICES
// starting at 0, and record fails CLOSED, so a gap or a renumbering is a SILENT
// loss of that reason's counts rather than a panic.
func TestAutoEnrollRefusalReasonsAreADenseRunFromZero(t *testing.T) {
	assert.Equal(t, autoEnrollReason(0), autoEnrollReasonHostnameClaimed)
	assert.Equal(t, autoEnrollReason(1), autoEnrollReasonFleetAtCeiling)
	assert.Equal(t, autoEnrollReason(2), autoEnrollReasonCredentialLive)
	assert.Equal(t, autoEnrollReason(3), autoEnrollReasonCount)
}

// TestAutoEnrollRefusalCounters_EveryReasonIsPublishedDistinctly is the mutation
// M15 detector: incrementing the wrong reason must be visible.
func TestAutoEnrollRefusalCounters_EveryReasonIsPublishedDistinctly(t *testing.T) {
	var c autoEnrollRefusalCounters
	for r := autoEnrollReason(0); r < autoEnrollReasonCount; r++ {
		for i := 0; i <= int(r); i++ {
			c.record(r)
		}
	}
	got := c.snapshot()
	assert.Equal(t, uint64(1), got.HostnameClaimed)
	assert.Equal(t, uint64(2), got.FleetAtCeiling)
	assert.Equal(t, uint64(3), got.CredentialLive)
}

func TestAutoEnrollRefusalCounters_AnOutOfRangeReasonIsDroppedNotPanicked(t *testing.T) {
	var c autoEnrollRefusalCounters
	require.NotPanics(t, func() { c.record(autoEnrollReasonCount) })
	assert.Equal(t, AutoEnrollRefusalCounts{}, c.snapshot())
}
