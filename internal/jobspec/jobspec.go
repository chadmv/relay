// Package jobspec defines the canonical job-template shape and validation
// rules used by both the REST API (POST /v1/jobs and POST /v1/scheduled-jobs)
// and any client that wants to validate a spec before submitting.
package jobspec

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// JobSpec is the canonical representation of a job template, used by both
// user-submitted jobs and scheduled-job templates. Matches createJobRequest.
type JobSpec struct {
	Name     string            `json:"name"`
	Priority string            `json:"priority"`
	Labels   map[string]string `json:"labels"`
	Tasks    []TaskSpec        `json:"tasks"`
}

// TaskSpec mirrors the existing taskSpec type, exported for reuse.
//
// A task carries one or more commands that the agent runs sequentially in the
// same workspace and environment. Specs may set EITHER the legacy single
// Command (a one-element argv) OR the multi-command Commands. Setting both is
// rejected by Validate; a single Command is normalized into a one-element
// Commands so downstream code only deals with Commands.
type TaskSpec struct {
	Name           string            `json:"name"`
	Command        []string          `json:"command,omitempty"`
	Commands       [][]string        `json:"commands,omitempty"`
	Env            map[string]string `json:"env"`
	Requires       map[string]string `json:"requires"`
	TimeoutSeconds *int32            `json:"timeout_seconds"`
	Retries        int32             `json:"retries"`
	DependsOn      []string          `json:"depends_on"`
	Source         *SourceSpec       `json:"source,omitempty"`
}

// SourceSpec describes how to prepare a workspace before the task runs.
type SourceSpec struct {
	Type               string      `json:"type"`
	Stream             string      `json:"stream,omitempty"`
	Sync               []SyncEntry `json:"sync,omitempty"`
	Unshelves          []int64     `json:"unshelves,omitempty"`
	WorkspaceExclusive bool        `json:"workspace_exclusive,omitempty"`
	ClientTemplate     *string     `json:"client_template,omitempty"`
}

// SyncEntry is a single depot path + revision to sync, or - with Exclude - a
// path to leave out of the sync.
//
// AN EXCLUDED ENTRY CARRIES NO REVISION. The revision its have-list preempt
// runs at comes from the include that covers it; a revision here would name a
// second one, and a preempt at the wrong revision does not merely fail to
// exclude - p4 syncs a file whose have-revision differs from the target in
// EITHER direction, so a preempt at a newer revision fetches the whole excluded
// subtree. validateSourceSpec refuses it.
type SyncEntry struct {
	Path    string `json:"path"`
	Rev     string `json:"rev"`
	Exclude bool   `json:"exclude,omitempty"`
}

var (
	revHeadRe    = regexp.MustCompile(`^#head$`)
	revCLRe      = regexp.MustCompile(`^@\d+$`)
	revLabelRe   = regexp.MustCompile(`^@[A-Za-z0-9._-]+$`)
	revNumRe     = regexp.MustCompile(`^#\d+$`)
	// The first character excludes '-': CreateStreamClient places this value
	// immediately after -t, so a leading hyphen makes it read as a flag rather
	// than as the flag's value. relay owns that argument shape.
	clientTmplRe = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
)

