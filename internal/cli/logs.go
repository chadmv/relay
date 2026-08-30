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

	"github.com/jackc/pgx/v5/pgtype"
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
// depends on, and this file is registered as a slicing site in BOTH lockstep
// guards - internal/store/tasks_status_vocabulary_lockstep_test.go for
// taskIsTerminal and internal/store/jobs_status_vocabulary_lockstep_test.go for
// jobIsTerminal. Read the matching one before adding a status to either.
//
// The second guard exists because the first one is not one: it reads
// tasks_status_check and nothing else, so from the day jobIsTerminal was
// registered until the day the jobs guard was written, the registration was
// prose that could never fire.
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

// canonicalJobID renders jobID in the one spelling the server uses for it, and
// returns jobID unchanged when it is not a UUID at all (a typo, or a fixture id -
// the server answers 400/404 for those and nothing downstream depends on the
// value).
//
// The server accepts MORE spellings than it emits. parseUUID is
// pgtype.UUID.Scan, which takes hex case-insensitively and takes the dashless
// 32-char form too; uuidStr renders `%08x-%04x-%04x-%04x-%012x`, always
// lowercase and always dashed. So `relay get 7E660488-...` and `relay get
// 7e660488123443218888abcdefabcdef` are both working commands against the same
// job.
//
// ONE thing here still reads the id and does not tolerate a second spelling:
// jobSnapshotUnusable compares the body's id against ours, so a canonical answer
// to a non-canonical request reads as a response about a different job. That
// comparison is entirely client-side and no server change can reach it, which is
// why this function is not redundant and must not be deleted.
//
// The SECOND reader was handleEvents, and it stopped being one on 2026-08-30:
// canonicalJobIDFilter (internal/api/events.go) now renders an accepted
// `?job_id=` into the one spelling every publisher emits, so a non-canonical
// subscription matches. `?job_id=` is still not VALIDATED - an unparseable id
// passes through and still buys an open, permanently empty stream on a
// connection with no heartbeat and no server-side timeout - and this function
// still keeps a non-canonical spelling out of the request line against an OLDER
// relay-server, which a CLI built from this tree may be pointed at.
//
// Canonicalising ARGV, before either request line is built, is what covers both.
// Adopting the id from the first usable snapshot instead would fix the
// comparison and could not fix the subscription, since the subscription is
// established before any snapshot is read - and reading one first to learn the id
// reopens the terminal-before-subscribe race the snapshot exists to close.
//
// Only the PARSE half is shared. This calls the same pgtype.UUID.Scan the
// server's parseUUID calls, so what counts as a uuid cannot drift. The RENDER
// below is a hand-written duplicate: the format string is the sixth production
// copy of it (internal/api/server.go, cmd/relay-server/main.go,
// internal/metrics/sweep.go, internal/scheduler/dispatch.go,
// internal/worker/handler.go, plus this one), byte-identical today and unified
// by nothing.
//
// Both directions are now pinned, but by two independent literals rather than by
// any relationship between the functions. TestWatchJobLogs_NonCanonicalJobID_-
// IsResolvedNotRejected hard-codes the expected spelling rather than deriving it
// from this function, so a change HERE goes red; and since 2026-08-30
// TestCanonicalJobIDFilter (internal/api/events_test.go) hard-codes the same
// canonical spelling on the server side, so a change to internal/api's uuidStr
// goes red too - measured, by rendering it uppercase and watching that test
// fail. Still no test relates the two functions, and the four other unexported
// copies of the format string remain related to nothing at all.
func canonicalJobID(jobID string) string {
	var u pgtype.UUID
	if err := u.Scan(jobID); err != nil || !u.Valid {
		return jobID
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// jobPath and jobEventsPath build this command's two jobID-bearing request
// lines. Both exist so the escaping is decided once, in the one place that knows
// which context the id lands in.
//
// jobID is args[0]: typed by whoever ran the command, or pasted from wherever
// they got it. It is the only untrusted input this file has, and it reached all
// three of these request lines raw while printTaskLogs escaped the ONE id that
// was a gen_random_uuid() primary key the server had just handed back.
//
// The two contexts need different escapers and the difference is not cosmetic.
// In a path a `/` reroutes the request to another endpoint on the same host with
// the operator's bearer token attached. In a query string an `&` or `#`
// truncates the request or injects a parameter the handler will read.
func jobPath(jobID string) string {
	return "/v1/jobs/" + url.PathEscape(jobID)
}

func jobEventsPath(jobID string) string {
	return "/v1/events?job_id=" + url.QueryEscape(jobID)
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
	// Once, before either request line is built and before anything compares an
	// id. See canonicalJobID for the ONE reader here that still needs it, and for
	// why the second one stopped being a reader on 2026-08-30.
	jobID = canonicalJobID(jobID)
	taskNames := make(map[string]string)
	printed := make(map[string]bool)
	// What the SUBSCRIBE-time snapshot settled, which is what the defer at the
	// bottom needs to decide whether a reconcile is owed. There are three answers
	// and the third one used to be indistinguishable from the second.
	//
	//   - It established the terminal job status. It IS the reconcile; a second
	//     read can only add a way to fail. snapshotWasFinal.
	//   - It established that the job is still running. The stream is ahead of us
	//     and will report the end; the reconcile is owed when it does. Neither
	//     flag.
	//   - It established NOTHING: the read failed, or the body could not be this
	//     job's answer. subscribeEstablishedNothing. See the defer.
	var snapshotWasFinal, subscribeEstablishedNothing bool
	// The subscribe-time read's fourth outcome, and the only one that is not about
	// what the snapshot settled: the read failed in a way no later read and no
	// stream frame can change. See onSubscribed and the defer.
	var fatalSnapshotErr error

	// emit prints one task's log and reports an incomplete one on errOut. One
	// diagnostic per failing task, naming the task, the task id and the last
	// seq written; the error's own text is the reason it stopped.
	//
	// It returns whether the whole log reached out. That boolean has exactly one
	// job: it stops a caller with its OWN reason to call the same task's log
	// incomplete from COUNTING that task twice. incompleteTasks counts tasks, not
	// reasons, and one task with two things wrong with it is still one task.
	//
	// It decides no diagnostic. A caller's reason is a different fact about the
	// same rows and the operator is owed both of them.
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
			whole := emit(t.ID, taskNames[t.ID])
			if terminal {
				continue
			}
			// Said whatever emit said, because it is a different fact: emit
			// reports that the FETCH stopped early, this reports that the rows
			// themselves were never final. An operator told only the first
			// concludes the task had finished and its log was merely unavailable.
			fmt.Fprintf(errOut,
				"relay: task %s (%s) was still %s when job %s ended, so its log is not final\n",
				taskNames[t.ID], t.ID, t.Status, jobID)
			// Counted only if emit did not already count it. This is the whole
			// purpose of emit's return value.
			if whole {
				completeness.incompleteTasks++
			}
		}
	}

	// readJobSnapshot makes one GET /v1/jobs/{id} and reports why the result cannot
	// be this job's authoritative answer, "" when it can.
	//
	// A transport failure and a 200 whose body cannot be an answer are ONE return
	// value because they are one fact to every caller: nothing was established.
	// Keeping them apart is what let the unusable body be handled as a lesser
	// problem than the failed read in one reader and an equal one in the other.
	//
	// The third return is the separate question of whether asking again could ever
	// change the answer, and it is non-nil only when it CANNOT: the job does not
	// exist, the id is malformed, the token is rejected. That is not a thinner
	// version of "nothing was established" - it is the opposite, an answer so
	// definite that no later read and no stream can improve on it. The partition
	// is relayclient.ErrorIsTransient, shared with internal/mcp's wait loop, which
	// makes the same decision about the same endpoint.
	//
	// An unusable BODY is never definite here. A 200 that carried no task list is
	// exactly the transient server-side failure the retry below exists for, and a
	// body describing another job is a server bug rather than a fact about the id.
	readJobSnapshot := func() (jobResp, string, error) {
		var job jobResp
		if err := c.Do(ctx, "GET", jobPath(jobID), nil, &job); err != nil {
			if !relayclient.ErrorIsTransient(err) {
				return jobResp{}, err.Error(), err
			}
			return jobResp{}, err.Error(), nil
		}
		return job, jobSnapshotUnusable(job, jobID), nil
	}

	// onSubscribed runs after the SSE subscription is live. Any task or job already
	// terminal at this point would never produce a future event, so we GET a snapshot
	// and handle it here. Returning false stops the stream when the job is done.
	onSubscribed := func() bool {
		job, why, fatal := readJobSnapshot()
		if why != "" && fatal == nil {
			// Ask once more before giving up on this read, because on the input
			// that matters it is the ONLY reader: for a job that went terminal
			// before the subscription existed, the broker has no replay and no
			// frame is ever coming. Falling straight through would wait on
			// something that cannot arrive - handleEvents holds the connection
			// open with no heartbeat and no server-side timeout, and this
			// command's context has no deadline - so the operator gets no output
			// and no error, forever. That is the symptom this whole slice exists
			// to fix, arriving through the guard written to make failures visible.
			//
			// One retry, not a loop: a transient ListTasksByJob failure is exactly
			// what the server-side half of this slice turned from a silent 200
			// into a 500, and it is what this covers. A TRANSIENT read that fails
			// twice leaves the client genuinely unable to tell a finished job from
			// a running one, so it falls through to the stream and waits - which is
			// the correct behaviour for the running case and is the residual hole
			// for the finished one. Closing that needs the snapshot re-read while
			// the stream is live, which this shape cannot express.
			//
			// That inability is what justifies the fall-through, and it is a claim
			// about transient failures ONLY. Against a 404, a 400, a 401 or a 403
			// the client knows definitively, and the branch below is where it says
			// so instead of waiting.
			job, why, fatal = readJobSnapshot()
		}
		if fatal != nil {
			// A definite answer ends the watch here, and this is the arm the whole
			// slice exists for. The stream cannot improve on it: handleEvents
			// canonicalises `?job_id=` but still does not VALIDATE it (its own
			// comment, internal/api/events.go), so an id naming no job - whether
			// it parses or not - gets an open, permanently empty stream with no
			// heartbeat and no server-side timeout, against a context cmd/relay
			// gives no deadline. Falling through would print nothing on either
			// stream until Ctrl-C - and a well-formed uuid that names no job is
			// the likeliest thing an operator mistypes into this command.
			//
			// The error is carried out through the defer rather than returned from
			// here, because this is a StreamEvents callback and its only return is
			// the bool that stops the stream.
			fatalSnapshotErr = fatal
			return false
		}
		if why != "" {
			// Nothing was established, so nothing is printed and no status is
			// taken - an unusable body must be rejected HERE and not only in the
			// reconcile, or a terminal status read off a body that cannot be an
			// answer would end the watch, print nothing and exit 0.
			//
			// taskNames stays empty on this path, so any subsequent stream task
			// event prints with a blank name; the stream's payload carries only
			// id/status, never the name, and the reconcile refreshes it.
			//
			// The flag is the whole point. It is what makes the deferred reconcile
			// reachable from here: this reader established nothing, so a reconcile
			// is owed the moment the stream ends, exactly as it is owed when the
			// stream reports the end itself.
			subscribeEstablishedNothing = true
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

	// reconcileFinalSnapshot runs when the STREAM is what reported the terminal
	// job status, and it is what makes the exit code's completeness claim true on
	// that path. When the subscribe-time snapshot reported it instead, that
	// snapshot IS this read and no second one is made - see the defer below.
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
		// Whether the failure was definite is not a question here. onSubscribed
		// asks it to decide between waiting on the stream and answering now, and
		// there is no stream left to wait on: every failure gets the same
		// fail-closed treatment below.
		job, why, _ := readJobSnapshot()
		if why != "" {
			// Unlike the onSubscribed snapshot, this failure is NOT survivable by
			// falling through. There the stream was still ahead and could deliver
			// everything the snapshot would have; here there is no stream left, so a
			// silent fall-through is exactly the never-attempted omission this
			// function exists to close. Fail closed - say so on errOut and refuse to
			// claim completeness - rather than exiting 0 on an unverified guess.
			completeness.unreconciled = true
			// The message says what this read was FOR, not what it believes about
			// the job. Two paths arm this reconcile and only one of them observed a
			// terminal status; the other is the subscribe-time snapshot that
			// established nothing, where the stream then ended without a word.
			// "after it finished" on that path contradicts the error printed
			// immediately beside it, which says the job may still be running.
			fmt.Fprintf(errOut,
				"relay: could not re-read job %s to find any task whose log went unprinted, so some logs may be missing: %s\n",
				jobID, why)
			return
		}
		emitSnapshot(job)
		// This read is the freshest thing the watch has, and the status came with
		// it. A job that emitted a `failed` frame and was then retried can be
		// `done` or `running` by now, and reporting the frame's status is stale by
		// choice when the fresher one is already in hand.
		//
		// Only a TERMINAL status is adopted. watchOutcomeError's vocabulary is
		// terminal-only, so "job finished running" is not a sentence it can
		// produce; and a non-terminal status here means the job was restarted
		// AFTER the frame this watch saw, which does not make that frame untrue
		// about the moment it described.
		if jobIsTerminal(job.Status) {
			finalStatus = job.Status
		}
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
	// It is skipped for exactly ONE of the three things the subscribe-time
	// snapshot can settle: that the job was already terminal. Then it is a pure
	// duplicate request. That snapshot is authoritative, and emitSnapshot has
	// already printed every task it listed - note the argument is that, and NOT
	// that every task in a terminal job is terminal. The stronger claim is what
	// this file used to make, and emitSnapshot exists partly to handle the case
	// that disproves it: once the job is terminal a non-terminal task is printed
	// too, and flagged. Making the request anyway can only add a way to fail, and
	// it did: a 500 on the duplicate read turned a run that printed every line
	// into exit 1 telling the operator their logs may be missing. `relay logs
	// <finished-job>` is this command's dominant invocation.
	//
	// The other two both owe a reconcile, and the gate has to name them
	// SEPARATELY. Writing it as "a terminal status was observed" covers only the
	// first of them, because a snapshot that established nothing observes no
	// status at all - and gating on the absence of a status is what turned that
	// path into "connection lost" with no output for a job that had finished long
	// before the command started.
	//
	// The connection-lost error is decided here too, after the reconcile rather
	// than before it, so what the reconcile establishes replaces it. Deciding it
	// at the return statement instead made the skip above depend on the error
	// paths happening to write "" into finalStatus on their way past: true today,
	// and one edit to a return statement away from arming a reconcile on the
	// transport-error path. Nothing below the stream call reads or writes the
	// outcome now, so there is no ordering left to get wrong.
	defer func() {
		// A definite snapshot failure stopped the stream cleanly, so StreamEvents
		// returned nil and there is no transport error to prefer. It is the
		// command's answer: no reconcile (the same read would fail the same way)
		// and not "connection lost" (nothing was lost - the server said what it
		// knew and this is it).
		if err == nil && fatalSnapshotErr != nil {
			err = fatalSnapshotErr
		}
		if err != nil {
			// An error is the answer and the more actionable half; the per-task
			// diagnostics already reached errOut as they happened. Neither error
			// that reaches here can have a finalStatus beside it: StreamEvents
			// returns nil whenever a callback stops the stream, so a transport
			// error means no callback ever did, and the definite-failure branch
			// returns from onSubscribed above the line that takes a status.
			return
		}
		if !snapshotWasFinal && (finalStatus != "" || subscribeEstablishedNothing) {
			reconcileFinalSnapshot()
		}
		if finalStatus == "" {
			err = fmt.Errorf("connection lost — job %s may still be running", jobID)
		}
	}()

	err = c.StreamEvents(ctx, jobEventsPath(jobID), onSubscribed, handler)
	return finalStatus, completeness, err
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
		// primary key that came from the same server this request goes to, so a
		// crafted value does not reach here today; escaping means the argument does
		// not rest on that provenance.
		//
		// It does NOT remove the class from this command, and the comment used to
		// say it did. The class lives on jobID, which is args[0] - see jobPath and
		// jobEventsPath, which are where it is actually covered.
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
			//
			// And on that exact input the envelope's own total already settles the
			// question, so do not re-raise it. The caller prepends "(400 of 400
			// rows)" to this text, and "the log may be longer than 400 rows" in the
			// clause after it contradicts the pair that exists to resolve exactly
			// this ambiguity.
			if progress.total > 0 && progress.rows >= progress.total {
				return progress, fmt.Errorf(
					"truncated after %d pages - hit the client's page cap; the server reported %d rows and every one printed, but it had not yet reported the log as drained",
					maxLogPages, progress.total)
			}
			return progress, fmt.Errorf(
				"truncated after %d pages - hit the client's page cap; the log may be longer than %d rows, or the server may never report it as drained",
				maxLogPages, int64(maxLogPages)*relayclient.PageRequestLimit)
		}
		since = page.NextSeq
	}
}
