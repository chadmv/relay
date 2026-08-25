package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnrollmentRefusalReasonsAreADenseRunFromZero. The values are ARRAY INDICES
// starting at 0, and record fails CLOSED, so a gap or a renumbering is a SILENT
// loss of that reason's counts rather than a panic.
func TestEnrollmentRefusalReasonsAreADenseRunFromZero(t *testing.T) {
	assert.Equal(t, enrollmentRefusalReason(0), enrollmentRefusalHostnameClaimed)
	assert.Equal(t, enrollmentRefusalReason(1), enrollmentRefusalFleetAtCeiling)
	assert.Equal(t, enrollmentRefusalReason(2), enrollmentRefusalCredentialLive)
	assert.Equal(t, enrollmentRefusalReason(3), enrollmentRefusalReasonCount)
}

// TestEnrollmentRefusalCounters_EveryReasonIsPublishedDistinctly is the mutation
// M15 detector: incrementing the wrong reason must be visible.
func TestEnrollmentRefusalCounters_EveryReasonIsPublishedDistinctly(t *testing.T) {
	var c enrollmentRefusalCounters
	for r := enrollmentRefusalReason(0); r < enrollmentRefusalReasonCount; r++ {
		for i := 0; i <= int(r); i++ {
			c.record(r)
		}
	}
	got := c.snapshot()
	assert.Equal(t, uint64(1), got.HostnameClaimed)
	assert.Equal(t, uint64(2), got.FleetAtCeiling)
	assert.Equal(t, uint64(3), got.CredentialLive)
}

func TestEnrollmentRefusalCounters_AnOutOfRangeReasonIsDroppedNotPanicked(t *testing.T) {
	var c enrollmentRefusalCounters
	require.NotPanics(t, func() { c.record(enrollmentRefusalReasonCount) })
	assert.Equal(t, EnrollmentRefusalCounts{}, c.snapshot())
}