// maxRetries bounds TaskSpec.Retries. Chosen for a render and task farm: the
// failures a retry actually rescues are the ones that clear BETWEEN dispatches -
// a flaky network mount, a p4 sync that hit a transient server error, one
// unhealthy node in a fleet - and eleven attempts covers all of them.
//
// DO NOT RAISE THIS FOR A CONTENDED-RESOURCE CASE. A saturated license server is
// the argument that will be made, and it argues the other way: there is no
// backoff anywhere between a retry and its next dispatch, because
// IncrementTaskRetryCount returns the task to `pending` and handleTaskStatus
// immediately calls NotifyTaskSubmitted, which wakes the dispatcher. N retries
// against a saturated pool are therefore N immediate failures inside a few
// seconds; a larger N buys no waiting, only a faster burn. The instrument for
// contention is a reservation, not a retry count.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE, AND THE SAME GOES FOR EVERY BOUND BELOW.
// Validate runs on STORED scheduled_jobs.job_spec rows on the paths enumerated
// here, and the older version of this paragraph named two of them and got one of
// those wrong:
//   - schedrunner.fireOne calls Validate DIRECTLY, hoisted above the overlap
//     check, and then reaches it again inside jobcreate.CreateJobFromSpec. (It
//     used to reach it only through CreateJobFromSpec; that changed and this
//     comment did not.) Its failure branch records the message in last_error.
//   - api.handleRunScheduledJobNow calls it DIRECTLY, ahead of the transaction,
//     and then again inside CreateJobFromSpec. That direct call is the site that
//     decides the status code: it answers a stored spec's failure with 400 and
//     the validator's own message, where CreateJobFromSpec's error collapses into
//     a 500 that relayclient.ErrorIsTransient reads as retryable.
//   - schedrunner.ValidateStoredSpecsOnStartup, at boot, over every ENABLED
//     schedule, through schedrunner.ValidateStoredSchedule.
//   - api.handlePatchScheduledJob's clear-decision, through the same
//     ValidateStoredSchedule, on the EFFECTIVE row.
//   - jobcreate.CreateJobFromSpec itself, reached from the first two with stored
//     data.
//
// An env-tunable bound would therefore make retroactive schedule invalidation
// environment-dependent: the same stored spec fires on one replica's
// configuration and silently stops on another's, and lowering the knob would
// disable schedules with no signal anywhere. Two of the sites above make that
// worse in ways that postdate the original argument. The startup sweep WRITES the
// returned message into last_error, so the recorded failure text would become a
// function of which replica happened to boot, and the number in a stored,
// operator-facing string would stop matching the binary that reads it. And the
// PATCH clear-decision clears the record if and only if the effective row
// validates, so a PATCH served by a lenient replica would clear a record a strict
// replica immediately re-writes, and the operator would watch the failure
// flicker.
//
// A validation vocabulary shared by every ingest path is a property of the
// binary, exactly as the priority set is. THE PATHS ARE ENUMERATED, NOT COUNTED,
// and deliberately so - see internal/schedrunner/failure.go, which settled this
// question: a number goes stale silently and has no maintainer, where an
// enumeration goes stale loudly because a reader can check it. (The older version
// of this sentence said "four ingest paths"; there are api.handleCreateJob,
// api.handleCreateScheduledJob, api.handlePatchScheduledJob when the request body
// carries a job_spec, mcp.submit, and mcp.schedules_write on both create and
// update. The CLI is not one of them: doSubmit unmarshals the file into an opaque
// map[string]any and POSTs it, so it inherits every rule through the API.)
//
// TWO CLIENTS DO HOLD A PARALLEL VALIDATOR, and an earlier version of the sentence
// above claimed neither did. Neither is an ingest path - both still POST to the
// API, which stays the validator of record - but a rule ADDED here, and a message
// REWORDED here, has to be checked against them:
//
//   - python/src/relay/models.py. JobSpec.validate_spec, whose own docstring says
//     "Mirrors server-side ValidateJobSpec", plus the Task field validators
//     _name_required and _commands_argv_nonempty, reproduce five of this file's
//     message texts verbatim: "at least one task is required", "duplicate task
//     name: {name}", "task {name}: commands is required", "task name is required",
//     "commands[{i}]: argv must not be empty". Reword one of those here and a
//     Python caller gets two different texts for one condition depending on
//     whether the SDK or the server refused. That drift is already live on the
//     dependency rules, which is what the hazard looks like in practice: the SDK
//     emits "task {name}: unknown depends_on: {dep}" where this file emits
//     "unknown depends_on: %s" with no task prefix, and the SDK has a "cannot
//     depend on itself" message that has no counterpart here at all (a self-edge
//     reaches detectCycle instead). The count bounds are deliberately NOT mirrored
//     there; adding them would put a number from this file into a separately
//     released package.
//   - web/src/jobs/specTemplate.ts, validateSpecText - the /jobs/new editor's
//     pre-check. It is deliberately shallow and worded independently, so it cannot
//     drift on a message: it duplicates only the LOWER end of the task-count range
//     ("Spec must have a non-empty \"tasks\" array."). Its own comment records why
//     the upper bounds must not be copied into it.
const maxRetries = 10

