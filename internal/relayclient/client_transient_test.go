// internal/relayclient/client_transient_test.go
package relayclient

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The partition two poll loops share - internal/mcp's relay_wait_for_job and
// internal/cli's `relay logs` subscribe-time snapshot. Each of them used to own
// its own answer to this question, or in the CLI's case no answer at all, so the
// boundary itself is what needs pinning rather than either caller's use of it.
//
// The permanent side is enumerated and the transient side is the default. That
// asymmetry is the design: an unrecognised status keeps a caller waiting on a
// server that may yet answer, where the other direction would report a permanent
// failure nobody established.
func TestErrorIsTransient_PartitionsByWhatALaterRequestCouldChange(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"malformed id", &ResponseError{StatusCode: http.StatusBadRequest, Message: "bad id"}, false},
		{"expired token", &ResponseError{StatusCode: http.StatusUnauthorized, Message: "unauthorized"}, false},
		{"not an admin", &ResponseError{StatusCode: http.StatusForbidden, Message: "forbidden"}, false},
		{"no such job", &ResponseError{StatusCode: http.StatusNotFound, Message: "job not found"}, false},
		{"conflicting change", &ResponseError{StatusCode: http.StatusConflict, Message: "conflict"}, false},

		{"rate limited", &ResponseError{StatusCode: http.StatusTooManyRequests, Message: "slow down"}, true},
		{"server error", &ResponseError{StatusCode: http.StatusInternalServerError, Message: "boom"}, true},
		{"bad gateway", &ResponseError{StatusCode: http.StatusBadGateway, Message: "boom"}, true},
		{"never reached a handler", errors.New("dial tcp 127.0.0.1:8080: connection refused"), true},

		// A status the partition does not name. It is transient by construction,
		// and that is the direction an unenumerated status has to fall: nothing
		// about a 418 establishes that the request itself is wrong.
		{"unenumerated status", &ResponseError{StatusCode: http.StatusTeapot, Message: "?"}, true},

		// The callers hand this whatever their own call returned, and one of them
		// wraps. errors.As, not a type assertion.
		{"wrapped permanent answer", fmt.Errorf("reading job: %w",
			&ResponseError{StatusCode: http.StatusNotFound, Message: "job not found"}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ErrorIsTransient(tc.err))
		})
	}
}
