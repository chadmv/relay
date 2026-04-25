package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
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
	DependsOn      []string        `json:"depends_on,omitempty"`
	WorkerID       string          `json:"worker_id,omitempty"`
}

type jobResponse struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Priority         string          `json:"priority"`
	Status           string          `json:"status"`
	SubmittedBy      string          `json:"submitted_by"`
	SubmittedByEmail string          `json:"submitted_by_email,omitempty"`
	Labels           json.RawMessage `json:"labels"`
	Tasks            []taskResponse  `json:"tasks,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// ─── Converters ───────────────────────────────────────────────────────────────

func toTaskResponse(t store.Task, dependsOn []string) taskResponse {
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
		DependsOn:      dependsOn,
		WorkerID:       uuidStr(t.WorkerID),
	}
}

func toJobResponse(j store.Job, email string, tasks []store.Task, taskDeps map[pgtype.UUID][]string) jobResponse {
	var taskResps []taskResponse
	if len(tasks) > 0 {
		taskResps = make([]taskResponse, len(tasks))
		for i, t := range tasks {
			taskResps[i] = toTaskResponse(t, taskDeps[t.ID])
		}
	}
	return jobResponse{
		ID:               uuidStr(j.ID),
		Name:             j.Name,
		Priority:         j.Priority,
		Status:           j.Status,
		SubmittedBy:      uuidStr(j.SubmittedBy),
		SubmittedByEmail: email,
		Labels:           rawJSON(j.Labels),
		Tasks:            taskResps,
		CreatedAt:        j.CreatedAt.Time,
		UpdatedAt:        j.UpdatedAt.Time,
	}
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	spec := JobSpec{
		Name:     req.Name,
		Priority: req.Priority,
		Labels:   req.Labels,
		Tasks:    make([]TaskSpec, len(req.Tasks)),
	}
	for i, t := range req.Tasks {
		spec.Tasks[i] = TaskSpec{
			Name:           t.Name,
			Command:        t.Command,
			Env:            t.Env,
			Requires:       t.Requires,
			TimeoutSeconds: t.TimeoutSeconds,
			Retries:        t.Retries,
			DependsOn:      t.DependsOn,
		}
	}

	if err := ValidateJobSpec(spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin transaction failed")
		return
	}
	defer tx.Rollback(ctx)

	job, tasks, err := CreateJobFromSpec(ctx, s.q.WithTx(tx), spec, u.ID, pgtype.UUID{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit failed")
		return
	}

	taskDeps := make(map[pgtype.UUID][]string, len(spec.Tasks))
	for i, ts := range spec.Tasks {
		taskDeps[tasks[i].ID] = ts.DependsOn
	}

	writeJSON(w, http.StatusCreated, toJobResponse(job, u.Email, tasks, taskDeps))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if schedIDStr := r.URL.Query().Get("scheduled_job_id"); schedIDStr != "" {
		schedID, err := parseUUID(schedIDStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid scheduled_job_id")
			return
		}
		// Enforce ownership: non-admins may only list jobs for schedules they own.
		// Reuse ownedScheduledJob which returns 404 for non-owner/non-admin callers,
		// preventing enumeration of another user's job history.
		if _, ok := s.ownedScheduledJob(w, r, schedID); !ok {
			return
		}
		rs, err := s.q.ListJobsByScheduledJob(ctx, schedID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		resp := make([]jobResponse, len(rs))
		for i, r := range rs {
			job := store.Job{ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status, SubmittedBy: r.SubmittedBy, Labels: r.Labels, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
			resp[i] = toJobResponse(job, r.SubmittedByEmail, nil, nil)
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	status := r.URL.Query().Get("status")

	type jobWithEmail struct {
		job   store.Job
		email string
	}
	var rows []jobWithEmail
	if status != "" {
		rs, err := s.q.ListJobsByStatusWithEmail(ctx, status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		for _, r := range rs {
			rows = append(rows, jobWithEmail{store.Job{ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status, SubmittedBy: r.SubmittedBy, Labels: r.Labels, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, r.SubmittedByEmail})
		}
	} else {
		rs, err := s.q.ListJobsWithEmail(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		for _, r := range rs {
			rows = append(rows, jobWithEmail{store.Job{ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status, SubmittedBy: r.SubmittedBy, Labels: r.Labels, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, r.SubmittedByEmail})
		}
	}

	resp := make([]jobResponse, len(rows))
	for i, r := range rows {
		resp[i] = toJobResponse(r.job, r.email, nil, nil)
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

	row, err := s.q.GetJobWithEmail(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	job := store.Job{ID: row.ID, Name: row.Name, Priority: row.Priority, Status: row.Status, SubmittedBy: row.SubmittedBy, Labels: row.Labels, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	tasks, _ := s.q.ListTasksByJob(ctx, row.ID)

	uuidToName := make(map[pgtype.UUID]string, len(tasks))
	for _, t := range tasks {
		uuidToName[t.ID] = t.Name
	}
	taskDeps := make(map[pgtype.UUID][]string, len(tasks))
	for _, t := range tasks {
		depUUIDs, err := s.q.GetTaskDependencies(ctx, t.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if len(depUUIDs) > 0 {
			names := make([]string, len(depUUIDs))
			for i, uid := range depUUIDs {
				names[i] = uuidToName[uid]
			}
			taskDeps[t.ID] = names
		}
	}

	writeJSON(w, http.StatusOK, toJobResponse(job, row.SubmittedByEmail, tasks, taskDeps))
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	// Check current job status before cancelling.
	job, err := q.GetJob(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}
	if job.Status == "cancelled" || job.Status == "done" {
		writeError(w, http.StatusConflict, "job is already in a terminal state")
		return
	}

	// Cancel all non-terminal tasks; collect running/dispatched ones for agent signals.
	tasks, err := q.ListTasksByJob(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	var runningTasks []store.Task
	for _, t := range tasks {
		switch t.Status {
		case "pending", "queued", "running", "dispatched":
			if _, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
				ID:         t.ID,
				Status:     "failed",
				WorkerID:   pgtype.UUID{},
				StartedAt:  pgtype.Timestamptz{},
				FinishedAt: pgtype.Timestamptz{Valid: true, Time: time.Now()},
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "db error")
				return
			}
			if (t.Status == "running" || t.Status == "dispatched") && t.WorkerID.Valid {
				runningTasks = append(runningTasks, t)
			}
		}
	}

	job, err = q.UpdateJobStatus(ctx, store.UpdateJobStatusParams{
		ID:     id,
		Status: "cancelled",
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	for _, t := range runningTasks {
		_ = s.registry.Send(uuidStr(t.WorkerID), &relayv1.CoordinatorMessage{
			Payload: &relayv1.CoordinatorMessage_CancelTask{
				CancelTask: &relayv1.CancelTask{
					TaskId: uuidStr(t.ID),
				},
			},
		})
	}

	s.broker.Publish(events.Event{
		Type:  "job",
		JobID: uuidStr(job.ID),
		Data:  []byte(`{"status":"cancelled"}`),
	})

	writeJSON(w, http.StatusOK, toJobResponse(job, "", nil, nil))
}

// ─── Package-level helper ─────────────────────────────────────────────────────

func updateJobStatusFromTasks(ctx context.Context, q *store.Queries, jobID pgtype.UUID) {
	tasks, err := q.ListTasksByJob(ctx, jobID)
	if err != nil || len(tasks) == 0 {
		// If we can't list tasks we can't determine the correct status; skip update.
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
	// Best-effort update; caller has no error return so we can't propagate failures.
	_, _ = q.UpdateJobStatus(ctx, store.UpdateJobStatusParams{ID: jobID, Status: newStatus})
}
