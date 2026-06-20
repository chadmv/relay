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
