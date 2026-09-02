//go:build integration

package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// --status drives handleListJobs status branch, whose count is asserted at
// zero on the empty case so a total that stopped being the filtered count
// cannot pass on rendered rows alone.
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
