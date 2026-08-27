//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

// ─── Response-struct arity ────────────────────────────────────────────────────
//
// doListJobs --json and doGetJob --json do not proxy the server's bytes: they
// DECODE into internal/cli's jobResp/taskResp and RE-ENCODE that. Every field
// the mirror lacks is therefore deleted from output the user was told is JSON,
// silently and with no error anywhere. `command` was one instance; the tests
// below cover the rest of internal/api's jobResponse and taskResponse.

// enrichedJobSpec is the subject of the arity tests. It differs from
// laneJobSpec in exactly the ways the assertions need:
//
//   - THREE tasks, so total_tasks (3) and done_tasks (2, set by seedEnrichedJob)
//     differ from each other AND from a zero value AND from each other's
//     transposition - a defaulted or swapped pair is visible.
//   - labels, so `labels` carries a value only correct decoding produces.
const enrichedJobSpec = `{
  "name": "enriched-job",
  "priority": "high",
  "labels": {"crew": "nightshift", "tier": "platinum"},
  "tasks": [
    {"name": "t1", "command": ["echo", "one"]},
    {"name": "t2", "command": ["echo", "two"]},
    {"name": "t3", "command": ["echo", "three"]}
  ]
}`

// The list aggregate reduces task timing to MIN(tasks.started_at) and
// MAX(tasks.finished_at) (see ListJobsWithEmailPage). seedEnrichedJob puts the
// MIN on t1 and the MAX on t2, so the pair below can only be produced by an
// aggregate over both rows - a single row's timestamps never spell it. They are
// fixed, far apart, and neither is "now", so a zero time or a swap is visible.
var (
	wantJobStartedAt  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	wantJobFinishedAt = time.Date(2026, 1, 5, 6, 7, 8, 0, time.UTC)
)

const (
	// Carried by t1, the FIRST task. A discriminating value placed last cannot
	// detect an early-exit defect ([[reference_mutation_proof_position]]), and
	// t2/t3 keep the ordinary 0 so "present and 0" stays distinguishable from
	// "key absent".
	wantRetryCount       = 4
	wantScheduledJobName = "nightly-render-sweep"
)

// seedEnrichedJob submits enrichedJobSpec through the real POST /v1/jobs and
// then writes, straight through the pool, the state this deliberately unwired
// harness has no scheduler or agent to produce: task completion, task timing, a
// retry count, and a scheduled-job source. Every one of those feeds a
// jobResponse or taskResponse field.
func seedEnrichedJob(t *testing.T, s *relayServer) (jobID, schedID string) {
	t.Helper()
	var out, errOut bytes.Buffer
	require.NoError(t, doSubmit(testCtx(t), s.adminCfg(),
		[]string{"--detach", writeSpecFile(t, enrichedJobSpec)}, &out, &errOut))
	jobID = strings.TrimSpace(out.String())
	require.NotEmpty(t, jobID)

	// t1 and t2 done, t3 left pending: done_tasks(2) < total_tasks(3).
	// t1 owns the MIN start, t2 owns the MAX finish.
	_, err := s.Pool.Exec(t.Context(), `
		UPDATE tasks SET status = 'done', started_at = $2, finished_at = $3, retry_count = $4
		WHERE job_id = $1::uuid AND name = 't1'`,
		jobID, wantJobStartedAt, wantJobStartedAt.Add(time.Hour), wantRetryCount)
	require.NoError(t, err)
	_, err = s.Pool.Exec(t.Context(), `
		UPDATE tasks SET status = 'done', started_at = $2, finished_at = $3
		WHERE job_id = $1::uuid AND name = 't2'`,
		jobID, wantJobStartedAt.Add(2*time.Hour), wantJobFinishedAt)
	require.NoError(t, err)

	require.NoError(t, s.Pool.QueryRow(t.Context(), `
		INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, next_run_at)
		SELECT $1, u.id, '@daily', $2::jsonb, NOW()
		FROM users u WHERE u.email = $3
		RETURNING id::text`, wantScheduledJobName, enrichedJobSpec, s.AdminEmail).Scan(&schedID))
	_, err = s.Pool.Exec(t.Context(),
		`UPDATE jobs SET scheduled_job_id = $2::uuid WHERE id = $1::uuid`, jobID, schedID)
	require.NoError(t, err)

	return jobID, schedID
}

// requireRFC3339 asserts a JSON value is a timestamp string equal to want.
// Comparison is on the parsed instant, not the rendered text, because pgx hands
// back a time.Time in the connection's location and Go's encoder renders the
// offset it finds there.
func requireRFC3339(t *testing.T, v any, want time.Time, key string) {
	t.Helper()
	s, ok := v.(string)
	require.True(t, ok, "%s must be a timestamp string, got %#v", key, v)
	got, err := time.Parse(time.RFC3339Nano, s)
	require.NoError(t, err, "parsing %s=%q", key, s)
	require.True(t, want.Equal(got), "%s: want %s, got %s", key, want, got)
}

