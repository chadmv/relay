// internal/cli/logs.go
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	"relay/internal/relayclient"
)

// taskLogPage mirrors the envelope GET /v1/tasks/{id}/logs returns
// (handleGetTaskLogs, internal/api/tasks.go). The handler has written this
// object since 2026-05-08; the CLI decoded a bare array into a slice until
// 2026-08-26, which fails and printed nothing for three and a half months.
type taskLogPage struct {
	Items   []taskLogEntry `json:"items"`
	NextSeq int64          `json:"next_seq"`
	Total   int64          `json:"total"`
}

// taskLogEntry is one row. created_at is deliberately not decoded: the CLI does
// not print it, and an unused field is a maintenance claim this package cannot
// keep. Seq is decoded because the incomplete-log diagnostic names the last seq
// printed.
type taskLogEntry struct {
	Seq     int64  `json:"seq"`
	Stream  string `json:"stream"`
	Content string `json:"content"`
}

// LogsCommand returns the relay logs Command.
func LogsCommand() Command {
	return Command{
		Name:  "logs",
		Usage: "logs <job-id>  - print each task's log as the task finishes, until the job is done",
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			return doLogs(ctx, cfg, args, os.Stdout, os.Stderr)
		},
	}
}

func doLogs(ctx context.Context, cfg *Config, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: relay logs <job-id>")
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	status, completeness, err := watchJobLogs(ctx, c, args[0], out, errOut)
	if err != nil {
		return err
	}
	return watchOutcomeError(status, completeness)
}

// watchOutcomeError turns the two independent facts a watch produces - the job's
// final status and how complete the printed output is - into the command's error.
//
// The two are COMPOSED, not ranked. They are not more and less informative
// versions of the same thing; they are different things, and a message about logs
// alone invites the reader to conclude the job itself was fine. Against a server
// reporting a failed job whose logs route is down, "logs incomplete for 1 of the
// job's tasks" was the entire output, and nothing on any stream said the job had
// failed.
//
// silentError{} survives for the one case it was written for: a job that finished
// non-done with complete output, where the exit code is the whole message and
// Dispatch prints nothing.
//
// Note what does NOT reach here. Both callers return a non-nil watch error
// directly, so a stream that aborts discards the completeness alongside it. The
// real precedence is therefore transport error, then this composition, then
// silence - and discarding is right: the per-task diagnostics already reached
// errOut as they happened, and the transport error is the more actionable half.
func watchOutcomeError(status string, completeness logCompleteness) error {
	if completeness.complete() {
		if status != "done" {
			return silentError{}
		}
		return nil
	}
	if status != "done" {
		return fmt.Errorf("job finished %s; %s", status, completeness.reason())
	}
	return errors.New(completeness.reason())
}

// logCompleteness records why the printed output may not be the whole log. Its
// ZERO VALUE is the claim the exit code makes: every task that reached a terminal
// state had its log printed in full.
//
// It is a struct rather than the plain count it replaced because the count could
// only describe logs that were ATTEMPTED AND FAILED, and was silent about logs
// that were never attempted at all - which is the larger of the two holes and the
// one this command shipped with. Both fields have to be able to say "incomplete",
// or the exit code overstates what was printed.
type logCompleteness struct {
	// incompleteTasks counts tasks whose log is not fully on stdout. Two
	// distinct things put a task here and they share a count because they make
	// the same claim about the output: the fetch (or the write) errored partway
	// through, or the task was still non-terminal in the job's final snapshot so
	// what was printed was never final. The per-task diagnostic on errOut is
	// where the two are told apart.
	incompleteTasks int
	// unreconciled is set when the authoritative final job snapshot could not be
	// read, so the CLI cannot know whether any task's log went unprinted.
	unreconciled bool
}

func (lc logCompleteness) complete() bool {
	return lc.incompleteTasks == 0 && !lc.unreconciled
}

// reason is the message for a non-complete outcome, and is empty when complete.
func (lc logCompleteness) reason() string {
	switch {
	case lc.incompleteTasks > 0 && lc.unreconciled:
		return fmt.Sprintf(
			"logs incomplete for %d of the job's tasks, and the job's final task list could not be read",
			lc.incompleteTasks)
	case lc.incompleteTasks > 0:
		return fmt.Sprintf("logs incomplete for %d of the job's tasks", lc.incompleteTasks)
	case lc.unreconciled:
		return "logs may be incomplete: the job's final task list could not be read"
	}
	return ""
}

