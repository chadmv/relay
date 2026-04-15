package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// ─── Request / Response types ─────────────────────────────────────────────────

type taskSpec struct {
	Name           string            `json:"name"`
	Command        []string          `json:"command"`
	Env            map[string]string `json:"env"`
	Requires       map[string]string `json:"requires"`
	TimeoutSeconds *int32            `json:"timeout_seconds"`
	Retries        int32             `json:"retries"`
	DependsOn      []string          `json:"depends_on"`
}

type createJobRequest struct {
	Name     string            `json:"name"`
	Priority string            `json:"priority"`
	Labels   map[string]string `json:"labels"`
	Tasks    []taskSpec        `json:"tasks"`
}

type taskResponse struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	Command        []string        `json:"command"`
	Env            json.RawMessage `json:"env"`
	Requires       json.RawMessage `json:"requires"`
	TimeoutSeconds *int32          `json:"timeout_seconds"`
	Retries        int32           `json:"retries"`
	RetryCount     int32           `json:"retry_count"`
	WorkerID       string          `json:"worker_id,omitempty"`
}

type jobResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Priority    string          `json:"priority"`
	Status      string          `json:"status"`
	SubmittedBy string          `json:"submitted_by"`
	Labels      json.RawMessage `json:"labels"`
	Tasks       []taskResponse  `json:"tasks,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// ─── Converters ───────────────────────────────────────────────────────────────

func toTaskResponse(t store.Task) taskResponse {
	return taskResponse{
		ID:             uuidStr(t.ID),
		Name:           t.Name,
		Status:         t.Status,
		Command:        t.Command,
		Env:            rawJSON(t.Env),
		Requires:       rawJSON(t.Requires),
		TimeoutSeconds: t.TimeoutSeconds,
		Retries:        t.Retries,
		RetryCount:     t.RetryCount,
		WorkerID:       uuidStr(t.WorkerID),
	}
}

func toJobResponse(j store.Job, tasks []store.Task) jobResponse {
	var taskResps []taskResponse
	if len(tasks) > 0 {
		taskResps = make([]taskResponse, len(tasks))
		for i, t := range tasks {
			taskResps[i] = toTaskResponse(t)
		}
	}
	return jobResponse{
		ID:          uuidStr(j.ID),
		Name:        j.Name,
		Priority:    j.Priority,
		Status:      j.Status,
		SubmittedBy: uuidStr(j.SubmittedBy),
		Labels:      rawJSON(j.Labels),
		Tasks:       taskResps,
		CreatedAt:   j.CreatedAt.Time,
		UpdatedAt:   j.UpdatedAt.Time,
	}
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromCtx(r.Context())

	var req createJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Validate
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Tasks) == 0 {
		writeError(w, http.StatusBadRequest, "at least one task is required")
		return
	}

	// Check unique task names and non-empty commands
	nameSet := make(map[string]struct{}, len(req.Tasks))
	for _, ts := range req.Tasks {
		if ts.Name == "" {
			writeError(w, http.StatusBadRequest, "task name is required")
			return
		}
		if len(ts.Command) == 0 {
			writeError(w, http.StatusBadRequest, "task command is required")
			return
		}
		if _, dup := nameSet[ts.Name]; dup {
			writeError(w, http.StatusBadRequest, "duplicate task name: "+ts.Name)
			return
		}
		nameSet[ts.Name] = struct{}{}
	}

	// Validate depends_on references
	for _, ts := range req.Tasks {
		for _, dep := range ts.DependsOn {
			if _, ok := nameSet[dep]; !ok {
				writeError(w, http.StatusBadRequest, "unknown depends_on: "+dep)
				return
			}
		}
	}

	if req.Priority == "" {
		req.Priority = "normal"
	}

	ctx := r.Context()

	// Begin transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin transaction failed")
		return
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	// Marshal labels
	labelsJSON, err := json.Marshal(req.Labels)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal labels failed")
		return
	}

	// Create job
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:        req.Name,
		Priority:    req.Priority,
		SubmittedBy: u.ID,
		Labels:      labelsJSON,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create job failed")
		return
	}

	// Create tasks and build name→ID map
	nameToID := make(map[string]pgtype.UUID, len(req.Tasks))
	tasks := make([]store.Task, 0, len(req.Tasks))

	for _, ts := range req.Tasks {
		envJSON, err := json.Marshal(ts.Env)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal env failed")
			return
		}
		requiresJSON, err := json.Marshal(ts.Requires)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal requires failed")
			return
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
			writeError(w, http.StatusInternalServerError, "create task failed")
			return
		}
		nameToID[ts.Name] = task.ID
		tasks = append(tasks, task)
	}

	// Wire dependencies
	for _, ts := range req.Tasks {
		taskID := nameToID[ts.Name]
		for _, depName := range ts.DependsOn {
			depID := nameToID[depName]
			if err := q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
				TaskID:        taskID,
				DependsOnTaskID: depID,
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "create dependency failed")
				return
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit failed")
		return
	}

	go s.triggerDispatch()

	writeJSON(w, http.StatusCreated, toJobResponse(job, tasks))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := r.URL.Query().Get("status")

	var jobs []store.Job
	var err error
	if status != "" {
		jobs, err = s.q.ListJobsByStatus(ctx, status)
	} else {
		jobs, err = s.q.ListJobs(ctx)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list jobs failed")
		return
	}

	resp := make([]jobResponse, len(jobs))
	for i, j := range jobs {
		resp[i] = toJobResponse(j, nil)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	job, err := s.q.GetJob(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	tasks, _ := s.q.ListTasksByJob(ctx, job.ID)
	writeJSON(w, http.StatusOK, toJobResponse(job, tasks))
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	// Cancel pending tasks
	tasks, _ := s.q.ListTasksByJob(ctx, id)
	for _, t := range tasks {
		if t.Status == "pending" || t.Status == "queued" {
			_, _ = s.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
				ID:         t.ID,
				Status:     "failed",
				WorkerID:   pgtype.UUID{},
				StartedAt:  pgtype.Timestamptz{},
				FinishedAt: pgtype.Timestamptz{Valid: true, Time: time.Now()},
			})
		}
	}

	job, err := s.q.UpdateJobStatus(ctx, store.UpdateJobStatusParams{
		ID:     id,
		Status: "cancelled",
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	s.broker.Publish(events.Event{
		Type:  "job",
		JobID: uuidStr(job.ID),
		Data:  []byte(`{"status":"cancelled"}`),
	})

	writeJSON(w, http.StatusOK, toJobResponse(job, nil))
}

// ─── Package-level helper ─────────────────────────────────────────────────────

func updateJobStatusFromTasks(ctx context.Context, q *store.Queries, jobID pgtype.UUID) {
	tasks, err := q.ListTasksByJob(ctx, jobID)
	if err != nil || len(tasks) == 0 {
		return
	}
	var done, failed, active int
	for _, t := range tasks {
		switch t.Status {
		case "done":
			done++
		case "failed", "timed_out":
			failed++
		default:
			active++
		}
	}
	var newStatus string
	switch {
	case active > 0:
		newStatus = "running"
	case done == len(tasks):
		newStatus = "done"
	default:
		newStatus = "failed"
	}
	_, _ = q.UpdateJobStatus(ctx, store.UpdateJobStatusParams{ID: jobID, Status: newStatus})
}
