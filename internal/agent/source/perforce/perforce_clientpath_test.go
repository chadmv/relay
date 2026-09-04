package perforce

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The prefix test is stream+"/" and not stream. The sharesATextualPrefix row is
// what discriminates against HasPrefix(depotPath, stream): //depot/xy is not
// under //depot/x, and rewriting it would synthesize a client path the operator
// never wrote. The error rows exist because the jobspec validator that
// establishes this precondition runs in the coordinator process and this
// function runs in the agent's, so the agent's only knowledge of the rule is
// the assumption.
//
// Every want is written out literally: an expectation computed from the inputs
// by the same rule would move with the thing under test.
func TestToClientPath(t *testing.T) {
	const client = "relay_h_ab12cd"
	for _, tc := range []struct {
		name    string
		stream  string
		path    string
		want    string
		wantErr bool
	}{
		{"pathEqualsTheStream", "//s/x", "//s/x", "//relay_h_ab12cd/...", false},
		{"streamWildcard", "//s/x", "//s/x/...", "//relay_h_ab12cd/...", false},
		{"strictlyUnderTheStream", "//s/x", "//s/x/sub/dir/...", "//relay_h_ab12cd/sub/dir/...", false},
		{"notUnderTheStreamAtAll", "//s/x", "//other/y/...", "", true},
		{"sharesATextualPrefixButIsNotUnder", "//depot/x", "//depot/xy/...", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toClientPath(client, tc.stream, tc.path)
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.path, "the error must name the offending path")
				require.Contains(t, err.Error(), tc.stream, "and the stream it was measured against")
				require.Empty(t, got, "a refused path must not also return a value")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