// taskIsTerminal and jobIsTerminal slice the two status vocabularies this command
// depends on, and this file is registered as a slicing site in
// internal/store/tasks_status_vocabulary_lockstep_test.go. Read that test before
// adding a status to either.
//
// A new TERMINAL task status omitted from taskIsTerminal means that task's log is
// never fetched, while the exit code still claims every task's log printed in
// full. A new terminal JOB status omitted from jobIsTerminal means relay logs
// hangs until the connection drops and then reports "connection lost".
// `preparing` is harmless at both because it is non-terminal; a task-level
// `cancelled` is the candidate that would bite.
func taskIsTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "timed_out"
}

func jobIsTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "cancelled"
}

// jobSnapshotUnusable reports why a decoded GET /v1/jobs/{id} body cannot be
// treated as the authoritative answer for jobID, and returns "" when it can.
//
// A 200 that decodes is not the same fact as an answer. jobResp's `tasks` field
// is `json:"tasks,omitempty"`, so a body that carries no task list decodes into
// a silently-empty slice - and handleGetJob discarded ListTasksByJob's error
// until 2026-08-26, so a pool exhaustion, statement timeout or cancelled context
// produced exactly that body behind a 200. Iterating it prints nothing, sets
// nothing, and returns having "reconciled": the same silently-zero decode
// against a body that does not carry what the code assumed that this command was
// fixed for, arriving through the function written to close it.
//
// Two things make a body unusable.
//
//   - No tasks. Every job in the database has at least one, and that is a
//     construction guarantee rather than an observation: jobcreate.CreateJobFromSpec
//     is the single job-creation path (CLAUDE.md's single job-spec pipeline
//     invariant) and it calls jobspec.Validate, which rejects a spec with zero
//     tasks. This holds for EVERY status, `cancelled` included. handleCancelJob
//     does not itself require the job to have tasks, but it cannot cancel a job
//     that was never created, so the creation-time floor is what governs and a
//     task-less cancelled job is not a case to accommodate. An empty list
//     therefore always means the response is not this job's task list.
//   - A different id. Neither reader otherwise checks that the body it reasons
//     about describes the job it asked for, and another job's tasks printed as
//     this job's output is a wrong answer, not a thin one.
func jobSnapshotUnusable(job jobResp, jobID string) string {
	if job.ID != jobID {
		return fmt.Sprintf("the response describes job %q, not %q", job.ID, jobID)
	}
	if len(job.Tasks) == 0 {
		return "the response carried no task list"
	}
	return ""
}

