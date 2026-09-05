package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMaxSchedulesPerOwner_NonPositiveFoldsToTheDefault pins the failure
// DIRECTION, which is the whole reason the field is resolved through a method
// instead of read raw.
//
// A deleted or crossed wiring assignment in cmd/relay-server's buildHTTPServer
// leaves this field at zero. Read raw, zero means "refuse everything" - a
// control that fails from a wiring slip into a total outage. Folded, it means
// "the operator's number was ignored", which is the direction
// Handler.autoEnrollWorkerCeiling's neighbours take and the reason its comment
// gives: a direct-construction caller fails BOUNDED rather than refusing
// everything.
func TestMaxSchedulesPerOwner_NonPositiveFoldsToTheDefault(t *testing.T) {
	for _, n := range []int{0, -1} {
		s := &Server{MaxSchedulesPerOwner: n}
		require.Equal(t, DefaultMaxSchedulesPerOwner, s.maxSchedulesPerOwner(),
			"%d must fold to the default: a zeroed wiring field must degrade to 'the operator's "+
				"number was ignored', never to 'every create is refused'", n)
	}
	require.Equal(t, DefaultMaxSchedulesPerOwner, (&Server{}).maxSchedulesPerOwner(),
		"an unset field is the state every test-lane api.New call is in")
	require.Equal(t, 7, (&Server{MaxSchedulesPerOwner: 7}).maxSchedulesPerOwner(),
		"a positive value is used as-is; folding it would make the environment variable dead")
}
