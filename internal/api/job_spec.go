package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
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
type TaskSpec struct {
	Name           string            `json:"name"`
	Command        []string          `json:"command"`
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

// ValidateJobSpec applies the same validation as POST /v1/jobs. Returns an
// error with a caller-facing message on the first problem.
func ValidateJobSpec(spec JobSpec) error {
	if spec.Name == "" {
		return errors.New("name is required")
	}
	if len(spec.Tasks) == 0 {
		return errors.New("at least one task is required")
	}
	nameSet := make(map[string]struct{}, len(spec.Tasks))
	for _, ts := range spec.Tasks {
		if ts.Name == "" {
			return errors.New("task name is required")
		}
		if len(ts.Command) == 0 {
			return errors.New("task command is required")
		}
		if _, dup := nameSet[ts.Name]; dup {
			return fmt.Errorf("duplicate task name: %s", ts.Name)
		}
		nameSet[ts.Name] = struct{}{}
	}
	for _, ts := range spec.Tasks {
		for _, dep := range ts.DependsOn {
			if _, ok := nameSet[dep]; !ok {
				return fmt.Errorf("unknown depends_on: %s", dep)
			}
		}
	}
	for _, ts := range spec.Tasks {
		if err := validateSourceSpec(ts.Source); err != nil {
			return fmt.Errorf("task %s: %w", ts.Name, err)
		}
	}
	return nil
}

var (
	revHeadRe    = regexp.MustCompile(`^#head$`)
	revCLRe      = regexp.MustCompile(`^@\d+$`)
	revLabelRe   = regexp.MustCompile(`^@[A-Za-z0-9._-]+$`)
	revNumRe     = regexp.MustCompile(`^#\d+$`)
	clientTmplRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

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
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID:          job.ID,
			Name:           ts.Name,
			Command:        ts.Command,
			Env:            envJSON,
			Requires:       requiresJSON,
			TimeoutSeconds: ts.TimeoutSeconds,
			Retries:        ts.Retries,
		})
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("create task %s: %w", ts.Name, err)
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
