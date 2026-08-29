package jobcreate_test

import (
	"context"
	"fmt"
	"testing"

	"relay/internal/jobcreate"
	"relay/internal/jobspec"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// TestCreateJobFromSpec_RefusesAnOverBoundSpecBeforeTouchingTheDatabase is the
// OTHER HALF of internal/store/createtask_guard_test.go's argument.
//
// That guard's header says the bound is safe without a CHECK constraint because
// "the bound has exactly one enforcement point AND these two statements have
// exactly one caller". The guard checks only the second clause. Deleting the
// jobspec.Validate call from jobcreate.CreateJobFromSpec left the ENTIRE plain
// `go test ./...` lane green, and left the REST and CLI bounds tests green too,
// because internal/api validates before it ever calls in. The only thing that
// killed that mutant was the schedrunner hazard test, which is Docker-gated - so
// the guard that was deliberately untagged to join the plain lane proved half of
// its own sentence there. This is the missing half, in the same lane.
//
// THE NIL *store.Queries IS THE POINT, NOT A SHORTCUT. Validate runs before any
// field of q is read, so a correct implementation never dereferences it and this
// test needs no database. Delete the Validate call and the very next statements
// reach q.CreateJob on a nil receiver, which panics - so the mutant fails LOUDLY
// here instead of passing silently. Verified both ways, not assumed.
//
// The offending task is SECOND so this also cannot pass by way of a validator
// that only looks at Tasks[0].
//
// THE PANIC IS THE MUTANT'S FAILURE MODE, NOT THIS TEST'S MECHANISM - the
// property is established by the require.EqualError below, which is why the
// recover exists rather than being relied on. (Not by the three assertions as a
// set: the only return carrying that message returns store.Job{} and a nil task
// slice, so the other two are implied whenever the first passes.) An unrecovered
// panic takes down
// the whole jobcreate_test BINARY, so under the mutant every OTHER test in this
// package would silently not run. The package has one test file today, so that
// cost is currently zero and the recover is there so it stays zero as the
// package grows. Containing it here changes nothing about how loudly the mutant
// dies: it still fails, and now it names its own cause.
//
// The nil q stays nil. It is what proves a correct implementation never
// dereferences it, and a stub that answered queries would give that up to buy
// containment the recover already provides.
func TestCreateJobFromSpec_RefusesAnOverBoundSpecBeforeTouchingTheDatabase(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CreateJobFromSpec panicked instead of refusing the spec: %v\n"+
				"q is a nil *store.Queries, so this is what reaching a query on it looks like: "+
				"jobspec.Validate is no longer running before the first database call, which is "+
				"the entire enforcement of the retries and timeout_seconds bounds for a caller "+
				"that does not validate first.", r)
		}
	}()

	spec := jobspec.JobSpec{
		Name: "over-budget",
		Tasks: []jobspec.TaskSpec{
			{Name: "healthy-task", Command: []string{"echo", "y"}},
			{Name: "bad-task", Command: []string{"echo", "x"}, Retries: 11},
		},
	}

	job, tasks, err := jobcreate.CreateJobFromSpec(
		context.Background(), nil, spec, pgtype.UUID{}, pgtype.UUID{})

	require.EqualError(t, err, "task bad-task: retries must be between 0 and 10",
		"CreateJobFromSpec must refuse an out-of-range spec itself. tasks.retries carries no CHECK "+
			"constraint, so this call is the entire enforcement for any caller that does not validate "+
			"first - schedrunner.fireOne is one such caller")
	require.Empty(t, tasks, "a refused spec must insert no tasks")
	require.False(t, job.ID.Valid, "a refused spec must insert no job")
}

// TestCreateJobFromSpec_RefusesAnOverCountSpecBeforeTouchingTheDatabase is the
// count-axis sibling of the test above and exists for the same reason: this call
// is the entire enforcement for any caller that does not validate first.
// schedrunner.fireOne is such a caller for its SECOND reach at Validate, and
// CreateJobFromSpec is retroactivity site 5 - the one whose every error collapses
// into "create job: %w", which is exactly why fireOne and handleRunScheduledJobNow
// both validate ahead of it.
//
// THE SUBJECT IS THE JOB-WIDE TOTAL rather than either per-axis bound, because it
// is the bound with no per-row analogue anywhere: tasks.retries and
// tasks.timeout_seconds are columns a CHECK constraint could in principle guard,
// and a per-JOB command total is not expressible as a row CHECK at all. If any
// bound is going to be enforced only here, it is this one.
//
// THE NIL *store.Queries IS THE POINT, NOT A SHORTCUT. Validate runs before any
// field of q is read, so a correct implementation never dereferences it. Delete
// the Validate call and the very next statements reach q.CreateJob on a nil
// receiver, which panics - so the mutant fails LOUDLY here instead of passing
// silently. The recover contains it so the mutant does not take down every other
// test in this package's binary.
//
// THE PROPERTY IS ESTABLISHED BY THE require.EqualError ALONE, not by the panic
// and not by the two assertions after it. The only return that carries this
// message is jobspec.Validate's, at the top of CreateJobFromSpec, and it returns
// store.Job{} with a nil task slice - so `created` being empty and job.ID being
// invalid are IMPLIED the moment the message matches, and neither can fail
// independently. They stay because they mirror the sibling test above and cost
// nothing, not because they carry the argument.
func TestCreateJobFromSpec_RefusesAnOverCountSpecBeforeTouchingTheDatabase(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CreateJobFromSpec panicked instead of refusing the spec: %v\n"+
				"q is a nil *store.Queries, so this is what reaching a query on it looks like: "+
				"jobspec.Validate is no longer running before the first database call, which is the "+
				"entire enforcement of the count bounds for a caller that does not validate first.", r)
		}
	}()

	// 51 tasks x 500 commands is 25,500. Every task is inside maxCommandsPerTask
	// and the count is inside maxTasksPerJob, so only the job-wide total refuses
	// this.
	tasks := make([]jobspec.TaskSpec, 51)
	for i := range tasks {
		cmds := make([][]string, 500)
		for j := range cmds {
			cmds[j] = []string{"true"}
		}
		tasks[i] = jobspec.TaskSpec{Name: fmt.Sprintf("t%d", i), Commands: cmds}
	}

	job, created, err := jobcreate.CreateJobFromSpec(
		context.Background(), nil, jobspec.JobSpec{Name: "over-count", Tasks: tasks},
		pgtype.UUID{}, pgtype.UUID{})

	require.EqualError(t, err, "at most 25000 commands in total across all tasks are allowed",
		"CreateJobFromSpec must refuse an over-count spec itself: no CHECK constraint can express a "+
			"per-job total, so this call is the entire enforcement for any caller that does not "+
			"validate first - schedrunner.fireOne is one such caller")
	require.Empty(t, created, "a refused spec must insert no tasks")
	require.False(t, job.ID.Valid, "a refused spec must insert no job")
}
