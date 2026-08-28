package api

import (
	"context"

	"relay/internal/jobcreate"
	"relay/internal/jobspec"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// Type aliases — kept so existing api code (handlers, schedrunner) compiles without changes.
type (
	JobSpec    = jobspec.JobSpec
	TaskSpec   = jobspec.TaskSpec
	SourceSpec = jobspec.SourceSpec
	SyncEntry  = jobspec.SyncEntry
)

// ValidateJobSpec preserves existing call sites (takes value, not pointer).
//
// THE VALUE PARAMETER DOES NOT MAKE THIS NON-MUTATING, and this comment is the
// only place that contract is written down. Only JobSpec's top-level fields are
// copied. Tasks is a slice, so the copy shares its backing array, and
// jobspec.Validate normalizes each element IN PLACE through `&spec.Tasks[i]`: a
// caller's task goes from Command=[echo hi] Commands=[] to Command=[]
// Commands=[[echo hi]] across this call.
//
// Harmless at all four call sites today - each either discards spec afterwards
// or reads only DependsOn, which normalization does not touch, and
// normalization is idempotent so the second pass inside CreateJobFromSpec is a
// no-op. A new caller that reads Command back must expect the normalized form.
func ValidateJobSpec(spec JobSpec) error {
	return jobspec.Validate(&spec)
}

// CreateJobFromSpec inserts a job, its tasks, and task dependencies inside the
// provided (transactional) Queries. Caller owns Begin/Commit. Emits
// NotifyTaskSubmitted on success.
//
// If scheduledID is a valid UUID, the resulting job.scheduled_job_id is set.
//
// This delegates to jobcreate.CreateJobFromSpec, the single shared creation
// path used by the REST API, run-now, and the cron scheduler.
func CreateJobFromSpec(
	ctx context.Context,
	q *store.Queries,
	spec JobSpec,
	submittedBy pgtype.UUID,
	scheduledID pgtype.UUID,
) (store.Job, []store.Task, error) {
	return jobcreate.CreateJobFromSpec(ctx, q, spec, submittedBy, scheduledID)
}