// watchJobLogs subscribes to SSE events for jobID, then takes a snapshot so a job
// that went terminal before the subscribe is still caught (the broker has no replay).
// When a task reaches a terminal state its logs are fetched and printed once.
//
// When the STREAM is what reports the terminal status, the authoritative task
// list is re-read and any task not yet printed is printed then - see
// reconcileFinalSnapshot for why the stream alone is not enough, and the defer
// near the bottom for why that re-read is skipped when the subscribe-time
// snapshot is what ended the watch instead.
//
// Returns the final job status ("done", "failed", or "cancelled"), how complete
// the printed output is, and any error.
//
// A log failure never aborts the watch: the remaining tasks still stream and
// print. It is reported on errOut immediately and counted, and doLogs turns an
// incomplete outcome into a non-silent error.
// The returns are NAMED because the reconcile below is armed by a defer that
// writes through them, not by a call at the bottom of the function.
func watchJobLogs(ctx context.Context, c *relayclient.Client, jobID string, out, errOut io.Writer) (finalStatus string, completeness logCompleteness, err error) {
	taskNames := make(map[string]string)
	printed := make(map[string]bool)
	// snapshotWasFinal records that the SUBSCRIBE-time snapshot, not the stream,
	// is what established the terminal job status - which is what makes the
	// reconcile redundant. See the defer below.
	var snapshotWasFinal bool

	// emit prints one task's log and reports an incomplete one on errOut. One
	// diagnostic per failing task, naming the task, the task id and the last
	// seq written; the error's own text is the reason it stopped.
	//
	// It returns whether the whole log reached out, so a caller with its OWN
	// reason to call that task's log incomplete does not count the same task
	// twice.
	emit := func(taskID, taskName string) bool {
		progress, err := printTaskLogs(ctx, c, taskID, taskName, out)
		if err != nil {
			completeness.incompleteTasks++
			fmt.Fprintf(errOut, "relay: logs for task %s (%s) are incomplete - stopped after seq %d%s: %v\n",
				taskName, taskID, progress.lastSeq, progress.ofTotal(), err)
			return false
		}
		return true
	}

	// emitSnapshot prints every not-yet-printed task in an authoritative job
	// snapshot. Both readers below share it; the only thing that varies between
	// them is the snapshot's own job status, which it reads for itself.
	//
	// While the job is RUNNING a non-terminal task is legitimately absent:
	// nothing is owed for it, its output is not final, and the stream will
	// deliver its terminal frame later.
	//
	// Once the job is TERMINAL the same task is a contradiction - the job says
	// everything is over and the task says it is not - and skipping it silently
	// printed nothing for it while the zero-value logCompleteness still claimed
	// the whole log was on stdout. That is unreachable today only by accident:
	// CancelJobTasks' allow-list is ('pending','queued','running','dispatched')
	// and omits `preparing`, which is already in the proto as
	// TASK_STATUS_PREPARING with the agent already streaming LOG_STREAM_PREPARE
	// chunks for it, so a cancelled job with a preparing task reaches this line
	// the day that status lands. Print the rows the server will give us - they
	// are what the operator came for - and say on errOut and in the exit code
	// that the log is not final. The failure direction has to be loud, not
	// optimistic.
	emitSnapshot := func(job jobResp) {
		jobDone := jobIsTerminal(job.Status)
		for _, t := range job.Tasks {
			taskNames[t.ID] = t.Name
		}
		for _, t := range job.Tasks {
			if printed[t.ID] {
				continue
			}
			terminal := taskIsTerminal(t.Status)
			if !terminal && !jobDone {
				continue
			}
			printed[t.ID] = true
			if emit(t.ID, taskNames[t.ID]) && !terminal {
				completeness.incompleteTasks++
				fmt.Fprintf(errOut,
					"relay: task %s (%s) was still %s when job %s ended, so its log is not final\n",
					taskNames[t.ID], t.ID, t.Status, jobID)
			}
		}
	}

	// onSubscribed runs after the SSE subscription is live. Any task or job already
	// terminal at this point would never produce a future event, so we GET a snapshot
	// and handle it here. Returning false stops the stream when the job is done.
	onSubscribed := func() bool {
		var job jobResp
		if err := c.Do(ctx, "GET", "/v1/jobs/"+jobID, nil, &job); err != nil {
			// Fall through to the stream; a transient snapshot error should not abort.
			// taskNames stays empty here, so any subsequent stream task event prints
			// with a blank name - acceptable on this degraded path (the stream event
			// payload carries only id/status, never the name). The final reconcile
			// below is the backstop that keeps the exit code honest.
			return true
		}
		if jobSnapshotUnusable(job, jobID) != "" {
			// An unusable body is treated exactly like a failed read, and it has to
			// be rejected HERE and not only in the reconcile. Nothing is printed and
			// no status is taken from it, so the watch stays on the stream and what
			// the operator ends up seeing is either the reconcile's diagnostic or
			// the connection-lost error - both non-silent. Accepting a terminal
			// status from such a body would end the watch, print nothing, and exit 0.
			return true
		}
		emitSnapshot(job)
		if jobIsTerminal(job.Status) {
			finalStatus = job.Status
			snapshotWasFinal = true
			return false
		}
		return true
	}

	// reconcileFinalSnapshot runs once a terminal job status has been observed,
	// and it is what makes the exit code's completeness claim true.
	//
	// A terminal job frame is the LAST thing the watch sees, and it is not preceded
	// by a task frame for every task. Two paths reach a terminal job with tasks that
	// never produced one:
	//
	//   - Cancel. handleCancelJob calls CancelJobTasks, which flips every
	//     non-terminal task to `failed` in a single statement, then publishes ONE
	//     event, for the job. No task frame is published for those tasks anywhere:
	//     the three production `Type: "task"` publish sites are two in
	//     internal/scheduler/dispatch.go and one in internal/worker/handler.go, and
	//     none of them is on the cancel path. Without this reconcile the command
	//     prints nothing at all for a cancelled job whose tasks ran and logged.
	//   - Ordering. handleTaskStatus publishes its task frame AFTER recomputing the
	//     job status, so with two tasks finishing concurrently the job's `done`
	//     frame can reach the subscriber ahead of the last task's own frame.
	//     internal/api/jobs.go already documents this class for a sibling publish:
	//     an SSE status is a cache hint, not a source of truth. This is what stops
	//     watchJobLogs from treating one as a source of truth.
	//
	// What a task still NON-TERMINAL in this snapshot means is emitSnapshot's
	// question, not this one's, and the answer differs by job status - see there.
	reconcileFinalSnapshot := func() {
		var job jobResp
		if err := c.Do(ctx, "GET", "/v1/jobs/"+jobID, nil, &job); err != nil {
			// Unlike the onSubscribed snapshot, this failure is NOT survivable by
			// falling through. There the stream was still ahead and could deliver
			// everything the snapshot would have; here there is no stream left, so a
			// silent fall-through is exactly the never-attempted omission this
			// function exists to close. Fail closed - say so on errOut and refuse to
			// claim completeness - rather than exiting 0 on an unverified guess.
			completeness.unreconciled = true
			fmt.Fprintf(errOut,
				"relay: could not re-read job %s after it finished, so some tasks' logs may be missing: %v\n",
				jobID, err)
			return
		}
		if why := jobSnapshotUnusable(job, jobID); why != "" {
			completeness.unreconciled = true
			fmt.Fprintf(errOut,
				"relay: could not re-read job %s after it finished, so some tasks' logs may be missing: %s\n",
				jobID, why)
			return
		}
		emitSnapshot(job)
	}

	handler := func(e relayclient.SSEEvent) bool {
		switch e.Type {
		case "task":
			var data struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(e.Data), &data) != nil {
				return true
			}
			if taskIsTerminal(data.Status) {
				if !printed[data.ID] {
					printed[data.ID] = true
					emit(data.ID, taskNames[data.ID])
				}
			}
		case "job":
			var data struct {
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(e.Data), &data) != nil {
				return true
			}
			if jobIsTerminal(data.Status) {
				finalStatus = data.Status
				return false
			}
		}
		return true
	}

	// Take the reconcile's obligation in the same breath as the call that can
	// create it. A terminal status can only be established from inside
	// onSubscribed or handler, both of which StreamEvents owns, and discharging
	// it a hundred lines later at a single call site left the window between the
	// two open to any early return added since: deleting the call is caught by a
	// test, but a `return` added above it would not be. Armed here, every exit
	// from this function passes through it.
	//
	// It is skipped when the SUBSCRIBE-time snapshot is what ended the watch,
	// because then it is a pure duplicate request. That snapshot is
	// authoritative and every task in a terminal job is terminal in it -
	// RecomputeJobStatus yields `running` while any is not, and CancelJobTasks
	// has already flipped the rest - so emitSnapshot has printed all of them
	// already. Making the request anyway can only add a way to fail, and it did:
	// a 500 on the duplicate read turned a run that printed every line into exit
	// 1 telling the operator their logs may be missing. `relay logs
	// <finished-job>` is this command's dominant invocation.
	//
	// The fail-closed argument that keeps this honest lives in onSubscribed: a
	// snapshot that cannot be a valid answer never sets finalStatus, so it never
	// reaches this skip.
	defer func() {
		if finalStatus == "" || snapshotWasFinal {
			return
		}
		reconcileFinalSnapshot()
	}()

	if err = c.StreamEvents(ctx, "/v1/events?job_id="+jobID, onSubscribed, handler); err != nil {
		return "", completeness, err
	}
	if finalStatus == "" {
		return "", completeness, fmt.Errorf("connection lost — job %s may still be running", jobID)
	}
	return finalStatus, completeness, nil
}