// TestIntegration_ListJobsJSON_CarriesTheListEnrichment pins the six fields
// GET /v1/jobs adds on top of the detail body, plus labels.
//
// internal/api uses ONE jobResponse for both endpoints; the enrichment block is
// populated only on list rows, by applyJobEnrichment. internal/cli's jobResp
// declared none of it, so `relay list --json` deleted all seven keys from every
// row it printed.
func TestIntegration_ListJobsJSON_CarriesTheListEnrichment(t *testing.T) {
	s := startRelayServer(t)
	_, schedID := seedEnrichedJob(t, s)

	var out bytes.Buffer
	require.NoError(t, doListJobs(testCtx(t), s.adminCfg(), []string{"--json"}, &out))

	var rows []map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))
	require.Len(t, rows, 1)
	row := rows[0]

	require.Equal(t, float64(3), row["total_tasks"], "total_tasks")
	require.Equal(t, float64(2), row["done_tasks"], "done_tasks")
	requireRFC3339(t, row["started_at"], wantJobStartedAt, "started_at")
	requireRFC3339(t, row["finished_at"], wantJobFinishedAt, "finished_at")
	require.Equal(t, schedID, row["scheduled_job_id"], "scheduled_job_id")
	require.Equal(t, wantScheduledJobName, row["scheduled_job_name"], "scheduled_job_name")
	require.Equal(t, map[string]any{"crew": "nightshift", "tier": "platinum"},
		row["labels"], "labels")
}

// TestIntegration_GetJobJSON_CarriesLabelsAndTaskRetryCount covers the detail
// body: jobResponse.Labels and taskResponse.RetryCount.
//
// It also pins that the CLI does NOT invent the list-only enrichment on the
// detail path. GET /v1/jobs/{id} runs toJobResponse and never applyJobEnrichment,
// and total_tasks/done_tasks carry no omitempty on the server, so the wire says
// 0/0 for a three-task job. That is internal/api's shape, and a mirror that
// computed len(tasks) here would stop being a mirror.
func TestIntegration_GetJobJSON_CarriesLabelsAndTaskRetryCount(t *testing.T) {
	s := startRelayServer(t)
	jobID, _ := seedEnrichedJob(t, s)

	var out bytes.Buffer
	require.NoError(t, doGetJob(testCtx(t), s.adminCfg(), []string{jobID, "--json"}, &out))

	var job map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &job))

	require.Equal(t, map[string]any{"crew": "nightshift", "tier": "platinum"},
		job["labels"], "labels")
	require.Equal(t, float64(0), job["total_tasks"], "detail rows carry the server's unenriched 0")
	require.Equal(t, float64(0), job["done_tasks"], "detail rows carry the server's unenriched 0")

	tasks, ok := job["tasks"].([]any)
	require.True(t, ok, "tasks must decode as an array, got %#v", job["tasks"])
	require.Len(t, tasks, 3)

	byName := map[string]map[string]any{}
	for _, raw := range tasks {
		tm, ok := raw.(map[string]any)
		require.True(t, ok, "each task must be an object, got %#v", raw)
		name, _ := tm["name"].(string)
		byName[name] = tm
	}
	require.Contains(t, byName, "t1")
	require.Contains(t, byName, "t2")

	require.Equal(t, float64(wantRetryCount), byName["t1"]["retry_count"],
		"t1 carries a retry_count only the server can have supplied")
	// Present-and-zero, not absent. Reading a missing key out of a map[string]any
	// yields nil, so this arm is what distinguishes "decoded 0" from "dropped".
	retryCount, ok := byName["t2"]["retry_count"]
	require.True(t, ok, "retry_count must be present on every task, not only nonzero ones")
	require.Equal(t, float64(0), retryCount)
}

// TestIntegration_GetJobJSON_LabelsWhenTheJobHasNone measures, against a real
// server, what `labels` actually is for a job submitted WITHOUT labels - the
// axis a presence-only sweep does not cover, and the exact axis the Python SDK
// sweep in PR #156 missed.
//
// jobcreate.CreateJobFromSpec does json.Marshal(spec.Labels) on a nil map, which
// is the four bytes `null`, and stores that in the JSONB column. rawJSON only
// substitutes `{}` for an EMPTY byte slice, so the literal null survives to the
// wire. `labels` is therefore null, not {}, and a client that types it as a
// required object breaks on it. json.RawMessage is the only mirror type that
// round-trips this faithfully.
func TestIntegration_GetJobJSON_LabelsWhenTheJobHasNone(t *testing.T) {
	s := startRelayServer(t)
	jobID := submitLaneJob(t, s)

	// The SERVER's own bytes, read past internal/cli entirely. Asserting only
	// the CLI's output would not settle this: a nil json.RawMessage marshals to
	// `null` too, so "null out" alone cannot tell a null the server sent from a
	// key the server omitted. This arm is what makes it a measurement.
	req, err := http.NewRequestWithContext(testCtx(t), "GET", s.BaseURL+"/v1/jobs/"+jobID, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+s.AdminToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(raw), `"labels":null`,
		"GET /v1/jobs/{id} emits a JSON null for an unlabelled job")

	// And the mirror reproduces it rather than laundering it into {}.
	var out bytes.Buffer
	require.NoError(t, doGetJob(testCtx(t), s.adminCfg(), []string{jobID, "--json"}, &out))
	require.Contains(t, out.String(), `"labels":null`)

	var job map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &job))
	v, ok := job["labels"]
	require.True(t, ok, "labels must be present even when the job has none")
	require.Nil(t, v, "an unlabelled job's labels is JSON null on the wire, not {}")
}