// maxTimeoutSeconds bounds TaskSpec.TimeoutSeconds. Seven days: comfortably
// above the outer edge of a plausible relay task (a full P4 sync of a workspace
// that can exceed 1 TB, followed by a heavy bake, cook or render, is plausibly
// 24 to 72 hours) and far below the ~68 years int32's maximum buys today.
//
// THIS IS NOT RELAY_TASK_MAX_ASSIGNMENT AND MUST NOT BE COUPLED TO IT. The two
// are independent bounds. timeout_seconds is the TASK's own execution deadline,
// enforced by the agent (newRunner, internal/agent/runner.go) and by the
// watchdog's execution arm (ListOverdueAssignedTasks);
// RELAY_TASK_MAX_ASSIGNMENT is the COORDINATOR's absolute assignment bound and
// sweeps the task regardless of this value. A task whose own timeout exceeds the
// absolute cap is simply swept by the other arm. Seven days is deliberately
// ABOVE that knob's 24h default so the independence is visible in the numbers - a
// cap chosen below it would read as agreement and be maintained as if it were
// one. Do not derive this from that env var at runtime.
const maxTimeoutSeconds = 604800

// maxTasksPerJob bounds len(JobSpec.Tasks). A task in relay is a frame, a frame
// chunk, a build step, or one unit of a fan-out, so the realistic high end for a
// single submission is a full animation submitted one task per frame: a 1000 to
// 2000 frame sequence. Chunking frames - the usual practice, because per-task
// dispatch and workspace-prep overhead dominates for fast frames - puts the same
// sequence at a couple of hundred tasks. A build with a few hundred steps and a
// parameter sweep of a few hundred units both land far below. 5000 is 2.5x to 5x
// above that high end, so no submission a user plausibly wants is refused.
//
// IT STILL BINDS. A realistic task with a real command line is around 100 bytes
// of JSON, so maxBodyBytes (1 MiB, internal/api/server.go) already caps a
// realistic request near 10,000 tasks and this cap binds at half of that. Against
// minimal JSON - a short unique name and a one-element argv, on the order of 30
// to 35 bytes - the body permits on the order of 30,000, so this is roughly a 6x
// reduction on the worst case.
//
// DO NOT RAISE THIS WITHOUT A REFUSED REAL SUBMISSION. "The number looks small"
// is not the reason; a job somebody actually wanted to run being rejected is. And
// before raising it, look at the two costs this number stands in for, because
// fixing either is a better answer than a larger cap:
//   - jobcreate.CreateJobFromSpec inserts tasks ONE AT A TIME, one round trip
//     each, inside the caller's transaction. 5000 tasks is 5000 sequential round
//     trips - a slow request. 30,000 is a different thing entirely.
//   - store.GetEligibleTasks has NO LIMIT, and scheduler.Dispatcher.dispatch runs
//     it on every Trigger() and every 30 seconds, so a large pending backlog is
//     re-read in full on every tick until it drains. That is a fleet-wide
//     property, so a per-request cap bounds one request's contribution to it and
//     nothing about repetition.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.job_spec rows, and it applies identically
// to every bound in this file.
const maxTasksPerJob = 5000

