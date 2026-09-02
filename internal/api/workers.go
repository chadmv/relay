package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type workerResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Hostname   string          `json:"hostname"`
	CpuCores   int32           `json:"cpu_cores"`
	RamGb      int32           `json:"ram_gb"`
	GpuCount   int32           `json:"gpu_count"`
	GpuModel   string          `json:"gpu_model"`
	Os         string          `json:"os"`
	MaxSlots   int32           `json:"max_slots"`
	Labels     json.RawMessage `json:"labels"`
	Status     string          `json:"status"`
	LastSeenAt   *time.Time      `json:"last_seen_at,omitempty"`
	LastSampleAt *time.Time      `json:"last_sample_at,omitempty"`
	DisabledAt   *time.Time      `json:"disabled_at,omitempty"`
	RevokedAt    *time.Time      `json:"revoked_at,omitempty"`
}

// disableWorkerResponse is the body returned by the disable endpoint. It embeds
// workerResponse (its fields flatten into the JSON object) and adds the count of
// tasks that were requeued - always 0 in drain mode.
type disableWorkerResponse struct {
	workerResponse
	RequeuedTasks int `json:"requeued_tasks"`
}

// deleteWorkerResponse is the body returned by DELETE /v1/workers/{id}. It is a
// 200 with a body rather than a 204 ON PURPOSE (spec 6.4): relay has no audit
// log, so these four counts plus the embedded identity are the ONLY record of
// what the delete destroyed. attribution_cleared was added after review found
// the first three omitted the LARGEST destruction - see its field comment.
// The embedded workerResponse is the row as it was, read under the FOR UPDATE.
type deleteWorkerResponse struct {
	workerResponse
	RequeuedTasks       int `json:"requeued_tasks"`
	ReservationsUpdated int `json:"reservations_updated"`
	EnrollmentsUnlinked int `json:"enrollments_unlinked"`
	// AttributionCleared is the count of this worker's TERMINAL tasks whose
	// worker_id the DELETE nulls via ON DELETE SET NULL. It is the largest thing a
	// delete destroys and the requeue does not rescue it; worker_id is public API,
	// so after this "which machine ran that job" is unanswerable for those rows.
	AttributionCleared int `json:"attribution_cleared"`
}

func toWorkerResponse(w store.Worker) workerResponse {
	var lastSeen *time.Time
	if w.LastSeenAt.Valid {
		t := w.LastSeenAt.Time
		lastSeen = &t
	}
	// A disabled worker keeps its live liveness status internally, but the API
	// reports "disabled" so existing consumers that read only `status` treat it
	// as unavailable. `disabled_at` is also exposed so both states are visible.
	status := w.Status
	var disabledAt *time.Time
	if w.DisabledAt.Valid {
		t := w.DisabledAt.Time
		disabledAt = &t
		status = "disabled"
	}
	var revokedAt *time.Time
	if w.RevokedAt.Valid {
		t := w.RevokedAt.Time
		revokedAt = &t
	}
	return workerResponse{
		ID:         uuidStr(w.ID),
		Name:       w.Name,
		Hostname:   w.Hostname,
		CpuCores:   w.CpuCores,
		RamGb:      w.RamGb,
		GpuCount:   w.GpuCount,
		GpuModel:   w.GpuModel,
		Os:         w.Os,
		MaxSlots:   w.MaxSlots,
		Labels:     rawJSON(w.Labels),
		Status:     status,
		LastSeenAt: lastSeen,
		DisabledAt: disabledAt,
		RevokedAt:  revokedAt,
	}
}

// workerStatsResponse is the fleet-wide summary returned by GET /v1/workers/stats.
// total is the sum of the four buckets; revoked workers are in no bucket and are
// therefore excluded from total.
type workerStatsResponse struct {
	Online   int64 `json:"online"`
	Stale    int64 `json:"stale"`
	Offline  int64 `json:"offline"`
	Disabled int64 `json:"disabled"`
	Total    int64 `json:"total"`
}

