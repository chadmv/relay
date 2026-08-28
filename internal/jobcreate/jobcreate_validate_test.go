package jobcreate_test

import (
	"context"
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
func TestCreateJobFromSpec_RefusesAnOverBoundSpecBeforeTouchingTheDatabase(t *testing.T) {
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
