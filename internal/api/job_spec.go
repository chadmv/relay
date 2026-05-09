package api

import (
	"context"
	"encoding/json"
	"fmt"

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
func CreateJobFromSpec(
	ctx context.Context,
	q *store.Queries,
	spec JobSpec,
	submittedBy pgtype.UUID,
	scheduledID pgtype.UUID,
) (store.Job, []store.Task, error) {
	if err := ValidateJobSpec(spec); err != nil {
		return store.Job{}, nil, err
	}

	priority := spec.Priority
	if priority == "" {
		priority = "normal"
	}

	labelsJSON, err := json.Marshal(spec.Labels)
	if err != nil {
		return store.Job{}, nil, fmt.Errorf("marshal labels: %w", err)
	}

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:           spec.Name,
		Priority:       priority,
		SubmittedBy:    submittedBy,
		Labels:         labelsJSON,
		ScheduledJobID: scheduledID,
	})
	if err != nil {
		return store.Job{}, nil, fmt.Errorf("create job: %w", err)
	}

	nameToID := make(map[string]pgtype.UUID, len(spec.Tasks))
	tasks := make([]store.Task, 0, len(spec.Tasks))
	for _, ts := range spec.Tasks {
		envJSON, err := json.Marshal(ts.Env)
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("marshal env for %s: %w", ts.Name, err)
		}
		requiresJSON, err := json.Marshal(ts.Requires)
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("marshal requires for %s: %w", ts.Name, err)
		}
		commandsJSON, err := json.Marshal(ts.Commands)
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("marshal commands for %s: %w", ts.Name, err)
		}
		var task store.Task
		var taskErr error
		if ts.Source != nil {
			sourceJSON, merr := json.Marshal(ts.Source)
			if merr != nil {
				return store.Job{}, nil, fmt.Errorf("marshal source for %s: %w", ts.Name, merr)
			}
			task, taskErr = q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
				JobID:          job.ID,
				Name:           ts.Name,
				Commands:       commandsJSON,
				Env:            envJSON,
				Requires:       requiresJSON,
				TimeoutSeconds: ts.TimeoutSeconds,
				Retries:        ts.Retries,
				Source:         sourceJSON,
			})
		} else {
			task, taskErr = q.CreateTask(ctx, store.CreateTaskParams{
				JobID:          job.ID,
				Name:           ts.Name,
				Commands:       commandsJSON,
				Env:            envJSON,
				Requires:       requiresJSON,
				TimeoutSeconds: ts.TimeoutSeconds,
				Retries:        ts.Retries,
			})
		}
		if taskErr != nil {
			return store.Job{}, nil, fmt.Errorf("create task %s: %w", ts.Name, taskErr)
		}
		nameToID[ts.Name] = task.ID
		tasks = append(tasks, task)
	}

	for _, ts := range spec.Tasks {
		for _, depName := range ts.DependsOn {
			if err := q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
				TaskID:          nameToID[ts.Name],
				DependsOnTaskID: nameToID[depName],
			}); err != nil {
				return store.Job{}, nil, fmt.Errorf("create dependency %s->%s: %w", ts.Name, depName, err)
			}
		}
	}

	_ = q.NotifyTaskSubmitted(ctx)

	return job, tasks, nil
}