// maxCommandsPerTask bounds len(TaskSpec.Commands) after normalization.
//
// `commands` exists so several steps share ONE prepared workspace and
// environment: sync, build, render, publish, clean up. The realistic shape is
// single digits. The plausible high end is a task that iterates a fixed list
// inside one prepared workspace - export N assets from a scene, bake N maps -
// which is tens. 500 is roughly 20x that.
//
// THIS IS THE CONCENTRATION CONTROL. It bounds how much sequential work a single
// request can pin to a single worker slot: at the bound, one task is 500
// subprocess spawns per attempt and 5500 across a full maxRetries budget.
//
// A USER AT THIS BOUND IS BEING TOLD TO USE THE BETTER MODEL, NOT TOLD NO. Past a
// few hundred, one task per unit is better anyway: separate tasks parallelize
// across the fleet, retry independently and report per-unit status, which is the
// entire point of a task graph. Tasks sharing a `source` reuse the same workspace
// (workspace_exclusive defaults to false), so splitting does not cost the
// workspace sharing that motivates `commands` in the first place.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.job_spec rows, and it applies identically
// to every bound in this file.
const maxCommandsPerTask = 500

// maxCommandsPerJob bounds the TOTAL number of commands across all of a job's
// tasks, accumulated during validation. IT IS THE ONE OF THE THREE THAT MOVES THE
// AGGREGATE NUMBER, and the other two do not produce it.
//
// TWO PER-AXIS CAPS WHOSE PRODUCT EXCEEDS WHAT THE BODY LIMIT ALREADY PERMITS
// REDUCE NOTHING IN AGGREGATE; they only change the shape of the worst case. The
// cost that matters - subprocess spawns, and one task_logs row per command from
// the agent's step marker alone, which nothing in this repo prunes - is
// total_commands x (1 + retries), and it does not care how the commands are
// distributed across tasks, because the retry budget is per task and every task's
// commands re-run on every attempt. maxTasksPerJob x maxCommandsPerTask is
// 2,500,000, roughly 15x more than a 1 MiB body can express with the cheapest
// RUNNABLE entry (see the range below), so with only those two in place the
// binding constraint on the total would remain maxBodyBytes - exactly as it was
// before any of these three existed.
//
// WHY THE ENTRY MUST BE RUNNABLE, since the arithmetic above turns on it:
// internal/agent/runner.go emits its step marker once per command EXECUTED, at
// the top of the loop body and AFTER the empty-argv guard, and a command whose
// Start or Wait fails breaks the loop. So a body full of entries that cannot
// execute costs one failed exec and one marker, not one per entry.
//
// THE CHEAPEST RUNNABLE ENTRY IS A RANGE, NOT A NUMBER, AND THE RANGE IS NOT A
// PROPERTY OF RELAY: it depends on what is on the agent's PATH and exits 0.
// `["ls"],` is 7 bytes and puts about 149,800 entries in a 1 MiB body; a
// one-character command that exits 0 is 6 bytes and puts about 174,800. An
// earlier version of this paragraph offered `["true"],` at 9 bytes and 116,000 as
// though it were the floor. It is not, and the error ran in the direction that
// UNDERSTATES the case for the bound. So: roughly 150,000 to 175,000 commands per
// body before this bound and 25,000 after, a 6x to 7x reduction, and 1.6 to 1.9
// million spawns across a full maxRetries budget taken to 275,000.
//
// THE PER-AXIS CAPS ARE NOT REDUNDANT ONCE THIS EXISTS. This one implies
// tasks <= 25000, since normalizeTaskCommands refuses a task with zero commands -
// which is weaker than maxTasksPerJob - and it says nothing about concentration,
// since 25,000 commands in one task satisfies it. Each of the three answers a
// different question: how long is the transaction and how big is the dispatcher's
// backlog; how much can one request pin to one worker slot; how much total work
// can one request buy.
//
// 25,000 IS PLACED TO KEEP THE LEGITIMATE SIDE CLEAR, not to make the adversarial
// number small. The legitimate high end is set by the many-tasks shape - thousands
// of tasks at a handful of commands each - rather than by the few-tasks shape,
// which tops out lower. The window between "what a real job needs" and "what 1
// MiB expresses" is only about 8x wide, because a legitimate command is a long
// string and an adversarial one is nine bytes, and a count bound cannot tell them
// apart.
//
// IT IS NOT A DoS CONTROL AND MUST NOT BE TIGHTENED AS IF IT WERE. Every figure
// above is per-request; repetition is bounded separately and per authenticated
// user by RELAY_JOB_SUBMIT_RATE_LIMIT, whose sliding window does bound the rate.
// What neither control bounds is the CUMULATIVE total: a caller submitting at
// exactly the permitted rate forever buys unbounded work over time. Tightening
// this to buy a constant factor there costs a refused real render, which has no
// workaround inside the product.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.job_spec rows, and it applies identically
// to every bound in this file.
const maxCommandsPerJob = 25000

