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

// SyncEntry is a single depot path + revision to sync.
type SyncEntry struct {
	Path string `json:"path"`
	Rev  string `json:"rev"`
}

var (
	revHeadRe    = regexp.MustCompile(`^#head$`)
	revCLRe      = regexp.MustCompile(`^@\d+$`)
	revLabelRe   = regexp.MustCompile(`^@[A-Za-z0-9._-]+$`)
	revNumRe     = regexp.MustCompile(`^#\d+$`)
	clientTmplRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
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
// DO NOT MAKE THIS ENV-CONFIGURABLE. Validate runs on STORED scheduled-job specs
// on BOTH paths that materialize one: schedrunner.fireOne at fire time, which
// reaches Validate only through jobcreate.CreateJobFromSpec, and
// handleRunScheduledJobNow on demand, which calls it DIRECTLY through
// api.ValidateJobSpec ahead of the transaction and then again inside
// CreateJobFromSpec. That direct call is the site that decides the status code:
// it answers a stored spec's failure with 400 and the per-task message, where
// CreateJobFromSpec's error collapses into a 500. An env-tunable bound would
// therefore make
// retroactive schedule invalidation environment-dependent: the same stored spec
// fires on one replica's configuration and silently stops on another's, and
// lowering the knob would disable schedules with no signal anywhere. A
// validation vocabulary shared by four ingest paths is a property of the binary,
// exactly as the priority set is.
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
// Validate running on STORED scheduled_jobs.spec rows, and it applies identically
// to every bound in this file.
const maxTasksPerJob = 5000

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
		// errors keep the precedence they have today.
		//
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
	if len(s.Sync) == 0 {
		return errors.New("source.sync must have at least one sync entry")
	}
	for i, e := range s.Sync {
		if !strings.HasPrefix(e.Path, "//") {
			return fmt.Errorf("sync[%d].path must start with //", i)
		}
		if e.Path != s.Stream &&
			e.Path != s.Stream+"/..." &&
			!strings.HasPrefix(e.Path, s.Stream+"/") {
			return fmt.Errorf("sync[%d].path must be under stream %s", i, s.Stream)
		}
		if !(revHeadRe.MatchString(e.Rev) || revCLRe.MatchString(e.Rev) ||
			revLabelRe.MatchString(e.Rev) || revNumRe.MatchString(e.Rev)) {
			return fmt.Errorf("sync[%d].rev: invalid rev %q", i, e.Rev)
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
