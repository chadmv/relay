//go:build integration

package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// The CLI sends only status and sort; it sends none of q, mine, since or
// until. The change is additive, so nothing it sends can break - but that is
// an argument, and this is the evidence.
//
// --status is the discriminating case: it is the only CLI path that reaches
// handleListJobs' status branch, whose list statement gained four predicates
// and whose count moved from a bare string argument to a Params struct. The
// bare case is covered elsewhere in this package; the empty case is here so a
// total that stopped being the filtered count cannot pass by rendering rows
// alone.
func TestIntegration_ListJobs_StatusFilterStillMatchesTheServer(t *testing.T) {
	s := startRelayServer(t)
	jobID := submitLaneJob(t, s)

	var bare bytes.Buffer
	require.NoError(t, doListJobs(testCtx(t), s.adminCfg(), nil, &bare))
	require.Contains(t, bare.String(), "Total: 1")
	require.Contains(t, bare.String(), jobID)
	require.Contains(t, bare.String(), "lane-job")
	require.Contains(t, bare.String(), s.AdminEmail)

	var matching bytes.Buffer
	require.NoError(t, doListJobs(testCtx(t), s.adminCfg(), []string{"--status", "pending"}, &matching))
	require.Contains(t, matching.String(), "Total: 1")
	require.Contains(t, matching.String(), jobID)
	require.Contains(t, matching.String(), "lane-job")

	var empty bytes.Buffer
	require.NoError(t, doListJobs(testCtx(t), s.adminCfg(), []string{"--status", "done"}, &empty))
	require.Contains(t, empty.String(), "Total: 0")
	require.NotContains(t, empty.String(), jobID)
}
