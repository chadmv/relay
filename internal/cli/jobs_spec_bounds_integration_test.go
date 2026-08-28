//go:build integration

package cli

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_SubmitSurfacesTheServersOutOfRangeRefusal is the only test in
// the tree that proves the half-A message reaches a user. It runs the real
// `relay submit` entrypoint against the real internal/api server over real HTTP,
// so nothing between jobspec.Validate and the terminal is faked.
func TestIntegration_SubmitSurfacesTheServersOutOfRangeRefusal(t *testing.T) {
	s := startRelayServer(t)

	// Offending task FIRST, healthy task second: the message must name which.
	const overBudget = `{
	  "name": "over-budget",
	  "tasks": [
	    {"name": "bad-task", "command": ["echo", "x"], "retries": 11},
	    {"name": "healthy-task", "command": ["echo", "y"]}
	  ]
	}`

	var out, errOut bytes.Buffer
	err := doSubmit(testCtx(t), s.adminCfg(),
		[]string{"--detach", writeSpecFile(t, overBudget)}, &out, &errOut)
	require.Error(t, err, "an out-of-range spec must not submit successfully")

	var re *relayclient.ResponseError
	require.ErrorAs(t, err, &re,
		"the refusal must arrive as a typed ResponseError, or ErrorIsTransient would classify it as "+
			"retryable and a polling caller would keep asking")
	assert.Equal(t, http.StatusBadRequest, re.StatusCode,
		"400, so ErrorIsTransient reports it as PERMANENT")
	assert.Equal(t, "task bad-task: retries must be between 0 and 10", err.Error(),
		"the SERVER's own message must reach the user verbatim - this is what a validation error is FOR, "+
			"and it is the only assertion in the tree covering that whole path")
	assert.Empty(t, strings.TrimSpace(out.String()),
		"a refused submit must print no job id: doSubmit prints job.ID only after Do returns nil, and a "+
			"printed empty id is worse than none")

	// POSITIVE CONTROL on the same command and the same live server, at BOTH
	// boundaries. Without it, a doSubmit that had started failing on every spec
	// would pass every assertion above.
	const atTheBoundary = `{
	  "name": "at-the-boundary",
	  "tasks": [{"name": "t1", "command": ["echo", "ok"], "retries": 10, "timeout_seconds": 604800}]
	}`
	var okOut, okErr bytes.Buffer
	require.NoError(t, doSubmit(testCtx(t), s.adminCfg(),
		[]string{"--detach", writeSpecFile(t, atTheBoundary)}, &okOut, &okErr),
		"a spec AT the boundary must still submit")
	require.NotEmpty(t, strings.TrimSpace(okOut.String()),
		"the accepted submit must print its job id")
}