// maxSyncExclusions bounds how many entries of one source spec may set
// `exclude`. Each one is an additional p4 subprocess inside the task's own
// prepare phase and an additional operator-facing log line, on the same
// per-entry axis
// docs/backlog/bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded
// already flags for `unshelves`. A realistic exclusion list is a handful of
// named heavy subtrees; 16 is several times that.
//
// IT ALSO BOUNDS A QUADRATIC. The coverage and swallow rules in
// validateSourceSpec compare every exclusion against every include, and the
// include side is bounded only by maxBodyBytes. The count is therefore checked
// BEFORE that loop runs, so an over-count spec is refused after one linear pass.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.job_spec rows, and it applies
// identically to every bound in this file.
const maxSyncExclusions = 16

func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 {
			return true
		}
	}
	return false
}

// Validate applies the same checks as POST /v1/jobs and normalizes each
// task's command form: a legacy single Command is rewritten into a one-element
// Commands and Command is cleared. Setting both Command and Commands is
// rejected. Validate mutates spec to apply normalization. Returns the first
// problem found, or nil.
func Validate(spec *JobSpec) error {
	if spec.Name == "" {
		return errors.New("name is required")
	}
	if len(spec.Tasks) == 0 {
		return errors.New("at least one task is required")
	}
	// The other end of the SAME range, deliberately adjacent to it, so a reader
	// changing either bound sees the other - the same adjacency argument that keeps
	// the retries and timeout_seconds bounds together. It is a JOB-level property,
	// so it has no task name to interpolate and does not belong in the per-task
	// loop. And it refuses the spec BEFORE the work it bounds: before nameSet is
	// allocated at len(spec.Tasks) capacity and before one normalizeTaskCommands
	// call per task.
	//
	// PRECEDENCE CONSEQUENCE, TAKEN DELIBERATELY. A spec that is over this bound
	// AND carries an invalid priority, a nameless task, a duplicate task name or a
	// bad command form now reports the task count, where the older code reported
	// whichever of those came first. No test can depend on the old order, since the
	// bound is new, and nothing reads these messages positionally. The wording
	// mirrors "at least one task is required" so the pair reads as one range.
	if len(spec.Tasks) > maxTasksPerJob {
		return fmt.Errorf("at most %d tasks are allowed, got %d", maxTasksPerJob, len(spec.Tasks))
	}
	// Priority is optional. Empty is allowed (jobcreate defaults it to "normal").
	// A non-empty value must be one of the known levels; this rejects typos that
	// would otherwise be stored silently and break the jobs_priority_check
	// constraint. This set MUST stay identical to jobs_priority_check in
	// migration 000019_status_vocabulary_checks.
	switch spec.Priority {
	case "", "low", "normal", "high":
		// ok
	default:
		return fmt.Errorf("invalid priority %q: must be low, normal, or high", spec.Priority)
	}
	nameSet := make(map[string]struct{}, len(spec.Tasks))
	totalCommands := 0
	for i := range spec.Tasks {
		ts := &spec.Tasks[i]
		if ts.Name == "" {
			return errors.New("task name is required")
		}
		if err := normalizeTaskCommands(ts); err != nil {
			return fmt.Errorf("task %s: %w", ts.Name, err)
		}
		if _, dup := nameSet[ts.Name]; dup {
			return fmt.Errorf("duplicate task name: %s", ts.Name)
		}
		nameSet[ts.Name] = struct{}{}
		// Bounds last in this loop body, so command-form and duplicate-name
		// errors keep the precedence they have today. That promise is about
		// precedence WITHIN one iteration. A bound has always been able to preempt
		// a form error on a LATER task - a bad `retries` at index 3 has always
		// outrun a duplicate name at index 90 - and the running total below is one
		// more instance of that rather than a change to it.
		//
		// THE COMMAND CHECK READS THE NORMALIZED VALUE, i.e. it sits AFTER
		// normalizeTaskCommands, which rewrites a legacy single Command into a
		// one-element Commands and clears Command. That covers both spellings by
		// construction. BE HONEST ABOUT WHAT THAT BUYS TODAY: a legacy Command can
		// only ever produce one command, so hoisting THIS check above the
		// normalization would behave identically and NO INPUT DISTINGUISHES THE TWO
		// POSITIONS. The position is correct rather than merely lucky the moment the
		// legacy form gains a second element. The ACCUMULATOR below is the half that
		// IS testable, because above the normalization a legacy task contributes 0.
		//
		// IT COMES BEFORE THE TOTAL so a task that is itself over the per-task cap
		// gets the specific, task-naming message rather than the job-level one. That
		// is the whole ordering argument; it says nothing about the retries and
		// timeout_seconds checks below, whose relative order is unchanged and
		// unimportant.
		if len(ts.Commands) > maxCommandsPerTask {
			return fmt.Errorf("task %s: at most %d commands are allowed, got %d",
				ts.Name, maxCommandsPerTask, len(ts.Commands))
		}
		totalCommands += len(ts.Commands)
		// CHECKED INSIDE THE LOOP, not after it, so a spec far over the budget is
		// refused partway through traversal rather than after a full pass over the
		// 150,000-plus entries a 1 MiB body can hold.
		//
		// JOB-LEVEL MESSAGE, NO TASK PREFIX. The budget is a property of the job,
		// and naming whichever task the accumulator happened to cross on would read
		// as an accusation against a task that may be entirely ordinary - the same
		// spec with its tasks in a different order would name a different one.
		//
		// NO "got" CLAUSE, AND THAT IS A DECISION RATHER THAN AN OMISSION. The other
		// two count messages report the offending number because they know it. This
		// one fires the moment the budget is exceeded and therefore does not know
		// the final total; printing the running count as if it were the total would
		// be false, and "got at least N" is honest but varies with task ordering for
		// the same spec while telling the operator nothing they can act on, since
		// the actionable number is the limit. Completing the pass to report an exact
		// total would trade the early refusal for a nicer message; not taken.
		if totalCommands > maxCommandsPerJob {
			return fmt.Errorf("at most %d commands in total across all tasks are allowed", maxCommandsPerJob)
		}
		// A nil TimeoutSeconds is SKIPPED, not defaulted: nil is the documented
		// "no deadline" and 0 is its second, equally valid spelling. Negatives
		// are rejected rather than documented as a third synonym, because
		// today's equivalence is an accident of THREE independent sites that
		// each happen to guard on `> 0`, none of them deriving that from the
		// others: newRunner sets a deadline only `if timeoutSec > 0`;
		// ListOverdueAssignedTasks's execution arm requires
		// `timeout_seconds IS NOT NULL AND timeout_seconds > 0`; and
		// overdueReason gates its time.Duration(*t.TimeoutSeconds)*time.Second
		// behind `*t.TimeoutSeconds > 0` in the same `if`, so it never renders
		// the negative duration it otherwise would. Three sites agreeing by
		// coincidence is not a contract, and nothing obliges a fourth to join
		// them - so the value is refused at the door rather than written down
		// as a synonym that only holds by luck.
		if ts.Retries < 0 || ts.Retries > maxRetries {
			return fmt.Errorf("task %s: retries must be between 0 and %d", ts.Name, maxRetries)
		}
		if ts.TimeoutSeconds != nil && (*ts.TimeoutSeconds < 0 || *ts.TimeoutSeconds > maxTimeoutSeconds) {
			return fmt.Errorf("task %s: timeout_seconds must be between 0 and %d (0 or omitted means no deadline)",
				ts.Name, maxTimeoutSeconds)
		}
	}
	for _, ts := range spec.Tasks {
		for _, dep := range ts.DependsOn {
			if _, ok := nameSet[dep]; !ok {
				return fmt.Errorf("unknown depends_on: %s", dep)
			}
		}
	}
	if cyc := detectCycle(spec.Tasks); len(cyc) > 0 {
		return fmt.Errorf("dependency cycle detected involving tasks: %s", strings.Join(cyc, ", "))
	}
	for _, ts := range spec.Tasks {
		if err := validateSourceSpec(ts.Source); err != nil {
			return fmt.Errorf("task %s: %w", ts.Name, err)
		}
	}
	return nil
}

