package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
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
	// The status of the job last_job_id names, verbatim from jobs.status: the
	// vocabulary migration 000019 constrains and TestJobsStatusVocabularyIsExactly
	// pins. NOT the pending -> queued rename jobStatsResponse performs; this field
	// must agree with jobResponse.status, which is what the cell linking to it
	// shows.
	//
	// PRESENT EXACTLY WHEN last_job_id IS PRESENT. The pairing is what makes an
	// absent key mean one thing - "no scheduled fire has produced a job" - and
	// never "unknown" or "healthy". It holds because the FK on last_job_id is
	// ON DELETE SET NULL, so a non-NULL id names a row that exists, and because
	// fillLastJobStatuses fails the request rather than dropping the key.
	//
	// The pairing binds any site that emits a schedule body, so a handler that
	// skips the enrichment breaks it. The mapper test cannot see that; the wire
	// tests can - TestListScheduledJobs_LastJobStatusPairingOnTheWire and
	// TestScheduledJob_LastJobStatusIsLive.
	//
	// Independent of last_error below, which records a fire that produced NO job.
	// Both may be present at once and that is not a contradiction.
	LastJobStatus string `json:"last_job_status,omitempty"`
	// The last time the SCHEDULER failed to produce a job from this schedule,
	// and why. ABSENT MEANS HEALTHY - not "" and not null - which is what makes
	// `omitempty` on a string safe here: the write site
	// (internal/schedrunner/failure.go) never stores an empty string, precisely
	// so that an empty one cannot be confused with an absent one.
	//
	// THE TEXT IS OPERATOR-SUPPLIED. It is derived from the schedule's stored
	// configuration - its job_spec, or its cron_expr and timezone when
	// schedrunner records a "parse cron: ..." failure - and it MAY quote prose the
	// schedule's owner chose: a task name flows verbatim into jobspec.Validate's
	// "task %s: ..." message, a cron expression into ParseSchedule's "invalid cron
	// expression %q". Some messages are fixed relay text with nothing
	// operator-chosen in them; every renderer treats the whole class the same way,
	// because the alternative is string-matching the server's internal branches.
	// An admin reading someone else's schedule is therefore reading potentially
	// attacker-chosen prose. It is sanitized at the
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

	items := []scheduledJobResponse{toScheduledJobResponse(row)}
	// The creator is the owner, so the email needs no lookup.
	s.fillOwnerEmails(r, items, u.Email)
	// A freshly created row has never fired, so this can find nothing to fill.
	// It runs anyway because the pairing is a property of every site that emits
	// a schedule body, not of the sites that happen to need it today.
	if err := s.fillLastJobStatuses(r, items); err != nil {
		log.Printf("scheduled_jobs: fillLastJobStatuses: %v", err)
		writeError(w, http.StatusInternalServerError, "create scheduled job failed")
		return
	}
	writeJSON(w, http.StatusCreated, items[0])
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