// maxLogPages bounds the paging loop against a server whose next_seq keeps
// advancing but which never reports the log as drained. 10000 pages at 200 rows
// is 2,000,000 rows: this is a hang bound, not a product limit - no real task
// log approaches it, and reaching it means the server is misbehaving.
//
// It is a var rather than a const so a test can shrink it, following this
// package's testability-override convention (readPasswordFn, saveConfigFn,
// configFilePathFn).
var maxLogPages = 10000

// logProgress is how far printTaskLogs got. The caller owns the diagnostic's
// wording, so this carries numbers rather than a formatted string.
type logProgress struct {
	// lastSeq is the seq of the last row written, 0 when nothing was written.
	lastSeq int64
	// rows is how many rows were written.
	rows int64
	// total is the row count the server last reported for this task, 0 when no
	// page was ever decoded and the count is therefore unknown.
	total int64
}

// ofTotal renders how much of the task's log was printed, for a diagnostic that
// otherwise names only a stopping point: 4200 of 4201 rows and 4200 of 91340 rows
// are very different situations. It is empty whenever the server never told us a
// total, chiefly when the very first request failed, since "(0 of 0 rows)" would
// be noise dressed up as data.
func (p logProgress) ofTotal() string {
	if p.total <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%d of %d rows)", p.rows, p.total)
}