// normalizeTaskCommands enforces command-form rules and collapses to Commands.
func normalizeTaskCommands(ts *TaskSpec) error {
	hasCommand := len(ts.Command) > 0
	hasCommands := len(ts.Commands) > 0
	switch {
	case hasCommand && hasCommands:
		return errors.New("set either command or commands, not both")
	case hasCommand:
		ts.Commands = [][]string{ts.Command}
		ts.Command = nil
	case !hasCommands:
		return errors.New("commands is required")
	}
	for i, argv := range ts.Commands {
		if len(argv) == 0 {
			return fmt.Errorf("commands[%d]: argv must not be empty", i)
		}
	}
	return nil
}

// detectCycle returns the sorted names of tasks that participate in or are
// blocked by a dependency cycle, or nil if the DependsOn graph is acyclic.
// Uses Kahn's algorithm: repeatedly remove tasks whose dependencies are all
// satisfied; any tasks left over are part of a cycle. Assumes every DependsOn
// name refers to an existing task (the caller checks this first).
func detectCycle(tasks []TaskSpec) []string {
	indegree := make(map[string]int, len(tasks))
	// dependents[x] = tasks that depend on x.
	dependents := make(map[string][]string, len(tasks))
	for _, ts := range tasks {
		indegree[ts.Name] = len(ts.DependsOn)
		for _, dep := range ts.DependsOn {
			dependents[dep] = append(dependents[dep], ts.Name)
		}
	}
	queue := make([]string, 0, len(tasks))
	for name, deg := range indegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	resolved := 0
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		resolved++
		for _, d := range dependents[name] {
			indegree[d]--
			if indegree[d] == 0 {
				queue = append(queue, d)
			}
		}
	}
	if resolved == len(tasks) {
		return nil
	}
	var stuck []string
	for name, deg := range indegree {
		if deg > 0 {
			stuck = append(stuck, name)
		}
	}
	sort.Strings(stuck)
	return stuck
}