// fillLastJobStatuses resolves last_job_status for a set of items, mutating them
// in place, from one batched lookup on the job ids the items already carry.
//
// IT FAILS THE REQUEST RATHER THAN DEGRADING, which is a deliberate divergence
// from fillOwnerEmails immediately above. owner_email is a key that is always
// present, so an empty value is visibly unknown and the list is still usable.
// last_job_status signals through key PRESENCE, so degrading would forge the
// signal "this schedule has never produced a job" out of a database fault, and a
// renderer would draw a missing dot as a fact.
func (s *Server) fillLastJobStatuses(r *http.Request, items []scheduledJobResponse) error {
	ids := make([]pgtype.UUID, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		if it.LastJobID == "" {
			continue
		}
		if _, ok := seen[it.LastJobID]; ok {
			continue
		}
		seen[it.LastJobID] = struct{}{}
		id, err := parseUUID(it.LastJobID)
		if err != nil {
			return fmt.Errorf("last_job_id is not a uuid: %w", err)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.q.GetJobStatusesByIDs(r.Context(), ids)
	if err != nil {
		return fmt.Errorf("get job statuses (%d id(s)): %w", len(ids), err)
	}
	statusByID := make(map[string]string, len(rows))
	for _, row := range rows {
		statusByID[uuidStr(row.ID)] = row.Status
	}
	for i := range items {
		if items[i].LastJobID == "" {
			continue
		}
		st, ok := statusByID[items[i].LastJobID]
		if !ok {
			// The FK guarantees the row exists, so a miss means the pairing
			// invariant cannot be honoured for this item. Emitting a last_job_id
			// with no status would break the contract silently.
			return fmt.Errorf("job %s has no status row", items[i].LastJobID)
		}
		items[i].LastJobStatus = st
	}
	return nil
}

// scheduleFilters carries the two optional GET /v1/scheduled-jobs predicates in
// the exact types the generated sqlc Params fields use, so a call site spreads
// them without conversion. The zero value means "no filter active": a nil Q and
// a nil Enabled each send SQL NULL, which the predicates read as "match
// everything".
type scheduleFilters struct {
	Enabled *bool
	Q       *string
}

// scheduleFilterParams are the two query parameters parseScheduleFilters reads.
// handleListScheduledJobs passes them to rejectRepeatedParams before calling in.
var scheduleFilterParams = []string{"enabled", "q"}

// parseScheduleFilters produces the two optional GET /v1/scheduled-jobs
// predicates. On invalid input it writes the response itself and returns
// ok=false. The caller spreads the result into every list and count Params
// struct on its path; a call site that omits a field disables that filter for
// its arm alone, with no error, which is what
// TestListScheduledJobs_FilterArms_FirstPage enumerates.
//
// qs is the query string parsePage already parsed and arity-checked; see
// parseFilterQ for why it is passed rather than re-read.
func parseScheduleFilters(w http.ResponseWriter, qs url.Values) (scheduleFilters, bool) {
	var f scheduleFilters

	q, ok := parseFilterQ(w, qs)
	if !ok {
		return scheduleFilters{}, false
	}
	f.Q = q

	// A tri-state, unlike the jobs list's ?mine=: enabled=false is the real
	// request "only paused schedules", so it must produce a pointer to false and
	// never be folded into absent.
	if raw := qs.Get("enabled"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid enabled; expected true or false")
			return scheduleFilters{}, false
		}
		f.Enabled = &b
	}

	return f, true
}

// scheduledJobStatsResponse is the schedules summary strip. Every key is always
// present; there is no omitempty anywhere in it, so a zero is a zero and never
// an absence.
type scheduledJobStatsResponse struct {
	Enabled       int64 `json:"enabled"`
	Paused        int64 `json:"paused"`
	Total         int64 `json:"total"`
	FailedRuns24h int64 `json:"failed_runs_24h"`
	Failing       int64 `json:"failing"`
}

// handleScheduledJobStats serves the fleet-wide census for an admin and the
// owner-scoped census for a non-admin.
//
// An absent identity is refused before the scope is built, because a zero
// pgtype.UUID is the same SQL NULL that means "fleet-wide" to the census
// statements: without this a non-admin whose id failed to resolve would be
// answered with the whole farm's numbers.
//
// AUTH-ONLY, not admin-only, and deliberately unlike /v1/server/counters: those
// are process-lifetime in-memory numbers describing adversary activity, while
// this is a database census of rows the caller may already page through one
// screen at a time. An admin-only version would leave every non-admin with a
// page-scoped strip.
//
// It accepts NO filters. The strip's purpose is a fleet-accurate count, and the
// list's own total already answers the filtered question.
func (s *Server) handleScheduledJobStats(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// SQL NULL for an admin: no owner predicate, so the census is fleet-wide.
	// The guard above it is what keeps that sentinel out of reach of a caller
	// who is not an admin.
	var scope pgtype.UUID
	if !u.IsAdmin {
		if !u.ID.Valid {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		scope = u.ID
	}

	ctx := r.Context()
	counts, err := s.q.ScheduledJobCounts(ctx, scope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scheduled job stats failed")
		return
	}

	writeJSON(w, http.StatusOK, scheduledJobStatsResponse{
		Enabled: counts.Enabled,
		Paused:  counts.Paused,
		// Computed from the two buckets rather than counted separately, so the
		// identity holds by construction.
		Total:         counts.Enabled + counts.Paused,
		FailedRuns24h: counts.FailedRuns24h,
		Failing:       counts.Failing,
	})
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

	if !rejectRepeatedParams(w, pp.Query, scheduleFilterParams...) {
		return
	}
	filters, ok := parseScheduleFilters(w, pp.Query)
	if !ok {
		return
	}

	// The same bucket handleListJobs charges, at the same point in the same
	// order: after the filters parse, gated on a non-nil needle. ONE bucket over
	// both routes, because the quantity bounded is scan work and it does not care
	// which route bought it - two buckets would hand a caller alternating routes
	// exactly twice the ceiling. The identity 401 at the top of this handler has
	// already run, so allowSearch's own fail-closed 401 is unreachable from here;
	// it is kept because the helper, not its callers, owns that decision.
	if filters.Q != nil && !s.allowSearch(w, u) {
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
				Enabled:   filters.Enabled,
				Q:         filters.Q,
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
				Enabled:   filters.Enabled,
				Q:         filters.Q,
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
				Enabled:   filters.Enabled,
				Q:         filters.Q,
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
				Enabled:   filters.Enabled,
				Q:         filters.Q,
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
				Enabled:   filters.Enabled,
				Q:         filters.Q,
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
				Enabled:   filters.Enabled,
				Q:         filters.Q,
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
				Enabled:   filters.Enabled,
				Q:         filters.Q,
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
				Enabled:   filters.Enabled,
				Q:         filters.Q,
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

		total, err := s.q.CountScheduledJobs(ctx, store.CountScheduledJobsParams{
			Enabled: filters.Enabled,
			Q:       filters.Q,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count scheduled jobs failed")
			return
		}
		s.fillOwnerEmails(r, items, "")
		if err := s.fillLastJobStatuses(r, items); err != nil {
			log.Printf("scheduled_jobs: fillLastJobStatuses: %v", err)
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
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
			Enabled:   filters.Enabled,
			Q:         filters.Q,
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
			Enabled:   filters.Enabled,
			Q:         filters.Q,
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
			Enabled:   filters.Enabled,
			Q:         filters.Q,
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
			Enabled:   filters.Enabled,
			Q:         filters.Q,
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
			Enabled:   filters.Enabled,
			Q:         filters.Q,
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
			Enabled:   filters.Enabled,
			Q:         filters.Q,
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
			Enabled:   filters.Enabled,
			Q:         filters.Q,
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
			Enabled:   filters.Enabled,
			Q:         filters.Q,
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

	total, err := s.q.CountScheduledJobsByOwner(ctx, store.CountScheduledJobsByOwnerParams{
		OwnerID: u.ID,
		Enabled: filters.Enabled,
		Q:       filters.Q,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count scheduled jobs failed")
		return
	}
	s.fillOwnerEmails(r, items, u.Email)
	if err := s.fillLastJobStatuses(r, items); err != nil {
		log.Printf("scheduled_jobs: fillLastJobStatuses: %v", err)
		writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
		return
	}
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
	if err := s.fillLastJobStatuses(r, items); err != nil {
		log.Printf("scheduled_jobs: fillLastJobStatuses: %v", err)
		writeError(w, http.StatusInternalServerError, "get scheduled job failed")
		return
	}
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

	// CLEAR THE FAILURE RECORD IF, AND ONLY IF, THIS PATCH TOUCHED ONE OF THE
	// THREE INPUTS THE RECORDED FAILURE CLASSES ARE ABOUT *AND* THE ROW IT
	// LEAVES BEHIND ACTUALLY VALIDATES. Both arms are load-bearing and they
	// answer different questions.
	//
	// THE FIRST ARM asks whether this PATCH is entitled to revisit the record at
	// all. A patch of name, overlap_policy or enabled PRESERVES it unconditionally:
	// renaming a schedule must not erase the only signal that it is broken, and on
	// an @monthly schedule nothing would rewrite it for a month. Enabling and
	// disabling preserve it too - nothing about the spec changed, and a re-enabled
	// schedule that still carries its failure is showing the truth at the most
	// useful moment to see it.
	//
	// THE SECOND ARM asks whether the record is actually stale, and it exists
	// because the first arm alone was ASSERTING that rather than checking it. The
	// validation above runs per key: job_spec is checked only inside
	// `if req.JobSpec != nil`, and the nextRunAt block re-parses cron/tz only when
	// one of them is supplied. So presence of ANY of the three keys was clearing a
	// record about the two the patch never looked at. Both directions were live
	// and both are reached from `relay schedules update`: a cron-only PATCH erased
	// a still-true "retries must be between 0 and 10", and a job_spec-only PATCH
	// erased a still-true "parse cron: ..." without ever calling ParseSchedule.
	//
	// IT ASKS schedrunner, NOT A LOCAL COPY. schedrunner.ValidateStoredSchedule is
	// the same unmarshal -> ParseSchedule -> Validate, in the same order, that
	// fireOne and the startup sweep use to WRITE the record. A second
	// implementation here could clear on a verdict the writer disagrees with,
	// which is the defect in a new spelling.
	//
	// A PATCH WHOSE EFFECTIVE ROW STILL DOES NOT VALIDATE IS STILL A 200. The
	// handler validates what it is GIVEN; an operator repairing a broken schedule
	// one field at a time is a legitimate sequence, and refusing it because some
	// other stored field is still broken would make a two-step repair impossible.
	// Only what the write CLEARS is conditional.
	//
	// It is a BOOLEAN ARGUMENT rather than a read-modify-write. The row was read
	// through ownedScheduledJob without a lock, so reading last_error into Go and
	// writing it back would let this PATCH carry a stale error forward over a
	// failure a tick recorded in between. The SQL CASE means the row's own value
	// is never round-tripped through the application. (next_run_at in this same
	// handler DOES have that read-modify-write hazard; this slice does not fix it
	// and does not join it.)
	clearFailure := (req.JobSpec != nil || req.CronExpr != nil || req.Timezone != nil) &&
		schedrunner.ValidateStoredSchedule(jobSpecJSON, cronExpr, tz) == nil

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
	items := []scheduledJobResponse{toScheduledJobResponse(updated)}
	// Owner-or-admin, so resolve per row exactly as the get does: an admin
	// patching someone else's schedule must see the real owner.
	selfEmail := ""
	if u, ok := UserFromCtx(r.Context()); ok && updated.OwnerID == u.ID {
		selfEmail = u.Email
	}
	s.fillOwnerEmails(r, items, selfEmail)
	if err := s.fillLastJobStatuses(r, items); err != nil {
		log.Printf("scheduled_jobs: fillLastJobStatuses: %v", err)
		// The update COMMITTED; only the response could not be built. Say so,
		// because "update failed" would send the caller to retry a write that
		// already landed.
		writeError(w, http.StatusInternalServerError, "update applied but the response could not be built")
		return
	}
	writeJSON(w, http.StatusOK, items[0])
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
	// run-now is the interactive path for this question, and since last_error
	// landed (migration 000022) it is no longer the only surface that carries the
	// answer: fireOne's failure branch records the same message on the row, and
	// GET /v1/scheduled-jobs and its list sibling both serve it. What run-now still
	// has that the recorded value does not is the UNTRUNCATED message and an
	// answer on demand rather than at the next scheduled fire - which is why it
	// stays the documented first step when a schedule reports a failure.
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