func (s *Server) handleWorkerStats(w http.ResponseWriter, r *http.Request) {
	counts, err := s.q.WorkerStatusCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "worker stats failed")
		return
	}
	writeJSON(w, http.StatusOK, workerStatsResponse{
		Online:   counts.Online,
		Stale:    counts.Stale,
		Offline:  counts.Offline,
		Disabled: counts.Disabled,
		Total:    counts.Online + counts.Stale + counts.Offline + counts.Disabled,
	})
}

var WorkersSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at":   SortKeyTimestamp,
		"name":         SortKeyText,
		"status":       SortKeyText,
		"last_seen_at": SortKeyTimestamp,
	},
}

// RevokedWorkersSortSpec drives GET /v1/workers/revoked. The endpoint is
// DESC-only; the revoked_at key exists solely so the "-revoked_at" default
// resolves in parseSort. handleListRevokedWorkers rejects an ascending request.
var RevokedWorkersSortSpec = SortSpec{
	Default: "-revoked_at",
	Keys: map[string]SortKeyKind{
		"revoked_at": SortKeyTimestamp,
	},
}

// WorkerTasksSortSpec drives GET /v1/workers/{id}/tasks. The endpoint serves one
// order; the assigned_at key exists so the "-assigned_at" default resolves in
// parseSort and tags the cursor. handleListWorkerTasks refuses an ascending
// request, as handleListRevokedWorkers does.
var WorkerTasksSortSpec = SortSpec{
	Default: "-assigned_at",
	Keys: map[string]SortKeyKind{
		"assigned_at": SortKeyTimestamp,
	},
}

func workersRowKey(w store.Worker) (anySortVal, pgtype.UUID) {
	return w.CreatedAt.Time, w.ID
}

func workersRowKeyByRevoked(w store.Worker) (anySortVal, pgtype.UUID) {
	if !w.RevokedAt.Valid {
		return (*time.Time)(nil), w.ID
	}
	t := w.RevokedAt.Time
	return &t, w.ID
}

func workersRowKeyByLastSeen(w store.Worker) (anySortVal, pgtype.UUID) {
	if !w.LastSeenAt.Valid {
		return (*time.Time)(nil), w.ID
	}
	t := w.LastSeenAt.Time
	return &t, w.ID
}