func validateSourceSpec(s *SourceSpec) error {
	if s == nil {
		return nil
	}
	if s.Type != "perforce" {
		return fmt.Errorf("unsupported source type: %s", s.Type)
	}
	if s.Stream == "" {
		return errors.New("stream is required")
	}
	if !strings.HasPrefix(s.Stream, "//") {
		return errors.New("stream must start with //")
	}
	if hasControlByte(s.Stream) {
		return errors.New("stream must not contain control characters")
	}
	if len(s.Sync) == 0 {
		return errors.New("source.sync must have at least one sync entry")
	}
	excluded := 0
	for i, e := range s.Sync {
		if !strings.HasPrefix(e.Path, "//") {
			return fmt.Errorf("sync[%d].path must start with //", i)
		}
		// The prefix and containment rules constrain where a path STARTS and
		// what it sits under; nothing else looks at its interior. Two consumers
		// need this one to: perforce.SourceKey separates the exclusion set with
		// a NUL, so two different sets could canonicalise to one workspace key
		// and share a workspace; and Postgres refuses \u0000 inside jsonb, so a
		// path carrying one turns a job submission into a 500 rather than this
		// 400. A depot path has no legitimate use for a byte below 0x20.
		if hasControlByte(e.Path) {
			return fmt.Errorf("sync[%d].path must not contain control characters", i)
		}
		if e.Path != s.Stream &&
			e.Path != s.Stream+"/..." &&
			!strings.HasPrefix(e.Path, s.Stream+"/") {
			return fmt.Errorf("sync[%d].path must be under stream %s", i, s.Stream)
		}
		// The rev check is CARVED OUT for an exclusion, not relaxed: an empty
		// rev matches none of the four patterns and is still refused for an
		// include.
		if e.Exclude {
			excluded++
			if e.Rev != "" {
				return fmt.Errorf("sync[%d].rev: an excluded path carries no revision; "+
					"it is preempted at the revision of the include that covers it", i)
			}
			continue
		}
		if !(revHeadRe.MatchString(e.Rev) || revCLRe.MatchString(e.Rev) ||
			revLabelRe.MatchString(e.Rev) || revNumRe.MatchString(e.Rev)) {
			return fmt.Errorf("sync[%d].rev: invalid rev %q", i, e.Rev)
		}
	}
	if excluded > maxSyncExclusions {
		return fmt.Errorf("at most %d excluded sync paths are allowed, got %d",
			maxSyncExclusions, excluded)
	}
	for i, e := range s.Sync {
		if !e.Exclude {
			continue
		}
		covering := 0
		for _, inc := range s.Sync {
			if inc.Exclude {
				continue
			}
			// The swallow check runs against EVERY include, not the covering
			// one. An exclusion broader than an include is not covered BY that
			// include - coverage is directional - so checking only the coverer
			// would never fire for the case it exists to catch.
			if DepotPathCovers(e.Path, inc.Path) {
				return fmt.Errorf("sync[%d]: excluded path %s leaves included path %s "+
					"with nothing to sync; remove the include instead", i, e.Path, inc.Path)
			}
			if DepotPathCovers(inc.Path, e.Path) {
				covering++
			}
		}
		// EXACTLY ONE, not "at one revision". Two covering includes spelled
		// #head are literally equal and still ambiguous: the agent resolves
		// #head per path, and this function cannot see which changelists they
		// land on.
		if covering != 1 {
			return fmt.Errorf("sync[%d]: excluded path %s must be covered by exactly one "+
				"included path, found %d", i, e.Path, covering)
		}
	}
	for i, cl := range s.Unshelves {
		if cl <= 0 {
			return fmt.Errorf("unshelves[%d]: unshelve must be positive", i)
		}
	}
	if s.ClientTemplate != nil && !clientTmplRe.MatchString(*s.ClientTemplate) {
		return fmt.Errorf("invalid client_template %q", *s.ClientTemplate)
	}
	return nil
}

// DepotPathCovers reports whether outer's subtree contains inner. A trailing
// "/..." is p4's recursive wildcard and names the same subtree as the bare path,
// so it is trimmed from both sides before comparing.
//
// EXPORTED FOR A CALLER OUTSIDE THIS PACKAGE: the Perforce provider picks an
// exclusion's covering include with the identical predicate, and a second
// implementation there could disagree with this one about which include
// supplies the preempt revision. This package imports only the standard
// library, so an agent package importing it introduces no cycle.
//
// It is not perforce.PathPrefixOverlap and must not be replaced by it: that one
// is symmetric ("could these two touch"), this one is directional ("is inner
// inside outer"), and the direction is the whole of the coverage and swallow
// rules in validateSourceSpec.
func DepotPathCovers(outer, inner string) bool {
	o := strings.TrimSuffix(outer, "/...")
	n := strings.TrimSuffix(inner, "/...")
	return n == o || strings.HasPrefix(n, o+"/")
}