// printTaskLogs pages GET /v1/tasks/{id}/logs and writes every line to out as
// each page arrives. It returns how far it got and the reason it stopped early,
// or a nil error when the server reported the log as drained.
//
// The progress is returned rather than logged here because the caller owns the
// diagnostic's wording, and the numbers are what make that diagnostic actionable:
// they tell an operator where the output stops, what since_seq to resume from by
// hand, and how much of the log is missing.
//
// Printing per page rather than accumulating is deliberate twice over: memory
// stays O(one page) on a multi-hundred-megabyte log, and a failure on page N
// still leaves pages 1..N-1 on the output.
//
// since_seq is EXCLUSIVE server-side - GetTaskLogsPage is
// `WHERE task_id = $1 AND id > $2` - so the cursor is the previous page's
// next_seq verbatim. Never lastSeq+1: task_logs.id is a global BIGSERIAL, so
// when one task is logging alone its ids are contiguous and +1 skips the very
// next row.
//
// Beyond the server's own drained signal the loop has THREE stops, and all three
// are needed. The cursor is server-supplied and drives a client loop, and the
// provenance of a value says nothing about who controls its content or the timing
// of the writes behind it. An empty page that still advertises more catches a
// server the client cannot page at all; next_seq <= since catches a non-advancing
// cursor on the second request; maxLogPages catches an ever-advancing cursor that
// never drains, which neither of the other two can see.
func printTaskLogs(ctx context.Context, c *relayclient.Client, taskID, taskName string, out io.Writer) (logProgress, error) {
	var progress logProgress
	since := int64(0)
	for pages := 1; ; pages++ {
		// The id is escaped rather than concatenated. It is a gen_random_uuid()
		// primary key that came from the same server this request goes to, so this
		// is not exploitable today; escaping removes the class instead of resting
		// the argument on that provenance, since a crafted id would otherwise
		// reach a different endpoint on the host with the bearer token attached.
		path := fmt.Sprintf("/v1/tasks/%s/logs?since_seq=%d&limit=%d",
			url.PathEscape(taskID), since, relayclient.PageRequestLimit)
		var page taskLogPage
		if err := c.Do(ctx, "GET", path, nil, &page); err != nil {
			return progress, fmt.Errorf("fetching page %d: %w", pages, err)
		}
		progress.total = page.Total
		for _, l := range page.Items {
			// The write is checked, and a failure stops the loop. Unchecked, a
			// stdout that rejects every write (a full disk, a closed pipe, a `>`
			// redirect onto something that refuses) reaches this slice's own
			// symptom by the other door: the log pages to the end, nothing is
			// printed, and the command exits 0 claiming it printed everything.
			if _, werr := fmt.Fprintf(out, "[%s %s] %s\n", taskName, l.Stream, l.Content); werr != nil {
				return progress, fmt.Errorf("writing page %d: %w", pages, werr)
			}
			progress.lastSeq = l.Seq
			progress.rows++
		}
		// Break on next_seq, never on len(items) < limit: the two agree today,
		// but the second re-derives a rule the server already applied and
		// desynchronizes the moment the server's drain rule changes.
		if page.NextSeq == 0 {
			return progress, nil // the server says drained
		}
		// An empty page that does NOT report the log as drained is a server the
		// client cannot page. It is unreachable against the real handler, which
		// sets next_seq = 0 whenever len(items) < limit, so an empty page always
		// reports drained and the arm above returns first - including on the very
		// common case of a log whose length is an exact multiple of the page size,
		// where the final request legitimately comes back empty.
		//
		// Which is precisely why this must be an ERROR and not the silent nil it
		// used to be: the only server that reaches this line is one that is
		// misbehaving, and returning nil would launder that into a completeness
		// claim the client cannot support.
		if len(page.Items) == 0 {
			return progress, fmt.Errorf(
				"server returned an empty page without reporting the log as drained (next_seq %d after since_seq %d)",
				page.NextSeq, since)
		}
		if page.NextSeq <= since {
			return progress, fmt.Errorf(
				"server cursor did not advance (next_seq %d after since_seq %d)", page.NextSeq, since)
		}
		if pages >= maxLogPages {
			// Do not blame the server here. A log of exactly maxLogPages * 200 rows
			// drains correctly, but its last page is full and so carries a non-zero
			// next_seq: the client stops one request short of learning it was done,
			// having in fact printed every row.
			return progress, fmt.Errorf(
				"truncated after %d pages - hit the client's page cap; the log may be longer than %d rows, or the server may never report it as drained",
				maxLogPages, int64(maxLogPages)*relayclient.PageRequestLimit)
		}
		since = page.NextSeq
	}
}