// workerTaskResponse is one currently-assigned task. It EMBEDS taskResponse so
// this endpoint cannot drift from GET /v1/tasks/{id} on the task's own fields,
// exactly as disableWorkerResponse embeds workerResponse.
// assignment_epoch is deliberately absent and must stay absent: it is a fence
// token, and this response would otherwise publish live (task id, epoch) pairs
// for a named worker to any authenticated user - both of the values a forged
// task-status update needs, which it would otherwise have to guess.
// TestWorkerTaskResponseDoesNotDeclareAssignmentEpoch and
// TestListWorkerTasks_DoesNotExposeAssignmentEpoch pin the absence.
type workerTaskResponse struct {
	taskResponse
	JobID      string     `json:"job_id"`
	JobName    string     `json:"job_name"`
	AssignedAt *time.Time `json:"assigned_at,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
}

func toWorkerTaskResponse(t store.Task) workerTaskResponse {
	resp := workerTaskResponse{
		taskResponse: toTaskResponse(t, nil),
		JobID:        uuidStr(t.JobID),
	}
	if t.AssignedAt.Valid {
		at := t.AssignedAt.Time
		resp.AssignedAt = &at
	}
	if t.StartedAt.Valid {
		st := t.StartedAt.Time
		resp.StartedAt = &st
	}
	return resp
}

// A nil *time.Time is how encodeCursorV2 represents a NULL sort value, which is
// the NULLS LAST tail of the query's order.
func workerTasksRowKey(t store.Task) (anySortVal, pgtype.UUID) {
	if !t.AssignedAt.Valid {
		return (*time.Time)(nil), t.ID
	}
	at := t.AssignedAt.Time
	return &at, t.ID
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, WorkersSortSpec)
	if !ok {
		return
	}

	total, err := s.q.CountWorkers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count workers failed")
		return
	}

	var items []workerResponse
	var next string

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListWorkersPage(ctx, store.ListWorkersPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKey)

	case "created_at":
		rows, err := s.q.ListWorkersPageByCreatedAsc(ctx, store.ListWorkersPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKey)

	case "-name":
		rows, err := s.q.ListWorkersPageByNameDesc(ctx, store.ListWorkersPageByNameDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, func(w store.Worker) (anySortVal, pgtype.UUID) {
			return w.Name, w.ID
		})

	case "name":
		rows, err := s.q.ListWorkersPageByNameAsc(ctx, store.ListWorkersPageByNameAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, func(w store.Worker) (anySortVal, pgtype.UUID) {
			return w.Name, w.ID
		})

	case "-status":
		rows, err := s.q.ListWorkersPageByStatusDesc(ctx, store.ListWorkersPageByStatusDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, func(w store.Worker) (anySortVal, pgtype.UUID) {
			return w.Status, w.ID
		})

	case "status":
		rows, err := s.q.ListWorkersPageByStatusAsc(ctx, store.ListWorkersPageByStatusAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, func(w store.Worker) (anySortVal, pgtype.UUID) {
			return w.Status, w.ID
		})

	case "-last_seen_at":
		rows, err := s.q.ListWorkersPageByLastSeenDesc(ctx, store.ListWorkersPageByLastSeenDescParams{
			CursorSet:    pp.Cursor.Set,
			CursorIsNull: pp.Cursor.IsNull,
			CursorTs:     pp.CursorTs(),
			CursorID:     pp.Cursor.ID,
			PageLimit:    pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKeyByLastSeen)

	case "last_seen_at":
		rows, err := s.q.ListWorkersPageByLastSeenAsc(ctx, store.ListWorkersPageByLastSeenAscParams{
			CursorSet:    pp.Cursor.Set,
			CursorIsNull: pp.Cursor.IsNull,
			CursorTs:     pp.CursorTs(),
			CursorID:     pp.Cursor.ID,
			PageLimit:    pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKeyByLastSeen)

	default:
		panic("handleListWorkers: missing dispatch arm for sort key " + pp.Sort)
	}

	if s.Metrics != nil {
		for i := range items {
			if at, ok := s.Metrics.LastSampleAt(items[i].ID); ok {
				items[i].LastSampleAt = &at
			}
		}
	}
	writeJSON(w, http.StatusOK, page[workerResponse]{Items: items, NextCursor: next, Total: total})
}

// handleListRevokedWorkers lists workers with status 'revoked' for admin audit.
// Admin-only. Ordered revoked_at DESC NULLS LAST, id DESC. Revoked workers are
// excluded from every other list/stats endpoint; this is the only surface for them.
func (s *Server) handleListRevokedWorkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, RevokedWorkersSortSpec)
	if !ok {
		return
	}

	// This endpoint is DESC-only (the SQL ordering is fixed). The sort spec
	// must list the revoked_at key so the "-revoked_at" default resolves, but
	// an explicit ascending request can't be honored by the fixed query, so
	// reject it rather than silently returning descending rows.
	if pp.Sort != "-revoked_at" {
		writeError(w, http.StatusBadRequest, "revoked workers can only be sorted by -revoked_at")
		return
	}

	total, err := s.q.CountRevokedWorkers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count revoked workers failed")
		return
	}

	rows, err := s.q.ListRevokedWorkersPage(ctx, store.ListRevokedWorkersPageParams{
		CursorSet:    pp.Cursor.Set,
		CursorIsNull: pp.Cursor.IsNull,
		CursorTs:     pp.CursorTs(),
		CursorID:     pp.Cursor.ID,
		PageLimit:    pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list revoked workers failed")
		return
	}
	items, next := buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKeyByRevoked)
	writeJSON(w, http.StatusOK, page[workerResponse]{Items: items, NextCursor: next, Total: total})
}

func (s *Server) handleGetWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	worker, err := s.q.GetWorker(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	resp := toWorkerResponse(worker)
	if s.Metrics != nil {
		if at, ok := s.Metrics.LastSampleAt(uuidStr(worker.ID)); ok {
			resp.LastSampleAt = &at
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleListWorkerTasks lists the tasks CURRENTLY ASSIGNED to one worker, newest
// assignment first. Read-only, and auth-only rather than admin: both neighbouring
// worker reads are auth-only, and this is a projection of task rows keyed by
// worker, so gating it on admin would be stricter than either thing it is made
// of.
//
// The worker is read before the page is built, so an unknown id is a 404 rather
// than an empty list - the same ordering handleGetTaskLogs uses. That read runs
// before parsePage, so an unknown worker with a bad ?limit= is a 404, not a 400.
// A revoked worker is returned by GetWorker and is therefore not a 404 here,
// matching GET /v1/workers/{id}.
//
// items and total come from two statements, so under concurrent dispatch they
// can disagree for an instant.
func (s *Server) handleListWorkerTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	if _, err := s.q.GetWorker(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	pp, ok := parsePage(w, r, WorkerTasksSortSpec)
	if !ok {
		return
	}
	// The SQL ordering is fixed, so an ascending request cannot be honored.
	// Refuse it rather than silently returning descending rows, exactly as
	// handleListRevokedWorkers does.
	if pp.Sort != "-assigned_at" {
		writeError(w, http.StatusBadRequest, "worker tasks can only be sorted by -assigned_at")
		return
	}

	total, err := s.q.CountActiveTasksForWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count worker tasks failed")
		return
	}

	rows, err := s.q.ListActiveTasksForWorkerPage(ctx, store.ListActiveTasksForWorkerPageParams{
		WorkerID:     id,
		CursorSet:    pp.Cursor.Set,
		CursorIsNull: pp.Cursor.IsNull,
		CursorTs:     pp.CursorTs(),
		CursorID:     pp.Cursor.ID,
		PageLimit:    pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list worker tasks failed")
		return
	}

	items, next := buildPage(rows, pp.Limit, pp.Sort, toWorkerTaskResponse, workerTasksRowKey)
	if err := s.fillJobNames(ctx, items); err != nil {
		log.Printf("list worker tasks: fill job names: %v", err)
		writeError(w, http.StatusInternalServerError, "list worker tasks failed")
		return
	}
	writeJSON(w, http.StatusOK, page[workerTaskResponse]{Items: items, NextCursor: next, Total: total})
}

// fillJobNames resolves job_name for one page of tasks in a single lookup on the
// jobs primary key, bounded by the page limit. It is a second statement, not a
// JOIN; see ListActiveTasksForWorkerPage.
// tasks.job_id is NOT NULL, so a missing name is not a normal absence. The
// reachable cause is a concurrent DeleteJob cascading to tasks (tasks.job_id
// ... ON DELETE CASCADE, migration 000001) between the list statement and this
// one. That is an error rather than an empty string because a blank job on a
// task reads as data, not as a row that vanished mid-request.
func (s *Server) fillJobNames(ctx context.Context, items []workerTaskResponse) error {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	ids := make([]pgtype.UUID, 0, len(items))
	for _, it := range items {
		if _, ok := seen[it.JobID]; ok {
			continue
		}
		seen[it.JobID] = struct{}{}
		id, err := parseUUID(it.JobID)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	rows, err := s.q.GetJobNamesByIDs(ctx, ids)
	if err != nil {
		return err
	}
	nameByID := make(map[string]string, len(rows))
	for _, row := range rows {
		nameByID[uuidStr(row.ID)] = row.Name
	}
	for i := range items {
		name, ok := nameByID[items[i].JobID]
		if !ok {
			return fmt.Errorf("no job name for task %s (job %s)", items[i].ID, items[i].JobID)
		}
		items[i].JobName = name
	}
	return nil
}

func (s *Server) handleUpdateWorker(w http.ResponseWriter, r *http.Request) {
	// Note: this is a read-modify-write without a transaction.
	// Concurrent PATCH requests could race; acceptable for v1 admin operations.
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	current, err := s.q.GetWorker(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	var body struct {
		Name     *string           `json:"name"`
		Labels   map[string]string `json:"labels"`
		MaxSlots *int32            `json:"max_slots"`
	}
	if !readJSON(w, r, &body) {
		return
	}

	// Merge with current values
	name := current.Name
	if body.Name != nil {
		name = *body.Name
	}

	labelsJSON := current.Labels
	if body.Labels != nil {
		labelsJSON, err = json.Marshal(body.Labels)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal labels failed")
			return
		}
	}

	maxSlots := current.MaxSlots
	if body.MaxSlots != nil {
		maxSlots = *body.MaxSlots
	}

	updated, err := s.q.UpdateWorker(ctx, store.UpdateWorkerParams{
		ID:       id,
		Name:     name,
		Labels:   labelsJSON,
		MaxSlots: maxSlots,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update worker failed")
		return
	}

	writeJSON(w, http.StatusOK, toWorkerResponse(updated))
}

func (s *Server) handleDisableWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}
	requeue, _ := strconv.ParseBool(r.URL.Query().Get("requeue"))

	current, err := s.q.GetWorker(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	// Already disabled: no-op. Do not re-stamp disabled_at or re-cancel tasks.
	if current.DisabledAt.Valid {
		writeJSON(w, http.StatusOK, disableWorkerResponse{
			workerResponse: toWorkerResponse(current),
		})
		return
	}

	var requeuedIDs []pgtype.UUID
	if requeue {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		defer tx.Rollback(ctx)
		q := s.q.WithTx(tx)

		// Set disabled_at first so a dispatcher woken by NotifyTaskSubmitted
		// already sees the worker as ineligible and won't re-dispatch to it.
		// The :execrows count is the atomic check-and-set: a zero count means a
		// concurrent request disabled the worker first, so roll back and return
		// the no-op response rather than requeueing tasks it already handled.
		n, err := q.DisableWorker(ctx, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "disable worker failed")
			return
		}
		if n == 0 {
			_ = tx.Rollback(ctx)
			refreshed, err := s.q.GetWorker(ctx, id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "db error")
				return
			}
			writeJSON(w, http.StatusOK, disableWorkerResponse{
				workerResponse: toWorkerResponse(refreshed),
			})
			return
		}
		requeuedIDs, err = q.RequeueWorkerTasks(ctx, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "requeue tasks failed")
			return
		}
		if err := q.NotifyTaskSubmitted(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}

		// Tell the still-connected agent to kill the now-orphaned subprocesses.
		// Best-effort: a failed send just means the agent already lost the task.
		cancels := make([]cancelSignal, 0, len(requeuedIDs))
		for _, tid := range requeuedIDs {
			cancels = append(cancels, cancelSignal{
				workerID: uuidStr(id),
				taskID:   uuidStr(tid),
				force:    false,
			})
		}
		s.sendCancelSignals(cancels)
	} else {
		if _, err := s.q.DisableWorker(ctx, id); err != nil {
			writeError(w, http.StatusInternalServerError, "disable worker failed")
			return
		}
	}

	updated, err := s.q.GetWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, disableWorkerResponse{
		workerResponse: toWorkerResponse(updated),
		RequeuedTasks:  len(requeuedIDs),
	})
}

// handleDeleteWorker destroys a worker identity (admin-only). Delete is the only
// verb that frees the hostname: revoke keeps the row, and every enrollment path
// keys on the UNIQUE hostname column.
//
// THE STATEMENT ORDER IS THE CORRECTNESS ARGUMENT, not a style choice. This is
// CLAUDE.md's first invariant in its original wording - end the generation before
// releasing the resource. The generation is tasks.assignment_epoch; the resource
// is the workers row. If the DELETE ran first, the FK's ON DELETE SET NULL would
// null tasks.worker_id with no epoch bump, and the row would then be unreachable
// by every worker-keyed statement in the tree, running forever, holding no slot,
// with its job never leaving 'running'. The requeue would then match zero rows
// and this handler would cheerfully report "requeued_tasks": 0.
func (s *Server) handleDeleteWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	// 1. Lock the worker row FIRST, matching both enrollment transactions' lock
	// order, and read the identity the response and the log line report.
	current, err := q.GetWorkerForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	// 2. The status gate. THE SQL PREDICATE IN DeleteWorker IS THE CONTROL; this
	// is a second question plus a better error (spec 8.2). Because step 1 took
	// FOR UPDATE, the two cannot disagree within this transaction; the SQL arm is
	// defence for a future caller who writes a second delete path without a lock.
	//
	// WRITTEN AS AN ALLOW-LIST, like the SQL. The deny-list is interchangeable
	// today and fails OPEN on the next status added. 'online' and 'stale' both
	// mean CONNECTED; a disabled worker is still 'online' or 'offline'
	// underneath, and this keys on the underlying value, so a
	// disabled-and-connected worker is refused - correct, since disable does not
	// close the stream. Note the consequence for the response body:
	// toWorkerResponse synthesises status "disabled" when disabled_at is set, so
	// a disabled-and-offline worker's own delete response reports "disabled",
	// not the "offline" this gate matched on.
	switch current.Status {
	case "offline", "revoked":
	default:
		writeError(w, http.StatusConflict,
			"worker is connected; disable it and wait for it to go offline before deleting. "+
				"Revoking does NOT disconnect it - it only clears the credential, and a revoked "+
				"worker may still be connected and running tasks")
		return
	}

	// 3. End every assignment generation while worker_id still names them.
	requeued, err := q.RequeueWorkerTasks(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "requeue tasks failed")
		return
	}

	// 4. Break the enrollment link. Must precede the DELETE or the no-action FK
	// fires; that FK is deliberately not ON DELETE SET NULL (spec 5).
	unlinked, err := q.ClearEnrollmentConsumerForWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unlink enrollments failed")
		return
	}

	// 5. Scrub the id out of reservations naming it. NOT a dispatch fix (spec 7).
	//
	// ITS POSITION HERE IS CONVENTION, NOT NECESSITY, and the spec and the plan
	// both said otherwise ("before the DELETE because after it there is no id to
	// scrub by"). That reasoning is self-refuting: reservations.worker_ids is a
	// bare UUID[] with NO foreign key (000001_initial.up.sql:89), which is the
	// entire reason this statement has to exist - and it is equally the reason
	// the DELETE does not disturb the array. The id lives in `id` either way.
	// Verified by mutation: moving this call after DeleteWorker leaves every
	// delete test green. Steps 3 and 6 are the pair whose order IS load-bearing.
	scrubbed, err := q.RemoveWorkerFromReservations(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scrub reservations failed")
		return
	}

	// 6. Count what the DELETE is about to de-attribute. Must be read BEFORE the
	// DELETE, because afterwards there is no worker_id left to count by - unlike
	// the reservation scrub above, this one really does depend on running first.
	attributionCleared, err := q.CountTerminalTasksForWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count task attribution failed")
		return
	}

	// 7. Release the resource. :execrows, and the zero case is handled rather
	// than assumed - Task 6 turns it into the 409 it should be.
	n, err := q.DeleteWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete worker failed")
		return
	}
	if n == 0 {
		// A zero-row delete after a FOR UPDATE read that said the status was
		// permitted means something is wrong - most plausibly a concurrent
		// delete. Roll back and refuse; NEVER report success. Keep "the fence
		// said no" distinguishable from "the query failed", per markWorkerOffline.
		//
		// THIS BRANCH IS UNREACHABLE BY CONSTRUCTION AND IT IS NOT DEAD CODE.
		// DO NOT DELETE IT. Step 1 took the row FOR UPDATE and step 2 read the
		// status off that locked row, so within this transaction the Go gate and
		// DeleteWorker's SQL allow-list cannot disagree - which means no
		// deterministic test can drive n == 0, and none does. Spec 13.2 (T-D4)
		// declined to write one rather than build a flaky concurrency test, and
		// proposed mutation M8 as the stand-in; M8 was RUN and SURVIVED, for this
		// same reason, so the property is genuinely untested. It is kept because
		// the two arms ARE separable - a future caller who adds a second delete
		// path without the lock, or who drops the FOR UPDATE (mutation M12,
		// declared unkillable), makes this reachable immediately - and the cost of
		// being wrong is reporting a destruction that did not happen.
		writeError(w, http.StatusConflict, "worker was modified concurrently; retry")
		return
	}

	// 8. Wake the dispatcher so requeued tasks are placed promptly; skipped when
	// nothing moved, to avoid a spurious cycle (same as handleDisableWorker).
	if len(requeued) > 0 {
		if err := q.NotifyTaskSubmitted(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// ONE UNBUDGETED LOG LINE, and the budget question is answered rather than
	// skipped: this site is reachable only by an authenticated admin, fires once
	// per successful delete of a row that then ceases to exist, and cannot be
	// driven by an unauthenticated peer. No counter, no new counters section, no
	// new logKind. No line on refusal: a refusal changes nothing and the caller
	// reads the 409 directly.
	// hostname is PEER-SUPPLIED and unbounded: workers.hostname is bare TEXT and
	// auto-enroll takes it off the wire unvalidated, so both log defences are
	// wanted. %q escapes it (no newline injection into the log), and the .200
	// precision clips it. This is not internal/worker's clipID: that helper is
	// unexported there and its constant is the ingest-log budget's policy, which
	// is a different question from this one, so the bound is stated here instead
	// of coupling two unrelated policies. Volume needs no defence - one line per
	// successful delete of a row that then ceases to exist, admin-gated.
	log.Printf("api: worker deleted: id=%s hostname=%.200q requeued_tasks=%d reservations_updated=%d enrollments_unlinked=%d attribution_cleared=%d",
		uuidStr(id), current.Hostname, len(requeued), scrubbed, unlinked, attributionCleared)

	// TELL THE AGENT, IF THERE IS ONE. The allow-list's two members are NOT
	// equivalent here and the original version of this comment got that wrong:
	// 'offline' does imply disconnected, but 'revoked' DOES NOT.
	// handleDeleteWorkerToken is a single ClearWorkerAgentToken - it does not
	// close the stream, unregister the sender, or requeue anything - and the
	// liveness sweeper only moves online <-> stale, so revoked-and-connected is a
	// STABLE state rather than a narrow window. Without this the requeue above
	// hands a still-executing task to a second worker and nobody tells the first,
	// which is a duplicate side effect (a p4 submit, a shared output path) that is
	// INVISIBLE in the task record because the original agent's writes are
	// correctly fenced away by the epoch bump.
	//
	// No branch on status is needed: Registry.Send on an unregistered id is one
	// map lookup returning an error that this best-effort path discards, so the
	// 'offline' arm costs nothing and cannot imply a connection. Routed through
	// sendCancelSignals so the sends stay bounded (the one-bounded-sender invariant), exactly
	// as handleDisableWorker does it.
	cancels := make([]cancelSignal, 0, len(requeued))
	for _, tid := range requeued {
		cancels = append(cancels, cancelSignal{
			workerID: uuidStr(id),
			taskID:   uuidStr(tid),
			force:    false,
		})
	}
	s.sendCancelSignals(cancels)

	writeJSON(w, http.StatusOK, deleteWorkerResponse{
		workerResponse:      toWorkerResponse(current),
		RequeuedTasks:       len(requeued),
		ReservationsUpdated: int(scrubbed),
		EnrollmentsUnlinked: int(unlinked),
		AttributionCleared:  int(attributionCleared),
	})
}

func (s *Server) handleEnableWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	if _, err := s.q.GetWorker(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	n, err := s.q.EnableWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "enable worker failed")
		return
	}
	// Wake the dispatcher so the re-enabled worker can pick up pending tasks
	// immediately. Skip the notify when the worker was already enabled (n == 0)
	// to avoid a spurious dispatch cycle.
	if n > 0 {
		if err := s.q.NotifyTaskSubmitted(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	updated, err := s.q.GetWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, toWorkerResponse(updated))
}
