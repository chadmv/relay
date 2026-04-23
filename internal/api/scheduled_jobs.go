package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const minScheduleInterval = 30 * time.Second

type scheduledJobResponse struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	OwnerID       string          `json:"owner_id"`
	CronExpr      string          `json:"cron_expr"`
	Timezone      string          `json:"timezone"`
	JobSpec       json.RawMessage `json:"job_spec"`
	OverlapPolicy string          `json:"overlap_policy"`
	Enabled       bool            `json:"enabled"`
	NextRunAt     time.Time       `json:"next_run_at"`
	LastRunAt     *time.Time      `json:"last_run_at,omitempty"`
	LastJobID     string          `json:"last_job_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func toScheduledJobResponse(sj store.ScheduledJob) scheduledJobResponse {
	out := scheduledJobResponse{
		ID:            uuidStr(sj.ID),
		Name:          sj.Name,
		OwnerID:       uuidStr(sj.OwnerID),
		CronExpr:      sj.CronExpr,
		Timezone:      sj.Timezone,
		JobSpec:       rawJSON(sj.JobSpec),
		OverlapPolicy: sj.OverlapPolicy,
		Enabled:       sj.Enabled,
		NextRunAt:     sj.NextRunAt.Time,
		CreatedAt:     sj.CreatedAt.Time,
		UpdatedAt:     sj.UpdatedAt.Time,
	}
	if sj.LastRunAt.Valid {
		t := sj.LastRunAt.Time
		out.LastRunAt = &t
	}
	if sj.LastJobID.Valid {
		out.LastJobID = uuidStr(sj.LastJobID)
	}
	return out
}

type createScheduledJobRequest struct {
	Name          string          `json:"name"`
	CronExpr      string          `json:"cron_expr"`
	Timezone      string          `json:"timezone"`
	OverlapPolicy string          `json:"overlap_policy"`
	Enabled       *bool           `json:"enabled"`
	JobSpec       json.RawMessage `json:"job_spec"`
}

func (s *Server) handleCreateScheduledJob(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createScheduledJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.CronExpr == "" {
		writeError(w, http.StatusBadRequest, "cron_expr is required")
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if req.OverlapPolicy == "" {
		req.OverlapPolicy = "skip"
	}
	if req.OverlapPolicy != "skip" && req.OverlapPolicy != "allow" {
		writeError(w, http.StatusBadRequest, "overlap_policy must be 'skip' or 'allow'")
		return
	}

	if len(req.JobSpec) == 0 {
		writeError(w, http.StatusBadRequest, "job_spec is required")
		return
	}
	var spec JobSpec
	if err := json.Unmarshal(req.JobSpec, &spec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid job_spec JSON")
		return
	}
	if err := ValidateJobSpec(spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := schedrunner.ValidateMinInterval(req.CronExpr, req.Timezone, minScheduleInterval); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sched, err := schedrunner.ParseSchedule(req.CronExpr, req.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	next := sched.Next(time.Now())
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	row, err := s.q.CreateScheduledJob(r.Context(), store.CreateScheduledJobParams{
		Name:          req.Name,
		OwnerID:       u.ID,
		CronExpr:      req.CronExpr,
		Timezone:      req.Timezone,
		JobSpec:       req.JobSpec,
		OverlapPolicy: req.OverlapPolicy,
		Enabled:       enabled,
		NextRunAt:     pgtype.Timestamptz{Time: next, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create scheduled job failed")
		return
	}

	writeJSON(w, http.StatusCreated, toScheduledJobResponse(row))
}

// ownedScheduledJob fetches a schedule and verifies the caller is the owner or
// an admin. Returns the row and whether the caller has access.
func (s *Server) ownedScheduledJob(w http.ResponseWriter, r *http.Request, id pgtype.UUID) (store.ScheduledJob, bool) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return store.ScheduledJob{}, false
	}
	row, err := s.q.GetScheduledJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "scheduled job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return store.ScheduledJob{}, false
	}
	if !u.IsAdmin && row.OwnerID != u.ID {
		writeError(w, http.StatusNotFound, "scheduled job not found")
		return store.ScheduledJob{}, false
	}
	return row, true
}

func (s *Server) handleListScheduledJobs(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var rows []store.ScheduledJob
	var err error
	if u.IsAdmin {
		rows, err = s.q.ListScheduledJobs(r.Context())
	} else {
		rows, err = s.q.ListScheduledJobsByOwner(r.Context(), u.ID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
		return
	}
	out := make([]scheduledJobResponse, len(rows))
	for i, row := range rows {
		out[i] = toScheduledJobResponse(row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetScheduledJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	row, ok := s.ownedScheduledJob(w, r, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toScheduledJobResponse(row))
}

type patchScheduledJobRequest struct {
	Name          *string          `json:"name"`
	CronExpr      *string          `json:"cron_expr"`
	Timezone      *string          `json:"timezone"`
	OverlapPolicy *string          `json:"overlap_policy"`
	Enabled       *bool            `json:"enabled"`
	JobSpec       *json.RawMessage `json:"job_spec"`
}

func (s *Server) handlePatchScheduledJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	row, ok := s.ownedScheduledJob(w, r, id)
	if !ok {
		return
	}

	var req patchScheduledJobRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := row.Name
	if req.Name != nil {
		name = *req.Name
	}
	cronExpr := row.CronExpr
	if req.CronExpr != nil {
		cronExpr = *req.CronExpr
	}
	tz := row.Timezone
	if req.Timezone != nil {
		tz = *req.Timezone
	}
	overlap := row.OverlapPolicy
	if req.OverlapPolicy != nil {
		overlap = *req.OverlapPolicy
		if overlap != "skip" && overlap != "allow" {
			writeError(w, http.StatusBadRequest, "overlap_policy must be 'skip' or 'allow'")
			return
		}
	}
	enabled := row.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	jobSpecJSON := row.JobSpec
	if req.JobSpec != nil {
		var spec JobSpec
		if err := json.Unmarshal(*req.JobSpec, &spec); err != nil {
			writeError(w, http.StatusBadRequest, "invalid job_spec JSON")
			return
		}
		if err := ValidateJobSpec(spec); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		jobSpecJSON = []byte(*req.JobSpec)
	}

	nextRunAt := row.NextRunAt
	if req.CronExpr != nil || req.Timezone != nil || (req.Enabled != nil && *req.Enabled && !row.Enabled) {
		if err := schedrunner.ValidateMinInterval(cronExpr, tz, minScheduleInterval); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sched, err := schedrunner.ParseSchedule(cronExpr, tz)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		nextRunAt = pgtype.Timestamptz{Time: sched.Next(time.Now()), Valid: true}
	}

	updated, err := s.q.UpdateScheduledJob(r.Context(), store.UpdateScheduledJobParams{
		ID:            id,
		Name:          name,
		CronExpr:      cronExpr,
		Timezone:      tz,
		JobSpec:       jobSpecJSON,
		OverlapPolicy: overlap,
		Enabled:       enabled,
		NextRunAt:     nextRunAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, toScheduledJobResponse(updated))
}

func (s *Server) handleDeleteScheduledJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, ok := s.ownedScheduledJob(w, r, id); !ok {
		return
	}
	n, err := s.q.DeleteScheduledJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "scheduled job not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunScheduledJobNow(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	row, ok := s.ownedScheduledJob(w, r, id)
	if !ok {
		return
	}

	var spec JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		writeError(w, http.StatusInternalServerError, "stored job_spec is invalid")
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx failed")
		return
	}
	defer tx.Rollback(ctx)

	job, tasks, err := CreateJobFromSpec(ctx, s.q.WithTx(tx), spec, row.OwnerID, row.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create job failed")
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
	writeJSON(w, http.StatusCreated, toJobResponse(job, "", tasks, taskDeps))
}
