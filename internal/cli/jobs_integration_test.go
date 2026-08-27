//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
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
// internal/api's taskResponse emits `commands` as json.RawMessage
// (toTaskResponse: rawJSON(t.Commands), where store.Task.Commands is the raw
// []byte JSONB column added by migration 000008_task_commands, which dropped
// tasks.command and added tasks.commands). internal/cli's taskResp used to
// decode `command` (singular) as []string - wrong key and wrong type - so
// `relay get <job-id> --json` emitted "command":null and carried no task
// definition at all, for every job, since 2026-05. The human-readable path
// prints only name/status/worker, which is why nobody saw it. internal/cli's
// taskResp now declares Commands as json.RawMessage tagged `json:"commands"`,
// matching the server exactly - see internal/cli/jobs.go.
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
// silently and with no error anywhere. `command` was one instance.
//
// The named-key assertions below (TestIntegration_ListJobsJSON_..., etc.) do
// NOT cover the rest of internal/api's jobResponse and taskResponse - a
// mutation battery run against this file found 9 surviving json-tag renames
// (Env, Requires, TimeoutSeconds, Retries, DependsOn, WorkerID on
// taskResponse; SubmittedBy, CreatedAt, UpdatedAt on jobResponse) that no
// assertion here touches. `created_at` -> `createdAt` alone left the whole
// package green while `relay list` rendered "0001-01-01 00:00" for every job
// on the farm. TestIntegration_GetJobJSON_MirrorsServerBodyExactly and
// TestIntegration_ListJobsJSON_MirrorsServerItemsExactly below are the total
// guard that actually closes that gap - keep the named-key assertions for
// their failure messages, but treat the JSONEq tests as the ones that fail
// closed on the next field added to either struct.

// enrichedJobSpec is the subject of the arity tests. It differs from
// laneJobSpec in exactly the ways the assertions need:
//
//   - THREE tasks, so total_tasks (3) and done_tasks (2, set by seedEnrichedJob)
//     differ from each other AND from a zero value AND from each other's
//     transposition - a defaulted or swapped pair is visible.
//   - labels, so `labels` carries a value only correct decoding produces.
//   - t2 depends_on t1, so taskResponse.DependsOn carries a value on the wire.
//     Both DependsOn and WorkerID (assigned in seedEnrichedJob) are
//     omitempty: a job graph with no dependency and no task ever assigned to
//     a worker would leave those two keys absent from EVERY response this
//     lane produces, which would make a json-tag rename on either one
//     invisible to TestIntegration_GetJobJSON_MirrorsServerBodyExactly's
//     whole-body JSONEq comparison - an absent key compares equal to an
//     absent key no matter what either side calls it.
const enrichedJobSpec = `{
  "name": "enriched-job",
  "priority": "high",
  "labels": {"crew": "nightshift", "tier": "platinum"},
  "tasks": [
    {"name": "t1", "command": ["echo", "one"]},
    {"name": "t2", "command": ["echo", "two"], "depends_on": ["t1"]},
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
//
// The direct `UPDATE tasks SET status = 'done', ...` below, and the direct
// `UPDATE tasks SET worker_id = ...` beside it, both bypass
// UpdateTaskStatus's epoch/worker_id/terminality fence entirely - same
// uncovered-axis cost seedLogRows below states for AppendTaskLog's fence, and
// noted here for the same reason: so this lane is not later misread as
// exercising the status-write fence, which it does not. The worker_id write
// in particular sets a column no production statement other than
// ClaimTaskForWorker writes. It also produces a state production cannot
// reach on its own: a `pending` job with 2 of its 3 tasks `done`, since
// RecomputeJobStatus never runs against data written this way.
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

	// t1 carries a worker assignment too, so taskResponse.WorkerID is
	// non-empty on the wire - see the omitempty note on enrichedJobSpec.
	workerID := seedWorker(t, s, "enriched-job-worker", "enriched-job-worker-host", "offline")
	_, err = s.Pool.Exec(t.Context(), `
		UPDATE tasks SET worker_id = $2::uuid WHERE job_id = $1::uuid AND name = 't1'`,
		jobID, workerID)
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
	raw := rawGET(t, s, "/v1/jobs/"+jobID)
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

// rawGET issues an authenticated GET against the live server and returns the
// raw response body, bypassing internal/cli entirely.
func rawGET(t *testing.T, s *relayServer, path string) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(testCtx(t), "GET", s.BaseURL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+s.AdminToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return raw
}

// TestIntegration_GetJobJSON_MirrorsServerBodyExactly is a total guard the
// named-key tests above cannot be: it does not enumerate fields, it compares
// the CLI's --json output against the server's own response body for the
// SAME request, byte-for-byte modulo JSON-insignificant formatting. Any field
// jobResp/taskResp lacks, or any json tag that drifts from internal/api's,
// fails this the moment it exists - it does not wait for someone to add a
// named assertion for it. seedEnrichedJob is used so the compared body is
// not near-empty: labels, a retry count, task timing and a scheduled-job
// link are all present on the wire this test walks.
//
// NEITHER THIS TEST NOR TestIntegration_ListJobsJSON_MirrorsServerItemsExactly
// IS TOTAL ALONE - only their union is. handleGetJob runs toJobResponse and
// never applyJobEnrichment, so on THIS (detail) body jobResponse's
// StartedAt/FinishedAt/ScheduledJobID/ScheduledJobName are all zero,
// omitempty, and therefore absent from BOTH sides of this comparison - an
// absent key compares equal to an absent key no matter what either side
// calls it, so a json-tag rename on any of those four passes here for
// exactly the reason DependsOn/WorkerID's omitempty used to hide them from
// this same test before that was fixed. This test is the SOLE guard for the
// nested `tasks` array and every one of taskResponse's 11 tags (id, name,
// status, commands, env, requires, timeout_seconds, retries, retry_count,
// depends_on, worker_id) - jobResponse.Tasks is itself omitempty and absent
// from every list row, so TestIntegration_ListJobsJSON_MirrorsServerItemsExactly
// cannot see any of them. See that test's comment for the fields it alone
// guards.
func TestIntegration_GetJobJSON_MirrorsServerBodyExactly(t *testing.T) {
	s := startRelayServer(t)
	jobID, _ := seedEnrichedJob(t, s)

	raw := rawGET(t, s, "/v1/jobs/"+jobID)

	var cliOut bytes.Buffer
	require.NoError(t, doGetJob(testCtx(t), s.adminCfg(), []string{jobID, "--json"}, &cliOut))

	require.JSONEq(t, string(raw), cliOut.String(),
		"relay get --json must reproduce the server's body exactly; a field missing from "+
			"internal/cli's jobResp is silently deleted from output the user was told is JSON")
}

// TestIntegration_ListJobsJSON_MirrorsServerItemsExactly is the list-path
// half of the pair with TestIntegration_GetJobJSON_MirrorsServerBodyExactly -
// see that test's comment for why neither is total alone; only their union
// is. doListJobs --json prints only the decoded []jobResp
// (json.NewEncoder(w).Encode(jobs)), not the full page envelope, so the
// comparison is against the server's own `items` array rather than its whole
// body.
//
// This test is the SOLE guard for jobResponse's list-only enrichment block:
// started_at, finished_at, scheduled_job_id and scheduled_job_name.
// applyJobEnrichment populates all four only on list rows; the detail test
// never sees a non-zero value for any of them, so a json-tag rename on one
// would pass the detail comparison (absent key vs absent key) and is caught
// here instead, where seedEnrichedJob gives each of the four a real,
// non-default value.
func TestIntegration_ListJobsJSON_MirrorsServerItemsExactly(t *testing.T) {
	s := startRelayServer(t)
	seedEnrichedJob(t, s)

	raw := rawGET(t, s, "/v1/jobs")
	var envelope struct {
		Items json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))

	// Non-emptiness floor: JSONEq's []-vs-[] is vacuously true, so without this
	// a list handler regressed to `items: []` would pass the "total guard"
	// below while `relay list --json` printed nothing at all.
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(envelope.Items, &items))
	require.Len(t, items, 1, "the server's items array must carry the one job seedEnrichedJob submitted")

	var cliOut bytes.Buffer
	require.NoError(t, doListJobs(testCtx(t), s.adminCfg(), []string{"--json"}, &cliOut))

	require.JSONEq(t, string(envelope.Items), cliOut.String(),
		"relay list --json must reproduce the server's items array exactly; a field missing from "+
			"internal/cli's jobResp is silently deleted from output the user was told is JSON")
}

// createdColumnPattern matches ONLY the shape doListJobs' `time.Format(
// "2006-01-02 15:04")` produces. It also matches the zero time rendered the
// same way (0001-01-01 00:00), which is deliberate: that is what the
// TestIntegration_ListJobs_CreatedColumnIsRendered's own assertion below
// tests for directly, rather than relying on this pattern to reject it.
var createdColumnPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)

