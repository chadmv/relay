package api

import (
	"encoding/json"
	"errors"
	"log"
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
	OwnerEmail    string          `json:"owner_email"`
	CronExpr      string          `json:"cron_expr"`
	Timezone      string          `json:"timezone"`
	JobSpec       json.RawMessage `json:"job_spec"`
	OverlapPolicy string          `json:"overlap_policy"`
	Enabled       bool            `json:"enabled"`
	NextRunAt     time.Time       `json:"next_run_at"`
	LastRunAt     *time.Time      `json:"last_run_at,omitempty"`
	LastJobID     string          `json:"last_job_id,omitempty"`
	// The last time the SCHEDULER failed to produce a job from this schedule,
	// and why. ABSENT MEANS HEALTHY - not "" and not null - which is what makes
	// `omitempty` on a string safe here: the write site
	// (internal/schedrunner/failure.go) never stores an empty string, precisely
	// so that an empty one cannot be confused with an absent one.
	//
	// THE TEXT IS OPERATOR-SUPPLIED. It is derived from the stored job_spec: a
	// task name the schedule's owner chose flows verbatim into jobspec.Validate's
	// "task %s: ..." message. An admin reading someone else's schedule is
	// therefore reading partly attacker-chosen prose. It is sanitized at the
	// write site (control characters stripped, truncated to 1 KB on a rune
	// boundary), and every renderer must treat it as untrusted text: a React text
	// child, never chrome, never dangerouslySetInnerHTML, and prefixed with its
	// provenance in the CLI.
	//
	// It is safe to serve because the read is owner-or-admin: ownedScheduledJob
	// 404s everyone else and both non-admin list arms are owner-scoped.
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	if sj.LastError != nil {
		out.LastError = *sj.LastError
	}
	if sj.LastErrorAt.Valid {
		t := sj.LastErrorAt.Time
		out.LastErrorAt = &t
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
	if !readJSON(w, r, &req) {
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

var ScheduledJobsSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at":  SortKeyTimestamp,
		"name":        SortKeyText,
		"next_run_at": SortKeyTimestamp,
		"updated_at":  SortKeyTimestamp,
	},
}

func scheduledJobsRowKey(s store.ScheduledJob) (anySortVal, pgtype.UUID) {
	return s.CreatedAt.Time, s.ID
}

func scheduledJobsRowKeyByName(s store.ScheduledJob) (anySortVal, pgtype.UUID) {
	return s.Name, s.ID
}

func scheduledJobsRowKeyByNextRun(s store.ScheduledJob) (anySortVal, pgtype.UUID) {
	return s.NextRunAt.Time, s.ID
}

func scheduledJobsRowKeyByUpdated(s store.ScheduledJob) (anySortVal, pgtype.UUID) {
	return s.UpdatedAt.Time, s.ID
}

// fillOwnerEmails resolves owner_email for a set of items, mutating them in
// place. Pass selfEmail when every item is known to belong to the caller, which
// skips the lookup entirely; pass "" to batch-resolve from the store.
//
// Three callers, and the choice is per call site rather than per path: the
// owner-scoped list passes selfEmail because every row is the caller's, the
// admin list passes "" because the rows are anyone's, and handleGetScheduledJob
// decides per row. A store lookup that fails is logged and leaves owner_email
// empty rather than failing the request - the same degradation for all three.
func (s *Server) fillOwnerEmails(r *http.Request, items []scheduledJobResponse, selfEmail string) {
	if selfEmail != "" {
		for i := range items {
			items[i].OwnerEmail = selfEmail
		}
		return
	}
	ids := make([]pgtype.UUID, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		if _, ok := seen[it.OwnerID]; !ok {
			seen[it.OwnerID] = struct{}{}
			id, err := parseUUID(it.OwnerID)
			if err == nil {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := s.q.GetUserEmailsByIDs(r.Context(), ids)
	if err != nil {
		// best-effort: leave owner_email empty on lookup failure, but log it
		// so the failure is visible to operators instead of silently swallowed.
		log.Printf("scheduled_jobs: GetUserEmailsByIDs (%d owner id(s)): %v", len(ids), err)
		return
	}
	emailByID := make(map[string]string, len(rows))
	for _, row := range rows {
		emailByID[uuidStr(row.ID)] = row.Email
	}
	for i := range items {
		items[i].OwnerEmail = emailByID[items[i].OwnerID]
	}
}

func (s *Server) handleListScheduledJobs(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	pp, ok := parsePage(w, r, ScheduledJobsSortSpec)
	if !ok {
		return
	}

	ctx := r.Context()

	if u.IsAdmin {
		var rows []store.ScheduledJob
		var err error
		var items []scheduledJobResponse
		var next string

		switch pp.Sort {
		case "-created_at":
			rows, err = s.q.ListScheduledJobsPage(ctx, store.ListScheduledJobsPageParams{
				CursorSet: pp.Cursor.Set,
				CursorTs:  pp.CursorTs(),
				CursorID:  pp.Cursor.ID,
				PageLimit: pp.Limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
				return
			}
			items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKey)

		case "created_at":
			rows, err = s.q.ListScheduledJobsPageByCreatedAsc(ctx, store.ListScheduledJobsPageByCreatedAscParams{
				CursorSet: pp.Cursor.Set,
				CursorTs:  pp.CursorTs(),
				CursorID:  pp.Cursor.ID,
				PageLimit: pp.Limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
				return
			}
			items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKey)

		case "-name":
			rows, err = s.q.ListScheduledJobsPageByNameDesc(ctx, store.ListScheduledJobsPageByNameDescParams{
				CursorSet: pp.Cursor.Set,
				CursorV:   pp.Cursor.StrVal,
				CursorID:  pp.Cursor.ID,
				PageLimit: pp.Limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
				return
			}
			items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByName)

		case "name":
			rows, err = s.q.ListScheduledJobsPageByNameAsc(ctx, store.ListScheduledJobsPageByNameAscParams{
				CursorSet: pp.Cursor.Set,
				CursorV:   pp.Cursor.StrVal,
				CursorID:  pp.Cursor.ID,
				PageLimit: pp.Limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
				return
			}
			items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByName)

		case "-next_run_at":
			rows, err = s.q.ListScheduledJobsPageByNextRunDesc(ctx, store.ListScheduledJobsPageByNextRunDescParams{
				CursorSet: pp.Cursor.Set,
				CursorTs:  pp.CursorTs(),
				CursorID:  pp.Cursor.ID,
				PageLimit: pp.Limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
				return
			}
			items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByNextRun)

		case "next_run_at":
			rows, err = s.q.ListScheduledJobsPageByNextRunAsc(ctx, store.ListScheduledJobsPageByNextRunAscParams{
				CursorSet: pp.Cursor.Set,
				CursorTs:  pp.CursorTs(),
				CursorID:  pp.Cursor.ID,
				PageLimit: pp.Limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
				return
			}
			items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByNextRun)

		case "-updated_at":
			rows, err = s.q.ListScheduledJobsPageByUpdatedDesc(ctx, store.ListScheduledJobsPageByUpdatedDescParams{
				CursorSet: pp.Cursor.Set,
				CursorTs:  pp.CursorTs(),
				CursorID:  pp.Cursor.ID,
				PageLimit: pp.Limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
				return
			}
			items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByUpdated)

		case "updated_at":
			rows, err = s.q.ListScheduledJobsPageByUpdatedAsc(ctx, store.ListScheduledJobsPageByUpdatedAscParams{
				CursorSet: pp.Cursor.Set,
				CursorTs:  pp.CursorTs(),
				CursorID:  pp.Cursor.ID,
				PageLimit: pp.Limit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
				return
			}
			items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByUpdated)

		default:
			panic("handleListScheduledJobs admin: missing dispatch arm for sort key " + pp.Sort)
		}

		total, err := s.q.CountScheduledJobs(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count scheduled jobs failed")
			return
		}
		s.fillOwnerEmails(r, items, "")
		writeJSON(w, http.StatusOK, page[scheduledJobResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	// Non-admin: owner-scoped queries.
	var rows []store.ScheduledJob
	var err error
	var items []scheduledJobResponse
	var next string

	switch pp.Sort {
	case "-created_at":
		rows, err = s.q.ListScheduledJobsByOwnerPage(ctx, store.ListScheduledJobsByOwnerPageParams{
			OwnerID:   u.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKey)

	case "created_at":
		rows, err = s.q.ListScheduledJobsByOwnerPageByCreatedAsc(ctx, store.ListScheduledJobsByOwnerPageByCreatedAscParams{
			OwnerID:   u.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKey)

	case "-name":
		rows, err = s.q.ListScheduledJobsByOwnerPageByNameDesc(ctx, store.ListScheduledJobsByOwnerPageByNameDescParams{
			OwnerID:   u.ID,
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByName)

	case "name":
		rows, err = s.q.ListScheduledJobsByOwnerPageByNameAsc(ctx, store.ListScheduledJobsByOwnerPageByNameAscParams{
			OwnerID:   u.ID,
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByName)

	case "-next_run_at":
		rows, err = s.q.ListScheduledJobsByOwnerPageByNextRunDesc(ctx, store.ListScheduledJobsByOwnerPageByNextRunDescParams{
			OwnerID:   u.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByNextRun)

	case "next_run_at":
		rows, err = s.q.ListScheduledJobsByOwnerPageByNextRunAsc(ctx, store.ListScheduledJobsByOwnerPageByNextRunAscParams{
			OwnerID:   u.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByNextRun)

	case "-updated_at":
		rows, err = s.q.ListScheduledJobsByOwnerPageByUpdatedDesc(ctx, store.ListScheduledJobsByOwnerPageByUpdatedDescParams{
			OwnerID:   u.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByUpdated)

	case "updated_at":
		rows, err = s.q.ListScheduledJobsByOwnerPageByUpdatedAsc(ctx, store.ListScheduledJobsByOwnerPageByUpdatedAscParams{
			OwnerID:   u.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toScheduledJobResponse, scheduledJobsRowKeyByUpdated)

	default:
		panic("handleListScheduledJobs owner: missing dispatch arm for sort key " + pp.Sort)
	}

	total, err := s.q.CountScheduledJobsByOwner(ctx, u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count scheduled jobs failed")
		return
	}
	s.fillOwnerEmails(r, items, u.Email)
	writeJSON(w, http.StatusOK, page[scheduledJobResponse]{Items: items, NextCursor: next, Total: total})
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
	// fillOwnerEmails mutates its elements in place, so hand it a one-element
	// slice and write that element back out. selfEmail short-circuits the
	// store lookup only when the caller owns this row; an admin reading someone
	// else's schedule must resolve the real owner, exactly as the admin list arm
	// does. ownedScheduledJob has already established the caller may read the row.
	selfEmail := ""
	if u, ok := UserFromCtx(r.Context()); ok && row.OwnerID == u.ID {
		selfEmail = u.Email
	}
	items := []scheduledJobResponse{toScheduledJobResponse(row)}
	s.fillOwnerEmails(r, items, selfEmail)
	writeJSON(w, http.StatusOK, items[0])
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
	if !readJSON(w, r, &req) {
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

	// CLEAR THE FAILURE RECORD IF, AND ONLY IF, THIS PATCH CHANGED ONE OF THE
	// THREE INPUTS THE THREE RECORDED FAILURE CLASSES ARE ABOUT.
	//
	// job_spec, cron_expr and timezone are exactly what an undecodable spec, an
	// unparseable cron and a failed jobspec.Validate are about, and all three have
	// already been validated above before reaching here - so any recorded failure
	// about the OLD values is stale by construction.
	//
	// A patch of name, overlap_policy or enabled PRESERVES the record. Renaming a
	// schedule must not erase the only signal that it is broken, and on an
	// @monthly schedule nothing would rewrite it for a month. Enabling and
	// disabling preserve it too: nothing about the spec changed, and a re-enabled
	// schedule that still carries its failure is showing the truth at the most
	// useful moment to see it.
	//
	// It is a BOOLEAN ARGUMENT rather than a read-modify-write. The row was read
	// through ownedScheduledJob without a lock, so reading last_error into Go and
	// writing it back would let this PATCH carry a stale error forward over a
	// failure a tick recorded in between. The SQL CASE means the row's own value
	// is never round-tripped through the application. (next_run_at in this same
	// handler DOES have that read-modify-write hazard; this slice does not fix it
	// and does not join it.)
	clearFailure := req.JobSpec != nil || req.CronExpr != nil || req.Timezone != nil

	updated, err := s.q.UpdateScheduledJob(r.Context(), store.UpdateScheduledJobParams{
		ID:            id,
		Name:          name,
		CronExpr:      cronExpr,
		Timezone:      tz,
		JobSpec:       jobSpecJSON,
		OverlapPolicy: overlap,
		Enabled:       enabled,
		NextRunAt:     nextRunAt,
		ClearFailure:  clearFailure,
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

	// 400, not 500, for the same reason the validation refusal below is: an
	// identical request made later gets an identical answer, which is exactly the
	// partition relayclient.ErrorIsTransient documents, so a 500 here told a
	// polling caller to retry a permanently broken schedule forever. The
	// operator's remedy is the same too - PATCH a new job_spec, or delete and
	// recreate.
	//
	// The two branches DO differ, and it does not change the code: a validation
	// failure is reachable with nothing corrupt (a later release tightened a
	// bound), whereas a decode failure is not reachable through any current write
	// path, since POST and PATCH both unmarshal before they validate. That makes
	// it rarer, not transient, and no caller can tell the two apart from outside.
	var spec JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		writeError(w, http.StatusBadRequest, "stored job_spec is invalid")
		return
	}

	// Validate the STORED spec explicitly, ahead of the transaction, so a spec
	// that no longer passes is answered as a fact about the request rather than
	// as a server fault. CreateJobFromSpec validates too, but every error it
	// returns collapses into one 500 below - which would both discard the
	// per-task message and, because relayclient.ErrorIsTransient reads 5xx as
	// transient, tell a polling caller to retry a permanently broken schedule
	// forever. The bounds in jobspec.Validate are retroactive over specs stored
	// by earlier releases, so this is reachable without anything being corrupt.
	//
	// run-now is the ONLY interactive path that can explain why a schedule
	// stopped producing jobs: schedrunner's fireOne logs one server-side line
	// and advances next_run_at, leaving nothing user-visible behind.
	if err := ValidateJobSpec(spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "begin tx failed")
		return
	}
	defer tx.Rollback(ctx)

	// run-now submits the job as the schedule owner, not the calling admin.
	// This preserves audit semantics: the job's submitted_by reflects whose
	// template fired, regardless of who triggered the explicit run.
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
