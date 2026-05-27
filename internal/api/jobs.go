package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
	Command        []string          `json:"command,omitempty"`
	Commands       [][]string        `json:"commands,omitempty"`
	Env            map[string]string `json:"env"`
	Requires       map[string]string `json:"requires"`
	TimeoutSeconds *int32            `json:"timeout_seconds"`
	Retries        int32             `json:"retries"`
	DependsOn      []string          `json:"depends_on"`
	Source         *SourceSpec       `json:"source,omitempty"`
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
	Commands       json.RawMessage `json:"commands"`
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
		Commands:       rawJSON(t.Commands),
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
			Commands:       t.Commands,
			Env:            t.Env,
			Requires:       t.Requires,
			TimeoutSeconds: t.TimeoutSeconds,
			Retries:        t.Retries,
			DependsOn:      t.DependsOn,
			Source:         t.Source,
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

// JobsSortSpec is the allowlist for ?sort= on the unfiltered /v1/jobs endpoint.
var JobsSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
		"name":       SortKeyText,
		"priority":   SortKeyText,
		"status":     SortKeyText,
		"updated_at": SortKeyTimestamp,
	},
}

func jobsRowKeyDefault(r store.ListJobsWithEmailPageRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func jobsRowKeyByStatus(r store.ListJobsByStatusWithEmailPageRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func jobsRowKeyByScheduled(r store.ListJobsByScheduledJobWithEmailPageRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}

func jobRowToResponseDefault(r store.ListJobsWithEmailPageRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}
func jobRowToResponseByStatus(r store.ListJobsByStatusWithEmailPageRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}
func jobRowToResponseByScheduled(r store.ListJobsByScheduledJobWithEmailPageRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

// ─── Sort-dispatch helpers for the unfiltered /v1/jobs list ──────────────────

func jobsRowKeyByCreatedAsc(r store.ListJobsWithEmailPageByCreatedAscRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func jobRowToResponseByCreatedAsc(r store.ListJobsWithEmailPageByCreatedAscRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func jobsRowKeyByNameDesc(r store.ListJobsWithEmailPageByNameDescRow) (anySortVal, pgtype.UUID) {
	return r.Name, r.ID
}
func jobRowToResponseByNameDesc(r store.ListJobsWithEmailPageByNameDescRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func jobsRowKeyByNameAsc(r store.ListJobsWithEmailPageByNameAscRow) (anySortVal, pgtype.UUID) {
	return r.Name, r.ID
}
func jobRowToResponseByNameAsc(r store.ListJobsWithEmailPageByNameAscRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func jobsRowKeyByPriorityDesc(r store.ListJobsWithEmailPageByPriorityDescRow) (anySortVal, pgtype.UUID) {
	return r.Priority, r.ID
}
func jobRowToResponseByPriorityDesc(r store.ListJobsWithEmailPageByPriorityDescRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func jobsRowKeyByPriorityAsc(r store.ListJobsWithEmailPageByPriorityAscRow) (anySortVal, pgtype.UUID) {
	return r.Priority, r.ID
}
func jobRowToResponseByPriorityAsc(r store.ListJobsWithEmailPageByPriorityAscRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func jobsRowKeyByStatusDesc(r store.ListJobsWithEmailPageByStatusDescRow) (anySortVal, pgtype.UUID) {
	return r.Status, r.ID
}
func jobRowToResponseByStatusDesc(r store.ListJobsWithEmailPageByStatusDescRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func jobsRowKeyByStatusAsc(r store.ListJobsWithEmailPageByStatusAscRow) (anySortVal, pgtype.UUID) {
	return r.Status, r.ID
}
func jobRowToResponseByStatusAsc(r store.ListJobsWithEmailPageByStatusAscRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func jobsRowKeyByUpdatedDesc(r store.ListJobsWithEmailPageByUpdatedDescRow) (anySortVal, pgtype.UUID) {
	return r.UpdatedAt.Time, r.ID
}
func jobRowToResponseByUpdatedDesc(r store.ListJobsWithEmailPageByUpdatedDescRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func jobsRowKeyByUpdatedAsc(r store.ListJobsWithEmailPageByUpdatedAscRow) (anySortVal, pgtype.UUID) {
	return r.UpdatedAt.Time, r.ID
}
func jobRowToResponseByUpdatedAsc(r store.ListJobsWithEmailPageByUpdatedAscRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, JobsSortSpec)
	if !ok {
		return
	}

	hasSort := r.URL.Query().Get("sort") != ""
	hasFilter := r.URL.Query().Get("status") != "" || r.URL.Query().Get("scheduled_job_id") != ""
	if hasSort && hasFilter {
		writeError(w, http.StatusBadRequest, "sort not supported on filtered list variant; remove the filter or remove the sort")
		return
	}

	// Branch 1: ?scheduled_job_id=<uuid>
	if schedIDStr := r.URL.Query().Get("scheduled_job_id"); schedIDStr != "" {
		schedID, err := parseUUID(schedIDStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid scheduled_job_id")
			return
		}
		// Auth gate runs BEFORE pagination — non-owners get 404, not a paginated empty result.
		if _, ok := s.ownedScheduledJob(w, r, schedID); !ok {
			return
		}
		rows, err := s.q.ListJobsByScheduledJobWithEmailPage(ctx, store.ListJobsByScheduledJobWithEmailPageParams{
			ScheduledJobID: schedID,
			CursorSet:      pp.Cursor.Set,
			CursorTs:       pp.CursorTs(),
			CursorID:       pp.Cursor.ID,
			PageLimit:      pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		total, err := s.q.CountJobsByScheduledJob(ctx, schedID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count jobs failed")
			return
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByScheduled, jobsRowKeyByScheduled)
		writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	// Branch 2: ?status=<status>
	if status := r.URL.Query().Get("status"); status != "" {
		rows, err := s.q.ListJobsByStatusWithEmailPage(ctx, store.ListJobsByStatusWithEmailPageParams{
			Status:    status,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		total, err := s.q.CountJobsByStatus(ctx, status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count jobs failed")
			return
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByStatus, jobsRowKeyByStatus)
		writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	// Default branch: no filter — dispatch on pp.Sort.
	items, next, total, err := s.listJobsBySort(ctx, pp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list jobs failed")
		return
	}
	writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
}

// listJobsBySort dispatches to the correct sqlc query based on pp.Sort and
// returns (items, nextCursor, total, error). All 10 sort arms are covered.
func (s *Server) listJobsBySort(ctx context.Context, pp pageParams) ([]jobResponse, string, int64, error) {
	total, err := s.q.CountJobs(ctx)
	if err != nil {
		return nil, "", 0, err
	}

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListJobsWithEmailPage(ctx, store.ListJobsWithEmailPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseDefault, jobsRowKeyDefault)
		return items, next, total, nil

	case "created_at":
		rows, err := s.q.ListJobsWithEmailPageByCreatedAsc(ctx, store.ListJobsWithEmailPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByCreatedAsc, jobsRowKeyByCreatedAsc)
		return items, next, total, nil

	case "-name":
		rows, err := s.q.ListJobsWithEmailPageByNameDesc(ctx, store.ListJobsWithEmailPageByNameDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByNameDesc, jobsRowKeyByNameDesc)
		return items, next, total, nil

	case "name":
		rows, err := s.q.ListJobsWithEmailPageByNameAsc(ctx, store.ListJobsWithEmailPageByNameAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByNameAsc, jobsRowKeyByNameAsc)
		return items, next, total, nil

	case "-priority":
		rows, err := s.q.ListJobsWithEmailPageByPriorityDesc(ctx, store.ListJobsWithEmailPageByPriorityDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByPriorityDesc, jobsRowKeyByPriorityDesc)
		return items, next, total, nil

	case "priority":
		rows, err := s.q.ListJobsWithEmailPageByPriorityAsc(ctx, store.ListJobsWithEmailPageByPriorityAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByPriorityAsc, jobsRowKeyByPriorityAsc)
		return items, next, total, nil

	case "-status":
		rows, err := s.q.ListJobsWithEmailPageByStatusDesc(ctx, store.ListJobsWithEmailPageByStatusDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByStatusDesc, jobsRowKeyByStatusDesc)
		return items, next, total, nil

	case "status":
		rows, err := s.q.ListJobsWithEmailPageByStatusAsc(ctx, store.ListJobsWithEmailPageByStatusAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByStatusAsc, jobsRowKeyByStatusAsc)
		return items, next, total, nil

	case "-updated_at":
		rows, err := s.q.ListJobsWithEmailPageByUpdatedDesc(ctx, store.ListJobsWithEmailPageByUpdatedDescParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByUpdatedDesc, jobsRowKeyByUpdatedDesc)
		return items, next, total, nil

	case "updated_at":
		rows, err := s.q.ListJobsWithEmailPageByUpdatedAsc(ctx, store.ListJobsWithEmailPageByUpdatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			return nil, "", 0, err
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByUpdatedAsc, jobsRowKeyByUpdatedAsc)
		return items, next, total, nil

	default:
		panic("listJobsBySort: missing dispatch arm for sort key " + pp.Sort)
	}
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
	force, _ := strconv.ParseBool(r.URL.Query().Get("force"))

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
					Force:  force,
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
