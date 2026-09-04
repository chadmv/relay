package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"relay/internal/events"
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

	// Enrichment populated only on list rows (GET /v1/jobs). Derived from the
	// job's tasks and its scheduled-job source.
	TotalTasks       int32      `json:"total_tasks"`
	DoneTasks        int32      `json:"done_tasks"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	ScheduledJobID   string     `json:"scheduled_job_id,omitempty"`
	ScheduledJobName string     `json:"scheduled_job_name,omitempty"`
}

// ─── Converters ───────────────────────────────────────────────────────────────

func toTaskResponse(t store.Task, dependsOn []string) taskResponse {
	return taskResponse{
		ID:             uuidStr(t.ID),
		Name:           t.Name,
		Status:         t.Status,
		Commands:       rawJSON(t.Commands),
		Env:            rawObject(t.Env),
		Requires:       rawObject(t.Requires),
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

// applyJobEnrichment sets the list-only fields (task progress, timing, schedule
// source) on a jobResponse. totalTasks/doneTasks come from the LATERAL aggregate;
// startedAt/finishedAt/scheduledJobName are nullable; scheduledJobID comes from
// the job row directly.
func applyJobEnrichment(resp *jobResponse, totalTasks, doneTasks int64, startedAt, finishedAt pgtype.Timestamptz, scheduledJobID pgtype.UUID, scheduledJobName *string) {
	// COUNT(*) is int64; a job's task count fits int32 in any realistic case.
	resp.TotalTasks = int32(totalTasks)
	resp.DoneTasks = int32(doneTasks)
	if startedAt.Valid {
		t := startedAt.Time
		resp.StartedAt = &t
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		resp.FinishedAt = &t
	}
	if scheduledJobID.Valid {
		resp.ScheduledJobID = uuidStr(scheduledJobID)
	}
	if scheduledJobName != nil {
		resp.ScheduledJobName = *scheduledJobName
	}
}

// jobStatsResponse is the fleet-wide KPI summary returned by GET /v1/jobs/stats.
// done_24h and failed_24h are windowed on updated_at (see JobStatusCounts).
type jobStatsResponse struct {
	Running   int64 `json:"running"`
	Queued    int64 `json:"queued"`
	Done24h   int64 `json:"done_24h"`
	Failed24h int64 `json:"failed_24h"`
}

func (s *Server) handleJobStats(w http.ResponseWriter, r *http.Request) {
	counts, err := s.q.JobStatusCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "job stats failed")
		return
	}
	writeJSON(w, http.StatusOK, jobStatsResponse{
		Running:   counts.Running,
		Queued:    counts.Queued,
		Done24h:   counts.Done24h,
		Failed24h: counts.Failed24h,
	})
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createJobRequest
	if !readJSON(w, r, &req) {
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
}
func jobRowToResponseByStatus(r store.ListJobsByStatusWithEmailPageRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
}
func jobRowToResponseByScheduled(r store.ListJobsByScheduledJobWithEmailPageRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
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
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
}

// jobsListArityParams are the parameters handleListJobs reads itself.
var jobsListArityParams = append([]string{"status", "scheduled_job_id"}, jobFilterParams...)

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, JobsSortSpec)
	if !ok {
		return
	}

	hasSort := pp.Query.Get("sort") != ""
	hasFilter := pp.Query.Get("status") != "" || pp.Query.Get("scheduled_job_id") != ""
	if hasSort && hasFilter {
		writeError(w, http.StatusBadRequest, "sort not supported on filtered list variant; remove the filter or remove the sort")
		return
	}

	if !rejectRepeatedParams(w, pp.Query, jobsListArityParams...) {
		return
	}

	// Parsed after the sort-versus-filter guard so that rule's 400 keeps its
	// precedence. The four parameters are deliberately NOT part of that
	// guard and must not be added to hasFilter: they are threaded into every
	// sort variant as optional arguments and never touch ORDER BY, so they
	// cannot create the ordering gap the guard exists to close.
	u, _ := UserFromCtx(ctx)
	filters, ok := parseJobFilters(w, pp.Query, u)
	if !ok {
		return
	}

	// Branch 1: ?scheduled_job_id=<uuid>
	if schedIDStr := pp.Query.Get("scheduled_job_id"); schedIDStr != "" {
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
			Q:              filters.Q,
			OwnerID:        filters.OwnerID,
			Since:          filters.Since,
			Until:          filters.Until,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		total, err := s.countJobsByScheduledJob(ctx, schedID, filters)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count jobs failed")
			return
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByScheduled, jobsRowKeyByScheduled)
		writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	// Branch 2: ?status=<status>
	if status := pp.Query.Get("status"); status != "" {
		rows, err := s.q.ListJobsByStatusWithEmailPage(ctx, store.ListJobsByStatusWithEmailPageParams{
			Status:    status,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		total, err := s.countJobsByStatus(ctx, status, filters)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count jobs failed")
			return
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, jobRowToResponseByStatus, jobsRowKeyByStatus)
		writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	// Default branch: no filter — dispatch on pp.Sort.
	items, next, total, err := s.listJobsBySort(ctx, pp, filters)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list jobs failed")
		return
	}
	writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
}

// listJobsBySort dispatches to the correct sqlc query based on pp.Sort and
// returns (items, nextCursor, total, error). All 10 sort arms are covered.
func (s *Server) listJobsBySort(ctx context.Context, pp pageParams, filters jobFilters) ([]jobResponse, string, int64, error) {
	total, err := s.countJobs(ctx, filters)
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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
			Q:         filters.Q,
			OwnerID:   filters.OwnerID,
			Since:     filters.Since,
			Until:     filters.Until,
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

// The three count helpers fork on whether q is present. The joined ...WithText
// twin is needed only for the owner-email arm of q; an inner join is not
// elidable, so without the fork an unfiltered count hash-joins every jobs row
// against users for a column it never reads.
func (s *Server) countJobs(ctx context.Context, filters jobFilters) (int64, error) {
	if filters.Q != nil {
		return s.q.CountJobsWithText(ctx, store.CountJobsWithTextParams{
			Q:       filters.Q,
			OwnerID: filters.OwnerID,
			Since:   filters.Since,
			Until:   filters.Until,
		})
	}
	return s.q.CountJobs(ctx, store.CountJobsParams{
		OwnerID: filters.OwnerID,
		Since:   filters.Since,
		Until:   filters.Until,
	})
}

func (s *Server) countJobsByStatus(ctx context.Context, status string, filters jobFilters) (int64, error) {
	if filters.Q != nil {
		return s.q.CountJobsByStatusWithText(ctx, store.CountJobsByStatusWithTextParams{
			Status:  status,
			Q:       filters.Q,
			OwnerID: filters.OwnerID,
			Since:   filters.Since,
			Until:   filters.Until,
		})
	}
	return s.q.CountJobsByStatus(ctx, store.CountJobsByStatusParams{
		Status:  status,
		OwnerID: filters.OwnerID,
		Since:   filters.Since,
		Until:   filters.Until,
	})
}

func (s *Server) countJobsByScheduledJob(ctx context.Context, schedID pgtype.UUID, filters jobFilters) (int64, error) {
	if filters.Q != nil {
		return s.q.CountJobsByScheduledJobWithText(ctx, store.CountJobsByScheduledJobWithTextParams{
			ScheduledJobID: schedID,
			Q:              filters.Q,
			OwnerID:        filters.OwnerID,
			Since:          filters.Since,
			Until:          filters.Until,
		})
	}
	return s.q.CountJobsByScheduledJob(ctx, store.CountJobsByScheduledJobParams{
		ScheduledJobID: schedID,
		OwnerID:        filters.OwnerID,
		Since:          filters.Since,
		Until:          filters.Until,
	})
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
	// Checked, like every other read on this handler. Discarded, a transient
	// failure here (pool exhaustion, statement timeout, a cancelled context)
	// answered 200 with `tasks` absent, since jobResponse's field is omitempty -
	// and a task-less 200 is the one failure shape a client cannot tell from a
	// real answer. relay logs' final reconcile was built on top of exactly that
	// body and reported the job fully reconciled, exit 0 and silent.
	tasks, err := s.q.ListTasksByJob(ctx, row.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

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

// jobOwnerOr404 is the shared owner-or-admin gate for the two destructive
// job-scoped writes, cancel and retry. Both callers run it BEFORE opening their
// transaction, against an UNLOCKED GetJob read, and only then take
// GetJobForUpdate. The lock still precedes every write; what it no longer
// precedes is the authorization decision.
//
// That ordering is load-bearing. Gating after GetJobForUpdate answers a stranger
// correctly but makes them wait first: any authenticated caller could park in the
// Postgres lock queue behind the owner's in-flight cancel or retry, holding a
// pool connection for the duration of somebody else's transaction, and neither
// route is rate limited. TestJobWrites_NonOwner_404_WithoutQueueingForTheJobRowLock
// pins it.
//
// Reading unlocked is safe for THIS question only because jobs.submitted_by is
// immutable: it is set once by CreateJob and no statement in the repo ever
// updates it (the only UPDATEs on jobs write status and updated_at). Every gate
// that reads a mutable column - the status checks in both handlers - stays on the
// LOCKED row, so it is still decided against the snapshot the write uses. If
// submitted_by ever becomes writable, this ordering becomes a TOCTOU and the gate
// has to be re-evaluated on the locked row.
//
// It writes the response and returns false when the caller may not act; callers
// simply return, before any transaction is open, so nothing is written and no
// agent signal is sent. A non-owner non-admin gets 404, not 403, matching
// ownedScheduledJob.
// See the Jobs block comment in server.go for why that is defense-in-depth
// rather than a true existence secret: the GET routes are global.
//
// There is exactly one copy of this gate. Do not inline a second one.
func (s *Server) jobOwnerOr404(ctx context.Context, w http.ResponseWriter, job store.Job) bool {
	u, ok := UserFromCtx(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if !u.IsAdmin && job.SubmittedBy != u.ID {
		writeError(w, http.StatusNotFound, "job not found")
		return false
	}
	return true
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	force, _ := strconv.ParseBool(r.URL.Query().Get("force"))

	// Owner-or-admin gate FIRST, decided from an UNLOCKED read and before any
	// transaction is open. handleRetryJob does the same; see jobOwnerOr404.
	job, err := s.q.GetJob(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}
	if !s.jobOwnerOr404(ctx, w, job) {
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	// Lock the job row FIRST, before touching any task row. handleRetryJob does
	// the same. The single lock order (job, then tasks) is what keeps the two
	// handlers from being an ABBA deadlock pair, and what makes a retry landing
	// in this request's window serialize instead of interleave. See
	// GetJobForUpdate.
	job, err = q.GetJobForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	// The status gate reads the LOCKED row, so it is decided against the same
	// snapshot the writes below use. Returning here rolls back the open tx, so no
	// task is cancelled and no agent signal is sent.
	if job.Status == "cancelled" || job.Status == "done" {
		writeError(w, http.StatusConflict, "job is already in a terminal state")
		return
	}

	// Cancel all non-terminal tasks; collect the currently-assigned ones for agent
	// signals.
	tasks, err := q.ListTasksByJob(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	// Collect the assignment partition for agent cancel signals before the bulk
	// update clears their worker_id. A status in that partition and missing here
	// leaves a live agent executing against a task the database has already
	// failed, so this list must move whenever the partition does; it is the same
	// set CancelJobTasks' non-terminal predicate fails.
	var runningTasks []store.Task
	for _, t := range tasks {
		if (t.Status == "running" || t.Status == "dispatched" || t.Status == "preparing") && t.WorkerID.Valid {
			runningTasks = append(runningTasks, t)
		}
	}

	// Fail every non-terminal task in one statement. This bumps assignment_epoch
	// so late updates from the assigned agent are fenced out; a per-task,
	// epoch-fenced update would reject any task that had ever been dispatched.
	if err := q.CancelJobTasks(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
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

	cancels := make([]cancelSignal, 0, len(runningTasks))
	for _, t := range runningTasks {
		cancels = append(cancels, cancelSignal{
			workerID: uuidStr(t.WorkerID),
			taskID:   uuidStr(t.ID),
			force:    force,
		})
	}
	s.sendCancelSignals(cancels)

	s.broker.Publish(events.Event{
		Type:  "job",
		JobID: uuidStr(job.ID),
		Data:  []byte(`{"status":"cancelled"}`),
	})

	writeJSON(w, http.StatusOK, toJobResponse(job, "", nil, nil))
}

// retryJobResponse is the body returned by POST /v1/jobs/{id}/retry. It embeds
// jobResponse (its fields flatten into the JSON object) and adds one key, the
// same shape disableWorkerResponse uses for requeued_tasks. tasks_retried is
// always >= 1 on a 200: a zero-match retry is a 409, never a successful no-op,
// so a client never has to tell a no-op from a real re-run by reading a number.
type retryJobResponse struct {
	jobResponse
	TasksRetried int `json:"tasks_retried"`
}

// logIDHead is how many task ids the 409 diagnostic lines name individually.
// Enough to recognize which tasks are involved; small enough that the line is
// the same size for a 3-task job and a 3000-task job.
const logIDHead = 8

// uuidStrHead renders at most max task ids for the two server-side diagnostic
// log lines, annotating how many it dropped.
//
// Bounded on purpose, as INSURANCE rather than as a fix for a live flood. Task
// count is now bounded at BOTH ends by jobspec.Validate - at least one, at most
// maxTasksPerJob - and the upper bound does not retire this cap, because THIS
// FUNCTION'S INPUT IS DATABASE ROWS AND NOT A VALIDATED SPEC. Both slices come
// from queries (SelectRetryableTaskIDs, RetryJobTasks), and maxTasksPerJob binds
// job CREATION from here on: no migration clamps or deletes tasks from a job that
// already exceeds it, so a job written before the bound existed still has however
// many tasks it was created with and nothing but this cap bounds the line.
// log.Printf holds a global mutex, and an unbounded rendering would let one line
// hold that mutex for all of it. Both call sites
// report a condition the code itself argues should not happen, which is exactly
// the kind of line nobody watches.
// The dependents guard in particular is believed unreachable in any well-formed
// history; the cost of that belief being wrong is what this cap removes.
//
// The ids belong in the log rather than the error body: every handler in this
// codebase errors through writeError into {"error": string}, and inventing a
// second error shape for one endpoint is a bigger change than the diagnosis is
// worth. The full per-task detail is one GET /v1/jobs/{id} away, which is also
// why truncating here loses nothing that cannot be recovered.
func uuidStrHead(ids []pgtype.UUID, max int) string {
	n := len(ids)
	if n > max {
		n = max
	}
	parts := make([]string, 0, n+1)
	for _, id := range ids[:n] {
		parts = append(parts, uuidStr(id))
	}
	if len(ids) > n {
		parts = append(parts, fmt.Sprintf("... (+%d more)", len(ids)-n))
	}
	return strings.Join(parts, ",")
}

// handleRetryJob returns a finished job's tasks to the queue so the farm re-runs
// them. See docs/superpowers/specs/2026-08-13-job-retry-endpoint.md.
//
// This is a fenced multi-row write on tasks. The ordering below is load-bearing
// and every 4xx/5xx path returns before the commit, so the deferred rollback
// undoes any write - which is what makes "nothing was applied" literally true.
//
// No request body: ?task= is a query parameter, matching ?force= on the cancel
// sibling. readJSON is never called and must not be added.
func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	// ?task is required, single-valued and matched exactly. Query().Get() would
	// silently return the first of a repeated parameter, and ParseBool-style
	// leniency is wrong here: ?force=garbage fails safe to "graceful", while a
	// misread here means "re-ran everything". Parsed BEFORE any database work,
	// so a malformed request costs nothing and returns the same 400 for an
	// existing and a non-existent job.
	vals := r.URL.Query()["task"]
	if len(vals) != 1 || (vals[0] != "failed" && vals[0] != "all") {
		writeError(w, http.StatusBadRequest,
			`query parameter "task" is required and must be exactly "failed" or "all"`)
		return
	}
	mode := vals[0]
	includeDone := mode == "all"

	// Owner-or-admin gate FIRST, decided from an UNLOCKED read and before any
	// transaction is open. handleCancelJob does the same; see jobOwnerOr404 for
	// why the gate must not sit behind the row lock.
	job, err := s.q.GetJob(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}
	if !s.jobOwnerOr404(ctx, w, job) {
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	// Lock the job row FIRST, before touching any task row. handleCancelJob does
	// the same; see GetJobForUpdate for the two properties that depend on it.
	job, err = q.GetJobForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	// Retry requires a finished job. This gate reads the LOCKED row, so it is
	// decided against the same snapshot the writes below use. Because this gate admits only done and
	// failed, the ONLY job-status transition this endpoint can cause is
	// done|failed -> running, so RecomputeJobStatus - which has no notion of
	// `cancelled` - is unreachable from a cancelled job through this path. That
	// is a stronger property than fixing its CASE would give, and it is
	// verifiable by reading these eight lines.
	//
	// A cancelled job is refused rather than un-cancelled: CancelJobTasks
	// squashes cancellation onto `failed`, so ?task=failed on a cancelled job
	// would select every task that was in flight when the cancel landed. "Retry"
	// would silently mean "un-cancel everything".
	switch job.Status {
	case "done", "failed":
	case "cancelled":
		writeError(w, http.StatusConflict,
			"job was cancelled; retry is not available for a cancelled job")
		return
	default:
		writeError(w, http.StatusConflict,
			"job is not finished; retry is available for a done or failed job")
		return
	}

	selected, err := q.SelectRetryableTaskIDs(ctx, store.SelectRetryableTaskIDsParams{
		JobID: id, IncludeDone: includeDone,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if len(selected) == 0 {
		if includeDone {
			writeError(w, http.StatusConflict,
				"no tasks matched task=all; this job has no finished tasks")
		} else {
			writeError(w, http.StatusConflict,
				"no tasks matched task=failed; this job has no failed or timed_out tasks")
		}
		return
	}

	reopened, err := q.RetryJobTasks(ctx, store.RetryJobTasksParams{
		JobID: id, IncludeDone: includeDone,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	// The dependents guard is all-or-nothing by construction (its NOT EXISTS is
	// uncorrelated), so zero-against-nonzero is the guard and any other mismatch
	// is provably concurrency. That structural argument is why no extra query is
	// needed to classify these two cases apart.
	if len(reopened) == 0 {
		log.Printf("api: retry job %s task=%s blocked by dependents: selected=%d [%s]",
			uuidStr(id), mode, len(selected), uuidStrHead(selected, logIDHead))
		writeError(w, http.StatusConflict,
			"no tasks were reopened: a selected task has dependents that have already run, "+
				"or the job changed while the request was in flight; nothing was applied")
		return
	}
	if len(reopened) != len(selected) {
		log.Printf("api: retry job %s task=%s raced: selected=%d [%s] reopened=%d [%s]",
			uuidStr(id), mode, len(selected), uuidStrHead(selected, logIDHead),
			len(reopened), uuidStrHead(reopened, logIDHead))
		writeError(w, http.StatusConflict,
			"the job changed while the retry was in flight; nothing was applied - try again")
		return
	}

	// By construction this returns 'running': at least one task is now pending.
	// The return is discarded because the re-read below carries it, along with
	// the new updated_at.
	if _, err := q.RecomputeJobStatus(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	// Re-read inside the transaction so the response carries the recomputed
	// status and the new updated_at; RecomputeJobStatus returns only the status.
	job, err = q.GetJob(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Wake the dispatcher from INSIDE the transaction. Postgres queues pg_notify
	// payloads until commit, so this side effect is gated on BOTH the row count
	// (we only reach here with len(reopened) == len(selected) >= 1) and on the
	// transaction actually committing - a strictly stronger form of "gate any
	// side effect on the fence having matched" than a post-commit call. Same
	// shape as the requeue path in workers.go.
	if err := q.NotifyTaskSubmitted(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// After commit, matching handleCancelJob. Unlike cancel there is no agent
	// signal to send: every reopened row was terminal, so no agent holds it.
	//
	// The payload is built from the status this handler actually read, not from a
	// literal. It is provably 'running' today - RecomputeJobStatus cannot return
	// anything else with a task now pending - but nothing keeps a literal and the
	// response body in step if that changes, and the body already carries
	// job.Status.
	//
	// KNOWN AND ACCEPTED: this publish happens AFTER tx.Commit, so on a one-task
	// job the dispatcher can claim, run and finish the task in the window between
	// those two lines, and a subscriber then sees this 'running' arrive after the
	// 'done' that overtook it. The status is stale, not wrong - it is the status
	// at commit - and a subscriber that treats an SSE status as a cache hint
	// rather than a source of truth converges on the next refetch. handleCancelJob
	// has the identical post-commit shape; publishing inside the transaction
	// instead would announce a state that a rollback could still erase, which is
	// the worse failure. Do not "fix" one of these two handlers alone.
	s.broker.Publish(events.Event{
		Type:  "job",
		JobID: uuidStr(job.ID),
		Data:  []byte(fmt.Sprintf(`{"status":%q}`, job.Status)),
	})

	writeJSON(w, http.StatusOK, retryJobResponse{
		jobResponse:  toJobResponse(job, "", nil, nil),
		TasksRetried: len(reopened),
	})
}