// TestIntegration_ListJobs_CreatedColumnIsRendered pins the human-readable
// render the JSONEq guards above do not cover: --json is one path through
// jobResp, but doListJobs' default table render is another, and it reads
// j.CreatedAt directly rather than round-tripping through JSON at all. The
// created_at json-tag mutant that JSONEq (and every named-key assertion in
// this file) misses entirely lands here - a mis-tagged CreatedAt decodes as
// the zero value, and doListJobs prints it as literally "0001-01-01 00:00"
// for every job on the farm.
//
// The assertion parses the matched substring and checks it is close to
// time.Now(), rather than only pattern-matching plus a substring deny-list on
// "0001-01-01 00:00": that pair matches the zero time TOO
// (`\d{4}-\d{2}-\d{2} \d{2}:\d{2}` fires on "0001-01-01 00:00" just as
// readily as on a real date), so the regex assertion alone proves nothing -
// only the NotContains line was ever doing the work, while the regex's
// failure message claimed a job it did not do. A within-window check catches
// both the tag-rename mutant AND a format regression (a differently shaped
// render fails to match the pattern at all, or fails to parse).
func TestIntegration_ListJobs_CreatedColumnIsRendered(t *testing.T) {
	s := startRelayServer(t)
	submitLaneJob(t, s)

	var out bytes.Buffer
	require.NoError(t, doListJobs(testCtx(t), s.adminCfg(), nil, &out))
	list := out.String()

	match := createdColumnPattern.FindString(list)
	require.NotEmpty(t, match, "CREATED must be rendered as a real timestamp")

	// ParseInLocation, not Parse: doListJobs' j.CreatedAt.Format carries
	// whatever offset the server's created_at arrived with, which is this
	// process's own Local zone (the server in this lane runs in-process, via
	// httptest - measured: the wire body carries e.g.
	// "2026-08-27T13:30:51-07:00", not a "Z"/UTC suffix). The rendered
	// "15:04" text therefore has no zone marker of its own; parsing it with
	// plain time.Parse silently defaults to UTC and manufactures a
	// same-digits-different-zone mismatch of exactly the local UTC offset,
	// which is not the defect this test exists to catch.
	got, err := time.ParseInLocation("2006-01-02 15:04", match, time.Local)
	require.NoError(t, err, "CREATED value %q must parse with doListJobs' own format", match)
	require.WithinDuration(t, time.Now(), got, 5*time.Minute,
		"CREATED must be close to now, not the zero time - a dropped/mis-tagged "+
			"created_at decodes to 0001-01-01 00:00, which is nowhere near this window")
}
