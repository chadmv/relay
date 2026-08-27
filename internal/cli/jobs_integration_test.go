//go:build integration

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// laneJobSpec is the spec every jobs/logs test submits. The REQUEST key is
// `command` (singular) - internal/api's taskSpec accepts it and
// jobspec.Validate normalises it into Commands - while the RESPONSE key is
// `commands`. That asymmetry is real and is the subject of the next task.
const laneJobSpec = `{
  "name": "lane-job",
  "priority": "high",
  "tasks": [
    {"name": "t1", "command": ["echo", "hello-from-the-lane"]}
  ]
}`

// submitLaneJob submits laneJobSpec with --detach and returns the job id.
func submitLaneJob(t *testing.T, s *relayServer) string {
	t.Helper()
	var out, errOut bytes.Buffer
	require.NoError(t, doSubmit(testCtx(t), s.adminCfg(),
		[]string{"--detach", writeSpecFile(t, laneJobSpec)}, &out, &errOut))
	id := strings.TrimSpace(out.String())
	require.NotEmpty(t, id)
	return id
}

func TestIntegration_SubmitListGet_RoundTrip(t *testing.T) {
	s := startRelayServer(t)
	jobID := submitLaneJob(t, s)

	// list: the envelope's Total and the row the real handler produced.
	var listOut bytes.Buffer
	require.NoError(t, doListJobs(testCtx(t), s.adminCfg(), nil, &listOut))
	list := listOut.String()
	require.Contains(t, list, "Total: 1")
	require.Contains(t, list, jobID)
	require.Contains(t, list, "lane-job")
	require.Contains(t, list, "pending")
	// submitted_by_email is enrichment the list handler joins in; a fixture
	// marshalled from jobResp would agree with the decoder whatever the
	// handler did.
	require.Contains(t, list, s.AdminEmail)

	// get: the detail body, including the nested task list.
	var getOut bytes.Buffer
	require.NoError(t, doGetJob(testCtx(t), s.adminCfg(), []string{jobID}, &getOut))
	got := getOut.String()
	require.Contains(t, got, "ID:           "+jobID)
	require.Contains(t, got, "Name:         lane-job")
	require.Contains(t, got, "Priority:     high")
	require.Contains(t, got, "Status:       pending")
	require.Contains(t, got, "Submitted by: "+s.AdminEmail)
	require.Contains(t, got, "Tasks:")
	require.Contains(t, got, "t1")
}

// TestIntegration_GetJobJSON_CarriesTheTasksCommands is a REAL instance of the
// defect class this lane exists for, not a synthetic mutation.
//
// internal/api's taskResponse emits `commands` (a [][]string, per migration
// 000008_task_commands, which dropped tasks.command and added tasks.commands).
// internal/cli's taskResp decoded `command` as []string - wrong key and wrong
// type - so `relay get <job-id> --json` emitted "command":null and carried no
// task definition at all, for every job, since 2026-05. The human-readable path
// prints only name/status/worker, which is why nobody saw it.
//
// The assertion is on the exact compact-encoded substring because doGetJob's
// --json path is json.NewEncoder(w).Encode(job) with no indent, so the key and
// its value appear adjacent and unspaced.
func TestIntegration_GetJobJSON_CarriesTheTasksCommands(t *testing.T) {
	s := startRelayServer(t)
	jobID := submitLaneJob(t, s)

	var out bytes.Buffer
	require.NoError(t, doGetJob(testCtx(t), s.adminCfg(), []string{jobID, "--json"}, &out))
	got := out.String()

	require.Contains(t, got, `"commands":[["echo","hello-from-the-lane"]]`)
	require.NotContains(t, got, `"command":null`,
		"the CLI must not re-emit a key the server stopped sending at migration 000008")
}
